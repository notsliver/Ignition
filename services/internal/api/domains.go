package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/proxy"
	"github.com/notsliver/Ignition/services/internal/store"
)

var hostnameRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// normalizeHostname accepts what people paste (a URL, trailing dot, capitals) and returns the bare hostname,
// or a message explaining why it can't be used.
func normalizeHostname(raw string) (string, string) {
	h := strings.ToLower(strings.TrimSpace(raw))
	h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	h = strings.TrimSuffix(h, ".")
	switch {
	case strings.HasPrefix(h, "*."):
		return "", "Wildcard domains aren't supported yet. Add each hostname separately."
	case net.ParseIP(h) != nil:
		return "", "Enter a domain name, not an IP address."
	case strings.Contains(h, ":"):
		return "", "Leave out the port. Domains are served on the proxy's ports."
	case h == "localhost":
		return "", "Use a name under localhost, like app.localhost."
	case len(h) > 253 || !hostnameRE.MatchString(h):
		return "", "Enter a domain like app.example.com, or app.localhost for this machine only."
	}
	return h, ""
}

func (s *Server) syncProxy(ctx context.Context) {
	routes, err := s.Store.ProxyRoutes(ctx)
	if err == nil {
		_, err = s.Proxy.Sync(ctx, routes, proxy.LoadSettings(ctx, s.Store))
	}
	if err != nil {
		slog.Warn("proxy sync failed", "error", err)
	}
}

func (s *Server) checkDomain(ctx context.Context, d *store.Domain) *store.Domain {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	s.Store.SetDomainCheck(ctx, d.ID, s.Proxy.Check(ctx, *d))
	fresh, err := s.Store.GetDomain(ctx, d.ID)
	if err != nil {
		return d
	}
	return fresh
}

func (s *Server) listDomains(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	if _, err := s.Store.GetResource(r.Context(), id); err != nil {
		return notFound(err, "Resource")
	}
	list, err := s.Store.ListDomains(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	providers, _ := s.Store.DNSProviders(r.Context())
	pv := []map[string]any{}
	for _, p := range providers {
		pv = append(pv, map[string]any{"id": p.ID, "name": p.Name, "kind": p.Kind, "zones": p.Zones})
	}
	httpx.JSON(w, 200, map[string]any{"domains": list, "dnsProviders": pv, "proxy": map[string]any{
		"running": s.Proxy.Running(ctx), "httpPort": s.Proxy.HTTPPort, "httpsPort": s.Proxy.HTTPSPort,
	}})
	return nil
}

func (s *Server) createDomain(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	var in struct {
		Hostname     string   `json:"hostname"`
		Port         *int     `json:"port"`
		HTTPS        *bool    `json:"https"`
		PathPrefix   string   `json:"pathPrefix"`
		StripPrefix  bool     `json:"stripPrefix"`
		RedirectTo   string   `json:"redirectTo"`
		RedirectCode int      `json:"redirectCode"`
		AllowIPs     []string `json:"allowIps"`
		DNSProvider  string   `json:"dnsProviderId"` // create the DNS records through this integration
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if res.Type != "application" {
		return httpx.Invalid(map[string]string{"hostname": "Domains are for applications that serve HTTP."})
	}
	host, problem := normalizeHostname(in.Hostname)
	if problem != "" {
		return httpx.Invalid(map[string]string{"hostname": problem})
	}
	if in.Port != nil && (*in.Port < 1 || *in.Port > 65535) {
		return httpx.Invalid(map[string]string{"port": "Ports are numbers from 1 to 65535."})
	}
	f := map[string]string{}
	path, msg := normalizePath(in.PathPrefix)
	if msg != "" {
		f["pathPrefix"] = msg
	}
	redirect, msg := validateRedirect(in.RedirectTo)
	if msg != "" {
		f["redirectTo"] = msg
	}
	if in.RedirectCode != 0 && in.RedirectCode != 301 && in.RedirectCode != 302 && in.RedirectCode != 307 && in.RedirectCode != 308 {
		f["redirectCode"] = "301, 302, 307 or 308."
	}
	allow, msg := validateAllowIPs(in.AllowIPs)
	if msg != "" {
		f["allowIps"] = msg
	}
	if redirect == "" && in.Port == nil && res.Port == nil {
		f["port"] = res.Name + " has no port. Enter the port your app listens on."
	}
	if in.StripPrefix && path == "" {
		f["stripPrefix"] = "Only routes with a path prefix can strip it."
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	https := true
	if in.HTTPS != nil {
		https = *in.HTTPS
	}
	token := s.hostnameToken(r.Context(), host)
	if token == "" {
		raw := make([]byte, 16)
		rand.Read(raw)
		token = hex.EncodeToString(raw)
	}
	domainID, err := s.Store.CreateDomain(r.Context(), id, store.NewDomain{Hostname: host, PathPrefix: path, RedirectTo: redirect, Token: token, Port: in.Port,
		HTTPS: https, StripPrefix: in.StripPrefix, RedirectCode: in.RedirectCode, AllowIPs: allow}, u.ID)
	if store.IsUnique(err) {
		if path == "" {
			return httpx.Invalid(map[string]string{"hostname": host + " is already routed. Add a path prefix to route part of the site elsewhere."})
		}
		return httpx.Invalid(map[string]string{"pathPrefix": host + path + " is already routed."})
	}
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "domain.create", "resource", id, map[string]any{"hostname": host, "path": path, "redirect": redirect}, clientIP(r))
	s.syncProxy(r.Context())
	d, err := s.Store.GetDomain(r.Context(), domainID)
	if err != nil {
		return err
	}
	if in.DNSProvider != "" {
		if err := s.createDNSRecords(r.Context(), d, in.DNSProvider); err != nil {
			httpx.JSON(w, 201, map[string]any{"domain": s.checkDomain(r.Context(), d), "dnsError": err.Error()})
			return nil
		}
	}
	httpx.JSON(w, 201, map[string]any{"domain": s.checkDomain(r.Context(), d)})
	return nil
}

func (s *Server) domainFor(r *http.Request) (*store.Domain, error) {
	id, err := pathID(r, "Domain")
	if err != nil {
		return nil, err
	}
	d, err := s.Store.GetDomain(r.Context(), id)
	if err != nil {
		return nil, notFound(err, "Domain")
	}
	return d, nil
}

func (s *Server) updateDomain(w http.ResponseWriter, r *http.Request, u *store.User) error {
	d, err := s.domainFor(r)
	if err != nil {
		return err
	}
	var in struct {
		HTTPS        *bool     `json:"https"`
		Port         *int      `json:"port"`
		Primary      bool      `json:"primary"`
		StripPrefix  *bool     `json:"stripPrefix"`
		RedirectTo   *string   `json:"redirectTo"`
		RedirectCode *int      `json:"redirectCode"`
		AllowIPs     *[]string `json:"allowIps"`
		// UseResourcePort clears a custom port so the domain follows the resource's port.
		UseResourcePort bool `json:"useResourcePort"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.Port != nil && (*in.Port < 1 || *in.Port > 65535) {
		return httpx.Invalid(map[string]string{"port": "Ports are numbers from 1 to 65535."})
	}
	if in.UseResourcePort {
		if res, err := s.Store.GetResource(r.Context(), d.ResourceID); err == nil && res.Port == nil {
			return httpx.Invalid(map[string]string{"port": res.Name + " has no port of its own, so this domain needs one."})
		}
	}
	patch := store.DomainPatch{HTTPS: in.HTTPS, Port: in.Port, ClearPort: in.UseResourcePort, Primary: in.Primary, StripPrefix: in.StripPrefix, RedirectCode: in.RedirectCode}
	f := map[string]string{}
	if in.RedirectTo != nil {
		v, msg := validateRedirect(*in.RedirectTo)
		if msg != "" {
			f["redirectTo"] = msg
		}
		patch.RedirectTo = &v
	}
	if in.RedirectCode != nil && *in.RedirectCode != 301 && *in.RedirectCode != 302 && *in.RedirectCode != 307 && *in.RedirectCode != 308 {
		f["redirectCode"] = "301, 302, 307 or 308."
	}
	if in.AllowIPs != nil {
		v, msg := validateAllowIPs(*in.AllowIPs)
		if msg != "" {
			f["allowIps"] = msg
		}
		patch.AllowIPs = &v
	}
	if in.StripPrefix != nil && *in.StripPrefix && d.PathPrefix == "" {
		f["stripPrefix"] = "Only routes with a path prefix can strip it."
	}
	if in.Primary && (d.PathPrefix != "" || d.RedirectTo != "") {
		f["primary"] = "The primary domain is a whole-site route, not a path or redirect."
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	if err := s.Store.UpdateDomain(r.Context(), d.ID, patch); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "domain.update", "resource", d.ResourceID, map[string]any{"hostname": d.Hostname}, clientIP(r))
	s.syncProxy(r.Context())
	if d, err = s.Store.GetDomain(r.Context(), d.ID); err != nil {
		return err
	}
	if in.HTTPS != nil {
		d = s.checkDomain(r.Context(), d)
	}
	httpx.JSON(w, 200, map[string]any{"domain": d})
	return nil
}

func (s *Server) deleteDomain(w http.ResponseWriter, r *http.Request, u *store.User) error {
	d, err := s.domainFor(r)
	if err != nil {
		return err
	}
	if r.URL.Query().Get("dns") == "1" && d.DNSProviderID != nil {
		if err := s.deleteDNSRecords(r.Context(), d); err != nil {
			return httpx.Unavailable("Couldn't remove the DNS records: " + err.Error() + ". The domain was kept; try again, or delete it without removing DNS.")
		}
	}
	if err := s.Store.DeleteDomain(r.Context(), d.ID); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "domain.delete", "resource", d.ResourceID, map[string]any{"hostname": d.Hostname}, clientIP(r))
	s.syncProxy(r.Context())
	httpx.JSON(w, 204, nil)
	return nil
}

func (s *Server) checkDomainHandler(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	d, err := s.domainFor(r)
	if err != nil {
		return err
	}
	s.syncProxy(r.Context())
	httpx.JSON(w, 200, map[string]any{"domain": s.checkDomain(r.Context(), d)})
	return nil
}

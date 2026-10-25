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

	"github.com/ignition/ignition/internal/httpx"
	"github.com/ignition/ignition/internal/store"
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
		_, err = s.Proxy.Sync(ctx, routes)
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
	httpx.JSON(w, 200, map[string]any{"domains": list, "proxy": map[string]any{
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
		Hostname string `json:"hostname"`
		Port     *int   `json:"port"`
		HTTPS    *bool  `json:"https"`
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
	if in.Port == nil && res.Port == nil {
		return httpx.Invalid(map[string]string{"port": res.Name + " has no port. Enter the port your app listens on."})
	}
	https := true
	if in.HTTPS != nil {
		https = *in.HTTPS
	}
	token := make([]byte, 16)
	rand.Read(token)
	domainID, err := s.Store.CreateDomain(r.Context(), id, host, in.Port, https, hex.EncodeToString(token), u.ID)
	if store.IsUnique(err) {
		return httpx.Invalid(map[string]string{"hostname": host + " is already used by a resource on this server."})
	}
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "domain.create", "resource", id, map[string]any{"hostname": host}, clientIP(r))
	s.syncProxy(r.Context())
	d, err := s.Store.GetDomain(r.Context(), domainID)
	if err != nil {
		return err
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
		HTTPS   *bool `json:"https"`
		Port    *int  `json:"port"`
		Primary bool  `json:"primary"`
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
	if err := s.Store.UpdateDomain(r.Context(), d.ID, store.DomainPatch{HTTPS: in.HTTPS, Port: in.Port, ClearPort: in.UseResourcePort, Primary: in.Primary}); err != nil {
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

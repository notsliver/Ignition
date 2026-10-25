// Package proxy drives the bundled Caddy server that routes custom domains to resources.
//
// Caddy's admin API listens on a unix socket shared only with the api and worker containers,
// so the apps Ignition runs (which share a network with Caddy) can't reconfigure it.
// The whole config is generated from the database and replaced atomically with POST /load.
package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ignition/ignition/internal/store"
)

const (
	CheckPath = "/.well-known/ignition-check"
	origin    = "ignition-proxy" // Host header for admin requests; must match "origins" below
)

type Client struct {
	Socket    string // path to Caddy's admin socket
	HTTPPort  int    // host ports the proxy is published on, used in redirects and links
	HTTPSPort int
	// Addr is how this container reaches the proxy's listeners (the compose service name).
	Addr string
	http *http.Client
}

func New(socket, addr string, httpPort, httpsPort int) *Client {
	return &Client{Socket: socket, Addr: addr, HTTPPort: httpPort, HTTPSPort: httpsPort, http: &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}},
	}}
}

// AdminConfig is the admin block every config must carry, or Caddy falls back to a TCP listener.
func AdminConfig(socket string) map[string]any {
	return map[string]any{"listen": "unix/" + socket, "origins": []string{origin}, "config": map[string]any{"persist": false}}
}

type obj = map[string]any

// Build turns routes into a Caddy config and a short hash identifying it.
func (c *Client) Build(routes []store.ProxyRoute) ([]byte, string) {
	var handlers []any
	var skip []string
	for _, r := range routes {
		host := []string{r.Hostname}
		// 1. Ownership check, answered over plain HTTP so it works before a certificate exists.
		handlers = append(handlers, obj{"match": []any{obj{"host": host, "path": []string{CheckPath}}},
			"handle": []any{obj{"handler": "static_response", "status_code": 200, "body": r.Token}}, "terminal": true})
		if r.HTTPS {
			loc := "https://{http.request.host}"
			if c.HTTPSPort != 443 {
				loc += ":" + strconv.Itoa(c.HTTPSPort)
			}
			handlers = append(handlers, obj{"match": []any{obj{"host": host, "protocol": "http"}},
				"handle": []any{obj{"handler": "static_response", "status_code": 308, "headers": obj{"Location": []string{loc + "{http.request.uri}"}}}}, "terminal": true})
		} else {
			skip = append(skip, r.Hostname)
		}
		var handle any
		switch {
		case r.Port == 0:
			handle = unavailable(503, page{Status: "No port", Tone: "warn", Title: r.Resource + " has no port to route to",
				Message: "Ignition doesn't know which port this app listens on, so " + r.Hostname + " can't reach it yet.",
				Hint:    "Run this app? Set its port under Networking in your Ignition dashboard."})
		case r.ContainerID == "":
			handle = unavailable(503, page{Status: "Stopped", Tone: "dot", Title: r.Resource + " isn’t running right now",
				Message: "This app is stopped, so " + r.Hostname + " has nothing to show yet. It will load here as soon as it’s back.",
				Hint:    "Run this app? Start or deploy <code>" + html.EscapeString(r.Resource) + "</code> from your Ignition dashboard.", RawHint: true})
		default:
			// Docker adds the short container ID as a DNS alias on user-defined networks.
			handle = obj{"handler": "reverse_proxy", "upstreams": []any{obj{"dial": shortID(r.ContainerID) + ":" + strconv.Itoa(r.Port)}}}
		}
		handlers = append(handlers, obj{"match": []any{obj{"host": host}}, "handle": []any{handle}, "terminal": true})
	}
	// Anything else that reaches the proxy gets an explanation instead of an empty response.
	handlers = append(handlers, obj{"handle": []any{unavailable(404, page{Status: "Not set up", Tone: "dot", Title: "This domain isn’t set up",
		Message: "This Ignition server doesn’t have an app for this address.",
		Hint:    "Run this server? Add the domain to an app under Domains in your Ignition dashboard."})}})
	server := obj{"listen": []string{":80", ":443"}, "routes": handlers, "automatic_https": obj{"disable_redirects": true, "skip": nonNil(skip)}}
	// The local CA signs certificates for *.localhost; installing its root into the container's trust store is pointless.
	body := obj{"apps": obj{"http": obj{"servers": obj{"ignition": server}}, "pki": obj{"certificate_authorities": obj{"local": obj{"install_trust": false}}}}}
	raw, _ := json.Marshal(body)
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:6])
	server["@id"] = "ignition-" + hash
	body["admin"] = AdminConfig(c.Socket)
	raw, _ = json.Marshal(body)
	return raw, hash
}

//go:embed unavailable.html
var pageTemplate string

//go:embed logo.svg
var logoSVG string

// page fills unavailable.html. Text is escaped; Hint may contain markup only when RawHint is set.
type page struct {
	Status, Tone, Title, Message, Hint string
	RawHint                            bool
}

// The real logo, drawn in the text colour so it shows on light and dark backgrounds.
var logo = strings.NewReplacer(
	`<svg xmlns="http://www.w3.org/2000/svg"`, `<svg class="logo" aria-hidden="true" xmlns="http://www.w3.org/2000/svg"`,
	` role="img" aria-label="Ignition lightning logo"`, "",
	`fill="#FFFFFF"`, `fill="currentColor"`,
).Replace(strings.TrimSpace(logoSVG))

func (p page) render() string {
	hint := p.Hint
	if !p.RawHint {
		hint = html.EscapeString(hint)
	}
	return strings.NewReplacer(
		"{{TITLE}}", html.EscapeString(p.Title), "{{STATUS}}", html.EscapeString(p.Status), "{{TONE}}", p.Tone,
		"{{MESSAGE}}", html.EscapeString(p.Message), "{{HINT}}", hint, "{{LOGO}}", logo,
	).Replace(pageTemplate)
}

func unavailable(code int, p page) obj {
	return obj{"handler": "static_response", "status_code": code, "body": p.render(), "headers": obj{
		"Content-Type": []string{"text/html; charset=utf-8"}, "Cache-Control": []string{"no-store"},
	}}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// Sync loads the config unless Caddy already runs this exact one. It reports whether it changed anything.
func (c *Client) Sync(ctx context.Context, routes []store.ProxyRoute) (bool, error) {
	raw, hash := c.Build(routes)
	if res, err := c.admin(ctx, http.MethodGet, "/id/ignition-"+hash, nil); err == nil && res == http.StatusOK {
		return false, nil
	}
	code, err := c.admin(ctx, http.MethodPost, "/load", raw)
	if err != nil {
		return false, err
	}
	if code != http.StatusOK {
		return false, fmt.Errorf("proxy rejected the config (HTTP %d)", code)
	}
	return true, nil
}

// Running reports whether the proxy's admin socket answers.
func (c *Client) Running(ctx context.Context) bool {
	code, err := c.admin(ctx, http.MethodGet, "/config/admin", nil)
	return err == nil && code == http.StatusOK
}

func (c *Client) admin(ctx context.Context, method, path string, body []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://"+origin+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return 0, errors.New("the proxy isn't running")
	}
	defer res.Body.Close()
	io.Copy(io.Discard, res.Body)
	return res.StatusCode, nil
}

/* --------------------------------- Checks --------------------------------- */

// IsLocal reports names that always mean "this machine" (RFC 6761), so they need no DNS.
func IsLocal(host string) bool {
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}

// Resolve looks the hostname up in public DNS (via the container's resolver).
func Resolve(ctx context.Context, host string) ([]string, error) {
	if IsLocal(host) {
		return []string{"127.0.0.1"}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && (dnsErr.IsNotFound || dnsErr.IsTemporary) {
		return nil, errors.New("No DNS record found for " + host)
	}
	return addrs, err
}

// Probe requests the ownership token over HTTP. addr is host:port to connect to; the Host header is the domain.
func Probe(ctx context.Context, addr, host, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+CheckPath, nil)
	req.Host = host
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't connect to %s", addr)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 256))
	if res.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != token {
		return fmt.Errorf("%s answered, but it isn't this Ignition server (HTTP %d)", addr, res.StatusCode)
	}
	return nil
}

// Certificate reads the certificate the proxy serves for host. issuer is empty when there's none yet.
func Certificate(ctx context.Context, addr, host string) (issuer string, expires time.Time, err error) {
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: &tls.Config{ServerName: host, InsecureSkipVerify: true}} // inspecting, not trusting
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", time.Time{}, err
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", time.Time{}, errors.New("no certificate")
	}
	leaf := certs[0]
	issuer = leaf.Issuer.CommonName
	if len(leaf.Issuer.Organization) > 0 && !strings.Contains(issuer, leaf.Issuer.Organization[0]) {
		issuer = leaf.Issuer.Organization[0] + " " + issuer
	}
	return issuer, leaf.NotAfter, nil
}

// Check finds out what a domain really does right now: DNS, whether it reaches this proxy, and its certificate.
func (c *Client) Check(ctx context.Context, d store.Domain) store.DomainCheck {
	if !c.Running(ctx) {
		return store.DomainCheck{Status: "pending", Message: "The Ignition proxy isn't running, so domains can't be routed. Run the installer again to start it."}
	}
	addrs, err := Resolve(ctx, d.Hostname)
	if err != nil {
		return store.DomainCheck{Status: "dns-missing", Message: err.Error() + ". Add the DNS record, then check again (changes can take a few minutes)."}
	}
	// Names that point at this machine are probed through the proxy directly; others at the address DNS gives.
	target := c.Addr + ":80"
	if !pointsHere(addrs) {
		target = net.JoinHostPort(preferIPv4(addrs), strconv.Itoa(c.HTTPPort))
	}
	if err := Probe(ctx, target, d.Hostname, d.Token); err != nil {
		return store.DomainCheck{Status: "unreachable", Addresses: addrs, Message: fmt.Sprintf("DNS points to %s, but %v. Forward port %d (and %d for HTTPS) to this machine.", strings.Join(addrs, ", "), err, c.HTTPPort, c.HTTPSPort)}
	}
	if !d.HTTPS {
		return store.DomainCheck{Status: "active", Addresses: addrs, Message: "Routing through the Ignition proxy over HTTP."}
	}
	issuer, expires, err := Certificate(ctx, c.Addr+":443", d.Hostname)
	if err != nil {
		msg := "Routing works. Waiting for the certificate"
		if !IsLocal(d.Hostname) {
			msg += "; Let's Encrypt must reach this machine on ports 80 and 443"
		}
		return store.DomainCheck{Status: "pending", Addresses: addrs, Message: msg + "."}
	}
	msg := "HTTPS active."
	if strings.Contains(issuer, "Caddy Local Authority") {
		msg = "HTTPS active with a local certificate. Browsers warn about it unless you trust Caddy's local root certificate."
	}
	return store.DomainCheck{Status: "active", Addresses: addrs, Message: msg, CertIssuer: issuer, CertExpiresAt: &expires}
}

func pointsHere(addrs []string) bool {
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.IsLoopback() {
			return true
		}
	}
	return false
}

func preferIPv4(addrs []string) string {
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			return a
		}
	}
	return addrs[0]
}

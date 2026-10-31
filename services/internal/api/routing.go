package api

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

/*
 * Traffic routing on domains: a hostname can send different path prefixes to different
 * resources, redirect instead of proxying, and only let certain addresses through.
 */

var pathRE = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@/-]*$`)

// normalizePath turns "api", "/api/", "/api" into "/api"; "" and "/" mean the whole site.
func normalizePath(raw string) (string, string) {
	p := strings.TrimSpace(raw)
	if p == "" || p == "/" {
		return "", ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = strings.TrimRight(p, "/")
	switch {
	case strings.ContainsAny(p, "*?#"):
		return "", "Use a plain prefix like /api; wildcards and query strings aren't supported."
	case len(p) > 200 || !pathRE.MatchString(p):
		return "", "Use a path like /api or /docs/v2."
	}
	return p, ""
}

// validateRedirect accepts an absolute http(s) URL; {path} keeps the requested path.
func validateRedirect(raw string) (string, string) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", ""
	}
	u, err := url.Parse(strings.ReplaceAll(v, "{path}", ""))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", "Enter a full URL like https://example.com{path}."
	}
	if len(v) > 500 {
		return "", "Up to 500 characters."
	}
	return v, ""
}

// validateAllowIPs accepts IPs and CIDR ranges, normalized.
func validateAllowIPs(list []string) ([]string, string) {
	out := []string{}
	for _, raw := range list {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if ip := net.ParseIP(v); ip != nil {
			out = append(out, ip.String())
			continue
		}
		_, n, err := net.ParseCIDR(v)
		if err != nil {
			return nil, fmt.Sprintf("%q isn't an IP address or range like 203.0.113.0/24.", v)
		}
		out = append(out, n.String())
	}
	if len(out) > 50 {
		return nil, "Up to 50 addresses or ranges."
	}
	return out, ""
}

// hostnameToken reuses the ownership token of a hostname that already has routes, since the
// proxy answers the ownership check once per hostname.
func (s *Server) hostnameToken(ctx context.Context, host string) string {
	var tok string
	s.Store.DB.QueryRow(ctx, `SELECT token FROM domains WHERE hostname = $1 LIMIT 1`, host).Scan(&tok)
	return tok
}

package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Authentication & Security. One policy, stored under the "security" setting and enforced where
 * it applies: sign-in (methods, lockout, rate limit, MFA), passwords (length, breach check),
 * sessions (lifetime, idle timeout, revocation), invitations, the network (trusted proxies,
 * allowed origins, IP allow list) and log output (secret redaction). Saving refuses any change
 * that would lock out the person saving it.
 */

type securityPolicy struct {
	LocalAuth           bool     `json:"localAuth"`
	DisableRegistration bool     `json:"disableRegistration"` // invitations can't be accepted with a password
	OIDCAuth            bool     `json:"oidcAuth"`
	RequiredMethod      string   `json:"requiredMethod"` // any | sso
	EnforceMFA          bool     `json:"enforceMfa"`
	MFADeadline         string   `json:"mfaDeadline"`       // YYYY-MM-DD; until then MFA is only announced. "" = enforced now
	MFADeadlineAction   string   `json:"mfaDeadlineAction"` // after the deadline: enroll (at next sign-in) | suspend
	MFAMessage          string   `json:"mfaMessage"`        // optional note shown in the announcement
	PasswordLength      int      `json:"passwordLength"`
	BreachedPasswords   bool     `json:"breachedPasswords"`
	RateLimit           int      `json:"rateLimit"` // sign-in attempts per IP per minute
	MaxAttempts         int      `json:"maxAttempts"`
	LockoutMinutes      int      `json:"lockoutMinutes"`
	ResetExpiryMinutes  int      `json:"resetExpiryMinutes"`
	Invitations         string   `json:"invitations"` // admins | members | disabled
	SSOProvisioning     bool     `json:"ssoProvisioning"`
	SSODefaultRole      string   `json:"ssoDefaultRole"`
	SessionLifetime     string   `json:"sessionLifetime"` // 24h | 7d | 30d
	IdleTimeout         string   `json:"idleTimeout"`     // 1h | 12h | 24h | never
	RevokeOnRoleChange  bool     `json:"revokeOnRoleChange"`
	TrustedProxies      []string `json:"trustedProxies"`
	AllowedOrigins      []string `json:"allowedOrigins"`
	IPRestrictions      bool     `json:"ipRestrictions"`
	AllowedIPs          []string `json:"allowedIps"`
	LogRedaction        bool     `json:"logRedaction"`
}

// defaultPolicy keeps what Ignition did before these settings existed.
func defaultPolicy() securityPolicy {
	return securityPolicy{
		LocalAuth: true, RequiredMethod: "any", PasswordLength: 12, BreachedPasswords: true, RateLimit: 10, MaxAttempts: 5, LockoutMinutes: 15,
		ResetExpiryMinutes: 1440, Invitations: "admins", MFADeadlineAction: "enroll", SSODefaultRole: "viewer", SessionLifetime: "7d", IdleTimeout: "never", RevokeOnRoleChange: true,
		TrustedProxies: []string{}, AllowedOrigins: []string{}, AllowedIPs: []string{}, LogRedaction: true,
	}
}

var (
	lifetimes    = map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}
	idleTimeouts = map[string]time.Duration{"1h": time.Hour, "12h": 12 * time.Hour, "24h": 24 * time.Hour, "never": 0}
)

func (p securityPolicy) lifetime() time.Duration { return lifetimes[p.SessionLifetime] }

// deadline is the end of the MFA deadline day (UTC), or zero without one.
func (p securityPolicy) deadline() time.Time {
	d, err := time.Parse("2006-01-02", p.MFADeadline)
	if err != nil {
		return time.Time{}
	}
	return d.Add(24*time.Hour - time.Second)
}

// mfaGrace: MFA is enforced but its deadline hasn't passed, so it's announced, not required yet.
func (p securityPolicy) mfaGrace() bool {
	d := p.deadline()
	return p.EnforceMFA && !d.IsZero() && time.Now().Before(d)
}

// mfaRequired: people without a second factor must set one up now.
func (p securityPolicy) mfaRequired() bool { return p.EnforceMFA && !p.mfaGrace() }

// mfaExempt: accounts that only sign in through SSO rely on the provider's second factor.
func mfaExempt(u *store.User) bool           { return u.SSO && !u.HasPassword }
func (p securityPolicy) idle() time.Duration { return idleTimeouts[p.IdleTimeout] }

type policyCache struct {
	mu     sync.RWMutex
	p      *securityPolicy
	loaded time.Time
	nets   struct{ proxies, allowed []*net.IPNet }
}

// policy returns the saved policy, re-read every 15 s so a change on another API replica applies.
func (s *Server) policy() securityPolicy {
	c := &s.security
	c.mu.RLock()
	if c.p != nil && time.Since(c.loaded) < 15*time.Second {
		p := *c.p
		c.mu.RUnlock()
		return p
	}
	c.mu.RUnlock()
	p := defaultPolicy()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Store.GetSetting(ctx, "security", &p); err != nil && c.p != nil {
		return *c.p // keep the last good policy if the database blips
	}
	s.setPolicy(p)
	return p
}

func (s *Server) setPolicy(p securityPolicy) {
	c := &s.security
	c.mu.Lock()
	defer c.mu.Unlock()
	c.p, c.loaded = &p, time.Now()
	c.nets.proxies, _ = parseNets(p.TrustedProxies)
	c.nets.allowed, _ = parseNets(p.AllowedIPs)
	proxies := c.nets.proxies
	trustedProxies.Store(&proxies)
}

func (s *Server) nets() (proxies, allowed []*net.IPNet) {
	s.policy()
	s.security.mu.RLock()
	defer s.security.mu.RUnlock()
	return s.security.nets.proxies, s.security.nets.allowed
}

// parseNets accepts IPs and CIDR ranges ("10.0.0.0/8", "203.0.113.7").
func parseNets(list []string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, raw := range list {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if !strings.Contains(v, "/") {
			ip := net.ParseIP(v)
			if ip == nil {
				return nil, fmt.Errorf("%q isn't an IP address or CIDR range", v)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			v = fmt.Sprintf("%s/%d", ip, bits)
		}
		_, n, err := net.ParseCIDR(v)
		if err != nil {
			return nil, fmt.Errorf("%q isn't an IP address or CIDR range", raw)
		}
		out = append(out, n)
	}
	return out, nil
}

func inNets(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// trustedProxies is read by clientIP on every request; setPolicy replaces it.
var trustedProxies atomic.Pointer[[]*net.IPNet]

// clientIP is the address of the person making the request. X-Forwarded-For is only believed
// when the connection comes from a trusted proxy; then the rightmost untrusted hop is the client.
func clientIP(r *http.Request) string {
	var proxies []*net.IPNet
	if p := trustedProxies.Load(); p != nil {
		proxies = *p
	}
	return clientIPWith(r, proxies)
}

func clientIPWith(r *http.Request, proxies []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil || len(proxies) == 0 || !inNets(ip, proxies) {
		return host
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		h := net.ParseIP(strings.TrimSpace(hops[i]))
		if h == nil {
			break
		}
		if !inNets(h, proxies) {
			return h.String()
		}
	}
	return host
}

// ipAllowList blocks every request from outside the allowed networks, except health checks and
// deploy/automation webhooks (they carry their own secrets and come from Git providers).
func (s *Server) ipAllowList(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := s.policy()
		path := r.URL.Path
		if !p.IPRestrictions || path == "/api/health" || path == "/api/ready" || strings.HasPrefix(path, "/api/hooks/") || strings.HasPrefix(path, "/api/automation-hooks/") {
			next.ServeHTTP(w, r)
			return
		}
		_, allowed := s.nets()
		if ip := net.ParseIP(clientIP(r)); ip == nil || !inNets(ip, allowed) {
			if strings.HasPrefix(path, "/api/") {
				httpx.WriteError(w, r, httpx.Forbidden("Access from your network isn't allowed on this Ignition."))
				return
			}
			http.Error(w, "Access from your network isn't allowed on this Ignition.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// originAllowed: the dashboard's own host, the configured public URL, or an allowed origin.
func (s *Server) originAllowed(o *url.URL, r *http.Request) bool {
	if o.Host == r.Host {
		return true
	}
	if allowed, _ := url.Parse(s.publicURL()); allowed != nil && o.Host == allowed.Host {
		return true
	}
	for _, a := range s.policy().AllowedOrigins {
		if u, err := url.Parse(a); err == nil && u.Host == o.Host && u.Scheme == o.Scheme {
			return true
		}
	}
	return false
}

/* --------------------------------- Endpoints ---------------------------------- */

func (s *Server) getSecurity(w http.ResponseWriter, r *http.Request, u *store.User) error {
	ctx := r.Context()
	events, err := s.Store.SecurityEvents(ctx, 25)
	if err != nil {
		return err
	}
	users, err := s.Store.ListUsers(ctx)
	if err != nil {
		return err
	}
	withMFA, admins, adminsWithMFA := 0, 0, 0
	for _, x := range users {
		if x.Status != "active" {
			continue
		}
		if x.MFAEnabledAt != nil {
			withMFA++
		}
		if x.Role == "admin" || x.Role == "owner" {
			admins++
			if x.MFAEnabledAt != nil {
				adminsWithMFA++
			}
		}
	}
	remote, _, _ := net.SplitHostPort(r.RemoteAddr)
	httpx.JSON(w, 200, map[string]any{
		"policy": s.policy(),
		"status": map[string]any{
			"https":      s.httpsStatus(ctx),
			"encryption": map[string]any{"configured": s.Secrets != nil, "algorithm": "AES-256-GCM", "keySource": "IGNITION_SECRET_KEY"},
			"oidc":       s.oidcStatus(ctx),
			"request":    map[string]any{"clientIp": clientIP(r), "remoteAddr": remote, "forwardedFor": r.Header.Get("X-Forwarded-For")},
			"users":      map[string]int{"active": countActive(users), "withMfa": withMFA, "admins": admins, "adminsWithMfa": adminsWithMFA},
			"you":        map[string]any{"mfa": u.MFAEnabledAt != nil},
		},
		"events": events,
	})
	return nil
}

func countActive(users []store.UserRow) int {
	n := 0
	for _, u := range users {
		if u.Status == "active" {
			n++
		}
	}
	return n
}

func (s *Server) putSecurity(w http.ResponseWriter, r *http.Request, u *store.User) error {
	in := defaultPolicy()
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	f := map[string]string{}
	clean := func(list []string) []string {
		out := []string{}
		for _, v := range list {
			if v = strings.TrimSpace(v); v != "" && !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
		return out
	}
	in.TrustedProxies, in.AllowedOrigins, in.AllowedIPs = clean(in.TrustedProxies), clean(in.AllowedOrigins), clean(in.AllowedIPs)
	oidc := s.oidcConfig(r.Context())
	switch {
	case !in.LocalAuth && !in.OIDCAuth:
		f["localAuth"] = "Keep at least one sign-in method, or nobody can sign in."
	case in.OIDCAuth && !oidc.Configured():
		f["oidcAuth"] = "Connect an identity provider first."
	case !in.LocalAuth && !u.SSO && u.Role != "owner":
		f["localAuth"] = "Your account isn't linked to SSO yet. Sign in with SSO once before turning passwords off."
	}
	if in.RequiredMethod != "any" && in.RequiredMethod != "sso" {
		f["requiredMethod"] = "Choose a method."
	} else if in.RequiredMethod == "sso" && !in.OIDCAuth {
		f["requiredMethod"] = "Turn on OpenID Connect before requiring it."
	}
	if in.MFADeadline != "" {
		if _, err := time.Parse("2006-01-02", in.MFADeadline); err != nil {
			f["mfaDeadline"] = "Pick a date."
		}
	}
	if in.MFADeadlineAction != "enroll" && in.MFADeadlineAction != "suspend" {
		f["mfaDeadlineAction"] = "Choose what happens after the deadline."
	}
	in.MFAMessage = strings.TrimSpace(in.MFAMessage)
	if len(in.MFAMessage) > 300 {
		f["mfaMessage"] = "Up to 300 characters."
	}
	if !slices.Contains([]int{8, 12, 16}, in.PasswordLength) {
		f["passwordLength"] = "8, 12 or 16 characters."
	}
	if !slices.Contains([]int{5, 10, 30}, in.RateLimit) {
		f["rateLimit"] = "Choose a limit."
	}
	if !slices.Contains([]int{3, 5, 10}, in.MaxAttempts) {
		f["maxAttempts"] = "Choose a number of attempts."
	}
	if !slices.Contains([]int{5, 15, 60}, in.LockoutMinutes) {
		f["lockoutMinutes"] = "Choose a duration."
	}
	if !slices.Contains([]int{15, 30, 60, 1440}, in.ResetExpiryMinutes) {
		f["resetExpiryMinutes"] = "Choose an expiry."
	}
	if !slices.Contains([]string{"admins", "members", "disabled"}, in.Invitations) {
		f["invitations"] = "Choose who can invite."
	}
	if !slices.Contains([]string{"viewer", "developer"}, in.SSODefaultRole) {
		f["ssoDefaultRole"] = "Viewer or developer."
	}
	if _, ok := lifetimes[in.SessionLifetime]; !ok {
		f["sessionLifetime"] = "Choose a lifetime."
	}
	if _, ok := idleTimeouts[in.IdleTimeout]; !ok {
		f["idleTimeout"] = "Choose a timeout."
	}
	if _, err := parseNets(in.TrustedProxies); err != nil {
		f["trustedProxies"] = err.Error()
	}
	for _, o := range in.AllowedOrigins {
		if u, err := url.Parse(o); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || (u.Path != "" && u.Path != "/") {
			f["allowedOrigins"] = fmt.Sprintf("%q isn't an origin like https://ignition.example.com.", o)
			break
		}
	}
	if allowed, err := parseNets(in.AllowedIPs); err != nil {
		f["allowedIps"] = err.Error()
	} else if in.IPRestrictions {
		// Judge the caller's address with the proxies being saved, the same way it'll be judged next.
		proxies, _ := parseNets(in.TrustedProxies)
		addr := clientIPWith(r, proxies)
		ip := net.ParseIP(addr)
		switch {
		case len(allowed) == 0:
			f["allowedIps"] = "Add at least one network."
		case ip == nil || !inNets(ip, allowed):
			f["allowedIps"] = fmt.Sprintf("Your address (%s) isn't in these networks. Saving would lock you out.", addr)
		}
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	before := s.policy()
	if err := s.Store.SetSetting(r.Context(), "security", in); err != nil {
		return err
	}
	s.setPolicy(in)
	if in.lifetime() < before.lifetime() {
		s.Store.ShortenSessions(r.Context(), in.lifetime())
	}
	s.Store.Audit(r.Context(), u, "security.settings", "settings", "security", map[string]any{"changed": policyDiff(before, in)}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"policy": in})
	return nil
}

// policyDiff names what changed, with before → after for scalar values (lists by count).
func policyDiff(a, b securityPolicy) map[string]string {
	am, bm := map[string]any{}, map[string]any{}
	toMap(a, am)
	toMap(b, bm)
	out := map[string]string{}
	for k, v := range bm {
		if fmt.Sprint(am[k]) != fmt.Sprint(v) {
			out[k] = fmt.Sprintf("%v → %v", am[k], v)
		}
	}
	return out
}

func (s *Server) revokeAllSessions(w http.ResponseWriter, r *http.Request, u *store.User) error {
	n, err := s.Store.RevokeAllSessions(r.Context())
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "security.sessions_revoked_all", "settings", "security", map[string]any{"sessions": n}, clientIP(r))
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	httpx.JSON(w, 200, map[string]any{"revoked": n})
	return nil
}

/* ---------------------------------- HTTPS ------------------------------------ */

type httpsStatus struct {
	URL       string     `json:"url"`
	HTTPS     bool       `json:"https"`
	Valid     bool       `json:"valid"`
	Issuer    string     `json:"issuer,omitempty"`
	Expires   *time.Time `json:"expires,omitempty"`
	Error     string     `json:"error,omitempty"`
	CheckedAt time.Time  `json:"checkedAt"`
}

var httpsCache struct {
	sync.Mutex
	v httpsStatus
}

// httpsStatus connects to the dashboard address and reads its certificate. Cached for 10 minutes.
func (s *Server) httpsStatus(ctx context.Context) httpsStatus {
	pub := s.publicURL()
	httpsCache.Lock()
	defer httpsCache.Unlock()
	if httpsCache.v.URL == pub && time.Since(httpsCache.v.CheckedAt) < 10*time.Minute {
		return httpsCache.v
	}
	st := httpsStatus{URL: pub, CheckedAt: time.Now()}
	u, err := url.Parse(pub)
	if err != nil || u.Host == "" {
		st.Error = "No dashboard address is set (Settings › General)."
		httpsCache.v = st
		return st
	}
	if u.Scheme != "https" {
		httpsCache.v = st
		return st
	}
	st.HTTPS = true
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "443")
	}
	dialer := &net.Dialer{Timeout: 4 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", host, &tls.Config{ServerName: u.Hostname(), InsecureSkipVerify: true}) // verified below, to report why
	if err != nil {
		st.Error = "Ignition couldn't connect to " + host + " to read the certificate: " + err.Error()
		httpsCache.v = st
		return st
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		st.Error = "The server presented no certificate."
		httpsCache.v = st
		return st
	}
	leaf := certs[0]
	exp := leaf.NotAfter
	st.Expires = &exp
	st.Issuer = leaf.Issuer.CommonName
	if len(leaf.Issuer.Organization) > 0 {
		st.Issuer = leaf.Issuer.Organization[0]
	}
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: u.Hostname(), Intermediates: inter}); err != nil {
		st.Error = err.Error()
	} else {
		st.Valid = true
	}
	httpsCache.v = st
	return st
}

func toMap(v any, out map[string]any) {
	b, _ := json.Marshal(v)
	json.Unmarshal(b, &out)
}

/* ------------------------------- Log redaction -------------------------------- */

// redactor replaces a resource's secret variable values with ••••• in log output, when the
// policy asks for it. Very short values (under 6 characters) would mask ordinary words, so
// they're left alone.
func (s *Server) redactor(ctx context.Context, resourceID string) func(string) string {
	if !s.policy().LogRedaction {
		return nil
	}
	vars, err := s.Store.ListEnv(ctx, resourceID)
	if err != nil {
		return nil
	}
	var pairs []string
	for _, v := range vars {
		if !v.Secret {
			continue
		}
		plain, err := s.Secrets.Open(v.Sealed, resourceID+":"+v.Key)
		if err == nil && len(plain) >= 6 {
			pairs = append(pairs, plain, "•••••")
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	return strings.NewReplacer(pairs...).Replace
}

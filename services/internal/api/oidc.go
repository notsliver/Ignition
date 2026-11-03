package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Single sign-on with OpenID Connect (Google, Okta, Authentik, Keycloak, Entra ID, ...).
 * Sign-in is the authorization code flow with PKCE, state and nonce; the ID token's signature is
 * checked against the provider's published keys. Identities are matched by issuer and subject.
 * Existing accounts link explicitly in Profile; provisioning can create an invited or new account. SSO sign-ins rely on the provider's own second factor.
 */

type oidcSettings struct {
	Provider       string     `json:"provider"`
	Issuer         string     `json:"issuer"`
	ClientID       string     `json:"clientId"`
	SecretSealed   []byte     `json:"secretSealed"`
	Label          string     `json:"label"`
	AllowedDomains []string   `json:"allowedDomains"`
	VerifiedAt     *time.Time `json:"verifiedAt"`
}

func (o oidcSettings) Configured() bool {
	return o.Issuer != "" && o.ClientID != "" && o.VerifiedAt != nil
}

func (o oidcSettings) label() string {
	if o.Label != "" {
		return o.Label
	}
	return "Single sign-on"
}

func (s *Server) oidcConfig(ctx context.Context) oidcSettings {
	var o oidcSettings
	s.Store.GetSetting(ctx, "oidc", &o)
	return o
}

func (s *Server) redirectURL(r *http.Request) string {
	base := strings.TrimRight(s.publicURL(), "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + "/api/auth/oidc/callback"
}

func (s *Server) oidcStatus(ctx context.Context) map[string]any {
	o := s.oidcConfig(ctx)
	return map[string]any{"configured": o.Configured(), "issuer": o.Issuer, "clientId": o.ClientID, "hasSecret": len(o.SecretSealed) > 0,
		"label": o.Label, "provider": o.Provider, "iconUrl": s.ssoIconURL(ctx), "allowedDomains": o.AllowedDomains, "verifiedAt": o.VerifiedAt}
}

type ssoState struct {
	mu       sync.Mutex
	pending  map[string]ssoPending
	provider *auth.OIDCProvider
	issuer   string
	fetched  time.Time
}

type ssoPending struct {
	startedAt                         time.Time
	userID, purpose, returnTo, issuer string
	sessionHash                       []byte
	onboardingHash                    []byte
	verifier, nonce                   string
	expires                           time.Time
}

func (st *ssoState) discover(ctx context.Context, issuer string) (*auth.OIDCProvider, error) {
	st.mu.Lock()
	if st.provider != nil && st.issuer == issuer && time.Since(st.fetched) < time.Hour {
		p := st.provider
		st.mu.Unlock()
		return p, nil
	}
	st.mu.Unlock()
	p, err := auth.DiscoverOIDC(ctx, issuer)
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	st.provider, st.issuer, st.fetched = p, issuer, time.Now()
	st.mu.Unlock()
	return p, nil
}

func randomString() string {
	raw := make([]byte, 24)
	rand.Read(raw)
	return base64.RawURLEncoding.EncodeToString(raw)
}

/* ----------------------------------- Public --------------------------------- */

// authMethods tells the sign-in page what to offer.
func (s *Server) authMethods(w http.ResponseWriter, r *http.Request) error {
	p := s.policy()
	o := s.oidcConfig(r.Context())
	loginProviders := []map[string]string{}
	configured, err := s.Store.Integrations(r.Context())
	if err != nil {
		return err
	}
	for _, c := range configured {
		if c.Enabled && c.SignInEnabled && c.Login.ClientID != "" && c.Login.HasSecret {
			label := c.Label
			if label == "" {
				label = c.Provider
			}
			loginProviders = append(loginProviders, map[string]string{"provider": c.Provider, "label": label, "url": "/api/auth/integrations/" + c.Provider + "/start"})
		}
	}
	_, passkeyErr := s.webAuthn()
	httpx.JSON(w, 200, map[string]any{"loginProviders": loginProviders, "passkeys": passkeyErr == nil, "password": p.LocalAuth, "sso": p.OIDCAuth && o.Configured(), "ssoLabel": o.label(), "ssoProvider": o.Provider, "ssoIconUrl": s.ssoIconURL(r.Context()), "requiredMethod": p.RequiredMethod})
	return nil
}

const ssoCookie = "ignition_sso"

func (s *Server) oidcStart(w http.ResponseWriter, r *http.Request) error {
	return s.beginSSO(w, r, "", "", "", nil)
}

func (s *Server) beginSSO(w http.ResponseWriter, r *http.Request, userID, purpose, returnTo string, sessionHash []byte) error {
	if !s.limiter.allowMax("login:"+clientIP(r), s.policy().RateLimit) {
		return httpx.TooMany("Too many sign-in attempts from your network. Wait a minute.")
	}
	o := s.oidcConfig(r.Context())
	if !s.policy().OIDCAuth || !o.Configured() {
		return httpx.NotFound("Single sign-on isn't set up")
	}
	p, err := s.sso.discover(r.Context(), o.Issuer)
	if err != nil {
		return httpx.Unavailable("The identity provider can't be reached: " + err.Error())
	}
	state, nonce := randomString(), randomString()
	verifier, challenge := auth.PKCE()
	s.sso.mu.Lock()
	if s.sso.pending == nil {
		s.sso.pending = map[string]ssoPending{}
	}
	for k, v := range s.sso.pending {
		if time.Now().After(v.expires) {
			delete(s.sso.pending, k)
		}
	}
	onboardingHash, _ := r.Context().Value(adminOnboardingKey{}).([]byte)
	s.sso.pending[state] = ssoPending{onboardingHash: onboardingHash, startedAt: time.Now(), verifier: verifier, nonce: nonce, expires: time.Now().Add(10 * time.Minute), userID: userID, purpose: purpose, returnTo: returnTo, issuer: o.Issuer, sessionHash: sessionHash}
	s.sso.mu.Unlock()
	// The cookie binds the callback to this browser, so a link can't be replayed elsewhere.
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: state, Path: "/api/auth/oidc", HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	authURL := p.AuthURL(o.ClientID, s.redirectURL(r), state, nonce, challenge)
	if purpose == "reauth" {
		target, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := target.Query()
		q.Set("prompt", "login")
		q.Set("max_age", "0")
		target.RawQuery = q.Encode()
		authURL = target.String()
	}
	if userID != "" {
		httpx.JSON(w, 200, map[string]string{"url": authURL})
		return nil
	}
	http.Redirect(w, r, authURL, http.StatusFound)
	return nil
}

// oidcCallback ends the flow in the browser: success sets the session cookie and goes to the
// dashboard; any failure goes to the sign-in page with a message.
func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) error {
	fail := func(msg string) error {
		http.Redirect(w, r, "/?sso_error="+url.QueryEscape(msg), http.StatusFound)
		return nil
	}
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: "", Path: "/api/auth/oidc", HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		msg := q.Get("error_description")
		if msg == "" {
			msg = e
		}
		return fail("The identity provider didn't sign you in: " + msg)
	}
	state := q.Get("state")
	c, err := r.Cookie(ssoCookie)
	if err != nil || c.Value != state || state == "" {
		return fail("This sign-in didn't start in this browser, or took too long. Try again.")
	}
	s.sso.mu.Lock()
	pend, ok := s.sso.pending[state]
	delete(s.sso.pending, state)
	s.sso.mu.Unlock()
	if !ok || time.Now().After(pend.expires) {
		return fail("This sign-in took too long. Try again.")
	}
	o := s.oidcConfig(r.Context())
	if !s.policy().OIDCAuth || !o.Configured() {
		return fail("Single sign-on is turned off.")
	}
	p, err := s.sso.discover(r.Context(), o.Issuer)
	if err != nil {
		return fail("The identity provider can't be reached.")
	}
	secret := ""
	if len(o.SecretSealed) > 0 {
		if secret, err = s.Secrets.Open(o.SecretSealed, "oidc"); err != nil {
			return fail("The stored client secret can't be read. Reconnect the provider.")
		}
	}
	idToken, err := p.Exchange(r.Context(), o.ClientID, secret, q.Get("code"), s.redirectURL(r), pend.verifier)
	if err != nil {
		return fail(err.Error())
	}
	claims, err := p.Verify(r.Context(), idToken, o.ClientID, pend.nonce, time.Now())
	if err != nil {
		return fail(err.Error())
	}
	if pend.purpose == "onboarding" {
		if pend.issuer != o.Issuer {
			return fail("The SSO provider changed. Start again.")
		}
		user, e := s.completeAdminSSO(r, pend, o, claims)
		if e != nil {
			return fail(e.Error())
		}
		rec := httptest.NewRecorder()
		if err = s.secondFactor(rec, r, user, "sso"); err != nil {
			return err
		}
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		http.Redirect(w, r, "/", 302)
		return nil
	}
	if pend.userID != "" {
		fail = func(msg string) error {
			http.Redirect(w, r, pend.returnTo+"?account_error="+url.QueryEscape(msg), http.StatusFound)
			return nil
		}
		c, err := r.Cookie(sessionCookie)
		if err != nil || !bytes.Equal(auth.HashToken(c.Value), pend.sessionHash) {
			return fail("Your account session changed. Start again.")
		}
		current, err := s.Store.UserBySession(r.Context(), pend.sessionHash, s.policy().idle())
		if err != nil || current.ID != pend.userID || o.Issuer != pend.issuer {
			return fail("Your account session or SSO provider changed. Start again.")
		}
		if pend.purpose == "reauth" {
			if claims.AuthTime < pend.startedAt.Add(-30*time.Second).Unix() || claims.AuthTime > time.Now().Add(30*time.Second).Unix() {
				return fail("The identity provider did not confirm a fresh sign-in. Use your password or passkey, or try again.")
			}
			linked, err := s.Store.UserByOIDCIdentity(r.Context(), o.Issuer, claims.Subject)
			if err != nil || linked.ID != current.ID {
				return fail("Use the SSO identity already connected to this account.")
			}
			if err = s.Store.ReauthenticateSession(r.Context(), current.ID, pend.sessionHash); err != nil {
				return err
			}
			return integrationIdentityConfirmed(w)
		} else {
			if err = s.requireRecentAccount(r, current); err != nil {
				return fail("Confirm your identity again before connecting.")
			}
			if current.SSO {
				return fail("Disconnect your existing SSO identity first.")
			}
			if err = s.Store.LinkOIDC(r.Context(), current.ID, o.Issuer, claims.Subject); err != nil {
				return fail("This SSO identity is already connected, or the connection changed.")
			}
			s.Store.Audit(r.Context(), current, "account.sso_connected", "user", current.ID, map[string]any{"issuer": o.Issuer}, clientIP(r))
		}
		http.Redirect(w, r, pend.returnTo+"?account_connected=1", http.StatusFound)
		return nil
	}
	user, err := s.ssoUser(r, o, claims)
	if err != nil {
		s.Store.Audit(r.Context(), nil, "auth.login_failed", "user", "", map[string]any{"email": claims.Email, "method": "sso", "reason": err.Error()}, clientIP(r))
		return fail(err.Error())
	}
	token, hash, err := auth.NewToken()
	if err != nil {
		return err
	}
	ttl := s.policy().lifetime()
	if err := s.Store.CreateSession(r.Context(), user.ID, hash, r.UserAgent(), clientIP(r), ttl, "sso"); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), user, "auth.login", "user", user.ID, map[string]any{"method": "sso", "issuer": o.Issuer}, clientIP(r))
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds())})
	http.Redirect(w, r, "/", http.StatusFound)
	return nil
}

// ssoUser finds or creates the account for a verified identity.
func (s *Server) ssoUser(r *http.Request, o oidcSettings, c *auth.Claims) (*store.User, error) {
	ctx := r.Context()
	email := strings.TrimSpace(c.Email)
	verified := c.EmailVerified == nil || *c.EmailVerified
	if len(o.AllowedDomains) > 0 {
		_, domain, _ := strings.Cut(strings.ToLower(email), "@")
		if !slices.Contains(o.AllowedDomains, domain) {
			return nil, errors.New("accounts from " + domain + " can't sign in here")
		}
	}
	user, err := s.Store.UserByOIDCIdentity(ctx, o.Issuer, c.Subject)
	if errors.Is(err, store.ErrNotFound) {
		if email == "" || !verified {
			return nil, errors.New("the identity provider didn't share a verified email address")
		}
		if _, err := mail.ParseAddress(email); err != nil {
			return nil, errors.New("the identity provider shared an invalid email address")
		}
		user, err = s.Store.UserByEmail(ctx, email)
		switch {
		case err == nil:
			return nil, errors.New("sign in with your existing method, then connect single sign-on in Profile")
		case errors.Is(err, store.ErrNotFound):
			user, err = s.provisionSSO(r, o, email, c)
			if err != nil {
				return nil, err
			}
		default:
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if user.Status != "active" {
		return nil, errors.New("this account is suspended")
	}
	return user, nil
}

// provisionSSO creates an account from a pending invitation (with its role) or, with
// provisioning on, with the default role.
func (s *Server) provisionSSO(r *http.Request, o oidcSettings, email string, c *auth.Claims) (*store.User, error) {
	ctx := r.Context()
	p := s.policy()
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name, _, _ = strings.Cut(email, "@")
	}
	if p.Invitations != "disabled" {
		invites, err := s.Store.PendingInvites(ctx)
		if err != nil {
			return nil, err
		}
		for _, inv := range invites {
			if strings.EqualFold(inv.Email, email) && inv.Role != nil {
				user, err := s.Store.AcceptInvite(ctx, inv.ID, inv.Email, name, "", *inv.Role)
				if err != nil {
					return nil, err
				}
				if err := s.Store.LinkOIDC(ctx, user.ID, o.Issuer, c.Subject); err != nil {
					return nil, err
				}
				s.Store.Audit(ctx, user, "user.joined", "user", user.ID, map[string]any{"email": email, "role": user.Role, "method": "sso"}, clientIP(r))
				return s.Store.GetUser(ctx, user.ID)
			}
		}
	}
	if !p.SSOProvisioning {
		return nil, errors.New("there's no Ignition account for " + email + ". Ask an admin for an invitation")
	}
	user, err := s.Store.CreateSSOUser(ctx, email, name, p.SSODefaultRole, o.Issuer, c.Subject)
	if err != nil {
		return nil, err
	}
	s.Store.Audit(ctx, user, "user.joined", "user", user.ID, map[string]any{"email": email, "role": user.Role, "method": "sso", "provisioned": true}, clientIP(r))
	return user, nil
}

/* ------------------------------ Configuration -------------------------------- */

type oidcInput struct {
	Provider       string   `json:"provider"`
	Issuer         string   `json:"issuer"`
	ClientID       string   `json:"clientId"`
	ClientSecret   string   `json:"clientSecret"` // "" keeps the stored one
	Label          string   `json:"label"`
	AllowedDomains []string `json:"allowedDomains"`
}

func (s *Server) testOIDC(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	var in oidcInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if message := validateSSOPreset(in.Provider, in.Issuer); message != "" {
		return httpx.Invalid(map[string]string{"issuer": message})
	}
	p, err := auth.DiscoverOIDC(r.Context(), in.Issuer)
	if err != nil {
		return httpx.Invalid(map[string]string{"issuer": err.Error()})
	}
	httpx.JSON(w, 200, map[string]any{"issuer": p.Issuer, "authorizationEndpoint": p.AuthorizationEndpoint, "tokenEndpoint": p.TokenEndpoint, "redirectUrl": s.redirectURL(r)})
	return nil
}

func (s *Server) putOIDC(w http.ResponseWriter, r *http.Request, u *store.User) error {
	var in oidcInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	f := map[string]string{}
	if message := validateSSOPreset(in.Provider, in.Issuer); message != "" {
		f["issuer"] = message
	}
	in.Issuer = strings.TrimRight(strings.TrimSpace(in.Issuer), "/")
	in.ClientID = strings.TrimSpace(in.ClientID)
	in.Label = strings.TrimSpace(in.Label)
	if in.ClientID == "" {
		f["clientId"] = "Paste the client ID from your provider."
	}
	if len(in.Label) > 40 {
		f["label"] = "Up to 40 characters."
	}
	domains := []string{}
	for _, d := range in.AllowedDomains {
		d = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d), "@"))
		if d == "" {
			continue
		}
		if strings.ContainsAny(d, " /@") || !strings.Contains(d, ".") {
			f["allowedDomains"] = d + " isn't a domain like example.com."
		}
		domains = append(domains, d)
	}
	old := s.oidcConfig(r.Context())
	if in.ClientSecret == "" && (len(old.SecretSealed) == 0 || old.Issuer != in.Issuer || old.ClientID != in.ClientID) {
		f["clientSecret"] = "Paste the client secret."
	}
	if len(f) == 0 {
		if _, err := auth.DiscoverOIDC(r.Context(), in.Issuer); err != nil {
			f["issuer"] = err.Error()
		}
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	sealed := old.SecretSealed
	if in.ClientSecret != "" {
		var err error
		if sealed, err = s.Secrets.Seal(strings.TrimSpace(in.ClientSecret), "oidc"); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	o := oidcSettings{Provider: in.Provider, Issuer: in.Issuer, ClientID: in.ClientID, SecretSealed: sealed, Label: in.Label, AllowedDomains: domains, VerifiedAt: &now}
	if err := s.Store.SetSetting(r.Context(), "oidc", o); err != nil {
		return err
	}
	s.sso.mu.Lock()
	s.sso.provider = nil
	s.sso.mu.Unlock()
	s.Store.Audit(r.Context(), u, "security.oidc_connected", "settings", "security", map[string]any{"issuer": in.Issuer, "clientId": in.ClientID, "allowedDomains": domains}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"oidc": s.oidcStatus(r.Context()), "redirectUrl": s.redirectURL(r)})
	return nil
}

// deleteOIDC disconnects the provider. Password sign-in comes back on so nobody is locked out.
func (s *Server) deleteOIDC(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := s.Store.SetSetting(r.Context(), "oidc", oidcSettings{}); err != nil {
		return err
	}
	p := s.policy()
	p.OIDCAuth, p.LocalAuth, p.RequiredMethod, p.SSOProvisioning = false, true, "any", false
	if err := s.Store.SetSetting(r.Context(), "security", p); err != nil {
		return err
	}
	s.setPolicy(p)
	s.Store.Audit(r.Context(), u, "security.oidc_disconnected", "settings", "security", nil, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"policy": p})
	return nil
}

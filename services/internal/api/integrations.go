package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/integrations"
	"github.com/notsliver/Ignition/services/internal/store"
)

func (s *Server) integrationRedirect(provider string) string {
	return strings.TrimRight(s.publicURL(), "/") + "/api/auth/integrations/" + provider + "/callback"
}
func platformAccountAdmin(u *store.User) bool {
	role := u.InstanceRole
	if role == "" {
		role = u.Role
	}
	return role == "admin" || role == "owner"
}

func (s *Server) integrationSettings(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if !platformAccountAdmin(u) {
		return httpx.Forbidden("Instance administrators manage integration applications.")
	}
	if _, err := accountSession(r, u); err != nil {
		return err
	}
	if r.Method == "PUT" {
		if err := s.requireRecentAccount(r, u); err != nil {
			return err
		}
		if r.PathValue("provider") == "source-policy" {
			var in struct {
				Mode string `json:"mode"`
			}
			if err := httpx.Decode(r, &in); err != nil {
				return err
			}
			if in.Mode != "managed" && in.Mode != "self-managed" {
				return httpx.Invalid(map[string]string{"mode": "Choose developer or managed hosting mode."})
			}
			if err := s.Store.SetSetting(r.Context(), "source_mode", in.Mode); err != nil {
				return err
			}
			s.Store.Audit(r.Context(), u, "integrations.source_policy", "instance", "", map[string]any{"mode": in.Mode}, clientIP(r))
		} else {
			provider := r.PathValue("provider")
			old, err := s.Store.Integration(r.Context(), provider)
			if err != nil {
				return notFound(err, "Provider")
			}
			old = integrations.Defaults(old)
			var in struct {
				Config       store.IntegrationConfig `json:"config"`
				LoginSecret  string                  `json:"loginSecret"`
				SourceSecret string                  `json:"sourceSecret"`
			}
			if err = httpx.Decode(r, &in); err != nil {
				return err
			}
			c := in.Config
			if c.Provider != provider || c.Revision != old.Revision {
				return httpx.Conflict("This provider changed. Reload its settings.")
			}
			c.LoginSecretEnc = old.LoginSecretEnc
			c.SourceSecretEnc = old.SourceSecretEnc
			c.Label = strings.TrimSpace(c.Label)
			c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
			if parsed, e := url.Parse(c.BaseURL); e == nil && parsed.Host != "" {
				parsed.Host = strings.ToLower(parsed.Host)
				parsed.Scheme = strings.ToLower(parsed.Scheme)
				c.BaseURL = parsed.String()
			}
			c.Login.ClientID = strings.TrimSpace(c.Login.ClientID)
			c.Repositories.ClientID = strings.TrimSpace(c.Repositories.ClientID)
			if in.LoginSecret != "" {
				if c.LoginSecretEnc, err = s.Secrets.Seal(in.LoginSecret, "integration:"+provider+":login"); err != nil {
					return err
				}
			}
			if in.SourceSecret != "" {
				if c.SourceSecretEnc, err = s.Secrets.Seal(in.SourceSecret, "integration:"+provider+":source"); err != nil {
					return err
				}
			}
			c.Login.HasSecret = len(c.LoginSecretEnc) > 0
			c.Repositories.HasSecret = len(c.SourceSecretEnc) > 0
			c.SourceRevision = old.SourceRevision
			if c.BaseURL != old.BaseURL || c.Repositories.ClientID != old.Repositories.ClientID || !slices.Equal(c.Repositories.Scopes, old.Repositories.Scopes) || in.SourceSecret != "" {
				c.SourceRevision++
			}
			if fields := integrations.Validate(c); len(fields) > 0 {
				return httpx.Invalid(fields)
			}
			if err = s.Store.SaveIntegration(r.Context(), c, old.Revision); err != nil {
				return httpx.Conflict("The provider changed. Reload before saving.")
			}
			s.Store.Audit(r.Context(), u, "integrations.configure", "integration", provider, map[string]any{"enabled": c.Enabled, "signIn": c.SignInEnabled, "sources": c.SourcesEnabled, "manual": c.ManualAllowed}, clientIP(r))
		}
	}
	list, err := s.Store.Integrations(r.Context())
	if err != nil {
		return err
	}
	out := []map[string]any{}
	for _, config := range list {
		config = integrations.Defaults(config)
		d := integrations.Catalog[config.Provider]
		out = append(out, map[string]any{"config": config, "oauthSupported": d.OAuth, "permissionNote": d.PermissionNote, "callbackUrl": s.integrationRedirect(config.Provider), "requiredLoginScopes": d.LoginScopes, "defaultSourceScopes": d.SourceScopes})
	}
	httpx.JSON(w, 200, map[string]any{"providers": out, "sourceMode": s.Store.SourceMode(r.Context())})
	return nil
}

func (s *Server) availableIntegrations(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if _, err := accountSession(r, u); err != nil {
		return err
	}
	list, err := s.Store.Integrations(r.Context())
	if err != nil {
		return err
	}
	ids, err := s.Store.IntegrationIdentities(r.Context(), u.ID)
	if err != nil {
		return err
	}
	out := []map[string]any{}
	for _, config := range list {
		c := integrations.Defaults(config)
		var identity *store.IntegrationIdentity
		for i := range ids {
			if ids[i].Provider == c.Provider {
				identity = &ids[i]
			}
		}
		if !c.Enabled && identity == nil {
			continue
		}
		out = append(out, map[string]any{"provider": c.Provider, "label": c.Label, "baseUrl": c.BaseURL, "oauth": integrations.Catalog[c.Provider].OAuth, "enabled": c.Enabled, "providerChanged": identity != nil && identity.Origin != c.BaseURL, "signIn": c.Enabled && c.SignInEnabled && c.Login.ClientID != "" && c.Login.HasSecret, "sources": c.Enabled && c.SourcesEnabled && c.Repositories.ClientID != "" && c.Repositories.HasSecret, "manualAllowed": c.Enabled && c.ManualAllowed, "identity": identity, "sourceScopes": c.Repositories.Scopes, "permissionNote": integrations.Catalog[c.Provider].PermissionNote})
	}
	httpx.JSON(w, 200, map[string]any{"providers": out, "sourceMode": s.Store.SourceMode(r.Context())})
	return nil
}
func (s *Server) unlinkIntegration(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := s.requireRecentAccount(r, u); err != nil {
		return err
	}
	provider := r.PathValue("provider")
	if _, ok := integrations.Catalog[provider]; !ok {
		return httpx.NotFound("Provider")
	}
	local := u.Role == "owner" || s.policy().LocalAuth && s.policy().RequiredMethod != "sso"
	issuer := ""
	o := s.oidcConfig(r.Context())
	if s.policy().OIDCAuth && o.Configured() {
		issuer = o.Issuer
	}
	hash, _ := accountSession(r, u)
	if err := s.Store.UnlinkIntegration(r.Context(), u.ID, provider, hash, local, issuer); err != nil {
		if errors.Is(err, store.ErrLastSignInMethod) {
			return httpx.Conflict(err.Error())
		}
		return notFound(err, "Connection")
	}
	s.Store.Audit(r.Context(), u, "account.integration_disconnected", "integration", provider, nil, clientIP(r))
	httpx.JSON(w, 204, nil)
	return nil
}

var integrationReturnRE = regexp.MustCompile(`^(/teams/[a-zA-Z0-9-]+/(settings/(profile|security|team-sources)|projects|resources|dashboard)(/[a-zA-Z0-9-]+)*|/admin/(integrations|admin-tokens))$`)

func (s *Server) integrationConnect(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if _, err := accountSession(r, u); err != nil {
		return err
	}
	purpose := r.PathValue("action")
	team := ""
	if r.PathValue("teamId") != "" {
		purpose = "source"
		team = u.TeamID
	} else {
		if purpose != "connect" && purpose != "reauth" {
			return httpx.NotFound("Action")
		}
		if purpose == "connect" {
			purpose = "account"
		}
		if purpose == "account" {
			if err := s.requireRecentAccount(r, u); err != nil {
				return err
			}
		}
	}
	var in struct {
		ReturnTo string `json:"returnTo"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if !integrationReturnRE.MatchString(in.ReturnTo) {
		return httpx.BadRequest("Return to an Ignition account or deployment page.")
	}
	return s.beginIntegration(w, r, u, purpose, team, in.ReturnTo)
}
func (s *Server) integrationLogin(w http.ResponseWriter, r *http.Request) error {
	return s.beginIntegration(w, r, nil, "login", "", "/")
}
func (s *Server) beginIntegration(w http.ResponseWriter, r *http.Request, u *store.User, purpose, team, returnTo string) error {
	if !s.limiter.allowMax("integration-start:"+clientIP(r), s.policy().RateLimit) {
		return httpx.TooMany("Too many connection attempts. Wait a minute.")
	}
	provider := r.PathValue("provider")
	c, err := s.Store.Integration(r.Context(), provider)
	if err != nil {
		return httpx.NotFound("Provider")
	}
	c = integrations.Defaults(c)
	if !c.Enabled || !integrations.Catalog[provider].OAuth {
		return httpx.Forbidden("This integration is disabled.")
	}
	source := purpose == "source"
	client := c.Login
	if source {
		client = c.Repositories
		if !c.SourcesEnabled {
			return httpx.Forbidden("OAuth repository connections are disabled for this provider.")
		}
	} else if !c.SignInEnabled {
		return httpx.Forbidden("Account sign-in is disabled for this provider.")
	}
	if client.ClientID == "" || !client.HasSecret {
		return httpx.Unavailable("An instance administrator must configure this OAuth application.")
	}
	if !source && u != nil {
		identities, err := s.Store.IntegrationIdentities(r.Context(), u.ID)
		if err != nil {
			return err
		}
		connected := false
		for _, v := range identities {
			connected = connected || v.Provider == provider
		}
		if purpose == "account" && connected {
			return httpx.Conflict("This provider is already connected.")
		}
		if purpose == "reauth" && !connected {
			return httpx.Forbidden("Connect this identity in Profile first.")
		}
	}
	state, hash, err := auth.NewToken()
	if err != nil {
		return err
	}
	browser, browserHash, err := auth.NewToken()
	if err != nil {
		return err
	}
	verifier, challenge := auth.PKCE()
	sealed, err := s.Secrets.Seal(verifier, "integration-state:"+hex.EncodeToString(hash))
	if err != nil {
		return err
	}
	row := store.IntegrationOAuthState{BrowserHash: browserHash, Provider: provider, Purpose: purpose, TeamID: team, ReturnTo: returnTo, VerifierEnc: sealed, Revision: c.Revision, Scopes: client.Scopes}
	if u != nil {
		row.UserID = u.ID
		row.SessionHash, err = accountSession(r, u)
		if err != nil {
			return err
		}
	}
	if err = s.Store.SaveIntegrationState(r.Context(), hash, row); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "ignition_oauth_" + provider, Value: browser, Path: "/api/auth/integrations/" + provider, HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	target := integrations.AuthorizationURL(c, source, s.integrationRedirect(provider), state, challenge)
	if purpose == "reauth" {
		parsed, _ := url.Parse(target)
		q := parsed.Query()
		if provider == "github" {
			q.Set("prompt", "select_account")
		} else if provider == "gitlab" {
			q.Set("prompt", "login")
		}
		parsed.RawQuery = q.Encode()
		target = parsed.String()
	}
	if purpose == "login" {
		http.Redirect(w, r, target, 302)
	} else {
		httpx.JSON(w, 200, map[string]string{"url": target})
	}
	return nil
}
func (s *Server) integrationCallback(w http.ResponseWriter, r *http.Request) error {
	provider := r.PathValue("provider")
	fail := func(message string) error {
		http.Redirect(w, r, "/?sso_error="+url.QueryEscape(message), 302)
		return nil
	}
	cookie, err := r.Cookie("ignition_oauth_" + provider)
	if err != nil {
		return fail("This connection did not start in this browser.")
	}
	state := r.URL.Query().Get("state")
	row, err := s.Store.TakeIntegrationState(r.Context(), auth.HashToken(state), auth.HashToken(cookie.Value), provider)
	if err != nil {
		return fail("This connection expired or was already used.")
	}
	http.SetCookie(w, &http.Cookie{Name: "ignition_oauth_" + provider, Value: "", Path: "/api/auth/integrations/" + provider, HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	if row.Purpose != "login" {
		fail = func(message string) error {
			http.Redirect(w, r, row.ReturnTo+"?account_error="+url.QueryEscape(message), 302)
			return nil
		}
	}
	if r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
		return fail("The provider did not authorize this connection.")
	}
	c, err := s.Store.Integration(r.Context(), provider)
	if err != nil {
		return err
	}
	c = integrations.Defaults(c)
	if !c.Enabled || c.Revision != row.Revision {
		return fail("The instance integration settings changed. Start again.")
	}
	var user *store.User
	if row.Purpose != "login" {
		session, err := r.Cookie(sessionCookie)
		if err != nil || !bytes.Equal(auth.HashToken(session.Value), row.SessionHash) {
			return fail("Your signed-in account changed. Start again.")
		}
		user, err = s.Store.UserBySession(r.Context(), row.SessionHash, s.policy().idle())
		if err != nil || user.ID != row.UserID {
			return fail("Your account session ended. Sign in again.")
		}
		if row.Purpose == "account" {
			if err = s.requireRecentAccount(r, user); err != nil {
				return fail("Confirm your identity again, then reconnect.")
			}
		}
	}
	secretEnc := c.LoginSecretEnc
	source := row.Purpose == "source"
	if source {
		secretEnc = c.SourceSecretEnc
	}
	secret, err := s.Secrets.Open(secretEnc, "integration:"+provider+map[bool]string{true: ":source", false: ":login"}[source])
	if err != nil {
		return fail("The saved client secret could not be read.")
	}
	verifier, err := s.Secrets.Open(row.VerifierEnc, "integration-state:"+hex.EncodeToString(auth.HashToken(state)))
	if err != nil {
		return err
	}
	token, err := integrations.Exchange(r.Context(), c, source, secret, r.URL.Query().Get("code"), s.integrationRedirect(provider), verifier)
	if err != nil {
		return fail(err.Error())
	}
	granted := strings.Fields(strings.ReplaceAll(token.Scope, ",", " "))
	for _, scope := range row.Scopes {
		if !slices.Contains(granted, scope) {
			return fail("The OAuth grant is missing requested permissions. Check the application scopes and reconnect.")
		}
	}
	{
		for _, scope := range granted {
			if !slices.Contains(row.Scopes, scope) {
				return fail("The sign-in application grants additional permissions. Configure a dedicated identity-only OAuth application.")
			}
		}
	}
	identity, verified, err := integrations.Identity(r.Context(), c, token.AccessToken, !source)
	if err != nil {
		return fail(err.Error())
	}
	switch row.Purpose {
	case "source":
		membership, err := s.Store.Membership(r.Context(), row.TeamID, user.ID)
		if err != nil && platformAccountAdmin(user) {
			membership, err = s.Store.AdminTeam(r.Context(), row.TeamID)
		}
		if err != nil {
			return fail("You no longer have access to that team.")
		}
		actor := *user
		actor.InstanceRole = user.Role
		actor.TeamID = row.TeamID
		actor.TeamRole = membership.Role
		actor.Role = membership.Role
		actor.Permissions = membership.Overrides
		if !can(&actor, "sources.use") {
			return fail("You no longer have permission to connect repository sources.")
		}
		ctx := store.WithScope(r.Context(), store.Scope{TeamID: row.TeamID, UserID: user.ID, Permission: "sources.use", MinRank: 2, InstanceAdmin: membership.AdminAccess})
		encoded, err := json.Marshal(token)
		if err != nil {
			return err
		}
		id, err := s.Store.SaveManagedSource(ctx, user.ID, row.TeamID, c, identity, granted, func(id string) ([]byte, error) { return s.Secrets.Seal(string(encoded), "source:"+id) })
		if err != nil {
			return fail("The connection could not be saved. Check team permissions and try again.")
		}
		s.Store.Audit(ctx, &actor, "source.oauth_connected", "source", id, map[string]any{"provider": provider}, clientIP(r))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte(`<!doctype html><html><meta name="viewport" content="width=device-width"><title>Source connected</title><body><p>Source connected. You can close this window and continue in Ignition.</p><button onclick="window.close()">Close window</button><script>window.close()</script></body></html>`))
		return nil
	case "account":
		if err = s.Store.LinkIntegration(r.Context(), user.ID, identity); err != nil {
			return fail("This provider identity is already linked to an account.")
		}
		s.Store.Audit(r.Context(), user, "account.integration_connected", "integration", provider, nil, clientIP(r))
	case "reauth":
		linked, err := s.Store.IntegrationUser(r.Context(), provider, c.BaseURL, identity.Subject)
		if err != nil || linked.ID != user.ID {
			return fail("Use the provider identity already linked to this account.")
		}
		if err = s.Store.ReauthenticateSession(r.Context(), user.ID, row.SessionHash); err != nil {
			return err
		}
		return integrationIdentityConfirmed(w)
	case "login":
		user, err = s.Store.IntegrationUser(r.Context(), provider, c.BaseURL, identity.Subject)
		if errors.Is(err, store.ErrNotFound) {
			if s.policy().RequiredMethod == "sso" {
				return fail("This instance requires organization single sign-on.")
			}
			if !c.AllowSignup || s.policy().DisableRegistration || s.policy().Invitations == "disabled" {
				return fail("Sign in with your existing method and connect this provider in Profile, or ask an administrator for an account.")
			}
			if !verified {
				return fail("The provider did not supply a verified email address. Ask an administrator for an account.")
			}
			if address, e := mail.ParseAddress(identity.Email); e != nil || address.Address != identity.Email {
				return fail("The provider returned an invalid email address.")
			}
			role := s.policy().SSODefaultRole
			if role != "developer" {
				role = "viewer"
			}
			user, err = s.Store.CreateIntegrationUser(r.Context(), strings.ToLower(identity.Email), identity.Username, role, identity)
		}
		if err != nil || user.Status != "active" {
			return fail("This account is unavailable, or its email already belongs to an account. Sign in with your existing method and connect in Profile.")
		}
		if s.policy().RequiredMethod == "sso" && user.Role != "owner" {
			return fail("This instance requires organization single sign-on.")
		}
		// Complete the existing MFA policy before establishing a session. The UI handles the ticket.
		buffer := &oauthLoginRecorder{ResponseWriter: w}
		if err = s.secondFactor(buffer, r, user, "oauth:"+provider); err != nil {
			return err
		}
		w.Header().Del("Content-Type")
		target := "/"
		if buffer.result != "" {
			target += "#oauth_result=" + url.QueryEscape(buffer.result)
		}
		http.Redirect(w, r, target, 302)
		return nil
	}
	http.Redirect(w, r, row.ReturnTo+"?account_connected=1", 302)
	return nil
}

// The callback carries only a short-lived MFA ticket; authenticated account/session data stays in /auth/me.
type oauthLoginRecorder struct {
	http.ResponseWriter
	result string
}

func (w *oauthLoginRecorder) WriteHeader(int) {}
func (w *oauthLoginRecorder) Write(b []byte) (int, error) {
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		return 0, err
	}
	if ticket, ok := v["ticket"].(string); ok {
		raw, _ := json.Marshal(map[string]any{"ticket": ticket, "mfaRequired": v["mfaRequired"], "mfaSetupRequired": v["mfaSetupRequired"]})
		w.result = string(raw)
	}
	return len(b), nil
}

func (s *Server) manualSourceAllowed(r *http.Request, u *store.User, provider string) error {
	if platformAccountAdmin(u) {
		return nil
	}
	if s.Store.SourceMode(r.Context()) == "managed" {
		return httpx.Forbidden("This hosting instance uses managed connections. Connect a provider through the instance OAuth application.")
	}
	c, err := s.Store.Integration(r.Context(), provider)
	if err != nil {
		return err
	}
	if !c.Enabled || !c.ManualAllowed {
		return httpx.Forbidden("An instance administrator disabled manual credentials for this provider.")
	}
	return nil
}
func (s *Server) disconnectManagedSource(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Connection")
	if err != nil {
		return err
	}
	src, err := s.Store.GetSource(r.Context(), id)
	if err != nil {
		return notFound(err, "Connection")
	}
	if src.OAuthProvider == "" || src.OAuthUserID != u.ID && !can(u, "sources.manage") {
		return httpx.Forbidden("Only the connection owner or a team administrator can disconnect this source.")
	}
	if src.LinkedResources > 0 {
		return httpx.Conflict("Change the linked resources to another source before disconnecting.")
	}
	if err = s.Store.DeleteSource(r.Context(), id); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "source.oauth_disconnected", "source", id, nil, clientIP(r))
	httpx.JSON(w, 204, nil)
	return nil
}

func integrationIdentityConfirmed(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, err := w.Write([]byte(`<!doctype html><html><meta name="viewport" content="width=device-width"><title>Identity confirmed</title><body><p>Identity confirmed. You can close this window and continue in Ignition.</p><button onclick="window.close()">Close window</button><script>window.close()</script></body></html>`))
	return err
}

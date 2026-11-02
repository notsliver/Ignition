package api

import (
	"net/http"
	"net/mail"
	"regexp"
	"strings"

	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

var accountReturnRE = regexp.MustCompile(`^(/teams/[a-zA-Z0-9-]+/settings/(profile|security)|/admin/integrations)$`)

func (s *Server) accountSSOStart(w http.ResponseWriter, r *http.Request, u *store.User) error {
	hash, err := accountSession(r, u)
	if err != nil {
		return err
	}
	action := r.PathValue("action")
	if action != "connect" && action != "reauth" {
		return httpx.NotFound("Account action")
	}
	if action == "connect" {
		if err = s.requireRecentAccount(r, u); err != nil {
			return err
		}
		if u.SSO {
			return httpx.Conflict("Disconnect your existing identity first.")
		}
	}
	var in struct {
		ReturnTo string `json:"returnTo"`
	}
	if err = httpx.Decode(r, &in); err != nil {
		return err
	}
	if !accountReturnRE.MatchString(in.ReturnTo) {
		return httpx.BadRequest("Return to Profile or Security to connect your account.")
	}
	return s.beginSSO(w, r, u.ID, action, in.ReturnTo, hash)
}

func accountSession(r *http.Request, u *store.User) ([]byte, error) {
	if u.CredentialTokenID != "" || u.ServiceResourceID != "" {
		return nil, httpx.Forbidden("Use your signed-in account.")
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, httpx.Unauthorized()
	}
	return auth.HashToken(c.Value), nil
}
func (s *Server) requireRecentAccount(r *http.Request, u *store.User) error {
	hash, err := accountSession(r, u)
	if err != nil {
		return err
	}
	ok, err := s.Store.RecentlyAuthenticated(r.Context(), u.ID, hash)
	if err != nil {
		return err
	}
	if !ok {
		return &httpx.Error{Status: 403, Code: "reauth_required", Message: "Confirm your identity to continue."}
	}
	return nil
}
func (s *Server) accountReauthenticate(w http.ResponseWriter, r *http.Request, u *store.User) error {
	hash, err := accountSession(r, u)
	if err != nil {
		return err
	}
	if !s.limiter.allowMax("account-reauth:"+u.ID, s.policy().MaxAttempts) {
		return httpx.TooMany("Too many attempts. Wait a minute.")
	}
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err = httpx.Decode(r, &in); err != nil {
		return err
	}
	if u.HasPassword {
		ok, err := auth.VerifyPassword(in.Password, u.PasswordHash)
		if err != nil || !ok {
			return httpx.Invalid(map[string]string{"password": "Your password doesn't match."})
		}
	} else if err = s.requireRecentAccount(r, u); err != nil {
		return err
	}
	if u.MFAEnabledAt != nil {
		if ok, _ := s.verifyCode(r, u, in.Code); !ok {
			return httpx.Invalid(map[string]string{"code": "Enter a current authenticator or recovery code."})
		}
	}
	if err = s.Store.ReauthenticateSession(r.Context(), u.ID, hash); err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"confirmed": true})
	return nil
}
func (s *Server) accountProfile(w http.ResponseWriter, r *http.Request, u *store.User) error {
	hash, err := accountSession(r, u)
	if err != nil {
		return err
	}
	switch r.Method {
	case "PATCH":
		var in struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		}
		if err = httpx.Decode(r, &in); err != nil {
			return err
		}
		in.Name = strings.TrimSpace(in.Name)
		in.Email = strings.ToLower(strings.TrimSpace(in.Email))
		fields := map[string]string{}
		if in.Name == "" || len([]rune(in.Name)) > 80 {
			fields["name"] = "Enter a name up to 80 characters."
		}
		if a, e := mail.ParseAddress(in.Email); e != nil || a.Address != in.Email || len(in.Email) > 254 {
			fields["email"] = "Enter a valid email address."
		}
		if len(fields) > 0 {
			return httpx.Invalid(fields)
		}
		if !strings.EqualFold(in.Email, u.Email) {
			if err = s.requireRecentAccount(r, u); err != nil {
				return err
			}
		}
		if err = s.Store.UpdateAccount(r.Context(), u.ID, in.Name, in.Email, hash); err != nil {
			if store.IsUnique(err) {
				return httpx.Invalid(map[string]string{"email": "This email address is already in use."})
			}
			return err
		}
		s.Store.Audit(r.Context(), u, "account.update", "user", u.ID, map[string]any{"emailChanged": !strings.EqualFold(in.Email, u.Email)}, clientIP(r))
		u, err = s.Store.GetUser(r.Context(), u.ID)
		if err != nil {
			return err
		}
	case "DELETE":
		if err = s.requireRecentAccount(r, u); err != nil {
			return err
		}
		var in struct {
			Email string `json:"email"`
		}
		if err = httpx.Decode(r, &in); err != nil {
			return err
		}
		if !strings.EqualFold(strings.TrimSpace(in.Email), u.Email) {
			return httpx.Invalid(map[string]string{"email": "Type your email address to confirm deletion."})
		}
		if err = s.Store.DeleteOwnAccount(r.Context(), u.ID); err != nil {
			return err
		}
		s.removeIcon("avatars", strings.TrimPrefix(u.AvatarURL, "/api/account/images/avatars/"))
		s.Store.Audit(r.Context(), nil, "account.deleted", "user", u.ID, nil, clientIP(r))
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
		httpx.JSON(w, 204, nil)
		return nil
	}
	recent, err := s.Store.RecentlyAuthenticated(r.Context(), u.ID, hash)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"user": u, "recentlyAuthenticated": recent})
	return nil
}
func (s *Server) accountPassword(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := s.requireRecentAccount(r, u); err != nil {
		return err
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if msg := s.checkPassword(r.Context(), in.Password); msg != "" {
		return httpx.Invalid(map[string]string{"password": msg})
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return err
	}
	if err = s.Store.SetPassword(r.Context(), u.ID, hash); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "account.password_changed", "user", u.ID, nil, clientIP(r))
	// SetPassword invalidates all sessions; establish a new authenticated session for this browser.
	u, err = s.Store.GetUser(r.Context(), u.ID)
	if err != nil {
		return err
	}
	return s.startSession(w, r, u, "password-change", nil)
}
func (s *Server) accountSessions(w http.ResponseWriter, r *http.Request, u *store.User) error {
	hash, err := accountSession(r, u)
	if err != nil {
		return err
	}
	if r.Method == "DELETE" {
		id := r.PathValue("id")
		if id == "others" {
			n, err := s.Store.RevokeOtherSessions(r.Context(), u.ID, hash)
			if err != nil {
				return err
			}
			s.Store.Audit(r.Context(), u, "account.sessions_revoked", "user", u.ID, map[string]any{"count": n}, clientIP(r))
			httpx.JSON(w, 200, map[string]any{"revoked": n})
			return nil
		}
		if !uuidRE.MatchString(id) {
			return httpx.NotFound("Session")
		}
		ok, err := s.Store.DeleteAccountSession(r.Context(), u.ID, id)
		if err != nil {
			return err
		}
		if !ok {
			return httpx.NotFound("Session")
		}
		s.Store.Audit(r.Context(), u, "account.session_revoked", "session", id, nil, clientIP(r))
		httpx.JSON(w, 204, nil)
		return nil
	}
	sessions, err := s.Store.AccountSessions(r.Context(), u.ID, hash, s.policy().idle())
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"sessions": sessions})
	return nil
}

func (s *Server) accountIntegrations(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if _, err := accountSession(r, u); err != nil {
		return err
	}
	o := s.oidcConfig(r.Context())
	enabled := s.policy().OIDCAuth && o.Configured()
	if r.Method == "DELETE" {
		if err := s.requireRecentAccount(r, u); err != nil {
			return err
		}
		local := u.Role == "owner" || (s.policy().LocalAuth && s.policy().RequiredMethod != "sso")
		if !local {
			return httpx.Conflict("This instance requires single sign-on. Ask an administrator before disconnecting.")
		}
		if err := s.Store.UnlinkAccountOIDC(r.Context(), u.ID, func() []byte { hash, _ := accountSession(r, u); return hash }()); err != nil {
			if err == store.ErrLastSignInMethod {
				return httpx.Conflict(err.Error())
			}
			return err
		}
		s.Store.Audit(r.Context(), u, "account.sso_disconnected", "user", u.ID, nil, clientIP(r))
		httpx.JSON(w, 204, nil)
		return nil
	}
	issuer, subject, err := s.Store.AccountOIDC(r.Context(), u.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"sso": map[string]any{"provider": o.Provider, "iconUrl": s.ssoIconURL(r.Context()), "label": o.label(), "issuer": o.Issuer, "configured": enabled, "connected": subject != "" && issuer == o.Issuer, "account": u.Email, "providerChanged": subject != "" && issuer != o.Issuer}, "passwordLength": s.policy().PasswordLength})
	return nil
}

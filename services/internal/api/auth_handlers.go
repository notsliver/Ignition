package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) error {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"required": n == 0, "instanceName": s.instance(r.Context()).Name})
	return nil
}

type credentials struct {
	Code     string `json:"code"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) error {
	if !s.limiter.allow("setup:" + clientIP(r)) {
		return httpx.TooMany("Too many attempts. Wait a minute and try again.")
	}
	var in credentials
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	s.setupMu.Lock()
	code := s.setupCode
	s.setupMu.Unlock()
	fields := map[string]string{}
	if code == "" || subtle.ConstantTimeCompare([]byte(strings.ToUpper(strings.TrimSpace(in.Code))), []byte(code)) != 1 {
		fields["code"] = "That setup code doesn't match. Find it in the API logs."
	}
	if _, err := mail.ParseAddress(in.Email); err != nil {
		fields["email"] = "Enter a valid email address."
	}
	if msg := s.checkPassword(r.Context(), in.Password); msg != "" {
		fields["password"] = msg
	}
	if len(fields) > 0 {
		return httpx.Invalid(fields)
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return err
	}
	user, err := s.Store.CreateFirstOwner(r.Context(), strings.TrimSpace(in.Email), strings.TrimSpace(in.Name), hash)
	if errors.Is(err, store.ErrNotFound) {
		return httpx.Conflict("Ignition is already set up. Sign in instead.")
	}
	if err != nil {
		return err
	}
	s.setupMu.Lock()
	s.setupCode = ""
	s.setupMu.Unlock()
	s.Store.Audit(r.Context(), user, "instance.setup", "user", user.ID, nil, clientIP(r))
	return s.startSession(w, r, user, "password", nil)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	p := s.policy()
	if !s.limiter.allowMax("login:"+clientIP(r), p.RateLimit) {
		return httpx.TooMany("Too many sign-in attempts from your network. Wait a minute.")
	}
	var in credentials
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	invalid := httpx.Invalid(map[string]string{"password": "Email or password is incorrect."})
	user, err := s.Store.UserByEmail(r.Context(), strings.TrimSpace(in.Email))
	if errors.Is(err, store.ErrNotFound) {
		auth.HashPassword(in.Password) // spend similar time so response timing doesn't reveal which emails exist
		s.Store.Audit(r.Context(), nil, "auth.login_failed", "user", "", map[string]any{"email": strings.TrimSpace(in.Email), "reason": "unknown email"}, clientIP(r))
		return invalid
	}
	if err != nil {
		return err
	}
	if user.LockedUntil != nil && user.LockedUntil.After(time.Now()) {
		return httpx.TooMany(fmt.Sprintf("This account is locked after too many failed attempts. Try again in %s.", time.Until(*user.LockedUntil).Round(time.Minute).String()))
	}
	ok, err := auth.VerifyPassword(in.Password, user.PasswordHash)
	if err != nil || !ok {
		s.Store.RecordFailedLogin(r.Context(), user.ID, p.MaxAttempts, time.Duration(p.LockoutMinutes)*time.Minute)
		s.Store.Audit(r.Context(), user, "auth.login_failed", "user", user.ID, nil, clientIP(r))
		if user.FailedLogins+1 >= p.MaxAttempts {
			s.Store.Audit(r.Context(), user, "auth.locked", "user", user.ID, map[string]any{"minutes": p.LockoutMinutes, "attempts": p.MaxAttempts}, clientIP(r))
		}
		return invalid
	}
	if user.Status != "active" {
		if p.EnforceMFA && user.MFAEnabledAt == nil && p.MFADeadlineAction == "suspend" && !p.deadline().IsZero() && !p.mfaGrace() {
			return httpx.Forbidden("This account was suspended because two-factor authentication wasn't set up by " + p.MFADeadline + ". Ask an admin to reactivate it; you'll set it up when you sign in.")
		}
		return httpx.Forbidden("This account is suspended.")
	}
	// Owners can always use their password: it's the way back in if the identity provider breaks.
	if user.Role != "owner" {
		if !p.LocalAuth {
			return httpx.Forbidden("Password sign-in is turned off. Use single sign-on.")
		}
		if p.RequiredMethod == "sso" {
			return httpx.Forbidden("This Ignition requires single sign-on. Use the SSO button.")
		}
	}
	s.Store.ResetFailedLogins(r.Context(), user.ID)
	return s.secondFactor(w, r, user, "password")
}

// secondFactor finishes a password sign-in: straight to a session, or to a TOTP challenge when
// the account has one, or to enrollment when MFA is enforced and the account has none.
func (s *Server) secondFactor(w http.ResponseWriter, r *http.Request, user *store.User, method string) error {
	switch {
	case user.MFAEnabledAt != nil:
		ticket := s.mfa.issue(user.ID, method, false)
		httpx.JSON(w, 200, map[string]any{"mfaRequired": true, "ticket": ticket})
		return nil
	case s.policy().mfaRequired() && !mfaExempt(user):
		ticket := s.mfa.issue(user.ID, method, true)
		httpx.JSON(w, 200, map[string]any{"mfaSetupRequired": true, "ticket": ticket})
		return nil
	}
	s.Store.Audit(r.Context(), user, "auth.login", "user", user.ID, map[string]any{"method": method}, clientIP(r))
	return s.startSession(w, r, user, method, nil)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user *store.User, method string, extra map[string]any) error {
	token, hash, err := auth.NewToken()
	if err != nil {
		return err
	}
	ttl := s.policy().lifetime()
	if err := s.Store.CreateSession(r.Context(), user.ID, hash, r.UserAgent(), clientIP(r), ttl, method); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.SecureCookies,
		SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds()),
	})
	prefs, _ := s.Store.UserPreferences(r.Context(), user.ID)
	out := map[string]any{"user": user, "permissions": effectivePermissions(user), "mfaNotice": s.mfaNotice(user), "preferences": prefs}
	for k, v := range extra {
		out[k] = v
	}
	httpx.JSON(w, 200, out)
	return nil
}

// checkPassword applies the password policy and returns a message for the form, or "".
// The breach check fails open: an install without internet access can still set passwords.
func (s *Server) checkPassword(ctx context.Context, password string) string {
	p := s.policy()
	if err := auth.ValidatePasswordMin(password, p.PasswordLength); err != nil {
		return strings.ToUpper(err.Error()[:1]) + err.Error()[1:] + "."
	}
	if p.BreachedPasswords {
		ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		if n, err := auth.BreachCount(ctx, password); err == nil && n > 0 {
			return fmt.Sprintf("This password appears in %d known data breaches. Choose another.", n)
		}
	}
	return ""
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.Store.DeleteSession(r.Context(), auth.HashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	httpx.JSON(w, 204, nil)
	return nil
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, user *store.User) error {
	prefs, _ := s.Store.UserPreferences(r.Context(), user.ID)
	httpx.JSON(w, 200, map[string]any{"user": user, "permissions": effectivePermissions(user), "mfaEnforced": s.policy().EnforceMFA, "mfaNotice": s.mfaNotice(user),
		"preferences": prefs})
	return nil
}

package api

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/ignition/ignition/internal/auth"
	"github.com/ignition/ignition/internal/httpx"
	"github.com/ignition/ignition/internal/store"
)

const (
	maxFailedLogins = 5
	lockoutPeriod   = 15 * time.Minute
)

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) error {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]bool{"required": n == 0})
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
	if err := auth.ValidatePassword(in.Password); err != nil {
		fields["password"] = strings.ToUpper(err.Error()[:1]) + err.Error()[1:] + "."
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
		return httpx.Conflict("IGNITION is already set up. Sign in instead.")
	}
	if err != nil {
		return err
	}
	s.setupMu.Lock()
	s.setupCode = ""
	s.setupMu.Unlock()
	s.Store.Audit(r.Context(), user, "instance.setup", "user", user.ID, nil, clientIP(r))
	return s.startSession(w, r, user)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	if !s.limiter.allow("login:" + clientIP(r)) {
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
		return invalid
	}
	if err != nil {
		return err
	}
	if user.LockedUntil != nil && user.LockedUntil.After(time.Now()) {
		return httpx.TooMany("This account is locked after too many failed attempts. Try again in 15 minutes.")
	}
	ok, err := auth.VerifyPassword(in.Password, user.PasswordHash)
	if err != nil || !ok {
		s.Store.RecordFailedLogin(r.Context(), user.ID, maxFailedLogins, lockoutPeriod)
		s.Store.Audit(r.Context(), user, "auth.login_failed", "user", user.ID, nil, clientIP(r))
		return invalid
	}
	if user.Status != "active" {
		return httpx.Forbidden("This account is suspended.")
	}
	s.Store.ResetFailedLogins(r.Context(), user.ID)
	s.Store.Audit(r.Context(), user, "auth.login", "user", user.ID, nil, clientIP(r))
	return s.startSession(w, r, user)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user *store.User) error {
	token, hash, err := auth.NewToken()
	if err != nil {
		return err
	}
	if err := s.Store.CreateSession(r.Context(), user.ID, hash, r.UserAgent(), clientIP(r), sessionTTL); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.SecureCookies,
		SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds()),
	})
	httpx.JSON(w, 200, map[string]any{"user": user})
	return nil
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
	httpx.JSON(w, 200, map[string]any{"user": user})
	return nil
}

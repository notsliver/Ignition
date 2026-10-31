package api

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Two-factor authentication with TOTP (any authenticator app) and ten one-time recovery codes.
 * A correct password with MFA on doesn't create a session: it returns a short-lived ticket that
 * a code (or a recovery code) turns into one. When MFA is enforced and the account has none, the
 * ticket leads through enrollment instead. Secrets are sealed with the instance key; recovery
 * codes are stored hashed and work once.
 */

const (
	ticketTTL       = 5 * time.Minute
	ticketAttempts  = 5
	recoveryCodeLen = 10
)

type mfaTicket struct {
	userID   string
	method   string
	setup    bool   // enrollment is required before a session is created
	secret   string // pending secret during enrollment
	expires  time.Time
	attempts int
}

type mfaChallenges struct {
	mu       sync.Mutex
	tickets  map[string]*mfaTicket
	lastStep map[string]uint64 // last accepted TOTP step per user, so a code can't be replayed
}

func (c *mfaChallenges) issue(userID, method string, setup bool) string {
	raw := make([]byte, 24)
	rand.Read(raw)
	t := base64.RawURLEncoding.EncodeToString(raw)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tickets == nil {
		c.tickets, c.lastStep = map[string]*mfaTicket{}, map[string]uint64{}
	}
	for k, v := range c.tickets {
		if time.Now().After(v.expires) {
			delete(c.tickets, k)
		}
	}
	c.tickets[t] = &mfaTicket{userID: userID, method: method, setup: setup, expires: time.Now().Add(ticketTTL)}
	return t
}

func (c *mfaChallenges) get(ticket string) (*mfaTicket, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.tickets[ticket]
	if !ok || time.Now().After(t.expires) || t.attempts >= ticketAttempts {
		return nil, false
	}
	return t, true
}

func (c *mfaChallenges) fail(ticket string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.tickets[ticket]; ok {
		t.attempts++
	}
}

func (c *mfaChallenges) done(ticket string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.tickets, ticket)
}

// acceptStep records a TOTP step and refuses it when it was already used (or an older one).
func (c *mfaChallenges) acceptStep(userID string, step uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastStep == nil {
		c.lastStep = map[string]uint64{}
	}
	if step <= c.lastStep[userID] {
		return false
	}
	c.lastStep[userID] = step
	return true
}

func ticketExpired() error {
	return &httpx.Error{Status: 401, Code: "mfa_ticket_expired", Message: "This sign-in took too long or had too many wrong codes. Sign in again."}
}

// verifyCode checks a TOTP code or, failing that, a recovery code (which is then used up).
func (s *Server) verifyCode(r *http.Request, user *store.User, code string) (ok bool, usedRecovery bool) {
	code = strings.TrimSpace(code)
	m, err := s.Store.GetMFA(r.Context(), user.ID)
	if err != nil || m.EnabledAt == nil {
		return false, false
	}
	if digits := strings.ReplaceAll(code, " ", ""); len(digits) == 6 {
		secret, err := s.Secrets.Open(m.SealedSecret, "totp:"+user.ID)
		if err != nil {
			return false, false
		}
		if step, ok := auth.VerifyTOTP(secret, digits, time.Now()); ok && s.mfa.acceptStep(user.ID, step) {
			return true, false
		}
		return false, false
	}
	used, err := s.Store.UseRecoveryCode(r.Context(), user.ID, auth.HashRecoveryCode(code))
	return err == nil && used, used
}

/* ------------------------------- Sign-in step ------------------------------- */

func (s *Server) mfaVerify(w http.ResponseWriter, r *http.Request) error {
	if !s.limiter.allowMax("mfa:"+clientIP(r), s.policy().RateLimit) {
		return httpx.TooMany("Too many attempts. Wait a minute.")
	}
	var in struct{ Ticket, Code string }
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	t, ok := s.mfa.get(in.Ticket)
	if !ok || t.setup {
		return ticketExpired()
	}
	user, err := s.Store.GetUser(r.Context(), t.userID)
	if err != nil || user.Status != "active" {
		return ticketExpired()
	}
	ok, recovery := s.verifyCode(r, user, in.Code)
	if !ok {
		s.mfa.fail(in.Ticket)
		s.Store.Audit(r.Context(), user, "auth.mfa_failed", "user", user.ID, nil, clientIP(r))
		return httpx.Invalid(map[string]string{"code": "That code isn't right. Codes change every 30 seconds; recovery codes work once."})
	}
	s.mfa.done(in.Ticket)
	detail := map[string]any{"method": t.method, "mfa": true}
	if recovery {
		m, _ := s.Store.GetMFA(r.Context(), user.ID)
		detail["recoveryCode"], detail["recoveryCodesLeft"] = true, len(m.RecoveryCodes)
	}
	s.Store.Audit(r.Context(), user, "auth.login", "user", user.ID, detail, clientIP(r))
	return s.startSession(w, r, user, t.method+"+totp", map[string]any{"usedRecoveryCode": recovery})
}

// mfaSetupStart begins enrollment, either during a sign-in that requires it (ticket) or from
// the signed-in account (user).
func (s *Server) enrollStart(r *http.Request, user *store.User) (map[string]any, string, error) {
	m, err := s.Store.GetMFA(r.Context(), user.ID)
	if err != nil {
		return nil, "", err
	}
	if m.EnabledAt != nil {
		return nil, "", httpx.Conflict("Two-factor authentication is already on.")
	}
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		return nil, "", err
	}
	sealed, err := s.Secrets.Seal(secret, "totp:"+user.ID)
	if err != nil {
		return nil, "", err
	}
	if err := s.Store.SetPendingTOTP(r.Context(), user.ID, sealed); err != nil {
		return nil, "", err
	}
	issuer := s.instance(r.Context()).Name
	return map[string]any{"secret": secret, "url": auth.TOTPURL(issuer, user.Email, secret), "issuer": issuer, "account": user.Email}, secret, nil
}

// enrollFinish confirms the first code and turns MFA on, returning fresh recovery codes.
func (s *Server) enrollFinish(r *http.Request, user *store.User, code string) ([]string, error) {
	m, err := s.Store.GetMFA(r.Context(), user.ID)
	if err != nil {
		return nil, err
	}
	if m.EnabledAt != nil {
		return nil, httpx.Conflict("Two-factor authentication is already on.")
	}
	if m.SealedSecret == nil {
		return nil, httpx.Conflict("Start setup again.")
	}
	secret, err := s.Secrets.Open(m.SealedSecret, "totp:"+user.ID)
	if err != nil {
		return nil, err
	}
	step, ok := auth.VerifyTOTP(secret, code, time.Now())
	if !ok || !s.mfa.acceptStep(user.ID, step) {
		return nil, httpx.Invalid(map[string]string{"code": "That code doesn't match. Check the time on your phone and try the newest code."})
	}
	codes, hashes, err := auth.NewRecoveryCodes(recoveryCodeLen)
	if err != nil {
		return nil, err
	}
	if err := s.Store.EnableTOTP(r.Context(), user.ID, hashes); err != nil {
		return nil, err
	}
	s.Store.Audit(r.Context(), user, "auth.mfa_enabled", "user", user.ID, nil, clientIP(r))
	return codes, nil
}

func (s *Server) mfaSetupWithTicket(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Ticket string }
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	t, ok := s.mfa.get(in.Ticket)
	if !ok || !t.setup {
		return ticketExpired()
	}
	user, err := s.Store.GetUser(r.Context(), t.userID)
	if err != nil {
		return ticketExpired()
	}
	out, _, err := s.enrollStart(r, user)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, out)
	return nil
}

func (s *Server) mfaConfirmWithTicket(w http.ResponseWriter, r *http.Request) error {
	if !s.limiter.allowMax("mfa:"+clientIP(r), s.policy().RateLimit) {
		return httpx.TooMany("Too many attempts. Wait a minute.")
	}
	var in struct{ Ticket, Code string }
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	t, ok := s.mfa.get(in.Ticket)
	if !ok || !t.setup {
		return ticketExpired()
	}
	user, err := s.Store.GetUser(r.Context(), t.userID)
	if err != nil || user.Status != "active" {
		return ticketExpired()
	}
	codes, err := s.enrollFinish(r, user, in.Code)
	if err != nil {
		s.mfa.fail(in.Ticket)
		return err
	}
	s.mfa.done(in.Ticket)
	user, _ = s.Store.GetUser(r.Context(), user.ID)
	s.Store.Audit(r.Context(), user, "auth.login", "user", user.ID, map[string]any{"method": t.method, "mfa": true, "enrolled": true}, clientIP(r))
	return s.startSession(w, r, user, t.method+"+totp", map[string]any{"recoveryCodes": codes})
}

/* ------------------------------ Your account -------------------------------- */

func (s *Server) accountSecurity(w http.ResponseWriter, r *http.Request, u *store.User) error {
	m, err := s.Store.GetMFA(r.Context(), u.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{
		"mfaEnabled": m.EnabledAt != nil, "mfaEnabledAt": m.EnabledAt, "recoveryCodesLeft": len(m.RecoveryCodes),
		"mfaEnforced": s.policy().EnforceMFA, "mfaNotice": s.mfaNotice(u), "sso": u.SSO, "hasPassword": u.HasPassword, "permissions": effectivePermissions(u), "role": u.Role,
		"invitations": s.policy().Invitations,
	})
	return nil
}

func (s *Server) accountMFASetup(w http.ResponseWriter, r *http.Request, u *store.User) error {
	out, _, err := s.enrollStart(r, u)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, out)
	return nil
}

func (s *Server) accountMFAEnable(w http.ResponseWriter, r *http.Request, u *store.User) error {
	var in struct{ Code string }
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	codes, err := s.enrollFinish(r, u, in.Code)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"recoveryCodes": codes})
	return nil
}

func (s *Server) accountMFADisable(w http.ResponseWriter, r *http.Request, u *store.User) error {
	var in struct{ Code string }
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if s.policy().EnforceMFA && !mfaExempt(u) {
		return httpx.Forbidden("Two-factor authentication is required on this Ignition, so it can't be turned off.")
	}
	if ok, _ := s.verifyCode(r, u, in.Code); !ok {
		return httpx.Invalid(map[string]string{"code": "Enter a current code (or a recovery code) to confirm."})
	}
	if err := s.Store.DisableTOTP(r.Context(), u.ID); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "auth.mfa_disabled", "user", u.ID, nil, clientIP(r))
	httpx.JSON(w, 204, nil)
	return nil
}

func (s *Server) accountRecoveryCodes(w http.ResponseWriter, r *http.Request, u *store.User) error {
	var in struct{ Code string }
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if ok, _ := s.verifyCode(r, u, in.Code); !ok {
		return httpx.Invalid(map[string]string{"code": "Enter a current code from your authenticator app."})
	}
	codes, hashes, err := auth.NewRecoveryCodes(recoveryCodeLen)
	if err != nil {
		return err
	}
	if err := s.Store.SetRecoveryCodes(r.Context(), u.ID, hashes); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "auth.recovery_codes_regenerated", "user", u.ID, nil, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"recoveryCodes": codes})
	return nil
}

/* ---------------------------------- Admin ----------------------------------- */

// resetUserMFA turns someone's second factor off (lost phone and codes). Their sessions end, and
// with MFA enforced they enroll again at the next sign-in.
func (s *Server) resetUserMFA(w http.ResponseWriter, r *http.Request, actor *store.User) error {
	target, err := s.userFor(r)
	if err != nil {
		return err
	}
	if target.ID == actor.ID {
		return httpx.Forbidden("Turn your own two-factor authentication off from your account instead.")
	}
	if !canManage(actor, target.Role) {
		return httpx.Forbidden("Only owners can manage owners.")
	}
	if err := s.Store.DisableTOTP(r.Context(), target.ID); err != nil {
		return err
	}
	s.Store.RevokeSessions(r.Context(), target.ID)
	s.Store.Audit(r.Context(), actor, "user.mfa_reset", "user", target.ID, map[string]any{"email": target.Email}, clientIP(r))
	httpx.JSON(w, 204, nil)
	return nil
}

/* ------------------------------ Enforcement deadline ------------------------------ */

// mfaNotice is the announcement for someone who still has to set up two-factor, or nil.
func (s *Server) mfaNotice(u *store.User) map[string]any {
	p := s.policy()
	if !p.EnforceMFA || u.MFAEnabledAt != nil || mfaExempt(u) {
		return nil
	}
	action := p.MFADeadlineAction
	if u.Role == "owner" {
		action = "enroll" // owners are never suspended: someone has to be able to get back in
	}
	return map[string]any{"deadline": p.MFADeadline, "deadlineAt": nullTime(p.deadline()), "action": action, "message": p.MFAMessage, "grace": p.mfaGrace()}
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// mfaDeadlineCheck runs on every signed-in request. Once the deadline has passed, a session of
// someone without two-factor ends: they set it up at the next sign-in, or, when the policy says
// so, their account is suspended (never an owner's).
func (s *Server) mfaDeadlineCheck(w http.ResponseWriter, r *http.Request, u *store.User, token string) error {
	p := s.policy()
	if !p.EnforceMFA || u.MFAEnabledAt != nil || mfaExempt(u) || p.deadline().IsZero() || p.mfaGrace() {
		return nil
	}
	if p.MFADeadlineAction == "suspend" && u.Role != "owner" {
		s.sweepMFADeadline(r)
		return &httpx.Error{Status: 401, Code: "mfa_deadline_suspended", Message: "Your account was suspended because two-factor authentication wasn't set up by " + p.MFADeadline + ". Ask an admin to reactivate it."}
	}
	s.Store.DeleteSession(r.Context(), auth.HashToken(token))
	return &httpx.Error{Status: 401, Code: "mfa_required", Message: "Two-factor authentication is now required. Sign in again to set it up."}
}

// sweepMFADeadline suspends everyone (except owners and SSO-only accounts) who missed the
// deadline, including people who haven't signed in since, so the Users page shows it.
func (s *Server) sweepMFADeadline(r *http.Request) {
	p := s.policy()
	if !p.EnforceMFA || p.MFADeadlineAction != "suspend" || p.deadline().IsZero() || p.mfaGrace() {
		return
	}
	emails, err := s.Store.SuspendWithoutMFA(r.Context())
	if err != nil {
		return
	}
	for _, e := range emails {
		s.Store.Audit(r.Context(), nil, "user.suspend", "user", "", map[string]any{"email": e, "reason": "Two-factor authentication not set up by " + p.MFADeadline}, "")
	}
}

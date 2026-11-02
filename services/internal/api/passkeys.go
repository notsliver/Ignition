package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

type passkeyUser struct {
	*store.User
	keys []webauthn.Credential
}

func (u passkeyUser) WebAuthnID() []byte                         { return []byte(u.ID) }
func (u passkeyUser) WebAuthnName() string                       { return u.Email }
func (u passkeyUser) WebAuthnDisplayName() string                { return u.Name }
func (u passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.keys }
func (s *Server) webAuthn() (*webauthn.WebAuthn, error) {
	origin, err := url.Parse(s.publicURL())
	if err != nil || origin.Hostname() == "" {
		return nil, httpx.Unavailable("Set the public dashboard URL before using passkeys.")
	}
	if origin.Scheme != "https" && (origin.Scheme != "http" || origin.Hostname() != "localhost" && origin.Hostname() != "127.0.0.1" && origin.Hostname() != "::1") {
		return nil, httpx.Unavailable("Passkeys need HTTPS or localhost.")
	}
	return webauthn.New(&webauthn.Config{RPDisplayName: "Ignition", RPID: origin.Hostname(), RPOrigins: []string{origin.Scheme + "://" + origin.Host}, AuthenticatorSelection: protocol.AuthenticatorSelection{ResidentKey: protocol.ResidentKeyRequirementRequired, UserVerification: protocol.VerificationRequired}})
}
func (s *Server) passkeyUser(r *http.Request, u *store.User) (passkeyUser, error) {
	keys, err := s.Store.AccountPasskeys(r.Context(), u.ID)
	out := passkeyUser{User: u, keys: []webauthn.Credential{}}
	for _, p := range keys {
		out.keys = append(out.keys, p.Credential)
	}
	return out, err
}
func (s *Server) accountPasskeys(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if _, err := accountSession(r, u); err != nil {
		return err
	}
	if r.Method == "DELETE" {
		if err := s.requireRecentAccount(r, u); err != nil {
			return err
		}
		id := r.PathValue("id")
		if !uuidRE.MatchString(id) {
			return httpx.NotFound("Passkey")
		}
		o := s.oidcConfig(r.Context())
		issuer := ""
		if s.policy().OIDCAuth && o.Configured() {
			issuer = o.Issuer
		}
		local := u.Role == "owner" || s.policy().LocalAuth && s.policy().RequiredMethod != "sso"
		if err := s.Store.DeleteAccountPasskey(r.Context(), u.ID, id, local, issuer); err != nil {
			if errors.Is(err, store.ErrLastSignInMethod) {
				return httpx.Conflict(err.Error())
			}
			return notFound(err, "Passkey")
		}
		s.Store.Audit(r.Context(), u, "account.passkey_removed", "user", u.ID, nil, clientIP(r))
		httpx.JSON(w, 204, nil)
		return nil
	}
	keys, err := s.Store.AccountPasskeys(r.Context(), u.ID)
	if err != nil {
		return err
	}
	_, configErr := s.webAuthn()
	local := u.Role == "owner" || s.policy().LocalAuth && s.policy().RequiredMethod != "sso"
	reason := ""
	if configErr != nil {
		reason = configErr.Error()
	}
	if !local {
		reason = "This instance requires single sign-on."
	}
	httpx.JSON(w, 200, map[string]any{"passkeys": keys, "available": configErr == nil && local, "reason": reason})
	return nil
}
func (s *Server) accountPasskeyBegin(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := s.requireRecentAccount(r, u); err != nil {
		return err
	}
	if u.Role != "owner" && (!s.policy().LocalAuth || s.policy().RequiredMethod == "sso") {
		return httpx.Forbidden("This instance requires single sign-on.")
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 80 {
		return httpx.Invalid(map[string]string{"name": "Name this passkey (up to 80 characters)."})
	}
	user, err := s.passkeyUser(r, u)
	if err != nil {
		return err
	}
	if len(user.keys) >= 20 {
		return httpx.Conflict("You can have up to 20 passkeys.")
	}
	wa, err := s.webAuthn()
	if err != nil {
		return err
	}
	exclude := []protocol.CredentialDescriptor{}
	for _, c := range user.keys {
		exclude = append(exclude, c.Descriptor())
	}
	options, data, err := wa.BeginRegistration(user, webauthn.WithExclusions(exclude))
	if err != nil {
		return err
	}
	hash, _ := accountSession(r, u)
	return s.savePasskeyFlow(w, r, hash, u.ID, "register", in.Name, options, data)
}
func (s *Server) savePasskeyFlow(w http.ResponseWriter, r *http.Request, browser []byte, user, purpose, name string, options any, data *webauthn.SessionData) error {
	token, hash, err := auth.NewToken()
	if err != nil {
		return err
	}
	if err = s.Store.SaveAccountChallenge(r.Context(), hash, browser, user, purpose, name, data); err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"options": options, "flowId": token})
	return nil
}
func (s *Server) accountPasskeyFinish(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := s.requireRecentAccount(r, u); err != nil {
		return err
	}
	hash, _ := accountSession(r, u)
	flow, credential, err := passkeyResponse(w, r)
	if err != nil {
		return err
	}
	userID, name, data, err := s.Store.TakeAccountChallenge(r.Context(), auth.HashToken(flow), hash, "register")
	if err != nil || userID != u.ID {
		return httpx.BadRequest("Passkey setup expired. Start again.")
	}
	wa, err := s.webAuthn()
	if err != nil {
		return err
	}
	user, err := s.passkeyUser(r, u)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(credential))
	key, err := wa.FinishRegistration(user, data, r)
	if err != nil {
		return httpx.BadRequest("Passkey verification failed. Start again.")
	}
	if err = s.Store.AddAccountPasskey(r.Context(), u.ID, name, key); err != nil {
		if store.IsUnique(err) {
			return httpx.Conflict("This passkey is already registered.")
		}
		return err
	}
	s.Store.Audit(r.Context(), u, "account.passkey_added", "user", u.ID, nil, clientIP(r))
	httpx.JSON(w, 201, map[string]any{"saved": true})
	return nil
}
func passkeyResponse(w http.ResponseWriter, r *http.Request) (string, json.RawMessage, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var in struct {
		FlowID     string          `json:"flowId"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return "", nil, err
	}
	if in.FlowID == "" || len(in.Credential) == 0 {
		return "", nil, httpx.BadRequest("Missing passkey response.")
	}
	return in.FlowID, in.Credential, nil
}

const passkeyCookie = "ignition_passkey"

func (s *Server) passkeyLoginBegin(w http.ResponseWriter, r *http.Request) error {
	if !s.limiter.allowMax("passkey-begin:"+clientIP(r), s.policy().RateLimit) {
		return httpx.TooMany("Too many attempts. Wait a minute.")
	}
	wa, err := s.webAuthn()
	if err != nil {
		return err
	}
	options, data, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return err
	}
	browser, hash, err := auth.NewToken()
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: passkeyCookie, Value: browser, Path: "/api/auth/passkeys", HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: 300})
	return s.savePasskeyFlow(w, r, hash, "", "login", "", options, data)
}
func (s *Server) passkeyLoginFinish(w http.ResponseWriter, r *http.Request) error {
	if !s.limiter.allowMax("passkey-finish:"+clientIP(r), s.policy().RateLimit) {
		return httpx.TooMany("Too many attempts. Wait a minute.")
	}
	c, err := r.Cookie(passkeyCookie)
	if err != nil {
		return httpx.Unauthorized()
	}
	flow, response, err := passkeyResponse(w, r)
	if err != nil {
		return err
	}
	_, _, data, err := s.Store.TakeAccountChallenge(r.Context(), auth.HashToken(flow), auth.HashToken(c.Value), "login")
	if err != nil {
		return httpx.BadRequest("Passkey sign-in expired. Try again.")
	}
	var user passkeyUser
	wa, err := s.webAuthn()
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(response))
	key, err := wa.FinishDiscoverableLogin(func(rawID, handle []byte) (webauthn.User, error) {
		u, err := s.Store.PasskeyOwner(r.Context(), rawID)
		if err != nil || u.Status != "active" || !bytes.Equal(handle, []byte(u.ID)) {
			return nil, httpx.Unauthorized()
		}
		if u.Role != "owner" && (!s.policy().LocalAuth || s.policy().RequiredMethod == "sso") {
			return nil, httpx.Forbidden("Single sign-on is required.")
		}
		user, err = s.passkeyUser(r, u)
		return user, err
	}, data, r)
	if err != nil || key == nil || key.Authenticator.CloneWarning {
		return httpx.BadRequest("This passkey couldn't be verified. Try another sign-in method.")
	}
	for _, old := range user.keys {
		if bytes.Equal(old.ID, key.ID) {
			if err = s.Store.UpdateAccountPasskey(r.Context(), user.ID, old, *key); err != nil {
				return httpx.BadRequest("The passkey changed. Try again.")
			}
			break
		}
	}
	return s.secondFactor(w, r, user.User, "passkey")
}

func (s *Server) accountPasskeyReauthBegin(w http.ResponseWriter, r *http.Request, u *store.User) error {
	hash, err := accountSession(r, u)
	if err != nil {
		return err
	}
	if !s.limiter.allowMax("account-reauth:"+u.ID, s.policy().MaxAttempts) {
		return httpx.TooMany("Too many attempts. Wait a minute.")
	}
	wa, err := s.webAuthn()
	if err != nil {
		return err
	}
	user, err := s.passkeyUser(r, u)
	if err != nil {
		return err
	}
	options, data, err := wa.BeginLogin(user, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return httpx.BadRequest("No usable passkeys are registered.")
	}
	return s.savePasskeyFlow(w, r, hash, u.ID, "reauth", "", options, data)
}
func (s *Server) accountPasskeyReauthFinish(w http.ResponseWriter, r *http.Request, u *store.User) error {
	hash, err := accountSession(r, u)
	if err != nil {
		return err
	}
	flow, response, err := passkeyResponse(w, r)
	if err != nil {
		return err
	}
	id, _, data, err := s.Store.TakeAccountChallenge(r.Context(), auth.HashToken(flow), hash, "reauth")
	if err != nil || id != u.ID {
		return httpx.BadRequest("Confirmation expired. Try again.")
	}
	user, err := s.passkeyUser(r, u)
	if err != nil {
		return err
	}
	wa, err := s.webAuthn()
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(response))
	key, err := wa.FinishLogin(user, data, r)
	if err != nil || key == nil || key.Authenticator.CloneWarning {
		return httpx.BadRequest("This passkey couldn't be verified.")
	}
	for _, old := range user.keys {
		if bytes.Equal(old.ID, key.ID) {
			if err = s.Store.UpdateAccountPasskey(r.Context(), u.ID, old, *key); err != nil {
				return err
			}
			break
		}
	}
	if err = s.Store.ReauthenticateSession(r.Context(), u.ID, hash); err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"confirmed": true})
	return nil
}

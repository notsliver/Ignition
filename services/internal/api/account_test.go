package api

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/database"
	"github.com/notsliver/Ignition/services/internal/secrets"
	"github.com/notsliver/Ignition/services/internal/store"
)

func TestAccountProfileSecurityAndOwnership(t *testing.T) {
	db := os.Getenv("IGNITION_TEST_DATABASE_URL")
	if db == "" {
		t.Skip("IGNITION_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	box, _ := secrets.New(make([]byte, 32))
	s := &Server{Store: &store.Store{DB: pool}, PublicURL: "http://localhost", Secrets: box, UploadsDir: t.TempDir()}
	p := defaultPolicy()
	p.BreachedPasswords = false
	s.setPolicy(p)
	password := "Long-test-password-92"
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	suffix := time.Now().Format("150405.000000000")
	u, err := s.Store.CreateUser(ctx, "account-"+suffix+"@test.example", "Account", passwordHash, "viewer")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Store.CreateUser(ctx, "other-account-"+suffix+"@test.example", "Other", passwordHash, "viewer")
	if err != nil {
		t.Fatal(err)
	}
	tokens := make([]string, 3)
	for i := range tokens {
		token, hash, _ := auth.NewToken()
		tokens[i] = token
		user := u
		if i == 2 {
			user = other
		}
		if err = s.Store.CreateSession(ctx, user.ID, hash, "Account test", "127.0.0.1", time.Hour, "password"); err != nil {
			t.Fatal(err)
		}
	}
	handler := s.Handler()
	call := func(who int, method, path string, in any, want int) map[string]any {
		t.Helper()
		var body bytes.Buffer
		if in != nil {
			json.NewEncoder(&body).Encode(in)
		}
		r := httptest.NewRequest(method, "http://localhost"+path, &body)
		r.Header.Set("Origin", "http://localhost")
		r.Header.Set("Content-Type", "application/json")
		if who >= 0 {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tokens[who]})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		for _, cookie := range w.Result().Cookies() {
			if who >= 0 && cookie.Name == sessionCookie && cookie.Value != "" {
				tokens[who] = cookie.Value
			}
		}
		out := map[string]any{}
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	call(-1, "GET", "/api/account/profile", nil, 401)
	call(0, "PATCH", "/api/account/profile", map[string]string{"name": "Updated name", "email": u.Email}, 200)
	newEmail := "new-" + suffix + "@test.example"
	call(0, "PATCH", "/api/account/profile", map[string]string{"name": "Updated name", "email": newEmail}, 403)
	call(0, "POST", "/api/account/mfa/setup", nil, 403)
	call(0, "POST", "/api/account/reauth", map[string]string{"password": "incorrect"}, 422)
	call(0, "POST", "/api/account/reauth", map[string]string{"password": password}, 200)
	call(0, "PATCH", "/api/account/profile", map[string]string{"name": "Updated name", "email": other.Email}, 422)
	call(0, "PATCH", "/api/account/profile", map[string]string{"name": "Updated name", "email": newEmail}, 200)
	call(1, "GET", "/api/account/profile", nil, 401)
	listed := call(2, "GET", "/api/account/sessions", nil, 200)["sessions"].([]any)
	otherSession := listed[0].(map[string]any)["id"].(string)
	call(0, "DELETE", "/api/account/sessions/"+otherSession, nil, 404)
	call(2, "GET", "/api/account/profile", nil, 200)
	call(0, "POST", "/api/account/passkeys/register/begin", map[string]string{"name": "Laptop"}, 200)
	// No assertion can be verified without a server-issued, session-bound challenge.
	call(2, "POST", "/api/account/passkeys/register/finish", map[string]any{"flowId": "missing", "credential": map[string]any{}}, 403)
	call(0, "POST", "/api/account/passkeys/register/finish", map[string]any{"flowId": "missing", "credential": map[string]any{}}, 400)
	// Linking is explicit and issuer-scoped, never an implicit match by email.
	if err = s.Store.LinkOIDC(ctx, u.ID, "https://issuer.test", "subject-"+suffix); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Store.UserByOIDCIdentity(ctx, "https://different.test", "subject-"+suffix); err != store.ErrNotFound {
		t.Fatalf("issuer boundary: %v", err)
	}
	call(0, "GET", "/api/account/integrations", nil, 200)
	call(0, "DELETE", "/api/account/integrations/sso", nil, 204)
	if err = s.Store.LinkOIDC(ctx, other.ID, "https://issuer.test", "subject-"+suffix); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET password_hash='' WHERE id=$1`, other.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Store.UnlinkAccountOIDC(ctx, other.ID, auth.HashToken(tokens[2])); err != store.ErrLastSignInMethod {
		t.Fatalf("last sign-in method: %v", err)
	}
	// Public credential metadata is managed only by its owner.
	if err = s.Store.AddAccountPasskey(ctx, u.ID, "Test device", &webauthn.Credential{ID: []byte("key-" + suffix)}); err != nil {
		t.Fatal(err)
	}
	keyList := call(0, "GET", "/api/account/passkeys", nil, 200)["passkeys"].([]any)
	keyID := keyList[0].(map[string]any)["id"].(string)
	if _, ok := keyList[0].(map[string]any)["credential"]; ok {
		t.Fatal("private credential storage leaked into management response")
	}
	if err = s.Store.ReauthenticateSession(ctx, other.ID, auth.HashToken(tokens[2])); err != nil {
		t.Fatal(err)
	}
	call(2, "DELETE", "/api/account/passkeys/"+keyID, nil, 404)
	call(0, "DELETE", "/api/account/passkeys/"+keyID, nil, 204)
	call(0, "DELETE", "/api/account/passkeys/"+keyID, nil, 404)
	// Password changes rotate the current session and revoke the old token.
	previousToken := tokens[0]
	newPassword := "Another-long-test-password-93"
	call(0, "PUT", "/api/account/password", map[string]string{"password": newPassword}, 200)
	if tokens[0] == previousToken {
		t.Fatal("password change did not rotate the session")
	}
	oldRequest := httptest.NewRequest("GET", "http://localhost/api/account/profile", nil)
	oldRequest.AddCookie(&http.Cookie{Name: sessionCookie, Value: previousToken})
	oldResponse := httptest.NewRecorder()
	handler.ServeHTTP(oldResponse, oldRequest)
	if oldResponse.Code != 401 {
		t.Fatal("old session survived password change")
	}
	call(0, "POST", "/api/account/reauth", map[string]string{"password": newPassword}, 200)
	// A sole team owner must transfer ownership before self-deletion.
	team, err := s.Store.CreateTeam(ctx, u.ID, "Account ownership")
	if err != nil {
		t.Fatal(err)
	}
	call(0, "DELETE", "/api/account/profile", map[string]string{"email": newEmail}, 409)
	if _, err = pool.Exec(ctx, `INSERT INTO team_memberships(team_id,user_id,role) VALUES($1,$2,'owner')`, team.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	call(0, "DELETE", "/api/account/profile", map[string]string{"email": "wrong@test.example"}, 422)
	call(0, "DELETE", "/api/account/profile", map[string]string{"email": newEmail}, 204)
	call(0, "GET", "/api/account/profile", nil, 401)
}

func TestAccountSSOLinkingAndSessionBinding(t *testing.T) {
	db := os.Getenv("IGNITION_TEST_DATABASE_URL")
	if db == "" {
		t.Skip("IGNITION_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	st := &store.Store{DB: pool}
	box, _ := secrets.New(make([]byte, 32))
	s := &Server{Store: st, Secrets: box, PublicURL: "http://localhost"}
	policy := defaultPolicy()
	policy.OIDCAuth = true
	s.setPolicy(policy)
	var original oidcSettings
	if err = st.GetSetting(ctx, "oidc", &original); err != nil {
		t.Fatal(err)
	}
	defer st.SetSetting(ctx, "oidc", original)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var assertedAuthTime atomic.Int64
	assertedAuthTime.Store(time.Now().Unix())
	var idToken atomic.Value
	idToken.Store("")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/keys" {
			json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "account-test", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id_token": idToken.Load()})
	}))
	defer provider.Close()
	issuer := "https://sso-account.example.test"
	now := time.Now()
	settings := oidcSettings{Issuer: issuer, ClientID: "ignition", Label: "Company SSO", VerifiedAt: &now}
	if err = st.SetSetting(ctx, "oidc", settings); err != nil {
		t.Fatal(err)
	}
	s.sso.provider = &auth.OIDCProvider{Issuer: issuer, AuthorizationEndpoint: issuer + "/authorize", TokenEndpoint: provider.URL + "/token", JWKSURI: provider.URL + "/keys"}
	s.sso.issuer = issuer
	s.sso.fetched = now
	u, err := st.CreateUser(ctx, "sso-link-"+now.Format("150405.000000000")+"@example.test", "SSO link", "hash", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	token, hash, _ := auth.NewToken()
	if err = st.CreateSession(ctx, u.ID, hash, "test", "", time.Hour, "password"); err != nil {
		t.Fatal(err)
	}
	if err = st.ReauthenticateSession(ctx, u.ID, hash); err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	begin := func(action string) (state string, cookie *http.Cookie) {
		t.Helper()
		r := httptest.NewRequest("POST", "http://localhost/api/account/integrations/sso/"+action, strings.NewReader(`{"returnTo":"/teams/test/settings/profile"}`))
		r.Header.Set("Origin", "http://localhost")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("SSO begin: %d %s", w.Code, w.Body.String())
		}
		var out struct {
			URL string `json:"url"`
		}
		json.Unmarshal(w.Body.Bytes(), &out)
		authorize, _ := url.Parse(out.URL)
		state = authorize.Query().Get("state")
		nonce := authorize.Query().Get("nonce")
		if action == "reauth" && (authorize.Query().Get("prompt") != "login" || authorize.Query().Get("max_age") != "0") {
			t.Fatal("SSO reauthentication must demand a fresh provider login")
		}
		header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "account-test"})
		claims, _ := json.Marshal(map[string]any{"iss": issuer, "sub": "subject-one" + now.Format("150405.000000000"), "aud": "ignition", "nonce": nonce, "exp": time.Now().Add(time.Minute).Unix(), "email": u.Email, "email_verified": true, "auth_time": assertedAuthTime.Load()})
		signed := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
		sum := sha256.Sum256([]byte(signed))
		signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		if err != nil {
			t.Fatal(err)
		}
		idToken.Store(signed + "." + base64.RawURLEncoding.EncodeToString(signature))
		for _, c := range w.Result().Cookies() {
			if c.Name == ssoCookie {
				cookie = c
			}
		}
		if cookie == nil {
			t.Fatal("missing browser state cookie")
		}
		return
	}
	finish := func(state string, cookie *http.Cookie, session string) string {
		t.Helper()
		r := httptest.NewRequest("GET", "http://localhost/api/auth/oidc/callback?code=code&state="+url.QueryEscape(state), nil)
		r.AddCookie(cookie)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 302 {
			t.Fatalf("SSO callback: %d %s", w.Code, w.Body.String())
		}
		return w.Header().Get("Location")
	}
	state, cookie := begin("connect")
	if location := finish(state, cookie, "changed-session"); !strings.Contains(location, "account_error=") {
		t.Fatalf("changed session accepted: %s", location)
	}
	if _, subject, err := st.AccountOIDC(ctx, u.ID); err != nil || subject != "" {
		t.Fatalf("identity linked without session proof: %s %v", subject, err)
	}
	state, cookie = begin("connect")
	if location := finish(state, cookie, token); !strings.Contains(location, "account_connected=1") {
		t.Fatalf("link failed: %s", location)
	}
	if _, subject, err := st.AccountOIDC(ctx, u.ID); err != nil || subject != "subject-one"+now.Format("150405.000000000") {
		t.Fatalf("identity not linked: %s %v", subject, err)
	}
	if location := finish(state, cookie, token); !strings.Contains(location, "sso_error=") {
		t.Fatal("SSO state was replayed")
	}
	assertedAuthTime.Store(time.Now().Add(-time.Hour).Unix())
	state, cookie = begin("reauth")
	if location := finish(state, cookie, token); !strings.Contains(location, "account_error=") {
		t.Fatal("stale provider authentication was accepted")
	}
	assertedAuthTime.Store(time.Now().Unix())
	state, cookie = begin("reauth")
	if location := finish(state, cookie, token); !strings.Contains(location, "account_connected=1") {
		t.Fatalf("SSO reauthentication failed: %s", location)
	}
	// A matching verified email alone must not attach a different SSO subject.
	verified := true
	if _, err = s.ssoUser(httptest.NewRequest("GET", "http://localhost", nil), settings, &auth.Claims{Subject: "different-subject", Email: u.Email, EmailVerified: &verified}); err == nil {
		t.Fatal("implicit SSO account linking was allowed")
	}
}

func TestAccountReturnPaths(t *testing.T) {
	for _, path := range []string{"https://attacker.test/", "//attacker.test/", "/teams/test/settings/profile?redirect=https://attacker.test", "/teams/../settings/profile"} {
		if accountReturnRE.MatchString(path) {
			t.Fatalf("unsafe return path: %s", path)
		}
	}
	for _, path := range []string{"/teams/xm6wzpjdsgr1w7by/settings/profile", "/teams/test/settings/security"} {
		if !accountReturnRE.MatchString(path) {
			t.Fatalf("valid return path: %s", path)
		}
	}
}

func TestAccountEndpointsRejectServiceCredentials(t *testing.T) {
	s := &Server{}
	for _, fn := range []func(http.ResponseWriter, *http.Request, *store.User) error{s.accountProfile, s.accountSessions, s.accountIntegrations, s.accountPasskeys, s.accountPasskeyBegin, s.accountPasskeyReauthBegin} {
		err := fn(httptest.NewRecorder(), httptest.NewRequest("GET", "http://localhost/api/account/profile", nil), &store.User{ID: "issuer", CredentialTokenID: "resource"})
		if err == nil || !strings.Contains(err.Error(), "signed-in") {
			t.Fatalf("resource token rejection: %v", err)
		}
	}
}

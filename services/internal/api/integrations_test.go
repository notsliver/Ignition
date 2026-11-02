package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/database"
	"github.com/notsliver/Ignition/services/internal/integrations"
	"github.com/notsliver/Ignition/services/internal/secrets"
	"github.com/notsliver/Ignition/services/internal/store"
)

func TestManagedIntegrationsOAuthAndTenantBoundaries(t *testing.T) {
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
	server := &Server{Store: st, Secrets: box, PublicURL: "http://localhost"}
	policy := defaultPolicy()
	policy.RateLimit = 100
	server.setPolicy(policy)
	original, err := st.Integration(ctx, "github")
	if err != nil {
		t.Fatal(err)
	}
	originalMode := st.SourceMode(ctx)
	defer st.SetSetting(ctx, "source_mode", originalMode)
	defer func() { latest, _ := st.Integration(ctx, "github"); st.SaveIntegration(ctx, original, latest.Revision) }()
	var subject atomic.Int64
	subject.Store(100)
	var scope atomic.Value
	scope.Store("read:user user:email")
	var refreshes atomic.Int64
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login/oauth/access_token":
			r.ParseForm()
			access := "private-access-token"
			refresh := "private-refresh-token"
			if r.Form.Get("grant_type") == "refresh_token" {
				refreshes.Add(1)
				access = "refreshed-access-token"
				refresh = "rotated-refresh-token"
			}
			if r.Form.Get("client_secret") == "" {
				t.Error("token exchange omitted client authentication")
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": refresh, "expires_in": 3600, "scope": scope.Load(), "token_type": "bearer"})
		case "/api/v3/user":
			if r.Header.Get("Authorization") == "" {
				t.Error("identity lookup omitted bearer token")
			}
			json.NewEncoder(w).Encode(map[string]any{"id": subject.Load(), "login": "git-person"})
		case "/api/v3/user/emails":
			json.NewEncoder(w).Encode([]map[string]any{{"email": "verified@example.test", "primary": true, "verified": true}})
		case "/api/v3/user/repos":
			json.NewEncoder(w).Encode([]map[string]any{{"full_name": "team/private-app", "clone_url": providerPlaceholder(r) + "/team/private-app.git", "default_branch": "main", "private": true}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	transport := http.DefaultTransport
	http.DefaultTransport = provider.Client().Transport
	defer func() { http.DefaultTransport = transport }()
	c := integrations.Defaults(original)
	c.Enabled = true
	c.SignInEnabled = true
	c.SourcesEnabled = true
	c.BaseURL = provider.URL
	c.SourceRevision = 1
	c.Login.ClientID = "login-client"
	c.Repositories.ClientID = "source-client"
	c.LoginSecretEnc, _ = box.Seal("login-client-secret", "integration:github:login")
	c.SourceSecretEnc, _ = box.Seal("source-client-secret", "integration:github:source")
	c.Login.HasSecret = true
	c.Repositories.HasSecret = true
	c.Repositories.Scopes = []string{"read:user"}
	if err = st.SaveIntegration(ctx, c, original.Revision); err != nil {
		t.Fatal(err)
	}
	c, _ = st.Integration(ctx, "github")
	users := make([]*store.User, 4)
	tokens := make([]string, 4)
	suffix := time.Now().Format("150405.000000000")
	for i := range users {
		role := "viewer"
		if i == 2 {
			role = "admin"
		}
		users[i], err = st.CreateUser(ctx, role+string(rune('a'+i))+suffix+"@integrations.test", "Integration user", "hash", role)
		if err != nil {
			t.Fatal(err)
		}
		token, hash, _ := auth.NewToken()
		tokens[i] = token
		if err = st.CreateSession(ctx, users[i].ID, hash, "test", "", time.Hour, "password"); err != nil {
			t.Fatal(err)
		}
		st.ReauthenticateSession(ctx, users[i].ID, hash)
	}
	a, err := st.CreateTeam(ctx, users[0].ID, "Integration A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateTeam(ctx, users[1].ID, "Integration B")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO team_memberships(team_id,user_id,role) VALUES($1,$2,'viewer')`, a.ID, users[3].ID); err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	call := func(who int, method, path string, input any, want int) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		if input != nil {
			json.NewEncoder(&body).Encode(input)
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
			t.Fatalf("%s %s actor %d: %d want %d: %s", method, path, who, w.Code, want, w.Body.String())
		}
		return w
	}
	call(0, "GET", "/api/settings/integrations", nil, 403)
	settings := call(2, "GET", "/api/settings/integrations", nil, 200)
	if strings.Contains(settings.Body.String(), "client-secret") {
		t.Fatal("admin response leaked client secrets")
	}
	call(0, "POST", "/api/teams/"+b.ID+"/integrations/github/connect", map[string]string{"returnTo": "/teams/test/settings/profile"}, 403)
	call(3, "POST", "/api/teams/"+a.ID+"/integrations/github/connect", map[string]string{"returnTo": "/teams/test/settings/profile"}, 403)
	if err = st.SetSetting(ctx, "source_mode", "managed"); err != nil {
		t.Fatal(err)
	}
	call(0, "POST", "/api/teams/"+a.ID+"/sources", map[string]string{"provider": "dockerhub", "name": "manual-bypass", "authType": "none"}, 403)
	if err = server.manualSourceAllowed(httptest.NewRequest("POST", "http://localhost", nil), users[2], "registry"); err != nil {
		t.Fatal("platform admin could not provision a registry", err)
	}
	begin := func(who int, purpose, team string) (string, *http.Cookie) {
		t.Helper()
		var w *httptest.ResponseRecorder
		if purpose == "login" {
			w = call(-1, "GET", "/api/auth/integrations/github/start", nil, 302)
		} else if purpose == "source" {
			w = call(who, "POST", "/api/teams/"+team+"/integrations/github/connect", map[string]string{"returnTo": "/teams/test/settings/profile"}, 200)
		} else {
			w = call(who, "POST", "/api/account/integrations/providers/github/"+purpose, map[string]string{"returnTo": "/teams/test/settings/profile"}, 200)
		}
		target := w.Header().Get("Location")
		if target == "" {
			var data struct {
				URL string `json:"url"`
			}
			json.Unmarshal(w.Body.Bytes(), &data)
			target = data.URL
		}
		parsed, _ := url.Parse(target)
		if parsed.Query().Get("code_challenge_method") != "S256" {
			t.Fatal("GitHub authorization omitted PKCE")
		}
		var cookie *http.Cookie
		for _, value := range w.Result().Cookies() {
			if value.Name == "ignition_oauth_github" {
				cookie = value
			}
		}
		if cookie == nil {
			t.Fatal("missing browser state cookie")
		}
		return parsed.Query().Get("state"), cookie
	}
	finish := func(who int, state string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "http://localhost/api/auth/integrations/github/callback?state="+url.QueryEscape(state)+"&code=provider-code", nil)
		r.AddCookie(cookie)
		if who >= 0 {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tokens[who]})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	state, cookie := begin(0, "connect", "")
	w := finish(1, state, cookie)
	if !strings.Contains(w.Header().Get("Location"), "account_error=") {
		t.Fatal("OAuth identity linked after a session switch")
	}
	state, cookie = begin(0, "connect", "")
	w = finish(0, state, cookie)
	if !strings.Contains(w.Header().Get("Location"), "account_connected=1") {
		t.Fatalf("account link failed: %s", w.Header().Get("Location"))
	}
	if _, err = st.IntegrationUser(ctx, "github", "https://different-provider.test", "100"); err != store.ErrNotFound {
		t.Fatal("provider origin did not isolate identities")
	}
	w = finish(0, state, cookie)
	if !strings.Contains(w.Header().Get("Location"), "sso_error=") {
		t.Fatal("OAuth state replay accepted")
	}
	state, cookie = begin(-1, "login", "")
	w = finish(-1, state, cookie)
	if w.Code != 302 || w.Header().Get("Location") != "/" {
		t.Fatalf("provider sign-in failed: %d %s", w.Code, w.Header().Get("Location"))
	}
	signedIn := false
	for _, cookie := range w.Result().Cookies() {
		signedIn = signedIn || cookie.Name == sessionCookie && cookie.Value != ""
	}
	if !signedIn {
		t.Fatal("provider sign-in did not establish a session")
	}
	scope.Store("read:user")
	state, cookie = begin(0, "source", a.ID)
	w = finish(0, state, cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Source connected") {
		t.Fatalf("source connection failed: %d %s %s", w.Code, w.Body.String(), w.Header().Get("Location"))
	}
	list := call(0, "GET", "/api/teams/"+a.ID+"/sources", nil, 200)
	if strings.Contains(list.Body.String(), "private-access-token") || strings.Contains(list.Body.String(), "refresh_token") {
		t.Fatal("source response exposed OAuth tokens")
	}
	var payload struct {
		Sources []store.Source `json:"sources"`
	}
	json.Unmarshal(list.Body.Bytes(), &payload)
	if len(payload.Sources) != 1 {
		t.Fatalf("expected one managed source: %s", list.Body.String())
	}
	src := payload.Sources[0]
	call(1, "GET", "/api/teams/"+b.ID+"/sources/"+src.ID, nil, 404)
	call(1, "DELETE", "/api/teams/"+b.ID+"/integrations/connections/"+src.ID, nil, 404)
	// Reauthorization replaces credentials without changing source IDs already used by workloads.
	state, cookie = begin(0, "source", a.ID)
	finish(0, state, cookie)
	var id string
	pool.QueryRow(ctx, `SELECT id FROM sources WHERE oauth_user_id=$1 AND team_id=$2`, users[0].ID, a.ID).Scan(&id)
	if id != src.ID {
		t.Fatal("reconnecting changed source ID")
	}
	repos := call(0, "GET", "/api/teams/"+a.ID+"/sources/"+src.ID+"/repositories", nil, 200)
	if !strings.Contains(repos.Body.String(), "private-app") {
		t.Fatal("managed source could not list repositories")
	}
	stored, err := st.GetSource(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	expired := integrations.Token{AccessToken: "expired-token", RefreshToken: "private-refresh-token", ExpiresAt: time.Now().Add(-time.Minute)}
	raw, _ := json.Marshal(expired)
	sealed, _ := box.Seal(string(raw), "source:"+src.ID)
	pool.Exec(ctx, `UPDATE sources SET secret_enc=$2 WHERE id=$1`, src.ID, sealed)
	var workers sync.WaitGroup
	failures := make(chan error, 4)
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			credentials, err := integrations.Credentials(ctx, st, box, stored)
			if err == nil && credentials.Secret != "refreshed-access-token" {
				err = errors.New("incorrect refreshed token")
			}
			failures <- err
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if refreshes.Load() != 1 {
		t.Fatalf("concurrent refreshes: %d", refreshes.Load())
	}
	// Removal of the authorizing member prevents future token use, even with an old source URL.
	pool.Exec(ctx, `DELETE FROM team_memberships WHERE team_id=$1 AND user_id=$2`, a.ID, users[0].ID)
	if _, err = integrations.Credentials(ctx, st, box, stored); err == nil {
		t.Fatal("removed team member's OAuth token was still used")
	}
	pool.Exec(ctx, `INSERT INTO team_memberships(team_id,user_id,role) VALUES($1,$2,'owner')`, a.ID, users[0].ID)
	call(0, "DELETE", "/api/teams/"+a.ID+"/integrations/connections/"+src.ID, nil, 204)
	// Login-only apps must not silently inherit repository grants.
	scope.Store("read:user user:email repo")
	state, cookie = begin(1, "connect", "")
	w = finish(1, state, cookie)
	if !strings.Contains(w.Header().Get("Location"), "account_error=") {
		t.Fatal("overprivileged login grant was accepted")
	}
}
func providerPlaceholder(r *http.Request) string { return "https://" + r.Host }

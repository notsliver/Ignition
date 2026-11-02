package integrations

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/notsliver/Ignition/services/internal/store"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestProviderOAuthPermissionsAndIdentity(t *testing.T) {
	original := client
	defer func() { client = original }()
	for _, name := range []string{"github", "gitlab", "bitbucket"} {
		t.Run(name, func(t *testing.T) {
			c := Defaults(store.IntegrationConfig{Provider: name, Enabled: true, SignInEnabled: true, SourcesEnabled: true, ManualAllowed: true})
			c.Login.ClientID = "identity-client"
			c.Login.HasSecret = true
			c.Repositories.ClientID = "repository-client"
			c.Repositories.HasSecret = true
			if fields := Validate(c); len(fields) != 0 {
				t.Fatalf("defaults invalid: %v", fields)
			}
			var selectedSource bool
			client = &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				body := ""
				if r.Method == "POST" {
					r.ParseForm()
					expected := "identity-client"
					scopes := c.Login.Scopes
					if selectedSource {
						expected = "repository-client"
						scopes = c.Repositories.Scopes
					}
					if c.Provider == "bitbucket" {
						id, secret, ok := r.BasicAuth()
						if !ok || id != expected || secret != "client-secret" {
							t.Error("Bitbucket client authentication mismatch")
						}
						if r.Form.Get("code_verifier") != "" {
							t.Error("unexpected Bitbucket PKCE extension")
						}
					} else {
						if r.Form.Get("client_id") != expected || r.Form.Get("client_secret") != "client-secret" || r.Form.Get("code_verifier") != "verifier" {
							t.Error("PKCE/client binding missing")
						}
					}
					body = `{"access_token":"token","refresh_token":"refresh","expires_in":3600,"scope":"` + strings.Join(scopes, " ") + `"}`
				} else {
					if r.Header.Get("Authorization") != "Bearer token" {
						t.Error("provider identity request did not use bearer authentication")
					}
					if strings.HasSuffix(r.URL.Path, "/emails") {
						if name == "github" {
							body = `[{"email":"verified@example.test","primary":true,"verified":true}]`
						} else {
							body = `{"values":[{"email":"verified@example.test","is_primary":true,"is_confirmed":true}]}`
						}
					} else {
						switch name {
						case "github":
							body = `{"id":42,"login":"developer"}`
						case "gitlab":
							body = `{"id":42,"username":"developer","email":"verified@example.test","confirmed_at":"2026-01-01T00:00:00Z"}`
						case "bitbucket":
							body = `{"uuid":"{stable-account-id}","nickname":"developer"}`
						}
					}
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			for _, source := range []bool{false, true} {
				selectedSource = source
				target, _ := url.Parse(AuthorizationURL(c, source, "https://ignition.test/callback", "state", "challenge"))
				expected := c.Login.Scopes
				if source {
					expected = c.Repositories.Scopes
				}
				if target.Query().Get("scope") != strings.Join(expected, " ") {
					t.Fatal("purpose scopes mixed")
				}
				if target.Query().Get("state") != "state" {
					t.Fatal("missing browser state")
				}
				if name != "bitbucket" && target.Query().Get("code_challenge") != "challenge" {
					t.Fatal("missing PKCE")
				}
				token, err := Exchange(context.Background(), c, source, "client-secret", "code", "https://ignition.test/callback", "verifier")
				if err != nil || token.AccessToken != "token" || !token.ExpiresAt.After(time.Now()) {
					t.Fatalf("token exchange failed: %v", err)
				}
			}
			identity, verified, err := Identity(context.Background(), c, "token", true)
			if err != nil || !verified || identity.Username != "developer" || identity.Origin != c.BaseURL {
				t.Fatalf("identity failed: %+v %v %v", identity, verified, err)
			}
		})
	}
}
func TestConfigurationCannotMisrepresentProviderPermissions(t *testing.T) {
	c := Defaults(store.IntegrationConfig{Provider: "bitbucket", SignInEnabled: true, SourcesEnabled: true})
	c.Login.ClientID = "same-client"
	c.Repositories.ClientID = "same-client"
	c.Login.HasSecret = true
	c.Repositories.HasSecret = true
	if Validate(c)["repositories"] == "" {
		t.Fatal("shared Bitbucket consumer accepted for differently scoped purposes")
	}
	c = Defaults(store.IntegrationConfig{Provider: "github", SignInEnabled: true})
	c.Login.ClientID = "login"
	c.Login.HasSecret = true
	c.Login.Scopes = []string{"repo"}
	if Validate(c)["loginScopes"] == "" {
		t.Fatal("repository grant accepted for identity login")
	}
	for _, provider := range []string{"git", "dockerhub", "ghcr", "gitlab-registry", "registry"} {
		c = Defaults(store.IntegrationConfig{Provider: provider, SourcesEnabled: true})
		if Validate(c)["provider"] == "" {
			t.Fatalf("unsupported OAuth enabled for %s", provider)
		}
	}
}

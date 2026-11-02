// Package integrations owns provider OAuth protocol details. Credentials stay server-side.
package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/secrets"
	"github.com/notsliver/Ignition/services/internal/sources"
	"github.com/notsliver/Ignition/services/internal/store"
)

type Definition struct {
	Label, BaseURL, PermissionNote string
	OAuth                          bool
	LoginScopes, SourceScopes      []string
}

var Catalog = map[string]Definition{
	"github":          {"GitHub", "https://github.com", "GitHub OAuth repo grants read AND write access to private repositories. Choose public-only to avoid that grant; for repository-specific read-only access use a GitHub App or deploy key.", true, []string{"read:user", "user:email"}, []string{"read:user"}},
	"gitlab":          {"GitLab", "https://gitlab.com", "Repository access uses read_user, read_api and read_repository; no write scope is requested.", true, []string{"read_user"}, []string{"read_user", "read_api", "read_repository"}},
	"bitbucket":       {"Bitbucket", "https://bitbucket.org", "Configure account/email on the sign-in consumer and account/repository read on the source consumer. Bitbucket grants consumer-configured scopes; separate consumers are required when both purposes are enabled.", true, []string{"account", "email"}, []string{"account", "repository"}},
	"git":             {"Git over HTTPS or SSH", "", "Use a repository-specific read-only deploy key or token.", false, nil, nil},
	"dockerhub":       {"Docker Hub", "", "Use a read-only Docker Hub access token for image pulls.", false, nil, nil},
	"ghcr":            {"GitHub Container Registry", "", "Use a token with read:packages; repository OAuth is a separate permission.", false, nil, nil},
	"gitlab-registry": {"GitLab Registry", "", "Use a deploy token with read_registry for image pulls.", false, nil, nil},
	"registry":        {"Private registry", "", "Use a registry credential restricted to pulling the required images.", false, nil, nil},
}

func Defaults(c store.IntegrationConfig) store.IntegrationConfig {
	d := Catalog[c.Provider]
	if c.Label == "" {
		c.Label = d.Label
	}
	if c.BaseURL == "" {
		c.BaseURL = d.BaseURL
	}
	if c.Login.Scopes == nil {
		c.Login.Scopes = d.LoginScopes
	}
	if c.Repositories.Scopes == nil {
		c.Repositories.Scopes = d.SourceScopes
	}
	if c.SourceRevision == 0 {
		c.SourceRevision = 1
	}
	return c
}
func Validate(c store.IntegrationConfig) map[string]string {
	f := map[string]string{}
	d, ok := Catalog[c.Provider]
	if !ok {
		f["provider"] = "Choose an existing source provider."
		return f
	}
	if strings.TrimSpace(c.Label) == "" || len(c.Label) > 80 {
		f["label"] = "Enter a label up to 80 characters."
	}
	if !d.OAuth && (c.SignInEnabled || c.SourcesEnabled) {
		f["provider"] = "This provider uses credentials or deploy keys; OAuth is not supported."
	}
	if d.OAuth {
		u, err := url.Parse(c.BaseURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
			f["baseUrl"] = "Enter an HTTPS origin without a path or credentials."
		}
		if c.Provider == "bitbucket" && c.BaseURL != "https://bitbucket.org" {
			f["baseUrl"] = "This connector uses Bitbucket Cloud."
		}
	}
	if c.SignInEnabled && (c.Login.ClientID == "" || !c.Login.HasSecret) {
		f["login"] = "Add the sign-in OAuth client ID and secret."
	}
	if c.SourcesEnabled && (c.Repositories.ClientID == "" || !c.Repositories.HasSecret) {
		f["repositories"] = "Add the repository OAuth client ID and secret."
	}
	if c.SignInEnabled && c.SourcesEnabled && c.Login.ClientID == c.Repositories.ClientID {
		f["repositories"] = "Use separate OAuth applications for identity-only sign-in and repository access."
	}
	for _, scope := range c.Login.Scopes {
		if !slices.Contains(d.LoginScopes, scope) {
			f["loginScopes"] = "Sign-in permissions must not grant repository access."
		}
	}
	if c.SignInEnabled && !slices.Equal(c.Login.Scopes, d.LoginScopes) {
		f["loginScopes"] = "Keep the identity permissions required by this provider."
	}
	if c.Provider == "github" {
		for _, scope := range c.Repositories.Scopes {
			if !slices.Contains([]string{"read:user", "repo", "public_repo"}, scope) {
				f["sourceScopes"] = "Choose public repositories or private repositories."
			}
		}
		if c.SourcesEnabled && !slices.Contains(c.Repositories.Scopes, "read:user") {
			f["sourceScopes"] = "GitHub needs read:user to identify the connected account."
		}
	}
	if c.Provider != "github" && c.SourcesEnabled && !slices.Equal(c.Repositories.Scopes, d.SourceScopes) {
		f["sourceScopes"] = "Use the provider's read-only repository permission preset."
	}
	return f
}
func endpoints(c store.IntegrationConfig) (authorize, token, api string) {
	switch c.Provider {
	case "github":
		api = "https://api.github.com"
		if c.BaseURL != "https://github.com" {
			api = c.BaseURL + "/api/v3"
		}
		return c.BaseURL + "/login/oauth/authorize", c.BaseURL + "/login/oauth/access_token", api
	case "gitlab":
		return c.BaseURL + "/oauth/authorize", c.BaseURL + "/oauth/token", c.BaseURL + "/api/v4"
	case "bitbucket":
		return "https://bitbucket.org/site/oauth2/authorize", "https://bitbucket.org/site/oauth2/access_token", "https://api.bitbucket.org/2.0"
	}
	return
}
func AuthorizationURL(c store.IntegrationConfig, source bool, redirect, state, challenge string) string {
	authorize, _, _ := endpoints(c)
	client := c.Login
	if source {
		client = c.Repositories
	}
	q := url.Values{"client_id": {client.ClientID}, "redirect_uri": {redirect}, "response_type": {"code"}, "state": {state}, "scope": {strings.Join(client.Scopes, " ")}}
	if c.Provider != "bitbucket" {
		q.Set("code_challenge", challenge)
		q.Set("code_challenge_method", "S256")
	}
	return authorize + "?" + q.Encode()
}

type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	Scope        string    `json:"scope"`
	ExpiresIn    int64     `json:"expires_in"`
	ExpiresAt    time.Time `json:"expires_at"`
}

var client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("provider redirect refused") }}

func Exchange(ctx context.Context, c store.IntegrationConfig, source bool, secret, code, redirect, verifier string) (Token, error) {
	cfg := c.Login
	if source {
		cfg = c.Repositories
	}
	values := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {cfg.ClientID}}
	if c.Provider != "bitbucket" {
		values.Set("code_verifier", verifier)
	}
	return tokenRequest(ctx, c, cfg.ClientID, secret, values)
}
func tokenRequest(ctx context.Context, c store.IntegrationConfig, id, secret string, values url.Values) (Token, error) {
	_, endpoint, _ := endpoints(c)
	var token Token
	if c.Provider != "bitbucket" {
		values.Set("client_secret", secret)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return token, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if c.Provider == "bitbucket" {
		req.SetBasicAuth(id, secret)
	}
	resp, err := client.Do(req)
	if err != nil {
		return token, errors.New("the OAuth provider could not be reached")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return token, errors.New("the OAuth provider rejected the request; reconnect or check the application settings")
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&token); err != nil || token.AccessToken == "" {
		return token, errors.New("the OAuth provider returned no access token")
	}
	if token.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	return token, nil
}
func get(ctx context.Context, endpoint, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Ignition")
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("the provider account API could not be reached")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("the provider denied the account lookup; check requested permissions")
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
}
func Identity(ctx context.Context, c store.IntegrationConfig, token string, needEmail bool) (store.IntegrationIdentity, bool, error) {
	_, _, api := endpoints(c)
	out := store.IntegrationIdentity{Provider: c.Provider, Origin: c.BaseURL}
	verified := false
	switch c.Provider {
	case "github":
		var u struct {
			ID    int64
			Login string
			Email string
		}
		if err := get(ctx, api+"/user", token, &u); err != nil {
			return out, false, err
		}
		out.Subject = strconv.FormatInt(u.ID, 10)
		out.Username = u.Login
		if needEmail {
			var emails []struct {
				Email             string
				Primary, Verified bool
			}
			if err := get(ctx, api+"/user/emails", token, &emails); err != nil {
				return out, false, err
			}
			for _, e := range emails {
				if e.Primary && e.Verified {
					out.Email = e.Email
					verified = true
					break
				}
			}
		}
	case "gitlab":
		var u struct {
			ID              int64
			Username, Email string
			ConfirmedAt     *time.Time `json:"confirmed_at"`
		}
		if err := get(ctx, api+"/user", token, &u); err != nil {
			return out, false, err
		}
		out.Subject = strconv.FormatInt(u.ID, 10)
		out.Username = u.Username
		out.Email = u.Email
		verified = u.ConfirmedAt != nil
	case "bitbucket":
		var u struct {
			UUID     string
			Username string
			Nickname string
		}
		if err := get(ctx, api+"/user", token, &u); err != nil {
			return out, false, err
		}
		out.Subject = u.UUID
		out.Username = u.Nickname
		if out.Username == "" {
			out.Username = u.Username
		}
		if needEmail {
			var p struct {
				Values []struct {
					Email     string
					Confirmed bool `json:"is_confirmed"`
					Primary   bool `json:"is_primary"`
				}
			}
			if err := get(ctx, api+"/user/emails", token, &p); err != nil {
				return out, false, err
			}
			for _, e := range p.Values {
				if e.Confirmed && e.Primary {
					out.Email = e.Email
					verified = true
					break
				}
			}
		}
	}
	if out.Subject == "" || out.Subject == "0" || out.Username == "" {
		return out, false, errors.New("the provider did not return a stable account identity")
	}
	return out, verified, nil
}
func Credentials(ctx context.Context, st *store.Store, box *secrets.Box, src *store.Source) (sources.Credentials, error) {
	c := sources.Credentials{Provider: src.Provider, URL: src.URL, AuthType: src.AuthType, Username: src.Username}
	config, err := st.Integration(ctx, src.Provider)
	if err != nil {
		return c, err
	}
	if !config.Enabled {
		return c, errors.New("this source provider is disabled by the instance administrator")
	}
	if src.OAuthProvider == "" {
		if src.SecretEnc != nil {
			plain, err := box.Open(src.SecretEnc, "source:"+src.ID)
			if err != nil {
				return c, err
			}
			c.Secret = plain
		}
		return c, nil
	}
	c.Managed = true
	origin, err := url.Parse(src.URL)
	if err != nil || origin.Scheme != "https" || origin.Host == "" {
		return c, errors.New("invalid managed source origin")
	}
	c.GitHost = origin.Host
	token, err := st.ManagedSourceToken(ctx, src.ID, func(row *store.Source, config store.IntegrationConfig) ([]byte, string, error) {
		config = Defaults(config)
		if !config.Enabled || !config.SourcesEnabled {
			return nil, "", errors.New("the instance administrator disabled this OAuth source provider")
		}
		if row.OAuthRevision != config.SourceRevision {
			return nil, "", errors.New("the instance OAuth application changed; reconnect this source")
		}
		if len(row.SecretEnc) == 0 {
			return nil, "", errors.New("reconnect this source")
		}
		plain, err := box.Open(row.SecretEnc, "source:"+row.ID)
		if err != nil {
			return nil, "", err
		}
		var saved Token
		if err = json.Unmarshal([]byte(plain), &saved); err != nil {
			return nil, "", err
		}
		if saved.ExpiresAt.IsZero() || saved.ExpiresAt.After(time.Now().Add(time.Minute)) {
			return nil, saved.AccessToken, nil
		}
		if saved.RefreshToken == "" {
			return nil, "", errors.New("the OAuth token expired; reconnect this source")
		}
		secret, err := box.Open(config.SourceSecretEnc, "integration:"+config.Provider+":source")
		if err != nil {
			return nil, "", err
		}
		next, err := tokenRequest(ctx, config, config.Repositories.ClientID, secret, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {saved.RefreshToken}, "client_id": {config.Repositories.ClientID}})
		if err != nil {
			return nil, "", err
		}
		if next.RefreshToken == "" {
			next.RefreshToken = saved.RefreshToken
		}
		raw, err := json.Marshal(next)
		if err != nil {
			return nil, "", err
		}
		sealed, err := box.Seal(string(raw), "source:"+row.ID)
		return sealed, next.AccessToken, err
	})
	if err != nil {
		return c, fmt.Errorf("managed OAuth source: %w", err)
	}
	c.Secret = token
	switch src.Provider {
	case "github":
		c.Username = "x-access-token"
	case "gitlab":
		c.Username = "oauth2"
	case "bitbucket":
		c.Username = "x-token-auth"
	}
	return c, nil
}

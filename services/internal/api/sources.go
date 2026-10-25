package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ignition/ignition/internal/httpx"
	"github.com/ignition/ignition/internal/sources"
	"github.com/ignition/ignition/internal/store"
)

// Which auth types each provider supports, and its kind.
var providers = map[string]struct {
	kind  string
	auths []string
}{
	"github":          {"git", []string{"token", "ssh"}},
	"gitlab":          {"git", []string{"token", "ssh"}},
	"bitbucket":       {"git", []string{"password", "ssh"}},
	"git":             {"git", []string{"none", "token", "password", "ssh"}},
	"dockerhub":       {"registry", []string{"none", "password", "token"}},
	"ghcr":            {"registry", []string{"token"}},
	"gitlab-registry": {"registry", []string{"token", "password"}},
	"registry":        {"registry", []string{"none", "password", "token"}},
}

type sourceInput struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	URL      string `json:"url"`
	AuthType string `json:"authType"`
	Username string `json:"username"`
	Secret   string `json:"secret"`
	// GenerateKey asks Ignition to create an SSH key pair instead of pasting a private key.
	GenerateKey bool `json:"generateKey"`
}

// prepareKey generates an SSH key pair when asked, and returns the masked hint and (for SSH) the public key.
func prepareKey(in *sourceInput) (keyHint, publicKey string, err error) {
	comment := "ignition-" + strings.ToLower(strings.ReplaceAll(in.Name, " ", "-"))
	if in.GenerateKey {
		if in.Secret, _, err = sources.GenerateSSHKey(comment); err != nil {
			return "", "", err
		}
	}
	if in.AuthType != "ssh" || in.Secret == "" {
		return hint(in.Secret), "", nil
	}
	publicKey, fp, err := sources.SSHPublicKey(in.Secret, comment)
	if err != nil {
		return "", "", httpx.Invalid(map[string]string{"secret": err.Error()})
	}
	return "…" + fp[len(fp)-8:], publicKey, nil
}

func hint(secret string) string {
	s := strings.TrimSpace(secret)
	if strings.Contains(s, "PRIVATE KEY") {
		return "SSH key"
	}
	if len(s) < 8 {
		return ""
	}
	return "…" + s[len(s)-4:]
}

// validateSource checks the merged settings. requireSecret is false when an existing secret is kept.
func validateSource(in sourceInput, requireSecret bool) map[string]string {
	f := map[string]string{}
	p, ok := providers[in.Provider]
	if !ok {
		f["provider"] = "Choose a provider."
		return f
	}
	if n := strings.TrimSpace(in.Name); n == "" || len(n) > 60 {
		f["name"] = "Name is required (up to 60 characters)."
	}
	allowed := false
	for _, a := range p.auths {
		allowed = allowed || a == in.AuthType
	}
	if !allowed {
		f["authType"] = "This provider doesn't support that authentication method."
	}
	switch {
	case in.Provider == "git":
		if in.AuthType == "ssh" && !sources.ValidSSHRepo(in.URL) {
			f["url"] = "Enter an SSH repository URL, like git@git.example.com:team/app.git."
		} else if in.AuthType != "ssh" && !sources.ValidHTTPURL(in.URL) {
			f["url"] = "Enter an https repository URL, like https://git.example.com/team/app.git."
		}
	case in.AuthType == "ssh" && in.URL != "" && !sources.ValidSSHRepo(in.URL):
		f["url"] = "To test an SSH key, enter one SSH repository URL it can read."
	case p.kind == "git" && in.URL != "" && in.AuthType != "ssh" && !sources.ValidHTTPURL(in.URL):
		f["url"] = "Self-hosted URLs must start with https://."
	case in.Provider == "registry" && !sources.ValidRegistryHost(in.URL):
		f["url"] = "Enter the registry host, like registry.example.com:5000 (no https://)."
	case in.Provider == "gitlab-registry" && in.URL != "" && !sources.ValidRegistryHost(in.URL):
		f["url"] = "Enter the registry host, like registry.gitlab.example.com."
	}
	if in.AuthType != "none" && in.AuthType != "ssh" && (in.Provider == "bitbucket" || in.AuthType == "password" || p.kind == "registry") && strings.TrimSpace(in.Username) == "" && in.Provider != "ghcr" {
		f["username"] = "Username is required."
	}
	if in.Provider == "ghcr" && strings.TrimSpace(in.Username) == "" {
		f["username"] = "Enter the GitHub username that owns the token."
	}
	if in.GenerateKey && in.AuthType != "ssh" {
		f["authType"] = "Only SSH keys can be generated."
	}
	if in.AuthType != "none" && requireSecret && !in.GenerateKey && strings.TrimSpace(in.Secret) == "" {
		f["secret"] = map[string]string{"ssh": "Paste the private key.", "password": "Enter the password.", "token": "Enter the token."}[in.AuthType]
	}
	if in.AuthType == "ssh" && !in.GenerateKey && strings.TrimSpace(in.Secret) != "" && !strings.Contains(in.Secret, "PRIVATE KEY-----") {
		f["secret"] = "That doesn't look like a private key. It should start with -----BEGIN … PRIVATE KEY-----."
	}
	if len(in.Secret) > 16<<10 {
		f["secret"] = "Secret is too long."
	}
	return f
}

func (s *Server) listSources(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	list, err := s.Store.ListSources(r.Context(), r.URL.Query().Get("kind"), strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"sources": list})
	return nil
}

func (s *Server) getSource(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Source")
	if err != nil {
		return err
	}
	src, err := s.Store.GetSource(r.Context(), id)
	if err != nil {
		return notFound(err, "Source")
	}
	linked, err := s.Store.SourceResources(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"source": src, "resources": linked})
	return nil
}

func (s *Server) createSource(w http.ResponseWriter, r *http.Request, u *store.User) error {
	var in sourceInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Name, in.URL, in.Username = strings.TrimSpace(in.Name), strings.TrimSpace(in.URL), strings.TrimSpace(in.Username)
	if f := validateSource(in, true); len(f) > 0 {
		return httpx.Invalid(f)
	}
	keyHint, publicKey, err := prepareKey(&in)
	if err != nil {
		return err
	}
	var seal func(string) ([]byte, error)
	if in.AuthType != "none" {
		seal = func(id string) ([]byte, error) { return s.Secrets.Seal(in.Secret, "source:"+id) }
	}
	id, err := s.Store.CreateSource(r.Context(), store.SourceInput{Name: in.Name, Kind: providers[in.Provider].kind, Provider: in.Provider, URL: in.URL,
		AuthType: in.AuthType, Username: in.Username, SecretHint: keyHint, PublicKey: publicKey, CreatedBy: u.ID}, seal)
	if store.IsUnique(err) {
		return httpx.Invalid(map[string]string{"name": "A source with this name already exists."})
	}
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "source.create", "source", id, map[string]any{"name": in.Name, "provider": in.Provider}, clientIP(r))
	// Test straight away so the list shows real status, not just "saved".
	src, _ := s.testSource(r.Context(), id)
	httpx.JSON(w, 201, map[string]any{"source": src})
	return nil
}

func (s *Server) updateSource(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Source")
	if err != nil {
		return err
	}
	cur, err := s.Store.GetSource(r.Context(), id)
	if err != nil {
		return notFound(err, "Source")
	}
	var in struct {
		Name        *string `json:"name"`
		URL         *string `json:"url"`
		AuthType    *string `json:"authType"`
		Username    *string `json:"username"`
		Secret      *string `json:"secret"`      // omitted or empty keeps the current secret; a new value rotates it
		GenerateKey bool    `json:"generateKey"` // replaces the SSH key with a newly generated pair
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	merged := sourceInput{Name: cur.Name, Provider: cur.Provider, URL: cur.URL, AuthType: cur.AuthType, Username: cur.Username}
	for dst, src := range map[*string]*string{&merged.Name: in.Name, &merged.URL: in.URL, &merged.AuthType: in.AuthType, &merged.Username: in.Username} {
		if src != nil {
			*dst = strings.TrimSpace(*src)
		}
	}
	merged.GenerateKey = in.GenerateKey
	rotating := in.GenerateKey || in.Secret != nil && strings.TrimSpace(*in.Secret) != ""
	if rotating && !in.GenerateKey {
		merged.Secret = *in.Secret
	}
	authChanged := merged.AuthType != cur.AuthType
	if f := validateSource(merged, (authChanged || !cur.HasSecret) && merged.AuthType != "none"); len(f) > 0 {
		return httpx.Invalid(f)
	}
	var sealed []byte
	var h, pub *string
	if rotating {
		keyHint, publicKey, err := prepareKey(&merged)
		if err != nil {
			return err
		}
		if sealed, err = s.Secrets.Seal(merged.Secret, "source:"+id); err != nil {
			return err
		}
		h, pub = &keyHint, &publicKey
	}
	err = s.Store.UpdateSource(r.Context(), id, store.SourcePatch{Name: &merged.Name, URL: &merged.URL, Username: &merged.Username, AuthType: &merged.AuthType}, sealed, h, pub, merged.AuthType == "none")
	if store.IsUnique(err) {
		return httpx.Invalid(map[string]string{"name": "A source with this name already exists."})
	}
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, map[bool]string{true: "source.rotate", false: "source.update"}[rotating], "source", id, map[string]any{"name": merged.Name}, clientIP(r))
	src, _ := s.testSource(r.Context(), id)
	httpx.JSON(w, 200, map[string]any{"source": src})
	return nil
}

func (s *Server) deleteSource(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Source")
	if err != nil {
		return err
	}
	src, err := s.Store.GetSource(r.Context(), id)
	if err != nil {
		return notFound(err, "Source")
	}
	if src.LinkedResources > 0 {
		noun := "resources still use"
		if src.LinkedResources == 1 {
			noun = "resource still uses"
		}
		return httpx.Conflict(fmt.Sprintf("%d %s %s. Remove or change them first.", src.LinkedResources, noun, src.Name))
	}
	if err := s.Store.DeleteSource(r.Context(), id); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "source.delete", "source", id, map[string]any{"name": src.Name}, clientIP(r))
	httpx.JSON(w, 204, nil)
	return nil
}

func (s *Server) credentials(src *store.Source) (sources.Credentials, error) {
	c := sources.Credentials{Provider: src.Provider, URL: src.URL, AuthType: src.AuthType, Username: src.Username}
	if src.SecretEnc != nil {
		secret, err := s.Secrets.Open(src.SecretEnc, "source:"+src.ID)
		if err != nil {
			return c, errors.New("the saved secret can't be decrypted; was IGNITION_SECRET_KEY changed?")
		}
		c.Secret = secret
	}
	return c, nil
}

// testSource runs a real check against the provider or registry and records the result.
func (s *Server) testSource(ctx context.Context, id string) (*store.Source, error) {
	src, err := s.Store.GetSource(ctx, id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c, err := s.credentials(src)
	account := ""
	if err == nil {
		account, err = sources.Check(ctx, c, s.Runtime)
	}
	if err != nil {
		s.Store.SetSourceStatus(context.WithoutCancel(ctx), id, "failed", err.Error(), "")
	} else {
		msg := "Connected as " + account
		if account == "public access" || account == "anonymous" || account == "SSH key" {
			msg = "Connected (" + account + ")"
		}
		s.Store.SetSourceStatus(context.WithoutCancel(ctx), id, "connected", msg, account)
	}
	return s.Store.GetSource(context.WithoutCancel(ctx), id)
}

func (s *Server) testSourceHandler(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Source")
	if err != nil {
		return err
	}
	src, err := s.testSource(r.Context(), id)
	if err != nil {
		return notFound(err, "Source")
	}
	s.Store.Audit(r.Context(), u, "source.test", "source", id, map[string]any{"status": src.Status}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"source": src})
	return nil
}

func (s *Server) sourceRepositories(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Source")
	if err != nil {
		return err
	}
	src, err := s.Store.GetSource(r.Context(), id)
	if err != nil {
		return notFound(err, "Source")
	}
	if src.AuthType == "ssh" || (src.Provider != "github" && src.Provider != "gitlab" && src.Provider != "bitbucket") {
		return httpx.Unsupported("Listing repositories needs a GitHub, GitLab or Bitbucket token.")
	}
	c, err := s.credentials(src)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	repos, err := sources.Repositories(ctx, c)
	if err != nil {
		return httpx.Unavailable(err.Error())
	}
	httpx.JSON(w, 200, map[string]any{"repositories": repos})
	return nil
}

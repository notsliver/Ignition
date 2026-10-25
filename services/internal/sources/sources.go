// Package sources checks Git provider and registry credentials and turns them into
// something git or Docker can use. Secrets only ever live in memory and temp files with mode 0600.
package sources

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Credentials struct {
	Provider string // github, gitlab, bitbucket, git, dockerhub, ghcr, gitlab-registry, registry
	URL      string // self-hosted base URL, generic repository URL, or registry address
	AuthType string // none, token, password, ssh
	Username string
	Secret   string
}

var (
	ErrRejected    = errors.New("credentials were rejected")
	httpRE         = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:[0-9]+)?(/[A-Za-z0-9._~/-]*)?$`)
	sshRepoRE      = regexp.MustCompile(`^(ssh://)?[A-Za-z0-9._-]+@[A-Za-z0-9.-]+(:[0-9]+)?[:/][A-Za-z0-9._~/-]+$`)
	registryHostRE = regexp.MustCompile(`^[a-z0-9.-]+(:[0-9]+)?$`)
)

// IsGitProvider reports whether the provider is a Git host rather than a registry.
func IsGitProvider(p string) bool {
	return p == "github" || p == "gitlab" || p == "bitbucket" || p == "git"
}

// ValidHTTPURL accepts https URLs only (no credentials embedded, no other schemes).
func ValidHTTPURL(u string) bool { return httpRE.MatchString(u) && !strings.Contains(u, "..") }

// ValidSSHRepo accepts git@host:owner/repo.git and ssh://git@host/owner/repo.git.
func ValidSSHRepo(u string) bool {
	return sshRepoRE.MatchString(u) && !strings.HasPrefix(u, "-") && !strings.Contains(u, "..")
}

// ValidRegistryHost accepts host[:port] without a scheme, e.g. registry.example.com:5000.
func ValidRegistryHost(h string) bool { return registryHostRE.MatchString(h) }

// RegistryAddress is the server address Docker expects for a registry provider.
func RegistryAddress(c Credentials) string {
	switch c.Provider {
	case "dockerhub":
		return "https://index.docker.io/v1/"
	case "ghcr":
		return "ghcr.io"
	case "gitlab-registry":
		if c.URL != "" {
			return c.URL
		}
		return "registry.gitlab.com"
	}
	return c.URL
}

// RegistryHost is the host part used to match image references to this registry.
func RegistryHost(c Credentials) string {
	switch c.Provider {
	case "dockerhub":
		return "docker.io"
	}
	return strings.TrimPrefix(strings.TrimPrefix(RegistryAddress(c), "https://"), "http://")
}

func defaultUsername(c Credentials) string {
	if c.Username != "" {
		return c.Username
	}
	switch c.Provider {
	case "github":
		return "x-access-token"
	case "gitlab":
		return "oauth2"
	}
	return "git"
}

/* ------------------------------ Git credentials ----------------------------- */

// GitAuth prepares git to authenticate with these credentials.
// It returns extra arguments (placed before the subcommand), extra environment, and a cleanup function.
func GitAuth(c Credentials) (args []string, env []string, cleanup func(), err error) {
	cleanup = func() {}
	switch c.AuthType {
	case "none", "":
		return nil, nil, cleanup, nil
	case "token", "password":
		basic := base64.StdEncoding.EncodeToString([]byte(defaultUsername(c) + ":" + c.Secret))
		return []string{"-c", "http.extraHeader=Authorization: Basic " + basic}, nil, cleanup, nil
	case "ssh":
		dir, err := os.MkdirTemp("", "ignition-ssh-*")
		if err != nil {
			return nil, nil, cleanup, err
		}
		cleanup = func() { os.RemoveAll(dir) }
		key := filepath.Join(dir, "key")
		secret := strings.ReplaceAll(c.Secret, "\r\n", "\n")
		if !strings.HasSuffix(secret, "\n") {
			secret += "\n"
		}
		if err := os.WriteFile(key, []byte(secret), 0o600); err != nil {
			cleanup()
			return nil, nil, func() {}, err
		}
		ssh := fmt.Sprintf("ssh -i %s -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=%s",
			key, filepath.Join(dir, "known_hosts"))
		return nil, []string{"GIT_SSH_COMMAND=" + ssh}, cleanup, nil
	}
	return nil, nil, cleanup, fmt.Errorf("unsupported auth type %q", c.AuthType)
}

// LsRemote checks that a repository is reachable with the credentials (read access only).
func LsRemote(ctx context.Context, c Credentials, repo string) error {
	args, env, cleanup, err := GitAuth(c)
	if err != nil {
		return err
	}
	defer cleanup()
	cmd := exec.CommandContext(ctx, "git", append(args, "ls-remote", "--heads", "--", RepoURLFor(c, repo))...)
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if c.AuthType == "ssh" && strings.Contains(msg, "Permission denied") {
			return fmt.Errorf("%w: the server refused this SSH key. Add the public key as a read-only deploy key, then test again", ErrRejected)
		}
		if strings.Contains(msg, "Authentication failed") || strings.Contains(msg, "Permission denied") || strings.Contains(msg, "could not read Username") || strings.Contains(msg, "403") {
			return fmt.Errorf("%w: %s", ErrRejected, gitReason(msg))
		}
		return errors.New(gitReason(msg))
	}
	return nil
}

// gitReason picks the line of git's output that explains the failure, not the generic advice after it.
func gitReason(s string) string {
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "fatal: ") || strings.HasPrefix(strings.TrimSpace(l), "remote: ") {
			return strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(l), "fatal: "), "remote: ")
		}
	}
	return lastLine(s)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimPrefix(strings.TrimSpace(lines[len(lines)-1]), "fatal: ")
}

/* -------------------------------- Provider APIs ----------------------------- */

var client = &http.Client{Timeout: 10 * time.Second}

func apiBase(c Credentials) string {
	switch c.Provider {
	case "github":
		if c.URL != "" {
			return strings.TrimRight(c.URL, "/") + "/api/v3" // GitHub Enterprise Server
		}
		return "https://api.github.com"
	case "gitlab":
		base := "https://gitlab.com"
		if c.URL != "" {
			base = strings.TrimRight(c.URL, "/")
		}
		return base + "/api/v4"
	case "bitbucket":
		return "https://api.bitbucket.org/2.0"
	}
	return ""
}

func providerGet(ctx context.Context, c Credentials, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", apiBase(c)+path, nil)
	if err != nil {
		return err
	}
	switch c.Provider {
	case "github":
		req.Header.Set("Authorization", "Bearer "+c.Secret)
		req.Header.Set("Accept", "application/vnd.github+json")
	case "gitlab":
		req.Header.Set("PRIVATE-TOKEN", c.Secret)
	case "bitbucket":
		req.SetBasicAuth(c.Username, c.Secret)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't reach %s: %w", req.URL.Host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return fmt.Errorf("%w (%s returned %d)", ErrRejected, req.URL.Host, resp.StatusCode)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned %d", req.URL.Host, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Authenticator logs in to a registry; implemented by the Docker runtime.
type Authenticator interface {
	RegistryLogin(ctx context.Context, username, password, server string) error
}

// Check tests the credentials for real and returns who they authenticate as.
func Check(ctx context.Context, c Credentials, docker Authenticator) (account string, err error) {
	switch c.Provider {
	case "github", "gitlab", "bitbucket":
		if c.AuthType == "ssh" {
			if c.URL == "" {
				return "", errors.New("add a repository URL to test an SSH key")
			}
			return "SSH key", LsRemote(ctx, c, c.URL)
		}
		var user struct {
			Login       string `json:"login"`
			Username    string `json:"username"`
			DisplayName string `json:"display_name"`
		}
		if err := providerGet(ctx, c, "/user", &user); err != nil {
			return "", err
		}
		for _, v := range []string{user.Login, user.Username, user.DisplayName} {
			if v != "" {
				return v, nil
			}
		}
		return "authenticated", nil
	case "git":
		if c.URL == "" {
			return "", errors.New("a repository URL is required")
		}
		if err := LsRemote(ctx, c, c.URL); err != nil {
			return "", err
		}
		if c.AuthType == "none" {
			return "public access", nil
		}
		return defaultUsername(c), nil
	default: // registries
		if c.AuthType == "none" {
			return "anonymous", checkRegistryReachable(ctx, RegistryHost(c))
		}
		if err := docker.RegistryLogin(ctx, c.Username, c.Secret, RegistryAddress(c)); err != nil {
			return "", err
		}
		return c.Username, nil
	}
}

// checkRegistryReachable confirms something speaking the registry v2 API answers at host.
func checkRegistryReachable(ctx context.Context, host string) error {
	if host == "docker.io" {
		host = "registry-1.docker.io"
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://"+host+"/v2/", nil)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't reach %s: %w", host, err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 401 {
		return fmt.Errorf("%s doesn't look like a container registry (HTTP %d on /v2/)", host, resp.StatusCode)
	}
	return nil
}

type Repository struct {
	Name          string `json:"name"`
	CloneURL      string `json:"cloneUrl"`
	SSHURL        string `json:"sshUrl"`
	DefaultBranch string `json:"defaultBranch"`
	Private       bool   `json:"private"`
}

// Repositories lists repositories the credentials can read (first 100, most recently updated).
func Repositories(ctx context.Context, c Credentials) ([]Repository, error) {
	out := []Repository{}
	switch c.Provider {
	case "github":
		var list []struct {
			FullName      string `json:"full_name"`
			CloneURL      string `json:"clone_url"`
			SSHURL        string `json:"ssh_url"`
			DefaultBranch string `json:"default_branch"`
			Private       bool   `json:"private"`
		}
		if err := providerGet(ctx, c, "/user/repos?per_page=100&sort=updated", &list); err != nil {
			return nil, err
		}
		for _, r := range list {
			out = append(out, Repository{r.FullName, strings.TrimSuffix(r.CloneURL, ".git"), r.SSHURL, r.DefaultBranch, r.Private})
		}
	case "gitlab":
		var list []struct {
			Path          string `json:"path_with_namespace"`
			HTTPURL       string `json:"http_url_to_repo"`
			SSHURL        string `json:"ssh_url_to_repo"`
			DefaultBranch string `json:"default_branch"`
			Visibility    string `json:"visibility"`
		}
		if err := providerGet(ctx, c, "/projects?membership=true&per_page=100&order_by=last_activity_at&simple=true", &list); err != nil {
			return nil, err
		}
		for _, r := range list {
			out = append(out, Repository{r.Path, strings.TrimSuffix(r.HTTPURL, ".git"), r.SSHURL, r.DefaultBranch, r.Visibility != "public"})
		}
	case "bitbucket":
		var page struct {
			Values []struct {
				FullName   string `json:"full_name"`
				IsPrivate  bool   `json:"is_private"`
				Mainbranch struct {
					Name string `json:"name"`
				} `json:"mainbranch"`
				Links struct {
					Clone []struct{ Name, Href string } `json:"clone"`
				} `json:"links"`
			} `json:"values"`
		}
		if err := providerGet(ctx, c, "/repositories?role=member&pagelen=100&sort=-updated_on", &page); err != nil {
			return nil, err
		}
		for _, r := range page.Values {
			repo := Repository{Name: r.FullName, DefaultBranch: r.Mainbranch.Name, Private: r.IsPrivate}
			for _, l := range r.Links.Clone {
				if l.Name == "https" {
					u, _ := url.Parse(l.Href)
					u.User = nil
					repo.CloneURL = strings.TrimSuffix(u.String(), ".git")
				} else if l.Name == "ssh" {
					repo.SSHURL = l.Href
				}
			}
			out = append(out, repo)
		}
	default:
		return nil, errors.New("listing repositories isn't available for this source")
	}
	return out, nil
}

// RegistryAuthHeader is the base64url JSON Docker expects in X-Registry-Auth for pulls.
func RegistryAuthHeader(c Credentials) string {
	b, _ := json.Marshal(map[string]string{"username": c.Username, "password": c.Secret, "serveraddress": RegistryAddress(c)})
	return base64.URLEncoding.EncodeToString(b)
}

type Commit struct {
	SHA     string    `json:"sha"`
	Message string    `json:"message"` // first line
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
}

// GitHubCompare reports how head relates to base and the commits base is missing (newest first, up to 50).
func GitHubCompare(ctx context.Context, c Credentials, ownerRepo, base, head string) (status string, ahead, behind int, commits []Commit, err error) {
	var out struct {
		Status   string `json:"status"` // ahead (head is newer), behind, identical, diverged
		AheadBy  int    `json:"ahead_by"`
		BehindBy int    `json:"behind_by"`
		Commits  []struct {
			SHA    string `json:"sha"`
			Commit struct {
				Message string `json:"message"`
				Author  struct {
					Name string    `json:"name"`
					Date time.Time `json:"date"`
				} `json:"author"`
			} `json:"commit"`
		} `json:"commits"`
	}
	if err = providerGet(ctx, c, "/repos/"+ownerRepo+"/compare/"+base+"..."+head, &out); err != nil {
		return
	}
	commits = []Commit{}
	for i := len(out.Commits) - 1; i >= 0 && len(commits) < 50; i-- {
		cm := out.Commits[i]
		msg, _, _ := strings.Cut(cm.Commit.Message, "\n")
		commits = append(commits, Commit{SHA: cm.SHA, Message: msg, Author: cm.Commit.Author.Name, Date: cm.Commit.Author.Date})
	}
	// From the installed build's point of view.
	switch out.Status {
	case "ahead":
		status = "behind"
	case "behind":
		status = "ahead"
	case "identical":
		status = "up-to-date"
	default:
		status = out.Status
	}
	return status, out.BehindBy, out.AheadBy, commits, nil
}

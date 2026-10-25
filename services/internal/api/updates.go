package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/sources"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/version"
)

/*
 * Updates: compares the commit this build came from with the Ignition repository, per release channel
 * (stable = newest release tag, prerelease = newest commit on the branch), and applies an update with a
 * short-lived updater container: it pulls in the install folder and rebuilds the api and worker containers.
 * Apps, the database and the domain proxy keep running; the dashboard is away for about a minute.
 */

type updateConfig struct {
	RepoURL  string `json:"repoUrl"`
	Branch   string `json:"branch"`
	SourceID string `json:"sourceId"` // saved credentials for a private repository
	Channel  string `json:"channel"`  // stable or prerelease
}

type releaseInfo struct {
	Version string `json:"version"` // tag name, or short commit for the branch
	Commit  string `json:"commit"`
	Ref     string `json:"ref"`  // what the updater fetches: tag name or branch
	Kind    string `json:"kind"` // tag or branch
	URL     string `json:"url"`  // release notes / changes
}

type updateCheck struct {
	CheckedAt time.Time        `json:"checkedAt"`
	Channel   string           `json:"channel"`
	Latest    *releaseInfo     `json:"latest"`
	Status    string           `json:"status"` // up-to-date, behind, ahead, diverged, unknown
	Behind    int              `json:"behind"` // commits you don't have yet (needs a GitHub token)
	Ahead     int              `json:"ahead"`
	Commits   []sources.Commit `json:"commits"`
	Error     string           `json:"error"`
}

// updateRun remembers the last update the updater container was asked to do.
type updateRun struct {
	Target     string    `json:"target"`
	Version    string    `json:"version"`
	From       string    `json:"from"`
	StartedAt  time.Time `json:"startedAt"`
	StartedBy  string    `json:"startedBy"`
	Container  string    `json:"container"`
	Dismissed  bool      `json:"dismissed"`
	State      string    `json:"state,omitempty"` // running, succeeded, failed (filled in when read)
	ExitCode   int       `json:"exitCode,omitempty"`
	Log        []string  `json:"log,omitempty"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
}

var updateBranchRE = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,100}$`)

var defaultUpdateConfig = updateConfig{RepoURL: "https://github.com/notsliver/Ignition", Branch: "main", Channel: "stable"}

func (s *Server) updateConfig(ctx context.Context) updateConfig {
	c := defaultUpdateConfig
	s.Store.GetSetting(ctx, "updates", &c)
	if c.Channel != "prerelease" {
		c.Channel = "stable"
	}
	return c
}

func installed() map[string]any {
	return map[string]any{
		"version": version.Version, "commit": version.Commit, "builtAt": version.BuiltAt, "dirty": version.Dirty == "true",
		"sourceDir": os.Getenv("IGNITION_SOURCE_DIR"),
	}
}

func (s *Server) updateCredentials(ctx context.Context, cfg updateConfig) sources.Credentials {
	creds := sources.Credentials{AuthType: "none", Provider: "git"}
	if cfg.SourceID != "" {
		if src, err := s.Store.GetSource(ctx, cfg.SourceID); err == nil {
			if c, err := s.credentials(src); err == nil {
				creds = c
			}
		}
	}
	return creds
}

func (s *Server) getUpdates(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out := map[string]any{"installed": installed(), "config": s.updateConfig(ctx)}
	var c updateCheck
	if s.Store.GetSetting(ctx, "updates_check", &c) == nil && !c.CheckedAt.IsZero() {
		out["lastCheck"] = c
	}
	if run := s.updateRunState(ctx); run != nil {
		out["run"] = run
	}
	if v, err := s.Runtime.Version(ctx); err == nil {
		out["docker"] = v.Docker
	}
	httpx.JSON(w, 200, out)
	return nil
}

func (s *Server) putUpdates(w http.ResponseWriter, r *http.Request, u *store.User) error {
	var in updateConfig
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.RepoURL, in.Branch = strings.TrimSuffix(strings.TrimSpace(in.RepoURL), ".git"), strings.TrimSpace(in.Branch)
	f := map[string]string{}
	if !sources.ValidHTTPURL(in.RepoURL) {
		f["repoUrl"] = "Enter the repository's https URL."
	}
	if !updateBranchRE.MatchString(in.Branch) {
		f["branch"] = "Enter a branch name."
	}
	if in.Channel != "stable" && in.Channel != "prerelease" {
		f["channel"] = "Stable or prerelease."
	}
	if in.SourceID != "" {
		if src, err := s.Store.GetSource(r.Context(), in.SourceID); err != nil || src.Kind != "git" {
			f["sourceId"] = "Pick a Git source."
		}
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	if err := s.Store.SetSetting(r.Context(), "updates", in); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "settings.updates", "settings", "updates", map[string]any{"repoUrl": in.RepoURL, "branch": in.Branch, "channel": in.Channel}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"config": in})
	return nil
}

// latestRelease finds what the channel points at: the newest stable tag, or the head of the branch.
func latestRelease(ctx context.Context, creds sources.Credentials, cfg updateConfig) (*releaseInfo, error) {
	ownerRepo := githubRepo(cfg.RepoURL)
	if cfg.Channel == "stable" {
		tags, err := sources.Tags(ctx, creds, cfg.RepoURL)
		if err != nil {
			return nil, err
		}
		for _, t := range tags {
			if !t.Prerelease {
				rel := &releaseInfo{Version: t.Name, Commit: t.Commit, Ref: t.Name, Kind: "tag"}
				if ownerRepo != "" {
					rel.URL = "https://github.com/" + ownerRepo + "/releases/tag/" + t.Name
				}
				return rel, nil
			}
		}
		return nil, nil // no releases published yet
	}
	branches, err := sources.Branches(ctx, creds, cfg.RepoURL)
	if err != nil {
		return nil, err
	}
	for _, b := range branches {
		if b.Name == cfg.Branch {
			rel := &releaseInfo{Version: b.Commit[:7], Commit: b.Commit, Ref: b.Name, Kind: "branch"}
			if ownerRepo != "" {
				rel.URL = "https://github.com/" + ownerRepo + "/commits/" + b.Name
			}
			return rel, nil
		}
	}
	return nil, fmt.Errorf("the repository has no branch called %s", cfg.Branch)
}

func (s *Server) checkUpdates(w http.ResponseWriter, r *http.Request, u *store.User) error {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	cfg := s.updateConfig(ctx)
	creds := s.updateCredentials(ctx, cfg)
	res := updateCheck{CheckedAt: time.Now().UTC(), Channel: cfg.Channel, Status: "unknown", Commits: []sources.Commit{}}
	latest, err := latestRelease(ctx, creds, cfg)
	current := version.Commit
	switch {
	case err != nil:
		res.Error = "Couldn't read " + cfg.RepoURL + ": " + err.Error() + "."
		if cfg.SourceID == "" && !strings.Contains(err.Error(), "credentials") {
			res.Error += " If the repository is private, choose credentials in Update source."
		}
	case latest == nil:
		res.Error = "No stable releases have been published yet. Switch to Prerelease to follow the newest commits."
	case current == "unknown" || current == "":
		res.Latest = latest
		res.Error = "This build doesn't know which commit it came from. Run the installer once to stamp it."
	default:
		res.Latest = latest
		if strings.HasPrefix(latest.Commit, current) || strings.HasPrefix(current, latest.Commit) {
			res.Status = "up-to-date"
			break
		}
		res.Status = "behind"
		// With a GitHub token we can tell how far behind (or ahead) this build is, and what changed.
		if ownerRepo := githubRepo(cfg.RepoURL); ownerRepo != "" && creds.Provider == "github" && creds.AuthType == "token" {
			if status, ahead, behind, commits, err := sources.GitHubCompare(ctx, creds, ownerRepo, current, latest.Commit); err == nil {
				res.Status, res.Ahead, res.Behind, res.Commits = status, ahead, behind, commits
			}
		}
	}
	s.Store.SetSetting(context.WithoutCancel(ctx), "updates_check", res)
	s.Store.Audit(r.Context(), u, "settings.update_check", "settings", "updates", map[string]any{"status": res.Status, "channel": cfg.Channel}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"lastCheck": res, "installed": installed()})
	return nil
}

// githubRepo returns owner/repo for github.com URLs.
func githubRepo(u string) string {
	rest, ok := strings.CutPrefix(u, "https://github.com/")
	if !ok {
		return ""
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

/* --------------------------------- Applying -------------------------------- */

const updaterImage = "docker:27-cli"

// The updater: refuse to touch uncommitted work, fast-forward to the target, rebuild api + worker.
const updaterScript = `set -eu
step() { printf '==> %s\n' "$1"; }
step "Preparing the updater"
apk add --no-cache git openssh-client >/dev/null 2>&1 || { echo "Couldn't install git in the updater container."; exit 10; }
cd /work
git config --global --add safe.directory /work
if [ -n "${SSH_KEY:-}" ]; then
  printf '%s\n' "$SSH_KEY" > /tmp/key && chmod 600 /tmp/key
  export GIT_SSH_COMMAND="ssh -i /tmp/key -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o BatchMode=yes"
fi
step "Checking the install folder for local changes"
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo "The install folder has uncommitted changes, so it wasn't touched. Commit or stash them, then update again:"
  git status --short --untracked-files=no | head -20
  exit 3
fi
step "Fetching $REF"
git fetch --quiet --tags "$REPO" "$REF"
step "Moving to $VERSION"
if [ "$KIND" = branch ]; then
  git checkout --quiet "$REF" 2>/dev/null || git checkout --quiet -b "$REF" "$TARGET"
  git merge-base --is-ancestor HEAD "$TARGET" || { echo "Your $REF has commits that aren't in the repository, so it can't fast-forward. Push or reset them first."; exit 4; }
  git merge --ff-only --quiet "$TARGET" || { echo "Couldn't move the install folder to $VERSION (see above). Nothing was rebuilt."; exit 5; }
else
  git checkout --quiet --detach "$TARGET"
fi
export IGNITION_COMMIT="$(git rev-parse HEAD)" IGNITION_VERSION="$(git describe --tags --always)" IGNITION_DIRTY=false
export IGNITION_BUILT_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)" IGNITION_SOURCE_DIR="$SOURCE_DIR"
step "Rebuilding Ignition $IGNITION_VERSION (your apps keep running)"
docker compose -p "$PROJECT" --env-file .env -f deploy/docker-compose.yml up -d --build --no-deps api worker 2>&1 | grep -v -e '^#' -e '^ *$' | tail -15
step "Update complete"
`

func (s *Server) updaterName(ctx context.Context) string {
	if p := s.Runtime.Instance(ctx); p != "" {
		return p + "-updater"
	}
	return "ignition-updater"
}

// updateRunState reads the last update and, from Docker, how the updater container is doing.
func (s *Server) updateRunState(ctx context.Context) *updateRun {
	var run updateRun
	if s.Store.GetSetting(ctx, "update_run", &run) != nil || run.StartedAt.IsZero() {
		return nil
	}
	st, err := s.Runtime.Inspect(ctx, run.Container)
	switch {
	case err != nil || !st.Exists:
		// The updater is gone (it removes itself only on success); judge by the commit we're running now.
		if strings.HasPrefix(version.Commit, run.Target) || strings.HasPrefix(run.Target, version.Commit) {
			run.State = "succeeded"
		} else {
			run.State = "failed"
			run.Log = []string{"The updater container is gone and Ignition is still on the old version."}
		}
	case st.Running:
		run.State = "running"
	case st.ExitCode == 0:
		run.State = "succeeded"
	default:
		run.State, run.ExitCode = "failed", st.ExitCode
	}
	if st.Exists {
		run.FinishedAt = st.FinishedAt
		if lines, err := s.Runtime.Logs(ctx, run.Container, 60, time.Time{}); err == nil {
			for _, l := range lines {
				run.Log = append(run.Log, l.Message)
			}
		}
	}
	return &run
}

func (s *Server) applyUpdate(w http.ResponseWriter, r *http.Request, u *store.User) error {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	sourceDir := os.Getenv("IGNITION_SOURCE_DIR")
	project := s.Runtime.Instance(ctx)
	if sourceDir == "" || project == "" {
		return httpx.Unsupported("This install doesn't know which folder it was installed from. Run the installer once more, then updates can run from here.")
	}
	if run := s.updateRunState(ctx); run != nil && run.State == "running" {
		return httpx.Conflict("An update is already running.")
	}
	var check updateCheck
	s.Store.GetSetting(ctx, "updates_check", &check)
	if check.Latest == nil || check.Status != "behind" {
		return httpx.Conflict("There's no newer version to update to. Check for updates first.")
	}
	cfg := s.updateConfig(ctx)
	creds := s.updateCredentials(ctx, cfg)

	// The updater joins Ignition's own network, so it can reach the same Git servers the API can.
	network := ""
	self, _ := os.Hostname()
	if st, err := s.Runtime.Inspect(ctx, self); err == nil {
		for _, n := range st.Networks {
			if strings.HasSuffix(n.Network, "_default") {
				network = n.Network
			}
		}
	}
	env := []string{
		"REPO=" + sources.RepoURLFor(creds, cfg.RepoURL), "REF=" + check.Latest.Ref, "KIND=" + check.Latest.Kind,
		"TARGET=" + check.Latest.Commit, "VERSION=" + check.Latest.Version, "PROJECT=" + project, "SOURCE_DIR=" + sourceDir,
		// Windows checkouts use CRLF; without this every file would look modified to the Linux git in the updater.
		"GIT_CONFIG_COUNT=3", "GIT_CONFIG_KEY_0=core.autocrlf", "GIT_CONFIG_VALUE_0=" + map[bool]string{true: "true", false: "false"}[isWindowsPath(sourceDir)],
		"GIT_CONFIG_KEY_1=core.filemode", "GIT_CONFIG_VALUE_1=false", "GIT_CONFIG_KEY_2=advice.detachedHead", "GIT_CONFIG_VALUE_2=false",
		"GIT_TERMINAL_PROMPT=0",
	}
	switch creds.AuthType {
	case "token", "password":
		user := creds.Username
		if user == "" {
			user = map[string]string{"github": "x-access-token", "gitlab": "oauth2"}[creds.Provider]
			if user == "" {
				user = "git"
			}
		}
		env = append(env, "GIT_CONFIG_COUNT=4", "GIT_CONFIG_KEY_3=http.extraHeader",
			"GIT_CONFIG_VALUE_3=Authorization: Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+creds.Secret)))
	case "ssh":
		env = append(env, "SSH_KEY="+creds.Secret)
	}

	if err := s.Runtime.PullImage(ctx, updaterImage, "", func(string, string) {}); err != nil {
		return httpx.Unavailable("Couldn't download the updater image: " + err.Error())
	}
	name := s.updaterName(ctx)
	s.Runtime.Remove(ctx, name, false) // the previous run's container, kept for its logs
	id, err := s.Runtime.CreateContainer(ctx, runtime.ContainerSpec{
		Name: name, Image: updaterImage, Env: env, Cmd: []string{"sh", "-c", updaterScript}, Network: network,
		Binds:  []string{"/var/run/docker.sock:/var/run/docker.sock", sourceDir + ":/work"},
		Labels: map[string]string{"ignition.managed": "true", "ignition.updater": "true"},
		Limits: runtime.Limits{Restart: "never"}, MemoryMB: 2048, CPUs: 2,
	})
	if err != nil {
		return httpx.Unavailable("Couldn't create the updater: " + err.Error())
	}
	run := updateRun{Target: check.Latest.Commit, Version: check.Latest.Version, From: version.Commit, StartedAt: time.Now().UTC(), StartedBy: u.Email, Container: name}
	if err := s.Store.SetSetting(ctx, "update_run", run); err != nil {
		return err
	}
	if err := s.Runtime.Start(ctx, id); err != nil {
		return httpx.Unavailable("Couldn't start the updater: " + err.Error())
	}
	s.Store.Audit(r.Context(), u, "settings.update", "settings", "updates", map[string]any{"from": version.Commit, "to": check.Latest.Version}, clientIP(r))
	run.State = "running"
	httpx.JSON(w, 202, map[string]any{"run": run})
	return nil
}

// dismissUpdate hides the "updated" / "update failed" notice.
func (s *Server) dismissUpdate(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	var run updateRun
	if s.Store.GetSetting(r.Context(), "update_run", &run) == nil && !run.StartedAt.IsZero() {
		run.Dismissed = true
		s.Store.SetSetting(r.Context(), "update_run", run)
	}
	httpx.JSON(w, 204, nil)
	return nil
}

func isWindowsPath(p string) bool {
	return len(p) > 2 && p[1] == ':' && (p[2] == '/' || p[2] == '\\')
}

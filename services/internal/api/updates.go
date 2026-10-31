package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/version"
)

/*
 * Updates come from the published Docker image (notsliver/ignition on Docker Hub). A check reads the
 * image's tags and picks the newest for the channel: stable (newest release), beta (newest release
 * or pre-release) or edge (built from main on every push). Updating runs a short-lived updater
 * container that pulls the image, records it as IGNITION_IMAGE in the install's .env (so restarts
 * keep the version), and recreates the api and worker containers. Apps, the database and the domain
 * proxy keep running; the dashboard is away for about a minute.
 */

type updateConfig struct {
	Image   string `json:"image"`   // Docker Hub repository, e.g. notsliver/ignition
	Channel string `json:"channel"` // stable, beta or edge
}

type releaseInfo struct {
	Version string `json:"version"` // the image tag, e.g. 0.2.0 or edge
	Commit  string `json:"commit"`  // short commit for edge builds, when known
	Image   string `json:"image"`   // what the updater pulls, e.g. notsliver/ignition:0.2.0
	Digest  string `json:"digest"`
	Updated string `json:"updated"` // when the tag was pushed
	URL     string `json:"url"`     // release notes / changes
}

type updateCheck struct {
	CheckedAt time.Time    `json:"checkedAt"`
	Channel   string       `json:"channel"`
	Latest    *releaseInfo `json:"latest"`
	Status    string       `json:"status"` // up-to-date, behind, ahead, unknown
	Error     string       `json:"error"`
}

// updateRun remembers the last update the updater container was asked to do.
type updateRun struct {
	Target     string    `json:"target"` // image reference
	Version    string    `json:"version"`
	Commit     string    `json:"commit,omitempty"`
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

const defaultImage = "notsliver/ignition"
const releaseNotesURL = "https://github.com/notsliver/Ignition/releases/tag/v"

var imageRepoRE = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*/[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

func (s *Server) updateConfig(ctx context.Context) updateConfig {
	c := updateConfig{Image: defaultImage, Channel: "stable"}
	s.Store.GetSetting(ctx, "updates", &c)
	if c.Channel == "prerelease" { // older installs followed git; their prerelease channel is now beta
		c.Channel = "beta"
	}
	if c.Channel != "beta" && c.Channel != "edge" {
		c.Channel = "stable"
	}
	if !imageRepoRE.MatchString(c.Image) || !localDevelopment() {
		c.Image = defaultImage
	}
	return c
}

func installed() map[string]any {
	_, release := parseSemver(version.Version)
	return map[string]any{
		"version": version.Version, "commit": version.Commit, "builtAt": version.BuiltAt, "dirty": version.Dirty == "true",
		"sourceDir": os.Getenv("IGNITION_SOURCE_DIR"), "image": os.Getenv("IGNITION_IMAGE"),
		// A release image carries its tag as the version; anything else was built from a checkout.
		"fromSource": !release && version.Version != "edge",
		"localDevelopment": localDevelopment(),
	}
}

// localDevelopment unlocks settings only Ignition developers need, like updating from another image.
func localDevelopment() bool { return os.Getenv("IGNITION_LOCAL_DEVELOPMENT") == "true" }

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
	in.Image = strings.TrimSpace(in.Image)
	if in.Image == "" || !localDevelopment() {
		in.Image = defaultImage // only local development may follow a different image
	}
	f := map[string]string{}
	if !imageRepoRE.MatchString(in.Image) {
		f["image"] = "A Docker Hub repository like notsliver/ignition."
	}
	if in.Channel != "stable" && in.Channel != "beta" && in.Channel != "edge" {
		f["channel"] = "Stable, beta or edge."
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	if err := s.Store.SetSetting(r.Context(), "updates", in); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "settings.updates", "settings", "updates", map[string]any{"image": in.Image, "channel": in.Channel}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"config": in})
	return nil
}

/* ------------------------------- Docker Hub tags ------------------------------ */

type hubTag struct {
	Name        string `json:"name"`
	Digest      string `json:"digest"`
	LastUpdated string `json:"last_updated"`
}

// hubURL is a variable so tests can point it at a fake registry.
var hubURL = "https://hub.docker.com/v2/namespaces/%s/repositories/%s/tags?page_size=100&ordering=last_updated"

func hubTags(ctx context.Context, image string) ([]hubTag, error) {
	ns, repo, _ := strings.Cut(image, "/")
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf(hubURL, ns, repo), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Docker Hub isn't reachable: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s isn't published on Docker Hub yet", image)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Docker Hub answered %s", resp.Status)
	}
	var out struct{ Results []hubTag }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("Docker Hub sent an unexpected answer: %v", err)
	}
	return out.Results, nil
}

type semver struct {
	nums [3]int
	pre  string
}

// parseSemver reads 1.2.3 or 1.2.3-beta.1 (an optional leading v is allowed).
func parseSemver(s string) (semver, bool) {
	s = strings.TrimPrefix(s, "v")
	core, pre, _ := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var v semver
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, false
		}
		v.nums[i] = n
	}
	v.pre = pre
	return v, true
}

// less orders versions; a release sorts after its own pre-releases.
func (a semver) less(b semver) bool {
	for i := range a.nums {
		if a.nums[i] != b.nums[i] {
			return a.nums[i] < b.nums[i]
		}
	}
	switch {
	case a.pre == b.pre:
		return false
	case a.pre == "":
		return false
	case b.pre == "":
		return true
	}
	return comparePre(a.pre, b.pre) < 0
}

func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		switch {
		case aerr == nil && berr == nil && an != bn:
			if an < bn {
				return -1
			}
			return 1
		case aerr != nil || berr != nil:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return len(as) - len(bs)
}

// pickRelease finds the tag the channel points at.
func pickRelease(tags []hubTag, image, channel string) *releaseInfo {
	if channel == "edge" {
		for _, t := range tags {
			if t.Name != "edge" {
				continue
			}
			rel := &releaseInfo{Version: "edge", Image: image + ":edge", Digest: t.Digest, Updated: t.LastUpdated, URL: "https://github.com/notsliver/Ignition/commits/main"}
			for _, o := range tags { // sha-<short> is pushed alongside edge with the same digest
				if strings.HasPrefix(o.Name, "sha-") && o.Digest == t.Digest {
					rel.Commit = strings.TrimPrefix(o.Name, "sha-")
				}
			}
			return rel
		}
		return nil
	}
	type cand struct {
		v semver
		t hubTag
	}
	var list []cand
	for _, t := range tags {
		v, ok := parseSemver(t.Name)
		if !ok || (channel == "stable" && v.pre != "") {
			continue
		}
		list = append(list, cand{v, t})
	}
	if len(list) == 0 {
		return nil
	}
	sort.Slice(list, func(i, j int) bool { return list[j].v.less(list[i].v) })
	t := list[0].t
	return &releaseInfo{Version: t.Name, Image: image + ":" + t.Name, Digest: t.Digest, Updated: t.LastUpdated, URL: releaseNotesURL + strings.TrimPrefix(t.Name, "v")}
}

// compareInstalled says whether the running build is behind the release.
func compareInstalled(rel *releaseInfo) string {
	if rel.Version == "edge" {
		if rel.Commit != "" && version.Commit != "" && strings.HasPrefix(version.Commit, rel.Commit) {
			return "up-to-date"
		}
		return "behind"
	}
	cur, ok := parseSemver(version.Version)
	if !ok {
		return "behind" // built from source or edge: switching to the release image is an update
	}
	latest, _ := parseSemver(rel.Version)
	switch {
	case cur.less(latest):
		return "behind"
	case latest.less(cur):
		return "ahead"
	}
	return "up-to-date"
}

func (s *Server) checkUpdates(w http.ResponseWriter, r *http.Request, u *store.User) error {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	cfg := s.updateConfig(ctx)
	res := updateCheck{CheckedAt: time.Now().UTC(), Channel: cfg.Channel, Status: "unknown"}
	tags, err := hubTags(ctx, cfg.Image)
	switch {
	case err != nil:
		res.Error = "Couldn't read " + cfg.Image + " on Docker Hub: " + err.Error() + "."
	default:
		res.Latest = pickRelease(tags, cfg.Image, cfg.Channel)
		if res.Latest == nil {
			res.Error = map[string]string{
				"stable": "No stable release has been published yet. Switch to Beta to follow pre-releases.",
				"beta":   "No release has been published yet.",
				"edge":   "No edge build has been published yet.",
			}[cfg.Channel]
		} else {
			res.Status = compareInstalled(res.Latest)
		}
	}
	s.Store.SetSetting(context.WithoutCancel(ctx), "updates_check", res)
	s.Store.Audit(r.Context(), u, "settings.update_check", "settings", "updates", map[string]any{"status": res.Status, "channel": cfg.Channel}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"lastCheck": res, "installed": installed()})
	return nil
}

/* --------------------------------- Applying -------------------------------- */

const updaterImage = "docker:27-cli"

// The updater: pull the image, pin it in .env, recreate api + worker without rebuilding anything.
const updaterScript = `set -eu
step() { printf '==> %s\n' "$1"; }
cd /work
[ -f .env ] || { echo "The install folder has no .env, so Ignition can't be restarted safely. Nothing was changed."; exit 3; }
step "Pulling $IMAGE"
docker pull "$IMAGE" 2>&1 | tail -3
step "Recording $IMAGE in .env"
cp .env .env.before-update
if grep -q '^IGNITION_IMAGE=' .env; then
  sed -i "s|^IGNITION_IMAGE=.*|IGNITION_IMAGE=$IMAGE|" .env
else
  printf '\nIGNITION_IMAGE=%s\n' "$IMAGE" >> .env
fi
step "Restarting Ignition on $VERSION (your apps keep running)"
if ! docker compose -p "$PROJECT" --env-file .env -f deploy/docker-compose.yml up -d --no-build --no-deps api worker 2>&1 | grep -v -e '^ *$' | tail -15; then
  cp .env.before-update .env
  echo "Restarting failed; .env was put back as it was."
  exit 5
fi
rm -f .env.before-update
step "Update complete"
`

func (s *Server) updaterName(ctx context.Context) string {
	if p := s.Runtime.Instance(ctx); p != "" {
		return p + "-updater"
	}
	return "ignition-updater"
}

// runningTarget reports whether this process is the version an update asked for.
func runningTarget(run updateRun) bool {
	if run.Version == "edge" {
		return run.Commit != "" && strings.HasPrefix(version.Commit, run.Commit)
	}
	return strings.TrimPrefix(version.Version, "v") == strings.TrimPrefix(run.Version, "v")
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
		if runningTarget(run) {
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
	rel := check.Latest

	network := ""
	self, _ := os.Hostname()
	if st, err := s.Runtime.Inspect(ctx, self); err == nil {
		for _, n := range st.Networks {
			if strings.HasSuffix(n.Network, "_default") {
				network = n.Network
			}
		}
	}
	if err := s.Runtime.PullImage(ctx, updaterImage, "", func(string, string) {}); err != nil {
		return httpx.Unavailable("Couldn't download the updater image: " + err.Error())
	}
	name := s.updaterName(ctx)
	s.Runtime.Remove(ctx, name, false) // the previous run's container, kept for its logs
	id, err := s.Runtime.CreateContainer(ctx, runtime.ContainerSpec{
		Name: name, Image: updaterImage, Cmd: []string{"sh", "-c", updaterScript}, Network: network,
		Env:    []string{"IMAGE=" + rel.Image, "VERSION=" + rel.Version, "PROJECT=" + project},
		Binds:  []string{"/var/run/docker.sock:/var/run/docker.sock", sourceDir + ":/work"},
		Labels: map[string]string{"ignition.managed": "true", "ignition.updater": "true"},
		Limits: runtime.Limits{Restart: "never"}, MemoryMB: 512, CPUs: 1,
	})
	if err != nil {
		return httpx.Unavailable("Couldn't create the updater: " + err.Error())
	}
	from := version.Version
	if from == "" || from == "dev" {
		from = version.Commit
	}
	run := updateRun{Target: rel.Image, Version: rel.Version, Commit: rel.Commit, From: from, StartedAt: time.Now().UTC(), StartedBy: u.Email, Container: name}
	if err := s.Store.SetSetting(ctx, "update_run", run); err != nil {
		return err
	}
	if err := s.Runtime.Start(ctx, id); err != nil {
		return httpx.Unavailable("Couldn't start the updater: " + err.Error())
	}
	s.Store.Audit(r.Context(), u, "settings.update", "settings", "updates", map[string]any{"from": from, "to": rel.Image}, clientIP(r))
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

package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/notsliver/Ignition/services/internal/compose"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/sources"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/worker"
)

/*
 * New project › Import from Git repository, From a Docker Compose file, Duplicate an existing project.
 *
 * Git imports look at the repository first: a compose file becomes one resource per service (built
 * from the repository where the service has a build:), otherwise the repository becomes one
 * application built from its Dockerfile or the detected stack. Every import is previewed before
 * anything is created, and created in one transaction.
 */

var composeFiles = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

type gitInput struct {
	RepoURL  string `json:"repoUrl"`
	Branch   string `json:"branch"`
	SourceID string `json:"sourceId"`
}

func (s *Server) gitCredentials(ctx context.Context, sourceID string) (*sources.Credentials, error) {
	if sourceID == "" {
		return nil, nil
	}
	src, err := s.Store.GetSource(ctx, sourceID)
	if err != nil {
		return nil, httpx.Invalid(map[string]string{"sourceId": "Unknown source."})
	}
	c, err := s.credentials(src)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

type gitInspection struct {
	Commit        string        `json:"commit"`
	SuggestedName string        `json:"suggestedName"`
	ComposeFile   string        `json:"composeFile,omitempty"`
	Compose       string        `json:"compose,omitempty"`
	Plan          *compose.Plan `json:"plan,omitempty"`
	Dockerfile    bool          `json:"dockerfile"`
	Stack         string        `json:"stack,omitempty"`
	StackSummary  string        `json:"stackSummary,omitempty"`
	Port          int           `json:"port,omitempty"`
	StackError    string        `json:"stackError,omitempty"`
	HasEnvExample bool          `json:"hasEnvExample"`
}

func repoName(url string) string {
	base := strings.TrimSuffix(strings.TrimRight(url, "/"), ".git")
	if i := strings.LastIndexAny(base, "/:"); i >= 0 {
		base = base[i+1:]
	}
	return compose.SafeName(base)
}

// inspectGit clones the branch (shallow) and reports what an import would create.
func (s *Server) inspectGit(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	var in gitInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	out, err := s.inspectRepo(r.Context(), in)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, out)
	return nil
}

func (s *Server) inspectRepo(ctx context.Context, in gitInput) (*gitInspection, error) {
	in.RepoURL, in.Branch = strings.TrimSuffix(strings.TrimSpace(in.RepoURL), ".git"), strings.TrimSpace(in.Branch)
	if in.Branch == "" {
		in.Branch = "main"
	}
	creds, err := s.gitCredentials(ctx, in.SourceID)
	if err != nil {
		return nil, err
	}
	if err := worker.ValidateGitSourceFor(in.RepoURL, in.Branch, creds != nil && creds.AuthType == "ssh"); err != nil {
		return nil, httpx.Invalid(map[string]string{"repoUrl": errText(err)})
	}
	// ls-remote first: it explains private or missing repositories and branches clearly, and fast.
	lctx, lcancel := context.WithTimeout(ctx, 30*time.Second)
	c := sources.Credentials{AuthType: "none", Provider: "git"}
	if creds != nil {
		c = *creds
	}
	branches, err := sources.Branches(lctx, c, in.RepoURL)
	lcancel()
	if err != nil {
		return nil, httpx.Invalid(map[string]string{"repoUrl": capitalize(errText(err)) + "."})
	}
	found := false
	var names []string
	for _, b := range branches {
		found = found || b.Name == in.Branch
		names = append(names, b.Name)
	}
	if !found {
		hint := ""
		if len(names) > 0 && len(names) <= 8 {
			hint = " Branches: " + strings.Join(names, ", ") + "."
		}
		return nil, httpx.Invalid(map[string]string{"branch": "The repository has no branch called " + in.Branch + "." + hint})
	}
	dir, err := os.MkdirTemp("", "ignition-inspect-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	commit, err := worker.CloneRepo(cctx, in.RepoURL, in.Branch, dir, creds)
	if err != nil {
		return nil, httpx.Invalid(map[string]string{"repoUrl": errText(err)})
	}
	out := &gitInspection{Commit: commit, SuggestedName: repoName(in.RepoURL)}
	for _, f := range composeFiles {
		if b, err := os.ReadFile(filepath.Join(dir, f)); err == nil {
			env := map[string]string{}
			if e, err := os.ReadFile(filepath.Join(dir, ".env")); err == nil {
				env = compose.ParseEnvFile(string(e))
			} else if e, err := os.ReadFile(filepath.Join(dir, ".env.example")); err == nil {
				env = compose.ParseEnvFile(string(e))
				out.HasEnvExample = true
			}
			out.ComposeFile, out.Compose = f, string(b)
			if plan, err := compose.Parse(b, env); err == nil {
				out.Plan = plan
			} else {
				out.StackError = f + ": " + err.Error()
			}
			break
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "Dockerfile")); err == nil {
		out.Dockerfile = true
	} else if plan, err := worker.DetectStack(dir, 0, false); err == nil {
		out.Stack, out.StackSummary, out.Port = plan.Stack, plan.Summary, exposedPort(plan.Dockerfile)
	} else if out.Plan == nil {
		out.StackError = err.Error()
	}
	return out, nil
}

func errText(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, ": "); i >= 0 && strings.HasPrefix(msg, "permanent") {
		msg = msg[i+2:]
	}
	return msg
}

type composeInput struct {
	Compose string `json:"compose"`
	Env     string `json:"env"`
}

// previewCompose parses a pasted compose file.
func (s *Server) previewCompose(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	var in composeInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if len(in.Compose) > 512<<10 {
		return httpx.Invalid(map[string]string{"compose": "That file is larger than 512 KB."})
	}
	plan, err := compose.Parse([]byte(in.Compose), compose.ParseEnvFile(in.Env))
	if err != nil {
		return httpx.Invalid(map[string]string{"compose": capitalize(err.Error()) + "."})
	}
	for i := range plan.Services {
		if svc := &plan.Services[i]; svc.Build != nil && svc.Image == "" {
			svc.Warnings = append([]string{"Builds from source, which needs the repository: import it from Git instead, or give it an image."}, svc.Warnings...)
		}
	}
	httpx.JSON(w, 200, map[string]any{"plan": plan})
	return nil
}

type projectImport struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind"` // git, compose
	gitInput
	Compose  string   `json:"compose"`
	Env      string   `json:"env"`
	Services []string `json:"services"` // compose services to create; empty = all
	Port     *int     `json:"port"`     // single-app git import
	Deploy   *bool    `json:"deploy"`
}

// importProject creates a project from a Git repository or a compose file.
func (s *Server) importProject(w http.ResponseWriter, r *http.Request, u *store.User) error {
	var in projectImport
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	in.Name = strings.TrimSpace(in.Name)
	if f := validateProject(projectInput{Name: &in.Name, Description: &in.Description}, true); len(f) > 0 {
		return httpx.Invalid(f)
	}
	deploy := in.Deploy == nil || *in.Deploy

	// Work out the resources first; nothing is created if any of them is invalid.
	var plan []resourcePlan
	switch in.Kind {
	case "git":
		insp, err := s.inspectRepo(ctx, in.gitInput)
		if err != nil {
			return err
		}
		if insp.Plan != nil && len(in.Services) > 0 || insp.Plan != nil && in.Port == nil {
			plan, err = planCompose(insp.Plan, in.Services, &in.gitInput)
			if err != nil {
				return err
			}
		} else {
			if insp.StackError != "" && !insp.Dockerfile {
				return httpx.Invalid(map[string]string{"repoUrl": insp.StackError})
			}
			port := in.Port
			if port == nil && insp.Port > 0 {
				port = &insp.Port
			}
			plan = []resourcePlan{{in: resourceInput{Name: insp.SuggestedName, Type: "application", SourceType: "git", RepoURL: strings.TrimSuffix(strings.TrimSpace(in.RepoURL), ".git"),
				Branch: firstNonBlank(strings.TrimSpace(in.Branch), "main"), Dockerfile: "Dockerfile", Port: port, SourceID: in.SourceID}}}
		}
	case "compose":
		p, err := compose.Parse([]byte(in.Compose), compose.ParseEnvFile(in.Env))
		if err != nil {
			return httpx.Invalid(map[string]string{"compose": capitalize(err.Error()) + "."})
		}
		if plan, err = planCompose(p, in.Services, nil); err != nil {
			return err
		}
	default:
		return httpx.BadRequest("Import kind is git or compose.")
	}
	if len(plan) == 0 {
		return httpx.Invalid(map[string]string{"services": "Pick at least one service."})
	}
	for i := range plan {
		p := &plan[i]
		if err := s.checkResourceSource(ctx, &p.in); err != nil {
			return err
		}
		if f := p.in.validate(); len(f) > 0 {
			for k, v := range f {
				return httpx.Invalid(map[string]string{"services": fmt.Sprintf("%s: %s (%s)", p.in.Name, v, k)})
			}
		}
	}

	tx, err := s.Store.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var projectID string
	if err := tx.QueryRow(ctx, `INSERT INTO projects (name, description, created_by) VALUES ($1, $2, $3) RETURNING id`, in.Name, strings.TrimSpace(in.Description), u.ID).Scan(&projectID); err != nil {
		if store.IsUnique(err) {
			return httpx.Invalid(map[string]string{"name": "A project with this name already exists."})
		}
		return err
	}
	var envID string
	if err := tx.QueryRow(ctx, `INSERT INTO environments (project_id, name, position) VALUES ($1, 'Production', 0) RETURNING id`, projectID).Scan(&envID); err != nil {
		return err
	}
	created := []string{}
	for _, p := range plan {
		p.x.NoDeploy = !deploy
		if _, _, err := s.provision(ctx, tx, u, projectID, envID, p.in, p.x); err != nil {
			if store.IsUnique(err) {
				return httpx.Invalid(map[string]string{"services": "Two services end up with the name " + p.in.Name + "."})
			}
			return err
		}
		created = append(created, p.in.Name)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.Store.Audit(ctx, u, "project.import", "project", projectID, map[string]any{"name": in.Name, "kind": in.Kind, "resources": created}, clientIP(r))
	proj, err := s.Store.GetProject(ctx, u.ID, projectID)
	if err != nil {
		return err
	}
	httpx.JSON(w, 201, map[string]any{"project": proj, "resources": created})
	return nil
}

type resourcePlan struct {
	in resourceInput
	x  provisionExtras
}

var engineByImage = []struct{ prefix, engine string }{{"postgres", "postgresql"}, {"postgis", "postgresql"}, {"timescale", "postgresql"}, {"mysql", "mysql"}, {"mariadb", "mariadb"},
	{"mongo", "mongodb"}, {"redis", "redis"}, {"valkey", "redis"}, {"dragonfly", "dragonfly"}, {"clickhouse", "clickhouse"}}

func engineOf(image string) string {
	repo := strings.ToLower(image)
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		repo = repo[i+1:]
	}
	for _, e := range engineByImage {
		if strings.HasPrefix(repo, e.prefix) {
			return e.engine
		}
	}
	return ""
}

func planCompose(p *compose.Plan, only []string, git *gitInput) ([]resourcePlan, error) {
	want := map[string]bool{}
	for _, n := range only {
		want[n] = true
	}
	var out []resourcePlan
	for _, svc := range p.Services {
		if len(want) > 0 && !want[svc.Name] {
			continue
		}
		in := resourceInput{Name: svc.Name, Type: svc.Type, Port: svc.Port, Command: svc.Command, MemoryMB: svc.MemoryMB, CPUs: svc.CPUs}
		x := provisionExtras{Restart: svc.Restart, HealthCommand: svc.Health, Volumes: svc.Volumes, Engine: engineOf(svc.Image)}
		switch {
		case svc.Build != nil && git != nil:
			in.SourceType, in.RepoURL, in.Branch, in.SourceID = "git", strings.TrimSuffix(strings.TrimSpace(git.RepoURL), ".git"), firstNonBlank(strings.TrimSpace(git.Branch), "main"), git.SourceID
			in.Dockerfile = svc.Build.Dockerfile
			x.Build = svc.Build
		case svc.Image != "":
			in.SourceType, in.Image = "image", svc.Image
		case svc.Build != nil:
			return nil, httpx.Invalid(map[string]string{"services": svc.Name + " builds from source; import the project from its Git repository, or leave this service out."})
		default:
			return nil, httpx.Invalid(map[string]string{"services": svc.Name + " has no image to run."})
		}
		if in.Type == "database" && in.Port == nil {
			in.Port = nil // databases stay private; others reach them by name on the environment network
		}
		for _, e := range svc.Env {
			in.Env = append(in.Env, envInput{Key: e.Key, Value: e.Value, Secret: e.Secret})
		}
		if len(svc.Volumes) > 0 { // the first volume keeps the familiar "data" name only when it is called data
			x.Volumes = svc.Volumes
		}
		out = append(out, resourcePlan{in: in, x: x})
	}
	// Dependencies first, so databases start before the apps that need them.
	sort.SliceStable(out, func(i, j int) bool { return out[i].in.Type == "database" && out[j].in.Type != "database" })
	return out, nil
}

/* ------------------------------- Duplication -------------------------------- */

// Columns copied as they are when a resource is duplicated; runtime state starts fresh.
var notCopied = map[string]bool{"id": true, "project_id": true, "environment_id": true, "status": true, "container_id": true, "host_port": true,
	"current_deployment_id": true, "running_since": true, "created_by": true, "created_at": true, "updated_at": true, "desired_state": true}

var columnRE = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

type duplicateInput struct {
	Name             string `json:"name"`
	IncludeVariables bool   `json:"includeVariables"`
	Deploy           bool   `json:"deploy"`
}

// duplicateProject copies a project's environments and resources with their settings. Data in
// volumes, domains, deploy history, tokens and automations are not copied (they belong to the original).
func (s *Server) duplicateProject(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Project")
	if err != nil {
		return err
	}
	ctx := r.Context()
	src, err := s.Store.GetProject(ctx, u.ID, id)
	if err != nil {
		return notFound(err, "Project")
	}
	var in duplicateInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	desc := src.Description
	if f := validateProject(projectInput{Name: &in.Name, Description: &desc}, true); len(f) > 0 {
		return httpx.Invalid(f)
	}
	var cols []string
	rows, err := s.Store.DB.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_name = 'resources' AND table_schema = current_schema() ORDER BY ordinal_position`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var c string
		if rows.Scan(&c) == nil && !notCopied[c] && columnRE.MatchString(c) {
			cols = append(cols, c)
		}
	}
	rows.Close()
	list := strings.Join(cols, ", ")

	tx, err := s.Store.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var newID string
	if err := tx.QueryRow(ctx, `INSERT INTO projects (name, description, created_by) VALUES ($1, $2, $3) RETURNING id`, in.Name, src.Description, u.ID).Scan(&newID); err != nil {
		if store.IsUnique(err) {
			return httpx.Invalid(map[string]string{"name": "A project with this name already exists."})
		}
		return err
	}
	envMap := map[string]string{}
	for _, e := range src.Environments {
		var eid string
		if err := tx.QueryRow(ctx, `INSERT INTO environments (project_id, name, position) SELECT $1, name, position FROM environments WHERE id = $2 RETURNING id`, newID, e.ID).Scan(&eid); err != nil {
			return err
		}
		envMap[e.ID] = eid
	}
	resources, err := s.Store.ListResources(ctx, id, "", "", "")
	if err != nil {
		return err
	}
	type copied struct{ oldID, newID, name string }
	var done []copied
	var notes []string
	for _, res := range resources {
		var rid string
		q := fmt.Sprintf(`INSERT INTO resources (project_id, environment_id, created_by, %s) SELECT $2, $3, $4, %s FROM resources WHERE id = $1 RETURNING id`, list, list)
		if err := tx.QueryRow(ctx, q, res.ID, newID, envMap[res.EnvironmentID], u.ID).Scan(&rid); err != nil {
			return fmt.Errorf("copy %s: %w", res.Name, err)
		}
		// Settings that live in their own tables. Fixed host ports can't be shared, so copies get a free one.
		for _, stmt := range []string{
			`INSERT INTO resource_volumes (resource_id, name, mount_path) SELECT $2, name, mount_path FROM resource_volumes WHERE resource_id = $1`,
			`INSERT INTO resource_ports (resource_id, host_port, container_port, protocol, bind_address) SELECT $2, NULL, container_port, protocol, bind_address FROM resource_ports WHERE resource_id = $1`,
			`INSERT INTO resource_networks (resource_id, network_id) SELECT $2, network_id FROM resource_networks WHERE resource_id = $1`,
			`INSERT INTO resource_health (resource_id, enabled, kind, path, port, expect_status, command, interval_seconds, timeout_seconds, retries, start_period_seconds, restart_unhealthy)
			 SELECT $2, enabled, kind, path, port, expect_status, command, interval_seconds, timeout_seconds, retries, start_period_seconds, restart_unhealthy FROM resource_health WHERE resource_id = $1`,
			`INSERT INTO scheduled_tasks (resource_id, name, schedule, timezone, command, timeout_seconds, enabled, created_by)
			 SELECT $2, name, schedule, timezone, command, timeout_seconds, false, created_by FROM scheduled_tasks WHERE resource_id = $1`,
		} {
			if _, err := tx.Exec(ctx, stmt, res.ID, rid); err != nil {
				return fmt.Errorf("copy settings of %s: %w", res.Name, err)
			}
		}
		// Variables are sealed for their resource, so they're opened and sealed again for the copy.
		vars, err := s.Store.ListEnv(ctx, res.ID)
		if err != nil {
			return err
		}
		for _, v := range vars {
			value := ""
			if in.IncludeVariables || !v.Secret {
				plain, err := s.Secrets.Open(v.Sealed, res.ID+":"+v.Key)
				if err != nil {
					return fmt.Errorf("%s's variable %s can't be decrypted", res.Name, v.Key)
				}
				value = plain
			}
			sealed, err := s.Secrets.Seal(value, rid+":"+v.Key)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO env_vars (resource_id, key, value_enc, secret, updated_by) VALUES ($1, $2, $3, $4, $5)`, rid, v.Key, sealed, v.Secret, u.ID); err != nil {
				return err
			}
		}
		done = append(done, copied{res.ID, rid, res.Name})
	}
	if !in.IncludeVariables {
		notes = append(notes, "Secret variables were copied with empty values; fill them in before deploying.")
	}
	deployed := 0
	if in.Deploy {
		for _, c := range done {
			res, _ := s.Store.GetResource(ctx, c.oldID)
			if res == nil || res.SourceType == "external" {
				continue
			}
			depID, err := s.Store.CreateDeployment(ctx, tx, c.newID, "create", "", res.Branch, "Duplicated from "+src.Name, u.ID)
			if err != nil {
				return err
			}
			if _, err := enqueueDeploy(ctx, tx, c.newID, depID, u.ID); err != nil {
				return err
			}
			deployed++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.Store.Audit(ctx, u, "project.duplicate", "project", newID, map[string]any{"from": src.Name, "name": in.Name, "resources": len(done), "variables": in.IncludeVariables}, clientIP(r))
	proj, err := s.Store.GetProject(ctx, u.ID, newID)
	if err != nil {
		return err
	}
	notes = append(notes, "Volume data, domains, deployment history, tokens and automations stay with the original. Scheduled tasks were copied paused.")
	httpx.JSON(w, 201, map[string]any{"project": proj, "resources": len(done), "deployed": deployed, "notes": notes})
	return nil
}

func enqueueDeploy(ctx context.Context, tx pgx.Tx, resourceID, depID, userID string) (string, error) {
	return jobs.Enqueue(ctx, tx, jobs.NewJob{Kind: jobs.KindDeploy, ResourceID: resourceID, DeploymentID: depID, CreatedBy: userID})
}

var exposeRE = regexp.MustCompile(`(?m)^EXPOSE\s+(\d+)`)

// exposedPort reads the port a generated Dockerfile exposes.
func exposedPort(dockerfile string) int {
	if m := exposeRE.FindStringSubmatch(dockerfile); m != nil {
		var n int
		fmt.Sscan(m[1], &n)
		return n
	}
	return 0
}

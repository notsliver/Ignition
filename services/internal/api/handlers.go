package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/functions"
	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/version"
	"github.com/notsliver/Ignition/services/internal/worker"
)

var (
	nameRE     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,48}[a-z0-9])?$`)
	envKeyRE   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	imageRE    = regexp.MustCompile(`^[a-z0-9]+([._/-][a-z0-9]+)*(:[0-9]+)?(/[a-z0-9]+([._-][a-z0-9]+)*)*(:[A-Za-z0-9._-]{1,128})?(@sha256:[a-f0-9]{64})?$`)
	dataPathRE = regexp.MustCompile(`^/[A-Za-z0-9._/-]*$`)
	uuidRE     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

func pathID(r *http.Request, what string) (string, error) {
	id := r.PathValue("id")
	if !uuidRE.MatchString(id) {
		return "", httpx.NotFound(what)
	}
	return id, nil
}

func notFound(err error, what string) error {
	if errors.Is(err, store.ErrNotFound) {
		return httpx.NotFound(what)
	}
	return err
}

/* -------------------------------- System --------------------------------- */

func (s *Server) system(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	dockerErr := s.Runtime.Ping(ctx)
	caps := map[string]any{
		"docker":       dockerErr == nil,
		"gitBuilds":    true,
		"builders":     []string{"dockerfile"},
		"domains":      true,
		"terminal":     true,
		"metrics":      "live", // live container stats only; no history is stored yet
		"publishHost":  s.PublishHost,
		"instanceName": s.instance(r.Context()).Name,
		"version":      version.Version,
	}
	if dockerErr != nil {
		caps["dockerError"] = dockerErr.Error()
	}
	httpx.JSON(w, 200, caps)
	return nil
}

/* -------------------------------- Projects ------------------------------- */

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request, u *store.User) error {
	list, err := s.Store.ListProjects(r.Context(), u.ID, strings.TrimSpace(r.URL.Query().Get("q")), r.URL.Query().Get("archived") == "true")
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"projects": list})
	return nil
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Project")
	if err != nil {
		return err
	}
	p, err := s.Store.GetProject(r.Context(), u.ID, id)
	if err != nil {
		return notFound(err, "Project")
	}
	httpx.JSON(w, 200, map[string]any{"project": p})
	return nil
}

type projectInput struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Archived    *bool   `json:"archived"`
}

func validateProject(in projectInput, creating bool) map[string]string {
	f := map[string]string{}
	if in.Name != nil || creating {
		name := ""
		if in.Name != nil {
			name = strings.TrimSpace(*in.Name)
		}
		if name == "" || len(name) > 60 {
			f["name"] = "Name is required (up to 60 characters)."
		}
	}
	if in.Description != nil && len(*in.Description) > 280 {
		f["description"] = "Keep the description under 280 characters."
	}
	return f
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request, u *store.User) error {
	var in projectInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if f := validateProject(in, true); len(f) > 0 {
		return httpx.Invalid(f)
	}
	desc := ""
	if in.Description != nil {
		desc = strings.TrimSpace(*in.Description)
	}
	id, err := s.Store.CreateProject(r.Context(), u.ID, strings.TrimSpace(*in.Name), desc)
	if store.IsUnique(err) {
		return httpx.Invalid(map[string]string{"name": "A project with this name already exists."})
	}
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "project.create", "project", id, map[string]any{"name": *in.Name}, clientIP(r))
	p, err := s.Store.GetProject(r.Context(), u.ID, id)
	if err != nil {
		return err
	}
	httpx.JSON(w, 201, map[string]any{"project": p})
	return nil
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Project")
	if err != nil {
		return err
	}
	var in projectInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if f := validateProject(in, false); len(f) > 0 {
		return httpx.Invalid(f)
	}
	if in.Name != nil {
		trimmed := strings.TrimSpace(*in.Name)
		in.Name = &trimmed
	}
	err = s.Store.UpdateProject(r.Context(), id, in.Name, in.Description, in.Archived)
	if store.IsUnique(err) {
		return httpx.Invalid(map[string]string{"name": "A project with this name already exists."})
	}
	if err != nil {
		return notFound(err, "Project")
	}
	action := "project.update"
	if in.Archived != nil {
		action = map[bool]string{true: "project.archive", false: "project.unarchive"}[*in.Archived]
	}
	s.Store.Audit(r.Context(), u, action, "project", id, nil, clientIP(r))
	p, err := s.Store.GetProject(r.Context(), u.ID, id)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"project": p})
	return nil
}

// deleteProject refuses while resources exist, so containers and volumes are never orphaned.
func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Project")
	if err != nil {
		return err
	}
	p, err := s.Store.GetProject(r.Context(), u.ID, id)
	if err != nil {
		return notFound(err, "Project")
	}
	if p.ResourceCount > 0 {
		return httpx.Conflict(fmt.Sprintf("Delete the %d resources in %s first. Their containers and data would otherwise be left running.", p.ResourceCount, p.Name))
	}
	if err := s.Store.DeleteProject(r.Context(), id); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "project.delete", "project", id, map[string]any{"name": p.Name}, clientIP(r))
	httpx.JSON(w, 204, nil)
	return nil
}

func (s *Server) favorite(on bool) func(http.ResponseWriter, *http.Request, *store.User) error {
	return func(w http.ResponseWriter, r *http.Request, u *store.User) error {
		id, err := pathID(r, "Project")
		if err != nil {
			return err
		}
		if _, err := s.Store.GetProject(r.Context(), u.ID, id); err != nil {
			return notFound(err, "Project")
		}
		if err := s.Store.SetFavorite(r.Context(), id, u.ID, on); err != nil {
			return err
		}
		httpx.JSON(w, 204, nil)
		return nil
	}
}

/* -------------------------------- Resources ------------------------------ */

func (s *Server) listResources(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Project")
	if err != nil {
		return err
	}
	q := r.URL.Query()
	list, err := s.Store.ListResources(r.Context(), id, q.Get("environment"), strings.TrimSpace(q.Get("q")), q.Get("type"))
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"resources": list})
	return nil
}

func (s *Server) listAllResources(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	q := r.URL.Query()
	list, err := s.Store.ListAllResources(r.Context(), strings.TrimSpace(q.Get("q")), q.Get("type"))
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"resources": list})
	return nil
}

func (s *Server) getResource(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	httpx.JSON(w, 200, map[string]any{"resource": res})
	return nil
}

type resourceInput struct {
	EnvironmentID string     `json:"environmentId"`
	Name          string     `json:"name"`
	Type          string     `json:"type"`
	SourceType    string     `json:"sourceType"`
	Image         string     `json:"image"`
	RepoURL       string     `json:"repoUrl"`
	Branch        string     `json:"branch"`
	Dockerfile    string     `json:"dockerfile"`
	Port          *int       `json:"port"`
	Command       string     `json:"command"`
	DataPath      string     `json:"dataPath"`
	MemoryMB      int        `json:"memoryMb"`
	CPUs          float64    `json:"cpus"`
	Env           []envInput `json:"env"`
	SourceID      string     `json:"sourceId"`
	Engine        string     `json:"engine"` // database engine, or a function's runtime
	Code          string     `json:"code"`   // a function's handler
	sourceSSH     bool       // set by the handler once the linked source is known
}

func (in *resourceInput) validate() map[string]string {
	f := map[string]string{}
	if !nameRE.MatchString(in.Name) {
		f["name"] = "Use lowercase letters, numbers and dashes (up to 50 characters)."
	}
	if in.Type == "" {
		in.Type = "application"
	}
	if in.Type == "function" {
		if in.Engine == "" {
			in.Engine = "node"
		}
		in.SourceType, in.Image = "image", "placeholder:function" // filled in by functions.Prepare
		return f
	}
	if in.Type != "application" && in.Type != "database" && in.Type != "worker" {
		f["type"] = "Unsupported resource type."
	}
	switch in.SourceType {
	case "image":
		if !imageRE.MatchString(in.Image) {
			f["image"] = "Enter an image reference like nginx:1.27 or ghcr.io/owner/app:tag."
		}
	case "git":
		if in.Branch == "" {
			in.Branch = "main"
		}
		if in.Dockerfile == "" {
			in.Dockerfile = "Dockerfile"
		}
		if err := worker.ValidateGitSourceFor(in.RepoURL, in.Branch, in.sourceSSH); err != nil {
			f["repository"] = strings.TrimPrefix(err.Error(), jobs.ErrPermanent.Error()+": ")
		}
		if _, err := worker.SafeDockerfile(in.Dockerfile); err != nil {
			f["dockerfile"] = "Dockerfile path must be inside the repository."
		}
	default:
		f["sourceType"] = "Choose an image or a Git repository."
	}
	if in.Port != nil && (*in.Port < 1 || *in.Port > 65535) {
		f["port"] = "Ports are numbers from 1 to 65535."
	}
	if in.MemoryMB == 0 {
		in.MemoryMB = 512
	}
	if in.MemoryMB < 64 || in.MemoryMB > 65536 {
		f["memoryMb"] = "Memory must be between 64 MB and 64 GB."
	}
	if in.CPUs == 0 {
		in.CPUs = 1
	}
	if in.CPUs <= 0 || in.CPUs > 64 {
		f["cpus"] = "CPUs must be between 0.1 and 64."
	}
	if in.DataPath != "" && !dataPathRE.MatchString(in.DataPath) {
		f["dataPath"] = "Data path must be an absolute path like /var/lib/postgresql/data."
	}
	if len(in.Command) > 2000 {
		f["command"] = "Command is too long."
	}
	for _, e := range in.Env {
		if !envKeyRE.MatchString(e.Key) {
			f["env"] = fmt.Sprintf("%q isn't a valid variable name.", e.Key)
		}
	}
	return f
}

// createResource stores the resource and queues its first deployment in one transaction.
func (s *Server) createResource(w http.ResponseWriter, r *http.Request, u *store.User) error {
	projectID, err := pathID(r, "Project")
	if err != nil {
		return err
	}
	project, err := s.Store.GetProject(r.Context(), u.ID, projectID)
	if err != nil {
		return notFound(err, "Project")
	}
	var in resourceInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	// Instance defaults fill in what the request leaves out.
	defs := s.defaults(r.Context())
	if in.MemoryMB == 0 {
		in.MemoryMB = defs.MemoryMB
	}
	if in.CPUs == 0 {
		in.CPUs = defs.CPUs
	}
	if err := s.checkResourceSource(r.Context(), &in); err != nil {
		return err
	}
	if f := in.validate(); len(f) > 0 {
		return httpx.Invalid(f)
	}
	envID := in.EnvironmentID
	if envID == "" && len(project.Environments) > 0 {
		envID = project.Environments[0].ID
	}
	valid := false
	for _, e := range project.Environments {
		valid = valid || e.ID == envID
	}
	if !valid {
		return httpx.Invalid(map[string]string{"environmentId": "Unknown environment."})
	}

	tx, err := s.Store.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	x := provisionExtras{Engine: in.Engine}
	if in.Type == "database" && x.Engine == "" {
		x.Engine = engineOf(in.Image)
	}
	if in.Type == "function" {
		fn, err := functions.Prepare(in.Engine, in.Code)
		if err != nil {
			return httpx.Invalid(map[string]string{"code": err.Error()})
		}
		in.Image, in.Command, in.Port = fn.Image, fn.Command, &fn.Port
		for _, v := range fn.Vars {
			in.Env = append(in.Env, envInput{Key: v[0], Value: v[1]})
		}
		x.Engine = in.Engine
	}
	id, depID, err := s.provision(r.Context(), tx, u, projectID, envID, in, x)
	if store.IsUnique(err) {
		return httpx.Invalid(map[string]string{"name": "A resource with this name already exists in this environment."})
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "resource.create", "resource", id, map[string]any{"name": in.Name, "project": project.Name, "source": in.SourceType}, clientIP(r))
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.JSON(w, 201, map[string]any{"resource": res, "deploymentId": depID})
	return nil
}

// checkResourceSource verifies a linked source exists and matches how the resource is built.
func (s *Server) checkResourceSource(ctx context.Context, in *resourceInput) error {
	if in.SourceID == "" {
		return nil
	}
	if !uuidRE.MatchString(in.SourceID) {
		return httpx.Invalid(map[string]string{"sourceId": "Unknown source."})
	}
	src, err := s.Store.GetSource(ctx, in.SourceID)
	if err != nil {
		return httpx.Invalid(map[string]string{"sourceId": "Unknown source."})
	}
	wantKind := map[string]string{"git": "git", "image": "registry"}[in.SourceType]
	if src.Kind != wantKind {
		return httpx.Invalid(map[string]string{"sourceId": fmt.Sprintf("%s is a %s source; this resource needs a %s source.", src.Name, src.Kind, wantKind)})
	}
	in.sourceSSH = src.AuthType == "ssh"
	return nil
}

func (s *Server) updateResource(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	current, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	var p store.ResourcePatch
	if err := httpx.Decode(r, &p); err != nil {
		return err
	}
	// Re-validate the merged result so partial updates can't produce an invalid resource.
	merged := resourceInput{Name: current.Name, Type: current.Type, SourceType: current.SourceType, Image: current.Image, RepoURL: current.RepoURL,
		Branch: current.Branch, Dockerfile: current.Dockerfile, Port: current.Port, Command: current.Command, DataPath: current.DataPath, MemoryMB: current.MemoryMB, CPUs: current.CPUs}
	for dst, src := range map[*string]*string{&merged.Image: p.Image, &merged.RepoURL: p.RepoURL, &merged.Branch: p.Branch, &merged.Dockerfile: p.Dockerfile, &merged.Command: p.Command} {
		if src != nil {
			*dst = strings.TrimSpace(*src)
		}
	}
	if p.Port != nil {
		merged.Port = p.Port
	}
	if p.MemoryMB != nil {
		merged.MemoryMB = *p.MemoryMB
	}
	if p.CPUs != nil {
		merged.CPUs = *p.CPUs
	}
	if current.SourceID != nil {
		merged.SourceID = *current.SourceID
	}
	if p.SourceID != nil {
		merged.SourceID = *p.SourceID
	}
	if err := s.checkResourceSource(r.Context(), &merged); err != nil {
		return err
	}
	if f := merged.validate(); len(f) > 0 {
		return httpx.Invalid(f)
	}
	if err := s.Store.UpdateResource(r.Context(), id, p); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "resource.update", "resource", id, nil, clientIP(r))
	res, _ := s.Store.GetResource(r.Context(), id)
	httpx.JSON(w, 200, map[string]any{"resource": res, "requiresRedeploy": true})
	return nil
}

func (s *Server) enqueueForResource(ctx context.Context, kind, resourceID, userID string, payload any) (string, error) {
	var active int
	s.Store.DB.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE resource_id = $1 AND kind <> 'health' AND status IN ('pending', 'running')`, resourceID).Scan(&active)
	if active > 0 && kind != jobs.KindDeploy {
		return "", httpx.Conflict("Another operation is in progress for this resource. Wait for it to finish.")
	}
	return jobs.Enqueue(ctx, s.Store.DB, jobs.NewJob{Kind: kind, ResourceID: resourceID, CreatedBy: userID, Payload: payload, MaxAttempts: 2})
}

func (s *Server) resourceAction(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	var in struct {
		Action string `json:"action"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	switch in.Action {
	case "start", "stop", "restart", "pause", "resume", "refresh":
	default:
		return httpx.BadRequest("Action must be start, stop, restart, pause, resume or refresh.")
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	if res.SourceType == "external" && in.Action != "refresh" {
		return httpx.Conflict(res.Name + " is an external database; start and stop it where it runs.")
	}
	if res.ContainerID == "" {
		return httpx.Conflict(res.Name + " isn't deployed yet. Deploy it first.")
	}
	if in.Action == "pause" && res.Status != "running" {
		return httpx.Conflict("Only a running resource can be paused.")
	}
	if in.Action == "resume" && res.Status != "paused" {
		return httpx.Conflict(res.Name + " isn't paused.")
	}
	jobID, err := s.enqueueForResource(r.Context(), jobs.KindAction, id, u.ID, map[string]string{"action": in.Action})
	if err != nil {
		return err
	}
	switch in.Action {
	case "stop":
		s.Store.SetDesiredState(r.Context(), id, "stopped")
	case "start", "restart", "resume":
		s.Store.SetDesiredState(r.Context(), id, "running")
	}
	s.Store.Audit(r.Context(), u, "resource."+in.Action, "resource", id, map[string]any{"name": res.Name}, clientIP(r))
	httpx.JSON(w, 202, map[string]string{"jobId": jobID})
	return nil
}

func (s *Server) deleteResource(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	if r.URL.Query().Get("confirm") != res.Name {
		return httpx.BadRequest("Confirm by passing the resource name.")
	}
	// Cancel anything queued for it, then queue the removal.
	s.Store.DB.Exec(r.Context(), `UPDATE jobs SET cancel_requested = true, status = CASE WHEN status = 'pending' THEN 'cancelled' ELSE status END WHERE resource_id = $1 AND status IN ('pending', 'running')`, id)
	s.Store.SetStatus(r.Context(), id, "removing")
	jobID, err := jobs.Enqueue(r.Context(), s.Store.DB, jobs.NewJob{Kind: jobs.KindRemove, ResourceID: id, CreatedBy: u.ID, Payload: map[string]bool{"removeVolumes": r.URL.Query().Get("volumes") == "true"}})
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "resource.delete", "resource", id, map[string]any{"name": res.Name}, clientIP(r))
	httpx.JSON(w, 202, map[string]string{"jobId": jobID})
	return nil
}

/* --------------------------- Environment variables ------------------------- */

type envInput struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Secret    bool   `json:"secret"`
	Unchanged bool   `json:"unchanged"` // keep the stored value (secrets aren't sent to the browser)
}

func (s *Server) getEnv(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	vars, err := s.Store.ListEnv(r.Context(), id)
	if err != nil {
		return err
	}
	for i := range vars {
		if !vars[i].Secret {
			vars[i].Value, err = s.Secrets.Open(vars[i].Sealed, id+":"+vars[i].Key)
			if err != nil {
				return err
			}
		}
	}
	httpx.JSON(w, 200, map[string]any{"variables": vars})
	return nil
}

func (s *Server) putEnv(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	var in struct {
		Variables []envInput `json:"variables"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	existing, err := s.Store.ListEnv(r.Context(), id)
	if err != nil {
		return err
	}
	byKey := map[string]store.EnvVar{}
	for _, v := range existing {
		byKey[v.Key] = v
	}
	seen := map[string]bool{}
	out := make([]store.EnvVar, 0, len(in.Variables))
	for _, v := range in.Variables {
		if !envKeyRE.MatchString(v.Key) {
			return httpx.Invalid(map[string]string{"variables": fmt.Sprintf("%q isn't a valid name.", v.Key)})
		}
		if seen[v.Key] {
			return httpx.Invalid(map[string]string{"variables": v.Key + " is defined twice."})
		}
		seen[v.Key] = true
		if len(v.Value) > 32<<10 {
			return httpx.Invalid(map[string]string{"variables": v.Key + " is longer than 32 KB."})
		}
		if v.Unchanged {
			old, ok := byKey[v.Key]
			if !ok {
				return httpx.Invalid(map[string]string{"variables": v.Key + " has no stored value to keep."})
			}
			out = append(out, store.EnvVar{Key: v.Key, Sealed: old.Sealed, Secret: v.Secret})
			continue
		}
		sealed, err := s.Secrets.Seal(v.Value, id+":"+v.Key)
		if err != nil {
			return err
		}
		out = append(out, store.EnvVar{Key: v.Key, Sealed: sealed, Secret: v.Secret})
	}
	if err := s.Store.ReplaceEnv(r.Context(), id, u.ID, out); err != nil {
		return err
	}
	keys := make([]string, 0, len(out))
	for _, v := range out {
		keys = append(keys, v.Key)
	}
	s.Store.Audit(r.Context(), u, "resource.env.update", "resource", id, map[string]any{"keys": keys}, clientIP(r)) // names only, never values
	httpx.JSON(w, 200, map[string]any{"requiresRedeploy": true})
	return nil
}

func (s *Server) revealEnv(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	key := r.PathValue("key")
	vars, err := s.Store.ListEnv(r.Context(), id)
	if err != nil {
		return err
	}
	for _, v := range vars {
		if v.Key == key {
			plain, err := s.Secrets.Open(v.Sealed, id+":"+v.Key)
			if err != nil {
				return err
			}
			s.Store.Audit(r.Context(), u, "resource.env.reveal", "resource", id, map[string]any{"key": key}, clientIP(r))
			httpx.JSON(w, 200, map[string]string{"value": plain})
			return nil
		}
	}
	return httpx.NotFound("Variable")
}

/* -------------------------------- Deployments ------------------------------ */

func (s *Server) listDeployments(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := s.Store.ListDeployments(r.Context(), id, limit)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"deployments": list})
	return nil
}

func (s *Server) getDeployment(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Deployment")
	if err != nil {
		return err
	}
	d, err := s.Store.GetDeployment(r.Context(), id)
	if err != nil {
		return notFound(err, "Deployment")
	}
	httpx.JSON(w, 200, map[string]any{"deployment": d})
	return nil
}

// createDeployment deploys the latest source, or redeploys/rolls back to a previous deployment's artifact.
func (s *Server) createDeployment(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	var in struct {
		From       string `json:"from"`
		Reason     string `json:"reason"`
		NoCache    bool   `json:"noCache"`    // rebuild every layer and pull fresh base images
		RestoreEnv bool   `json:"restoreEnv"` // rollback: also bring back that release's variables
	}
	if err := httpx.DecodeOptional(r, &in); err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	if res.SourceType == "external" {
		return httpx.Conflict(res.Name + " is an external database: IGNITION connects to it but doesn't run it, so there's nothing to deploy.")
	}
	trigger, imageRef, branch := "manual", "", res.Branch
	if strings.HasPrefix(u.Email, tokenActorPrefix) {
		trigger = "token"
	}
	var from *store.Deployment
	if in.From != "" {
		if from, err = s.Store.GetDeployment(r.Context(), in.From); err != nil || from.ResourceID != id {
			return httpx.NotFound("Deployment")
		}
		if from.ImageRef == "" {
			return httpx.Conflict("That deployment never produced an image, so it can't be redeployed.")
		}
		trigger, imageRef, branch = "redeploy", from.ImageRef, from.Branch
		if from.Status == "successful" && !from.Current {
			// A rollback runs exactly that release's image.
			img, why := rollbackImage(res, from)
			if img == "" {
				return httpx.Conflict(why)
			}
			if iid, err := s.Runtime.ImageID(r.Context(), img); err == nil && iid == "" {
				return httpx.Conflict(fmt.Sprintf("#%d's image is no longer on this machine (old images were removed). Deploy that commit from the source instead.", from.Number))
			}
			trigger, imageRef = "rollback", img
		} else if res.SourceType == "image" {
			imageRef = "" // image resources redeploy by pulling the configured tag again
		}
		if in.RestoreEnv && !from.EnvKept {
			return httpx.Conflict("That release's variables weren't kept (it's older than this feature), so they can't be restored.")
		}
	} else if in.RestoreEnv {
		return httpx.BadRequest("Restoring variables needs a release to restore them from.")
	}
	tx, err := s.Store.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	depID, err := s.Store.CreateDeployment(r.Context(), tx, id, trigger, imageRef, branch, strings.TrimSpace(in.Reason), u.ID)
	if err != nil {
		return err
	}
	if in.NoCache && imageRef == "" {
		if _, err := tx.Exec(r.Context(), `UPDATE deployments SET no_cache = true WHERE id = $1`, depID); err != nil {
			return err
		}
	}
	if in.RestoreEnv {
		if err := s.Store.RestoreEnv(r.Context(), tx, id, from.ID, u.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE deployments SET restore_env = true WHERE id = $1`, depID); err != nil {
			return err
		}
	}
	jobID, err := jobs.Enqueue(r.Context(), tx, jobs.NewJob{Kind: jobs.KindDeploy, ResourceID: id, DeploymentID: depID, CreatedBy: u.ID})
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "deployment."+trigger, "resource", id, map[string]any{"deployment": depID, "reason": in.Reason, "restoreEnv": in.RestoreEnv}, clientIP(r))
	httpx.JSON(w, 202, map[string]string{"deploymentId": depID, "jobId": jobID})
	return nil
}

func (s *Server) cancelDeployment(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Deployment")
	if err != nil {
		return err
	}
	d, err := s.Store.GetDeployment(r.Context(), id)
	if err != nil {
		return notFound(err, "Deployment")
	}
	if d.JobID == nil {
		return httpx.Conflict("This deployment has no job to cancel.")
	}
	status, err := jobs.RequestCancel(r.Context(), s.Store.DB, *d.JobID)
	if err != nil {
		return err
	}
	if status == "" {
		return httpx.Conflict("This deployment already finished.")
	}
	if status == "cancelled" {
		s.Store.SetDeploymentStatus(r.Context(), id, "cancelled", "")
	}
	s.Store.Audit(r.Context(), u, "deployment.cancel", "resource", d.ResourceID, map[string]any{"deployment": id}, clientIP(r))
	httpx.JSON(w, 202, map[string]string{"status": status})
	return nil
}

func (s *Server) deploymentLogs(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Deployment")
	if err != nil {
		return err
	}
	d, err := s.Store.GetDeployment(r.Context(), id)
	if err != nil {
		return notFound(err, "Deployment")
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	logs := []store.JobLog{}
	if d.JobID != nil {
		if logs, err = s.Store.JobLogs(r.Context(), *d.JobID, after); err != nil {
			return err
		}
	}
	httpx.JSON(w, 200, map[string]any{"logs": logs, "status": d.Status})
	return nil
}

/* ------------------------------ Runtime queries ---------------------------- */

// containerLogs returns runtime output: the running container's from Docker, or an earlier deployment's
// from the archive kept when its container was replaced (?deployment=<id>).
func (s *Server) containerLogs(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	if tail <= 0 || tail > 10000 {
		tail = 1000
	}
	var since time.Time
	if v, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64); err == nil && v > 0 {
		since = time.Unix(v, 0)
	}
	const retention = "Logs of the running release come from Docker. When a release is replaced, its last 10,000 lines are kept for the 10 most recent deployments."
	dep := r.URL.Query().Get("deployment")
	if dep != "" && (res.CurrentDeploymentID == nil || dep != *res.CurrentDeploymentID) {
		if !uuidRE.MatchString(dep) {
			return httpx.Invalid(map[string]string{"deployment": "Unknown deployment."})
		}
		d, err := s.Store.GetDeployment(r.Context(), dep)
		if err != nil || d.ResourceID != id {
			return httpx.NotFound("Deployment")
		}
		lines, err := s.Store.ArchivedRuntimeLogs(r.Context(), dep, since, tail)
		if err != nil {
			return err
		}
		reason := ""
		if len(lines) == 0 {
			reason = fmt.Sprintf("No logs were kept for deployment #%d. It produced no output, or its archive was pruned.", d.Number)
		}
		httpx.JSON(w, 200, map[string]any{"lines": lines, "available": len(lines) > 0, "reason": reason, "archived": true, "retention": retention})
		return nil
	}
	if res.ContainerID == "" {
		httpx.JSON(w, 200, map[string]any{"lines": []any{}, "available": false, "reason": "This resource has no running container yet. Earlier deployments' logs are in the deployment picker."})
		return nil
	}
	lines, err := s.Runtime.Logs(r.Context(), res.ContainerID, tail, since)
	if err != nil {
		httpx.JSON(w, 200, map[string]any{"lines": []any{}, "available": false, "reason": "Docker didn't return logs: " + err.Error()})
		return nil
	}
	httpx.JSON(w, 200, map[string]any{"lines": lines, "available": true, "archived": false, "retention": retention})
	return nil
}

// logSources lists the deployments whose runtime logs can be read.
func (s *Server) logSources(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	if _, err := s.Store.GetResource(r.Context(), id); err != nil {
		return notFound(err, "Resource")
	}
	list, err := s.Store.LogSources(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"deployments": list})
	return nil
}

func (s *Server) containerStats(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	if res.ContainerID == "" || res.Status != "running" {
		httpx.JSON(w, 200, map[string]any{"available": false, "reason": "Stats are only collected while the resource is running."})
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	st, err := s.Runtime.Stats(ctx, res.ContainerID)
	if err != nil {
		httpx.JSON(w, 200, map[string]any{"available": false, "reason": "Docker didn't return stats: " + err.Error()})
		return nil
	}
	httpx.JSON(w, 200, map[string]any{"available": true, "stats": st})
	return nil
}

// metricRanges maps a range to its window and bucket size (~60–170 points per chart).
var metricRanges = map[string][2]time.Duration{
	"15m": {15 * time.Minute, 15 * time.Second},
	"1h":  {time.Hour, time.Minute},
	"6h":  {6 * time.Hour, 5 * time.Minute},
	"24h": {24 * time.Hour, 15 * time.Minute},
	"7d":  {7 * 24 * time.Hour, time.Hour},
}

func (s *Server) resourceMetrics(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	rng := r.URL.Query().Get("range")
	spec, ok := metricRanges[rng]
	if !ok {
		rng, spec = "1h", metricRanges["1h"]
	}
	buckets, err := s.Store.MetricSeries(r.Context(), id, spec[0], spec[1])
	if err != nil {
		return err
	}
	last, err := s.Store.LastMetricAt(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{
		"range": rng, "stepSeconds": int(spec[1].Seconds()), "buckets": buckets, "lastSampleAt": last,
		"sampleIntervalSeconds": 15, "retentionDays": 7,
		"limits": map[string]any{"memoryBytes": int64(res.MemoryMB) << 20, "cpus": res.CPUs},
	})
	return nil
}

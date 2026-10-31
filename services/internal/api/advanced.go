package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/worker"
)

/*
 * Advanced settings: container options applied on the next deploy, the running container's drift from
 * what's saved, and rare operations (rename, move, purge old images, export).
 */

var (
	labelKeyRE = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._/-]{0,126}[a-zA-Z0-9])?$`)
	userRE     = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,31}(:[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,31})?$`)
)

type advancedInput struct {
	Name string `json:"name"`
	store.Advanced
}

func (in *advancedInput) validate() map[string]string {
	f := map[string]string{}
	in.Name = strings.TrimSpace(in.Name)
	in.Entrypoint, in.Workdir, in.User = strings.TrimSpace(in.Entrypoint), strings.TrimSpace(in.Workdir), strings.TrimSpace(in.User)
	if !nameRE.MatchString(in.Name) {
		f["name"] = "Use lowercase letters, numbers and dashes (up to 50 characters)."
	}
	if _, err := worker.SplitArgs(in.Entrypoint); err != nil {
		f["entrypoint"] = "The entrypoint " + err.Error() + "."
	} else if len(in.Entrypoint) > 2000 {
		f["entrypoint"] = "That entrypoint is too long."
	}
	if in.Workdir != "" && (!strings.HasPrefix(in.Workdir, "/") || strings.ContainsAny(in.Workdir, "\n\t") || len(in.Workdir) > 300) {
		f["workdir"] = "Use an absolute path like /app."
	}
	if in.User != "" && !userRE.MatchString(in.User) {
		f["user"] = "Use a user name or ID, optionally with a group: 1000 or 1000:1000 or node."
	}
	if in.Labels == nil {
		in.Labels = map[string]string{}
	}
	if len(in.Labels) > 50 {
		f["labels"] = "Up to 50 labels."
	}
	for k, v := range in.Labels {
		switch {
		case !labelKeyRE.MatchString(k):
			f["labels"] = fmt.Sprintf("%q isn't a valid label name.", k)
		case strings.HasPrefix(k, "ignition.") || strings.HasPrefix(k, "com.docker."):
			f["labels"] = fmt.Sprintf("Labels starting with %s are reserved.", strings.SplitN(k, ".", 2)[0]+".")
		case len(v) > 1000:
			f["labels"] = fmt.Sprintf("The value of %s is too long.", k)
		}
	}
	return f
}

type driftItem struct {
	Field   string `json:"field"`
	Saved   string `json:"saved"`
	Running string `json:"running"`
}

func listOrDefault(v []string) string {
	if len(v) == 0 {
		return "(image default)"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func orDefault(v string) string {
	if v == "" {
		return "(image default)"
	}
	return v
}

func onOff(b bool) string {
	if b {
		return "On"
	}
	return "Off"
}

var dockerRestart = map[string]string{"always": "unless-stopped", "on-failure": "on-failure", "never": "no"}

// drift compares what's saved with the running container. Changes that apply on the next deploy show up here.
func (s *Server) drift(r *http.Request, res *store.Resource, adv store.Advanced, st runtime.State) ([]driftItem, []string) {
	ctx := r.Context()
	var out []driftItem
	add := func(field, saved, running string) {
		if saved != running {
			out = append(out, driftItem{field, saved, running})
		}
	}
	if entry, err := worker.SplitArgs(adv.Entrypoint); err == nil && len(entry) > 0 {
		add("Entrypoint", listOrDefault(entry), listOrDefault(st.Config.Entrypoint))
	}
	if res.Command != "" {
		add("Command", listOrDefault([]string{"sh", "-c", res.Command}), listOrDefault(st.Config.Cmd))
	}
	if adv.Workdir != "" {
		add("Working directory", adv.Workdir, orDefault(st.Config.WorkingDir))
	}
	if adv.User != "" {
		add("User", adv.User, orDefault(st.Config.User))
	}
	keys := make([]string, 0, len(adv.Labels))
	for k := range adv.Labels {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		running, ok := st.Config.Labels[k]
		if !ok {
			running = "(missing)"
		}
		add("Label "+k, adv.Labels[k], running)
	}
	// Only labels Ignition set (listed in ignition.labels) can be "removed"; others come from the image.
	for _, k := range strings.Split(st.Config.Labels["ignition.labels"], ",") {
		if _, saved := adv.Labels[k]; k != "" && !saved {
			add("Label "+k, "(removed)", st.Config.Labels[k])
		}
	}
	add("Read-only root filesystem", onOff(adv.ReadOnlyRoot), onOff(st.Config.ReadOnlyRoot))
	add("Init process", onOff(adv.Init), onOff(st.Config.Init))
	add("Privileged", onOff(adv.Privileged), onOff(st.Config.Privileged))
	if lim, err := s.Store.GetLimits(ctx, res.ID); err == nil {
		add("Memory limit", fmt.Sprintf("%d MB", lim.MemoryMB), fmt.Sprintf("%d MB", st.Config.MemoryBytes>>20))
		add("CPU limit", fmt.Sprintf("%g vCPU", lim.CPUs), fmt.Sprintf("%g vCPU", float64(st.Config.NanoCPUs)/1e9))
		add("Restart policy", dockerRestart[lim.RestartPolicy], st.Config.Restart)
	}
	if res.CurrentDeploymentID != nil {
		if d, err := s.Store.GetDeployment(ctx, *res.CurrentDeploymentID); err == nil && d.ImageID != "" && st.ImageID != "" {
			add("Image", shortID(d.ImageID), shortID(st.ImageID))
		}
	}

	// Variables changed since the running release started (names only).
	var envChanged []string
	if res.CurrentDeploymentID != nil {
		if d, err := s.Store.GetDeployment(ctx, *res.CurrentDeploymentID); err == nil && d.EnvKept {
			then, _ := s.Store.DeploymentEnv(ctx, d.ID)
			now, _ := s.Store.ListEnv(ctx, res.ID)
			open := func(v store.EnvVar) string { p, _ := s.Secrets.Open(v.Sealed, res.ID+":"+v.Key); return p }
			old := map[string]string{}
			for _, v := range then {
				old[v.Key] = open(v)
			}
			for _, v := range now {
				if prev, ok := old[v.Key]; !ok || prev != open(v) {
					envChanged = append(envChanged, v.Key)
				}
				delete(old, v.Key)
			}
			for k := range old {
				envChanged = append(envChanged, k)
			}
			slices.Sort(envChanged)
		}
	}
	return out, envChanged
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func (s *Server) getAdvanced(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	adv, err := s.Store.GetAdvanced(r.Context(), id)
	if err != nil {
		return err
	}
	out := map[string]any{"name": res.Name, "advanced": adv, "status": res.Status, "sourceType": res.SourceType}
	ids := map[string]any{"resourceId": res.ID, "environmentId": res.EnvironmentID, "projectId": res.ProjectID, "network": runtime.EnvNetwork(res.EnvironmentID),
		"createdAt": res.CreatedAt, "updatedAt": res.UpdatedAt}
	if res.ContainerID != "" {
		st, err := s.Runtime.Inspect(r.Context(), res.ContainerID)
		if err == nil && st.Exists {
			ids["containerId"], ids["image"], ids["imageId"] = res.ContainerID, st.Image, st.ImageID
			drift, envChanged := s.drift(r, res, adv, st)
			if drift == nil {
				drift = []driftItem{}
			}
			out["drift"], out["envChanged"] = drift, envChanged
		}
	}
	out["identifiers"] = ids
	if res.SourceType == "git" {
		if imgs, err := s.Runtime.Images(r.Context(), "ignition/"+res.ID[:8], "ignition.resource="+res.ID); err == nil {
			var size int64
			for _, i := range imgs {
				size += i.Size
			}
			out["images"] = map[string]any{"count": len(imgs), "bytes": size}
		}
	}
	// Where it could move to: every environment except its own.
	rows, err := s.Store.DB.Query(r.Context(), `
		SELECT p.id, p.name, e.id, e.name FROM environments e JOIN projects p ON p.id = e.project_id
		WHERE p.archived_at IS NULL AND e.id <> $1 ORDER BY lower(p.name), e.position, e.name`, res.EnvironmentID)
	if err == nil {
		type target struct {
			ProjectID       string `json:"projectId"`
			ProjectName     string `json:"projectName"`
			EnvironmentID   string `json:"environmentId"`
			EnvironmentName string `json:"environmentName"`
		}
		targets := []target{}
		for rows.Next() {
			var t target
			if rows.Scan(&t.ProjectID, &t.ProjectName, &t.EnvironmentID, &t.EnvironmentName) == nil {
				targets = append(targets, t)
			}
		}
		rows.Close()
		out["moveTargets"] = targets
	}
	httpx.JSON(w, 200, out)
	return nil
}

func (s *Server) putAdvanced(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	current, err := s.Store.GetAdvanced(r.Context(), id)
	if err != nil {
		return err
	}
	var in advancedInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if f := in.validate(); len(f) > 0 {
		return httpx.Invalid(f)
	}
	if in.Privileged != current.Privileged && !can(u, "resources.privileged") {
		return httpx.Forbidden("Changing privileged mode needs the resources.privileged permission (admins by default): it gives the container control over this machine.")
	}
	err = s.Store.PutAdvanced(r.Context(), id, in.Name, in.Advanced)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return httpx.Invalid(map[string]string{"name": "Another resource in this environment already has that name."})
	}
	if err != nil {
		return err
	}
	detail := map[string]any{"privileged": in.Privileged, "readOnlyRoot": in.ReadOnlyRoot}
	if in.Name != res.Name {
		detail["renamedFrom"] = res.Name
	}
	s.Store.Audit(r.Context(), u, "resource.advanced", "resource", id, detail, clientIP(r))
	adv, _ := s.Store.GetAdvanced(r.Context(), id)
	updated, _ := s.Store.GetResource(r.Context(), id)
	httpx.JSON(w, 200, map[string]any{"advanced": adv, "resource": updated, "requiresRedeploy": res.ContainerID != ""})
	return nil
}

// exportResource downloads the resource's configuration (variable names only, never values).
func (s *Server) exportResource(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	ctx := r.Context()
	res, err := s.Store.GetResource(ctx, id)
	if err != nil {
		return notFound(err, "Resource")
	}
	adv, _ := s.Store.GetAdvanced(ctx, id)
	build, _ := s.Store.GetBuildSettings(ctx, id)
	limits, _ := s.Store.GetLimits(ctx, id)
	netcfg, _ := s.Store.GetNetworking(ctx, id)
	vols, _ := s.Store.Volumes(ctx, id)
	health, _, _ := s.Store.GetHealth(ctx, id)
	tasks, _ := s.Store.ListTasks(ctx, id)
	env, _ := s.Store.ListEnv(ctx, id)
	vars := []map[string]any{}
	for _, v := range env {
		vars = append(vars, map[string]any{"key": v.Key, "secret": v.Secret})
	}
	taskList := []map[string]any{}
	for _, t := range tasks {
		taskList = append(taskList, map[string]any{"name": t.Name, "schedule": t.Schedule, "timezone": t.Timezone, "command": t.Command, "timeoutSeconds": t.TimeoutSeconds, "enabled": t.Enabled})
	}
	volumes := []map[string]string{}
	for _, v := range vols {
		volumes = append(volumes, map[string]string{"name": v.Name, "mountPath": v.MountPath})
	}
	doc := map[string]any{
		"ignitionExport": 1,
		"resource": map[string]any{"name": res.Name, "type": res.Type, "environment": res.Environment, "sourceType": res.SourceType, "image": res.Image,
			"repoUrl": res.RepoURL, "branch": res.Branch, "port": res.Port, "command": res.Command},
		"build": build, "limits": limits, "networking": netcfg, "volumes": volumes, "healthCheck": health, "scheduledTasks": taskList,
		"runtime":   adv,
		"variables": vars,
		"note":      "Variable values aren't exported. Secrets stay on the server.",
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.ignition.json"`, res.Name))
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// purgeImages removes this resource's built images except the running one. Rollbacks to purged
// releases need a fresh deploy from source.
func (s *Server) purgeImages(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	if res.SourceType != "git" {
		return httpx.Conflict("Only resources built from Git have their own images. Images pulled from a registry are shared.")
	}
	var in struct {
		Keep int `json:"keep"` // newest releases to keep besides the running one
	}
	if err := httpx.DecodeOptional(r, &in); err != nil {
		return err
	}
	if in.Keep < 0 || in.Keep > 50 {
		return httpx.Invalid(map[string]string{"keep": "Keep 0 to 50 releases."})
	}
	imgs, err := s.Runtime.Images(r.Context(), "ignition/"+res.ID[:8], "ignition.resource="+res.ID)
	if err != nil {
		return err
	}
	running := ""
	if res.ContainerID != "" {
		if st, err := s.Runtime.Inspect(r.Context(), res.ContainerID); err == nil {
			running = st.ImageID
		}
	}
	var removed int
	var freed int64
	var failed []string
	kept := 0
	for _, img := range imgs { // newest first
		if img.ID == running {
			continue
		}
		if kept < in.Keep {
			kept++
			continue
		}
		if err := s.Runtime.RemoveImage(r.Context(), img.ID); err != nil {
			failed = append(failed, shortID(img.ID)+": "+err.Error())
			continue
		}
		removed++
		freed += img.Size
	}
	s.Store.Audit(r.Context(), u, "resource.purge_images", "resource", id, map[string]any{"removed": removed, "bytes": freed}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"removed": removed, "freedBytes": freed, "failed": failed})
	return nil
}

// moveResource puts the resource in another environment (possibly another project) and redeploys it
// there, so it joins that environment's network. Domains, variables, volumes and history go with it.
func (s *Server) moveResource(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Resource")
	if err != nil {
		return err
	}
	res, err := s.Store.GetResource(r.Context(), id)
	if err != nil {
		return notFound(err, "Resource")
	}
	var in struct {
		EnvironmentID string `json:"environmentId"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var projectID, projectName, envName string
	if err := s.Store.DB.QueryRow(r.Context(), `SELECT p.id, p.name, e.name FROM environments e JOIN projects p ON p.id = e.project_id WHERE e.id = $1`, in.EnvironmentID).
		Scan(&projectID, &projectName, &envName); err != nil {
		return httpx.Invalid(map[string]string{"environmentId": "Choose an environment to move to."})
	}
	if in.EnvironmentID == res.EnvironmentID {
		return httpx.Invalid(map[string]string{"environmentId": "It's already there."})
	}
	var busy int
	s.Store.DB.QueryRow(r.Context(), `SELECT count(*) FROM jobs WHERE resource_id = $1 AND kind <> 'health' AND status IN ('pending', 'running')`, id).Scan(&busy)
	if busy > 0 {
		return httpx.Conflict("Wait for the current operation on " + res.Name + " to finish, then move it.")
	}
	err = s.Store.MoveResource(r.Context(), id, projectID, in.EnvironmentID)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return httpx.Invalid(map[string]string{"environmentId": fmt.Sprintf("%s / %s already has a resource called %s. Rename one first.", projectName, envName, res.Name)})
	}
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "resource.move", "resource", id, map[string]any{"from": res.EnvironmentID, "to": in.EnvironmentID, "project": projectName, "environment": envName}, clientIP(r))
	out := map[string]any{"projectId": projectID, "projectName": projectName, "environmentName": envName}
	if res.ContainerID != "" {
		// Redeploy the same release so the container moves to the new environment's network.
		tx, err := s.Store.DB.Begin(r.Context())
		if err != nil {
			return err
		}
		defer tx.Rollback(r.Context())
		imageRef := ""
		if res.CurrentDeploymentID != nil {
			if d, err := s.Store.GetDeployment(r.Context(), *res.CurrentDeploymentID); err == nil && res.SourceType == "git" {
				imageRef = d.ImageRef
			}
		}
		depID, err := s.Store.CreateDeployment(r.Context(), tx, id, "config", imageRef, res.Branch, fmt.Sprintf("Moved to %s / %s", projectName, envName), u.ID)
		if err != nil {
			return err
		}
		if _, err := jobs.Enqueue(r.Context(), tx, jobs.NewJob{Kind: jobs.KindDeploy, ResourceID: id, DeploymentID: depID, CreatedBy: u.ID}); err != nil {
			return err
		}
		if err := tx.Commit(r.Context()); err != nil {
			return err
		}
		out["deploymentId"] = depID
	}
	updated, _ := s.Store.GetResource(r.Context(), id)
	out["resource"] = updated
	httpx.JSON(w, 200, out)
	return nil
}

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/cron"
	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/servers"
	"github.com/notsliver/Ignition/services/internal/store"
	"github.com/notsliver/Ignition/services/internal/worker"
)

/* --------------------------------- Diagnostics --------------------------------- */

func (s *Server) enqueueDiagnostics(ctx context.Context, serverID string, u *store.User, checks []string) (string, string, error) {
	tx, err := s.Store.DB.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx)
	runID, jobID, err := servers.EnqueueDiagnostics(ctx, s.Store, tx, serverID, u.ID, actorName(u), checks)
	if err != nil {
		return "", "", err
	}
	return runID, jobID, tx.Commit(ctx)
}

func (s *Server) getServerDiagnostics(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	latest, err := s.Store.LatestDiagnostics(r.Context(), srv.ID)
	if err != nil {
		return err
	}
	runs, _ := s.Store.ListDiagnosticRuns(r.Context(), srv.ID, 10)
	active, _ := s.Store.ActiveDiagnosticRun(r.Context(), srv.ID)
	httpx.JSON(w, 200, map[string]any{"checks": servers.Checks, "latest": latest, "runs": runs, "active": active, "server": viewServer(srv)})
	return nil
}

func (s *Server) runServerDiagnostics(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	var in struct {
		Checks []string `json:"checks"`
	}
	if err := httpx.DecodeOptional(r, &in); err != nil {
		return err
	}
	if len(in.Checks) == 0 {
		in.Checks = servers.CheckIDs()
	}
	for _, c := range in.Checks {
		if !servers.ValidCheck(c) {
			return httpx.Invalid(map[string]string{"checks": "Unknown check " + c + "."})
		}
	}
	if active, _ := s.Store.ActiveDiagnosticRun(r.Context(), srv.ID); active != nil {
		return httpx.Conflict("Diagnostics are already running on this server.")
	}
	runID, jobID, err := s.enqueueDiagnostics(r.Context(), srv.ID, u, in.Checks)
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "server.diagnostics", "server", srv.ID, map[string]any{"name": srv.Name, "checks": in.Checks}, clientIP(r))
	httpx.JSON(w, 202, map[string]string{"runId": runID, "jobId": jobID})
	return nil
}

func (s *Server) getDiagnosticRun(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Diagnostic run")
	if err != nil {
		return err
	}
	run, err := s.Store.GetDiagnosticRun(r.Context(), id)
	if err != nil {
		return notFound(err, "Diagnostic run")
	}
	httpx.JSON(w, 200, map[string]any{"run": run})
	return nil
}

// bundle is a safe snapshot for troubleshooting: identity, state, engine facts, recent events and
// check results. No credentials, keys, environment values or container inspect documents.
func (s *Server) bundle(ctx context.Context, srv *store.Server) map[string]any {
	v := viewServer(srv)
	latest, _ := s.Store.LatestDiagnostics(ctx, srv.ID)
	events, _ := s.Store.ListServerEvents(ctx, srv.ID, store.EventQuery{Limit: 200})
	jobs, _ := s.Store.ListServerJobs(ctx, srv.ID, 20)
	sample, _ := s.Store.LatestServerSample(ctx, srv.ID)
	res, _ := s.Store.ListServerResources(ctx, srv.ID)
	workloads := make([]map[string]any, 0, len(res))
	for _, x := range res {
		workloads = append(workloads, map[string]any{"name": x.Name, "type": x.Type, "project": x.ProjectName, "desired": x.DesiredState, "status": x.Status})
	}
	for i := range latest {
		latest[i].Output = servers.Redact(latest[i].Output)
	}
	for i := range events {
		events[i].Message = servers.Redact(events[i].Message)
	}
	return map[string]any{
		"generatedAt": time.Now().UTC(),
		"server": map[string]any{
			"id": srv.ID, "name": srv.Name, "hostname": srv.Hostname, "environment": srv.Environment, "labels": srv.Labels, "local": srv.IsLocal,
			"connectionMethod": srv.ConnectionMethod, "endpoint": srv.Endpoint, "tlsFingerprint": srv.TLSFingerprint,
			"connectionState": srv.EffectiveState, "storedConnectionState": srv.ConnectionState, "runtimeState": srv.RuntimeState,
			"deploymentsEnabled": srv.DeploymentsEnabled, "maintenance": srv.Maintenance, "lastSeenAt": srv.LastSeenAt, "lastCheckAt": srv.LastCheckAt,
			"lastError": servers.Redact(srv.LastError), "failures": srv.Failures, "settings": v.Settings, "credentialsStored": v.HasKey,
			"clientCertificate": v.ClientCert,
		},
		"engine": v.Info, "capabilities": v.Caps, "warnings": v.Warnings, "latestSample": sample, "diagnostics": latest,
		"events": events, "jobs": jobs, "workloads": workloads,
		"excluded": "Credentials, private keys, environment variables, container inspect documents and log contents are never included.",
	}
}

func (s *Server) serverBundle(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "server.diagnostics_export", "server", srv.ID, map[string]any{"name": srv.Name}, clientIP(r))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="ignition-server-%s-%s.json"`, safeFilename(srv.Name), time.Now().UTC().Format("20060102-150405")))
	httpx.JSON(w, 200, s.bundle(r.Context(), srv))
	return nil
}

func (s *Server) serversBundle(w http.ResponseWriter, r *http.Request, u *store.User) error {
	ids := strings.Split(r.URL.Query().Get("ids"), ",")
	if len(ids) == 0 || len(ids) > 100 {
		return httpx.BadRequest("Pass 1 to 100 server IDs.")
	}
	out := []any{}
	failed := []map[string]string{}
	for _, id := range ids {
		if !uuidRE.MatchString(id) {
			continue
		}
		srv, err := s.Store.GetServer(r.Context(), id)
		if err != nil {
			failed = append(failed, map[string]string{"id": id, "error": "not found"})
			continue
		}
		out = append(out, s.bundle(r.Context(), srv))
	}
	s.Store.Audit(r.Context(), u, "server.diagnostics_export", "server", "", map[string]any{"servers": ids}, clientIP(r))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="ignition-servers-%s.json"`, time.Now().UTC().Format("20060102-150405")))
	httpx.JSON(w, 200, map[string]any{"generatedAt": time.Now().UTC(), "servers": out, "failed": failed})
	return nil
}

func safeFilename(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, s)
}

/* ------------------------------------- Logs ------------------------------------ */

type logLine struct {
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Source  string         `json:"source"`
	Stream  string         `json:"stream,omitempty"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
	Cursor  string         `json:"cursor"`
}

// serverLogs unifies the logs relevant to a server. Every message is redacted before it leaves.
func (s *Server) serverLogs(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	source := q.Get("source")
	if source == "" {
		source = "events"
	}
	search := strings.TrimSpace(q.Get("q"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	since := time.Now().Add(-time.Hour)
	if v, err := time.ParseDuration(q.Get("range")); err == nil && v > 0 && v <= 30*24*time.Hour {
		since = time.Now().Add(-v)
	}
	var levels []string
	if l := q.Get("level"); l != "" {
		levels = strings.Split(l, ",")
	}
	sources := []map[string]any{
		{"id": "events", "label": "Server events", "available": true, "retention": "30 days", "detail": "Connection and runtime changes, maintenance, credentials, diagnostics and operations."},
		{"id": "jobs", "label": "Operations and deployments", "available": true, "retention": "Kept with deployment history", "detail": "Step logs of server jobs and of deployments to resources on this server."},
		{"id": "container", "label": "Container logs", "available": viewServer(srv).Available, "retention": "Docker's log driver settings on the host", "detail": "stdout and stderr of a container, read through the Docker API."},
		{"id": "daemon", "label": "Docker daemon", "available": false, "detail": "The Docker Engine API doesn't expose the daemon's own log. Read it on the host: journalctl -u docker."},
		{"id": "agent", "label": "Agent", "available": false, "detail": "Ignition has no agent on servers; its connection results are in Server events."},
	}
	lines := []logLine{}
	switch source {
	case "events":
		ev, err := s.Store.ListServerEvents(r.Context(), srv.ID, store.EventQuery{Levels: levels, Q: search, Since: &since, Limit: limit})
		if err != nil {
			return err
		}
		for _, e := range ev {
			var fields map[string]any
			json.Unmarshal(e.Detail, &fields)
			if fields == nil {
				fields = map[string]any{}
			}
			fields["kind"], fields["actor"] = e.Kind, e.Actor
			lines = append(lines, logLine{Time: e.At, Level: e.Level, Source: e.Kind, Message: servers.Redact(e.Message), Fields: fields, Cursor: strconv.FormatInt(e.ID, 10)})
		}
	case "jobs":
		logs, err := s.Store.ServerJobLogs(r.Context(), srv.ID, since, levels, search, q.Get("resource"), limit)
		if err != nil {
			return err
		}
		for _, l := range logs {
			src := l.Kind
			if l.Deployment != nil {
				src = fmt.Sprintf("%s #%d", l.Resource, *l.Deployment)
			} else if l.Resource != "" {
				src = l.Resource + " · " + l.Kind
			}
			lines = append(lines, logLine{Time: l.At, Level: l.Level, Source: src, Message: servers.Redact(l.Message), Cursor: strconv.FormatInt(l.ID, 10),
				Fields: map[string]any{"step": l.Step, "jobId": l.JobID, "resourceId": l.ResourceID}})
		}
	case "container":
		cid := q.Get("container")
		if cid == "" {
			return httpx.Invalid(map[string]string{"container": "Choose a container."})
		}
		dk, err := s.Servers.Docker(srv)
		if err != nil {
			return httpx.Unavailable(err.Error())
		}
		owner, err := s.containerOwner(r.Context(), dk, srv, cid)
		if err != nil {
			return err
		}
		if owner == nil && !can(u, "servers.inspect") {
			return httpx.Forbidden("Logs of containers Ignition doesn't manage can hold anything; only admins can read them.")
		}
		raw, err := dk.Logs(r.Context(), cid, limit, since)
		if err != nil {
			return httpx.Unavailable("Docker didn't return logs: " + err.Error())
		}
		stream := q.Get("stream")
		for i, l := range raw {
			if stream != "" && l.Stream != stream {
				continue
			}
			msg := servers.Redact(l.Message)
			if search != "" && !strings.Contains(strings.ToLower(msg), strings.ToLower(search)) {
				continue
			}
			level := "info"
			if l.Stream == "stderr" {
				level = "warn"
			}
			lines = append(lines, logLine{Time: l.Time, Level: level, Source: "container", Stream: l.Stream, Message: msg, Cursor: strconv.Itoa(i)})
		}
		sort.SliceStable(lines, func(i, j int) bool { return lines[i].Time.After(lines[j].Time) })
	case "daemon", "agent":
		for _, src := range sources {
			if src["id"] == source {
				httpx.JSON(w, 200, map[string]any{"lines": lines, "available": false, "reason": src["detail"], "sources": sources})
				return nil
			}
		}
	default:
		return httpx.BadRequest("Unknown log source.")
	}
	httpx.JSON(w, 200, map[string]any{"lines": lines, "available": true, "sources": sources, "since": since.UTC(), "timezone": "UTC", "limit": limit, "truncated": len(lines) >= limit})
	return nil
}

/* ------------------------------------ Tasks ------------------------------------ */

var taskKinds = map[string]struct{ Title, Description string }{
	"diagnostics":       {"Scheduled diagnostics", "Runs every diagnostic check and records the results. Fails when a check fails."},
	"disk_check":        {"Disk-space check", "Runs the disk check. Fails when the disk is 95% full or more."},
	"docker_cleanup":    {"Safe Docker cleanup", "Removes stopped Ignition containers, untagged unused images and unused build cache. Never volumes or tagged images."},
	"reconcile":         {"Runtime reconciliation", "Checks the connection and refreshes every workload's recorded status from Docker."},
	"maintenance_check": {"Maintenance check", "Reports in-flight work, and fails if maintenance mode has been on longer than allowed."},
}

type serverTaskInput struct {
	Kind           string         `json:"kind"`
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Schedule       string         `json:"schedule"`
	Timezone       string         `json:"timezone"`
	Enabled        *bool          `json:"enabled"`
	TimeoutSeconds int            `json:"timeoutSeconds"`
	MaxAttempts    int            `json:"maxAttempts"`
	Concurrency    string         `json:"concurrency"`
	Options        map[string]any `json:"options"`
}

func (in *serverTaskInput) apply(t *store.ServerTask) map[string]string {
	errs := map[string]string{}
	if in.Name != "" || t.Name == "" {
		t.Name = strings.TrimSpace(in.Name)
	}
	if t.Name == "" || len(t.Name) > 80 {
		errs["name"] = "Between 1 and 80 characters."
	}
	if in.Description != "" {
		t.Description = strings.TrimSpace(in.Description)
	}
	if len(t.Description) > 500 {
		errs["description"] = "Up to 500 characters."
	}
	if in.Schedule != "" {
		t.Schedule = strings.TrimSpace(in.Schedule)
	}
	if in.Timezone != "" {
		t.Timezone = in.Timezone
	}
	if t.Timezone == "" {
		t.Timezone = "UTC"
	}
	if _, err := cron.Parse(t.Schedule); err != nil {
		errs["schedule"] = err.Error()
	} else if _, err := time.LoadLocation(t.Timezone); err != nil {
		errs["timezone"] = "Unknown timezone."
	}
	if in.Enabled != nil {
		t.Enabled = *in.Enabled
	}
	if in.TimeoutSeconds != 0 {
		t.TimeoutSeconds = in.TimeoutSeconds
	}
	if t.TimeoutSeconds < 10 || t.TimeoutSeconds > 3600 {
		errs["timeoutSeconds"] = "Between 10 seconds and 1 hour."
	}
	if in.MaxAttempts != 0 {
		t.MaxAttempts = in.MaxAttempts
	}
	if t.MaxAttempts < 1 || t.MaxAttempts > 5 {
		errs["maxAttempts"] = "Between 1 and 5 attempts."
	}
	if in.Concurrency != "" {
		t.Concurrency = in.Concurrency
	}
	if t.Concurrency != "skip" && t.Concurrency != "queue" {
		errs["concurrency"] = "Skip or queue."
	}
	if in.Options != nil {
		clean := map[string]any{}
		switch t.Kind {
		case "docker_cleanup":
			for _, k := range []string{"containers", "dangling", "buildCache", "networks"} {
				if v, ok := in.Options[k].(bool); ok {
					clean[k] = v
				}
			}
		case "maintenance_check":
			if v, ok := in.Options["maxHours"].(float64); ok {
				if v < 1 || v > 720 {
					errs["options"] = "Allowed maintenance length is 1 to 720 hours."
				}
				clean["maxHours"] = v
			}
		}
		t.Options, _ = json.Marshal(clean)
	}
	if len(t.Options) == 0 {
		t.Options = json.RawMessage(`{}`)
	}
	if len(errs) == 0 && t.Enabled {
		next, err := worker.NextRun(t.Schedule, t.Timezone, time.Now())
		if err != nil {
			errs["schedule"] = err.Error()
		}
		t.NextRunAt = next
	} else if !t.Enabled {
		t.NextRunAt = nil
	}
	return errs
}

func (s *Server) listServerTasks(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	list, err := s.Store.ListServerTasks(r.Context(), srv.ID)
	if err != nil {
		return err
	}
	kinds := []map[string]string{}
	for _, k := range []string{"diagnostics", "disk_check", "docker_cleanup", "reconcile", "maintenance_check"} {
		kinds = append(kinds, map[string]string{"id": k, "title": taskKinds[k].Title, "description": taskKinds[k].Description})
	}
	httpx.JSON(w, 200, map[string]any{"tasks": list, "kinds": kinds})
	return nil
}

func (s *Server) createServerTask(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	var in serverTaskInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if _, ok := taskKinds[in.Kind]; !ok {
		return httpx.Invalid(map[string]string{"kind": "Choose a task type."})
	}
	t := store.ServerTask{ServerID: srv.ID, Kind: in.Kind, Enabled: true, TimeoutSeconds: 300, MaxAttempts: 1, Concurrency: "skip"}
	if in.Options == nil {
		in.Options = map[string]any{}
	}
	if errs := in.apply(&t); len(errs) > 0 {
		return httpx.Invalid(errs)
	}
	id, err := s.Store.CreateServerTask(r.Context(), t, u.ID)
	if err != nil {
		if store.IsUnique(err) {
			return httpx.Invalid(map[string]string{"name": "Another task on this server has this name."})
		}
		return err
	}
	s.Store.Audit(r.Context(), u, "server.task_create", "server", srv.ID, map[string]any{"task": t.Name, "kind": t.Kind, "schedule": t.Schedule}, clientIP(r))
	created, _ := s.Store.GetServerTask(r.Context(), id)
	httpx.JSON(w, 201, map[string]any{"task": created})
	return nil
}

func (s *Server) loadServerTask(r *http.Request) (*store.ServerTask, error) {
	id, err := pathID(r, "Task")
	if err != nil {
		return nil, err
	}
	t, err := s.Store.GetServerTask(r.Context(), id)
	return t, notFound(err, "Task")
}

func (s *Server) updateServerTask(w http.ResponseWriter, r *http.Request, u *store.User) error {
	t, err := s.loadServerTask(r)
	if err != nil {
		return err
	}
	var in serverTaskInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.Kind != "" && in.Kind != t.Kind {
		return httpx.Invalid(map[string]string{"kind": "A task's type can't change. Create a new task instead."})
	}
	if errs := in.apply(t); len(errs) > 0 {
		return httpx.Invalid(errs)
	}
	if err := s.Store.UpdateServerTask(r.Context(), *t); err != nil {
		if store.IsUnique(err) {
			return httpx.Invalid(map[string]string{"name": "Another task on this server has this name."})
		}
		return err
	}
	s.Store.Audit(r.Context(), u, "server.task_update", "server", t.ServerID, map[string]any{"task": t.Name, "enabled": t.Enabled, "schedule": t.Schedule}, clientIP(r))
	httpx.JSON(w, 200, map[string]any{"task": t})
	return nil
}

func (s *Server) deleteServerTask(w http.ResponseWriter, r *http.Request, u *store.User) error {
	t, err := s.loadServerTask(r)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteServerTask(r.Context(), t.ID); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "server.task_delete", "server", t.ServerID, map[string]any{"task": t.Name}, clientIP(r))
	w.WriteHeader(204)
	return nil
}

func (s *Server) runServerTask(w http.ResponseWriter, r *http.Request, u *store.User) error {
	t, err := s.loadServerTask(r)
	if err != nil {
		return err
	}
	tx, err := s.Store.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	runID, status, err := servers.EnqueueTask(r.Context(), s.Store, tx, t, "manual", nil, u.ID, actorName(u))
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	if status == "skipped" {
		return httpx.Conflict("The previous run is still in progress, and this task skips overlapping runs.")
	}
	s.Store.Audit(r.Context(), u, "server.task_run", "server", t.ServerID, map[string]any{"task": t.Name}, clientIP(r))
	httpx.JSON(w, 202, map[string]string{"runId": runID})
	return nil
}

func (s *Server) listServerTaskRuns(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	t, err := s.loadServerTask(r)
	if err != nil {
		return err
	}
	runs, err := s.Store.ListServerTaskRuns(r.Context(), t.ID, 50)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"runs": runs})
	return nil
}

/* ------------------------------- Access & security ------------------------------ */

func (s *Server) getServerAccess(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	v := viewServer(srv)
	events, _ := s.Store.ListServerEvents(r.Context(), srv.ID, store.EventQuery{Kinds: []string{"security", "connection"}, Limit: 50})
	failures, _ := s.Store.ListServerEvents(r.Context(), srv.ID, store.EventQuery{Kinds: []string{"connection"}, Levels: []string{"warn", "error"}, Limit: 20})
	audit := []store.AuditEvent{} // the audit log is admin-only everywhere else too
	if can(u, "audit.view") {
		audit, _ = s.Store.ListAudit(r.Context(), srv.ID, 50)
	}
	permissions := []map[string]string{
		{"role": "Viewer", "can": "See the server, its workloads, metrics, events, diagnostics results and Docker inventory; read logs of Ignition workloads."},
		{"role": "Developer", "can": "Also test the connection, refresh runtime state, run diagnostics, and start, stop or restart Ignition workloads."},
		{"role": "Admin / Owner", "can": "Also change settings, maintenance and deployment eligibility, run cleanups, read logs and inspect documents of unmanaged containers, rotate or revoke credentials, and remove the server."},
	}
	access := map[string]any{"method": srv.ConnectionMethod, "permissions": permissions, "events": events, "failures": failures, "audit": audit}
	if srv.IsLocal {
		access["trust"] = "trusted"
		access["identity"] = map[string]any{"socket": s.Servers.Local.SocketPath(), "description": "Ignition's api and worker containers mount this host's Docker socket. Anything that can use the socket has root-equivalent access to the host, so it's never exposed to browsers: every Docker call goes through the API, which checks your role first."}
		access["rotation"] = "There are no credentials to rotate: access is controlled by who can reach the socket on this host."
	} else {
		trust := "trusted"
		switch {
		case srv.CredentialsRevokedAt != nil:
			trust = "revoked"
		case srv.LastError != "" && strings.Contains(srv.LastError, "identity"):
			trust = "identity_changed"
		}
		access["trust"] = trust
		access["identity"] = map[string]any{
			"endpoint": srv.Endpoint, "hostFingerprint": srv.TLSFingerprint, "clientCertificate": v.ClientCert, "credentialsStored": v.HasKey,
			"rotatedAt": srv.CredentialsRotatedAt, "revokedAt": srv.CredentialsRevokedAt, "keyStorage": "AES-256-GCM with IGNITION_SECRET_KEY, bound to this server. Never returned by the API.",
		}
		if eng := v.Info.Engine; eng != nil {
			access["version"] = map[string]string{"docker": eng.Version, "api": eng.APIVersion}
		}
	}
	httpx.JSON(w, 200, map[string]any{"server": v, "access": access})
	return nil
}

// rotateServerCredentials verifies the new certificate and key against the engine before replacing
// the old ones, so a bad pair can't lock Ignition out. With acceptIdentity the engine may present a
// new host certificate (it was reissued); the fingerprint the operator reviewed must still match.
func (s *Server) rotateServerCredentials(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	if srv.IsLocal {
		return httpx.Conflict("This machine uses the local Docker socket; there are no credentials to rotate.")
	}
	if !s.limiter.allow("server-verify:" + u.ID) {
		return httpx.TooMany("Too many attempts. Wait a minute.")
	}
	var in struct {
		servers.Credentials
		AcceptFingerprint string `json:"acceptFingerprint"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.CACert == "" {
		in.CACert = srv.CACert
	}
	pinned := srv.TLSFingerprint
	if in.AcceptFingerprint != "" {
		pinned = in.AcceptFingerprint
	}
	probe := serverInput{Endpoint: srv.Endpoint, Credentials: in.Credentials}
	res, errs := s.verifyRemote(r.Context(), &probe, pinned)
	if len(errs) > 0 {
		return httpx.Invalid(errs)
	}
	if !res.OK {
		for _, st := range res.Steps {
			if st.Status == "failed" {
				msg := st.Title + ": " + st.Detail + " The existing credentials were kept."
				if st.ID == "tls" && res.Fingerprint != "" && in.AcceptFingerprint == "" {
					httpx.JSON(w, 409, map[string]any{"error": map[string]any{"code": "identity_changed", "message": msg, "requestId": httpx.RequestID(r.Context())}, "fingerprint": res.Fingerprint})
					return nil
				}
				return httpx.Conflict(msg)
			}
		}
	}
	sealed, err := s.Secrets.Seal(in.ClientKey, servers.KeyContext(srv.ID))
	if err != nil {
		return err
	}
	if err := s.Store.RotateServerCredentials(r.Context(), srv.ID, in.CACert, in.ClientCert, sealed, res.Fingerprint); err != nil {
		return err
	}
	s.Servers.Forget(srv.ID)
	detail := map[string]any{"name": srv.Name, "fingerprint": res.Fingerprint, "identityChanged": res.Fingerprint != srv.TLSFingerprint}
	if res.ClientCert != nil {
		detail["clientCertExpires"] = res.ClientCert.NotAfter
	}
	s.Store.Audit(r.Context(), u, "server.credentials_rotate", "server", srv.ID, detail, clientIP(r))
	msg := "Credentials rotated; verified against the engine before the old ones were replaced."
	if res.Fingerprint != srv.TLSFingerprint {
		msg += " Accepted new host certificate " + res.Fingerprint[:16] + "…"
	}
	s.Store.AddServerEvent(r.Context(), srv.ID, "security", "info", msg, actorName(u), nil)
	srv, _ = s.Store.GetServer(r.Context(), srv.ID)
	servers.Check(r.Context(), s.Servers, s.Store, srv, actorName(u))
	srv, _ = s.Store.GetServer(r.Context(), srv.ID)
	httpx.JSON(w, 200, map[string]any{"server": viewServer(srv)})
	return nil
}

func (s *Server) revokeServerCredentials(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	if srv.IsLocal {
		return httpx.Conflict("This machine uses the local Docker socket; there are no credentials to revoke.")
	}
	if r.URL.Query().Get("confirm") != srv.Name {
		return httpx.BadRequest("Type the server's name to confirm.")
	}
	if err := s.Store.RevokeServerCredentials(r.Context(), srv.ID); err != nil {
		return err
	}
	s.Servers.Forget(srv.ID)
	s.Store.Audit(r.Context(), u, "server.credentials_revoke", "server", srv.ID, map[string]any{"name": srv.Name}, clientIP(r))
	s.Store.AddServerEvent(r.Context(), srv.ID, "security", "warn", "Credentials revoked: the stored client key was deleted. Ignition can't connect until new credentials are rotated in.", actorName(u), nil)
	srv, _ = s.Store.GetServer(r.Context(), srv.ID)
	httpx.JSON(w, 200, map[string]any{"server": viewServer(srv)})
	return nil
}

// changeServerEndpoint moves a remote server to a new address. The new address must present the
// same host identity, so this can't silently point a registration at a different machine.
func (s *Server) changeServerEndpoint(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	if srv.IsLocal {
		return httpx.Conflict("This machine is reached through its local socket.")
	}
	var in struct {
		Endpoint string `json:"endpoint"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if len(srv.ClientKeySealed) == 0 {
		return httpx.Conflict("Rotate in credentials first; there's no key to verify the new address with.")
	}
	plain, err := s.Secrets.Open(srv.ClientKeySealed, servers.KeyContext(srv.ID))
	if err != nil {
		return httpx.Conflict("The stored key can't be decrypted. Rotate credentials instead.")
	}
	probe := serverInput{Endpoint: in.Endpoint, Credentials: servers.Credentials{CACert: srv.CACert, ClientCert: srv.ClientCert, ClientKey: plain}}
	res, errs := s.verifyRemote(r.Context(), &probe, srv.TLSFingerprint)
	if len(errs) > 0 {
		return httpx.Invalid(errs)
	}
	if !res.OK {
		for _, st := range res.Steps {
			if st.Status == "failed" {
				return httpx.Conflict(st.Title + ": " + st.Detail + " The address wasn't changed.")
			}
		}
	}
	if err := s.Store.UpdateServerEndpoint(r.Context(), srv.ID, res.Endpoint, res.Fingerprint); err != nil {
		if store.IsUnique(err) {
			return httpx.Conflict("Another server already uses this address.")
		}
		return err
	}
	s.Servers.Forget(srv.ID)
	s.Store.Audit(r.Context(), u, "server.endpoint_change", "server", srv.ID, map[string]any{"from": srv.Endpoint, "to": res.Endpoint}, clientIP(r))
	s.Store.AddServerEvent(r.Context(), srv.ID, "settings", "info", "Address changed from "+srv.Endpoint+" to "+res.Endpoint+" (same host identity verified).", actorName(u), nil)
	srv, _ = s.Store.GetServer(r.Context(), srv.ID)
	httpx.JSON(w, 200, map[string]any{"server": viewServer(srv)})
	return nil
}

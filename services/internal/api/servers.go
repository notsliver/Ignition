package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/servers"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Infrastructure › Servers. Server management is a privileged control plane: reads are open to
 * viewers, routine checks to developers, and anything that changes a server, its credentials or
 * its Docker objects needs an admin. Every change is audited and recorded as a server event.
 */

type serverView struct {
	*store.Server
	Settings    servers.Settings     `json:"settings"`
	Info        servers.Info         `json:"info"`
	Caps        servers.Capabilities `json:"capabilities"`
	Warnings    []servers.Warning    `json:"warnings"`
	Eligibility servers.Eligibility  `json:"deploymentEligibility"`
	HasKey      bool                 `json:"hasCredentials"`
	ClientCert  *servers.CertSummary `json:"clientCertificate"`
	Available   bool                 `json:"available"`
}

func viewServer(srv *store.Server) serverView {
	v := serverView{Server: srv, Settings: servers.ParseSettings(srv.Settings), Caps: servers.Capabilities{}}
	json.Unmarshal(srv.Info, &v.Info)
	json.Unmarshal(srv.Capabilities, &v.Caps)
	v.Warnings = servers.Warnings(srv, v.Info)
	if v.Warnings == nil {
		v.Warnings = []servers.Warning{}
	}
	v.Eligibility = servers.DeploymentEligibility(srv)
	v.HasKey = len(srv.ClientKeySealed) > 0
	if !srv.IsLocal {
		v.ClientCert = servers.ClientCertSummary(srv.ClientCert)
	}
	v.Available = (srv.EffectiveState == servers.StateOnline || srv.EffectiveState == servers.StateDegraded) && srv.RuntimeState == servers.RuntimeReady
	return v
}

func (s *Server) loadServer(r *http.Request) (*store.Server, error) {
	id, err := pathID(r, "Server")
	if err != nil {
		return nil, err
	}
	srv, err := s.Store.GetServer(r.Context(), id)
	return srv, notFound(err, "Server")
}

func actorName(u *store.User) string { return u.Email }

func (s *Server) listServers(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	list, total, err := s.Store.ListServers(r.Context(), store.ServerQuery{Q: strings.TrimSpace(q.Get("q")), Filter: q.Get("filter"), Sort: q.Get("sort"),
		Desc: q.Get("dir") == "desc", Limit: limit, Offset: offset})
	if err != nil {
		return err
	}
	sum, err := s.Store.ServerSummary(r.Context())
	if err != nil {
		return err
	}
	views := make([]serverView, len(list))
	for i := range list {
		views[i] = viewServer(&list[i])
	}
	httpx.JSON(w, 200, map[string]any{"servers": views, "total": total, "summary": sum, "staleAfterSeconds": int(store.StaleAfter.Seconds()),
		"checkIntervalSeconds": int(servers.CheckInterval.Seconds())})
	return nil
}

func (s *Server) getServer(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"server": viewServer(srv)})
	return nil
}

/* ------------------------------ Metadata & settings ----------------------------- */

var serverLabelKeyRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9._/-]{0,62}[a-z0-9])?$`)

func validateIdentity(name, hostname, description, environment, location, notes *string, labels *map[string]string) map[string]string {
	errs := map[string]string{}
	if name != nil {
		*name = strings.TrimSpace(*name)
		if *name == "" || len(*name) > 64 {
			errs["name"] = "Between 1 and 64 characters."
		}
	}
	if hostname != nil {
		*hostname = strings.TrimSpace(*hostname)
		if len(*hostname) > 253 || strings.ContainsAny(*hostname, " /") {
			errs["hostname"] = "A hostname or IP address."
		}
	}
	if description != nil && len(*description) > 500 {
		errs["description"] = "Up to 500 characters."
	}
	if environment != nil {
		*environment = strings.TrimSpace(*environment)
		if len(*environment) > 40 {
			errs["environment"] = "Up to 40 characters."
		}
	}
	if location != nil {
		*location = strings.TrimSpace(*location)
		if len(*location) > 64 {
			errs["location"] = "Up to 64 characters."
		}
	}
	if notes != nil && len(*notes) > 4000 {
		errs["notes"] = "Up to 4,000 characters."
	}
	if labels != nil {
		if len(*labels) > 30 {
			errs["labels"] = "Up to 30 labels."
		}
		for k, v := range *labels {
			if !serverLabelKeyRE.MatchString(k) {
				errs["labels"] = fmt.Sprintf("Label %q: keys are lowercase letters, digits, dots, dashes, slashes.", k)
			} else if len(v) > 100 {
				errs["labels"] = fmt.Sprintf("Label %q: values up to 100 characters.", k)
			}
		}
	}
	return errs
}

func (s *Server) updateServer(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	var in store.ServerPatch
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	errs := validateIdentity(in.Name, in.Hostname, in.Description, in.Environment, in.Location, in.Notes, in.Labels)
	if len(in.Settings) > 0 {
		var set servers.Settings
		if err := json.Unmarshal(in.Settings, &set); err != nil {
			errs["settings"] = "Invalid settings."
		} else {
			for k, v := range servers.ValidateSettings(set) {
				errs[k] = v
			}
			in.Settings, _ = json.Marshal(set)
		}
	}
	if len(errs) > 0 {
		return httpx.Invalid(errs)
	}
	if err := s.Store.UpdateServer(r.Context(), srv.ID, in); err != nil {
		if store.IsUnique(err) {
			return httpx.Invalid(map[string]string{"name": "Another server already has this name."})
		}
		return err
	}
	changed := []string{}
	for k, set := range map[string]bool{"name": in.Name != nil, "hostname": in.Hostname != nil, "description": in.Description != nil, "environment": in.Environment != nil, "location": in.Location != nil,
		"labels": in.Labels != nil, "notes": in.Notes != nil, "settings": len(in.Settings) > 0} {
		if set {
			changed = append(changed, k)
		}
	}
	s.Store.Audit(r.Context(), u, "server.update", "server", srv.ID, map[string]any{"name": srv.Name, "fields": changed}, clientIP(r))
	s.Store.AddServerEvent(r.Context(), srv.ID, "settings", "info", "Updated "+strings.Join(changed, ", "), actorName(u), nil)
	srv, _ = s.Store.GetServer(r.Context(), srv.ID)
	httpx.JSON(w, 200, map[string]any{"server": viewServer(srv)})
	return nil
}

func (s *Server) setServerDeployments(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if !srv.IsLocal && in.Enabled {
		return httpx.Conflict("Remote servers can't receive deployments: the deploy engine only targets the local Docker host.")
	}
	if err := s.Store.SetServerDeployments(r.Context(), srv.ID, in.Enabled); err != nil {
		return err
	}
	verb := map[bool]string{true: "enabled", false: "disabled"}[in.Enabled]
	s.Store.Audit(r.Context(), u, "server.deployments_"+verb, "server", srv.ID, map[string]any{"name": srv.Name}, clientIP(r))
	s.Store.AddServerEvent(r.Context(), srv.ID, "deployments", "info", "New deployments "+verb, actorName(u), nil)
	srv, _ = s.Store.GetServer(r.Context(), srv.ID)
	httpx.JSON(w, 200, map[string]any{"server": viewServer(srv)})
	return nil
}

/* --------------------------------- Maintenance --------------------------------- */

func (s *Server) setServerMaintenance(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	var in struct {
		Enabled bool   `json:"enabled"`
		Note    string `json:"note"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if len(in.Note) > 2000 {
		return httpx.Invalid(map[string]string{"note": "Up to 2,000 characters."})
	}
	if srv.Removing {
		return httpx.Conflict("This server is being removed.")
	}
	changed, err := s.Store.SetServerMaintenance(r.Context(), srv.ID, in.Enabled, strings.TrimSpace(in.Note), u.ID)
	if err != nil {
		return err
	}
	if changed {
		act, _ := s.Store.ServerActivity(r.Context(), srv.ID)
		verb := map[bool]string{true: "entered", false: "exited"}[in.Enabled]
		msg := "Maintenance " + verb
		if in.Enabled && in.Note != "" {
			msg += ": " + strings.TrimSpace(in.Note)
		}
		s.Store.Audit(r.Context(), u, "server.maintenance_"+verb, "server", srv.ID, map[string]any{"name": srv.Name, "note": in.Note, "activeDeployments": len(act.Deployments)}, clientIP(r))
		s.Store.AddServerEvent(r.Context(), srv.ID, "maintenance", "info", msg, actorName(u), map[string]any{"activeDeployments": len(act.Deployments), "activeJobs": len(act.Jobs)})
	}
	srv, _ = s.Store.GetServer(r.Context(), srv.ID)
	httpx.JSON(w, 200, map[string]any{"server": viewServer(srv), "changed": changed})
	return nil
}

func (s *Server) putMaintenanceNote(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	var in struct {
		Note string `json:"note"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if !srv.Maintenance {
		return httpx.Conflict("Notes are kept for an active maintenance window. Enter maintenance first.")
	}
	if len(in.Note) > 2000 {
		return httpx.Invalid(map[string]string{"note": "Up to 2,000 characters."})
	}
	s.Store.SetMaintenanceNote(r.Context(), srv.ID, strings.TrimSpace(in.Note))
	s.Store.AddServerEvent(r.Context(), srv.ID, "maintenance", "info", "Maintenance note: "+strings.TrimSpace(in.Note), actorName(u), nil)
	w.WriteHeader(204)
	return nil
}

func (s *Server) getMaintenance(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	act, err := s.Store.ServerActivity(r.Context(), srv.ID)
	if err != nil {
		return err
	}
	res, err := s.Store.ListServerResources(r.Context(), srv.ID)
	if err != nil {
		return err
	}
	vols, _ := s.Store.ServerVolumes(r.Context(), srv.ID)
	tasks, _ := s.Store.ListServerTasks(r.Context(), srv.ID)
	history, _ := s.Store.ListServerEvents(r.Context(), srv.ID, store.EventQuery{Kinds: []string{"maintenance", "deployments"}, Limit: 50})
	running := 0
	for _, x := range res {
		if x.Status == "running" {
			running++
		}
	}
	httpx.JSON(w, 200, map[string]any{
		"server": viewServer(srv), "activity": act, "workloads": len(res), "running": running, "volumes": len(vols), "tasks": len(tasks), "history": history,
		"policy": map[string]any{
			"blocksDeployments": true, "pausesServerTasks": true, "stopsWorkloads": false, "drains": false,
			"text": "While in maintenance, Ignition refuses new deployments (manual, hooks, tokens, rollbacks) to resources on this server and skips scheduled server tasks except diagnostics and checks. Running workloads keep running. Ignition can't move workloads to another server or reboot the host: do that yourself, then exit maintenance.",
		},
	})
	return nil
}

/* ------------------------------- Checks & refresh ------------------------------- */

// testServer checks the connection now and returns the outcome; the result is also recorded.
func (s *Server) testServer(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	o := servers.Check(r.Context(), s.Servers, s.Store, srv, actorName(u))
	srv, _ = s.Store.GetServer(r.Context(), srv.ID)
	result := map[string]any{"reached": o.Reached, "runtime": o.Runtime, "error": o.Error, "errorKind": o.ErrorKind, "latencyMs": o.Latency.Milliseconds()}
	if o.HasInfo {
		result["docker"] = o.Info.Version
		result["apiVersion"] = o.Info.APIVersion
	}
	httpx.JSON(w, 200, map[string]any{"result": result, "server": viewServer(srv)})
	return nil
}

func (s *Server) refreshServer(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	jobID, err := s.enqueueServerOp(r.Context(), srv.ID, u.ID, servers.Payload{Op: "refresh", Actor: actorName(u), TimeoutSeconds: 120})
	if err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "server.refresh", "server", srv.ID, map[string]any{"name": srv.Name}, clientIP(r))
	httpx.JSON(w, 202, map[string]string{"jobId": jobID})
	return nil
}

func (s *Server) enqueueServerOp(ctx context.Context, serverID, userID string, p servers.Payload) (string, error) {
	tx, err := s.Store.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	id, err := servers.Enqueue(ctx, tx, serverID, userID, p, 1)
	if err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func (s *Server) getServerJob(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	id, err := pathID(r, "Job")
	if err != nil {
		return err
	}
	j, err := s.Store.GetServerJob(r.Context(), id)
	if err != nil {
		return notFound(err, "Job")
	}
	logs, _ := s.Store.JobLogs(r.Context(), id, 0)
	httpx.JSON(w, 200, map[string]any{"job": j, "logs": logs})
	return nil
}

func (s *Server) cancelServerJob(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := pathID(r, "Job")
	if err != nil {
		return err
	}
	j, err := s.Store.GetServerJob(r.Context(), id)
	if err != nil {
		return notFound(err, "Job")
	}
	status, err := jobs.RequestCancel(r.Context(), s.Store.DB, id)
	if err != nil {
		return err
	}
	if status == "" {
		return httpx.Conflict("This job already finished.")
	}
	s.Store.Audit(r.Context(), u, "server.job_cancel", "server", j.ServerID, map[string]any{"job": id, "op": j.Op}, clientIP(r))
	httpx.JSON(w, 202, map[string]string{"status": status})
	return nil
}

func (s *Server) listServerJobs(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	list, err := s.Store.ListServerJobs(r.Context(), srv.ID, 50)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"jobs": list})
	return nil
}

/* ------------------------------- Events & activity ------------------------------ */

func (s *Server) listServerEvents(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	eq := store.EventQuery{Limit: limit, Before: before, Q: q.Get("q")}
	if k := q.Get("kind"); k != "" {
		eq.Kinds = strings.Split(k, ",")
	}
	list, err := s.Store.ListServerEvents(r.Context(), srv.ID, eq)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"events": list})
	return nil
}

/* ----------------------------------- Workloads ---------------------------------- */

func (s *Server) listServerResources(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	list, err := s.Store.ListServerResources(r.Context(), srv.ID)
	if err != nil {
		return err
	}
	// Health state per resource, so "unhealthy" can be shown apart from "running".
	health := map[string]string{}
	rows, err := s.Store.DB.Query(r.Context(), `SELECT h.resource_id, h.state FROM resource_health h JOIN resources r ON r.id = h.resource_id WHERE r.server_id = $1 AND h.enabled`, srv.ID)
	if err == nil {
		for rows.Next() {
			var id, st string
			rows.Scan(&id, &st)
			health[id] = st
		}
		rows.Close()
	}
	httpx.JSON(w, 200, map[string]any{"resources": list, "health": health, "observedAt": time.Now().UTC(), "server": viewServer(srv)})
	return nil
}

func (s *Server) listServerDeployments(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	list, total, err := s.Store.ListServerDeployments(r.Context(), srv.ID, store.DeploymentQuery{Q: strings.TrimSpace(q.Get("q")), Status: q.Get("status"), Limit: limit, Offset: offset})
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"deployments": list, "total": total})
	return nil
}

/* ----------------------------------- Metrics ----------------------------------- */

func (s *Server) serverMetrics(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	ranges := map[string][2]time.Duration{
		"15m": {15 * time.Minute, 15 * time.Second}, "1h": {time.Hour, time.Minute}, "6h": {6 * time.Hour, 5 * time.Minute},
		"24h": {24 * time.Hour, 20 * time.Minute}, "7d": {7 * 24 * time.Hour, 2 * time.Hour},
	}
	rg := r.URL.Query().Get("range")
	spec, ok := ranges[rg]
	if !ok {
		rg, spec = "1h", ranges["1h"]
	}
	buckets, err := s.Store.ServerMetricSeries(r.Context(), srv.ID, spec[0], spec[1])
	if err != nil {
		return err
	}
	latest, _ := s.Store.LatestServerSample(r.Context(), srv.ID)
	v := viewServer(srv)
	settings := v.Settings
	// Why each metric family is or isn't available, so the page never shows zeros for "not measured".
	collector := v.Info.Collector
	support := map[string]string{}
	set := func(state string, keys ...string) {
		for _, k := range keys {
			support[k] = state
		}
	}
	switch {
	case !settings.CollectMetrics:
		set("disabled", "cpu", "load", "memory", "swap", "disk", "diskio", "uptime", "containers", "workloads", "network")
	case collector == "probe":
		set("supported", "cpu", "load", "memory", "swap", "disk", "diskio", "uptime", "containers", "workloads", "network")
	case collector == "procfs":
		set("supported", "cpu", "load", "memory", "swap", "disk", "diskio", "uptime", "containers", "workloads")
		set("unsupported", "network")
	case collector == "engine":
		set("unsupported", "cpu", "load", "swap", "disk", "diskio", "uptime", "network")
		set("supported", "containers", "workloads")
		set("partial", "memory")
	default:
		set("unknown", "cpu", "load", "memory", "swap", "disk", "diskio", "uptime", "containers", "workloads", "network")
	}
	collectorState := "ok"
	switch {
	case !settings.CollectMetrics:
		collectorState = "disabled"
	case v.EffectiveState == servers.StateUnknown:
		collectorState = "stale"
	case !v.Available:
		collectorState = "unavailable"
	case latest == nil:
		collectorState = "waiting"
	case time.Since(latest.At) > store.MetricFreshness:
		collectorState = "stale"
	}
	reasons := map[string]string{}
	if collector != "probe" {
		reasons["network"] = "Host network throughput comes from the host probe, which isn't running here. Ignition's own container only sees its own interface. Turn the host probe on in Settings."
		if !settings.HostProbe {
			reasons["network"] = "Host network throughput comes from the host probe, which is off in Settings."
		}
	}
	if collector == "engine" {
		reasons["cpu"] = "The Docker Engine API doesn't report host CPU or load. The host probe reads them; it's off or hasn't answered yet."
		reasons["memory"] = "Only total memory is known (from the engine). Used memory isn't reported by the Docker API."
		reasons["disk"] = "Filesystem capacity isn't reported by the Docker API. Docker's own usage is on the Storage page."
	}
	httpx.JSON(w, 200, map[string]any{"range": rg, "stepSeconds": int(spec[1].Seconds()), "buckets": buckets, "latest": latest, "support": support, "reasons": reasons, "hostProbe": settings.HostProbe,
		"collector": collector, "collectorState": collectorState, "retentionDays": 7, "intervalSeconds": int(servers.CheckInterval.Seconds()), "capacity": v.Info.Engine})
	return nil
}

/* ------------------------------------ Bulk ------------------------------------- */

type bulkTarget struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason"`
	Status   string `json:"status"` // queued, done, failed, skipped
	JobID    string `json:"jobId,omitempty"`
	Error    string `json:"error,omitempty"`
}

var bulkActions = map[string]string{
	"test": "Test connection", "refresh": "Refresh runtime state", "diagnostics": "Run diagnostics",
	"enable_deployments": "Enable deployments", "disable_deployments": "Disable deployments", "enter_maintenance": "Enter maintenance",
}

// bulkServers validates every target on its own. With preview, nothing runs: the caller shows
// which targets are eligible first. Read-only checks run as jobs; state changes apply per server.
func (s *Server) bulkServers(w http.ResponseWriter, r *http.Request, u *store.User) error {
	var in struct {
		Action  string   `json:"action"`
		IDs     []string `json:"ids"`
		Note    string   `json:"note"`
		Preview bool     `json:"preview"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if _, ok := bulkActions[in.Action]; !ok {
		return httpx.BadRequest("Unknown bulk action.")
	}
	if len(in.IDs) == 0 || len(in.IDs) > 100 {
		return httpx.BadRequest("Select between 1 and 100 servers.")
	}
	out := []bulkTarget{}
	for _, id := range in.IDs {
		t := bulkTarget{ID: id}
		if !uuidRE.MatchString(id) {
			t.Reason, t.Status = "Unknown server.", "skipped"
			out = append(out, t)
			continue
		}
		srv, err := s.Store.GetServer(r.Context(), id)
		if err != nil {
			t.Reason, t.Status = "Server not found.", "skipped"
			out = append(out, t)
			continue
		}
		t.Name = srv.Name
		t.Eligible, t.Reason = bulkEligible(in.Action, srv)
		if !t.Eligible {
			t.Status = "skipped"
			out = append(out, t)
			continue
		}
		if in.Preview {
			out = append(out, t)
			continue
		}
		switch in.Action {
		case "test", "refresh":
			t.JobID, err = s.enqueueServerOp(r.Context(), srv.ID, u.ID, servers.Payload{Op: "refresh", Actor: actorName(u), TimeoutSeconds: 120})
		case "diagnostics":
			_, t.JobID, err = s.enqueueDiagnostics(r.Context(), srv.ID, u, servers.CheckIDs())
		case "enable_deployments", "disable_deployments":
			on := in.Action == "enable_deployments"
			if err = s.Store.SetServerDeployments(r.Context(), srv.ID, on); err == nil {
				verb := map[bool]string{true: "enabled", false: "disabled"}[on]
				s.Store.AddServerEvent(r.Context(), srv.ID, "deployments", "info", "New deployments "+verb+" (bulk)", actorName(u), nil)
			}
		case "enter_maintenance":
			var changed bool
			if changed, err = s.Store.SetServerMaintenance(r.Context(), srv.ID, true, strings.TrimSpace(in.Note), u.ID); err == nil && changed {
				s.Store.AddServerEvent(r.Context(), srv.ID, "maintenance", "info", "Maintenance entered (bulk)", actorName(u), nil)
			}
		}
		if err != nil {
			t.Status, t.Error = "failed", err.Error()
		} else if t.JobID != "" {
			t.Status = "queued"
		} else {
			t.Status = "done"
		}
		out = append(out, t)
	}
	if !in.Preview {
		ids := make([]string, 0, len(out))
		for _, t := range out {
			if t.Eligible {
				ids = append(ids, t.ID)
			}
		}
		s.Store.Audit(r.Context(), u, "server.bulk_"+in.Action, "server", "", map[string]any{"servers": ids, "requested": len(in.IDs)}, clientIP(r))
	}
	httpx.JSON(w, 200, map[string]any{"action": in.Action, "label": bulkActions[in.Action], "targets": out})
	return nil
}

func bulkEligible(action string, srv *store.Server) (bool, string) {
	if srv.Removing {
		return false, "Being removed."
	}
	switch action {
	case "test", "refresh":
		if !srv.IsLocal && len(srv.ClientKeySealed) == 0 {
			return false, "No credentials (revoked)."
		}
	case "diagnostics":
		if !srv.IsLocal && len(srv.ClientKeySealed) == 0 {
			return false, "No credentials (revoked)."
		}
	case "enable_deployments":
		if !srv.IsLocal {
			return false, "Remote servers can't receive deployments."
		}
		if srv.DeploymentsEnabled {
			return false, "Already enabled."
		}
	case "disable_deployments":
		if !srv.IsLocal {
			return false, "Remote servers never receive deployments."
		}
		if !srv.DeploymentsEnabled {
			return false, "Already disabled."
		}
	case "enter_maintenance":
		if srv.Maintenance {
			return false, "Already in maintenance."
		}
	}
	return true, ""
}

/* --------------------------------- Add server ---------------------------------- */

type serverInput struct {
	Name        string            `json:"name"`
	Hostname    string            `json:"hostname"`
	Description string            `json:"description"`
	Environment string            `json:"environment"`
	Location    string            `json:"location"`
	Labels      map[string]string `json:"labels"`
	Notes       string            `json:"notes"`
	Endpoint    string            `json:"endpoint"`
	servers.Credentials
	ExpectedFingerprint string `json:"expectedFingerprint"` // the identity the operator reviewed in the wizard
}

type verifyStep struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // passed, failed, skipped, warning
	Detail string `json:"detail"`
	MS     int64  `json:"ms,omitempty"`
}

type verifyResult struct {
	OK           bool                 `json:"ok"`
	Endpoint     string               `json:"endpoint"`
	Steps        []verifyStep         `json:"steps"`
	Fingerprint  string               `json:"fingerprint"`
	Info         *runtime.EngineInfo  `json:"engine"`
	Capabilities servers.Capabilities `json:"capabilities"`
	ClientCert   *servers.CertSummary `json:"clientCertificate"`
}

// verifyRemote runs the enrollment checks. It never persists anything and never echoes secrets.
func (s *Server) verifyRemote(ctx context.Context, in *serverInput, pinned string) (verifyResult, map[string]string) {
	res := verifyResult{Steps: []verifyStep{}}
	errs := map[string]string{}
	host, port, err := servers.ParseEndpoint(in.Endpoint)
	if err != nil {
		errs["endpoint"] = err.Error()
	}
	credErrs, cert := in.Credentials.Validate()
	for k, v := range credErrs {
		errs[k] = v
	}
	if len(errs) > 0 {
		return res, errs
	}
	res.ClientCert = cert
	endpoint := net.JoinHostPort(host, strconv.Itoa(port))
	res.Endpoint = endpoint
	step := func(id, title, status, detail string, start time.Time) {
		res.Steps = append(res.Steps, verifyStep{id, title, status, detail, time.Since(start).Milliseconds()})
	}
	skipRest := func(ids ...string) {
		titles := map[string]string{"tcp": "Network connectivity", "tls": "Server identity", "auth": "Authentication", "engine": "Docker Engine", "platform": "Operating system and architecture", "permissions": "Runtime permissions"}
		for _, id := range ids {
			res.Steps = append(res.Steps, verifyStep{ID: id, Title: titles[id], Status: "skipped", Detail: "Skipped because an earlier step failed."})
		}
	}

	t0 := time.Now()
	if err := servers.ResolveAllowed(host, func(h string) ([]netip.Addr, error) { return net.DefaultResolver.LookupNetIP(ctx, "ip", h) }); err != nil {
		step("tcp", "Network connectivity", "failed", err.Error(), t0)
		skipRest("tls", "auth", "engine", "platform", "permissions")
		return res, nil
	}
	dctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	conn, err := (&net.Dialer{}).DialContext(dctx, "tcp", endpoint)
	cancel()
	if err != nil {
		msg, _ := servers.Classify(err)
		step("tcp", "Network connectivity", "failed", msg, t0)
		skipRest("tls", "auth", "engine", "platform", "permissions")
		return res, nil
	}
	conn.Close()
	step("tcp", "Network connectivity", "passed", "Opened a TCP connection to "+endpoint+".", t0)

	t0 = time.Now()
	var seen string
	dk, err := servers.RemoteDocker(endpoint, in.Credentials, pinned, &seen, 8*time.Second)
	if err != nil {
		step("tls", "Server identity", "failed", err.Error(), t0)
		skipRest("auth", "engine", "platform", "permissions")
		return res, nil
	}
	ectx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	info, err := dk.EngineInfo(ectx)
	res.Fingerprint = seen
	if err != nil {
		msg, kind := servers.Classify(err)
		switch kind {
		case "identity", "tls":
			step("tls", "Server identity", "failed", msg, t0)
			skipRest("auth", "engine", "platform", "permissions")
		case "auth":
			step("tls", "Server identity", "passed", "Certificate signed by the CA, valid for "+host+".", t0)
			step("auth", "Authentication", "failed", msg, t0)
			skipRest("engine", "platform", "permissions")
		default:
			step("tls", "Server identity", "passed", "Certificate signed by the CA, valid for "+host+".", t0)
			step("auth", "Authentication", "passed", "The engine accepted Ignition's client certificate.", t0)
			step("engine", "Docker Engine", "failed", msg, t0)
			skipRest("platform", "permissions")
		}
		return res, nil
	}
	step("tls", "Server identity", "passed", fmt.Sprintf("Certificate signed by the CA and valid for %s. SHA-256 %s.", host, seen), t0)
	step("auth", "Authentication", "passed", "The engine accepted Ignition's client certificate.", t0)
	o := servers.Observation{Reached: true, Runtime: servers.RuntimeReady, Info: info, HasInfo: true}
	if !runtime.APICompatible(info.APIVersion) {
		o.Runtime = servers.RuntimeIncompatible
		step("engine", "Docker Engine", "failed", fmt.Sprintf("Docker %s (API %s) is too old: Ignition needs API 1.44 or newer.", info.Version, info.APIVersion), t0)
	} else {
		step("engine", "Docker Engine", "passed", fmt.Sprintf("Docker %s, API %s, storage driver %s.", info.Version, info.APIVersion, info.StorageDriver), t0)
	}
	t0 = time.Now()
	status := "passed"
	detail := fmt.Sprintf("%s (%s, %s), %d CPUs, %s memory.", info.OS, info.OSType, info.Arch, info.NCPU, servers.BytesText(info.MemTotal))
	if info.OSType != "linux" {
		status, detail = "failed", "The engine runs "+info.OSType+" containers. Ignition manages Linux engines."
	}
	step("platform", "Operating system and architecture", status, detail, t0)
	t0 = time.Now()
	if _, err := dk.ListContainers(ectx); err != nil {
		step("permissions", "Runtime permissions", "failed", "Listing containers failed: "+err.Error(), t0)
	} else if _, err := dk.DiskUsage(ectx); err != nil {
		step("permissions", "Runtime permissions", "warning", "Containers are visible, but disk usage can't be read: "+err.Error(), t0)
	} else {
		step("permissions", "Runtime permissions", "passed", "Ignition can list containers, images, networks and volumes.", t0)
	}
	res.Info = &info
	res.Capabilities = servers.DeriveCapabilities(&store.Server{IsLocal: false}, o)
	res.OK = true
	for _, st := range res.Steps {
		if st.Status == "failed" {
			res.OK = false
		}
	}
	return res, nil
}

func (s *Server) verifyServer(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if !s.limiter.allow("server-verify:" + u.ID) {
		return httpx.TooMany("Too many verification attempts. Wait a minute.")
	}
	var in serverInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	res, errs := s.verifyRemote(r.Context(), &in, "")
	if len(errs) > 0 {
		return httpx.Invalid(errs)
	}
	if res.OK {
		var dup int
		s.Store.DB.QueryRow(r.Context(), `SELECT count(*) FROM servers WHERE lower(endpoint) = lower($1)`, res.Endpoint).Scan(&dup)
		if dup > 0 {
			res.Steps = append(res.Steps, verifyStep{ID: "duplicate", Title: "Not already registered", Status: "failed", Detail: "A server with this endpoint is already registered."})
			res.OK = false
		}
	}
	httpx.JSON(w, 200, res)
	return nil
}

// createServer re-verifies the connection and registers the server only if the engine still
// presents the identity the operator reviewed. A retry after a failure can't create a duplicate:
// endpoints and names are unique.
func (s *Server) createServer(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if !s.limiter.allow("server-verify:" + u.ID) {
		return httpx.TooMany("Too many attempts. Wait a minute.")
	}
	var in serverInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	errs := validateIdentity(&in.Name, &in.Hostname, &in.Description, &in.Environment, &in.Location, &in.Notes, &in.Labels)
	if in.ExpectedFingerprint == "" {
		errs["expectedFingerprint"] = "Verify the connection and review the server's identity first."
	}
	if len(errs) > 0 {
		return httpx.Invalid(errs)
	}
	res, verrs := s.verifyRemote(r.Context(), &in, in.ExpectedFingerprint)
	if len(verrs) > 0 {
		return httpx.Invalid(verrs)
	}
	if !res.OK {
		for _, st := range res.Steps {
			if st.Status == "failed" {
				return httpx.Conflict(st.Title + ": " + st.Detail)
			}
		}
		return httpx.Conflict("Verification failed.")
	}
	if in.Hostname == "" {
		in.Hostname, _, _ = net.SplitHostPort(res.Endpoint)
	}
	key := in.ClientKey
	info := servers.Info{CheckedAt: time.Now().UTC(), Collector: "engine", Engine: res.Info}
	if res.ClientCert != nil {
		info.ClientCertExp = &res.ClientCert.NotAfter
	}
	id, err := s.Store.CreateRemoteServer(r.Context(), store.NewServer{
		Name: in.Name, Hostname: in.Hostname, Description: in.Description, Environment: in.Environment, Location: in.Location, Notes: in.Notes, Labels: in.Labels,
		Endpoint: res.Endpoint, CACert: in.CACert, ClientCert: in.ClientCert, TLSFingerprint: res.Fingerprint, Info: info, Capabilities: res.Capabilities, CreatedBy: u.ID,
		ClientKeySealer: func(id string) ([]byte, error) { return s.Secrets.Seal(key, servers.KeyContext(id)) },
	})
	if err != nil {
		if store.IsUnique(err) {
			return httpx.Conflict("A server with this name or endpoint is already registered.")
		}
		return err
	}
	s.Store.Audit(r.Context(), u, "server.create", "server", id, map[string]any{"name": in.Name, "endpoint": res.Endpoint, "fingerprint": res.Fingerprint}, clientIP(r))
	s.Store.AddServerEvent(r.Context(), id, "security", "info", "Registered over mutual TLS; accepted host certificate "+res.Fingerprint[:16]+"…", actorName(u), nil)
	srv, _ := s.Store.GetServer(r.Context(), id)
	httpx.JSON(w, 201, map[string]any{"server": viewServer(srv)})
	return nil
}

/* ----------------------------------- Removal ----------------------------------- */

type removalPreview struct {
	Server        serverView              `json:"server"`
	Resources     []store.Resource        `json:"resources"`
	Activity      store.ServerActivity    `json:"activity"`
	Volumes       int                     `json:"volumes"`
	Tasks         int                     `json:"tasks"`
	Blockers      []string                `json:"blockers"`
	Effects       []string                `json:"effects"`
	NotDone       []string                `json:"notDone"`
	Reachable     bool                    `json:"reachable"`
}

func (s *Server) buildRemovalPreview(ctx context.Context, srv *store.Server) (removalPreview, error) {
	p := removalPreview{Server: viewServer(srv), Blockers: []string{}}
	var err error
	if p.Resources, err = s.Store.ListServerResources(ctx, srv.ID); err != nil {
		return p, err
	}
	if p.Activity, err = s.Store.ServerActivity(ctx, srv.ID); err != nil {
		return p, err
	}
	vols, _ := s.Store.ServerVolumes(ctx, srv.ID)
	p.Volumes = len(vols)
	p.Tasks, _ = s.Store.ServerTaskCount(ctx, srv.ID)
	p.Reachable = p.Server.Available
	if srv.IsLocal {
		p.Blockers = append(p.Blockers, "This is the host Ignition runs on. It can't be removed.")
	}
	if len(p.Resources) > 0 {
		p.Blockers = append(p.Blockers, fmt.Sprintf("%d resource(s) are assigned to this server. Delete them from their projects first; Ignition doesn't migrate workloads between servers.", len(p.Resources)))
	}
	if len(p.Activity.Deployments) > 0 || len(p.Activity.Jobs) > 0 {
		p.Blockers = append(p.Blockers, fmt.Sprintf("%d deployment(s) and %d server job(s) are in progress. Wait for them or cancel them.", len(p.Activity.Deployments), len(p.Activity.Jobs)))
	}
	p.Effects = []string{
		"The server's registration, events, metrics history, diagnostic results and scheduled server tasks are deleted from Ignition.",
		"The stored client key is deleted, so Ignition can no longer connect.",
	}
	if p.Tasks > 0 {
		p.Effects = append(p.Effects, fmt.Sprintf("%d scheduled server task(s) stop running.", p.Tasks))
	}
	p.NotDone = []string{
		"Nothing on the server is stopped or deleted: containers, images, networks and volumes (with their data) stay exactly as they are.",
		"The client certificate isn't revoked at its CA. To cut access completely, remove the CA from the daemon's --tlscacert or reissue the daemon's certificates.",
		"No data moves to another server. Docker volumes belong to this host.",
	}
	if !p.Reachable {
		p.NotDone = append(p.NotDone, "The server isn't reachable now, so Ignition can't confirm anything about its current state.")
	}
	return p, nil
}

func (s *Server) serverRemovalPreview(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	p, err := s.buildRemovalPreview(r.Context(), srv)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, p)
	return nil
}

func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request, u *store.User) error {
	srv, err := s.loadServer(r)
	if err != nil {
		return err
	}
	if r.URL.Query().Get("confirm") != srv.Name {
		return httpx.BadRequest("Type the server's name to confirm.")
	}
	p, err := s.buildRemovalPreview(r.Context(), srv)
	if err != nil {
		return err
	}
	if len(p.Blockers) > 0 {
		return httpx.Conflict(strings.Join(p.Blockers, " "))
	}
	if err := s.Store.SetServerRemoving(r.Context(), srv.ID, true); err != nil {
		return err
	}
	if err := s.Store.DeleteServer(r.Context(), srv.ID); err != nil {
		s.Store.SetServerRemoving(context.WithoutCancel(r.Context()), srv.ID, false)
		return httpx.Conflict("The server couldn't be removed: " + err.Error())
	}
	s.Servers.Forget(srv.ID)
	s.Store.Audit(r.Context(), u, "server.remove", "server", srv.ID, map[string]any{"name": srv.Name, "endpoint": srv.Endpoint, "reachable": p.Reachable}, clientIP(r))
	w.WriteHeader(204)
	return nil
}

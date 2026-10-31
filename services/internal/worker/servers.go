package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/notsliver/Ignition/services/internal/hostinfo"
	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/servers"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Server checks and operations.
 *
 * Every CheckInterval the worker checks each server (one worker per server per interval, claimed in
 * the database), records the connection and runtime state, emits an event when either changes, and
 * stores a host metrics sample. Operators' server work (refresh, diagnostics, cleanup, scheduled
 * server tasks) runs as KindServer jobs on the shared durable queue, so it survives restarts,
 * retries per policy and can be cancelled.
 */

const (
	serverEventRetention = 30 * 24 * time.Hour
	serverMetricsKeep    = 7 * 24 * time.Hour
)

type hostState struct {
	mu      sync.Mutex
	prev    map[string]hostinfo.Snapshot // keyed by server ID and source
	factsAt map[string]time.Time         // when hardware facts were last read per server
	probeOK map[string]bool              // last probe outcome, to log changes once
	removed map[string]bool              // probes already removed because the setting is off
}

func newHostState() *hostState {
	return &hostState{prev: map[string]hostinfo.Snapshot{}, factsAt: map[string]time.Time{}, probeOK: map[string]bool{}, removed: map[string]bool{}}
}

// rates fills the counters that need two snapshots from the same source.
func (hs *hostState) rates(key string, cur hostinfo.Snapshot, m *store.ServerMetricSample) {
	hs.mu.Lock()
	prev, had := hs.prev[key]
	hs.prev[key] = cur
	hs.mu.Unlock()
	if !had {
		return
	}
	if p, ok := hostinfo.CPUPercent(prev, cur); ok {
		m.CPUPercent = &p
	}
	if r, wr, ok := hostinfo.DiskRates(prev, cur); ok {
		m.DiskReadBps, m.DiskWriteBps = &r, &wr
	}
	if rx, tx, ok := hostinfo.NetRates(prev, cur); ok {
		m.NetRxBps, m.NetTxBps = &rx, &tx
	}
}

func (w *Worker) registry() *servers.Registry {
	w.regOnce.Do(func() {
		if w.Servers == nil {
			w.Servers = &servers.Registry{Store: w.Store, Secrets: w.Secrets, Local: w.Docker}
		}
	})
	return w.Servers
}

// ServerLoop checks every server on an interval and prunes old samples and events.
func (w *Worker) ServerLoop(ctx context.Context) {
	tick := time.NewTicker(servers.CheckInterval)
	defer tick.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	hs := newHostState()
	w.checkAll(ctx, hs)
	for {
		select {
		case <-ctx.Done():
			return
		case <-prune.C:
			w.Store.PruneServerMetrics(ctx, serverMetricsKeep)
			w.Store.PruneServerEvents(ctx, serverEventRetention)
		case <-tick.C:
			w.checkAll(ctx, hs)
		}
	}
}

func (w *Worker) checkAll(ctx context.Context, hs *hostState) {
	ids, err := w.Store.ServerIDs(ctx)
	if err != nil {
		return
	}
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, id := range ids {
		if ok, err := w.Store.ClaimServerCheck(ctx, id, servers.CheckInterval); err != nil || !ok {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			srv, err := w.Store.GetServer(ctx, id)
			if err != nil {
				return
			}
			o := w.checkServer(ctx, srv, "system")
			settings := servers.ParseSettings(srv.Settings)
			if settings.CollectMetrics {
				w.sampleServer(ctx, srv, o, hs)
			}
			if (!settings.CollectMetrics || !settings.HostProbe) && o.Reached {
				w.removeHostProbe(ctx, srv, hs)
			}
		}(id)
	}
	wg.Wait()
}

// checkServer probes one server and records the outcome (see servers.Check).
func (w *Worker) checkServer(ctx context.Context, srv *store.Server, actor string) servers.Observation {
	return servers.Check(ctx, w.registry(), w.Store, srv, actor)
}

func (w *Worker) sampleServer(ctx context.Context, srv *store.Server, o servers.Observation, hs *hostState) {
	if !o.Reached || o.Runtime != servers.RuntimeReady {
		return // no sample beats a sample of zeros
	}
	m := store.ServerMetricSample{ServerID: srv.ID, At: time.Now().UTC().Truncate(time.Second)}
	running := o.Info.Running
	m.ContainersRunning = &running
	if o.Info.MemTotal > 0 {
		m.MemoryTotal = &o.Info.MemTotal
	}
	var cur hostinfo.Snapshot
	source := ""
	if servers.ParseSettings(srv.Settings).HostProbe {
		if snap, err := w.readHostProbe(ctx, srv, hs); err == nil {
			cur, source = snap, "probe"
		} else if hs.noteProbe(srv.ID, false) {
			w.Store.AddServerEvent(ctx, srv.ID, "metrics", "warn", "Host probe didn't answer: "+err.Error(), "system", nil)
		}
	}
	if source == "" && srv.IsLocal && o.Procfs {
		cur, source = hostinfo.Read(), "procfs"
	}
	collector := source
	if collector == "" {
		collector = "engine"
	}
	if (collector == "probe") != o.HostProbe {
		probeObs := o
		probeObs.HostProbe = collector == "probe"
		w.Store.SetServerCollector(ctx, srv.ID, collector, servers.DeriveCapabilities(srv, probeObs)["metrics"])
	}
	if source != "" {
		hs.rates(srv.ID+"/"+source, cur, &m)
		if cur.LoadOK {
			m.Load1, m.Load5, m.Load15 = &cur.Load[0], &cur.Load[1], &cur.Load[2]
		}
		if cur.MemOK {
			used, swapUsed := cur.MemTotal-cur.MemAvail, cur.SwapTotal-cur.SwapFree
			m.MemoryUsed, m.MemoryTotal, m.SwapUsed, m.SwapTotal = &used, &cur.MemTotal, &swapUsed, &cur.SwapTotal
		}
		if cur.FSOK {
			m.DiskUsed, m.DiskTotal = &cur.FSUsed, &cur.FSTotal
		}
		if cur.UptimeOK {
			m.UptimeSeconds = &cur.Uptime
		}
		if cur.Facts.CPUModel != "" || cur.Facts.Cores > 0 {
			w.Store.SetServerHostFacts(ctx, srv.ID, cur.Facts)
		}
	}
	if cpu, mem, n, err := w.Store.WorkloadUsage(ctx, srv.ID); err == nil && n > 0 {
		m.WorkloadCPU, m.WorkloadMemory = &cpu, &mem
	}
	if err := w.Store.InsertServerMetric(ctx, m); err != nil {
		slog.Warn("store server metric failed", "server", srv.ID, "error", err)
	}
}

// noteProbe records the probe outcome and reports whether it changed from a success.
func (hs *hostState) noteProbe(id string, ok bool) bool {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	was, known := hs.probeOK[id]
	hs.probeOK[id] = ok
	if ok {
		delete(hs.removed, id)
	}
	return !ok && (!known || was)
}

// readHostProbe makes sure the probe container runs on the server and reads /proc through it.
// Hardware facts (cpuinfo) are only read once an hour.
func (w *Worker) readHostProbe(ctx context.Context, srv *store.Server, hs *hostState) (hostinfo.Snapshot, error) {
	d, err := w.registry().Docker(srv)
	if err != nil {
		return hostinfo.Snapshot{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second) // room for a first-time image pull
	defer cancel()
	id, err := d.EnsureHostProbe(ctx, w.Docker.Instance(ctx))
	if err != nil {
		return hostinfo.Snapshot{}, err
	}
	hs.mu.Lock()
	facts := time.Since(hs.factsAt[srv.ID]) > time.Hour
	hs.mu.Unlock()
	execCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	out, code, err := d.ExecCapture(execCtx, id, []string{"sh", "-c", runtime.HostProbeScript(facts)})
	if err != nil {
		return hostinfo.Snapshot{}, err
	}
	snap := hostinfo.Parse(runtime.SplitHostProbe(out))
	if !snap.CPUOK || !snap.MemOK {
		return hostinfo.Snapshot{}, fmt.Errorf("unexpected output (exit %d)", code)
	}
	hs.noteProbe(srv.ID, true)
	if facts {
		hs.mu.Lock()
		hs.factsAt[srv.ID] = time.Now()
		hs.mu.Unlock()
	}
	return snap, nil
}

// removeHostProbe takes the probe container down once after the setting is turned off.
func (w *Worker) removeHostProbe(ctx context.Context, srv *store.Server, hs *hostState) {
	hs.mu.Lock()
	done := hs.removed[srv.ID]
	hs.removed[srv.ID] = true
	delete(hs.probeOK, srv.ID)
	hs.mu.Unlock()
	if done {
		return
	}
	d, err := w.registry().Docker(srv)
	if err != nil {
		return
	}
	if err := d.RemoveHostProbe(ctx, w.Docker.Instance(ctx)); err != nil {
		slog.Warn("remove host probe failed", "server", srv.ID, "error", err)
	}
}

/* --------------------------------- Server jobs -------------------------------- */

func (w *Worker) serverJob(ctx context.Context, job *jobs.Job) error {
	if job.ServerID == nil {
		return fmt.Errorf("%w: server job without server", jobs.ErrPermanent)
	}
	var p servers.Payload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return fmt.Errorf("%w: bad payload", jobs.ErrPermanent)
	}
	if p.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(p.TimeoutSeconds)*time.Second)
		defer cancel()
	}
	srv, err := w.Store.GetServer(ctx, *job.ServerID)
	if err != nil {
		return fmt.Errorf("%w: server no longer exists", jobs.ErrPermanent)
	}
	switch p.Op {
	case "refresh":
		return w.opRefresh(ctx, job, srv, p)
	case "diagnostics":
		return w.opDiagnostics(ctx, job, srv, p.RunID, nil)
	case "cleanup":
		return w.opCleanup(ctx, job, srv, p.Cleanup, p.Items, p.Actor)
	case "task":
		return w.opTask(ctx, job, srv, p)
	}
	return fmt.Errorf("%w: unknown server operation %q", jobs.ErrPermanent, p.Op)
}

func (w *Worker) opRefresh(ctx context.Context, job *jobs.Job, srv *store.Server, p servers.Payload) error {
	w.Queue.Progress(ctx, job.ID, "Checking connection")
	o := w.checkServer(ctx, srv, p.Actor)
	result := map[string]any{"reached": o.Reached, "runtime": o.Runtime, "latencyMs": o.Latency.Milliseconds(), "error": o.Error}
	if o.HasInfo {
		result["docker"] = o.Info.Version
	}
	w.Store.SetJobResult(ctx, job.ID, result)
	if !o.Reached {
		return fmt.Errorf("%w: %s", jobs.ErrPermanent, o.Error)
	}
	if srv.IsLocal {
		// Refreshing runtime state also reconciles each workload's recorded status with Docker.
		w.Queue.Progress(ctx, job.ID, "Reconciling workload status")
		list, _ := w.Store.ListServerResources(ctx, srv.ID)
		for _, r := range list {
			id := r.ID
			w.refreshStatus(ctx, &id)
		}
		result["reconciled"] = len(list)
		w.Store.SetJobResult(ctx, job.ID, result)
	}
	if o.Runtime != servers.RuntimeReady {
		return fmt.Errorf("%w: reached the server, but Docker is %s: %s", jobs.ErrPermanent, strings.ReplaceAll(o.Runtime, "_", " "), o.Error)
	}
	return nil
}

func (w *Worker) opDiagnostics(ctx context.Context, job *jobs.Job, srv *store.Server, runID string, only []string) error {
	run, err := w.Store.GetDiagnosticRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("%w: diagnostic run missing", jobs.ErrPermanent)
	}
	checks := run.Checks
	if only != nil {
		checks = only
	}
	w.Store.SetDiagnosticRunStatus(ctx, runID, "running")
	d := &servers.Diagnoser{Registry: w.registry(), Store: w.Store, Server: srv, Verbose: servers.ParseSettings(srv.Settings).DiagnosticOutput == "verbose"}
	counts := map[string]int{}
	for i, c := range checks {
		if ctx.Err() != nil {
			w.Store.SetDiagnosticRunStatus(context.WithoutCancel(ctx), runID, "cancelled")
			return errCancelled
		}
		w.Queue.Progress(ctx, job.ID, fmt.Sprintf("%d of %d: %s", i+1, len(checks), c))
		res := d.Run(ctx, c)
		counts[res.Status]++
		w.Store.SaveDiagnosticResult(ctx, runID, res)
	}
	w.Store.SetDiagnosticRunStatus(ctx, runID, "finished")
	w.Store.SetJobResult(ctx, job.ID, counts)
	level := "info"
	if counts["failed"] > 0 {
		level = "warn"
	}
	w.Store.AddServerEvent(ctx, srv.ID, "diagnostics", level,
		fmt.Sprintf("Diagnostics: %d passed, %d warnings, %d failed", counts["passed"], counts["warning"], counts["failed"]), run.RequestedBy, map[string]any{"runId": runID})
	return nil
}

func (w *Worker) opCleanup(ctx context.Context, job *jobs.Job, srv *store.Server, o servers.CleanupOptions, items []servers.CleanupItem, actor string) error {
	dk, err := w.registry().Docker(srv)
	if err != nil {
		return fmt.Errorf("%w: %v", jobs.ErrPermanent, err)
	}
	out, err := servers.ExecuteCleanup(ctx, dk, w.Store, srv, o, items, func(s string) { w.Queue.Progress(ctx, job.ID, s) })
	if err != nil {
		return err
	}
	for _, it := range out.Removed {
		w.Queue.Log(ctx, job.ID, "info", "cleanup", fmt.Sprintf("Removed %s %s", it.Kind, it.Name))
	}
	for _, it := range out.Failed {
		w.Queue.Log(ctx, job.ID, "warn", "cleanup", fmt.Sprintf("Couldn't remove %s %s: %s", it.Kind, it.Name, it.Reason))
	}
	for _, it := range out.Skipped {
		w.Queue.Log(ctx, job.ID, "info", "cleanup", fmt.Sprintf("Skipped %s %s: %s", it.Kind, it.Name, it.Reason))
	}
	w.Store.SetJobResult(ctx, job.ID, out)
	level := "info"
	if len(out.Failed) > 0 {
		level = "warn"
	}
	w.Store.AddServerEvent(ctx, srv.ID, "operation", level,
		fmt.Sprintf("Docker cleanup: removed %d, failed %d, skipped %d, reclaimed %s", len(out.Removed), len(out.Failed), len(out.Skipped), servers.BytesText(out.Reclaimed)),
		actor, map[string]any{"jobId": job.ID})
	return nil
}

// opTask runs one scheduled server task. Kinds are fixed and parameter-checked; none runs commands.
func (w *Worker) opTask(ctx context.Context, job *jobs.Job, srv *store.Server, p servers.Payload) error {
	w.Store.FinishServerTaskRun(ctx, p.TaskRunID, "running", "")
	paused := srv.Maintenance && p.TaskKind != "diagnostics" && p.TaskKind != "maintenance_check" && p.TaskKind != "disk_check"
	if paused {
		w.Store.FinishServerTaskRun(ctx, p.TaskRunID, "skipped", "Skipped: the server is in maintenance mode.")
		return nil
	}
	summary, err := w.runTaskKind(ctx, job, srv, p)
	if err != nil {
		return err
	}
	w.Store.SetJobResult(ctx, job.ID, map[string]string{"summary": summary})
	w.Store.FinishServerTaskRun(ctx, p.TaskRunID, "succeeded", summary)
	return nil
}

func (w *Worker) runTaskKind(ctx context.Context, job *jobs.Job, srv *store.Server, p servers.Payload) (string, error) {
	switch p.TaskKind {
	case "diagnostics", "disk_check":
		checks := servers.CheckIDs()
		if p.TaskKind == "disk_check" {
			checks = []string{"disk"}
		}
		tx, err := w.Store.DB.Begin(ctx)
		if err != nil {
			return "", err
		}
		runID, err := w.Store.CreateDiagnosticRun(ctx, tx, srv.ID, checks, "scheduled task")
		if err != nil {
			tx.Rollback(ctx)
			return "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		if err := w.opDiagnostics(ctx, job, srv, runID, nil); err != nil {
			return "", err
		}
		run, _ := w.Store.GetDiagnosticRun(ctx, runID)
		failed := 0
		for _, r := range run.Results {
			if r.Status == "failed" {
				failed++
			}
		}
		if failed > 0 {
			return "", fmt.Errorf("%w: %d check(s) failed — see Diagnostics", jobs.ErrPermanent, failed)
		}
		return fmt.Sprintf("%d checks ran, none failed.", len(run.Results)), nil
	case "docker_cleanup":
		var o servers.CleanupOptions
		b, _ := json.Marshal(p.Options)
		json.Unmarshal(b, &o)
		if !o.Any() {
			o = servers.CleanupOptions{Containers: true, Dangling: true, BuildCache: true}
		}
		o.UnusedImages = false // scheduled cleanup never removes tagged images; do that by hand with a preview
		dk, err := w.registry().Docker(srv)
		if err != nil {
			return "", fmt.Errorf("%w: %v", jobs.ErrPermanent, err)
		}
		plan, err := servers.PlanCleanup(ctx, dk, w.Store, srv, o)
		if err != nil {
			return "", err
		}
		if err := w.opCleanup(ctx, job, srv, o, plan.Items, "scheduled task"); err != nil {
			return "", err
		}
		return fmt.Sprintf("Cleanup ran on %d eligible object(s).", len(plan.Items)), nil
	case "reconcile":
		o := w.checkServer(ctx, srv, "scheduled task")
		if !o.Reached {
			return "", fmt.Errorf("%w: %s", jobs.ErrPermanent, o.Error)
		}
		list, _ := w.Store.ListServerResources(ctx, srv.ID)
		for _, r := range list {
			id := r.ID
			w.refreshStatus(ctx, &id)
		}
		return fmt.Sprintf("Connection %s; %d workload status(es) reconciled with Docker.", map[bool]string{true: "ok", false: "degraded"}[o.Runtime == servers.RuntimeReady], len(list)), nil
	case "maintenance_check":
		act, err := w.Store.ServerActivity(ctx, srv.ID)
		if err != nil {
			return "", err
		}
		msg := fmt.Sprintf("%d deployment(s), %d server job(s) and %d task run(s) active.", len(act.Deployments), max(len(act.Jobs)-1, 0), act.TaskRuns)
		if srv.Maintenance && srv.MaintenanceSince != nil {
			hours := 24.0
			if v, ok := p.Options["maxHours"].(float64); ok && v > 0 {
				hours = v
			}
			if time.Since(*srv.MaintenanceSince) > time.Duration(hours*float64(time.Hour)) {
				w.Store.AddServerEvent(ctx, srv.ID, "maintenance", "warn", fmt.Sprintf("Maintenance mode has been on for over %.0f hours.", hours), "scheduled task", nil)
				return "", fmt.Errorf("%w: maintenance mode has been on since %s. %s", jobs.ErrPermanent, srv.MaintenanceSince.Format(time.RFC3339), msg)
			}
			return "In maintenance (within the allowed time). " + msg, nil
		}
		return "Not in maintenance. " + msg, nil
	}
	return "", fmt.Errorf("%w: unknown task kind %q", jobs.ErrPermanent, p.TaskKind)
}

// serverJobEnded is called by execute when a server job finishes for good without success.
func (w *Worker) serverJobEnded(ctx context.Context, job *jobs.Job, status string, cause error) {
	var p servers.Payload
	if json.Unmarshal(job.Payload, &p) != nil {
		return
	}
	msg := ""
	if cause != nil {
		msg = strings.TrimPrefix(cause.Error(), jobs.ErrPermanent.Error()+": ")
	}
	if p.TaskRunID != "" {
		w.Store.FinishServerTaskRun(ctx, p.TaskRunID, status, msg)
	}
	if p.RunID != "" {
		w.Store.SetDiagnosticRunStatus(ctx, p.RunID, map[string]string{"failed": "failed", "cancelled": "cancelled"}[status])
	}
}

/* ------------------------------- Task scheduler ------------------------------- */

// ServerTaskLoop turns due server tasks into queued jobs. Rows are locked while scheduling, so
// several workers never queue the same run, and the scheduled minute is unique per task.
func (w *Worker) ServerTaskLoop(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if err := w.scheduleServerTasks(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("server task scheduling failed", "error", err)
		}
	}
}

func (w *Worker) scheduleServerTasks(ctx context.Context) error {
	tx, err := w.Store.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	due, err := w.Store.DueServerTasks(ctx, tx)
	if err != nil || len(due) == 0 {
		return err
	}
	now := time.Now()
	for _, t := range due {
		scheduledFor := t.NextRunAt.UTC().Truncate(time.Minute)
		next, _ := NextRun(t.Schedule, t.Timezone, now)
		if _, err := tx.Exec(ctx, `UPDATE server_tasks SET next_run_at = $2 WHERE id = $1`, t.ID, next); err != nil {
			return err
		}
		if _, _, err := servers.EnqueueTask(ctx, w.Store, tx, &t, "schedule", &scheduledFor, "", "scheduler"); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

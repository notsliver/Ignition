package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/notsliver/Ignition/services/internal/automations"
	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/servers"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * The automation engine. Every few seconds it:
 *   1. turns due schedules into runs;
 *   2. evaluates event and threshold triggers per target (a condition must hold for its duration,
 *      and a target that fired stays quiet for the cooldown);
 *   3. expires approvals nobody decided on;
 *   4. claims queued and approved runs and works them: plan → (approval) → steps → verification.
 *
 * A step only counts as done when its outcome is verified (a container running and healthy, a
 * deployment serving, objects actually removed), not when a command returned. Repeated failures
 * pause the automation and flag it for a person instead of trying again and again.
 */

// target is something an automation acts on.
type target struct {
	Kind     string `json:"kind"` // server, resource
	ID       string `json:"id"`
	Name     string `json:"name"`
	ServerID string `json:"serverId,omitempty"`
}

type parsed struct {
	row  *store.Automation
	spec automations.Automation
}

func parseAutomation(a *store.Automation) parsed {
	p := parsed{row: a, spec: automations.Automation{Name: a.Name, Scope: a.Scope, Mode: a.Mode, EscalateAfter: a.EscalateAfter, MaxRunsPerHour: a.MaxRunsPerHour}}
	json.Unmarshal(a.Trigger, &p.spec.Trigger)
	json.Unmarshal(a.Conditions, &p.spec.Conditions)
	json.Unmarshal(a.Targets, &p.spec.Targets)
	json.Unmarshal(a.Steps, &p.spec.Steps)
	return p
}

// ParseSpec reads a stored automation into the shared model (for the API's previews).
func ParseSpec(a *store.Automation) automations.Automation { return parseAutomation(a).spec }

func (w *Worker) AutomationLoop(ctx context.Context) {
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	slots := make(chan struct{}, 3)
	lastEval := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		w.scheduleAutomations(ctx)
		if time.Since(lastEval) >= 10*time.Second {
			w.evaluateTriggers(ctx)
			w.Store.DB.Exec(ctx, `UPDATE automation_runs SET status = 'expired', finished_at = now(), summary = 'Nobody approved it within 24 hours, so it wasn''t run.'
				WHERE status = 'awaiting_approval' AND expires_at < now()`)
			lastEval = time.Now()
		}
		for len(slots) < cap(slots) {
			id := w.claimAutomationRun(ctx)
			if id == "" {
				break
			}
			slots <- struct{}{}
			wg.Add(1)
			go func() {
				defer func() { <-slots; wg.Done() }()
				w.workRun(context.WithoutCancel(ctx), id)
			}()
		}
	}
}

func (w *Worker) scheduleAutomations(ctx context.Context) {
	tx, err := w.Store.DB.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id, trigger FROM automations WHERE enabled AND next_run_at <= now() FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return
	}
	type due struct {
		id string
		t  automations.Trigger
	}
	var list []due
	for rows.Next() {
		var d due
		var raw []byte
		if rows.Scan(&d.id, &raw) == nil && json.Unmarshal(raw, &d.t) == nil {
			list = append(list, d)
		}
	}
	rows.Close()
	for _, d := range list {
		next, _ := NextRun(d.t.Schedule, d.t.Timezone, time.Now())
		tx.Exec(ctx, `UPDATE automations SET next_run_at = $2 WHERE id = $1`, d.id, next)
	}
	if tx.Commit(ctx) != nil {
		return
	}
	for _, d := range list {
		w.Store.CreateAutomationRun(ctx, d.id, "schedule", "Scheduled ("+d.t.Schedule+")", "scheduler", nil)
	}
}

// resolveTargets lists what an automation acts on right now.
func (w *Worker) resolveTargets(ctx context.Context, p parsed) ([]target, map[string]*store.Server, error) {
	srvs := map[string]*store.Server{}
	if p.row.Scope == automations.ScopeResource {
		res, err := w.Store.GetResource(ctx, *p.row.ResourceID)
		if err != nil {
			return nil, nil, err
		}
		if srv, err := w.Store.GetServer(ctx, res.ServerID); err == nil {
			srvs[srv.ID] = srv
		}
		return []target{{Kind: "resource", ID: res.ID, Name: res.Name, ServerID: res.ServerID}}, srvs, nil
	}
	list, _, err := w.Store.ListServers(ctx, store.ServerQuery{Limit: 1000, Sort: "name"})
	if err != nil {
		return nil, nil, err
	}
	tg := p.spec.Targets
	var out []target
	for i := range list {
		s := &list[i]
		if s.Removing {
			continue
		}
		match := tg.Matches(s.ID, s.Environment, s.Labels)
		if match {
			srvs[s.ID] = s
			out = append(out, target{Kind: "server", ID: s.ID, Name: s.Name, ServerID: s.ID})
		}
	}
	return out, srvs, nil
}

/* ------------------------------ Event triggers ------------------------------ */

func (w *Worker) evaluateTriggers(ctx context.Context) {
	rows, err := w.Store.DB.Query(ctx, `SELECT id FROM automations WHERE enabled AND trigger->>'type' IN ('event', 'metric')`)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		a, err := w.Store.GetAutomation(ctx, id)
		if err != nil {
			continue
		}
		p := parseAutomation(a)
		targets, srvs, err := w.resolveTargets(ctx, p)
		if err != nil {
			continue
		}
		for _, t := range targets {
			holding, reason := w.conditionHolds(ctx, p, t, srvs[t.ServerID])
			w.advanceState(ctx, p, t, holding, reason)
		}
	}
}

// conditionHolds reports whether the trigger's condition is true for the target now, and why.
func (w *Worker) conditionHolds(ctx context.Context, p parsed, t target, srv *store.Server) (bool, string) {
	tr := p.spec.Trigger
	if tr.Type == "metric" {
		v, ok := w.metricValue(ctx, tr.Metric, t, srv)
		if !ok {
			return false, ""
		}
		return v > tr.Above, fmt.Sprintf("%s %s at %.0f%% (above %.0f%% for %d min)", t.Name, metricLabel[tr.Metric], v, tr.Above, tr.ForMinutes)
	}
	switch tr.Event {
	case "server.offline":
		return srv != nil && srv.EffectiveState == servers.StateOffline, fmt.Sprintf("%s went offline: %s", t.Name, srvErr(srv))
	case "server.degraded":
		return srv != nil && srv.EffectiveState == servers.StateDegraded, fmt.Sprintf("%s is degraded: %s", t.Name, srvErr(srv))
	case "resource.unhealthy":
		cfg, st, err := w.Store.GetHealth(ctx, t.ID)
		return err == nil && cfg.Enabled && st.State == "unhealthy", fmt.Sprintf("%s failed %d health checks in a row", t.Name, st.Failures)
	case "resource.failed":
		res, err := w.Store.GetResource(ctx, t.ID)
		return err == nil && res.Status == "failed" && res.DesiredState == "running", t.Name + " stopped unexpectedly"
	case "deployment.failed", "deployment.succeeded":
		// Edge-triggered: a deployment that finished after the automation last fired (or was created).
		want := strings.TrimPrefix(tr.Event, "deployment.")
		if want == "succeeded" {
			want = "successful"
		}
		var number int
		var finished time.Time
		var errText string
		err := w.Store.DB.QueryRow(ctx, `
			SELECT d.number, d.finished_at, d.error FROM deployments d
			LEFT JOIN automation_state s ON s.automation_id = $2 AND s.target_id = d.resource_id
			WHERE d.resource_id = $1 AND d.status = $3 AND d.finished_at > GREATEST(COALESCE(s.last_fired_at, '-infinity'), $4)
			ORDER BY d.finished_at DESC LIMIT 1`, t.ID, p.row.ID, want, p.row.CreatedAt).Scan(&number, &finished, &errText)
		if err != nil {
			return false, ""
		}
		if want == "failed" {
			return true, fmt.Sprintf("Deployment #%d of %s failed: %s", number, t.Name, truncate(errText, 160))
		}
		return true, fmt.Sprintf("Deployment #%d of %s succeeded", number, t.Name)
	}
	return false, ""
}

var metricLabel = map[string]string{"cpu": "CPU", "memory": "memory", "disk": "disk"}

func srvErr(s *store.Server) string {
	if s == nil || s.LastError == "" {
		return "no details"
	}
	return truncate(s.LastError, 160)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// metricValue is the current value in percent: servers from the newest fresh host sample,
// resources averaged over the last two minutes (of their CPU and memory limits).
func (w *Worker) metricValue(ctx context.Context, metric string, t target, srv *store.Server) (float64, bool) {
	if t.Kind == "server" {
		if srv == nil || srv.Metrics == nil {
			return 0, false
		}
		m := srv.Metrics
		switch metric {
		case "cpu":
			if m.CPUPercent != nil {
				return *m.CPUPercent, true
			}
		case "memory":
			if m.MemoryUsed != nil && m.MemoryTotal != nil && *m.MemoryTotal > 0 {
				return float64(*m.MemoryUsed) / float64(*m.MemoryTotal) * 100, true
			}
		case "disk":
			if m.DiskUsed != nil && m.DiskTotal != nil && *m.DiskTotal > 0 {
				return float64(*m.DiskUsed) / float64(*m.DiskTotal) * 100, true
			}
		}
		return 0, false
	}
	var v *float64
	q := `SELECT avg(m.memory_bytes::float8 / NULLIF(m.memory_limit, 0) * 100) FROM metric_samples m WHERE m.resource_id = $1 AND m.at > now() - interval '2 minutes'`
	if metric == "cpu" {
		q = `SELECT avg(m.cpu_percent / NULLIF(r.cpus::float8, 0)) FROM metric_samples m JOIN resources r ON r.id = m.resource_id WHERE m.resource_id = $1 AND m.at > now() - interval '2 minutes'`
	}
	if w.Store.DB.QueryRow(ctx, q, t.ID).Scan(&v) != nil || v == nil {
		return 0, false
	}
	return *v, true
}

// advanceState tracks how long a condition has held and fires once it held long enough,
// unless the target fired within the cooldown.
func (w *Worker) advanceState(ctx context.Context, p parsed, t target, holding bool, reason string) {
	var since, fired *time.Time
	w.Store.DB.QueryRow(ctx, `SELECT holding_since, last_fired_at FROM automation_state WHERE automation_id = $1 AND target_id = $2`, p.row.ID, t.ID).Scan(&since, &fired)
	if !holding {
		if since != nil {
			w.Store.DB.Exec(ctx, `UPDATE automation_state SET holding_since = NULL WHERE automation_id = $1 AND target_id = $2`, p.row.ID, t.ID)
		}
		return
	}
	now := time.Now()
	if since == nil {
		since = &now
		w.Store.DB.Exec(ctx, `INSERT INTO automation_state (automation_id, target_id, holding_since) VALUES ($1, $2, now())
			ON CONFLICT (automation_id, target_id) DO UPDATE SET holding_since = COALESCE(automation_state.holding_since, now())`, p.row.ID, t.ID)
	}
	need := time.Duration(p.spec.Trigger.ForMinutes) * time.Minute
	if now.Sub(*since) < need {
		return
	}
	if fired != nil && now.Sub(*fired) < time.Duration(p.spec.Conditions.CooldownMinutes)*time.Minute {
		return
	}
	w.Store.DB.Exec(ctx, `UPDATE automation_state SET last_fired_at = now() WHERE automation_id = $1 AND target_id = $2`, p.row.ID, t.ID)
	w.Store.CreateAutomationRun(ctx, p.row.ID, p.spec.Trigger.Type, reason, "trigger", []target{t})
}

/* ----------------------------------- Runs ----------------------------------- */

// claimAutomationRun takes the oldest run that can be worked now. Runs whose automation has a
// maintenance window wait for it (an approved plan included).
func (w *Worker) claimAutomationRun(ctx context.Context) string {
	rows, err := w.Store.DB.Query(ctx, `
		SELECT r.id, a.conditions FROM automation_runs r JOIN automations a ON a.id = r.automation_id
		WHERE r.status IN ('queued', 'approved') ORDER BY r.created_at LIMIT 20`)
	if err != nil {
		return ""
	}
	type cand struct {
		id   string
		cond automations.Conditions
		mode string
	}
	var list []cand
	for rows.Next() {
		var c cand
		var raw []byte
		if rows.Scan(&c.id, &raw) == nil {
			json.Unmarshal(raw, &c.cond)
			list = append(list, c)
		}
	}
	rows.Close()
	for _, c := range list {
		var status, mode string
		w.Store.DB.QueryRow(ctx, `SELECT r.status, a.mode FROM automation_runs r JOIN automations a ON a.id = r.automation_id WHERE r.id = $1`, c.id).Scan(&status, &mode)
		// Planning and observing don't change anything, so they don't wait for the window.
		executes := status == "approved" || mode == automations.ModeAuto
		if executes && !automations.InWindow(c.cond.Window, time.Now()) {
			continue
		}
		var id string
		w.Store.DB.QueryRow(ctx, `UPDATE automation_runs SET status = 'running', started_at = COALESCE(started_at, now())
			WHERE id = $1 AND status IN ('queued', 'approved') RETURNING id`, c.id).Scan(&id)
		if id != "" {
			return id
		}
	}
	return ""
}

type planStep struct {
	Action   string         `json:"action"`
	Label    string         `json:"label"`
	Does     string         `json:"does"`
	Verifies string         `json:"verifies"`
	Risk     string         `json:"risk"`
	ReadOnly bool           `json:"readOnly"`
	Params   map[string]any `json:"params,omitempty"`
}

type planTarget struct {
	Target  target     `json:"target"`
	Steps   []planStep `json:"steps"`
	Skipped string     `json:"skipped,omitempty"`
}

func (w *Worker) workRun(ctx context.Context, runID string) {
	run, err := w.Store.GetAutomationRun(ctx, runID)
	if err != nil {
		return
	}
	a, err := w.Store.GetAutomation(ctx, run.AutomationID)
	if err != nil {
		return
	}
	p := parseAutomation(a)
	approved := run.DecidedBy != "" && run.DecidedAt != nil

	// The plan: who, what, and what's deliberately left out. An approved run keeps its plan.
	var plan []planTarget
	if approved {
		json.Unmarshal(run.Plan, &plan)
	} else {
		plan, err = w.makePlan(ctx, p, run)
		if err != nil {
			w.finishAutomationRun(ctx, p, runID, "failed", "Couldn't plan: "+err.Error(), true)
			return
		}
		b, _ := json.Marshal(plan)
		w.Store.DB.Exec(ctx, `UPDATE automation_runs SET plan = $2 WHERE id = $1`, runID, b)
	}
	active := 0
	for _, t := range plan {
		if t.Skipped == "" {
			active++
		}
	}
	if active == 0 {
		w.finishAutomationRun(ctx, p, runID, "skipped", "Nothing to act on: "+skippedText(plan), false)
		return
	}

	switch {
	case !approved && p.spec.Mode == automations.ModeAssisted:
		w.Store.DB.Exec(ctx, `UPDATE automation_runs SET status = 'awaiting_approval', expires_at = now() + $2::interval, started_at = NULL WHERE id = $1`,
			runID, fmt.Sprintf("%d seconds", int(automations.ApprovalTTL.Seconds())))
		w.notify(ctx, a, "approval", fmt.Sprintf("%s needs approval: %s", a.Name, run.Reason), runID)
		return
	case p.spec.Mode == automations.ModeObserve:
		w.executePlan(ctx, p, run, plan, true)
		return
	}
	w.executePlan(ctx, p, run, plan, false)
}

func (w *Worker) makePlan(ctx context.Context, p parsed, run *store.AutomationRun) ([]planTarget, error) {
	all, srvs, err := w.resolveTargets(ctx, p)
	if err != nil {
		return nil, err
	}
	// A trigger that fired for specific targets acts only on those.
	var fired []target
	json.Unmarshal(run.Targets, &fired)
	if len(fired) > 0 {
		keep := map[string]bool{}
		for _, t := range fired {
			keep[t.ID] = true
		}
		var only []target
		for _, t := range all {
			if keep[t.ID] {
				only = append(only, t)
			}
		}
		all = only
	}
	var plan []planTarget
	for _, t := range all {
		pt := planTarget{Target: t}
		srv := srvs[t.ServerID]
		switch {
		case p.spec.Conditions.SkipInMaintenance && srv != nil && srv.Maintenance:
			pt.Skipped = srv.Name + " is in maintenance"
		case t.Kind == "server" && srv != nil && srv.EffectiveState != servers.StateOnline && srv.EffectiveState != servers.StateDegraded && !onlyReconnect(p.spec.Steps):
			pt.Skipped = fmt.Sprintf("%s is %s; only reconnecting can run against it", t.Name, srv.EffectiveState)
		}
		for _, s := range p.spec.Steps {
			act := automations.ActionByID(s.Action)
			if act == nil {
				continue
			}
			pt.Steps = append(pt.Steps, planStep{Action: s.Action, Label: act.Label, Does: act.Does, Verifies: act.Verifies, Risk: act.Risk, ReadOnly: act.ReadOnly, Params: s.Params})
		}
		plan = append(plan, pt)
	}
	if len(plan) == 0 {
		return nil, errors.New("no target matches (check the target selection)")
	}
	return plan, nil
}

func onlyReconnect(steps []automations.Step) bool {
	for _, s := range steps {
		if s.Action == "refresh" || s.Action == "diagnostics" || s.Action == "notify" {
			return true
		}
	}
	return false
}

func skippedText(plan []planTarget) string {
	var why []string
	for _, t := range plan {
		if t.Skipped != "" {
			why = append(why, t.Skipped)
		}
	}
	return strings.Join(why, "; ")
}

// executePlan works the plan target by target (a staged rollout). In observe mode only read-only
// steps run, to gather evidence; the rest are recorded as recommendations.
func (w *Worker) executePlan(ctx context.Context, p parsed, run *store.AutomationRun, plan []planTarget, observe bool) {
	w.Store.DB.Exec(ctx, `UPDATE automation_runs SET status = 'running', started_at = COALESCE(started_at, now()) WHERE id = $1`, run.ID)
	seq := 0
	failedTargets, doneTargets, recommended := 0, 0, 0
	var lastError string
	var summaryParts []string
	stopRollout := false
	for _, pt := range plan {
		if pt.Skipped != "" {
			seq++
			w.Store.DB.Exec(ctx, `INSERT INTO automation_run_steps (run_id, seq, target_kind, target_id, target_name, action, status, output, finished_at)
				VALUES ($1, $2, $3, $4, $5, 'skip', 'skipped', $6, now())`, run.ID, seq, pt.Target.Kind, pt.Target.ID, pt.Target.Name, "Left out: "+pt.Skipped)
			continue
		}
		targetFailed, blocked := false, false
		for i, ps := range pt.Steps {
			seq++
			step := p.spec.Steps[min(i, len(p.spec.Steps)-1)]
			if stopRollout || blocked {
				w.Store.DB.Exec(ctx, `INSERT INTO automation_run_steps (run_id, seq, target_kind, target_id, target_name, action, status, output, finished_at)
					VALUES ($1, $2, $3, $4, $5, $6, 'skipped', $7, now())`, run.ID, seq, pt.Target.Kind, pt.Target.ID, pt.Target.Name, ps.Action,
					map[bool]string{true: "Not run: an earlier target failed and the rollout stops at the first failure.", false: "Not run: an earlier step failed."}[stopRollout])
				continue
			}
			if observe && !ps.ReadOnly {
				recommended++
				w.Store.DB.Exec(ctx, `INSERT INTO automation_run_steps (run_id, seq, target_kind, target_id, target_name, action, status, output, finished_at)
					VALUES ($1, $2, $3, $4, $5, $6, 'skipped', $7, now())`, run.ID, seq, pt.Target.Kind, pt.Target.ID, pt.Target.Name, ps.Action,
					"Recommended, not run (observe mode): "+ps.Does)
				continue
			}
			w.Store.DB.Exec(ctx, `INSERT INTO automation_run_steps (run_id, seq, target_kind, target_id, target_name, action, status, started_at)
				VALUES ($1, $2, $3, $4, $5, $6, 'running', now())`, run.ID, seq, pt.Target.Kind, pt.Target.ID, pt.Target.Name, ps.Action)
			out := w.runStep(ctx, p, run, pt.Target, step)
			status := "succeeded"
			if out.err != nil || (out.verified != nil && !*out.verified) {
				status = "failed"
				targetFailed = true
				blocked = step.OnFailure != "continue"
				lastError = fmt.Sprintf("%s on %s: %s", ps.Label, pt.Target.Name, firstNonEmpty(errText(out.err), out.verification))
			}
			output := out.output
			if out.err != nil {
				output = strings.TrimSpace(output + "\n" + errText(out.err))
			}
			w.Store.DB.Exec(ctx, `UPDATE automation_run_steps SET status = $3, output = $4, verification = $5, verified = $6, ref = $7, finished_at = now() WHERE run_id = $1 AND seq = $2`,
				run.ID, seq, status, truncate(output, 64<<10), out.verification, out.verified, out.ref)
		}
		if targetFailed {
			failedTargets++
			if p.spec.Targets.StopOnFailure {
				stopRollout = true
			}
		} else {
			doneTargets++
		}
	}

	switch {
	case failedTargets > 0:
		summaryParts = append(summaryParts, fmt.Sprintf("Failed on %d of %d target(s). %s", failedTargets, failedTargets+doneTargets, lastError))
		w.finishAutomationRun(ctx, p, run.ID, "failed", strings.Join(summaryParts, " "), true)
	case observe && recommended == 0:
		w.finishAutomationRun(ctx, p, run.ID, "succeeded", fmt.Sprintf("Checked %d target(s); every step was read-only, so there's nothing to recommend.", doneTargets), false)
	case observe:
		msg := fmt.Sprintf("%d action(s) recommended on %d target(s). Switch to assisted or automatic to act on them.", recommended, doneTargets)
		w.finishAutomationRun(ctx, p, run.ID, "recommended", msg, true)
	default:
		w.finishAutomationRun(ctx, p, run.ID, "succeeded", fmt.Sprintf("Done and verified on %d target(s).", doneTargets), false)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimPrefix(err.Error(), jobs.ErrPermanent.Error()+": ")
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// finishAutomationRun records the outcome and applies escalation: after escalate_after failed runs in a row
// the automation pauses itself and asks for a person.
func (w *Worker) finishAutomationRun(ctx context.Context, p parsed, runID, status, summary string, attention bool) {
	w.Store.DB.Exec(ctx, `UPDATE automation_runs SET status = $2, summary = $3, attention = $4, finished_at = now() WHERE id = $1`, runID, status, summary, attention)
	a := p.row
	switch status {
	case "succeeded":
		w.Store.DB.Exec(ctx, `UPDATE automations SET failures = 0 WHERE id = $1`, a.ID)
	case "failed":
		var failures int
		var enabled bool
		w.Store.DB.QueryRow(ctx, `UPDATE automations SET failures = failures + 1 WHERE id = $1 RETURNING failures, enabled`, a.ID).Scan(&failures, &enabled)
		if enabled && failures >= a.EscalateAfter {
			runs := "runs"
			if failures == 1 {
				runs = "run"
			}
			reason := fmt.Sprintf("Paused after %d failed %s in a row, so it doesn't keep trying something that isn't working. Last: %s", failures, runs, truncate(summary, 300))
			w.Store.DB.Exec(ctx, `UPDATE automations SET enabled = false, paused_reason = $2 WHERE id = $1`, a.ID, reason)
			w.notify(ctx, a, "escalation", a.Name+": "+reason, runID)
			slog.Warn("automation paused after repeated failures", "automation", a.Name, "failures", failures)
			return
		}
		w.notify(ctx, a, "failure", fmt.Sprintf("%s failed: %s", a.Name, truncate(summary, 300)), runID)
	case "recommended":
		if attention {
			w.notify(ctx, a, "recommendation", fmt.Sprintf("%s recommends action: %s", a.Name, summary), runID)
		}
	}
}

// notify posts to the automation's webhook. Slack and Discord read "text"/"content".
func (w *Worker) notify(ctx context.Context, a *store.Automation, kind, text, runID string) error {
	if len(a.NotifySealed) == 0 {
		return errors.New("this automation has no notification webhook")
	}
	url, err := w.Secrets.Open(a.NotifySealed, "automation:"+a.ID)
	if err != nil {
		return errors.New("the webhook URL can't be decrypted (was IGNITION_SECRET_KEY changed?)")
	}
	body, _ := json.Marshal(map[string]any{"text": text, "content": text, "kind": kind, "automation": a.Name, "automationId": a.ID, "runId": runID, "at": time.Now().UTC()})
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, "POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Ignition-Automations/1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("the webhook didn't answer: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("the webhook answered %s", resp.Status)
	}
	return nil
}

/* ----------------------------------- Steps ----------------------------------- */

type stepOutcome struct {
	output, verification, ref string
	verified                  *bool
	err                       error
}

func verdict(ok bool, text string) (*bool, string) { return &ok, text }

func (w *Worker) runStep(ctx context.Context, p parsed, run *store.AutomationRun, t target, s automations.Step) (o stepOutcome) {
	defer func() {
		if r := recover(); r != nil {
			o.err = fmt.Errorf("internal error: %v", r)
		}
	}()
	actor := "automation: " + p.row.Name
	switch s.Action {
	case "diagnostics":
		return w.stepDiagnostics(ctx, t, actor)
	case "refresh":
		return w.stepServerJob(ctx, t, servers.Payload{Op: "refresh", Actor: actor, TimeoutSeconds: 60}, func(job *store.ServerJob) (bool, string) {
			return job.Status == "succeeded", "Reached the server and Docker is ready."
		})
	case "docker_cleanup":
		return w.stepCleanup(ctx, t, s, actor)
	case "disk_report":
		return w.stepDiskReport(ctx, t, s)
	case "restart":
		return w.stepRestart(ctx, t, s)
	case "redeploy", "rollback":
		return w.stepDeploy(ctx, p, run, t, s.Action)
	case "run_command":
		return w.stepCommand(ctx, t, s)
	case "collect_diagnostics":
		return w.stepCollect(ctx, t)
	case "notify":
		msg := s.Text("message")
		if msg == "" {
			msg = run.Reason
		}
		if err := w.notify(ctx, p.row, "step", fmt.Sprintf("%s · %s: %s", p.row.Name, t.Name, msg), run.ID); err != nil {
			return stepOutcome{err: err}
		}
		o.verified, o.verification = verdict(true, "The webhook answered 2xx.")
		o.output = "Sent: " + msg
		return o
	}
	return stepOutcome{err: fmt.Errorf("unknown action %s", s.Action)}
}

// stepServerJob queues a server operation through the normal server job pipeline (so it shows
// in the server's activity too) and waits for it.
func (w *Worker) stepServerJob(ctx context.Context, t target, payload servers.Payload, verify func(*store.ServerJob) (bool, string)) stepOutcome {
	tx, err := w.Store.DB.Begin(ctx)
	if err != nil {
		return stepOutcome{err: err}
	}
	jobID, err := servers.Enqueue(ctx, tx, t.ID, "", payload, 1)
	if err != nil {
		tx.Rollback(ctx)
		return stepOutcome{err: err}
	}
	if err := tx.Commit(ctx); err != nil {
		return stepOutcome{err: err}
	}
	job, err := w.waitServerJob(ctx, jobID, 15*time.Minute)
	if err != nil {
		return stepOutcome{err: err, ref: "job:" + jobID}
	}
	o := stepOutcome{ref: "job:" + jobID, output: job.Progress}
	if job.Status != "succeeded" {
		o.err = errors.New(firstNonEmpty(job.Error, "the job "+job.Status))
		return o
	}
	ok, text := verify(job)
	o.verified, o.verification = verdict(ok, text)
	return o
}

func (w *Worker) waitServerJob(ctx context.Context, id string, max time.Duration) (*store.ServerJob, error) {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		job, err := w.Store.GetServerJob(ctx, id)
		if err == nil && (job.Status == "succeeded" || job.Status == "failed" || job.Status == "cancelled") {
			return job, nil
		}
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("the server job didn't finish within %s", max)
}

func (w *Worker) stepDiagnostics(ctx context.Context, t target, actor string) stepOutcome {
	tx, err := w.Store.DB.Begin(ctx)
	if err != nil {
		return stepOutcome{err: err}
	}
	runID, jobID, err := servers.EnqueueDiagnostics(ctx, w.Store, tx, t.ID, "", actor, servers.CheckIDs())
	if err != nil {
		tx.Rollback(ctx)
		return stepOutcome{err: err}
	}
	if err := tx.Commit(ctx); err != nil {
		return stepOutcome{err: err}
	}
	job, err := w.waitServerJob(ctx, jobID, 10*time.Minute)
	o := stepOutcome{ref: "diagnostics:" + runID}
	if err != nil {
		o.err = err
		return o
	}
	if job.Status != "succeeded" {
		o.err = errors.New(firstNonEmpty(job.Error, "diagnostics "+job.Status))
		return o
	}
	dr, err := w.Store.GetDiagnosticRun(ctx, runID)
	if err != nil {
		o.err = err
		return o
	}
	var lines []string
	failed, warned := 0, 0
	for _, r := range dr.Results {
		switch r.Status {
		case "failed":
			failed++
			lines = append(lines, fmt.Sprintf("✗ %s: %s", r.Check, r.Summary))
		case "warning":
			warned++
			lines = append(lines, fmt.Sprintf("! %s: %s", r.Check, r.Summary))
		}
	}
	o.output = fmt.Sprintf("%d checks: %d failed, %d warnings.", len(dr.Results), failed, warned)
	if len(lines) > 0 {
		o.output += "\n" + strings.Join(lines, "\n")
	}
	o.verified, o.verification = verdict(failed == 0, map[bool]string{true: "No check failed.", false: fmt.Sprintf("%d check(s) failed.", failed)}[failed == 0])
	return o
}

func (w *Worker) stepCleanup(ctx context.Context, t target, s automations.Step, actor string) stepOutcome {
	srv, err := w.Store.GetServer(ctx, t.ID)
	if err != nil {
		return stepOutcome{err: err}
	}
	if srv.Maintenance {
		return stepOutcome{err: errors.New("the server is in maintenance mode; cleanup is paused there")}
	}
	dk, err := w.registry().Docker(srv)
	if err != nil {
		return stepOutcome{err: err}
	}
	opts := servers.CleanupOptions{Containers: s.Bool("containers"), Dangling: s.Bool("dangling"), BuildCache: s.Bool("buildCache")} // never tagged images or volumes
	if !opts.Any() {
		return stepOutcome{err: errors.New("no cleanup category is selected")}
	}
	plan, err := servers.PlanCleanup(ctx, dk, w.Store, srv, opts)
	if err != nil {
		return stepOutcome{err: err}
	}
	if len(plan.Items) == 0 {
		o := stepOutcome{output: fmt.Sprintf("Nothing to remove. %d protected object(s) kept; volumes are never touched.", len(plan.Protected))}
		o.verified, o.verification = verdict(true, "Nothing was eligible.")
		return o
	}
	return w.stepServerJob(ctx, t, servers.Payload{Op: "cleanup", Cleanup: opts, Items: plan.Items, Actor: actor, TimeoutSeconds: 600}, func(job *store.ServerJob) (bool, string) {
		var out servers.CleanupOutcome
		json.Unmarshal(job.Result, &out)
		text := fmt.Sprintf("Removed %d of %d planned object(s), reclaimed %s", len(out.Removed), len(plan.Items), servers.BytesText(out.Reclaimed))
		if len(out.Skipped) > 0 {
			text += fmt.Sprintf("; %d were in use again and kept", len(out.Skipped))
		}
		if len(out.Failed) > 0 {
			text += fmt.Sprintf("; %d couldn't be removed (%s)", len(out.Failed), out.Failed[0].Reason)
		}
		return len(out.Failed) == 0, text + "."
	})
}

// stepDiskReport measures the disk, breaks down Docker's share and projects when it fills.
func (w *Worker) stepDiskReport(ctx context.Context, t target, s automations.Step) stepOutcome {
	srv, err := w.Store.GetServer(ctx, t.ID)
	if err != nil {
		return stepOutcome{err: err}
	}
	threshold := float64(s.Int("threshold"))
	var b strings.Builder
	var pct float64
	measured := false
	if m := srv.Metrics; m != nil && m.DiskUsed != nil && m.DiskTotal != nil && *m.DiskTotal > 0 {
		pct = float64(*m.DiskUsed) / float64(*m.DiskTotal) * 100
		measured = true
		fmt.Fprintf(&b, "Disk: %s of %s used (%.0f%%), %s free.\n", servers.BytesText(*m.DiskUsed), servers.BytesText(*m.DiskTotal), pct, servers.BytesText(*m.DiskTotal-*m.DiskUsed))
		if eta, rate, ok := w.diskForecast(ctx, srv.ID, *m.DiskTotal); ok {
			if rate <= 0 {
				fmt.Fprintf(&b, "Trend: not growing over the last 24 hours (%s/day).\n", signedBytes(rate))
			} else {
				fmt.Fprintf(&b, "Trend: growing %s/day; full in about %s at this rate.\n", servers.BytesText(int64(rate)), humanDuration(eta))
			}
		} else {
			b.WriteString("Trend: not enough samples yet to project (needs a few hours).\n")
		}
	} else {
		b.WriteString("Disk: this server's collector doesn't report disk usage.\n")
	}
	if dk, err := w.registry().Docker(srv); err == nil {
		if du, err := dk.DiskUsage(ctx); err == nil {
			var images, unusedImages, volumes, cache, reclaimCache int64
			for _, i := range du.Images {
				own := i.Size
				if i.SharedSize > 0 {
					own -= i.SharedSize
				}
				images += own
				if i.Containers == 0 {
					unusedImages += own
				}
			}
			for _, v := range du.Volumes {
				if v.Size > 0 {
					volumes += v.Size
				}
			}
			for _, c := range du.BuildCache {
				cache += c.Size
				if !c.InUse {
					reclaimCache += c.Size
				}
			}
			fmt.Fprintf(&b, "Docker: images %s (%s not used by any container), build cache %s (%s reclaimable), volumes %s (persistent data, never cleaned up).",
				servers.BytesText(du.LayersSize), servers.BytesText(unusedImages), servers.BytesText(cache), servers.BytesText(reclaimCache), servers.BytesText(volumes))
		}
	}
	o := stepOutcome{output: strings.TrimSpace(b.String())}
	if !measured {
		o.verified, o.verification = verdict(true, "Disk usage isn't measured on this server, so there's no threshold to check.")
		return o
	}
	ok := pct < threshold
	o.verified, o.verification = verdict(ok, fmt.Sprintf("%.0f%% used; healthy below %.0f%%.", pct, threshold))
	return o
}

// diskForecast fits a line through the last 24 hours of disk samples.
func (w *Worker) diskForecast(ctx context.Context, serverID string, total int64) (time.Duration, float64, bool) {
	rows, err := w.Store.DB.Query(ctx, `
		SELECT extract(epoch FROM at), disk_used FROM server_metric_samples
		WHERE server_id = $1 AND at > now() - interval '24 hours' AND disk_used IS NOT NULL ORDER BY at`, serverID)
	if err != nil {
		return 0, 0, false
	}
	defer rows.Close()
	var xs, ys []float64
	for rows.Next() {
		var x float64
		var y int64
		if rows.Scan(&x, &y) == nil {
			xs, ys = append(xs, x), append(ys, float64(y))
		}
	}
	if len(xs) < 20 || xs[len(xs)-1]-xs[0] < 3*3600 {
		return 0, 0, false
	}
	slope := linearSlope(xs, ys) // bytes per second
	perDay := slope * 86400
	if slope <= 0 {
		return 0, perDay, true
	}
	left := float64(total) - ys[len(ys)-1]
	return time.Duration(left/slope) * time.Second, perDay, true
}

func linearSlope(xs, ys []float64) float64 {
	n := float64(len(xs))
	var sx, sy, sxx, sxy float64
	for i := range xs {
		x := xs[i] - xs[0]
		sx, sy, sxx, sxy = sx+x, sy+ys[i], sxx+x*x, sxy+x*ys[i]
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0
	}
	return (n*sxy - sx*sy) / den
}

func signedBytes(perDay float64) string {
	if perDay < 0 {
		return "-" + servers.BytesText(int64(-perDay))
	}
	return servers.BytesText(int64(perDay))
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%.0f hours", d.Hours())
	case d < 120*24*time.Hour:
		return fmt.Sprintf("%.0f days", d.Hours()/24)
	}
	return "more than 4 months"
}

func (w *Worker) localOnly(ctx context.Context, t target) (*store.Resource, error) {
	res, err := w.Store.GetResource(ctx, t.ID)
	if err != nil {
		return nil, errors.New("the resource no longer exists")
	}
	if srv, err := w.Store.GetServer(ctx, res.ServerID); err == nil && !srv.IsLocal {
		return nil, errors.New("this resource isn't on the local server, where Ignition runs workloads")
	}
	return res, nil
}

// stepRestart restarts the container and waits until it runs again and, with a health check, passes it.
func (w *Worker) stepRestart(ctx context.Context, t target, s automations.Step) stepOutcome {
	res, err := w.localOnly(ctx, t)
	if err != nil {
		return stepOutcome{err: err}
	}
	if res.ContainerID == "" {
		return stepOutcome{err: errors.New(res.Name + " has never been deployed")}
	}
	if res.Status == "deploying" {
		return stepOutcome{err: errors.New(res.Name + " is being deployed right now; restarting it would interfere")}
	}
	ds, _ := w.Store.GetBuildSettings(ctx, res.ID)
	started := time.Now()
	if err := w.Runtime.Restart(ctx, res.ContainerID, time.Duration(ds.StopGrace)*time.Second); err != nil {
		return stepOutcome{err: fmt.Errorf("restart failed: %v", err)}
	}
	o := stepOutcome{output: "Restarted the container."}
	hc, _, _ := w.Store.GetHealth(ctx, res.ID)
	deadline := started.Add(time.Duration(s.Int("verifySeconds")) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		st, err := w.Runtime.Inspect(ctx, res.ContainerID)
		if err != nil || !st.Exists {
			continue
		}
		if !st.Running || st.Restarting {
			continue
		}
		if !hc.Enabled {
			if time.Since(st.StartedAt) >= 5*time.Second {
				o.verified, o.verification = verdict(true, "Running again (no health check is configured, so the process staying up is the signal).")
				return o
			}
			continue
		}
		_, hs, _ := w.Store.GetHealth(ctx, res.ID)
		if hs.State == "healthy" && hs.LastCheckedAt != nil && hs.LastCheckedAt.After(started) {
			o.verified, o.verification = verdict(true, "Running again and passing its health check.")
			return o
		}
	}
	w.refreshStatus(ctx, &res.ID)
	o.verified, o.verification = verdict(false, fmt.Sprintf("Not recovered within %ds: still not running and healthy.", s.Int("verifySeconds")))
	return o
}

// stepDeploy starts a redeploy or a rollback and waits for it to serve.
func (w *Worker) stepDeploy(ctx context.Context, p parsed, run *store.AutomationRun, t target, kind string) stepOutcome {
	res, err := w.localOnly(ctx, t)
	if err != nil {
		return stepOutcome{err: err}
	}
	trigger, imageRef, reason := "automation", "", "Automation: "+p.row.Name
	var target *store.Deployment
	if kind == "rollback" {
		list, err := w.Store.ListDeployments(ctx, res.ID, 100)
		if err != nil {
			return stepOutcome{err: err}
		}
		current := 0
		for _, d := range list {
			if d.Current {
				current = d.Number
			}
		}
		for i := range list {
			d := &list[i]
			if d.Status != "successful" || d.Current || (current > 0 && d.Number > current) {
				continue
			}
			img := d.ImageID
			if img == "" && res.SourceType == "git" {
				img = d.ImageRef
			}
			if img == "" {
				continue
			}
			if id, err := w.Runtime.ImageID(ctx, img); err == nil && id != "" {
				target, imageRef = d, img
				break
			}
		}
		if target == nil {
			return stepOutcome{err: errors.New("there's no earlier successful release whose image is still on this machine")}
		}
		trigger, reason = "rollback", fmt.Sprintf("Rollback to #%d (automation: %s)", target.Number, p.row.Name)
	}
	tx, err := w.Store.DB.Begin(ctx)
	if err != nil {
		return stepOutcome{err: err}
	}
	defer tx.Rollback(ctx)
	depID, err := w.Store.CreateDeployment(ctx, tx, res.ID, trigger, imageRef, res.Branch, reason, "")
	if err != nil {
		return stepOutcome{err: err}
	}
	if _, err := jobs.Enqueue(ctx, tx, jobs.NewJob{Kind: jobs.KindDeploy, ResourceID: res.ID, DeploymentID: depID}); err != nil {
		return stepOutcome{err: err}
	}
	if err := tx.Commit(ctx); err != nil {
		return stepOutcome{err: err}
	}
	o := stepOutcome{ref: "deployment:" + depID}
	deadline := time.Now().Add(40 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		d, err := w.Store.GetDeployment(ctx, depID)
		if err != nil {
			continue
		}
		switch d.Status {
		case "successful":
			o.output = fmt.Sprintf("Deployment #%d finished.", d.Number)
			if target != nil {
				o.output = fmt.Sprintf("Rolled back to #%d as deployment #%d.", target.Number, d.Number)
			}
			o.verified, o.verification = verdict(d.Current, map[bool]string{true: "It's the release serving now.", false: "It finished, but another release is serving."}[d.Current])
			return o
		case "failed", "cancelled":
			o.output = fmt.Sprintf("Deployment #%d %s.", d.Number, d.Status)
			o.err = errors.New(firstNonEmpty(d.Error, "the deployment "+d.Status))
			return o
		}
	}
	o.err = errors.New("the deployment didn't finish within 40 minutes")
	return o
}

func (w *Worker) stepCommand(ctx context.Context, t target, s automations.Step) stepOutcome {
	res, err := w.localOnly(ctx, t)
	if err != nil {
		return stepOutcome{err: err}
	}
	if res.ContainerID == "" || res.Status != "running" {
		return stepOutcome{err: fmt.Errorf("%s isn't running, so there's nowhere to run the command", res.Name)}
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(s.Int("timeoutSeconds"))*time.Second)
	defer cancel()
	out := &taskOutput{}
	code, err := w.Runtime.Exec(cctx, res.ContainerID, []string{"sh", "-c", s.Text("command")}, nil, func(_, line string) { out.add(line) })
	text, _ := out.text()
	o := stepOutcome{output: strings.TrimSpace(text)}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		o.err = fmt.Errorf("it didn't finish within %ds", s.Int("timeoutSeconds"))
	case err != nil:
		o.err = err
	case (code == 126 || code == 127) && noShell(text):
		o.err = errors.New("the container has no shell (sh), so commands can't run in it")
	default:
		expect := s.Text("expect")
		ok := code == 0 && (expect == "" || strings.Contains(text, expect))
		why := fmt.Sprintf("Exited with %d", code)
		if expect != "" {
			why += map[bool]string{true: fmt.Sprintf(" and printed %q.", expect), false: fmt.Sprintf("; expected output %q wasn't printed.", expect)}[strings.Contains(text, expect)]
		} else {
			why += "."
		}
		o.verified, o.verification = verdict(ok, why)
	}
	return o
}

// stepCollect saves evidence for whoever looks next.
func (w *Worker) stepCollect(ctx context.Context, t target) stepOutcome {
	res, err := w.Store.GetResource(ctx, t.ID)
	if err != nil {
		return stepOutcome{err: errors.New("the resource no longer exists")}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Status: %s (wants %s).\n", res.Status, res.DesiredState)
	if res.ContainerID != "" {
		if st, err := w.Runtime.Inspect(ctx, res.ContainerID); err == nil && st.Exists {
			fmt.Fprintf(&b, "Container: %s, exit code %d, restarted %d time(s)", st.Status, st.ExitCode, st.RestartCount)
			if st.OOMKilled {
				fmt.Fprintf(&b, ", killed for using too much memory (limit %d MB)", res.MemoryMB)
			}
			if !st.StartedAt.IsZero() {
				fmt.Fprintf(&b, ", started %s ago", humanDuration(time.Since(st.StartedAt)))
			}
			b.WriteString(".\n")
		}
	}
	if cfg, hs, err := w.Store.GetHealth(ctx, res.ID); err == nil && cfg.Enabled {
		fmt.Fprintf(&b, "Health: %s, %d failed in a row.\n", hs.State, hs.Failures)
		if results, err := w.Store.HealthResults(ctx, res.ID, 5); err == nil {
			for _, r := range results {
				mark := "✓"
				if !r.OK {
					mark = "✗"
				}
				fmt.Fprintf(&b, "  %s %s %s\n", mark, r.CheckedAt.Format("15:04:05"), r.Detail)
			}
		}
	}
	if deps, err := w.Store.ListDeployments(ctx, res.ID, 1); err == nil && len(deps) > 0 {
		d := deps[0]
		fmt.Fprintf(&b, "Latest deployment: #%d %s", d.Number, d.Status)
		if d.Error != "" {
			fmt.Fprintf(&b, ": %s", d.Error)
		}
		b.WriteString(".\n")
	}
	if res.ContainerID != "" {
		if lines, err := w.Runtime.Logs(ctx, res.ContainerID, 40, time.Time{}); err == nil && len(lines) > 0 {
			b.WriteString("Last log lines:\n")
			for _, l := range lines {
				b.WriteString("  " + l.Message + "\n")
			}
		}
	}
	o := stepOutcome{output: strings.TrimSpace(b.String())}
	o.verified, o.verification = verdict(true, "Evidence captured.")
	return o
}

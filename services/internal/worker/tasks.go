package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/notsliver/Ignition/services/internal/cron"
)

/*
 * Scheduled tasks run a command inside the resource's running container. The scheduler turns due
 * schedules into queued runs (one per scheduled minute, deduplicated by the database, so several workers
 * never run a task twice), and up to taskSlots runs execute at once. A run that would overlap a run still
 * in progress is recorded as skipped. Missed runs while the worker was down are caught up once, not
 * replayed one by one.
 */

const (
	taskSlots     = 4
	taskOutputMax = 64 << 10
	pidMarker     = "@@ignition-pid "
)

// NextRun is when a task runs next, or nil if the schedule never fires.
func NextRun(schedule, timezone string, after time.Time) (*time.Time, error) {
	s, err := cron.Parse(schedule)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %s", timezone)
	}
	next := s.Next(after, loc)
	if next.IsZero() {
		return nil, nil
	}
	next = next.UTC()
	return &next, nil
}

func (w *Worker) TaskLoop(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	slots := make(chan struct{}, taskSlots)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		w.scheduleDue(ctx)
		w.failInterrupted(ctx)
		for free := taskSlots - len(slots); free > 0; free-- {
			id := w.claimRun(ctx)
			if id == "" {
				break
			}
			slots <- struct{}{}
			wg.Add(1)
			go func() {
				defer func() { <-slots; wg.Done() }()
				w.runTask(context.WithoutCancel(ctx), id)
			}()
		}
	}
}

func (w *Worker) scheduleDue(ctx context.Context) {
	tx, err := w.Store.DB.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT id, schedule, timezone, next_run_at FROM scheduled_tasks
		WHERE enabled AND next_run_at <= now() ORDER BY next_run_at LIMIT 50 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return
	}
	type due struct {
		id, schedule, tz string
		at               time.Time
	}
	var list []due
	for rows.Next() {
		var d due
		if rows.Scan(&d.id, &d.schedule, &d.tz, &d.at) == nil {
			list = append(list, d)
		}
	}
	rows.Close()
	for _, d := range list {
		// A run still going means this one is skipped rather than piling up.
		tx.Exec(ctx, `
			INSERT INTO task_runs (task_id, trigger, scheduled_for, status, error, finished_at)
			SELECT $1, 'schedule', $2,
			       CASE WHEN busy THEN 'skipped' ELSE 'queued' END,
			       CASE WHEN busy THEN 'The previous run was still going.' ELSE '' END,
			       CASE WHEN busy THEN now() END
			FROM (SELECT EXISTS (SELECT 1 FROM task_runs WHERE task_id = $1 AND status IN ('queued', 'running')) AS busy) b
			ON CONFLICT DO NOTHING`, d.id, d.at)
		next, err := NextRun(d.schedule, d.tz, time.Now())
		if err != nil {
			slog.Warn("task schedule", "task", d.id, "error", err)
		}
		tx.Exec(ctx, `UPDATE scheduled_tasks SET next_run_at = $2 WHERE id = $1`, d.id, next)
	}
	tx.Commit(ctx)
}

// failInterrupted closes runs whose worker went away mid-run (well past their timeout).
func (w *Worker) failInterrupted(ctx context.Context) {
	w.Store.DB.Exec(ctx, `
		UPDATE task_runs r SET status = 'failed', finished_at = now(),
		       error = 'Interrupted: Ignition''s worker stopped while this ran. The command may have finished anyway.'
		FROM scheduled_tasks t
		WHERE t.id = r.task_id AND r.status = 'running' AND r.started_at < now() - make_interval(secs => t.timeout_seconds + 120)`)
}

func (w *Worker) claimRun(ctx context.Context) string {
	var id string
	w.Store.DB.QueryRow(ctx, `
		UPDATE task_runs SET status = 'running', started_at = now()
		WHERE id = (SELECT id FROM task_runs WHERE status = 'queued' ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING id`).Scan(&id)
	return id
}

// taskOutput keeps the last taskOutputMax bytes of a run's output.
type taskOutput struct {
	mu      sync.Mutex
	buf     []byte
	cut     bool
	pid     string
	changed bool
}

func (o *taskOutput) add(line string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.pid == "" && strings.HasPrefix(line, pidMarker) {
		o.pid = strings.TrimSpace(strings.TrimPrefix(line, pidMarker))
		return
	}
	o.buf = append(append(o.buf, line...), '\n')
	if len(o.buf) > taskOutputMax {
		o.buf = o.buf[len(o.buf)-taskOutputMax:]
		if i := strings.IndexByte(string(o.buf), '\n'); i >= 0 {
			o.buf = o.buf[i+1:]
		}
		o.cut = true
	}
	o.changed = true
}

func (o *taskOutput) text() (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	changed := o.changed
	o.changed = false
	s := string(o.buf)
	if o.cut {
		s = "… earlier output was trimmed …\n" + s
	}
	return s, changed
}

func (o *taskOutput) process() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.pid
}

func (w *Worker) finishRun(ctx context.Context, id, status, errMsg string, code *int, out *taskOutput) {
	text, _ := out.text()
	w.Store.DB.Exec(ctx, `UPDATE task_runs SET status = $2, error = $3, exit_code = $4, output = $5, finished_at = now() WHERE id = $1`,
		id, status, errMsg, code, text)
	// Keep the latest 50 runs per task.
	w.Store.DB.Exec(ctx, `
		DELETE FROM task_runs WHERE task_id = (SELECT task_id FROM task_runs WHERE id = $1)
		  AND id NOT IN (SELECT id FROM task_runs WHERE task_id = (SELECT task_id FROM task_runs WHERE id = $1) ORDER BY created_at DESC LIMIT 50)`, id)
}

func (w *Worker) runTask(ctx context.Context, runID string) {
	out := &taskOutput{}
	run, err := w.Store.GetTaskRun(ctx, runID)
	if err != nil {
		return
	}
	task, err := w.Store.GetTask(ctx, run.TaskID)
	if err != nil {
		return
	}
	res, err := w.Store.GetResource(ctx, task.ResourceID)
	if err != nil {
		return
	}
	if res.ContainerID == "" || res.Status != "running" {
		state := map[string]string{"paused": "is paused", "stopped": "is stopped", "failed": "isn't running (it failed)", "deploying": "is being deployed"}[res.Status]
		if state == "" {
			state = "isn't running"
		}
		w.finishRun(ctx, runID, "failed", fmt.Sprintf("%s %s, so the task couldn't run.", res.Name, state), nil, out)
		return
	}

	// The shell prints its PID first, so a timeout or cancel can stop the command inside the container.
	cmd := []string{"sh", "-c", `echo "` + pidMarker + `$$"; exec sh -c "$IGNITION_TASK"`}
	env := []string{"IGNITION_TASK=" + task.Command, "IGNITION_TASK_NAME=" + task.Name, "IGNITION_TASK_RUN=" + runID}
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	execCtx, stopExec := context.WithCancel(ctx)
	defer stopExec()
	go func() {
		code, err := w.Runtime.Exec(execCtx, res.ContainerID, cmd, env, func(_, line string) { out.add(line) })
		done <- result{code, err}
	}()

	deadline := time.After(time.Duration(task.TimeoutSeconds) * time.Second)
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()
	stopReason := ""
	var killAt time.Time
	for {
		select {
		case r := <-done:
			switch {
			case r.err != nil && errors.Is(r.err, context.Canceled):
				w.finishRun(ctx, runID, "failed", stopReason+" It didn't stop when asked and may still be running inside the container.", nil, out)
			case stopReason != "":
				status := "failed"
				if strings.HasPrefix(stopReason, "Cancelled") {
					status = "cancelled"
				}
				w.finishRun(ctx, runID, status, stopReason, &r.code, out)
			case r.err != nil && (strings.Contains(r.err.Error(), "executable file not found") || strings.Contains(r.err.Error(), "no such file")):
				w.finishRun(ctx, runID, "failed", "The container has no shell (sh), so commands can't run in it.", nil, out)
			case r.err != nil && strings.Contains(r.err.Error(), "is not running"):
				w.finishRun(ctx, runID, "failed", res.Name+" stopped before the task could run.", nil, out)
			case r.err != nil:
				w.finishRun(ctx, runID, "failed", "Couldn't run the command: "+r.err.Error(), nil, out)
			case (r.code == 126 || r.code == 127) && func() bool { text, _ := out.text(); return noShell(text) }():
				w.finishRun(ctx, runID, "failed", "The container has no shell (sh), so commands can't run in it. Images built FROM scratch or distroless can't run tasks.", &r.code, out)
			case r.code == 0:
				w.finishRun(ctx, runID, "succeeded", "", &r.code, out)
			default:
				w.finishRun(ctx, runID, "failed", exitReason(r.code), &r.code, out)
			}
			return
		case <-deadline:
			if stopReason == "" {
				stopReason = fmt.Sprintf("Timed out after %s and was stopped.", (time.Duration(task.TimeoutSeconds) * time.Second).String())
				w.signalTask(ctx, res.ContainerID, out.process(), "TERM")
				killAt = time.Now().Add(10 * time.Second)
			}
		case <-poll.C:
			if text, changed := out.text(); changed {
				w.Store.DB.Exec(ctx, `UPDATE task_runs SET output = $2 WHERE id = $1`, runID, text)
			}
			var cancel bool
			w.Store.DB.QueryRow(ctx, `SELECT cancel_requested FROM task_runs WHERE id = $1`, runID).Scan(&cancel)
			if cancel && stopReason == "" {
				stopReason = "Cancelled while running."
				w.signalTask(ctx, res.ContainerID, out.process(), "TERM")
				killAt = time.Now().Add(10 * time.Second)
			}
			if !killAt.IsZero() && time.Now().After(killAt) {
				w.signalTask(ctx, res.ContainerID, out.process(), "KILL")
				killAt = time.Time{}
				time.AfterFunc(15*time.Second, stopExec) // give up waiting on a process that won't die
			}
		}
	}
}

// signalTask sends a signal to the task's process group inside the container.
func (w *Worker) signalTask(ctx context.Context, container, pid, signal string) {
	if _, err := strconv.Atoi(pid); err != nil {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	w.Runtime.Exec(sctx, container, []string{"sh", "-c", "kill -" + signal + " -" + pid + " 2>/dev/null || kill -" + signal + " " + pid}, nil, func(string, string) {})
}

func exitReason(code int) string {
	switch code {
	case 126:
		return "Exited with 126: the command isn't executable."
	case 127:
		return "Exited with 127: command not found in the container."
	case 137:
		return "Exited with 137: killed (out of memory, or stopped)."
	case 143:
		return "Exited with 143: stopped (SIGTERM)."
	}
	return fmt.Sprintf("Exited with %d.", code)
}

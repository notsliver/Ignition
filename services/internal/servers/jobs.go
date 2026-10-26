package servers

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/notsliver/Ignition/services/internal/jobs"
	"github.com/notsliver/Ignition/services/internal/store"
)

// Payload is the body of a jobs.KindServer job.
type Payload struct {
	Op             string         `json:"op"` // refresh, diagnostics, cleanup, task
	RunID          string         `json:"runId,omitempty"`
	TaskRunID      string         `json:"taskRunId,omitempty"`
	TaskKind       string         `json:"taskKind,omitempty"`
	TimeoutSeconds int            `json:"timeoutSeconds,omitempty"`
	Cleanup        CleanupOptions `json:"cleanup,omitempty"`
	Items          []CleanupItem  `json:"items,omitempty"`
	Options        map[string]any `json:"options,omitempty"`
	Actor          string         `json:"actor,omitempty"`
}

// Enqueue queues one server operation. Checks and cleanups don't retry by default: repeating a
// failed connection test or a refused removal tells the operator nothing new.
func Enqueue(ctx context.Context, tx pgx.Tx, serverID, userID string, p Payload, maxAttempts int) (string, error) {
	if maxAttempts == 0 {
		maxAttempts = 1
	}
	return jobs.Enqueue(ctx, tx, jobs.NewJob{Kind: jobs.KindServer, ServerID: serverID, CreatedBy: userID, Payload: p, MaxAttempts: maxAttempts})
}

// EnqueueDiagnostics creates a diagnostic run and the job that executes it.
func EnqueueDiagnostics(ctx context.Context, st *store.Store, tx pgx.Tx, serverID, userID, actor string, checks []string) (runID, jobID string, err error) {
	if runID, err = st.CreateDiagnosticRun(ctx, tx, serverID, checks, actor); err != nil {
		return
	}
	jobID, err = Enqueue(ctx, tx, serverID, userID, Payload{Op: "diagnostics", RunID: runID, Actor: actor, TimeoutSeconds: 300}, 1)
	return
}

// EnqueueTask records a run of a scheduled server task and queues its job, honouring the task's
// concurrency policy: with "skip", a run that would overlap an active one is recorded as skipped.
// It returns "" when this scheduled minute was already recorded.
func EnqueueTask(ctx context.Context, st *store.Store, tx pgx.Tx, t *store.ServerTask, trigger string, scheduledFor *time.Time, userID, actor string) (string, string, error) {
	if t.Concurrency == "skip" {
		active, err := st.TaskRunActive(ctx, tx, t.ID)
		if err != nil {
			return "", "", err
		}
		if active {
			id, err := st.CreateServerTaskRun(ctx, tx, t.ID, trigger, "skipped", scheduledFor, actor, "Skipped: the previous run is still in progress.")
			return id, "skipped", err
		}
	}
	runID, err := st.CreateServerTaskRun(ctx, tx, t.ID, trigger, "queued", scheduledFor, actor, "")
	if err != nil || runID == "" {
		return runID, "", err
	}
	var opts map[string]any
	if len(t.Options) > 0 {
		jsonUnmarshal(t.Options, &opts)
	}
	jobID, err := Enqueue(ctx, tx, t.ServerID, userID, Payload{Op: "task", TaskRunID: runID, TaskKind: t.Kind, TimeoutSeconds: t.TimeoutSeconds, Options: opts, Actor: actor}, t.MaxAttempts)
	if err != nil {
		return "", "", err
	}
	return runID, "queued", st.AttachTaskRunJob(ctx, tx, runID, jobID)
}

// Package jobs is a durable PostgreSQL-backed queue.
//
// Claiming uses SELECT ... FOR UPDATE SKIP LOCKED so many workers can poll safely.
// A claimed job holds a lease that the worker extends with heartbeats; if the worker
// dies, the lease expires and another worker reclaims the job (counted as an attempt).
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	KindDeploy = "deploy" // build (if needed) and roll out a resource
	KindAction = "action" // start / stop / restart a resource
	KindRemove = "remove" // delete a resource's containers (and optionally volumes)
	KindImport = "import" // load a dump or a live database into a database resource
	KindInvoke = "invoke" // call a function once (Functions › Test)
	KindHealth = "health" // run one health check with the given settings (Health Checks, Test)
	KindServer = "server" // a server operation: refresh, diagnostics, cleanup, scheduled server task
)

type Job struct {
	ID           string
	Kind         string
	Payload      json.RawMessage
	ResourceID   *string
	DeploymentID *string
	ServerID     *string
	Attempts     int
	MaxAttempts  int
}

type Queue struct{ DB *pgxpool.Pool }

type NewJob struct {
	Kind         string
	Payload      any
	ResourceID   string
	DeploymentID string
	ServerID     string
	CreatedBy    string
	MaxAttempts  int
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Enqueue inserts a job. Pass a transaction to enqueue atomically with other writes.
func Enqueue(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, j NewJob) (string, error) {
	payload, err := json.Marshal(j.Payload)
	if err != nil {
		return "", err
	}
	if j.MaxAttempts == 0 {
		j.MaxAttempts = 3
	}
	var id string
	err = q.QueryRow(ctx, `
		INSERT INTO jobs (kind, payload, resource_id, deployment_id, server_id, created_by, max_attempts)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		j.Kind, payload, nullable(j.ResourceID), nullable(j.DeploymentID), nullable(j.ServerID), nullable(j.CreatedBy), j.MaxAttempts).Scan(&id)
	return id, err
}

// Claim takes the next runnable job, or an abandoned one whose lease expired.
func (q *Queue) Claim(ctx context.Context, owner string, lease time.Duration) (*Job, error) {
	var j Job
	err := q.DB.QueryRow(ctx, `
		UPDATE jobs SET status = 'running', lease_owner = $1, lease_until = now() + $2::interval,
		       heartbeat_at = now(), attempts = attempts + 1, started_at = COALESCE(started_at, now())
		WHERE id = (
			SELECT id FROM jobs j
			WHERE ((j.status = 'pending' AND j.run_after <= now())
			       OR (j.status = 'running' AND j.lease_until < now()))
			  -- One job at a time per resource: two deploys of the same resource must never overlap.
			  -- The advisory lock covers two workers claiming at the same instant.
			  AND (j.resource_id IS NULL OR (
			       NOT EXISTS (SELECT 1 FROM jobs o WHERE o.resource_id = j.resource_id AND o.id <> j.id AND o.status = 'running' AND o.lease_until >= now())
			       AND pg_try_advisory_xact_lock(hashtext('ignition-job:' || j.resource_id::text))))
			ORDER BY j.created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING id, kind, payload, resource_id, deployment_id, server_id, attempts, max_attempts`,
		owner, fmt.Sprintf("%d seconds", int(lease.Seconds()))).
		Scan(&j.ID, &j.Kind, &j.Payload, &j.ResourceID, &j.DeploymentID, &j.ServerID, &j.Attempts, &j.MaxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &j, err
}

// Heartbeat extends the lease and reports whether the job was cancelled.
func (q *Queue) Heartbeat(ctx context.Context, id, owner string, lease time.Duration) (cancelled bool, err error) {
	err = q.DB.QueryRow(ctx, `
		UPDATE jobs SET lease_until = now() + $3::interval, heartbeat_at = now()
		WHERE id = $1 AND lease_owner = $2 AND status = 'running'
		RETURNING cancel_requested`, id, owner, fmt.Sprintf("%d seconds", int(lease.Seconds()))).Scan(&cancelled)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil // we lost the lease; stop working
	}
	return cancelled, err
}

func (q *Queue) Progress(ctx context.Context, id, progress string) {
	q.DB.Exec(ctx, `UPDATE jobs SET progress = $2 WHERE id = $1`, id, progress)
}

func (q *Queue) Succeed(ctx context.Context, id string) error {
	_, err := q.DB.Exec(ctx, `UPDATE jobs SET status = 'succeeded', finished_at = now(), lease_owner = NULL, lease_until = NULL WHERE id = $1`, id)
	return err
}

func (q *Queue) Cancelled(ctx context.Context, id string) error {
	_, err := q.DB.Exec(ctx, `UPDATE jobs SET status = 'cancelled', finished_at = now(), lease_owner = NULL, lease_until = NULL WHERE id = $1`, id)
	return err
}

// ErrPermanent marks failures that retrying can't fix (bad config, missing image).
var ErrPermanent = errors.New("permanent failure")

// Fail records the error and either schedules a retry with bounded exponential backoff
// or marks the job failed. It returns true when the job won't run again.
func (q *Queue) Fail(ctx context.Context, j *Job, cause error) (final bool, err error) {
	final = errors.Is(cause, ErrPermanent) || j.Attempts >= j.MaxAttempts
	if final {
		_, err = q.DB.Exec(ctx, `UPDATE jobs SET status = 'failed', last_error = $2, finished_at = now(), lease_owner = NULL, lease_until = NULL WHERE id = $1`, j.ID, cause.Error())
		return
	}
	backoff := time.Duration(1<<min(j.Attempts, 6)) * 5 * time.Second // 10s, 20s, 40s … capped at 320s
	_, err = q.DB.Exec(ctx, `UPDATE jobs SET status = 'pending', last_error = $2, run_after = now() + $3::interval, lease_owner = NULL, lease_until = NULL WHERE id = $1`,
		j.ID, cause.Error(), fmt.Sprintf("%d seconds", int(backoff.Seconds())))
	return
}

// RequestCancel flags a job. Pending jobs are cancelled immediately; running ones stop at the next heartbeat.
func RequestCancel(ctx context.Context, db *pgxpool.Pool, id string) (status string, err error) {
	err = db.QueryRow(ctx, `
		UPDATE jobs SET cancel_requested = true,
		       status = CASE WHEN status = 'pending' THEN 'cancelled' ELSE status END,
		       finished_at = CASE WHEN status = 'pending' THEN now() ELSE finished_at END
		WHERE id = $1 AND status IN ('pending', 'running')
		RETURNING status`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return status, err
}

// Log appends a line to a job's persisted log, shown in the deployment timeline.
func (q *Queue) Log(ctx context.Context, jobID, level, step, message string) {
	q.DB.Exec(ctx, `INSERT INTO job_logs (job_id, level, step, message) VALUES ($1, $2, $3, $4)`, jobID, level, step, message)
}

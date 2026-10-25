package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

/* ------------------------------- Health checks ------------------------------ */

type HealthConfig struct {
	Enabled          bool   `json:"enabled"`
	Kind             string `json:"kind"` // http, tcp, command
	Path             string `json:"path"`
	Port             *int   `json:"port"` // nil = the resource's port
	ExpectStatus     string `json:"expectStatus"`
	Command          string `json:"command"`
	IntervalSeconds  int    `json:"intervalSeconds"`
	TimeoutSeconds   int    `json:"timeoutSeconds"`
	Retries          int    `json:"retries"`
	StartPeriod      int    `json:"startPeriodSeconds"`
	RestartUnhealthy bool   `json:"restartUnhealthy"`
}

type HealthState struct {
	State         string      `json:"state"` // unknown, starting, healthy, unhealthy
	Failures      int         `json:"failures"`
	LastCheckedAt *time.Time  `json:"lastCheckedAt"`
	LastChangeAt  *time.Time  `json:"lastChangeAt"`
	Restarts      []time.Time `json:"restarts"`
}

var DefaultHealth = HealthConfig{Kind: "http", Path: "/", ExpectStatus: "200-399", IntervalSeconds: 10, TimeoutSeconds: 5, Retries: 3, StartPeriod: 30, RestartUnhealthy: true}

func (s *Store) GetHealth(ctx context.Context, resourceID string) (HealthConfig, HealthState, error) {
	c, st := DefaultHealth, HealthState{State: "unknown", Restarts: []time.Time{}}
	err := s.DB.QueryRow(ctx, `
		SELECT enabled, kind, path, port, expect_status, command, interval_seconds, timeout_seconds, retries, start_period_seconds, restart_unhealthy,
		       state, failures, last_checked_at, last_change_at, restarts
		FROM resource_health WHERE resource_id = $1`, resourceID).
		Scan(&c.Enabled, &c.Kind, &c.Path, &c.Port, &c.ExpectStatus, &c.Command, &c.IntervalSeconds, &c.TimeoutSeconds, &c.Retries, &c.StartPeriod, &c.RestartUnhealthy,
			&st.State, &st.Failures, &st.LastCheckedAt, &st.LastChangeAt, &st.Restarts)
	if err == pgx.ErrNoRows {
		return c, st, nil
	}
	return c, st, err
}

// PutHealth saves the configuration; changing it starts the observed state over.
func (s *Store) PutHealth(ctx context.Context, resourceID string, c HealthConfig) error {
	_, err := s.DB.Exec(ctx, `
		INSERT INTO resource_health (resource_id, enabled, kind, path, port, expect_status, command, interval_seconds, timeout_seconds, retries, start_period_seconds, restart_unhealthy)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (resource_id) DO UPDATE SET enabled = $2, kind = $3, path = $4, port = $5, expect_status = $6, command = $7, interval_seconds = $8,
		       timeout_seconds = $9, retries = $10, start_period_seconds = $11, restart_unhealthy = $12,
		       state = 'unknown', failures = 0, last_change_at = now(), updated_at = now()`,
		resourceID, c.Enabled, c.Kind, c.Path, c.Port, c.ExpectStatus, c.Command, c.IntervalSeconds, c.TimeoutSeconds, c.Retries, c.StartPeriod, c.RestartUnhealthy)
	return err
}

func (s *Store) SetHealthState(ctx context.Context, resourceID, state string, failures int, changed bool) error {
	_, err := s.DB.Exec(ctx, `
		UPDATE resource_health SET state = $2, failures = $3, last_checked_at = now(),
		       last_change_at = CASE WHEN $4 THEN now() ELSE last_change_at END
		WHERE resource_id = $1`, resourceID, state, failures, changed)
	return err
}

// NoteHealthRestart records an automatic restart and keeps only the last hour of them.
func (s *Store) NoteHealthRestart(ctx context.Context, resourceID string) error {
	_, err := s.DB.Exec(ctx, `
		UPDATE resource_health SET state = 'starting', failures = 0, last_change_at = now(),
		       restarts = ARRAY(SELECT t FROM unnest(restarts || now()) t WHERE t > now() - interval '1 hour' ORDER BY t)
		WHERE resource_id = $1`, resourceID)
	return err
}

type HealthResult struct {
	ID        int64     `json:"id"`
	CheckedAt time.Time `json:"checkedAt"`
	OK        bool      `json:"ok"`
	LatencyMS int       `json:"latencyMs"`
	Detail    string    `json:"detail"`
	Source    string    `json:"source"`
}

// RecordHealth stores a result and keeps the latest 300 per resource.
func (s *Store) RecordHealth(ctx context.Context, resourceID string, r HealthResult) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO health_results (resource_id, ok, latency_ms, detail, source) VALUES ($1, $2, $3, $4, $5)`,
		resourceID, r.OK, r.LatencyMS, r.Detail, r.Source)
	if err == nil {
		s.DB.Exec(ctx, `DELETE FROM health_results WHERE resource_id = $1 AND id < (SELECT id FROM health_results WHERE resource_id = $1 ORDER BY id DESC OFFSET 299 LIMIT 1)`, resourceID)
	}
	return err
}

func (s *Store) HealthResults(ctx context.Context, resourceID string, limit int) ([]HealthResult, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, checked_at, ok, latency_ms, detail, source FROM health_results WHERE resource_id = $1 ORDER BY id DESC LIMIT $2`, resourceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HealthResult{}
	for rows.Next() {
		var r HealthResult
		if err := rows.Scan(&r.ID, &r.CheckedAt, &r.OK, &r.LatencyMS, &r.Detail, &r.Source); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// HealthTarget is a running resource whose health the worker watches.
type HealthTarget struct {
	ResourceID, Name, ContainerID, EnvironmentID string
	Port                                         *int
	RunningSince                                 *time.Time
	Config                                       HealthConfig
	State                                        HealthState
}

func (s *Store) HealthTargets(ctx context.Context) ([]HealthTarget, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT r.id, r.name, r.container_id, r.environment_id, r.port, r.running_since,
		       h.kind, h.path, h.port, h.expect_status, h.command, h.interval_seconds, h.timeout_seconds, h.retries, h.start_period_seconds, h.restart_unhealthy,
		       h.state, h.failures, h.last_checked_at, h.last_change_at, h.restarts
		FROM resource_health h JOIN resources r ON r.id = h.resource_id
		WHERE h.enabled AND r.status = 'running' AND r.container_id <> ''
		  AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.resource_id = r.id AND j.kind <> 'health' AND j.status IN ('pending', 'running'))`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HealthTarget
	for rows.Next() {
		var t HealthTarget
		c := &t.Config
		c.Enabled = true
		if err := rows.Scan(&t.ResourceID, &t.Name, &t.ContainerID, &t.EnvironmentID, &t.Port, &t.RunningSince,
			&c.Kind, &c.Path, &c.Port, &c.ExpectStatus, &c.Command, &c.IntervalSeconds, &c.TimeoutSeconds, &c.Retries, &c.StartPeriod, &c.RestartUnhealthy,
			&t.State.State, &t.State.Failures, &t.State.LastCheckedAt, &t.State.LastChangeAt, &t.State.Restarts); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

/* --------------------------- Releases and rollbacks ------------------------- */

func (s *Store) SetDeploymentImageID(ctx context.Context, id, imageID string) error {
	_, err := s.DB.Exec(ctx, `UPDATE deployments SET image_id = $2 WHERE id = $1`, id, imageID)
	return err
}

// SnapshotEnv keeps the variables a deployment runs with (still sealed), so a rollback can restore them.
func (s *Store) SnapshotEnv(ctx context.Context, deploymentID, resourceID string) error {
	_, err := s.DB.Exec(ctx, `
		INSERT INTO deployment_env (deployment_id, key, value_enc, secret)
		SELECT $1, key, value_enc, secret FROM env_vars WHERE resource_id = $2
		ON CONFLICT DO NOTHING`, deploymentID, resourceID)
	return err
}

// DeploymentEnv returns a deployment's kept variables, sealed.
func (s *Store) DeploymentEnv(ctx context.Context, deploymentID string) ([]EnvVar, error) {
	rows, err := s.DB.Query(ctx, `SELECT key, value_enc, secret FROM deployment_env WHERE deployment_id = $1 ORDER BY key`, deploymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EnvVar{}
	for rows.Next() {
		var v EnvVar
		if err := rows.Scan(&v.Key, &v.Sealed, &v.Secret); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// RestoreEnv replaces the resource's variables with the ones a deployment ran with.
func (s *Store) RestoreEnv(ctx context.Context, tx pgx.Tx, resourceID, deploymentID, userID string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM env_vars WHERE resource_id = $1`, resourceID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO env_vars (resource_id, key, value_enc, secret, updated_by)
		SELECT $1, key, value_enc, secret, $3 FROM deployment_env WHERE deployment_id = $2`, resourceID, deploymentID, nullable(userID))
	return err
}

/* ------------------------------ Scheduled tasks ----------------------------- */

type Task struct {
	ID             string     `json:"id"`
	ResourceID     string     `json:"resourceId"`
	Name           string     `json:"name"`
	Schedule       string     `json:"schedule"`
	Timezone       string     `json:"timezone"`
	Command        string     `json:"command"`
	TimeoutSeconds int        `json:"timeoutSeconds"`
	Enabled        bool       `json:"enabled"`
	NextRunAt      *time.Time `json:"nextRunAt"`
	CreatedBy      string     `json:"createdBy"`
	CreatedAt      time.Time  `json:"createdAt"`
	LastRun        *TaskRun   `json:"lastRun"`
}

type TaskRun struct {
	ID           string     `json:"id"`
	TaskID       string     `json:"taskId"`
	Trigger      string     `json:"trigger"`
	Status       string     `json:"status"`
	ScheduledFor *time.Time `json:"scheduledFor"`
	ExitCode     *int       `json:"exitCode"`
	Output       string     `json:"output,omitempty"`
	Error        string     `json:"error"`
	By           string     `json:"by"`
	CreatedAt    time.Time  `json:"createdAt"`
	StartedAt    *time.Time `json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt"`
}

const taskSelect = `
	SELECT t.id, t.resource_id, t.name, t.schedule, t.timezone, t.command, t.timeout_seconds, t.enabled, t.next_run_at,
	       COALESCE(u.email, ''), t.created_at
	FROM scheduled_tasks t LEFT JOIN users u ON u.id = t.created_by`

func scanTask(row pgx.Row) (*Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.ResourceID, &t.Name, &t.Schedule, &t.Timezone, &t.Command, &t.TimeoutSeconds, &t.Enabled, &t.NextRunAt, &t.CreatedBy, &t.CreatedAt)
	return &t, noRows(err)
}

func (s *Store) ListTasks(ctx context.Context, resourceID string) ([]Task, error) {
	rows, err := s.DB.Query(ctx, taskSelect+` WHERE t.resource_id = $1 ORDER BY t.created_at`, resourceID)
	if err != nil {
		return nil, err
	}
	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *t)
	}
	rows.Close()
	for i := range out {
		if runs, err := s.TaskRuns(ctx, out[i].ID, 1, false); err == nil && len(runs) > 0 {
			out[i].LastRun = &runs[0]
		}
	}
	return out, nil
}

func (s *Store) GetTask(ctx context.Context, id string) (*Task, error) {
	return scanTask(s.DB.QueryRow(ctx, taskSelect+` WHERE t.id = $1`, id))
}

func (s *Store) CreateTask(ctx context.Context, t Task, userID string) (string, error) {
	var id string
	err := s.DB.QueryRow(ctx, `
		INSERT INTO scheduled_tasks (resource_id, name, schedule, timezone, command, timeout_seconds, enabled, next_run_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		t.ResourceID, t.Name, t.Schedule, t.Timezone, t.Command, t.TimeoutSeconds, t.Enabled, t.NextRunAt, nullable(userID)).Scan(&id)
	return id, err
}

func (s *Store) UpdateTask(ctx context.Context, t Task) error {
	_, err := s.DB.Exec(ctx, `
		UPDATE scheduled_tasks SET name = $2, schedule = $3, timezone = $4, command = $5, timeout_seconds = $6, enabled = $7, next_run_at = $8, updated_at = now()
		WHERE id = $1`, t.ID, t.Name, t.Schedule, t.Timezone, t.Command, t.TimeoutSeconds, t.Enabled, t.NextRunAt)
	return err
}

func (s *Store) DeleteTask(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM scheduled_tasks WHERE id = $1`, id)
	return err
}

func (s *Store) TaskRuns(ctx context.Context, taskID string, limit int, withOutput bool) ([]TaskRun, error) {
	output := `''`
	if withOutput {
		output = `r.output`
	}
	rows, err := s.DB.Query(ctx, `
		SELECT r.id, r.task_id, r.trigger, r.status, r.scheduled_for, r.exit_code, `+output+`, r.error, COALESCE(u.email, ''),
		       r.created_at, r.started_at, r.finished_at
		FROM task_runs r LEFT JOIN users u ON u.id = r.created_by
		WHERE r.task_id = $1 ORDER BY r.created_at DESC LIMIT $2`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskRun{}
	for rows.Next() {
		var r TaskRun
		if err := rows.Scan(&r.ID, &r.TaskID, &r.Trigger, &r.Status, &r.ScheduledFor, &r.ExitCode, &r.Output, &r.Error, &r.By, &r.CreatedAt, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetTaskRun(ctx context.Context, id string) (*TaskRun, error) {
	var r TaskRun
	err := s.DB.QueryRow(ctx, `
		SELECT r.id, r.task_id, r.trigger, r.status, r.scheduled_for, r.exit_code, r.output, r.error, COALESCE(u.email, ''), r.created_at, r.started_at, r.finished_at
		FROM task_runs r LEFT JOIN users u ON u.id = r.created_by WHERE r.id = $1`, id).
		Scan(&r.ID, &r.TaskID, &r.Trigger, &r.Status, &r.ScheduledFor, &r.ExitCode, &r.Output, &r.Error, &r.By, &r.CreatedAt, &r.StartedAt, &r.FinishedAt)
	return &r, noRows(err)
}

// QueueTaskRun asks the worker to run a task now. It refuses while one is already queued or running.
func (s *Store) QueueTaskRun(ctx context.Context, taskID, userID string) (string, bool, error) {
	var id string
	err := s.DB.QueryRow(ctx, `
		INSERT INTO task_runs (task_id, trigger, created_by)
		SELECT $1, 'manual', $2 WHERE NOT EXISTS (SELECT 1 FROM task_runs WHERE task_id = $1 AND status IN ('queued', 'running'))
		RETURNING id`, taskID, nullable(userID)).Scan(&id)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	return id, err == nil, err
}

func (s *Store) CancelTaskRun(ctx context.Context, id string) (string, error) {
	var status string
	err := s.DB.QueryRow(ctx, `
		UPDATE task_runs SET cancel_requested = true,
		       status = CASE WHEN status = 'queued' THEN 'cancelled' ELSE status END,
		       finished_at = CASE WHEN status = 'queued' THEN now() ELSE finished_at END
		WHERE id = $1 AND status IN ('queued', 'running') RETURNING status`, id).Scan(&status)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return status, err
}

/* -------------------------- Deploy hooks and tokens ------------------------- */

type ResourceToken struct {
	ID         string     `json:"id"`
	ResourceID string     `json:"resourceId"`
	Kind       string     `json:"kind"`
	Name       string     `json:"name"`
	Scope      string     `json:"scope"`
	Hint       string     `json:"hint"`
	CreatedBy  string     `json:"createdBy"`
	CreatorID  *string    `json:"-"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	LastUsedIP string     `json:"lastUsedIp"`
	Sealed     []byte     `json:"-"`
}

const tokenSelect = `
	SELECT t.id, t.resource_id, t.kind, t.name, t.scope, t.hint, COALESCE(u.email, ''), t.created_by, t.created_at, t.expires_at, t.last_used_at, t.last_used_ip, t.token_enc
	FROM resource_tokens t LEFT JOIN users u ON u.id = t.created_by`

func scanToken(row pgx.Row) (*ResourceToken, error) {
	var t ResourceToken
	err := row.Scan(&t.ID, &t.ResourceID, &t.Kind, &t.Name, &t.Scope, &t.Hint, &t.CreatedBy, &t.CreatorID, &t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt, &t.LastUsedIP, &t.Sealed)
	return &t, noRows(err)
}

func (s *Store) ListTokens(ctx context.Context, resourceID string) ([]ResourceToken, error) {
	rows, err := s.DB.Query(ctx, tokenSelect+` WHERE t.resource_id = $1 ORDER BY t.kind, t.created_at`, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ResourceToken{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// ResourceTokenByHash finds a hook or token by the SHA-256 of its secret.
func (s *Store) ResourceTokenByHash(ctx context.Context, hash []byte) (*ResourceToken, error) {
	return scanToken(s.DB.QueryRow(ctx, tokenSelect+` WHERE t.token_hash = $1`, hash))
}

func (s *Store) GetToken(ctx context.Context, id string) (*ResourceToken, error) {
	return scanToken(s.DB.QueryRow(ctx, tokenSelect+` WHERE t.id = $1`, id))
}

// CreateToken stores a token; for hooks it replaces the resource's existing hook.
func (s *Store) CreateToken(ctx context.Context, t ResourceToken, hash []byte, userID string) (string, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if t.Kind == "hook" {
		if _, err := tx.Exec(ctx, `DELETE FROM resource_tokens WHERE resource_id = $1 AND kind = 'hook'`, t.ResourceID); err != nil {
			return "", err
		}
	}
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO resource_tokens (resource_id, kind, name, scope, token_hash, token_enc, hint, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		t.ResourceID, t.Kind, t.Name, t.Scope, hash, t.Sealed, t.Hint, nullable(userID), t.ExpiresAt).Scan(&id); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func (s *Store) DeleteToken(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM resource_tokens WHERE id = $1`, id)
	return err
}

// TouchToken records use, at most once a minute per token to spare the database.
func (s *Store) TouchToken(ctx context.Context, id, ip string) {
	s.DB.Exec(ctx, `UPDATE resource_tokens SET last_used_at = now(), last_used_ip = $2
		WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute' OR last_used_ip <> $2)`, id, ip)
}

/* ------------------------------ Advanced runtime ---------------------------- */

type Advanced struct {
	Entrypoint   string            `json:"entrypoint"`
	Workdir      string            `json:"workdir"`
	User         string            `json:"user"`
	Labels       map[string]string `json:"labels"`
	ReadOnlyRoot bool              `json:"readOnlyRoot"`
	Init         bool              `json:"init"`
	Privileged   bool              `json:"privileged"`
}

func (s *Store) GetAdvanced(ctx context.Context, resourceID string) (Advanced, error) {
	var a Advanced
	var labels []byte
	err := s.DB.QueryRow(ctx, `SELECT entrypoint, workdir, run_as_user, labels, read_only_root, init_process, privileged FROM resources WHERE id = $1`, resourceID).
		Scan(&a.Entrypoint, &a.Workdir, &a.User, &labels, &a.ReadOnlyRoot, &a.Init, &a.Privileged)
	if err != nil {
		return a, noRows(err)
	}
	a.Labels = map[string]string{}
	json.Unmarshal(labels, &a.Labels)
	return a, nil
}

func (s *Store) PutAdvanced(ctx context.Context, resourceID, name string, a Advanced) error {
	labels, _ := json.Marshal(a.Labels)
	_, err := s.DB.Exec(ctx, `
		UPDATE resources SET name = $2, entrypoint = $3, workdir = $4, run_as_user = $5, labels = $6, read_only_root = $7, init_process = $8, privileged = $9, updated_at = now()
		WHERE id = $1`, resourceID, name, a.Entrypoint, a.Workdir, a.User, labels, a.ReadOnlyRoot, a.Init, a.Privileged)
	return err
}

// MoveResource puts a resource in another project's environment. Its domains, variables and volumes go with it.
func (s *Store) MoveResource(ctx context.Context, resourceID, projectID, environmentID string) error {
	_, err := s.DB.Exec(ctx, `UPDATE resources SET project_id = $2, environment_id = $3, updated_at = now() WHERE id = $1`, resourceID, projectID, environmentID)
	return err
}

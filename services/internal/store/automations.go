package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

type Automation struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Scope          string          `json:"scope"`
	ResourceID     *string         `json:"resourceId"`
	ResourceName   string          `json:"resourceName,omitempty"`
	Targets        json.RawMessage `json:"targets"`
	Trigger        json.RawMessage `json:"trigger"`
	Conditions     json.RawMessage `json:"conditions"`
	Steps          json.RawMessage `json:"steps"`
	Mode           string          `json:"mode"`
	Enabled        bool            `json:"enabled"`
	PausedReason   string          `json:"pausedReason"`
	EscalateAfter  int             `json:"escalateAfter"`
	MaxRunsPerHour int             `json:"maxRunsPerHour"`
	NotifySealed   []byte          `json:"-"`
	HasNotify      bool            `json:"hasNotify"`
	HasHook        bool            `json:"hasHook"`
	NextRunAt      *time.Time      `json:"nextRunAt"`
	Failures       int             `json:"failures"`
	CreatedBy      string          `json:"createdBy"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	LastRun        *AutomationRun  `json:"lastRun"`
}

const automationSelect = `
	SELECT a.id, a.name, a.description, a.scope, a.resource_id, COALESCE(r.name, ''), a.targets, a.trigger, a.conditions, a.steps, a.mode, a.enabled,
	       a.paused_reason, a.escalate_after, a.max_runs_per_hour, a.notify_url_enc, a.hook_hash IS NOT NULL, a.next_run_at, a.failures,
	       COALESCE(u.email, ''), a.created_at, a.updated_at
	FROM automations a LEFT JOIN resources r ON r.id = a.resource_id LEFT JOIN users u ON u.id = a.created_by`

func scanAutomation(row pgx.Row) (*Automation, error) {
	var a Automation
	err := row.Scan(&a.ID, &a.Name, &a.Description, &a.Scope, &a.ResourceID, &a.ResourceName, &a.Targets, &a.Trigger, &a.Conditions, &a.Steps, &a.Mode, &a.Enabled,
		&a.PausedReason, &a.EscalateAfter, &a.MaxRunsPerHour, &a.NotifySealed, &a.HasHook, &a.NextRunAt, &a.Failures, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt)
	a.HasNotify = len(a.NotifySealed) > 0
	return &a, noRows(err)
}

// AutomationFilter narrows listings: one resource's, or those that can act on a server.
type AutomationFilter struct {
	ResourceID string
	ServerID   string // server-scoped automations whose targets may include this server (resolved by the caller)
	Scope      string
}

func (s *Store) ListAutomations(ctx context.Context, f AutomationFilter) ([]Automation, error) {
	rows, err := s.DB.Query(ctx, automationSelect+`
		WHERE ($1 = '' OR a.resource_id::text = $1) AND ($2 = '' OR a.scope = $2)
		ORDER BY a.created_at`, f.ResourceID, f.Scope)
	if err != nil {
		return nil, err
	}
	out := []Automation{}
	for rows.Next() {
		a, err := scanAutomation(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *a)
	}
	rows.Close()
	for i := range out {
		if runs, err := s.AutomationRuns(ctx, out[i].ID, 1); err == nil && len(runs) > 0 {
			out[i].LastRun = &runs[0]
		}
	}
	return out, nil
}

func (s *Store) GetAutomation(ctx context.Context, id string) (*Automation, error) {
	return scanAutomation(s.DB.QueryRow(ctx, automationSelect+` WHERE a.id = $1`, id))
}

func (s *Store) AutomationByHook(ctx context.Context, hash []byte) (*Automation, error) {
	return scanAutomation(s.DB.QueryRow(ctx, automationSelect+` WHERE a.hook_hash = $1`, hash))
}

// SaveAutomation inserts (empty ID) or updates. notify: nil keeps the sealed URL, empty slice clears it.
func (s *Store) SaveAutomation(ctx context.Context, a *Automation, notify []byte, userID string) (string, error) {
	if a.ID == "" {
		var id string
		err := s.DB.QueryRow(ctx, `
			INSERT INTO automations (name, description, scope, resource_id, targets, trigger, conditions, steps, mode, enabled, escalate_after, max_runs_per_hour, notify_url_enc, next_run_at, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15) RETURNING id`,
			a.Name, a.Description, a.Scope, a.ResourceID, a.Targets, a.Trigger, a.Conditions, a.Steps, a.Mode, a.Enabled, a.EscalateAfter, a.MaxRunsPerHour,
			notify, a.NextRunAt, nullable(userID)).Scan(&id)
		return id, err
	}
	_, err := s.DB.Exec(ctx, `
		UPDATE automations SET name = $2, description = $3, targets = $4, trigger = $5, conditions = $6, steps = $7, mode = $8, enabled = $9,
		       escalate_after = $10, max_runs_per_hour = $11, notify_url_enc = CASE WHEN $12::bytea IS NULL THEN notify_url_enc WHEN length($12::bytea) = 0 THEN NULL ELSE $12::bytea END,
		       next_run_at = $13, paused_reason = CASE WHEN $9 THEN '' ELSE paused_reason END, failures = CASE WHEN $9 AND NOT enabled THEN 0 ELSE failures END,
		       updated_at = now()
		WHERE id = $1`, a.ID, a.Name, a.Description, a.Targets, a.Trigger, a.Conditions, a.Steps, a.Mode, a.Enabled, a.EscalateAfter, a.MaxRunsPerHour, notify, a.NextRunAt)
	return a.ID, err
}

func (s *Store) SetAutomationHook(ctx context.Context, id string, hash []byte) error {
	_, err := s.DB.Exec(ctx, `UPDATE automations SET hook_hash = $2 WHERE id = $1`, id, hash)
	return err
}

func (s *Store) DeleteAutomation(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM automations WHERE id = $1`, id)
	return err
}

/* ----------------------------------- Runs ----------------------------------- */

type AutomationRun struct {
	ID           string          `json:"id"`
	AutomationID string          `json:"automationId"`
	Automation   string          `json:"automation,omitempty"`
	Trigger      string          `json:"trigger"`
	Reason       string          `json:"reason"`
	Status       string          `json:"status"`
	Plan         json.RawMessage `json:"plan"`
	Targets      json.RawMessage `json:"targets"`
	Summary      string          `json:"summary"`
	Attention    bool            `json:"attention"`
	RequestedBy  string          `json:"requestedBy"`
	DecidedBy    string          `json:"decidedBy"`
	DecidedAt    *time.Time      `json:"decidedAt"`
	DecisionNote string          `json:"decisionNote"`
	CreatedAt    time.Time       `json:"createdAt"`
	StartedAt    *time.Time      `json:"startedAt"`
	FinishedAt   *time.Time      `json:"finishedAt"`
	ExpiresAt    *time.Time      `json:"expiresAt"`
	Steps        []RunStep       `json:"steps,omitempty"`
}

type RunStep struct {
	Seq          int        `json:"seq"`
	TargetKind   string     `json:"targetKind"`
	TargetID     string     `json:"targetId"`
	TargetName   string     `json:"targetName"`
	Action       string     `json:"action"`
	Status       string     `json:"status"`
	Output       string     `json:"output"`
	Verification string     `json:"verification"`
	Verified     *bool      `json:"verified"`
	Ref          string     `json:"ref"`
	StartedAt    *time.Time `json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt"`
}

const runSelect = `
	SELECT r.id, r.automation_id, a.name, r.trigger, r.reason, r.status, r.plan, r.targets, r.summary, r.attention, r.requested_by, r.decided_by, r.decided_at,
	       r.decision_note, r.created_at, r.started_at, r.finished_at, r.expires_at
	FROM automation_runs r JOIN automations a ON a.id = r.automation_id`

func scanRun(row pgx.Row) (*AutomationRun, error) {
	var r AutomationRun
	err := row.Scan(&r.ID, &r.AutomationID, &r.Automation, &r.Trigger, &r.Reason, &r.Status, &r.Plan, &r.Targets, &r.Summary, &r.Attention, &r.RequestedBy,
		&r.DecidedBy, &r.DecidedAt, &r.DecisionNote, &r.CreatedAt, &r.StartedAt, &r.FinishedAt, &r.ExpiresAt)
	return &r, noRows(err)
}

func (s *Store) collectRuns(rows pgx.Rows, err error) ([]AutomationRun, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AutomationRun{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) AutomationRuns(ctx context.Context, automationID string, limit int) ([]AutomationRun, error) {
	return s.collectRuns(s.DB.Query(ctx, runSelect+` WHERE r.automation_id = $1 ORDER BY r.created_at DESC LIMIT $2`, automationID, limit))
}

// RunsByIDs returns runs of the given automations that need someone: awaiting approval or flagged.
func (s *Store) AttentionRuns(ctx context.Context, automationIDs []string) ([]AutomationRun, error) {
	return s.collectRuns(s.DB.Query(ctx, runSelect+`
		WHERE r.automation_id = ANY($1::uuid[]) AND (r.status = 'awaiting_approval' OR r.attention)
		ORDER BY r.created_at DESC LIMIT 50`, automationIDs))
}

func (s *Store) GetAutomationRun(ctx context.Context, id string) (*AutomationRun, error) {
	r, err := scanRun(s.DB.QueryRow(ctx, runSelect+` WHERE r.id = $1`, id))
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `
		SELECT seq, target_kind, target_id, target_name, action, status, output, verification, verified, ref, started_at, finished_at
		FROM automation_run_steps WHERE run_id = $1 ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	r.Steps = []RunStep{}
	for rows.Next() {
		var st RunStep
		if err := rows.Scan(&st.Seq, &st.TargetKind, &st.TargetID, &st.TargetName, &st.Action, &st.Status, &st.Output, &st.Verification, &st.Verified, &st.Ref, &st.StartedAt, &st.FinishedAt); err != nil {
			return nil, err
		}
		r.Steps = append(r.Steps, st)
	}
	return r, rows.Err()
}

// CreateAutomationRun queues a run. Runs beyond the automation's hourly limit are recorded as skipped.
func (s *Store) CreateAutomationRun(ctx context.Context, automationID, trigger, reason, requestedBy string, targets any) (string, string, error) {
	tb, _ := json.Marshal(targets)
	if string(tb) == "null" {
		tb = []byte("[]")
	}
	var id, status string
	err := s.DB.QueryRow(ctx, `
		INSERT INTO automation_runs (automation_id, trigger, reason, requested_by, targets, status, summary, finished_at)
		SELECT $1, $2, $3, $4, $5,
		       CASE WHEN over THEN 'skipped' ELSE 'queued' END,
		       CASE WHEN over THEN 'Skipped: it already ran ' || a.max_runs_per_hour || CASE WHEN a.max_runs_per_hour = 1 THEN ' time' ELSE ' times' END || ' in the last hour (its limit).' ELSE '' END,
		       CASE WHEN over THEN now() END
		FROM automations a, LATERAL (SELECT count(*) >= a.max_runs_per_hour AS over FROM automation_runs r
		     WHERE r.automation_id = a.id AND r.created_at > now() - interval '1 hour' AND r.status NOT IN ('skipped', 'rejected', 'expired')) x
		WHERE a.id = $1
		RETURNING id, status`, automationID, trigger, reason, requestedBy, tb).Scan(&id, &status)
	return id, status, err
}

func (s *Store) DecideRun(ctx context.Context, id, status, by, note string) (bool, error) {
	tag, err := s.DB.Exec(ctx, `
		UPDATE automation_runs SET status = $2, decided_by = $3, decided_at = now(), decision_note = $4,
		       finished_at = CASE WHEN $2 = 'rejected' THEN now() ELSE finished_at END
		WHERE id = $1 AND status = 'awaiting_approval'`, id, status, by, note)
	return tag.RowsAffected() == 1, err
}

func (s *Store) AcknowledgeRun(ctx context.Context, id, by string) error {
	_, err := s.DB.Exec(ctx, `UPDATE automation_runs SET attention = false, decided_by = CASE WHEN decided_by = '' THEN $2 ELSE decided_by END WHERE id = $1`, id, by)
	return err
}

func (s *Store) CancelAutomationRun(ctx context.Context, id string) (bool, error) {
	tag, err := s.DB.Exec(ctx, `UPDATE automation_runs SET status = 'cancelled', finished_at = now(), attention = false
		WHERE id = $1 AND status IN ('queued', 'awaiting_approval', 'approved')`, id)
	return tag.RowsAffected() == 1, err
}

// AutomationCounts are the numbers at the top of an Automations page.
type AutomationCounts struct {
	Active    int `json:"active"`
	Attention int `json:"attention"`
	Approvals int `json:"approvals"`
	Runs24h   int `json:"runs24h"`
}

func (s *Store) CountAutomations(ctx context.Context, ids []string) (AutomationCounts, error) {
	var c AutomationCounts
	err := s.DB.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM automations WHERE id = ANY($1::uuid[]) AND enabled),
		       (SELECT count(*) FROM automation_runs WHERE automation_id = ANY($1::uuid[]) AND attention)
		         + (SELECT count(*) FROM automations WHERE id = ANY($1::uuid[]) AND NOT enabled AND paused_reason <> ''),
		       (SELECT count(*) FROM automation_runs WHERE automation_id = ANY($1::uuid[]) AND status = 'awaiting_approval'),
		       (SELECT count(*) FROM automation_runs WHERE automation_id = ANY($1::uuid[]) AND created_at > now() - interval '24 hours')`, ids).
		Scan(&c.Active, &c.Attention, &c.Approvals, &c.Runs24h)
	return c, err
}

package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

/*
 * Project-wide views: every deployment, metric and audit event of a project's resources, and
 * its environments. Pages filter by environment and resource; nothing here is per resource only.
 */

// deploymentsWhere lists deployments matching cond (written with $1..$n for args), newest first,
// with the resource, project and environment names. q.Q, limit and offset follow as $n+1...
func (s *Store) deploymentsWhere(ctx context.Context, cond string, args []any, q DeploymentQuery) ([]ServerDeployment, int, error) {
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}
	status := map[string]string{"active": `d.status IN ('queued', 'building', 'deploying')`, "successful": `d.status = 'successful'`,
		"failed": `d.status = 'failed'`, "cancelled": `d.status = 'cancelled'`}[q.Status]
	if status == "" {
		status = "true"
	}
	n := len(args)
	qa, la, oa := fmt.Sprintf("$%d", n+1), fmt.Sprintf("$%d", n+2), fmt.Sprintf("$%d", n+3)
	sql := strings.Replace(deploymentSelect, "\n\tFROM deployments d", `, r.name, r.type, p.id, p.name, e.name, e.id, count(*) OVER ()
	FROM deployments d`, 1) + `
		JOIN projects p ON p.id = r.project_id JOIN environments e ON e.id = r.environment_id
		WHERE ` + cond + ` AND ` + status + ` AND (` + qa + ` = '' OR r.name ILIKE '%' || ` + qa + ` || '%' OR p.name ILIKE '%' || ` + qa + ` || '%' OR d.commit_sha ILIKE ` + qa + ` || '%'
		      OR d.image_ref ILIKE '%' || ` + qa + ` || '%' OR d.error ILIKE '%' || ` + qa + ` || '%' OR d.reason ILIKE '%' || ` + qa + ` || '%' OR d.id::text = ` + qa + `)
		ORDER BY d.created_at DESC LIMIT ` + la + ` OFFSET ` + oa
	rows, err := s.DB.Query(ctx, sql, append(args, q.Q, q.Limit, max(q.Offset, 0))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []ServerDeployment{}
	total := 0
	for rows.Next() {
		var d ServerDeployment
		err := rows.Scan(&d.ID, &d.ResourceID, &d.Number, &d.Status, &d.Trigger, &d.ImageRef, &d.CommitSHA, &d.Branch, &d.Reason, &d.Error,
			&d.Author, &d.JobID, &d.Current, &d.ImageID, &d.Previous, &d.RestoreEnv, &d.EnvKept, &d.CreatedAt, &d.StartedAt, &d.FinishedAt,
			&d.ResourceName, &d.ResourceType, &d.ProjectID, &d.ProjectName, &d.Environment, &d.EnvironmentID, &total)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, d)
	}
	return out, total, rows.Err()
}

// ListProjectDeployments: envID and resourceID narrow it down when set.
func (s *Store) ListProjectDeployments(ctx context.Context, projectID, envID, resourceID string, q DeploymentQuery) ([]ServerDeployment, int, error) {
	return s.deploymentsWhere(ctx, `r.project_id = $1 AND ($2 = '' OR e.id::text = $2) AND ($3 = '' OR r.id::text = $3)`,
		[]any{projectID, envID, resourceID}, q)
}

// DeployStats summarise a project's deployments over a window.
type DeployStats struct {
	Total        int        `json:"total"`
	Successful   int        `json:"successful"`
	Failed       int        `json:"failed"`
	Cancelled    int        `json:"cancelled"`
	Active       int        `json:"active"`
	AvgSeconds   *float64   `json:"avgSeconds"` // successful ones, queue to finish
	LastFailedAt *time.Time `json:"lastFailedAt"`
	Daily        []DayCount `json:"daily"` // oldest first, one per day of the window
}

type DayCount struct {
	Day        time.Time `json:"day"`
	Successful int       `json:"successful"`
	Failed     int       `json:"failed"`
}

func (s *Store) ProjectDeployStats(ctx context.Context, projectID, envID string, days int) (DeployStats, error) {
	var st DeployStats
	err := s.DB.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE d.status = 'successful'), count(*) FILTER (WHERE d.status = 'failed'),
		       count(*) FILTER (WHERE d.status = 'cancelled'), count(*) FILTER (WHERE d.status IN ('queued', 'building', 'deploying')),
		       avg(extract(epoch FROM d.finished_at - d.created_at)) FILTER (WHERE d.status = 'successful' AND d.finished_at IS NOT NULL),
		       max(d.created_at) FILTER (WHERE d.status = 'failed')
		FROM deployments d JOIN resources r ON r.id = d.resource_id
		WHERE r.project_id = $1 AND ($2 = '' OR r.environment_id::text = $2) AND d.created_at > now() - make_interval(days => $3)`,
		projectID, envID, days).Scan(&st.Total, &st.Successful, &st.Failed, &st.Cancelled, &st.Active, &st.AvgSeconds, &st.LastFailedAt)
	if err != nil {
		return st, err
	}
	rows, err := s.DB.Query(ctx, `
		SELECT g.day, count(d.id) FILTER (WHERE d.status = 'successful'), count(d.id) FILTER (WHERE d.status = 'failed')
		FROM generate_series(date_trunc('day', now()) - make_interval(days => $3 - 1), date_trunc('day', now()), interval '1 day') g(day)
		LEFT JOIN (deployments d JOIN resources r ON r.id = d.resource_id AND r.project_id = $1 AND ($2 = '' OR r.environment_id::text = $2))
		       ON date_trunc('day', d.created_at) = g.day
		GROUP BY g.day ORDER BY g.day`, projectID, envID, days)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	st.Daily = []DayCount{}
	for rows.Next() {
		var d DayCount
		if err := rows.Scan(&d.Day, &d.Successful, &d.Failed); err != nil {
			return st, err
		}
		st.Daily = append(st.Daily, d)
	}
	return st, rows.Err()
}

/* --------------------------------- Metrics ----------------------------------- */

// ResourceUsage is one resource's series plus its newest sample, for project monitoring.
type ResourceUsage struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Type        string        `json:"type"`
	Status      string        `json:"status"`
	Environment string        `json:"environment"`
	MemoryLimit int64         `json:"memoryLimit"` // bytes, from its settings
	CPUs        float64       `json:"cpus"`
	RunningAt   *time.Time    `json:"runningSince"`
	Latest      *UsagePoint   `json:"latest"` // nil when the newest sample is older than a minute
	Series      []*UsagePoint `json:"series"` // one per step; nil entries mean no sample
}

type UsagePoint struct {
	At     time.Time `json:"at"`
	CPU    float64   `json:"cpu"`
	Memory float64   `json:"memory"`
	RxBps  *float64  `json:"rxBps"`
	TxBps  *float64  `json:"txBps"`
}

func (s *Store) ProjectUsage(ctx context.Context, projectID, envID string, window, step time.Duration) ([]time.Time, []ResourceUsage, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT r.id, r.name, r.type, r.status, e.name, r.memory_mb::bigint << 20, r.cpus::float8, r.running_since
		FROM resources r JOIN environments e ON e.id = r.environment_id
		WHERE r.project_id = $1 AND ($2 = '' OR e.id::text = $2) AND r.source_type <> 'external'
		ORDER BY e.position, r.name`, projectID, envID)
	if err != nil {
		return nil, nil, err
	}
	list := []ResourceUsage{}
	idx := map[string]int{}
	for rows.Next() {
		var u ResourceUsage
		if err := rows.Scan(&u.ID, &u.Name, &u.Type, &u.Status, &u.Environment, &u.MemoryLimit, &u.CPUs, &u.RunningAt); err != nil {
			rows.Close()
			return nil, nil, err
		}
		idx[u.ID] = len(list)
		list = append(list, u)
	}
	rows.Close()
	// The buckets every series shares, oldest first.
	var buckets []time.Time
	brows, err := s.DB.Query(ctx, `SELECT generate_series(date_bin($2::interval, now() - $1::interval, 'epoch'), now(), $2::interval)`, interval(window), interval(step))
	if err != nil {
		return nil, nil, err
	}
	for brows.Next() {
		var t time.Time
		brows.Scan(&t)
		buckets = append(buckets, t)
	}
	brows.Close()
	pos := map[int64]int{}
	for i, b := range buckets {
		pos[b.Unix()] = i
	}
	for i := range list {
		list[i].Series = make([]*UsagePoint, len(buckets))
	}
	srows, err := s.DB.Query(ctx, `
		SELECT m.resource_id, date_bin($3::interval, m.at, 'epoch'), avg(m.cpu_percent)::float8, avg(m.memory_bytes)::float8, avg(m.net_rx_bps)::float8, avg(m.net_tx_bps)::float8
		FROM metric_samples m JOIN resources r ON r.id = m.resource_id
		WHERE r.project_id = $1 AND ($4 = '' OR r.environment_id::text = $4) AND m.at >= now() - $2::interval - $3::interval
		GROUP BY 1, 2`, projectID, interval(window), interval(step), envID)
	if err != nil {
		return nil, nil, err
	}
	for srows.Next() {
		var id string
		p := &UsagePoint{}
		if err := srows.Scan(&id, &p.At, &p.CPU, &p.Memory, &p.RxBps, &p.TxBps); err != nil {
			srows.Close()
			return nil, nil, err
		}
		if i, ok := idx[id]; ok {
			if b, ok := pos[p.At.Unix()]; ok {
				list[i].Series[b] = p
			}
		}
	}
	srows.Close()
	lrows, err := s.DB.Query(ctx, `
		SELECT DISTINCT ON (m.resource_id) m.resource_id, m.at, m.cpu_percent::float8, m.memory_bytes::float8, m.net_rx_bps::float8, m.net_tx_bps::float8
		FROM metric_samples m JOIN resources r ON r.id = m.resource_id
		WHERE r.project_id = $1 AND m.at > now() - interval '60 seconds' ORDER BY m.resource_id, m.at DESC`, projectID)
	if err != nil {
		return nil, nil, err
	}
	defer lrows.Close()
	for lrows.Next() {
		var id string
		p := &UsagePoint{}
		if err := lrows.Scan(&id, &p.At, &p.CPU, &p.Memory, &p.RxBps, &p.TxBps); err != nil {
			return nil, nil, err
		}
		if i, ok := idx[id]; ok {
			list[i].Latest = p
		}
	}
	return buckets, list, lrows.Err()
}

/* --------------------------------- Activity ---------------------------------- */

// ProjectActivity is the audit trail of the project and everything in it.
func (s *Store) ProjectActivity(ctx context.Context, projectID string, limit int) ([]AuditEvent, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT a.id, a.actor_email, a.action, a.target_type, a.target_id, a.detail, a.ip, a.created_at FROM audit_events a
		WHERE a.target_id = $1 OR a.target_id IN (SELECT id::text FROM resources WHERE project_id::text = $1)
		ORDER BY a.id DESC LIMIT $2`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var e AuditEvent
		if err := rows.Scan(&e.ID, &e.ActorEmail, &e.Action, &e.TargetType, &e.TargetID, &e.Detail, &e.IP, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

/* ------------------------------- Environments -------------------------------- */

type EnvironmentRow struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Position  int       `json:"position"`
	Resources int       `json:"resources"`
	Running   int       `json:"running"`
	Domains   int       `json:"domains"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) ProjectEnvironments(ctx context.Context, projectID string) ([]EnvironmentRow, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT e.id, e.name, e.position, count(r.id), count(r.id) FILTER (WHERE r.status = 'running'),
		       (SELECT count(*) FROM domains d JOIN resources x ON x.id = d.resource_id WHERE x.environment_id = e.id), e.created_at
		FROM environments e LEFT JOIN resources r ON r.environment_id = e.id
		WHERE e.project_id = $1 GROUP BY e.id ORDER BY e.position, e.created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EnvironmentRow{}
	for rows.Next() {
		var e EnvironmentRow
		if err := rows.Scan(&e.ID, &e.Name, &e.Position, &e.Resources, &e.Running, &e.Domains, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) CreateEnvironment(ctx context.Context, projectID, name string) (string, error) {
	var id string
	err := s.DB.QueryRow(ctx, `
		INSERT INTO environments (project_id, name, position)
		VALUES ($1, $2, COALESCE((SELECT max(position) + 1 FROM environments WHERE project_id = $1), 0)) RETURNING id`, projectID, name).Scan(&id)
	return id, err
}

// EnvironmentProject returns the project an environment belongs to.
func (s *Store) EnvironmentProject(ctx context.Context, id string) (projectID, name string, resources int, err error) {
	err = s.DB.QueryRow(ctx, `SELECT e.project_id, e.name, (SELECT count(*) FROM resources r WHERE r.environment_id = e.id) FROM environments e WHERE e.id = $1`, id).
		Scan(&projectID, &name, &resources)
	return projectID, name, resources, noRows(err)
}

// EnvironmentNameTaken: environment names are unique per project regardless of case.
func (s *Store) EnvironmentNameTaken(ctx context.Context, projectID, name, exceptID string) bool {
	var n int
	s.DB.QueryRow(ctx, `SELECT count(*) FROM environments WHERE project_id = $1 AND lower(name) = lower($2) AND id::text <> $3`, projectID, name, exceptID).Scan(&n)
	return n > 0
}

func (s *Store) RenameEnvironment(ctx context.Context, id, name string) error {
	_, err := s.DB.Exec(ctx, `UPDATE environments SET name = $2 WHERE id = $1`, id, name)
	return err
}

// ReorderEnvironments sets positions in the given order; the first is the project's default.
func (s *Store) ReorderEnvironments(ctx context.Context, projectID string, ids []string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for i, id := range ids {
		if _, err := tx.Exec(ctx, `UPDATE environments SET position = $3 WHERE id = $1 AND project_id = $2`, id, projectID, i); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// DeleteEnvironment refuses while resources live in it, and never deletes a project's last one.
func (s *Store) DeleteEnvironment(ctx context.Context, id string) error {
	tag, err := s.DB.Exec(ctx, `
		DELETE FROM environments e WHERE e.id = $1
		AND NOT EXISTS (SELECT 1 FROM resources r WHERE r.environment_id = e.id)
		AND (SELECT count(*) FROM environments x WHERE x.project_id = e.project_id) > 1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

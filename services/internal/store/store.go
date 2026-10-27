// Package store contains all SQL. Handlers and the worker call these typed functions
// instead of writing queries, so business rules live in one place.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

// interval formats a duration for a ::interval parameter.
func interval(d time.Duration) string { return fmt.Sprintf("%d seconds", int(d.Seconds())) }

type Store struct{ DB *pgxpool.Pool }

// IsUnique reports a unique-constraint violation, for friendly "already exists" errors.
func IsUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func noRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

/* ---------------------------------- Users ---------------------------------- */

type User struct {
	ID           string     `json:"id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	Role         string     `json:"role"`
	Status       string     `json:"status"`
	PasswordHash string     `json:"-"`
	FailedLogins int        `json:"-"`
	LockedUntil  *time.Time `json:"-"`
	CreatedAt    time.Time  `json:"createdAt"`
}

const userCols = `id, email, name, role, status, password_hash, failed_logins, locked_until, created_at`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Status, &u.PasswordHash, &u.FailedLogins, &u.LockedUntil, &u.CreatedAt)
	return &u, noRows(err)
}

func (s *Store) CountUsers(ctx context.Context) (n int, err error) {
	err = s.DB.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return
}

func (s *Store) CreateUser(ctx context.Context, email, name, hash, role string) (*User, error) {
	return scanUser(s.DB.QueryRow(ctx, `INSERT INTO users (email, name, password_hash, role) VALUES ($1, $2, $3, $4) RETURNING `+userCols, email, name, hash, role))
}

// CreateFirstOwner creates the initial owner only if no user exists, atomically.
func (s *Store) CreateFirstOwner(ctx context.Context, email, name, hash string) (*User, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `LOCK TABLE users IN EXCLUSIVE MODE`); err != nil {
		return nil, err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, ErrNotFound
	}
	u, err := scanUser(tx.QueryRow(ctx, `INSERT INTO users (email, name, password_hash, role) VALUES ($1, $2, $3, 'owner') RETURNING `+userCols, email, name, hash))
	if err != nil {
		return nil, err
	}
	return u, tx.Commit(ctx)
}

func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.DB.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE lower(email) = lower($1)`, email))
}

func (s *Store) RecordFailedLogin(ctx context.Context, id string, maxAttempts int, lockFor time.Duration) error {
	_, err := s.DB.Exec(ctx, `
		UPDATE users SET failed_logins = failed_logins + 1,
		       locked_until = CASE WHEN failed_logins + 1 >= $2 THEN now() + $3::interval ELSE locked_until END
		WHERE id = $1`, id, maxAttempts, interval(lockFor))
	return err
}

func (s *Store) ResetFailedLogins(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `UPDATE users SET failed_logins = 0, locked_until = NULL WHERE id = $1`, id)
	return err
}

/* --------------------------------- Sessions -------------------------------- */

func (s *Store) CreateSession(ctx context.Context, userID string, tokenHash []byte, ua, ip string, ttl time.Duration) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO sessions (user_id, token_hash, user_agent, ip, expires_at) VALUES ($1, $2, $3, $4, now() + $5::interval)`,
		userID, tokenHash, ua, ip, interval(ttl))
	return err
}

// UserBySession returns the session's active user and slides last_seen_at forward.
func (s *Store) UserBySession(ctx context.Context, tokenHash []byte) (*User, error) {
	return scanUser(s.DB.QueryRow(ctx, `
		WITH touched AS (
			UPDATE sessions SET last_seen_at = now() WHERE token_hash = $1 AND expires_at > now() RETURNING user_id
		)
		SELECT `+prefix("u.", userCols)+` FROM users u JOIN touched t ON t.user_id = u.id WHERE u.status = 'active'`, tokenHash))
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

func prefix(p, cols string) string {
	out := ""
	for i, c := range splitCols(cols) {
		if i > 0 {
			out += ", "
		}
		out += p + c
	}
	return out
}

func splitCols(cols string) []string {
	var out []string
	cur := ""
	for _, r := range cols {
		switch r {
		case ',':
			out = append(out, cur)
			cur = ""
		case ' ', '\n', '\t':
		default:
			cur += string(r)
		}
	}
	return append(out, cur)
}

/* --------------------------------- Projects -------------------------------- */

type Environment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Project struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Description   string        `json:"description"`
	Archived      bool          `json:"archived"`
	Favorite      bool          `json:"favorite"`
	Environments  []Environment `json:"environments"`
	ResourceCount int           `json:"resourceCount"`
	Running       int           `json:"running"`
	Failed        int           `json:"failed"`
	Deploying     int           `json:"deploying"`
	LastDeployAt  *time.Time    `json:"lastDeployAt"`
	CreatedAt     time.Time     `json:"createdAt"`
}

const projectSelect = `
	SELECT p.id, p.name, p.description, p.archived_at IS NOT NULL,
	       EXISTS (SELECT 1 FROM project_favorites f WHERE f.project_id = p.id AND f.user_id = $1),
	       COALESCE((SELECT json_agg(json_build_object('id', e.id, 'name', e.name) ORDER BY e.position, e.name) FROM environments e WHERE e.project_id = p.id), '[]'),
	       (SELECT count(*) FROM resources r WHERE r.project_id = p.id),
	       (SELECT count(*) FROM resources r WHERE r.project_id = p.id AND r.status = 'running'),
	       (SELECT count(*) FROM resources r WHERE r.project_id = p.id AND r.status = 'failed'),
	       (SELECT count(*) FROM resources r WHERE r.project_id = p.id AND r.status = 'deploying'),
	       (SELECT max(d.created_at) FROM deployments d JOIN resources r ON r.id = d.resource_id WHERE r.project_id = p.id),
	       p.created_at
	FROM projects p`

func scanProject(row pgx.Row) (*Project, error) {
	var p Project
	var envs []byte
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.Archived, &p.Favorite, &envs, &p.ResourceCount, &p.Running, &p.Failed, &p.Deploying, &p.LastDeployAt, &p.CreatedAt)
	if err != nil {
		return nil, noRows(err)
	}
	json.Unmarshal(envs, &p.Environments)
	return &p, nil
}

func (s *Store) ListProjects(ctx context.Context, userID, query string, archived bool) ([]Project, error) {
	rows, err := s.DB.Query(ctx, projectSelect+`
		WHERE (p.archived_at IS NOT NULL) = $2 AND ($3 = '' OR p.name ILIKE '%' || $3 || '%' OR p.description ILIKE '%' || $3 || '%')
		ORDER BY 5 DESC, p.name`, userID, archived, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *Store) GetProject(ctx context.Context, userID, id string) (*Project, error) {
	return scanProject(s.DB.QueryRow(ctx, projectSelect+` WHERE p.id = $2`, userID, id))
}

// CreateProject creates the project with a default Production environment in one transaction.
func (s *Store) CreateProject(ctx context.Context, userID, name, description string) (string, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO projects (name, description, created_by) VALUES ($1, $2, $3) RETURNING id`, name, description, nullable(userID)).Scan(&id); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO environments (project_id, name, position) VALUES ($1, 'Production', 0)`, id); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func (s *Store) UpdateProject(ctx context.Context, id string, name, description *string, archived *bool) error {
	tag, err := s.DB.Exec(ctx, `
		UPDATE projects SET name = COALESCE($2, name), description = COALESCE($3, description),
		       archived_at = CASE WHEN $4::boolean IS NULL THEN archived_at WHEN $4 THEN COALESCE(archived_at, now()) ELSE NULL END,
		       updated_at = now()
		WHERE id = $1`, id, name, description, archived)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) SetFavorite(ctx context.Context, projectID, userID string, on bool) error {
	var err error
	if on {
		_, err = s.DB.Exec(ctx, `INSERT INTO project_favorites (project_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, projectID, userID)
	} else {
		_, err = s.DB.Exec(ctx, `DELETE FROM project_favorites WHERE project_id = $1 AND user_id = $2`, projectID, userID)
	}
	return err
}

func (s *Store) DeleteProject(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id)
	return err
}

/* -------------------------------- Resources -------------------------------- */

type Resource struct {
	ID                  string     `json:"id"`
	ProjectID           string     `json:"projectId"`
	EnvironmentID       string     `json:"environmentId"`
	Environment         string     `json:"environment"`
	Name                string     `json:"name"`
	Type                string     `json:"type"`
	SourceType          string     `json:"sourceType"`
	Image               string     `json:"image"`
	RepoURL             string     `json:"repoUrl"`
	Branch              string     `json:"branch"`
	Dockerfile          string     `json:"dockerfile"`
	Port                *int       `json:"port"`
	Command             string     `json:"command"`
	DataPath            string     `json:"dataPath"`
	MemoryMB            int        `json:"memoryMb"`
	CPUs                float64    `json:"cpus"`
	DesiredState        string     `json:"desiredState"`
	Status              string     `json:"status"`
	ContainerID         string     `json:"-"`
	HostPort            *int       `json:"hostPort"`
	CurrentDeploymentID *string    `json:"currentDeploymentId"`
	RunningSince        *time.Time `json:"runningSince"`
	SourceID            *string    `json:"sourceId"`
	Domain              string     `json:"domain"`  // primary working domain as a URL, "" when none
	Volumes             []string   `json:"volumes"` // mount paths of its volumes
	ProjectName         string     `json:"projectName,omitempty"`
	Usage               *Usage     `json:"usage,omitempty"`
	LastDeployAt        *time.Time `json:"lastDeployAt"`
	ServerID            string     `json:"serverId"`
	Engine              string     `json:"engine"` // database engine or function runtime
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

// ProxyHTTPPort and ProxyHTTPSPort are the host ports of the domain proxy, for building domain URLs.
var ProxyHTTPPort, ProxyHTTPSPort = 80, 443

func withPort(url *string) string {
	switch {
	case url == nil:
		return ""
	case strings.HasPrefix(*url, "https://") && ProxyHTTPSPort != 443:
		return fmt.Sprintf("%s:%d", *url, ProxyHTTPSPort)
	case strings.HasPrefix(*url, "http://") && ProxyHTTPPort != 80:
		return fmt.Sprintf("%s:%d", *url, ProxyHTTPPort)
	}
	return *url
}

const resourceSelect = `
	SELECT r.id, r.project_id, r.environment_id, e.name, r.name, r.type, r.source_type, r.image, r.repo_url, r.branch, r.dockerfile,
	       r.port, r.command, r.data_path, r.memory_mb, r.cpus::float8, r.desired_state, r.status, r.container_id, r.host_port, r.current_deployment_id, r.running_since, r.source_id,
	       (SELECT CASE WHEN d.https THEN 'https://' ELSE 'http://' END || d.hostname FROM domains d WHERE d.resource_id = r.id AND d.status = 'active' ORDER BY d.is_primary DESC, d.created_at LIMIT 1),
	       COALESCE((SELECT array_agg(v.mount_path ORDER BY v.created_at, v.name) FROM resource_volumes v WHERE v.resource_id = r.id), '{}'),
	       (SELECT max(d.created_at) FROM deployments d WHERE d.resource_id = r.id), r.server_id, r.engine, r.created_at, r.updated_at
	FROM resources r JOIN environments e ON e.id = r.environment_id`

func scanResource(row pgx.Row) (*Resource, error) {
	var r Resource
	var domain *string
	err := row.Scan(&r.ID, &r.ProjectID, &r.EnvironmentID, &r.Environment, &r.Name, &r.Type, &r.SourceType, &r.Image, &r.RepoURL, &r.Branch, &r.Dockerfile,
		&r.Port, &r.Command, &r.DataPath, &r.MemoryMB, &r.CPUs, &r.DesiredState, &r.Status, &r.ContainerID, &r.HostPort, &r.CurrentDeploymentID, &r.RunningSince, &r.SourceID, &domain, &r.Volumes, &r.LastDeployAt, &r.ServerID, &r.Engine, &r.CreatedAt, &r.UpdatedAt)
	r.Domain = withPort(domain)
	return &r, noRows(err)
}

func (s *Store) ListResources(ctx context.Context, projectID, environmentID, query, typ string) ([]Resource, error) {
	rows, err := s.DB.Query(ctx, resourceSelect+`
		WHERE r.project_id = $1 AND ($2 = '' OR r.environment_id::text = $2) AND ($3 = '' OR r.name ILIKE '%' || $3 || '%' OR r.image ILIKE '%' || $3 || '%' OR r.repo_url ILIKE '%' || $3 || '%')
		  AND ($4 = '' OR r.type = $4)
		ORDER BY r.type, r.name`, projectID, environmentID, query, typ)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Resource{}
	for rows.Next() {
		r, err := scanResource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) GetResource(ctx context.Context, id string) (*Resource, error) {
	return scanResource(s.DB.QueryRow(ctx, resourceSelect+` WHERE r.id = $1`, id))
}

type NewResource struct {
	ProjectID, EnvironmentID, Name, Type, SourceType, Image, RepoURL, Branch, Dockerfile, Command, DataPath, CreatedBy, SourceID, Engine string
	Port                                                                                                                         *int
	MemoryMB                                                                                                                     int
	CPUs                                                                                                                         float64
	Defaults                                                                                                                     *ResourceDefaults // instance defaults for settings without their own input
}

func (s *Store) CreateResource(ctx context.Context, tx pgx.Tx, r NewResource) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO resources (project_id, environment_id, name, type, source_type, image, repo_url, branch, dockerfile, port, command, data_path, memory_mb, cpus, created_by, source_id, engine)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17) RETURNING id`,
		r.ProjectID, r.EnvironmentID, r.Name, r.Type, r.SourceType, r.Image, r.RepoURL, r.Branch, r.Dockerfile, r.Port, r.Command, r.DataPath, r.MemoryMB, r.CPUs, nullable(r.CreatedBy), nullable(r.SourceID), r.Engine).Scan(&id)
	if err == nil && r.Defaults != nil {
		d := r.Defaults
		_, err = tx.Exec(ctx, `UPDATE resources SET health_seconds = $2, stop_grace_seconds = $3, restart_policy = $4, build_mode = $5 WHERE id = $1`,
			id, d.HealthSeconds, d.StopGrace, d.RestartPolicy, d.BuildMode)
	}
	if err == nil && r.DataPath != "" {
		_, err = tx.Exec(ctx, `INSERT INTO resource_volumes (resource_id, name, mount_path) VALUES ($1, 'data', $2)`, id, r.DataPath)
	}
	if err == nil && (r.Type == "application" || r.Type == "function") && r.Port != nil {
		// Private by default: applications get a random host port on this machine only; databases and workers get none.
		_, err = tx.Exec(ctx, `INSERT INTO resource_ports (resource_id, container_port, bind_address) VALUES ($1, $2, '127.0.0.1')`, id, *r.Port)
	}
	return id, err
}

type ResourcePatch struct {
	Image      *string  `json:"image"`
	RepoURL    *string  `json:"repoUrl"`
	Branch     *string  `json:"branch"`
	Dockerfile *string  `json:"dockerfile"`
	Port       *int     `json:"port"`
	Command    *string  `json:"command"`
	MemoryMB   *int     `json:"memoryMb"`
	CPUs       *float64 `json:"cpus"`
	SourceID   *string  `json:"sourceId"` // "" unlinks the source
}

func (s *Store) UpdateResource(ctx context.Context, id string, p ResourcePatch) error {
	_, err := s.DB.Exec(ctx, `
		UPDATE resources SET image = COALESCE($2, image), repo_url = COALESCE($3, repo_url), branch = COALESCE($4, branch),
		       dockerfile = COALESCE($5, dockerfile), port = COALESCE($6, port), command = COALESCE($7, command),
		       memory_mb = COALESCE($8, memory_mb), cpus = COALESCE($9, cpus),
		       source_id = CASE WHEN $10::text IS NULL THEN source_id WHEN $10 = '' THEN NULL ELSE $10::uuid END, updated_at = now()
		WHERE id = $1`, id, p.Image, p.RepoURL, p.Branch, p.Dockerfile, p.Port, p.Command, p.MemoryMB, p.CPUs, p.SourceID)
	return err
}

// SetRuntimeState is written by the worker from what Docker reports.
func (s *Store) SetRuntimeState(ctx context.Context, id, status, containerID string, hostPort *int) error {
	_, err := s.DB.Exec(ctx, `UPDATE resources SET status = $2, container_id = $3, host_port = $4, updated_at = now() WHERE id = $1`, id, status, containerID, hostPort)
	return err
}

func (s *Store) SetStatus(ctx context.Context, id, status string) error {
	_, err := s.DB.Exec(ctx, `UPDATE resources SET status = $2, updated_at = now() WHERE id = $1`, id, status)
	return err
}

func (s *Store) SetDesiredState(ctx context.Context, id, desired string) error {
	_, err := s.DB.Exec(ctx, `UPDATE resources SET desired_state = $2, updated_at = now() WHERE id = $1`, id, desired)
	return err
}

// SetRunningSince records when the current container started (nil when it isn't running).
func (s *Store) SetRunningSince(ctx context.Context, id string, since *time.Time) error {
	_, err := s.DB.Exec(ctx, `UPDATE resources SET running_since = $2 WHERE id = $1 AND running_since IS DISTINCT FROM $2`, id, since)
	return err
}

// Usage is the newest metric sample for a resource, if one was taken in the last minute.
type Usage struct {
	CPUPercent  float64   `json:"cpuPercent"`
	MemoryBytes int64     `json:"memoryBytes"`
	At          time.Time `json:"at"`
}

// ListAllResources is the cross-project inventory used by Monitor, with live usage attached.
func (s *Store) ListAllResources(ctx context.Context, query, typ string) ([]Resource, error) {
	return s.listResourcesWithUsage(ctx, query, typ, "")
}

// ListServerResources is every workload assigned to a server, archived projects included.
func (s *Store) ListServerResources(ctx context.Context, serverID string) ([]Resource, error) {
	return s.listResourcesWithUsage(ctx, "", "", serverID)
}

func (s *Store) listResourcesWithUsage(ctx context.Context, query, typ, serverID string) ([]Resource, error) {
	// Same columns as resourceSelect, plus the project name and the newest sample.
	sql := strings.Replace(resourceSelect, "\n\tFROM resources r", ", p.name, m.cpu_percent, m.memory_bytes, m.at\n\tFROM resources r", 1) + `
		JOIN projects p ON p.id = r.project_id
		LEFT JOIN LATERAL (
			SELECT cpu_percent, memory_bytes, at FROM metric_samples
			WHERE resource_id = r.id AND at > now() - interval '60 seconds' ORDER BY at DESC LIMIT 1
		) m ON true
		WHERE (($3 = '' AND p.archived_at IS NULL) OR r.server_id::text = $3)
		  AND ($1 = '' OR r.name ILIKE '%' || $1 || '%' OR r.image ILIKE '%' || $1 || '%' OR r.repo_url ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR r.type = $2)
		ORDER BY p.name, r.type, r.name`
	rows, err := s.DB.Query(ctx, sql, query, typ, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Resource{}
	for rows.Next() {
		var r Resource
		var cpu *float32
		var mem *int64
		var at *time.Time
		var domain *string
		err := rows.Scan(&r.ID, &r.ProjectID, &r.EnvironmentID, &r.Environment, &r.Name, &r.Type, &r.SourceType, &r.Image, &r.RepoURL, &r.Branch, &r.Dockerfile,
			&r.Port, &r.Command, &r.DataPath, &r.MemoryMB, &r.CPUs, &r.DesiredState, &r.Status, &r.ContainerID, &r.HostPort, &r.CurrentDeploymentID, &r.RunningSince, &r.SourceID, &domain, &r.Volumes, &r.LastDeployAt, &r.ServerID, &r.Engine, &r.CreatedAt, &r.UpdatedAt,
			&r.ProjectName, &cpu, &mem, &at)
		if err != nil {
			return nil, err
		}
		r.Domain = withPort(domain)
		if cpu != nil && mem != nil && at != nil {
			r.Usage = &Usage{CPUPercent: float64(*cpu), MemoryBytes: *mem, At: *at}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) DeleteResourceRow(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM resources WHERE id = $1`, id)
	return err
}

/* ---------------------------- Environment variables ------------------------- */

type EnvVar struct {
	Key       string    `json:"key"`
	Value     string    `json:"value,omitempty"`
	Secret    bool      `json:"secret"`
	UpdatedBy string    `json:"updatedBy"`
	UpdatedAt time.Time `json:"updatedAt"`
	Sealed    []byte    `json:"-"`
}

func (s *Store) ListEnv(ctx context.Context, resourceID string) ([]EnvVar, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT v.key, v.value_enc, v.secret, COALESCE(u.email, ''), v.updated_at
		FROM env_vars v LEFT JOIN users u ON u.id = v.updated_by WHERE v.resource_id = $1 ORDER BY v.key`, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EnvVar{}
	for rows.Next() {
		var v EnvVar
		if err := rows.Scan(&v.Key, &v.Sealed, &v.Secret, &v.UpdatedBy, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ReplaceEnv swaps the full variable set atomically. Unchanged rows keep their updated_by/at.
func (s *Store) ReplaceEnv(ctx context.Context, resourceID, userID string, vars []EnvVar) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	keys := make([]string, 0, len(vars))
	for _, v := range vars {
		keys = append(keys, v.Key)
		if _, err := tx.Exec(ctx, `
			INSERT INTO env_vars (resource_id, key, value_enc, secret, updated_by) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (resource_id, key) DO UPDATE SET value_enc = EXCLUDED.value_enc, secret = EXCLUDED.secret,
			   updated_by = EXCLUDED.updated_by, updated_at = now()`, resourceID, v.Key, v.Sealed, v.Secret, userID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM env_vars WHERE resource_id = $1 AND NOT (key = ANY($2))`, resourceID, keys); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

/* -------------------------------- Deployments ------------------------------ */

type Deployment struct {
	ID         string     `json:"id"`
	ResourceID string     `json:"resourceId"`
	Number     int        `json:"number"`
	Status     string     `json:"status"`
	Trigger    string     `json:"trigger"`
	ImageRef   string     `json:"imageRef"`
	CommitSHA  string     `json:"commit"`
	Branch     string     `json:"branch"`
	Reason     string     `json:"reason"`
	Error      string     `json:"error"`
	Author     string     `json:"author"`
	JobID      *string    `json:"jobId"`
	Current    bool       `json:"current"`
	ImageID    string     `json:"imageId"`
	Previous   *int       `json:"previousNumber"` // the release that was live when this one was started
	RestoreEnv bool       `json:"restoreEnv"`     // a rollback that also restored that release's variables
	EnvKept    bool       `json:"envSnapshot"`    // the variables it ran with are kept
	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
}

const deploymentSelect = `
	SELECT d.id, d.resource_id, d.number, d.status, d.trigger, d.image_ref, d.commit_sha, d.branch, d.reason, d.error,
	       COALESCE(u.email, 'system'), (SELECT j.id FROM jobs j WHERE j.deployment_id = d.id ORDER BY j.created_at DESC LIMIT 1),
	       r.current_deployment_id IS NOT DISTINCT FROM d.id, d.image_id,
	       (SELECT p.number FROM deployments p WHERE p.id = d.previous_deployment_id), d.restore_env,
	       EXISTS (SELECT 1 FROM deployment_env e WHERE e.deployment_id = d.id), d.created_at, d.started_at, d.finished_at
	FROM deployments d JOIN resources r ON r.id = d.resource_id LEFT JOIN users u ON u.id = d.created_by`

func scanDeployment(row pgx.Row) (*Deployment, error) {
	var d Deployment
	err := row.Scan(&d.ID, &d.ResourceID, &d.Number, &d.Status, &d.Trigger, &d.ImageRef, &d.CommitSHA, &d.Branch, &d.Reason, &d.Error,
		&d.Author, &d.JobID, &d.Current, &d.ImageID, &d.Previous, &d.RestoreEnv, &d.EnvKept, &d.CreatedAt, &d.StartedAt, &d.FinishedAt)
	return &d, noRows(err)
}

func (s *Store) ListDeployments(ctx context.Context, resourceID string, limit int) ([]Deployment, error) {
	rows, err := s.DB.Query(ctx, deploymentSelect+` WHERE d.resource_id = $1 ORDER BY d.number DESC LIMIT $2`, resourceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *Store) GetDeployment(ctx context.Context, id string) (*Deployment, error) {
	return scanDeployment(s.DB.QueryRow(ctx, deploymentSelect+` WHERE d.id = $1`, id))
}

// CreateDeployment numbers deployments per resource; the row lock on the resource serializes numbering.
func (s *Store) CreateDeployment(ctx context.Context, tx pgx.Tx, resourceID, trigger, imageRef, branch, reason, userID string) (string, error) {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM resources WHERE id = $1 FOR UPDATE`, resourceID); err != nil {
		return "", err
	}
	// Every path that deploys goes through here, so this is where a server's deployment policy is enforced.
	// The server row is locked too, so a deployment can't slip in while maintenance is being entered.
	var name string
	var enabled, maintenance, removing bool
	err := tx.QueryRow(ctx, `SELECT s.name, s.deployments_enabled, s.maintenance, s.removing FROM servers s JOIN resources r ON r.server_id = s.id WHERE r.id = $1 FOR SHARE OF s`, resourceID).
		Scan(&name, &enabled, &maintenance, &removing)
	if err != nil {
		return "", noRows(err)
	}
	switch {
	case removing:
		return "", &DeploymentsBlocked{Server: name, Reason: "is being removed"}
	case maintenance:
		return "", &DeploymentsBlocked{Server: name, Reason: "is in maintenance mode"}
	case !enabled:
		return "", &DeploymentsBlocked{Server: name, Reason: "has new deployments disabled"}
	}
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO deployments (resource_id, number, trigger, image_ref, branch, reason, created_by, previous_deployment_id)
		VALUES ($1, COALESCE((SELECT max(number) FROM deployments WHERE resource_id = $1), 0) + 1, $2, $3, $4, $5, $6,
		        (SELECT current_deployment_id FROM resources WHERE id = $1))
		RETURNING id`, resourceID, trigger, imageRef, branch, reason, nullable(userID)).Scan(&id)
	return id, err
}

// DeploymentsBlocked is returned when the resource's server isn't accepting new deployments.
type DeploymentsBlocked struct{ Server, Reason string }

func (e *DeploymentsBlocked) Error() string {
	return fmt.Sprintf("Server %s %s, so new deployments are paused. Change this on the server's Maintenance or Settings page.", e.Server, e.Reason)
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *Store) SetDeploymentStatus(ctx context.Context, id, status, errMsg string) error {
	_, err := s.DB.Exec(ctx, `
		UPDATE deployments SET status = $2, error = $3,
		       started_at = CASE WHEN $2 IN ('building', 'deploying') THEN COALESCE(started_at, now()) ELSE started_at END,
		       finished_at = CASE WHEN $2 IN ('successful', 'failed', 'cancelled') THEN now() ELSE finished_at END
		WHERE id = $1`, id, status, errMsg)
	return err
}

func (s *Store) SetDeploymentArtifact(ctx context.Context, id, imageRef, commit string) error {
	_, err := s.DB.Exec(ctx, `UPDATE deployments SET image_ref = $2, commit_sha = COALESCE(NULLIF($3, ''), commit_sha) WHERE id = $1`, id, imageRef, commit)
	return err
}

func (s *Store) MarkCurrent(ctx context.Context, resourceID, deploymentID string) error {
	_, err := s.DB.Exec(ctx, `UPDATE resources SET current_deployment_id = $2, updated_at = now() WHERE id = $1`, resourceID, deploymentID)
	return err
}

type JobLog struct {
	ID        int64     `json:"id"`
	Level     string    `json:"level"`
	Step      string    `json:"step"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) JobLogs(ctx context.Context, jobID string, afterID int64) ([]JobLog, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, level, step, message, created_at FROM job_logs WHERE job_id = $1 AND id > $2 ORDER BY id LIMIT 2000`, jobID, afterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JobLog{}
	for rows.Next() {
		var l JobLog
		if err := rows.Scan(&l.ID, &l.Level, &l.Step, &l.Message, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

/* ----------------------------------- Audit --------------------------------- */

type AuditEvent struct {
	ID         int64           `json:"id"`
	ActorEmail string          `json:"actor"`
	Action     string          `json:"action"`
	TargetType string          `json:"targetType"`
	TargetID   string          `json:"targetId"`
	Detail     json.RawMessage `json:"detail"`
	IP         string          `json:"ip"`
	CreatedAt  time.Time       `json:"createdAt"`
}

// Audit records an event. Callers must never put secret values in detail.
func (s *Store) Audit(ctx context.Context, actor *User, action, targetType, targetID string, detail map[string]any, ip string) {
	b, _ := json.Marshal(detail)
	var id, email *string
	if actor != nil {
		email = &actor.Email
		if actor.ID != "" {
			id = &actor.ID
		}
	}
	s.DB.Exec(ctx, `INSERT INTO audit_events (actor_id, actor_email, action, target_type, target_id, detail, ip) VALUES ($1, COALESCE($2, 'system'), $3, $4, $5, $6, $7)`,
		id, email, action, targetType, targetID, b, ip)
}

func (s *Store) ListAudit(ctx context.Context, targetID string, limit int) ([]AuditEvent, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, actor_email, action, target_type, target_id, detail, ip, created_at FROM audit_events
		WHERE ($1 = '' OR target_id = $1) ORDER BY id DESC LIMIT $2`, targetID, limit)
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

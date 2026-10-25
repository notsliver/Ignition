package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

/* --------------------------------- Settings -------------------------------- */

// GetSetting decodes the JSON stored under key into v. A missing key leaves v unchanged.
func (s *Store) GetSetting(ctx context.Context, key string, v any) error {
	var raw []byte
	err := s.DB.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func (s *Store) SetSetting(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, raw)
	return err
}

/* ----------------------------------- Users ---------------------------------- */

type UserRow struct {
	User
	LastSeenAt *time.Time `json:"lastSeenAt"`
	Sessions   int        `json:"sessions"`
	Locked     bool       `json:"locked"`
}

func (s *Store) ListUsers(ctx context.Context) ([]UserRow, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT `+userColsU+`, (SELECT max(last_seen_at) FROM sessions x WHERE x.user_id = u.id),
		       (SELECT count(*) FROM sessions x WHERE x.user_id = u.id AND x.expires_at > now()),
		       COALESCE(u.locked_until > now(), false)
		FROM users u ORDER BY CASE u.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 WHEN 'developer' THEN 2 ELSE 3 END, lower(u.email)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserRow{}
	for rows.Next() {
		var r UserRow
		if err := rows.Scan(&r.ID, &r.Email, &r.Name, &r.Role, &r.Status, &r.PasswordHash, &r.FailedLogins, &r.LockedUntil, &r.CreatedAt, &r.LastSeenAt, &r.Sessions, &r.Locked); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const userColsU = `u.id, u.email, u.name, u.role, u.status, u.password_hash, u.failed_logins, u.locked_until, u.created_at`

func (s *Store) GetUser(ctx context.Context, id string) (*User, error) {
	return scanUser(s.DB.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

func (s *Store) CountActiveOwners(ctx context.Context) (n int, err error) {
	err = s.DB.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = 'owner' AND status = 'active'`).Scan(&n)
	return
}

func (s *Store) SetUserRole(ctx context.Context, id, role string) error {
	_, err := s.DB.Exec(ctx, `UPDATE users SET role = $2, updated_at = now() WHERE id = $1`, id, role)
	return err
}

// SetUserStatus suspends or reactivates; suspending also ends every session and clears a lockout on reactivation.
func (s *Store) SetUserStatus(ctx context.Context, id, status string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE users SET status = $2, failed_logins = 0, locked_until = NULL, updated_at = now() WHERE id = $1`, id, status); err != nil {
		return err
	}
	if status == "suspended" {
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) RevokeSessions(ctx context.Context, userID string) (int64, error) {
	tag, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return tag.RowsAffected(), err
}

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	return err
}

/* ------------------------------ Invites & resets ---------------------------- */

type UserToken struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Email     string     `json:"email"`
	Role      *string    `json:"role"`
	UserID    *string    `json:"userId"`
	CreatedBy string     `json:"createdBy"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt time.Time  `json:"expiresAt"`
	UsedAt    *time.Time `json:"-"`
}

func (s *Store) CreateUserToken(ctx context.Context, kind string, hash []byte, email string, role, userID *string, createdBy string, ttl time.Duration) (*UserToken, error) {
	// A new link replaces any unused one for the same person.
	if _, err := s.DB.Exec(ctx, `DELETE FROM user_tokens WHERE kind = $1 AND lower(email) = lower($2) AND used_at IS NULL`, kind, email); err != nil {
		return nil, err
	}
	var t UserToken
	err := s.DB.QueryRow(ctx, `
		INSERT INTO user_tokens (kind, token_hash, email, role, user_id, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, now() + make_interval(secs => $7)) RETURNING id, created_at, expires_at`,
		kind, hash, email, role, userID, nullable(createdBy), ttl.Seconds()).Scan(&t.ID, &t.CreatedAt, &t.ExpiresAt)
	t.Kind, t.Email, t.Role, t.UserID = kind, email, role, userID
	return &t, err
}

// TokenByHash returns a usable (unused, unexpired) token.
func (s *Store) TokenByHash(ctx context.Context, hash []byte) (*UserToken, error) {
	var t UserToken
	err := s.DB.QueryRow(ctx, `
		SELECT id, kind, email, role, user_id, created_at, expires_at FROM user_tokens
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()`, hash).Scan(&t.ID, &t.Kind, &t.Email, &t.Role, &t.UserID, &t.CreatedAt, &t.ExpiresAt)
	return &t, noRows(err)
}

// AcceptInvite creates the account and marks the invite used, atomically.
func (s *Store) AcceptInvite(ctx context.Context, tokenID, email, name, hash, role string) (*User, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE user_tokens SET used_at = now() WHERE id = $1 AND used_at IS NULL AND expires_at > now()`, tokenID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	u, err := scanUser(tx.QueryRow(ctx, `INSERT INTO users (email, name, password_hash, role) VALUES ($1, $2, $3, $4) RETURNING `+userCols, email, name, hash, role))
	if err != nil {
		return nil, err
	}
	return u, tx.Commit(ctx)
}

// ResetPassword sets a new password from a reset link and signs the account out everywhere.
func (s *Store) ResetPassword(ctx context.Context, tokenID, userID, hash string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE user_tokens SET used_at = now() WHERE id = $1 AND used_at IS NULL AND expires_at > now()`, tokenID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2, failed_logins = 0, locked_until = NULL, updated_at = now() WHERE id = $1`, userID, hash); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) PendingInvites(ctx context.Context) ([]UserToken, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT t.id, t.kind, t.email, t.role, t.user_id, COALESCE(u.email, ''), t.created_at, t.expires_at
		FROM user_tokens t LEFT JOIN users u ON u.id = t.created_by
		WHERE t.kind = 'invite' AND t.used_at IS NULL AND t.expires_at > now() ORDER BY t.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserToken{}
	for rows.Next() {
		var t UserToken
		if err := rows.Scan(&t.ID, &t.Kind, &t.Email, &t.Role, &t.UserID, &t.CreatedBy, &t.CreatedAt, &t.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) RevokeInvite(ctx context.Context, id string) (string, error) {
	var email string
	err := s.DB.QueryRow(ctx, `DELETE FROM user_tokens WHERE id = $1 AND kind = 'invite' AND used_at IS NULL RETURNING email`, id).Scan(&email)
	return email, noRows(err)
}

/* ----------------------------------- Audit ---------------------------------- */

type AuditFilter struct {
	Query, Actor, Action, Target string
	Since, Until                 *time.Time
	Before                       int64 // cursor: events with id < Before
	Limit                        int
}

func (s *Store) SearchAudit(ctx context.Context, f AuditFilter) ([]AuditEvent, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id, actor_email, action, target_type, target_id, detail, ip, created_at FROM audit_events
		WHERE ($1 = '' OR actor_email ILIKE '%' || $1 || '%' OR action ILIKE '%' || $1 || '%' OR target_id = $1 OR detail::text ILIKE '%' || $1 || '%' OR ip = $1)
		  AND ($2 = '' OR actor_email = $2)
		  AND ($3 = '' OR action = $3 OR action LIKE $3 || '.%')
		  AND ($4 = '' OR target_id = $4)
		  AND ($5::timestamptz IS NULL OR created_at >= $5)
		  AND ($6::timestamptz IS NULL OR created_at < $6)
		  AND ($7 = 0 OR id < $7)
		ORDER BY id DESC LIMIT $8`, f.Query, f.Actor, f.Action, f.Target, f.Since, f.Until, f.Before, f.Limit)
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

// AuditFacets lists who and what appear in the log, for the filters.
func (s *Store) AuditFacets(ctx context.Context) (actors []string, actions []string, err error) {
	actors, actions = []string{}, []string{}
	rows, err := s.DB.Query(ctx, `SELECT DISTINCT actor_email FROM audit_events ORDER BY 1`)
	if err != nil {
		return
	}
	for rows.Next() {
		var a string
		rows.Scan(&a)
		actors = append(actors, a)
	}
	rows.Close()
	rows, err = s.DB.Query(ctx, `SELECT DISTINCT action FROM audit_events ORDER BY 1`)
	if err != nil {
		return
	}
	for rows.Next() {
		var a string
		rows.Scan(&a)
		actions = append(actions, a)
	}
	rows.Close()
	return actors, actions, nil
}

// TargetNames resolves resource and project IDs in audit events to names (and a resource's project) for links.
func (s *Store) TargetNames(ctx context.Context, ids []string) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.DB.Query(ctx, `
		SELECT r.id::text, r.name, r.project_id::text, 'resource' FROM resources r WHERE r.id::text = ANY($1)
		UNION ALL SELECT p.id::text, p.name, p.id::text, 'project' FROM projects p WHERE p.id::text = ANY($1)
		UNION ALL SELECT u.id::text, u.email, '', 'user' FROM users u WHERE u.id::text = ANY($1)
		UNION ALL SELECT s.id::text, s.name, '', 'source' FROM sources s WHERE s.id::text = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, project, kind string
		if err := rows.Scan(&id, &name, &project, &kind); err != nil {
			return nil, err
		}
		out[id] = map[string]string{"name": name, "projectId": project, "kind": kind}
	}
	return out, rows.Err()
}

// InstalledAt is when the first account was created.
func (s *Store) InstalledAt(ctx context.Context) (*time.Time, error) {
	var t *time.Time
	err := s.DB.QueryRow(ctx, `SELECT min(created_at) FROM users`).Scan(&t)
	return t, err
}

func (s *Store) PostgresVersion(ctx context.Context) string {
	var v string
	s.DB.QueryRow(ctx, `SHOW server_version`).Scan(&v)
	return v
}

type ResourceDefaults struct {
	HealthSeconds, StopGrace int
	RestartPolicy, BuildMode string
}

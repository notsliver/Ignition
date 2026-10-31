package store

import (
	"context"
	"encoding/json"
	"slices"
	"time"
)

/* ------------------------------ Two-factor auth ------------------------------ */

// MFAState is what a user's second factor looks like in the database. The secret stays sealed.
type MFAState struct {
	SealedSecret  []byte
	EnabledAt     *time.Time
	RecoveryCodes []string // SHA-256 hashes; a used code is removed
}

func (s *Store) GetMFA(ctx context.Context, userID string) (MFAState, error) {
	var m MFAState
	var codes []byte
	err := s.DB.QueryRow(ctx, `SELECT totp_secret_enc, totp_enabled_at, recovery_codes FROM users WHERE id = $1`, userID).Scan(&m.SealedSecret, &m.EnabledAt, &codes)
	json.Unmarshal(codes, &m.RecoveryCodes)
	return m, noRows(err)
}

// SetPendingTOTP stores a secret that isn't active until EnableTOTP confirms a code from it.
func (s *Store) SetPendingTOTP(ctx context.Context, userID string, sealed []byte) error {
	_, err := s.DB.Exec(ctx, `UPDATE users SET totp_secret_enc = $2 WHERE id = $1 AND totp_enabled_at IS NULL`, userID, sealed)
	return err
}

func (s *Store) EnableTOTP(ctx context.Context, userID string, recoveryHashes []string) error {
	b, _ := json.Marshal(recoveryHashes)
	_, err := s.DB.Exec(ctx, `UPDATE users SET totp_enabled_at = now(), recovery_codes = $2, updated_at = now() WHERE id = $1 AND totp_secret_enc IS NOT NULL`, userID, b)
	return err
}

func (s *Store) DisableTOTP(ctx context.Context, userID string) error {
	_, err := s.DB.Exec(ctx, `UPDATE users SET totp_secret_enc = NULL, totp_enabled_at = NULL, recovery_codes = '[]', updated_at = now() WHERE id = $1`, userID)
	return err
}

func (s *Store) SetRecoveryCodes(ctx context.Context, userID string, hashes []string) error {
	b, _ := json.Marshal(hashes)
	_, err := s.DB.Exec(ctx, `UPDATE users SET recovery_codes = $2 WHERE id = $1`, userID, b)
	return err
}

// UseRecoveryCode removes a matching code atomically; false means it wasn't valid (or already used).
func (s *Store) UseRecoveryCode(ctx context.Context, userID, hash string) (bool, error) {
	tag, err := s.DB.Exec(ctx, `
		UPDATE users SET recovery_codes = recovery_codes - $2
		WHERE id = $1 AND recovery_codes ? $2`, userID, hash)
	return tag.RowsAffected() == 1, err
}

/* -------------------------------- Permissions -------------------------------- */

func (s *Store) SetUserPermissions(ctx context.Context, userID string, p PermOverrides) error {
	slices.Sort(p.Grant)
	slices.Sort(p.Deny)
	b, _ := json.Marshal(p)
	_, err := s.DB.Exec(ctx, `UPDATE users SET permissions = $2, updated_at = now() WHERE id = $1`, userID, b)
	return err
}

/* ----------------------------------- SSO ------------------------------------ */

func (s *Store) UserByOIDCSubject(ctx context.Context, subject string) (*User, error) {
	return scanUser(s.DB.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE oidc_subject = $1`, subject))
}

func (s *Store) LinkOIDC(ctx context.Context, userID, subject string) error {
	_, err := s.DB.Exec(ctx, `UPDATE users SET oidc_subject = $2, updated_at = now() WHERE id = $1`, userID, subject)
	return err
}

// CreateSSOUser provisions an account on first SSO sign-in. It has no password, so it can only
// sign in through the identity provider until someone sends it a reset link.
func (s *Store) CreateSSOUser(ctx context.Context, email, name, role, subject string) (*User, error) {
	return scanUser(s.DB.QueryRow(ctx, `INSERT INTO users (email, name, password_hash, role, oidc_subject) VALUES ($1, $2, '', $3, $4) RETURNING `+userCols,
		email, name, role, subject))
}

/* --------------------------------- Sessions ---------------------------------- */

// RevokeAllSessions signs everyone out.
func (s *Store) RevokeAllSessions(ctx context.Context) (int64, error) {
	tag, err := s.DB.Exec(ctx, `DELETE FROM sessions`)
	return tag.RowsAffected(), err
}

// RevokeOtherSessions ends a user's sessions except the one making the request.
func (s *Store) RevokeOtherSessions(ctx context.Context, userID string, keep []byte) (int64, error) {
	tag, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND token_hash <> $2`, userID, keep)
	return tag.RowsAffected(), err
}

// ShortenSessions caps existing sessions at a new, shorter lifetime.
func (s *Store) ShortenSessions(ctx context.Context, lifetime time.Duration) error {
	_, err := s.DB.Exec(ctx, `UPDATE sessions SET expires_at = least(expires_at, created_at + $1::interval)`, interval(lifetime))
	return err
}

/* ------------------------------ Security events ------------------------------- */

// SecurityEvents are the audit entries about sign-in, accounts, permissions and security settings.
func (s *Store) SecurityEvents(ctx context.Context, limit int) ([]AuditEvent, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, actor_email, action, target_type, target_id, detail, ip, created_at FROM audit_events
		WHERE action LIKE 'auth.%' OR action LIKE 'user.%' OR action LIKE 'security.%' OR action = 'instance.setup'
		ORDER BY id DESC LIMIT $1`, limit)
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

// FailedLoginsSince counts failed sign-ins per IP, for the events summary.
func (s *Store) FailedLoginsSince(ctx context.Context, since time.Time) (map[string]int, error) {
	rows, err := s.DB.Query(ctx, `SELECT ip, count(*) FROM audit_events WHERE action = 'auth.login_failed' AND created_at > $1 GROUP BY ip`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var ip string
		var n int
		if err := rows.Scan(&ip, &n); err != nil {
			return nil, err
		}
		out[ip] = n
	}
	return out, rows.Err()
}

// SuspendWithoutMFA suspends active accounts that have a password but no second factor, except
// owners, and ends their sessions. It returns their emails.
func (s *Store) SuspendWithoutMFA(ctx context.Context) ([]string, error) {
	rows, err := s.DB.Query(ctx, `
		WITH hit AS (
			UPDATE users SET status = 'suspended', updated_at = now()
			WHERE status = 'active' AND role <> 'owner' AND totp_enabled_at IS NULL AND password_hash <> ''
			RETURNING id, email
		), gone AS (DELETE FROM sessions WHERE user_id IN (SELECT id FROM hit))
		SELECT email FROM hit`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

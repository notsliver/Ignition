package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

var ErrLastSignInMethod = errors.New("Add a password or another passkey before removing your last sign-in method.")

func (s *Store) SetPassword(ctx context.Context, user, hash string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE users SET password_hash=$2,failed_logins=0,locked_until=NULL,updated_at=now() WHERE id=$1`, user, hash); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, user); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM user_tokens WHERE user_id=$1 AND kind='reset'`, user); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) AccountOIDC(ctx context.Context, user string) (issuer, subject string, err error) {
	err = s.DB.QueryRow(ctx, `SELECT oidc_issuer,coalesce(oidc_subject,'') FROM users WHERE id=$1`, user).Scan(&issuer, &subject)
	return
}
func (s *Store) UnlinkAccountOIDC(ctx context.Context, user string, keep []byte) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var password bool
	if err = tx.QueryRow(ctx, `SELECT password_hash<>'' OR EXISTS(SELECT 1 FROM account_integration_identities i JOIN instance_integrations c ON c.provider=i.provider WHERE i.user_id=users.id AND i.origin=c.config->>'baseUrl' AND coalesce((c.config->>'enabled')::boolean,true) AND coalesce((c.config->>'signInEnabled')::boolean,false)) FROM users WHERE id=$1 FOR UPDATE`, user).Scan(&password); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM account_passkeys WHERE user_id=$1`, user).Scan(&count); err != nil {
		return err
	}
	if !password && count == 0 {
		return ErrLastSignInMethod
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET oidc_subject=NULL,oidc_issuer='',updated_at=now() WHERE id=$1`, user); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1 AND method='sso' AND token_hash<>$2`, user, keep); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type AccountSession struct {
	ID         string    `json:"id"`
	UserAgent  string    `json:"userAgent"`
	IP         string    `json:"ip"`
	Method     string    `json:"method"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Current    bool      `json:"current"`
}

func (s *Store) AccountSessions(ctx context.Context, user string, current []byte, idle time.Duration) ([]AccountSession, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,user_agent,ip,method,created_at,last_seen_at,expires_at,token_hash=$2 FROM sessions WHERE user_id=$1 AND expires_at>now() AND ($3=0 OR last_seen_at>now()-make_interval(secs=>$3)) ORDER BY created_at DESC`, user, current, int(idle.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccountSession{}
	for rows.Next() {
		var v AccountSession
		if err := rows.Scan(&v.ID, &v.UserAgent, &v.IP, &v.Method, &v.CreatedAt, &v.LastSeenAt, &v.ExpiresAt, &v.Current); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) DeleteAccountSession(ctx context.Context, user, id string) (bool, error) {
	tag, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1 AND id=$2`, user, id)
	return tag.RowsAffected() == 1, err
}
func (s *Store) ReauthenticateSession(ctx context.Context, user string, hash []byte) error {
	_, err := s.DB.Exec(ctx, `UPDATE sessions SET reauthenticated_at=now() WHERE user_id=$1 AND token_hash=$2 AND expires_at>now()`, user, hash)
	return err
}
func (s *Store) RecentlyAuthenticated(ctx context.Context, user string, hash []byte) (bool, error) {
	var ok bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE user_id=$1 AND token_hash=$2 AND expires_at>now() AND (reauthenticated_at>now()-interval '5 minutes' OR ((method='sso' OR method LIKE 'oauth:%') AND created_at>now()-interval '5 minutes')))`, user, hash).Scan(&ok)
	return ok, err
}
func (s *Store) UpdateAccount(ctx context.Context, user, name, email string, current []byte) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var old string
	if err = tx.QueryRow(ctx, `SELECT email FROM users WHERE id=$1 FOR UPDATE`, user).Scan(&old); err != nil {
		return noRows(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET name=$2,email=$3,updated_at=now() WHERE id=$1`, user, name, email); err != nil {
		return err
	}
	if old != email {
		if _, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1 AND token_hash<>$2`, user, current); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM user_tokens WHERE user_id=$1 AND kind='reset'`, user); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) DeleteOwnAccount(ctx context.Context, user string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Match the team-owner locking order used by all other account deletion paths.
	if err = lockUserTeamOwners(ctx, tx, user); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM users WHERE role='owner' AND status='active' ORDER BY id FOR UPDATE`)
	if err != nil {
		return err
	}
	owners := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		owners = append(owners, id)
	}
	rows.Close()
	if rows.Err() != nil {
		return rows.Err()
	}
	if len(owners) == 1 && owners[0] == user {
		return ErrLastOwner
	}
	// Withdraw capabilities issued by the account before removing its identity.
	if _, err = tx.Exec(ctx, `UPDATE sources SET secret_enc=NULL,status='failed',status_message='The connected account was removed; reconnect this source.' WHERE oauth_user_id=$1`, user); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM resource_tokens WHERE created_by=$1`, user); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE automations SET hook_hash=NULL WHERE created_by=$1`, user); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM users WHERE id=$1`, user); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type AccountPasskey struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	CreatedAt  time.Time           `json:"createdAt"`
	LastUsedAt *time.Time          `json:"lastUsedAt"`
	Credential webauthn.Credential `json:"-"`
}

func (s *Store) AccountPasskeys(ctx context.Context, user string) ([]AccountPasskey, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,name,created_at,last_used_at,credential FROM account_passkeys WHERE user_id=$1 ORDER BY created_at`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccountPasskey{}
	for rows.Next() {
		var p AccountPasskey
		var raw []byte
		if err = rows.Scan(&p.ID, &p.Name, &p.CreatedAt, &p.LastUsedAt, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &p.Credential); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) AddAccountPasskey(ctx context.Context, user, name string, c *webauthn.Credential) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO account_passkeys(user_id,name,credential_id,credential) VALUES($1,$2,$3,$4)`, user, name, c.ID, raw)
	return err
}
func (s *Store) PasskeyOwner(ctx context.Context, id []byte) (*User, error) {
	var user string
	if err := s.DB.QueryRow(ctx, `SELECT user_id FROM account_passkeys WHERE credential_id=$1`, id).Scan(&user); err != nil {
		return nil, noRows(err)
	}
	return s.GetUser(ctx, user)
}
func (s *Store) UpdateAccountPasskey(ctx context.Context, user string, old, next webauthn.Credential) error {
	before, err := json.Marshal(old)
	if err != nil {
		return err
	}
	after, err := json.Marshal(next)
	if err != nil {
		return err
	}
	tag, err := s.DB.Exec(ctx, `UPDATE account_passkeys SET credential=$3,last_used_at=now() WHERE user_id=$1 AND credential_id=$2 AND credential=$4::jsonb`, user, next.ID, after, before)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) DeleteAccountPasskey(ctx context.Context, user, id string, localAllowed bool, ssoIssuer string) error {
	// Lock the account to serialize removal and SSO disconnection; never strand SSO-only users.
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var fallback bool
	if err = tx.QueryRow(ctx, `SELECT ($2 AND password_hash<>'') OR (oidc_subject IS NOT NULL AND oidc_issuer=$3 AND $3<>'') OR EXISTS(SELECT 1 FROM account_integration_identities i JOIN instance_integrations c ON c.provider=i.provider WHERE i.user_id=users.id AND i.origin=c.config->>'baseUrl' AND coalesce((c.config->>'enabled')::boolean,true) AND coalesce((c.config->>'signInEnabled')::boolean,false)) FROM users WHERE id=$1 FOR UPDATE`, user, localAllowed, ssoIssuer).Scan(&fallback); err != nil {
		return noRows(err)
	}
	var owned bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM account_passkeys WHERE user_id=$1 AND id=$2)`, user, id).Scan(&owned); err != nil {
		return err
	}
	if !owned {
		return ErrNotFound
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM account_passkeys WHERE user_id=$1`, user).Scan(&count); err != nil {
		return err
	}
	if !fallback && count <= 1 {
		return ErrLastSignInMethod
	}
	tag, err := tx.Exec(ctx, `DELETE FROM account_passkeys WHERE user_id=$1 AND id=$2`, user, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}
func (s *Store) SaveAccountChallenge(ctx context.Context, hash, browser []byte, user, purpose, name string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err = s.DB.Exec(ctx, `DELETE FROM account_challenges WHERE expires_at<now()`); err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO account_challenges(token_hash,browser_hash,user_id,purpose,name,data) VALUES($1,$2,nullif($3,'')::uuid,$4,$5,$6)`, hash, browser, user, purpose, name, raw)
	return err
}
func (s *Store) TakeAccountChallenge(ctx context.Context, hash, browser []byte, purpose string) (user, name string, data webauthn.SessionData, err error) {
	var id *string
	var raw []byte
	err = s.DB.QueryRow(ctx, `DELETE FROM account_challenges WHERE token_hash=$1 AND browser_hash=$2 AND purpose=$3 AND expires_at>now() RETURNING user_id,name,data`, hash, browser, purpose).Scan(&id, &name, &raw)
	if err != nil {
		return "", "", data, noRows(err)
	}
	if id != nil {
		user = *id
	}
	err = json.Unmarshal(raw, &data)
	return
}

package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

var IntegrationNames = []string{"github", "gitlab", "bitbucket", "git", "dockerhub", "ghcr", "gitlab-registry", "registry"}

type OAuthClientConfig struct {
	ClientID  string   `json:"clientId"`
	Scopes    []string `json:"scopes"`
	HasSecret bool     `json:"hasSecret"`
}
type IntegrationConfig struct {
	Provider        string            `json:"provider"`
	Label           string            `json:"label"`
	BaseURL         string            `json:"baseUrl"`
	Enabled         bool              `json:"enabled"`
	ManualAllowed   bool              `json:"manualAllowed"`
	SignInEnabled   bool              `json:"signInEnabled"`
	SourcesEnabled  bool              `json:"sourcesEnabled"`
	AllowSignup     bool              `json:"allowSignup"`
	Login           OAuthClientConfig `json:"login"`
	Repositories    OAuthClientConfig `json:"repositories"`
	Revision        int64             `json:"revision"`
	SourceRevision  int64             `json:"sourceRevision"`
	LoginSecretEnc  []byte            `json:"-"`
	SourceSecretEnc []byte            `json:"-"`
}

func scanIntegration(row pgx.Row) (IntegrationConfig, error) {
	c := IntegrationConfig{Enabled: true, ManualAllowed: true}
	var raw []byte
	err := row.Scan(&c.Provider, &raw, &c.LoginSecretEnc, &c.SourceSecretEnc, &c.Revision)
	if err != nil {
		return c, noRows(err)
	}
	provider, revision := c.Provider, c.Revision
	if err = json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	c.Provider = provider
	c.Revision = revision
	c.Login.HasSecret = len(c.LoginSecretEnc) > 0
	c.Repositories.HasSecret = len(c.SourceSecretEnc) > 0
	return c, nil
}
func (s *Store) Integration(ctx context.Context, provider string) (IntegrationConfig, error) {
	return scanIntegration(s.DB.QueryRow(ctx, `SELECT provider,config,login_secret_enc,source_secret_enc,revision FROM instance_integrations WHERE provider=$1`, provider))
}
func (s *Store) Integrations(ctx context.Context) ([]IntegrationConfig, error) {
	rows, err := s.DB.Query(ctx, `SELECT provider,config,login_secret_enc,source_secret_enc,revision FROM instance_integrations ORDER BY array_position($1::text[],provider)`, IntegrationNames)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IntegrationConfig{}
	for rows.Next() {
		c, err := scanIntegration(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) SaveIntegration(ctx context.Context, c IntegrationConfig, expected int64) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tag, err := s.DB.Exec(ctx, `UPDATE instance_integrations SET config=$2,login_secret_enc=$3,source_secret_enc=$4,revision=revision+1,updated_at=now() WHERE provider=$1 AND revision=$5`, c.Provider, raw, c.LoginSecretEnc, c.SourceSecretEnc, expected)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("the provider changed; reload before saving")
	}
	return nil
}
func (s *Store) SourceMode(ctx context.Context) string {
	var mode string
	s.GetSetting(ctx, "source_mode", &mode)
	if mode != "managed" {
		return "self-managed"
	}
	return mode
}

type IntegrationIdentity struct {
	Origin   string `json:"origin"`
	Provider string `json:"provider"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Subject  string `json:"-"`
}

func (s *Store) IntegrationIdentities(ctx context.Context, user string) ([]IntegrationIdentity, error) {
	rows, err := s.DB.Query(ctx, `SELECT provider,username,email,subject,origin FROM account_integration_identities WHERE user_id=$1 ORDER BY provider`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IntegrationIdentity{}
	for rows.Next() {
		var c IntegrationIdentity
		if err = rows.Scan(&c.Provider, &c.Username, &c.Email, &c.Subject, &c.Origin); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) IntegrationUser(ctx context.Context, provider, origin, subject string) (*User, error) {
	var id string
	if err := s.DB.QueryRow(ctx, `SELECT user_id FROM account_integration_identities WHERE provider=$1 AND origin=$2 AND subject=$3`, provider, origin, subject).Scan(&id); err != nil {
		return nil, noRows(err)
	}
	return s.GetUser(ctx, id)
}
func (s *Store) LinkIntegration(ctx context.Context, user string, identity IntegrationIdentity) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO account_integration_identities(user_id,provider,subject,username,email,origin) VALUES($1,$2,$3,$4,$5,$6)`, user, identity.Provider, identity.Subject, identity.Username, identity.Email, identity.Origin)
	return err
}
func (s *Store) UnlinkIntegration(ctx context.Context, user, provider string, keep []byte, local bool, issuer string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var fallback bool
	err = tx.QueryRow(ctx, `SELECT ($2 AND (password_hash<>'' OR EXISTS(SELECT 1 FROM account_passkeys k WHERE k.user_id=u.id))) OR (oidc_subject IS NOT NULL AND oidc_issuer=$3 AND $3<>'') OR EXISTS(SELECT 1 FROM account_integration_identities i JOIN instance_integrations c ON c.provider=i.provider WHERE i.user_id=u.id AND i.provider<>$4 AND i.origin=c.config->>'baseUrl' AND coalesce((c.config->>'enabled')::boolean,true) AND coalesce((c.config->>'signInEnabled')::boolean,false)) FROM users u WHERE id=$1 FOR UPDATE`, user, local, issuer, provider).Scan(&fallback)
	if err != nil {
		return err
	}
	if !fallback {
		return ErrLastSignInMethod
	}
	tag, err := tx.Exec(ctx, `DELETE FROM account_integration_identities WHERE user_id=$1 AND provider=$2`, user, provider)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1 AND method LIKE $2 AND token_hash<>$3`, user, "oauth:"+provider+"%", keep); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) CreateIntegrationUser(ctx context.Context, email, name, role string, identity IntegrationIdentity) (*User, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	u, err := scanUser(tx.QueryRow(ctx, `INSERT INTO users(email,name,password_hash,role) VALUES($1,$2,'',$3) RETURNING `+userCols, email, name, role))
	if err != nil {
		return nil, err
	}
	if err = s.createPersonalTeam(ctx, tx, u); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO account_integration_identities(user_id,provider,subject,username,email,origin) VALUES($1,$2,$3,$4,$5,$6)`, u.ID, identity.Provider, identity.Subject, identity.Username, identity.Email, identity.Origin); err != nil {
		return nil, err
	}
	return u, tx.Commit(ctx)
}

type IntegrationOAuthState struct {
	BrowserHash, SessionHash                    []byte
	UserID, TeamID, Provider, Purpose, ReturnTo string
	VerifierEnc                                 []byte
	Revision                                    int64
	Scopes                                      []string
}

func (s *Store) SaveIntegrationState(ctx context.Context, hash []byte, v IntegrationOAuthState) error {
	if _, err := s.DB.Exec(ctx, `DELETE FROM integration_oauth_states WHERE expires_at<now()`); err != nil {
		return err
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO integration_oauth_states(token_hash,browser_hash,session_hash,user_id,team_id,provider,purpose,return_to,verifier_enc,revision,scopes) VALUES($1,$2,$3,nullif($4,'')::uuid,nullif($5,'')::uuid,$6,$7,$8,$9,$10,$11)`, hash, v.BrowserHash, v.SessionHash, v.UserID, v.TeamID, v.Provider, v.Purpose, v.ReturnTo, v.VerifierEnc, v.Revision, v.Scopes)
	return err
}
func (s *Store) TakeIntegrationState(ctx context.Context, hash, browser []byte, provider string) (IntegrationOAuthState, error) {
	var v IntegrationOAuthState
	err := s.DB.QueryRow(ctx, `DELETE FROM integration_oauth_states WHERE token_hash=$1 AND browser_hash=$2 AND provider=$3 AND expires_at>now() RETURNING browser_hash,session_hash,coalesce(user_id::text,''),coalesce(team_id::text,''),provider,purpose,return_to,verifier_enc,revision,scopes`, hash, browser, provider).Scan(&v.BrowserHash, &v.SessionHash, &v.UserID, &v.TeamID, &v.Provider, &v.Purpose, &v.ReturnTo, &v.VerifierEnc, &v.Revision, &v.Scopes)
	return v, noRows(err)
}
func (s *Store) SaveManagedSource(ctx context.Context, user, team string, c IntegrationConfig, identity IntegrationIdentity, scopes []string, seal func(string) ([]byte, error)) (string, error) {
	tx, err := s.SQL(ctx).Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	nameRunes := []rune(c.Label + " · " + identity.Username)
	if len(nameRunes) > 48 {
		nameRunes = nameRunes[:48]
	}
	suffix := user
	if len(suffix) > 6 {
		suffix = suffix[:6]
	}
	name := string(nameRunes) + " · " + suffix
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO sources(team_id,name,kind,provider,url,auth_type,username,created_by,oauth_provider,oauth_user_id,oauth_revision,oauth_scopes) VALUES($1,$2,'git',$3,$4,'token',$5,$6,$3,$6,$7,$8) ON CONFLICT(team_id,oauth_provider,oauth_user_id) WHERE oauth_provider IS NOT NULL DO UPDATE SET username=excluded.username,url=excluded.url,oauth_revision=excluded.oauth_revision,oauth_scopes=excluded.oauth_scopes RETURNING id`, team, name, c.Provider, c.BaseURL, identity.Username, user, c.SourceRevision, scopes).Scan(&id)
	if err != nil {
		return "", err
	}
	encrypted, err := seal(id)
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `UPDATE sources SET secret_enc=$2,status='connected',status_message='Connected through the instance OAuth application.',account=$3,last_checked_at=now(),updated_at=now() WHERE id=$1`, id, encrypted, identity.Username); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

// Refresh serializes token rotation across the API and all workers.
func (s *Store) ManagedSourceToken(ctx context.Context, id string, refresh func(*Source, IntegrationConfig) ([]byte, string, error)) (string, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	src, err := scanSource(tx.QueryRow(ctx, sourceSelect+` WHERE s.id=$1 FOR UPDATE OF s`, id))
	if err != nil {
		return "", err
	}
	var activeOwner bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sources s JOIN users u ON u.id=s.oauth_user_id WHERE s.id=$1 AND u.status='active' AND (u.role IN ('admin','owner') OR EXISTS(SELECT 1 FROM team_memberships m WHERE m.user_id=u.id AND m.team_id=s.team_id)))`, id).Scan(&activeOwner); err != nil {
		return "", err
	}
	if !activeOwner {
		return "", errors.New("the authorizing account is unavailable or no longer belongs to this team; reconnect the source")
	}
	c, err := scanIntegration(tx.QueryRow(ctx, `SELECT provider,config,login_secret_enc,source_secret_enc,revision FROM instance_integrations WHERE provider=$1`, src.OAuthProvider))
	if err != nil {
		return "", err
	}
	sealed, token, err := refresh(src, c)
	if err != nil {
		return "", err
	}
	if sealed != nil {
		if _, err = tx.Exec(ctx, `UPDATE sources SET secret_enc=$2,updated_at=now() WHERE id=$1`, id, sealed); err != nil {
			return "", err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return token, nil
}

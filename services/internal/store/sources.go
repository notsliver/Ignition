package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type Source struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Kind            string     `json:"kind"`
	Provider        string     `json:"provider"`
	URL             string     `json:"url"`
	AuthType        string     `json:"authType"`
	Username        string     `json:"username"`
	HasSecret       bool       `json:"hasSecret"`
	SecretHint      string     `json:"secretHint"`
	PublicKey       string     `json:"publicKey"`
	Status          string     `json:"status"`
	StatusMessage   string     `json:"statusMessage"`
	Account         string     `json:"account"`
	LastCheckedAt   *time.Time `json:"lastCheckedAt"`
	LinkedResources int        `json:"linkedResources"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	SecretEnc       []byte     `json:"-"`
}

const sourceSelect = `
	SELECT s.id, s.name, s.kind, s.provider, s.url, s.auth_type, s.username, s.secret_enc IS NOT NULL, s.secret_hint, s.public_key,
	       s.status, s.status_message, s.account, s.last_checked_at,
	       (SELECT count(*) FROM resources r WHERE r.source_id = s.id), s.created_at, s.updated_at, s.secret_enc
	FROM sources s`

func scanSource(row pgx.Row) (*Source, error) {
	var s Source
	err := row.Scan(&s.ID, &s.Name, &s.Kind, &s.Provider, &s.URL, &s.AuthType, &s.Username, &s.HasSecret, &s.SecretHint, &s.PublicKey,
		&s.Status, &s.StatusMessage, &s.Account, &s.LastCheckedAt, &s.LinkedResources, &s.CreatedAt, &s.UpdatedAt, &s.SecretEnc)
	return &s, noRows(err)
}

func (s *Store) ListSources(ctx context.Context, kind, query string) ([]Source, error) {
	rows, err := s.DB.Query(ctx, sourceSelect+`
		WHERE ($1 = '' OR s.kind = $1) AND ($2 = '' OR s.name ILIKE '%' || $2 || '%' OR s.provider ILIKE '%' || $2 || '%' OR s.url ILIKE '%' || $2 || '%')
		ORDER BY s.kind, lower(s.name)`, kind, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *src)
	}
	return out, rows.Err()
}

func (s *Store) GetSource(ctx context.Context, id string) (*Source, error) {
	return scanSource(s.DB.QueryRow(ctx, sourceSelect+` WHERE s.id = $1`, id))
}

type SourceInput struct {
	Name, Kind, Provider, URL, AuthType, Username, SecretHint, PublicKey, CreatedBy string
}

// CreateSource inserts the row first so the secret can be sealed against its id, then stores it.
func (s *Store) CreateSource(ctx context.Context, in SourceInput, seal func(id string) ([]byte, error)) (string, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO sources (name, kind, provider, url, auth_type, username, secret_hint, public_key, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		in.Name, in.Kind, in.Provider, in.URL, in.AuthType, in.Username, in.SecretHint, in.PublicKey, nullable(in.CreatedBy)).Scan(&id); err != nil {
		return "", err
	}
	if seal != nil {
		sealed, err := seal(id)
		if err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE sources SET secret_enc = $2 WHERE id = $1`, id, sealed); err != nil {
			return "", err
		}
	}
	return id, tx.Commit(ctx)
}

type SourcePatch struct {
	Name     *string `json:"name"`
	URL      *string `json:"url"`
	Username *string `json:"username"`
	AuthType *string `json:"authType"`
}

// UpdateSource changes settings; any change resets the status, since the old test no longer applies.
func (s *Store) UpdateSource(ctx context.Context, id string, p SourcePatch, sealed []byte, hint, publicKey *string, clearSecret bool) error {
	_, err := s.DB.Exec(ctx, `
		UPDATE sources SET name = COALESCE($2, name), url = COALESCE($3, url), username = COALESCE($4, username),
		       auth_type = COALESCE($5, auth_type),
		       secret_enc = CASE WHEN $8 THEN NULL WHEN $6::bytea IS NOT NULL THEN $6 ELSE secret_enc END,
		       secret_hint = CASE WHEN $8 THEN '' ELSE COALESCE($7, secret_hint) END,
		       public_key = CASE WHEN $8 OR $5 <> 'ssh' THEN '' ELSE COALESCE($9, public_key) END,
		       status = 'untested', status_message = 'Settings changed. Test the connection again.', updated_at = now()
		WHERE id = $1`, id, p.Name, p.URL, p.Username, p.AuthType, sealed, hint, clearSecret, publicKey)
	return err
}

func (s *Store) SetSourceStatus(ctx context.Context, id, status, message, account string) error {
	_, err := s.DB.Exec(ctx, `UPDATE sources SET status = $2, status_message = $3, account = $4, last_checked_at = now() WHERE id = $1`, id, status, message, account)
	return err
}

func (s *Store) DeleteSource(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM sources WHERE id = $1`, id)
	return err
}

type LinkedResource struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	Status      string `json:"status"`
}

func (s *Store) SourceResources(ctx context.Context, id string) ([]LinkedResource, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT r.id, r.name, p.id, p.name, r.status FROM resources r JOIN projects p ON p.id = r.project_id
		WHERE r.source_id = $1 ORDER BY p.name, r.name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LinkedResource{}
	for rows.Next() {
		var l LinkedResource
		if err := rows.Scan(&l.ID, &l.Name, &l.ProjectID, &l.ProjectName, &l.Status); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

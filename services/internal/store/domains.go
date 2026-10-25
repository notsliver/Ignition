package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type Domain struct {
	ID            string     `json:"id"`
	ResourceID    string     `json:"resourceId"`
	Hostname      string     `json:"hostname"`
	Port          *int       `json:"port"`
	HTTPS         bool       `json:"https"`
	Primary       bool       `json:"primary"`
	Status        string     `json:"status"`
	StatusMessage string     `json:"statusMessage"`
	Addresses     []string   `json:"addresses"`
	CertIssuer    string     `json:"certIssuer"`
	CertExpiresAt *time.Time `json:"certExpiresAt"`
	LastCheckedAt *time.Time `json:"lastCheckedAt"`
	CreatedAt     time.Time  `json:"createdAt"`
	Token         string     `json:"-"`
}

const domainSelect = `SELECT id, resource_id, hostname, port, https, is_primary, status, status_message, addresses, cert_issuer,
	cert_expires_at, last_checked_at, created_at, token FROM domains`

func scanDomain(row pgx.Row) (*Domain, error) {
	var d Domain
	err := row.Scan(&d.ID, &d.ResourceID, &d.Hostname, &d.Port, &d.HTTPS, &d.Primary, &d.Status, &d.StatusMessage, &d.Addresses,
		&d.CertIssuer, &d.CertExpiresAt, &d.LastCheckedAt, &d.CreatedAt, &d.Token)
	return &d, noRows(err)
}

func (s *Store) ListDomains(ctx context.Context, resourceID string) ([]Domain, error) {
	rows, err := s.DB.Query(ctx, domainSelect+` WHERE resource_id = $1 ORDER BY is_primary DESC, created_at`, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *Store) GetDomain(ctx context.Context, id string) (*Domain, error) {
	return scanDomain(s.DB.QueryRow(ctx, domainSelect+` WHERE id = $1`, id))
}

// CreateDomain adds a hostname; the first one on a resource becomes its primary.
func (s *Store) CreateDomain(ctx context.Context, resourceID, hostname string, port *int, https bool, token, userID string) (string, error) {
	var id string
	err := s.DB.QueryRow(ctx, `
		INSERT INTO domains (resource_id, hostname, port, https, token, created_by, is_primary)
		VALUES ($1, $2, $3, $4, $5, $6, NOT EXISTS (SELECT 1 FROM domains WHERE resource_id = $1))
		RETURNING id`, resourceID, hostname, port, https, token, nullable(userID)).Scan(&id)
	return id, err
}

type DomainPatch struct {
	HTTPS     *bool `json:"https"`
	Port      *int  `json:"port"`
	ClearPort bool  `json:"-"`
	Primary   bool  `json:"primary"`
}

func (s *Store) UpdateDomain(ctx context.Context, id string, p DomainPatch) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if p.Primary {
		if _, err := tx.Exec(ctx, `UPDATE domains SET is_primary = false WHERE resource_id = (SELECT resource_id FROM domains WHERE id = $1)`, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE domains SET https = COALESCE($2, https), port = CASE WHEN $4 THEN NULL ELSE COALESCE($3, port) END,
		       is_primary = is_primary OR $5,
		       status = CASE WHEN $2 IS NOT NULL AND $2 <> https THEN 'pending' ELSE status END
		WHERE id = $1`, id, p.HTTPS, p.Port, p.ClearPort, p.Primary); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteDomain removes a hostname and hands "primary" to the oldest remaining one.
func (s *Store) DeleteDomain(ctx context.Context, id string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var resourceID string
	if err := tx.QueryRow(ctx, `DELETE FROM domains WHERE id = $1 RETURNING resource_id`, id).Scan(&resourceID); err != nil {
		return noRows(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE domains SET is_primary = true WHERE id = (SELECT id FROM domains WHERE resource_id = $1 ORDER BY created_at LIMIT 1)
		AND NOT EXISTS (SELECT 1 FROM domains WHERE resource_id = $1 AND is_primary)`, resourceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type DomainCheck struct {
	Status, Message, CertIssuer string
	Addresses                   []string
	CertExpiresAt               *time.Time
}

func (s *Store) SetDomainCheck(ctx context.Context, id string, c DomainCheck) error {
	if c.Addresses == nil {
		c.Addresses = []string{}
	}
	_, err := s.DB.Exec(ctx, `
		UPDATE domains SET status = $2, status_message = $3, addresses = $4, cert_issuer = $5, cert_expires_at = $6, last_checked_at = now()
		WHERE id = $1`, id, c.Status, c.Message, c.Addresses, c.CertIssuer, c.CertExpiresAt)
	return err
}

// ProxyRoute is everything the proxy needs to route one hostname.
type ProxyRoute struct {
	Hostname    string
	HTTPS       bool
	Token       string
	Port        int    // 0 when neither the domain nor the resource has a port
	ContainerID string // empty when the resource isn't running
	Resource    string
}

func (s *Store) ProxyRoutes(ctx context.Context) ([]ProxyRoute, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT d.hostname, d.https, d.token, COALESCE(d.port, r.port, 0),
		       CASE WHEN r.status = 'running' THEN COALESCE(r.container_id, '') ELSE '' END, r.name
		FROM domains d JOIN resources r ON r.id = d.resource_id
		ORDER BY d.hostname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProxyRoute{}
	for rows.Next() {
		var p ProxyRoute
		if err := rows.Scan(&p.Hostname, &p.HTTPS, &p.Token, &p.Port, &p.ContainerID, &p.Resource); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

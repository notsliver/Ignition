package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

type Domain struct {
	ID            string         `json:"id"`
	ResourceID    string         `json:"resourceId"`
	Hostname      string         `json:"hostname"`
	Port          *int           `json:"port"`
	HTTPS         bool           `json:"https"`
	Primary       bool           `json:"primary"`
	Status        string         `json:"status"`
	StatusMessage string         `json:"statusMessage"`
	Addresses     []string       `json:"addresses"`
	CertIssuer    string         `json:"certIssuer"`
	CertExpiresAt *time.Time     `json:"certExpiresAt"`
	LastCheckedAt *time.Time     `json:"lastCheckedAt"`
	CreatedAt     time.Time      `json:"createdAt"`
	Token         string         `json:"-"`
	PathPrefix    string         `json:"pathPrefix"`   // "" = the whole site
	StripPrefix   bool           `json:"stripPrefix"`  // remove the prefix before passing the request on
	RedirectTo    string         `json:"redirectTo"`   // set: redirect instead of proxying
	RedirectCode  int            `json:"redirectCode"` // 301, 302, 307, 308
	AllowIPs      []string       `json:"allowIps"`     // set: only these addresses/ranges reach the app
	DNSProviderID *string        `json:"dnsProviderId"`
	DNSRecords    []DNSRecordRef `json:"dnsRecords"` // records created through a DNS integration
}

// DNSRecordRef is a record an integration created for a domain, so it can be updated or removed.
type DNSRecordRef struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	ZoneID  string `json:"zoneId"`
	Content string `json:"content"`
}

const domainSelect = `SELECT id, resource_id, hostname, port, https, is_primary, status, status_message, addresses, cert_issuer,
	cert_expires_at, last_checked_at, created_at, token, path_prefix, strip_prefix, redirect_to, redirect_code, allow_ips, dns_provider_id, dns_records FROM domains`

func scanDomain(row pgx.Row) (*Domain, error) {
	var d Domain
	var recs []byte
	err := row.Scan(&d.ID, &d.ResourceID, &d.Hostname, &d.Port, &d.HTTPS, &d.Primary, &d.Status, &d.StatusMessage, &d.Addresses,
		&d.CertIssuer, &d.CertExpiresAt, &d.LastCheckedAt, &d.CreatedAt, &d.Token, &d.PathPrefix, &d.StripPrefix, &d.RedirectTo, &d.RedirectCode,
		&d.AllowIPs, &d.DNSProviderID, &recs)
	json.Unmarshal(recs, &d.DNSRecords)
	if d.DNSRecords == nil {
		d.DNSRecords = []DNSRecordRef{}
	}
	if d.AllowIPs == nil {
		d.AllowIPs = []string{}
	}
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
type NewDomain struct {
	Hostname, PathPrefix, RedirectTo, Token string
	Port                                    *int
	HTTPS, StripPrefix                      bool
	RedirectCode                            int
	AllowIPs                                []string
}

// CreateDomain adds a route. The first whole-site, non-redirect one on a resource becomes its primary.
func (s *Store) CreateDomain(ctx context.Context, resourceID string, n NewDomain, userID string) (string, error) {
	var id string
	if n.AllowIPs == nil {
		n.AllowIPs = []string{}
	}
	if n.RedirectCode == 0 {
		n.RedirectCode = 308
	}
	err := s.DB.QueryRow(ctx, `
		INSERT INTO domains (resource_id, hostname, port, https, token, created_by, path_prefix, strip_prefix, redirect_to, redirect_code, allow_ips, is_primary)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
		        $7 = '' AND $9 = '' AND NOT EXISTS (SELECT 1 FROM domains WHERE resource_id = $1 AND is_primary))
		RETURNING id`, resourceID, n.Hostname, n.Port, n.HTTPS, n.Token, nullable(userID), n.PathPrefix, n.StripPrefix, n.RedirectTo, n.RedirectCode, n.AllowIPs).Scan(&id)
	return id, err
}

// SetDomainDNS records which integration manages the domain's DNS records.
func (s *Store) SetDomainDNS(ctx context.Context, id string, providerID *string, recs []DNSRecordRef) error {
	if recs == nil {
		recs = []DNSRecordRef{}
	}
	b, _ := json.Marshal(recs)
	_, err := s.DB.Exec(ctx, `UPDATE domains SET dns_provider_id = $2, dns_records = $3 WHERE id = $1`, id, providerID, b)
	return err
}

// DomainRow is a domain with the names of what it belongs to, for the instance-wide list.
type DomainRow struct {
	Domain
	ResourceName   string `json:"resourceName"`
	ResourceType   string `json:"resourceType"`
	ResourceStatus string `json:"resourceStatus"`
	ProjectID      string `json:"projectId"`
	ProjectName    string `json:"projectName"`
	Environment    string `json:"environment"`
}

func (s *Store) AllDomains(ctx context.Context) ([]DomainRow, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT d.id, d.resource_id, d.hostname, d.port, d.https, d.is_primary, d.status, d.status_message, d.addresses, d.cert_issuer,
		       d.cert_expires_at, d.last_checked_at, d.created_at, d.token, d.path_prefix, d.strip_prefix, d.redirect_to, d.redirect_code,
		       d.allow_ips, d.dns_provider_id, d.dns_records, r.name, r.type, r.status, p.id, p.name, e.name
		FROM domains d JOIN resources r ON r.id = d.resource_id JOIN projects p ON p.id = r.project_id JOIN environments e ON e.id = r.environment_id
		ORDER BY d.hostname, d.path_prefix`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DomainRow{}
	for rows.Next() {
		var x DomainRow
		var recs []byte
		d := &x.Domain
		if err := rows.Scan(&d.ID, &d.ResourceID, &d.Hostname, &d.Port, &d.HTTPS, &d.Primary, &d.Status, &d.StatusMessage, &d.Addresses,
			&d.CertIssuer, &d.CertExpiresAt, &d.LastCheckedAt, &d.CreatedAt, &d.Token, &d.PathPrefix, &d.StripPrefix, &d.RedirectTo, &d.RedirectCode,
			&d.AllowIPs, &d.DNSProviderID, &recs, &x.ResourceName, &x.ResourceType, &x.ResourceStatus, &x.ProjectID, &x.ProjectName, &x.Environment); err != nil {
			return nil, err
		}
		json.Unmarshal(recs, &d.DNSRecords)
		if d.DNSRecords == nil {
			d.DNSRecords = []DNSRecordRef{}
		}
		if d.AllowIPs == nil {
			d.AllowIPs = []string{}
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

type DomainPatch struct {
	HTTPS        *bool     `json:"https"`
	Port         *int      `json:"port"`
	ClearPort    bool      `json:"-"`
	Primary      bool      `json:"primary"`
	StripPrefix  *bool     `json:"stripPrefix"`
	RedirectTo   *string   `json:"redirectTo"`
	RedirectCode *int      `json:"redirectCode"`
	AllowIPs     *[]string `json:"allowIps"`
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
		       status = CASE WHEN $2 IS NOT NULL AND $2 <> https THEN 'pending' ELSE status END,
		       strip_prefix = COALESCE($6, strip_prefix), redirect_to = COALESCE($7, redirect_to), redirect_code = COALESCE($8, redirect_code),
		       allow_ips = COALESCE($9, allow_ips)
		WHERE id = $1`, id, p.HTTPS, p.Port, p.ClearPort, p.Primary, p.StripPrefix, p.RedirectTo, p.RedirectCode, p.AllowIPs); err != nil {
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

// ProxyRoute is everything the proxy needs to route one hostname and path.
type ProxyRoute struct {
	Hostname     string
	HTTPS        bool
	Token        string
	Port         int    // 0 when neither the domain nor the resource has a port
	ContainerID  string // empty when the resource isn't running
	Resource     string
	PathPrefix   string
	StripPrefix  bool
	RedirectTo   string
	RedirectCode int
	AllowIPs     []string
}

func (s *Store) ProxyRoutes(ctx context.Context) ([]ProxyRoute, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT d.hostname, d.https, d.token, COALESCE(d.port, r.port, 0),
		       CASE WHEN r.status = 'running' THEN COALESCE(r.container_id, '') ELSE '' END, r.name,
		       d.path_prefix, d.strip_prefix, d.redirect_to, d.redirect_code, d.allow_ips
		FROM domains d JOIN resources r ON r.id = d.resource_id
		ORDER BY d.hostname, length(d.path_prefix) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProxyRoute{}
	for rows.Next() {
		var p ProxyRoute
		if err := rows.Scan(&p.Hostname, &p.HTTPS, &p.Token, &p.Port, &p.ContainerID, &p.Resource, &p.PathPrefix, &p.StripPrefix, &p.RedirectTo, &p.RedirectCode, &p.AllowIPs); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

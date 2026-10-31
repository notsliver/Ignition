package store

import (
	"context"
	"time"
)

/* ------------------------------ Shared networks ------------------------------ */

// NetworkSpec is how a shared network is created in Docker. Empty subnets let Docker choose.
type NetworkSpec struct {
	Subnet   string `json:"subnet"`
	Gateway  string `json:"gateway"`
	IPv6     bool   `json:"ipv6"`
	Subnet6  string `json:"subnet6"`
	Internal bool   `json:"internal"` // no route to the internet
}

type SharedNetwork struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Pinned      bool   `json:"pinned"`
	NetworkSpec
	Members   []NetworkMember `json:"members"`
	CreatedAt time.Time       `json:"createdAt"`
}

type NetworkMember struct {
	ResourceID   string `json:"resourceId"`
	ResourceName string `json:"resourceName"`
	ProjectName  string `json:"projectName"`
	IPv4         string `json:"ipv4"` // fixed address, "" = assigned by Docker
	IPv6         string `json:"ipv6"`
}

func (s *Store) ListSharedNetworks(ctx context.Context) ([]SharedNetwork, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT n.id, n.name, n.description, n.pinned, n.subnet, n.gateway, n.ipv6, n.subnet6, n.internal, n.created_at
		FROM shared_networks n ORDER BY n.name`)
	if err != nil {
		return nil, err
	}
	out := []SharedNetwork{}
	for rows.Next() {
		var n SharedNetwork
		if err := rows.Scan(&n.ID, &n.Name, &n.Description, &n.Pinned, &n.Subnet, &n.Gateway, &n.IPv6, &n.Subnet6, &n.Internal, &n.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		n.Members = []NetworkMember{}
		out = append(out, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = s.DB.Query(ctx, `
		SELECT m.network_id, r.id, r.name, p.name, m.ipv4, m.ipv6
		FROM resource_networks m JOIN resources r ON r.id = m.resource_id JOIN projects p ON p.id = r.project_id
		ORDER BY p.name, r.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[string]int{}
	for i, n := range out {
		idx[n.ID] = i
	}
	for rows.Next() {
		var netID string
		var m NetworkMember
		if err := rows.Scan(&netID, &m.ResourceID, &m.ResourceName, &m.ProjectName, &m.IPv4, &m.IPv6); err != nil {
			return nil, err
		}
		if i, ok := idx[netID]; ok {
			out[i].Members = append(out[i].Members, m)
		}
	}
	return out, rows.Err()
}

func (s *Store) GetSharedNetwork(ctx context.Context, name string) (*SharedNetwork, error) {
	var n SharedNetwork
	err := s.DB.QueryRow(ctx, `SELECT id, name, description, pinned, subnet, gateway, ipv6, subnet6, internal, created_at FROM shared_networks WHERE name = $1`, name).
		Scan(&n.ID, &n.Name, &n.Description, &n.Pinned, &n.Subnet, &n.Gateway, &n.IPv6, &n.Subnet6, &n.Internal, &n.CreatedAt)
	return &n, noRows(err)
}

func (s *Store) CreateSharedNetwork(ctx context.Context, name, description string, spec NetworkSpec, userID string) (string, error) {
	var id string
	err := s.DB.QueryRow(ctx, `
		INSERT INTO shared_networks (name, description, pinned, subnet, gateway, ipv6, subnet6, internal, created_by)
		VALUES ($1, $2, true, $3, $4, $5, $6, $7, $8) RETURNING id`,
		name, description, spec.Subnet, spec.Gateway, spec.IPv6, spec.Subnet6, spec.Internal, nullable(userID)).Scan(&id)
	return id, err
}

func (s *Store) UpdateSharedNetwork(ctx context.Context, name, description string) error {
	_, err := s.DB.Exec(ctx, `UPDATE shared_networks SET description = $2, pinned = true WHERE name = $1`, name, description)
	return err
}

func (s *Store) DeleteSharedNetwork(ctx context.Context, name string) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM shared_networks n WHERE name = $1 AND NOT EXISTS (SELECT 1 FROM resource_networks m WHERE m.network_id = n.id)`, name)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// NetworkAddresses returns a resource's fixed addresses per shared network.
func (s *Store) NetworkAddresses(ctx context.Context, resourceID string) (map[string]NetAddress, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT n.name, m.ipv4, m.ipv6 FROM resource_networks m JOIN shared_networks n ON n.id = m.network_id
		WHERE m.resource_id = $1 AND (m.ipv4 <> '' OR m.ipv6 <> '')`, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]NetAddress{}
	for rows.Next() {
		var name string
		var a NetAddress
		if err := rows.Scan(&name, &a.IPv4, &a.IPv6); err != nil {
			return nil, err
		}
		out[name] = a
	}
	return out, rows.Err()
}

// TakenAddress reports which other resource already has ip on the network, if any.
func (s *Store) TakenAddress(ctx context.Context, network, resourceID, ip string) (string, error) {
	var name string
	err := s.DB.QueryRow(ctx, `
		SELECT r.name FROM resource_networks m JOIN shared_networks n ON n.id = m.network_id JOIN resources r ON r.id = m.resource_id
		WHERE n.name = $1 AND m.resource_id <> $2 AND (m.ipv4 = $3 OR m.ipv6 = $3) LIMIT 1`, network, resourceID, ip).Scan(&name)
	if err != nil {
		return "", noRows(err)
	}
	return name, nil
}

/* ----------------------------------- Ports ----------------------------------- */

type PortReservation struct {
	ID        string    `json:"id"`
	Port      int       `json:"port"`
	Protocol  string    `json:"protocol"`
	Note      string    `json:"note"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) PortReservations(ctx context.Context) ([]PortReservation, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT r.id, r.port, r.protocol, r.note, COALESCE(u.email, ''), r.created_at
		FROM port_reservations r LEFT JOIN users u ON u.id = r.created_by ORDER BY r.port, r.protocol`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PortReservation{}
	for rows.Next() {
		var p PortReservation
		if err := rows.Scan(&p.ID, &p.Port, &p.Protocol, &p.Note, &p.CreatedBy, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ReservePort(ctx context.Context, port int, protocol, note, userID string) (string, error) {
	var id string
	err := s.DB.QueryRow(ctx, `INSERT INTO port_reservations (port, protocol, note, created_by) VALUES ($1, $2, $3, $4) RETURNING id`,
		port, protocol, note, nullable(userID)).Scan(&id)
	return id, err
}

func (s *Store) DeleteReservation(ctx context.Context, id string) (int, string, error) {
	var port int
	var proto string
	err := s.DB.QueryRow(ctx, `DELETE FROM port_reservations WHERE id = $1 RETURNING port, protocol`, id).Scan(&port, &proto)
	return port, proto, noRows(err)
}

func (s *Store) ReservedPort(ctx context.Context, port int, protocol string) (string, bool) {
	var note string
	err := s.DB.QueryRow(ctx, `SELECT note FROM port_reservations WHERE port = $1 AND protocol = $2`, port, protocol).Scan(&note)
	return note, err == nil
}

// PortAssignment is a host port a resource asks for (or got from Docker when it asked for none).
type PortAssignment struct {
	ResourceID    string `json:"resourceId"`
	ResourceName  string `json:"resourceName"`
	ProjectID     string `json:"projectId"`
	ProjectName   string `json:"projectName"`
	HostPort      *int   `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
	BindAddress   string `json:"bindAddress"`
	Status        string `json:"status"`
	ContainerID   string `json:"-"`
}

func (s *Store) PortAssignments(ctx context.Context) ([]PortAssignment, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT r.id, r.name, p.id, p.name, rp.host_port, rp.container_port, rp.protocol, rp.bind_address, r.status, COALESCE(r.container_id, '')
		FROM resource_ports rp JOIN resources r ON r.id = rp.resource_id JOIN projects p ON p.id = r.project_id
		ORDER BY rp.host_port NULLS LAST, r.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PortAssignment{}
	for rows.Next() {
		var a PortAssignment
		if err := rows.Scan(&a.ResourceID, &a.ResourceName, &a.ProjectID, &a.ProjectName, &a.HostPort, &a.ContainerPort, &a.Protocol, &a.BindAddress, &a.Status, &a.ContainerID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

/* --------------------------------- Firewall ---------------------------------- */

type FirewallRule struct {
	ID        string    `json:"id"`
	ServerID  string    `json:"serverId"`
	Port      int       `json:"port"`
	Protocol  string    `json:"protocol"`
	Sources   []string  `json:"sources"`
	Note      string    `json:"note"`
	Enabled   bool      `json:"enabled"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) FirewallRules(ctx context.Context, serverID string) ([]FirewallRule, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT f.id, f.server_id, f.port, f.protocol, f.sources, f.note, f.enabled, COALESCE(u.email, ''), f.created_at
		FROM firewall_rules f LEFT JOIN users u ON u.id = f.created_by
		WHERE ($1 = '' OR f.server_id::text = $1) ORDER BY f.port, f.protocol`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FirewallRule{}
	for rows.Next() {
		var f FirewallRule
		if err := rows.Scan(&f.ID, &f.ServerID, &f.Port, &f.Protocol, &f.Sources, &f.Note, &f.Enabled, &f.CreatedBy, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) GetFirewallRule(ctx context.Context, id string) (*FirewallRule, error) {
	var f FirewallRule
	err := s.DB.QueryRow(ctx, `SELECT id, server_id, port, protocol, sources, note, enabled, '', created_at FROM firewall_rules WHERE id = $1`, id).
		Scan(&f.ID, &f.ServerID, &f.Port, &f.Protocol, &f.Sources, &f.Note, &f.Enabled, &f.CreatedBy, &f.CreatedAt)
	return &f, noRows(err)
}

func (s *Store) SaveFirewallRule(ctx context.Context, f FirewallRule, userID string) (string, error) {
	var id string
	err := s.DB.QueryRow(ctx, `
		INSERT INTO firewall_rules (server_id, port, protocol, sources, note, enabled, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (server_id, port, protocol) DO UPDATE SET sources = EXCLUDED.sources, note = EXCLUDED.note, enabled = EXCLUDED.enabled
		RETURNING id`, f.ServerID, f.Port, f.Protocol, f.Sources, f.Note, f.Enabled, nullable(userID)).Scan(&id)
	return id, err
}

func (s *Store) DeleteFirewallRule(ctx context.Context, id string) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM firewall_rules WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

/* ------------------------------- DNS providers ------------------------------- */

type DNSProvider struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Kind       string     `json:"kind"`
	Zones      []string   `json:"zones"`
	VerifiedAt *time.Time `json:"verifiedAt"`
	Domains    int        `json:"domains"`
	CreatedAt  time.Time  `json:"createdAt"`
	TokenEnc   []byte     `json:"-"`
}

func (s *Store) DNSProviders(ctx context.Context) ([]DNSProvider, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT p.id, p.name, p.kind, p.zones, p.verified_at, (SELECT count(*) FROM domains d WHERE d.dns_provider_id = p.id), p.created_at, p.token_enc
		FROM dns_providers p ORDER BY p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DNSProvider{}
	for rows.Next() {
		var p DNSProvider
		if err := rows.Scan(&p.ID, &p.Name, &p.Kind, &p.Zones, &p.VerifiedAt, &p.Domains, &p.CreatedAt, &p.TokenEnc); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetDNSProvider(ctx context.Context, id string) (*DNSProvider, error) {
	var p DNSProvider
	err := s.DB.QueryRow(ctx, `SELECT id, name, kind, zones, verified_at, 0, created_at, token_enc FROM dns_providers WHERE id = $1`, id).
		Scan(&p.ID, &p.Name, &p.Kind, &p.Zones, &p.VerifiedAt, &p.Domains, &p.CreatedAt, &p.TokenEnc)
	return &p, noRows(err)
}

// CreateDNSProvider stores a provider; the token is sealed by the caller, bound to the new row's ID.
func (s *Store) CreateDNSProvider(ctx context.Context, name, kind string, zones []string, seal func(id string) ([]byte, error), userID string) (string, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO dns_providers (name, kind, token_enc, zones, verified_at, created_by) VALUES ($1, $2, '\x00', $3, now(), $4) RETURNING id`,
		name, kind, zones, nullable(userID)).Scan(&id); err != nil {
		return "", err
	}
	sealed, err := seal(id)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE dns_providers SET token_enc = $2 WHERE id = $1`, id, sealed); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func (s *Store) SetDNSProviderZones(ctx context.Context, id string, zones []string) error {
	_, err := s.DB.Exec(ctx, `UPDATE dns_providers SET zones = $2, verified_at = now() WHERE id = $1`, id, zones)
	return err
}

func (s *Store) DeleteDNSProvider(ctx context.Context, id string) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM dns_providers WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

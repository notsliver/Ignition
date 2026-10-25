package store

import (
	"context"
	"time"
)

type PortMapping struct {
	ID            string `json:"id"`
	HostPort      *int   `json:"hostPort"` // nil: Docker picks a free port
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
	BindAddress   string `json:"bindAddress"`
}

type Networking struct {
	Ports    []PortMapping `json:"ports"`
	Networks []string      `json:"networks"` // extra networks besides "ignition"
	Aliases  []string      `json:"aliases"`  // extra DNS names besides the resource name
}

func (s *Store) GetNetworking(ctx context.Context, resourceID string) (Networking, error) {
	n := Networking{Ports: []PortMapping{}, Networks: []string{}, Aliases: []string{}}
	rows, err := s.DB.Query(ctx, `SELECT id, host_port, container_port, protocol, bind_address FROM resource_ports WHERE resource_id = $1 ORDER BY container_port, created_at`, resourceID)
	if err != nil {
		return n, err
	}
	for rows.Next() {
		var p PortMapping
		if err := rows.Scan(&p.ID, &p.HostPort, &p.ContainerPort, &p.Protocol, &p.BindAddress); err != nil {
			rows.Close()
			return n, err
		}
		n.Ports = append(n.Ports, p)
	}
	rows.Close()
	rows, err = s.DB.Query(ctx, `SELECT n.name FROM resource_networks m JOIN shared_networks n ON n.id = m.network_id WHERE m.resource_id = $1 ORDER BY n.name`, resourceID)
	if err != nil {
		return n, err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return n, err
		}
		n.Networks = append(n.Networks, name)
	}
	rows.Close()
	err = s.DB.QueryRow(ctx, `SELECT aliases FROM resources WHERE id = $1`, resourceID).Scan(&n.Aliases)
	return n, noRows(err)
}

// SetNetworking replaces the whole configuration in one transaction. Shared networks named in n.Networks
// are created when new; shared networks nobody uses any more are deleted.
func (s *Store) SetNetworking(ctx context.Context, resourceID, userID string, n Networking) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM resource_ports WHERE resource_id = $1`, resourceID); err != nil {
		return err
	}
	for _, p := range n.Ports {
		if _, err := tx.Exec(ctx, `INSERT INTO resource_ports (resource_id, host_port, container_port, protocol, bind_address) VALUES ($1, $2, $3, $4, $5)`,
			resourceID, p.HostPort, p.ContainerPort, p.Protocol, p.BindAddress); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM resource_networks WHERE resource_id = $1`, resourceID); err != nil {
		return err
	}
	for _, name := range n.Networks {
		if _, err := tx.Exec(ctx, `INSERT INTO shared_networks (name, created_by) VALUES ($1, $2) ON CONFLICT (name) DO NOTHING`, name, nullable(userID)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO resource_networks (resource_id, network_id) SELECT $1, id FROM shared_networks WHERE name = $2`, resourceID, name); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM shared_networks n WHERE NOT EXISTS (SELECT 1 FROM resource_networks m WHERE m.network_id = n.id)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE resources SET aliases = $2, updated_at = now() WHERE id = $1`, resourceID, n.Aliases); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// TakenHostPort reports which other resource already maps hostPort/protocol, if any.
func (s *Store) TakenHostPort(ctx context.Context, resourceID string, hostPort int, protocol string) (string, error) {
	var name string
	err := s.DB.QueryRow(ctx, `
		SELECT r.name FROM resource_ports p JOIN resources r ON r.id = p.resource_id
		WHERE p.host_port = $1 AND p.protocol = $2 AND p.resource_id <> $3 LIMIT 1`, hostPort, protocol, resourceID).Scan(&name)
	if err != nil {
		return "", noRows(err)
	}
	return name, nil
}

// Peer is another resource that shares a network with this one.
type Peer struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	ProjectName string   `json:"projectName"`
	Environment string   `json:"environment"`
	Status      string   `json:"status"`
	Port        *int     `json:"port"`
	Hostnames   []string `json:"hostnames"`
	SameEnv     bool     `json:"sameEnvironment"` // reachable over the private environment network
	Shared      []string `json:"shared"`          // shared networks both resources joined
}

// Peers lists resources this one can reach: everything in its environment, plus members of shared networks it joined.
func (s *Store) Peers(ctx context.Context, resourceID string) ([]Peer, error) {
	rows, err := s.DB.Query(ctx, `
		WITH me AS (SELECT environment_id FROM resources WHERE id = $1),
		     mine AS (SELECT network_id FROM resource_networks WHERE resource_id = $1)
		SELECT r.id, r.name, r.type, p.name, e.name, r.status, r.port, array_prepend(r.name, r.aliases),
		       r.environment_id = (SELECT environment_id FROM me),
		       COALESCE((SELECT array_agg(n.name ORDER BY n.name) FROM resource_networks m JOIN shared_networks n ON n.id = m.network_id
		                 WHERE m.resource_id = r.id AND m.network_id IN (SELECT network_id FROM mine)), '{}')
		FROM resources r JOIN projects p ON p.id = r.project_id JOIN environments e ON e.id = r.environment_id
		WHERE r.id <> $1 AND p.archived_at IS NULL
		  AND (r.environment_id = (SELECT environment_id FROM me)
		       OR EXISTS (SELECT 1 FROM resource_networks m WHERE m.resource_id = r.id AND m.network_id IN (SELECT network_id FROM mine)))
		ORDER BY p.name, r.name`, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Peer{}
	for rows.Next() {
		var p Peer
		if err := rows.Scan(&p.ID, &p.Name, &p.Type, &p.ProjectName, &p.Environment, &p.Status, &p.Port, &p.Hostnames, &p.SameEnv, &p.Shared); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SharedNetworkInfo is a shared network and the resources in it.
type SharedNetworkInfo struct {
	Name    string   `json:"name"`
	Members []string `json:"members"` // "resource (project)"
}

func (s *Store) SharedNetworks(ctx context.Context) ([]SharedNetworkInfo, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT n.name, COALESCE(array_agg(r.name || ' (' || p.name || ')' ORDER BY p.name, r.name) FILTER (WHERE r.id IS NOT NULL), '{}')
		FROM shared_networks n LEFT JOIN resource_networks m ON m.network_id = n.id
		LEFT JOIN resources r ON r.id = m.resource_id LEFT JOIN projects p ON p.id = r.project_id
		GROUP BY n.name ORDER BY n.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SharedNetworkInfo{}
	for rows.Next() {
		var n SharedNetworkInfo
		if err := rows.Scan(&n.Name, &n.Members); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Attachment is what a running resource container should be connected to.
type NetworkTarget struct {
	ResourceID, ContainerID, EnvironmentID string
	Aliases                                []string // resource name first
	Shared                                 []string // shared network names
}

func (s *Store) NetworkTargets(ctx context.Context) ([]NetworkTarget, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT r.id, r.container_id, r.environment_id, array_prepend(r.name, r.aliases),
		       COALESCE((SELECT array_agg(n.name) FROM resource_networks m JOIN shared_networks n ON n.id = m.network_id WHERE m.resource_id = r.id), '{}')
		FROM resources r WHERE COALESCE(r.container_id, '') <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NetworkTarget
	for rows.Next() {
		var t NetworkTarget
		if err := rows.Scan(&t.ResourceID, &t.ContainerID, &t.EnvironmentID, &t.Aliases, &t.Shared); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// EnvironmentIDs lists the environments that have resources; each gets a private network.
func (s *Store) EnvironmentIDs(ctx context.Context) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT DISTINCT r.environment_id FROM resources r`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

type NetworkTest struct {
	ID      int64     `json:"id"`
	Kind    string    `json:"kind"`
	Target  string    `json:"target"`
	OK      bool      `json:"ok"`
	Summary string    `json:"summary"`
	Output  string    `json:"output"`
	RanBy   string    `json:"ranBy"`
	RanAt   time.Time `json:"ranAt"`
}

func (s *Store) AddNetworkTest(ctx context.Context, resourceID, userID string, t NetworkTest) (int64, error) {
	var id int64
	err := s.DB.QueryRow(ctx, `INSERT INTO network_tests (resource_id, kind, target, ok, summary, output, ran_by) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		resourceID, t.Kind, t.Target, t.OK, t.Summary, t.Output, nullable(userID)).Scan(&id)
	return id, err
}

func (s *Store) NetworkTests(ctx context.Context, resourceID string, limit int) ([]NetworkTest, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT t.id, t.kind, t.target, t.ok, t.summary, t.output, COALESCE(u.name, ''), t.ran_at
		FROM network_tests t LEFT JOIN users u ON u.id = t.ran_by
		WHERE t.resource_id = $1 ORDER BY t.ran_at DESC LIMIT $2`, resourceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NetworkTest{}
	for rows.Next() {
		var t NetworkTest
		if err := rows.Scan(&t.ID, &t.Kind, &t.Target, &t.OK, &t.Summary, &t.Output, &t.RanBy, &t.RanAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

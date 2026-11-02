package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Allocation is one IP:port a server hands out, and what (if anything) has it.
type Allocation struct {
	ID           string    `json:"id"`
	IP           string    `json:"ip"`
	Port         int       `json:"port"`
	Alias        string    `json:"alias"`
	ResourceID   *string   `json:"resourceId"`
	ResourceName string    `json:"resourceName,omitempty"`
	TeamName     string    `json:"teamName,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

// Allocations are platform objects: read and written with the privileged pool by platform
// administrators only.
func (s *Store) Allocations(ctx context.Context, server string) ([]Allocation, error) {
	rows, err := s.DB.Query(ctx, `SELECT a.id, a.ip, a.port, a.alias, a.resource_id, coalesce(r.name,''), coalesce(t.name,''), a.created_at
		FROM server_allocations a LEFT JOIN resources r ON r.id = a.resource_id LEFT JOIN teams t ON t.id = r.team_id
		WHERE a.server_id = $1 ORDER BY a.ip, a.port`, server)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Allocation{}
	for rows.Next() {
		var a Allocation
		if err := rows.Scan(&a.ID, &a.IP, &a.Port, &a.Alias, &a.ResourceID, &a.ResourceName, &a.TeamName, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AddAllocations adds ports on one IP; ones that already exist are skipped. Returns how many were new.
func (s *Store) AddAllocations(ctx context.Context, server, ip, alias string, ports []int) (int, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	added := 0
	for _, p := range ports {
		tag, err := tx.Exec(ctx, `INSERT INTO server_allocations (server_id, ip, port, alias) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, server, ip, p, alias)
		if err != nil {
			return 0, err
		}
		added += int(tag.RowsAffected())
	}
	return added, tx.Commit(ctx)
}

// DeleteAllocations removes free allocations; ones a resource holds are left alone (and counted).
func (s *Store) DeleteAllocations(ctx context.Context, server string, ids []string) (deleted, inUse int, err error) {
	tag, err := s.DB.Exec(ctx, `DELETE FROM server_allocations WHERE server_id = $1 AND id = ANY($2::uuid[]) AND resource_id IS NULL`, server, ids)
	if err != nil {
		return 0, 0, err
	}
	err = s.DB.QueryRow(ctx, `SELECT count(*) FROM server_allocations WHERE server_id = $1 AND id = ANY($2::uuid[])`, server, ids).Scan(&inUse)
	return int(tag.RowsAffected()), inUse, err
}

func (s *Store) SetAllocationAlias(ctx context.Context, server, id, alias string) error {
	tag, err := s.DB.Exec(ctx, `UPDATE server_allocations SET alias = $3 WHERE server_id = $1 AND id = $2`, server, id, alias)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ServerHasAllocations says whether a server hands out ports from a pool (else the next free port is used).
func (s *Store) ServerHasAllocations(ctx context.Context, server string) bool {
	var ok bool
	s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM server_allocations WHERE server_id = $1)`, server).Scan(&ok)
	return ok
}

// ClaimAllocation gives a resource the lowest free allocation on the server, inside tx so a failed
// create releases it. ErrNotFound when every allocation is taken.
func (s *Store) ClaimAllocation(ctx context.Context, tx pgx.Tx, server, resource string) (*Allocation, error) {
	var a Allocation
	err := tx.QueryRow(ctx, `UPDATE server_allocations SET resource_id = $2 WHERE id = (
			SELECT id FROM server_allocations WHERE server_id = $1 AND resource_id IS NULL ORDER BY port, ip LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING id, ip, port, alias`, server, resource).Scan(&a.ID, &a.IP, &a.Port, &a.Alias)
	if err != nil {
		return nil, noRows(err)
	}
	return &a, nil
}

// ResourceAllocation is the allocation a resource holds, if any.
func (s *Store) ResourceAllocation(ctx context.Context, resource string) (*Allocation, error) {
	var a Allocation
	err := s.DB.QueryRow(ctx, `SELECT id, ip, port, alias FROM server_allocations WHERE resource_id = $1 ORDER BY port LIMIT 1`, resource).Scan(&a.ID, &a.IP, &a.Port, &a.Alias)
	if err != nil {
		return nil, noRows(err)
	}
	return &a, nil
}

func (s *Store) SetServerVisible(ctx context.Context, server string, visible bool) error {
	tag, err := s.DB.Exec(ctx, `UPDATE servers SET visible = $2, updated_at = now() WHERE id = $1`, server, visible)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

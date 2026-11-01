package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// GameServer is a game resource's settings. Spec is the games package's Spec as JSON.
type GameServer struct {
	ResourceID     string          `json:"resourceId"`
	Game           string          `json:"game"`
	Spec           json.RawMessage `json:"spec"`
	Initialized    bool            `json:"initialized"`
	EULAAcceptedAt *time.Time      `json:"eulaAcceptedAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

func (s *Store) GetGameServer(ctx context.Context, resourceID string) (*GameServer, error) {
	var g GameServer
	err := s.DB.QueryRow(ctx, `SELECT resource_id, game, spec, initialized, eula_accepted_at, updated_at FROM game_servers WHERE resource_id = $1`, resourceID).
		Scan(&g.ResourceID, &g.Game, &g.Spec, &g.Initialized, &g.EULAAcceptedAt, &g.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (s *Store) CreateGameServer(ctx context.Context, tx pgx.Tx, resourceID, game string, spec any, userID string) error {
	b, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO game_servers (resource_id, game, spec, eula_accepted_by, eula_accepted_at) VALUES ($1, $2, $3, $4, now())`,
		resourceID, game, b, nullable(userID))
	return err
}

// UpdateGameServer saves a changed spec and the image it now runs on.
func (s *Store) UpdateGameServer(ctx context.Context, resourceID string, spec any, image string) error {
	b, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE game_servers SET spec = $2, updated_at = now() WHERE resource_id = $1`, resourceID, b); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE resources SET image = $2, updated_at = now() WHERE id = $1`, resourceID, image); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MarkGameInitialized records that the game wrote its own settings, so setup values stop being passed.
func (s *Store) MarkGameInitialized(ctx context.Context, resourceID string) error {
	_, err := s.DB.Exec(ctx, `UPDATE game_servers SET initialized = true WHERE resource_id = $1 AND NOT initialized`, resourceID)
	return err
}

// TakenHostPorts lists host ports Ignition has assigned (any protocol), for picking a free game port.
func (s *Store) TakenHostPorts(ctx context.Context, protocol string) (map[int]bool, error) {
	rows, err := s.DB.Query(ctx, `SELECT host_port FROM resource_ports WHERE host_port IS NOT NULL AND protocol = $1`, protocol)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out[p] = true
	}
	return out, rows.Err()
}

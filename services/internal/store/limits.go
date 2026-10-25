package store

import (
	"context"
	"time"
)

type Limits struct {
	MemoryMB            int     `json:"memoryMb"`
	MemoryReservationMB *int    `json:"memoryReservationMb"`
	CPUs                float64 `json:"cpus"`
	PidsLimit           *int    `json:"pidsLimit"`
	AllowSwap           bool    `json:"allowSwap"`
	RestartPolicy       string  `json:"restartPolicy"` // always, on-failure, never
	MaxRestarts         int     `json:"maxRestarts"`   // for on-failure
}

func (s *Store) GetLimits(ctx context.Context, resourceID string) (Limits, error) {
	var l Limits
	err := s.DB.QueryRow(ctx, `
		SELECT memory_mb, memory_reservation_mb, cpus::float8, pids_limit, allow_swap, restart_policy, max_restarts
		FROM resources WHERE id = $1`, resourceID).Scan(&l.MemoryMB, &l.MemoryReservationMB, &l.CPUs, &l.PidsLimit, &l.AllowSwap, &l.RestartPolicy, &l.MaxRestarts)
	return l, noRows(err)
}

func (s *Store) SetLimits(ctx context.Context, resourceID string, l Limits) error {
	_, err := s.DB.Exec(ctx, `
		UPDATE resources SET memory_mb = $2, memory_reservation_mb = $3, cpus = $4, pids_limit = $5, allow_swap = $6,
		       restart_policy = $7, max_restarts = $8, updated_at = now()
		WHERE id = $1`, resourceID, l.MemoryMB, l.MemoryReservationMB, l.CPUs, l.PidsLimit, l.AllowSwap, l.RestartPolicy, l.MaxRestarts)
	return err
}

// UsageSummary is the last 7 days of samples (the retention window).
type UsageSummary struct {
	Samples    int        `json:"samples"`
	Since      *time.Time `json:"since"`
	CPUAvg     float64    `json:"cpuAvg"`  // percent of one core
	CPUPeak    float64    `json:"cpuPeak"` // percent of one core
	MemoryAvg  int64      `json:"memoryAvg"`
	MemoryPeak int64      `json:"memoryPeak"`
	NearMemCap int        `json:"nearMemoryLimit"` // samples at ≥ 90% of the memory limit
}

func (s *Store) UsageSummary(ctx context.Context, resourceID string) (UsageSummary, error) {
	var u UsageSummary
	err := s.DB.QueryRow(ctx, `
		SELECT count(*), min(at), COALESCE(avg(cpu_percent), 0), COALESCE(max(cpu_percent), 0),
		       COALESCE(avg(memory_bytes), 0)::bigint, COALESCE(max(memory_bytes), 0),
		       count(*) FILTER (WHERE memory_limit > 0 AND memory_bytes >= memory_limit * 0.9)
		FROM metric_samples WHERE resource_id = $1 AND at > now() - interval '7 days'`, resourceID).
		Scan(&u.Samples, &u.Since, &u.CPUAvg, &u.CPUPeak, &u.MemoryAvg, &u.MemoryPeak, &u.NearMemCap)
	return u, err
}

type Volume struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	MountPath string    `json:"mountPath"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) Volumes(ctx context.Context, resourceID string) ([]Volume, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, name, mount_path, created_at FROM resource_volumes WHERE resource_id = $1 ORDER BY created_at, name`, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Volume{}
	for rows.Next() {
		var v Volume
		if err := rows.Scan(&v.ID, &v.Name, &v.MountPath, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SetVolumes replaces the volume list. Keeps data_path in step with the first volume for older callers.
func (s *Store) SetVolumes(ctx context.Context, resourceID string, vols []Volume) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM resource_volumes WHERE resource_id = $1`, resourceID); err != nil {
		return err
	}
	first := ""
	for _, v := range vols {
		if first == "" {
			first = v.MountPath
		}
		if _, err := tx.Exec(ctx, `INSERT INTO resource_volumes (resource_id, name, mount_path) VALUES ($1, $2, $3)`, resourceID, v.Name, v.MountPath); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE resources SET data_path = $2, updated_at = now() WHERE id = $1`, resourceID, first); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

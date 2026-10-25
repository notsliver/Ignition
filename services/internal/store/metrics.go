package store

import (
	"context"
	"time"
)

type MetricSample struct {
	ResourceID  string
	At          time.Time
	CPUPercent  float64
	MemoryBytes uint64
	MemoryLimit uint64
	NetRxBps    *float64
	NetTxBps    *float64
}

func (s *Store) InsertMetric(ctx context.Context, m MetricSample) error {
	_, err := s.DB.Exec(ctx, `
		INSERT INTO metric_samples (resource_id, at, cpu_percent, memory_bytes, memory_limit, net_rx_bps, net_tx_bps)
		VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT DO NOTHING`,
		m.ResourceID, m.At, m.CPUPercent, int64(m.MemoryBytes), int64(m.MemoryLimit), m.NetRxBps, m.NetTxBps)
	return err
}

func (s *Store) PruneMetrics(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := s.DB.Exec(ctx, `DELETE FROM metric_samples WHERE at < now() - $1::interval`, interval(olderThan))
	return tag.RowsAffected(), err
}

// MetricBucket is one point on a chart: averages and peaks over `step`.
// Buckets with no samples are returned with Samples = 0 so the UI can show gaps.
type MetricBucket struct {
	At          time.Time `json:"at"`
	Samples     int       `json:"samples"`
	CPUAvg      *float64  `json:"cpuAvg"`
	CPUMax      *float64  `json:"cpuMax"`
	MemoryAvg   *float64  `json:"memoryAvg"`
	MemoryMax   *float64  `json:"memoryMax"`
	MemoryLimit *float64  `json:"memoryLimit"`
	NetRxAvg    *float64  `json:"netRxAvg"`
	NetTxAvg    *float64  `json:"netTxAvg"`
}

// MetricSeries returns evenly spaced buckets covering [now-window, now].
func (s *Store) MetricSeries(ctx context.Context, resourceID string, window, step time.Duration) ([]MetricBucket, error) {
	rows, err := s.DB.Query(ctx, `
		WITH buckets AS (
			SELECT generate_series(date_bin($3::interval, now() - $2::interval, 'epoch'), now(), $3::interval) AS at
		), agg AS (
			SELECT date_bin($3::interval, at, 'epoch') AS at, count(*) AS n,
			       avg(cpu_percent) AS cpu_avg, max(cpu_percent) AS cpu_max,
			       avg(memory_bytes) AS mem_avg, max(memory_bytes) AS mem_max, max(memory_limit) AS mem_limit,
			       avg(net_rx_bps) AS rx, avg(net_tx_bps) AS tx
			FROM metric_samples
			WHERE resource_id = $1 AND at >= now() - $2::interval - $3::interval
			GROUP BY 1
		)
		SELECT b.at, COALESCE(a.n, 0), a.cpu_avg, a.cpu_max, a.mem_avg, a.mem_max, a.mem_limit, a.rx, a.tx
		FROM buckets b LEFT JOIN agg a ON a.at = b.at
		ORDER BY b.at`, resourceID, interval(window), interval(step))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MetricBucket{}
	for rows.Next() {
		var b MetricBucket
		if err := rows.Scan(&b.At, &b.Samples, &b.CPUAvg, &b.CPUMax, &b.MemoryAvg, &b.MemoryMax, &b.MemoryLimit, &b.NetRxAvg, &b.NetTxAvg); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) LastMetricAt(ctx context.Context, resourceID string) (*time.Time, error) {
	var at *time.Time
	err := s.DB.QueryRow(ctx, `SELECT max(at) FROM metric_samples WHERE resource_id = $1`, resourceID).Scan(&at)
	return at, err
}

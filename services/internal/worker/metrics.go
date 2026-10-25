package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/notsliver/Ignition/services/internal/store"
)

const (
	metricsInterval  = 15 * time.Second
	metricsRetention = 7 * 24 * time.Hour
)

type netCounter struct {
	container string
	rx, tx    uint64
	at        time.Time
}

// metricsLoop samples every running container on an interval and stores the result.
// Network rates are derived from Docker's cumulative counters between consecutive samples.
func (w *Worker) metricsLoop(ctx context.Context) {
	last := map[string]netCounter{}
	var mu sync.Mutex
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	tick := time.NewTicker(metricsInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-prune.C:
			if n, err := w.Store.PruneMetrics(ctx, metricsRetention); err == nil && n > 0 {
				slog.Info("pruned old metrics", "rows", n)
			}
			continue
		case <-tick.C:
		}

		rows, err := w.Store.DB.Query(ctx, `SELECT id, container_id FROM resources WHERE status = 'running' AND container_id <> ''`)
		if err != nil {
			continue
		}
		type target struct{ id, container string }
		var targets []target
		for rows.Next() {
			var t target
			rows.Scan(&t.id, &t.container)
			targets = append(targets, t)
		}
		rows.Close()

		// Docker's stats call takes ~1s per container (it waits for two CPU readings), so sample in parallel.
		sem := make(chan struct{}, 8)
		var wg sync.WaitGroup
		for _, t := range targets {
			wg.Add(1)
			sem <- struct{}{}
			go func(t target) {
				defer wg.Done()
				defer func() { <-sem }()
				sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				st, err := w.Runtime.Stats(sctx, t.container)
				if err != nil {
					return
				}
				at := st.CollectedAt
				if at.IsZero() {
					at = time.Now()
				}
				sample := store.MetricSample{ResourceID: t.id, At: at.UTC().Truncate(time.Second), CPUPercent: st.CPUPercent, MemoryBytes: st.MemoryBytes, MemoryLimit: st.MemoryLimit}
				mu.Lock()
				prev, ok := last[t.id]
				last[t.id] = netCounter{container: t.container, rx: st.NetworkRxB, tx: st.NetworkTxB, at: at}
				mu.Unlock()
				// Only compute a rate against the same container; a new container resets the counters.
				if ok && prev.container == t.container && at.After(prev.at) && st.NetworkRxB >= prev.rx && st.NetworkTxB >= prev.tx {
					secs := at.Sub(prev.at).Seconds()
					rx, tx := float64(st.NetworkRxB-prev.rx)/secs, float64(st.NetworkTxB-prev.tx)/secs
					sample.NetRxBps, sample.NetTxBps = &rx, &tx
				}
				if err := w.Store.InsertMetric(ctx, sample); err != nil {
					slog.Warn("store metric failed", "resource", t.id, "error", err)
				}
			}(t)
		}
		wg.Wait()
	}
}

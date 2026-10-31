package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/notsliver/Ignition/services/internal/proxy"
)

// ProxyLoop keeps the domain proxy's routes in step with the database (containers change on every deploy,
// stop and start) and re-checks domains in the background: unfinished ones every minute, working ones hourly.
func (w *Worker) ProxyLoop(ctx context.Context) {
	sync := time.NewTicker(3 * time.Second)
	defer sync.Stop()
	check := time.NewTicker(time.Minute)
	defer check.Stop()
	nets := time.NewTicker(15 * time.Second)
	defer nets.Stop()
	w.ReconcileNetworks(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-sync.C:
			routes, err := w.Store.ProxyRoutes(ctx)
			if err != nil {
				continue
			}
			if changed, err := w.Proxy.Sync(ctx, routes, proxy.LoadSettings(ctx, w.Store)); err != nil {
				slog.Debug("proxy sync failed", "error", err)
			} else if changed {
				slog.Info("proxy routes updated", "domains", len(routes))
			}
		case <-nets.C:
			w.ReconcileNetworks(ctx)
		case <-check.C:
			rows, err := w.Store.DB.Query(ctx, `
				SELECT id FROM domains
				WHERE last_checked_at IS NULL OR last_checked_at < now() - CASE WHEN status = 'active' THEN interval '1 hour' ELSE interval '1 minute' END
				ORDER BY last_checked_at NULLS FIRST LIMIT 20`)
			if err != nil {
				continue
			}
			var ids []string
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()
			for _, id := range ids {
				d, err := w.Store.GetDomain(ctx, id)
				if err != nil {
					continue
				}
				cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
				w.Store.SetDomainCheck(cctx, id, w.Proxy.Check(cctx, *d))
				cancel()
			}
		}
	}
}

package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/notsliver/Ignition/services/internal/firewall"
	"github.com/notsliver/Ignition/services/internal/store"
)

// ReconcileFirewalls re-applies host firewall rules that drifted: a reboot clears iptables, and
// Docker may recreate its chains. Servers with no rules and nothing applied are left alone.
func (w *Worker) ReconcileFirewalls(ctx context.Context) {
	all, err := w.Store.FirewallRules(ctx, "")
	if err != nil {
		return
	}
	byServer := map[string][]store.FirewallRule{}
	for _, f := range all {
		byServer[f.ServerID] = append(byServer[f.ServerID], f)
	}
	for serverID, rules := range byServer {
		srv, err := w.Store.GetServer(ctx, serverID)
		if err != nil || srv.Maintenance {
			continue
		}
		dk, err := w.registry().Docker(srv)
		if err != nil {
			continue
		}
		var want []firewall.Rule
		for _, f := range rules {
			if f.Enabled {
				want = append(want, firewall.Rule{Port: f.Port, Protocol: f.Protocol, Sources: f.Sources})
			}
		}
		code, out, err := dk.RunHostTool(ctx, firewall.Image, firewall.StatusScript, 60*time.Second)
		if err != nil || code != 0 {
			continue
		}
		applied, dockerUser := firewall.ParseStatus(out)
		if !dockerUser || applied == firewall.Hash(want) || (applied == "none" && len(want) == 0) {
			continue
		}
		if code, out, err := dk.RunHostTool(ctx, firewall.Image, firewall.Script(want), 90*time.Second); err != nil || code != 0 {
			slog.Warn("firewall reapply failed", "server", srv.Name, "error", err, "output", out)
			continue
		}
		w.Store.AddServerEvent(ctx, srv.ID, "security", "warn", "Firewall rules had drifted from what's saved (a reboot or Docker restart?) and were applied again.", "system", map[string]any{"rules": len(want)})
	}
}

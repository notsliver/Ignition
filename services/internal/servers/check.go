package servers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/store"
)

// Check probes one server, stores the outcome and records transitions as events. The worker
// calls it on every interval; "Test connection" calls it directly.
func Check(ctx context.Context, reg *Registry, st *store.Store, srv *store.Server, actor string) Observation {
	o := reg.Probe(ctx, srv)
	settings := ParseSettings(srv.Settings)
	// The sampler marks the collector "probe" once the probe answers; a check keeps that until a
	// sample says otherwise, so "Test connection" doesn't flip it back.
	var prev Info
	json.Unmarshal(srv.Info, &prev)
	o.HostProbe = prev.Collector == "probe" && settings.CollectMetrics && settings.HostProbe
	state, rt, failures := Transition(srv.ConnectionState, srv.Failures, o, settings)
	h := store.HealthUpdate{ConnectionState: state, RuntimeState: rt, Success: o.Reached, Error: o.Error, Failures: failures}
	if o.HasInfo {
		info := Info{Engine: &o.Info, CheckedAt: time.Now().UTC(), LatencyMS: o.Latency.Milliseconds(), ClockSkewMS: o.SkewMS}
		switch {
		case o.HostProbe:
			info.Collector = "probe"
		case srv.IsLocal && o.Procfs:
			info.Collector = "procfs"
		default:
			info.Collector = "engine"
		}
		if !srv.IsLocal {
			if c := ClientCertSummary(srv.ClientCert); c != nil {
				info.ClientCertExp = &c.NotAfter
			}
		}
		h.Info = info
	}
	if o.Reached {
		h.Capabilities = DeriveCapabilities(srv, o)
	}
	if err := st.RecordServerHealth(ctx, srv.ID, h); err != nil {
		slog.Warn("record server health failed", "server", srv.ID, "error", err)
	}
	if state != srv.ConnectionState {
		level := map[string]string{StateOnline: "info", StateDegraded: "warn", StateOffline: "error"}[state]
		if level == "" {
			level = "info"
		}
		msg := fmt.Sprintf("Connection %s → %s", srv.ConnectionState, state)
		if o.Error != "" && state != StateOnline {
			msg += ": " + o.Error
		}
		st.AddServerEvent(ctx, srv.ID, "connection", level, msg, actor, map[string]any{"from": srv.ConnectionState, "to": state, "failures": failures})
	}
	if rt != srv.RuntimeState && o.Reached {
		level := "info"
		if rt != RuntimeReady {
			level = "error"
		}
		msg := fmt.Sprintf("Docker %s → %s", strings.ReplaceAll(srv.RuntimeState, "_", " "), strings.ReplaceAll(rt, "_", " "))
		if o.Error != "" && rt != RuntimeReady {
			msg += ": " + o.Error
		}
		st.AddServerEvent(ctx, srv.ID, "runtime", level, msg, actor, map[string]any{"from": srv.RuntimeState, "to": rt, "version": o.Info.Version})
	}
	if o.ErrorKind == "identity" && srv.LastError != o.Error {
		st.AddServerEvent(ctx, srv.ID, "security", "error", o.Error, actor, nil)
	}
	return o
}


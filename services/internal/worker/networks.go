package worker

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/store"
)

func envLabels(environmentID string) map[string]string {
	return map[string]string{"ignition.network": "environment", "ignition.environment": environmentID}
}

func sharedLabels(name string) map[string]string {
	return map[string]string{"ignition.network": "shared", "ignition.shared": name}
}

// ensureShared creates a shared network with the subnets, IPv6 and isolation set on the
// Networking page (Docker picks the subnet when none is set).
func (w *Worker) ensureShared(ctx context.Context, name string) error {
	o := runtime.NetworkOptions{}
	if n, err := w.Store.GetSharedNetwork(ctx, name); err == nil {
		o = runtime.NetworkOptions{Subnet: n.Subnet, Gateway: n.Gateway, IPv6: n.IPv6, Subnet6: n.Subnet6, Internal: n.Internal}
	}
	return w.Runtime.EnsureManagedNetworkWith(ctx, runtime.SharedNetwork(name), sharedLabels(name), o)
}

// proxyContainer finds the domain proxy of this Ignition install (same compose project as the worker).
func (w *Worker) proxyContainer(ctx context.Context) string {
	project := w.Runtime.Instance(ctx)
	if project == "" {
		return ""
	}
	id, _ := w.Runtime.FindContainer(ctx, map[string]string{"com.docker.compose.project": project, "com.docker.compose.service": "proxy"})
	return id
}

// ReconcileNetworks makes Docker match Ignition's network model without restarting anything:
//   - every running resource is on its environment's private network (answering to its name and aliases)
//     and on exactly the shared networks it joined;
//   - containers are taken off the legacy all-in-one "ignition" network once they're on their private one;
//   - the domain proxy is on every private network, so it can reach every app;
//   - networks nothing uses any more are removed.
func (w *Worker) ReconcileNetworks(ctx context.Context) {
	envs, err := w.Store.EnvironmentIDs(ctx)
	if err != nil {
		return
	}
	proxy := w.proxyContainer(ctx)
	self := w.selfContainer(ctx) // the worker joins too, for health checks
	for _, env := range envs {
		name := runtime.EnvNetwork(env)
		if err := w.Runtime.EnsureManagedNetwork(ctx, name, envLabels(env)); err != nil {
			slog.Warn("network reconcile", "network", name, "error", err)
			continue
		}
		if proxy != "" {
			w.Runtime.ConnectNetwork(ctx, name, proxy, nil)
		}
		if self != "" {
			w.Runtime.ConnectNetwork(ctx, name, self, nil)
		}
	}

	targets, err := w.Store.NetworkTargets(ctx)
	if err != nil {
		return
	}
	inUse := map[string]bool{}
	for _, t := range targets {
		st, err := w.Runtime.Inspect(ctx, t.ContainerID)
		if err != nil || !st.Exists {
			continue
		}
		attached := map[string]bool{}
		for _, a := range st.Networks {
			attached[a.Network] = true
		}
		want := []string{runtime.EnvNetwork(t.EnvironmentID)}
		for _, n := range t.Shared {
			sn := runtime.SharedNetwork(n)
			want = append(want, sn)
			inUse[sn] = true
			w.ensureShared(ctx, n)
		}
		var addrs map[string]store.NetAddress
		for _, n := range want {
			if !attached[n] {
				if addrs == nil {
					addrs, _ = w.Store.NetworkAddresses(ctx, t.ResourceID)
				}
				a := addrs[strings.TrimPrefix(n, "ign-net-")]
				if err := w.Runtime.ConnectNetworkAt(ctx, n, t.ContainerID, t.Aliases, runtime.Address{IPv4: a.IPv4, IPv6: a.IPv6}); err != nil {
					slog.Warn("attach network", "network", n, "resource", t.ResourceID, "error", err)
					continue
				}
				attached[n] = true
				slog.Info("attached network", "network", n, "resource", t.ResourceID)
			}
		}
		for n := range attached {
			legacy := n == LegacyNetwork && attached[want[0]]
			leftShared := strings.HasPrefix(n, "ign-net-") && !slices.Contains(want, n)
			if legacy || leftShared {
				if err := w.Runtime.DisconnectNetwork(ctx, n, t.ContainerID); err == nil {
					slog.Info("detached network", "network", n, "resource", t.ResourceID)
				}
			}
		}
	}

	// Probe (diagnostics) and backup helpers remove themselves; this catches any left by a Docker hiccup.
	if stale, err := w.Runtime.StaleHelpers(ctx, time.Now().Add(-10*time.Minute)); err == nil {
		for _, id := range stale {
			w.Runtime.Remove(ctx, id, false)
		}
	}

	// Clean up networks that no longer belong to anything.
	nets, err := w.Runtime.Networks(ctx)
	if err != nil {
		return
	}
	for _, n := range nets {
		if n.Labels["ignition.instance"] != w.Runtime.Instance(ctx) {
			continue // another Ignition install's network
		}
		kind := n.Labels["ignition.network"]
		stale := (kind == "shared" && !inUse[n.Name]) || (kind == "environment" && !slices.Contains(envs, n.Labels["ignition.environment"]))
		if !stale {
			continue
		}
		if proxy != "" {
			w.Runtime.DisconnectNetwork(ctx, n.Name, proxy)
		}
		if self != "" {
			w.Runtime.DisconnectNetwork(ctx, n.Name, self)
		}
		if err := w.Runtime.RemoveNetwork(ctx, n.Name); err == nil {
			slog.Info("removed unused network", "network", n.Name)
		}
	}
}

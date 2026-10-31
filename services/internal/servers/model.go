// Package servers is the control plane for the Docker hosts Ignition manages.
//
// Status model. A server has three independent dimensions, never folded into one string:
//
//	Connection   pending → online ⇄ degraded → offline, plus unknown when the last check is stale.
//	             pending:  registered but never reached (enrollment not finished).
//	             online:   the last check reached the host and its engine is usable.
//	             degraded: reached, but the engine isn't ready; or a check failed after the server
//	                       was online and the failure threshold isn't met yet (intermittent).
//	             offline:  OfflineAfterFailures consecutive checks failed, or credentials were revoked.
//	             unknown:  derived, never stored — no check for StaleAfter (the worker isn't reporting).
//	             One successful check returns a server to online.
//	Runtime      ready | not_installed | unavailable | incompatible | unknown (Docker Engine on the host).
//	Operations   deployments enabled/disabled, maintenance, removing — set by operators, never by checks.
//
// A server is *available* when its effective connection is online or degraded and its runtime is
// ready. It's *eligible for deployments* when it's the local server (the deploy engine only targets
// the local Docker host), deployments are enabled, and it's neither in maintenance nor being removed.
package servers

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/store"
)

const (
	StatePending    = "pending"
	StateConnecting = "connecting"
	StateOnline     = "online"
	StateDegraded   = "degraded"
	StateOffline    = "offline"
	StateUnknown    = "unknown"

	RuntimeReady        = "ready"
	RuntimeNotInstalled = "not_installed"
	RuntimeUnavailable  = "unavailable"
	RuntimeIncompatible = "incompatible"
	RuntimeUnknown      = "unknown"

	MethodLocal     = "local"
	MethodDockerTLS = "docker-tls"

	// CheckInterval is how often the worker checks every server.
	CheckInterval = 15 * time.Second
)

// Settings are per-server knobs. Each one changes behaviour; nothing here is decorative.
type Settings struct {
	ConnectTimeoutSeconds int    `json:"connectTimeoutSeconds"` // dial + TLS handshake + API call budget
	OfflineAfterFailures  int    `json:"offlineAfterFailures"`  // consecutive failed checks before "offline"
	CollectMetrics        bool   `json:"collectMetrics"`        // host samples every 15 s
	HostProbe             bool   `json:"hostProbe"`             // run the host probe container for host-wide figures
	DiagnosticOutput      string `json:"diagnosticOutput"`      // summary | verbose: how much raw output checks keep
}

func DefaultSettings() Settings {
	return Settings{ConnectTimeoutSeconds: 5, OfflineAfterFailures: 3, CollectMetrics: true, HostProbe: true, DiagnosticOutput: "summary"}
}

func ParseSettings(raw json.RawMessage) Settings {
	s := DefaultSettings()
	if len(raw) > 0 {
		json.Unmarshal(raw, &s)
	}
	if s.ConnectTimeoutSeconds < 1 || s.ConnectTimeoutSeconds > 60 {
		s.ConnectTimeoutSeconds = 5
	}
	if s.OfflineAfterFailures < 1 || s.OfflineAfterFailures > 10 {
		s.OfflineAfterFailures = 3
	}
	if s.DiagnosticOutput != "verbose" {
		s.DiagnosticOutput = "summary"
	}
	return s
}

// ValidateSettings returns per-field errors for out-of-range values.
func ValidateSettings(s Settings) map[string]string {
	errs := map[string]string{}
	if s.ConnectTimeoutSeconds < 1 || s.ConnectTimeoutSeconds > 60 {
		errs["connectTimeoutSeconds"] = "Between 1 and 60 seconds."
	}
	if s.OfflineAfterFailures < 1 || s.OfflineAfterFailures > 10 {
		errs["offlineAfterFailures"] = "Between 1 and 10 checks."
	}
	if s.DiagnosticOutput != "summary" && s.DiagnosticOutput != "verbose" {
		errs["diagnosticOutput"] = "Summary or verbose."
	}
	return errs
}

func (s Settings) Timeout() time.Duration {
	return time.Duration(s.ConnectTimeoutSeconds) * time.Second
}

// Info is what Ignition knows about the host, refreshed on every successful check.
type Info struct {
	Engine        *runtime.EngineInfo `json:"engine"`
	Collector     string              `json:"collector"` // probe | procfs | engine: where host metrics come from
	CheckedAt     time.Time           `json:"checkedAt"`
	LatencyMS     int64               `json:"latencyMs"`
	ClockSkewMS   *int64              `json:"clockSkewMs"` // engine clock minus Ignition's, when measurable
	ClientCertExp *time.Time          `json:"clientCertExpires,omitempty"`
}

// Capability says whether a feature works on this server and, if not, why.
type Capability struct {
	Available bool   `json:"available"`
	Detail    string `json:"detail"`
}

// Capabilities are verified by the last successful check, not assumed from the connection method.
type Capabilities map[string]Capability

// CapabilityKeys in display order.
var CapabilityKeys = []string{"docker", "inspect", "logs", "metrics", "storage", "networkDiagnostics", "maintenance", "deploy"}

func DeriveCapabilities(srv *store.Server, o Observation) Capabilities {
	c := Capabilities{}
	ready := o.Reached && o.Runtime == RuntimeReady
	no := func(why string) Capability { return Capability{false, why} }
	if ready {
		c["docker"] = Capability{true, fmt.Sprintf("Docker %s, API %s", o.Info.Version, o.Info.APIVersion)}
		c["inspect"] = Capability{true, "Containers, images, networks and volumes through the Engine API."}
		c["logs"] = Capability{true, "Container logs through the Engine API. Docker daemon logs aren't exposed by the API."}
		c["storage"] = Capability{true, "Volume and image sizes from Docker's disk usage report."}
	} else {
		why := "The Docker Engine isn't reachable."
		for _, k := range []string{"docker", "inspect", "logs", "storage"} {
			c[k] = no(why)
		}
	}
	switch {
	case !ready:
		c["metrics"] = no("No engine to sample.")
	case o.HostProbe:
		c["metrics"] = Capability{true, "Host CPU, load, memory, swap, disk, network and uptime from the host probe container, plus workload usage."}
	case srv.IsLocal && o.Procfs:
		c["metrics"] = Capability{true, "Host CPU, load, memory, swap, disk and uptime from /proc, plus workload usage. Network needs the host probe."}
	case srv.IsLocal:
		c["metrics"] = Capability{true, "Engine capacity and workload usage only: /proc isn't readable here and the host probe isn't running."}
	default:
		c["metrics"] = Capability{true, "Engine capacity and container counts only. Turn on the host probe in Settings for host CPU, memory, disk and network."}
	}
	if srv.IsLocal && ready {
		c["networkDiagnostics"] = Capability{true, "DNS and TCP checks from a short-lived probe container on Ignition's networks."}
	} else if ready {
		c["networkDiagnostics"] = Capability{true, "DNS and TCP checks from Ignition to the server's engine endpoint."}
	} else {
		c["networkDiagnostics"] = no("The server isn't reachable.")
	}
	c["maintenance"] = Capability{true, "Pause new deployments and scheduled server tasks. Workloads keep running; Ignition doesn't drain or reboot hosts."}
	if srv.IsLocal {
		c["deploy"] = Capability{ready, map[bool]string{true: "The deploy engine runs workloads here.", false: "Deployments need a working Docker Engine."}[ready]}
	} else {
		c["deploy"] = no("The deploy engine only targets the local Docker host. Remote servers can be monitored, inspected and cleaned up.")
	}
	return c
}

// Transition computes the next stored state from one observation.
func Transition(prevState string, prevFailures int, o Observation, s Settings) (state, runtimeState string, failures int) {
	if o.Reached {
		if o.Runtime == RuntimeReady {
			return StateOnline, o.Runtime, 0
		}
		return StateDegraded, o.Runtime, 0
	}
	failures = prevFailures + 1
	runtimeState = RuntimeUnknown
	if o.Runtime != "" {
		runtimeState = o.Runtime
	}
	switch {
	case prevState == StatePending:
		return StatePending, runtimeState, failures
	case failures >= s.OfflineAfterFailures:
		return StateOffline, runtimeState, failures
	case prevState == StateOnline || prevState == StateDegraded:
		return StateDegraded, runtimeState, failures
	}
	return StateOffline, runtimeState, failures
}

// Eligibility explains whether new deployments may target the server.
type Eligibility struct {
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason"`
}

func DeploymentEligibility(srv *store.Server) Eligibility {
	switch {
	case !srv.IsLocal:
		return Eligibility{false, "Remote servers can't receive deployments: the deploy engine only targets the local Docker host."}
	case srv.Removing:
		return Eligibility{false, "The server is being removed."}
	case srv.Maintenance:
		return Eligibility{false, "Maintenance mode pauses new deployments."}
	case !srv.DeploymentsEnabled:
		return Eligibility{false, "New deployments are disabled for this server."}
	case srv.RuntimeState != RuntimeReady:
		return Eligibility{true, "Deployments are allowed, but they'll fail until Docker is ready."}
	}
	return Eligibility{true, "New deployments run here."}
}

// Warning is something an operator should know about, with what to do next.
type Warning struct {
	Level string `json:"level"` // warning | danger | info
	Code  string `json:"code"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

func Warnings(srv *store.Server, info Info) []Warning {
	var out []Warning
	switch srv.EffectiveState {
	case StateOffline:
		text := "Ignition can't reach this server, so figures shown are from the last successful contact."
		if srv.CredentialsRevokedAt != nil {
			text = "Its credentials were revoked. Rotate in new credentials from Access & Security to reconnect."
		} else if srv.LastError != "" {
			text = srv.LastError + " Open Diagnostics to find out why."
		}
		out = append(out, Warning{"danger", "offline", "Server offline", text})
	case StateUnknown:
		out = append(out, Warning{"warning", "stale", "Status is stale", "No check has completed for over a minute. The Ignition worker may be stopped; its last result can't be trusted."})
	case StatePending:
		out = append(out, Warning{"info", "pending", "Enrollment not finished", "The server hasn't been reached yet."})
	case StateDegraded:
		if srv.Failures > 0 {
			out = append(out, Warning{"warning", "intermittent", "Connection failing", fmt.Sprintf("%d check(s) in a row failed: %s", srv.Failures, srv.LastError)})
		}
	}
	if srv.EffectiveState != StateOffline && srv.EffectiveState != StatePending {
		switch srv.RuntimeState {
		case RuntimeNotInstalled:
			out = append(out, Warning{"danger", "runtime_missing", "Docker isn't installed", "No Docker socket was found. Install Docker Engine and mount its socket into Ignition."})
		case RuntimeUnavailable:
			out = append(out, Warning{"danger", "runtime_unavailable", "Docker is unavailable", "The Docker Engine didn't respond. Check that the daemon is running: systemctl status docker."})
		case RuntimeIncompatible:
			v := ""
			if info.Engine != nil {
				v = " (API " + info.Engine.APIVersion + ")"
			}
			out = append(out, Warning{"danger", "runtime_incompatible", "Docker is too old" + v, "Ignition needs Engine API " + runtime.MinAPIVersion + " or newer (Docker 25+). Upgrade Docker on this host."})
		}
	}
	if srv.Maintenance {
		out = append(out, Warning{"warning", "maintenance", "Maintenance mode", "New deployments and scheduled server tasks are paused. Running workloads are untouched."})
	} else if srv.IsLocal && !srv.DeploymentsEnabled {
		out = append(out, Warning{"warning", "deployments_disabled", "New deployments disabled", "Deploys, redeploys and deploy hooks for resources here are refused until you enable them."})
	}
	if info.ClockSkewMS != nil && abs(*info.ClockSkewMS) > 30_000 {
		out = append(out, Warning{"warning", "clock_skew", "Clock out of sync", fmt.Sprintf("The engine's clock is %ds away from Ignition's. Enable NTP (timedatectl set-ntp true).", *info.ClockSkewMS/1000)})
	}
	if info.ClientCertExp != nil && time.Until(*info.ClientCertExp) < 14*24*time.Hour {
		out = append(out, Warning{"warning", "cert_expiring", "Client certificate expiring", "It expires " + info.ClientCertExp.Format("2 Jan 2006") + ". Rotate credentials before then to avoid losing the connection."})
	}
	if info.Engine != nil {
		for _, w := range info.Engine.Warnings {
			out = append(out, Warning{"info", "engine", "Docker reports", w})
		}
	}
	return out
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

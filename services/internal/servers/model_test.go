package servers

import (
	"net/netip"
	"testing"

	"github.com/notsliver/Ignition/services/internal/store"
)

func TestTransition(t *testing.T) {
	s := DefaultSettings() // offline after 3 failures
	ok := Observation{Reached: true, Runtime: RuntimeReady}
	down := Observation{}
	noDocker := Observation{Reached: true, Runtime: RuntimeUnavailable}

	steps := []struct {
		prev     string
		failures int
		o        Observation
		want     string
		wantF    int
	}{
		{StatePending, 0, ok, StateOnline, 0},
		{StatePending, 0, down, StatePending, 1}, // never reached: still enrolling, not "offline"
		{StateOnline, 0, down, StateDegraded, 1}, // one blip doesn't take a server offline
		{StateDegraded, 1, down, StateDegraded, 2},
		{StateDegraded, 2, down, StateOffline, 3},
		{StateOffline, 3, ok, StateOnline, 0}, // one success recovers
		{StateOnline, 0, noDocker, StateDegraded, 0},
		{StateOffline, 5, noDocker, StateDegraded, 0},
	}
	for i, st := range steps {
		got, _, f := Transition(st.prev, st.failures, st.o, s)
		if got != st.want || f != st.wantF {
			t.Errorf("step %d: %s+%v = %s/%d, want %s/%d", i, st.prev, st.o.Reached, got, f, st.want, st.wantF)
		}
	}
}

func TestEligibility(t *testing.T) {
	local := &store.Server{IsLocal: true, DeploymentsEnabled: true, RuntimeState: RuntimeReady}
	if !DeploymentEligibility(local).Eligible {
		t.Fatal("healthy local server should be eligible")
	}
	m := *local
	m.Maintenance = true
	if DeploymentEligibility(&m).Eligible {
		t.Fatal("maintenance must block deployments")
	}
	remote := &store.Server{DeploymentsEnabled: true, RuntimeState: RuntimeReady}
	if DeploymentEligibility(remote).Eligible {
		t.Fatal("remote servers can't receive deployments")
	}
}

func TestParseEndpoint(t *testing.T) {
	good := map[string]string{"docker1.example.com:2376": "docker1.example.com", "10.0.0.5": "10.0.0.5", "tcp://Host.Lan:2376": "host.lan"}
	for in, host := range good {
		h, _, err := ParseEndpoint(in)
		if err != nil || h != host {
			t.Errorf("ParseEndpoint(%q) = %q, %v", in, h, err)
		}
	}
	for _, bad := range []string{"", "127.0.0.1:2376", "[::1]:2376", "169.254.169.254:80", "0.0.0.0:2376", "host:99999", "host/path:2376", "-bad-.com:2376", "user@host:2376"} {
		if _, _, err := ParseEndpoint(bad); err == nil {
			t.Errorf("ParseEndpoint(%q) should fail", bad)
		}
	}
	lookup := func(string) ([]netip.Addr, error) { return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil }
	if ResolveAllowed("sneaky.example.com", lookup) == nil {
		t.Error("a hostname resolving to loopback must be refused")
	}
}

func TestSettingsDefaults(t *testing.T) {
	s := ParseSettings([]byte(`{"connectTimeoutSeconds": 999, "diagnosticOutput": "loud"}`))
	if s.ConnectTimeoutSeconds != 5 || s.DiagnosticOutput != "summary" || !s.CollectMetrics || s.OfflineAfterFailures != 3 {
		t.Fatalf("got %+v", s)
	}
}

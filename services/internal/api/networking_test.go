package api

import (
	"testing"

	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/store"
)

func TestSummarizeDNS(t *testing.T) {
	out := `== web
Server:		127.0.0.11
Address:	127.0.0.11:53

Non-authoritative answer:
Name:	web
Address: 172.20.0.5

== cache
Server:		127.0.0.11
Address:	127.0.0.11:53

** server can't find cache: NXDOMAIN
!! cache`
	ok, summary := summarizeDNS(out, []string{"web", "cache"})
	if ok || summary != "web → 172.20.0.5 · cache doesn't resolve" {
		t.Fatalf("got %v %q", ok, summary)
	}
}

func TestDriftSeesUnappliedChanges(t *testing.T) {
	port := 8080
	res := &store.Resource{Name: "web", ContainerID: "abc", EnvironmentID: "0d3c9a5e-1111-2222-3333-444455556666"}
	cfg := store.Networking{Ports: []store.PortMapping{{HostPort: &port, ContainerPort: 3000, Protocol: "tcp", BindAddress: "127.0.0.1"}}, Aliases: []string{"api"}}
	live := liveNetworking{
		Networks: []runtime.Attachment{{Network: "ign-env-0d3c9a5e1111", Aliases: []string{"web"}}, {Network: "ignition"}},
		Ports:    []runtime.PortMapping{{HostIP: "0.0.0.0", HostPort: 55001, ContainerPort: 3000, Protocol: "tcp"}},
	}
	got := drift(res, cfg, live)
	want := []string{"Host port 55001 → 3000/tcp is still published", "Host port 8080 → 3000/tcp isn't published yet", `web doesn't answer to api on the private network yet`}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %q, want %q", got[i], want[i])
		}
	}
}

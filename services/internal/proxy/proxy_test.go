package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/notsliver/Ignition/services/internal/store"
)

func TestBuildRoutesEachDomain(t *testing.T) {
	c := New("/run/x/admin.sock", "proxy", 8081, 8444)
	raw, hash := c.Build([]store.ProxyRoute{
		{Hostname: "shop.localhost", HTTPS: false, Token: "tok1", Port: 3000, ContainerID: "0123456789abcdef0123", Resource: "shop"},
		{Hostname: "app.example.com", HTTPS: true, Token: "tok2", Port: 80, Resource: "app"},
	})
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{
		`"dial":"0123456789ab:3000"`,     // short container ID, the domain's port
		`"body":"tok1"`, `"body":"tok2"`, // ownership tokens
		`"skip":["shop.localhost"]`,                          // no certificate for an HTTP-only domain
		`https://{http.request.host}:8444{http.request.uri}`, // redirect keeps a non-standard HTTPS port
		`app isn’t running right now`,
		`fill=\"currentColor\"`,
		`"listen":"unix//run/x/admin.sock"`,
		`"@id":"ignition-` + hash + `"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("config is missing %s\n%s", want, s)
		}
	}
	if _, again := c.Build([]store.ProxyRoute{{Hostname: "other.localhost", Token: "t", Port: 1}}); again == hash {
		t.Fatal("different routes must hash differently")
	}
}

func TestLocalNamesNeedNoDNS(t *testing.T) {
	if !IsLocal("app.localhost") || IsLocal("localhost.example.com") {
		t.Fatal("only names under .localhost are local")
	}
}

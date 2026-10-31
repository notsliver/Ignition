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
	}, Settings{})
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
	if _, again := c.Build([]store.ProxyRoute{{Hostname: "other.localhost", Token: "t", Port: 1}}, Settings{}); again == hash {
		t.Fatal("different routes must hash differently")
	}
}

func TestLocalNamesNeedNoDNS(t *testing.T) {
	if !IsLocal("app.localhost") || IsLocal("localhost.example.com") {
		t.Fatal("only names under .localhost are local")
	}
}

func TestBuildRouting(t *testing.T) {
	c := New("/s", "proxy", 80, 443)
	raw, _ := c.Build([]store.ProxyRoute{
		{Hostname: "shop.example.com", HTTPS: true, Token: "tok", Port: 8080, ContainerID: "aaaaaaaaaaaaaaaa", Resource: "api", PathPrefix: "/api", StripPrefix: true, AllowIPs: []string{"10.0.0.0/8"}},
		{Hostname: "shop.example.com", HTTPS: true, Token: "tok", Port: 3000, ContainerID: "bbbbbbbbbbbbbbbb", Resource: "web"},
		{Hostname: "www.example.com", HTTPS: true, Token: "t2", Resource: "web", RedirectTo: "https://shop.example.com{path}", RedirectCode: 301},
	}, Settings{Compression: true, HSTS: true, MaxBodyMB: 10, ACMEEmail: "ops@example.com", ACMECA: "letsencrypt-staging"})
	s := string(raw)
	for _, want := range []string{
		`"path":["/api","/api/*"]`, `"strip_path_prefix":"/api"`, `"dial":"aaaaaaaaaaaa:8080"`, `"dial":"bbbbbbbbbbbb:3000"`,
		`"remote_ip":{"ranges":["10.0.0.0/8"]}`, `"Location":["https://shop.example.com{http.request.uri}"]`, `"status_code":301`,
		`"handler":"encode"`, `"Strict-Transport-Security"`, `"max_size":10485760`,
		`"email":"ops@example.com"`, `acme-staging-v02`, `"subjects":["shop.example.com","www.example.com"]`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("config is missing %s", want)
		}
	}
	if strings.Count(s, `"body":"tok"`) != 1 {
		t.Error("one ownership check per hostname")
	}
	if strings.Index(s, `"/api/*"`) > strings.Index(s, `"dial":"bbbbbbbbbbbb:3000"`) {
		t.Error("the path route must come before the whole-site route")
	}
}

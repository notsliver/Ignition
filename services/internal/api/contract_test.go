package api

import (
	"gopkg.in/yaml.v3"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestRoutesMatchContract fails when a route is added to server.go without documenting it in
// contracts/openapi.yaml (or documented without existing), so the API and contract can't drift.
func TestRoutesMatchContract(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile("../../../contracts/openapi.yaml")
	if err != nil {
		t.Skip("contract not found (running outside the repository)")
	}
	routeRE := regexp.MustCompile(`(?:h|authed)\("(GET|POST|PUT|PATCH|DELETE) (/api/[^"]+)"`)
	routes := map[string]bool{}
	for _, m := range routeRE.FindAllStringSubmatch(string(src), -1) {
		routes[strings.ToLower(m[1])+" "+m[2]] = true
		if strings.HasPrefix(m[0], "authed(") && teamRoute(m[1]+" "+m[2]) {
			routes[strings.ToLower(m[1])+" "+strings.Replace(m[2], "/api/", "/api/teams/{teamId}/", 1)] = true
		}
	}
	if len(routes) < 20 {
		t.Fatalf("found only %d routes; did the registration style change?", len(routes))
	}

	var contract struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(spec, &contract); err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for path, operations := range contract.Paths {
		for method := range operations {
			switch method {
			case "get", "post", "put", "patch", "delete":
				documented[method+" "+path] = true
			}
		}
	}

	for r := range routes {
		if !documented[r] {
			t.Errorf("route %q is not documented in contracts/openapi.yaml", r)
		}
	}
	for d := range documented {
		if !routes[d] {
			t.Errorf("contract documents %q but the API doesn't serve it", d)
		}
	}
}

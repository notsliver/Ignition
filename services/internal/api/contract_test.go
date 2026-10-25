package api

import (
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
	}
	if len(routes) < 20 {
		t.Fatalf("found only %d routes; did the registration style change?", len(routes))
	}

	documented := map[string]bool{}
	var path string
	for _, line := range strings.Split(string(spec), "\n") {
		if m := regexp.MustCompile(`^  (/api/\S+):\s*$`).FindStringSubmatch(line); m != nil {
			path = m[1]
			continue
		}
		if m := regexp.MustCompile(`^    (get|post|put|patch|delete):`).FindStringSubmatch(line); m != nil && path != "" {
			documented[m[1]+" "+path] = true
		}
		if !strings.HasPrefix(line, "  ") && strings.TrimSpace(line) != "" {
			path = ""
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

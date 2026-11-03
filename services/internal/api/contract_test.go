package api

import (
	"encoding/json"
	"gopkg.in/yaml.v3"
	"os"
	"reflect"
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
	for _, route := range publicAdminRoutes {
		method, path, _ := strings.Cut(route.Pattern, " ")
		routes[strings.ToLower(method)+" /api/admin/v1"+path] = true
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

func TestPublicAdminContractMatchesEmbeddedReference(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/public-admin.openapi.yaml")
	if err != nil {
		t.Skip("public contract not found")
	}
	var fromYAML map[string]any
	if err = yaml.Unmarshal(raw, &fromYAML); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(fromYAML)
	if err != nil {
		t.Fatal(err)
	}
	var expected, embedded map[string]any
	json.Unmarshal(canonical, &expected)
	if err = json.Unmarshal(publicOpenAPI, &embedded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expected, embedded) {
		t.Fatal("run python scripts/generate-public-admin.py to refresh embedded OpenAPI")
	}
	combinedRaw, e := os.ReadFile("../../../contracts/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	var combined map[string]any
	if e = yaml.Unmarshal(combinedRaw, &combined); e != nil {
		t.Fatal(e)
	}
	combinedPaths := combined["paths"].(map[string]any)
	for _, route := range publicAdminRoutes {
		method, path, _ := strings.Cut(route.Pattern, " ")
		op := combinedPaths["/api/admin/v1"+path].(map[string]any)[strings.ToLower(method)].(map[string]any)
		security, ok := op["security"].([]any)
		if !ok || len(security) != 1 {
			t.Fatal("public operation inherited browser authentication", route.Pattern)
		}
		if _, ok = security[0].(map[string]any)["adminToken"]; !ok {
			t.Fatal("missing admin token authentication", route.Pattern)
		}
	}
	paths := embedded["paths"].(map[string]any)
	count := 0
	for _, methods := range paths {
		count += len(methods.(map[string]any))
	}
	if count != len(publicAdminRoutes) {
		t.Fatalf("contract has %d operations, registry has %d", count, len(publicAdminRoutes))
	}
	for _, route := range publicAdminRoutes {
		method, path, _ := strings.Cut(route.Pattern, " ")
		item, ok := paths[path].(map[string]any)
		if !ok {
			t.Fatal("missing", route.Pattern)
		}
		op, ok := item[strings.ToLower(method)].(map[string]any)
		if !ok {
			t.Fatal("missing", route.Pattern)
		}
		want := route.Permission
		if want == "" {
			want = "Valid admin token"
		}
		if op["x-permission"] != want {
			t.Fatal("wrong permission", route.Pattern)
		}
	}
}

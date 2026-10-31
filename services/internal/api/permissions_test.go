package api

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/notsliver/Ignition/services/internal/store"
)

var authedRE = regexp.MustCompile(`authed\("([A-Z]+ /api/[^"]+)", "(\w+)"`)

// Every signed-in route names a permission (or is an account route), and every entry in
// routePerms is a real route, so the table and the router can't drift apart.
func TestEveryRouteHasAPermission(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, m := range authedRE.FindAllStringSubmatch(string(src), -1) {
		seen[m[1]] = true
		_, mapped := routePerms[m[1]]
		if !mapped && !accountRoutes[m[1]] {
			t.Errorf("%s (%s) has no entry in routePerms", m[1], m[2])
		}
		if mapped && accountRoutes[m[1]] {
			t.Errorf("%s is both an account route and mapped", m[1])
		}
	}
	for route, perm := range routePerms {
		if !seen[route] {
			t.Errorf("routePerms lists %s, which isn't registered", route)
		}
		if _, ok := permissionByKey[perm]; !ok {
			t.Errorf("%s needs unknown permission %q", route, perm)
		}
	}
	for route := range accountRoutes {
		if !seen[route] {
			t.Errorf("accountRoutes lists %s, which isn't registered", route)
		}
	}
}

// Role defaults reproduce the old role checks: each route's permission defaults to exactly the
// role it's registered with.
func TestRoleDefaultsMatchRoutes(t *testing.T) {
	src, _ := os.ReadFile("server.go")
	for _, m := range authedRE.FindAllStringSubmatch(string(src), -1) {
		perm, ok := routePerms[m[1]]
		if !ok {
			continue
		}
		if got := permissionByKey[perm].Role; got != m[2] {
			t.Errorf("%s is registered for %s but %q defaults to %s", m[1], m[2], perm, got)
		}
	}
}

func TestCatalogKeysAndGroups(t *testing.T) {
	used := map[string]bool{}
	for _, p := range routePerms {
		used[p] = true
	}
	inHandlers := []string{"resources.privileged", "terminal.root", "variables.reveal", "servers.inspect", "audit.view", "users.invite", "automations.manage", "automations.run", "servers.update"}
	for _, k := range inHandlers {
		used[k] = true
	}
	for _, p := range permissionCatalog {
		area, action, ok := strings.Cut(p.Key, ".")
		if !ok || area == "" || action == "" || p.Group == "" {
			t.Errorf("%q isn't area.action with a group", p.Key)
		}
		if p.Grantable && !used[p.Key] {
			t.Errorf("%s isn't required anywhere", p.Key)
		}
	}
}

func TestCan(t *testing.T) {
	viewer := &store.User{Role: "viewer", Permissions: store.PermOverrides{Grant: []string{"deployments.create"}, Deny: []string{"logs.view"}}}
	admin := &store.User{Role: "admin", Permissions: store.PermOverrides{Deny: []string{"variables.reveal"}}}
	owner := &store.User{Role: "owner", Permissions: store.PermOverrides{Deny: []string{"owners.manage"}}}
	cases := []struct {
		u    *store.User
		perm string
		want bool
	}{
		{viewer, "projects.view", true}, {viewer, "logs.view", false}, {viewer, "deployments.create", true}, {viewer, "deployments.cancel", false},
		{admin, "variables.reveal", false}, {admin, "users.delete", true}, {admin, "owners.manage", false},
		{owner, "owners.manage", true}, // not grantable, so a denial doesn't apply
		{viewer, "", true}, {viewer, "role:developer", false}, {admin, "role:admin", true}, {viewer, "no.such", false},
	}
	for _, c := range cases {
		if got := allowed(c.u, c.perm); got != c.want {
			t.Errorf("%s %q: got %v", c.u.Role, c.perm, got)
		}
	}
}

package api

import (
	"slices"
	"strings"

	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Granular permissions, named area.action (projects.view, resources.delete, servers.cleanup...).
 * A role gives a default set; each user can have permissions granted on top or denied. Every
 * route names the permission it needs in routePerms (a few account routes need none), so the API
 * enforces the same table the Users page shows. Role defaults reproduce the old role checks.
 */

type permission struct {
	Key         string `json:"key"`
	Group       string `json:"group"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Role        string `json:"role"` // the lowest role that has it by default
	Grantable   bool   `json:"grantable"`
}

func perm(group, key, label, desc, role string) permission {
	return permission{key, group, label, desc, role, true}
}

var permissionCatalog = []permission{
	perm("Projects", "projects.view", "View projects", "See projects, their environments and the dashboard", "viewer"),
	perm("Projects", "projects.create", "Create projects", "Create, import and duplicate projects", "admin"),
	perm("Projects", "projects.update", "Update projects", "Rename projects and change their settings", "admin"),
	perm("Projects", "projects.delete", "Delete projects", "Delete projects with everything in them", "admin"),

	perm("Resources", "resources.view", "View resources", "See resources, their settings, metrics and imports", "viewer"),
	perm("Resources", "resources.create", "Create resources", "Add applications, databases, workers, functions and external databases", "developer"),
	perm("Resources", "resources.update", "Update resources", "Change source, build, limits, networking, health checks and advanced settings", "developer"),
	perm("Resources", "resources.control", "Start, stop and restart", "Control running resources, run diagnostics and invoke functions", "developer"),
	perm("Resources", "resources.delete", "Delete resources", "Delete resources, purge old images and move resources between projects", "admin"),
	perm("Resources", "resources.privileged", "Privileged settings", "Privileged mode and connectivity tests to custom hosts", "admin"),

	perm("Deployments", "deployments.view", "View deployments", "See deployments, releases and what changed between them", "viewer"),
	perm("Deployments", "deployments.create", "Deploy", "Start deployments, roll back and roll forward", "developer"),
	perm("Deployments", "deployments.cancel", "Cancel deployments", "Stop a deployment in progress", "developer"),

	perm("Variables & secrets", "variables.view", "View variables", "See variable names and non-secret values", "viewer"),
	perm("Variables & secrets", "variables.update", "Edit variables", "Add, change and remove variables", "developer"),
	perm("Variables & secrets", "variables.reveal", "Reveal secrets", "Show secret values and database passwords", "admin"),

	perm("Logs & terminal", "logs.view", "View logs", "Read runtime and build logs", "viewer"),
	perm("Logs & terminal", "terminal.open", "Open a terminal", "A shell inside a running container, as the image's user", "developer"),
	perm("Logs & terminal", "terminal.root", "Root terminal", "Open a shell as root", "admin"),

	perm("Domains & storage", "domains.view", "View domains", "See domains and check their DNS and certificates", "viewer"),
	perm("Domains & storage", "domains.manage", "Manage domains", "Add, change and remove domains", "developer"),
	perm("Domains & storage", "storage.view", "View storage", "See volumes and their sizes", "viewer"),
	perm("Domains & storage", "storage.update", "Change storage", "Add and change volume mounts", "developer"),
	perm("Domains & storage", "storage.backup", "Download backups", "Download volume archives", "admin"),
	perm("Domains & storage", "storage.delete", "Delete volumes", "Delete volumes with their data", "admin"),

	perm("Databases", "databases.import", "Import data", "Import into databases from files or live servers", "developer"),
	perm("Databases", "databases.connect", "Share connections", "Wire a database into other resources and test connections", "developer"),

	perm("Tasks & access", "tasks.view", "View tasks", "See scheduled tasks and their runs", "viewer"),
	perm("Tasks & access", "tasks.manage", "Manage tasks", "Create, change and delete scheduled tasks", "developer"),
	perm("Tasks & access", "tasks.run", "Run tasks", "Run tasks now and cancel runs", "developer"),
	perm("Tasks & access", "access.view", "View access", "See deploy hooks and API tokens (not their secrets)", "viewer"),
	perm("Tasks & access", "access.manage", "Manage access", "Create, reveal and remove deploy hooks and API tokens", "developer"),

	perm("Automations", "automations.view", "View automations", "See automations and their runs", "viewer"),
	perm("Automations", "automations.manage", "Manage automations", "Create, change and delete automations and their hooks", "developer"),
	perm("Automations", "automations.run", "Run automations", "Run automations and approve, reject or cancel runs", "developer"),

	perm("Sources", "sources.view", "View sources", "See Git providers and registries", "viewer"),
	perm("Sources", "sources.use", "Use sources", "Test sources and browse their repositories", "developer"),
	perm("Sources", "sources.manage", "Manage sources", "Add, change and remove sources and their credentials", "admin"),

	perm("Servers", "servers.view", "View servers", "See servers, their health, metrics, logs and Docker inventory", "viewer"),
	perm("Servers", "servers.operate", "Operate servers", "Test, refresh and diagnose servers; act on Ignition's containers", "developer"),
	perm("Servers", "servers.create", "Add servers", "Register and verify new servers", "admin"),
	perm("Servers", "servers.update", "Update servers", "Settings, deployments, maintenance, address, credentials and bulk actions", "admin"),
	perm("Servers", "servers.delete", "Remove servers", "Remove server registrations", "admin"),
	perm("Servers", "servers.cleanup", "Clean up servers", "Remove unused containers, images, networks and volumes", "admin"),
	perm("Servers", "servers.inspect", "Inspect servers", "Raw Docker inspect, diagnostic bundles and logs of unmanaged containers", "admin"),
	perm("Servers", "servers.tasks", "Server tasks", "Create, change, run and cancel server tasks and jobs", "admin"),

	perm("Networking", "network.view", "View networking", "See addresses, ports, internal networks, the proxy, DNS providers and firewall rules", "viewer"),
	perm("Networking", "network.diagnose", "Run network tests", "DNS, TCP, HTTP, ping and traceroute from a server", "developer"),
	perm("Networking", "network.manage", "Manage networking", "Public addresses, port range and reservations, internal networks and subnets", "admin"),
	perm("Networking", "proxy.manage", "Manage the proxy", "Proxy settings, certificates, reloads and proxy logs", "admin"),
	perm("Networking", "dns.manage", "Manage DNS providers", "Connect and remove DNS provider integrations", "admin"),
	perm("Networking", "firewall.manage", "Manage firewall rules", "Change and apply host firewall rules for published ports", "admin"),

	perm("Administration", "settings.view", "View settings", "General and update settings", "admin"),
	perm("Administration", "settings.update", "Change settings", "General settings, update checks and applying updates", "admin"),
	perm("Administration", "security.manage", "Security settings", "Authentication & security policy, single sign-on and signing everyone out", "admin"),
	perm("Administration", "audit.view", "Audit log", "Read and export the audit log", "admin"),

	perm("Users", "users.view", "View users", "See accounts, invitations and sessions", "admin"),
	perm("Users", "users.invite", "Invite users", "Create and revoke invitations", "admin"),
	perm("Users", "users.update", "Change users", "Roles, suspending, password reset links, two-factor resets and ending sessions", "admin"),
	perm("Users", "users.permissions", "Change permissions", "Grant and deny permissions to others", "admin"),
	perm("Users", "users.delete", "Remove users", "Delete accounts", "admin"),
	{"owners.manage", "Users", "Manage owners", "Make someone an owner and manage owners", "owner", false},
}

var permissionByKey = func() map[string]permission {
	m := map[string]permission{}
	for _, p := range permissionCatalog {
		m[p.Key] = p
	}
	return m
}()

// can reports whether the user has a permission: role default, plus grants, minus denies.
// Non-grantable permissions follow the role only.
func can(u *store.User, key string) bool {
	p, ok := permissionByKey[key]
	if !ok {
		return false
	}
	has := roleRank[u.Role] >= roleRank[p.Role]
	if !p.Grantable {
		return has
	}
	if slices.Contains(u.Permissions.Deny, key) {
		return false
	}
	return has || slices.Contains(u.Permissions.Grant, key)
}

func effectivePermissions(u *store.User) []string {
	out := []string{}
	for _, p := range permissionCatalog {
		if can(u, p.Key) {
			out = append(out, p.Key)
		}
	}
	return out
}

// accountRoutes concern only the signed-in person (or are harmless lookups) and need no permission.
var accountRoutes = map[string]bool{
	"GET /api/auth/me": true, "GET /api/account/security": true, "POST /api/account/mfa/setup": true, "POST /api/account/mfa/enable": true,
	"POST /api/account/mfa/disable": true, "POST /api/account/mfa/recovery-codes": true, "GET /api/permissions": true, "GET /api/system": true,
	"PUT /api/projects/{id}/favorite": true, "DELETE /api/projects/{id}/favorite": true, "GET /api/cron/preview": true, "GET /api/databases/engines": true,
	"GET /api/automations/catalog": true,
	"GET /api/account/preferences": true, "PUT /api/account/preferences": true,
	"POST /api/invites": true, // who may invite is a setting; checked in createInvite
}

// routePerms names the permission each route needs. TestEveryRouteHasAPermission keeps it complete.
var routePerms = map[string]string{
	// Projects
	"GET /api/projects":                         "projects.view",
	"GET /api/projects/{id}":                    "projects.view",
	"GET /api/dashboard":                        "projects.view",
	"POST /api/projects":                        "projects.create",
	"POST /api/projects/import/git/inspect":     "projects.create",
	"POST /api/projects/import/compose/preview": "projects.create",
	"POST /api/projects/import":                 "projects.create",
	"POST /api/projects/{id}/duplicate":         "projects.create",
	"PATCH /api/projects/{id}":                  "projects.update",
	"DELETE /api/projects/{id}":                 "projects.delete",

	"GET /api/projects/{id}/deployments":        "deployments.view",
	"GET /api/projects/{id}/metrics":            "resources.view",
	"GET /api/projects/{id}/logs":               "logs.view",
	"GET /api/projects/{id}/activity":           "audit.view",
	"GET /api/projects/{id}/environments":       "projects.view",
	"POST /api/projects/{id}/environments":      "projects.update",
	"PUT /api/projects/{id}/environments/order": "projects.update",
	"PATCH /api/environments/{id}":              "projects.update",
	"DELETE /api/environments/{id}":             "projects.update",

	// Resources
	"GET /api/projects/{id}/resources":               "resources.view",
	"GET /api/resources":                             "resources.view",
	"GET /api/resources/{id}":                        "resources.view",
	"GET /api/resources/{id}/stats":                  "resources.view",
	"GET /api/resources/{id}/metrics":                "resources.view",
	"GET /api/resources/{id}/source":                 "resources.view",
	"GET /api/resources/{id}/limits":                 "resources.view",
	"GET /api/resources/{id}/build":                  "resources.view",
	"GET /api/resources/{id}/networking":             "resources.view",
	"GET /api/resources/{id}/health":                 "resources.view",
	"GET /api/resources/{id}/advanced":               "resources.view",
	"GET /api/resources/{id}/export":                 "resources.view",
	"GET /api/resources/{id}/connection":             "resources.view",
	"GET /api/resources/{id}/function":               "resources.view",
	"GET /api/resources/{id}/imports":                "resources.view",
	"GET /api/data-imports/{id}":                     "resources.view",
	"POST /api/projects/{id}/resources":              "resources.create",
	"POST /api/projects/{id}/external-databases":     "resources.create",
	"PATCH /api/resources/{id}":                      "resources.update",
	"PUT /api/resources/{id}/limits":                 "resources.update",
	"PUT /api/resources/{id}/build":                  "resources.update",
	"POST /api/resources/{id}/build/inspect":         "resources.update",
	"PUT /api/resources/{id}/networking":             "resources.update",
	"POST /api/resources/{id}/networking/port-check": "resources.update",
	"PUT /api/resources/{id}/health":                 "resources.update",
	"POST /api/resources/{id}/health/test":           "resources.update",
	"PUT /api/resources/{id}/advanced":               "resources.update",
	"PUT /api/resources/{id}/function":               "resources.update",
	"POST /api/resources/{id}/actions":               "resources.control",
	"POST /api/resources/{id}/diagnostics":           "resources.control",
	"POST /api/resources/{id}/function/invoke":       "resources.control",
	"DELETE /api/resources/{id}":                     "resources.delete",
	"POST /api/resources/{id}/images/purge":          "resources.delete",
	"POST /api/resources/{id}/move":                  "resources.delete",

	// Deployments
	"GET /api/resources/{id}/deployments":  "deployments.view",
	"GET /api/resources/{id}/releases":     "deployments.view",
	"GET /api/deployments/{id}":            "deployments.view",
	"GET /api/deployments/{id}/env-diff":   "deployments.view",
	"POST /api/resources/{id}/deployments": "deployments.create",
	"POST /api/deployments/{id}/cancel":    "deployments.cancel",

	// Variables
	"GET /api/resources/{id}/env":               "variables.view",
	"PUT /api/resources/{id}/env":               "variables.update",
	"POST /api/resources/{id}/env/{key}/reveal": "variables.reveal",

	// Logs & terminal
	"GET /api/resources/{id}/logs":             "logs.view",
	"GET /api/resources/{id}/logs/deployments": "logs.view",
	"GET /api/deployments/{id}/logs":           "logs.view",
	"GET /api/resources/{id}/terminal":         "terminal.open",

	// Domains & storage
	"GET /api/resources/{id}/domains":                 "domains.view",
	"POST /api/domains/{id}/check":                    "domains.view",
	"POST /api/resources/{id}/domains":                "domains.manage",
	"PATCH /api/domains/{id}":                         "domains.manage",
	"DELETE /api/domains/{id}":                        "domains.manage",
	"GET /api/resources/{id}/storage":                 "storage.view",
	"PUT /api/resources/{id}/storage":                 "storage.update",
	"GET /api/resources/{id}/storage/{volume}/backup": "storage.backup",
	"DELETE /api/resources/{id}/storage/{volume}":     "storage.delete",

	// Databases
	"POST /api/databases/check-source":          "databases.import",
	"POST /api/resources/{id}/imports":          "databases.import",
	"POST /api/data-imports/{id}/cancel":        "databases.import",
	"POST /api/resources/{id}/connection/share": "databases.connect",
	"POST /api/resources/{id}/connection/test":  "databases.connect",

	// Tasks & access
	"GET /api/resources/{id}/tasks":        "tasks.view",
	"GET /api/tasks/{id}/runs":             "tasks.view",
	"GET /api/task-runs/{id}":              "tasks.view",
	"POST /api/resources/{id}/tasks":       "tasks.manage",
	"PATCH /api/tasks/{id}":                "tasks.manage",
	"DELETE /api/tasks/{id}":               "tasks.manage",
	"POST /api/tasks/{id}/run":             "tasks.run",
	"POST /api/task-runs/{id}/cancel":      "tasks.run",
	"GET /api/resources/{id}/access":       "access.view",
	"POST /api/resources/{id}/hook":        "access.manage",
	"POST /api/resources/{id}/hook/reveal": "access.manage",
	"DELETE /api/resources/{id}/hook":      "access.manage",
	"POST /api/resources/{id}/tokens":      "access.manage",
	"DELETE /api/resource-tokens/{id}":     "access.manage",

	// Automations
	"GET /api/automations":                       "automations.view",
	"GET /api/automations/{id}":                  "automations.view",
	"GET /api/automation-runs/{id}":              "automations.view",
	"POST /api/automations/preview":              "automations.view",
	"POST /api/automations":                      "automations.manage",
	"PUT /api/automations/{id}":                  "automations.manage",
	"DELETE /api/automations/{id}":               "automations.manage",
	"POST /api/automations/{id}/hook":            "automations.manage",
	"POST /api/automations/{id}/test-notify":     "automations.manage",
	"POST /api/automations/{id}/run":             "automations.run",
	"POST /api/automation-runs/{id}/approve":     "automations.run",
	"POST /api/automation-runs/{id}/reject":      "automations.run",
	"POST /api/automation-runs/{id}/acknowledge": "automations.run",
	"POST /api/automation-runs/{id}/cancel":      "automations.run",

	// Sources
	"GET /api/sources":                   "sources.view",
	"GET /api/sources/{id}":              "sources.view",
	"POST /api/sources/{id}/test":        "sources.use",
	"GET /api/sources/{id}/repositories": "sources.use",
	"POST /api/sources":                  "sources.manage",
	"PATCH /api/sources/{id}":            "sources.manage",
	"DELETE /api/sources/{id}":           "sources.manage",

	// Servers
	"GET /api/servers":                                       "servers.view",
	"GET /api/servers/capacity":                              "servers.view",
	"GET /api/servers/{id}":                                  "servers.view",
	"GET /api/servers/{id}/maintenance":                      "servers.view",
	"GET /api/servers/{id}/events":                           "servers.view",
	"GET /api/servers/{id}/jobs":                             "servers.view",
	"GET /api/servers/{id}/metrics":                          "servers.view",
	"GET /api/servers/{id}/resources":                        "servers.view",
	"GET /api/servers/{id}/deployments":                      "servers.view",
	"GET /api/servers/{id}/logs":                             "servers.view",
	"GET /api/servers/{id}/diagnostics":                      "servers.view",
	"GET /api/diagnostic-runs/{id}":                          "servers.view",
	"GET /api/servers/{id}/docker":                           "servers.view",
	"GET /api/servers/{id}/docker/containers":                "servers.view",
	"GET /api/servers/{id}/docker/images":                    "servers.view",
	"GET /api/servers/{id}/docker/networks":                  "servers.view",
	"GET /api/servers/{id}/docker/volumes":                   "servers.view",
	"GET /api/servers/{id}/storage":                          "servers.view",
	"GET /api/servers/{id}/networking":                       "servers.view",
	"GET /api/servers/{id}/tasks":                            "servers.view",
	"GET /api/server-tasks/{id}/runs":                        "servers.view",
	"GET /api/server-jobs/{id}":                              "servers.view",
	"GET /api/servers/{id}/access":                           "servers.view",
	"POST /api/servers/{id}/test":                            "servers.operate",
	"POST /api/servers/{id}/refresh":                         "servers.operate",
	"POST /api/servers/{id}/diagnostics":                     "servers.operate",
	"POST /api/servers/{id}/docker/containers/{cid}/actions": "servers.operate",
	"POST /api/servers":                                      "servers.create",
	"POST /api/servers/verify":                               "servers.create",
	"PATCH /api/servers/{id}":                                "servers.update",
	"PUT /api/servers/{id}/deployments":                      "servers.update",
	"POST /api/servers/{id}/maintenance":                     "servers.update",
	"PUT /api/servers/{id}/maintenance/note":                 "servers.update",
	"PUT /api/servers/{id}/credentials":                      "servers.update",
	"DELETE /api/servers/{id}/credentials":                   "servers.update",
	"PUT /api/servers/{id}/endpoint":                         "servers.update",
	"POST /api/servers/bulk":                                 "servers.update",
	"DELETE /api/servers/{id}":                               "servers.delete",
	"GET /api/servers/{id}/removal":                          "servers.delete",
	"POST /api/servers/{id}/cleanup/preview":                 "servers.cleanup",
	"POST /api/servers/{id}/cleanup":                         "servers.cleanup",
	"GET /api/servers/bundle":                                "servers.inspect",
	"GET /api/servers/{id}/diagnostics/bundle":               "servers.inspect",
	"GET /api/servers/{id}/docker/inspect":                   "servers.inspect",
	"POST /api/servers/{id}/tasks":                           "servers.tasks",
	"PATCH /api/server-tasks/{id}":                           "servers.tasks",
	"DELETE /api/server-tasks/{id}":                          "servers.tasks",
	"POST /api/server-tasks/{id}/run":                        "servers.tasks",
	"POST /api/server-jobs/{id}/cancel":                      "servers.tasks",

	// Networking
	"GET /api/network/addresses":                  "network.view",
	"PUT /api/network/settings":                   "network.manage",
	"GET /api/network/ports":                      "network.view",
	"POST /api/network/ports/reservations":        "network.manage",
	"DELETE /api/network/ports/reservations/{id}": "network.manage",
	"GET /api/networks":                           "network.view",
	"POST /api/networks":                          "network.manage",
	"PATCH /api/networks/{name}":                  "network.manage",
	"DELETE /api/networks/{name}":                 "network.manage",
	"GET /api/network/proxy":                      "network.view",
	"PUT /api/network/proxy":                      "proxy.manage",
	"POST /api/network/proxy/reload":              "proxy.manage",
	"GET /api/network/proxy/logs":                 "proxy.manage",
	"POST /api/network/diagnose":                  "network.diagnose",
	"GET /api/domains":                            "domains.view",
	"POST /api/domains/check":                     "domains.view",
	"POST /api/domains/{id}/dns":                  "domains.manage",
	"GET /api/dns-providers":                      "network.view",
	"POST /api/dns-providers":                     "dns.manage",
	"POST /api/dns-providers/{id}/refresh":        "dns.manage",
	"DELETE /api/dns-providers/{id}":              "dns.manage",
	"GET /api/servers/{id}/firewall":              "network.view",
	"PUT /api/servers/{id}/firewall/rules":        "firewall.manage",
	"DELETE /api/firewall-rules/{id}":             "firewall.manage",
	"POST /api/servers/{id}/firewall/apply":       "firewall.manage",

	// Administration
	"GET /api/settings/general":                   "settings.view",
	"GET /api/settings/updates":                   "settings.view",
	"PUT /api/settings/general":                   "settings.update",
	"PUT /api/settings/updates":                   "settings.update",
	"POST /api/settings/updates/check":            "settings.update",
	"POST /api/settings/updates/apply":            "settings.update",
	"POST /api/settings/updates/dismiss":          "settings.update",
	"GET /api/settings/security":                  "security.manage",
	"PUT /api/settings/security":                  "security.manage",
	"POST /api/settings/security/revoke-sessions": "security.manage",
	"PUT /api/settings/security/oidc":             "security.manage",
	"DELETE /api/settings/security/oidc":          "security.manage",
	"POST /api/settings/security/oidc/test":       "security.manage",
	"GET /api/audit":                              "audit.view",
	"GET /api/audit/export":                       "audit.view",

	// Users
	"GET /api/users":                       "users.view",
	"DELETE /api/invites/{id}":             "users.invite",
	"PATCH /api/users/{id}":                "users.update",
	"POST /api/users/{id}/sessions/revoke": "users.update",
	"POST /api/users/{id}/reset-link":      "users.update",
	"DELETE /api/users/{id}/mfa":           "users.update",
	"PUT /api/users/{id}/permissions":      "users.permissions",
	"DELETE /api/users/{id}":               "users.delete",
}

// permissionFor resolves what a route needs. Unlisted routes fall back to their registered role,
// never looser than before.
func permissionFor(pattern, minRole string) string {
	if p, ok := routePerms[pattern]; ok {
		return p
	}
	if accountRoutes[pattern] {
		return ""
	}
	return "role:" + minRole
}

func allowed(u *store.User, perm string) bool {
	if perm == "" {
		return true
	}
	if role, ok := strings.CutPrefix(perm, "role:"); ok {
		return roleRank[u.Role] >= roleRank[role]
	}
	return can(u, perm)
}

func deniedMessage(u *store.User, perm string) string {
	if p, ok := permissionByKey[perm]; ok {
		if slices.Contains(u.Permissions.Deny, perm) {
			return "The “" + p.Label + "” permission (" + p.Key + ") was removed from your account. Ask an admin."
		}
		return "This needs the “" + p.Label + "” permission (" + p.Key + "), which your role (" + u.Role + ") doesn't include. Ask an admin."
	}
	return "Your role (" + u.Role + ") can't do this. Ask an admin."
}

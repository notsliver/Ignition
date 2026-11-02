package api

import "testing"

// "Infrastructure in Instance administration only" must cover server and network administration, but
// leave what teams own or rely on: their sources, domains, shared networks.
func TestInfrastructureRoute(t *testing.T) {
	for pattern, want := range map[string]bool{
		"GET /api/teams/{teamId}/servers":                true,
		"GET /api/teams/{teamId}/servers/{id}":           true,
		"POST /api/teams/{teamId}/server-tasks":          true,
		"GET /api/teams/{teamId}/network/addresses":      true,
		"PUT /api/teams/{teamId}/firewall-rules":         true,
		"POST /api/teams/{teamId}/sources":               false,
		"DELETE /api/teams/{teamId}/sources/{id}":        false,
		"GET /api/teams/{teamId}/sources":                false,
		"GET /api/teams/{teamId}/domains":                false,
		"GET /api/teams/{teamId}/networks":               false,
		"GET /api/teams/{teamId}/resources/{id}/domains": false,
		"POST /api/teams/{teamId}/resources":             false,
	} {
		if got := infrastructureRoute(pattern); got != want {
			t.Errorf("infrastructureRoute(%q) = %v, want %v", pattern, got, want)
		}
	}
}

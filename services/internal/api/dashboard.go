package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/servers"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * The dashboard in one call: counts, what needs attention (each item links to where it's fixed),
 * and the latest deployments across every resource. Server usage charts come from the local
 * server's metrics endpoint.
 */

type dashLink struct {
	Kind       string `json:"kind"` // resource, server
	ProjectID  string `json:"projectId,omitempty"`
	ResourceID string `json:"resourceId,omitempty"`
	ServerID   string `json:"serverId,omitempty"`
	View       string `json:"view"`
}

type dashAlert struct {
	Tone   string   `json:"tone"` // danger, warning
	Title  string   `json:"title"`
	Detail string   `json:"detail"`
	Action string   `json:"action"`
	Link   dashLink `json:"link"`
}

type dashDeployment struct {
	store.Deployment
	ResourceName string `json:"resourceName"`
	ProjectID    string `json:"projectId"`
	ProjectName  string `json:"projectName"`
	Environment  string `json:"environment"`
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	ctx := r.Context()
	var st struct {
		Projects    int `json:"projects"`
		Resources   int `json:"resources"`
		Running     int `json:"running"`
		Failed      int `json:"failed"`
		Deploys24h  int `json:"deployments24h"`
		Failed24h   int `json:"failedDeployments24h"`
		Active      int `json:"activeDeployments"`
		Servers     int `json:"servers"`
		Online      int `json:"serversOnline"`
		Automations int `json:"automations"`
		Approvals   int `json:"approvals"`
	}
	if err := s.Store.DB.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM projects WHERE archived_at IS NULL),
		       (SELECT count(*) FROM resources),
		       (SELECT count(*) FROM resources WHERE status = 'running'),
		       (SELECT count(*) FROM resources WHERE status = 'failed'),
		       (SELECT count(*) FROM deployments WHERE created_at > now() - interval '24 hours'),
		       (SELECT count(*) FROM deployments WHERE created_at > now() - interval '24 hours' AND status = 'failed'),
		       (SELECT count(*) FROM deployments WHERE status IN ('queued', 'building', 'deploying')),
		       (SELECT count(*) FROM automations WHERE enabled),
		       (SELECT count(*) FROM automation_runs WHERE status = 'awaiting_approval')`).
		Scan(&st.Projects, &st.Resources, &st.Running, &st.Failed, &st.Deploys24h, &st.Failed24h, &st.Active, &st.Automations, &st.Approvals); err != nil {
		return err
	}

	alerts := []dashAlert{}
	srvs, _, err := s.Store.ListServers(ctx, store.ServerQuery{Limit: 1000, Sort: "name"})
	if err != nil {
		return err
	}
	var local *store.Server
	st.Servers = len(srvs)
	for i := range srvs {
		sv := &srvs[i]
		if sv.IsLocal {
			local = sv
		}
		link := dashLink{Kind: "server", ServerID: sv.ID, View: "Overview"}
		switch sv.EffectiveState {
		case servers.StateOnline:
			st.Online++
		case servers.StateOffline:
			alerts = append(alerts, dashAlert{"danger", sv.Name + " is offline", firstNonBlank(sv.LastError, "Ignition can't reach its Docker Engine."), "Open server", link})
		case servers.StateDegraded:
			st.Online++
			alerts = append(alerts, dashAlert{"warning", sv.Name + " is degraded", firstNonBlank(sv.LastError, "Docker isn't fully ready."), "Open server", link})
		}
		if m := sv.Metrics; m != nil && m.DiskUsed != nil && m.DiskTotal != nil && *m.DiskTotal > 0 {
			if pct := float64(*m.DiskUsed) / float64(*m.DiskTotal) * 100; pct >= 85 {
				alerts = append(alerts, dashAlert{"warning", fmt.Sprintf("%s disk is %.0f%% full", sv.Name, pct), "Free space before builds and databases start failing.", "Storage", dashLink{Kind: "server", ServerID: sv.ID, View: "Storage"}})
			}
		}
	}

	// Resources that are down or failing their health check.
	rows, err := s.Store.DB.Query(ctx, `
		SELECT r.id, r.name, r.project_id, p.name, r.status, COALESCE(h.state, ''), COALESCE(h.failures, 0),
		       COALESCE((SELECT d.error FROM deployments d WHERE d.resource_id = r.id ORDER BY d.number DESC LIMIT 1), '')
		FROM resources r JOIN projects p ON p.id = r.project_id LEFT JOIN resource_health h ON h.resource_id = r.id AND h.enabled
		WHERE r.status = 'failed' OR (r.status = 'running' AND h.state = 'unhealthy') OR r.status = 'paused'
		ORDER BY r.updated_at DESC LIMIT 10`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, name, pid, pname, status, health, lastErr string
		var failures int
		if rows.Scan(&id, &name, &pid, &pname, &status, &health, &failures, &lastErr) != nil {
			continue
		}
		link := dashLink{Kind: "resource", ProjectID: pid, ResourceID: id, View: "Logs"}
		switch {
		case status == "failed":
			alerts = append(alerts, dashAlert{"danger", name + " isn't running", firstNonBlank(lastErr, "The container stopped unexpectedly.") + " · " + pname, "Logs", link})
		case status == "paused":
			link.View = "Advanced Settings"
			alerts = append(alerts, dashAlert{"warning", name + " is paused", "Its processes are frozen and it doesn't answer requests. · " + pname, "Resume", link})
		default:
			link.View = "Health Checks"
			alerts = append(alerts, dashAlert{"warning", name + " is unhealthy", fmt.Sprintf("%d health checks failed in a row. · %s", failures, pname), "Health", link})
		}
	}
	rows.Close()

	// Automation runs waiting for a decision.
	rows, err = s.Store.DB.Query(ctx, `
		SELECT ar.reason, a.name, a.scope, COALESCE(a.resource_id::text, ''), COALESCE(r.project_id::text, ''), a.targets
		FROM automation_runs ar JOIN automations a ON a.id = ar.automation_id LEFT JOIN resources r ON r.id = a.resource_id
		WHERE ar.status = 'awaiting_approval' ORDER BY ar.created_at DESC LIMIT 3`)
	if err == nil {
		for rows.Next() {
			var reason, name, scope, rid, pid string
			var targets []byte
			if rows.Scan(&reason, &name, &scope, &rid, &pid, &targets) != nil {
				continue
			}
			link := dashLink{Kind: "resource", ProjectID: pid, ResourceID: rid, View: "Automations"}
			if scope == "servers" && local != nil {
				link = dashLink{Kind: "server", ServerID: local.ID, View: "Automations"}
			}
			alerts = append(alerts, dashAlert{"warning", name + " is waiting for approval", reason, "Review", link})
		}
		rows.Close()
	}
	if len(alerts) > 8 {
		alerts = alerts[:8]
	}

	// Latest deployments everywhere.
	deps := []dashDeployment{}
	rows, err = s.Store.DB.Query(ctx, `
		SELECT d.id, r.name, r.project_id, p.name, e.name FROM deployments d
		JOIN resources r ON r.id = d.resource_id JOIN projects p ON p.id = r.project_id JOIN environments e ON e.id = r.environment_id
		ORDER BY d.created_at DESC LIMIT 8`)
	if err != nil {
		return err
	}
	var meta []dashDeployment
	for rows.Next() {
		var d dashDeployment
		if rows.Scan(&d.ID, &d.ResourceName, &d.ProjectID, &d.ProjectName, &d.Environment) == nil {
			meta = append(meta, d)
		}
	}
	rows.Close()
	for _, m := range meta {
		if full, err := s.Store.GetDeployment(ctx, m.ID); err == nil {
			m.Deployment = *full
			deps = append(deps, m)
		}
	}

	out := map[string]any{"stats": st, "alerts": alerts, "deployments": deps, "at": time.Now().UTC()}
	if local != nil {
		out["localServer"] = map[string]any{"id": local.ID, "name": local.Name, "connectionState": local.EffectiveState}
	}
	httpx.JSON(w, 200, out)
	return nil
}

func firstNonBlank(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

func (s *Server) publicResource(r *http.Request, id string) (map[string]any, error) {
	var raw []byte
	err := s.Store.SQL(r.Context()).QueryRow(r.Context(), `SELECT jsonb_build_object(
 'id',coalesce(r.id,sv.original_resource_id),'name',coalesce(r.name,sv.name),'kind',coalesce(p.kind,r.type),
 'teamId',coalesce(r.team_id,sv.team_id),'projectId',r.project_id,'environmentId',r.environment_id,
 'runtimeStatus',coalesce(r.status,'removed'),'managed',sv.id IS NOT NULL,'managementStatus',sv.status,
 'productId',sv.product_id,'planId',sv.plan_id,'suspendedReason',coalesce(sv.suspended_reason,''),'note',coalesce(sv.note,m.note,''),
 'serverId',r.server_id,'cpus',r.cpus,'memoryMb',r.memory_mb,
 'externalId',(SELECT external_id FROM admin_external_ids WHERE kind='resource' AND object_id=coalesce(r.id,sv.original_resource_id)))
 FROM resources r FULL JOIN services sv ON coalesce(sv.original_resource_id,sv.resource_id)=r.id
 LEFT JOIN products p ON p.id=sv.product_id LEFT JOIN admin_resource_metadata m ON m.resource_id=r.id WHERE coalesce(r.id,sv.original_resource_id)=$1`, id).Scan(&raw)
	if err != nil {
		var snapshot []byte
		if e := s.Store.SQL(r.Context()).QueryRow(r.Context(), `SELECT details->'resourceSnapshot' FROM admin_operations WHERE resource_id=$1 AND type='resource.delete' AND status='succeeded' AND details ? 'resourceSnapshot' ORDER BY created_at DESC LIMIT 1`, id).Scan(&snapshot); e != nil {
			return nil, httpx.NotFound("Resource")
		}
		var history map[string]any
		if json.Unmarshal(snapshot, &history) != nil {
			return nil, httpx.NotFound("Resource")
		}
		history["runtimeStatus"] = "removed"
		if history["managed"] == true {
			history["managementStatus"] = "terminated"
		}
		return history, nil
	}
	var out map[string]any
	err = json.Unmarshal(raw, &out)
	return out, err
}
func (s *Server) publicOperation(w http.ResponseWriter, r *http.Request, u *store.User, t *adminToken, kind, permission, resource, team string, out map[string]any, details any) error {
	ctx := r.Context()
	job, _ := out["jobId"].(string)
	dep, _ := out["deploymentId"].(string)
	if dep != "" && job == "" {
		s.Store.SQL(ctx).QueryRow(ctx, `SELECT id FROM jobs WHERE deployment_id=$1 ORDER BY created_at DESC LIMIT 1`, dep).Scan(&job)
	}
	var id string
	err := s.Store.SQL(ctx).QueryRow(ctx, `INSERT INTO admin_operations(token_id,actor_id,team_id,resource_id,type,permission,job_id,deployment_id,details) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, t.ID, u.ID, team, resource, kind, permission, optionalID(job), optionalID(dep), encodePublic(details)).Scan(&id)
	if err != nil {
		return err
	}
	httpx.JSON(w, 202, map[string]any{"data": map[string]any{"resourceId": resource, "operationId": id, "deploymentId": optionalID(dep)}})
	return nil
}
func (s *Server) publicResources(w http.ResponseWriter, r *http.Request, u *store.User, t *adminToken, a string) error {
	ctx := r.Context()
	id := r.PathValue("id")
	q := r.URL.Query()
	switch a {
	case "operation":
		var raw []byte
		var permission string
		err := s.Store.SQL(ctx).QueryRow(ctx, `SELECT permission,jsonb_build_object('id',id,'type',type,'status',status,'resourceId',resource_id,'deploymentId',deployment_id,'error',error,'details',details,'createdAt',created_at,'completedAt',completed_at) FROM admin_operations WHERE id=$1`, id).Scan(&permission, &raw)
		if err != nil {
			return httpx.NotFound("Operation")
		}
		if !publicHas(t, permission) {
			return httpx.Forbidden("This operation requires " + permission + ".")
		}
		var v any
		json.Unmarshal(raw, &v)
		publicOK(w, v)
		return nil
	case "usage":
		if teamID := q.Get("teamId"); teamID != "" {
			if !uuidRE.MatchString(teamID) {
				return httpx.NotFound("Team")
			}
			if _, e := s.Store.AdminTeam(ctx, teamID); e != nil {
				return httpx.NotFound("Team")
			}
			usage, e := s.Store.QuotaUsage(ctx, "team", teamID)
			if e != nil {
				return e
			}
			fillStorage(&usage, s.volumeSizes(ctx))
			publicOK(w, publicUsage(usage))
			return nil
		}
		usage, e := s.Store.QuotaUsage(ctx, "instance", "")
		if e != nil {
			return e
		}
		fillStorage(&usage, s.volumeSizes(ctx))
		publicOK(w, publicUsage(usage))
		return nil
	case "audit-event":
		var raw []byte
		e := s.Store.SQL(ctx).QueryRow(ctx, `SELECT jsonb_build_object('id',id,'actor',actor_email,'action',action,'targetType',target_type,'targetId',target_id,'detail',detail,'createdAt',created_at) FROM audit_events WHERE id::text=$1`, id).Scan(&raw)
		if e != nil {
			return httpx.NotFound("Audit event")
		}
		var out any
		json.Unmarshal(raw, &out)
		publicOK(w, out)
		return nil
	case "audit":
		var since, until *time.Time
		for name, target := range map[string]**time.Time{"since": &since, "until": &until} {
			if value := q.Get(name); value != "" {
				parsed, e := time.Parse(time.RFC3339, value)
				if e != nil {
					return httpx.Invalid(map[string]string{name: "Use an RFC3339 timestamp."})
				}
				*target = &parsed
			}
		}
		rows, e := s.Store.SQL(ctx).Query(ctx, `SELECT id,actor_email,action,target_type,target_id,detail,ip,created_at FROM audit_events
   WHERE ($1='' OR actor_email ILIKE '%'||$1||'%' OR action ILIKE '%'||$1||'%' OR target_id=$1)
   AND ($2='' OR actor_id::text=$2) AND ($3='' OR action=$3) AND ($4='' OR target_id=$4)
   AND ($5='' OR team_id::text=$5) AND ($6='' OR detail->>'adminTokenId'=$6 OR detail->>'tokenId'=$6)
   AND ($7='' OR (target_type='resource' AND target_id=$7) OR detail->>'resource'=$7)
   AND ($8::timestamptz IS NULL OR created_at>=$8) AND ($9::timestamptz IS NULL OR created_at<=$9) ORDER BY id`, q.Get("q"), q.Get("actorId"), q.Get("action"), q.Get("target"), q.Get("teamId"), q.Get("tokenId"), q.Get("resourceId"), since, until)
		if e != nil {
			return e
		}
		list := []store.AuditEvent{}
		for rows.Next() {
			var v store.AuditEvent
			if e = rows.Scan(&v.ID, &v.ActorEmail, &v.Action, &v.TargetType, &v.TargetID, &v.Detail, &v.IP, &v.CreatedAt); e != nil {
				rows.Close()
				return e
			}
			list = append(list, v)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		return publicList(w, r, list)
	case "resources":
		rows, e := s.Store.SQL(ctx).Query(ctx, `SELECT id FROM resources UNION SELECT original_resource_id FROM services WHERE original_resource_id IS NOT NULL UNION SELECT resource_id FROM admin_operations WHERE type='resource.delete' AND status='succeeded' AND details ? 'resourceSnapshot'`)
		if e != nil {
			return e
		}
		ids := []string{}
		for rows.Next() {
			var v string
			if e = rows.Scan(&v); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, v)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		list := []map[string]any{}
		for _, v := range ids {
			row, e := s.publicResource(r, v)
			if e != nil {
				return e
			}
			keep := true
			for _, key := range []string{"teamId", "productId", "planId", "managementStatus", "runtimeStatus", "externalId"} {
				if val := q.Get(key); val != "" && fmt.Sprint(row[key]) != val {
					keep = false
				}
			}
			if val := q.Get("managed"); val != "" && fmt.Sprint(row["managed"]) != val {
				keep = false
			}
			if val := strings.ToLower(q.Get("q")); val != "" && !strings.Contains(strings.ToLower(fmt.Sprint(row["name"])), val) {
				keep = false
			}
			if keep {
				list = append(list, row)
			}
		}
		return publicList(w, r, list)
	case "provision":
		var in struct {
			ProductID     string         `json:"productId"`
			PlanID        string         `json:"planId"`
			TeamID        string         `json:"teamId"`
			Name          string         `json:"name"`
			Note          string         `json:"note"`
			ExternalID    string         `json:"externalId"`
			Answers       map[string]any `json:"answers"`
			ServerID      string         `json:"serverId"`
			Configuration map[string]any `json:"configuration"`
		}
		if err := httpx.Decode(r, &in); err != nil {
			return err
		}
		if !uuidRE.MatchString(in.TeamID) {
			return httpx.Invalid(map[string]string{"teamId": "Choose a team using its UUID."})
		}
		if _, err := s.Store.AdminTeam(ctx, in.TeamID); err != nil {
			return httpx.NotFound("Team")
		}
		if len(in.Note) > 2000 {
			return httpx.Invalid(map[string]string{"note": "Use at most 2000 characters."})
		}
		manual := in.Configuration != nil
		if manual && (in.ProductID != "" || in.PlanID != "" || len(in.Answers) > 0 || in.ServerID != "") {
			return httpx.Invalid(map[string]string{"configuration": "Use configuration alone, without productId, planId, answers or serverId."})
		}
		var resource string
		if manual {
			// Keep manual creation on the same team-scoped validation and provisioning path as the dashboard.
			configuration := in.Configuration
			configuration["name"] = in.Name
			handler := s.createResource
			if configuration["type"] == "game" {
				handler = s.createGame
				delete(configuration, "type")
			}
			out, err := s.callAsTeam(r, u, in.TeamID, "", "resources.create", encodePublic(configuration), handler)
			if err != nil {
				return err
			}
			resource, _ = publicMap(out["resource"])["id"].(string)
			if note := strings.TrimSpace(in.Note); note != "" {
				if _, err = s.Store.SQL(ctx).Exec(ctx, `INSERT INTO admin_resource_metadata(resource_id,note) VALUES($1,$2)`, resource, note); err != nil {
					return err
				}
			}
		} else {
			if !uuidRE.MatchString(in.ProductID) || !uuidRE.MatchString(in.PlanID) {
				return httpx.Invalid(map[string]string{"productId": "Choose a product and plan, or provide configuration for a manual resource."})
			}
			p, err := s.Store.Product(ctx, in.ProductID)
			if err != nil {
				return httpx.NotFound("Product")
			}
			if p.Status != "active" {
				return httpx.Conflict("Only active products can be provisioned.")
			}
			body := publicMap(in)
			delete(body, "externalId")
			delete(body, "configuration")
			out, err := s.publicExisting(publicRequest(r, "POST", body), u, s.provisionService, nil)
			if err != nil {
				return err
			}
			resource, _ = publicMap(out["service"])["resourceId"].(string)
		}
		if !uuidRE.MatchString(resource) {
			return httpx.Unavailable("Provisioning returned no resource ID.")
		}
		if err := s.setExternal(r, "resource", resource, in.ExternalID); err != nil {
			return err
		}
		var dep string
		s.Store.SQL(ctx).QueryRow(ctx, `SELECT id FROM deployments WHERE resource_id=$1 ORDER BY created_at DESC LIMIT 1`, resource).Scan(&dep)
		return s.publicOperation(w, r, u, t, "resource.provision", "resources.provision", resource, in.TeamID, map[string]any{"deploymentId": dep}, map[string]any{})
	}
	if strings.HasPrefix(a, "server") || strings.HasPrefix(a, "allocation") || a == "maintenance" {
		return s.publicInfrastructure(w, r, u, t, a)
	}
	if strings.HasPrefix(a, "deployment") && a != "deployments" {
		d, e := s.Store.GetDeployment(ctx, id)
		if e != nil {
			return httpx.NotFound("Deployment")
		}
		var deploymentTeam string
		if e = s.Store.SQL(ctx).QueryRow(ctx, `SELECT team_id FROM resources WHERE id=$1`, d.ResourceID).Scan(&deploymentTeam); e != nil {
			return httpx.NotFound("Resource")
		}
		if a == "deployment" {
			publicOK(w, d)
			return nil
		}
		if a == "deployment-logs" {
			out, e := s.callAsTeam(r, u, deploymentTeam, id, "logs.view", nil, s.deploymentLogs)
			if e != nil {
				return e
			}
			publicOK(w, out)
			return nil
		}
		if e = publicEmpty(r); e != nil {
			return e
		}
		out, e := s.callAsTeam(r, u, deploymentTeam, id, "deployments.cancel", []byte(`{}`), s.cancelDeployment)
		if e != nil {
			return e
		}
		publicOK(w, out)
		return nil
	}
	row, err := s.publicResource(r, id)
	if err != nil {
		return err
	}
	team, _ := row["teamId"].(string)
	if a == "resource" {
		publicOK(w, row)
		return nil
	}
	if a == "resource-operations" {
		rows, e := s.Store.SQL(ctx).Query(ctx, `SELECT jsonb_build_object('id',id,'type',type,'status',status,'resourceId',resource_id,'deploymentId',deployment_id,'error',error,'createdAt',created_at) FROM admin_operations WHERE resource_id=$1 ORDER BY created_at,id`, id)
		if e != nil {
			return e
		}
		out := []map[string]any{}
		for rows.Next() {
			var raw []byte
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				return e
			}
			v := map[string]any{}
			json.Unmarshal(raw, &v)
			out = append(out, v)
		}
		rows.Close()
		return publicList(w, r, out)
	}
	if row["managementStatus"] == "terminated" || row["runtimeStatus"] == "removed" {
		return httpx.Conflict("This resource has been terminated.")
	}
	switch a {
	case "resource-usage", "resource-limits":
		out, e := s.callAsTeam(r, u, team, id, "resources.view", nil, s.getLimits)
		if e != nil {
			return e
		}
		if a == "resource-usage" {
			publicOK(w, out["usage"])
		} else {
			publicOK(w, out)
		}
		return nil
	case "deployments":
		out, e := s.callAsTeam(r, u, team, id, "deployments.view", nil, s.listDeployments)
		if e != nil {
			return e
		}
		return publicList(w, r, out["deployments"])
	case "resource-update":
		var in struct {
			Name       *string `json:"name"`
			Note       *string `json:"note"`
			ExternalID *string `json:"externalId"`
		}
		if err = httpx.Decode(r, &in); err != nil {
			return err
		}
		if in.Name != nil {
			if !nameRE.MatchString(*in.Name) {
				return httpx.Invalid(map[string]string{"name": "Use lowercase letters, numbers and dashes, up to 50 characters."})
			}
			if _, err = s.Store.SQL(ctx).Exec(ctx, `UPDATE resources SET name=$2,updated_at=now() WHERE id=$1`, id, *in.Name); err != nil {
				return err
			}
			if _, err = s.Store.SQL(ctx).Exec(ctx, `UPDATE services SET name=$2 WHERE resource_id=$1`, id, *in.Name); err != nil {
				return err
			}
		}
		if in.Note != nil {
			if len(*in.Note) > 2000 {
				return httpx.Invalid(map[string]string{"note": "Up to 2000 characters."})
			}
			if row["managed"] == true {
				_, err = s.Store.SQL(ctx).Exec(ctx, `UPDATE services SET note=$2 WHERE resource_id=$1`, id, *in.Note)
			} else {
				_, err = s.Store.SQL(ctx).Exec(ctx, `INSERT INTO admin_resource_metadata(resource_id,note) VALUES($1,$2) ON CONFLICT(resource_id) DO UPDATE SET note=excluded.note`, id, *in.Note)
			}
			if err != nil {
				return err
			}
		}
		if in.ExternalID != nil {
			if err = s.setExternal(r, "resource", id, *in.ExternalID); err != nil {
				return err
			}
		}
		updated, e := s.publicResource(r, id)
		if e != nil {
			return e
		}
		publicOK(w, updated)
		return nil
	}
	if row["managementStatus"] == "suspended" && a != "resource-resume" && a != "resource-delete" {
		return httpx.Conflict("Resume this resource before operating or resizing it.")
	}
	if a != "resource-delete" {
		var busy bool
		if err = s.Store.SQL(ctx).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_operations WHERE resource_id=$1 AND status IN ('queued','running')) OR EXISTS(SELECT 1 FROM jobs WHERE resource_id=$1 AND status IN ('pending','running'))`, id).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return httpx.Conflict("Wait for the current resource operation to finish.")
		}
	}
	out := map[string]any{}
	details := map[string]any{}
	permission := "resources.control"
	kind := "resource." + strings.TrimPrefix(a, "resource-")
	switch a {
	case "resource-start", "resource-stop", "resource-restart":
		if err = publicEmpty(r); err != nil {
			return err
		}
		action := strings.TrimPrefix(a, "resource-")
		out, err = s.callAsTeam(r, u, team, id, permission, encodePublic(map[string]string{"action": action}), s.resourceAction)
	case "resource-redeploy":
		permission = "deployments.create"
		if err = publicEmpty(r); err != nil {
			return err
		}
		out, err = s.callAsTeam(r, u, team, id, permission, []byte(`{}`), s.createDeployment)
	case "resource-delete":
		permission = "resources.delete"
		if err = publicEmpty(r); err != nil {
			return err
		}
		if q.Get("volumes") == "true" && !publicHas(t, "storage.delete") {
			return httpx.Forbidden("Deleting volumes additionally requires storage.delete.")
		}
		if q.Get("volumes") != "" && q.Get("volumes") != "false" && q.Get("volumes") != "true" {
			return httpx.BadRequest("volumes must be true or false.")
		}
		details["resourceSnapshot"] = row
		details["removeVolumes"] = q.Get("volumes") == "true"
		if q.Get("volumes") != "true" && s.Runtime != nil {
			volumes, e := s.Runtime.Volumes(ctx, "ign-"+id[:8]+"-")
			if e != nil {
				return httpx.Unavailable("Couldn't inspect volumes before termination.")
			}
			retained := []string{}
			for _, v := range volumes {
				retained = append(retained, v.Name)
			}
			details["retainedVolumes"] = retained
		}

		out, err = s.callAsTeam(r, u, team, id, permission, nil, s.deleteResource)
	case "resource-suspend", "resource-resume":
		permission = "resources.suspend"
		if row["managed"] != true {
			return httpx.Conflict("This resource has no product assignment.")
		}
		var in struct {
			Reason string `json:"reason"`
		}
		if err = httpx.DecodeOptional(r, &in); err != nil {
			return err
		}
		if len(in.Reason) > 300 {
			return httpx.Invalid(map[string]string{"reason": "Up to 300 characters."})
		}
		status, action := "suspended", "stop"
		if a == "resource-resume" {
			status, action = "active", "start"
			if row["managementStatus"] != "suspended" {
				return httpx.Conflict("This resource is not suspended.")
			}
		} else if row["managementStatus"] == "suspended" {
			return httpx.Conflict("This resource is already suspended.")
		}
		if _, err = s.Store.SQL(ctx).Exec(ctx, `UPDATE services SET status=$2,suspended_reason=$3,updated_at=now() WHERE resource_id=$1`, id, status, in.Reason); err != nil {
			return err
		}
		if a == "resource-suspend" {
			if _, err = s.Store.SQL(ctx).Exec(ctx, `UPDATE jobs SET cancel_requested=true,status=CASE WHEN status='pending' THEN 'cancelled' ELSE status END WHERE resource_id=$1 AND status IN ('pending','running')`, id); err != nil {
				return err
			}
		}
		if a == "resource-resume" || row["runtimeStatus"] == "running" || row["runtimeStatus"] == "paused" {
			out, err = s.callAsTeam(r, u, team, id, "resources.control", encodePublic(map[string]string{"action": action}), s.resourceAction)
		}
	case "resource-change-plan":
		permission = "resources.change-plan"
		if row["managed"] != true {
			return httpx.Conflict("This resource has no product assignment.")
		}
		var in struct {
			PlanID string `json:"planId"`
		}
		if err = httpx.Decode(r, &in); err != nil {
			return err
		}
		product, e := s.Store.Product(ctx, fmt.Sprint(row["productId"]))
		if e != nil {
			return e
		}
		var plan *store.Plan
		for i := range product.Plans {
			if product.Plans[i].ID == in.PlanID {
				plan = &product.Plans[i]
			}
		}
		if plan == nil {
			return httpx.Invalid(map[string]string{"planId": "Choose a plan from the same product."})
		}
		if err = s.checkResourceCap(ctx, fmt.Sprint(row["kind"]), plan.CPUs, plan.MemoryMB, plan.Ports); err != nil {
			return err
		}
		if err = s.checkQuotas(ctx, store.QuotaOwner{Team: team, User: u.ID, Location: s.Store.LocalLocation(ctx)}, u.ID, quotaDemand{CPUs: max(0, plan.CPUs-asFloat(row["cpus"])), MemoryMB: max(0, plan.MemoryMB-int(asFloat(row["memoryMb"])))}); err != nil {
			return err
		}
		if err = s.publicPlanCapacity(r, product, plan, row); err != nil {
			return err
		}
		if _, err = s.Store.SQL(ctx).Exec(ctx, `UPDATE services SET plan_id=$2,updated_at=now() WHERE resource_id=$1`, id, plan.ID); err != nil {
			return err
		}
		if _, err = s.Store.SQL(ctx).Exec(ctx, `UPDATE resources SET cpus=$2,memory_mb=$3,updated_at=now() WHERE id=$1`, id, plan.CPUs, plan.MemoryMB); err != nil {
			return err
		}
		out, err = s.callAsTeam(r, u, team, id, "deployments.create", []byte(`{}`), s.createDeployment)
	default:
		return httpx.NotFound("Resource operation")
	}
	if err != nil {
		return err
	}
	return s.publicOperation(w, r, u, t, kind, permission, id, team, out, details)
}
func asFloat(v any) float64 { x, _ := v.(float64); return x }
func (s *Server) publicPlanCapacity(r *http.Request, p *store.Product, plan *store.Plan, row map[string]any) error {
	// A resize replaces the existing commitment, and must not consume another stock/team slot.
	copy := *p
	placement := parsePlacement(p.Placement)
	placement.Stock, placement.PerTeam, placement.PerServer, placement.MaxConcurrent = 0, 0, 0, 0
	copy.Placement = encodePublic(placement)
	delta := *plan
	delta.CPUs = max(0, plan.CPUs-asFloat(row["cpus"]))
	delta.MemoryMB = max(0, plan.MemoryMB-int(asFloat(row["memoryMb"])))
	avail, err := s.availabilityFor(r.Context(), &copy, &delta, fmt.Sprint(row["teamId"]))
	if err != nil {
		return err
	}
	eligible := false
	for _, v := range avail.Servers {
		if v.ID == fmt.Sprint(row["serverId"]) && v.OK {
			eligible = true
		}
	}
	if !eligible {
		return httpx.Conflict("The resource's server cannot accommodate this plan.")
	}
	var ports int
	s.Store.SQL(r.Context()).QueryRow(r.Context(), `SELECT count(*) FROM resource_ports WHERE resource_id=$1`, row["id"]).Scan(&ports)
	if plan.Ports > 0 && ports > plan.Ports {
		return httpx.Conflict("Remove published ports before selecting this plan.")
	}
	sizes := s.volumeSizes(r.Context())
	var storage, backup int64
	prefix := "ign-" + fmt.Sprint(row["id"])[:8] + "-"
	for name, size := range sizes {
		if strings.HasPrefix(name, prefix) {
			if strings.HasSuffix(name, "-backups") {
				backup += size
			} else {
				storage += size
			}
		}
	}
	if plan.StorageGB > 0 && storage > int64(plan.StorageGB)<<30 || plan.BackupGB > 0 && backup > int64(plan.BackupGB)<<30 {
		return httpx.Conflict("Stored data exceeds this plan's storage or backup allowance.")
	}
	return nil
}

func publicUsage(v store.QuotaUsage) map[string]any {
	out := publicMap(v)
	delete(out, "services")
	out["resources"] = v.Services
	return out
}

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/notsliver/Ignition/services/internal/adminapi"
	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/database"
	"github.com/notsliver/Ignition/services/internal/runtime"
	"github.com/notsliver/Ignition/services/internal/secrets"
	"github.com/notsliver/Ignition/services/internal/store"
)

func TestPublicAdminAPI(t *testing.T) {
	dsn := os.Getenv("IGNITION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("IGNITION_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	st := &store.Store{DB: pool}
	box, _ := secrets.New(make([]byte, 32))
	s := &Server{Store: st, Secrets: box, PublicURL: "http://localhost", Runtime: adminTestRuntime{}}
	policy := defaultPolicy()
	policy.BreachedPasswords = false
	s.setPolicy(policy)
	suffix := strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")
	administrator, err := st.CreateUser(ctx, "admin-public-"+suffix+"@test.example", "Admin", "hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	session, hash, _ := auth.NewToken()
	if err = st.CreateSession(ctx, administrator.ID, hash, "test", "127.0.0.1", time.Hour, "password"); err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	call := func(credential, method, path string, body any, key string, want int) map[string]any {
		t.Helper()
		var b bytes.Buffer
		if body != nil {
			json.NewEncoder(&b).Encode(body)
		}
		r := httptest.NewRequest(method, "http://localhost"+path, &b)
		r.RemoteAddr = "127.0.0.1:5500"
		r.Header.Set("Origin", "http://localhost")
		r.Header.Set("Content-Type", "application/json")
		if strings.HasPrefix(credential, "ign_admin_") {
			r.Header.Set("Authorization", "Bearer "+credential)
		} else if credential != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: credential})
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		out := map[string]any{}
		if w.Body.Len() > 0 {
			if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
				t.Fatal(e, w.Body.String())
			}
		}
		return out
	}
	call(session, "GET", "/api/admin/v1/users", nil, "", 401)
	rejected := call(session, "POST", "/api/settings/admin-tokens", map[string]any{"name": "Requires confirmation", "permissions": []string{"users.view"}}, "", 403)
	if rejected["error"].(map[string]any)["code"] != "reauth_required" {
		t.Fatal("token minting did not require recent authentication")
	}
	if err = st.ReauthenticateSession(ctx, administrator.ID, hash); err != nil {
		t.Fatal(err)
	}
	permissions := []string{}
	for _, p := range publicPermissionCatalog() {
		permissions = append(permissions, p.Key)
	}
	minted := call(session, "POST", "/api/settings/admin-tokens", map[string]any{"name": "Integration", "permissions": permissions}, "", 201)
	defaultExpiry, err := time.Parse(time.RFC3339Nano, minted["metadata"].(map[string]any)["expiresAt"].(string))
	if err != nil || time.Until(defaultExpiry) < 29*24*time.Hour || time.Until(defaultExpiry) > 31*24*time.Hour {
		t.Fatal("omitted expiry must default to 30 days")
	}
	never := call(session, "POST", "/api/settings/admin-tokens", map[string]any{"name": "Never expires", "permissions": []string{"users.view", "users.update"}, "expiresAt": nil}, "", 201)
	if never["metadata"].(map[string]any)["expiresAt"] != nil {
		t.Fatal("explicit null must create a token without expiry")
	}
	neverSecret := never["token"].(string)
	call(neverSecret, "GET", "/api/admin/v1/users", nil, "", 200)
	// A write exercises the transaction's second credential check too.
	call(neverSecret, "PATCH", "/api/admin/v1/users/00000000-0000-4000-8000-000000000000", map[string]any{"name": "Missing"}, "", 404)
	neverID := never["metadata"].(map[string]any)["id"].(string)
	replacement := call(session, "POST", "/api/settings/admin-tokens/"+neverID+"/rotate", map[string]any{"expiresAt": nil}, "", 201)
	if replacement["metadata"].(map[string]any)["expiresAt"] != nil {
		t.Fatal("rotation must accept no expiry")
	}
	call(neverSecret, "GET", "/api/admin/v1/users", nil, "", 401)
	call(replacement["token"].(string), "GET", "/api/admin/v1/users", nil, "", 200)
	call(session, "DELETE", "/api/settings/admin-tokens/"+replacement["metadata"].(map[string]any)["id"].(string), nil, "", 204)
	call(replacement["token"].(string), "GET", "/api/admin/v1/users", nil, "", 401)
	token := minted["token"].(string)
	tokenID := minted["metadata"].(map[string]any)["id"].(string)
	metadata := call(session, "GET", "/api/settings/admin-tokens", nil, "", 200)
	if strings.Contains(string(encodePublic(metadata)), token) {
		t.Fatal("management list leaked the secret")
	}
	var digest []byte
	pool.QueryRow(ctx, `SELECT token_hash FROM admin_tokens WHERE id=$1`, tokenID).Scan(&digest)
	if bytes.Equal(digest, []byte(token)) || !bytes.Equal(digest, auth.HashToken(token)) {
		t.Fatal("token was not stored as a hash")
	}
	call(token, "GET", "/api/admin/v1/token", nil, "", 200)
	call(token, "POST", "/api/settings/admin-tokens", map[string]any{}, "", 401)
	read := call(session, "POST", "/api/settings/admin-tokens", map[string]any{"name": "Read only", "permissions": []string{"users.view"}}, "", 201)["token"].(string)
	call(read, "POST", "/api/admin/v1/users", map[string]any{}, "", 403)
	oldLimits := st.Limits(ctx)
	defer st.SetSetting(ctx, "limits", oldLimits)
	st.SetSetting(ctx, "limits", store.InstanceLimits{PersonalTeams: true})
	body := map[string]any{"name": "Customer", "email": "customer-public-" + suffix + "@test.example", "role": "viewer", "externalId": "customer-" + suffix}
	created := call(token, "POST", "/api/admin/v1/users", body, "customer", 201)["data"].(map[string]any)
	uid := created["id"].(string)
	team := created["createdTeam"].(map[string]any)["id"].(string)
	replay := call(token, "POST", "/api/admin/v1/users", body, "customer", 201)["data"].(map[string]any)
	if replay["id"] != uid {
		t.Fatal("idempotency created a second user")
	}
	changed := map[string]any{"name": "Changed", "email": body["email"], "role": "viewer"}
	call(token, "POST", "/api/admin/v1/users", changed, "customer", 409)
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM teams WHERE created_by=$1`, uid).Scan(&n)
	if n != 1 {
		t.Fatal("personal team count", n)
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM environments WHERE team_id=$1 AND name='Production' AND is_team_default`, team).Scan(&n)
	if n != 1 {
		t.Fatal("missing default environment")
	}
	// Failure after account/team initialization must roll all rows back.
	duplicate := map[string]any{"name": "Duplicate external", "email": "rollback-public-" + suffix + "@test.example", "role": "viewer", "externalId": "customer-" + suffix}
	call(token, "POST", "/api/admin/v1/users", duplicate, "rollback", 409)
	pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email=$1`, duplicate["email"]).Scan(&n)
	if n != 0 {
		t.Fatal("failed request left an account behind")
	}
	setup := call(token, "POST", "/api/admin/v1/users/"+uid+"/setup-link", map[string]any{}, "setup", 201)
	if !strings.Contains(setup["data"].(map[string]any)["url"].(string), "/join/") {
		t.Fatal("no setup link")
	}
	setupURL := setup["data"].(map[string]any)["url"].(string)
	setupToken := setupURL[strings.LastIndex(setupURL, "/")+1:]
	call("", "POST", "/api/links/"+setupToken, map[string]any{"password": "API-customer-password-2026!"}, "", 200)
	call("", "POST", "/api/links/"+setupToken, map[string]any{"password": "API-customer-password-2026!"}, "", 404)
	call(token, "POST", "/api/admin/v1/users/"+uid+"/setup-link", map[string]any{}, "", 409)
	call(token, "PATCH", "/api/admin/v1/users/"+administrator.ID, map[string]any{"name": "Escalate"}, "", 403)
	call(token, "POST", "/api/admin/v1/users", map[string]any{"name": "Admin", "email": "bad-" + suffix + "@test.example", "role": "admin"}, "", 422)
	st.SetSetting(ctx, "limits", store.InstanceLimits{PersonalTeams: false})
	noTeam := call(token, "POST", "/api/admin/v1/users", map[string]any{"name": "No team", "email": "no-team-" + suffix + "@test.example", "role": "viewer"}, "", 201)["data"].(map[string]any)
	if noTeam["createdTeam"] != nil {
		t.Fatal("personal team setting ignored")
	}
	st.SetSetting(ctx, "limits", oldLimits)
	product := call(token, "POST", "/api/admin/v1/products", map[string]any{"name": "Public worker", "kind": "worker", "status": "active", "template": map[string]any{"sourceType": "image", "image": "busybox", "env": []any{map[string]any{"key": "PASSWORD", "value": "should-never-leak", "secret": true}}}, "plans": []any{map[string]any{"name": "Small", "cpus": 0.5, "memoryMb": 128}}}, "product", 201)["data"].(map[string]any)
	if strings.Contains(string(encodePublic(product)), "should-never-leak") {
		t.Fatal("product response leaked template secrets")
	}
	pid := product["id"].(string)
	plan := product["plans"].([]any)[0].(map[string]any)["id"].(string)
	pool.Exec(ctx, `INSERT INTO team_server_assignments(team_id,server_id) SELECT $1,id FROM servers WHERE is_local ON CONFLICT DO NOTHING`, team)
	manualBody := map[string]any{"teamId": team, "name": "manual-worker-" + suffix[:6], "note": "Manual hosting", "externalId": "manual-" + suffix, "configuration": map[string]any{"type": "worker", "sourceType": "image", "image": "busybox:1.37", "command": "sleep 3600"}}
	manual := call(token, "POST", "/api/admin/v1/resources", manualBody, "manual-provision", 202)["data"].(map[string]any)
	manualID := manual["resourceId"].(string)
	manualReplay := call(token, "POST", "/api/admin/v1/resources", manualBody, "manual-provision", 202)["data"].(map[string]any)
	if manualReplay["resourceId"] != manualID {
		t.Fatal("manual provisioning replay created another resource")
	}
	manualResource := call(token, "GET", "/api/admin/v1/resources/"+manualID, nil, "", 200)["data"].(map[string]any)
	if manualResource["managed"] != false || manualResource["productId"] != nil || manualResource["planId"] != nil || manualResource["note"] != "Manual hosting" {
		t.Fatal("manual resource unexpectedly assigned to a product or lost its note")
	}
	defaults := s.defaults(ctx)
	if manualResource["cpus"] != defaults.CPUs || manualResource["memoryMb"] != float64(defaults.MemoryMB) {
		t.Fatal("manual creation ignored instance defaults")
	}
	call(token, "GET", "/api/admin/v1/operations/"+manual["operationId"].(string), nil, "", 200)
	manualBody["productId"], manualBody["planId"] = pid, plan
	call(token, "POST", "/api/admin/v1/resources", manualBody, "", 422)
	delete(manualBody, "productId")
	delete(manualBody, "planId")
	manualBody["name"] = "manual-rollback-" + suffix[:6]
	call(token, "POST", "/api/admin/v1/resources", manualBody, "", 409)
	pool.QueryRow(ctx, `SELECT count(*) FROM resources WHERE name=$1`, manualBody["name"]).Scan(&n)
	if n != 0 {
		t.Fatal("failed manual provisioning left an orphaned resource")
	}
	manualBody["configuration"] = map[string]any{"type": "worker", "sourceType": "image", "image": "busybox", "cpus": 65}
	call(token, "POST", "/api/admin/v1/resources", manualBody, "", 422)
	call(token, "POST", "/api/admin/v1/resources", map[string]any{"teamId": team, "name": "missing-mode"}, "", 422)
	request := map[string]any{"teamId": team, "productId": pid, "planId": plan, "name": "api-worker-" + suffix[:6], "externalId": "order-" + suffix}
	provisioned := call(token, "POST", "/api/admin/v1/resources", request, "provision", 202)["data"].(map[string]any)
	rid := provisioned["resourceId"].(string)
	operation := provisioned["operationId"].(string)
	var stored string
	pool.QueryRow(ctx, `SELECT resource_id FROM services WHERE original_resource_id=$1`, rid).Scan(&stored)
	if stored != rid {
		t.Fatal("public ID was not the runtime resource UUID")
	}
	call(token, "GET", "/api/admin/v1/resources/"+rid, nil, "", 200)
	call(token, "GET", "/api/admin/v1/operations/"+operation, nil, "", 200)
	call(token, "PATCH", "/api/admin/v1/products/"+pid+"/plans/"+plan, map[string]any{"memoryMb": 256}, "", 409)
	request["name"] = "rollback-worker-" + suffix[:6]
	call(token, "POST", "/api/admin/v1/resources", request, "failed-provision", 409)
	pool.QueryRow(ctx, `SELECT count(*) FROM resources WHERE name=$1`, request["name"]).Scan(&n)
	if n != 0 {
		t.Fatal("failed provisioning left an orphaned resource")
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM jobs j JOIN resources r ON r.id=j.resource_id WHERE r.name=$1`, request["name"]).Scan(&n)
	if n != 0 {
		t.Fatal("failed provisioning left a deploy job")
	}
	call(token, "GET", "/api/admin/v1/resources?limit=1", nil, "", 200)
	// Runtime work is not executed by the test; reconcile its recorded successful queue outcome.
	pool.Exec(ctx, `UPDATE jobs SET status='succeeded' WHERE deployment_id=$1`, provisioned["deploymentId"])
	pool.Exec(ctx, `UPDATE resources SET status='running' WHERE id=$1`, rid)
	if err = adminapi.Reconcile(ctx, st); err != nil {
		t.Fatal(err)
	}
	observed := call(token, "GET", "/api/admin/v1/operations/"+operation, nil, "", 200)["data"].(map[string]any)
	if observed["status"] != "succeeded" {
		t.Fatal(observed)
	}
	hook := call(token, "POST", "/api/admin/v1/webhooks", map[string]any{"url": "https://hooks.example.com/ignition", "events": []string{"resource.provisioned"}}, "hook", 201)["data"].(map[string]any)
	hid := hook["id"].(string)
	readHook := call(token, "GET", "/api/admin/v1/webhooks/"+hid, nil, "", 200)
	if strings.Contains(string(encodePublic(readHook)), hook["signingSecret"].(string)) {
		t.Fatal("webhook read leaked signing secret")
	}
	call(token, "POST", "/api/admin/v1/webhooks", map[string]any{"url": "https://127.0.0.1/internal", "events": []string{"resource.provisioned"}}, "", 422)
	call(token, "POST", "/api/admin/v1/webhooks/"+hid+"/test", map[string]any{}, "", 202)
	// Live permission removal takes effect without rotating the token.
	pool.Exec(ctx, `UPDATE users SET permissions='{"deny":["users.view"]}' WHERE id=$1`, administrator.ID)
	call(read, "GET", "/api/admin/v1/users", nil, "", 403)
	pool.Exec(ctx, `UPDATE users SET permissions='{}' WHERE id=$1`, administrator.ID)
	rotated := call(session, "POST", "/api/settings/admin-tokens/"+tokenID+"/rotate", map[string]any{}, "", 201)
	call(token, "GET", "/api/admin/v1/token", nil, "", 401)
	next := rotated["token"].(string)
	nextID := rotated["metadata"].(map[string]any)["id"].(string)
	call(next, "GET", "/api/admin/v1/openapi.json", nil, "", 200)
	call(session, "DELETE", "/api/settings/admin-tokens/"+nextID, nil, "", 204)
	call(next, "GET", "/api/admin/v1/token", nil, "", 401)
	// Check the endpoints beyond the initial provisioning path against real database state.
	more := call(session, "POST", "/api/settings/admin-tokens", map[string]any{"name": "Full endpoint checks", "permissions": permissions}, "", 201)["token"].(string)
	for _, path := range []string{"/instance", "/defaults", "/permissions", "/users", "/users/" + uid, "/teams", "/teams/" + team, "/teams/" + team + "/members", "/teams/" + team + "/invitations", "/teams/" + team + "/usage", "/usage?teamId=" + team, "/usage", "/capacity", "/products", "/products/" + pid, "/products/" + pid + "/plans", "/products/" + pid + "/plans/" + plan, "/products/" + pid + "/availability?planId=" + plan + "&teamId=" + team, "/resources/" + rid + "/usage", "/resources/" + rid + "/limits", "/resources/" + rid + "/operations", "/resources/" + rid + "/deployments", "/deployments/" + provisioned["deploymentId"].(string), "/deployments/" + provisioned["deploymentId"].(string) + "/logs", "/webhooks", "/webhooks/" + hid + "/deliveries", "/audit-events?tokenId=" + tokenID, "/servers"} {
		call(more, "GET", "/api/admin/v1"+path, nil, "", 200)
	}
	// Customer account changes cannot suspend a sole active team owner.
	call(more, "POST", "/api/admin/v1/users/"+uid+"/suspend", map[string]any{}, "", 409)
	noTeamID := noTeam["id"].(string)
	call(more, "POST", "/api/admin/v1/users/"+noTeamID+"/suspend", map[string]any{}, "", 200)
	call(more, "POST", "/api/admin/v1/users/"+noTeamID+"/resume", map[string]any{}, "", 200)
	call(more, "PATCH", "/api/admin/v1/users/"+noTeamID, map[string]any{"name": "Updated customer"}, "", 200)
	call(more, "POST", "/api/admin/v1/users/"+noTeamID+"/sessions/revoke", map[string]any{}, "", 200)
	st.SetSetting(ctx, "limits", store.InstanceLimits{PersonalTeams: true, TeamsPerUser: 4})
	extraTeam := call(more, "POST", "/api/admin/v1/teams", map[string]any{"name": "Explicit team", "ownerId": uid}, "", 201)["data"].(map[string]any)["id"].(string)
	call(more, "PATCH", "/api/admin/v1/teams/"+extraTeam, map[string]any{"name": "Renamed team"}, "", 200)
	call(more, "POST", "/api/admin/v1/teams/"+extraTeam+"/members", map[string]any{"userId": noTeamID, "role": "viewer"}, "", 204)
	call(more, "PATCH", "/api/admin/v1/teams/"+extraTeam+"/members/"+noTeamID, map[string]any{"role": "developer"}, "", 204)
	call(more, "DELETE", "/api/admin/v1/teams/"+extraTeam+"/members/"+noTeamID, nil, "", 204)
	call(more, "DELETE", "/api/admin/v1/teams/"+extraTeam+"/members/"+uid, nil, "", 403)
	invite := call(more, "POST", "/api/admin/v1/invitations", map[string]any{"email": "invite-" + suffix + "@test.example", "role": "viewer"}, "", 201)["data"].(map[string]any)["invite"].(map[string]any)["id"].(string)
	call(more, "GET", "/api/admin/v1/invitations", nil, "", 200)
	call(more, "DELETE", "/api/admin/v1/invitations/"+invite, nil, "", 204)
	teamInvite := call(more, "POST", "/api/admin/v1/teams/"+extraTeam+"/invitations", map[string]any{"email": "team-invite-" + suffix + "@test.example", "role": "viewer"}, "", 201)["data"].(map[string]any)["id"].(string)
	call(more, "DELETE", "/api/admin/v1/teams/"+extraTeam+"/invitations/"+teamInvite, nil, "", 204)
	addedPlan := call(more, "POST", "/api/admin/v1/products/"+pid+"/plans", map[string]any{"name": "Large", "cpus": 1, "memoryMb": 256}, "", 201)["data"].(map[string]any)["id"].(string)
	call(more, "PATCH", "/api/admin/v1/products/"+pid+"/plans/"+addedPlan, map[string]any{"name": "Large renamed"}, "", 200)
	call(more, "DELETE", "/api/admin/v1/products/"+pid+"/plans/"+addedPlan, nil, "", 204)
	copyID := call(more, "POST", "/api/admin/v1/products/"+pid+"/duplicate", map[string]any{}, "", 201)["data"].(map[string]any)["id"].(string)
	call(more, "PATCH", "/api/admin/v1/products/"+copyID, map[string]any{"name": "Copy changed"}, "", 200)
	call(more, "DELETE", "/api/admin/v1/products/"+copyID, nil, "", 204)
	call(more, "PATCH", "/api/admin/v1/resources/"+rid, map[string]any{"note": "Customer note"}, "", 200)
	call(more, "POST", "/api/admin/v1/resources/"+rid+"/suspend", map[string]any{"reason": "Test suspension"}, "", 409) // running resource needs a deployed container
	call(more, "PATCH", "/api/admin/v1/webhooks/"+hid, map[string]any{"enabled": false}, "", 200)
	call(more, "POST", "/api/admin/v1/webhooks/"+hid+"/test", map[string]any{}, "", 409)
	call(more, "DELETE", "/api/admin/v1/webhooks/"+hid, nil, "", 204)
	var serverID string
	pool.QueryRow(ctx, `SELECT id FROM servers WHERE is_local LIMIT 1`).Scan(&serverID)
	for _, path := range []string{"/servers/" + serverID, "/servers/" + serverID + "/capacity", "/servers/" + serverID + "/allocations"} {
		call(more, "GET", "/api/admin/v1"+path, nil, "", 200)
	}
	call(more, "PUT", "/api/admin/v1/servers/"+serverID+"/maintenance", map[string]any{"enabled": false, "note": ""}, "", 200)
	call(more, "POST", "/api/admin/v1/servers/"+serverID+"/allocations", map[string]any{"ip": "127.0.0.1", "ports": "39123"}, "", 201)
	var allocationID string
	pool.QueryRow(ctx, `SELECT id FROM server_allocations WHERE server_id=$1 AND ip='127.0.0.1' AND port=39123`, serverID).Scan(&allocationID)
	call(more, "PATCH", "/api/admin/v1/servers/"+serverID+"/allocations/"+allocationID, map[string]any{"alias": "play.example.com"}, "", 204)
	call(more, "DELETE", "/api/admin/v1/servers/"+serverID+"/allocations/"+allocationID, nil, "", 204)
	// Successful queue completion must agree with observed runtime state before lifecycle events fire.
	pool.Exec(ctx, `UPDATE resources SET container_id='test-container',status='running' WHERE id=$1`, rid)
	finish := func(operationID, state string) {
		t.Helper()
		var jobID string
		if e := pool.QueryRow(ctx, `SELECT job_id FROM admin_operations WHERE id=$1`, operationID).Scan(&jobID); e != nil {
			t.Fatal(e)
		}
		pool.Exec(ctx, `UPDATE jobs SET status='succeeded' WHERE id=$1`, jobID)
		pool.Exec(ctx, `UPDATE resources SET status=$2 WHERE id=$1`, rid, state)
		if e := adminapi.Reconcile(ctx, st); e != nil {
			t.Fatal(e)
		}
	}
	suspended := call(more, "POST", "/api/admin/v1/resources/"+rid+"/suspend", map[string]any{"reason": "Billing test"}, "suspend", 202)["data"].(map[string]any)
	finish(suspended["operationId"].(string), "stopped")
	call(more, "POST", "/api/admin/v1/resources/"+rid+"/start", map[string]any{}, "", 409)
	resumed := call(more, "POST", "/api/admin/v1/resources/"+rid+"/resume", map[string]any{}, "resume", 202)["data"].(map[string]any)
	finish(resumed["operationId"].(string), "running")
	resizePlan := call(more, "POST", "/api/admin/v1/products/"+pid+"/plans", map[string]any{"name": "Resize", "cpus": 1, "memoryMb": 256}, "", 201)["data"].(map[string]any)["id"].(string)
	resized := call(more, "POST", "/api/admin/v1/resources/"+rid+"/change-plan", map[string]any{"planId": resizePlan}, "resize", 202)["data"].(map[string]any)
	finish(resized["operationId"].(string), "running")
	for _, action := range []string{"stop", "start", "restart", "redeploy"} {
		out := call(more, "POST", "/api/admin/v1/resources/"+rid+"/"+action, map[string]any{}, action, 202)["data"].(map[string]any)
		state := "running"
		if action == "stop" {
			state = "stopped"
		}
		finish(out["operationId"].(string), state)
	}
	removalOnly := call(session, "POST", "/api/settings/admin-tokens", map[string]any{"name": "Removal only", "permissions": []string{"resources.delete"}}, "", 201)["token"].(string)
	name := request["name"].(string)
	_ = name
	call(removalOnly, "DELETE", "/api/admin/v1/resources/"+rid+"?confirm=api-worker-"+suffix[:6]+"&volumes=true", nil, "", 403)
	removed := call(more, "DELETE", "/api/admin/v1/resources/"+rid+"?confirm=api-worker-"+suffix[:6]+"&volumes=false", nil, "remove", 202)["data"].(map[string]any)
	pool.Exec(ctx, `DELETE FROM resources WHERE id=$1`, rid)
	if e := adminapi.Reconcile(ctx, st); e != nil {
		t.Fatal(e)
	}
	call(more, "GET", "/api/admin/v1/operations/"+removed["operationId"].(string), nil, "", 200)
	history := call(more, "GET", "/api/admin/v1/resources/"+rid, nil, "", 200)["data"].(map[string]any)
	if history["managementStatus"] != "terminated" {
		t.Fatal(history)
	}
	call(more, "DELETE", "/api/admin/v1/products/"+pid, nil, "", 204)
	call(more, "GET", "/api/admin/v1/resources/"+rid, nil, "", 200) // operation snapshot survives catalog removal
	// Pre-created SSO accounts link only with setup proof plus a verified matching identity.
	oldOIDC := s.oidcConfig(ctx)
	defer st.SetSetting(ctx, "oidc", oldOIDC)
	now := time.Now()
	oidc := oidcSettings{Issuer: "https://sso.example.test", ClientID: "test", VerifiedAt: &now}
	st.SetSetting(ctx, "oidc", oidc)
	ssoPolicy := policy
	ssoPolicy.OIDCAuth = true
	ssoPolicy.RequiredMethod = "sso"
	ssoPolicy.SSODefaultRole = "developer"
	s.setPolicy(ssoPolicy)
	ssoUser := call(more, "POST", "/api/admin/v1/users", map[string]any{"email": "sso-public-" + suffix + "@test.example", "name": "SSO customer"}, "sso-customer", 201)["data"].(map[string]any)
	ssoID := ssoUser["id"].(string)
	if ssoUser["role"] != "developer" {
		t.Fatal("SSO role defaults ignored")
	}
	link := call(more, "POST", "/api/admin/v1/users/"+ssoID+"/setup-link", map[string]any{}, "sso-setup", 201)["data"].(map[string]any)["url"].(string)
	raw := link[strings.LastIndex(link, "/")+1:]
	verified := true
	claims := &auth.Claims{Subject: "public-subject-" + suffix, Email: "wrong@example.test", EmailVerified: &verified}
	pending := ssoPending{userID: ssoID, onboardingHash: auth.HashToken(raw)}
	if _, e := s.completeAdminSSO(httptest.NewRequest("GET", "http://localhost/", nil), pending, oidc, claims); e == nil {
		t.Fatal("linked a mismatched SSO identity")
	}
	claims.Email = ssoUser["email"].(string)
	if _, e := s.completeAdminSSO(httptest.NewRequest("GET", "http://localhost/", nil), pending, oidc, claims); e != nil {
		t.Fatal(e)
	}
	if _, e := s.completeAdminSSO(httptest.NewRequest("GET", "http://localhost/", nil), pending, oidc, claims); e == nil {
		t.Fatal("replayed SSO setup link")
	}
	s.setPolicy(policy)
	// Network and creator status restrictions remain active independently of token scopes.
	restricted := call(session, "POST", "/api/settings/admin-tokens", map[string]any{"name": "Restricted", "permissions": []string{"users.view"}, "networks": []string{"203.0.113.0/24"}}, "", 201)["token"].(string)
	call(restricted, "GET", "/api/admin/v1/users", nil, "", 403)
	expiredMint := call(session, "POST", "/api/settings/admin-tokens", map[string]any{"name": "Expired check", "permissions": []string{"users.view"}}, "", 201)
	expiredToken := expiredMint["token"].(string)
	expiredID := expiredMint["metadata"].(map[string]any)["id"].(string)
	pool.Exec(ctx, `UPDATE admin_tokens SET expires_at=now()-interval '1 minute' WHERE id=$1`, expiredID)
	call(expiredToken, "GET", "/api/admin/v1/users", nil, "", 401)
	pool.Exec(ctx, `UPDATE users SET role='viewer' WHERE id=$1`, administrator.ID)
	call(more, "GET", "/api/admin/v1/token", nil, "", 401)
	pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID)
	// Concurrent same-key requests return the same user and one personal team.
	minted = call(session, "POST", "/api/settings/admin-tokens", map[string]any{"name": "Concurrency", "permissions": permissions}, "", 201)
	concurrentToken := minted["token"].(string)
	var wg sync.WaitGroup
	results := make(chan string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := call(concurrentToken, "POST", "/api/admin/v1/users", map[string]any{"email": "concurrent-" + suffix + "@test.example", "name": "Concurrent", "role": "viewer"}, "concurrent", 201)
			results <- result["data"].(map[string]any)["id"].(string)
		}()
	}
	wg.Wait()
	close(results)
	previous := ""
	for id := range results {
		if previous != "" && previous != id {
			t.Fatal("concurrent duplicate")
		}
		previous = id
	}
}

type adminTestRuntime struct{ runtime.Runtime }

func (adminTestRuntime) Host(context.Context) (runtime.HostInfo, error) {
	return runtime.HostInfo{CPUs: 64, MemoryBytes: 64 << 30}, nil
}
func (adminTestRuntime) Volumes(context.Context, string) ([]runtime.VolumeInfo, error) {
	return []runtime.VolumeInfo{}, nil
}

func (adminTestRuntime) Inspect(context.Context, string) (runtime.State, error) {
	return runtime.State{Exists: true, Running: true}, nil
}

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/database"
	"github.com/notsliver/Ignition/services/internal/secrets"
	"github.com/notsliver/Ignition/services/internal/store"
)

func TestProductsAndServices(t *testing.T) {
	url := os.Getenv("IGNITION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IGNITION_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	lock, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lock.Exec(ctx, `SELECT pg_advisory_lock(8724312)`)
	t.Cleanup(func() { lock.Exec(context.Background(), `SELECT pg_advisory_unlock(8724312)`); lock.Release() })

	box, _ := secrets.New(make([]byte, 32))
	s := &Server{Store: &store.Store{DB: pool}, PublicURL: "http://localhost", Secrets: box, UploadsDir: t.TempDir()}
	s.setPolicy(defaultPolicy())
	suffix := time.Now().Format("150405.000000000")
	session := func(role string) (*store.User, string) {
		u, err := s.Store.CreateUser(ctx, role+"-products-"+suffix+"@test.example", role, "hash", role)
		if err != nil {
			t.Fatal(err)
		}
		raw, h, _ := auth.NewToken()
		if err = s.Store.CreateSession(ctx, u.ID, h, "test", "127.0.0.1", time.Hour, "password"); err != nil {
			t.Fatal(err)
		}
		return u, raw
	}
	customer, customerSession := session("viewer")
	_, adminSession := session("admin")
	team, err := s.Store.CreateTeam(ctx, customer.ID, "Customer "+suffix)
	if err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, `INSERT INTO team_server_assignments(team_id,server_id) SELECT $1, s.id FROM servers s WHERE s.is_local ON CONFLICT DO NOTHING`, team.ID)

	handler := s.Handler()
	call := func(cred, method, path string, in any, want int) map[string]any {
		t.Helper()
		var body bytes.Buffer
		if in != nil {
			json.NewEncoder(&body).Encode(in)
		}
		r := httptest.NewRequest(method, "http://localhost"+path, &body)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://localhost")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cred})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		out := map[string]any{}
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}

	// Only instance administrators manage products.
	call(customerSession, "GET", "/api/platform/products", nil, 403)
	call(adminSession, "POST", "/api/platform/products", map[string]any{"name": "Queue worker", "kind": "worker", "plans": []any{}}, 422)

	product := call(adminSession, "POST", "/api/platform/products", map[string]any{
		"name": "Queue worker", "category": "Workers", "kind": "worker", "status": "active",
		"template": map[string]any{"sourceType": "image", "image": "busybox", "command": "sleep infinity",
			"env": []any{map[string]any{"key": "SECRET", "value": "{{password}}", "secret": true}, map[string]any{"key": "SERVICE", "value": "{{name}}"}}},
		"plans": []any{
			map[string]any{"name": "Small", "cpus": 0.5, "memoryMb": 256, "price": "4.99", "period": "month"},
			map[string]any{"name": "Large", "cpus": 2, "memoryMb": 2048, "price": "19.99", "period": "month"},
		},
	}, 201)["product"].(map[string]any)
	pid := product["id"].(string)
	plans := product["plans"].([]any)
	small, large := plans[0].(map[string]any)["id"].(string), plans[1].(map[string]any)["id"].(string)

	// Assigning a plan provisions the resource in the team, sized to the plan.
	svc := call(adminSession, "POST", "/api/platform/services", map[string]any{"productId": pid, "planId": small, "teamId": team.ID, "name": "jobs-" + suffix[:6]}, 201)["service"].(map[string]any)
	sid, rid := svc["id"].(string), svc["resourceId"].(string)
	var mem int
	var cpus float64
	pool.QueryRow(ctx, `SELECT memory_mb, cpus::float8 FROM resources WHERE id = $1 AND team_id = $2`, rid, team.ID).Scan(&mem, &cpus)
	if mem != 256 || cpus != 0.5 {
		t.Fatalf("resource size = %d MB, %g CPU", mem, cpus)
	}
	var secret, name string
	pool.QueryRow(ctx, `SELECT count(*)::text FROM env_vars WHERE resource_id = $1`, rid).Scan(&secret)
	if secret != "2" {
		t.Fatalf("template variables = %s", secret)
	}
	_ = name
	call(customerSession, "GET", "/api/teams/"+team.ID+"/resources/"+rid, nil, 200) // the team owns it

	// Experiences › Resources set to "assigned": teams can't create their own; assigning still works.
	pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ('instance', '{"name":"Ignition","resourceCreation":"assigned"}') ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	if e := call(customerSession, "POST", "/api/teams/"+team.ID+"/resources", map[string]any{"name": "own-" + suffix[:6], "type": "worker", "sourceType": "image", "image": "busybox"}, 403)["error"].(map[string]any); e["code"] != "resource_creation_disabled" {
		t.Fatalf("own resource while assigned-only = %v", e)
	}
	call(adminSession, "POST", "/api/platform/services", map[string]any{"productId": pid, "planId": small, "teamId": team.ID, "name": "more-" + suffix[:6]}, 201)
	pool.Exec(ctx, `DELETE FROM settings WHERE key = 'instance'`)

	// A product in use can't be deleted, nor its plans removed.
	call(adminSession, "DELETE", "/api/platform/products/"+pid, nil, 409)
	edit := map[string]any{"name": product["name"], "kind": product["kind"], "status": product["status"], "template": product["template"], "plans": plans[1:]}
	call(adminSession, "PUT", "/api/platform/products/"+pid, edit, 409)

	// Suspended: the team can't start or deploy it.
	call(adminSession, "POST", "/api/platform/services/"+sid+"/actions", map[string]any{"action": "suspend", "reason": "Unpaid invoice"}, 200)
	if e := call(customerSession, "POST", "/api/teams/"+team.ID+"/resources/"+rid+"/deployments", map[string]any{}, 403)["error"].(map[string]any); e["code"] != "service_suspended" {
		t.Fatalf("suspended deploy = %v", e)
	}
	if e := call(customerSession, "POST", "/api/teams/"+team.ID+"/resources/"+rid+"/actions", map[string]any{"action": "start"}, 403)["error"].(map[string]any); e["code"] != "service_suspended" || !strings.Contains(e["message"].(string), "Unpaid invoice") {
		t.Fatalf("suspended start = %v", e)
	}
	got := call(customerSession, "GET", "/api/teams/"+team.ID+"/resources/"+rid, nil, 200)["resource"].(map[string]any)
	if svc, _ := got["service"].(map[string]any); svc == nil || svc["status"] != "suspended" || svc["plan"] != "Small" {
		t.Fatalf("team sees service = %v", got["service"])
	}
	var queued int
	pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE resource_id = $1 AND kind = 'deploy' AND created_at > now() - interval '5 seconds'`, rid).Scan(&queued)
	_ = queued

	// Changing plan resizes the resource.
	call(adminSession, "POST", "/api/platform/services/"+sid+"/actions", map[string]any{"action": "change-plan", "planId": large}, 200)
	pool.QueryRow(ctx, `SELECT memory_mb FROM resources WHERE id = $1`, rid).Scan(&mem)
	if mem != 2048 {
		t.Fatalf("after change-plan memory = %d", mem)
	}
	if list := call(adminSession, "GET", "/api/platform/services?team="+team.ID+"&product="+pid, nil, 200)["services"].([]any); len(list) != 2 {
		t.Fatalf("services = %v", list)
	}
	// Settings left to the assigner, and what the team may change afterwards.
	custom := call(adminSession, "POST", "/api/platform/products", map[string]any{
		"name": "Your own image", "kind": "worker", "status": "active",
		"template": map[string]any{"sourceType": "image", "image": "busybox", "env": []any{map[string]any{"key": "MODE", "value": "prod"}},
			"_ask": []any{
				map[string]any{"key": "source", "kind": "source", "label": "Source", "path": "source", "default": map[string]any{"sourceType": "image", "image": "busybox"}},
				map[string]any{"key": "env.MODE", "kind": "select", "label": "Mode", "path": "env.MODE", "default": "prod", "options": []any{map[string]any{"value": "prod", "label": "Production"}, map[string]any{"value": "dev", "label": "Development"}}},
			},
			"_allow": []any{"env"}},
		"plans": []any{map[string]any{"name": "One", "cpus": 1, "memoryMb": 512}},
	}, 201)["product"].(map[string]any)
	cplan := custom["plans"].([]any)[0].(map[string]any)["id"].(string)
	assign := func(answers map[string]any, want int) map[string]any {
		return call(adminSession, "POST", "/api/platform/services", map[string]any{"productId": custom["id"], "planId": cplan, "teamId": team.ID, "name": "own-" + suffix[:6], "answers": answers}, want)
	}
	assign(map[string]any{"env.MODE": "staging"}, 422)
	assign(map[string]any{"source": map[string]any{"sourceType": "git", "repoUrl": "ftp://nope"}}, 422)
	own := assign(map[string]any{"source": map[string]any{"sourceType": "image", "image": "alpine:3.20"}, "env.MODE": "dev"}, 201)["service"].(map[string]any)
	ownID := own["resourceId"].(string)
	var image string
	pool.QueryRow(ctx, `SELECT image FROM resources WHERE id = $1`, ownID).Scan(&image)
	if image != "alpine:3.20" {
		t.Fatalf("answered image = %q", image)
	}
	if e := call(customerSession, "PATCH", "/api/teams/"+team.ID+"/resources/"+ownID, map[string]any{"image": "nginx"}, 403)["error"].(map[string]any); e["code"] != "managed_by_provider" {
		t.Fatalf("source change = %v", e)
	}
	call(customerSession, "PUT", "/api/teams/"+team.ID+"/resources/"+ownID+"/env", map[string]any{"variables": []any{map[string]any{"key": "MODE", "value": "dev"}}}, 200)

	// Bring your own files: a runtime the team fills with Files.
	call(adminSession, "POST", "/api/platform/products", map[string]any{"name": "Static site", "kind": "files", "status": "active",
		"template": map[string]any{"image": "nginx:1.27-alpine", "dataPath": "/usr/share/nginx/html", "port": 80},
		"plans":    []any{map[string]any{"name": "One", "cpus": 0.5, "memoryMb": 128}}}, 201)

	// Placement: stock, per-team limits and eligible servers.
	limited := call(adminSession, "POST", "/api/platform/products", map[string]any{"name": "Limited", "kind": "worker", "status": "active", "icon": "hero:CubeIcon:#4f7cff",
		"template":  map[string]any{"sourceType": "image", "image": "busybox"},
		"placement": map[string]any{"stock": 1, "strategy": "spread"},
		"plans":     []any{map[string]any{"name": "One", "cpus": 0.5, "memoryMb": 128}}}, 201)["product"].(map[string]any)
	lid, lplan := limited["id"].(string), limited["plans"].([]any)[0].(map[string]any)["id"].(string)
	av := call(adminSession, "GET", "/api/platform/products/"+lid+"/availability?team="+team.ID, nil, 200)
	if av["available"] != true || av["chosen"] == nil {
		t.Fatalf("availability = %v", av)
	}
	call(adminSession, "POST", "/api/platform/services", map[string]any{"productId": lid, "planId": lplan, "teamId": team.ID, "name": "lim-" + suffix[:6]}, 201)
	if e := call(adminSession, "POST", "/api/platform/services", map[string]any{"productId": lid, "planId": lplan, "teamId": team.ID, "name": "lim2-" + suffix[:6]}, 409)["error"].(map[string]any); !strings.Contains(e["message"].(string), "Sold out") {
		t.Fatalf("over stock = %v", e)
	}
	nowhere := call(adminSession, "POST", "/api/platform/products", map[string]any{"name": "Nowhere", "kind": "worker", "status": "active",
		"template":  map[string]any{"sourceType": "image", "image": "busybox"},
		"placement": map[string]any{"locations": []string{"mars"}},
		"plans":     []any{map[string]any{"name": "One", "cpus": 0.5, "memoryMb": 128}}}, 201)["product"].(map[string]any)
	call(adminSession, "POST", "/api/platform/services", map[string]any{"productId": nowhere["id"], "planId": nowhere["plans"].([]any)[0].(map[string]any)["id"], "teamId": team.ID, "name": "nw-" + suffix[:6]}, 409)
	call(adminSession, "POST", "/api/platform/products", map[string]any{"name": "Bad icon", "kind": "worker", "icon": "javascript:alert(1)",
		"template": map[string]any{"image": "busybox"}, "plans": []any{map[string]any{"name": "One", "cpus": 1, "memoryMb": 128}}}, 422)

	dup := call(adminSession, "POST", "/api/platform/products/"+pid+"/duplicate", nil, 201)["product"].(map[string]any)
	if dup["status"] != "draft" || len(dup["plans"].([]any)) != 2 {
		t.Fatalf("duplicate = %v", dup)
	}
	call(adminSession, "DELETE", "/api/platform/products/"+dup["id"].(string), nil, 204)
}

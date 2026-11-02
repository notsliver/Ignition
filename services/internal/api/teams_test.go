package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/notsliver/Ignition/services/internal/auth"
	"github.com/notsliver/Ignition/services/internal/database"
	"github.com/notsliver/Ignition/services/internal/store"
)

func TestTeamAPIUsesMembershipAndOptionalProjects(t *testing.T) {
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
	// Queue fixtures truncate jobs; serialize their lifetime with API queue assertions.
	lock, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lock.Exec(ctx, `SELECT pg_advisory_lock(8724312)`); err != nil {
		lock.Release()
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Exec(context.Background(), `SELECT pg_advisory_unlock(8724312)`); lock.Release() })
	s := &Server{Store: &store.Store{DB: pool}, PublicURL: "http://example.test"}
	policy := defaultPolicy()
	policy.BreachedPasswords = false
	s.setPolicy(policy)
	u, err := s.Store.CreateUser(ctx, "api-teams-"+time.Now().Format("150405.000000")+"@example.test", "Team API", "hash", "owner")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Store.CreateTeam(ctx, u.ID, "API A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Store.CreateTeam(ctx, u.ID, "API B")
	if err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, `INSERT INTO team_server_assignments(team_id,server_id) SELECT x,s.id FROM unnest($1::uuid[]) x CROSS JOIN servers s WHERE s.is_local`, []string{a.ID, b.ID})
	token, hash, _ := auth.NewToken()
	if err = s.Store.CreateSession(ctx, u.ID, hash, "test", "", time.Hour, "password"); err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	call := func(method, path string, input any, want int) map[string]any {
		t.Helper()
		var body bytes.Buffer
		if input != nil {
			json.NewEncoder(&body).Encode(input)
		}
		req := httptest.NewRequest(method, "http://example.test"+path, &body)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		req.Header.Set("Origin", "http://example.test")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		out := map[string]any{}
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	call("GET", "/api/resources", nil, 400)
	created := call("POST", "/api/teams/"+a.ID+"/resources", map[string]any{"name": "api-standalone", "type": "worker", "sourceType": "image", "image": "busybox"}, 201)
	resource := created["resource"].(map[string]any)
	id := resource["id"].(string)
	if resource["projectId"] != nil || resource["teamId"] != a.ID {
		t.Fatalf("standalone ownership: %v", resource)
	}
	// Issuer-less legacy credentials stay bound to one resource and its immutable team.
	service, serviceHash, _ := auth.NewToken()
	if _, err = s.Store.CreateToken(ctx, store.ResourceToken{ResourceID: id, Kind: "token", Scope: "read", Name: "legacy service"}, serviceHash, ""); err != nil {
		t.Fatal(err)
	}
	bearer := func(raw, path string, want int) {
		t.Helper()
		req := httptest.NewRequest("GET", "http://example.test"+path, nil)
		req.Header.Set("Authorization", "Bearer "+raw)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("credential %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	bearer(service, "/api/teams/"+a.ID+"/resources/"+id, 200)
	bearer(service, "/api/teams/"+b.ID+"/resources/"+id, 404)
	bearer(service, "/api/teams/"+a.ID+"/projects", 403)
	call("GET", "/api/teams/"+b.ID+"/resources/"+id, nil, 404)
	for _, team := range []struct {
		id        string
		resources float64
	}{{a.ID, 1}, {b.ID, 0}} {
		out := call("GET", "/api/teams/"+team.id+"/dashboard", nil, 200)
		if out["stats"].(map[string]any)["resources"] != team.resources {
			t.Fatalf("dashboard leaked between teams: %v", out)
		}
	}
	// A platform owner is only a viewer in B; platform authority never overrides membership.
	pool.Exec(ctx, `UPDATE team_memberships SET role='viewer' WHERE team_id=$1 AND user_id=$2`, b.ID, u.ID)
	call("POST", "/api/teams/"+b.ID+"/resources", map[string]any{"name": "denied"}, 403)
	call("DELETE", "/api/teams/"+a.ID+"/members/"+u.ID, nil, 409)
	project := call("POST", "/api/teams/"+a.ID+"/projects", map[string]string{"name": "optional-group"}, 201)["project"].(map[string]any)
	pid := project["id"].(string)
	call("PUT", "/api/teams/"+a.ID+"/resources/"+id+"/project", map[string]string{"projectId": pid}, 204)
	call("DELETE", "/api/teams/"+a.ID+"/projects/"+pid, nil, 204)
	out := call("GET", "/api/teams/"+a.ID+"/resources/"+id, nil, 200)["resource"].(map[string]any)
	if out["projectId"] != nil {
		t.Fatal("deleting a grouping did not detach its workload")
	}
	var queuedTeam string
	if err = pool.QueryRow(ctx, `SELECT team_id FROM jobs WHERE resource_id=$1`, id).Scan(&queuedTeam); err != nil || queuedTeam != a.ID {
		t.Fatalf("queued ownership: %s %v", queuedTeam, err)
	}
	pool.Exec(ctx, `DELETE FROM team_memberships WHERE team_id=$1 AND user_id=$2`, b.ID, u.ID)
	// Without a membership, a platform administrator works in the team as its owner, and it's audited.
	if info := call("GET", "/api/teams/"+b.ID, nil, 200)["team"].(map[string]any); info["role"] != "owner" || info["adminAccess"] != true {
		t.Fatalf("admin access team = %v", info)
	}
	call("GET", "/api/teams/"+b.ID+"/resources", nil, 200)
	var audited int
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='team.admin_access' AND target_id=$1`, b.ID).Scan(&audited)
	if audited != 1 {
		t.Fatalf("admin access audited %d times, want 1", audited)
	}
	pool.Exec(ctx, `UPDATE users SET role='developer' WHERE id=$1`, u.ID)
	denied := call("GET", "/api/teams/"+b.ID+"/resources", nil, 403)
	if denied["error"].(map[string]any)["code"] != "team_access_lost" {
		t.Fatal("membership revocation is not recognizable by the client")
	}
	pool.Exec(ctx, `UPDATE users SET role='owner' WHERE id=$1`, u.ID)
	// Registering from a team invitation never grants an instance administrator role.
	invite := call("POST", "/api/teams/"+a.ID+"/invitations", map[string]string{"email": "invite-" + u.ID + "@example.test", "role": "admin"}, 201)
	inviteToken := invite["token"].(string)
	call("GET", "/api/team-invitations/"+inviteToken, nil, 200)
	registered := call("POST", "/api/team-invitations/"+inviteToken+"/register", map[string]string{"name": "Invited administrator", "password": "fresh-unique-test-passphrase-34987"}, 200)["user"].(map[string]any)
	invitedID := registered["id"].(string)
	if registered["role"] != "viewer" {
		t.Fatal("team invitation granted platform authority")
	}
	member, err := s.Store.Membership(ctx, a.ID, invitedID)
	if err != nil || member.Role != "admin" {
		t.Fatalf("invited team role: %v %v", member, err)
	}
	call("POST", "/api/team-invitations/"+inviteToken+"/register", map[string]string{"name": "Replay", "password": "fresh-unique-test-passphrase-34987"}, 404)
	// Existing identities join without creating a duplicate global account.
	existing, err := s.Store.CreateUser(ctx, "existing-"+u.ID+"@example.test", "Existing", "hash", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	invitation, hash, _ := auth.NewToken()
	if _, err = s.Store.InviteTeamMember(ctx, a.ID, existing.Email, "member", u.ID, hash); err != nil {
		t.Fatal(err)
	}
	if joined, err := s.Store.AcceptTeamInvitation(ctx, existing.ID, existing.Email, auth.HashToken(invitation)); err != nil || joined != a.ID {
		t.Fatalf("existing identity did not join: %v", err)
	}
	list, err := s.Store.Teams(ctx, existing.ID)
	if err != nil || len(list) != 1 {
		t.Fatal("existing identity joined an unintended default team")
	}
	if err = s.Store.SetTeamMember(ctx, a.ID, existing.ID, "developer", store.PermOverrides{}); err != nil {
		t.Fatal(err)
	}
	issued, issuedHash, _ := auth.NewToken()
	if _, err = s.Store.CreateToken(ctx, store.ResourceToken{ResourceID: id, Kind: "token", Scope: "deploy", Name: "member token"}, issuedHash, existing.ID); err != nil {
		t.Fatal(err)
	}
	bearer(issued, "/api/teams/"+a.ID+"/resources/"+id, 200)
	if err = s.Store.SetTeamMember(ctx, a.ID, existing.ID, "", store.PermOverrides{}); err != nil {
		t.Fatal(err)
	}
	bearer(issued, "/api/teams/"+a.ID+"/resources/"+id, 401)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM resources WHERE team_id=ANY($1::uuid[])`, []string{a.ID, b.ID})
		pool.Exec(ctx, `DELETE FROM audit_events WHERE team_id=ANY($1::uuid[])`, []string{a.ID, b.ID})
		pool.Exec(ctx, `DELETE FROM project_environments WHERE team_id=ANY($1::uuid[])`, []string{a.ID, b.ID})
		pool.Exec(ctx, `DELETE FROM environments WHERE team_id=ANY($1::uuid[])`, []string{a.ID, b.ID})
		pool.Exec(ctx, `DELETE FROM team_experiences WHERE team_id=ANY($1::uuid[])`, []string{a.ID, b.ID})
		pool.Exec(ctx, `DELETE FROM team_server_assignments WHERE team_id=ANY($1::uuid[])`, []string{a.ID, b.ID})
		pool.Exec(ctx, `DELETE FROM team_invitations WHERE team_id=ANY($1::uuid[])`, []string{a.ID, b.ID})
		pool.Exec(ctx, `DELETE FROM team_memberships WHERE team_id=ANY($1::uuid[])`, []string{a.ID, b.ID})
		pool.Exec(ctx, `DELETE FROM teams WHERE id=ANY($1::uuid[])`, []string{a.ID, b.ID})
		s.Store.DeleteUser(ctx, u.ID)
		s.Store.DeleteUser(ctx, invitedID)
		s.Store.DeleteUser(ctx, existing.ID)
	})
}

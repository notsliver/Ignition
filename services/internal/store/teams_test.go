package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/notsliver/Ignition/services/internal/database"
)

func TestTeamOwnershipAndProjectAssignments(t *testing.T) {
	url := os.Getenv("IGNITION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IGNITION_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: pool}
	u, err := s.CreateUser(ctx, "team-test-"+time.Now().Format("150405.000000")+"@example.test", "Owner", "hash", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateTeam(ctx, u.ID, "Team A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateTeam(ctx, u.ID, "Team B")
	if err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, `INSERT INTO team_server_assignments(team_id,server_id) SELECT x, s.id FROM unnest($1::uuid[]) x CROSS JOIN servers s WHERE s.is_local`, []string{a.ID, b.ID})
	scoped := func(team, permission string, rank int) context.Context {
		return WithScope(ctx, Scope{TeamID: team, UserID: u.ID, Permission: permission, MinRank: rank})
	}
	ac, bc := scoped(a.ID, "resources.view", 1), scoped(b.ID, "resources.view", 1)
	// Team-wide usage (the dashboard without server access) passes no project at all.
	if _, _, err := s.ProjectUsage(ac, "", "", time.Hour, time.Minute); err != nil {
		t.Fatalf("team-wide usage: %v", err)
	}
	pa, err := s.CreateProject(ac, u.ID, "same-name", "")
	if err != nil {
		t.Fatal(err)
	}
	pb, err := s.CreateProject(bc, u.ID, "same-name", "")
	if err != nil {
		t.Fatal(err)
	}
	var extraID string
	defer func() {
		pool.Exec(ctx, `DELETE FROM resources WHERE team_id=ANY($1::uuid[])`, []string{a.ID, b.ID})
		s.DeleteProject(ctx, pa)
		s.DeleteProject(ctx, pb)
		pool.Exec(ctx, `DELETE FROM project_environments WHERE team_id=ANY($1::uuid[])`, []string{a.ID, b.ID})
		pool.Exec(ctx, `DELETE FROM environments WHERE team_id=ANY($1::uuid[])`, []string{a.ID, b.ID})
		pool.Exec(ctx, `DELETE FROM team_experiences WHERE team_id=ANY($1::uuid[])`, []string{a.ID, b.ID})
		pool.Exec(ctx, `DELETE FROM team_server_assignments WHERE team_id=ANY($1::uuid[])`, []string{a.ID, b.ID})
		pool.Exec(ctx, `DELETE FROM team_memberships WHERE team_id=ANY($1::uuid[])`, []string{a.ID, b.ID})
		pool.Exec(ctx, `DELETE FROM teams WHERE id=ANY($1::uuid[])`, []string{a.ID, b.ID})
		s.DeleteUser(ctx, u.ID)
		if extraID != "" {
			s.DeleteUser(ctx, extraID)
		}
	}()
	if _, err = s.GetProject(ac, u.ID, pb); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-team project exposed: %v", err)
	}
	ea, err := s.DefaultEnvironment(ac)
	if err != nil {
		t.Fatal(err)
	}
	eb, err := s.DefaultEnvironment(bc)
	if err != nil {
		t.Fatal(err)
	}
	create := func(c context.Context, project, env, name string) (string, error) {
		tx, err := s.SQL(c).Begin(c)
		if err != nil {
			return "", err
		}
		defer tx.Rollback(c)
		id, err := s.CreateResource(c, tx, NewResource{ProjectID: project, EnvironmentID: env, Name: name, Type: "worker", SourceType: "image", Image: "busybox", MemoryMB: 128, CPUs: 1})
		if err != nil {
			return "", err
		}
		return id, tx.Commit(c)
	}
	ra, err := create(ac, "", ea, "standalone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = create(ac, pa, eb, "cross-team"); err == nil {
		t.Fatal("cross-team environment accepted")
	}
	if _, err = s.GetResource(bc, ra); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-team resource exposed: %v", err)
	}
	res, err := s.GetResource(ac, ra)
	if err != nil || res.ProjectID != "" || res.TeamID != a.ID {
		t.Fatalf("standalone resource: %+v %v", res, err)
	}
	for i := 0; i < 6; i++ {
		list, err := s.ListAllResources(bc, "", "")
		if err != nil || len(list) != 0 {
			t.Fatalf("pool leaked A into B: %v %v", list, err)
		}
		if _, err = s.GetResource(ac, ra); err != nil {
			t.Fatal(err)
		}
	}
	grouped, err := create(ac, pa, ea, "grouped")
	if err != nil {
		t.Fatal(err)
	}
	// A low baseline can gain authority in one project, never in standalone resources.
	pool.Exec(ctx, `UPDATE team_memberships SET role='member' WHERE team_id=$1 AND user_id=$2`, a.ID, u.ID)
	if err = s.SetProjectAssignment(ctx, a.ID, pa, u.ID, "developer", PermOverrides{}); err != nil {
		t.Fatal(err)
	}
	pc := scoped(a.ID, "resources.update", 2)
	if _, err = s.GetResource(pc, grouped); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetResource(pc, ra); !errors.Is(err, ErrNotFound) {
		t.Fatal("project grant authorized a standalone resource")
	}
	pool.Exec(ctx, `UPDATE project_assignments SET permissions='{"deny":["resources.update"]}' WHERE project_id=$1`, pa)
	if _, err = s.GetResource(pc, grouped); !errors.Is(err, ErrNotFound) {
		t.Fatal("project denial did not override role")
	}
	pool.Exec(ctx, `UPDATE team_memberships SET role='owner' WHERE team_id=$1 AND user_id=$2`, a.ID, u.ID)
	if err = s.SetTeamMember(ctx, a.ID, u.ID, "", PermOverrides{}); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("last owner removal: %v", err)
	}
	if err = s.DeleteProject(ac, pa); err != nil {
		t.Fatal(err)
	}
	if res, err = s.GetResource(ac, grouped); err != nil || res.ProjectID != "" {
		t.Fatalf("project deletion removed workload: %+v %v", res, err)
	}
	if err = s.SetUserStatus(ctx, u.ID, "suspended"); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("suspension orphaned team ownership: %v", err)
	}
	second, err := s.CreateUser(ctx, "concurrent-owner-"+u.ID+"@example.test", "Second owner", "hash", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	extraID = second.ID
	if _, err = pool.Exec(ctx, `INSERT INTO team_memberships(team_id,user_id,role) VALUES($1,$2,'owner')`, a.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, id := range []string{u.ID, second.ID} {
		go func(id string) { <-start; results <- s.SetTeamMember(ctx, a.ID, id, "viewer", PermOverrides{}) }(id)
	}
	close(start)
	blocked := 0
	for i := 0; i < 2; i++ {
		err := <-results
		if errors.Is(err, ErrLastOwner) {
			blocked++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if blocked != 1 {
		t.Fatalf("concurrent demotions left no owner: %d blocked", blocked)
	}
	// Merely using a scoped request role without a team must fail closed.
	tx, _ := pool.Begin(ctx)
	defer tx.Rollback(ctx)
	tx.Exec(ctx, `SET LOCAL ROLE ignition_request`)
	var n int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM resources`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("missing scope exposed resources: %d %v", n, err)
	}
}

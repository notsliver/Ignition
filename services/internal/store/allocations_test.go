package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/notsliver/Ignition/services/internal/database"
)

func TestServerVisibilityAndAllocations(t *testing.T) {
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
	var server string
	if err = pool.QueryRow(ctx, `INSERT INTO servers (name, connection_method, endpoint) VALUES ($1, 'docker-tls', 'tcp://203.0.113.9:2376') RETURNING id`, "vis-"+time.Now().Format("150405.000000")).Scan(&server); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM servers WHERE id = $1`, server)
	count := func() (n int) {
		pool.QueryRow(ctx, `SELECT count(*) FROM team_server_assignments WHERE server_id = $1`, server).Scan(&n)
		return
	}
	var teams int
	pool.QueryRow(ctx, `SELECT count(*) FROM teams`).Scan(&teams)
	if count() != teams {
		t.Fatalf("new visible server assigned to %d of %d teams", count(), teams)
	}
	if err = s.SetServerVisible(ctx, server, false); err != nil || count() != 0 {
		t.Fatalf("hidden server still assigned to %d teams (%v)", count(), err)
	}
	if err = s.SetServerVisible(ctx, server, true); err != nil || count() != teams {
		t.Fatalf("shown again: %d of %d teams (%v)", count(), teams, err)
	}

	added, err := s.AddAllocations(ctx, server, "203.0.113.9", "play.example.test", []int{25565, 25566})
	if err != nil || added != 2 {
		t.Fatalf("add = %d, %v", added, err)
	}
	if again, _ := s.AddAllocations(ctx, server, "203.0.113.9", "", []int{25565}); again != 0 {
		t.Fatalf("duplicate added")
	}
	if !s.ServerHasAllocations(ctx, server) {
		t.Fatal("server has allocations")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	// Claiming needs a resource to hold the allocation.
	var resource string
	if err = tx.QueryRow(ctx, `INSERT INTO resources (environment_id, name, type, source_type, image)
		SELECT id, $1, 'application', 'image', 'nginx' FROM environments WHERE is_team_default LIMIT 1 RETURNING id`, "alloc-"+time.Now().Format("150405000000")).Scan(&resource); err != nil {
		t.Fatal(err)
	}
	a, err := s.ClaimAllocation(ctx, tx, server, resource)
	if err != nil || a.Port != 25565 {
		t.Fatalf("claim = %+v, %v", a, err)
	}
	b, err := s.ClaimAllocation(ctx, tx, server, resource)
	if err != nil || b.Port != 25566 {
		t.Fatalf("second claim = %+v, %v", b, err)
	}
	if _, err = s.ClaimAllocation(ctx, tx, server, resource); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pool exhausted should be ErrNotFound, got %v", err)
	}
}

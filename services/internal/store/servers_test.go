package store

// Integration test; runs when IGNITION_TEST_DATABASE_URL points at a disposable Postgres.

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/notsliver/Ignition/services/internal/database"
)

func TestServerStatusFiltersAndDeploymentGate(t *testing.T) {
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
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: pool}

	local, err := s.LocalServer(ctx)
	if err != nil {
		t.Fatalf("migration must create the local server: %v", err)
	}
	defer s.SetServerDeployments(ctx, local.ID, true)
	defer s.SetServerMaintenance(ctx, local.ID, false, "", "")

	// A remote server reported online, but its last check is two minutes old: it must read as
	// "unknown", count as needing attention, never as available, and its old sample isn't current.
	suffix := time.Now().Format("150405.000000")
	id, err := s.CreateRemoteServer(ctx, NewServer{Name: "stale-" + suffix, Endpoint: "stale-" + suffix + ".example:2376", CACert: "x", ClientCert: "x", TLSFingerprint: "ab",
		ClientKeySealer: func(string) ([]byte, error) { return []byte("sealed"), nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.DeleteServer(ctx, id)
	pool.Exec(ctx, `UPDATE servers SET last_check_at = now() - interval '2 minutes', connection_state = 'online', runtime_state = 'ready' WHERE id = $1`, id)
	cpu := 42.0
	s.InsertServerMetric(ctx, ServerMetricSample{ServerID: id, At: time.Now().Add(-30 * time.Second), CPUPercent: &cpu})

	srv, err := s.GetServer(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if srv.EffectiveState != "unknown" || !srv.Stale || srv.Metrics != nil {
		t.Fatalf("stale server: state %s stale %v metrics %+v", srv.EffectiveState, srv.Stale, srv.Metrics)
	}
	list, total, err := s.ListServers(ctx, ServerQuery{Q: "stale-" + suffix, Filter: "attention"})
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("attention filter: %d %v", total, err)
	}
	if _, total, _ := s.ListServers(ctx, ServerQuery{Q: "stale-" + suffix, Filter: "online"}); total != 0 {
		t.Fatal("a stale server must not be listed as online")
	}
	// Remote servers are never deployment targets.
	if _, total, _ := s.ListServers(ctx, ServerQuery{Q: "stale-" + suffix, Filter: "deployable"}); total != 0 {
		t.Fatal("remote servers can't be deployable")
	}
	// A fresh check makes it online and its sample current.
	s.RecordServerHealth(ctx, id, HealthUpdate{ConnectionState: "online", RuntimeState: "ready", Success: true})
	srv, _ = s.GetServer(ctx, id)
	if srv.EffectiveState != "online" || srv.Metrics == nil || *srv.Metrics.CPUPercent != 42 {
		t.Fatalf("fresh server: %s %+v", srv.EffectiveState, srv.Metrics)
	}
	for _, sort := range []string{"name", "status", "lastSeen", "workloads", "cpu", "memory", "disk"} {
		if _, _, err := s.ListServers(ctx, ServerQuery{Sort: sort, Desc: true}); err != nil {
			t.Fatalf("sort %s: %v", sort, err)
		}
	}

	// Deployment gate: maintenance or disabled deployments refuse new deployments on the local server.
	projectID, err := s.CreateProject(ctx, "", "servers-test-"+suffix, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DeleteProject(ctx, projectID)
	var envID string
	pool.QueryRow(ctx, `SELECT id FROM environments WHERE project_id = $1`, projectID).Scan(&envID)
	tx, _ := pool.Begin(ctx)
	resID, err := s.CreateResource(ctx, tx, NewResource{ProjectID: projectID, EnvironmentID: envID, Name: "gate", Type: "worker", SourceType: "image", Image: "busybox", MemoryMB: 128, CPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	tx.Commit(ctx)
	res, _ := s.GetResource(ctx, resID)
	if res.ServerID != local.ID {
		t.Fatalf("new resources belong to the local server, got %q", res.ServerID)
	}

	deploy := func() error {
		tx, _ := pool.Begin(ctx)
		defer tx.Rollback(ctx)
		_, err := s.CreateDeployment(ctx, tx, resID, "manual", "", "", "", "")
		return err
	}
	var blocked *DeploymentsBlocked
	if changed, _ := s.SetServerMaintenance(ctx, local.ID, true, "patching", ""); !changed {
		t.Fatal("entering maintenance should change state")
	}
	if err := deploy(); !errors.As(err, &blocked) {
		t.Fatalf("maintenance must block deployments, got %v", err)
	}
	s.SetServerMaintenance(ctx, local.ID, false, "", "")
	s.SetServerDeployments(ctx, local.ID, false)
	if err := deploy(); !errors.As(err, &blocked) {
		t.Fatalf("disabled deployments must block, got %v", err)
	}
	s.SetServerDeployments(ctx, local.ID, true)
	if err := deploy(); err != nil {
		t.Fatalf("eligible server should accept deployments: %v", err)
	}

	// The local server can't be deleted, and a server with resources can't be either.
	s.DeleteServer(ctx, local.ID)
	if _, err := s.LocalServer(ctx); err != nil {
		t.Fatal("the local server must survive DeleteServer")
	}
}

package store

// Integration test; runs when IGNITION_TEST_DATABASE_URL points at a disposable Postgres.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/notsliver/Ignition/services/internal/database"
)

func TestMetricSeriesBucketsAndGaps(t *testing.T) {
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

	projectID, err := s.CreateProject(ctx, "", "metrics-test-"+time.Now().Format("150405.000000"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DeleteProject(ctx, projectID)
	var envID, resID string
	pool.QueryRow(ctx, `SELECT id FROM environments WHERE project_id = $1`, projectID).Scan(&envID)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) // no-op after Commit; releases the connection if the insert fails
	resID, err = s.CreateResource(ctx, tx, NewResource{ProjectID: projectID, EnvironmentID: envID, Name: "m", Type: "application", SourceType: "image", Image: "nginx", MemoryMB: 128, CPUs: 1})
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// Two samples in the bucket two minutes ago, nothing in the previous minute (a gap).
	base := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	rx := 100.0
	for i, cpu := range []float64{10, 30} {
		if err := s.InsertMetric(ctx, MetricSample{ResourceID: resID, At: base.Add(time.Duration(i*15) * time.Second), CPUPercent: cpu, MemoryBytes: 1000, MemoryLimit: 4000, NetRxBps: &rx}); err != nil {
			t.Fatal(err)
		}
	}
	// An old sample that pruning must remove.
	s.InsertMetric(ctx, MetricSample{ResourceID: resID, At: time.Now().Add(-8 * 24 * time.Hour), CPUPercent: 99, MemoryBytes: 1, MemoryLimit: 1})

	buckets, err := s.MetricSeries(ctx, resID, 15*time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) < 15 || len(buckets) > 17 {
		t.Fatalf("expected ~16 evenly spaced buckets, got %d", len(buckets))
	}
	var hit, gaps int
	for _, b := range buckets {
		if b.At.Equal(base) {
			hit++
			if b.Samples != 2 || *b.CPUAvg != 20 || *b.CPUMax != 30 || *b.NetRxAvg != 100 {
				t.Fatalf("wrong aggregation: %+v", b)
			}
		} else if b.Samples == 0 {
			gaps++
			if b.CPUAvg != nil {
				t.Fatal("empty buckets must have null values, not zero")
			}
		}
	}
	if hit != 1 || gaps < 10 {
		t.Fatalf("expected one filled bucket and gaps elsewhere, got hit=%d gaps=%d", hit, gaps)
	}

	if n, err := s.PruneMetrics(ctx, 7*24*time.Hour); err != nil || n < 1 {
		t.Fatalf("expected the old sample to be pruned, got %d (%v)", n, err)
	}
}

func TestListAllResourcesWithUsage(t *testing.T) {
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
	database.Migrate(ctx, pool)
	s := &Store{DB: pool}
	projectID, err := s.CreateProject(ctx, "", "inventory-test-"+time.Now().Format("150405.000000"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DeleteProject(ctx, projectID)
	var envID string
	pool.QueryRow(ctx, `SELECT id FROM environments WHERE project_id = $1`, projectID).Scan(&envID)
	tx, _ := pool.Begin(ctx)
	defer tx.Rollback(ctx)
	idle, _ := s.CreateResource(ctx, tx, NewResource{ProjectID: projectID, EnvironmentID: envID, Name: "idle", Type: "worker", SourceType: "image", Image: "busybox", MemoryMB: 64, CPUs: 1})
	busy, err := s.CreateResource(ctx, tx, NewResource{ProjectID: projectID, EnvironmentID: envID, Name: "busy", Type: "application", SourceType: "image", Image: "nginx", MemoryMB: 64, CPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	tx.Commit(ctx)
	s.InsertMetric(ctx, MetricSample{ResourceID: busy, At: time.Now().Add(-5 * time.Second), CPUPercent: 12.5, MemoryBytes: 2048, MemoryLimit: 4096})
	s.InsertMetric(ctx, MetricSample{ResourceID: idle, At: time.Now().Add(-10 * time.Minute), CPUPercent: 50, MemoryBytes: 1, MemoryLimit: 1}) // stale

	list, err := s.ListAllResources(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]Resource{}
	for _, r := range list {
		if r.ProjectID == projectID {
			found[r.Name] = r
		}
	}
	if found["busy"].Usage == nil || found["busy"].Usage.CPUPercent != 12.5 || found["busy"].ProjectName == "" {
		t.Fatalf("expected fresh usage and project name, got %+v", found["busy"])
	}
	if found["idle"].Usage != nil {
		t.Fatal("samples older than a minute must not be reported as current usage")
	}
	filtered, _ := s.ListAllResources(ctx, "bus", "application")
	if len(filtered) != 1 || filtered[0].Name != "busy" {
		t.Fatalf("search and type filter failed: %d results", len(filtered))
	}
}

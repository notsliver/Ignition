package store

// Integration test; runs when IGNITION_TEST_DATABASE_URL points at a disposable Postgres.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ignition/ignition/internal/database"
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

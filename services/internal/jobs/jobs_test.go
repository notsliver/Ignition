package jobs

// Integration tests against a real PostgreSQL. Set IGNITION_TEST_DATABASE_URL to run them
// (make test does this with a throwaway Postgres container); otherwise they're skipped.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ignition/ignition/internal/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testQueue(t *testing.T) *Queue {
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
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE jobs CASCADE`); err != nil {
		t.Fatal(err)
	}
	return &Queue{DB: pool}
}

func enqueue(t *testing.T, db *pgxpool.Pool, max int) string {
	id, err := Enqueue(context.Background(), db, NewJob{Kind: "test", Payload: map[string]int{"n": 1}, MaxAttempts: max})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func status(t *testing.T, db *pgxpool.Pool, id string) (s string, attempts int) {
	db.QueryRow(context.Background(), `SELECT status, attempts FROM jobs WHERE id = $1`, id).Scan(&s, &attempts)
	return
}

func TestClaimIsExclusiveUnderConcurrency(t *testing.T) {
	q := testQueue(t)
	for i := 0; i < 20; i++ {
		enqueue(t, q.DB, 3)
	}
	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for {
				j, err := q.Claim(context.Background(), fmt.Sprintf("w%d", w), time.Minute)
				if err != nil {
					t.Error(err)
					return
				}
				if j == nil {
					return
				}
				mu.Lock()
				seen[j.ID]++
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	if len(seen) != 20 {
		t.Fatalf("expected 20 distinct jobs, got %d", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("job %s was claimed %d times", id, n)
		}
	}
}

func TestExpiredLeaseIsRecovered(t *testing.T) {
	q := testQueue(t)
	id := enqueue(t, q.DB, 3)
	if j, _ := q.Claim(context.Background(), "crashed-worker", time.Minute); j == nil || j.ID != id {
		t.Fatal("expected to claim the job")
	}
	if j, _ := q.Claim(context.Background(), "other", time.Minute); j != nil {
		t.Fatal("a job with a live lease must not be reclaimed")
	}
	// Simulate the worker dying: its lease runs out.
	q.DB.Exec(context.Background(), `UPDATE jobs SET lease_until = now() - interval '1 second' WHERE id = $1`, id)
	j, _ := q.Claim(context.Background(), "other", time.Minute)
	if j == nil || j.ID != id || j.Attempts != 2 {
		t.Fatalf("expected recovery as attempt 2, got %+v", j)
	}
	// The original worker can no longer heartbeat it.
	if stop, _ := q.Heartbeat(context.Background(), id, "crashed-worker", time.Minute); !stop {
		t.Fatal("a worker that lost the lease must be told to stop")
	}
}

func TestRetryThenFinalFailure(t *testing.T) {
	q := testQueue(t)
	id := enqueue(t, q.DB, 2)
	j, _ := q.Claim(context.Background(), "w", time.Minute)
	final, err := q.Fail(context.Background(), j, errors.New("flaky network"))
	if err != nil || final {
		t.Fatalf("first failure should be retried (final=%v, err=%v)", final, err)
	}
	if s, _ := status(t, q.DB, id); s != "pending" {
		t.Fatalf("expected pending, got %s", s)
	}
	if j, _ := q.Claim(context.Background(), "w", time.Minute); j != nil {
		t.Fatal("a retry must wait for its backoff")
	}
	q.DB.Exec(context.Background(), `UPDATE jobs SET run_after = now() WHERE id = $1`, id)
	j, _ = q.Claim(context.Background(), "w", time.Minute)
	final, _ = q.Fail(context.Background(), j, errors.New("still broken"))
	if !final {
		t.Fatal("the last attempt must fail permanently")
	}
	if s, attempts := status(t, q.DB, id); s != "failed" || attempts != 2 {
		t.Fatalf("expected failed after 2 attempts, got %s/%d", s, attempts)
	}
}

func TestPermanentErrorsSkipRetries(t *testing.T) {
	q := testQueue(t)
	id := enqueue(t, q.DB, 5)
	j, _ := q.Claim(context.Background(), "w", time.Minute)
	final, _ := q.Fail(context.Background(), j, fmt.Errorf("%w: bad config", ErrPermanent))
	if s, _ := status(t, q.DB, id); !final || s != "failed" {
		t.Fatalf("permanent errors must not retry, got %s", s)
	}
}

func TestCancellation(t *testing.T) {
	q := testQueue(t)
	pending := enqueue(t, q.DB, 3)
	if s, _ := RequestCancel(context.Background(), q.DB, pending); s != "cancelled" {
		t.Fatalf("pending jobs cancel immediately, got %q", s)
	}
	running := enqueue(t, q.DB, 3)
	q.Claim(context.Background(), "w", time.Minute)
	if s, _ := RequestCancel(context.Background(), q.DB, running); s != "running" {
		t.Fatalf("running jobs stay running until the worker notices, got %q", s)
	}
	if stop, _ := q.Heartbeat(context.Background(), running, "w", time.Minute); !stop {
		t.Fatal("the heartbeat must report the cancellation")
	}
	if s, _ := RequestCancel(context.Background(), q.DB, pending); s != "" {
		t.Fatal("finished jobs can't be cancelled again")
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	q := testQueue(t)
	if err := database.Migrate(context.Background(), q.DB); err != nil {
		t.Fatalf("re-running migrations must be a no-op: %v", err)
	}
}

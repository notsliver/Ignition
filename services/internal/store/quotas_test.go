package store

import (
	"context"
	"os"
	"testing"

	"github.com/notsliver/Ignition/services/internal/database"
)

func TestQuotaUsageAndLegacyLimit(t *testing.T) {
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
	defer pool.Exec(ctx, `DELETE FROM settings WHERE key IN ('quotas', 'limits')`)

	// Before quotas are saved, the old services-per-team limit carries over.
	pool.Exec(ctx, `DELETE FROM settings WHERE key = 'quotas'`)
	if err = s.SetSetting(ctx, "limits", map[string]any{"resourcesPerTeam": 7}); err != nil {
		t.Fatal(err)
	}
	if q := s.Quotas(ctx); q.Team.Services != 7 || q.Teams == nil {
		t.Fatalf("legacy quotas = %+v", q)
	}
	if err = s.SetQuotas(ctx, Quotas{Team: Quota{GameServers: 2}}); err != nil {
		t.Fatal(err)
	}
	if q := s.Quotas(ctx); q.Team.Services != 0 || q.Team.GameServers != 2 {
		t.Fatalf("saved quotas = %+v", q)
	}

	// Every scope's query runs, and an unknown scope holds nothing.
	for _, c := range [][2]string{{"instance", ""}, {"team", "00000000-0000-0000-0000-000000000000"}, {"user", "00000000-0000-0000-0000-000000000000"}, {"location", "nowhere"}} {
		u, err := s.QuotaUsage(ctx, c[0], c[1])
		if err != nil {
			t.Fatalf("%s usage: %v", c[0], err)
		}
		if c[0] != "instance" && (u.Services != 0 || u.Ports != 0 || len(u.ResourceIDs) != 0) {
			t.Fatalf("%s usage = %+v", c[0], u)
		}
	}
}

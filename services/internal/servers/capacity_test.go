package servers

import (
	"strings"
	"testing"

	"github.com/notsliver/Ignition/services/internal/store"
)

func f(v float64) *float64 { return &v }

func TestAssess(t *testing.T) {
	const gib = 1 << 30
	hot := Assess(&store.ServerCapacity{Samples: 1000, CPUP95: f(92), MemoryP95: f(3 * gib), CommittedMemory: 6 * gib, DiskUsed: f(50), DiskTotal: f(100), DiskGrowthPerDay: f(5)}, 4, 4*gib)
	if hot.Status != "constrained" || *hot.MemoryCommitPct != 150 || *hot.MemoryP95Pct != 75 || *hot.DaysUntilDiskFull != 10 {
		t.Fatalf("hot: %+v", hot)
	}
	joined := strings.Join(hot.Findings, " ")
	if !strings.Contains(joined, "CPU") || !strings.Contains(joined, "fills in about 10 days") || !strings.Contains(joined, "150%") {
		t.Fatalf("findings: %v", hot.Findings)
	}
	idle := Assess(&store.ServerCapacity{Samples: 1000, CPUP95: f(3), MemoryP95: f(gib)}, 8, 8*gib)
	if idle.Status != "underused" {
		t.Fatalf("idle: %+v", idle)
	}
	// Too little history never calls a server underused.
	young := Assess(&store.ServerCapacity{Samples: 20, CPUP95: f(3), MemoryP95: f(gib)}, 8, 8*gib)
	if young.Status != "ok" {
		t.Fatalf("young: %+v", young)
	}
	if u := Assess(&store.ServerCapacity{}, 4, 4*gib); u.Status != "unknown" {
		t.Fatalf("no samples: %+v", u)
	}
	if e := Assess(&store.ServerCapacity{Samples: 100}, 4, 4*gib); e.Status != "unknown" || !strings.Contains(e.Findings[0], "host probe") {
		t.Fatalf("engine only: %+v", e)
	}
}

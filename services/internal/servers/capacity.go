package servers

import (
	"fmt"
	"math"

	"github.com/notsliver/Ignition/services/internal/store"
)

// Capacity thresholds. p95 over the window, so a short spike doesn't flag a server but sustained
// pressure does.
const (
	CPUHot       = 85.0 // % p95
	MemoryHot    = 90.0 // % p95 of total
	DiskHot      = 85.0 // % used now
	DiskSoonDays = 14.0 // projected days until the disk fills
	CPUIdle      = 10.0
	MemoryIdle   = 30.0
	// MinSamples is about two hours at the 15 s interval: less history says nothing about trends.
	MinSamples = 480
)

// Assessment is one server's capacity picture: what it has, what's promised to workloads, what
// it actually uses, and what that means.
type Assessment struct {
	*store.ServerCapacity
	Cores             int      `json:"cores"`
	MemoryCapacity    int64    `json:"memoryCapacity"`
	CPUCommitPct      *float64 `json:"cpuCommitPct"`    // committed CPUs as % of cores
	MemoryCommitPct   *float64 `json:"memoryCommitPct"` // committed memory limits as % of total
	MemoryP95Pct      *float64 `json:"memoryP95Pct"`
	DiskPct           *float64 `json:"diskPct"`
	DaysUntilDiskFull *float64 `json:"daysUntilDiskFull"`
	Status            string   `json:"status"` // constrained | watch | ok | underused | unknown
	Findings          []string `json:"findings"`
}

func pct(a, b float64) *float64 {
	if b <= 0 {
		return nil
	}
	v := math.Round(a/b*1000) / 10
	return &v
}

func Assess(c *store.ServerCapacity, cores int, memTotal int64) Assessment {
	a := Assessment{ServerCapacity: c, Cores: cores, MemoryCapacity: memTotal, Findings: []string{}}
	if memTotal == 0 && c.MemoryTotal != nil {
		a.MemoryCapacity = int64(*c.MemoryTotal)
	}
	if cores > 0 {
		a.CPUCommitPct = pct(c.CommittedCPUs, float64(cores))
	}
	if a.MemoryCapacity > 0 {
		a.MemoryCommitPct = pct(float64(c.CommittedMemory), float64(a.MemoryCapacity))
		if c.MemoryP95 != nil {
			a.MemoryP95Pct = pct(*c.MemoryP95, float64(a.MemoryCapacity))
		}
	}
	if c.DiskUsed != nil && c.DiskTotal != nil {
		a.DiskPct = pct(*c.DiskUsed, *c.DiskTotal)
		if c.DiskGrowthPerDay != nil && *c.DiskGrowthPerDay > 0 {
			d := math.Round((*c.DiskTotal-*c.DiskUsed) / *c.DiskGrowthPerDay * 10) / 10
			a.DaysUntilDiskFull = &d
		}
	}

	hot, watch := false, false
	add := func(s string, args ...any) { a.Findings = append(a.Findings, fmt.Sprintf(s, args...)) }
	if c.CPUP95 != nil && *c.CPUP95 >= CPUHot {
		hot = true
		add("CPU is at %.0f%% or more 5%% of the time. Move workloads off or add cores.", *c.CPUP95)
	}
	if a.MemoryP95Pct != nil && *a.MemoryP95Pct >= MemoryHot {
		hot = true
		add("Memory use peaks at %.0f%% of %s. The kernel may start killing processes.", *a.MemoryP95Pct, bytesIEC(a.MemoryCapacity))
	}
	if a.DiskPct != nil && *a.DiskPct >= DiskHot {
		hot = true
		add("The disk is %.0f%% full. Clean up unused images and build cache, or grow the disk.", *a.DiskPct)
	}
	if a.DaysUntilDiskFull != nil && *a.DaysUntilDiskFull < DiskSoonDays {
		hot = true
		add("At the current growth (%s a day) the disk fills in about %.0f days.", bytesIEC(int64(*c.DiskGrowthPerDay)), math.Max(*a.DaysUntilDiskFull, 0))
	}
	if a.MemoryCommitPct != nil && *a.MemoryCommitPct > 100 {
		watch = true
		add("Workload memory limits add up to %.0f%% of the host's memory. That's fine while they stay below their limits, but they can't all use them at once.", *a.MemoryCommitPct)
	}
	if a.CPUCommitPct != nil && *a.CPUCommitPct > 200 {
		watch = true
		add("Workload CPU limits add up to %.0f%% of the cores. Busy workloads will slow each other down.", *a.CPUCommitPct)
	}
	if c.CPUP95 != nil && *c.CPUP95 >= 70 && *c.CPUP95 < CPUHot {
		watch = true
		add("CPU peaks at %.0f%%, close to the %.0f%% threshold.", *c.CPUP95, CPUHot)
	}
	enough := c.Samples >= MinSamples
	switch {
	case hot:
		a.Status = "constrained"
	case watch:
		a.Status = "watch"
	case c.Samples == 0:
		a.Status = "unknown"
		add("No host samples in this period, so usage can't be judged.")
	case c.CPUP95 == nil && a.MemoryP95Pct == nil:
		a.Status = "unknown"
		add("Only engine data is available here. Turn on the host probe to measure CPU and memory.")
	case enough && c.CPUP95 != nil && *c.CPUP95 < CPUIdle && (a.MemoryP95Pct == nil || *a.MemoryP95Pct < MemoryIdle):
		a.Status = "underused"
		add("CPU stays under %.0f%% and memory under %.0f%%. This server has room for more workloads, or could be made smaller.", CPUIdle, MemoryIdle)
	default:
		a.Status = "ok"
	}
	if !enough && c.Samples > 0 && (a.Status == "ok" || a.Status == "underused") {
		add("Less than two hours of history, so this is a first impression.")
	}
	return a
}

func bytesIEC(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

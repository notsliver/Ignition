package hostinfo

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixture(t *testing.T, files map[string]string) {
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := Root
	Root = dir
	t.Cleanup(func() { Root = old })
}

func TestReadAndRates(t *testing.T) {
	fixture(t, map[string]string{
		"stat":      "cpu  100 0 100 700 100 0 0 0 0 0\ncpu0 1 1 1 1 1 0 0 0 0 0\n",
		"loadavg":   "0.50 0.25 0.10 1/200 1234\n",
		"meminfo":   "MemTotal:       2048 kB\nMemFree:        512 kB\nMemAvailable:   1024 kB\nSwapTotal:      100 kB\nSwapFree:       50 kB\n",
		"uptime":    "3600.55 100.00\n",
		"diskstats": "   8       0 sda 10 0 100 0 10 0 200 0 0 0 0\n   8       1 sda1 10 0 999 0 10 0 999 0 0 0 0\n 259 0 nvme0n1 1 0 10 0 1 0 20 0 0 0 0\n   7 0 loop0 1 0 5000 0 1 0 5000 0 0 0 0\n",
	})
	a := Read()
	if !a.CPUOK || a.CPU.Total != 1000 || a.CPU.Busy != 200 {
		t.Fatalf("cpu = %+v", a.CPU)
	}
	if !a.LoadOK || a.Load[0] != 0.5 {
		t.Fatalf("load = %v", a.Load)
	}
	if !a.MemOK || a.MemTotal != 2048*1024 || a.MemAvail != 1024*1024 || a.SwapTotal-a.SwapFree != 50*1024 {
		t.Fatalf("mem = %+v", a)
	}
	if !a.UptimeOK || a.Uptime != 3600 {
		t.Fatalf("uptime = %d", a.Uptime)
	}
	// Partitions and loop devices are skipped: sda (100) + nvme0n1 (10) sectors read.
	if !a.DiskOK || a.DiskRead != 110*512 || a.DiskWrite != 220*512 {
		t.Fatalf("disk = %d %d", a.DiskRead, a.DiskWrite)
	}

	b := a
	b.At = a.At.Add(10 * time.Second)
	b.CPU = CPUTimes{Busy: 300, Total: 1200}
	b.DiskRead += 5120
	if p, ok := CPUPercent(a, b); !ok || p != 50 {
		t.Fatalf("cpu%% = %v %v", p, ok)
	}
	if r, w, ok := DiskRates(a, b); !ok || r != 512 || w != 0 {
		t.Fatalf("rates = %v %v %v", r, w, ok)
	}
	// Counters going backwards (reboot) give no rate rather than a nonsense one.
	b.CPU = CPUTimes{Busy: 1, Total: 2}
	if _, ok := CPUPercent(a, b); ok {
		t.Fatal("expected no cpu rate after a counter reset")
	}
}

func TestMissingProcIsNotZero(t *testing.T) {
	fixture(t, map[string]string{})
	s := Read()
	if s.CPUOK || s.LoadOK || s.MemOK || s.UptimeOK || s.DiskOK {
		t.Fatalf("expected nothing readable: %+v", s)
	}
	if Available() {
		t.Fatal("Available with no stat file")
	}
}

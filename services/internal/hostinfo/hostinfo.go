// Package hostinfo reads host-wide figures from /proc for the local server.
//
// IGNITION runs in containers, but /proc/stat, /proc/loadavg, /proc/meminfo, /proc/uptime and
// /proc/diskstats are not namespaced, so they describe the whole machine. /proc/net/dev *is*
// namespaced (it would show the container's own interface), so host network throughput isn't
// reported from here. Every reading carries an ok flag: a value that couldn't be read is absent,
// never zero.
package hostinfo

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

// Root is where procfs is read from; tests point it at fixtures.
var Root = "/proc"

type CPUTimes struct{ Busy, Total uint64 }

type Snapshot struct {
	At         time.Time
	CPU        CPUTimes
	CPUOK      bool
	Load       [3]float64
	LoadOK     bool
	MemTotal   int64
	MemAvail   int64
	SwapTotal  int64
	SwapFree   int64
	MemOK      bool
	Uptime     int64
	UptimeOK   bool
	DiskRead   uint64 // bytes, cumulative over whole disks
	DiskWrite  uint64
	DiskOK     bool
	FSTotal    int64 // the filesystem holding / as seen from this process (Docker's data root, in practice)
	FSUsed     int64
	FSOK       bool
	FSPath     string
	Hostname   string
	OSRelease  string
	KernelArch string
}

// Available reports whether procfs can be read at all here.
func Available() bool {
	_, err := os.Stat(Root + "/stat")
	return err == nil
}

func Read() Snapshot {
	s := Snapshot{At: time.Now()}
	s.CPU, s.CPUOK = readCPU()
	s.Load, s.LoadOK = readLoad()
	s.readMem()
	if b, err := os.ReadFile(Root + "/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				s.Uptime, s.UptimeOK = int64(v), true
			}
		}
	}
	s.DiskRead, s.DiskWrite, s.DiskOK = readDisk()
	s.FSPath = "/"
	s.FSTotal, s.FSUsed, s.FSOK = statfs("/")
	return s
}

// CPUPercent is busy time between two snapshots as a share of all cores (0–100).
func CPUPercent(prev, cur Snapshot) (float64, bool) {
	if !prev.CPUOK || !cur.CPUOK || cur.CPU.Total <= prev.CPU.Total || cur.CPU.Busy < prev.CPU.Busy {
		return 0, false
	}
	return float64(cur.CPU.Busy-prev.CPU.Busy) / float64(cur.CPU.Total-prev.CPU.Total) * 100, true
}

// DiskRates are bytes per second read and written between two snapshots.
func DiskRates(prev, cur Snapshot) (float64, float64, bool) {
	secs := cur.At.Sub(prev.At).Seconds()
	if !prev.DiskOK || !cur.DiskOK || secs <= 0 || cur.DiskRead < prev.DiskRead || cur.DiskWrite < prev.DiskWrite {
		return 0, 0, false
	}
	return float64(cur.DiskRead-prev.DiskRead) / secs, float64(cur.DiskWrite-prev.DiskWrite) / secs, true
}

func readCPU() (CPUTimes, bool) {
	f, err := os.Open(Root + "/stat")
	if err != nil {
		return CPUTimes{}, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		var t CPUTimes
		for i, v := range fields[1:] {
			n, _ := strconv.ParseUint(v, 10, 64)
			if i >= 8 { // guest and guest_nice are already counted in user and nice
				break
			}
			t.Total += n
			if i != 3 && i != 4 { // idle, iowait
				t.Busy += n
			}
		}
		return t, true
	}
	return CPUTimes{}, false
}

func readLoad() ([3]float64, bool) {
	b, err := os.ReadFile(Root + "/loadavg")
	if err != nil {
		return [3]float64{}, false
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return [3]float64{}, false
	}
	var out [3]float64
	for i := range out {
		v, err := strconv.ParseFloat(f[i], 64)
		if err != nil {
			return out, false
		}
		out[i] = v
	}
	return out, true
}

func (s *Snapshot) readMem() {
	f, err := os.Open(Root + "/meminfo")
	if err != nil {
		return
	}
	defer f.Close()
	vals := map[string]int64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		n, _ := strconv.ParseInt(fields[0], 10, 64)
		vals[k] = n * 1024
	}
	total, okT := vals["MemTotal"]
	avail, okA := vals["MemAvailable"]
	if !okT || !okA || total == 0 {
		return
	}
	s.MemTotal, s.MemAvail, s.SwapTotal, s.SwapFree, s.MemOK = total, avail, vals["SwapTotal"], vals["SwapFree"], true
}

// readDisk sums whole-disk counters, skipping partitions, loop, ram and device-mapper devices so
// the same I/O isn't counted twice.
func readDisk() (uint64, uint64, bool) {
	f, err := os.Open(Root + "/diskstats")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	var read, write uint64
	found := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 {
			continue
		}
		name := fields[2]
		if !wholeDisk(name) {
			continue
		}
		r, _ := strconv.ParseUint(fields[5], 10, 64)
		w, _ := strconv.ParseUint(fields[9], 10, 64)
		read += r * 512
		write += w * 512
		found = true
	}
	return read, write, found
}

func wholeDisk(name string) bool {
	for _, p := range []string{"loop", "ram", "dm-", "zram", "sr", "md"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	switch {
	case strings.HasPrefix(name, "nvme"), strings.HasPrefix(name, "mmcblk"):
		return !strings.Contains(name[4:], "p") // nvme0n1 yes, nvme0n1p1 no
	case strings.HasPrefix(name, "sd"), strings.HasPrefix(name, "vd"), strings.HasPrefix(name, "xvd"), strings.HasPrefix(name, "hd"):
		last := name[len(name)-1]
		return last < '0' || last > '9'
	}
	return false
}

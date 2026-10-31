// Package hostinfo reads host-wide figures from procfs.
//
// The same parsers serve two sources. Read() opens files under Root for the local server: Ignition
// runs in containers, but /proc/stat, /proc/loadavg, /proc/meminfo, /proc/uptime and
// /proc/diskstats are not namespaced, so they describe the whole machine. /proc/net/dev *is*
// namespaced (it shows the container's own interface), so Read() leaves network out. Parse()
// takes the output of the host probe, a tiny container on the host network namespace, where
// net/dev is the host's own; that's how network and remote servers get measured. Every reading
// carries an ok flag: a value that couldn't be read is absent, never zero.
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
	NetRx      uint64 // bytes, cumulative over physical interfaces
	NetTx      uint64
	NetOK      bool
	FSTotal    int64 // the filesystem holding / as seen from this process (Docker's data root, in practice)
	FSUsed     int64
	FSOK       bool
	FSPath     string
	Hostname   string
	OSRelease  string
	KernelArch string
	Facts      Facts
}

// Facts are slow-changing hardware details.
type Facts struct {
	CPUModel   string   `json:"cpuModel,omitempty"`
	Cores      int      `json:"cores,omitempty"`   // logical CPUs
	Sockets    int      `json:"sockets,omitempty"` // physical packages, when the kernel reports them
	BootedAt   string   `json:"bootedAt,omitempty"`
	Interfaces []string `json:"interfaces,omitempty"` // the ones counted for network throughput
}

// Available reports whether procfs can be read at all here.
func Available() bool {
	_, err := os.Stat(Root + "/stat")
	return err == nil
}

func file(name string) string {
	b, err := os.ReadFile(Root + "/" + name)
	if err != nil {
		return ""
	}
	return string(b)
}

func Read() Snapshot {
	s := Parse(map[string]string{
		"stat": file("stat"), "loadavg": file("loadavg"), "meminfo": file("meminfo"), "uptime": file("uptime"),
		"diskstats": file("diskstats"), "cpuinfo": file("cpuinfo"),
	})
	s.FSPath = "/"
	s.FSTotal, s.FSUsed, s.FSOK = statfs("/")
	return s
}

// Parse builds a snapshot from procfs file contents keyed by path under /proc ("stat", "net/dev",
// ...) plus "df" holding `df -Pk /` output. Missing or unreadable sections are simply not OK.
func Parse(files map[string]string) Snapshot {
	s := Snapshot{At: time.Now()}
	s.CPU, s.CPUOK = parseCPU(files["stat"])
	s.Load, s.LoadOK = parseLoad(files["loadavg"])
	s.parseMem(files["meminfo"])
	if f := strings.Fields(files["uptime"]); len(f) > 0 {
		if v, err := strconv.ParseFloat(f[0], 64); err == nil {
			s.Uptime, s.UptimeOK = int64(v), true
			s.Facts.BootedAt = s.At.Add(-time.Duration(v) * time.Second).UTC().Truncate(time.Minute).Format(time.RFC3339)
		}
	}
	s.DiskRead, s.DiskWrite, s.DiskOK = parseDisk(files["diskstats"])
	if body, ok := files["net/dev"]; ok {
		s.NetRx, s.NetTx, s.Facts.Interfaces, s.NetOK = parseNet(body)
	}
	if body, ok := files["df"]; ok {
		s.FSTotal, s.FSUsed, s.FSOK = parseDF(body)
		s.FSPath = "/"
	}
	s.Facts.CPUModel, s.Facts.Cores, s.Facts.Sockets = parseCPUInfo(files["cpuinfo"])
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

// NetRates are bytes per second received and sent between two snapshots.
func NetRates(prev, cur Snapshot) (float64, float64, bool) {
	secs := cur.At.Sub(prev.At).Seconds()
	if !prev.NetOK || !cur.NetOK || secs <= 0 || cur.NetRx < prev.NetRx || cur.NetTx < prev.NetTx {
		return 0, 0, false
	}
	return float64(cur.NetRx-prev.NetRx) / secs, float64(cur.NetTx-prev.NetTx) / secs, true
}

func lines(body string) *bufio.Scanner { return bufio.NewScanner(strings.NewReader(body)) }

func parseCPU(body string) (CPUTimes, bool) {
	sc := lines(body)
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

func parseLoad(body string) ([3]float64, bool) {
	f := strings.Fields(body)
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

func (s *Snapshot) parseMem(body string) {
	vals := map[string]int64{}
	sc := lines(body)
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

// parseDisk sums whole-disk counters, skipping partitions, loop, ram and device-mapper devices so
// the same I/O isn't counted twice.
func parseDisk(body string) (uint64, uint64, bool) {
	var read, write uint64
	found := false
	sc := lines(body)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 {
			continue
		}
		if !wholeDisk(fields[2]) {
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

// parseNet sums the interfaces that carry traffic in and out of the machine. Loopback, Docker's
// bridges and container veths are skipped: traffic between containers crosses them on the host
// and would be counted twice (or isn't network traffic at all).
func parseNet(body string) (rx, tx uint64, names []string, ok bool) {
	sc := lines(body)
	for sc.Scan() {
		name, rest, found := strings.Cut(sc.Text(), ":")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		fields := strings.Fields(rest)
		if len(fields) < 9 || !physicalInterface(name) {
			continue
		}
		r, err1 := strconv.ParseUint(fields[0], 10, 64)
		t, err2 := strconv.ParseUint(fields[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		rx, tx, ok = rx+r, tx+t, true
		if r+t > 0 { // idle stubs (bond0 with no slaves, dummy devices) aren't worth listing
			names = append(names, name)
		}
	}
	return
}

func physicalInterface(name string) bool {
	if name == "lo" {
		return false
	}
	for _, p := range []string{"docker", "br-", "veth", "virbr", "cni", "flannel", "cali", "vxlan", "tun", "tap", "dummy", "kube", "weave", "lxc", "podman",
		// Tunnels carry traffic that already crossed a physical interface.
		"teql", "gre", "erspan", "ip_vti", "ip6_vti", "sit", "ip6tnl", "ip6gre", "ipip", "wg", "tailscale", "zt"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

// parseDF reads POSIX `df -Pk` output: the second line's total and used 1K blocks.
func parseDF(body string) (total, used int64, ok bool) {
	sc := lines(body)
	sc.Scan() // header
	if !sc.Scan() {
		return 0, 0, false
	}
	f := strings.Fields(sc.Text())
	if len(f) < 4 {
		return 0, 0, false
	}
	t, err1 := strconv.ParseInt(f[1], 10, 64)
	u, err2 := strconv.ParseInt(f[2], 10, 64)
	if err1 != nil || err2 != nil || t <= 0 {
		return 0, 0, false
	}
	return t * 1024, u * 1024, true
}

// parseCPUInfo finds the CPU model (x86 "model name", Arm "Model"/"Hardware"), the logical CPU
// count and the number of distinct physical packages.
func parseCPUInfo(body string) (model string, cores, sockets int) {
	ids := map[string]bool{}
	sc := lines(body)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "processor":
			cores++
		case "model name", "Model", "Hardware", "cpu model":
			if model == "" && v != "" {
				model = strings.Join(strings.Fields(v), " ")
			}
		case "physical id":
			ids[v] = true
		}
	}
	return model, cores, len(ids)
}

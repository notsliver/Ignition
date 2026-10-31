package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/servers"
	"github.com/notsliver/Ignition/services/internal/store"
)

type capacityServer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Environment string `json:"environment"`
	Location    string `json:"location"`
	State       string `json:"connectionState"`
	Collector   string `json:"collector"`
	Deployable  bool   `json:"deployable"`
	servers.Assessment
}

type capacityTotals struct {
	Servers         int     `json:"servers"`
	Measured        int     `json:"measured"` // servers with host CPU and memory figures in the window
	Cores           int     `json:"cores"`
	Memory          int64   `json:"memory"`
	DiskTotal       float64 `json:"diskTotal"`
	DiskUsed        float64 `json:"diskUsed"`
	CommittedCPUs   float64 `json:"committedCpus"`
	CommittedMemory int64   `json:"committedMemory"`
	Workloads       int     `json:"workloads"`
	Constrained     int     `json:"constrained"`
	Watch           int     `json:"watch"`
	Underused       int     `json:"underused"`
}

// fleetCapacity compares what every server has with what's committed to it and what it actually
// uses over a window (p95), and projects when disks fill. Nothing here is estimated from engine
// data alone: a server without host samples is reported as unknown.
func (s *Server) fleetCapacity(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	windows := map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour}
	rg := r.URL.Query().Get("window")
	window, ok := windows[rg]
	if !ok {
		rg, window = "7d", windows["7d"]
	}
	caps, err := s.Store.FleetCapacity(r.Context(), window)
	if err != nil {
		return err
	}
	out := []capacityServer{}
	var t capacityTotals
	for offset := 0; ; offset += 200 {
		list, total, err := s.Store.ListServers(r.Context(), store.ServerQuery{Sort: "name", Limit: 200, Offset: offset})
		if err != nil {
			return err
		}
		for i := range list {
			srv := &list[i]
			v := viewServer(srv)
			c := caps[srv.ID]
			if c == nil {
				c = &store.ServerCapacity{ServerID: srv.ID}
			}
			cores, mem := 0, int64(0)
			if v.Info.Engine != nil {
				cores, mem = v.Info.Engine.NCPU, v.Info.Engine.MemTotal
			}
			if cores == 0 {
				var f struct{ Cores int }
				json.Unmarshal(srv.HostFacts, &f)
				cores = f.Cores
			}
			a := servers.Assess(c, cores, mem)
			out = append(out, capacityServer{ID: srv.ID, Name: srv.Name, Environment: srv.Environment, Location: srv.Location, State: srv.EffectiveState,
				Collector: v.Info.Collector, Deployable: v.Eligibility.Eligible, Assessment: a})
			t.Servers++
			t.Cores += a.Cores
			t.Memory += a.MemoryCapacity
			t.CommittedCPUs += c.CommittedCPUs
			t.CommittedMemory += c.CommittedMemory
			t.Workloads += c.Workloads
			if c.DiskTotal != nil && c.DiskUsed != nil {
				t.DiskTotal += *c.DiskTotal
				t.DiskUsed += *c.DiskUsed
			}
			if c.CPUP95 != nil && c.MemoryP95 != nil {
				t.Measured++
			}
			switch a.Status {
			case "constrained":
				t.Constrained++
			case "watch":
				t.Watch++
			case "underused":
				t.Underused++
			}
		}
		if offset+len(list) >= total || len(list) == 0 {
			break
		}
	}
	httpx.JSON(w, 200, map[string]any{"window": rg, "servers": out, "totals": t, "thresholds": map[string]float64{
		"cpuHot": servers.CPUHot, "memoryHot": servers.MemoryHot, "diskHot": servers.DiskHot, "diskSoonDays": servers.DiskSoonDays,
		"cpuIdle": servers.CPUIdle, "memoryIdle": servers.MemoryIdle}})
	return nil
}

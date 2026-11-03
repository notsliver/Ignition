package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Where a product's services run and how many there can be. Every server is checked against the
 * product (eligible servers and locations), its state (maintenance, deployments off, removing),
 * the team (servers assigned to it) and its free capacity after a safety margin; a strategy picks
 * among the ones that pass. Ignition deploys workloads to this machine only for now, so other
 * servers are listed with that reason rather than chosen.
 */

type productPlacement struct {
	Servers           []string `json:"servers"`   // eligible servers; empty = every server
	Locations         []string `json:"locations"` // eligible locations; empty = anywhere
	IPs               []string `json:"ips"`       // game servers take ports only from these allocation IPs; empty = any
	Strategy          string   `json:"strategy"`  // least-utilized, most-available, spread, random, location, manual
	PreferredLocation string   `json:"preferredLocation"`
	AllowMaintenance  bool     `json:"allowMaintenance"` // place on servers in maintenance (normally skipped)
	MinFreeMemoryMB   int      `json:"minFreeMemoryMb"`  // memory that must stay free on a server after placing
	MinFreeCPUs       float64  `json:"minFreeCpus"`
	SafetyMarginPct   int      `json:"safetyMarginPct"` // share of each server never committed, 0-50
	PerServer         int      `json:"perServer"`       // most services of this product on one server; 0 = no limit
	Stock             int      `json:"stock"`           // most active services in total; 0 = unlimited
	PerTeam           int      `json:"perTeam"`         // most active services per team; 0 = unlimited
	MaxConcurrent     int      `json:"maxConcurrent"`   // provisioning at the same time; 0 = no limit
}

var strategies = map[string]string{"least-utilized": "Least utilized", "most-available": "Most available capacity", "spread": "Spread evenly",
	"random": "Random eligible server", "location": "Preferred location", "manual": "Choose when assigning"}

func parsePlacement(raw json.RawMessage) productPlacement {
	var p productPlacement
	json.Unmarshal(raw, &p)
	if p.Strategy == "" {
		p.Strategy = "least-utilized"
	}
	return p
}

func (p *productPlacement) validate(f map[string]string) {
	if strategies[p.Strategy] == "" {
		f["placement.strategy"] = "Choose a placement strategy."
	}
	for _, id := range p.Servers {
		if !uuidRE.MatchString(id) {
			f["placement.servers"] = "Unknown server."
		}
	}
	for _, ip := range p.IPs {
		if net.ParseIP(ip) == nil {
			f["placement.ips"] = ip + " isn't an IP address."
		}
	}
	switch {
	case p.SafetyMarginPct < 0 || p.SafetyMarginPct > 50:
		f["placement.safetyMarginPct"] = "A safety margin between 0 and 50%."
	case p.MinFreeMemoryMB < 0 || p.MinFreeCPUs < 0 || p.PerServer < 0 || p.Stock < 0 || p.PerTeam < 0 || p.MaxConcurrent < 0:
		f["placement.stock"] = "Limits can't be negative."
	case p.Strategy == "location" && strings.TrimSpace(p.PreferredLocation) == "":
		f["placement.preferredLocation"] = "Choose the preferred location."
	}
}

// placementCandidate is one server, and whether a service of a plan can go there.
type placementCandidate struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Location     string  `json:"location"`
	OK           bool    `json:"ok"`
	Reason       string  `json:"reason,omitempty"`
	FreeMemoryMB int     `json:"freeMemoryMb"`
	FreeCPUs     float64 `json:"freeCpus"`
	Services     int     `json:"services"` // of this product
	committedMem float64
	totalMem     float64
}

type availability struct {
	Available bool                 `json:"available"`
	Reason    string               `json:"reason,omitempty"`
	StockLeft *int                 `json:"stockLeft"` // nil when unlimited
	TeamLeft  *int                 `json:"teamLeft"`  // nil when unlimited or no team given
	Strategy  string               `json:"strategy"`
	Chosen    *placementCandidate  `json:"chosen"` // where the next service would go
	Servers   []placementCandidate `json:"servers"`
}

// availabilityFor works out where a service of plan would go for team ("" = any team), and why not.
func (s *Server) availabilityFor(ctx context.Context, p *store.Product, plan *store.Plan, team string) (availability, error) {
	pl := parsePlacement(p.Placement)
	out := availability{Strategy: pl.Strategy, Servers: []placementCandidate{}}
	var active, teamActive int
	s.Store.DB.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE team_id::text = $2) FROM services WHERE product_id = $1 AND status <> 'terminated'`, p.ID, team).Scan(&active, &teamActive)
	if pl.Stock > 0 {
		left := max(pl.Stock-active, 0)
		out.StockLeft = &left
	}
	if pl.PerTeam > 0 && team != "" {
		left := max(pl.PerTeam-teamActive, 0)
		out.TeamLeft = &left
	}

	rows, err := s.Store.DB.Query(ctx, `SELECT sv.id, sv.name, sv.location, sv.is_local, sv.maintenance, sv.deployments_enabled,
		$2 = '' OR EXISTS (SELECT 1 FROM team_server_assignments a WHERE a.server_id = sv.id AND a.team_id::text = $2),
		(SELECT count(*) FROM services x WHERE x.server_id = sv.id AND x.product_id = $1 AND x.status <> 'terminated')
		FROM servers sv WHERE NOT sv.removing ORDER BY sv.is_local DESC, sv.name`, p.ID, team)
	if err != nil {
		return out, err
	}
	type row struct {
		c                                 placementCandidate
		local, maintenance, enabled, team bool
	}
	var list []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.c.ID, &x.c.Name, &x.c.Location, &x.local, &x.maintenance, &x.enabled, &x.team, &x.c.Services); err != nil {
			rows.Close()
			return out, err
		}
		list = append(list, x)
	}
	rows.Close()
	committed, _ := s.Store.FleetCapacity(ctx, time.Hour, true)
	var hostMem float64
	var hostCPUs float64
	if s.Runtime != nil {
		if h, err := s.Runtime.Host(ctx); err == nil {
			hostMem, hostCPUs = float64(h.MemoryBytes>>20), float64(h.CPUs)
		}
	}
	margin := 1 - float64(pl.SafetyMarginPct)/100
	for _, x := range list {
		c := x.c
		var usedMem, usedCPU float64
		if cap := committed[c.ID]; cap != nil {
			usedMem, usedCPU = float64(cap.CommittedMemory>>20), cap.CommittedCPUs
		}
		if x.local {
			c.FreeMemoryMB, c.FreeCPUs = int(hostMem*margin-usedMem), round2(hostCPUs*margin-usedCPU)
			c.committedMem, c.totalMem = usedMem, hostMem
		}
		switch {
		case len(pl.Servers) > 0 && !slices.Contains(pl.Servers, c.ID):
			c.Reason = "Not eligible for this product"
		case len(pl.Locations) > 0 && !slices.ContainsFunc(pl.Locations, func(l string) bool { return strings.EqualFold(l, c.Location) }):
			c.Reason = "Its location isn't eligible"
		case !x.local:
			c.Reason = "Can't run services yet: Ignition deploys to this machine only for now"
		case x.maintenance && !pl.AllowMaintenance:
			c.Reason = "In maintenance"
		case !x.enabled:
			c.Reason = "New deployments are turned off"
		case !x.team:
			c.Reason = "Not assigned to this team"
		case pl.PerServer > 0 && c.Services >= pl.PerServer:
			c.Reason = fmt.Sprintf("Holds its limit of %d services of this product", pl.PerServer)
		case hostMem > 0 && plan != nil && float64(c.FreeMemoryMB-plan.MemoryMB) < float64(pl.MinFreeMemoryMB):
			c.Reason = fmt.Sprintf("Not enough free memory: %s free after the safety margin, %s needed", mb(max(c.FreeMemoryMB, 0)), mb(plan.MemoryMB+pl.MinFreeMemoryMB))
		case hostCPUs > 0 && plan != nil && c.FreeCPUs-plan.CPUs < pl.MinFreeCPUs-1e-9:
			c.Reason = fmt.Sprintf("Not enough free CPU: %g cores free after the safety margin, %g needed", round2(max(c.FreeCPUs, 0)), round2(plan.CPUs+pl.MinFreeCPUs))
		default:
			c.OK = true
		}
		out.Servers = append(out.Servers, c)
	}

	switch {
	case p.Status != "active":
		out.Reason = map[string]string{"draft": "It's a draft. Make it active to assign it.", "retired": "It's retired: existing services keep running, no new ones."}[p.Status]
	case out.StockLeft != nil && *out.StockLeft == 0:
		out.Reason = fmt.Sprintf("Sold out: all %d are in use.", pl.Stock)
	case out.TeamLeft != nil && *out.TeamLeft == 0:
		out.Reason = fmt.Sprintf("This team already has the most allowed (%d).", pl.PerTeam)
	}
	var ok []placementCandidate
	for _, c := range out.Servers {
		if c.OK {
			ok = append(ok, c)
		}
	}
	if out.Reason == "" && len(ok) == 0 {
		out.Reason = "No eligible server has room for it right now."
	}
	if out.Reason == "" {
		out.Available = true
		if pl.Strategy != "manual" {
			c := pickServer(pl, ok)
			out.Chosen = &c
		}
	}
	return out, nil
}

// pickServer applies the product's strategy to the servers that can take the service.
func pickServer(pl productPlacement, ok []placementCandidate) placementCandidate {
	used := func(c placementCandidate) float64 {
		if c.totalMem == 0 {
			return 0
		}
		return c.committedMem / c.totalMem
	}
	switch pl.Strategy {
	case "most-available":
		sort.SliceStable(ok, func(i, j int) bool { return ok[i].FreeMemoryMB > ok[j].FreeMemoryMB })
	case "spread":
		sort.SliceStable(ok, func(i, j int) bool { return ok[i].Services < ok[j].Services })
	case "random":
		return ok[rand.IntN(len(ok))]
	case "location":
		sort.SliceStable(ok, func(i, j int) bool {
			pi, pj := strings.EqualFold(ok[i].Location, pl.PreferredLocation), strings.EqualFold(ok[j].Location, pl.PreferredLocation)
			if pi != pj {
				return pi
			}
			return used(ok[i]) < used(ok[j])
		})
	default: // least-utilized
		sort.SliceStable(ok, func(i, j int) bool { return used(ok[i]) < used(ok[j]) })
	}
	return ok[0]
}

// productAvailability shows where the next service of a plan would go for a team, and why not.
func (s *Server) productAvailability(w http.ResponseWriter, r *http.Request, u *store.User) error {
	if err := requireInstanceAdmin(u); err != nil {
		return err
	}
	p, err := s.productFor(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	var plan *store.Plan
	for i := range p.Plans {
		if p.Plans[i].ID == q.Get("plan") || (plan == nil && q.Get("plan") == "") {
			plan = &p.Plans[i]
		}
	}
	team := q.Get("team")
	if team != "" && !uuidRE.MatchString(team) {
		return httpx.NotFound("Team")
	}
	a, err := s.availabilityFor(r.Context(), p, plan, team)
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, a)
	return nil
}

// Provisioning at once, per product (Maximum concurrent provisioning).
var provisioning = struct {
	sync.Mutex
	n map[string]int
}{n: map[string]int{}}

func startProvisioning(product string, limit int) (func(), bool) {
	provisioning.Lock()
	defer provisioning.Unlock()
	if limit > 0 && provisioning.n[product] >= limit {
		return nil, false
	}
	provisioning.n[product]++
	return func() { provisioning.Lock(); provisioning.n[product]--; provisioning.Unlock() }, true
}

type allocationIPsKey struct{}

// allocationIPs are the IPs a game server being provisioned may take a port from.
func allocationIPs(ctx context.Context) []string {
	ips, _ := ctx.Value(allocationIPsKey{}).([]string)
	return ips
}

var heroIconRE = regexp.MustCompile(`^hero:[A-Za-z0-9]+:#[0-9a-fA-F]{6}$`)
var dataImageRE = regexp.MustCompile(`^data:image/(png|jpeg|webp|gif|svg\+xml);base64,[A-Za-z0-9+/=]+$`)

func validIcon(icon string) bool {
	return icon == "" || heroIconRE.MatchString(icon) || (len(icon) <= 300_000 && dataImageRE.MatchString(icon))
}

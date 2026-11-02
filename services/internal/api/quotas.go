package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/notsliver/Ignition/services/internal/httpx"
	"github.com/notsliver/Ignition/services/internal/store"
)

/*
 * Quotas: limits at every level (instance, team, person, location) plus caps on a single resource
 * (any resource, and per type). Every quota that applies is checked before something is created,
 * resized, published or stored; the error names the level and how much is used.
 */

// quotaDemand is what an action adds. Storage and Backup mean "writes to disk": refused once the
// scope is at its storage quota, since the size isn't known in advance.
type quotaDemand struct {
	Type     string // a new resource of this type; "" for changes to an existing one
	CPUs     float64
	MemoryMB int
	Ports    int
	Storage  bool
	Backup   bool
}

var volumeSizeCache struct {
	sync.Mutex
	at    time.Time
	sizes map[string]int64
}

// volumeSizes reads every Ignition volume's size from Docker, cached briefly: Docker computes them
// by walking the disk.
func (s *Server) volumeSizes(ctx context.Context) map[string]int64 {
	volumeSizeCache.Lock()
	defer volumeSizeCache.Unlock()
	if volumeSizeCache.sizes != nil && time.Since(volumeSizeCache.at) < 30*time.Second {
		return volumeSizeCache.sizes
	}
	sizes := map[string]int64{}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if list, err := s.Runtime.Volumes(ctx, "ign-"); err == nil {
		for _, v := range list {
			if v.SizeBytes > 0 {
				sizes[v.Name] = v.SizeBytes
			}
		}
		volumeSizeCache.sizes, volumeSizeCache.at = sizes, time.Now()
	}
	return sizes
}

// fillStorage adds the volume sizes of the scope's resources: backups volumes count as backup
// storage, the rest as storage.
func fillStorage(u *store.QuotaUsage, sizes map[string]int64) {
	prefixes := map[string]bool{}
	for _, id := range u.ResourceIDs {
		if len(id) >= 8 {
			prefixes["ign-"+id[:8]+"-"] = true
		}
	}
	for name, size := range sizes {
		if len(name) < 13 || !prefixes[name[:13]] {
			continue
		}
		if strings.HasSuffix(name, "-backups") {
			u.BackupBytes += size
		} else {
			u.StorageBytes += size
		}
	}
}

type quotaScope struct {
	kind, id, label string
	quota           store.Quota
}

// quotaScopes lists every quota that applies to an owner, most general first.
func quotaScopes(q store.Quotas, o store.QuotaOwner, actor string) []quotaScope {
	out := []quotaScope{{"instance", "", "This instance", q.Instance}}
	if o.Team != "" {
		t, ok := q.Teams[o.Team]
		if !ok {
			t = q.Team
		}
		out = append(out, quotaScope{"team", o.Team, "This team", t})
	}
	if o.User != "" {
		p, ok := q.Users[o.User]
		if !ok {
			p = q.User
		}
		label := "The person who created it"
		if o.User == actor {
			label = "Your account"
		}
		out = append(out, quotaScope{"user", o.User, label, p})
	}
	if l, ok := q.LocationQuota(o.Location); ok && o.Location != "" {
		out = append(out, quotaScope{"location", o.Location, fmt.Sprintf("The %s location", o.Location), l})
	}
	return out
}

const quotaHint = " A platform administrator can change it under Instance administration › Limits."

func quotaErr(format string, a ...any) error {
	return &store.QuotaError{Message: fmt.Sprintf(format, a...) + quotaHint}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func gb(bytes int64) string { return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30)) }

// checkQuotas returns a *store.QuotaError when d would take any applicable scope over its quota.
func (s *Server) checkQuotas(ctx context.Context, o store.QuotaOwner, actor string, d quotaDemand) error {
	q := s.Store.Quotas(ctx)
	var sizes map[string]int64
	for _, sc := range quotaScopes(q, o, actor) {
		m := sc.quota
		if m == (store.Quota{}) {
			continue
		}
		use, err := s.Store.QuotaUsage(ctx, sc.kind, sc.id)
		if err != nil {
			return err
		}
		if d.Type != "" {
			count := map[string][3]any{
				"game":        {m.GameServers, use.GameServers, "game servers"},
				"application": {m.Applications, use.Applications, "applications"},
				"database":    {m.Databases, use.Databases, "databases"},
				"worker":      {m.Workers, use.Workers, "workers and cron jobs"},
				"cron":        {m.Workers, use.Workers, "workers and cron jobs"},
				"function":    {m.Functions, use.Functions, "functions"},
			}[d.Type]
			if max, _ := count[0].(int); max > 0 {
				if used := count[1].(int); used >= max {
					return quotaErr("%s has used %d of its %d %s.", sc.label, used, max, count[2])
				}
			}
			if m.Services > 0 && use.Services >= m.Services {
				return quotaErr("%s has used %d of its %d services.", sc.label, use.Services, m.Services)
			}
		}
		if d.CPUs > 0 && m.CPUs > 0 && use.CPUs+d.CPUs > m.CPUs+1e-9 {
			return quotaErr("%s has %g of its %g CPU cores in use; this needs %g more.", sc.label, round2(use.CPUs), m.CPUs, round2(d.CPUs))
		}
		if d.MemoryMB > 0 && m.MemoryMB > 0 && use.MemoryMB+d.MemoryMB > m.MemoryMB {
			return quotaErr("%s has %s of its %s of memory in use; this needs %s more.", sc.label, mb(use.MemoryMB), mb(m.MemoryMB), mb(d.MemoryMB))
		}
		if d.Ports > 0 && m.Ports > 0 && use.Ports+d.Ports > m.Ports {
			return quotaErr("%s has %d of its %d published %s in use.", sc.label, use.Ports, m.Ports, plural(m.Ports, "port", "ports"))
		}
		if (d.Storage || d.Type != "") && m.StorageGB > 0 || d.Backup && m.BackupGB > 0 {
			if sizes == nil {
				sizes = s.volumeSizes(ctx)
			}
			fillStorage(&use, sizes)
			if (d.Storage || d.Type != "") && m.StorageGB > 0 && use.StorageBytes >= int64(m.StorageGB)<<30 {
				return quotaErr("%s has used %s of its %d GB of storage.", sc.label, gb(use.StorageBytes), m.StorageGB)
			}
			if d.Backup && m.BackupGB > 0 && use.BackupBytes >= int64(m.BackupGB)<<30 {
				return quotaErr("%s has used %s of its %d GB of backup storage.", sc.label, gb(use.BackupBytes), m.BackupGB)
			}
		}
	}
	return nil
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

func mb(n int) string {
	if n >= 1024 && n%1024 == 0 {
		return fmt.Sprintf("%d GB", n/1024)
	}
	if n >= 1024 {
		return fmt.Sprintf("%.1f GB", float64(n)/1024)
	}
	return fmt.Sprintf("%d MB", n)
}

var typeNames = map[string]string{"application": "An application", "database": "A database", "worker": "A worker", "cron": "A cron job", "function": "A function", "game": "A game server"}

// checkResourceCap applies the per-resource and per-type caps to one resource's settings.
func (s *Server) checkResourceCap(ctx context.Context, typ string, cpus float64, memoryMB, ports int) error {
	q := s.Store.Quotas(ctx)
	name := typeNames[typ]
	if name == "" {
		name = "A resource"
	}
	for _, c := range []store.ResourceCap{q.Resource, q.Types[typ]} {
		if c.CPUs > 0 && cpus > c.CPUs+1e-9 {
			return quotaErr("%s can have at most %g CPU cores.", name, c.CPUs)
		}
		if c.MemoryMB > 0 && memoryMB > c.MemoryMB {
			return quotaErr("%s can have at most %s of memory.", name, mb(c.MemoryMB))
		}
		if c.Ports > 0 && ports > c.Ports {
			return quotaErr("%s can publish at most %d %s.", name, c.Ports, plural(c.Ports, "port", "ports"))
		}
	}
	return nil
}

// checkResourceStorage applies the storage caps (per resource, per type, and every scope's quota)
// before anything is written to a resource's volumes.
func (s *Server) checkResourceStorage(ctx context.Context, res *store.Resource, actor string, backup bool) error {
	o, err := s.Store.ResourceQuotaOwner(ctx, res.ID)
	if err != nil {
		return err
	}
	if err := s.checkQuotas(ctx, o, actor, quotaDemand{Storage: !backup, Backup: backup}); err != nil {
		return err
	}
	if backup {
		return nil
	}
	q := s.Store.Quotas(ctx)
	use := store.QuotaUsage{ResourceIDs: []string{res.ID}}
	for _, c := range []store.ResourceCap{q.Resource, q.Types[res.Type]} {
		if c.StorageGB <= 0 {
			continue
		}
		if use.StorageBytes == 0 {
			fillStorage(&use, s.volumeSizes(ctx))
		}
		if use.StorageBytes >= int64(c.StorageGB)<<30 {
			name := typeNames[res.Type]
			if name == "" {
				name = "A resource"
			}
			return quotaErr("%s can store at most %d GB; this one holds %s.", name, c.StorageGB, gb(use.StorageBytes))
		}
	}
	return nil
}

/* ----------------------------- Administration ----------------------------- */

// getQuotas returns every quota with current use beside it, and the teams, people and locations
// an administrator can give their own quota.
func (s *Server) getQuotas(w http.ResponseWriter, r *http.Request, _ *store.User) error {
	ctx := r.Context()
	sizes := s.volumeSizes(ctx)
	usage := func(kind, id string) (store.QuotaUsage, error) {
		u, err := s.Store.QuotaUsage(ctx, kind, id)
		fillStorage(&u, sizes)
		return u, err
	}
	type named struct {
		ID    string           `json:"id"`
		Name  string           `json:"name"`
		Usage store.QuotaUsage `json:"usage"`
	}
	list := func(query string, kind string) ([]named, error) {
		rows, err := s.Store.DB.Query(ctx, query)
		if err != nil {
			return nil, err
		}
		var out []named
		for rows.Next() {
			var n named
			if err := rows.Scan(&n.ID, &n.Name); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, n)
		}
		rows.Close()
		for i := range out {
			if out[i].Usage, err = usage(kind, out[i].ID); err != nil {
				return nil, err
			}
		}
		return out, rows.Err()
	}
	teams, err := list(`SELECT id::text, name FROM teams ORDER BY lower(name)`, "team")
	if err != nil {
		return err
	}
	users, err := list(`SELECT id::text, email FROM users ORDER BY lower(email)`, "user")
	if err != nil {
		return err
	}
	locations, err := list(`SELECT DISTINCT location, location FROM servers WHERE location <> '' ORDER BY 1`, "location")
	if err != nil {
		return err
	}
	instance, err := usage("instance", "")
	if err != nil {
		return err
	}
	httpx.JSON(w, 200, map[string]any{"quotas": s.Store.Quotas(ctx), "usage": instance, "teams": teams, "users": users, "locations": locations})
	return nil
}

func (s *Server) putQuotas(w http.ResponseWriter, r *http.Request, u *store.User) error {
	var in store.Quotas
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	f := map[string]string{}
	check := func(prefix string, q store.Quota) {
		for key, v := range map[string]int{"gameServers": q.GameServers, "applications": q.Applications, "databases": q.Databases, "workers": q.Workers,
			"functions": q.Functions, "services": q.Services, "memoryMb": q.MemoryMB, "storageGb": q.StorageGB, "backupGb": q.BackupGB, "ports": q.Ports} {
			if v < 0 || v > 10_000_000 {
				f[prefix+key] = "0 (unlimited) or more."
			}
		}
		if q.CPUs < 0 || q.CPUs > 100000 {
			f[prefix+"cpus"] = "0 (unlimited) or more."
		}
	}
	capCheck := func(prefix string, c store.ResourceCap) {
		if c.CPUs < 0 || c.MemoryMB < 0 || c.StorageGB < 0 || c.Ports < 0 {
			f[prefix+"cpus"] = "0 (unlimited) or more."
		}
		if c.MemoryMB > 0 && c.MemoryMB < 64 {
			f[prefix+"memoryMb"] = "At least 64 MB."
		}
		if c.CPUs > 0 && c.CPUs < 0.1 {
			f[prefix+"cpus"] = "At least 0.1."
		}
	}
	check("instance.", in.Instance)
	check("team.", in.Team)
	check("user.", in.User)
	for id, q := range in.Teams {
		check("teams."+id+".", q)
	}
	for id, q := range in.Users {
		check("users."+id+".", q)
	}
	for loc, q := range in.Locations {
		if strings.TrimSpace(loc) == "" || len(loc) > 64 {
			f["locations"] = "Locations need a name."
		}
		check("locations."+loc+".", q)
	}
	capCheck("resource.", in.Resource)
	for t, c := range in.Types {
		if typeNames[t] == "" {
			f["types"] = "Unknown resource type " + t + "."
		}
		capCheck("types."+t+".", c)
	}
	if len(f) > 0 {
		return httpx.Invalid(f)
	}
	if err := s.Store.SetQuotas(r.Context(), in); err != nil {
		return err
	}
	s.Store.Audit(r.Context(), u, "instance.quotas", "instance", "", map[string]any{"quotas": in}, clientIP(r))
	return s.getQuotas(w, r, u)
}

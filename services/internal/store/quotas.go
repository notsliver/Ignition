package store

import (
	"context"
	"strings"
)

// Quota caps what a scope (the whole instance, a team, a person or a location) may hold in total.
// 0 means unlimited. Set in Instance administration › Limits; the API checks every quota that
// applies before creating, resizing or storing anything.
type Quota struct {
	GameServers  int     `json:"gameServers"`
	Applications int     `json:"applications"`
	Databases    int     `json:"databases"`
	Workers      int     `json:"workers"` // workers and cron jobs
	Functions    int     `json:"functions"`
	Services     int     `json:"services"` // all of the above together
	CPUs         float64 `json:"cpus"`
	MemoryMB     int     `json:"memoryMb"`
	StorageGB    int     `json:"storageGb"`
	BackupGB     int     `json:"backupGb"`
	Ports        int     `json:"ports"` // published ports, allocations included
}

// ResourceCap is the most a single resource may have.
type ResourceCap struct {
	CPUs      float64 `json:"cpus"`
	MemoryMB  int     `json:"memoryMb"`
	StorageGB int     `json:"storageGb"`
	Ports     int     `json:"ports"`
}

// Quotas holds every level. A team, person or location with its own entry uses that entry instead
// of the default; per-resource caps apply to every resource, and per-type caps add to them.
type Quotas struct {
	Instance  Quota                  `json:"instance"`
	Team      Quota                  `json:"team"`
	Teams     map[string]Quota       `json:"teams"`
	User      Quota                  `json:"user"`
	Users     map[string]Quota       `json:"users"`
	Locations map[string]Quota       `json:"locations"`
	Resource  ResourceCap            `json:"resource"`
	Types     map[string]ResourceCap `json:"types"`
}

func (s *Store) Quotas(ctx context.Context) Quotas {
	q := Quotas{}
	var probe map[string]any
	s.GetSetting(ctx, "quotas", &probe)
	if probe != nil {
		s.GetSetting(ctx, "quotas", &q)
	} else {
		// Before quotas existed, Limits had one "services per team" number.
		var old struct {
			ResourcesPerTeam int `json:"resourcesPerTeam"`
		}
		s.GetSetting(ctx, "limits", &old)
		q.Team.Services = old.ResourcesPerTeam
	}
	if q.Teams == nil {
		q.Teams = map[string]Quota{}
	}
	if q.Users == nil {
		q.Users = map[string]Quota{}
	}
	if q.Locations == nil {
		q.Locations = map[string]Quota{}
	}
	if q.Types == nil {
		q.Types = map[string]ResourceCap{}
	}
	return q
}

func (s *Store) SetQuotas(ctx context.Context, q Quotas) error { return s.SetSetting(ctx, "quotas", q) }

// QuotaUsage is what a scope holds now. Storage is filled in by the caller, which can read Docker.
type QuotaUsage struct {
	GameServers  int      `json:"gameServers"`
	Applications int      `json:"applications"`
	Databases    int      `json:"databases"`
	Workers      int      `json:"workers"`
	Functions    int      `json:"functions"`
	Services     int      `json:"services"`
	CPUs         float64  `json:"cpus"`
	MemoryMB     int      `json:"memoryMb"`
	Ports        int      `json:"ports"`
	StorageBytes int64    `json:"storageBytes"`
	BackupBytes  int64    `json:"backupBytes"`
	ResourceIDs  []string `json:"-"`
}

// QuotaUsage counts a scope's resources: kind is instance, team, user or location. It uses the
// privileged pool, since a member can't see everything their team or the instance holds.
func (s *Store) QuotaUsage(ctx context.Context, kind, id string) (QuotaUsage, error) {
	where := map[string]string{
		"instance": `$1 = $1`,
		"team":     `r.team_id::text = $1`,
		"user":     `r.created_by::text = $1`,
		"location": `r.server_id IN (SELECT id FROM servers WHERE lower(location) = lower($1))`,
	}[kind]
	var u QuotaUsage
	err := s.DB.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE r.type = 'game'), count(*) FILTER (WHERE r.type = 'application'),
		count(*) FILTER (WHERE r.type = 'database'), count(*) FILTER (WHERE r.type IN ('worker', 'cron')),
		count(*) FILTER (WHERE r.type = 'function'), count(*),
		coalesce(sum(r.cpus), 0)::float8, coalesce(sum(r.memory_mb), 0)::int,
		(SELECT count(*) FROM resource_ports p JOIN resources r ON r.id = p.resource_id WHERE p.bind_address <> '127.0.0.1' AND `+where+`)::int,
		coalesce(array_agg(r.id::text), '{}')
		FROM resources r WHERE `+where, id).
		Scan(&u.GameServers, &u.Applications, &u.Databases, &u.Workers, &u.Functions, &u.Services, &u.CPUs, &u.MemoryMB, &u.Ports, &u.ResourceIDs)
	return u, err
}

// QuotaOwner is who a resource counts against: its team, the person who created it, and the
// location of its server.
type QuotaOwner struct {
	Team, User, Location string
}

func (s *Store) ResourceQuotaOwner(ctx context.Context, resourceID string) (QuotaOwner, error) {
	var o QuotaOwner
	err := s.DB.QueryRow(ctx, `SELECT r.team_id::text, coalesce(r.created_by::text, ''), coalesce(sv.location, '')
		FROM resources r LEFT JOIN servers sv ON sv.id = r.server_id WHERE r.id = $1`, resourceID).Scan(&o.Team, &o.User, &o.Location)
	return o, noRows(err)
}

// LocalLocation is where new resources go: the local server's location.
func (s *Store) LocalLocation(ctx context.Context) string {
	var loc string
	s.DB.QueryRow(ctx, `SELECT location FROM servers WHERE is_local LIMIT 1`).Scan(&loc)
	return loc
}

// LocationQuota finds a location's quota regardless of case.
func (q Quotas) LocationQuota(loc string) (Quota, bool) {
	for k, v := range q.Locations {
		if strings.EqualFold(k, loc) {
			return v, true
		}
	}
	return Quota{}, false
}

// QuotaError says which quota was reached, in words the API can return as is.
type QuotaError struct{ Message string }

func (e *QuotaError) Error() string { return e.Message }

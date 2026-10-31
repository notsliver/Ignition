package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* ---------------------------------- Servers --------------------------------- */

// StaleAfter is how long a connection state stays trustworthy without a fresh check. The worker
// checks every 15 s, so this is four missed checks. Past it, "online" is reported as "unknown":
// a server is never counted as available on the strength of an old observation.
const StaleAfter = 60 * time.Second

// MetricFreshness is how old the newest host sample may be before it's no longer shown as current.
const MetricFreshness = 90 * time.Second

type Server struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	Hostname             string            `json:"hostname"`
	Description          string            `json:"description"`
	Environment          string            `json:"environment"`
	Location             string            `json:"location"`
	Labels               map[string]string `json:"labels"`
	Notes                string            `json:"notes"`
	IsLocal              bool              `json:"isLocal"`
	ConnectionMethod     string            `json:"connectionMethod"`
	Endpoint             string            `json:"endpoint"`
	CACert               string            `json:"-"`
	ClientCert           string            `json:"-"`
	ClientKeySealed      []byte            `json:"-"`
	TLSFingerprint       string            `json:"tlsFingerprint"`
	CredentialsRotatedAt *time.Time        `json:"credentialsRotatedAt"`
	CredentialsRevokedAt *time.Time        `json:"credentialsRevokedAt"`
	ConnectionState      string            `json:"storedConnectionState"` // as last written; see EffectiveState
	EffectiveState       string            `json:"connectionState"`       // stored state adjusted for staleness
	Stale                bool              `json:"stale"`
	RuntimeState         string            `json:"runtimeState"`
	DeploymentsEnabled   bool              `json:"deploymentsEnabled"`
	Maintenance          bool              `json:"maintenance"`
	MaintenanceNote      string            `json:"maintenanceNote"`
	MaintenanceSince     *time.Time        `json:"maintenanceSince"`
	MaintenanceBy        string            `json:"maintenanceBy"`
	Removing             bool              `json:"removing"`
	LastSeenAt           *time.Time        `json:"lastSeenAt"`
	LastCheckAt          *time.Time        `json:"lastCheckAt"`
	LastError            string            `json:"lastError"`
	Failures             int               `json:"failures"`
	Info                 json.RawMessage   `json:"info"`
	HostFacts            json.RawMessage   `json:"hostFacts"`
	Capabilities         json.RawMessage   `json:"capabilities"`
	Settings             json.RawMessage   `json:"settings"`
	Workloads            int               `json:"workloads"`
	Metrics              *ServerUsage      `json:"metrics"` // newest sample if fresh, else nil
	CreatedAt            time.Time         `json:"createdAt"`
	UpdatedAt            time.Time         `json:"updatedAt"`
}

// ServerUsage is the newest host sample, only when it's recent enough to be current.
type ServerUsage struct {
	At          time.Time `json:"at"`
	CPUPercent  *float64  `json:"cpuPercent"`
	MemoryUsed  *int64    `json:"memoryUsed"`
	MemoryTotal *int64    `json:"memoryTotal"`
	DiskUsed    *int64    `json:"diskUsed"`
	DiskTotal   *int64    `json:"diskTotal"`
}

// effectiveState mirrors the Go rule in SQL so filters and sorting agree with what's displayed.
var effectiveState = fmt.Sprintf(`CASE
	WHEN sv.connection_state IN ('online', 'degraded', 'connecting') AND (sv.last_check_at IS NULL OR sv.last_check_at < now() - interval '%d seconds') THEN 'unknown'
	ELSE sv.connection_state END`, int(StaleAfter.Seconds()))

// serverBase names every column a listing can filter or sort on; serverSelect reads from it.
var serverBase = `
	WITH base AS (
		SELECT sv.*, ` + effectiveState + ` AS es,
		       COALESCE((SELECT email FROM users WHERE id = sv.maintenance_by), '') AS maint_by,
		       (SELECT count(*) FROM resources r WHERE r.server_id = sv.id) AS workloads,
		       m.at AS m_at, m.cpu_percent::float8 AS m_cpu, m.memory_used AS m_mu, m.memory_total AS m_mt, m.disk_used AS m_du, m.disk_total AS m_dt
		FROM servers sv
		LEFT JOIN LATERAL (
			SELECT at, cpu_percent, memory_used, memory_total, disk_used, disk_total FROM server_metric_samples
			WHERE server_id = sv.id AND at > now() - interval '` + fmt.Sprint(int(MetricFreshness.Seconds())) + ` seconds' ORDER BY at DESC LIMIT 1
		) m ON true
	)`

const serverCols = `id, name, hostname, description, environment, location, labels, notes, is_local, connection_method, endpoint,
	ca_cert, client_cert, client_key_enc, tls_fingerprint, credentials_rotated_at, credentials_revoked_at,
	connection_state, es, runtime_state, deployments_enabled, maintenance, maintenance_note, maintenance_since,
	maint_by, removing, last_seen_at, last_check_at, last_error, failures,
	info, host_facts, capabilities, settings, workloads, m_at, m_cpu, m_mu, m_mt, m_du, m_dt, created_at, updated_at`

var serverSelect = serverBase + ` SELECT ` + serverCols + ` FROM base`

func scanServer(row pgx.Row, extra ...any) (*Server, error) {
	var s Server
	var labels []byte
	var mAt *time.Time
	var cpu *float64
	var memU, memT, diskU, diskT *int64
	dest := []any{&s.ID, &s.Name, &s.Hostname, &s.Description, &s.Environment, &s.Location, &labels, &s.Notes, &s.IsLocal, &s.ConnectionMethod, &s.Endpoint,
		&s.CACert, &s.ClientCert, &s.ClientKeySealed, &s.TLSFingerprint, &s.CredentialsRotatedAt, &s.CredentialsRevokedAt,
		&s.ConnectionState, &s.EffectiveState, &s.RuntimeState, &s.DeploymentsEnabled, &s.Maintenance, &s.MaintenanceNote, &s.MaintenanceSince,
		&s.MaintenanceBy, &s.Removing, &s.LastSeenAt, &s.LastCheckAt, &s.LastError, &s.Failures,
		&s.Info, &s.HostFacts, &s.Capabilities, &s.Settings, &s.Workloads, &mAt, &cpu, &memU, &memT, &diskU, &diskT, &s.CreatedAt, &s.UpdatedAt}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return nil, noRows(err)
	}
	json.Unmarshal(labels, &s.Labels)
	if s.Labels == nil {
		s.Labels = map[string]string{}
	}
	s.Stale = s.EffectiveState != s.ConnectionState
	// An offline or stale server's last sample isn't current, even inside the freshness window.
	if mAt != nil && (s.EffectiveState == "online" || s.EffectiveState == "degraded") {
		s.Metrics = &ServerUsage{At: *mAt, CPUPercent: cpu, MemoryUsed: memU, MemoryTotal: memT, DiskUsed: diskU, DiskTotal: diskT}
	}
	return &s, nil
}

type ServerQuery struct {
	Q      string
	Filter string // all, online, offline, degraded, pending, unknown, maintenance, deployable, blocked, attention
	Sort   string // name, status, lastSeen, workloads, cpu, memory, disk
	Desc   bool
	Limit  int
	Offset int
}

var serverFilters = map[string]string{
	"online":      `es = 'online'`,
	"offline":     `es = 'offline'`,
	"degraded":    `es = 'degraded'`,
	"pending":     `es IN ('pending', 'connecting')`,
	"unknown":     `es = 'unknown'`,
	"maintenance": `maintenance`,
	"deployable":  `deployments_enabled AND NOT maintenance AND NOT removing AND is_local`,
	"blocked":     `NOT (deployments_enabled AND NOT maintenance AND NOT removing AND is_local)`,
	"attention":   AttentionSQL,
}

// AvailableSQL and AttentionSQL define the overview counts (columns of the listing subquery).
// Available: talking to it right now and its Docker Engine works.
// Needs attention: anything not available, plus degraded ones, except servers still being enrolled
// or removed (they're expected to be unavailable).
const (
	AvailableSQL = `(es IN ('online', 'degraded') AND runtime_state = 'ready')`
	AttentionSQL = `(NOT removing AND es <> 'pending' AND (es <> 'online' OR runtime_state <> 'ready'))`
)

var serverSorts = map[string]string{
	"name":      `lower(name)`,
	"status":    `CASE es WHEN 'offline' THEN 0 WHEN 'unknown' THEN 1 WHEN 'degraded' THEN 2 WHEN 'connecting' THEN 3 WHEN 'pending' THEN 4 ELSE 5 END`,
	"lastSeen":  `last_seen_at`,
	"workloads": `workloads`,
	"cpu":       `CASE WHEN es IN ('online', 'degraded') THEN m_cpu END`,
	"memory":    `CASE WHEN es IN ('online', 'degraded') AND m_mt > 0 THEN m_mu::float8 / m_mt END`,
	"disk":      `CASE WHEN es IN ('online', 'degraded') AND m_dt > 0 THEN m_du::float8 / m_dt END`,
}

type ServerSummary struct {
	Total     int `json:"total"`
	Available int `json:"available"`
	Attention int `json:"attention"`
}

// ListServers filters, sorts and pages in the database so large inventories stay fast.
func (s *Store) ListServers(ctx context.Context, q ServerQuery) ([]Server, int, error) {
	where := []string{"true"}
	args := []any{}
	if q.Q != "" {
		args = append(args, "%"+q.Q+"%")
		where = append(where, `(name ILIKE $1 OR hostname ILIKE $1 OR endpoint ILIKE $1 OR environment ILIKE $1 OR location ILIKE $1 OR description ILIKE $1 OR labels::text ILIKE $1)`)
	}
	if f, ok := serverFilters[q.Filter]; ok {
		where = append(where, f)
	}
	order, ok := serverSorts[q.Sort]
	if !ok {
		order = serverSorts["name"]
	}
	dir := "ASC"
	if q.Desc {
		dir = "DESC"
	}
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}
	sql := fmt.Sprintf(`%s SELECT %s, count(*) OVER () FROM base WHERE %s ORDER BY %s %s NULLS LAST, lower(name) LIMIT %d OFFSET %d`,
		serverBase, serverCols, strings.Join(where, " AND "), order, dir, q.Limit, max(q.Offset, 0))
	rows, err := s.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Server{}
	total := 0
	for rows.Next() {
		sv, err := scanServer(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *sv)
	}
	return out, total, rows.Err()
}

func (s *Store) ServerSummary(ctx context.Context) (ServerSummary, error) {
	var out ServerSummary
	err := s.DB.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE `+AvailableSQL+`), count(*) FILTER (WHERE `+AttentionSQL+`)
		FROM (SELECT `+effectiveState+` AS es, sv.runtime_state, sv.removing FROM servers sv) x`).Scan(&out.Total, &out.Available, &out.Attention)
	return out, err
}

func (s *Store) GetServer(ctx context.Context, id string) (*Server, error) {
	return scanServer(s.DB.QueryRow(ctx, serverSelect+` WHERE id = $1`, id))
}

func (s *Store) LocalServer(ctx context.Context) (*Server, error) {
	return scanServer(s.DB.QueryRow(ctx, serverSelect+` WHERE is_local`))
}

// ServerIDs lists every server for the worker's heartbeat loop.
func (s *Store) ServerIDs(ctx context.Context) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT id FROM servers WHERE NOT removing ORDER BY is_local DESC, created_at`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

type NewServer struct {
	Name, Hostname, Description, Environment, Location, Notes string
	Labels                                                    map[string]string
	Endpoint, CACert, ClientCert, TLSFingerprint              string
	ClientKeySealer                                           func(serverID string) ([]byte, error) // seals the key once the ID is known
	Info, Capabilities                                        any
	CreatedBy                                                 string
}

// CreateRemoteServer registers a verified remote engine. The key is sealed inside the transaction,
// bound to the new row's ID, so a failure leaves nothing behind.
func (s *Store) CreateRemoteServer(ctx context.Context, n NewServer) (string, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	labels, _ := json.Marshal(n.Labels)
	info, _ := json.Marshal(n.Info)
	caps, _ := json.Marshal(n.Capabilities)
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO servers (name, hostname, description, environment, location, labels, notes, connection_method, endpoint, ca_cert, client_cert, tls_fingerprint,
		                     connection_state, runtime_state, deployments_enabled, last_seen_at, last_check_at, info, capabilities, credentials_rotated_at, created_by)
		VALUES ($1, $2, $3, $4, $14, $5, $6, 'docker-tls', $7, $8, $9, $10, 'online', 'ready', false, now(), now(), $11, $12, now(), $13) RETURNING id`,
		n.Name, n.Hostname, n.Description, n.Environment, labels, n.Notes, n.Endpoint, n.CACert, n.ClientCert, n.TLSFingerprint, info, caps, nullable(n.CreatedBy), n.Location).Scan(&id)
	if err != nil {
		return "", err
	}
	sealed, err := n.ClientKeySealer(id)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE servers SET client_key_enc = $2 WHERE id = $1`, id, sealed); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

type ServerPatch struct {
	Name        *string            `json:"name"`
	Hostname    *string            `json:"hostname"`
	Description *string            `json:"description"`
	Environment *string            `json:"environment"`
	Location    *string            `json:"location"`
	Labels      *map[string]string `json:"labels"`
	Notes       *string            `json:"notes"`
	Settings    json.RawMessage    `json:"settings"`
}

func (s *Store) UpdateServer(ctx context.Context, id string, p ServerPatch) error {
	var labels []byte
	if p.Labels != nil {
		labels, _ = json.Marshal(*p.Labels)
	}
	var settings []byte
	if len(p.Settings) > 0 {
		settings = p.Settings
	}
	_, err := s.DB.Exec(ctx, `
		UPDATE servers SET name = COALESCE($2, name), hostname = COALESCE($3, hostname), description = COALESCE($4, description),
		       environment = COALESCE($5, environment), labels = COALESCE($6, labels), notes = COALESCE($7, notes),
		       settings = COALESCE($8, settings), location = COALESCE($9, location), updated_at = now()
		WHERE id = $1`, id, p.Name, p.Hostname, p.Description, p.Environment, labels, p.Notes, settings, p.Location)
	return err
}

func (s *Store) SetServerDeployments(ctx context.Context, id string, enabled bool) error {
	_, err := s.DB.Exec(ctx, `UPDATE servers SET deployments_enabled = $2, updated_at = now() WHERE id = $1`, id, enabled)
	return err
}

// SetServerMaintenance enters or leaves maintenance. It returns false when nothing changed.
func (s *Store) SetServerMaintenance(ctx context.Context, id string, on bool, note, userID string) (bool, error) {
	tag, err := s.DB.Exec(ctx, `
		UPDATE servers SET maintenance = $2,
		       maintenance_note = CASE WHEN $2 THEN $3 ELSE '' END,
		       maintenance_since = CASE WHEN $2 THEN now() END,
		       maintenance_by = CASE WHEN $2 THEN $4::uuid END, updated_at = now()
		WHERE id = $1 AND maintenance <> $2`, id, on, note, nullable(userID))
	return tag.RowsAffected() > 0, err
}

func (s *Store) SetMaintenanceNote(ctx context.Context, id, note string) error {
	_, err := s.DB.Exec(ctx, `UPDATE servers SET maintenance_note = $2, updated_at = now() WHERE id = $1 AND maintenance`, id, note)
	return err
}

// HealthUpdate is what one connection check observed.
type HealthUpdate struct {
	ConnectionState string
	RuntimeState    string
	Success         bool // reached the host
	Error           string
	Failures        int
	Info            any // nil keeps the previous value
	Capabilities    any
	Fingerprint     string // "" keeps the previous value
}

func (s *Store) RecordServerHealth(ctx context.Context, id string, h HealthUpdate) error {
	var info, caps []byte
	if h.Info != nil {
		info, _ = json.Marshal(h.Info)
	}
	if h.Capabilities != nil {
		caps, _ = json.Marshal(h.Capabilities)
	}
	_, err := s.DB.Exec(ctx, `
		UPDATE servers SET connection_state = $2, runtime_state = $3, last_check_at = now(),
		       last_seen_at = CASE WHEN $4 THEN now() ELSE last_seen_at END,
		       last_error = $5, failures = $6, info = COALESCE($7, info), capabilities = COALESCE($8, capabilities)
		WHERE id = $1`, id, h.ConnectionState, h.RuntimeState, h.Success, h.Error, h.Failures, info, caps)
	return err
}

// RotateServerCredentials replaces the client certificate and key after the new pair was verified.
func (s *Store) RotateServerCredentials(ctx context.Context, id, caCert, clientCert string, sealedKey []byte, fingerprint string) error {
	_, err := s.DB.Exec(ctx, `
		UPDATE servers SET ca_cert = $2, client_cert = $3, client_key_enc = $4, tls_fingerprint = $5, credentials_rotated_at = now(),
		       credentials_revoked_at = NULL, updated_at = now()
		WHERE id = $1 AND NOT is_local`, id, caCert, clientCert, sealedKey, fingerprint)
	return err
}

// RevokeServerCredentials deletes the stored key. The server stays registered but can't be reached
// until new credentials are provided.
func (s *Store) RevokeServerCredentials(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `
		UPDATE servers SET client_key_enc = NULL, credentials_revoked_at = now(), connection_state = 'offline', runtime_state = 'unknown',
		       last_error = 'Credentials were revoked.', updated_at = now()
		WHERE id = $1 AND NOT is_local`, id)
	return err
}

func (s *Store) UpdateServerEndpoint(ctx context.Context, id, endpoint, fingerprint string) error {
	_, err := s.DB.Exec(ctx, `UPDATE servers SET endpoint = $2, tls_fingerprint = $3, updated_at = now() WHERE id = $1 AND NOT is_local`, id, endpoint, fingerprint)
	return err
}

func (s *Store) SetServerRemoving(ctx context.Context, id string, on bool) error {
	_, err := s.DB.Exec(ctx, `UPDATE servers SET removing = $2, updated_at = now() WHERE id = $1`, id, on)
	return err
}

// DeleteServer removes the registration. Resources reference servers with ON DELETE RESTRICT, so a
// server that still hosts workloads can't be deleted by accident.
func (s *Store) DeleteServer(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM servers WHERE id = $1 AND NOT is_local`, id)
	return err
}

/* ---------------------------------- Events ---------------------------------- */

type ServerEvent struct {
	ID       int64           `json:"id"`
	ServerID string          `json:"serverId"`
	At       time.Time       `json:"at"`
	Kind     string          `json:"kind"`
	Level    string          `json:"level"`
	Message  string          `json:"message"`
	Actor    string          `json:"actor"`
	Detail   json.RawMessage `json:"detail"`
}

func (s *Store) AddServerEvent(ctx context.Context, serverID, kind, level, message, actor string, detail map[string]any) {
	if actor == "" {
		actor = "system"
	}
	if detail == nil {
		detail = map[string]any{}
	}
	b, _ := json.Marshal(detail)
	s.DB.Exec(ctx, `INSERT INTO server_events (server_id, kind, level, message, actor, detail) VALUES ($1, $2, $3, $4, $5, $6)`, serverID, kind, level, message, actor, b)
}

type EventQuery struct {
	Kinds  []string
	Levels []string
	Q      string
	Since  *time.Time
	Before int64 // id cursor
	Limit  int
}

func (s *Store) ListServerEvents(ctx context.Context, serverID string, q EventQuery) ([]ServerEvent, error) {
	if q.Limit <= 0 || q.Limit > 1000 {
		q.Limit = 100
	}
	rows, err := s.DB.Query(ctx, `
		SELECT id, server_id, at, kind, level, message, actor, detail FROM server_events
		WHERE server_id = $1 AND (cardinality($2::text[]) = 0 OR kind = ANY($2)) AND (cardinality($3::text[]) = 0 OR level = ANY($3))
		  AND ($4 = '' OR message ILIKE '%' || $4 || '%') AND ($5::timestamptz IS NULL OR at >= $5) AND ($6 = 0 OR id < $6)
		ORDER BY id DESC LIMIT $7`, serverID, nonNil(q.Kinds), nonNil(q.Levels), q.Q, q.Since, q.Before, q.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServerEvent{}
	for rows.Next() {
		var e ServerEvent
		if err := rows.Scan(&e.ID, &e.ServerID, &e.At, &e.Kind, &e.Level, &e.Message, &e.Actor, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func (s *Store) PruneServerEvents(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := s.DB.Exec(ctx, `DELETE FROM server_events WHERE at < now() - $1::interval`, interval(olderThan))
	return tag.RowsAffected(), err
}

/* ---------------------------------- Metrics --------------------------------- */

type ServerMetricSample struct {
	ServerID                  string
	At                        time.Time
	CPUPercent                *float64
	Load1, Load5, Load15      *float64
	MemoryUsed, MemoryTotal   *int64
	SwapUsed, SwapTotal       *int64
	DiskUsed, DiskTotal       *int64
	DiskReadBps, DiskWriteBps *float64
	NetRxBps, NetTxBps        *float64
	UptimeSeconds             *int64
	ContainersRunning         *int
	WorkloadCPU               *float64
	WorkloadMemory            *int64
}

func (s *Store) InsertServerMetric(ctx context.Context, m ServerMetricSample) error {
	_, err := s.DB.Exec(ctx, `
		INSERT INTO server_metric_samples (server_id, at, cpu_percent, load1, load5, load15, memory_used, memory_total, swap_used, swap_total,
		       disk_used, disk_total, disk_read_bps, disk_write_bps, uptime_seconds, containers_running, workload_cpu_percent, workload_memory, net_rx_bps, net_tx_bps)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20) ON CONFLICT DO NOTHING`,
		m.ServerID, m.At, m.CPUPercent, m.Load1, m.Load5, m.Load15, m.MemoryUsed, m.MemoryTotal, m.SwapUsed, m.SwapTotal,
		m.DiskUsed, m.DiskTotal, m.DiskReadBps, m.DiskWriteBps, m.UptimeSeconds, m.ContainersRunning, m.WorkloadCPU, m.WorkloadMemory, m.NetRxBps, m.NetTxBps)
	return err
}

func (s *Store) PruneServerMetrics(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := s.DB.Exec(ctx, `DELETE FROM server_metric_samples WHERE at < now() - $1::interval`, interval(olderThan))
	return tag.RowsAffected(), err
}

// ServerMetricBucket averages samples over a step. A nil field means nothing was measured in that
// bucket (either no samples, or the collector can't measure it); zero is a real zero.
type ServerMetricBucket struct {
	At             time.Time `json:"at"`
	Samples        int       `json:"samples"`
	CPU            *float64  `json:"cpu"`
	CPUMax         *float64  `json:"cpuMax"`
	Load1          *float64  `json:"load1"`
	Load5          *float64  `json:"load5"`
	Load15         *float64  `json:"load15"`
	MemoryUsed     *float64  `json:"memoryUsed"`
	MemoryTotal    *float64  `json:"memoryTotal"`
	SwapUsed       *float64  `json:"swapUsed"`
	SwapTotal      *float64  `json:"swapTotal"`
	DiskUsed       *float64  `json:"diskUsed"`
	DiskTotal      *float64  `json:"diskTotal"`
	DiskRead       *float64  `json:"diskReadBps"`
	DiskWrite      *float64  `json:"diskWriteBps"`
	NetRx          *float64  `json:"netRxBps"`
	NetTx          *float64  `json:"netTxBps"`
	Uptime         *float64  `json:"uptimeSeconds"`
	Containers     *float64  `json:"containersRunning"`
	WorkloadCPU    *float64  `json:"workloadCpu"`
	WorkloadMemory *float64  `json:"workloadMemory"`
}

func (s *Store) ServerMetricSeries(ctx context.Context, serverID string, window, step time.Duration) ([]ServerMetricBucket, error) {
	rows, err := s.DB.Query(ctx, `
		WITH buckets AS (
			SELECT generate_series(date_bin($3::interval, now() - $2::interval, 'epoch'), now(), $3::interval) AS at
		), agg AS (
			SELECT date_bin($3::interval, at, 'epoch') AS at, count(*) AS n,
			       avg(cpu_percent) AS cpu, max(cpu_percent) AS cpu_max, avg(load1) AS l1, avg(load5) AS l5, avg(load15) AS l15,
			       avg(memory_used) AS mu, max(memory_total) AS mt, avg(swap_used) AS su, max(swap_total) AS st,
			       avg(disk_used) AS du, max(disk_total) AS dt, avg(disk_read_bps) AS dr, avg(disk_write_bps) AS dw,
			       max(uptime_seconds) AS up, avg(containers_running) AS cr, avg(workload_cpu_percent) AS wc, avg(workload_memory) AS wm,
			       avg(net_rx_bps) AS nr, avg(net_tx_bps) AS nt
			FROM server_metric_samples
			WHERE server_id = $1 AND at >= now() - $2::interval - $3::interval
			GROUP BY 1
		)
		SELECT b.at, COALESCE(a.n, 0), a.cpu, a.cpu_max, a.l1, a.l5, a.l15, a.mu, a.mt, a.su, a.st, a.du, a.dt, a.dr, a.dw, a.up, a.cr, a.wc, a.wm, a.nr, a.nt
		FROM buckets b LEFT JOIN agg a ON a.at = b.at ORDER BY b.at`, serverID, interval(window), interval(step))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServerMetricBucket{}
	for rows.Next() {
		var b ServerMetricBucket
		if err := rows.Scan(&b.At, &b.Samples, &b.CPU, &b.CPUMax, &b.Load1, &b.Load5, &b.Load15, &b.MemoryUsed, &b.MemoryTotal, &b.SwapUsed, &b.SwapTotal,
			&b.DiskUsed, &b.DiskTotal, &b.DiskRead, &b.DiskWrite, &b.Uptime, &b.Containers, &b.WorkloadCPU, &b.WorkloadMemory, &b.NetRx, &b.NetTx); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LatestServerSample is the newest sample regardless of age (callers judge freshness).
func (s *Store) LatestServerSample(ctx context.Context, serverID string) (*ServerMetricBucket, error) {
	var b ServerMetricBucket
	var at *time.Time
	err := s.DB.QueryRow(ctx, `
		SELECT at, cpu_percent, load1, load5, load15, memory_used, memory_total, swap_used, swap_total, disk_used, disk_total,
		       disk_read_bps, disk_write_bps, uptime_seconds, containers_running, workload_cpu_percent, workload_memory, net_rx_bps, net_tx_bps
		FROM server_metric_samples WHERE server_id = $1 ORDER BY at DESC LIMIT 1`, serverID).
		Scan(&at, &b.CPU, &b.Load1, &b.Load5, &b.Load15, &b.MemoryUsed, &b.MemoryTotal, &b.SwapUsed, &b.SwapTotal, &b.DiskUsed, &b.DiskTotal,
			&b.DiskRead, &b.DiskWrite, &b.Uptime, &b.Containers, &b.WorkloadCPU, &b.WorkloadMemory, &b.NetRx, &b.NetTx)
	if err != nil {
		return nil, noRows(err)
	}
	b.At, b.Samples = *at, 1
	return &b, nil
}

// WorkloadUsage sums the newest per-container samples of a server's Ignition workloads.
func (s *Store) WorkloadUsage(ctx context.Context, serverID string) (cpu float64, mem int64, n int, err error) {
	err = s.DB.QueryRow(ctx, `
		SELECT COALESCE(sum(m.cpu_percent), 0), COALESCE(sum(m.memory_bytes), 0), count(m.at)
		FROM resources r
		JOIN LATERAL (SELECT cpu_percent, memory_bytes, at FROM metric_samples WHERE resource_id = r.id AND at > now() - interval '60 seconds' ORDER BY at DESC LIMIT 1) m ON true
		WHERE r.server_id = $1`, serverID).Scan(&cpu, &mem, &n)
	return
}

/* -------------------------------- Diagnostics ------------------------------- */

type DiagnosticResult struct {
	Check      string    `json:"check"`
	Status     string    `json:"status"`
	Location   string    `json:"location"`
	Summary    string    `json:"summary"`
	Output     string    `json:"output"`
	Hint       string    `json:"hint"`
	DurationMS *int      `json:"durationMs"`
	RanAt      time.Time `json:"ranAt"`
}

type DiagnosticRun struct {
	ID          string             `json:"id"`
	ServerID    string             `json:"serverId"`
	Checks      []string           `json:"checks"`
	Status      string             `json:"status"`
	RequestedBy string             `json:"requestedBy"`
	CreatedAt   time.Time          `json:"createdAt"`
	FinishedAt  *time.Time         `json:"finishedAt"`
	Results     []DiagnosticResult `json:"results"`
}

func (s *Store) CreateDiagnosticRun(ctx context.Context, tx pgx.Tx, serverID string, checks []string, actor string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO server_diagnostic_runs (server_id, checks, requested_by) VALUES ($1, $2, $3) RETURNING id`, serverID, checks, actor).Scan(&id)
	return id, err
}

func (s *Store) SetDiagnosticRunStatus(ctx context.Context, id, status string) error {
	_, err := s.DB.Exec(ctx, `UPDATE server_diagnostic_runs SET status = $2, finished_at = CASE WHEN $2 IN ('finished', 'failed', 'cancelled') THEN now() END WHERE id = $1`, id, status)
	return err
}

func (s *Store) SaveDiagnosticResult(ctx context.Context, runID string, r DiagnosticResult) error {
	_, err := s.DB.Exec(ctx, `
		INSERT INTO server_diagnostic_results (run_id, check_id, status, location, summary, output, hint, duration_ms, ran_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (run_id, check_id) DO UPDATE SET status = EXCLUDED.status, location = EXCLUDED.location, summary = EXCLUDED.summary,
		       output = EXCLUDED.output, hint = EXCLUDED.hint, duration_ms = EXCLUDED.duration_ms, ran_at = EXCLUDED.ran_at`,
		runID, r.Check, r.Status, r.Location, r.Summary, r.Output, r.Hint, r.DurationMS, r.RanAt)
	return err
}

func (s *Store) GetDiagnosticRun(ctx context.Context, id string) (*DiagnosticRun, error) {
	var d DiagnosticRun
	err := s.DB.QueryRow(ctx, `SELECT id, server_id, checks, status, requested_by, created_at, finished_at FROM server_diagnostic_runs WHERE id = $1`, id).
		Scan(&d.ID, &d.ServerID, &d.Checks, &d.Status, &d.RequestedBy, &d.CreatedAt, &d.FinishedAt)
	if err != nil {
		return nil, noRows(err)
	}
	d.Results, err = s.diagnosticResults(ctx, `run_id = $1`, id)
	return &d, err
}

func (s *Store) diagnosticResults(ctx context.Context, where string, arg any) ([]DiagnosticResult, error) {
	rows, err := s.DB.Query(ctx, `SELECT check_id, status, location, summary, output, hint, duration_ms, ran_at FROM server_diagnostic_results WHERE `+where+` ORDER BY ran_at`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DiagnosticResult{}
	for rows.Next() {
		var r DiagnosticResult
		if err := rows.Scan(&r.Check, &r.Status, &r.Location, &r.Summary, &r.Output, &r.Hint, &r.DurationMS, &r.RanAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestDiagnostics is the most recent result of each check on a server, across runs.
func (s *Store) LatestDiagnostics(ctx context.Context, serverID string) ([]DiagnosticResult, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT DISTINCT ON (r.check_id) r.check_id, r.status, r.location, r.summary, r.output, r.hint, r.duration_ms, r.ran_at
		FROM server_diagnostic_results r JOIN server_diagnostic_runs d ON d.id = r.run_id
		WHERE d.server_id = $1 ORDER BY r.check_id, r.ran_at DESC`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DiagnosticResult{}
	for rows.Next() {
		var r DiagnosticResult
		if err := rows.Scan(&r.Check, &r.Status, &r.Location, &r.Summary, &r.Output, &r.Hint, &r.DurationMS, &r.RanAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ActiveDiagnosticRun(ctx context.Context, serverID string) (*DiagnosticRun, error) {
	var id string
	err := s.DB.QueryRow(ctx, `SELECT id FROM server_diagnostic_runs WHERE server_id = $1 AND status IN ('queued', 'running') ORDER BY created_at DESC LIMIT 1`, serverID).Scan(&id)
	if err != nil {
		return nil, noRows(err)
	}
	return s.GetDiagnosticRun(ctx, id)
}

func (s *Store) ListDiagnosticRuns(ctx context.Context, serverID string, limit int) ([]DiagnosticRun, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT d.id, d.server_id, d.checks, d.status, d.requested_by, d.created_at, d.finished_at
		FROM server_diagnostic_runs d WHERE d.server_id = $1 ORDER BY d.created_at DESC LIMIT $2`, serverID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DiagnosticRun{}
	for rows.Next() {
		var d DiagnosticRun
		if err := rows.Scan(&d.ID, &d.ServerID, &d.Checks, &d.Status, &d.RequestedBy, &d.CreatedAt, &d.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

/* ------------------------------- Server jobs ------------------------------- */

type ServerJob struct {
	ID         string          `json:"id"`
	ServerID   string          `json:"serverId"`
	Kind       string          `json:"kind"`
	Op         string          `json:"op"`
	Status     string          `json:"status"`
	Progress   string          `json:"progress"`
	Error      string          `json:"error"`
	Result     json.RawMessage `json:"result"`
	Attempts   int             `json:"attempts"`
	Actor      string          `json:"actor"`
	CreatedAt  time.Time       `json:"createdAt"`
	StartedAt  *time.Time      `json:"startedAt"`
	FinishedAt *time.Time      `json:"finishedAt"`
}

const serverJobSelect = `
	SELECT j.id, j.server_id, j.kind, COALESCE(j.payload->>'op', ''), j.status, j.progress, j.last_error, j.result, j.attempts,
	       COALESCE(u.email, 'system'), j.created_at, j.started_at, j.finished_at
	FROM jobs j LEFT JOIN users u ON u.id = j.created_by`

func scanServerJob(row pgx.Row) (*ServerJob, error) {
	var j ServerJob
	err := row.Scan(&j.ID, &j.ServerID, &j.Kind, &j.Op, &j.Status, &j.Progress, &j.Error, &j.Result, &j.Attempts, &j.Actor, &j.CreatedAt, &j.StartedAt, &j.FinishedAt)
	j.Error = strings.TrimPrefix(j.Error, "permanent failure: ") // the queue's retry marker, not part of the message
	return &j, noRows(err)
}

func (s *Store) GetServerJob(ctx context.Context, id string) (*ServerJob, error) {
	return scanServerJob(s.DB.QueryRow(ctx, serverJobSelect+` WHERE j.id = $1 AND j.server_id IS NOT NULL`, id))
}

func (s *Store) ListServerJobs(ctx context.Context, serverID string, limit int) ([]ServerJob, error) {
	rows, err := s.DB.Query(ctx, serverJobSelect+` WHERE j.server_id = $1 ORDER BY j.created_at DESC LIMIT $2`, serverID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServerJob{}
	for rows.Next() {
		j, err := scanServerJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

func (s *Store) SetJobResult(ctx context.Context, jobID string, result any) error {
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `UPDATE jobs SET result = $2 WHERE id = $1`, jobID, b)
	return err
}

// ServerActivity is the work in flight on a server: what maintenance and removal must account for.
type ServerActivity struct {
	Deployments []ActiveDeployment `json:"deployments"`
	Jobs        []ServerJob        `json:"jobs"`
	TaskRuns    int                `json:"taskRuns"` // resource scheduled-task runs queued or running
}

type ActiveDeployment struct {
	ID           string    `json:"id"`
	Number       int       `json:"number"`
	Status       string    `json:"status"`
	ResourceID   string    `json:"resourceId"`
	ResourceName string    `json:"resourceName"`
	ProjectID    string    `json:"projectId"`
	CreatedAt    time.Time `json:"createdAt"`
}

func (s *Store) ServerActivity(ctx context.Context, serverID string) (ServerActivity, error) {
	out := ServerActivity{Deployments: []ActiveDeployment{}, Jobs: []ServerJob{}}
	rows, err := s.DB.Query(ctx, `
		SELECT d.id, d.number, d.status, r.id, r.name, r.project_id, d.created_at
		FROM deployments d JOIN resources r ON r.id = d.resource_id
		WHERE r.server_id = $1 AND d.status IN ('queued', 'building', 'deploying') ORDER BY d.created_at`, serverID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var d ActiveDeployment
		if err := rows.Scan(&d.ID, &d.Number, &d.Status, &d.ResourceID, &d.ResourceName, &d.ProjectID, &d.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Deployments = append(out.Deployments, d)
	}
	rows.Close()
	jrows, err := s.DB.Query(ctx, serverJobSelect+` WHERE j.server_id = $1 AND j.status IN ('pending', 'running') ORDER BY j.created_at`, serverID)
	if err != nil {
		return out, err
	}
	for jrows.Next() {
		j, err := scanServerJob(jrows)
		if err != nil {
			jrows.Close()
			return out, err
		}
		out.Jobs = append(out.Jobs, *j)
	}
	jrows.Close()
	err = s.DB.QueryRow(ctx, `
		SELECT count(*) FROM task_runs t JOIN scheduled_tasks st ON st.id = t.task_id JOIN resources r ON r.id = st.resource_id
		WHERE r.server_id = $1 AND t.status IN ('queued', 'running')`, serverID).Scan(&out.TaskRuns)
	return out, err
}

/* ------------------------------- Deployments ------------------------------- */

type ServerDeployment struct {
	Deployment
	ResourceName  string `json:"resourceName"`
	ResourceType  string `json:"resourceType"`
	ProjectID     string `json:"projectId"`
	ProjectName   string `json:"projectName"`
	Environment   string `json:"environment"`
	EnvironmentID string `json:"environmentId"`
}

type DeploymentQuery struct {
	Q      string
	Status string // '', active, successful, failed, cancelled
	Limit  int
	Offset int
}

func (s *Store) ListServerDeployments(ctx context.Context, serverID string, q DeploymentQuery) ([]ServerDeployment, int, error) {
	return s.deploymentsWhere(ctx, `r.server_id = $1`, []any{serverID}, q)
}

// RetainedImageIDs are images deployments of this server's resources ran, which a rollback could need.
func (s *Store) RetainedImageIDs(ctx context.Context, serverID string) (map[string]bool, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT DISTINCT d.image_id FROM deployments d JOIN resources r ON r.id = d.resource_id
		WHERE r.server_id = $1 AND d.image_id <> '' AND d.status = 'successful'`, serverID)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out, err
}

// ResourceIndex maps resource IDs to their names, project and server, for labelling Docker objects.
type ResourceRef struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	ServerID    string `json:"serverId"`
	ContainerID string `json:"-"`
}

func (s *Store) ResourceIndex(ctx context.Context) (map[string]ResourceRef, error) {
	rows, err := s.DB.Query(ctx, `SELECT r.id, r.name, r.type, p.id, p.name, r.server_id, r.container_id FROM resources r JOIN projects p ON p.id = r.project_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ResourceRef{}
	for rows.Next() {
		var r ResourceRef
		if err := rows.Scan(&r.ID, &r.Name, &r.Type, &r.ProjectID, &r.ProjectName, &r.ServerID, &r.ContainerID); err != nil {
			return nil, err
		}
		out[r.ID] = r
	}
	return out, rows.Err()
}

// ServerVolumeRows are the volumes Ignition configured for this server's resources.
type ServerVolumeRow struct {
	ResourceID string `json:"resourceId"`
	Name       string `json:"name"`
	MountPath  string `json:"mountPath"`
}

func (s *Store) ServerVolumes(ctx context.Context, serverID string) ([]ServerVolumeRow, error) {
	rows, err := s.DB.Query(ctx, `SELECT v.resource_id, v.name, v.mount_path FROM resource_volumes v JOIN resources r ON r.id = v.resource_id WHERE r.server_id = $1`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServerVolumeRow{}
	for rows.Next() {
		var v ServerVolumeRow
		if err := rows.Scan(&v.ResourceID, &v.Name, &v.MountPath); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ServerDomains are the custom domains routed to this server's resources.
type ServerDomain struct {
	Hostname     string `json:"hostname"`
	HTTPS        bool   `json:"https"`
	Status       string `json:"status"`
	ResourceID   string `json:"resourceId"`
	ResourceName string `json:"resourceName"`
	ProjectID    string `json:"projectId"`
}

func (s *Store) ServerDomains(ctx context.Context, serverID string) ([]ServerDomain, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT d.hostname, d.https, d.status, r.id, r.name, r.project_id FROM domains d JOIN resources r ON r.id = d.resource_id
		WHERE r.server_id = $1 ORDER BY d.hostname`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServerDomain{}
	for rows.Next() {
		var d ServerDomain
		if err := rows.Scan(&d.Hostname, &d.HTTPS, &d.Status, &d.ResourceID, &d.ResourceName, &d.ProjectID); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

/* ----------------------------------- Tasks ---------------------------------- */

type ServerTask struct {
	ID             string          `json:"id"`
	ServerID       string          `json:"serverId"`
	Kind           string          `json:"kind"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Schedule       string          `json:"schedule"`
	Timezone       string          `json:"timezone"`
	Enabled        bool            `json:"enabled"`
	TimeoutSeconds int             `json:"timeoutSeconds"`
	MaxAttempts    int             `json:"maxAttempts"`
	Concurrency    string          `json:"concurrency"`
	Options        json.RawMessage `json:"options"`
	NextRunAt      *time.Time      `json:"nextRunAt"`
	LastRun        *ServerTaskRun  `json:"lastRun"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

type ServerTaskRun struct {
	ID           string     `json:"id"`
	TaskID       string     `json:"taskId"`
	JobID        *string    `json:"jobId"`
	Trigger      string     `json:"trigger"`
	Status       string     `json:"status"`
	ScheduledFor *time.Time `json:"scheduledFor"`
	Summary      string     `json:"summary"`
	CreatedBy    string     `json:"createdBy"`
	CreatedAt    time.Time  `json:"createdAt"`
	FinishedAt   *time.Time `json:"finishedAt"`
	Attempts     int        `json:"attempts"`
	Error        string     `json:"error"`
}

const serverTaskSelect = `SELECT id, server_id, kind, name, description, schedule, timezone, enabled, timeout_seconds, max_attempts, concurrency, options, next_run_at, created_at, updated_at FROM server_tasks`

func scanServerTask(row pgx.Row) (*ServerTask, error) {
	var t ServerTask
	err := row.Scan(&t.ID, &t.ServerID, &t.Kind, &t.Name, &t.Description, &t.Schedule, &t.Timezone, &t.Enabled, &t.TimeoutSeconds, &t.MaxAttempts, &t.Concurrency, &t.Options, &t.NextRunAt, &t.CreatedAt, &t.UpdatedAt)
	return &t, noRows(err)
}

func (s *Store) ListServerTasks(ctx context.Context, serverID string) ([]ServerTask, error) {
	rows, err := s.DB.Query(ctx, serverTaskSelect+` WHERE server_id = $1 ORDER BY name`, serverID)
	if err != nil {
		return nil, err
	}
	out := []ServerTask{}
	for rows.Next() {
		t, err := scanServerTask(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *t)
	}
	rows.Close()
	for i := range out {
		runs, err := s.ListServerTaskRuns(ctx, out[i].ID, 1)
		if err == nil && len(runs) > 0 {
			out[i].LastRun = &runs[0]
		}
	}
	return out, nil
}

func (s *Store) GetServerTask(ctx context.Context, id string) (*ServerTask, error) {
	return scanServerTask(s.DB.QueryRow(ctx, serverTaskSelect+` WHERE id = $1`, id))
}

func (s *Store) CreateServerTask(ctx context.Context, t ServerTask, userID string) (string, error) {
	var id string
	err := s.DB.QueryRow(ctx, `
		INSERT INTO server_tasks (server_id, kind, name, description, schedule, timezone, enabled, timeout_seconds, max_attempts, concurrency, options, next_run_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id`,
		t.ServerID, t.Kind, t.Name, t.Description, t.Schedule, t.Timezone, t.Enabled, t.TimeoutSeconds, t.MaxAttempts, t.Concurrency, t.Options, t.NextRunAt, nullable(userID)).Scan(&id)
	return id, err
}

func (s *Store) UpdateServerTask(ctx context.Context, t ServerTask) error {
	_, err := s.DB.Exec(ctx, `
		UPDATE server_tasks SET name = $2, description = $3, schedule = $4, timezone = $5, enabled = $6, timeout_seconds = $7, max_attempts = $8,
		       concurrency = $9, options = $10, next_run_at = $11, updated_at = now()
		WHERE id = $1`, t.ID, t.Name, t.Description, t.Schedule, t.Timezone, t.Enabled, t.TimeoutSeconds, t.MaxAttempts, t.Concurrency, t.Options, t.NextRunAt)
	return err
}

func (s *Store) DeleteServerTask(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM server_tasks WHERE id = $1`, id)
	return err
}

func (s *Store) SetServerTaskNextRun(ctx context.Context, id string, next *time.Time) error {
	_, err := s.DB.Exec(ctx, `UPDATE server_tasks SET next_run_at = $2 WHERE id = $1`, id, next)
	return err
}

// DueServerTasks locks enabled tasks whose next run has passed, so only one worker schedules each.
func (s *Store) DueServerTasks(ctx context.Context, tx pgx.Tx) ([]ServerTask, error) {
	rows, err := tx.Query(ctx, serverTaskSelect+` WHERE enabled AND next_run_at <= now() ORDER BY next_run_at LIMIT 20 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServerTask{}
	for rows.Next() {
		t, err := scanServerTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

const serverTaskRunSelect = `
	SELECT t.id, t.task_id, t.job_id, t.trigger, t.status, t.scheduled_for, t.summary, t.created_by, t.created_at, t.finished_at,
	       COALESCE(j.attempts, 0), COALESCE(j.last_error, '')
	FROM server_task_runs t LEFT JOIN jobs j ON j.id = t.job_id`

func (s *Store) ListServerTaskRuns(ctx context.Context, taskID string, limit int) ([]ServerTaskRun, error) {
	rows, err := s.DB.Query(ctx, serverTaskRunSelect+` WHERE t.task_id = $1 ORDER BY t.created_at DESC LIMIT $2`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServerTaskRun{}
	for rows.Next() {
		var r ServerTaskRun
		if err := rows.Scan(&r.ID, &r.TaskID, &r.JobID, &r.Trigger, &r.Status, &r.ScheduledFor, &r.Summary, &r.CreatedBy, &r.CreatedAt, &r.FinishedAt, &r.Attempts, &r.Error); err != nil {
			return nil, err
		}
		r.Error = strings.TrimPrefix(r.Error, "permanent failure: ")
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetServerTaskRun(ctx context.Context, id string) (*ServerTaskRun, error) {
	var r ServerTaskRun
	err := s.DB.QueryRow(ctx, serverTaskRunSelect+` WHERE t.id = $1`, id).
		Scan(&r.ID, &r.TaskID, &r.JobID, &r.Trigger, &r.Status, &r.ScheduledFor, &r.Summary, &r.CreatedBy, &r.CreatedAt, &r.FinishedAt, &r.Attempts, &r.Error)
	r.Error = strings.TrimPrefix(r.Error, "permanent failure: ")
	return &r, noRows(err)
}

// TaskRunActive reports whether a run of the task is queued or running.
func (s *Store) TaskRunActive(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, taskID string) (bool, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM server_task_runs WHERE task_id = $1 AND status IN ('queued', 'running')`, taskID).Scan(&n)
	return n > 0, err
}

// CreateServerTaskRun records a run. A duplicate scheduled minute returns "" without error.
func (s *Store) CreateServerTaskRun(ctx context.Context, tx pgx.Tx, taskID, trigger, status string, scheduledFor *time.Time, actor, summary string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO server_task_runs (task_id, trigger, status, scheduled_for, created_by, summary, finished_at)
		VALUES ($1, $2, $3, $4, $5, $6, CASE WHEN $3 = 'skipped' THEN now() END)
		ON CONFLICT DO NOTHING RETURNING id`, taskID, trigger, status, scheduledFor, actor, summary).Scan(&id)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return id, err
}

func (s *Store) AttachTaskRunJob(ctx context.Context, tx pgx.Tx, runID, jobID string) error {
	_, err := tx.Exec(ctx, `UPDATE server_task_runs SET job_id = $2 WHERE id = $1`, runID, jobID)
	return err
}

func (s *Store) FinishServerTaskRun(ctx context.Context, runID, status, summary string) error {
	_, err := s.DB.Exec(ctx, `UPDATE server_task_runs SET status = $2, summary = $3, finished_at = CASE WHEN $2 IN ('succeeded', 'failed', 'cancelled', 'skipped') THEN now() END WHERE id = $1`, runID, status, summary)
	return err
}

// ServerTaskCount is used by the removal preview.
func (s *Store) ServerTaskCount(ctx context.Context, serverID string) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM server_tasks WHERE server_id = $1`, serverID).Scan(&n)
	return n, err
}

// PortClaim is a fixed host port a resource on the server is configured to publish.
type PortClaim struct {
	ResourceID   string `json:"resourceId"`
	ResourceName string `json:"resourceName"`
	HostPort     int    `json:"hostPort"`
	Protocol     string `json:"protocol"`
	BindAddress  string `json:"bindAddress"`
}

func (s *Store) ServerPortClaims(ctx context.Context, serverID string) ([]PortClaim, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT r.id, r.name, p.host_port, p.protocol, p.bind_address FROM resource_ports p JOIN resources r ON r.id = p.resource_id
		WHERE r.server_id = $1 AND p.host_port IS NOT NULL ORDER BY p.host_port`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PortClaim{}
	for rows.Next() {
		var p PortClaim
		if err := rows.Scan(&p.ResourceID, &p.ResourceName, &p.HostPort, &p.Protocol, &p.BindAddress); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ClaimServerCheck lets exactly one worker check a server per interval.
func (s *Store) ClaimServerCheck(ctx context.Context, id string, every time.Duration) (bool, error) {
	tag, err := s.DB.Exec(ctx, `UPDATE servers SET check_claimed_at = now() WHERE id = $1 AND (check_claimed_at IS NULL OR check_claimed_at < now() - $2::interval)`,
		id, interval(every-2*time.Second))
	return tag.RowsAffected() > 0, err
}

// ServerJobLog is a job log line from work on a server: its own jobs and its resources' deployments.
type ServerJobLog struct {
	ID         int64     `json:"id"`
	At         time.Time `json:"at"`
	Level      string    `json:"level"`
	Step       string    `json:"step"`
	Message    string    `json:"message"`
	JobID      string    `json:"jobId"`
	Kind       string    `json:"kind"`
	Resource   string    `json:"resource"`
	ResourceID string    `json:"resourceId"`
	Deployment *int      `json:"deployment"`
}

func (s *Store) ServerJobLogs(ctx context.Context, serverID string, since time.Time, levels []string, q, resourceID string, limit int) ([]ServerJobLog, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	rows, err := s.DB.Query(ctx, `
		SELECT l.id, l.created_at, l.level, l.step, l.message, j.id, j.kind, COALESCE(r.name, ''), COALESCE(r.id::text, ''), d.number
		FROM job_logs l JOIN jobs j ON j.id = l.job_id
		LEFT JOIN resources r ON r.id = j.resource_id LEFT JOIN deployments d ON d.id = j.deployment_id
		WHERE (j.server_id = $1 OR r.server_id = $1) AND l.created_at >= $2
		  AND (cardinality($3::text[]) = 0 OR l.level = ANY($3)) AND ($4 = '' OR l.message ILIKE '%' || $4 || '%')
		  AND ($5 = '' OR r.id::text = $5)
		ORDER BY l.id DESC LIMIT $6`, serverID, since, nonNil(levels), q, resourceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServerJobLog{}
	for rows.Next() {
		var l ServerJobLog
		if err := rows.Scan(&l.ID, &l.At, &l.Level, &l.Step, &l.Message, &l.JobID, &l.Kind, &l.Resource, &l.ResourceID, &l.Deployment); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

/* --------------------------------- Capacity --------------------------------- */

// SetServerHostFacts stores what the host probe read about the machine (CPU model, cores, boot time).
func (s *Store) SetServerHostFacts(ctx context.Context, id string, facts any) error {
	b, _ := json.Marshal(facts)
	_, err := s.DB.Exec(ctx, `UPDATE servers SET host_facts = $2 WHERE id = $1`, id, b)
	return err
}

// ServerCapacity is one server's committed limits next to its observed load over a window.
type ServerCapacity struct {
	ServerID         string   `json:"serverId"`
	Workloads        int      `json:"workloads"`
	CommittedMemory  int64    `json:"committedMemory"` // bytes, summed memory limits of workloads meant to run
	CommittedCPUs    float64  `json:"committedCpus"`
	CPUP95           *float64 `json:"cpuP95"`
	CPUAvg           *float64 `json:"cpuAvg"`
	MemoryP95        *float64 `json:"memoryP95"`
	MemoryTotal      *float64 `json:"memoryTotal"`
	DiskUsed         *float64 `json:"diskUsed"`
	DiskTotal        *float64 `json:"diskTotal"`
	DiskGrowthPerDay *float64 `json:"diskGrowthPerDay"` // bytes/day from a linear fit, nil with too little history
	Samples          int      `json:"samples"`
}

// FleetCapacity reads committed limits and window statistics for every server in one pass.
// External databases aren't run by Ignition, so they commit nothing.
func (s *Store) FleetCapacity(ctx context.Context, window time.Duration) (map[string]*ServerCapacity, error) {
	out := map[string]*ServerCapacity{}
	rows, err := s.DB.Query(ctx, `
		SELECT sv.id, count(r.id), COALESCE(sum(r.memory_mb), 0)::bigint * 1048576, COALESCE(sum(r.cpus), 0)::float8
		FROM servers sv LEFT JOIN resources r ON r.server_id = sv.id AND r.desired_state = 'running'
		     AND r.status <> 'removing' AND r.source_type <> 'external'
		GROUP BY sv.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		c := &ServerCapacity{}
		if err := rows.Scan(&c.ServerID, &c.Workloads, &c.CommittedMemory, &c.CommittedCPUs); err != nil {
			rows.Close()
			return nil, err
		}
		out[c.ServerID] = c
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = s.DB.Query(ctx, `
		SELECT server_id, count(*),
		       percentile_cont(0.95) WITHIN GROUP (ORDER BY cpu_percent), avg(cpu_percent),
		       percentile_cont(0.95) WITHIN GROUP (ORDER BY memory_used), max(memory_total)::float8,
		       (array_agg(disk_used ORDER BY at DESC) FILTER (WHERE disk_used IS NOT NULL))[1]::float8,
		       max(disk_total)::float8,
		       regr_slope(disk_used, extract(epoch FROM at)) * 86400,
		       extract(epoch FROM max(at) FILTER (WHERE disk_used IS NOT NULL) - min(at) FILTER (WHERE disk_used IS NOT NULL))
		FROM server_metric_samples WHERE at > now() - $1::interval GROUP BY server_id`, interval(window))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		var cpu95, cpuAvg, mem95, memT, du, dt, slope, span *float64
		if err := rows.Scan(&id, &n, &cpu95, &cpuAvg, &mem95, &memT, &du, &dt, &slope, &span); err != nil {
			return nil, err
		}
		c := out[id]
		if c == nil {
			continue
		}
		c.Samples, c.CPUP95, c.CPUAvg, c.MemoryP95, c.MemoryTotal, c.DiskUsed, c.DiskTotal = n, cpu95, cpuAvg, mem95, memT, du, dt
		// A slope over less than six hours is mostly noise from logs and image pulls.
		if slope != nil && span != nil && *span >= 6*3600 {
			c.DiskGrowthPerDay = slope
		}
	}
	return out, rows.Err()
}

// SetServerCollector records where host metrics now come from, with the matching capability text,
// without waiting for the next full check to rewrite info.
func (s *Store) SetServerCollector(ctx context.Context, id, collector string, metrics any) error {
	b, _ := json.Marshal(metrics)
	_, err := s.DB.Exec(ctx, `
		UPDATE servers SET info = jsonb_set(info, '{collector}', to_jsonb($2::text)),
		       capabilities = jsonb_set(capabilities, '{metrics}', $3::jsonb)
		WHERE id = $1 AND info ? 'collector' AND capabilities ? 'metrics'`, id, collector, b)
	return err
}

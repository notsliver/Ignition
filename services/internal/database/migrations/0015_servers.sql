-- Servers: the Docker hosts IGNITION knows about.
--
-- Every install has exactly one local server (the Docker Engine on this machine, reached through the
-- mounted socket). That is where the deploy engine runs workloads. Remote servers are Docker Engines
-- reached over mutual TLS; IGNITION monitors, inspects and cleans them up, but doesn't deploy to them.
--
-- Three status dimensions are kept apart on purpose:
--   connection_state  can IGNITION talk to the host at all            (written by the worker)
--   runtime_state     is the Docker Engine there usable               (written by the worker)
--   deployments_enabled / maintenance / removing                       (set by operators)
CREATE TABLE servers (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                   text NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    hostname               text NOT NULL DEFAULT '' CHECK (length(hostname) <= 253),
    description            text NOT NULL DEFAULT '' CHECK (length(description) <= 500),
    environment            text NOT NULL DEFAULT '' CHECK (length(environment) <= 40),
    labels                 jsonb NOT NULL DEFAULT '{}',
    notes                  text NOT NULL DEFAULT '' CHECK (length(notes) <= 4000),
    is_local               boolean NOT NULL DEFAULT false,
    connection_method      text NOT NULL CHECK (connection_method IN ('local', 'docker-tls')),
    endpoint               text NOT NULL DEFAULT '', -- host:port of a remote Docker Engine; '' for local
    -- Remote credentials. The CA and client certificate are public material; the client key is sealed
    -- with IGNITION_SECRET_KEY (bound to the server ID) and never returned by the API.
    ca_cert                text NOT NULL DEFAULT '',
    client_cert            text NOT NULL DEFAULT '',
    client_key_enc         bytea,
    tls_fingerprint        text NOT NULL DEFAULT '', -- SHA-256 of the host certificate accepted at enrollment
    credentials_rotated_at timestamptz,
    credentials_revoked_at timestamptz,
    connection_state       text NOT NULL DEFAULT 'pending'
                           CHECK (connection_state IN ('pending', 'connecting', 'online', 'degraded', 'offline', 'unknown')),
    runtime_state          text NOT NULL DEFAULT 'unknown'
                           CHECK (runtime_state IN ('ready', 'not_installed', 'unavailable', 'incompatible', 'unknown')),
    deployments_enabled    boolean NOT NULL DEFAULT true,
    maintenance            boolean NOT NULL DEFAULT false,
    maintenance_note       text NOT NULL DEFAULT '',
    maintenance_since      timestamptz,
    maintenance_by         uuid REFERENCES users (id) ON DELETE SET NULL,
    removing               boolean NOT NULL DEFAULT false,
    last_seen_at           timestamptz, -- last successful contact
    last_check_at          timestamptz, -- last attempt, successful or not
    last_error             text NOT NULL DEFAULT '',
    failures               integer NOT NULL DEFAULT 0, -- consecutive failed checks
    check_claimed_at       timestamptz, -- one worker checks each server per interval
    info                   jsonb NOT NULL DEFAULT '{}', -- what the engine reported: OS, arch, versions, counts
    capabilities           jsonb NOT NULL DEFAULT '{}', -- verified features, see servers.Capabilities
    settings               jsonb NOT NULL DEFAULT '{}', -- see servers.Settings
    created_by             uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CHECK ((connection_method = 'local') = is_local),
    CHECK (connection_method = 'local' OR endpoint <> '')
);
CREATE UNIQUE INDEX servers_one_local ON servers (is_local) WHERE is_local;
CREATE UNIQUE INDEX servers_endpoint_key ON servers (lower(endpoint)) WHERE endpoint <> '';
CREATE UNIQUE INDEX servers_name_key ON servers (lower(name));

INSERT INTO servers (name, hostname, description, is_local, connection_method)
VALUES ('This machine', 'localhost', 'The Docker host IGNITION runs on. Deployments run here.', true, 'local');

-- Workloads belong to a server. All existing resources run on the local one.
-- New resources default to it too: the deploy engine only targets the local Docker host.
CREATE FUNCTION local_server_id() RETURNS uuid LANGUAGE sql STABLE AS $$ SELECT id FROM servers WHERE is_local $$;
ALTER TABLE resources ADD COLUMN server_id uuid REFERENCES servers (id) ON DELETE RESTRICT;
UPDATE resources SET server_id = local_server_id();
ALTER TABLE resources ALTER COLUMN server_id SET DEFAULT local_server_id(), ALTER COLUMN server_id SET NOT NULL;
CREATE INDEX resources_server ON resources (server_id);

-- Host-level samples written by the worker. Columns are NULL when the collector can't measure them
-- on that server (NULL is "not measured", never zero).
CREATE TABLE server_metric_samples (
    server_id        uuid NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    at               timestamptz NOT NULL,
    cpu_percent      real,   -- of all cores; 100 = every core busy
    load1            real,
    load5            real,
    load15           real,
    memory_used      bigint,
    memory_total     bigint,
    swap_used        bigint,
    swap_total       bigint,
    disk_used        bigint,
    disk_total       bigint,
    disk_read_bps    real,
    disk_write_bps   real,
    uptime_seconds   bigint,
    containers_running integer,
    workload_cpu_percent real, -- sum over IGNITION workloads on this server
    workload_memory  bigint,
    PRIMARY KEY (server_id, at)
);
CREATE INDEX server_metric_samples_at ON server_metric_samples (at);

-- Things that happened to a server: state transitions, maintenance, credential changes, checks.
CREATE TABLE server_events (
    id         bigserial PRIMARY KEY,
    server_id  uuid NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    at         timestamptz NOT NULL DEFAULT now(),
    kind       text NOT NULL CHECK (kind IN ('connection', 'runtime', 'maintenance', 'deployments', 'security', 'diagnostics', 'operation', 'settings')),
    level      text NOT NULL DEFAULT 'info' CHECK (level IN ('info', 'warn', 'error')),
    message    text NOT NULL,
    actor      text NOT NULL DEFAULT 'system',
    detail     jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX server_events_server ON server_events (server_id, id DESC);

-- Diagnostic runs and the result of each check.
CREATE TABLE server_diagnostic_runs (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    server_id   uuid NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    checks      text[] NOT NULL,
    status      text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'finished', 'failed', 'cancelled')),
    requested_by text NOT NULL DEFAULT 'system',
    created_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE INDEX server_diagnostic_runs_server ON server_diagnostic_runs (server_id, created_at DESC);

CREATE TABLE server_diagnostic_results (
    run_id      uuid NOT NULL REFERENCES server_diagnostic_runs (id) ON DELETE CASCADE,
    check_id    text NOT NULL,
    status      text NOT NULL CHECK (status IN ('passed', 'warning', 'failed', 'unsupported', 'skipped')),
    location    text NOT NULL,
    summary     text NOT NULL,
    output      text NOT NULL DEFAULT '',
    hint        text NOT NULL DEFAULT '',
    duration_ms integer,
    ran_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, check_id)
);

-- Scheduled server operations. Only built-in, parameter-checked kinds exist: no arbitrary commands.
CREATE TABLE server_tasks (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    server_id       uuid NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    kind            text NOT NULL CHECK (kind IN ('diagnostics', 'disk_check', 'docker_cleanup', 'reconcile', 'maintenance_check')),
    name            text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    description     text NOT NULL DEFAULT '' CHECK (length(description) <= 500),
    schedule        text NOT NULL,
    timezone        text NOT NULL DEFAULT 'UTC',
    enabled         boolean NOT NULL DEFAULT true,
    timeout_seconds integer NOT NULL DEFAULT 300 CHECK (timeout_seconds BETWEEN 10 AND 3600),
    max_attempts    integer NOT NULL DEFAULT 1 CHECK (max_attempts BETWEEN 1 AND 5),
    concurrency     text NOT NULL DEFAULT 'skip' CHECK (concurrency IN ('skip', 'queue')),
    options         jsonb NOT NULL DEFAULT '{}',
    next_run_at     timestamptz,
    created_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (server_id, name)
);
CREATE INDEX server_tasks_due ON server_tasks (next_run_at) WHERE enabled;

-- Server work runs on the shared job queue. These columns tie a job to its server and keep its outcome.
ALTER TABLE jobs ADD COLUMN server_id uuid REFERENCES servers (id) ON DELETE CASCADE;
ALTER TABLE jobs ADD COLUMN result jsonb;
CREATE INDEX jobs_server ON jobs (server_id, created_at DESC) WHERE server_id IS NOT NULL;

CREATE TABLE server_task_runs (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id       uuid NOT NULL REFERENCES server_tasks (id) ON DELETE CASCADE,
    job_id        uuid REFERENCES jobs (id) ON DELETE SET NULL,
    trigger       text NOT NULL CHECK (trigger IN ('schedule', 'manual')),
    status        text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'skipped', 'cancelled')),
    scheduled_for timestamptz,
    summary       text NOT NULL DEFAULT '',
    created_by    text NOT NULL DEFAULT 'system',
    created_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz
);
CREATE INDEX server_task_runs_task ON server_task_runs (task_id, created_at DESC);
CREATE UNIQUE INDEX server_task_runs_once ON server_task_runs (task_id, scheduled_for) WHERE trigger = 'schedule';

-- Core schema for IGNITION. Timestamps are timestamptz; statuses are checked text enums.

CREATE TABLE users (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email           text NOT NULL,
    name            text NOT NULL DEFAULT '',
    password_hash   text NOT NULL,
    role            text NOT NULL CHECK (role IN ('owner', 'admin', 'developer', 'viewer')),
    status          text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    failed_logins   integer NOT NULL DEFAULT 0,
    locked_until    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

-- Only a SHA-256 of the session token is stored, so a database leak can't be replayed.
CREATE TABLE sessions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash      bytea NOT NULL UNIQUE,
    user_agent      text NOT NULL DEFAULT '',
    ip              text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE projects (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    archived_at     timestamptz,
    created_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX projects_name_key ON projects (lower(name));

CREATE TABLE project_favorites (
    project_id      uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, user_id)
);

CREATE TABLE environments (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name            text NOT NULL,
    position        integer NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);

CREATE TABLE resources (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    environment_id  uuid NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    name            text NOT NULL CHECK (name ~ '^[a-z0-9]([a-z0-9-]{0,48}[a-z0-9])?$'),
    type            text NOT NULL CHECK (type IN ('application', 'database', 'worker', 'cron')),
    source_type     text NOT NULL CHECK (source_type IN ('image', 'git')),
    image           text NOT NULL DEFAULT '',
    repo_url        text NOT NULL DEFAULT '',
    branch          text NOT NULL DEFAULT '',
    dockerfile      text NOT NULL DEFAULT 'Dockerfile',
    port            integer CHECK (port BETWEEN 1 AND 65535),
    command         text NOT NULL DEFAULT '',
    memory_mb       integer NOT NULL DEFAULT 512 CHECK (memory_mb BETWEEN 64 AND 65536),
    cpus            numeric(5, 2) NOT NULL DEFAULT 1 CHECK (cpus > 0),
    -- Desired state is set by users; observed state is written by the worker from Docker.
    desired_state   text NOT NULL DEFAULT 'running' CHECK (desired_state IN ('running', 'stopped')),
    status          text NOT NULL DEFAULT 'created'
                    CHECK (status IN ('created', 'deploying', 'running', 'stopped', 'failed', 'removing')),
    container_id    text NOT NULL DEFAULT '',
    host_port       integer,
    current_deployment_id uuid,
    created_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (environment_id, name)
);
CREATE INDEX resources_project_idx ON resources (project_id);

-- Values are AES-256-GCM ciphertext. Plaintext never touches the database.
CREATE TABLE env_vars (
    resource_id     uuid NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    key             text NOT NULL CHECK (key ~ '^[A-Za-z_][A-Za-z0-9_]*$'),
    value_enc       bytea NOT NULL,
    secret          boolean NOT NULL DEFAULT false,
    updated_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (resource_id, key)
);

CREATE TABLE deployments (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id     uuid NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    number          integer NOT NULL,
    status          text NOT NULL DEFAULT 'queued'
                    CHECK (status IN ('queued', 'building', 'deploying', 'successful', 'failed', 'cancelled')),
    trigger         text NOT NULL CHECK (trigger IN ('manual', 'create', 'redeploy', 'rollback', 'retry', 'config')),
    image_ref       text NOT NULL DEFAULT '',
    commit_sha      text NOT NULL DEFAULT '',
    branch          text NOT NULL DEFAULT '',
    reason          text NOT NULL DEFAULT '',
    error           text NOT NULL DEFAULT '',
    created_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    started_at      timestamptz,
    finished_at     timestamptz,
    UNIQUE (resource_id, number)
);
CREATE INDEX deployments_resource_idx ON deployments (resource_id, created_at DESC);
ALTER TABLE resources ADD CONSTRAINT resources_current_deployment_fk
    FOREIGN KEY (current_deployment_id) REFERENCES deployments (id) ON DELETE SET NULL;

-- Durable job queue. Workers claim rows with SELECT ... FOR UPDATE SKIP LOCKED and hold a lease.
CREATE TABLE jobs (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind            text NOT NULL,
    payload         jsonb NOT NULL DEFAULT '{}',
    status          text NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled')),
    resource_id     uuid REFERENCES resources (id) ON DELETE CASCADE,
    deployment_id   uuid REFERENCES deployments (id) ON DELETE CASCADE,
    attempts        integer NOT NULL DEFAULT 0,
    max_attempts    integer NOT NULL DEFAULT 3,
    run_after       timestamptz NOT NULL DEFAULT now(),
    lease_owner     text,
    lease_until     timestamptz,
    heartbeat_at    timestamptz,
    cancel_requested boolean NOT NULL DEFAULT false,
    progress        text NOT NULL DEFAULT '',
    last_error      text NOT NULL DEFAULT '',
    created_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    started_at      timestamptz,
    finished_at     timestamptz
);
CREATE INDEX jobs_claim_idx ON jobs (run_after) WHERE status = 'pending';
CREATE INDEX jobs_lease_idx ON jobs (lease_until) WHERE status = 'running';
CREATE INDEX jobs_resource_idx ON jobs (resource_id, created_at DESC);

CREATE TABLE job_logs (
    id              bigserial PRIMARY KEY,
    job_id          uuid NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    level           text NOT NULL DEFAULT 'info' CHECK (level IN ('info', 'warn', 'error')),
    step            text NOT NULL DEFAULT '',
    message         text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX job_logs_job_idx ON job_logs (job_id, id);

CREATE TABLE audit_events (
    id              bigserial PRIMARY KEY,
    actor_id        uuid REFERENCES users (id) ON DELETE SET NULL,
    actor_email     text NOT NULL DEFAULT '',
    action          text NOT NULL,
    target_type     text NOT NULL DEFAULT '',
    target_id       text NOT NULL DEFAULT '',
    detail          jsonb NOT NULL DEFAULT '{}',
    ip              text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_events_created_idx ON audit_events (created_at DESC);

CREATE TABLE settings (
    key             text PRIMARY KEY,
    value           jsonb NOT NULL,
    updated_at      timestamptz NOT NULL DEFAULT now()
);

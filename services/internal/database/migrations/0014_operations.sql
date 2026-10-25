-- Health checks, exact rollbacks, scheduled tasks, deploy hooks / resource tokens and advanced runtime options.

-- Health checks: the worker probes each running resource and keeps the recent results.
CREATE TABLE resource_health (
    resource_id          uuid PRIMARY KEY REFERENCES resources (id) ON DELETE CASCADE,
    enabled              boolean NOT NULL DEFAULT false,
    kind                 text    NOT NULL DEFAULT 'http' CHECK (kind IN ('http', 'tcp', 'command')),
    path                 text    NOT NULL DEFAULT '/',
    port                 integer CHECK (port BETWEEN 1 AND 65535), -- NULL = the resource's port
    expect_status        text    NOT NULL DEFAULT '200-399',
    command              text    NOT NULL DEFAULT '',
    interval_seconds     integer NOT NULL DEFAULT 10 CHECK (interval_seconds BETWEEN 2 AND 3600),
    timeout_seconds      integer NOT NULL DEFAULT 5  CHECK (timeout_seconds BETWEEN 1 AND 120),
    retries              integer NOT NULL DEFAULT 3  CHECK (retries BETWEEN 1 AND 20),
    start_period_seconds integer NOT NULL DEFAULT 30 CHECK (start_period_seconds BETWEEN 0 AND 900),
    restart_unhealthy    boolean NOT NULL DEFAULT true,
    -- Observed by the worker.
    state                text    NOT NULL DEFAULT 'unknown' CHECK (state IN ('unknown', 'starting', 'healthy', 'unhealthy')),
    failures             integer NOT NULL DEFAULT 0,
    last_checked_at      timestamptz,
    last_change_at       timestamptz,
    restarts             timestamptz[] NOT NULL DEFAULT '{}', -- recent automatic restarts, to stop restart loops
    updated_at           timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE health_results (
    id          bigserial PRIMARY KEY,
    resource_id uuid NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    checked_at  timestamptz NOT NULL DEFAULT now(),
    ok          boolean NOT NULL,
    latency_ms  integer NOT NULL DEFAULT 0,
    detail      text NOT NULL DEFAULT '',
    source      text NOT NULL DEFAULT 'monitor' CHECK (source IN ('monitor', 'deploy', 'test', 'restart'))
);
CREATE INDEX health_results_resource ON health_results (resource_id, id DESC);

-- Deployments remember the exact image and the variables they ran with, so a rollback restores that release.
ALTER TABLE deployments
    ADD COLUMN image_id               text NOT NULL DEFAULT '', -- sha256:… of the image the container ran
    ADD COLUMN previous_deployment_id uuid REFERENCES deployments (id) ON DELETE SET NULL,
    ADD COLUMN restore_env            boolean NOT NULL DEFAULT false;
ALTER TABLE deployments DROP CONSTRAINT deployments_trigger_check;
ALTER TABLE deployments ADD CONSTRAINT deployments_trigger_check
    CHECK (trigger IN ('manual', 'create', 'redeploy', 'rollback', 'retry', 'config', 'hook', 'token'));

CREATE TABLE deployment_env (
    deployment_id uuid NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
    key           text NOT NULL,
    value_enc     bytea NOT NULL, -- sealed for the resource, exactly as in env_vars
    secret        boolean NOT NULL DEFAULT false,
    PRIMARY KEY (deployment_id, key)
);

-- Scheduled tasks run a command inside the resource's running container.
CREATE TABLE scheduled_tasks (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id     uuid NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    name            text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    schedule        text NOT NULL,
    timezone        text NOT NULL DEFAULT 'UTC',
    command         text NOT NULL CHECK (length(command) BETWEEN 1 AND 4000),
    timeout_seconds integer NOT NULL DEFAULT 600 CHECK (timeout_seconds BETWEEN 10 AND 86400),
    enabled         boolean NOT NULL DEFAULT true,
    next_run_at     timestamptz,
    created_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (resource_id, name)
);
CREATE INDEX scheduled_tasks_due ON scheduled_tasks (next_run_at) WHERE enabled;

CREATE TABLE task_runs (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id          uuid NOT NULL REFERENCES scheduled_tasks (id) ON DELETE CASCADE,
    trigger          text NOT NULL CHECK (trigger IN ('schedule', 'manual')),
    status           text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'skipped', 'cancelled')),
    scheduled_for    timestamptz,
    cancel_requested boolean NOT NULL DEFAULT false,
    exit_code        integer,
    output           text NOT NULL DEFAULT '', -- the last 64 KB
    error            text NOT NULL DEFAULT '',
    created_by       uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    started_at       timestamptz,
    finished_at      timestamptz
);
CREATE INDEX task_runs_task ON task_runs (task_id, created_at DESC);
CREATE INDEX task_runs_queued ON task_runs (created_at) WHERE status = 'queued';
CREATE UNIQUE INDEX task_runs_once ON task_runs (task_id, scheduled_for) WHERE trigger = 'schedule';

-- Deploy hooks (one secret URL per resource) and tokens scoped to a single resource. Only hashes are
-- looked up; the hook URL is also kept sealed so it can be shown again.
CREATE TABLE resource_tokens (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id  uuid NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    kind         text NOT NULL CHECK (kind IN ('hook', 'token')),
    name         text NOT NULL DEFAULT '',
    scope        text NOT NULL DEFAULT 'deploy' CHECK (scope IN ('read', 'deploy')),
    token_hash   bytea NOT NULL UNIQUE,
    token_enc    bytea,
    hint         text NOT NULL DEFAULT '', -- last characters, to tell tokens apart
    created_by   uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz,
    last_used_at timestamptz,
    last_used_ip text NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX resource_tokens_one_hook ON resource_tokens (resource_id) WHERE kind = 'hook';

-- Advanced runtime options, applied when the container is created.
ALTER TABLE resources
    ADD COLUMN entrypoint     text    NOT NULL DEFAULT '',
    ADD COLUMN workdir        text    NOT NULL DEFAULT '',
    ADD COLUMN run_as_user    text    NOT NULL DEFAULT '',
    ADD COLUMN labels         jsonb   NOT NULL DEFAULT '{}',
    ADD COLUMN read_only_root boolean NOT NULL DEFAULT false,
    ADD COLUMN init_process   boolean NOT NULL DEFAULT false,
    ADD COLUMN privileged     boolean NOT NULL DEFAULT false;
ALTER TABLE resources DROP CONSTRAINT resources_status_check;
ALTER TABLE resources ADD CONSTRAINT resources_status_check
    CHECK (status IN ('created', 'deploying', 'running', 'stopped', 'failed', 'removing', 'paused'));

-- Automations: one engine for servers and resources. An automation has a trigger, conditions, an
-- autonomy mode and an ordered list of built-in steps; every run records its plan, each step's
-- outcome and how the outcome was verified.
CREATE TABLE automations (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name            text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    description     text NOT NULL DEFAULT '' CHECK (length(description) <= 500),
    -- What it acts on: one resource, or servers picked by a selector.
    scope           text NOT NULL CHECK (scope IN ('resource', 'servers')),
    resource_id     uuid REFERENCES resources (id) ON DELETE CASCADE,
    targets         jsonb NOT NULL DEFAULT '{}', -- servers: {"mode": "ids"|"all"|"environment"|"label", ...}
    trigger         jsonb NOT NULL,              -- {"type": "schedule"|"event"|"metric"|"manual"|"webhook", ...}
    conditions      jsonb NOT NULL DEFAULT '{}', -- cooldown, maintenance window, skip in maintenance, ...
    steps           jsonb NOT NULL,              -- [{"action": "...", "params": {...}, "onFailure": "stop"|"continue"}]
    mode            text NOT NULL DEFAULT 'assisted' CHECK (mode IN ('observe', 'assisted', 'auto')),
    enabled         boolean NOT NULL DEFAULT true,
    paused_reason   text NOT NULL DEFAULT '',    -- set when escalation paused it
    escalate_after  integer NOT NULL DEFAULT 3 CHECK (escalate_after BETWEEN 1 AND 20),
    max_runs_per_hour integer NOT NULL DEFAULT 6 CHECK (max_runs_per_hour BETWEEN 1 AND 60),
    notify_url_enc  bytea,                       -- sealed webhook for failures, approvals and escalations
    hook_hash       bytea UNIQUE,                -- webhook trigger secret (SHA-256)
    next_run_at     timestamptz,
    failures        integer NOT NULL DEFAULT 0,  -- consecutive failed runs
    created_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CHECK ((scope = 'resource') = (resource_id IS NOT NULL))
);
CREATE INDEX automations_resource ON automations (resource_id);
CREATE INDEX automations_due ON automations (next_run_at) WHERE enabled;

-- Per automation and target: when an event condition started holding and when it last fired,
-- so "sustained for 10 minutes" and cooldowns survive worker restarts.
CREATE TABLE automation_state (
    automation_id uuid NOT NULL REFERENCES automations (id) ON DELETE CASCADE,
    target_id     uuid NOT NULL,
    holding_since timestamptz,
    last_fired_at timestamptz,
    PRIMARY KEY (automation_id, target_id)
);

CREATE TABLE automation_runs (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    automation_id uuid NOT NULL REFERENCES automations (id) ON DELETE CASCADE,
    trigger       text NOT NULL CHECK (trigger IN ('schedule', 'event', 'metric', 'manual', 'webhook')),
    reason        text NOT NULL DEFAULT '',     -- why it ran, in words ("Disk 92% for 10 min on prod-1")
    status        text NOT NULL DEFAULT 'queued' CHECK (status IN (
                      'queued', 'awaiting_approval', 'approved', 'running', 'succeeded', 'failed',
                      'recommended', 'rejected', 'expired', 'skipped', 'cancelled')),
    plan          jsonb NOT NULL DEFAULT '[]',  -- [{"target": {...}, "steps": [...]}] decided before anything runs
    targets       jsonb NOT NULL DEFAULT '[]',  -- the target(s) the trigger fired for, if any
    summary       text NOT NULL DEFAULT '',
    attention     boolean NOT NULL DEFAULT false, -- shows under "Needs attention" until acknowledged
    requested_by  text NOT NULL DEFAULT 'system',
    decided_by    text NOT NULL DEFAULT '',
    decided_at    timestamptz,
    decision_note text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    started_at    timestamptz,
    finished_at   timestamptz,
    expires_at    timestamptz                    -- approvals that wait too long expire
);
CREATE INDEX automation_runs_automation ON automation_runs (automation_id, created_at DESC);
CREATE INDEX automation_runs_work ON automation_runs (created_at) WHERE status IN ('queued', 'approved');
CREATE INDEX automation_runs_attention ON automation_runs (automation_id) WHERE attention;

CREATE TABLE automation_run_steps (
    run_id       uuid NOT NULL REFERENCES automation_runs (id) ON DELETE CASCADE,
    seq          integer NOT NULL,
    target_kind  text NOT NULL,  -- server, resource
    target_id    uuid NOT NULL,
    target_name  text NOT NULL,
    action       text NOT NULL,
    status       text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'skipped')),
    output       text NOT NULL DEFAULT '',  -- what was done
    verification text NOT NULL DEFAULT '',  -- how the outcome was confirmed (or why it couldn't be)
    verified     boolean,
    ref          text NOT NULL DEFAULT '',  -- job or deployment it created, for linking
    started_at   timestamptz,
    finished_at  timestamptz,
    PRIMARY KEY (run_id, seq)
);

-- Deployments an automation starts say so.
ALTER TABLE deployments DROP CONSTRAINT deployments_trigger_check;
ALTER TABLE deployments ADD CONSTRAINT deployments_trigger_check
    CHECK (trigger IN ('manual', 'create', 'redeploy', 'rollback', 'retry', 'config', 'hook', 'token', 'automation'));

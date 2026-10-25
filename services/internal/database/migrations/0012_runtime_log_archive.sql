-- Runtime output (stdout/stderr) of containers that were replaced or failed, kept per deployment so their
-- logs survive the container being removed. The worker keeps the last 10 archived deployments per resource.
CREATE TABLE runtime_logs (
    deployment_id uuid NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
    seq           integer NOT NULL,
    at            timestamptz NOT NULL,
    stream        text NOT NULL CHECK (stream IN ('stdout', 'stderr')),
    message       text NOT NULL,
    PRIMARY KEY (deployment_id, seq)
);
CREATE INDEX runtime_logs_at ON runtime_logs (deployment_id, at);

ALTER TABLE deployments
    ADD COLUMN logs_archived_at timestamptz,
    ADD COLUMN logs_lines       integer NOT NULL DEFAULT 0;

-- Volumes: named mounts per resource. Docker volume name is ign-<resource id prefix>-<name>, so the
-- existing single data volume ("data") keeps its data.
CREATE TABLE resource_volumes (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id uuid NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    name        text NOT NULL CHECK (name ~ '^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$'),
    mount_path  text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (resource_id, name),
    UNIQUE (resource_id, mount_path)
);
INSERT INTO resource_volumes (resource_id, name, mount_path)
SELECT id, 'data', data_path FROM resources WHERE data_path <> '';

-- Limits Docker enforces on the running container (all can be changed live).
ALTER TABLE resources
    ADD COLUMN memory_reservation_mb integer CHECK (memory_reservation_mb BETWEEN 16 AND 65536),
    ADD COLUMN pids_limit            integer CHECK (pids_limit BETWEEN 16 AND 100000),
    ADD COLUMN allow_swap            boolean NOT NULL DEFAULT true, -- Docker's default: swap up to the memory limit again
    ADD COLUMN restart_policy        text    NOT NULL DEFAULT 'always' CHECK (restart_policy IN ('always', 'on-failure', 'never')),
    ADD COLUMN max_restarts          integer NOT NULL DEFAULT 5 CHECK (max_restarts BETWEEN 1 AND 100);

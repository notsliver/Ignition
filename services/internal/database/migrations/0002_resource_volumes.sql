-- Persistent data directory for a resource (e.g. /var/lib/postgresql/data for PostgreSQL).
-- When set, the worker mounts a named Docker volume there that survives redeploys.
ALTER TABLE resources ADD COLUMN data_path text NOT NULL DEFAULT ''
    CHECK (data_path = '' OR data_path ~ '^/[A-Za-z0-9._/-]*$');

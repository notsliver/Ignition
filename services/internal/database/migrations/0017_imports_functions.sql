-- Functions (a handler file run by a small built-in server), external databases (registered, not
-- run), and a history of data imports into databases.
ALTER TABLE resources DROP CONSTRAINT resources_type_check;
ALTER TABLE resources ADD CONSTRAINT resources_type_check CHECK (type IN ('application', 'database', 'worker', 'cron', 'function'));
ALTER TABLE resources DROP CONSTRAINT resources_source_type_check;
ALTER TABLE resources ADD CONSTRAINT resources_source_type_check CHECK (source_type IN ('image', 'git', 'external'));

-- engine: postgresql, mysql, mariadb, mongodb, redis, dragonfly, clickhouse (databases IGNITION runs or connects to);
-- for functions, the runtime (node, python).
ALTER TABLE resources ADD COLUMN engine text NOT NULL DEFAULT '';

CREATE TABLE data_imports (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id uuid NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    job_id      uuid REFERENCES jobs (id) ON DELETE SET NULL,
    source      text NOT NULL CHECK (source IN ('upload', 'url', 's3', 'live')),
    label       text NOT NULL DEFAULT '', -- file name, URL host or source host (never credentials)
    size_bytes  bigint,
    replace     boolean NOT NULL DEFAULT false,
    secret_enc  bytea,                     -- sealed source details (live connection URL, S3 keys)
    format      text NOT NULL DEFAULT '', -- what the file turned out to be, once inspected
    status      text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    summary     text NOT NULL DEFAULT '',
    created_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE INDEX data_imports_resource ON data_imports (resource_id, created_at DESC);

-- Work out the engine of existing databases from their image.
UPDATE resources SET engine = CASE
    WHEN image LIKE 'postgres%' OR image LIKE '%/postgres%' THEN 'postgresql'
    WHEN image LIKE 'mysql%' OR image LIKE '%/mysql%' THEN 'mysql'
    WHEN image LIKE 'mariadb%' OR image LIKE '%/mariadb%' THEN 'mariadb'
    WHEN image LIKE 'mongo%' OR image LIKE '%/mongo%' THEN 'mongodb'
    WHEN image LIKE 'redis%' OR image LIKE '%/redis%' THEN 'redis'
    WHEN image LIKE '%dragonfly%' THEN 'dragonfly'
    WHEN image LIKE '%clickhouse%' THEN 'clickhouse'
    ELSE '' END
WHERE type = 'database';

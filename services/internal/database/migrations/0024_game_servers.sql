-- Game servers: a resource whose container is described by a game adapter (internal/games). The spec
-- holds what the owner chose (game, edition, version, loader, slots...); the adapter turns it into an
-- image and environment at every deployment, so changing the version is a redeploy.
ALTER TABLE resources DROP CONSTRAINT resources_type_check;
ALTER TABLE resources ADD CONSTRAINT resources_type_check CHECK (type IN ('application', 'database', 'worker', 'cron', 'function', 'game'));

CREATE TABLE game_servers (
    resource_id   uuid PRIMARY KEY REFERENCES resources (id) ON DELETE CASCADE,
    game          text NOT NULL,
    spec          jsonb NOT NULL DEFAULT '{}',
    initialized   boolean NOT NULL DEFAULT false, -- first start done: the game's own files now own its settings
    eula_accepted_by uuid REFERENCES users (id) ON DELETE SET NULL,
    eula_accepted_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

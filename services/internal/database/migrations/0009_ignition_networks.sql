-- Networks are Ignition concepts now, not raw Docker networks:
--   * every environment has a private network (derived from its ID, nothing to store);
--   * shared networks are created by name to connect resources across projects on purpose.
CREATE TABLE shared_networks (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL UNIQUE CHECK (name ~ '^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$'),
    created_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Memberships used to name arbitrary Docker networks; those are no longer offered.
DROP TABLE resource_networks;
CREATE TABLE resource_networks (
    resource_id uuid NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    network_id  uuid NOT NULL REFERENCES shared_networks (id) ON DELETE CASCADE,
    PRIMARY KEY (resource_id, network_id)
);

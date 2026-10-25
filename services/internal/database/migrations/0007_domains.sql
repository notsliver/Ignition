-- Hostnames routed to a resource by the bundled Caddy proxy. Status is what the last real check found.
CREATE TABLE domains (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id     uuid NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    hostname        text NOT NULL CHECK (hostname = lower(hostname) AND length(hostname) BETWEEN 3 AND 253),
    port            integer CHECK (port BETWEEN 1 AND 65535), -- NULL: the resource's own port
    https           boolean NOT NULL DEFAULT true,
    is_primary      boolean NOT NULL DEFAULT false,
    token           text NOT NULL,                             -- served at /.well-known/ignition-check to prove routing
    status          text NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'dns-missing', 'unreachable', 'active')),
    status_message  text NOT NULL DEFAULT '',
    addresses       text[] NOT NULL DEFAULT '{}',
    cert_issuer     text NOT NULL DEFAULT '',
    cert_expires_at timestamptz,
    last_checked_at timestamptz,
    created_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX domains_hostname ON domains (hostname);
CREATE INDEX domains_resource ON domains (resource_id);
CREATE UNIQUE INDEX domains_one_primary ON domains (resource_id) WHERE is_primary;

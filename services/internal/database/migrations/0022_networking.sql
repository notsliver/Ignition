-- Networking: traffic routing (paths, redirects, IP allow lists) on domains, DNS provider
-- integrations, subnets and IPv6 on shared networks, static addresses for resources, port
-- reservations and host firewall rules.

-- A hostname can now route different paths to different resources, or redirect.
DROP INDEX domains_hostname;
ALTER TABLE domains
    ADD COLUMN path_prefix   text NOT NULL DEFAULT '' CHECK (path_prefix = '' OR path_prefix ~ '^/[A-Za-z0-9._~!$&''()*+,;=:@/-]*$'),
    ADD COLUMN strip_prefix  boolean NOT NULL DEFAULT false,
    ADD COLUMN redirect_to   text NOT NULL DEFAULT '' CHECK (length(redirect_to) <= 500),
    ADD COLUMN redirect_code integer NOT NULL DEFAULT 308 CHECK (redirect_code IN (301, 302, 307, 308)),
    ADD COLUMN allow_ips     text[] NOT NULL DEFAULT '{}',
    ADD COLUMN dns_provider_id uuid,
    ADD COLUMN dns_records   jsonb NOT NULL DEFAULT '[]';
CREATE UNIQUE INDEX domains_hostname_path ON domains (hostname, path_prefix);

CREATE TABLE dns_providers (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
    kind        text NOT NULL CHECK (kind IN ('cloudflare')),
    token_enc   bytea NOT NULL,
    zones       text[] NOT NULL DEFAULT '{}', -- zones the token can edit, as of the last check
    verified_at timestamptz,
    created_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE domains ADD CONSTRAINT domains_dns_provider FOREIGN KEY (dns_provider_id) REFERENCES dns_providers (id) ON DELETE SET NULL;

ALTER TABLE shared_networks
    ADD COLUMN subnet      text NOT NULL DEFAULT '',
    ADD COLUMN gateway     text NOT NULL DEFAULT '',
    ADD COLUMN ipv6        boolean NOT NULL DEFAULT false,
    ADD COLUMN subnet6     text NOT NULL DEFAULT '',
    ADD COLUMN internal    boolean NOT NULL DEFAULT false,
    ADD COLUMN description text NOT NULL DEFAULT '',
    ADD COLUMN pinned      boolean NOT NULL DEFAULT false; -- created on the Networking page; kept without members

ALTER TABLE resource_networks
    ADD COLUMN ipv4 text NOT NULL DEFAULT '',
    ADD COLUMN ipv6 text NOT NULL DEFAULT '';

CREATE TABLE port_reservations (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    port       integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
    protocol   text NOT NULL CHECK (protocol IN ('tcp', 'udp')),
    note       text NOT NULL DEFAULT '' CHECK (length(note) <= 200),
    created_by uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (port, protocol)
);

CREATE TABLE firewall_rules (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    server_id  uuid NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    port       integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
    protocol   text NOT NULL CHECK (protocol IN ('tcp', 'udp')),
    sources    text[] NOT NULL DEFAULT '{}', -- CIDRs allowed; everything else is dropped
    note       text NOT NULL DEFAULT '' CHECK (length(note) <= 200),
    enabled    boolean NOT NULL DEFAULT true,
    created_by uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (server_id, port, protocol)
);

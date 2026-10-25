-- Host-to-container port mappings. host_port NULL lets Docker pick a free port.
-- bind_address 127.0.0.1 = this machine only; 0.0.0.0 = every interface (LAN, and the internet if forwarded).
CREATE TABLE resource_ports (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id     uuid NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    host_port       integer CHECK (host_port BETWEEN 1 AND 65535),
    container_port  integer NOT NULL CHECK (container_port BETWEEN 1 AND 65535),
    protocol        text NOT NULL DEFAULT 'tcp' CHECK (protocol IN ('tcp', 'udp')),
    bind_address    text NOT NULL DEFAULT '127.0.0.1' CHECK (bind_address IN ('127.0.0.1', '0.0.0.0')),
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX resource_ports_resource ON resource_ports (resource_id);
CREATE UNIQUE INDEX resource_ports_host ON resource_ports (host_port, protocol) WHERE host_port IS NOT NULL;

-- Keep what already runs: until now every resource with a port was published on all interfaces at a random port.
INSERT INTO resource_ports (resource_id, container_port, bind_address)
SELECT id, port, '0.0.0.0' FROM resources WHERE port IS NOT NULL;

-- Extra DNS names (besides the resource name) and extra Docker networks to join (besides "ignition").
ALTER TABLE resources ADD COLUMN aliases text[] NOT NULL DEFAULT '{}';
CREATE TABLE resource_networks (
    resource_id uuid NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    network     text NOT NULL,
    PRIMARY KEY (resource_id, network)
);

-- Results of DNS/TCP tests run from a probe container on the resource's networks.
CREATE TABLE network_tests (
    id          bigserial PRIMARY KEY,
    resource_id uuid NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    kind        text NOT NULL CHECK (kind IN ('dns', 'tcp')),
    target      text NOT NULL,
    ok          boolean NOT NULL,
    summary     text NOT NULL,
    output      text NOT NULL DEFAULT '',
    ran_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    ran_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX network_tests_resource ON network_tests (resource_id, ran_at DESC);

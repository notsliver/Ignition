-- Instance-wide integrations that deployments pull inputs from: Git providers and container registries.
-- Credentials are AES-256-GCM ciphertext bound to the source id; they are never returned by the API.
CREATE TABLE sources (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name            text NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
    kind            text NOT NULL CHECK (kind IN ('git', 'registry')),
    provider        text NOT NULL CHECK (provider IN ('github', 'gitlab', 'bitbucket', 'git', 'dockerhub', 'ghcr', 'gitlab-registry', 'registry')),
    url             text NOT NULL DEFAULT '',          -- API/host for self-hosted providers, or registry address
    auth_type       text NOT NULL CHECK (auth_type IN ('none', 'token', 'password', 'ssh')),
    username        text NOT NULL DEFAULT '',
    secret_enc      bytea,                             -- token, password or SSH private key
    secret_hint     text NOT NULL DEFAULT '',          -- last 4 characters, for recognising rotated secrets
    status          text NOT NULL DEFAULT 'untested' CHECK (status IN ('untested', 'connected', 'failed')),
    status_message  text NOT NULL DEFAULT '',
    account         text NOT NULL DEFAULT '',          -- who the credentials authenticate as, from the last test
    last_checked_at timestamptz,
    created_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX sources_name_key ON sources (lower(name));

-- A resource may pull its code or image through a source. Deleting a source in use is refused.
ALTER TABLE resources ADD COLUMN source_id uuid REFERENCES sources (id) ON DELETE RESTRICT;
CREATE INDEX resources_source_idx ON resources (source_id) WHERE source_id IS NOT NULL;

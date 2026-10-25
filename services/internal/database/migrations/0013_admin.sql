-- One-time links: invitations (create an account) and password resets. Only a SHA-256 of the token is stored.
CREATE TABLE user_tokens (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind        text NOT NULL CHECK (kind IN ('invite', 'reset')),
    token_hash  bytea NOT NULL UNIQUE,
    email       text NOT NULL,
    role        text CHECK (role IN ('owner', 'admin', 'developer', 'viewer')),
    user_id     uuid REFERENCES users (id) ON DELETE CASCADE, -- the account a reset belongs to
    created_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz
);
CREATE INDEX user_tokens_pending ON user_tokens (kind, expires_at) WHERE used_at IS NULL;

CREATE INDEX audit_events_created ON audit_events (created_at DESC);
CREATE INDEX audit_events_actor ON audit_events (actor_email);

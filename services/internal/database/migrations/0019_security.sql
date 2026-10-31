-- Two-factor authentication (TOTP secret sealed with the instance key, hashed recovery codes),
-- per-user permission overrides on top of the role, and accounts linked to an OpenID Connect
-- identity. Sessions remember how they were started.
ALTER TABLE users
    ADD COLUMN totp_secret_enc  bytea,
    ADD COLUMN totp_enabled_at  timestamptz,
    ADD COLUMN recovery_codes   jsonb NOT NULL DEFAULT '[]',
    ADD COLUMN permissions      jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN oidc_subject     text;
CREATE UNIQUE INDEX users_oidc_subject_key ON users (oidc_subject) WHERE oidc_subject IS NOT NULL;
ALTER TABLE sessions ADD COLUMN method text NOT NULL DEFAULT 'password';

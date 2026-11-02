ALTER TABLE sessions ADD COLUMN reauthenticated_at timestamptz;
ALTER TABLE users ADD COLUMN oidc_issuer text NOT NULL DEFAULT '';
UPDATE users SET oidc_issuer=coalesce((SELECT value->>'issuer' FROM settings WHERE key='oidc'),'') WHERE oidc_subject IS NOT NULL;
DROP INDEX users_oidc_subject_key;
CREATE UNIQUE INDEX users_oidc_identity_key ON users(oidc_issuer,oidc_subject) WHERE oidc_subject IS NOT NULL;

CREATE TABLE account_passkeys (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 credential_id bytea NOT NULL UNIQUE,
 credential jsonb NOT NULL,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 80),
 created_at timestamptz NOT NULL DEFAULT now(), last_used_at timestamptz
);
CREATE INDEX account_passkeys_user_idx ON account_passkeys(user_id);
CREATE TABLE account_challenges (
 token_hash bytea PRIMARY KEY, browser_hash bytea NOT NULL,
 user_id uuid REFERENCES users(id) ON DELETE CASCADE,
 purpose text NOT NULL, data jsonb NOT NULL, name text NOT NULL DEFAULT '',
 expires_at timestamptz NOT NULL DEFAULT now()+interval '5 minutes'
);

ALTER TABLE team_invitations DROP CONSTRAINT team_invitations_created_by_fkey;
ALTER TABLE team_invitations ADD CONSTRAINT team_invitations_created_by_fkey FOREIGN KEY(created_by) REFERENCES users(id) ON DELETE SET NULL;

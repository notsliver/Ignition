CREATE TABLE instance_integrations (
 provider text PRIMARY KEY, config jsonb NOT NULL DEFAULT '{}',
 login_secret_enc bytea, source_secret_enc bytea,
 revision bigint NOT NULL DEFAULT 1, updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO instance_integrations(provider) VALUES('github'),('gitlab'),('bitbucket'),('git'),('dockerhub'),('ghcr'),('gitlab-registry'),('registry');
CREATE TABLE account_integration_identities (
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 provider text NOT NULL REFERENCES instance_integrations(provider),
 origin text NOT NULL, subject text NOT NULL, username text NOT NULL, email text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(user_id,provider), UNIQUE(provider,origin,subject)
);
ALTER TABLE sources ADD COLUMN oauth_provider text REFERENCES instance_integrations(provider);
ALTER TABLE sources ADD COLUMN oauth_user_id uuid REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE sources ADD COLUMN oauth_revision bigint;
ALTER TABLE sources ADD COLUMN oauth_scopes text[] NOT NULL DEFAULT '{}';
CREATE UNIQUE INDEX sources_oauth_connection ON sources(team_id,oauth_provider,oauth_user_id) WHERE oauth_provider IS NOT NULL;
CREATE TABLE integration_oauth_states (
 token_hash bytea PRIMARY KEY, browser_hash bytea NOT NULL,
 session_hash bytea REFERENCES sessions(token_hash) ON DELETE CASCADE,
 user_id uuid REFERENCES users(id) ON DELETE CASCADE,
 team_id uuid REFERENCES teams(id) ON DELETE CASCADE,
 provider text NOT NULL REFERENCES instance_integrations(provider),
 purpose text NOT NULL CHECK(purpose IN ('login','account','source','reauth')),
 return_to text NOT NULL, verifier_enc bytea NOT NULL,
 revision bigint NOT NULL, scopes text[] NOT NULL,
 expires_at timestamptz NOT NULL DEFAULT now()+interval '10 minutes'
);

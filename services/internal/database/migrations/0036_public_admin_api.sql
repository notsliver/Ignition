CREATE TABLE IF NOT EXISTS admin_tokens (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), created_by uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 80), prefix text NOT NULL,
 token_hash bytea UNIQUE NOT NULL, permissions text[] NOT NULL, networks text[] NOT NULL DEFAULT '{}',
 expires_at timestamptz NOT NULL, revoked_at timestamptz, created_at timestamptz NOT NULL DEFAULT now(),
 last_used_at timestamptz, last_ip text NOT NULL DEFAULT '', rotated_from uuid REFERENCES admin_tokens(id) ON DELETE SET NULL
);
-- An earlier instance-token prototype may have been applied by another checkout.
-- Preserve its rows and metadata, but never guess at the authority of legacy scopes.
ALTER TABLE admin_tokens ADD COLUMN IF NOT EXISTS permissions text[] NOT NULL DEFAULT '{}';
ALTER TABLE admin_tokens ADD COLUMN IF NOT EXISTS networks text[] NOT NULL DEFAULT '{}';
ALTER TABLE admin_tokens ADD COLUMN IF NOT EXISTS revoked_at timestamptz;
ALTER TABLE admin_tokens ADD COLUMN IF NOT EXISTS last_ip text NOT NULL DEFAULT '';
ALTER TABLE admin_tokens ADD COLUMN IF NOT EXISTS rotated_from uuid REFERENCES admin_tokens(id) ON DELETE SET NULL;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='admin_tokens' AND column_name='scopes') THEN
  UPDATE admin_tokens SET permissions='{}',networks=allow_ips,last_ip=last_used_ip,revoked_at=coalesce(revoked_at,now()),expires_at=coalesce(expires_at,now());
  ALTER TABLE admin_tokens ALTER COLUMN scopes SET DEFAULT '{}';
 END IF;
END $$;
UPDATE admin_tokens SET expires_at=now(),revoked_at=coalesce(revoked_at,now()) WHERE expires_at IS NULL;
ALTER TABLE admin_tokens ALTER COLUMN expires_at SET NOT NULL;
ALTER TABLE admin_tokens DROP CONSTRAINT IF EXISTS admin_tokens_name_check;
ALTER TABLE admin_tokens ADD CONSTRAINT admin_tokens_name_check CHECK(length(name) BETWEEN 1 AND 80);
CREATE TABLE admin_external_ids (
 kind text NOT NULL CHECK(kind IN ('user','team','resource')), object_id uuid NOT NULL,
 external_id text NOT NULL CHECK(length(external_id) BETWEEN 1 AND 200), PRIMARY KEY(kind,object_id), UNIQUE(kind,external_id)
);
CREATE TABLE admin_onboarding (
 user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, method text NOT NULL CHECK(method IN ('password','sso')),
 completed_at timestamptz, created_team_id uuid REFERENCES teams(id) ON DELETE SET NULL
);
CREATE TABLE admin_operations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), token_id uuid REFERENCES admin_tokens(id) ON DELETE SET NULL,
 actor_id uuid REFERENCES users(id) ON DELETE SET NULL, team_id uuid REFERENCES teams(id) ON DELETE SET NULL,
 resource_id uuid NOT NULL, type text NOT NULL, permission text NOT NULL,
 job_id uuid, deployment_id uuid, status text NOT NULL DEFAULT 'queued', error text,
 details jsonb NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL DEFAULT now(), completed_at timestamptz
);
CREATE INDEX admin_operations_resource ON admin_operations(resource_id,created_at DESC);
ALTER TABLE services ADD COLUMN original_resource_id uuid;
UPDATE services SET original_resource_id=resource_id;
CREATE UNIQUE INDEX services_original_resource ON services(original_resource_id) WHERE original_resource_id IS NOT NULL;
CREATE FUNCTION ignition_service_original_resource() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 NEW.original_resource_id=coalesce(NEW.original_resource_id,NEW.resource_id); RETURN NEW; END $$;
CREATE TRIGGER services_keep_resource_id BEFORE INSERT ON services FOR EACH ROW EXECUTE FUNCTION ignition_service_original_resource();
CREATE TABLE admin_idempotency (
 token_id uuid NOT NULL REFERENCES admin_tokens(id) ON DELETE CASCADE, key_hash bytea NOT NULL,
 request_hash bytea NOT NULL, response_enc bytea NOT NULL, status integer NOT NULL,
 expires_at timestamptz NOT NULL DEFAULT now()+interval '24 hours', PRIMARY KEY(token_id,key_hash)
);
CREATE TABLE admin_webhooks (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), created_by uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 url text NOT NULL, events text[] NOT NULL, enabled boolean NOT NULL DEFAULT true,
 secret_enc bytea NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE admin_webhook_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), operation_id uuid UNIQUE REFERENCES admin_operations(id) ON DELETE SET NULL,
 type text NOT NULL, payload jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE admin_webhook_deliveries (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), webhook_id uuid NOT NULL REFERENCES admin_webhooks(id) ON DELETE CASCADE,
 event_id uuid NOT NULL REFERENCES admin_webhook_events(id) ON DELETE CASCADE,
 attempts integer NOT NULL DEFAULT 0, status text NOT NULL DEFAULT 'pending', response_status integer,
 next_attempt_at timestamptz NOT NULL DEFAULT now(), leased_until timestamptz,
 last_error text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(webhook_id,event_id)
);

CREATE TABLE admin_setup_links (
 token_hash bytea PRIMARY KEY, user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 expires_at timestamptz NOT NULL, used_at timestamptz
);

CREATE TABLE admin_resource_metadata (
 resource_id uuid PRIMARY KEY REFERENCES resources(id) ON DELETE CASCADE,
 note text NOT NULL DEFAULT '' CHECK(length(note)<=2000)
);

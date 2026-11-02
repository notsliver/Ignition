-- A short public team ID: 16 random lowercase letters and digits, shown on the Team page and used in
-- browser URLs (/teams/<id>/…). The UUID stays the internal key for references, row-level security
-- and the API. It lives in the existing unique slug column, which nothing displayed yet.
CREATE FUNCTION ignition_team_code() RETURNS text LANGUAGE sql VOLATILE AS $$
 SELECT string_agg(substr('abcdefghijklmnopqrstuvwxyz0123456789', 1 + floor(random() * 36)::int, 1), '')
 FROM generate_series(1, 16)
$$;
UPDATE teams SET slug = ignition_team_code() WHERE slug !~ '^[a-z0-9]{16}$';
ALTER TABLE teams ALTER COLUMN slug SET DEFAULT ignition_team_code();
ALTER TABLE teams ADD CONSTRAINT teams_slug_format CHECK (slug ~ '^[a-z0-9]{16}$');

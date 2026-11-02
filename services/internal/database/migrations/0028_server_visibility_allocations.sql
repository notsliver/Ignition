-- Server visibility: a server teams can't see isn't offered to them, so nothing new is deployed to
-- it (for a private, reserved or maintenance machine). Workloads already on it keep running.
ALTER TABLE servers ADD COLUMN visible boolean NOT NULL DEFAULT true;

-- Capacity stays automatic, but only for visible servers.
CREATE OR REPLACE FUNCTION ignition_assign_new_team() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  INSERT INTO team_server_assignments (team_id, server_id) SELECT NEW.id, s.id FROM servers s WHERE s.visible ON CONFLICT DO NOTHING;
  RETURN NEW;
END $$;
CREATE OR REPLACE FUNCTION ignition_assign_new_server() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.visible THEN
    INSERT INTO team_server_assignments (team_id, server_id) SELECT t.id, NEW.id FROM teams t ON CONFLICT DO NOTHING;
  END IF;
  RETURN NEW;
END $$;
CREATE FUNCTION ignition_server_visibility() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.visible AND NOT OLD.visible THEN
    INSERT INTO team_server_assignments (team_id, server_id) SELECT t.id, NEW.id FROM teams t ON CONFLICT DO NOTHING;
  ELSIF OLD.visible AND NOT NEW.visible THEN
    DELETE FROM team_server_assignments WHERE server_id = NEW.id;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER servers_visibility AFTER UPDATE OF visible ON servers FOR EACH ROW EXECUTE FUNCTION ignition_server_visibility();

-- Allocations: the IP:port pairs a server hands out (a hosting provider's pool). Game servers take
-- a free one; deleting the resource frees it again.
CREATE TABLE server_allocations (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    server_id   uuid NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    ip          text NOT NULL,
    port        integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
    alias       text NOT NULL DEFAULT '',
    resource_id uuid REFERENCES resources (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (server_id, ip, port)
);
CREATE INDEX server_allocations_free ON server_allocations (server_id, port) WHERE resource_id IS NULL;
CREATE INDEX server_allocations_resource ON server_allocations (resource_id);
GRANT SELECT, INSERT, UPDATE, DELETE ON server_allocations TO ignition_request;

-- Deployment capacity is automatic: every team can deploy to every server. The assignment table
-- stays (server visibility and row-level security are built on it); it's now kept complete by
-- triggers instead of being chosen by hand. SECURITY DEFINER so a request creating a server or team
-- under the restricted request role can still record it for every team.
INSERT INTO team_server_assignments (team_id, server_id)
SELECT t.id, s.id FROM teams t CROSS JOIN servers s
ON CONFLICT DO NOTHING;

CREATE FUNCTION ignition_assign_new_team() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  INSERT INTO team_server_assignments (team_id, server_id) SELECT NEW.id, s.id FROM servers s ON CONFLICT DO NOTHING;
  RETURN NEW;
END $$;
CREATE TRIGGER teams_capacity AFTER INSERT ON teams FOR EACH ROW EXECUTE FUNCTION ignition_assign_new_team();

CREATE FUNCTION ignition_assign_new_server() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  INSERT INTO team_server_assignments (team_id, server_id) SELECT t.id, NEW.id FROM teams t ON CONFLICT DO NOTHING;
  RETURN NEW;
END $$;
CREATE TRIGGER servers_capacity AFTER INSERT ON servers FOR EACH ROW EXECUTE FUNCTION ignition_assign_new_server();

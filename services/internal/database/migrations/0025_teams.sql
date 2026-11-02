-- Team ownership. Existing IDs, deployment networks, and ciphertext are preserved.
CREATE TABLE teams (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
 slug text NOT NULL UNIQUE, created_by uuid REFERENCES users(id) ON DELETE SET NULL, created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO teams(id,name,slug) VALUES ('00000000-0000-4000-8000-000000000001','Ignition','ignition');
CREATE TABLE team_memberships (
 team_id uuid NOT NULL REFERENCES teams(id), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 role text NOT NULL CHECK(role IN ('owner','admin','developer','viewer','member')),
 permissions jsonb NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(team_id,user_id)
);
INSERT INTO team_memberships(team_id,user_id,role,permissions) SELECT '00000000-0000-4000-8000-000000000001',id,role,permissions FROM users;
CREATE TABLE team_experiences (
 team_id uuid NOT NULL REFERENCES teams(id), experience text NOT NULL CHECK(experience IN ('infrastructure','hosting')), PRIMARY KEY(team_id,experience)
);
INSERT INTO team_experiences VALUES ('00000000-0000-4000-8000-000000000001','infrastructure');
INSERT INTO team_experiences SELECT '00000000-0000-4000-8000-000000000001','hosting' WHERE EXISTS(SELECT 1 FROM settings WHERE key='onboarding' AND value->>'useCase'='hosting');
CREATE TABLE team_server_assignments (
 team_id uuid NOT NULL REFERENCES teams(id),server_id uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,PRIMARY KEY(team_id,server_id)
);
INSERT INTO team_server_assignments SELECT '00000000-0000-4000-8000-000000000001',id FROM servers;
CREATE TABLE team_invitations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),team_id uuid NOT NULL REFERENCES teams(id),email text NOT NULL,
 role text NOT NULL CHECK(role IN ('admin','developer','viewer','member')),token_hash bytea NOT NULL UNIQUE,
 created_by uuid REFERENCES users(id),created_at timestamptz NOT NULL DEFAULT now(),expires_at timestamptz NOT NULL,accepted_at timestamptz
);
CREATE FUNCTION ignition_team() RETURNS uuid LANGUAGE sql STABLE AS $$
 SELECT nullif(current_setting('ignition.team_id',true),'')::uuid
$$;
-- The fallback is exclusively for privileged maintenance/legacy worker writes. Request RLS fails closed without a scope.
CREATE FUNCTION ignition_write_team() RETURNS uuid LANGUAGE sql STABLE AS $$
 SELECT coalesce(ignition_team(),'00000000-0000-4000-8000-000000000001'::uuid)
$$;
ALTER TABLE projects ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX projects_team_idx ON projects(team_id);
ALTER TABLE environments ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX environments_team_idx ON environments(team_id);
ALTER TABLE resources ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX resources_team_idx ON resources(team_id);
ALTER TABLE sources ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX sources_team_idx ON sources(team_id);
ALTER TABLE shared_networks ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX shared_networks_team_idx ON shared_networks(team_id);
ALTER TABLE dns_providers ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX dns_providers_team_idx ON dns_providers(team_id);
ALTER TABLE automations ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX automations_team_idx ON automations(team_id);
ALTER TABLE project_favorites ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX project_favorites_team_idx ON project_favorites(team_id);
ALTER TABLE env_vars ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX env_vars_team_idx ON env_vars(team_id);
ALTER TABLE deployments ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX deployments_team_idx ON deployments(team_id);
ALTER TABLE resource_ports ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX resource_ports_team_idx ON resource_ports(team_id);
ALTER TABLE resource_volumes ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX resource_volumes_team_idx ON resource_volumes(team_id);
ALTER TABLE resource_networks ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX resource_networks_team_idx ON resource_networks(team_id);
ALTER TABLE network_tests ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX network_tests_team_idx ON network_tests(team_id);
ALTER TABLE metric_samples ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX metric_samples_team_idx ON metric_samples(team_id);
ALTER TABLE runtime_logs ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX runtime_logs_team_idx ON runtime_logs(team_id);
ALTER TABLE resource_health ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX resource_health_team_idx ON resource_health(team_id);
ALTER TABLE health_results ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX health_results_team_idx ON health_results(team_id);
ALTER TABLE deployment_env ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX deployment_env_team_idx ON deployment_env(team_id);
ALTER TABLE scheduled_tasks ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX scheduled_tasks_team_idx ON scheduled_tasks(team_id);
ALTER TABLE resource_tokens ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX resource_tokens_team_idx ON resource_tokens(team_id);
ALTER TABLE task_runs ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX task_runs_team_idx ON task_runs(team_id);
ALTER TABLE data_imports ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX data_imports_team_idx ON data_imports(team_id);
ALTER TABLE game_servers ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX game_servers_team_idx ON game_servers(team_id);
ALTER TABLE automation_state ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX automation_state_team_idx ON automation_state(team_id);
ALTER TABLE automation_runs ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX automation_runs_team_idx ON automation_runs(team_id);
ALTER TABLE automation_run_steps ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX automation_run_steps_team_idx ON automation_run_steps(team_id);
ALTER TABLE jobs ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX jobs_team_idx ON jobs(team_id);
ALTER TABLE job_logs ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX job_logs_team_idx ON job_logs(team_id);
ALTER TABLE audit_events ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX audit_events_team_idx ON audit_events(team_id);
ALTER TABLE projects ADD UNIQUE(team_id,id);
DROP INDEX projects_name_key;
CREATE UNIQUE INDEX projects_team_name ON projects(team_id,lower(name));
ALTER TABLE environments ADD UNIQUE(team_id,id);
ALTER TABLE resources ADD UNIQUE(team_id,id);
ALTER TABLE sources ADD UNIQUE(team_id,id);
ALTER TABLE shared_networks ADD UNIQUE(team_id,id);
ALTER TABLE dns_providers ADD UNIQUE(team_id,id);
ALTER TABLE resources ALTER COLUMN project_id DROP NOT NULL;
ALTER TABLE environments ALTER COLUMN project_id DROP NOT NULL;
ALTER TABLE resources DROP CONSTRAINT resources_project_id_fkey;
ALTER TABLE environments DROP CONSTRAINT environments_project_id_fkey;
ALTER TABLE resources ADD FOREIGN KEY(team_id,project_id) REFERENCES projects(team_id,id);
ALTER TABLE resources ADD FOREIGN KEY(team_id,environment_id) REFERENCES environments(team_id,id);
ALTER TABLE resources ADD FOREIGN KEY(team_id,source_id) REFERENCES sources(team_id,id);
ALTER TABLE environments ADD FOREIGN KEY(team_id,project_id) REFERENCES projects(team_id,id);
ALTER TABLE resource_networks ADD FOREIGN KEY(team_id,network_id) REFERENCES shared_networks(team_id,id);
ALTER TABLE domains ADD COLUMN team_id uuid NOT NULL DEFAULT ignition_write_team() REFERENCES teams(id);
CREATE INDEX domains_team_idx ON domains(team_id);
ALTER TABLE domains ADD FOREIGN KEY(team_id,dns_provider_id) REFERENCES dns_providers(team_id,id);
CREATE TABLE project_environments (
 team_id uuid NOT NULL DEFAULT ignition_write_team(),project_id uuid NOT NULL,environment_id uuid NOT NULL,
 PRIMARY KEY(project_id,environment_id), FOREIGN KEY(team_id,project_id) REFERENCES projects(team_id,id) ON DELETE CASCADE,
 FOREIGN KEY(team_id,environment_id) REFERENCES environments(team_id,id) ON DELETE CASCADE
);
INSERT INTO project_environments(team_id,project_id,environment_id) SELECT team_id,project_id,id FROM environments;
CREATE TABLE project_assignments (
 team_id uuid NOT NULL,project_id uuid NOT NULL,user_id uuid NOT NULL,
 role text NOT NULL CHECK(role IN ('admin','developer','viewer')),permissions jsonb NOT NULL DEFAULT '{}',
 PRIMARY KEY(project_id,user_id), FOREIGN KEY(team_id,project_id) REFERENCES projects(team_id,id) ON DELETE CASCADE,
 FOREIGN KEY(team_id,user_id) REFERENCES team_memberships(team_id,user_id) ON DELETE CASCADE
);
ALTER TABLE environments ADD COLUMN is_team_default boolean NOT NULL DEFAULT false;
-- A separate default environment supports workloads without a project.
INSERT INTO environments(team_id,name,is_team_default) SELECT id,'Production',true FROM teams;
CREATE UNIQUE INDEX environments_team_default ON environments(team_id) WHERE is_team_default;
ALTER TABLE user_tokens ADD COLUMN team_id uuid REFERENCES teams(id);
UPDATE user_tokens SET team_id='00000000-0000-4000-8000-000000000001' WHERE kind='invite';
CREATE FUNCTION ignition_rank(role text) RETURNS integer LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE role WHEN 'owner' THEN 4 WHEN 'admin' THEN 3 WHEN 'developer' THEN 2 WHEN 'viewer' THEN 1 ELSE 0 END
$$;
CREATE FUNCTION ignition_access(project uuid) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public,pg_temp AS $$
 SELECT coalesce((SELECT
   CASE WHEN coalesce(m.permissions->'deny','[]') ? current_setting('ignition.permission',true) OR coalesce(a.permissions->'deny','[]') ? current_setting('ignition.permission',true) THEN false
   ELSE ignition_rank(m.role)>=coalesce(nullif(current_setting('ignition.min_rank',true),''),'1')::integer
     OR coalesce(m.permissions->'grant','[]') ? current_setting('ignition.permission',true)
     OR (a.project_id IS NOT NULL AND (ignition_rank(a.role)>=coalesce(nullif(current_setting('ignition.min_rank',true),''),'1')::integer OR coalesce(a.permissions->'grant','[]') ? current_setting('ignition.permission',true))) END
 FROM team_memberships m LEFT JOIN project_assignments a ON a.team_id=m.team_id AND a.user_id=m.user_id AND a.project_id=project
 WHERE m.team_id=ignition_team() AND m.user_id=nullif(current_setting('ignition.user_id',true),'')::uuid
 AND (project IS NULL OR EXISTS(SELECT 1 FROM projects p WHERE p.id=project AND p.team_id=m.team_id))),false)
$$;
-- Triggers derive inherited ownership and reject cross-team writes even on privileged paths.
CREATE FUNCTION ignition_inherit_team() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE parent_team uuid; parent_id uuid;
BEGIN
 IF TG_NARGS>0 THEN
   parent_id := nullif(to_jsonb(NEW)->>TG_ARGV[1],'')::uuid;
   IF parent_id IS NOT NULL THEN
     EXECUTE format('SELECT team_id FROM %I WHERE %I=$1',TG_ARGV[0],TG_ARGV[2]) INTO parent_team USING parent_id;
     IF parent_team IS NULL THEN RAISE EXCEPTION 'Ownership target is unavailable' USING ERRCODE='23503'; END IF;
     IF ignition_team() IS NOT NULL AND parent_team<>ignition_team() THEN RAISE EXCEPTION 'Ownership mismatch' USING ERRCODE='23503'; END IF;
     IF TG_OP='INSERT' THEN NEW.team_id:=parent_team;
     ELSIF NEW.team_id<>parent_team THEN RAISE EXCEPTION 'Ownership mismatch' USING ERRCODE='23503'; END IF;
   END IF;
 END IF;
 IF TG_OP='UPDATE' AND NEW.team_id<>OLD.team_id THEN RAISE EXCEPTION 'Team ownership is immutable' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER projects_ownership BEFORE INSERT OR UPDATE ON projects FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team();
CREATE TRIGGER environments_ownership BEFORE INSERT OR UPDATE ON environments FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('projects','project_id','id');
CREATE TRIGGER resources_ownership BEFORE INSERT OR UPDATE ON resources FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('environments','environment_id','id');
CREATE TRIGGER sources_ownership BEFORE INSERT OR UPDATE ON sources FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team();
CREATE TRIGGER shared_networks_ownership BEFORE INSERT OR UPDATE ON shared_networks FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team();
CREATE TRIGGER dns_providers_ownership BEFORE INSERT OR UPDATE ON dns_providers FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team();
CREATE TRIGGER automations_ownership BEFORE INSERT OR UPDATE ON automations FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('resources','resource_id','id');
CREATE TRIGGER project_favorites_ownership BEFORE INSERT OR UPDATE ON project_favorites FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('projects','project_id','id');
CREATE TRIGGER env_vars_ownership BEFORE INSERT OR UPDATE ON env_vars FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('resources','resource_id','id');
CREATE TRIGGER deployments_ownership BEFORE INSERT OR UPDATE ON deployments FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('resources','resource_id','id');
CREATE TRIGGER resource_ports_ownership BEFORE INSERT OR UPDATE ON resource_ports FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('resources','resource_id','id');
CREATE TRIGGER resource_volumes_ownership BEFORE INSERT OR UPDATE ON resource_volumes FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('resources','resource_id','id');
CREATE TRIGGER resource_networks_ownership BEFORE INSERT OR UPDATE ON resource_networks FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('resources','resource_id','id');
CREATE TRIGGER network_tests_ownership BEFORE INSERT OR UPDATE ON network_tests FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('resources','resource_id','id');
CREATE TRIGGER metric_samples_ownership BEFORE INSERT OR UPDATE ON metric_samples FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('resources','resource_id','id');
CREATE TRIGGER runtime_logs_ownership BEFORE INSERT OR UPDATE ON runtime_logs FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('deployments','deployment_id','id');
CREATE TRIGGER resource_health_ownership BEFORE INSERT OR UPDATE ON resource_health FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('resources','resource_id','id');
CREATE TRIGGER health_results_ownership BEFORE INSERT OR UPDATE ON health_results FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('resources','resource_id','id');
CREATE TRIGGER deployment_env_ownership BEFORE INSERT OR UPDATE ON deployment_env FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('deployments','deployment_id','id');
CREATE TRIGGER scheduled_tasks_ownership BEFORE INSERT OR UPDATE ON scheduled_tasks FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('resources','resource_id','id');
CREATE TRIGGER resource_tokens_ownership BEFORE INSERT OR UPDATE ON resource_tokens FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('resources','resource_id','id');
CREATE TRIGGER task_runs_ownership BEFORE INSERT OR UPDATE ON task_runs FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('scheduled_tasks','task_id','id');
CREATE TRIGGER data_imports_ownership BEFORE INSERT OR UPDATE ON data_imports FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('resources','resource_id','id');
CREATE TRIGGER game_servers_ownership BEFORE INSERT OR UPDATE ON game_servers FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('resources','resource_id','id');
CREATE TRIGGER automation_state_ownership BEFORE INSERT OR UPDATE ON automation_state FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('automations','automation_id','id');
CREATE TRIGGER automation_runs_ownership BEFORE INSERT OR UPDATE ON automation_runs FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('automations','automation_id','id');
CREATE TRIGGER automation_run_steps_ownership BEFORE INSERT OR UPDATE ON automation_run_steps FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('automation_runs','run_id','id');
CREATE TRIGGER jobs_ownership BEFORE INSERT OR UPDATE ON jobs FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('resources','resource_id','id');
CREATE TRIGGER job_logs_ownership BEFORE INSERT OR UPDATE ON job_logs FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('jobs','job_id','id');
CREATE TRIGGER domains_ownership BEFORE INSERT OR UPDATE ON domains FOR EACH ROW EXECUTE FUNCTION ignition_inherit_team('resources','resource_id','id');
-- Composite references protect secondary IDs as well as root resource links.
DO $$ DECLARE f record; BEGIN
 FOR f IN SELECT c.conrelid::regclass child,c.confrelid::regclass parent,a.attname col,b.attname parent_col
 FROM pg_constraint c JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=c.conkey[1]
 JOIN pg_attribute b ON b.attrelid=c.confrelid AND b.attnum=c.confkey[1]
 WHERE c.contype='f' AND array_length(c.conkey,1)=1
 AND EXISTS(SELECT 1 FROM pg_attribute x WHERE x.attrelid=c.conrelid AND x.attname='team_id')
 AND EXISTS(SELECT 1 FROM pg_attribute x WHERE x.attrelid=c.confrelid AND x.attname='team_id')
 AND a.attname<>'team_id'
 LOOP
 EXECUTE format('CREATE UNIQUE INDEX IF NOT EXISTS %I ON %s(team_id,%I)','team_ref_'||f.parent||'_'||f.parent_col,f.parent,f.parent_col);
 EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I FOREIGN KEY(team_id,%I) REFERENCES %s(team_id,%I)',f.child,'team_fk_'||f.col,f.col,f.parent,f.parent_col);
 END LOOP;
END $$;
DROP INDEX sources_name_key;
CREATE UNIQUE INDEX sources_team_name ON sources(team_id,lower(name));
-- Shared Docker network names remain globally unique until runtime names are derived from their IDs.
ALTER TABLE audit_events ALTER COLUMN team_id DROP NOT NULL;
ALTER TABLE audit_events ALTER COLUMN team_id SET DEFAULT ignition_team();
-- Use a restricted role even when the migration/worker connection is a superuser.
DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='ignition_request') THEN CREATE ROLE ignition_request NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
GRANT ignition_request TO CURRENT_USER;
GRANT USAGE ON SCHEMA public TO ignition_request;
GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO ignition_request;
GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO ignition_request;
ALTER TABLE projects ENABLE ROW LEVEL SECURITY;
CREATE POLICY projects_scope ON projects TO ignition_request USING (team_id=ignition_team() AND (ignition_access(NULL) OR ignition_access(id))) WITH CHECK (team_id=ignition_team() AND (ignition_access(NULL) OR ignition_access(id)));
ALTER TABLE resources ENABLE ROW LEVEL SECURITY;
CREATE POLICY resources_scope ON resources TO ignition_request USING (team_id=ignition_team() AND (ignition_access(project_id))) WITH CHECK (team_id=ignition_team() AND (ignition_access(project_id)));
ALTER TABLE environments ENABLE ROW LEVEL SECURITY;
CREATE POLICY environments_scope ON environments TO ignition_request USING (team_id=ignition_team() AND (ignition_access(NULL) OR EXISTS(SELECT 1 FROM project_environments pe WHERE pe.environment_id=environments.id) OR EXISTS(SELECT 1 FROM resources r WHERE r.environment_id=environments.id))) WITH CHECK (team_id=ignition_team() AND (ignition_access(NULL) OR EXISTS(SELECT 1 FROM project_environments pe WHERE pe.environment_id=environments.id) OR EXISTS(SELECT 1 FROM resources r WHERE r.environment_id=environments.id)));
ALTER TABLE project_environments ENABLE ROW LEVEL SECURITY;
CREATE POLICY project_environments_scope ON project_environments TO ignition_request USING (team_id=ignition_team() AND (ignition_access(project_id))) WITH CHECK (team_id=ignition_team() AND (ignition_access(project_id)));
ALTER TABLE project_assignments ENABLE ROW LEVEL SECURITY;
CREATE POLICY project_assignments_scope ON project_assignments TO ignition_request USING (team_id=ignition_team() AND (ignition_access(project_id))) WITH CHECK (team_id=ignition_team() AND (ignition_access(project_id)));
ALTER TABLE sources ENABLE ROW LEVEL SECURITY;
CREATE POLICY sources_scope ON sources TO ignition_request USING (team_id=ignition_team() AND (ignition_access(NULL))) WITH CHECK (team_id=ignition_team() AND (ignition_access(NULL)));
ALTER TABLE shared_networks ENABLE ROW LEVEL SECURITY;
CREATE POLICY shared_networks_scope ON shared_networks TO ignition_request USING (team_id=ignition_team() AND (ignition_access(NULL))) WITH CHECK (team_id=ignition_team() AND (ignition_access(NULL)));
ALTER TABLE dns_providers ENABLE ROW LEVEL SECURITY;
CREATE POLICY dns_providers_scope ON dns_providers TO ignition_request USING (team_id=ignition_team() AND (ignition_access(NULL))) WITH CHECK (team_id=ignition_team() AND (ignition_access(NULL)));
ALTER TABLE automations ENABLE ROW LEVEL SECURITY;
CREATE POLICY automations_scope ON automations TO ignition_request USING (team_id=ignition_team() AND ((resource_id IS NOT NULL AND EXISTS(SELECT 1 FROM resources r WHERE r.id=automations.resource_id)) OR (resource_id IS NULL AND ignition_access(NULL)))) WITH CHECK (team_id=ignition_team() AND ((resource_id IS NOT NULL AND EXISTS(SELECT 1 FROM resources r WHERE r.id=automations.resource_id)) OR (resource_id IS NULL AND ignition_access(NULL))));
ALTER TABLE project_favorites ENABLE ROW LEVEL SECURITY;
CREATE POLICY project_favorites_scope ON project_favorites TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM projects parent WHERE parent.id=project_favorites.project_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM projects parent WHERE parent.id=project_favorites.project_id)));
ALTER TABLE env_vars ENABLE ROW LEVEL SECURITY;
CREATE POLICY env_vars_scope ON env_vars TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=env_vars.resource_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=env_vars.resource_id)));
ALTER TABLE deployments ENABLE ROW LEVEL SECURITY;
CREATE POLICY deployments_scope ON deployments TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=deployments.resource_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=deployments.resource_id)));
ALTER TABLE resource_ports ENABLE ROW LEVEL SECURITY;
CREATE POLICY resource_ports_scope ON resource_ports TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=resource_ports.resource_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=resource_ports.resource_id)));
ALTER TABLE resource_volumes ENABLE ROW LEVEL SECURITY;
CREATE POLICY resource_volumes_scope ON resource_volumes TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=resource_volumes.resource_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=resource_volumes.resource_id)));
ALTER TABLE resource_networks ENABLE ROW LEVEL SECURITY;
CREATE POLICY resource_networks_scope ON resource_networks TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=resource_networks.resource_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=resource_networks.resource_id)));
ALTER TABLE network_tests ENABLE ROW LEVEL SECURITY;
CREATE POLICY network_tests_scope ON network_tests TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=network_tests.resource_id) OR (network_tests.resource_id IS NULL AND ignition_access(NULL)))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=network_tests.resource_id) OR (network_tests.resource_id IS NULL AND ignition_access(NULL))));
ALTER TABLE metric_samples ENABLE ROW LEVEL SECURITY;
CREATE POLICY metric_samples_scope ON metric_samples TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=metric_samples.resource_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=metric_samples.resource_id)));
ALTER TABLE runtime_logs ENABLE ROW LEVEL SECURITY;
CREATE POLICY runtime_logs_scope ON runtime_logs TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM deployments parent WHERE parent.id=runtime_logs.deployment_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM deployments parent WHERE parent.id=runtime_logs.deployment_id)));
ALTER TABLE resource_health ENABLE ROW LEVEL SECURITY;
CREATE POLICY resource_health_scope ON resource_health TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=resource_health.resource_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=resource_health.resource_id)));
ALTER TABLE health_results ENABLE ROW LEVEL SECURITY;
CREATE POLICY health_results_scope ON health_results TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=health_results.resource_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=health_results.resource_id)));
ALTER TABLE deployment_env ENABLE ROW LEVEL SECURITY;
CREATE POLICY deployment_env_scope ON deployment_env TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM deployments parent WHERE parent.id=deployment_env.deployment_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM deployments parent WHERE parent.id=deployment_env.deployment_id)));
ALTER TABLE scheduled_tasks ENABLE ROW LEVEL SECURITY;
CREATE POLICY scheduled_tasks_scope ON scheduled_tasks TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=scheduled_tasks.resource_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=scheduled_tasks.resource_id)));
ALTER TABLE resource_tokens ENABLE ROW LEVEL SECURITY;
CREATE POLICY resource_tokens_scope ON resource_tokens TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=resource_tokens.resource_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=resource_tokens.resource_id)));
ALTER TABLE task_runs ENABLE ROW LEVEL SECURITY;
CREATE POLICY task_runs_scope ON task_runs TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM scheduled_tasks parent WHERE parent.id=task_runs.task_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM scheduled_tasks parent WHERE parent.id=task_runs.task_id)));
ALTER TABLE data_imports ENABLE ROW LEVEL SECURITY;
CREATE POLICY data_imports_scope ON data_imports TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=data_imports.resource_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=data_imports.resource_id)));
ALTER TABLE game_servers ENABLE ROW LEVEL SECURITY;
CREATE POLICY game_servers_scope ON game_servers TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=game_servers.resource_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=game_servers.resource_id)));
ALTER TABLE automation_state ENABLE ROW LEVEL SECURITY;
CREATE POLICY automation_state_scope ON automation_state TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM automations parent WHERE parent.id=automation_state.automation_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM automations parent WHERE parent.id=automation_state.automation_id)));
ALTER TABLE automation_runs ENABLE ROW LEVEL SECURITY;
CREATE POLICY automation_runs_scope ON automation_runs TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM automations parent WHERE parent.id=automation_runs.automation_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM automations parent WHERE parent.id=automation_runs.automation_id)));
ALTER TABLE automation_run_steps ENABLE ROW LEVEL SECURITY;
CREATE POLICY automation_run_steps_scope ON automation_run_steps TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM automation_runs parent WHERE parent.id=automation_run_steps.run_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM automation_runs parent WHERE parent.id=automation_run_steps.run_id)));
ALTER TABLE jobs ENABLE ROW LEVEL SECURITY;
CREATE POLICY jobs_scope ON jobs TO ignition_request USING (team_id=ignition_team() AND ((resource_id IS NOT NULL AND EXISTS(SELECT 1 FROM resources r WHERE r.id=jobs.resource_id)) OR (resource_id IS NULL AND ignition_access(NULL)))) WITH CHECK (team_id=ignition_team() AND ((resource_id IS NOT NULL AND EXISTS(SELECT 1 FROM resources r WHERE r.id=jobs.resource_id)) OR (resource_id IS NULL AND ignition_access(NULL))));
ALTER TABLE job_logs ENABLE ROW LEVEL SECURITY;
CREATE POLICY job_logs_scope ON job_logs TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM jobs parent WHERE parent.id=job_logs.job_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM jobs parent WHERE parent.id=job_logs.job_id)));
ALTER TABLE domains ENABLE ROW LEVEL SECURITY;
CREATE POLICY domains_scope ON domains TO ignition_request USING (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=domains.resource_id))) WITH CHECK (team_id=ignition_team() AND (EXISTS(SELECT 1 FROM resources parent WHERE parent.id=domains.resource_id)));
ALTER TABLE audit_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY audit_events_scope ON audit_events TO ignition_request USING (team_id=ignition_team() AND (ignition_access(NULL))) WITH CHECK (team_id=ignition_team() AND (ignition_access(NULL)));
ALTER TABLE servers ENABLE ROW LEVEL SECURITY;
CREATE POLICY servers_scope ON servers TO ignition_request USING (true AND (EXISTS(SELECT 1 FROM team_server_assignments a WHERE a.server_id=servers.id AND a.team_id=ignition_team()))) WITH CHECK (true AND (EXISTS(SELECT 1 FROM team_server_assignments a WHERE a.server_id=servers.id AND a.team_id=ignition_team())));

ALTER TABLE teams ADD COLUMN access_version bigint NOT NULL DEFAULT 1;
CREATE FUNCTION ignition_membership_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN UPDATE teams SET access_version=access_version+1 WHERE id=OLD.team_id;RETURN OLD;
 ELSE UPDATE teams SET access_version=access_version+1 WHERE id=NEW.team_id;RETURN NEW; END IF;
END $$;
CREATE TRIGGER team_membership_version AFTER INSERT OR UPDATE OR DELETE ON team_memberships FOR EACH ROW EXECUTE FUNCTION ignition_membership_changed();
CREATE TRIGGER project_assignment_version AFTER INSERT OR UPDATE OR DELETE ON project_assignments FOR EACH ROW EXECUTE FUNCTION ignition_membership_changed();
CREATE TRIGGER resource_group_version AFTER UPDATE OF project_id ON resources FOR EACH ROW WHEN (OLD.project_id IS DISTINCT FROM NEW.project_id) EXECUTE FUNCTION ignition_membership_changed();

CREATE FUNCTION ignition_link_environment() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF NEW.project_id IS NOT NULL THEN INSERT INTO project_environments(team_id,project_id,environment_id) VALUES(NEW.team_id,NEW.project_id,NEW.id) ON CONFLICT DO NOTHING; END IF; RETURN NEW; END $$;
CREATE TRIGGER environment_group_link AFTER INSERT ON environments FOR EACH ROW EXECUTE FUNCTION ignition_link_environment();
ALTER POLICY audit_events_scope ON audit_events USING (team_id=ignition_team() AND (ignition_access(NULL) OR (target_type='resource' AND EXISTS(SELECT 1 FROM resources r WHERE r.id::text=audit_events.target_id)) OR (target_type='project' AND EXISTS(SELECT 1 FROM projects p WHERE p.id::text=audit_events.target_id))));

-- A hostname is an exclusive physical routing claim. Different teams cannot replace
-- one another's ownership challenge or add competing paths to an existing host.
CREATE TABLE team_hostnames(hostname text PRIMARY KEY,team_id uuid NOT NULL REFERENCES teams(id));
INSERT INTO team_hostnames SELECT DISTINCT hostname,team_id FROM domains;
CREATE FUNCTION ignition_hostname_claim() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=public,pg_temp AS $$
DECLARE owner_team uuid;
BEGIN
 IF TG_OP<>'DELETE' THEN
 INSERT INTO team_hostnames(hostname,team_id) VALUES(NEW.hostname,NEW.team_id) ON CONFLICT DO NOTHING;
 SELECT team_id INTO owner_team FROM team_hostnames WHERE hostname=NEW.hostname FOR UPDATE;
 IF owner_team<>NEW.team_id THEN RAISE EXCEPTION 'Hostname is already in use' USING ERRCODE='23505'; END IF;
 END IF;
 IF TG_OP='DELETE' THEN DELETE FROM team_hostnames WHERE hostname=OLD.hostname AND NOT EXISTS(SELECT 1 FROM domains WHERE hostname=OLD.hostname);RETURN OLD;END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER domains_z_hostname_claim BEFORE INSERT OR UPDATE OF hostname ON domains FOR EACH ROW EXECUTE FUNCTION ignition_hostname_claim();
CREATE TRIGGER domains_release_hostname AFTER DELETE ON domains FOR EACH ROW EXECUTE FUNCTION ignition_hostname_claim();

ALTER POLICY environments_scope ON environments WITH CHECK (team_id=ignition_team() AND (ignition_access(NULL) OR EXISTS(SELECT 1 FROM project_environments pe WHERE pe.environment_id=environments.id)));

ALTER POLICY sources_scope ON sources USING (team_id=ignition_team() AND (ignition_access(NULL) OR EXISTS(SELECT 1 FROM resources r WHERE r.source_id=sources.id)));
ALTER POLICY shared_networks_scope ON shared_networks USING (team_id=ignition_team() AND (ignition_access(NULL) OR EXISTS(SELECT 1 FROM resource_networks m WHERE m.network_id=shared_networks.id)));
ALTER POLICY dns_providers_scope ON dns_providers USING (team_id=ignition_team() AND (ignition_access(NULL) OR EXISTS(SELECT 1 FROM domains d WHERE d.dns_provider_id=dns_providers.id)));

-- Legacy issuer-less tokens are resource capabilities, never general team identities.
ALTER POLICY resources_scope ON resources USING (team_id=ignition_team() AND (ignition_access(project_id) OR id=nullif(current_setting('ignition.resource_id',true),'')::uuid)) WITH CHECK (team_id=ignition_team() AND (ignition_access(project_id) OR id=nullif(current_setting('ignition.resource_id',true),'')::uuid));

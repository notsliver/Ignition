-- Platform administrators can work in any team as its owner without being a member. The API sets
-- ignition.instance_admin only for a signed-in platform administrator and audits it; row security
-- then treats them as an owner of that one team.
CREATE OR REPLACE FUNCTION ignition_access(project uuid) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=public,pg_temp AS $$
 SELECT CASE WHEN current_setting('ignition.instance_admin',true)='true' AND ignition_team() IS NOT NULL
   THEN project IS NULL OR EXISTS(SELECT 1 FROM projects p WHERE p.id=project AND p.team_id=ignition_team())
 ELSE coalesce((SELECT
   CASE WHEN coalesce(m.permissions->'deny','[]') ? current_setting('ignition.permission',true) OR coalesce(a.permissions->'deny','[]') ? current_setting('ignition.permission',true) THEN false
   ELSE ignition_rank(m.role)>=coalesce(nullif(current_setting('ignition.min_rank',true),''),'1')::integer
     OR coalesce(m.permissions->'grant','[]') ? current_setting('ignition.permission',true)
     OR (a.project_id IS NOT NULL AND (ignition_rank(a.role)>=coalesce(nullif(current_setting('ignition.min_rank',true),''),'1')::integer OR coalesce(a.permissions->'grant','[]') ? current_setting('ignition.permission',true))) END
 FROM team_memberships m LEFT JOIN project_assignments a ON a.team_id=m.team_id AND a.user_id=m.user_id AND a.project_id=project
 WHERE m.team_id=ignition_team() AND m.user_id=nullif(current_setting('ignition.user_id',true),'')::uuid
 AND (project IS NULL OR EXISTS(SELECT 1 FROM projects p WHERE p.id=project AND p.team_id=m.team_id))),false) END
$$;

-- Permissions became area.action keys (projects.create, resources.delete, ...). Overrides saved
-- with the earlier, broader keys are expanded to every key that replaced them.
CREATE TEMP TABLE permission_rename (old text, new text);
INSERT INTO permission_rename VALUES
    ('deploy', 'deployments.create'), ('deploy', 'deployments.cancel'), ('deploy', 'resources.control'), ('deploy', 'tasks.run'), ('deploy', 'automations.run'),
    ('configure', 'resources.create'), ('configure', 'resources.update'), ('configure', 'variables.update'), ('configure', 'domains.manage'),
    ('configure', 'storage.update'), ('configure', 'tasks.manage'), ('configure', 'access.manage'), ('configure', 'automations.manage'),
    ('configure', 'databases.import'), ('configure', 'databases.connect'), ('configure', 'sources.use'),
    ('terminal', 'terminal.open'),
    ('servers.operate', 'servers.operate'),
    ('secrets', 'variables.reveal'),
    ('terminal.root', 'terminal.root'), ('terminal.root', 'resources.privileged'),
    ('backups', 'storage.backup'), ('backups', 'storage.delete'),
    ('projects', 'projects.create'), ('projects', 'projects.update'), ('projects', 'projects.delete'), ('projects', 'resources.delete'),
    ('sources', 'sources.manage'),
    ('servers.manage', 'servers.create'), ('servers.manage', 'servers.update'), ('servers.manage', 'servers.delete'),
    ('servers.manage', 'servers.cleanup'), ('servers.manage', 'servers.inspect'), ('servers.manage', 'servers.tasks'),
    ('settings', 'settings.view'), ('settings', 'settings.update'), ('settings', 'security.manage'),
    ('audit', 'audit.view'),
    ('users', 'users.view'), ('users', 'users.invite'), ('users', 'users.update'), ('users', 'users.permissions'), ('users', 'users.delete');

UPDATE users SET permissions = jsonb_build_object(
    'grant', COALESCE((SELECT jsonb_agg(DISTINCT m.new ORDER BY m.new) FROM jsonb_array_elements_text(permissions -> 'grant') g JOIN permission_rename m ON m.old = g.value), '[]'::jsonb),
    'deny', COALESCE((SELECT jsonb_agg(DISTINCT m.new ORDER BY m.new) FROM jsonb_array_elements_text(permissions -> 'deny') d JOIN permission_rename m ON m.old = d.value), '[]'::jsonb))
WHERE jsonb_array_length(COALESCE(permissions -> 'grant', '[]')) + jsonb_array_length(COALESCE(permissions -> 'deny', '[]')) > 0;
DROP TABLE permission_rename;

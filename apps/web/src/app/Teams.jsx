import { createContext, useContext, useEffect, useRef, useState } from 'react';
import { ArrowRightIcon, ArrowRightStartOnRectangleIcon, CheckIcon, InformationCircleIcon, PlusIcon, ShieldExclamationIcon, UserGroupIcon } from '@heroicons/react/16/solid';
import { Avatar, Button, Dialog, FormField, Logo, Notice, Select, TextInput } from '../ui';
import { api, onTeamAccessChanged, setActiveTeamId } from '../api/client';
import { confirmLeave } from './unsaved';
import { SessionContext, useSession } from './AuthGate';
import { AuthShell } from './AuthShell';
import './Teams.css';

/*
 * Teams are the ownership boundary: every resource belongs to one, and what you can do comes from
 * your role in it (and in its projects). The active team lives in the URL, so each browser tab has
 * its own. Switching aborts the old team's requests and remounts the app, so nothing from one team
 * can show up in another.
 */

const TeamContext = createContext(null);
export const useTeam = () => useContext(TeamContext);

export const TEAM_ROLES = [
  { value: 'owner', label: 'Owner', description: 'Everything, including ownership and deleting the team\'s work' },
  { value: 'admin', label: 'Admin', description: 'Members, projects, integrations and every resource' },
  { value: 'developer', label: 'Developer', description: 'Deploy and configure every resource in the team' },
  { value: 'viewer', label: 'Viewer', description: 'See every resource, change nothing' },
  { value: 'member', label: 'Member', description: 'Only the projects they\'re assigned to' },
];
export const roleLabel = Object.fromEntries(TEAM_ROLES.map(r => [r.value, r.label]));
const platformAdmin = role => role === 'owner' || role === 'admin';

/** A team's mark: its initials on a colour picked from its ID, so each team is recognisable. */
export function TeamMark({ team, size = 22 }) {
  const [failedSrc, setFailedSrc] = useState(null);
  const initials = (team?.name ?? '?').split(/\s+/).filter(Boolean).slice(0, 2).map(w => w[0]).join('').toUpperCase() || '?';
  const hue = [...(team?.id ?? '')].reduce((n, c) => (n * 31 + c.charCodeAt(0)) % 360, 7);
  return <span className="team-mark" aria-hidden="true" style={{ width: size, height: size, fontSize: size * 0.42, '--team-hue': hue }}>{team?.iconUrl && failedSrc !== team.iconUrl ? <img src={team.iconUrl} alt="" onError={() => setFailedSrc(team.iconUrl)} /> : initials}</span>;
}

export function TeamGate({ children }) {
  const session = useSession();
  const { user } = session;
  const switchAttempt = useRef(0);
  const [teams, setTeams] = useState(null);
  const [active, setActive] = useState(null);
  const [error, setError] = useState('');
  const [creating, setCreating] = useState(false);
  const [invitation, setInvitation] = useState(() => location.pathname.match(/^\/join-team\/([A-Za-z0-9_-]+)$/)?.[1] ?? sessionStorage.getItem('ignition:team-invitation'));
  const activate = team => {
    setActiveTeamId(team?.id ?? '', team?.accessVersion, team?.slug);
    setActive(team);
    if (team) sessionStorage.setItem('ignition:last-team', team.id);
  };

  async function refresh(preferred) {
    const originalPath = location.pathname;
    try {
      const out = await api.teams();
      setTeams(out.teams);
      let requested = preferred ?? location.pathname.match(/^\/teams\/([^/]+)/)?.[1];
      // Old links (/projects/…) still work: find the team that owns them, then use the team URL.
      const legacyResource = originalPath.match(/^\/projects\/[0-9a-f-]{36}\/resources\/([0-9a-f-]{36})(.*)$/);
      const legacyProject = originalPath.match(/^\/projects\/([0-9a-f-]{36})(.*)$/);
      if (!requested && (legacyResource || legacyProject)) {
        const results = await Promise.allSettled(out.teams.map(t => legacyResource ? api.teamResource(t.id, legacyResource[1]) : api.teamProject(t.id, legacyProject[1])));
        const found = results.findIndex(r => r.status === 'fulfilled');
        if (found >= 0 && location.pathname === originalPath) {
          requested = out.teams[found].id;
          history.replaceState(null, '', legacyResource ? `/teams/${requested}/resources/${legacyResource[1]}${legacyResource[2]}${location.search}` : `/teams/${requested}/projects/${legacyProject[1]}${legacyProject[2]}${location.search}`);
        }
      }
      if (!requested) {
        const last = sessionStorage.getItem('ignition:last-team');
        if (out.teams.some(t => t.id === last)) requested = last;
      }
      // URLs carry the short team ID; the UUID still works (older links, API results).
      let team = requested ? out.teams.find(t => t.id === requested || t.slug === requested) : out.teams.length === 1 ? out.teams[0] : null;
      // Platform administrators can open any team as its owner (Instance administration › Teams); it's audited.
      if (!team && requested && platformAdmin(user.role)) {
        const all = await api.platformTeams().catch(() => null);
        const other = all?.teams.find(t => t.id === requested || t.slug === requested);
        if (other) team = (await api.team(other.id)).team;
      }
      if (team && requested === team.id && team.slug) history.replaceState(null, '', location.pathname.replace(`/teams/${team.id}`, `/teams/${team.slug}`) + location.search);
      activate(team ?? null);
      setError(requested && !team ? 'You don\'t have access to that team (any more). Choose one of yours.' : '');
    } catch (e) { if (e.name !== 'AbortError') setError(e.message); }
  }
  useEffect(() => { refresh(); return () => setActiveTeamId(''); }, []); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    const pop = () => { const id = location.pathname.match(/^\/teams\/([^/]+)/)?.[1]; if (id && id !== active?.id && id !== active?.slug) activate(null); refresh(); };
    addEventListener('popstate', pop);
    return () => removeEventListener('popstate', pop);
  }, [active?.id]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => onTeamAccessChanged(() => { activate(null); refresh(); }), []); // eslint-disable-line react-hooks/exhaustive-deps

  const switchTeam = async team => {
    if (team.id === active?.id || !confirmLeave()) return;
    const attempt = ++switchAttempt.current;
    try {
      const out = await api.team(team.id);
      if (attempt !== switchAttempt.current) return; // a newer switch won
      history.pushState(null, '', `/teams/${team.slug || team.id}/dashboard`);
      setCreating(false); setError('');
      activate(out.team);
    } catch (e) { if (e.name !== 'AbortError') setError(e.message); }
  };
  const created = async team => { setCreating(false); history.pushState(null, '', `/teams/${team.slug || team.id}/dashboard`); await refresh(team.id); };
  const canCreate = platformAdmin(user.role);
  const createDialog = creating && <CreateTeamDialog onClose={() => setCreating(false)} onCreated={created} />;

  if (invitation) return <JoinTeam token={invitation} email={user.email} onDone={async teamId => {
    sessionStorage.removeItem('ignition:team-invitation'); setInvitation(null);
    history.replaceState(null, '', teamId ? `/teams/${teamId}/dashboard` : '/');
    await refresh(teamId || undefined);
  }} />;

  if (!active) return <><AuthShell foot={false}>
      <div className="auth-card team-picker">
        {!teams && !error && <p className="auth-loading">Loading your teams…</p>}
        {teams?.length === 0 && <div className="auth-form">
          <header><h1>You're not in a team yet</h1><p>Everything in Ignition belongs to a team: projects, resources, servers you can deploy to.</p></header>
          {canCreate ? <Button variant="primary" icon={PlusIcon} onClick={() => setCreating(true)}>Create a team</Button>
            : <Notice tone="info" title="Ask to be invited">A team administrator can invite {user.email} from their team's Members page. Open the link they send you.</Notice>}
        </div>}
        {teams?.length > 0 && <div className="auth-form">
          <header><h1>Choose a team</h1><p>Signed in as {user.email}. You can switch any time from the menu at the top left.</p></header>
          {error && <Notice tone="warning" title={error} />}
          <div className="team-choices">
            {teams.map(t => <button key={t.id} type="button" className="team-choice" onClick={() => switchTeam(t)}>
              <TeamMark team={t} size={32} />
              <span className="team-choice-main"><strong>{t.name}</strong><small>{roleLabel[t.role] ?? t.role}</small></span>
              <ArrowRightIcon />
            </button>)}
          </div>
          <div className="team-picker-foot">
            {canCreate && <button type="button" className="inline-link team-create-link" onClick={() => setCreating(true)}><PlusIcon />Create another team</button>}
            <span className="ob-spacer" />
            <button type="button" className="team-signout" onClick={session.signOut}><ArrowRightStartOnRectangleIcon />Sign out</button>
          </div>
        </div>}
        {error && !teams && <div className="auth-form"><Notice tone="danger" title="Couldn't load your teams">{error}</Notice><Button onClick={() => refresh()}>Try again</Button></div>}
      </div>
  </AuthShell>{createDialog}</>;

  return <TeamContext.Provider value={{ team: active, teams, switchTeam, refresh, canCreateTeam: canCreate, createTeam: () => setCreating(true) }}>
    <SessionContext.Provider value={{ ...session, platformUser: user, user: { ...user, instanceRole: user.role, role: active.role, permissions: mergedPermissions(active.permissions, user.permissions) } }}>
      <div key={`${active.id}:${active.accessVersion}`} className="team-scope">{children}</div>
      {active.adminAccess && <div className="team-admin-pill" role="status">
        <ShieldExclamationIcon aria-hidden="true" />
        <span>You're in <strong>{active.name}</strong> as an instance administrator. Everything you do here is in the audit log.</span>
        <button type="button" onClick={() => { const home = teams?.[0]; if (home) switchTeam(home); else { history.pushState(null, '', '/'); activate(null); } }}>Leave</button>
      </div>}
    </SessionContext.Provider>
    {createDialog}
  </TeamContext.Provider>;
}

function CreateTeamDialog({ onClose, onCreated }) {
  const [name, setName] = useState('');
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  async function submit(e) {
    e?.preventDefault();
    if (!name.trim()) return setError('Name the team.');
    setPending(true); setError('');
    try { const { team } = await api.createTeam(name.trim()); await onCreated(team); } catch (err) { setError(err.fields?.name ?? err.message); setPending(false); }
  }
  return <Dialog open onClose={onClose} icon={UserGroupIcon} title="Create a team" description="A separate space with its own members, projects and resources. You'll be its owner."
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" type="submit" form="create-team" disabled={pending}>{pending ? 'Creating…' : 'Create team'}</Button></>}>
    <form id="create-team" className="team-form" onSubmit={submit}>
      <FormField label="Team name" htmlFor="team-name" error={error}><TextInput id="team-name" value={name} maxLength={80} autoFocus placeholder="Acme Platform" onChange={e => { setName(e.target.value); setError(''); }} /></FormField>
      <p className="team-hint"><InformationCircleIcon />It can deploy to every server on this instance straight away.</p>
    </form>
  </Dialog>;
}

function JoinTeam({ token, email, onDone }) {
  const [invite, setInvite] = useState(null);
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  useEffect(() => { api.teamInvitation(token).then(setInvite).catch(e => setError(e.status === 404 ? 'This invitation has expired, was revoked, or was already used.' : e.message)); }, [token]);
  const wrongEmail = invite && invite.email.toLowerCase() !== email.toLowerCase();
  async function accept() {
    setPending(true); setError('');
    try { const out = await api.acceptTeamInvitation(token); await onDone(out.teamId); } catch (e) { setError(e.status === 404 ? 'This invitation can\'t be used by this account.' : e.message); setPending(false); }
  }
  return <AuthShell foot={false}>
      <div className="auth-card"><div className="auth-form">
        {!invite && !error && <p className="auth-loading">Checking your invitation…</p>}
        {invite && <>
          <div className="team-invite-head"><TeamMark team={{ id: invite.teamId, name: invite.teamName }} size={44} /></div>
          <header><h1>Join {invite.teamName}</h1><p>You've been invited as {invite.role === 'admin' ? 'an' : 'a'} <strong>{roleLabel[invite.role] ?? invite.role}</strong>: {TEAM_ROLES.find(r => r.value === invite.role)?.description.toLowerCase()}.</p></header>
          {wrongEmail && <Notice tone="warning" title={`This invitation is for ${invite.email}`}>You're signed in as {email}. Sign out and sign in (or register) as {invite.email} to accept it.</Notice>}
        </>}
        {error && <Notice tone="danger" title={error} />}
        <div className="team-actions">
          {invite && !wrongEmail && <Button variant="primary" disabled={pending} onClick={accept}>{pending ? 'Joining…' : `Join ${invite.teamName}`}</Button>}
          <Button onClick={() => onDone(null)}>{invite && !wrongEmail ? 'Not now' : 'Continue to Ignition'}</Button>
        </div>
      </div></div>
  </AuthShell>;
}

/* ------------------------------ Project access ------------------------------ */

const PROJECT_ROLES = [
  { value: '', label: 'No project role' },
  { value: 'admin', label: 'Project admin' },
  { value: 'developer', label: 'Project developer' },
  { value: 'viewer', label: 'Project viewer' },
];

export function ProjectAccessDialog({ project, onClose }) {
  const [data, setData] = useState(null);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState('');
  const [saved, setSaved] = useState('');
  const load = () => api.projectAssignments(project.id).then(setData).catch(e => setError(e.message));
  useEffect(() => { load(); }, [project.id]); // eslint-disable-line react-hooks/exhaustive-deps
  async function change(m, role) {
    setSaving(m.userId); setError(''); setSaved('');
    try { await api.setProjectAssignment(project.id, { userId: m.userId, role }); await load(); setSaved(m.userId); } catch (e) { setError(e.message); }
    setSaving('');
  }
  const assigned = id => data?.assignments.find(a => a.userId === id)?.role ?? '';
  return <Dialog open onClose={onClose} icon={UserGroupIcon} title={`Who can work on ${project.name}`} className="team-dialog"
    description="A project role adds access to this project on top of someone's team role. Members with the Member team role only see projects they're assigned to."
    footer={<Button variant="primary" onClick={onClose}>Done</Button>}>
    {error && <Notice tone="danger" title={error} />}
    {!data && !error && <p className="team-muted">Loading…</p>}
    {data && <div className="team-rows">
      {data.members.map(m => {
        const covers = ['owner', 'admin', 'developer'].includes(m.role) || (m.role === 'viewer' && assigned(m.userId) === '');
        return <div key={m.userId} className="team-row">
          <Avatar src={m.avatarUrl} name={m.name || m.email} size={28} />
          <span className="team-row-main"><strong>{m.name || m.email}</strong><small>{m.name ? `${m.email} · ` : ''}Team {roleLabel[m.role]?.toLowerCase() ?? m.role}{covers && m.role !== 'viewer' ? ': already has access' : ''}</small></span>
          {saved === m.userId && <CheckIcon className="team-saved" aria-label="Saved" />}
          <Select label={`Project role for ${m.name || m.email}`} value={assigned(m.userId)} options={PROJECT_ROLES} onChange={role => role !== assigned(m.userId) && change(m, role)} />
        </div>;
      })}
      {!data.members.length && <p className="team-muted">No one else is in this team yet. Invite people under Settings › Team Members.</p>}
    </div>}
    {saving && <p className="team-muted">Saving…</p>}
  </Dialog>;
}

export function ProjectGroupingDialog({ resource, onClose, onChanged }) {
  const [projects, setProjects] = useState([]);
  const [projectId, setProjectId] = useState(resource.projectId ?? '');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => { api.projects().then(out => setProjects(out.projects)).catch(e => setError(e.message)); }, []);
  const changed = projectId !== (resource.projectId ?? '');
  async function save() {
    setPending(true); setError('');
    try { await api.groupResource(resource.id, projectId); onChanged(); } catch (e) { setError(e.message); setPending(false); }
  }
  return <Dialog open onClose={onClose} title="Move to a project" className="team-dialog"
    description={`${resource.name} keeps running exactly as it is: same environment, network and container. Only who can see and change it follows the project.`}
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" disabled={pending || !changed} onClick={save}>{pending ? 'Moving…' : 'Move'}</Button></>}>
    {error && <Notice tone="danger" title={error} />}
    <FormField label="Project" htmlFor="group-project" hint={projectId ? 'People assigned to this project gain access to it.' : 'Without a project, only people with a team-wide role can see it.'}>
      <Select id="group-project" label="Project" value={projectId} onChange={setProjectId} options={[{ value: '', label: 'No project' }, ...projects.map(p => ({ value: p.id, label: p.name }))]} />
    </FormField>
  </Dialog>;
}

/* -------------------------------- Permissions ------------------------------- */

const platformPermission = key => /^(users\.|settings\.)/.test(key) || ['security.manage', 'owners.manage'].includes(key);
const projectPermission = key => /^(projects|resources|deployments|variables|logs|terminal|domains|backups|files|storage|databases|tasks|access|games|automations|audit)\./.test(key) && key !== 'projects.create';
// Team permissions decide workloads; platform permissions (users, settings, servers) come from the account.
function mergedPermissions(teamPermissions = [], platformPermissions = []) {
  return [...new Set([...teamPermissions.filter(key => !platformPermission(key) && (!key.startsWith('servers.') || key === 'servers.view' || platformPermissions.includes(key)) && (key !== 'resources.privileged' || platformPermissions.includes(key))),
    ...platformPermissions.filter(platformPermission)])];
}
/** Inside a project or resource, project-scoped permissions come from your role there. */
export function ResourceAccess({ resource, children }) {
  const session = useSession();
  const permissions = [...new Set([...session.user.permissions.filter(key => !projectPermission(key)), ...(resource.permissions ?? session.user.permissions).filter(projectPermission)])];
  return <SessionContext.Provider value={{ ...session, user: { ...session.user, role: resource.effectiveRole ?? session.user.role, permissions } }}>{children}</SessionContext.Provider>;
}


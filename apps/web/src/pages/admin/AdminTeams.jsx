import { useMemo, useState } from 'react';
import { ArrowRightIcon, PlusIcon, ShieldExclamationIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, Notice, SearchField, Select } from '../../ui';
import { api } from '../../api/client';
import { timeAgo, useQuery } from '../../api/hooks';
import { TeamMark, roleLabel, useTeam } from '../../app/Teams';
import '../settings/settings.css';
import './AdminTeams.css';

/*
 * Instance administration › Teams: every team on this installation, including ones you aren't in.
 * It shows sizes only. To work in a team you aren't in, enter it as its owner: that's recorded in
 * the audit log, and so is everything you change there.
 */

const plural = (n, one, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

export function AdminTeams() {
  const { teams: mine, switchTeam, createTeam } = useTeam();
  const q = useQuery(() => api.platformTeams(), [], { pollMs: 30000 });
  const [query, setQuery] = useState('');
  const [entering, setEntering] = useState(null);
  const list = q.data?.teams ?? [];
  const shown = useMemo(() => {
    const s = query.trim().toLowerCase();
    return list.filter(t => !s || t.name.toLowerCase().includes(s) || t.slug.includes(s));
  }, [list, query]);
  const totals = list.reduce((a, t) => ({ resources: a.resources + t.resources, members: a.members + t.members }), { resources: 0, members: 0 });

  if (q.error && !q.data) return <div className="settings-page"><Notice tone="danger" title="Couldn't load teams">{q.error.message} <button type="button" className="inline-link" onClick={q.refetch}>Retry</button></Notice></div>;
  return <div className="settings-page admin-teams">
    <div className="admin-toolbar">
      <span className="admin-count">{plural(list.length, 'team')} · {plural(totals.resources, 'resource')}</span>
      <SearchField value={query} onChange={setQuery} placeholder="Search name or ID" />
      <Button variant="primary" icon={PlusIcon} onClick={createTeam}>Create team</Button>
    </div>
    <p className="admin-intro">Teams own projects and resources; people only see the teams they're members of. You can see every team here because you administer this instance, and enter any of them as its owner, for support or cleanup. Entering is recorded in the audit log, and so is every change you make there.</p>

    <div className="settings-card admin-list">
      {shown.map(t => {
        const member = mine?.find(m => m.id === t.id);
        return <div key={t.id} className="admin-row">
          <TeamMark team={t} size={32} />
          <span className="admin-main">
            <strong>{t.name}{t.yourRole && <span className="rp-chip">{roleLabel[t.yourRole] ?? t.yourRole}</span>}</strong>
            <span><code>{t.slug}</code> · created {timeAgo(t.createdAt)}</span>
          </span>
          <span className="admin-stats">
            <span><b>{t.members}</b> {t.members === 1 ? 'member' : 'members'}</span>
            <span><b>{t.projects}</b> {t.projects === 1 ? 'project' : 'projects'}</span>
            <span><b>{t.resources}</b> {t.resources === 1 ? 'resource' : 'resources'}</span>
          </span>
          {member ? <Button icon={ArrowRightIcon} onClick={() => switchTeam(member)}>Open</Button>
            : <Button icon={ShieldExclamationIcon} onClick={() => setEntering(t)}>Enter as owner</Button>}
        </div>;
      })}
      {!q.data && <p className="users-empty">Loading…</p>}
      {q.data && !shown.length && <p className="users-empty">No team matches.</p>}
    </div>
    {entering && <Dialog open title={`Enter ${entering.name} as owner?`} onClose={() => setEntering(null)}
      footer={<><Button onClick={() => setEntering(null)}>Cancel</Button><Button variant="primary" icon={ArrowRightIcon} onClick={() => { const t = entering; setEntering(null); switchTeam(t); }}>Enter team</Button></>}>
      <p className="admin-enter-text">You aren't a member of this team. As an instance administrator you can work in it with an owner's access: its projects, resources, members and settings.</p>
      <Notice tone="warning" title="This is recorded">Entering {entering.name} is written to the audit log, and every change you make there is marked as made by an instance administrator. Its members can't see you in their member list.</Notice>
    </Dialog>}
    {list.some(t => !t.owners) && <Notice tone="warning" title="A team has no owner">Someone has to own every team. Invite a person and make them its owner.</Notice>}
  </div>;
}

/**
 * Some infrastructure belongs to a team even when it's managed here (Sources: Git and registry
 * credentials). This says whose you're looking at, and switches between the teams you're in.
 */
export function TeamScopeNote({ what }) {
  const { team, teams, switchTeam } = useTeam();
  return <div className="admin-scope">
    <TeamMark team={team} size={20} />
    <span>{what} belong to a team. These are <strong>{team.name}</strong>'s; each team deploys with its own.</span>
    {teams?.length > 1 && <Select label="Team" value={team.id} onChange={id => { const t = teams.find(x => x.id === id); if (t) switchTeam(t); }}
      options={teams.map(t => ({ value: t.id, label: t.name }))} />}
  </div>;
}

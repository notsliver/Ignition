import { useMemo, useState } from 'react';
import {
  ArrowPathIcon, CheckIcon, EllipsisHorizontalIcon, EnvelopeIcon, KeyIcon, LinkIcon, MinusIcon, NoSymbolIcon, PlayIcon, ShieldCheckIcon,
  TrashIcon, UserPlusIcon, XMarkIcon,
} from '@heroicons/react/16/solid';
import { Avatar, Button, Checkbox, Dialog, FormField, Menu, Notice, SearchField, SegmentedControl, Select, Status, cx } from '../../../ui';
import { projects } from '../../Projects/data';
import '../settings.css';
import './Users.css';

/*
 * Instance-wide accounts and roles. Resource-specific access lives on each resource's Access & Secrets page.
 * Guardrails: there is always at least one Owner, and you can't lock yourself out.
 */

const ME = 'admin@devme.app';
const ROLES = [
  { value: 'owner', label: 'Owner', description: 'Everything, including transferring ownership' },
  { value: 'admin', label: 'Admin', description: 'Manage users, settings and every project' },
  { value: 'developer', label: 'Developer', description: 'Deploy and configure assigned projects' },
  { value: 'viewer', label: 'Viewer', description: 'Read-only access to assigned projects' },
];
const roleLabel = Object.fromEntries(ROLES.map(r => [r.value, r.label]));
const rank = { owner: 3, admin: 2, developer: 1, viewer: 0 };
const PERMISSIONS = [
  ['View projects, logs and metrics', ['owner', 'admin', 'developer', 'viewer']],
  ['Deploy, restart and roll back', ['owner', 'admin', 'developer']],
  ['Edit resource settings and variables', ['owner', 'admin', 'developer']],
  ['Reveal secret values', ['owner', 'admin']],
  ['Open a terminal', ['owner', 'admin', 'developer']],
  ['Create and delete projects', ['owner', 'admin']],
  ['Manage servers and instance settings', ['owner', 'admin']],
  ['Invite and manage users', ['owner', 'admin']],
  ['Transfer ownership', ['owner']],
];
const allProjects = projects.map(p => p.name);
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

const seed = [
  { email: ME, name: 'You', role: 'owner', status: 'active', mfa: true, lastActive: 'Now', projects: 'all' },
  { email: 'maya@acme.dev', name: 'Maya Chen', role: 'admin', status: 'active', mfa: true, lastActive: '12 min ago', projects: 'all' },
  { email: 'jonas@acme.dev', name: 'Jonas Weber', role: 'developer', status: 'active', mfa: false, lastActive: '2h ago', projects: ['Acme Platform', 'Backend'] },
  { email: 'lena@acme.dev', name: 'Lena Fischer', role: 'developer', status: 'active', mfa: true, lastActive: 'Yesterday', projects: ['Commerce'] },
  { email: 'tom@acme.dev', name: 'Tom Okafor', role: 'viewer', status: 'suspended', mfa: false, lastActive: 'Sep 30', projects: ['Commerce'] },
  { email: 'sara@contractor.io', name: null, role: 'developer', status: 'invited', mfa: false, lastActive: null, invited: '2 days ago', expires: 'in 5 days', projects: ['Marketing site'] },
];

const statusTone = { active: ['success', 'Active'], invited: ['warning', 'Invited'], suspended: ['neutral', 'Suspended'] };

export function Users() {
  const [users, setUsers] = useState(seed);
  const [query, setQuery] = useState('');
  const [tab, setTab] = useState('all');
  const [roleFilter, setRoleFilter] = useState('all');
  const [inviting, setInviting] = useState(false);
  const [confirm, setConfirm] = useState(null);
  const [editingAccess, setEditingAccess] = useState(null);
  const [flash, setFlash] = useState('');

  const owners = users.filter(u => u.role === 'owner' && u.status === 'active').length;
  const counts = { all: users.length, active: users.filter(u => u.status === 'active').length, invited: users.filter(u => u.status === 'invited').length, suspended: users.filter(u => u.status === 'suspended').length };
  const noMfa = users.filter(u => u.status === 'active' && !u.mfa);

  const rows = useMemo(() => {
    const q = query.trim().toLowerCase();
    return users.filter(u => (tab === 'all' || u.status === tab) && (roleFilter === 'all' || u.role === roleFilter)
      && (!q || u.email.includes(q) || u.name?.toLowerCase().includes(q)))
      .sort((a, b) => rank[b.role] - rank[a.role] || a.email.localeCompare(b.email));
  }, [users, query, tab, roleFilter]);

  const update = (email, patch) => setUsers(list => list.map(u => u.email === email ? { ...u, ...patch } : u));
  const notify = text => { setFlash(text); setTimeout(() => setFlash(''), 2200); };

  // Why a user's role can't be changed right now, if it can't.
  function roleLock(u) {
    if (u.email === ME) return 'You can\'t change your own role.';
    if (u.role === 'owner' && owners === 1) return 'Every instance needs at least one owner.';
    return null;
  }

  function changeRole(u, role) {
    if (role === u.role) return;
    if (role === 'owner') return setConfirm({ kind: 'owner', user: u });
    update(u.email, { role, projects: rank[role] >= 2 ? 'all' : u.projects === 'all' ? [] : u.projects });
    notify(`${u.name ?? u.email} is now ${roleLabel[role]}.`);
  }

  function actions(u) {
    const self = u.email === ME;
    const lastOwner = u.role === 'owner' && owners === 1;
    if (u.status === 'invited') return [
      { label: 'Resend invitation', icon: EnvelopeIcon, onSelect: () => { update(u.email, { invited: 'Just now', expires: 'in 7 days' }); notify(`Invitation resent to ${u.email}.`); } },
      { label: 'Copy invitation link', icon: LinkIcon, onSelect: () => { navigator.clipboard?.writeText(`https://ignition.example.com/invite/${btoa(u.email).slice(0, 18)}`); notify('Invitation link copied.'); } },
      'separator',
      { label: 'Revoke invitation', icon: XMarkIcon, danger: true, onSelect: () => setConfirm({ kind: 'revoke', user: u }) },
    ];
    return [
      ...(rank[u.role] < 2 ? [{ label: 'Edit project access', icon: ShieldCheckIcon, onSelect: () => setEditingAccess({ email: u.email, projects: u.projects === 'all' ? [...allProjects] : [...u.projects] }) }] : []),
      ...(u.mfa && !self ? [{ label: 'Reset MFA', icon: KeyIcon, onSelect: () => setConfirm({ kind: 'mfa', user: u }) }] : []),
      ...(!self ? [{ label: 'Send password reset', icon: ArrowPathIcon, onSelect: () => notify(`Password reset email sent to ${u.email}.`) }] : []),
      ...(self || lastOwner ? [] : ['separator',
        u.status === 'suspended'
          ? { label: 'Reactivate', icon: PlayIcon, onSelect: () => { update(u.email, { status: 'active' }); notify(`${u.name} can sign in again.`); } }
          : { label: 'Suspend', icon: NoSymbolIcon, onSelect: () => setConfirm({ kind: 'suspend', user: u }) },
        { label: 'Remove from instance', icon: TrashIcon, danger: true, onSelect: () => setConfirm({ kind: 'remove', user: u }) }]),
    ];
  }

  const confirmCopy = confirm && {
    owner: { title: `Make ${confirm.user.name ?? confirm.user.email} an owner?`, body: 'Owners have full control, including removing other owners. You stay an owner too.', action: 'Make owner', run: () => update(confirm.user.email, { role: 'owner', projects: 'all' }) },
    revoke: { title: `Revoke the invitation for ${confirm.user.email}?`, body: 'The invitation link stops working immediately.', action: 'Revoke', danger: true, run: () => setUsers(l => l.filter(u => u.email !== confirm.user.email)) },
    mfa: { title: `Reset MFA for ${confirm.user.name}?`, body: 'Their authenticator and recovery codes stop working. They\'ll set up MFA again at their next sign-in.', action: 'Reset MFA', danger: true, run: () => update(confirm.user.email, { mfa: false }) },
    suspend: { title: `Suspend ${confirm.user.name}?`, body: 'They\'re signed out everywhere and can\'t sign in. Their API tokens stop working. Nothing they own is deleted.', action: 'Suspend', danger: true, run: () => update(confirm.user.email, { status: 'suspended' }) },
    remove: { title: `Remove ${confirm.user.name}?`, body: 'Their account, sessions and personal API tokens are deleted. Deployments they made stay in the history.', action: 'Remove', danger: true, run: () => setUsers(l => l.filter(u => u.email !== confirm.user.email)) },
  }[confirm.kind];

  return <div className="settings-page users-page">
    {noMfa.length > 0 && <Notice tone="warning" title={`${noMfa.length} active ${noMfa.length === 1 ? 'user doesn\'t' : 'users don\'t'} use MFA`}>{noMfa.map(u => u.name).join(', ')}. You can require MFA for everyone in Authentication & Security.</Notice>}

    <div className="users-toolbar">
      <SegmentedControl label="Status" value={tab} onChange={setTab} className="users-tabs" options={[
        { value: 'all', label: `All ${counts.all}` }, { value: 'active', label: `Active ${counts.active}` },
        { value: 'invited', label: `Invited ${counts.invited}` }, { value: 'suspended', label: `Suspended ${counts.suspended}` },
      ]} />
      <Select label="Role" value={roleFilter} onChange={setRoleFilter} className="users-role-filter" options={[{ value: 'all', label: 'All roles' }, ...ROLES.map(r => ({ value: r.value, label: r.label }))]} />
      <SearchField value={query} onChange={setQuery} placeholder="Search name or email" className="users-search" />
      <Button variant="primary" icon={UserPlusIcon} onClick={() => setInviting(true)}>Invite users</Button>
    </div>

    <div className="settings-card users-list">
      {rows.map(u => {
        const [tone, label] = statusTone[u.status];
        const lock = roleLock(u);
        return <div key={u.email} className={cx('user-row', u.status !== 'active' && u.status)}>
          <Avatar name={u.name ?? u.email} size={30} />
          <span className="user-main">
            <strong>{u.name ?? u.email}{u.email === ME && <span className="rp-chip">You</span>}</strong>
            <span>{u.name ? u.email : `Invited ${u.invited} · expires ${u.expires}`}</span>
          </span>
          <span className="user-access">{u.projects === 'all' ? 'All projects' : u.projects.length ? u.projects.join(', ') : 'No projects'}</span>
          <span className="user-mfa" title={u.mfa ? 'MFA enabled' : 'MFA not set up'}>{u.status === 'invited' ? '—' : u.mfa ? <><CheckIcon />MFA</> : <><MinusIcon />No MFA</>}</span>
          <span className="user-status"><Status tone={tone}>{label}</Status><small>{u.lastActive ? `Active ${u.lastActive}` : ''}</small></span>
          <span className="user-role" title={lock ?? undefined}>
            {lock || u.status === 'suspended' ? <span className="user-role-static">{roleLabel[u.role]}</span>
              : <Select label={`Role for ${u.name ?? u.email}`} value={u.role} onChange={role => changeRole(u, role)} options={ROLES} />}
          </span>
          <Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${u.name ?? u.email}`} items={actions(u)} />
        </div>;
      })}
      {!rows.length && <p className="users-empty">No users match. <button type="button" className="inline-link" onClick={() => { setQuery(''); setTab('all'); setRoleFilter('all'); }}>Clear filters</button></p>}
    </div>
    {flash && <p className="users-flash" role="status">{flash}</p>}

    <section className="settings-group">
      <h2>Roles and permissions</h2>
      <p>Built-in roles. Owners and admins see every project; developers and viewers only see projects they're assigned to.</p>
      <div className="settings-card perm-table-wrap">
        <table className="perm-table">
          <thead><tr><th>Permission</th>{ROLES.map(r => <th key={r.value}>{r.label}<small>{users.filter(u => u.role === r.value).length} {users.filter(u => u.role === r.value).length === 1 ? 'user' : 'users'}</small></th>)}</tr></thead>
          <tbody>{PERMISSIONS.map(([name, roles]) => <tr key={name}><th>{name}</th>{ROLES.map(r => <td key={r.value}>{roles.includes(r.value) ? <CheckIcon className="perm-yes" aria-label="Allowed" /> : <MinusIcon className="perm-no" aria-label="Not allowed" />}</td>)}</tr>)}</tbody>
        </table>
      </div>
    </section>

    {inviting && <InviteDialog existing={users.map(u => u.email)} onClose={() => setInviting(false)} onInvite={(emails, role, access) => {
      setUsers(list => [...list, ...emails.map(email => ({ email, name: null, role, status: 'invited', mfa: false, lastActive: null, invited: 'Just now', expires: 'in 7 days', projects: rank[role] >= 2 ? 'all' : access }))]);
      setInviting(false);
      setTab('all');
      notify(`${emails.length} ${emails.length === 1 ? 'invitation' : 'invitations'} sent.`);
    }} />}

    <Dialog open={!!editingAccess} onClose={() => setEditingAccess(null)} title="Project access" description={editingAccess && `Projects ${users.find(u => u.email === editingAccess.email)?.name ?? editingAccess.email} can see.`}
      footer={<><Button onClick={() => setEditingAccess(null)}>Cancel</Button><Button variant="primary" onClick={() => { update(editingAccess.email, { projects: editingAccess.projects }); notify('Project access updated.'); setEditingAccess(null); }}>Save</Button></>}>
      {editingAccess && <ProjectPicker value={editingAccess.projects} onChange={projects => setEditingAccess(e => ({ ...e, projects }))} />}
    </Dialog>

    <Dialog open={!!confirm} onClose={() => setConfirm(null)} title={confirmCopy?.title} description={confirmCopy?.body}
      footer={<><Button onClick={() => setConfirm(null)}>Cancel</Button><Button variant={confirmCopy?.danger ? undefined : 'primary'} className={cx(confirmCopy?.danger && 'danger-solid')} onClick={() => { confirmCopy.run(); notify('Done.'); setConfirm(null); }}>{confirmCopy?.action}</Button></>} />
  </div>;
}

function ProjectPicker({ value, onChange }) {
  const all = value.length === allProjects.length;
  return <div className="project-picker">
    <label className="picker-row all"><Checkbox label="All projects" checked={all} indeterminate={!all && value.length > 0} onChange={() => onChange(all ? [] : [...allProjects])} />All projects</label>
    {allProjects.map(p => <label key={p} className="picker-row"><Checkbox label={p} checked={value.includes(p)} onChange={on => onChange(on ? [...value, p] : value.filter(x => x !== p))} />{p}</label>)}
  </div>;
}

function InviteDialog({ existing, onClose, onInvite }) {
  const [text, setText] = useState('');
  const [role, setRole] = useState('developer');
  const [access, setAccess] = useState([]);
  const [error, setError] = useState('');
  const emails = [...new Set(text.split(/[\s,;]+/).map(e => e.trim().toLowerCase()).filter(Boolean))];
  const invalid = emails.filter(e => !EMAIL_RE.test(e));
  const duplicates = emails.filter(e => existing.includes(e));

  function submit() {
    if (!emails.length) return setError('Enter at least one email address.');
    if (invalid.length) return setError(`Not a valid email: ${invalid.join(', ')}`);
    if (duplicates.length) return setError(`Already a member or invited: ${duplicates.join(', ')}`);
    if (rank[role] < 2 && !access.length) return setError('Choose at least one project, or make them an admin.');
    onInvite(emails, role, access);
  }

  return <Dialog open onClose={onClose} title="Invite users" description="They'll get an email with a link that expires in 7 days."
    footer={<><span className="invite-count">{emails.length ? `${emails.length} ${emails.length === 1 ? 'person' : 'people'}` : ''}</span><Button onClick={onClose}>Cancel</Button><Button variant="primary" icon={EnvelopeIcon} onClick={submit}>Send {emails.length > 1 ? `${emails.length} invitations` : 'invitation'}</Button></>}>
    <FormField label="Email addresses" hint="Separate several with commas or new lines." htmlFor="invite-emails">
      <textarea id="invite-emails" className="labels-input invite-emails" rows={3} value={text} placeholder="sam@acme.dev, alex@acme.dev" onChange={e => { setText(e.target.value); setError(''); }} autoFocus />
    </FormField>
    <FormField label="Role" htmlFor="invite-role"><Select id="invite-role" label="Role" value={role} onChange={r => { setRole(r); setError(''); }} options={ROLES.filter(r => r.value !== 'owner')} /></FormField>
    {rank[role] < 2
      ? <FormField label="Projects"><ProjectPicker value={access} onChange={a => { setAccess(a); setError(''); }} /></FormField>
      : <p className="invite-note">Admins can see every project.</p>}
    {error && <p className="rp-error invite-error">{error}</p>}
  </Dialog>;
}

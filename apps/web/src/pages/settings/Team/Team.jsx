import { useEffect, useMemo, useState } from 'react';
import {
  CheckIcon, ClipboardDocumentIcon, EllipsisHorizontalIcon, LinkIcon, ShieldCheckIcon, UserMinusIcon, UserPlusIcon, XMarkIcon,
} from '@heroicons/react/16/solid';
import { Avatar, Button, Dialog, FormField, Menu, Notice, SearchField, Select, Status, Switch, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { useSession } from '../../../app/AuthGate';
import { TEAM_ROLES, TeamMark, roleLabel, useTeam } from '../../../app/Teams';
import '../settings.css';
import '../Users/Users.css';
import './Team.css';
import { ImageUpload } from '../../../ui/components/ImageUpload/ImageUpload';

/*
 * The active team: its name and its members. What the installation offers (experiences) is an
 * instance setting, under Instance administration › General. Team
 * roles only cover this team's work; signing in, two-factor and platform settings stay with the
 * account (Instance administration › Users). Guardrails match the API: only owners manage owners, nobody changes
 * their own role here, and a team always keeps an active owner.
 */

const teamAdmin = team => team.role === 'owner' || team.role === 'admin';

export function TeamSettings() {
  const { team, refresh } = useTeam();
  const canEdit = teamAdmin(team);
  const [name, setName] = useState(team.name);
  const [feedback, setFeedback] = useState(null);
  const [pending, setPending] = useState(false);
  const dirty = name.trim() !== team.name;

  async function save(e) {
    e.preventDefault();
    if (!name.trim()) return setFeedback({ tone: 'danger', title: 'Name the team.' });
    setPending(true); setFeedback(null);
    try {
      await api.updateTeam(team.id, { name: name.trim(), experiences: team.experiences }); // experiences are an instance setting now
      await refresh(team.id);
      setFeedback({ tone: 'success', title: 'Saved' });
    } catch (err) { setFeedback({ tone: 'danger', title: 'Couldn\'t save', text: err.fields ? Object.values(err.fields).join(' ') : err.message }); }
    setPending(false);
  }

  return <div className="settings-page team-page">
    <header className="team-head">
      <TeamMark team={team} size={44} />
      <span><h2>{team.name}</h2><small>You're {team.role === 'admin' || team.role === 'owner' ? 'an' : 'a'} {roleLabel[team.role]?.toLowerCase()}: {TEAM_ROLES.find(r => r.value === team.role)?.description.toLowerCase()}.</small></span>
    </header>
    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}</Notice>}

    <form className="settings-group" onSubmit={save}>
      <h2>Team</h2>
      <div className="settings-card team-card">
        <ImageUpload key={team.id} label="Team icon" name={team.name} src={team.iconUrl} square disabled={!canEdit}
          onUpload={file => api.uploadTeamIcon(team.id, file)} onRemove={() => api.removeTeamIcon(team.id)} onChanged={() => refresh(team.id)} />
        <FormField label="Name" htmlFor="team-name" hint="Shown in the team menu and to everyone in the team.">
          <TextInput id="team-name" value={name} maxLength={80} disabled={!canEdit} onChange={e => setName(e.target.value)} />
        </FormField>
        <div className="team-field-row">
          <span><strong>Team ID</strong><small>Appears in this team's links. It never changes, even when the team is renamed.</small></span>
          <span className="team-id-copy"><code className="team-id">{team.slug}</code><CopyId value={team.slug} /></span>
        </div>
      </div>
    </form>


    {canEdit && <div className="team-save">{dirty && <span className="team-muted-text">Unsaved changes</span>}<Button variant="primary" disabled={!dirty || pending} onClick={save}>{pending ? 'Saving…' : 'Save'}</Button></div>}
    {!canEdit && <Notice tone="info">Only team owners and admins can change these settings.</Notice>}

  </div>;
}

function CopyId({ value }) {
  const [copied, setCopied] = useState(false);
  return <button type="button" className="icon-action" title="Copy" aria-label="Copy team ID" onClick={() => { navigator.clipboard?.writeText(value); setCopied(true); setTimeout(() => setCopied(false), 1500); }}>
    {copied ? <CheckIcon /> : <ClipboardDocumentIcon />}</button>;
}

/* --------------------------------- Members --------------------------------- */

export function TeamMembers() {
  const { team, refresh } = useTeam();
  const session = useSession();
  const me = session.platformUser ?? session.user;
  const admin = teamAdmin(team);
  const members = useQuery(() => api.teamMembers(team.id), [team.id], { pollMs: 30000 });
  const invites = useQuery(() => (admin ? api.teamInvitations(team.id) : Promise.resolve({ invitations: [] })), [team.id, admin]);
  const [query, setQuery] = useState('');
  const [feedback, setFeedback] = useState(null);
  const [confirm, setConfirm] = useState(null);
  const [inviting, setInviting] = useState(false);
  const [link, setLink] = useState(null);
  const list = members.data?.members ?? [];
  const pending = invites.data?.invitations ?? [];
  const owners = list.filter(m => m.role === 'owner' && m.status !== 'suspended').length;
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return list.filter(m => !q || (m.name ?? '').toLowerCase().includes(q) || m.email.toLowerCase().includes(q));
  }, [list, query]);

  async function run(fn, done) {
    setFeedback(null);
    try { await fn(); if (done) setFeedback({ tone: 'success', title: done }); members.refetch(); invites.refetch(); } catch (e) { setFeedback({ tone: 'danger', title: 'Couldn\'t do that', text: e.message }); }
  }
  const lockReason = m => m.userId === me.id ? 'You can\'t change your own role.'
    : m.role === 'owner' && team.role !== 'owner' ? 'Only owners can manage owners.'
      : m.role === 'owner' && owners <= 1 ? 'The only owner. Make someone else an owner first.' : null;
  const roleOptions = TEAM_ROLES.filter(r => r.value !== 'owner' || team.role === 'owner').map(r => ({ value: r.value, label: r.label }));
  function changeRole(m, role) {
    if (role === m.role) return;
    const down = TEAM_ROLES.findIndex(r => r.value === role) > TEAM_ROLES.findIndex(r => r.value === m.role);
    if (role === 'owner' || down) return setConfirm({ kind: 'role', member: m, role });
    run(() => api.setTeamMember(team.id, m.userId, { role, permissionOverrides: m.permissionOverrides }), `${m.name || m.email} is now ${role === 'admin' || role === 'owner' ? 'an' : 'a'} ${roleLabel[role].toLowerCase()}`);
  }

  const confirmCopy = confirm && (confirm.kind === 'remove' ? {
    title: `Remove ${confirm.member.name || confirm.member.email} from ${team.name}?`,
    body: 'They lose access to everything in this team, including project roles, and their resource tokens and automation hooks for this team stop working. Their account and other teams aren\'t affected.',
    action: 'Remove from team', danger: true, run: () => api.removeTeamMember(team.id, confirm.member.userId), done: `${confirm.member.name || confirm.member.email} was removed`,
  } : {
    title: confirm.role === 'owner' ? `Make ${confirm.member.name || confirm.member.email} an owner?` : `Change ${confirm.member.name || confirm.member.email} to ${roleLabel[confirm.role].toLowerCase()}?`,
    body: confirm.role === 'owner' ? 'Owners can do everything in this team, including managing other owners and removing you.' : `They'll keep only what a ${roleLabel[confirm.role].toLowerCase()} can do: ${TEAM_ROLES.find(r => r.value === confirm.role).description.toLowerCase()}.`,
    action: 'Change role', danger: confirm.role !== 'owner', run: () => api.setTeamMember(team.id, confirm.member.userId, { role: confirm.role, permissionOverrides: confirm.member.permissionOverrides }),
    done: `${confirm.member.name || confirm.member.email} is now ${confirm.role === 'admin' || confirm.role === 'owner' ? 'an' : 'a'} ${roleLabel[confirm.role].toLowerCase()}`,
  });

  if (members.error && !members.data) return <div className="settings-page"><Notice tone="danger" title="Couldn't load members">{members.error.message} <button type="button" className="inline-link" onClick={members.refetch}>Retry</button></Notice></div>;

  return <div className="settings-page users-page">
    <div className="users-toolbar">
      <span className="team-count">{list.length} {list.length === 1 ? 'member' : 'members'}{pending.length ? ` · ${pending.length} invited` : ''}</span>
      <SearchField value={query} onChange={setQuery} placeholder="Search name or email" className="users-search" />
      {admin && <Button variant="primary" icon={UserPlusIcon} onClick={() => setInviting(true)}>Invite</Button>}
    </div>
    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}</Notice>}
    {!admin && <Notice tone="info">Team owners and admins manage members. You can see who's in {team.name}.</Notice>}

    <div className="settings-card users-list">
      {shown.map(m => {
        const lock = lockReason(m);
        const suspended = m.status === 'suspended';
        return <div key={m.userId} className={cx('user-row', suspended && 'suspended')}>
          <Avatar src={m.avatarUrl} name={m.name || m.email} size={30} />
          <span className="user-main">
            <strong>{m.name || m.email}{m.userId === me.id && <span className="rp-chip">You</span>}
              {m.mfa && <span className="user-badge ok" title="Two-factor authentication is on"><ShieldCheckIcon />2FA</span>}
              {(m.permissionOverrides?.grant?.length > 0 || m.permissionOverrides?.deny?.length > 0) && <span className="user-badge custom" title="Permissions differ from the role">Custom permissions</span>}
            </strong>
            <span>{m.name ? m.email : 'No name yet'}</span>
          </span>
          <span className="user-status">{suspended ? <Status tone="neutral">Suspended</Status> : <Status tone="success">Active</Status>}
            {m.joinedAt && <small>Joined {timeAgo(m.joinedAt)}</small>}</span>
          <span className="user-role" title={lock ?? undefined}>
            {!admin || lock ? <span className="user-role-static">{roleLabel[m.role] ?? m.role}</span>
              : <Select label={`Team role for ${m.name || m.email}`} value={m.role} options={roleOptions} onChange={role => changeRole(m, role)} />}
          </span>
          {admin && m.userId !== me.id && !(m.role === 'owner' && (team.role !== 'owner' || owners <= 1))
            ? <Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${m.name || m.email}`} items={[{ label: 'Remove from team', icon: UserMinusIcon, danger: true, onSelect: () => setConfirm({ kind: 'remove', member: m }) }]} />
            : <span className="user-menu-gap" />}
        </div>;
      })}
      {admin && pending.map(i => <div key={i.id} className="user-row invited">
        <Avatar name={i.email} size={30} />
        <span className="user-main"><strong>{i.email}</strong><span>Invitation expires {timeAgo(i.expiresAt).replace(' ago', '')}</span></span>
        <span className="user-status"><Status tone="warning">Invited</Status></span>
        <span className="user-role"><span className="user-role-static">{roleLabel[i.role]}</span></span>
        <Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${i.email}`} items={[
          { label: 'Revoke invitation', icon: XMarkIcon, danger: true, onSelect: () => run(() => api.revokeTeamInvitation(team.id, i.id), `Invitation for ${i.email} revoked`) },
        ]} />
      </div>)}
      {!members.data && <p className="users-empty">Loading…</p>}
      {members.data && !shown.length && <p className="users-empty">No one matches. <button type="button" className="inline-link" onClick={() => setQuery('')}>Clear search</button></p>}
    </div>

    <section className="settings-group">
      <h2>Team roles</h2>
      <p>A role covers everything in {team.name}. For one project only, give someone the Member role and assign them under Projects › Access.</p>
      <div className="settings-card">
        {TEAM_ROLES.map(r => <div key={r.value} className="team-toggle-row"><span><strong>{r.label}</strong><small>{r.description}</small></span>
          <span className="team-count">{list.filter(m => m.role === r.value).length}</span></div>)}
      </div>
    </section>

    {inviting && <InviteDialog team={team} options={roleOptions.filter(r => r.value !== 'owner')} existing={list.map(m => m.email.toLowerCase())}
      onClose={() => setInviting(false)} onInvited={(email, url) => { setInviting(false); setLink({ email, url }); invites.refetch(); refresh(team.id); }} />}
    <Dialog open={!!link} onClose={() => setLink(null)} icon={LinkIcon} title={`Invitation link for ${link?.email}`}
      description="Send it to them yourself. Ignition doesn't send email. It works once, for 7 days, and only for that email address."
      footer={<Button variant="primary" onClick={() => setLink(null)}>Done</Button>}>
      {link && <LinkBox url={link.url} />}
    </Dialog>
    <Dialog open={!!confirm} onClose={() => setConfirm(null)} title={confirmCopy?.title} description={confirmCopy?.body}
      footer={<><Button onClick={() => setConfirm(null)}>Cancel</Button><Button variant={confirmCopy?.danger ? 'danger-solid' : 'primary'}
        onClick={() => { const c = confirmCopy; setConfirm(null); run(c.run, c.done); }}>{confirmCopy?.action}</Button></>} />
  </div>;
}

function InviteDialog({ team, options, existing, onClose, onInvited }) {
  const [email, setEmail] = useState('');
  const [role, setRole] = useState('developer');
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  useEffect(() => setError(''), [email]);
  async function submit(e) {
    e.preventDefault();
    const address = email.trim();
    if (!/^\S+@\S+\.\S+$/.test(address)) return setError('Enter their email address.');
    if (existing.includes(address.toLowerCase())) return setError(`${address} is already in ${team.name}.`);
    setPending(true);
    try { const out = await api.inviteTeamMember(team.id, { email: address, role }); onInvited(address, `${location.origin}${out.path}`); } catch (err) { setError(err.fields ? Object.values(err.fields).join(' ') : err.message); setPending(false); }
  }
  return <Dialog open onClose={onClose} icon={UserPlusIcon} title={`Invite someone to ${team.name}`} description="You'll get a one-time link to send them. If they don't have an account yet, they create one from the link."
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" type="submit" form="team-invite" disabled={pending}>{pending ? 'Creating…' : 'Create invitation link'}</Button></>}>
    <form id="team-invite" className="invite-form" onSubmit={submit}>
      <FormField label="Email" htmlFor="team-invite-email" error={error}><TextInput id="team-invite-email" type="email" value={email} autoFocus placeholder="sam@example.com" onChange={e => setEmail(e.target.value)} /></FormField>
      <FormField label="Team role" htmlFor="team-invite-role" hint={TEAM_ROLES.find(r => r.value === role)?.description}><Select id="team-invite-role" label="Team role" value={role} onChange={setRole} options={options} /></FormField>
    </form>
  </Dialog>;
}

function LinkBox({ url }) {
  const [copied, setCopied] = useState(false);
  return <div className="link-box">
    <code>{url}</code>
    <Button icon={copied ? CheckIcon : ClipboardDocumentIcon} onClick={() => { navigator.clipboard?.writeText(url); setCopied(true); setTimeout(() => setCopied(false), 1500); }}>{copied ? 'Copied' : 'Copy'}</Button>
  </div>;
}


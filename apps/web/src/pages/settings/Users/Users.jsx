import { useMemo, useState } from 'react';
import {
  AdjustmentsHorizontalIcon, ArrowRightStartOnRectangleIcon, CheckIcon, ClipboardDocumentIcon, EllipsisHorizontalIcon, KeyIcon, LinkIcon, MinusIcon, NoSymbolIcon, PlayIcon,
  ShieldCheckIcon, ShieldExclamationIcon, TrashIcon, UserPlusIcon, XMarkIcon,
} from '@heroicons/react/16/solid';
import { Avatar, Button, Dialog, FormField, Menu, Notice, SearchField, SegmentedControl, Select, Status, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { can, useSession } from '../../../app/AuthGate';
import '../settings.css';
import './Users.css';

/*
 * Instance-wide accounts and roles. There's no email delivery in a self-hosted install, so invitations and
 * password resets are one-time links to share. Guardrails (enforced by the API too): you can't change or remove
 * yourself, only owners manage owners, and there's always at least one active owner.
 */

const ROLES = [
  { value: 'owner', label: 'Owner', description: 'Everything, including managing owners' },
  { value: 'admin', label: 'Admin', description: 'Users, settings, projects and secrets' },
  { value: 'developer', label: 'Developer', description: 'Deploy and configure resources' },
  { value: 'viewer', label: 'Viewer', description: 'Read-only' },
];
const roleLabel = Object.fromEntries(ROLES.map(r => [r.value, r.label]));
const rank = { viewer: 0, developer: 1, admin: 2, owner: 3 };
const statusTone = { active: ['success', 'Active'], suspended: ['neutral', 'Suspended'] };

export function Users() {
  const { user: me } = useSession();
  const { data, error, refetch } = useQuery(() => api.users(), [], { pollMs: 30000 });
  const catalog = useQuery(() => api.permissions(), []).data?.permissions ?? [];
  const [editing, setEditing] = useState(null);
  const [query, setQuery] = useState('');
  const [tab, setTab] = useState('all');
  const [inviting, setInviting] = useState(false);
  const [confirm, setConfirm] = useState(null);
  const [link, setLink] = useState(null); // { title, url, note }
  const [feedback, setFeedback] = useState(null);
  const users = data?.users ?? [];
  const invites = data?.invites ?? [];
  const owners = users.filter(u => u.role === 'owner' && u.status === 'active').length;

  const counts = { all: users.length + invites.length, active: users.filter(u => u.status === 'active').length, invited: invites.length, suspended: users.filter(u => u.status === 'suspended').length };
  const rows = useMemo(() => {
    const q = query.trim().toLowerCase();
    const match = (...v) => !q || v.some(x => x?.toLowerCase().includes(q));
    return {
      users: tab === 'invited' ? [] : users.filter(u => (tab === 'all' || u.status === tab) && match(u.name, u.email)),
      invites: tab === 'all' || tab === 'invited' ? invites.filter(i => match(i.email)) : [],
    };
  }, [users, invites, query, tab]);

  async function run(fn, success) {
    setFeedback(null);
    try { const out = await fn(); if (success) setFeedback({ tone: 'success', title: typeof success === 'function' ? success(out) : success }); refetch(); return out; } catch (err) { setFeedback({ tone: 'danger', title: 'Couldn\'t do that', text: err.message }); }
  }
  // Why this person's role can't be changed here (or null).
  const lockReason = u => u.id === me.id ? 'You can\'t change your own role.'
    : u.role === 'owner' && me.role !== 'owner' ? 'Only owners can manage owners.'
      : u.role === 'owner' && owners <= 1 ? 'The only owner. Make someone else an owner first.' : null;
  const roleOptions = ROLES.filter(r => r.value !== 'owner' || me.role === 'owner');

  const actions = u => {
    const self = u.id === me.id;
    const manageable = !self && (me.role === 'owner' || u.role !== 'owner');
    const lastOwner = u.role === 'owner' && u.status === 'active' && owners <= 1;
    return [
      ...(manageable || self ? [{ label: 'Create password reset link', icon: KeyIcon, onSelect: () => run(() => api.resetLink(u.id)).then(out => out && setLink({ title: `Password reset link for ${u.name || u.email}`, url: out.url, note: `Works once, until ${new Date(out.expiresAt).toLocaleString()}. Setting a new password signs them out everywhere.` })) }] : []),
      ...(u.sessions > 0 && (manageable || self) ? [{ label: `Sign out everywhere (${u.sessions})`, icon: ArrowRightStartOnRectangleIcon, onSelect: () => setConfirm({ kind: 'signout', user: u }) }] : []),
      ...(manageable ? [{ label: 'Permissions…', icon: AdjustmentsHorizontalIcon, onSelect: () => setEditing(u) }] : []),
      ...(manageable && u.mfaEnabledAt ? [{ label: 'Reset two-factor authentication', icon: ShieldExclamationIcon, onSelect: () => setConfirm({ kind: 'mfa', user: u }) }] : []),
      ...(manageable && !lastOwner ? ['separator',
        u.status === 'active' ? { label: 'Suspend', icon: NoSymbolIcon, onSelect: () => setConfirm({ kind: 'suspend', user: u }) }
          : { label: 'Reactivate', icon: PlayIcon, onSelect: () => run(() => api.updateUser(u.id, { status: 'active' }), `${u.name || u.email} can sign in again`) },
        { label: 'Remove', icon: TrashIcon, danger: true, onSelect: () => setConfirm({ kind: 'remove', user: u }) },
      ] : []),
    ];
  };

  const confirmCopy = confirm && {
    signout: { title: `Sign ${confirm.user.name || confirm.user.email} out everywhere?`, body: confirm.user.id === me.id ? 'This includes this browser.' : `Ends all ${confirm.user.sessions} of their sessions. They can sign in again.`, action: 'Sign out everywhere', run: () => api.revokeSessions(confirm.user.id), done: 'Signed out everywhere' },
    suspend: { title: `Suspend ${confirm.user.name || confirm.user.email}?`, body: 'They\'re signed out now and can\'t sign in until reactivated. Their name stays in the audit log.', action: 'Suspend', danger: true, run: () => api.updateUser(confirm.user.id, { status: 'suspended' }), done: 'Suspended' },
    mfa: { title: `Reset two-factor authentication for ${confirm.user.name || confirm.user.email}?`, body: 'For a lost phone and recovery codes. Their authenticator and recovery codes stop working and they\'re signed out. If two-factor is enforced, they set it up again at the next sign-in.', action: 'Reset', danger: true, run: () => api.resetUserMfa(confirm.user.id), done: 'Two-factor authentication reset' },
    remove: { title: `Remove ${confirm.user.name || confirm.user.email}?`, body: 'Their account is deleted. Projects and resources they created stay; the audit log keeps their email.', action: 'Remove', danger: true, run: () => api.deleteUser(confirm.user.id), done: 'Removed' },
  }[confirm.kind];

  if (error && !data) return <div className="settings-page"><Notice tone="danger" title="Couldn't load users">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice></div>;

  return <div className="settings-page users-page">
    <div className="users-toolbar">
      <SegmentedControl label="Status" value={tab} onChange={setTab} className="users-tabs" options={[
        { value: 'all', label: `All ${counts.all}` }, { value: 'active', label: `Active ${counts.active}` },
        { value: 'invited', label: `Invited ${counts.invited}` }, { value: 'suspended', label: `Suspended ${counts.suspended}` },
      ]} />
      <SearchField value={query} onChange={setQuery} placeholder="Search name or email" className="users-search" />
      <Button variant="primary" icon={UserPlusIcon} onClick={() => setInviting(true)}>Invite</Button>
    </div>
    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}</Notice>}

    <div className="settings-card users-list">
      {rows.users.map(u => {
        const [tone, label] = u.locked ? ['warning', 'Locked'] : statusTone[u.status];
        const lock = lockReason(u);
        return <div key={u.id} className={cx('user-row', u.status !== 'active' && u.status)}>
          <Avatar name={u.name || u.email} size={30} />
          <span className="user-main">
            <strong>{u.name || u.email}{u.id === me.id && <span className="rp-chip">You</span>}
              {u.mfaEnabledAt ? <span className="user-badge ok" title={`Two-factor authentication on since ${new Date(u.mfaEnabledAt).toLocaleDateString()}`}><ShieldCheckIcon />2FA</span> : null}
              {u.sso && <span className="user-badge" title={u.hasPassword ? 'Linked to single sign-on; can also use a password' : 'Signs in with single sign-on only'}>SSO</span>}
              {(u.permissionOverrides?.grant?.length > 0 || u.permissionOverrides?.deny?.length > 0) && <button type="button" className="user-badge custom" title="Permissions differ from the role. Click to review." onClick={() => setEditing(u)}>Custom permissions</button>}
            </strong>
            <span>{u.name ? u.email : 'No name yet'}</span>
          </span>
          <span className="user-status"><Status tone={tone}>{label}</Status>
            <small title={u.locked ? 'Too many failed sign-ins; unlocks automatically after 15 minutes' : undefined}>{u.lastSeenAt ? `Active ${timeAgo(u.lastSeenAt)}` : `Joined ${timeAgo(u.createdAt)}`}</small></span>
          <span className="user-role" title={lock ?? undefined}>
            {lock || u.status === 'suspended' ? <span className="user-role-static">{roleLabel[u.role]}</span>
              : <Select label={`Role for ${u.name || u.email}`} value={u.role} options={roleOptions}
                onChange={role => role !== u.role && run(() => api.updateUser(u.id, { role }), `${u.name || u.email} is now ${roleLabel[role].toLowerCase() === 'admin' ? 'an admin' : `a ${roleLabel[role].toLowerCase()}`}`)} />}
          </span>
          {actions(u).length ? <Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${u.name || u.email}`} items={actions(u)} /> : <span className="user-menu-gap" />}
        </div>;
      })}
      {rows.invites.map(i => <div key={i.id} className="user-row invited">
        <Avatar name={i.email} size={30} />
        <span className="user-main"><strong>{i.email}</strong><span>Invited by {i.createdBy || 'someone'} {timeAgo(i.createdAt)} · link expires {timeAgo(i.expiresAt).replace(' ago', '')}</span></span>
        <span className="user-status"><Status tone="warning">Invited</Status></span>
        <span className="user-role"><span className="user-role-static">{roleLabel[i.role]}</span></span>
        <Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${i.email}`} items={[
          { label: 'Create a new link', icon: LinkIcon, onSelect: () => run(() => api.invite(i.email, i.role)).then(out => out && setLink({ title: `Invite link for ${i.email}`, url: out.url, note: 'The previous link stops working. This one works once, for 7 days.' })) },
          { label: 'Revoke invite', icon: XMarkIcon, danger: true, onSelect: () => run(() => api.revokeInvite(i.id), `Invite for ${i.email} revoked`) },
        ]} />
      </div>)}
      {!data && <p className="users-empty">Loading…</p>}
      {data && !rows.users.length && !rows.invites.length && <p className="users-empty">No one matches. <button type="button" className="inline-link" onClick={() => { setQuery(''); setTab('all'); }}>Clear filters</button></p>}
    </div>

    <section className="settings-group">
      <h2>Roles and permissions</h2>
      <p>Roles set the defaults. Grant or deny single permissions for one person from their menu › Permissions. Every project is visible to everyone who can sign in.</p>
      <div className="settings-card perm-table-wrap">
        <table className="perm-table">
          <thead><tr><th>Permission</th>{ROLES.map(r => { const n = users.filter(u => u.role === r.value).length; return <th key={r.value}>{r.label}<small>{n} {n === 1 ? 'user' : 'users'}</small></th>; })}</tr></thead>
          {groupCatalog(catalog).map(([group, perms]) => <tbody key={group}>
            <tr className="perm-group-row"><th colSpan={ROLES.length + 1}>{group}</th></tr>
            {perms.map(p => <tr key={p.key}><th>{p.label} <code className="perm-key">{p.key}</code><small>{p.description}</small></th>{ROLES.map(r => <td key={r.value}>{rank[r.value] >= rank[p.role] ? <CheckIcon className="perm-yes" aria-label="Allowed" /> : <MinusIcon className="perm-no" aria-label="Not allowed" />}</td>)}</tr>)}
          </tbody>)}
        </table>
      </div>
    </section>

    {editing && <PermissionsDialog user={editing} catalog={catalog} me={me} onClose={() => setEditing(null)}
      onSaved={u => { setEditing(null); setFeedback({ tone: 'success', title: `Permissions saved for ${u.name || u.email}`, text: 'They were signed out so the change applies right away.' }); refetch(); }} />}
    {inviting && <InviteDialog roles={roleOptions} onClose={() => setInviting(false)} onInvited={(email, url) => { setInviting(false); setLink({ title: `Invite link for ${email}`, url, note: 'Send it to them yourself (chat, email). It works once, for 7 days. They choose their name and password.' }); refetch(); }} />}

    <Dialog open={!!link} onClose={() => setLink(null)} icon={LinkIcon} title={link?.title} description={link?.note}
      footer={<Button variant="primary" onClick={() => setLink(null)}>Done</Button>}>
      {link && <LinkBox url={link.url} />}
    </Dialog>

    <Dialog open={!!confirm} onClose={() => setConfirm(null)} title={confirmCopy?.title} description={confirmCopy?.body}
      footer={<><Button onClick={() => setConfirm(null)}>Cancel</Button><Button variant={confirmCopy?.danger ? undefined : 'primary'} className={cx(confirmCopy?.danger && 'danger-solid')}
        onClick={() => { const c = confirmCopy; setConfirm(null); run(c.run, c.done); }}>{confirmCopy?.action}</Button></>} />
  </div>;
}

function LinkBox({ url }) {
  const [copied, setCopied] = useState(false);
  return <div className="link-box">
    <code>{url}</code>
    <Button icon={copied ? CheckIcon : ClipboardDocumentIcon} onClick={() => { navigator.clipboard?.writeText(url); setCopied(true); setTimeout(() => setCopied(false), 1500); }}>{copied ? 'Copied' : 'Copy'}</Button>
  </div>;
}

function InviteDialog({ roles, onClose, onInvited }) {
  const [email, setEmail] = useState('');
  const [role, setRole] = useState('developer');
  const [errors, setErrors] = useState({});
  const [pending, setPending] = useState(false);
  async function submit(e) {
    e.preventDefault();
    setPending(true);
    try { const out = await api.invite(email.trim(), role); onInvited(email.trim(), out.url); } catch (err) { setErrors(err.fields ?? { email: err.message }); }
    setPending(false);
  }
  return <Dialog open onClose={onClose} icon={UserPlusIcon} title="Invite someone" description="You'll get a one-time link to send them. Ignition doesn't send email."
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" type="submit" form="invite-form" disabled={pending}>{pending ? 'Creating…' : 'Create invite link'}</Button></>}>
    <form id="invite-form" className="invite-form" onSubmit={submit}>
      <FormField label="Email" htmlFor="invite-email" error={errors.email}><TextInput id="invite-email" type="email" value={email} autoFocus onChange={e => { setEmail(e.target.value); setErrors({}); }} placeholder="sam@example.com" /></FormField>
      <FormField label="Role" htmlFor="invite-role" error={errors.role} hint={ROLES.find(r => r.value === role)?.description}><Select id="invite-role" label="Role" value={role} onChange={setRole} options={roles} /></FormField>
    </form>
  </Dialog>;
}

const choiceLabel = { default: 'Role default', grant: 'Allow', deny: 'Deny' };

/** Catalog order, grouped by area: [[group, permissions], ...]. */
function groupCatalog(list) {
  const out = [];
  for (const p of list) {
    const last = out[out.length - 1];
    if (last?.[0] === p.group) last[1].push(p); else out.push([p.group, [p]]);
  }
  return out;
}

/** Grants and denials on top of one person's role. You can only grant what you have yourself. */
function PermissionsDialog({ user, catalog, me, onClose, onSaved }) {
  const initial = Object.fromEntries(catalog.filter(p => p.grantable).map(p => [p.key,
    user.permissionOverrides?.grant?.includes(p.key) ? 'grant' : user.permissionOverrides?.deny?.includes(p.key) ? 'deny' : 'default']));
  const [choice, setChoice] = useState(initial);
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  const roleHas = p => rank[user.role] >= rank[p.role];
  const effective = p => choice[p.key] === 'grant' || (choice[p.key] !== 'deny' && roleHas(p));
  const changed = JSON.stringify(choice) !== JSON.stringify(initial);
  const [query, setQuery] = useState('');
  const q = query.trim().toLowerCase();
  const groups = groupCatalog(catalog.filter(p => !q || [p.key, p.label, p.description, p.group].some(v => v.toLowerCase().includes(q))));
  async function save() {
    setPending(true);
    setError('');
    try {
      const grant = Object.keys(choice).filter(k => choice[k] === 'grant');
      const deny = Object.keys(choice).filter(k => choice[k] === 'deny');
      onSaved((await api.setUserPermissions(user.id, { grant, deny })).user);
    } catch (e) { setError(e.fields?.permissions ?? e.message); setPending(false); }
  }
  return <Dialog open onClose={onClose} icon={AdjustmentsHorizontalIcon} className="perm-dialog" title={`Permissions for ${user.name || user.email}`}
    description={`${roleLabel[user.role]} by role. Allow adds a permission the role doesn't include; deny removes one it does.`}
    footer={<>
      <Button onClick={() => setChoice(Object.fromEntries(Object.keys(choice).map(k => [k, 'default'])))} disabled={Object.values(choice).every(c => c === 'default')}>Reset to role</Button>
      <span className="spacer" />
      <Button onClick={onClose}>Cancel</Button>
      <Button variant="primary" onClick={save} disabled={!changed || pending}>{pending ? 'Saving…' : 'Save'}</Button>
    </>}>
    {error && <Notice tone="danger" title={error} />}
    <SearchField value={query} onChange={setQuery} placeholder="Search permissions, e.g. delete or servers." className="perm-search" />
    {groups.map(([group, perms]) => {
      const editable = perms.filter(p => p.grantable);
      // Allow-all only grants what you have yourself; deny-all only removes what the role gives.
      const allow = () => setChoice(c => ({ ...c, ...Object.fromEntries(editable.map(p => [p.key, roleHas(p) ? 'default' : can(me, p.key) ? 'grant' : c[p.key]])) }));
      const deny = () => setChoice(c => ({ ...c, ...Object.fromEntries(editable.map(p => [p.key, roleHas(p) ? 'deny' : 'default'])) }));
      return <section key={group} className="perm-edit-group">
        <header>
          <h4>{group}</h4>
          <span className="perm-edit-count">{perms.filter(effective).length} of {perms.length}</span>
          {editable.length > 1 && !query && <><button type="button" className="perm-edit-bulk" onClick={allow}>Allow all</button><button type="button" className="perm-edit-bulk" onClick={deny}>Deny all</button></>}
        </header>
        <ul className="perm-edit">
          {perms.map(p => {
            const cantGrant = !can(me, p.key);
            return <li key={p.key} className={cx(choice[p.key] && choice[p.key] !== 'default' && 'overridden')}>
              <span className={cx('perm-edit-state', effective(p) ? 'yes' : 'no')}>{effective(p) ? <CheckIcon aria-label="Allowed" /> : <MinusIcon aria-label="Not allowed" />}</span>
              <span className="perm-edit-main"><strong>{p.label} <code className="perm-key">{p.key}</code></strong><small>{p.description}</small></span>
              {!p.grantable ? <span className="perm-edit-fixed">Owners only</span>
                : <Select label={`${p.label} for ${user.name || user.email}`} value={choice[p.key]} onChange={v => setChoice(c => ({ ...c, [p.key]: v }))}
                  options={[
                    { value: 'default', label: `${choiceLabel.default} (${roleHas(p) ? 'yes' : 'no'})` },
                    ...(!roleHas(p) && (!cantGrant || choice[p.key] === 'grant') ? [{ value: 'grant', label: choiceLabel.grant }] : []),
                    ...(roleHas(p) ? [{ value: 'deny', label: choiceLabel.deny }] : []),
                  ]} />}
            </li>;
          })}
        </ul>
      </section>;
    })}
    {!groups.length && <p className="perm-edit-note">No permission matches “{query}”.</p>}
    <p className="perm-edit-note">Saving signs {user.name || user.email} out when “Invalidate on role change” is on, so the change applies at once.</p>
  </Dialog>;
}

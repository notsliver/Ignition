import { useEffect, useMemo, useState } from 'react';
import { ArrowDownTrayIcon, ChevronRightIcon } from '@heroicons/react/16/solid';
import { Avatar, Button, Notice, SearchField, Select, cx } from '../../../ui';
import { api } from '../../../api/client';
import '../settings.css';
import './AuditLog.css';

/*
 * Everything people (and Ignition itself) did, newest first. Events are written by the API as actions happen
 * and never contain secret values. Filters run on the server; CSV export uses the same filters.
 */

const LABELS = {
  'instance.setup': 'Set up Ignition',
  'auth.login': 'Signed in', 'auth.login_failed': 'Failed to sign in', 'auth.logout': 'Signed out',
  'user.invite': 'Invited someone', 'user.invite_revoked': 'Revoked an invite', 'user.joined': 'Joined', 'user.role': 'Changed a role',
  'user.suspend': 'Suspended a user', 'user.reactivate': 'Reactivated a user', 'user.remove': 'Removed a user',
  'user.sessions_revoked': 'Signed someone out everywhere', 'user.reset_link': 'Created a password reset link', 'user.password_reset': 'Reset their password',
  'project.create': 'Created a project', 'project.update': 'Updated a project', 'project.delete': 'Deleted a project', 'project.archive': 'Archived a project',
  'resource.create': 'Created a resource', 'resource.update': 'Updated a resource', 'resource.delete': 'Deleted a resource',
  'resource.start': 'Started', 'resource.stop': 'Stopped', 'resource.restart': 'Restarted',
  'resource.env.update': 'Changed variables', 'resource.env.reveal': 'Revealed a secret',
  'resource.terminal_open': 'Opened a terminal', 'resource.terminal_close': 'Closed a terminal',
  'resource.networking': 'Changed networking', 'resource.diagnostic': 'Ran a network test', 'resource.limits': 'Changed limits',
  'resource.storage': 'Changed volumes', 'resource.volume_backup': 'Downloaded a volume backup', 'resource.volume_delete': 'Deleted a volume',
  'resource.build_settings': 'Changed build settings',
  'deployment.manual': 'Deployed', 'deployment.redeploy': 'Redeployed', 'deployment.rollback': 'Rolled back', 'deployment.cancel': 'Cancelled a deployment',
  'domain.create': 'Added a domain', 'domain.update': 'Changed a domain', 'domain.delete': 'Removed a domain',
  'source.create': 'Added a source', 'source.update': 'Changed a source', 'source.rotate': 'Rotated a secret', 'source.delete': 'Deleted a source', 'source.test': 'Tested a source',
  'settings.general': 'Changed general settings', 'settings.updates': 'Changed update settings', 'settings.update_check': 'Checked for updates',
  'audit.export': 'Exported the audit log',
};
const CATEGORIES = { auth: 'Sign-ins', user: 'Users', project: 'Projects', resource: 'Resources', deployment: 'Deployments', domain: 'Domains', source: 'Sources', settings: 'Settings', instance: 'Instance', audit: 'Audit log' };
const RANGES = [
  { value: '', label: 'All time' }, { value: '86400', label: 'Last 24 hours' }, { value: '604800', label: 'Last 7 days' }, { value: '2592000', label: 'Last 30 days' },
];
const SENSITIVE = /^(auth\.login_failed|resource\.env\.reveal|resource\.terminal_open|resource\.volume_backup|user\.(role|suspend|remove)|source\.(delete|rotate)|audit\.export)$/;

const humanize = action => LABELS[action] ?? (action.split('.').slice(1).join(' ').replace(/_/g, ' ').replace(/^\w/, c => c.toUpperCase()) || action);
const dayLabel = d => {
  const today = new Date(); today.setHours(0, 0, 0, 0);
  const that = new Date(d); that.setHours(0, 0, 0, 0);
  const diff = Math.round((today - that) / 86400000);
  return diff === 0 ? 'Today' : diff === 1 ? 'Yesterday' : that.toLocaleDateString(undefined, { weekday: 'long', month: 'short', day: 'numeric', year: that.getFullYear() === today.getFullYear() ? undefined : 'numeric' });
};
const time = d => new Date(d).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' });

// The detail object, flattened for display.
function details(raw) {
  if (!raw || typeof raw !== 'object') return [];
  return Object.entries(raw).filter(([, v]) => v !== null && v !== '').map(([k, v]) => [k, typeof v === 'object' ? JSON.stringify(v) : String(v)]);
}

export function AuditLog({ onOpenResource }) {
  const [query, setQuery] = useState('');
  const [debounced, setDebounced] = useState('');
  const [actor, setActor] = useState('');
  const [category, setCategory] = useState('');
  const [range, setRange] = useState('');
  const [events, setEvents] = useState([]);
  const [targets, setTargets] = useState({});
  const [facets, setFacets] = useState({ actors: [], actions: [] });
  const [next, setNext] = useState(null);
  const [state, setState] = useState({ loading: true });
  const [open, setOpen] = useState(null);
  useEffect(() => { const t = setTimeout(() => setDebounced(query.trim()), 250); return () => clearTimeout(t); }, [query]);

  const params = useMemo(() => ({
    q: debounced, actor, action: category,
    since: range ? new Date(Date.now() - Number(range) * 1000).toISOString().replace(/\.\d+Z$/, 'Z') : undefined,
  }), [debounced, actor, category, range]);

  async function load(before) {
    setState({ loading: true, more: !!before });
    try {
      const out = await api.audit({ ...params, before });
      setEvents(e => before ? [...e, ...out.events] : out.events);
      setTargets(t => ({ ...(before ? t : {}), ...out.targets }));
      if (out.actors) setFacets({ actors: out.actors, actions: out.actions });
      setNext(out.nextBefore ?? null);
      setState({});
    } catch (err) { setState({ error: err.message }); }
  }
  useEffect(() => { load(); }, [params]); // eslint-disable-line react-hooks/exhaustive-deps

  const categoryOptions = [{ value: '', label: 'All activity' }, ...[...new Set(facets.actions.map(a => a.split('.')[0]))].map(c => ({ value: c, label: CATEGORIES[c] ?? c }))];
  const actorOptions = [{ value: '', label: 'Everyone' }, ...facets.actors.map(a => ({ value: a, label: a === 'system' ? 'Ignition (system)' : a }))];
  const filtered = debounced || actor || category || range;
  const groups = [];
  for (const e of events) {
    const label = dayLabel(e.createdAt);
    if (groups.at(-1)?.label !== label) groups.push({ label, events: [] });
    groups.at(-1).events.push(e);
  }

  function target(e) {
    const t = targets[e.targetId];
    const d = e.detail ?? {};
    if (t?.kind === 'resource') return <button type="button" className="audit-target link" onClick={ev => { ev.stopPropagation(); onOpenResource?.({ id: e.targetId, projectId: t.projectId }); }}>{t.name}</button>;
    if (t) return <span className="audit-target">{t.name}</span>;
    const name = d.name ?? d.email ?? d.hostname;
    if (name) return <span className="audit-target">{name}{e.targetId && e.targetType !== 'settings' && <em> (deleted)</em>}</span>;
    return null;
  }

  return <div className="settings-page audit-page">
    <div className="audit-toolbar">
      <SearchField value={query} onChange={setQuery} placeholder="Search people, actions, names, IPs" className="audit-search" />
      <Select label="Person" value={actor} options={actorOptions} onChange={setActor} searchable={8} className="audit-select" />
      <Select label="Activity" value={category} options={categoryOptions} onChange={setCategory} className="audit-select" />
      <Select label="Time range" value={range} options={RANGES} onChange={setRange} className="audit-select" />
      <a className="button" href={api.auditExportUrl(params)} download><ArrowDownTrayIcon />Export CSV</a>
    </div>
    {state.error && <Notice tone="danger" title="Couldn't load the audit log">{state.error} <button type="button" className="inline-link" onClick={() => load()}>Retry</button></Notice>}

    {groups.map(g => <section key={g.label} className="audit-day">
      <h2>{g.label}</h2>
      <div className="settings-card">
        {g.events.map(e => {
          const expanded = open === e.id;
          const rows = details(e.detail);
          return <div key={e.id} className={cx('audit-event', expanded && 'open', SENSITIVE.test(e.action) && 'sensitive')}>
            <button type="button" className="audit-row" aria-expanded={expanded} onClick={() => setOpen(expanded ? null : e.id)}>
              <ChevronRightIcon className="audit-caret" />
              <time dateTime={e.createdAt}>{time(e.createdAt)}</time>
              <span className="audit-actor"><Avatar name={e.actor} size={20} /><span>{e.actor === 'system' ? 'Ignition' : e.actor}</span></span>
              <span className="audit-what"><span className="audit-action">{humanize(e.action)}</span>{target(e)}</span>
            </button>
            {expanded && <dl className="audit-detail">
              <div><dt>Action</dt><dd className="mono">{e.action}</dd></div>
              {rows.map(([k, v]) => <div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}
              {e.targetType && <div><dt>Target</dt><dd className="mono">{e.targetType}{e.targetId && ` · ${e.targetId}`}</dd></div>}
              {e.ip && <div><dt>IP address</dt><dd className="mono">{e.ip}</dd></div>}
              <div><dt>Time</dt><dd className="mono">{new Date(e.createdAt).toISOString()}</dd></div>
              <div><dt>Event</dt><dd className="mono">#{e.id}</dd></div>
            </dl>}
          </div>;
        })}
      </div>
    </section>)}

    {!state.loading && !state.error && !events.length && <p className="audit-empty">{filtered ? 'Nothing matches these filters.' : 'Nothing has been recorded yet.'}</p>}
    {state.loading && !state.more && !events.length && <p className="audit-empty">Loading…</p>}
    {next && <div className="audit-more"><Button onClick={() => load(next)} disabled={state.loading}>{state.loading ? 'Loading…' : 'Load older events'}</Button></div>}
    <p className="audit-foot">Events never include secret values. Highlighted rows are sensitive (secrets revealed, terminals, failed sign-ins, role changes).</p>
  </div>;
}

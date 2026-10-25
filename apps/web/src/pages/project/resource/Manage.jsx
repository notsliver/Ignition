import { useMemo, useState } from 'react';
import {
  ArrowPathIcon, ArrowRightIcon, ArrowUturnLeftIcon, CalendarDaysIcon, EyeIcon, EyeSlashIcon, KeyIcon, PlayIcon, PlusIcon, TrashIcon,
} from '@heroicons/react/16/solid';
import { Avatar, Button, Checkbox, CommitIcon, Dialog, Notice, Select, Status, Switch, TextInput, cx } from '../../../ui';
import { projects } from '../../Projects/data';
import { deploymentsByProject } from '../Deployments/data';
import { SaveBar, SettingsField, SettingsGroup } from '../../settings/SettingsField';
import { CopyButton, Empty, PageIntro, Table, randomId, useLater, useSettingsForm } from './shared';

/* ------------------------------ Rollback History ---------------------------- */

export function RollbackHistory({ project, resource }) {
  const [releases, setReleases] = useState(() => {
    const own = (deploymentsByProject[project.name] ?? []).filter(d => d.resource === resource.name && d.status === 'success');
    const list = own.length ? own : [
      { id: randomId(), commit: '4e0b7d1', message: 'Latest release', author: 'you', time: resource.deployed ?? 'Today', branch: resource.branch ?? 'main', current: true },
      { id: randomId(), commit: 'f03c6aa', message: 'Bump dependencies', author: 'admin', time: 'Yesterday', branch: resource.branch ?? 'main' },
      { id: randomId(), commit: '19ce4b0', message: 'Initial release', author: 'admin', time: 'Oct 20', branch: resource.branch ?? 'main' },
    ];
    return list.some(d => d.current) ? list : list.map((d, i) => ({ ...d, current: i === 0 }));
  });
  const [events, setEvents] = useState([{ from: 'c2d88e9', to: '19ce4b0', by: 'admin', time: 'Oct 22', reason: 'Elevated error rate after deploy' }]);
  const [target, setTarget] = useState(null);
  const [withEnv, setWithEnv] = useState(false);
  const [reason, setReason] = useState('');
  const current = releases.find(r => r.current);

  function rollback() {
    setReleases(list => list.map(r => ({ ...r, current: r.id === target.id })));
    setEvents(list => [{ from: current?.commit, to: target.commit, by: 'you', time: 'Just now', reason: reason.trim() || null }, ...list]);
    setTarget(null);
    setReason('');
    setWithEnv(false);
  }

  return <div className="rp-page">
    <PageIntro>Instantly switch back to a previous successful release. Rollbacks reuse the original image, so nothing is rebuilt.</PageIntro>
    <section>
      <h2 className="rp-heading">Releases</h2>
      <div className="rp-card">
        {releases.map(r => <div key={r.id} className={cx('rp-row', r.current && 'current-release')}>
          <span className="rp-row-main">
            <strong>{r.message}{r.current && <span className="rp-chip success">Live</span>}</strong>
            <span><CommitIcon className="inline-icon" />{r.commit} · {r.branch} · {r.time}</span>
          </span>
          <Avatar name={r.author} />
          {r.current ? <span className="rp-row-meta">Current</span> : <Button icon={ArrowUturnLeftIcon} onClick={() => setTarget(r)}>Roll back</Button>}
        </div>)}
      </div>
    </section>
    <section>
      <h2 className="rp-heading">Rollback log</h2>
      {events.length ? <ol className="rp-card rollback-log">
        {events.map((e, i) => <li key={i} className="rp-row">
          <span className="rp-row-main"><strong className="rollback-move"><code>{e.from}</code><ArrowRightIcon /><code>{e.to}</code></strong><span>{e.reason ?? 'No reason given'}</span></span>
          <span className="rp-row-meta">{e.by} · {e.time}</span>
        </li>)}
      </ol> : <p className="rp-row-meta">No rollbacks yet.</p>}
    </section>

    <Dialog open={!!target} onClose={() => setTarget(null)} title="Roll back this resource?" description={target && `Traffic switches from ${current?.commit} to ${target.commit} immediately. You can roll forward again at any time.`}
      footer={<><Button onClick={() => setTarget(null)}>Cancel</Button><Button variant="primary" icon={ArrowUturnLeftIcon} onClick={rollback}>Roll back</Button></>}>
      <label className="check-row"><Checkbox label="Also restore environment variables" checked={withEnv} onChange={setWithEnv} />Also restore the environment variables from that release</label>
      <TextInput placeholder="Reason (optional), e.g. error spike after deploy" value={reason} onChange={event => setReason(event.target.value)} aria-label="Reason" />
    </Dialog>
  </div>;
}

/* ------------------------------ Scheduled Tasks ----------------------------- */

const schedules = [['*/5 * * * *', 'Every 5 minutes'], ['0 * * * *', 'Every hour'], ['0 3 * * *', 'Every day at 03:00'], ['0 3 * * 1', 'Every Monday at 03:00'], ['custom', 'Custom…']];
const describe = cron => schedules.find(s => s[0] === cron)?.[1] ?? cron;
const CRON_RE = /^(\S+\s+){4}\S+$/;

export function ScheduledTasks({ resource }) {
  const later = useLater();
  const [tasks, setTasks] = useState(() => [
    { id: 1, name: 'Clear expired sessions', schedule: '0 3 * * *', command: 'npm run sessions:prune', enabled: true, last: 'success', lastRun: '9h ago' },
    { id: 2, name: 'Send digest emails', schedule: '0 * * * *', command: 'node scripts/digest.js', enabled: true, last: 'failed', lastRun: '41m ago' },
    { id: 3, name: 'Rebuild search index', schedule: '0 3 * * 1', command: 'npm run search:reindex', enabled: false, last: 'success', lastRun: '6d ago' },
  ]);
  const [adding, setAdding] = useState(false);
  const [draft, setDraft] = useState({ name: '', schedule: '0 * * * *', custom: '', command: '' });
  const [error, setError] = useState('');
  const update = (id, patch) => setTasks(list => list.map(t => t.id === id ? { ...t, ...patch } : t));

  function run(task) {
    update(task.id, { last: 'running', lastRun: 'Now' });
    later(() => update(task.id, { last: 'success', lastRun: 'Just now' }), 2500);
  }
  function add(event) {
    event.preventDefault();
    const schedule = draft.schedule === 'custom' ? draft.custom.trim() : draft.schedule;
    if (!draft.name.trim()) return setError('Give the task a name.');
    if (!CRON_RE.test(schedule)) return setError('Schedules use five cron fields, like "*/10 * * * *".');
    if (!draft.command.trim()) return setError('Enter the command to run.');
    setTasks(list => [...list, { id: Date.now(), name: draft.name.trim(), schedule, command: draft.command.trim(), enabled: true, last: null, lastRun: 'Never' }]);
    setDraft({ name: '', schedule: '0 * * * *', custom: '', command: '' });
    setAdding(false);
    setError('');
  }
  const lastStatus = { success: ['success', 'Succeeded'], failed: ['danger', 'Failed'], running: ['progress', 'Running'] };

  return <div className="rp-page">
    <PageIntro actions={!adding && <Button variant="primary" icon={PlusIcon} onClick={() => setAdding(true)}>New task</Button>}>
      Run commands inside {resource.name}'s container on a schedule. Times use the instance's default timezone.
    </PageIntro>
    {adding && <form className="rp-card task-form" onSubmit={add}>
      <TextInput placeholder="Task name" value={draft.name} aria-label="Task name" autoFocus onChange={event => setDraft(d => ({ ...d, name: event.target.value }))} />
      <Select label="Schedule" options={schedules.map(([value, label]) => ({ value, label }))} value={draft.schedule} onChange={schedule => setDraft(d => ({ ...d, schedule }))} />
      {draft.schedule === 'custom' && <TextInput className="mono" placeholder="*/10 * * * *" value={draft.custom} aria-label="Cron expression" onChange={event => setDraft(d => ({ ...d, custom: event.target.value }))} />}
      <TextInput className="mono task-command" placeholder="npm run job" value={draft.command} aria-label="Command" onChange={event => setDraft(d => ({ ...d, command: event.target.value }))} />
      <div className="task-form-actions">{error && <p className="rp-error">{error}</p>}<Button type="button" onClick={() => { setAdding(false); setError(''); }}>Cancel</Button><Button type="submit" variant="primary">Create task</Button></div>
    </form>}
    {tasks.length ? <Table columns={[{ label: '' }, { label: 'Task' }, { label: 'Schedule' }, { label: 'Last run' }, { label: '', className: 'actions' }]}>
      {tasks.map(t => {
        const [tone, label] = lastStatus[t.last] ?? ['neutral', 'Never run'];
        return <tr key={t.id} className={cx(!t.enabled && 'disabled-row')}>
          <td className="switch-cell"><Switch label={`${t.enabled ? 'Disable' : 'Enable'} ${t.name}`} checked={t.enabled} onChange={enabled => update(t.id, { enabled })} /></td>
          <td><strong className="cell-title">{t.name}</strong><div className="cell-sub mono">{t.command}</div></td>
          <td><span>{describe(t.schedule)}</span><div className="cell-sub mono">{t.schedule}</div></td>
          <td><Status tone={tone}>{label}</Status><div className="cell-sub">{t.lastRun}</div></td>
          <td className="actions"><div>
            <button type="button" className="icon-action" aria-label={`Run ${t.name} now`} title="Run now" disabled={t.last === 'running'} onClick={() => run(t)}><PlayIcon /></button>
            <button type="button" className="icon-action danger" aria-label={`Delete ${t.name}`} title="Delete" onClick={() => setTasks(list => list.filter(x => x.id !== t.id))}><TrashIcon /></button>
          </div></td>
        </tr>;
      })}
    </Table> : !adding && <Empty icon={CalendarDaysIcon} title="No scheduled tasks">Create one to run cleanup jobs, reports or syncs on a schedule.</Empty>}
  </div>;
}

/* ------------------------------ Access & Secrets ---------------------------- */

const roles = [{ value: 'viewer', label: 'Viewer', description: 'See status, logs and metrics' }, { value: 'deployer', label: 'Deployer', description: 'Deploy, restart and roll back' }, { value: 'admin', label: 'Admin', description: 'Change settings and secrets' }];
const people = ['maya', 'jonas', 'lena', 'tom', 'admin'];

export function AccessSecrets({ resource }) {
  const [members, setMembers] = useState([{ name: 'admin', role: 'admin', inherited: true }, { name: 'maya', role: 'deployer', inherited: true }, { name: 'jonas', role: 'viewer' }]);
  const [invite, setInvite] = useState('');
  const [hook, setHook] = useState(() => `https://ignition.example.com/hooks/deploy/${randomId(24)}`);
  const [showHook, setShowHook] = useState(false);
  const [tokens, setTokens] = useState([{ id: 1, name: 'GitHub Actions', scope: 'deploy', created: 'Sep 30', used: '5m ago' }]);
  const [tokenName, setTokenName] = useState('');
  const [tokenScope, setTokenScope] = useState('deploy');
  const [newToken, setNewToken] = useState(null);
  const available = people.filter(p => !members.some(m => m.name === p));

  function createToken(event) {
    event.preventDefault();
    if (!tokenName.trim()) return;
    const value = `ign_${randomId(32)}`;
    setTokens(list => [...list, { id: Date.now(), name: tokenName.trim(), scope: tokenScope, created: 'Just now', used: 'Never' }]);
    setNewToken({ name: tokenName.trim(), value });
    setTokenName('');
  }

  return <div className="settings-page rp-settings">
    <section className="settings-group">
      <h2>People with access</h2>
      <p>Project members inherit access. Grant extra access to just this resource here.</p>
      <div className="rp-card">
        {members.map(m => <div key={m.name} className="rp-row">
          <Avatar name={m.name} size={26} />
          <span className="rp-row-main"><strong>{m.name}</strong><span>{m.inherited ? 'From project' : `Only ${resource.name}`}</span></span>
          {m.inherited ? <span className="rp-chip">{roles.find(r => r.value === m.role).label}</span>
            : <Select label={`Role for ${m.name}`} options={roles} value={m.role} onChange={role => setMembers(list => list.map(x => x.name === m.name ? { ...x, role } : x))} className="role-select" />}
          <button type="button" className="icon-action danger" aria-label={`Remove ${m.name}`} disabled={m.inherited} title={m.inherited ? 'Manage in project settings' : 'Remove'} onClick={() => setMembers(list => list.filter(x => x.name !== m.name))}><TrashIcon /></button>
        </div>)}
        {available.length > 0 && <div className="rp-row access-add">
          <Select label="Add person" options={[{ value: '', label: 'Add a person…' }, ...available.map(p => ({ value: p, label: p }))]} value={invite} onChange={setInvite} className="role-select wide" />
          <Button disabled={!invite} icon={PlusIcon} onClick={() => { setMembers(list => [...list, { name: invite, role: 'viewer' }]); setInvite(''); }}>Grant access</Button>
        </div>}
      </div>
    </section>

    <SettingsGroup title="Deploy hook" description="POST to this URL to trigger a deployment, e.g. from CI. Treat it like a password.">
      <div className="hook-row">
        <code className={cx('mono', !showHook && 'masked')}>{showHook ? hook : hook.replace(/[^/]+$/, '•'.repeat(24))}</code>
        <button type="button" className="icon-action" aria-label={showHook ? 'Hide hook URL' : 'Show hook URL'} onClick={() => setShowHook(s => !s)}>{showHook ? <EyeSlashIcon /> : <EyeIcon />}</button>
        <CopyButton value={hook} label="Copy hook URL" />
        <Button icon={ArrowPathIcon} onClick={() => { setHook(`https://ignition.example.com/hooks/deploy/${randomId(24)}`); setShowHook(true); }}>Regenerate</Button>
      </div>
    </SettingsGroup>

    <section className="settings-group">
      <h2>Service tokens</h2>
      <p>Tokens for scripts and CI. They only work for {resource.name}.</p>
      {newToken && <Notice tone="success" title={`Token "${newToken.name}" created`}>Copy it now — you won't see it again. <code className="mono token-value">{newToken.value}</code> <CopyButton value={newToken.value} label="Copy token" /></Notice>}
      <div className="rp-card">
        {tokens.map(t => <div key={t.id} className="rp-row">
          <KeyIcon className="volume-icon" />
          <span className="rp-row-main"><strong>{t.name}</strong><span>Scope: {t.scope} · created {t.created} · last used {t.used}</span></span>
          <Button className="danger-text" onClick={() => setTokens(list => list.filter(x => x.id !== t.id))}>Revoke</Button>
        </div>)}
        <form className="rp-row access-add" onSubmit={createToken}>
          <TextInput placeholder="Token name, e.g. GitHub Actions" value={tokenName} onChange={event => setTokenName(event.target.value)} aria-label="Token name" />
          <Select label="Token scope" options={[{ value: 'read', label: 'Read only' }, { value: 'deploy', label: 'Deploy' }, { value: 'admin', label: 'Full access' }]} value={tokenScope} onChange={setTokenScope} className="role-select" />
          <Button type="submit" icon={PlusIcon} disabled={!tokenName.trim()}>Create token</Button>
        </form>
      </div>
    </section>
  </div>;
}


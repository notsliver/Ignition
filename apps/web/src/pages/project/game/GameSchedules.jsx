import { useState } from 'react';
import { ArchiveBoxIcon, ArrowPathIcon, ChatBubbleLeftIcon, ClockIcon, CommandLineIcon, PlayIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, Notice, Select, Switch, TextInput } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { useSession, can } from '../../../app/AuthGate';
import { Empty } from '../resource/shared';
import './game.css';

/*
 * The routine jobs of running a server, as presets: a nightly restart with a countdown, regular
 * backups, announcements, any console command. Each is an automation underneath, so it shows under
 * Automations too, with its run history.
 */

const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
const hours = [1, 2, 3, 4, 6, 8, 12, 24];
const presets = {
  restart: { icon: ArrowPathIcon, title: 'Daily restart', text: 'Warns players, counts down, saves and restarts. Keeps long-running servers smooth.' },
  backup: { icon: ArchiveBoxIcon, title: 'Regular backups', text: 'Saves the world and archives the server, keeping the newest few.' },
  announce: { icon: ChatBubbleLeftIcon, title: 'Announcement', text: 'Broadcasts a message to everyone online, like rules or your Discord.' },
  command: { icon: CommandLineIcon, title: 'Console command', text: 'Runs any command on a schedule.' },
};
const kindOf = a => {
  const action = a.steps?.[0]?.action;
  return { game_restart: 'restart', game_backup: 'backup', game_announce: 'announce', game_command: 'command' }[action];
};
const describe = trigger => {
  const [m, h, , , ] = (trigger.schedule ?? '').split(' ');
  if (/^\d+$/.test(m) && /^\d+$/.test(h)) return `Every day at ${h.padStart(2, '0')}:${m.padStart(2, '0')} (${trigger.timezone})`;
  if (/^\d+$/.test(m) && h?.startsWith('*/')) return `Every ${h.slice(2)} hours`;
  if (h === '*' && /^\d+$/.test(m)) return 'Every hour';
  return `${trigger.schedule} (${trigger.timezone})`;
};

export function GameSchedules({ resource, onNavigate }) {
  const { user } = useSession();
  const canManage = can(user, 'automations.manage');
  const canRun = can(user, 'automations.run');
  const q = useQuery(() => api.automations({ resourceId: resource.id }), [resource.id], { pollMs: 15000 });
  const [creating, setCreating] = useState(null);
  const [result, setResult] = useState(null);
  const list = q.data?.automations ?? [];
  const schedules = list.filter(a => a.trigger?.type === 'schedule' && kindOf(a));
  const others = list.length - schedules.length;

  async function toggle(a, enabled) {
    try {
      await api.updateAutomation(a.id, { name: a.name, description: a.description, scope: a.scope, resourceId: a.resourceId, targets: a.targets, trigger: a.trigger,
        conditions: a.conditions, steps: a.steps, mode: a.mode, enabled, escalateAfter: a.escalateAfter, maxRunsPerHour: a.maxRunsPerHour });
      q.refetch();
    } catch (e) { setResult({ tone: 'danger', text: e.message }); }
  }
  async function remove(a) {
    if (!window.confirm(`Delete “${a.name}”?`)) return;
    try { await api.deleteAutomation(a.id); q.refetch(); } catch (e) { setResult({ tone: 'danger', text: e.message }); }
  }
  async function runNow(a) {
    try { await api.runAutomation(a.id, 'Run from Schedules'); setResult({ tone: 'success', text: `${a.name} is running. Its result shows under Automations.` }); q.refetch(); } catch (e) { setResult({ tone: 'danger', text: e.message }); }
  }

  return <div className="rp-page gm-page">
    {canManage && <div className="gm-presets">
      {Object.entries(presets).map(([k, p]) => <button key={k} type="button" className="gm-preset" onClick={() => setCreating(k)}>
        <p.icon /><span><strong>{p.title}</strong><small>{p.text}</small></span></button>)}
    </div>}
    {result && <Notice tone={result.tone}>{result.text}</Notice>}
    {q.error && !q.data && <Notice tone="danger" title="Couldn't load schedules">{q.error.message}</Notice>}
    {q.data && (schedules.length ? <div className="rp-card">
      {schedules.map(a => {
        const P = presets[kindOf(a)];
        const run = a.lastRun;
        return <div key={a.id} className="rp-row">
          <P.icon className="gm-row-icon" />
          <span className="rp-row-main"><strong>{a.name}{a.pausedReason && <span className="rp-chip warning" title={a.pausedReason}>Paused</span>}</strong>
            <span>{describe(a.trigger)}{a.nextRunAt && a.enabled ? ` · next ${new Date(a.nextRunAt).toLocaleString()}` : ''}{run ? ` · last ran ${timeAgo(run.createdAt ?? run.startedAt)}: ${run.status}` : ''}</span></span>
          {canRun && <button type="button" className="icon-action" title="Run now" aria-label={`Run ${a.name} now`} onClick={() => runNow(a)}><PlayIcon /></button>}
          {canManage && <Switch label={`${a.enabled ? 'Turn off' : 'Turn on'} ${a.name}`} checked={a.enabled} onChange={v => toggle(a, v)} />}
          {canManage && <button type="button" className="icon-action danger" title="Delete" aria-label={`Delete ${a.name}`} onClick={() => remove(a)}><TrashIcon /></button>}
        </div>;
      })}
    </div> : <Empty icon={ClockIcon} title="Nothing scheduled">Pick one above. A nightly restart and backups every few hours suit most servers.</Empty>)}
    {others > 0 && <p className="gm-muted gm-small">{others} other automation{others === 1 ? '' : 's'} for this server under <button type="button" className="inline-link" onClick={() => onNavigate('Automations')}>Automations</button>.</p>}
    {creating && <CreateDialog resource={resource} kind={creating} onClose={() => setCreating(null)} onCreated={name => { setCreating(null); setResult({ tone: 'success', text: `${name} is scheduled.` }); q.refetch(); }} />}
  </div>;
}

function CreateDialog({ resource, kind, onClose, onCreated }) {
  const [time, setTime] = useState(kind === 'restart' ? '05:00' : '12:00');
  const [every, setEvery] = useState(kind === 'backup' ? '6' : kind === 'announce' ? '1' : '24');
  const [warn, setWarn] = useState('5');
  const [keep, setKeep] = useState('12');
  const [message, setMessage] = useState('');
  const [command, setCommand] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const p = presets[kind];
  const daily = kind === 'restart' || every === '24';

  async function save() {
    setError('');
    const [h, m] = time.split(':').map(Number);
    const schedule = daily ? `${m} ${h} * * *` : every === '1' ? `${m || 0} * * * *` : `${m || 0} */${every} * * *`;
    const step = { restart: { action: 'game_restart', params: { warnMinutes: Number(warn), message: 'The server restarts', verifySeconds: 300 } },
      backup: { action: 'game_backup', params: { keep: Number(keep) } },
      announce: { action: 'game_announce', params: { message: message.trim() } },
      command: { action: 'game_command', params: { command: command.trim(), expect: '' } } }[kind];
    if (kind === 'announce' && !message.trim()) return setError('Write the message.');
    if (kind === 'command' && !command.trim()) return setError('Enter the command.');
    const name = kind === 'command' ? `Run "${command.trim().slice(0, 40)}"` : kind === 'announce' ? `Announce "${message.trim().slice(0, 40)}"` : p.title;
    setBusy(true);
    try {
      await api.createAutomation({ name, description: `${p.text} Created from ${resource.name}'s Schedules.`, scope: 'resource', resourceId: resource.id, targets: {},
        trigger: { type: 'schedule', schedule, timezone: tz }, conditions: { cooldownMinutes: 0, skipInMaintenance: true }, steps: [step], mode: 'auto', enabled: true, escalateAfter: 3, maxRunsPerHour: 6 });
      onCreated(name);
    } catch (e) { setError(e.fields ? Object.values(e.fields).join(' ') : e.message); setBusy(false); }
  }
  return <Dialog open onClose={onClose} title={p.title} description={p.text} className="gm-dialog"
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" disabled={busy} onClick={save}>{busy ? 'Saving…' : 'Schedule'}</Button></>}>
    {kind !== 'restart' && <FormField label="How often" htmlFor="gm-every"><Select id="gm-every" value={every} onChange={setEvery} options={hours.map(n => ({ value: String(n), label: n === 1 ? 'Every hour' : n === 24 ? 'Once a day' : `Every ${n} hours` }))} /></FormField>}
    {daily && <FormField label="At" htmlFor="gm-at" hint={`Your time zone: ${tz}`}><TextInput id="gm-at" type="time" value={time} onChange={e => setTime(e.target.value)} /></FormField>}
    {kind === 'restart' && <FormField label="Warn players" htmlFor="gm-warn"><Select id="gm-warn" value={warn} onChange={setWarn} options={['0', '1', '5', '10', '15'].map(v => ({ value: v, label: v === '0' ? 'No warning' : `${v} minute${v === '1' ? '' : 's'} before` }))} /></FormField>}
    {kind === 'backup' && <FormField label="Keep" htmlFor="gm-keep" hint="Older scheduled backups are removed. Ones you make by hand are never removed."><Select id="gm-keep" value={keep} onChange={setKeep} options={['3', '6', '12', '24', '48', '96'].map(v => ({ value: v, label: `The newest ${v}` }))} /></FormField>}
    {kind === 'announce' && <FormField label="Message" htmlFor="gm-msg"><TextInput id="gm-msg" value={message} maxLength={250} autoFocus onChange={e => setMessage(e.target.value)} placeholder="Join our Discord: discord.gg/…" /></FormField>}
    {kind === 'command' && <FormField label="Command" htmlFor="gm-cmd" hint="Without the slash."><TextInput id="gm-cmd" className="mono" value={command} autoFocus onChange={e => setCommand(e.target.value)} placeholder="save-all" /></FormField>}
    {error && <p className="gm-error">{error}</p>}
  </Dialog>;
}

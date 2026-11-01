import { useState } from 'react';
import { ArchiveBoxIcon, ArrowDownTrayIcon, ArrowUturnLeftIcon, ClockIcon, PlusIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Button, Checkbox, Dialog, Notice, Select, Switch, TextInput } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { useSession, can } from '../../../app/AuthGate';
import { Empty, PageIntro } from './shared';
import './Backups.css';

/*
 * Backups of a resource's volumes, for every kind of resource. Each is a .tar.gz of one volume, kept
 * in the resource's backups volume on this machine. Game servers save their world first; anything
 * else can be paused while it's copied (databases should be: their files change while they run).
 */

const bytes = n => n < 1 << 20 ? `${Math.max(1, Math.round(n / 1024))} KB` : n < 1 << 30 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${(n / (1 << 30)).toFixed(2)} GB`;
const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
const download = url => { const a = Object.assign(document.createElement('a'), { href: url, download: '' }); document.body.append(a); a.click(); a.remove(); };

export function Backups({ resource, onNavigate }) {
  const { user } = useSession();
  const canManage = can(user, 'backups.manage');
  const canDownload = can(user, 'storage.backup');
  const canSchedule = can(user, 'automations.manage');
  const volumes = useQuery(() => api.fileVolumes(resource.id), [resource.id]);
  const vols = (volumes.data?.volumes ?? []).filter(v => v.name !== 'backups');
  const [picked, setPicked] = useState(null);
  const volume = picked ?? vols[0]?.name;
  const q = useQuery(() => (volume ? api.backups(resource.id, volume) : Promise.resolve(null)), [resource.id, volume], { pollMs: 30000 });
  const autos = useQuery(() => api.automations({ resourceId: resource.id }), [resource.id]);
  const isGame = resource.type === 'game' && volume === 'data';
  const [label, setLabel] = useState('');
  const [pause, setPause] = useState(resource.type === 'database');
  const [busy, setBusy] = useState('');
  const [result, setResult] = useState(null);
  const [restoring, setRestoring] = useState(null);
  const [scheduling, setScheduling] = useState(false);
  const scheduled = (autos.data?.automations ?? []).filter(a => a.steps?.some(s => s.action === 'backup' || s.action === 'game_backup'));

  if (volumes.error && !volumes.data) return <div className="rp-page"><Notice tone="danger" title="Couldn't load volumes">{volumes.error.message}</Notice></div>;
  if (!volumes.data) return <div className="rp-page"><p className="bk-muted">Loading…</p></div>;
  if (!vols.length) return <div className="rp-page"><Empty icon={ArchiveBoxIcon} title="Nothing to back up">
    {resource.name} has no volumes, so it keeps no files of its own. Add a volume under Storage and it can be backed up here.
    <><br /><button type="button" className="inline-link" onClick={() => onNavigate('Storage')}>Open Storage</button></>
  </Empty></div>;

  async function create(e) {
    e.preventDefault();
    setBusy('create'); setResult(null);
    try {
      const b = await api.createBackup(resource.id, volume, { label: label.trim(), stop: pause && !isGame });
      setResult({ tone: 'success', text: `Backed up ${volume}: ${bytes(b.size)}.` }); setLabel(''); q.refetch();
    } catch (err) { setResult({ tone: 'danger', text: err.message }); }
    setBusy('');
  }
  async function remove(b) {
    if (!window.confirm(`Delete the backup from ${new Date(b.created).toLocaleString()}? It can't be recovered.`)) return;
    setBusy(b.name);
    try { await api.deleteBackup(resource.id, volume, b.name); q.refetch(); } catch (err) { setResult({ tone: 'danger', text: err.message }); }
    setBusy('');
  }

  const d = q.data;
  return <div className="rp-page bk-page">
    <PageIntro actions={<>
      {vols.length > 1 && <Select label="Volume" value={volume} onChange={setPicked} options={vols.map(v => ({ value: v.name, label: `${v.name} · ${v.mountPath}` }))} />}
      {canSchedule && <Button icon={ClockIcon} onClick={() => setScheduling(s => !s)}>Back up on a schedule</Button>}
    </>}>
      Each backup is everything in the {volume} volume{vols.length > 1 ? '' : ` (${vols[0].mountPath})`}. They're kept on this machine{d ? `, ${bytes(d.totalSize)} in total` : ''}; download the ones you can't lose to keep a copy elsewhere.
    </PageIntro>

    {scheduling && <ScheduleForm resource={resource} onClose={() => setScheduling(false)} onDone={text => { setScheduling(false); setResult({ tone: 'success', text }); autos.refetch(); }} defaultPause={resource.type === 'database'} isGame={resource.type === 'game'} />}
    {scheduled.length > 0 && <p className="bk-muted bk-small"><ClockIcon className="bk-inline" />{scheduled.map(a => `${a.name}${a.enabled ? '' : ' (off)'}`).join(', ')} · manage under <button type="button" className="inline-link" onClick={() => onNavigate(resource.type === 'game' ? 'Schedules' : 'Automations')}>{resource.type === 'game' ? 'Schedules' : 'Automations'}</button></p>}

    {canManage && <form className="rp-card bk-form" onSubmit={create}>
      <TextInput value={label} placeholder="Note (optional), e.g. before the upgrade" maxLength={60} aria-label="Backup note" onChange={e => setLabel(e.target.value)} />
      {!isGame && <label className="bk-toggle" title="Stops it for the copy and starts it again"><Switch label="Pause while copying" checked={pause} onChange={setPause} />Pause while copying</label>}
      <Button type="submit" variant="primary" icon={PlusIcon} disabled={!!busy}>{busy === 'create' ? 'Backing up…' : 'Back up now'}</Button>
    </form>}
    {canManage && !isGame && resource.type === 'database' && !pause && <Notice tone="warning">A database writes to its files while it runs, so a copy taken without pausing it may not restore cleanly. For a live backup, use the database's own dump tools.</Notice>}
    {isGame && <p className="bk-muted bk-small">The server saves the world and pauses saving while it's copied, so it stays online.</p>}
    {result && <Notice tone={result.tone}>{result.text}</Notice>}
    {q.error && !d && <Notice tone="danger" title="Couldn't list backups">{q.error.message}</Notice>}

    {d && (d.backups.length ? <div className="rp-card">
      {d.backups.map(b => <div key={b.name} className="rp-row">
        <ArchiveBoxIcon className="bk-icon" />
        <span className="rp-row-main"><strong>{new Date(b.created).toLocaleString()}{b.auto && <span className="rp-chip">Scheduled</span>}{b.label && b.label !== 'scheduled' && <span className="bk-label">{b.label}</span>}</strong>
          <span>{bytes(b.size)} · {timeAgo(b.created)}</span></span>
        {canDownload && <button type="button" className="icon-action" title="Download" aria-label="Download backup" onClick={() => download(api.backupDownloadUrl(resource.id, volume, b.name))}><ArrowDownTrayIcon /></button>}
        {canManage && <Button icon={ArrowUturnLeftIcon} disabled={!!busy} onClick={() => setRestoring(b)}>Restore</Button>}
        {canManage && <button type="button" className="icon-action danger" disabled={!!busy} title="Delete" aria-label="Delete backup" onClick={() => remove(b)}><TrashIcon /></button>}
      </div>)}
    </div> : <Empty icon={ArchiveBoxIcon} title="No backups yet">Back up before big changes: upgrades, migrations, imports.</Empty>)}

    {restoring && <RestoreDialog resource={resource} volume={volume} backup={restoring} running={d?.running} onClose={() => setRestoring(null)}
      onDone={() => { setRestoring(null); setResult({ tone: 'success', text: `Restored ${volume} to ${new Date(restoring.created).toLocaleString()}.` }); q.refetch(); }} />}
  </div>;
}

function ScheduleForm({ resource, onClose, onDone, defaultPause, isGame }) {
  const [every, setEvery] = useState('24');
  const [time, setTime] = useState('04:00');
  const [keep, setKeep] = useState('7');
  const [pause, setPause] = useState(defaultPause);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  async function save() {
    const [h, m] = time.split(':').map(Number);
    const schedule = every === '24' ? `${m} ${h} * * *` : every === '168' ? `${m} ${h} * * 0` : `${m} */${every} * * *`;
    const name = every === '24' ? 'Daily backup' : every === '168' ? 'Weekly backup' : `Backup every ${every} hours`;
    setBusy(true); setError('');
    try {
      await api.createAutomation({ name, description: 'Created from Backups.', scope: 'resource', resourceId: resource.id, targets: {},
        trigger: { type: 'schedule', schedule, timezone: tz }, conditions: { cooldownMinutes: 0, skipInMaintenance: true },
        steps: [{ action: 'backup', params: { keep: Number(keep), stop: pause && !isGame } }], mode: 'auto', enabled: true, escalateAfter: 3, maxRunsPerHour: 6 });
      onDone(`${name} is scheduled.`);
    } catch (e) { setError(e.fields ? Object.values(e.fields).join(' ') : e.message); setBusy(false); }
  }
  return <div className="rp-card bk-schedule">
    <Select label="How often" value={every} onChange={setEvery} options={[['1', 'Every hour'], ['6', 'Every 6 hours'], ['12', 'Every 12 hours'], ['24', 'Every day'], ['168', 'Every week']].map(([value, label]) => ({ value, label }))} />
    {(every === '24' || every === '168') && <TextInput type="time" value={time} aria-label="At" onChange={e => setTime(e.target.value)} />}
    <Select label="Keep" value={keep} onChange={setKeep} options={['3', '7', '14', '30', '60'].map(v => ({ value: v, label: `Keep ${v}` }))} />
    {!isGame && <label className="bk-toggle"><Switch label="Pause while copying" checked={pause} onChange={setPause} />Pause</label>}
    <span className="bk-spacer" />
    <Button onClick={onClose}>Cancel</Button>
    <Button variant="primary" disabled={busy} onClick={save}>Schedule</Button>
    {error && <p className="bk-error">{error}</p>}
    <p className="bk-muted bk-small bk-full">Backs up every volume. Older scheduled backups are removed; ones made by hand are kept. Times are {tz}.</p>
  </div>;
}

function RestoreDialog({ resource, volume, backup, running, onClose, onDone }) {
  const [first, setFirst] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  async function run() {
    setBusy(true); setError('');
    try { await api.restoreBackup(resource.id, volume, backup.name, first); onDone(); } catch (e) { setError(e.message); setBusy(false); }
  }
  return <Dialog open onClose={onClose} title="Restore this backup?" description={`Everything in ${volume} is replaced with the backup from ${new Date(backup.created).toLocaleString()}.`}
    footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button variant="danger-solid" disabled={busy} onClick={run}>{busy ? 'Restoring…' : 'Restore'}</Button></>}>
    {running && <p className="bk-muted bk-small">{resource.name} stops, the files are replaced, and it starts again.</p>}
    <div className="bk-check" onClick={() => setFirst(f => !f)}><Checkbox checked={first} label="Back up the current state first" onChange={setFirst} />
      <span><strong>Back up the current state first</strong><small>So you can come back to now.</small></span></div>
    {error && <p className="bk-error">{error}</p>}
  </Dialog>;
}

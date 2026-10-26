import { useEffect, useState } from 'react';
import {
  ArrowRightIcon, CalendarDaysIcon, CheckCircleIcon, ClockIcon, EllipsisHorizontalIcon, ExclamationTriangleIcon, NoSymbolIcon, PencilSquareIcon,
  PlayIcon, PlusIcon, TrashIcon, WrenchScrewdriverIcon, XCircleIcon,
} from '@heroicons/react/16/solid';
import { Button, Checkbox, Dialog, FormField, Menu, Notice, Select, Status, Switch, TextInput } from '../../ui';
import { api } from '../../api/client';
import { duration, timeAgo, useQuery } from '../../api/hooks';
import { useSession } from '../../app/AuthGate';
import { Table } from '../project/resource/shared';
import { isAdmin } from './model';
import { ConfirmDialog, JobProgress } from './shared';

/* ---------------------------------- Maintenance --------------------------------- */

export function Maintenance({ server, actions, onChanged, onOpenResource, setFeedback }) {
  const { user } = useSession();
  const admin = isAdmin(user);
  const { data, error, refetch } = useQuery(() => api.serverMaintenance(server.id), [server.id], { pollMs: 5000 });
  const [note, setNote] = useState(server.maintenanceNote);
  useEffect(() => setNote(server.maintenanceNote), [server.maintenanceNote]);
  const act = data?.activity;
  const busy = act && (act.deployments.length > 0 || act.jobs.length > 0 || act.taskRuns > 0);

  async function saveNote() {
    try { await api.putMaintenanceNote(server.id, note); setFeedback({ tone: 'success', title: 'Note saved' }); onChanged(); refetch(); }
    catch (e) { setFeedback({ tone: 'danger', title: 'Couldn\'t save the note', text: e.message }); }
  }

  return <>
    <section className="ov-hero">
      <div className="ov-status">
        <span className="ov-label">Maintenance</span>
        <Status tone={server.maintenance ? 'warning' : 'success'}>{server.maintenance ? 'In maintenance' : 'Not in maintenance'}</Status>
        <dl>
          {server.maintenance && <div><dt>Since</dt><dd>{new Date(server.maintenanceSince).toLocaleString()}</dd></div>}
          {server.maintenance && <div><dt>By</dt><dd>{server.maintenanceBy || 'Unknown'}</dd></div>}
          <div><dt>New deployments</dt><dd>{server.deploymentEligibility.eligible ? 'Accepted' : 'Refused'}</dd></div>
          <div><dt>In flight</dt><dd className={busy ? 'bad' : undefined}>{act ? `${act.deployments.length} deployment(s), ${act.jobs.length} job(s), ${act.taskRuns} task run(s)` : '…'}</dd></div>
        </dl>
        <p className="sv-reason">{server.deploymentEligibility.reason}</p>
      </div>
      <div className="ov-actions">
        <span className="ov-label">Actions</span>
        <div>
          {admin && server.isLocal && <Button icon={server.deploymentsEnabled ? NoSymbolIcon : PlayIcon} onClick={actions.toggleDeployments}>{server.deploymentsEnabled ? 'Disable deployments' : 'Enable deployments'}</Button>}
          {admin && <Button variant={server.maintenance ? undefined : 'primary'} icon={WrenchScrewdriverIcon} onClick={actions.maintenance}>{server.maintenance ? 'Exit maintenance' : 'Enter maintenance'}</Button>}
        </div>
      </div>
    </section>
    {error && <Notice tone="danger" title="Couldn't load maintenance details">{error.message}</Notice>}

    {data && <Notice tone="info" title="What maintenance mode does">{data.policy.text}</Notice>}

    {server.maintenance && admin && <section className="ov-card">
      <header><h2>Maintenance notes</h2></header>
      <div className="sv-note">
        <textarea className="labels-input" rows={3} value={note} maxLength={2000} onChange={e => setNote(e.target.value)} placeholder="What's being done, and when it'll be finished" aria-label="Maintenance note" />
        <Button onClick={saveNote} disabled={note === server.maintenanceNote}>Save note</Button>
      </div>
    </section>}
    {server.maintenance && !admin && server.maintenanceNote && <Notice tone="warning" title="Maintenance note">{server.maintenanceNote}</Notice>}

    <div className="ov-columns">
      <div className="ov-stack">
        <section className="ov-card">
          <header><h2>Before you reboot or change the host</h2></header>
          <ol className="sv-checklist">
            <li className={server.maintenance ? 'done' : ''}>{server.maintenance ? <CheckCircleIcon /> : <span className="sv-num">1</span>}<div><strong>Enter maintenance</strong><span>Stops new deployments and scheduled server tasks from starting.</span></div></li>
            <li className={act && !act.deployments.length && !act.jobs.length ? 'done' : ''}>{act && !act.deployments.length && !act.jobs.length ? <CheckCircleIcon /> : <span className="sv-num">2</span>}
              <div><strong>Let in-flight work finish</strong><span>{act ? (act.deployments.length || act.jobs.length ? `${act.deployments.length} deployment(s) and ${act.jobs.length} server job(s) are still running.` : 'Nothing is running.') : 'Checking…'}</span></div></li>
            <li><span className="sv-num">3</span><div><strong>Review workloads and storage</strong><span>{data ? `${data.workloads} workload(s), ${data.running} running, ${data.volumes} configured volume(s). They stay on this host; IGNITION can't move them elsewhere.` : '…'}</span></div></li>
            <li><span className="sv-num">4</span><div><strong>Reboot or change the host yourself</strong><span>IGNITION never reboots machines. Containers with a restart policy come back when Docker starts.</span></div></li>
            <li><span className="sv-num">5</span><div><strong>Check, then exit maintenance</strong><span>Run diagnostics to confirm Docker and workloads are healthy, then exit maintenance.</span></div></li>
          </ol>
        </section>
        {act?.deployments.length > 0 && <section className="ov-card">
          <header><h2>Deployments in progress</h2></header>
          <ol className="ov-events">{act.deployments.map(d => <li key={d.id}><ClockIcon className="neutral" /><span>{d.resourceName} #{d.number} · {d.status}</span>
            <button type="button" className="ov-link" onClick={() => onOpenResource({ id: d.resourceId, projectId: d.projectId }, 'Deployments')}>Open<ArrowRightIcon /></button></li>)}</ol>
        </section>}
      </div>
      <section className="ov-card">
        <header><h2>History</h2></header>
        {(data?.history ?? []).length ? <ol className="ov-events">{data.history.map(e => <li key={e.id}>
          {e.kind === 'maintenance' ? <WrenchScrewdriverIcon className="neutral" /> : <NoSymbolIcon className="neutral" />}<span>{e.message} · {e.actor}</span><time>{timeAgo(e.at)}</time>
        </li>)}</ol> : <p className="ov-empty">{data ? 'No maintenance or deployment-policy changes yet.' : 'Loading…'}</p>}
      </section>
    </div>
  </>;
}

/* ------------------------------ Tasks & Automation ------------------------------ */

const runTone = { queued: 'neutral', running: 'progress', succeeded: 'success', failed: 'danger', skipped: 'neutral', cancelled: 'neutral' };

export function Tasks({ server, setFeedback }) {
  const { user } = useSession();
  const admin = isAdmin(user);
  const { data, error, refetch } = useQuery(() => api.serverTasks(server.id), [server.id], { pollMs: 5000 });
  const [editing, setEditing] = useState(null);
  const [deleting, setDeleting] = useState(null);
  const [history, setHistory] = useState(null);
  const kinds = data?.kinds ?? [];
  const kindTitle = k => kinds.find(x => x.id === k)?.title ?? k;

  async function run(t) {
    try { await api.runServerTask(t.id); setFeedback({ tone: 'success', title: `${t.name} queued`, text: 'Its run appears in the history.' }); refetch(); setHistory(t); }
    catch (e) { setFeedback({ tone: 'danger', title: `Couldn't run ${t.name}`, text: e.message }); }
  }
  async function toggle(t) {
    try { await api.updateServerTask(t.id, { enabled: !t.enabled }); refetch(); }
    catch (e) { setFeedback({ tone: 'danger', title: 'Couldn\'t change the task', text: e.message }); }
  }

  return <>
    <div className="rp-intro">
      <p>Scheduled operations for this server, run by IGNITION's durable job queue. Only built-in task types exist: none runs commands on the host. Tasks other than diagnostics and checks are skipped while the server is in maintenance.</p>
      {admin && <div className="rp-intro-actions"><Button variant="primary" icon={PlusIcon} onClick={() => setEditing({})}>New task</Button></div>}
    </div>
    {error && <Notice tone="danger" title="Couldn't load tasks">{error.message}</Notice>}
    {data && !data.tasks.length ? <div className="rp-empty"><CalendarDaysIcon /><strong>No scheduled tasks</strong><p>Schedule diagnostics, disk checks, safe cleanups or reconciliation to keep this server healthy without remembering to.</p>
      {admin && <Button icon={PlusIcon} onClick={() => setEditing({})}>New task</Button>}</div>
      : <Table className="sv-table-wrap" columns={[{ label: 'Task' }, { label: 'Schedule' }, { label: 'Next run' }, { label: 'Last run' }, { label: 'Policy' }, { label: 'Enabled' }, { label: '', className: 'actions' }]}>
        {(data?.tasks ?? []).map(t => <tr key={t.id}>
          <td><span className="resource-name">{t.name}<span>{kindTitle(t.kind)}</span></span></td>
          <td><span className="mono">{t.schedule}</span> <span className="muted">{t.timezone}</span></td>
          <td className="muted">{t.enabled && t.nextRunAt ? new Date(t.nextRunAt).toLocaleString() : 'Paused'}</td>
          <td>{t.lastRun ? <button type="button" className="sv-link" onClick={() => setHistory(t)}><Status tone={runTone[t.lastRun.status]}>{t.lastRun.status}</Status><span>{timeAgo(t.lastRun.createdAt)}</span></button> : <span className="muted">Never</span>}</td>
          <td className="muted">{t.timeoutSeconds}s timeout · {t.maxAttempts === 1 ? 'no retry' : `${t.maxAttempts} attempts`} · {t.concurrency === 'skip' ? 'skip overlaps' : 'queue overlaps'}</td>
          <td><Switch checked={t.enabled} disabled={!admin} onChange={() => toggle(t)} label={`Enable ${t.name}`} /></td>
          <td className="actions"><Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${t.name}`} items={[
            { label: 'History', icon: ClockIcon, onSelect: () => setHistory(t) },
            ...(admin ? [{ label: 'Run now', icon: PlayIcon, onSelect: () => run(t) }, { label: 'Edit', icon: PencilSquareIcon, onSelect: () => setEditing(t) }, 'separator',
              { label: 'Delete', icon: TrashIcon, danger: true, onSelect: () => setDeleting(t) }] : []),
          ]} /></td>
        </tr>)}
      </Table>}
    {editing && <TaskDialog server={server} task={editing.id ? editing : null} kinds={kinds} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); refetch(); setFeedback({ tone: 'success', title: 'Task saved' }); }} />}
    {deleting && <ConfirmDialog danger title={`Delete ${deleting.name}?`} description="Its schedule and run history are deleted. Runs in progress finish." confirmLabel="Delete task"
      onClose={() => setDeleting(null)} onConfirm={async () => { await api.deleteServerTask(deleting.id); setDeleting(null); refetch(); setFeedback({ tone: 'success', title: 'Task deleted' }); }} />}
    {history && <TaskHistory task={history} onClose={() => setHistory(null)} />}
  </>;
}

// supportedValuesOf leaves out "UTC" itself, which is the default here and in most containers.
const timezones = (() => { try { return [...new Set(['UTC', ...Intl.supportedValuesOf('timeZone')])]; } catch { return ['UTC']; } })();

function TaskDialog({ server, task, kinds, onClose, onSaved }) {
  const [form, setForm] = useState(() => task ? { ...task, options: task.options ?? {} }
    : { kind: 'diagnostics', name: '', description: '', schedule: '0 * * * *', timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC', timeoutSeconds: 300, maxAttempts: 1, concurrency: 'skip', enabled: true, options: {} });
  const [errors, setErrors] = useState({});
  const [pending, setPending] = useState(false);
  const preview = useQuery(() => api.cronPreview(form.schedule, form.timezone), [form.schedule, form.timezone], { enabled: !!form.schedule });
  const set = (k, v) => setForm(f => ({ ...f, [k]: v }));
  const kind = kinds.find(k => k.id === form.kind);
  async function save(e) {
    e.preventDefault();
    setPending(true);
    const body = { name: form.name, description: form.description, schedule: form.schedule, timezone: form.timezone, timeoutSeconds: Number(form.timeoutSeconds),
      maxAttempts: Number(form.maxAttempts), concurrency: form.concurrency, enabled: form.enabled, options: form.options };
    try {
      if (task) await api.updateServerTask(task.id, body); else await api.createServerTask(server.id, { ...body, kind: form.kind });
      onSaved();
    } catch (err) { setErrors(err.fields && Object.keys(err.fields).length ? err.fields : { _: err.message }); setPending(false); }
  }
  const opt = (k, v) => set('options', { ...form.options, [k]: v });
  return <Dialog open onClose={onClose} title={task ? `Edit ${task.name}` : 'New scheduled task'} footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" type="submit" form="task-form" disabled={pending}>{pending ? 'Saving…' : 'Save'}</Button></>}>
    <form id="task-form" className="sv-form" onSubmit={save}>
      {!task && <FormField label="Type" htmlFor="tkind" error={errors.kind}><Select id="tkind" label="Type" value={form.kind} onChange={v => { set('kind', v); set('options', {}); }} options={kinds.map(k => ({ value: k.id, label: k.title, description: k.description }))} /></FormField>}
      {kind && <p className="sv-step-intro">{kind.description}</p>}
      <FormField label="Name" htmlFor="tname" error={errors.name}><TextInput id="tname" value={form.name} onChange={e => set('name', e.target.value)} maxLength={80} placeholder={kind?.title} /></FormField>
      <FormField label="Description" htmlFor="tdesc" error={errors.description}><TextInput id="tdesc" value={form.description} onChange={e => set('description', e.target.value)} maxLength={500} /></FormField>
      <div className="sv-form-row">
        <FormField label="Schedule (cron)" htmlFor="tsched" error={errors.schedule} hint={preview.data?.valid ? preview.data.description : preview.data?.error}>
          <TextInput id="tsched" className="mono" value={form.schedule} onChange={e => set('schedule', e.target.value)} />
        </FormField>
        <FormField label="Timezone" htmlFor="ttz" error={errors.timezone}><Select id="ttz" label="Timezone" value={form.timezone} onChange={v => set('timezone', v)} searchable options={[...new Set([form.timezone, ...timezones])].map(z => ({ value: z, label: z }))} /></FormField>
      </div>
      {preview.data?.valid && preview.data.next?.length > 0 && <p className="muted sv-next">Next: {preview.data.next.slice(0, 3).map(n => new Date(n).toLocaleString()).join(' · ')}</p>}
      <div className="sv-form-row">
        <FormField label="Timeout (seconds)" htmlFor="tto" error={errors.timeoutSeconds}><TextInput id="tto" type="number" min={10} max={3600} value={form.timeoutSeconds} onChange={e => set('timeoutSeconds', e.target.value)} /></FormField>
        <FormField label="Attempts" htmlFor="tatt" error={errors.maxAttempts} hint="Retries with backoff"><TextInput id="tatt" type="number" min={1} max={5} value={form.maxAttempts} onChange={e => set('maxAttempts', e.target.value)} /></FormField>
        <FormField label="If still running" htmlFor="tconc" error={errors.concurrency}><Select id="tconc" label="Concurrency" value={form.concurrency} onChange={v => set('concurrency', v)} options={[{ value: 'skip', label: 'Skip the new run' }, { value: 'queue', label: 'Queue it' }]} /></FormField>
      </div>
      {form.kind === 'docker_cleanup' && <ul className="sv-options">
        {[['containers', 'Stopped IGNITION containers'], ['dangling', 'Untagged images'], ['buildCache', 'Unused build cache'], ['networks', 'Unused networks']].map(([k, l]) => <li key={k}>
          <Checkbox label={l} checked={form.options[k] ?? (k !== 'networks')} onChange={v => opt(k, v)} /><div><strong>{l}</strong></div>
        </li>)}
        <li className="sv-locked"><div><strong>Never:</strong><span>volumes or tagged images. Remove those by hand, with a preview, from the Docker page.</span></div></li>
      </ul>}
      {form.kind === 'maintenance_check' && <FormField label="Fail if maintenance lasts longer than (hours)" htmlFor="tmax" error={errors.options}>
        <TextInput id="tmax" type="number" min={1} max={720} value={form.options.maxHours ?? 24} onChange={e => opt('maxHours', Number(e.target.value))} />
      </FormField>}
      {errors._ && <Notice tone="danger" title="Couldn't save">{errors._}</Notice>}
    </form>
  </Dialog>;
}

function TaskHistory({ task, onClose }) {
  const { data, error } = useQuery(() => api.serverTaskRuns(task.id), [task.id], { pollMs: 3000 });
  const [open, setOpen] = useState(null);
  return <Dialog open onClose={onClose} title={`${task.name} · history`} className="sv-wide-dialog" footer={<Button onClick={onClose}>Close</Button>}>
    {error && <Notice tone="danger" title="Couldn't load runs">{error.message}</Notice>}
    {data && !data.runs.length && <p className="muted">No runs yet.</p>}
    <ul className="sv-runs">{(data?.runs ?? []).map(r => <li key={r.id}>
      <button type="button" className="sv-run-head" onClick={() => setOpen(open === r.id ? null : r.id)} aria-expanded={open === r.id} disabled={!r.jobId}>
        {r.status === 'succeeded' ? <CheckCircleIcon className="success" /> : r.status === 'failed' ? <XCircleIcon className="danger" /> : r.status === 'skipped' ? <ExclamationTriangleIcon className="muted" /> : <ClockIcon className="muted" />}
        <Status tone={runTone[r.status]}>{r.status}</Status>
        <span>{r.trigger === 'manual' ? `Run by ${r.createdBy}` : 'Scheduled'} · {new Date(r.createdAt).toLocaleString()}{r.finishedAt ? ` · ${duration(r.createdAt, r.finishedAt)}` : ''}{r.attempts > 1 ? ` · ${r.attempts} attempts` : ''}</span>
      </button>
      {(r.summary || r.error) && <p className={r.status === 'failed' ? 'rp-error' : 'muted'}>{r.summary || r.error}</p>}
      {open === r.id && r.jobId && <JobProgress jobId={r.jobId} title="Job" />}
    </li>)}</ul>
  </Dialog>;
}

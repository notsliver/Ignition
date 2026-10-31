import { useEffect, useState } from 'react';
import {
  ArrowDownTrayIcon, ArrowPathIcon, ArrowRightIcon, BeakerIcon, CheckCircleIcon, ExclamationTriangleIcon, InformationCircleIcon, KeyIcon, MapPinIcon,
  NoSymbolIcon, PencilSquareIcon, PlayIcon, SignalIcon, TrashIcon, WrenchScrewdriverIcon, XCircleIcon,
} from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, Notice, Status, TextInput, cx } from '../../ui';
import { api } from '../../api/client';
import { timeAgo, useQuery } from '../../api/hooks';
import { useSession, can } from '../../app/AuthGate';
import { connection, download, isAdmin, isDeveloper, labelsToText, runtimeOf, textToLabels, warningTarget } from './model';
import '../settings/settings.css';
import '../project/resource/resource.css';
import '../project/ResourceDetail/ResourceDetail.css';
import './servers.css';

/* ---------------------------------- Feedback ---------------------------------- */

/** Inline result banner. Only shown once the backend has answered: never optimistic. */
export function useFeedback() {
  const [feedback, setFeedback] = useState(null);
  const node = feedback && <Notice tone={feedback.tone} title={feedback.title} onDismiss={() => setFeedback(null)}>{feedback.text}</Notice>;
  return [node, setFeedback];
}

/* ------------------------------------ Jobs ------------------------------------ */

const terminal = ['succeeded', 'failed', 'cancelled'];

/** Polls a server job until it finishes. */
export function useJob(jobId, { onDone } = {}) {
  const [stopped, setStopped] = useState(false);
  const q = useQuery(() => api.serverJob(jobId), [jobId], { enabled: !!jobId, pollMs: stopped ? 0 : 1500 });
  const job = q.data?.job;
  const done = !!job && terminal.includes(job.status);
  useEffect(() => { if (done && !stopped) { setStopped(true); onDone?.(job); } }, [done]); // eslint-disable-line react-hooks/exhaustive-deps
  return { job, logs: q.data?.logs ?? [], done, error: q.error };
}

const jobTone = { pending: 'neutral', running: 'progress', succeeded: 'success', failed: 'danger', cancelled: 'neutral' };
const jobLabel = { pending: 'Queued', running: 'Running', succeeded: 'Succeeded', failed: 'Failed', cancelled: 'Cancelled' };

export function JobStatus({ job }) {
  if (!job) return <Status tone="neutral">Queued</Status>;
  return <Status tone={jobTone[job.status]}>{jobLabel[job.status]}{job.status === 'running' && job.progress ? ` · ${job.progress}` : ''}</Status>;
}

/** A job row with live status, cancellation for admins, and its logs on demand. */
export function JobProgress({ jobId, title, onDone, renderResult }) {
  const { user } = useSession();
  const { job, logs, done } = useJob(jobId, { onDone });
  const [showLogs, setShowLogs] = useState(false);
  const [cancelError, setCancelError] = useState('');
  return <div className="sv-job">
    <div className="sv-job-head">
      <strong>{title}</strong>
      <JobStatus job={job} />
      <span className="spacer" />
      {logs.length > 0 && <button type="button" className="ov-link" onClick={() => setShowLogs(v => !v)}>{showLogs ? 'Hide log' : `Log (${logs.length})`}</button>}
      {!done && can(user, 'servers.tasks') && <button type="button" className="ov-link" onClick={() => api.cancelServerJob(jobId).catch(e => setCancelError(e.message))}>Cancel</button>}
    </div>
    {job?.status === 'failed' && job.error && <p className="sv-job-error"><XCircleIcon />{job.error}</p>}
    {cancelError && <p className="sv-job-error"><XCircleIcon />{cancelError}</p>}
    {done && renderResult?.(job)}
    {showLogs && <pre className="sv-pre">{logs.map(l => `${new Date(l.createdAt).toLocaleTimeString()}  ${l.level.padEnd(5)} ${l.message}`).join('\n')}</pre>}
  </div>;
}

/* ---------------------------------- Status UI --------------------------------- */

export function ConnectionStatus({ server, withText }) {
  const c = connection(server);
  return <span title={c.text}><Status tone={c.tone}>{c.label}{withText && server.stale ? ' (stale)' : ''}</Status></span>;
}

export function RuntimeStatus({ server }) {
  const r = runtimeOf(server);
  return <span title={r.title}><Status tone={r.tone}>{r.label}</Status></span>;
}

export function Chips({ server }) {
  const labels = Object.entries(server.labels ?? {});
  return <span className="sv-chips">
    {server.environment && <span className="rp-chip accent">{server.environment}</span>}
    {server.location && <span className="rp-chip" title="Location"><MapPinIcon />{server.location}</span>}
    {labels.slice(0, 3).map(([k, v]) => <span key={k} className="rp-chip">{v ? `${k}=${v}` : k}</span>)}
    {labels.length > 3 && <span className="rp-chip" title={labels.slice(3).map(([k, v]) => `${k}=${v}`).join(', ')}>+{labels.length - 3}</span>}
  </span>;
}

/** Warnings for one server, each with a link to where it can be fixed. */
export function ServerWarnings({ server, onNavigate, skip = [] }) {
  const list = (server.warnings ?? []).filter(w => !skip.includes(w.code));
  if (!list.length) return null;
  return <ul className="ov-warnings">
    {list.map(w => <li key={w.code + w.text} className={w.level === 'danger' ? 'danger' : w.level === 'info' ? 'info' : ''}>
      {w.level === 'info' ? <InformationCircleIcon /> : <ExclamationTriangleIcon />}
      <span><strong>{w.title}.</strong> {w.text}</span>
      {onNavigate && warningTarget[w.code] && <button type="button" onClick={() => onNavigate(warningTarget[w.code])}>{warningTarget[w.code]}<ArrowRightIcon /></button>}
    </li>)}
  </ul>;
}

/** "Last contact 2 min ago", or why there's none. */
export function LastContact({ server }) {
  if (!server.lastSeenAt) return <span className="muted">Never</span>;
  return <span title={new Date(server.lastSeenAt).toLocaleString()}>{timeAgo(server.lastSeenAt)}</span>;
}

/* ------------------------------------ Actions ---------------------------------- */

/**
 * The actions available for a server, in one place so the inventory row menu, the workspace header
 * and the overview offer the same things with the same confirmation flows.
 * Returns menu items plus the dialogs those items open (render `dialogs` somewhere on the page).
 */
export function useServerActions(server, { onChanged, onNavigate, onOpen, setFeedback, onRemoved }) {
  const { user } = useSession();
  const [dialog, setDialog] = useState(null);
  const [busy, setBusy] = useState('');
  const admin = isAdmin(user), dev = isDeveloper(user);

  async function run(label, fn, success) {
    setBusy(label);
    try {
      const out = await fn();
      setFeedback?.(typeof success === 'function' ? success(out) : success);
      onChanged?.(out?.server);
    } catch (e) {
      setFeedback?.({ tone: 'danger', title: `${label} failed`, text: e.message });
    } finally {
      setBusy('');
    }
  }

  const test = () => run('Test connection', () => api.testServer(server.id), out => out.result.reached && out.result.runtime === 'ready'
    ? { tone: 'success', title: 'Connection works', text: `Reached ${server.name} in ${out.result.latencyMs} ms. Docker ${out.result.docker ?? ''} is ready.` }
    : out.result.reached
      ? { tone: 'warning', title: 'Reached, but Docker isn\'t ready', text: out.result.error }
      : { tone: 'danger', title: 'Couldn\'t reach the server', text: out.result.error });
  const refresh = () => setDialog({ kind: 'job', title: `Refresh runtime state of ${server.name}`, start: () => api.refreshServer(server.id) });
  const toggleDeployments = () => setDialog({ kind: 'deployments' });
  const maintenance = () => setDialog({ kind: 'maintenance' });

  const items = [
    ...(onOpen ? [{ label: 'Open server', icon: ArrowRightIcon, onSelect: () => onOpen(server) }, 'separator'] : []),
    ...(dev ? [
      { label: busy === 'Test connection' ? 'Testing…' : 'Test connection', icon: SignalIcon, onSelect: test },
      { label: 'Refresh runtime state', icon: ArrowPathIcon, onSelect: refresh },
    ] : []),
    ...(onNavigate ? [{ label: 'Open diagnostics', icon: BeakerIcon, onSelect: () => onNavigate('Diagnostics') }] : onOpen ? [{ label: 'Open diagnostics', icon: BeakerIcon, onSelect: () => onOpen(server, 'Diagnostics') }] : []),
    ...(admin ? [
      'separator',
      { label: 'Edit details', icon: PencilSquareIcon, onSelect: () => setDialog({ kind: 'edit' }) },
      ...(server.isLocal ? [{ label: server.deploymentsEnabled ? 'Disable new deployments' : 'Enable new deployments', icon: server.deploymentsEnabled ? NoSymbolIcon : PlayIcon, onSelect: toggleDeployments }] : []),
      { label: server.maintenance ? 'Exit maintenance' : 'Enter maintenance', icon: WrenchScrewdriverIcon, onSelect: maintenance },
      ...(!server.isLocal ? [{ label: server.hasCredentials ? 'Rotate credentials' : 'Reconnect with new credentials', icon: KeyIcon, onSelect: () => (onNavigate ? onNavigate('Access & Security') : onOpen?.(server, 'Access & Security')) }] : []),
    ] : []),
    ...(can(user, 'servers.inspect') ? [{ label: 'Export diagnostics', icon: ArrowDownTrayIcon, onSelect: () => download(api.serverBundleUrl(server.id)) }] : []),
    ...(!server.isLocal && can(user, 'servers.delete') ? ['separator', { label: 'Remove server', icon: TrashIcon, danger: true, onSelect: () => setDialog({ kind: 'remove' }) }] : []),
  ];

  const close = () => setDialog(null);
  const dialogs = dialog && <>
    {dialog.kind === 'edit' && <EditServerDialog server={server} onClose={close} onSaved={s => { close(); onChanged?.(s); setFeedback?.({ tone: 'success', title: 'Details saved' }); }} />}
    {dialog.kind === 'deployments' && <ConfirmDialog
      title={server.deploymentsEnabled ? `Disable new deployments on ${server.name}?` : `Enable new deployments on ${server.name}?`}
      description={server.deploymentsEnabled
        ? 'Deploys, redeploys, rollbacks, deploy hooks and token deploys of resources on this server will be refused. Running workloads keep running; you can enable deployments again at any time.'
        : 'Resources on this server can be deployed again.'}
      confirmLabel={server.deploymentsEnabled ? 'Disable deployments' : 'Enable deployments'}
      onClose={close}
      onConfirm={async () => {
        const out = await api.setServerDeployments(server.id, !server.deploymentsEnabled);
        close(); onChanged?.(out.server);
        setFeedback?.({ tone: 'success', title: out.server.deploymentsEnabled ? 'New deployments enabled' : 'New deployments disabled', text: out.server.deploymentEligibility.reason });
      }} />}
    {dialog.kind === 'maintenance' && <MaintenanceDialog server={server} onClose={close} onDone={out => { close(); onChanged?.(out.server); setFeedback?.({ tone: 'success', title: out.server.maintenance ? 'Maintenance mode on' : 'Maintenance mode off', text: out.server.maintenance ? 'New deployments and scheduled server tasks are paused. Workloads keep running.' : 'New deployments are accepted again.' }); }} />}
    {dialog.kind === 'remove' && <RemoveServerDialog server={server} onClose={close} onRemoved={() => { close(); onRemoved?.(); }} />}
    {dialog.kind === 'job' && <JobDialog title={dialog.title} start={dialog.start} onClose={() => { close(); onChanged?.(); }} />}
  </>;
  return { items, dialogs, busy, test, refresh, toggleDeployments, maintenance, admin, dev };
}

/* ------------------------------------ Dialogs ---------------------------------- */

/** Generic confirmation. Errors stay in the dialog; it only closes on success. */
export function ConfirmDialog({ title, description, children, confirmLabel = 'Confirm', danger, phrase, onConfirm, onClose, icon }) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [typed, setTyped] = useState('');
  async function go() {
    setPending(true);
    setError('');
    try { await onConfirm(); } catch (e) { setError(e.message); setPending(false); }
  }
  return <Dialog open onClose={onClose} title={title} description={description} icon={icon}
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant={danger ? undefined : 'primary'} className={danger ? 'danger-solid' : undefined} disabled={pending || (phrase && typed !== phrase)} onClick={go}>{pending ? 'Working…' : confirmLabel}</Button></>}>
    {children}
    {phrase && <><p className="confirm-hint">Type <code>{phrase}</code> to confirm.</p><TextInput value={typed} onChange={e => setTyped(e.target.value)} aria-label={`Type ${phrase} to confirm`} autoComplete="off" autoFocus /></>}
    {error && <Notice tone="danger" title="That didn't work">{error}</Notice>}
  </Dialog>;
}

/** Starts a job and shows its progress until it finishes. */
export function JobDialog({ title, start, onClose, renderResult }) {
  const [jobId, setJobId] = useState(null);
  const [error, setError] = useState('');
  useEffect(() => { start().then(r => setJobId(r.jobId)).catch(e => setError(e.message)); }, []); // eslint-disable-line react-hooks/exhaustive-deps
  return <Dialog open onClose={onClose} title={title} footer={<Button onClick={onClose}>Close</Button>}>
    {error ? <Notice tone="danger" title="Couldn't start">{error}</Notice>
      : jobId ? <JobProgress jobId={jobId} title="Progress" renderResult={renderResult ?? (job => <JobResult job={job} />)} /> : <p className="muted">Starting…</p>}
  </Dialog>;
}

export function JobResult({ job }) {
  const r = job.result ?? {};
  if (job.status === 'succeeded') return <p className="sv-job-ok"><CheckCircleIcon />{r.summary ?? (r.reconciled != null ? `Reached the server; ${r.reconciled} workload status(es) reconciled.` : 'Done.')}</p>;
  return null;
}

function EditServerDialog({ server, onClose, onSaved }) {
  const [form, setForm] = useState({ name: server.name, hostname: server.hostname, description: server.description, environment: server.environment, location: server.location ?? '', labels: labelsToText(server.labels) });
  const [errors, setErrors] = useState({});
  const [pending, setPending] = useState(false);
  const set = k => e => setForm(f => ({ ...f, [k]: e.target.value }));
  async function save(event) {
    event.preventDefault();
    setPending(true);
    try {
      const out = await api.updateServer(server.id, { ...form, labels: textToLabels(form.labels) });
      onSaved(out.server);
    } catch (e) {
      setErrors(e.fields && Object.keys(e.fields).length ? e.fields : { _: e.message });
      setPending(false);
    }
  }
  return <Dialog open onClose={onClose} title="Edit server details" footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" type="submit" form="edit-server" disabled={pending}>{pending ? 'Saving…' : 'Save'}</Button></>}>
    <form id="edit-server" className="sv-form" onSubmit={save}>
      <IdentityFields form={form} set={set} errors={errors} />
      {errors._ && <Notice tone="danger" title="Couldn't save">{errors._}</Notice>}
    </form>
  </Dialog>;
}

export function IdentityFields({ form, set, errors, hostnameHint }) {
  return <>
    <FormField label="Display name" htmlFor="sv-name" error={errors.name}><TextInput id="sv-name" value={form.name} onChange={set('name')} maxLength={64} autoFocus /></FormField>
    <FormField label="Hostname" htmlFor="sv-host" error={errors.hostname} hint={hostnameHint ?? 'How people know this machine. Shown in lists and search.'}><TextInput id="sv-host" value={form.hostname} onChange={set('hostname')} maxLength={253} /></FormField>
    <div className="sv-form-row">
      <FormField label="Environment" htmlFor="sv-env" error={errors.environment} hint="e.g. production, staging"><TextInput id="sv-env" value={form.environment} onChange={set('environment')} maxLength={40} /></FormField>
      <FormField label="Location" htmlFor="sv-loc" error={errors.location} hint="Region, datacenter or rack. Used for grouping."><TextInput id="sv-loc" value={form.location ?? ''} onChange={set('location')} maxLength={64} placeholder="eu-west · fra1" /></FormField>
    </div>
    <FormField label="Description" htmlFor="sv-desc" error={errors.description}><TextInput id="sv-desc" value={form.description} onChange={set('description')} maxLength={500} /></FormField>
    <FormField label="Labels" htmlFor="sv-labels" error={errors.labels} hint="One per line: key=value. Searchable.">
      <textarea id="sv-labels" className="labels-input" rows={3} value={form.labels} onChange={set('labels')} placeholder={'region=eu-west\nrack=a3'} />
    </FormField>
  </>;
}

function MaintenanceDialog({ server, onClose, onDone }) {
  const entering = !server.maintenance;
  const [note, setNote] = useState('');
  const impact = useQuery(() => api.serverMaintenance(server.id), [server.id], { enabled: entering });
  const a = impact.data;
  return <ConfirmDialog icon={WrenchScrewdriverIcon}
    title={entering ? `Put ${server.name} into maintenance?` : `Take ${server.name} out of maintenance?`}
    description={entering ? 'New deployments and scheduled server tasks pause. Running workloads keep running: Ignition doesn\'t stop, drain or move them, and it won\'t reboot the host.' : 'New deployments and scheduled tasks resume.'}
    confirmLabel={entering ? 'Enter maintenance' : 'Exit maintenance'} onClose={onClose}
    onConfirm={async () => onDone(await api.setServerMaintenance(server.id, entering, note))}>
    {entering && <>
      {a ? <ul className="sv-impact">
        <li><strong>{a.workloads}</strong> workload(s) assigned, {a.running} running — they keep running.</li>
        <li><strong>{a.activity.deployments.length}</strong> deployment(s) in progress — they'll finish; new ones are refused.</li>
        <li><strong>{a.activity.jobs.length}</strong> server job(s) and <strong>{a.activity.taskRuns}</strong> task run(s) active.</li>
        <li><strong>{a.tasks}</strong> scheduled server task(s) — skipped while in maintenance, except diagnostics and checks.</li>
      </ul> : impact.error ? <Notice tone="warning" title="Couldn't load the impact">{impact.error.message}</Notice> : <p className="muted">Checking what's running…</p>}
      <FormField label="Note" htmlFor="mnote" hint="Why, and what's planned. Shown to everyone and kept in the history."><TextInput id="mnote" value={note} onChange={e => setNote(e.target.value)} maxLength={2000} placeholder="Kernel update and reboot, back by 14:00" /></FormField>
    </>}
  </ConfirmDialog>;
}

export function RemoveServerDialog({ server, onClose, onRemoved }) {
  const p = useQuery(() => api.serverRemoval(server.id), [server.id]);
  const d = p.data;
  const blocked = d?.blockers.length > 0;
  return <ConfirmDialog icon={TrashIcon} danger title={`Remove ${server.name} from Ignition?`}
    description="This removes the registration only. It's not a cleanup of the machine."
    confirmLabel="Remove server" phrase={d && !blocked ? server.name : undefined} onClose={onClose}
    onConfirm={async () => { if (blocked) throw new Error('Resolve the blockers first.'); await api.deleteServer(server.id, server.name); onRemoved(); }}>
    {p.error && <Notice tone="danger" title="Couldn't check what removal affects">{p.error.message}</Notice>}
    {!d && !p.error && <p className="muted">Checking resources, jobs, volumes and tasks…</p>}
    {d && <>
      {blocked && <Notice tone="danger" title="Removal is blocked">{d.blockers.join(' ')}</Notice>}
      <div className="sv-removal">
        <h3>What happens</h3>
        <ul>{d.effects.map(e => <li key={e}>{e}</li>)}</ul>
        <h3>What doesn't happen</h3>
        <ul>{d.notDone.map(e => <li key={e}>{e}</li>)}</ul>
        <p className="muted">{d.resources.length} resource(s) · {d.volumes} configured volume(s) · {d.tasks} scheduled task(s) · {d.reachable ? 'reachable now' : 'not reachable now'}</p>
      </div>
    </>}
  </ConfirmDialog>;
}

/* ------------------------------------- Misc ------------------------------------- */

export function Unavailable({ server, error, what, onNavigate }) {
  const offline = ['offline', 'unknown', 'pending'].includes(server.connectionState);
  return <div className="rp-empty">
    <ExclamationTriangleIcon />
    <strong>{offline ? `${what} unavailable while the server is ${server.connectionState === 'pending' ? 'not enrolled' : server.connectionState}` : `Couldn't load ${what.toLowerCase()}`}</strong>
    <p>{error?.message ?? 'Ignition needs a working connection to the Docker Engine to show this.'}</p>
    {onNavigate && <Button icon={BeakerIcon} onClick={() => onNavigate('Diagnostics')}>Open diagnostics</Button>}
  </div>;
}

export function Fresh({ at, label = 'Updated' }) {
  const [, tick] = useState(0);
  useEffect(() => { const t = setInterval(() => tick(n => n + 1), 10000); return () => clearInterval(t); }, []);
  if (!at) return null;
  return <span className="rp-row-meta" title={new Date(at).toLocaleString()}>{label} {timeAgo(at).toLowerCase()}</span>;
}

export const Muted = ({ children, className }) => <span className={cx('muted', className)}>{children}</span>;

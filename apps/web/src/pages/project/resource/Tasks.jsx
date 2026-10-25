import { Fragment, useEffect, useMemo, useRef, useState } from 'react';
import { CalendarDaysIcon, ChevronDownIcon, ChevronRightIcon, PencilSquareIcon, PlayIcon, PlusIcon, StopIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, Notice, Select, Status, Switch, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { useSession } from '../../../app/AuthGate';
import { Empty, PageIntro, Table } from './shared';
import './Operations.css';

/*
 * Scheduled tasks run a command inside the resource's running container on a cron schedule, in a
 * timezone of your choice. Ignition's worker runs them: a run that would overlap the previous one is
 * skipped, output is kept (last 64 KB), and a timeout or Cancel stops the command.
 */

const PRESETS = [
  ['*/5 * * * *', 'Every 5 minutes'], ['*/15 * * * *', 'Every 15 minutes'], ['0 * * * *', 'Every hour'], ['0 */6 * * *', 'Every 6 hours'],
  ['0 3 * * *', 'Every day at 03:00'], ['0 9 * * 1-5', 'Weekdays at 09:00'], ['0 3 * * 1', 'Every Monday at 03:00'], ['0 4 1 * *', 'Monthly on the 1st at 04:00'],
];
const TIMEOUTS = [[60, '1 minute'], [300, '5 minutes'], [600, '10 minutes'], [1800, '30 minutes'], [3600, '1 hour'], [21600, '6 hours'], [86400, '24 hours']];
const RUN = { succeeded: ['success', 'Succeeded'], failed: ['danger', 'Failed'], running: ['progress', 'Running'], queued: ['neutral', 'Queued'], skipped: ['warning', 'Skipped'], cancelled: ['neutral', 'Cancelled'] };
const browserZone = (() => { try { return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'; } catch { return 'UTC'; } })();
const zones = (() => { try { return Intl.supportedValuesOf('timeZone'); } catch { return []; } })();

function inZone(iso, tz, withDay = true) {
  try {
    return new Date(iso).toLocaleString(undefined, { timeZone: tz, weekday: withDay ? 'short' : undefined, day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
  } catch {
    return new Date(iso).toLocaleString();
  }
}
function until(iso) {
  const s = Math.round((new Date(iso) - Date.now()) / 1000);
  if (s < 60) return 'in under a minute';
  if (s < 3600) return `in ${Math.round(s / 60)} min`;
  if (s < 86400) return `in ${Math.round(s / 3600)} h`;
  return `in ${Math.round(s / 86400)} d`;
}
function took(run) {
  if (!run.startedAt || !run.finishedAt) return '';
  const s = Math.max(0, Math.round((new Date(run.finishedAt) - new Date(run.startedAt)) / 1000));
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
}

const emptyDraft = { name: '', schedule: '0 3 * * *', custom: '', timezone: browserZone, command: '', timeoutSeconds: 600 };

export function ScheduledTasks({ resource }) {
  const { user } = useSession();
  const canEdit = user.role !== 'viewer';
  const [hasRunning, setHasRunning] = useState(false);
  const { data, error, refetch, setData } = useQuery(() => api.tasks(resource.id), [resource.id], { pollMs: hasRunning ? 2000 : 10000 });
  const [editing, setEditing] = useState(null); // 'new' or a task
  const [open, setOpen] = useState(null);
  const [removing, setRemoving] = useState(null);
  const [notice, setNotice] = useState(null);
  const tasks = data?.tasks ?? [];
  useEffect(() => { setHasRunning(tasks.some(t => ['queued', 'running'].includes(t.lastRun?.status))); }, [tasks]);

  if (error && !data) return <div className="rp-page"><Notice tone="danger" title="Couldn't load tasks">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice></div>;
  if (!data) return <div className="rp-page"><p className="ops-muted">Loading…</p></div>;

  const replace = task => setData(d => ({ ...d, tasks: d.tasks.map(t => t.id === task.id ? task : t) }));
  async function toggle(t, enabled) {
    replace({ ...t, enabled });
    try { replace((await api.updateTask(t.id, { enabled })).task); } catch (err) { replace(t); setNotice({ tone: 'danger', title: 'Couldn\'t change the task', text: err.message }); }
  }
  async function runNow(t) {
    setNotice(null);
    try {
      const { run } = await api.runTask(t.id);
      replace({ ...t, lastRun: run });
      setOpen(t.id);
    } catch (err) { setNotice({ tone: 'warning', title: 'Couldn\'t run it', text: err.message }); }
  }
  const running = data.status === 'running';

  return <div className="rp-page ops-page">
    <PageIntro actions={canEdit && editing !== 'new' && <Button variant="primary" icon={PlusIcon} onClick={() => setEditing('new')}>New task</Button>}>
      Run commands inside {resource.name}'s container on a schedule, e.g. cleanups, reports or syncs. They run with sh -c and the resource's variables.
    </PageIntro>
    {!running && tasks.length > 0 && <Notice tone="warning" title={`${resource.name} isn't running`}>Scheduled runs fail until it runs again, because tasks run inside its container.</Notice>}
    {notice && <Notice tone={notice.tone} title={notice.title} onDismiss={() => setNotice(null)}>{notice.text}</Notice>}
    {editing === 'new' && <TaskForm resource={resource} onCancel={() => setEditing(null)} onSaved={task => { setData(d => ({ ...d, tasks: [...d.tasks, task] })); setEditing(null); }} />}

    {tasks.length === 0 && editing !== 'new'
      ? <Empty icon={CalendarDaysIcon} title="No scheduled tasks" action={canEdit && <Button icon={PlusIcon} onClick={() => setEditing('new')}>New task</Button>}>Create one to run cleanup jobs, reports or syncs on a schedule.</Empty>
      : tasks.length > 0 && <Table className="ops-tasks" columns={[{ label: '', className: 'switch-cell' }, { label: 'Task' }, { label: 'Schedule' }, { label: 'Next run' }, { label: 'Last run' }, { label: '', className: 'actions' }]}>
        {tasks.map(t => {
          const last = t.lastRun;
          const [tone, label] = last ? RUN[last.status] ?? ['neutral', last.status] : ['neutral', 'Never run'];
          const expanded = open === t.id;
          if (editing?.id === t.id) {
            return <tr key={t.id}><td colSpan={6} className="ops-edit-cell">
              <TaskForm resource={resource} task={t} onCancel={() => setEditing(null)} onSaved={task => { replace(task); setEditing(null); }} />
            </td></tr>;
          }
          return <Fragment key={t.id}>
            <tr className={cx(!t.enabled && 'disabled-row', 'ops-task-row')} onClick={() => setOpen(expanded ? null : t.id)}>
              <td className="switch-cell" onClick={e => e.stopPropagation()}><Switch label={`${t.enabled ? 'Pause' : 'Resume'} ${t.name}`} checked={t.enabled} disabled={!canEdit} onChange={enabled => toggle(t, enabled)} /></td>
              <td><strong className="cell-title">{expanded ? <ChevronDownIcon className="ops-caret" /> : <ChevronRightIcon className="ops-caret" />}{t.name}</strong><div className="cell-sub mono ops-cmd" title={t.command}>{t.command}</div></td>
              <td><span>{t.description}</span><div className="cell-sub mono">{t.schedule} · {t.timezone}</div></td>
              <td>{t.enabled && t.nextRunAt ? <><span title={inZone(t.nextRunAt, t.timezone)}>{until(t.nextRunAt)}</span><div className="cell-sub">{inZone(t.nextRunAt, browserZone)}</div></> : <span className="ops-muted">{t.enabled ? '—' : 'Paused'}</span>}</td>
              <td><Status tone={tone}>{label}</Status><div className="cell-sub">{last ? `${timeAgo(last.finishedAt ?? last.startedAt ?? last.createdAt)}${took(last) ? ` · ${took(last)}` : ''}` : ''}</div></td>
              <td className="actions" onClick={e => e.stopPropagation()}><div>
                {canEdit && <button type="button" className="icon-action" aria-label={`Run ${t.name} now`} title={running ? 'Run now' : `${resource.name} isn't running`} disabled={!running || ['queued', 'running'].includes(last?.status)} onClick={() => runNow(t)}><PlayIcon /></button>}
                {canEdit && <button type="button" className="icon-action" aria-label={`Edit ${t.name}`} title="Edit" onClick={() => setEditing(t)}><PencilSquareIcon /></button>}
                {canEdit && <button type="button" className="icon-action danger" aria-label={`Delete ${t.name}`} title="Delete" onClick={() => setRemoving(t)}><TrashIcon /></button>}
              </div></td>
            </tr>
            {expanded && <tr className="ops-runs-row"><td colSpan={6}><Runs task={t} canEdit={canEdit} onChange={refetch} /></td></tr>}
          </Fragment>;
        })}
      </Table>}

    <Dialog open={!!removing} onClose={() => setRemoving(null)} title={`Delete ${removing?.name}?`} description="Its schedule and run history are removed. A run in progress is stopped."
      footer={<><Button onClick={() => setRemoving(null)}>Cancel</Button><Button className="danger-solid" icon={TrashIcon} onClick={async () => {
        const t = removing;
        setRemoving(null);
        try { await api.deleteTask(t.id); setData(d => ({ ...d, tasks: d.tasks.filter(x => x.id !== t.id) })); } catch (err) { setNotice({ tone: 'danger', title: 'Couldn\'t delete it', text: err.message }); }
      }}>Delete task</Button></>} />
  </div>;
}

function TaskForm({ resource, task, onCancel, onSaved }) {
  const preset = task ? PRESETS.find(p => p[0] === task.schedule) : PRESETS[4];
  const [draft, setDraft] = useState(task
    ? { name: task.name, schedule: preset ? task.schedule : 'custom', custom: preset ? '' : task.schedule, timezone: task.timezone, command: task.command, timeoutSeconds: task.timeoutSeconds }
    : emptyDraft);
  const [errors, setErrors] = useState({});
  const [failure, setFailure] = useState('');
  const [saving, setSaving] = useState(false);
  const [preview, setPreview] = useState(null);
  const schedule = draft.schedule === 'custom' ? draft.custom.trim() : draft.schedule;
  const set = key => value => { setDraft(d => ({ ...d, [key]: value })); setErrors(({ [key]: _, ...rest }) => rest); };
  const timer = useRef();
  useEffect(() => {
    clearTimeout(timer.current);
    if (!schedule) { setPreview(null); return undefined; }
    timer.current = setTimeout(() => api.cronPreview(schedule, draft.timezone).then(setPreview).catch(() => setPreview(null)), 250);
    return () => clearTimeout(timer.current);
  }, [schedule, draft.timezone]);
  const zoneOptions = useMemo(() => {
    const list = [...new Set(['UTC', browserZone, draft.timezone, ...zones])];
    return list.map(z => ({ value: z, label: z === browserZone ? `${z} (yours)` : z }));
  }, [draft.timezone]);
  const timeoutOptions = useMemo(() => {
    const list = TIMEOUTS.some(t => t[0] === draft.timeoutSeconds) ? TIMEOUTS : [...TIMEOUTS, [draft.timeoutSeconds, `${draft.timeoutSeconds} seconds`]];
    return list.map(([v, l]) => ({ value: String(v), label: l }));
  }, [draft.timeoutSeconds]);

  async function save(event) {
    event.preventDefault();
    setSaving(true);
    setFailure('');
    const body = { name: draft.name, schedule, timezone: draft.timezone, command: draft.command, timeoutSeconds: draft.timeoutSeconds };
    try {
      const out = task ? await api.updateTask(task.id, body) : await api.createTask(resource.id, { ...body, enabled: true });
      onSaved(out.task);
    } catch (err) {
      setErrors(err.fields ?? {});
      if (!Object.keys(err.fields ?? {}).length) setFailure(err.message);
      setSaving(false);
    }
  }
  const err = key => errors[key] && <span className="ops-error">{errors[key]}</span>;

  return <form className="rp-card ops-task-form" onSubmit={save} onClick={e => e.stopPropagation()}>
    <div className="ops-form-grid">
      <label className="ops-label">Name<TextInput value={draft.name} autoFocus placeholder="Prune expired sessions" aria-invalid={!!errors.name} onChange={e => set('name')(e.target.value)} maxLength={80} />{err('name')}</label>
      <label className="ops-label">Schedule<Select label="Schedule" value={draft.schedule} options={[...PRESETS.map(([value, label]) => ({ value, label, description: value })), { value: 'custom', label: 'Custom cron expression…' }]} onChange={set('schedule')} /></label>
      {draft.schedule === 'custom' && <label className="ops-label">Cron expression<TextInput className="mono" placeholder="*/10 * * * *" value={draft.custom} aria-invalid={!!errors.schedule || preview?.valid === false} onChange={e => set('custom')(e.target.value)} spellCheck={false} /></label>}
      <label className="ops-label">Timezone<Select label="Timezone" searchable value={draft.timezone} options={zoneOptions} onChange={set('timezone')} />{err('timezone')}</label>
      <label className="ops-label ops-span">Command<TextInput className="mono" placeholder="npm run sessions:prune" value={draft.command} aria-invalid={!!errors.command} onChange={e => set('command')(e.target.value)} spellCheck={false} />{err('command')}</label>
      <label className="ops-label">Stop after<Select label="Timeout" value={String(draft.timeoutSeconds)} options={timeoutOptions} onChange={v => set('timeoutSeconds')(Number(v))} />{err('timeoutSeconds')}</label>
    </div>
    <p className={cx('ops-preview', preview?.valid === false && 'bad')}>
      {errors.schedule ? errors.schedule
        : !preview ? ' '
          : !preview.valid ? preview.error
            : <><strong>{preview.description}</strong>{preview.next?.length > 0 && <> · next {preview.next.map(n => inZone(n, draft.timezone)).join(', ')}{draft.timezone !== browserZone && ` (${draft.timezone})`}</>}</>}
    </p>
    {failure && <p className="rp-error">{failure}</p>}
    <div className="task-form-actions"><Button type="button" onClick={onCancel}>Cancel</Button><Button type="submit" variant="primary" disabled={saving}>{saving ? 'Saving…' : task ? 'Save task' : 'Create task'}</Button></div>
  </form>;
}

function Runs({ task, canEdit, onChange }) {
  const [live, setLive] = useState(true);
  const { data, refetch } = useQuery(() => api.taskRuns(task.id), [task.id], { pollMs: live ? 2000 : 0 });
  const runs = data?.runs ?? [];
  const [selected, setSelected] = useState(null);
  const active = runs.some(r => ['queued', 'running'].includes(r.status));
  useEffect(() => { setLive(active); if (!active) onChange?.(); }, [active]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { if (!selected && runs[0]) setSelected(runs[0].id); }, [runs, selected]);

  if (!data) return <p className="ops-muted ops-pad">Loading runs…</p>;
  if (!runs.length) return <p className="ops-muted ops-pad">No runs yet. {canEdit ? 'Use Run now to try it.' : ''}</p>;
  return <div className="ops-runs">
    <ol className="ops-run-list">
      {runs.map(r => {
        const [tone, label] = RUN[r.status] ?? ['neutral', r.status];
        return <li key={r.id}><button type="button" className={cx(selected === r.id && 'active')} onClick={() => setSelected(r.id)}>
          <Status tone={tone} label={label} />
          <span>{inZone(r.startedAt ?? r.createdAt, browserZone, false)}</span>
          <small>{r.trigger === 'manual' ? `by ${r.by || 'someone'}` : 'scheduled'}{took(r) && ` · ${took(r)}`}</small>
        </button></li>;
      })}
    </ol>
    {selected && <RunOutput key={selected} runId={selected} canEdit={canEdit} onStopped={refetch} />}
  </div>;
}

function RunOutput({ runId, canEdit, onStopped }) {
  const [poll, setPoll] = useState(true);
  const { data } = useQuery(() => api.taskRun(runId), [runId], { pollMs: poll ? 2000 : 0 });
  const run = data?.run;
  const pre = useRef(null);
  useEffect(() => { if (run && !['queued', 'running'].includes(run.status)) setPoll(false); }, [run?.status]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { if (pre.current) pre.current.scrollTop = pre.current.scrollHeight; }, [run?.output]);
  if (!run) return <div className="ops-run-output"><p className="ops-muted ops-pad">Loading…</p></div>;
  const [tone, label] = RUN[run.status] ?? ['neutral', run.status];
  return <div className="ops-run-output">
    <div className="ops-run-head">
      <Status tone={tone}>{label}</Status>
      <span className="ops-muted">{run.exitCode != null && `exit ${run.exitCode} · `}{run.trigger === 'manual' ? `run by ${run.by || 'someone'}` : `scheduled for ${inZone(run.scheduledFor, browserZone)}`}</span>
      {canEdit && ['queued', 'running'].includes(run.status) && <Button icon={StopIcon} onClick={async () => { await api.cancelTaskRun(run.id).catch(() => {}); onStopped?.(); }}>Stop</Button>}
    </div>
    {run.error && <p className={cx('ops-run-error', run.status === 'skipped' && 'warn')}>{run.error}</p>}
    <pre ref={pre} className="rp-console ops-console">{run.output || (run.status === 'running' ? 'Waiting for output…' : run.status === 'queued' ? 'Starting…' : 'No output.')}</pre>
  </div>;
}

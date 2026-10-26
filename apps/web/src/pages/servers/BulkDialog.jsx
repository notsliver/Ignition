import { useEffect, useState } from 'react';
import { ArrowPathIcon, CheckCircleIcon, MinusCircleIcon, XCircleIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, Notice, TextInput } from '../../ui';
import { api } from '../../api/client';
import { useJob } from './shared';

/*
 * Bulk actions validate every server on its own, show who's eligible before anything runs, then
 * report per-server progress. Partial failure is a normal outcome, with a retry for the failures.
 * No bulk action is destructive.
 */

const impact = {
  test: 'Checks each connection now. Read-only.',
  refresh: 'Checks each connection and, on this machine, refreshes every workload\'s recorded status from Docker. Read-only for workloads.',
  diagnostics: 'Runs every diagnostic check on each server. Read-only.',
  enable_deployments: 'Resources on these servers can be deployed again.',
  disable_deployments: 'New deploys, redeploys, rollbacks and deploy hooks are refused. Running workloads keep running.',
  enter_maintenance: 'New deployments and scheduled server tasks pause. Workloads keep running; nothing is drained or rebooted.',
};

export function BulkDialog({ action, servers, onClose }) {
  const [preview, setPreview] = useState(null);
  const [results, setResults] = useState(null);
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  const [note, setNote] = useState('');
  const [finished, setFinished] = useState({});

  async function load(ids) {
    setError('');
    try { setPreview(await api.bulkServers({ action: action.action, ids, preview: true })); } catch (e) { setError(e.message); }
  }
  useEffect(() => { load(servers.map(s => s.id)); }, []); // eslint-disable-line react-hooks/exhaustive-deps

  async function run(ids) {
    setPending(true);
    setError('');
    setFinished({});
    try { setResults(await api.bulkServers({ action: action.action, ids, note })); } catch (e) { setError(e.message); } finally { setPending(false); }
  }

  const eligible = preview?.targets.filter(t => t.eligible) ?? [];
  const outcome = t => t.status === 'queued' ? finished[t.id] ?? 'running' : t.status;
  const failed = results?.targets.filter(t => t.eligible && outcome(t) === 'failed') ?? [];
  const running = results?.targets.some(t => outcome(t) === 'running');
  const succeeded = results?.targets.filter(t => ['done', 'succeeded'].includes(outcome(t))).length ?? 0;

  const footer = results
    ? <>{failed.length > 0 && !running && <Button icon={ArrowPathIcon} onClick={() => { setResults(null); load(failed.map(t => t.id)); }}>Retry {failed.length} failed</Button>}<Button variant="primary" onClick={() => onClose(true)} disabled={running}>{running ? 'Running…' : 'Done'}</Button></>
    : <><Button onClick={() => onClose(false)}>Cancel</Button><Button variant="primary" disabled={!eligible.length || pending} onClick={() => run(eligible.map(t => t.id))}>{pending ? 'Starting…' : `${action.label} on ${eligible.length}`}</Button></>;

  return <Dialog open onClose={() => !running && onClose(!!results)} title={`${action.label}: ${servers.length} server${servers.length === 1 ? '' : 's'}`} description={impact[action.action]} footer={footer}>
    {error && <Notice tone="danger" title="Couldn't run the bulk action">{error}</Notice>}
    {!preview && !error && <p className="muted">Checking each server…</p>}
    {action.action === 'enter_maintenance' && !results && <FormField label="Note" htmlFor="bnote" hint="Kept with each server's maintenance history."><TextInput id="bnote" value={note} onChange={e => setNote(e.target.value)} maxLength={2000} /></FormField>}
    {results && !running && <Notice tone={failed.length ? (succeeded ? 'warning' : 'danger') : 'success'} title={failed.length ? `${succeeded} succeeded, ${failed.length} failed` : `Succeeded on ${succeeded} server${succeeded === 1 ? '' : 's'}`} />}
    {preview && <ul className="sv-bulk">
      {(results ?? preview).targets.map(t => results && t.status === 'queued'
        ? <JobTarget key={t.id} target={t} onFinish={status => setFinished(f => ({ ...f, [t.id]: status }))} />
        : <TargetRow key={t.id} target={t} previewing={!results} />)}
    </ul>}
  </Dialog>;
}

function TargetRow({ target: t, previewing }) {
  const ok = previewing ? t.eligible : t.status === 'done';
  const Icon = !t.eligible ? MinusCircleIcon : ok ? CheckCircleIcon : t.status === 'failed' ? XCircleIcon : CheckCircleIcon;
  return <li className={!t.eligible ? 'skipped' : t.status === 'failed' ? 'failed' : 'ok'}>
    <Icon /><strong>{t.name || t.id}</strong>
    <span>{!t.eligible ? `Skipped: ${t.reason}` : previewing ? 'Eligible' : t.status === 'failed' ? t.error : 'Done'}</span>
  </li>;
}

function JobTarget({ target, onFinish }) {
  const { job, done } = useJob(target.jobId, { onDone: j => onFinish(j.status === 'succeeded' ? 'succeeded' : 'failed') });
  const failed = done && job.status !== 'succeeded';
  return <li className={failed ? 'failed' : done ? 'ok' : 'running'}>
    {failed ? <XCircleIcon /> : done ? <CheckCircleIcon /> : <ArrowPathIcon className="sv-spin" />}
    <strong>{target.name}</strong>
    <span>{!done ? (job?.progress || (job?.status === 'running' ? 'Running…' : 'Queued')) : failed ? job.error || job.status : job.result?.docker ? `Docker ${job.result.docker} · ${job.result.latencyMs} ms` : 'Succeeded'}</span>
  </li>;
}

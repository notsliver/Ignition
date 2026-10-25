import { useEffect, useState } from 'react';
import {
  ArrowPathIcon, ArrowRightIcon, ArrowTopRightOnSquareIcon, CheckCircleIcon, ExclamationTriangleIcon, HeartIcon,
  PlayIcon, RocketLaunchIcon, StopIcon, XCircleIcon,
} from '@heroicons/react/16/solid';
import { Avatar, BranchIcon, Button, CommitIcon, Dialog, Meter, Notice, Status, cx } from '../../../ui';
import { api } from '../../../api/client';
import { duration, timeAgo, useQuery } from '../../../api/hooks';
import { useSession } from '../../../app/AuthGate';
import { publicUrl, resourceStatus, sourceLabel } from '../Resources/Resources';
import { CopyButton } from '../resource/shared';
import './ResourceDetail.css';

/*
 * Overview answers: is it healthy, what's running, what can I do right now?
 * Everything here comes from the API and Docker; values Ignition doesn't measure say so.
 */

const typeLabels = { application: 'Application', database: 'Database', worker: 'Worker' };
const deployTone = { queued: 'neutral', building: 'warning', deploying: 'progress', successful: 'success', failed: 'danger', cancelled: 'neutral' };
const deployLabel = { queued: 'Queued', building: 'Building', deploying: 'Deploying', successful: 'Deployment successful', failed: 'Deployment failed', cancelled: 'Deployment cancelled' };
const bytes = n => n >= 1 << 30 ? `${(n / (1 << 30)).toFixed(2)} GB` : n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(0)} MB` : `${(n / 1024).toFixed(0)} KB`;
const eventText = { 'resource.create': 'Resource created', 'resource.start': 'Started', 'resource.stop': 'Stopped', 'resource.restart': 'Restarted', 'resource.update': 'Settings changed',
  'resource.env.update': 'Environment variables updated', 'resource.env.reveal': 'A secret value was revealed', 'deployment.manual': 'Deployment started', 'deployment.redeploy': 'Redeploy started',
  'deployment.rollback': 'Rollback started', 'deployment.cancel': 'Deployment cancelled' };

export function ResourceDetail({ project, environment, resource: initial, onNavigate, onResourceChange }) {
  const { user } = useSession();
  const isAdmin = user.role === 'admin' || user.role === 'owner';
  const [confirmStop, setConfirmStop] = useState(false);
  const [actionError, setActionError] = useState('');
  const [busy, setBusy] = useState('');

  const res = useQuery(() => api.resource(initial.id), [initial.id], { pollMs: 3000 });
  const resource = res.data?.resource ?? initial;
  useEffect(() => { if (res.data?.resource) onResourceChange?.(res.data.resource); }, [res.data]); // eslint-disable-line react-hooks/exhaustive-deps
  const running = resource.status === 'running';
  const stats = useQuery(() => api.stats(initial.id), [initial.id, running], { pollMs: running ? 5000 : 0 });
  const deployments = useQuery(() => api.deployments(initial.id, { limit: 5 }), [initial.id], { pollMs: 4000 });
  const audit = useQuery(() => api.audit({ target: initial.id }), [initial.id], { pollMs: 10000, enabled: isAdmin });

  const latest = deployments.data?.deployments?.[0];
  const current = deployments.data?.deployments?.find(d => d.current);
  const status = resourceStatus[resource.status] ?? resourceStatus.created;
  const url = publicUrl(resource);
  const s = stats.data?.available ? stats.data.stats : null;
  const internal = resource.port ? `${resource.name}:${resource.port}` : resource.name;

  const warnings = [];
  if (resource.status === 'failed') warnings.push({ tone: 'danger', text: latest?.error ? `Not running: ${latest.error}` : 'The container stopped unexpectedly.', link: 'Logs', action: 'Logs' });
  if (latest?.status === 'failed' && resource.status === 'running') warnings.push({ tone: 'warning', text: 'The latest deployment failed; the previous release is still serving.', link: 'Deployments', action: 'Deployment' });

  async function act(label, fn) {
    setBusy(label);
    setActionError('');
    try { await fn(); await Promise.all([res.refetch(), deployments.refetch()]); } catch (e) { setActionError(e.message); } finally { setBusy(''); }
  }
  const events = isAdmin && audit.data ? audit.data.events.slice(0, 6).map(e => ({ tone: e.action.includes('stop') || e.action.includes('reveal') ? 'neutral' : 'success', text: `${eventText[e.action] ?? e.action} by ${e.actor}`, time: timeAgo(e.createdAt) }))
    : (deployments.data?.deployments ?? []).map(d => ({ tone: d.status === 'failed' ? 'warning' : 'success', text: `Deployment #${d.number} ${d.status}`, time: timeAgo(d.createdAt) }));

  return <div className="resource-overview">
    {warnings.length > 0 && <ul className="ov-warnings">
      {warnings.map(w => <li key={w.text} className={w.tone}>
        <ExclamationTriangleIcon />{w.text}
        <button type="button" onClick={() => onNavigate(w.link)}>{w.action}<ArrowRightIcon /></button>
      </li>)}
    </ul>}
    {actionError && <Notice tone="danger" title="That didn't work">{actionError}</Notice>}

    <section className="ov-hero">
      <div className="ov-status">
        <span className="ov-label">Resource status</span>
        <Status tone={status.tone}>{status.label}</Status>
        <dl>
          <div><dt>Desired state</dt><dd>{resource.desiredState === 'running' ? 'Running' : 'Stopped'}</dd></div>
          <div><dt>Last change</dt><dd>{timeAgo(resource.updatedAt)}</dd></div>
          <div><dt>Health</dt><dd className={cx(resource.status === 'failed' && 'bad')}>{running ? 'Process running' : resource.status === 'failed' ? 'Not running' : '—'}</dd></div>
        </dl>
      </div>
      <div className="ov-actions">
        <span className="ov-label">Quick actions</span>
        <div>
          {running && <Button icon={ArrowPathIcon} disabled={!!busy} onClick={() => act('restart', () => api.resourceAction(resource.id, 'restart'))}>{busy === 'restart' ? 'Restarting…' : 'Restart'}</Button>}
          {resource.status === 'stopped'
            ? <Button icon={PlayIcon} disabled={!!busy} onClick={() => act('start', () => api.resourceAction(resource.id, 'start'))}>{busy === 'start' ? 'Starting…' : 'Start'}</Button>
            : <Button icon={StopIcon} onClick={() => setConfirmStop(true)} disabled={!running || !!busy}>Stop</Button>}
          {url && resource.type === 'application' && <a className="button" href={url} target="_blank" rel="noreferrer"><ArrowTopRightOnSquareIcon />Open app</a>}
          <Button variant="primary" icon={RocketLaunchIcon} disabled={resource.status === 'deploying' || !!busy} onClick={() => act('deploy', () => api.deploy(resource.id, {}))}>{resource.status === 'deploying' ? 'Deploying…' : 'Redeploy'}</Button>
        </div>
        {url && <div className="ov-url"><a href={url} target="_blank" rel="noreferrer">{url.replace('http://', '')}</a><CopyButton value={url} label="Copy URL" /></div>}
      </div>
    </section>

    <section className="ov-usage" aria-label="Usage">
      <button type="button" onClick={() => onNavigate('Metrics')}>
        <span>CPU</span><strong>{s ? `${s.cpuPercent.toFixed(1)}%` : '—'}</strong><small>{s ? `of ${resource.cpus} vCPU limit` : stats.data?.reason ?? 'Not running'}</small>
        {s && <Meter label="CPU" value={Math.min(100, Math.round(s.cpuPercent / resource.cpus))} compact />}
      </button>
      <button type="button" onClick={() => onNavigate('Metrics')}>
        <span>Memory</span><strong>{s ? bytes(s.memoryBytes) : '—'}</strong><small>of {resource.memoryMb} MB limit</small>
        {s && <Meter label="Memory" value={Math.round(s.memoryBytes / s.memoryLimit * 100)} compact />}
      </button>
      <button type="button" onClick={() => onNavigate('Metrics')}>
        <span>Network</span><strong>{s ? bytes(s.networkRxBytes + s.networkTxBytes) : '—'}</strong><small>{s ? `↓ ${bytes(s.networkRxBytes)} · ↑ ${bytes(s.networkTxBytes)} since start` : 'Not running'}</small>
      </button>
      <button type="button" onClick={() => onNavigate('Environment Variables')}>
        <span>Storage</span><strong>{resource.dataPath ? 'Volume' : 'None'}</strong><small>{resource.dataPath ? `Mounted at ${resource.dataPath}` : 'Stateless: files are lost on redeploy'}</small>
      </button>
    </section>

    <div className="ov-columns">
      <div className="ov-stack">
        <section className="ov-card">
          <header><h2>Latest deployment</h2><button type="button" className="ov-link" onClick={() => onNavigate('Deployments')}>All deployments<ArrowRightIcon /></button></header>
          {latest ? <div className="ov-deploy">
            <Status tone={deployTone[latest.status]}>{deployLabel[latest.status]}</Status>
            <p className="ov-deploy-message">#{latest.number} · {latest.trigger === 'create' ? 'First deployment' : latest.reason || `${latest.trigger[0].toUpperCase()}${latest.trigger.slice(1)}`}</p>
            <p className="ov-deploy-meta">{latest.branch && <><BranchIcon />{latest.branch}<span>·</span></>}{latest.commit && <><CommitIcon /><code>{latest.commit}</code><span>·</span></>}{timeAgo(latest.createdAt)}<span>·</span>{duration(latest.startedAt, latest.finishedAt)}</p>
            <div className="ov-deploy-by"><Avatar name={latest.author} />{latest.author}</div>
            {latest.error && <p className="ov-deploy-error"><XCircleIcon />{latest.error}</p>}
          </div> : <p className="ov-empty">{deployments.loading ? 'Loading…' : 'No deployments yet.'}</p>}
        </section>

        <section className="ov-card">
          <header><h2>Recent events</h2><button type="button" className="ov-link" onClick={() => onNavigate('Logs')}>Open logs<ArrowRightIcon /></button></header>
          {events.length ? <ol className="ov-events">
            {events.map((e, i) => <li key={i}>
              {e.tone === 'success' ? <CheckCircleIcon className="success" /> : e.tone === 'warning' ? <ExclamationTriangleIcon className="warning" /> : <HeartIcon className="neutral" />}
              <span>{e.text}</span><time>{e.time}</time>
            </li>)}
          </ol> : <p className="ov-empty">No events yet.</p>}
        </section>
      </div>

      <section className="ov-card">
        <header><h2>Details</h2></header>
        <dl className="ov-identity">
          <div><dt>Type</dt><dd>{typeLabels[resource.type] ?? resource.type}</dd></div>
          <div><dt>Project</dt><dd>{project.name} · {environment}</dd></div>
          <div><dt>Server</dt><dd>This machine</dd></div>
          <div><dt>{resource.sourceType === 'git' ? 'Repository' : 'Image'}</dt><dd className="mono">{sourceLabel(resource)}</dd></div>
          {current?.imageRef && <div><dt>Running image</dt><dd className="mono">{current.imageRef}</dd></div>}
          <div><dt>Internal address</dt><dd className="mono">{internal}<CopyButton value={internal} label="Copy internal address" /></dd></div>
          <div><dt>Resource ID</dt><dd className="mono">{resource.id.slice(0, 8)}<CopyButton value={resource.id} label="Copy resource ID" /></dd></div>
          <div><dt>Created</dt><dd>{new Date(resource.createdAt).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })}</dd></div>
        </dl>
        <nav className="ov-shortcuts" aria-label="Shortcuts">
          {['Logs', 'Deployments', 'Environment Variables'].map(page => <button key={page} type="button" onClick={() => onNavigate(page)}>{page}<ArrowRightIcon /></button>)}
        </nav>
      </section>
    </div>

    <Dialog open={confirmStop} onClose={() => setConfirmStop(false)} title={`Stop ${resource.name}?`}
      description={resource.type === 'application' ? 'It stops serving traffic until you start it again. Volume data is kept.' : 'It stops until you start it again. Volume data is kept.'}
      footer={<><Button onClick={() => setConfirmStop(false)}>Cancel</Button><Button className="danger-solid" icon={StopIcon} onClick={() => { setConfirmStop(false); act('stop', () => api.resourceAction(resource.id, 'stop')); }}>Stop</Button></>} />
  </div>;
}

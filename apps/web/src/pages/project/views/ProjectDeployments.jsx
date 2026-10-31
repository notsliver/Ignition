import { useMemo, useState } from 'react';
import {
  ArrowPathIcon, ArrowRightIcon, ArrowTopRightOnSquareIcon, ArrowUturnLeftIcon, ClipboardDocumentIcon, EllipsisHorizontalIcon, RocketLaunchIcon, StopCircleIcon,
} from '@heroicons/react/16/solid';
import { Avatar, BranchIcon, Button, CommitIcon, Dialog, Menu, Notice, SearchField, SegmentedControl, Select, Status, cx } from '../../../ui';
import { api } from '../../../api/client';
import { duration, timeAgo, useQuery } from '../../../api/hooks';
import { can, useSession } from '../../../app/AuthGate';
import { Timeline, active, statusInfo, triggerLabel } from '../resource/ResourceDeployments';
import '../Deployments/Deployments.css';
import '../resource/ResourceDeployments.css';
import './views.css';

/*
 * Every deployment of every resource in the project (in the selected environment), newest first,
 * with 14-day statistics. Each row opens the same step-by-step timeline as the resource page, and
 * offers the same actions: cancel, retry, redeploy, roll back.
 */

const PAGE = 50;
const tabs = [{ value: '', label: 'All' }, { value: 'active', label: 'In progress' }, { value: 'failed', label: 'Failed' }, { value: 'successful', label: 'Successful' }];
const secs = s => s == null ? '—' : s < 60 ? `${Math.round(s)}s` : `${Math.floor(s / 60)}m ${String(Math.round(s % 60)).padStart(2, '0')}s`;

export function ProjectDeployments({ project, environment, onOpenResource }) {
  const { user } = useSession();
  const [status, setStatus] = useState('');
  const [resource, setResource] = useState('');
  const [query, setQuery] = useState('');
  const [limit, setLimit] = useState(PAGE);
  const [expanded, setExpanded] = useState(null);
  const [feedback, setFeedback] = useState(null);
  const [redeploying, setRedeploying] = useState(false);
  const params = { env: environment, status, resource, q: query.trim(), limit };
  const { data, error, refetch } = useQuery(() => api.projectDeployments(project.id, params), [project.id, environment, status, resource, query, limit], { pollMs: 4000 });
  const resources = useQuery(() => api.resources(project.id, { environment: project.environments.find(e => e.name === environment)?.id }), [project.id, environment]);
  const list = data?.deployments ?? [];
  const st = data?.stats;
  const openId = expanded ?? list.find(d => active(d.status))?.id;
  const deployable = can(user, 'deployments.create');

  async function run(fn, title) {
    setFeedback(null);
    try { const out = await fn(); setFeedback({ tone: 'success', title }); await refetch(); if (out?.deploymentId) setExpanded(out.deploymentId); }
    catch (e) { setFeedback({ tone: 'danger', title: 'That didn\'t work', text: e.message }); }
  }
  const actions = d => [
    { label: 'Open resource', icon: ArrowTopRightOnSquareIcon, onSelect: () => onOpenResource({ id: d.resourceId, projectId: project.id }, 'Deployments') },
    ...(deployable ? ['separator',
      ...(active(d.status) && can(user, 'deployments.cancel') ? [{ label: 'Cancel deployment', icon: StopCircleIcon, danger: true, onSelect: () => run(() => api.cancelDeployment(d.id), `#${d.number} of ${d.resourceName} cancelled`) }] : []),
      ...(d.status === 'failed' ? [{ label: 'Retry', icon: ArrowPathIcon, onSelect: () => run(() => api.deploy(d.resourceId, { reason: `Retry of #${d.number}` }), `Retrying ${d.resourceName}`) }] : []),
      ...(!active(d.status) && d.imageRef ? [{ label: 'Redeploy this version', icon: RocketLaunchIcon, onSelect: () => run(() => api.deploy(d.resourceId, { from: d.id }), `Redeploying #${d.number} of ${d.resourceName}`) }] : []),
      ...(d.status === 'successful' && !d.current && d.imageRef ? [{ label: 'Roll back to this', icon: ArrowUturnLeftIcon, onSelect: () => run(() => api.deploy(d.resourceId, { from: d.id, reason: `Rollback to #${d.number}` }), `Rolling ${d.resourceName} back to #${d.number}`) }] : []),
    ] : []),
    'separator',
    { label: 'Copy deployment ID', icon: ClipboardDocumentIcon, onSelect: () => navigator.clipboard?.writeText(d.id) },
    ...(d.commit ? [{ label: 'Copy commit SHA', icon: ClipboardDocumentIcon, onSelect: () => navigator.clipboard?.writeText(d.commit) }] : []),
  ];
  const resourceOptions = [{ value: '', label: 'All resources' }, ...(resources.data?.resources ?? []).filter(r => r.sourceType !== 'external').map(r => ({ value: r.id, label: r.name }))];
  const max = Math.max(1, ...(st?.daily ?? []).map(d => d.successful + d.failed));
  const rate = st && st.successful + st.failed ? Math.round(st.successful / (st.successful + st.failed) * 100) : null;

  return <div className="pv-page">
    <section className="pv-summary">
      <div><span>Deployments</span><strong>{st ? st.total : '—'}</strong><small>Last 14 days{st?.active ? ` · ${st.active} in progress` : ''}</small></div>
      <div className={cx(rate != null && rate < 80 && 'warn')}><span>Success rate</span><strong>{rate != null ? `${rate}%` : '—'}</strong><small>{st ? `${st.successful} successful · ${st.failed} failed` : ''}</small></div>
      <div><span>Typical duration</span><strong>{st ? secs(st.avgSeconds) : '—'}</strong><small>Average of successful ones</small></div>
      <div className="pv-chart" aria-label="Deployments per day, last 14 days">
        <span>Per day</span>
        <div className="pv-bars">{(st?.daily ?? []).map(d => <span key={d.day} title={`${new Date(d.day).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}: ${d.successful} successful, ${d.failed} failed`}>
          <i className="ok" style={{ height: `${d.successful / max * 100}%` }} /><i className="bad" style={{ height: `${d.failed / max * 100}%` }} />
        </span>)}</div>
        <small>{st?.lastFailedAt ? `Last failure ${timeAgo(st.lastFailedAt)}` : 'No failures'}</small>
      </div>
    </section>

    <div className="deployments-toolbar">
      <SegmentedControl label="Status" options={tabs} value={status} onChange={v => { setStatus(v); setLimit(PAGE); }} className="deployments-status" />
      <Select label="Resource" value={resource} onChange={v => { setResource(v); setLimit(PAGE); }} options={resourceOptions} searchable={8} className="pv-select" />
      <SearchField value={query} onChange={setQuery} placeholder="Search resource, commit, image, error or note" className="deployments-search" />
      {deployable && <Button variant="primary" icon={RocketLaunchIcon} onClick={() => setRedeploying(true)} disabled={!resources.data?.resources?.length}>Redeploy {environment}</Button>}
    </div>
    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}</Notice>}
    {error && !data && <Notice tone="danger" title="Couldn't load deployments">{error.message}</Notice>}

    {list.length ? <ol className="deployment-list">
      {list.map(d => {
        const s = statusInfo[d.status];
        const open = openId === d.id;
        const timing = active(d.status) ? duration(d.startedAt ?? d.createdAt) : `${duration(d.startedAt, d.finishedAt)} (${timeAgo(d.createdAt)})`;
        return <li key={d.id} className={cx('deployment', open && 'open')}>
          <div className="deployment-row pv-dep-row" role="button" tabIndex={0} aria-expanded={open} onClick={() => setExpanded(open ? '' : d.id)}
            onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setExpanded(open ? '' : d.id); } }}>
            <span className="pv-dep-resource"><strong>{d.resourceName}</strong><span>#{d.number}{d.current && <span className="current-pill"><ArrowRightIcon />Current</span>}</span></span>
            <span className="deployment-env">{triggerLabel[d.trigger] ?? d.trigger}</span>
            <span className="deployment-status"><Status tone={s.tone}>{s.label}</Status><span className="deployment-timing">{timing}</span></span>
            <span className="deployment-ref"><BranchIcon /><span>{d.branch || '—'}</span></span>
            <span className="deployment-ref commit"><CommitIcon /><code>{d.commit || '—'}</code><span className="deployment-message">{d.error || d.reason || d.imageRef}</span></span>
            <span className="deployment-author" title={`By ${d.author}`}><Avatar name={d.author} /></span>
            <span onClick={e => e.stopPropagation()} onKeyDown={e => e.stopPropagation()}><Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${d.resourceName} #${d.number}`} items={actions(d)} /></span>
          </div>
          {open && <div className="deployment-details">
            <div className="dep-facts">
              <span><b>Image</b><code>{d.imageRef || 'not built yet'}</code></span>
              <span><b>By</b>{d.author}{d.reason && <> — “{d.reason}”</>}</span>
              {d.error && <span className="dep-error-fact"><b>Error</b>{d.error}</span>}
            </div>
            <Timeline key={`${d.id}:${d.status}`} d={d} />
          </div>}
        </li>;
      })}
    </ol> : <p className="deployments-none">{!data ? 'Loading deployments…' : status || resource || query ? 'No deployments match these filters.' : `No deployments in ${environment} yet.`}</p>}
    {data && data.total > list.length && <div className="pv-more"><Button onClick={() => setLimit(l => l + PAGE)}>Show {Math.min(PAGE, data.total - list.length)} more</Button><span>{list.length} of {data.total}</span></div>}

    {redeploying && <RedeployDialog project={project} environment={environment} resources={(resources.data?.resources ?? []).filter(r => r.sourceType !== 'external')}
      onClose={() => setRedeploying(false)} onDone={(n, failed) => { setRedeploying(false); setFeedback(failed.length ? { tone: 'warning', title: `${n} deployment${n === 1 ? '' : 's'} started`, text: `Couldn't start: ${failed.join('; ')}` } : { tone: 'success', title: `${n} deployment${n === 1 ? '' : 's'} started` }); refetch(); }} />}
  </div>;
}

function RedeployDialog({ environment, resources, onClose, onDone }) {
  const [picked, setPicked] = useState(() => new Set(resources.map(r => r.id)));
  const [pending, setPending] = useState(false);
  const toggle = id => setPicked(s => { const n = new Set(s); n.has(id) ? n.delete(id) : n.add(id); return n; });
  async function go() {
    setPending(true);
    const failed = [];
    let n = 0;
    for (const r of resources.filter(r => picked.has(r.id))) {
      try { await api.deploy(r.id, { reason: `Redeploy of ${environment}` }); n++; } catch (e) { failed.push(`${r.name}: ${e.message}`); }
    }
    onDone(n, failed);
  }
  return <Dialog open onClose={onClose} icon={RocketLaunchIcon} title={`Redeploy ${environment}`} className="pv-dialog"
    description="Each picked resource builds its latest source (or pulls its image again) and rolls out. They deploy side by side."
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" icon={RocketLaunchIcon} onClick={go} disabled={pending || !picked.size}>{pending ? 'Starting…' : `Deploy ${picked.size}`}</Button></>}>
    <ul className="pv-pick">{resources.map(r => <li key={r.id}><label>
      <input type="checkbox" checked={picked.has(r.id)} onChange={() => toggle(r.id)} />
      <span><strong>{r.name}</strong><small>{r.sourceType === 'git' ? `${r.repoUrl?.replace(/^https?:\/\//, '')} · ${r.branch}` : r.image}</small></span>
      <Status tone={r.status === 'running' ? 'success' : r.status === 'failed' ? 'danger' : 'neutral'}>{r.status}</Status>
    </label></li>)}</ul>
  </Dialog>;
}


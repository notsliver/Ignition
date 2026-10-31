import { useEffect, useMemo, useState } from 'react';
import {
  ArrowPathIcon, ArrowTopRightOnSquareIcon, ChevronLeftIcon, ChevronRightIcon, CubeIcon, DocumentTextIcon, EllipsisHorizontalIcon, PlayIcon,
  RocketLaunchIcon, Square3Stack3DIcon, StopIcon, XMarkIcon,
} from '@heroicons/react/16/solid';
import { Button, Menu, Notice, SearchField, SegmentedControl, Status, cx } from '../../ui';
import { api } from '../../api/client';
import { duration, timeAgo, useQuery } from '../../api/hooks';
import { useSession, can } from '../../app/AuthGate';
import { resourceStatus, sourceLabel } from '../project/Resources/Resources';
import { Table } from '../project/resource/shared';
import { bytes, isDeveloper } from './model';
import { ConfirmDialog, Fresh } from './shared';

/* ---------------------------------- Resources --------------------------------- */

const categories = [
  { value: 'all', label: 'All' }, { value: 'application', label: 'Applications' }, { value: 'database', label: 'Databases' },
  { value: 'worker', label: 'Services' }, { value: 'cron', label: 'Scheduled' },
];
const states = [
  { value: 'all', label: 'Any state' }, { value: 'running', label: 'Running' }, { value: 'stopped', label: 'Stopped' },
  { value: 'unhealthy', label: 'Unhealthy' }, { value: 'starting', label: 'Starting' }, { value: 'unknown', label: 'Unknown' },
];
const typeLabel = { application: 'Application', database: 'Database', worker: 'Service', cron: 'Scheduled job' };

function observed(r, health) {
  if (r.status === 'running' && health[r.id] === 'unhealthy') return { key: 'unhealthy', tone: 'warning', label: 'Running · unhealthy' };
  if (r.status === 'running' && health[r.id] === 'starting') return { key: 'starting', tone: 'progress', label: 'Running · starting' };
  if (r.status === 'deploying') return { key: 'starting', tone: 'progress', label: 'Deploying' };
  if (r.status === 'failed') return { key: 'unhealthy', tone: 'danger', label: 'Failed' };
  if (r.status === 'running') return { key: 'running', tone: 'success', label: 'Running' };
  if (r.status === 'stopped' || r.status === 'paused') return { key: 'stopped', ...resourceStatus[r.status] };
  return { key: 'unknown', tone: 'neutral', label: resourceStatus[r.status]?.label ?? r.status };
}

export function ServerResources({ server, onNavigate, onOpenResource, setFeedback }) {
  const { user } = useSession();
  const dev = can(user, 'resources.control');
  const [category, setCategory] = useState('all');
  const [state, setState] = useState('all');
  const [query, setQuery] = useState('');
  const [stopping, setStopping] = useState(null);
  const { data, error, loading, refetch } = useQuery(() => api.serverResources(server.id), [server.id], { pollMs: 5000 });
  const list = data?.resources ?? [];
  const health = data?.health ?? {};
  const rows = useMemo(() => list.filter(r => (category === 'all' || r.type === category) && (state === 'all' || observed(r, health).key === state)
    && (!query || `${r.name} ${r.projectName} ${sourceLabel(r)}`.toLowerCase().includes(query.toLowerCase()))), [list, health, category, state, query]);

  async function act(r, label, fn) {
    try { await fn(); setFeedback({ tone: 'success', title: `${label} queued for ${r.name}`, text: 'The resource page shows its progress.' }); refetch(); }
    catch (e) { setFeedback({ tone: 'danger', title: `${label} failed for ${r.name}`, text: e.message }); }
  }
  const menu = r => [
    { label: 'Open resource', icon: ArrowTopRightOnSquareIcon, onSelect: () => onOpenResource(r, 'Overview') },
    { label: 'View logs', icon: DocumentTextIcon, onSelect: () => onOpenResource(r, 'Logs') },
    ...(dev ? ['separator',
      ...(r.status === 'running' ? [{ label: 'Restart', icon: ArrowPathIcon, onSelect: () => act(r, 'Restart', () => api.resourceAction(r.id, 'restart')) },
        { label: 'Stop', icon: StopIcon, onSelect: () => setStopping(r) }] : []),
      ...(r.status === 'stopped' ? [{ label: 'Start', icon: PlayIcon, onSelect: () => act(r, 'Start', () => api.resourceAction(r.id, 'start')) }] : []),
      { label: 'Redeploy', icon: RocketLaunchIcon, onSelect: () => act(r, 'Redeploy', () => api.deploy(r.id, {})) },
    ] : []),
  ];

  if (!server.isLocal) return <div className="rp-empty">
    <CubeIcon />
    <strong>No Ignition workloads on remote servers</strong>
    <p>The deploy engine only runs resources on this machine. Containers already on {server.name} are listed on its Docker page.</p>
    <Button icon={Square3Stack3DIcon} onClick={() => onNavigate('Docker')}>Open Docker</Button>
  </div>;

  return <>
    <div className="rp-toolbar">
      <SegmentedControl label="Category" options={categories} value={category} onChange={setCategory} className="auto-width" />
      <SegmentedControl label="State" options={states} value={state} onChange={setState} className="auto-width sv-hide-narrow" />
      <SearchField value={query} onChange={setQuery} placeholder="Search name, project or image" />
      <span className="spacer" />
      <Fresh at={data?.observedAt} label="Observed" />
    </div>
    {error && <Notice tone="danger" title="Couldn't load workloads" action="Retry" onAction={refetch}>{error.message}</Notice>}
    {!rows.length ? <div className="rp-empty"><CubeIcon /><strong>{loading && !data ? 'Loading…' : list.length ? 'No workloads match' : 'No workloads on this server'}</strong>
      <p>{list.length ? 'Change the filters to see more.' : 'Resources you create in projects run on this machine.'}</p></div>
      : <Table className="sv-table-wrap" columns={[{ label: 'Resource' }, { label: 'Category' }, { label: 'Desired' }, { label: 'Observed' }, { label: 'Image / release' },
        { label: 'CPU', className: 'num' }, { label: 'Memory', className: 'num' }, { label: 'Last deploy' }, { label: '', className: 'actions' }]}>
        {rows.map(r => { const o = observed(r, health); const live = r.status === 'running' && r.usage; return <tr key={r.id} className="sv-click" onClick={() => onOpenResource(r, 'Overview')}>
          <td><span className="resource-name">{r.name}<span>{r.projectName} · {r.environment}</span></span></td>
          <td className="muted">{typeLabel[r.type] ?? r.type}</td>
          <td className="muted">{r.desiredState === 'running' ? 'Running' : 'Stopped'}</td>
          <td><Status tone={o.tone}>{o.label}</Status>{r.desiredState === 'running' && ['stopped', 'failed'].includes(r.status) && <span className="sv-drift" title="Desired and observed state differ">drift</span>}</td>
          <td className="mono sv-ellipsis" title={sourceLabel(r)}>{sourceLabel(r)}</td>
          <td className="num" title={live ? `Sampled ${timeAgo(r.usage.at).toLowerCase()}` : 'No current sample'}>{live ? `${r.usage.cpuPercent.toFixed(1)}%` : '—'}</td>
          <td className="num">{live ? bytes(r.usage.memoryBytes) : '—'}</td>
          <td className="muted">{r.lastDeployAt ? timeAgo(r.lastDeployAt) : 'Never'}</td>
          <td className="actions" onClick={e => e.stopPropagation()}><Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${r.name}`} items={menu(r)} /></td>
        </tr>; })}
      </Table>}
    <p className="monitor-footnote">Desired state is what you asked for; observed state is what Docker reports, refreshed every 10 seconds by the worker. Actions go through each resource's own job queue.</p>
    {stopping && <ConfirmDialog title={`Stop ${stopping.name}?`} description="It stops serving until you start it again. Its data volumes are kept." confirmLabel="Stop" danger onClose={() => setStopping(null)}
      onConfirm={async () => { await api.resourceAction(stopping.id, 'stop'); setStopping(null); setFeedback({ tone: 'success', title: `Stop queued for ${stopping.name}` }); refetch(); }} />}
  </>;
}

/* --------------------------------- Deployments -------------------------------- */

const depStatus = {
  queued: { tone: 'neutral', label: 'Queued' }, building: { tone: 'progress', label: 'Building' }, deploying: { tone: 'progress', label: 'Deploying' },
  successful: { tone: 'success', label: 'Successful' }, failed: { tone: 'danger', label: 'Failed' }, cancelled: { tone: 'neutral', label: 'Cancelled' },
};
const PAGE = 30;

export function ServerDeployments({ server, onOpenResource, setFeedback }) {
  const { user } = useSession();
  const dev = can(user, 'resources.control');
  const [status, setStatus] = useState('');
  const [query, setQuery] = useState('');
  const [debounced, setDebounced] = useState('');
  const [page, setPage] = useState(0);
  const [open, setOpen] = useState(null);
  useEffect(() => { const t = setTimeout(() => setDebounced(query.trim()), 250); return () => clearTimeout(t); }, [query]);
  useEffect(() => setPage(0), [status, debounced]);
  const { data, error, loading, refetch } = useQuery(() => api.serverDeployments(server.id, { status, q: debounced, limit: PAGE, offset: page * PAGE }), [server.id, status, debounced, page], { pollMs: 5000 });
  const list = data?.deployments ?? [];
  const total = data?.total ?? 0;

  return <>
    <div className="rp-toolbar">
      <SegmentedControl label="Status" className="auto-width" value={status} onChange={setStatus}
        options={[{ value: '', label: 'All' }, { value: 'active', label: 'In progress' }, { value: 'successful', label: 'Successful' }, { value: 'failed', label: 'Failed' }, { value: 'cancelled', label: 'Cancelled' }]} />
      <SearchField value={query} onChange={setQuery} placeholder="Resource, project, commit, image or error" />
      <span className="spacer" />
      {total > PAGE && <span className="sv-pager-buttons">
        <span className="rp-row-meta">{page * PAGE + 1}–{Math.min(total, (page + 1) * PAGE)} of {total}</span>
        <Button icon={ChevronLeftIcon} aria-label="Previous page" disabled={!page} onClick={() => setPage(p => p - 1)} />
        <Button icon={ChevronRightIcon} aria-label="Next page" disabled={(page + 1) * PAGE >= total} onClick={() => setPage(p => p + 1)} />
      </span>}
    </div>
    <p className="sv-step-intro">Deployments belong to their resources; this is every deployment of a resource assigned to {server.name}. Retrying or cancelling uses the resource's deployment engine.</p>
    {error && <Notice tone="danger" title="Couldn't load deployments" action="Retry" onAction={refetch}>{error.message}</Notice>}
    {!list.length ? <div className="rp-empty"><RocketLaunchIcon /><strong>{loading && !data ? 'Loading…' : debounced || status ? 'No deployments match' : 'No deployments yet'}</strong>
      <p>{server.isLocal ? 'Deployments of resources on this server appear here.' : 'Remote servers don\'t receive deployments.'}</p></div>
      : <Table className="sv-table-wrap" columns={[{ label: 'Deployment' }, { label: 'Resource' }, { label: 'Status' }, { label: 'Trigger' }, { label: 'By' }, { label: 'Started' }, { label: 'Duration', className: 'num' }, { label: 'Commit / image' }]}>
        {list.map(d => <DeploymentRow key={d.id} d={d} open={open === d.id} onToggle={() => setOpen(open === d.id ? null : d.id)} dev={dev}
          onOpenResource={onOpenResource} setFeedback={setFeedback} refetch={refetch} />)}
      </Table>}
  </>;
}

function DeploymentRow({ d, open, onToggle, dev, onOpenResource, setFeedback, refetch }) {
  const s = depStatus[d.status] ?? depStatus.queued;
  const active = ['queued', 'building', 'deploying'].includes(d.status);
  const resource = { id: d.resourceId, projectId: d.projectId, name: d.resourceName };
  async function act(label, fn) {
    try { await fn(); setFeedback({ tone: 'success', title: `${label} started for ${d.resourceName}` }); refetch(); }
    catch (e) { setFeedback({ tone: 'danger', title: `${label} failed`, text: e.message }); }
  }
  return <>
    <tr className={cx('sv-click', open && 'selected')} onClick={onToggle} aria-expanded={open}>
      <td><span className="mono">#{d.number}</span> <span className="muted mono">{d.id.slice(0, 8)}</span></td>
      <td><span className="resource-name">{d.resourceName}<span>{d.projectName} · {d.environment}</span></span></td>
      <td><Status tone={s.tone}>{s.label}</Status></td>
      <td className="muted">{d.trigger}</td>
      <td className="muted sv-ellipsis">{d.author}</td>
      <td className="muted" title={new Date(d.createdAt).toLocaleString()}>{timeAgo(d.createdAt)}</td>
      <td className="num muted">{d.startedAt ? duration(d.startedAt, d.finishedAt) : '—'}</td>
      <td className="mono sv-ellipsis" title={d.imageId || d.imageRef}>{d.commit ? d.commit.slice(0, 7) : d.imageRef || '—'}</td>
    </tr>
    {open && <tr className="sv-detail-row"><td colSpan={8}>
      <div className="sv-detail">
        {d.error && <Notice tone="danger" title="Failure reason">{d.error}</Notice>}
        <dl className="sv-facts">
          <div><dt>Started</dt><dd>{d.startedAt ? new Date(d.startedAt).toLocaleString() : 'Not started'}</dd></div>
          <div><dt>Finished</dt><dd>{d.finishedAt ? new Date(d.finishedAt).toLocaleString() : active ? 'In progress' : '—'}</dd></div>
          <div><dt>Image</dt><dd className="mono">{d.imageRef || '—'}</dd></div>
          {d.imageId && <div><dt>Image digest</dt><dd className="mono">{d.imageId.replace('sha256:', '').slice(0, 24)}…</dd></div>}
          {d.reason && <div><dt>Reason</dt><dd>{d.reason}</dd></div>}
          <div><dt>Current release</dt><dd>{d.current ? 'Yes' : 'No'}</dd></div>
        </dl>
        <DeploymentLog id={d.id} active={active} />
        <div className="sv-detail-actions">
          <Button icon={ArrowTopRightOnSquareIcon} onClick={() => onOpenResource(resource, 'Deployments')}>Open in resource</Button>
          {dev && d.status === 'failed' && <Button icon={ArrowPathIcon} onClick={() => act('Retry', () => api.deploy(d.resourceId, d.imageRef ? { from: d.id, reason: `Retry of #${d.number}` } : { reason: `Retry of #${d.number}` }))}>Retry</Button>}
          {dev && active && <Button icon={XMarkIcon} onClick={() => act('Cancel', () => api.cancelDeployment(d.id))}>Cancel</Button>}
        </div>
      </div>
    </td></tr>}
  </>;
}

function DeploymentLog({ id, active }) {
  const { data, error } = useQuery(() => api.deploymentLogs(id), [id], { pollMs: active ? 2000 : 0 });
  const logs = data?.logs ?? [];
  if (error) return <p className="rp-error">Couldn't load the build log: {error.message}</p>;
  if (!data) return <p className="muted">Loading log…</p>;
  if (!logs.length) return <p className="muted">No output was recorded.</p>;
  return <pre className="sv-pre sv-log">{logs.slice(-200).map(l => `${new Date(l.createdAt).toLocaleTimeString()}  ${l.step ? `[${l.step}] ` : ''}${l.message}`).join('\n')}</pre>;
}

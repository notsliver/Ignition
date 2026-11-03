import { useState } from 'react';
import { CircleStackIcon, CloudArrowUpIcon, FolderPlusIcon, ServerIcon } from '@heroicons/react/20/solid';
import { ArrowRightIcon, ClipboardDocumentIcon, DocumentTextIcon, EllipsisHorizontalIcon, ExclamationTriangleIcon, RocketLaunchIcon, XCircleIcon } from '@heroicons/react/16/solid';
import { Avatar, BranchIcon, CommitIcon, Menu, Notice, Section, Sparkline, Status, cx } from '../../ui';
import { api } from '../../api/client';
import { duration, timeAgo, useQuery } from '../../api/hooks';
import { databaseOptions, deployOptions, serverOptions } from '../../app/createMenus';
import { useProjectCreation } from '../Projects/Projects';
import { useCanCreateResources, useProjectsEnabled } from '../../app/instance';
import { ResourceQuickCreate } from './ResourceQuickCreate';
import '../project/Deployments/Deployments.css';
import './Dashboard.css';
import { useInfrastructureAdminOnly } from '../../app/instance';

/*
 * The dashboard: what needs attention, the numbers that matter, the latest deployments across every
 * resource (same rows as a resource's Deployments page) and this machine's usage over the last hour.
 * Everything is live; anything Ignition doesn't measure says so instead of showing a number.
 */

const statusInfo = {
  queued: { tone: 'neutral', label: 'Queued' }, building: { tone: 'warning', label: 'Building' }, deploying: { tone: 'progress', label: 'Deploying' },
  successful: { tone: 'success', label: 'Successful' }, failed: { tone: 'danger', label: 'Failed' }, cancelled: { tone: 'neutral', label: 'Cancelled' },
};
const active = s => s === 'queued' || s === 'building' || s === 'deploying';
const triggerLabel = { manual: 'Manual', create: 'First deploy', redeploy: 'Redeploy', rollback: 'Rollback', retry: 'Retry', config: 'Config change', hook: 'Deploy hook', token: 'API token', automation: 'Automation' };
const percent = v => `${v.toFixed(1)}%`;
const gb = b => b >= 1 << 30 ? `${(b / (1 << 30)).toFixed(1)} GB` : `${Math.round(b / (1 << 20))} MB`;

export function Dashboard({ onOpenResource, onOpenProject, onOpenServer, onNavigate }) {
  // With infrastructure managed in Instance administration, the dashboard (a team page) shows the team's
  // own resources, even to platform admins: servers are in Instance administration › Infrastructure.
  const infraAdmin = useInfrastructureAdminOnly();
  const serversHere = !infraAdmin;
  const [resourceAction, setResourceAction] = useState(null);
  const { data, error, refetch } = useQuery(() => api.dashboard(), [], { pollMs: 5000 });
  const projectsOn = useProjectsEnabled();
  const canCreate = useCanCreateResources();
  const projectsQuery = useQuery(() => api.projects(), []);
  const projectCreation = useProjectCreation({ projects: projectsQuery.data?.projects ?? [],
    onCreated: p => { projectsQuery.refetch(); refetch(); onOpenProject?.(p); } });
  const resourceMenu = (items, prefix) => items.map(item => item === 'separator' ? item
    : item.items ? { ...item, items: resourceMenu(item.items, `${prefix}${item.label}/`) }
      : { ...item, onSelect: () => setResourceAction({ path: `${prefix}${item.label}`, label: item.label }) });
  const localId = data?.localServer?.id;
  const metrics = useQuery(() => (serversHere && localId ? api.serverMetrics(localId, '1h') : Promise.resolve(null)), [localId, serversHere], { pollMs: 30000 });
  // Without access to the servers, show what this team's own resources use instead of the whole host.
  const usage = useQuery(() => (serversHere ? Promise.resolve(null) : api.teamUsage('1h')), [serversHere], { pollMs: 30000 });

  const open = link => link.kind === 'server'
    ? onOpenServer?.({ id: link.serverId }, link.view)
    : onOpenResource?.({ id: link.resourceId, projectId: link.projectId }, link.view);

  const st = data?.stats;
  const stats = st ? [
    projectsOn
      ? { label: 'Projects', value: st.projects, detail: st.resources ? `${st.resources} resource${st.resources === 1 ? '' : 's'}` : 'No resources yet', go: () => onNavigate?.('Projects') }
      : { label: 'Resources', value: st.resources, detail: st.resources ? 'Across your team' : 'No resources yet', go: () => onNavigate?.('Resources') },
    { label: 'Running resources', value: st.running, detail: [st.failed && `${st.failed} failed`, st.resources - st.running - st.failed > 0 && `${st.resources - st.running - st.failed} not running`].filter(Boolean).join(' · ') || (st.resources ? 'All running' : '—') },
    { label: 'Servers', value: `${st.serversOnline} / ${st.servers}`, detail: st.serversOnline === st.servers ? 'All connected' : `${st.servers - st.serversOnline} not connected`, go: serversHere ? () => onNavigate?.('Servers') : undefined },
    { label: 'Deployments, 24 h', value: st.deployments24h, detail: st.activeDeployments ? `${st.activeDeployments} in progress` : st.deployments24h ? `${st.deployments24h - st.failedDeployments24h} successful · ${st.failedDeployments24h} failed` : 'None today' },
  ] : [];

  return <div className="dashboard">
    <div className="quick-actions">
      {canCreate && <Menu className="button primary" label="Deploy application" items={resourceMenu(deployOptions, 'Application/')} align="start"><CloudArrowUpIcon />Deploy application</Menu>}
      {projectsOn && <Menu className="button" label="New project" items={projectCreation.items} align="start"><FolderPlusIcon />New project</Menu>}
      {canCreate && <Menu className="button" label="New database" items={resourceMenu(databaseOptions, 'Database/')} align="start"><CircleStackIcon />New database</Menu>}
      {serversHere && <Menu className="button" label="Add server" items={serverOptions} align="start"><ServerIcon />Add server</Menu>}
    </div>

    {error && !data && <Notice tone="danger" title="Couldn't load the dashboard">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice>}

    {data?.alerts.length > 0 && <div className="alerts" role="status">
      {data.alerts.map(alert => <div key={alert.title} className={cx('alert', alert.tone)}>
        {alert.tone === 'danger' ? <XCircleIcon /> : <ExclamationTriangleIcon />}
        <strong>{alert.title}</strong><span>{alert.detail}</span>
        <button className="alert-action" onClick={() => open(alert.link)}>{alert.action}</button>
      </div>)}
    </div>}

    <div className="stats">
      {(stats.length ? stats : Array.from({ length: 4 }, (_, i) => ({ label: ['Projects', 'Running resources', 'Servers', 'Deployments, 24 h'][i], value: '—', detail: 'Loading…' }))).map(stat =>
        <div key={stat.label} className="stat">
          <span className="stat-label">{stat.label}</span>
          <span className="stat-value">{stat.value}</span>
          <span className="stat-detail">{stat.detail}</span>
        </div>)}
    </div>

    <Section title="Recent deployments" count={data?.deployments.length} action={projectsOn ? 'All projects' : 'All resources'} onAction={() => onNavigate?.(projectsOn ? 'Projects' : 'Resources')}>
      {data && !data.deployments.length
        ? <div className="deployments-empty"><RocketLaunchIcon /><strong>No deployments yet</strong><p>They appear here when you deploy a resource, push to a connected branch or call a deploy hook.</p></div>
        : <ol className="deployment-list dashboard-deployments">
          {(data?.deployments ?? []).map(d => {
            const s = statusInfo[d.status] ?? statusInfo.queued;
            const timing = active(d.status) ? duration(d.startedAt ?? d.createdAt) : `${duration(d.startedAt, d.finishedAt)} (${timeAgo(d.createdAt)})`;
            const go = () => onOpenResource?.({ id: d.resourceId, projectId: d.projectId }, 'Deployments');
            const items = [
              { label: 'View deployment', icon: DocumentTextIcon, onSelect: go },
              { label: 'Open resource', icon: ArrowRightIcon, onSelect: () => onOpenResource?.({ id: d.resourceId, projectId: d.projectId }, 'Overview') },
              'separator',
              { label: 'Copy deployment ID', icon: ClipboardDocumentIcon, onSelect: () => navigator.clipboard?.writeText(d.id) },
              ...(d.commit ? [{ label: 'Copy commit SHA', icon: ClipboardDocumentIcon, onSelect: () => navigator.clipboard?.writeText(d.commit) }] : []),
            ];
            return <li key={d.id} className="deployment">
              <div className="deployment-row" role="button" tabIndex={0} onClick={go} onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); go(); } }}>
                <span className="deployment-id"><span>{d.resourceName}</span>{d.current && <span className="current-pill"><ArrowRightIcon />Current</span>}</span>
                <span className="deployment-env" title={`${d.projectName} · ${d.environment}`}>{d.projectName}</span>
                <span className="deployment-status"><Status tone={s.tone}>{s.label}</Status><span className="deployment-timing">{timing}</span></span>
                <span className="deployment-ref"><BranchIcon /><span>{d.branch || '—'}</span></span>
                <span className="deployment-ref commit"><CommitIcon /><code>{d.commit || `#${d.number}`}</code><span className="deployment-message">{d.error || d.reason || triggerLabel[d.trigger] || d.imageRef}</span></span>
                <span className="deployment-resource" title="Trigger">{triggerLabel[d.trigger] ?? d.trigger}</span>
                <span className="deployment-author" title={`By ${d.author}`}><Avatar name={d.author} /></span>
                <span onClick={e => e.stopPropagation()} onKeyDown={e => e.stopPropagation()}>
                  <Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${d.resourceName} #${d.number}`} items={items} />
                </span>
              </div>
            </li>;
          })}
        </ol>}
    </Section>

    {serversHere ? <Section title={data?.localServer ? `Server resources · ${data.localServer.name}` : 'Server resources'} action="All servers" onAction={() => onNavigate?.('Servers')}>
      <ServerUsage m={metrics.data} />
    </Section> : <Section title="Your team's resources" action="Monitor" onAction={() => onNavigate?.('Monitor')}>
      <TeamUsage u={usage.data} error={usage.error} />
    </Section>}
    {projectCreation.dialogs}
    {resourceAction && <ResourceQuickCreate action={resourceAction} projectsQuery={projectsQuery} projectsOn={projectsOn} onClose={() => setResourceAction(null)}
      onCreateProject={() => { setResourceAction(null); projectCreation.createEmpty(); }}
      onCreated={r => { setResourceAction(null); refetch(); onOpenResource?.(r); }}
      onOpen={r => { setResourceAction(null); onOpenResource?.(r); }} />}
  </div>;
}

// The team's resources added together, one value per step: CPU and network summed, memory against
// the sum of their limits.
function TeamUsage({ u, error }) {
  const resources = u?.resources ?? [];
  const steps = u?.buckets?.length ?? 0;
  const sum = (key, i) => { let t = null; for (const r of resources) { const v = r.series?.[i]?.[key]; if (v != null) t = (t ?? 0) + v; } return t; };
  const series = key => Array.from({ length: steps }, (_, i) => sum(key, i));
  const latest = key => { let t = null; for (const r of resources) { const v = r.latest?.[key]; if (v != null) t = (t ?? 0) + v; } return t; };
  const limit = resources.reduce((n, r) => n + (r.status === 'running' ? r.memoryLimit || 0 : 0), 0);
  const running = resources.filter(r => r.status === 'running').length;
  const rate = v => v == null ? null : v >= 1 << 20 ? `${(v / (1 << 20)).toFixed(1)} MB/s` : `${Math.round(v / 1024)} KB/s`;
  const cpu = latest('cpu'), mem = latest('memory');
  const cards = [
    { label: 'Running', value: u ? `${running} / ${resources.length}` : null, detail: 'Resources in this team', data: [] },
    { label: 'CPU', value: cpu != null ? percent(cpu) : null, detail: 'Of one core, added up', data: series('cpu'), format: percent },
    { label: 'Memory', value: mem != null ? gb(mem) : null, detail: limit ? `of ${gb(limit)} in limits` : '', data: series('memory') },
    { label: 'Network in', value: rate(latest('rxBps')), detail: 'Last hour', data: series('rxBps') },
    { label: 'Network out', value: rate(latest('txBps')), detail: 'Last hour', data: series('txBps') },
  ];
  if (error && !u) return <p className="stat-detail">{error.message}</p>;
  return <div className="resources">
    {cards.map(c => {
      const has = c.value != null;
      return <div key={c.label} className={cx('resource', !has && 'unmeasured')}>
        <span className="stat-label">{c.label}</span>
        <span className="resource-value">{has ? c.value : '—'}</span>
        <span className="stat-detail">{!u ? 'Loading…' : has ? c.detail : running ? 'Waiting for samples' : 'Nothing running'}</span>
        {c.data.some(v => v != null) && <Sparkline data={c.data} label={c.label} format={c.format} />}
      </div>;
    })}
  </div>;
}

function ServerUsage({ m }) {
  const buckets = m?.buckets ?? [];
  const series = key => buckets.map(b => b[key]);
  const pctSeries = (used, total) => buckets.map(b => b[used] != null && b[total] ? (b[used] / b[total]) * 100 : null);
  const last = m?.latest;
  const why = key => m?.support?.[key] === 'supported' ? null : m?.reasons?.[key] ?? (m?.collectorState === 'disabled' ? 'Metrics collection is off for this server.' : 'Not measured yet.');
  const cap = m?.capacity;
  const cards = [
    { label: 'CPU', key: 'cpu', value: last?.cpu != null ? percent(last.cpu) : null, detail: last?.load1 != null ? `${cap?.ncpu ? `${cap.ncpu} cores · ` : ''}load ${[last.load1, last.load5, last.load15].map(v => v?.toFixed(2)).join(', ')}` : '', data: series('cpu'), max: 100, format: percent },
    { label: 'Memory', key: 'memory', value: last?.memoryUsed != null && last?.memoryTotal ? percent((last.memoryUsed / last.memoryTotal) * 100) : null, detail: last?.memoryTotal ? `${gb(last.memoryUsed)} / ${gb(last.memoryTotal)}` : '', data: pctSeries('memoryUsed', 'memoryTotal'), max: 100, format: percent },
    { label: 'Disk', key: 'disk', value: last?.diskUsed != null && last?.diskTotal ? percent((last.diskUsed / last.diskTotal) * 100) : null, detail: last?.diskTotal ? `${gb(last.diskUsed)} / ${gb(last.diskTotal)}` : '', data: pctSeries('diskUsed', 'diskTotal'), max: 100, format: percent },
    { label: 'Network in', key: 'network', value: null, detail: '', data: [] },
    { label: 'Network out', key: 'network', value: null, detail: '', data: [] },
  ];
  return <div className="resources">
    {cards.map(c => {
      const reason = why(c.key);
      const has = !reason && c.value != null && c.data.some(v => v != null);
      return <div key={c.label} className={cx('resource', !has && 'unmeasured')}>
        <span className="stat-label">{c.label}</span>
        <span className="resource-value">{has ? c.value : '—'}</span>
        <span className="stat-detail" title={reason ?? c.detail}>{has ? c.detail || 'Last hour' : !m ? 'Loading…' : reason ? 'Not measured' : 'Waiting for samples'}</span>
        {has ? <Sparkline data={c.data} max={c.max} label={c.label} format={c.format} /> : m && reason && <p className="resource-why">{reason}</p>}
      </div>;
    })}
  </div>;
}

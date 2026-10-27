import { CircleStackIcon, CloudArrowUpIcon, FolderPlusIcon, ServerIcon } from '@heroicons/react/20/solid';
import { ArrowRightIcon, ClipboardDocumentIcon, DocumentTextIcon, EllipsisHorizontalIcon, ExclamationTriangleIcon, RocketLaunchIcon, XCircleIcon } from '@heroicons/react/16/solid';
import { Avatar, BranchIcon, CommitIcon, Menu, Notice, Section, Sparkline, Status, cx } from '../../ui';
import { api } from '../../api/client';
import { duration, timeAgo, useQuery } from '../../api/hooks';
import { databaseOptions, deployOptions, projectOptions, serverOptions } from '../../app/createMenus';
import '../project/Deployments/Deployments.css';
import './Dashboard.css';

/*
 * The dashboard: what needs attention, the numbers that matter, the latest deployments across every
 * resource (same rows as a resource's Deployments page) and this machine's usage over the last hour.
 * Everything is live; anything IGNITION doesn't measure says so instead of showing a number.
 */

const statusInfo = {
  queued: { tone: 'neutral', label: 'Queued' }, building: { tone: 'warning', label: 'Building' }, deploying: { tone: 'progress', label: 'Deploying' },
  successful: { tone: 'success', label: 'Successful' }, failed: { tone: 'danger', label: 'Failed' }, cancelled: { tone: 'neutral', label: 'Cancelled' },
};
const active = s => s === 'queued' || s === 'building' || s === 'deploying';
const triggerLabel = { manual: 'Manual', create: 'First deploy', redeploy: 'Redeploy', rollback: 'Rollback', retry: 'Retry', config: 'Config change', hook: 'Deploy hook', token: 'API token', automation: 'Automation' };
const percent = v => `${v.toFixed(1)}%`;
const gb = b => b >= 1 << 30 ? `${(b / (1 << 30)).toFixed(1)} GB` : `${Math.round(b / (1 << 20))} MB`;

export function Dashboard({ onOpenResource, onOpenServer, onNavigate }) {
  const { data, error, refetch } = useQuery(() => api.dashboard(), [], { pollMs: 5000 });
  const localId = data?.localServer?.id;
  const metrics = useQuery(() => (localId ? api.serverMetrics(localId, '1h') : Promise.resolve(null)), [localId], { pollMs: 30000 });

  const open = link => link.kind === 'server'
    ? onOpenServer?.({ id: link.serverId }, link.view)
    : onOpenResource?.({ id: link.resourceId, projectId: link.projectId }, link.view);

  const st = data?.stats;
  const stats = st ? [
    { label: 'Projects', value: st.projects, detail: st.resources ? `${st.resources} resource${st.resources === 1 ? '' : 's'}` : 'No resources yet', go: () => onNavigate?.('Projects') },
    { label: 'Running resources', value: st.running, detail: [st.failed && `${st.failed} failed`, st.resources - st.running - st.failed > 0 && `${st.resources - st.running - st.failed} not running`].filter(Boolean).join(' · ') || (st.resources ? 'All running' : '—') },
    { label: 'Servers', value: `${st.serversOnline} / ${st.servers}`, detail: st.serversOnline === st.servers ? 'All connected' : `${st.servers - st.serversOnline} not connected`, go: () => onNavigate?.('Servers') },
    { label: 'Deployments, 24 h', value: st.deployments24h, detail: st.activeDeployments ? `${st.activeDeployments} in progress` : st.deployments24h ? `${st.deployments24h - st.failedDeployments24h} successful · ${st.failedDeployments24h} failed` : 'None today' },
  ] : [];

  return <div className="dashboard">
    <div className="quick-actions">
      <Menu className="button primary" label="Deploy application" items={deployOptions} align="start"><CloudArrowUpIcon />Deploy application</Menu>
      <Menu className="button" label="New project" items={projectOptions} align="start"><FolderPlusIcon />New project</Menu>
      <Menu className="button" label="New database" items={databaseOptions} align="start"><CircleStackIcon />New database</Menu>
      <Menu className="button" label="Add server" items={serverOptions} align="start"><ServerIcon />Add server</Menu>
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

    <Section title="Recent deployments" count={data?.deployments.length} action="All projects" onAction={() => onNavigate?.('Projects')}>
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

    <Section title={data?.localServer ? `Server resources · ${data.localServer.name}` : 'Server resources'} action="All servers" onAction={() => onNavigate?.('Servers')}>
      <ServerUsage m={metrics.data} />
    </Section>
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

import { ArrowPathIcon, ArrowRightIcon, CheckCircleIcon, DocumentTextIcon, ExclamationTriangleIcon, InformationCircleIcon, NoSymbolIcon, PlayIcon, SignalIcon, XCircleIcon } from '@heroicons/react/16/solid';
import { Button, Meter, Status } from '../../ui';
import { api } from '../../api/client';
import { timeAgo, useQuery } from '../../api/hooks';
import { bytes, connection, pct, rate, runtimeOf, uptime } from './model';

/*
 * Overview answers, in order: is it reachable, is Docker ready, can it take deployments, what runs
 * here, is anything wrong, and what can I do. Every number is a current observation or says why
 * it isn't there.
 */

const eventIcon = { info: CheckCircleIcon, warn: ExclamationTriangleIcon, error: XCircleIcon };
const fresh = (at, server) => at && server.available && Date.now() - new Date(at) < 90_000;

export function Overview({ server, onNavigate, actions }) {
  const metrics = useQuery(() => api.serverMetrics(server.id, '15m'), [server.id], { pollMs: 15000 });
  const resources = useQuery(() => api.serverResources(server.id), [server.id], { pollMs: 10000 });
  const events = useQuery(() => api.serverEvents(server.id, { limit: 8 }), [server.id], { pollMs: 10000 });
  const failures = useQuery(() => api.serverDeployments(server.id, { status: 'failed', limit: 4 }), [server.id], { pollMs: 20000 });
  const diag = useQuery(() => api.serverDiagnostics(server.id), [server.id], { pollMs: 30000 });

  const engine = server.info?.engine;
  const latest = metrics.data?.latest;
  const live = fresh(latest?.at, server) ? latest : null;
  const support = metrics.data?.support ?? {};
  const reasons = metrics.data?.reasons ?? {};
  const list = resources.data?.resources ?? [];
  const health = resources.data?.health ?? {};
  const counts = {
    running: list.filter(r => r.status === 'running').length,
    stopped: list.filter(r => r.status === 'stopped' || r.status === 'created').length,
    unhealthy: list.filter(r => r.status === 'failed' || health[r.id] === 'unhealthy').length,
  };
  const latestDiag = diag.data?.latest ?? [];
  const diagFailed = latestDiag.filter(d => d.status === 'failed');
  const diagWarn = latestDiag.filter(d => d.status === 'warning');
  const conn = connection(server);
  const rt = runtimeOf(server);
  const elig = server.deploymentEligibility;

  const notMeasured = key => !server.available ? 'Not current: the server isn\'t reachable.' : support[key] === 'unsupported' ? (reasons[key] ?? 'Not measured on this server.')
    : support[key] === 'disabled' ? 'Collection is off in Settings.' : 'Waiting for the first sample.';

  const cpuPct = live?.cpu != null ? Math.round(live.cpu) : null;
  const memPct = live ? pct(live.memoryUsed, live.memoryTotal) : null;
  const diskPct = live ? pct(live.diskUsed, live.diskTotal) : null;
  const facts = server.hostFacts ?? {};
  const collector = { probe: 'Host probe (/proc on the host)', procfs: 'This host\'s /proc', engine: 'Docker Engine API only' }[server.info?.collector] ?? '—';

  return <>
    <section className="ov-hero">
      <div className="ov-status">
        <span className="ov-label">Server status</span>
        <Status tone={conn.tone}>{conn.label}{server.available ? ' and ready' : ''}</Status>
        <dl>
          <div><dt>Reachable</dt><dd className={server.connectionState === 'offline' ? 'bad' : undefined}>{server.connectionState === 'online' || server.connectionState === 'degraded' ? `Yes, ${timeAgo(server.lastCheckAt).toLowerCase()}` : server.connectionState === 'unknown' ? 'Unknown (stale)' : 'No'}</dd></div>
          <div><dt>Docker</dt><dd className={rt.tone === 'danger' ? 'bad' : undefined}>{rt.label}{engine?.version && rt.label === 'Ready' ? ` · ${engine.version}` : ''}</dd></div>
          <div><dt>New deployments</dt><dd title={elig.reason}>{server.maintenance ? 'Paused (maintenance)' : elig.eligible ? 'Accepted' : 'Not accepted'}</dd></div>
          <div><dt>Workloads</dt><dd>{server.workloads}</dd></div>
        </dl>
        <p className="sv-reason">{elig.reason}</p>
      </div>
      <div className="ov-actions">
        <span className="ov-label">Quick actions</span>
        <div>
          {actions.dev && <Button icon={SignalIcon} onClick={actions.test} disabled={!!actions.busy}>{actions.busy === 'Test connection' ? 'Testing…' : 'Test connection'}</Button>}
          {actions.dev && <Button icon={ArrowPathIcon} onClick={actions.refresh}>Refresh</Button>}
          <Button icon={DocumentTextIcon} onClick={() => onNavigate('Logs')}>Logs</Button>
          {actions.admin && server.isLocal && <Button icon={server.deploymentsEnabled ? NoSymbolIcon : PlayIcon} onClick={actions.toggleDeployments}>{server.deploymentsEnabled ? 'Disable deployments' : 'Enable deployments'}</Button>}
          <Button variant="primary" onClick={() => onNavigate('Resources')}>View resources<ArrowRightIcon /></Button>
        </div>
      </div>
    </section>

    <section className="ov-usage sv-usage5" aria-label="Utilisation">
      <button type="button" onClick={() => onNavigate('Monitoring')}>
        <span>CPU</span><strong>{cpuPct != null ? `${cpuPct}%` : '—'}</strong>
        <small>{engine?.ncpu ? `${engine.ncpu} cores` : ''}{live?.load1 != null ? ` · load ${live.load1.toFixed(2)}` : ''}{cpuPct == null ? ` ${notMeasured('cpu')}` : ''}</small>
        {cpuPct != null && <Meter label="CPU" value={Math.min(100, cpuPct)} compact />}
      </button>
      <button type="button" onClick={() => onNavigate('Monitoring')}>
        <span>Memory</span><strong>{memPct != null ? `${memPct}%` : engine?.memTotal ? bytes(engine.memTotal) : '—'}</strong>
        <small>{memPct != null ? `${bytes(live.memoryUsed)} of ${bytes(live.memoryTotal)}` : engine?.memTotal ? `Total. ${notMeasured('memory')}` : notMeasured('memory')}</small>
        {memPct != null && <Meter label="Memory" value={memPct} compact />}
      </button>
      <button type="button" onClick={() => onNavigate('Storage')}>
        <span>Disk</span><strong>{diskPct != null ? `${diskPct}%` : '—'}</strong>
        <small>{diskPct != null ? `${bytes(live.diskTotal - live.diskUsed)} free of ${bytes(live.diskTotal)}` : notMeasured('disk')}</small>
        {diskPct != null && <Meter label="Disk" value={diskPct} compact />}
      </button>
      <button type="button" onClick={() => onNavigate('Monitoring')}>
        <span>Uptime</span><strong>{live?.uptimeSeconds != null ? uptime(live.uptimeSeconds) : '—'}</strong>
        <small>{live?.containersRunning != null ? `${live.containersRunning} containers running` : notMeasured('uptime')}</small>
      </button>
      <button type="button" onClick={() => onNavigate('Monitoring')}>
        <span>Network</span><strong>{live?.netRxBps != null ? rate(live.netRxBps + (live.netTxBps ?? 0)) : '—'}</strong>
        <small>{live?.netRxBps != null ? `in ${rate(live.netRxBps)} · out ${rate(live.netTxBps)}` : notMeasured('network')}</small>
      </button>
    </section>

    <div className="ov-columns">
      <div className="ov-stack">
        <section className="ov-card">
          <header><h2>Workloads</h2><button type="button" className="ov-link" onClick={() => onNavigate('Resources')}>All resources<ArrowRightIcon /></button></header>
          {resources.error ? <p className="ov-empty">Couldn't load workloads: {resources.error.message}</p>
            : !resources.data ? <p className="ov-empty">Loading…</p>
              : !list.length ? <p className="ov-empty">{server.isLocal ? 'No resources yet. Add one from a project; it will run here.' : 'Remote servers don\'t host IGNITION resources. Their containers are on the Docker page.'}</p>
                : <div className="sv-counts">
                  <div><strong>{list.length}</strong><span>assigned</span></div>
                  <div><strong>{counts.running}</strong><span>running</span></div>
                  <div><strong>{counts.stopped}</strong><span>stopped</span></div>
                  <div className={counts.unhealthy ? 'bad' : undefined}><strong>{counts.unhealthy}</strong><span>failed or unhealthy</span></div>
                </div>}
        </section>

        <section className="ov-card">
          <header><h2>Recent deployment failures</h2><button type="button" className="ov-link" onClick={() => onNavigate('Deployments')}>Deployments<ArrowRightIcon /></button></header>
          {(failures.data?.deployments ?? []).length ? <ol className="ov-events">
            {failures.data.deployments.map(d => <li key={d.id}><XCircleIcon className="warning" /><span><strong>{d.resourceName} #{d.number}</strong> · {d.error || 'failed'}</span><time>{timeAgo(d.createdAt)}</time></li>)}
          </ol> : <p className="ov-empty">{failures.data ? 'No failed deployments.' : 'Loading…'}</p>}
        </section>

        <section className="ov-card">
          <header><h2>Recent events</h2><button type="button" className="ov-link" onClick={() => onNavigate('Logs')}>All events<ArrowRightIcon /></button></header>
          {(events.data?.events ?? []).length ? <ol className="ov-events">
            {events.data.events.map(e => { const Icon = eventIcon[e.level] ?? InformationCircleIcon; return <li key={e.id}>
              <Icon className={e.level === 'info' ? 'success' : 'warning'} /><span>{e.message}</span><time>{timeAgo(e.at)}</time>
            </li>; })}
          </ol> : <p className="ov-empty">{events.data ? 'No events yet. Connection changes, maintenance and operations appear here.' : 'Loading…'}</p>}
        </section>
      </div>

      <div className="ov-stack">
        <section className="ov-card">
          <header><h2>Host</h2><button type="button" className="ov-link" onClick={() => onNavigate('Docker')}>Docker<ArrowRightIcon /></button></header>
          {engine ? <dl className="sv-facts">
            <div><dt>Operating system</dt><dd>{engine.os || '—'}</dd></div>
            <div><dt>Architecture</dt><dd>{engine.arch} · {engine.ncpu} CPUs{facts.sockets > 1 ? ` · ${facts.sockets} sockets` : ''}</dd></div>
            {facts.cpuModel && <div><dt>Processor</dt><dd title={facts.cpuModel}>{facts.cpuModel}</dd></div>}
            <div><dt>Kernel</dt><dd className="mono">{engine.kernel}</dd></div>
            <div><dt>Memory</dt><dd>{bytes(engine.memTotal)}</dd></div>
            <div><dt>Docker</dt><dd>{engine.version} · API {engine.apiVersion}</dd></div>
            <div><dt>Storage driver</dt><dd>{engine.storageDriver}</dd></div>
            <div><dt>Hostname</dt><dd>{engine.name}</dd></div>
            {server.location && <div><dt>Location</dt><dd>{server.location}</dd></div>}
            {facts.bootedAt && <div><dt>Booted</dt><dd title={new Date(facts.bootedAt).toLocaleString()}>{timeAgo(facts.bootedAt)}</dd></div>}
            {facts.interfaces?.length > 0 && <div><dt>Network interfaces</dt><dd className="mono">{facts.interfaces.join(', ')}</dd></div>}
            <div><dt>Metrics source</dt><dd>{collector}</dd></div>
            <div><dt>Detected</dt><dd>{server.info?.checkedAt ? timeAgo(server.info.checkedAt) : '—'}</dd></div>
          </dl> : <p className="ov-empty">Not detected yet: no successful check has read the engine.</p>}
        </section>

        <section className="ov-card">
          <header><h2>Diagnostics</h2><button type="button" className="ov-link" onClick={() => onNavigate('Diagnostics')}>Open<ArrowRightIcon /></button></header>
          {!latestDiag.length ? <p className="ov-empty">{diag.data ? 'No checks have run yet.' : 'Loading…'}</p>
            : !diagFailed.length && !diagWarn.length ? <p className="ov-empty sv-ok"><CheckCircleIcon />All {latestDiag.length} recent checks passed or don't apply.</p>
              : <ol className="ov-events">
                {[...diagFailed, ...diagWarn].slice(0, 5).map(d => <li key={d.check}>
                  {d.status === 'failed' ? <XCircleIcon className="warning" /> : <ExclamationTriangleIcon className="warning" />}<span>{d.summary}</span><time>{timeAgo(d.ranAt)}</time>
                </li>)}
              </ol>}
        </section>
      </div>
    </div>
  </>;
}

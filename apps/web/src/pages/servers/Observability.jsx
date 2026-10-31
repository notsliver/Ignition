import { useEffect, useMemo, useRef, useState } from 'react';
import {
  ArrowDownTrayIcon, BeakerIcon, CheckCircleIcon, ChevronDownIcon, ChevronRightIcon, ClipboardDocumentIcon, ExclamationTriangleIcon,
  MinusCircleIcon, PauseIcon, PlayIcon, XCircleIcon,
} from '@heroicons/react/16/solid';
import { Button, Checkbox, Notice, SearchField, SegmentedControl, Select, Sparkline, Status, cx } from '../../ui';
import { api } from '../../api/client';
import { timeAgo, useQuery } from '../../api/hooks';
import { useSession, can } from '../../app/AuthGate';
import { bytes, download, downloadText, isAdmin, isDeveloper, rate, uptime } from './model';

/* --------------------------------- Monitoring --------------------------------- */

const ranges = ['15m', '1h', '6h', '24h', '7d'].map(v => ({ value: v, label: v }));
const pctFmt = v => `${v.toFixed(1)}%`;

function stamp(iso, range) {
  const d = new Date(iso);
  return range === '7d' || range === '24h' ? d.toLocaleString(undefined, { weekday: 'short', hour: '2-digit', minute: '2-digit' }) : d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
}

const collectorNotice = {
  disabled: { tone: 'info', title: 'Collection is off', text: 'Host samples aren\'t being recorded for this server. Turn collection on in Settings.' },
  stale: { tone: 'warning', title: 'Data is stale', text: 'No sample has arrived recently. The worker may be stopped or the server unreachable; the latest values below aren\'t current.' },
  unavailable: { tone: 'warning', title: 'Collector unavailable', text: 'The server isn\'t reachable or Docker isn\'t ready, so nothing new is being sampled.' },
  waiting: { tone: 'info', title: 'No data yet', text: 'The worker samples every 15 seconds. The first points appear within a minute.' },
};

export function Monitoring({ server }) {
  const [range, setRange] = useState('1h');
  const [live, setLive] = useState(true);
  const { data, error, refetch } = useQuery(() => api.serverMetrics(server.id, range), [server.id, range], { pollMs: live ? 15000 : 0 });
  const buckets = data?.buckets ?? [];
  const support = data?.support ?? {};
  const reasons = data?.reasons ?? {};
  const labels = buckets.map(b => stamp(b.at, range));
  const latest = data?.latest;
  const current = latest && data?.collectorState === 'ok';
  const pick = k => buckets.map(b => b[k] ?? null);
  const memTotal = Math.max(0, ...buckets.map(b => b.memoryTotal ?? 0)) || data?.capacity?.memTotal || null;
  const diskTotal = Math.max(0, ...buckets.map(b => b.diskTotal ?? 0)) || null;
  const swapTotal = Math.max(0, ...buckets.map(b => b.swapTotal ?? 0));

  const charts = [
    { key: 'cpu', family: 'cpu', label: 'CPU', unit: 'of all cores', data: pick('cpu'), max: 100, format: pctFmt, now: latest?.cpu },
    { key: 'load', family: 'load', label: 'Load average (1 min)', unit: data?.capacity?.ncpu ? `${data.capacity.ncpu} cores` : '', data: pick('load1'), format: v => v.toFixed(2), now: latest?.load1,
      extra: latest?.load5 != null ? `5 min ${latest.load5.toFixed(2)} · 15 min ${latest.load15.toFixed(2)}` : null },
    { key: 'memory', family: 'memory', label: 'Memory used', unit: memTotal ? `of ${bytes(memTotal)}` : '', data: pick('memoryUsed'), max: memTotal ? memTotal * 1.02 : undefined, limit: memTotal, format: bytes, now: latest?.memoryUsed },
    { key: 'swap', family: 'swap', label: 'Swap used', unit: swapTotal ? `of ${bytes(swapTotal)}` : 'no swap configured', data: pick('swapUsed'), format: bytes, now: latest?.swapUsed, hide: support.swap === 'supported' && !swapTotal },
    { key: 'disk', family: 'disk', label: 'Disk used', unit: diskTotal ? `of ${bytes(diskTotal)} (Docker's filesystem)` : '', data: pick('diskUsed'), max: diskTotal ? diskTotal * 1.02 : undefined, limit: diskTotal, format: bytes, now: latest?.diskUsed },
    { key: 'diskio', family: 'diskio', label: 'Disk I/O', unit: 'read + write', data: buckets.map(b => b.diskReadBps != null ? b.diskReadBps + (b.diskWriteBps ?? 0) : null), format: rate,
      now: latest?.diskReadBps != null ? latest.diskReadBps + (latest.diskWriteBps ?? 0) : null, extra: latest?.diskReadBps != null ? `read ${rate(latest.diskReadBps)} · write ${rate(latest.diskWriteBps)}` : null },
    { key: 'network', family: 'network', label: 'Network throughput', unit: 'in + out, physical interfaces', data: buckets.map(b => b.netRxBps != null ? b.netRxBps + (b.netTxBps ?? 0) : null), format: rate,
      now: latest?.netRxBps != null ? latest.netRxBps + (latest.netTxBps ?? 0) : null, extra: latest?.netRxBps != null ? `in ${rate(latest.netRxBps)} · out ${rate(latest.netTxBps)}` : null },
    { key: 'containers', family: 'containers', label: 'Containers running', unit: 'all containers on the engine', data: pick('containersRunning'), format: v => Math.round(v).toString(), now: latest?.containersRunning },
    { key: 'wcpu', family: 'workloads', label: 'Workload CPU', unit: '% of one core, summed over Ignition workloads', data: pick('workloadCpu'), format: pctFmt, now: latest?.workloadCpu },
    { key: 'wmem', family: 'workloads', label: 'Workload memory', unit: 'summed over Ignition workloads', data: pick('workloadMemory'), format: bytes, now: latest?.workloadMemory },
  ].filter(c => !c.hide);

  function exportCsv() {
    const cols = ['at', 'samples', 'cpu', 'load1', 'load5', 'load15', 'memoryUsed', 'memoryTotal', 'swapUsed', 'diskUsed', 'diskTotal', 'diskReadBps', 'diskWriteBps', 'netRxBps', 'netTxBps', 'containersRunning', 'workloadCpu', 'workloadMemory'];
    downloadText(`${server.name}-host-metrics-${range}.csv`, [cols.join(','), ...buckets.map(b => cols.map(c => b[c] ?? '').join(','))].join('\n'), 'text/csv');
  }
  const notice = data && collectorNotice[data.collectorState];

  return <>
    <div className="rp-toolbar">
      <SegmentedControl label="Time range" options={ranges} value={range} onChange={setRange} className="auto-width" />
      <span className="spacer" />
      <span className="rp-row-meta">{latest ? `Last sample ${timeAgo(latest.at).toLowerCase()}` : 'No samples yet'}{data ? ` · every ${data.intervalSeconds} s · kept ${data.retentionDays} days` : ''}</span>
      <Button icon={live ? PauseIcon : PlayIcon} onClick={() => { setLive(l => !l); if (!live) refetch(); }}>{live ? 'Live' : 'Paused'}</Button>
      <Button icon={ArrowDownTrayIcon} onClick={exportCsv} disabled={!buckets.some(b => b.samples)}>CSV</Button>
    </div>
    {error && <Notice tone="danger" title="Couldn't load metrics">{error.message}</Notice>}
    {notice && <Notice tone={notice.tone} title={notice.title}>{notice.text}</Notice>}
    {data && <p className="sv-step-intro">Source: {data.collector === 'probe' ? 'the host probe container reading the host\'s /proc (host-wide figures, including network) and the Docker Engine' : data.collector === 'procfs' ? 'this host\'s /proc (host-wide figures) and the Docker Engine' : data.collector === 'engine' ? 'the Docker Engine API only' : 'not determined yet'}. Gaps mean no sample was taken; they're never drawn as zero.</p>}
    <div className="sv-charts">
      {charts.map(c => {
        const state = support[c.family];
        const values = c.data.filter(v => v != null);
        const body = state === 'unsupported' || state === 'disabled' || state === 'unknown'
          ? <p className="sv-chart-empty">{state === 'unsupported' ? 'Not supported here. ' : state === 'disabled' ? 'Not collected: collection is off. ' : 'Not known yet. '}{reasons[c.family] ?? ''}</p>
          : !values.length ? <p className="sv-chart-empty">{buckets.some(b => b.samples) ? 'Not collected in this period.' : 'No data yet.'}</p>
            : <Sparkline data={c.data} max={c.max} limit={c.limit} label={c.label} format={c.format} labels={labels} />;
        return <section key={c.key} className="sv-chart">
          <header>
            <h3>{c.label}</h3>
            <strong>{c.now != null && state !== 'unsupported' ? c.format(c.now) : '—'}</strong>
          </header>
          <small>{c.extra ?? c.unit}{c.now != null && !current && state !== 'unsupported' ? ' · not current' : ''}</small>
          {body}
        </section>;
      })}
    </div>
    {latest?.uptimeSeconds != null && <p className="monitor-footnote">Host uptime {uptime(latest.uptimeSeconds)} at the last sample.</p>}
  </>;
}

/* ------------------------------------ Logs ------------------------------------ */

const levelTone = { info: '', warn: 'warn', error: 'error' };
const logRanges = [{ value: '15m', label: 'Last 15 minutes' }, { value: '1h', label: 'Last hour' }, { value: '6h', label: 'Last 6 hours' }, { value: '24h', label: 'Last 24 hours' }, { value: '168h', label: 'Last 7 days' }, { value: '720h', label: 'Last 30 days' }];

export function ServerLogs({ server }) {
  const { user } = useSession();
  const [source, setSource] = useState('events');
  const [level, setLevel] = useState('');
  const [range, setRange] = useState('1h');
  const [query, setQuery] = useState('');
  const [debounced, setDebounced] = useState('');
  const [container, setContainer] = useState('');
  const [stream, setStream] = useState('');
  const [live, setLive] = useState(true);
  const [follow, setFollow] = useState(true);
  const [utc, setUtc] = useState(false);
  const [selected, setSelected] = useState(() => new Set());
  const [expanded, setExpanded] = useState(() => new Set());
  const [copied, setCopied] = useState(false);
  const scroller = useRef(null);
  useEffect(() => { const t = setTimeout(() => setDebounced(query.trim()), 300); return () => clearTimeout(t); }, [query]);
  useEffect(() => { setSelected(new Set()); setExpanded(new Set()); }, [source, container]);

  const containers = useQuery(() => api.serverContainers(server.id), [server.id], { enabled: source === 'container' });
  const ready = source !== 'container' || container;
  const params = { source, level, range, q: debounced, container: source === 'container' ? container : undefined, stream: source === 'container' ? stream : undefined, limit: 1000 };
  const { data, error, loading, refetch } = useQuery(() => api.serverLogs(server.id, params), [server.id, source, level, range, debounced, container, stream], { pollMs: live && ready ? 3000 : 0, enabled: !!ready });
  const lines = useMemo(() => [...(data?.lines ?? [])].reverse(), [data]); // oldest first, like a terminal
  const sources = data?.sources ?? [{ id: 'events', label: 'Server events', available: true }, { id: 'jobs', label: 'Operations and deployments', available: true }, { id: 'container', label: 'Container logs', available: true }, { id: 'daemon', label: 'Docker daemon', available: false }, { id: 'agent', label: 'Agent', available: false }];
  const current = sources.find(s => s.id === source);

  useEffect(() => { if (follow && scroller.current) scroller.current.scrollTop = scroller.current.scrollHeight; }, [lines, follow]);
  const fmtTime = t => utc ? new Date(t).toISOString().replace('T', ' ').slice(0, 23) : new Date(t).toLocaleString(undefined, { month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });
  const text = list => list.map(l => `${fmtTime(l.time)} ${l.level.toUpperCase().padEnd(5)} [${l.source}${l.stream ? `/${l.stream}` : ''}] ${l.message}`).join('\n');
  const keyOf = (l, i) => `${l.cursor}-${i}`;
  function copySelected() {
    const list = lines.filter((l, i) => selected.has(keyOf(l, i)));
    navigator.clipboard?.writeText(text(list.length ? list : lines));
    setCopied(true);
    setTimeout(() => setCopied(false), 1200);
  }
  const containerOptions = (containers.data?.containers ?? []).map(c => ({ value: c.id, label: c.name, description: `${c.owner.kind === 'resource' ? `${c.owner.resource.name} · ` : c.owner.kind === 'unmanaged' ? 'not managed by Ignition · ' : ''}${c.state}` }));

  return <>
    <div className="rp-toolbar">
      <Select label="Log source" value={source} onChange={setSource} className="sv-select" options={sources.map(s => ({ value: s.id, label: s.label, description: s.available ? s.detail : `Unavailable: ${s.detail}` }))} />
      {source === 'container' && <Select label="Container" value={container} onChange={setContainer} className="sv-select" searchable={6}
        options={[{ value: '', label: containers.loading ? 'Loading containers…' : 'Choose a container' }, ...containerOptions]} />}
      {source === 'container' && <SegmentedControl label="Stream" className="auto-width" value={stream} onChange={setStream} options={[{ value: '', label: 'Both' }, { value: 'stdout', label: 'stdout' }, { value: 'stderr', label: 'stderr' }]} />}
      {source !== 'container' && <SegmentedControl label="Severity" className="auto-width" value={level} onChange={setLevel} options={[{ value: '', label: 'All' }, { value: 'warn,error', label: 'Warnings' }, { value: 'error', label: 'Errors' }]} />}
      <Select label="Time range" value={range} onChange={setRange} options={logRanges} className="sv-select" />
      <SearchField value={query} onChange={setQuery} placeholder="Search messages" />
    </div>

    {current && !current.available && <Notice tone="info" title={`${current.label} isn't available`}>{current.detail}</Notice>}
    {data?.available === false && data.reason && <Notice tone="info" title="Unavailable">{data.reason}</Notice>}
    {error && <Notice tone={error.status === 403 ? 'warning' : 'danger'} title={error.status === 403 ? 'Permission denied' : 'Couldn\'t load logs'}>{error.message}</Notice>}

    <div className="rp-console sv-console">
      <div className="rp-console-bar">
        <span className={cx('live-dot', !live && 'paused')}>{live ? 'Live' : 'Paused'}</span>
        <span>{lines.length} line{lines.length === 1 ? '' : 's'}{data?.truncated ? ' (newest 1,000)' : ''} · times in {utc ? 'UTC' : Intl.DateTimeFormat().resolvedOptions().timeZone}</span>
        <span className="spacer" />
        <button type="button" className="icon-action" onClick={() => setUtc(u => !u)} title="Toggle UTC / local time" aria-label="Toggle UTC or local time">{utc ? 'UTC' : 'Local'}</button>
        <button type="button" className="icon-action" onClick={() => setFollow(f => !f)} aria-pressed={follow} title="Follow new lines" aria-label="Follow new lines">{follow ? <ChevronDownIcon /> : <MinusCircleIcon />}</button>
        <button type="button" className="icon-action" onClick={() => { setLive(l => !l); if (!live) refetch(); }} aria-label={live ? 'Pause' : 'Resume'} title={live ? 'Pause' : 'Resume'}>{live ? <PauseIcon /> : <PlayIcon />}</button>
        <button type="button" className="icon-action" onClick={copySelected} disabled={!lines.length} aria-label={selected.size ? `Copy ${selected.size} selected lines` : 'Copy all lines'} title={copied ? 'Copied' : selected.size ? `Copy ${selected.size} selected` : 'Copy all'}>{copied ? <CheckCircleIcon /> : <ClipboardDocumentIcon />}</button>
        <button type="button" className="icon-action" onClick={() => downloadText(`${server.name}-${source}-logs.txt`, text(lines))} disabled={!lines.length} aria-label="Download" title="Download"><ArrowDownTrayIcon /></button>
      </div>
      <div className="sv-console-body" ref={scroller} onScroll={e => { const el = e.currentTarget; const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 24; if (atBottom !== follow) setFollow(atBottom); }}>
        {!ready ? <p className="dim sv-console-msg">Choose a container to read its logs.{!can(user, 'servers.inspect') ? ' Containers Ignition doesn\'t manage need the servers.inspect permission.' : ''}</p>
          : loading && !data ? <p className="dim sv-console-msg">Loading…</p>
            : !lines.length ? <p className="dim sv-console-msg">{data?.available === false ? 'Nothing to show.' : debounced || level ? 'No lines match these filters.' : 'No log lines in this time range.'}</p>
              : lines.map((l, i) => { const k = keyOf(l, i); const hasFields = l.fields && Object.keys(l.fields).length > 0; return <div key={k} className={cx('sv-line', levelTone[l.level], selected.has(k) && 'selected')}>
                <Checkbox label="Select line" checked={selected.has(k)} onChange={() => setSelected(s => { const n = new Set(s); n.has(k) ? n.delete(k) : n.add(k); return n; })} />
                <time className="dim">{fmtTime(l.time)}</time>
                <span className="sv-line-src">{l.source}{l.stream === 'stderr' ? '/err' : ''}</span>
                <span className="sv-line-msg">{l.message}
                  {hasFields && <button type="button" className="sv-fields-toggle" onClick={() => setExpanded(s => { const n = new Set(s); n.has(k) ? n.delete(k) : n.add(k); return n; })} aria-expanded={expanded.has(k)}>{expanded.has(k) ? <ChevronDownIcon /> : <ChevronRightIcon />}fields</button>}
                  {expanded.has(k) && <code className="sv-fields">{JSON.stringify(l.fields, null, 2)}</code>}
                </span>
              </div>; })}
      </div>
    </div>
    <p className="monitor-footnote">{current?.retention ? `Retention: ${current.retention}. ` : ''}Secrets that look like passwords, tokens, keys or credentials in URLs are masked before lines leave the server.</p>
  </>;
}

/* --------------------------------- Diagnostics -------------------------------- */

const resultIcon = { passed: CheckCircleIcon, warning: ExclamationTriangleIcon, failed: XCircleIcon, unsupported: MinusCircleIcon, skipped: MinusCircleIcon };
const resultTone = { passed: 'success', warning: 'warning', failed: 'danger', unsupported: 'neutral', skipped: 'neutral' };
const resultLabel = { passed: 'Passed', warning: 'Warning', failed: 'Failed', unsupported: 'Not applicable', skipped: 'Skipped' };

export function Diagnostics({ server, setFeedback }) {
  const { user } = useSession();
  const dev = isDeveloper(user);
  const { data, error, refetch } = useQuery(() => api.serverDiagnostics(server.id), [server.id], { pollMs: 5000 });
  const [open, setOpen] = useState(() => new Set());
  const [starting, setStarting] = useState(false);
  const active = data?.active;
  const latest = Object.fromEntries((data?.latest ?? []).map(r => [r.check, r]));
  const running = new Set(active ? active.checks.filter(c => !active.results.some(r => r.check === c)) : []);

  async function run(checks) {
    setStarting(true);
    try { await api.runServerDiagnostics(server.id, checks); refetch(); }
    catch (e) { setFeedback({ tone: 'danger', title: 'Couldn\'t start diagnostics', text: e.message }); }
    finally { setStarting(false); }
  }
  const counts = Object.values(latest).reduce((m, r) => ({ ...m, [r.status]: (m[r.status] ?? 0) + 1 }), {});

  return <>
    <div className="rp-intro">
      <p>Each check tests one thing from Ignition's worker against a fixed target (this server's socket or engine endpoint, or Docker Hub for pulls). No check accepts an address you type, so this page can't be used to probe other hosts.</p>
      <div className="rp-intro-actions">
        {can(user, 'servers.inspect') && <Button icon={ArrowDownTrayIcon} onClick={() => download(api.serverBundleUrl(server.id))}>Export bundle</Button>}
        {dev && <Button variant="primary" icon={BeakerIcon} disabled={!!active || starting} onClick={() => run()}>{active ? 'Running…' : 'Run all checks'}</Button>}
      </div>
    </div>
    {error && <Notice tone="danger" title="Couldn't load diagnostics">{error.message}</Notice>}
    {active && <RunProgress run={active} />}
    {Object.keys(latest).length > 0 && <p className="sv-step-intro">Latest results: {['failed', 'warning', 'passed', 'unsupported', 'skipped'].filter(s => counts[s]).map(s => `${counts[s]} ${resultLabel[s].toLowerCase()}`).join(' · ')}</p>}
    <ul className="sv-checks">
      {(data?.checks ?? []).map(c => {
        const r = latest[c.id];
        const Icon = r ? resultIcon[r.status] : MinusCircleIcon;
        const isOpen = open.has(c.id);
        return <li key={c.id} className={cx(r?.status, running.has(c.id) && 'running')}>
          <button type="button" className="sv-check-head" aria-expanded={isOpen} onClick={() => setOpen(s => { const n = new Set(s); n.has(c.id) ? n.delete(c.id) : n.add(c.id); return n; })}>
            <Icon className={cx('sv-check-icon', r?.status)} />
            <span className="sv-check-title"><strong>{c.title}</strong><span>{running.has(c.id) ? 'Running…' : r ? r.summary : c.description}</span></span>
            {r && <Status tone={resultTone[r.status]}>{resultLabel[r.status]}</Status>}
            <span className="rp-row-meta">{r ? timeAgo(r.ranAt) : 'Never run'}</span>
          </button>
          {isOpen && <div className="sv-check-body">
            <p className="muted">{c.description}</p>
            {r ? <>
              <dl className="sv-facts">
                <div><dt>Ran</dt><dd>{new Date(r.ranAt).toLocaleString()}</dd></div>
                <div><dt>Where</dt><dd>{r.location}</dd></div>
                <div><dt>Duration</dt><dd>{r.durationMs != null ? `${r.durationMs} ms` : '—'}</dd></div>
                <div><dt>Result</dt><dd>{resultLabel[r.status]}</dd></div>
              </dl>
              {r.hint && <Notice tone={r.status === 'failed' ? 'danger' : 'warning'} title="What to do">{r.hint}</Notice>}
              {r.output && <pre className="sv-pre">{r.output}</pre>}
            </> : <p className="muted">This check hasn't run on this server yet.</p>}
            {dev && <Button icon={BeakerIcon} disabled={!!active || starting} onClick={() => run([c.id])}>Run this check</Button>}
          </div>}
        </li>;
      })}
    </ul>
    {(data?.runs ?? []).length > 0 && <section className="ov-card">
      <header><h2>Recent runs</h2></header>
      <ol className="ov-events">{data.runs.map(r => <li key={r.id}>
        {r.status === 'finished' ? <CheckCircleIcon className="success" /> : r.status === 'failed' || r.status === 'cancelled' ? <XCircleIcon className="warning" /> : <BeakerIcon className="neutral" />}
        <span>{r.checks.length === data.checks.length ? 'All checks' : `${r.checks.length} check${r.checks.length === 1 ? '' : 's'}`} · {r.status} · by {r.requestedBy}</span>
        <time>{timeAgo(r.createdAt)}</time>
      </li>)}</ol>
    </section>}
  </>;
}

function RunProgress({ run }) {
  const done = run.results.length;
  return <Notice tone="info" title={`Diagnostics running: ${done} of ${run.checks.length} checks done`}>Started by {run.requestedBy} {timeAgo(run.createdAt).toLowerCase()}. Results appear as each check finishes.</Notice>;
}


import { useMemo, useState } from 'react';
import { ArrowTopRightOnSquareIcon, ChartBarIcon } from '@heroicons/react/16/solid';
import { Meter, Notice, SegmentedControl, Sparkline, Status, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import '../resource/resource.css';
import './views.css';

/*
 * Project monitoring: what every resource in the environment uses right now and over time, the
 * environment's totals, and who uses the most. Samples come every 15 seconds; a gap is drawn as a
 * gap, never as zero.
 */

const ranges = [{ value: '15m', label: '15 min' }, { value: '1h', label: '1 hour' }, { value: '6h', label: '6 hours' }, { value: '24h', label: '24 hours' }];
const sorts = [{ value: 'name', label: 'Name' }, { value: 'cpu', label: 'CPU' }, { value: 'memory', label: 'Memory' }];
const bytes = n => n == null ? '—' : n >= 1 << 30 ? `${(n / (1 << 30)).toFixed(1)} GB` : n >= 1 << 20 ? `${Math.round(n / (1 << 20))} MB` : `${Math.round(n / 1024)} KB`;
const rate = n => n == null ? '—' : n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MB/s` : n >= 1024 ? `${Math.round(n / 1024)} KB/s` : `${Math.round(n)} B/s`;
const pct = v => v == null ? '—' : `${v < 10 ? v.toFixed(1) : Math.round(v)}%`;
const statusTone = { running: 'success', deploying: 'progress', failed: 'danger', stopped: 'neutral', created: 'neutral', removing: 'neutral' };

export function ProjectMonitoring({ project, environment, onOpenResource }) {
  const [range, setRange] = useState('1h');
  const [sort, setSort] = useState('cpu');
  const { data, error } = useQuery(() => api.projectMetrics(project.id, { env: environment, range }), [project.id, environment, range], { pollMs: 15000 });
  const resources = data?.resources ?? [];
  const labels = (data?.buckets ?? []).map(b => new Date(b).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' }));

  const totals = useMemo(() => {
    const n = data?.buckets?.length ?? 0;
    const sum = key => Array.from({ length: n }, (_, i) => {
      const vals = resources.map(r => r.series[i]?.[key]).filter(v => v != null);
      return vals.length ? vals.reduce((a, b) => a + b, 0) : null;
    });
    const live = resources.filter(r => r.latest);
    return {
      cpu: sum('cpu'), memory: sum('memory'), rx: sum('rxBps'), tx: sum('txBps'),
      nowCpu: live.reduce((a, r) => a + r.latest.cpu, 0), nowMem: live.reduce((a, r) => a + r.latest.memory, 0),
      nowNet: live.reduce((a, r) => a + (r.latest.rxBps ?? 0) + (r.latest.txBps ?? 0), 0),
      memLimit: resources.filter(r => r.status === 'running').reduce((a, r) => a + r.memoryLimit, 0),
      cpuLimit: resources.filter(r => r.status === 'running').reduce((a, r) => a + r.cpus, 0),
      running: resources.filter(r => r.status === 'running').length, live: live.length,
    };
  }, [data, resources]);

  const rows = [...resources].sort((a, b) => sort === 'name' ? a.name.localeCompare(b.name)
    : sort === 'cpu' ? (b.latest?.cpu ?? -1) - (a.latest?.cpu ?? -1) : (b.latest?.memory ?? -1) - (a.latest?.memory ?? -1));
  const unhealthy = resources.filter(r => r.status === 'failed');

  return <div className="pv-page">
    <div className="pv-toolbar">
      <SegmentedControl label="Range" options={ranges} value={range} onChange={setRange} className="auto-width" />
      <span className="pv-spacer" />
      <span className="pv-meta">{data ? `Every ${data.sampleIntervalSeconds} s · kept ${data.retentionDays} days` : ''}</span>
    </div>
    {error && <Notice tone="danger" title="Couldn't load metrics">{error.message}</Notice>}
    {unhealthy.length > 0 && <Notice tone="danger" title={`${unhealthy.length} resource${unhealthy.length === 1 ? ' has' : 's have'} failed`}>{unhealthy.map(r => r.name).join(', ')}. Their logs and deployments say why.</Notice>}

    <section className="pv-totals">
      <Total label="Running" value={data ? `${totals.running} of ${resources.length}` : '—'} hint={totals.live < totals.running ? `${totals.running - totals.live} without a recent sample` : 'All reporting'} />
      <Total label="CPU" value={data ? pct(totals.nowCpu) : '—'} hint={`of one core · limits add up to ${totals.cpuLimit} CPU${totals.cpuLimit === 1 ? '' : 's'}`} series={totals.cpu} labels={labels} format={pct} />
      <Total label="Memory" value={data ? bytes(totals.nowMem) : '—'} hint={`of ${bytes(totals.memLimit)} in limits`} series={totals.memory} labels={labels} format={bytes}
        meter={totals.memLimit ? Math.min(100, Math.round(totals.nowMem / totals.memLimit * 100)) : null} />
      <Total label="Network" value={data ? rate(totals.nowNet) : '—'} hint="in + out, all resources" series={totals.rx.map((v, i) => v == null && totals.tx[i] == null ? null : (v ?? 0) + (totals.tx[i] ?? 0))} labels={labels} format={rate} />
    </section>

    <div className="pv-toolbar">
      <h2 className="pv-h2">Resources</h2>
      <span className="pv-spacer" />
      <SegmentedControl label="Sort" options={sorts} value={sort} onChange={setSort} className="auto-width" />
    </div>
    <div className="pv-usage">
      {rows.map(r => <article key={r.id} className={cx('pv-usage-row', r.status !== 'running' && 'idle')}>
        <button type="button" className="pv-usage-name" onClick={() => onOpenResource({ id: r.id, projectId: project.id }, 'Metrics')}>
          <strong>{r.name}</strong>
          <Status tone={statusTone[r.status] ?? 'neutral'}>{r.status}</Status>
          <small>{r.latest ? `Sampled ${timeAgo(r.latest.at)}` : r.status === 'running' ? 'Waiting for a sample' : 'Not running'}</small>
          <ArrowTopRightOnSquareIcon className="pv-open" />
        </button>
        <Cell label="CPU" now={r.latest ? pct(r.latest.cpu) : '—'} sub={`limit ${r.cpus} CPU`} series={r.series.map(p => p?.cpu ?? null)} labels={labels} format={pct} />
        <Cell label="Memory" now={r.latest ? bytes(r.latest.memory) : '—'} sub={`of ${bytes(r.memoryLimit)}`} series={r.series.map(p => p?.memory ?? null)} labels={labels} format={bytes}
          meter={r.latest ? Math.min(100, Math.round(r.latest.memory / r.memoryLimit * 100)) : null} max={r.memoryLimit} />
        <Cell label="Network" now={r.latest?.rxBps != null ? rate((r.latest.rxBps ?? 0) + (r.latest.txBps ?? 0)) : '—'} sub={r.latest?.rxBps != null ? `in ${rate(r.latest.rxBps)} · out ${rate(r.latest.txBps)}` : 'in + out'}
          series={r.series.map(p => p?.rxBps == null && p?.txBps == null ? null : (p.rxBps ?? 0) + (p.txBps ?? 0))} labels={labels} format={rate} />
      </article>)}
      {data && !rows.length && <div className="rp-empty"><ChartBarIcon /><strong>Nothing to monitor in {environment}</strong><p>Resources show up here once they're created.</p></div>}
    </div>
  </div>;
}

function Total({ label, value, hint, series, labels, format, meter }) {
  return <div className="pv-total">
    <span>{label}</span><strong>{value}</strong>
    {meter != null && <Meter label={label} value={meter} compact />}
    <small>{hint}</small>
    {series && (series.some(v => v != null) ? <Sparkline data={series} label={label} format={format} labels={labels} /> : <p className="pv-nodata">No samples in this range</p>)}
  </div>;
}

function Cell({ label, now, sub, series, labels, format, meter, max }) {
  const has = series.some(v => v != null);
  return <div className="pv-cell">
    <span className="pv-cell-head"><span>{label}</span><strong>{now}</strong></span>
    {meter != null && <Meter label={label} value={meter} compact />}
    <small>{sub}</small>
    {has ? <Sparkline data={series} label={label} format={format} labels={labels} max={max} /> : <p className="pv-nodata">No samples</p>}
  </div>;
}

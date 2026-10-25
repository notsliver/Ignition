import { useState } from 'react';
import { ArrowDownTrayIcon, ArrowRightIcon, PauseIcon, PlayIcon } from '@heroicons/react/16/solid';
import { Button, Notice, SegmentedControl, Sparkline, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { PageIntro } from './shared';
import './Metrics.css';

/*
 * Usage history from the worker's samples (every 15 s while running, kept 7 days).
 * Missing samples are shown as gaps. Ignition doesn't instrument apps, so there are no
 * request or latency charts; this page says so instead of inventing them.
 */

const ranges = [{ value: '15m', label: '15m' }, { value: '1h', label: '1h' }, { value: '6h', label: '6h' }, { value: '24h', label: '24h' }, { value: '7d', label: '7d' }];
const pct = v => `${v.toFixed(1)}%`;
const bytes = v => v >= 1 << 30 ? `${(v / (1 << 30)).toFixed(2)} GB` : v >= 1 << 20 ? `${(v / (1 << 20)).toFixed(1)} MB` : `${(v / 1024).toFixed(0)} KB`;
const rate = v => v >= 1 << 20 ? `${(v / (1 << 20)).toFixed(2)} MB/s` : v >= 1024 ? `${(v / 1024).toFixed(1)} KB/s` : `${v.toFixed(0)} B/s`;
const avg = list => { const v = list.filter(x => x != null); return v.length ? v.reduce((a, b) => a + b, 0) / v.length : null; };
const peak = list => { const v = list.filter(x => x != null); return v.length ? Math.max(...v) : null; };

function stamp(iso, range) {
  const d = new Date(iso);
  return range === '7d' || range === '24h'
    ? d.toLocaleString(undefined, { weekday: 'short', hour: '2-digit', minute: '2-digit' })
    : d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: range === '15m' ? '2-digit' : undefined });
}

export function Metrics({ resource, onNavigate }) {
  const [range, setRange] = useState('1h');
  const [mode, setMode] = useState('avg');
  const [live, setLive] = useState(true);
  const { data, error, loading, refetch } = useQuery(() => api.metrics(resource.id, range), [resource.id, range], { pollMs: live ? 15000 : 0 });

  const buckets = data?.buckets ?? [];
  const filled = buckets.filter(b => b.samples > 0).length;
  const labels = buckets.map(b => stamp(b.at, range));
  const cpus = data?.limits.cpus ?? resource.cpus;
  const memLimit = data?.limits.memoryBytes ?? resource.memoryMb * 1048576;
  const pick = (a, m) => buckets.map(b => (mode === 'max' ? b[m] : b[a]) ?? null);

  const charts = [
    { key: 'cpu', label: 'CPU', unit: `% of one core · limit ${cpus} vCPU = ${cpus * 100}%`, data: pick('cpuAvg', 'cpuMax'), max: cpus * 100, format: pct },
    { key: 'memory', label: 'Memory', unit: `limit ${bytes(memLimit)}`, data: pick('memoryAvg', 'memoryMax'), max: memLimit * 1.05, limit: memLimit, format: bytes },
    { key: 'rx', label: 'Network in', unit: 'received', data: buckets.map(b => b.netRxAvg ?? null), format: rate },
    { key: 'tx', label: 'Network out', unit: 'sent', data: buckets.map(b => b.netTxAvg ?? null), format: rate },
  ];

  function exportCsv() {
    const header = 'time,samples,cpu_avg_percent,cpu_max_percent,memory_avg_bytes,memory_max_bytes,memory_limit_bytes,net_rx_bytes_per_s,net_tx_bytes_per_s';
    const rows = buckets.map(b => [b.at, b.samples, b.cpuAvg, b.cpuMax, b.memoryAvg, b.memoryMax, b.memoryLimit, b.netRxAvg, b.netTxAvg].map(v => v ?? '').join(','));
    const url = URL.createObjectURL(new Blob([[header, ...rows].join('\n')], { type: 'text/csv' }));
    Object.assign(document.createElement('a'), { href: url, download: `${resource.name}-metrics-${range}.csv` }).click();
    URL.revokeObjectURL(url);
  }

  return <div className="rp-page">
    <div className="rp-toolbar">
      <SegmentedControl label="Time range" options={ranges} value={range} onChange={setRange} className="auto-width" />
      <SegmentedControl label="Aggregation" options={[{ value: 'avg', label: 'Average' }, { value: 'max', label: 'Peak' }]} value={mode} onChange={setMode} className="auto-width" />
      <span className="spacer" />
      <span className="rp-row-meta">{data?.lastSampleAt ? `Last sample ${timeAgo(data.lastSampleAt).toLowerCase()}` : 'No samples yet'}</span>
      <Button icon={live ? PauseIcon : PlayIcon} onClick={() => { setLive(l => !l); if (!live) refetch(); }}>{live ? 'Live' : 'Paused'}</Button>
      <Button icon={ArrowDownTrayIcon} onClick={exportCsv} disabled={!filled}>CSV</Button>
    </div>

    {error && <Notice tone="danger" title="Couldn't load metrics">{error.message}</Notice>}
    {data && !filled && <Notice tone="info" title={resource.status === 'running' ? 'Collecting the first samples' : 'No data for this period'}>
      {resource.status === 'running'
        ? 'Ignition samples running containers every 15 seconds. The first points appear within a minute.'
        : `${resource.name} isn't running, and Ignition only collects metrics while it runs. Samples are kept for 7 days.`}
    </Notice>}

    <div className="metrics-grid">
      {charts.map(c => {
        const latest = [...c.data].reverse().find(v => v != null);
        return <section key={c.key} className={cx('rp-card metric-card', loading && !data && 'loading')}>
          <header>
            <div><h3>{c.label}</h3><span>{c.unit}</span></div>
            <strong>{latest != null ? c.format(latest) : '—'}</strong>
          </header>
          {buckets.length ? <Sparkline data={c.data} max={c.max} limit={c.limit} label={c.label} format={c.format} labels={labels} /> : <div className="metric-placeholder" />}
          <footer>
            <span>Avg <b>{avg(c.data) != null ? c.format(avg(c.data)) : '—'}</b></span>
            <span>Peak <b>{peak(c.data) != null ? c.format(peak(c.data)) : '—'}</b></span>
            {c.limit && <span className="metric-limit-key">Limit</span>}
          </footer>
        </section>;
      })}
    </div>

    <p className="metrics-footnote">
      {data ? `${filled} of ${buckets.length} intervals have data · ${data.stepSeconds >= 60 ? `${data.stepSeconds / 60} min` : `${data.stepSeconds} s`} per point · sampled every 15 s, kept 7 days. ` : ''}
      Request rate and latency aren't shown: Ignition doesn't instrument your app.{' '}
      <button type="button" className="inline-link" onClick={() => onNavigate?.('Logs')}>Open logs<ArrowRightIcon style={{ width: 11, verticalAlign: -1 }} /></button>
    </p>
  </div>;
}

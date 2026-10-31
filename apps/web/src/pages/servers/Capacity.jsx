import { useEffect, useState } from 'react';
import { ChevronDownIcon, ChevronRightIcon } from '@heroicons/react/16/solid';
import { Meter, Notice, SegmentedControl, Status, cx } from '../../ui';
import { api } from '../../api/client';
import { useQuery } from '../../api/hooks';
import { bytes } from './model';

/*
 * Capacity planning for the whole fleet: what each server has, what's promised to workloads
 * (their memory and CPU limits), what it actually uses at p95, and when its disk fills at the
 * current rate. Collapsed to one line until opened; servers without host samples say so instead
 * of showing guesses.
 */

const statusTone = { constrained: 'danger', watch: 'warning', ok: 'success', underused: 'neutral', unknown: 'neutral' };
const statusLabel = { constrained: 'Constrained', watch: 'Watch', ok: 'Healthy', underused: 'Underused', unknown: 'Not measured' };
const order = { constrained: 0, watch: 1, unknown: 3, ok: 2, underused: 2 };

function openPref() {
  try { return localStorage.getItem('ignition:servers:capacity') === 'open'; } catch { return false; }
}

const pctText = v => v == null ? '—' : `${Math.round(v)}%`;
const cores = v => `${Number(v.toFixed(2))} CPU${v === 1 ? '' : 's'}`;

export function CapacityPanel({ onOpen }) {
  const [open, setOpen] = useState(openPref);
  const [span, setSpan] = useState('7d');
  useEffect(() => { try { localStorage.setItem('ignition:servers:capacity', open ? 'open' : 'closed'); } catch { /* private mode */ } }, [open]);
  const { data, error } = useQuery(() => api.serversCapacity(span), [span], { pollMs: 60000 });
  const t = data?.totals;
  const openOne = id => api.server(id).then(r => onOpen(r.server)).catch(() => {});
  const list = [...(data?.servers ?? [])].sort((a, b) => order[a.status] - order[b.status] || a.name.localeCompare(b.name));

  const headline = !t ? 'Loading…'
    : !t.servers ? 'No servers yet'
      : [t.constrained && `${t.constrained} constrained`, t.watch && `${t.watch} to watch`, t.underused && `${t.underused} underused`].filter(Boolean).join(' · ')
        || (t.measured ? 'Every measured server has headroom' : 'No host measurements yet');

  return <section className={cx('sv-capacity', open && 'open')} aria-label="Capacity planning">
    <button type="button" className="sv-capacity-toggle" aria-expanded={open} onClick={() => setOpen(o => !o)}>
      {open ? <ChevronDownIcon /> : <ChevronRightIcon />}
      <strong>Capacity</strong>
      <span className={cx(t?.constrained > 0 && 'bad')}>{headline}</span>
      {t?.servers > 0 && <small>{t.cores} cores · {bytes(t.memory)} memory · {t.diskTotal ? `${bytes(t.diskUsed)} of ${bytes(t.diskTotal)} disk` : 'disk not measured'}</small>}
    </button>
    {open && <div className="sv-capacity-body">
      {error && <Notice tone="danger" title="Couldn't load capacity">{error.message}</Notice>}
      {t && <div className="sv-capacity-totals">
        <Total label="CPU limits committed" value={t.cores ? Math.round(t.committedCpus / t.cores * 100) : null} detail={`${cores(t.committedCpus)} of ${t.cores} cores`} />
        <Total label="Memory limits committed" value={t.memory ? Math.round(t.committedMemory / t.memory * 100) : null} detail={`${bytes(t.committedMemory)} of ${bytes(t.memory)}`} />
        <Total label="Disk used" value={t.diskTotal ? Math.round(t.diskUsed / t.diskTotal * 100) : null} detail={t.diskTotal ? `${bytes(t.diskTotal - t.diskUsed)} free` : 'Needs host metrics'} />
        <div className="sv-capacity-window">
          <SegmentedControl label="Window" className="auto-width" value={span} onChange={setSpan} options={[{ value: '24h', label: '24 h' }, { value: '7d', label: '7 days' }]} />
          <small>{t.measured} of {t.servers} server(s) measured · {t.workloads} workload(s)</small>
        </div>
      </div>}
      {list.length > 0 && <div className="monitor-table-wrap">
        <table className="monitor-table sv-capacity-table">
          <thead><tr>
            <th>Server</th><th>Status</th><th className="num" title="95th percentile over the window">CPU p95</th><th className="num" title="95th percentile over the window">Memory p95</th>
            <th className="num" title="Sum of the CPU limits of workloads meant to run here">CPU committed</th><th className="num" title="Sum of the memory limits of workloads meant to run here">Memory committed</th>
            <th>Disk</th><th className="num">Full in</th><th>Findings</th>
          </tr></thead>
          <tbody>
            {list.map(s => <tr key={s.id} tabIndex={0} onClick={() => openOne(s.id)} onKeyDown={e => { if (e.key === 'Enter') openOne(s.id); }}>
              <td><span className="resource-name">{s.name}<span>{[s.location, s.environment].filter(Boolean).join(' · ') || (s.cores ? `${s.cores} cores · ${bytes(s.memoryCapacity)}` : '—')}</span></span></td>
              <td><Status tone={statusTone[s.status]}>{statusLabel[s.status]}</Status></td>
              <td className="num">{pctText(s.cpuP95)}</td>
              <td className="num">{pctText(s.memoryP95Pct)}</td>
              <td className="num">{s.cpuCommitPct != null ? pctText(s.cpuCommitPct) : '—'}<small>{s.committedCpus ? cores(s.committedCpus) : ''}</small></td>
              <td className="num">{s.memoryCommitPct != null ? pctText(s.memoryCommitPct) : '—'}<small>{s.committedMemory ? bytes(s.committedMemory) : ''}</small></td>
              <td>{s.diskPct != null ? <span className="sv-usage"><Meter label="Disk" value={Math.min(100, s.diskPct)} compact /></span> : <span className="muted">—</span>}</td>
              <td className="num">{s.daysUntilDiskFull != null ? (s.daysUntilDiskFull > 365 ? '> 1 year' : `${Math.max(0, Math.round(s.daysUntilDiskFull))} days`) : <span className="muted" title="Needs at least 6 hours of disk samples with growth">—</span>}</td>
              <td className="sv-findings">{s.findings.length ? <ul>{s.findings.map(f => <li key={f}>{f}</li>)}</ul> : <span className="muted">Nothing to flag.</span>}</td>
            </tr>)}
          </tbody>
        </table>
      </div>}
      {data && <p className="monitor-footnote">
        Constrained: CPU p95 ≥ {data.thresholds.cpuHot}%, memory p95 ≥ {data.thresholds.memoryHot}%, disk ≥ {data.thresholds.diskHot}% or full within {data.thresholds.diskSoonDays} days.
        Underused: CPU p95 under {data.thresholds.cpuIdle}% and memory under {data.thresholds.memoryIdle}% with at least two hours of history.
        Committed is the sum of workload limits; workloads may use less. Deployments only target the local server, so moving workloads to another server is manual.
      </p>}
    </div>}
  </section>;
}

function Total({ label, value, detail }) {
  return <div className="sv-capacity-total">
    <span>{label}</span>
    <strong>{value != null ? `${value}%` : '—'}</strong>
    <span className={cx('sv-capacity-bar', value >= 85 && 'hot')}><i style={{ width: `${Math.min(100, value ?? 0)}%` }} /></span>
    <small>{detail}</small>
  </div>;
}

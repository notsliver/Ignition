import { useMemo } from 'react';
import { ArrowRightIcon, ArrowUturnLeftIcon, ExclamationTriangleIcon, LightBulbIcon } from '@heroicons/react/16/solid';
import { Button, Notice, Select, Switch, TextInput, cx } from '../../../ui';
import { SaveBar, SettingsField, SettingsGroup } from '../../settings/SettingsField';
import { useSettingsForm } from './shared';
import './Limits.css';

/*
 * Capacity for one replica: what's configured, what the server actually enforces,
 * and how the last 7 days of usage compare. Lowering a limit below observed peaks warns first.
 */

const cpuSteps = ['0.25', '0.5', '1', '2', '4'];
const memSteps = ['256', '512', '768', '1024', '2048', '4096'];
const cpuLabel = v => `${v} vCPU`;
const memLabel = v => Number(v) >= 1024 ? `${Number(v) / 1024} GB` : `${v} MB`;
const opts = (list, label) => list.map(v => ({ value: v, label: label(v) }));

const DEFAULTS = { cpu: '1', cpuReserve: '0.25', memory: '1024', memReserve: '256', swap: 'off', pids: '512', replicas: '2', autoscale: false, min: '1', max: '4', target: '70', onOom: 'restart', backoff: '10', maxRestarts: '5' };
const LABELS = { cpu: 'CPU limit', cpuReserve: 'CPU reservation', memory: 'Memory limit', memReserve: 'Memory reservation', swap: 'Swap', pids: 'Process limit', replicas: 'Replicas', autoscale: 'Autoscaling', min: 'Min replicas', max: 'Max replicas', target: 'Target CPU', onOom: 'On out of memory', backoff: 'Restart backoff', maxRestarts: 'Max restarts' };
const show = (k, v) => k === 'cpu' || k === 'cpuReserve' ? cpuLabel(v) : k === 'memory' || k === 'memReserve' ? memLabel(v) : v === true ? 'On' : v === false ? 'Off' : String(v);

function UsageBar({ label, unit, used, avg, peak, reserve, limit, format }) {
  const pct = v => `${Math.min(100, (v / limit) * 100)}%`;
  const level = peak / limit >= 0.9 ? 'critical' : peak / limit >= 0.75 ? 'high' : '';
  return <div className={cx('usage', level)}>
    <div className="usage-head"><span>{label}</span><strong>{format(used)}<small> / {format(limit)}{unit}</small></strong></div>
    <div className="usage-track" role="img" aria-label={`${label}: ${format(used)} used, peak ${format(peak)}, limit ${format(limit)}`}>
      <span className="usage-fill" style={{ width: pct(used) }} />
      <span className="usage-marker reserve" style={{ left: pct(reserve) }} title={`Reservation ${format(reserve)}`} />
      <span className="usage-marker peak" style={{ left: pct(peak) }} title={`7-day peak ${format(peak)}`} />
    </div>
    <div className="usage-legend"><span>Avg {format(avg)}</span><span className="peak-key">Peak {format(peak)}</span><span className="reserve-key">Reserved {format(reserve)}</span></div>
  </div>;
}

export function ResourceLimits({ resource, onNavigate }) {
  const form = useSettingsForm(DEFAULTS);
  const { values, set, bind, bindInput } = form;
  const isApp = resource.type === 'application';

  // Observed over the last 7 days (sample data; real values come from the metrics backend).
  const observed = useMemo(() => {
    const mem = parseFloat(resource.memory) * (resource.memory?.endsWith('GB') ? 1024 : 1) || 300;
    const cpuNow = (resource.cpu ?? 10) / 100;
    return { cpuNow, cpuAvg: cpuNow * 0.7, cpuPeak: Math.min(1, cpuNow * 2.6 + 0.12), memNow: mem, memAvg: mem * 0.85, memPeak: mem * 1.45, throttled: 4.2, restarts: 3 };
  }, [resource]);
  const oomEvents = [
    { time: 'Yesterday 14:12', detail: `Container exceeded ${memLabel(DEFAULTS.memory)} and was killed (exit code 137)`, replica: `${resource.name}-2` },
    { time: 'Oct 20 03:04', detail: 'Killed during nightly import (exit code 137)', replica: `${resource.name}-1` },
  ];

  const cpu = Number(values.cpu);
  const memory = Number(values.memory);
  const warnings = [];
  if (memory < observed.memPeak) warnings.push(`The memory limit (${memLabel(values.memory)}) is below the 7-day peak of ${memLabel(Math.round(observed.memPeak))}. Expect out-of-memory restarts.`);
  if (cpu < observed.cpuPeak) warnings.push(`The CPU limit (${cpuLabel(values.cpu)}) is below the 7-day peak of ${observed.cpuPeak.toFixed(2)} vCPU. Requests will slow down under load.`);
  if (Number(values.memReserve) > memory) warnings.push('Memory reservation can\'t be higher than the limit.');
  if (Number(values.cpuReserve) > cpu) warnings.push('CPU reservation can\'t be higher than the limit.');
  if (values.autoscale && Number(values.min) > Number(values.max)) warnings.push('Minimum replicas can\'t be higher than the maximum.');
  const blocking = warnings.filter(w => w.includes('can\'t'));

  // Recommend roughly 30% headroom over the observed peak, rounded to an available step.
  const recMem = memSteps.find(m => Number(m) >= observed.memPeak * 1.3) ?? memSteps.at(-1);
  const recCpu = cpuSteps.find(c => Number(c) >= observed.cpuPeak * 1.3) ?? cpuSteps.at(-1);
  const changes = Object.keys(DEFAULTS).filter(k => values[k] !== form.saved[k]);

  return <form className="settings-page rp-settings limits-page" onSubmit={e => { e.preventDefault(); if (!blocking.length) form.save(); }}>
    <section className="settings-group">
      <div className="limits-title"><h2>Usage per replica · last 7 days</h2><button type="button" className="ov-link" onClick={() => onNavigate?.('Metrics')}>Metrics<ArrowRightIcon /></button></div>
      <div className="settings-card limits-usage-card">
        <UsageBar label="CPU" unit="" used={observed.cpuNow} avg={observed.cpuAvg} peak={observed.cpuPeak} reserve={Number(values.cpuReserve)} limit={cpu} format={v => `${v.toFixed(2)} vCPU`} />
        <UsageBar label="Memory" unit="" used={observed.memNow} avg={observed.memAvg} peak={observed.memPeak} reserve={Number(values.memReserve)} limit={memory} format={v => memLabel(Math.round(v))} />
        <dl className="limits-stats">
          <div><dt>CPU throttled</dt><dd className={cx(observed.throttled > 5 && 'warn')}>{observed.throttled}% of the time</dd></div>
          <div><dt>Out-of-memory kills</dt><dd className={cx(oomEvents.length && 'bad')}>{oomEvents.length}</dd></div>
          <div><dt>Restarts</dt><dd>{observed.restarts}</dd></div>
          <div><dt>Last exit reason</dt><dd>OOMKilled (137)</dd></div>
        </dl>
      </div>
    </section>

    {(recMem !== values.memory || recCpu !== values.cpu) && <div className="limits-rec">
      <LightBulbIcon />
      <p><strong>Recommended:</strong> {recCpu !== values.cpu && <>{cpuLabel(recCpu)}</>}{recCpu !== values.cpu && recMem !== values.memory && ' and '}{recMem !== values.memory && <>{memLabel(recMem)} memory</>}, about 30% above the observed peak.</p>
      <Button type="button" onClick={() => { set('cpu')(recCpu); set('memory')(recMem); }}>Apply recommendation</Button>
    </div>}

    <SettingsGroup title="CPU">
      <SettingsField label="Limit" hint="Hard cap. The process is throttled above it." htmlFor="cpu"><Select {...bind('cpu')} label="CPU limit" options={opts(cpuSteps, cpuLabel)} /><span className="rp-chip success">Enforced</span></SettingsField>
      <SettingsField label="Reservation" hint="Guaranteed share, used when placing replicas on servers." htmlFor="cpuReserve"><Select {...bind('cpuReserve')} label="CPU reservation" options={opts(cpuSteps, cpuLabel)} /><span className="rp-chip success">Enforced</span></SettingsField>
    </SettingsGroup>

    <SettingsGroup title="Memory">
      <SettingsField label="Limit" hint="The container is killed if it goes above this." htmlFor="memory"><Select {...bind('memory')} label="Memory limit" options={opts(memSteps, memLabel)} /><span className="rp-chip success">Enforced</span></SettingsField>
      <SettingsField label="Reservation" htmlFor="memReserve"><Select {...bind('memReserve')} label="Memory reservation" options={opts(memSteps, memLabel)} /><span className="rp-chip success">Enforced</span></SettingsField>
      <SettingsField label="Swap" hint="production-01 has swap disabled, so this setting has no effect there." htmlFor="swap"><Select {...bind('swap')} label="Swap" options={[{ value: 'off', label: 'Off' }, { value: 'equal', label: 'Same as memory limit' }]} /><span className="rp-chip warning">Not enforced</span></SettingsField>
      <SettingsField label="Process limit" hint="Maximum processes and threads (PIDs)." htmlFor="pids"><TextInput type="number" min="16" {...bindInput('pids')} /><span className="rp-chip success">Enforced</span></SettingsField>
    </SettingsGroup>

    <SettingsGroup title="Scaling" description={isApp ? undefined : 'This resource runs a single replica.'}>
      <SettingsField label="Replicas" hint={values.autoscale ? 'Managed by autoscaling.' : `Total: ${cpuLabel(String(cpu * Number(values.replicas || 1)))} and ${memLabel(memory * Number(values.replicas || 1))}.`} htmlFor="replicas">
        <TextInput type="number" min="1" {...bindInput('replicas')} disabled={values.autoscale || !isApp} />
      </SettingsField>
      {isApp && <SettingsField label="Autoscaling" hint="Add replicas when average CPU stays above the target."><Switch label="Autoscaling" checked={values.autoscale} onChange={set('autoscale')} /></SettingsField>}
      {isApp && values.autoscale && <>
        <SettingsField label="Replica range" htmlFor="min"><span className="limits-range"><TextInput type="number" min="1" aria-label="Minimum replicas" {...bindInput('min')} /><span>to</span><TextInput type="number" min="1" aria-label="Maximum replicas" {...bindInput('max')} /></span></SettingsField>
        <SettingsField label="Target CPU" htmlFor="target"><TextInput type="number" suffix="%" {...bindInput('target')} /></SettingsField>
      </>}
    </SettingsGroup>

    <SettingsGroup title="When limits are hit">
      <SettingsField label="On out of memory" htmlFor="onOom"><Select {...bind('onOom')} label="On out of memory" options={[{ value: 'restart', label: 'Restart the replica', description: 'With backoff between attempts' }, { value: 'stop', label: 'Stop and alert', description: 'Leave it stopped for investigation' }]} /></SettingsField>
      {values.onOom === 'restart' && <>
        <SettingsField label="Restart backoff" hint="Doubles after each restart, up to 5 minutes." htmlFor="backoff"><TextInput type="number" suffix="seconds" {...bindInput('backoff')} /></SettingsField>
        <SettingsField label="Max restarts" hint="Within 10 minutes, then the replica stays stopped." htmlFor="maxRestarts"><TextInput type="number" {...bindInput('maxRestarts')} /></SettingsField>
      </>}
    </SettingsGroup>

    <section className="settings-group">
      <h2>Out-of-memory events</h2>
      <div className="settings-card">
        {oomEvents.map(e => <div key={e.time} className="rp-row">
          <ExclamationTriangleIcon className="limits-oom-icon" />
          <span className="rp-row-main"><strong>{e.detail}</strong><span>{e.replica} · {e.time}</span></span>
          <Button type="button" onClick={() => onNavigate?.('Logs')}>Logs around this</Button>
        </div>)}
      </div>
    </section>

    <p className="limits-build-note">Build containers have separate limits (memory and timeout). <button type="button" className="inline-link" onClick={() => onNavigate?.('Build & Deploy')}>Build & Deploy</button></p>

    {warnings.length > 0 && <Notice tone={blocking.length ? 'danger' : 'warning'} title={blocking.length ? 'Fix these before applying' : 'Check the impact before applying'}>{warnings.join(' ')}</Notice>}

    {changes.length > 0 && <section className="settings-group">
      <h2>Current vs proposed</h2>
      <div className="settings-card pending-changes">
        {changes.map(k => <div key={k}><span>{LABELS[k]}</span><code>{show(k, form.saved[k])}</code><span className="arrow">→</span><code>{show(k, values[k])}</code></div>)}
      </div>
    </section>}

    <div className="limits-footer"><Button type="button" icon={ArrowUturnLeftIcon} onClick={() => Object.entries(DEFAULTS).forEach(([k, v]) => set(k)(v))}>Restore defaults</Button></div>
    <SaveBar dirty={form.dirty} onDiscard={form.discard} summary={`${changes.length} ${changes.length === 1 ? 'change' : 'changes'} · replicas restart one at a time to apply`} />
  </form>;
}


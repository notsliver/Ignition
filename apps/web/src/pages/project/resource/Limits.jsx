import { useEffect, useState } from 'react';
import { ArrowRightIcon, CheckCircleIcon, ExclamationTriangleIcon, LightBulbIcon } from '@heroicons/react/16/solid';
import { Button, Notice, Select, Switch, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { useSession } from '../../../app/AuthGate';
import { SaveBar, SettingsField, SettingsGroup } from '../../settings/SettingsField';
import './Limits.css';

/*
 * Limits Docker enforces on this resource's container. Every setting here is applied to the running
 * container live (docker update), so nothing restarts. Usage comes from the worker's 15-second samples (7 days).
 */

const CPU_STEPS = [0.25, 0.5, 1, 1.5, 2, 3, 4, 6, 8, 12, 16];
const MEM_STEPS = [128, 256, 384, 512, 768, 1024, 1536, 2048, 3072, 4096, 6144, 8192, 12288, 16384];
const cpuLabel = v => `${Number(v)} vCPU`;
const memLabel = v => Number(v) >= 1024 ? `${+(Number(v) / 1024).toFixed(2)} GB` : `${Math.round(Number(v))} MB`;
const MB = 1 << 20;
const RESTART = [
  { value: 'always', label: 'Always restart', description: 'Also after Docker or this machine restarts' },
  { value: 'on-failure', label: 'Restart a few times', description: 'Then leave it stopped for investigation' },
  { value: 'never', label: 'Never restart', description: 'Stay stopped after a crash' },
];
const LABELS = { cpus: 'CPU limit', memoryMb: 'Memory limit', memoryReservationMb: 'Memory reservation', allowSwap: 'Swap', pidsLimit: 'Process limit', restartPolicy: 'On crash', maxRestarts: 'Max restarts' };
const show = (k, v) => v === '' || v === null ? 'None' : k === 'cpus' ? cpuLabel(v) : k.startsWith('memory') ? memLabel(v) : v === true ? 'Allowed' : v === false ? 'Off' : k === 'restartPolicy' ? RESTART.find(r => r.value === v)?.label : String(v);

const toForm = l => ({ cpus: String(l.cpus), memoryMb: String(l.memoryMb), memoryReservationMb: l.memoryReservationMb ? String(l.memoryReservationMb) : '', allowSwap: l.allowSwap, pidsLimit: l.pidsLimit ? String(l.pidsLimit) : '', restartPolicy: l.restartPolicy, maxRestarts: String(l.maxRestarts) });
const toBody = f => ({ cpus: Number(f.cpus), memoryMb: Number(f.memoryMb), memoryReservationMb: f.memoryReservationMb ? Number(f.memoryReservationMb) : null, allowSwap: f.allowSwap, pidsLimit: f.pidsLimit ? Number(f.pidsLimit) : null, restartPolicy: f.restartPolicy, maxRestarts: Number(f.maxRestarts) || 5 });

function UsageBar({ label, now, avg, peak, limit, format }) {
  const pct = v => `${Math.min(100, (v / limit) * 100)}%`;
  const level = peak / limit >= 0.9 ? 'critical' : peak / limit >= 0.75 ? 'high' : '';
  return <div className={cx('usage', level)}>
    <div className="usage-head"><span>{label}</span><strong>{now == null ? '—' : format(now)}<small> / {format(limit)}</small></strong></div>
    <div className="usage-track" role="img" aria-label={`${label}: ${now == null ? 'not running' : format(now)} now, 7-day peak ${format(peak)}, limit ${format(limit)}`}>
      {now != null && <span className="usage-fill" style={{ width: pct(now) }} />}
      {peak > 0 && <span className="usage-marker peak" style={{ left: pct(peak) }} title={`7-day peak ${format(peak)}`} />}
    </div>
    <div className="usage-legend"><span>Avg {format(avg)}</span><span className="peak-key">Peak {format(peak)}</span></div>
  </div>;
}

export function ResourceLimits({ resource, onNavigate, onResourceChange }) {
  const { user } = useSession();
  const canEdit = user.role !== 'viewer';
  const { data, error, refetch } = useQuery(() => api.limits(resource.id), [resource.id], { pollMs: 15000 });
  const [saved, setSaved] = useState(null);
  const [form, setForm] = useState(null);
  const [errors, setErrors] = useState({});
  const [feedback, setFeedback] = useState(null);
  const [busy, setBusy] = useState(false);
  const dirty = saved && JSON.stringify(form) !== JSON.stringify(saved);
  useEffect(() => { if (data && !dirty) { const f = toForm(data.limits); setSaved(f); setForm(f); } }, [data]); // eslint-disable-line react-hooks/exhaustive-deps

  if (error && !data) return <div className="rp-page"><Notice tone="danger" title="Couldn't load limits">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice></div>;
  if (!data || !form) return <div className="rp-page"><p className="limits-muted">Loading…</p></div>;

  const set = key => value => { setForm(f => ({ ...f, [key]: value })); setErrors(({ [key]: _, ...rest }) => rest); setFeedback(null); };
  const { usage, live, host } = data;
  const cpu = Number(form.cpus), memory = Number(form.memoryMb);
  const hostCpus = host?.cpus ?? 16, hostMem = host ? host.memoryBytes / MB : 16384;
  const withCurrent = (steps, current, max) => [...new Set([...steps.filter(s => s <= max), Number(current)])].sort((a, b) => a - b);
  const cpuOptions = withCurrent(CPU_STEPS, saved.cpus, hostCpus).map(v => ({ value: String(v), label: cpuLabel(v) }));
  const memOptions = withCurrent(MEM_STEPS, saved.memoryMb, hostMem).map(v => ({ value: String(v), label: memLabel(v) }));
  const reserveOptions = [{ value: '', label: 'None' }, ...MEM_STEPS.filter(m => m <= memory && m >= 64).map(v => ({ value: String(v), label: memLabel(v) }))];

  // Usage in the same units as the limits: vCPU and MB.
  const hasUsage = usage.samples > 0;
  const cpuNow = live?.running && live.cpuPercent != null ? live.cpuPercent / 100 : null;
  const memNow = live?.running && live.memoryBytes != null ? live.memoryBytes / MB : null;
  const cpuPeak = usage.cpuPeak / 100, memPeak = usage.memoryPeak / MB;
  const warnings = [];
  if (hasUsage && memory < memPeak) warnings.push(`The memory limit (${memLabel(memory)}) is below the 7-day peak of ${memLabel(memPeak)}. Expect out-of-memory kills.`);
  if (hasUsage && cpu < cpuPeak) warnings.push(`The CPU limit (${cpuLabel(cpu)}) is below the 7-day peak of ${cpuPeak.toFixed(2)} vCPU. It will slow down under load.`);
  // Recommend ~30% headroom over a real peak, once there's at least an hour of samples.
  const enough = usage.samples >= 240;
  const recMem = enough ? MEM_STEPS.find(m => m >= memPeak * 1.3 && m <= hostMem) : null;
  const recCpu = enough ? CPU_STEPS.find(c => c >= Math.max(cpuPeak * 1.3, 0.25) && c <= hostCpus) : null;
  const recommend = enough && ((recMem && memPeak * 1.1 > Number(saved.memoryMb)) || (recCpu && cpuPeak * 1.1 > Number(saved.cpus)));
  const changes = Object.keys(LABELS).filter(k => form[k] !== saved[k] && !(k === 'maxRestarts' && form.restartPolicy !== 'on-failure'));

  async function save(event) {
    event.preventDefault();
    setBusy(true); setErrors({});
    try {
      const out = await api.putLimits(resource.id, toBody(form));
      const f = toForm(out.limits);
      setSaved(f); setForm(f);
      onResourceChange?.({ ...resource, memoryMb: out.limits.memoryMb, cpus: out.limits.cpus });
      setFeedback(out.applyError ? { tone: 'warning', title: 'Saved, not applied yet', text: out.applyError }
        : out.applied ? { tone: 'success', title: 'Applied to the running container', text: 'No restart was needed.' }
          : { tone: 'success', title: 'Saved', text: 'Applies when the resource starts.' });
      refetch();
    } catch (err) {
      setErrors(err.fields ?? {});
      setFeedback({ tone: 'danger', title: 'Couldn\'t save limits', text: err.fields ? Object.values(err.fields).join(' ') : err.message });
    }
    setBusy(false);
  }
  const field = (key, label, hint, control) => <SettingsField label={label} hint={errors[key] ? <span className="limits-error">{errors[key]}</span> : hint} htmlFor={key}>{control}</SettingsField>;
  const lastExit = live && !live.running && live.finishedAt ? (live.oomKilled ? 'Out of memory (137)' : `Exit code ${live.exitCode}`) : live?.oomKilled ? 'Out of memory (137)' : '—';

  return <form className="settings-page rp-settings limits-page" onSubmit={save}>
    <section className="settings-group">
      <div className="limits-title"><h2>Usage · last 7 days</h2><button type="button" className="ov-link" onClick={() => onNavigate?.('Metrics')}>Metrics<ArrowRightIcon /></button></div>
      <div className="settings-card limits-usage-card">
        {hasUsage || cpuNow != null ? <>
          <UsageBar label="CPU" now={cpuNow} avg={usage.cpuAvg / 100} peak={cpuPeak} limit={Number(saved.cpus)} format={v => `${v.toFixed(2)} vCPU`} />
          <UsageBar label="Memory" now={memNow} avg={usage.memoryAvg / MB} peak={memPeak} limit={Number(saved.memoryMb)} format={memLabel} />
        </> : <p className="limits-empty">No usage recorded yet. Ignition samples every 15 seconds while {resource.name} runs.</p>}
        <dl className="limits-stats">
          <div><dt>Restarts</dt><dd className={cx(live?.restartCount > 0 && 'warn')}>{live ? live.restartCount : '—'}</dd></div>
          <div><dt>Near the memory limit</dt><dd className={cx(usage.nearMemoryLimit > 0 && 'warn')}>{usage.nearMemoryLimit ? `${Math.round(usage.nearMemoryLimit / 4)} min` : 'Never'}</dd></div>
          <div><dt>Last exit</dt><dd className={cx(live?.oomKilled && 'bad')}>{lastExit}{live?.finishedAt && !live.running && <small> · {timeAgo(live.finishedAt)}</small>}</dd></div>
          <div><dt>Recorded since</dt><dd>{usage.since ? timeAgo(usage.since) : '—'}</dd></div>
        </dl>
      </div>
    </section>

    {recommend && canEdit && <div className="limits-rec">
      <LightBulbIcon />
      <p><strong>Recommended:</strong> {[recCpu && cpuLabel(recCpu), recMem && `${memLabel(recMem)} memory`].filter(Boolean).join(' and ')}, about 30% above the 7-day peak.</p>
      <Button type="button" onClick={() => { if (recCpu) set('cpus')(String(recCpu)); if (recMem) set('memoryMb')(String(recMem)); }}>Use these</Button>
    </div>}
    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}</Notice>}

    <SettingsGroup title="CPU">
      {field('cpus', 'Limit', `Hard cap; the app is throttled above it. This machine has ${hostCpus} CPUs.`,
        <Select id="cpus" label="CPU limit" value={form.cpus} options={cpuOptions} onChange={set('cpus')} />)}
    </SettingsGroup>

    <SettingsGroup title="Memory">
      {field('memoryMb', 'Limit', `The app is killed (out of memory) above this. Docker on this machine has ${memLabel(hostMem)}.`,
        <Select id="memoryMb" label="Memory limit" value={form.memoryMb} options={memOptions} onChange={v => { set('memoryMb')(v); if (Number(form.memoryReservationMb) > Number(v)) set('memoryReservationMb')(''); }} />)}
      {field('memoryReservationMb', 'Reservation', 'Soft limit: when memory is tight, Docker reclaims memory down to this first.',
        <Select id="memoryReservationMb" label="Memory reservation" value={form.memoryReservationMb} options={reserveOptions} onChange={set('memoryReservationMb')} />)}
      {field('allowSwap', 'Swap', form.allowSwap ? `May use up to ${memLabel(memory)} of swap on top, if this machine has swap.` : 'No swap: hitting the limit kills the app instead of slowing it down.',
        <Switch label="Allow swap" checked={form.allowSwap} disabled={!canEdit} onChange={set('allowSwap')} />)}
      {field('pidsLimit', 'Process limit', 'Maximum processes and threads. Empty means no limit.',
        <TextInput id="pidsLimit" className="limits-num" inputMode="numeric" placeholder="No limit" value={form.pidsLimit} disabled={!canEdit} onChange={e => set('pidsLimit')(e.target.value.replace(/\D/g, ''))} />)}
    </SettingsGroup>

    <SettingsGroup title="When it crashes">
      {field('restartPolicy', 'On crash', 'Out-of-memory kills count as crashes.',
        <Select id="restartPolicy" label="On crash" value={form.restartPolicy} options={RESTART} onChange={set('restartPolicy')} />)}
      {form.restartPolicy === 'on-failure' && field('maxRestarts', 'Max restarts', 'Then it stays stopped until you start or redeploy it.',
        <TextInput id="maxRestarts" className="limits-num" inputMode="numeric" value={form.maxRestarts} disabled={!canEdit} onChange={e => set('maxRestarts')(e.target.value.replace(/\D/g, '').slice(0, 3))} />)}
    </SettingsGroup>

    <p className="limits-note"><CheckCircleIcon />Changes apply to the running container right away, without a restart. One container runs per resource; replicas and autoscaling aren't supported yet. Build limits are under <button type="button" className="inline-link" onClick={() => onNavigate?.('Build & Deploy')}>Build & Deploy</button>.</p>

    {warnings.length > 0 && <Notice tone="warning" title="Check the impact before applying"><ExclamationTriangleIcon className="visually-hidden" />{warnings.join(' ')}</Notice>}
    {changes.length > 0 && <section className="settings-group">
      <h2>Current vs proposed</h2>
      <div className="settings-card pending-changes">
        {changes.map(k => <div key={k}><span>{LABELS[k]}</span><code>{show(k, saved[k])}</code><span className="arrow">→</span><code>{show(k, form[k])}</code></div>)}
      </div>
    </section>}

    <SaveBar dirty={dirty} onDiscard={() => { setForm(saved); setErrors({}); }} summary={busy ? 'Applying…' : `${changes.length} ${changes.length === 1 ? 'change' : 'changes'} · applied live, no restart`} />
  </form>;
}

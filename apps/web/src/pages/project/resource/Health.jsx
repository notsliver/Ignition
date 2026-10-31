import { useEffect, useState } from 'react';
import { ArrowPathIcon, CheckCircleIcon, ExclamationTriangleIcon, InformationCircleIcon } from '@heroicons/react/16/solid';
import { Button, Notice, Select, Status, Switch, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { useSession, can } from '../../../app/AuthGate';
import { SaveBar, SettingsField, SettingsGroup } from '../../settings/SettingsField';
import './Operations.css';

/*
 * Health checks are run by Ignition's worker from inside the app's private network: an HTTP request,
 * a TCP connection, or a command run in the container. While enabled, a deployment only takes over once
 * it passes, and an app that keeps failing is restarted (at most three times in 15 minutes).
 */

const KINDS = [
  { value: 'http', label: 'HTTP', description: 'Request a path and check the status code' },
  { value: 'tcp', label: 'TCP', description: 'Check that the port accepts connections' },
  { value: 'command', label: 'Command', description: 'Run a command in the container; exit 0 is healthy' },
];
const STATE = {
  healthy: ['success', 'Healthy'], unhealthy: ['danger', 'Unhealthy'], starting: ['progress', 'Starting'], unknown: ['neutral', 'Not checked yet'],
};

const toForm = c => ({ enabled: c.enabled, kind: c.kind, path: c.path, port: c.port ? String(c.port) : '', expectStatus: c.expectStatus, command: c.command,
  intervalSeconds: String(c.intervalSeconds), timeoutSeconds: String(c.timeoutSeconds), retries: String(c.retries), startPeriodSeconds: String(c.startPeriodSeconds), restartUnhealthy: c.restartUnhealthy });
const toBody = f => ({ enabled: f.enabled, kind: f.kind, path: f.path, port: f.port ? Number(f.port) : null, expectStatus: f.expectStatus, command: f.command,
  intervalSeconds: Number(f.intervalSeconds) || 0, timeoutSeconds: Number(f.timeoutSeconds) || 0, retries: Number(f.retries) || 0, startPeriodSeconds: Number(f.startPeriodSeconds) || 0, restartUnhealthy: f.restartUnhealthy });
const ms = n => n < 1 ? '<1 ms' : `${n} ms`;
const digits = v => v.replace(/\D/g, '').slice(0, 4);
const at = iso => new Date(iso).toLocaleString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', month: 'short', day: 'numeric' });

export function HealthChecks({ resource }) {
  const { user } = useSession();
  const canEdit = can(user, 'resources.update');
  const { data, error, refetch } = useQuery(() => api.health(resource.id), [resource.id], { pollMs: 5000 });
  const [saved, setSaved] = useState(null);
  const [form, setForm] = useState(null);
  const [errors, setErrors] = useState({});
  const [feedback, setFeedback] = useState(null);
  const [test, setTest] = useState(null);
  const dirty = saved && JSON.stringify(form) !== JSON.stringify(saved);
  useEffect(() => { if (data && !dirty) { const f = toForm(data.config); setSaved(f); setForm(f); } }, [data]); // eslint-disable-line react-hooks/exhaustive-deps

  if (error && !data) return <div className="rp-page"><Notice tone="danger" title="Couldn't load health checks">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice></div>;
  if (!data || !form) return <div className="rp-page"><p className="ops-muted">Loading…</p></div>;

  const set = key => value => { setForm(f => ({ ...f, [key]: value })); setErrors(({ [key]: _, ...rest }) => rest); setFeedback(null); };
  const input = (key, props = {}) => <TextInput id={key} value={form[key]} disabled={!canEdit} aria-invalid={!!errors[key]}
    onChange={e => set(key)(props.numeric ? digits(e.target.value) : e.target.value)} {...(props.numeric ? { inputMode: 'numeric', className: 'ops-num' } : {})} {...props.attrs} suffix={props.suffix} />;
  const field = (key, label, hint, control) => <SettingsField label={label} hint={errors[key] ? <span className="ops-error">{errors[key]}</span> : hint} htmlFor={key}>{control}</SettingsField>;

  const { state, results } = data;
  const running = data.status === 'running';
  const [tone, label] = !saved.enabled ? ['neutral', 'Off'] : STATE[state.state] ?? STATE.unknown;
  const strip = results.filter(r => r.source === 'monitor' || r.source === 'deploy').slice(0, 40).reverse(); // manual tests aren't the app's track record
  const okCount = strip.filter(r => r.ok).length;
  const latencies = strip.filter(r => r.ok).map(r => r.latencyMs).sort((a, b) => a - b);
  const median = latencies.length ? latencies[Math.floor(latencies.length / 2)] : null;
  const events = results.filter(r => !r.ok).slice(0, 6);
  const portHint = data.resourcePort ? `Empty uses the resource's port (${data.resourcePort}).` : 'This resource has no port set, so enter one.';
  const timing = form.kind === 'command' ? 'command' : form.kind === 'tcp' ? `port ${form.port || data.resourcePort || '?'}` : `GET ${form.path}`;

  async function save(event) {
    event.preventDefault();
    setErrors({});
    try {
      const out = await api.putHealth(resource.id, toBody(form));
      const f = toForm(out.config);
      setSaved(f); setForm(f);
      setFeedback(out.config.enabled
        ? { tone: 'success', title: 'Saved', text: running ? 'The worker starts checking within a few seconds. The next deployment has to pass this check before it takes over.' : 'Checks start once the resource runs.' }
        : { tone: 'success', title: 'Health checks are off', text: 'Deployments only wait for the container to stay up.' });
      refetch();
    } catch (err) {
      setErrors(err.fields ?? {});
      setFeedback({ tone: 'danger', title: 'Couldn\'t save', text: Object.keys(err.fields ?? {}).length ? 'Check the highlighted fields.' : err.message });
    }
  }

  async function runTest() {
    setTest({ state: 'running' });
    setErrors({});
    try {
      const { result } = await api.testHealth(resource.id, toBody(form));
      setTest(result.inconclusive ? { state: 'inconclusive', text: result.inconclusive } : { state: result.ok ? 'ok' : 'fail', text: result.detail, ms: result.latencyMs });
      refetch();
    } catch (err) {
      setErrors(err.fields ?? {});
      setTest({ state: 'inconclusive', text: Object.keys(err.fields ?? {}).length ? 'Fix the highlighted fields first.' : err.message });
    }
  }

  return <form className="settings-page rp-settings ops-page" onSubmit={save}>
    <section className="settings-group">
      <div className="health-head">
        <h2>Status</h2>
        {canEdit && <Button type="button" icon={ArrowPathIcon} className={cx(test?.state === 'running' && 'is-busy')} onClick={runTest} disabled={test?.state === 'running' || !running}
          title={running ? 'Runs one check with the settings below, saved or not' : `${resource.name} isn't running`}>{test?.state === 'running' ? 'Checking…' : dirty ? 'Test these settings' : 'Check now'}</Button>}
      </div>
      <div className="settings-card ops-health-card">
        <div className="ops-health-summary">
          <Status tone={tone}>{label}</Status>
          {saved.enabled && <span className="ops-muted">
            {state.lastCheckedAt ? `Checked ${timeAgo(state.lastCheckedAt).toLowerCase()}` : running ? 'Waiting for the first check' : `${resource.name} isn't running`}
            {state.state === 'unhealthy' && state.failures > 0 && ` · ${state.failures} failed in a row`}
            {median != null && ` · ${ms(median)} typical`}
          </span>}
        </div>
        {strip.length > 0
          ? <div className="health-strip ops-strip" role="img" aria-label={`${okCount} of the last ${strip.length} checks passed`} style={{ gridTemplateColumns: `repeat(40, 1fr)` }}>
            {Array.from({ length: 40 - strip.length }, (_, i) => <span key={`e${i}`} className="empty" />)}
            {strip.map(r => <span key={r.id} className={r.ok ? 'ok' : 'fail'} title={`${at(r.checkedAt)} · ${r.detail}${r.ok ? ` · ${ms(r.latencyMs)}` : ''}`} />)}
          </div>
          : <p className="ops-muted ops-pad">{saved.enabled ? 'No results yet.' : 'Turn health checks on to see results here.'}</p>}
        {test && test.state !== 'running' && <p className={cx('health-result', test.state)}>
          {test.state === 'ok' ? <CheckCircleIcon /> : test.state === 'fail' ? <ExclamationTriangleIcon /> : <InformationCircleIcon />}
          <span>{test.text}{test.state === 'ok' && ` · ${ms(test.ms)}`}</span>
        </p>}
      </div>
    </section>

    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}</Notice>}

    <SettingsGroup title="Check">
      {field('enabled', 'Enabled', 'While on, a deployment must pass this check before it gets traffic.',
        <Switch label="Health checks enabled" checked={form.enabled} disabled={!canEdit} onChange={set('enabled')} />)}
      {field('kind', 'Type', null, <Select id="kind" label="Type" value={form.kind} options={KINDS} onChange={set('kind')} />)}
      {form.kind === 'http' && field('path', 'Path', 'Requested from inside the private network, without following redirects.', input('path', { attrs: { className: 'mono', placeholder: '/health', spellCheck: false } }))}
      {form.kind === 'http' && field('expectStatus', 'Healthy status codes', 'e.g. 200, 200-299, 2xx or 200,204.', input('expectStatus', { attrs: { className: 'mono', placeholder: '200-399', spellCheck: false } }))}
      {form.kind !== 'command' && field('port', 'Port', portHint, input('port', { numeric: true, attrs: { placeholder: data.resourcePort ? String(data.resourcePort) : '3000' } }))}
      {form.kind === 'command' && field('command', 'Command', 'Runs with sh -c inside the container, e.g. pg_isready -U postgres. The image needs a shell.',
        input('command', { attrs: { className: 'mono', placeholder: 'pg_isready -U postgres', spellCheck: false } }))}
    </SettingsGroup>

    <SettingsGroup title="Timing">
      {field('intervalSeconds', 'Interval', 'How often a running app is checked.', input('intervalSeconds', { numeric: true, suffix: 'seconds' }))}
      {field('timeoutSeconds', 'Timeout', 'A check that takes longer fails.', input('timeoutSeconds', { numeric: true, suffix: 'seconds' }))}
      {field('retries', 'Failures before unhealthy', 'Checks that must fail in a row.', input('retries', { numeric: true }))}
      {field('startPeriodSeconds', 'Start period', 'Time to boot after starting; failures don\'t count yet.', input('startPeriodSeconds', { numeric: true, suffix: 'seconds' }))}
    </SettingsGroup>

    <SettingsGroup title="When it's unhealthy">
      {field('restartUnhealthy', 'Restart automatically', 'At most 3 restarts in 15 minutes, so a broken app isn\'t restarted forever.',
        <Switch label="Restart unhealthy containers" checked={form.restartUnhealthy} disabled={!canEdit} onChange={set('restartUnhealthy')} />)}
    </SettingsGroup>

    {saved.enabled && events.length > 0 && <section className="settings-group">
      <h2>Recent problems</h2>
      <div className="settings-card ops-events">
        {events.map(e => <div key={e.id} className="ops-event">
          <span className={cx('ops-event-dot', e.source === 'restart' ? 'warn' : 'bad')} />
          <span className="ops-event-text">{e.detail}{e.source === 'deploy' && <em> · during a deployment</em>}{e.source === 'test' && <em> · manual check</em>}</span>
          <time dateTime={e.checkedAt} title={at(e.checkedAt)}>{timeAgo(e.checkedAt)}</time>
        </div>)}
      </div>
    </section>}

    <p className="ops-note"><InformationCircleIcon />Checks come from Ignition's worker inside {resource.name}'s private network, so they test the app itself, not its domain or published port. During a deployment the check runs every few seconds; it currently tests {timing}.</p>

    {canEdit && <SaveBar dirty={dirty} onDiscard={() => { setForm(saved); setErrors({}); }} />}
  </form>;
}

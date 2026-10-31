import { useState } from 'react';
import { ExclamationTriangleIcon, KeyIcon, LockClosedIcon, NoSymbolIcon, ShieldCheckIcon, SignalIcon, XCircleIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, Notice, Select, Status, Switch, TextInput } from '../../ui';
import { api } from '../../api/client';
import { timeAgo, useQuery } from '../../api/hooks';
import { useSession } from '../../app/AuthGate';
import { SaveBar, SettingsField, SettingsGroup } from '../settings/SettingsField';
import { useSettingsForm } from '../project/resource/shared';
import { isAdmin, labelsToText, methodLabel, textToLabels } from './model';
import { ConfirmDialog } from './shared';

/* ------------------------------- Access & Security ------------------------------ */

const trustText = {
  trusted: ['success', 'Trusted'],
  revoked: ['danger', 'Credentials revoked'],
  identity_changed: ['danger', 'Host identity changed'],
};
const capabilityLabels = { docker: 'Docker runtime', inspect: 'Inspection', logs: 'Logs', metrics: 'Metrics', storage: 'Storage', networkDiagnostics: 'Network diagnostics', maintenance: 'Maintenance', deploy: 'Deployments' };

export function Access({ server, onChanged, setFeedback }) {
  const { user } = useSession();
  const admin = isAdmin(user);
  const { data, error, refetch } = useQuery(() => api.serverAccess(server.id), [server.id], { pollMs: 15000 });
  const [rotating, setRotating] = useState(false);
  const [revoking, setRevoking] = useState(false);
  const a = data?.access;
  const id = a?.identity;
  const [tone, label] = trustText[a?.trust] ?? ['neutral', '…'];

  return <>
    <div className="rp-intro"><p>How Ignition connects to {server.name} and who can act on it. This is about this server only; sign-in, sessions and API tokens are under Settings › Authentication & Security.</p></div>
    {error && <Notice tone="danger" title="Couldn't load access details">{error.message}</Notice>}
    <section className="ov-hero">
      <div className="ov-status">
        <span className="ov-label">Connection trust</span>
        <Status tone={tone}>{label}</Status>
        <dl>
          <div><dt>Method</dt><dd>{methodLabel[server.connectionMethod]}</dd></div>
          {a?.version && <div><dt>Version</dt><dd>Docker {a.version.docker} · API {a.version.api}</dd></div>}
          {!server.isLocal && <div><dt>Credentials</dt><dd className={id?.credentialsStored ? undefined : 'bad'}>{id?.credentialsStored ? 'Stored, encrypted' : 'None (revoked)'}</dd></div>}
          {!server.isLocal && <div><dt>Last rotated</dt><dd>{id?.rotatedAt ? timeAgo(id.rotatedAt) : 'Never'}</dd></div>}
        </dl>
      </div>
      {admin && !server.isLocal && <div className="ov-actions">
        <span className="ov-label">Credentials</span>
        <div>
          <Button icon={KeyIcon} variant="primary" onClick={() => setRotating(true)}>{id?.credentialsStored ? 'Rotate credentials' : 'Provide new credentials'}</Button>
          {id?.credentialsStored && <Button icon={NoSymbolIcon} className="danger" onClick={() => setRevoking(true)}>Revoke</Button>}
        </div>
      </div>}
    </section>

    {server.isLocal ? <Notice tone="info" icon={LockClosedIcon} title="Local Docker socket">{id?.description} {a?.rotation}</Notice>
      : id && <section className="ov-card">
        <header><h2>Identity</h2></header>
        <dl className="sv-facts">
          <div><dt>Engine address</dt><dd className="mono">{id.endpoint}</dd></div>
          <div><dt>Host certificate (SHA-256)</dt><dd className="mono sv-break">{id.hostFingerprint}</dd></div>
          <div><dt>Client certificate</dt><dd>{id.clientCertificate ? `${id.clientCertificate.subject}, issued by ${id.clientCertificate.issuer}` : '—'}</dd></div>
          <div><dt>Client certificate expires</dt><dd className={id.clientCertificate && new Date(id.clientCertificate.notAfter) - Date.now() < 14 * 864e5 ? 'bad' : undefined}>{id.clientCertificate ? new Date(id.clientCertificate.notAfter).toLocaleDateString() : '—'}</dd></div>
          <div><dt>Private key</dt><dd>{id.keyStorage}</dd></div>
          {id.revokedAt && <div><dt>Revoked</dt><dd className="bad">{new Date(id.revokedAt).toLocaleString()}</dd></div>}
        </dl>
      </section>}

    <div className="ov-columns">
      <div className="ov-stack">
        <section className="ov-card">
          <header><h2>Who can do what</h2></header>
          <ul className="sv-mini-list">{(a?.permissions ?? []).map(p => <li key={p.role}><strong>{p.role}</strong><span>{p.can}</span></li>)}</ul>
        </section>
        <section className="ov-card">
          <header><h2>Verified capabilities</h2></header>
          <ul className="sv-mini-list">{Object.entries(capabilityLabels).map(([k, l]) => { const c = server.capabilities?.[k]; return <li key={k}>
            <strong><Status tone={c?.available ? 'success' : 'neutral'}>{l}</Status></strong><span>{c?.detail ?? 'Not verified yet.'}</span></li>; })}</ul>
        </section>
      </div>
      <div className="ov-stack">
        <section className="ov-card">
          <header><h2>Recent connection failures</h2></header>
          {(a?.failures ?? []).length ? <ol className="ov-events">{a.failures.map(e => <li key={e.id}><XCircleIcon className="warning" /><span>{e.message}</span><time>{timeAgo(e.at)}</time></li>)}</ol>
            : <p className="ov-empty">{data ? 'None recorded.' : 'Loading…'}</p>}
        </section>
        <section className="ov-card">
          <header><h2>Security events</h2></header>
          {(a?.events ?? []).filter(e => e.kind === 'security').length ? <ol className="ov-events">{a.events.filter(e => e.kind === 'security').map(e => <li key={e.id}>
            {e.level === 'info' ? <ShieldCheckIcon className="success" /> : <ExclamationTriangleIcon className="warning" />}<span>{e.message} · {e.actor}</span><time>{timeAgo(e.at)}</time></li>)}</ol>
            : <p className="ov-empty">{data ? 'No security events.' : 'Loading…'}</p>}
        </section>
        {admin && <section className="ov-card">
          <header><h2>Audit</h2></header>
          {(a?.audit ?? []).length ? <ol className="ov-events">{a.audit.slice(0, 15).map(e => <li key={e.id}><ShieldCheckIcon className="neutral" /><span>{e.action.replace('server.', '').replaceAll('_', ' ')} · {e.actor}</span><time>{timeAgo(e.createdAt)}</time></li>)}</ol>
            : <p className="ov-empty">{data ? 'No audited actions yet.' : 'Loading…'}</p>}
        </section>}
      </div>
    </div>
    {rotating && <RotateDialog server={server} onClose={() => setRotating(false)} onDone={s => { setRotating(false); onChanged(s); refetch(); setFeedback({ tone: 'success', title: 'Credentials rotated', text: 'The new certificate was verified against the engine before the old one was replaced.' }); }} />}
    {revoking && <ConfirmDialog danger icon={NoSymbolIcon} title={`Revoke Ignition's credentials for ${server.name}?`} phrase={server.name} confirmLabel="Revoke"
      description="The stored client key is deleted immediately and Ignition stops connecting. The server stays registered so you can rotate in new credentials later. This doesn't revoke the certificate at its CA: to cut access completely, also stop trusting it on the daemon."
      onClose={() => setRevoking(false)} onConfirm={async () => { const out = await api.revokeServerCredentials(server.id, server.name); setRevoking(false); onChanged(out.server); refetch(); setFeedback({ tone: 'warning', title: 'Credentials revoked', text: 'Ignition can no longer reach this server.' }); }} />}
  </>;
}

function RotateDialog({ server, onClose, onDone }) {
  const [form, setForm] = useState({ caCert: '', clientCert: '', clientKey: '' });
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [errors, setErrors] = useState({});
  const [changed, setChanged] = useState(null); // fingerprint the engine now presents
  const [accept, setAccept] = useState('');
  const set = k => e => setForm(f => ({ ...f, [k]: e.target.value }));
  async function go() {
    setPending(true); setError(''); setErrors({});
    try {
      const body = { ...form, ...(changed && accept === changed.slice(0, 8) ? { acceptFingerprint: changed } : {}) };
      const out = await api.rotateServerCredentials(server.id, body);
      onDone(out.server);
    } catch (e) {
      if (e.code === 'identity_changed') { setChanged(e.body?.fingerprint ?? null); setAccept(''); setError(e.message); setPending(false); return; }
      setErrors(e.fields ?? {}); setError(e.message); setPending(false);
    }
  }
  return <Dialog open onClose={onClose} title={`Rotate credentials for ${server.name}`} icon={KeyIcon}
    description="Ignition verifies the new certificate and key against the engine first. If that fails, the current credentials stay in place, so a mistake can't lock you out."
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" disabled={pending || !form.clientCert || !form.clientKey} onClick={go}>{pending ? 'Verifying…' : 'Verify and rotate'}</Button></>}>
    <div className="sv-form">
      <FormField label="New client certificate (cert.pem)" htmlFor="rc" error={errors.clientCert}><textarea id="rc" className="labels-input" rows={3} value={form.clientCert} onChange={set('clientCert')} spellCheck={false} /></FormField>
      <FormField label="New client private key (key.pem)" htmlFor="rk" error={errors.clientKey} hint="Encrypted before it's stored; never shown again."><textarea id="rk" className="labels-input sv-secret" rows={3} value={form.clientKey} onChange={set('clientKey')} spellCheck={false} autoComplete="off" /></FormField>
      <FormField label="New CA certificate (optional)" htmlFor="rca" error={errors.caCert} hint="Only if the daemon's CA changed. Leave empty to keep the current one."><textarea id="rca" className="labels-input" rows={2} value={form.caCert} onChange={set('caCert')} spellCheck={false} /></FormField>
      {error && <Notice tone="danger" title="Not rotated">{error}</Notice>}
      {changed && <Notice tone="warning" title="The engine presents a different host certificate">
        <span className="mono sv-break">{changed}</span> Compare it on the server (openssl x509 -in server-cert.pem -noout -fingerprint -sha256). Accept it only if the daemon's certificate was intentionally replaced.
      </Notice>}
      {changed && <FormField label={`Type ${changed.slice(0, 8)} to accept the new host certificate`} htmlFor="racc" hint="Only do this if the daemon's certificate was intentionally replaced.">
        <TextInput id="racc" value={accept} onChange={e => setAccept(e.target.value)} autoComplete="off" /></FormField>}
    </div>
  </Dialog>;
}

/* ------------------------------------ Settings ---------------------------------- */

export function Settings({ server, onChanged, actions, setFeedback }) {
  const { user } = useSession();
  const admin = isAdmin(user);
  const initial = {
    name: server.name, hostname: server.hostname, description: server.description, environment: server.environment, location: server.location ?? '', labels: labelsToText(server.labels), notes: server.notes,
    connectTimeoutSeconds: String(server.settings.connectTimeoutSeconds), offlineAfterFailures: String(server.settings.offlineAfterFailures),
    collectMetrics: server.settings.collectMetrics, hostProbe: server.settings.hostProbe ?? true, diagnosticOutput: server.settings.diagnosticOutput,
  };
  const form = useSettingsForm(initial);
  const [errors, setErrors] = useState({});
  const [endpoint, setEndpoint] = useState(null);
  const v = form.values;

  async function save(e) {
    e.preventDefault();
    setErrors({});
    try {
      const out = await api.updateServer(server.id, {
        name: v.name, hostname: v.hostname, description: v.description, environment: v.environment, location: v.location, labels: textToLabels(v.labels), notes: v.notes,
        settings: { connectTimeoutSeconds: Number(v.connectTimeoutSeconds), offlineAfterFailures: Number(v.offlineAfterFailures), collectMetrics: v.collectMetrics, hostProbe: v.hostProbe, diagnosticOutput: v.diagnosticOutput },
      });
      form.save();
      onChanged(out.server);
      setFeedback({ tone: 'success', title: 'Settings saved' });
    } catch (err) {
      setErrors(err.fields ?? {});
      setFeedback({ tone: 'danger', title: 'Couldn\'t save', text: err.message });
    }
  }
  const field = (k, label, hint, props = {}) => <SettingsField label={label} hint={errors[k] ? <span className="rp-error">{errors[k]}</span> : hint} htmlFor={k}>
    <TextInput {...form.bindInput(k)} disabled={!admin} {...props} />
  </SettingsField>;

  return <form className="settings-page rp-settings sv-settings" onSubmit={save}>
    {!admin && <Notice tone="info" title="Read-only">Only admins can change server settings.</Notice>}
    <SettingsGroup title="General">
      {field('name', 'Display name', 'Shown everywhere in Ignition.', { maxLength: 64 })}
      {field('hostname', 'Hostname', 'For people; Ignition connects to the address under Connection.', { maxLength: 253 })}
      {field('environment', 'Environment', 'e.g. production, staging.', { maxLength: 40 })}
      {field('location', 'Location', 'Region, datacenter or rack, e.g. eu-west · fra1. Servers can be grouped by it.', { maxLength: 64 })}
      {field('description', 'Description', null, { maxLength: 500 })}
      <SettingsField label="Labels" hint={errors.labels ? <span className="rp-error">{errors.labels}</span> : 'One per line: key=value.'} htmlFor="labels">
        <textarea id="labels" className="labels-input" rows={3} value={v.labels} onChange={e => form.set('labels')(e.target.value)} disabled={!admin} />
      </SettingsField>
      <SettingsField label="Notes" hint="For your team: owners, runbooks, quirks." htmlFor="notes">
        <textarea id="notes" className="labels-input" rows={3} value={v.notes} onChange={e => form.set('notes')(e.target.value)} disabled={!admin} maxLength={4000} />
      </SettingsField>
    </SettingsGroup>

    <SettingsGroup title="Connection" description="Changing how Ignition connects is verified before it's applied.">
      <SettingsField label="Method"><span className="readonly-value">{methodLabel[server.connectionMethod]}</span></SettingsField>
      {!server.isLocal && <SettingsField label="Engine address" hint="Moving to a new address requires the same host certificate.">
        <span className="readonly-value mono">{server.endpoint}</span>{admin && <Button type="button" onClick={() => setEndpoint(server.endpoint)}>Change…</Button>}
      </SettingsField>}
      {field('connectTimeoutSeconds', 'Connection timeout', 'Seconds per check: dial, TLS handshake and API call. 1–60.', { type: 'number', min: 1, max: 60, suffix: 's' })}
      {field('offlineAfterFailures', 'Offline after', 'Consecutive failed checks before the server is marked offline (checked every 15 s). One success brings it back.', { type: 'number', min: 1, max: 10, suffix: 'checks' })}
      <SettingsField label="Verify now" hint="Runs a connection check with the saved settings.">
        <Button type="button" icon={SignalIcon} onClick={actions.test} disabled={!actions.dev || !!actions.busy}>{actions.busy ? 'Testing…' : 'Test connection'}</Button>
      </SettingsField>
      {!server.isLocal && <SettingsField label="Credentials" hint="Client certificate and key."><span className="readonly-value">{server.hasCredentials ? 'Stored, encrypted' : 'None'}</span></SettingsField>}
    </SettingsGroup>

    <SettingsGroup title="Deployment">
      <SettingsField label="New deployments" hint={server.deploymentEligibility.reason}>
        {server.isLocal ? <Switch checked={server.deploymentsEnabled} disabled={!admin} onChange={actions.toggleDeployments} label="Accept new deployments" /> : <span className="readonly-value">Not supported on remote servers</span>}
      </SettingsField>
      <SettingsField label="Maintenance" hint="Pauses deployments and scheduled server tasks; workloads keep running.">
        <span className="readonly-value">{server.maintenance ? 'On' : 'Off'}</span>{admin && <Button type="button" onClick={actions.maintenance}>{server.maintenance ? 'Exit…' : 'Enter…'}</Button>}
      </SettingsField>
      <SettingsField label="Runtime capabilities" hint="Verified by the last successful check.">
        <span className="readonly-value">{Object.entries(server.capabilities ?? {}).filter(([, c]) => c.available).map(([k]) => capabilityLabels[k]).join(', ') || 'None verified'}</span>
      </SettingsField>
    </SettingsGroup>

    <SettingsGroup title="Advanced">
      <SettingsField label="Collect host metrics" hint="Sample this server every 15 seconds for Monitoring. Kept 7 days.">
        <Switch checked={v.collectMetrics} disabled={!admin} onChange={form.set('collectMetrics')} label="Collect host metrics" />
      </SettingsField>
      <SettingsField label="Host probe" hint={<>Runs a tiny <code>busybox</code> container named <code>ignition-hostprobe</code> on the host's network to read host CPU, memory, disk and network from <code>/proc</code>. No capabilities, read-only, 16 MB. Without it, {server.isLocal ? 'network isn\'t measured' : 'only engine figures are available'}. Turning it off removes the container.</>}>
        <Switch checked={v.hostProbe} disabled={!admin || !v.collectMetrics} onChange={form.set('hostProbe')} label="Run the host probe" />
      </SettingsField>
      <SettingsField label="Diagnostic output" hint="How much raw output each diagnostic check keeps. Secrets are masked either way.">
        <Select label="Diagnostic output" value={v.diagnosticOutput} onChange={form.set('diagnosticOutput')} options={[{ value: 'summary', label: 'Summary (2 KB per check)' }, { value: 'verbose', label: 'Verbose (16 KB per check)' }]} />
      </SettingsField>
    </SettingsGroup>
    {admin && <SaveBar dirty={form.dirty} onDiscard={() => { form.discard(); setErrors({}); }} />}
    {endpoint != null && <EndpointDialog server={server} onClose={() => setEndpoint(null)} onDone={s => { setEndpoint(null); onChanged(s); setFeedback({ tone: 'success', title: 'Address changed', text: 'The engine at the new address presented the same host certificate.' }); }} />}
  </form>;
}

function EndpointDialog({ server, onClose, onDone }) {
  const [value, setValue] = useState(server.endpoint);
  return <ConfirmDialog title="Change the engine address" confirmLabel="Verify and change" onClose={onClose}
    description="Ignition connects to the new address with the current credentials and only switches if the engine there presents the same host certificate. If it can't connect, nothing changes."
    onConfirm={async () => onDone((await api.changeServerEndpoint(server.id, value)).server)}>
    <FormField label="New address" htmlFor="newep" hint="host:port"><TextInput id="newep" value={value} onChange={e => setValue(e.target.value)} autoFocus /></FormField>
  </ConfirmDialog>;
}

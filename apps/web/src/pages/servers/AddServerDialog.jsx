import { useState } from 'react';
import { ArrowPathIcon, CheckCircleIcon, ExclamationTriangleIcon, LockClosedIcon, MinusCircleIcon, ServerStackIcon, XCircleIcon } from '@heroicons/react/16/solid';
import { Button, Checkbox, Dialog, FormField, Notice, Status, TextInput, cx } from '../../ui';
import { api } from '../../api/client';
import { CopyButton } from '../project/resource/shared';
import { textToLabels } from './model';
import { IdentityFields } from './shared';

/*
 * Five steps: identity, connection, verification, capabilities, review. Nothing is saved until the
 * last step, and registering re-verifies the connection and requires the engine to present the
 * exact certificate the operator reviewed. The private key lives only in this dialog's state and
 * in the request; the API seals it and never returns it.
 */

const steps = ['Identity', 'Connection', 'Verification', 'Capabilities', 'Review'];

const methods = [
  { id: 'docker-tls', title: 'Docker Engine over mutual TLS', text: 'IGNITION connects to dockerd on port 2376 with a client certificate. The engine proves its identity with a certificate from your CA.', supported: true },
  { id: 'local', title: 'Local Docker socket', text: 'Already registered as “This machine”: the host IGNITION runs on.', supported: false },
  { id: 'ssh', title: 'SSH', text: 'Not supported in this version. IGNITION doesn\'t run commands over SSH.', supported: false },
  { id: 'agent', title: 'IGNITION agent', text: 'Not available: IGNITION has no agent. Remote hosts are reached through the Docker Engine API.', supported: false },
];

const capabilityLabels = { docker: 'Docker runtime', inspect: 'Resource inspection', logs: 'Log access', metrics: 'Metrics collection', storage: 'Storage inspection',
  networkDiagnostics: 'Network diagnostics', maintenance: 'Maintenance operations', deploy: 'Deployments' };

const daemonJson = `{
  "tlsverify": true,
  "tlscacert": "/etc/docker/tls/ca.pem",
  "tlscert": "/etc/docker/tls/server-cert.pem",
  "tlskey": "/etc/docker/tls/server-key.pem",
  "hosts": ["unix:///var/run/docker.sock", "tcp://0.0.0.0:2376"]
}`;
const certCommands = `# On the server, in /etc/docker/tls (keep ca-key.pem offline afterwards)
openssl genrsa -out ca-key.pem 4096
openssl req -new -x509 -days 825 -key ca-key.pem -sha256 -subj "/CN=docker-ca" -out ca.pem
openssl genrsa -out server-key.pem 4096
openssl req -new -key server-key.pem -subj "/CN=HOST" -out server.csr
echo "subjectAltName=DNS:HOST,IP:ADDRESS" > server.ext
openssl x509 -req -days 825 -sha256 -in server.csr -CA ca.pem -CAkey ca-key.pem -CAcreateserial -out server-cert.pem -extfile server.ext
openssl genrsa -out key.pem 4096
openssl req -new -key key.pem -subj "/CN=ignition" -out client.csr
echo "extendedKeyUsage=clientAuth" > client.ext
openssl x509 -req -days 825 -sha256 -in client.csr -CA ca.pem -CAkey ca-key.pem -CAcreateserial -out cert.pem -extfile client.ext
# If dockerd is started by systemd with -H fd://, remove that flag (hosts now come from daemon.json), then:
systemctl restart docker`;

const stepIcon = { passed: CheckCircleIcon, warning: ExclamationTriangleIcon, failed: XCircleIcon, skipped: MinusCircleIcon };

export function AddServerDialog({ onClose, onCreated }) {
  const [step, setStep] = useState(0);
  const [form, setForm] = useState({ name: '', hostname: '', description: '', environment: '', location: '', labels: '' });
  const [conn, setConn] = useState({ endpoint: '', caCert: '', clientCert: '', clientKey: '' });
  const [errors, setErrors] = useState({});
  const [verify, setVerify] = useState(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [confirmed, setConfirmed] = useState(false);
  const [created, setCreated] = useState(null);
  const set = k => e => setForm(f => ({ ...f, [k]: e.target.value }));
  const setC = k => e => { setConn(c => ({ ...c, [k]: e.target.value })); setVerify(null); setConfirmed(false); };

  function next() {
    setError('');
    if (step === 0) {
      const errs = {};
      if (!form.name.trim()) errs.name = 'Give the server a name.';
      setErrors(errs);
      if (Object.keys(errs).length) return;
    }
    if (step === 1) {
      const errs = {};
      if (!conn.endpoint.trim()) errs.endpoint = 'Enter the engine\'s address.';
      if (!conn.caCert.trim()) errs.caCert = 'Paste ca.pem.';
      if (!conn.clientCert.trim()) errs.clientCert = 'Paste cert.pem.';
      if (!conn.clientKey.trim()) errs.clientKey = 'Paste key.pem.';
      setErrors(errs);
      if (Object.keys(errs).length) return;
      runVerify();
    }
    setStep(s => s + 1);
  }

  async function runVerify() {
    setPending(true);
    setVerify(null);
    setError('');
    try {
      setVerify(await api.verifyServer(conn));
    } catch (e) {
      if (e.fields && Object.keys(e.fields).length) { setErrors(e.fields); setStep(1); }
      setError(e.message);
    } finally {
      setPending(false);
    }
  }

  async function register() {
    setPending(true);
    setError('');
    try {
      const out = await api.createServer({ ...form, labels: textToLabels(form.labels), ...conn, expectedFingerprint: verify.fingerprint });
      setConn({ endpoint: '', caCert: '', clientCert: '', clientKey: '' }); // drop the key as soon as it's stored
      setCreated(out.server);
    } catch (e) {
      setError(e.fields && Object.keys(e.fields).length ? Object.values(e.fields).join(' ') : e.message);
    } finally {
      setPending(false);
    }
  }

  const canNext = step === 2 ? verify?.ok : step === 3 ? true : true;
  const footer = created
    ? <><Button onClick={onClose}>Close</Button><Button variant="primary" onClick={() => onCreated(created)}>Open server</Button></>
    : <>
      <Button onClick={step ? () => setStep(s => s - 1) : onClose} disabled={pending}>{step ? 'Back' : 'Cancel'}</Button>
      {step < 4
        ? <Button variant="primary" onClick={next} disabled={pending || !canNext}>{step === 1 ? 'Verify connection' : 'Continue'}</Button>
        : <Button variant="primary" icon={ServerStackIcon} onClick={register} disabled={pending || !confirmed}>{pending ? 'Registering…' : 'Register server'}</Button>}
    </>;

  return <Dialog open onClose={onClose} title="Add server" className="sv-wizard" footer={footer}>
    <ol className="sv-steps" aria-label="Steps">
      {steps.map((s, i) => <li key={s} className={cx(i === step && !created && 'current', (i < step || created) && 'done')} aria-current={i === step ? 'step' : undefined}>{s}</li>)}
    </ol>

    {created ? <div className="sv-created">
      <CheckCircleIcon />
      <strong>{created.name} is registered</strong>
      <p>IGNITION verified the connection again while registering, and the worker will check it every 15 seconds from now on. It's monitoring-only: deployments keep running on this machine.</p>
    </div>

    : step === 0 ? <form className="sv-form" onSubmit={e => { e.preventDefault(); next(); }}>
      <p className="sv-step-intro">How the server shows up in IGNITION. You can change this later.</p>
      <IdentityFields form={form} set={set} errors={errors} hostnameHint="Leave empty to use the address you enter next." />
    </form>

    : step === 1 ? <div className="sv-form">
      <p className="sv-step-intro">Choose how IGNITION reaches the server. Only methods this version supports can be selected.</p>
      <div className="sv-methods" role="radiogroup" aria-label="Connection method">
        {methods.map(m => <div key={m.id} role="radio" aria-checked={m.id === 'docker-tls'} aria-disabled={!m.supported} className={cx('sv-method', m.id === 'docker-tls' && 'selected', !m.supported && 'disabled')}>
          <strong>{m.title}</strong><span>{m.text}</span>
        </div>)}
      </div>
      <details className="sv-howto">
        <summary>Set up TLS on the server</summary>
        <p>Docker refuses unauthenticated TCP access only when it's started with <code>tlsverify</code>. Never expose port 2375 or the Docker socket without TLS: that's root access to the host for anyone who can reach it.</p>
        <p className="sv-howto-label">1. Create a CA, a server certificate for the host's name and address, and a client certificate for IGNITION. Replace HOST and ADDRESS.<CopyButton value={certCommands} label="Copy commands" /></p>
        <pre className="sv-pre">{certCommands}</pre>
        <p className="sv-howto-label">2. Point dockerd at them in /etc/docker/daemon.json and restart Docker.<CopyButton value={daemonJson} label="Copy daemon.json" /></p>
        <pre className="sv-pre">{daemonJson}</pre>
        <p className="sv-howto-label">3. Allow TCP 2376 from IGNITION's address only, then paste ca.pem, cert.pem and key.pem below.</p>
      </details>
      <FormField label="Engine address" htmlFor="ep" error={errors.endpoint} hint="host:port. Port 2376 if omitted. Loopback and link-local addresses aren't accepted.">
        <TextInput id="ep" value={conn.endpoint} onChange={setC('endpoint')} placeholder="docker1.example.com:2376" autoComplete="off" />
      </FormField>
      <FormField label="CA certificate (ca.pem)" htmlFor="ca" error={errors.caCert} hint="Authenticates the server: IGNITION only trusts an engine certificate signed by this CA and valid for the address.">
        <textarea id="ca" className="labels-input" rows={3} value={conn.caCert} onChange={setC('caCert')} placeholder="-----BEGIN CERTIFICATE-----" spellCheck={false} />
      </FormField>
      <FormField label="Client certificate (cert.pem)" htmlFor="cc" error={errors.clientCert}>
        <textarea id="cc" className="labels-input" rows={3} value={conn.clientCert} onChange={setC('clientCert')} placeholder="-----BEGIN CERTIFICATE-----" spellCheck={false} />
      </FormField>
      <FormField label="Client private key (key.pem)" htmlFor="ck" error={errors.clientKey} hint="Encrypted with IGNITION_SECRET_KEY before it's stored, and never shown again.">
        <textarea id="ck" className="labels-input sv-secret" rows={3} value={conn.clientKey} onChange={setC('clientKey')} placeholder="-----BEGIN PRIVATE KEY-----" spellCheck={false} autoComplete="off" />
      </FormField>
    </div>

    : step === 2 ? <div className="sv-form">
      <p className="sv-step-intro">IGNITION is connecting to <code>{conn.endpoint}</code> from its worker. Nothing is saved yet.</p>
      {error && <Notice tone="danger" title="Verification couldn't run">{error}</Notice>}
      {pending && !verify && <p className="muted"><ArrowPathIcon className="inline-icon sv-spin" />Testing network, identity, authentication and Docker…</p>}
      {verify && <>
        <ul className="sv-verify">
          {verify.steps.map(s => { const Icon = stepIcon[s.status] ?? MinusCircleIcon; return <li key={s.id} className={s.status}>
            <Icon /><div><strong>{s.title}</strong><span>{s.detail}</span></div>{s.ms ? <small>{s.ms} ms</small> : null}
          </li>; })}
        </ul>
        {verify.ok
          ? <Notice tone="success" title="Connection verified">Continue to see what IGNITION can do on this server.</Notice>
          : <Notice tone="warning" title="Verification failed" action="Try again" onAction={runVerify}>Fix the failing step (go Back to change the address or certificates), then try again. Nothing was registered.</Notice>}
      </>}
    </div>

    : step === 3 ? <div className="sv-form">
      <p className="sv-step-intro">What the checks proved works on {verify?.engine?.name || conn.endpoint}. Features are only listed as available when they were verified.</p>
      <ul className="sv-caps">
        {Object.entries(capabilityLabels).map(([k, label]) => { const c = verify?.capabilities?.[k]; return <li key={k}>
          <Status tone={c?.available ? 'success' : 'neutral'}>{label}</Status><span>{c?.detail ?? 'Not verified.'}</span>
        </li>; })}
      </ul>
    </div>

    : <div className="sv-form">
      <dl className="sv-review">
        <div><dt>Name</dt><dd>{form.name}</dd></div>
        <div><dt>Hostname</dt><dd>{form.hostname || verify?.endpoint?.split(':')[0]}</dd></div>
        {form.environment && <div><dt>Environment</dt><dd>{form.environment}</dd></div>}
        {form.location && <div><dt>Location</dt><dd>{form.location}</dd></div>}
        <div><dt>Connection</dt><dd>Docker Engine over mutual TLS · {verify?.endpoint}</dd></div>
        <div><dt>Engine</dt><dd>Docker {verify?.engine?.version} on {verify?.engine?.os} ({verify?.engine?.arch})</dd></div>
        <div><dt>Client certificate</dt><dd>{verify?.clientCertificate?.subject} · expires {verify?.clientCertificate && new Date(verify.clientCertificate.notAfter).toLocaleDateString()}</dd></div>
        <div><dt>Deployments</dt><dd>Not eligible: the deploy engine only targets this machine. The server is monitored, inspected and cleaned up.</dd></div>
      </dl>
      <div className="sv-fingerprint">
        <LockClosedIcon />
        <div>
          <strong>Host identity</strong>
          <span>SHA-256 of the engine's certificate. IGNITION will refuse to connect if it ever changes, until you rotate credentials.</span>
          <code>{verify?.fingerprint}</code>
          <span>Compare it on the server: <code>openssl x509 -in /etc/docker/tls/server-cert.pem -noout -fingerprint -sha256</code></span>
        </div>
      </div>
      <Notice tone="info" title="Security">The client certificate grants root-equivalent control of this Docker host. Its key is stored encrypted; only admins can rotate or revoke it, and every change is audited.</Notice>
      <div className="sv-confirm-row">
        <Checkbox label="I confirm this fingerprint matches the server" checked={confirmed} onChange={setConfirmed} />
        <span className="sv-confirm-label" onClick={() => setConfirmed(v => !v)}>I've checked the fingerprint matches the server's certificate.</span>
      </div>
      {error && <Notice tone="danger" title="Couldn't register">{error} Nothing was saved; you can try again.</Notice>}
    </div>}
  </Dialog>;
}

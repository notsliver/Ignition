import { useMemo, useState } from 'react';
import {
  ArrowTopRightOnSquareIcon, CheckCircleIcon, CircleStackIcon, CodeBracketIcon, ExclamationTriangleIcon,
  EyeIcon, EyeSlashIcon, GlobeAltIcon, LockClosedIcon, PlusIcon, StarIcon, TrashIcon,
} from '@heroicons/react/16/solid';
import { BranchIcon, Button, GitHubIcon, GitLabIcon, Meter, Notice, Select, Status, Switch, TextInput, cx } from '../../../ui';
import { SaveBar, SettingsField, SettingsGroup } from '../../settings/SettingsField';
import { CopyButton, Empty, PageIntro, Table, isPublicUrl, portOf, randomId, useLater, useSettingsForm } from './shared';

const opts = list => list.map(v => (Array.isArray(v) ? { value: v[0], label: v[1], description: v[2] } : { value: v, label: v }));

function SettingsPage({ form, children, intro }) {
  return <form className="settings-page rp-settings" onSubmit={event => { event.preventDefault(); form.save(); }}>
    {intro && <PageIntro>{intro}</PageIntro>}
    {children}
    <SaveBar dirty={form.dirty} onDiscard={form.discard} />
  </form>;
}

/* -------------------------------- Networking -------------------------------- */

export function Networking({ resource }) {
  const internal = `${resource.name}.internal`;
  const [ports, setPorts] = useState(() => [{ id: 1, port: portOf(resource), protocol: resource.type === 'database' ? 'tcp' : 'http', public: resource.type === 'application' }]);
  const [tcpProxy, setTcpProxy] = useState(false);
  const [draft, setDraft] = useState({ port: '', protocol: 'http' });
  const [error, setError] = useState('');
  const form = useSettingsForm({ allowlist: '', http2: true });

  function add(event) {
    event.preventDefault();
    const port = Number(draft.port);
    if (!Number.isInteger(port) || port < 1 || port > 65535) return setError('Ports are numbers from 1 to 65535.');
    if (ports.some(p => Number(p.port) === port)) return setError(`Port ${port} is already exposed.`);
    setPorts(list => [...list, { id: Date.now(), port: String(port), protocol: draft.protocol, public: false }]);
    setDraft({ port: '', protocol: 'http' });
    setError('');
  }

  return <form className="settings-page rp-settings" onSubmit={event => { event.preventDefault(); form.save(); }}>
    <SettingsGroup title="Private network" description="Other resources in this project reach it over the private network. Traffic never leaves your servers.">
      <SettingsField label="Internal hostname"><code className="readonly-value mono">{internal}:{portOf(resource)}</code><CopyButton value={`${internal}:${portOf(resource)}`} label="Copy internal hostname" /></SettingsField>
    </SettingsGroup>

    <section className="settings-group">
      <h2>Ports</h2>
      <Table columns={[{ label: 'Port' }, { label: 'Protocol' }, { label: 'Public' }, { label: '', className: 'actions' }]}>
        {ports.map(p => <tr key={p.id}>
          <td className="mono">{p.port}</td>
          <td className="muted">{p.protocol.toUpperCase()}</td>
          <td><Switch label={`Expose port ${p.port} publicly`} checked={p.public} onChange={value => setPorts(list => list.map(x => x.id === p.id ? { ...x, public: value } : x))} /></td>
          <td className="actions"><div><button type="button" className="icon-action danger" aria-label={`Remove port ${p.port}`} disabled={ports.length === 1} onClick={() => setPorts(list => list.filter(x => x.id !== p.id))}><TrashIcon /></button></div></td>
        </tr>)}
      </Table>
      <div className="rp-inline-form">
        <TextInput type="number" placeholder="8080" value={draft.port} aria-label="Port" onChange={event => { setDraft(d => ({ ...d, port: event.target.value })); setError(''); }} />
        <Select label="Protocol" value={draft.protocol} onChange={protocol => setDraft(d => ({ ...d, protocol }))} options={opts([['http', 'HTTP'], ['tcp', 'TCP'], ['udp', 'UDP']])} className="protocol-select" />
        <Button type="button" icon={PlusIcon} onClick={add}>Expose port</Button>
      </div>
      {error && <p className="rp-error">{error}</p>}
    </section>

    {resource.type === 'database' && <SettingsGroup title="TCP proxy" description="Connect from outside, e.g. from a database GUI on your laptop.">
      <SettingsField label="Enable TCP proxy" hint={tcpProxy ? 'Protect it with a strong password and an IP allowlist.' : 'Off by default for safety.'}>
        <Switch label="Enable TCP proxy" checked={tcpProxy} onChange={setTcpProxy} />
      </SettingsField>
      {tcpProxy && <SettingsField label="Public address"><code className="readonly-value mono">proxy.ignition.app:41823</code><CopyButton value="proxy.ignition.app:41823" label="Copy public address" /></SettingsField>}
    </SettingsGroup>}

    <SettingsGroup title="Access">
      <SettingsField label="IP allowlist" hint="CIDR ranges, comma-separated. Empty allows everyone." htmlFor="allowlist">
        <TextInput className="mono" placeholder="203.0.113.0/24" {...form.bindInput('allowlist')} />
      </SettingsField>
      {resource.type === 'application' && <SettingsField label="HTTP/2" hint="Serve public traffic over HTTP/2.">
        <Switch label="HTTP/2" checked={form.values.http2} onChange={form.set('http2')} />
      </SettingsField>}
    </SettingsGroup>
    <SaveBar dirty={form.dirty} onDiscard={form.discard} />
  </form>;
}

/* ---------------------------------- Storage --------------------------------- */

const sizes = opts([['1', '1 GB'], ['5', '5 GB'], ['10', '10 GB'], ['25', '25 GB'], ['50', '50 GB'], ['100', '100 GB']]);

export function Storage({ resource }) {
  const [volumes, setVolumes] = useState(() => resource.type === 'database'
    ? [{ id: 1, name: `${resource.name}-data`, path: '/var/lib/postgresql/data', size: '10', used: 4.1, backups: true }]
    : resource.type === 'application' ? [{ id: 1, name: `${resource.name}-uploads`, path: '/app/uploads', size: '5', used: 1.3, backups: false }] : []);
  const [adding, setAdding] = useState(false);
  const [draft, setDraft] = useState({ name: '', path: '', size: '5' });
  const [error, setError] = useState('');
  const [confirm, setConfirm] = useState(null);

  function add(event) {
    event.preventDefault();
    if (!/^[a-z0-9][a-z0-9-]*$/.test(draft.name)) return setError('Names use lowercase letters, numbers and dashes.');
    if (!draft.path.startsWith('/')) return setError('Mount paths must be absolute, like /app/data.');
    if (volumes.some(v => v.path === draft.path)) return setError('Another volume is already mounted there.');
    setVolumes(list => [...list, { id: Date.now(), ...draft, used: 0, backups: false }]);
    setDraft({ name: '', path: '', size: '5' });
    setAdding(false);
    setError('');
  }
  const update = (id, patch) => setVolumes(list => list.map(v => v.id === id ? { ...v, ...patch } : v));

  return <div className="rp-page">
    <PageIntro actions={!adding && <Button variant="primary" icon={PlusIcon} onClick={() => setAdding(true)}>Add volume</Button>}>
      Volumes keep data across deployments and restarts. Anything written outside a volume is lost when the container is replaced.
    </PageIntro>

    {adding && <form className="rp-card volume-form" onSubmit={add}>
      <TextInput placeholder="volume-name" value={draft.name} aria-label="Volume name" autoFocus onChange={event => setDraft(d => ({ ...d, name: event.target.value }))} />
      <TextInput className="mono" placeholder="/app/data" value={draft.path} aria-label="Mount path" onChange={event => setDraft(d => ({ ...d, path: event.target.value }))} />
      <Select label="Size" options={sizes} value={draft.size} onChange={size => setDraft(d => ({ ...d, size }))} />
      <Button type="button" onClick={() => { setAdding(false); setError(''); }}>Cancel</Button>
      <Button type="submit" variant="primary">Create</Button>
      {error && <p className="rp-error">{error}</p>}
    </form>}

    {volumes.length ? <div className="rp-card">
      {volumes.map(v => <div key={v.id} className="rp-row volume-row">
        <CircleStackIcon className="volume-icon" />
        <span className="rp-row-main"><strong>{v.name}</strong><span className="mono">{v.path}</span></span>
        <div className="volume-usage"><Meter label="Used" value={Math.round((v.used / Number(v.size)) * 100)} compact /><span>{v.used} of {v.size} GB</span></div>
        <Select label={`Resize ${v.name}`} options={sizes.filter(s => Number(s.value) >= Number(v.size))} value={v.size} onChange={size => update(v.id, { size })} className="volume-size" />
        <label className="volume-backups"><Switch label={`Daily backups for ${v.name}`} checked={v.backups} onChange={backups => update(v.id, { backups })} />Backups</label>
        {confirm === v.id
          ? <><Button onClick={() => setConfirm(null)}>Keep</Button><Button className="danger-solid" onClick={() => setVolumes(list => list.filter(x => x.id !== v.id))}>Delete data</Button></>
          : <button type="button" className="icon-action danger" aria-label={`Delete ${v.name}`} onClick={() => setConfirm(v.id)}><TrashIcon /></button>}
      </div>)}
    </div> : !adding && <Empty icon={CircleStackIcon} title="No volumes">This resource is stateless. Add a volume if it needs to keep files.</Empty>}
    <p className="rp-row-meta">Volumes can grow but not shrink.</p>
  </div>;
}

/* ------------------------------- Health Checks ------------------------------ */

export function HealthChecks({ resource }) {
  const later = useLater();
  const form = useSettingsForm({ enabled: true, type: resource.type === 'database' ? 'tcp' : 'http', path: '/health', port: portOf(resource), interval: '10', timeout: '5', retries: '3', startPeriod: '30' });
  const { values, set, bind, bindInput } = form;
  const [test, setTest] = useState(null);
  const results = useMemo(() => Array.from({ length: 40 }, (_, i) => (resource.status === 'unhealthy' ? i % 4 === 3 : i === 27) ? 'fail' : 'ok'), [resource.status]);

  function runTest() {
    setTest({ state: 'running' });
    later(() => setTest(resource.status === 'unhealthy' ? { state: 'fail', text: `GET ${values.path} → 503 Service Unavailable in 4812ms` } : { state: 'ok', text: values.type === 'http' ? `GET ${values.path} → 200 OK in 18ms` : `TCP :${values.port} connected in 3ms` }), 1200);
  }

  return <SettingsPage form={form} intro="Ignition only sends traffic to replicas that pass their health check, and restarts replicas that keep failing.">
    <section className="settings-group">
      <div className="health-head"><h2>Last 40 checks</h2><Button type="button" onClick={runTest} disabled={test?.state === 'running'}>{test?.state === 'running' ? 'Checking…' : 'Run check now'}</Button></div>
      <div className="health-strip" role="img" aria-label={`${results.filter(r => r === 'ok').length} of 40 checks passed`}>{results.map((r, i) => <span key={i} className={r} />)}</div>
      {test?.text && <p className={cx('health-result', test.state)}>{test.state === 'ok' ? <CheckCircleIcon /> : <ExclamationTriangleIcon />}{test.text}</p>}
    </section>
    <SettingsGroup title="Check">
      <SettingsField label="Enabled"><Switch label="Health checks enabled" checked={values.enabled} onChange={set('enabled')} /></SettingsField>
      <SettingsField label="Type" htmlFor="type"><Select {...bind('type')} label="Type" options={opts([['http', 'HTTP', 'Expect a 2xx response'], ['tcp', 'TCP', 'Expect the port to accept connections'], ['command', 'Command', 'Run a command inside the container']])} /></SettingsField>
      {values.type === 'http' && <SettingsField label="Path" htmlFor="path"><TextInput className="mono" {...bindInput('path')} /></SettingsField>}
      {values.type === 'command' ? <SettingsField label="Command" htmlFor="command"><TextInput className="mono" placeholder="pg_isready -U ignition" id="command" /></SettingsField>
        : <SettingsField label="Port" htmlFor="port"><TextInput type="number" {...bindInput('port')} /></SettingsField>}
    </SettingsGroup>
    <SettingsGroup title="Timing">
      <SettingsField label="Interval" htmlFor="interval"><TextInput type="number" suffix="seconds" {...bindInput('interval')} /></SettingsField>
      <SettingsField label="Timeout" htmlFor="timeout"><TextInput type="number" suffix="seconds" {...bindInput('timeout')} /></SettingsField>
      <SettingsField label="Failures before restart" htmlFor="retries"><TextInput type="number" {...bindInput('retries')} /></SettingsField>
      <SettingsField label="Start period" hint="Grace period while the app boots." htmlFor="startPeriod"><TextInput type="number" suffix="seconds" {...bindInput('startPeriod')} /></SettingsField>
    </SettingsGroup>
  </SettingsPage>;
}


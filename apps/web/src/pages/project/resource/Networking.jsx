import { useEffect, useMemo, useState } from 'react';
import {
  ArrowPathIcon, CheckCircleIcon, ExclamationTriangleIcon, GlobeAltIcon, LinkIcon, LockClosedIcon, PlayIcon, PlusIcon, ServerStackIcon, SignalIcon, TrashIcon, XCircleIcon, XMarkIcon,
} from '@heroicons/react/16/solid';
import { Button, Dialog, Notice, SegmentedControl, Select, Status, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { useSession, can } from '../../../app/AuthGate';
import { SaveBar } from '../../settings/SettingsField';
import { CopyButton, PageIntro, Table } from './shared';
import './Networking.css';

/*
 * Networks, aliases and port mappings are saved here and applied by the next deployment (the container is recreated).
 * Everything marked "live" is what Docker reports right now. Diagnostics run from a temporary probe container
 * attached to the same networks as this resource, so they see what the app sees.
 */

const BIND = [
  { value: '127.0.0.1', label: 'This machine only', description: '127.0.0.1' },
  { value: '0.0.0.0', label: 'All interfaces', description: 'Other devices can connect' },
];
const PROTOCOLS = [{ value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }];
const toForm = cfg => ({
  aliases: cfg.aliases,
  networks: cfg.networks,
  ports: cfg.ports.map((p, i) => ({ key: p.id || `p${i}`, hostPort: p.hostPort ? String(p.hostPort) : '', containerPort: String(p.containerPort), protocol: p.protocol, bindAddress: p.bindAddress })),
});
const toBody = f => ({
  aliases: f.aliases,
  networks: f.networks,
  ports: f.ports.map(p => ({ hostPort: p.hostPort ? Number(p.hostPort) : null, containerPort: Number(p.containerPort), protocol: p.protocol, bindAddress: p.bindAddress })),
});

export function Networking({ resource, onNavigate }) {
  const { user } = useSession();
  const canEdit = can(user, 'resources.update');
  const { data, error, refetch, setData } = useQuery(() => api.networking(resource.id), [resource.id], { pollMs: 10000 });
  const [saved, setSaved] = useState(null);
  const [form, setForm] = useState(null);
  const [errors, setErrors] = useState({});
  const [confirm, setConfirm] = useState(false);
  const [feedback, setFeedback] = useState(null);
  const [busy, setBusy] = useState(false);
  const dirty = saved && JSON.stringify(form) !== JSON.stringify(saved);
  useEffect(() => { if (data && !dirty) { const f = toForm(data.config); setSaved(f); setForm(f); } }, [data]); // eslint-disable-line react-hooks/exhaustive-deps

  if (error && !data) return <div className="rp-page"><Notice tone="danger" title="Couldn't load networking">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice></div>;
  if (!data || !form) return <div className="rp-page"><p className="net-muted">Loading…</p></div>;

  const set = patch => { setForm(f => ({ ...f, ...patch })); setFeedback(null); };
  const setPort = (key, patch) => {
    set({ ports: form.ports.map(p => p.key === key ? { ...p, ...patch } : p) });
    setErrors(e => Object.fromEntries(Object.entries(e).filter(([k]) => !k.startsWith(`ports.${form.ports.findIndex(p => p.key === key)}.`))));
  };
  const needsRecreate = JSON.stringify(form.ports) !== JSON.stringify(saved.ports) || JSON.stringify(form.aliases) !== JSON.stringify(saved.aliases);
  const fixedPorts = form.ports.filter(p => p.hostPort);
  const newlyPublic = form.ports.filter(p => p.bindAddress === '0.0.0.0' && !saved.ports.some(s => s.bindAddress === '0.0.0.0' && s.containerPort === p.containerPort));

  async function save(redeploy) {
    setBusy(true); setErrors({});
    try {
      const out = await api.putNetworking(resource.id, toBody(form));
      const f = toForm(out.config);
      setSaved(f); setForm(f); setConfirm(false);
      if (redeploy) {
        await api.deploy(resource.id, {});
        setFeedback({ tone: 'success', title: 'Saved. Redeploying to apply it', text: 'Follow it under Deployments. This page shows the live state once it finishes.' });
      } else {
        setFeedback(out.applyError ? { tone: 'warning', title: 'Saved', text: out.applyError }
          : out.requiresRedeploy ? { tone: 'info', title: 'Saved', text: 'Network changes are live. Ports and aliases apply on the next deployment.', redeploy: true }
            : { tone: 'success', title: 'Saved', text: data.live.networks.length ? 'Network changes are live; nothing was restarted.' : 'Applies when the resource is running.' });
      }
      refetch();
    } catch (err) {
      setConfirm(false);
      setErrors(err.fields ?? {});
      setFeedback({ tone: 'danger', title: 'Couldn\'t save networking', text: Object.values(err.fields ?? {})[0] ?? err.message });
    }
    setBusy(false);
  }
  async function redeployNow() {
    setBusy(true);
    try { await api.deploy(resource.id, {}); setFeedback({ tone: 'success', title: 'Redeploying to apply networking', text: 'Follow it under Deployments.' }); } catch (err) { setFeedback({ tone: 'danger', title: 'Couldn\'t redeploy', text: err.message }); }
    setBusy(false);
  }

  return <form className="settings-page rp-settings net-page" onSubmit={e => { e.preventDefault(); needsRecreate && data.live.networks.length > 0 ? setConfirm(true) : save(false); }}>
    <PageIntro>How {resource.name} connects: the Docker networks it joins, the ports it publishes on this machine, and who can reach it. Domains and certificates live under <button type="button" className="inline-link" onClick={() => onNavigate?.('Domains')}>Domains</button>.</PageIntro>
    {data.dockerError && <Notice tone="danger" title="Can't reach Docker">{data.dockerError}</Notice>}
    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}{feedback.redeploy && canEdit && <> <button type="button" className="inline-link" disabled={busy} onClick={redeployNow}>Redeploy now</button></>}</Notice>}
    {!dirty && data.drift.length > 0 && <Notice tone="warning" title="Saved settings aren't applied yet">
      {data.drift.join(' · ')}.{canEdit && <> <button type="button" className="inline-link" disabled={busy} onClick={redeployNow}>Redeploy to apply</button></>}
    </Notice>}

    <Networks data={data} form={form} set={set} canEdit={canEdit} resource={resource} errors={errors} />
    <Ports data={data} form={form} set={set} setPort={setPort} canEdit={canEdit} resource={resource} errors={errors} />
    <Connectivity data={data} resource={resource} canEdit={canEdit} isAdmin={can(user, 'resources.privileged')} onTest={t => setData(d => ({ ...d, tests: [t, ...d.tests].slice(0, 8) }))} />
    <Exposure data={data} form={saved} resource={resource} onNavigate={onNavigate} />

    <SaveBar dirty={dirty} onDiscard={() => { setForm(saved); setErrors({}); }} summary="Networking changes apply on the next deployment" />

    <Dialog open={confirm} onClose={() => setConfirm(false)} icon={ServerStackIcon} title="Apply networking changes?"
      description={`${resource.name}'s container has to be recreated for these changes to take effect.`}
      footer={<><Button type="button" onClick={() => setConfirm(false)}>Cancel</Button><span className="net-spacer" />
        <Button type="button" disabled={busy} onClick={() => save(false)}>Save only</Button>
        <Button type="button" variant="primary" disabled={busy} onClick={() => save(true)}>{busy ? 'Saving…' : 'Save and redeploy'}</Button></>}>
      <ul className="net-impact">
        {fixedPorts.length > 0
          ? <li><ExclamationTriangleIcon className="warn" />Fixed host ports ({fixedPorts.map(p => p.hostPort).join(', ')}) can only be held by one container, so the old one stops before the new one starts: a few seconds of downtime. If the new one fails, the old one is started again.</li>
          : <li><CheckCircleIcon className="ok" />No downtime expected: the new container starts and passes its check before the old one is removed.</li>}
        {newlyPublic.length > 0 && <li><ExclamationTriangleIcon className="bad" />Port {newlyPublic.map(p => p.containerPort).join(', ')} will listen on all interfaces, so other devices on your network can connect{resource.type === 'database' ? '. This is a database: make sure it requires a strong password' : ''}.</li>}
        <li><CheckCircleIcon className="ok" />Joining or leaving networks applies right away either way. Save only keeps ports and aliases as they are until the next deployment.</li>
      </ul>
    </Dialog>
  </form>;
}

/* --------------------------------- Networks -------------------------------- */

const NEW = '__new';
const NETWORK_RE = /^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$/;

function Networks({ data, form, set, canEdit, resource, errors }) {
  const [pick, setPick] = useState('');
  const [newName, setNewName] = useState('');
  const [joinError, setJoinError] = useState('');
  const [alias, setAlias] = useState('');
  const byName = Object.fromEntries(data.networks.map(n => [n.name, n]));
  const privateNet = data.networks.find(n => n.kind === 'private');
  const legacy = data.networks.find(n => n.kind === 'legacy');
  const shared = form.networks.map(name => byName[name] ?? { kind: 'shared', name, attached: false, aliases: [], members: [], unsaved: true });
  const left = data.networks.filter(n => n.kind === 'shared' && !form.networks.includes(n.name));
  const available = data.availableShared.filter(n => !form.networks.includes(n.name));
  const options = [
    ...available.map(n => ({ value: n.name, label: n.name, description: n.members.length ? `${n.members.length} ${n.members.length === 1 ? 'resource' : 'resources'}` : 'empty' })),
    { value: NEW, label: 'New shared network…' },
  ];
  const choice = pick || options[0].value;

  function join() {
    const name = choice === NEW ? newName.trim().toLowerCase() : choice;
    if (!NETWORK_RE.test(name)) return setJoinError('Use lowercase letters, numbers and dashes (up to 32).');
    if (form.networks.includes(name)) return setJoinError(`Already in ${name}.`);
    set({ networks: [...form.networks, name] }); setPick(''); setNewName(''); setJoinError('');
  }
  function addAlias(e) {
    e?.preventDefault();
    const a = alias.trim().toLowerCase();
    if (!a || a === resource.name || form.aliases.includes(a)) { setAlias(''); return; }
    set({ aliases: [...form.aliases, a] }); setAlias('');
  }
  const status = n => n.unsaved ? { tone: 'neutral', label: 'Joins when saved' }
    : n.attached ? { tone: 'success', label: 'Connected' }
      : resource.status === 'running' ? { tone: 'progress', label: 'Connecting…' } : { tone: 'neutral', label: 'When running' };
  const live = n => n.attached ? <><span className="mono">{n.ip || '—'}</span>{n.aliases.length > 0 && <span className="net-muted"> · answers to {n.aliases.filter(a => !/^[0-9a-f]{12}$/.test(a) && !a.startsWith('ign-')).join(', ')}</span>}</> : <span className="net-muted">—</span>;

  return <section className="settings-group">
    <h2>Networks</h2>
    <p className="net-desc">Resources in {privateNet?.name ?? 'the same environment'} reach each other by name over their private network. Join a shared network to connect {resource.name} with resources in other projects.</p>
    <div className="settings-card net-list">
      {privateNet && <div className="net-item">
        <LockClosedIcon className="net-icon" />
        <span className="net-item-main"><strong>Private network <span className="rp-chip net-chip">Automatic</span></strong><span>{privateNet.name} · {live(privateNet)}</span></span>
        <Status tone={status(privateNet).tone}>{status(privateNet).label}</Status>
      </div>}
      {shared.map(n => <div key={n.name} className="net-item">
        <LinkIcon className="net-icon" />
        <span className="net-item-main"><strong>{n.name} <span className="rp-chip net-chip">Shared</span></strong>
          <span>{n.members?.length ? `With ${n.members.filter(m => !m.startsWith(`${resource.name} (`)).join(', ') || 'no other resources yet'}` : 'No other resources yet'}{n.attached && <> · {live(n)}</>}</span></span>
        <Status tone={status(n).tone}>{status(n).label}</Status>
        {canEdit && <button type="button" className="icon-action danger" aria-label={`Leave ${n.name}`} title="Leave" onClick={() => set({ networks: form.networks.filter(x => x !== n.name) })}><XMarkIcon /></button>}
      </div>)}
      {left.map(n => <div key={n.name} className="net-item net-leaving">
        <LinkIcon className="net-icon" />
        <span className="net-item-main"><strong>{n.name}</strong><span>Leaves when you save</span></span>
      </div>)}
      {legacy && <div className="net-item net-leaving">
        <ArrowPathIcon className="net-icon" />
        <span className="net-item-main"><strong>{legacy.name}</strong><span>Ignition is moving {resource.name} off the old all-in-one network. This finishes on its own within a minute.</span></span>
      </div>}
    </div>
    {canEdit && <div className="rp-inline-form net-row">
      <Select label="Shared network" value={choice} options={options} onChange={v => { setPick(v); setJoinError(''); }} className="net-select" />
      {choice === NEW && <TextInput className="mono net-new" placeholder="network-name" value={newName} aria-label="New network name" autoFocus
        onChange={e => { setNewName(e.target.value.toLowerCase()); setJoinError(''); }} onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); join(); } }} />}
      <Button type="button" icon={PlusIcon} onClick={join}>Join</Button>
    </div>}
    {(joinError || errors.networks) && <p className="rp-error">{joinError || errors.networks}</p>}

    <div className="net-aliases">
      <strong>Internal aliases</strong>
      <span className="net-muted">Extra names other resources can use for {resource.name}, on every network it joins.</span>
      <div className="net-chips">
        <span className="rp-chip mono">{resource.name}</span>
        {form.aliases.map(a => <span key={a} className="rp-chip mono">{a}{canEdit && <button type="button" aria-label={`Remove alias ${a}`} onClick={() => set({ aliases: form.aliases.filter(x => x !== a) })}><XMarkIcon /></button>}</span>)}
        {canEdit && <TextInput className="mono net-alias-input" placeholder="add alias" value={alias} aria-label="New alias" onChange={e => setAlias(e.target.value)} onKeyDown={e => { if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); addAlias(); } }} onBlur={addAlias} />}
      </div>
      {errors.aliases && <p className="rp-error">{errors.aliases}</p>}
    </div>
  </section>;
}

/* ------------------------------- Port mappings ------------------------------ */

function Ports({ data, form, set, setPort, canEdit, resource, errors }) {
  const [checks, setChecks] = useState({});
  async function check(p) {
    if (!p.hostPort) { setChecks(c => ({ ...c, [p.key]: null })); return; }
    try {
      const r = await api.checkHostPort(resource.id, { hostPort: Number(p.hostPort), protocol: p.protocol });
      setChecks(c => ({ ...c, [p.key]: r.available ? null : r.message }));
    } catch (err) { setChecks(c => ({ ...c, [p.key]: err.fields?.hostPort ?? err.message })); }
  }
  const liveFor = p => data.live.ports.filter(l => l.containerPort === Number(p.containerPort) && l.protocol === p.protocol && l.hostIp === p.bindAddress && (!p.hostPort || l.hostPort === Number(p.hostPort)));
  const add = () => set({ ports: [...form.ports, { key: `n${Date.now()}`, hostPort: '', containerPort: String(resource.port ?? ''), protocol: 'tcp', bindAddress: '127.0.0.1' }] });

  return <section className="settings-group">
    <h2>Port mappings</h2>
    <p className="net-desc">Publish container ports directly on this machine. Leave the host port empty to let Docker pick a free one. Web traffic for domains goes through the proxy instead and needs no mapping.</p>
    {form.ports.length > 0 ? <Table className="net-ports" columns={[{ label: 'Host port' }, { label: 'Container port' }, { label: 'Protocol' }, { label: 'Listens on' }, { label: 'Live' }, { label: '', className: 'actions' }]}>
      {form.ports.map((p, i) => {
        const err = errors[`ports.${i}.hostPort`] ?? checks[p.key] ?? errors[`ports.${i}.containerPort`];
        const live = liveFor(p);
        return <tr key={p.key} className={cx(err && 'has-error')}>
          <td><TextInput className="mono net-port" inputMode="numeric" placeholder="auto" disabled={!canEdit} value={p.hostPort} aria-label="Host port" aria-invalid={!!err}
            onChange={e => setPort(p.key, { hostPort: e.target.value.replace(/\D/g, '').slice(0, 5) })} onBlur={() => check(p)} />{err && <span className="net-error">{err}</span>}</td>
          <td><TextInput className="mono net-port" inputMode="numeric" disabled={!canEdit} value={p.containerPort} aria-label="Container port" onChange={e => setPort(p.key, { containerPort: e.target.value.replace(/\D/g, '').slice(0, 5) })} /></td>
          <td>{canEdit ? <Select label="Protocol" value={p.protocol} options={PROTOCOLS} onChange={protocol => setPort(p.key, { protocol })} className="net-proto" /> : p.protocol.toUpperCase()}</td>
          <td>{canEdit ? <Select label="Listens on" value={p.bindAddress} options={BIND} onChange={bindAddress => setPort(p.key, { bindAddress })} className="net-bind" /> : BIND.find(b => b.value === p.bindAddress)?.label}
            {p.bindAddress === '0.0.0.0' && <span className={cx('net-flag', resource.type === 'database' && 'bad')}><ExclamationTriangleIcon />{resource.type === 'database' ? 'Database open to your network' : 'Reachable from other devices'}</span>}</td>
          <td className="mono">{live.length ? live.map(l => `${l.hostIp === '0.0.0.0' ? '*' : l.hostIp}:${l.hostPort}`).join(', ') : <span className="net-muted">{resource.status === 'running' ? 'after redeploy' : '—'}</span>}</td>
          <td className="actions"><div>{canEdit && <button type="button" className="icon-action danger" aria-label={`Remove mapping for port ${p.containerPort}`} onClick={() => set({ ports: form.ports.filter(x => x.key !== p.key) })}><TrashIcon /></button>}</div></td>
        </tr>;
      })}
    </Table> : <p className="net-empty"><LockClosedIcon />No ports are published on this machine. {resource.type === 'database' ? 'That\'s the safe default for databases: other resources still connect over the network.' : 'Other resources still connect over the network.'}</p>}
    {canEdit && <div className="rp-inline-form net-row"><Button type="button" icon={PlusIcon} onClick={add}>Add mapping</Button></div>}
    {errors.ports && <p className="rp-error">{errors.ports}</p>}
  </section>;
}

/* ------------------------------- Connectivity ------------------------------- */

function Connectivity({ data, resource, canEdit, isAdmin, onTest }) {
  const port = resource.port;
  const [kind, setKind] = useState('tcp');
  const [target, setTarget] = useState('self');
  const [custom, setCustom] = useState('');
  const [testPort, setTestPort] = useState('');
  const [running, setRunning] = useState(false);
  const [failure, setFailure] = useState('');
  const [open, setOpen] = useState(null);
  const peer = data.peers.find(p => p.id === target);
  const defaultPort = target === 'self' ? port : peer?.port;
  const targets = useMemo(() => [
    { value: 'self', label: `${resource.name} (this resource)` },
    ...data.peers.map(p => ({ value: p.id, label: p.name, description: `${p.projectName}${p.environment !== 'Production' ? ` · ${p.environment}` : ''}` })),
    ...(isAdmin ? [{ value: 'custom', label: 'Custom host…', description: 'Admins only' }] : []),
  ], [data.peers, resource.name, isAdmin]);

  async function run(override) {
    const body = { kind, target, ...override };
    if (body.target === 'custom') body.hostname = custom;
    if (body.kind === 'tcp') body.port = Number(override?.port ?? (testPort || defaultPort || 0));
    setRunning(true); setFailure('');
    try {
      const { test } = await api.runDiagnostic(resource.id, body);
      onTest(test); setOpen(test.id);
    } catch (err) { setFailure(err.fields ? Object.values(err.fields)[0] : err.message); }
    setRunning(false);
  }

  return <section className="settings-group">
    <h2>Connectivity</h2>
    <p className="net-desc">How other resources reach {resource.name}, and tests run from a temporary probe container on {resource.name}'s networks, so they see what the app sees.</p>
    <div className="settings-card">
      <div className="net-kv">
        <span>Internal address</span>
        <div className="net-addresses">{data.hostnames.map(h => {
          const v = port ? `${h}:${port}` : h;
          return <span key={h} className="net-address"><code className="mono">{v}</code><CopyButton value={v} label={`Copy ${v}`} /></span>;
        })}</div>
      </div>
      {resource.type === 'database' && port && <div className="net-kv"><span>Connect from another resource</span><span className="net-muted">Use host <code className="mono">{resource.name}</code> and port <code className="mono">{port}</code> with the credentials under Environment Variables.</span></div>}
    </div>

    <h3 className="net-sub">Resources on the same network <span className="net-count">{data.peers.length}</span></h3>
    {data.peers.length ? <Table columns={[{ label: 'Resource' }, { label: 'Address' }, { label: 'Network' }, { label: 'Status' }, { label: '', className: 'actions' }]}>
      {data.peers.map(p => <tr key={p.id}>
        <td><strong className="net-name">{p.name}</strong><span className="net-muted"> {p.projectName}</span></td>
        <td className="mono">{p.hostnames[0]}{p.port ? `:${p.port}` : ''}</td>
        <td className="muted">{[p.sameEnvironment && 'Private network', ...p.shared].filter(Boolean).join(', ')}</td>
        <td><Status tone={p.status === 'running' ? 'success' : p.status === 'failed' ? 'danger' : 'neutral'}>{p.status[0].toUpperCase() + p.status.slice(1)}</Status></td>
        <td className="actions"><div>{canEdit && p.port && <Button type="button" icon={SignalIcon} disabled={running} onClick={() => { setKind('tcp'); setTarget(p.id); run({ kind: 'tcp', target: p.id, port: p.port }); }}>Test</Button>}</div></td>
      </tr>)}
    </Table> : <p className="net-empty">No other resources share a network with {resource.name}.</p>}

    {canEdit && <>
      <h3 className="net-sub">Diagnostics</h3>
      <div className="settings-card net-diag">
        <SegmentedControl label="Test" value={kind} onChange={setKind} options={[{ value: 'tcp', label: 'TCP connection' }, { value: 'dns', label: 'DNS lookup' }]} />
        <Select label="Target" value={target} options={targets} onChange={setTarget} searchable={8} className="net-target" />
        {target === 'custom' && <TextInput className="mono net-custom" placeholder="host or IP" value={custom} aria-label="Custom host" onChange={e => setCustom(e.target.value)} />}
        {kind === 'tcp' && <TextInput className="mono net-port" inputMode="numeric" placeholder={defaultPort ? String(defaultPort) : 'port'} value={testPort} aria-label="Port to test" onChange={e => setTestPort(e.target.value.replace(/\D/g, '').slice(0, 5))} />}
        <Button type="button" variant="primary" icon={running ? ArrowPathIcon : PlayIcon} disabled={running || (target === 'custom' && !custom.trim())} onClick={() => run()}>{running ? 'Testing…' : 'Run test'}</Button>
      </div>
      {failure && <p className="rp-error">{failure}</p>}
    </>}
    {data.tests.length > 0 && <div className="settings-card net-tests">
      {data.tests.map(t => <div key={t.id} className="net-test" data-test-id={t.id}>
        <button type="button" className="net-test-row" aria-expanded={open === t.id} onClick={() => setOpen(open === t.id ? null : t.id)}>
          {t.ok ? <CheckCircleIcon className="ok" /> : <XCircleIcon className="bad" />}
          <span className="net-test-main"><strong>{t.kind === 'dns' ? 'DNS' : 'TCP'} · <span className="mono">{t.target}</span></strong><span>{t.summary}</span></span>
          <span className="net-muted net-when">{timeAgo(t.ranAt)}{t.ranBy && ` · ${t.ranBy}`}</span>
        </button>
        {open === t.id && t.output && <pre className="net-output">{t.output}</pre>}
      </div>)}
      <p className="net-footnote">Tests run from a temporary probe container (busybox) on {resource.name}'s networks{data.config.networks.length ? ` (private, ${data.config.networks.join(', ')})` : ''}, not from the Ignition server itself.</p>
    </div>}
  </section>;
}

/* --------------------------------- Exposure -------------------------------- */

function Exposure({ data, form, resource, onNavigate }) {
  const live = data.live.ports.filter(p => p.hostIp !== '::');
  const open = live.filter(p => p.hostIp === '0.0.0.0');
  const local = live.filter(p => p.hostIp === '127.0.0.1');
  const domains = data.domains ?? [];
  const configuredOpen = form.ports.filter(p => p.bindAddress === '0.0.0.0');
  const isPrivate = !live.length && !domains.length;

  return <section className="settings-group">
    <h2>External access</h2>
    {resource.type === 'database' && (open.length > 0 || configuredOpen.length > 0) && <Notice tone="danger" title="This database listens on all interfaces">Any device that can reach this machine can try to log in on port {(open[0]?.hostPort ?? configuredOpen[0]?.hostPort) || 'the published port'}. Change it to "This machine only" unless you need it, and keep a strong password.</Notice>}
    <div className="settings-card">
      {isPrivate && <div className="net-expo"><LockClosedIcon className="ok" /><span><strong>Private</strong><span>Only resources on its networks can reach {resource.name}.</span></span></div>}
      {local.map(p => <div key={`l${p.hostPort}${p.protocol}`} className="net-expo"><LockClosedIcon className="ok" />
        <span><strong className="mono">localhost:{p.hostPort} → {p.containerPort}/{p.protocol}</strong><span>This machine only.</span></span></div>)}
      {open.map(p => <div key={`o${p.hostPort}${p.protocol}`} className="net-expo"><ExclamationTriangleIcon className={resource.type === 'database' ? 'bad' : 'warn'} />
        <span><strong className="mono">*:{p.hostPort} → {p.containerPort}/{p.protocol}</strong><span>All interfaces: other devices on your network can connect. From the internet only if your router forwards {p.protocol.toUpperCase()} {p.hostPort} to this machine.</span></span></div>)}
      {domains.length > 0 && <div className="net-expo"><GlobeAltIcon className="ok" />
        <span><strong>Through the domain proxy · ports {data.proxy.httpPort}/{data.proxy.httpsPort}</strong>
          <span>{domains.map(d => `${d.hostname} (${d.status === 'active' ? 'routing' : 'not routing yet'})`).join(', ')}. <button type="button" className="inline-link" onClick={() => onNavigate?.('Domains')}>Manage under Domains</button></span></span></div>}
      {open.length > 0 && <div className="net-expo"><ServerStackIcon />
        <span><strong>Firewall</strong><span>Ignition doesn't change firewall rules. If other devices can't connect, allow inbound {open.map(p => `${p.protocol.toUpperCase()} ${p.hostPort}`).join(', ')} in your operating system's firewall (on Windows: Windows Defender Firewall).</span></span></div>}
    </div>
  </section>;
}

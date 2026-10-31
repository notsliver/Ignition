import { useEffect, useState } from 'react';
import { BeakerIcon, EllipsisHorizontalIcon, PencilSquareIcon, PlayIcon, PlusIcon, ShareIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, Menu, Notice, SegmentedControl, Select, Status, Switch, TextInput, cx } from '../../ui';
import { api } from '../../api/client';
import { timeAgo, useQuery } from '../../api/hooks';
import { can, useSession } from '../../app/AuthGate';
import { SettingsField, SettingsGroup } from '../settings/SettingsField';
import '../Monitor/Monitor.css';
import '../project/resource/resource.css';
import '../settings/settings.css';
import './network.css';

/*
 * Infrastructure › Networking: this machine's addresses, the host ports everything uses (with an
 * allocation range and reservations), internal networks with their subnets, IPv6 and fixed
 * addresses, host firewall rules for published ports, and network tests from any server.
 * Everything is read live; sections say why when something can't be.
 */

export function Networking({ onOpenResource }) {
  const { user } = useSession();
  return <div className="monitor nw-page">
    <div className="rp-intro">
      <p>Addresses, ports, internal networks and the host firewall. Domains and the proxy are under Domains.</p>
    </div>
    <Addresses canManage={can(user, 'network.manage')} />
    <Ports canManage={can(user, 'network.manage')} onOpenResource={onOpenResource} />
    <Networks canManage={can(user, 'network.manage')} />
    <Firewall canManage={can(user, 'firewall.manage')} />
    {can(user, 'network.diagnose') && <Diagnostics />}
  </div>;
}

/* --------------------------------- Addresses --------------------------------- */

const scopeTone = { public: 'accent', private: '', loopback: '', 'link-local': '', docker: '' };

function Addresses({ canManage }) {
  const { data, error, refetch } = useQuery(() => api.networkAddresses(), []);
  const [editing, setEditing] = useState(false);
  const [all, setAll] = useState(false);
  const pub = data?.public;
  const every = data?.interfaces ?? [];
  // Docker's bridges and veths, and link-local addresses, are noise unless you ask for them.
  const ifaces = all ? every : every.filter(a => a.scope !== 'docker' && a.scope !== 'link-local');
  const hidden = every.length - ifaces.length;
  return <SettingsGroup title="Addresses" description="Where this server can be reached. DNS records created by Ignition point at the public addresses.">
    {error && <div className="field-notice"><Notice tone="danger" title={error.message} /></div>}
    <SettingsField label="Public IPv4" hint={pub?.source === 'settings' ? 'Set by hand.' : 'Detected from outgoing traffic. Set it by hand behind NAT or with several addresses.'}>
      <span className="readonly-value mono">{pub ? pub.ipv4 || 'None found' : '…'}</span>
      {canManage && <Button icon={PencilSquareIcon} onClick={() => setEditing(true)}>Edit</Button>}
    </SettingsField>
    <SettingsField label="Public IPv6" hint={pub && !pub.ipv6 ? 'No IPv6 route out of this server, or IPv6 is off.' : undefined}>
      <span className="readonly-value mono">{pub ? pub.ipv6 || 'None' : '…'}</span>
    </SettingsField>
    <div className="nw-inner-table">
      {data?.interfacesReason ? <p className="nw-empty">{data.interfacesReason}</p> : <table className="monitor-table nw-compact">
        <thead><tr><th>Interface</th><th>Address</th><th>Family</th><th>Scope</th></tr></thead>
        <tbody>{ifaces.map(a => <tr key={a.interface + a.address}>
          <td className="mono">{a.interface}</td><td className="mono">{a.address}/{a.prefix}</td><td className="muted">{a.family === 'inet6' ? 'IPv6' : 'IPv4'}</td>
          <td><span className={cx('rp-chip', scopeTone[a.scope])}>{a.scope}</span></td>
        </tr>)}</tbody>
      </table>}
      {(hidden > 0 || all) && <button type="button" className="inline-link nw-more" onClick={() => setAll(a => !a)}>{all ? 'Hide Docker and link-local addresses' : `Show ${hidden} Docker and link-local address${hidden === 1 ? '' : 'es'}`}</button>}
    </div>
    {editing && <AddressDialog settings={data.settings} detected={pub} onClose={() => setEditing(false)} onSaved={() => { setEditing(false); refetch(); }} />}
  </SettingsGroup>;
}

function AddressDialog({ settings, onClose, onSaved }) {
  const [v, setV] = useState({ publicIpv4: settings.publicIpv4, publicIpv6: settings.publicIpv6 });
  const [errors, setErrors] = useState({});
  return <Dialog open onClose={onClose} title="Public addresses" description="Leave both empty to detect them automatically."
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" onClick={async () => { try { await api.saveNetworkSettings({ ...settings, ...v }); onSaved(); } catch (e) { setErrors(e.fields ?? {}); } }}>Save</Button></>}>
    <div className="nw-form">
      <FormField label="IPv4" htmlFor="pub4" error={errors.publicIpv4}><TextInput id="pub4" className="mono" value={v.publicIpv4} onChange={e => setV(x => ({ ...x, publicIpv4: e.target.value }))} placeholder="203.0.113.7" /></FormField>
      <FormField label="IPv6" htmlFor="pub6" error={errors.publicIpv6}><TextInput id="pub6" className="mono" value={v.publicIpv6} onChange={e => setV(x => ({ ...x, publicIpv6: e.target.value }))} placeholder="2001:db8::7" /></FormField>
    </div>
  </Dialog>;
}

/* ----------------------------------- Ports ----------------------------------- */

function Ports({ canManage, onOpenResource }) {
  const { data, error, refetch } = useQuery(() => api.networkPorts(), [], { pollMs: 15000 });
  const [range, setRange] = useState(null);
  const [reserving, setReserving] = useState(false);
  const [feedback, setFeedback] = useState(null);
  const s = data?.settings;
  const resources = new Set((data?.assignments ?? []).map(a => a.resourceId));
  // One row per port: Docker lists IPv4 and IPv6 bindings of the same port separately.
  const others = Object.values(Object.fromEntries((data?.live ?? [])
    .filter(l => !resources.has(l.resourceId) && !(data.system ?? []).some(p => p.port === l.hostPort))
    .map(l => [`${l.hostPort}/${l.protocol}/${l.container}`, l])));
  async function saveRange() {
    try { await api.saveNetworkSettings({ ...s, portRangeStart: Number(range.start) || 0, portRangeEnd: Number(range.end) || 0 }); setRange(null); setFeedback({ tone: 'success', title: 'Port range saved' }); refetch(); }
    catch (e) { setFeedback({ tone: 'danger', title: e.fields?.portRangeStart ?? e.message }); }
  }
  return <section className="settings-group">
    <div className="group-head nw-group-head">
      <div><h2>Ports</h2><p>Host ports in use on this machine. Mappings without a fixed port get one from the allocation range when it's set; otherwise Docker picks a random one at each deploy.</p></div>
      {canManage && <Button icon={PlusIcon} onClick={() => setReserving(true)}>Reserve a port</Button>}
    </div>
    {error && <Notice tone="danger" title={error.message} />}
    {feedback && <Notice tone={feedback.tone} title={feedback.title} />}
    {data && <div className="settings-card">
      <SettingsField label="Allocation range" hint={s.portRangeStart ? `Next free: ${data.nextFree ?? '—'}${data.nextFreeReason ? ` (${data.nextFreeReason})` : ''}` : 'Off: Docker picks ports.'}>
        {range ? <>
          <TextInput className="nw-port-input" type="number" value={range.start} onChange={e => setRange(r => ({ ...r, start: e.target.value }))} placeholder="20000" />
          <span className="muted">to</span>
          <TextInput className="nw-port-input" type="number" value={range.end} onChange={e => setRange(r => ({ ...r, end: e.target.value }))} placeholder="29999" />
          <Button onClick={() => setRange(null)}>Cancel</Button><Button variant="primary" onClick={saveRange}>Save</Button>
        </> : <>
          <span className="readonly-value mono">{s.portRangeStart ? `${s.portRangeStart}–${s.portRangeEnd}` : 'Not set'}</span>
          {canManage && <Button onClick={() => setRange({ start: s.portRangeStart || '', end: s.portRangeEnd || '' })}>{s.portRangeStart ? 'Change' : 'Set range'}</Button>}
        </>}
      </SettingsField>
    </div>}
    {data && <div className="monitor-table-wrap">
      <table className="monitor-table nw-compact">
        <thead><tr><th className="num">Host port</th><th>Protocol</th><th>Used by</th><th>Container port</th><th>Exposure</th><th className="actions"><span className="visually-hidden">Actions</span></th></tr></thead>
        <tbody>
          {data.system.map(p => <tr key={`sys${p.port}`}><td className="num mono">{p.port}</td><td className="muted">{p.protocol}</td><td>{p.use}</td><td className="muted">—</td><td><span className="rp-chip accent">Ignition</span></td><td /></tr>)}
          {data.assignments.map((a, i) => { const port = a.hostPort ?? a.livePort; return <tr key={`a${i}`} onClick={() => onOpenResource?.({ id: a.resourceId, projectId: a.projectId })}>
            <td className="num mono">{port ?? '—'}{a.hostPort == null && <small className="nw-small" title="Docker picked this port; it can change on the next deploy"> auto</small>}</td>
            <td className="muted">{a.protocol}</td>
            <td><span className="resource-name">{a.resourceName}<span>{a.projectName}</span></span></td>
            <td className="mono">{a.containerPort}</td>
            <td>{a.bindAddress === '0.0.0.0' ? <span className="rp-chip warning">All interfaces</span> : <span className="rp-chip">This machine only</span>}</td>
            <td />
          </tr>; })}
          {others.map((l, i) => <tr key={`o${i}`}><td className="num mono">{l.hostPort}</td><td className="muted">{l.protocol}</td><td className="muted">{l.container} (not managed by Ignition)</td><td className="muted">—</td>
            <td>{!l.hostIp || l.hostIp === '0.0.0.0' || l.hostIp === '::' ? <span className="rp-chip warning">All interfaces</span> : <span className="rp-chip">{l.hostIp}</span>}</td><td /></tr>)}
          {data.reservations.map(rv => <tr key={rv.id}><td className="num mono">{rv.port}</td><td className="muted">{rv.protocol}</td><td>Reserved{rv.note ? `: ${rv.note}` : ''} <small className="muted">by {rv.createdBy || 'someone'} {timeAgo(rv.createdAt)}</small></td><td className="muted">—</td><td><span className="rp-chip">Reserved</span></td>
            <td className="actions" onClick={e => e.stopPropagation()}>{canManage && <Menu trigger={EllipsisHorizontalIcon} label={`Actions for port ${rv.port}`} items={[{ label: 'Release', icon: TrashIcon, danger: true, onSelect: async () => { await api.releasePort(rv.id); refetch(); } }]} />}</td></tr>)}
        </tbody>
      </table>
      {data.liveReason && <p className="nw-empty">{data.liveReason}</p>}
    </div>}
    {reserving && <ReserveDialog suggestion={data?.nextFree} onClose={() => setReserving(false)} onSaved={() => { setReserving(false); refetch(); }} />}
  </section>;
}

function ReserveDialog({ suggestion, onClose, onSaved }) {
  const [v, setV] = useState({ port: suggestion ?? '', protocol: 'tcp', note: '' });
  const [errors, setErrors] = useState({});
  return <Dialog open onClose={onClose} title="Reserve a host port" description="A reserved port is never mapped or allocated, e.g. for a service outside Ignition."
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" onClick={async () => { try { await api.reservePort({ ...v, port: Number(v.port) }); onSaved(); } catch (e) { setErrors(e.fields ?? { port: e.message }); } }}>Reserve</Button></>}>
    <div className="nw-form">
      <div className="nw-row">
        <FormField label="Port" htmlFor="rv-port" error={errors.port}><TextInput id="rv-port" type="number" value={v.port} onChange={e => setV(x => ({ ...x, port: e.target.value }))} autoFocus /></FormField>
        <FormField label="Protocol" htmlFor="rv-proto"><Select id="rv-proto" label="Protocol" value={v.protocol} onChange={p => setV(x => ({ ...x, protocol: p }))} options={[{ value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }]} /></FormField>
      </div>
      <FormField label="Note" htmlFor="rv-note" error={errors.note}><TextInput id="rv-note" value={v.note} onChange={e => setV(x => ({ ...x, note: e.target.value }))} placeholder="Grafana on the host" maxLength={200} /></FormField>
    </div>
  </Dialog>;
}

/* ------------------------------ Internal networks ------------------------------ */

function Networks({ canManage }) {
  const { data, error, refetch } = useQuery(() => api.networks(), [], { pollMs: 20000 });
  const [creating, setCreating] = useState(false);
  const [feedback, setFeedback] = useState(null);
  async function remove(n) {
    try { await api.deleteNetwork(n.name); setFeedback({ tone: 'success', title: `${n.name} deleted` }); refetch(); } catch (e) { setFeedback({ tone: 'danger', title: 'Couldn\'t delete', text: e.message }); }
  }
  return <section className="settings-group">
    <div className="group-head nw-group-head">
      <div><h2>Internal networks</h2><p>Each environment has a private network. Shared networks connect resources across projects on purpose; with a subnet set, resources can have fixed IPv4 and IPv6 addresses on them.</p></div>
      {canManage && <Button icon={PlusIcon} onClick={() => setCreating(true)}>New shared network</Button>}
    </div>
    {error && <Notice tone="danger" title={error.message} />}
    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}</Notice>}
    {data && <div className="nw-net-list">
      {data.shared.map(n => <article key={n.id} className="nw-net">
        <header>
          <ShareIcon />
          <strong>{n.name}</strong>
          {n.internal && <span className="rp-chip warning" title="No route to the internet">Internal only</span>}
          {(n.ipv6 || n.live.ipv6) && <span className="rp-chip accent">IPv6</span>}
          {!n.live.exists && <span className="rp-chip" title="Created in Docker when the first member deploys">Not created yet</span>}
          <span className="nw-net-meta">{(n.live.subnets.length ? n.live.subnets : [n.subnet, n.subnet6].filter(Boolean)).join(' · ') || 'Subnet chosen by Docker'}{n.live.gateways.length ? ` · gateway ${n.live.gateways.join(', ')}` : ''}</span>
          {canManage && <Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${n.name}`} items={[{ label: n.members.length ? 'Delete (remove its members first)' : 'Delete', icon: TrashIcon, danger: true, onSelect: () => remove(n) }]} />}
        </header>
        {n.description && <p className="nw-net-desc">{n.description}</p>}
        <Endpoints live={n.live} members={n.members} />
      </article>)}
      {!data.shared.length && <p className="nw-empty">No shared networks. Create one here, or join one from a resource's Networking page.</p>}
      {data.environments.map(e => <article key={e.environmentId} className="nw-net env">
        <header><ShareIcon /><strong>{e.name}</strong><span className="rp-chip">Environment</span>
          <span className="nw-net-meta">{e.live.subnets.join(' · ') || (e.live.exists ? '' : 'Created at the next deploy')}</span></header>
        <Endpoints live={e.live} members={[]} />
      </article>)}
    </div>}
    {creating && <CreateNetworkDialog used={data?.usedSubnets ?? []} onClose={() => setCreating(false)} onCreated={name => { setCreating(false); setFeedback({ tone: 'success', title: `${name} created` }); refetch(); }} />}
  </section>;
}

function Endpoints({ live, members }) {
  const fixed = Object.fromEntries(members.map(m => [m.resourceName, m]));
  if (!live.endpoints.length && !members.length) return <p className="nw-net-empty">No containers on it.</p>;
  return <table className="nw-endpoints">
    <tbody>
      {live.endpoints.map(ep => <tr key={ep.container}><td className="mono">{ep.container}</td><td className="mono">{ep.ipv4 || '—'}</td><td className="mono">{ep.ipv6 || ''}</td>
        <td className="muted">{Object.values(fixed).some(m => m.ipv4 === ep.ipv4 && ep.ipv4) ? 'fixed' : ''}</td></tr>)}
      {members.filter(m => !live.endpoints.some(ep => ep.container.includes(`-${m.resourceName}-`) || ep.container.startsWith(`ign-${m.resourceName}-`))).map(m =>
        <tr key={m.resourceId} className="muted"><td>{m.resourceName} <small>({m.projectName}, not running)</small></td><td className="mono">{m.ipv4 || 'auto'}</td><td className="mono">{m.ipv6}</td><td>{m.ipv4 || m.ipv6 ? 'fixed' : ''}</td></tr>)}
    </tbody>
  </table>;
}

function CreateNetworkDialog({ used, onClose, onCreated }) {
  const [v, setV] = useState({ name: '', description: '', subnet: '', gateway: '', ipv6: false, subnet6: '', internal: false });
  const [errors, setErrors] = useState({});
  const [pending, setPending] = useState(false);
  const set = k => e => { const val = e?.target ? e.target.value : e; setV(x => ({ ...x, [k]: val })); setErrors(x => ({ ...x, [k]: undefined })); };
  return <Dialog open onClose={onClose} icon={ShareIcon} title="New shared network" className="nw-dialog"
    description="Resources join it from their Networking page and reach each other by name. Subnets can't be changed later."
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" type="submit" form="net-form" disabled={pending}>{pending ? 'Creating…' : 'Create'}</Button></>}>
    <form id="net-form" className="nw-form" onSubmit={async e => { e.preventDefault(); setPending(true); try { await api.createNetwork(v); onCreated(v.name); } catch (err) { setErrors(Object.keys(err.fields ?? {}).length ? err.fields : { name: err.message }); setPending(false); } }}>
      <div className="nw-row">
        <FormField label="Name" htmlFor="net-name" error={errors.name}><TextInput id="net-name" className="mono" value={v.name} onChange={set('name')} placeholder="backend" autoFocus /></FormField>
        <FormField label="Description" htmlFor="net-desc" error={errors.description}><TextInput id="net-desc" value={v.description} onChange={set('description')} placeholder="APIs and their databases" /></FormField>
      </div>
      <div className="nw-row">
        <FormField label="IPv4 subnet" htmlFor="net-sub" error={errors.subnet} hint="Optional; needed for fixed addresses"><TextInput id="net-sub" className="mono" value={v.subnet} onChange={set('subnet')} placeholder="10.20.0.0/24" /></FormField>
        <FormField label="Gateway" htmlFor="net-gw" error={errors.gateway} hint="Optional; first address by default"><TextInput id="net-gw" className="mono" value={v.gateway} onChange={set('gateway')} placeholder="10.20.0.1" /></FormField>
      </div>
      <label className="nw-check"><Switch label="IPv6" checked={v.ipv6} onChange={set('ipv6')} /><span>Dual-stack: containers get an IPv6 address too. Docker must have IPv6 enabled.</span></label>
      {v.ipv6 && <FormField label="IPv6 range" htmlFor="net-sub6" error={errors.subnet6} hint="Optional; a ULA range like fd00:20::/64"><TextInput id="net-sub6" className="mono" value={v.subnet6} onChange={set('subnet6')} placeholder="fd00:20::/64" /></FormField>}
      <label className="nw-check"><Switch label="Internal only" checked={v.internal} onChange={set('internal')} /><span>No route to the internet: members only reach each other. Good for databases.</span></label>
      {used.length > 0 && <details className="nw-used"><summary>Subnets already in use ({used.length})</summary><ul>{used.map(u => <li key={u.network + u.subnet}><code>{u.subnet}</code> {u.network}</li>)}</ul></details>}
    </form>
  </Dialog>;
}

/* ---------------------------------- Firewall ---------------------------------- */

function Firewall({ canManage }) {
  const servers = useQuery(() => api.servers({ limit: 200 }), []);
  const list = servers.data?.servers ?? [];
  const [serverId, setServerId] = useState('');
  useEffect(() => { if (!serverId && list.length) setServerId((list.find(s => s.isLocal) ?? list[0]).id); }, [list.length]); // eslint-disable-line react-hooks/exhaustive-deps
  const [checkStatus, setCheckStatus] = useState(false);
  const { data, error, refetch } = useQuery(() => serverId ? api.firewall(serverId, checkStatus) : Promise.resolve(null), [serverId, checkStatus]);
  const [editing, setEditing] = useState(null);
  const [feedback, setFeedback] = useState(null);
  const [busy, setBusy] = useState(false);
  async function apply() {
    setBusy(true); setFeedback(null);
    try { const out = await api.applyFirewall(serverId); setFeedback({ tone: 'success', title: `${out.rules} rule${out.rules === 1 ? '' : 's'} applied`, text: `The host now runs rule set ${out.applied}.` }); setCheckStatus(true); refetch(); }
    catch (e) { setFeedback({ tone: 'danger', title: 'Couldn\'t apply', text: e.message }); }
    setBusy(false);
  }
  const st = data?.status ?? {};
  return <section className="settings-group">
    <div className="group-head nw-group-head">
      <div><h2>Firewall</h2><p>Limit who can reach a published port. Rules go into Docker's DOCKER-USER chain, so they only affect container ports, never SSH or other host services. Checked every 10 minutes and re-applied after a reboot.</p></div>
      <div className="nw-head-actions">
        {list.length > 1 && <Select label="Server" value={serverId} onChange={setServerId} options={list.map(s => ({ value: s.id, label: s.name }))} />}
        {canManage && <Button icon={PlusIcon} onClick={() => setEditing({ port: '', protocol: 'tcp', sources: '', note: '', enabled: true })} disabled={!serverId}>Add rule</Button>}
      </div>
    </div>
    {error && <Notice tone="danger" title={error.message} />}
    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}</Notice>}
    {data?.dockerDesktop && <Notice tone="info" title="Docker Desktop forwards ports through its own proxy">Rules are applied, but traffic from your computer to published ports doesn't pass through them there. On a Linux server they filter as expected.</Notice>}
    {data && <div className="settings-card">
      <SettingsField label="Host status" hint={st.error ? st.error : st.applied ? (st.inSync ? `The host runs exactly these rules (${st.applied}).` : `The host runs ${st.applied === 'none' ? 'no Ignition rules' : `rule set ${st.applied}`}; saved rules are ${st.desired}. Apply to bring it in line.`) : st.dockerUser === false ? 'This Docker has no DOCKER-USER chain, so rules can\'t be applied.' : 'Not checked yet. Checking starts a short helper container on the host.'}>
        {st.applied ? <Status tone={st.inSync ? 'success' : 'warning'}>{st.inSync ? 'In sync' : 'Out of sync'}</Status> : <span className="readonly-value">Unknown</span>}
        {canManage && <Button onClick={() => { setCheckStatus(true); refetch(); }}>Check</Button>}
        {canManage && <Button variant="primary" icon={PlayIcon} onClick={apply} disabled={busy}>{busy ? 'Applying…' : 'Apply now'}</Button>}
      </SettingsField>
    </div>}
    {data && <div className="monitor-table-wrap">
      <table className="monitor-table nw-compact">
        <thead><tr><th className="num">Port</th><th>Protocol</th><th>Allowed from</th><th>Note</th><th>Enabled</th><th className="actions"><span className="visually-hidden">Actions</span></th></tr></thead>
        <tbody>{data.rules.map(r => <tr key={r.id} onClick={() => canManage && setEditing({ ...r, sources: r.sources.join(', ') })}>
          <td className="num mono">{r.port}</td><td className="muted">{r.protocol}</td>
          <td>{r.sources.length ? <span className="mono">{r.sources.join(', ')}</span> : <span className="rp-chip danger">Nobody (port closed)</span>}</td>
          <td className="muted">{r.note}</td><td>{r.enabled ? <Status tone="success">On</Status> : <Status tone="neutral">Off</Status>}</td>
          <td className="actions" onClick={e => e.stopPropagation()}>{canManage && <Menu trigger={EllipsisHorizontalIcon} label={`Actions for port ${r.port}`} items={[
            { label: 'Delete', icon: TrashIcon, danger: true, onSelect: async () => { await api.deleteFirewallRule(r.id); setFeedback({ tone: 'warning', title: `Rule for ${r.port} deleted`, text: 'Apply to remove it from the host.' }); refetch(); } }]} />}</td>
        </tr>)}</tbody>
      </table>
      {!data.rules.length && <p className="monitor-empty">No rules: published ports are open to whatever their bind address allows.</p>}
    </div>}
    {editing && <RuleDialog serverId={serverId} rule={editing} you={data?.you} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); setFeedback({ tone: 'warning', title: 'Rule saved', text: 'Apply to put it on the host now (it would also be applied within 10 minutes).' }); refetch(); }} />}
  </section>;
}

function RuleDialog({ serverId, rule, you, onClose, onSaved }) {
  const [v, setV] = useState(rule);
  const [errors, setErrors] = useState({});
  const set = k => e => { const val = e?.target ? e.target.value : e; setV(x => ({ ...x, [k]: val })); setErrors(x => ({ ...x, [k]: undefined })); };
  return <Dialog open onClose={onClose} title={rule.id ? `Rule for port ${rule.port}` : 'Add a firewall rule'} className="nw-dialog"
    description="Only the listed addresses can reach this published port; everyone else is dropped. An empty list closes the port."
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" onClick={async () => { try { await api.saveFirewallRule(serverId, { ...v, port: Number(v.port), sources: v.sources.split(/[\s,]+/).filter(Boolean) }); onSaved(); } catch (e) { setErrors(e.fields ?? { port: e.message }); } }}>Save</Button></>}>
    <div className="nw-form">
      <div className="nw-row">
        <FormField label="Host port" htmlFor="fw-port" error={errors.port}><TextInput id="fw-port" type="number" value={v.port} onChange={set('port')} disabled={!!rule.id} autoFocus /></FormField>
        <FormField label="Protocol" htmlFor="fw-proto" error={errors.protocol}><Select id="fw-proto" label="Protocol" value={v.protocol} onChange={set('protocol')} options={[{ value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }]} /></FormField>
      </div>
      <FormField label="Allowed from" htmlFor="fw-src" error={errors.sources} hint={`IPs or ranges, comma-separated. You're ${you}.`}><TextInput id="fw-src" className="mono" value={v.sources} onChange={set('sources')} placeholder="203.0.113.0/24, 2001:db8::/32" /></FormField>
      <FormField label="Note" htmlFor="fw-note" error={errors.note}><TextInput id="fw-note" value={v.note} onChange={set('note')} placeholder="Postgres for the office only" maxLength={200} /></FormField>
      <label className="nw-check"><Switch label="Enabled" checked={v.enabled} onChange={set('enabled')} /><span>Disabled rules are kept but not applied.</span></label>
    </div>
  </Dialog>;
}

/* -------------------------------- Diagnostics --------------------------------- */

const kinds = [
  { value: 'dns', label: 'DNS', placeholder: 'example.com' },
  { value: 'tcp', label: 'TCP', placeholder: 'db.internal' },
  { value: 'http', label: 'HTTP', placeholder: 'https://example.com/health' },
  { value: 'ping', label: 'Ping', placeholder: '1.1.1.1' },
  { value: 'traceroute', label: 'Traceroute', placeholder: 'example.com' },
];

function Diagnostics() {
  const servers = useQuery(() => api.servers({ limit: 200 }), []);
  const nets = useQuery(() => api.networks(), []);
  const [v, setV] = useState({ serverId: '', network: 'host', kind: 'dns', target: '', port: '' });
  const [result, setResult] = useState(null);
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  const serverList = servers.data?.servers ?? [];
  const local = serverList.find(s => s.isLocal);
  const isLocal = !v.serverId || v.serverId === local?.id;
  const netOptions = [{ value: 'host', label: 'Host network', description: 'What the server itself sees' },
    ...(isLocal ? [...(nets.data?.environments ?? []).map(e => ({ value: e.docker, label: e.name, description: 'Environment network' })),
      ...(nets.data?.shared ?? []).map(n => ({ value: n.docker, label: n.name, description: 'Shared network' }))] : [])];
  async function run(e) {
    e.preventDefault(); setPending(true); setError(''); setResult(null);
    try { setResult(await api.diagnose({ ...v, serverId: v.serverId || local?.id, port: Number(v.port) || 0 })); } catch (err) { setError(Object.values(err.fields ?? {})[0] ?? err.message); }
    setPending(false);
  }
  return <section className="settings-group">
    <h2>Diagnostics</h2>
    <p>Run a test from a server, either on its host network or inside one of Ignition's networks (what a container there would see).</p>
    <form className="settings-card nw-diag" onSubmit={run}>
      <div className="nw-diag-row">
        <SegmentedControl label="Test" className="auto-width" value={v.kind} onChange={k => setV(x => ({ ...x, kind: k }))} options={kinds.map(k => ({ value: k.value, label: k.label }))} />
        {serverList.length > 1 && <Select label="Server" value={v.serverId || local?.id || ''} onChange={id => setV(x => ({ ...x, serverId: id, network: 'host' }))} options={serverList.map(s => ({ value: s.id, label: s.name }))} />}
        <Select label="From" value={v.network} onChange={n => setV(x => ({ ...x, network: n }))} options={netOptions} searchable={8} />
      </div>
      <div className="nw-diag-row">
        <TextInput className="mono nw-diag-target" value={v.target} onChange={e => setV(x => ({ ...x, target: e.target.value }))} placeholder={kinds.find(k => k.value === v.kind).placeholder} aria-label="Target" />
        {v.kind === 'tcp' && <TextInput className="nw-port-input" type="number" value={v.port} onChange={e => setV(x => ({ ...x, port: e.target.value }))} placeholder="5432" aria-label="Port" />}
        <Button type="submit" variant="primary" icon={BeakerIcon} disabled={pending || !v.target.trim()}>{pending ? 'Running…' : 'Run'}</Button>
      </div>
      {error && <Notice tone="danger" title={error} />}
      {result && <div className="nw-diag-result">
        <div className="nw-diag-head"><Status tone={result.ok ? 'success' : 'danger'}>{result.ok ? 'Succeeded' : `Failed (exit ${result.exitCode})`}</Status><span className="muted">{result.server} · {result.network} · {result.ms} ms · <code>{result.command}</code></span></div>
        <pre className="nw-log">{result.output || 'No output.'}</pre>
      </div>}
    </form>
  </section>;
}

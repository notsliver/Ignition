import { useMemo, useState } from 'react';
import {
  ArrowPathIcon, ArrowTopRightOnSquareIcon, CloudIcon, DocumentTextIcon, EllipsisHorizontalIcon, GlobeAltIcon, PlusIcon, TrashIcon,
} from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, Menu, Notice, SearchField, SegmentedControl, Select, Status, Switch, TextInput, cx } from '../../ui';
import { api } from '../../api/client';
import { timeAgo, useQuery } from '../../api/hooks';
import { can, useSession } from '../../app/AuthGate';
import { SettingsField, SettingsGroup } from '../settings/SettingsField';
import '../Monitor/Monitor.css';
import '../project/resource/resource.css';
import '../settings/settings.css';
import './network.css';

/*
 * Infrastructure › Domains: every hostname and path Ignition routes, across all resources, with
 * its live status and certificate. Routes can send a path to another resource, redirect, or only
 * let some networks in. Below: the proxy that serves them (settings, certificates, the route
 * table, logs) and DNS provider integrations that create the records.
 */

const statusView = {
  active: { tone: 'success', label: 'Active' },
  pending: { tone: 'progress', label: 'Pending' },
  'dns-missing': { tone: 'warning', label: 'DNS missing' },
  unreachable: { tone: 'danger', label: 'Unreachable' },
};
const DAY = 864e5;
const certDays = d => d.certExpiresAt ? Math.floor((new Date(d.certExpiresAt) - Date.now()) / DAY) : null;
const routeLabel = d => d.redirectTo ? `Redirect ${d.redirectCode} → ${d.redirectTo.replace('{path}', '…')}` : `${d.resourceName}${d.port ? `:${d.port}` : ''}`;
const filters = [{ value: 'all', label: 'All' }, { value: 'attention', label: 'Needs attention' }, { value: 'routing', label: 'Paths & redirects' }];

export function Domains({ onOpenResource, project, environment }) {
  const { user } = useSession();
  // Inside a project, only its routes (in the selected environment); the proxy and DNS sections live under Infrastructure.
  const scoped = !!project;
  const { data, error, refetch } = useQuery(() => api.allDomains(scoped ? { project: project.id, env: environment } : {}), [project?.id, environment], { pollMs: 15000 });
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState('all');
  const [adding, setAdding] = useState(null);
  const [deleting, setDeleting] = useState(null);
  const [feedback, setFeedback] = useState(null);
  const list = data?.domains ?? [];

  const hosts = useMemo(() => {
    const q = query.trim().toLowerCase();
    const map = new Map();
    for (const d of list) {
      if (q && ![d.hostname, d.pathPrefix, d.resourceName, d.projectName, d.redirectTo].some(v => v?.toLowerCase().includes(q))) continue;
      if (filter === 'attention' && d.status === 'active' && !(certDays(d) != null && certDays(d) < 14)) continue;
      if (filter === 'routing' && !d.pathPrefix && !d.redirectTo) continue;
      if (!map.has(d.hostname)) map.set(d.hostname, []);
      map.get(d.hostname).push(d);
    }
    return [...map.entries()];
  }, [list, query, filter]);

  const hostnames = new Set(list.map(d => d.hostname));
  const active = new Set(list.filter(d => d.status === 'active').map(d => d.hostname)).size;
  const attention = new Set(list.filter(d => d.status !== 'active').map(d => d.hostname)).size;
  const expiring = new Set(list.filter(d => certDays(d) != null && certDays(d) < 14).map(d => d.hostname)).size;

  async function run(fn, title) {
    setFeedback(null);
    try { await fn(); setFeedback({ tone: 'success', title }); refetch(); } catch (e) { setFeedback({ tone: 'danger', title: 'Couldn\'t do that', text: e.message }); }
  }

  const actions = d => [
    { label: 'Open resource', icon: ArrowTopRightOnSquareIcon, onSelect: () => onOpenResource?.({ id: d.resourceId, projectId: d.projectId }) },
    { label: 'Check now', icon: ArrowPathIcon, onSelect: () => run(() => api.checkDomain(d.id), `${d.hostname} checked`) },
    ...(can(user, 'domains.manage') && (data?.dnsProviders ?? []).length ? (data.dnsProviders).map(p => ({ label: `Create DNS records with ${p.name}`, icon: CloudIcon,
      onSelect: () => run(() => api.domainDns(d.id, p.id), `DNS records for ${d.hostname} created`) })) : []),
    ...(can(user, 'domains.manage') ? ['separator', { label: 'Delete route', icon: TrashIcon, danger: true, onSelect: () => setDeleting(d) }] : []),
  ];

  return <div className="monitor nw-page">
    <div className="rp-intro">
      <p>{scoped ? <>Hostnames that reach {project.name} in {environment}. Paths can go to different resources, redirect, or only let some networks in. Proxy, certificates and DNS providers are under <a href="/domains" onClick={e => { e.preventDefault(); history.pushState(null, '', '/domains'); dispatchEvent(new PopStateEvent('popstate')); }}>Infrastructure › Domains</a>.</>
        : 'Every hostname Ignition serves. A hostname can route paths to different resources, redirect, or only let some networks in. Certificates are issued automatically.'}</p>
      <div className="rp-intro-actions">
        <Button icon={ArrowPathIcon} onClick={() => run(api.recheckDomains, 'Checking every domain in the background')}>Check all</Button>
        {can(user, 'domains.manage') && <Button variant="primary" icon={PlusIcon} onClick={() => setAdding({})}>Add domain</Button>}
      </div>
    </div>

    <section className="nw-summary">
      <div><span>Hostnames</span><strong>{data ? hostnames.size : '—'}</strong><small>{list.length} route{list.length === 1 ? '' : 's'}</small></div>
      <div><span>Active</span><strong>{data ? active : '—'}</strong><small>Reaching this server</small></div>
      <div className={cx(attention > 0 && 'warn')}><span>Need attention</span><strong>{data ? attention : '—'}</strong><small>DNS, reachability or certificate pending</small></div>
      <div className={cx(expiring > 0 && 'warn')}><span>Certificates expiring</span><strong>{data ? expiring : '—'}</strong><small>Within 14 days</small></div>
    </section>

    {data && !data.proxy.running && <Notice tone="danger" title="The domain proxy isn't running">Domains can't be served until it is. Run the installer again to start it.</Notice>}
    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}</Notice>}
    {error && !data && <Notice tone="danger" title="Couldn't load domains">{error.message}</Notice>}

    <div className="monitor-toolbar">
      <SegmentedControl label="Filter" className="auto-width" options={filters} value={filter} onChange={setFilter} />
      <SearchField value={query} onChange={setQuery} placeholder="Search hostname, path, resource or project" className="nw-search" />
    </div>

    <div className="monitor-table-wrap">
      <table className="monitor-table nw-domains">
        <thead><tr><th>Hostname and path</th><th>Routes to</th><th>Status</th><th>Certificate</th><th>DNS</th><th className="actions"><span className="visually-hidden">Actions</span></th></tr></thead>
        {hosts.map(([host, routes]) => <tbody key={host}>
          {routes.map((d, i) => {
            const st = statusView[d.status] ?? statusView.pending;
            const days = certDays(d);
            return <tr key={d.id} className={cx(i > 0 && 'nw-subroute')} onClick={() => onOpenResource?.({ id: d.resourceId, projectId: d.projectId })}>
              <td>
                <span className="resource-name nw-host">
                  {i === 0 ? <><GlobeAltIcon /><a href={`${d.https ? 'https' : 'http'}://${d.hostname}${d.pathPrefix}`} target="_blank" rel="noreferrer" onClick={e => e.stopPropagation()}>{d.hostname}</a></> : <span className="nw-indent" />}
                  <code>{d.pathPrefix || '/'}{d.pathPrefix && d.stripPrefix ? ' (stripped)' : ''}</code>
                  {d.allowIps.length > 0 && <span className="rp-chip warning" title={d.allowIps.join(', ')}>{d.allowIps.length} allowed network{d.allowIps.length === 1 ? '' : 's'}</span>}
                  {!d.https && <span className="rp-chip">HTTP only</span>}
                </span>
              </td>
              <td><span className="resource-name">{routeLabel(d)}{!d.redirectTo && <span>{d.projectName} · {d.environment}</span>}</span></td>
              <td title={d.statusMessage}>{i === 0 ? <Status tone={st.tone}>{st.label}</Status> : <span className="muted">same host</span>}</td>
              <td className={cx(days != null && days < 14 && 'nw-warn')}>{i > 0 ? '' : !d.https ? <span className="muted">—</span> : d.certIssuer ? <span title={`${d.certIssuer}, expires ${new Date(d.certExpiresAt).toLocaleDateString()}`}>{d.certIssuer.replace(/^Let's Encrypt\s*/, 'Let\'s Encrypt ')} · {days} days</span> : <span className="muted">Waiting</span>}</td>
              <td className="muted">{d.dnsRecords.length ? d.dnsRecords.map(r => `${r.type} ${r.content}`).join(', ') : i === 0 && d.addresses.length ? d.addresses.slice(0, 2).join(', ') : ''}</td>
              <td className="actions" onClick={e => e.stopPropagation()}><Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${d.hostname}${d.pathPrefix}`} items={actions(d)} /></td>
            </tr>;
          })}
        </tbody>)}
      </table>
      {!hosts.length && <div className="rp-empty">
        <GlobeAltIcon />
        <strong>{list.length ? 'Nothing matches' : 'No domains yet'}</strong>
        <p>{list.length ? 'Try another search or filter.' : `Point a hostname at ${scoped ? `an application in ${environment}` : 'an application'}. Names under .localhost work on this machine without DNS.`}</p>
        {!list.length && can(user, 'domains.manage') && <Button variant="primary" icon={PlusIcon} onClick={() => setAdding({})}>Add domain</Button>}
      </div>}
    </div>

    {!scoped && <ProxySection canManage={can(user, 'proxy.manage')} />}
    {!scoped && <DnsProviders canManage={can(user, 'dns.manage')} publicIpv4={data?.publicIpv4} publicIpv6={data?.publicIpv6} onChanged={refetch} />}

    {adding && <AddRouteDialog initial={adding} projectId={project?.id} environment={environment} providers={data?.dnsProviders ?? []} publicIpv4={data?.publicIpv4} onClose={() => setAdding(null)}
      onAdded={(d, dnsError) => { setAdding(null); setFeedback(dnsError ? { tone: 'warning', title: `${d.hostname}${d.pathPrefix} added, but DNS records weren't created`, text: dnsError } : { tone: 'success', title: `${d.hostname}${d.pathPrefix} added`, text: d.statusMessage }); refetch(); }} />}
    {deleting && <DeleteRouteDialog domain={deleting} onClose={() => setDeleting(null)} onDeleted={() => { setFeedback({ tone: 'success', title: `${deleting.hostname}${deleting.pathPrefix} deleted` }); setDeleting(null); refetch(); }} />}
  </div>;
}

/* ------------------------------- Add a route ------------------------------- */

export function AddRouteDialog({ initial = {}, resourceId, projectId, environment, providers = [], publicIpv4, onClose, onAdded }) {
  const resources = useQuery(() => api.allResources(), [], { enabled: !resourceId });
  const apps = (resources.data?.resources ?? []).filter(r => r.type === 'application' && (!projectId || (r.projectId === projectId && (!environment || r.environment === environment))));
  const [v, setV] = useState({ resourceId: resourceId ?? '', hostname: initial.hostname ?? '', mode: 'proxy', pathPrefix: '', stripPrefix: false, port: '', https: true,
    redirectTo: '', redirectCode: '308', allowIps: '', dnsProviderId: '' });
  const [errors, setErrors] = useState({});
  const [pending, setPending] = useState(false);
  const set = k => e => { const val = e?.target ? (e.target.type === 'checkbox' ? e.target.checked : e.target.value) : e; setV(x => ({ ...x, [k]: val })); setErrors(x => ({ ...x, [k]: undefined, _: undefined })); };
  const host = v.hostname.trim().toLowerCase();
  const zoneMatch = p => p.zones.find(z => host === z || host.endsWith(`.${z}`));
  async function submit(e) {
    e.preventDefault();
    if (!v.resourceId) return setErrors({ resourceId: 'Pick the application to route to.' });
    setPending(true);
    try {
      const out = await api.createDomain(v.resourceId, {
        hostname: v.hostname, pathPrefix: v.pathPrefix, stripPrefix: v.mode === 'proxy' && v.stripPrefix, https: v.https,
        port: v.mode === 'proxy' && v.port ? Number(v.port) : undefined,
        redirectTo: v.mode === 'redirect' ? v.redirectTo : '', redirectCode: Number(v.redirectCode),
        allowIps: v.allowIps.split(/[\s,]+/).filter(Boolean), dnsProviderId: v.dnsProviderId || undefined,
      });
      onAdded(out.domain, out.dnsError);
    } catch (err) { setErrors(Object.keys(err.fields ?? {}).length ? err.fields : { _: err.message }); setPending(false); }
  }
  return <Dialog open onClose={onClose} icon={GlobeAltIcon} title="Add a domain route" className="nw-dialog"
    description="Send a hostname, or one path on it, to an application. Ownership is checked and certificates issued automatically."
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" type="submit" form="route-form" disabled={pending}>{pending ? 'Adding…' : 'Add route'}</Button></>}>
    <form id="route-form" className="nw-form" onSubmit={submit}>
      {errors._ && <Notice tone="danger" title={errors._} />}
      {!resourceId && <FormField label="Application" htmlFor="rt-res" error={errors.resourceId}>
        <Select id="rt-res" label="Application" value={v.resourceId} onChange={set('resourceId')} searchable={8}
          options={[{ value: '', label: resources.data ? (apps.length ? 'Choose an application' : 'No applications yet') : 'Loading…' },
            ...apps.map(r => ({ value: r.id, label: r.name, description: `${r.projectName ?? ''}${r.environment ? ` · ${r.environment}` : ''}` }))]} />
      </FormField>}
      <div className="nw-row">
        <FormField label="Hostname" htmlFor="rt-host" error={errors.hostname} hint="app.example.com, or app.localhost on this machine"><TextInput id="rt-host" value={v.hostname} onChange={set('hostname')} placeholder="app.example.com" autoFocus /></FormField>
        <FormField label="Path prefix" htmlFor="rt-path" error={errors.pathPrefix} hint="Empty for the whole site"><TextInput id="rt-path" className="mono" value={v.pathPrefix} onChange={set('pathPrefix')} placeholder="/api" /></FormField>
      </div>
      <SegmentedControl label="What it does" className="auto-width" value={v.mode} onChange={set('mode')} options={[{ value: 'proxy', label: 'Serve the app' }, { value: 'redirect', label: 'Redirect' }]} />
      {v.mode === 'proxy' ? <div className="nw-row">
        <FormField label="Port" htmlFor="rt-port" error={errors.port} hint="Empty uses the app's port"><TextInput id="rt-port" type="number" min={1} max={65535} value={v.port} onChange={set('port')} /></FormField>
        <label className="nw-check"><Switch label="Strip prefix" checked={v.stripPrefix} onChange={set('stripPrefix')} disabled={!v.pathPrefix.trim()} /><span>Remove the prefix before passing the request on (/api/users → /users)</span></label>
      </div> : <div className="nw-row">
        <FormField label="Redirect to" htmlFor="rt-to" error={errors.redirectTo} hint="{path} keeps the requested path"><TextInput id="rt-to" value={v.redirectTo} onChange={set('redirectTo')} placeholder="https://example.com{path}" /></FormField>
        <FormField label="Status" htmlFor="rt-code"><Select id="rt-code" label="Status" value={v.redirectCode} onChange={set('redirectCode')}
          options={[{ value: '308', label: '308 Permanent' }, { value: '301', label: '301 Permanent (legacy)' }, { value: '307', label: '307 Temporary' }, { value: '302', label: '302 Temporary (legacy)' }]} /></FormField>
      </div>}
      <FormField label="Allowed networks" htmlFor="rt-allow" error={errors.allowIps} hint="Optional. IPs or ranges, comma-separated. Everyone else gets a 403.">
        <TextInput id="rt-allow" className="mono" value={v.allowIps} onChange={set('allowIps')} placeholder="203.0.113.0/24, 2001:db8::/32" />
      </FormField>
      <label className="nw-check"><Switch label="HTTPS" checked={v.https} onChange={set('https')} /><span>HTTPS with an automatic certificate, and HTTP redirects to it</span></label>
      {providers.length > 0 && host && !host.endsWith('.localhost') && <FormField label="DNS records" htmlFor="rt-dns" hint={v.dnsProviderId ? `Creates A${publicIpv4 ? ` → ${publicIpv4}` : ''} (and AAAA when there's an IPv6 address), not proxied.` : 'Create the records yourself, or let a provider do it.'}>
        <Select id="rt-dns" label="DNS provider" value={v.dnsProviderId} onChange={set('dnsProviderId')}
          options={[{ value: '', label: 'I\'ll create them myself' }, ...providers.map(p => ({ value: p.id, label: p.name, description: zoneMatch(p) ? `Zone ${zoneMatch(p)}` : 'No zone for this hostname' }))]} />
      </FormField>}
    </form>
  </Dialog>;
}

function DeleteRouteDialog({ domain: d, onClose, onDeleted }) {
  const [dns, setDns] = useState(d.dnsRecords.length > 0);
  const [error, setError] = useState('');
  return <Dialog open onClose={onClose} icon={TrashIcon} title={`Delete ${d.hostname}${d.pathPrefix}?`}
    description={`Requests to it stop reaching ${d.resourceName} right away.`}
    footer={<><Button onClick={onClose}>Cancel</Button><Button className="danger-solid" onClick={async () => { try { await api.deleteDomain(d.id, dns); onDeleted(); } catch (e) { setError(e.message); } }}>Delete</Button></>}>
    {error && <Notice tone="danger" title={error} />}
    {d.dnsRecords.length > 0 && <label className="nw-check"><Switch label="Remove DNS records" checked={dns} onChange={setDns} /><span>Also remove {d.dnsRecords.map(r => `${r.type} ${r.content}`).join(' and ')} at the DNS provider</span></label>}
  </Dialog>;
}

/* ------------------------------- Proxy & certificates ------------------------------- */

function ProxySection({ canManage }) {
  const { data, error, refetch } = useQuery(() => api.proxy(), [], { pollMs: 20000 });
  const [form, setForm] = useState(null);
  const [errors, setErrors] = useState({});
  const [feedback, setFeedback] = useState(null);
  const [logs, setLogs] = useState(false);
  const v = form ?? data?.settings;
  const dirty = form && JSON.stringify(form) !== JSON.stringify(data?.settings);
  const set = k => val => { setForm(f => ({ ...(f ?? data.settings), [k]: val })); setErrors(e => ({ ...e, [k]: undefined })); };
  async function save() {
    try { await api.saveProxy({ ...v, maxBodyMb: Number(v.maxBodyMb) || 0 }); setForm(null); setFeedback({ tone: 'success', title: 'Proxy settings saved and applied' }); refetch(); }
    catch (e) { setErrors(e.fields ?? {}); setFeedback({ tone: 'danger', title: 'Couldn\'t save', text: e.message }); }
  }
  return <>
    <SettingsGroup title="Proxy & certificates" description={data ? `Caddy listens on ports ${data.httpPort} and ${data.httpsPort}. Certificates for public names come from ACME; names under .localhost get a local certificate.` : 'Loading…'}>
      {error && <div className="field-notice"><Notice tone="danger" title="Couldn't read the proxy">{error.message}</Notice></div>}
      {feedback && <div className="field-notice"><Notice tone={feedback.tone} title={feedback.title}>{feedback.text}</Notice></div>}
      {data && <>
        <SettingsField label="Status" hint={data.running ? (data.current ? `Running the current configuration (${data.configHash}).` : 'Running, but an older configuration. It updates within seconds, or reload now.') : 'Not running. Run the installer again to start it.'}>
          <Status tone={!data.running ? 'danger' : data.current ? 'success' : 'warning'}>{!data.running ? 'Stopped' : data.current ? 'Up to date' : 'Updating'}</Status>
          {canManage && <Button icon={ArrowPathIcon} onClick={async () => { await api.reloadProxy().catch(e => setFeedback({ tone: 'danger', title: e.message })); refetch(); }}>Reload</Button>}
          {canManage && <Button icon={DocumentTextIcon} onClick={() => setLogs(true)}>Logs</Button>}
        </SettingsField>
        <SettingsField label="Certificate authority" hint="Use staging while testing: it has no rate limits, but browsers don't trust its certificates." htmlFor="acmeCa">
          <Select id="acmeCa" label="Certificate authority" value={v.acmeCa} onChange={set('acmeCa')} disabled={!canManage}
            options={[{ value: 'letsencrypt', label: 'Let\'s Encrypt' }, { value: 'letsencrypt-staging', label: 'Let\'s Encrypt staging' }]} />
        </SettingsField>
        <SettingsField label="Contact email" hint={errors.acmeEmail ?? 'Let\'s Encrypt writes here if a certificate is about to expire unrenewed. Optional.'} htmlFor="acmeEmail">
          <TextInput id="acmeEmail" type="email" value={v.acmeEmail} onChange={e => set('acmeEmail')(e.target.value)} disabled={!canManage} placeholder="ops@example.com" />
        </SettingsField>
        <SettingsField label="Compression" hint="gzip and zstd for responses that benefit from it.">
          <Switch label="Compression" checked={v.compression} onChange={set('compression')} disabled={!canManage} />
        </SettingsField>
        <SettingsField label="HSTS" hint="Tell browsers to only use HTTPS for a year. Hard to undo; turn it on once HTTPS works everywhere.">
          <Switch label="HSTS" checked={v.hsts} onChange={set('hsts')} disabled={!canManage} />
        </SettingsField>
        <SettingsField label="Request size limit" hint={errors.maxBodyMb ?? 'Uploads larger than this are refused. 0 means no limit.'} htmlFor="maxBodyMb">
          <TextInput id="maxBodyMb" type="number" min={0} max={10240} value={v.maxBodyMb} onChange={e => set('maxBodyMb')(e.target.value)} disabled={!canManage} suffix="MB" />
        </SettingsField>
        {dirty && <div className="field-notice nw-actions"><Button onClick={() => { setForm(null); setErrors({}); }}>Discard</Button><Button variant="primary" onClick={save}>Save and apply</Button></div>}
      </>}
    </SettingsGroup>

    {data && <section className="settings-group">
      <h2>Traffic routing</h2>
      <p>What the proxy does with each request, in the order it checks them. More specific paths win.</p>
      <div className="monitor-table-wrap">
        <table className="monitor-table nw-routes">
          <thead><tr><th>Match</th><th>Action</th><th>Restrictions</th><th>Live</th></tr></thead>
          <tbody>{data.routes.map((r, i) => <tr key={i}>
            <td className="mono">{r.https ? 'https' : 'http'}://{r.hostname}{r.path || ''}{r.path ? '/*' : '/*'}</td>
            <td>{r.redirect ? `Redirect ${r.code} → ${r.redirect.replace('{path}', '{path}')}` : `Proxy to ${r.target}${r.strip ? ` (strip ${r.path})` : ''}`}</td>
            <td className="muted">{r.allowIps.length ? `Only ${r.allowIps.join(', ')}` : 'Anyone'}</td>
            <td>{r.live ? <Status tone="success">Serving</Status> : <Status tone="neutral">App stopped</Status>}</td>
          </tr>)}</tbody>
        </table>
        {!data.routes.length && <p className="monitor-empty">No routes yet. Anything that reaches the proxy gets a friendly "not set up" page.</p>}
      </div>
    </section>}
    {logs && <ProxyLogs onClose={() => setLogs(false)} />}
  </>;
}

function ProxyLogs({ onClose }) {
  const { data, error, refetch } = useQuery(() => api.proxyLogs(), [], { pollMs: 5000 });
  return <Dialog open onClose={onClose} icon={DocumentTextIcon} title="Proxy logs" className="nw-logs-dialog" description="The last 300 lines from the Caddy container, refreshed every 5 seconds."
    footer={<><Button icon={ArrowPathIcon} onClick={refetch}>Refresh</Button><Button variant="primary" onClick={onClose}>Done</Button></>}>
    {error && <Notice tone="danger" title={error.message} />}
    <pre className="nw-log">{(data?.lines ?? []).map(l => l.message).join('\n') || (data ? 'No output yet.' : 'Loading…')}</pre>
  </Dialog>;
}

/* --------------------------------- DNS providers --------------------------------- */

function DnsProviders({ canManage, publicIpv4, publicIpv6, onChanged }) {
  const { data, refetch } = useQuery(() => api.dnsProviders(), []);
  const [adding, setAdding] = useState(false);
  const [feedback, setFeedback] = useState(null);
  const list = data?.providers ?? [];
  async function act(fn, title) {
    try { await fn(); setFeedback({ tone: 'success', title }); refetch(); onChanged(); } catch (e) { setFeedback({ tone: 'danger', title: 'Couldn\'t do that', text: e.message }); }
  }
  return <section className="settings-group">
    <div className="group-head nw-group-head">
      <div><h2>DNS integrations</h2><p>Let Ignition create the A and AAAA records that point a domain at this server{publicIpv4 || publicIpv6 ? ` (${[publicIpv4, publicIpv6].filter(Boolean).join(', ')})` : ''}.</p></div>
      {canManage && <Button icon={PlusIcon} onClick={() => setAdding(true)}>Connect Cloudflare</Button>}
    </div>
    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}</Notice>}
    <div className="settings-card">
      {list.length ? <ul className="nw-providers">{list.map(p => <li key={p.id}>
        <CloudIcon />
        <span className="nw-provider-main"><strong>{p.name}</strong><small>Cloudflare · {p.zones.length} zone{p.zones.length === 1 ? '' : 's'}: {p.zones.slice(0, 4).join(', ')}{p.zones.length > 4 ? '…' : ''} · {p.domains} domain{p.domains === 1 ? '' : 's'} · checked {timeAgo(p.verifiedAt)}</small></span>
        {canManage && <Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${p.name}`} items={[
          { label: 'Refresh zones', icon: ArrowPathIcon, onSelect: () => act(() => api.refreshDnsProvider(p.id), `${p.name} zones refreshed`) },
          { label: 'Remove', icon: TrashIcon, danger: true, onSelect: () => act(() => api.deleteDnsProvider(p.id), `${p.name} removed; its records stay at Cloudflare`) },
        ]} />}
      </li>)}</ul> : <p className="nw-empty">No DNS provider connected. Records are then added by hand at your registrar; each domain shows what it needs.</p>}
    </div>
    {adding && <ConnectDnsDialog onClose={() => setAdding(false)} onConnected={z => { setAdding(false); setFeedback({ tone: 'success', title: `Connected · ${z.length} zone${z.length === 1 ? '' : 's'}`, text: z.join(', ') }); refetch(); onChanged(); }} />}
  </section>;
}

function ConnectDnsDialog({ onClose, onConnected }) {
  const [v, setV] = useState({ name: 'Cloudflare', token: '' });
  const [errors, setErrors] = useState({});
  const [pending, setPending] = useState(false);
  return <Dialog open onClose={onClose} icon={CloudIcon} title="Connect Cloudflare" className="nw-dialog"
    description="Create an API token at Cloudflare › My Profile › API Tokens with Zone:Read and DNS:Edit for the zones Ignition may change. It's verified now and stored encrypted."
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" type="submit" form="dns-form" disabled={pending}>{pending ? 'Verifying…' : 'Verify and connect'}</Button></>}>
    <form id="dns-form" className="nw-form" onSubmit={async e => {
      e.preventDefault(); setPending(true);
      try { onConnected((await api.createDnsProvider({ kind: 'cloudflare', ...v })).zones); } catch (err) { setErrors(Object.keys(err.fields ?? {}).length ? err.fields : { token: err.message }); setPending(false); }
    }}>
      <FormField label="Name" htmlFor="dns-name" error={errors.name}><TextInput id="dns-name" value={v.name} onChange={e => setV(x => ({ ...x, name: e.target.value }))} /></FormField>
      <FormField label="API token" htmlFor="dns-token" error={errors.token}><TextInput id="dns-token" className="mono" type="password" autoComplete="off" value={v.token} onChange={e => { setV(x => ({ ...x, token: e.target.value })); setErrors({}); }} autoFocus /></FormField>
    </form>
  </Dialog>;
}

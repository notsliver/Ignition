import { useEffect, useRef, useState } from 'react';
import { ArrowPathIcon, ArrowTopRightOnSquareIcon, ChevronRightIcon, GlobeAltIcon, LockClosedIcon, LockOpenIcon, PlusIcon, StarIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, Notice, Status, Switch, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { useSession, can } from '../../../app/AuthGate';
import { CopyButton, Empty, PageIntro, Table } from './shared';
import { AddRouteDialog } from '../../network/Domains';

/*
 * Domains are routed by the Caddy proxy bundled with Ignition. Every status comes from a real check:
 * DNS lookup → a request for this domain's token through the proxy → the certificate it serves.
 * The worker re-checks in the background; this page also re-checks domains that are still settling.
 */

const STATES = {
  pending: { tone: 'progress', label: 'Setting up' },
  'dns-missing': { tone: 'warning', label: 'DNS not found' },
  unreachable: { tone: 'danger', label: 'Not reaching this server' },
  active: { tone: 'success', label: 'Active' },
};
const isLocal = host => host.endsWith('.localhost');
const slugify = s => s.toLowerCase().replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '');
const urlFor = (d, proxy) => {
  const https = d.https;
  const port = https ? proxy?.httpsPort : proxy?.httpPort;
  return `${https ? 'https' : 'http'}://${d.hostname}${port && port !== (https ? 443 : 80) ? `:${port}` : ''}`;
};
const recordName = host => host.split('.').length === 2 ? '@' : host.split('.').slice(0, -2).join('.');
const fmtDate = iso => new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });

export function Domains({ resource }) {
  const { user } = useSession();
  const canEdit = can(user, 'domains.manage');
  const { data, error, refetch, setData } = useQuery(() => api.domains(resource.id), [resource.id], { pollMs: 10000 });
  const domains = data?.domains ?? [];
  const proxy = data?.proxy;
  const [draft, setDraft] = useState('');
  const [adding, setAdding] = useState(false);
  const [formError, setFormError] = useState('');
  const [open, setOpen] = useState(null);
  const [checking, setChecking] = useState(() => new Set());
  const [rowError, setRowError] = useState({});
  const [removing, setRemoving] = useState(null);
  const [advanced, setAdvanced] = useState(false);
  const [removeDns, setRemoveDns] = useState(true);
  const suggestion = `${slugify(resource.name)}.localhost`;

  const replace = d => setData(cur => cur && { ...cur, domains: cur.domains.map(x => x.id === d.id ? d : x) });
  const busy = (id, on) => setChecking(s => { const n = new Set(s); on ? n.add(id) : n.delete(id); return n; });

  async function check(d) {
    busy(d.id, true);
    try { replace((await api.checkDomain(d.id)).domain); } catch (err) { setRowError(e => ({ ...e, [d.id]: err.message })); }
    busy(d.id, false);
  }
  async function update(d, patch) {
    setRowError(e => ({ ...e, [d.id]: '' }));
    replace({ ...d, ...patch.optimistic });
    if (patch.body.https !== undefined) busy(d.id, true);
    try {
      const { domain } = await api.updateDomain(d.id, patch.body);
      if (patch.body.primary) refetch(); else replace(domain);
    } catch (err) {
      replace(d);
      setRowError(e => ({ ...e, [d.id]: err.message }));
    }
    busy(d.id, false);
  }

  // Domains that are still settling (certificate on its way, DNS just added) are re-checked while the page is open.
  const settling = domains.filter(d => d.status !== 'active').map(d => d.id).join();
  const checkingRef = useRef(checking);
  checkingRef.current = checking;
  useEffect(() => {
    if (!settling) return;
    const t = setInterval(() => domains.filter(d => d.status === 'pending' && !checkingRef.current.has(d.id)).forEach(check), 8000);
    return () => clearInterval(t);
  }, [settling]); // eslint-disable-line react-hooks/exhaustive-deps

  if (resource.type !== 'application') return <div className="rp-page">
    <Empty icon={GlobeAltIcon} title="This resource doesn't serve HTTP">Domains are for web applications. Other apps on this server reach it at {resource.name}:{resource.port ?? 'its port'}.</Empty>
  </div>;

  async function add(event, hostname = draft) {
    event?.preventDefault();
    if (!hostname.trim()) return setFormError('Enter a domain.');
    setAdding(true); setFormError('');
    try {
      const body = { hostname };
      if (isLocal(hostname.trim().toLowerCase())) body.https = false; // a local certificate would make browsers warn
      const { domain } = await api.createDomain(resource.id, body);
      setData(cur => ({ ...cur, domains: [...(cur?.domains ?? []), domain] }));
      setOpen(domain.id);
      setDraft('');
    } catch (err) {
      setFormError(err.fields?.hostname ?? err.fields?.port ?? err.message);
    }
    setAdding(false);
  }

  return <div className="rp-page">
    <PageIntro>Hostnames that open {resource.name}. Ignition's proxy routes them{proxy && <> on port {proxy.httpPort}{proxy.httpsPort ? ` (HTTPS ${proxy.httpsPort})` : ''}</>} and gets certificates automatically.</PageIntro>
    {proxy && !proxy.running && <Notice tone="danger" title="The domain proxy isn't running">Domains can't be routed until it is. Run the installer again, or start it with: docker compose -f deploy/docker-compose.yml up -d proxy</Notice>}
    {error && !data && <Notice tone="danger" title="Couldn't load domains">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice>}
    {resource.status !== 'running' && domains.length > 0 && <Notice tone="warning" title={`${resource.name} isn't running`}>Its domains show a "not running" page until you start or deploy it.</Notice>}

    {canEdit && <form className="rp-inline-form" onSubmit={add}>
      <TextInput placeholder="app.example.com" value={draft} onChange={e => { setDraft(e.target.value); setFormError(''); }} aria-label="Domain" aria-invalid={!!formError} autoComplete="off" spellCheck={false} disabled={adding} />
      <Button type="submit" variant="primary" icon={PlusIcon} disabled={adding}>{adding ? 'Adding…' : 'Add domain'}</Button>
      <Button type="button" onClick={() => setAdvanced(true)} title="Path prefixes, redirects, IP allow lists and DNS records">Advanced…</Button>
    </form>}
    {formError && <p className="rp-error">{formError}</p>}
    {canEdit && !domains.some(d => d.hostname === suggestion) && <p className="domain-suggest">No domain yet? <button type="button" className="inline-link" disabled={adding} onClick={() => add(null, suggestion)}>Use {suggestion}</button> — works on this machine with no DNS setup.</p>}

    {domains.length > 0 && <div className="rp-card">
      {domains.map(d => {
        const s = checking.has(d.id) ? { tone: 'progress', label: 'Checking…' } : STATES[d.status] ?? STATES.pending;
        const expanded = open === d.id;
        const url = urlFor(d, proxy);
        return <div key={d.id} className={cx('domain', expanded && 'open')}>
          <div className="rp-row domain-row">
            <button type="button" className="domain-toggle" aria-expanded={expanded} aria-label={`${expanded ? 'Hide' : 'Show'} details for ${d.hostname}`} onClick={() => setOpen(expanded ? null : d.id)}><ChevronRightIcon /></button>
            <span className="rp-row-main">
              <strong className="domain-host"><a href={url + d.pathPrefix} target="_blank" rel="noreferrer">{d.hostname}{d.pathPrefix && <span className="mono muted">{d.pathPrefix}</span>}</a>{d.primary && <span className="rp-chip accent"><StarIcon />Primary</span>}
                {d.redirectTo && <span className="rp-chip">Redirect {d.redirectCode}</span>}{d.allowIps?.length > 0 && <span className="rp-chip warning" title={d.allowIps.join(', ')}>{d.allowIps.length} allowed network{d.allowIps.length === 1 ? '' : 's'}</span>}</strong>
              <span className="domain-meta">
                <Status tone={s.tone}>{s.label}</Status>
                {d.https ? <span className="domain-ssl"><LockClosedIcon />{d.certExpiresAt ? `${d.certIssuer.includes('Caddy Local') ? 'Local certificate' : 'Certificate'} · expires ${fmtDate(d.certExpiresAt)}` : 'HTTPS'}</span> : <span className="domain-ssl"><LockOpenIcon />HTTP only</span>}
                <span>{d.redirectTo ? `→ ${d.redirectTo.replace('{path}', '…')}` : `→ port ${d.port ?? resource.port}${d.stripPrefix ? `, without ${d.pathPrefix}` : ''}`}</span>
              </span>
            </span>
            <div className="domain-actions">
            {d.status === 'active' && <a className="button" href={url} target="_blank" rel="noreferrer"><ArrowTopRightOnSquareIcon />Open</a>}
            <Button icon={ArrowPathIcon} disabled={checking.has(d.id)} onClick={() => check(d)}>Check</Button>
            {canEdit && <button type="button" className="icon-action danger" aria-label={`Remove ${d.hostname}`} onClick={() => setRemoving(d)}><TrashIcon /></button>}
            </div>
          </div>
          {expanded && <div className="domain-body">
            {d.statusMessage && <p className={cx('domain-check', d.status === 'active' ? 'ok' : d.status === 'pending' ? '' : 'bad')}>{d.statusMessage}</p>}
            {rowError[d.id] && <p className="domain-check bad">{rowError[d.id]}</p>}
            {!isLocal(d.hostname) && <>
              <h3>DNS record</h3>
              <Table columns={[{ label: 'Type' }, { label: 'Name' }, { label: 'Value' }, { label: '', className: 'actions' }]}>
                <tr><td className="mono">A</td><td className="mono">{recordName(d.hostname)}</td><td>This machine's public IP address</td><td className="actions"><div><CopyButton value={recordName(d.hostname)} label="Copy record name" /></div></td></tr>
              </Table>
              <p className="domain-cert">Then forward ports {proxy?.httpPort ?? 80} and {proxy?.httpsPort ?? 443} on your router to this machine. For use on your own network only, point the record at this machine's local IP instead.{d.addresses?.length > 0 && <> Currently resolves to <span className="mono">{d.addresses.join(', ')}</span>.</>}</p>
            </>}
            {canEdit && <div className="domain-settings">
              <label>Target port<PortInput domain={d} fallback={resource.port} onSave={body => update(d, { body, optimistic: body.useResourcePort ? { port: null } : { port: body.port } })} /></label>
              <label className="domain-switch"><Switch label={`HTTPS for ${d.hostname}`} checked={d.https} onChange={https => update(d, { body: { https }, optimistic: { https, status: 'pending' } })} />HTTPS{isLocal(d.hostname) && d.https && <span className="muted"> (local certificate)</span>}</label>
              <span className="spacer" />
              {!d.primary && <Button icon={StarIcon} onClick={() => update(d, { body: { primary: true }, optimistic: { primary: true } })}>Make primary</Button>}
            </div>}
            <p className="domain-cert">{d.lastCheckedAt ? `Checked ${timeAgo(d.lastCheckedAt)}` : 'Not checked yet'}{d.certIssuer && ` · issued by ${d.certIssuer}`}{d.primary && ' · used for "Open app" links'}</p>
          </div>}
        </div>;
      })}
    </div>}

    {advanced && <AddRouteDialog resourceId={resource.id} providers={data?.dnsProviders ?? []} onClose={() => setAdvanced(false)} onAdded={(d, dnsError) => { setAdvanced(false); if (dnsError) setFormError(`Added, but DNS records weren't created: ${dnsError}`); refetch(); }} />}
    <Dialog open={!!removing} onClose={() => setRemoving(null)} title={`Remove ${removing?.hostname}${removing?.pathPrefix ?? ''}?`}
      description={removing?.dnsRecords?.length ? 'The domain stops routing to this resource right away.' : 'The domain stops routing to this resource right away. DNS records at your provider are left untouched.'}
      footer={<><Button onClick={() => setRemoving(null)}>Cancel</Button><Button className="danger-solid" onClick={async () => {
        const d = removing;
        setRemoving(null);
        setData(cur => ({ ...cur, domains: cur.domains.filter(x => x.id !== d.id) }));
        try { await api.deleteDomain(d.id, removeDns && d.dnsRecords?.length > 0); } catch (err) { setFormError(err.message); }
        refetch();
      }}>Remove domain</Button></>}>
      {removing?.dnsRecords?.length > 0 && <label className="nw-check"><Switch label="Remove DNS records" checked={removeDns} onChange={setRemoveDns} /><span>Also remove {removing.dnsRecords.map(r => `${r.type} ${r.content}`).join(' and ')} at the DNS provider</span></label>}
    </Dialog>
  </div>;
}

// Saves on Enter or blur. Empty means "follow the resource's port".
function PortInput({ domain, fallback, onSave }) {
  const [value, setValue] = useState(domain.port ? String(domain.port) : '');
  useEffect(() => setValue(domain.port ? String(domain.port) : ''), [domain.port]);
  function commit() {
    const v = value.trim();
    if (v === (domain.port ? String(domain.port) : '')) return;
    if (!v) return onSave({ useResourcePort: true });
    const n = Number(v);
    if (!Number.isInteger(n) || n < 1 || n > 65535) { setValue(domain.port ? String(domain.port) : ''); return; }
    onSave({ port: n });
  }
  return <TextInput className="domain-port mono" inputMode="numeric" value={value} placeholder={fallback ? String(fallback) : 'port'} aria-label={`Target port for ${domain.hostname}`}
    onChange={e => setValue(e.target.value.replace(/\D/g, ''))} onBlur={commit} onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); e.currentTarget.blur(); } }} />;
}

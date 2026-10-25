import { useState } from 'react';
import { ArrowPathIcon, ChevronRightIcon, GlobeAltIcon, LockClosedIcon, PlusIcon, SignalIcon, StarIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, Select, Status, Switch, TextInput, cx } from '../../../ui';
import { CopyButton, Empty, PageIntro, Table, isPublicUrl, portOf, randomId, useLater } from './shared';

/*
 * Lifecycle: DNS not configured → DNS verified → certificate pending → HTTPS active.
 * Failures: certificate renewal failing, domain unreachable.
 */

const HOST_RE = /^(?!-)[a-z0-9-]{1,63}(?<!-)(\.(?!-)[a-z0-9-]{1,63}(?<!-))+$/;
const STATES = {
  'dns-missing': { tone: 'warning', label: 'DNS not configured' },
  'dns-checking': { tone: 'progress', label: 'Checking DNS…' },
  'dns-verified': { tone: 'progress', label: 'DNS verified' },
  'cert-pending': { tone: 'progress', label: 'Certificate pending' },
  active: { tone: 'success', label: 'HTTPS active' },
  'cert-failing': { tone: 'danger', label: 'Certificate renewal failing' },
  unreachable: { tone: 'danger', label: 'Domain unreachable' },
};
const TIPS = {
  'dns-missing': ['Add the record below at your DNS provider, then select Check DNS.', 'Records can take a few minutes, sometimes up to an hour, to propagate.', 'Cloudflare users: set the record to "DNS only" (grey cloud) until HTTPS is active.'],
  'cert-failing': ['Make sure the CAA record (if any) allows letsencrypt.org.', 'Port 80 must reach this server for the HTTP challenge.', 'Too many failed attempts can trigger Let\'s Encrypt rate limits; wait an hour before retrying.'],
  unreachable: ['Check that the server firewall allows ports 80 and 443.', 'Confirm the record still points to 203.0.113.24.', 'Look at the resource logs for crashes or failing health checks.'],
};
const isApex = host => host.split('.').length === 2;
const recordFor = host => isApex(host) ? [{ type: 'A', name: '@', value: '203.0.113.24' }, { type: 'AAAA', name: '@', value: '2001:db8::24' }] : [{ type: 'CNAME', name: host.split('.')[0], value: 'edge.ignition.app' }];

export function Domains({ resource }) {
  const later = useLater();
  const generated = `${resource.name}-${randomId(4)}.ignition.app`;
  const [domains, setDomains] = useState(() => [
    { host: generated, state: 'active', generated: true, primary: !isPublicUrl(resource.url), port: portOf(resource), redirect: true, expires: 'Jan 4, 2027' },
    ...(isPublicUrl(resource.url) ? [{ host: resource.url, state: 'active', primary: true, port: portOf(resource), redirect: true, expires: 'Jan 12, 2027' }] : []),
    { host: 'shop-legacy.example.com', state: 'cert-failing', port: portOf(resource), redirect: true, expires: 'Nov 2, 2026' },
  ]);
  const [draft, setDraft] = useState('');
  const [error, setError] = useState('');
  const [open, setOpen] = useState(null);
  const [checks, setChecks] = useState({});
  const [removing, setRemoving] = useState(null);

  if (resource.type !== 'application') return <div className="rp-page">
    <Empty icon={GlobeAltIcon} title="This resource doesn't serve HTTP">Domains are for web applications. Use Networking to expose a TCP port or share the internal hostname.</Empty>
  </div>;

  const update = (host, patch) => setDomains(list => list.map(d => d.host === host ? { ...d, ...patch } : d));
  const note = (host, text, tone = 'ok') => setChecks(c => ({ ...c, [host]: { text, tone } }));

  function add(event) {
    event.preventDefault();
    const host = draft.trim().toLowerCase().replace(/^https?:\/\//, '').replace(/\/.*$/, '');
    if (host.startsWith('*.')) return setError('Wildcard domains need a DNS-01 challenge, which isn\'t set up on this server yet.');
    if (!HOST_RE.test(host)) return setError('Enter a domain like app.example.com.');
    if (domains.some(d => d.host === host)) return setError('That domain is already added.');
    setDomains(list => [...list, { host, state: 'dns-missing', port: portOf(resource), redirect: true }]);
    setOpen(host);
    setDraft('');
    setError('');
  }
  function checkDns(d) {
    update(d.host, { state: 'dns-checking' });
    later(() => {
      // Demo rule: anything containing "pending" hasn't propagated yet.
      if (d.host.includes('pending')) { update(d.host, { state: 'dns-missing' }); note(d.host, 'No record found yet at 4 of 4 resolvers. Try again in a few minutes.', 'bad'); return; }
      update(d.host, { state: 'dns-verified' });
      note(d.host, `${recordFor(d.host)[0].type} record found at 4 of 4 resolvers.`);
      later(() => update(d.host, { state: 'cert-pending' }), 900);
      later(() => { update(d.host, { state: 'active', expires: 'Jan 23, 2027' }); note(d.host, 'Certificate issued by Let\'s Encrypt (R11).'); }, 2800);
    }, 1200);
  }
  function retryCert(d) {
    update(d.host, { state: 'cert-pending' });
    later(() => { update(d.host, { state: 'active', expires: 'Jan 23, 2027' }); note(d.host, 'Certificate renewed.'); }, 2000);
  }
  function testReach(d) {
    note(d.host, 'Requesting https://' + d.host + '…', 'muted');
    later(() => d.state === 'active' ? note(d.host, `HTTP 200 in 84ms from 3 regions · TLS 1.3`) : note(d.host, 'Connection failed: TLS handshake error', 'bad'), 1100);
  }
  const makePrimary = host => setDomains(list => list.map(d => ({ ...d, primary: d.host === host })));

  return <div className="rp-page">
    <PageIntro>Public hostnames for {resource.name}. Certificates are issued and renewed automatically with Let's Encrypt.</PageIntro>
    <form className="rp-inline-form" onSubmit={add}>
      <TextInput placeholder="app.example.com" value={draft} onChange={e => { setDraft(e.target.value); setError(''); }} aria-label="Domain" aria-invalid={!!error} autoComplete="off" spellCheck={false} />
      <Button type="submit" variant="primary" icon={PlusIcon}>Add domain</Button>
    </form>
    {error && <p className="rp-error">{error}</p>}

    <div className="rp-card">
      {domains.map(d => {
        const s = STATES[d.state];
        const expanded = open === d.host;
        const check = checks[d.host];
        return <div key={d.host} className={cx('domain', expanded && 'open')}>
          <div className="rp-row domain-row">
            <button type="button" className="domain-toggle" aria-expanded={expanded} aria-label={`${expanded ? 'Hide' : 'Show'} details for ${d.host}`} onClick={() => setOpen(expanded ? null : d.host)}><ChevronRightIcon /></button>
            <span className="rp-row-main">
              <strong className="domain-host"><a href={`https://${d.host}`} target="_blank" rel="noreferrer">{d.host}</a>{d.primary && <span className="rp-chip accent"><StarIcon />Primary</span>}{d.generated && <span className="rp-chip">Generated</span>}</strong>
              <span className="domain-meta"><Status tone={s.tone}>{s.label}</Status>{d.state === 'active' && <span className="domain-ssl"><LockClosedIcon />Expires {d.expires}</span>}<span>→ port {d.port}</span></span>
            </span>
            {d.state === 'dns-missing' && <Button onClick={() => checkDns(d)} icon={ArrowPathIcon}>Check DNS</Button>}
            {d.state === 'cert-failing' && <Button onClick={() => retryCert(d)} icon={ArrowPathIcon}>Retry certificate</Button>}
            {!d.generated && <button type="button" className="icon-action danger" aria-label={`Remove ${d.host}`} onClick={() => setRemoving(d)}><TrashIcon /></button>}
          </div>
          {expanded && <div className="domain-body">
            {!d.generated && <>
              <h3>DNS records</h3>
              <Table columns={[{ label: 'Type' }, { label: 'Name' }, { label: 'Value' }, { label: '', className: 'actions' }]}>
                {recordFor(d.host).map(r => <tr key={r.type}><td className="mono">{r.type}</td><td className="mono">{r.name}</td><td className="mono">{r.value}</td><td className="actions"><div><CopyButton value={r.value} label={`Copy ${r.type} value`} /></div></td></tr>)}
              </Table>
            </>}
            <div className="domain-settings">
              <label>Target port<Select label={`Target port for ${d.host}`} value={d.port} onChange={port => update(d.host, { port })} options={[portOf(resource), '8080', '9000'].filter((v, i, a) => a.indexOf(v) === i).map(v => ({ value: v, label: v }))} /></label>
              <label className="domain-switch"><Switch label={`Redirect HTTP to HTTPS for ${d.host}`} checked={d.redirect} onChange={redirect => update(d.host, { redirect })} />Redirect HTTP to HTTPS</label>
              <span className="spacer" />
              <Button icon={SignalIcon} onClick={() => testReach(d)}>Test reachability</Button>
              {!d.primary && d.state === 'active' && <Button icon={StarIcon} onClick={() => makePrimary(d.host)}>Make primary</Button>}
            </div>
            {d.state === 'active' && <p className="domain-cert">Certificate: Let's Encrypt R11 · {d.generated ? '*.ignition.app wildcard' : d.host} · auto-renews 30 days before {d.expires}</p>}
            {check && <p className={cx('domain-check', check.tone)}>{check.text}</p>}
            {TIPS[d.state] && <div className="domain-tips"><h3>Troubleshooting</h3><ul>{TIPS[d.state].map(t => <li key={t}>{t}</li>)}</ul></div>}
          </div>}
        </div>;
      })}
    </div>

    <Dialog open={!!removing} onClose={() => setRemoving(null)} title={`Remove ${removing?.host}?`} description="The domain stops routing to this resource right away. Its certificate is revoked. DNS records at your provider are left untouched."
      footer={<><Button onClick={() => setRemoving(null)}>Cancel</Button><Button className="danger-solid" onClick={() => { setDomains(list => { const rest = list.filter(x => x.host !== removing.host); return removing.primary ? rest.map((x, i) => ({ ...x, primary: i === 0 })) : rest; }); setRemoving(null); }}>Remove domain</Button></>} />
  </div>;
}

import { useEffect, useState } from 'react';
import { ArrowPathIcon, ArrowTopRightOnSquareIcon, LockClosedIcon, LockOpenIcon } from '@heroicons/react/16/solid';
import { Button, Status } from '../../../ui';
import { api } from '../../../api/client';
import { CopyButton } from '../../project/resource/shared';
import './DashboardDomain.css';

/*
 * The dashboard address, checked like an app's domain. A hostname is served by Ignition's proxy
 * (with automatic HTTPS when it starts with https://), so it needs DNS pointing here and the
 * proxy's ports reachable. An IP address only changes links.
 */

const STATES = {
  pending: { tone: 'progress', label: 'Setting up' },
  'dns-missing': { tone: 'warning', label: 'DNS not found' },
  unreachable: { tone: 'danger', label: 'Not reaching this server' },
  active: { tone: 'success', label: 'Active' },
};
const recordName = host => host.split('.').length === 2 ? '@' : host.split('.').slice(0, -2).join('.');
const isIP = host => /^[\d.]+$/.test(host) || host.includes(':');

export function DashboardDomain({ url }) {
  const [state, setState] = useState(null);
  const [checking, setChecking] = useState(false);
  const host = (() => { try { return url ? new URL(url).hostname : ''; } catch { return ''; } })();
  const routed = host && !isIP(host);

  const check = () => {
    setChecking(true);
    api.checkDashboardUrl().then(setState).catch(e => setState({ error: e.message })).finally(() => setChecking(false));
  };
  useEffect(() => { if (routed) check(); else setState(null); }, [url]); // eslint-disable-line react-hooks/exhaustive-deps

  if (!url) return null;
  if (!routed) return <p className="dd-note solo">Ignition is opened directly at this address. Use a domain name instead to have it served with HTTPS through Ignition's proxy.</p>;

  const c = state?.check;
  const s = checking ? { tone: 'progress', label: 'Checking…' } : c ? STATES[c.status] ?? STATES.pending : null;
  const proxy = state?.proxy;
  const https = url.startsWith('https://');
  const port = https ? proxy?.httpsPort : proxy?.httpPort;
  const odd = proxy && (proxy.httpPort !== 80 || proxy.httpsPort !== 443);

  return <div className="dd">
    <div className="dd-head">
      <span className="dd-host">{https ? <LockClosedIcon /> : <LockOpenIcon />}<strong>{host}</strong></span>
      {s && <Status tone={s.tone}>{s.label}</Status>}
      <span className="dd-spacer" />
      {c?.status === 'active' && <a className="button" href={url} target="_blank" rel="noreferrer"><ArrowTopRightOnSquareIcon />Open</a>}
      <Button icon={ArrowPathIcon} disabled={checking} onClick={check}>Check</Button>
    </div>
    {state?.error && <p className="domain-check bad">{state.error}</p>}
    {c?.message && <p className={`domain-check ${c.status === 'active' ? 'ok' : c.status === 'pending' ? '' : 'bad'}`}>{c.message}</p>}
    {c?.status !== 'active' && <>
      <table className="dd-dns">
        <thead><tr><th>Type</th><th>Name</th><th>Value</th><th /></tr></thead>
        <tbody><tr>
          <td className="mono">A</td><td className="mono">{recordName(host)}</td>
          <td className="mono">{state?.serverAddress && state.serverAddress !== 'localhost' ? state.serverAddress : "This server's public IP"}</td>
          <td className="dd-copy"><CopyButton value={recordName(host)} label="Copy record name" /></td>
        </tr></tbody>
      </table>
      <p className="dd-note">Add this record at your DNS provider. If the domain uses Cloudflare's proxy (orange cloud), turn it off until the certificate is issued.</p>
    </>}
    {odd && <p className="dd-note warn">Ignition's proxy is on ports {proxy.httpPort} and {proxy.httpsPort} (another program holds 80 and 443), so this address works as {https ? 'https' : 'http'}://{host}:{port}. Free ports 80 and 443 and re-run the installer with IGNITION_HTTP_PORT=80 IGNITION_HTTPS_PORT=443 for a plain {host}{https ? ' and a trusted certificate' : ''}.</p>}
    <p className="dd-note">The dashboard stays reachable on its own port too, so you can't lock yourself out with a wrong address.</p>
  </div>;
}

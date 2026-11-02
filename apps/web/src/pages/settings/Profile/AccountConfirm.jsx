import { useEffect, useRef, useState } from 'react';
import { Button, Dialog, FormField, Notice, TextInput } from '../../../ui';
import { api } from '../../../api/client';
import { useQuery } from '../../../api/hooks';
import { passkeyCeremony, supportsPasskeys } from '../../../app/passkeys';

/** Retry a concrete operation only after the server confirms this session's identity. */
export function useAccountAction() {
  const [queued, setQueued] = useState(null);
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  async function run(action) {
    setPending(true); setError('');
    try { await action(); }
    catch (err) {
      if (err.code === 'reauth_required') setQueued(() => action);
      else setError(Object.values(err.fields || {}).join(' ') || err.message);
    } finally { setPending(false); }
  }
  return { run, pending, error, clearError: () => setError(''), confirmation: queued && <ConfirmIdentity onClose={() => setQueued(null)} onDone={async () => { const action = queued; setQueued(null); await run(action); }} /> };
}

function ConfirmIdentity({ onClose, onDone }) {
  const security = useQuery(api.accountSecurity, []);
  const keys = useQuery(api.accountPasskeys, []);
  const providers = useQuery(api.availableIntegrations, []);
  const connectedProviders = providers.data?.providers.filter(p=>p.signIn && p.identity && !p.providerChanged) ?? [];
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  async function confirm(action) {
    setPending(true); setError('');
    try { await action(); await onDone(); }
    catch (err) { setError(Object.values(err.fields || {}).join(' ') || err.message); setPending(false); }
  }
  const popup = useRef(null);
  const poll = useRef(null);
  useEffect(()=>()=>{clearInterval(poll.current);popup.current?.close();},[]);
  function externalConfirmation(start){
    const opened=window.open('about:blank','_blank','popup,width=680,height=760');
    if(!opened) return Promise.reject(new Error('Allow popups for Ignition to confirm your identity without leaving this form.'));
    opened.opener=null;popup.current=opened;
    return new Promise(async(resolve,reject)=>{
      try{const out=await start();opened.location.replace(out.url);const started=Date.now();
        poll.current=setInterval(async()=>{if(opened.closed||Date.now()-started>10*60*1000){clearInterval(poll.current);popup.current=null;try{const account=await api.accountProfile();if(!account.recentlyAuthenticated)throw new Error('Your identity was not confirmed. Try again.');resolve();}catch(err){reject(err);}}},750);
      }catch(err){opened.close();popup.current=null;reject(err);}
    });
  }
  const data = security.data;
  return <Dialog open title="Confirm your identity" description="This change affects your account. Confirmation lasts five minutes on this session." onClose={() => { if (!pending) onClose(); }}
    footer={<><Button disabled={pending} onClick={onClose}>Cancel</Button>{data?.hasPassword && <Button variant="primary" type="submit" form="account-confirm" disabled={pending || !password || (data.mfaEnabled && !code)}>{pending ? 'Checking…' : 'Confirm'}</Button>}</>}>
    {(error || security.error) && <Notice tone="danger" title="Couldn't confirm">{error || security.error.message}</Notice>}
    {!data && !security.error && <p className="account-muted">Loading…</p>}
    {data?.hasPassword && <form id="account-confirm" className="account-form" onSubmit={e => { e.preventDefault(); confirm(() => api.reauthenticate({password, code})); }}>
      <FormField label="Current password" htmlFor="confirm-password"><TextInput id="confirm-password" type="password" autoComplete="current-password" autoFocus value={password} onChange={e => setPassword(e.target.value)} /></FormField>
      {data.mfaEnabled && <FormField label="Authenticator or recovery code" htmlFor="confirm-code"><TextInput id="confirm-code" autoComplete="one-time-code" value={code} onChange={e => setCode(e.target.value)} /></FormField>}
    </form>}
    <div className="account-actions">
      {connectedProviders.map(provider=><Button key={provider.provider} disabled={pending} onClick={()=>confirm(()=>externalConfirmation(()=>api.startProviderAccount(provider.provider,"reauth",location.pathname)))}>Confirm with {provider.label}</Button>)}
      {supportsPasskeys() && keys.data?.passkeys.length > 0 && <Button disabled={pending} onClick={() => confirm(async () => api.finishPasskeyReauth(await passkeyCeremony(await api.beginPasskeyReauth())))}>Confirm with a passkey</Button>}
      {data?.sso && <Button disabled={pending} onClick={() => confirm(() => externalConfirmation(() => api.startAccountSSO('reauth', location.pathname)))}>Confirm with single sign-on</Button>}
    </div>
    {data && !data.hasPassword && !data.sso && !keys.data?.passkeys.length && !connectedProviders.length && <Notice tone="warning">No confirmation method is available. Ask an instance administrator for a password reset link.</Notice>}
  </Dialog>;
}

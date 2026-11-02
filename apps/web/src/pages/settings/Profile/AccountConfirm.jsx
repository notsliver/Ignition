import { useState } from 'react';
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
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  async function confirm(action) {
    setPending(true); setError('');
    try { await action(); await onDone(); }
    catch (err) { setError(Object.values(err.fields || {}).join(' ') || err.message); setPending(false); }
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
      {supportsPasskeys() && keys.data?.passkeys.length > 0 && <Button disabled={pending} onClick={() => confirm(async () => api.finishPasskeyReauth(await passkeyCeremony(await api.beginPasskeyReauth())))}>Confirm with a passkey</Button>}
      {data?.sso && <Button disabled={pending} onClick={() => confirm(async () => { const out = await api.startAccountSSO('reauth', location.pathname); location.assign(out.url); })}>Confirm with single sign-on</Button>}
    </div>
    {data && !data.hasPassword && !data.sso && !keys.data?.passkeys.length && <Notice tone="warning">No confirmation method is available. Ask an instance administrator for a password reset link.</Notice>}
  </Dialog>;
}

import { useState } from 'react';
import { ComputerDesktopIcon, DevicePhoneMobileIcon, KeyIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, Notice, Status, TextInput } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { useSession } from '../../../app/AuthGate';
import { EnrollTwoFactor, RecoveryCodes } from '../../../app/TwoFactor';
import { passkeyCeremony, supportsPasskeys } from '../../../app/passkeys';
import { SettingsField, SettingsGroup } from '../SettingsField';
import { useAccountAction } from './AccountConfirm';
import '../settings.css';
import './Profile.css';

export function AccountSecurity() {
  const session = useSession();
  const user = session.platformUser ?? session.user;
  const security = useQuery(api.accountSecurity, []);
  const sessions = useQuery(api.accountSessions, [], {pollMs: 30000});
  const passkeys = useQuery(api.accountPasskeys, []);
  const action = useAccountAction();
  const [message, setMessage] = useState(() => new URLSearchParams(location.search).has('account_connected') ? 'Identity confirmed. You can continue your change.' : '');
  const [connectionError] = useState(() => new URLSearchParams(location.search).get('account_error'));
  const [mode, setMode] = useState(null);
  const [setup, setSetup] = useState(null);
  const [codes, setCodes] = useState(null);
  const [name, setName] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [repeatPassword, setRepeatPassword] = useState('');
  const [removeKey, setRemoveKey] = useState(null);
  const [revoke, setRevoke] = useState(null);
  const data = security.data;
  const finish = async (text, nextCodes) => { setCodes(nextCodes ?? null); setMode(nextCodes ? 'codes' : null); setMessage(text); await security.refetch(); await session.refresh(); };

  return <div className="settings-page account-page">
    <p className="account-intro">Manage sign-in methods and sessions for your account across all teams.</p>
    {message && <Notice tone="success" title={message} />}
    {(action.error || connectionError) && <Notice tone="danger" title="Couldn't complete the change">{action.error || connectionError}</Notice>}
    {security.error && <Notice tone="danger" title="Couldn't load account security">{security.error.message} <Button onClick={security.refetch}>Retry</Button></Notice>}
    <SettingsGroup title="Two-factor authentication">
      <div className="account-block">
        {!data ? <p className="account-muted">Loading…</p> : <>
          <div className="account-actions"><strong>Authenticator app</strong><Status tone={data.mfaEnabled ? 'success' : data.mfaEnforced ? 'warning' : 'neutral'}>{data.mfaEnabled ? 'On' : data.mfaEnforced ? 'Required' : 'Off'}</Status></div>
          <p className="account-muted">{data.mfaEnabled ? `${data.recoveryCodesLeft} recovery codes remaining. A code is required after password or passkey sign-in.` : 'Add a time-based code from your authenticator app to protect sign-in.'}</p>
          {mode === 'enroll' ? <><EnrollTwoFactor start={() => Promise.resolve(setup)} confirm={async code => finish('Two-factor authentication is on.', (await api.accountMfaEnable(code)).recoveryCodes)} /><Button onClick={() => setMode(null)}>Cancel setup</Button></>
            : mode === 'codes' ? <><RecoveryCodes codes={codes} account={user.email} /><Button onClick={() => {setCodes(null); setMode(null);}}>I've saved these codes</Button></>
              : ['disable', 'regenerate'].includes(mode) ? <CodeForm danger={mode === 'disable'} label={mode === 'disable' ? 'Turn off two-factor' : 'Generate recovery codes'} onCancel={() => setMode(null)}
                onConfirm={async code => mode === 'disable' ? (await api.accountMfaDisable(code), finish('Two-factor authentication is off.')) : finish('New recovery codes created.', (await api.accountRecoveryCodes(code)).recoveryCodes)} />
                : data.mfaEnabled ? <div className="account-actions"><Button onClick={() => setMode('regenerate')}>New recovery codes</Button>{!data.mfaEnforced && <Button className="danger" onClick={() => setMode('disable')}>Turn off</Button>}</div>
                  : <Button variant="primary" disabled={action.pending} onClick={() => action.run(async () => { setSetup(await api.accountMfaSetup()); setMode('enroll'); })}>Set up authenticator</Button>}
          {data.mfaEnforced && <p className="account-muted">Required by your instance’s security policy.</p>}
        </>}
      </div>
    </SettingsGroup>

    <SettingsGroup title="Password">
      <SettingsField label={data?.hasPassword ? 'Account password' : 'Set a password'} hint="Changing your password signs out all other sessions.">
        <Button onClick={() => {setNewPassword(''); setRepeatPassword(''); setMode('password');}}>{data?.hasPassword ? 'Change password' : 'Set password'}</Button>
      </SettingsField>
    </SettingsGroup>

    <section className="settings-group">
      <div className="account-section-head"><h2>Passkeys</h2><Button icon={KeyIcon} disabled={!supportsPasskeys() || !passkeys.data?.available || action.pending} onClick={() => {setName(''); setMode('passkey');}}>Add passkey</Button></div>
      <p>Sign in using your device’s fingerprint, face recognition, or a security key. Your two-factor policy still applies.</p>
      {passkeys.error && <Notice tone="danger" title="Couldn't load passkeys">{passkeys.error.message} <Button onClick={passkeys.refetch}>Retry</Button></Notice>}
      {!supportsPasskeys() && <Notice tone="info">Passkeys require a supported browser with HTTPS or localhost.</Notice>}
      {passkeys.data?.reason && <Notice tone="info">{passkeys.data.reason}</Notice>}
      <div className="settings-card">
        {passkeys.data?.passkeys.map(key => <div key={key.id} className="account-session"><KeyIcon /><span className="account-session-main"><strong>{key.name}</strong><small>Added {timeAgo(key.createdAt)} · {key.lastUsedAt ? `Last used ${timeAgo(key.lastUsedAt)}` : 'Not used yet'}</small></span><Button className="danger" onClick={() => setRemoveKey(key)}>Remove</Button></div>)}
        {!passkeys.data?.passkeys.length && <p className="account-block account-muted">{passkeys.data ? 'No passkeys registered yet.' : 'Loading…'}</p>}
      </div>
    </section>

    <section className="settings-group">
      <div className="account-section-head"><h2>Sessions</h2><Button disabled={sessions.data?.sessions.filter(s => !s.current).length === 0 || !sessions.data} onClick={() => setRevoke({id: 'others'})}>Sign out other sessions</Button></div>
      <p>Only your sessions are shown. Revoking a session ends its access immediately.</p>
      {sessions.error && <Notice tone="danger" title="Couldn't load sessions">{sessions.error.message} <Button onClick={sessions.refetch}>Retry</Button></Notice>}
      <div className="settings-card">
        {sessions.data?.sessions.map(s => {
          const mobile = /Android|iPhone|iPad|Mobile/i.test(s.userAgent);
          const Icon = mobile ? DevicePhoneMobileIcon : ComputerDesktopIcon;
          return <div key={s.id} className="account-session"><Icon /><span className="account-session-main"><span className="account-session-title"><strong>{sessionLabel(s.userAgent)}</strong>{s.current && <Status tone="success">This session</Status>}</span><small>{s.ip || 'Unknown address'} · {s.method} · Last active {timeAgo(s.lastSeenAt)}</small><small>Signed in {timeAgo(s.createdAt)} · Expires {new Date(s.expiresAt).toLocaleString()}</small></span><Button onClick={() => setRevoke(s)}>Sign out</Button></div>;
        })}
        {!sessions.data && <p className="account-block account-muted">Loading…</p>}
      </div>
    </section>

    <Dialog open={mode === 'passkey'} onClose={() => setMode(null)} title="Add a passkey" description="Give it a name you will recognize. Your browser will ask you to verify using your device."
      footer={<><Button onClick={() => setMode(null)}>Cancel</Button><Button type="submit" form="add-passkey" variant="primary" disabled={!name.trim() || action.pending}>Continue</Button></>}>
      {action.error && <Notice tone="danger" title="Couldn't add passkey">{action.error}</Notice>}
      <form id="add-passkey" onSubmit={e => {e.preventDefault(); action.run(async () => {await api.finishPasskeyRegistration(await passkeyCeremony(await api.beginPasskeyRegistration(name.trim()), true)); setMode(null); setMessage('Passkey added.'); await passkeys.refetch();});}}><FormField label="Passkey name" htmlFor="passkey-name"><TextInput id="passkey-name" maxLength={80} value={name} onChange={e => setName(e.target.value)} placeholder="My laptop" autoFocus /></FormField></form>
    </Dialog>
    <Dialog open={mode === 'password'} onClose={() => setMode(null)} title={data?.hasPassword ? 'Change password' : 'Set password'} description="Use a long, unique password. Other sessions will be signed out."
      footer={<><Button onClick={() => setMode(null)}>Cancel</Button><Button type="submit" form="change-password" variant="primary" disabled={action.pending || !newPassword || newPassword !== repeatPassword}>Save password</Button></>}>
      {action.error && <Notice tone="danger" title="Couldn't save password">{action.error}</Notice>}
      <form id="change-password" className="account-form" onSubmit={e => { e.preventDefault(); action.run(async () => {await api.changeAccountPassword(newPassword); setMode(null); setNewPassword(''); setRepeatPassword(''); await session.refresh(); await sessions.refetch(); await security.refetch(); setMessage('Password saved. Other sessions have been signed out.');});}}>
        <FormField label="New password" htmlFor="new-account-password"><TextInput id="new-account-password" type="password" autoComplete="new-password" value={newPassword} onChange={e => setNewPassword(e.target.value)} /></FormField>
        <FormField label="Repeat password" htmlFor="repeat-account-password" error={repeatPassword && newPassword !== repeatPassword ? 'Passwords do not match.' : ''}><TextInput id="repeat-account-password" type="password" autoComplete="new-password" value={repeatPassword} onChange={e => setRepeatPassword(e.target.value)} /></FormField>
      </form>
    </Dialog>
    <Dialog open={!!removeKey} onClose={() => setRemoveKey(null)} title={`Remove ${removeKey?.name ?? 'passkey'}?`} description="This credential will no longer sign in to your account."
      footer={<><Button onClick={() => setRemoveKey(null)}>Cancel</Button><Button disabled={action.pending} variant="danger-solid" onClick={() => action.run(async () => {await api.removeAccountPasskey(removeKey.id); setRemoveKey(null); await passkeys.refetch(); setMessage('Passkey removed.');})}>Remove passkey</Button></>}>
      {action.error && <Notice tone="danger" title="Couldn't remove passkey">{action.error}</Notice>}
    </Dialog>
    <Dialog open={!!revoke} onClose={() => setRevoke(null)} title={revoke?.id === 'others' ? 'Sign out your other sessions?' : revoke?.current ? 'Sign out this session?' : 'Sign out this device?'} description="The affected devices will need to sign in again."
      footer={<><Button onClick={() => setRevoke(null)}>Cancel</Button><Button variant="primary" disabled={action.pending} onClick={() => action.run(async () => {await api.revokeAccountSession(revoke.id); if (revoke.current) await session.signOut(); else {setRevoke(null); await sessions.refetch(); setMessage('Sessions signed out.');}})}>Sign out</Button></>} />
    {action.confirmation}
  </div>;
}

function sessionLabel(agent) {
  const browser = /Edg\//.test(agent) ? 'Edge' : /Firefox\//.test(agent) ? 'Firefox' : /Chrome\//.test(agent) ? 'Chrome' : /Safari\//.test(agent) ? 'Safari' : 'Browser';
  const os = /Android/.test(agent) ? 'Android' : /iPhone|iPad/.test(agent) ? 'iOS' : /Windows/.test(agent) ? 'Windows' : /Macintosh/.test(agent) ? 'macOS' : /Linux/.test(agent) ? 'Linux' : '';
  return agent ? `${browser}${os ? ` on ${os}` : ''}` : 'Unknown device';
}
function CodeForm({label, danger, onConfirm, onCancel}) {
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  return <form className="account-form" onSubmit={async e => { e.preventDefault(); setPending(true); setError(''); try {await onConfirm(code);} catch(err){setError(err.fields?.code || err.message);setPending(false);} }}>
    <FormField label="Authenticator or recovery code" htmlFor="security-code" error={error}><TextInput id="security-code" autoFocus autoComplete="one-time-code" value={code} onChange={e => setCode(e.target.value)} /></FormField>
    <div className="account-actions"><Button type="button" disabled={pending} onClick={onCancel}>Cancel</Button><Button type="submit" variant={danger ? 'danger-solid' : 'primary'} disabled={!code.trim() || pending}>{pending ? 'Checking…' : label}</Button></div>
  </form>;
}

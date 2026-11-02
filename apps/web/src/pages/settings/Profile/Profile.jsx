import { useState } from 'react';
import { CheckCircleIcon, LinkIcon, ShieldCheckIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, Notice, Status, TextInput } from '../../../ui';
import { ImageUpload } from '../../../ui/components/ImageUpload/ImageUpload';
import { api } from '../../../api/client';
import { useQuery } from '../../../api/hooks';
import { useSession } from '../../../app/AuthGate';
import { useUnsavedChanges } from '../../../app/unsaved';
import { SettingsField, SettingsGroup } from '../SettingsField';
import { useAccountAction } from './AccountConfirm';
import '../settings.css';
import './Profile.css';

export function Profile() {
  const session = useSession();
  const user = session.platformUser ?? session.user;
  const [name, setName] = useState(user.name);
  const [email, setEmail] = useState(user.email);
  const [message, setMessage] = useState(() => {
    const query = new URLSearchParams(location.search);
    return query.has('account_connected') ? 'Identity confirmed. Your account connection is up to date.' : '';
  });
  const [connectionError] = useState(() => new URLSearchParams(location.search).get('account_error'));
  const integrations = useQuery(api.accountIntegrations, []);
  const action = useAccountAction();
  const [deleting, setDeleting] = useState(false);
  const [disconnecting, setDisconnecting] = useState(false);
  const [confirmation, setConfirmation] = useState('');
  const dirty = name.trim() !== user.name || email.trim().toLowerCase() !== user.email.toLowerCase();
  useUnsavedChanges(dirty);
  const sso = integrations.data?.sso;

  return <div className="settings-page account-page">
    <p className="account-intro">Your account details apply across all your teams.</p>
    {message && <Notice tone="success" title={message} />}
    {(action.error || connectionError) && <Notice tone="danger" title="Couldn't complete the change">{action.error || connectionError}</Notice>}
    <form onSubmit={e => { e.preventDefault(); setMessage(''); action.run(async () => { await api.updateAccountProfile({name: name.trim(), email: email.trim()}); await session.refresh(); setMessage('Account information saved.'); }); }}>
      <SettingsGroup title="Account Information">
        <SettingsField label="Name" htmlFor="profile-name"><TextInput id="profile-name" value={name} maxLength={80} autoComplete="name" onChange={e => setName(e.target.value)} /></SettingsField>
        <SettingsField label="Email" htmlFor="profile-email" hint="Changing this signs out your other sessions."><TextInput id="profile-email" type="email" value={email} maxLength={254} autoComplete="email" onChange={e => setEmail(e.target.value)} /></SettingsField>
        <div className="account-avatar"><ImageUpload label="Avatar" name={user.name || user.email} src={user.avatarUrl} onUpload={api.uploadAvatar} onRemove={api.removeAvatar} onChanged={session.refresh} /></div>
        <div className="account-save"><Button type="submit" variant="primary" disabled={!dirty || !name.trim() || !email.trim() || action.pending}>{action.pending ? 'Saving…' : 'Save changes'}</Button></div>
      </SettingsGroup>
    </form>

    <section className="settings-group">
      <h2>Account Integrations</h2>
      <p>Connect the single sign-on provider configured for this instance.</p>
      {integrations.error && <Notice tone="danger" title="Couldn't load integrations">{integrations.error.message} <Button onClick={integrations.refetch}>Retry</Button></Notice>}
      {!sso && !integrations.error && <p className="account-muted">Loading…</p>}
      {sso && <article className="account-integration settings-card">
        <header><span className="account-integration-title"><ShieldCheckIcon /><strong>{sso.label}</strong></span>
          <Status tone={sso.connected ? 'success' : 'neutral'}>{sso.connected ? 'Connected' : sso.configured ? 'Not connected' : 'Not configured'}</Status></header>
        {sso.connected ? <><p className="account-connected"><CheckCircleIcon />Connected to {sso.account}</p><p>{sso.configured ? 'Use this identity to sign in to Ignition.' : 'Your identity is linked. Single sign-on is currently disabled on this instance.'}</p></>
          : <p>{sso.providerChanged ? 'The instance SSO provider changed. Disconnect the previous identity before connecting again.' : sso.configured ? 'Link your identity to sign in using your organization’s single sign-on.' : 'An instance administrator can configure single sign-on under Authentication & Security.'}</p>}
        {sso.issuer && <span className="account-provider">{sso.issuer}</span>}
        <div className="account-integration-footer">
          {sso.connected || sso.providerChanged ? <Button disabled={action.pending} onClick={() => setDisconnecting(true)}>Disconnect</Button>
            : <Button icon={LinkIcon} disabled={!sso.configured || action.pending} onClick={() => action.run(async () => { const out = await api.startAccountSSO('connect', location.pathname); location.assign(out.url); })}>Connect</Button>}
        </div>
      </article>}
    </section>

    <SettingsGroup title="Danger zone">
      <SettingsField label="Delete account" hint="Permanently delete your account and end all sessions. Transfer sole ownership of your teams first.">
        <Button icon={TrashIcon} className="danger" onClick={() => setDeleting(true)}>Delete account</Button>
      </SettingsField>
    </SettingsGroup>
    <Dialog open={disconnecting} onClose={() => setDisconnecting(false)} title="Disconnect single sign-on?" description="Keep a password or passkey available to sign in. Your instance may require SSO."
      footer={<><Button onClick={() => setDisconnecting(false)}>Cancel</Button><Button disabled={action.pending} onClick={() => action.run(async () => { await api.disconnectAccountSSO(); setDisconnecting(false); await integrations.refetch(); await session.refresh(); setMessage('Single sign-on disconnected.'); })}>Disconnect</Button></>}>
      {action.error && <Notice tone="danger" title="Couldn't disconnect">{action.error}</Notice>}
    </Dialog>
    <Dialog open={deleting} onClose={() => setDeleting(false)} title="Delete your account?" description="This permanently removes your identity, avatar, passkeys and sessions. Shared team workloads stay with their team. This cannot be undone."
      footer={<><Button onClick={() => setDeleting(false)}>Cancel</Button><Button variant="danger-solid" disabled={confirmation.toLowerCase() !== user.email.toLowerCase() || action.pending} onClick={() => action.run(async () => { await api.deleteAccount(confirmation); await session.signOut(); })}>Delete account permanently</Button></>}>
      {action.error && <Notice tone="danger" title="Couldn't delete account">{action.error}</Notice>}
      <FormField label={`Type ${user.email} to confirm`} htmlFor="delete-account-email"><TextInput id="delete-account-email" value={confirmation} onChange={e => setConfirmation(e.target.value)} autoComplete="off" /></FormField>
    </Dialog>
    {action.confirmation}
  </div>;
}

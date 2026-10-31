import { useEffect, useState } from 'react';
import { ArrowRightIcon, BookOpenIcon, KeyIcon, LinkIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, Notice, Select, Status, Switch, TextInput } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { useSession } from '../../../app/AuthGate';
import { SaveBar, SettingsField, SettingsGroup } from '../SettingsField';
import { describeEvent } from './data';
import { MfaNoticeCard } from '../../../app/MfaBanner';
import './Security.css';

/*
 * Authentication & Security. Every control here is enforced by the API: sign-in methods and
 * lockout, two-factor enforcement, password rules, session lifetime and idle timeout, invitations,
 * trusted proxies, allowed origins, the IP allow list and log redaction. Status rows (HTTPS,
 * encryption, SSO) are read from the running instance, never assumed. The API refuses saves that
 * would lock you out, and says why.
 */

const options = list => list.map(([value, label]) => ({ value, label }));
const requiredMethods = [
  { value: 'any', label: 'Any enabled method', description: 'Users choose how to sign in' },
  { value: 'sso', label: 'SSO only', description: 'Owners keep password sign-in as a way back in' },
];
const passwordLengths = options([[8, '8 characters'], [12, '12 characters'], [16, '16 characters']]);
const rateLimits = options([[5, '5 per minute'], [10, '10 per minute'], [30, '30 per minute']]);
const attempts = options([[3, '3 attempts'], [5, '5 attempts'], [10, '10 attempts']]);
const lockouts = options([[5, '5 minutes'], [15, '15 minutes'], [60, '1 hour']]);
const resetExpiries = options([[15, '15 minutes'], [30, '30 minutes'], [60, '1 hour'], [1440, '24 hours']]);
const invitationPolicies = [
  { value: 'admins', label: 'Admins only', description: 'People who can manage users' },
  { value: 'members', label: 'Any member', description: 'Anyone, for their own role or below' },
  { value: 'disabled', label: 'Disabled', description: 'No new accounts; pending links stop working' },
];
const mfaActions = [
  { value: 'enroll', label: 'Require setup at sign-in', description: 'Sessions end; setup comes first next time' },
  { value: 'suspend', label: 'Suspend the account', description: 'An admin has to reactivate it' },
];
const ssoRoles = options([['viewer', 'Viewer'], ['developer', 'Developer']]);
const lifetimes = options([['24h', '24 hours'], ['7d', '7 days'], ['30d', '30 days']]);
const idleTimeouts = options([['1h', '1 hour'], ['12h', '12 hours'], ['24h', '24 hours'], ['never', 'Never']]);

// The form edits lists as comma-separated text; the API stores arrays.
const lists = ['trustedProxies', 'allowedOrigins', 'allowedIps'];
const toForm = p => ({ ...p, ...Object.fromEntries(lists.map(k => [k, (p[k] ?? []).join(', ')])) });
const fromForm = v => ({ ...v, ...Object.fromEntries(lists.map(k => [k, v[k].split(',').map(x => x.trim()).filter(Boolean)])) });

export function Security() {
  const { user } = useSession();
  const { data, error, refetch } = useQuery(() => api.security(), [], { pollMs: 60000 });
  const [saved, setSaved] = useState(null);
  const [values, setValues] = useState(null);
  const [errors, setErrors] = useState({});
  const [feedback, setFeedback] = useState(null);
  const [confirmRevoke, setConfirmRevoke] = useState(false);
  const [oidcDialog, setOidcDialog] = useState(false);
  const [guide, setGuide] = useState(null);
  useEffect(() => {
    if (!data) return;
    const next = toForm(data.policy);
    // Don't throw away unsaved edits when the page refreshes in the background.
    setSaved(prev => { if (!prev || JSON.stringify(values) === JSON.stringify(prev)) setValues(next); return next; });
  }, [data]); // eslint-disable-line react-hooks/exhaustive-deps

  if (error && !data) return <div className="settings-page"><Notice tone="danger" title="Couldn't load security settings">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice></div>;
  if (!values) return <div className="settings-page"><p className="security-loading">Loading…</p></div>;

  const status = data.status;
  const oidc = status.oidc;
  const dirty = JSON.stringify(values) !== JSON.stringify(saved);
  const set = key => value => { setValues(v => ({ ...v, [key]: value })); setErrors(e => ({ ...e, [key]: undefined })); };
  const bind = key => ({ id: key, value: values[key], onChange: set(key) });
  const bindInput = key => ({ id: key, value: values[key], onChange: event => set(key)(event.target.value) });
  const err = key => errors[key] && <div className="field-notice"><Notice tone="danger" title={errors[key]} /></div>;
  const noMethods = !values.localAuth && !values.oidcAuth;

  async function save(event) {
    event.preventDefault();
    if (noMethods) return;
    setFeedback(null);
    try {
      const out = await api.saveSecurity(fromForm(values));
      const next = toForm(out.policy);
      setSaved(next);
      setValues(next);
      setErrors({});
      setFeedback({ tone: 'success', title: 'Security settings saved' });
      refetch();
    } catch (e) {
      setErrors(e.fields ?? {});
      setFeedback({ tone: 'danger', title: 'Couldn\'t save', text: Object.keys(e.fields ?? {}).length ? 'Fix the highlighted settings.' : e.message });
    }
  }
  async function revokeAll() {
    setConfirmRevoke(false);
    try { await api.revokeAllSessions(); location.assign('/'); } catch (e) { setFeedback({ tone: 'danger', title: 'Couldn\'t sign everyone out', text: e.message }); }
  }
  const httpsHint = !status.https.url ? 'No dashboard address is set in Settings › General.'
    : !status.https.https ? `The dashboard is at ${status.https.url}, over plain HTTP. Put it behind HTTPS (a domain in Settings › General) so passwords and sessions are encrypted in transit.`
      : status.https.valid ? `Certificate by ${status.https.issuer}, expires ${new Date(status.https.expires).toLocaleDateString()}.`
        : status.https.expires ? `Certificate by ${status.https.issuer || 'an unknown issuer'} isn't trusted: ${status.https.error}`
          : status.https.error;
  const users = status.users;

  return <form className="settings-page security" onSubmit={save}>
    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}</Notice>}

    <SettingsGroup title="Authentication providers" description="Which sign-in methods are available on this instance.">
      <SettingsField label="Local authentication" hint="Email and password accounts stored in Ignition. When off, only owners can still use a password, as a way back in.">
        <Switch label="Local authentication" checked={values.localAuth} onChange={set('localAuth')} />
      </SettingsField>
      {err('localAuth')}
      <SettingsField label="Disable password registration" hint="Invitations can't be accepted with a password; invited people join through SSO instead. There's no public sign-up either way.">
        <Switch label="Disable password registration" checked={values.disableRegistration} onChange={set('disableRegistration')} disabled={!values.localAuth} />
      </SettingsField>
      <SettingsField label="OpenID Connect (SSO)" hint={oidc.configured ? `${oidc.issuer} · client ${oidc.clientId}${oidc.allowedDomains?.length ? ` · ${oidc.allowedDomains.join(', ')}` : ''}` : 'Google, Okta, Authentik, Keycloak, Entra ID or any OIDC provider.'}>
        {oidc.configured
          ? <><Switch label="OpenID Connect" checked={values.oidcAuth} onChange={set('oidcAuth')} /><Button type="button" onClick={() => setOidcDialog(true)}>Edit</Button></>
          : <><span className="readonly-value">Not connected</span><Button type="button" onClick={() => setOidcDialog(true)}>Connect</Button></>}
      </SettingsField>
      {err('oidcAuth')}
      {noMethods && <div className="field-notice"><Notice tone="danger" title="At least one method must stay enabled">Disabling every method would lock everyone out, including admins.</Notice></div>}
      <SettingsField label="Required sign-in method" hint="Instance-wide requirement for every user." htmlFor="requiredMethod">
        <Select options={requiredMethods} {...bind('requiredMethod')} />
      </SettingsField>
      {values.requiredMethod === 'sso' && !oidc.configured && <div className="field-notice"><Notice tone="warning" title="SSO isn't connected yet">Connect and verify an identity provider before requiring it, or nobody will be able to sign in.</Notice></div>}
      {err('requiredMethod')}
    </SettingsGroup>

    <SettingsGroup title="Authentication policies" description="Rules applied to every sign-in.">
      <SettingsField label="Enforce MFA" hint={`Every password sign-in needs a code from an authenticator app; people without one set it up at their next sign-in. ${users.withMfa} of ${users.active} active users have it on${users.admins ? `, including ${users.adminsWithMfa} of ${users.admins} admins and owners` : ''}. SSO sign-ins rely on the provider's own MFA.`}>
        <Switch label="Enforce MFA" checked={values.enforceMfa} onChange={set('enforceMfa')} />
      </SettingsField>
      {values.enforceMfa && !saved.enforceMfa && <div className="field-notice"><Notice tone="warning" title={status.you.mfa ? 'Check admin access first' : 'You don\'t have two-factor authentication yet'}>
        {status.you.mfa
          ? `Users without MFA ${values.mfaDeadline ? `see a reminder until ${values.mfaDeadline}, then ${values.mfaDeadlineAction === 'suspend' ? 'their accounts are suspended' : 'must enroll at their next sign-in'}` : 'will have to enroll at their next sign-in'}. Make sure admins have saved their recovery codes.`
          : 'You don\'t have it yet either. Set it up from the account menu (top left) › Two-factor authentication.'}
      </Notice></div>}
      {values.enforceMfa && <>
        <SettingsField label="Deadline" hint="Until the end of this day (UTC), people without two-factor get a friendly reminder at the top of every page and can keep working. Leave empty to require it at the next sign-in." htmlFor="mfaDeadline">
          <TextInput type="date" min={new Date().toISOString().slice(0, 10)} {...bindInput('mfaDeadline')} />
          {values.mfaDeadline && <Button type="button" onClick={() => set('mfaDeadline')('')}>Clear</Button>}
        </SettingsField>
        {err('mfaDeadline')}
        <SettingsField label="After the deadline" hint={values.mfaDeadlineAction === 'suspend' ? 'Suspended people can\'t sign in until an admin reactivates them on Users & Permissions. Owners and SSO-only accounts are never suspended.' : 'Their current sessions end, and the next sign-in walks them through setup.'} htmlFor="mfaDeadlineAction">
          <Select options={mfaActions} {...bind('mfaDeadlineAction')} />
        </SettingsField>
        <SettingsField label="Note in the reminder" hint="Optional. Why you're asking, or where to get help." htmlFor="mfaMessage">
          <TextInput maxLength={300} placeholder="Part of our security review. Ping #it-help if you get stuck!" {...bindInput('mfaMessage')} />
        </SettingsField>
        {err('mfaMessage')}
        <div className="field-notice mfa-preview">
          <span>Preview of what people without two-factor see</span>
          <MfaNoticeCard preview name={user.name} notice={{
            grace: !!values.mfaDeadline && new Date(`${values.mfaDeadline}T23:59:59Z`) > new Date(),
            deadlineAt: values.mfaDeadline ? `${values.mfaDeadline}T23:59:59Z` : null, action: values.mfaDeadlineAction, message: values.mfaMessage.trim(),
          }} />
        </div>
      </>}
      <SettingsField label="Minimum password length" hint="Local accounts. Applies to new passwords: setup, invitations and resets." htmlFor="passwordLength">
        <Select options={passwordLengths} {...bind('passwordLength')} />
      </SettingsField>
      <SettingsField label="Block known breached passwords" hint="Checked with Have I Been Pwned: only the first 5 characters of the password's SHA-1 hash leave this server. If the service can't be reached, the password is allowed.">
        <Switch label="Block known breached passwords" checked={values.breachedPasswords} onChange={set('breachedPasswords')} />
      </SettingsField>
      <SettingsField label="Login rate limit" hint="Sign-in and code attempts allowed per IP address." htmlFor="rateLimit">
        <Select options={rateLimits} {...bind('rateLimit')} />
      </SettingsField>
      <SettingsField label="Account lockout" hint="Failed attempts before an account is temporarily locked." htmlFor="maxAttempts">
        <Select options={attempts} {...bind('maxAttempts')} />
      </SettingsField>
      <SettingsField label="Lockout duration" htmlFor="lockoutMinutes">
        <Select options={lockouts} {...bind('lockoutMinutes')} />
      </SettingsField>
      <SettingsField label="Password reset link expiry" hint="Reset links work once and expire after this." htmlFor="resetExpiryMinutes">
        <Select options={resetExpiries} {...bind('resetExpiryMinutes')} />
      </SettingsField>
      <SettingsField label="Invitations" hint="Who can invite new users." htmlFor="invitations">
        <Select options={invitationPolicies} {...bind('invitations')} />
      </SettingsField>
      <SettingsField label="Provision accounts from SSO" hint={oidc.configured ? 'Create an account on first SSO sign-in, with the role below. Without it, only invited or existing people can sign in. Pending invitations are honoured either way.' : 'Connect an identity provider first.'}>
        <Switch label="Provision accounts from SSO" checked={values.ssoProvisioning} onChange={set('ssoProvisioning')} disabled={!oidc.configured} />
      </SettingsField>
      {values.ssoProvisioning && <SettingsField label="Role for new SSO accounts" htmlFor="ssoDefaultRole">
        <Select options={ssoRoles} {...bind('ssoDefaultRole')} />
      </SettingsField>}
    </SettingsGroup>

    <SettingsGroup title="Session policies">
      <SettingsField label="Maximum session lifetime" hint="Users must sign in again after this. Shortening it also shortens existing sessions." htmlFor="sessionLifetime">
        <Select options={lifetimes} {...bind('sessionLifetime')} />
      </SettingsField>
      <SettingsField label="Idle timeout" hint="Sign out after a period of inactivity." htmlFor="idleTimeout">
        <Select options={idleTimeouts} {...bind('idleTimeout')} />
      </SettingsField>
      <SettingsField label="Invalidate on password change" hint="Revoke a user's other sessions. Always on.">
        <Switch label="Invalidate on password change" checked onChange={() => {}} disabled />
      </SettingsField>
      <SettingsField label="Invalidate on role change" hint="Revoke sessions when someone's role or permissions change.">
        <Switch label="Invalidate on role change" checked={values.revokeOnRoleChange} onChange={set('revokeOnRoleChange')} />
      </SettingsField>
      <SettingsField label="Revoke all sessions" hint="Signs out every user on the instance, including you.">
        {confirmRevoke
          ? <><Button type="button" onClick={() => setConfirmRevoke(false)}>Cancel</Button><Button type="button" className="danger-solid" onClick={revokeAll}>Sign out everyone</Button></>
          : <Button type="button" className="danger" onClick={() => setConfirmRevoke(true)}>Revoke all</Button>}
      </SettingsField>
    </SettingsGroup>

    <SettingsGroup title="Network & transport security" description={`Requests from you arrive from ${status.request.remoteAddr}${status.request.forwardedFor ? ` (forwarded for ${status.request.forwardedFor})` : ''}; Ignition sees you as ${status.request.clientIp}.`}>
      <SettingsField label="HTTPS" hint={httpsHint}>
        <Status tone={!status.https.https ? 'warning' : status.https.valid ? 'success' : 'danger'}>{!status.https.https ? 'Not HTTPS' : status.https.valid ? 'Valid certificate' : status.https.expires ? 'Untrusted certificate' : 'Couldn\'t check'}</Status>
      </SettingsField>
      <SettingsField label="Trusted proxies" hint="Only trust X-Forwarded-For from these addresses (IPs or CIDR ranges, comma-separated). Empty: the connecting address is the client." htmlFor="trustedProxies">
        <TextInput className="mono" placeholder="10.0.0.0/8, 172.16.0.0/12" {...bindInput('trustedProxies')} />
      </SettingsField>
      {err('trustedProxies')}
      <SettingsField label="Allowed origins" hint="Extra origins whose pages may make changes, comma-separated. The dashboard's own address is always allowed; others are rejected." htmlFor="allowedOrigins">
        <TextInput placeholder="https://ignition.example.com" {...bindInput('allowedOrigins')} />
      </SettingsField>
      {err('allowedOrigins')}
      <SettingsField label="IP restrictions" hint="Only allow dashboard and API access from specific networks. Deploy and automation webhooks still work.">
        <Switch label="IP restrictions" checked={values.ipRestrictions} onChange={set('ipRestrictions')} />
      </SettingsField>
      {values.ipRestrictions && <SettingsField label="Allowed networks" hint={`CIDR ranges or IPs, comma-separated. Include your own (${status.request.clientIp}).`} htmlFor="allowedIps">
        <TextInput className="mono" placeholder={`${status.request.clientIp}, 203.0.113.0/24`} {...bindInput('allowedIps')} />
      </SettingsField>}
      {err('allowedIps')}
    </SettingsGroup>

    <SettingsGroup title="Secrets & encryption" description="Stored secret values are never displayed without the reveal permission, and every reveal is audited.">
      <SettingsField label="Encryption key" hint={`Loaded from ${status.encryption.keySource} in the install folder's .env.`}>
        <Status tone={status.encryption.configured ? 'success' : 'danger'}>{status.encryption.configured ? 'Configured' : 'Missing'}</Status>
      </SettingsField>
      <SettingsField label="Credentials at rest" hint="Secret variables, server keys, source credentials, the SSO client secret and two-factor secrets.">
        <Status tone="success">Encrypted · {status.encryption.algorithm}</Status>
      </SettingsField>
      <SettingsField label="Redact secrets in logs" hint="Replace secret variable values with ••••• in build, deployment and runtime logs when they're shown or downloaded.">
        <Switch label="Redact secrets in logs" checked={values.logRedaction} onChange={set('logRedaction')} />
      </SettingsField>
      <SettingsField label="Key rotation" hint="The key is set when Ignition is installed. Rotating it in place isn't supported yet.">
        <Button type="button" icon={BookOpenIcon} onClick={() => setGuide('rotation')}>Rotation guide</Button>
      </SettingsField>
      <SettingsField label="Key backup & recovery" hint="Without the key, stored secrets can't be recovered.">
        <Button type="button" icon={BookOpenIcon} onClick={() => setGuide('recovery')}>Recovery guide</Button>
      </SettingsField>
    </SettingsGroup>

    <section className="settings-group">
      <div className="group-head">
        <h2>Security events</h2>
        <button type="button" className="text-link" onClick={() => { history.pushState(null, '', '/settings/audit-log'); dispatchEvent(new PopStateEvent('popstate')); }}>Audit log<ArrowRightIcon /></button>
      </div>
      <div className="settings-card">
        {data.events.length ? <ul className="event-list">
          {data.events.map(e => { const d = describeEvent(e); return <li key={e.id}>
            <Status tone={d.tone} label={d.tone === 'danger' ? 'High severity' : d.tone === 'warning' ? 'Medium severity' : 'Info'} />
            <span className="event-main"><strong>{d.action}</strong><span>{d.detail}</span></span>
            <span className="event-time" title={new Date(e.createdAt).toLocaleString()}>{timeAgo(e.createdAt)}</span>
          </li>; })}
        </ul> : <p className="security-empty">No sign-in or account events yet.</p>}
      </div>
    </section>

    <SaveBar dirty={dirty} onDiscard={() => { setValues(saved); setErrors({}); }} />
    {oidcDialog && <OidcDialog status={oidc} onClose={() => setOidcDialog(false)} onChanged={msg => { setOidcDialog(false); setFeedback({ tone: 'success', title: msg }); refetch(); }} canDisconnect={user.role === 'owner' || !values.oidcAuth || values.localAuth} />}
    {guide && <GuideDialog kind={guide} onClose={() => setGuide(null)} />}
  </form>;
}

function OidcDialog({ status, onClose, onChanged, canDisconnect }) {
  const [v, setV] = useState({ issuer: status.issuer ?? '', clientId: status.clientId ?? '', clientSecret: '', label: status.label ?? '', allowedDomains: (status.allowedDomains ?? []).join(', ') });
  const [errors, setErrors] = useState({});
  const [check, setCheck] = useState(null);
  const [pending, setPending] = useState('');
  const set = k => e => { setV(x => ({ ...x, [k]: e.target.value })); setErrors(x => ({ ...x, [k]: undefined, _: undefined })); setCheck(null); };
  const body = () => ({ ...v, allowedDomains: v.allowedDomains.split(',').map(d => d.trim()).filter(Boolean) });
  async function run(kind, fn) {
    setPending(kind);
    try { await fn(); } catch (e) { setErrors(Object.keys(e.fields ?? {}).length ? e.fields : { _: e.message }); }
    setPending('');
  }
  const redirect = check?.redirectUrl ?? `${location.origin}/api/auth/oidc/callback`;
  return <Dialog open onClose={onClose} icon={KeyIcon} title={status.configured ? 'Single sign-on' : 'Connect single sign-on'} className="oidc-dialog"
    description="Register Ignition as a web application (confidential client) with your identity provider, then paste its details here."
    footer={<>
      {status.configured && canDisconnect && <Button className="danger" onClick={() => run('disconnect', async () => { await api.deleteOidc(); onChanged('Single sign-on disconnected; password sign-in is on'); })} disabled={!!pending}>Disconnect</Button>}
      <span className="spacer" />
      <Button onClick={onClose}>Cancel</Button>
      <Button variant="primary" type="submit" form="oidc-form" disabled={!!pending}>{pending === 'save' ? 'Verifying…' : 'Verify and save'}</Button>
    </>}>
    <form id="oidc-form" className="oidc-form" onSubmit={e => { e.preventDefault(); run('save', async () => { await api.saveOidc(body()); onChanged('Single sign-on connected. Turn it on, then save.'); }); }}>
      <div className="oidc-redirect"><span>Redirect URI to register</span><code><LinkIcon />{redirect}</code></div>
      {errors._ && <Notice tone="danger" title={errors._} />}
      <FormField label="Issuer URL" htmlFor="oidc-issuer" error={errors.issuer} hint="e.g. https://accounts.google.com or https://auth.example.com/application/o/ignition/">
        <TextInput id="oidc-issuer" value={v.issuer} onChange={set('issuer')} placeholder="https://" autoFocus
          suffix={<button type="button" className="oidc-test" disabled={!v.issuer.trim() || !!pending} onClick={() => run('test', async () => setCheck(await api.testOidc(body())))}>{pending === 'test' ? 'Checking…' : 'Check'}</button>} />
      </FormField>
      {check && <p className="oidc-ok">Found {check.issuer}. Sign-in goes to {new URL(check.authorizationEndpoint).host}.</p>}
      <FormField label="Client ID" htmlFor="oidc-client" error={errors.clientId}><TextInput id="oidc-client" className="mono" value={v.clientId} onChange={set('clientId')} /></FormField>
      <FormField label="Client secret" htmlFor="oidc-secret" error={errors.clientSecret} hint={status.hasSecret ? 'Stored encrypted. Leave empty to keep it.' : 'Stored encrypted; never shown again.'}>
        <TextInput id="oidc-secret" className="mono" type="password" autoComplete="off" value={v.clientSecret} onChange={set('clientSecret')} placeholder={status.hasSecret ? '••••••••' : ''} />
      </FormField>
      <div className="db-row oidc-row">
        <FormField label="Button label" htmlFor="oidc-label" error={errors.label} hint="Shown as “Sign in with …”"><TextInput id="oidc-label" value={v.label} onChange={set('label')} placeholder="Google" maxLength={40} /></FormField>
        <FormField label="Allowed email domains" htmlFor="oidc-domains" error={errors.allowedDomains} hint="Comma-separated. Empty allows any."><TextInput id="oidc-domains" value={v.allowedDomains} onChange={set('allowedDomains')} placeholder="example.com" /></FormField>
      </div>
      <p className="oidc-note">Accounts are matched by the provider's user ID, then by verified email. Scopes: openid, email, profile. PKCE is used.</p>
    </form>
  </Dialog>;
}

function GuideDialog({ kind, onClose }) {
  const rotation = kind === 'rotation';
  return <Dialog open onClose={onClose} icon={BookOpenIcon} title={rotation ? 'Rotating the encryption key' : 'Backing up the encryption key'} className="guide-dialog"
    footer={<Button variant="primary" onClick={onClose}>Done</Button>}>
    {rotation ? <div className="guide">
      <p><code>IGNITION_SECRET_KEY</code> encrypts secret variables, server client keys, source credentials, the SSO client secret and two-factor secrets (AES-256-GCM, each bound to what it protects).</p>
      <Notice tone="warning" title="Ignition can't re-encrypt with a new key yet">Changing the key in <code>.env</code> makes every stored secret unreadable. Deployments that need them fail, and two-factor sign-in stops working.</Notice>
      <p>If the key may have leaked:</p>
      <ol>
        <li>Rotate the secrets themselves first: database passwords, API keys in variables, Git tokens in Sources, and server certificates (Servers › Credentials).</li>
        <li>Only then, if you must replace the key: note every secret, stop Ignition, set a new key (<code>openssl rand -base64 32</code>), start it and enter the secrets again. Everyone resets two-factor authentication.</li>
      </ol>
    </div> : <div className="guide">
      <p>The key lives in the <code>.env</code> file of the install folder, next to <code>deploy/docker-compose.yml</code>. Database backups alone are useless without it.</p>
      <ol>
        <li>Copy <code>.env</code> (or just the <code>IGNITION_SECRET_KEY=</code> line) into a password manager or an offline vault.</li>
        <li>Store it separately from database backups, so one leak doesn't expose both.</li>
        <li>To restore on a new machine, install Ignition, put the same key in <code>.env</code>, then restore the database.</li>
      </ol>
      <p>Lost key, no copy: secrets can't be recovered. Re-enter them and have everyone set up two-factor authentication again.</p>
    </div>}
  </Dialog>;
}

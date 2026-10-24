import { useState } from 'react';
import { ArrowRightIcon } from '@heroicons/react/16/solid';
import { Button, Notice, Select, Status, Switch, TextInput } from '../../../ui';
import { SaveBar, SettingsField, SettingsGroup } from '../SettingsField';
import { events, status } from './data';
import './Security.css';

const initial = {
  localAuth: true,
  disableRegistration: false,
  oidcAuth: false,
  requiredMethod: 'any',
  enforceMfa: false,
  passwordLength: '12',
  breachedPasswords: true,
  rateLimit: '10',
  maxAttempts: '5',
  lockout: '15',
  resetExpiry: '30',
  invitations: 'admins',
  ssoProvisioning: false,
  sessionLifetime: '7d',
  idleTimeout: '12h',
  revokeOnRoleChange: true,
  allowedOrigins: 'https://ignition.example.com',
  trustedProxies: '10.0.0.0/8',
  ipRestrictions: false,
  allowedIps: '',
  logRedaction: true,
};

const options = list => list.map(([value, label]) => ({ value, label }));
const requiredMethods = [
  { value: 'any', label: 'Any enabled method', description: 'Users choose how to sign in' },
  { value: 'sso', label: 'SSO only', description: 'Available once SSO is configured and verified' },
];
const passwordLengths = options([['8', '8 characters'], ['12', '12 characters'], ['16', '16 characters']]);
const rateLimits = options([['5', '5 per minute'], ['10', '10 per minute'], ['30', '30 per minute']]);
const attempts = options([['3', '3 attempts'], ['5', '5 attempts'], ['10', '10 attempts']]);
const lockouts = options([['5', '5 minutes'], ['15', '15 minutes'], ['60', '1 hour']]);
const resetExpiries = options([['15', '15 minutes'], ['30', '30 minutes'], ['60', '1 hour']]);
const invitationPolicies = [
  { value: 'admins', label: 'Admins only', description: 'Only admins can invite new users' },
  { value: 'members', label: 'Any member', description: 'Every user can send invitations' },
  { value: 'disabled', label: 'Disabled', description: 'No new accounts can be created' },
];
const lifetimes = options([['24h', '24 hours'], ['7d', '7 days'], ['30d', '30 days']]);
const idleTimeouts = options([['1h', '1 hour'], ['12h', '12 hours'], ['24h', '24 hours'], ['never', 'Never']]);

export function Security() {
  const [saved, setSaved] = useState(initial);
  const [values, setValues] = useState(initial);
  const [confirmRevoke, setConfirmRevoke] = useState(false);
  const dirty = JSON.stringify(values) !== JSON.stringify(saved);
  const set = key => value => setValues(v => ({ ...v, [key]: value }));
  const bind = key => ({ id: key, value: values[key], onChange: set(key) });
  const bindInput = key => ({ id: key, value: values[key], onChange: event => set(key)(event.target.value) });
  const noMethods = !values.localAuth && !values.oidcAuth;

  return <form className="settings-page security" onSubmit={event => { event.preventDefault(); if (!noMethods) setSaved(values); }}>
    <SettingsGroup title="Authentication providers" description="Which sign-in methods are available on this instance.">
      <SettingsField label="Local authentication" hint="Email and password accounts stored in Ignition.">
        <Switch label="Local authentication" checked={values.localAuth} onChange={set('localAuth')} />
      </SettingsField>
      <SettingsField label="Disable password registration" hint="Hide sign-up. New accounts only via invitation or SSO.">
        <Switch label="Disable password registration" checked={values.disableRegistration} onChange={set('disableRegistration')} disabled={!values.localAuth} />
      </SettingsField>
      <SettingsField label="OpenID Connect (SSO)" hint="Google, Okta, Authentik, Keycloak or any OIDC provider.">
        {status.oidc.configured
          ? <Switch label="OpenID Connect" checked={values.oidcAuth} onChange={set('oidcAuth')} />
          : <><span className="readonly-value">Not connected</span><Button type="button">Connect</Button></>}
      </SettingsField>
      {noMethods && <div className="field-notice"><Notice tone="danger" title="At least one method must stay enabled">Disabling every method would lock everyone out, including admins.</Notice></div>}
      <SettingsField label="Required sign-in method" hint="Instance-wide requirement for every user." htmlFor="requiredMethod">
        <Select options={requiredMethods} {...bind('requiredMethod')} />
      </SettingsField>
      {values.requiredMethod === 'sso' && !status.oidc.configured && <div className="field-notice"><Notice tone="warning" title="SSO isn't connected yet">Connect and verify an identity provider before requiring it, or nobody will be able to sign in.</Notice></div>}
    </SettingsGroup>

    <SettingsGroup title="Authentication policies" description="Rules applied to every sign-in.">
      <SettingsField label="Enforce MFA" hint="Every user must use a second factor. Users can't opt out.">
        <Switch label="Enforce MFA" checked={values.enforceMfa} onChange={set('enforceMfa')} />
      </SettingsField>
      {values.enforceMfa && !saved.enforceMfa && <div className="field-notice"><Notice tone="warning" title="Check admin access first">Users without MFA will have to enroll at their next sign-in. Make sure admins have recovery codes.</Notice></div>}
      <SettingsField label="Minimum password length" hint="Local accounts only." htmlFor="passwordLength">
        <Select options={passwordLengths} {...bind('passwordLength')} />
      </SettingsField>
      <SettingsField label="Block known breached passwords" hint="Reject passwords found in public breach lists.">
        <Switch label="Block known breached passwords" checked={values.breachedPasswords} onChange={set('breachedPasswords')} />
      </SettingsField>
      <SettingsField label="Login rate limit" hint="Sign-in attempts allowed per IP address." htmlFor="rateLimit">
        <Select options={rateLimits} {...bind('rateLimit')} />
      </SettingsField>
      <SettingsField label="Account lockout" hint="Failed attempts before an account is temporarily locked." htmlFor="maxAttempts">
        <Select options={attempts} {...bind('maxAttempts')} />
      </SettingsField>
      <SettingsField label="Lockout duration" htmlFor="lockout">
        <Select options={lockouts} {...bind('lockout')} />
      </SettingsField>
      <SettingsField label="Password reset link expiry" hint="Reset links work once and expire after this." htmlFor="resetExpiry">
        <Select options={resetExpiries} {...bind('resetExpiry')} />
      </SettingsField>
      <SettingsField label="Invitations" hint="Who can invite new users." htmlFor="invitations">
        <Select options={invitationPolicies} {...bind('invitations')} />
      </SettingsField>
      <SettingsField label="Provision accounts from SSO" hint="Create an account automatically on first SSO sign-in.">
        <Switch label="Provision accounts from SSO" checked={values.ssoProvisioning} onChange={set('ssoProvisioning')} disabled={!status.oidc.configured} />
      </SettingsField>
    </SettingsGroup>

    <SettingsGroup title="Session policies">
      <SettingsField label="Maximum session lifetime" hint="Users must sign in again after this." htmlFor="sessionLifetime">
        <Select options={lifetimes} {...bind('sessionLifetime')} />
      </SettingsField>
      <SettingsField label="Idle timeout" hint="Sign out after a period of inactivity." htmlFor="idleTimeout">
        <Select options={idleTimeouts} {...bind('idleTimeout')} />
      </SettingsField>
      <SettingsField label="Invalidate on password change" hint="Revoke a user's other sessions. Always on.">
        <Switch label="Invalidate on password change" checked onChange={() => {}} disabled />
      </SettingsField>
      <SettingsField label="Invalidate on role change" hint="Revoke sessions when permissions change.">
        <Switch label="Invalidate on role change" checked={values.revokeOnRoleChange} onChange={set('revokeOnRoleChange')} />
      </SettingsField>
      <SettingsField label="Revoke all sessions" hint="Signs out every user on the instance, including you.">
        {confirmRevoke
          ? <><Button type="button" onClick={() => setConfirmRevoke(false)}>Cancel</Button><Button type="button" className="danger-solid" onClick={() => setConfirmRevoke(false)}>Sign out everyone</Button></>
          : <Button type="button" className="danger" onClick={() => setConfirmRevoke(true)}>Revoke all</Button>}
      </SettingsField>
    </SettingsGroup>

    <SettingsGroup title="Network & transport security" description="Ignition always requires HTTPS for dashboard traffic.">
      <SettingsField label="HTTPS" hint={`Certificate by ${status.https.issuer}, expires ${status.https.expires}.`}>
        <Status tone={status.https.valid ? 'success' : 'danger'}>{status.https.valid ? 'Valid certificate' : 'Not secure'}</Status>
      </SettingsField>
      <SettingsField label="Trusted proxies" hint="Only trust forwarded headers from these addresses." htmlFor="trustedProxies">
        <TextInput className="mono" {...bindInput('trustedProxies')} />
      </SettingsField>
      <SettingsField label="Allowed origins" hint="Comma-separated. Other origins are rejected." htmlFor="allowedOrigins">
        <TextInput {...bindInput('allowedOrigins')} />
      </SettingsField>
      <SettingsField label="IP restrictions" hint="Only allow dashboard access from specific networks.">
        <Switch label="IP restrictions" checked={values.ipRestrictions} onChange={set('ipRestrictions')} />
      </SettingsField>
      {values.ipRestrictions && <SettingsField label="Allowed networks" hint="CIDR ranges, comma-separated. Include your own." htmlFor="allowedIps">
        <TextInput className="mono" placeholder="203.0.113.0/24" {...bindInput('allowedIps')} />
      </SettingsField>}
    </SettingsGroup>

    <SettingsGroup title="Secrets & encryption" description="Stored secret values are never displayed, regardless of permissions.">
      <SettingsField label="Encryption key" hint={`Loaded from ${status.encryption.keySource}.`}>
        <Status tone="success">Configured</Status>
      </SettingsField>
      <SettingsField label="Credentials at rest" hint="Environment variables, registry credentials and tokens.">
        <Status tone="success">Encrypted · {status.encryption.algorithm}</Status>
      </SettingsField>
      <SettingsField label="Redact secrets in logs" hint="Replace secret values with ••••• in build and runtime logs.">
        <Switch label="Redact secrets in logs" checked={values.logRedaction} onChange={set('logRedaction')} />
      </SettingsField>
      <SettingsField label="Key rotation" hint={`Last rotated ${status.encryption.lastRotated}.`}>
        <Button type="button">Rotation guide</Button>
      </SettingsField>
      <SettingsField label="Key backup & recovery" hint="Without the key, stored secrets can't be recovered.">
        <Button type="button">Recovery guide</Button>
      </SettingsField>
    </SettingsGroup>

    <section className="settings-group">
      <div className="group-head">
        <h2>Security events</h2>
        <button type="button" className="text-link">Audit log<ArrowRightIcon /></button>
      </div>
      <div className="settings-card">
        <ul className="event-list">
          {events.map((e, i) => <li key={i}>
            <Status tone={e.tone} label={e.tone === 'danger' ? 'High severity' : e.tone === 'warning' ? 'Medium severity' : 'Info'} />
            <span className="event-main"><strong>{e.action}</strong><span>{e.detail}</span></span>
            <span className="event-time">{e.time}</span>
          </li>)}
        </ul>
      </div>
    </section>

    <SaveBar dirty={dirty} onDiscard={() => setValues(saved)} />
  </form>;
}

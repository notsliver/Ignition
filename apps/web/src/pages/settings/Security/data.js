// Turns audit events about sign-in, accounts and security settings into the rows on the
// Authentication & Security page. Severity: danger for attacks and lockouts, warning for changes
// that weaken or bypass protection, neutral for the rest.

const settingNames = {
  localAuth: 'local authentication', disableRegistration: 'password registration', oidcAuth: 'SSO', requiredMethod: 'required method', enforceMfa: 'MFA enforcement', mfaDeadline: 'MFA deadline', mfaDeadlineAction: 'after the MFA deadline', mfaMessage: 'MFA reminder note',
  passwordLength: 'password length', breachedPasswords: 'breached password check', rateLimit: 'login rate limit', maxAttempts: 'lockout attempts',
  lockoutMinutes: 'lockout duration', resetExpiryMinutes: 'reset link expiry', invitations: 'invitations', ssoProvisioning: 'SSO provisioning',
  ssoDefaultRole: 'SSO role', sessionLifetime: 'session lifetime', idleTimeout: 'idle timeout', revokeOnRoleChange: 'revoke on role change',
  trustedProxies: 'trusted proxies', allowedOrigins: 'allowed origins', ipRestrictions: 'IP restrictions', allowedIps: 'allowed networks', logRedaction: 'log redaction',
};
const weakening = { enforceMfa: 'false', breachedPasswords: 'false', ipRestrictions: 'false', logRedaction: 'false', revokeOnRoleChange: 'false' };

export function describeEvent(e) {
  const d = e.detail ?? {};
  const who = d.email ?? e.actor;
  const by = e.actor && e.actor !== who ? ` · by ${e.actor}` : '';
  const from = e.ip ? ` · from ${e.ip}` : '';
  switch (e.action) {
    case 'auth.login': return { tone: 'neutral', action: `${who} signed in`, detail: `${d.method === 'sso' ? 'Single sign-on' : d.mfa ? 'Password and two-factor code' : 'Password'}${d.recoveryCode ? ` · used a recovery code (${d.recoveryCodesLeft} left)` : ''}${from}` };
    case 'auth.login_failed': return { tone: 'warning', action: 'Failed sign-in', detail: `${who}${d.reason ? ` · ${d.reason}` : ''}${from}` };
    case 'auth.locked': return { tone: 'danger', action: `${who} locked out`, detail: `${d.attempts} failed attempts · locked for ${d.minutes} min${from}` };
    case 'auth.mfa_failed': return { tone: 'warning', action: 'Wrong two-factor code', detail: `${who}${from}` };
    case 'auth.mfa_enabled': return { tone: 'neutral', action: `${who} turned on two-factor authentication`, detail: from.slice(3) || '—' };
    case 'auth.mfa_disabled': return { tone: 'warning', action: `${who} turned off two-factor authentication`, detail: from.slice(3) || '—' };
    case 'auth.recovery_codes_regenerated': return { tone: 'neutral', action: `${who} created new recovery codes`, detail: from.slice(3) || '—' };
    case 'auth.sso_linked': return { tone: 'neutral', action: `${who} linked single sign-on`, detail: d.issuer ?? '' };
    case 'user.mfa_reset': return { tone: 'warning', action: `Two-factor authentication reset for ${d.email}`, detail: `Their sessions ended${by}` };
    case 'user.role': return { tone: 'warning', action: 'Role changed', detail: `${d.email}: ${d.from} → ${d.to}${by}` };
    case 'user.permissions': return { tone: 'warning', action: 'Permissions changed', detail: `${d.email}${d.grant?.length ? ` · granted ${d.grant.join(', ')}` : ''}${d.deny?.length ? ` · denied ${d.deny.join(', ')}` : ''}${!d.grant?.length && !d.deny?.length ? ' · back to role defaults' : ''}${by}` };
    case 'user.invite': return { tone: 'neutral', action: `${d.email} invited`, detail: `As ${d.role}${by}` };
    case 'user.invite_revoked': return { tone: 'neutral', action: 'Invitation revoked', detail: `${d.email}${by}` };
    case 'user.joined': return { tone: 'neutral', action: `${d.email} joined`, detail: `As ${d.role}${d.method === 'sso' ? ' · through single sign-on' : ''}${d.provisioned ? ' · provisioned automatically' : ''}` };
    case 'user.suspend': return { tone: 'warning', action: `${d.email} suspended`, detail: d.reason ?? (by.slice(3) || '—') };
    case 'user.reactivate': return { tone: 'neutral', action: `${d.email} reactivated`, detail: by.slice(3) || '—' };
    case 'user.remove': return { tone: 'warning', action: `${d.email} removed`, detail: `Was ${d.role}${by}` };
    case 'user.sessions_revoked': return { tone: 'neutral', action: `${d.email} signed out everywhere`, detail: `${d.sessions} session(s)${by}` };
    case 'user.reset_link': return { tone: 'neutral', action: `Password reset link for ${d.email}`, detail: by.slice(3) || '—' };
    case 'user.password_reset': return { tone: 'neutral', action: `${e.actor} set a new password`, detail: 'Signed out of other sessions' };
    case 'security.settings': {
      const changed = Object.entries(d.changed ?? {});
      const weak = changed.some(([k, v]) => weakening[k] && v.endsWith(`→ ${weakening[k]}`));
      return { tone: weak ? 'warning' : 'neutral', action: changed.length === 1 ? `${settingNames[changed[0][0]] ?? changed[0][0]} changed` : 'Security settings changed',
        detail: `${changed.map(([k, v]) => `${settingNames[k] ?? k}: ${v}`).join(' · ') || 'No changes'}${by}` };
    }
    case 'security.sessions_revoked_all': return { tone: 'warning', action: 'Everyone signed out', detail: `${d.sessions} session(s)${by}` };
    case 'security.oidc_connected': return { tone: 'neutral', action: 'Single sign-on connected', detail: `${d.issuer}${by}` };
    case 'security.oidc_disconnected': return { tone: 'warning', action: 'Single sign-on disconnected', detail: by.slice(3) || '—' };
    case 'instance.setup': return { tone: 'neutral', action: 'Ignition set up', detail: `Owner ${e.actor}` };
  }
  return { tone: 'neutral', action: e.action, detail: `${e.actor}${from}` };
}

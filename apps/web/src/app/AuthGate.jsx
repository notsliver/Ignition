import { createContext, useContext, useEffect, useState } from 'react';
import { ArrowRightEndOnRectangleIcon, EyeIcon, EyeSlashIcon, KeyIcon, LockClosedIcon } from '@heroicons/react/16/solid';
import { Button, FormField, Logo, Notice, TextInput } from '../ui';
import { api, onUnauthenticated } from '../api/client';
import { EnrollTwoFactor, RecoveryCodes } from './TwoFactor';
import { adoptServerPreferences, setPreferenceSaver } from './preferences';
import './AuthGate.css';

const SessionContext = createContext(null);
export const useSession = () => useContext(SessionContext);

/** Whether the signed-in user has a permission (role defaults plus grants, minus denials). */
export const can = (user, permission) => !!user?.permissions?.includes(permission);

const withPermissions = out => {
  // Signed in: preferences are saved to the account, and the account's win over this browser's.
  setPreferenceSaver(p => api.savePreferences(p).catch(() => {}));
  adoptServerPreferences(out.preferences);
  return { ...out.user, permissions: out.permissions ?? [], mfaNotice: out.mfaNotice ?? null };
};

/** Shows first-run setup, sign-in, or an invite / reset link until there's a session, then renders the app. */
export function AuthGate({ children }) {
  const [state, setState] = useState({ phase: 'loading' });
  const [instance, setInstance] = useState('Ignition');

  async function check() {
    // Invite and password reset links (/join/<token>) work whether or not someone is signed in here.
    const join = location.pathname.match(/^\/join\/([A-Za-z0-9_-]+)$/);
    if (join) return setState({ phase: 'join', token: join[1] });
    try {
      const status = await api.setupStatus();
      if (status.instanceName) setInstance(status.instanceName);
      if (status.required) return setState({ phase: 'setup' });
      setState({ phase: 'ready', user: withPermissions(await api.me()) });
    } catch (e) {
      setState(e.status === 401 ? { phase: 'login' } : { phase: 'error', error: e });
    }
  }
  // Sessions ended by the two-factor deadline say why on the sign-in screen.
  useEffect(() => { check(); return onUnauthenticated(err => setState(['mfa_required', 'mfa_deadline_suspended'].includes(err?.code) ? { phase: 'login', message: err.message } : { phase: 'login', expired: true })); }, []);

  // A sign-in or link result is a session, a code to enter, or enrollment to finish first.
  const proceed = (out, after) => {
    if (out.mfaRequired) return setState({ phase: 'mfa', ticket: out.ticket });
    if (out.mfaSetupRequired) return setState({ phase: 'enroll', ticket: out.ticket });
    after?.();
    setState({ phase: 'ready', user: withPermissions(out) });
  };
  const restart = message => setState({ phase: 'login', message });

  if (state.phase === 'ready') return <SessionContext.Provider value={{
    user: state.user,
    refresh: async () => setState({ phase: 'ready', user: withPermissions(await api.me()) }),
    signOut: async () => { await api.logout().catch(() => {}); setPreferenceSaver(null); setState({ phase: 'login' }); },
  }}>{children}</SessionContext.Provider>;
  return <div className="auth-screen">
    <main className="auth-column">
      <div className="auth-brand"><Logo className="auth-logo" /><span>{instance}</span></div>
      <div className="auth-card">
        {state.phase === 'loading' && <p className="auth-loading">Connecting…</p>}
        {state.phase === 'error' && <div className="auth-form">
          <h1>Can't reach Ignition</h1>
          <Notice tone="danger" title="The API didn't answer">{state.error.message}</Notice>
          <Button variant="primary" onClick={check}>Try again</Button>
        </div>}
        {state.phase === 'setup' && <SetupForm onDone={out => proceed(out)} />}
        {state.phase === 'join' && <JoinForm token={state.token} onInstance={setInstance}
          onDone={out => proceed(out, () => history.replaceState(null, '', '/'))}
          onCancel={() => { history.replaceState(null, '', '/'); setState({ phase: 'loading' }); check(); }} />}
        {state.phase === 'login' && <LoginForm instance={instance} expired={state.expired} message={state.message} onDone={proceed} />}
        {state.phase === 'mfa' && <MfaForm ticket={state.ticket} onDone={proceed} onRestart={restart} />}
        {state.phase === 'enroll' && <EnrollForm ticket={state.ticket} instance={instance} onDone={proceed} onRestart={restart} />}
      </div>
      <p className="auth-foot"><LockClosedIcon />Self-hosted. Your apps and data stay on this machine.</p>
    </main>
  </div>;
}

function useForm(initial) {
  const [values, setValues] = useState(initial);
  const [errors, setErrors] = useState({});
  const [message, setMessage] = useState('');
  const [pending, setPending] = useState(false);
  const bind = key => ({ id: key, value: values[key], 'aria-invalid': !!errors[key], onChange: e => { setValues(v => ({ ...v, [key]: e.target.value })); setErrors(x => ({ ...x, [key]: undefined })); setMessage(''); } });
  async function submit(fn) {
    setPending(true);
    setMessage('');
    try {
      await fn(values);
    } catch (e) {
      setErrors(e.fields ?? {});
      if (!Object.keys(e.fields ?? {}).length) setMessage(e.message);
    } finally {
      setPending(false);
    }
  }
  return { values, errors, setErrors, message, pending, bind, submit };
}

// Password field with show/hide and a Caps Lock warning.
function PasswordInput({ count, min = 12, ...props }) {
  const [show, setShow] = useState(false);
  const [caps, setCaps] = useState(false);
  const capsCheck = e => setCaps(e.getModifierState?.('CapsLock') ?? false);
  return <>
    <TextInput {...props} type={show ? 'text' : 'password'} onKeyDown={capsCheck} onKeyUp={capsCheck} onBlur={() => setCaps(false)}
      suffix={<button type="button" className="auth-eye" aria-label={show ? 'Hide password' : 'Show password'} title={show ? 'Hide password' : 'Show password'} onClick={() => setShow(s => !s)}>{show ? <EyeSlashIcon /> : <EyeIcon />}</button>} />
    {(caps || count) && <span className="auth-under">{caps && <span className="auth-caps">Caps Lock is on</span>}{count && <span className={props.value.length >= min ? 'ok' : undefined}>{props.value.length}/{min}</span>}</span>}
  </>;
}

function useMethods() {
  const [methods, setMethods] = useState(null);
  useEffect(() => { api.authMethods().then(setMethods).catch(() => setMethods({ password: true, sso: false })); }, []);
  return methods;
}

const ssoButtonLabel = (label, verb) => !label || label === 'Single sign-on' ? `${verb} with single sign-on` : `${verb} with ${label}`;

function LoginForm({ instance, expired, message, onDone }) {
  const f = useForm({ email: '', password: '' });
  const methods = useMethods();
  const [ssoError] = useState(() => {
    const e = new URLSearchParams(location.search).get('sso_error');
    if (e) history.replaceState(null, '', location.pathname);
    return e;
  });
  const ssoOnly = methods?.requiredMethod === 'sso' || methods?.password === false;
  return <form className="auth-form" onSubmit={e => { e.preventDefault(); f.submit(async v => onDone(await api.login(v.email, v.password))); }}>
    <header><h1>Sign in</h1><p>to {instance}</p></header>
    {expired && <Notice tone="info" title="Your session ended">Sign in again to pick up where you left off.</Notice>}
    {message && <Notice tone="warning" title={message} />}
    {ssoError && <Notice tone="danger" title="Single sign-on didn't work">{ssoError}</Notice>}
    {f.message && <Notice tone="danger" title={f.message} />}
    {methods?.sso && <a className="button primary auth-sso" href={api.ssoStartUrl}><ArrowRightEndOnRectangleIcon />{ssoButtonLabel(methods.ssoLabel, 'Sign in')}</a>}
    {methods?.sso && <div className="auth-or"><span>{ssoOnly ? 'Owners only' : 'or'}</span></div>}
    <FormField label="Email" htmlFor="email" error={f.errors.email}><TextInput type="email" autoComplete="username" autoFocus={!methods?.sso} placeholder="you@example.com" {...f.bind('email')} /></FormField>
    <FormField label="Password" htmlFor="password" error={f.errors.password}><PasswordInput autoComplete="current-password" {...f.bind('password')} /></FormField>
    <Button type="submit" variant={methods?.sso ? undefined : 'primary'} disabled={f.pending}>{f.pending ? 'Signing in…' : methods?.sso ? 'Sign in with password' : 'Sign in'}</Button>
    <p className="auth-hint">{ssoOnly ? 'Password sign-in is kept for owners, in case single sign-on is down.' : 'Forgot your password? Ask an admin for a reset link.'}</p>
  </form>;
}

function MfaForm({ ticket, onDone, onRestart }) {
  const [recovery, setRecovery] = useState(false);
  const f = useForm({ code: '' });
  const submit = e => {
    e.preventDefault();
    f.submit(async v => {
      try { onDone(await api.mfaVerify(ticket, v.code)); } catch (err) { if (err.code === 'mfa_ticket_expired') onRestart(err.message); else throw err; }
    });
  };
  return <form className="auth-form" onSubmit={submit}>
    <header><h1>Two-factor authentication</h1><p>{recovery ? 'Enter one of your recovery codes. Each works once.' : 'Enter the 6-digit code from your authenticator app.'}</p></header>
    {f.message && <Notice tone="danger" title={f.message} />}
    <FormField label={recovery ? 'Recovery code' : 'Code'} htmlFor="code" error={f.errors.code}>
      <TextInput key={recovery ? 'recovery' : 'totp'} className={recovery ? 'mono' : 'mono tf-code-input'} autoFocus autoComplete="one-time-code" inputMode={recovery ? 'text' : 'numeric'} maxLength={recovery ? 20 : 7}
        placeholder={recovery ? 'xxxx-xxxx-xxxx' : '123 456'} {...f.bind('code')} />
    </FormField>
    <Button type="submit" variant="primary" disabled={f.pending || !f.values.code.trim()}>{f.pending ? 'Checking…' : 'Verify'}</Button>
    <button type="button" className="auth-link" onClick={() => setRecovery(r => !r)}><KeyIcon className="auth-link-icon" />{recovery ? 'Use my authenticator app' : 'Use a recovery code'}</button>
    <button type="button" className="auth-link" onClick={() => onRestart()}>Back to sign in</button>
  </form>;
}

function EnrollForm({ ticket, instance, onDone, onRestart }) {
  const [done, setDone] = useState(null);
  if (done) return <div className="auth-form">
    <header><h1>Two-factor authentication is on</h1><p>Keep these somewhere safe, like a password manager.</p></header>
    <RecoveryCodes codes={done.recoveryCodes} instance={instance} account={done.user.email} />
    <Button variant="primary" onClick={() => onDone(done)}>I've saved them, continue</Button>
  </div>;
  return <div className="auth-form">
    <header><h1>Set up two-factor authentication</h1><p>{instance} requires a second step at sign-in. It takes a minute.</p></header>
    <EnrollTwoFactor submitLabel="Verify and sign in"
      start={() => api.mfaSetupTicket(ticket).catch(e => { if (e.code === 'mfa_ticket_expired') onRestart(e.message); throw e; })}
      confirm={async code => setDone(await api.mfaConfirmTicket(ticket, code))} />
    <button type="button" className="auth-link" onClick={() => onRestart()}>Back to sign in</button>
  </div>;
}

function SetupForm({ onDone }) {
  const f = useForm({ code: '', name: '', email: '', password: '' });
  return <form className="auth-form" onSubmit={e => { e.preventDefault(); f.submit(async v => onDone(await api.setup(v))); }}>
    <header><h1>Set up Ignition</h1><p>Create the owner account for this machine.</p></header>
    <div className="auth-step">
      <span>The one-time setup code is in the API logs:</span>
      <code>docker compose logs api | grep setup_code</code>
    </div>
    {f.message && <Notice tone="danger" title={f.message} />}
    <FormField label="Setup code" htmlFor="code" error={f.errors.code}><TextInput className="mono" autoComplete="off" autoFocus placeholder="A1B2C3D4" {...f.bind('code')} /></FormField>
    <FormField label="Your name" htmlFor="name" error={f.errors.name}><TextInput autoComplete="name" {...f.bind('name')} /></FormField>
    <FormField label="Email" htmlFor="email" error={f.errors.email}><TextInput type="email" autoComplete="username" placeholder="you@example.com" {...f.bind('email')} /></FormField>
    <FormField label="Password" htmlFor="password" error={f.errors.password}><PasswordInput autoComplete="new-password" count {...f.bind('password')} /></FormField>
    <Button type="submit" variant="primary" disabled={f.pending}>{f.pending ? 'Creating…' : 'Create owner account'}</Button>
  </form>;
}

const ROLE_NAMES = { owner: 'Owner', admin: 'Admin', developer: 'Developer', viewer: 'Viewer' };

function JoinForm({ token, onDone, onCancel, onInstance }) {
  const [link, setLink] = useState(null);
  const [failure, setFailure] = useState('');
  const f = useForm({ name: '', password: '', confirm: '' });
  useEffect(() => {
    api.link(token).then(l => { setLink(l); if (l.instance) onInstance(l.instance); }).catch(e => setFailure(e.message));
  }, [token]); // eslint-disable-line react-hooks/exhaustive-deps

  if (failure) return <div className="auth-form">
    <header><h1>This link doesn't work</h1><p>{failure}.</p></header>
    <Button variant="primary" onClick={onCancel}>Go to sign in</Button>
  </div>;
  if (!link) return <p className="auth-loading">Checking your link…</p>;
  const invite = link.kind === 'invite';
  if (invite && link.passwordAllowed === false) return <div className="auth-form">
    <header><h1>{`Join ${link.instance}`}</h1><p>New accounts here are created through single sign-on.</p></header>
    <div className="auth-who"><span className="auth-who-email">{link.email}</span><span className="auth-role">{ROLE_NAMES[link.role] ?? link.role}</span></div>
    {link.sso ? <a className="button primary auth-sso" href={api.ssoStartUrl}><ArrowRightEndOnRectangleIcon />{ssoButtonLabel(link.ssoLabel, 'Continue')}</a>
      : <Notice tone="warning" title="There's no way to join yet">Password sign-up is off and single sign-on isn't set up. Ask the admin who invited you.</Notice>}
    <p className="auth-hint">Sign in as {link.email}; your invitation's role is applied.</p>
    <button type="button" className="auth-link" onClick={onCancel}>Go to sign in</button>
  </div>;
  return <form className="auth-form" onSubmit={e => {
    e.preventDefault();
    if (f.values.password !== f.values.confirm) { f.setErrors({ confirm: "The passwords don't match." }); return; }
    f.submit(async v => onDone(await api.acceptLink(token, { name: v.name, password: v.password })));
  }}>
    <header>
      <h1>{invite ? `Join ${link.instance}` : 'Choose a new password'}</h1>
      <p>{invite ? 'You\'ve been invited. Pick a name and password to finish.' : 'Setting it signs you out on every other device.'}</p>
    </header>
    <div className="auth-who">
      <span className="auth-who-email">{link.email}</span>
      {invite && <span className="auth-role">{ROLE_NAMES[link.role] ?? link.role}</span>}
    </div>
    {f.message && <Notice tone="danger" title={f.message} />}
    {invite && <FormField label="Your name" htmlFor="name" error={f.errors.name}><TextInput autoComplete="name" autoFocus {...f.bind('name')} /></FormField>}
    <input type="email" autoComplete="username" value={link.email} readOnly hidden />
    <FormField label={invite ? 'Password' : 'New password'} htmlFor="password" error={f.errors.password}><PasswordInput autoComplete="new-password" autoFocus={!invite} count min={link.passwordLength ?? 12} {...f.bind('password')} /></FormField>
    <FormField label="Confirm password" htmlFor="confirm" error={f.errors.confirm}><PasswordInput autoComplete="new-password" {...f.bind('confirm')} /></FormField>
    <Button type="submit" variant="primary" disabled={f.pending}>{f.pending ? 'One moment…' : invite ? 'Create my account' : 'Set password and sign in'}</Button>
    <button type="button" className="auth-link" onClick={onCancel}>{invite ? 'I already have an account' : 'Cancel'}</button>
  </form>;
}

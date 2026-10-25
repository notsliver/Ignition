import { createContext, useContext, useEffect, useState } from 'react';
import { EyeIcon, EyeSlashIcon, LockClosedIcon } from '@heroicons/react/16/solid';
import { Button, FormField, Logo, Notice, TextInput } from '../ui';
import { api, onUnauthenticated } from '../api/client';
import './AuthGate.css';

const SessionContext = createContext(null);
export const useSession = () => useContext(SessionContext);

const MIN_PASSWORD = 12;

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
      const { user } = await api.me();
      setState({ phase: 'ready', user });
    } catch (e) {
      setState(e.status === 401 ? { phase: 'login' } : { phase: 'error', error: e });
    }
  }
  useEffect(() => { check(); return onUnauthenticated(() => setState({ phase: 'login', expired: true })); }, []);

  if (state.phase === 'ready') return <SessionContext.Provider value={{ user: state.user, signOut: async () => { await api.logout().catch(() => {}); setState({ phase: 'login' }); } }}>{children}</SessionContext.Provider>;
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
        {state.phase === 'setup' && <SetupForm onDone={user => setState({ phase: 'ready', user })} />}
        {state.phase === 'join' && <JoinForm token={state.token} onInstance={setInstance}
          onDone={user => { history.replaceState(null, '', '/'); setState({ phase: 'ready', user }); }}
          onCancel={() => { history.replaceState(null, '', '/'); setState({ phase: 'loading' }); check(); }} />}
        {state.phase === 'login' && <LoginForm instance={instance} expired={state.expired} onDone={user => setState({ phase: 'ready', user })} />}
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
function PasswordInput({ count, ...props }) {
  const [show, setShow] = useState(false);
  const [caps, setCaps] = useState(false);
  const capsCheck = e => setCaps(e.getModifierState?.('CapsLock') ?? false);
  return <>
    <TextInput {...props} type={show ? 'text' : 'password'} onKeyDown={capsCheck} onKeyUp={capsCheck} onBlur={() => setCaps(false)}
      suffix={<button type="button" className="auth-eye" aria-label={show ? 'Hide password' : 'Show password'} title={show ? 'Hide password' : 'Show password'} onClick={() => setShow(s => !s)}>{show ? <EyeSlashIcon /> : <EyeIcon />}</button>} />
    {(caps || count) && <span className="auth-under">{caps && <span className="auth-caps">Caps Lock is on</span>}{count && <span className={props.value.length >= MIN_PASSWORD ? 'ok' : undefined}>{props.value.length}/{MIN_PASSWORD}</span>}</span>}
  </>;
}

function LoginForm({ instance, expired, onDone }) {
  const f = useForm({ email: '', password: '' });
  return <form className="auth-form" onSubmit={e => { e.preventDefault(); f.submit(async v => onDone((await api.login(v.email, v.password)).user)); }}>
    <header><h1>Sign in</h1><p>to {instance}</p></header>
    {expired && <Notice tone="info" title="Your session ended">Sign in again to pick up where you left off.</Notice>}
    {f.message && <Notice tone="danger" title={f.message} />}
    <FormField label="Email" htmlFor="email" error={f.errors.email}><TextInput type="email" autoComplete="username" autoFocus placeholder="you@example.com" {...f.bind('email')} /></FormField>
    <FormField label="Password" htmlFor="password" error={f.errors.password}><PasswordInput autoComplete="current-password" {...f.bind('password')} /></FormField>
    <Button type="submit" variant="primary" disabled={f.pending}>{f.pending ? 'Signing in…' : 'Sign in'}</Button>
    <p className="auth-hint">Forgot your password? Ask an admin for a reset link.</p>
  </form>;
}

function SetupForm({ onDone }) {
  const f = useForm({ code: '', name: '', email: '', password: '' });
  return <form className="auth-form" onSubmit={e => { e.preventDefault(); f.submit(async v => onDone((await api.setup(v)).user)); }}>
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
  return <form className="auth-form" onSubmit={e => {
    e.preventDefault();
    if (f.values.password !== f.values.confirm) { f.setErrors({ confirm: "The passwords don't match." }); return; }
    f.submit(async v => onDone((await api.acceptLink(token, { name: v.name, password: v.password })).user));
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
    <FormField label={invite ? 'Password' : 'New password'} htmlFor="password" error={f.errors.password}><PasswordInput autoComplete="new-password" autoFocus={!invite} count {...f.bind('password')} /></FormField>
    <FormField label="Confirm password" htmlFor="confirm" error={f.errors.confirm}><PasswordInput autoComplete="new-password" {...f.bind('confirm')} /></FormField>
    <Button type="submit" variant="primary" disabled={f.pending}>{f.pending ? 'One moment…' : invite ? 'Create my account' : 'Set password and sign in'}</Button>
    <button type="button" className="auth-link" onClick={onCancel}>{invite ? 'I already have an account' : 'Cancel'}</button>
  </form>;
}

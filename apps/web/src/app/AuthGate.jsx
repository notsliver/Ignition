import { createContext, useContext, useEffect, useState } from 'react';
import { Button, FormField, Notice, TextInput } from '../ui';
import { api, onUnauthenticated } from '../api/client';
import './AuthGate.css';

const SessionContext = createContext(null);
export const useSession = () => useContext(SessionContext);

/** Shows first-run setup or sign-in until there's a session, then renders the app. */
export function AuthGate({ children }) {
  const [state, setState] = useState({ phase: 'loading' });

  async function check() {
    try {
      const { required } = await api.setupStatus();
      if (required) return setState({ phase: 'setup' });
      const { user } = await api.me();
      setState({ phase: 'ready', user });
    } catch (e) {
      setState(e.status === 401 ? { phase: 'login' } : { phase: 'error', error: e });
    }
  }
  useEffect(() => { check(); return onUnauthenticated(() => setState({ phase: 'login', expired: true })); }, []);

  if (state.phase === 'ready') return <SessionContext.Provider value={{ user: state.user, signOut: async () => { await api.logout().catch(() => {}); setState({ phase: 'login' }); } }}>{children}</SessionContext.Provider>;
  return <div className="auth-screen">
    <div className="auth-card">
      <div className="auth-brand"><span className="brand-mark"><img src="/ignition-logo.svg" alt="" /></span>Ignition</div>
      {state.phase === 'loading' && <p className="auth-muted">Connecting…</p>}
      {state.phase === 'error' && <><Notice tone="danger" title="Can't reach the API">{state.error.message}</Notice><Button onClick={check}>Try again</Button></>}
      {state.phase === 'setup' && <SetupForm onDone={user => setState({ phase: 'ready', user })} />}
      {state.phase === 'login' && <LoginForm expired={state.expired} onDone={user => setState({ phase: 'ready', user })} />}
    </div>
  </div>;
}

function useForm(initial) {
  const [values, setValues] = useState(initial);
  const [errors, setErrors] = useState({});
  const [message, setMessage] = useState('');
  const [pending, setPending] = useState(false);
  const bind = key => ({ id: key, value: values[key], 'aria-invalid': !!errors[key], onChange: e => { setValues(v => ({ ...v, [key]: e.target.value })); setErrors(x => ({ ...x, [key]: undefined })); } });
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
  return { values, errors, message, pending, bind, submit };
}

function LoginForm({ expired, onDone }) {
  const f = useForm({ email: '', password: '' });
  return <form className="auth-form" onSubmit={e => { e.preventDefault(); f.submit(async v => onDone((await api.login(v.email, v.password)).user)); }}>
    <h1>Sign in</h1>
    {expired && <p className="auth-muted">Your session ended. Sign in again to continue.</p>}
    <FormField label="Email" htmlFor="email" error={f.errors.email}><TextInput type="email" autoComplete="username" autoFocus {...f.bind('email')} /></FormField>
    <FormField label="Password" htmlFor="password" error={f.errors.password}><TextInput type="password" autoComplete="current-password" {...f.bind('password')} /></FormField>
    {f.message && <p className="rp-error">{f.message}</p>}
    <Button type="submit" variant="primary" disabled={f.pending}>{f.pending ? 'Signing in…' : 'Sign in'}</Button>
  </form>;
}

function SetupForm({ onDone }) {
  const f = useForm({ code: '', name: '', email: '', password: '' });
  return <form className="auth-form" onSubmit={e => { e.preventDefault(); f.submit(async v => onDone((await api.setup(v)).user)); }}>
    <h1>Set up Ignition</h1>
    <p className="auth-muted">Create the owner account. Find the one-time setup code in the API logs: <code>docker compose logs api</code></p>
    <FormField label="Setup code" htmlFor="code" error={f.errors.code}><TextInput className="mono" autoComplete="off" autoFocus {...f.bind('code')} /></FormField>
    <FormField label="Your name" htmlFor="name" error={f.errors.name}><TextInput autoComplete="name" {...f.bind('name')} /></FormField>
    <FormField label="Email" htmlFor="email" error={f.errors.email}><TextInput type="email" autoComplete="username" {...f.bind('email')} /></FormField>
    <FormField label="Password" hint="At least 12 characters." htmlFor="password" error={f.errors.password}><TextInput type="password" autoComplete="new-password" {...f.bind('password')} /></FormField>
    {f.message && <p className="rp-error">{f.message}</p>}
    <Button type="submit" variant="primary" disabled={f.pending}>{f.pending ? 'Creating…' : 'Create owner account'}</Button>
  </form>;
}

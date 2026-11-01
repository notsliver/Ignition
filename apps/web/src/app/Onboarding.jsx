import { useEffect, useState } from 'react';
import {
  ArrowLeftIcon, ArrowRightIcon, BuildingStorefrontIcon, CheckCircleIcon, CheckIcon, ClipboardDocumentIcon, ExclamationTriangleIcon,
  InformationCircleIcon, LockClosedIcon, ServerStackIcon, ShieldCheckIcon, UserCircleIcon,
} from '@heroicons/react/16/solid';
import { Button, Checkbox, FormField, Logo, Notice, Select, TextInput, cx } from '../ui';
import { api } from '../api/client';
import { EnrollTwoFactor, RecoveryCodes } from './TwoFactor';
import './Onboarding.css';

// Setup finishes once the initial administrator has two-factor authentication.

export function Onboarding({ instance, user: signedIn, useCase: savedUseCase, onDone }) {
  const [step, setStep] = useState(signedIn ? 2 : 0);
  const [useCase, setUseCase] = useState(savedUseCase || null);
  const [user, setUser] = useState(signedIn ?? null);

  return <div className="ob-app">
    <header className="ob-title">Setting up Ignition</header>
    <main className="ob-container">
      <div className="ob-body">
        {step === 0 && <Purpose value={useCase} onChange={setUseCase} onNext={() => setStep(1)} />}
        {step === 1 && <Account useCase={useCase} onBack={() => setStep(0)} onCreated={u => { setUser(u); setStep(2); }} />}
        {step === 2 && <Secure instance={instance} user={user} onDone={onDone} />}
      </div>
    </main>
  </div>;
}

/* ---------------------------------- Purpose --------------------------------- */

function Purpose({ value, onChange, onNext }) {
  const options = [
    { id: 'infrastructure', icon: ServerStackIcon, title: 'Manage my infrastructure', who: 'For individuals, developers, and infrastructure teams',
      text: 'Manage your own servers, applications, deployments, networking, storage, and game servers.' },
    { id: 'hosting', icon: BuildingStorefrontIcon, title: 'Run a hosting business', who: 'For hosting providers and service operators',
      text: 'Manage customers, services, provisioning, and the infrastructure powering your business.' },
  ];
  return <section className="ob-step">
    <Logo className="ob-hero-logo" />
    <h1>Welcome to Ignition.</h1>
    <p className="ob-lead">Let's get your control plane ready. First, tell us what you want to use Ignition for.</p>
    <div className="ob-options" role="radiogroup" aria-label="What you'll use Ignition for">
      {options.map(o => <button key={o.id} type="button" role="radio" aria-checked={value === o.id} className={cx('ob-option', value === o.id && 'on')}
        onClick={() => onChange(o.id)} onDoubleClick={() => { onChange(o.id); onNext(); }}>
        <span className="ob-option-icon"><o.icon /></span>
        <span className="ob-option-text"><strong>{o.title}</strong><span>{o.text}</span><small>{o.who}</small></span>
        <span className="ob-radio">{value === o.id && <CheckIcon />}</span>
      </button>)}
    </div>
    {value === 'hosting' && <Notice tone="info" title="The hosting provider experience is on its way">
      Customers, services and provisioning aren't in this version yet. Ignition remembers your choice and starts you with infrastructure, which everything else builds on.
    </Notice>}
    <div className="ob-note"><InformationCircleIcon /><span><strong>Not sure yet?</strong> You can start with infrastructure and enable the hosting provider experience later. Your choice isn't permanent.</span></div>
    <footer className="ob-actions">
      <span className="ob-spacer" />
      <Button variant="primary" disabled={!value} onClick={onNext}>Continue<ArrowRightIcon className="ob-after" /></Button>
    </footer>
  </section>;
}

/* ---------------------------------- Account --------------------------------- */

function Account({ useCase, onBack, onCreated }) {
  const [v, setV] = useState({ code: '', name: '', email: '', password: '', confirm: '' });
  const [errors, setErrors] = useState({});
  const [message, setMessage] = useState('');
  const [pending, setPending] = useState(false);
  const [copied, setCopied] = useState(false);
  const bind = key => ({ id: `ob-${key}`, value: v[key], onChange: e => { setV(x => ({ ...x, [key]: e.target.value })); setErrors(x => ({ ...x, [key]: '' })); } });
  const cmd = 'docker compose logs api | grep setup_code';
  async function submit(e) {
    e.preventDefault();
    const f = {};
    if (!v.code.trim()) f.code = 'Enter the setup code from the API logs.';
    if (!v.name.trim()) f.name = 'Enter your name.';
    if (!/^\S+@\S+\.\S+$/.test(v.email.trim())) f.email = 'Enter a valid email address.';
    if (v.password.length < 12) f.password = 'Use at least 12 characters.';
    if (v.confirm !== v.password) f.confirm = 'The passwords don\'t match.';
    setErrors(f); setMessage('');
    if (Object.keys(f).length) return;
    setPending(true);
    try {
      onCreated(await api.setup({ code: v.code, name: v.name.trim(), email: v.email.trim(), password: v.password, useCase: useCase ?? 'infrastructure' }));
    } catch (err) {
      setErrors(err.fields ?? {});
      setMessage(err.fields && Object.keys(err.fields).length ? '' : err.message);
      setPending(false);
    }
  }
  return <form className="ob-step" onSubmit={submit} noValidate>
    <h1>Create your Ignition account</h1>
    <p className="ob-lead">Set up the administrator of this Ignition instance.</p>
    {message && <Notice tone="danger" title={message} />}
    <div className="ob-card">
      <div className="ob-code">
        <FormField label="Setup code" htmlFor="ob-code" error={errors.code} hint="Proves you're the one installing it. It's printed once in the API logs.">
          <TextInput className="mono" autoComplete="off" autoFocus placeholder="A1B2C3D4" {...bind('code')} />
        </FormField>
        <button type="button" className="ob-cmd" onClick={() => { navigator.clipboard?.writeText(cmd); setCopied(true); setTimeout(() => setCopied(false), 1500); }} title="Copy">
          <code>{cmd}</code>{copied ? <CheckIcon /> : <ClipboardDocumentIcon />}</button>
      </div>
      <div className="ob-grid">
        <FormField label="Full name" htmlFor="ob-name" error={errors.name}><TextInput autoComplete="name" placeholder="Ada Lovelace" {...bind('name')} /></FormField>
        <FormField label="Email address" htmlFor="ob-email" error={errors.email}><TextInput type="email" autoComplete="username" placeholder="you@example.com" {...bind('email')} /></FormField>
        <FormField label="Password" htmlFor="ob-password" error={errors.password} hint={v.password ? `${v.password.length} characters${v.password.length < 12 ? ', 12 or more needed' : ''}` : 'At least 12 characters. A passphrase works well.'}>
          <TextInput type="password" autoComplete="new-password" {...bind('password')} />
        </FormField>
        <FormField label="Confirm password" htmlFor="ob-confirm" error={errors.confirm}>
          <TextInput type="password" autoComplete="new-password" {...bind('confirm')} />
        </FormField>
      </div>
    </div>
    <div className="ob-note role"><UserCircleIcon /><span><strong>Initial administrator</strong> This account will have full administrative access to the instance. Invite your team with their own accounts afterwards.</span></div>
    <footer className="ob-actions">
      <Button icon={ArrowLeftIcon} onClick={onBack} disabled={pending}>Back</Button>
      <span className="ob-spacer" />
      <Button type="submit" variant="primary" disabled={pending}>{pending ? 'Creating…' : 'Create account'}{!pending && <ArrowRightIcon className="ob-after" />}</Button>
    </footer>
  </form>;
}

/* ---------------------------------- Secure ---------------------------------- */

function Secure({ instance, user, onDone }) {
  const [mfaOn, setMfaOn] = useState(!!user?.mfaEnabledAt);
  const [codes, setCodes] = useState(null);
  const [saved, setSaved] = useState(!!user?.mfaEnabledAt);
  const [security, setSecurity] = useState(null);
  const [session, setSession] = useState(null);
  const [sessionSaved, setSessionSaved] = useState(false);
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  const load = () => api.security().then(s => { setSecurity(s); setSession(x => x ?? { sessionLifetime: s.policy.sessionLifetime, idleTimeout: s.policy.idleTimeout }); }).catch(() => {});
  useEffect(() => { load(); }, []);
  const protectedNow = mfaOn && saved;

  async function saveSession() {
    try {
      await api.saveSecurity({ ...security.policy, ...session });
      setSessionSaved(true);
      load();
    } catch (e) { setError(e.fields ? Object.values(e.fields).join(' ') : e.message); }
  }
  async function finish() {
    setPending(true); setError('');
    try {
      if (security && (session.sessionLifetime !== security.policy.sessionLifetime || session.idleTimeout !== security.policy.idleTimeout)) await api.saveSecurity({ ...security.policy, ...session });
      await api.completeSetup();
      onDone();
    } catch (e) { setError(e.message); setPending(false); }
  }

  const st = security?.status;
  const checks = [
    { ok: mfaOn, label: 'Two-factor authentication', detail: mfaOn ? 'On for your account' : 'Required to finish' },
    { ok: saved, label: 'Recovery codes stored', detail: saved ? 'You confirmed they\'re saved' : 'Shown once two-factor is on' },
    { ok: true, label: 'Setup code used', detail: 'It can\'t be used again' },
    { ok: !!st?.encryption?.configured, label: 'Secrets encrypted', detail: st ? `${st.encryption.algorithm}, key from ${st.encryption.keySource}` : 'Checking…' },
    { ok: st?.https?.https ? st.https.valid : null, label: 'HTTPS', detail: !st ? 'Checking…' : st.https.https ? (st.https.valid ? `Valid certificate${st.https.issuer ? ` from ${st.https.issuer}` : ''}` : (st.https.error || 'The certificate isn\'t valid')) : 'Not yet: set a domain under Settings › General to turn it on' },
    { ok: null, label: 'Email verification', detail: 'Email delivery isn\'t configured, so there\'s nothing to verify. Your address is used to sign in.' },
  ];

  return <section className="ob-step wide">
    <h1>Secure your instance</h1>
    <p className="ob-lead">Some security options can be configured later, but the initial account must be protected.</p>
    {error && <Notice tone="danger" title={error} />}
    <div className="ob-secure">
      <div className="ob-secure-main">
        <section className={cx('ob-card', protectedNow && 'done')}>
          <header className="ob-card-head"><ShieldCheckIcon /><span><strong>Two-factor authentication</strong><small>Required · a code from your phone at every sign-in</small></span>
            {mfaOn && <span className="ob-chip ok"><CheckIcon />On</span>}</header>
          {!mfaOn ? <EnrollTwoFactor start={api.accountMfaSetup} submitLabel="Turn on two-factor"
            confirm={async code => { const out = await api.accountMfaEnable(code); setCodes(out.recoveryCodes ?? []); setMfaOn(true); setSaved(false); load(); }} />
            : codes ? <>
              <RecoveryCodes codes={codes} instance={instance} account={user?.email} />
              <div className={cx('ob-confirm', saved && 'on')} onClick={() => setSaved(s => !s)}>
                <Checkbox checked={saved} label="I've saved my recovery codes" onChange={setSaved} />
                <span><strong>I've saved my recovery codes somewhere safe</strong><small>A password manager is ideal. Without them, losing your phone locks you out.</small></span>
              </div>
            </> : <p className="ob-muted">Two-factor is on and your recovery codes were already shown. New ones can be made under your account.</p>}
        </section>
        <section className="ob-card">
          <header className="ob-card-head"><LockClosedIcon /><span><strong>Sessions</strong><small>How long a sign-in lasts</small></span>{sessionSaved && <span className="ob-chip ok"><CheckIcon />Saved</span>}</header>
          {session ? <div className="ob-grid">
            <FormField label="Stay signed in for" htmlFor="ob-life"><Select id="ob-life" value={session.sessionLifetime} onChange={x => { setSession(s => ({ ...s, sessionLifetime: x })); setSessionSaved(false); }}
              options={[['24h', '1 day'], ['7d', '7 days'], ['30d', '30 days']].map(([value, label]) => ({ value, label }))} /></FormField>
            <FormField label="Sign out after inactivity" htmlFor="ob-idle"><Select id="ob-idle" value={session.idleTimeout} onChange={x => { setSession(s => ({ ...s, idleTimeout: x })); setSessionSaved(false); }}
              options={[['1h', '1 hour'], ['12h', '12 hours'], ['24h', '1 day'], ['never', 'Never']].map(([value, label]) => ({ value, label }))} /></FormField>
          </div> : <p className="ob-muted">Loading…</p>}
          {session && !sessionSaved && <Button onClick={saveSession}>Save session settings</Button>}
        </section>
      </div>
      <aside className="ob-status" aria-label="Security status">
        <h2>Security status</h2>
        <ul>{checks.map(c => <li key={c.label} className={c.ok === true ? 'ok' : c.ok === false ? 'bad' : 'na'}>
          {c.ok === true ? <CheckCircleIcon /> : c.ok === false ? <ExclamationTriangleIcon /> : <InformationCircleIcon />}
          <span><strong>{c.label}</strong><small>{c.detail}</small></span></li>)}</ul>
        <p>More under Settings › Authentication & Security.</p>
      </aside>
    </div>
    <footer className="ob-actions">
      {!protectedNow && <span className="ob-muted">{mfaOn ? 'Confirm your recovery codes are saved to finish.' : 'Turn on two-factor authentication to finish.'}</span>}
      <span className="ob-spacer" />
      <Button variant="primary" disabled={!protectedNow || pending} onClick={finish}>{pending ? 'Finishing…' : 'Finish setup'}{!pending && <ArrowRightIcon className="ob-after" />}</Button>
    </footer>
  </section>;
}

import { useState } from 'react';
import { CheckIcon, LinkIcon, MinusIcon, ShieldCheckIcon, UserPlusIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, Notice, Select, Status, TextInput } from '../ui';
import { api } from '../api/client';
import { useQuery } from '../api/hooks';
import { useSession } from './AuthGate';
import { useInstanceName } from './instance';
import { EnrollTwoFactor, RecoveryCodes } from './TwoFactor';
import './AccountDialog.css';

/*
 * Your account: two-factor authentication (turn on, new recovery codes, turn off) and what your
 * role and permissions let you do. Opened from the account menu; no separate page.
 */

const roleNames = { owner: 'Owner', admin: 'Admin', developer: 'Developer', viewer: 'Viewer' };

export function AccountDialog({ onClose }) {
  const { user, refresh } = useSession();
  const instance = useInstanceName();
  const { data, error, refetch } = useQuery(() => api.accountSecurity(), []);
  const catalog = useQuery(() => api.permissions(), []);
  const [mode, setMode] = useState(null); // enroll | codes | disable | regenerate | invite
  const [codes, setCodes] = useState(null);
  const [feedback, setFeedback] = useState(null);

  // Refreshing the session hides the two-factor banner as soon as it's set up.
  const done = (title, nextCodes) => { setMode(nextCodes ? 'codes' : null); setCodes(nextCodes ?? null); setFeedback(title ? { tone: 'success', title } : null); refetch(); refresh?.(); };
  const canInvite = data && data.invitations === 'members' && !data.permissions.includes('users.invite');

  return <Dialog open onClose={onClose} icon={ShieldCheckIcon} title="Your account" description={`${user.name ? `${user.name} · ` : ''}${user.email}`} className="acct-dialog"
    footer={<Button variant="primary" onClick={onClose}>Done</Button>}>
    {error && <Notice tone="danger" title="Couldn't load your account">{error.message}</Notice>}
    {feedback && <Notice tone={feedback.tone} title={feedback.title} />}

    <section className="acct-section">
      <header>
        <h3>Two-factor authentication</h3>
        {data && <Status tone={data.mfaEnabled ? 'success' : data.mfaEnforced ? 'warning' : 'neutral'}>{data.mfaEnabled ? 'On' : data.mfaEnforced ? 'Required' : 'Off'}</Status>}
      </header>
      {!data ? <p className="acct-muted">Loading…</p>
        : mode === 'enroll' ? <EnrollTwoFactor start={api.accountMfaSetup} confirm={async code => done('Two-factor authentication is on', (await api.accountMfaEnable(code)).recoveryCodes)} />
          : mode === 'codes' ? <><RecoveryCodes codes={codes} instance={instance} account={user.email} /><Button onClick={() => done(null)}>I've saved them</Button></>
            : mode === 'disable' ? <CodeConfirm label="Turn off" danger hint="Enter a current code from your app, or a recovery code."
              onCancel={() => setMode(null)} onConfirm={async code => { await api.accountMfaDisable(code); done('Two-factor authentication is off'); }} />
              : mode === 'regenerate' ? <CodeConfirm label="Create new codes" hint="Your old recovery codes stop working."
                onCancel={() => setMode(null)} onConfirm={async code => done('New recovery codes created', (await api.accountRecoveryCodes(code)).recoveryCodes)} />
                : data.mfaEnabled ? <>
                  <p className="acct-muted">Sign-in asks for a code from your authenticator app. {data.recoveryCodesLeft} of 10 recovery codes left{data.recoveryCodesLeft <= 3 ? '; create new ones soon.' : '.'}</p>
                  <div className="acct-actions">
                    <Button onClick={() => setMode('regenerate')}>New recovery codes</Button>
                    {!data.mfaEnforced && <Button className="danger" onClick={() => setMode('disable')}>Turn off</Button>}
                  </div>
                  {data.mfaEnforced && <p className="acct-muted">Required on {instance}, so it can't be turned off.</p>}
                </> : <>
                  <p className="acct-muted">{data.sso && !data.hasPassword ? 'You sign in with single sign-on, which uses your identity provider\'s own second factor. ' : ''}Add a code from an authenticator app to every password sign-in.{data.mfaEnforced ? ` ${instance} requires it.` : ''}</p>
                  <div className="acct-actions"><Button variant="primary" onClick={() => setMode('enroll')}>Set up</Button></div>
                </>}
    </section>

    {data && <section className="acct-section">
      <header><h3>Access</h3><span className="rp-chip">{roleNames[data.role]}</span>{data.sso && <span className="rp-chip">SSO linked</span>}</header>
      <dl className="acct-perms">
        {[...new Set((catalog.data?.permissions ?? []).map(p => p.group))].map(group => {
          const perms = catalog.data.permissions.filter(p => p.group === group);
          const n = perms.filter(p => data.permissions.includes(p.key)).length;
          return <div key={group}>
            <dt>{n === perms.length ? <CheckIcon className="all" aria-label="All" /> : n ? <CheckIcon className="some" aria-label="Some" /> : <MinusIcon aria-label="None" />}{group}</dt>
            <dd>{perms.map(p => <span key={p.key} className={data.permissions.includes(p.key) ? 'yes' : 'no'} title={`${p.key}: ${p.description}`}>{p.label}</span>)}</dd>
          </div>;
        })}
      </dl>
    </section>}

    {canInvite && <section className="acct-section">
      <header><h3>Invite someone</h3></header>
      {mode === 'invite' ? <InviteForm role={data.role} onDone={() => setMode(null)} /> : <>
        <p className="acct-muted">Anyone on {instance} can invite people with their own role or below.</p>
        <div className="acct-actions"><Button icon={UserPlusIcon} onClick={() => setMode('invite')}>Invite</Button></div>
      </>}
    </section>}
  </Dialog>;
}

function CodeConfirm({ label, hint, danger, onConfirm, onCancel }) {
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  async function submit(e) {
    e.preventDefault();
    setPending(true);
    try { await onConfirm(code); } catch (err) { setError(err.fields?.code ?? err.message); setPending(false); }
  }
  return <form className="acct-confirm" onSubmit={submit}>
    <FormField label="Code" htmlFor="acct-code" error={error} hint={hint}>
      <TextInput id="acct-code" className="mono" autoFocus autoComplete="one-time-code" value={code} onChange={e => { setCode(e.target.value); setError(''); }} placeholder="123 456" />
    </FormField>
    <div className="acct-actions">
      <Button type="button" onClick={onCancel}>Cancel</Button>
      <Button type="submit" variant={danger ? undefined : 'primary'} className={danger ? 'danger-solid' : undefined} disabled={pending || !code.trim()}>{pending ? 'Checking…' : label}</Button>
    </div>
  </form>;
}

const order = ['viewer', 'developer', 'admin', 'owner'];

function InviteForm({ role, onDone }) {
  const [email, setEmail] = useState('');
  const [as, setAs] = useState('viewer');
  const [error, setError] = useState('');
  const [url, setUrl] = useState('');
  const roles = order.slice(0, order.indexOf(role) + 1).filter(r => r !== 'owner').map(value => ({ value, label: roleNames[value] }));
  if (url) return <div className="acct-confirm">
    <p className="acct-muted">Send this link to {email} yourself. It works once, for 7 days.</p>
    <div className="acct-link"><LinkIcon /><code>{url}</code></div>
    <div className="acct-actions"><Button onClick={() => navigator.clipboard?.writeText(url)}>Copy</Button><Button variant="primary" onClick={onDone}>Done</Button></div>
  </div>;
  return <form className="acct-confirm" onSubmit={async e => { e.preventDefault(); try { setUrl((await api.invite(email.trim(), as)).url); } catch (err) { setError(err.fields?.email ?? err.message); } }}>
    <FormField label="Email" htmlFor="acct-invite" error={error}><TextInput id="acct-invite" type="email" value={email} onChange={e => { setEmail(e.target.value); setError(''); }} placeholder="sam@example.com" autoFocus /></FormField>
    <FormField label="Role" htmlFor="acct-invite-role"><Select id="acct-invite-role" label="Role" value={as} onChange={setAs} options={roles} /></FormField>
    <div className="acct-actions"><Button type="button" onClick={onDone}>Cancel</Button><Button type="submit" variant="primary" disabled={!email.trim()}>Create invite link</Button></div>
  </form>;
}

import { useState } from 'react';
import { ArrowPathIcon, EyeIcon, EyeSlashIcon, KeyIcon, LinkIcon, PlusIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Avatar, Button, Dialog, Notice, Select, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { useSession } from '../../../app/AuthGate';
import { CopyButton } from './shared';
import './Operations.css';

/*
 * Access from outside the dashboard: a deploy hook (a secret URL that deploys when POSTed to, e.g. a
 * GitHub push webhook) and tokens that only work for this resource. People's access comes from their
 * role, which applies to every resource; it's shown here so it's clear who can do what.
 */

const ROLE = { owner: 'Owner', admin: 'Admin', developer: 'Developer', viewer: 'Viewer' };
const CAN = { manage: 'Change settings, secrets and access', deploy: 'Deploy, restart, roll back and run tasks', view: 'See status, logs and metrics' };
const SCOPES = [{ value: 'deploy', label: 'Deploy', description: 'Read, deploy and cancel deployments' }, { value: 'read', label: 'Read only', description: 'Status, deployments and logs' }];
const EXPIRY = [{ value: '30', label: '30 days' }, { value: '90', label: '90 days' }, { value: '365', label: '1 year' }, { value: '0', label: 'Never' }];
const masked = (apiBase, hint) => `${apiBase}/hooks/igh_${'•'.repeat(20)}${hint}`;

export function AccessSecrets({ resource }) {
  const { user } = useSession();
  const canEdit = user.role !== 'viewer';
  const { data, error, refetch, setData } = useQuery(() => api.access(resource.id), [resource.id]);
  const [hookUrl, setHookUrl] = useState('');
  const [shown, setShown] = useState(false);
  const [confirm, setConfirm] = useState(null); // 'regenerate' | 'delete-hook' | token
  const [created, setCreated] = useState(null);
  const [form, setForm] = useState({ name: '', scope: 'deploy', expires: '90' });
  const [formErrors, setFormErrors] = useState({});
  const [failure, setFailure] = useState('');

  if (error && !data) return <div className="rp-page"><Notice tone="danger" title="Couldn't load access">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice></div>;
  if (!data) return <div className="rp-page"><p className="ops-muted">Loading…</p></div>;

  const { hook, tokens, people } = data;
  const act = async fn => { setFailure(''); try { await fn(); } catch (err) { setFailure(err.message); } };
  const reveal = async () => { if (!hookUrl) setHookUrl((await api.revealHook(resource.id)).url); setShown(s => !s); };
  const createHook = () => act(async () => { const { url } = await api.createHook(resource.id); setHookUrl(url); setShown(true); setConfirm(null); refetch(); });
  const endpoint = `${data.apiBase}/resources/${resource.id}`;

  async function createToken(event) {
    event.preventDefault();
    setFormErrors({});
    try {
      const out = await api.createResourceToken(resource.id, { name: form.name, scope: form.scope, expiresInDays: Number(form.expires) });
      setCreated({ ...out.item, value: out.token });
      setData(d => ({ ...d, tokens: [...d.tokens, out.item] }));
      setForm(f => ({ ...f, name: '' }));
    } catch (err) {
      setFormErrors(err.fields ?? {});
      if (!Object.keys(err.fields ?? {}).length) setFailure(err.message);
    }
  }

  return <div className="settings-page rp-settings ops-page">
    {failure && <Notice tone="danger" title="That didn't work" onDismiss={() => setFailure('')}>{failure}</Notice>}

    <section className="settings-group">
      <h2>Deploy hook</h2>
      <p>POST to this URL to deploy {resource.name}, from CI or as a {resource.sourceType === 'git' ? 'GitHub or GitLab push webhook' : 'registry webhook'}. Treat it like a password.</p>
      <div className="settings-card">
        {hook ? <>
          <div className="hook-row">
            <LinkIcon className="ops-row-icon" />
            <code className={cx('mono', !shown && 'masked')}>{shown && hookUrl ? hookUrl : masked(data.apiBase, hook.hint)}</code>
            {canEdit && <button type="button" className="icon-action" aria-label={shown ? 'Hide hook URL' : 'Show hook URL'} title={shown ? 'Hide' : 'Show'} onClick={() => act(reveal)}>{shown ? <EyeSlashIcon /> : <EyeIcon />}</button>}
            {canEdit && hookUrl && <CopyButton value={hookUrl} label="Copy hook URL" />}
          </div>
          <div className="ops-hook-meta">
            <span>Created {timeAgo(hook.createdAt).toLowerCase()}{hook.createdBy && ` by ${hook.createdBy}`} · {hook.lastUsedAt ? `last used ${timeAgo(hook.lastUsedAt).toLowerCase()}${hook.lastUsedIp ? ` from ${hook.lastUsedIp}` : ''}` : 'never used'}</span>
            {canEdit && <span className="ops-hook-actions">
              <Button icon={ArrowPathIcon} onClick={() => setConfirm('regenerate')}>Regenerate</Button>
              <Button className="danger-text" onClick={() => setConfirm('delete-hook')}>Remove</Button>
            </span>}
          </div>
          <div className="ops-howto">
            {resource.sourceType === 'git'
              ? <p>As a webhook: add the URL in your repository's webhook settings with content type <code>application/json</code> and push events. Pushes to <code>{data.branch}</code> deploy; other branches and tags are ignored, and a push while a deployment is still waiting joins it.</p>
              : <p>Each call pulls <code>{resource.image || 'the image'}</code> again and deploys it, e.g. from your registry's webhook after a push.</p>}
            <p>From CI: <code>curl -fsS -X POST {shown && hookUrl ? hookUrl : '<hook URL>'}</code></p>
          </div>
        </> : <div className="ops-empty-row">
          <span>No deploy hook. {canEdit ? 'Create one to deploy from CI or on every push.' : ''}</span>
          {canEdit && <Button icon={LinkIcon} onClick={createHook}>Create deploy hook</Button>}
        </div>}
      </div>
    </section>

    <section className="settings-group">
      <h2>Tokens</h2>
      <p>For scripts and CI. A token only works for {resource.name}: reading its status, deployments and logs, and with Deploy also deploying. Send it as <code>Authorization: Bearer …</code>.</p>
      {created && <Notice tone="success" title={`Token "${created.name}" created. Copy it now; it won't be shown again.`} onDismiss={() => setCreated(null)}>
        <span className="ops-token-reveal"><code className="mono token-value">{created.value}</code><CopyButton value={created.value} label="Copy token" /></span>
        <code className="ops-snippet">{created.scope === 'deploy'
          ? `curl -fsS -X POST -H "Authorization: Bearer ${created.value}" ${endpoint}/deployments`
          : `curl -fsS -H "Authorization: Bearer ${created.value}" ${endpoint}`}</code>
      </Notice>}
      <div className="rp-card">
        {tokens.length === 0 && <div className="ops-empty-row"><span>No tokens yet.</span></div>}
        {tokens.map(t => {
          const expired = t.expiresAt && new Date(t.expiresAt) < new Date();
          return <div key={t.id} className={cx('rp-row', expired && 'ops-unavailable')}>
            <KeyIcon className="volume-icon" />
            <span className="rp-row-main">
              <strong>{t.name}<span className="rp-chip">{t.scope === 'deploy' ? 'Deploy' : 'Read only'}</span>{expired && <span className="rp-chip">Expired</span>}</strong>
              <span>ign_…{t.hint} · created {timeAgo(t.createdAt).toLowerCase()}{t.createdBy && ` by ${t.createdBy}`} · {t.lastUsedAt ? `used ${timeAgo(t.lastUsedAt).toLowerCase()}` : 'never used'}{t.expiresAt && !expired && ` · expires ${new Date(t.expiresAt).toLocaleDateString()}`}</span>
            </span>
            {canEdit && <Button className="danger-text" onClick={() => setConfirm(t)}>Revoke</Button>}
          </div>;
        })}
        {canEdit && <form className="rp-row access-add ops-token-form" onSubmit={createToken}>
          <TextInput placeholder="Name, e.g. GitHub Actions" value={form.name} aria-invalid={!!formErrors.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} aria-label="Token name" maxLength={60} />
          <Select label="Token scope" options={SCOPES} value={form.scope} onChange={scope => setForm(f => ({ ...f, scope }))} className="role-select" />
          <Select label="Expires" options={EXPIRY.map(o => ({ ...o, label: o.value === '0' ? 'Never expires' : `Expires in ${o.label}` }))} value={form.expires} onChange={expires => setForm(f => ({ ...f, expires }))} className="role-select wide-ish" />
          <Button type="submit" icon={PlusIcon} disabled={!form.name.trim()}>Create token</Button>
        </form>}
        {formErrors.name && <p className="ops-error ops-pad">{formErrors.name}</p>}
      </div>
    </section>

    <section className="settings-group">
      <h2>People</h2>
      <p>Access comes from each person's role and applies to every resource. Roles are managed in Settings › Users & Permissions.</p>
      <div className="rp-card">
        {people.map(p => <div key={p.id} className={cx('rp-row', p.status === 'suspended' && 'ops-unavailable')}>
          <Avatar name={p.name || p.email} size={26} />
          <span className="rp-row-main"><strong>{p.name || p.email}{p.id === user.id && <span className="rp-chip">You</span>}{p.status === 'suspended' && <span className="rp-chip">Suspended</span>}</strong><span>{p.email}</span></span>
          <span className="ops-can" title={CAN[p.can]}>{CAN[p.can]}</span>
          <span className="rp-chip">{ROLE[p.role] ?? p.role}</span>
        </div>)}
      </div>
    </section>

    <Dialog open={!!confirm} onClose={() => setConfirm(null)}
      title={confirm === 'regenerate' ? 'Regenerate the deploy hook?' : confirm === 'delete-hook' ? 'Remove the deploy hook?' : `Revoke "${confirm?.name}"?`}
      description={confirm === 'regenerate' ? 'The current URL stops working right away. Update it wherever it\'s used.'
        : confirm === 'delete-hook' ? 'The URL stops working right away. Webhooks and CI jobs using it will get 404.'
          : 'Scripts using it get 401 from now on.'}
      footer={<><Button onClick={() => setConfirm(null)}>Cancel</Button><Button className="danger-solid" onClick={() => {
        if (confirm === 'regenerate') return createHook();
        if (confirm === 'delete-hook') return act(async () => { await api.deleteHook(resource.id); setHookUrl(''); setShown(false); setConfirm(null); refetch(); });
        const t = confirm;
        return act(async () => { await api.deleteResourceToken(t.id); setData(d => ({ ...d, tokens: d.tokens.filter(x => x.id !== t.id) })); setConfirm(null); });
      }}>{confirm === 'regenerate' ? 'Regenerate' : confirm === 'delete-hook' ? 'Remove' : 'Revoke'}</Button></>} />
  </div>;
}

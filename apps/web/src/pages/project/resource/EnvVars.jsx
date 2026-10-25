import { useEffect, useMemo, useRef, useState } from 'react';
import { ArrowDownTrayIcon, ArrowUpTrayIcon, DocumentDuplicateIcon, EllipsisHorizontalIcon, EyeIcon, EyeSlashIcon, LockClosedIcon, PlusIcon, RocketLaunchIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Avatar, Button, Menu, Notice, SearchField, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { useSession } from '../../../app/AuthGate';
import { SaveBar } from '../../settings/SettingsField';
import { CopyButton, PageIntro } from './shared';

/*
 * Variables are encrypted at rest. Secret values are never sent to the browser unless an admin
 * reveals one (audited). Unchanged secrets are kept server-side by sending { unchanged: true }.
 * Changes take effect on the next deployment.
 */

const NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
const looksSecret = key => /SECRET|PASSWORD|TOKEN|KEY|DSN|PRIVATE/i.test(key);
const toEnv = vars => vars.filter(v => !v.hidden).map(v => `${v.key}=${/\s/.test(v.value) ? `"${v.value}"` : v.value}`).join('\n');
const parseEnv = text => text.split(/\r?\n/).map(l => l.trim()).filter(l => l && !l.startsWith('#') && l.includes('='))
  .map(l => { const at = l.indexOf('='); return { key: l.slice(0, at).replace(/^export\s+/, '').trim(), value: l.slice(at + 1).trim().replace(/^["']|["']$/g, '') }; });

// Server rows → editable rows. `hidden` means we only know a secret exists, not its value.
const fromServer = list => list.map(v => ({ key: v.key, value: v.value ?? '', secret: v.secret, hidden: v.secret && v.value === undefined, by: v.updatedBy, at: v.updatedAt, original: true }));

export function EnvironmentVariables({ resource, environment }) {
  const { user } = useSession();
  const canReveal = user.role === 'admin' || user.role === 'owner';
  const canEdit = user.role !== 'viewer';
  const { data, error, loading, refetch } = useQuery(() => api.env(resource.id), [resource.id]);
  const [saved, setSaved] = useState([]);
  const [vars, setVars] = useState([]);
  useEffect(() => { if (data) { const rows = fromServer(data.variables); setSaved(rows); setVars(rows); } }, [data]);

  const [query, setQuery] = useState('');
  const [revealed, setRevealed] = useState(() => new Set());
  const [raw, setRaw] = useState(null);
  const [draft, setDraft] = useState({ key: '', value: '' });
  const [error2, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const [justSaved, setJustSaved] = useState(false);
  const [deploying, setDeploying] = useState(false);
  const fileRef = useRef(null);

  const visible = vars.filter(v => !query || v.key.toLowerCase().includes(query.toLowerCase()) || (!v.secret && v.value.toLowerCase().includes(query.toLowerCase())));
  const diff = useMemo(() => {
    const before = new Map(saved.map(v => [v.key, v]));
    const after = new Map(vars.map(v => [v.key, v]));
    return [
      ...vars.filter(v => !before.has(v.key)).map(v => ({ type: 'added', v })),
      ...vars.filter(v => before.has(v.key) && (!v.original || before.get(v.key).secret !== v.secret)).map(v => ({ type: 'changed', v })),
      ...saved.filter(v => !after.has(v.key)).map(v => ({ type: 'removed', v })),
    ];
  }, [saved, vars]);
  const problems = useMemo(() => {
    const names = vars.map(v => v.key);
    return [...vars.filter(v => !NAME_RE.test(v.key)).map(v => `"${v.key}" isn't a valid name.`), ...[...new Set(names.filter((n, i) => names.indexOf(n) !== i))].map(n => `${n} is defined twice.`)];
  }, [vars]);

  const update = (key, patch) => setVars(list => list.map(v => v.key === key ? { ...v, ...patch, original: false } : v));
  function add(key = draft.key.trim(), value = draft.value) {
    if (!NAME_RE.test(key)) return setError('Names start with a letter or underscore, then letters, numbers or underscores.');
    if (vars.some(v => v.key === key)) return setError(`${key} already exists.`);
    setVars(list => [...list, { key, value, secret: looksSecret(key), hidden: false, original: false }]);
    setDraft({ key: '', value: '' });
    setError('');
  }
  function merge(incoming) {
    setVars(list => [...list.map(v => { const hit = incoming.find(i => i.key === v.key); return hit ? { ...v, value: hit.value, hidden: false, original: false } : v; }),
      ...incoming.filter(i => !list.some(v => v.key === i.key)).map(i => ({ ...i, secret: looksSecret(i.key), hidden: false, original: false }))]);
  }
  async function reveal(key) {
    if (revealed.has(key)) return setRevealed(s => { const n = new Set(s); n.delete(key); return n; });
    const v = vars.find(x => x.key === key);
    if (v.hidden) {
      try {
        const { value } = await api.revealEnv(resource.id, key);
        setVars(list => list.map(x => x.key === key ? { ...x, value, hidden: false } : x));
        setSaved(list => list.map(x => x.key === key ? { ...x, value, hidden: false } : x));
      } catch (e) { return setError(e.message); }
    }
    setRevealed(s => new Set(s).add(key));
  }
  function exportPlain() {
    const url = URL.createObjectURL(new Blob([`# ${resource.name} · ${environment} · secrets excluded\n${toEnv(vars.filter(v => !v.secret))}\n`], { type: 'text/plain' }));
    Object.assign(document.createElement('a'), { href: url, download: `${resource.name}.${environment.toLowerCase()}.env` }).click();
    URL.revokeObjectURL(url);
  }
  async function save(event) {
    event.preventDefault();
    const current = raw === null ? vars : (() => { const parsed = parseEnv(raw); return parsed.map(p => ({ ...(vars.find(v => v.key === p.key) ?? { secret: looksSecret(p.key) }), ...p, hidden: false, original: false })); })();
    if (problems.length) return;
    setSaving(true);
    setError('');
    try {
      await api.putEnv(resource.id, current.map(v => v.hidden && v.original ? { key: v.key, secret: v.secret, unchanged: true } : { key: v.key, value: v.value, secret: v.secret }));
      setRaw(null);
      setJustSaved(true);
      await refetch();
    } catch (e) {
      setError(e.fields?.variables ?? e.message);
    } finally {
      setSaving(false);
    }
  }
  async function redeploy() {
    setDeploying(true);
    try { await api.deploy(resource.id, { reason: 'Apply environment variable changes' }); setJustSaved(false); } catch (e) { setError(e.message); } finally { setDeploying(false); }
  }

  if (loading && !data) return <div className="rp-page"><p className="rp-row-meta">Loading variables…</p></div>;
  if (error && !data) return <div className="rp-page"><Notice tone="danger" title="Couldn't load variables">{error.message}</Notice></div>;

  return <form className="rp-page env-page" onSubmit={save}>
    <PageIntro>Variables for <strong>{resource.name}</strong> in {environment}. Values are encrypted at rest; secrets stay hidden{canReveal ? ' unless you reveal them (recorded in the audit log)' : ''}. Changes apply on the next deployment.</PageIntro>
    {justSaved && <Notice tone="success" title="Saved">The running container still uses the old values. <button type="button" className="inline-link" disabled={deploying} onClick={redeploy}><RocketLaunchIcon style={{ width: 12, verticalAlign: -2 }} /> {deploying ? 'Starting deployment…' : 'Redeploy now to apply'}</button></Notice>}

    <div className="rp-toolbar">
      <SearchField value={query} onChange={setQuery} placeholder="Search variables" />
      <span className="spacer" />
      {canEdit && <Button type="button" onClick={() => setRaw(r => r === null ? toEnv(vars) : (merge(parseEnv(r)), null))}>{raw === null ? 'Edit as .env' : 'Edit as table'}</Button>}
      <Menu trigger={EllipsisHorizontalIcon} label="More" items={[
        ...(canEdit ? [{ label: 'Import .env file', icon: ArrowUpTrayIcon, onSelect: () => fileRef.current.click() }] : []),
        { label: 'Export non-secret values', icon: ArrowDownTrayIcon, onSelect: exportPlain },
      ]} />
      <input ref={fileRef} type="file" accept=".env,text/plain" hidden onChange={e => { e.target.files[0]?.text().then(t => merge(parseEnv(t))); e.target.value = ''; }} />
    </div>
    {raw !== null && vars.some(v => v.hidden) && <Notice tone="info" title="Hidden secrets aren't in this editor">They're kept as they are unless you add a line with the same name.</Notice>}

    {raw !== null
      ? <textarea className="env-raw" value={raw} onChange={e => setRaw(e.target.value)} spellCheck={false} aria-label=".env editor" rows={Math.max(8, raw.split('\n').length + 2)} />
      : <div className="rp-card env-table">
        <div className="env-head env-head-api" aria-hidden="true"><span>Name</span><span>Value</span><span>Last change</span><span /></div>
        {visible.map(v => <div key={v.key} className={cx('env-line env-line-api', !v.original && 'unsaved')}>
          <code className="env-var-name" title={v.key}>{v.key}{v.secret && <LockClosedIcon className="env-lock" aria-label="Secret" />}</code>
          <span className="env-value-cell">
            {v.hidden
              ? <span className="env-hidden">•••••••••••• {canEdit && <button type="button" className="inline-link" onClick={() => update(v.key, { hidden: false, value: '' })}>Replace</button>}</span>
              : <TextInput className="mono env-var-value" type={v.secret && !revealed.has(v.key) ? 'password' : 'text'} value={v.value} aria-label={`${v.key} value`} spellCheck={false} autoComplete="off" readOnly={!canEdit} onChange={e => update(v.key, { value: e.target.value })} />}
            {v.secret && (canReveal || !v.hidden) && <button type="button" className="icon-action" aria-label={revealed.has(v.key) ? `Hide ${v.key}` : `Reveal ${v.key}`} title={revealed.has(v.key) ? 'Hide' : 'Reveal'} onClick={() => reveal(v.key)}>{revealed.has(v.key) ? <EyeSlashIcon /> : <EyeIcon />}</button>}
          </span>
          <span className="env-by">{v.original ? <><Avatar name={v.by || '?'} size={18} /><span>{v.by || 'system'} · {timeAgo(v.at)}</span></> : <span>Unsaved</span>}</span>
          <span className="env-actions">
            {!v.hidden && <CopyButton value={v.value} label={`Copy ${v.key}`} />}
            {canEdit && <Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${v.key}`} items={[
              { label: v.secret ? 'Mark as plain value' : 'Mark as secret', icon: LockClosedIcon, onSelect: () => update(v.key, { secret: !v.secret }) },
              ...(!v.hidden ? [{ label: 'Duplicate', icon: DocumentDuplicateIcon, onSelect: () => { let key = `${v.key}_COPY`; while (vars.some(x => x.key === key)) key += '_'; setVars(l => [...l, { ...v, key, original: false }]); } }] : []),
              'separator',
              { label: 'Delete', icon: TrashIcon, danger: true, onSelect: () => setVars(list => list.filter(x => x.key !== v.key)) },
            ]} />}
          </span>
        </div>)}
        {!visible.length && <p className="env-empty">{vars.length ? 'No variables match.' : 'No variables yet.'}</p>}
        {canEdit && <div className="env-line env-add">
          <TextInput className="mono" placeholder="NEW_VARIABLE" value={draft.key} aria-label="New variable name" spellCheck={false}
            onChange={e => { setDraft(d => ({ ...d, key: e.target.value })); setError(''); }}
            onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); add(); } }}
            onPaste={e => { const text = e.clipboardData.getData('text'); if (!text.includes('\n')) return; e.preventDefault(); merge(parseEnv(text)); }} />
          <TextInput className="mono" placeholder="value" value={draft.value} aria-label="New variable value" spellCheck={false} onChange={e => setDraft(d => ({ ...d, value: e.target.value }))} onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); add(); } }} />
          <Button type="button" icon={PlusIcon} onClick={() => add()}>Add</Button>
        </div>}
      </div>}
    {error2 && <p className="rp-error">{error2}</p>}
    {problems.length > 0 && <Notice tone="danger" title="Fix these before saving">{problems.join(' ')}</Notice>}

    {diff.length > 0 && <section>
      <h2 className="rp-heading">Pending changes</h2>
      <ul className="rp-card env-diff">
        {diff.map(({ type, v }) => <li key={type + v.key} className={type}><span className="env-diff-mark">{type === 'added' ? '+' : type === 'removed' ? '−' : '~'}</span><code>{v.key}</code><span>{type === 'removed' ? 'removed' : v.secret ? 'value hidden' : v.value}</span><em>Redeploy</em></li>)}
      </ul>
    </section>}

    <SaveBar dirty={(diff.length > 0 || raw !== null) && !saving} onDiscard={() => { setVars(saved); setRaw(null); }} summary={`${diff.length} ${diff.length === 1 ? 'change' : 'changes'} · applies on the next deployment`} />
  </form>;
}

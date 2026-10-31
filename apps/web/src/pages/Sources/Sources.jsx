import { useEffect, useMemo, useState } from 'react';
import {
  ArrowPathIcon, CheckIcon, CircleStackIcon, ClipboardDocumentIcon, CodeBracketIcon, CubeIcon, DocumentTextIcon, EllipsisHorizontalIcon, EyeIcon, KeyIcon, LinkIcon, PencilSquareIcon, PlusIcon, ServerIcon, TrashIcon,
} from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, GitHubIcon, GitLabIcon, Menu, Notice, SearchField, SegmentedControl, Select, Status, TextInput, cx } from '../../ui';
import { api } from '../../api/client';
import { timeAgo, useQuery } from '../../api/hooks';
import { useSession, can } from '../../app/AuthGate';
import './Sources.css';

/*
 * Instance-wide credentials for pulling code and images. Secrets are encrypted by the API and never sent back;
 * status is only ever what the last real connection test returned.
 */

const providers = {
  github: { kind: 'git', label: 'GitHub', icon: GitHubIcon, auths: ['token', 'ssh'], url: 'optional', urlLabel: 'GitHub Enterprise URL', urlPlaceholder: 'https://github.example.com', blurb: 'Personal access token or deploy key.' },
  gitlab: { kind: 'git', label: 'GitLab', icon: GitLabIcon, auths: ['token', 'ssh'], url: 'optional', urlLabel: 'Self-managed GitLab URL', urlPlaceholder: 'https://gitlab.example.com', blurb: 'gitlab.com or self-managed.' },
  bitbucket: { kind: 'git', label: 'Bitbucket', icon: CodeBracketIcon, auths: ['password', 'ssh'], blurb: 'App password or SSH key.' },
  git: { kind: 'git', label: 'Git over HTTPS or SSH', icon: LinkIcon, auths: ['none', 'token', 'password', 'ssh'], url: 'required', urlLabel: 'Repository URL', urlPlaceholder: 'https://git.example.com/team/app.git', blurb: 'Any Git server, one repository.' },
  dockerhub: { kind: 'registry', label: 'Docker Hub', icon: CubeIcon, auths: ['token', 'password', 'none'], blurb: 'Access token for private images and higher pull limits.' },
  ghcr: { kind: 'registry', label: 'GitHub Container Registry', icon: GitHubIcon, auths: ['token'], blurb: 'ghcr.io with a token that can read packages.' },
  'gitlab-registry': { kind: 'registry', label: 'GitLab Registry', icon: GitLabIcon, auths: ['token', 'password'], url: 'optional', urlLabel: 'Registry host', urlPlaceholder: 'registry.gitlab.com', blurb: 'registry.gitlab.com or self-managed.' },
  registry: { kind: 'registry', label: 'Private registry', icon: ServerIcon, auths: ['password', 'token', 'none'], url: 'required', urlLabel: 'Registry host', urlPlaceholder: 'registry.example.com:5000', blurb: 'Any self-hosted Docker registry.' },
};
const authLabels = { none: 'No authentication (public)', token: 'Access token', password: 'Username and password', ssh: 'SSH private key' };
const secretLabels = { token: 'Token', password: 'Password', ssh: 'Private key' };
const statusView = {
  connected: { tone: 'success', label: 'Connected' },
  failed: { tone: 'danger', label: 'Failed' },
  untested: { tone: 'neutral', label: 'Not tested' },
};
const needsUsername = (provider, auth) => auth !== 'none' && auth !== 'ssh' && (auth === 'password' || providers[provider].kind === 'registry' || provider === 'bitbucket');

export function Sources({ onOpenResource }) {
  const { user } = useSession();
  const isAdmin = can(user, 'sources.manage');
  const canTest = can(user, 'sources.use');
  const [filter, setFilter] = useState('all');
  const [query, setQuery] = useState('');
  const [debounced, setDebounced] = useState('');
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState(null);
  const [viewing, setViewing] = useState(null);
  const [deleting, setDeleting] = useState(null);
  const [feedback, setFeedback] = useState(null);
  const [testing, setTesting] = useState(() => new Set());
  useEffect(() => { const t = setTimeout(() => setDebounced(query.trim()), 250); return () => clearTimeout(t); }, [query]);

  const { data, error, loading, refetch } = useQuery(() => api.sources({ q: debounced }), [debounced], { pollMs: 15000 });
  const all = data?.sources ?? [];
  const rows = useMemo(() => all.filter(s => filter === 'all' || s.kind === filter), [all, filter]);
  const count = kind => all.filter(s => s.kind === kind).length;
  const filters = [{ value: 'all', label: `All ${all.length}` }, { value: 'git', label: `Git providers ${count('git')}` }, { value: 'registry', label: `Container registries ${count('registry')}` }];
  const connected = all.filter(s => s.status === 'connected').length;
  const attention = all.filter(s => s.status === 'failed').length;

  async function test(s) {
    setTesting(t => new Set(t).add(s.id));
    try {
      const { source } = await api.testSource(s.id);
      setFeedback(source.status === 'connected'
        ? { tone: 'success', title: `${source.name} is connected`, text: source.statusMessage }
        : { tone: 'danger', title: `${source.name} couldn't connect`, text: source.statusMessage });
    } catch (err) {
      setFeedback({ tone: 'danger', title: `Couldn't test ${s.name}`, text: err.message });
    }
    setTesting(t => { const n = new Set(t); n.delete(s.id); return n; });
    refetch();
  }

  const actions = s => [
    { label: 'View details', icon: EyeIcon, onSelect: () => setViewing(s) },
    ...(canTest ? [{ label: 'Test connection', icon: ArrowPathIcon, onSelect: () => test(s) }] : []),
    ...(isAdmin ? [
      { label: 'Edit', icon: PencilSquareIcon, onSelect: () => setEditing({ source: s }) },
      { label: s.authType === 'none' ? 'Add credentials' : 'Reconnect', icon: KeyIcon, onSelect: () => setEditing({ source: s, rotate: true }) },
      'separator',
      { label: 'Delete', icon: TrashIcon, danger: true, onSelect: () => setDeleting(s) },
    ] : []),
  ];

  return <div className="sources">
    <div className="sources-intro">
      <p>Manage code repositories and container registries. Sources are shared across every project on this instance.</p>
      {isAdmin && <Button variant="primary" icon={PlusIcon} onClick={() => setAdding(true)}>Add source</Button>}
    </div>

    <div className="sources-summary" aria-label="Summary">
      <div><span>Total</span><strong>{all.length}</strong></div>
      <div><span>Connected</span><strong>{connected}</strong></div>
      <div className={cx(attention && 'attention')}><span>Needs attention</span><strong>{attention}</strong></div>
    </div>

    <div className="sources-toolbar">
      <SegmentedControl label="Source type" options={filters} value={filter} onChange={setFilter} className="sources-filter" />
      <SearchField value={query} onChange={setQuery} placeholder="Search name, provider or URL" className="sources-search" />
    </div>

    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}</Notice>}
    {error && !data && <Notice tone="danger" title="Couldn't load sources">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice>}

    <div className="sources-table-wrap">
      <table className="sources-table">
        <thead><tr><th>Name</th><th>Type</th><th>Provider</th><th>Status</th><th className="num">Linked resources</th><th>Last checked</th><th className="actions"><span className="visually-hidden">Actions</span></th></tr></thead>
        <tbody>
          {rows.map(s => {
            const p = providers[s.provider] ?? providers.git;
            const st = testing.has(s.id) ? { tone: 'progress', label: 'Testing…' } : statusView[s.status] ?? statusView.untested;
            return <tr key={s.id} onClick={() => setViewing(s)}>
              <td><span className="source-name"><p.icon />{s.name}<span>{s.url || (s.account && s.status === 'connected' ? s.account : '')}</span></span></td>
              <td className="muted">{s.kind === 'git' ? 'Git provider' : 'Container registry'}</td>
              <td className="muted">{p.label}</td>
              <td title={s.statusMessage || undefined}><Status tone={st.tone}>{st.label}</Status></td>
              <td className="num">{s.linkedResources}</td>
              <td className="muted">{s.lastCheckedAt ? timeAgo(s.lastCheckedAt) : 'Never'}</td>
              <td className="actions" onClick={e => e.stopPropagation()}><Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${s.name}`} items={actions(s)} /></td>
            </tr>;
          })}
        </tbody>
      </table>
      {!rows.length && <p className="sources-empty">{loading && !data ? 'Loading…' : debounced ? `No sources match “${debounced}”.`
        : all.length ? 'No sources of this type yet.' : isAdmin ? 'No sources yet. Add a Git provider or container registry to deploy private code and images.' : 'No sources yet. Ask an admin to add one.'}</p>}
    </div>

    <Definitions onOpenResource={onOpenResource} />

    {adding && <SourceDialog onClose={() => setAdding(false)} onSaved={s => { setAdding(false); savedFeedback(s, setFeedback); refetch(); }} />}
    {editing && <SourceDialog source={editing.source} rotate={editing.rotate} onClose={() => setEditing(null)} onSaved={s => { setEditing(null); savedFeedback(s, setFeedback); refetch(); }} />}
    {viewing && <DetailsDialog id={viewing.id} canTest={canTest} onTest={test} onClose={() => setViewing(null)} onOpenResource={r => { setViewing(null); onOpenResource(r); }} />}
    {deleting && <DeleteSourceDialog source={deleting} onClose={() => setDeleting(null)} onDeleted={() => { setFeedback({ tone: 'success', title: `${deleting.name} deleted` }); setDeleting(null); refetch(); }} />}
  </div>;
}

const savedFeedback = (s, set) => set(s.status === 'connected'
  ? { tone: 'success', title: `${s.name} saved and connected`, text: s.statusMessage }
  : { tone: 'warning', title: `${s.name} saved, but the connection test failed`, text: `${s.statusMessage} Fix it with Edit or Reconnect.` });

// Dockerfiles and build contexts come from the Git resources that use them.
function Definitions({ onOpenResource }) {
  const { data } = useQuery(() => api.allResources(), []);
  const builds = (data?.resources ?? []).filter(r => r.sourceType === 'git');
  return <section className="sources-section">
    <h2>Deployment definitions</h2>
    <p className="sources-note">Dockerfiles and build contexts used by Git-based resources. Docker Compose files aren't supported yet; deploy each service as its own resource.</p>
    {builds.length ? <ul className="definitions">
      {builds.map(r => <li key={r.id}>
        <DocumentTextIcon />
        <button type="button" className="inline-link" onClick={() => onOpenResource(r)}>{r.name}</button>
        <span className="mono">{(r.dockerfile || 'Dockerfile')} · {r.repoUrl.replace(/^https:\/\//, '')}@{r.branch}</span>
        <span className="muted">{r.projectName}</span>
      </li>)}
    </ul> : <p className="sources-empty small">No Git-based resources yet.</p>}
  </section>;
}

function SourceDialog({ source, rotate = false, onClose, onSaved }) {
  const editing = !!source;
  const [provider, setProvider] = useState(source?.provider ?? null);
  const p = provider && providers[provider];
  const [values, setValues] = useState(() => ({
    name: source?.name ?? '', url: source?.url ?? '', username: source?.username ?? '', secret: '',
    authType: source?.authType === 'none' && rotate ? (providers[source.provider].auths.find(a => a !== 'none')) : source?.authType ?? '',
  }));
  const [errors, setErrors] = useState({});
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState('');
  const auth = values.authType || p?.auths[0];
  const set = (key, value) => { setValues(v => ({ ...v, [key]: value })); setErrors(({ [key]: _, ...rest }) => rest); };
  const keepsSecret = editing && source.hasSecret && auth === source.authType;
  // SSH keys: generated by Ignition (recommended), pasted, or (when editing) kept as they are.
  const [keyMode, setKeyMode] = useState(keepsSecret && !rotate ? 'keep' : 'generate');
  const [generated, setGenerated] = useState(null);

  async function save(event) {
    event.preventDefault();
    setPending(true); setFailure('');
    try {
      const generate = auth === 'ssh' && keyMode === 'generate';
      const body = { name: values.name.trim(), url: values.url.trim(), username: values.username.trim(), authType: auth };
      if (generate) body.generateKey = true;
      else if (values.secret.trim()) body.secret = values.secret;
      const { source: saved } = editing ? await api.updateSource(source.id, body) : await api.createSource({ ...body, provider, secret: generate ? '' : values.secret });
      // A new key can't work until it's added to the provider, so show it before closing.
      if (generate) { setGenerated(saved); setPending(false); return; }
      onSaved(saved);
    } catch (err) {
      setErrors(err.fields ?? {});
      if (!Object.keys(err.fields ?? {}).length) setFailure(err.message);
      setPending(false);
    }
  }

  if (!p) {
    return <Dialog open onClose={onClose} title="Add source" description="Choose what to connect. You can add as many as you need." className="source-dialog">
      {['git', 'registry'].map(kind => <div key={kind} className="provider-group">
        <h3>{kind === 'git' ? 'Git providers' : 'Container registries'}</h3>
        <div className="provider-grid">
          {Object.entries(providers).filter(([, v]) => v.kind === kind).map(([key, v]) => <button key={key} type="button" className="provider-option" onClick={() => { setProvider(key); set('authType', v.auths[0]); }}>
            <v.icon /><span><strong>{v.label}</strong><span>{v.blurb}</span></span>
          </button>)}
        </div>
      </div>)}
    </Dialog>;
  }

  if (generated) return <DeployKeyStep source={generated} onDone={onSaved} />;

  const urlRequired = p.url === 'required' || (auth === 'ssh' && p.kind === 'git');
  const urlLabel = auth === 'ssh' ? 'Repository to test' : p.urlLabel;
  const urlPlaceholder = auth === 'ssh' ? 'git@github.com:owner/repo.git' : p.urlPlaceholder;
  const field = (key, label, control, hint) => <FormField label={label} htmlFor={`source-${key}`} error={errors[key]} hint={hint}>{control}</FormField>;

  return <Dialog open onClose={onClose} icon={p.icon} className="source-dialog"
    title={editing ? (rotate ? `Reconnect ${source.name}` : `Edit ${source.name}`) : `Connect ${p.label}`}
    description={editing ? 'Saving re-tests the connection.' : 'Ignition tests the connection when you save. Secrets are encrypted and never shown again.'}
    footer={<>
      {!editing && <Button onClick={() => setProvider(null)}>Back</Button>}
      <span className="dialog-spacer" />
      <Button onClick={onClose}>Cancel</Button>
      <Button variant="primary" type="submit" form="source-form" disabled={pending}>{pending ? 'Testing…' : editing ? 'Save and test' : 'Connect'}</Button>
    </>}>
    <form id="source-form" className="source-form" onSubmit={save} noValidate>
      {failure && <Notice tone="danger" title="Couldn't save">{failure}</Notice>}
      {field('name', 'Name', <TextInput id="source-name" value={values.name} autoFocus={!rotate} placeholder={`${p.label.split(' ')[0].toLowerCase()}-main`} maxLength={60} onChange={e => set('name', e.target.value)} />)}
      {p.auths.length > 1 && field('authType', 'Authentication',
        <Select id="source-authType" label="Authentication" value={auth} options={p.auths.map(a => ({ value: a, label: authLabels[a] }))} onChange={v => set('authType', v)} />)}
      {(p.url || auth === 'ssh') && field('url', urlRequired ? urlLabel : `${urlLabel} (optional)`,
        <TextInput id="source-url" className="mono" value={values.url} placeholder={urlPlaceholder} spellCheck={false} autoComplete="off" onChange={e => set('url', e.target.value)} />,
        auth === 'ssh' ? 'An SSH key is tested by reading this repository.' : p.url === 'optional' ? 'Leave empty for the hosted service.' : undefined)}
      {(needsUsername(provider, auth) || provider === 'ghcr' || (auth === 'token' && provider === 'git')) && field('username', provider === 'ghcr' ? 'GitHub username' : needsUsername(provider, auth) ? 'Username' : 'Username (optional)',
        <TextInput id="source-username" value={values.username} autoComplete="off" spellCheck={false} onChange={e => set('username', e.target.value)} />)}
      {auth === 'ssh' && field('keyMode', 'SSH key',
        <SegmentedControl label="SSH key" value={keyMode} onChange={setKeyMode}
          options={[{ value: 'generate', label: keepsSecret ? 'Generate new' : 'Generate for me' }, { value: 'paste', label: 'Paste my own' }, ...(keepsSecret ? [{ value: 'keep', label: 'Keep current' }] : [])]} />,
        keyMode === 'generate' ? 'Ignition creates an Ed25519 key pair. You add the public key to the repository next.' : undefined)}
      {auth !== 'none' && !(auth === 'ssh' && keyMode !== 'paste') && field('secret', keepsSecret ? `New ${secretLabels[auth].toLowerCase()}${rotate ? '' : ' (optional)'}` : secretLabels[auth],
        auth === 'ssh'
          ? <textarea id="source-secret" className="input mono source-key" rows={5} value={values.secret} autoFocus={rotate} spellCheck={false} placeholder={keepsSecret ? 'Saved — paste a new key to replace it' : '-----BEGIN OPENSSH PRIVATE KEY-----'} onChange={e => set('secret', e.target.value)} />
          : <TextInput id="source-secret" type="password" value={values.secret} autoFocus={rotate} autoComplete="new-password" placeholder={keepsSecret ? `Saved ${source.secretHint || '••••'} — enter a new one to rotate` : ''} onChange={e => set('secret', e.target.value)} />,
        keepsSecret ? 'Leave empty to keep the saved secret.' : auth === 'ssh' ? 'Use a read-only deploy key without a passphrase.' : 'Read-only access is enough.')}
    </form>
  </Dialog>;
}

const deployKeyHelp = {
  github: 'In the repository, open Settings › Deploy keys › Add deploy key and paste it. Leave write access off.',
  gitlab: 'In the project, open Settings › Repository › Deploy keys and paste it. Leave write access off.',
  bitbucket: 'In the repository, open Repository settings › Access keys › Add key and paste it.',
  git: 'Add it to the Git server as a read-only deploy key for this repository.',
};

function PublicKey({ source }) {
  const [copied, setCopied] = useState(false);
  async function copy() {
    try { await navigator.clipboard.writeText(source.publicKey); setCopied(true); setTimeout(() => setCopied(false), 1500); } catch { /* clipboard blocked: the key is still selectable */ }
  }
  return <div className="public-key">
    <div className="public-key-head"><span>Public key</span><Button icon={copied ? CheckIcon : ClipboardDocumentIcon} onClick={copy}>{copied ? 'Copied' : 'Copy'}</Button></div>
    <code onClick={e => getSelection().selectAllChildren(e.currentTarget)}>{source.publicKey}</code>
    <p className="sources-note">{deployKeyHelp[source.provider] ?? deployKeyHelp.git}</p>
  </div>;
}

function DeployKeyStep({ source: initial, onDone }) {
  const [source, setSource] = useState(initial);
  const [testing, setTesting] = useState(false);
  const connected = source.status === 'connected';
  async function test() {
    setTesting(true);
    try { setSource((await api.testSource(source.id)).source); } catch (err) { setSource(s => ({ ...s, status: 'failed', statusMessage: err.message })); }
    setTesting(false);
  }
  return <Dialog open onClose={() => onDone(source)} icon={KeyIcon} className="source-dialog" title="Add the deploy key"
    description="Ignition generated a key pair and encrypted the private key. Give the public key read access to the repository, then test."
    footer={<><Button onClick={() => onDone(source)}>{connected ? 'Done' : 'Finish later'}</Button><span className="dialog-spacer" />
      <Button variant="primary" icon={ArrowPathIcon} disabled={testing} onClick={test}>{testing ? 'Testing…' : 'Test connection'}</Button></>}>
    <div className="source-form">
      <PublicKey source={source} />
      {connected
        ? <Notice tone="success" title="Connected">Ignition can read {source.url}.</Notice>
        : source.status === 'failed' && source.lastCheckedAt && <Notice tone="warning" title="Not connected yet">{source.statusMessage}</Notice>}
    </div>
  </Dialog>;
}

function DetailsDialog({ id, canTest, onTest, onClose, onOpenResource }) {
  const { data, error, refetch } = useQuery(() => api.source(id), [id], { pollMs: 5000 });
  const [repos, setRepos] = useState(null);
  const s = data?.source;
  const p = s && (providers[s.provider] ?? providers.git);
  const st = s && (statusView[s.status] ?? statusView.untested);
  const listable = s && s.authType !== 'ssh' && ['github', 'gitlab', 'bitbucket'].includes(s.provider);
  async function loadRepos() {
    setRepos({ loading: true });
    try { setRepos({ list: (await api.sourceRepositories(id)).repositories }); } catch (err) { setRepos({ error: err.message }); }
  }
  return <Dialog open onClose={onClose} title={s?.name ?? 'Source'} icon={p?.icon ?? CircleStackIcon} description={p?.label} className="source-dialog"
    footer={<>{canTest && s && <Button icon={ArrowPathIcon} onClick={async () => { await onTest(s); refetch(); }}>Test connection</Button>}<span className="dialog-spacer" /><Button onClick={onClose}>Close</Button></>}>
    {error && <Notice tone="danger" title="Couldn't load source">{error.message}</Notice>}
    {s && <>
      <dl className="source-facts">
        <dt>Status</dt><dd><Status tone={st.tone}>{st.label}</Status>{s.statusMessage && <span className="muted"> {s.statusMessage.replace(/^Connected\s*/, '')}</span>}</dd>
        <dt>Last checked</dt><dd>{s.lastCheckedAt ? timeAgo(s.lastCheckedAt) : 'Never'}</dd>
        {s.url && <><dt>URL</dt><dd className="mono">{s.url}</dd></>}
        <dt>Authentication</dt><dd>{authLabels[s.authType]}{s.username && ` · ${s.username}`}</dd>
        {s.authType !== 'none' && <><dt>{s.authType === 'ssh' ? 'Private key' : 'Secret'}</dt><dd className="mono">{s.hasSecret ? `Encrypted ${s.secretHint || '••••••••'}` : 'Missing'}</dd></>}
        <dt>Added</dt><dd>{timeAgo(s.createdAt)}</dd>
      </dl>
      {s.publicKey && <PublicKey source={s} />}
      <h3 className="source-subhead">Used by {data.resources.length} {data.resources.length === 1 ? 'resource' : 'resources'}</h3>
      {data.resources.length ? <ul className="definitions">
        {data.resources.map(r => <li key={r.id}><CubeIcon /><button type="button" className="inline-link" onClick={() => onOpenResource(r)}>{r.name}</button><span className="muted">{r.projectName}</span></li>)}
      </ul> : <p className="sources-note">No resources use this source yet. Pick it under Credentials when adding a resource.</p>}
      {listable && <>
        <h3 className="source-subhead">Repositories</h3>
        {!repos && <Button onClick={loadRepos} disabled={s.status !== 'connected'}>Show repositories</Button>}
        {repos?.loading && <p className="sources-note">Loading…</p>}
        {repos?.error && <Notice tone="danger" title="Couldn't list repositories">{repos.error}</Notice>}
        {repos?.list && (repos.list.length ? <ul className="definitions repos">{repos.list.map(r => <li key={r.name}><CodeBracketIcon /><span className="mono">{r.name}</span><span className="muted">{r.private ? "private · " : ""}{r.defaultBranch}</span></li>)}</ul>
          : <p className="sources-note">This token can't see any repositories.</p>)}
      </>}
    </>}
  </Dialog>;
}

function DeleteSourceDialog({ source, onClose, onDeleted }) {
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState('');
  async function run() {
    setPending(true);
    try { await api.deleteSource(source.id); onDeleted(); } catch (err) { setFailure(err.message); setPending(false); }
  }
  return <Dialog open onClose={onClose} icon={TrashIcon} title={`Delete ${source.name}?`} description="The encrypted secret is destroyed. Resources must stop using this source first."
    footer={<><Button onClick={onClose}>Cancel</Button><Button className="danger" variant="primary" disabled={pending || source.linkedResources > 0} onClick={run}>Delete</Button></>}>
    {source.linkedResources > 0 && <Notice tone="warning" title="Still in use">{source.linkedResources} {source.linkedResources === 1 ? 'resource uses' : 'resources use'} this source. Remove or change them first.</Notice>}
    {failure && <Notice tone="danger" title="Couldn't delete">{failure}</Notice>}
  </Dialog>;
}

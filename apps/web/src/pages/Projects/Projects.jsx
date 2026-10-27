import { useEffect, useState } from 'react';
import { ArchiveBoxIcon, ArrowUturnLeftIcon, DocumentDuplicateIcon, EllipsisHorizontalIcon, ListBulletIcon, PencilSquareIcon, PlusIcon, Squares2X2Icon, StarIcon, TrashIcon, UserGroupIcon } from '@heroicons/react/16/solid';
import { Badge, Button, Dialog, FormField, Menu, Notice, SearchField, SegmentedControl, Select, Status, TextInput, cx } from '../../ui';
import { api } from '../../api/client';
import { timeAgo, useQuery } from '../../api/hooks';
import { projectOptions } from '../../app/createMenus';
import { Unsupported } from '../../app/Unsupported';
import { DuplicateProjectDialog, ImportComposeDialog, ImportGitDialog } from './ImportDialogs';
import './Projects.css';

const views = [
  { value: 'grid', label: 'Grid view', icon: Squares2X2Icon, iconOnly: true },
  { value: 'list', label: 'List view', icon: ListBulletIcon, iconOnly: true },
];

// Health is derived from the real statuses of a project's resources.
function health(p) {
  if (!p.resourceCount) return { tone: 'neutral', label: 'No resources yet' };
  if (p.deploying) return { tone: 'progress', label: `Deploying ${p.deploying}` };
  if (p.failed) return { tone: 'danger', label: `${p.failed} ${p.failed === 1 ? 'resource' : 'resources'} failing` };
  if (p.running === p.resourceCount) return { tone: 'success', label: 'All resources healthy' };
  return { tone: 'warning', label: `${p.resourceCount - p.running} stopped` };
}

function Environments({ list }) {
  const shown = list.slice(0, 2);
  const rest = list.length - shown.length;
  return <span className="env-badges">
    {shown.map(env => <Badge key={env.id} className={cx('env-badge', env.name === 'Production' && 'production')}>{env.name}</Badge>)}
    {rest > 0 && <Badge className="env-badge" title={list.slice(2).map(e => e.name).join(', ')}>+{rest}</Badge>}
  </span>;
}

function readView() {
  try { return localStorage.getItem('projects-view') || 'grid'; } catch { return 'grid'; }
}

export function Projects({ onOpen }) {
  const [query, setQuery] = useState('');
  const [debounced, setDebounced] = useState('');
  const [archived, setArchived] = useState(false);
  const [view, setView] = useState(readView);
  const [editing, setEditing] = useState(null);
  const [deleting, setDeleting] = useState(null);
  const [unsupported, setUnsupported] = useState(null);
  const [importing, setImporting] = useState(null); // { kind: 'git'|'compose'|'duplicate', provider?, project? }
  const [actionError, setActionError] = useState('');
  useEffect(() => { const t = setTimeout(() => setDebounced(query.trim()), 250); return () => clearTimeout(t); }, [query]);

  const { data, error, loading, refetch } = useQuery(() => api.projects({ q: debounced, archived }), [debounced, archived], { pollMs: 5000 });
  const rows = data?.projects ?? [];

  async function run(fn) {
    setActionError('');
    try { await fn(); await refetch(); } catch (e) { setActionError(e.message); }
  }

  const projectActions = p => [
    { label: p.favorite ? 'Remove from favorites' : 'Set as favorite', icon: StarIcon, onSelect: () => run(() => api.setFavorite(p.id, !p.favorite)) },
    'separator',
    { label: 'Edit project', icon: PencilSquareIcon, onSelect: () => setEditing(p) },
    { label: 'Duplicate project', icon: DocumentDuplicateIcon, onSelect: () => setImporting({ kind: 'duplicate', project: p }) },
    { label: 'Manage access', icon: UserGroupIcon, onSelect: () => setUnsupported({ title: 'Per-project access isn\'t available yet', body: 'Today every signed-in user can see all projects, limited by their instance role (viewer, developer, admin, owner).' }) },
    'separator',
    p.archived
      ? { label: 'Unarchive project', icon: ArrowUturnLeftIcon, onSelect: () => run(() => api.updateProject(p.id, { archived: false })) }
      : { label: 'Archive project', icon: ArchiveBoxIcon, onSelect: () => run(() => api.updateProject(p.id, { archived: true })) },
    { label: 'Delete project', icon: TrashIcon, danger: true, onSelect: () => setDeleting(p) },
  ];

  // Empty, Git, Compose and Duplicate are backed by the API; the rest explain why they're unavailable.
  const providerOf = { 'Continue with GitHub': 'github', 'Continue with GitLab': 'gitlab', 'Continue with other source': 'other' };
  const notYet = item => ({ ...item, items: undefined, onSelect: () => setUnsupported({ title: `${item.label} isn't available yet`, body: 'Create an empty project, then add resources from a Git repository or a container image.' }) });
  const newProjectMenu = projectOptions.map(item => {
    if (item === 'separator') return item;
    switch (item.label) {
      case 'Empty project': return { ...item, onSelect: () => setEditing({}) };
      case 'Import from Git repository': return { ...item, items: item.items.map(g => g === 'separator' ? g : providerOf[g.label]
        ? { ...g, onSelect: () => setImporting({ kind: 'git', provider: providerOf[g.label] }) }
        : { ...g, onSelect: () => setUnsupported({ title: `${g.label.replace('Continue with ', '')} isn't supported yet`, body: 'Import from GitHub, GitLab or any https/SSH Git URL instead.' }) }) };
      case 'From a Docker Compose file': return { ...item, onSelect: () => setImporting({ kind: 'compose' }) };
      case 'Duplicate an existing project': return { ...item, onSelect: () => setImporting({ kind: 'duplicate' }) };
      default: return notYet(item);
    }
  });
  const created = p => { setImporting(null); refetch(); onOpen(p); };

  function changeView(value) {
    setView(value);
    try { localStorage.setItem('projects-view', value); } catch {}
  }

  const menu = p => <span className="stop-click" onClick={event => event.stopPropagation()}><Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${p.name}`} items={projectActions(p)} /></span>;

  return <div className="projects">
    <div className="projects-toolbar">
      <SearchField value={query} onChange={setQuery} placeholder="Search projects" className="projects-search" />
      <Select label="Show" value={archived ? 'archived' : 'active'} onChange={v => setArchived(v === 'archived')} className="projects-scope" options={[{ value: 'active', label: 'Active projects' }, { value: 'archived', label: 'Archived' }]} />
      <SegmentedControl label="Layout" options={views} value={view} onChange={changeView} className="view-toggle" />
      <Menu className="button primary" label="New project" items={newProjectMenu}><PlusIcon />New project</Menu>
    </div>
    {actionError && <Notice tone="danger" title="That didn't work">{actionError}</Notice>}
    {error && !data && <Notice tone="danger" title="Couldn't load projects">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice>}

    {loading && !data ? <p className="projects-empty">Loading projects…</p>
      : rows.length === 0
        ? <div className="projects-empty">{debounced ? <>No projects match “{debounced}”.</> : archived ? 'No archived projects.' : <>No projects yet. <button type="button" className="inline-link" onClick={() => setEditing({})}>Create your first project</button></>}</div>
        : view === 'grid'
          ? <div className="project-grid">
            {rows.map(p => {
              const h = health(p);
              return <article key={p.id} className="project-card" onClick={() => onOpen(p)}>
                <header>
                  <h3>{p.name}{p.favorite && <StarIcon className="favorite-star" aria-label="Favorite" />}</h3>
                  {menu(p)}
                </header>
                <p className="project-description">{p.description || 'No description'}</p>
                <div className="project-meta"><span>{p.resourceCount} {p.resourceCount === 1 ? 'resource' : 'resources'}</span><Environments list={p.environments} /></div>
                <footer>
                  <Status tone={h.tone}>{h.label}</Status>
                  <span className="project-deployed">{p.lastDeployAt ? timeAgo(p.lastDeployAt) : 'Never deployed'}</span>
                </footer>
              </article>;
            })}
          </div>
          : <div className="project-table-wrap">
            <table className="project-table">
              <thead><tr><th>Project</th><th>Status</th><th>Environments</th><th className="num">Resources</th><th className="num">Last deployment</th><th className="actions"><span className="visually-hidden">Actions</span></th></tr></thead>
              <tbody>
                {rows.map(p => {
                  const h = health(p);
                  return <tr key={p.id} onClick={() => onOpen(p)}>
                    <td><span className="project-name"><strong>{p.name}</strong>{p.favorite && <StarIcon className="favorite-star" aria-label="Favorite" />}<span>{p.description}</span></span></td>
                    <td><Status tone={h.tone}>{h.label}</Status></td>
                    <td><Environments list={p.environments} /></td>
                    <td className="num">{p.resourceCount}</td>
                    <td className="num muted">{p.lastDeployAt ? timeAgo(p.lastDeployAt) : '—'}</td>
                    <td className="actions">{menu(p)}</td>
                  </tr>;
                })}
              </tbody>
            </table>
          </div>}

    {editing && <ProjectDialog project={editing} onClose={() => setEditing(null)} onSaved={p => { setEditing(null); refetch(); if (!editing.id) onOpen(p); }} />}
    <Dialog open={!!deleting} onClose={() => setDeleting(null)} title={`Delete ${deleting?.name}?`}
      description={deleting?.resourceCount ? `It still has ${deleting.resourceCount} resources. Delete them first so their containers and data aren't left running.` : 'This removes the project and its environments. It can\'t be undone.'}
      footer={<><Button onClick={() => setDeleting(null)}>Cancel</Button><Button className="danger-solid" disabled={!!deleting?.resourceCount} onClick={() => { const p = deleting; setDeleting(null); run(() => api.deleteProject(p.id)); }}>Delete project</Button></>} />
    <Unsupported info={unsupported} onClose={() => setUnsupported(null)} />
    {importing?.kind === 'git' && <ImportGitDialog provider={importing.provider} onClose={() => setImporting(null)} onCreated={created} />}
    {importing?.kind === 'compose' && <ImportComposeDialog onClose={() => setImporting(null)} onCreated={created} />}
    {importing?.kind === 'duplicate' && <DuplicateProjectDialog projects={rows.length || !archived ? rows : []} project={importing.project} onClose={() => setImporting(null)} onCreated={created} />}
  </div>;
}

function ProjectDialog({ project, onClose, onSaved }) {
  const editing = !!project.id;
  const [name, setName] = useState(project.name ?? '');
  const [description, setDescription] = useState(project.description ?? '');
  const [errors, setErrors] = useState({});
  const [pending, setPending] = useState(false);
  async function save() {
    setPending(true);
    try {
      const { project: saved } = editing ? await api.updateProject(project.id, { name, description }) : await api.createProject({ name, description });
      onSaved(saved);
    } catch (e) {
      setErrors(Object.keys(e.fields ?? {}).length ? e.fields : { name: e.message });
    } finally {
      setPending(false);
    }
  }
  return <Dialog open onClose={onClose} title={editing ? 'Edit project' : 'New project'} description={editing ? undefined : 'A project groups related resources. It starts with a Production environment.'}
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" onClick={save} disabled={pending || !name.trim()}>{pending ? 'Saving…' : editing ? 'Save' : 'Create project'}</Button></>}>
    <FormField label="Name" htmlFor="project-name" error={errors.name}><TextInput id="project-name" value={name} autoFocus onChange={e => { setName(e.target.value); setErrors({}); }} onKeyDown={e => e.key === 'Enter' && save()} /></FormField>
    <FormField label="Description" htmlFor="project-description" error={errors.description}><TextInput id="project-description" value={description} placeholder="Optional" onChange={e => setDescription(e.target.value)} /></FormField>
  </Dialog>;
}

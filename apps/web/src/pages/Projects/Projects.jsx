import { useMemo, useState } from 'react';
import { ArchiveBoxIcon, EllipsisHorizontalIcon, ListBulletIcon, PencilSquareIcon, PlusIcon, Squares2X2Icon, StarIcon, TrashIcon, UserGroupIcon } from '@heroicons/react/16/solid';
import { Badge, Button, Menu, SearchField, SegmentedControl, Status, cx } from '../../ui';
import { projects } from './data';
import './Projects.css';

const health = {
  healthy: { tone: 'success', label: () => 'All resources healthy' },
  degraded: { tone: 'warning', label: p => `${p.issues} resource needs attention` },
  failing: { tone: 'danger', label: p => `${p.issues} deployment failed` },
  deploying: { tone: 'progress', label: () => 'Deploying' },
  stopped: { tone: 'neutral', label: () => 'Stopped' },
};
const views = [
  { value: 'grid', label: 'Grid view', icon: Squares2X2Icon, iconOnly: true },
  { value: 'list', label: 'List view', icon: ListBulletIcon, iconOnly: true },
];
const projectActions = (favorite, toggleFavorite, open) => [
  { label: favorite ? 'Remove from favorites' : 'Set as favorite', icon: StarIcon, onSelect: toggleFavorite },
  'separator',
  { label: 'Edit project', icon: PencilSquareIcon, onSelect: open },
  { label: 'Manage access', icon: UserGroupIcon },
  'separator',
  { label: 'Archive project', icon: ArchiveBoxIcon },
  { label: 'Delete project', icon: TrashIcon, danger: true },
];

function Environments({ list }) {
  const shown = list.slice(0, 2);
  const rest = list.length - shown.length;
  return <span className="env-badges">
    {shown.map(env => <Badge key={env} className={cx('env-badge', env === 'Production' && 'production')}>{env}</Badge>)}
    {rest > 0 && <Badge className="env-badge" title={list.slice(2).join(', ')}>+{rest}</Badge>}
  </span>;
}

function readView() {
  try { return localStorage.getItem('projects-view') || 'grid'; } catch { return 'grid'; }
}

export function Projects({ onOpen }) {
  const [query, setQuery] = useState('');
  const [view, setView] = useState(readView);
  const [favorites, setFavorites] = useState(() => new Set());
  const toggleFavorite = name => setFavorites(current => {
    const next = new Set(current);
    next.has(name) ? next.delete(name) : next.add(name);
    return next;
  });
  const rows = useMemo(() => {
    const q = query.trim().toLowerCase();
    return projects
      .filter(p => !q || p.name.toLowerCase().includes(q) || p.description.toLowerCase().includes(q))
      .sort((a, b) => favorites.has(b.name) - favorites.has(a.name));
  }, [query, favorites]);

  function changeView(value) {
    setView(value);
    try { localStorage.setItem('projects-view', value); } catch {}
  }

  return <div className="projects">
    <div className="projects-toolbar">
      <SearchField value={query} onChange={setQuery} placeholder="Search projects" className="projects-search" />
      <SegmentedControl label="Layout" options={views} value={view} onChange={changeView} className="view-toggle" />
      <Button variant="primary" icon={PlusIcon}>New project</Button>
    </div>

    {rows.length === 0
      ? <p className="projects-empty">No projects match “{query}”.</p>
      : view === 'grid'
        ? <div className="project-grid">
          {rows.map(p => {
            const h = health[p.health];
            return <article key={p.name} className="project-card" onClick={() => onOpen(p)}>
              <header>
                <h3>{p.name}{favorites.has(p.name) && <StarIcon className="favorite-star" aria-label="Favorite" />}</h3>
                <span className="stop-click" onClick={event => event.stopPropagation()}><Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${p.name}`} items={projectActions(favorites.has(p.name), () => toggleFavorite(p.name), () => onOpen(p))} /></span>
              </header>
              <p className="project-description">{p.description}</p>
              <div className="project-meta"><span>{p.resources} resources</span><Environments list={p.environments} /></div>
              <footer>
                <Status tone={h.tone}>{h.label(p)}</Status>
                <span className="project-deployed">{p.lastDeploy}</span>
              </footer>
            </article>;
          })}
        </div>
        : <div className="project-table-wrap">
          <table className="project-table">
            <thead><tr><th>Project</th><th>Status</th><th>Environments</th><th className="num">Resources</th><th className="num">Last deployment</th><th className="actions"><span className="visually-hidden">Actions</span></th></tr></thead>
            <tbody>
              {rows.map(p => {
                const h = health[p.health];
                return <tr key={p.name} onClick={() => onOpen(p)}>
                  <td><span className="project-name"><strong>{p.name}</strong>{favorites.has(p.name) && <StarIcon className="favorite-star" aria-label="Favorite" />}<span>{p.description}</span></span></td>
                  <td><Status tone={h.tone}>{h.label(p)}</Status></td>
                  <td><Environments list={p.environments} /></td>
                  <td className="num">{p.resources}</td>
                  <td className="num muted">{p.lastDeploy}</td>
                  <td className="actions"><span className="stop-click" onClick={event => event.stopPropagation()}><Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${p.name}`} items={projectActions(favorites.has(p.name), () => toggleFavorite(p.name), () => onOpen(p))} /></span></td>
                </tr>;
              })}
            </tbody>
          </table>
        </div>}
  </div>;
}

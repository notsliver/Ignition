import { useEffect, useMemo, useState } from 'react';
import {
  ArrowPathIcon, ArrowTopRightOnSquareIcon, CircleStackIcon, ClockIcon, CogIcon, CubeIcon,
  DocumentTextIcon, EllipsisHorizontalIcon, PlayIcon, PlusIcon, StopIcon, TrashIcon,
} from '@heroicons/react/16/solid';
import { Button, Checkbox, Dialog, Menu, Notice, SearchField, SegmentedControl, Status, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { databaseOptions, deployOptions } from '../../../app/createMenus';
import { Unsupported } from '../../../app/Unsupported';
import { flows, unsupported } from './flows';
import { AddResourceDialog } from './AddResourceDialog';
import './Resources.css';

const groups = [
  { type: 'application', label: 'Applications', icon: CubeIcon },
  { type: 'database', label: 'Databases', icon: CircleStackIcon },
  { type: 'worker', label: 'Workers', icon: CogIcon },
];
export const resourceStatus = {
  created: { tone: 'neutral', label: 'Created' },
  deploying: { tone: 'progress', label: 'Deploying' },
  running: { tone: 'success', label: 'Running' },
  stopped: { tone: 'neutral', label: 'Stopped' },
  failed: { tone: 'danger', label: 'Failed' },
  removing: { tone: 'warning', label: 'Removing' },
};
export const sourceLabel = r => r.sourceType === 'git' ? r.repoUrl.replace(/^https:\/\//, '') : r.image;
// A working domain wins over the published host port.
export const publicUrl = (r, host = 'localhost') => r.domain || (r.hostPort ? `http://${host}:${r.hostPort}` : null);

// Attach onSelect to every leaf, passing its menu path (e.g. "Database/Create new database/Redis").
const withPaths = (items, onPick, prefix = '') => items.map(item => item === 'separator' ? item
  : item.items ? { ...item, items: withPaths(item.items, onPick, `${prefix}${item.label}/`) }
    : { ...item, onSelect: () => onPick(`${prefix}${item.label}`, item.label) });
// Database engines stay nested; "Import existing data" becomes one entry that explains it's unsupported.
const addMenu = [
  { label: 'Application', icon: CubeIcon, items: deployOptions },
  { label: 'Database', icon: CircleStackIcon, items: databaseOptions.map(o => o === 'separator' || o.label === 'Create new database' ? o : { ...o, items: undefined }) },
  { label: 'Worker', icon: CogIcon },
  { label: 'Scheduled job', icon: ClockIcon },
];

export function Resources({ project, environment, environmentId, onOpen }) {
  const [flowKey, setFlowKey] = useState(null);
  const [notice, setNotice] = useState(null);
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState('all');
  const [deleting, setDeleting] = useState(null);
  const [actionError, setActionError] = useState('');
  const [highlight, setHighlight] = useState(null);

  const { data, error, loading, refetch } = useQuery(() => api.resources(project.id, { environment: environmentId }), [project.id, environmentId], { pollMs: 3000 });
  const all = data?.resources ?? [];
  useEffect(() => { if (highlight) { const t = setTimeout(() => setHighlight(null), 2500); return () => clearTimeout(t); } }, [highlight]);

  const filters = [{ value: 'all', label: 'All' }, ...groups.filter(g => all.some(r => r.type === g.type)).map(g => ({ value: g.type, label: g.label }))];
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    return all.filter(r => (filter === 'all' || r.type === filter) && (!q || r.name.includes(q) || sourceLabel(r).toLowerCase().includes(q)));
  }, [all, filter, query]);

  const addOptions = useMemo(() => withPaths(addMenu, (path, label) => {
    if (flows[path]) setFlowKey(path);
    else setNotice({ title: `${label} isn't available yet`, body: unsupported[label] ?? 'This option isn\'t supported in this version.' });
  }), []);
  const add = <Menu className="button primary" label="Add resource" items={addOptions}><PlusIcon />Add resource</Menu>;

  async function create(payload) {
    const { resource } = await api.createResource(project.id, { ...payload, environmentId });
    setFlowKey(null);
    setFilter('all');
    setQuery('');
    setHighlight(resource.id);
    refetch();
  }

  async function run(fn) {
    setActionError('');
    try { await fn(); refetch(); } catch (e) { setActionError(e.message); }
  }

  const actions = r => {
    const url = publicUrl(r);
    const live = r.status === 'running';
    return [
      ...(url && r.type === 'application' ? [{ label: 'Open app', icon: ArrowTopRightOnSquareIcon, onSelect: () => window.open(url, '_blank', 'noopener') }, 'separator'] : []),
      { label: 'Redeploy', icon: ArrowPathIcon, onSelect: () => run(() => api.deploy(r.id, {})) },
      ...(r.status === 'stopped' ? [{ label: 'Start', icon: PlayIcon, onSelect: () => run(() => api.resourceAction(r.id, 'start')) }]
        : live ? [{ label: 'Restart', icon: ArrowPathIcon, onSelect: () => run(() => api.resourceAction(r.id, 'restart')) }, { label: 'Stop', icon: StopIcon, onSelect: () => run(() => api.resourceAction(r.id, 'stop')) }] : []),
      'separator',
      { label: 'View logs', icon: DocumentTextIcon, onSelect: () => onOpen(r, 'Logs') },
      'separator',
      { label: 'Delete', icon: TrashIcon, danger: true, onSelect: () => setDeleting(r) },
    ];
  };

  const dialog = flowKey && flows[flowKey] && <AddResourceDialog key={flowKey} flow={flows[flowKey]} projectName={project.name} environment={environment}
    takenNames={all.map(r => r.name)} onClose={() => setFlowKey(null)} onCreate={create} />;
  const shared = <>
    {dialog}
    <Unsupported info={notice} onClose={() => setNotice(null)} />
    {deleting && <DeleteDialog resource={deleting} onClose={() => setDeleting(null)} onDeleted={() => { setDeleting(null); refetch(); }} />}
  </>;

  if (loading && !data) return <div className="resources-page"><p className="resources-none">Loading resources…</p></div>;
  if (error && !data) return <div className="resources-page"><Notice tone="danger" title="Couldn't load resources">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice></div>;

  if (!all.length) return <div className="resources-page">
    <div className="resources-empty">
      <CubeIcon />
      <strong>No resources in {environment} yet</strong>
      <p>Deploy an application from Git or an image, or add a database.</p>
      {add}
    </div>
    {shared}
  </div>;

  return <div className="resources-page">
    <div className="resources-toolbar">
      {filters.length > 2 && <SegmentedControl label="Resource type" options={filters} value={filter} onChange={setFilter} className="resources-filter" />}
      <SearchField value={query} onChange={setQuery} placeholder="Search resources" className="resources-search" />
      {add}
    </div>
    {actionError && <Notice tone="danger" title="That didn't work">{actionError}</Notice>}

    {groups.map(group => {
      const rows = visible.filter(r => r.type === group.type);
      if (!rows.length) return null;
      const Icon = group.icon;
      return <section key={group.type} className="resource-group">
        <h2><Icon />{group.label}<span>{rows.length}</span></h2>
        <ul className="resource-list">
          {rows.map(r => {
            const status = resourceStatus[r.status] ?? resourceStatus.created;
            const url = publicUrl(r);
            return <li key={r.id} className={cx('resource-row', highlight === r.id && 'is-new')} role="button" tabIndex={0} aria-label={`Open ${r.name}`}
              onClick={() => onOpen(r)} onKeyDown={event => { if (event.target === event.currentTarget && (event.key === 'Enter' || event.key === ' ')) { event.preventDefault(); onOpen(r); } }}>
              <span className="resource-main">
                <strong>{r.name}</strong>
                <span className="resource-source">{sourceLabel(r)}{r.branch && <><span className="sep">·</span><code>{r.branch}</code></>}</span>
              </span>
              <Status tone={status.tone}>{status.label}</Status>
              <span className="resource-endpoint">
                {url ? <a href={url} target="_blank" rel="noreferrer" onClick={event => event.stopPropagation()}>{url.replace('http://', '')}</a>
                  : r.port ? <code>{r.name}:{r.port}</code> : '—'}
              </span>
              <span className="resource-usage">{r.memoryMb >= 1024 ? `${r.memoryMb / 1024} GB` : `${r.memoryMb} MB`}<span className="sep">·</span>{r.cpus} vCPU</span>
              <span className="resource-deployed">{r.lastDeployAt ? timeAgo(r.lastDeployAt) : '—'}</span>
              <span className="stop-click" onClick={event => event.stopPropagation()} onKeyDown={event => event.stopPropagation()}><Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${r.name}`} items={actions(r)} /></span>
            </li>;
          })}
        </ul>
      </section>;
    })}
    {!visible.length && <p className="resources-none">No resources match “{query}”.</p>}
    {shared}
  </div>;
}

export function DeleteDialog({ resource, onClose, onDeleted }) {
  const [confirm, setConfirm] = useState('');
  const [volumes, setVolumes] = useState(false);
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  const hasData = !!resource.dataPath;
  async function remove() {
    setPending(true);
    try {
      await api.deleteResource(resource.id, confirm, volumes);
      onDeleted();
    } catch (e) {
      setError(e.message);
      setPending(false);
    }
  }
  return <Dialog open onClose={onClose} title={`Delete ${resource.name}?`}
    description={`Its container is stopped and removed, along with its deployment history and variables.${hasData ? ' The data volume is kept unless you choose to delete it.' : ''}`}
    footer={<><Button onClick={onClose}>Cancel</Button><Button className="danger-solid" icon={TrashIcon} disabled={confirm !== resource.name || pending} onClick={remove}>{pending ? 'Deleting…' : 'Delete'}</Button></>}>
    {hasData && <label className="check-row"><Checkbox label="Also delete the data volume" checked={volumes} onChange={setVolumes} />Also delete the data volume ({resource.dataPath}). This can't be undone.</label>}
    <p className="confirm-hint">Type <code>{resource.name}</code> to confirm.</p>
    <TextInput value={confirm} onChange={e => setConfirm(e.target.value)} aria-label={`Type ${resource.name} to confirm`} autoComplete="off" autoFocus />
    {error && <p className="rp-error">{error}</p>}
  </Dialog>;
}

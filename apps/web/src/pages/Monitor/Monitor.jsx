import { useEffect, useMemo, useState } from 'react';
import {
  ArrowPathIcon, ArrowTopRightOnSquareIcon, ChartBarIcon, CheckCircleIcon, CircleStackIcon, ClockIcon, CogIcon, CommandLineIcon, CubeIcon,
  DocumentDuplicateIcon, DocumentTextIcon, EllipsisHorizontalIcon, PlayIcon, StopIcon, TrashIcon,
} from '@heroicons/react/16/solid';
import { Button, Checkbox, Dialog, Menu, MenuPopover, Notice, SearchField, SegmentedControl, Status, TextInput, cx } from '../../ui';
import { api } from '../../api/client';
import { useQuery } from '../../api/hooks';
import { Unsupported } from '../../app/Unsupported';
import { DeleteDialog, publicUrl, resourceStatus, sourceLabel } from '../project/Resources/Resources';
import './Monitor.css';

/*
 * A live inventory of every resource across active projects. Status comes from Docker via the worker;
 * CPU and memory are the newest 15-second sample (older than a minute shows as "—").
 */

const types = {
  application: { label: 'Application', plural: 'Applications', icon: CubeIcon },
  database: { label: 'Database', plural: 'Databases', icon: CircleStackIcon },
  worker: { label: 'Worker', plural: 'Workers', icon: CogIcon },
};
const mb = bytes => bytes >= 1 << 30 ? `${(bytes / (1 << 30)).toFixed(1)} GB` : `${Math.round(bytes / (1 << 20))} MB`;
function uptime(since) {
  if (!since) return '—';
  const s = Math.max(0, Math.floor((Date.now() - new Date(since)) / 1000));
  const d = Math.floor(s / 86400), h = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60);
  return d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : m ? `${m}m` : `${s}s`;
}

export function Monitor({ onOpenResource }) {
  const [filter, setFilter] = useState('all');
  const [query, setQuery] = useState('');
  const [debounced, setDebounced] = useState('');
  const [selected, setSelected] = useState(() => new Set());
  const [lastClicked, setLastClicked] = useState(null);
  const [contextMenu, setContextMenu] = useState(null);
  const [selectMode, setSelectMode] = useState(false);
  const [notice, setNotice] = useState(null);
  const [deleting, setDeleting] = useState(null);
  const [bulkDelete, setBulkDelete] = useState(false);
  const [feedback, setFeedback] = useState(null);
  useEffect(() => { const t = setTimeout(() => setDebounced(query.trim()), 250); return () => clearTimeout(t); }, [query]);

  const { data, error, loading, refetch } = useQuery(() => api.allResources({ q: debounced }), [debounced], { pollMs: 5000 });
  const all = data?.resources ?? [];
  const rows = useMemo(() => all.filter(r => filter === 'all' || r.type === filter), [all, filter]);
  const byId = useMemo(() => new Map(all.map(r => [r.id, r])), [all]);
  const counts = type => all.filter(r => r.type === type).length;
  const filters = [{ value: 'all', label: `All ${all.length}` }, ...Object.entries(types).filter(([t]) => counts(t)).map(([t, v]) => ({ value: t, label: `${v.plural} ${counts(t)}` }))];

  useEffect(() => { setSelected(new Set()); setLastClicked(null); }, [filter, debounced]);
  const selecting = selectMode || selected.size > 0;
  const exitSelection = () => { setSelected(new Set()); setSelectMode(false); };
  useEffect(() => {
    const onKey = event => { if (event.key === 'Escape' && !contextMenu) exitSelection(); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [contextMenu]);

  const allSelected = rows.length > 0 && rows.every(r => selected.has(r.id));
  const someSelected = selected.size > 0 && !allSelected;

  function toggle(id, { shift = false } = {}) {
    setSelected(current => {
      const next = new Set(current);
      if (shift && lastClicked) {
        const ids = rows.map(r => r.id);
        const [from, to] = [ids.indexOf(lastClicked), ids.indexOf(id)].sort((a, b) => a - b);
        ids.slice(from, to + 1).forEach(n => next.add(n));
      } else if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
    setLastClicked(id);
  }

  // Runs an action on one or many resources and reports partial failures honestly.
  async function runOn(list, label, fn) {
    setFeedback(null);
    if (!list.length) return;
    const results = await Promise.allSettled(list.map(fn));
    const failed = results.map((r, i) => r.status === 'rejected' ? `${list[i].name}: ${r.reason.message}` : null).filter(Boolean);
    setFeedback(failed.length
      ? { tone: failed.length === list.length ? 'danger' : 'warning', title: `${label} failed for ${failed.length} of ${list.length}`, text: failed.join(' · ') }
      : { tone: 'success', title: `${label} started for ${list.length} ${list.length === 1 ? 'resource' : 'resources'}` });
    refetch();
  }
  const action = (list, verb) => runOn(list, verb[0].toUpperCase() + verb.slice(1), r => api.resourceAction(r.id, verb));

  const actions = r => [
    ...(publicUrl(r) && r.type === 'application' ? [{ label: 'Open app', icon: ArrowTopRightOnSquareIcon, onSelect: () => window.open(publicUrl(r), '_blank', 'noopener') }, 'separator'] : []),
    ...(r.status === 'running' ? [{ label: 'Restart', icon: ArrowPathIcon, onSelect: () => action([r], 'restart') }] : []),
    r.status === 'stopped' ? { label: 'Start', icon: PlayIcon, onSelect: () => action([r], 'start') } : { label: 'Stop', icon: StopIcon, onSelect: () => action([r], 'stop') },
    { label: 'Redeploy', icon: ArrowPathIcon, onSelect: () => runOn([r], 'Redeploy', x => api.deploy(x.id, {})) },
    { label: 'Open terminal', icon: CommandLineIcon, onSelect: () => setNotice({ title: 'Terminal isn\'t available yet', body: `Until it is, use: docker exec -it $(docker ps -qf label=ignition.resource=${r.id}) sh` }) },
    'separator',
    { label: 'View logs', icon: DocumentTextIcon, onSelect: () => onOpenResource(r, 'Logs') },
    { label: 'View metrics', icon: ChartBarIcon, onSelect: () => onOpenResource(r, 'Metrics') },
    { label: 'Deployment history', icon: ClockIcon, onSelect: () => onOpenResource(r, 'Deployments') },
    'separator',
    { label: 'Duplicate', icon: DocumentDuplicateIcon, onSelect: () => setNotice({ title: 'Duplicating isn\'t available yet', body: 'Create a new resource with the same image or repository from the project\'s Resources page.' }) },
    { label: `Delete ${types[r.type]?.label.toLowerCase() ?? 'resource'}`, icon: TrashIcon, danger: true, onSelect: () => setDeleting(r) },
  ];
  const selectedRows = () => [...selected].map(id => byId.get(id)).filter(Boolean);
  const bulkActions = n => [
    { label: 'Restart', icon: ArrowPathIcon, onSelect: () => action(selectedRows().filter(r => r.status === 'running'), 'restart') },
    { label: 'Stop', icon: StopIcon, onSelect: () => action(selectedRows().filter(r => r.status === 'running'), 'stop') },
    { label: 'Start', icon: PlayIcon, onSelect: () => action(selectedRows().filter(r => r.status === 'stopped'), 'start') },
    'separator',
    { label: `Delete ${n} resources`, icon: TrashIcon, danger: true, onSelect: () => setBulkDelete(true) },
  ];

  function onRowClick(event, r) {
    if (event.shiftKey) { event.preventDefault(); toggle(r.id, { shift: true }); }
    else if (event.metaKey || event.ctrlKey || selecting) toggle(r.id);
    else onOpenResource(r, 'Overview');
  }
  function onRowContextMenu(event, r) {
    event.preventDefault();
    const inSelection = selected.has(r.id) && selected.size > 1;
    setContextMenu({
      anchor: { x: event.clientX, y: event.clientY },
      label: inSelection ? `Actions for ${selected.size} resources` : `Actions for ${r.name}`,
      items: inSelection ? bulkActions(selected.size) : actions(r),
      row: inSelection ? null : r.id,
    });
  }

  const running = selectedRows().filter(r => r.status === 'running').length;

  return <div className="monitor">
    {selecting
      ? <div className="monitor-toolbar bulk-bar" role="toolbar" aria-label="Selected resources">
        <Checkbox label="Select all" checked={allSelected} indeterminate={someSelected} onChange={() => setSelected(allSelected ? new Set() : new Set(rows.map(r => r.id)))} />
        <span className="bulk-count">{selected.size ? `${selected.size} selected` : 'Select resources'}</span>
        <div className="bulk-actions">
          <Button icon={ArrowPathIcon} disabled={!running} onClick={() => action(selectedRows().filter(r => r.status === 'running'), 'restart')}>Restart</Button>
          <Button icon={StopIcon} disabled={!running} onClick={() => action(selectedRows().filter(r => r.status === 'running'), 'stop')}>Stop</Button>
          <Button icon={TrashIcon} className="danger" disabled={!selected.size} onClick={() => setBulkDelete(true)}>Delete</Button>
          <Button onClick={exitSelection}>Done</Button>
        </div>
      </div>
      : <div className="monitor-toolbar">
        <SegmentedControl label="Resource type" options={filters} value={filter} onChange={setFilter} className="monitor-filter" />
        <div className="monitor-toolbar-end">
          <SearchField value={query} onChange={setQuery} placeholder="Search name, image or repository" className="monitor-search" />
          <Button icon={CheckCircleIcon} onClick={() => setSelectMode(true)} disabled={!all.length}>Select</Button>
        </div>
      </div>}

    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}</Notice>}
    {error && !data && <Notice tone="danger" title="Couldn't load resources">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice>}

    <div className="monitor-table-wrap">
      <table className={cx('monitor-table', selecting && 'selecting')}>
        <thead><tr>
          {selecting && <th className="select"><span className="visually-hidden">Selected</span></th>}
          <th>Name</th><th>Type</th><th>Status</th><th>Address</th><th className="num">CPU</th><th className="num">Memory</th><th className="num">Uptime</th><th className="actions"><span className="visually-hidden">Actions</span></th>
        </tr></thead>
        <tbody>
          {rows.map(r => {
            const { icon: TypeIcon, label } = types[r.type] ?? types.application;
            const status = resourceStatus[r.status] ?? resourceStatus.created;
            const isSelected = selected.has(r.id);
            const url = publicUrl(r);
            const live = r.status === 'running' && r.usage;
            return <tr key={r.id} className={cx(isSelected && 'selected', contextMenu?.row === r.id && 'context')} aria-selected={isSelected} onClick={event => onRowClick(event, r)} onContextMenu={event => onRowContextMenu(event, r)}>
              {selecting && <td className="select" onClick={e => e.stopPropagation()}><Checkbox label={`Select ${r.name}`} checked={isSelected} onChange={(_, native) => toggle(r.id, { shift: native.shiftKey })} /></td>}
              <td><span className="resource-name" title={sourceLabel(r)}><TypeIcon />{r.name}<span>{r.projectName}{r.environment !== 'Production' ? ` · ${r.environment}` : ''}</span></span></td>
              <td className="muted">{label}</td>
              <td><Status tone={status.tone}>{status.label}</Status></td>
              <td className="mono">{url ? <a href={url} target="_blank" rel="noreferrer" onClick={e => e.stopPropagation()}>{url.replace(/^https?:\/\//, '')}</a> : r.port ? `${r.name}:${r.port}` : 'internal'}</td>
              <td className="num" title={live ? `of ${r.cpus} vCPU limit` : undefined}>{live ? `${r.usage.cpuPercent.toFixed(1)}%` : '—'}</td>
              <td className="num" title={live ? `of ${r.memoryMb} MB limit` : undefined}>{live ? mb(r.usage.memoryBytes) : '—'}</td>
              <td className="num muted">{r.status === 'running' ? uptime(r.runningSince) : '—'}</td>
              <td className="actions" onClick={e => e.stopPropagation()} onContextMenu={event => event.stopPropagation()}><Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${r.name}`} items={actions(r)} /></td>
            </tr>;
          })}
        </tbody>
      </table>
      {!rows.length && <p className="monitor-empty">{loading && !data ? 'Loading…' : debounced ? `No resources match “${debounced}”.` : 'Nothing is running yet. Add a resource from a project to see it here.'}</p>}
    </div>
    <p className="monitor-footnote">CPU and memory are the latest 15-second sample. Archived projects aren't shown.</p>

    {contextMenu && <MenuPopover {...contextMenu} onClose={() => setContextMenu(null)} />}
    <Unsupported info={notice} onClose={() => setNotice(null)} />
    {deleting && <DeleteDialog resource={deleting} onClose={() => setDeleting(null)} onDeleted={() => { setDeleting(null); refetch(); }} />}
    {bulkDelete && <BulkDeleteDialog resources={selectedRows()} onClose={() => setBulkDelete(false)} onDone={result => { setBulkDelete(false); exitSelection(); setFeedback(result); refetch(); }} />}
  </div>;
}

function BulkDeleteDialog({ resources, onClose, onDone }) {
  const [confirm, setConfirm] = useState('');
  const [pending, setPending] = useState(false);
  const phrase = `delete ${resources.length}`;
  async function run() {
    setPending(true);
    const results = await Promise.allSettled(resources.map(r => api.deleteResource(r.id, r.name, false)));
    const failed = results.map((x, i) => x.status === 'rejected' ? `${resources[i].name}: ${x.reason.message}` : null).filter(Boolean);
    onDone(failed.length ? { tone: 'danger', title: `Couldn't delete ${failed.length} of ${resources.length}`, text: failed.join(' · ') } : { tone: 'success', title: `Deleting ${resources.length} resources` });
  }
  return <Dialog open onClose={onClose} title={`Delete ${resources.length} resources?`}
    description="Their containers are removed along with deployment history and variables. Data volumes are kept; delete them from each resource if you need to."
    footer={<><Button onClick={onClose}>Cancel</Button><Button className="danger-solid" icon={TrashIcon} disabled={confirm !== phrase || pending} onClick={run}>{pending ? 'Deleting…' : 'Delete'}</Button></>}>
    <ul className="bulk-delete-list">{resources.map(r => <li key={r.id}><strong>{r.name}</strong> <span>{r.projectName}</span></li>)}</ul>
    <p className="confirm-hint">Type <code>{phrase}</code> to confirm.</p>
    <TextInput value={confirm} onChange={e => setConfirm(e.target.value)} aria-label={`Type ${phrase} to confirm`} autoComplete="off" autoFocus />
  </Dialog>;
}

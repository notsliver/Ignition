import { useEffect, useMemo, useState } from 'react';
import { ArrowPathIcon, ChartBarIcon, CircleStackIcon, ClockIcon, CogIcon, CommandLineIcon, CubeIcon, DocumentDuplicateIcon, DocumentTextIcon, EllipsisHorizontalIcon, PlayIcon, StopIcon, TrashIcon, CheckCircleIcon } from '@heroicons/react/16/solid';
import { Button, Checkbox, Menu, MenuPopover, SearchField, SegmentedControl, Status, cx } from '../../ui';
import { resources } from './data';
import './Monitor.css';

const types = {
  application: { label: 'Application', icon: CubeIcon },
  database: { label: 'Database', icon: CircleStackIcon },
  service: { label: 'Service', icon: CogIcon },
};
const statuses = {
  running: { tone: 'success', label: 'Running' },
  deploying: { tone: 'progress', label: 'Deploying' },
  failed: { tone: 'danger', label: 'Failed' },
  stopped: { tone: 'neutral', label: 'Stopped' },
};
const count = type => resources.filter(r => r.type === type).length;
const filters = [
  { value: 'all', label: `All ${resources.length}` },
  { value: 'application', label: `Applications ${count('application')}` },
  { value: 'database', label: `Databases ${count('database')}` },
  { value: 'service', label: `Services ${count('service')}` },
];

const actions = resource => [
  { label: 'Restart', icon: ArrowPathIcon },
  resource.status === 'stopped' ? { label: 'Start', icon: PlayIcon } : { label: 'Stop', icon: StopIcon },
  { label: 'Open terminal', icon: CommandLineIcon },
  'separator',
  { label: 'View logs', icon: DocumentTextIcon },
  { label: 'View metrics', icon: ChartBarIcon },
  { label: 'Deployment history', icon: ClockIcon },
  'separator',
  { label: 'Duplicate', icon: DocumentDuplicateIcon },
  { label: `Delete ${types[resource.type].label.toLowerCase()}`, icon: TrashIcon, danger: true },
];

const bulkActions = count => [
  { label: 'Restart', icon: ArrowPathIcon },
  { label: 'Stop', icon: StopIcon },
  { label: 'Start', icon: PlayIcon },
  'separator',
  { label: `Delete ${count} resources`, icon: TrashIcon, danger: true },
];

export function Monitor() {
  const [filter, setFilter] = useState('all');
  const [query, setQuery] = useState('');
  const [selected, setSelected] = useState(() => new Set());
  const [lastClicked, setLastClicked] = useState(null);
  const [contextMenu, setContextMenu] = useState(null);
  const [selectMode, setSelectMode] = useState(false);

  const rows = useMemo(() => {
    const q = query.trim().toLowerCase();
    return resources.filter(r => (filter === 'all' || r.type === filter) && (!q || r.name.includes(q) || r.server.includes(q)));
  }, [filter, query]);

  useEffect(() => { setSelected(new Set()); setLastClicked(null); }, [filter, query]);
  const selecting = selectMode || selected.size > 0;
  const exitSelection = () => { setSelected(new Set()); setSelectMode(false); };
  useEffect(() => {
    const onKey = event => { if (event.key === 'Escape' && !contextMenu) exitSelection(); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [contextMenu]);

  const allSelected = rows.length > 0 && rows.every(r => selected.has(r.name));
  const someSelected = selected.size > 0 && !allSelected;

  function toggle(name, { shift = false, only = false } = {}) {
    setSelected(current => {
      const next = new Set(only ? [] : current);
      if (shift && lastClicked) {
        const names = rows.map(r => r.name);
        const [from, to] = [names.indexOf(lastClicked), names.indexOf(name)].sort((a, b) => a - b);
        names.slice(from, to + 1).forEach(n => next.add(n));
      } else if (next.has(name) && !only) next.delete(name);
      else next.add(name);
      return next;
    });
    setLastClicked(name);
  }

  function onRowClick(event, name) {
    if (event.shiftKey) { event.preventDefault(); toggle(name, { shift: true }); }
    else if (event.metaKey || event.ctrlKey || selecting) toggle(name);
  }

  function onRowContextMenu(event, resource) {
    event.preventDefault();
    const inSelection = selected.has(resource.name) && selected.size > 1;
    setContextMenu({
      anchor: { x: event.clientX, y: event.clientY },
      label: inSelection ? `Actions for ${selected.size} resources` : `Actions for ${resource.name}`,
      items: inSelection ? bulkActions(selected.size) : actions(resource),
      row: inSelection ? null : resource.name,
    });
  }

  return <div className="monitor">
    {selecting
      ? <div className="monitor-toolbar bulk-bar" role="toolbar" aria-label="Selected resources">
        <Checkbox label="Select all" checked={allSelected} indeterminate={someSelected} onChange={() => setSelected(allSelected ? new Set() : new Set(rows.map(r => r.name)))} />
        <span className="bulk-count">{selected.size ? `${selected.size} selected` : 'Select resources'}</span>
        <div className="bulk-actions">
          <Button icon={ArrowPathIcon} disabled={!selected.size}>Restart</Button>
          <Button icon={StopIcon} disabled={!selected.size}>Stop</Button>
          <Button icon={TrashIcon} className="danger" disabled={!selected.size}>Delete</Button>
          <Button onClick={exitSelection}>Done</Button>
        </div>
      </div>
      : <div className="monitor-toolbar">
        <SegmentedControl label="Resource type" options={filters} value={filter} onChange={setFilter} className="monitor-filter" />
        <div className="monitor-toolbar-end">
          <SearchField value={query} onChange={setQuery} placeholder="Search by name or server" className="monitor-search" />
          <Button icon={CheckCircleIcon} onClick={() => setSelectMode(true)}>Select</Button>
        </div>
      </div>}

    <div className="monitor-table-wrap">
      <table className={cx('monitor-table', selecting && 'selecting')}>
        <thead><tr>
          {selecting && <th className="select"><span className="visually-hidden">Selected</span></th>}
          <th>Name</th><th>Type</th><th>Status</th><th>Server</th><th className="num">CPU</th><th className="num">Memory</th><th className="num">Uptime</th><th className="actions"><span className="visually-hidden">Actions</span></th>
        </tr></thead>
        <tbody>
          {rows.map(r => {
            const { icon: TypeIcon, label } = types[r.type];
            const status = statuses[r.status];
            const isSelected = selected.has(r.name);
            return <tr key={r.name} className={cx(isSelected && 'selected', contextMenu?.row === r.name && 'context')} aria-selected={isSelected} onClick={event => onRowClick(event, r.name)} onContextMenu={event => onRowContextMenu(event, r)}>
              {selecting && <td className="select"><Checkbox label={`Select ${r.name}`} checked={isSelected} onChange={(_, native) => toggle(r.name, { shift: native.shiftKey })} /></td>}
              <td><span className="resource-name"><TypeIcon />{r.name}<span>{r.project}</span></span></td>
              <td className="muted">{label}</td>
              <td><Status tone={status.tone}>{status.label}</Status></td>
              <td className="mono">{r.server}</td>
              <td className="num">{r.status === 'running' || r.status === 'deploying' ? `${r.cpu.toFixed(1)}%` : '—'}</td>
              <td className="num">{r.memory === '0 MB' ? '—' : r.memory}</td>
              <td className="num muted">{r.uptime}</td>
              <td className="actions" onContextMenu={event => event.stopPropagation()}><Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${r.name}`} items={actions(r)} /></td>
            </tr>;
          })}
        </tbody>
      </table>
      {!rows.length && <p className="monitor-empty">No resources match “{query}”.</p>}
    </div>
    {contextMenu && <MenuPopover {...contextMenu} onClose={() => setContextMenu(null)} />}
  </div>;
}

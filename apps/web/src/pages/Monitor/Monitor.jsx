import { useMemo, useState } from 'react';
import { ArrowPathIcon, ChartBarIcon, CircleStackIcon, ClockIcon, CogIcon, CommandLineIcon, CubeIcon, DocumentDuplicateIcon, DocumentTextIcon, EllipsisHorizontalIcon, PlayIcon, StopIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Menu, SearchField, SegmentedControl, Status } from '../../ui';
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

export function Monitor() {
  const [filter, setFilter] = useState('all');
  const [query, setQuery] = useState('');
  const rows = useMemo(() => {
    const q = query.trim().toLowerCase();
    return resources.filter(r => (filter === 'all' || r.type === filter) && (!q || r.name.includes(q) || r.server.includes(q)));
  }, [filter, query]);

  return <div className="monitor">
    <div className="monitor-toolbar">
      <SegmentedControl label="Resource type" options={filters} value={filter} onChange={setFilter} className="monitor-filter" />
      <SearchField value={query} onChange={setQuery} placeholder="Search by name or server" className="monitor-search" />
    </div>

    <div className="monitor-table-wrap">
      <table className="monitor-table">
        <thead><tr><th>Name</th><th>Type</th><th>Status</th><th>Server</th><th className="num">CPU</th><th className="num">Memory</th><th className="num">Uptime</th><th className="actions"><span className="visually-hidden">Actions</span></th></tr></thead>
        <tbody>
          {rows.map(r => {
            const { icon: TypeIcon, label } = types[r.type];
            const status = statuses[r.status];
            return <tr key={r.name}>
              <td><span className="resource-name"><TypeIcon />{r.name}<span>{r.project}</span></span></td>
              <td className="muted">{label}</td>
              <td><Status tone={status.tone}>{status.label}</Status></td>
              <td className="mono">{r.server}</td>
              <td className="num">{r.status === 'running' || r.status === 'deploying' ? `${r.cpu.toFixed(1)}%` : '—'}</td>
              <td className="num">{r.memory === '0 MB' ? '—' : r.memory}</td>
              <td className="num muted">{r.uptime}</td>
              <td className="actions"><Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${r.name}`} items={actions(r)} /></td>
            </tr>;
          })}
        </tbody>
      </table>
      {!rows.length && <p className="monitor-empty">No resources match “{query}”.</p>}
    </div>
  </div>;
}

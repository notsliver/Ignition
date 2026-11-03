import { useEffect, useState } from 'react';
import { ChevronRightIcon, NoSymbolIcon } from '@heroicons/react/16/solid';
import { api } from '../../../api/client';
import { refreshFactor } from '../../../app/preferences';
import { ProductMark } from '../../admin/products/shared';
import { cx } from '../../../ui';
import '../../admin/products/products.css';
import './PanelList.css';

/*
 * Resources › the Overview layout (Preferences › Resources, or the instance default): one line per resource, the way
 * hosting panels list servers. Name and kind, its plan, where it runs, whether it's online, live
 * CPU and memory, and Manage.
 */

const KIND = { application: 'App', database: 'Database', worker: 'Worker', cron: 'Cron', function: 'Function', game: 'Game' };
const STATE = {
  running: { label: 'Online', tone: 'ok' }, deploying: { label: 'Starting', tone: 'busy' }, failed: { label: 'Failed', tone: 'bad' },
  stopped: { label: 'Offline', tone: 'off' }, created: { label: 'Not deployed', tone: 'off' }, removing: { label: 'Removing', tone: 'busy' },
};

function useLiveStats(resources) {
  const [stats, setStats] = useState({});
  const running = resources.filter(r => r.status === 'running').map(r => r.id).join(',');
  useEffect(() => {
    if (!running) return undefined;
    let stop = false;
    const load = () => Promise.all(running.split(',').map(id => api.stats(id).then(s => [id, s.available ? s.stats : null]).catch(() => [id, null])))
      .then(list => { if (!stop) setStats(Object.fromEntries(list)); });
    load();
    const t = setInterval(load, 10000 * refreshFactor());
    return () => { stop = true; clearInterval(t); };
  }, [running]);
  return stats;
}

// The same marks as Instance administration › Products: the product's own icon, a game's edition
// logo, or the kind's icon on its colour.
const KIND_OF = { application: 'application', database: 'database', worker: 'worker', cron: 'worker', function: 'function', game: 'game' };
function Mark({ resource }) {
  return <ProductMark size={34} kind={KIND_OF[resource.type] ?? 'application'}
    product={{ icon: resource.service?.icon, kind: KIND_OF[resource.type], template: { spec: { edition: resource.edition } } }} />;
}

export function PanelList({ resources, onOpen, sourceLabel }) {
  const stats = useLiveStats(resources);
  return <div className="pl-list">
    {resources.map(r => {
      const suspended = r.service?.status === 'suspended';
      const state = suspended ? { label: 'Suspended', tone: 'warn' } : STATE[r.status] ?? STATE.created;
      const s = stats[r.id];
      const ram = s?.memoryLimit ? Math.round((s.memoryBytes / s.memoryLimit) * 100) : null;
      return <div key={r.id} className={cx('pl-row', suspended && 'suspended')} role="button" tabIndex={0} aria-label={`Manage ${r.name}`}
        onClick={() => onOpen(r)} onKeyDown={e => { if (e.target === e.currentTarget && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); onOpen(r); } }}>
        <Mark resource={r} />
        <span className="pl-main">
          <span className="pl-name"><strong>{r.name}</strong><span className="pl-kind">{r.service?.product ?? KIND[r.type] ?? r.type}</span></span>
          <small>{r.service ? r.service.plan : sourceLabel(r)}</small>
        </span>
        <span className="pl-location">{r.location || '—'}</span>
        <span className={cx('pl-state', state.tone)} title={suspended ? r.service.suspendedReason || 'Suspended by your provider' : undefined}>
          {suspended ? <NoSymbolIcon /> : <span className="pl-dot" />}{state.label}</span>
        <span className="pl-usage">{s ? `CPU ${Math.round(s.cpuPercent)}% · RAM ${ram ?? '—'}%` : r.status === 'running' ? '…' : '—'}</span>
        <span className="pl-manage">Manage<ChevronRightIcon /></span>
      </div>;
    })}
  </div>;
}

import { useEffect, useMemo, useState } from 'react';
import {
  ArrowDownTrayIcon, ArrowPathIcon, BarsArrowDownIcon, BarsArrowUpIcon, BeakerIcon, CheckCircleIcon, ChevronLeftIcon, ChevronRightIcon,
  EllipsisHorizontalIcon, ListBulletIcon, NoSymbolIcon, PlayIcon, PlusIcon, ServerStackIcon, SignalIcon, TableCellsIcon, WrenchScrewdriverIcon,
} from '@heroicons/react/16/solid';
import { Button, Checkbox, Menu, MenuPopover, Meter, Notice, SearchField, SegmentedControl, Select, cx } from '../../ui';
import { api } from '../../api/client';
import { useQuery } from '../../api/hooks';
import { useSession } from '../../app/AuthGate';
import { AddServerDialog } from './AddServerDialog';
import { BulkDialog } from './BulkDialog';
import { download, isAdmin, pct } from './model';
import { Chips, ConnectionStatus, LastContact, RuntimeStatus, useFeedback, useServerActions } from './shared';

/*
 * Infrastructure › Servers. Everything is filtered, sorted and paged by the API so large
 * inventories stay fast. Utilisation columns only show samples that are current: an offline or
 * stale server shows "—" with the reason, never its last numbers as if they were live.
 */

const PAGE = 25;
const filters = [
  { value: 'all', label: 'All servers' },
  { value: 'online', label: 'Online' },
  { value: 'degraded', label: 'Degraded' },
  { value: 'offline', label: 'Offline' },
  { value: 'unknown', label: 'Unknown (stale)' },
  { value: 'pending', label: 'Pending enrollment' },
  { value: 'maintenance', label: 'In maintenance' },
  { value: 'deployable', label: 'Accepting deployments' },
  { value: 'blocked', label: 'Not accepting deployments' },
  { value: 'attention', label: 'Needs attention' },
];
const sorts = [
  { value: 'name', label: 'Name' }, { value: 'status', label: 'Status' }, { value: 'lastSeen', label: 'Last contact' }, { value: 'workloads', label: 'Workloads' },
  { value: 'cpu', label: 'CPU' }, { value: 'memory', label: 'Memory' }, { value: 'disk', label: 'Disk' },
];
const bulkMenu = [
  { action: 'test', label: 'Test connections', icon: SignalIcon },
  { action: 'refresh', label: 'Refresh runtime state', icon: ArrowPathIcon },
  { action: 'diagnostics', label: 'Run diagnostics', icon: BeakerIcon },
  { action: 'enable_deployments', label: 'Enable deployments', icon: PlayIcon },
  { action: 'disable_deployments', label: 'Disable deployments', icon: NoSymbolIcon },
  { action: 'enter_maintenance', label: 'Enter maintenance', icon: WrenchScrewdriverIcon },
];

function usagePrefs() {
  try { return localStorage.getItem('ignition:servers:view') === 'list' ? 'list' : 'table'; } catch { return 'table'; }
}

export function Servers({ onOpen }) {
  const { user } = useSession();
  const admin = isAdmin(user);
  const [query, setQuery] = useState('');
  const [debounced, setDebounced] = useState('');
  const [filter, setFilter] = useState('all');
  const [sort, setSort] = useState('name');
  const [desc, setDesc] = useState(false);
  const [page, setPage] = useState(0);
  const [layout, setLayout] = useState(usagePrefs);
  const [selected, setSelected] = useState(() => new Set());
  const [selectMode, setSelectMode] = useState(false);
  const [adding, setAdding] = useState(false);
  const [bulk, setBulk] = useState(null);
  const [contextMenu, setContextMenu] = useState(null);
  const [feedbackNode, setFeedback] = useFeedback();
  useEffect(() => { const t = setTimeout(() => setDebounced(query.trim()), 250); return () => clearTimeout(t); }, [query]);
  useEffect(() => { setPage(0); }, [debounced, filter, sort, desc]);
  useEffect(() => { try { localStorage.setItem('ignition:servers:view', layout); } catch { /* private mode */ } }, [layout]);

  const params = { q: debounced, filter, sort, dir: desc ? 'desc' : 'asc', limit: PAGE, offset: page * PAGE };
  const { data, error, loading, refetch } = useQuery(() => api.servers(params), [debounced, filter, sort, desc, page], { pollMs: 10000 });
  const rows = data?.servers ?? [];
  const total = data?.total ?? 0;
  const summary = data?.summary;
  const byId = useMemo(() => new Map(rows.map(s => [s.id, s])), [rows]);
  const selecting = selectMode || selected.size > 0;
  const allSelected = rows.length > 0 && rows.every(r => selected.has(r.id));

  function toggle(id) {
    setSelected(cur => { const next = new Set(cur); next.has(id) ? next.delete(id) : next.add(id); return next; });
  }
  function onRowClick(event, s) {
    if (event.metaKey || event.ctrlKey || selecting) toggle(s.id);
    else onOpen(s);
  }
  const sortBy = key => { if (sort === key) setDesc(d => !d); else { setSort(key); setDesc(['cpu', 'memory', 'disk', 'workloads', 'lastSeen'].includes(key)); } };
  const selectedServers = () => [...selected].map(id => byId.get(id)).filter(Boolean);
  const exit = () => { setSelected(new Set()); setSelectMode(false); };

  const isEmptyInventory = data && summary?.total === 0;
  const noMatches = data && !rows.length && !isEmptyInventory;

  return <div className="monitor sv-inventory">
    <div className="sv-intro">
      <p>Manage your infrastructure and deployment targets.</p>
      {admin && <Button variant="primary" icon={PlusIcon} onClick={() => setAdding(true)}>Add server</Button>}
    </div>

    <section className="sv-summary" aria-label="Summary">
      <button type="button" className={cx(filter === 'all' && 'active')} onClick={() => setFilter('all')}>
        <span>Total servers</span><strong>{summary ? summary.total : '—'}</strong><small>Registered with this IGNITION</small>
      </button>
      <button type="button" className={cx(filter === 'online' && 'active')} onClick={() => setFilter('online')} title="Reached by a check in the last minute, with a ready Docker Engine.">
        <span>Available</span><strong>{summary ? summary.available : '—'}</strong><small>Reached recently, Docker ready</small>
      </button>
      <button type="button" className={cx(filter === 'attention' && 'active', summary?.attention > 0 && 'attention')} onClick={() => setFilter('attention')}
        title="Offline, degraded, stale, or Docker not ready. Servers still enrolling or being removed aren't counted.">
        <span>Needs attention</span><strong>{summary ? summary.attention : '—'}</strong><small>{summary?.attention ? 'Offline, degraded, stale or Docker not ready' : 'Nothing to look at'}</small>
      </button>
    </section>

    {selecting
      ? <div className="monitor-toolbar bulk-bar" role="toolbar" aria-label="Selected servers">
        <Checkbox label="Select all on this page" checked={allSelected} indeterminate={selected.size > 0 && !allSelected} onChange={() => setSelected(allSelected ? new Set() : new Set(rows.map(r => r.id)))} />
        <span className="bulk-count">{selected.size ? `${selected.size} selected` : 'Select servers'}</span>
        <div className="bulk-actions">
          {admin && <Menu label="Bulk actions" items={[
            ...bulkMenu.map(b => ({ label: b.label, icon: b.icon, onSelect: () => setBulk(b) })),
            'separator',
            { label: 'Export diagnostics', icon: ArrowDownTrayIcon, onSelect: () => download(api.serversBundleUrl([...selected])) },
          ]} className="button"><CheckCircleIcon />Actions</Menu>}
          <Button onClick={exit}>Done</Button>
        </div>
      </div>
      : <div className="monitor-toolbar">
        <div className="sv-toolbar-start">
          <SearchField value={query} onChange={setQuery} placeholder="Search name, host, address, environment or label" className="monitor-search sv-search" />
          <Select label="Filter" options={filters} value={filter} onChange={setFilter} className="sv-select" />
        </div>
        <div className="monitor-toolbar-end">
          <Select label="Sort by" options={sorts.map(s => ({ ...s, label: `Sort: ${s.label}` }))} value={sort} onChange={setSort} className="sv-select" />
          <Button icon={desc ? BarsArrowDownIcon : BarsArrowUpIcon} aria-label={desc ? 'Descending' : 'Ascending'} title={desc ? 'Descending' : 'Ascending'} onClick={() => setDesc(d => !d)} />
          <SegmentedControl label="Layout" className="auto-width" value={layout} onChange={setLayout}
            options={[{ value: 'table', label: 'Table', icon: TableCellsIcon, iconOnly: true }, { value: 'list', label: 'Compact list', icon: ListBulletIcon, iconOnly: true }]} />
          {admin && <Button icon={CheckCircleIcon} onClick={() => setSelectMode(true)} disabled={!rows.length}>Select</Button>}
        </div>
      </div>}

    {feedbackNode}
    {error && <Notice tone="danger" title="Couldn't load servers" action="Retry" onAction={refetch}>{error.message}{data ? ' Showing the last loaded list.' : ''}</Notice>}

    {isEmptyInventory ? <div className="rp-empty">
      <ServerStackIcon />
      <strong>No servers registered</strong>
      <p>Add a Docker host to monitor it, inspect its containers and storage, and run diagnostics.</p>
      {admin && <Button variant="primary" icon={PlusIcon} onClick={() => setAdding(true)}>Add server</Button>}
    </div> : layout === 'table' ? <div className="monitor-table-wrap">
      <table className={cx('monitor-table', 'sv-table', selecting && 'selecting')}>
        <thead><tr>
          {selecting && <th className="select"><span className="visually-hidden">Selected</span></th>}
          <SortTh k="name" label="Server" {...{ sort, desc, sortBy }} />
          <th>Environment</th>
          <SortTh k="status" label="Connection" {...{ sort, desc, sortBy }} />
          <th>Docker</th>
          <SortTh k="workloads" label="Workloads" num {...{ sort, desc, sortBy }} />
          <SortTh k="cpu" label="CPU" {...{ sort, desc, sortBy }} />
          <SortTh k="memory" label="Memory" {...{ sort, desc, sortBy }} />
          <SortTh k="disk" label="Disk" {...{ sort, desc, sortBy }} />
          <SortTh k="lastSeen" label="Last contact" {...{ sort, desc, sortBy }} />
          <th className="actions"><span className="visually-hidden">Actions</span></th>
        </tr></thead>
        <tbody>
          {rows.map(s => <tr key={s.id} className={cx(selected.has(s.id) && 'selected', contextMenu?.row === s.id && 'context')} aria-selected={selected.has(s.id)}
            onClick={e => onRowClick(e, s)} onKeyDown={e => { if (e.key === 'Enter' && e.target === e.currentTarget) onOpen(s); }} tabIndex={0}
            onContextMenu={e => { e.preventDefault(); setContextMenu({ anchor: { x: e.clientX, y: e.clientY }, server: s, row: s.id }); }}>
            {selecting && <td className="select" onClick={e => e.stopPropagation()}><Checkbox label={`Select ${s.name}`} checked={selected.has(s.id)} onChange={() => toggle(s.id)} /></td>}
            <td><span className="resource-name sv-name"><ServerStackIcon />{s.name}<span>{s.isLocal ? 'This machine' : s.endpoint}</span></span></td>
            <td><Chips server={s} /></td>
            <td><ConnectionStatus server={s} /></td>
            <td><RuntimeStatus server={s} /></td>
            <td className="num">{s.workloads}</td>
            <td><Usage server={s} kind="cpu" /></td>
            <td><Usage server={s} kind="memory" /></td>
            <td><Usage server={s} kind="disk" /></td>
            <td className="muted"><LastContact server={s} /></td>
            <td className="actions" onClick={e => e.stopPropagation()} onContextMenu={e => e.stopPropagation()}><RowMenu server={s} onOpen={onOpen} onChanged={refetch} setFeedback={setFeedback} /></td>
          </tr>)}
        </tbody>
      </table>
      {!rows.length && <p className="monitor-empty">{loading && !data ? 'Loading servers…' : error && !data ? 'Servers couldn\'t be loaded.' : noMatches ? (debounced ? `No servers match “${debounced}”.` : 'No servers match this filter.') : ''}</p>}
    </div> : <ul className="sv-list">
      {rows.map(s => <li key={s.id} className={cx(selected.has(s.id) && 'selected')}>
        {selecting && <Checkbox label={`Select ${s.name}`} checked={selected.has(s.id)} onChange={() => toggle(s.id)} />}
        <button type="button" className="sv-list-main" onClick={e => onRowClick(e, s)}>
          <strong>{s.name}</strong>
          <span>{s.isLocal ? 'This machine' : s.endpoint} · {s.workloads} workload(s) · <LastContact server={s} /></span>
        </button>
        <span className="sv-list-status"><ConnectionStatus server={s} /><RuntimeStatus server={s} /></span>
        <RowMenu server={s} onOpen={onOpen} onChanged={refetch} setFeedback={setFeedback} />
      </li>)}
      {!rows.length && <li className="monitor-empty">{loading && !data ? 'Loading servers…' : debounced ? `No servers match “${debounced}”.` : 'No servers match this filter.'}</li>}
    </ul>}

    <div className="sv-pager">
      <span className="monitor-footnote">
        {total > 0 ? `${page * PAGE + 1}–${Math.min(total, (page + 1) * PAGE)} of ${total}` : ''}
        {data ? ` · Checked every ${data.checkIntervalSeconds} s; a server not checked for ${data.staleAfterSeconds} s shows as Unknown.` : ''}
      </span>
      {total > PAGE && <span className="sv-pager-buttons">
        <Button icon={ChevronLeftIcon} aria-label="Previous page" disabled={page === 0} onClick={() => setPage(p => p - 1)} />
        <Button icon={ChevronRightIcon} aria-label="Next page" disabled={(page + 1) * PAGE >= total} onClick={() => setPage(p => p + 1)} />
      </span>}
    </div>

    {contextMenu && <ContextMenu {...contextMenu} onOpen={onOpen} onChanged={refetch} setFeedback={setFeedback} onClose={() => setContextMenu(null)} />}
    {adding && <AddServerDialog onClose={() => setAdding(false)} onCreated={s => { setAdding(false); refetch(); onOpen(s); }} />}
    {bulk && <BulkDialog action={bulk} servers={selectedServers()} onClose={changed => { setBulk(null); if (changed) refetch(); }} />}
  </div>;
}

function SortTh({ k, label, num, sort, desc, sortBy }) {
  const active = sort === k;
  return <th className={cx(num && 'num')} aria-sort={active ? (desc ? 'descending' : 'ascending') : 'none'}>
    <button type="button" className={cx('sv-sort', active && 'active')} onClick={() => sortBy(k)}>{label}{active && (desc ? ' ↓' : ' ↑')}</button>
  </th>;
}

/** A utilisation cell that only shows a current sample, and says why when there isn't one. */
function Usage({ server: s, kind }) {
  const m = s.metrics;
  let value = null, title;
  if (m) {
    if (kind === 'cpu' && m.cpuPercent != null) value = Math.round(m.cpuPercent);
    if (kind === 'memory') value = pct(m.memoryUsed, m.memoryTotal);
    if (kind === 'disk') value = pct(m.diskUsed, m.diskTotal);
  }
  if (value == null) {
    title = !['online', 'degraded'].includes(s.connectionState) ? 'Not current: the server isn\'t reachable or its status is stale.'
      : !s.isLocal ? 'Not measured: the Docker API doesn\'t report host utilisation for remote servers.'
        : s.settings?.collectMetrics === false ? 'Collection is off in Settings.' : 'No sample yet.';
    return <span className="muted" title={title}>—</span>;
  }
  return <span className="sv-usage" title={`${value}% · sampled ${new Date(m.at).toLocaleTimeString()}`}><Meter label={kind} value={value} compact /></span>;
}

function RowMenu({ server, onOpen, onChanged, setFeedback }) {
  const actions = useServerActions(server, { onOpen, onChanged, setFeedback, onRemoved: () => { onChanged(); setFeedback({ tone: 'success', title: `${server.name} removed`, text: 'Its registration and stored credentials are gone. Nothing on the host was changed.' }); } });
  return <><Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${server.name}`} items={actions.items} />{actions.dialogs}</>;
}

function ContextMenu({ anchor, server, onOpen, onChanged, setFeedback, onClose }) {
  const actions = useServerActions(server, { onOpen, onChanged, setFeedback, onRemoved: onChanged });
  const [open, setOpen] = useState(true);
  // Stay mounted while a dialog opened from the menu is showing.
  useEffect(() => { if (!open && !actions.dialogs) onClose(); }, [open, actions.dialogs]); // eslint-disable-line react-hooks/exhaustive-deps
  return <>
    {open && <MenuPopover anchor={anchor} items={actions.items} label={`Actions for ${server.name}`} onClose={() => setOpen(false)} />}
    {actions.dialogs}
  </>;
}

import { useMemo, useState } from 'react';
import {
  ArrowDownTrayIcon, ArrowPathIcon, ArrowRightIcon, ArrowUturnLeftIcon, CheckCircleIcon, ChevronRightIcon, ClipboardDocumentIcon,
  DocumentDuplicateIcon, DocumentTextIcon, EllipsisHorizontalIcon, MinusCircleIcon, RocketLaunchIcon, StopCircleIcon, XCircleIcon,
} from '@heroicons/react/16/solid';
import { Avatar, BranchIcon, Button, CommitIcon, Dialog, FormField, Menu, Notice, SearchField, SegmentedControl, Status, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { duration, timeAgo, useQuery } from '../../../api/hooks';
import '../Deployments/Deployments.css';
import './ResourceDeployments.css';

// Pipeline steps as the worker logs them. Image deploys skip source/build; git ones skip pull.
const STEPS = [['source', 'Source fetched'], ['pull', 'Image pulled'], ['build', 'Build'], ['rollout', 'Rollout'], ['health', 'Health verification'], ['complete', 'Complete']];
const statusInfo = {
  queued: { tone: 'neutral', label: 'Queued' },
  building: { tone: 'warning', label: 'Building' },
  deploying: { tone: 'progress', label: 'Deploying' },
  successful: { tone: 'success', label: 'Successful' },
  failed: { tone: 'danger', label: 'Failed' },
  cancelled: { tone: 'neutral', label: 'Cancelled' },
};
const active = s => s === 'queued' || s === 'building' || s === 'deploying';
const triggerLabel = { manual: 'Manual', create: 'First deploy', redeploy: 'Redeploy', rollback: 'Rollback', retry: 'Retry', config: 'Config change', hook: 'Deploy hook', token: 'API token', automation: 'Automation' };
const statusTabs = [{ value: 'all', label: 'All' }, { value: 'active', label: 'In progress' }, { value: 'failed', label: 'Failed' }];

function Timeline({ d }) {
  const { data } = useQuery(() => api.deploymentLogs(d.id), [d.id], { pollMs: active(d.status) ? 1500 : 0 });
  const logs = data?.logs ?? [];
  const [open, setOpen] = useState(null);
  const byStep = useMemo(() => {
    const m = {};
    for (const l of logs) (m[l.step || 'other'] ??= []).push(l);
    return m;
  }, [logs]);
  const steps = [['queued', 'Queued'], ...STEPS.filter(([key]) => byStep[key] || (key !== 'pull' && key !== 'source' && key !== 'build')), ...(byStep.other ? [['other', 'Errors']] : [])];
  const lastStep = logs.at(-1)?.step;

  return <ol className="dep-timeline">
    {steps.map(([key, label]) => {
      const lines = key === 'queued' ? [] : byStep[key] ?? [];
      const failed = lines.some(l => l.level === 'error') || (key === 'other' && d.status === 'failed');
      const reached = key === 'queued' || lines.length > 0;
      const state = failed ? 'failed' : !reached ? (d.status === 'cancelled' ? 'cancelled' : 'pending') : active(d.status) && key === lastStep ? 'running' : 'done';
      const first = lines[0]?.createdAt, last = lines.at(-1)?.createdAt;
      const expandable = lines.length > 0;
      const expanded = open === key || (open === null && failed);
      return <li key={key} className={state}>
        <button type="button" className="dep-step" disabled={!expandable} aria-expanded={expanded} onClick={() => setOpen(expanded ? '' : key)}>
          <span className="dep-step-icon">{state === 'done' ? <CheckCircleIcon /> : state === 'failed' ? <XCircleIcon /> : state === 'cancelled' ? <MinusCircleIcon /> : state === 'running' ? <ArrowPathIcon className="spin" /> : <span className="dot" />}</span>
          <span className="dep-step-name">{label}</span>
          <span className="dep-step-time">{state === 'running' ? 'In progress' : first ? duration(first, last) : ''}</span>
          {expandable && <ChevronRightIcon className="dep-step-caret" />}
        </button>
        {expanded && expandable && <div className="dep-step-log"><pre>{lines.map(l => <span key={l.id} className={cx(l.level === 'error' && 'err')}>{l.message}{'\n'}</span>)}</pre></div>}
      </li>;
    })}
  </ol>;
}

export function ResourceDeployments({ resource }) {
  const { data, error, loading, refetch } = useQuery(() => api.deployments(resource.id, { limit: 100 }), [resource.id], { pollMs: 3000 });
  const list = data?.deployments ?? [];
  const [expanded, setExpanded] = useState(null);
  const [query, setQuery] = useState('');
  const [statusTab, setStatusTab] = useState('all');
  const [compare, setCompare] = useState([]);
  const [showCompare, setShowCompare] = useState(false);
  const [noteDialog, setNoteDialog] = useState(false);
  const [reason, setReason] = useState('');
  const [actionError, setActionError] = useState('');
  const openId = expanded ?? list.find(d => active(d.status) || d.status === 'failed')?.id;

  const rows = useMemo(() => {
    const q = query.trim().toLowerCase();
    return list.filter(d => (statusTab === 'all' || (statusTab === 'active' ? active(d.status) : d.status === statusTab))
      && (!q || [d.commit, d.reason, d.author, d.id, `#${d.number}`, d.imageRef].some(v => v?.toLowerCase().includes(q))));
  }, [list, query, statusTab]);

  async function run(fn) {
    setActionError('');
    try { const r = await fn(); await refetch(); if (r?.deploymentId) setExpanded(r.deploymentId); } catch (e) { setActionError(e.message); }
  }
  async function downloadLogs(d) {
    const { logs } = await api.deploymentLogs(d.id);
    const text = logs.map(l => `${l.createdAt} ${l.level.toUpperCase().padEnd(5)} [${l.step || '-'}] ${l.message}`).join('\n');
    const url = URL.createObjectURL(new Blob([text], { type: 'text/plain' }));
    Object.assign(document.createElement('a'), { href: url, download: `${resource.name}-deployment-${d.number}.log` }).click();
    URL.revokeObjectURL(url);
  }

  const actions = d => [
    { label: 'View details', icon: DocumentTextIcon, onSelect: () => setExpanded(d.id) },
    { label: compare.includes(d.id) ? 'Remove from comparison' : 'Compare…', icon: DocumentDuplicateIcon, onSelect: () => setCompare(c => c.includes(d.id) ? c.filter(x => x !== d.id) : [...c.slice(-1), d.id]) },
    'separator',
    ...(active(d.status) ? [{ label: 'Cancel deployment', icon: StopCircleIcon, danger: true, onSelect: () => run(() => api.cancelDeployment(d.id)) }] : []),
    ...(d.status === 'failed' ? [{ label: 'Retry', icon: ArrowPathIcon, onSelect: () => run(() => api.deploy(resource.id, { reason: `Retry of #${d.number}` })) }] : []),
    ...(!active(d.status) && d.imageRef ? [{ label: 'Redeploy this version', icon: RocketLaunchIcon, onSelect: () => run(() => api.deploy(resource.id, { from: d.id })) }] : []),
    ...(d.status === 'successful' && !d.current && d.imageRef ? [{ label: 'Roll back to this', icon: ArrowUturnLeftIcon, onSelect: () => run(() => api.deploy(resource.id, { from: d.id, reason: `Rollback to #${d.number}` })) }] : []),
    'separator',
    { label: 'Copy deployment ID', icon: ClipboardDocumentIcon, onSelect: () => navigator.clipboard?.writeText(d.id) },
    ...(d.commit ? [{ label: 'Copy commit SHA', icon: ClipboardDocumentIcon, onSelect: () => navigator.clipboard?.writeText(d.commit) }] : []),
    { label: 'Download logs', icon: ArrowDownTrayIcon, onSelect: () => downloadLogs(d) },
  ];

  const deployMenu = [
    { label: 'Deploy latest', icon: RocketLaunchIcon, onSelect: () => run(() => api.deploy(resource.id, {})) },
    { label: 'Deploy with a note…', icon: BranchIcon, onSelect: () => setNoteDialog(true) },
  ];
  const [a, b] = compare.map(id => list.find(d => d.id === id));

  return <div className="rp-page">
    <div className="deployments-toolbar">
      <SegmentedControl label="Status" options={statusTabs} value={statusTab} onChange={setStatusTab} className="deployments-status" />
      {compare.length > 0 && <Button onClick={() => compare.length === 2 && setShowCompare(true)} disabled={compare.length < 2}>{compare.length < 2 ? 'Pick one more to compare' : 'Compare'}</Button>}
      {compare.length > 0 && <Button onClick={() => setCompare([])}>Clear</Button>}
      <SearchField value={query} onChange={setQuery} placeholder="Search #, commit, author or image" className="deployments-search" />
      <Menu className="button primary" label="Deploy" items={deployMenu}><RocketLaunchIcon />Deploy</Menu>
    </div>
    {actionError && <Notice tone="danger" title="That didn't work">{actionError}</Notice>}
    {error && !data && <Notice tone="danger" title="Couldn't load deployments">{error.message}</Notice>}

    {rows.length ? <ol className="deployment-list">
      {rows.map(d => {
        const s = statusInfo[d.status];
        const open = openId === d.id;
        const timing = active(d.status) ? duration(d.startedAt ?? d.createdAt) : `${duration(d.startedAt, d.finishedAt)} (${timeAgo(d.createdAt)})`;
        return <li key={d.id} className={cx('deployment', open && 'open', compare.includes(d.id) && 'comparing')}>
          <div className="deployment-row" role="button" tabIndex={0} aria-expanded={open} onClick={() => setExpanded(open ? '' : d.id)}
            onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); setExpanded(open ? '' : d.id); } }}>
            <span className="deployment-id"><span>#{d.number}</span>{d.current && <span className="current-pill"><ArrowRightIcon />Current</span>}</span>
            <span className="deployment-env">{triggerLabel[d.trigger] ?? d.trigger}</span>
            <span className="deployment-status"><Status tone={s.tone}>{s.label}</Status><span className="deployment-timing">{timing}</span></span>
            <span className="deployment-ref"><BranchIcon /><span>{d.branch || '—'}</span></span>
            <span className="deployment-ref commit"><CommitIcon /><code>{d.commit || '—'}</code><span className="deployment-message">{d.error || d.reason || d.imageRef}</span></span>
            <span className="deployment-resource" title="Deployment ID">{d.id.slice(0, 8)}</span>
            <span className="deployment-author" title={`By ${d.author}`}><Avatar name={d.author} /></span>
            <span onClick={event => event.stopPropagation()} onKeyDown={event => event.stopPropagation()}>
              <Menu trigger={EllipsisHorizontalIcon} label={`Actions for #${d.number}`} items={actions(d)} />
            </span>
          </div>
          {open && <div className="deployment-details">
            <div className="dep-facts">
              <span><b>Image</b><code>{d.imageRef || 'not built yet'}</code></span>
              <span><b>By</b>{d.author}{d.reason && <> — “{d.reason}”</>}</span>
              {d.error && <span className="dep-error-fact"><b>Error</b>{d.error}</span>}
            </div>
            <Timeline key={`${d.id}:${d.status}`} d={d} />
          </div>}
        </li>;
      })}
    </ol> : <p className="deployments-none">{loading ? 'Loading deployments…' : list.length ? 'No deployments match these filters.' : 'No deployments yet. Use Deploy to ship the first release.'}</p>}

    <Dialog open={noteDialog} onClose={() => setNoteDialog(false)} title="Deploy with a note" description={resource.sourceType === 'git' ? `Builds the latest commit on ${resource.branch} and rolls it out.` : `Pulls ${resource.image} again and rolls it out.`}
      footer={<><Button onClick={() => setNoteDialog(false)}>Cancel</Button><Button variant="primary" icon={RocketLaunchIcon} onClick={() => { setNoteDialog(false); run(() => api.deploy(resource.id, { reason })); setReason(''); }}>Deploy</Button></>}>
      <FormField label="Reason" hint="Shown in the deployment history." htmlFor="dep-reason"><TextInput id="dep-reason" value={reason} autoFocus placeholder="e.g. Pick up the new config" onChange={e => setReason(e.target.value)} /></FormField>
    </Dialog>

    <Dialog open={showCompare && !!a && !!b} onClose={() => setShowCompare(false)} title={a && b ? `Compare #${a.number} and #${b.number}` : ''} footer={<Button onClick={() => setShowCompare(false)}>Close</Button>}>
      {a && b && <table className="dep-compare">
        <thead><tr><th /><th>#{a.number}</th><th>#{b.number}</th></tr></thead>
        <tbody>
          {[['Status', statusInfo[a.status].label, statusInfo[b.status].label], ['Trigger', triggerLabel[a.trigger], triggerLabel[b.trigger]], ['Commit', a.commit || '—', b.commit || '—'],
            ['Branch', a.branch || '—', b.branch || '—'], ['Image', a.imageRef || '—', b.imageRef || '—'], ['By', a.author, b.author],
            ['Duration', duration(a.startedAt, a.finishedAt), duration(b.startedAt, b.finishedAt)], ['Started', timeAgo(a.createdAt), timeAgo(b.createdAt)]]
            .map(([label, x, y]) => <tr key={label} className={cx(String(x) !== String(y) && 'diff')}><th>{label}</th><td>{x}</td><td>{y}</td></tr>)}
        </tbody>
      </table>}
    </Dialog>
  </div>;
}

import { useMemo, useState } from 'react';
import {
  ArrowPathIcon, ArrowRightIcon, ArrowUturnLeftIcon, CheckCircleIcon, ClipboardDocumentIcon, DocumentTextIcon,
  EllipsisHorizontalIcon, RocketLaunchIcon, StopCircleIcon, XCircleIcon,
} from '@heroicons/react/16/solid';
import { Avatar, BranchIcon, CommitIcon, Menu, SearchField, SegmentedControl, Select, Status, cx } from '../../../ui';
import { deploymentsByProject } from './data';
import './Deployments.css';

const statuses = {
  building: { tone: 'warning', label: 'Building' },
  success: { tone: 'success', label: 'Ready' },
  failed: { tone: 'danger', label: 'Failed' },
  cancelled: { tone: 'neutral', label: 'Cancelled' },
};
const triggers = { push: 'Push', manual: 'Manual', rollback: 'Rollback' };
const statusFilters = [{ value: 'all', label: 'All' }, { value: 'building', label: 'In progress' }, { value: 'failed', label: 'Failed' }];

function StepIcon({ status }) {
  if (status === 'done') return <CheckCircleIcon className="step-icon done" />;
  if (status === 'failed') return <XCircleIcon className="step-icon failed" />;
  if (status === 'running') return <ArrowPathIcon className="step-icon running" />;
  return <span className="step-icon pending" />;
}

export function Deployments({ project, resourceName }) {
  const [all, setAll] = useState(() => (deploymentsByProject[project.name] ?? []).filter(d => !resourceName || d.resource === resourceName));
  const [resource, setResource] = useState(resourceName ?? 'all');
  const [status, setStatus] = useState('all');
  const [query, setQuery] = useState('');
  const [expanded, setExpanded] = useState(() => new Set(all.filter(d => d.status === 'building' || d.status === 'failed').slice(0, 1).map(d => d.id)));

  const resources = [{ value: 'all', label: 'All resources' }, ...[...new Set(all.map(d => d.resource))].map(r => ({ value: r, label: r }))];
  const rows = useMemo(() => {
    const q = query.trim().toLowerCase();
    return all.filter(d => (resource === 'all' || d.resource === resource) && (status === 'all' || d.status === status)
      && (!q || d.commit.includes(q) || d.message.toLowerCase().includes(q) || d.author.includes(q)));
  }, [all, resource, status, query]);

  const toggle = id => setExpanded(current => {
    const next = new Set(current);
    next.has(id) ? next.delete(id) : next.add(id);
    return next;
  });

  function redeploy(d, trigger = 'manual') {
    const copy = { ...d, id: Array.from(crypto.getRandomValues(new Uint8Array(9)), n => 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz0123456789'[n % 58]).join(''), trigger, status: 'building', current: false, time: 'Now', duration: '0s', error: undefined,
      message: trigger === 'rollback' ? `Rollback to ${d.commit}` : d.message, author: 'you',
      steps: ['Clone', 'Build', 'Push image', 'Deploy', 'Health check'].map((name, i) => ({ name, duration: null, status: i === 0 ? 'running' : 'pending' })) };
    setAll(list => [copy, ...list]);
    setExpanded(current => new Set([...current, copy.id]));
    setTimeout(() => setAll(list => list.map(x => {
      if (x.id === copy.id) return { ...x, status: 'success', current: true, duration: '1m 34s', time: 'Just now', steps: x.steps.map(s => ({ ...s, status: 'done', duration: '—' })) };
      if (x.resource === copy.resource) return { ...x, current: false };
      return x;
    })), 4000);
  }

  const cancel = d => setAll(list => list.map(x => x.id === d.id ? { ...x, status: 'cancelled', steps: x.steps.map(s => s.status === 'running' ? { ...s, status: 'pending' } : s) } : x));

  const actions = d => [
    { label: 'View build logs', icon: DocumentTextIcon },
    'separator',
    ...(d.status === 'building' ? [{ label: 'Cancel deployment', icon: StopCircleIcon, danger: true, onSelect: () => cancel(d) }] : [
      { label: 'Redeploy', icon: ArrowPathIcon, onSelect: () => redeploy(d) },
      ...(d.status === 'success' && !d.current ? [{ label: 'Roll back to this', icon: ArrowUturnLeftIcon, onSelect: () => redeploy(d, 'rollback') }] : []),
    ]),
    'separator',
    { label: 'Copy commit SHA', icon: ClipboardDocumentIcon, onSelect: () => navigator.clipboard?.writeText(d.commit) },
  ];

  if (!all.length) return <div className="deployments-page">
    <div className="deployments-empty"><RocketLaunchIcon /><strong>No deployments yet</strong><p>Deployments appear here when you push to a connected branch or deploy manually.</p></div>
  </div>;

  return <div className="deployments-page">
    <div className="deployments-toolbar">
      {!resourceName && <Select className="deployments-resource" label="Resource" options={resources} value={resource} onChange={setResource} />}
      <SegmentedControl label="Status" options={statusFilters} value={status} onChange={setStatus} className="deployments-status" />
      <SearchField value={query} onChange={setQuery} placeholder="Search commits or authors" className="deployments-search" />
    </div>

    <ol className="deployment-list">
      {rows.map(d => {
        const s = statuses[d.status];
        const open = expanded.has(d.id);
        return <li key={d.id} className={cx('deployment', open && 'open', d.status)}>
          <div className="deployment-row" role="button" tabIndex={0} aria-expanded={open} onClick={() => toggle(d.id)}
            onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); toggle(d.id); } }}>
            <span className="deployment-id"><span>{d.resource}</span>{d.current && <span className="current-pill"><ArrowRightIcon />Current</span>}</span>
            <span className="deployment-env">{d.environment}</span>
            <span className="deployment-status"><Status tone={s.tone}>{s.label}</Status><span className="deployment-timing">{d.status === 'building' ? d.duration : `${d.duration} (${d.time})`}</span></span>
            <span className="deployment-ref"><BranchIcon /><span>{d.branch}</span></span>
            <span className="deployment-ref commit"><CommitIcon /><code>{d.commit}</code><span className="deployment-message">{d.message}</span></span>
            <span className="deployment-resource" title="Deployment ID">{d.id}</span>
            <span className="deployment-author" title={`${triggers[d.trigger]} by ${d.author}`}><Avatar name={d.author} /></span>
            <span onClick={event => event.stopPropagation()} onKeyDown={event => event.stopPropagation()}>
              <Menu trigger={EllipsisHorizontalIcon} label={`Actions for deployment ${d.id}`} items={actions(d)} />
            </span>
          </div>
          {open && <div className="deployment-details">
            {d.error && <p className="deployment-error"><XCircleIcon />{d.error}</p>}
            <ol className="deployment-steps">
              {d.steps.map(step => <li key={step.name} className={step.status}>
                <StepIcon status={step.status} /><span>{step.name}</span><span className="step-duration">{step.duration ?? (step.status === 'running' ? 'Running…' : '')}</span>
              </li>)}
            </ol>
          </div>}
        </li>;
      })}
    </ol>
    {!rows.length && <p className="deployments-none">No deployments match these filters.</p>}
  </div>;
}

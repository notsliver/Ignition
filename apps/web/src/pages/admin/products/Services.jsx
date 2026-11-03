import { useEffect, useMemo, useState } from 'react';
import { ArrowTopRightOnSquareIcon, ArrowsUpDownIcon, CheckIcon, EllipsisHorizontalIcon, NoSymbolIcon, PencilSquareIcon, PlayIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, Menu, Notice, SearchField, Select, Status, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { ProductMark, price, specs } from './shared';
import '../../settings/settings.css';
import './products.css';

/*
 * Instance administration › Services: every plan assigned to a team. Suspend (it stops, and the team
 * can't start it), change plan (resizes and redeploys), add a note, or terminate (deletes it).
 */

const STATES = { active: { label: 'Active', tone: 'success' }, suspended: { label: 'Suspended', tone: 'warning' }, terminated: { label: 'Terminated', tone: 'neutral' } };

export function Services() {
  const [filters, setFilters] = useState({ product: '', team: '', status: '', q: '' });
  const [query, setQuery] = useState('');
  useEffect(() => { const t = setTimeout(() => setFilters(f => ({ ...f, q: query.trim() })), 250); return () => clearTimeout(t); }, [query]);
  const q = useQuery(() => api.services(Object.fromEntries(Object.entries(filters).filter(([, v]) => v))), [JSON.stringify(filters)], { pollMs: 20000 });
  const products = useQuery(() => api.products(), []);
  const teams = useQuery(() => api.platformTeams(), []);
  const [dialog, setDialog] = useState(null); // { kind, service }
  const [feedback, setFeedback] = useState(null);
  const list = q.data?.services ?? [];
  const set = key => value => setFilters(f => ({ ...f, [key]: value }));
  const productById = useMemo(() => Object.fromEntries((products.data?.products ?? []).map(p => [p.id, p])), [products.data]);
  const open = v => `/teams/${v.teamSlug}/resources/${v.resourceId}`;

  async function act(fn, done) {
    setFeedback(null);
    try { await fn(); setFeedback({ tone: 'success', title: done }); q.refetch(); }
    catch (err) { setFeedback({ tone: 'danger', title: err.message }); }
  }

  if (q.error && !q.data) return <div className="settings-page"><Notice tone="danger" title="Couldn't load services">{q.error.message}</Notice></div>;
  return <div className="settings-page pd-page">
    <p className="pd-intro">Every plan you've assigned. Teams see these with their resources and can't go beyond their plan; a suspended one stays stopped until you unsuspend it.</p>
    <div className="pd-toolbar">
      <SearchField value={query} onChange={setQuery} placeholder="Search services or teams" />
      <Select label="Product" value={filters.product} onChange={set('product')} options={[{ value: '', label: 'All products' }, ...(products.data?.products ?? []).map(p => ({ value: p.id, label: p.name }))]} />
      <Select label="Team" value={filters.team} searchable={8} onChange={set('team')} options={[{ value: '', label: 'All teams' }, ...(teams.data?.teams ?? []).map(t => ({ value: t.id, label: t.name }))]} />
      <Select label="Status" value={filters.status} onChange={set('status')} options={[{ value: '', label: 'Any status' }, { value: 'active', label: 'Active' }, { value: 'suspended', label: 'Suspended' }, { value: 'terminated', label: 'Terminated' }]} />
      <span className="pd-count">{q.data ? `${list.length} ${list.length === 1 ? 'service' : 'services'}` : ''}</span>
    </div>
    {feedback && <Notice tone={feedback.tone} title={feedback.title} onDismiss={() => setFeedback(null)} />}

    <div className="settings-card pd-table-card">
      <div className="pd-scroll">
        <table className="pd-table">
          <thead><tr><th>Service</th><th>Team</th><th>Status</th><th>Plan</th><th className="num">Price</th><th>Since</th><th /></tr></thead>
          <tbody>
            {list.map(v => {
              const s = STATES[v.status];
              return <tr key={v.id} className={cx(v.status === 'terminated' && 'terminated')}>
                <td><span className="pd-svc"><ProductMark product={productById[v.productId] ?? { kind: v.kind }} size={28} />
                  <span><strong>{v.name}</strong><small>{v.productName}{v.note && <> · <span title={v.note}>{v.note}</span></>}</small></span></span></td>
                <td>{v.teamName}</td>
                <td><Status tone={s.tone}>{s.label}</Status>
                  {v.status === 'active' && v.resourceStatus && <small className="pd-sub">{v.resourceStatus === 'created' ? 'not deployed' : v.resourceStatus}</small>}
                  {v.status === 'suspended' && v.suspendedReason && <small className="pd-sub" title={v.suspendedReason}>{v.suspendedReason}</small>}</td>
                <td><strong className="pd-plan-name">{v.plan.name}</strong><small className="pd-sub">{specs(v.plan)}</small></td>
                <td className="num">{price(v.plan) || '—'}</td>
                <td className="pd-muted">{timeAgo(v.createdAt)}</td>
                <td className="pd-actions">
                  {v.status !== 'terminated' && <Menu className="icon-action" label={`Actions for ${v.name}`} align="end" items={[
                    ...(v.resourceId ? [{ label: 'Open in team', icon: ArrowTopRightOnSquareIcon, hint: 'As an instance administrator', onSelect: () => { location.href = open(v); } }] : []),
                    v.status === 'suspended'
                      ? { label: 'Unsuspend', icon: PlayIcon, hint: 'Starts it again', onSelect: () => act(() => api.serviceAction(v.id, { action: 'unsuspend' }), `${v.name} is active again`) }
                      : { label: 'Suspend', icon: NoSymbolIcon, hint: 'Stops it; the team can\'t start it', onSelect: () => setDialog({ kind: 'suspend', service: v }) },
                    { label: 'Change plan', icon: ArrowsUpDownIcon, onSelect: () => setDialog({ kind: 'plan', service: v }) },
                    { label: 'Edit note', icon: PencilSquareIcon, onSelect: () => setDialog({ kind: 'note', service: v }) },
                    'separator',
                    { label: 'Terminate', icon: TrashIcon, danger: true, onSelect: () => setDialog({ kind: 'terminate', service: v }) },
                  ]}><EllipsisHorizontalIcon /></Menu>}
                </td>
              </tr>;
            })}
            {q.data && !list.length && <tr><td colSpan={7} className="pd-empty-row">{Object.values(filters).some(Boolean) ? 'Nothing matches these filters.' : 'No services yet. Assign a product to a team from Products.'}</td></tr>}
            {!q.data && <tr><td colSpan={7} className="pd-empty-row">Loading…</td></tr>}
          </tbody>
        </table>
      </div>
    </div>

    {dialog?.kind === 'suspend' && <SuspendDialog service={dialog.service} onClose={() => setDialog(null)}
      onSubmit={reason => { setDialog(null); act(() => api.serviceAction(dialog.service.id, { action: 'suspend', reason }), `${dialog.service.name} is suspended`); }} />}
    {dialog?.kind === 'plan' && <PlanDialog service={dialog.service} product={productById[dialog.service.productId]} onClose={() => setDialog(null)}
      onSubmit={planId => { setDialog(null); act(() => api.serviceAction(dialog.service.id, { action: 'change-plan', planId }), `${dialog.service.name} changed plan and is redeploying`); }} />}
    {dialog?.kind === 'note' && <NoteDialog service={dialog.service} onClose={() => setDialog(null)}
      onSubmit={note => { setDialog(null); act(() => api.updateService(dialog.service.id, { note }), 'Note saved'); }} />}
    {dialog?.kind === 'terminate' && <TerminateDialog service={dialog.service} onClose={() => setDialog(null)}
      onSubmit={() => { setDialog(null); act(() => api.terminateService(dialog.service.id), `${dialog.service.name} was terminated`); }} />}
  </div>;
}

function SuspendDialog({ service, onClose, onSubmit }) {
  const [reason, setReason] = useState('');
  return <Dialog open onClose={onClose} title={`Suspend ${service.name}?`} description={`It stops now, and ${service.teamName} can't start or deploy it until you unsuspend it. Its data is kept.`}
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" icon={NoSymbolIcon} onClick={() => onSubmit(reason.trim())}>Suspend</Button></>}>
    <FormField label="Reason" htmlFor="pd-reason" hint="Optional. The team sees it when they try to start it.">
      <TextInput id="pd-reason" autoFocus value={reason} maxLength={300} placeholder="Unpaid invoice #1042" onChange={e => setReason(e.target.value)} />
    </FormField>
  </Dialog>;
}

function PlanDialog({ service, product, onClose, onSubmit }) {
  const [plan, setPlan] = useState(service.plan.id);
  const plans = product?.plans ?? [service.plan];
  return <Dialog open onClose={onClose} title={`Change ${service.name}'s plan`} description="It's resized and redeploys right away. Games and databases restart with their data."
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" icon={CheckIcon} disabled={plan === service.plan.id} onClick={() => onSubmit(plan)}>Change plan</Button></>}>
    <div className="pd-plan-cards" role="radiogroup" aria-label="Plan">
      {plans.map(p => <button key={p.id} type="button" role="radio" aria-checked={plan === p.id} className={cx('pd-plan-card', plan === p.id && 'on')} onClick={() => setPlan(p.id)}>
        <span className="pd-radio">{plan === p.id && <CheckIcon />}</span>
        <span><strong>{p.name}{p.id === service.plan.id && <em className="pd-current">Current</em>}</strong><small>{specs(p)}</small></span>
        {price(p) && <em>{price(p)}</em>}
      </button>)}
    </div>
  </Dialog>;
}

function NoteDialog({ service, onClose, onSubmit }) {
  const [note, setNote] = useState(service.note);
  return <Dialog open onClose={onClose} title="Note" description="Only administrators see it."
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" onClick={() => onSubmit(note)}>Save</Button></>}>
    <textarea className="settings-textarea" autoFocus rows={4} maxLength={2000} value={note} onChange={e => setNote(e.target.value)} aria-label="Note" />
  </Dialog>;
}

function TerminateDialog({ service, onClose, onSubmit }) {
  const [typed, setTyped] = useState('');
  return <Dialog open onClose={onClose} title={`Terminate ${service.name}?`}
    description={`Deletes it from ${service.teamName} with all its data, files and backups. This can't be undone. The service stays in this list as terminated.`}
    footer={<><Button onClick={onClose}>Cancel</Button><Button className="danger-solid" icon={TrashIcon} disabled={typed !== service.name} onClick={onSubmit}>Terminate</Button></>}>
    <FormField label={<>Type <span className="mono">{service.name}</span> to confirm</>} htmlFor="pd-confirm">
      <TextInput id="pd-confirm" className="mono" autoFocus value={typed} onChange={e => setTyped(e.target.value)} />
    </FormField>
  </Dialog>;
}

import { Fragment, useEffect, useMemo, useRef, useState } from 'react';
import {
  ArrowDownIcon, ArrowPathIcon, ArrowRightIcon, ArrowUpIcon, BoltIcon, CheckCircleIcon, CheckIcon, ChevronDownIcon, ChevronRightIcon,
  ClockIcon, EllipsisHorizontalIcon, ExclamationTriangleIcon, EyeIcon, HandRaisedIcon, LinkIcon, PauseIcon, PencilSquareIcon, PlayIcon,
  PlusIcon, SparklesIcon, TrashIcon, XMarkIcon,
} from '@heroicons/react/16/solid';
import { Button, Checkbox, Dialog, Menu, Notice, SegmentedControl, Select, Status, Switch, TextInput, cx } from '../../ui';
import { api } from '../../api/client';
import { timeAgo, useQuery } from '../../api/hooks';
import { useSession, can } from '../../app/AuthGate';
import { CopyButton } from '../project/resource/shared';
import './automations.css';

/*
 * Automations: Ignition's operations engine. One page, used in two places: a server's workspace
 * (automations that act on servers) and a resource's sidebar (automations that act on that resource).
 * Everything shown is live: counts, runs, plans and verification come from the API; capabilities
 * Ignition can't perform yet are listed with what they need, never offered.
 */

const MODES = [
  { value: 'observe', label: 'Observe', description: 'Detect and recommend. Read-only steps gather evidence; nothing changes.' },
  { value: 'assisted', label: 'Assisted', description: 'Prepare a plan and wait for approval. Plans expire after 24 hours.' },
  { value: 'auto', label: 'Automatic', description: 'Execute within the limits below, verify, and pause itself on repeated failure.' },
];
const MODE_LABEL = { observe: 'Observe', assisted: 'Assisted', auto: 'Automatic' };
const RUN = {
  queued: ['neutral', 'Queued'], awaiting_approval: ['warning', 'Awaiting approval'], approved: ['progress', 'Approved'], running: ['progress', 'Running'],
  succeeded: ['success', 'Succeeded'], failed: ['danger', 'Failed'], recommended: ['warning', 'Recommendation'], rejected: ['neutral', 'Rejected'],
  expired: ['neutral', 'Expired'], skipped: ['neutral', 'Skipped'], cancelled: ['neutral', 'Cancelled'],
};
const STEP = { succeeded: ['success', 'Done'], failed: ['danger', 'Failed'], running: ['progress', 'Running'], skipped: ['neutral', 'Skipped'], pending: ['neutral', 'Pending'] };
const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const SCHEDULES = [['*/15 * * * *', 'Every 15 minutes'], ['0 * * * *', 'Every hour'], ['0 3 * * *', 'Every day at 03:00'], ['0 7 * * 1-5', 'Weekdays at 07:00'], ['0 3 * * 0', 'Sundays at 03:00'], ['custom', 'Custom…']];
const METRIC = { cpu: 'CPU', memory: 'Memory', disk: 'Disk' };
const CATALOG_STATUS = {
  available: ['success', 'Automation'], builtin: ['success', 'Built in'], agent: ['neutral', 'Needs agent'], loadbalancer: ['neutral', 'Needs load balancer'], provider: ['neutral', 'Needs provider API'],
};
const zone = (() => { try { return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'; } catch { return 'UTC'; } })();
const zones = (() => { try { return Intl.supportedValuesOf('timeZone'); } catch { return []; } })();
const when = iso => iso ? new Date(iso).toLocaleString(undefined, { weekday: 'short', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' }) : '';

function triggerText(t, events) {
  switch (t?.type) {
    case 'schedule': return `${SCHEDULES.find(s => s[0] === t.schedule)?.[1] ?? t.schedule}${t.timezone && t.timezone !== 'UTC' ? ` (${t.timezone})` : ' (UTC)'}`;
    case 'event': return `When ${(events.find(e => e.id === t.event)?.label ?? t.event).replace(/^The /, 'the ').replace(/^Its /, 'its ').replace(/^A /, 'a ')}`;
    case 'metric': return `${METRIC[t.metric]} above ${t.above}% for ${t.forMinutes} min`;
    case 'webhook': return 'When its webhook is called';
    default: return 'When run by hand';
  }
}

function targetText(a, servers) {
  if (a.scope === 'resource') return a.resourceName;
  const t = a.targets ?? {};
  if (t.mode === 'all') return 'All servers';
  if (t.mode === 'environment') return `Servers in ${t.environment}`;
  if (t.mode === 'label') return `Servers labelled ${t.label}`;
  const names = (t.ids ?? []).map(id => servers?.find(s => s.id === id)?.name ?? 'removed server');
  return names.length > 2 ? `${names.slice(0, 2).join(', ')} +${names.length - 2}` : names.join(', ');
}

export function Automations({ scope, server, resource, onNavigate }) {
  const { user } = useSession();
  const canManage = can(user, 'automations.manage') && (scope !== 'servers' || can(user, 'servers.update'));
  const query = scope === 'servers' ? { serverId: server.id } : { resourceId: resource.id };
  const [fast, setFast] = useState(false);
  const { data, error, refetch } = useQuery(() => api.automations(query), [server?.id, resource?.id], { pollMs: fast ? 3000 : 10000 });
  const catalog = useQuery(() => api.automationCatalog(), []);
  const servers = useQuery(() => (scope === 'servers' ? api.servers({ limit: 200 }) : Promise.resolve(null)), [scope]);
  const [editing, setEditing] = useState(null); // {} for new, or an automation
  const [openRun, setOpenRun] = useState(null);
  const [feedback, setFeedback] = useState(null);
  const [showCatalog, setShowCatalog] = useState(false);
  const [removing, setRemoving] = useState(null);
  const list = data?.automations ?? [];
  const active = list.some(a => ['queued', 'running', 'approved'].includes(a.lastRun?.status));
  useEffect(() => setFast(active), [active]);

  if (error && !data) return <Notice tone="danger" title="Couldn't load automations">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice>;
  if (!data || !catalog.data) return <p className="au-muted">Loading…</p>;

  const cat = catalog.data;
  const counts = data.counts;
  const attention = data.attention ?? [];
  const paused = list.filter(a => !a.enabled && a.pausedReason);
  const say = (tone, title, text) => setFeedback({ tone, title, text });
  const act = async (fn, ok) => { try { const out = await fn(); if (ok) say('success', ok); refetch(); return out; } catch (e) { say('danger', 'That didn\'t work', e.message); } };
  const serverList = servers.data?.servers ?? [];

  const fourth = scope === 'servers'
    ? { label: 'Managed servers', value: data.managedServers ?? '—', hint: data.resourceAutomations ? `${data.resourceAutomations} resource automation${data.resourceAutomations === 1 ? '' : 's'} run here too` : 'Docker hosts automations can act on' }
    : { label: 'Runs, last 24 h', value: counts.runs24h, hint: 'Including skipped and recommendations' };

  return <div className="au-page">
    <div className="au-intro">
      <p>{scope === 'servers'
        ? <>Decide what should happen on {server.name} and the servers it's grouped with, run approved operations, and prove they worked.</>
        : <>Detect problems with {resource.name}, act within the rules you set, and verify the outcome.</>}</p>
      {canManage && <Button variant="primary" icon={PlusIcon} onClick={() => setEditing({})}>New automation</Button>}
    </div>

    <section className="au-stats" aria-label="Summary">
      <div><span>Active automations</span><strong>{counts.active}</strong><small>{list.length - counts.active > 0 ? `${list.length - counts.active} paused` : 'All running'}</small></div>
      <div className={cx(counts.attention > 0 && 'attention')}><span>Needs attention</span><strong>{counts.attention}</strong><small>Failed runs, recommendations, paused</small></div>
      <div className={cx(counts.approvals > 0 && 'attention')}><span>Pending approvals</span><strong>{counts.approvals}</strong><small>Plans waiting for a decision</small></div>
      <div><span>{fourth.label}</span><strong>{fourth.value}</strong><small>{fourth.hint}</small></div>
    </section>

    {feedback && <Notice tone={feedback.tone} title={feedback.title} onDismiss={() => setFeedback(null)}>{feedback.text}</Notice>}

    {(attention.length > 0 || paused.length > 0) && <section className="au-section">
      <h2>What needs attention</h2>
      <div className="au-card">
        {paused.map(a => <div key={a.id} className="au-attn">
          <span className="au-attn-icon bad"><PauseIcon /></span>
          <div className="au-attn-main"><strong>{a.name} paused itself</strong><span>{a.pausedReason}</span></div>
          {canManage && <div className="au-attn-actions"><Button onClick={() => setEditing(a)}>Review</Button>
            <Button onClick={() => act(() => api.updateAutomation(a.id, editable(a, { enabled: true })), `${a.name} is running again`)}>Resume</Button></div>}
        </div>)}
        {attention.map(r => <AttentionItem key={r.id} run={r} canManage={canManage} onOpen={() => setOpenRun(r.id)}
          onDecide={(kind, note) => act(() => api.decideAutomationRun(r.id, kind, note), { approve: 'Approved. It runs now, or when its maintenance window opens.', reject: 'Rejected. Nothing was changed.', acknowledge: 'Cleared from Needs attention.' }[kind])} />)}
      </div>
    </section>}

    <section className="au-section">
      <h2>{scope === 'servers' ? 'Automations for this server' : 'Automations'}</h2>
      {list.length === 0
        ? <div className="au-empty">
          <BoltIcon />
          <strong>No automations yet</strong>
          <p>{scope === 'servers' ? 'Start from a template: disk pressure cleanup, weekly Docker cleanup, offline recovery or a daily health report.' : 'Start from a template: recover when unhealthy, investigate crashes, roll back an unhealthy release or a nightly command.'}</p>
          {canManage && <Button icon={PlusIcon} onClick={() => setEditing({})}>New automation</Button>}
        </div>
        : <div className="au-list">
          {list.map(a => <AutomationRow key={a.id} a={a} events={cat.events} servers={serverList} canManage={canManage}
            onEdit={() => setEditing(a)} onOpenRun={setOpenRun}
            onToggle={enabled => act(() => api.updateAutomation(a.id, editable(a, { enabled })), enabled ? `${a.name} is on` : `${a.name} is paused`)}
            onRun={() => act(async () => { const { run } = await api.runAutomation(a.id); setOpenRun(run.id); return run; })}
            onHook={() => act(async () => { const { url } = await api.automationHook(a.id); say('success', 'Webhook URL created. Copy it now; it won\'t be shown again.', <span className="au-secret"><code>{url}</code><CopyButton value={url} label="Copy webhook URL" /></span>); })}
            onTestNotify={() => act(() => api.testAutomationNotify(a.id), 'Test notification sent')}
            onDelete={() => setRemoving(a)} />)}
        </div>}
    </section>

    <section className="au-section">
      <button type="button" className="au-catalog-toggle" aria-expanded={showCatalog} onClick={() => setShowCatalog(s => !s)}>
        {showCatalog ? <ChevronDownIcon /> : <ChevronRightIcon />}What Ignition can automate
        <small>{cat.catalog.flatMap(g => g.items).filter(i => i.status === 'available' || i.status === 'builtin').length} of {cat.catalog.flatMap(g => g.items).length} capabilities available</small>
      </button>
      {showCatalog && <Catalog groups={cat.catalog} />}
    </section>

    {editing && <Builder scope={scope} server={server} resource={resource} catalog={cat} servers={serverList} automation={editing.id ? editing : null}
      onClose={() => setEditing(null)} onSaved={(a, created) => { setEditing(null); say('success', created ? `${a.name} created` : `${a.name} saved`, created && a.trigger.type === 'manual' ? 'Use Run now to try it.' : undefined); refetch(); }} />}
    {removing && <Dialog open onClose={() => setRemoving(null)} title={`Delete ${removing.name}?`} description="Its run history goes too. Runs in progress finish; queued and waiting ones are dropped."
      footer={<><Button onClick={() => setRemoving(null)}>Cancel</Button><Button className="danger-solid" icon={TrashIcon} onClick={() => { const a = removing; setRemoving(null); act(() => api.deleteAutomation(a.id), `${a.name} deleted`); }}>Delete</Button></>} />}
    {openRun && <RunDialog runId={openRun} canManage={canManage} onClose={() => { setOpenRun(null); refetch(); }} onNavigate={onNavigate} />}
  </div>;
}

// editable turns a stored automation back into the body PUT expects.
function editable(a, patch = {}) {
  return { name: a.name, description: a.description, targets: a.targets, trigger: a.trigger, conditions: a.conditions, steps: a.steps, mode: a.mode,
    enabled: a.enabled, escalateAfter: a.escalateAfter, maxRunsPerHour: a.maxRunsPerHour, ...patch };
}

function Workflow({ steps, compact }) {
  return <ol className={cx('au-flow', compact && 'compact')}>
    {steps.map((s, i) => <li key={i} className={cx(['Approval', 'Recommend'].includes(s) && 'gate', ['Verify'].includes(s) && 'verify')}>{s}{i < steps.length - 1 && <ArrowRightIcon />}</li>)}
  </ol>;
}

function AutomationRow({ a, events, servers, canManage, onEdit, onToggle, onRun, onOpenRun, onHook, onTestNotify, onDelete }) {
  const last = a.lastRun;
  const [tone, label] = last ? RUN[last.status] ?? ['neutral', last.status] : ['neutral', 'Never ran'];
  const items = [
    { label: 'Edit', icon: PencilSquareIcon, onSelect: onEdit },
    { label: a.hasHook ? 'New webhook URL' : 'Create webhook URL', icon: LinkIcon, onSelect: onHook },
    ...(a.hasNotify ? [{ label: 'Send test notification', icon: BoltIcon, onSelect: onTestNotify }] : []),
    'separator',
    { label: 'Delete', icon: TrashIcon, danger: true, onSelect: onDelete },
  ];
  return <article className={cx('au-row', !a.enabled && 'off')}>
    <div className="au-row-head">
      <Switch label={a.enabled ? `Pause ${a.name}` : `Turn on ${a.name}`} checked={a.enabled} disabled={!canManage} onChange={onToggle} />
      <div className="au-row-title">
        <strong>{a.name}</strong>
        <span>{triggerText(a.trigger, events)} · {targetText(a, servers)}</span>
      </div>
      <span className={cx('au-mode', a.mode)}>{MODE_LABEL[a.mode]}</span>
      {canManage && <Button icon={PlayIcon} onClick={onRun} disabled={!a.enabled} title={a.enabled ? 'Run now' : 'Turn it on to run it'}>Run now</Button>}
      {canManage && <Menu label={`More for ${a.name}`} items={items} trigger={EllipsisHorizontalIcon} />}
    </div>
    {a.description && <p className="au-row-desc">{a.description}</p>}
    <Workflow steps={a.workflow} compact />
    <div className="au-row-foot">
      {last ? <button type="button" className="au-last" onClick={() => onOpenRun(last.id)}>
        <Status tone={tone}>{label}</Status><span>{timeAgo(last.finishedAt ?? last.createdAt)}{last.summary && ` · ${last.summary}`}</span><ChevronRightIcon />
      </button> : <span className="au-muted">Hasn't run yet.</span>}
      {a.enabled && a.nextRunAt && <span className="au-muted au-next"><ClockIcon />Next {when(a.nextRunAt)}</span>}
      {!a.enabled && a.pausedReason && <span className="au-paused"><ExclamationTriangleIcon />Paused after repeated failures</span>}
    </div>
  </article>;
}

function AttentionItem({ run, canManage, onOpen, onDecide }) {
  const [note, setNote] = useState('');
  const [open, setOpen] = useState(false);
  const approval = run.status === 'awaiting_approval';
  const plan = Array.isArray(run.plan) ? run.plan : [];
  const changing = plan.flatMap(t => t.skipped ? [] : t.steps.filter(s => !s.readOnly).map(s => ({ ...s, target: t.target.name })));
  return <div className="au-attn">
    <span className={cx('au-attn-icon', approval ? 'wait' : run.status === 'failed' ? 'bad' : 'warn')}>{approval ? <HandRaisedIcon /> : run.status === 'failed' ? <XMarkIcon /> : <SparklesIcon />}</span>
    <div className="au-attn-main">
      <strong>{approval ? `${run.automation} wants to act` : run.status === 'failed' ? `${run.automation} failed` : `${run.automation} recommends action`}</strong>
      <span>{run.reason}{run.summary && ` · ${run.summary}`}</span>
      {approval && <button type="button" className="au-plan-toggle" onClick={() => setOpen(o => !o)}>{open ? <ChevronDownIcon /> : <ChevronRightIcon />}
        {changing.length} change{changing.length === 1 ? '' : 's'} on {plan.filter(t => !t.skipped).length} target{plan.filter(t => !t.skipped).length === 1 ? '' : 's'}{run.expiresAt && ` · expires ${timeAgo(run.expiresAt).replace(' ago', '')}`}</button>}
      {approval && open && <ul className="au-plan">
        {plan.map(t => <li key={t.target.id}><strong>{t.target.name}</strong>{t.skipped ? <em> — left out: {t.skipped}</em>
          : <ol>{t.steps.map((s, i) => <li key={i}><span className={cx('au-risk', s.risk)}>{s.risk === 'disruptive' ? 'Disruptive' : 'Safe'}</span>{s.label}<small>Verifies: {s.verifies}</small></li>)}</ol>}</li>)}
      </ul>}
      {approval && canManage && <TextInput placeholder="Note (optional)" value={note} onChange={e => setNote(e.target.value)} aria-label="Decision note" className="au-note" maxLength={300} />}
    </div>
    <div className="au-attn-actions">
      <Button icon={EyeIcon} onClick={onOpen}>Details</Button>
      {canManage && (approval
        ? <><Button onClick={() => onDecide('reject', note)}>Reject</Button><Button variant="primary" icon={CheckIcon} onClick={() => onDecide('approve', note)}>Approve</Button></>
        : <Button onClick={() => onDecide('acknowledge')}>Dismiss</Button>)}
    </div>
  </div>;
}

function Catalog({ groups }) {
  return <div className="au-catalog">
    {groups.map(g => <div key={g.label} className="au-catalog-group">
      <h3>{g.label}</h3>
      <ul>{g.items.map(i => {
        const [tone, label] = CATALOG_STATUS[i.status];
        return <li key={i.label} className={cx(tone === 'neutral' && 'unavailable')}>
          <div><strong>{i.label}</strong><span>{i.how}</span></div>
          <span className={cx('au-cap', i.status)}>{label}</span>
        </li>;
      })}</ul>
    </div>)}
  </div>;
}

/* ---------------------------------- Builder ---------------------------------- */

function draftFrom(a, scope, resource) {
  if (a) return { ...editable(a), scope: a.scope, notifyUrl: undefined, hasNotify: a.hasNotify };
  return { name: '', description: '', scope, resourceId: resource?.id, targets: scope === 'servers' ? { mode: 'ids', ids: [] } : {}, trigger: { type: 'manual' },
    conditions: { cooldownMinutes: 0, skipInMaintenance: true }, steps: [], mode: 'assisted', enabled: true, escalateAfter: 3, maxRunsPerHour: 6, notifyUrl: '' };
}

function Builder({ scope, server, resource, catalog, servers, automation, onClose, onSaved }) {
  const [draft, setDraft] = useState(() => {
    const d = draftFrom(automation, scope, resource);
    if (!automation && scope === 'servers') d.targets = { mode: 'ids', ids: [server.id] };
    return d;
  });
  const [picked, setPicked] = useState(!!automation);
  const [errors, setErrors] = useState({});
  const [failure, setFailure] = useState('');
  const [saving, setSaving] = useState(false);
  const [preview, setPreview] = useState(null);
  const timer = useRef();
  const set = (key, value) => { setDraft(d => ({ ...d, [key]: value })); setErrors(({ [key]: _, ...rest }) => rest); };
  const setIn = (key, patch) => set(key, { ...draft[key], ...patch });
  const actions = catalog.actions.filter(a => a.scope.includes(scope));
  const events = catalog.events.filter(e => e.scope === scope);
  const templates = catalog.templates.filter(t => t.scope === scope);

  useEffect(() => {
    clearTimeout(timer.current);
    timer.current = setTimeout(() => api.previewAutomation(body(draft)).then(setPreview).catch(() => {}), 300);
    return () => clearTimeout(timer.current);
  }, [draft]);

  function body(d) {
    const { hasNotify: _h, ...rest } = d;
    if (rest.notifyUrl === undefined) delete rest.notifyUrl;
    return rest;
  }
  function applyTemplate(t) {
    setDraft(d => ({ ...d, name: t.name, description: t.description, mode: t.mode, trigger: { timezone: zone, ...t.trigger }, conditions: { skipInMaintenance: true, ...t.conditions },
      steps: t.steps.map(s => ({ ...s, params: { ...defaultsFor(actions, s.action), ...s.params } })) }));
    setPicked(true);
  }
  async function save() {
    setSaving(true);
    setFailure('');
    try {
      const out = automation ? await api.updateAutomation(automation.id, body(draft)) : await api.createAutomation(body(draft));
      onSaved(out.automation, !automation);
    } catch (e) {
      setErrors(e.fields ?? {});
      if (!Object.keys(e.fields ?? {}).length) setFailure(e.message);
      setSaving(false);
    }
  }

  const problems = { ...(preview?.errors ?? {}), ...errors };
  const shown = key => errors[key] && <p className="au-error">{errors[key]}</p>;

  return <Dialog open onClose={onClose} className="au-builder" title={automation ? `Edit ${automation.name}` : 'New automation'}
    description={scope === 'servers' ? 'Acts on servers through the Docker API, using the same operations as the server pages.' : `Acts on ${resource.name}.`}
    footer={picked && <>
      {Object.keys(problems).length > 0 && <span className="au-footer-hint"><ExclamationTriangleIcon />{Object.values(problems)[0]}</span>}
      <Button onClick={onClose}>Cancel</Button>
      <Button variant="primary" disabled={saving} onClick={save}>{saving ? 'Saving…' : automation ? 'Save' : 'Create automation'}</Button>
    </>}>
    {!picked ? <div className="au-templates">
      <p className="au-muted">Start from a template and adjust it, or build one from scratch.</p>
      <div className="au-template-grid">
        {templates.map(t => <button key={t.id} type="button" className="au-template" onClick={() => applyTemplate(t)}>
          <strong>{t.name}</strong><span>{t.description}</span><em className={cx('au-mode', t.mode)}>{MODE_LABEL[t.mode]}</em>
        </button>)}
        <button type="button" className="au-template blank" onClick={() => setPicked(true)}><PlusIcon /><strong>From scratch</strong><span>Pick a trigger, targets and steps yourself.</span></button>
      </div>
    </div> : <div className="au-form">
      {failure && <Notice tone="danger" title={failure} />}
      <div className="au-field-row">
        <label className="au-label">Name<TextInput value={draft.name} onChange={e => set('name', e.target.value)} aria-invalid={!!errors.name} maxLength={80} placeholder="Critical security patching" autoFocus={!automation} /></label>
        <label className="au-label">Description <small>optional</small><TextInput value={draft.description} onChange={e => set('description', e.target.value)} maxLength={500} placeholder="What it's for, for whoever reads this later" /></label>
      </div>
      {shown('name')}

      <fieldset className="au-group"><legend><span>1</span>When should it run?</legend>
        <TriggerEditor trigger={draft.trigger} scope={scope} events={events} metrics={catalog.metrics[scope]} onChange={t => set('trigger', t)} />
        {shown('trigger')}
        {preview?.nextRuns?.length > 0 && <p className="au-muted">Next: {preview.nextRuns.map(when).join(', ')}</p>}
      </fieldset>

      {scope === 'servers' && <fieldset className="au-group"><legend><span>2</span>Which servers?</legend>
        <TargetEditor targets={draft.targets} servers={servers} onChange={t => set('targets', t)} />
        {shown('targets')}
        {preview && <p className="au-muted">{preview.targets?.length ? `Acts on ${preview.targets.length} server${preview.targets.length === 1 ? '' : 's'} now: ${preview.targets.slice(0, 6).join(', ')}${preview.targets.length > 6 ? '…' : ''}` : 'No server matches right now.'}</p>}
      </fieldset>}

      <fieldset className="au-group"><legend><span>{scope === 'servers' ? 3 : 2}</span>Conditions</legend>
        <ConditionsEditor conditions={draft.conditions} trigger={draft.trigger} scope={scope} onChange={c => set('conditions', c)} />
        {shown('conditions')}
      </fieldset>

      <fieldset className="au-group"><legend><span>{scope === 'servers' ? 4 : 3}</span>Steps</legend>
        <StepsEditor steps={draft.steps} actions={actions} onChange={s => set('steps', s)} />
        {shown('steps')}
      </fieldset>

      <fieldset className="au-group"><legend><span>{scope === 'servers' ? 5 : 4}</span>How much can it do on its own?</legend>
        <div className="au-modes" role="radiogroup" aria-label="Autonomy">
          {MODES.map(m => <button key={m.value} type="button" role="radio" aria-checked={draft.mode === m.value} className={cx(draft.mode === m.value && 'on')} onClick={() => set('mode', m.value)}>
            <strong>{m.label}</strong><span>{m.description}</span>
          </button>)}
          <div className="au-mode-off" title="Not available"><strong>Adaptive</strong><span>Choosing among approved fixes from observed state. Not available yet: every step here is chosen by you.</span></div>
        </div>
        {draft.mode === 'auto' && preview?.disruptive && <Notice tone="warning" title="Automatic with disruptive steps">It can restart or redeploy without asking. Limits below cap how often; after {draft.escalateAfter || 3} failed runs in a row it pauses and asks for a person.</Notice>}
        <div className="au-field-row three">
          <label className="au-label">Pause after<Select label="Escalate after" value={String(draft.escalateAfter)} onChange={v => set('escalateAfter', Number(v))}
            options={[1, 2, 3, 5, 10].map(n => ({ value: String(n), label: `${n} failed run${n === 1 ? '' : 's'} in a row` }))} /></label>
          <label className="au-label">At most<Select label="Runs per hour" value={String(draft.maxRunsPerHour)} onChange={v => set('maxRunsPerHour', Number(v))}
            options={[1, 2, 3, 6, 12, 30, 60].map(n => ({ value: String(n), label: `${n} run${n === 1 ? '' : 's'} an hour` }))} /></label>
          <label className="au-label">Notify (webhook) <small>failures, approvals, escalations</small>
            <TextInput className="mono" value={draft.notifyUrl ?? ''} placeholder={draft.hasNotify ? 'Saved; type to replace, or clear' : 'https://hooks.slack.com/…'}
              onChange={e => set('notifyUrl', e.target.value)} aria-invalid={!!errors.notifyUrl} spellCheck={false} /></label>
        </div>
        {(errors.notifyUrl || preview?.errors?.notifyUrl) && <p className="au-error">{errors.notifyUrl || preview.errors.notifyUrl}</p>}
      </fieldset>

      <div className="au-preview">
        <span>Workflow</span>
        {preview?.workflow ? <Workflow steps={preview.workflow} /> : <p className="au-muted">…</p>}
        {draft.conditions.window && preview && !preview.inWindowNow && <p className="au-muted"><ClockIcon className="au-inline" />Outside the maintenance window right now: changes wait for it; observing and planning don't.</p>}
      </div>
    </div>}
  </Dialog>;
}

function defaultsFor(actions, id) {
  return Object.fromEntries((actions.find(a => a.id === id)?.params ?? []).map(p => [p.key, p.default]));
}

function TriggerEditor({ trigger, scope, events, metrics, onChange }) {
  const types = [
    { value: 'schedule', label: 'Schedule' }, { value: 'event', label: 'Event' }, { value: 'metric', label: 'Threshold' },
    { value: 'manual', label: 'By hand' }, { value: 'webhook', label: 'Webhook' },
  ];
  const preset = SCHEDULES.some(s => s[0] === trigger.schedule) ? trigger.schedule : 'custom';
  const pick = type => onChange({
    schedule: { type, schedule: '0 3 * * *', timezone: zone }, event: { type, event: events[0]?.id }, metric: { type, metric: metrics[0], above: 85, forMinutes: 10 },
    manual: { type }, webhook: { type },
  }[type]);
  return <div className="au-stack">
    <SegmentedControl label="Trigger" options={types} value={trigger.type} onChange={pick} />
    {trigger.type === 'schedule' && <div className="au-field-row">
      <label className="au-label">Schedule<Select label="Schedule" value={preset} options={SCHEDULES.map(([value, label]) => ({ value, label, description: value === 'custom' ? undefined : value }))}
        onChange={v => onChange({ ...trigger, schedule: v === 'custom' ? '' : v })} /></label>
      {preset === 'custom' && <label className="au-label">Cron expression<TextInput className="mono" value={trigger.schedule} placeholder="*/10 * * * *" onChange={e => onChange({ ...trigger, schedule: e.target.value })} spellCheck={false} /></label>}
      <label className="au-label">Timezone<Select label="Timezone" searchable value={trigger.timezone || 'UTC'} options={[...new Set(['UTC', zone, trigger.timezone || 'UTC', ...zones])].map(z => ({ value: z, label: z }))} onChange={v => onChange({ ...trigger, timezone: v })} /></label>
    </div>}
    {trigger.type === 'event' && <label className="au-label">Event<Select label="Event" value={trigger.event} options={events.map(e => ({ value: e.id, label: e.label }))} onChange={v => onChange({ ...trigger, event: v })} /></label>}
    {trigger.type === 'metric' && <div className="au-field-row three">
      <label className="au-label">Metric<Select label="Metric" value={trigger.metric} options={metrics.map(m => ({ value: m, label: `${METRIC[m]}${scope === 'resource' ? ' (of its limit)' : ''}` }))} onChange={v => onChange({ ...trigger, metric: v })} /></label>
      <label className="au-label">Above<TextInput inputMode="numeric" suffix="%" value={String(trigger.above ?? '')} onChange={e => onChange({ ...trigger, above: Number(e.target.value.replace(/\D/g, '').slice(0, 3)) })} /></label>
      <label className="au-label">For at least<TextInput inputMode="numeric" suffix="minutes" value={String(trigger.forMinutes ?? '')} onChange={e => onChange({ ...trigger, forMinutes: Number(e.target.value.replace(/\D/g, '').slice(0, 4)) })} /></label>
    </div>}
    {trigger.type === 'webhook' && <p className="au-muted">After saving, use "Create webhook URL" in the automation's menu. Anyone with the URL can trigger it, so keep it secret.</p>}
    {trigger.type === 'manual' && <p className="au-muted">Runs only when someone clicks Run now.</p>}
  </div>;
}

function TargetEditor({ targets, servers, onChange }) {
  const modes = [{ value: 'ids', label: 'Selected' }, { value: 'environment', label: 'Environment' }, { value: 'label', label: 'Label' }, { value: 'all', label: 'All servers' }];
  const ids = targets.ids ?? [];
  const envs = [...new Set(servers.map(s => s.environment).filter(Boolean))];
  return <div className="au-stack">
    <SegmentedControl label="Targets" options={modes} value={targets.mode} onChange={mode => onChange({ ...targets, mode })} />
    {targets.mode === 'ids' && <div className="au-servers">
      {servers.map(s => <label key={s.id} className="check-row"><Checkbox label={s.name} checked={ids.includes(s.id)}
        onChange={on => onChange({ ...targets, ids: on ? [...ids, s.id] : ids.filter(x => x !== s.id) })} />{s.name}<small>{s.isLocal ? 'this machine' : s.endpoint}</small></label>)}
    </div>}
    {targets.mode === 'environment' && <label className="au-label">Environment<TextInput value={targets.environment ?? ''} placeholder={envs[0] ?? 'production'} onChange={e => onChange({ ...targets, environment: e.target.value })} />
      {envs.length > 0 && <small className="au-muted">In use: {envs.join(', ')}</small>}</label>}
    {targets.mode === 'label' && <label className="au-label">Label<TextInput className="mono" value={targets.label ?? ''} placeholder="role=web" onChange={e => onChange({ ...targets, label: e.target.value })} spellCheck={false} /></label>}
    {targets.mode !== 'ids' && <p className="au-muted">Servers added later that match are included automatically.</p>}
    <label className="check-row"><Checkbox label="Stop at first failure" checked={!!targets.stopOnFailure} onChange={v => onChange({ ...targets, stopOnFailure: v })} />
      Roll out one server at a time and stop at the first one that fails</label>
  </div>;
}

function ConditionsEditor({ conditions, trigger, scope, onChange }) {
  const w = conditions.window;
  const eventLike = trigger.type === 'event' || trigger.type === 'metric';
  return <div className="au-stack">
    <div className="au-field-row">
      <label className="au-label">Cooldown {eventLike && <small>between runs for the same target</small>}
        <Select label="Cooldown" value={String(conditions.cooldownMinutes || (eventLike ? 30 : 0))} onChange={v => onChange({ ...conditions, cooldownMinutes: Number(v) })}
          options={[[0, 'None'], [5, '5 minutes'], [15, '15 minutes'], [30, '30 minutes'], [60, '1 hour'], [360, '6 hours'], [1440, '1 day']].filter(([v]) => !(eventLike && v === 0)).map(([value, label]) => ({ value: String(value), label }))} /></label>
      <label className="check-row au-check-pad"><Checkbox label="Skip in maintenance" checked={!!conditions.skipInMaintenance} onChange={v => onChange({ ...conditions, skipInMaintenance: v })} />
        Leave {scope === 'servers' ? 'servers' : 'it'} alone while {scope === 'servers' ? 'they\'re' : 'its server is'} in maintenance mode</label>
    </div>
    <label className="check-row"><Checkbox label="Maintenance window" checked={!!w} onChange={on => onChange({ ...conditions, window: on ? { days: [0, 6], start: '02:00', end: '05:00', timezone: zone } : undefined })} />
      Only make changes inside a maintenance window</label>
    {w && <div className="au-window">
      <div className="au-days">{DAYS.map((d, i) => <button key={d} type="button" aria-pressed={w.days.includes(i)} className={cx(w.days.includes(i) && 'on')}
        onClick={() => onChange({ ...conditions, window: { ...w, days: w.days.includes(i) ? w.days.filter(x => x !== i) : [...w.days, i].sort() } })}>{d}</button>)}</div>
      <TextInput className="au-time" type="time" value={w.start} onChange={e => onChange({ ...conditions, window: { ...w, start: e.target.value } })} aria-label="Window start" />
      <span className="au-muted">to</span>
      <TextInput className="au-time" type="time" value={w.end} onChange={e => onChange({ ...conditions, window: { ...w, end: e.target.value } })} aria-label="Window end" />
      <Select label="Window timezone" searchable value={w.timezone} options={[...new Set(['UTC', zone, w.timezone, ...zones])].map(z => ({ value: z, label: z }))} onChange={v => onChange({ ...conditions, window: { ...w, timezone: v } })} />
    </div>}
  </div>;
}

function StepsEditor({ steps, actions, onChange }) {
  const [adding, setAdding] = useState('');
  const update = (i, patch) => onChange(steps.map((s, j) => j === i ? { ...s, ...patch } : s));
  const move = (i, by) => { const next = [...steps]; [next[i], next[i + by]] = [next[i + by], next[i]]; onChange(next); };
  return <div className="au-stack">
    {steps.length === 0 && <p className="au-muted">No steps yet. Each step's outcome is verified before the next one runs.</p>}
    <ol className="au-steps">
      {steps.map((s, i) => {
        const a = actions.find(x => x.id === s.action);
        if (!a) return null;
        return <li key={i} className="au-step">
          <div className="au-step-head">
            <span className="au-step-n">{i + 1}</span>
            <strong>{a.label}</strong>
            <span className={cx('au-risk', a.risk)}>{a.risk === 'disruptive' ? 'Disruptive' : 'Safe'}</span>
            {a.readOnly && <span className="au-risk safe">Read-only</span>}
            <span className="au-step-tools">
              <button type="button" className="icon-action" aria-label="Move up" disabled={i === 0} onClick={() => move(i, -1)}><ArrowUpIcon /></button>
              <button type="button" className="icon-action" aria-label="Move down" disabled={i === steps.length - 1} onClick={() => move(i, 1)}><ArrowDownIcon /></button>
              <button type="button" className="icon-action danger" aria-label={`Remove ${a.label}`} onClick={() => onChange(steps.filter((_, j) => j !== i))}><TrashIcon /></button>
            </span>
          </div>
          <p className="au-step-does">{a.does} <em>Verifies: {a.verifies}</em></p>
          {a.params?.length > 0 && <div className="au-params">
            {a.params.map(p => <ParamInput key={p.key} p={p} value={s.params?.[p.key] ?? p.default} onChange={v => update(i, { params: { ...s.params, [p.key]: v } })} />)}
          </div>}
          {i < steps.length - 1 && <label className="check-row au-onfail"><Checkbox label="Continue on failure" checked={s.onFailure === 'continue'} onChange={v => update(i, { onFailure: v ? 'continue' : 'stop' })} />
            Continue with the next step if this one fails</label>}
        </li>;
      })}
    </ol>
    {steps.length < 10 && <div className="au-add-step">
      <Select label="Add a step" value={adding} onChange={setAdding} options={[{ value: '', label: 'Add a step…' }, ...actions.map(a => ({ value: a.id, label: a.label, description: a.risk === 'disruptive' ? 'Disruptive' : a.readOnly ? 'Read-only' : 'Safe' }))]} />
      <Button icon={PlusIcon} disabled={!adding} onClick={() => { onChange([...steps, { action: adding, params: defaultsFor(actions, adding), onFailure: 'stop' }]); setAdding(''); }}>Add</Button>
    </div>}
  </div>;
}

function ParamInput({ p, value, onChange }) {
  if (p.kind === 'bool') return <label className="check-row"><Checkbox label={p.label} checked={!!value} onChange={onChange} />{p.label}</label>;
  if (p.kind === 'int') return <label className="au-label">{p.label}<TextInput inputMode="numeric" suffix={p.hint} value={String(value ?? '')} onChange={e => onChange(Number(e.target.value.replace(/\D/g, '').slice(0, 5)))} /></label>;
  return <label className={cx('au-label', p.kind === 'command' && 'wide')}>{p.label}{p.hint && <small>{p.hint}</small>}
    <TextInput className={p.kind === 'command' ? 'mono' : undefined} value={value ?? ''} placeholder={p.kind === 'command' ? 'npm run cleanup' : ''} onChange={e => onChange(e.target.value)} spellCheck={false} /></label>;
}

/* ----------------------------------- Runs ----------------------------------- */

function RunDialog({ runId, canManage, onClose, onNavigate }) {
  const [poll, setPoll] = useState(true);
  const { data, refetch } = useQuery(() => api.automationRun(runId), [runId], { pollMs: poll ? 2000 : 0 });
  const run = data?.run;
  useEffect(() => { if (run && !['queued', 'running', 'approved'].includes(run.status)) setPoll(false); }, [run?.status]); // eslint-disable-line react-hooks/exhaustive-deps
  const [busy, setBusy] = useState(false);
  const decide = async kind => { setBusy(true); try { await api.decideAutomationRun(runId, kind); setPoll(true); refetch(); } finally { setBusy(false); } };
  const byTarget = useMemo(() => {
    const groups = [];
    for (const s of run?.steps ?? []) {
      let g = groups.find(x => x.id === s.targetId);
      if (!g) groups.push(g = { id: s.targetId, name: s.targetName, kind: s.targetKind, steps: [] });
      g.steps.push(s);
    }
    return groups;
  }, [run?.steps]);
  if (!run) return null;
  const [tone, label] = RUN[run.status] ?? ['neutral', run.status];
  const plan = Array.isArray(run.plan) ? run.plan : [];

  return <Dialog open onClose={onClose} className="au-run" title={run.automation} description={run.reason}
    footer={<>
      {canManage && run.status === 'awaiting_approval' && <><Button disabled={busy} onClick={() => decide('reject')}>Reject</Button><Button variant="primary" icon={CheckIcon} disabled={busy} onClick={() => decide('approve')}>Approve</Button></>}
      {canManage && ['queued', 'approved'].includes(run.status) && <Button disabled={busy} onClick={() => decide('cancel')}>Cancel run</Button>}
      {canManage && run.attention && <Button disabled={busy} onClick={() => decide('acknowledge')}>Dismiss</Button>}
      <Button onClick={onClose}>Close</Button>
    </>}>
    <div className="au-run-head">
      <Status tone={tone}>{label}</Status>
      <span className="au-muted">{run.trigger === 'manual' ? `Run by ${run.requestedBy}` : run.trigger === 'schedule' ? 'Scheduled' : run.trigger === 'webhook' ? 'Webhook' : 'Triggered automatically'} · {when(run.createdAt)}
        {run.finishedAt && run.startedAt && ` · took ${Math.max(1, Math.round((new Date(run.finishedAt) - new Date(run.startedAt)) / 1000))}s`}</span>
      {['queued', 'running', 'approved'].includes(run.status) && <ArrowPathIcon className="au-spin" aria-hidden />}
    </div>
    {run.summary && <p className={cx('au-run-summary', run.status)}>{run.summary}</p>}
    {run.decidedBy && run.decidedAt && <p className="au-muted">{run.status === 'rejected' ? 'Rejected' : 'Decided'} by {run.decidedBy} {timeAgo(run.decidedAt).toLowerCase()}{run.decisionNote && `: "${run.decisionNote}"`}</p>}
    {run.status === 'awaiting_approval' && <p className="au-muted">Nothing has changed yet. This is the plan: {plan.flatMap(t => t.steps ?? []).filter(s => !s.readOnly).length} change(s) would be made.</p>}

    {byTarget.length > 0 ? byTarget.map(g => <section key={g.id} className="au-run-target">
      <h3>{g.name}<small>{g.kind}</small></h3>
      <ol className="au-timeline">
        {g.steps.map(s => {
          const [st, sl] = STEP[s.status] ?? ['neutral', s.status];
          return <li key={s.seq} className={s.status}>
            <div className="au-tl-head"><Status tone={st}>{sl}</Status><strong>{s.action === 'skip' ? 'Left out' : actionLabel(plan, s.action)}</strong>
              {s.ref && <RefLink value={s.ref} onNavigate={onNavigate} />}</div>
            {s.output && <pre className="au-output">{s.output}</pre>}
            {s.verification && <p className={cx('au-verify', s.verified === false && 'bad')}>{s.verified === false ? <XMarkIcon /> : <CheckCircleIcon />}{s.verification}</p>}
          </li>;
        })}
      </ol>
    </section>)
      : plan.length > 0 && <section className="au-run-target">
        {plan.map(t => <Fragment key={t.target.id}><h3>{t.target.name}<small>{t.target.kind}</small></h3>
          {t.skipped ? <p className="au-muted">Left out: {t.skipped}</p>
            : <ol className="au-timeline">{t.steps.map((s, i) => <li key={i} className="pending"><div className="au-tl-head"><span className={cx('au-risk', s.risk)}>{s.risk === 'disruptive' ? 'Disruptive' : 'Safe'}</span><strong>{s.label}</strong></div>
              <p className="au-muted">{s.does} Verifies: {s.verifies}</p></li>)}</ol>}</Fragment>)}
      </section>}
  </Dialog>;
}

function actionLabel(plan, id) {
  for (const t of plan) for (const s of t.steps ?? []) if (s.action === id) return s.label;
  return id;
}

function RefLink({ value, onNavigate }) {
  const [kind] = value.split(':');
  const label = { deployment: 'Deployments', diagnostics: 'Diagnostics', job: 'Server activity' }[kind];
  if (!label) return null;
  const view = kind === 'deployment' ? 'Deployments' : kind === 'diagnostics' ? 'Diagnostics' : null;
  return view && onNavigate ? <button type="button" className="au-ref" onClick={() => onNavigate(view)}>{label}<ArrowRightIcon /></button> : <span className="au-ref muted">{label}</span>;
}

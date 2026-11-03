import { useMemo, useState } from 'react';
import { ArchiveBoxIcon, CheckIcon, DocumentDuplicateIcon, EllipsisHorizontalIcon, PencilIcon, PlusIcon, RocketLaunchIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, Menu, Notice, SearchField, SegmentedControl, Select, Status, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { useQuery } from '../../../api/hooks';
import { ProductEditor } from './ProductEditor';
import { KINDS, ProductMark, STATUS, kindOf, price, slug, specs, stockLeft, summary } from './shared';
import { ServerTable } from './ProductEditor';
import '../../settings/settings.css';
import './products.css';

/*
 * Instance administration › Products: what this instance offers, grouped by category. Each product
 * has plans; Assign gives a plan to a team, which creates its service right away.
 */

export function Products() {
  const q = useQuery(() => api.products(), []);
  const [editing, setEditing] = useState(null); // { product? , kind? }
  const [assigning, setAssigning] = useState(null);
  const [deleting, setDeleting] = useState(null);
  const [query, setQuery] = useState('');
  const [feedback, setFeedback] = useState(null);
  const list = q.data?.products ?? [];
  const categories = useMemo(() => [...new Set(list.map(p => p.category).filter(Boolean))].sort(), [list]);
  const groups = useMemo(() => {
    const s = query.trim().toLowerCase();
    const shown = list.filter(p => !s || p.name.toLowerCase().includes(s) || p.category.toLowerCase().includes(s));
    const m = new Map();
    for (const p of shown) { const c = p.category || 'Other'; if (!m.has(c)) m.set(c, []); m.get(c).push(p); }
    return [...m];
  }, [list, query]);
  const services = list.reduce((n, p) => n + p.services, 0);

  async function act(fn, done) {
    setFeedback(null);
    try { await fn(); if (done) setFeedback({ tone: 'success', title: done }); q.refetch(); }
    catch (err) { setFeedback({ tone: 'danger', title: err.message }); }
  }
  const setStatus = (p, status) => act(() => api.saveProduct(p.id, {
    name: p.name, description: p.description, category: p.category, kind: p.kind, template: p.template, icon: p.icon, placement: p.placement, status,
    plans: p.plans.map(({ services: _, ...pl }) => pl),
  }), `${p.name} is ${STATUS[status].label.toLowerCase()}`);

  if (q.error && !q.data) return <div className="settings-page"><Notice tone="danger" title="Couldn't load products">{q.error.message} <button type="button" className="inline-link" onClick={q.refetch}>Retry</button></Notice></div>;

  return <div className="settings-page pd-page">
    <div className="pd-top">
      <p>What this instance offers. Give a plan to a team with <strong>Assign</strong>: it creates their server, database or app right away, sized to the plan. Prices are shown here only; Ignition doesn't bill anyone.</p>
      <Button variant="primary" icon={PlusIcon} onClick={() => setEditing({})}>New product</Button>
    </div>
    {feedback && <Notice tone={feedback.tone} title={feedback.title} onDismiss={() => setFeedback(null)} />}

    {q.data && !list.length && <div className="pd-empty">
      <strong>Create your first product</strong>
      <span>Pick what it runs. You'll set its plans and prices next.</span>
      <div className="pd-starters">
        {KINDS.map(k => <button key={k.id} type="button" className="pd-starter" onClick={() => setEditing({ kind: k.id })}>
          <ProductMark kind={k.id} product={k.id === 'game' ? { template: { spec: { edition: 'paper' } } } : undefined} size={36} />
          <strong>{k.label}</strong><small>{k.text}</small>
        </button>)}
      </div>
    </div>}

    {list.length > 0 && <>
      <div className="pd-toolbar">
        <SearchField value={query} onChange={setQuery} placeholder="Search products" />
        <span className="pd-count">{list.length} {list.length === 1 ? 'product' : 'products'} · {services} active {services === 1 ? 'service' : 'services'}</span>
      </div>
      {groups.map(([category, products]) => <section key={category} className="pd-group">
        <h2>{category}</h2>
        <div className="pd-grid">
          {products.map(p => <article key={p.id} className={cx('pd-card', p.status === 'retired' && 'retired')}>
            <header>
              <ProductMark product={p} />
              <span className="pd-card-title"><strong>{p.name}</strong><small>{kindOf(p.kind).label}{summary(p) && ` · ${summary(p)}`}</small></span>
              {p.status !== 'active' && <Status tone={STATUS[p.status].tone}>{STATUS[p.status].label}</Status>}
              {p.status === 'active' && stockLeft(p) === 0 && <Status tone="warning">Sold out</Status>}
              {p.status === 'active' && stockLeft(p) > 0 && stockLeft(p) <= 5 && <span className="pd-stock">{stockLeft(p)} left</span>}
              <Menu className="icon-action" label={`More for ${p.name}`} align="end" items={[
                { label: 'Edit', icon: PencilIcon, onSelect: () => setEditing({ product: p }) },
                { label: 'Duplicate', icon: DocumentDuplicateIcon, onSelect: () => act(() => api.duplicateProduct(p.id), `Copied ${p.name} as a draft`) },
                p.status === 'active'
                  ? { label: 'Retire', icon: ArchiveBoxIcon, hint: 'Keeps services, stops new ones', onSelect: () => setStatus(p, 'retired') }
                  : { label: 'Make active', icon: CheckIcon, onSelect: () => setStatus(p, 'active') },
                'separator',
                { label: 'Delete', icon: TrashIcon, danger: true, onSelect: () => setDeleting(p) },
              ]}><EllipsisHorizontalIcon /></Menu>
            </header>
            {p.description && <p className="pd-desc">{p.description}</p>}
            <ul className="pd-plan-list">
              {p.plans.map(pl => <li key={pl.id}><strong>{pl.name}</strong><span>{specs(pl)}</span><em>{price(pl)}</em></li>)}
            </ul>
            <footer>
              <span className="pd-muted">{p.services ? `${p.services} ${p.services === 1 ? 'service' : 'services'}` : 'Not assigned yet'}</span>
              <Button icon={PencilIcon} onClick={() => setEditing({ product: p })}>Edit</Button>
              <Button variant="primary" icon={RocketLaunchIcon} disabled={p.status !== 'active'} title={p.status !== 'active' ? 'Make it active to assign it' : undefined} onClick={() => setAssigning(p)}>Assign</Button>
            </footer>
          </article>)}
        </div>
      </section>)}
    </>}
    {!q.data && <p className="readonly-value">Loading…</p>}

    {editing && <ProductEditor product={editing.product} startKind={editing.kind} categories={categories} onClose={() => setEditing(null)}
      onSaved={p => { setEditing(null); setFeedback({ tone: 'success', title: `Saved ${p.name}` }); q.refetch(); }} />}
    {assigning && <AssignDialog product={assigning} onClose={() => setAssigning(null)}
      onDone={v => { setAssigning(null); setFeedback({ tone: 'success', title: `Assigned ${v.plan.name} to ${v.teamName}`, }); q.refetch(); }} />}
    <Dialog open={!!deleting} onClose={() => setDeleting(null)} title={`Delete ${deleting?.name}?`}
      description={deleting?.services ? `${deleting.services} services use it. Retire it instead: they keep running and nobody gets a new one.` : 'It and its plans are removed. Nothing running is affected.'}
      footer={<><Button onClick={() => setDeleting(null)}>Cancel</Button>
        {deleting?.services ? <Button variant="primary" icon={ArchiveBoxIcon} onClick={() => { const p = deleting; setDeleting(null); setStatus(p, 'retired'); }}>Retire instead</Button>
          : <Button className="danger-solid" icon={TrashIcon} onClick={() => { const p = deleting; setDeleting(null); act(() => api.deleteProduct(p.id), `Deleted ${p.name}`); }}>Delete product</Button>}</>} />
  </div>;
}

export function AssignDialog({ product, onClose, onDone }) {
  const teams = useQuery(() => api.platformTeams(), []);
  const [team, setTeam] = useState('');
  const [plan, setPlan] = useState(product.plans[0]?.id ?? '');
  const [name, setName] = useState(() => `${slug(product.name).slice(0, 30)}-${Math.random().toString(36).slice(2, 6)}`);
  const [note, setNote] = useState('');
  const asks = product.template?._ask ?? [];
  const [answers, setAnswers] = useState(() => Object.fromEntries(asks.map(a => [a.key, a.default ?? ''])));
  const [errors, setErrors] = useState({});
  const [pending, setPending] = useState(false);
  const [server, setServer] = useState('');
  const avail = useQuery(() => team && plan ? api.productAvailability(product.id, { team, plan }) : Promise.resolve(null), [team, plan]);
  const manual = product.placement?.strategy === 'manual';
  const a = avail.data;
  async function submit() {
    setPending(true); setErrors({});
    try { onDone((await api.provisionService({ productId: product.id, planId: plan, teamId: team, name, note, answers, serverId: server })).service); }
    catch (err) { setErrors(err.fields && Object.keys(err.fields).length ? err.fields : { form: err.message }); setPending(false); }
  }
  return <Dialog open onClose={onClose} className="pd-assign" title={`Assign ${product.name}`}
    description="Creates it in the team right away, sized to the plan. The team sees it with their resources."
    footer={<>
      {(errors.form || Object.values(errors)[0]) && <span className="pi-error" role="alert">{errors.form || Object.values(errors)[0]}</span>}
      <Button onClick={onClose}>Cancel</Button>
      <Button variant="primary" icon={RocketLaunchIcon} disabled={pending || !team || !plan || !name || (a && !a.available) || (manual && !server)} onClick={submit}>{pending ? 'Creating…' : 'Assign and create'}</Button>
    </>}>
    <div className="pd-form">
      <FormField label="Team" htmlFor="pd-team" error={errors.teamId}>
        <Select id="pd-team" label="Team" value={team} searchable={8} onChange={setTeam}
          options={[{ value: '', label: teams.data ? 'Choose a team' : 'Loading teams…' }, ...(teams.data?.teams ?? []).map(t => ({ value: t.id, label: t.name }))]} />
      </FormField>
      <div className="pd-field-label">Plan</div>
      <div className="pd-plan-cards" role="radiogroup" aria-label="Plan">
        {product.plans.map(p => <button key={p.id} type="button" role="radio" aria-checked={plan === p.id} className={cx('pd-plan-card', plan === p.id && 'on')} onClick={() => setPlan(p.id)}>
          <span className="pd-radio">{plan === p.id && <CheckIcon />}</span>
          <span><strong>{p.name}</strong><small>{specs(p)}</small></span>
          {price(p) && <em>{price(p)}</em>}
        </button>)}
      </div>
      {asks.length > 0 && <div className="pd-answers">
        <div className="pd-field-label">For this service</div>
        {asks.map(a => <AskField key={a.key} ask={a} value={answers[a.key]} error={errors[`answers.${a.key}`]} answers={answers} product={product}
          onChange={value => { setAnswers(x => ({ ...x, [a.key]: value })); setErrors({}); }} />)}
      </div>}
      {team && plan && <div className={cx('pd-placement', a && (a.available ? 'ok' : 'bad'))}>
        {!a ? <span className="pd-muted">Checking where it can go…</span>
          : !a.available ? <><strong>Can't assign this right now</strong><span>{a.reason}</span><details><summary>Servers</summary><ServerTable avail={a} /></details></>
            : manual ? <FormField label="Server" htmlFor="pd-server" error={errors.serverId}>
              <Select id="pd-server" label="Server" value={server} onChange={setServer}
                options={[{ value: '', label: 'Choose a server' }, ...a.servers.filter(s => s.ok).map(s => ({ value: s.id, label: `${s.name}${s.location ? ` · ${s.location}` : ''}` }))]} />
            </FormField>
              : <><strong>Goes to {a.chosen.name}{a.chosen.location && ` · ${a.chosen.location}`}</strong>
                <span>{[a.stockLeft != null && `${a.stockLeft} left in stock`, a.teamLeft != null && `${a.teamLeft} more for this team`].filter(Boolean).join(' · ') || 'Picked by the product\'s placement strategy.'}</span></>}
      </div>}
      <FormField label="Name" htmlFor="pd-svc-name" error={errors.name} hint="The resource's name in the team. Lowercase letters, numbers and dashes.">
        <TextInput id="pd-svc-name" className="mono" value={name} onChange={e => setName(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-'))} />
      </FormField>
      <FormField label="Note" htmlFor="pd-note" hint="Optional. Only administrators see it, like an order or invoice number.">
        <TextInput id="pd-note" value={note} placeholder="Order #1042" onChange={e => setNote(e.target.value)} />
      </FormField>
    </div>
  </Dialog>;
}

/** One "ask when assigning" setting, as a field in the Assign dialog. */
function AskField({ ask, value, onChange, error, answers, product }) {
  const id = `pd-ask-${ask.key}`;
  const edition = answers.edition ?? product.template?.spec?.edition;
  const versions = useQuery(() => ask.ui === 'gameVersion' ? api.gameVersions(product.template?.game ?? 'minecraft', edition) : Promise.resolve(null), [ask.ui, edition]);
  if (ask.kind === 'source') {
    const v = value && typeof value === 'object' ? value : { sourceType: 'image', image: '', repoUrl: '', branch: 'main' };
    const set = patch => onChange({ ...v, ...patch });
    return <FormField label={ask.label} htmlFor={id} error={error}>
      <div className="pd-source">
        <SegmentedControl label="Source" value={v.sourceType} onChange={sourceType => set({ sourceType })} options={[{ value: 'image', label: 'Image' }, { value: 'git', label: 'Git repository' }]} />
        {v.sourceType === 'git'
          ? <div className="pd-row"><TextInput id={id} className="mono" value={v.repoUrl} placeholder="https://github.com/acme/app" onChange={e => set({ repoUrl: e.target.value.trim() })} />
            <TextInput aria-label="Branch" className="mono" value={v.branch} placeholder="main" onChange={e => set({ branch: e.target.value })} /></div>
          : <TextInput id={id} className="mono" value={v.image} placeholder="ghcr.io/acme/app:1.4" onChange={e => set({ image: e.target.value.trim() })} />}
      </div>
    </FormField>;
  }
  if (ask.kind === 'select') return <FormField label={ask.label} htmlFor={id} error={error}>
    <Select id={id} label={ask.label} value={value} onChange={onChange} options={ask.options.map(o => ({ value: o.value, label: o.label }))} />
  </FormField>;
  if (ask.ui === 'gameVersion') return <FormField label={ask.label} htmlFor={id} error={error}>
    <Select id={id} label={ask.label} value={value ?? ''} searchable={10} onChange={onChange}
      options={[{ value: '', label: `Latest${versions.data?.latest ? ` (${versions.data.latest})` : ''}` }, ...(versions.data?.versions ?? []).filter(x => x.stable).slice(0, 80).map(x => ({ value: x.id, label: x.id }))]} />
  </FormField>;
  if (ask.kind === 'number') return <FormField label={ask.label} htmlFor={id} error={error} hint={ask.min != null ? `${ask.min} to ${ask.max}` : undefined}>
    <TextInput id={id} inputMode="numeric" value={value === '' || value == null ? '' : String(value)} onChange={e => onChange(e.target.value === '' ? '' : Number(e.target.value.replace(/\D/g, '')))} />
  </FormField>;
  return <FormField label={ask.label} htmlFor={id} error={error}>
    <TextInput id={id} className={ask.path?.startsWith('env.') || ask.key === 'command' ? 'mono' : undefined} type={ask.secret ? 'password' : 'text'} value={value ?? ''} onChange={e => onChange(e.target.value)} />
  </FormField>;
}

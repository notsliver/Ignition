import { useEffect, useMemo, useState } from 'react';
import { ArrowLeftIcon, ArrowRightIcon, CheckIcon, PlusIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, Notice, SegmentedControl, Select, Switch, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { useQuery } from '../../../api/hooks';
import { engines } from '../../project/Resources/flows';
import { CodeEditor } from '../../project/resource/Function';
import { logos } from '../../project/game/logos';
import { ALLOW, ALLOW_DEFAULT, ALLOW_FOR, COLORS, ICONS, KINDS, PERIODS, ProductMark, RUNTIMES, STRATEGIES, kindOf, memory, parseIcon } from './shared';

/*
 * Create or edit a product in four steps: what it is, what it runs, its plans, and a last look.
 * The template it saves is what the dashboard sends to create that kind of resource. Any setting
 * can be left to whoever assigns it ("Ask when assigning"), and the product says what teams may
 * change afterwards. {{password}}, {{name}} and {{team}} are filled in for each service.
 */

const STEPS = ['Basics', 'What it runs', 'Plans', 'Placement', 'Review'];
const PLACEMENT = { strategy: 'least-utilized', servers: [], locations: [], ips: [], preferredLocation: '', allowMaintenance: false, minFreeMemoryMb: 0, minFreeCpus: 0, safetyMarginPct: 10, perServer: 0, stock: 0, perTeam: 0, maxConcurrent: 0 };
const PRESETS = {
  game: [['Starter', 1, 2048, 10, 10, 1], ['Standard', 2, 4096, 25, 25, 1], ['Pro', 4, 8192, 50, 50, 2]],
  database: [['Small', 0.5, 512, 5, 5, 0], ['Medium', 1, 2048, 20, 20, 0], ['Large', 2, 4096, 50, 50, 0]],
  default: [['Small', 0.5, 512, 5, 5, 1], ['Medium', 1, 1024, 10, 10, 1], ['Large', 2, 2048, 25, 25, 2]],
};
const blankPlan = (name = '', cpus = 1, memoryMb = 1024, storageGb = 0, backupGb = 0, ports = 0) =>
  ({ key: Math.random().toString(36).slice(2), name, cpus: String(cpus), memoryGb: String(+(memoryMb / 1024).toFixed(2)), storageGb: String(storageGb), backupGb: String(backupGb), ports: String(ports), price: '', period: 'month' });

function initialTemplate(kind) {
  const ui = { ask: [], allow: ALLOW_DEFAULT[kind] };
  switch (kind) {
    case 'game': return { game: 'minecraft', spec: { edition: 'paper', version: '', slots: 20, displayName: '{{name}}' }, acceptEula: false, _ui: { ...ui, editionName: 'Paper', editionChoices: [] } };
    case 'database': return { _ui: { ...ui, engine: 'PostgreSQL', version: engines.PostgreSQL.versions[0], database: 'app', user: 'app', public: false } };
    case 'function': return { engine: 'node', code: '', _ui: { ...ui, runtimeLabel: 'Node.js 22' } };
    case 'files': { const r = RUNTIMES[0]; return { image: r.image, dataPath: r.dataPath, port: r.port, command: r.command, env: [], _ui: { ...ui, runtime: r.id } }; }
    case 'worker': return { sourceType: 'image', image: '', command: '', env: [], _ui: ui };
    default: return { sourceType: 'image', image: '', repoUrl: '', branch: 'main', port: 3000, command: '', env: [], _ui: ui };
  }
}

// Products saved before these options existed: recover the editor state from the saved template.
function editorTemplate(product) {
  const t = structuredClone(product.template ?? {});
  t._ui = { ...(t._ui ?? {}) };
  t._ui.ask ??= (t._ask ?? []).map(a => a.key);
  t._ui.allow ??= t._allow ?? ALLOW_FOR[product.kind] ?? [];
  if (product.kind === 'game') t._ui.editionChoices ??= (t._ask ?? []).find(a => a.key === 'edition')?.options?.map(o => o.value) ?? [];
  return t;
}

// The final template: settings, plus _ask (built from the switches, with today's values as
// defaults) and _allow.
function finalize(kind, t, catalog) {
  const ask = new Set(t._ui?.ask ?? []);
  const asks = [];
  let base = t;
  if (kind === 'database') {
    const ui = t._ui;
    const e = engines[ui.engine];
    const envDefs = e.env({ database: '\u0001', user: '\u0002', password: '{{password}}' });
    base = {
      sourceType: 'image', image: e.image(ui.version), dataPath: e.data, command: e.command ?? '', port: ui.public ? e.port : null,
      env: e.env({ database: ui.database, user: ui.user, password: '{{password}}' }).map(([key, value]) => ({ key, value, secret: /PASSWORD|requirepass/i.test(key) })),
      _ui: ui,
    };
    if (ask.has('version')) asks.push({ key: 'version', kind: 'select', label: 'Version', path: 'image', default: e.image(ui.version), options: e.versions.map(v => ({ value: e.image(v), label: `${ui.engine} ${v}` })) });
    const dbKey = envDefs.find(([, v]) => v === '\u0001')?.[0];
    const userKey = envDefs.find(([, v]) => v === '\u0002')?.[0];
    if (ask.has('database') && dbKey) asks.push({ key: 'database', kind: 'text', label: 'Database name', path: `env.${dbKey}`, default: ui.database, required: true });
    if (ask.has('user') && userKey) asks.push({ key: 'user', kind: 'text', label: 'User', path: `env.${userKey}`, default: ui.user, required: true });
  }
  if (kind === 'application' || kind === 'worker') {
    if (ask.has('source')) asks.push({ key: 'source', kind: 'source', label: 'Source', path: 'source', default: { sourceType: t.sourceType, image: t.image ?? '', repoUrl: t.repoUrl ?? '', branch: t.branch ?? 'main' } });
  }
  if (kind === 'application' || kind === 'files') {
    if (ask.has('port')) asks.push({ key: 'port', kind: 'number', label: 'Port', path: 'port', default: Number(t.port) || 80, min: 1, max: 65535 });
  }
  if (kind === 'application' || kind === 'worker' || kind === 'files') {
    if (ask.has('command')) asks.push({ key: 'command', kind: 'text', label: 'Start command', path: 'command', default: t.command ?? '' });
    for (const e of t.env ?? []) if (e.key && ask.has(`env.${e.key}`)) asks.push({ key: `env.${e.key}`, kind: 'text', label: e.key, path: `env.${e.key}`, default: e.value, secret: !!e.secret });
  }
  if (kind === 'game') {
    const game = catalog?.games.find(g => g.id === t.game);
    const choices = t._ui.editionChoices?.length ? t._ui.editionChoices : [t.spec.edition];
    if (ask.has('edition')) asks.push({ key: 'edition', kind: 'select', label: 'Server type', path: 'spec.edition', default: t.spec.edition,
      options: choices.map(id => ({ value: id, label: game?.editions.find(e => e.id === id)?.name ?? id })) });
    if (ask.has('version')) asks.push({ key: 'version', kind: 'text', ui: 'gameVersion', label: 'Version', path: 'spec.version', default: t.spec.version ?? '' });
    if (ask.has('slots')) asks.push({ key: 'slots', kind: 'number', label: 'Player slots', path: 'spec.slots', default: Number(t.spec.slots) || 20, min: 1, max: 1000 });
    if (ask.has('displayName')) asks.push({ key: 'displayName', kind: 'text', label: 'Server name', path: 'spec.displayName', default: t.spec.displayName ?? '' });
  }
  return { ...base, _ask: asks, _allow: (t._ui?.allow ?? []).filter(a => (ALLOW_FOR[kind] ?? []).includes(a)) };
}

export function ProductEditor({ product, startKind, categories, onClose, onSaved }) {
  const editing = !!product?.id;
  const [step, setStep] = useState(0);
  const catalog = useQuery(() => api.games(), []);
  const [v, setV] = useState(() => product?.id ? {
    name: product.name, description: product.description, category: product.category, status: product.status, kind: product.kind, template: editorTemplate(product),
    icon: product.icon ?? '', placement: { ...PLACEMENT, ...(product.placement ?? {}) },
    currency: product.plans[0]?.currency ?? 'USD',
    plans: product.plans.map(p => ({ ...blankPlan(p.name, p.cpus, p.memoryMb, p.storageGb, p.backupGb, p.ports), id: p.id, price: p.price, period: p.period, services: p.services })),
  } : { name: '', description: '', category: '', status: 'active', kind: startKind ?? 'game', template: initialTemplate(startKind ?? 'game'), currency: 'USD', plans: [], icon: '', placement: { ...PLACEMENT } });
  const [errors, setErrors] = useState({});
  const [pending, setPending] = useState(false);
  const set = patch => { setV(x => ({ ...x, ...patch })); setErrors({}); };
  const setT = patch => setV(x => ({ ...x, template: { ...x.template, ...patch } }));
  const setUI = patch => setV(x => ({ ...x, template: { ...x.template, _ui: { ...x.template._ui, ...patch } } }));
  const ask = key => ({ on: (v.template._ui?.ask ?? []).includes(key), toggle: on => setUI({ ask: on ? [...(v.template._ui?.ask ?? []), key] : (v.template._ui?.ask ?? []).filter(k => k !== key) }) });

  const problems = useMemo(() => {
    const p = {};
    if (!v.name.trim()) p.name = 'Name it.';
    const t = v.template;
    const asked = t._ui?.ask ?? [];
    if (v.kind === 'game' && !t.acceptEula) p.template = 'Accept the game\'s EULA for the servers you hand out.';
    if ((v.kind === 'application' || v.kind === 'worker') && !asked.includes('source') && !(t.sourceType === 'git' ? t.repoUrl : t.image)) p.template = t.sourceType === 'git' ? 'Enter a repository, or ask for it when assigning.' : 'Enter an image, or ask for it when assigning.';
    if (v.kind === 'files' && !t.image) p.template = 'Enter the image for your custom runtime.';
    if (!v.plans.length) p.plans = 'Add at least one plan.';
    if (v.placement.strategy === 'location' && !v.placement.preferredLocation) p.placement = 'Choose the preferred location.';
    v.plans.forEach((pl, i) => { if (!pl.name.trim()) p[`plans.${i}.name`] = 'Name it.'; });
    return p;
  }, [v]);
  const stepOk = [!problems.name, !problems.template, !problems.plans && !Object.keys(problems).some(k => k.startsWith('plans.')), !problems.placement, true];

  async function save() {
    setPending(true); setErrors({});
    const template = finalize(v.kind, v.template, catalog.data);
    // A source left to the assigner still needs a placeholder for validation.
    if ((v.kind === 'application' || v.kind === 'worker') && !template.image && !template.repoUrl) template.image = 'nginx:1.27-alpine';
    const body = {
      name: v.name.trim(), description: v.description.trim(), category: v.category.trim(), status: v.status, kind: v.kind, template, icon: v.icon,
      placement: { ...v.placement, minFreeMemoryMb: Math.round(Number(v.placement.minFreeMemoryMb) || 0), minFreeCpus: Number(v.placement.minFreeCpus) || 0,
        safetyMarginPct: Number(v.placement.safetyMarginPct) || 0, perServer: Number(v.placement.perServer) || 0, stock: Number(v.placement.stock) || 0,
        perTeam: Number(v.placement.perTeam) || 0, maxConcurrent: Number(v.placement.maxConcurrent) || 0 },
      plans: v.plans.map(p => ({ ...(p.id ? { id: p.id } : {}), name: p.name.trim(), cpus: Number(p.cpus) || 0, memoryMb: Math.round((Number(p.memoryGb) || 0) * 1024),
        storageGb: Number(p.storageGb) || 0, backupGb: Number(p.backupGb) || 0, ports: Number(p.ports) || 0, price: p.price.trim(), currency: v.currency.trim() || 'USD', period: p.period })),
    };
    try {
      const out = editing ? await api.saveProduct(product.id, body) : await api.createProduct(body);
      onSaved(out.product);
    } catch (err) {
      setErrors(err.fields && Object.keys(err.fields).length ? err.fields : { form: err.message });
      const f = Object.keys(err.fields ?? {});
      if (f.some(k => ['name', 'description', 'category', 'status'].includes(k))) setStep(0);
      else if (f.some(k => k === 'template' || k === 'kind')) setStep(1);
      else if (f.some(k => k.startsWith('plans'))) setStep(2);
      else if (f.some(k => k.startsWith('placement'))) setStep(3);
      setPending(false);
    }
  }

  const last = step === STEPS.length - 1;
  return <Dialog open onClose={onClose} className="pd-editor" title={editing ? `Edit ${product.name}` : 'New product'}
    description="Something you hand out to teams, in plans of different sizes. Prices are shown, never charged."
    footer={<>
      {(errors.form || Object.values(errors)[0]) && <span className="pi-error" role="alert">{errors.form || Object.values(errors)[0]}</span>}
      {step > 0 && <Button icon={ArrowLeftIcon} onClick={() => setStep(s => s - 1)}>Back</Button>}
      <span className="pd-spacer" />
      <Button onClick={onClose}>Cancel</Button>
      {last
        ? <Button variant="primary" icon={CheckIcon} disabled={pending || stepOk.slice(0, 3).some(ok => !ok)} onClick={save}>{pending ? 'Saving…' : editing ? 'Save product' : 'Create product'}</Button>
        : <Button variant="primary" disabled={!stepOk[step]} title={!stepOk[step] ? Object.values(problems)[0] : undefined} onClick={() => setStep(s => s + 1)}>Continue<ArrowRightIcon className="pd-after" /></Button>}
    </>}>
    <ol className="pd-steps" aria-label="Steps">
      {STEPS.map((s, i) => <li key={s} className={cx(i === step && 'on', i < step && 'done')}>
        <button type="button" disabled={i > step && !stepOk.slice(0, i).every(Boolean)} onClick={() => setStep(i)}>
          <span className="pd-step-dot">{i < step ? <CheckIcon /> : i + 1}</span>{s}</button>
      </li>)}
    </ol>

    {step === 0 && <div className="pd-form">
      <IconPicker value={v.icon} onChange={icon => set({ icon })} product={{ kind: v.kind, template: v.template }} error={errors.icon} />
      <FormField label="Name" htmlFor="pd-name" error={errors.name} hint="What you and the teams call it.">
        <TextInput id="pd-name" autoFocus value={v.name} maxLength={80} placeholder="Minecraft server" onChange={e => set({ name: e.target.value })} />
      </FormField>
      <div className="pd-row">
        <FormField label="Category" htmlFor="pd-cat" hint="Groups products on the Products page.">
          <TextInput id="pd-cat" list="pd-categories" value={v.category} maxLength={60} placeholder="Game servers" onChange={e => set({ category: e.target.value })} />
          <datalist id="pd-categories">{categories.map(c => <option key={c} value={c} />)}</datalist>
        </FormField>
        <FormField label="Status" htmlFor="pd-status" hint={{ draft: 'Not assignable yet.', active: 'Ready to assign.', retired: 'Existing services keep running; no new ones.' }[v.status]}>
          <Select id="pd-status" label="Status" value={v.status} onChange={status => set({ status })} options={[{ value: 'active', label: 'Active' }, { value: 'draft', label: 'Draft' }, { value: 'retired', label: 'Retired' }]} />
        </FormField>
      </div>
      <FormField label="Description" htmlFor="pd-desc" hint="Optional. A line or two about what's included.">
        <textarea id="pd-desc" className="settings-textarea" rows={3} maxLength={1000} value={v.description} placeholder="A Paper server with backups every night." onChange={e => set({ description: e.target.value })} />
      </FormField>
    </div>}

    {step === 1 && <div className="pd-form">
      <div className="pd-kinds" role="radiogroup" aria-label="What it runs">
        {KINDS.map(k => <button key={k.id} type="button" role="radio" aria-checked={v.kind === k.id} disabled={editing && product.services > 0 && v.kind !== k.id}
          className={cx('pd-kind', v.kind === k.id && 'on')} onClick={() => v.kind !== k.id && set({ kind: k.id, template: initialTemplate(k.id) })}>
          <ProductMark kind={k.id} size={30} /><span><strong>{k.label}</strong><small>{k.text}</small></span>
        </button>)}
      </div>
      {editing && product.services > 0 && <p className="pd-muted">What it runs can't change to another kind while services use it.</p>}
      <p className="pd-hint-ask"><span className="pd-ask-badge">Ask</span> Switch on <strong>Ask when assigning</strong> next to a setting to choose it per service in the Assign dialog. What you enter here becomes the suggestion.</p>
      {v.kind === 'game' && <GameTemplate t={v.template} set={setT} setUI={setUI} ask={ask} catalog={catalog} />}
      {v.kind === 'database' && <DatabaseTemplate ui={v.template._ui} set={setUI} ask={ask} />}
      {(v.kind === 'application' || v.kind === 'worker') && <AppTemplate t={v.template} set={setT} worker={v.kind === 'worker'} ask={ask} />}
      {v.kind === 'files' && <FilesTemplate t={v.template} set={setT} setUI={setUI} ask={ask} />}
      {v.kind === 'function' && <FunctionTemplate t={v.template} set={setT} />}
      <AllowSection kind={v.kind} allow={v.template._ui?.allow ?? []} onChange={allow => setUI({ allow })} />
    </div>}

    {step === 2 && <PlansStep v={v} set={set} errors={errors} />}

    {step === 3 && <PlacementStep v={v} set={placement => set({ placement: { ...v.placement, ...placement } })} product={product} />}

    {step === 4 && <Review v={v} editing={editing} catalog={catalog.data} />}
  </Dialog>;
}

/* --------------------------------- Helpers --------------------------------- */

/** A field label with its "Ask when assigning" switch. */
function Asked({ label, ask, children, hint }) {
  return <div className={cx('pd-asked', ask?.on && 'on')}>
    <div className="pd-asked-head">
      <span className="pd-field-label">{label}</span>
      {ask && <label className="pd-ask"><Switch label={`Ask for ${label} when assigning`} checked={ask.on} onChange={ask.toggle} />Ask when assigning</label>}
    </div>
    {children}
    {hint && <span className="pd-asked-hint">{ask?.on ? 'Suggested in the Assign dialog. ' : ''}{hint}</span>}
  </div>;
}

function AllowSection({ kind, allow, onChange }) {
  const options = ALLOW_FOR[kind] ?? [];
  if (!options.length) return null;
  return <div className="pd-allow">
    <div className="pd-field-label">Teams can change later</div>
    <div className="pd-allow-list">
      {options.map(k => <button key={k} type="button" role="checkbox" aria-checked={allow.includes(k)} className={cx('pd-allow-item', allow.includes(k) && 'on')}
        onClick={() => onChange(allow.includes(k) ? allow.filter(x => x !== k) : [...allow, k])}>
        <span className="pd-box" aria-hidden="true">{allow.includes(k) && <CheckIcon />}</span>
        <span><strong>{ALLOW[k].label}</strong><small>{ALLOW[k].text}</small></span>
      </button>)}
    </div>
    <span className="pd-muted">Anything not ticked stays as you assigned it; only instance administrators can change it. Teams can always start, stop, view logs and use Files.</span>
  </div>;
}

/* --------------------------------- Templates -------------------------------- */

function GameTemplate({ t, set, setUI, ask, catalog }) {
  const game = catalog.data?.games.find(g => g.id === t.game) ?? catalog.data?.games[0];
  const versions = useQuery(() => game ? api.gameVersions(game.id, t.spec.edition) : Promise.resolve(null), [game?.id, t.spec.edition]);
  const edition = game?.editions.find(e => e.id === t.spec.edition);
  const spec = patch => set({ spec: { ...t.spec, ...patch } });
  const editionAsk = ask('edition');
  const choices = t._ui.editionChoices ?? [];
  if (catalog.error) return <Notice tone="danger" title="Couldn't load the games">{catalog.error.message}</Notice>;
  if (!game) return <p className="pd-muted">Loading games…</p>;
  const groups = [...new Set(game.editions.map(e => e.group || ''))];
  return <>
    <Asked label="Server type" ask={editionAsk} hint={editionAsk.on ? 'Tick the types to offer; the highlighted one is suggested.' : undefined}>
      {groups.map(g => <div key={g} className="pd-editions">
        {g && <span className="pd-ed-group">{g}</span>}
        {game.editions.filter(e => (e.group || '') === g).map(e => {
          const offered = choices.includes(e.id) || e.id === t.spec.edition;
          return <div key={e.id} className={cx('pd-edition', t.spec.edition === e.id && 'on')}>
            <button type="button" className="pd-edition-main" onClick={() => { set({ spec: { ...t.spec, edition: e.id, version: '' }, _ui: { ...t._ui, editionName: e.name, editionChoices: [...new Set([...choices, e.id])] } }); }}>
              {logos[e.id] ? <img src={logos[e.id]} alt="" /> : <span className="pd-edition-dot" />}
              <span><strong>{e.name}{e.recommended && <em>Recommended</em>}</strong><small>{e.description}</small></span>
            </button>
            {editionAsk.on && <button type="button" role="checkbox" aria-checked={offered} aria-label={`Offer ${e.name}`} className={cx('pd-box pd-edition-box', offered && 'on')}
              disabled={e.id === t.spec.edition} onClick={() => setUI({ editionChoices: offered ? choices.filter(x => x !== e.id) : [...choices, e.id] })}>{offered && <CheckIcon />}</button>}
          </div>;
        })}
      </div>)}
    </Asked>
    <div className="pd-row">
      {edition?.versions === 'list' && <Asked label="Version" ask={ask('version')} hint={t.spec.version ? '' : 'Latest means the newest release when each server is created.'}>
        <Select id="pd-version" label="Version" value={t.spec.version} searchable={10} onChange={version => spec({ version })}
          options={[{ value: '', label: `Latest when assigned${versions.data?.latest ? ` (now ${versions.data.latest})` : ''}` }, ...(versions.data?.versions ?? []).filter(x => x.stable).slice(0, 60).map(x => ({ value: x.id, label: x.id }))]} />
      </Asked>}
      <Asked label="Player slots" ask={ask('slots')}>
        <TextInput aria-label="Player slots" inputMode="numeric" value={String(t.spec.slots ?? '')} onChange={e => spec({ slots: Number(e.target.value.replace(/\D/g, '')) || 0 })} />
      </Asked>
    </div>
    <Asked label="Server name" ask={ask('displayName')} hint="Shown to players. {{name}} becomes the service's name, {{team}} the team's.">
      <TextInput aria-label="Server name" value={t.spec.displayName ?? ''} onChange={e => spec({ displayName: e.target.value })} />
    </Asked>
    {game.eula && <label className="pd-check"><Switch label="Accept the EULA" checked={!!t.acceptEula} onChange={acceptEula => set({ acceptEula })} />
      <span>Every server of this product accepts the <a href={game.eula.url} target="_blank" rel="noreferrer">{game.eula.label}</a>. Make sure the teams you assign it to agree to it.</span></label>}
  </>;
}

function DatabaseTemplate({ ui, set, ask }) {
  const e = engines[ui.engine];
  return <>
    <div className="pd-field-label">Engine</div>
    <div className="pd-chips">
      {Object.keys(engines).map(name => <button key={name} type="button" className={cx('pd-chip', ui.engine === name && 'on')}
        onClick={() => set({ engine: name, version: engines[name].versions[0] })}>{name}</button>)}
    </div>
    <div className="pd-row">
      <Asked label="Version" ask={ask('version')}><Select label="Version" value={ui.version} onChange={version => set({ version })} options={e.versions.map(x => ({ value: x, label: x }))} /></Asked>
      {!e.keyValue && <Asked label="Database" ask={ask('database')}><TextInput aria-label="Database" className="mono" value={ui.database} onChange={ev => set({ database: ev.target.value })} /></Asked>}
      {!e.keyValue && <Asked label="User" ask={ask('user')}><TextInput aria-label="User" className="mono" value={ui.user} onChange={ev => set({ user: ev.target.value })} /></Asked>}
    </div>
    <label className="pd-check"><Switch label="Publish the port" checked={!!ui.public} onChange={p => set({ public: p })} />
      <span>Publish port {e.port} so it's reachable from outside this machine. Off keeps it on the team's private network.</span></label>
    <p className="pd-muted">Every service gets its own random password, stored encrypted. Teams see it under Connection.</p>
  </>;
}

function AppTemplate({ t, set, worker, ask }) {
  const source = ask('source');
  return <>
    <Asked label="Source" ask={source} hint={source.on ? 'You enter the image or repository for each service. Leave a suggestion below, or leave it empty.' : undefined}>
      <SegmentedControl label="Source" value={t.sourceType} onChange={sourceType => set({ sourceType })}
        options={[{ value: 'image', label: 'Container image' }, { value: 'git', label: 'Git repository' }]} />
      {t.sourceType === 'git'
        ? <div className="pd-row">
          <TextInput aria-label="Repository" className="mono" value={t.repoUrl ?? ''} placeholder="https://github.com/acme/app" onChange={e => set({ repoUrl: e.target.value.trim().replace(/\.git$/, '') })} />
          <TextInput aria-label="Branch" className="mono" value={t.branch ?? 'main'} onChange={e => set({ branch: e.target.value })} />
        </div>
        : <TextInput aria-label="Image" className="mono" value={t.image ?? ''} placeholder="ghcr.io/acme/app:1.4" onChange={e => set({ image: e.target.value.trim() })} />}
    </Asked>
    <div className="pd-row">
      {!worker && <Asked label="Port" ask={ask('port')}><TextInput aria-label="Port" inputMode="numeric" value={t.port ? String(t.port) : ''} onChange={e => set({ port: Number(e.target.value.replace(/\D/g, '')) || null })} /></Asked>}
      <Asked label="Start command" ask={ask('command')}><TextInput aria-label="Start command" className="mono" value={t.command ?? ''} placeholder="Empty uses the image's own" onChange={e => set({ command: e.target.value })} /></Asked>
    </div>
    <EnvRows env={t.env ?? []} onChange={env => set({ env })} ask={ask} />
  </>;
}

function FilesTemplate({ t, set, setUI, ask }) {
  const current = t._ui?.runtime ?? 'static';
  return <>
    <div className="pd-field-label">Runtime</div>
    <div className="pd-runtimes" role="radiogroup" aria-label="Runtime">
      {RUNTIMES.map(r => <button key={r.id} type="button" role="radio" aria-checked={current === r.id} className={cx('pd-runtime', current === r.id && 'on')}
        onClick={() => { set({ image: r.image, dataPath: r.dataPath, port: r.port, command: r.command }); setUI({ runtime: r.id }); }}>
        <strong>{r.label}</strong><small>{r.text}</small>
      </button>)}
    </div>
    <div className="pd-row">
      <FormField label="Image" htmlFor="pd-fimg"><TextInput id="pd-fimg" className="mono" value={t.image ?? ''} placeholder="ghcr.io/acme/runtime:1" onChange={e => set({ image: e.target.value.trim() })} /></FormField>
      <FormField label="Files folder" htmlFor="pd-fdir" hint="Where the team's uploads go, kept on a volume."><TextInput id="pd-fdir" className="mono" value={t.dataPath ?? ''} onChange={e => set({ dataPath: e.target.value.trim() })} /></FormField>
      <Asked label="Port" ask={ask('port')}><TextInput aria-label="Port" inputMode="numeric" value={t.port ? String(t.port) : ''} onChange={e => set({ port: Number(e.target.value.replace(/\D/g, '')) || null })} /></Asked>
    </div>
    <Asked label="Start command" ask={ask('command')} hint="Empty uses the image's own. The presets wait for uploaded files instead of crashing.">
      <textarea className="settings-textarea mono" rows={3} value={t.command ?? ''} onChange={e => set({ command: e.target.value })} aria-label="Start command" />
    </Asked>
    <EnvRows env={t.env ?? []} onChange={env => set({ env })} ask={ask} />
    <p className="pd-muted">The service starts empty. The team opens it, uploads their site or app under <strong>Files</strong> (folders and zips work), and restarts it.</p>
  </>;
}

function FunctionTemplate({ t, set }) {
  const q = useQuery(() => api.databaseEngines(), []);
  const runtimes = q.data?.runtimes ?? [];
  const rt = runtimes.find(r => r.id === t.engine);
  useEffect(() => { if (rt && !t.code) set({ code: rt.template, _ui: { ...t._ui, runtimeLabel: rt.label } }); }, [rt?.id]); // eslint-disable-line react-hooks/exhaustive-deps
  return <>
    <SegmentedControl label="Runtime" value={t.engine} onChange={engine => { const r = runtimes.find(x => x.id === engine); set({ engine, code: r?.template ?? '', _ui: { ...t._ui, runtimeLabel: r?.label } }); }}
      options={(runtimes.length ? runtimes : [{ id: 'node', label: 'Node.js' }]).map(r => ({ value: r.id, label: r.label }))} />
    <div className="pd-field-label">{rt?.file ?? 'Handler'} <span className="pd-muted">The starter every service gets. Teams edit it under Code.</span></div>
    <CodeEditor label="Handler code" path={rt?.file ?? 'handler.js'} value={t.code ?? ''} onChange={code => set({ code })} height={240} />
  </>;
}

function EnvRows({ env, onChange, ask }) {
  const upd = (i, patch) => onChange(env.map((e, j) => j === i ? { ...e, ...patch } : e));
  return <div className="pd-env">
    <div className="pd-field-label">Environment variables <span className="pd-muted">{'{{password}}'} becomes a random password per service; {'{{name}}'} and {'{{team}}'} its names.</span></div>
    {env.map((e, i) => {
      const a = e.key ? ask(`env.${e.key}`) : null;
      return <div key={i} className="pd-env-row">
        <TextInput className="mono" aria-label="Name" placeholder="KEY" value={e.key} onChange={ev => upd(i, { key: ev.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, '_') })} />
        <TextInput className="mono" aria-label="Value" placeholder={a?.on ? 'Suggested value' : 'value'} value={e.value} onChange={ev => upd(i, { value: ev.target.value })} />
        <label className="pd-secret"><Switch label="Secret" checked={!!e.secret} onChange={secret => upd(i, { secret })} />Secret</label>
        <label className="pd-secret" title="Fill it in when assigning"><Switch label={`Ask for ${e.key || 'this variable'} when assigning`} checked={!!a?.on} onChange={on => a?.toggle(on)} />Ask</label>
        <button type="button" className="icon-action" aria-label="Remove variable" onClick={() => onChange(env.filter((_, j) => j !== i))}><TrashIcon /></button>
      </div>;
    })}
    <button type="button" className="inline-link pd-add" onClick={() => onChange([...env, { key: '', value: '', secret: false }])}><PlusIcon />Add variable</button>
  </div>;
}

/* ----------------------------------- Plans ----------------------------------- */

function PlansStep({ v, set, errors }) {
  const upd = (i, patch) => set({ plans: v.plans.map((p, j) => j === i ? { ...p, ...patch } : p) });
  const num = (i, key) => e => upd(i, { [key]: e.target.value.replace(key === 'cpus' || key === 'memoryGb' ? /[^\d.]/g : /\D/g, '') });
  const presets = PRESETS[v.kind] ?? PRESETS.default;
  return <div className="pd-form">
    <div className="pd-plans-head">
      <span className="pd-muted">Each plan sets the size of the service it creates. Teams can't go beyond it. Disk and backups at 0 mean no plan limit.</span>
      <label className="pd-currency">Currency<TextInput aria-label="Currency" value={v.currency} maxLength={8} onChange={e => set({ currency: e.target.value.toUpperCase() })} /></label>
    </div>
    {!v.plans.length && <div className="pd-presets">
      <span>Start with</span>
      <Button onClick={() => set({ plans: presets.map(p => blankPlan(...p)) })}>{presets.map(p => p[0]).join(' · ')}</Button>
      <Button icon={PlusIcon} onClick={() => set({ plans: [blankPlan('Standard')] })}>One plan</Button>
    </div>}
    {v.plans.length > 0 && <div className="pd-plans">
      <div className="pd-plan pd-plan-head" aria-hidden="true"><span>Plan</span><span>CPU</span><span>Memory (GB)</span><span>Disk (GB)</span><span>Backups (GB)</span><span>Ports</span><span>Price</span><span /></div>
      {v.plans.map((p, i) => <div key={p.key} className="pd-plan">
        <TextInput aria-label="Plan name" value={p.name} placeholder="Standard" aria-invalid={!!errors[`plans.${i}.name`]} onChange={e => upd(i, { name: e.target.value })} />
        <TextInput aria-label="CPU cores" inputMode="decimal" value={p.cpus} onChange={num(i, 'cpus')} />
        <TextInput aria-label="Memory in GB" inputMode="decimal" value={p.memoryGb} onChange={num(i, 'memoryGb')} />
        <TextInput aria-label="Disk in GB" inputMode="numeric" value={p.storageGb} onChange={num(i, 'storageGb')} />
        <TextInput aria-label="Backups in GB" inputMode="numeric" value={p.backupGb} onChange={num(i, 'backupGb')} />
        <TextInput aria-label="Published ports" inputMode="numeric" value={p.ports} onChange={num(i, 'ports')} />
        <span className="pd-price">
          <TextInput aria-label="Price" value={p.price} placeholder="9.99" onChange={e => upd(i, { price: e.target.value.replace(/[^\d.,]/g, '') })} />
          <Select label="Billing period" value={p.period} onChange={period => upd(i, { period })} options={PERIODS} />
        </span>
        <button type="button" className="icon-action danger" disabled={p.services > 0} title={p.services ? `${p.services} services use this plan` : 'Remove plan'} aria-label={`Remove ${p.name || 'plan'}`}
          onClick={() => set({ plans: v.plans.filter((_, j) => j !== i) })}><TrashIcon /></button>
      </div>)}
      <button type="button" className="inline-link pd-add" onClick={() => set({ plans: [...v.plans, blankPlan(`Plan ${v.plans.length + 1}`)] })}><PlusIcon />Add plan</button>
    </div>}
  </div>;
}

function Review({ v, editing, catalog }) {
  const k = kindOf(v.kind);
  const t = finalize(v.kind, v.template, catalog);
  return <div className="pd-form">
    <div className="pd-review">
      <ProductMark product={{ kind: v.kind, template: v.template, icon: v.icon }} size={44} />
      <span><strong>{v.name}</strong><small>{k.label}{v.category && ` · ${v.category}`} · {v.status}</small>{v.description && <p>{v.description}</p>}</span>
    </div>
    <div className="pd-review-facts">
      <div><span>Asked when assigning</span><strong>{t._ask.length ? t._ask.map(a => a.label).join(', ') : 'Nothing: every service is the same'}</strong></div>
      <div><span>Teams can change</span><strong>{t._allow.length ? t._allow.map(a => ALLOW[a].label.toLowerCase()).join(', ') : 'Nothing beyond starting, stopping and their files'}</strong></div>
    </div>
    <div className="pd-review-plans">
      {v.plans.map(p => <div key={p.key} className="pd-review-plan">
        <strong>{p.name}</strong>
        <span>{p.cpus} CPU · {memory(Math.round(Number(p.memoryGb) * 1024))}{Number(p.storageGb) ? ` · ${p.storageGb} GB disk` : ''}{Number(p.backupGb) ? ` · ${p.backupGb} GB backups` : ''}{Number(p.ports) ? ` · ${p.ports} ports` : ''}</span>
        <em>{p.price ? `${p.price} ${v.currency} ${PERIODS.find(x => x.value === p.period)?.label}` : 'No price'}</em>
      </div>)}
    </div>
    <p className="pd-muted">{editing ? 'Changes apply to services assigned from now on, and what teams may change applies to every service right away.' : 'Assign it to a team from the Products page once it\'s saved.'}</p>
  </div>;
}

/* ----------------------------------- Icon ----------------------------------- */

function IconPicker({ value, onChange, product, error }) {
  const cur = parseIcon(value);
  const [color, setColor] = useState(cur.color ?? COLORS[0]);
  const [uploadError, setUploadError] = useState('');
  function upload(file) {
    setUploadError('');
    if (!file) return;
    if (!/^image\/(png|jpeg|webp|gif|svg\+xml)$/.test(file.type)) return setUploadError('Use a PNG, JPEG, WebP, GIF or SVG image.');
    if (file.size > 200 * 1024) return setUploadError('Keep it under 200 KB; 256×256 pixels is plenty.');
    const reader = new FileReader();
    reader.onload = () => onChange(String(reader.result));
    reader.readAsDataURL(file);
  }
  return <div className="pd-icon-picker">
    <div className="pd-icon-preview"><ProductMark product={{ ...product, icon: value }} size={56} /></div>
    <div className="pd-icon-options">
      <div className="pd-icon-head">
        <span className="pd-field-label">Icon</span>
        <span className="pd-icon-actions">
          <button type="button" className={cx('pd-chip', cur.type === 'auto' && 'on')} onClick={() => onChange('')}>Automatic</button>
          <label className={cx('pd-chip pd-upload', cur.type === 'image' && 'on')}>Upload image<input type="file" accept="image/png,image/jpeg,image/webp,image/gif,image/svg+xml" hidden onChange={e => upload(e.target.files[0])} /></label>
        </span>
      </div>
      <div className="pd-icon-grid" role="radiogroup" aria-label="Icon">
        {Object.entries(ICONS).map(([name, Icon]) => <button key={name} type="button" role="radio" aria-checked={cur.name === name} aria-label={name.replace(/Icon$/, '')}
          className={cx('pd-icon-cell', cur.name === name && 'on')} style={{ '--pd-hue': cur.name === name ? cur.color : color }} onClick={() => onChange(`hero:${name}:${color}`)}><Icon /></button>)}
      </div>
      <div className="pd-colors" role="radiogroup" aria-label="Colour">
        {COLORS.map(c => <button key={c} type="button" role="radio" aria-checked={color === c} aria-label={`Colour ${c}`} className={cx('pd-color', color === c && 'on')} style={{ background: c }}
          onClick={() => { setColor(c); if (cur.type === 'hero') onChange(`hero:${cur.name}:${c}`); }} />)}
      </div>
      {(uploadError || error) && <span className="form-error">{uploadError || error}</span>}
    </div>
  </div>;
}

/* --------------------------------- Placement -------------------------------- */

function PlacementStep({ v, set, product }) {
  const servers = useQuery(() => api.servers().catch(() => ({ servers: [] })), []);
  const list = servers.data?.servers ?? [];
  const locations = [...new Set(list.map(s => s.location).filter(Boolean))].sort();
  const p = v.placement;
  const toggle = (key, value) => set({ [key]: p[key].includes(value) ? p[key].filter(x => x !== value) : [...p[key], value] });
  const avail = useQuery(() => product?.id ? api.productAvailability(product.id) : Promise.resolve(null), [product?.id]);
  const num = key => e => set({ [key]: e.target.value.replace(key === 'minFreeCpus' ? /[^\d.]/g : /\D/g, '') });
  return <div className="pd-form">
    <section className="pd-section">
      <h3>Where it can run</h3>
      <div className="pd-field-label">Servers <span className="pd-muted">None ticked means any server.</span></div>
      <div className="pd-chips">
        {list.map(s => <button key={s.id} type="button" role="checkbox" aria-checked={p.servers.includes(s.id)} className={cx('pd-chip', p.servers.includes(s.id) && 'on')} onClick={() => toggle('servers', s.id)}>
          {s.name}{s.location && <small> · {s.location}</small>}</button>)}
        {!list.length && <span className="pd-muted">{servers.data ? 'No servers.' : 'Loading servers…'}</span>}
      </div>
      {locations.length > 0 && <>
        <div className="pd-field-label">Locations <span className="pd-muted">None ticked means anywhere. Set a server's location under Servers.</span></div>
        <div className="pd-chips">{locations.map(l => <button key={l} type="button" role="checkbox" aria-checked={p.locations.includes(l)} className={cx('pd-chip', p.locations.includes(l) && 'on')} onClick={() => toggle('locations', l)}>{l}</button>)}</div>
      </>}
      {v.kind === 'game' && <FormField label="Game port IPs" htmlFor="pd-ips" hint="Optional. Game servers take a port only from these allocation IPs, e.g. a dedicated gaming IP. Empty means any allocation.">
        <TextInput id="pd-ips" className="mono" value={p.ips.join(', ')} placeholder="203.0.113.20, 203.0.113.21" onChange={e => set({ ips: e.target.value.split(/[\s,]+/).filter(Boolean) })} />
      </FormField>}
      <label className="pd-check"><Switch label="Allow servers in maintenance" checked={p.allowMaintenance} onChange={allowMaintenance => set({ allowMaintenance })} />
        <span>Place on servers in maintenance too. Normally they're skipped until maintenance ends.</span></label>
    </section>

    <section className="pd-section">
      <h3>How a server is picked</h3>
      <div className="pd-strategies" role="radiogroup" aria-label="Placement strategy">
        {STRATEGIES.map(s => <button key={s.id} type="button" role="radio" aria-checked={p.strategy === s.id} className={cx('pd-runtime', p.strategy === s.id && 'on')} onClick={() => set({ strategy: s.id })}>
          <strong>{s.label}</strong><small>{s.text}</small></button>)}
      </div>
      {p.strategy === 'location' && <FormField label="Preferred location" htmlFor="pd-pref">
        <Select id="pd-pref" label="Preferred location" value={p.preferredLocation} onChange={preferredLocation => set({ preferredLocation })}
          options={[{ value: '', label: 'Choose a location' }, ...locations.map(l => ({ value: l, label: l }))]} />
      </FormField>}
    </section>

    <section className="pd-section">
      <h3>Capacity</h3>
      <div className="pd-row">
        <FormField label="Safety margin" htmlFor="pd-margin" hint="Share of each server never committed."><TextInput id="pd-margin" inputMode="numeric" suffix="%" value={String(p.safetyMarginPct)} onChange={num('safetyMarginPct')} /></FormField>
        <FormField label="Keep free: memory" htmlFor="pd-minmem" hint="After placing, in MB."><TextInput id="pd-minmem" inputMode="numeric" suffix="MB" value={String(p.minFreeMemoryMb)} onChange={num('minFreeMemoryMb')} /></FormField>
        <FormField label="Keep free: CPU" htmlFor="pd-mincpu" hint="After placing."><TextInput id="pd-mincpu" inputMode="decimal" suffix="cores" value={String(p.minFreeCpus)} onChange={num('minFreeCpus')} /></FormField>
      </div>
      <div className="pd-row">
        <FormField label="Per server" htmlFor="pd-perserver" hint="Most of this product on one server. 0 = no limit."><TextInput id="pd-perserver" inputMode="numeric" value={String(p.perServer)} onChange={num('perServer')} /></FormField>
        <FormField label="At once" htmlFor="pd-conc" hint="Most provisioning at the same time. 0 = no limit."><TextInput id="pd-conc" inputMode="numeric" value={String(p.maxConcurrent)} onChange={num('maxConcurrent')} /></FormField>
      </div>
    </section>

    <section className="pd-section">
      <h3>Availability</h3>
      <div className="pd-row">
        <FormField label="Stock" htmlFor="pd-stock" hint="Most active services in total. When they're all in use it shows as sold out. 0 = unlimited."><TextInput id="pd-stock" inputMode="numeric" value={String(p.stock)} onChange={num('stock')} /></FormField>
        <FormField label="Per team" htmlFor="pd-perteam" hint="Most active services one team can have. 0 = unlimited."><TextInput id="pd-perteam" inputMode="numeric" value={String(p.perTeam)} onChange={num('perTeam')} /></FormField>
      </div>
      <p className="pd-muted">It also shows as unavailable whenever no eligible server has room for a plan.</p>
    </section>

    {product?.id && <section className="pd-section">
      <h3>Right now <span className="pd-muted">(as saved, for the first plan)</span></h3>
      {avail.data ? <ServerTable avail={avail.data} /> : <p className="pd-muted">Checking servers…</p>}
    </section>}
    <p className="pd-note-honest">Ignition deploys services to this machine for now. Other servers you add are listed and can be made eligible, but they're shown as unable to take services until remote deployment arrives.</p>
  </div>;
}

export function ServerTable({ avail }) {
  return <div className="pd-servers">
    {avail.servers.map(s => <div key={s.id} className={cx('pd-server', s.ok && 'ok', avail.chosen?.id === s.id && 'chosen')}>
      <span className="pd-server-dot" aria-hidden="true" />
      <span className="pd-server-main"><strong>{s.name}{s.location && <small> · {s.location}</small>}{avail.chosen?.id === s.id && <em>Next service goes here</em>}</strong>
        <small>{s.ok ? `${memory(Math.max(s.freeMemoryMb, 0))} and ${Math.max(s.freeCpus, 0)} CPU free after the margin · ${s.services} of this product` : s.reason}</small></span>
    </div>)}
  </div>;
}

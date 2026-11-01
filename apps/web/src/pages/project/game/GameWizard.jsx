import { useEffect, useMemo, useRef, useState } from 'react';
import {
  ArrowLeftIcon, ArrowRightIcon, ArrowTopRightOnSquareIcon, CheckIcon, ChevronDownIcon, CubeTransparentIcon, MagnifyingGlassIcon, SparklesIcon,
} from '@heroicons/react/16/solid';
import { Button, Checkbox, FormField, Notice, SearchField, Select, Switch, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { useQuery } from '../../../api/hooks';
import { logos } from './logos';
import { compact } from './shared';
import './game.css';
import './setup.css';

/*
 * Add resource › Game server › <game>. A page, not a dialog: pick the kind of server, fill in the
 * details, review. A summary beside the form shows what you're about to get as you type. Everything
 * comes from the game's manifest (GET /api/games), so a new game appears here without changes.
 */

const STEPS = ['Server type', 'Details', 'Review'];
const groupNotes = { 'Java Edition': 'For players on Windows, macOS and Linux with Minecraft: Java Edition.', 'Bedrock Edition': 'For phones, tablets, consoles and Minecraft for Windows.' };
const slotPresets = [5, 10, 20, 50, 100];
const slug = s => s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 50).replace(/-+$/, '');
export const suggestMemory = (m, edition, slots) => {
  const mb = m.baseMb + m.perSlotMb * slots + (edition?.modded ? m.moddedMb : 0);
  return Math.min(Math.max(Math.ceil(mb / 512) * 512, m.minMb), m.maxMb);
};
export const memoryLabel = mb => mb % 1024 ? `${(mb / 1024).toFixed(1)} GB` : `${mb / 1024} GB`;

/** The edition's logo: each project's own mark (bundled, so nothing loads from elsewhere). */
export function EditionLogo({ id, className }) {
  const src = logos[id];
  if (src) return <img className={cx('gm-logo', className)} src={src} alt="" aria-hidden="true" />;
  return <span className={cx('gm-logo', className)} aria-hidden="true"><CubeTransparentIcon /></span>;
}

// A few versions most people want: the newest of each recent line (26.3, 26.2, 1.21.x, 1.20.x...).
function quickVersions(list) {
  const seen = new Set(), out = [];
  for (const v of list) {
    if (!v.stable) continue;
    const line = v.id.split('.').slice(0, 2).join('.');
    if (seen.has(line)) continue;
    seen.add(line);
    out.push(v.id);
    if (out.length === 5) break;
  }
  return out;
}

export function GameWizard({ gameId, project, environment, environmentId, onClose, onCreated }) {
  const catalog = useQuery(() => api.games(), []);
  const resources = useQuery(() => api.resources(project.id, { environment: environmentId }), [project.id, environmentId]);
  const takenNames = (resources.data?.resources ?? []).map(r => r.name);
  const game = catalog.data?.games.find(g => g.id === gameId) ?? catalog.data?.games[0];
  const [step, setStep] = useState(0);
  const [edition, setEdition] = useState(null);
  const [spec, setSpec] = useState({ version: '', loaderVersion: '', displayName: '', slots: 20, java: 0, customUrl: '', modpack: null, options: {} });
  const [name, setName] = useState('');
  const [nameTouched, setNameTouched] = useState(false);
  const [memory, setMemory] = useState(null);
  const [eula, setEula] = useState(false);
  const [errors, setErrors] = useState({});
  const [busy, setBusy] = useState(false);
  const top = useRef(null);
  const set = patch => setSpec(s => ({ ...s, ...patch }));
  const finalName = nameTouched ? name : slug(spec.displayName);
  const suggested = game && edition ? suggestMemory(game.memory, edition, spec.slots) : 2048;
  const mem = memory ?? suggested;

  useEffect(() => { if (game) setSpec(s => ({ ...s, slots: game.slots.default, options: Object.fromEntries(game.fields.map(f => [f.key, f.default])) })); }, [game]);
  useEffect(() => { top.current?.scrollIntoView({ block: 'start', behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' }); }, [step]);

  function chooseEdition(e) {
    if (e.id !== edition?.id) { setEdition(e); set({ version: '', loaderVersion: '', modpack: null, customUrl: '' }); setMemory(null); }
    setErrors({});
    setStep(1);
  }

  function validate() {
    const f = {};
    if (edition.versions === 'list' && !spec.version) f.version = 'Choose a version.';
    if (edition.modpacks && !spec.modpack) f.modpack = 'Choose a modpack.';
    if (edition.customJar && !/^https?:\/\/\S+$/.test(spec.customUrl.trim())) f.customUrl = 'Paste a direct download link to the .jar.';
    if (!spec.displayName.trim()) f.displayName = 'Name your server.';
    if (!/^[a-z0-9]([a-z0-9-]{0,48}[a-z0-9])?$/.test(finalName)) f.name = 'The resource name needs letters or numbers. Set one under More options.';
    else if (takenNames.includes(finalName)) f.name = `Something called ${finalName} already exists in ${environment}. Change the resource name under More options.`;
    if (!(spec.slots >= game.slots.min && spec.slots <= game.slots.max)) f.slots = `Between ${game.slots.min} and ${game.slots.max} players.`;
    setErrors(f);
    return !Object.keys(f).length;
  }

  async function create() {
    if (!eula) return setErrors({ eula: `Accept the ${game.eula.label} to continue.` });
    setBusy(true); setErrors({});
    try {
      const out = await api.createGame(project.id, {
        environmentId, game: game.id, name: finalName, memoryMb: mem, acceptEula: eula,
        spec: { ...spec, edition: edition.id, displayName: spec.displayName.trim(), customUrl: spec.customUrl.trim(), modpack: spec.modpack && { projectId: spec.modpack.id, versionId: spec.modpack.versionId ?? '' } },
      });
      onCreated(out.resource);
    } catch (e) {
      const f = e.fields ?? {};
      setErrors(Object.keys(f).length ? f : { form: e.message });
      if (Object.keys(f).some(k => k !== 'eula')) setStep(1);
      setBusy(false);
    }
  }

  const firstError = Object.values(errors)[0];
  if (catalog.error) return <div className="rp-page gm-page gm-setup"><Notice tone="danger" title="Couldn't load games">{catalog.error.message} <button type="button" className="inline-link" onClick={catalog.refetch}>Retry</button></Notice></div>;
  if (!game) return <div className="rp-page gm-page gm-setup"><p className="gm-muted">Loading…</p></div>;

  return <div className="rp-page gm-page gm-setup" ref={top}>
    <header className="gm-setup-head">
      <button type="button" className="icon-action" aria-label="Back to resources" title="Back to resources" onClick={onClose}><ArrowLeftIcon /></button>
      <div><h2>New {game.name} server</h2><p>in {project.name} · {environment}</p></div>
      <span className="gm-spacer" />
      <ol className="gm-steps" aria-label="Steps">
        {STEPS.map((s, i) => <li key={s}><button type="button" className={cx('gm-step', i === step && 'current', i < step && 'done')} disabled={i > step}
          aria-current={i === step ? 'step' : undefined} onClick={() => { setErrors({}); setStep(i); }}>{i < step ? <CheckIcon /> : <b>{i + 1}</b>}{s}</button></li>)}
      </ol>
    </header>

    {step === 0 ? <EditionStep game={game} selected={edition} onPick={chooseEdition} /> : <div className="gm-setup-grid">
      <div className="gm-setup-main">
        {step === 1 && <form className="gm-sections" onSubmit={e => { e.preventDefault(); if (validate()) setStep(2); }} noValidate>
          <VersionSection game={game} edition={edition} spec={spec} set={set} errors={errors} onChangeType={() => setStep(0)} />
          <ServerSection game={game} edition={edition} spec={spec} set={set} errors={errors} name={finalName} onName={v => { setName(v); setNameTouched(true); }}
            memory={mem} suggested={suggested} onMemory={setMemory} />
          <button type="submit" hidden />
        </form>}
        {step === 2 && <Review game={game} edition={edition} spec={spec} eula={eula} onEula={v => { setEula(v); setErrors({}); }} />}
      </div>
      <Summary game={game} edition={edition} spec={spec} name={finalName} memory={mem} project={project} environment={environment} />
    </div>}

    {step > 0 && <footer className="gm-footer">
      <Button icon={ArrowLeftIcon} onClick={() => { setErrors({}); setStep(s => s - 1); }}>Back</Button>
      {firstError && <span className="add-error" role="alert">{firstError}</span>}
      <span className="gm-spacer" />
      <Button onClick={onClose}>Cancel</Button>
      {step === 1 ? <Button variant="primary" onClick={() => validate() && setStep(2)}>Review<ArrowRightIcon className="gm-after-icon" /></Button>
        : <Button variant="primary" disabled={busy} onClick={create}>{busy ? 'Creating…' : 'Create server'}</Button>}
    </footer>}
  </div>;
}

function EditionStep({ game, selected, onPick }) {
  const groups = [...new Set(game.editions.map(e => e.group))];
  return <div className="gm-sections">
    {groups.map(g => <section key={g} className="gm-edition-group">
      <div className="gm-group-head"><h3>{g}</h3><p>{groupNotes[g]}</p></div>
      <div className="gm-editions">
        {game.editions.filter(e => e.group === g).map(e => <button key={e.id} type="button" className={cx('gm-edition', e.recommended && 'recommended', selected?.id === e.id && 'selected')} onClick={() => onPick(e)}>
          <span className="gm-edition-top"><EditionLogo id={e.id} />{e.recommended && <span className="rp-chip accent"><SparklesIcon />Recommended</span>}</span>
          <strong>{e.name}</strong>
          <span>{e.description}</span>
          <em>{e.content.length ? e.content.map(c => c.label).join(' + ') : e.modpacks ? 'Modpacks from Modrinth' : e.customJar ? 'Your own .jar' : 'No add-ons'}</em>
        </button>)}
      </div>
    </section>)}
  </div>;
}

function Section({ title, aside, children }) {
  return <section className="gm-section">
    <header><h3>{title}</h3>{aside}</header>
    <div className="gm-section-body">{children}</div>
  </section>;
}

function VersionSection({ game, edition, spec, set, errors, onChangeType }) {
  const change = <button type="button" className="gm-type-pill" onClick={onChangeType}><EditionLogo id={edition.id} />{edition.name}<span>Change</span></button>;
  if (edition.modpacks) return <Section title="Modpack" aside={change}><ModpackPicker game={game} spec={spec} set={set} error={errors.modpack} /></Section>;
  if (edition.customJar) return <Section title="Server jar" aside={change}>
    <FormField label="Download link" htmlFor="gm-jar" error={errors.customUrl} hint="A direct link to the .jar, for example from Purpur's or Folia's downloads. It's fetched on the first start.">
      <TextInput id="gm-jar" className="mono" placeholder="https://…/server.jar" value={spec.customUrl} onChange={e => set({ customUrl: e.target.value })} autoFocus />
    </FormField>
    <div className="gm-field-grid">
      <FormField label="Java" htmlFor="gm-java" hint="25 for 26.1+, 21 for 1.20.5–1.21, 17 for 1.17–1.20.4.">
        <Select id="gm-java" value={String(spec.java || 21)} onChange={v => set({ java: Number(v) })} options={[25, 21, 17, 11, 8].map(n => ({ value: String(n), label: `Java ${n}` }))} />
      </FormField>
      <FormField label="Minecraft version" htmlFor="gm-cv" hint="Optional; finds compatible plugins.">
        <TextInput id="gm-cv" className="mono" placeholder="1.21.1" value={spec.version} onChange={e => set({ version: e.target.value })} />
      </FormField>
    </div>
  </Section>;
  return <Section title="Version" aside={change}><VersionPicker game={game} edition={edition} spec={spec} set={set} errors={errors} quick /></Section>;
}

export function VersionPicker({ game, edition, spec, set, errors = {}, compactView = false, quick = false }) {
  const versions = useQuery(() => api.gameVersions(game.id, edition.id), [game.id, edition.id]);
  const [showAll, setShowAll] = useState(false);
  const [typed, setTyped] = useState(false);
  const [loaderOpen, setLoaderOpen] = useState(!!spec.loaderVersion);
  const list = versions.data?.versions ?? [];
  const shown = list.filter(v => showAll || v.stable || v.id === spec.version);
  useEffect(() => { if (versions.data && !spec.version) set({ version: versions.data.latest }); }, [versions.data]); // eslint-disable-line react-hooks/exhaustive-deps
  const loaders = versions.data?.loaders?.[spec.version] ?? versions.data?.loaders?.['*'] ?? [];
  const chips = quick ? quickVersions(list) : [];
  const free = typed || versions.error;
  return <div className={cx('gm-form', compactView && 'flat')}>
    {versions.data?.note && <p className="gm-note">{versions.data.note}</p>}
    {chips.length > 1 && !free && <div className="gm-chips" role="radiogroup" aria-label="Popular versions">
      {chips.map(v => <button key={v} type="button" role="radio" aria-checked={spec.version === v} className={cx('gm-chip', spec.version === v && 'on')} onClick={() => set({ version: v, loaderVersion: '' })}>
        {v}{v === versions.data.latest && <small>latest</small>}</button>)}
    </div>}
    <FormField label={chips.length > 1 ? 'Or any version' : 'Version'} htmlFor="gm-version" error={errors.version}
      hint={versions.error ? `The version list didn't load (${versions.error.message}). Type a version instead.` : !versions.data ? 'Loading versions…' : undefined}>
      {free ? <TextInput id="gm-version" className="mono" value={spec.version} placeholder="1.21.1" onChange={e => set({ version: e.target.value })} />
        : <Select id="gm-version" searchable={8} value={spec.version} onChange={v => set({ version: v, loaderVersion: '' })}
          options={shown.map(v => ({ value: v.id, label: v.id + (v.id === versions.data?.latest ? ' (latest)' : !v.stable ? ' (preview)' : '') }))} />}
    </FormField>
    <div className="gm-inline-options">
      {list.some(v => !v.stable) && !free && <label className="gm-toggle"><Switch label="Show previews" checked={showAll} onChange={setShowAll} />Show {edition.snapshots ? 'snapshots and pre-releases' : 'betas and previews'}</label>}
      {versions.data && <button type="button" className="inline-link" onClick={() => setTyped(t => !t)}>{typed ? 'Pick from the list' : 'Type a version'}</button>}
    </div>
    {edition.loader && (loaderOpen
      ? <FormField label={edition.loader} htmlFor="gm-loader" error={errors.loaderVersion} hint="Latest picks the newest for this version. Pin one only if your mods need it.">
        <Select id="gm-loader" searchable={8} value={spec.loaderVersion} onChange={v => set({ loaderVersion: v })} options={[{ value: '', label: 'Latest' }, ...loaders]} />
      </FormField>
      : <button type="button" className="gm-disclosure" onClick={() => setLoaderOpen(true)}><ChevronDownIcon />{edition.loader}: Latest</button>)}
  </div>;
}

export function ModpackPicker({ game, spec, set, error }) {
  const [query, setQuery] = useState('');
  const [debounced, setDebounced] = useState('');
  useEffect(() => { const t = setTimeout(() => setDebounced(query), 300); return () => clearTimeout(t); }, [query]);
  const results = useQuery(() => api.gameModpacks(game.id, { q: debounced, sort: debounced ? 'relevance' : 'downloads' }), [game.id, debounced]);
  const versions = useQuery(() => spec.modpack ? api.gameModpackVersions(game.id, spec.modpack.id) : Promise.resolve(null), [spec.modpack?.id]);
  const picked = spec.modpack;
  if (picked) return <div className="gm-form">
    <div className="gm-pack picked">
      {picked.iconUrl ? <img src={picked.iconUrl} alt="" /> : <span className="gm-pack-icon" />}
      <span><strong>{picked.title}</strong><small>{picked.description}</small></span>
      <Button onClick={() => set({ modpack: null })}>Change</Button>
    </div>
    <FormField label="Modpack version" htmlFor="gm-pv" hint="The newest release unless you pick one.">
      <Select id="gm-pv" searchable={8} value={picked.versionId ?? ''} onChange={v => set({ modpack: { ...picked, versionId: v || undefined } })}
        options={[{ value: '', label: 'Newest release' }, ...(versions.data?.versions ?? []).map(v => ({ value: v.id, label: `${v.versionNumber} · ${v.gameVersions.at(-1) ?? ''} ${v.loaders.join('/')}${v.versionType !== 'release' ? ` (${v.versionType})` : ''}` }))]} />
    </FormField>
  </div>;
  return <div className="gm-form">
    <SearchField value={query} onChange={setQuery} placeholder="Search Modrinth modpacks" />
    {error && <p className="gm-error">{error}</p>}
    {results.error && <Notice tone="danger" title="Couldn't search Modrinth">{results.error.message}</Notice>}
    <div className="gm-packs">
      {(results.data?.hits ?? []).map(p => <button key={p.id} type="button" className="gm-pack" onClick={() => set({ modpack: { id: p.id, title: p.title, iconUrl: p.iconUrl, description: p.description } })}>
        {p.iconUrl ? <img src={p.iconUrl} alt="" loading="lazy" /> : <span className="gm-pack-icon" />}
        <span><strong>{p.title}</strong><small>{p.description}</small></span>
        <em>{compact(p.downloads)} downloads</em>
      </button>)}
      {results.data && !results.data.hits.length && <p className="gm-muted">No modpacks match.</p>}
      {!results.data && !results.error && <p className="gm-muted"><MagnifyingGlassIcon className="gm-inline-icon" />Searching…</p>}
    </div>
  </div>;
}

function ServerSection({ game, edition, spec, set, errors, name, onName, memory, suggested, onMemory }) {
  const fields = game.fields.filter(f => !f.for?.length || f.for.includes(edition.id));
  const [more, setMore] = useState(false);
  const memoryOptions = useMemo(() => {
    const opts = [];
    for (let mb = game.memory.minMb; mb <= Math.min(game.memory.maxMb, 32768); mb += mb < 4096 ? 512 : 1024) opts.push(mb);
    if (!opts.includes(memory)) opts.push(memory);
    return opts.sort((a, b) => a - b).map(mb => ({ value: String(mb), label: memoryLabel(mb) + (mb === suggested ? ' · suggested' : '') }));
  }, [game, memory, suggested]);
  const opt = (k, v) => set({ options: { ...spec.options, [k]: v } });
  useEffect(() => { if (errors.name) setMore(true); }, [errors.name]);
  return <>
    <Section title="Your server">
      <FormField label="Server name" htmlFor="gm-name" error={errors.displayName} hint="Shown in players' server lists. You can change it later.">
        <TextInput id="gm-name" value={spec.displayName} placeholder="e.g. Friends SMP" maxLength={59} autoFocus onChange={e => set({ displayName: e.target.value })} />
      </FormField>
      <FormField label="Player slots" htmlFor="gm-slots" error={errors.slots} hint="How many can play at the same time.">
        <div className="gm-slots">
          <div className="gm-chips" role="radiogroup" aria-label="Common sizes">
            {slotPresets.map(n => <button key={n} type="button" role="radio" aria-checked={spec.slots === n} className={cx('gm-chip', spec.slots === n && 'on')} onClick={() => set({ slots: n })}>{n}</button>)}
          </div>
          <TextInput id="gm-slots" type="number" min={game.slots.min} max={game.slots.max} value={spec.slots} aria-label="Player slots" onChange={e => set({ slots: Number(e.target.value) || 0 })} />
        </div>
      </FormField>
      <FormField label="Memory" htmlFor="gm-mem" error={errors.memoryMb} hint={memory === suggested ? `Fits ${spec.slots} players${edition.modded ? ' on a modded server' : ''}. Big modpacks may need more.` : `${memoryLabel(suggested)} is suggested for ${spec.slots} players.`}>
        <Select id="gm-mem" value={String(memory)} onChange={v => onMemory(Number(v))} options={memoryOptions} />
      </FormField>
    </Section>
    <Section title="Gameplay" aside={<span className="gm-muted gm-small">Change any time in Server Settings</span>}>
      <div className="gm-field-grid">
        {fields.filter(f => f.kind === 'select').map(f => <FormField key={f.key} label={f.label} htmlFor={`gm-${f.key}`} error={errors[f.key]}>
          <Select id={`gm-${f.key}`} value={spec.options[f.key] ?? f.default} onChange={v => opt(f.key, v)} options={f.options} />
        </FormField>)}
      </div>
      {fields.filter(f => f.kind === 'bool').map(f => <div key={f.key} className="gm-check" onClick={() => opt(f.key, String(spec.options[f.key] !== 'true'))}>
        <Checkbox checked={spec.options[f.key] === 'true'} label={f.label} onChange={v => opt(f.key, String(v))} />
        <span><strong>{f.label}</strong>{f.hint && <small>{f.hint}</small>}</span></div>)}
      {more ? <>
        {fields.filter(f => f.kind === 'text').map(f => <FormField key={f.key} label={f.label} htmlFor={`gm-${f.key}`} hint={f.hint} error={errors[f.key]}>
          <TextInput id={`gm-${f.key}`} className="mono" value={spec.options[f.key] ?? ''} onChange={e => opt(f.key, e.target.value)} />
        </FormField>)}
        <FormField label="Resource name" htmlFor="gm-res" error={errors.name} hint="How Ignition names it in URLs, logs and the internal network.">
          <TextInput id="gm-res" className="mono" value={name} onChange={e => onName(e.target.value)} />
        </FormField>
      </> : <button type="button" className="gm-disclosure" onClick={() => setMore(true)}><ChevronDownIcon />More options: seed, resource name</button>}
    </Section>
  </>;
}

function Summary({ game, edition, spec, name, memory, project, environment }) {
  const version = edition.modpacks ? spec.modpack?.title ?? 'Pick a modpack' : edition.customJar ? (spec.version || 'Custom JAR') : spec.version || '…';
  return <aside className="gm-summary" aria-label="Summary">
    <div className="gm-summary-head"><EditionLogo id={edition.id} className="large" />
      <span><strong>{spec.displayName.trim() || 'Your server'}</strong><small>{game.name} {edition.name} · {version}</small></span></div>
    <dl>
      <div><dt>Players</dt><dd>{spec.slots}</dd></div>
      <div><dt>Memory</dt><dd>{memoryLabel(memory)}</dd></div>
      {edition.content.length > 0 && <div><dt>Add-ons</dt><dd>{edition.content.map(c => c.label).join(', ')}</dd></div>}
      <div><dt>Resource</dt><dd className="mono">{name || '—'}</dd></div>
      <div><dt>Where</dt><dd>{project.name} / {environment}</dd></div>
    </dl>
    <p>Players join on port {edition.group === 'Bedrock Edition' ? '19132 (UDP)' : '25565'}, or the next free one. The address shows on the overview once it's up.</p>
  </aside>;
}

function Review({ game, edition, spec, eula, onEula }) {
  const steps = [
    `Download ${game.name} ${edition.name}${spec.version ? ` ${spec.version}` : ''}${edition.modded ? ' and the mod loader' : ''}`,
    'Generate the world',
    'Open the port for players',
  ];
  return <div className="gm-sections">
    <section className="gm-section">
      <header><h3>What happens next</h3></header>
      <div className="gm-section-body">
        <ol className="gm-next">{steps.map(s => <li key={s}>{s}</li>)}</ol>
        <p className="gm-note">The first start takes about a minute, a few for modded servers. You'll land on the server's overview and can follow along in the console.</p>
      </div>
    </section>
    {game.eula && <div className={cx('gm-check gm-eula', eula && 'on')} onClick={e => e.target.closest('a') || onEula(!eula)}>
      <Checkbox checked={eula} label="Accept the licence" onChange={onEula} />
      <span><strong>I accept the <a href={game.eula.url} target="_blank" rel="noreferrer">{game.eula.label}<ArrowTopRightOnSquareIcon className="gm-inline-icon" /></a></strong>
        <small>Mojang requires it before a server can run. Ignition records who accepted it and when.</small></span>
    </div>}
  </div>;
}

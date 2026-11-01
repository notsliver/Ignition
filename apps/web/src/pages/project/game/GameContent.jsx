import { useEffect, useMemo, useState } from 'react';
import { ArrowPathIcon, ArrowTopRightOnSquareIcon, ArrowUpTrayIcon, CheckIcon, PuzzlePieceIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, Notice, SearchField, SegmentedControl, Select, Switch, cx } from '../../../ui';
import { api } from '../../../api/client';
import { useQuery } from '../../../api/hooks';
import { useSession, can } from '../../../app/AuthGate';
import { Empty } from '../resource/shared';
import { RestartNotice, bytes, compact, pickFile, plural } from './shared';
import './game.css';

/*
 * Mods and plugins. Installed shows the jars actually in the server's folders, identified on
 * Modrinth by their hash (so hand-copied files are recognised too) with any newer compatible
 * version. Browse searches Modrinth for things that run on this server's loader and version, and
 * installing pulls in required dependencies. Changes apply when the server restarts.
 */

export function GameContent({ resource, onNavigate }) {
  const { user } = useSession();
  const canManage = can(user, 'games.content');
  const installed = useQuery(() => api.gameContent(resource.id), [resource.id]);
  const [tab, setTab] = useState('installed');
  const [kindId, setKindId] = useState(null);
  const [restart, setRestart] = useState('');
  const [result, setResult] = useState(null);
  const [busy, setBusy] = useState('');

  const d = installed.data;
  if (installed.error && !d) return <div className="rp-page gm-page"><Notice tone="danger" title="Couldn't read mods and plugins">{installed.error.message}</Notice></div>;
  if (!d) return <div className="rp-page gm-page"><p className="gm-muted">Reading the server's folders…</p></div>;
  if (!d.kinds.length) return <div className="rp-page gm-page"><Empty icon={PuzzlePieceIcon} title="This server type runs without mods or plugins">
    Switch to Paper for plugins, or Fabric, NeoForge or Forge for mods, from the overview's Change type. Bedrock add-ons can be uploaded under Files.
    <><br /><button type="button" className="inline-link" onClick={() => onNavigate('Overview')}>Open overview</button></>
  </Empty></div>;
  const kind = d.kinds.find(k => k.id === kindId) ?? d.kinds[0];
  const items = d.items.filter(i => i.kind === kind.id);
  const updates = items.filter(i => i.update);

  async function change(item, body, label) {
    setBusy(item.file); setResult(null);
    try {
      const out = await api.gameContentChange(resource.id, { kind: item.kind, file: item.file, ...body });
      if (out.restartNeeded) setRestart(`${label}. Restart the server to apply it.`);
      installed.refetch();
    } catch (e) { setResult({ tone: 'danger', text: e.message }); }
    setBusy('');
  }
  async function updateAll() {
    for (const it of updates) await change(it, { update: true }, `Updated ${updates.length} ${kind.label.toLowerCase()}`); // eslint-disable-line no-await-in-loop
  }
  function upload() {
    pickFile('.jar', async file => {
      setBusy('upload'); setResult(null);
      try {
        const out = await api.gameContentUpload(resource.id, kind.id, file);
        setResult({ tone: 'success', text: `Uploaded ${out.file}.` });
        if (out.restartNeeded) setRestart(`Uploaded ${out.file}. Restart the server to load it.`);
        installed.refetch();
      } catch (e) { setResult({ tone: 'danger', text: e.message }); }
      setBusy('');
    });
  }

  return <div className="rp-page gm-page gm-content">
    <div className="rp-toolbar">
      <SegmentedControl label="View" className="auto-width" value={tab} onChange={setTab}
        options={[{ value: 'installed', label: `Installed${items.length ? ` · ${items.length}` : ''}` }, { value: 'browse', label: `Browse ${kind.label.toLowerCase()}` }]} />
      {d.kinds.length > 1 && <SegmentedControl label="Kind" className="auto-width" value={kind.id} onChange={setKindId} options={d.kinds.map(k => ({ value: k.id, label: k.label }))} />}
      <span className="spacer" />
      {tab === 'installed' && canManage && <>
        {updates.length > 0 && <Button icon={ArrowPathIcon} disabled={!!busy} onClick={updateAll}>Update all ({updates.length})</Button>}
        <Button icon={ArrowUpTrayIcon} disabled={!!busy} onClick={upload}>{busy === 'upload' ? 'Uploading…' : 'Upload .jar'}</Button>
      </>}
    </div>
    {restart && <RestartNotice resource={resource} text={restart} onDone={() => setRestart('')} />}
    {result && <Notice tone={result.tone}>{result.text}</Notice>}
    {d.sourceError && <Notice tone="warning" title="Modrinth didn't answer">{d.sourceError} Files are listed by name.</Notice>}

    {tab === 'installed' ? (items.length ? <div className="rp-card">
      {items.map(it => <div key={it.file} className={cx('rp-row gm-mod', !it.enabled && 'off')}>
        {it.project?.iconUrl ? <img src={it.project.iconUrl} alt="" className="gm-mod-icon" loading="lazy" /> : <span className="gm-mod-icon blank"><PuzzlePieceIcon /></span>}
        <span className="rp-row-main">
          <strong>{it.project?.title ?? it.file.replace(/\.jar(\.disabled)?$/, '')}{!it.enabled && <span className="rp-chip">Off</span>}
            {it.project?.serverSide === 'unsupported' && <span className="rp-chip warning" title="Its author says it doesn't run on servers">Client only</span>}</strong>
          <span>{it.version ? `${it.version.versionNumber}` : 'Not on Modrinth'} · <span className="mono">{it.file}</span> · {bytes(it.size)}</span>
        </span>
        {it.update && canManage && <Button icon={ArrowPathIcon} disabled={!!busy} title={`Update to ${it.update.versionNumber}`} onClick={() => change(it, { update: true }, `Updated ${it.project?.title ?? it.file}`)}>
          {busy === it.file ? 'Updating…' : `Update to ${it.update.versionNumber}`}</Button>}
        {it.project && <a className="icon-action" href={it.project.url} target="_blank" rel="noreferrer" title="Open on Modrinth" aria-label="Open on Modrinth"><ArrowTopRightOnSquareIcon /></a>}
        {canManage && <Switch label={`${it.enabled ? 'Turn off' : 'Turn on'} ${it.file}`} checked={it.enabled} disabled={!!busy}
          onChange={on => change(it, { enabled: on }, `${on ? 'Turned on' : 'Turned off'} ${it.project?.title ?? it.file}`)} />}
        {canManage && <button type="button" className="icon-action danger" disabled={!!busy} aria-label={`Remove ${it.file}`} title="Remove"
          onClick={() => window.confirm(`Remove ${it.project?.title ?? it.file}? The file is deleted from the server.`) && change(it, { remove: true }, `Removed ${it.project?.title ?? it.file}`)}><TrashIcon /></button>}
      </div>)}
    </div> : <Empty icon={PuzzlePieceIcon} title={`No ${kind.label.toLowerCase()} yet`} action={<Button variant="primary" onClick={() => setTab('browse')}>Browse {kind.label.toLowerCase()}</Button>}>
      Find {kind.label.toLowerCase()} for {kind.loaders.join('/')} {d.gameVersion} on Modrinth, or upload a .jar.
    </Empty>) : <Browse resource={resource} kind={kind} gameVersion={d.gameVersion} items={d.items} canManage={canManage}
      onInstalled={(out, title) => { setResult({ tone: 'success', text: `Installed ${out.installed.map(i => `${i.title} ${i.version}${i.reason === 'required' ? ' (required)' : ''}`).join(', ')}.` }); if (out.restartNeeded) setRestart(`Installed ${title}. Restart the server to load it.`); installed.refetch(); }}
      onError={text => setResult({ tone: 'danger', text })} />}
  </div>;
}

function Browse({ resource, kind, gameVersion, items, canManage, onInstalled, onError }) {
  const [query, setQuery] = useState('');
  const [debounced, setDebounced] = useState('');
  const [sort, setSort] = useState('downloads');
  const [offset, setOffset] = useState(0);
  const [hits, setHits] = useState([]);
  const [busy, setBusy] = useState('');
  const [versionsFor, setVersionsFor] = useState(null);
  useEffect(() => { const t = setTimeout(() => { setDebounced(query); setOffset(0); }, 300); return () => clearTimeout(t); }, [query]);
  useEffect(() => { setOffset(0); }, [sort, kind.id]);
  const search = useQuery(() => api.gameContentSearch(resource.id, { kind: kind.id, q: debounced, sort: debounced && sort === 'downloads' ? 'relevance' : sort, offset }), [resource.id, kind.id, debounced, sort, offset]);
  useEffect(() => { if (search.data) setHits(h => offset ? [...h, ...search.data.hits] : search.data.hits); }, [search.data]); // eslint-disable-line react-hooks/exhaustive-deps
  const installedIds = useMemo(() => new Set(items.filter(i => i.project).map(i => i.project.id)), [items]);

  async function install(p, versionId) {
    setBusy(p.id);
    try { onInstalled(await api.gameContentInstall(resource.id, { kind: kind.id, projectId: p.id, versionId }), p.title); } catch (e) { onError(e.message); }
    setBusy('');
    setVersionsFor(null);
  }

  return <>
    <div className="rp-toolbar">
      <SearchField value={query} onChange={setQuery} placeholder={`Search ${kind.label.toLowerCase()} on Modrinth`} />
      <Select label="Sort" value={sort} onChange={setSort} options={[{ value: 'downloads', label: 'Most downloaded' }, { value: 'follows', label: 'Most followed' }, { value: 'updated', label: 'Recently updated' }, { value: 'newest', label: 'Newest' }, { value: 'relevance', label: 'Best match' }]} />
      <span className="gm-muted gm-small">Showing what runs on {kind.loaders.join(', ')} {gameVersion}{search.data ? ` · ${search.data.total.toLocaleString()} found` : ''}</span>
    </div>
    {search.error && <Notice tone="danger" title="Couldn't search Modrinth">{search.error.message}</Notice>}
    <div className="rp-card gm-results">
      {hits.map(p => {
        const has = installedIds.has(p.id);
        return <div key={p.id} className="rp-row gm-mod">
          {p.iconUrl ? <img src={p.iconUrl} alt="" className="gm-mod-icon" loading="lazy" /> : <span className="gm-mod-icon blank"><PuzzlePieceIcon /></span>}
          <span className="rp-row-main">
            <strong><a href={p.url} target="_blank" rel="noreferrer">{p.title}</a>{p.author && <small className="gm-muted"> by {p.author}</small>}</strong>
            <span>{p.description}</span>
            <span className="gm-meta">{compact(p.downloads)} downloads · {p.categories.filter(c => !kind.loaders.includes(c)).slice(0, 4).join(', ')}{p.serverSide === 'required' ? ' · server side' : ''}</span>
          </span>
          {canManage && (has ? <span className="rp-chip success"><CheckIcon />Installed</span> : <>
            <button type="button" className="inline-link gm-small" onClick={() => setVersionsFor(p)}>Versions</button>
            <Button variant="primary" disabled={!!busy} onClick={() => install(p)}>{busy === p.id ? 'Installing…' : 'Install'}</Button>
          </>)}
        </div>;
      })}
      {search.data && !hits.length && <p className="gm-pad gm-muted">Nothing matches for {kind.loaders.join('/')} {gameVersion}.</p>}
      {!search.data && !search.error && <p className="gm-pad gm-muted">Searching…</p>}
    </div>
    {search.data && hits.length < search.data.total && <Button onClick={() => setOffset(hits.length)} disabled={search.loading}>Show more</Button>}
    {versionsFor && <VersionsDialog resource={resource} kind={kind} project={versionsFor} busy={busy === versionsFor.id} onClose={() => setVersionsFor(null)} onInstall={v => install(versionsFor, v)} />}
  </>;
}

function VersionsDialog({ resource, kind, project, busy, onClose, onInstall }) {
  const q = useQuery(() => api.gameContentVersions(resource.id, { kind: kind.id, project: project.id }), [project.id]);
  return <Dialog open onClose={onClose} title={`${project.title} versions`} description="Versions that run on this server, newest first." className="gm-dialog">
    {q.error && <Notice tone="danger">{q.error.message}</Notice>}
    {!q.data && !q.error && <p className="gm-muted">Loading…</p>}
    {q.data && !q.data.versions.length && <p className="gm-muted">No version fits this server's loader and Minecraft version.</p>}
    <div className="gm-version-list">
      {q.data?.versions.slice(0, 50).map(v => <div key={v.id} className="rp-row">
        <span className="rp-row-main"><strong>{v.versionNumber}{v.versionType !== 'release' && <span className="rp-chip warning">{v.versionType}</span>}</strong>
          <span>{new Date(v.published).toLocaleDateString()} · {plural(v.downloads, 'download')} · {v.dependencies.filter(d => d.type === 'required').length} required dependencies</span></span>
        <Button disabled={busy} onClick={() => onInstall(v.id)}>Install</Button>
      </div>)}
    </div>
  </Dialog>;
}

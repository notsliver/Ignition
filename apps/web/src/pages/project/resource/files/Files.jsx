import { Suspense, lazy, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ArchiveBoxArrowDownIcon, ArrowDownTrayIcon, ArrowPathIcon, ArrowTopRightOnSquareIcon, ArrowUpTrayIcon, ArrowUturnLeftIcon, ArrowsRightLeftIcon,
  ChevronRightIcon, ClipboardDocumentIcon, ClipboardIcon, CodeBracketIcon, DocumentDuplicateIcon, DocumentIcon, DocumentPlusIcon,
  DocumentTextIcon, EllipsisHorizontalIcon, FolderIcon, FolderOpenIcon, FolderPlusIcon, InformationCircleIcon, LinkIcon, PencilIcon,
  PhotoIcon, ScissorsIcon, ShieldCheckIcon, TrashIcon, ViewColumnsIcon, XMarkIcon, ArchiveBoxIcon, FolderArrowDownIcon,
} from '@heroicons/react/16/solid';
import { Button, Checkbox, MenuPopover, Notice, SearchField, Select, Switch, TextInput, cx } from '../../../../ui';
import { api } from '../../../../api/client';
import { timeAgo, useQuery } from '../../../../api/hooks';
import { useSession, can } from '../../../../app/AuthGate';
import { useUnsavedChanges } from '../../../../app/unsaved';
import { Empty } from '../shared';
import './Files.css';

/*
 * The files in a resource's volumes, the same for every kind of resource. A list on the left, and
 * VS Code's editor on the right where files open as tabs (two side by side if you like). Everything
 * a file manager does is in the ⋯ menu and on right-click. Details, permissions, sharing and
 * "move to" open as tabs too, so nothing covers the page.
 */

const CodeEditor = lazy(() => import('./CodeEditor'));
const IMAGE = /\.(png|jpe?g|gif|webp|bmp|ico|avif)$/i;
const ARCHIVE = /\.(zip|jar|tar|tar\.gz|tgz)$/i;
const TEXTISH = /\.(properties|ya?ml|json5?|toml|txt|cfg|conf|ini|log|md|mcmeta|sh|xml|csv|[cm]?js|ts|jsx|tsx|css|html?|lang|snbt|env|py|go|rs|java|kt|sql|lua|rb|php|service|gitignore|dockerignore)$/i;
const bytes = n => n == null ? '' : n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(0)} KB` : n < 1 << 30 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${(n / (1 << 30)).toFixed(2)} GB`;
const parentOf = p => p.includes('/') ? p.slice(0, p.lastIndexOf('/')) : '';
const baseOf = p => p.split('/').pop();
const join = (dir, name) => (dir ? `${dir}/${name}` : name);
const iconFor = e => e.dir ? FolderIcon : IMAGE.test(e.name) ? PhotoIcon : ARCHIVE.test(e.name) ? ArchiveBoxIcon : TEXTISH.test(e.name) ? DocumentTextIcon : /\.(js|ts|go|py|java)$/.test(e.name) ? CodeBracketIcon : DocumentIcon;
const clipboards = new Map(); // per resource, survives page switches

export function Files({ resource, onNavigate }) {
  const { user } = useSession();
  const canEdit = can(user, 'files.edit');
  const canShare = can(user, 'files.share');
  const volumes = useQuery(() => api.fileVolumes(resource.id), [resource.id]);
  const [volume, setVolume] = useState(null);
  const vols = volumes.data?.volumes ?? [];
  const vol = volume ?? vols[0]?.name;

  if (volumes.error && !volumes.data) return <div className="rp-page"><Notice tone="danger" title="Couldn't load volumes">{volumes.error.message}</Notice></div>;
  if (!volumes.data) return <div className="rp-page"><p className="fx-muted">Loading…</p></div>;
  if (!vols.length) return <div className="rp-page"><Empty icon={FolderIcon} title="No volumes to browse">
    {resource.name} keeps no files between deployments: anything it writes is lost when its container is replaced. Add a volume to keep files, and they show up here.
    <><br /><button type="button" className="inline-link" onClick={() => onNavigate('Storage')}>Open Storage</button></>
  </Empty></div>;
  return <Browser key={`${resource.id}:${vol}`} resource={resource} volumes={vols} volume={vol} onVolume={setVolume} canEdit={canEdit} canShare={canShare} />;
}

function Browser({ resource, volumes, volume, onVolume, canEdit, canShare }) {
  const rid = resource.id;
  const [dir, setDir] = useState('');
  const [trashView, setTrashView] = useState(false);
  const list = useQuery(() => api.files(rid, volume, dir), [rid, volume, dir]);
  const [filter, setFilter] = useState('');
  const [selected, setSelected] = useState(() => new Set());
  const anchor = useRef(null);
  const [menu, setMenu] = useState(null); // { anchor, items }
  const [renaming, setRenaming] = useState(null); // path
  const [creating, setCreating] = useState(null); // 'file' | 'folder'
  const [notice, setNotice] = useState(null);
  const [busy, setBusy] = useState('');
  const [drag, setDrag] = useState(false);
  const [clip, setClip] = useState(() => clipboards.get(rid) ?? null);
  // Editor groups: tabs and which one is showing in each of up to two groups.
  const [tabs, setTabs] = useState([]);
  const [active, setActive] = useState([null, null]);
  const [focusGroup, setFocusGroup] = useState(0);
  const dirty = tabs.some(t => t.dirty);
  useUnsavedChanges(dirty);

  useEffect(() => { setSelected(new Set()); setFilter(''); setRenaming(null); setCreating(null); }, [dir]);
  const entries = useMemo(() => (list.data?.entries ?? []).filter(e => !filter || e.name.toLowerCase().includes(filter.toLowerCase())), [list.data, filter]);
  const byPath = useMemo(() => Object.fromEntries(entries.map(e => [e.path, e])), [entries]);
  const say = (tone, text) => setNotice({ tone, text });
  const refresh = () => list.refetch();
  const setClipboard = c => { clipboards.set(rid, c); setClip(c); };

  async function op(body, done) {
    setBusy(body.op); setNotice(null);
    try { const out = await api.fileOp(rid, volume, body); if (done) say('success', typeof done === 'function' ? done(out) : done); refresh(); return out ?? {}; } catch (e) { say('danger', e.message); return null; } finally { setBusy(''); }
  }

  /* ----------------------------- Tabs and editors ----------------------------- */
  const keyOf = (kind, path) => `${volume}:${kind}:${path}`;
  function openTab(tab, group = focusGroup) {
    const key = keyOf(tab.kind, tab.path);
    setTabs(ts => ts.some(t => t.key === key && t.group === group) ? ts : [...ts, { ...tab, key, group }]);
    setActive(a => { const n = [...a]; n[group] = key; return n; });
    setFocusGroup(group);
    if (tab.kind === 'file') loadFile(key, tab.path, tab.asText);
  }
  async function loadFile(key, path, asText) {
    setTabs(ts => ts.map(t => t.key === key ? { ...t, loading: true, error: '' } : t));
    try {
      const out = await api.fileContent(rid, volume, path, asText);
      setTabs(ts => ts.map(t => t.key === key ? { ...t, loading: false, binary: out.binary && !asText, tooLarge: out.tooLarge, size: out.size, content: out.content ?? null, saved: out.content ?? null, dirty: false } : t));
    } catch (e) { setTabs(ts => ts.map(t => t.key === key ? { ...t, loading: false, error: e.message } : t)); }
  }
  function open(e, { side = false, asText = false } = {}) {
    if (e.dir) { setDir(e.path); return; }
    const group = side ? 1 : focusGroup;
    if (IMAGE.test(e.name) && !asText) return openTab({ kind: 'image', path: e.path, title: e.name }, group);
    openTab({ kind: 'file', path: e.path, title: e.name, asText }, group);
  }
  function closeTab(t) {
    if (t.dirty && !window.confirm(`${t.title} has unsaved changes. Close it anyway?`)) return;
    setTabs(ts => {
      const rest = ts.filter(x => !(x.key === t.key && x.group === t.group));
      setActive(a => {
        const n = [...a];
        if (n[t.group] === t.key) n[t.group] = rest.filter(x => x.group === t.group).at(-1)?.key ?? null;
        if (!rest.some(x => x.group === 1)) n[1] = null;
        return n;
      });
      return rest.map(x => (rest.some(y => y.group === 0) ? x : { ...x, group: 0 })); // keep a left group
    });
  }
  async function save(t) {
    if (!t?.dirty || !canEdit) return;
    try {
      await api.saveFile(rid, volume, t.path, t.content);
      setTabs(ts => ts.map(x => x.key === t.key ? { ...x, saved: x.content, dirty: false } : x));
      say('success', `Saved ${t.path}.`);
    } catch (e) { say('danger', `${t.title}: ${e.message}`); }
  }

  /* -------------------------------- Selection -------------------------------- */
  const sel = [...selected];
  function clickRow(e, ev) {
    if (ev.shiftKey && anchor.current) {
      const a = entries.findIndex(x => x.path === anchor.current), b = entries.findIndex(x => x.path === e.path);
      const [lo, hi] = a < b ? [a, b] : [b, a];
      setSelected(new Set(entries.slice(lo, hi + 1).map(x => x.path)));
      return;
    }
    if (ev.ctrlKey || ev.metaKey) {
      setSelected(s => { const n = new Set(s); n.has(e.path) ? n.delete(e.path) : n.add(e.path); return n; });
    } else setSelected(new Set([e.path]));
    anchor.current = e.path;
  }

  /* --------------------------------- Actions --------------------------------- */
  const paths = target => (selected.has(target.path) && selected.size > 1 ? sel : [target.path]);
  const label = ps => (ps.length === 1 ? baseOf(ps[0]) : `${ps.length} items`);
  const copyText = text => navigator.clipboard?.writeText(text).then(() => say('success', `Copied ${text}`), () => say('danger', 'The browser didn\'t allow copying.'));
  async function remove(ps, permanent) {
    if (permanent && !window.confirm(`Delete ${label(ps)} permanently? This can't be undone.`)) return;
    await op({ op: permanent ? 'delete' : 'trash', paths: ps }, permanent ? `Deleted ${label(ps)}.` : `Moved ${label(ps)} to the trash.`);
    setSelected(new Set());
    setTabs(ts => ts.filter(t => !ps.some(p => t.path === p || t.path.startsWith(`${p}/`)) || t.kind === 'info'));
  }
  function upload(files, into = dir) {
    files = [...files];
    if (!files.length || !canEdit) return;
    (async () => {
      setNotice(null);
      for (const [i, f] of files.entries()) {
        setBusy(`Uploading ${i + 1} of ${files.length}: ${f.name}`);
        try { await api.uploadFile(rid, volume, into, f); } catch (e) { say('danger', `${f.name}: ${e.message}`); setBusy(''); refresh(); return; } // eslint-disable-line no-await-in-loop
      }
      setBusy('');
      say('success', `Uploaded ${files.length === 1 ? files[0].name : `${files.length} files`}.`);
      refresh();
    })();
  }
  const pickUpload = () => { const i = Object.assign(document.createElement('input'), { type: 'file', multiple: true }); i.onchange = () => upload(i.files); i.click(); };
  async function paste(into = dir) {
    if (!clip || clip.volume !== volume) return;
    const out = await op({ op: clip.mode === 'cut' ? 'move' : 'copy', paths: clip.paths, to: into }, `${clip.mode === 'cut' ? 'Moved' : 'Copied'} ${label(clip.paths)}.`);
    if (out && clip.mode === 'cut') setClipboard(null);
  }
  const download = (p, inline) => { const a = Object.assign(document.createElement('a'), { href: api.fileDownloadUrl(rid, volume, p, inline), target: inline ? '_blank' : '', rel: 'noreferrer' }); if (!inline) a.download = ''; document.body.append(a); a.click(); a.remove(); };

  function itemMenu(e) {
    const ps = paths(e);
    const many = ps.length > 1;
    return [
      ...(!many ? [{ label: 'Open', icon: e.dir ? FolderOpenIcon : ArrowTopRightOnSquareIcon, onSelect: () => open(e) }] : []),
      ...(!many && !e.dir ? [
        { label: 'Open to the side', icon: ViewColumnsIcon, onSelect: () => open(e, { side: true }) },
        { label: 'Open as text', icon: DocumentTextIcon, onSelect: () => open(e, { asText: true }) },
        ...(IMAGE.test(e.name) || /\.(pdf|txt|log)$/i.test(e.name) ? [{ label: 'Open in a new tab', icon: ArrowTopRightOnSquareIcon, onSelect: () => download(e.path, true) }] : []),
      ] : []),
      { label: e.dir && !many ? 'Download as .tar.gz' : 'Download', icon: ArrowDownTrayIcon, onSelect: () => ps.forEach(p => download(p)) },
      'separator',
      ...(canEdit ? [
        ...(!many ? [{ label: 'Rename', icon: PencilIcon, onSelect: () => setRenaming(e.path) }] : []),
        { label: 'Duplicate', icon: DocumentDuplicateIcon, onSelect: () => op({ op: 'duplicate', paths: ps }, out => `Duplicated as ${out.paths?.map(baseOf).join(', ')}.`) },
      ] : []),
      { label: 'Copy', icon: ClipboardIcon, onSelect: () => { setClipboard({ mode: 'copy', paths: ps, volume }); say('info', `Copied ${label(ps)}. Open a folder and paste.`); } },
      ...(canEdit ? [{ label: 'Cut', icon: ScissorsIcon, onSelect: () => { setClipboard({ mode: 'cut', paths: ps, volume }); say('info', `Cut ${label(ps)}. Open a folder and paste.`); } }] : []),
      ...(canEdit && e.dir && clip && !many ? [{ label: `Paste into ${e.name}`, icon: ClipboardDocumentIcon, onSelect: () => paste(e.path) }] : []),
      ...(canEdit ? [
        { label: 'Move to…', icon: ArrowsRightLeftIcon, onSelect: () => openTab({ kind: 'pick', path: ps.join('\n'), title: `Move ${label(ps)}`, paths: ps, mode: 'move' }) },
        { label: 'Copy to…', icon: FolderArrowDownIcon, onSelect: () => openTab({ kind: 'pick', path: ps.join('\n'), title: `Copy ${label(ps)}`, paths: ps, mode: 'copy' }) },
        'separator',
        { label: 'Compress', icon: ArchiveBoxArrowDownIcon, onSelect: async () => {
          const out = await op({ op: 'compress', paths: ps, name: `${many ? `archive-${new Date().toISOString().slice(0, 10)}` : e.name.replace(/\.[^.]+$/, '') || e.name}.tar.gz` }, out => `Created ${baseOf(out.path)}.`);
          if (out?.path) { setSelected(new Set([out.path])); setRenaming(out.path); }
        } },
        ...(!many && !e.dir && ARCHIVE.test(e.name) ? [{ label: 'Extract here', icon: FolderOpenIcon, onSelect: () => op({ op: 'extract', path: e.path }, out => `Extracted to ${baseOf(out.path)}.`) }] : []),
        { label: 'Permissions', icon: ShieldCheckIcon, onSelect: () => openTab({ kind: 'info', path: e.path, title: `${e.name} · permissions`, section: 'permissions', paths: ps }) },
      ] : []),
      ...(canShare && !many ? [{ label: 'Share link', icon: LinkIcon, onSelect: () => openTab({ kind: 'info', path: e.path, title: `${e.name} · share`, section: 'share', paths: ps }) }] : []),
      { label: 'Copy path', icon: DocumentDuplicateIcon, onSelect: () => copyText(ps.map(p => `/${p}`).join('\n')) },
      ...(!many ? [{ label: 'Properties', icon: InformationCircleIcon, onSelect: () => openTab({ kind: 'info', path: e.path, title: `${e.name} · properties`, section: 'properties', paths: ps }) }] : []),
      ...(canEdit ? ['separator',
        { label: 'Move to trash', icon: TrashIcon, danger: true, onSelect: () => remove(ps, false) },
        { label: 'Delete permanently', icon: TrashIcon, danger: true, onSelect: () => remove(ps, true) },
      ] : []),
    ];
  }
  const folderMenu = () => [
    ...(canEdit ? [{ label: 'New file', icon: DocumentPlusIcon, onSelect: () => setCreating('file') }, { label: 'New folder', icon: FolderPlusIcon, onSelect: () => setCreating('folder') },
      { label: 'Upload files', icon: ArrowUpTrayIcon, onSelect: pickUpload }] : []),
    ...(canEdit && clip ? [{ label: `Paste ${label(clip.paths)}`, icon: ClipboardDocumentIcon, onSelect: () => paste() }] : []),
    'separator',
    { label: 'Download this folder', icon: ArrowDownTrayIcon, onSelect: () => download(dir) },
    { label: 'Copy path', icon: DocumentDuplicateIcon, onSelect: () => copyText(`/${dir}`) },
    { label: 'Refresh', icon: ArrowPathIcon, onSelect: refresh },
  ];
  const showMenu = (ev, items) => { ev.preventDefault(); ev.stopPropagation(); setMenu({ anchor: ev.type === 'contextmenu' ? { x: ev.clientX, y: ev.clientY } : ev.currentTarget.getBoundingClientRect(), items }); };

  function onKeyDown(ev) {
    if (ev.target.closest('input, textarea, .fx-monaco')) return;
    const one = sel.length === 1 ? byPath[sel[0]] : null;
    if (ev.key === 'Enter' && one) { ev.preventDefault(); open(one); }
    else if (ev.key === 'F2' && one && canEdit) { ev.preventDefault(); setRenaming(one.path); }
    else if (ev.key === 'Delete' && sel.length && canEdit) { ev.preventDefault(); remove(sel, ev.shiftKey); }
    else if (ev.key === 'Backspace' && dir) { ev.preventDefault(); setDir(parentOf(dir)); }
    else if ((ev.ctrlKey || ev.metaKey) && ev.key === 'a') { ev.preventDefault(); setSelected(new Set(entries.map(e => e.path))); }
    else if ((ev.ctrlKey || ev.metaKey) && ev.key === 'c' && sel.length) { setClipboard({ mode: 'copy', paths: sel, volume }); say('info', `Copied ${label(sel)}.`); }
    else if ((ev.ctrlKey || ev.metaKey) && ev.key === 'x' && sel.length && canEdit) { setClipboard({ mode: 'cut', paths: sel, volume }); say('info', `Cut ${label(sel)}.`); }
    else if ((ev.ctrlKey || ev.metaKey) && ev.key === 'v' && clip && canEdit) { ev.preventDefault(); paste(); }
  }

  const crumbs = dir ? dir.split('/') : [];
  const groups = [0, 1].filter(g => tabs.some(t => t.group === g));
  const editorOpen = groups.length > 0;

  return <div className={cx('rp-page fx-page', editorOpen && 'with-editor')} onKeyDown={onKeyDown}>
    <div className="fx-toolbar">
      {volumes.length > 1 && <Select label="Volume" value={volume} onChange={v => { if (!dirty || window.confirm('Discard unsaved changes?')) onVolume(v); }}
        options={volumes.map(v => ({ value: v.name, label: `${v.name} · ${v.mountPath}` }))} />}
      <nav className="fx-crumbs" aria-label="Folder">
        <button type="button" onClick={() => { setTrashView(false); setDir(''); }} title={volumes.find(v => v.name === volume)?.mountPath}>{volume}</button>
        {crumbs.map((c, i) => <span key={i}><ChevronRightIcon /><button type="button" onClick={() => setDir(crumbs.slice(0, i + 1).join('/'))}>{c}</button></span>)}
        {trashView && <span><ChevronRightIcon /><button type="button">Trash</button></span>}
      </nav>
      <span className="fx-spacer" />
      {!trashView && <SearchField value={filter} onChange={setFilter} placeholder="Filter" />}
      {canEdit && !trashView && <>
        {clip && clip.volume === volume && <Button icon={ClipboardDocumentIcon} onClick={() => paste()} title={`Paste ${label(clip.paths)} here`}>Paste {clip.paths.length > 1 ? clip.paths.length : ''}</Button>}
        <Button icon={DocumentPlusIcon} onClick={() => setCreating('file')} title="New file">New file</Button>
        <button type="button" className="icon-action" title="New folder" aria-label="New folder" onClick={() => setCreating('folder')}><FolderPlusIcon /></button>
        <button type="button" className="icon-action" title="Upload" aria-label="Upload files" onClick={pickUpload}><ArrowUpTrayIcon /></button>
      </>}
      <button type="button" className={cx('icon-action', trashView && 'on')} title="Trash" aria-label="Trash" aria-pressed={trashView} onClick={() => setTrashView(t => !t)}><TrashIcon /></button>
    </div>
    {(notice || busy) && <div className="fx-status">{busy && !notice ? <span className="fx-muted">{busy.startsWith('Uploading') ? busy : 'Working…'}</span>
      : <Notice tone={notice.tone}>{notice.text} <button type="button" className="inline-link" onClick={() => setNotice(null)}>Dismiss</button></Notice>}</div>}

    <div className="fx-split">
      {trashView ? <Trash rid={rid} volume={volume} canEdit={canEdit} onChanged={refresh} say={say} /> : <section className={cx('fx-list', drag && 'drag')} aria-label="Files"
        onContextMenu={ev => !ev.target.closest('.fx-row') && showMenu(ev, folderMenu())}
        onDragOver={ev => { if (canEdit && ev.dataTransfer.types.includes('Files')) { ev.preventDefault(); setDrag(true); } }}
        onDragLeave={ev => { if (!ev.currentTarget.contains(ev.relatedTarget)) setDrag(false); }}
        onDrop={ev => { ev.preventDefault(); setDrag(false); upload(ev.dataTransfer.files); }}>
        <div className="fx-head"><span />{canEdit && <Checkbox checked={entries.length > 0 && selected.size === entries.length} indeterminate={selected.size > 0 && selected.size < entries.length} label="Select all"
          onChange={on => setSelected(on ? new Set(entries.map(e => e.path)) : new Set())} />}<span>Name</span>{!editorOpen && <><span className="num">Size</span><span>Permissions</span><span>Changed</span></>}<span /></div>
        <div className="fx-rows" tabIndex={-1}>
          {dir && <button type="button" className="fx-row up" onDoubleClick={() => setDir(parentOf(dir))} onClick={() => setDir(parentOf(dir))}><span /><span /><span className="fx-name"><FolderIcon />..</span></button>}
          {creating && <NewRow kind={creating} onCancel={() => setCreating(null)} onCreate={async name => {
            const out = await op({ op: creating === 'file' ? 'touch' : 'mkdir', path: join(dir, name) }, `Created ${name}.`);
            if (out) { setCreating(null); if (creating === 'file') openTab({ kind: 'file', path: join(dir, name), title: name }); }
          }} />}
          {entries.map(e => {
            const Icon = iconFor(e);
            const cut = clip?.mode === 'cut' && clip.volume === volume && clip.paths.includes(e.path);
            return <div key={e.path} role="row" tabIndex={0} aria-selected={selected.has(e.path)} className={cx('fx-row', selected.has(e.path) && 'selected', cut && 'cut', tabs.some(t => t.path === e.path && active[t.group] === t.key) && 'open')}
              onClick={ev => clickRow(e, ev)} onDoubleClick={() => open(e)} onContextMenu={ev => { if (!selected.has(e.path)) setSelected(new Set([e.path])); showMenu(ev, itemMenu(e)); }}
              draggable={canEdit} onDragStart={ev => { ev.dataTransfer.setData('text/x-ignition-paths', JSON.stringify(paths(e))); ev.dataTransfer.effectAllowed = 'move'; }}
              onDragOver={ev => { if (e.dir && ev.dataTransfer.types.includes('text/x-ignition-paths')) { ev.preventDefault(); ev.currentTarget.classList.add('drop'); } }}
              onDragLeave={ev => ev.currentTarget.classList.remove('drop')}
              onDrop={ev => { ev.currentTarget.classList.remove('drop'); const raw = ev.dataTransfer.getData('text/x-ignition-paths'); if (e.dir && raw) { ev.preventDefault(); ev.stopPropagation(); const ps = JSON.parse(raw).filter(p => p !== e.path); if (ps.length) op({ op: 'move', paths: ps, to: e.path }, `Moved ${label(ps)} into ${e.name}.`); } }}>
              <span className="fx-open-dot" />
              {canEdit ? <span onClick={ev => ev.stopPropagation()}><Checkbox checked={selected.has(e.path)} label={`Select ${e.name}`} onChange={on => setSelected(s => { const n = new Set(s); on ? n.add(e.path) : n.delete(e.path); return n; })} /></span> : <span />}
              {renaming === e.path ? <RenameInput entry={e} onCancel={() => setRenaming(null)} onRename={async name => {
                const to = join(dir, name);
                const out = await op({ op: 'rename', path: e.path, to }, `Renamed to ${name}.`);
                if (out) { setRenaming(null); setTabs(ts => ts.map(t => t.path === e.path ? { ...t, path: to, title: name, key: keyOf(t.kind, to) } : t)); }
              }} /> : <span className={cx('fx-name', e.dir && 'dir')}><Icon />{e.name}{e.link && <em>link</em>}</span>}
              {!editorOpen && <><span className="num fx-muted">{e.dir ? '' : bytes(e.size)}</span><span className="mono fx-muted" title={e.mode}>{e.mode}</span><span className="fx-muted" title={new Date(e.modified).toLocaleString()}>{timeAgo(e.modified)}</span></>}
              <button type="button" className="icon-action" aria-label={`Actions for ${e.name}`} onClick={ev => { if (!selected.has(e.path)) setSelected(new Set([e.path])); showMenu(ev, itemMenu(e)); }}><EllipsisHorizontalIcon /></button>
            </div>;
          })}
          {list.data && !entries.length && !creating && <div className="fx-empty">{filter ? 'Nothing matches.' : canEdit ? 'Empty folder. Drop files here to upload.' : 'Empty folder.'}</div>}
          {!list.data && !list.error && <div className="fx-empty">Loading…</div>}
          {list.error && <div className="fx-empty"><Notice tone="danger">{list.error.message}</Notice></div>}
        </div>
        <footer className="fx-foot">{entries.length} item{entries.length === 1 ? '' : 's'}{selected.size > 0 && ` · ${selected.size} selected`}{drag && ' · Drop to upload here'}</footer>
      </section>}

      {editorOpen && <div className={cx('fx-editors', groups.length > 1 && 'two')}>
        {groups.map(g => {
          const gtabs = tabs.filter(t => t.group === g);
          const cur = gtabs.find(t => t.key === active[g]) ?? gtabs.at(-1);
          return <section key={g} className={cx('fx-group', focusGroup === g && 'focused')} onMouseDown={() => setFocusGroup(g)}>
            <div className="fx-tabs" role="tablist">
              {gtabs.map(t => <div key={t.key} role="tab" aria-selected={t === cur} className={cx('fx-tab', t === cur && 'on')} title={t.path}
                onClick={() => setActive(a => { const n = [...a]; n[g] = t.key; return n; })} onAuxClick={ev => ev.button === 1 && closeTab(t)}>
                <span>{t.title}</span>
                <button type="button" aria-label={`Close ${t.title}`} className={cx(t.dirty && 'dirty')} onClick={ev => { ev.stopPropagation(); closeTab(t); }}><XMarkIcon /></button>
              </div>)}
              <span className="fx-spacer" />
              {cur?.kind === 'file' && cur.dirty && canEdit && <Button className="fx-save" variant="primary" onClick={() => save(cur)}>Save</Button>}
            </div>
            <div className="fx-pane">{cur && <Pane key={cur.key} tab={cur} rid={rid} volume={volume} canEdit={canEdit}
              onChange={v => setTabs(ts => ts.map(x => x.key === cur.key ? { ...x, content: v, dirty: v !== x.saved } : x))} onSave={() => save(tabs.find(x => x.key === cur.key))}
              onReload={() => loadFile(cur.key, cur.path, cur.asText)} onFocus={() => setFocusGroup(g)} op={op} say={say} onDone={() => closeTab(cur)} />}</div>
          </section>;
        })}
      </div>}
    </div>

    {menu && <MenuPopover anchor={menu.anchor} items={menu.items} label="File actions" onClose={() => setMenu(null)} />}
  </div>;
}

function NewRow({ kind, onCancel, onCreate }) {
  const [name, setName] = useState(kind === 'file' ? 'untitled.txt' : 'new folder');
  const ref = useRef(null);
  useEffect(() => { const el = ref.current?.querySelector('input'); el?.focus(); el?.setSelectionRange(0, kind === 'file' ? name.lastIndexOf('.') : name.length); }, []); // eslint-disable-line react-hooks/exhaustive-deps
  return <form ref={ref} className="fx-row editing" onSubmit={ev => { ev.preventDefault(); if (name.trim()) onCreate(name.trim()); }}>
    <span /><span />
    <span className="fx-name">{kind === 'file' ? <DocumentPlusIcon /> : <FolderPlusIcon />}<TextInput value={name} aria-label={`New ${kind} name`} onChange={e => setName(e.target.value)} onKeyDown={e => e.key === 'Escape' && onCancel()} onBlur={onCancel} /></span>
  </form>;
}

function RenameInput({ entry, onCancel, onRename }) {
  const [name, setName] = useState(entry.name);
  const ref = useRef(null);
  useEffect(() => { const el = ref.current?.querySelector('input'); el?.focus(); const dot = entry.dir ? -1 : entry.name.lastIndexOf('.'); el?.setSelectionRange(0, dot > 0 ? dot : entry.name.length); }, []); // eslint-disable-line react-hooks/exhaustive-deps
  return <form ref={ref} className="fx-name" onClick={e => e.stopPropagation()} onSubmit={ev => { ev.preventDefault(); const n = name.trim(); if (n && n !== entry.name) onRename(n); else onCancel(); }}>
    <TextInput value={name} aria-label={`Rename ${entry.name}`} onChange={e => setName(e.target.value)} onKeyDown={e => e.key === 'Escape' && onCancel()} onBlur={onCancel} />
  </form>;
}

/* ----------------------------------- Panes ----------------------------------- */

function Pane({ tab, rid, volume, canEdit, onChange, onSave, onReload, onFocus, op, say, onDone }) {
  if (tab.kind === 'image') return <div className="fx-preview"><img src={api.fileDownloadUrl(rid, volume, tab.path, true)} alt={tab.title} /></div>;
  if (tab.kind === 'info') return <InfoPane tab={tab} rid={rid} volume={volume} canEdit={canEdit} op={op} say={say} />;
  if (tab.kind === 'pick') return <PickFolder rid={rid} volume={volume} tab={tab} op={op} onDone={onDone} />;
  if (tab.loading) return <div className="fx-pane-msg">Opening…</div>;
  if (tab.error) return <div className="fx-pane-msg"><Notice tone="danger">{tab.error} <button type="button" className="inline-link" onClick={onReload}>Retry</button></Notice></div>;
  if (tab.tooLarge) return <div className="fx-pane-msg"><Notice tone="info" title="Too large to edit here">{bytes(tab.size)}. Files up to 5 MB open in the editor; download it instead.</Notice></div>;
  if (tab.binary) return <div className="fx-pane-msg"><Notice tone="info" title="Not a text file">{bytes(tab.size)} of binary data. Use <strong>Open as text</strong> from the menu to look anyway, or download it.</Notice></div>;
  return <Suspense fallback={<div className="fx-pane-msg">Loading the editor…</div>}>
    <CodeEditor modelKey={`${rid}:${tab.key}`} path={tab.path} value={tab.saved ?? ''} readOnly={!canEdit} onChange={onChange} onSave={onSave} onFocus={onFocus} />
  </Suspense>;
}

const PERMS = [['Owner', 6], ['Group', 3], ['Everyone', 0]];
function InfoPane({ tab, rid, volume, canEdit, op, say }) {
  const stat = useQuery(() => api.fileStat(rid, volume, tab.path), [tab.path]);
  const [mode, setMode] = useState(null);
  const [recursive, setRecursive] = useState(false);
  const [hours, setHours] = useState('24');
  const [link, setLink] = useState(null);
  const info = stat.data;
  useEffect(() => { if (info) setMode(info.mode.padStart(3, '0').slice(-3)); }, [info]);
  const bits = mode ? parseInt(mode, 8) : 0;
  const toggle = bit => setMode((bits ^ (1 << bit)).toString(8).padStart(3, '0'));
  async function share() {
    try { setLink(await api.shareFile(rid, volume, tab.path, Number(hours))); } catch (e) { say('danger', e.message); }
  }
  const ref = useRef(null);
  useEffect(() => { ref.current?.querySelector(`[data-section="${tab.section}"]`)?.scrollIntoView({ block: 'start' }); }, [info, tab.section]);
  if (stat.error) return <div className="fx-pane-msg"><Notice tone="danger">{stat.error.message}</Notice></div>;
  if (!info) return <div className="fx-pane-msg">Measuring…</div>;
  return <div className="fx-info" ref={ref}>
    <section data-section="properties">
      <h3>{info.name || volume}</h3>
      <dl>
        <div><dt>Kind</dt><dd>{info.dir ? 'Folder' : 'File'}{info.link ? ` · link to ${info.link}` : ''}</dd></div>
        <div><dt>Where</dt><dd className="mono">/{info.path}</dd></div>
        <div><dt>Size</dt><dd>{bytes(info.size)}{info.dir ? ` · ${info.files.toLocaleString()} files, ${info.folders.toLocaleString()} folders` : ''}</dd></div>
        <div><dt>Changed</dt><dd>{new Date(info.modified).toLocaleString()}</dd></div>
        <div><dt>Opened</dt><dd>{new Date(info.accessed).toLocaleString()}</dd></div>
        <div><dt>Owner</dt><dd className="mono">{info.owner}</dd></div>
        {info.sha256 && <div><dt>SHA-256</dt><dd className="mono fx-hash">{info.sha256}</dd></div>}
      </dl>
    </section>
    <section data-section="permissions">
      <h3>Permissions <span className="mono fx-muted">{info.perms}</span></h3>
      <table className="fx-perms"><thead><tr><th /><th>Read</th><th>Write</th><th>{info.dir ? 'Open' : 'Run'}</th></tr></thead>
        <tbody>{PERMS.map(([who, shift]) => <tr key={who}><td>{who}</td>{[2, 1, 0].map(b => <td key={b}><Checkbox checked={!!(bits & (1 << (shift + b)))} label={`${who} ${['run', 'write', 'read'][b]}`} onChange={() => canEdit && toggle(shift + b)} /></td>)}</tr>)}</tbody></table>
      {canEdit && <div className="fx-row-form">
        <TextInput className="mono fx-mode" value={mode ?? ''} aria-label="Octal mode" maxLength={4} onChange={e => setMode(e.target.value.replace(/[^0-7]/g, ''))} />
        {info.dir && <label className="fx-toggle"><Switch label="Apply inside" checked={recursive} onChange={setRecursive} />Everything inside too</label>}
        <Button variant="primary" disabled={!mode || mode === info.mode.slice(-3)} onClick={async () => { if (await op({ op: 'chmod', paths: tab.paths, mode, recursive }, `Permissions set to ${mode}.`)) stat.refetch(); }}>Apply</Button>
      </div>}
    </section>
    <section data-section="share">
      <h3>Share link</h3>
      <p className="fx-muted">Anyone with the link can download {info.dir ? 'this folder (as .tar.gz)' : 'this file'} without signing in, until it expires.</p>
      <div className="fx-row-form">
        <Select label="Expires" value={hours} onChange={setHours} options={[['1', 'In 1 hour'], ['24', 'In 1 day'], ['72', 'In 3 days'], ['168', 'In 7 days']].map(([value, label]) => ({ value, label }))} />
        <Button icon={LinkIcon} onClick={share}>Create link</Button>
      </div>
      {link && <div className="fx-link"><code className="mono">{link.url || location.origin + link.path}</code>
        <Button onClick={() => navigator.clipboard?.writeText(link.url || location.origin + link.path).then(() => say('success', 'Link copied.'))}>Copy</Button>
        <span className="fx-muted">Expires {new Date(link.expiresAt).toLocaleString()}</span></div>}
    </section>
  </div>;
}

function PickFolder({ rid, volume, tab, op, onDone }) {
  const [dir, setDir] = useState('');
  const list = useQuery(() => api.files(rid, volume, dir), [dir]);
  const folders = (list.data?.entries ?? []).filter(e => e.dir && !tab.paths.includes(e.path));
  const crumbs = dir ? dir.split('/') : [];
  return <div className="fx-info">
    <section>
      <h3>{tab.mode === 'move' ? 'Move' : 'Copy'} {tab.paths.length === 1 ? baseOf(tab.paths[0]) : `${tab.paths.length} items`} to…</h3>
      <nav className="fx-crumbs"><button type="button" onClick={() => setDir('')}>{volume}</button>
        {crumbs.map((c, i) => <span key={i}><ChevronRightIcon /><button type="button" onClick={() => setDir(crumbs.slice(0, i + 1).join('/'))}>{c}</button></span>)}</nav>
      <div className="fx-picker">
        {dir && <button type="button" onClick={() => setDir(parentOf(dir))}><ArrowUturnLeftIcon />Up</button>}
        {folders.map(f => <button key={f.path} type="button" onClick={() => setDir(f.path)}><FolderIcon />{f.name}<ChevronRightIcon className="fx-chev" /></button>)}
        {list.data && !folders.length && <p className="fx-muted">No folders here.</p>}
      </div>
      <div className="fx-row-form">
        <Button variant="primary" onClick={async () => { if (await op({ op: tab.mode, paths: tab.paths, to: dir }, `${tab.mode === 'move' ? 'Moved' : 'Copied'} into /${dir}.`)) onDone(); }}>
          {tab.mode === 'move' ? 'Move' : 'Copy'} here{dir ? ` (/${dir})` : ''}</Button>
        <Button onClick={onDone}>Cancel</Button>
      </div>
    </section>
  </div>;
}

function Trash({ rid, volume, canEdit, onChanged, say }) {
  const q = useQuery(() => api.fileTrash(rid, volume), [rid, volume]);
  const items = q.data?.items ?? [];
  const act = useCallback(async (body, text) => {
    try { await api.fileOp(rid, volume, body); say('success', text); q.refetch(); onChanged(); } catch (e) { say('danger', e.message); }
  }, [rid, volume]); // eslint-disable-line react-hooks/exhaustive-deps
  return <section className="fx-list fx-trash" aria-label="Trash">
    <div className="fx-trash-head"><span className="fx-muted">Items in the trash still use space until it's emptied.</span>
      {canEdit && items.length > 0 && <Button icon={TrashIcon} onClick={() => window.confirm('Delete everything in the trash permanently?') && act({ op: 'empty-trash' }, 'Emptied the trash.')}>Empty trash</Button>}</div>
    <div className="fx-rows">
      {items.map(t => <div key={t.id} className="fx-row trash">
        <span /><span />
        <span className="fx-name">{t.dir ? <FolderIcon /> : <DocumentIcon />}{t.name}<em>from /{t.from}</em></span>
        <span className="fx-muted">{bytes(t.size)} · {timeAgo(t.trashed)}</span>
        {canEdit && <span className="fx-actions">
          <Button icon={ArrowUturnLeftIcon} onClick={() => act({ op: 'restore', ids: [t.id] }, `Restored ${t.name}.`)}>Restore</Button>
          <button type="button" className="icon-action danger" aria-label={`Delete ${t.name} permanently`} title="Delete permanently" onClick={() => window.confirm(`Delete ${t.name} permanently?`) && act({ op: 'empty-trash', ids: [t.id] }, `Deleted ${t.name}.`)}><TrashIcon /></button>
        </span>}
      </div>)}
      {q.data && !items.length && <div className="fx-empty">The trash is empty.</div>}
      {!q.data && <div className="fx-empty">Loading…</div>}
    </div>
  </section>;
}

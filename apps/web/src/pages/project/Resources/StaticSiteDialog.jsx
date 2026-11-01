import { useMemo, useRef, useState } from 'react';
import { ArchiveBoxIcon, CheckCircleIcon, DocumentIcon, ExclamationTriangleIcon, FolderOpenIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, Meter, Notice, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import './AddResourceDialog.css';
import './StaticSiteDialog.css';

/*
 * Application › Upload your static files. A small web server (nginx) serves the files from the
 * resource's own volume, so there's nothing to build: drop a folder, some files or a .zip and the
 * site is live. Changing files later (under Files) goes live at once, no redeploy.
 */

const IMAGE = 'nginx:1.27-alpine';
const ROOT = '/usr/share/nginx/html';
const ARCHIVE = /\.(zip|tar|tar\.gz|tgz)$/i;
const slug = s => s.toLowerCase().replace(/\.(zip|tar|tar\.gz|tgz)$/i, '').replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 50).replace(/-+$/, '');
const bytes = n => n < 1 << 20 ? `${Math.max(1, Math.round(n / 1024))} KB` : n < 1 << 30 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${(n / (1 << 30)).toFixed(2)} GB`;

// Every file in a dropped folder, with its path inside it.
async function walk(entry, prefix = '') {
  if (entry.isFile) return [await new Promise((res, rej) => entry.file(f => res({ file: f, path: prefix + f.name }), rej))];
  const reader = entry.createReader();
  const all = [];
  for (;;) {
    const batch = await new Promise((res, rej) => reader.readEntries(res, rej)); // eslint-disable-line no-await-in-loop
    if (!batch.length) break;
    all.push(...batch);
  }
  return (await Promise.all(all.map(e => walk(e, `${prefix}${entry.name}/`)))).flat();
}

// A folder dropped or picked arrives as folder/…; the site is what's inside it.
function stripCommonFolder(items) {
  const first = items[0]?.path.split('/')[0];
  if (items.length && items.every(i => i.path.includes('/') && i.path.split('/')[0] === first)) return { items: items.map(i => ({ ...i, path: i.path.slice(first.length + 1) })), folder: first };
  return { items, folder: '' };
}

export function StaticSiteDialog({ project, environment, environmentId, takenNames, onClose, onCreated }) {
  const [items, setItems] = useState([]); // { file, path }
  const [source, setSource] = useState('');
  const [name, setName] = useState('');
  const [touched, setTouched] = useState(false);
  const [drag, setDrag] = useState(false);
  const [error, setError] = useState('');
  const [progress, setProgress] = useState(null); // { done, total, bytes, totalBytes, step }
  const [done, setDone] = useState(null);
  const filesRef = useRef(null);
  const folderRef = useRef(null);

  const archive = items.length === 1 && ARCHIVE.test(items[0].path) ? items[0] : null;
  const totalBytes = items.reduce((n, i) => n + i.file.size, 0);
  const hasIndex = archive ? null : items.some(i => i.path.toLowerCase() === 'index.html');
  const finalName = touched ? name : slug(source) || '';
  const nameError = !finalName ? '' : !/^[a-z0-9]([a-z0-9-]{0,48}[a-z0-9])?$/.test(finalName) ? 'Use lowercase letters, numbers and dashes.' : takenNames.includes(finalName) ? `${finalName} already exists in ${environment}.` : '';
  const folders = useMemo(() => new Set(items.map(i => i.path.split('/').slice(0, -1).join('/')).filter(Boolean)).size, [items]);

  function take(list, label) {
    const { items: clean, folder } = stripCommonFolder(list.filter(i => !/(^|\/)(\.DS_Store|Thumbs\.db)$/.test(i.path)));
    if (!clean.length) return setError('Nothing to upload there.');
    setItems(clean); setError('');
    setSource(folder || label || (clean.length === 1 ? clean[0].path : 'site'));
  }
  async function onDrop(ev) {
    ev.preventDefault(); setDrag(false);
    const entries = [...ev.dataTransfer.items].map(i => i.webkitGetAsEntry?.()).filter(Boolean);
    if (entries.length) {
      const all = (await Promise.all(entries.map(e => walk(e)))).flat();
      take(all, entries.length === 1 ? entries[0].name : '');
    } else take([...ev.dataTransfer.files].map(f => ({ file: f, path: f.name })));
  }

  async function create() {
    if (!items.length) return setError('Add your site\'s files first.');
    if (!finalName || nameError) return setError(nameError || 'Name the site.');
    setError('');
    let resource;
    try {
      setProgress({ done: 0, total: items.length, sent: 0, step: 'Creating the site…' });
      ({ resource } = await api.createResource(project.id, { environmentId, name: finalName, type: 'application', sourceType: 'image', image: IMAGE, port: 80, dataPath: ROOT }));
      let sent = 0;
      for (const [i, it] of items.entries()) {
        setProgress({ done: i, total: items.length, sent, step: archive ? `Uploading ${it.file.name}…` : `Uploading ${it.path}` });
        const dir = it.path.split('/').slice(0, -1).join('/');
        await api.uploadFile(resource.id, 'data', dir, new File([it.file], it.path.split('/').pop())); // eslint-disable-line no-await-in-loop
        sent += it.file.size;
      }
      if (archive) {
        setProgress({ done: 1, total: 1, sent, step: 'Unpacking…' });
        await api.fileOp(resource.id, 'data', { op: 'unpack', path: archive.path.split('/').pop() });
      }
      setProgress(null);
      setDone(resource);
    } catch (e) {
      setProgress(null);
      setError(resource ? `${resource.name} was created, but the upload stopped: ${e.message} Finish it under Files.` : e.message);
      if (resource) setDone(resource);
    }
  }

  const url = done && (done.domain || (done.hostPort ? `http://${location.hostname}:${done.hostPort}` : null));
  return <Dialog open onClose={() => (done ? onCreated(done) : !progress && onClose())} title="Upload your static files" className="add-resource static-site"
    description="HTML, CSS, JavaScript, images: a built site (dist, build, public) or a hand-made one. It's served as it is, no build step.">
    {done ? <div className="add-panel ss-done">
      <div className="add-card">
        <span className="ss-done-head"><CheckCircleIcon />{done.name} is live</span>
        <p>{items.length.toLocaleString()} files are being served. It starts in a few seconds; change files any time under Files and they go live straight away.</p>
        {error && <Notice tone="warning">{error}</Notice>}
        <div className="ss-actions">
          {url && <Button onClick={() => window.open(url, '_blank', 'noopener')}>Open site</Button>}
          <Button variant="primary" onClick={() => onCreated(done)}>Go to {done.name}</Button>
        </div>
      </div>
    </div> : <div className="add-panel">
      <div className="add-card">
        <div className={cx('ss-drop', drag && 'over', items.length && 'filled')} onDragOver={e => { e.preventDefault(); setDrag(true); }} onDragLeave={() => setDrag(false)} onDrop={onDrop}>
          {items.length ? <>
            <span className="ss-drop-icon">{archive ? <ArchiveBoxIcon /> : <FolderOpenIcon />}</span>
            <strong>{archive ? archive.path : source}</strong>
            <span>{archive ? `${bytes(totalBytes)} archive, unpacked on the server` : `${items.length.toLocaleString()} file${items.length === 1 ? '' : 's'}${folders ? ` in ${folders} folder${folders === 1 ? '' : 's'}` : ''} · ${bytes(totalBytes)}`}</span>
            <button type="button" className="inline-link" disabled={!!progress} onClick={() => { setItems([]); setSource(''); }}>Choose something else</button>
          </> : <>
            <span className="ss-drop-icon"><FolderOpenIcon /></span>
            <strong>Drop your site's folder here</strong>
            <span>or a .zip of it, or the files themselves</span>
            <div className="ss-pick">
              <Button onClick={() => folderRef.current.click()}>Choose a folder</Button>
              <Button onClick={() => filesRef.current.click()}>Choose files or a .zip</Button>
            </div>
          </>}
          <input ref={folderRef} type="file" webkitdirectory="" directory="" multiple hidden onChange={e => take([...e.target.files].map(f => ({ file: f, path: f.webkitRelativePath || f.name })))} />
          <input ref={filesRef} type="file" multiple hidden onChange={e => take([...e.target.files].map(f => ({ file: f, path: f.name })), e.target.files.length === 1 ? e.target.files[0].name : '')} />
        </div>
        {items.length > 0 && !archive && !hasIndex && <Notice tone="warning"><ExclamationTriangleIcon className="ss-inline" />There's no index.html at the top, so the site's address shows nothing until one is added. Did you mean to pick the built folder (dist or build)?</Notice>}
        {items.length > 0 && <ul className="ss-preview">
          {items.slice(0, 6).map(i => <li key={i.path}><DocumentIcon />{i.path}<span>{bytes(i.file.size)}</span></li>)}
          {items.length > 6 && <li className="more">and {(items.length - 6).toLocaleString()} more</li>}
        </ul>}
        <FormField label="Name" htmlFor="ss-name" error={nameError} hint="Lowercase letters, numbers and dashes. Add a domain for it afterwards.">
          <TextInput id="ss-name" placeholder="my-site" value={finalName} onChange={e => { setName(e.target.value); setTouched(true); }} />
        </FormField>
        {progress && <div className="ss-progress">
          <Meter compact label="Upload" value={progress.total ? (archive ? (progress.step === 'Unpacking…' ? 100 : 50) : progress.done / progress.total * 100) : 0} />
          <span>{progress.step}{!archive && progress.total > 1 ? ` · ${progress.done} of ${progress.total}` : ''}</span>
        </div>}
      </div>
      <div className="add-footer">
        <span className="add-target"><strong>{project.name}</strong><span className="sep">/</span>{environment}</span>
        {error && <span className="add-error" role="alert">{error}</span>}
        <Button onClick={onClose} disabled={!!progress}>Cancel</Button>
        <Button variant="primary" disabled={!!progress || !items.length} onClick={create}>{progress ? 'Uploading…' : 'Upload and publish'}</Button>
      </div>
    </div>}
  </Dialog>;
}

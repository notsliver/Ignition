import { useState } from 'react';
import { ArrowDownTrayIcon, ArrowPathIcon, ArrowUpTrayIcon, MapIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Button, Checkbox, Dialog, FormField, Notice, TextInput } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { useSession, can } from '../../../app/AuthGate';
import { CopyButton, Empty } from '../resource/shared';
import { bytes, download, pickFile } from './shared';
import './game.css';

/*
 * The worlds on the server. Switching, resetting and deleting stop the server (it saves on the way
 * down) and start it again; each offers a backup first, ticked by default.
 */

export function GameWorlds({ resource }) {
  const { user } = useSession();
  const canManage = can(user, 'games.worlds');
  const canDownload = can(user, 'storage.backup');
  const q = useQuery(() => api.gameWorlds(resource.id), [resource.id], { pollMs: 15000 });
  const [dialog, setDialog] = useState(null); // { action, world }
  const [result, setResult] = useState(null);
  const [uploading, setUploading] = useState(null); // { file, name }

  if (q.error && !q.data) return <div className="rp-page gm-page"><Notice tone="danger" title="Couldn't read worlds">{q.error.message}</Notice></div>;
  if (!q.data) return <div className="rp-page gm-page"><p className="gm-muted">Reading worlds…</p></div>;
  const d = q.data;

  return <div className="rp-page gm-page">
    <div className="rp-intro">
      <p>The server loads <strong>{d.active}</strong>{d.seed ? <> · seed <code className="mono">{d.seed}</code> <CopyButton value={d.seed} label="Copy seed" /></> : ''}.
        {d.config.companions?.length > 0 && ' Its Nether and End are kept with it.'}</p>
      {canManage && <Button icon={ArrowUpTrayIcon} onClick={() => pickFile('.zip', file => setUploading({ file, name: file.name.replace(/\.zip$/i, '').replace(/[^A-Za-z0-9 _-]+/g, '').slice(0, 64) || 'uploaded' }))}>Upload a world</Button>}
    </div>
    {result && <Notice tone={result.tone}>{result.text}</Notice>}
    {d.worlds.length ? <div className="rp-card">
      {d.worlds.map(w => <div key={w.name} className="rp-row">
        <MapIcon className="gm-row-icon" />
        <span className="rp-row-main"><strong>{w.name}{w.active && <span className="rp-chip success">Loaded</span>}</strong>
          <span>{bytes(w.size)} · saved {timeAgo(w.modified)}{w.companions.length ? ` · with ${w.companions.join(', ')}` : ''}</span></span>
        {canDownload && <button type="button" className="icon-action" title="Download (.tar.gz)" aria-label={`Download ${w.name}`} onClick={() => download(api.gameWorldDownloadUrl(resource.id, w.name))}><ArrowDownTrayIcon /></button>}
        {canManage && !w.active && <Button onClick={() => setDialog({ action: 'activate', world: w.name })}>Load this world</Button>}
        {canManage && <Button icon={ArrowPathIcon} onClick={() => setDialog({ action: 'reset', world: w.name })}>Reset</Button>}
        {canManage && !w.active && <button type="button" className="icon-action danger" title="Delete" aria-label={`Delete ${w.name}`} onClick={() => setDialog({ action: 'delete', world: w.name })}><TrashIcon /></button>}
      </div>)}
    </div> : <Empty icon={MapIcon} title="No world yet">The server creates {d.active} on its first start. You can also upload one.</Empty>}

    {dialog && <WorldDialog resource={resource} {...dialog} seed={d.seed} running={d.running} onClose={() => setDialog(null)}
      onDone={text => { setDialog(null); setResult({ tone: 'success', text }); q.refetch(); }} />}
    {uploading && <UploadDialog resource={resource} upload={uploading} onClose={() => setUploading(null)}
      onDone={name => { setUploading(null); setResult({ tone: 'success', text: `Uploaded ${name}. Load it to play on it.` }); q.refetch(); }} />}
  </div>;
}

function WorldDialog({ resource, action, world, seed: currentSeed, running, onClose, onDone }) {
  const [backup, setBackup] = useState(true);
  const [seed, setSeed] = useState('');
  const [newSeed, setNewSeed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const copy = {
    reset: { title: `Reset ${world}`, button: 'Reset world', text: `${world} is deleted and a new one is generated on the next start.`, done: `${world} was reset. A fresh world is being generated.` },
    activate: { title: `Load ${world}`, button: 'Load world', text: `The server switches to ${world}. The current world is kept.`, done: `The server now loads ${world}.` },
    delete: { title: `Delete ${world}`, button: 'Delete world', text: `${world} and its dimensions are deleted from the server.`, done: `${world} was deleted.` },
  }[action];
  async function run() {
    setBusy(true); setError('');
    try {
      await api.gameWorldAction(resource.id, action, { world, backup, ...(action === 'reset' && newSeed ? { seed } : {}) });
      onDone(copy.done);
    } catch (e) { setError(e.message); setBusy(false); }
  }
  return <Dialog open onClose={onClose} title={copy.title} description={copy.text} className="gm-dialog"
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant={action === 'activate' ? 'primary' : 'danger-solid'} disabled={busy} onClick={run}>{busy ? 'Working…' : copy.button}</Button></>}>
    {running && <p className="gm-note">The server stops (saving first) and starts again afterwards.</p>}
    {action === 'reset' && <>
      <div className="gm-check" onClick={() => setNewSeed(v => !v)}><Checkbox checked={newSeed} label="Use a different seed" onChange={setNewSeed} /><span><strong>Use a different seed</strong>
        <small>{currentSeed ? `Now ${currentSeed}.` : 'Now random.'} Leave it empty for a random world.</small></span></div>
      {newSeed && <FormField label="Seed" htmlFor="gm-seed"><TextInput id="gm-seed" className="mono" value={seed} maxLength={64} onChange={e => setSeed(e.target.value)} placeholder="Random" /></FormField>}
    </>}
    <div className="gm-check" onClick={() => setBackup(b => !b)}><Checkbox checked={backup} label="Back up first" onChange={setBackup} /><span><strong>Back up first</strong><small>The whole server, restorable under Backups.</small></span></div>
    {error && <p className="gm-error">{error}</p>}
  </Dialog>;
}

function UploadDialog({ resource, upload, onClose, onDone }) {
  const [name, setName] = useState(upload.name);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  async function run() {
    setBusy(true); setError('');
    try { await api.gameWorldUpload(resource.id, name.trim(), upload.file); onDone(name.trim()); } catch (e) { setError(e.fields ? Object.values(e.fields).join(' ') : e.message); setBusy(false); }
  }
  return <Dialog open onClose={onClose} title="Upload a world" description={`${upload.file.name} · ${bytes(upload.file.size)}. A zip of a world folder (the one with level.dat in it).`} className="gm-dialog"
    footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button variant="primary" disabled={busy || !name.trim()} onClick={run}>{busy ? 'Uploading…' : 'Upload'}</Button></>}>
    <FormField label="World name" htmlFor="gm-wn" hint="The folder it's saved as on the server."><TextInput id="gm-wn" value={name} onChange={e => setName(e.target.value)} /></FormField>
    {error && <p className="gm-error">{error}</p>}
  </Dialog>;
}

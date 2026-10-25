import { useEffect, useState } from 'react';
import { ArrowDownTrayIcon, ArrowUturnLeftIcon, CircleStackIcon, ExclamationTriangleIcon, PlusIcon, TrashIcon, XMarkIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, Notice, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { useQuery } from '../../../api/hooks';
import { useSession } from '../../../app/AuthGate';
import { SaveBar } from '../../settings/SettingsField';
import { Empty, PageIntro } from './shared';
import './Storage.css';

/*
 * Named Docker volumes mounted into the container. Adding, removing or moving a volume applies on the next
 * deployment. Removing one only detaches it: its data stays on this machine until it's deleted here.
 * Sizes are what Docker reports on disk; local volumes have no size limit.
 */

const NAME_RE = /^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$/;
const bytes = n => n == null ? null : n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(1)} KB` : n < 1 << 30 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${(n / (1 << 30)).toFixed(2)} GB`;
const backupUrl = (resourceId, docker) => `/api/resources/${resourceId}/storage/${encodeURIComponent(docker)}/backup`;

export function Storage({ resource, onNavigate, onResourceChange }) {
  const { user } = useSession();
  const canEdit = user.role !== 'viewer';
  const isAdmin = user.role === 'admin' || user.role === 'owner';
  const [fast, setFast] = useState(false);
  const { data, error, refetch } = useQuery(() => api.storage(resource.id), [resource.id], { pollMs: fast ? 5000 : 15000 });
  // Poll faster while a volume is still held by a container that's going away.
  useEffect(() => { setFast(!!data?.detached.some(v => v.inUse || v.mounted)); }, [data]);
  const [saved, setSaved] = useState(null);
  const [list, setList] = useState(null);
  const [draft, setDraft] = useState(null);
  const [errors, setErrors] = useState({});
  const [feedback, setFeedback] = useState(null);
  const [busy, setBusy] = useState(false);
  const [deleting, setDeleting] = useState(null);
  const dirty = saved && JSON.stringify(list) !== JSON.stringify(saved);
  useEffect(() => {
    if (data && !dirty) { const l = data.volumes.map(v => ({ name: v.name, mountPath: v.mountPath })); setSaved(l); setList(l); }
  }, [data]); // eslint-disable-line react-hooks/exhaustive-deps

  if (error && !data) return <div className="rp-page"><Notice tone="danger" title="Couldn't load storage">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice></div>;
  if (!data || !list) return <div className="rp-page"><p className="st-muted">Loading…</p></div>;

  const live = Object.fromEntries(data.volumes.map(v => [v.name, v]));
  const removed = saved.filter(s => !list.some(l => l.name === s.name));
  const isDb = resource.type === 'database';

  function addDraft(event) {
    event.preventDefault();
    const name = draft.name.trim().toLowerCase(), mountPath = draft.mountPath.trim();
    if (!NAME_RE.test(name)) return setDraft(d => ({ ...d, error: 'Names use lowercase letters, numbers and dashes.' }));
    if (list.some(v => v.name === name)) return setDraft(d => ({ ...d, error: 'A volume with this name already exists.' }));
    if (!mountPath.startsWith('/')) return setDraft(d => ({ ...d, error: 'Mount paths are absolute, like /app/data.' }));
    setList(l => [...l, { name, mountPath }]); setDraft(null); setFeedback(null);
  }
  async function save() {
    setBusy(true); setErrors({});
    try {
      const out = await api.putStorage(resource.id, { volumes: list });
      setSaved(list);
      onResourceChange?.({ ...resource, volumes: list.map(v => v.mountPath) });
      setFeedback(out.requiresRedeploy ? { tone: 'info', title: 'Saved', text: 'Volume changes apply on the next deployment. Removed volumes keep their data until you delete it.', redeploy: true }
        : { tone: 'success', title: 'Saved', text: 'Applies on the first deployment.' });
      refetch();
    } catch (err) {
      setErrors(err.fields ?? {});
      setFeedback({ tone: 'danger', title: 'Couldn\'t save', text: err.fields ? Object.values(err.fields).join(' ') : err.message });
    }
    setBusy(false);
  }
  async function redeploy() {
    setBusy(true);
    try { await api.deploy(resource.id, {}); setFeedback({ tone: 'success', title: 'Redeploying', text: 'Volumes are mounted when the new release starts.', link: true }); } catch (err) { setFeedback({ tone: 'danger', title: 'Couldn\'t redeploy', text: err.message }); }
    setBusy(false);
  }

  return <div className="rp-page storage-page">
    <PageIntro actions={canEdit && !draft && <Button variant="primary" icon={PlusIcon} onClick={() => setDraft({ name: list.length ? '' : 'data', mountPath: '' })}>Add volume</Button>}>
      Volumes keep files across deployments and restarts. Anything {resource.name} writes outside a volume is lost when its container is replaced.
    </PageIntro>
    {data.dockerError && <Notice tone="danger" title="Can't read volumes from Docker">{data.dockerError}</Notice>}
    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}
      {feedback.redeploy && canEdit && <> <button type="button" className="inline-link" disabled={busy} onClick={redeploy}>Redeploy now</button></>}
      {feedback.link && <> <button type="button" className="inline-link" onClick={() => onNavigate?.('Deployments')}>Open Deployments</button></>}</Notice>}

    {draft && <form className="rp-card volume-form" onSubmit={addDraft}>
      <TextInput placeholder="name" value={draft.name} aria-label="Volume name" autoFocus onChange={e => setDraft(d => ({ ...d, name: e.target.value, error: '' }))} />
      <TextInput className="mono" placeholder="/app/data" value={draft.mountPath} aria-label="Mount path" onChange={e => setDraft(d => ({ ...d, mountPath: e.target.value, error: '' }))} />
      <Button type="button" onClick={() => setDraft(null)}>Cancel</Button>
      <Button type="submit" variant="primary">Add</Button>
      {draft.error && <p className="rp-error">{draft.error}</p>}
    </form>}

    {list.length ? <div className="rp-card">
      {list.map((v, i) => {
        const l = live[v.name];
        const changedPath = l && l.mountPath !== v.mountPath;
        const pending = !saved.some(s => s.name === v.name) || changedPath;
        const err = errors[`volumes.${i}.name`] ?? errors[`volumes.${i}.mountPath`];
        const state = pending ? { cls: 'pending', text: dirty ? 'Unsaved' : 'Mounts on next deploy' }
          : l?.mounted ? { cls: 'ok', text: 'Mounted' } : data.running ? { cls: 'pending', text: 'Mounts on next deploy' } : { cls: '', text: 'Not running' };
        return <div key={v.name} className={cx('rp-row volume-row', err && 'has-error')}>
          <CircleStackIcon className="volume-icon" />
          <span className="rp-row-main"><strong>{v.name}{isDb && v.name === 'data' && <span className="rp-chip">Database data</span>}</strong>
            <span className="mono">{v.mountPath}{err && <em className="st-error"> · {err}</em>}</span></span>
          <span className="st-size">{l?.sizeBytes != null ? bytes(l.sizeBytes) : <span className="st-muted">Created on deploy</span>}</span>
          <span className={cx('rp-chip', state.cls === 'ok' && 'success', state.cls === 'pending' && 'warning')}>{state.text}</span>
          {isAdmin && l?.sizeBytes != null && <a className="icon-action" href={backupUrl(resource.id, l.docker)} download aria-label={`Download a backup of ${v.name}`} title="Download backup (.tar.gz)"><ArrowDownTrayIcon /></a>}
          {canEdit && <button type="button" className="icon-action danger" aria-label={`Remove ${v.name}`} title="Remove (data is kept)" onClick={() => { setList(x => x.filter(y => y.name !== v.name)); setFeedback(null); }}><XMarkIcon /></button>}
        </div>;
      })}
    </div> : !draft && <Empty icon={CircleStackIcon} title="No volumes">{resource.name} is stateless: files it writes are lost on every deploy. Add a volume if it needs to keep uploads, a database or other files.</Empty>}

    {removed.length > 0 && <Notice tone={removed.some(r => isDb && r.name === 'data') ? 'danger' : 'warning'} title={`Removing ${removed.map(r => r.name).join(', ')}`}>
      {removed.some(r => isDb && r.name === 'data') ? 'This is the database\'s data. Without it the database starts empty on the next deploy. ' : ''}The data stays on this machine as a detached volume until you delete it.
    </Notice>}

    {data.detached.length > 0 && <section className="settings-group st-detached">
      <h2>Detached volumes</h2>
      <p className="st-desc">Not mounted any more, but their data is still on this machine.</p>
      <div className="rp-card">
        {data.detached.map(v => <div key={v.docker} className="rp-row volume-row">
          <CircleStackIcon className="volume-icon st-faded" />
          <span className="rp-row-main"><strong>{v.name}</strong><span className="mono">{v.docker}{v.mounted ? ` · still mounted at ${v.mounted} until the next deploy` : v.inUse ? ' · in use by the previous container, which is shutting down' : ''}</span></span>
          <span className="st-size">{bytes(v.sizeBytes)}</span>
          {canEdit && !list.some(l => l.name === v.name) && <Button icon={ArrowUturnLeftIcon} onClick={() => setDraft({ name: v.name, mountPath: '' })}>Reattach</Button>}
          {isAdmin && <a className="icon-action" href={backupUrl(resource.id, v.docker)} download aria-label={`Download a backup of ${v.name}`} title="Download backup (.tar.gz)"><ArrowDownTrayIcon /></a>}
          {isAdmin && <button type="button" className="icon-action danger" aria-label={`Delete ${v.name} and its data`} title={v.inUse ? 'Still in use; wait for the deployment to finish' : 'Delete data'} disabled={v.inUse} onClick={() => setDeleting(v)}><TrashIcon /></button>}
        </div>)}
      </div>
    </section>}

    <p className="st-footnote">Sizes are what the volumes use on this machine's disk; local volumes grow as needed. Backups download the volume's files as .tar.gz{isDb ? '. For a consistent database backup, also use the database\'s own dump tool' : ''}.</p>

    <form onSubmit={e => { e.preventDefault(); save(); }}>
      <SaveBar dirty={dirty} onDiscard={() => { setList(saved); setErrors({}); }} summary={busy ? 'Saving…' : 'Volume changes apply on the next deployment'} />
    </form>
    <DeleteVolume volume={deleting} resourceId={resource.id} onClose={() => setDeleting(null)} onDeleted={() => { setDeleting(null); setFeedback({ tone: 'success', title: 'Volume deleted' }); refetch(); }} />
  </div>;
}

function DeleteVolume({ volume, resourceId, onClose, onDeleted }) {
  const [confirm, setConfirm] = useState('');
  const [failure, setFailure] = useState('');
  const [pending, setPending] = useState(false);
  useEffect(() => { setConfirm(''); setFailure(''); }, [volume]);
  async function run() {
    setPending(true);
    try { await api.deleteVolume(resourceId, volume.docker); onDeleted(); } catch (err) { setFailure(err.message); }
    setPending(false);
  }
  return <Dialog open={!!volume} onClose={onClose} icon={ExclamationTriangleIcon} title={`Delete ${volume?.name} and its data?`}
    description="Every file in this volume is deleted from this machine. Download a backup first if you might need it."
    footer={<><Button onClick={onClose}>Cancel</Button><Button className="danger-solid" disabled={pending || confirm !== volume?.name} onClick={run}>Delete data</Button></>}>
    <label className="st-confirm">Type <code>{volume?.name}</code> to confirm<TextInput value={confirm} aria-label="Volume name" onChange={e => setConfirm(e.target.value)} /></label>
    {failure && <p className="rp-error">{failure}</p>}
  </Dialog>;
}

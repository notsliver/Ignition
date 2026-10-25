import { useEffect, useState } from 'react';
import { ArrowDownTrayIcon, ArrowPathIcon, ArrowsRightLeftIcon, CheckCircleIcon, ExclamationTriangleIcon, PauseIcon, PlayIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Button, Checkbox, Dialog, Notice, Select, Switch, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { useSession } from '../../../app/AuthGate';
import { SaveBar, SettingsField, SettingsGroup } from '../../settings/SettingsField';
import { CopyButton } from './shared';
import './Operations.css';

/*
 * Rare, high-impact controls. Container options apply on the next deploy (the drift table shows what
 * the running container still lacks); destructive actions sit at the bottom and spell out their impact.
 */

const DEFAULTS = { entrypoint: '', workdir: '', user: '', labels: '', readOnlyRoot: false, init: false, privileged: false };
const toLabels = text => Object.fromEntries(text.split('\n').map(l => l.trim()).filter(Boolean).map(l => { const i = l.indexOf('='); return i < 0 ? [l, ''] : [l.slice(0, i).trim(), l.slice(i + 1).trim()]; }));
const fromLabels = labels => Object.entries(labels ?? {}).sort(([a], [b]) => a.localeCompare(b)).map(([k, v]) => `${k}=${v}`).join('\n');
const toForm = d => ({ name: d.name, ...d.advanced, labels: fromLabels(d.advanced.labels) });
const toBody = f => ({ ...f, labels: toLabels(f.labels) });
const bytes = n => n >= 1 << 30 ? `${(n / (1 << 30)).toFixed(1)} GB` : n >= 1 << 20 ? `${Math.round(n / (1 << 20))} MB` : `${Math.max(1, Math.round(n / 1024))} KB`;
const shortId = id => (id || '').replace('sha256:', '').slice(0, 12);

export function AdvancedSettings({ resource, onResourceChange, onDeleted }) {
  const { user } = useSession();
  const canEdit = user.role !== 'viewer';
  const isAdmin = user.role === 'admin' || user.role === 'owner';
  const { data, error, refetch } = useQuery(() => api.advanced(resource.id), [resource.id], { pollMs: 10000 });
  const [saved, setSaved] = useState(null);
  const [form, setForm] = useState(null);
  const [errors, setErrors] = useState({});
  const [feedback, setFeedback] = useState(null);
  const [busy, setBusy] = useState('');
  const [danger, setDanger] = useState(null);
  const dirty = saved && JSON.stringify(form) !== JSON.stringify(saved);
  useEffect(() => { if (data && !dirty) { const f = toForm(data); setSaved(f); setForm(f); } }, [data]); // eslint-disable-line react-hooks/exhaustive-deps

  if (error && !data) return <div className="rp-page"><Notice tone="danger" title="Couldn't load settings">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice></div>;
  if (!data || !form) return <div className="rp-page"><p className="ops-muted">Loading…</p></div>;

  const set = key => value => { setForm(f => ({ ...f, [key]: value })); setErrors(({ [key]: _, ...rest }) => rest); };
  const field = (key, label, hint, control) => <SettingsField label={label} hint={errors[key] ? <span className="ops-error">{errors[key]}</span> : hint} htmlFor={key}>{control}</SettingsField>;
  const input = (key, attrs = {}) => <TextInput id={key} value={form[key]} disabled={!canEdit} aria-invalid={!!errors[key]} onChange={e => set(key)(e.target.value)} spellCheck={false} {...attrs} />;
  const ids = data.identifiers;
  const deployed = !!ids.containerId;
  const status = data.status;
  const drift = data.drift ?? [];
  const envChanged = data.envChanged ?? [];
  const current = resource.currentDeploymentId;

  async function reloadResource() {
    const { resource: r } = await api.resource(resource.id).catch(() => ({}));
    if (r) onResourceChange?.(r);
  }
  async function act(name, fn, done) {
    setBusy(name);
    setFeedback(null);
    try {
      await fn();
      if (done) setFeedback({ tone: 'success', title: done });
      setTimeout(() => { refetch(); reloadResource(); }, 1500);
    } catch (err) {
      setFeedback({ tone: 'danger', title: 'That didn\'t work', text: err.message });
    }
    setBusy('');
  }
  const recreate = reason => act('recreate', () => api.deploy(resource.id, { from: current, reason }), 'Recreating the container with the same release. Follow it under Deployments.');

  async function save(event) {
    event.preventDefault();
    setErrors({});
    try {
      const out = await api.putAdvanced(resource.id, toBody(form));
      const f = toForm({ name: out.resource.name, advanced: out.advanced });
      setSaved(f); setForm(f);
      onResourceChange?.(out.resource);
      setFeedback(out.requiresRedeploy ? { tone: 'success', title: 'Saved. Applies when the container is recreated.', action: 'Recreate now' } : { tone: 'success', title: 'Saved' });
      refetch();
    } catch (err) {
      setErrors(err.fields ?? {});
      setFeedback({ tone: 'danger', title: 'Couldn\'t save', text: Object.keys(err.fields ?? {}).length ? 'Check the highlighted fields.' : err.message });
    }
  }

  return <form className="settings-page rp-settings ops-page" onSubmit={save}>
    {feedback && <Notice tone={feedback.tone} title={feedback.title} action={feedback.action && current && canEdit ? feedback.action : undefined}
      onAction={() => recreate('Apply advanced settings')} onDismiss={() => setFeedback(null)}>{feedback.text}</Notice>}

    <SettingsGroup title="Container">
      {field('name', 'Name', 'Also its hostname on the private network. Renaming takes effect on the next deploy; update anything that calls it by name.', input('name'))}
      {field('entrypoint', 'Entrypoint', 'Overrides the image\'s ENTRYPOINT. Quote arguments with spaces, or use a JSON array. Empty keeps the image\'s.',
        input('entrypoint', { className: 'mono', placeholder: '/usr/local/bin/docker-entrypoint.sh' }))}
      {field('workdir', 'Working directory', 'Empty keeps the image\'s.', input('workdir', { className: 'mono', placeholder: '/app' }))}
      {field('user', 'Run as user', 'A user name or ID, optionally with a group (1000:1000). Empty keeps the image\'s user.', input('user', { className: 'mono', placeholder: '1000' }))}
      {field('labels', 'Labels', 'Docker labels, key=value per line. Names starting with ignition. are reserved.',
        <textarea id="labels" className="labels-input" rows={3} spellCheck={false} disabled={!canEdit} value={form.labels} placeholder="team=platform" onChange={e => set('labels')(e.target.value)} />)}
    </SettingsGroup>

    <SettingsGroup title="Security">
      {field('readOnlyRoot', 'Read-only root filesystem', 'Writes only go to volumes, /tmp and /run. Apps that write elsewhere will fail.',
        <Switch label="Read-only root filesystem" checked={form.readOnlyRoot} disabled={!canEdit} onChange={set('readOnlyRoot')} />)}
      {field('init', 'Init process', 'Runs a tiny init as PID 1: forwards stop signals and cleans up zombie processes. Helps apps that ignore SIGTERM.',
        <Switch label="Init process" checked={form.init} disabled={!canEdit} onChange={set('init')} />)}
      {field('privileged', 'Privileged', isAdmin ? 'Full access to this machine\'s devices and kernel. Only for trusted system tools.' : 'Only admins can change this.',
        <Switch label="Privileged mode" checked={form.privileged} disabled={!isAdmin} onChange={set('privileged')} />)}
    </SettingsGroup>
    {form.privileged && <Notice tone="warning" title="Privileged containers can control this machine">Anyone who can deploy {resource.name} could take over the host. Leave this off unless the app manages the host on purpose.</Notice>}

    <section className="settings-group">
      <h2>Container state</h2>
      <div className="settings-card">
        <div className="lifecycle-actions">
          {status === 'paused'
            ? <Button type="button" icon={PlayIcon} disabled={!canEdit || !!busy} onClick={() => act('resume', () => api.resourceAction(resource.id, 'resume'), 'Resumed.')}>Resume</Button>
            : <Button type="button" icon={PauseIcon} disabled={!canEdit || !!busy || status !== 'running'} title={status !== 'running' ? 'Only a running container can be paused' : 'Freezes its processes in memory; nothing restarts'}
              onClick={() => act('pause', () => api.resourceAction(resource.id, 'pause'), 'Paused. Its processes are frozen and it gets no CPU until you resume it.')}>Pause</Button>}
          <Button type="button" icon={ArrowPathIcon} disabled={!canEdit || !!busy || !current} title="New container from the same release, with the saved settings" onClick={() => recreate('Recreate container')}>{busy === 'recreate' ? 'Starting…' : 'Recreate container'}</Button>
          <Button type="button" disabled={!!busy || !deployed || !canEdit} onClick={() => act('refresh', () => api.resourceAction(resource.id, 'refresh'), 'Status read from Docker again.')}>Refresh status</Button>
        </div>
        {status === 'paused' && <p className="lifecycle-status">Paused: processes are frozen in memory, requests wait or time out, and health checks and scheduled tasks are on hold.</p>}
        {deployed ? <div className="drift">
          <div className="drift-head"><strong>Running container vs saved settings</strong>{drift.length || envChanged.length
            ? canEdit && current && <Button type="button" onClick={() => recreate('Apply saved settings')}>Recreate to apply</Button>
            : <span className="rp-chip success">In sync</span>}</div>
          {drift.length > 0 && <table className="dep-compare"><thead><tr><th>Setting</th><th>Saved</th><th>Running</th></tr></thead>
            <tbody>{drift.map(d => <tr key={d.field} className="diff"><th>{d.field}</th><td><code>{d.saved}</code></td><td><code>{d.running}</code></td></tr>)}</tbody></table>}
          {envChanged.length > 0 && <p className="ops-muted ops-drift-env">Variables changed since this release started: {envChanged.join(', ')}.</p>}
          {!drift.length && !envChanged.length && <p className="ops-muted">The container runs with exactly what's saved.</p>}
        </div> : <p className="lifecycle-status">Not deployed yet.</p>}
      </div>
    </section>

    <SettingsGroup title="Identifiers">
      <SettingsField label="Resource ID"><code className="readonly-value mono">{ids.resourceId}</code><CopyButton value={ids.resourceId} label="Copy resource ID" /></SettingsField>
      {ids.containerId && <SettingsField label="Container"><code className="readonly-value mono">{ids.containerId.slice(0, 12)}</code><CopyButton value={ids.containerId} label="Copy container ID" /></SettingsField>}
      {ids.image && <SettingsField label="Image" hint={ids.imageId ? `ID ${shortId(ids.imageId)}` : undefined}><code className="readonly-value mono ops-ellipsis" title={ids.image}>{ids.image}</code></SettingsField>}
      <SettingsField label="Private network"><code className="readonly-value mono">{ids.network}</code></SettingsField>
      <SettingsField label="Created · changed"><span className="readonly-value">{new Date(ids.createdAt).toLocaleDateString()} · {timeAgo(ids.updatedAt)}</span></SettingsField>
      <SettingsField label="Configuration" hint="Everything except variable values, as JSON."><a className="button" href={`/api/resources/${resource.id}/export`} download><ArrowDownTrayIcon className="button-icon" />Export</a></SettingsField>
    </SettingsGroup>

    {canEdit && <section className="settings-group danger-zone">
      <h2>Danger zone</h2>
      <div className="settings-card">
        <div className="settings-field"><div className="settings-field-label"><strong>Reset container options</strong><span>Back to the image's defaults: entrypoint, working directory, user, labels and security switches. Save to keep it.</span></div>
          <div className="settings-field-control"><Button type="button" onClick={() => { setForm(f => ({ ...f, ...DEFAULTS, privileged: isAdmin ? false : f.privileged })); setErrors({}); }}>Reset</Button></div></div>
        {data.sourceType === 'git' && data.images && <div className="settings-field"><div className="settings-field-label"><strong>Remove old images</strong>
          <span>{data.images.count > 1 ? `${data.images.count} built images, about ${bytes(data.images.bytes)} of their own (layers they share aren't counted). Releases whose image is removed can't be rolled back to without a rebuild.` : 'Only the running image is kept. Nothing to remove.'}</span></div>
          <div className="settings-field-control"><Button type="button" disabled={!isAdmin || data.images.count <= 1} title={isAdmin ? undefined : 'Admins only'} onClick={() => setDanger('purge')}>Remove…</Button></div></div>}
        <div className="settings-field"><div className="settings-field-label"><strong>Move to another environment</strong><span>Domains, variables, volumes and history go along. It's redeployed there and joins that environment's network.</span></div>
          <div className="settings-field-control"><Button type="button" icon={ArrowsRightLeftIcon} disabled={!isAdmin || !(data.moveTargets?.length)} title={isAdmin ? undefined : 'Admins only'} onClick={() => setDanger('move')}>Move…</Button></div></div>
        <div className="settings-field"><div className="settings-field-label"><strong>Delete {resource.name}</strong><span>Removes its container, domains, tasks and history. Can't be undone.</span></div>
          <div className="settings-field-control"><Button type="button" className="danger-solid" icon={TrashIcon} disabled={!isAdmin} title={isAdmin ? undefined : 'Admins only'} onClick={() => setDanger('delete')}>Delete…</Button></div></div>
      </div>
    </section>}

    {danger === 'purge' && <PurgeDialog resource={resource} images={data.images} onClose={() => setDanger(null)} onDone={text => { setDanger(null); setFeedback({ tone: 'success', title: text }); refetch(); }} />}
    {danger === 'move' && <MoveDialog resource={resource} targets={data.moveTargets} onClose={() => setDanger(null)} onDone={out => { setDanger(null); onResourceChange?.(out.resource); setFeedback({ tone: 'success', title: `Moved to ${out.projectName} / ${out.environmentName}`, text: out.deploymentId ? 'It\'s being redeployed there.' : undefined }); refetch(); }} />}
    {danger === 'delete' && <DeleteDialog resource={resource} onClose={() => setDanger(null)} onDone={() => { setDanger(null); onDeleted?.(); }} />}
    {canEdit && <SaveBar dirty={dirty} onDiscard={() => { setForm(saved); setErrors({}); }} summary="Applies when the container is recreated" />}
  </form>;
}

function PurgeDialog({ resource, images, onClose, onDone }) {
  const [keep, setKeep] = useState('3');
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState('');
  return <Dialog open onClose={onClose} title="Remove old images?" description={`Keeps the running image and the newest ones you choose. ${resource.name} has ${images.count} images (${bytes(images.bytes)}).`}
    footer={<><Button onClick={onClose}>Cancel</Button><Button className="danger-solid" disabled={pending} onClick={async () => {
      setPending(true);
      try {
        const out = await api.purgeImages(resource.id, Number(keep));
        onDone(out.removed ? `Removed ${out.removed} image${out.removed === 1 ? '' : 's'}, freed ${bytes(out.freedBytes)}.${out.failed?.length ? ` ${out.failed.length} couldn't be removed.` : ''}` : 'Nothing to remove.');
      } catch (err) { setFailure(err.message); setPending(false); }
    }}>{pending ? 'Removing…' : 'Remove images'}</Button></>}>
    <div className="ops-dialog-body">
      {failure && <Notice tone="danger" title={failure} />}
      <label className="ops-label">Also keep<Select label="Keep" value={keep} onChange={setKeep} options={[['0', 'Only the running image'], ['1', 'The newest other release'], ['3', 'The 3 newest other releases'], ['5', 'The 5 newest other releases']].map(([value, label]) => ({ value, label }))} /></label>
      <p className="ops-muted">Rolling back to a release whose image was removed needs a fresh deploy from source.</p>
    </div>
  </Dialog>;
}

function MoveDialog({ resource, targets, onClose, onDone }) {
  const [target, setTarget] = useState(targets[0]?.environmentId ?? '');
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState('');
  const chosen = targets.find(t => t.environmentId === target);
  return <Dialog open onClose={onClose} title={`Move ${resource.name}?`} description="Its domains, variables, volumes and history move with it. It's redeployed in the new environment, so it changes networks: resources it talked to by name in this environment can't reach it any more (use a shared network for that)."
    footer={<><Button onClick={onClose}>Cancel</Button><Button className="danger-solid" disabled={pending || !target} onClick={async () => {
      setPending(true);
      try { onDone(await api.moveResource(resource.id, target)); } catch (err) { setFailure(Object.values(err.fields ?? {})[0] ?? err.message); setPending(false); }
    }}>{pending ? 'Moving…' : chosen ? `Move to ${chosen.environmentName}` : 'Move'}</Button></>}>
    <div className="ops-dialog-body">
      {failure && <Notice tone="danger" title={failure} />}
      <label className="ops-label">Move to<Select label="Environment" searchable value={target} onChange={setTarget} options={targets.map(t => ({ value: t.environmentId, label: `${t.projectName} / ${t.environmentName}` }))} /></label>
    </div>
  </Dialog>;
}

function DeleteDialog({ resource, onClose, onDone }) {
  const [typed, setTyped] = useState('');
  const [volumes, setVolumes] = useState(false);
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState('');
  return <Dialog open onClose={onClose} title={`Delete ${resource.name}?`} description="Its container, domains, scheduled tasks, tokens and deployment history are removed. This can't be undone."
    footer={<><Button onClick={onClose}>Cancel</Button><Button className="danger-solid" icon={TrashIcon} disabled={typed !== resource.name || pending} onClick={async () => {
      setPending(true);
      try { await api.deleteResource(resource.id, resource.name, volumes); onDone(); } catch (err) { setFailure(err.message); setPending(false); }
    }}>{pending ? 'Deleting…' : 'Delete forever'}</Button></>}>
    <div className="ops-dialog-body">
      {failure && <Notice tone="danger" title={failure} />}
      {resource.volumes?.length > 0 && <label className="check-row"><Checkbox label="Delete volumes" checked={volumes} onChange={setVolumes} />Also delete its volumes and the data in them ({resource.volumes.join(', ')})</label>}
      <p className={cx('confirm-hint')}><ExclamationTriangleIcon />Type <code>{resource.name}</code> to confirm.</p>
      <TextInput value={typed} onChange={e => setTyped(e.target.value)} aria-label={`Type ${resource.name} to confirm`} autoComplete="off" autoFocus />
      {!volumes && resource.volumes?.length > 0 && <p className="ops-muted"><CheckCircleIcon className="inline-icon" />The volumes stay on this machine as Docker volumes named <code>ign-{resource.id.slice(0, 8)}-…</code>, so the data isn't lost. Remove them with <code>docker volume rm</code> when you no longer need them.</p>}
    </div>
  </Dialog>;
}

import { useEffect, useState } from 'react';
import { ArrowPathIcon, CheckCircleIcon, CommandLineIcon, CubeIcon, DocumentMagnifyingGlassIcon, ExclamationTriangleIcon, RocketLaunchIcon } from '@heroicons/react/16/solid';
import { Button, Notice, Select, TextInput } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { useSession } from '../../../app/AuthGate';
import { SaveBar, SettingsField, SettingsGroup } from '../../settings/SettingsField';
import './BuildDeploy.css';

/*
 * How source becomes a running release. Build settings apply on the next build, deploy settings on the next deployment.
 * Automatic builds use the repository's Dockerfile, or detect Node.js, Python, Go and static sites when there isn't one.
 */

const MODES = [
  { value: 'auto', label: 'Automatic', description: 'Dockerfile if there is one, otherwise detect the stack' },
  { value: 'dockerfile', label: 'Dockerfile only', description: 'Fail when the Dockerfile is missing' },
];
const LABELS = {
  mode: 'Builder', dockerfile: 'Dockerfile path', context: 'Build context', target: 'Target stage', args: 'Build arguments', timeoutMinutes: 'Build timeout',
  command: 'Start command', port: 'Port', healthSeconds: 'Health wait', stopGraceSeconds: 'Shutdown grace period', releaseCommand: 'Release command',
};
const BUILD_KEYS = ['mode', 'dockerfile', 'context', 'target', 'args', 'timeoutMinutes'];

// Settings ⇄ form strings, so inputs can be edited freely and validated on save.
const toForm = s => ({
  mode: s.mode, dockerfile: s.dockerfile, context: s.context, target: s.target, args: s.args.join('\n'), timeoutMinutes: String(s.timeoutMinutes),
  command: s.command, port: s.port ? String(s.port) : '', healthSeconds: String(s.healthSeconds), stopGraceSeconds: String(s.stopGraceSeconds), releaseCommand: s.releaseCommand,
});
const toBody = f => ({
  mode: f.mode, dockerfile: f.dockerfile, context: f.context, target: f.target, args: f.args.split('\n').map(a => a.trim()).filter(Boolean),
  timeoutMinutes: Number(f.timeoutMinutes), command: f.command, port: f.port ? Number(f.port) : null,
  healthSeconds: Number(f.healthSeconds), stopGraceSeconds: Number(f.stopGraceSeconds), releaseCommand: f.releaseCommand,
});
const show = (k, v) => v === '' ? '(empty)' : k === 'mode' ? MODES.find(m => m.value === v)?.label : k === 'args' ? `${v.split('\n').filter(Boolean).length} args` : v;

export function BuildDeploy({ resource, onNavigate, onResourceChange }) {
  const { user } = useSession();
  const canEdit = user.role !== 'viewer';
  const { data, error, refetch } = useQuery(() => api.build(resource.id), [resource.id], { pollMs: 8000 });
  const [saved, setSaved] = useState(null);
  const [form, setForm] = useState(null);
  const [errors, setErrors] = useState({});
  const [feedback, setFeedback] = useState(null);
  const [busy, setBusy] = useState(false);
  const [inspect, setInspect] = useState(null);
  const dirty = saved && JSON.stringify(form) !== JSON.stringify(saved);
  useEffect(() => { if (data && !dirty) { const f = toForm(data.settings); setSaved(f); setForm(f); } }, [data]); // eslint-disable-line react-hooks/exhaustive-deps

  if (error && !data) return <div className="rp-page"><Notice tone="danger" title="Couldn't load build settings">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice></div>;
  if (!data || !form) return <div className="rp-page"><p className="bd-muted">Loading…</p></div>;

  const builds = data.builds;
  const last = data.lastBuild;
  const changes = Object.keys(LABELS).filter(k => form[k] !== saved[k]);
  const needsRebuild = builds && changes.some(k => BUILD_KEYS.includes(k));
  const set = key => value => { setForm(f => ({ ...f, [key]: value })); setErrors(({ [key]: _, ...rest }) => rest); setFeedback(null); if (BUILD_KEYS.includes(key)) setInspect(null); };
  const input = (key, props = {}) => <TextInput id={key} value={form[key]} disabled={!canEdit} aria-invalid={!!errors[key]} onChange={e => set(key)(props.numeric ? e.target.value.replace(/\D/g, '') : e.target.value)} spellCheck={false} {...props} numeric={undefined} />;
  const field = (key, hint, control) => <SettingsField label={LABELS[key]} hint={errors[key] ? <span className="bd-error">{errors[key]}</span> : hint} htmlFor={key}>{control}</SettingsField>;

  async function save(event) {
    event.preventDefault();
    setBusy(true); setErrors({});
    try {
      const out = await api.putBuild(resource.id, toBody(form));
      const f = toForm(out.settings);
      setSaved(f); setForm(f);
      if (String(resource.port ?? '') !== f.port || resource.command !== f.command) onResourceChange?.({ ...resource, port: out.settings.port, command: out.settings.command });
      setFeedback({ tone: 'success', title: 'Saved', text: out.requiresRebuild ? 'Build changes apply on the next build.' : 'Applies on the next deployment.', deploy: true });
    } catch (err) {
      setErrors(err.fields ?? {});
      setFeedback({ tone: 'danger', title: 'Couldn\'t save', text: err.fields ? 'Check the highlighted fields.' : err.message });
    }
    setBusy(false);
  }
  async function deploy(noCache = false) {
    setBusy(true); setFeedback(null);
    try {
      await api.deploy(resource.id, noCache ? { noCache: true } : {});
      setFeedback({ tone: 'success', title: noCache ? 'Rebuilding from scratch' : builds ? 'Building and deploying' : 'Deploying', text: 'Follow it under Deployments.', link: true });
      refetch();
    } catch (err) { setFeedback({ tone: 'danger', title: 'Couldn\'t start the deployment', text: err.message }); }
    setBusy(false);
  }
  async function checkRepo() {
    setInspect({ loading: true });
    try { setInspect(await api.inspectBuild(resource.id, toBody(form))); } catch (err) { setInspect({ error: err.fields ? Object.values(err.fields)[0] : err.message }); }
  }

  return <form className="settings-page rp-settings build-deploy" onSubmit={save}>
    <div className="bd-bar">
      <div className="bd-last">
        {last ? <>
          <strong>{builds ? `Build #${last.number}` : `Release #${last.number}`}{last.commit && <code>{last.commit}</code>}</strong>
          <span>{[last.buildInfo || (builds ? 'Dockerfile' : last.imageRef), last.branch && `from ${last.branch}`, timeAgo(last.at), last.current ? 'running now' : last.status].filter(Boolean).join(' · ')}</span>
        </> : <><strong>Nothing deployed yet</strong><span>{builds ? 'The first deployment clones the repository and builds it.' : 'The first deployment pulls the image.'}</span></>}
      </div>
      {canEdit && <div className="bd-actions">
        {builds && <Button type="button" icon={ArrowPathIcon} disabled={busy || dirty} onClick={() => deploy(true)} title="Ignore cached layers and pull fresh base images">Rebuild without cache</Button>}
        <Button type="button" variant="primary" icon={RocketLaunchIcon} disabled={busy || dirty} onClick={() => deploy(false)}>{builds ? 'Build and deploy' : 'Pull and deploy'}</Button>
      </div>}
    </div>
    {dirty && <p className="bd-note">Save or discard your changes before deploying.</p>}
    {feedback && <Notice tone={feedback.tone} title={feedback.title}>{feedback.text}
      {feedback.link && <> <button type="button" className="inline-link" onClick={() => onNavigate?.('Deployments')}>Open Deployments</button></>}
      {feedback.deploy && canEdit && <> <button type="button" className="inline-link" disabled={busy} onClick={() => deploy(false)}>Deploy now</button></>}</Notice>}

    {builds ? <SettingsGroup title="Build" description="Applies on the next build.">
      {field('mode', 'Nixpacks and Buildpacks aren\'t supported; Automatic covers Node.js, Python, Go and static sites without a Dockerfile.',
        <Select id="mode" label="Builder" value={form.mode} options={MODES} onChange={set('mode')} />)}
      {field('dockerfile', 'Relative to the build context.', input('dockerfile', { className: 'mono', placeholder: 'Dockerfile' }))}
      {field('context', 'The folder to build, for monorepos. Empty is the repository root.', input('context', { className: 'mono', prefix: '/', placeholder: 'apps/web' }))}
      {field('target', 'For multi-stage Dockerfiles. Empty builds the last stage.', input('target', { className: 'mono', placeholder: 'runtime' }))}
      {field('args', 'NAME=value per line. Passed as --build-arg; not available at runtime unless the Dockerfile copies them.',
        <textarea id="args" className="input mono bd-args" rows={3} spellCheck={false} disabled={!canEdit} value={form.args} placeholder="NODE_VERSION=22" onChange={e => set('args')(e.target.value)} />)}
      {field('timeoutMinutes', 'The build is stopped and fails after this long.', input('timeoutMinutes', { numeric: true, suffix: 'minutes', className: 'bd-num', inputMode: 'numeric' }))}
      <div className="bd-inspect">
        <Button type="button" icon={DocumentMagnifyingGlassIcon} disabled={inspect?.loading} onClick={checkRepo}>{inspect?.loading ? 'Checking repository…' : 'Check what will be built'}</Button>
        <span className="bd-muted">Clones {resource.branch} and shows what the next build uses, without building.</span>
      </div>
      {inspect && !inspect.loading && <InspectResult result={inspect} />}
    </SettingsGroup> : <section className="settings-group">
      <h2>Build</h2>
      <div className="settings-card bd-empty">
        <CubeIcon />
        <div><strong>Nothing to build</strong>
          <span>{resource.name} runs the image <code>{resource.image}</code>, pulled again on every deployment. Change the image or its registry credentials under <button type="button" className="inline-link" onClick={() => onNavigate?.('Source')}>Source</button>.</span></div>
      </div>
    </section>}

    <SettingsGroup title="Deploy" description="Applies on the next deployment, without rebuilding.">
      {field('command', builds ? 'Overrides the image\'s CMD. Empty uses what the build defines.' : 'Overrides the image\'s CMD. Empty uses the image\'s default.',
        input('command', { className: 'mono', placeholder: builds ? 'npm start' : 'image default' }))}
      {field('port', resource.type === 'application' ? 'The port your app listens on. Domains and the default port mapping use it.' : 'Optional for databases and workers.',
        input('port', { numeric: true, className: 'bd-num', inputMode: 'numeric', placeholder: '3000' }))}
      {field('releaseCommand', 'Runs once in the new release before it takes over, e.g. database migrations. If it fails, the previous release keeps running.',
        input('releaseCommand', { className: 'mono', placeholder: 'npm run migrate' }))}
      {field('healthSeconds', 'The new release must stay up this long before the old one is retired.', input('healthSeconds', { numeric: true, suffix: 'seconds', className: 'bd-num', inputMode: 'numeric' }))}
      {field('stopGraceSeconds', 'Time the old release gets to finish requests after SIGTERM.', input('stopGraceSeconds', { numeric: true, suffix: 'seconds', className: 'bd-num', inputMode: 'numeric' }))}
      <p className="bd-footnote"><CheckCircleIcon />Zero downtime: the new release starts and passes the health wait before the old one stops (unless a fixed host port is mapped under Networking). One instance runs per resource; replicas aren't supported yet.</p>
    </SettingsGroup>

    {changes.length > 0 && <section className="settings-group">
      <h2>Pending changes</h2>
      <div className="settings-card pending-changes">
        {changes.map(k => <div key={k}><span>{LABELS[k]}</span><code>{show(k, saved[k])}</code><span className="arrow">→</span><code>{show(k, form[k])}</code><em>{builds && BUILD_KEYS.includes(k) ? 'Rebuild' : 'Redeploy'}</em></div>)}
      </div>
    </section>}

    <SaveBar dirty={dirty} onDiscard={() => { setForm(saved); setErrors({}); setInspect(null); }} summary={`${changes.length} ${changes.length === 1 ? 'change' : 'changes'} · applies on the next ${needsRebuild ? 'build' : 'deployment'}`} />
  </form>;
}

function InspectResult({ result }) {
  const [open, setOpen] = useState(false);
  if (result.error) return <p className="bd-result bad"><ExclamationTriangleIcon />{result.error}</p>;
  return <div className="bd-result-wrap">
    {result.uses === 'dockerfile' && <p className="bd-result ok"><CheckCircleIcon />Builds with <code>{result.dockerfile}</code> at {result.commit} on {result.branch}.</p>}
    {result.uses === 'detected' && <>
      <p className="bd-result ok"><CommandLineIcon />No Dockerfile, so Ignition will build it as <strong>{result.plan.stack}</strong>: {result.plan.summary}. <button type="button" className="inline-link" onClick={() => setOpen(o => !o)}>{open ? 'Hide' : 'Show'} the Dockerfile</button></p>
      {open && <pre className="bd-dockerfile">{result.plan.dockerfile}</pre>}
    </>}
  </div>;
}

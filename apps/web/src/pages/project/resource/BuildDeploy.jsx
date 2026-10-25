import { useMemo, useState } from 'react';
import { ArrowPathIcon, ArrowUturnLeftIcon, CheckCircleIcon, RocketLaunchIcon, TrashIcon, WrenchIcon } from '@heroicons/react/16/solid';
import { Button, Notice, Select, Switch, TextInput } from '../../../ui';
import { SaveBar, SettingsField, SettingsGroup } from '../../settings/SettingsField';
import { useLater, useSettingsForm } from './shared';

/* How source becomes a running release. Build settings need a rebuild; deploy settings only a redeploy. */

const opts = list => list.map(v => (Array.isArray(v) ? { value: v[0], label: v[1], description: v[2] } : { value: v, label: v }));

const detected = {
  builder: 'nixpacks', dockerfile: 'Dockerfile', context: '.', target: '', install: 'npm ci', build: 'npm run build',
  buildArgs: 'NODE_VERSION=22', buildVars: 'NEXT_PUBLIC_API_URL', platform: 'linux/amd64', buildTimeout: '20', buildMemory: '4096', cache: true,
  start: 'npm start', workdir: '/app', port: '3000', strategy: 'rolling', zeroDowntime: true, replicas: '2', grace: '30',
  deployTimeout: '10', healthDeadline: '120', concurrency: 'cancel', preDeploy: '', postDeploy: '',
};
const labels = {
  builder: 'Builder', dockerfile: 'Dockerfile path', context: 'Build context', target: 'Target stage', install: 'Install command', build: 'Build command',
  buildArgs: 'Build arguments', buildVars: 'Build-time variables', platform: 'Platform', buildTimeout: 'Build timeout', buildMemory: 'Build memory', cache: 'Build cache',
  start: 'Start command', workdir: 'Working directory', port: 'Port', strategy: 'Strategy', zeroDowntime: 'Zero-downtime', replicas: 'Replicas', grace: 'Shutdown grace period',
  deployTimeout: 'Deployment timeout', healthDeadline: 'Health deadline', concurrency: 'Concurrent deployments', preDeploy: 'Pre-deploy hook', postDeploy: 'Post-deploy hook',
};
const buildKeys = new Set(['builder', 'dockerfile', 'context', 'target', 'install', 'build', 'buildArgs', 'buildVars', 'platform', 'buildTimeout', 'buildMemory', 'cache']);
const show = v => v === true ? 'On' : v === false ? 'Off' : v === '' ? '(empty)' : v;

function validate(v) {
  const errors = [];
  const port = Number(v.port);
  if (!Number.isInteger(port) || port < 1 || port > 65535) errors.push('Port must be a number from 1 to 65535.');
  if (Number(v.buildTimeout) < 1 || Number(v.buildTimeout) > 120) errors.push('Build timeout must be between 1 and 120 minutes.');
  if (Number(v.replicas) < 1) errors.push('At least one replica is required.');
  if (v.builder === 'dockerfile' && !v.dockerfile.trim()) errors.push('Enter the Dockerfile path.');
  if (v.builder !== 'dockerfile' && v.builder !== 'static' && !v.start.trim()) errors.push('Enter a start command.');
  for (const line of v.buildArgs.split('\n').filter(Boolean)) if (!/^[A-Za-z_][A-Za-z0-9_]*=/.test(line.trim())) errors.push(`Build argument "${line.trim()}" must look like NAME=value.`);
  return errors;
}

export function BuildDeploy({ resource }) {
  const later = useLater();
  const form = useSettingsForm(detected);
  const { values, set, bind, bindInput } = form;
  const [pipeline, setPipeline] = useState(null);
  const [cacheCleared, setCacheCleared] = useState(false);
  const errors = validate(values);
  const isDocker = values.builder === 'dockerfile';
  const isStatic = values.builder === 'static';

  // Compare against the saved values that useSettingsForm discards back to.
  const [baseline, setBaseline] = useState(detected);
  const changes = useMemo(() => Object.keys(labels).filter(k => values[k] !== baseline[k]), [values, baseline]);
  const needsRebuild = changes.some(k => buildKeys.has(k));

  function run(kind) {
    setPipeline({ kind, state: 'running' });
    later(() => setPipeline({ kind, state: 'done' }), 2500);
  }
  function save(event) {
    event.preventDefault();
    if (errors.length) return;
    form.save();
    setBaseline(values);
  }

  return <form className="settings-page rp-settings build-deploy" onSubmit={save}>
    <div className="pipeline-bar">
      <Button type="button" variant="primary" icon={RocketLaunchIcon} onClick={() => run('deploy')} disabled={pipeline?.state === 'running' || form.dirty}>Build and deploy</Button>
      <Button type="button" icon={WrenchIcon} onClick={() => run('build')} disabled={pipeline?.state === 'running' || form.dirty}>Build only</Button>
      <Button type="button" icon={TrashIcon} onClick={() => setCacheCleared(true)} disabled={cacheCleared}>{cacheCleared ? 'Cache cleared' : 'Clear build cache'}</Button>
      <span className="spacer" />
      <Button type="button" icon={ArrowUturnLeftIcon} onClick={() => Object.entries(detected).forEach(([k, v]) => set(k)(v))}>Reset to detected</Button>
    </div>
    {form.dirty && <p className="pipeline-note">Save or discard your changes before starting a build.</p>}
    {pipeline && <p className={`pipeline-status ${pipeline.state}`}>{pipeline.state === 'running' ? <ArrowPathIcon className="spin" /> : <CheckCircleIcon />}
      {pipeline.state === 'running' ? (pipeline.kind === 'build' ? 'Building image…' : 'Building and deploying…') : pipeline.kind === 'build' ? `Image ${resource.name}:9c41e0a built. It isn't deployed yet.` : 'Deployed. See Deployments for details.'}</p>}

    <SettingsGroup title="Build" description="Changes here take effect on the next build.">
      <SettingsField label="Builder" htmlFor="builder"><Select {...bind('builder')} label="Builder" options={opts([['nixpacks', 'Nixpacks', 'Detects your stack automatically'], ['dockerfile', 'Dockerfile', 'Build from a Dockerfile'], ['buildpacks', 'Cloud Native Buildpacks', 'Heroku-compatible builds'], ['static', 'Static site', 'Serve files with no runtime']])} /></SettingsField>
      {isDocker ? <>
        <SettingsField label="Dockerfile path" htmlFor="dockerfile"><TextInput className="mono" {...bindInput('dockerfile')} /></SettingsField>
        <SettingsField label="Build context" htmlFor="context"><TextInput className="mono" {...bindInput('context')} /></SettingsField>
        <SettingsField label="Target stage" hint="For multi-stage Dockerfiles. Empty builds the last stage." htmlFor="target"><TextInput className="mono" placeholder="runtime" {...bindInput('target')} /></SettingsField>
      </> : <>
        <SettingsField label="Install command" htmlFor="install"><TextInput className="mono" {...bindInput('install')} /></SettingsField>
        <SettingsField label="Build command" htmlFor="build"><TextInput className="mono" {...bindInput('build')} /></SettingsField>
      </>}
      <SettingsField label="Build arguments" hint="NAME=value per line. Passed as --build-arg; not available at runtime." htmlFor="buildArgs">
        <textarea id="buildArgs" className="labels-input" rows={2} spellCheck={false} value={values.buildArgs} onChange={e => set('buildArgs')(e.target.value)} />
      </SettingsField>
      <SettingsField label="Build-time variables" hint="Names of environment variables exposed during the build. Values come from Environment Variables." htmlFor="buildVars">
        <TextInput className="mono" {...bindInput('buildVars')} />
      </SettingsField>
      <SettingsField label="Platform" htmlFor="platform"><Select {...bind('platform')} label="Platform" options={opts([['linux/amd64', 'linux/amd64'], ['linux/arm64', 'linux/arm64']])} /></SettingsField>
      <SettingsField label="Build timeout" htmlFor="buildTimeout"><TextInput type="number" suffix="minutes" {...bindInput('buildTimeout')} /></SettingsField>
      <SettingsField label="Build memory" hint="Separate from the runtime memory limit." htmlFor="buildMemory"><Select {...bind('buildMemory')} label="Build memory" options={opts([['2048', '2 GB'], ['4096', '4 GB'], ['8192', '8 GB']])} /></SettingsField>
      <SettingsField label="Build cache" hint="Reuse dependency layers between builds."><Switch label="Build cache" checked={values.cache} onChange={set('cache')} /></SettingsField>
    </SettingsGroup>

    <SettingsGroup title="Deploy" description="Changes here take effect on the next deployment, without rebuilding.">
      {!isDocker && !isStatic && <SettingsField label="Start command" htmlFor="start"><TextInput className="mono" {...bindInput('start')} /></SettingsField>}
      <SettingsField label="Working directory" htmlFor="workdir"><TextInput className="mono" {...bindInput('workdir')} /></SettingsField>
      <SettingsField label="Port" hint="The port your process listens on." htmlFor="port"><TextInput type="number" {...bindInput('port')} /></SettingsField>
      <SettingsField label="Replicas" htmlFor="replicas"><TextInput type="number" min="1" {...bindInput('replicas')} /></SettingsField>
      <SettingsField label="Strategy" htmlFor="strategy"><Select {...bind('strategy')} label="Strategy" options={opts([['rolling', 'Rolling', 'Replace replicas one at a time'], ['recreate', 'Recreate', 'Stop all replicas, then start the new release']])} /></SettingsField>
      <SettingsField label="Zero-downtime" hint="Keep the old release serving until the new one is healthy."><Switch label="Zero-downtime" checked={values.zeroDowntime && values.strategy === 'rolling'} onChange={set('zeroDowntime')} disabled={values.strategy === 'recreate'} /></SettingsField>
      <SettingsField label="Shutdown grace period" htmlFor="grace"><TextInput type="number" suffix="seconds" {...bindInput('grace')} /></SettingsField>
      <SettingsField label="Deployment timeout" hint="Fail the deployment if the rollout takes longer." htmlFor="deployTimeout"><TextInput type="number" suffix="minutes" {...bindInput('deployTimeout')} /></SettingsField>
      <SettingsField label="Health deadline" hint="Time for new replicas to pass their health check." htmlFor="healthDeadline"><TextInput type="number" suffix="seconds" {...bindInput('healthDeadline')} /></SettingsField>
      <SettingsField label="Concurrent deployments" htmlFor="concurrency"><Select {...bind('concurrency')} label="Concurrent deployments" options={opts([['cancel', 'Cancel the older one', 'A new push replaces a queued deployment'], ['queue', 'Queue them', 'Deploy every push in order']])} /></SettingsField>
      <SettingsField label="Pre-deploy hook" hint="Runs once before traffic switches, e.g. migrations." htmlFor="preDeploy"><TextInput className="mono" placeholder="npm run migrate" {...bindInput('preDeploy')} /></SettingsField>
      <SettingsField label="Post-deploy hook" hint="Runs after the release is live." htmlFor="postDeploy"><TextInput className="mono" placeholder="npm run warm-cache" {...bindInput('postDeploy')} /></SettingsField>
    </SettingsGroup>

    {errors.length > 0 && <Notice tone="danger" title="Fix these before saving">{errors.join(' ')}</Notice>}
    {changes.length > 0 && <section className="settings-group">
      <h2>Pending changes</h2>
      <div className="settings-card pending-changes">
        {changes.map(k => <div key={k}><span>{labels[k]}</span><code>{show(baseline[k])}</code><span className="arrow">→</span><code>{show(values[k])}</code><em>{buildKeys.has(k) ? 'Rebuild' : 'Redeploy'}</em></div>)}
      </div>
    </section>}

    <SaveBar dirty={form.dirty} onDiscard={() => { form.discard(); }} summary={`${changes.length} ${changes.length === 1 ? 'change' : 'changes'} · applies on the next ${needsRebuild ? 'build' : 'deployment'}`} />
  </form>;
}

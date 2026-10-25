import { useMemo, useState } from 'react';
import { ArrowDownTrayIcon, ArrowPathIcon, CheckCircleIcon, ExclamationTriangleIcon, PauseIcon, PlayIcon, TrashIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, Notice, Select, Switch, TextInput } from '../../../ui';
import { projects } from '../../Projects/data';
import { SaveBar, SettingsField, SettingsGroup } from '../../settings/SettingsField';
import { CopyButton, Empty, useLater, useSettingsForm } from './shared';

/* Rare, high-impact controls. Destructive actions sit at the bottom and spell out their impact. */

const opts = list => list.map(v => (Array.isArray(v) ? { value: v[0], label: v[1], description: v[2] } : { value: v, label: v }));

export function AdvancedSettings({ project, resource }) {
  const later = useLater();
  const form = useSettingsForm({ name: resource.name, entrypoint: '', args: '', workdir: '/app', user: '1000', restart: 'on-failure', grace: '30', labels: 'team=platform\ntier=backend', readOnlyRoot: false, privileged: false });
  const { values, set, bind, bindInput } = form;
  const [paused, setPaused] = useState(false);
  const [lifecycle, setLifecycle] = useState(null);
  const [drift, setDrift] = useState([{ field: 'Memory limit', desired: '1 GB', actual: '512 MB' }, { field: 'Label tier', desired: 'backend', actual: '(missing)' }]);
  const [danger, setDanger] = useState(null);
  const [confirmText, setConfirmText] = useState('');
  const [server, setServer] = useState('production-02');
  const [moveTo, setMoveTo] = useState('');
  const [deleted, setDeleted] = useState(false);
  const nameError = values.name && !/^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/.test(values.name) ? 'Use lowercase letters, numbers and dashes.' : '';
  const others = useMemo(() => projects.filter(p => p.name !== project.name).map(p => ({ value: p.name, label: p.name })), [project.name]);
  const ids = { resource: `res_${resource.name.replace(/-/g, '')}8f3a`, container: `c-${resource.name}-7f9d2e41b0aa`, network: 'net_commerce_prod' };

  function runLifecycle(kind, text, ms = 2000) {
    setLifecycle({ kind, state: 'running', text });
    later(() => {
      setLifecycle({ kind, state: 'done', text: { recreate: 'Containers recreated with the same release.', force: 'Containers force-recreated.', refresh: 'Runtime state refreshed. 2 differences found.', reconcile: 'Runtime reconciled with the saved configuration.' }[kind] });
      if (kind === 'reconcile') setDrift([]);
    }, ms);
  }
  function exportConfig() {
    const config = { name: values.name, type: resource.type, source: resource.source, branch: resource.branch, runtime: { entrypoint: values.entrypoint || null, args: values.args || null, workdir: values.workdir, user: values.user, restart: values.restart, grace: Number(values.grace) }, labels: Object.fromEntries(values.labels.split('\n').filter(Boolean).map(l => l.split('='))) };
    const url = URL.createObjectURL(new Blob([JSON.stringify(config, null, 2)], { type: 'application/json' }));
    Object.assign(document.createElement('a'), { href: url, download: `${values.name}.ignition.json` }).click();
    URL.revokeObjectURL(url);
  }

  const dangers = {
    server: { title: 'Move to another server', impact: `${resource.name} is rebuilt on the new server. Expect a short interruption; volumes are copied first.`, action: 'Move server', run: () => setServer(s => s === 'production-02' ? 'production-01' : 'production-02') },
    detach: { title: 'Detach persistent storage', impact: 'The volume is kept, but the resource starts without it. Files written afterwards go to ephemeral storage.', action: 'Detach' },
    reset: { title: 'Reset runtime configuration', impact: 'Entrypoint, working directory, user, labels and restart policy return to detected defaults.', action: 'Reset', run: () => form.discard() },
    purge: { title: 'Purge build artifacts', impact: 'Deletes cached layers and old images. Rollbacks to purged releases will need a rebuild.', action: 'Purge' },
    move: { title: `Move to ${moveTo}`, impact: 'Domains and variables move with it. Internal hostnames change and references from this project break.', action: 'Move' },
    delete: { title: `Delete ${resource.name}`, impact: 'Removes containers, volumes, domains and deployment history. This can\'t be undone.', action: 'Delete forever', typed: true, run: () => setDeleted(true) },
  };
  const active = danger && dangers[danger];

  if (deleted) return <div className="rp-page"><Empty icon={TrashIcon} title={`${resource.name} was deleted`}>Its containers, volumes and domains have been removed.</Empty></div>;

  return <form className="settings-page rp-settings" onSubmit={e => { e.preventDefault(); if (!nameError) form.save(); }}>
    <SettingsGroup title="Runtime">
      <SettingsField label="Name" hint={nameError || 'Renaming changes the internal hostname.'} htmlFor="name"><TextInput aria-invalid={!!nameError} {...bindInput('name')} /></SettingsField>
      <SettingsField label="Entrypoint" hint="Overrides the image's ENTRYPOINT. Empty keeps the image default." htmlFor="entrypoint"><TextInput className="mono" placeholder="/usr/local/bin/docker-entrypoint.sh" {...bindInput('entrypoint')} /></SettingsField>
      <SettingsField label="Arguments" hint="Overrides CMD." htmlFor="args"><TextInput className="mono" placeholder="node server.js --port 3000" {...bindInput('args')} /></SettingsField>
      <SettingsField label="Working directory" htmlFor="workdir"><TextInput className="mono" {...bindInput('workdir')} /></SettingsField>
      <SettingsField label="Run as user ID" hint="Non-root is recommended." htmlFor="user"><TextInput className="mono" {...bindInput('user')} /></SettingsField>
      <SettingsField label="Restart policy" htmlFor="restart"><Select {...bind('restart')} label="Restart policy" options={opts([['always', 'Always'], ['on-failure', 'On failure', 'Only when the process exits with an error'], ['never', 'Never']])} /></SettingsField>
      <SettingsField label="Shutdown grace period" htmlFor="grace"><TextInput type="number" suffix="seconds" {...bindInput('grace')} /></SettingsField>
      <SettingsField label="Labels" hint="key=value per line." htmlFor="labels"><textarea id="labels" className="labels-input" rows={3} spellCheck={false} value={values.labels} onChange={e => set('labels')(e.target.value)} /></SettingsField>
      <SettingsField label="Read-only root filesystem" hint="Writes are only allowed to volumes and /tmp."><Switch label="Read-only root filesystem" checked={values.readOnlyRoot} onChange={set('readOnlyRoot')} /></SettingsField>
      <SettingsField label="Privileged mode" hint="Grants full access to the host."><Switch label="Privileged mode" checked={values.privileged} onChange={set('privileged')} /></SettingsField>
    </SettingsGroup>
    {values.privileged && <Notice tone="warning" title="Privileged containers can control the host">Anyone who can deploy {resource.name} could take over {server}. Only enable this for trusted system tools.</Notice>}

    <section className="settings-group">
      <h2>Lifecycle</h2>
      <div className="settings-card">
        <div className="lifecycle-actions">
          <Button type="button" icon={paused ? PlayIcon : PauseIcon} onClick={() => setPaused(p => !p)}>{paused ? 'Resume' : 'Pause'}</Button>
          <Button type="button" icon={ArrowPathIcon} disabled={lifecycle?.state === 'running'} onClick={() => runLifecycle('recreate', 'Recreating containers…')}>Recreate containers</Button>
          <Button type="button" disabled={lifecycle?.state === 'running'} onClick={() => runLifecycle('force', 'Force-recreating…')}>Force recreate</Button>
          <Button type="button" disabled={lifecycle?.state === 'running'} onClick={() => runLifecycle('refresh', 'Reading runtime state…', 1000)}>Refresh state</Button>
        </div>
        {paused && <p className="lifecycle-status">Paused. The container is frozen in memory and receives no traffic.</p>}
        {lifecycle && <p className={`lifecycle-status ${lifecycle.state}`}>{lifecycle.state === 'running' ? <ArrowPathIcon className="spin" /> : <CheckCircleIcon />}{lifecycle.text}</p>}
        <div className="drift">
          <div className="drift-head"><strong>Configuration drift</strong>{drift.length ? <Button type="button" onClick={() => runLifecycle('reconcile', 'Reconciling…')}>Reconcile</Button> : <span className="rp-chip success">In sync</span>}</div>
          {drift.length > 0 && <table className="dep-compare"><thead><tr><th>Setting</th><th>Saved</th><th>Running</th></tr></thead>
            <tbody>{drift.map(d => <tr key={d.field} className="diff"><th>{d.field}</th><td>{d.desired}</td><td>{d.actual}</td></tr>)}</tbody></table>}
        </div>
      </div>
    </section>

    <SettingsGroup title="Identifiers">
      <SettingsField label="Resource ID"><code className="readonly-value mono">{ids.resource}</code><CopyButton value={ids.resource} label="Copy resource ID" /></SettingsField>
      <SettingsField label="Container ID"><code className="readonly-value mono">{ids.container}</code><CopyButton value={ids.container} label="Copy container ID" /></SettingsField>
      <SettingsField label="Server"><span className="readonly-value">{server}</span></SettingsField>
      <SettingsField label="Created · modified"><span className="readonly-value">Mar 14, 2026 · {form.dirty ? 'unsaved changes' : 'Oct 24, 2026'}</span></SettingsField>
      <SettingsField label="Configuration" hint="Everything except secret values."><Button type="button" icon={ArrowDownTrayIcon} onClick={exportConfig}>Export JSON</Button></SettingsField>
    </SettingsGroup>

    <section className="settings-group danger-zone">
      <h2>Danger zone</h2>
      <div className="settings-card">
        {[
          ['server', 'Change server', `Runs on ${server}.`],
          ...(resource.type !== 'cron' ? [['detach', 'Detach persistent storage', 'Keeps the volume, starts without it.']] : []),
          ['reset', 'Reset runtime configuration', 'Return runtime settings to detected defaults.'],
          ['purge', 'Purge build artifacts', 'Free disk space used by old images.'],
        ].map(([key, title, hint]) => <div key={key} className="settings-field"><div className="settings-field-label"><strong>{title}</strong><span>{hint}</span></div><div className="settings-field-control"><Button type="button" onClick={() => setDanger(key)}>{dangers[key].action}</Button></div></div>)}
        <div className="settings-field"><div className="settings-field-label"><strong>Move to another project</strong><span>Domains and variables move with it.</span></div>
          <div className="settings-field-control"><Select label="Target project" options={[{ value: '', label: 'Choose project…' }, ...others]} value={moveTo} onChange={setMoveTo} /><Button type="button" disabled={!moveTo} onClick={() => setDanger('move')}>Move</Button></div></div>
        <div className="settings-field"><div className="settings-field-label"><strong>Delete {resource.name}</strong><span>Removes everything. Can't be undone.</span></div>
          <div className="settings-field-control"><Button type="button" className="danger-solid" icon={TrashIcon} onClick={() => setDanger('delete')}>Delete</Button></div></div>
      </div>
    </section>

    <Dialog open={!!active} onClose={() => { setDanger(null); setConfirmText(''); }} title={active?.title} description={active?.impact}
      footer={<><Button type="button" onClick={() => { setDanger(null); setConfirmText(''); }}>Cancel</Button>
        <Button type="button" className="danger-solid" disabled={active?.typed && confirmText !== resource.name} onClick={() => { active.run?.(); setDanger(null); setConfirmText(''); if (danger === 'move') setMoveTo(''); }}>{active?.action}</Button></>}>
      {active?.typed && <><p className="confirm-hint"><ExclamationTriangleIcon />Type <code>{resource.name}</code> to confirm.</p><TextInput value={confirmText} onChange={e => setConfirmText(e.target.value)} aria-label={`Type ${resource.name} to confirm`} autoComplete="off" /></>}
    </Dialog>
    <SaveBar dirty={form.dirty} onDiscard={form.discard} summary="Runtime changes recreate the containers when saved" />
  </form>;
}

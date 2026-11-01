import { useState } from 'react';
import {
  ArchiveBoxIcon, ArrowPathIcon, CommandLineIcon, DocumentTextIcon, MapIcon, PencilSquareIcon, PlayIcon, PuzzlePieceIcon, StopIcon, UserGroupIcon,
  AdjustmentsVerticalIcon, ExclamationTriangleIcon,
} from '@heroicons/react/16/solid';
import { Button, Checkbox, Dialog, FormField, Notice, Select, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { useQuery } from '../../../api/hooks';
import { useSession, can } from '../../../app/AuthGate';
import { CopyButton } from '../resource/shared';
import { ModpackPicker, VersionPicker } from './GameWizard';
import { bytes, useGame } from './shared';
import './game.css';

/*
 * The one page most owners need: is it up, what do players type to join, who's on, and the
 * handful of things done every day. Everything else is one click away in the sidebar.
 */

export const versionCmp = (a = '', b = '') => {
  const x = a.split(/[^0-9]+/).filter(Boolean).map(Number), y = b.split(/[^0-9]+/).filter(Boolean).map(Number);
  for (let i = 0; i < Math.max(x.length, y.length); i++) if ((x[i] ?? 0) !== (y[i] ?? 0)) return (x[i] ?? 0) - (y[i] ?? 0);
  return 0;
};

export function gameState(resource, data) {
  if (resource.status === 'deploying') return { tone: 'progress', label: data?.initialized ? 'Updating' : 'Setting up' };
  if (resource.status === 'failed') return { tone: 'danger', label: 'Crashed' };
  if (resource.status === 'paused') return { tone: 'warning', label: 'Paused' };
  if (resource.status !== 'running') return { tone: 'neutral', label: resource.status === 'created' ? 'Not started' : 'Stopped' };
  if (data?.status) return { tone: 'success', label: 'Online' };
  return { tone: 'progress', label: 'Starting' };
}

export function GameOverview({ resource: initial, onNavigate, onResourceChange }) {
  const { user } = useSession();
  const canControl = can(user, 'resources.control');
  const canUpdate = can(user, 'resources.update');
  const canConsole = can(user, 'games.console');
  const res = useQuery(() => api.resource(initial.id), [initial.id], { pollMs: 5000 });
  const resource = res.data?.resource ?? initial;
  const game = useGame(resource, 5000);
  const running = resource.status === 'running';
  const stats = useQuery(() => running ? api.stats(resource.id) : Promise.resolve(null), [resource.id, running], { pollMs: running ? 5000 : 0 });
  const [busy, setBusy] = useState('');
  const [message, setMessage] = useState(null);
  const [changing, setChanging] = useState(false);

  async function act(action) {
    setBusy(action); setMessage(null);
    try {
      await api.resourceAction(resource.id, action);
      setMessage({ tone: 'info', text: { start: 'Starting…', stop: 'Stopping: the server saves the world first.', restart: 'Restarting: the server saves the world first.' }[action] });
      setTimeout(() => { res.refetch(); game.refetch(); }, 1500);
    } catch (e) { setMessage({ tone: 'danger', text: e.message }); }
    setBusy('');
  }
  async function quick(command) {
    setBusy(command); setMessage(null);
    try {
      const out = await api.gameConsole(resource.id, command);
      setMessage({ tone: 'success', text: out.replies ? (out.output || 'Done.') : 'Sent. The reply shows in the console.', mono: !!out.output });
    } catch (e) { setMessage({ tone: 'danger', text: e.message }); }
    setBusy('');
  }

  const data = game.data;
  const state = gameState(resource, data);
  if (game.error && !data) return <div className="rp-page gm-page"><Notice tone="danger" title="Couldn't load the server">{game.error.message}</Notice></div>;
  if (!data) return <div className="rp-page gm-page"><p className="gm-muted">Loading…</p></div>;
  const { spec, edition, status, address } = data;
  const version = data.gameVersion || (edition.customJar ? 'Custom JAR' : '');
  const mem = stats.data?.available ? stats.data.stats : null;
  const kinds = data.content ?? [];

  return <div className="rp-page gm-page gm-overview">
    <header className="gm-hero">
      <div className="gm-hero-main">
        <span className={cx('gm-state', state.tone)}><i />{state.label}</span>
        <h1>{spec.displayName}</h1>
        <p>{data.game.name} {edition.name}{version && ` ${version}`}{spec.loaderVersion && ` · ${edition.loader} ${spec.loaderVersion}`}{spec.modpack && ` · ${spec.modpack.title} ${spec.modpack.versionNumber}`}</p>
      </div>
      <div className="gm-hero-actions">
        {canControl && (running
          ? <><Button icon={ArrowPathIcon} disabled={!!busy} onClick={() => act('restart')}>Restart</Button><Button icon={StopIcon} disabled={!!busy} onClick={() => act('stop')}>Stop</Button></>
          : resource.status !== 'deploying' && <Button variant="primary" icon={PlayIcon} disabled={!!busy || !resource.currentDeploymentId} onClick={() => act('start')}>Start</Button>)}
        {data.console && <Button icon={CommandLineIcon} onClick={() => onNavigate('Console')}>Console</Button>}
      </div>
    </header>

    {message && <Notice tone={message.tone} title={message.tone === 'danger' ? 'That didn\'t work' : undefined}>{message.mono ? <pre className="gm-reply">{message.text}</pre> : message.text}</Notice>}
    {state.label === 'Setting up' && <Notice tone="info" title="Setting up">The first start downloads {data.game.name} {edition.modded ? 'and the mod loader ' : ''}and builds the world. It can take a few minutes; follow along under <button type="button" className="inline-link" onClick={() => onNavigate('Console')}>Console</button>.</Notice>}
    {state.label === 'Starting' && <Notice tone="info" title="Starting">The server is up and loading the world. Players can join once it shows Online.</Notice>}
    {state.label === 'Crashed' && <Notice tone="danger" title="The server stopped unexpectedly">See what it printed last under <button type="button" className="inline-link" onClick={() => onNavigate('Logs')}>Logs</button>, or the last deployment under <button type="button" className="inline-link" onClick={() => onNavigate('Deployments')}>Deployments</button>.</Notice>}

    <div className="gm-tiles">
      <section className="gm-tile gm-address">
        <h2>{edition.group === 'Bedrock Edition' ? 'Server address and port' : 'Server address'}</h2>
        {address ? <>
          <div className="gm-address-line"><code>{edition.group === 'Bedrock Edition' ? address.host : address.display}</code><CopyButton value={edition.group === 'Bedrock Edition' ? address.host : address.display} label="Copy address" /></div>
          {edition.group === 'Bedrock Edition' && <div className="gm-address-line small"><span>Port</span><code>{address.port}</code><CopyButton value={String(address.port)} label="Copy port" /></div>}
          <p>{address.public ? `Players add this as a server. ${address.protocol.toUpperCase()} port ${address.port} must be open on this machine's firewall and router.`
            : <>This is the address you opened Ignition with. For players outside your network, set the public address under <button type="button" className="inline-link" onClick={() => onNavigate('Networking')}>Networking</button> and forward {address.protocol.toUpperCase()} port {address.port} on your router.</>}</p>
        </> : <p>No port is published. Add one under <button type="button" className="inline-link" onClick={() => onNavigate('Networking')}>Networking</button>.</p>}
      </section>
      <section className="gm-tile">
        <h2>Players</h2>
        <div className="gm-big">{status ? <>{status.players}<small> / {status.maxPlayers}</small></> : <small>{running ? 'Waiting for the server' : 'Offline'}</small>}</div>
        {status?.names?.length > 0 && <div className="gm-heads">{status.names.slice(0, 12).map(n => <PlayerHead key={n} name={n} bedrock={edition.group === 'Bedrock Edition'} />)}
          {status.players > 12 && <span className="gm-more">+{status.players - 12}</span>}</div>}
        {status && !status.players && <p>Nobody's online.</p>}
        {status && status.players > 0 && !status.namesKnown && edition.group === 'Bedrock Edition' && <p>Bedrock servers don't share names. Type list in the console to see them.</p>}
        <button type="button" className="inline-link" onClick={() => onNavigate('Players')}>Manage players</button>
      </section>
      <section className="gm-tile">
        <h2>Resources</h2>
        {mem ? <>
          <div className="gm-meter-row"><span>Memory</span><strong>{bytes(mem.memoryBytes)} <small>of {bytes(mem.memoryLimit)}</small></strong></div>
          <Bar value={mem.memoryLimit ? mem.memoryBytes / mem.memoryLimit * 100 : 0} />
          <div className="gm-meter-row"><span>CPU</span><strong>{mem.cpuPercent.toFixed(0)}%</strong></div>
          <Bar value={mem.cpuPercent / Math.max(resource.cpus, 1)} />
        </> : <p>{running ? 'Reading…' : `${(resource.memoryMb / 1024).toFixed(1)} GB memory and ${resource.cpus} CPUs when running.`}</p>}
        <button type="button" className="inline-link" onClick={() => onNavigate('Metrics')}>History</button>
      </section>
    </div>

    {running && data.quick.length > 0 && canConsole && <section className="gm-quick">
      <h2>Quick actions</h2>
      <div>{data.quick.map(q => <Button key={q.command} disabled={!!busy || !status} title={q.hint ?? q.command} onClick={() => quick(q.command)}>{q.label}</Button>)}</div>
    </section>}

    <div className="gm-columns">
      <section className="rp-card gm-facts">
        <header><h2>Server</h2>{canUpdate && <Button icon={PencilSquareIcon} onClick={() => setChanging(true)}>Change type or version</Button>}</header>
        <dl>
          <div><dt>Type</dt><dd>{data.game.name} {edition.name}</dd></div>
          {version && <div><dt>Version</dt><dd>{version}</dd></div>}
          {edition.loader && <div><dt>{edition.loader}</dt><dd>{spec.loaderVersion || 'Latest'}</dd></div>}
          {spec.modpack && <div><dt>Modpack</dt><dd>{spec.modpack.title} {spec.modpack.versionNumber}</dd></div>}
          {status?.version && <div><dt>Reports</dt><dd>{status.version}</dd></div>}
          <div><dt>Image</dt><dd className="mono">{data.image}</dd></div>
          <div><dt>Slots</dt><dd>{status?.maxPlayers ?? spec.slots} <button type="button" className="inline-link" onClick={() => onNavigate('Server Settings')}>Change</button></dd></div>
        </dl>
      </section>
      <nav className="rp-card gm-shortcuts" aria-label="Manage">
        {[[UserGroupIcon, 'Players', 'Whitelist, operators, bans'],
          ...(kinds.length ? [[PuzzlePieceIcon, 'Mods & Plugins', `Add ${kinds.map(k => k.label.toLowerCase()).join(' and ')} from Modrinth`]] : []),
          [MapIcon, 'Worlds', 'Upload, switch, reset'], [AdjustmentsVerticalIcon, 'Server Settings', 'Difficulty, PvP, view distance…'],
          [ArchiveBoxIcon, 'Backups', 'Back up and restore'], [DocumentTextIcon, 'Files', 'Every file on the server']].map(([Icon, title, text]) =>
          <button key={title} type="button" className="gm-shortcut" onClick={() => onNavigate(title)}><Icon /><span><strong>{title}</strong><small>{text}</small></span></button>)}
      </nav>
    </div>

    {changing && <ChangeServerDialog resource={resource} data={data} onClose={() => setChanging(false)}
      onDone={out => { setChanging(false); game.refetch(); res.refetch(); onResourceChange?.({ ...resource, image: out.image }); setMessage({ tone: 'success', text: out.deploymentId ? 'Switching now: the server stops, saves, and starts on the new version. Follow it under Deployments.' : 'Saved. It applies on the next deployment.' }); }} />}
  </div>;
}

// A thin usage bar; the numbers are printed beside it.
function Bar({ value }) {
  const v = Math.min(Math.max(value || 0, 0), 100);
  return <div className={cx('gm-bar', v >= 90 ? 'critical' : v >= 70 && 'high')} role="meter" aria-valuenow={Math.round(v)} aria-valuemin={0} aria-valuemax={100}><i style={{ width: `${v}%` }} /></div>;
}

function PlayerHead({ name, bedrock }) {
  const [broken, setBroken] = useState(false);
  if (bedrock || broken) return <span className="gm-head letter" title={name}>{name[0]?.toUpperCase()}</span>;
  return <img className="gm-head" src={`https://mc-heads.net/avatar/${encodeURIComponent(name)}/24`} alt={name} title={name} onError={() => setBroken(true)} />;
}

function ChangeServerDialog({ resource, data, onClose, onDone }) {
  const groupEditions = data.editions.filter(e => e.group === data.edition.group);
  const [edition, setEdition] = useState(data.edition);
  const [spec, setSpec] = useState({ ...data.spec, modpack: data.spec.modpack ? { id: data.spec.modpack.projectId, title: data.spec.modpack.title, iconUrl: data.spec.modpack.iconUrl, versionId: data.spec.modpack.versionId } : null });
  const [backup, setBackup] = useState(true);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const set = patch => setSpec(s => ({ ...s, ...patch }));
  const older = !edition.modpacks && !edition.customJar && data.gameVersion && spec.version && versionCmp(spec.version, data.gameVersion) < 0;
  const changed = edition.id !== data.edition.id || spec.version !== data.spec.version || (spec.loaderVersion ?? '') !== (data.spec.loaderVersion ?? '')
    || spec.customUrl !== data.spec.customUrl || (spec.java ?? 0) !== (data.spec.java ?? 0) || spec.modpack?.id !== data.spec.modpack?.projectId || (spec.modpack?.versionId ?? '') !== (data.spec.modpack?.versionId ?? '');
  async function save() {
    setError('');
    try {
      if (backup) { setBusy('Backing up…'); await api.createBackup(resource.id, 'data', { label: 'before version change' }); }
      setBusy('Switching…');
      const out = await api.updateGame(resource.id, {
        edition: edition.id, version: spec.version ?? '', loaderVersion: spec.loaderVersion ?? '', customUrl: spec.customUrl ?? '', java: spec.java ?? 0, deploy: true,
        ...(edition.modpacks && spec.modpack ? { modpack: { projectId: spec.modpack.id, versionId: spec.modpack.versionId ?? '' } } : {}),
      });
      onDone(out);
    } catch (e) { setError(e.fields ? Object.values(e.fields).join(' ') : e.message); setBusy(''); }
  }
  return <Dialog open onClose={onClose} title="Change type or version" description="The world, players and settings stay. The server restarts on the new version." className="gm-dialog"
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" disabled={!changed || !!busy} onClick={save}>{busy || 'Switch and restart'}</Button></>}>
    <FormField label="Type" htmlFor="gm-ed">
      <Select id="gm-ed" value={edition.id} onChange={id => { setEdition(groupEditions.find(e => e.id === id)); set({ version: id === data.edition.id ? data.spec.version : '', loaderVersion: '' }); }}
        options={groupEditions.map(e => ({ value: e.id, label: e.name }))} />
    </FormField>
    {edition.modpacks ? <ModpackPicker game={data.game} spec={spec} set={set} />
      : edition.customJar ? <>
        <FormField label="Server .jar download link" htmlFor="gm-cjar"><TextInput id="gm-cjar" className="mono" value={spec.customUrl ?? ''} onChange={e => set({ customUrl: e.target.value })} /></FormField>
        <FormField label="Java" htmlFor="gm-cj"><Select id="gm-cj" value={String(spec.java || 21)} onChange={v => set({ java: Number(v) })} options={[25, 21, 17, 11, 8].map(n => ({ value: String(n), label: `Java ${n}` }))} /></FormField>
      </> : <VersionPicker key={edition.id} game={data.game} edition={edition} spec={spec} set={set} compactView />}
    {edition.id !== data.edition.id && data.content.length > 0 && <Notice tone="warning" title="Mods and plugins may not carry over">{data.edition.name} content doesn't run on every type. Check Mods & Plugins after the switch.</Notice>}
    {older && <Notice tone="danger" title={`Going back from ${data.gameVersion} to ${spec.version}`}><ExclamationTriangleIcon className="gm-inline-icon" />Minecraft can't open a world saved by a newer version. Restore a backup from before the upgrade instead, or start a new world.</Notice>}
    <div className="gm-check" onClick={() => setBackup(b => !b)}><Checkbox checked={backup} label="Back up first" onChange={setBackup} /><span><strong>Back up first</strong><small>Recommended: a world opened by a newer version can't go back.</small></span></div>
    {error && <p className="gm-error">{error}</p>}
  </Dialog>;
}

import { useState } from 'react';
import {
  ArrowPathIcon, ArrowTopRightOnSquareIcon, BeakerIcon, CodeBracketIcon, DocumentTextIcon, EllipsisHorizontalIcon, LockClosedIcon, PlayIcon,
  RocketLaunchIcon, SparklesIcon, StopIcon, TrashIcon,
} from '@heroicons/react/16/solid';
import { Button, Checkbox, Dialog, Menu, Notice, SearchField, SegmentedControl, Status } from '../../ui';
import { api } from '../../api/client';
import { timeAgo, useQuery } from '../../api/hooks';
import { useSession, can } from '../../app/AuthGate';
import { Table } from '../project/resource/shared';
import { bytes, isAdmin, isDeveloper, shortId } from './model';
import { ConfirmDialog, JobProgress, Unavailable } from './shared';

const stateTone = { running: 'success', paused: 'warning', restarting: 'warning', exited: 'neutral', created: 'neutral', dead: 'danger', removing: 'warning' };

function OwnerCell({ owner, onOpenResource }) {
  if (owner.kind === 'resource') return <button type="button" className="sv-link" onClick={e => { e.stopPropagation(); onOpenResource(owner.resource, 'Overview'); }}>{owner.resource.name}<span>{owner.resource.projectName}</span></button>;
  return <span className="muted">{{ helper: 'Ignition helper', ignition: 'Ignition (no current resource)', unmanaged: 'Not managed' }[owner.kind]}</span>;
}

/* ------------------------------------ Docker ------------------------------------ */

export function Docker({ server, onNavigate, onOpenResource, setFeedback }) {
  const { user } = useSession();
  const admin = isAdmin(user);
  const [view, setView] = useState('containers');
  const [inspect, setInspect] = useState(null);
  const [cleanup, setCleanup] = useState(false);
  const overview = useQuery(() => api.serverDocker(server.id), [server.id], { pollMs: 15000 });
  const o = overview.data;

  if (overview.error && !o) return <Unavailable server={server} error={overview.error} what="Docker" onNavigate={onNavigate} />;
  return <>
    <section className="ov-hero sv-docker-hero">
      <div className="ov-status">
        <span className="ov-label">Docker daemon</span>
        {o ? <Status tone={o.compatible ? 'success' : 'danger'}>{o.compatible ? `Running · Docker ${o.engine.version}` : `Incompatible · Docker ${o.engine.version}`}</Status> : <Status tone="neutral">Checking…</Status>}
        {o && <dl>
          <div><dt>API</dt><dd className={o.compatible ? undefined : 'bad'}>{o.engine.apiVersion} (needs ≥ {o.minApiVersion})</dd></div>
          <div><dt>Storage driver</dt><dd>{o.engine.storageDriver}</dd></div>
          {o.dataRoot && <div><dt>Data root</dt><dd className="mono">{o.dataRoot}</dd></div>}
          <div><dt>Containers</dt><dd>{o.counts.containers} ({o.counts.running} running)</dd></div>
          <div><dt>Images</dt><dd>{o.counts.images}{o.disk ? ` · ${bytes(o.disk.images)}` : ''}</dd></div>
          <div><dt>Networks</dt><dd>{o.counts.networks}</dd></div>
          <div><dt>Volumes</dt><dd>{o.counts.volumes}{o.disk ? ` · ${bytes(o.disk.volumes)}` : ''}</dd></div>
          {o.disk && <div><dt>Build cache</dt><dd>{bytes(o.disk.buildCache)} ({bytes(o.disk.buildCacheReclaimable)} unused)</dd></div>}
        </dl>}
      </div>
      <div className="ov-actions">
        <span className="ov-label">Maintenance</span>
        <div>{can(user, 'servers.cleanup') && <Button icon={SparklesIcon} onClick={() => setCleanup(true)} disabled={!o}>Clean up…</Button>}</div>
      </div>
    </section>
    {o?.engine.warnings?.length > 0 && <Notice tone="warning" title="Docker reports warnings">{o.engine.warnings.join(' ')}</Notice>}

    <div className="rp-toolbar">
      <SegmentedControl label="Object type" className="auto-width" value={view} onChange={setView}
        options={[{ value: 'containers', label: 'Containers' }, { value: 'images', label: 'Images' }, { value: 'networks', label: 'Networks' }, { value: 'volumes', label: 'Volumes' }]} />
    </div>
    {view === 'containers' && <Containers server={server} onInspect={setInspect} onOpenResource={onOpenResource} setFeedback={setFeedback} />}
    {view === 'images' && <Images server={server} onInspect={setInspect} setFeedback={setFeedback} />}
    {view === 'networks' && <Networks server={server} onInspect={setInspect} setFeedback={setFeedback} />}
    {view === 'volumes' && <Volumes server={server} onInspect={setInspect} onOpenResource={onOpenResource} />}

    {inspect && <InspectDialog server={server} {...inspect} onClose={() => setInspect(null)} />}
    {cleanup && <CleanupDialog server={server} onClose={() => { setCleanup(false); overview.refetch(); }} />}
  </>;
}

function Containers({ server, onInspect, onOpenResource, setFeedback }) {
  const { user } = useSession();
  const [query, setQuery] = useState('');
  const [logs, setLogs] = useState(null);
  const [stopping, setStopping] = useState(null);
  const { data, error, refetch } = useQuery(() => api.serverContainers(server.id), [server.id], { pollMs: 10000 });
  const usage = useQuery(() => api.serverResources(server.id), [server.id], { enabled: server.isLocal, pollMs: 15000 });
  const byResource = Object.fromEntries((usage.data?.resources ?? []).map(r => [r.id, r]));
  const list = (data?.containers ?? []).filter(c => !query || `${c.name} ${c.image} ${c.id}`.toLowerCase().includes(query.toLowerCase()));
  async function act(c, action) {
    try { await api.serverContainerAction(server.id, c.id, action); setFeedback({ tone: 'success', title: `${action[0].toUpperCase()}${action.slice(1)} queued for ${c.name}`, text: 'It runs through the resource\'s job queue.' }); refetch(); }
    catch (e) { setFeedback({ tone: 'danger', title: `Couldn't ${action} ${c.name}`, text: e.message }); }
  }
  const menu = c => {
    const owned = c.owner.kind === 'resource';
    const dev = isDeveloper(user) && owned;
    return [
      ...(owned ? [{ label: 'Open resource', icon: ArrowTopRightOnSquareIcon, onSelect: () => onOpenResource(c.owner.resource, 'Overview') }] : []),
      { label: 'View logs', icon: DocumentTextIcon, onSelect: () => setLogs(c) },
      ...(can(user, 'servers.inspect') ? [{ label: 'Inspect', icon: CodeBracketIcon, onSelect: () => onInspect({ kind: 'container', id: c.id, name: c.name }) }] : []),
      ...(dev ? ['separator',
        ...(c.state === 'running' ? [{ label: 'Restart', icon: ArrowPathIcon, onSelect: () => act(c, 'restart') }, { label: 'Stop', icon: StopIcon, onSelect: () => setStopping(c) }]
          : [{ label: 'Start', icon: PlayIcon, onSelect: () => act(c, 'start') }]),
        { label: 'Recreate (redeploy resource)', icon: RocketLaunchIcon, onSelect: () => api.deploy(c.owner.resource.id, { reason: 'Recreated from the server\'s Docker page' }).then(() => setFeedback({ tone: 'success', title: `Redeploy started for ${c.owner.resource.name}` }), e => setFeedback({ tone: 'danger', title: 'Redeploy failed', text: e.message })) },
      ] : []),
    ];
  };
  if (error && !data) return <Notice tone="danger" title="Couldn't list containers">{error.message}</Notice>;
  return <>
    <div className="rp-toolbar"><SearchField value={query} onChange={setQuery} placeholder="Search name, image or ID" /><span className="spacer" /><span className="rp-row-meta">Only Ignition resources can be started or stopped here.</span></div>
    <Table className="sv-table-wrap" columns={[{ label: 'Container' }, { label: 'Owner' }, { label: 'Image' }, { label: 'State' }, { label: 'Created' }, { label: 'CPU', className: 'num' }, { label: 'Memory', className: 'num' }, { label: 'Ports' }, { label: '', className: 'actions' }]}>
      {list.map(c => { const r = c.owner.kind === 'resource' ? byResource[c.owner.resource.id] : null; const live = r?.usage && r.status === 'running'; return <tr key={c.id}>
        <td><span className="resource-name">{c.name}<span className="mono">{shortId(c.id)}</span></span></td>
        <td><OwnerCell owner={c.owner} onOpenResource={onOpenResource} /></td>
        <td className="mono sv-ellipsis" title={`${c.image}\n${c.imageId}`}>{c.image}</td>
        <td><Status tone={stateTone[c.state] ?? 'neutral'}>{c.status || c.state}</Status></td>
        <td className="muted">{timeAgo(c.created)}</td>
        <td className="num">{live ? `${r.usage.cpuPercent.toFixed(1)}%` : '—'}</td>
        <td className="num">{live ? bytes(r.usage.memoryBytes) : '—'}</td>
        <td className="mono sv-ellipsis">{(c.ports ?? []).filter(p => p.hostPort).map(p => `${p.hostIp && p.hostIp !== '0.0.0.0' ? `${p.hostIp}:` : ''}${p.hostPort}→${p.containerPort}`).filter((v, i, a) => a.indexOf(v) === i).join(', ') || '—'}</td>
        <td className="actions"><Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${c.name}`} items={menu(c)} /></td>
      </tr>; })}
    </Table>
    {!list.length && <p className="monitor-empty">{data ? 'No containers.' : 'Loading…'}</p>}
    {logs && <ContainerLogsDialog server={server} container={logs} onClose={() => setLogs(null)} />}
    {stopping && <ConfirmDialog title={`Stop ${stopping.name}?`} description={`This stops ${stopping.owner.resource.name}: it won't serve until started again. Its volumes are kept.`} danger confirmLabel="Stop"
      onClose={() => setStopping(null)} onConfirm={async () => { await api.serverContainerAction(server.id, stopping.id, 'stop'); setStopping(null); setFeedback({ tone: 'success', title: `Stop queued for ${stopping.name}` }); refetch(); }} />}
  </>;
}

function ContainerLogsDialog({ server, container, onClose }) {
  const { data, error } = useQuery(() => api.serverLogs(server.id, { source: 'container', container: container.id, range: '24h', limit: 300 }), [container.id], { pollMs: 5000 });
  return <Dialog open onClose={onClose} title={`Logs · ${container.name}`} description="Last 300 lines from the past 24 hours, secrets masked. Use the Logs page to search, filter and download." className="sv-wide-dialog" footer={<Button onClick={onClose}>Close</Button>}>
    {error ? <Notice tone={error.status === 403 ? 'warning' : 'danger'} title={error.status === 403 ? 'Permission denied' : 'Couldn\'t read logs'}>{error.message}</Notice>
      : <pre className="sv-pre sv-log">{data ? (data.lines.length ? [...data.lines].reverse().map(l => `${new Date(l.time).toLocaleTimeString()} ${l.stream === 'stderr' ? 'ERR' : 'OUT'} ${l.message}`).join('\n') : 'No output in the last 24 hours.') : 'Loading…'}</pre>}
  </Dialog>;
}

function Images({ server, onInspect, setFeedback }) {
  const { user } = useSession();
  const [query, setQuery] = useState('');
  const [removing, setRemoving] = useState(null);
  const [job, setJob] = useState(null);
  const { data, error, refetch } = useQuery(() => api.serverImages(server.id), [server.id]);
  const list = (data?.images ?? []).filter(i => !query || `${i.tags.join(' ')} ${i.id}`.toLowerCase().includes(query.toLowerCase()));
  if (error && !data) return <Notice tone="danger" title="Couldn't list images">{error.message}</Notice>;
  return <>
    <div className="rp-toolbar"><SearchField value={query} onChange={setQuery} placeholder="Search tag or ID" /><span className="spacer" />{data && <span className="rp-row-meta">{bytes(data.layersSize)} on disk</span>}</div>
    {job && <JobProgress jobId={job} title="Removing image" onDone={() => refetch()} renderResult={j => <CleanupResult result={j.result} />} />}
    <Table className="sv-table-wrap" columns={[{ label: 'Image' }, { label: 'ID / digest' }, { label: 'Size', className: 'num' }, { label: 'Created' }, { label: 'Used by' }, { label: '', className: 'actions' }]}>
      {list.map(i => { const removable = !i.usedBy.length && !i.retained && i.containers <= 0; return <tr key={i.id}>
        <td><span className="resource-name">{i.tags[0] ?? <span className="muted">untagged</span>}{i.tags.length > 1 && <span>+{i.tags.length - 1} tags</span>}</span></td>
        <td className="mono" title={[i.id, ...i.digests].join('\n')}>{shortId(i.id)}</td>
        <td className="num">{bytes(i.size)}</td>
        <td className="muted">{timeAgo(i.created)}</td>
        <td>{i.usedBy.length ? <span className="sv-ellipsis">{i.running ? <Status tone="success">{i.usedBy.join(', ')}</Status> : i.usedBy.join(', ')}</span>
          : i.retained ? <span className="rp-chip" title="A successful deployment ran this image; it's kept so you can roll back.">Kept for rollback</span> : <span className="muted">Unused</span>}</td>
        <td className="actions"><Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${i.tags[0] ?? shortId(i.id)}`} items={[
          ...(can(user, 'servers.inspect') ? [{ label: 'Inspect', icon: CodeBracketIcon, onSelect: () => onInspect({ kind: 'image', id: i.id, name: i.tags[0] ?? shortId(i.id) }) }] : []),
          ...(can(user, 'servers.cleanup') && removable ? [{ label: 'Remove image', icon: TrashIcon, danger: true, onSelect: () => setRemoving(i) }] : []),
        ]} /></td>
      </tr>; })}
    </Table>
    {!list.length && <p className="monitor-empty">{data ? 'No images.' : 'Loading…'}</p>}
    {removing && <ConfirmDialog danger title={`Remove ${removing.tags[0] ?? shortId(removing.id)}?`} confirmLabel="Remove image"
      description="No container uses it and no deployment keeps it for rollback. If it's needed later it will be pulled or built again. Ignition checks again right before removing it, and Docker refuses if something started using it."
      onClose={() => setRemoving(null)} onConfirm={async () => {
        const r = await api.runCleanup(server.id, { dangling: true, unusedImages: true }, [{ kind: 'image', id: removing.id, name: removing.tags[0] ?? shortId(removing.id), size: removing.size, reason: '' }]);
        setRemoving(null); setJob(r.jobId);
      }} />}
  </>;
}

function Networks({ server, onInspect }) {
  const { user } = useSession();
  const [removing, setRemoving] = useState(null);
  const [job, setJob] = useState(null);
  const { data, error, refetch } = useQuery(() => api.serverNetworks(server.id), [server.id]);
  if (error && !data) return <Notice tone="danger" title="Couldn't list networks">{error.message}</Notice>;
  return <>
    {job && <JobProgress jobId={job} title="Removing network" onDone={() => refetch()} renderResult={j => <CleanupResult result={j.result} />} />}
    <Table className="sv-table-wrap" columns={[{ label: 'Network' }, { label: 'Driver' }, { label: 'Scope' }, { label: 'Subnets' }, { label: 'Containers' }, { label: '', className: 'actions' }]}>
      {(data?.networks ?? []).map(n => { const removable = !n.system && !n.ignition && !n.containers.length; return <tr key={n.id}>
        <td><span className="resource-name">{n.name}{n.system ? <span>Docker built-in</span> : n.ignition ? <span>Ignition</span> : null}{n.internal && <span>internal</span>}</span></td>
        <td className="muted">{n.driver}</td>
        <td className="muted">{n.scope}</td>
        <td className="mono">{n.subnets.join(', ') || '—'}</td>
        <td className="sv-ellipsis" title={n.containers.map(c => `${c.name} ${c.ip}`).join('\n')}>{n.containers.length ? n.containers.map(c => c.name).join(', ') : <span className="muted">None</span>}</td>
        <td className="actions"><Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${n.name}`} items={[
          ...(can(user, 'servers.inspect') ? [{ label: 'Inspect', icon: CodeBracketIcon, onSelect: () => onInspect({ kind: 'network', id: n.id, name: n.name }) }] : []),
          ...(can(user, 'servers.cleanup') && removable ? [{ label: 'Remove unused network', icon: TrashIcon, danger: true, onSelect: () => setRemoving(n) }] : []),
        ]} /></td>
      </tr>; })}
    </Table>
    {removing && <ConfirmDialog danger title={`Remove network ${removing.name}?`} confirmLabel="Remove network" description="No container is attached to it. Docker refuses if one attaches before it's removed."
      onClose={() => setRemoving(null)} onConfirm={async () => { const r = await api.runCleanup(server.id, { networks: true }, [{ kind: 'network', id: removing.id, name: removing.name, size: -1, reason: '' }]); setRemoving(null); setJob(r.jobId); }} />}
  </>;
}

function Volumes({ server, onInspect, onOpenResource }) {
  const { user } = useSession();
  const { data, error } = useQuery(() => api.serverVolumes(server.id), [server.id]);
  if (error && !data) return <Notice tone="danger" title="Couldn't list volumes">{error.message}</Notice>;
  return <>
    <Notice tone="info" icon={LockClosedIcon} title="Volumes hold persistent data">They can't be deleted from this page or by cleanups. Delete a resource's volume from its Storage page, where the data it holds is explained.</Notice>
    <Table className="sv-table-wrap" columns={[{ label: 'Volume' }, { label: 'Owner' }, { label: 'Driver' }, { label: 'Size', className: 'num' }, { label: 'Mounted by' }, { label: '', className: 'actions' }]}>
      {(data?.volumes ?? []).map(v => <tr key={v.name}>
        <td><span className="resource-name mono">{v.name.length > 40 ? `${v.name.slice(0, 40)}…` : v.name}{!v.persistent && <span>anonymous</span>}</span></td>
        <td>{v.owner.kind === 'unmanaged' ? <span className="rp-chip warning" title="Ignition doesn't know what this volume is for. Treat it as important data.">Unknown owner</span> : <OwnerCell owner={v.owner} onOpenResource={onOpenResource} />}</td>
        <td className="muted">{v.driver}</td>
        <td className="num" title={v.size < 0 ? 'Docker hasn\'t measured this volume.' : undefined}>{v.size < 0 ? 'Not measured' : bytes(v.size)}</td>
        <td className="sv-ellipsis">{v.mountedBy.length ? v.mountedBy.join(', ') : <span className="muted">Not mounted</span>}</td>
        <td className="actions">{can(user, 'servers.inspect') && <Menu trigger={EllipsisHorizontalIcon} label={`Actions for ${v.name}`} items={[{ label: 'Inspect', icon: CodeBracketIcon, onSelect: () => onInspect({ kind: 'volume', id: v.name, name: v.name }) }]} />}</td>
      </tr>)}
    </Table>
    {!(data?.volumes ?? []).length && <p className="monitor-empty">{data ? 'No volumes.' : 'Loading…'}</p>}
  </>;
}

function InspectDialog({ server, kind, id, name, onClose }) {
  const { data, error } = useQuery(() => api.inspectServerObject(server.id, kind, id), [kind, id]);
  return <Dialog open onClose={onClose} title={`Inspect ${kind} · ${name}`} description={data?.redacted ? 'Environment values are masked. Labels and commands are shown as Docker reports them.' : 'As Docker reports it.'} className="sv-wide-dialog" footer={<Button onClick={onClose}>Close</Button>}>
    {error ? <Notice tone="danger" title="Couldn't inspect">{error.message}</Notice> : <pre className="sv-pre sv-log">{data ? JSON.stringify(data.document, null, 2) : 'Loading…'}</pre>}
  </Dialog>;
}

/* ------------------------------------ Cleanup ----------------------------------- */

const cleanupOptions = [
  { key: 'containers', label: 'Stopped Ignition containers', text: 'Stopped containers from earlier deployments of this Ignition\'s resources. Containers it didn\'t create, or that belong to another install, are never touched.' },
  { key: 'dangling', label: 'Untagged images', text: 'Images no tag points to and no container uses.' },
  { key: 'unusedImages', label: 'Unused tagged images', text: 'Tagged images no container uses and no deployment keeps for rollback. They\'ll be pulled again if needed.' },
  { key: 'networks', label: 'Unused networks', text: 'Custom networks with no containers. Docker\'s built-in and Ignition\'s in-use networks are kept.' },
  { key: 'buildCache', label: 'Unused build cache', text: 'Speeds up rebuilds; the next build of each app may be slower.' },
];

export function CleanupDialog({ server, onClose }) {
  const [opts, setOpts] = useState({ containers: true, dangling: true, unusedImages: false, networks: false, buildCache: true });
  const [plan, setPlan] = useState(null);
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  const [jobId, setJobId] = useState(null);
  const [typed, setTyped] = useState('');
  async function preview() {
    setPending(true); setError('');
    try { setPlan(await api.previewCleanup(server.id, opts)); } catch (e) { setError(e.message); } finally { setPending(false); }
  }
  async function run() {
    setPending(true); setError('');
    try { setJobId((await api.runCleanup(server.id, opts, plan.items)).jobId); } catch (e) { setError(e.message); } finally { setPending(false); }
  }
  const groups = plan ? ['container', 'image', 'network', 'buildcache'].map(k => [k, plan.items.filter(i => i.kind === k)]).filter(([, l]) => l.length) : [];
  const footer = jobId ? <Button onClick={onClose}>Close</Button>
    : plan ? <><Button onClick={() => { setPlan(null); setTyped(''); }}>Back</Button><Button className="danger-solid" disabled={!plan.items.length || typed !== 'clean up' || pending} onClick={run}>{pending ? 'Starting…' : `Remove ${plan.items.length} object${plan.items.length === 1 ? '' : 's'}`}</Button></>
      : <><Button onClick={onClose}>Cancel</Button><Button variant="primary" disabled={pending || !Object.values(opts).some(Boolean)} onClick={preview}>{pending ? 'Checking…' : 'Preview'}</Button></>;
  return <Dialog open onClose={onClose} title={`Clean up Docker on ${server.name}`} className="sv-wide-dialog" icon={SparklesIcon}
    description={jobId ? 'Running as a job; it continues if you close this.' : plan ? 'Exactly these objects would be removed. Everything else stays.' : 'Choose what to look for. Nothing is removed until you review the list and confirm.'} footer={footer}>
    {error && <Notice tone="danger" title="That didn't work">{error}</Notice>}
    {jobId ? <JobProgress jobId={jobId} title="Cleanup" renderResult={j => <CleanupResult result={j.result} />} />
      : !plan ? <ul className="sv-options">{cleanupOptions.map(o => <li key={o.key}>
        <Checkbox label={o.label} checked={opts[o.key]} onChange={v => setOpts(x => ({ ...x, [o.key]: v }))} />
        <div><strong>{o.label}</strong><span>{o.text}</span></div>
      </li>)}<li className="sv-locked"><LockClosedIcon /><div><strong>Volumes</strong><span>Never included. They hold persistent data.</span></div></li></ul>
        : <>
          {!plan.items.length ? <Notice tone="success" title="Nothing to clean up">No objects match these options.</Notice>
            : <p className="sv-step-intro">{plan.items.length} object(s), about {bytes(plan.bytes)} reclaimable. Running workloads aren't affected. {plan.volumes} volume(s) stay untouched.</p>}
          {groups.map(([kind, list]) => <details key={kind} open={list.length <= 8} className="sv-plan">
            <summary>{{ container: 'Containers', image: 'Images', network: 'Networks', buildcache: 'Build cache' }[kind]} · {list.length}</summary>
            <ul>{list.map(i => <li key={i.id}><span className="mono">{i.name}</span><span className="muted">{i.reason}</span><span>{i.size > 0 ? bytes(i.size) : ''}</span></li>)}</ul>
          </details>)}
          {plan.protected.length > 0 && <details className="sv-plan"><summary>Kept on purpose · {plan.protected.length}</summary>
            <ul>{plan.protected.map(i => <li key={i.kind + i.id}><span className="mono">{i.name}</span><span className="muted">{i.reason}</span><span /></li>)}</ul></details>}
          {plan.items.length > 0 && <><p className="confirm-hint">Type <code>clean up</code> to confirm.</p><input className="sv-confirm-input" value={typed} onChange={e => setTyped(e.target.value)} aria-label="Type clean up to confirm" autoComplete="off" /></>}
        </>}
  </Dialog>;
}

function CleanupResult({ result }) {
  if (!result) return null;
  return <div className="sv-cleanup-result">
    <p><strong>{result.removed.length}</strong> removed{result.reclaimed > 0 ? ` · ${bytes(result.reclaimed)} reclaimed` : ''}{result.failed.length ? ` · ${result.failed.length} failed` : ''}{result.skipped.length ? ` · ${result.skipped.length} skipped` : ''}</p>
    {result.failed.length > 0 && <ul>{result.failed.map(i => <li key={i.id}><span className="mono">{i.name}</span>: {i.reason}</li>)}</ul>}
    {result.skipped.length > 0 && <ul className="muted">{result.skipped.map(i => <li key={i.id}><span className="mono">{i.name}</span>: {i.reason}</li>)}</ul>}
  </div>;
}

/* ------------------------------------ Storage ----------------------------------- */

export function Storage({ server, onNavigate, onOpenResource }) {
  const { data, error } = useQuery(() => api.serverStorage(server.id), [server.id], { pollMs: 30000 });
  if (error && !data) return <Unavailable server={server} error={error} what="Storage" onNavigate={onNavigate} />;
  const vols = data?.volumes ?? [];
  const measured = vols.filter(v => v.size >= 0);
  return <>
    <div className="rp-intro"><p>Persistent storage on {server.name}. Volumes belong to this host: they don't move with a resource or follow it to another server, and removing the server from Ignition leaves them in place. Configure mounts on each resource's Storage page.</p></div>
    {data?.missing?.length > 0 && <Notice tone="info" title={`${data.missing.length} configured volume(s) don't exist yet`}>Docker creates them on the next deployment: {data.missing.map(m => `${m.resource} (${m.mountPath})`).join(', ')}.</Notice>}
    <section className="ov-card">
      <header><h2>Volumes</h2><span className="rp-row-meta">{measured.length ? `${bytes(measured.reduce((a, v) => a + v.size, 0))} measured` : ''}</span></header>
      <Table className="sv-table-wrap sv-flat" columns={[{ label: 'Volume' }, { label: 'Owner' }, { label: 'Mounted at' }, { label: 'Access' }, { label: 'Driver' }, { label: 'Size', className: 'num' }, ...(data?.hostPaths ? [{ label: 'Host path' }] : []), { label: 'Backups' }]}>
        {vols.map(v => { const mounts = data.mounts[v.name] ?? []; return <tr key={v.name}>
          <td className="mono sv-ellipsis" title={v.name}>{v.name}</td>
          <td>{v.owner.kind === 'resource' ? <button type="button" className="sv-link" onClick={() => onOpenResource(v.owner.resource, 'Storage')}>{v.owner.resource.name}<span>Storage settings</span></button>
            : v.owner.kind === 'unmanaged' ? <span className="rp-chip warning" title="Unknown purpose: treat as important data.">Unknown owner</span> : <span className="muted">Ignition</span>}</td>
          <td className="mono sv-ellipsis">{mounts.map(m => `${m.container}:${m.destination}`).join(', ') || v.mountPath || <span className="muted">Not mounted</span>}</td>
          <td className="muted">{mounts.length ? (mounts.every(m => m.readOnly) ? 'Read-only' : 'Read-write') : '—'}</td>
          <td className="muted">{v.driver}</td>
          <td className="num" title={v.size < 0 ? 'Docker hasn\'t measured this volume yet.' : undefined}>{v.size < 0 ? 'Not measured' : bytes(v.size)}</td>
          {data.hostPaths && <td className="mono sv-ellipsis" title={v.mountpoint}>{v.mountpoint}</td>}
          <td className="muted" title={data.backups}>Not tracked</td>
        </tr>; })}
      </Table>
      {!vols.length && <p className="ov-empty">{data ? 'No volumes.' : 'Loading…'}</p>}
    </section>
    <section className="ov-card">
      <header><h2>Bind mounts</h2><span className="rp-row-meta">Host directories mounted into containers</span></header>
      {(data?.binds ?? []).length ? <Table className="sv-table-wrap sv-flat" columns={[{ label: 'Container' }, { label: 'Owner' }, { label: 'Host path' }, { label: 'Container path' }, { label: 'Access' }]}>
        {data.binds.map((b, i) => <tr key={i}><td>{b.container}</td><td><OwnerCell owner={b.owner} onOpenResource={onOpenResource} /></td><td className="mono sv-ellipsis">{b.source}</td><td className="mono">{b.destination}</td><td className="muted">{b.readOnly ? 'Read-only' : 'Read-write'}</td></tr>)}
      </Table> : <p className="ov-empty">{data ? 'No bind mounts.' : 'Loading…'}</p>}
    </section>
    <p className="monitor-footnote">{data?.backups} Sizes are Docker's own measurements; “Not measured” means Docker hasn't calculated it, not that the volume is empty.</p>
  </>;
}

/* ---------------------------------- Networking ---------------------------------- */

const exposureChip = { public: ['warning', 'All interfaces'], loopback: ['success', 'This host only'], specific: ['', 'One address'] };

export function Networking({ server, onNavigate, onOpenResource, setFeedback }) {
  const { user } = useSession();
  const { data, error } = useQuery(() => api.serverNetworking(server.id), [server.id], { pollMs: 30000 });
  if (error && !data) return <Unavailable server={server} error={error} what="Networking" onNavigate={onNavigate} />;
  async function check() {
    try { await api.runServerDiagnostics(server.id, ['ports', 'dns', 'tcp']); setFeedback({ tone: 'success', title: 'Network checks started', text: 'Results appear on the Diagnostics page.' }); onNavigate('Diagnostics'); }
    catch (e) { setFeedback({ tone: 'danger', title: 'Couldn\'t start checks', text: e.message }); }
  }
  const publicCount = (data?.ports ?? []).filter(p => p.exposure === 'public').length;
  return <>
    <div className="rp-intro">
      <p>What's published and routed on {server.name}. Docker networks, host ports, the domain proxy and the host firewall are separate layers; Ignition manages the first three for its resources, and none of the firewall.</p>
      <div className="rp-intro-actions">{isDeveloper(user) && <Button icon={BeakerIcon} onClick={check}>Check ports, DNS and TCP</Button>}</div>
    </div>
    <section className="ov-card">
      <header><h2>Published host ports</h2><span className="rp-row-meta">{publicCount ? `${publicCount} on all interfaces` : ''}</span></header>
      {(data?.ports ?? []).length ? <Table className="sv-table-wrap sv-flat" columns={[{ label: 'Host port' }, { label: 'Binding' }, { label: 'Exposure' }, { label: 'Container' }, { label: 'Owner' }]}>
        {data.ports.map((p, i) => { const [tone, label] = exposureChip[p.exposure]; return <tr key={i}>
          <td className="mono">{p.hostPort}/{p.protocol} → {p.containerPort}</td>
          <td className="mono">{p.hostIp || '0.0.0.0'}</td>
          <td><span className={`rp-chip ${tone}`}>{label}</span></td>
          <td>{p.container}</td>
          <td><OwnerCell owner={p.owner} onOpenResource={onOpenResource} /></td>
        </tr>; })}
      </Table> : <p className="ov-empty">{data ? 'No container publishes a host port.' : 'Loading…'}</p>}
    </section>
    <div className="ov-columns">
      <section className="ov-card">
        <header><h2>Docker networks</h2><button type="button" className="ov-link" onClick={() => onNavigate('Docker')}>Docker</button></header>
        <ul className="sv-mini-list">{(data?.networks ?? []).map(n => <li key={n.name}><strong>{n.name}</strong><span>{n.driver} · {n.subnets?.join(', ') || 'no subnet'} · {n.containers} container(s){n.internal ? ' · internal' : ''}{n.ignition ? ' · Ignition' : ''}</span></li>)}</ul>
      </section>
      <div className="ov-stack">
        <section className="ov-card">
          <header><h2>Domains and proxy</h2></header>
          <p className="ov-empty">{data?.proxy?.description}{data?.proxy?.httpPort ? ` Listening on ${data.proxy.httpPort} and ${data.proxy.httpsPort}.` : ''}</p>
          {(data?.domains ?? []).length > 0 && <ul className="sv-mini-list">{data.domains.map(d => <li key={d.hostname}><strong>{d.https ? 'https' : 'http'}://{d.hostname}</strong><span>{d.resourceName} · {d.status}</span></li>)}</ul>}
        </section>
        <section className="ov-card">
          <header><h2>Host interfaces and routes</h2></header>
          <p className="ov-empty">{data?.hostInterfaces?.reason}</p>
        </section>
        <section className="ov-card">
          <header><h2>Firewall</h2></header>
          <p className="ov-empty">{data?.firewall?.reason}</p>
        </section>
      </div>
    </div>
  </>;
}

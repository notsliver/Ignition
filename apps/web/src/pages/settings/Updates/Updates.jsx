import { useEffect, useState } from 'react';
import { ArrowPathIcon, ArrowRightIcon, ArrowUpIcon, CheckIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, Notice, SegmentedControl, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { activeNotices } from './notices';
import './Updates.css';

// Updates come from the published image on Docker Hub; the API reads its tags.
const channels = [{ value: 'stable', label: 'Stable' }, { value: 'beta', label: 'Beta' }, { value: 'edge', label: 'Edge' }];
const channelHelp = {
  stable: 'Tested releases, recommended for production.',
  beta: 'Pre-releases with new features before they\'re stable. May contain bugs.',
  edge: 'Built from the main branch on every change. For testing only.',
};
const AUTO_CHECK_MS = 6 * 60 * 60 * 1000;

const v = s => (s && /^\d+\.\d+/.test(s) ? `v${s}` : s);
const date = s => (s ? new Date(s).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : '');

export function Updates() {
  const { data, refetch, setData } = useQuery(() => api.updates(), []);
  const [checking, setChecking] = useState(false);
  const [starting, setStarting] = useState(false);
  const [startError, setStartError] = useState('');
  const [dismissed, setDismissed] = useState([]);
  const [logsOpen, setLogsOpen] = useState(false);
  const [imageOpen, setImageOpen] = useState(false);
  const [confirm, setConfirm] = useState(false);

  const run = data?.run;
  const updating = starting || run?.state === 'running';

  // While the updater restarts IGNITION the API goes away for a moment; keep asking until it reports back.
  useEffect(() => {
    if (!updating) return;
    const t = setInterval(() => api.updates().then(setData).catch(() => {}), 3000);
    return () => clearInterval(t);
  }, [updating]); // eslint-disable-line react-hooks/exhaustive-deps

  // Check on open when the last check is old, so "Update now" is there without an extra click.
  useEffect(() => {
    if (!data || updating || checking) return;
    const last = data.lastCheck?.checkedAt ? new Date(data.lastCheck.checkedAt).getTime() : 0;
    if (Date.now() - last > AUTO_CHECK_MS || data.lastCheck?.channel !== data.config.channel) runCheck();
  }, [!!data]); // eslint-disable-line react-hooks/exhaustive-deps

  if (!data) return <div className="updates" />;
  const { installed, config, lastCheck } = data;
  const check = lastCheck?.channel === config.channel ? lastCheck : null;
  const latest = check?.latest;
  const available = check?.status === 'behind' && !!latest;
  const shown = installed.version === 'edge' && installed.commit !== 'unknown' ? `edge · ${installed.commit.slice(0, 7)}` : v(installed.version);

  async function runCheck() {
    setChecking(true);
    try {
      const res = await api.checkUpdates();
      setData(d => ({ ...d, ...res }));
    } catch (e) {
      setData(d => ({ ...d, lastCheck: { checkedAt: new Date().toISOString(), channel: d.config.channel, error: e.message } }));
    } finally {
      setChecking(false);
    }
  }

  async function setChannel(channel) {
    setData(d => ({ ...d, config: { ...d.config, channel } }));
    try {
      await api.putUpdates({ ...config, channel });
      await runCheck();
    } catch {
      refetch();
    }
  }

  async function update() {
    setConfirm(false);
    setStarting(true);
    setStartError('');
    try {
      const res = await api.applyUpdate();
      setData(d => ({ ...d, run: res.run }));
    } catch (e) {
      setStartError(e.message);
    } finally {
      setStarting(false);
    }
  }

  function dismiss(n) {
    setDismissed(ids => [...ids, n.id]);
    if (n.server) api.dismissUpdate().catch(() => {});
  }

  const notices = activeNotices({ ...data, check, startError, retry: runCheck, viewLogs: () => setLogsOpen(true) })
    .filter(n => !dismissed.includes(n.id));
  const target = latest?.version === 'edge' ? `edge${latest.commit ? ` (${latest.commit})` : ''}` : v(latest?.version);

  return <div className="updates">
    {notices.length > 0 && <div className="update-notices">
      {notices.map(n => <Notice key={n.id} tone={n.tone} title={n.title} action={n.action} onAction={n.onAction} onDismiss={() => dismiss(n)}>{n.text}</Notice>)}
    </div>}

    <section className="version-hero">
      <div className="version-copy">
        <div className="version-line">
          <span className="version-number">{shown}</span>
          {available
            ? <span className="version-pill available"><ArrowUpIcon />Update available</span>
            : check?.status === 'up-to-date' && <span className="version-pill current"><CheckIcon />Up to date</span>}
        </div>
        <span className="version-meta">
          {installed.builtAt && <>Built {date(installed.builtAt)}<span>·</span></>}
          {installed.fromSource ? 'Built from source' : <code>{installed.image || config.image}</code>}<span>·</span>
          {channels.find(c => c.value === config.channel)?.label} channel
        </span>
      </div>
      <div className="version-check">
        <Button icon={ArrowPathIcon} onClick={runCheck} disabled={checking || updating} className={cx(checking && 'is-busy')}>{checking ? 'Checking…' : 'Check for updates'}</Button>
        <span>Last checked {checking ? '…' : lastCheck?.checkedAt ? timeAgo(lastCheck.checkedAt).toLowerCase() : 'never'}</span>
      </div>
    </section>

    {(available || updating) && latest && <section className={cx('update-card', updating && 'is-updating')} aria-live="polite">
      <div className="update-copy">
        <strong>{updating ? `Updating to ${target}…` : `IGNITION ${target} is available`}</strong>
        <span>{updating
          ? 'IGNITION restarts automatically and this page reconnects. Your apps, databases and domains stay online.'
          : <>
            {latest.updated && <>Published {date(latest.updated)}<span>·</span></>}
            <code>{latest.image}</code>
            {latest.url && <><span>·</span><a className="update-link" href={latest.url} target="_blank" rel="noreferrer">{latest.version === 'edge' ? 'Changes' : 'Release notes'}<ArrowRightIcon /></a></>}
          </>}</span>
      </div>
      <Button variant="primary" icon={ArrowUpIcon} onClick={() => setConfirm(true)} disabled={updating}>{updating ? 'Updating…' : 'Update now'}</Button>
      {updating && <div className="update-progress" />}
    </section>}

    <section className="channel-row">
      <div><strong>Release channel</strong><span>{channelHelp[config.channel]}</span></div>
      <SegmentedControl label="Release channel" options={channels} value={config.channel} onChange={setChannel} />
    </section>

    {installed.localDevelopment && <section className="channel-row">
      <div><strong>Image</strong><span className="mono">{config.image} on Docker Hub</span></div>
      <Button onClick={() => setImageOpen(true)} disabled={updating}>Change</Button>
    </section>}

    <ImageDialog open={imageOpen} config={config} onClose={() => setImageOpen(false)}
      onSaved={c => { setData(d => ({ ...d, config: c })); setImageOpen(false); runCheck(); }} />

    <Dialog open={confirm} onClose={() => setConfirm(false)} title={`Update to ${target}?`}
      description="IGNITION pulls the new image and restarts its dashboard and worker. It's unavailable for about a minute; your apps, databases and domains keep running. Database changes apply automatically on start."
      footer={<><Button onClick={() => setConfirm(false)}>Cancel</Button><Button variant="primary" icon={ArrowUpIcon} onClick={update}>Update now</Button></>}>
      {installed.fromSource && <Notice tone="info" title="This instance was built from source">Updating switches it to the published image. To go back, remove IGNITION_IMAGE from .env and run the installer.</Notice>}
    </Dialog>

    <Dialog open={logsOpen} onClose={() => setLogsOpen(false)} title="Updater log"
      description={run ? `Update to ${v(run.version)} started ${timeAgo(run.startedAt)} by ${run.startedBy}.` : ''}
      footer={<Button onClick={() => setLogsOpen(false)}>Close</Button>}>
      <pre className="update-log">{run?.log?.length ? run.log.join('\n') : 'No output was kept.'}</pre>
    </Dialog>
  </div>;
}

function ImageDialog({ open, config, onClose, onSaved }) {
  const [image, setImage] = useState(config.image);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  useEffect(() => { if (open) { setImage(config.image); setError(''); } }, [open]); // eslint-disable-line react-hooks/exhaustive-deps

  async function save(e) {
    e.preventDefault();
    setSaving(true);
    try {
      onSaved((await api.putUpdates({ ...config, image })).config);
    } catch (err) {
      setError(err.fields?.image ?? err.message);
    } finally {
      setSaving(false);
    }
  }

  return <Dialog open={open} onClose={onClose} title="Update image" description="The Docker Hub repository IGNITION updates from. Only change this if you publish your own build."
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" type="submit" form="update-image" disabled={saving}>{saving ? 'Saving…' : 'Save'}</Button></>}>
    <form id="update-image" className="update-source" onSubmit={save}>
      <FormField label="Repository" htmlFor="image" error={error} hint="namespace/name, e.g. notsliver/ignition">
        <TextInput id="image" className="mono" spellCheck={false} value={image} onChange={e => setImage(e.target.value)} aria-invalid={!!error} />
      </FormField>
    </form>
  </Dialog>;
}

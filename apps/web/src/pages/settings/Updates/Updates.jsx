import { useEffect, useState } from 'react';
import { ArrowPathIcon, ArrowRightIcon, ArrowUpIcon, CheckIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, FormField, Notice, SegmentedControl, Select, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { activeNotices } from './notices';
import './Updates.css';

const channels = [{ value: 'stable', label: 'Stable' }, { value: 'prerelease', label: 'Prerelease' }];
const channelHelp = {
  stable: 'Tested releases, recommended for production.',
  prerelease: 'Early access to new features. May contain bugs.',
};

const v = s => (s && /^\d+\.\d+/.test(s) ? `v${s}` : s);
const date = s => (s ? new Date(s).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : '');

export function Updates() {
  const { data, refetch, setData } = useQuery(() => api.updates(), []);
  const [checking, setChecking] = useState(false);
  const [starting, setStarting] = useState(false);
  const [startError, setStartError] = useState('');
  const [dismissed, setDismissed] = useState([]);
  const [logsOpen, setLogsOpen] = useState(false);
  const [sourceOpen, setSourceOpen] = useState(false);

  const run = data?.run;
  const updating = starting || run?.state === 'running';

  // While the updater rebuilds Ignition the API goes away for a moment; keep asking until it reports back.
  useEffect(() => {
    if (!updating) return;
    const t = setInterval(() => api.updates().then(setData).catch(() => {}), 3000);
    return () => clearInterval(t);
  }, [updating]); // eslint-disable-line react-hooks/exhaustive-deps

  if (!data) return <div className="updates" />;
  const { installed, config, lastCheck } = data;
  const check = lastCheck?.channel === config.channel ? lastCheck : null;
  const latest = check?.latest;
  const available = check?.status === 'behind' && !!latest;

  async function runCheck() {
    setChecking(true);
    try {
      const res = await api.checkUpdates();
      setData(d => ({ ...d, ...res }));
    } catch (e) {
      setData(d => ({ ...d, lastCheck: { checkedAt: new Date().toISOString(), channel: config.channel, error: e.message } }));
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

  const notices = activeNotices({ ...data, check, startError, retry: runCheck, viewLogs: () => setLogsOpen(true), update })
    .filter(n => !dismissed.includes(n.id));

  return <div className="updates">
    {notices.length > 0 && <div className="update-notices">
      {notices.map(n => <Notice key={n.id} tone={n.tone} title={n.title} action={n.action} onAction={n.onAction} onDismiss={() => dismiss(n)}>{n.text}</Notice>)}
    </div>}

    <section className="version-hero">
      <div className="version-copy">
        <div className="version-line">
          <span className="version-number">{v(installed.version)}</span>
          {available
            ? <span className="version-pill available"><ArrowUpIcon />Update available</span>
            : check?.status === 'up-to-date' && <span className="version-pill current"><CheckIcon />Up to date</span>}
        </div>
        <span className="version-meta">
          {installed.builtAt && <>Built {date(installed.builtAt)}<span>·</span></>}
          {installed.commit && installed.commit !== 'unknown' && !installed.commit.startsWith(installed.version) && <><code>{installed.commit.slice(0, 7)}</code><span>·</span></>}
          {config.channel === 'stable' ? 'Stable' : 'Prerelease'} channel
        </span>
      </div>
      <div className="version-check">
        <Button icon={ArrowPathIcon} onClick={runCheck} disabled={checking || updating} className={cx(checking && 'is-busy')}>{checking ? 'Checking…' : 'Check for updates'}</Button>
        <span>Last checked {checking ? '…' : lastCheck?.checkedAt ? timeAgo(lastCheck.checkedAt).toLowerCase() : 'never'}</span>
      </div>
    </section>

    {(available || updating) && latest && <section className={cx('update-card', updating && 'is-updating')} aria-live="polite">
      <div className="update-copy">
        <strong>{updating ? `Updating to ${v(latest.version)}…` : `Ignition ${v(latest.version)} is available`}</strong>
        <span>{updating
          ? 'Ignition will restart automatically. Running apps stay online.'
          : <>
            {latest.kind === 'tag' ? 'Latest release' : `Latest commit on ${latest.ref}`}
            {check.behind > 0 && <><span>·</span>{check.behind} commit{check.behind === 1 ? '' : 's'} behind</>}
            {latest.url && <><span>·</span><a className="update-link" href={latest.url} target="_blank" rel="noreferrer">{latest.kind === 'tag' ? 'Release notes' : 'Changes'}<ArrowRightIcon /></a></>}
          </>}</span>
      </div>
      <Button variant="primary" onClick={update} disabled={updating}>{updating ? 'Updating…' : `Update to ${v(latest.version)}`}</Button>
      {updating && <div className="update-progress" />}
    </section>}

    <section className="channel-row">
      <div><strong>Release channel</strong><span>{channelHelp[config.channel]}</span></div>
      <SegmentedControl label="Release channel" options={channels} value={config.channel} onChange={setChannel} />
    </section>

    <section className="channel-row">
      <div><strong>Update source</strong><span className="mono">{config.repoUrl.replace(/^https:\/\//, '')}{config.channel === 'prerelease' && ` · ${config.branch}`}</span></div>
      <Button onClick={() => setSourceOpen(true)} disabled={updating}>Change</Button>
    </section>

    <SourceDialog open={sourceOpen} config={config} onClose={() => setSourceOpen(false)}
      onSaved={c => { setData(d => ({ ...d, config: c })); setSourceOpen(false); runCheck(); }} />

    <Dialog open={logsOpen} onClose={() => setLogsOpen(false)} title="Updater log"
      description={run ? `Update to ${v(run.version)} started ${timeAgo(run.startedAt)} by ${run.startedBy}.` : ''}
      footer={<Button onClick={() => setLogsOpen(false)}>Close</Button>}>
      <pre className="update-log">{run?.log?.length ? run.log.join('\n') : 'No output was kept.'}</pre>
    </Dialog>
  </div>;
}

function SourceDialog({ open, config, onClose, onSaved }) {
  const sources = useQuery(() => api.sources({ kind: 'git' }), [open], { enabled: open });
  const [form, setForm] = useState(config);
  const [errors, setErrors] = useState({});
  const [saving, setSaving] = useState(false);
  useEffect(() => { if (open) { setForm(config); setErrors({}); } }, [open]); // eslint-disable-line react-hooks/exhaustive-deps

  const options = [{ value: '', label: 'None (public repository)' },
    ...(sources.data?.sources ?? []).map(s => ({ value: s.id, label: s.name, description: s.account || s.status }))];
  const bind = key => ({ id: key, value: form[key], 'aria-invalid': !!errors[key], onChange: e => setForm(f => ({ ...f, [key]: e.target.value })) });

  async function save(e) {
    e.preventDefault();
    setSaving(true);
    try {
      onSaved((await api.putUpdates(form)).config);
    } catch (err) {
      setErrors(err.fields ?? { repoUrl: err.message });
    } finally {
      setSaving(false);
    }
  }

  return <Dialog open={open} onClose={onClose} title="Update source" description="Where Ignition looks for new versions."
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" type="submit" form="update-source" disabled={saving}>{saving ? 'Saving…' : 'Save'}</Button></>}>
    <form id="update-source" className="update-source" onSubmit={save}>
      <FormField label="Repository" htmlFor="repoUrl" error={errors.repoUrl}><TextInput className="mono" spellCheck={false} {...bind('repoUrl')} /></FormField>
      <FormField label="Branch" htmlFor="branch" error={errors.branch} hint="Followed on the Prerelease channel."><TextInput className="mono" spellCheck={false} {...bind('branch')} /></FormField>
      <FormField label="Credentials" htmlFor="sourceId" error={errors.sourceId} hint="For a private repository. Add one under Infrastructure › Sources.">
        <Select id="sourceId" label="Credentials" options={options} value={form.sourceId ?? ''} onChange={id => setForm(f => ({ ...f, sourceId: id }))} />
      </FormField>
    </form>
  </Dialog>;
}

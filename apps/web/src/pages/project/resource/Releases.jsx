import { useEffect, useState } from 'react';
import { ArrowRightIcon, ArrowUturnLeftIcon, ArrowUturnRightIcon, InformationCircleIcon } from '@heroicons/react/16/solid';
import { Button, Checkbox, CommitIcon, Dialog, Notice, Status, TextInput, cx } from '../../../ui';
import { api } from '../../../api/client';
import { timeAgo, useQuery } from '../../../api/hooks';
import { useSession, can } from '../../../app/AuthGate';
import { Empty, PageIntro } from './shared';
import './Operations.css';

/*
 * Releases are successful deployments. Rolling back runs that release's exact image again (nothing is
 * rebuilt or pulled), optionally with the variables it ran with. The switch is a normal deployment:
 * the old release keeps serving until the new one passes its checks.
 */

const TRIGGER = { manual: 'Manual', create: 'First deploy', redeploy: 'Redeploy', rollback: 'Rollback', retry: 'Retry', config: 'Config change', hook: 'Deploy hook', token: 'API token', automation: 'Automation' };
const STATUS = { successful: ['success', 'Done'], failed: ['danger', 'Failed'], cancelled: ['neutral', 'Cancelled'], queued: ['neutral', 'Queued'], building: ['progress', 'Building'], deploying: ['progress', 'Deploying'] };
const short = s => (s || '').slice(0, 7);
const names = list => list.length > 3 ? `${list.slice(0, 3).join(', ')} and ${list.length - 3} more` : list.join(', ');

function title(d) {
  if (d.reason) return d.reason;
  if (d.commit) return `Commit ${short(d.commit)}`;
  return `Deployment #${d.number}`;
}

export function RollbackHistory({ resource, onNavigate }) {
  const { user } = useSession();
  const canDeploy = can(user, 'deployments.create');
  const [busy, setBusy] = useState(false);
  const { data, error, refetch } = useQuery(() => api.releases(resource.id), [resource.id], { pollMs: busy ? 3000 : 15000 });
  const [target, setTarget] = useState(null);
  const [started, setStarted] = useState(null);
  const releases = data?.releases ?? [];
  const rollbacks = data?.rollbacks ?? [];
  const current = releases.find(r => r.current);
  const inFlight = rollbacks.some(d => ['queued', 'building', 'deploying'].includes(d.status));
  useEffect(() => { setBusy(inFlight); }, [inFlight]);

  if (error && !data) return <div className="rp-page"><Notice tone="danger" title="Couldn't load releases">{error.message} <button type="button" className="inline-link" onClick={refetch}>Retry</button></Notice></div>;
  if (!data) return <div className="rp-page"><p className="ops-muted">Loading…</p></div>;

  // Which release a rollback went to: the one whose image it ran.
  const releaseOf = d => releases.find(r => r.trigger !== 'rollback' && (r.imageId && r.imageId === d.imageRef || r.imageRef === d.imageRef || (r.imageId && r.imageId === d.imageId)));

  return <div className="rp-page ops-page">
    <PageIntro>Switch back to an earlier release. It runs that release's exact image again, so nothing is rebuilt, and the current release keeps serving until it passes its checks.</PageIntro>
    {started && <Notice tone="success" title={`Rolling back to #${started.to}`} action="Follow it" onAction={() => onNavigate?.('Deployments')} onDismiss={() => setStarted(null)}>
      Started as deployment #{started.number}{started.restoreEnv ? ', with the variables from that release' : ''}.
    </Notice>}

    <section>
      <h2 className="rp-heading">Releases</h2>
      {releases.length === 0
        ? <Empty icon={ArrowUturnLeftIcon} title="No releases yet">Every successful deployment shows up here, ready to roll back to.</Empty>
        : <div className="rp-card">
          {releases.map(r => {
            const forward = current && r.number > current.number;
            return <div key={r.id} className={cx('rp-row', r.current && 'current-release', !r.available && !r.current && 'ops-unavailable')}>
              <span className="ops-release-num">#{r.number}</span>
              <span className="rp-row-main">
                <strong>{title(r)}{r.current && <span className="rp-chip success">Live</span>}</strong>
                <span>
                  {r.commit && <><CommitIcon className="inline-icon" />{short(r.commit)} · </>}
                  {r.branch && `${r.branch} · `}{TRIGGER[r.trigger] ?? r.trigger} · {r.author.replace(/^token:/, 'token ')} · <time dateTime={r.finishedAt ?? r.createdAt} title={new Date(r.finishedAt ?? r.createdAt).toLocaleString()}>{timeAgo(r.finishedAt ?? r.createdAt)}</time>
                  {!r.envSnapshot && !r.current && ' · variables not kept'}
                </span>
              </span>
              {r.current ? <span className="rp-row-meta">Running now</span>
                : !r.available ? <span className="rp-row-meta" title={r.why}>Can't restore</span>
                  : canDeploy && <Button icon={forward ? ArrowUturnRightIcon : ArrowUturnLeftIcon} disabled={inFlight} onClick={() => setTarget(r)}>{forward ? 'Roll forward' : 'Roll back'}</Button>}
            </div>;
          })}
        </div>}
      {releases.some(r => !r.available && !r.current) && <p className="ops-note"><InformationCircleIcon />Releases marked "Can't restore" no longer have their image on this machine (hover for why). Deploy that commit from the source instead.</p>}
    </section>

    <section>
      <h2 className="rp-heading">Rollback log</h2>
      {rollbacks.length ? <ol className="rp-card rollback-log">
        {rollbacks.map(d => {
          const to = releaseOf(d);
          const [tone, label] = STATUS[d.status] ?? ['neutral', d.status];
          return <li key={d.id} className="rp-row">
            <span className="rp-row-main">
              <strong className="rollback-move">
                <code>{d.previousNumber ? `#${d.previousNumber}` : '—'}</code><ArrowRightIcon /><code>{to ? `#${to.number}` : short(d.imageRef.replace('sha256:', ''))}</code>
                <Status tone={tone}>{label}</Status>
                {d.restoreEnv && <span className="rp-chip">with variables</span>}
              </strong>
              <span>{d.status === 'failed' && d.error ? d.error : d.reason && !/^Rollback to #\d+$/.test(d.reason) ? d.reason : 'No reason given'}</span>
            </span>
            <span className="rp-row-meta">#{d.number} · {d.author} · {timeAgo(d.createdAt)}</span>
          </li>;
        })}
      </ol> : <p className="rp-row-meta">No rollbacks yet.</p>}
    </section>

    {target && <RollbackDialog resource={resource} target={target} current={current} onClose={() => setTarget(null)}
      onDone={(out, restoreEnv) => { setStarted({ to: target.number, number: out.number, restoreEnv }); setTarget(null); setBusy(true); refetch(); }} />}
  </div>;
}

function RollbackDialog({ resource, target, current, onClose, onDone }) {
  const diff = useQuery(() => api.envDiff(target.id), [target.id]);
  const [restoreEnv, setRestoreEnv] = useState(false);
  const [reason, setReason] = useState('');
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState('');
  const d = diff.data;
  const changes = d ? d.added.length + d.removed.length + d.changed.length : 0;
  const forward = current && target.number > current.number;

  async function confirm() {
    setPending(true);
    setFailure('');
    try {
      const out = await api.deploy(resource.id, { from: target.id, restoreEnv, reason: reason.trim() || `${forward ? 'Roll forward' : 'Rollback'} to #${target.number}` });
      const dep = await api.deployment(out.deploymentId).catch(() => null);
      onDone({ number: dep?.deployment?.number ?? '?' }, restoreEnv);
    } catch (err) {
      setFailure(err.message);
      setPending(false);
    }
  }

  return <Dialog open onClose={onClose} title={`${forward ? 'Roll forward' : 'Roll back'} to #${target.number}?`}
    description={`${resource.name} switches${current ? ` from #${current.number}` : ''} to #${target.number}'s image once it passes its checks. If it doesn't, the current release keeps running.`}
    footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" icon={forward ? ArrowUturnRightIcon : ArrowUturnLeftIcon} disabled={pending} onClick={confirm}>{pending ? 'Starting…' : forward ? 'Roll forward' : 'Roll back'}</Button></>}>
    <div className="ops-dialog-body">
      {failure && <Notice tone="danger" title="Couldn't start">{failure}</Notice>}
      {!d ? <p className="ops-muted">Comparing variables…</p>
        : !d.snapshot ? <p className="ops-muted">The variables from #{target.number} weren't kept (it's older than this feature), so it runs with the current ones.</p>
          : changes === 0 ? <p className="ops-muted">Its variables are the same as now.</p>
            : <div className="ops-env-choice">
              <label className="check-row"><Checkbox label="Restore variables" checked={restoreEnv} onChange={setRestoreEnv} />Also restore the variables from #{target.number}</label>
              <ul className="ops-env-diff">
                {d.changed.length > 0 && <li><span className="chg">changes</span>{names(d.changed)}</li>}
                {d.added.length > 0 && <li><span className="add">brings back</span>{names(d.added)}</li>}
                {d.removed.length > 0 && <li><span className="del">removes</span>{names(d.removed)}</li>}
              </ul>
              {!restoreEnv && <p className="ops-muted">Without this, #{target.number} runs with today's variables.</p>}
            </div>}
      <TextInput placeholder="Reason (optional), e.g. error spike after deploy" value={reason} onChange={e => setReason(e.target.value)} aria-label="Reason" maxLength={200} />
    </div>
  </Dialog>;
}

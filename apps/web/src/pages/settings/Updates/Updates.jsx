import { useEffect, useRef, useState } from 'react';
import { ArrowPathIcon, ArrowRightIcon, ArrowUpIcon, CheckIcon } from '@heroicons/react/16/solid';
import { Button, Notice, SegmentedControl, cx } from '../../../ui';
import { installed, releases } from './data';
import { notices } from './notices';
import './Updates.css';

const channels = [{ value: 'stable', label: 'Stable' }, { value: 'prerelease', label: 'Prerelease' }];
const channelHelp = {
  stable: 'Tested releases, recommended for production.',
  prerelease: 'Early access to new features. May contain bugs.',
};

export function Updates() {
  const [current, setCurrent] = useState(installed.version);
  const [channel, setChannel] = useState(installed.channel);
  const [checking, setChecking] = useState(false);
  const [checkedAt, setCheckedAt] = useState('5 minutes ago');
  const [updating, setUpdating] = useState(false);
  const [dismissed, setDismissed] = useState([]);
  const timer = useRef();
  useEffect(() => () => clearTimeout(timer.current), []);

  const latest = releases[channel];
  const available = latest.version !== current;

  function check() {
    setChecking(true);
    timer.current = setTimeout(() => { setChecking(false); setCheckedAt('just now'); }, 1200);
  }

  function update() {
    setUpdating(true);
    timer.current = setTimeout(() => { setCurrent(latest.version); setUpdating(false); }, 2400);
  }

  const visibleNotices = notices.filter(n => n.enabled && !dismissed.includes(n.id));

  return <div className="updates">
    {visibleNotices.length > 0 && <div className="update-notices">
      {visibleNotices.map(n => <Notice key={n.id} tone={n.tone} title={n.title} action={n.action} onDismiss={() => setDismissed(ids => [...ids, n.id])}>{n.text}</Notice>)}
    </div>}

    <section className="version-hero">
      <div className="version-copy">
        <div className="version-line">
          <span className="version-number">v{current}</span>
          {available
            ? <span className="version-pill available"><ArrowUpIcon />Update available</span>
            : <span className="version-pill current"><CheckIcon />Up to date</span>}
        </div>
        <span className="version-meta">
          {current === installed.version ? <>Released {installed.released}<span>·</span><code>{installed.commit}</code></> : <>Updated just now</>}
          <span>·</span>{channel === 'stable' ? 'Stable' : 'Prerelease'} channel
        </span>
      </div>
      <div className="version-check">
        <Button icon={ArrowPathIcon} onClick={check} disabled={checking || updating} className={cx(checking && 'is-busy')}>{checking ? 'Checking…' : 'Check for updates'}</Button>
        <span>Last checked {checking ? '…' : checkedAt}</span>
      </div>
    </section>

    {available && <section className={cx('update-card', updating && 'is-updating')} aria-live="polite">
      <div className="update-copy">
        <strong>{updating ? `Updating to v${latest.version}…` : `Ignition v${latest.version} is available`}</strong>
        <span>{updating ? 'Ignition will restart automatically. Running apps stay online.' : <>Released {latest.released}<span>·</span><button className="update-link">Release notes<ArrowRightIcon /></button></>}</span>
      </div>
      <Button variant="primary" onClick={update} disabled={updating}>{updating ? 'Updating…' : `Update to v${latest.version}`}</Button>
      {updating && <div className="update-progress" />}
    </section>}

    <section className="channel-row">
      <div><strong>Release channel</strong><span>{channelHelp[channel]}</span></div>
      <SegmentedControl label="Release channel" options={channels} value={channel} onChange={setChannel} />
    </section>
  </div>;
}

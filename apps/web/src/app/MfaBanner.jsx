import { useState } from 'react';
import { Button, cx } from '../ui';
import { useSession } from './AuthGate';
import './MfaBanner.css';

/*
 * The friendly nudge shown to people who still need two-factor authentication when an admin
 * enforces it. Before the deadline it counts down and can be snoozed for a day; on the last day
 * (or without a deadline) it stays. "Set it up" opens the account dialog.
 */

const SNOOZE_KEY = 'ignition:mfa-banner:snoozed-until';
const DAY = 24 * 60 * 60 * 1000;

export const openAccount = () => dispatchEvent(new Event('ignition:open-account'));

function snoozedUntil() {
  try { return Number(localStorage.getItem(SNOOZE_KEY)) || 0; } catch { return 0; }
}

export function MfaBanner() {
  const { user } = useSession();
  const [snoozed, setSnoozed] = useState(snoozedUntil);
  const n = user.mfaNotice;
  if (!n) return null;
  const deadline = n.deadlineAt ? new Date(n.deadlineAt) : null;
  const lastDay = deadline && deadline - Date.now() <= DAY;
  if (snoozed > Date.now() && !lastDay && n.grace) return null;
  function snooze() {
    const until = Date.now() + DAY;
    try { localStorage.setItem(SNOOZE_KEY, String(until)); } catch { /* private mode */ }
    setSnoozed(until);
  }
  return <MfaNoticeCard notice={n} name={user.name} onSnooze={snooze} onSetUp={openAccount} />;
}

/** The announcement itself; Settings shows it as a preview with no actions wired. */
export function MfaNoticeCard({ notice: n, name, onSnooze, onSetUp, preview }) {
  const deadline = n.deadlineAt ? new Date(n.deadlineAt) : null;
  const daysLeft = deadline ? Math.max(0, Math.ceil((deadline - Date.now()) / DAY)) : null;
  const lastDay = daysLeft != null && daysLeft <= 1;
  const first = (name || '').split(' ')[0];
  const when = deadline?.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric', timeZone: 'UTC' });
  const countdown = daysLeft == null ? null : daysLeft === 0 ? 'last day' : daysLeft === 1 ? '1 day left' : `${daysLeft} days left`;
  const after = n.action === 'suspend'
    ? 'accounts without it are paused until an admin switches them back on.'
    : 'you\'ll be asked to set it up before you can sign in again.';
  const tone = !n.grace || lastDay ? 'urgent' : daysLeft <= 3 ? 'soon' : 'calm';

  return <aside className={cx('mfa-banner', tone, preview && 'preview')} role={preview ? undefined : 'status'} aria-live={preview ? undefined : 'polite'}>
    <div className="mfa-banner-text">
      <strong>
        {n.grace ? <>{first ? `Hey ${first}, ` : ''}your account wants a second lock</> : 'Two-factor authentication is required now'}
        {countdown && n.grace && <span className="mfa-banner-count">{countdown}</span>}
      </strong>
      <p>
        {n.grace
          ? <>Please set up two-factor authentication by <b>{when}</b>. It takes about a minute with an authenticator app. After that, {after}</>
          : <>Your admins turned it on for everyone. Set it up now to keep using {n.action === 'suspend' ? 'your account' : 'Ignition without interruption'}.</>}
      </p>
      {n.message && <blockquote>“{n.message}” <cite>— your admins</cite></blockquote>}
    </div>
    <div className="mfa-banner-actions">
      <Button variant="primary" onClick={onSetUp} tabIndex={preview ? -1 : undefined}>Set it up</Button>
      {n.grace && !lastDay && <Button className="mfa-banner-later" onClick={onSnooze} tabIndex={preview ? -1 : undefined}>Remind me tomorrow</Button>}
    </div>
  </aside>;
}

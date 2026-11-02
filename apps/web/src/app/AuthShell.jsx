import { LockClosedIcon } from '@heroicons/react/16/solid';
import { Logo, cx } from '../ui';
import './Onboarding.css';
import './AuthGate.css';

/*
 * The frame for everything before the dashboard (sign-in, two-factor, invitations, choosing a team,
 * maintenance): the same full app panel onboarding uses, with the sidebar closed.
 */
export function AuthShell({ title = 'Ignition', wide, children, foot = true }) {
  return <div className="ob-app auth-shell">
    <header className="ob-title">{title}</header>
    <main className="ob-container">
      <div className="ob-body">
        <section className={cx('ob-step', 'auth-step-col', wide && 'wide')}>
          <Logo className="ob-hero-logo" />
          {children}
          {foot && <p className="auth-foot"><LockClosedIcon />Self-hosted. Your apps and data stay on this machine.</p>}
        </section>
      </div>
    </main>
  </div>;
}

import { useEffect, useState } from 'react';
import { ArrowPathIcon, ArrowRightStartOnRectangleIcon, InformationCircleIcon, WrenchScrewdriverIcon } from '@heroicons/react/16/solid';
import { Button } from '../ui';
import { api } from '../api/client';
import { useSession } from './AuthGate';
import { useInstanceName } from './instance';
import { AuthShell } from './AuthShell';
import './Maintenance.css';

const DEFAULT_MESSAGE = 'This instance is down for maintenance. Please try again later.';

/*
 * Maintenance mode (Instance administration › General). Everyone but platform administrators gets
 * this screen instead of the dashboard, and the API refuses their requests; administrators keep
 * working with a reminder that it's on. Workloads keep running either way.
 */
export function MaintenanceGate({ children }) {
  const session = useSession();
  const name = useInstanceName();
  const [state, setState] = useState(null);
  const [checking, setChecking] = useState(false);
  const admin = ['owner', 'admin'].includes(session.user.role);

  const check = () => {
    setChecking(true);
    return api.system().then(s => setState({ on: !!s.maintenance, message: s.maintenanceMessage })).catch(() => setState(s => s ?? { on: false }))
      .finally(() => setChecking(false));
  };
  useEffect(() => {
    check();
    const onChange = e => { if ('maintenance' in (e.detail ?? {})) setState({ on: !!e.detail.maintenance, message: e.detail.maintenanceMessage }); };
    window.addEventListener('ignition:instance', onChange);
    window.addEventListener('ignition:maintenance', onChange);
    return () => { window.removeEventListener('ignition:instance', onChange); window.removeEventListener('ignition:maintenance', onChange); };
  }, []);

  // While closed, look again every 30 seconds so people get in as soon as it's over.
  useEffect(() => {
    if (!state?.on || admin) return;
    const t = setInterval(() => api.system().then(s => setState({ on: !!s.maintenance, message: s.maintenanceMessage })).catch(() => {}), 30000);
    return () => clearInterval(t);
  }, [state?.on, admin]);

  if (!state) return null;
  if (!state.on) return children;
  if (admin) return <>
    {children}
    <a className="maintenance-pill" href="/admin/general" title="Instance administration › General">
      <WrenchScrewdriverIcon aria-hidden="true" />
      <span>Maintenance mode is on. Only administrators can use {name}.</span>
    </a>
  </>;
  return <AuthShell title={name} foot={false}>
    <div className="maintenance-step" role="status">
      <h1>We'll be right back.</h1>
      <p className="ob-lead">{name} is down for maintenance. You're signed in as {session.user.email}.</p>
      <div className="maintenance-message-card">
        <span className="maintenance-icon"><WrenchScrewdriverIcon aria-hidden="true" /></span>
        <span><strong>Message from the administrators</strong><p className="maintenance-message">{state.message || DEFAULT_MESSAGE}</p></span>
      </div>
      <div className="ob-note"><InformationCircleIcon /><span><strong>Your services are fine.</strong> Sites, databases and game servers keep running. Only the dashboard is closed; this page checks again on its own every 30 seconds.</span></div>
      <footer className="ob-actions">
        <Button icon={ArrowRightStartOnRectangleIcon} onClick={session.signOut}>Sign out</Button>
        <span className="ob-spacer" />
        <Button variant="primary" icon={ArrowPathIcon} disabled={checking} onClick={check}>{checking ? 'Checking…' : 'Check again'}</Button>
      </footer>
    </div>
  </AuthShell>;
}

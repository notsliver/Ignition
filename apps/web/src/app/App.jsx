import { useState } from 'react';
import { Bars3Icon, ViewColumnsIcon } from '@heroicons/react/20/solid';
import { Badge, IconButton, cx } from '../ui';
import { ComingSoon } from '../pages/ComingSoon';
import { Dashboard } from '../pages/Dashboard/Dashboard';
import { Monitor } from '../pages/Monitor/Monitor';
import { Updates } from '../pages/settings/Updates/Updates';
import { AppSidebar } from './AppSidebar';
import './App.css';

const pages = { Dashboard: <Dashboard />, Monitor: <Monitor />, Updates: <Updates /> };

export function App() {
  const [active, setActive] = useState('Dashboard');
  const [hidden, setHidden] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const navigate = title => { setActive(title); setMobileOpen(false); };

  return <div className={cx('app', hidden && 'sidebar-hidden', mobileOpen && 'mobile-open')}>
    {mobileOpen && <button className="backdrop" aria-label="Close navigation" onClick={() => setMobileOpen(false)} />}
    <AppSidebar active={active} onNavigate={navigate} onHide={() => setHidden(true)} />
    <main className="container">
      <header className="page-header">
        <IconButton className={cx('show-sidebar', hidden && 'visible')} icon={hidden ? ViewColumnsIcon : Bars3Icon} label="Show navigation" onClick={() => { setHidden(false); setMobileOpen(true); }} />
        <h1>{active}</h1><Badge>Preview</Badge>
      </header>
      {pages[active] ?? <ComingSoon title={active} />}
    </main>
  </div>;
}

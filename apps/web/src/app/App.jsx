import { useRef, useState } from 'react';
import { Bars3Icon, ViewColumnsIcon } from '@heroicons/react/20/solid';
import { Badge, IconButton, cx } from '../ui';
import { ComingSoon } from '../pages/ComingSoon';
import { Dashboard } from '../pages/Dashboard/Dashboard';
import { Monitor } from '../pages/Monitor/Monitor';
import { Projects } from '../pages/Projects/Projects';
import { General } from '../pages/settings/General/General';
import { Security } from '../pages/settings/Security/Security';
import { Updates } from '../pages/settings/Updates/Updates';
import { useSidebarWidth, SIDEBAR_MAX, SIDEBAR_MIN } from '../hooks/useSidebarWidth';
import { AppSidebar } from './AppSidebar';
import './App.css';

const pages = { Dashboard: <Dashboard />, Monitor: <Monitor />, General: <General />, 'Authentication & Security': <Security />, Updates: <Updates /> };

export function App() {
  const [active, setActive] = useState('Dashboard');
  const [hidden, setHidden] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const [project, setProject] = useState(null);
  const appRef = useRef(null);
  const sidebar = useSidebarWidth(appRef);
  const navigate = title => { setActive(title); setMobileOpen(false); };
  const [environment, setEnvironment] = useState(null);
  const openProject = p => { setProject(p); setEnvironment(p.environments[0]); navigate('Resources'); };
  const closeProject = () => { setProject(null); navigate('Projects'); };
  const page = !project && active === 'Projects' ? <Projects onOpen={openProject} /> : !project && pages[active];

  return <div ref={appRef} className={cx('app', hidden && 'sidebar-hidden', mobileOpen && 'mobile-open', sidebar.resizing && 'resizing')}>
    {mobileOpen && <button className="backdrop" aria-label="Close navigation" onClick={() => setMobileOpen(false)} />}
    <AppSidebar active={active} project={project} environment={environment} onEnvironmentChange={setEnvironment} onNavigate={navigate} onCloseProject={closeProject} onHide={() => setHidden(true)} />
    <div className="sidebar-resizer" role="separator" aria-orientation="vertical" aria-label="Resize sidebar" aria-valuemin={SIDEBAR_MIN} aria-valuemax={SIDEBAR_MAX} aria-valuenow={sidebar.width} tabIndex={0} title="Drag to resize · double-click to reset"
      onPointerDown={sidebar.startResize} onKeyDown={sidebar.onKeyDown} onDoubleClick={sidebar.reset} />
    <main className="container">
      <header className="page-header">
        <IconButton className={cx('show-sidebar', hidden && 'visible')} icon={hidden ? ViewColumnsIcon : Bars3Icon} label="Show navigation" onClick={() => { setHidden(false); setMobileOpen(true); }} />
        {project && <><button className="page-crumb" onClick={closeProject}>{project.name}</button><span className="page-crumb-sep">/</span></>}
        <h1>{active}</h1>{project ? <Badge className={cx('env-badge', environment === 'Production' && 'production')}>{environment}</Badge> : <Badge>Preview</Badge>}
      </header>
      {page || <ComingSoon title={active} />}
    </main>
  </div>;
}

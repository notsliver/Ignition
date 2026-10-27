import { useEffect, useRef, useState } from 'react';
import { Bars3Icon, ViewColumnsIcon } from '@heroicons/react/20/solid';
import { Badge, IconButton, Notice, cx } from '../ui';
import { ComingSoon } from '../pages/ComingSoon';
import { Dashboard } from '../pages/Dashboard/Dashboard';
import { Monitor } from '../pages/Monitor/Monitor';
import { Sources } from '../pages/Sources/Sources';
import { Projects } from '../pages/Projects/Projects';
import { Resources } from '../pages/project/Resources/Resources';
import { Deployments } from '../pages/project/Deployments/Deployments';
import { ResourceDetail } from '../pages/project/ResourceDetail/ResourceDetail';
import * as ResourcePages from '../pages/project/resource';
import { General } from '../pages/settings/General/General';
import { Security } from '../pages/settings/Security/Security';
import { Updates } from '../pages/settings/Updates/Updates';
import { Users } from '../pages/settings/Users/Users';
import { AuditLog } from '../pages/settings/Audit/AuditLog';
import { Servers } from '../pages/servers/Servers';
import { ServerWorkspace } from '../pages/servers/Workspace';
import { useInstanceName } from './instance';
import { useSidebarWidth, SIDEBAR_MAX, SIDEBAR_MIN } from '../hooks/useSidebarWidth';
import { AppSidebar } from './AppSidebar';
import { confirmLeave } from './unsaved';
import { fromUrl, slug, toUrl } from './routing';
import { api } from '../api/client';
import { PreviewBanner } from './Unsupported';
import './App.css';

const resourcePageComponents = {
  Logs: 'Logs', Metrics: 'Metrics', Terminal: 'Terminal',
  Source: 'Source', 'Build & Deploy': 'BuildDeploy', 'Environment Variables': 'EnvironmentVariables', Domains: 'Domains',
  Networking: 'Networking', Storage: 'Storage', 'Health Checks': 'HealthChecks', 'Resource Limits': 'ResourceLimits',
  'Rollback History': 'RollbackHistory', 'Scheduled Tasks': 'ScheduledTasks', 'Access & Secrets': 'AccessSecrets', 'Advanced Settings': 'AdvancedSettings', Automations: 'ResourceAutomations',
};
// Pages still running on sample data are labelled so nothing pretends to be live.
const preview = node => <><PreviewBanner />{node}</>;
const LIVE_RESOURCE_VIEWS = ['Overview', 'Deployments', 'Logs', 'Metrics', 'Environment Variables', 'Source', 'Domains', 'Networking', 'Build & Deploy', 'Resource Limits', 'Storage', 'Terminal',
  'Health Checks', 'Rollback History', 'Scheduled Tasks', 'Access & Secrets', 'Advanced Settings', 'Automations'];
// Settings pages backed by the API (the rest are still previews).
const LIVE_PAGES = ['General', 'Updates', 'Users & Permissions'];
const pages = { Dashboard: <Dashboard />, General: <General />, 'Authentication & Security': <Security />, Updates: <Updates />, 'Users & Permissions': <Users /> };

export function App() {
  const instanceName = useInstanceName();
  const [active, setActive] = useState('Dashboard');
  const [hidden, setHidden] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const [project, setProject] = useState(null);
  const appRef = useRef(null);
  const sidebar = useSidebarWidth(appRef);
  const [resource, setResourceState] = useState(null);
  const [resourceView, setResourceView] = useState('Overview');
  const setResource = (r, view = 'Overview') => { if (!confirmLeave()) return; setResourceState(r); setResourceView(view); setMobileOpen(false); };
  const showResourceView = view => { if (view === resourceView || !confirmLeave()) return; setResourceView(view); setMobileOpen(false); };
  const [server, setServerState] = useState(null);
  const [serverView, setServerView] = useState('Overview');
  const openServer = (s, view = 'Overview') => { if (!confirmLeave()) return; setServerState(s); setServerView(view); setActive('Servers'); setMobileOpen(false); };
  const showServerView = view => { if (view === serverView || !confirmLeave()) return; setServerView(view); setMobileOpen(false); };
  const navigate = title => { if (!confirmLeave()) return; setActive(title); setResourceState(null); setServerState(null); setMissing(''); setMobileOpen(false); };
  const [environment, setEnvironment] = useState(null);
  const openProject = p => { setProject(p); setEnvironment(p.environments[0]?.name ?? 'Production'); navigate('Resources'); };
  const environmentId = project?.environments.find(e => e.name === environment)?.id;
  const closeProject = () => { if (!confirmLeave()) return; setProject(null); setActive('Projects'); setResourceState(null); setMobileOpen(false); };
  // Keep the address bar in sync with what's on screen, and restore it on reload or Back/Forward.
  const [restoring, setRestoring] = useState(true);
  const [missing, setMissing] = useState('');
  async function applyUrl() {
    const target = fromUrl(location.pathname, location.search);
    setMissing('');
    try {
      const p = target.projectId ? (await api.project(target.projectId)).project : null;
      const r = target.resourceId ? (await api.resource(target.resourceId)).resource : null;
      const srv = target.serverId ? (await api.server(target.serverId)).server : null;
      if (r && r.projectId !== p?.id) throw new Error('mismatch');
      setProject(p);
      setEnvironment(p ? (p.environments.some(e => e.name === target.env) ? target.env : p.environments[0]?.name ?? 'Production') : null);
      setResourceState(r);
      setResourceView(target.resourceView ?? 'Overview');
      setServerState(srv);
      setServerView(target.serverView ?? 'Overview');
      setActive(target.active);
    } catch (e) {
      // A link to something deleted (or never visible to you): land on Projects with an explanation.
      setProject(null);
      setResourceState(null);
      setServerState(null);
      setActive(target.projectId ? 'Projects' : target.active);
      if (target.projectId) setMissing(e.status === 404 || e.message === 'mismatch' ? 'That project or resource no longer exists.' : e.message);
      if (target.serverId) setMissing(e.status === 404 ? 'That server no longer exists. It may have been removed.' : e.message);
    } finally {
      setRestoring(false);
    }
  }
  useEffect(() => {
    applyUrl();
    const onPop = () => applyUrl();
    window.addEventListener('popstate', onPop);
    return () => window.removeEventListener('popstate', onPop);
  }, []); // eslint-disable-line react-hooks/exhaustive-deps
  const url = toUrl({ active, project, resource, resourceView, environment, server, serverView });
  useEffect(() => {
    if (restoring || url === location.pathname + location.search) return;
    history.pushState(null, '', url);
  }, [url, restoring]);
  useEffect(() => {
    const where = resource ? `${resourceView === 'Overview' ? resource.name : `${resourceView} · ${resource.name}`}` : project ? `${active} · ${project.name}`
      : server ? (serverView === 'Overview' ? server.name : `${serverView} · ${server.name}`) : active;
    document.title = `${where} · ${instanceName}`;
  }, [active, project, resource, resourceView, server, serverView, instanceName]);

  const projectPages = project && { Resources: resource
    ? renderResourcePage()
    : <Resources project={project} environment={environment} environmentId={environmentId} onOpen={setResource} />, Deployments: preview(<Deployments project={project} />) };
  function renderResourcePage() {
    const props = { key: `${resource.id ?? resource.name}:${resourceView}`, project, environment, resource, onNavigate: showResourceView, onResourceChange: setResourceState, onDeleted: () => setResourceState(null) };
    if (resourceView === 'Overview') return <ResourceDetail {...props} />;
    if (resourceView === 'Deployments') return <ResourcePages.ResourceDeployments {...props} />;
    const Page = ResourcePages[resourcePageComponents[resourceView]];
    return Page ? (LIVE_RESOURCE_VIEWS.includes(resourceView) ? <Page {...props} /> : preview(<Page {...props} />)) : <ComingSoon title={resourceView} />;
  }
  // Opening a resource from Monitor goes through its URL, so project and resource load the same way as a reload.
  const openResourceFromAnywhere = (r, view = 'Overview') => {
    if (!confirmLeave()) return;
    history.pushState(null, '', `/projects/${r.projectId}/resources/${r.id}${view === 'Overview' ? '' : `/${slug(view)}`}`);
    applyUrl();
  };
  const page = project ? projectPages[active]
    : active === 'Projects' ? <Projects onOpen={openProject} />
        : active === 'Servers' ? (server
          ? <ServerWorkspace key={server.id} server={server} view={serverView} onNavigate={showServerView} onServerChange={setServerState} onRemoved={() => { setServerState(null); setServerView('Overview'); }} onOpenResource={openResourceFromAnywhere} />
          : <Servers onOpen={openServer} />)
        : active === 'Dashboard' ? <Dashboard onOpenResource={openResourceFromAnywhere} onOpenServer={openServer} onNavigate={navigate} />
        : active === 'Monitor' ? <Monitor onOpenResource={openResourceFromAnywhere} />
        : active === 'Sources' ? <Sources onOpenResource={openResourceFromAnywhere} />
          : active === 'Audit Log' ? <AuditLog onOpenResource={openResourceFromAnywhere} />
            : LIVE_PAGES.includes(active) ? pages[active] : pages[active] && preview(pages[active]);

  return <div ref={appRef} className={cx('app', hidden && 'sidebar-hidden', mobileOpen && 'mobile-open', sidebar.resizing && 'resizing')}>
    {mobileOpen && <button className="backdrop" aria-label="Close navigation" onClick={() => setMobileOpen(false)} />}
    <AppSidebar active={active} project={project} environment={environment} onEnvironmentChange={setEnvironment} onNavigate={navigate} onCloseProject={closeProject} resource={resource} resourceView={resourceView}
      onResourceNavigate={showResourceView} onCloseResource={() => setResource(null)} onHide={() => setHidden(true)}
      server={server} serverView={serverView} onServerNavigate={showServerView} onCloseServer={() => { if (confirmLeave()) { setServerState(null); setMobileOpen(false); } }} />
    <div className="sidebar-resizer" role="separator" aria-orientation="vertical" aria-label="Resize sidebar" aria-valuemin={SIDEBAR_MIN} aria-valuemax={SIDEBAR_MAX} aria-valuenow={sidebar.width} tabIndex={0} title="Drag to resize · double-click to reset"
      onPointerDown={sidebar.startResize} onKeyDown={sidebar.onKeyDown} onDoubleClick={sidebar.reset} />
    <main className="container">
      <header className="page-header">
        <IconButton className={cx('show-sidebar', hidden && 'visible')} icon={hidden ? ViewColumnsIcon : Bars3Icon} label="Show navigation" onClick={() => { setHidden(false); setMobileOpen(true); }} />
        {project && <><button className="page-crumb" onClick={closeProject}>{project.name}</button><span className="page-crumb-sep">/</span></>}
        {resource && <><button className="page-crumb" onClick={() => setResource(null)}>{active}</button><span className="page-crumb-sep">/</span></>}
        {resource && resourceView !== 'Overview' && <><button className="page-crumb" onClick={() => showResourceView('Overview')}>{resource.name}</button><span className="page-crumb-sep">/</span></>}
        {server && <><button className="page-crumb" onClick={() => { if (confirmLeave()) setServerState(null); }}>Servers</button><span className="page-crumb-sep">/</span></>}
        {server && serverView !== 'Overview' && <><button className="page-crumb" onClick={() => showServerView('Overview')}>{server.name}</button><span className="page-crumb-sep">/</span></>}
        <h1>{resource ? (resourceView === 'Overview' ? resource.name : resourceView) : server ? (serverView === 'Overview' ? server.name : serverView) : active}</h1>{project ? <Badge className={cx('env-badge', environment === 'Production' && 'production')}>{environment}</Badge> : null}
      </header>
      {missing && <div className="preview-banner"><Notice tone="warning" title="Link not found">{missing}</Notice></div>}
      {restoring ? null : page || <ComingSoon title={active} />}
    </main>
  </div>;
}

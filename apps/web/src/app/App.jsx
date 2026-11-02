import { useEffect, useRef, useState } from 'react';
import { Bars3Icon, ViewColumnsIcon } from '@heroicons/react/20/solid';
import { Badge, IconButton, Notice, cx } from '../ui';
import { ComingSoon } from '../pages/ComingSoon';
import { Domains as DomainsHub } from '../pages/network/Domains';
import { Networking } from '../pages/network/Networking';
import { ProjectDeployments } from '../pages/project/views/ProjectDeployments';
import { ProjectMonitoring } from '../pages/project/views/ProjectMonitoring';
import { ProjectLogs } from '../pages/project/views/ProjectLogs';
import { ProjectSettings } from '../pages/project/views/ProjectSettings';
import { Dashboard } from '../pages/Dashboard/Dashboard';
import { Monitor } from '../pages/Monitor/Monitor';
import { Sources } from '../pages/Sources/Sources';
import { Projects } from '../pages/Projects/Projects';
import { Resources } from '../pages/project/Resources/Resources';
import { ResourceDetail } from '../pages/project/ResourceDetail/ResourceDetail';
import * as ResourcePages from '../pages/project/resource';
import * as GamePages from '../pages/project/game';
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
import { MfaBanner } from './MfaBanner';
import { getPreferences } from './preferences';
import { useShortcuts } from './shortcuts';
import { Preferences } from '../pages/settings/Preferences/Preferences';
import { TeamMembers, TeamSettings } from '../pages/settings/Team/Team';
import { AdminTeams } from '../pages/admin/AdminTeams';
import { Limits } from '../pages/admin/Limits';
import { useSession } from './AuthGate';
import { adminPages, infrastructurePages } from './navigation';
import { useInfrastructureAdminOnly } from './instance';
import { TeamScopeNote } from '../pages/admin/AdminTeams';
import { confirmLeave } from './unsaved';
import { fromUrl, slug, toUrl } from './routing';
import { useQuery } from '../api/hooks';
import { Select } from '../ui';
import { api, teamPath } from '../api/client';
import { ResourceAccess, useTeam } from './Teams';
import { PreviewBanner } from './Unsupported';
import './App.css';

const resourcePageComponents = {
  Logs: 'Logs', Metrics: 'Metrics', Terminal: 'Terminal',
  Source: 'Source', 'Build & Deploy': 'BuildDeploy', 'Environment Variables': 'EnvironmentVariables', Domains: 'Domains',
  Networking: 'Networking', Storage: 'Storage', 'Health Checks': 'HealthChecks', 'Resource Limits': 'ResourceLimits',
  'Rollback History': 'RollbackHistory', 'Scheduled Tasks': 'ScheduledTasks', 'Access & Secrets': 'AccessSecrets', 'Advanced Settings': 'AdvancedSettings', Automations: 'ResourceAutomations', Code: 'FunctionCode', Files: 'Files', Backups: 'Backups',
};
// A game server's own pages; the rest of its sidebar is the usual resource pages.
const gamePageComponents = { Console: 'GameConsole', Players: 'GamePlayers', 'Mods & Plugins': 'GameContent', Worlds: 'GameWorlds', 'Server Settings': 'GameProperties', Schedules: 'GameSchedules' };
// Pages still running on sample data are labelled so nothing pretends to be live.
const preview = node => <><PreviewBanner />{node}</>;
const LIVE_RESOURCE_VIEWS = ['Overview', 'Deployments', 'Logs', 'Metrics', 'Environment Variables', 'Source', 'Domains', 'Networking', 'Build & Deploy', 'Resource Limits', 'Storage', 'Terminal',
  'Health Checks', 'Rollback History', 'Scheduled Tasks', 'Access & Secrets', 'Advanced Settings', 'Automations', 'Code', 'Files', 'Backups'];
// Settings pages backed by the API (the rest are still previews).
const LIVE_PAGES = ['Team', 'Team Members', 'Team Sources', 'General', 'Authentication & Security', 'Updates', 'Users', 'Teams', 'Limits', 'Preferences'];
const pages = { Team: <TeamSettings />, 'Team Members': <TeamMembers />, Dashboard: <Dashboard />, General: <General />, 'Authentication & Security': <Security />, Updates: <Updates />, Users: <Users />, Teams: <AdminTeams />, Limits: <Limits />, Preferences: <Preferences /> };

export function App() {
  const { team } = useTeam();
  const session = useSession();
  const infraAdmin = useInfrastructureAdminOnly();
  const instanceName = useInstanceName();
  const [active, setActive] = useState('Dashboard');
  const [hidden, setHidden] = useState(() => getPreferences().sidebar === 'collapsed');
  // Game server setup is a page of its own; the sidebar is tucked away while it's open and comes back after.
  const [gameSetup, setGameSetup] = useState(null);
  const hiddenBefore = useRef(false);
  const openGameSetup = id => { hiddenBefore.current = hidden; setHidden(true); setMobileOpen(false); setGameSetup(id); };
  const closeGameSetup = () => { setGameSetup(null); setHidden(hiddenBefore.current); };
  const [mobileOpen, setMobileOpen] = useState(false);
  const [project, setProjectData] = useState(null);
  const setProject = value => setProjectData(current => {const next=typeof value==='function' ? value(current) : value;return next && next.id===current?.id ? {...current,...next} : next;});
  const appRef = useRef(null);
  const sidebar = useSidebarWidth(appRef);
  const [resource, setResourceData] = useState(null);
  const setResourceState = value => setResourceData(current => {const next=typeof value==='function' ? value(current) : value;return next && next.id===current?.id ? {...current,...next} : next;});
  const [resourceView, setResourceView] = useState('Overview');
  const setResource = async (r, view = 'Overview') => { if (!confirmLeave()) return; try {setResourceState(r ? (await api.resource(r.id)).resource : null);setResourceView(view);setMobileOpen(false);}catch(e){if(e.name!=='AbortError')setMissing(e.message);} };
  const showResourceView = view => { if (view === resourceView || !confirmLeave()) return; setResourceView(view); setMobileOpen(false); };
  const [server, setServerState] = useState(null);
  const [serverView, setServerView] = useState('Overview');
  const openServer = (s, view = 'Overview') => { if (!confirmLeave()) return; setServerState(s); setServerView(view); setActive('Servers'); setMobileOpen(false); };
  const showServerView = view => { if (view === serverView || !confirmLeave()) return; setServerView(view); setMobileOpen(false); };
  const navigate = title => { if (!confirmLeave()) return; setActive(title); setResourceState(null); setServerState(null); setMissing(''); setMobileOpen(false); };
  useShortcuts({ navigate, closeProject: () => { setProject(null); }, toggleSidebar: () => setHidden(h => !h) });
  const [environment, setEnvironment] = useState(null);
  // Leaving for another page (or project) ends the setup and brings the sidebar back.
  useEffect(() => { if (gameSetup) closeGameSetup(); }, [project?.id, active, environment, resource?.id]); // eslint-disable-line react-hooks/exhaustive-deps
  const openProject = async p => { try {const loaded=(await api.project(p.id)).project;if(!confirmLeave())return;setProject(loaded);setEnvironment(loaded.environments[0]?.name ?? 'Production');setActive('Resources');setResourceState(null);setServerState(null);setMissing('');setMobileOpen(false);}catch(e){if(e.name!=='AbortError')setMissing(e.message);} };
  const environmentId = project?.environments.find(e => e.name === environment)?.id;
  const closeProject = () => { if (!confirmLeave()) return; setProject(null); setActive('Projects'); setResourceState(null); setMobileOpen(false); };
  // Keep the address bar in sync with what's on screen, and restore it on reload or Back/Forward.
  const [restoring, setRestoring] = useState(true);
  const [missing, setMissing] = useState('');
  async function applyUrl(initial = false) {
    // Opening Ignition at its root goes to the start page chosen in Preferences.
    if (initial && (location.pathname === '/' || location.pathname === `/teams/${team.id}/` || location.pathname === `/teams/${team.slug}/`)) {
      const start = getPreferences().startPage;
      let last = null;
      try { last = localStorage.getItem('ignition:last-path'); } catch { /* private mode */ }
      const to = start === 'last' ? last : { projects: '/projects', servers: '/servers', monitor: '/monitor', domains: '/domains' }[start];
      if (to && to !== '/') history.replaceState(null, '', to.startsWith('/teams/') ? to : teamPath(to));
    }
    const target = fromUrl(location.pathname, location.search);
    setMissing('');
    try {
      let p = target.projectId ? (await api.project(target.projectId)).project : null;
      const r = target.resourceId ? (await api.resource(target.resourceId)).resource : null;
      const srv = target.serverId ? (await api.server(target.serverId)).server : null;
      if (r?.projectId && !p) {try {p=(await api.project(r.projectId)).project;}catch(e){if(![403,404].includes(e.status))throw e;}}
      if (r && target.projectId && r.projectId !== p?.id) throw new Error('mismatch');
      setProject(p);
      setEnvironment(p ? (p.environments.some(e => e.name === target.env) ? target.env : p.environments[0]?.name ?? 'Production') : r?.environment ?? null);
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
    applyUrl(true);
    const onPop = () => applyUrl();
    window.addEventListener('popstate', onPop);
    return () => window.removeEventListener('popstate', onPop);
  }, []); // eslint-disable-line react-hooks/exhaustive-deps
  const url = toUrl({ active, project, resource, resourceView, environment, server, serverView });
  useEffect(() => {
    if (restoring) return;
    try { localStorage.setItem('ignition:last-path', url); } catch { /* private mode */ }
    if (url === location.pathname + location.search) return;
    history.pushState(null, '', url);
  }, [url, restoring]);
  useEffect(() => {
    const where = resource ? `${resourceView === 'Overview' ? resource.name : `${resourceView} · ${resource.name}`}` : project ? `${active} · ${project.name}`
      : server ? (serverView === 'Overview' ? server.name : `${serverView} · ${server.name}`) : active;
    document.title = `${where} · ${instanceName}`;
  }, [active, project, resource, resourceView, server, serverView, instanceName]);

  // Declared before the pages that link to resources use it.
  const openResourceFromAnywhere = (r, view = 'Overview') => {
    if (!confirmLeave()) return;
    history.pushState(null, '', teamPath(`/resources/${r.id}${view === 'Overview' ? '' : `/${slug(view)}`}`));
    applyUrl();
  };
  const projectPages = project && { Resources: resource
    ? renderResourcePage()
    : gameSetup ? <GamePages.GameWizard key={gameSetup} gameId={gameSetup} project={project} environment={environment} environmentId={environmentId}
      takenNames={[]} onClose={closeGameSetup} onCreated={r => { closeGameSetup(); setResource(r); }} />
    : <Resources project={project} environment={environment} environmentId={environmentId} onOpen={setResource} onCreateGame={openGameSetup} />,
    Deployments: <ProjectDeployments key={`${project.id}:${environment}`} project={project} environment={environment} onOpenResource={openResourceFromAnywhere} />,
    Domains: <DomainsHub key={`${project.id}:${environment}`} project={project} environment={environment} onOpenResource={openResourceFromAnywhere} />,
    Monitoring: <ProjectMonitoring key={`${project.id}:${environment}`} project={project} environment={environment} onOpenResource={openResourceFromAnywhere} />,
    Logs: <ProjectLogs key={`${project.id}:${environment}`} project={project} environment={environment} onOpenResource={openResourceFromAnywhere} />,
    'Project Settings': <ResourceAccess resource={project}><ProjectSettings key={project.id} project={project} onProjectChange={setProject} onDeleted={() => { setProject(null); setActive('Projects'); }} /></ResourceAccess> };
  function renderResourcePage(){return <ResourceAccess resource={resource}>{renderResourceContent()}</ResourceAccess>;}
  function renderResourceContent() {
    const props = { key: `${resource.id ?? resource.name}:${resourceView}`, project: project ?? {id:null,name:'No project',environments:[]}, environment, resource, onNavigate: showResourceView, onResourceChange: setResourceState, onDeleted: () => setResourceState(null) };
    if (resource.type === 'game') {
      if (resourceView === 'Overview') return <GamePages.GameOverview {...props} />;
      const GamePage = GamePages[gamePageComponents[resourceView]];
      if (GamePage) return <GamePage {...props} />;
    }
    if (resourceView === 'Overview') return <ResourceDetail {...props} />;
    if (resourceView === 'Deployments') return <ResourcePages.ResourceDeployments {...props} />;
    const Page = ResourcePages[resourcePageComponents[resourceView]];
    return Page ? (LIVE_RESOURCE_VIEWS.includes(resourceView) ? <Page {...props} /> : preview(<Page {...props} />)) : <ComingSoon title={resourceView} />;
  }
  // Opening a resource from Monitor goes through its URL, so project and resource load the same way as a reload.
  // Instance administration is for platform admins; the API refuses the rest anyway, this explains why.
  const platformAdmin = ['owner', 'admin'].includes((session.platformUser ?? session.user).role);
  const adminArea = (adminPages.includes(active) || (infraAdmin && infrastructurePages.includes(active))) && !project && !resource && !server;
  const adminDenied = adminArea && !platformAdmin;
  // To people who don't administer the instance, /admin (and admin-only infrastructure) doesn't exist:
  // they land on their team's dashboard, with no hint of what's there.
  useEffect(() => { if (adminDenied) { setActive('Dashboard'); history.replaceState(null, '', teamPath('/dashboard')); } }, [adminDenied]);
  const page = adminDenied ? null
    : resource ? renderResourcePage() : project ? projectPages[active]
    : active === 'Resources' ? <TeamResources team={team} onOpen={openResourceFromAnywhere} />
    : active === 'Projects' ? <Projects onOpen={openProject} />
        : active === 'Servers' ? (server
          ? <ServerWorkspace key={server.id} server={server} view={serverView} onNavigate={showServerView} onServerChange={setServerState} onRemoved={() => { setServerState(null); setServerView('Overview'); }} onOpenResource={openResourceFromAnywhere} />
          : <Servers onOpen={openServer} />)
        : active === 'Dashboard' ? <Dashboard onOpenResource={openResourceFromAnywhere} onOpenProject={openProject} onOpenServer={openServer} onNavigate={navigate} />
        : active === 'Monitor' ? <Monitor onOpenResource={openResourceFromAnywhere} />
        : active === 'Team Sources' ? <><TeamScopeNote what="Sources" /><Sources onOpenResource={openResourceFromAnywhere} /></>
        : active === 'Sources' ? <>{infraAdmin && <TeamScopeNote what="Sources" />}<Sources onOpenResource={openResourceFromAnywhere} /></>
        : active === 'Domains' ? <DomainsHub onOpenResource={openResourceFromAnywhere} />
        : active === 'Networking' ? <Networking onOpenResource={openResourceFromAnywhere} />
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
      <MfaBanner />
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

function TeamResources({team,onOpen}) {
 const envs=useQuery(()=>api.teamEnvironments(),[]);
 const [selected,setSelected]=useState('');
 const [game,setGame]=useState(null);
 const environment=envs.data?.environments.find(e=>e.id===selected) ?? envs.data?.environments[0];
 if(!environment)return envs.error ? <Notice tone="danger" title={envs.error.message}/> : <p>Loading environments?</p>;
 const project={id:null,name:team.name,environments:envs.data.environments};
 if(game)return <GamePages.GameWizard gameId={game} project={project} environment={environment.name} environmentId={environment.id} takenNames={[]} onClose={()=>setGame(null)} onCreated={r=>{setGame(null);onOpen(r);}}/>;
 return <><div style={{padding:'16px 24px 0',maxWidth:240}}><Select label="Environment" value={selected} options={[{value:'',label:'All environments'},...envs.data.environments.map(e=>({value:e.id,label:e.name}))]} onChange={setSelected}/></div><Resources key={selected} listEnvironmentId={selected || null} project={project} environment={environment.name} environmentId={environment.id} onOpen={onOpen} onCreateGame={setGame}/></>;
}

import { ArrowLeftIcon, PlusIcon, ChevronDownIcon, MoonIcon, SunIcon, ViewColumnsIcon } from '@heroicons/react/20/solid';
import { IconButton, Menu, Select, Status, SegmentedControl, Sidebar, SidebarFooter, SidebarGroup, SidebarHeader, SidebarItem, SidebarNav, SidebarSeparator, SidebarSubItem, Logo } from '../ui';
import { useTheme } from '../hooks/useTheme';
import { useSession } from './AuthGate';
import { useInstanceName } from './instance';
import { ArrowRightStartOnRectangleIcon, UserCircleIcon } from '@heroicons/react/16/solid';
import { groups, projectItems, resourceSectionsFor, serverSections, settingsPages, settingsSections, topItems } from './navigation';
import { connection } from '../pages/servers/model';

const envColor = env => env === 'Production' ? 'var(--success)' : env === 'Staging' ? 'var(--warning)' : 'var(--muted)';
const themeOptions = [{ value: 'light', label: 'Light', icon: SunIcon }, { value: 'dark', label: 'Dark', icon: MoonIcon }];

const resourceTone = { running: 'success', deploying: 'progress', unhealthy: 'warning', paused: 'warning', failed: 'danger' };

export function AppSidebar({ active, project, environment, onEnvironmentChange, onNavigate, onCloseProject, resource, resourceView, onResourceNavigate, onCloseResource, onHide, server, serverView, onServerNavigate, onCloseServer }) {
  const [theme, setTheme] = useTheme();
  const { user, signOut } = useSession();
  const instanceName = useInstanceName();
  const footer = <SidebarFooter><SegmentedControl label="Theme" options={themeOptions} value={theme} onChange={setTheme} /></SidebarFooter>;

  if (server) return <Sidebar label={`${server.name} navigation`}>
    <SidebarHeader>
      <button className="back-to-app" onClick={onCloseServer}><ArrowLeftIcon />All servers</button>
      <IconButton icon={ViewColumnsIcon} label="Hide sidebar" onClick={onHide} />
    </SidebarHeader>
    <div className="sidebar-project">
      <span className="sidebar-project-label">Server</span>
      <strong className="sidebar-resource-name"><Status tone={connection(server).tone} label={connection(server).label} />{server.name}</strong>
      <span className="sidebar-resource-meta">{server.isLocal ? 'This machine' : server.endpoint}</span>
    </div>
    <SidebarNav>
      {serverSections.map(({ label, items }) => <div key={label} className="sidebar-section">
        <span className="sidebar-section-label">{label}</span>
        {items.map(({ title, icon }) => <SidebarItem key={title} icon={icon} active={serverView === title} onClick={() => onServerNavigate(title)}>{title}</SidebarItem>)}
      </div>)}
    </SidebarNav>
    {footer}
  </Sidebar>;

  if (project && resource) return <Sidebar label={`${resource.name} navigation`}>
    <SidebarHeader>
      <button className="back-to-app" onClick={onCloseResource}><ArrowLeftIcon />{project.name}</button>
      <IconButton icon={ViewColumnsIcon} label="Hide sidebar" onClick={onHide} />
    </SidebarHeader>
    <div className="sidebar-project">
      <span className="sidebar-project-label">Resource</span>
      <strong className="sidebar-resource-name"><Status tone={resourceTone[resource.status] ?? 'neutral'} label={resource.status} />{resource.name}</strong>
      <span className="sidebar-resource-meta">{environment}</span>
    </div>
    <SidebarNav>
      {resourceSectionsFor(resource.type, resource.sourceType).map((section, i) => <div key={i}>
        {i > 0 && <SidebarSeparator />}
        {section.map(({ title, icon }) => <SidebarItem key={title} icon={icon} active={resourceView === title} onClick={() => onResourceNavigate(title)}>{title}</SidebarItem>)}
      </div>)}
    </SidebarNav>
    {footer}
  </Sidebar>;

  if (project) return <Sidebar label={`${project.name} navigation`}>
    <SidebarHeader>
      <button className="back-to-app" onClick={onCloseProject}><ArrowLeftIcon />All projects</button>
      <IconButton icon={ViewColumnsIcon} label="Hide sidebar" onClick={onHide} />
    </SidebarHeader>
    <div className="sidebar-project">
      <span className="sidebar-project-label">Project</span>
      <strong>{project.name}</strong>
      <Select className="sidebar-env-select" label="Environment" value={environment} onChange={onEnvironmentChange} searchable={6}
        options={project.environments.map(env => ({ value: env.name, label: env.name, dot: envColor(env.name) }))} />
    </div>
    <SidebarNav>
      {projectItems.map(({ title, icon }) => <SidebarItem key={title} icon={icon} active={active === title} onClick={() => onNavigate(title)}>{title}</SidebarItem>)}
    </SidebarNav>
    {footer}
  </Sidebar>;

  if (settingsPages.includes(active)) return <Sidebar label="Settings navigation">
    <SidebarHeader>
      <button className="back-to-app" onClick={() => onNavigate('Dashboard')}><ArrowLeftIcon />Back to App</button>
      <IconButton icon={ViewColumnsIcon} label="Hide sidebar" onClick={onHide} />
    </SidebarHeader>
    <SidebarNav>
      {settingsSections.map((section, i) => <div key={i}>
        {i > 0 && <SidebarSeparator />}
        {section.map(({ title, icon }) => <SidebarItem key={title} icon={icon} active={active === title} onClick={() => onNavigate(title)}>{title}</SidebarItem>)}
      </div>)}
    </SidebarNav>
    {footer}
  </Sidebar>;

  return <Sidebar>
    <SidebarHeader>
      <Menu className="workspace" label="Account" align="start" items={[
        { label: `Signed in as ${user.email} (${user.role})`, icon: UserCircleIcon, onSelect: () => {} },
        'separator',
        { label: 'Sign out', icon: ArrowRightStartOnRectangleIcon, onSelect: signOut },
      ]}><Logo className="brand-logo" /><span>{instanceName}</span><ChevronDownIcon className="workspace-caret" /></Menu>
      <IconButton icon={ViewColumnsIcon} label="Hide sidebar" onClick={onHide} />
    </SidebarHeader>
    <SidebarNav>
      {topItems.map(({ title, icon }) => <SidebarItem key={title} icon={icon} active={active === title} onClick={() => onNavigate(title === 'Settings' ? settingsPages[0] : title)}>{title}</SidebarItem>)}
      {groups.map(({ label, items }) => <SidebarGroup key={label} label={label}>
        {items.map(title => <SidebarSubItem key={title} active={active === title} onClick={() => onNavigate(title)}>{title}</SidebarSubItem>)}
      </SidebarGroup>)}
    </SidebarNav>
    {footer}
  </Sidebar>;
}

import { ArrowLeftIcon, PlusIcon, ChevronDownIcon, MoonIcon, SunIcon, ViewColumnsIcon } from '@heroicons/react/20/solid';
import { Avatar, IconButton, Menu, Select, Status, SegmentedControl, Sidebar, SidebarFooter, SidebarGroup, SidebarHeader, SidebarItem, SidebarNav, SidebarSeparator, SidebarSubItem, Logo } from '../ui';
import { TeamMark, roleLabel, useTeam } from './Teams';
import { useInfrastructureAdminOnly } from './instance';
import { useTheme } from '../hooks/useTheme';
import { useSession } from './AuthGate';
import { ArrowRightStartOnRectangleIcon, CheckIcon, Cog6ToothIcon, ServerStackIcon, ShieldCheckIcon, UsersIcon } from '@heroicons/react/16/solid';
import { useEffect } from 'react';
import { adminPages, adminSections, infrastructurePages, infrastructureSection, groups, projectItems, resourceSectionsFor, serverSections, settingsPages, settingsSections, topItems } from './navigation';
import { connection } from '../pages/servers/model';

const envColor = env => env === 'Production' ? 'var(--success)' : env === 'Staging' ? 'var(--warning)' : 'var(--muted)';
const themeOptions = [{ value: 'light', label: 'Light', icon: SunIcon }, { value: 'dark', label: 'Dark', icon: MoonIcon }];

const resourceTone = { running: 'success', deploying: 'progress', unhealthy: 'warning', paused: 'warning', failed: 'danger' };

export function AppSidebar({ active, project, environment, onEnvironmentChange, onNavigate, onCloseProject, resource, resourceView, onResourceNavigate, onCloseResource, onHide, server, serverView, onServerNavigate, onCloseServer }) {
  const teamsContext = useTeam();
  const [theme, setTheme] = useTheme();
  const { user, platformUser, signOut } = useSession();
  const platformRole = (platformUser ?? user).role;
  const infraAdmin = useInfrastructureAdminOnly();
  const inAdmin = (adminPages.includes(active) || (infraAdmin && infrastructurePages.includes(active))) && !project && !resource && !server;
  const adminGroups = infraAdmin ? [...adminSections, infrastructureSection] : adminSections;
  useEffect(() => { const open = () => onNavigate('Security'); addEventListener('ignition:open-account', open); return () => removeEventListener('ignition:open-account', open); }, []);
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

  if (resource) return <Sidebar label={`${resource.name} navigation`}>
    <SidebarHeader>
      <button className="back-to-app" onClick={onCloseResource}><ArrowLeftIcon />{project?.name ?? 'Resources'}</button>
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

  // Instance administration: the whole installation, separate from your team's settings.
  if (inAdmin) return <Sidebar label="Instance administration">
    <SidebarHeader>
      <button className="back-to-app" onClick={() => onNavigate('Dashboard')}><ArrowLeftIcon />Back to App</button>
      <IconButton icon={ViewColumnsIcon} label="Hide sidebar" onClick={onHide} />
    </SidebarHeader>
    <div className="sidebar-project"><span className="sidebar-project-label">Instance administration</span><strong>Ignition</strong></div>
    <SidebarNav>
      {adminGroups.map(group => <SidebarGroup key={group.label} label={group.label}>
        {group.items.map(({ title, icon }) => <SidebarItem key={title} icon={icon} active={active === title} onClick={() => onNavigate(title)}>{title}</SidebarItem>)}
      </SidebarGroup>)}
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
        // Teams first: switching is what this menu is for most often.
        { heading: 'Teams' },
        ...teamsContext.teams.map(t => ({ key: `team:${t.id}`, label: t.name, hint: roleLabel[t.role] ?? t.role, mark: <TeamMark team={t} size={22} />, checked: t.id === teamsContext.team.id, onSelect: () => teamsContext.switchTeam(t) })),
        ...(teamsContext.canCreateTeam ? [{ label: 'Create team', icon: PlusIcon, onSelect: teamsContext.createTeam }] : []),
        'separator',
        { label: 'Team settings', icon: Cog6ToothIcon, onSelect: () => onNavigate('Team') },
        { label: 'Team members', icon: UsersIcon, onSelect: () => onNavigate('Team Members') },
        'separator',
        ...(['owner', 'admin'].includes(platformRole) ? [{ label: 'Instance administration', icon: ServerStackIcon, onSelect: () => onNavigate('General') }, 'separator'] : []),
        { label: `Account: ${user.email}`, mark: <Avatar name={user.name || user.email} src={user.avatarUrl} size={20} />, onSelect: () => onNavigate('Profile') },
        { label: 'Two-factor authentication', icon: ShieldCheckIcon, onSelect: () => onNavigate('Security') },
        'separator',
        { label: 'Sign out', icon: ArrowRightStartOnRectangleIcon, onSelect: signOut },
      ]}><TeamMark team={teamsContext.team} size={20} /><span className="workspace-name">{teamsContext.team.name}</span><ChevronDownIcon className="workspace-caret" /></Menu>
      <IconButton icon={ViewColumnsIcon} label="Hide sidebar" onClick={onHide} />
    </SidebarHeader>
    <SidebarNav>
      {topItems.map(({ title, icon }) => <SidebarItem key={title} icon={icon} active={active === title} onClick={() => onNavigate(title === 'Settings' ? settingsPages[0] : title)}>{title}</SidebarItem>)}
      {groups.filter(g => !(infraAdmin && g.label === 'Infrastructure')).map(({ label, items }) => <SidebarGroup key={label} label={label}>
        {items.map(title => <SidebarSubItem key={title} active={active === title} onClick={() => onNavigate(title)}>{title}</SidebarSubItem>)}
      </SidebarGroup>)}
    </SidebarNav>
    {footer}
  </Sidebar>;
}

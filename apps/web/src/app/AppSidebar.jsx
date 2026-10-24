import { ArrowLeftIcon, PlusIcon, BoltIcon, ChevronDownIcon, MoonIcon, SunIcon, ViewColumnsIcon } from '@heroicons/react/20/solid';
import { IconButton, Select, SegmentedControl, Sidebar, SidebarFooter, SidebarGroup, SidebarHeader, SidebarItem, SidebarNav, SidebarSeparator, SidebarSubItem } from '../ui';
import { useTheme } from '../hooks/useTheme';
import { groups, projectItems, settingsPages, settingsSections, topItems } from './navigation';

const envColor = env => env === 'Production' ? 'var(--success)' : env === 'Staging' ? 'var(--warning)' : 'var(--muted)';
const themeOptions = [{ value: 'light', label: 'Light', icon: SunIcon }, { value: 'dark', label: 'Dark', icon: MoonIcon }];

export function AppSidebar({ active, project, environment, onEnvironmentChange, onNavigate, onCloseProject, onHide }) {
  const [theme, setTheme] = useTheme();
  const footer = <SidebarFooter><SegmentedControl label="Theme" options={themeOptions} value={theme} onChange={setTheme} /></SidebarFooter>;

  if (project) return <Sidebar label={`${project.name} navigation`}>
    <SidebarHeader>
      <button className="back-to-app" onClick={onCloseProject}><ArrowLeftIcon />All projects</button>
      <IconButton icon={ViewColumnsIcon} label="Hide sidebar" onClick={onHide} />
    </SidebarHeader>
    <div className="sidebar-project">
      <span className="sidebar-project-label">Project</span>
      <strong>{project.name}</strong>
      <Select className="sidebar-env-select" label="Environment" value={environment} onChange={onEnvironmentChange} searchable={6}
        options={project.environments.map(env => ({ value: env, label: env, dot: envColor(env) }))}
        action={{ label: 'New environment', icon: PlusIcon }} />
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
      <button className="workspace"><span className="brand-mark"><BoltIcon /></span><span>Ignition</span><ChevronDownIcon className="workspace-caret" /></button>
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

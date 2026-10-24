import { BoltIcon, ChevronDownIcon, MoonIcon, SunIcon, ViewColumnsIcon } from '@heroicons/react/20/solid';
import { IconButton, SegmentedControl, Sidebar, SidebarFooter, SidebarGroup, SidebarHeader, SidebarItem, SidebarNav, SidebarSubItem } from '../ui';
import { useTheme } from '../hooks/useTheme';
import { groups, topItems } from './navigation';

const themeOptions = [{ value: 'light', label: 'Light', icon: SunIcon }, { value: 'dark', label: 'Dark', icon: MoonIcon }];

export function AppSidebar({ active, onNavigate, onHide }) {
  const [theme, setTheme] = useTheme();
  return <Sidebar>
    <SidebarHeader>
      <button className="workspace"><span className="brand-mark"><BoltIcon /></span><span>Ignition</span><ChevronDownIcon className="workspace-caret" /></button>
      <IconButton icon={ViewColumnsIcon} label="Hide sidebar" onClick={onHide} />
    </SidebarHeader>
    <SidebarNav>
      {topItems.map(({ title, icon }) => <SidebarItem key={title} icon={icon} active={active === title} onClick={() => onNavigate(title)}>{title}</SidebarItem>)}
      {groups.map(({ label, items }) => <SidebarGroup key={label} label={label}>
        {items.map(title => <SidebarSubItem key={title} active={active === title} onClick={() => onNavigate(title)}>{title}</SidebarSubItem>)}
      </SidebarGroup>)}
    </SidebarNav>
    <SidebarFooter><SegmentedControl label="Theme" options={themeOptions} value={theme} onChange={setTheme} /></SidebarFooter>
  </Sidebar>;
}

import {
  AdjustmentsHorizontalIcon, CommandLineIcon, CubeIcon, GlobeAltIcon, RocketLaunchIcon, ArchiveBoxIcon, ArrowPathIcon, BellIcon, ChartBarIcon, ClipboardDocumentListIcon, Cog6ToothIcon,
  KeyIcon, RectangleStackIcon, ShieldCheckIcon, Squares2X2Icon, UserCircleIcon, UsersIcon, WrenchScrewdriverIcon,
} from '@heroicons/react/20/solid';

export const topItems = [
  { title: 'Dashboard', icon: Squares2X2Icon },
  { title: 'Projects', icon: RectangleStackIcon },
  { title: 'Monitor', icon: ChartBarIcon },
  { title: 'Settings', icon: Cog6ToothIcon },
];

export const groups = [
  { label: 'Resources', items: ['Applications', 'Databases', 'Services', 'Scheduled jobs', 'Deployments'] },
  { label: 'Infrastructure', items: ['Servers', 'Sources', 'Domains', 'Logs & monitoring'] },
  { label: 'Discover', items: ['Templates', 'Changelog', 'Documentation'] },
];

export const settingsSections = [
  [
    { title: 'General', icon: Cog6ToothIcon },
    { title: 'Authentication & Security', icon: ShieldCheckIcon },
    { title: 'Backups', icon: ArchiveBoxIcon },
    { title: 'Notifications', icon: BellIcon },
    { title: 'Updates', icon: ArrowPathIcon },
    { title: 'Advanced', icon: WrenchScrewdriverIcon },
  ],
  [
    { title: 'Users & Permissions', icon: UsersIcon },
    { title: 'API Tokens', icon: KeyIcon },
    { title: 'Audit Log', icon: ClipboardDocumentListIcon },
  ],
  [
    { title: 'Profile', icon: UserCircleIcon },
    { title: 'Preferences', icon: AdjustmentsHorizontalIcon },
  ],
];

export const settingsPages = settingsSections.flat().map(item => item.title);

export const projectItems = [
  { title: 'Resources', icon: CubeIcon },
  { title: 'Deployments', icon: RocketLaunchIcon },
  { title: 'Domains', icon: GlobeAltIcon },
  { title: 'Monitoring', icon: ChartBarIcon },
  { title: 'Logs', icon: CommandLineIcon },
  { title: 'Project Settings', icon: Cog6ToothIcon },
];

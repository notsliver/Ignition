import {
  AdjustmentsHorizontalIcon, ArrowUturnLeftIcon, CalendarDaysIcon, CircleStackIcon, CpuChipIcon, DocumentTextIcon, HeartIcon, HomeIcon,
  CodeBracketIcon, LockClosedIcon, ServerStackIcon, VariableIcon, CommandLineIcon, CubeIcon, GlobeAltIcon, RocketLaunchIcon, ArchiveBoxIcon, ArrowPathIcon, BellIcon, ChartBarIcon, ClipboardDocumentListIcon, Cog6ToothIcon,
  KeyIcon, RectangleStackIcon, ShieldCheckIcon, Squares2X2Icon, UserCircleIcon, UsersIcon, WrenchScrewdriverIcon,
  BeakerIcon, BoltIcon, ClockIcon, PresentationChartLineIcon, ShareIcon, Square3Stack3DIcon,
} from '@heroicons/react/20/solid';

export const topItems = [
  { title: 'Dashboard', icon: Squares2X2Icon },
  { title: 'Projects', icon: RectangleStackIcon },
  { title: 'Monitor', icon: ChartBarIcon },
  { title: 'Settings', icon: Cog6ToothIcon },
];

export const groups = [
  { label: 'Resources', items: ['Applications', 'Databases', 'Services', 'Scheduled jobs', 'Deployments'] },
  { label: 'Infrastructure', items: ['Servers', 'Sources', 'Domains', 'Networking', 'Logs & monitoring'] },
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

export const resourceSections = [
  [
    { title: 'Overview', icon: HomeIcon },
    { title: 'Deployments', icon: RocketLaunchIcon },
    { title: 'Logs', icon: DocumentTextIcon },
    { title: 'Metrics', icon: ChartBarIcon },
    { title: 'Terminal', icon: CommandLineIcon },
  ],
  [
    { title: 'Code', icon: CodeBracketIcon },
    { title: 'Source', icon: CodeBracketIcon },
    { title: 'Build & Deploy', icon: CubeIcon },
    { title: 'Environment Variables', icon: VariableIcon },
    { title: 'Domains', icon: GlobeAltIcon },
    { title: 'Networking', icon: ServerStackIcon },
    { title: 'Storage', icon: CircleStackIcon },
    { title: 'Health Checks', icon: HeartIcon },
    { title: 'Resource Limits', icon: CpuChipIcon },
  ],
  [
    { title: 'Rollback History', icon: ArrowUturnLeftIcon },
    { title: 'Scheduled Tasks', icon: CalendarDaysIcon },
    { title: 'Automations', icon: BoltIcon },
    { title: 'Access & Secrets', icon: LockClosedIcon },
    { title: 'Advanced Settings', icon: WrenchScrewdriverIcon },
  ],
];

// Hide sections that don't apply to a resource type instead of showing empty pages.
const hiddenByType = {
  application: ['Code'],
  database: ['Code', 'Build & Deploy', 'Domains'],
  worker: ['Code', 'Domains'],
  cron: ['Code', 'Domains', 'Networking', 'Health Checks', 'Scheduled Tasks'],
  function: ['Source', 'Build & Deploy', 'Storage'],
};
// External databases run elsewhere: only what Ignition does with them applies.
const externalSections = ['Overview', 'Environment Variables', 'Automations'];
export const resourceSectionsFor = (type, sourceType) => resourceSections
  .map(section => section.filter(item => sourceType === 'external' ? externalSections.includes(item.title) : !hiddenByType[type]?.includes(item.title)))
  .filter(section => section.length > 0);

// A server's workspace, grouped so the list stays short. Titles double as URL slugs.
export const serverSections = [
  { label: 'General', items: [
    { title: 'Overview', icon: HomeIcon },
    { title: 'Resources', icon: CubeIcon },
    { title: 'Deployments', icon: RocketLaunchIcon },
  ] },
  { label: 'Observability', items: [
    { title: 'Monitoring', icon: PresentationChartLineIcon },
    { title: 'Logs', icon: DocumentTextIcon },
    { title: 'Diagnostics', icon: BeakerIcon },
  ] },
  { label: 'Runtime', items: [
    { title: 'Docker', icon: Square3Stack3DIcon },
    { title: 'Storage', icon: CircleStackIcon },
    { title: 'Networking', icon: ShareIcon },
  ] },
  { label: 'Operations', items: [
    { title: 'Maintenance', icon: WrenchScrewdriverIcon },
    { title: 'Tasks & Automation', icon: ClockIcon },
    { title: 'Automations', icon: BoltIcon },
  ] },
  { label: 'Administration', items: [
    { title: 'Access & Security', icon: ShieldCheckIcon },
    { title: 'Settings', icon: Cog6ToothIcon },
  ] },
];
export const serverViews = serverSections.flatMap(s => s.items.map(i => i.title));

import {
  AdjustmentsHorizontalIcon, ArrowUturnLeftIcon, CalendarDaysIcon, CircleStackIcon, CpuChipIcon, DocumentTextIcon, HeartIcon, HomeIcon,
  CodeBracketIcon, LockClosedIcon, ServerStackIcon, VariableIcon, CommandLineIcon, CubeIcon, GlobeAltIcon, RocketLaunchIcon, ArchiveBoxIcon, ArrowPathIcon, BellIcon, ChartBarIcon, ClipboardDocumentListIcon, Cog6ToothIcon,
  KeyIcon, RectangleStackIcon, ShieldCheckIcon, Squares2X2Icon, UserCircleIcon, UsersIcon, WrenchScrewdriverIcon,
  BeakerIcon, BoltIcon, ClockIcon, PresentationChartLineIcon, ShareIcon, Square3Stack3DIcon,
  AdjustmentsVerticalIcon, FolderIcon, MapIcon, PuzzlePieceIcon, UserGroupIcon,
} from '@heroicons/react/20/solid';

export const topItems = [
  { title: 'Dashboard', icon: Squares2X2Icon },
  { title: 'Resources', icon: CubeIcon },
  { title: 'Projects', icon: RectangleStackIcon },
  { title: 'Monitor', icon: ChartBarIcon },
  { title: 'Settings', icon: Cog6ToothIcon },
];

export const groups = [
  { label: 'Resources', items: ['Applications', 'Databases', 'Services', 'Scheduled jobs', 'Deployments'] },
  { label: 'Infrastructure', items: ['Servers', 'Sources', 'Domains', 'Networking', 'Logs & monitoring'] },
  { label: 'Discover', items: ['Templates', 'Changelog', 'Documentation'] },
];

// Settings: what's yours. Your team, and you.
export const settingsSections = [
  [
    { title: 'Team', icon: UserGroupIcon },
    { title: 'Team Members', icon: UsersIcon },
    { title: 'Team Sources', icon: CodeBracketIcon },
  ],
  [
    { title: 'Profile', icon: UserCircleIcon },
    { title: 'Security', icon: ShieldCheckIcon },
    { title: 'Preferences', icon: AdjustmentsHorizontalIcon },
    { title: 'API Tokens', icon: KeyIcon },
  ],
];

// Instance administration (/admin): the whole installation, for platform admins. Every team is
// affected by these, so they don't live in a team's settings.
export const adminSections = [
  { label: 'Instance', items: [
    { title: 'General', icon: Cog6ToothIcon },
    { title: 'Updates', icon: ArrowPathIcon },
    { title: 'Limits', icon: AdjustmentsHorizontalIcon },
    { title: 'Backups', icon: ArchiveBoxIcon },
    { title: 'Notifications', icon: BellIcon },
    { title: 'Advanced', icon: WrenchScrewdriverIcon },
  ] },
  { label: 'People & access', items: [
    { title: 'Users', icon: UsersIcon },
    { title: 'Teams', icon: UserGroupIcon },
    { title: 'Authentication & Security', icon: ShieldCheckIcon },
  ] },
  { label: 'Records', items: [
    { title: 'Audit Log', icon: ClipboardDocumentListIcon },
  ] },
];
export const adminPages = adminSections.flatMap(s => s.items.map(i => i.title));
// When General › Infrastructure is "Instance administration only", these join the admin sidebar.
export const infrastructureSection = { label: 'Infrastructure', items: [
  { title: 'Servers', icon: ServerStackIcon },
  { title: 'Sources', icon: CodeBracketIcon },
  { title: 'Domains', icon: GlobeAltIcon },
  { title: 'Networking', icon: ShareIcon },
  { title: 'Logs & monitoring', icon: PresentationChartLineIcon },
] };
export const infrastructurePages = infrastructureSection.items.map(i => i.title);

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
    { title: 'Files', icon: FolderIcon },
    { title: 'Backups', icon: ArchiveBoxIcon },
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
  function: ['Source', 'Build & Deploy', 'Storage', 'Files', 'Backups'],
};
// External databases run elsewhere: only what Ignition does with them applies.
const externalSections = ['Overview', 'Environment Variables', 'Automations'];
// A game server's sidebar: playing first, then how it runs. Titles double as URL slugs.
export const gameSections = [
  [{ title: 'Overview', icon: HomeIcon }, { title: 'Console', icon: CommandLineIcon }, { title: 'Players', icon: UserGroupIcon },
    { title: 'Mods & Plugins', icon: PuzzlePieceIcon }, { title: 'Worlds', icon: MapIcon }],
  [{ title: 'Server Settings', icon: AdjustmentsVerticalIcon }, { title: 'Files', icon: FolderIcon }, { title: 'Backups', icon: ArchiveBoxIcon }, { title: 'Schedules', icon: ClockIcon }],
  [{ title: 'Deployments', icon: RocketLaunchIcon }, { title: 'Logs', icon: DocumentTextIcon }, { title: 'Metrics', icon: ChartBarIcon }, { title: 'Terminal', icon: CommandLineIcon }],
  [{ title: 'Environment Variables', icon: VariableIcon }, { title: 'Networking', icon: ServerStackIcon }, { title: 'Storage', icon: CircleStackIcon },
    { title: 'Resource Limits', icon: CpuChipIcon }, { title: 'Automations', icon: BoltIcon }, { title: 'Access & Secrets', icon: LockClosedIcon }, { title: 'Advanced Settings', icon: WrenchScrewdriverIcon }],
];
export const gameViews = gameSections[0].concat(gameSections[1]).map(i => i.title).filter(t => t !== 'Overview');

export const resourceSectionsFor = (type, sourceType) => type === 'game' ? gameSections : resourceSections
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
    { title: 'Allocations', icon: MapIcon },
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

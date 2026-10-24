import { Squares2X2Icon, PuzzlePieceIcon, Cog6ToothIcon } from '@heroicons/react/20/solid';

export const topItems = [
  { title: 'Dashboard', icon: Squares2X2Icon },
  { title: 'Integrations', icon: PuzzlePieceIcon },
  { title: 'Settings', icon: Cog6ToothIcon },
];

export const groups = [
  { label: 'Projects', items: ['Applications', 'Databases', 'Services', 'Scheduled jobs', 'Deployments'] },
  { label: 'Infrastructure', items: ['Servers', 'Sources', 'Domains', 'Logs & monitoring'] },
  { label: 'Discover', items: ['Templates', 'Changelog', 'Documentation'] },
];

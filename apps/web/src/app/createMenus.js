// "Create" menus used by the Dashboard quick actions and the Projects page.
import {
  ArchiveBoxArrowDownIcon, ArrowDownTrayIcon, ArrowsRightLeftIcon, CircleStackIcon, CloudArrowDownIcon, CloudArrowUpIcon, CloudIcon,
  CodeBracketIcon, CommandLineIcon, CubeIcon, ComputerDesktopIcon, DocumentArrowUpIcon, DocumentDuplicateIcon, DocumentTextIcon, FolderOpenIcon,
  FolderPlusIcon, GlobeAltIcon, KeyIcon, LinkIcon, PlusIcon, ServerIcon, Squares2X2Icon, WrenchScrewdriverIcon,
} from '@heroicons/react/20/solid';
import { GitHubIcon, GitLabIcon } from '../ui';

const gitSources = [
  { label: 'Continue with Artifacts', icon: CloudArrowUpIcon, color: '#f38020' },
  { label: 'Continue with GitHub', icon: GitHubIcon, color: 'var(--text)' },
  { label: 'Continue with GitLab', icon: GitLabIcon },
  { label: 'Continue with other source', icon: LinkIcon },
];

export const deployOptions = [
  {
    label: 'From Git repository', icon: CodeBracketIcon, items: gitSources,
  },
  'separator',
  { label: 'Deploy a container image', icon: CubeIcon, color: 'var(--accent)' },
  { label: 'Start with Hello World!', icon: GlobeAltIcon, color: 'var(--success)' },
  { label: 'Select a template', icon: Squares2X2Icon, color: 'var(--accent)' },
  { label: 'Upload your static files', icon: FolderOpenIcon, color: 'var(--warning)' },
];

const engine = (label, color) => ({ label, icon: CircleStackIcon, color });
export const databaseOptions = [
  {
    label: 'Create new database', icon: PlusIcon, items: [
      engine('PostgreSQL', '#336791'),
      engine('MySQL', '#00758f'),
      engine('MariaDB', '#c0765a'),
      engine('MongoDB', '#47a248'),
      'separator',
      engine('Redis', '#dc382d'),
      engine('Dragonfly', '#7b61ff'),
      engine('ClickHouse', '#e8b400'),
    ],
  },
  {
    label: 'Import existing data', icon: ArrowDownTrayIcon, items: [
      { label: 'Upload a dump file', icon: DocumentArrowUpIcon },
      { label: 'From S3-compatible storage', icon: CloudArrowDownIcon },
      { label: 'Migrate from a live database', icon: ArrowsRightLeftIcon },
    ],
  },
  'separator',
  { label: 'Restore from an Ignition backup', icon: ArchiveBoxArrowDownIcon, color: 'var(--accent)' },
  { label: 'Connect external database', icon: LinkIcon, color: 'var(--muted)' },
];

const provider = (label, color) => ({ label, icon: ServerIcon, color });
export const serverOptions = [
  { label: 'Connect with SSH', icon: KeyIcon, color: 'var(--accent)' },
  { label: 'Run install script', icon: CommandLineIcon, color: 'var(--success)' },
  {
    label: 'Provision from a cloud provider', icon: CloudIcon, items: [
      provider('Hetzner Cloud', '#d50c2d'),
      provider('DigitalOcean', '#0080ff'),
      provider('AWS EC2', '#ff9900'),
      provider('Vultr', '#007bfc'),
      provider('Linode', '#00a95c'),
    ],
  },
  'separator',
  { label: 'Use this machine', icon: ComputerDesktopIcon },
  { label: 'Add a dedicated build server', icon: WrenchScrewdriverIcon },
];

const template = (label, color) => ({ label, icon: Squares2X2Icon, color });
export const projectOptions = [
  { label: 'Empty project', icon: FolderPlusIcon, color: 'var(--accent)' },
  {
    label: 'Start from a template', icon: Squares2X2Icon, items: [
      template('Next.js + PostgreSQL', 'var(--text)'),
      template('Laravel + MySQL', '#ff2d20'),
      template('Django + Redis', '#0c4b33'),
      template('Rails + PostgreSQL', '#cc0000'),
      'separator',
      template('WordPress', '#21759b'),
      template('Ghost', '#738a94'),
      template('n8n', '#ea4b71'),
      { label: 'Browse all templates', icon: GlobeAltIcon },
    ],
  },
  { label: 'Import from Git repository', icon: CodeBracketIcon, items: gitSources },
  { label: 'From a Docker Compose file', icon: DocumentTextIcon },
  'separator',
  { label: 'Duplicate an existing project', icon: DocumentDuplicateIcon },
  { label: 'Import a project export', icon: ArrowDownTrayIcon },
];

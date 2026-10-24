import { ArchiveBoxArrowDownIcon, CloudIcon, CommandLineIcon, ComputerDesktopIcon, KeyIcon, WrenchScrewdriverIcon, ArrowDownTrayIcon, ArrowsRightLeftIcon, CircleStackIcon, CloudArrowDownIcon, DocumentArrowUpIcon, CloudArrowUpIcon, CodeBracketIcon, FolderOpenIcon, GlobeAltIcon, LinkIcon, PlusIcon, ServerIcon, Squares2X2Icon } from '@heroicons/react/20/solid';
import { ExclamationTriangleIcon, XCircleIcon } from '@heroicons/react/16/solid';
import { Button, GitHubIcon, GitLabIcon, Menu, Section, Sparkline, Status, cx } from '../../ui';
import { alerts, deployments, resources, stats } from './data';
import './Dashboard.css';

const deployOptions = [
  {
    label: 'From Git repository', icon: CodeBracketIcon, items: [
      { label: 'Continue with Artifacts', icon: CloudArrowUpIcon, color: '#f38020' },
      { label: 'Continue with GitHub', icon: GitHubIcon, color: 'var(--text)' },
      { label: 'Continue with GitLab', icon: GitLabIcon },
      { label: 'Continue with other source', icon: LinkIcon },
    ],
  },
  'separator',
  { label: 'Start with Hello World!', icon: GlobeAltIcon, color: 'var(--success)' },
  { label: 'Select a template', icon: Squares2X2Icon, color: 'var(--accent)' },
  { label: 'Upload your static files', icon: FolderOpenIcon, color: 'var(--warning)' },
];

const engine = (label, color) => ({ label, icon: CircleStackIcon, color });
const databaseOptions = [
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
const serverOptions = [
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

const deploymentTone = { success: 'success', building: 'progress', failed: 'danger' };
const deploymentLabel = { success: 'Success', building: 'Building', failed: 'Failed' };

export function Dashboard() {
  return <div className="dashboard">
    <div className="quick-actions">
      <Menu className="button primary" label="Deploy application" items={deployOptions} align="start"><CloudArrowUpIcon />Deploy application</Menu>
      <Button icon={PlusIcon}>New project</Button>
      <Menu className="button" label="New database" items={databaseOptions} align="start"><CircleStackIcon />New database</Menu>
      <Menu className="button" label="Add server" items={serverOptions} align="start"><ServerIcon />Add server</Menu>
    </div>

    {alerts.length > 0 && <div className="alerts" role="status">
      {alerts.map(alert => <div key={alert.title} className={cx('alert', alert.tone)}>
        {alert.tone === 'danger' ? <XCircleIcon /> : <ExclamationTriangleIcon />}
        <strong>{alert.title}</strong><span>{alert.detail}</span>
        <button className="alert-action">{alert.action}</button>
      </div>)}
    </div>}

    <div className="stats">
      {stats.map(stat => <div key={stat.label} className="stat">
        <span className="stat-label">{stat.label}</span>
        <span className="stat-value">{stat.value}</span>
        <span className="stat-detail">{stat.detail}</span>
      </div>)}
    </div>

    <Section title="Recent deployments" count={deployments.length} action="View all">
      <ul className="list">
        {deployments.map(d => <li key={d.app + d.commit}><button className="list-row deployment-row">
          <Status tone={deploymentTone[d.status]} label={deploymentLabel[d.status]} />
          <span className="row-name">{d.app}<span>{d.project}</span></span>
          <span className="commit"><code>{d.commit}</code><span>{d.message}</span></span>
          <span className="row-meta">{d.duration}</span>
          <span className="row-meta">{d.time}</span>
        </button></li>)}
      </ul>
    </Section>

    <Section title="Server resources" action="All servers">
      <div className="resources">
        {resources.map(r => <div key={r.label} className="resource">
          <span className="stat-label">{r.label}</span>
          <span className="resource-value">{r.value}</span>
          <span className="stat-detail">{r.detail}</span>
          <Sparkline data={r.data} max={r.max} label={r.label} format={r.format} />
        </div>)}
      </div>
    </Section>
  </div>;
}

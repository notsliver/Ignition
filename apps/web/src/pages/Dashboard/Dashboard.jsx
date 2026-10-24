import { CircleStackIcon, CloudArrowUpIcon, PlusIcon, ServerIcon } from '@heroicons/react/20/solid';
import { ExclamationTriangleIcon, XCircleIcon } from '@heroicons/react/16/solid';
import { Button, Section, Sparkline, Status, cx } from '../../ui';
import { alerts, deployments, resources, stats } from './data';
import './Dashboard.css';

const deploymentTone = { success: 'success', building: 'progress', failed: 'danger' };
const deploymentLabel = { success: 'Success', building: 'Building', failed: 'Failed' };

export function Dashboard() {
  return <div className="dashboard">
    <div className="quick-actions">
      <Button variant="primary" icon={CloudArrowUpIcon}>Deploy application</Button>
      <Button icon={PlusIcon}>New project</Button>
      <Button icon={CircleStackIcon}>New database</Button>
      <Button icon={ServerIcon}>Add server</Button>
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

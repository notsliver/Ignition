import { useEffect } from 'react';
import { EllipsisHorizontalIcon } from '@heroicons/react/16/solid';
import { Menu, Notice } from '../../ui';
import { api } from '../../api/client';
import { useQuery } from '../../api/hooks';
import { methodLabel } from './model';
import { Chips, ConnectionStatus, LastContact, RuntimeStatus, ServerWarnings, useFeedback, useServerActions } from './shared';
import { Overview } from './Overview';
import { ServerResources, ServerDeployments } from './Workloads';
import { Monitoring, ServerLogs, Diagnostics } from './Observability';
import { Docker, Storage, Networking } from './Runtime';
import { Maintenance, Tasks } from './Operations';
import { Access, Settings } from './Administration';
import { Automations } from '../automations/Automations';

const ServerAutomations = ({ server, onNavigate }) => <Automations scope="servers" server={server} onNavigate={onNavigate} />;

const pages = {
  Overview, Resources: ServerResources, Deployments: ServerDeployments, Monitoring, Logs: ServerLogs, Diagnostics,
  Docker, Storage, Networking, Maintenance, 'Tasks & Automation': Tasks, Automations: ServerAutomations, 'Access & Security': Access, Settings,
};

/**
 * A server's workspace: the same header on every page (identity, state, last contact, actions),
 * the warnings that matter everywhere, then the page. The server reloads every 5 seconds so
 * status changes show up wherever you are.
 */
export function ServerWorkspace({ server: initial, view, onNavigate, onServerChange, onRemoved, onOpenResource }) {
  const q = useQuery(() => api.server(initial.id), [initial.id], { pollMs: 5000 });
  const server = q.data?.server ?? initial;
  useEffect(() => { if (q.data?.server) onServerChange(q.data.server); }, [q.data]); // eslint-disable-line react-hooks/exhaustive-deps
  const [feedbackNode, setFeedback] = useFeedback();
  useEffect(() => setFeedback(null), [view]); // eslint-disable-line react-hooks/exhaustive-deps
  const changed = s => { if (s) { q.setData({ server: s }); onServerChange(s); } else q.refetch(); };
  const actions = useServerActions(server, { onChanged: changed, onNavigate, setFeedback, onRemoved });
  const Page = pages[view] ?? Overview;
  const engine = server.info?.engine;

  return <div className="rp-page sv-page">
    <div className="sv-header">
      <div className="sv-header-facts">
        <ConnectionStatus server={server} withText />
        <span className="sv-fact"><span>Docker</span><RuntimeStatus server={server} />{engine?.version && <small>{engine.version}</small>}</span>
        <span className="sv-fact"><span>{methodLabel[server.connectionMethod]}</span>{!server.isLocal && <small className="mono">{server.endpoint}</small>}</span>
        <span className="sv-fact"><span>Last contact</span><LastContact server={server} /></span>
        <Chips server={server} />
      </div>
      {actions.items.length > 0 && <Menu label={`Actions for ${server.name}`} items={actions.items} className="button"><EllipsisHorizontalIcon />Actions</Menu>}
    </div>
    {q.error && <Notice tone="warning" title="Couldn't refresh this server">{q.error.message} The details below may be out of date.</Notice>}
    {feedbackNode}
    <ServerWarnings server={server} onNavigate={view === 'Overview' ? onNavigate : null} skip={view === 'Overview' ? [] : ['engine', 'intermittent', 'clock_skew', 'cert_expiring']} />
    <Page key={view} server={server} onNavigate={onNavigate} onChanged={changed} setFeedback={setFeedback} actions={actions} onOpenResource={onOpenResource} onRemoved={onRemoved} />
    {actions.dialogs}
  </div>;
}

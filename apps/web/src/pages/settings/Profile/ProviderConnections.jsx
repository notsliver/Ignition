import { useEffect, useRef, useState } from 'react';
import { CheckCircleIcon, EllipsisVerticalIcon, LinkIcon } from '@heroicons/react/16/solid';
import { Button, Dialog, Menu, Notice } from '../../../ui';
import { api } from '../../../api/client';
import { useQuery } from '../../../api/hooks';
import { can, useSession } from '../../../app/AuthGate';
import { useTeam } from '../../../app/Teams';
import { useAccountAction } from './AccountConfirm';
import '../../admin/Integrations.css';
import { GitHubIcon, GitLabIcon, BitbucketIcon } from '../../../ui/icons/brands';
const providerIcons = {github: GitHubIcon, gitlab: GitLabIcon, bitbucket: BitbucketIcon};

export function AccountProviderConnections({children}) {
  const connection = useProviderSources();
  const {query, sources, user, team, pending, error, setError, connect, changed, remove, setRemove} = connection;
  const session = useSession();
  const action = useAccountAction();
  const [identityRemoval, setIdentityRemoval] = useState(null);
  const [consent, setConsent] = useState(null);
  const providers = query.data?.providers.filter(p => p.oauth && (p.signIn || p.identity || p.sources || sources.data?.sources.some(s => s.oauthProvider === p.provider))) ?? [];
  const authorize = provider => provider.provider === 'github' && provider.sourceScopes.includes('repo') ? setConsent(provider) : connect(provider);
  return <>
    {(error || query.error || sources.error || action.error) && <Notice tone="danger" title="Couldn't load or change integrations">{error || query.error?.message || sources.error?.message || action.error} <Button onClick={() => {query.refetch(); sources.refetch();}}>Retry</Button></Notice>}
    {!query.data && !query.error && <p className="account-muted">Loading integrations…</p>}
    <div className="account-integrations-grid">
      {providers.map(provider => {
        const Icon = providerIcons[provider.provider] || LinkIcon;
        const connections = sources.data?.sources.filter(s => s.oauthProvider === provider.provider) ?? [];
        const own = connections.find(s => s.connectionOwnerId === user.id);
        const username = provider.identity?.username || own?.account;
        const items = [];
        if (provider.identity) items.push({label: 'Disconnect sign-in', danger: true, onSelect: () => {action.clearError(); setIdentityRemoval(provider);}});
        if (own && provider.sources && can(user, 'sources.use') && !pending) items.push({label: 'Reconnect repositories', onSelect: () => authorize(provider)});
        for (const source of connections) if ((source.connectionOwnerId === user.id || can(user, 'sources.manage')) && can(user, 'sources.use') && !pending) items.push({label: connections.length > 1 ? `Disconnect repositories · ${source.account}` : 'Disconnect repositories', danger: true, onSelect: () => {setError(''); setRemove(source);}});
        return <article className="account-integration provider-integration settings-card" key={provider.provider}>
          <header><span className="account-integration-title"><Icon /><strong>{provider.label}</strong></span>
            {items.length > 0 ? <Menu trigger={EllipsisVerticalIcon} label={`Manage ${provider.label}`} items={items} /> : <Button disabled={!!pending || action.pending || (!provider.signIn && (!provider.sources || !can(user, 'sources.use')))} onClick={() => provider.signIn ? action.run(async () => {const out = await api.startProviderAccount(provider.provider, 'connect', location.pathname); location.assign(out.url);}) : authorize(provider)}>Connect</Button>}
          </header>
          <div className="provider-integration-details">
            {username ? <p className="provider-integration-line"><CheckCircleIcon /><span>Connected as <strong>{username}</strong></span></p> : <p>Connect your {provider.label} account to {provider.signIn ? 'sign in to Ignition' : `access repositories for ${team.name}`}.</p>}
            {provider.identity && <p className={`provider-integration-line${!provider.signIn || provider.providerChanged ? ' inactive' : ''}`}><CheckCircleIcon /><span>{provider.providerChanged ? 'Provider changed. Reconnect sign-in.' : provider.signIn ? `Use ${provider.label} to sign in to Ignition` : 'Account linked · sign-in currently disabled'}</span></p>}
            {!provider.identity && provider.signIn && connections.length > 0 && <div className="provider-integration-line"><LinkIcon /><Button variant="ghost" disabled={action.pending} onClick={() => action.run(async () => {const out = await api.startProviderAccount(provider.provider, 'connect', location.pathname); location.assign(out.url);})}>Enable {provider.label} sign-in</Button></div>}
            {connections.map(source => <p className={`provider-integration-line${source.status !== 'connected' ? ' inactive' : ''}`} key={source.id}><CheckCircleIcon /><span>Repositories {source.status === 'connected' ? 'connected' : source.status} for <strong>{team.name}</strong>{source.connectionOwnerId !== user.id && <> · {source.account}</>}</span></p>)}
            {provider.sources && !own && (provider.identity || connections.length > 0) && <div className="provider-integration-line"><LinkIcon /><Button variant="ghost" disabled={!!pending || !can(user, 'sources.use')} onClick={() => authorize(provider)}>Connect repositories{connections.length > 0 ? ' with your account' : ''}</Button></div>}
            {pending === provider.provider && <div className="provider-integration-line"><span>Waiting for authorization…</span><Button onClick={connection.cancel}>Cancel</Button></div>}
          </div>
        </article>;
      })}
      {children}
    </div>
    {query.data && !providers.length && !children && <p className="account-muted">No account integrations are available yet.</p>}
    <Dialog open={!!consent} title="Connect GitHub repositories" onClose={() => setConsent(null)} description="This GitHub OAuth app requests repo access, including read and write access to private repositories. Review the permissions on GitHub before authorizing."
      footer={<><Button onClick={() => setConsent(null)}>Cancel</Button><Button onClick={() => {const provider = consent; setConsent(null); connect(provider);}}>Continue to GitHub</Button></>} />
    <SourceDisconnect connection={connection} />
    <Dialog open={!!identityRemoval} onClose={() => setIdentityRemoval(null)} title={`Disconnect ${identityRemoval?.label ?? 'provider'} sign-in?`} description="Keep another usable sign-in method. Team repository access is managed independently."
      footer={<><Button onClick={() => setIdentityRemoval(null)}>Cancel</Button><Button disabled={action.pending} onClick={() => action.run(async () => {await api.disconnectProviderAccount(identityRemoval.provider); setIdentityRemoval(null); await query.refetch(); await session.refresh();})}>Disconnect</Button></>}>
      {action.error && <Notice tone="danger" title="Couldn't disconnect">{action.error}</Notice>}
    </Dialog>
    {action.confirmation}
  </>;
}

/** OAuth happens in an isolated popup so deployment forms keep their unsaved values. */
function useProviderSources(onChanged) {
  const {user} = useSession();
  const {team} = useTeam();
  const query = useQuery(api.availableIntegrations, []);
  const sources = useQuery(() => api.sources({kind: 'git'}), [team.id]);
  const [pending, setPending] = useState('');
  const [error, setError] = useState('');
  const [remove, setRemove] = useState(null);
  const timer = useRef(null);
  const popup = useRef(null);
  useEffect(() => () => {clearInterval(timer.current); popup.current?.close();}, []);
  const providers = query.data?.providers.filter(p => p.oauth && p.sources) ?? [];
  const connections = sources.data?.sources.filter(s => s.oauthProvider) ?? [];
  async function changed() {
    await sources.refetch(); onChanged?.();
    window.dispatchEvent(new Event('ignition:sources-changed'));
  }
  async function connect(provider) {
    setError('');
    const opened = window.open('about:blank', '_blank', 'popup,width=680,height=760');
    if (!opened) return setError('Allow popups for Ignition to authorize the provider. Your deployment form will stay open.');
    opened.opener = null; popup.current = opened; setPending(provider.provider);
    try {
      const out = await api.connectProviderSource(team.id, provider.provider, `/teams/${team.slug || team.id}/settings/profile`);
      opened.location.replace(out.url);
      const started = Date.now();
      timer.current = setInterval(async () => {
        if (opened.closed || Date.now() - started > 10 * 60 * 1000) {
          clearInterval(timer.current); setPending(''); popup.current = null;
          await changed();
        }
      }, 750);
    } catch (err) {opened.close(); popup.current = null; setPending(''); setError(err.message);}
  }
  const cancel = () => {popup.current?.close(); clearInterval(timer.current); popup.current = null; setPending(''); changed();};
  return {user, team, query, sources, pending, error, setError, providers, connections, connect, changed, remove, setRemove, cancel};
}

export function SourceProviderConnections({compact = false, onChanged}) {
  const connection = useProviderSources(onChanged);
  const {user, team, query, sources, pending, error, providers, connections, connect, changed, remove, setRemove, setError} = connection;
  return <div className={compact ? '' : 'settings-card'}>
    <div className="source-connect-body">
      {!compact && <p className="account-muted">Connections are shared with <strong>{team.name}</strong>. Your sign-in integration does not grant repository access. Tokens stay on the server.</p>}
      {(error || query.error || sources.error) && <Notice tone="danger" title="Couldn't load or change the connection">{error || query.error?.message || sources.error?.message}</Notice>}
      <div className="source-connect-list">{providers.map(provider => <Button key={provider.provider} type="button" icon={LinkIcon} title={provider.permissionNote} disabled={!!pending || !can(user, 'sources.use')} onClick={() => connect(provider)}>{pending === provider.provider ? 'Waiting for authorization…' : `Connect ${provider.label}`}</Button>)}</div>
      {query.data && !providers.length && <p className="account-muted">No instance OAuth source providers are enabled. An instance administrator can configure them under Integrations.</p>}
      {!can(user, 'sources.use') && <p className="account-muted">Connecting a provider requires Use sources permission.</p>}
      {!compact && providers.some(p => p.provider === 'github' && p.sourceScopes.includes('repo')) && <Notice tone="warning">The GitHub application requests repo access, which includes read and write access to private repositories. Review the provider’s authorization screen.</Notice>}
      {pending && <Button type="button" onClick={connection.cancel}>Cancel authorization</Button>}
    </div>
    {!compact && connections.map(source => <div className="source-connection-row" key={source.id}><span><strong>{source.name}</strong><small>{source.oauthProvider} · {source.account} · {source.status}</small></span>
      {source.connectionOwnerId === user.id && <Button type="button" disabled={!!pending} onClick={() => {const provider = providers.find(p => p.provider === source.oauthProvider); if (provider) connect(provider);}}>Reconnect</Button>}
      {(source.connectionOwnerId === user.id || can(user, 'sources.manage')) && <Button type="button" disabled={!!pending} onClick={() => setRemove(source)}>Disconnect</Button>}
    </div>)}
    <SourceDisconnect connection={connection} />
  </div>;
}

function SourceDisconnect({connection}) {
  const {remove, setRemove, team, changed, error, setError} = connection;
  return (
<Dialog open={!!remove} title={`Disconnect ${remove?.name ?? 'source'}?`} description="Resources using this connection must switch to another source first." onClose={() => setRemove(null)}
      footer={<><Button type="button" onClick={() => setRemove(null)}>Cancel</Button><Button type="button" onClick={async () => {try {await api.disconnectProviderSource(team.id, remove.id); setRemove(null); await changed();} catch(err){setError(err.message);}}}>Disconnect</Button></>}>
      {error && <Notice tone="danger" title="Couldn't disconnect">{error}</Notice>}
    </Dialog>
  );
}

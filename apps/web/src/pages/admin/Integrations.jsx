import { useEffect, useState } from 'react';
import { ClipboardDocumentIcon, Cog6ToothIcon, CubeIcon } from '@heroicons/react/16/solid';
import { Button, Checkbox, Dialog, FormField, Notice, Select, Status, Switch, TextInput } from '../../ui';
import { api } from '../../api/client';
import { useQuery } from '../../api/hooks';
import { SettingsField, SettingsGroup } from '../settings/SettingsField';
import { useAccountAction } from '../settings/Profile/AccountConfirm';
import '../settings/settings.css';
import './Integrations.css';
import { GitHubIcon, GitLabIcon, BitbucketIcon, DockerIcon, GitIcon } from '../../ui/icons/brands';

const providerIcons = {github: GitHubIcon, gitlab: GitLabIcon, bitbucket: BitbucketIcon, git: GitIcon, dockerhub: DockerIcon, ghcr: GitHubIcon, 'gitlab-registry': GitLabIcon, registry: CubeIcon};
function ProviderIcon({provider}) {
  const Icon = providerIcons[provider] || CubeIcon;
  return <Icon />;
}

export function Integrations() {
  const query = useQuery(api.integrationSettings, []);
  const action = useAccountAction();
  const [mode, setMode] = useState(null);
  const [editing, setEditing] = useState(null);
  const [message, setMessage] = useState('');
  useEffect(() => { if (query.data) setMode(query.data.sourceMode); }, [query.data]);
  const save = async (provider, body) => {
    const out = await api.saveIntegrationSettings(provider, body);
    query.setData(out); setEditing(null); setMessage('Integration settings saved.');
    window.dispatchEvent(new CustomEvent('ignition:source-policy', {detail: {mode: out.sourceMode}}));
  };
  if (!query.data) return <div className="settings-page">{query.error ? <Notice tone="danger" title="Couldn't load integrations">{query.error.message} <Button onClick={query.refetch}>Retry</Button></Notice> : <p className="account-muted">Loading…</p>}</div>;
  return <div className="settings-page integrations-page">
    <p className="integrations-intro">Configure the applications your instance uses. Customers authorize their own accounts; OAuth never shares an administrator’s personal token across teams.</p>
    {message && <Notice tone="success" title={message} />}
    {action.error && <Notice tone="danger" title="Couldn't save integration settings">{action.error}</Notice>}
    <SettingsGroup title="Team source experience">
      <SettingsField label="Connection mode" hint="Managed hosting hides both Sources pages from teams and blocks manual credential changes through the API.">
        <Select label="Connection mode" value={mode} onChange={setMode} options={[{value: 'self-managed', label: 'Developer · team-managed sources'}, {value: 'managed', label: 'Hosting · instance OAuth connections'}]} />
      </SettingsField>
      <div className="integration-policy-note">{mode === 'managed'
        ? 'Teams connect approved Git providers from Profile or the deployment picker. Existing team credentials remain usable; instance administrators can provision Git/registry credentials for a team. Deployments stay team-scoped.'
        : 'Teams with Manage sources permission can add approved tokens, deploy keys and registry credentials. OAuth connections are also available for enabled providers.'}</div>
      <div className="integration-save"><Button variant="primary" disabled={mode === query.data.sourceMode || action.pending} onClick={() => action.run(() => save('source-policy', {mode}))}>Save connection mode</Button></div>
    </SettingsGroup>
    <section className="settings-group">
      <h2>Providers</h2><p>Separate application credentials and permissions for account sign-in and deployment access.</p>
      <div className="settings-card">
        {query.data.providers.map(provider => <div className="integration-provider-row" key={provider.config.provider}>
          <ProviderIcon provider={provider.config.provider} /><span className="integration-provider-main"><strong>{provider.config.label}</strong><small>{[provider.config.signInEnabled && 'Account sign-in', provider.config.sourcesEnabled && 'Repository OAuth', provider.config.manualAllowed && 'Manual credentials'].filter(Boolean).join(' · ') || 'No connection methods enabled'}</small></span>
          <Status tone={provider.config.enabled ? 'success' : 'neutral'}>{provider.config.enabled ? 'Enabled' : 'Disabled'}</Status>
          <Button icon={Cog6ToothIcon} onClick={() => {action.clearError(); setEditing(provider);}}>Configure</Button>
        </div>)}
      </div>
    </section>
    <Notice tone="info">Ignition permissions control who can connect and use a source. Provider scopes control what the remote OAuth app can access. GitHub private-repository OAuth includes write access; Git and registries continue to use deploy keys or pull credentials.</Notice>
    {editing && <ProviderEditor key={editing.config.provider} provider={editing} pending={action.pending} error={action.error} onClose={() => setEditing(null)} onSave={body => action.run(() => save(editing.config.provider, body))} />}
    {action.confirmation}
  </div>;
}

function ProviderEditor({provider, pending, error, onClose, onSave}) {
  const [config, setConfig] = useState(() => structuredClone(provider.config));
  const [loginSecret, setLoginSecret] = useState('');
  const [sourceSecret, setSourceSecret] = useState('');
  const [copied, setCopied] = useState(false);
  const set = key => value => setConfig(c => ({...c, [key]: value}));
  const client = (which, key, value) => setConfig(c => ({...c, [which]: {...c[which], [key]: value}}));
  const isGitHub = config.provider === 'github';
  return <Dialog open onClose={onClose} title={`Configure ${config.label}`} description="Client secrets are encrypted on the server and never returned. Saving a new repository client or scope requires affected sources to reconnect."
    footer={<><Button disabled={pending} onClick={onClose}>Cancel</Button><Button variant="primary" form="provider-config" type="submit" disabled={pending}>{pending ? 'Saving…' : 'Save provider'}</Button></>}>
    <form id="provider-config" className="integration-editor" onSubmit={e => {e.preventDefault(); onSave({config, loginSecret, sourceSecret});}}>
      {error && <Notice tone="danger" title="Couldn't save provider">{error}</Notice>}
      <FormField label="Display name" htmlFor="integration-label"><TextInput id="integration-label" value={config.label} maxLength={80} onChange={e => set('label')(e.target.value)} /></FormField>
      <Toggle label="Enable this provider" checked={config.enabled} onChange={set('enabled')} /><p className="account-muted">Disabling a provider stops future sign-ins and credential use. Running workloads are not stopped.</p>
      <Toggle label="Allow team-managed credentials" checked={config.manualAllowed} onChange={set('manualAllowed')} />
      <p className="account-muted">Manual credential entry also requires developer mode and the team’s Manage sources permission. Instance administrators can always provision team credentials.</p>
      {provider.oauthSupported && <>
        <FormField label={config.provider === 'github' ? 'GitHub or Enterprise origin' : 'Provider origin'} htmlFor="integration-origin"><TextInput id="integration-origin" value={config.baseUrl} disabled={config.provider === 'bitbucket'} onChange={e => set('baseUrl')(e.target.value)} /></FormField>
        <FormField label="OAuth callback URL" htmlFor="integration-callback"><div className="integration-callback"><TextInput id="integration-callback" value={provider.callbackUrl} readOnly /><Button type="button" icon={ClipboardDocumentIcon} onClick={() => {navigator.clipboard?.writeText(provider.callbackUrl); setCopied(true);}}>{copied ? 'Copied' : 'Copy'}</Button></div></FormField>
        <fieldset><legend>Account sign-in</legend><p className="account-muted">Use a dedicated OAuth application for sign-in so repository grants cannot be inherited.</p>
          <Toggle label="Allow account sign-in and linking" checked={config.signInEnabled} onChange={set('signInEnabled')} />
          <FormField label="Sign-in client ID" htmlFor="integration-login-id"><TextInput id="integration-login-id" value={config.login.clientId} autoComplete="off" onChange={e => client('login', 'clientId', e.target.value)} /></FormField>
          <FormField label={config.login.hasSecret ? 'Replace sign-in secret (optional)' : 'Sign-in client secret'} htmlFor="integration-login-secret"><TextInput id="integration-login-secret" type="password" autoComplete="new-password" value={loginSecret} onChange={e => setLoginSecret(e.target.value)} /></FormField>
          <span className="integration-scopes">Permissions: {config.login.scopes.join(', ')}</span>
          <CheckOption label="Allow verified provider accounts to register" checked={config.allowSignup} onChange={set('allowSignup')} />
          <p className="account-muted">New accounts use the instance’s SSO default role and registration policy. Existing accounts must explicitly link in Profile; a matching email never links an account automatically. Organization SSO-only policy still applies.</p>
        </fieldset>
        <fieldset><legend>Repository connections</legend>
          <Toggle label="Allow teams to connect repositories via OAuth" checked={config.sourcesEnabled} onChange={set('sourcesEnabled')} />
          <FormField label="Repository client ID" htmlFor="integration-source-id"><TextInput id="integration-source-id" value={config.repositories.clientId} autoComplete="off" onChange={e => client('repositories', 'clientId', e.target.value)} /></FormField>
          <FormField label={config.repositories.hasSecret ? 'Replace repository secret (optional)' : 'Repository client secret'} htmlFor="integration-source-secret"><TextInput id="integration-source-secret" type="password" autoComplete="new-password" value={sourceSecret} onChange={e => setSourceSecret(e.target.value)} /></FormField>
          {isGitHub && <CheckOption label="Include private repositories (GitHub repo grants read and write)" checked={config.repositories.scopes.includes('repo')} onChange={on => client('repositories', 'scopes', on ? ['read:user', 'repo'] : ['read:user'])} />}
          <span className="integration-scopes">Permissions: {config.repositories.scopes.join(', ')}</span>
          <p className="account-muted">Connecting requires Use sources permission (Developer by default). Each connection belongs to its authorizing user and team; credentials are never sent to browsers.</p>
        </fieldset>
      </>}
      <Notice tone={isGitHub && config.repositories.scopes.includes('repo') ? 'warning' : 'info'}>{provider.permissionNote}</Notice>
    </form>
  </Dialog>;
}

function Toggle(props){return <div className="integration-toggle"><span>{props.label}</span><Switch {...props}/></div>}
function CheckOption(props){return <div className="integration-check"><Checkbox {...props}/><span>{props.label}</span></div>}

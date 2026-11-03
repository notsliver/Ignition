// The one place the dashboard talks to the Ignition API.
// Errors follow the API's shape: { error: { code, message, fields, requestId } }.

export class ApiError extends Error {
  constructor(status, body) {
    const e = body?.error ?? {};
    super(e.message ?? `Request failed (${status})`);
    this.status = status;
    this.code = e.code ?? 'unknown';
    this.fields = e.fields ?? {};
    this.requestId = e.requestId;
    this.body = body; // some errors carry details next to `error`, e.g. a presented certificate fingerprint
  }
}

const listeners = new Set();
/** Called with no arguments whenever the API says the session has ended. */
export const onUnauthenticated = fn => { listeners.add(fn); return () => listeners.delete(fn); };

let activeTeamId = '';
let activeTeamCode = ''; // the short public ID, for browser URLs
let authorizationVersion = '';
const teamAccessListeners = new Set();
export const onTeamAccessChanged = fn => {teamAccessListeners.add(fn);return ()=>teamAccessListeners.delete(fn);};
let teamRequests = new AbortController();
export const getTeamSignal = () => teamRequests.signal;
export const getActiveTeamId = () => activeTeamId;
export function setActiveTeamId(id, version, code) {
  activeTeamCode = code || id;
  authorizationVersion = version === undefined ? '' : String(version);
  if (id === activeTeamId) return;
  teamRequests.abort(); teamRequests = new AbortController(); activeTeamId = id;
}
export function teamApiUrl(path) {
  const relative = path.replace(/^\/api/, '');
  const global = /^\/(platform|auth|account|setup|settings|users|invites|teams|team-invitations)(\/|$|\?)/.test(relative) || /^\/(system|permissions)(\?|$)/.test(relative);
  if (global) return `/api${relative}`;
  if (!activeTeamId) throw new Error('Select a team first.');
  return `/api/teams/${activeTeamId}${relative}`;
}
export const teamPath = path => `/teams/${activeTeamCode || activeTeamId}${path}`;

async function request(method, path, body) {
  let res;
  const url = teamApiUrl(path), signal = teamRequests.signal;
  try {
    res = await fetch(url, {
      signal,
      method,
      credentials: 'same-origin',
      headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  } catch (error) {
    if (signal.aborted) throw new DOMException('Team changed', 'AbortError');
    throw new ApiError(0, { error: { code: 'offline', message: 'Can\'t reach the Ignition API. Is it running?' } });
  }
  if (signal.aborted) throw new DOMException('Team changed', 'AbortError');
  const version=res.headers.get('X-Ignition-Authorization-Version');
  if(authorizationVersion && version && res.headers.get('X-Ignition-Team')===activeTeamId && version!==authorizationVersion){teamAccessListeners.forEach(fn=>fn());throw new DOMException('Team permissions changed','AbortError');}
  if (res.status === 204) return null;
  const data = await res.json().catch(() => null);
  if (!res.ok) {
    const err = new ApiError(res.status, data);
    if(err.code==='team_access_lost')teamAccessListeners.forEach(fn=>fn());
    if (err.code === 'maintenance') window.dispatchEvent(new CustomEvent('ignition:maintenance', { detail: { maintenance: true, maintenanceMessage: err.message } }));
    if (res.status === 401 && !path.startsWith('/auth/') && !path.startsWith('/setup')) listeners.forEach(fn => fn(err));
    throw err;
  }
  return data;
}

/** Sends a file as the raw request body (uploads up to several GB without multipart). */
async function upload(path, file) {
  let res;
  const url=teamApiUrl(path),signal=teamRequests.signal;
  try {
    res = await fetch(url, { signal, method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/octet-stream' }, body: file });
  } catch {
    throw new ApiError(0, { error: { code: 'offline', message: 'The upload was interrupted. Check your connection and try again.' } });
  }
  const data = await res.json().catch(() => null);
  if (!res.ok) throw new ApiError(res.status, data);
  return data;
}

const q = params => {
  const s = new URLSearchParams(Object.entries(params).filter(([, v]) => v !== undefined && v !== '' && v !== null)).toString();
  return s ? `?${s}` : '';
};

export const api = {
  teamResource: (team,id) => request('GET',`/teams/${team}/resources/${id}`),
  teamProject: (team,id) => request('GET',`/teams/${team}/projects/${id}`),
  teamInvitation: token => request('GET',`/team-invitations/${token}`),
  registerTeamInvitation: (token,input) => request('POST',`/team-invitations/${token}/register`,input),
  groupResource: (id,projectId) => request('PUT',`/resources/${id}/project`,{projectId:projectId || null}),
  platformCapacity: team => request('GET',`/platform/capacity${team ? `?team=${team}` : ''}`),
  platformTeams: () => request('GET', '/platform/teams'),
  products: () => request('GET', '/platform/products'),
  createProduct: body => request('POST', '/platform/products', body),
  saveProduct: (id, body) => request('PUT', `/platform/products/${id}`, body),
  deleteProduct: id => request('DELETE', `/platform/products/${id}`),
  duplicateProduct: id => request('POST', `/platform/products/${id}/duplicate`),
  productAvailability: (id, params = {}) => request('GET', `/platform/products/${id}/availability${q(params)}`),
  services: (params = {}) => request('GET', `/platform/services${q(params)}`),
  provisionService: body => request('POST', '/platform/services', body),
  serviceAction: (id, body) => request('POST', `/platform/services/${id}/actions`, body),
  updateService: (id, body) => request('PATCH', `/platform/services/${id}`, body),
  terminateService: id => request('DELETE', `/platform/services/${id}?confirm=true`),
  serverAllocations: id => request('GET', `/servers/${id}/allocations`),
  addServerAllocations: (id, body) => request('POST', `/servers/${id}/allocations`, body),
  deleteServerAllocations: (id, ids) => request('POST', `/servers/${id}/allocations/delete`, { ids }),
  updateServerAllocation: (id, allocation, body) => request('PATCH', `/servers/${id}/allocations/${allocation}`, body),
  setServerVisibility: (id, visible) => request('PUT', `/servers/${id}/visibility`, { visible }),
  instanceLimits: () => request('GET', '/settings/limits'),
  saveInstanceLimits: limits => request('PUT', '/settings/limits', limits),
  quotas: () => request('GET', '/settings/quotas'),
  saveQuotas: quotas => request('PUT', '/settings/quotas', quotas),
  teamUsage: range => request('GET', `/usage${q({ range })}`),
  assignTeamCapacity: input => request('POST','/platform/capacity',input),
  teams: () => request('GET', '/teams'),
  createTeam: name => request('POST', '/teams', {name}),
  team: id => request('GET', `/teams/${id}`),
  updateTeam: (id, input) => request('PATCH', `/teams/${id}`, input),
  teamMembers: id => request('GET', `/teams/${id}/members`),
  setTeamMember: (team,id,input) => request('PUT', `/teams/${team}/members/${id}`,input),
  removeTeamMember: (team,id) => request('DELETE', `/teams/${team}/members/${id}`),
  teamInvitations: id => request('GET', `/teams/${id}/invitations`),
  inviteTeamMember: (id,input) => request('POST', `/teams/${id}/invitations`,input),
  revokeTeamInvitation: (team,id) => request('DELETE', `/teams/${team}/invitations/${id}`),
  acceptTeamInvitation: token => request('POST', `/team-invitations/${token}/accept`),
  teamEnvironments: () => request('GET', `/teams/${activeTeamId}/environments`),
  projectAssignments: id => request('GET', `/projects/${id}/assignments`),
  setProjectAssignment: (id,input) => request('PUT', `/projects/${id}/assignments`,input),
  setupStatus: () => request('GET', '/setup'),
  setup: input => request('POST', '/setup', input),
  completeSetup: () => request('POST', '/setup/complete'),
  login: (email, password) => request('POST', '/auth/login', { email, password }),
  logout: () => request('POST', '/auth/logout'),
  me: () => request('GET', '/auth/me'),
  authMethods: () => request('GET', '/auth/methods'),
  mfaVerify: (ticket, code) => request('POST', '/auth/mfa', { ticket, code }),
  mfaSetupTicket: ticket => request('POST', '/auth/mfa/setup', { ticket }),
  mfaConfirmTicket: (ticket, code) => request('POST', '/auth/mfa/setup/confirm', { ticket, code }),
  ssoStartUrl: '/api/auth/oidc/start',
  uploadAvatar: file => upload("/account/avatar", file),
  removeAvatar: () => request("DELETE", "/account/avatar"),
  uploadTeamIcon: (id, file) => upload(`/teams/${id}/icon`, file),
  removeTeamIcon: id => request("DELETE", `/teams/${id}/icon`),
  integrationSettings: () => request('GET', '/settings/integrations'),
  saveIntegrationSettings: (provider, body) => request('PUT', `/settings/integrations/${provider}`, body),
  availableIntegrations: () => request('GET', '/account/integrations/providers'),
  startProviderAccount: (provider, action, returnTo) => request('POST', `/account/integrations/providers/${provider}/${action}`, {returnTo}),
  disconnectProviderAccount: provider => request('DELETE', `/account/integrations/providers/${provider}`),
  connectProviderSource: (team, provider, returnTo) => request('POST', `/teams/${team}/integrations/${provider}/connect`, {returnTo}),
  disconnectProviderSource: (team, id) => request('DELETE', `/teams/${team}/integrations/connections/${id}`),
  accountProfile: () => request('GET', '/account/profile'),
  updateAccountProfile: input => request('PATCH', '/account/profile', input),
  deleteAccount: email => request('DELETE', '/account/profile', {email}),
  reauthenticate: input => request('POST', '/account/reauth', input),
  changeAccountPassword: password => request('PUT', '/account/password', {password}),
  accountIntegrations: () => request('GET', '/account/integrations'),
  disconnectAccountSSO: () => request('DELETE', '/account/integrations/sso'),
  startAccountSSO: (action, returnTo) => request('POST', `/account/integrations/sso/${action}`, {returnTo}),
  accountSessions: () => request('GET', '/account/sessions'),
  revokeAccountSession: id => request('DELETE', `/account/sessions/${id}`),
  accountPasskeys: () => request('GET', '/account/passkeys'),
  removeAccountPasskey: id => request('DELETE', `/account/passkeys/${id}`),
  beginPasskeyRegistration: name => request('POST', '/account/passkeys/register/begin', {name}),
  finishPasskeyRegistration: input => request('POST', '/account/passkeys/register/finish', input),
  beginPasskeyReauth: () => request('POST', '/account/passkeys/reauth/begin'),
  finishPasskeyReauth: input => request('POST', '/account/passkeys/reauth/finish', input),
  beginPasskeyLogin: () => request('POST', '/auth/passkeys/begin'),
  finishPasskeyLogin: input => request('POST', '/auth/passkeys/finish', input),
  accountSecurity: () => request('GET', '/account/security'),
  savePreferences: prefs => request('PUT', '/account/preferences', prefs),
  accountMfaSetup: () => request('POST', '/account/mfa/setup'),
  accountMfaEnable: code => request('POST', '/account/mfa/enable', { code }),
  accountMfaDisable: code => request('POST', '/account/mfa/disable', { code }),
  accountRecoveryCodes: code => request('POST', '/account/mfa/recovery-codes', { code }),
  permissions: () => request('GET', '/permissions'),
  security: () => request('GET', '/settings/security'),
  saveSecurity: policy => request('PUT', '/settings/security', policy),
  revokeAllSessions: () => request('POST', '/settings/security/revoke-sessions'),
  saveOidc: config => request('PUT', '/settings/security/oidc', config),
  uploadSSOIcon: file => upload('/settings/security/oidc/icon', file),
  removeSSOIcon: () => request('DELETE', '/settings/security/oidc/icon'),
  testOidc: config => request('POST', '/settings/security/oidc/test', config),
  deleteOidc: () => request('DELETE', '/settings/security/oidc'),
  resetUserMfa: id => request('DELETE', `/users/${id}/mfa`),
  setUserPermissions: (id, overrides) => request('PUT', `/users/${id}/permissions`, overrides),
  system: () => request('GET', '/system'),

  projects: (params = {}) => request('GET', `/projects${q(params)}`),
  project: id => request('GET', `/projects/${id}`),
  createProject: input => request('POST', '/projects', input),
  updateProject: (id, patch) => request('PATCH', `/projects/${id}`, patch),
  deleteProject: id => request('DELETE', `/projects/${id}`),
  projectDeployments: (id, params = {}) => request('GET', `/projects/${id}/deployments${q(params)}`),
  projectMetrics: (id, params = {}) => request('GET', `/projects/${id}/metrics${q(params)}`),
  projectLogs: (id, params = {}) => request('GET', `/projects/${id}/logs${q(params)}`),
  projectActivity: id => request('GET', `/projects/${id}/activity`),
  environments: id => request('GET', `/projects/${id}/environments`),
  createEnvironment: (id, name) => request('POST', `/projects/${id}/environments`, { name }),
  orderEnvironments: (id, ids) => request('PUT', `/projects/${id}/environments/order`, { ids }),
  renameEnvironment: (id, name) => request('PATCH', `/environments/${id}`, { name }),
  deleteEnvironment: id => request('DELETE', `/environments/${id}`),
  setFavorite: (id, on) => request(on ? 'PUT' : 'DELETE', `/projects/${id}/favorite`),

  allResources: (params = {}) => request('GET', `/resources${q(params)}`),
  resources: (projectId, params = {}) => request('GET', `${projectId ? `/projects/${projectId}` : ''}/resources${q(params)}`),
  resource: id => request('GET', `/resources/${id}`),
  createResource: (projectId, input) => request('POST', `${projectId ? `/projects/${projectId}` : ''}/resources`, input),
  updateResource: (id, patch) => request('PATCH', `/resources/${id}`, patch),
  deleteResource: (id, confirm, removeVolumes = false) => request('DELETE', `/resources/${id}${q({ confirm, volumes: removeVolumes ? 'true' : '' })}`),
  resourceAction: (id, action) => request('POST', `/resources/${id}/actions`, { action }),
  env: id => request('GET', `/resources/${id}/env`),
  putEnv: (id, variables) => request('PUT', `/resources/${id}/env`, { variables }),
  revealEnv: (id, key) => request('POST', `/resources/${id}/env/${encodeURIComponent(key)}/reveal`),
  containerLogs: (id, params = {}) => request('GET', `/resources/${id}/logs${q(params)}`),
  logDeployments: id => request('GET', `/resources/${id}/logs/deployments`),
  stats: id => request('GET', `/resources/${id}/stats`),
  metrics: (id, range) => request('GET', `/resources/${id}/metrics${q({ range })}`),

  deployments: (resourceId, params = {}) => request('GET', `/resources/${resourceId}/deployments${q(params)}`),
  deploy: (resourceId, input = {}) => request('POST', `/resources/${resourceId}/deployments`, input),
  deployment: id => request('GET', `/deployments/${id}`),
  deploymentLogs: (id, after = 0) => request('GET', `/deployments/${id}/logs${q({ after })}`),
  cancelDeployment: id => request('POST', `/deployments/${id}/cancel`),
  audit: (params = {}) => request('GET', `/audit${q(params)}`),
  auditExportUrl: (params = {}) => teamApiUrl(`/api/audit/export${q(params)}`),
  general: () => request('GET', '/settings/general'),
  putGeneral: body => request('PUT', '/settings/general', body),
  checkDashboardUrl: () => request('POST', '/settings/dashboard-url/check'),
  updates: () => request('GET', '/settings/updates'),
  putUpdates: body => request('PUT', '/settings/updates', body),
  checkUpdates: () => request('POST', '/settings/updates/check'),
  applyUpdate: () => request('POST', '/settings/updates/apply'),
  dismissUpdate: () => request('POST', '/settings/updates/dismiss'),
  users: () => request('GET', '/users'),
  updateUser: (id, patch) => request('PATCH', `/users/${id}`, patch),
  deleteUser: id => request('DELETE', `/users/${id}`),
  revokeSessions: id => request('POST', `/users/${id}/sessions/revoke`),
  resetLink: id => request('POST', `/users/${id}/reset-link`),
  invite: (email, role) => request('POST', '/invites', { email, role }),
  revokeInvite: id => request('DELETE', `/invites/${id}`),
  link: token => request('GET', `/links/${encodeURIComponent(token)}`),
  acceptLink: (token, body) => request('POST', `/links/${encodeURIComponent(token)}`, body),

  limits: id => request('GET', `/resources/${id}/limits`),
  putLimits: (id, limits) => request('PUT', `/resources/${id}/limits`, limits),
  storage: id => request('GET', `/resources/${id}/storage`),
  putStorage: (id, body) => request('PUT', `/resources/${id}/storage`, body),
  deleteVolume: (id, volume) => request('DELETE', `/resources/${id}/storage/${encodeURIComponent(volume)}`),
  build: id => request('GET', `/resources/${id}/build`),
  putBuild: (id, settings) => request('PUT', `/resources/${id}/build`, settings),
  inspectBuild: (id, settings) => request('POST', `/resources/${id}/build/inspect`, settings),
  networking: id => request('GET', `/resources/${id}/networking`),
  putNetworking: (id, config) => request('PUT', `/resources/${id}/networking`, config),
  checkHostPort: (id, input) => request('POST', `/resources/${id}/networking/port-check`, input),
  runDiagnostic: (id, input) => request('POST', `/resources/${id}/diagnostics`, input),
  domains: resourceId => request('GET', `/resources/${resourceId}/domains`),
  createDomain: (resourceId, input) => request('POST', `/resources/${resourceId}/domains`, input),
  updateDomain: (id, patch) => request('PATCH', `/domains/${id}`, patch),
  deleteDomain: (id, removeDns) => request('DELETE', `/domains/${id}${removeDns ? '?dns=1' : ''}`),
  allDomains: (params = {}) => request('GET', `/domains${q(params)}`),
  recheckDomains: () => request('POST', '/domains/check'),
  domainDns: (id, providerId) => request('POST', `/domains/${id}/dns`, { providerId }),
  dnsProviders: () => request('GET', '/dns-providers'),
  createDnsProvider: input => request('POST', '/dns-providers', input),
  refreshDnsProvider: id => request('POST', `/dns-providers/${id}/refresh`),
  deleteDnsProvider: id => request('DELETE', `/dns-providers/${id}`),
  networkAddresses: () => request('GET', '/network/addresses'),
  saveNetworkSettings: input => request('PUT', '/network/settings', input),
  networkPorts: () => request('GET', '/network/ports'),
  reservePort: input => request('POST', '/network/ports/reservations', input),
  releasePort: id => request('DELETE', `/network/ports/reservations/${id}`),
  networks: () => request('GET', '/networks'),
  createNetwork: input => request('POST', '/networks', input),
  updateNetwork: (name, input) => request('PATCH', `/networks/${encodeURIComponent(name)}`, input),
  deleteNetwork: name => request('DELETE', `/networks/${encodeURIComponent(name)}`),
  proxy: () => request('GET', '/network/proxy'),
  saveProxy: input => request('PUT', '/network/proxy', input),
  reloadProxy: () => request('POST', '/network/proxy/reload'),
  proxyLogs: () => request('GET', '/network/proxy/logs'),
  diagnose: input => request('POST', '/network/diagnose', input),
  firewall: (serverId, status) => request('GET', `/servers/${serverId}/firewall${status ? '?status=1' : ''}`),
  saveFirewallRule: (serverId, rule) => request('PUT', `/servers/${serverId}/firewall/rules`, rule),
  deleteFirewallRule: id => request('DELETE', `/firewall-rules/${id}`),
  applyFirewall: serverId => request('POST', `/servers/${serverId}/firewall/apply`),
  checkDomain: id => request('POST', `/domains/${id}/check`),
  resourceSource: id => request('GET', `/resources/${id}/source`),
  health: id => request('GET', `/resources/${id}/health`),
  putHealth: (id, config) => request('PUT', `/resources/${id}/health`, config),
  testHealth: (id, config) => request('POST', `/resources/${id}/health/test`, config),
  releases: id => request('GET', `/resources/${id}/releases`),
  envDiff: deploymentId => request('GET', `/deployments/${deploymentId}/env-diff`),
  tasks: id => request('GET', `/resources/${id}/tasks`),
  createTask: (id, task) => request('POST', `/resources/${id}/tasks`, task),
  updateTask: (taskId, patch) => request('PATCH', `/tasks/${taskId}`, patch),
  deleteTask: taskId => request('DELETE', `/tasks/${taskId}`),
  runTask: taskId => request('POST', `/tasks/${taskId}/run`),
  taskRuns: taskId => request('GET', `/tasks/${taskId}/runs`),
  taskRun: runId => request('GET', `/task-runs/${runId}`),
  cancelTaskRun: runId => request('POST', `/task-runs/${runId}/cancel`),
  cronPreview: (schedule, timezone) => request('GET', `/cron/preview${q({ schedule, timezone })}`),
  access: id => request('GET', `/resources/${id}/access`),
  createHook: id => request('POST', `/resources/${id}/hook`),
  revealHook: id => request('POST', `/resources/${id}/hook/reveal`),
  deleteHook: id => request('DELETE', `/resources/${id}/hook`),
  createResourceToken: (id, body) => request('POST', `/resources/${id}/tokens`, body),
  deleteResourceToken: tokenId => request('DELETE', `/resource-tokens/${tokenId}`),
  advanced: id => request('GET', `/resources/${id}/advanced`),
  putAdvanced: (id, body) => request('PUT', `/resources/${id}/advanced`, body),
  purgeImages: (id, keep) => request('POST', `/resources/${id}/images/purge`, { keep }),
  moveResource: (id, environmentId) => request('POST', `/resources/${id}/move`, { environmentId }),
  dashboard: () => request('GET', '/dashboard'),
  databaseEngines: () => request('GET', '/databases/engines'),
  checkSource: (engine, url) => request('POST', '/databases/check-source', { engine, url }),
  createImport: (resourceId, body) => request('POST', `/resources/${resourceId}/imports`, body),
  imports: resourceId => request('GET', `/resources/${resourceId}/imports`),
  dataImport: (id, after = 0) => request('GET', `/data-imports/${id}${q({ after: after || undefined })}`),
  cancelImport: id => request('POST', `/data-imports/${id}/cancel`),
  createExternalDatabase: (projectId, body) => request('POST', `${projectId ? `/projects/${projectId}` : ''}/external-databases`, body),
  connection: id => request('GET', `/resources/${id}/connection`),
  shareConnection: (id, targetId, key) => request('POST', `/resources/${id}/connection/share`, { targetId, key }),
  testConnection: id => request('POST', `/resources/${id}/connection/test`),
  fn: id => request('GET', `/resources/${id}/function`),
  putFunction: (id, body) => request('PUT', `/resources/${id}/function`, body),
  invokeFunction: (id, body) => request('POST', `/resources/${id}/function/invoke`, body),
  inspectGitImport: body => request('POST', '/projects/import/git/inspect', body),
  previewComposeImport: body => request('POST', '/projects/import/compose/preview', body),
  importProject: body => request('POST', '/projects/import', body),
  duplicateProject: (id, body) => request('POST', `/projects/${id}/duplicate`, body),
  automationCatalog: () => request('GET', '/automations/catalog'),
  automations: params => request('GET', `/automations${q(params)}`),
  automation: id => request('GET', `/automations/${id}`),
  createAutomation: body => request('POST', '/automations', body),
  updateAutomation: (id, body) => request('PUT', `/automations/${id}`, body),
  deleteAutomation: id => request('DELETE', `/automations/${id}`),
  previewAutomation: body => request('POST', '/automations/preview', body),
  runAutomation: (id, reason) => request('POST', `/automations/${id}/run`, reason ? { reason } : undefined),
  automationHook: id => request('POST', `/automations/${id}/hook`),
  testAutomationNotify: id => request('POST', `/automations/${id}/test-notify`),
  automationRun: id => request('GET', `/automation-runs/${id}`),
  decideAutomationRun: (id, decision, note) => request('POST', `/automation-runs/${id}/${decision}`, note ? { note } : undefined),
  sources: (params = {}) => request('GET', `/sources${q(params)}`),
  source: id => request('GET', `/sources/${id}`),
  createSource: input => request('POST', '/sources', input),
  updateSource: (id, patch) => request('PATCH', `/sources/${id}`, patch),
  deleteSource: id => request('DELETE', `/sources/${id}`),
  testSource: id => request('POST', `/sources/${id}/test`),
  sourceRepositories: id => request('GET', `/sources/${id}/repositories`),

  servers: (params = {}) => request('GET', `/servers${q(params)}`),
  server: id => request('GET', `/servers/${id}`),
  serversCapacity: window => request('GET', `/servers/capacity${q({ window })}`),
  verifyServer: input => request('POST', '/servers/verify', input),
  createServer: input => request('POST', '/servers', input),
  updateServer: (id, patch) => request('PATCH', `/servers/${id}`, patch),
  deleteServer: (id, confirm) => request('DELETE', `/servers/${id}${q({ confirm })}`),
  serverRemoval: id => request('GET', `/servers/${id}/removal`),
  testServer: id => request('POST', `/servers/${id}/test`),
  refreshServer: id => request('POST', `/servers/${id}/refresh`),
  setServerDeployments: (id, enabled) => request('PUT', `/servers/${id}/deployments`, { enabled }),
  serverMaintenance: id => request('GET', `/servers/${id}/maintenance`),
  setServerMaintenance: (id, enabled, note = '') => request('POST', `/servers/${id}/maintenance`, { enabled, note }),
  putMaintenanceNote: (id, note) => request('PUT', `/servers/${id}/maintenance/note`, { note }),
  serverEvents: (id, params = {}) => request('GET', `/servers/${id}/events${q(params)}`),
  serverJobs: id => request('GET', `/servers/${id}/jobs`),
  serverJob: jobId => request('GET', `/server-jobs/${jobId}`),
  cancelServerJob: jobId => request('POST', `/server-jobs/${jobId}/cancel`),
  serverMetrics: (id, range) => request('GET', `/servers/${id}/metrics${q({ range })}`),
  serverResources: id => request('GET', `/servers/${id}/resources`),
  serverDeployments: (id, params = {}) => request('GET', `/servers/${id}/deployments${q(params)}`),
  serverLogs: (id, params = {}) => request('GET', `/servers/${id}/logs${q(params)}`),
  serverDiagnostics: id => request('GET', `/servers/${id}/diagnostics`),
  runServerDiagnostics: (id, checks) => request('POST', `/servers/${id}/diagnostics`, checks ? { checks } : {}),
  diagnosticRun: runId => request('GET', `/diagnostic-runs/${runId}`),
  serverBundleUrl: id => teamApiUrl(`/api/servers/${id}/diagnostics/bundle`),
  serversBundleUrl: ids => teamApiUrl(`/api/servers/bundle${q({ ids: ids.join(',') })}`),
  serverDocker: id => request('GET', `/servers/${id}/docker`),
  serverContainers: id => request('GET', `/servers/${id}/docker/containers`),
  serverImages: id => request('GET', `/servers/${id}/docker/images`),
  serverNetworks: id => request('GET', `/servers/${id}/docker/networks`),
  serverVolumes: id => request('GET', `/servers/${id}/docker/volumes`),
  inspectServerObject: (id, kind, objectId) => request('GET', `/servers/${id}/docker/inspect${q({ kind, id: objectId })}`),
  serverContainerAction: (id, containerId, action) => request('POST', `/servers/${id}/docker/containers/${encodeURIComponent(containerId)}/actions`, { action }),
  previewCleanup: (id, options) => request('POST', `/servers/${id}/cleanup/preview`, options),
  runCleanup: (id, options, items) => request('POST', `/servers/${id}/cleanup`, { options, items }),
  serverStorage: id => request('GET', `/servers/${id}/storage`),
  serverNetworking: id => request('GET', `/servers/${id}/networking`),
  serverTasks: id => request('GET', `/servers/${id}/tasks`),
  createServerTask: (id, task) => request('POST', `/servers/${id}/tasks`, task),
  updateServerTask: (taskId, patch) => request('PATCH', `/server-tasks/${taskId}`, patch),
  deleteServerTask: taskId => request('DELETE', `/server-tasks/${taskId}`),
  runServerTask: taskId => request('POST', `/server-tasks/${taskId}/run`),
  serverTaskRuns: taskId => request('GET', `/server-tasks/${taskId}/runs`),
  serverAccess: id => request('GET', `/servers/${id}/access`),
  rotateServerCredentials: (id, body) => request('PUT', `/servers/${id}/credentials`, body),
  revokeServerCredentials: (id, confirm) => request('DELETE', `/servers/${id}/credentials${q({ confirm })}`),
  changeServerEndpoint: (id, endpoint) => request('PUT', `/servers/${id}/endpoint`, { endpoint }),
  bulkServers: body => request('POST', '/servers/bulk', body),
  // Backups of any resource's volumes
  backups: (id, volume) => request('GET', `/resources/${id}/backups${q({ volume })}`),
  createBackup: (id, volume, body = {}) => request('POST', `/resources/${id}/backups${q({ volume })}`, body),
  restoreBackup: (id, volume, name, backupFirst) => request('POST', `/resources/${id}/backups/${encodeURIComponent(name)}/restore${q({ volume })}`, { backupFirst }),
  deleteBackup: (id, volume, name) => request('DELETE', `/resources/${id}/backups/${encodeURIComponent(name)}${q({ volume })}`),
  backupDownloadUrl: (id, volume, name) => teamApiUrl(`/api/resources/${id}/backups/${encodeURIComponent(name)}/download${q({ volume })}`),
  // Files in any resource's volumes
  fileVolumes: id => request('GET', `/resources/${id}/files/volumes`),
  files: (id, volume, path) => request('GET', `/resources/${id}/files${q({ volume, path })}`),
  fileContent: (id, volume, path, text) => request('GET', `/resources/${id}/files/content${q({ volume, path, text: text ? 1 : undefined })}`),
  saveFile: (id, volume, path, content) => request('PUT', `/resources/${id}/files/content${q({ volume, path })}`, { content }),
  uploadFile: (id, volume, path, file) => upload(`/resources/${id}/files/upload${q({ volume, path, name: file.name })}`, file),
  fileStat: (id, volume, path) => request('GET', `/resources/${id}/files/stat${q({ volume, path })}`),
  fileTrash: (id, volume) => request('GET', `/resources/${id}/files/trash${q({ volume })}`),
  fileOp: (id, volume, body) => request('POST', `/resources/${id}/files/ops${q({ volume })}`, body),
  shareFile: (id, volume, path, hours) => request('POST', `/resources/${id}/files/share${q({ volume })}`, { path, hours }),
  fileDownloadUrl: (id, volume, path, inline) => teamApiUrl(`/api/resources/${id}/files/download${q({ volume, path, inline: inline ? 1 : undefined })}`),
  // Game servers
  games: () => request('GET', '/games'),
  gameVersions: (game, edition) => request('GET', `/games/${game}/versions${q({ edition })}`),
  gameModpacks: (game, params = {}) => request('GET', `/games/${game}/modpacks${q(params)}`),
  gameModpackVersions: (game, project) => request('GET', `/games/${game}/modpacks/${encodeURIComponent(project)}/versions`),
  createGame: (projectId, body) => request('POST', `${projectId ? `/projects/${projectId}` : ''}/games`, body),
  game: id => request('GET', `/resources/${id}/game`),
  updateGame: (id, body) => request('PATCH', `/resources/${id}/game`, body),
  gameConsole: (id, command) => request('POST', `/resources/${id}/game/console`, { command }),
  gamePlayers: id => request('GET', `/resources/${id}/game/players`),
  gamePlayerAction: (id, body) => request('POST', `/resources/${id}/game/players`, body),
  gameContent: id => request('GET', `/resources/${id}/game/content`),
  gameContentSearch: (id, params) => request('GET', `/resources/${id}/game/content/search${q(params)}`),
  gameContentVersions: (id, params) => request('GET', `/resources/${id}/game/content/versions${q(params)}`),
  gameContentInstall: (id, body) => request('POST', `/resources/${id}/game/content`, body),
  gameContentChange: (id, body) => request('PATCH', `/resources/${id}/game/content`, body),
  gameContentUpload: (id, kind, file) => upload(`/resources/${id}/game/content/upload${q({ kind, name: file.name })}`, file),
  gameProperties: id => request('GET', `/resources/${id}/game/properties`),
  saveGameProperties: (id, body) => request('PUT', `/resources/${id}/game/properties`, body),
  gameWorlds: id => request('GET', `/resources/${id}/game/worlds`),
  gameWorldAction: (id, action, body) => request('POST', `/resources/${id}/game/worlds/${action}`, body),
  gameWorldUpload: (id, name, file) => upload(`/resources/${id}/game/worlds/upload${q({ name })}`, file),
  gameWorldDownloadUrl: (id, world) => teamApiUrl(`/api/resources/${id}/game/worlds/download${q({ world })}`),
};

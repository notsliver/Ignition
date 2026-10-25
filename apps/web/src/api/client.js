// The one place the dashboard talks to the IGNITION API.
// Errors follow the API's shape: { error: { code, message, fields, requestId } }.

export class ApiError extends Error {
  constructor(status, body) {
    const e = body?.error ?? {};
    super(e.message ?? `Request failed (${status})`);
    this.status = status;
    this.code = e.code ?? 'unknown';
    this.fields = e.fields ?? {};
    this.requestId = e.requestId;
  }
}

const listeners = new Set();
/** Called with no arguments whenever the API says the session has ended. */
export const onUnauthenticated = fn => { listeners.add(fn); return () => listeners.delete(fn); };

async function request(method, path, body) {
  let res;
  try {
    res = await fetch(`/api${path}`, {
      method,
      credentials: 'same-origin',
      headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  } catch {
    throw new ApiError(0, { error: { code: 'offline', message: 'Can\'t reach the IGNITION API. Is it running?' } });
  }
  if (res.status === 204) return null;
  const data = await res.json().catch(() => null);
  if (!res.ok) {
    const err = new ApiError(res.status, data);
    if (res.status === 401 && !path.startsWith('/auth/') && !path.startsWith('/setup')) listeners.forEach(fn => fn());
    throw err;
  }
  return data;
}

const q = params => {
  const s = new URLSearchParams(Object.entries(params).filter(([, v]) => v !== undefined && v !== '' && v !== null)).toString();
  return s ? `?${s}` : '';
};

export const api = {
  setupStatus: () => request('GET', '/setup'),
  setup: input => request('POST', '/setup', input),
  login: (email, password) => request('POST', '/auth/login', { email, password }),
  logout: () => request('POST', '/auth/logout'),
  me: () => request('GET', '/auth/me'),
  system: () => request('GET', '/system'),

  projects: (params = {}) => request('GET', `/projects${q(params)}`),
  project: id => request('GET', `/projects/${id}`),
  createProject: input => request('POST', '/projects', input),
  updateProject: (id, patch) => request('PATCH', `/projects/${id}`, patch),
  deleteProject: id => request('DELETE', `/projects/${id}`),
  setFavorite: (id, on) => request(on ? 'PUT' : 'DELETE', `/projects/${id}/favorite`),

  allResources: (params = {}) => request('GET', `/resources${q(params)}`),
  resources: (projectId, params = {}) => request('GET', `/projects/${projectId}/resources${q(params)}`),
  resource: id => request('GET', `/resources/${id}`),
  createResource: (projectId, input) => request('POST', `/projects/${projectId}/resources`, input),
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
  auditExportUrl: (params = {}) => `/api/audit/export${q(params)}`,
  general: () => request('GET', '/settings/general'),
  putGeneral: body => request('PUT', '/settings/general', body),
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
  deleteDomain: id => request('DELETE', `/domains/${id}`),
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
  sources: (params = {}) => request('GET', `/sources${q(params)}`),
  source: id => request('GET', `/sources/${id}`),
  createSource: input => request('POST', '/sources', input),
  updateSource: (id, patch) => request('PATCH', `/sources/${id}`, patch),
  deleteSource: id => request('DELETE', `/sources/${id}`),
  testSource: id => request('POST', `/sources/${id}/test`),
  sourceRepositories: id => request('GET', `/sources/${id}/repositories`),
};

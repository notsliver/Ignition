// One definition of server status for every page. The API computes the effective connection state
// (a stale "online" comes back as "unknown"), so the UI never upgrades an old observation.

export const connectionStates = {
  online: { tone: 'success', label: 'Online', text: 'The last check reached the server.' },
  degraded: { tone: 'warning', label: 'Degraded', text: 'Reachable but Docker isn\'t ready, or recent checks failed.' },
  offline: { tone: 'danger', label: 'Offline', text: 'Several checks in a row couldn\'t reach the server.' },
  pending: { tone: 'neutral', label: 'Pending enrollment', text: 'Registered but not reached yet.' },
  connecting: { tone: 'progress', label: 'Connecting', text: 'A check is in progress.' },
  unknown: { tone: 'neutral', label: 'Unknown', text: 'No check has finished recently, so the last result can\'t be trusted.' },
};

export const runtimeStates = {
  ready: { tone: 'success', label: 'Ready' },
  not_installed: { tone: 'danger', label: 'Not installed' },
  unavailable: { tone: 'danger', label: 'Unavailable' },
  incompatible: { tone: 'danger', label: 'Incompatible' },
  unknown: { tone: 'neutral', label: 'Unknown' },
};

export const connection = s => connectionStates[s?.connectionState] ?? connectionStates.unknown;

/** Runtime readiness, but only as a current fact while the server is reachable. */
export function runtimeOf(s) {
  const r = runtimeStates[s?.runtimeState] ?? runtimeStates.unknown;
  if (['offline', 'unknown', 'pending'].includes(s?.connectionState)) {
    return { tone: 'neutral', label: 'Unknown', title: s?.runtimeState && s.runtimeState !== 'unknown' ? `Last known: ${r.label}` : undefined };
  }
  return r;
}

export function operational(s) {
  if (s.removing) return { tone: 'warning', label: 'Removing' };
  if (s.maintenance) return { tone: 'warning', label: 'Maintenance' };
  if (!s.isLocal) return { tone: 'neutral', label: 'Monitoring only' };
  return s.deploymentsEnabled ? { tone: 'success', label: 'Deployments on' } : { tone: 'neutral', label: 'Deployments off' };
}

export const methodLabel = { local: 'Local Docker socket', 'docker-tls': 'Docker Engine over mutual TLS' };

export const isAdmin = user => user?.role === 'admin' || user?.role === 'owner';
export const isDeveloper = user => isAdmin(user) || user?.role === 'developer';

export function bytes(n, digits = 1) {
  if (n == null || n < 0) return '—';
  if (n >= 2 ** 40) return `${(n / 2 ** 40).toFixed(digits)} TB`;
  if (n >= 2 ** 30) return `${(n / 2 ** 30).toFixed(digits)} GB`;
  if (n >= 2 ** 20) return `${(n / 2 ** 20).toFixed(0)} MB`;
  if (n >= 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${n} B`;
}

export const rate = v => v == null ? '—' : v >= 2 ** 20 ? `${(v / 2 ** 20).toFixed(1)} MB/s` : v >= 1024 ? `${(v / 1024).toFixed(0)} KB/s` : `${v.toFixed(0)} B/s`;
export const pct = (used, total) => used != null && total ? Math.round(used / total * 100) : null;

export function uptime(seconds) {
  if (seconds == null) return '—';
  const d = Math.floor(seconds / 86400), h = Math.floor(seconds % 86400 / 3600), m = Math.floor(seconds % 3600 / 60);
  return d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : `${m}m`;
}

export const shortId = id => (id ?? '').replace(/^sha256:/, '').slice(0, 12);

// Where each warning's next step lives.
export const warningTarget = {
  offline: 'Diagnostics', stale: 'Diagnostics', intermittent: 'Diagnostics', runtime_missing: 'Diagnostics', runtime_unavailable: 'Diagnostics',
  runtime_incompatible: 'Diagnostics', clock_skew: 'Diagnostics', maintenance: 'Maintenance', deployments_disabled: 'Settings', cert_expiring: 'Access & Security', pending: 'Access & Security',
};

/** Labels as editable text, one key=value per line. */
export const labelsToText = labels => Object.entries(labels ?? {}).map(([k, v]) => `${k}=${v}`).join('\n');
export function textToLabels(text) {
  const out = {};
  for (const line of text.split('\n').map(l => l.trim()).filter(Boolean)) {
    const [k, ...rest] = line.split('=');
    out[k.trim()] = rest.join('=').trim();
  }
  return out;
}

export function download(url) {
  Object.assign(document.createElement('a'), { href: url, download: '' }).click();
}

export function downloadText(name, text, type = 'text/plain') {
  const url = URL.createObjectURL(new Blob([text], { type }));
  Object.assign(document.createElement('a'), { href: url, download: name }).click();
  URL.revokeObjectURL(url);
}

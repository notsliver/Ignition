// Fictional sample data until the API is wired up.
export const stats = [
  { label: 'Projects', value: '12', detail: 'Across all environments' },
  { label: 'Running resources', value: '34', detail: '2 stopped' },
  { label: 'Servers', value: '3 / 3', detail: 'All connected' },
  { label: 'Deployments today', value: '18', detail: '16 successful · 2 failed' },
];

export const deployments = [
  { app: 'api-server', project: 'Backend', commit: 'a91f3c2', message: 'Add rate limiting', status: 'building', duration: '1m 12s', time: 'Now' },
  { app: 'storefront', project: 'Commerce', commit: '4e0b7d1', message: 'Update checkout flow', status: 'success', duration: '2m 04s', time: '2m ago' },
  { app: 'docs', project: 'Documentation', commit: 'c2d88e9', message: 'Fix broken sidebar links', status: 'failed', duration: '48s', time: '18m ago' },
  { app: 'worker', project: 'Backend', commit: '7b1a002', message: 'Retry failed jobs', status: 'success', duration: '1m 31s', time: '1h ago' },
  { app: 'admin', project: 'Commerce', commit: 'f03c6aa', message: 'Bump dependencies', status: 'success', duration: '2m 47s', time: '3h ago' },
];

// Deterministic 30-minute history ending at `last`.
const history = (last, spread, seed) => Array.from({ length: 30 }, (_, i) =>
  i === 29 ? last : Math.max(0, last + spread * (Math.sin(i * 0.7 + seed) * 0.6 + Math.sin(i * 1.9 + seed * 2) * 0.4)));

const bytes = v => v >= 1024 ? `${(v / 1024).toFixed(1)} KB/s` : `${v.toFixed(1)} B/s`;
const percent = v => `${v.toFixed(1)}%`;

export const resources = [
  { label: 'CPU', value: '28.2%', detail: '4 cores � load 1.34, 1.31, 1.31', data: history(28.2, 9, 1), max: 100, format: percent },
  { label: 'Memory', value: '72.1%', detail: '5.5 GB / 7.6 GB', data: history(72.1, 3, 2), max: 100, format: percent },
  { label: 'Disk', value: '41.5%', detail: '31.0 GB / 74.8 GB', data: history(41.5, 0.4, 3), max: 100, format: percent },
  { label: 'Network in', value: '36.7 B/s', detail: 'Last 30 minutes', data: history(36.7, 18, 4), format: bytes },
  { label: 'Network out', value: '230.4 B/s', detail: 'Last 30 minutes', data: history(230.4, 90, 5), format: bytes },
];

export const alerts = [
  { tone: 'danger', title: 'docs deployment failed', detail: 'Build exited with code 1 · 18m ago', action: 'View logs' },
  { tone: 'warning', title: 'build-01 disk at 91%', detail: 'Consider pruning old images', action: 'Open server' },
];

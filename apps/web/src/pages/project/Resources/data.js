// Fictional sample data until the API is wired up. Keyed by project name.
const acme = [
  { name: 'web', type: 'application', source: 'github.com/acme/platform', branch: 'main', status: 'running', url: 'acme.example.com', cpu: 14.2, memory: '384 MB', deployed: '12 min ago' },
  { name: 'api', type: 'application', source: 'github.com/acme/platform', branch: 'main', status: 'running', url: 'api.acme.example.com', cpu: 22.8, memory: '512 MB', deployed: '12 min ago' },
  { name: 'postgres', type: 'database', source: 'PostgreSQL 16', status: 'running', url: 'internal:5432', cpu: 6.1, memory: '1.1 GB', deployed: '9d ago' },
  { name: 'redis', type: 'database', source: 'Redis 7.2', status: 'running', url: 'internal:6379', cpu: 0.8, memory: '48 MB', deployed: '9d ago' },
  { name: 'emails', type: 'worker', source: 'github.com/acme/platform', branch: 'main', status: 'running', cpu: 2.3, memory: '128 MB', deployed: '12 min ago' },
  { name: 'nightly-cleanup', type: 'cron', source: '0 3 * * *', status: 'idle', nextRun: 'in 6h', deployed: '3d ago' },
];

const commerce = [
  { name: 'storefront', type: 'application', source: 'github.com/acme/commerce', branch: 'main', status: 'running', url: 'shop.example.com', cpu: 12.4, memory: '412 MB', deployed: '2 min ago' },
  { name: 'admin', type: 'application', source: 'github.com/acme/commerce', branch: 'main', status: 'running', url: 'admin.shop.example.com', cpu: 3.1, memory: '186 MB', deployed: '1d ago' },
  { name: 'checkout', type: 'application', source: 'gitlab.com/acme/checkout', branch: 'release', status: 'unhealthy', url: 'checkout.shop.example.com', cpu: 48.9, memory: '702 MB', deployed: '2h ago' },
  { name: 'postgres-main', type: 'database', source: 'PostgreSQL 16', status: 'running', url: 'internal:5432', cpu: 8.7, memory: '1.2 GB', deployed: '21d ago' },
  { name: 'mailer', type: 'worker', source: 'github.com/acme/commerce', branch: 'main', status: 'running', cpu: 0.4, memory: '72 MB', deployed: '6d ago' },
  { name: 'image-resizer', type: 'worker', source: 'ghcr.io/acme/resizer:2.4', status: 'running', cpu: 27.5, memory: '540 MB', deployed: '2d ago' },
  { name: 'sitemap', type: 'cron', source: '0 */6 * * *', status: 'failed', nextRun: 'in 2h', deployed: '6d ago' },
];

export const resourcesByProject = { 'Acme Platform': acme, Commerce: commerce };

// Fictional sample data until the API is wired up.
export const resources = [
  { name: 'storefront', type: 'application', project: 'Commerce', status: 'running', server: 'production-01', cpu: 12.4, memory: '412 MB', uptime: '3d 4h' },
  { name: 'admin', type: 'application', project: 'Commerce', status: 'running', server: 'production-01', cpu: 3.1, memory: '186 MB', uptime: '3d 4h' },
  { name: 'api-server', type: 'application', project: 'Backend', status: 'deploying', server: 'production-02', cpu: 41.8, memory: '624 MB', uptime: '—' },
  { name: 'docs', type: 'application', project: 'Documentation', status: 'failed', server: 'production-02', cpu: 0, memory: '0 MB', uptime: '—' },
  { name: 'marketing-site', type: 'application', project: 'Marketing', status: 'running', server: 'production-02', cpu: 1.2, memory: '96 MB', uptime: '12d 2h' },
  { name: 'postgres-main', type: 'database', project: 'Commerce', status: 'running', server: 'production-01', cpu: 8.7, memory: '1.2 GB', uptime: '21d 7h' },
  { name: 'redis-cache', type: 'database', project: 'Backend', status: 'running', server: 'production-02', cpu: 0.9, memory: '64 MB', uptime: '21d 7h' },
  { name: 'mysql-legacy', type: 'database', project: 'Marketing', status: 'stopped', server: 'build-01', cpu: 0, memory: '0 MB', uptime: '—' },
  { name: 'worker', type: 'service', project: 'Backend', status: 'running', server: 'production-02', cpu: 18.3, memory: '298 MB', uptime: '1h 12m' },
  { name: 'mailer', type: 'service', project: 'Commerce', status: 'running', server: 'production-01', cpu: 0.4, memory: '72 MB', uptime: '6d 19h' },
  { name: 'image-resizer', type: 'service', project: 'Commerce', status: 'running', server: 'build-01', cpu: 27.5, memory: '540 MB', uptime: '2d 1h' },
];

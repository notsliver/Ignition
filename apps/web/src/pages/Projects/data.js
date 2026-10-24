// Fictional sample data until the API is wired up.
export const projects = [
  { name: 'Acme Platform', description: 'Main customer-facing platform and API.', health: 'healthy', resources: 5, lastDeploy: '12 min ago', environments: ['Production', 'Staging'] },
  { name: 'Commerce', description: 'Storefront, admin panel and checkout services.', health: 'degraded', issues: 1, resources: 9, lastDeploy: '2 min ago', environments: ['Production', 'Staging', 'QA', 'Demo', 'Preview', 'pr-412', 'pr-415', 'pr-418', 'pr-421', 'feature-checkout-v2', 'load-test'] },
  { name: 'Backend', description: 'Internal APIs, workers and job queues.', health: 'deploying', resources: 7, lastDeploy: 'Now', environments: ['Production'] },
  { name: 'Documentation', description: 'Public docs site and search index.', health: 'failing', issues: 1, resources: 2, lastDeploy: '18 min ago', environments: ['Production'] },
  { name: 'Marketing site', description: 'Landing pages and blog.', health: 'healthy', resources: 3, lastDeploy: 'Yesterday', environments: ['Production', 'Staging'] },
  { name: 'Analytics', description: 'Self-hosted product analytics and dashboards.', health: 'stopped', resources: 2, lastDeploy: '3 weeks ago', environments: ['Staging'] },
];

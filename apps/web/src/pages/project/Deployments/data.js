// Fictional sample data until the API is wired up. Keyed by project name, newest first.
const steps = (status, times) => ['Clone', 'Build', 'Push image', 'Deploy', 'Health check'].map((name, i) => ({
  name,
  duration: times[i] ?? null,
  status: times[i] == null ? (status === 'building' && times.length === i ? 'running' : 'pending') : status === 'failed' && i === times.length - 1 ? 'failed' : 'done',
}));

const acme = [
  { id: 'EjnkIDBk2', environment: 'Development', resource: 'api', commit: 'a91f3c2', message: 'Add rate limiting to public endpoints', branch: 'feat/core-update-rate-limits', author: 'maya', trigger: 'push', status: 'building', duration: '1m 12s', time: 'Now', steps: steps('building', ['3s', '58s']) },
  { id: 'ZytrqPml9', environment: 'Production', resource: 'web', commit: '4e0b7d1', message: 'Update pricing page copy', branch: 'main', author: 'jonas', trigger: 'push', status: 'success', current: true, duration: '1m 46s', time: '12 min ago', steps: steps('success', ['2s', '1m 21s', '9s', '11s', '3s']) },
  { id: 'VokmjXsw3', environment: 'Production', resource: 'api', commit: 'c2d88e9', message: 'Upgrade to Node 22', branch: 'main', author: 'maya', trigger: 'push', status: 'success', current: true, duration: '2m 03s', time: '2h ago', steps: steps('success', ['3s', '1m 38s', '10s', '9s', '3s']) },
  { id: 'Qp4neTyr7', environment: 'Production', resource: 'emails', commit: 'c2d88e9', message: 'Upgrade to Node 22', branch: 'main', author: 'maya', trigger: 'push', status: 'success', current: true, duration: '54s', time: '2h ago', steps: steps('success', ['2s', '41s', '6s', '4s', '1s']) },
  { id: 'Hd82kLmQa', environment: 'Development', resource: 'web', commit: '7b1a002', message: 'Fix hydration warning on dashboard', branch: 'feat/primary-flow', author: 'jonas', trigger: 'push', status: 'failed', duration: '49s', time: 'Yesterday', error: 'Build failed: Type error in src/app/page.tsx:42', steps: steps('failed', ['2s', '47s']) },
  { id: 'Xb1sPq0Wn', environment: 'Production', resource: 'web', commit: 'f03c6aa', message: 'Bump dependencies', branch: 'main', author: 'admin', trigger: 'manual', status: 'success', duration: '1m 52s', time: 'Yesterday', steps: steps('success', ['2s', '1m 27s', '9s', '11s', '3s']) },
  { id: 'Tz7GhvR2c', environment: 'Production', resource: 'api', commit: '19ce4b0', message: 'Rollback to 19ce4b0', branch: 'main', author: 'admin', trigger: 'rollback', status: 'success', duration: '22s', time: 'Oct 22', steps: steps('success', ['—', '—', '—', '19s', '3s']) },
  { id: 'Mn5aQe8Lk', environment: 'Development', resource: 'nightly-cleanup', commit: '5d2e771', message: 'Delete sessions older than 30 days', branch: 'chore/default-retention', author: 'maya', trigger: 'push', status: 'cancelled', duration: '12s', time: 'Oct 21', steps: steps('cancelled', ['2s']) },
];

const commerce = [
  { id: 'Rk3vW9pLs', environment: 'Production', resource: 'storefront', commit: '9fa21de', message: 'New checkout button styles', branch: 'main', author: 'lena', trigger: 'push', status: 'success', current: true, duration: '2m 11s', time: '2 min ago', steps: steps('success', ['3s', '1m 44s', '12s', '9s', '3s']) },
  { id: 'Fj6tYb2Nd', environment: 'Staging', resource: 'checkout', commit: '2b8c1f4', message: 'Retry payment webhooks', branch: 'release', author: 'tom', trigger: 'push', status: 'success', current: true, duration: '1m 31s', time: '2h ago', steps: steps('success', ['2s', '1m 08s', '10s', '8s', '3s']) },
  { id: 'Lp0cXz4Ve', environment: 'Production', resource: 'sitemap', commit: 'e44a09c', message: 'Include product variants in sitemap', branch: 'main', author: 'lena', trigger: 'push', status: 'failed', duration: '38s', time: '6d ago', error: 'Health check failed: process exited with code 1', steps: steps('failed', ['2s', '24s', '7s', '4s', '1s']) },
];

export const deploymentsByProject = { 'Acme Platform': acme, Commerce: commerce };

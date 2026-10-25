// Git-specific paths of the operations pages: built-image rollback, purging old images, hook branch
// filtering, and a scratch image without a shell (tasks and command checks explain themselves).
const BASE = process.env.IGNITION_URL;
let cookie = '';
const ok = msg => console.log('✓', msg);
const sleep = ms => new Promise(r => setTimeout(r, ms));
async function call(method, path, body) {
  const res = await fetch(`${BASE}/api${path}`, { method, headers: { Origin: BASE, ...(cookie ? { Cookie: cookie } : {}), ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}) }, body: body === undefined ? undefined : JSON.stringify(body) });
  const set = res.headers.get('set-cookie');
  if (set) cookie = set.split(';')[0];
  const text = await res.text();
  try { return { status: res.status, data: JSON.parse(text) }; } catch { return { status: res.status, data: text }; }
}
const api = async (method, path, body) => { const r = await call(method, path, body); if (r.status >= 300) throw new Error(`${method} ${path} → ${r.status} ${JSON.stringify(r.data)}`); return r.data; };
async function until(what, fn, ms) { const end = Date.now() + ms; while (Date.now() < end) { const v = await fn(); if (v) return v; await sleep(1500); } throw new Error('timed out waiting for ' + what); }
const deployed = (id, n) => until(`#${n}`, async () => (await api('GET', `/resources/${id}/deployments`)).deployments.find(d => d.number === n && ['successful', 'failed', 'cancelled'].includes(d.status)), 600_000);

try {
  await api('POST', '/auth/login', { email: process.env.IGNITION_EMAIL, password: process.env.IGNITION_PASSWORD });
  const { project } = await api('POST', '/projects', { name: `git-${Date.now() % 100000}` });
  const { resource } = await api('POST', `/projects/${project.id}/resources`, { environmentId: project.environments[0].id, name: 'whoami', type: 'application', sourceType: 'git', repoUrl: 'https://github.com/traefik/whoami', branch: 'master', port: 80 });
  const id = resource.id;
  const d1 = await deployed(id, 1);
  if (d1.status !== 'successful') throw new Error('build failed: ' + d1.error);
  ok(`built and deployed #1 (${d1.imageRef}, ${d1.imageId.slice(7, 19)})`);

  const cmd = await api('POST', `/resources/${id}/health/test`, { kind: 'command', command: 'true', intervalSeconds: 10, timeoutSeconds: 5, retries: 3, startPeriodSeconds: 0, path: '/', expectStatus: '200-399' });
  ok(`command check on a scratch image: ${cmd.result.detail}`);
  const http = await api('POST', `/resources/${id}/health/test`, { kind: 'http', path: '/health', expectStatus: '200', intervalSeconds: 10, timeoutSeconds: 5, retries: 3, startPeriodSeconds: 0 });
  ok(`HTTP check needs nothing in the image: ${http.result.detail} (${http.result.ok})`);
  const { task } = await api('POST', `/resources/${id}/tasks`, { name: 'Shell-less', schedule: '@daily', command: 'echo hi' });
  const { run } = await api('POST', `/tasks/${task.id}/run`);
  const r = await until('run', async () => { const x = (await api('GET', `/task-runs/${run.id}`)).run; return !['queued', 'running'].includes(x.status) && x; }, 30_000);
  ok(`task on a scratch image: ${r.status}: ${r.error}`);

  // Two more builds (no cache on the second, so the image differs), then roll back to #1 and purge.
  await api('POST', `/resources/${id}/deployments`, {});
  if ((await deployed(id, 2)).status !== 'successful') throw new Error('#2 failed');
  await api('POST', `/resources/${id}/deployments`, { noCache: true });
  const d3 = await deployed(id, 3);
  if (d3.status !== 'successful') throw new Error('#3 failed: ' + d3.error);
  const rel = await api('GET', `/resources/${id}/releases`);
  ok(`releases: ${rel.releases.map(x => `#${x.number}${x.current ? '*' : ''} ${x.imageId.slice(7, 15)}`).join(', ')}`);
  const one = rel.releases.find(x => x.number === 1);
  await api('POST', `/resources/${id}/deployments`, { from: one.id, reason: 'Rollback to #1' });
  const d4 = await deployed(id, 4);
  if (d4.status !== 'successful' || d4.imageId !== one.imageId) throw new Error(`rollback should run #1's exact image: ${d4.imageId} vs ${one.imageId}`);
  ok(`rollback #3 → #1 ran the exact image ${d4.imageId.slice(7, 19)}, no rebuild`);
  const adv = await api('GET', `/resources/${id}/advanced`);
  ok(`images before purge: ${adv.images.count} (${Math.round(adv.images.bytes / 1048576)} MB)`);
  const purge = await api('POST', `/resources/${id}/images/purge`, { keep: 0 });
  ok(`purged ${purge.removed} image(s), freed ${Math.round(purge.freedBytes / 1048576)} MB${purge.failed?.length ? `, failed: ${purge.failed}` : ''}`);
  const after = await api('GET', `/resources/${id}/releases`);
  ok(`after purge: ${after.releases.map(x => `#${x.number}${x.current ? '*' : ''}${x.available ? '' : ' (gone)'}`).join(', ')}`);
  const gone = after.releases.find(x => !x.available && !x.current);
  if (gone) {
    const refused = await call('POST', `/resources/${id}/deployments`, { from: gone.id });
    const d5 = refused.status === 202 ? await deployed(id, 5) : null;
    ok(`rolling back to a purged release: ${refused.status === 202 ? `${d5.status}: ${d5.error}` : refused.data.error.message}`);
  }

  const { url } = await api('POST', `/resources/${id}/hook`);
  const hook = (body, event = 'push') => fetch(url.replace(/^https?:\/\/[^/]+/, BASE), { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-GitHub-Event': event }, body: JSON.stringify(body) }).then(x => x.json());
  ok(`hook push to another branch: ${(await hook({ ref: 'refs/heads/feature' })).skipped}`);
  ok(`hook branch delete: ${(await hook({ ref: 'refs/heads/master', after: '0000000000000000000000000000000000000000' })).skipped}`);
  ok(`hook issues event: ${(await hook({}, 'issues')).skipped}`);
  ok(`hook ping: ${(await hook({}, 'ping')).message}`);
} catch (e) {
  console.log('✗', e.message);
  process.exitCode = 1;
}

// End-to-end checks for Health Checks, Rollback History, Scheduled Tasks, Access & Secrets and Advanced
// Settings against a running Ignition (IGNITION_URL, IGNITION_EMAIL, IGNITION_PASSWORD). Uses real containers.
const BASE = process.env.IGNITION_URL;
let cookie = '';
const ok = msg => console.log('✓', msg);
const sleep = ms => new Promise(r => setTimeout(r, ms));

async function call(method, path, body, headers = {}) {
  const res = await fetch(`${BASE}/api${path}`, {
    method, headers: { Origin: BASE, ...(cookie ? { Cookie: cookie } : {}), ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}), ...headers },
    body: body === undefined ? undefined : typeof body === 'string' ? body : JSON.stringify(body),
  });
  const set = res.headers.get('set-cookie');
  if (set) cookie = set.split(';')[0];
  const text = await res.text();
  let data;
  try { data = JSON.parse(text); } catch { data = text; }
  return { status: res.status, data };
}
const api = async (method, path, body, want = [200, 201, 202, 204]) => {
  const r = await call(method, path, body);
  if (!want.includes(r.status)) throw new Error(`${method} ${path} → ${r.status} ${JSON.stringify(r.data)}`);
  return r.data;
};
async function until(what, fn, ms = 90_000) {
  const end = Date.now() + ms;
  let last;
  while (Date.now() < end) {
    last = await fn();
    if (last) return last;
    await sleep(1000);
  }
  throw new Error(`timed out waiting for ${what}`);
}
// A deployment reads "successful" once traffic switches; its job then retires the old container (stop grace).
const action = async (id, name) => until(`${name} accepted`, async () => {
  const r = await call('POST', `/resources/${id}/actions`, { action: name });
  if (r.status === 409 && /in progress/.test(r.data.error.message)) return false;
  if (r.status !== 202) throw new Error(`${name} → ${r.status} ${JSON.stringify(r.data)}`);
  return true;
}, 30_000);
const deployed = async (id, number) => until(`deployment #${number}`, async () => {
  const { deployments } = await api('GET', `/resources/${id}/deployments`);
  const d = deployments.find(x => x.number === number);
  return d && ['successful', 'failed', 'cancelled'].includes(d.status) && d;
}, 180_000);

try {
  await api('POST', '/auth/login', { email: process.env.IGNITION_EMAIL, password: process.env.IGNITION_PASSWORD });
  const run = Date.now() % 100000;
  const { project } = await api('POST', '/projects', { name: `ops-${run}` });
  const env = project.environments[0];
  const created = await api('POST', `/projects/${project.id}/resources`, { environmentId: env.id, name: 'web', type: 'application', sourceType: 'image', image: 'nginx:1.27-alpine', port: 80 });
  const id = created.resource.id;
  const first = await deployed(id, 1);
  if (first.status !== 'successful') throw new Error('first deploy failed: ' + first.error);
  ok(`deployed nginx (#1, image ${first.imageId.slice(7, 19)})`);

  /* ---------------------------- Health checks ---------------------------- */
  let h = await api('GET', `/resources/${id}/health`);
  if (h.config.enabled || h.config.kind !== 'http') throw new Error('health should start off');
  const bad = await call('PUT', `/resources/${id}/health`, { ...h.config, enabled: true, path: 'nope', expectStatus: 'ok', timeoutSeconds: 20, intervalSeconds: 10 });
  if (bad.status !== 422 || !bad.data.error.fields.path || !bad.data.error.fields.expectStatus || !bad.data.error.fields.timeoutSeconds) throw new Error('validation: ' + JSON.stringify(bad.data));
  ok(`validation: ${Object.values(bad.data.error.fields).join(' / ')}`);
  const t1 = await api('POST', `/resources/${id}/health/test`, { ...h.config, path: '/', kind: 'http' });
  if (!t1.result.ok) throw new Error('test / should pass: ' + JSON.stringify(t1.result));
  ok(`test GET / → ${t1.result.detail} in ${t1.result.latencyMs} ms`);
  const t2 = await api('POST', `/resources/${id}/health/test`, { ...h.config, path: '/missing' });
  if (t2.result.ok) throw new Error('test /missing should fail');
  ok(`test GET /missing → ${t2.result.detail.slice(0, 80)}`);
  const t3 = await api('POST', `/resources/${id}/health/test`, { ...h.config, kind: 'tcp', port: 81 });
  ok(`test TCP :81 → ${t3.result.detail}`);
  const t4 = await api('POST', `/resources/${id}/health/test`, { ...h.config, kind: 'command', command: 'test -f /etc/nginx/nginx.conf' });
  if (!t4.result.ok) throw new Error('command check should pass: ' + JSON.stringify(t4.result));
  ok(`test command → ${t4.result.detail}`);

  await api('PUT', `/resources/${id}/health`, { ...h.config, enabled: true, path: '/', intervalSeconds: 2, timeoutSeconds: 2, retries: 2, startPeriodSeconds: 0, restartUnhealthy: true });
  h = await until('healthy', async () => { const x = await api('GET', `/resources/${id}/health`); return x.state.state === 'healthy' && x.results.length >= 2 && x; }, 30_000);
  ok(`monitor: healthy after ${h.results.length} checks`);
  // Break it: the path now 404s, so after 2 failures it's unhealthy and gets restarted.
  await api('PUT', `/resources/${id}/health`, { ...h.config, path: '/broken' });
  const restarted = await until('automatic restart', async () => {
    const x = await api('GET', `/resources/${id}/health`);
    return x.results.find(r => r.source === 'restart') && x;
  }, 40_000);
  ok(`monitor: ${restarted.results.find(r => r.source === 'restart').detail} (state ${restarted.state.state})`);

  // Deploy gate: a release that fails its check never takes over.
  await api('POST', `/resources/${id}/deployments`, {});
  const gated = await deployed(id, 2);
  if (gated.status !== 'failed' || !/health check failed/.test(gated.error)) throw new Error('deploy should fail its health check: ' + gated.error);
  const still = (await api('GET', `/resources/${id}`)).resource;
  if (still.status !== 'running') throw new Error('the previous release should keep running');
  ok(`deploy gate: #2 failed (${gated.error.slice(0, 90)}…), #1 keeps running`);
  await api('PUT', `/resources/${id}/health`, { ...h.config, path: '/', intervalSeconds: 5 });

  /* ------------------------------- Releases ------------------------------- */
  await api('PUT', `/resources/${id}/env`, { variables: [{ key: 'GREETING', value: 'hello', secret: false }, { key: 'TOKEN', value: 'one', secret: true }] });
  await api('POST', `/resources/${id}/deployments`, { reason: 'With variables' });
  const third = await deployed(id, 3);
  if (third.status !== 'successful') throw new Error('#3 failed: ' + third.error);
  await api('PUT', `/resources/${id}/env`, { variables: [{ key: 'GREETING', value: 'changed', secret: false }, { key: 'EXTRA', value: '1', secret: false }] });
  await api('POST', `/resources/${id}/deployments`, { reason: 'Changed variables' });
  if ((await deployed(id, 4)).status !== 'successful') throw new Error('#4 failed');
  const rel = await api('GET', `/resources/${id}/releases`);
  ok(`releases: ${rel.releases.map(r => `#${r.number}${r.current ? '*' : ''}${r.available ? '' : '(gone)'}`).join(' ')}`);
  const target = rel.releases.find(r => r.number === 3);
  const diff = await api('GET', `/deployments/${target.id}/env-diff`);
  if (!diff.snapshot || !diff.changed.includes('GREETING') || !diff.added.includes('TOKEN') || !diff.removed.includes('EXTRA')) throw new Error('env diff: ' + JSON.stringify(diff));
  ok(`env diff vs #3: changes ${diff.changed}, brings back ${diff.added}, removes ${diff.removed} (names only)`);
  const rb = await api('POST', `/resources/${id}/deployments`, { from: target.id, restoreEnv: true, reason: 'Rollback to #3' });
  const five = await deployed(id, 5);
  if (five.status !== 'successful' || five.trigger !== 'rollback' || !five.restoreEnv || five.previousNumber !== 4) throw new Error('rollback: ' + JSON.stringify(five));
  const envNow = await api('GET', `/resources/${id}/env`);
  const keys = envNow.variables.map(v => v.key).sort().join(',');
  if (keys !== 'GREETING,TOKEN') throw new Error('variables not restored: ' + keys);
  ok(`rollback #4 → #3 as #5 (${rb.deploymentId.slice(0, 8)}), variables restored: ${keys}`);
  const noEnv = await call('POST', `/resources/${id}/deployments`, { from: rel.releases.find(r => r.number === 1).id, restoreEnv: true });
  if (noEnv.status !== 409) throw new Error('restoring variables from #1 (none kept) should be refused');
  ok(`refused: ${noEnv.data.error.message}`);

  /* ---------------------------- Scheduled tasks ---------------------------- */
  const badTask = await call('POST', `/resources/${id}/tasks`, { name: '', schedule: '61 * * * *', timezone: 'Mars/Base', command: '' });
  ok(`task validation: ${Object.values(badTask.data.error.fields).join(' / ')}`);
  const preview = await api('GET', `/cron/preview?schedule=${encodeURIComponent('30 9 * * 1-5')}&timezone=Europe/Amsterdam`);
  ok(`preview: ${preview.description}, next ${preview.next[0]}`);
  const { task } = await api('POST', `/resources/${id}/tasks`, { name: 'Say hello', schedule: '* * * * *', timezone: 'UTC', command: 'echo "hello $GREETING from $(hostname)"; echo oops >&2; exit 3', timeoutSeconds: 60 });
  ok(`task created: ${task.description}, next ${task.nextRunAt}`);
  const dup = await call('POST', `/resources/${id}/tasks`, { name: 'Say hello', schedule: '@daily', command: 'true' });
  if (dup.status !== 422) throw new Error('duplicate name should be refused');
  const { run: r1 } = await api('POST', `/tasks/${task.id}/run`);
  const again = await call('POST', `/tasks/${task.id}/run`);
  if (again.status !== 409) throw new Error('second run while running should be refused');
  const done = await until('task run', async () => { const { run } = await api('GET', `/task-runs/${r1.id}`); return ['succeeded', 'failed'].includes(run.status) && run; }, 30_000);
  if (done.exitCode !== 3 || !done.output.includes('hello hello from') || !done.output.includes('oops')) throw new Error('run: ' + JSON.stringify(done));
  ok(`manual run: ${done.status}, exit ${done.exitCode}, output ${JSON.stringify(done.output.trim())}, "${done.error}"`);
  const slow = (await api('POST', `/resources/${id}/tasks`, { name: 'Slow', schedule: '@yearly', command: 'echo start; sleep 300; echo never', timeoutSeconds: 10 })).task;
  const { run: r2 } = await api('POST', `/tasks/${slow.id}/run`);
  const timedOut = await until('timeout', async () => { const { run } = await api('GET', `/task-runs/${r2.id}`); return run.status !== 'running' && run.status !== 'queued' && run; }, 40_000);
  if (timedOut.status !== 'failed' || !/Timed out/.test(timedOut.error)) throw new Error('timeout: ' + JSON.stringify(timedOut));
  ok(`timeout: ${timedOut.error} (exit ${timedOut.exitCode}), output ${JSON.stringify(timedOut.output.trim())}`);
  const { run: r3 } = await api('POST', `/tasks/${slow.id}/run`);
  await until('running', async () => (await api('GET', `/task-runs/${r3.id}`)).run.status === 'running', 15_000);
  await sleep(2500);
  await api('POST', `/task-runs/${r3.id}/cancel`);
  const cancelled = await until('cancelled', async () => { const { run } = await api('GET', `/task-runs/${r3.id}`); return run.status !== 'running' && run; }, 30_000);
  ok(`cancel: ${cancelled.status} (${cancelled.error})`);
  const scheduled = await until('a scheduled run', async () => {
    const { runs } = await api('GET', `/tasks/${task.id}/runs`);
    return runs.find(r => r.trigger === 'schedule' && r.status !== 'queued' && r.status !== 'running');
  }, 75_000);
  ok(`scheduled run at ${scheduled.scheduledFor}: ${scheduled.status}`);
  await api('PATCH', `/tasks/${task.id}`, { enabled: false });
  await api('DELETE', `/tasks/${slow.id}`);

  /* ---------------------------- Access & secrets ---------------------------- */
  const { url: hookUrl } = await api('POST', `/resources/${id}/hook`);
  const hookPath = new URL(hookUrl).pathname.replace(/^\/api/, '');
  const ping = await fetch(`${BASE}/api${hookPath}`, { method: 'POST', headers: { 'X-GitHub-Event': 'ping', 'Content-Type': 'application/json' }, body: '{}' });
  ok(`hook ping (no Origin, no cookie): ${ping.status} ${(await ping.json()).message}`);
  const push = await fetch(`${BASE}/api${hookPath}`, { method: 'POST', headers: { 'X-GitHub-Event': 'push', 'Content-Type': 'application/json' },
    body: JSON.stringify({ ref: 'refs/heads/main', after: 'abc', head_commit: { id: '1234567890abcdef', message: 'Fix the thing\n\nlong body' } }) });
  const pushed = await push.json();
  const push2 = await (await fetch(`${BASE}/api${hookPath}`, { method: 'POST' })).json();
  const tag = await (await fetch(`${BASE}/api${hookPath}`, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-GitHub-Event': 'push' }, body: JSON.stringify({ ref: 'refs/tags/v1' }) })).json();
  ok(`hook push: #${pushed.number}; second call joins it: ${push2.message ? 'yes' : JSON.stringify(push2)}; tag push: ${tag.skipped}`);
  const hd = await deployed(id, pushed.number);
  ok(`hook deployment #${hd.number}: ${hd.status}, trigger ${hd.trigger}, "${hd.reason}"`);
  const wrong = await fetch(`${BASE}/api/hooks/igh_notarealtoken`, { method: 'POST' });
  ok(`unknown hook: ${wrong.status}`);

  const { token: deployTok } = await api('POST', `/resources/${id}/tokens`, { name: 'CI', scope: 'deploy', expiresInDays: 30 });
  const { token: readTok } = await api('POST', `/resources/${id}/tokens`, { name: 'Status page', scope: 'read', expiresInDays: 0 });
  const bearer = (tok, method, path) => fetch(`${BASE}/api${path}`, { method, headers: { Authorization: `Bearer ${tok}` } });
  const checks = [
    [readTok, 'GET', `/resources/${id}`, 200], [readTok, 'GET', `/resources/${id}/deployments`, 200], [readTok, 'POST', `/resources/${id}/deployments`, 403],
    [deployTok, 'GET', '/projects', 403], [deployTok, 'GET', `/resources/${id}/env`, 403], [readTok, 'GET', `/resources/${id.slice(0, -1)}${id.endsWith('0') ? '1' : '0'}`, 403],
    ['ign_wrong', 'GET', `/resources/${id}`, 401], [deployTok, 'POST', `/resources/${id}/deployments`, 202],
  ];
  const statuses = [];
  for (const [tok, method, path, want] of checks) {
    const r = await bearer(tok, method, path);
    statuses.push(`${method} ${path.replace(id, ':id').replace(/\/resources\/[0-9a-f-]{36}$/, '/resources/other')} ${r.status}`);
    if (r.status !== want) throw new Error(`${method} ${path} with token → ${r.status}, want ${want}: ${await r.text()}`);
  }
  ok(`tokens: ${statuses.join(' · ')}`);
  const access = await api('GET', `/resources/${id}/access`);
  ok(`access: ${access.people.length} people, hook last used ${access.hook.lastUsedAt ? 'yes' : 'no'}, tokens ${access.tokens.map(t => `${t.name}(${t.scope}, used ${!!t.lastUsedAt})`).join(', ')}`);
  const last = (await api('GET', `/resources/${id}/deployments`)).deployments[0];
  await deployed(id, last.number);
  ok(`token deployment #${last.number}: trigger ${last.trigger}, by ${last.author}`);

  /* ------------------------------- Advanced ------------------------------- */
  const adv0 = await api('GET', `/resources/${id}/advanced`);
  if (adv0.drift.length) throw new Error('should start in sync: ' + JSON.stringify(adv0.drift));
  const badAdv = await call('PUT', `/resources/${id}/advanced`, { name: 'Web', entrypoint: 'sh -c "x', workdir: 'app', user: 'a b', labels: { 'ignition.x': '1' } });
  ok(`advanced validation: ${Object.keys(badAdv.data.error.fields).join(', ')}`);
  await api('PUT', `/resources/${id}/advanced`, { name: 'web', entrypoint: '', workdir: '/usr/share/nginx/html', user: '', labels: { team: 'core' }, readOnlyRoot: false, init: true, privileged: false });
  const adv1 = await api('GET', `/resources/${id}/advanced`);
  ok(`drift after saving: ${adv1.drift.map(d => `${d.field} ${d.running}→${d.saved}`).join('; ')}`);
  await api('POST', `/resources/${id}/deployments`, { from: (await api('GET', `/resources/${id}`)).resource.currentDeploymentId, reason: 'Apply advanced settings' });
  const latest = (await api('GET', `/resources/${id}/deployments`)).deployments[0];
  if ((await deployed(id, latest.number)).status !== 'successful') throw new Error('recreate failed');
  const adv2 = await api('GET', `/resources/${id}/advanced`);
  if (adv2.drift.length) throw new Error('still drifting: ' + JSON.stringify(adv2.drift));
  ok('recreated: in sync');
  await action(id, 'pause');
  await until('paused', async () => (await api('GET', `/resources/${id}`)).resource.status === 'paused', 20_000);
  const pausedRun = await call('POST', `/tasks/${task.id}/run`);
  ok(`paused (task run refused: ${pausedRun.status})`);
  await action(id, 'resume');
  await until('running again', async () => (await api('GET', `/resources/${id}`)).resource.status === 'running', 20_000);
  ok('resumed');
  const exp = await fetch(`${BASE}/api/resources/${id}/export`, { headers: { Cookie: cookie } });
  const doc = await exp.json();
  if (doc.variables.some(v => 'value' in v) || !doc.variables.some(v => v.key === 'TOKEN')) throw new Error('export must have names, not values');
  ok(`export: ${exp.headers.get('content-disposition')}, ${doc.variables.length} variable names, ${doc.scheduledTasks.length} task(s), no values`);
  const purge = await call('POST', `/resources/${id}/images/purge`, { keep: 0 });
  ok(`purge on an image resource: ${purge.status} ${purge.data.error?.message}`);
  const other = await api('POST', '/projects', { name: `ops-${run}-b` });
  const moved = await api('POST', `/resources/${id}/move`, { environmentId: other.project.environments[0].id });
  const md = await deployed(id, (await api('GET', `/resources/${id}/deployments`)).deployments[0].number);
  ok(`moved to ${moved.projectName} / ${moved.environmentName}, redeployed: ${md.status} ("${md.reason}")`);
  console.log(JSON.stringify({ resourceId: id, projectId: other.project.id }));
} catch (e) {
  console.log('✗', e.message);
  process.exitCode = 1;
}

// End-to-end checks for Automations against a running IGNITION (IGNITION_URL, IGNITION_EMAIL,
// IGNITION_PASSWORD). Uses real containers: verification and escalation are exercised for real.
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
async function until(what, fn, ms = 90_000) { const end = Date.now() + ms; while (Date.now() < end) { const v = await fn(); if (v) return v; await sleep(1500); } throw new Error('timed out waiting for ' + what); }
const deployed = (id, n) => until(`#${n}`, async () => (await api('GET', `/resources/${id}/deployments`)).deployments.find(d => d.number === n && ['successful', 'failed', 'cancelled'].includes(d.status)), 240_000);
const finished = id => until('run to finish', async () => { const { run } = await api('GET', `/automation-runs/${id}`); return !['queued', 'running', 'approved'].includes(run.status) && run; }, 180_000);
const steps = run => run.steps.map(s => `${s.targetName}/${s.action}:${s.status}${s.verified === false ? '(unverified)' : ''}`).join(', ');

try {
  await api('POST', '/auth/login', { email: process.env.IGNITION_EMAIL, password: process.env.IGNITION_PASSWORD });
  const cat = await api('GET', '/automations/catalog');
  const caps = cat.catalog.flatMap(g => g.items);
  ok(`catalog: ${cat.actions.length} steps, ${cat.templates.length} templates, ${caps.filter(c => ['available', 'builtin'].includes(c.status)).length}/${caps.length} capabilities available (rest say what they need)`);

  const { project } = await api('POST', '/projects', { name: `auto-${Date.now() % 100000}` });
  const created = await api('POST', `/projects/${project.id}/resources`, { environmentId: project.environments[0].id, name: 'web', type: 'application', sourceType: 'image', image: 'nginx:1.27-alpine', port: 80 });
  const id = created.resource.id;
  if ((await deployed(id, 1)).status !== 'successful') throw new Error('deploy failed');
  ok('deployed nginx');

  // Validation
  const bad = await call('POST', '/automations', { scope: 'resource', resourceId: id, name: '', mode: 'auto', trigger: { type: 'metric', metric: 'disk', above: 90, forMinutes: 5 }, steps: [{ action: 'docker_cleanup' }] });
  ok(`validation: ${Object.entries(bad.data.error.fields).map(([k, v]) => `${k}: ${v}`).join(' | ')}`);

  // Escalation: a restart can't fix a broken health path, so the run fails verification and the automation pauses.
  const h = (await api('GET', `/resources/${id}/health`)).config;
  await api('PUT', `/resources/${id}/health`, { ...h, enabled: true, path: '/broken', intervalSeconds: 2, timeoutSeconds: 2, retries: 2, startPeriodSeconds: 0, restartUnhealthy: false });
  const heal = (await api('POST', '/automations', { scope: 'resource', resourceId: id, name: 'Recover when unhealthy', mode: 'auto', escalateAfter: 1,
    trigger: { type: 'event', event: 'resource.unhealthy' }, conditions: { cooldownMinutes: 1 },
    steps: [{ action: 'collect_diagnostics', onFailure: 'continue' }, { action: 'restart', params: { verifySeconds: 20 } }] })).automation;
  ok(`created "${heal.name}": ${heal.workflow.join(' → ')}`);
  const firstRun = await until('the unhealthy event to fire', async () => (await api('GET', `/automations/${heal.id}`)).runs.find(r => r.trigger === 'event'), 90_000);
  const failed = await finished(firstRun.id);
  ok(`event run: ${failed.status} — "${failed.reason}"; steps ${steps(failed)}`);
  const evidence = failed.steps.find(s => s.action === 'collect_diagnostics');
  ok(`evidence captured: ${evidence.output.split('\n').slice(0, 3).join(' / ')}`);
  const paused = (await api('GET', `/automations/${heal.id}`)).automation;
  if (paused.enabled || !paused.pausedReason) throw new Error('should have paused itself after the failed run');
  ok(`escalated: ${paused.pausedReason.slice(0, 120)}…`);
  const list = await api('GET', `/automations?resourceId=${id}`);
  ok(`counts: ${JSON.stringify(list.counts)}; needs attention: ${list.attention.map(r => r.status).join(', ')}`);

  // Fixed health path → resume and run by hand: restart verified by a passing health check.
  await api('PUT', `/resources/${id}/health`, { ...h, enabled: true, path: '/', intervalSeconds: 2, timeoutSeconds: 2, retries: 2, startPeriodSeconds: 0, restartUnhealthy: false });
  await until('healthy', async () => (await api('GET', `/resources/${id}/health`)).state.state === 'healthy', 30_000);
  const { trigger, conditions, steps: st, mode, escalateAfter, maxRunsPerHour, description } = paused;
  await api('PUT', `/automations/${heal.id}`, { name: paused.name, description, trigger, conditions, steps: st, mode, escalateAfter: 2, maxRunsPerHour, enabled: true });
  const { run: manual } = await api('POST', `/automations/${heal.id}/run`);
  const healed = await finished(manual.id);
  ok(`manual run after the fix: ${healed.status} — ${healed.steps.find(s => s.action === 'restart').verification}`);
  for (const r of list.attention) await call('POST', `/automation-runs/${r.id}/acknowledge`);

  // Assisted rollback: the plan waits for approval, then runs and is verified.
  await api('POST', `/resources/${id}/deployments`, { reason: 'second release' });
  if ((await deployed(id, 2)).status !== 'successful') throw new Error('#2 failed');
  const rb = (await api('POST', '/automations', { scope: 'resource', resourceId: id, name: 'Roll back', mode: 'assisted', trigger: { type: 'manual' }, steps: [{ action: 'rollback' }] })).automation;
  const { run: proposed } = await api('POST', `/automations/${rb.id}/run`, { reason: 'Error spike after #2' });
  const waiting = await until('approval', async () => { const { run } = await api('GET', `/automation-runs/${proposed.id}`); return run.status === 'awaiting_approval' && run; }, 30_000);
  ok(`assisted: ${waiting.status}, plan ${waiting.plan.map(t => `${t.target.name}: ${t.steps.map(s => `${s.label} [${s.risk}]`).join(', ')}`).join('; ')}, expires ${waiting.expiresAt}`);
  await api('POST', `/automation-runs/${proposed.id}/approve`, { note: 'go' });
  const again = await call('POST', `/automation-runs/${proposed.id}/approve`);
  const rolled = await finished(proposed.id);
  ok(`approved by ${rolled.decidedBy} ("${rolled.decisionNote}"), second approval refused (${again.status}): ${rolled.status} — ${rolled.steps[0].output} ${rolled.steps[0].verification}`);
  const { run: rej } = await api('POST', `/automations/${rb.id}/run`);
  await until('approval', async () => (await api('GET', `/automation-runs/${rej.id}`)).run.status === 'awaiting_approval', 30_000);
  await api('POST', `/automation-runs/${rej.id}/reject`, { note: 'not now' });
  ok(`rejected: ${(await api('GET', `/automation-runs/${rej.id}`)).run.status}`);

  // Observe: evidence steps run, changing steps are only recommended.
  const obs = (await api('POST', '/automations', { scope: 'resource', resourceId: id, name: 'Observe', mode: 'observe', trigger: { type: 'manual' },
    steps: [{ action: 'collect_diagnostics' }, { action: 'run_command', params: { command: 'touch /tmp/should-not-exist' } }] })).automation;
  const { run: oRun } = await api('POST', `/automations/${obs.id}/run`);
  const observed = await finished(oRun.id);
  ok(`observe: ${observed.status} — ${observed.summary} [${steps(observed)}]`);

  // Command verification uses output, not only the exit code.
  const cmd = (await api('POST', '/automations', { scope: 'resource', resourceId: id, name: 'Check config', mode: 'auto', trigger: { type: 'manual' },
    steps: [{ action: 'run_command', params: { command: 'nginx -t 2>&1', expect: 'syntax is ok' } }] })).automation;
  const cRun = await finished((await api('POST', `/automations/${cmd.id}/run`)).run.id);
  ok(`command: ${cRun.status} — ${cRun.steps[0].verification}`);

  // Rate limit
  await api('PUT', `/automations/${cmd.id}`, { name: cmd.name, trigger: cmd.trigger, conditions: cmd.conditions, steps: cmd.steps, mode: 'auto', maxRunsPerHour: 1, escalateAfter: 3, enabled: true });
  const limited = await call('POST', `/automations/${cmd.id}/run`);
  ok(`hourly limit: ${limited.data.run.status} — ${limited.data.run.summary}`);

  // Maintenance window: an automatic run outside the window waits.
  const now = new Date();
  const later = `${String((now.getUTCHours() + 3) % 24).padStart(2, '0')}:00`;
  const until2 = `${String((now.getUTCHours() + 4) % 24).padStart(2, '0')}:00`;
  const win = (await api('POST', '/automations', { scope: 'resource', resourceId: id, name: 'Windowed', mode: 'auto', trigger: { type: 'manual' },
    conditions: { window: { days: [0, 1, 2, 3, 4, 5, 6], start: later, end: until2, timezone: 'UTC' } }, steps: [{ action: 'restart' }] })).automation;
  const { run: wRun } = await api('POST', `/automations/${win.id}/run`);
  await sleep(8000);
  ok(`outside its window (${later}-${until2} UTC) the run waits: ${(await api('GET', `/automation-runs/${wRun.id}`)).run.status}`);
  await api('POST', `/automation-runs/${wRun.id}/cancel`);

  // Webhook trigger
  const { url } = await api('POST', `/automations/${obs.id}/hook`);
  const hooked = await fetch(url.replace(/^https?:\/\/[^/]+/, BASE), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ reason: 'from CI' }) });
  const hj = await hooked.json();
  ok(`webhook (no session): ${hooked.status} → run ${hj.status}, "${(await api('GET', `/automation-runs/${hj.runId}`)).run.reason}"`);

  // Notification webhook errors are reported, not swallowed.
  await api('PUT', `/automations/${obs.id}`, { name: obs.name, trigger: obs.trigger, conditions: obs.conditions, steps: obs.steps, mode: 'observe', notifyUrl: 'http://127.0.0.1:9/nothing', enabled: true });
  const tn = await call('POST', `/automations/${obs.id}/test-notify`);
  ok(`test notification to a dead URL: ${tn.status} ${tn.data.error.message}`);

  // Server automation on this machine
  const servers = await api('GET', '/servers');
  const local = servers.servers.find(s => s.isLocal);
  const srvAuto = (await api('POST', '/automations', { scope: 'servers', name: 'Server care', mode: 'auto', targets: { mode: 'ids', ids: [local.id], stopOnFailure: true },
    trigger: { type: 'manual' }, steps: [{ action: 'disk_report', params: { threshold: 99 }, onFailure: 'continue' }, { action: 'docker_cleanup', params: { containers: true, dangling: true, buildCache: false } }, { action: 'diagnostics' }] })).automation;
  const sRun = await finished((await api('POST', `/automations/${srvAuto.id}/run`)).run.id);
  ok(`server run: ${sRun.status} — ${sRun.summary}`);
  for (const s of sRun.steps) ok(`  ${s.action}: ${s.status}; ${s.output.split('\n')[0].slice(0, 140)} | ${s.verification}`);
  const sv = await api('GET', `/automations?serverId=${local.id}`);
  ok(`server page: ${sv.automations.length} automation(s), ${sv.managedServers} managed server(s), ${sv.resourceAutomations} resource automation(s) running here`);
  const preview = await api('POST', '/automations/preview', { scope: 'servers', name: 'x', mode: 'auto', targets: { mode: 'all' }, trigger: { type: 'schedule', schedule: '0 3 * * 0', timezone: 'Europe/Amsterdam' }, steps: [{ action: 'docker_cleanup' }] });
  ok(`preview: targets ${preview.targets.join(', ')}; next ${preview.nextRuns[0]}; ${preview.workflow.join(' → ')}`);

  // Schedule
  const sch = (await api('POST', '/automations', { scope: 'resource', resourceId: id, name: 'Every minute', mode: 'observe', trigger: { type: 'schedule', schedule: '* * * * *', timezone: 'UTC' }, steps: [{ action: 'collect_diagnostics' }] })).automation;
  const sr = await until('a scheduled run', async () => (await api('GET', `/automations/${sch.id}`)).runs.find(r => r.trigger === 'schedule'), 80_000);
  ok(`scheduled run: ${(await finished(sr.id)).status}`);
  await api('DELETE', `/automations/${sch.id}`);
  console.log(JSON.stringify({ projectId: project.id, resourceId: id, serverId: local.id }));
} catch (e) {
  console.log('✗', e.message);
  process.exitCode = 1;
}

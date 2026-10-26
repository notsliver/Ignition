// Infrastructure › Servers against a running stack. Optional: a Docker-in-Docker engine started with
// --tlsverify reachable as dind-remote:2376, its ca.pem/cert.pem/key.pem mounted at /certs, to test
// enrolling a remote server through the wizard.
//   IGNITION_URL, IGNITION_EMAIL, IGNITION_PASSWORD
import { chromium } from 'playwright';
import { existsSync, readFileSync } from 'node:fs';

const BASE = process.env.IGNITION_URL;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
page.setDefaultTimeout(20_000);
const errors = [];
page.on('pageerror', e => { errors.push(e.message); console.log('[browser error]', e.message); });
const ok = msg => console.log('✓', msg);
const shot = name => page.screenshot({ path: `/shots/servers-${name}.png`, fullPage: true });
const api = async (method, path, body) => page.evaluate(async ([m, p, b]) => {
  const r = await fetch(`/api${p}`, { method: m, headers: { 'Content-Type': 'application/json' }, body: b ? JSON.stringify(b) : undefined });
  return { status: r.status, body: r.status === 204 ? null : await r.json() };
}, [method, path, body]);

try {
  await page.goto(BASE + '/servers');
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password', { exact: true }).fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();

  // Leftovers from an interrupted run would trip the duplicate-endpoint check.
  await page.locator('.sv-table tbody tr').first().waitFor();
  for (const s of (await api('GET', '/servers?q=lab-engine')).body.servers) await api('DELETE', `/servers/${s.id}?confirm=${s.name}`);
  for (const p of (await api('GET', '/projects?q=servers-e2e-')).body.projects) {
    for (const r of (await api('GET', `/projects/${p.id}/resources`)).body.resources) await api('DELETE', `/resources/${r.id}?confirm=${r.name}`);
    for (let i = 0; i < 30 && (await api('DELETE', `/projects/${p.id}`)).status !== 204; i++) await page.waitForTimeout(1000);
  }
  await page.reload();

  // Inventory
  await page.locator('.sv-table tbody tr').first().waitFor();
  await page.getByText('Manage your infrastructure and deployment targets.').waitFor();
  const summary = await page.locator('.sv-summary').innerText();
  ok(`summary: ${summary.replace(/\s+/g, ' ')}`);
  const local = page.locator('.sv-table tbody tr', { hasText: 'This machine' });
  await local.locator('.status', { hasText: 'Online' }).waitFor({ timeout: 40_000 });
  await local.locator('.meter').first().waitFor({ timeout: 40_000 });
  ok('local server online with a current CPU sample');
  await shot('inventory');

  await page.getByLabel('Search name, host, address, environment or label').fill('nothing-matches-this');
  await page.getByText('No servers match “nothing-matches-this”.').waitFor();
  await page.getByLabel('Search name, host, address, environment or label').fill('');
  ok('search with an empty result');
  await page.getByRole('button', { name: 'Compact list' }).click();
  await page.locator('.sv-list li').first().waitFor();
  await page.getByRole('button', { name: 'Table' }).click();
  ok('table / compact list');

  await local.locator('.menu-trigger').click();
  await page.getByRole('menuitem', { name: 'Test connection' }).click();
  await page.getByText('Connection works').waitFor();
  ok('row action: test connection');

  // Add server wizard against the TLS engine
  if (existsSync('/certs/ca_cert.pem')) {
    const pem = n => readFileSync(`/certs/${n}`, 'utf8');
    await page.getByRole('button', { name: 'Add server' }).click();
    await page.getByLabel('Display name', { exact: true }).fill('lab-engine');
    await page.getByLabel('Environment', { exact: true }).fill('staging');
    await page.getByLabel('Labels', { exact: true }).fill('region=lab');
    await page.getByRole('button', { name: 'Continue' }).click();
    await page.getByText('Docker Engine over mutual TLS').first().waitFor();
    await page.getByLabel('Engine address', { exact: true }).fill('dind-remote:2376');
    await page.getByLabel('CA certificate (ca.pem)').fill(pem('ca_cert.pem'));
    await page.getByLabel('Client certificate (cert.pem)').fill(pem('client_cert.pem'));
    await page.getByLabel('Client private key (key.pem)').fill(pem('client_key.pem'));
    await shot('wizard-connection');
    await page.getByRole('button', { name: 'Verify connection' }).click();
    await page.getByText('Connection verified').waitFor({ timeout: 40_000 });
    await shot('wizard-verify');
    ok(`wizard verification: ${await page.locator('.sv-verify li').count()} steps passed`);
    await page.getByRole('button', { name: 'Continue' }).click();
    await page.locator('.sv-caps').waitFor();
    await page.getByRole('button', { name: 'Continue' }).click();
    await page.getByText('Host identity').waitFor();
    const register = page.getByRole('button', { name: 'Register server' });
    if (!(await register.isDisabled())) throw new Error('Register must wait for the fingerprint confirmation');
    await page.locator('.sv-confirm-label').click();
    await shot('wizard-review');
    await register.click();
    await page.getByText('lab-engine is registered').waitFor({ timeout: 40_000 });
    await page.getByRole('button', { name: 'Open server' }).click();
    await page.locator('.page-header h1', { hasText: 'lab-engine' }).waitFor();
    ok('remote server registered through the wizard and opened');
    await page.goto(BASE + '/servers');
    await page.locator('.sv-table tbody tr', { hasText: 'lab-engine' }).waitFor();
  }

  // Workspace: every page renders without errors
  await page.goto(BASE + '/servers');
  await local.locator('.resource-name').click();
  await page.locator('.sv-header').waitFor();
  const serverUrl = new URL(page.url()).pathname;
  ok(`workspace at ${serverUrl}`);
  await page.locator('.ov-usage').waitFor();
  await shot('overview');
  const views = [
    ['Resources', '.rp-toolbar, .rp-empty'], ['Deployments', '.rp-toolbar'], ['Monitoring', '.sv-chart'], ['Logs', '.sv-console'],
    ['Diagnostics', '.sv-checks'], ['Docker', '.sv-docker-hero'], ['Storage', '.ov-card'], ['Networking', '.ov-card'],
    ['Maintenance', '.sv-checklist'], ['Tasks & Automation', '.rp-intro'], ['Access & Security', '.ov-hero'], ['Settings', '.settings-group'],
  ];
  for (const [view, selector] of views) {
    await page.locator('.sidebar').getByRole('button', { name: view, exact: true }).click();
    await page.locator('.page-header h1', { hasText: view }).waitFor();
    await page.locator(selector).first().waitFor();
    await page.waitForTimeout(600);
    await shot(view.toLowerCase().replace(/[^a-z]+/g, '-'));
    ok(`${view} renders`);
  }
  await page.reload();
  await page.locator('.page-header h1', { hasText: 'Settings' }).waitFor();
  ok('reload keeps the server and page (URL restore)');

  // Diagnostics: run all and wait for results
  await page.locator('.sidebar').getByRole('button', { name: 'Diagnostics', exact: true }).click();
  await page.getByRole('button', { name: 'Run all checks' }).click();
  await page.getByText(/Latest results: .*passed/).waitFor({ timeout: 60_000 });
  await page.getByRole('button', { name: /Run all checks/ }).waitFor();
  await page.locator('.sv-check-head', { hasText: 'Docker daemon' }).click();
  await page.locator('.sv-check-body').getByText('IGNITION worker on this host').waitFor();
  await shot('diagnostics-run');
  ok('diagnostics ran with location and output');

  // Docker cleanup preview (never executed here: this is a shared machine)
  await page.locator('.sidebar').getByRole('button', { name: 'Docker', exact: true }).click();
  await page.getByRole('button', { name: 'Clean up…' }).click();
  await page.getByRole('button', { name: 'Preview' }).click();
  await page.getByText(/volume\(s\) stay untouched|Nothing to clean up/).waitFor({ timeout: 40_000 });
  await shot('cleanup-preview');
  await page.getByRole('button', { name: 'Back' }).click();
  await page.locator('dialog[open] .dialog-close').click();
  ok('cleanup preview lists objects and keeps volumes');

  // Maintenance blocks deployments end to end
  const projectName = `servers-e2e-${Date.now()}`;
  const project = await api('POST', '/projects', { name: projectName });
  const env = project.body.project.environments[0].id;
  const res = await api('POST', `/projects/${project.body.project.id}/resources`, { name: 'gate', type: 'application', sourceType: 'image', image: 'nginx:alpine', port: 80, environmentId: env });
  if (res.status >= 300) throw new Error(`create resource: ${JSON.stringify(res.body)}`);
  const rid = res.body.resource.id;
  await page.locator('.sidebar').getByRole('button', { name: 'Maintenance', exact: true }).click();
  await page.locator('.ov-actions').getByRole('button', { name: 'Enter maintenance' }).click();
  await page.getByLabel('Note', { exact: true }).fill('e2e: kernel update');
  await page.locator('dialog[open]').getByRole('button', { name: 'Enter maintenance' }).click();
  await page.getByText('Maintenance mode on').waitFor();
  await page.locator('.ov-status .status', { hasText: 'In maintenance' }).waitFor();
  const blocked = await api('POST', `/resources/${rid}/deployments`, {});
  if (blocked.status !== 409) throw new Error(`deploy during maintenance returned ${blocked.status}`);
  ok(`deploy refused during maintenance: ${blocked.body.error.message.slice(0, 70)}…`);
  await shot('maintenance-on');
  await page.locator('.ov-actions').getByRole('button', { name: 'Exit maintenance' }).click();
  await page.locator('dialog[open]').getByRole('button', { name: 'Exit maintenance' }).click();
  await page.getByText('Maintenance mode off').waitFor();
  const allowed = await api('POST', `/resources/${rid}/deployments`, {});
  if (allowed.status !== 202 && allowed.status !== 201) throw new Error(`deploy after maintenance returned ${allowed.status}: ${JSON.stringify(allowed.body)}`);
  ok('deploy accepted again after exiting maintenance');

  await page.locator('.sidebar').getByRole('button', { name: 'Resources', exact: true }).click();
  const gateRow = page.locator('.rp-table tbody tr', { hasText: projectName });
  await gateRow.waitFor();
  await gateRow.locator('.status', { hasText: 'Running' }).waitFor({ timeout: 90_000 });
  await shot('resources');
  ok('workload listed with observed status');
  await page.locator('.sidebar').getByRole('button', { name: 'Deployments', exact: true }).click();
  await page.locator('.rp-table tbody tr', { hasText: projectName }).first().click();
  await page.locator('.sv-detail').waitFor();
  await shot('deployments');
  ok('deployment detail with log');

  // Tasks: create, run, history
  await page.locator('.sidebar').getByRole('button', { name: 'Tasks & Automation', exact: true }).click();
  const taskName = `Disk check ${Date.now() % 100000}`;
  await page.getByRole('button', { name: 'New task' }).first().click();
  await page.locator('dialog[open]').getByRole('button', { name: 'Type' }).click();
  await page.getByRole('option', { name: /Disk-space check/ }).click();
  await page.getByLabel('Name', { exact: true }).fill(taskName);
  await page.locator('dialog[open]').getByRole('button', { name: 'Save' }).click();
  const taskRow = page.locator('.rp-table tbody tr', { hasText: taskName });
  await taskRow.waitFor();
  await taskRow.locator('.menu-trigger').click();
  await page.getByRole('menuitem', { name: 'Run now' }).click();
  await page.locator('.sv-runs li').first().locator('.status', { hasText: /succeeded|failed/ }).waitFor({ timeout: 60_000 });
  await shot('task-history');
  ok(`task run finished: ${await page.locator('.sv-runs li').first().innerText().then(t => t.split('\n')[0])}`);
  await page.locator('dialog[open] .dialog-close').click();

  // Logs: events with search, then a container
  await page.locator('.sidebar').getByRole('button', { name: 'Logs', exact: true }).click();
  await page.locator('.sv-line').first().waitFor({ timeout: 30_000 });
  ok(`logs show ${await page.locator('.sv-line').count()} event lines`);

  // Settings: unsaved-change bar and save
  await page.locator('.sidebar').getByRole('button', { name: 'Settings', exact: true }).click();
  await page.getByLabel('Environment', { exact: true }).fill(`prod-${Date.now() % 1000}`);
  await page.locator('.save-bar.visible').waitFor();
  await page.getByRole('button', { name: 'Save changes' }).click();
  await page.getByText('Settings saved').waitFor();
  ok('settings save with the unsaved-change bar');

  // Bulk: select all, preview and run a connection test
  await page.goto(BASE + '/servers');
  await page.getByRole('button', { name: 'Select' }).click();
  await page.getByLabel('Select all on this page').check({ force: true });
  await page.getByRole('button', { name: 'Actions', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Test connections' }).click();
  await page.locator('.sv-bulk li').first().waitFor();
  await page.locator('dialog[open]').getByRole('button', { name: /^Test connections on/ }).click();
  await page.getByText(/Succeeded on \d+ server|succeeded, \d+ failed/).waitFor({ timeout: 60_000 });
  await shot('bulk');
  ok('bulk test with per-server results');
  await page.locator('dialog[open]').getByRole('button', { name: 'Done' }).click();

  // Remote server pages and removal
  const remote = page.locator('.sv-table tbody tr', { hasText: 'lab-engine' });
  if (await remote.count()) {
    await page.getByRole('button', { name: 'Done' }).click().catch(() => {});
    await remote.locator('.resource-name').click();
    await page.locator('.sidebar').getByRole('button', { name: 'Monitoring', exact: true }).click();
    await page.getByText('Not supported here.').first().waitFor();
    await shot('remote-monitoring');
    ok('remote monitoring explains unsupported metrics');
    await page.locator('.sidebar').getByRole('button', { name: 'Access & Security', exact: true }).click();
    await page.getByText('Host certificate (SHA-256)').waitFor();
    await shot('remote-access');
    await page.getByRole('button', { name: 'Actions', exact: true }).click();
    await page.getByRole('menuitem', { name: 'Remove server' }).click();
    await page.getByText('What doesn\'t happen').waitFor();
    await shot('remove-dialog');
    await page.getByLabel('Type lab-engine to confirm').fill('lab-engine');
    await page.locator('dialog[open]').getByRole('button', { name: 'Remove server' }).click();
    await page.locator('.sv-table tbody tr').first().waitFor();
    if (await page.locator('.sv-table tbody tr', { hasText: 'lab-engine' }).count()) throw new Error('remote server still listed after removal');
    ok('remote server removed from the workspace');
  }

  // Responsive and dark mode
  await page.goto(BASE + serverUrl);
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'dark'));
  await page.locator('.ov-usage').waitFor();
  await shot('overview-dark');
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(BASE + '/servers');
  await page.locator('.sv-summary').waitFor();
  await shot('inventory-mobile');
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  if (overflow > 2) throw new Error(`horizontal page scroll on mobile: ${overflow}px`);
  ok('phone width has no horizontal page scroll');

  await api('DELETE', `/resources/${rid}?confirm=gate`);
  for (let i = 0; i < 30 && (await api('GET', `/resources/${rid}`)).status !== 404; i++) await page.waitForTimeout(1000);
  await api('DELETE', `/projects/${project.body.project.id}`);
  ok('cleaned up the e2e resource and project');

  if (errors.length) throw new Error(`${errors.length} browser error(s)`);
  console.log('\nAll server checks passed.');
} catch (e) {
  await page.screenshot({ path: '/shots/servers-failure.png', fullPage: true }).catch(() => {});
  console.error('✗', e.message);
  process.exitCode = 1;
} finally {
  await browser.close();
}

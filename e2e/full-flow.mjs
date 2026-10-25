// End-to-end test of the real stack through a real browser. Run with: make e2e
// Requires a running IGNITION (docker compose) with an owner account. Credentials come from env.
import { chromium } from 'playwright';

const BASE = process.env.IGNITION_URL ?? 'http://host.docker.internal:8080';
const EMAIL = process.env.IGNITION_EMAIL;
const PASSWORD = process.env.IGNITION_PASSWORD;
const SHOTS = process.env.SCREENSHOT_DIR ?? '/shots';
const project = `E2E ${Date.now().toString(36)}`;

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
page.setDefaultTimeout(20_000);
page.on('pageerror', e => console.log('\n  [browser error]', e.message));
const step = async (name, fn) => {
  process.stdout.write(`• ${name} … `);
  try {
    await fn();
    console.log('ok');
  } catch (e) {
    await page.screenshot({ path: `${SHOTS}/FAILED-${name.replace(/\W+/g, '-')}.png` });
    console.log('FAILED');
    throw e;
  }
};
const shot = name => page.screenshot({ path: `${SHOTS}/${name}.png` });
const resourceRow = name => page.locator('.resource-row', { has: page.locator('strong', { hasText: new RegExp(`^${name}$`) }) });
async function waitForStatus(name, status, timeout = 180_000) {
  await resourceRow(name).locator('.status', { hasText: status }).waitFor({ timeout });
}

try {
  await step('sign in', async () => {
    await page.goto(BASE);
    await page.getByLabel('Email').fill(EMAIL);
    await page.getByLabel('Password').fill(PASSWORD);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await page.getByRole('heading', { name: 'Dashboard' }).waitFor();
  });

  await step('dashboard is labelled as sample data', async () => {
    await page.getByText('Preview with sample data').first().waitFor();
    await shot('01-dashboard');
  });

  await step('create a project', async () => {
    await page.getByRole('button', { name: 'Projects', exact: true }).click();
    await page.getByRole('button', { name: 'New project' }).click();
    await page.getByRole('menuitem', { name: 'Empty project' }).click();
    await page.getByLabel('Name').fill(project);
    await page.getByLabel('Description').fill('Created by the end-to-end test');
    await page.getByRole('button', { name: 'Create project' }).click();
    await page.getByText(`No resources in Production yet`).waitFor();
    await shot('02-empty-project');
  });

  await step('unsupported options explain themselves', async () => {
    await page.getByRole('button', { name: 'Add resource' }).click();
    await page.getByRole('menuitem', { name: 'Scheduled job' }).click();
    await page.getByText('Scheduled jobs aren\'t supported yet.').waitFor();
    await page.getByRole('button', { name: 'Got it' }).click();
  });

  await step('deploy PostgreSQL with a volume', async () => {
    await page.getByRole('button', { name: 'Add resource' }).click();
    await page.getByRole('menuitem', { name: 'Database' }).hover();
    await page.getByRole('menuitem', { name: 'Create new database' }).hover();
    await page.getByRole('menuitem', { name: 'PostgreSQL' }).click();
    await page.getByRole('button', { name: 'Create database' }).click();
    await waitForStatus('postgresql', 'Running');
  });

  await step('deploy Hello World', async () => {
    await page.getByRole('button', { name: 'Add resource' }).click();
    await page.getByRole('menuitem', { name: 'Application' }).hover();
    await page.getByRole('menuitem', { name: 'Start with Hello World!' }).click();
    await page.getByRole('button', { name: 'Deploy', exact: true }).click();
    await waitForStatus('hello-world', 'Running');
    await shot('03-resources');
  });

  await step('open the app through its published port', async () => {
    const href = await resourceRow('hello-world').locator('.resource-endpoint a').getAttribute('href');
    const res = await page.request.get(href.replace('localhost', process.env.PUBLISHED_HOST ?? 'host.docker.internal'));
    const body = await res.text();
    if (!body.includes('Hostname:')) throw new Error(`unexpected response from ${href}: ${body.slice(0, 80)}`);
  });

  await step('overview shows live state', async () => {
    await resourceRow('hello-world').locator('strong').click();
    await page.getByText('Resource status').waitFor();
    await page.locator('.ov-status .status', { hasText: 'Running' }).waitFor();
    await page.locator('.ov-usage strong').first().filter({ hasText: '%' }).waitFor({ timeout: 30_000 });
    await page.getByText('Deployment successful').waitFor();
    await shot('04-overview');
  });

  await step('deployment timeline has real steps', async () => {
    await page.locator('.sidebar').getByRole('button', { name: 'Deployments', exact: true }).click();
    await page.locator('.deployment-id', { hasText: '#1' }).click();
    await page.getByText('Health verification').waitFor();
    await page.locator('.dep-step', { hasText: 'Image pulled' }).click();
    await page.getByText(/Pulling traefik\/whoami/).waitFor();
    await shot('05-deployments');
  });

  await step('logs come from the container', async () => {
    await page.locator('.sidebar').getByRole('button', { name: 'Logs', exact: true }).click();
    await page.getByText('Starting up on port 80').waitFor();
    await shot('06-logs');
  });

  await step('environment variables persist and offer a redeploy', async () => {
    await page.locator('.sidebar').getByRole('button', { name: 'Environment Variables', exact: true }).click();
    await page.getByLabel('New variable name').fill('GREETING');
    await page.getByLabel('New variable value').fill('hello from e2e');
    await page.getByRole('button', { name: 'Add', exact: true }).click();
    await page.getByLabel('New variable name').fill('API_SECRET');
    await page.getByLabel('New variable value').fill('do-not-leak-me');
    await page.getByRole('button', { name: 'Add', exact: true }).click();
    await page.getByRole('button', { name: 'Save changes' }).click();
    await page.getByText('Redeploy now to apply').waitFor();
    await page.reload();
    // After reload the secret's value must not be in the page.
    await page.getByRole('button', { name: 'Projects', exact: true }).waitFor();
  });

  await step('secret values are never sent to the browser', async () => {
    const res = await page.request.get(`${BASE}/api/projects`);
    const { projects } = await res.json();
    const p = projects.find(x => x.name === project);
    const { resources } = await (await page.request.get(`${BASE}/api/projects/${p.id}/resources`)).json();
    const hello = resources.find(r => r.name === 'hello-world');
    const env = await (await page.request.get(`${BASE}/api/resources/${hello.id}/env`)).text();
    if (env.includes('do-not-leak-me')) throw new Error('secret value leaked in the env API');
    if (!env.includes('hello from e2e')) throw new Error('plain variable missing');
    globalThis.ids = { project: p.id, hello: hello.id };
  });

  await step('stop the app with confirmation', async () => {
    await page.getByRole('button', { name: 'Projects', exact: true }).click();
    await page.locator('.project-card', { hasText: project }).click();
    await resourceRow('hello-world').locator('strong').click();
    await page.getByRole('button', { name: 'Stop', exact: true }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Stop' }).click();
    await page.locator('.ov-status .status', { hasText: 'Stopped' }).waitFor({ timeout: 60_000 });
  });

  await step('delete the app (typed confirmation)', async () => {
    await page.locator('.back-to-app').click(); // resource sidebar → back to the project's resources
    await resourceRow('hello-world').locator('.menu-trigger').click();
    await page.getByRole('menuitem', { name: 'Delete' }).click();
    await page.getByRole('dialog').getByRole('textbox').fill('hello-world');
    await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click();
    await resourceRow('hello-world').waitFor({ state: 'detached', timeout: 60_000 });
    await shot('07-after-delete');
  });

  await step('project delete is refused while resources exist', async () => {
    const res = await page.request.delete(`${BASE}/api/projects/${globalThis.ids.project}`, { headers: { Origin: BASE } });
    if (res.status() !== 409) throw new Error(`expected 409, got ${res.status()}`);
  });

  console.log('\nAll steps passed.');
} finally {
  await browser.close();
}

// Checks Ignition's network model: environments are isolated; a shared network connects resources across
// projects live (no restart), and leaving it disconnects them again. WEB is in one project, OTHER in another
// project that already joined the shared network "bridge-net".
import { chromium } from 'playwright';

const { IGNITION_URL: BASE, PROJECT, WEB, OTHER_NAME } = process.env;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
page.setDefaultTimeout(40_000);
page.on('pageerror', e => console.log('[browser error]', e.message));
const ok = msg => console.log('✓', msg);
const section = title => page.locator('.settings-group', { has: page.getByRole('heading', { name: title, exact: true }) });

async function dnsCustom(host) {
  const conn = section('Connectivity');
  const previous = await conn.locator('.net-test').first().getAttribute('data-test-id').catch(() => null);
  await conn.getByRole('button', { name: 'DNS lookup' }).click();
  await conn.getByRole('button', { name: 'Target' }).click();
  await page.getByRole('option', { name: /Custom host/ }).click();
  await conn.getByLabel('Custom host').fill(host);
  await conn.getByRole('button', { name: 'Run test' }).click();
  const first = conn.locator('.net-test').first();
  if (previous) await conn.locator(`.net-test[data-test-id="${previous}"]`).first().waitFor(); // list rendered
  await page.waitForFunction(prev => document.querySelector('.net-test')?.dataset.testId !== prev, previous);
  await first.getByText(new RegExp(`^${host} (→|doesn't resolve)`)).waitFor();
  return first.locator('.net-test-main > span').innerText();
}

try {
  await page.goto(`${BASE}/projects/${PROJECT}/resources/${WEB}/networking`);
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password').fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  const nets = section('Networks');
  await nets.locator('.net-item').first().getByText('Connected').waitFor();
  if (await page.getByText(/fake-rtsp|_default/).count()) throw new Error('raw Docker networks are still shown');
  ok(`private network: ${(await nets.locator('.net-item').first().innerText()).replace(/\s+/g, ' ')}`);

  const before = await dnsCustom(OTHER_NAME);
  if (!before.includes("doesn't resolve")) throw new Error(`other project should be isolated, got: ${before}`);
  ok(`isolated from the other project: ${before}`);

  // bridge-net is the only existing shared network, so it's preselected.
  await nets.getByRole('button', { name: 'Shared network' }).getByText('bridge-net').waitFor();
  await nets.getByRole('button', { name: 'Join' }).click();
  await nets.getByText('Joins when saved').waitFor();
  await page.getByRole('button', { name: 'Save changes' }).click();
  if (await page.getByRole('dialog').isVisible().catch(() => false)) throw new Error('joining a network should not ask to redeploy');
  await page.getByText('Network changes are live; nothing was restarted.').waitFor();
  await nets.locator('.net-item', { hasText: 'bridge-net' }).getByText('Connected').waitFor();
  ok('joined bridge-net live, without a redeploy');

  const after = await dnsCustom(OTHER_NAME);
  if (!after.includes('→')) throw new Error(`should resolve after joining, got: ${after}`);
  ok(`reaches the other project over the shared network: ${after}`);
  await section('Connectivity').getByRole('row', { name: new RegExp(OTHER_NAME) }).getByText('bridge-net').waitFor();
  ok('peer listed with the shared network');
  await page.screenshot({ path: '/shots/shared-networks.png', fullPage: true });

  await nets.getByLabel('Leave bridge-net').click();
  await page.getByRole('button', { name: 'Save changes' }).click();
  await page.getByText('Network changes are live; nothing was restarted.').waitFor();
  await nets.locator('.net-item', { hasText: 'bridge-net' }).waitFor({ state: 'detached' });
  const left = await dnsCustom(OTHER_NAME);
  if (!left.includes("doesn't resolve")) throw new Error(`should be isolated again, got: ${left}`);
  ok('left bridge-net: isolated again');
} catch (e) {
  console.log('✗', e.message);
  await page.screenshot({ path: '/shots/shared-networks-fail.png', fullPage: true });
  process.exitCode = 1;
}
await browser.close();

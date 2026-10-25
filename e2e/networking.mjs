// Checks a resource's Networking page: live networks, port mappings with conflict checks, apply + redeploy,
// diagnostics, and exposure. Expects an app (WEB) and a database (DB) in the same environment.
import { chromium } from 'playwright';

const { IGNITION_URL: BASE, PROJECT, WEB, DB } = process.env;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
page.setDefaultTimeout(40_000);
page.on('pageerror', e => console.log('[browser error]', e.message));
const ok = msg => console.log('✓', msg);
const section = title => page.locator('.settings-group', { has: page.getByRole('heading', { name: title, exact: true }) });

try {
  await page.goto(`${BASE}/projects/${PROJECT}/resources/${WEB}/networking`);
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password').fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await section('Networks').getByText('Connected').first().waitFor();
  if (await page.getByText('Preview with sample data').count()) throw new Error('still a preview');
  ok(`networks: ${(await section('Networks').locator('.net-item').allInnerTexts()).map(t => t.replace(/\s+/g, ' ')).join(' | ')}`);

  const ports = section('Port mappings');
  await ports.getByRole('button', { name: 'Add mapping' }).click();
  const row = ports.locator('tbody tr').last();
  await row.getByLabel('Host port').fill('8091');
  await row.getByLabel('Container port').click();
  await row.locator('.net-error').waitFor();
  ok(`conflict shown while typing: ${await row.locator('.net-error').innerText()}`);
  await row.getByLabel('Host port').fill('18081');
  await row.getByLabel('Container port').click();
  await row.getByRole('button', { name: 'Listens on' }).click();
  await page.getByRole('option', { name: /All interfaces/ }).click();
  await row.getByText('Reachable from other devices').waitFor();
  ok('exposure flag on an all-interfaces mapping');

  await page.getByRole('button', { name: 'Save changes' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByText(/Fixed host ports \((\d+, )*18081\)/).waitFor();
  ok(`impact preview: ${(await dialog.locator('.net-impact').innerText()).split('\n')[0].slice(0, 90)}…`);
  await dialog.getByRole('button', { name: 'Save and redeploy' }).click();
  await page.getByText('Saved. Redeploying to apply it').waitFor();
  await ports.locator('tbody tr').last().getByText('*:18081').waitFor({ timeout: 60_000 });
  ok('applied: live shows *:18081');
  if (await ports.locator('tbody tr').count() !== 2) throw new Error('expected exactly 2 mappings after saving, got ' + await ports.locator('tbody tr').count());
  ok('exactly 2 mappings listed after saving');
  await section('External access').getByText('*:18081 → 80/tcp').waitFor();
  await section('External access').getByText(/Ignition doesn't change firewall rules/).waitFor();
  ok('external access lists the public port and firewall guidance');

  const conn = section('Connectivity');
  await conn.getByRole('row', { name: /cachenet/ }).getByRole('button', { name: 'Test' }).click();
  await conn.locator('.net-test').first().getByText('cachenet:6379 accepts connections.').waitFor();
  ok('TCP test to a peer from the probe container');
  await conn.getByRole('button', { name: 'DNS lookup' }).click();
  await conn.getByRole('button', { name: 'Run test' }).click();
  await conn.locator('.net-test').first().getByText(/^cachenet → \d/).waitFor();
  ok(`DNS test: ${await conn.locator('.net-test').first().locator('.net-test-main span').first().innerText()}`);
  await page.screenshot({ path: '/shots/networking.png', fullPage: true });

  await page.goto(`${BASE}/projects/${PROJECT}/resources/${DB}/networking`);
  await section('Port mappings').getByText(/No ports are published on this machine/).waitFor();
  await section('External access').getByText('Private').waitFor();
  ok('database is private by default');

  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(500);
  if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw new Error('horizontal scroll on phone');
  ok('phone width fits');
  await page.screenshot({ path: '/shots/networking-phone.png', fullPage: true });
} catch (e) {
  console.log('✗', e.message);
  await page.screenshot({ path: '/shots/networking-fail.png', fullPage: true });
  process.exitCode = 1;
}
await browser.close();

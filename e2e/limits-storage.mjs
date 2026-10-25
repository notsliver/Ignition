// Checks Resource Limits (live apply) and Storage (volumes, backup link, detach) for a database resource DB.
import { chromium } from 'playwright';

const { IGNITION_URL: BASE, PROJECT, DB } = process.env;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
page.setDefaultTimeout(60_000);
page.on('pageerror', e => console.log('[browser error]', e.message));
const ok = msg => console.log('✓', msg);

try {
  await page.goto(`${BASE}/projects/${PROJECT}/resources/${DB}/resource-limits`);
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password').fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.locator('.limits-stats').waitFor();
  if (await page.getByText('Preview with sample data').count()) throw new Error('limits still a preview');
  ok(`usage: ${(await page.locator('.limits-usage-card').innerText()).replace(/\s+/g, ' ').slice(0, 120)}…`);

  const current = await page.getByRole('button', { name: 'Memory limit' }).innerText();
  await page.getByRole('button', { name: 'Memory limit' }).click();
  await page.getByRole('option', { name: current.includes('384') ? '512 MB' : '384 MB', exact: true }).click();
  await page.getByRole('switch', { name: 'Allow swap' }).click();
  await page.locator('.pending-changes').getByText('Memory limit').waitFor();
  ok(`pending: ${(await page.locator('.pending-changes').innerText()).replace(/\s+/g, ' ')}`);
  await page.getByRole('button', { name: 'Save changes' }).click();
  await page.getByText('Applied to the running container').waitFor();
  ok('applied live, no restart');
  await page.screenshot({ path: '/shots/limits.png', fullPage: true });

  await page.goto(`${BASE}/projects/${PROJECT}/resources/${DB}/storage`);
  await page.locator('.volume-row', { hasText: 'data' }).getByText('Mounted').waitFor();
  if (await page.getByText('Preview with sample data').count()) throw new Error('storage still a preview');
  const href = await page.locator('.volume-row', { hasText: 'data' }).getByRole('link', { name: /Download a backup/ }).getAttribute('href');
  const res = await page.request.get(BASE + href);
  if (res.status() !== 200 || !(res.headers()['content-type'] ?? '').includes('gzip')) throw new Error(`backup ${res.status()}`);
  ok(`backup download: ${res.headers()['content-disposition']}`);

  await page.getByRole('button', { name: 'Add volume' }).click();
  await page.getByLabel('Volume name').fill('uploads');
  await page.getByLabel('Mount path').fill('/etc');
  await page.getByRole('button', { name: 'Add', exact: true }).click();
  await page.getByRole('button', { name: 'Save changes' }).click();
  await page.getByText(/system folder/).first().waitFor();
  ok('system folder rejected');
  await page.getByRole('button', { name: 'Remove uploads' }).click();
  await page.getByRole('button', { name: 'Add volume' }).click();
  await page.getByLabel('Volume name').fill('uploads');
  await page.getByLabel('Mount path').fill('/uploads');
  await page.getByRole('button', { name: 'Add', exact: true }).click();
  await page.getByRole('button', { name: 'Save changes' }).click();
  await page.getByText('Volume changes apply on the next deployment').first().waitFor();
  await page.getByRole('button', { name: 'Redeploy now' }).click();
  await page.locator('.volume-row', { hasText: 'uploads' }).getByText('Mounted').waitFor({ timeout: 90_000 });
  ok('added a volume and redeployed: mounted');

  await page.getByRole('button', { name: 'Remove uploads' }).click();
  await page.getByText(/stays on this machine as a detached volume/).waitFor();
  await page.getByRole('button', { name: 'Save changes' }).click();
  await page.getByRole('button', { name: 'Redeploy now' }).click();
  await page.locator('.st-detached .volume-row', { hasText: 'uploads' }).waitFor({ timeout: 90_000 });
  ok('removed: listed under detached volumes with its data');
  await page.screenshot({ path: '/shots/storage.png', fullPage: true });

  const del = page.locator('.st-detached').getByRole('button', { name: /Delete uploads/ });
  await page.waitForFunction(() => !document.querySelector('.st-detached .icon-action.danger')?.disabled, null, { timeout: 60_000 });
  await del.click();
  await page.getByRole('dialog').getByLabel('Volume name').fill('uploads');
  await page.getByRole('dialog').getByRole('button', { name: 'Delete data' }).click();
  await page.getByText('Volume deleted').waitFor();
  ok('detached volume deleted');

  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(500);
  if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw new Error('horizontal scroll on phone');
  ok('phone width fits');
} catch (e) {
  console.log('✗', e.message);
  await page.screenshot({ path: '/shots/limits-storage-fail.png', fullPage: true });
  process.exitCode = 1;
}
await browser.close();

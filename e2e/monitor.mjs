// Checks Monitor against a running stack with several resources across projects.
import { chromium } from 'playwright';

const BASE = process.env.IGNITION_URL;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
page.setDefaultTimeout(20_000);
page.on('pageerror', e => console.log('[browser error]', e.message));
const ok = msg => console.log('✓', msg);
const row = name => page.locator('.monitor-table tbody tr', { has: page.locator('.resource-name', { hasText: new RegExp(`^${name}`) }) });

try {
  await page.goto(BASE + '/monitor');
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password').fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.locator('.monitor-table tbody tr').first().waitFor();
  if (await page.getByText('Preview with sample data').count()) throw new Error('Monitor still shows the sample-data banner');
  ok(`rows: ${await page.locator('.monitor-table tbody tr').count()}`);
  ok(`filter: ${await page.locator('.monitor-filter').innerText().then(t => t.replace(/\s+/g, ' '))}`);

  await row('web').locator('td.num').first().filter({ hasText: '%' }).waitFor({ timeout: 40_000 });
  const cells = await row('web').locator('td').allInnerTexts();
  ok(`live row: ${cells.filter(Boolean).join(' | ')}`);
  await page.screenshot({ path: '/shots/monitor.png' });

  await page.getByLabel('Search name, image or repository').fill('nginx');
  await page.waitForFunction(() => document.querySelectorAll('.monitor-table tbody tr').length === 1);
  ok('server-side search by image');
  await page.getByLabel('Search name, image or repository').fill('');
  await page.waitForFunction(() => document.querySelectorAll('.monitor-table tbody tr').length >= 3);

  await row('api').locator('.menu-trigger').click();
  await page.getByRole('menuitem', { name: 'Stop' }).click();
  await page.getByText('Stop started for 1 resource').waitFor();
  await row('api').locator('.status', { hasText: 'Stopped' }).waitFor({ timeout: 40_000 });
  ok('stop from the row menu');

  await row('api').click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Start' }).click();
  await row('api').locator('.status', { hasText: 'Running' }).waitFor({ timeout: 40_000 });
  ok('start from the right-click menu');

  await page.getByRole('button', { name: 'Select' }).click();
  await row('web').locator('.resource-name').click();
  await row('api').locator('.resource-name').click();
  await page.getByText('2 selected').waitFor();
  await page.locator('.bulk-actions').getByRole('button', { name: 'Restart' }).click();
  await page.getByText('Restart started for 2 resources').waitFor();
  ok('bulk restart of 2');
  await page.locator('.bulk-actions').getByRole('button', { name: 'Done' }).click();

  await row('site').locator('.menu-trigger').click();
  await page.getByRole('menuitem', { name: 'View metrics' }).click();
  await page.locator('.page-header h1', { hasText: 'Metrics' }).waitFor();
  ok(`view metrics opens ${new URL(page.url()).pathname}`);

  await page.goto(BASE + '/monitor');
  await row('site').locator('.resource-name').click();
  await page.locator('.ov-status').waitFor();
  ok('clicking a row opens the resource overview');
  console.log('OK');
} finally {
  await browser.close();
}

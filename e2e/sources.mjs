// Checks Infrastructure > Sources against a running stack: add, real test results, details, edit, delete, and the resource picker.
import { chromium } from 'playwright';

const BASE = process.env.IGNITION_URL;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
page.setDefaultTimeout(30_000);
page.on('pageerror', e => console.log('[browser error]', e.message));
const ok = msg => console.log('✓', msg);
const row = name => page.locator('.sources-table tbody tr', { has: page.locator('.source-name', { hasText: new RegExp(`^${name}`) }) });

try {
  await page.goto(BASE + '/sources');
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password').fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.locator('.sources-table tbody tr').first().waitFor();
  if (await page.getByText('Preview with sample data').count()) throw new Error('Sources shows the sample-data banner');
  ok(`summary: ${(await page.locator('.sources-summary').innerText()).replace(/\s+/g, ' ')}`);
  ok(`filters: ${(await page.locator('.sources-filter').innerText()).replace(/\s+/g, ' ')}`);

  await page.getByRole('button', { name: 'Add source' }).click();
  await page.getByRole('button', { name: /GitHub Container Registry/ }).click();
  await page.locator('#source-name').fill('ghcr-ui');
  await page.locator('#source-username').fill('someone');
  await page.locator('#source-secret').fill('ghp_fakeTokenFromTheUI99');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.getByText('ghcr-ui saved, but the connection test failed').waitFor();
  await row('ghcr-ui').locator('.status', { hasText: 'Failed' }).waitFor();
  ok('add from chooser → real failed status');

  await page.getByRole('button', { name: 'Add source' }).click();
  await page.getByRole('button', { name: /Git over HTTPS or SSH/ }).click();
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.locator('.form-error').first().waitFor();
  ok(`validation: ${(await page.locator('.form-error').allInnerTexts()).join(' | ')}`);
  await page.getByRole('button', { name: 'Cancel' }).click();

  await page.getByLabel('Source type').getByText(/Container registries/).click();
  ok(`registry filter rows: ${await page.locator('.sources-table tbody tr').count()}`);
  await page.getByLabel('Source type').getByText(/^All/).click();
  await page.getByLabel('Search name, provider or URL').fill('whoami');
  await page.waitForFunction(() => document.querySelectorAll('.sources-table tbody tr').length === 1);
  ok('search by URL');
  await page.getByLabel('Search name, provider or URL').fill('');

  await row('public-git').locator('.menu-trigger').click();
  await page.getByRole('menuitem', { name: 'Test connection' }).click();
  await page.getByText('public-git is connected').waitFor();
  ok('test connection');

  await row('hub').first().click();
  await page.getByText(/Used by 1 resource/).waitFor();
  ok('details show linked resources');
  await page.waitForTimeout(600);
  await page.screenshot({ path: '/shots/sources-details.png' });
  await page.getByRole('button', { name: 'Close' }).last().click();

  await row('hub-bad').locator('.menu-trigger').click();
  await page.getByRole('menuitem', { name: 'Reconnect' }).click();
  const placeholder = await page.locator('#source-secret').getAttribute('placeholder');
  if (!placeholder.includes('…d123')) throw new Error('masked hint missing: ' + placeholder);
  if (await page.locator('#source-secret').inputValue()) throw new Error('secret prefilled');
  ok(`masked secret: ${placeholder}`);
  await page.getByRole('button', { name: 'Cancel' }).click();

  await row('ghcr-ui').locator('.menu-trigger').click();
  await page.getByRole('menuitem', { name: 'Delete' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click();
  await page.getByText('ghcr-ui deleted').waitFor();
  ok('delete');

  await page.getByRole('heading', { name: 'Deployment definitions' }).waitFor();
  ok(`definitions: ${(await page.locator('.definitions').first().innerText()).replace(/\s+/g, ' ')}`);
  await page.screenshot({ path: '/shots/sources.png', fullPage: true });

  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(500);
  const wide = await page.evaluate(() => [...document.querySelectorAll('body *')].filter(e => e.getBoundingClientRect().right > innerWidth + 1 && !e.closest('.sources-table-wrap')).filter(e => ![...e.children].some(c => c.getBoundingClientRect().right > innerWidth + 1)).slice(0, 6).map(e => e.tagName + '.' + e.className + ' R' + Math.round(e.getBoundingClientRect().right)));
  if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw new Error('horizontal scroll on phone: ' + wide.join(', '));
  ok('phone width fits');
  await page.screenshot({ path: '/shots/sources-phone.png', fullPage: true });
} catch (e) {
  console.log('✗', e.message);
  await page.screenshot({ path: '/shots/sources-fail.png' });
  process.exitCode = 1;
}
await browser.close();

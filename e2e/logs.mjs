// Checks the Logs page against a resource (LOGGER) that writes JSON, logfmt and plain stderr lines and has
// been deployed twice, so deployment #1's logs are archived.
import { chromium } from 'playwright';

const { IGNITION_URL: BASE, PROJECT, LOGGER } = process.env;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
page.setDefaultTimeout(30_000);
page.on('pageerror', e => console.log('[browser error]', e.message));
const ok = msg => console.log('✓', msg);
const rows = () => page.locator('.logs-body .log');
const search = () => page.getByLabel('Filter logs');

try {
  await page.goto(`${BASE}/projects/${PROJECT}/resources/${LOGGER}/logs`);
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password').fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await rows().first().waitFor();
  if (await page.getByText('Preview with sample data').count()) throw new Error('still a preview');
  ok(`deployment picker: ${await page.getByRole('button', { name: 'Deployment', exact: true }).innerText()}`);
  ok(`levels: ${(await page.locator('.logs-levels').innerText()).replace(/\s+/g, ' ')}`);

  // Structured lines show their message, not raw JSON.
  await page.locator('.log.error .log-message', { hasText: 'payment failed' }).first().waitFor();
  if (await page.locator('.log-message', { hasText: '{"level"' }).count()) throw new Error('JSON shown raw');
  ok('JSON lines parsed: level, message and fields');

  await search().fill('status:>=500');
  await page.waitForFunction(() => [...document.querySelectorAll('.logs-body .log')].every(r => r.textContent.includes('payment failed')));
  ok(`status:>=500 → ${await rows().count()} lines, all "payment failed"`);
  await search().fill('"slow query" -orders');
  await page.waitForFunction(() => document.querySelectorAll('.logs-body .log').length === 0);
  await search().fill('"slow query" ms:>1000');
  await page.waitForFunction(() => document.querySelectorAll('.logs-body .log.warn').length > 0 && [...document.querySelectorAll('.logs-body .log')].every(r => r.classList.contains('warn')));
  ok('phrase, exclusion and numeric comparison on logfmt fields');
  await search().fill('');

  await page.locator('.logs-level-chip.error').click({ modifiers: ['Alt'] });
  await page.waitForFunction(() => [...document.querySelectorAll('.logs-body .log')].every(r => r.classList.contains('error')));
  ok('Alt-click: errors only');
  await rows().first().locator('.log-line').click();
  await rows().first().locator('.log-attrs dd:not(.log-raw)', { hasText: 'stripe' }).hover();
  await rows().first().locator('.log-attrs dd:not(.log-raw)', { hasText: 'stripe' }).getByLabel('Filter by this value').click();
  if (await search().inputValue() !== 'provider:stripe') throw new Error(`field filter: ${await search().inputValue()}`);
  ok('clicking + on a field adds provider:stripe');
  if (!page.url().includes('q=provider')) throw new Error('filter not in the URL');
  await page.reload();
  await rows().first().waitFor();
  if (await search().inputValue() !== 'provider:stripe') throw new Error('filter lost on reload');
  ok('filters survive a reload via the URL');
  await page.getByRole('button', { name: 'Reset filters' }).first().click();

  const bars = page.locator('.logs-bars');
  const box = await bars.boundingBox();
  await page.mouse.move(box.x + box.width * 0.3, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width * 0.6, box.y + box.height / 2, { steps: 5 });
  await page.mouse.up();
  await page.locator('.logs-window').waitFor();
  ok(`histogram drag → window ${await page.locator('.logs-window').innerText()}`);
  await page.locator('body').click({ position: { x: 5, y: 5 } });
  await page.keyboard.press('Escape');
  await page.locator('.logs-window').waitFor({ state: 'detached' });
  await page.keyboard.press('/');
  if (!(await page.evaluate(() => document.activeElement?.closest('.logs-search')))) throw new Error('"/" did not focus the filter');
  ok('Esc clears the window, "/" focuses the filter');
  await page.screenshot({ path: '/shots/logs.png', fullPage: true });

  await page.getByRole('button', { name: 'Deployment', exact: true }).click();
  await page.getByRole('option', { name: /#1/ }).click();
  await page.locator('.logs-live.archived').waitFor();
  await rows().first().waitFor();
  ok(`archived deployment: ${(await page.locator('.logs-source').innerText())} · ${await rows().count()} lines`);
  await page.screenshot({ path: '/shots/logs-archived.png', fullPage: true });

  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(500);
  if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw new Error('horizontal scroll on phone');
  ok('phone width fits');
} catch (e) {
  console.log('✗', e.message);
  await page.screenshot({ path: '/shots/logs-fail.png', fullPage: true });
  process.exitCode = 1;
}
await browser.close();

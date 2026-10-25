// Drives the five operations pages in a browser (IGNITION_URL, IGNITION_EMAIL, IGNITION_PASSWORD,
// PROJECT_ID, RESOURCE_ID from operations-api.mjs) and saves screenshots to SHOTS if set.
import { chromium } from 'playwright';

const { IGNITION_URL: BASE, PROJECT_ID, RESOURCE_ID, SHOTS } = process.env;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
page.setDefaultTimeout(30_000);
const errors = [];
page.on('pageerror', e => errors.push(e.message));
page.on('console', m => { if (m.type() === 'error' && !/Failed to load resource/.test(m.text())) errors.push(m.text()); });
const ok = msg => console.log('✓', msg);
const shot = async name => SHOTS && page.screenshot({ path: `${SHOTS}/ops-${name}.png`, fullPage: true });
const open = view => page.goto(`${BASE}/projects/${PROJECT_ID}/resources/${RESOURCE_ID}/${view}`);
const text = async sel => (await page.locator(sel).first().innerText()).replace(/\s+/g, ' ').trim();

try {
  await open('health-checks');
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password', { exact: true }).fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();

  // Health checks
  await page.locator('.ops-strip').waitFor();
  if (await page.getByText('Preview with sample data').count()) throw new Error('still marked as a preview');
  ok(`health: ${await text('.ops-health-summary')}; strip ${await page.locator('.ops-strip span.ok').count()} ok / ${await page.locator('.ops-strip span.fail').count()} failed`);
  await page.getByRole('button', { name: 'Check now' }).click();
  await page.locator('.health-result').waitFor();
  ok(`check now: ${await text('.health-result')}`);
  await page.locator('#path').fill('/does-not-exist');
  await page.getByRole('button', { name: 'Test these settings' }).click();
  await page.locator('.health-result.fail').waitFor();
  ok(`test unsaved settings: ${(await text('.health-result')).slice(0, 90)}`);
  await shot('health');
  await page.getByRole('button', { name: 'Discard' }).click();

  // Rollback history
  await open('rollback-history');
  await page.locator('.ops-release-num').first().waitFor();
  ok(`releases: ${(await page.locator('.ops-release-num').allInnerTexts()).join(' ')}; rollbacks logged: ${await page.locator('.rollback-log li').count()}`);
  await page.getByRole('button', { name: /Roll (back|forward)/ }).first().click();
  await page.locator('.ops-dialog-body').waitFor();
  await page.waitForFunction(() => !document.querySelector('.ops-dialog-body')?.textContent.includes('Comparing'));
  ok(`dialog: ${(await text('dialog[open]')).slice(0, 220)}`);
  await shot('rollback-dialog');
  await page.getByRole('button', { name: 'Cancel' }).click();

  // Scheduled tasks
  await open('scheduled-tasks');
  await page.locator('.ops-task-row').first().waitFor();
  await page.getByRole('button', { name: 'New task' }).first().click();
  await page.locator('.ops-task-form').getByRole('button', { name: 'Schedule' }).click();
  await page.getByRole('option', { name: /Custom cron/ }).click();
  await page.getByPlaceholder('*/10 * * * *').fill('*/10 9-17 * * mon-fri');
  await page.locator('.ops-preview strong').waitFor();
  ok(`preview: ${await text('.ops-preview')}`);
  await page.getByPlaceholder('*/10 * * * *').fill('*/10 25 * * *');
  await page.locator('.ops-preview.bad').waitFor();
  ok(`bad schedule: ${await text('.ops-preview')}`);
  await page.getByRole('button', { name: 'Cancel' }).click();
  await page.locator('.ops-task-row').first().click();
  await page.locator('.ops-console').waitFor();
  ok(`runs: ${await page.locator('.ops-run-list li').count()} listed; output: ${(await text('.ops-console')).slice(0, 60)}`);
  await shot('tasks');
  await page.setViewportSize({ width: 390, height: 844 });
  await shot('tasks-phone');
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  ok(`phone width: horizontal overflow ${overflow}px`);
  await page.setViewportSize({ width: 1280, height: 900 });

  // Access & secrets
  await open('access-and-secrets');
  await page.locator('.hook-row').waitFor();
  await page.getByRole('button', { name: 'Show hook URL' }).click();
  await page.waitForFunction(() => !document.querySelector('.hook-row code').classList.contains('masked'));
  ok(`hook revealed: ${(await text('.hook-row code')).replace(/igh_\w+/, 'igh_…')}`);
  await page.getByPlaceholder('Name, e.g. GitHub Actions').fill('Release script');
  await page.getByRole('button', { name: 'Create token' }).click();
  await page.locator('.ops-token-reveal').waitFor();
  ok(`token created and shown once: ${(await text('.ops-snippet')).replace(/ign_\w+/, 'ign_…').slice(0, 100)}`);
  await shot('access');

  // Advanced settings
  await open('advanced-settings');
  await page.locator('.drift-head').waitFor();
  ok(`drift: ${await text('.drift-head')}`);
  await page.locator('#workdir').fill('/srv');
  await page.getByRole('button', { name: 'Save changes' }).click();
  await page.getByText('Saved. Applies when the container is recreated.').waitFor();
  await page.waitForFunction(() => document.querySelector('.dep-compare'), null, { timeout: 15_000 }).catch(() => {});
  ok(`after saving a new working directory: ${await page.locator('.dep-compare tbody tr').count()} difference(s) shown`);
  await shot('advanced');
  await page.locator('#workdir').fill('/usr/share/nginx/html');
  await page.getByRole('button', { name: 'Save changes' }).click();
  await page.getByText('Saved. Applies when the container is recreated.').waitFor();

  if (errors.length) throw new Error('browser errors: ' + errors.join(' | '));
} catch (e) {
  console.log('✗', e.message.split('\n')[0]);
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/ops-fail.png`, fullPage: true });
  process.exitCode = 1;
} finally {
  await browser.close();
}

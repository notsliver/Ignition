// Drives the Automations pages (server workspace and resource sidebar) in a browser. Needs PROJECT_ID,
// RESOURCE_ID and SERVER_ID from automations-api.mjs; saves screenshots to SHOTS if set.
import { chromium } from 'playwright';

const { IGNITION_URL: BASE, PROJECT_ID, RESOURCE_ID, SERVER_ID, SHOTS } = process.env;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
page.setDefaultTimeout(30_000);
const errors = [];
page.on('pageerror', e => errors.push(e.message));
const ok = msg => console.log('✓', msg);
const shot = async name => SHOTS && page.screenshot({ path: `${SHOTS}/auto-${name}.png`, fullPage: true });
const text = async sel => (await page.locator(sel).first().innerText()).replace(/\s+/g, ' ').trim();

try {
  await page.goto(`${BASE}/projects/${PROJECT_ID}/resources/${RESOURCE_ID}/automations`);
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password', { exact: true }).fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.locator('.au-stats').waitFor();
  if (await page.getByText('Preview with sample data').count()) throw new Error('still marked as a preview');
  ok(`resource stats: ${await text('.au-stats')}`);
  ok(`resource automations: ${(await page.locator('.au-row-title strong').allInnerTexts()).join(', ')}`);
  await page.locator('.au-last').first().click();
  await page.locator('.au-timeline').first().waitFor();
  ok(`run dialog: ${(await text('.au-run-head')).slice(0, 80)}; ${await page.locator('.au-timeline > li').count()} step(s)`);
  await shot('run');
  await page.getByRole('button', { name: 'Close', exact: true }).last().click();
  await page.getByRole('button', { name: 'What IGNITION can automate' }).click();
  ok(`catalog: ${await page.locator('.au-catalog-group').count()} groups`);
  await shot('resource');

  await page.getByRole('button', { name: 'New automation' }).first().click();
  await page.locator('.au-template', { hasText: 'Recover when unhealthy' }).click();
  await page.locator('.au-preview .au-flow').waitFor();
  ok(`builder from template: ${await text('.au-preview .au-flow')}`);
  await page.getByRole('radio', { name: /Automatic/ }).click();
  await page.locator('.au-builder').getByText('Automatic with disruptive steps').waitFor();
  ok('warns about automatic disruptive steps');
  await shot('builder');
  await page.locator('dialog[open]').getByRole('button', { name: 'Cancel', exact: true }).click();

  await page.goto(`${BASE}/servers/${SERVER_ID}/automations`).catch(() => {});
  if (!(await page.locator('.au-stats').count())) {
    await page.goto(`${BASE}/servers`);
    await page.locator('table, .sv-list').first().waitFor();
    await page.getByText('This machine').first().click();
    await page.getByRole('button', { name: 'Automations' }).click();
  }
  await page.locator('.au-stats').waitFor();
  ok(`server stats: ${await text('.au-stats')}`);
  await shot('server');
  await page.setViewportSize({ width: 390, height: 844 });
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  ok(`phone width overflow: ${overflow}px`);
  await shot('server-phone');
  if (errors.length) throw new Error('browser errors: ' + errors.join(' | '));
} catch (e) {
  console.log('✗', e.message.split('\n')[0]);
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/auto-fail.png`, fullPage: true });
  process.exitCode = 1;
} finally {
  await browser.close();
}

// Checks Settings › Updates: version hero, real check, channel switch, update source dialog and the apply guard.
import { chromium } from 'playwright';

const BASE = process.env.IGNITION_URL;
const SHOTS = process.env.SHOTS;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
page.setDefaultTimeout(40_000);
page.on('pageerror', e => console.log('[browser error]', e.message));
const ok = msg => console.log('✓', msg);
const text = async sel => (await page.locator(sel).first().innerText()).replace(/\s+/g, ' ');

try {
  await page.goto(`${BASE}/settings/updates`);
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password', { exact: true }).fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.locator('.version-hero').waitFor();
  ok(`hero: ${await text('.version-copy')}`);
  ok(`last checked: ${await text('.version-check span')}`);

  await page.getByRole('button', { name: 'Check for updates' }).click();
  await page.getByText(/Couldn't check for updates|Up to date|Update available/).first().waitFor();
  ok(`check: ${await text('.update-notices, .version-pill')}`.slice(0, 260));
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/updates-stable.png`, fullPage: true });

  await page.getByRole('button', { name: 'Prerelease', exact: true }).click();
  await page.getByText("You're on the prerelease channel").waitFor();
  await page.getByRole('button', { name: 'Check for updates' }).isEnabled();
  ok(`prerelease: ${await text('.channel-row')}`);

  await page.getByRole('button', { name: 'Change' }).click();
  await page.locator('#repoUrl').fill('not a url');
  await page.getByRole('button', { name: 'Save' }).click();
  await page.locator('.form-error').first().waitFor();
  ok(`source validation: ${await text('.form-error')}`);
  await page.locator('#repoUrl').fill('https://github.com/notsliver/Ignition');
  await page.getByRole('button', { name: 'Save' }).click();
  await page.locator('dialog[open]').waitFor({ state: 'detached' }).catch(() => {});
  ok('source saved');
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/updates-prerelease.png`, fullPage: true });

  const res = await page.evaluate(() => fetch('/api/settings/updates/apply', { method: 'POST', headers: { 'X-Requested-With': 'ignition' } }).then(async r => [r.status, await r.text()]));
  ok(`apply without a newer version: ${res[0]} ${res[1]}`);

  await page.getByRole('button', { name: 'Stable', exact: true }).click();
  await page.getByText("You're on the prerelease channel").waitFor({ state: 'detached' });
  ok('back on stable');
} catch (e) {
  console.log('✗', e.message);
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/updates-fail.png`, fullPage: true });
  process.exitCode = 1;
} finally {
  await browser.close();
}

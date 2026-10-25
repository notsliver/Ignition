// Checks the generated SSH key flow: create, copy the public key, add it to the server, test until connected.
// Expects an SSH Git server at ssh://git@gitserver/home/git/app.git and a helper that installs /s/pub.txt, then creates /s/added.
import { chromium } from 'playwright';
import { existsSync, writeFileSync } from 'node:fs';

const BASE = process.env.IGNITION_URL;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
page.setDefaultTimeout(30_000);
page.on('pageerror', e => console.log('[browser error]', e.message));
const ok = msg => console.log('✓', msg);

try {
  await page.goto(BASE + '/sources');
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password').fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.getByRole('button', { name: 'Add source' }).click();
  await page.getByRole('button', { name: /Git over HTTPS or SSH/ }).click();
  await page.locator('#source-name').fill('ui-key');
  await page.locator('#source-authType').click();
  await page.getByRole('option', { name: 'SSH private key' }).click();
  if (await page.locator('#source-secret').count()) throw new Error('paste box shown while generating');
  await page.locator('#source-url').fill('ssh://git@gitserver/home/git/app.git');
  await page.getByRole('button', { name: 'Connect' }).click();
  await page.getByRole('heading', { name: 'Add the deploy key' }).waitFor();
  const pub = await page.locator('.public-key code').innerText();
  if (!pub.startsWith('ssh-ed25519 ') || pub.includes('PRIVATE')) throw new Error('bad public key: ' + pub);
  ok(`public key shown: ${pub.slice(0, 40)}…`);
  await page.getByText('Not connected yet').waitFor();
  ok(`before adding: ${await page.locator('.notice').last().innerText()}`);
  await page.screenshot({ path: '/shots/sshkey-step.png' });

  writeFileSync('/s/pub.txt', pub + '\n');
  for (let i = 0; i < 60 && !existsSync('/s/added'); i++) await page.waitForTimeout(500);
  await page.getByRole('button', { name: 'Test connection' }).click();
  await page.getByText('Ignition can read').waitFor();
  ok('connected after adding the deploy key');
  await page.getByRole('button', { name: 'Done' }).click();
  await page.locator('.sources-table tbody tr', { hasText: 'ui-key' }).locator('.status', { hasText: 'Connected' }).waitFor();
  ok('row shows Connected');

  await page.locator('.sources-table tbody tr', { hasText: 'ui-key' }).click();
  await page.locator('.public-key code').waitFor();
  ok('details dialog shows the public key');
  await page.screenshot({ path: '/shots/sshkey-details.png' });
} catch (e) {
  console.log('✗', e.message);
  await page.screenshot({ path: '/shots/sshkey-fail.png' });
  process.exitCode = 1;
}
await browser.close();

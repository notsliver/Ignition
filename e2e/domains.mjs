// Checks a resource's Domains page against a running stack whose proxy is reachable at PROXY_HTTP (Host header routing).
import http from 'node:http';
import { chromium } from 'playwright';

const { IGNITION_URL: BASE, PROJECT, RESOURCE, PROXY_HTTP } = process.env;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
page.setDefaultTimeout(30_000);
page.on('pageerror', e => console.log('[browser error]', e.message));
const ok = msg => console.log('✓', msg);
const row = host => page.locator('.domain', { has: page.locator('.domain-host a', { hasText: new RegExp(`^${host.replace(/\./g, '\\.')}$`) }) });
// node:http, because fetch ignores a custom Host header.
const viaProxy = host => new Promise((resolve, reject) => {
  http.get(PROXY_HTTP, { headers: { Host: host } }, res => { res.resume(); resolve(res.statusCode); }).on('error', reject);
});

try {
  await page.goto(`${BASE}/projects/${PROJECT}/resources/${RESOURCE}/domains`);
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password').fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.locator('.domain').first().waitFor();
  if (await page.getByText('Preview with sample data').count()) throw new Error('still a preview');
  ok(`domains listed: ${await page.locator('.domain').count()}`);

  await page.getByLabel('Domain').fill('shop.localhost');
  await page.getByRole('button', { name: 'Add domain' }).click();
  await row('shop.localhost').locator('.status', { hasText: 'Active' }).waitFor();
  ok(`added shop.localhost → Active, proxy answers ${await viaProxy('shop.localhost')}`);
  ok(`details: ${await row('shop.localhost').locator('.domain-check').first().innerText()}`);

  await page.getByLabel('Domain').fill('*.wild.com');
  await page.getByRole('button', { name: 'Add domain' }).click();
  ok(`validation: ${await page.locator('.rp-error').innerText()}`);
  await page.getByLabel('Domain').fill('');

  await row('shop.localhost').getByRole('switch').click();
  await row('shop.localhost').getByText(/Local certificate · expires/).waitFor({ timeout: 40_000 });
  if (await viaProxy('shop.localhost') !== 308) throw new Error('HTTP should redirect to HTTPS now');
  ok('HTTPS on → local certificate, HTTP redirects');
  await row('shop.localhost').getByRole('switch').click();
  await row('shop.localhost').getByText('HTTP only').waitFor();
  await page.waitForFunction(async () => true);
  ok('HTTPS off again');

  await row('shop.localhost').getByLabel('Target port for shop.localhost').fill('9999');
  await row('shop.localhost').getByLabel('Target port for shop.localhost').press('Enter');
  await row('shop.localhost').getByText('→ port 9999').waitFor();
  for (let i = 0; i < 20 && await viaProxy('shop.localhost') !== 502; i++) await page.waitForTimeout(500);
  ok(`port 9999 (nothing listens) → proxy answers ${await viaProxy('shop.localhost')}`);
  await row('shop.localhost').getByLabel('Target port for shop.localhost').fill('');
  await row('shop.localhost').getByLabel('Target port for shop.localhost').press('Enter');
  await row('shop.localhost').getByText('→ port 80').waitFor();
  for (let i = 0; i < 20 && await viaProxy('shop.localhost') !== 200; i++) await page.waitForTimeout(500);
  ok(`back to the resource's port → ${await viaProxy('shop.localhost')}`);

  await row('shop.localhost').getByRole('button', { name: 'Make primary' }).click();
  await row('shop.localhost').getByText('Primary').waitFor();
  ok('made primary');
  await page.screenshot({ path: '/shots/domains.png', fullPage: true });

  await row('shop.localhost').getByLabel('Remove shop.localhost').click();
  await page.getByRole('button', { name: 'Remove domain' }).click();
  await row('shop.localhost').waitFor({ state: 'detached' });
  for (let i = 0; i < 20 && await viaProxy('shop.localhost') !== 503; i++) await page.waitForTimeout(500);
  ok(`removed → proxy answers ${await viaProxy('shop.localhost')}`);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(500);
  if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw new Error('horizontal scroll on phone');
  ok('phone width fits');
  await page.screenshot({ path: '/shots/domains-phone.png', fullPage: true });
} catch (e) {
  console.log('✗', e.message);
  await page.screenshot({ path: '/shots/domains-fail.png', fullPage: true });
  process.exitCode = 1;
}
await browser.close();

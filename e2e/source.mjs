// Checks a resource's Source page: live branches, switching branch and credentials, saving, and an image resource.
import { chromium } from 'playwright';

const { IGNITION_URL: BASE, PROJECT, GIT_RESOURCE, IMAGE_RESOURCE } = process.env;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
page.setDefaultTimeout(30_000);
page.on('pageerror', e => console.log('[browser error]', e.message));
const ok = msg => console.log('✓', msg);

try {
  await page.goto(`${BASE}/projects/${PROJECT}/resources/${GIT_RESOURCE}/source`);
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password').fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.getByText(/Read access confirmed · 2 branches/).waitFor();
  if (await page.getByText('Preview with sample data').count()) throw new Error('still a preview');
  ok(`repository: ${(await page.locator('.source-repo .rp-row-main').innerText()).replace(/\s+/g, ' ')}`);
  ok(`deployed: ${(await page.locator('.source-drift').innerText()).replace(/\s+/g, ' ')}`);

  await page.locator('#branch').click();
  await page.getByRole('option', { name: /^main/ }).click();
  await page.locator('#sourceId').click();
  await page.getByRole('option', { name: /None/ }).click();
  await page.getByRole('button', { name: 'Save changes' }).click();
  await page.getByText('Source saved').waitFor();
  await page.getByText(/private or doesn't exist|Could not connect|Failed to connect/i).waitFor();
  ok(`unlinked credentials → honest error: ${await page.locator('.source-result.bad').first().innerText()}`);

  await page.locator('#sourceId').click();
  await page.getByRole('option', { name: /^key/ }).click();
  await page.getByRole('button', { name: 'Save changes' }).click();
  await page.getByText(/Read access confirmed/).waitFor();
  ok('relinked credentials');
  await page.screenshot({ path: '/shots/source-git.png', fullPage: true });

  await page.goto(`${BASE}/projects/${PROJECT}/resources/${IMAGE_RESOURCE}/source`);
  await page.locator('#image').waitFor();
  const before = await page.locator('#image').inputValue();
  await page.locator('#image').fill('bad image!!');
  await page.getByRole('button', { name: 'Save changes' }).click();
  await page.getByText('Couldn\'t save').waitFor();
  ok(`invalid image rejected: ${await page.locator('.settings-field-label span').first().innerText()}`);
  await page.locator('#image').fill(before === 'traefik/whoami:v1.10.1' ? 'traefik/whoami:v1.10' : 'traefik/whoami:v1.10.1');
  await page.getByRole('button', { name: 'Save changes' }).click();
  await page.getByText('Source saved').waitFor();
  await page.getByRole('button', { name: 'Deploy now' }).click();
  await page.getByText('Deployment started').waitFor();
  ok('image saved and deployed');
  await page.screenshot({ path: '/shots/source-image.png', fullPage: true });
} catch (e) {
  console.log('✗', e.message);
  await page.screenshot({ path: '/shots/source-fail.png', fullPage: true });
  process.exitCode = 1;
}
await browser.close();

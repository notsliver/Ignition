// Checks Build & Deploy: a repository without a Dockerfile (detected stack), validation, saving, deploying,
// and the empty state for an image-based resource. GIT is a Node app without a Dockerfile; IMAGE runs an image.
import { chromium } from 'playwright';

const { IGNITION_URL: BASE, PROJECT, GIT, IMAGE } = process.env;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
page.setDefaultTimeout(90_000);
page.on('pageerror', e => console.log('[browser error]', e.message));
const ok = msg => console.log('✓', msg);

try {
  await page.goto(`${BASE}/projects/${PROJECT}/resources/${GIT}/build-and-deploy`);
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password').fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.locator('.bd-last').getByText(/Detected Node\.js 22 \(npm\)/).waitFor();
  if (await page.getByText('Preview with sample data').count()) throw new Error('still a preview');
  ok(`last build: ${(await page.locator('.bd-last').innerText()).replace(/\s+/g, ' ')}`);

  await page.getByRole('button', { name: 'Check what will be built' }).click();
  await page.getByText(/No Dockerfile, so Ignition will build it as/).waitFor();
  await page.getByRole('button', { name: 'Show the Dockerfile' }).click();
  await page.locator('.bd-dockerfile').getByText('FROM node:22-alpine').waitFor();
  ok(`inspect: ${(await page.locator('.bd-result').innerText()).split('.')[0]}`);

  await page.getByRole('button', { name: 'Builder' }).click();
  await page.getByRole('option', { name: /Dockerfile only/ }).click();
  await page.getByRole('button', { name: 'Check what will be built' }).click();
  await page.locator('.bd-result.bad').getByText(/isn't in the build context/).waitFor();
  ok('Dockerfile-only mode reports the missing Dockerfile');
  await page.getByRole('button', { name: 'Discard' }).click();

  await page.locator('#args').fill('not-an-arg');
  await page.getByRole('button', { name: 'Save changes' }).click();
  await page.getByText(/must look like NAME=value/).waitFor();
  ok('invalid build argument rejected');
  await page.locator('#args').fill('APP_VERSION=1.2.3');
  await page.locator('#healthSeconds').fill('6');
  await page.locator('#releaseCommand').fill('echo release ok');
  await page.locator('.pending-changes').getByText('Health wait').waitFor();
  ok(`pending: ${(await page.locator('.pending-changes').innerText()).replace(/\s+/g, ' ')}`);
  await page.getByRole('button', { name: 'Save changes' }).click();
  await page.getByText('Build changes apply on the next build.').waitFor();
  ok('saved');

  await page.getByRole('button', { name: 'Build and deploy' }).click();
  await page.getByText('Building and deploying').waitFor();
  await page.locator('.bd-last').getByText(/^Build #6/).waitFor({ timeout: 240_000 });
  await page.locator('.bd-last').getByText('running now').waitFor({ timeout: 240_000 });
  ok(`deployed: ${(await page.locator('.bd-last').innerText()).replace(/\s+/g, ' ')}`);
  await page.screenshot({ path: '/shots/build-deploy.png', fullPage: true });

  await page.goto(`${BASE}/projects/${PROJECT}/resources/${IMAGE}/build-and-deploy`);
  await page.getByText('Nothing to build').waitFor();
  await page.getByRole('button', { name: 'Pull and deploy' }).waitFor();
  if (await page.getByRole('button', { name: 'Rebuild without cache' }).count()) throw new Error('image resources have no build cache');
  ok('image resource: nothing to build, pull and deploy');

  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(500);
  if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw new Error('horizontal scroll on phone');
  ok('phone width fits');
  await page.screenshot({ path: '/shots/build-deploy-image-phone.png', fullPage: true });
} catch (e) {
  console.log('✗', e.message);
  await page.screenshot({ path: '/shots/build-deploy-fail.png', fullPage: true });
  process.exitCode = 1;
}
await browser.close();

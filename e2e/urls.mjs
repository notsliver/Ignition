// Checks that every page has a real URL: reload keeps you there, Back/Forward work, deep links open directly.
// Needs a running stack with a project that contains at least one resource.
import { chromium } from 'playwright';

const BASE = process.env.IGNITION_URL;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
page.setDefaultTimeout(15_000);
page.on('pageerror', e => console.log('[browser error]', e.message));
const path = () => new URL(page.url()).pathname + new URL(page.url()).search;
const h1 = () => page.locator('.page-header h1').innerText();
const check = (label, actual, expected) => {
  if (actual !== expected) throw new Error(`${label}: expected ${expected}, got ${actual}`);
  console.log(`✓ ${label}: ${actual}`);
};

try {
  await page.goto(BASE);
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password').fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.getByRole('heading', { name: 'Dashboard' }).waitFor();
  check('home', path(), '/dashboard');

  await page.getByRole('button', { name: 'Projects', exact: true }).click();
  check('projects', path(), '/projects');
  await page.locator('.project-card').first().click();
  await page.locator('.resource-row').first().waitFor();
  const projectPath = path();
  check('project url shape', /^\/projects\/[0-9a-f-]{36}$/.test(projectPath), true);

  await page.locator('.resource-row strong').first().click();
  await page.locator('.sidebar').getByRole('button', { name: 'Environment Variables', exact: true }).click();
  const deep = path();
  check('resource page url shape', /^\/projects\/[0-9a-f-]{36}\/resources\/[0-9a-f-]{36}\/environment-variables$/.test(deep), true);

  await page.reload();
  await page.locator('.page-header h1', { hasText: 'Environment Variables' }).waitFor();
  check('reload keeps the page', path(), deep);
  check('title after reload', await h1(), 'Environment Variables');
  check('document title', (await page.title()).startsWith('Environment Variables ·'), true);

  await page.goBack();
  await page.locator('.ov-status').waitFor();
  check('back goes to overview', path(), deep.replace('/environment-variables', ''));
  await page.goBack();
  await page.locator('.resource-row').first().waitFor();
  check('back goes to the project', path(), projectPath);
  await page.goForward();
  await page.locator('.ov-status').waitFor();
  check('forward returns to the resource', path(), deep.replace('/environment-variables', ''));

  const fresh = await browser.newPage();
  await fresh.context().addCookies(await page.context().cookies());
  await fresh.goto(BASE + deep.replace('environment-variables', 'metrics'));
  await fresh.locator('.page-header h1', { hasText: 'Metrics' }).waitFor();
  console.log('✓ deep link opens Metrics directly in a new tab');

  await fresh.goto(BASE + '/settings/users-and-permissions');
  await fresh.locator('.page-header h1', { hasText: 'Users & Permissions' }).waitFor();
  console.log('✓ deep link to a settings page');

  await fresh.goto(BASE + '/projects/00000000-0000-4000-8000-000000000000/resources');
  await fresh.getByText('That project or resource no longer exists.').waitFor();
  console.log('✓ a link to a missing project explains itself');

  await fresh.goto(BASE + '/nonsense/path');
  await fresh.locator('.page-header h1', { hasText: 'Dashboard' }).waitFor();
  console.log('✓ unknown paths fall back to the dashboard');
  console.log('OK');
} finally {
  await browser.close();
}

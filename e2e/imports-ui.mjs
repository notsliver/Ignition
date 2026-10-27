// Browser pass over the import dialogs, function Code page and database overviews. Needs PROJECT_ID,
// PG_ID, FN_ID and EXT_ID from imports-api.mjs; saves screenshots to SHOTS if set.
import { chromium } from 'playwright';

const { IGNITION_URL: BASE, PROJECT_ID, PG_ID, FN_ID, EXT_ID, SHOTS } = process.env;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
page.setDefaultTimeout(40_000);
const errors = [];
page.on('pageerror', e => errors.push(e.message));
const ok = msg => console.log('✓', msg);
const shot = async name => SHOTS && page.screenshot({ path: `${SHOTS}/imp-${name}.png` });
const text = async sel => (await page.locator(sel).first().innerText()).replace(/\s+/g, ' ').trim();
const dialog = () => page.locator('dialog[open]');

try {
  await page.goto(`${BASE}/projects`);
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password', { exact: true }).fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();

  // Compose dialog with live preview
  await page.getByRole('button', { name: 'New project' }).click();
  await page.getByRole('menuitem', { name: 'From a Docker Compose file' }).click();
  await dialog().locator('textarea').first().fill('services:\n  web:\n    image: nginx:1.27-alpine\n    ports: ["80:80"]\n  db:\n    image: postgres:16\n    volumes: [pg:/var/lib/postgresql/data, ./init.sql:/docker-entrypoint-initdb.d/x.sql]\n  app:\n    build: .\n');
  await dialog().locator('.pi-services').waitFor();
  ok(`compose preview: ${(await dialog().locator('.pi-svc-main strong').allInnerTexts()).join(', ')}; warnings ${await dialog().locator('.pi-warn').count()}`);
  await dialog().locator('.pi-warn').first().click();
  await shot('compose');
  await dialog().getByRole('button', { name: 'Cancel', exact: true }).click();

  // Git dialog
  await page.getByRole('button', { name: 'New project' }).click();
  await page.getByRole('menuitem', { name: 'Import from Git repository' }).click();
  await page.getByRole('menuitem', { name: 'Continue with GitHub' }).click();
  await dialog().getByPlaceholder('owner/repository').fill('traefik/whoami');
  await dialog().locator('.pi-branch input').fill('master');
  await dialog().getByRole('button', { name: 'Continue' }).click();
  await dialog().locator('.pi-found').waitFor();
  ok(`git inspect: ${await text('.pi-found-head')}`);
  await shot('git');
  await dialog().getByRole('button', { name: 'Cancel', exact: true }).click();

  // Duplicate
  await page.getByRole('button', { name: 'New project' }).click();
  await page.getByRole('menuitem', { name: 'Duplicate an existing project' }).click();
  await dialog().getByRole('button', { name: 'Duplicate' }).waitFor();
  ok(`duplicate dialog: ${(await text('dialog[open] .pi-form')).slice(0, 90)}`);
  await shot('duplicate');
  await dialog().getByRole('button', { name: 'Cancel', exact: true }).click();

  // Add resource › Database › Import existing data
  await page.goto(`${BASE}/projects/${PROJECT_ID}`);
  await page.getByRole('button', { name: 'Add resource' }).first().click();
  await page.getByRole('menuitem', { name: 'Database' }).click();
  await page.getByRole('menuitem', { name: 'Import existing data' }).click();
  await page.getByRole('menuitem', { name: 'Migrate from a live database' }).click();
  await dialog().locator('.db-live input').fill('postgres://ignition:x@pg-a:5432/app');
  await dialog().getByRole('button', { name: 'Check' }).click();
  await dialog().locator('.db-check').waitFor();
  ok(`import dialog live check: ${await text('.db-check')}`);
  await dialog().getByRole('button', { name: 'Upload' }).click();
  await shot('import');
  await dialog().getByRole('button', { name: 'Cancel', exact: true }).click();

  // Function in the add menu
  await page.getByRole('button', { name: 'Add resource' }).first().click();
  await page.getByRole('menuitem', { name: 'Function' }).click();
  await dialog().locator('.fn-editor textarea').waitFor();
  ok(`function dialog: ${(await dialog().locator('.fn-editor textarea').inputValue()).split('\n')[0]}`);
  await dialog().getByRole('button', { name: 'Python 3.12' }).click();
  await page.waitForTimeout(300);
  ok(`switching runtime loads the Python starter: ${(await dialog().locator('.fn-editor textarea').inputValue()).includes('def handler')}`);
  await dialog().getByRole('button', { name: 'Cancel', exact: true }).click();

  // Function Code page + test
  await page.goto(`${BASE}/projects/${PROJECT_ID}/resources/${FN_ID}/code`);
  await page.locator('.fn-editor').waitFor();
  await page.getByRole('button', { name: 'Send' }).click();
  await page.locator('.fn-response, .fn-result').waitFor();
  ok(`code page test call: ${(await text('.fn-response-head, .fn-result'))}`);
  await shot('code');

  // Database overview + external overview
  await page.goto(`${BASE}/projects/${PROJECT_ID}/resources/${PG_ID}`);
  await page.locator('.db-conn').waitFor();
  ok(`pg overview connection: ${(await text('.db-conn')).slice(0, 120)}`);
  await page.setViewportSize({ width: 1280, height: 1300 });
  await shot('db-overview');
  await page.goto(`${BASE}/projects/${PROJECT_ID}/resources/${EXT_ID}`);
  await page.locator('.db-conn').waitFor();
  ok(`external overview: ${(await text('.ov-status')).slice(0, 80)}; sidebar: ${(await page.locator('.sidebar a, .sidebar button').allInnerTexts()).filter(t => ['Overview', 'Environment Variables', 'Automations', 'Logs', 'Terminal'].includes(t.trim())).join(', ')}`);
  await shot('external');
  if (errors.length) throw new Error('browser errors: ' + errors.join(' | '));
} catch (e) {
  console.log('✗', e.message.split('\n')[0]);
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/imp-fail.png` });
  process.exitCode = 1;
} finally {
  await browser.close();
}

// Checks Settings › General, Updates, Users & Permissions (invite → join in a second browser) and the Audit Log.
import { chromium } from 'playwright';

const BASE = process.env.IGNITION_URL;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
page.setDefaultTimeout(30_000);
page.on('pageerror', e => console.log('[browser error]', e.message));
const ok = msg => console.log('✓', msg);
const RUN = Date.now() % 100000;
const NAME = `Garage ${RUN % 10000}`;
const GUEST = `Grace ${RUN}`, GUEST_EMAIL = `grace${RUN}@test.local`;
const field = label => page.locator('.settings-field', { has: page.getByText(label, { exact: true }) });

try {
  await page.goto(`${BASE}/settings/general`);
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password').fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await field('Instance ID').locator('code').waitFor();
  if (await page.getByText('Preview with sample data').count()) throw new Error('general is still a preview');
  ok(`about: ${(await field('Version').innerText()).replace(/\s+/g, ' ')} · ${(await field('Docker').innerText()).replace(/\s+/g, ' ')}`);
  await page.locator('#name').fill(NAME);
  await page.locator('#healthSeconds').fill(String(5 + Date.now() % 20));
  await page.getByRole('button', { name: 'Save changes' }).click();
  await page.getByText('Saved', { exact: true }).waitFor();
  await page.waitForFunction(n => document.title.endsWith(n), NAME);
  ok(`renamed without a reload: tab title "${await page.title()}"`);
  await page.goto(`${BASE}/projects`);
  await page.locator('.workspace', { hasText: NAME }).waitFor();
  ok(`sidebar shows "${NAME}"`);

  await page.goto(`${BASE}/settings/updates`);
  await page.getByRole('button', { name: 'Check for updates' }).click();
  await page.getByText(/Couldn't check for updates|Up to date|Update available/).first().waitFor();
  ok(`update check: ${(await page.locator('.notice, .version-pill').first().innerText()).replace(/\s+/g, ' ').slice(0, 110)}`);
  ok(`update command: ${await page.locator('.update-command code').innerText()}`);

  await page.goto(`${BASE}/settings/users-and-permissions`);
  await page.locator('.user-row').first().waitFor();
  if (await page.getByText('Preview with sample data').count()) throw new Error('users is still a preview');
  await page.getByRole('button', { name: 'Invite', exact: true }).click();
  await page.locator('#invite-email').fill(GUEST_EMAIL);
  await page.getByRole('dialog').getByRole('button', { name: 'Role' }).click();
  await page.getByRole('option', { name: /Viewer/ }).click();
  await page.getByRole('button', { name: 'Create invite link' }).click();
  const url = await page.locator('.link-box code').innerText();
  ok(`invite link: ${url.replace(/[^/]+$/, '…')}`);
  await page.getByRole('button', { name: 'Done' }).click();
  await page.locator('.user-row.invited', { hasText: GUEST_EMAIL }).waitFor();
  ok('pending invite listed');

  // The invitee opens the link in their own browser.
  const guest = await browser.newPage({ viewport: { width: 420, height: 860 } });
  await guest.goto(url.replace(/^https?:\/\/[^/]+/, BASE));
  await guest.getByText(`Join ${NAME}`).waitFor();
  await guest.getByLabel('Your name').fill(GUEST);
  await guest.getByLabel('Password', { exact: true }).fill('grace test password');
  await guest.getByLabel('Confirm password').fill('grace test password!');
  await guest.getByRole('button', { name: 'Create my account' }).click();
  await guest.getByText("The passwords don't match.").waitFor();
  await guest.getByLabel('Confirm password').fill('grace test password');
  await guest.getByRole('button', { name: 'Create my account' }).click();
  await guest.locator('.workspace', { hasText: NAME }).waitFor();
  if (new URL(guest.url()).pathname.startsWith('/join')) throw new Error('join URL not cleared');
  ok('invitee joined and is signed in');
  await guest.close();

  await page.reload();
  const grace = page.locator('.user-row', { hasText: GUEST });
  await grace.waitFor();
  await grace.getByRole('button', { name: /Role for/ }).click();
  await page.getByRole('option', { name: /Developer/ }).click();
  await page.getByText(`${GUEST} is now a developer`).waitFor();
  ok('role changed to developer');
  const meRow = page.locator('.user-row', { hasText: 'You' });
  if (await meRow.getByRole('button', { name: /Role for/ }).count()) throw new Error('own role should be locked');
  ok('own role is locked');
  await grace.getByRole('button', { name: /Actions for/ }).click();
  await page.getByRole('menuitem', { name: 'Create password reset link' }).click();
  await page.getByRole('dialog').locator('.link-box code').waitFor();
  ok('reset link created');
  await page.getByRole('button', { name: 'Done' }).click();
  await grace.getByRole('button', { name: /Actions for/ }).click();
  await page.getByRole('menuitem', { name: 'Suspend' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Suspend' }).click();
  await grace.getByText('Suspended').first().waitFor();
  ok('suspended');
  await page.screenshot({ path: '/shots/users.png', fullPage: true });

  await page.goto(`${BASE}/settings/audit-log`);
  await page.locator('.audit-event').first().waitFor();
  ok(`audit: ${await page.locator('.audit-event').count()} events, first: ${(await page.locator('.audit-row').first().innerText()).replace(/\s+/g, ' ')}`);
  await page.getByRole('button', { name: 'Activity' }).click();
  await page.getByRole('option', { name: 'Users' }).click();
  await page.waitForFunction(() => { const a = [...document.querySelectorAll('.audit-action')].map(x => x.textContent); return a.length > 0 && !a.some(t => /Created a resource|Created a project|Changed general|Checked for updates|Set up Ignition|Deployed/.test(t)); });
  ok(`filtered to users: ${(await page.locator('.audit-action').allInnerTexts()).join(', ')}`);
  await page.locator('.audit-row').first().click();
  await page.locator('.audit-detail').getByText('IP address').waitFor();
  ok('event details expand');
  const href = await page.getByRole('link', { name: 'Export CSV' }).getAttribute('href');
  const csv = await (await page.request.get(BASE + href)).text();
  if (!csv.startsWith('id,time,actor,action')) throw new Error('csv');
  ok(`CSV export: ${csv.trim().split('\n').length - 1} rows (same filter)`);
  await page.screenshot({ path: '/shots/audit.png', fullPage: true });

  await page.setViewportSize({ width: 390, height: 844 });
  for (const p of ['general', 'updates', 'users-and-permissions', 'audit-log']) {
    await page.goto(`${BASE}/settings/${p}`);
    await page.waitForTimeout(800);
    if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw new Error(`horizontal scroll on phone: ${p}`);
  }
  ok('all four pages fit at phone width');
} catch (e) {
  console.log('✗', e.message);
  await page.screenshot({ path: '/shots/admin-fail.png', fullPage: true });
  process.exitCode = 1;
}
await browser.close();

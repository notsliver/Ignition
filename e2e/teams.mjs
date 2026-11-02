// Run against disposable fixtures only. The API may queue jobs; do not attach a worker.
// Fixture JSON: session, user, teamA, teamB, resourceA, resourceB. The user is an owner
// of both teams; B has a second owner so revoking this membership is permitted.
import { chromium } from 'playwright';
import { readFile } from 'node:fs/promises';
import assert from 'node:assert/strict';

if (!process.env.IGNITION_TEST_TEAMS_FIXTURE) throw new Error('IGNITION_TEST_TEAMS_FIXTURE must name disposable team fixtures');
const fixture = JSON.parse(await readFile(process.env.IGNITION_TEST_TEAMS_FIXTURE, 'utf8'));
const base = process.env.IGNITION_URL ?? 'http://127.0.0.1:55440';
const browser = await chromium.launch({ executablePath: process.env.IGNITION_BROWSER_EXECUTABLE || undefined });
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
await context.addCookies([{ name: 'ignition_session', value: fixture.session, url: base }]);
const page = await context.newPage();
page.setDefaultTimeout(15000);
const errors = [];
page.on('pageerror', e => errors.push(e.message));
const teamUrl = (team, path) => `${base}/teams/${team}${path}`;
const heading = text => page.locator('.page-header h1').filter({ hasText: text });

try {
  await page.goto(teamUrl(fixture.teamA, '/resources'));
  await page.getByRole('button', { name: 'Open only-team-a', exact: true }).waitFor();
  assert.equal(await page.getByText('only-team-b', { exact: true }).count(), 0);
  await page.getByRole('button', { name: 'Open only-team-a', exact: true }).click();
  await heading('only-team-a').waitFor();
  assert.equal(new URL(page.url()).pathname, `/teams/${fixture.teamA}/resources/${fixture.resourceA}`);
  await page.reload();
  await heading('only-team-a').waitFor();
  console.log('PASS standalone resource navigation and reload');
  await page.locator('.sidebar').getByRole('button',{name:'Advanced Settings',exact:true}).click();
  await heading('Advanced Settings').waitFor();
  assert.equal(await page.getByRole('button',{name:/^Change project/}).isEnabled(),true);
  console.log('PASS team ownership enables editing independently of the instance role');

  await page.getByRole('button', { name: 'Browser team A', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Browser team B', exact: true }).click();
  await heading('Dashboard').waitFor();
  assert.equal(new URL(page.url()).pathname, `/teams/${fixture.teamB}/dashboard`);
  await page.getByRole('button', { name: 'Resources', exact: true }).first().click();
  await page.getByRole('button', { name: 'Open only-team-b', exact: true }).waitFor();
  assert.equal(await page.getByText('only-team-a', { exact: true }).count(), 0);
  console.log('PASS switching teams clears resource context and data');

  await page.getByRole('button', { name: 'Dashboard', exact: true }).click();
  await page.getByRole('button', { name: 'Deploy application', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Start with Hello World!', exact: true }).click();
  await page.getByRole('button', { name: 'Project', exact: true }).waitFor();
  assert.match(await page.getByRole('button', { name: 'Project', exact: true }).innerText(), /No project/);
  await page.getByRole('button', { name: 'Continue', exact: true }).click();
  await page.getByRole('button', { name: 'Deploy', exact: true }).waitFor();
  const created = page.waitForResponse(r => r.url() === `${base}/api/teams/${fixture.teamB}/resources` && r.request().method() === 'POST');
  await page.getByRole('button', { name: 'Deploy', exact: true }).click();
  const response = await created;
  assert.equal(response.status(), 201);
  const { resource } = await response.json();
  assert.equal(resource.projectId, null);
  assert.equal(resource.teamId, fixture.teamB);
  await heading('hello-world').waitFor();
  console.log('PASS dashboard creation without a project');

  const secondTab = await context.newPage();
  await secondTab.goto(teamUrl(fixture.teamA, '/resources'));
  await secondTab.getByRole('button', { name: 'Open only-team-a', exact: true }).waitFor();
  await page.goto(teamUrl(fixture.teamB, '/resources'));
  await page.getByRole('button', { name: 'Open only-team-b', exact: true }).waitFor();
  assert.equal(new URL(secondTab.url()).pathname, `/teams/${fixture.teamA}/resources`);
  assert.equal(await secondTab.getByText('only-team-b', { exact: true }).count(), 0);
  console.log('PASS independent team scope across browser tabs');

  const revoked = await context.request.delete(`${base}/api/teams/${fixture.teamB}/members/${fixture.user}`, { headers: { Origin: base } });
  assert.equal(revoked.status(), 204);
  await page.getByRole('heading', { name: 'Choose your team', exact: true }).waitFor();
  assert.equal(await page.getByText('only-team-b', { exact: true }).count(), 0);
  console.log('PASS revoked membership discards cached workloads');
  assert.deepEqual(errors, []);
} finally {
  await browser.close();
}



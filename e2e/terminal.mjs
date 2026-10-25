// Drives the real terminal: bash in nginx (Debian), sh in redis (Alpine), and the "no shell" case for a
// scratch image. Reads what xterm renders, so it checks the full browser → WebSocket → docker exec path.
import { chromium } from 'playwright';

const { IGNITION_URL: BASE, PROJECT, NGINX, REDIS, SCRATCH } = process.env;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
page.setDefaultTimeout(30_000);
page.on('pageerror', e => console.log('[browser error]', e.message));
const ok = msg => console.log('✓', msg);
const screen = () => page.locator('.xterm-rows').innerText();
async function waitFor(text) {
  await page.waitForFunction(t => document.querySelector('.xterm-rows')?.innerText.includes(t), text, { timeout: 20_000 });
}
async function run(cmd) {
  await page.locator('.term-host').click();
  await page.keyboard.type(cmd);
  await page.keyboard.press('Enter');
}
const open = id => page.goto(`${BASE}/projects/${PROJECT}/resources/${id}/terminal`);

try {
  await open(NGINX);
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password').fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.locator('.term-status', { hasText: 'Connected' }).waitFor();
  if (await page.getByText('Preview with sample data').count()) throw new Error('still a preview');
  ok(`status: ${await page.locator('.term-status').innerText()}`);

  await run('echo "answer=$((6*7)) shell=$0"');
  await waitFor('answer=42 shell=bash');
  ok('bash in nginx: arithmetic and $0');
  await run('cat /etc/os-release | head -1; tty');
  await waitFor('/dev/pts/');
  ok('real TTY (/dev/pts)');
  await run('stty size');
  await page.waitForFunction(() => /\n\s*\d+ \d+\s*\n/.test(document.querySelector('.xterm-rows').innerText));
  const before = (await screen()).match(/(\d+) (\d+)\s*$/m)?.slice(1).join('x');
  await page.getByRole('button', { name: 'Full size' }).click();
  await page.waitForTimeout(400);
  await run('clear; stty size');
  await page.waitForFunction(b => { const m = document.querySelector('.xterm-rows').innerText.match(/(\d+) (\d+)/); return m && `${m[1]}x${m[2]}` !== b; }, before);
  ok(`resize reaches the container: ${before} → ${(await screen()).match(/(\d+) (\d+)/).slice(1).join('x')}`);
  await page.getByRole('button', { name: 'Exit full size' }).click();

  await run('printf "\\033[31mred\\033[0m\\n"');
  await waitFor('red');
  ok('ANSI colours render');
  await run('exit 3');
  await page.locator('.term-overlay', { hasText: 'The shell exited with code 3.' }).waitFor();
  ok('exit code reported');
  await page.getByRole('button', { name: 'Reconnect' }).click();
  await page.locator('.term-status', { hasText: 'Connected' }).waitFor();
  ok('reconnected');

  await page.getByRole('switch', { name: 'Open as root' }).click();
  await page.locator('.term-status', { hasText: 'as root' }).waitFor();
  await run('echo uid=$(id -u)');
  await waitFor('uid=0');
  ok('root session (admin)');
  await page.screenshot({ path: '/shots/terminal.png', fullPage: true });

  await open(REDIS);
  await page.locator('.term-status', { hasText: 'Connected' }).waitFor();
  await run('echo "shell=$0"; redis-cli ping');
  await waitFor('PONG');
  ok(`Alpine: falls back to sh and runs redis-cli (${(await screen()).match(/shell=\S+/)?.[0]})`);

  await open(SCRATCH);
  await page.locator('.term-overlay', { hasText: 'no shell' }).waitFor();
  ok(`scratch image: ${(await page.locator('.term-overlay span').innerText()).slice(0, 80)}…`);

  await page.setViewportSize({ width: 390, height: 844 });
  await open(REDIS);
  await page.locator('.term-status', { hasText: 'Connected' }).waitFor();
  if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw new Error('horizontal scroll on phone');
  ok('phone width fits');
} catch (e) {
  console.log('✗', e.message);
  await page.screenshot({ path: '/shots/terminal-fail.png', fullPage: true });
  process.exitCode = 1;
}
await browser.close();

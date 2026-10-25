// Checks the Metrics page against a running stack that already has a deployed, running resource.
import { chromium } from 'playwright';

const BASE = process.env.IGNITION_URL;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
page.setDefaultTimeout(20_000);
page.on('pageerror', e => console.log('[browser error]', e.message));
try {
  await page.goto(BASE);
  await page.getByLabel('Email').fill(process.env.IGNITION_EMAIL);
  await page.getByLabel('Password').fill(process.env.IGNITION_PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.getByRole('button', { name: 'Projects', exact: true }).click();
  await page.locator('.project-card').first().click();
  await page.locator('.resource-row strong').first().click();
  await page.locator('.sidebar').getByRole('button', { name: 'Metrics', exact: true }).click();
  await page.getByText(/Last sample/).waitFor();
  await page.getByRole('button', { name: '15m' }).click();
  await page.locator('.metric-card strong', { hasText: '%' }).waitFor();
  if (await page.getByText('Preview with sample data').count()) throw new Error('Metrics still shows the sample-data banner');
  const values = await page.locator('.metric-card header strong').allInnerTexts();
  console.log('chart values:', values.join(' | '));
  // Hover a chart to check the tooltip shows a real timestamp.
  const chart = page.locator('.metric-card .sparkline').first();
  const box = await chart.boundingBox();
  await page.mouse.move(box.x + box.width - 3, box.y + box.height / 2);
  console.log('tooltip:', await page.locator('.sparkline-tip').first().innerText());
  await page.screenshot({ path: '/shots/metrics.png' });
  const [download] = await Promise.all([page.waitForEvent('download'), page.getByRole('button', { name: 'CSV' }).click()]);
  console.log('csv:', download.suggestedFilename());
  console.log('OK');
} finally {
  await browser.close();
}

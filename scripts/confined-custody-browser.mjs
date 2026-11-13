// One real Settings update in the disposable Local VM, never a production UI.
import { chromium } from '@playwright/test';

const [port, instance] = process.argv.slice(2);
if (!/^[0-9]{4,5}$/.test(port ?? '') || Number(port) < 1024 || Number(port) > 65535 || !/^[A-Za-z0-9_-]{43}$/.test(instance ?? ''))
  throw new Error('fixed fixture listener and instance required');
const origin = `http://127.0.0.1:${port}`;
const browser = await chromium.launch({ headless: true });
try {
  const context = await browser.newContext({ locale: 'en-US', viewport: { width: 1200, height: 850 } });
  await context.route('**/*', route => new URL(route.request().url()).origin === origin ? route.continue() : route.abort());
  const page = await context.newPage();
  page.setDefaultTimeout(30000);
  await page.goto(`${origin}/?instance=${instance}#settings`);
  await page.getByLabel('Owner password', { exact: true }).fill('Disposable custody owner 7WQfE2tV');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  const panel = page.locator('section[aria-labelledby="application-updates-title"]');
  await panel.getByRole('button', { name: 'Check', exact: true }).click();
  await panel.getByRole('button', { name: 'Update', exact: true }).waitFor();
  await panel.getByText('0.1.12', { exact: true }).waitFor();
  await panel.getByRole('button', { name: 'Update', exact: true }).click();
  const response = page.waitForResponse(r => r.url().endsWith('/api/v1/updates/application') &&
    r.request().method() === 'POST' && r.request().postDataJSON()?.action === 'apply');
  await panel.getByRole('button', { name: 'Update now', exact: true }).click();
  const accepted = await response;
  if (!accepted.ok()) throw new Error(`update request refused: HTTP ${accepted.status()}`);
  const operationId = (await accepted.json()).operation?.id;
  if (!/^[a-f0-9]{32}$/.test(operationId ?? '')) throw new Error('update response did not identify the original operation');
  process.stdout.write(JSON.stringify({ result: 'APPLY_SENT', fixtureOnly: true, version: '0.1.12', operationId }) + '\n');
} finally {
  await browser.close();
}

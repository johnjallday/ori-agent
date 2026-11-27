import { test, expect } from '@playwright/test';
import { existsSync, mkdirSync, realpathSync, utimesSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { freshGuidedHQ } from './helpers/fresh-guided-hq';
import { installLocalCdn } from './helpers/offline-cdn';

const evidence = 'tasks/evidence-downloads-manager-routing';

test('the Downloads manager reviews individual destinations without moving the rest of the scan', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_DOWNLOADS_SANDBOX;
  test.skip(
    !sandbox || process.env.ORI_DEMO_SPLIT_PROFILE !== '1',
    'requires a fresh split demo home'
  );
  test.setTimeout(150_000);
  const home = join(realpathSync(sandbox!), 'home');
  expect(home).toMatch(/\/ori-e2e\.[^/]+\/home$/);
  expect(
    (await (await request.get('/api/settings/workspace-root')).json()).default_workspace_root
  ).toBe(join(home, 'Ori Workspaces'));
  const root = join(home, 'Downloads');
  const receipts = join(home, 'Travel', 'Receipts');
  mkdirSync(root, { recursive: true });
  mkdirSync(receipts, { recursive: true });
  const seed = (name: string) => {
    const path = join(root, name);
    writeFileSync(path, 'synthetic ' + name);
    const old = new Date(Date.now() - 86400000);
    utimesSync(path, old, old);
  };
  const post = async (url: string, data = {}) => {
    const r = await request.post(url, { data });
    expect(r.ok(), await r.text()).toBeTruthy();
    return r.json();
  };
  seed('hotel-receipt.pdf');
  await installLocalCdn(page);
  await freshGuidedHQ(request);
  const agents = '/api/personal-assistant/folder-agent';
  const { card } = await post(agents + '/choose', { chip: 'downloads' });
  const { folder_agent: manager } = await post(agents + '/start', {
    choice_token: card.choice_token
  });
  const base = `/api/workspaces/${manager.workspace_id}/file-janitor`;
  await expect
    .poll(
      async () =>
        (await (await request.get(agents + '/' + manager.workspace_id)).json()).folder_agent.state
    )
    .toBe('reported');
  await page.goto(manager.route + '?view=details');
  await page.getByRole('button', { name: 'Open in chat', exact: true }).click();
  const link = page.getByRole('link', { name: 'Review where downloads belong' });
  const url = await link.getAttribute('href');
  await link.click();
  await expect(page.getByRole('button', { name: 'Install Filing', exact: true })).toBeVisible();
  expect(
    (await (await request.get(base + '?review=downloads')).json()).status.downloads_review
  ).toBe('install');
  await page.screenshot({ path: `${evidence}/setup.png` });
  await page.getByRole('button', { name: 'Install Filing', exact: true }).click();
  await page.getByRole('button', { name: 'Allow access to Downloads' }).click();
  await page.locator('#fileJanitorScan').click();
  const row = (name: string) =>
    page
      .locator('.fj-route-file')
      .filter({ has: page.getByRole('heading', { name, exact: true }) });
  await row('hotel-receipt.pdf').getByRole('button', { name: 'Choose a folder' }).click();
  const folder = page.locator('#filingFolderChoice');
  await folder.getByRole('textbox').fill(receipts);
  await folder.getByRole('button', { name: 'Preview folder' }).click();
  await expect(folder).toContainText('File to Travel/Receipts?');
  expect(existsSync(join(root, 'hotel-receipt.pdf'))).toBeTruthy();
  await folder.getByRole('button', { name: 'File', exact: true }).click();
  const receipt = page.locator('#fileJanitorReceipt');
  await expect(receipt).toContainText('1 file filed');
  const saved = page.waitForResponse(
    r => r.request().method() === 'POST' && r.url().endsWith('/example')
  );
  await receipt.getByRole('button', { name: 'Suggest this folder for similar files' }).click();
  expect((await saved).ok()).toBe(true);
  seed('hotel-receipt-may.pdf');
  seed('client-proof.pdf');
  seed('unclear.pdf');
  await post(base + '/scan');
  await page.goto(url!);
  const repeat = row('hotel-receipt-may.pdf');
  await expect(repeat).toContainText('You filed hotel-receipt.pdf in Travel/Receipts.');
  await expect(repeat.getByRole('button', { name: 'Choose another folder' })).toBeVisible();
  await page.screenshot({ path: `${evidence}/remembered-folder.png` });
  await repeat.getByRole('button', { name: 'Review filing' }).click();
  await expect(folder).toContainText('File to Travel/Receipts?');
  expect(existsSync(join(root, 'hotel-receipt-may.pdf'))).toBe(true);
  await folder.getByRole('button', { name: 'File', exact: true }).click();
  await expect(receipt).toContainText('1 file filed');
  expect(existsSync(join(receipts, 'hotel-receipt-may.pdf'))).toBe(true);
  expect(existsSync(join(root, 'client-proof.pdf'))).toBe(true);
  await row('unclear.pdf').getByRole('button', { name: 'Leave in Downloads' }).click();
  await expect(page.locator('.fj-batch-scan-status')).toContainText(
    'unclear.pdf was left in Downloads.'
  );
  expect(existsSync(join(root, 'unclear.pdf'))).toBe(true);
});

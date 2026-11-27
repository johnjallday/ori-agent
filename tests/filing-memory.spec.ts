import { test, expect } from '@playwright/test';
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  realpathSync,
  utimesSync,
  writeFileSync
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { filingWorkspace } from './helpers/filing-workspace';

test('a picked folder is a reviewed destination, not an attachment or an automatic rule', async ({
  page,
  request
}) => {
  const fixture = realpathSync(mkdtempSync(join(tmpdir(), 'ori-filing-memory-')));
  const root = join(fixture, 'Downloads');
  const receipts = join(fixture, 'Documents', 'Travel', 'Receipts');
  mkdirSync(root);
  mkdirSync(receipts, { recursive: true });
  const seed = (name: string) => {
    const file = join(root, name);
    const settled = new Date(Date.now() - 86400000);
    writeFileSync(file, 'synthetic ' + name);
    utimesSync(file, settled, settled);
  };
  seed('hotel-receipt.pdf');
  const ws = await filingWorkspace(request, 'Downloads', root);
  const scan = async () => {
    expect((await request.post(ws.base + '/scan')).ok()).toBeTruthy();
  };
  await scan();
  const url =
    '/workspaces/' + encodeURIComponent(ws.slug) + '?view=details&panel=file-janitor&tab=';
  const review = async () => {
    await page.goto(url + 'review');
    await expect(page.locator('#fileJanitorPicture')).toBeVisible();
  };
  await review();
  const row = page
    .locator('li[data-fj-file-row]')
    .filter({ has: page.getByRole('button', { name: 'hotel-receipt.pdf', exact: true }) })
    .first();
  await row.getByRole('button', { name: 'hotel-receipt.pdf', exact: true }).click();
  await row.getByRole('button', { name: 'Choose a folder…' }).click();
  const dialog = page.locator('#filingFolderChoice');
  await dialog.getByRole('textbox').fill(receipts);
  await dialog.getByRole('button', { name: 'Preview folder' }).click();
  await expect(dialog.getByRole('button', { name: 'File', exact: true })).toBeVisible();
  expect(existsSync(join(root, 'hotel-receipt.pdf'))).toBe(true);
  expect(existsSync(join(receipts, 'hotel-receipt.pdf'))).toBe(false);
  await expect(dialog.locator('.fj-destination-path')).toHaveText(receipts);
  await dialog.getByRole('button', { name: 'File', exact: true }).click();
  const receipt = page.locator('#fileJanitorReceipt');
  await expect(receipt).toContainText('1 file filed');
  await expect(receipt).toContainText('Receipts');
  const saved = page.waitForResponse(
    r => r.request().method() === 'POST' && r.url().endsWith('/example')
  );
  await receipt.getByRole('button', { name: 'Suggest this folder for similar files' }).click();
  expect((await saved).ok()).toBe(true);
  seed('hotel-receipt-may.pdf');
  seed('signed-lease.pdf');
  await scan();
  await review();
  const suggestions = page
    .locator('section.fj-frame')
    .filter({ has: page.getByRole('heading', { name: 'Suggested destinations' }) });
  await expect(suggestions).toContainText('hotel-receipt-may.pdf');
  await expect(suggestions).not.toContainText('signed-lease.pdf');
  await suggestions.getByRole('button', { name: 'Review suggestion' }).click();
  await expect(dialog).toContainText('File to Receipts?');
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  expect(existsSync(join(root, 'hotel-receipt-may.pdf'))).toBe(true);
  expect(existsSync(join(root, 'signed-lease.pdf'))).toBe(true);
  const res = await request.get(`/api/workspaces/${ws.id}`);
  const body = await res.json();
  const workspace = body.workspace || body.folder || body;
  expect(workspace.directory_references.map((ref: { path: string }) => ref.path)).not.toContain(
    receipts
  );
  expect((await (await request.get(ws.base)).json()).status.settings.paused).toBe(true);
  await page.goto(url + 'history');
  await page.getByRole('searchbox').fill('hotel receipt');
  await page.getByRole('button', { name: 'Search history' }).click();
  await expect(page.locator('.fj-history-list')).toContainText('Receipts/hotel-receipt.pdf');
  await page.getByRole('button', { name: 'Clear search' }).click();
  await page.locator('#fileJanitorLastFilingUndo').click();
  await expect.poll(() => existsSync(join(root, 'hotel-receipt.pdf'))).toBe(true);
  const history = await request.get(ws.base + '/history?filename=hotel%20receipt');
  expect((await history.json()).matches[0].outcome).toContain('Undo verified');
});

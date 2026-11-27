import { test, expect } from '@playwright/test';
import { existsSync, mkdirSync, realpathSync, utimesSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { freshGuidedHQ } from './helpers/fresh-guided-hq';
import { filingWorkspace } from './helpers/filing-workspace';
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
  const acmeRoot = join(home, 'Acme');
  mkdirSync(acmeRoot);
  const acme = await filingWorkspace(request, 'Acme', acmeRoot, 'Acme');
  const { connections } = await post(base + '/connections', {
    receiver_workspace_id: acme.id,
    kinds: ['documents']
  });
  const connection = connections.sends_to[0].id;
  await page.goto(url!);
  await expect(repeat).toContainText('Is this for your travel records or for Acme?');
  await expect(repeat).toContainText('top level');
  await page.screenshot({ path: `${evidence}/two-destinations.png` });
  await repeat.getByRole('button', { name: 'Review filing' }).click();
  await expect(folder).toContainText('File to Travel/Receipts?');
  expect(existsSync(join(root, 'hotel-receipt-may.pdf'))).toBe(true);
  await folder.getByRole('button', { name: 'File', exact: true }).click();
  await expect(receipt).toContainText('1 file filed');
  expect(existsSync(join(receipts, 'hotel-receipt-may.pdf'))).toBe(true);
  const repeatFiling = (await (await request.get(base + '/filings?limit=1')).json()).filings[0]
    .filing_id;
  expect(existsSync(join(root, 'client-proof.pdf'))).toBe(true);
  await row('unclear.pdf').getByRole('button', { name: 'Leave in Downloads' }).click();
  await expect(page.locator('.fj-batch-scan-status')).toContainText(
    'unclear.pdf was left in Downloads.'
  );
  expect(existsSync(join(root, 'unclear.pdf'))).toBe(true);
  const proof = row('client-proof.pdf');
  await proof.getByRole('button', { name: 'Send to Acme' }).click();
  const send = page.getByRole('dialog', { name: 'Send client-proof.pdf to Acme?' });
  await expect(send).toContainText('top level of Acme');
  expect(existsSync(join(root, 'client-proof.pdf'))).toBe(true);
  await send.screenshot({ path: `${evidence}/confirm-send.png` });
  await send.getByRole('button', { name: 'Confirm send' }).click();
  await expect(receipt).toContainText('1 went to Acme');
  expect(existsSync(join(acmeRoot, 'client-proof.pdf'))).toBe(true);
  expect(existsSync(join(root, 'client-proof.pdf'))).toBe(false);
  expect(existsSync(join(root, 'unclear.pdf'))).toBe(true);
  expect(existsSync(join(receipts, 'hotel-receipt-may.pdf'))).toBe(true);

  await page.goto(`/workspaces/${acme.slug}?view=details&panel=file-janitor&tab=review`);
  const arrival = page.locator('#fileJanitorArrivalsHost');
  await expect(arrival).toContainText('1 file arrived from Downloads');
  await expect(arrival).toContainText('client-proof.pdf');
  await arrival.screenshot({ path: `${evidence}/arrival.png` });
  await arrival.getByRole('button', { name: 'Review local filing' }).click();
  await expect(page.locator('#fileJanitorPicture')).toContainText('client-proof.pdf');
  await expect(page.locator('#fileJanitorApprove')).toHaveText('File 1 file');
  expect(existsSync(join(acmeRoot, 'client-proof.pdf'))).toBe(true);
  expect((await (await request.get(agents + '/' + acme.id)).json()).folder_agent).toBeNull();
  expect((await (await request.get(acme.base + '/filings')).json()).filings || []).toHaveLength(0);
  await page.screenshot({ path: `${evidence}/local-review.png` });
  await arrival.getByRole('button', { name: 'Undo this send' }).click();
  await expect(arrival).toContainText('back in Downloads');
  expect(existsSync(join(root, 'client-proof.pdf'))).toBe(true);
  expect(existsSync(join(acmeRoot, 'client-proof.pdf'))).toBe(false);
  const undo = await request.post(base + `/filings/${repeatFiling}/undo`);
  expect(undo.ok(), await undo.text()).toBe(true);
  expect(existsSync(join(root, 'hotel-receipt-may.pdf'))).toBe(true);
  expect(existsSync(join(receipts, 'hotel-receipt-may.pdf'))).toBe(false);

  seed('client-proof-revised.pdf');
  await post(base + '/scan');
  await page.goto(url!);
  await row('client-proof-revised.pdf').getByRole('button', { name: 'Send to Acme' }).click();
  const check = page.getByRole('button', { name: 'Confirm send' });
  await expect(check).toBeVisible();
  const before = (await (await request.get(base + '/filings')).json()).filings;
  const revoke = await request.post(acme.base + `/connections/${connection}/turn-off`);
  expect(revoke.ok(), await revoke.text()).toBe(true);
  await check.click();
  await expect(
    page.getByRole('dialog', { name: 'Send client-proof-revised.pdf to Acme?' })
  ).toContainText('Nothing was sent');
  await page.screenshot({ path: `${evidence}/revoked.png` });
  expect(existsSync(join(root, 'client-proof-revised.pdf'))).toBe(true);
  expect(existsSync(join(acmeRoot, 'client-proof-revised.pdf'))).toBe(false);
  expect((await (await request.get(base + '/filings')).json()).filings).toEqual(before);
});

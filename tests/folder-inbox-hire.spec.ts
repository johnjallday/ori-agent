import { expect, test, type Page } from '@playwright/test';
import { existsSync, readdirSync, readFileSync, realpathSync, mkdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { openFolderAttach } from './helpers/assistant-menus';
import { installLocalCdn } from './helpers/offline-cdn';
import { inboxFirstBatchItems } from './helpers/folder-hire-report-cases';

// scripts/e2e-inbox-folder.sh owns a disposable no-provider workspace, its HQ
// and its known-folder chips. These stories go through the assistant's actual
// attachment (not the similarly named folder-agent hire on the Agents page).
const sandbox = process.env.ORI_INBOX_SANDBOX || '';
const home = existsSync(join(sandbox, 'home', 'Downloads')) ? join(sandbox, 'home') : sandbox;
test.skip(!sandbox, 'requires scripts/e2e-inbox-folder.sh');
test.describe.configure({ mode: 'serial' });
test.setTimeout(90_000);
const panel = (page: Page) => page.locator('#personalAssistantPanel');
async function get(page: Page, path: string) {
  const r = await page.request.get(path);
  expect(r.ok(), await r.text()).toBeTruthy();
  return r.json();
}
async function attach(page: Page, name: string, entry = '/') {
  await page.goto(entry);
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel.open(
      document.getElementById('personalAssistantLauncher')
    )
  );
  await openFolderAttach(page);
  await panel(page).locator('.personal-assistant-panel__chip', { hasText: name }).first().click();
}
async function expectCurator(page: Page, name: string) {
  await expect(panel(page).locator('.folder-visual__source h4')).toHaveText(name);
  await expect(panel(page).locator('.folder-visual__agent strong')).toHaveText('File Curator');
}
async function hire(page: Page) {
  const count = (await get(page, '/api/workspaces')).folders.length;
  await panel(page).getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  const review = page.locator('#assistedWorkspacePlacementPanel');
  const confirm = review.getByRole('button', { name: 'Hire and create workspace', exact: true });
  await expect(confirm).toBeEnabled();
  await expect(review).toContainText(
    'No Filing access, scan, monitoring or Filed/ folder is included'
  );
  await expect(review.getByRole('checkbox')).toHaveCount(0);
  expect((await get(page, '/api/workspaces')).folders.length).toBe(count);
  const posted = page.waitForResponse(
    r => r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith('/janitor-prepare')
  );
  await confirm.click();
  const response = await posted;
  expect(response.ok(), await response.text()).toBeTruthy();
  const body = await response.json();
  expect(body.outcome.state).toBe('hired');
  expect(body.preparation).toBeUndefined();
  await expect(review.getByRole('heading', { name: 'Manager hired' })).toBeVisible();
  await expect(review).toContainText('Filing was not installed. No scan was run.');
  return { review, id: body.workspace_id, slug: body.folder_slug };
}
test.beforeEach(async ({ page }) => {
  expect(
    realpathSync(sandbox).startsWith(resolve(realpathSync(tmpdir()), 'ori-inbox-'))
  ).toBeTruthy();
  await installLocalCdn(page);
});

test('Home and composer show the reviewed hire without installing Filing', async ({ page }) => {
  for (const entry of ['/', '/agents']) {
    await attach(page, 'Downloads', entry);
    await expectCurator(page, 'Downloads');
    await expect(panel(page).getByLabel(/Also watch this folder/)).not.toBeVisible();
    await expect(
      panel(page).getByRole('button', { name: 'Review setup', exact: true })
    ).toBeVisible();
    await panel(page)
      .getByRole('button', { name: /Keep chatting/ })
      .click();
  }
  expect(
    (await get(page, '/api/workspaces')).folders.some(
      (w: { name: string }) => w.name === 'Downloads'
    )
  ).toBe(false);
  expect(existsSync(join(home, 'Downloads', 'Filed'))).toBe(false);
});

test('hire, reload and review are inert; only Build grants Filing and scans the saved Downloads pile', async ({
  page
}, info) => {
  const root = join(home, 'Downloads');
  const names = readdirSync(root).sort();
  const original = names.map(n => readFileSync(join(root, n)));
  expect(names.length).toBe(25);
  await attach(page, 'Downloads');
  await expectCurator(page, 'Downloads');
  const { review, id } = await hire(page);
  const status = async () => (await get(page, `/api/workspaces/${id}/file-janitor`)).status;
  expect((await status()).applies).toBe(false);
  expect(readdirSync(root).sort()).toEqual(names);
  expect(await inboxFirstBatchItems(page.request)).toHaveLength(0);
  await review.getByRole('button', { name: 'Open workspace Map' }).click();
  await expect(page).toHaveURL(/\/workspaces\/downloads\?mode=map/);
  await page.reload();
  const close = page.locator('#personalAssistantClose');
  if (await close.isVisible()) await close.click();
  await expect(page.locator('.filing-station-plot')).toHaveText('25');
  await page.getByRole('button', { name: 'Review station build', exact: true }).click();
  const card = page.getByRole('dialog', { name: 'Filing', exact: true });
  await expect(
    card.getByRole('heading', { name: 'Build a Filing station for Downloads' })
  ).toBeVisible();
  expect((await status()).applies).toBe(false);
  expect(existsSync(join(root, 'Filed'))).toBe(false);
  await card.screenshot({ path: info.outputPath('assistant-inbox-build.png') });
  const post = page.waitForResponse(
    r => r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith('/prepare-review')
  );
  await card.getByRole('button', { name: 'Build the station', exact: true }).press('Enter');
  const r = await post;
  expect(r.ok(), await r.text()).toBeTruthy();
  expect((await r.json()).preparation.result.eligible_count).toBe(25);
  await expect(page.locator('[data-cmd-hq-station="file-janitor"]:visible')).toContainText(
    '25 to review'
  );
  expect(readdirSync(root).sort()).toEqual(names);
  expect(names.map(n => readFileSync(join(root, n)))).toEqual(original);
});

test('a mixed folder shows the reason for its lead; swapping to the Curator creates nothing', async ({
  page
}) => {
  const count = (await get(page, '/api/workspaces')).folders.length;
  await attach(page, 'Desktop');
  const card = panel(page);
  const swap = card.getByText('Or: hire File Curator to tidy the loose files instead');
  await expect(swap).toBeVisible();
  await card.locator('#personalAssistantFolderLeadWhy summary').click();
  await expect(card.locator('#personalAssistantFolderLeadReason')).toContainText(
    /5 loose files of 5 kinds sit beside a Go module/
  );
  await swap.click();
  await expectCurator(page, 'Desktop');
  await card.getByRole('button', { name: /Keep chatting/ }).click();
  await attach(page, 'Documents');
  await expectCurator(page, 'Documents');
  await card.getByText('Or: hire Development Assistant for widget instead').click();
  await expect(card.getByText(/widget looks like a Go module/)).toBeVisible();
  await card.getByText('Or: hire File Curator to tidy the loose files instead').click();
  await expectCurator(page, 'Documents');
  expect((await get(page, '/api/workspaces')).folders.length).toBe(count);
  await card.getByRole('button', { name: /Keep chatting/ }).click();
});

test('a second hire assigns the saved Curator and keeps new shelves untouched', async ({
  page
}) => {
  const root = join(home, 'Documents');
  mkdirSync(join(root, 'Invoices'), { mode: 0o750 });
  const names = readdirSync(root).sort();
  await attach(page, 'Documents');
  await expectCurator(page, 'Documents');
  await expect(panel(page).locator('.folder-visual__agent')).toContainText('Already hired');
  const { review, id } = await hire(page);
  await review.getByRole('button', { name: 'Close', exact: true }).click();
  expect((await get(page, `/api/workspaces/${id}/file-janitor`)).status.applies).toBe(false);
  expect(readdirSync(root).sort()).toEqual(names);
  expect(readdirSync(join(root, 'Invoices'))).toEqual([]);
});

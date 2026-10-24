import { expect, test, type Page } from '@playwright/test';
import { existsSync, readdirSync, rmSync, writeFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';
import {
  expectFirstBatchCard,
  inboxFirstBatchItems,
  type FirstBatchItem
} from './helpers/folder-hire-report-cases';

// Hire File Curator for an inbox folder (Downloads, Desktop, Documents and the
// like), from the attach to the first batch's report, in a real browser against
// the real server and store, with NO model configured.
//
// Run it against the sandbox scripts/demo-inbox-folder.sh serves (it prints
// SANDBOX= and the port):
//
//   ./scripts/demo-inbox-folder.sh --port 8952 --desktop-project-leads
//   ./scripts/e2e.sh --port 8952 --env ORI_INBOX_SANDBOX=<sandbox> tests/folder-inbox-hire.spec.ts
//
// The specs are one story over one sandbox and run in order: Downloads on both
// entries, the report, Assign, already tidies, Not now, a mixed folder's lead and
// swap, and an empty folder's report. Nothing outside the sandbox is touched.
const sandbox = process.env.ORI_INBOX_SANDBOX || '';
test.skip(!sandbox, 'requires the sandbox that scripts/demo-inbox-folder.sh serves');
test.describe.configure({ mode: 'serial' });
test.setTimeout(120_000);

const downloads = join(sandbox, 'Downloads');
const desktop = join(sandbox, 'Desktop');
const panel = (page: Page) => page.locator('.personal-assistant-panel');

async function attachFromHome(page: Page, name: string) {
  await page.goto('/');
  // The drawer stays open across pages; Home's own button is under it.
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.close));
  await page.evaluate(() => (window as any).PersonalAssistantPanel.close());
  await page.locator('#cockpitPrimaryActionBtn').click();
  await page.locator('.personal-assistant-panel__chip', { hasText: name }).first().click();
}

async function openDrawer(page: Page) {
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel.open(
      document.getElementById('personalAssistantLauncher')
    )
  );
}

// The composer's own Add folder: the drawer opens on any page, and the chooser
// offers the same chips.
async function attachFromComposer(page: Page, name: string) {
  await page.goto('/agents');
  await openDrawer(page);
  await page.locator('#personalAssistantFolderChip').click();
  await page.locator('.personal-assistant-panel__chip', { hasText: name }).first().click();
}

async function api<T = any>(page: Page, path: string): Promise<T> {
  const response = await page.request.get(path);
  expect(response.ok(), path).toBeTruthy();
  return response.json();
}

const count = async (page: Page, kind: 'workspaces' | 'agents', name: string) => {
  if (kind === 'workspaces') {
    const body = await api(page, '/api/workspaces');
    return (body.folders || []).filter((w: { name: string }) => w.name === name).length;
  }
  const body = await api(page, '/api/agents');
  return (JSON.stringify(body).match(new RegExp(`"name":"${name}"`, 'g')) || []).length;
};

async function hireAndStart(page: Page, label: RegExp, folder: string) {
  await page.getByRole('button', { name: label }).click();
  await expect(panel(page).getByText(`${folder} is ready`, { exact: true })).toBeVisible({
    timeout: 60_000
  });
}

test.beforeAll(async ({ request }) => {
  await request.post('/api/onboarding/skip');
});

test('no model is configured, and Downloads is answered by a hire card on Home', async ({
  page
}) => {
  await attachFromHome(page, 'Downloads');
  const card = panel(page);
  await expect(card.getByText('Hire File Curator for Downloads')).toBeVisible({ timeout: 30_000 });
  await expect(card.getByText('Nothing to download: File Janitor is built into Ori')).toBeVisible();
  await expect(card.getByText('Not needed for the first batch')).toBeVisible();
  await expect(card.getByLabel('What should it do with it?')).toHaveValue(
    /^Prepare reviewable filing batches/
  );
  await expect(card.getByLabel(/Also watch this folder/)).not.toBeChecked();
  await expect(card.getByRole('button', { name: /Hire & start/ })).toBeVisible();
  const text = await card.innerText();
  expect(text).not.toMatch(/model tokens|Start first look/);
  expect(text).toContain('Linked where it is');
  // Closing the review is not a decision: it leaves no review open for the next attach.
  await card.getByRole('button', { name: /Keep chatting/ }).click();
  // Showing the card changed nothing.
  expect(await count(page, 'workspaces', 'Downloads')).toBe(0);
  expect(await count(page, 'agents', 'File Curator')).toBe(0);
});

test('the composer answers Downloads with the same card, and Not now is this folder only', async ({
  page
}) => {
  await attachFromComposer(page, 'Downloads');
  const card = panel(page);
  await expect(card.getByText('Hire File Curator for Downloads')).toBeVisible({ timeout: 30_000 });
  await card.getByRole('button', { name: /Keep chatting/ }).click();
  await expect(card.getByText('Hire File Curator for Downloads')).toHaveCount(0);
  // Another folder is still offered a hire.
  await attachFromComposer(page, 'Documents');
  await expect(card.getByText(/File Curator (for|to) Documents/)).toBeVisible({ timeout: 30_000 });
  await card.getByRole('button', { name: /Keep chatting/ }).click();
});

test('Hire and start makes one workspace, links the folder metadata-only and reports the batch', async ({
  page
}) => {
  await attachFromHome(page, 'Downloads');
  await expect(panel(page).getByText('Hire File Curator for Downloads')).toBeVisible({
    timeout: 30_000
  });
  const before = readdirSync(downloads).sort();
  await hireAndStart(page, /Hire & start/, 'Downloads');
  await expect(
    panel(page)
      .getByRole('button', { name: 'Review batch' })
      .or(panel(page).getByRole('link', { name: 'Review batch' }))
  ).toBeVisible();
  // The canonical result: one workspace, one agent, Filed/ inside, nothing moved,
  // watching off.
  expect(await count(page, 'workspaces', 'Downloads')).toBe(1);
  expect(await count(page, 'agents', 'File Curator')).toBe(1);
  expect(existsSync(join(downloads, 'Filed'))).toBeTruthy();
  expect(
    readdirSync(downloads)
      .filter(name => name !== 'Filed')
      .sort()
  ).toEqual(before);
  const workspaces = await api(page, '/api/workspaces');
  const id = workspaces.folders.find((w: { name: string }) => w.name === 'Downloads').id;
  const readiness = await api(page, `/api/workspaces/${id}/file-janitor/readiness`);
  expect(readiness.readiness.paused).toBe(true);
  const review = await api(page, `/api/workspaces/${id}/file-janitor/setup-review`);
  expect(review.review.current_mode).toBe('manual');
});

test('the first batch is reported under Needs you on Home and in the drawer, and opens its review', async ({
  page
}) => {
  const items: FirstBatchItem[] = await inboxFirstBatchItems(page.request);
  const item = items.find(entry => entry.title.includes('Downloads'));
  expect(item, 'the Downloads first batch report').toBeTruthy();
  expect(item!.title).toMatch(
    /prepared a first batch for Downloads: \d+ files? proposed for filing, \d+ needs? your review\./
  );
  await expectFirstBatchCard(page, item!, 'batch');
  await page.goto('/');
  await openDrawer(page);
  const link = page
    .locator(`article[data-look-id="${item!.ref.entity_id}"]`)
    .first()
    .getByRole('link', { name: 'Review batch' });
  await link.click();
  await expect(page).toHaveURL(/\/workspaces\/downloads\?panel=file-janitor&batch_id=/);
  await expect(page.getByText(/files ready for review|proposed/).first()).toBeVisible({
    timeout: 30_000
  });
});

test('where the loose files are few the project leads, and the tidy is one link away', async ({
  page
}) => {
  // Desktop is a Go module beside five loose files: not a dump, so the project
  // leads; the swap makes File Curator's card, and nothing is created.
  for (const name of readdirSync(desktop))
    rmSync(join(desktop, name), { recursive: true, force: true });
  mkdirSync(join(desktop, 'tool'), { recursive: true });
  writeFileSync(join(desktop, 'tool', 'go.mod'), 'module example.com/tool\n\ngo 1.22\n');
  writeFileSync(join(desktop, 'tool', 'main.go'), 'package main\n');
  for (const name of ['a.pdf', 'b.png', 'c.zip', 'd.csv', 'e.txt'])
    writeFileSync(join(desktop, name), 'fixture\n');
  const workspacesBefore = (await api(page, '/api/workspaces')).folders.length;
  await attachFromHome(page, 'Desktop');
  const card = panel(page);
  const toTidy = card.getByText('Or: hire File Curator to tidy the loose files instead');
  await expect(toTidy).toBeVisible({ timeout: 30_000 });
  await card.locator('#personalAssistantFolderLeadWhy summary').click();
  await expect(card.locator('#personalAssistantFolderLeadReason')).toContainText(
    /^5 loose files of 5 kinds sit beside a Go module changed on .+: the project is the bigger job\.$/
  );
  await toTidy.click();
  await expect(card.getByText(/File Curator to Desktop|File Curator for Desktop/)).toBeVisible({
    timeout: 30_000
  });
  expect((await api(page, '/api/workspaces')).folders.length).toBe(workspacesBefore);
  await card.getByRole('button', { name: /Keep chatting/ }).click();
});

test('a second inbox folder is an Assign, and the same folder again is already tidied', async ({
  page
}) => {
  // Desktop is empty for this part of the story.
  for (const name of readdirSync(desktop))
    rmSync(join(desktop, name), { recursive: true, force: true });
  await attachFromHome(page, 'Desktop');
  await expect(panel(page).getByText('Assign File Curator to Desktop')).toBeVisible({
    timeout: 30_000
  });
  await expect(panel(page).getByRole('button', { name: /Assign & start/ })).toBeVisible();
  await hireAndStart(page, /Assign & start/, 'Desktop');
  // An empty folder reports that there is nothing to file.
  const items = await inboxFirstBatchItems(page.request);
  const empty = items.find(entry => entry.title === 'Nothing to file in Desktop right now.');
  expect(empty, 'the empty folder report').toBeTruthy();
  await expectFirstBatchCard(page, empty!, 'empty');
  expect(await count(page, 'agents', 'File Curator')).toBe(1);
  // Downloads again: already tidied, nothing created.
  await attachFromHome(page, 'Downloads');
  await expect(panel(page).getByText('File Curator already tidies Downloads')).toBeVisible({
    timeout: 30_000
  });
  await expect(panel(page).getByRole('link', { name: 'Open it' })).toHaveAttribute(
    'href',
    '/workspaces/downloads'
  );
  await expect(
    panel(page).getByRole('button', { name: /Hire & start|Assign & start/ })
  ).toHaveCount(0);
  expect(await count(page, 'workspaces', 'Downloads')).toBe(1);
});

test('Not now takes the report away for good, and only that report', async ({ page }) => {
  const items = await inboxFirstBatchItems(page.request);
  const downloadsItem = items.find(entry => entry.title.includes('Downloads'))!;
  const desktopItem = items.find(entry => entry.title.includes('Desktop'))!;
  await page.goto('/');
  await openDrawer(page);
  const card = page.locator(`article[data-look-id="${downloadsItem.ref.entity_id}"]`).first();
  await card.getByRole('button', { name: 'Not now' }).click();
  await expect(card).toHaveCount(0, { timeout: 30_000 });
  const after = await inboxFirstBatchItems(page.request);
  expect(after.some(entry => entry.ref.entity_id === downloadsItem.ref.entity_id)).toBe(false);
  expect(after.some(entry => entry.ref.entity_id === desktopItem.ref.entity_id)).toBe(true);
});

test('a mixed folder leads with one hire, says why, and swaps without creating anything', async ({
  page
}) => {
  // Documents: a dump beside a Go module, so File Curator leads.
  const workspacesBefore = (await api(page, '/api/workspaces')).folders.length;
  await attachFromHome(page, 'Documents');
  const card = panel(page);
  await expect(card.getByText(/File Curator (for|to) Documents/)).toBeVisible({ timeout: 30_000 });
  await card.locator('#personalAssistantFolderLeadWhy summary').click();
  await expect(card.locator('#personalAssistantFolderLeadReason')).toContainText(
    /\d+ loose files of \d+ kinds sit beside a Go module changed on .+: the loose files are the bigger job\./
  );
  const toProject = card.getByText('Or: hire Development Assistant for widget instead');
  await expect(toProject).toHaveCount(1);
  await toProject.click();
  await expect(card.getByText(/widget looks like a Go module/)).toBeVisible({ timeout: 30_000 });
  const back = card.getByText('Or: hire File Curator to tidy the loose files instead');
  await expect(back).toHaveCount(1);
  await back.click();
  await expect(card.getByText(/File Curator (for|to) Documents/)).toBeVisible({ timeout: 30_000 });
  expect((await api(page, '/api/workspaces')).folders.length).toBe(workspacesBefore);
});

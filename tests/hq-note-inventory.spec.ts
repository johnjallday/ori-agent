import { test, expect, type APIRequestContext, type Locator, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';
import { mkdirSync, readFileSync, readdirSync, realpathSync } from 'node:fs';
import { installLocalCdn } from './helpers/offline-cdn';

// The HQ note inventory journey, on its own disposable demo. One command
// builds, starts a fresh sandbox, runs and stops it:
//   ./scripts/e2e-fresh.sh --port <free port> --sandbox-env NOTE_INVENTORY_SANDBOX \
//     tests/hq-note-inventory.spec.ts -- --workers=1
// By hand: `scripts/demo-server.sh <port>`, then PLAYWRIGHT_BASE_URL and
// NOTE_INVENTORY_SANDBOX (the printed SANDBOX's realpath, /private/var/… on
// macOS); NOTE_INVENTORY_EVIDENCE=<dir> saves screenshots.
// The suite is serial and builds its own state: an assistant, the Personal
// HQ, a fresh Ori notebook with a House project and an EMPTY Inbox (for the
// exact 0 → 1), and last a connected folder (a destructive switch, so this
// suite never shares a sandbox with the My Notes suite).

const endpoint = '/api/personal-assistant/second-brain';
const sandbox = process.env.NOTE_INVENTORY_SANDBOX;
const evidence = process.env.NOTE_INVENTORY_EVIDENCE;

test.describe.configure({ mode: 'serial' });
test.skip(!sandbox, 'Use a fresh disposable demo; set NOTE_INVENTORY_SANDBOX to its root.');
test.beforeEach(async ({ baseURL, page }) => {
  expect(new URL(baseURL!).hostname).toBe('localhost');
  expect(readFileSync(join(sandbox!, '.ori-demo-owner'), 'utf8').trim()).toBe(
    `worktree=${realpathSync(process.cwd())}`
  );
  await installLocalCdn(page);
});

async function capture(page: Page, name: string) {
  if (evidence) await page.screenshot({ path: join(evidence, `${name}.png`) });
}

async function post(request: APIRequestContext, path: string, data: unknown) {
  const response = await request.post(path, { data });
  expect(response.ok(), `${path}: ${await response.text()}`).toBeTruthy();
  return response.json();
}

async function connection(request: APIRequestContext) {
  const response = await request.get(`${endpoint}?view=connection`);
  expect(response.ok(), await response.text()).toBeTruthy();
  return (await response.json()).second_brain;
}

function fixture(baseURL: string, mode: 'ori-empty' | 'external') {
  const port = new URL(baseURL).port;
  return execFileSync('./scripts/my-notes-demo-source.sh', [port, mode, sandbox!], {
    encoding: 'utf8'
  });
}

// Every notes request the page makes while recording, as "METHOD /path?query".
function recordNotesRequests(page: Page) {
  const seen: string[] = [];
  page.on('request', req => {
    const url = new URL(req.url());
    if (url.pathname.startsWith(endpoint))
      seen.push(`${req.method()} ${url.pathname.slice(endpoint.length)}${url.search}`);
  });
  return seen;
}

let hqSlug = '';

async function hqMap(page: Page) {
  if (!hqSlug) {
    const today = await (await page.request.get('/api/personal-assistant/today')).json();
    hqSlug = today.today?.hq_workspace_slug || '';
  }
  expect(hqSlug).toBeTruthy();
  await page.goto(`/workspaces/${hqSlug}?mode=map`);
  // The HQ's first-visit "Things to try" sheet can sit over the map.
  const later = page.getByRole('button', { name: 'Maybe later' });
  if (await later.isVisible().catch(() => false)) await later.click();
}

const inventoryOf = (page: Page) => page.getByRole('dialog', { name: 'Note inventory' });
const tabOf = (inventory: Locator, key: string) =>
  inventory.locator(`[data-note-inventory-control="tab:${key}"]`);
const slotOf = (inventory: Locator, id: string) =>
  inventory.locator(`[data-note-inventory-action="select"][data-note-inventory-value="${id}"]`);
const inspectorOf = (inventory: Locator) => inventory.locator('.hq-note-inventory__inspector');
const freeNotes = (page: Page) =>
  page
    .locator('.ws-cmd-map-hq-station[data-cmd-hq-station="second-brain"]')
    .filter({ visible: true });
const flowNotes = (page: Page) =>
  page.locator('.ws-cmd-flow-node[data-cmd-hq-station="second-brain"]');

test('setup: an assistant, the Personal HQ and an Ori notebook with House and an empty Inbox', async ({
  baseURL,
  request
}) => {
  await post(request, '/api/onboarding/complete', {});
  await post(request, '/api/settings/workspace-root', {
    workspace_root: join(sandbox!, 'Ori Workspaces')
  });
  await post(request, '/api/personal-assistant/hire', {
    request_id: 'inventory-test-hire',
    if_version: 0,
    display_name: 'Atlas',
    mandate: 'Keep my projects moving and remember what I am working on.',
    focus_areas: ['keep_projects_moving']
  });
  const assistant = await (await request.get('/api/personal-assistant')).json();
  await post(request, '/api/personal-assistant/hq', {
    request_id: 'inventory-test-hq',
    if_version: assistant.personal_assistant?.state_version ?? 2,
    name: 'Personal HQ',
    timezone: 'UTC',
    schedule_days: ['mon'],
    schedule_time: '08:00',
    scope: 'all',
    include_future_workspaces: true,
    notify_on_ready: false
  });
  expect(fixture(baseURL!, 'ori-empty')).toContain('created_by_ori');
  const source = await connection(request);
  expect(source.status).toBe('connected');
  // The exact starting point: a House note and nothing in Inbox.
  const inbox = readdirSync(join(source.source.path, 'Inbox'));
  expect(inbox).toEqual(['README.md']);
  // A project folder with no note in it, in this disposable notebook only.
  mkdirSync(join(source.source.path, 'Projects', 'Garden'));
  const today = await (await request.get('/api/personal-assistant/today')).json();
  hqSlug = today.today?.hq_workspace_slug;
  expect(hqSlug).toBeTruthy();
});

test('golden path: House, a Quick note 0 → 1, close to the same place, Read full note, Flow', async ({
  page,
  request
}) => {
  const source = await connection(request);
  const ref = source.browse_ref as string;
  const inboxDir = join(source.source.path, 'Inbox');
  const status = await (await request.get('/api/personal-assistant/today')).json();
  const workspaceNotesURL = `/api/workspaces/${status.today.hq_workspace_id}/notes`;
  const workspaceNotes = async () => (await (await request.get(workspaceNotesURL)).json()).notes;
  const workspaceBefore = await workspaceNotes();

  await hqMap(page);
  await page.locator('[data-cmd-map-arrangement="free"]').click();
  const building = freeNotes(page).first();
  await expect(building).toHaveAttribute('aria-label', /0 in Inbox/);
  // The page scrolls smoothly; jump instead, so "before" is the settled place.
  await page.evaluate(() => scrollTo({ top: 80, behavior: 'instant' }));
  const before = { url: page.url(), scrollY: await page.evaluate(() => scrollY) };
  const reads = recordNotesRequests(page);

  await building.click();
  const inventory = inventoryOf(page);
  await expect(inventory).toBeVisible();
  await expect(tabOf(inventory, 'inbox')).toHaveAttribute('aria-label', 'Inbox, 0');
  await expect(inventory.getByText('Inbox empty')).toBeVisible();
  await tabOf(inventory, 'projects').click();
  await expect(inventory).toContainText(/Project notes \/ 02/i);
  // A folder is not given a note: Garden has none, and choosing it reads nothing.
  const garden = slotOf(inventory, 'project:Garden');
  await expect(garden).toContainText('No project note');
  await garden.click();
  await expect(inspectorOf(inventory)).toContainText('no project note (README.md) yet');
  expect(reads.filter(r => r.includes('/preview/'))).toEqual([]);
  const house = slotOf(inventory, 'project:House');
  await expect(house).toContainText('Project note');
  await house.click();
  await expect(house).toHaveAttribute('aria-pressed', 'true');
  await expect(inspectorOf(inventory)).toContainText('Repaint the hallway');
  await expect(inspectorOf(inventory)).toContainText(/Projects \/ House/i);
  await capture(page, '20-house-selected');
  // Names, one look inside House, one preview; no look, Inbox read or write.
  expect(reads.filter(r => r.startsWith('POST'))).toEqual([]);
  expect(reads.filter(r => r.includes('/preview/'))).toEqual([
    `GET /preview/project/House?ref=${ref}`
  ]);
  expect(reads.some(r => r.startsWith('GET /look') || r.startsWith('GET /inbox'))).toBe(false);

  await inventory.getByRole('button', { name: /Quick note/ }).click();
  await expect(inventory.getByText('Save to My Notes → Inbox')).toBeVisible();
  const field = inventory.locator('[data-note-inventory-control="draft"]');
  await expect(field).toBeFocused();
  await field.fill('Ask about the roof warranty.');
  await inventory.getByRole('button', { name: 'Save to Inbox' }).click();
  await expect(inventory.getByRole('status').filter({ hasText: 'Saved' })).toHaveText(
    'Saved to My Notes → Inbox.'
  );
  await expect(tabOf(inventory, 'inbox')).toHaveAttribute('aria-label', 'Inbox, 1');
  await expect(house).toHaveAttribute('aria-pressed', 'true');
  await expect(inspectorOf(inventory)).toContainText('Repaint the hallway');
  await expect(house).toBeFocused();
  expect(readdirSync(inboxDir).filter(name => name !== 'README.md')).toHaveLength(1);
  expect(reads.filter(r => r.startsWith('POST'))).toEqual(['POST /capture']);
  expect(await workspaceNotes()).toEqual(workspaceBefore);
  await capture(page, '21-saved-house-kept');

  await page.keyboard.press('Escape');
  await expect(inventory).toBeHidden();
  await expect(building).toBeFocused();
  expect(page.url()).toBe(before.url);
  expect(await page.evaluate(() => scrollY)).toBe(before.scrollY);
  await expect(building).toHaveAttribute('aria-label', /1 in Inbox/);
  await capture(page, '22-closed-badge-one');

  // Reopen, House again, and leave for that note in My Notes.
  await building.click();
  await tabOf(inventory, 'projects').click();
  await slotOf(inventory, 'project:House').click();
  await expect(inspectorOf(inventory)).toContainText('Repaint the hallway');
  const read = inventory.locator('[data-note-inventory-control="read-note"]');
  await expect(read).toHaveText(/Read full note/);
  await read.click();
  await expect(page).toHaveURL(/\/my-notes#/);
  expect(new URL(page.url()).search).toBe('');
  const browser = page.getByRole('region', { name: 'Notebook browser' });
  await expect(browser.locator('.my-notes-reader')).toContainText('Repaint the hallway');
  await expect(
    browser.getByRole('navigation', { name: 'Folders' }).getByRole('button', { name: 'House' })
  ).toHaveAttribute('aria-current', 'true');
  await capture(page, '23-house-in-my-notes');
  await page.goBack();
  await expect(page).toHaveURL(new RegExp(`/workspaces/${hqSlug}`));

  // Flow is the same station.
  await page.locator('[data-cmd-map-arrangement="flow"]').click();
  await flowNotes(page).click();
  await expect(inventory).toBeVisible();
  await inventory.getByRole('button', { name: 'Close note inventory' }).click();
  await expect(flowNotes(page)).toBeFocused();
});

test('a failed save keeps the draft; a double submit saves once', async ({ page, request }) => {
  const source = await connection(request);
  const inboxDir = join(source.source.path, 'Inbox');
  const filesBefore = readdirSync(inboxDir).length;
  await hqMap(page);
  await flowNotes(page).click();
  const inventory = inventoryOf(page);
  await inventory.getByRole('button', { name: /Quick note/ }).click();
  const field = inventory.locator('[data-note-inventory-control="draft"]');
  await field.fill('Check the gutter quote.');
  await page.route(
    `**${endpoint}/capture`,
    route => route.fulfill({ status: 500, json: { error: 'Try the save again.' } }),
    { times: 1 }
  );
  await inventory.getByRole('button', { name: 'Save to Inbox' }).click();
  await expect(inventory.getByRole('alert')).toHaveText('Try the save again.');
  await expect(field).toHaveValue('Check the gutter quote.');
  await expect(field).toBeFocused();
  const posts: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && req.url().endsWith(`${endpoint}/capture`)) posts.push(req.url());
  });
  const save = inventory.getByRole('button', { name: 'Save to Inbox' });
  await save.dblclick();
  await expect(inventory.getByRole('status').filter({ hasText: 'Saved' })).toBeVisible();
  expect(posts).toHaveLength(1);
  expect(readdirSync(inboxDir).length).toBe(filesBefore + 1);
});

test('note links open their own folder and note; stale and malformed links fall back visibly', async ({
  page,
  request
}) => {
  const source = await connection(request);
  const ref = source.browse_ref as string;
  const captured = readdirSync(join(source.source.path, 'Inbox')).find(
    name => name !== 'README.md'
  )!;
  const browser = page.getByRole('region', { name: 'Notebook browser' });
  const readerPane = browser.locator('.my-notes-reader');
  const folderButton = (name: string) =>
    browser.getByRole('navigation', { name: 'Folders' }).getByRole('button', { name, exact: true });
  // A link is followed from another page (the HQ). Changing only the
  // fragment of an open /my-notes is not an arrival, so each link starts
  // from a blank page.
  const follow = async (params: Record<string, string>) => {
    await page.goto('about:blank');
    await page.goto('/my-notes#' + new URLSearchParams(params));
  };

  await follow({ ref, kind: 'inbox', name: captured });
  await expect(folderButton('Inbox')).toHaveAttribute('aria-current', 'true');
  await expect(readerPane).toContainText(/roof warranty|gutter quote/i);

  await follow({ ref, kind: 'project', name: 'House' });
  await expect(folderButton('House')).toHaveAttribute('aria-current', 'true');
  await expect(readerPane).toContainText('Repaint the hallway');

  await follow({ ref: 'f'.repeat(64), kind: 'project', name: 'House' });
  await expect(page.getByText(/made for an earlier notebook connection/)).toBeVisible();
  await expect(folderButton('Inbox')).toHaveAttribute('aria-current', 'true');

  await follow({ ref, kind: 'project', name: '../House' });
  await expect(page.getByText(/not one Ori can open/)).toBeVisible();

  await follow({ ref, kind: 'project', name: 'Gone' });
  await expect(page.getByText(/no longer in your notebook/)).toBeVisible();

  // Plain /my-notes is still the notebook's own landing.
  await page.goto('/my-notes');
  await expect(folderButton('Inbox')).toHaveAttribute('aria-current', 'true');
});

test('a connected folder: its own names, no Quick note, no note text, its own door', async ({
  baseURL,
  page
}) => {
  expect(fixture(baseURL!, 'external')).toContain('connected · Field Notebook');
  await hqMap(page);
  const reads = recordNotesRequests(page);
  await flowNotes(page).click();
  const inventory = inventoryOf(page);
  await expect(inventory).toBeVisible();
  await expect(inventory.locator('[data-note-inventory-control^="tab:"]')).toHaveCount(0);
  await expect(inventory.getByRole('button', { name: /Quick note/ })).toHaveCount(0);
  await slotOf(inventory, 'folder:Expeditions').click();
  await slotOf(inventory, 'file:Canyon.md').click();
  await expect(inspectorOf(inventory)).toContainText(/connected folder/);
  await expect(inventory.getByRole('link', { name: 'Open in Obsidian' })).toHaveAttribute(
    'href',
    'obsidian://open?vault=Field%20Notebook&file=Expeditions%2FCanyon'
  );
  await capture(page, '24-external-note');
  expect(reads.filter(r => r.includes('/preview/') || r.startsWith('POST'))).toEqual([]);
  expect(await inventory.textContent()).not.toMatch(/\/(?:private|var|Users)\//);
});

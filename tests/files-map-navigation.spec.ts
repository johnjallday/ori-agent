import { test, expect, type Locator, type Page, type Request } from '@playwright/test';
import { readFileSync, mkdirSync, writeFileSync, utimesSync, existsSync, chmodSync } from 'node:fs';
import { join } from 'node:path';
import { seedQuestionFolder } from './helpers/commander-question-fixture';
import { installLocalCdn } from './helpers/offline-cdn';
import { expectDestination, goToFiles, goToMap } from './helpers/home-destination';

/**
 * Files and Map, and getting between them and the rest of Ori, once folders are
 * managed in the merged Files (merge-tree-into-files PR 2). Two searchable
 * folders with no workspace (an upgraded installation's grants) are found by
 * name, told apart by where they are and when they changed, scoped from their
 * rows in the Search-only folders group, and kept across Map, Back and another
 * tab removing one of them. The starting page is chosen in Files.
 *
 *   python3 scripts/test-search-first.py --scenario navigation
 */

const fixture = process.env.ORI_SEARCH_FIRST_FIXTURE;
const evidenceRoot = 'tasks/evidence-files-map-navigation';
test.skip(
  !fixture || process.env.ORI_SEARCH_FIRST_SCENARIO !== 'navigation',
  'requires a marked disposable profile'
);

const search = (page: Page) => page.locator('#cockpitFilesSearchInput');
const results = (page: Page) =>
  page.locator('#cockpitTreePane [data-results-group="files"] [data-result-indexed]');
const scopeChip = (page: Page) => page.locator('#cockpitFilesSearchScope');
const treeRow = (page: Page, key: string) =>
  page.locator(`#cockpitTreeNav [data-tree-row="${key}"]`);

/** Choose an item from a tree row's menu, by its visible words. */
async function chooseFromRow(page: Page, row: Locator, item: string) {
  await row.hover();
  await row.locator('[data-tree-menu-for]').click();
  await page.getByRole('menuitem', { name: new RegExp(`^${item}`) }).click();
}

test('distinguish duplicate copies by location and date before acting', async ({
  page,
  context,
  request,
  baseURL,
  browserName
}) => {
  const evidence = join(evidenceRoot, browserName);
  expect(new URL(baseURL!).hostname).toBe('localhost');
  expect(readFileSync(join(fixture!, '.search-first-fixture'), 'utf8')).toBe('synthetic-v1\n');
  expect(readFileSync(join(fixture!, '.search-first-test-url'), 'utf8').trim()).toBe(baseURL);
  // Exercise navigation on an established assistant fixture. First-run model
  // verification belongs to onboarding, not the filename query/controller.
  await installLocalCdn(page);
  // This navigation regression starts with independent search-only grants, as
  // on an upgraded installation. Create them on the first-run page, before
  // workspace onboarding; established-profile workspace review has its own
  // acceptance journeys.
  await page.goto('/search');
  for (const kind of ['Documents', 'Downloads']) {
    if (kind === 'Downloads') await page.getByRole('button', { name: /Add folder/ }).click();
    await page.getByRole('button', { name: kind, exact: true }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Allow search' }).click();
    await expect(page.locator('#sources li').filter({ hasText: kind })).toContainText('ready');
  }
  // The tall, empty-map welcome is where a stretched destination row was
  // most obvious. Capture it before creating the assistant fixture.
  expect((await request.post('/api/onboarding/skip')).ok()).toBeTruthy();
  const defaultSize = page.viewportSize()!;
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto('/?view=map');
  await expect(page.getByRole('heading', { name: 'Meet your assistant.' })).toBeVisible();
  await page.screenshot({ path: join(evidence, 'map-header-welcome.png') });
  await page.setViewportSize(defaultSize);
  await page.goto('about:blank');
  const music = await seedQuestionFolder(request, join(fixture!, 'home'), 'Map Albums', true);
  const other = await seedQuestionFolder(request, join(fixture!, 'home'), 'Other Albums', true);
  const original = join(fixture!, 'home/Downloads/Saved/proposal.pdf');
  for (const [dir, stamp] of [
    ['Documents/Final', 1704067200],
    ['Downloads/Saved', 1735689600]
  ] as const) {
    const root = join(fixture!, 'home', dir);
    mkdirSync(root, { recursive: true });
    const copy = join(root, 'proposal.pdf');
    writeFileSync(copy, 'Synthetic', { flag: 'wx' });
    utimesSync(copy, stamp, stamp);
  }
  for (let n = 0; n < 27; n++)
    writeFileSync(join(fixture!, `home/Downloads/sample-${n}.unknown`), '', { flag: 'wx' });

  // With workspaces, /search is the merged Files. The two grants have no
  // workspace: they are the Search-only folders group, managed from its rows.
  await page.goto('/search');
  await expect(page).toHaveURL(/\/\?view=tree$/);
  const sources = async () =>
    await (
      await request.get('/api/file-search/sources', { headers: { 'X-Ori-Search': '1' } })
    ).json();
  const idOf = async (label: string) =>
    (await sources()).find((s: { label: string }) => s.label === label).id as string;
  const documentsRow = treeRow(page, `search-only/${await idOf('Documents')}`);
  const downloadsRow = treeRow(page, `search-only/${await idOf('Downloads')}`);
  await expect(treeRow(page, 'search-only')).toContainText('Search-only folders');
  // Files were added after the folders were indexed. Refresh, rather than granting again.
  for (const row of [documentsRow, downloadsRow]) {
    await chooseFromRow(page, row, 'Refresh index');
    await expect(row.locator('[data-folder-search]')).toHaveText('Searchable', { timeout: 15000 });
  }
  await expect
    .poll(async () => (await sources()).every((s: { state: string }) => s.state === 'ready'))
    .toBe(true);

  // --- two copies of one file, told apart by place and date --------------------
  await search(page).fill('proposal');
  await expect(results(page)).toHaveCount(2);
  await expect(results(page).first()).toContainText('Downloads › Saved');
  await expect(results(page).last()).toContainText('Documents › Final');
  await results(page).first().click();
  await expect(results(page).first()).toHaveAttribute('aria-pressed', 'true');
  await page.screenshot({ path: join(evidence, 'copies.png') });

  // --- Search in this folder, from a search-only folder's row -------------------
  const indexOnly: string[] = [];
  const noteRequest = (r: Request) => {
    const path = new URL(r.url()).pathname;
    if (path === '/api/name-search' || path === '/api/file-search/query') indexOnly.push(path);
  };
  page.on('request', noteRequest);
  await chooseFromRow(page, documentsRow, 'Search in this folder');
  await expect(scopeChip(page)).toContainText('in Documents');
  await search(page).fill('proposal.pdf');
  await expect(results(page)).toHaveCount(1);
  await expect(results(page).first()).toContainText('Documents › Final');
  await page.screenshot({ path: join(evidence, 'documents-scope.png') });
  await chooseFromRow(page, downloadsRow, 'Search in this folder');
  await expect(scopeChip(page)).toContainText('in Downloads');
  await search(page).fill('sample-');
  await expect(results(page)).toHaveCount(25);
  await page.locator('#cockpitTreePane [data-result-more="files"]').click();
  await expect(results(page)).toHaveCount(27);
  await results(page).last().click();
  await expect(results(page).last()).toHaveAttribute('aria-pressed', 'true');
  await page.screenshot({ path: join(evidence, 'scoped-last-page.png') });
  page.off('request', noteRequest);
  // A folder with no workspace is the filename index's alone: nothing else is asked.
  expect(indexOnly).toContain('/api/file-search/query');
  expect(indexOnly).not.toContain('/api/name-search');

  const read = async (id: string) =>
    (await (await request.get(`/api/personal-assistant/folder-agent/${id}`)).json()).folder_agent;
  // Seed a pending-question record, not a simulated answer or a live model turn.
  // Only this marked, newly minted split profile is writable here. With a
  // degraded turn the real launcher correctly leads with the failure instead.
  for (const slug of ['map-albums', 'other-albums']) {
    const path = join(fixture!, 'data/workspace-staging', slug, '.ori/folder-agent-v1.json');
    const record = JSON.parse(readFileSync(path, 'utf8'));
    delete record.turn;
    writeFileSync(path, JSON.stringify(record));
  }
  const mutations: string[] = [];
  page.on('request', r => {
    if (r.method() === 'POST') mutations.push(new URL(r.url()).pathname);
  });
  const toMap = async () => {
    await goToMap(page);
    await expectDestination(page, 'map');
    await expect(page.locator('[data-ws-map-viewport]').first()).toBeVisible();
  };
  const toFiles = async () => {
    await goToFiles(page);
    await expectDestination(page, 'files');
    await expect(search(page)).toBeVisible();
  };

  // --- Map: only the map takes spare height, never the destination controls ----
  await toMap();
  const originalSize = page.viewportSize()!;
  for (const size of [
    { width: 1440, height: 1000 },
    { width: 390, height: 844 }
  ]) {
    await page.setViewportSize(size);
    const picker = page.getByRole('navigation', { name: 'Destination', exact: true });
    // WebKit can momentarily expose both the rail and mobile picker while
    // applying the viewport change. Assert the layout settles to exactly one.
    await expect(picker).toHaveCount(1);
    await expect(picker).toBeVisible();
    await expect(page.locator('[data-ws-map-viewport]').first()).toBeVisible();
    const gap = await page.evaluate(() => {
      const toolbar = document.querySelector('.atlas-toolbar')!.getBoundingClientRect();
      const map = document.querySelector('#cockpitMap')!.getBoundingClientRect();
      return { above: map.top - toolbar.bottom, left: Math.abs(map.left - toolbar.left) };
    });
    expect(gap.above).toBeGreaterThanOrEqual(0);
    expect(gap.above).toBeLessThan(20);
    expect(gap.left).toBeLessThan(2); // Neither screen size loses a sidebar's width.
    await page.screenshot({ path: join(evidence, `map-header-${size.width}.png`) });
  }
  await page.setViewportSize(originalSize);

  // Files and Map are one page: the search, its scope and the selected file
  // are still there after a visit to the Map.
  await toFiles();
  await expect(search(page)).toHaveValue('sample-');
  await expect(scopeChip(page)).toContainText('in Downloads');
  await expect(results(page)).toHaveCount(27);
  await expect(results(page).last()).toHaveAttribute('aria-pressed', 'true');

  // --- the Map's selection, the Commander's question, and Back ----------------
  // By its own address: switched to in the page, the Map's address is a bare /,
  // which a Files starting page sends to Files on the way Back (since PR 1).
  await page.goto('/?view=map');
  await expectDestination(page, 'map');
  await expect(page.locator('[data-ws-map-viewport]').first()).toBeVisible();
  const camera = () => page.evaluate(() => (window as any).OriWorkspaceMap.getCamera());
  const viewport = page.locator('[data-ws-map-viewport]').first();
  const initialCamera = await camera();
  await viewport.press('ArrowRight');
  await viewport.press('+');
  const tile = page
    .locator(
      `.ws-map-tile[data-ws-id="${music.workspace_id}"], .ws-map-district-tag[data-ws-id="${music.workspace_id}"]`
    )
    .first();
  await tile.press('Space');
  const open = page.locator(`[data-cockpit-rail-open][data-workspace-id="${music.workspace_id}"]`);
  await expect(open).toBeVisible();
  await page.screenshot({ path: join(evidence, 'workspace-selected.png') });
  const panned = await camera();
  expect(panned).not.toEqual(initialCamera);
  await open.press('Enter');
  const launcher = page.locator('#personalAssistantLauncher');
  await expect(launcher.locator('[data-question]')).toBeVisible();
  // Looking around the Map and opening the Commander changes no searchable folder.
  expect(
    mutations.filter(p =>
      /\/file-search\/(begin|select|review-link|connect|open|reveal|refresh|remove|preference)$|\/premise$/.test(
        p
      )
    )
  ).toEqual([]);
  await launcher.press('Enter');
  const answer = page.locator(
    `[data-folder-agent-chat-card][data-message-id="${music.chat.message_id}"] [data-folder-agent-action="premise_confirm"]`
  );
  await expect(answer).toBeVisible();
  await page.screenshot({ path: join(evidence, 'commander-question.png') });
  await answer.press('Enter');
  await expect(answer).toHaveCount(0);
  await expect(launcher.locator('[data-question]')).toHaveCount(0);
  expect((await read(other.workspace_id)).premise.state).toBe(other.premise.state);
  await page.getByRole('button', { name: 'Close personal assistant', exact: true }).click();
  // Opening the workspace left Home; Back puts the Map's selection back a
  // moment after it draws.
  await page.goBack();
  await expect(tile).toHaveClass(/is-selected/);
  if (await page.locator('#cockpitContextModal').isVisible()) await page.keyboard.press('Escape');

  // Return to the same workspace, not a cached cue or reply; Back brings the
  // Map's selection and camera back.
  await expect(tile).toHaveClass(/is-selected/);
  await tile.press('Enter');
  await expect(page).toHaveURL(new RegExp('/workspaces/map-albums'));
  if (await page.locator('#personalAssistantPanel').isVisible())
    await page.getByRole('button', { name: 'Close personal assistant', exact: true }).click();
  await expect(launcher).toBeVisible();
  await expect(launcher.locator('[data-question]')).toHaveCount(0);
  expect((await read(music.workspace_id)).premise.state).not.toBe('asked');
  await page.goBack();
  await expect(tile).toHaveClass(/is-selected/);
  await expect.poll(camera).toEqual(panned);
  if (await page.locator('#cockpitContextModal').isVisible()) await page.keyboard.press('Escape');

  // Back from a workspace page brings Files' search back as it was: the
  // scope, the term and the selected file (this is a new page, so scope again).
  await toFiles();
  await chooseFromRow(page, downloadsRow, 'Search in this folder');
  await expect(scopeChip(page)).toContainText('in Downloads');
  await search(page).fill('proposal.pdf');
  await expect(results(page)).toHaveCount(1);
  await results(page).first().click();
  await expect(results(page).first()).toHaveAttribute('aria-pressed', 'true');
  await page.goto(`/workspaces/${encodeURIComponent('map-albums')}`);
  await expect(page).toHaveURL(/\/workspaces\/map-albums/);
  await page.goBack();
  await expect(search(page)).toHaveValue('proposal.pdf');
  await expect(scopeChip(page)).toContainText('in Downloads');
  await expect(results(page)).toHaveCount(1);
  await expect(results(page).first()).toHaveAttribute('aria-pressed', 'true');
  const disk = await page.evaluate(() =>
    JSON.stringify([localStorage, sessionStorage, history.state, location.href, window.name])
  );
  expect(disk).not.toContain('proposal.pdf');
  await page.screenshot({ path: join(evidence, 'restored-copy.png') });

  // --- another tab removes Downloads from search -------------------------------
  const second = await context.newPage();
  await second.goto('/?view=tree');
  const secondRow = treeRow(second, `search-only/${await idOf('Downloads')}`);
  await chooseFromRow(second, secondRow, 'Remove from search');
  await second
    .getByRole('dialog', { name: 'Remove Downloads from search?' })
    .getByRole('button', { name: 'Remove from search' })
    .click();
  await expect(secondRow).toHaveCount(0);
  await second.close();
  // This tab hears it: the scope goes, the search is asked again everywhere,
  // and the removed folder's copy is no longer offered. The file itself stays.
  await expect(scopeChip(page)).toBeHidden();
  await expect(downloadsRow).toHaveCount(0);
  await expect(results(page)).toHaveCount(1);
  await expect(results(page).first()).toContainText('Documents › Final');
  expect(existsSync(original)).toBeTruthy();
  await page.screenshot({ path: join(evidence, 'revoked-copy.png') });

  // A workspace deleted elsewhere leaves the Map and selects nothing.
  const deleted = await request.delete(
    `/api/workspaces/${music.workspace_id}?confirm=true&delete_sessions=true`
  );
  expect(deleted.ok()).toBe(true);
  await page.goto('/?view=map');
  await expectDestination(page, 'map');
  await expect(tile).toHaveCount(0);
  await expect(page.locator('.ws-map-tile.is-selected')).toHaveCount(0);

  // --- a slower, older answer never replaces a newer one; a failure retries ----
  await page.goto('/?view=tree');
  await expect(search(page)).toBeVisible();
  let release!: () => void;
  const wait = new Promise<void>(resolve => (release = resolve));
  let started!: () => void;
  const captured = new Promise<void>(resolve => (started = resolve));
  await page.route('**/api/file-search/query', async route => {
    if (route.request().postDataJSON().term !== 'Harbor') return route.continue();
    const response = await route.fetch();
    started();
    await wait;
    await route.fulfill({ response });
  });
  await search(page).fill('Harbor');
  await captured;
  await search(page).fill('Welcome');
  await expect(results(page)).toHaveCount(1);
  await expect(results(page).first()).toContainText('Welcome');
  release();
  await expect(results(page).first()).toContainText('Welcome');
  await page.unroute('**/api/file-search/query');
  await page.route(
    '**/api/file-search/query',
    route => route.fulfill({ status: 503, body: 'Search unavailable.' }),
    { times: 1 }
  );
  await search(page).fill('proposal');
  const retry = page.locator('#cockpitTreePane [data-result-retry]').first();
  await expect(retry).toBeVisible();
  await expect(results(page)).toHaveCount(0);
  await retry.click();
  await expect(results(page)).toHaveCount(1);

  // --- narrow, the two destinations, and 200% zoom ------------------------------
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(results(page)).toHaveCount(1);
  const homePicker = page.locator('[data-map-picker]');
  await expect(homePicker).toBeVisible();
  await expect(homePicker.locator('option')).toHaveText(['Map', 'Files']);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: join(evidence, 'narrow.png') });
  await goToMap(page);
  await expectDestination(page, 'map');
  await goToFiles(page);
  await expectDestination(page, 'files');
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.evaluate(() => (document.documentElement.style.zoom = '200%'));
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: join(evidence, 'zoom.png') });
  await page.evaluate(() => (document.documentElement.style.zoom = ''));

  // --- a folder that can only be partly read says so on its row ----------------
  const locked = join(fixture!, 'home/Documents/Locked');
  mkdirSync(locked, { mode: 0o700 });
  try {
    chmodSync(locked, 0);
    await chooseFromRow(page, documentsRow, 'Refresh index');
    await expect(documentsRow.locator('[data-folder-search]')).toHaveText('Partly indexed', {
      timeout: 15000
    });
  } finally {
    chmodSync(locked, 0o700);
  }

  // --- the starting page is chosen in Files (requirement 40) -------------------
  // The first folder made Files the starting page.
  const landing = page.locator('[data-files-landing]');
  await expect(landing).toHaveText('Start on Map instead');
  await landing.click();
  await expect(landing).toHaveText('Make Files my starting page');
  await page.goto('/');
  await expect(page).not.toHaveURL(/view=tree/);
  await expect(page.locator('.home-command-bridge')).toBeVisible();
  await expectDestination(page, 'map');
  await page.goto('/?view=tree');
  // A choice changed elsewhere is refused, not overwritten.
  expect(
    (
      await request.post('/api/file-search/preference', {
        headers: { 'X-Ori-Search': '1', Origin: baseURL! },
        data: { expected: 'later', next: 'search' }
      })
    ).status()
  ).toBe(204);
  await landing.click();
  await expect(page.locator('#cockpitRailLive')).toContainText('Decision changed');
  await page.reload();
  await expect(landing).toHaveText('Start on Map instead');
  // Files as the starting page, with workspaces present, is the merged Files.
  await page.goto('/');
  await expect(page).toHaveURL(/\/\?view=tree$/);
  await expect(page.locator('#cockpitTree')).toBeVisible();
  await expect(page.locator('[data-cockpit-area-title]')).toHaveText('Files');
  await expectDestination(page, 'files');
  // No temporary link to the standalone page is left (requirement 43).
  await expect(page.getByRole('link', { name: 'Manage search locations' })).toHaveCount(0);
});

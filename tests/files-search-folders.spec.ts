import { test, expect, type Page, type Request } from '@playwright/test';
import { readFileSync, mkdirSync, writeFileSync, utimesSync, chmodSync } from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

/**
 * Files in searchable folders, found from Files' one search box
 * (tasks/prd-merge-tree-into-files.md, requirements 13, 18-19, 23, 28).
 *
 * The filename index lives only on the Mac that runs Ori, so this runs on the
 * split-profile fixture scripts/test-search-first.py builds:
 *
 *   python3 scripts/test-search-first.py        (scenario `files-search`)
 *
 * Two kinds of folder are searched: Documents is search-only (no workspace), and
 * Downloads is linked to a workspace made through the reviewed folder setup.
 * Desktop effects are suppressed headless, so opening and revealing are proved
 * by the request that is made, not by an app opening.
 */

const fixture = process.env.ORI_SEARCH_FIRST_FIXTURE;
test.skip(
  !fixture || process.env.ORI_SEARCH_FIRST_SCENARIO !== 'files-search',
  'requires a marked disposable profile'
);

const search = (page: Page) => page.locator('#cockpitFilesSearchInput');
const pane = (page: Page) => page.locator('#cockpitTreePane');
const files = (page: Page) => pane(page).locator('[data-results-group="files"]');
const rows = (page: Page) => files(page).locator('[data-result-indexed]');
const notice = (page: Page) => pane(page).locator('[data-results-notice]');

test('files in searchable folders: found, ordered, selected, opened, refused, shown in the tree', async ({
  page,
  baseURL,
  request,
  browserName
}) => {
  expect(new URL(baseURL!).hostname).toBe('localhost');
  expect(readFileSync(join(fixture!, '.search-first-fixture'), 'utf8')).toBe('synthetic-v1\n');
  expect(readFileSync(join(fixture!, '.search-first-test-url'), 'utf8').trim()).toBe(baseURL);
  const evidence = join('tasks/evidence-merge-tree-into-files/03-folders', browserName);
  mkdirSync(evidence, { recursive: true });
  await installLocalCdn(page);

  const home = join(fixture!, 'home');
  const indexHeaders = { 'X-Ori-Search': '1', Origin: baseURL! };
  const sources = async () =>
    await (await request.get('/api/file-search/sources', { headers: indexHeaders })).json();
  const waitFor = async (label: string, done: (all: any[]) => boolean) => {
    await expect
      .poll(async () => done(await sources()), { message: label, timeout: 30000 })
      .toBe(true);
  };

  // Files that are only in the folders (nothing in a workspace), with known dates.
  mkdirSync(join(home, 'Downloads/Saved'), { recursive: true });
  const stamp = (file: string, seconds: number) => utimesSync(file, seconds, seconds);
  writeFileSync(join(home, 'Downloads/Ship.txt'), 'Synthetic.\n');
  stamp(join(home, 'Downloads/Ship.txt'), 1767312000); // 2026-01-02
  writeFileSync(join(home, 'Downloads/Saved/ship-plan.txt'), 'Synthetic.\n');
  stamp(join(home, 'Downloads/Saved/ship-plan.txt'), 1704067200); // 2024-01-01

  // Documents: searchable on its own, with no workspace. A search-only grant is
  // only made on a first-run profile (an established one reviews the workspace
  // and the filename search together), so it is made before onboarding is skipped,
  // as an upgraded installation's independent grants are.
  const begun = await request.post('/api/file-search/begin', {
    headers: indexHeaders,
    data: { kind: 'documents' }
  });
  expect(begun.ok(), `begin: ${begun.status()} ${await begun.text()}`).toBeTruthy();
  const picked = await request.post('/api/file-search/select', {
    headers: indexHeaders,
    data: await begun.json()
  });
  expect(picked.ok(), `select: ${picked.status()} ${await picked.text()}`).toBeTruthy();
  expect((await request.post('/api/onboarding/skip')).ok()).toBeTruthy();

  // Downloads: linked to a workspace, through the reviewed folder setup.
  await page.goto('/search');
  await page.getByRole('button', { name: /Add folder/ }).click();
  await page.locator('[data-kind="downloads"]').filter({ visible: true }).click();
  const review = page.locator('dialog.explore-card');
  await review.getByRole('checkbox').check();
  await review.getByLabel('Workspace name').fill('Archive for Downloads');
  await review.getByRole('button', { name: 'Create workspace' }).click();
  await waitFor(
    'both folders indexed',
    all => all.length === 2 && all.every(s => s.state === 'ready')
  );
  const all = await sources();
  const linked = all.find((s: any) => s.workspace && s.workspace.name === 'Archive for Downloads');
  const searchOnly = all.find((s: any) => !s.workspace);
  expect(linked, 'the linked folder is a source of its workspace').toBeTruthy();
  expect(searchOnly.label).toBe('Documents');

  const requests: Request[] = [];
  page.on('request', r => {
    if (new URL(r.url()).pathname.startsWith('/api/file-search/') && r.method() === 'POST') {
      requests.push(r);
    }
  });
  const post = (op: string) => requests.filter(r => r.url().endsWith(`/api/file-search/${op}`));

  await page.goto('/?view=tree');
  await expect(page.locator('#cockpitTree')).toBeVisible();
  await expect(search(page)).toBeVisible();

  // --- found by name, newest first, with where they live and when -------------
  await search(page).fill('harbor');
  await expect(rows(page)).toHaveCount(2);
  await expect(rows(page).nth(0)).toContainText('Harbor.txt');
  await expect(rows(page).nth(0)).toContainText('Documents › Final');
  await expect(rows(page).nth(1)).toContainText('Documents › Rough');
  const dates = await rows(page)
    .locator('time')
    .evaluateAll(els => els.map(e => e.getAttribute('datetime')));
  expect(Date.parse(dates[0]!)).toBeGreaterThan(Date.parse(dates[1]!));
  await expect(pane(page).locator('.cockpit-results-status')).toContainText('2 results');
  // A folder with no workspace has no row to show.
  await expect(files(page).locator('[data-result-reveal]')).toHaveCount(0);
  await page.screenshot({ path: join(evidence, 'found.png') });

  // --- selecting shows the actions; nothing opens a tab -----------------------
  await rows(page).nth(0).click();
  await expect(rows(page).nth(0)).toHaveAttribute('aria-pressed', 'true');
  const actions = files(page).locator('.cockpit-result-actions');
  await expect(actions.getByRole('button', { name: 'Reveal in Finder' })).toBeVisible();
  const reader = await actions.getByRole('button', { name: /^Open in / }).count();
  await page.screenshot({ path: join(evidence, 'selected.png') });
  await expect(pane(page).locator('.cockpit-pane-tab')).toHaveCount(1); // Results only
  // Nothing about the term, or about a file in a search-only folder, is kept
  // anywhere the page can read: not the address, history state, window.name,
  // either storage, nor the remembered tree tabs. Checked now and after a reload.
  const remembered = () =>
    page.evaluate(() => {
      const tree = Object.entries(localStorage).find(([key]) =>
        key.startsWith('ori.home.fileTree')
      );
      return {
        everything: JSON.stringify({
          url: location.href,
          state: history.state,
          name: window.name,
          local: { ...localStorage },
          session: { ...sessionStorage }
        }),
        tabs: tree ? JSON.stringify(JSON.parse(tree[1]).tabs ?? []) : '[]'
      };
    });
  const expectNothingKept = async () => {
    const kept = await remembered();
    for (const word of ['idx:', 'harbor']) {
      expect(kept.everything.toLowerCase(), word).not.toContain(word.toLowerCase());
    }
    expect(kept.tabs).not.toContain('search:results');
    expect(kept.tabs).not.toContain('idx:');
    expect(kept.tabs.toLowerCase()).not.toContain('harbor');
  };
  await expectNothingKept();
  await page.reload();
  await expect(page.locator('#cockpitTree')).toBeVisible();
  await expect(search(page)).toHaveValue('');
  await expect(pane(page).locator('.cockpit-pane-tab--results')).toHaveCount(0);
  await expectNothingKept();
  // The page after a reload holds nothing to search from: ask again.
  await search(page).fill('harbor');
  await expect(rows(page)).toHaveCount(2);
  await rows(page).nth(0).click();
  await expect(rows(page).nth(0)).toHaveAttribute('aria-pressed', 'true');

  // On a real desktop the answer is "Requested in Finder." A sandboxed server
  // runs with desktop effects off and says so in its own words; either way the
  // request is made and whatever the server said is what the user is shown.
  const finder = /^(Requested in Finder\.|Folder search is unavailable\.)$/;
  const reader_ = /^(Sent to the document reader\.|Folder search is unavailable\.)$/;

  // --- reveal is a request with the file's id and generation -------------------
  await actions.getByRole('button', { name: 'Reveal in Finder' }).click();
  await expect(notice(page)).toHaveText(finder);
  expect(post('reveal')).toHaveLength(1);
  const body = post('reveal')[0].postDataJSON();
  expect(Object.keys(body).sort()).toEqual(['generation', 'id']);
  expect(JSON.stringify(body)).not.toContain('harbor');

  // --- Enter opens (or reveals when there is no reader); Shift+Enter reveals ----
  await rows(page).nth(0).focus();
  await page.keyboard.press('Enter');
  await expect.poll(() => post(reader ? 'open' : 'reveal').length).toBeGreaterThan(reader ? 0 : 1);
  await expect(notice(page)).toHaveText(reader ? reader_ : finder);
  const revealsBefore = post('reveal').length;
  await rows(page).nth(0).focus();
  await page.keyboard.press('Shift+Enter');
  await expect.poll(() => post('reveal').length).toBeGreaterThan(revealsBefore);
  await expect(notice(page)).toHaveText(finder);

  // --- a file replaced since it was indexed is refused, in words ----------------
  const rough = join(home, 'Documents/Rough/Harbor.txt');
  await rows(page).nth(1).click();
  writeFileSync(rough, 'Replaced after the index was made.\n');
  stamp(rough, 1800000000);
  await files(page)
    .locator('.cockpit-result-actions')
    .getByRole('button', { name: 'Reveal in Finder' })
    .click();
  // A moved or replaced file is refused by the server in words of its own on a
  // real desktop; a sandboxed server answers before looking at the file. Either
  // way the user is told something, and it is not "Requested in Finder."
  await expect(notice(page)).not.toBeEmpty();
  await expect(notice(page)).not.toHaveText('Requested in Finder.');
  await page.screenshot({ path: join(evidence, 'refused.png') });

  // --- a file in a workspace-linked folder shows in the tree --------------------
  await search(page).fill('ship');
  await expect(rows(page)).toHaveCount(2);
  await expect(rows(page).nth(0)).toContainText('Ship.txt'); // 2026, newer than 2024
  await expect(rows(page).nth(1)).toContainText('ship-plan.txt');
  await expect(rows(page).nth(1)).toContainText('Saved');
  await files(page)
    .locator('li', { hasText: 'ship-plan.txt' })
    .getByRole('button', { name: /Show .* in tree/ })
    .click();
  const treeKey = `${linked.workspace.id}/lf/${linked.workspace.link}/Saved/ship-plan.txt`;
  const row = page.locator(`#cockpitTreeNav [data-tree-row="${treeKey}"]`);
  await expect(row).toBeVisible();
  await expect(row).toBeFocused();
  await page.screenshot({ path: join(evidence, 'show-in-tree.png') });
  // Showing is not opening: still no tab, and the results are still there.
  await expect(pane(page).locator('.cockpit-pane-tab')).toHaveCount(1);
  await expect(rows(page)).toHaveCount(2);

  // --- a folder that could only be partly read says so, with Refresh index -------
  // A subfolder of Documents that cannot be read: the next index of Documents is
  // partial. It is made readable again whatever happens, so the sandbox can go.
  const locked = join(home, 'Documents/Locked');
  mkdirSync(locked, { mode: 0o700, recursive: true });
  writeFileSync(join(locked, 'Inside.txt'), 'Synthetic.\n');
  chmodSync(locked, 0);
  try {
    expect(
      (
        await request.post('/api/file-search/refresh', {
          headers: indexHeaders,
          data: { id: searchOnly.id, generation: searchOnly.generation }
        })
      ).ok()
    ).toBeTruthy();
    await waitFor('Documents is only partly indexed', list =>
      list.some((s: any) => s.label === 'Documents' && s.state === 'partial')
    );

    await search(page).fill('harbor');
    await expect(rows(page)).toHaveCount(2);
    const partly = pane(page).locator('[data-results-partly-indexed]');
    await expect(partly).toHaveCount(1);
    await expect(partly).toContainText('Documents');
    await expect(partly).toContainText('some files may be missing');
    await page.screenshot({ path: join(evidence, 'partly-indexed.png') });

    // Refresh index re-indexes it; once it can be read the notice goes.
    chmodSync(locked, 0o700);
    await partly.getByRole('button', { name: 'Refresh index' }).click();
    await expect.poll(() => post('refresh').length, { timeout: 30000 }).toBeGreaterThan(0);
    await expect(pane(page).locator('[data-results-partly-indexed]')).toHaveCount(0, {
      timeout: 30000
    });
    await expect(rows(page)).toHaveCount(2);
  } finally {
    chmodSync(locked, 0o700);
  }
});

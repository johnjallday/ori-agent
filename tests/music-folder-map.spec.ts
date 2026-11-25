import { expect, test, type APIRequestContext } from '@playwright/test';
import { join } from 'node:path';
import { musicHomeSnapshot } from './helpers/music-home-placement';
import { installLocalCdn } from './helpers/offline-cdn';
import { verifyMusicFolderViews } from './helpers/music-folder-matrix';

const evidence = 'tasks/evidence-music-folder-hierarchy';
const mode = process.env.ORI_MUSIC_FOLDER_MAP;
test.skip(!mode, 'requires the listed eight-entry disposable Music Home fixture');
async function listedHome(request: APIRequestContext) {
  const state = await musicHomeSnapshot(request);
  const home = state.workspaces.find(
    (row: any) => row.group_template?.plugin_id === 'music-project-management'
  );
  expect(home).toBeTruthy();
  return home;
}

test('choose two tracks for placement and review remaining songs separately', async ({
  page,
  request
}) => {
  test.skip(mode !== 'setup');
  test.setTimeout(180000);
  await installLocalCdn(page);
  const home = await listedHome(request);
  const sheet = page.locator(`[data-library-sheet="${home.id}"]`);
  const list = async () =>
    (
      await (
        await request.get(
          `/api/workspaces/${home.id}/assistant-program/library/projects?folder_sections=1&page_size=50`
        )
      ).json()
    ).rows;
  async function open() {
    await page.goto(`/workspaces/${home.folder_slug}?mode=map&setup_projects=1`);
    await expect(sheet.locator('.ws-map-library-sheet-rows').first()).toBeVisible({
      timeout: 30000
    });
  }
  async function confirm(n: number, label: string) {
    await sheet.getByRole('button', { name: `Put ${n} on the map`, exact: true }).click();
    const placement = sheet.locator('[data-sheet-staffing-choice="placement"]');
    if (await placement.count()) await placement.check();
    await sheet.locator('.ws-map-library-sheet-confirm').scrollIntoViewIfNeeded();
    await sheet
      .locator('.ws-map-library-sheet-confirm')
      .screenshot({ path: join(evidence, `confirm-${label}.png`) });
    await sheet.getByRole('button', { name: 'Put on the map', exact: true }).click();
    await expect(sheet.locator('[data-sheet-results]')).toBeVisible({ timeout: 60000 });
    if (label === 'tracks') {
      const retry = sheet.locator('[data-sheet-retry]');
      await expect(retry).toBeVisible();
      const first = (await list()).filter((r: any) => r.connection === 'connected');
      expect(first).toHaveLength(1);
      await sheet.screenshot({ path: join(evidence, 'partial-tracks.png') });
      await retry.click();
      await expect(sheet.locator('[data-sheet-result].is-complete')).toHaveCount(2);
      expect((await list()).find((r: any) => r.id === first[0].id)?.workspace_id).toBe(
        first[0].workspace_id
      );
    }
    await sheet.screenshot({ path: join(evidence, `receipt-${label}.png`) });
  }
  await open();
  await sheet
    .getByRole('checkbox', { name: /Select projects with a chosen file in .*Picker Songs/ })
    .check();
  let commits = 0;
  await page.route('**/activation/commit', async route => {
    if (++commits === 2)
      await route.fulfill({ status: 409, json: { error: 'Fixture: please review again' } });
    else await route.continue();
  });
  await confirm(2, 'tracks');
  expect(commits).toBe(3);
  let rows = await list();
  expect(rows).toHaveLength(8);
  expect(
    rows
      .filter((r: any) => r.connection === 'connected')
      .map((r: any) => r.name)
      .sort()
  ).toEqual(['Track-1', 'Track-2']);
  expect(rows.filter((r: any) => r.connection === 'catalog_only')).toHaveLength(6);
  await open();
  await expect(
    sheet.getByRole('checkbox', { name: /Select projects with a chosen file in .*Picker Songs/ })
  ).toBeDisabled();
  const files = sheet.getByRole('radiogroup', { name: 'Project file for Album-5' });
  await files.getByRole('radio', { name: 'Alternate.rpp' }).check();
  await sheet
    .getByRole('checkbox', { name: /Select projects with a chosen file in .*Albums/ })
    .check();
  await confirm(5, 'albums');
  rows = await list();
  expect(rows.filter((r: any) => r.connection === 'connected')).toHaveLength(7);
  const album = rows.find((r: any) => r.name === 'Album-5');
  const child = await (await request.get(`/api/workspaces/${album.workspace_id}`)).json();
  expect(child.shared_data.project_entry.relative_path).toBe('Alternate.rpp');
  const base = `/api/workspaces/${home.id}/assistant-program/library`;
  expect((await (await request.get(`${base}/roots`)).json()).total_roots).toBe(1);
  expect((await (await request.get(`${base}/collections`)).json()).total).toBe(0);
  const populated = await musicHomeSnapshot(request);
  expect(
    populated.workspaces.filter(
      (w: any) => w.group_template?.plugin_id === 'music-project-management'
    )
  ).toHaveLength(1);
  const fed = populated.workspaces.filter((w: any) => w.parent_id === home.id);
  expect(fed.map((w: any) => w.name)).toEqual(['Ori Music Demo']);
  expect(
    populated.workspaces
      .filter((w: any) => w.parent_id === fed[0].id)
      .map((w: any) => w.id)
      .sort()
  ).toEqual(
    rows
      .filter((r: any) => r.workspace_id)
      .map((r: any) => r.workspace_id)
      .sort()
  );
  // Fixture preparation, before the read-only map journey/restart. Interleave
  // the two branches explicitly rather than relying on renderer seed positions.
  const positions = Object.fromEntries(
    ['Album-1', 'Track-1', 'Album-2', 'Track-2', 'Album-3', 'Album-4', 'Album-5'].map((name, i) => [
      rows.find((r: any) => r.name === name).workspace_id,
      // Layout coordinates are world pixels, not grid indices. Separate
      // the interleaved buildings by more than one tile footprint.
      { x: (i % 3) * 228 + 38, y: Math.floor(i / 3) * 190 + 38 }
    ])
  );
  const saved = await request.patch('/api/workspace-map/layout', {
    data: { operations: [{ op: 'set_positions', positions }] }
  });
  expect(saved.ok()).toBeTruthy();
});

test('the populated map groups seven existing tiles without a layout or staffing write', async ({
  page,
  request
}) => {
  test.skip(mode !== 'existing');
  await installLocalCdn(page);
  const home = await listedHome(request);
  await page.goto(`/workspaces/${home.folder_slug}?mode=map`);
  const button = page.getByRole('button', { name: 'Group projects by folder', exact: true });
  await expect(button).toBeVisible({ timeout: 30000 });
  const before = await musicHomeSnapshot(request);
  await button.click();
  const map = page.getByRole('region', { name: 'Workspaces by folder' });
  await expect(map.locator('summary').filter({ hasText: 'Albums · 5' })).toBeVisible();
  const albums = map
    .locator('details')
    .filter({ has: page.locator('summary', { hasText: 'Albums · 5' }) });
  const tracks = map
    .locator('details')
    .filter({ has: page.locator('summary', { hasText: 'Picker Songs · 2' }) });
  await expect(albums.locator('button')).toHaveCount(5);
  await expect(tracks.locator('button')).toHaveCount(2);
  await map.screenshot({ path: join(evidence, 'existing-seven.png') });
  const collapse = albums.locator('summary');
  await collapse.focus();
  await collapse.press('Enter');
  await expect(collapse).toBeFocused();
  await expect(albums.locator('button').first()).toBeHidden();
  await tracks.getByRole('button', { name: 'Track-1', exact: true }).click();
  await expect(page).not.toHaveURL(new RegExp(home.folder_slug));
  await page.goto(`/workspaces/${home.folder_slug}?mode=map`);
  await button.click();
  await page.getByRole('button', { name: 'Saved layout' }).click();
  await expect(map).toHaveCount(0);
  await verifyMusicFolderViews(page, `/workspaces/${home.folder_slug}?mode=map`, 'map');
  const after = await musicHomeSnapshot(request);
  expect(after.positions).toEqual(before.positions);
  const ids = (s: any) => s.workspaces.map((w: any) => [w.id, w.parent_id, w.agents]);
  expect(ids(after)).toEqual(ids(before));
});

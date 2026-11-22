import { expect, test, type APIRequestContext } from '@playwright/test';
import { join } from 'node:path';
import { musicHomeSnapshot } from './helpers/music-home-placement';
import { installLocalCdn } from './helpers/offline-cdn';

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
    await sheet.screenshot({ path: join(evidence, `confirm-${label}.png`) });
    await sheet.getByRole('button', { name: 'Put on the map', exact: true }).click();
    await expect(sheet.locator('[data-sheet-results]')).toBeVisible({ timeout: 60000 });
    await sheet.screenshot({ path: join(evidence, `receipt-${label}.png`) });
  }
  await open();
  await sheet
    .getByRole('checkbox', { name: /Select projects with a chosen file in .*Picker Songs/ })
    .check();
  await confirm(2, 'tracks');
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
  expect(JSON.stringify(child)).toContain('Alternate.rpp');
});

test('the populated map groups seven existing tiles without a layout or staffing write', async ({
  page,
  request
}) => {
  test.skip(mode !== 'existing');
  await installLocalCdn(page);
  const home = await listedHome(request);
  await page.goto(`/workspaces/${home.folder_slug}?mode=map`);
  const button = page.getByRole('button', { name: 'By folder', exact: true });
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
  const after = await musicHomeSnapshot(request);
  expect(after.positions).toEqual(before.positions);
  const ids = (s: any) => s.workspaces.map((w: any) => [w.id, w.parent_id, w.agents]);
  expect(ids(after)).toEqual(ids(before));
});

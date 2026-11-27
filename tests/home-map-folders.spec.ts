import { expect, test } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Read-only replay against the disposable two-feed journey. Never seeds, picks
// a folder or buys a model turn. Setup/placement itself is the live demo.
const route = process.env.ORI_HOME_MAP_FOLDERS_ROUTE || '';
const evidence = 'tasks/evidence-home-map-folders';
const listedFolders = Number(process.env.ORI_HOME_MAP_LISTED_FOLDERS || 2);
test.skip(!route, 'requires a disposable Home with two fully listed fed folders');
test('Add folder requests a native choice for the page and Cancel returns focus without hiring', async ({
  page
}) => {
  await installLocalCdn(page);
  const chooser = '**/api/personal-assistant/folder-agent/choose';
  // Stub only the cancelled native result. This test neither drives an OS
  // dialog nor buys a model turn; those belong to the live journey.
  await page.route(chooser, r => r.fulfill({ json: { cancelled: true } }));
  let starts = 0;
  page.on('request', r => {
    if (r.url().endsWith('/folder-agent/start')) starts++;
  });
  await page.goto(route);
  const add = page.getByRole('button', { name: '+ Add folder', exact: true });
  const read = page.waitForRequest(chooser);
  await add.click();
  const body = (await read).postDataJSON();
  expect(Object.keys(body).sort()).toEqual(['from_workspace_id', 'in_home', 'picker']);
  expect(body.in_home).toBe(true);
  expect(body.picker).toBe(true);
  const homeRead = await page.request.get(
    `/api/workspaces/${encodeURIComponent(body.from_workspace_id)}/assistant-program/map-folders`
  );
  expect((await homeRead.json()).can_add_folder).toBe(true);
  await expect(page.getByRole('dialog', { name: 'Add folder' })).toHaveCount(0);
  await expect(add).toBeFocused();
  expect(starts).toBe(0);
});

test('extra folders fold into a reachable disclosure on a narrow map', async ({ page }) => {
  await installLocalCdn(page);
  await page.setViewportSize({ width: 390, height: 844 });
  // A presentation fixture only: no station or library root is created. Keep
  // the actual last folder as the keyboard navigation target.
  await page.route('**/assistant-program/map-folders', async route => {
    const response = await route.fetch();
    const data = await response.json();
    for (let i = 0; i < 4; i++)
      data.folders.push({ workspace_id: `fixture-${i}`, name: `A ${i}`, songs: i });
    await route.fulfill({ response, json: data });
  });
  await page.goto(route);
  const names = page.locator('[data-cmd-folder-names]');
  const more = names.locator('summary');
  await expect(more).toHaveText(`${listedFolders} more folders`);
  await expect(names.locator('[data-cmd-frame-folder]:visible')).toHaveCount(4);
  await more.focus();
  await page.keyboard.press('Enter');
  const last = names.locator('details [data-cmd-frame-folder]').last();
  const bounds = await last.boundingBox();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(391);
  await last.click();
  await expect(more).toBeFocused();
  await expect(names.locator('details')).not.toHaveAttribute('open', '');
});

test('a pending hire is one station inside the originating Home on both maps', async ({ page }) => {
  const id = process.env.ORI_HOME_MAP_PENDING_ID;
  test.skip(!id, 'requires a disposable map hire before Set up');
  await installLocalCdn(page);
  const view = (await (await page.request.get(`/api/personal-assistant/folder-agent/${id}`)).json())
    .folder_agent;
  expect(['looking', 'reported']).toContain(view.state);
  expect(view.setup).toBeFalsy();
  const home = view.origin_home_id;
  const tile = page.locator(`.ws-map-tile[data-ws-id="${id}"]`);
  await page.goto(route);
  await expect(tile).toHaveCount(1);
  await expect(tile).toBeVisible();
  await expect(page.locator(`[data-cmd-home-feed="${id}"]`)).toHaveCount(1);
  await expect(page.locator(`[data-cmd-frame-folder="${id}"]`)).toHaveCount(0);
  await page.goto('/');
  const district = page.locator(`.ws-map-district[data-group-id="${home}"]`);
  await expect(tile).toHaveCount(1);
  // Districts and tiles share a flat canvas. Check the actual drawn bounds,
  // not only parent_id, so a misplaced tile on the global map fails too.
  await expect
    .poll(async () => {
      const [t, d] = await Promise.all([tile.boundingBox(), district.boundingBox()]);
      return (
        !!t &&
        !!d &&
        t.x >= d.x - 1 &&
        t.y >= d.y - 1 &&
        t.x + t.width <= d.x + d.width + 1 &&
        t.y + t.height <= d.y + d.height + 1
      );
    })
    .toBe(true);
  const tree = await (await page.request.get('/api/workspaces?tree=true')).json();
  const roots = tree.workspaces || tree.folders;
  expect(roots.some((w: { id: string }) => w.id === id)).toBe(false);
  expect(
    roots
      .find((w: { id: string }) => w.id === home)
      ?.children.filter((w: { id: string }) => w.id === id)
  ).toHaveLength(1);
  await page.screenshot({ path: join(evidence, 'pending-in-global-home.png') });
});

for (const width of [1440, 390]) {
  for (const theme of ['light', 'dark']) {
    test(`fed folders on the Home map at ${width}px in ${theme}`, async ({ page }) => {
      expect(route).toMatch(/^\/workspaces\/[\w-]+\?mode=map$/);
      await installLocalCdn(page);
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
      let layoutWrites = 0;
      page.on('request', r => {
        if (r.method() !== 'GET' && r.url().includes('/layout')) layoutWrites++;
      });
      await page.goto(route);
      const bar = page.getByRole('toolbar', { name: 'Group map controls' });
      const chips = bar.locator('[data-cmd-frame-folder]');
      await expect(chips).toHaveCount(listedFolders);
      await expect(bar.getByRole('button', { name: '+ Add folder' })).toBeVisible();
      await expect(page.locator('html')).toHaveAttribute('data-bs-theme', theme);
      const chip = chips.first();
      const id = await chip.getAttribute('data-cmd-frame-folder');
      const tile = page
        .locator(`.ws-map-tile[data-ws-id="${id}"], .ws-map-district-tag[data-ws-id="${id}"]`)
        .first();
      await expect(tile).toBeVisible();
      await chip.click();
      await expect(tile).toHaveAttribute('aria-pressed', 'true');
      await expect(page.locator('.is-just-placed')).toHaveCount(0);
      for (const button of await bar.locator('button:visible').all()) {
        const bounds = await button.boundingBox();
        expect(bounds!.x).toBeGreaterThanOrEqual(-1);
        expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width + 1);
      }
      mkdirSync(evidence, { recursive: true });
      await bar.screenshot({ path: join(evidence, `toolbar-${width}-${theme}.png`) });
      const setup = bar.locator('[data-cmd-folder-root]').first();
      const root = await setup.getAttribute('data-cmd-folder-root');
      const request = page.waitForRequest(
        r =>
          new URL(r.url()).searchParams.get('root_id') === root &&
          r.url().includes('/library/projects?')
      );
      await setup.focus();
      await page.keyboard.press('Enter');
      await request;
      await expect(page.locator('[data-library-sheet]')).toBeVisible();
      expect(layoutWrites).toBe(0);
    });
  }
}

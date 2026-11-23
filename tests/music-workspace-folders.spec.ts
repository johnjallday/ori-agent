import { expect, test, type Page } from '@playwright/test';
import { join } from 'node:path';
import { readFileSync, writeFileSync } from 'node:fs';
import { withOwnedBrowserZoom } from './helpers/owned-browser-zoom';
import { musicHomeSnapshot } from './helpers/music-home-placement';
import { installLocalCdn } from './helpers/offline-cdn';

const evidence = 'tasks/evidence-music-workspace-folder-context';
test.skip(!process.env.ORI_MUSIC_FOLDER_MAP, 'requires the isolated seven-project Music Home');

async function groupFixture(page: Page) {
  await installLocalCdn(page);
  const snapshot = await musicHomeSnapshot(page.request);
  const home = snapshot.workspaces.find(
    w => w.group_template?.plugin_id === 'music-project-management'
  );
  const group = snapshot.workspaces.find(
    w => w.parent_id === home?.id && w.name === 'Ori Music Demo'
  );
  expect(group).toBeTruthy();
  return { snapshot, group: group!, home: home! };
}

test('group Overview keeps source folders with their real member rows', async ({ page }) => {
  const { group } = await groupFixture(page);
  await page.goto(`/workspaces/${group.folder_slug}?mode=details`);
  const region = page.getByRole('region', { name: 'Linked workspaces', exact: true });
  for (const [name, label] of [
    ['Track-1', 'Picker Songs'],
    ['Album-1', 'Albums']
  ]) {
    const open = region.getByRole('button', { name: `Open ${name}`, exact: true });
    await expect(open).toHaveAccessibleDescription(`Source folder: ${label}`);
    await expect(open.locator('[data-member-folder]')).toBeVisible();
  }
  await expect(region.locator('[data-member-id]')).toHaveCount(7);
  const tree = await (await page.request.get('/api/workspaces?tree=true')).json();
  const rows = (nodes: any[]): any => {
    for (const node of nodes) {
      if (node.id === group.id) return node.children;
      const found = rows(node.children || []);
      if (found) return found;
    }
  };
  expect(
    await region
      .locator('[data-member-id]')
      .evaluateAll(els => els.map(e => e.getAttribute('data-member-id')))
  ).toEqual(rows(tree.workspaces || tree.folders).map((r: any) => r.id));
  await region.screenshot({ path: join(evidence, 'group-overview.png') });
});

test('saved map tiles show scoped source folders without a layout write', async ({ page }) => {
  const { snapshot, group, home } = await groupFixture(page);
  const track = snapshot.workspaces.find(w => w.parent_id === group.id && w.name === 'Track-1');
  const writes: string[] = [];
  page.on('request', req => {
    if (req.method() !== 'GET' && req.url().includes('/api/workspace-map/layout'))
      writes.push(req.method());
  });
  for (const scope of [home, group]) {
    await page.goto(`/workspaces/${scope.folder_slug}?mode=map`);
    const tile = page.locator(`.ws-map-tile[data-ws-id="${track.id}"]`);
    await expect(tile).toHaveAccessibleName(/Track-1.*Source folder: Picker Songs/);
    await expect(tile.locator('[data-source-folder]')).toHaveText('Picker Songs');
    // The whole district opens framed to fit. Inspect the actual caption at
    // reading size, as when zooming in on a building.
    const zoom = page.locator('.ws-cmd-detachment-host [data-map-zoom-in]');
    for (let i = 0; i < 14 && (await tile.boundingBox())!.width < 180; i++)
      await zoom.evaluate(e => (e as HTMLButtonElement).click());
    await tile.focus();
    await tile.screenshot({
      path: join(evidence, `${scope === home ? 'home' : 'group'}-saved-tile.png`)
    });
    await tile.press('Enter');
    await expect(page).toHaveURL(new RegExp(`/workspaces/${track.folder_slug}`));
  }
  expect(writes).toEqual([]);
  expect((await musicHomeSnapshot(page.request)).positions).toEqual(snapshot.positions);
});

test('child By folder opens only member tiles and returns to the same saved layout', async ({
  page
}) => {
  const { snapshot, group } = await groupFixture(page);
  const scope = await page.request.get(`/api/workspaces/${group.id}/source-folders`);
  expect(scope.ok()).toBeTruthy();
  const labels = (await scope.json()).rows;
  expect(labels.map(r => r.workspace_id).sort()).toEqual(
    snapshot.workspaces
      .filter(w => w.parent_id === group.id)
      .map(w => w.id)
      .sort()
  );
  writeFileSync(join(evidence, 'scoped-labels.json'), JSON.stringify(labels, null, 2));
  const identity = s => ({
    positions: s.positions,
    workspaces: s.workspaces.map(w => [w.id, w.parent_id || '', w.agents || []]),
    agents: s.agents.map(a => a.name),
    labels
  });
  const snapshotFile = join(evidence, 'before-restart.json');
  if (process.env.ORI_MUSIC_FOLDER_MAP === 'setup')
    writeFileSync(snapshotFile, JSON.stringify(identity(snapshot), null, 2));
  const route = `/workspaces/${group.folder_slug}?mode=map`;
  const members = snapshot.workspaces.filter(w => w.parent_id === group.id);
  const writes: string[] = [];
  page.on('request', r => {
    if (
      r.method() !== 'GET' &&
      /workspace-map\/layout|activation\/commit|staffing\/commit/.test(new URL(r.url()).pathname)
    )
      writes.push(r.url());
  });
  await page.goto(route);
  const toggle = page.locator('[data-cmd-by-folder]');
  await toggle.focus();
  await toggle.press('Enter');
  const folders = page.getByRole('region', { name: 'Workspaces by folder' });
  await expect(folders.locator('summary')).toHaveText(['Albums · 5', 'Picker Songs · 2']);
  expect(
    (
      await folders
        .locator('[data-folder-workspace]')
        .evaluateAll(els => els.map(e => e.getAttribute('data-folder-workspace')))
    ).sort()
  ).toEqual(members.map(w => w.id).sort());
  await folders.screenshot({ path: join(evidence, 'child-by-folder.png') });
  await folders.locator('summary').first().press('Space');
  await expect(folders.getByRole('button', { name: 'Album-1', exact: true })).toBeHidden();
  await folders.getByRole('button', { name: 'Track-1', exact: true }).press('Enter');
  await expect(page).toHaveURL(
    new RegExp(`/workspaces/${members.find(w => w.name === 'Track-1').folder_slug}`)
  );
  await page.goto(route);
  await toggle.press('Enter');
  await expect(folders).toBeVisible();
  await toggle.press('Space');
  await expect(toggle).toBeFocused();
  await expect(folders).toHaveCount(0);
  await page
    .locator('.ws-cmd-detachment-host')
    .screenshot({ path: join(evidence, 'restored-layout.png') });
  // A failed read is not an empty inventory, and never traps the keyboard.
  await page.route('**/source-folders', r => r.fulfill({ status: 409, json: {} }));
  await toggle.press('Enter');
  await expect(page.locator('.music-folder-view')).toContainText('could not be loaded');
  await toggle.press('Enter');
  await expect(toggle).toBeFocused();
  expect(writes).toEqual([]);
  const after = await musicHomeSnapshot(page.request);
  expect(identity(after)).toEqual(identity(snapshot));
  if (process.env.ORI_MUSIC_COMPARE_RESTART === '1') {
    expect(identity(after)).toEqual(JSON.parse(readFileSync(snapshotFile, 'utf8')));
    writeFileSync(join(evidence, 'after-restart.json'), JSON.stringify(identity(after), null, 2));
  }
});

test('group folder text and controls at narrow width and actual 200 percent zoom', async ({
  page
}) => {
  test.skip(process.env.ORI_MUSIC_FOLDER_MAP !== 'existing', 'layout matrix runs after restart');
  const { group, snapshot } = await groupFixture(page);
  const track = snapshot.workspaces.find(w => w.name === 'Track-1' && w.parent_id === group.id);
  async function check(p: Page, theme: string, name: string) {
    await installLocalCdn(p);
    await p.emulateMedia({ reducedMotion: 'reduce' });
    // At native tab zoom Playwright's CSS-pixel clip can capture only the
    // upper-left quadrant. Capture the compositor's full viewport instead.
    const browser = await p.context().newCDPSession(p);
    const capture = async (part: string) => {
      const { data } = await browser.send('Page.captureScreenshot', {
        format: 'png',
        captureBeyondViewport: false
      });
      writeFileSync(join(evidence, `${part}-${name}.png`), Buffer.from(data, 'base64'));
    };
    await p.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
    await p.goto(`/workspaces/${group.folder_slug}?mode=details`);
    await expect(p.locator('html')).toHaveAttribute('data-bs-theme', theme);
    const row = p.locator(`[data-member-id="${track.id}"]`);
    await expect(row).toHaveAccessibleDescription('Source folder: Picker Songs');
    await row.focus();
    await expect(row).toBeFocused();
    await row.evaluate(el => el.scrollIntoView({ block: 'center', behavior: 'instant' }));
    await capture('row');
    await p.goto(`/workspaces/${group.folder_slug}?mode=map`);
    const tile = p.locator(`.ws-map-tile[data-ws-id="${track.id}"]`);
    await expect(tile).toHaveAccessibleName(/Source folder: Picker Songs/);
    const zoom = p.locator('.ws-cmd-detachment-host [data-map-zoom-in]');
    for (let i = 0; i < 14 && (await tile.boundingBox())!.width < 180; i++)
      await zoom.evaluate(el => (el as HTMLElement).click());
    await expect
      .poll(() => p.evaluate(id => (window as any).OriWorkspaceMap.reveal(id), track.id))
      .toBe(true);
    await tile.focus();
    await tile.evaluate(el => el.scrollIntoView({ block: 'center', behavior: 'instant' }));
    await capture('tile');
    const toggle = p.locator('[data-cmd-by-folder]');
    await toggle.press('Enter');
    const headings = p.locator('.music-folder-view summary');
    await expect(headings).toHaveText(['Albums · 5', 'Picker Songs · 2']);
    await headings.first().focus();
    await p.keyboard.press('Space');
    await expect(headings.first()).toBeFocused();
    await p.keyboard.press('Tab');
    await expect(headings.last()).toBeFocused();
    await headings
      .last()
      .evaluate(el => el.scrollIntoView({ block: 'center', behavior: 'instant' }));
    await capture('folders');
    await browser.detach();
    await toggle.press('Enter');
    await expect(toggle).toBeFocused();
  }
  const base = new URL((await page.request.get('/health')).url()).origin;
  await page.setViewportSize({ width: 390, height: 844 });
  await check(page, 'light', 'narrow-light');
  await withOwnedBrowserZoom(base, 2, p => check(p, 'dark', 'zoom-dark'));
});

test('an intentional tile move still saves on the group map', async ({ page }) => {
  test.skip(process.env.ORI_MUSIC_FOLDER_MAP !== 'move', 'explicit move demo only');
  const { snapshot, group } = await groupFixture(page);
  const song = snapshot.workspaces.find(w => w.parent_id === group.id && w.name === 'Track-1');
  await page.goto(`/workspaces/${group.folder_slug}?mode=map`);
  const tile = page.locator(`.ws-map-tile[data-ws-id="${song.id}"]`);
  await expect(tile).toHaveAccessibleName(/Source folder: Picker Songs/);
  await tile.focus();
  await tile.press('Space');
  await page.locator('.ws-cmd-detachment-host [data-map-drag]').evaluate(e => {
    if (e.getAttribute('aria-pressed') !== 'true') (e as HTMLElement).click();
  });
  const canvas = page.locator('.ws-cmd-detachment-host .ws-map-canvas');
  await canvas.press('ArrowRight');
  await canvas.press('Enter');
  await expect
    .poll(async () => (await musicHomeSnapshot(page.request)).positions[song.id])
    .not.toEqual(snapshot.positions[song.id]);
  await page.reload();
  await expect(tile).toHaveAccessibleName(/Source folder: Picker Songs/);
  await tile.press('Enter');
  await expect(page).toHaveURL(new RegExp(`/workspaces/${song.folder_slug}`));
});

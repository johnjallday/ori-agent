import { expect, test } from '@playwright/test';
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  realpathSync,
  renameSync,
  writeFileSync
} from 'node:fs';
import { dirname, join, sep } from 'node:path';
import { tmpdir } from 'node:os';
import { installLocalCdn } from './helpers/offline-cdn';

// Fresh picker fixture (music-folder-hierarchy-demo.sh), Track-1 placed and
// Track-2 still on the shelf. This case really sets up Track-2, exactly once.
const sandbox = process.env.ORI_FILES_HOME_SANDBOX;
test.skip(!sandbox, 'requires a disposable two-track shelf');
test('a fed folder previews the originals, opens its song and sets up just the chosen project', async ({
  page,
  request
}) => {
  test.setTimeout(90000);
  const root = realpathSync(sandbox!);
  expect(dirname(root)).toBe(realpathSync(tmpdir()));
  expect(root.split(sep).at(-1)).toMatch(/^ori-music-hierarchy\./);
  const ws = async (url: string, data: object) => {
    const res = await request.post(url, { data });
    expect(res.ok(), await res.text()).toBeTruthy();
    return res.json();
  };
  const tree = await (await request.get('/api/workspaces?tree=true')).json();
  const home = (tree.workspaces || tree.folders).find(
    (r: any) => r.group_template?.kind === 'managed_home'
  );
  const base = `/api/workspaces/${home.id}`;
  const output = await (await request.get(`${base}/output-dir`)).json();
  expect(realpathSync(dirname(output.output_dir)).startsWith(root + sep)).toBeTruthy();
  const folder = realpathSync(join(root, 'fixture', 'Ori Music Demo'));
  const track = realpathSync(join(folder, 'Picker Songs', 'Track-1'));
  expect(track.startsWith(root + sep)).toBeTruthy();
  const shelf = await (
    await request.get(`${base}/assistant-program/library/projects?page_size=50`)
  ).json();
  const song = shelf.rows.find((r: any) => r.name === 'Track-1');
  expect(song.connection).toBe('connected');
  const detail = await (
    await request.get(`${base}/assistant-program/library/projects/${song.id}`)
  ).json();
  expect(shelf.rows.find((r: any) => r.name === 'Track-2').connection).toBe('catalog_only');
  if (!home.children.some((r: any) => r.name === 'Second feed')) {
    const second = mkdtempSync(join(root, 'Second feed '));
    await ws('/api/workspaces', {
      name: 'Second feed',
      parent_id: home.id,
      blank: true,
      create_template_agents: true,
      anchor_path: second
    });
    const missing = mkdtempSync(join(root, 'Missing '));
    await ws(`${base}/directories`, { name: 'Archive', path: missing });
    renameSync(missing, missing + ' offline');
    await ws(`${base}/notes`, { name: 'Home journal', content: 'Saved in Ori' });
    await ws(`/api/workspaces/${detail.workspace_id}/notes`, {
      name: 'Song journal',
      content: 'Song note'
    });
  }
  writeFileSync(join(track, 'credits.txt'), 'Lead vocals');
  writeFileSync(join(folder, 'loose.txt'), 'Loose text');
  const evidence = 'tasks/evidence-files-disk-first';
  mkdirSync(evidence, { recursive: true });
  const shot = (name: string) => page.screenshot({ path: join(evidence, name + '.png') });
  await installLocalCdn(page);
  const url = `/workspaces/${home.folder_slug}?mode=files`;
  const marksURL = '**/assistant-program/map-folders?files=1';
  // A failed optional read cannot delay or replace names. Then exercise the
  // same real shelf response with the writable flag off on a narrow screen.
  await page.route(marksURL, route => route.fulfill({ status: 503, body: 'Unavailable' }));
  await page.goto(url);
  const files = page.locator('#workspace-files-view');
  const row = (name: string) => files.getByRole('treeitem', { name, exact: true });
  await row('Picker Songs').click();
  await expect(row('Track-1')).toBeVisible();
  await page.unroute(marksURL);
  await page.route(marksURL, async route => {
    const res = await route.fetch();
    await route.fulfill({ response: res, json: { ...(await res.json()), can_set_up: false } });
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  await row('Picker Songs').click();
  await row('Track-2 not set up').click();
  await expect(files.locator('.wf-body')).toContainText('Project not set up');
  await expect(files.getByRole('button', { name: 'Set up this project' })).toHaveCount(0);
  await shot('read-only-narrow');
  await files.getByRole('button', { name: 'Back to files' }).click();
  await page.unroute(marksURL);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.reload();
  await expect(row('Folders on this Mac')).toBeVisible();
  await expect(row('Folder unavailable. Retry')).toBeVisible();
  expect(
    (await (await request.get(`${base}/assistant-program/map-folders`)).json()).folders
  ).toHaveLength(2);
  await row('Picker Songs').click();
  const mapped = row('Track-1 on the map');
  const project = row('Track-2 not set up');
  await expect(mapped).toHaveCount(1);
  await expect(project).toBeVisible();
  await shot('both-feeds');
  await mapped.click();
  await expect(files.getByRole('link', { name: 'Open workspace' })).toBeVisible();
  await expect(files.locator('.wf-body')).toContainText('Song journal');
  await shot('song-pane');
  await row('credits.txt').click();
  await expect(files.locator('pre')).toHaveText('Lead vocals');
  await row('loose.txt').click();
  await expect(files.locator('pre')).toHaveText('Loose text');
  await mapped.click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Move to…' }).click();
  const move = page.getByRole('dialog', { name: 'Move 1 folder' });
  await expect(move).toContainText('song on the map');
  await move.getByRole('button', { name: 'Cancel' }).click();
  await mapped.click({ button: 'right' });
  let warning = '';
  page.once('dialog', async dialog => {
    warning = dialog.message();
    await dialog.dismiss();
  });
  await page.getByRole('menuitem', { name: 'Move to Trash…' }).click();
  expect(warning).toContain('song on the map');
  expect(existsSync(track)).toBeTruthy();
  await row('Stored in Ori').click();
  await files.locator(`[data-key="${home.id}/s/notes"]`).click();
  await expect(row('Home journal')).toBeVisible();
  await files
    .locator(
      `[data-key="${home.children.find((w: any) => w.children?.some((c: any) => c.id === detail.workspace_id)).id}"]`
    )
    .click();
  await files.locator(`[data-key="${detail.workspace_id}"]`).click();
  await files.locator(`[data-key="${detail.workspace_id}/s/notes"]`).click();
  await expect(row('Song journal')).toBeVisible();
  await project.click();
  await files.getByRole('button', { name: 'Set up this project' }).click();
  const sheet = page.locator('[data-library-sheet]');
  await expect(sheet).toContainText('Track-2');
  await expect(sheet).not.toContainText('Track-1');
  await shot('project-review');
  await sheet.getByRole('button', { name: 'Put 1 on the map', exact: true }).click();
  const placement = sheet.locator('[data-sheet-staffing-choice="placement"]');
  if (await placement.count()) await placement.check();
  await sheet.getByRole('button', { name: 'Put on the map', exact: true }).click();
  await expect(sheet.locator('[data-sheet-results]')).toBeVisible({ timeout: 45000 });
  await sheet.getByRole('button', { name: 'Close', exact: true }).first().click();
  await expect(row('Track-2 on the map')).toHaveCount(1);
  await expect(project).toHaveCount(0);
  await shot('updated-without-reload');
});

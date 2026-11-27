import { test, expect } from '@playwright/test';
import { mkdtemp, readFile, readdir, realpath, utimes, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';
import { freshGuidedHQ } from './helpers/fresh-guided-hq';
import { installLocalCdn } from './helpers/offline-cdn';

// scripts/e2e-fresh.sh --sandbox-env GROUNDS_SANDBOX tests/hq-grounds.spec.ts -- --workers=1
const sandbox = process.env.GROUNDS_SANDBOX;
test.skip(!sandbox, 'requires a fresh isolated demo server');
test.use({ viewport: { width: 1280, height: 900 }, hasTouch: true });

test('HQ grounds: review without filing, place a building, remember Flow', async ({
  page,
  request
}, info) => {
  test.setTimeout(90_000);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  const home = await realpath(sandbox!);
  expect(basename(home)).toMatch(/^ori-e2e\./);
  expect(await readFile(join(home, '.ori-demo-owner'), 'utf8')).toBe(
    `worktree=${await realpath(process.cwd())}\n`
  );
  const path = await freshGuidedHQ(request);
  const post = async (url: string, data = {}) => {
    const r = await request.post(url, { data });
    expect(r.ok(), `${url}: ${await r.text()}`).toBeTruthy();
    return r;
  };
  await post('/api/personal-hq/first-visit-state', { state: 'done' });
  const { status } = await (await request.get('/api/personal-hq/status')).json();
  const workspace = `/api/workspaces/${status.workspace_id}`;
  const root = await mkdtemp(join(home, 'grounds-inbox-'));
  const files = ['brief.pdf', 'index.csv', 'map.png'];
  const old = new Date(Date.now() - 3600000);
  for (const name of files) {
    await writeFile(join(root, name), 'Example: ' + name);
    await utimes(join(root, name), old, old);
  }
  await post(`${workspace}/capabilities/file-janitor/install`, { source: 'in-place' });
  await post(`${workspace}/file-janitor/setup`, { path: root, paused: true });
  await post(`${workspace}/file-janitor/scan`);
  const before = (await readdir(root)).sort();
  await installLocalCdn(page);
  await page.addInitScript(() => localStorage.setItem('ori-theme', 'light'));
  await page.goto(path + '?mode=map');
  const grounds = page.getByRole('region', { name: 'HQ grounds' });
  const btn = (key: string) => grounds.locator(`[data-cmd-hq-station="${key}"]`);
  const depot = btn('file-janitor');
  const memory = btn('memory');
  const review = page.getByRole('dialog', { name: 'Filing', exact: true });
  await expect(depot).toHaveAccessibleName('Filing · 3 to review');
  await expect(depot.locator('[data-building-variant="depot"]')).toBeVisible();
  await expect(memory.locator('[data-building-variant="archive"]')).toBeVisible();
  await expect(btn('watchtower').locator('[data-building-variant="tower"]')).toBeVisible();
  await expect(grounds.locator('[data-building-variant="hq"]')).toBeVisible();
  await expect(grounds.getByRole('button', { name: /Lead.*Atlas/ })).toBeVisible();
  await expect(memory.locator('.ws-cmd-map-hq-station-state')).toHaveCount(0);
  await btn('daily-brief').focus();
  await page.keyboard.press('Tab');
  await expect(depot).toBeFocused();
  await depot.evaluate(el => el.scrollIntoView({ block: 'center', behavior: 'instant' }));
  await page.screenshot({ path: info.outputPath('light-keyboard.png') });
  await depot.press('Enter');
  await expect(review).toBeVisible();
  await expect(review.getByRole('tab', { name: 'Review' })).toHaveAttribute(
    'aria-selected',
    'true'
  );
  const panelBox = (await review.boundingBox())!;
  const depotBox = (await depot.boundingBox())!;
  expect(
    panelBox.x + panelBox.width <= depotBox.x || depotBox.x + depotBox.width <= panelBox.x
  ).toBe(true);
  await expect(depot).toHaveCSS('outline-style', 'solid');
  await page.screenshot({ path: info.outputPath('depot-panel.png') });
  await page.keyboard.press('Escape');
  await expect(review).toBeHidden();
  await expect(depot).toBeFocused();
  expect((await readdir(root)).sort()).toEqual(before);
  for (const name of files)
    expect(await readFile(join(root, name), 'utf8')).toBe('Example: ' + name);

  // Drop against the whole field, exactly as the layout endpoint stores it.
  await memory.scrollIntoViewIfNeeded();
  const box = (await memory.boundingBox())!;
  const field = (await grounds.boundingBox())!;
  const save = page.waitForResponse(
    r => r.url().endsWith('/station-layout') && r.request().method() === 'PUT'
  );
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(field.x + field.width * 0.32, field.y + field.height * 0.32, { steps: 10 });
  await page.mouse.up();
  expect((await save).ok()).toBeTruthy();
  await expect(page.locator('.ws-cmd-modal:not([hidden])')).toHaveCount(0);
  const position = () => memory.getAttribute('style');
  const dropped = await position();
  await memory.press('Enter');
  await expect(page.locator('.ws-cmd-modal:not([hidden])')).toBeVisible();
  await page.keyboard.press('Escape');
  await page.reload();
  await expect.poll(position).toEqual(dropped);

  const flow = page.getByRole('button', { name: 'Flow', exact: true });
  await flow.click();
  await page.reload();
  await expect(flow).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('.ws-cmd-flow-lead')).toBeVisible();
  await flow.evaluate(el => el.scrollIntoView({ block: 'center', behavior: 'instant' }));
  await page.screenshot({ path: info.outputPath('remembered-flow.png') });
  await page.getByRole('button', { name: 'Grounds', exact: true }).click();

  // The only simulated response: a bounded status read fails. No fake station
  // or fabricated action is installed, and the rest of the field stays usable.
  await page.route('**/api/personal-hq/watchtower?*', route =>
    route.fulfill({ status: 503, body: '{}' })
  );
  await page.reload();
  await expect(btn('watchtower')).toContainText('Unavailable');
  await expect.poll(position).toEqual(dropped);
  await page.locator('#darkModeToggle').click();
  await expect(page.locator('html')).toHaveAttribute('data-bs-theme', 'dark');
  await page.setViewportSize({ width: 390, height: 844 });
  await grounds.evaluate(el => el.scrollIntoView({ block: 'center', behavior: 'instant' }));
  await page.screenshot({ path: info.outputPath('dark-phone-unavailable.png') });
  await depot.tap();
  await expect(review).toBeVisible();
  await review.getByRole('button', { name: 'Back to map' }).tap();
  await expect(review).toBeHidden();
  expect((await readdir(root)).sort()).toEqual(before);

  const ordinary = (await (await post('/api/workspaces', { name: 'Archive only' })).json()).folder;
  await page.goto(`/workspaces/${ordinary.folder_slug}?mode=map`);
  await expect(page.locator('[data-cmd-hq-station="memory"]')).toBeVisible();
  await expect(page.locator('[data-station-visual="depot"], .ws-cmd-grounds')).toHaveCount(0);
});

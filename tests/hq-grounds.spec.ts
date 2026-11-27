import { test, expect } from '@playwright/test';
import { mkdtemp, readFile, readdir, realpath, utimes, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';
import { freshGuidedHQ } from './helpers/fresh-guided-hq';
import { filingWorkspace } from './helpers/filing-workspace';
import { installLocalCdn } from './helpers/offline-cdn';

// scripts/e2e-fresh.sh --sandbox-env GROUNDS_SANDBOX tests/hq-grounds.spec.ts -- --workers=1
const sandbox = process.env.GROUNDS_SANDBOX;
test.skip(!sandbox, 'requires a fresh isolated demo server');
test.use({ viewport: { width: 1280, height: 900 }, hasTouch: true });

async function ownedSandbox() {
  const home = await realpath(sandbox!);
  expect(basename(home)).toMatch(/^ori-e2e\./);
  expect(await readFile(join(home, '.ori-demo-owner'), 'utf8')).toBe(
    `worktree=${await realpath(process.cwd())}\n`
  );
  return home;
}

test('HQ grounds: review beside the depot, remember the map, file only on request', async ({
  page,
  request
}, info) => {
  test.setTimeout(90_000);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  const home = await ownedSandbox();
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
  const loose = 'atlas-' + 'quarterly-inventory-'.repeat(6) + 'index.csv';
  const files = ['brief.pdf', loose, 'map.png'];
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
  const fileRequests: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && /\/batches\/[^/]+\/file$/.test(new URL(req.url()).pathname))
      fileRequests.push(req.url());
  });
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
  const mapContext = () =>
    depot.evaluate(el => {
      const { x, y } = el.getBoundingClientRect();
      return { x, y, position: el.getAttribute('style'), scrollX, scrollY };
    });
  const atDepot = await mapContext();
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
  expect(await depot.evaluate(el => !!el.closest('[inert]'))).toBe(true);
  await expect(page.getByRole('dialog')).toHaveCount(1);
  await page.mouse.move(depotBox.x + depotBox.width / 2, depotBox.y + depotBox.height / 2);
  await page.mouse.wheel(0, 480);
  await expect(review.locator('.fj-console-folder')).toHaveText(basename(root));
  await page.screenshot({ path: info.outputPath('depot-panel.png') });
  await review.getByRole('button', { name: 'All files', exact: true }).click();
  const leaveLoose = review.getByRole('checkbox', { name: `Select ${loose}`, exact: true });
  await leaveLoose.uncheck();
  const fileTwo = review.getByRole('button', { name: 'File 2 files', exact: true });
  await expect(fileTwo).toBeEnabled();
  await page.keyboard.press('Escape');
  await expect(review).toBeHidden();
  await expect(depot).toBeFocused();
  expect(await mapContext()).toEqual(atDepot);
  expect((await readdir(root)).sort()).toEqual(before);
  await depot.press('Enter');
  await expect(fileTwo).toBeEnabled();
  await page.goBack();
  await expect(review).toBeHidden();
  await expect(depot).toBeFocused();
  await page.goForward();
  await expect(review.locator('.fj-depot-bar')).toBeVisible();
  await review.getByRole('tab', { name: 'Settings' }).click();
  await review.getByRole('button', { name: 'Stop using this folder' }).click();
  await expect(review).toContainText('again to confirm');
  await page.keyboard.press('Escape');
  await expect(review).toBeVisible();
  // A deliberate exit cancels the first press; it never revokes the folder.
  await review.getByRole('button', { name: 'Back to map' }).click();
  await expect(depot).toBeFocused();
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
  await expect(fileTwo).toBeEnabled();
  const sheet = (await review.boundingBox())!;
  expect(sheet.y).toBeGreaterThan(0);
  expect(sheet.y + sheet.height).toBeCloseTo(844, 0);
  if (await review.getByRole('button', { name: 'All files', exact: true }).isVisible())
    await review.getByRole('button', { name: 'All files', exact: true }).tap();
  const row = review.getByRole('row').filter({ hasText: loose });
  await row.scrollIntoViewIfNeeded();
  await expect(row).toHaveCSS('display', 'grid');
  await expect(leaveLoose).not.toBeChecked();
  await page.screenshot({ path: info.outputPath('depot-sheet.png') });
  // 390 x 844 at 200% zoom exposes half as many CSS pixels on both axes.
  await page.setViewportSize({ width: 195, height: 422 });
  await leaveLoose.scrollIntoViewIfNeeded();
  await expect(leaveLoose).toBeInViewport();
  await expect(review.getByRole('button', { name: 'Back to map' })).toBeInViewport();
  await fileTwo.scrollIntoViewIfNeeded();
  await expect(fileTwo).toBeInViewport();
  const scroller = review.locator('.fj-depot-content');
  expect(await scroller.evaluate(el => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(1);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(leaveLoose).not.toBeChecked();
  await expect(fileTwo).toBeEnabled();
  await review.getByRole('button', { name: 'Back to map' }).tap();
  await expect(review).toBeHidden();
  expect((await readdir(root)).sort()).toEqual(before);

  expect(fileRequests).toEqual([]);
  await depot.tap();
  await expect(fileTwo).toBeEnabled();
  // Hold the request, not its result: the real server files the two files
  // only when the one explicit File press is released below.
  let release!: () => void;
  const held = new Promise<void>(resolve => {
    release = resolve;
  });
  await page.route('**/batches/*/file', async route => {
    await held;
    await route.continue();
  });
  await fileTwo.tap();
  try {
    await expect.poll(() => fileRequests.length).toBe(1);
    await page.keyboard.press('Escape');
    await expect(review).toBeVisible();
    await review.getByRole('button', { name: 'Back to map' }).tap();
    await expect(review).toBeVisible();
    await page.goBack();
    await expect(review).toBeVisible();
    await expect(page).toHaveURL(/panel=file-janitor/);
  } finally {
    release();
  }
  const receipt = review.locator('#fileJanitorReceipt');
  await expect(receipt).toContainText('2 files filed.', { timeout: 60000 });
  await expect(receipt).toContainText('1 file stays loose');
  await expect(receipt).toContainText('Filed/Documents');
  await expect(receipt).toContainText('Filed/Images');
  await receipt.getByText('Filed files', { exact: true }).tap();
  await expect(receipt.locator('.fj-receipt-files li')).toHaveText(['brief.pdf', 'map.png']);
  await page.screenshot({ path: info.outputPath('depot-receipt.png') });
  expect(fileRequests).toHaveLength(1);
  expect((await readdir(root)).sort()).toEqual(['Filed', loose].sort());
  for (const [folder, name] of [
    ['Documents', 'brief.pdf'],
    ['Images', 'map.png']
  ]) {
    expect(await readdir(join(root, 'Filed', folder))).toEqual([name]);
    expect(await readFile(join(root, 'Filed', folder, name), 'utf8')).toBe('Example: ' + name);
  }
  expect(await readFile(join(root, loose), 'utf8')).toBe('Example: ' + loose);

  await review.getByRole('button', { name: 'Back to map' }).tap();
  await expect(depot).toBeFocused();
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.locator('[data-cmd-view-mode="details"]').click();
  const overview = page
    .getByRole('region', { name: 'Stations', exact: true })
    .getByRole('button', { name: /Filing/ });
  await overview.click();
  await expect(review.getByRole('button', { name: 'Close Filing' })).toBeVisible();
  await expect(review.locator('.fj-depot-bar')).toHaveCount(0);
  await page.screenshot({ path: info.outputPath('overview-console.png') });
  await review.getByRole('button', { name: 'Close Filing' }).click();
  await page.goto(path + '?mode=map&panel=file-janitor&tab=review');
  await expect(review.getByRole('tab', { name: 'Review' })).toHaveAttribute(
    'aria-selected',
    'true'
  );
  await expect(review.locator('.fj-depot-bar')).toHaveCount(0);
  await page.screenshot({ path: info.outputPath('linked-review.png') });
  expect(fileRequests).toHaveLength(1);

  const ordinary = (await (await post('/api/workspaces', { name: 'Archive only' })).json()).folder;
  await page.goto(`/workspaces/${ordinary.folder_slug}?mode=map`);
  await expect(page.locator('[data-cmd-hq-station="memory"]')).toBeVisible();
  await expect(page.locator('[data-station-visual="depot"], .ws-cmd-grounds')).toHaveCount(0);
});

test('a long depot review keeps the last page and way out reachable at high zoom', async ({
  page,
  request
}, info) => {
  const root = await mkdtemp(join(await ownedSandbox(), 'sheet-inbox-'));
  const names = Array.from({ length: 55 }, (_, i) => `study-${String(i).padStart(2, '0')}.png`);
  const old = new Date(Date.now() - 3600000);
  for (const name of names) {
    await writeFile(join(root, name), name);
    await utimes(join(root, name), old, old);
  }
  expect((await request.post('/api/onboarding/skip', { data: {} })).ok()).toBeTruthy();
  const ws = await filingWorkspace(request, 'Reflow', root);
  await installLocalCdn(page);
  const nudge = page.locator('#workspace-detail-task-confirm-modal');
  await page.addLocatorHandler(nudge, () =>
    nudge.getByRole('button', { name: 'Not now', exact: true }).click()
  );
  await page.goto(`/workspaces/${ws.slug}?mode=map`);
  const depot = page.locator('.ws-cmd-map-hq-station[data-cmd-hq-station="file-janitor"]');
  await depot.click();
  const review = page.getByRole('dialog', { name: 'Filing', exact: true });
  await review.getByRole('button', { name: 'All files', exact: true }).click();
  await expect(review.getByRole('button', { name: 'File 55 files', exact: true })).toBeEnabled();
  await page.setViewportSize({ width: 195, height: 422 });
  await review.getByRole('button', { name: 'Next', exact: true }).tap();
  const last = review.getByRole('checkbox', { name: 'Select study-54.png' });
  await last.uncheck();
  const file = review.getByRole('button', { name: 'File 54 files', exact: true });
  await file.scrollIntoViewIfNeeded();
  await expect(file).toBeInViewport();
  await expect(review.getByRole('button', { name: 'Back to map' })).toBeInViewport();
  await page.screenshot({ path: info.outputPath('long-review-zoom.png') });
  await review.getByRole('button', { name: 'Back to map' }).tap();
  await expect(depot).toBeFocused();
  expect((await readdir(root)).filter(name => name.endsWith('.png')).sort()).toEqual(names);
});

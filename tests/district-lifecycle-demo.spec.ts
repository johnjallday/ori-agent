import { test, expect, Page, Locator } from '@playwright/test';

/**
 * Issue #346 group-7 demo: the presentation lifecycle.
 *
 * Customizes geometry, collapse, and appearance; runs Reset layout and checks
 * the colours survive while the arrangement goes; undoes it and checks the
 * geometry comes back exactly; then forces a save failure and takes the retry.
 *
 * Not part of CI, and it needs a FRESH demo sandbox — one run per server:
 *   ./scripts/demo-346.sh 8959 --fresh
 *   ./scripts/e2e.sh --port 8959 tests/district-lifecycle-demo.spec.ts
 */

async function skipOnboarding(page: Page) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
    })
  );
}

async function seedFixture(page: Page, tag: string) {
  const make = async (name: string, kind = 'workspace') => {
    const res = await page.request.post('/api/workspaces', { data: { name, kind } });
    return (await res.json()).folder.id;
  };
  const sized = await make(`Life Sized ${tag}`, 'group');
  const shut = await make(`Life Shut ${tag}`, 'group');
  const a = await make(`Life A ${tag}`);
  const b = await make(`Life B ${tag}`);
  await page.request.put(`/api/workspaces/${a}`, { data: { parent_id: sized } });
  await page.request.put(`/api/workspaces/${b}`, { data: { parent_id: shut } });
  await page.request.patch('/api/workspace-map/layout', {
    data: {
      operations: [
        { op: 'set_positions', positions: { [a]: { x: 380, y: 380 }, [b]: { x: 1100, y: 380 } } },
        {
          op: 'set_group_frame',
          group_id: sized,
          frame: { x: 340, y: 340, width: 460, height: 260 }
        },
        { op: 'set_group_appearance', group_id: sized, accent: 'moss', theme: 'blueprint' },
        { op: 'set_group_collapsed', group_id: shut, collapsed: true },
        { op: 'set_group_appearance', group_id: shut, accent: 'tide' }
      ]
    }
  });
  return { sized, shut, a, b };
}

async function layoutOf(page: Page) {
  return (await (await page.request.get('/api/workspace-map/layout')).json()).layout;
}

async function frameAndSelect(page: Page, district: Locator) {
  const canvas = page.locator('[data-ws-map-viewport]');
  await canvas.click({ button: 'right', position: { x: 40, y: 40 } });
  await page.getByRole('menuitem', { name: 'Fit all' }).click();
  await page.waitForTimeout(300);
  await expect(async () => {
    await district.locator('.ws-map-district-tag').click({ force: true });
    await expect(page.locator('[data-rail-map-layout]')).toBeVisible({ timeout: 1500 });
  }).toPass({ timeout: 20000 });
}

test('#346 Reset layout clears the arrangement, keeps the colours, and Undo restores geometry exactly', async ({
  page
}) => {
  await skipOnboarding(page);
  const tag = String(Date.now()).slice(-5);
  const existing = await (await page.request.get('/api/workspaces')).json();
  const stale = (existing.folders || []).filter((f: { name?: string }) =>
    String(f.name || '').startsWith('Life A ')
  ).length;
  expect(stale, 'this demo needs a fresh sandbox: ./scripts/demo-346.sh <port> --fresh').toBe(0);
  const { sized, shut, a } = await seedFixture(page, tag);

  // Reset layout asks for confirmation; answer yes.
  await page.addInitScript(() => {
    window.confirm = () => true;
  });
  await page.goto('/');
  const sizedDistrict = page.locator(`.ws-map-district[data-group-id="${sized}"]`);
  await sizedDistrict.waitFor({ timeout: 15000 });

  const before = await layoutOf(page);
  expect(before.groups[sized].sizing_mode).toBe('custom');
  expect(before.groups[shut].collapsed).toBe(true);
  await expect(sizedDistrict).toHaveClass(/ws-map-accent-moss/);

  // FR-186: Reset clears geometry and collapse, and keeps every chosen preset.
  await page.locator('[data-map-reset-layout]').click();
  await page.waitForTimeout(800);

  const reset = await layoutOf(page);
  expect(Object.keys(reset.positions)).toHaveLength(0);
  expect(reset.groups[sized]?.sizing_mode ?? 'auto').toBe('auto');
  expect(reset.groups[sized]?.frame ?? null).toBeNull();
  expect(reset.groups[shut]?.collapsed ?? false).toBe(false);
  expect(reset.groups[sized].accent).toBe('moss', 'a colour is not an arrangement');
  expect(reset.groups[sized].theme).toBe('blueprint');
  expect(reset.groups[shut].accent).toBe('tide');
  expect(reset.snap_to_grid).toBe(before.snap_to_grid, 'the snap preference is preserved');

  await page.screenshot({ path: 'test-results/346-lifecycle-reset.png' });

  // FR-187: Undo puts the whole geometry back, atomically.
  await page.locator('[data-map-undo-reset]').click();
  await page.waitForTimeout(800);

  const undone = await layoutOf(page);
  expect(undone.positions[a]).toEqual(before.positions[a]);
  expect(undone.groups[sized].sizing_mode).toBe('custom');
  expect(undone.groups[sized].frame).toEqual(before.groups[sized].frame);
  expect(undone.groups[shut].collapsed).toBe(true);
  // And appearance was never part of the round trip.
  expect(undone.groups[sized].accent).toBe('moss');
  expect(undone.groups[shut].accent).toBe('tide');

  // The restored layout is actually ON SCREEN. Reset framed the automatic
  // arrangement on its way out, so an Undo that only fixed the data would leave
  // the user looking at empty world and wondering whether it worked.
  const canvasBox = (await page.locator('[data-ws-map-viewport]').boundingBox())!;
  const restoredBox = (await sizedDistrict.boundingBox())!;
  expect(restoredBox.y).toBeGreaterThanOrEqual(canvasBox.y - 1);
  expect(restoredBox.y + restoredBox.height).toBeLessThanOrEqual(
    canvasBox.y + canvasBox.height + 1
  );

  await page.screenshot({ path: 'test-results/346-lifecycle-undone.png' });

  // FR-66/FR-188: a save failure restores the committed state, says so, and the
  // retry genuinely re-sends the same intent.
  await frameAndSelect(page, sizedDistrict);
  const committed = await layoutOf(page);

  let failNext = true;
  await page.route('**/api/workspace-map/layout', async route => {
    if (route.request().method() === 'PATCH' && failNext) {
      failNext = false;
      await route.fulfill({ status: 500, body: '{}' });
      return;
    }
    await route.continue();
  });

  await page.locator('[data-cockpit-group-collapse]').click();
  await page.waitForTimeout(700);
  const afterFailure = await layoutOf(page);
  expect(afterFailure.groups[sized].collapsed).toBe(
    committed.groups[sized].collapsed,
    'a failed save leaves the committed state alone'
  );
  expect(
    await page.evaluate(() => (window as any).OriWorkspaceMap.districtActions.hasRetry())
  ).toBe(true);

  await page.evaluate(() => (window as any).OriWorkspaceMap.districtActions.retryLastFailure());
  await page.waitForTimeout(700);
  const afterRetry = await layoutOf(page);
  expect(afterRetry.groups[sized].collapsed).toBe(true, 'the retry re-sent the same intent');

  await page.screenshot({ path: 'test-results/346-lifecycle-retry.png' });
});

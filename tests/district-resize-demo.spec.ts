import { test, expect, Page, Locator } from '@playwright/test';

/**
 * Issue #346 group-3 demo: direct resizing of a selected district.
 *
 * Covers what the DOM harness cannot — real pointer geometry against a
 * screen-space overlay whose handles must stay finger-sized while the world
 * scales underneath them.
 *
 * Not part of CI, and it needs a FRESH demo sandbox — one run per server. It
 * drives the camera with Fit all, which frames everything on the map, so
 * fixtures left by an earlier run push the zoom to its 10% floor and shrink the
 * district until its eight handles overlap each other. Run it as:
 *   ./scripts/demo-346.sh 8949 --fresh
 *   ./scripts/e2e.sh --port 8949 tests/district-resize-demo.spec.ts
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

/** A group of two members plus an unrelated workspace to collide with. */
async function seedResizeFixture(page: Page, tag: string) {
  const make = async (name: string, kind = 'workspace') => {
    const res = await page.request.post('/api/workspaces', { data: { name, kind } });
    return (await res.json()).folder.id;
  };
  const group = await make(`Resize Group ${tag}`, 'group');
  const a = await make(`Resize A ${tag}`);
  const b = await make(`Resize B ${tag}`);
  const outsider = await make(`Resize Outsider ${tag}`);
  for (const id of [a, b]) {
    await page.request.put(`/api/workspaces/${id}`, { data: { parent_id: group } });
  }
  // Near the world origin, so framing leaves the district comfortably in the
  // middle of the canvas with room to drag its edges in every direction.
  const y = 380;
  await page.request.patch('/api/workspace-map/layout', {
    data: {
      operations: [
        {
          op: 'set_positions',
          positions: {
            [a]: { x: 380, y },
            [b]: { x: 608, y },
            // Two cells clear to the east: close enough to collide with a
            // deliberate over-drag, far enough not to conflict by default.
            [outsider]: { x: 1216, y }
          }
        }
      ]
    }
  });
  return { group, a, b, outsider, y };
}

async function frameOf(district: Locator) {
  return district.evaluate(el => ({
    x: parseFloat((el as HTMLElement).style.left),
    y: parseFloat((el as HTMLElement).style.top),
    width: parseFloat((el as HTMLElement).style.width),
    height: parseFloat((el as HTMLElement).style.height)
  }));
}

/**
 * Drag a resize handle by a delta expressed in WORLD units.
 *
 * The screen delta is derived from the live camera zoom, so the demo does not
 * depend on the map happening to sit at 100% — which it cannot be relied on to
 * do once a sandbox holds more than one fixture.
 */
async function dragHandle(
  page: Page,
  district: Locator,
  edge: string,
  dx: number,
  dy: number,
  release = true
) {
  await ensureSelected(page, district);
  const zoom = await page.evaluate(() => (window as any).OriWorkspaceMap.getCamera().zoom);
  const handle = page.locator(`[data-resize-handle="${edge}"]`);
  const box = (await handle.boundingBox())!;
  const cx = box.x + box.width / 2;
  const cy = box.y + box.height / 2;
  await page.mouse.move(cx, cy);
  await page.mouse.down();
  await page.mouse.move(cx + dx * zoom, cy + dy * zoom, { steps: 6 });
  if (release) await page.mouse.up();
  return { cx, cy };
}

/**
 * Make sure the district is the current selection, so its handles exist.
 *
 * The Home cockpit re-mounts the Map while it finishes hydrating and after a
 * background refresh, and a click that lands in that window is replaced along
 * with the DOM it hit. Selection itself is stable once it takes — this only
 * guards the seam between the two.
 */
async function ensureSelected(page: Page, district: Locator) {
  await expect(async () => {
    if (await page.locator('[data-ws-map-resize]').isHidden()) {
      await district.locator('.ws-map-district-tag').click({ force: true });
    }
    await expect(page.locator('[data-ws-map-resize]')).toBeVisible({ timeout: 1500 });
  }).toPass({ timeout: 20000 });
}

/**
 * Bring the fixture on screen and make it the selection.
 */
async function frameAndSelect(page: Page, district: Locator) {
  const canvas = page.locator('[data-ws-map-viewport]');
  // Fit all first: the district may be anywhere in a sandbox that holds other
  // fixtures, and it has to be on screen before it can be clicked.
  await canvas.click({ button: 'right', position: { x: 40, y: 40 } });
  await page.getByRole('menuitem', { name: 'Fit all' }).click();
  await page.waitForTimeout(300);

  await ensureSelected(page, district);

  // Then centre it, which keeps the zoom the user is on (#346 FR-47).
  await canvas.click({ button: 'right', position: { x: 40, y: 40 } });
  await page.getByRole('menuitem', { name: 'Center selected' }).click();
  await page.waitForTimeout(300);
  await expect(page.locator('[data-ws-map-resize]')).toBeVisible();
}

test('#346 a selected district resizes by pointer, blocks false containment, and fits back', async ({
  page
}) => {
  await skipOnboarding(page);
  const tag = String(Date.now()).slice(-5);
  const existing = await (await page.request.get('/api/workspaces')).json();
  const stale = (existing.folders || []).filter((f: { name?: string }) =>
    String(f.name || '').startsWith('Resize A ')
  ).length;
  expect(stale, 'this demo needs a fresh sandbox: ./scripts/demo-346.sh <port> --fresh').toBe(0);
  const { group, outsider } = await seedResizeFixture(page, tag);

  await page.goto('/');
  const district = page.locator(`.ws-map-district[data-group-id="${group}"]`);
  await district.waitFor({ timeout: 15000 });

  await frameAndSelect(page, district);

  // FR-52: handles appear only once the district is selected.
  const overlay = page.locator('[data-ws-map-resize]');
  await expect(overlay).toBeVisible();

  // FR-56: every handle keeps a 44×44 CSS-pixel target.
  for (const edge of ['n', 'ne', 'e', 'se', 's', 'sw', 'w', 'nw']) {
    const box = (await page.locator(`[data-resize-handle="${edge}"]`).boundingBox())!;
    expect(box.width, `${edge} width`).toBeGreaterThanOrEqual(44);
    expect(box.height, `${edge} height`).toBeGreaterThanOrEqual(44);
  }

  const before = await frameOf(district);

  // FR-58/FR-63: drag the east edge out; one saved update, west edge pinned.
  await dragHandle(page, district, 'e', 160, 0);
  await page.waitForTimeout(400);
  const widened = await frameOf(district);
  expect(widened.width).toBeGreaterThan(before.width);
  expect(widened.x).toBe(before.x);
  expect(widened.height).toBe(before.height);

  const saved = await (await page.request.get('/api/workspace-map/layout')).json();
  expect(saved.layout.groups[group].sizing_mode).toBe('custom');
  expect(saved.layout.groups[group].frame.width).toBeCloseTo(widened.width, 1);

  // FR-152: the rail states the new mode in words, without needing a re-select.
  await expect(page.locator('[data-rail-sizing-mode]')).toHaveText(/Custom size/);
  await expect(page.locator('[data-cockpit-group-fit]')).toBeEnabled();

  await page.screenshot({ path: 'test-results/346-resize-widened.png' });

  // FR-65: a cancelled gesture writes nothing and restores the frame.
  const revisionBefore = saved.layout.revision;
  await dragHandle(page, district, 's', 0, 200, false);
  await page.keyboard.press('Escape');
  await page.mouse.up();
  await page.waitForTimeout(300);
  const afterCancel = await frameOf(district);
  expect(afterCancel.height).toBe(widened.height);
  const afterCancelLayout = await (await page.request.get('/api/workspace-map/layout')).json();
  expect(afterCancelLayout.layout.revision).toBe(revisionBefore);

  // FR-78/FR-82: dragging east across the unrelated workspace is refused.
  await dragHandle(page, district, 'e', 700, 0, false);
  const box = page.locator('[data-ws-map-resize-box]');
  await expect(box).toHaveClass(/is-blocked/);
  await expect(page.locator('[data-ws-map-resize-readout]')).toContainText('blocked by');
  await page.screenshot({ path: 'test-results/346-resize-blocked.png' });
  await page.mouse.up();
  await page.waitForTimeout(300);

  const afterBlocked = await (await page.request.get('/api/workspace-map/layout')).json();
  expect(afterBlocked.layout.revision).toBe(revisionBefore, 'a blocked release saved nothing');
  expect((await frameOf(district)).width).toBe(widened.width);
  // The outsider never moved.
  expect(afterBlocked.layout.positions[outsider]).toEqual(saved.layout.positions[outsider]);

  // FR-50: a reload keeps the custom frame.
  await page.reload();
  await district.waitFor({ timeout: 15000 });
  const reloaded = await frameOf(district);
  expect(reloaded.width).toBeCloseTo(widened.width, 1);

  // FR-40: Fit to contents from the OTHER surface — the Home rail — returns it
  // to automatic sizing without moving a workspace.
  await frameAndSelect(page, district);
  const fit = page.locator('[data-cockpit-group-fit]');
  await expect(fit).toBeEnabled();
  await expect(page.locator('[data-rail-sizing-mode]')).toHaveText(/Custom size/);
  await fit.click();
  await page.waitForTimeout(600);

  const fitted = await (await page.request.get('/api/workspace-map/layout')).json();
  expect(fitted.layout.groups[group]).toBeUndefined();
  expect(fitted.layout.positions[outsider]).toEqual(saved.layout.positions[outsider]);
  expect((await frameOf(district)).width).toBeCloseTo(before.width, 1);

  await page.screenshot({ path: 'test-results/346-resize-fitted.png' });
});

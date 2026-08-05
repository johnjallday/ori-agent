import { test, expect, Page } from '@playwright/test';

/**
 * Browser suite for the coordinate-based Workspace Map
 * (tasks/prd-292-coordinate-based-map.md).
 *
 * Not part of CI — run against an isolated demo server:
 *   wt demo 8931
 *   ./scripts/e2e.sh tests/workspace-map-coordinate.spec.ts
 *
 * These cover what unit tests cannot: that the saved layout, the camera, the
 * placement mode, and the existing Create Workspace modal are actually wired to
 * each other in a real page — and that navigating the map never moves anything
 * on it.
 */

const LAYOUT_API = '**/api/workspace-map/layout';

async function skipOnboarding(page: Page) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
    })
  );
}

/** A concrete (non-group) workspace so the map has a building to draw. */
async function ensureWorkspace(page: Page, name?: string): Promise<string> {
  const list = await (await page.request.get('/api/workspaces')).json();
  const rows = list.workspaces || list.folders || [];
  const existing = rows.find(
    (ws: { id?: string; kind?: string }) =>
      ws?.id && String(ws.kind || '').toLowerCase() !== 'group'
  );
  if (existing?.id && !name) return existing.id;
  const res = await page.request.post('/api/workspaces', {
    data: { name: name || `Map spec ${Date.now()}` }
  });
  return (await res.json())?.folder?.id;
}

async function listWorkspaces(page: Page): Promise<Array<{ id: string; name?: string }>> {
  const body = await (await page.request.get('/api/workspaces')).json();
  return body.workspaces || body.folders || [];
}

/**
 * Drive Ori's existing Create Workspace wizard to completion.
 *
 * Build mode reuses this flow rather than replacing it (FR-51), so the spec
 * drives the real four steps — Blueprint → Details → Team → Review — exactly as
 * tests/create-workspace-behavior.spec.ts does.
 */
async function completeCreateWizard(page: Page, name: string) {
  await page.locator('.workspace-template-card', { hasText: 'Blank' }).first().click();
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await page.locator('#folderNameInput').fill(name);
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep4')).toBeVisible();
  await page.locator('#createFolderBtn').click();
}

async function savePosition(page: Page, id: string, x: number, y: number) {
  const response = await page.request.patch('/api/workspace-map/layout', {
    data: { operations: [{ op: 'set_positions', positions: { [id]: { x, y } } }] }
  });
  expect(response.ok(), `saving a position should succeed: ${await response.text()}`).toBe(true);
}

/** Every building's world anchor, read straight out of the rendered DOM. */
async function anchors(page: Page) {
  return page.evaluate(() =>
    [...document.querySelectorAll('.ws-map-tile[data-ws-id]')]
      .map(el => ({
        id: el.getAttribute('data-ws-id'),
        x: Math.round(parseFloat((el as HTMLElement).style.left || '0')),
        y: Math.round(parseFloat((el as HTMLElement).style.top || '0'))
      }))
      .sort((a, b) => (a.id! < b.id! ? -1 : 1))
  );
}

const cameraOf = (page: Page) =>
  page.evaluate(() => (window as unknown as { OriWorkspaceMap: any }).OriWorkspaceMap.getCamera());

/**
 * A viewport point that is genuinely empty world space.
 *
 * Picking a corner by hand is how a Build-mode test ends up clicking the camera
 * controls and then reporting that placement is broken. This asks the page what
 * is actually under each candidate and returns the first that is the canvas or
 * the world layer itself.
 */
async function emptyPointOn(page: Page): Promise<{ x: number; y: number }> {
  const box = (await page.locator('.ws-map-canvas').boundingBox())!;
  const candidates = [
    [0.5, 0.25],
    [0.75, 0.3],
    [0.3, 0.2],
    [0.85, 0.6],
    [0.15, 0.45],
    [0.6, 0.5]
  ];
  for (const [fx, fy] of candidates) {
    const point = { x: box.x + box.width * fx, y: box.y + box.height * fy };
    const isEmpty = await page.evaluate(
      ([x, y]) => {
        const el = document.elementFromPoint(x, y);
        return (
          !!el && (el.classList.contains('ws-map-canvas') || el.classList.contains('ws-map-world'))
        );
      },
      [point.x, point.y]
    );
    if (isEmpty) return point;
  }
  throw new Error('no empty spot found on the map');
}

async function openMap(page: Page) {
  await page.goto('/');
  await page.locator('.ws-map-world .ws-map-tile[data-ws-id]').first().waitFor();
  await page.waitForTimeout(300);
}

test.describe('Coordinate Workspace Map', () => {
  // One layout per user is the whole point of the feature, so these tests share
  // a single server-side record. Running them in parallel means one test's save
  // lands between another's save and its assertion — the failure looks like a
  // product bug and is not one.
  test.describe.configure({ mode: 'serial' });

  test.beforeEach(async ({ page }) => {
    await skipOnboarding(page);
  });

  test('a saved coordinate is where the building is, after a reload (FR-17, metric 1)', async ({
    page
  }) => {
    const id = await ensureWorkspace(page);
    await savePosition(page, id, 494, 342);
    await openMap(page);

    const tile = page.locator(`.ws-map-tile[data-ws-id="${id}"]`);
    await expect(tile).toHaveAttribute('style', /left:494px;top:342px/);

    await page.reload();
    await page.locator(`.ws-map-tile[data-ws-id="${id}"]`).waitFor();
    await expect(page.locator(`.ws-map-tile[data-ws-id="${id}"]`)).toHaveAttribute(
      'style',
      /left:494px;top:342px/
    );
  });

  test('resizing, filtering, and switching Map/Tree move nothing (FR-13, metric 2)', async ({
    page
  }) => {
    const id = await ensureWorkspace(page);
    await savePosition(page, id, 380, 228);
    await openMap(page);
    const before = await anchors(page);

    await page.setViewportSize({ width: 420, height: 860 });
    await page.waitForTimeout(300);
    expect(await anchors(page)).toEqual(before);

    await page.setViewportSize({ width: 1440, height: 900 });
    await page.locator('[data-cockpit-view="tree"]').click();
    await page.locator('[data-cockpit-view="map"]').click();
    await page.locator('.ws-map-world .ws-map-tile[data-ws-id]').first().waitFor();
    await page.waitForTimeout(300);
    expect(await anchors(page)).toEqual(before);
  });

  test('pan and zoom navigate without moving a building (FR-31 – FR-42)', async ({ page }) => {
    await ensureWorkspace(page);
    await openMap(page);
    const before = await anchors(page);
    const box = (await page.locator('.ws-map-canvas').boundingBox())!;

    await page.mouse.move(box.x + box.width - 40, box.y + box.height - 40);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width - 240, box.y + box.height - 140, { steps: 10 });
    await page.mouse.up();
    const panned = await cameraOf(page);

    await page.click('[data-map-zoom-in]');
    expect((await cameraOf(page)).zoom).toBeGreaterThan(panned.zoom);

    await page.click('[data-map-fit]');
    await page.click('[data-map-reset-view]');
    expect((await cameraOf(page)).zoom).toBe(1);

    expect(await anchors(page)).toEqual(before);
  });

  test('the camera persists for the next visit (FR-44)', async ({ page }) => {
    await ensureWorkspace(page);
    await openMap(page);
    await page.click('[data-map-zoom-in]');
    await page.waitForTimeout(900); // past the debounce

    const saved = await cameraOf(page);
    await page.reload();
    await page.locator('.ws-map-world .ws-map-tile[data-ws-id]').first().waitFor();
    await page.waitForTimeout(400);
    const restored = await cameraOf(page);
    expect(Math.round(restored.zoom * 100)).toBe(Math.round(saved.zoom * 100));
  });

  test('Build mode places a workspace at the chosen point (FR-47 – FR-53, metric 3)', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await openMap(page);

    await page.click('[data-map-build]');
    await expect(page.locator('[data-map-build-banner]')).toBeVisible();

    const site = await emptyPointOn(page);
    await page.mouse.click(site.x, site.y);

    // The existing Create Workspace modal opens — the same four-step wizard,
    // not a second form (FR-51).
    await expect(page.locator('#addFolderModal')).toBeVisible();
    await expect(page.locator('[data-map-build-banner]')).toBeHidden();

    const name = `Built ${Date.now()}`;
    await completeCreateWizard(page, name);

    // FR-53: the flow returns to the map rather than navigating into the new
    // workspace, and the coordinate the user chose is what was saved.
    await page.waitForTimeout(2000);
    expect(new URL(page.url()).pathname).toBe('/');

    const rows = await listWorkspaces(page);
    const built = rows.find((ws: { name?: string }) => ws?.name === name);
    expect(built, 'the workspace was created').toBeTruthy();

    const layout = await (await page.request.get('/api/workspace-map/layout')).json();
    expect(layout.layout.positions[built!.id], 'its chosen coordinate was saved').toBeTruthy();
  });

  test('cancelling Build mode creates nothing (FR-54)', async ({ page }) => {
    await ensureWorkspace(page);
    await openMap(page);
    // Counted through the API rather than the DOM: what must not change is the
    // set of workspaces, not how many tiles happen to be painted.
    const before = new Set((await listWorkspaces(page)).map(ws => ws.id));
    const positionsBefore = Object.keys(
      (await (await page.request.get('/api/workspace-map/layout')).json()).layout.positions
    ).length;

    await page.click('[data-map-build]');
    await page.locator('.ws-map-canvas').press('Escape');
    await expect(page.locator('[data-map-build-banner]')).toBeHidden();
    await expect(page.locator('#addFolderModal')).toBeHidden();

    // And cancelling from inside the modal leaves nothing behind either.
    await page.click('[data-map-build]');
    const site = await emptyPointOn(page);
    await page.mouse.click(site.x, site.y);
    await expect(page.locator('#addFolderModal')).toBeVisible();
    // Let the show transition finish: hiding a Bootstrap modal mid-fade is a
    // no-op, which looks exactly like "cancel is broken".
    await page.waitForTimeout(500);
    // Dismiss the same way the rest of the create-flow suite does — the modal's
    // own Bootstrap instance — so this tests the cancel contract rather than
    // Bootstrap's click handling.
    await page.evaluate(() => {
      const el = document.getElementById('addFolderModal');
      // @ts-expect-error bootstrap is a page global
      window.bootstrap.Modal.getInstance(el)?.hide();
    });
    await expect(page.locator('#addFolderModal')).toBeHidden();
    await page.waitForTimeout(600);

    const after = new Set((await listWorkspaces(page)).map(ws => ws.id));
    expect([...after].filter(id => !before.has(id))).toEqual([]);
    const positionsAfter = Object.keys(
      (await (await page.request.get('/api/workspace-map/layout')).json()).layout.positions
    ).length;
    expect(positionsAfter, 'no stray position record').toBe(positionsBefore);
  });

  test('dragging a building saves once and survives a reload (FR-63 – FR-70)', async ({ page }) => {
    const id = await ensureWorkspace(page);
    await savePosition(page, id, 342, 190);
    await openMap(page);
    await page.click('[data-map-reset-view]');

    const writes: string[] = [];
    page.on('request', request => {
      if (request.url().includes('/api/workspace-map/layout') && request.method() === 'PATCH') {
        writes.push(request.postData() || '');
      }
    });

    const tile = page.locator(`.ws-map-tile[data-ws-id="${id}"]`);
    const box = (await tile.boundingBox())!;
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.mouse.down();
    // Several move events, one drop.
    await page.mouse.move(box.x + box.width / 2 + 140, box.y + box.height / 2 + 90, { steps: 15 });
    const duringDrag = writes.filter(body => body.includes('set_positions')).length;
    expect(duringDrag, 'pointer movement performs zero writes (metric 7)').toBe(0);
    await page.mouse.up();
    await page.waitForTimeout(800);

    expect(
      writes.filter(body => body.includes('set_positions')).length,
      'one completed drop, at most one update'
    ).toBe(1);

    const moved = await anchors(page);
    const movedAnchor = moved.find(a => a.id === id)!;
    expect(movedAnchor.x !== 342 || movedAnchor.y !== 190, 'the building actually moved').toBe(
      true
    );

    await page.reload();
    await page.locator(`.ws-map-tile[data-ws-id="${id}"]`).waitFor();
    await page.waitForTimeout(300);
    const afterReload = (await anchors(page)).find(a => a.id === id)!;
    expect(afterReload).toEqual(movedAnchor);
  });

  test('a failed move visibly returns the building to where it was (FR-71, metric 6)', async ({
    page
  }) => {
    const id = await ensureWorkspace(page);
    await savePosition(page, id, 266, 152);
    await openMap(page);
    await page.click('[data-map-reset-view]');

    await page.route(LAYOUT_API, async route => {
      if (route.request().method() === 'PATCH') {
        await route.fulfill({ status: 500, contentType: 'application/json', body: '{}' });
        return;
      }
      await route.fallback();
    });

    const tile = page.locator(`.ws-map-tile[data-ws-id="${id}"]`);
    const box = (await tile.boundingBox())!;
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width / 2 + 120, box.y + box.height / 2 + 60, { steps: 10 });
    await page.mouse.up();
    await page.waitForTimeout(800);

    // The browser re-serializes an inline style set from JS with spaces, so the
    // pattern has to tolerate both spellings.
    await expect(tile).toHaveAttribute('style', /left:\s*266px;\s*top:\s*152px/);
    await expect(tile).toHaveClass(/is-unsaved/);
  });

  test('a selected building can be moved by keyboard alone (FR-77 – FR-79)', async ({ page }) => {
    const id = await ensureWorkspace(page);
    await savePosition(page, id, 380, 228);
    await openMap(page);

    await page.locator(`.ws-map-tile[data-ws-id="${id}"]`).click();
    await page.click('[data-map-move]');
    await page.locator('.ws-map-canvas').press('ArrowRight');
    await page.locator('.ws-map-canvas').press('ArrowDown');
    await page.locator('.ws-map-canvas').press('Enter');
    await page.waitForTimeout(800);

    const layout = await (await page.request.get('/api/workspace-map/layout')).json();
    expect(layout.layout.positions[id]).toEqual({ x: 418, y: 266 });
  });

  test('an unavailable layout still renders a navigable read-only map (FR-105)', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.route(LAYOUT_API, route =>
      route.fulfill({ status: 503, contentType: 'application/json', body: '{}' })
    );
    await openMap(page);

    await expect(page.locator('.ws-map-canvas.is-readonly')).toBeVisible();
    await expect(page.locator('.ws-map-tile[data-ws-id]').first()).toBeVisible();
    // Navigation still works; placement does not.
    await page.click('[data-map-zoom-in]');
    expect((await cameraOf(page)).zoom).toBeGreaterThan(0.5);
    await expect(page.locator('[data-map-build]')).toBeDisabled();
  });

  test('Snap to Grid is visible, on by default, and persists (FR-57)', async ({ page }) => {
    await ensureWorkspace(page);
    await openMap(page);

    const toggle = page.locator('[data-map-snap]');
    await expect(toggle).toHaveAttribute('aria-pressed', 'true');
    await toggle.click();
    await expect(toggle).toHaveAttribute('aria-pressed', 'false');
    await page.waitForTimeout(500);

    await page.reload();
    await page.locator('.ws-map-world .ws-map-tile[data-ws-id]').first().waitFor();
    await expect(page.locator('[data-map-snap]')).toHaveAttribute('aria-pressed', 'false');
    // Leave the sandbox as we found it.
    await page.locator('[data-map-snap]').click();
  });
});

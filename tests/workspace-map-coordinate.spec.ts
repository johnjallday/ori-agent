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
 * Build reuses this flow rather than replacing it (FR-51), so the spec
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

/**
 * A workspace of this test's own, parked somewhere nothing else is.
 *
 * The demo sandbox accumulates workspaces across runs, so reusing "the first
 * workspace" eventually means dragging one that another building is sitting on
 * top of. A drag test needs a fixture it controls.
 */
async function ownWorkspaceAt(page: Page): Promise<string> {
  const layout = await (await page.request.get('/api/workspace-map/layout')).json();
  const placed = Object.values(layout.layout.positions || {}) as Array<{ x: number; y: number }>;
  const bottom = placed.length ? Math.max(...placed.map(p => p.y)) : 0;
  const id = await ensureWorkspace(page, `Drag subject ${Date.now()}`);
  // Below everything that already exists, so repeated runs never stack their
  // subjects on top of each other.
  await savePosition(page, id, 38, bottom + 380);
  return id;
}

/**
 * A point on a building that is actually on top at that pixel.
 *
 * The map floats control clusters over the world, so a tile's bounding-box
 * centre is not necessarily grabbable. Pressing there hits the control instead
 * and the test then reports that dragging is broken when it is not.
 */
async function grabPointOn(page: Page, id: string): Promise<{ x: number; y: number }> {
  const box = (await page.locator(`.ws-map-tile[data-ws-id="${id}"]`).boundingBox())!;
  const candidates = [
    [0.5, 0.5],
    [0.5, 0.25],
    [0.25, 0.35],
    [0.75, 0.35],
    [0.5, 0.1]
  ];
  for (const [fx, fy] of candidates) {
    const point = { x: box.x + box.width * fx, y: box.y + box.height * fy };
    const onTile = await page.evaluate(
      ([x, y, wanted]) => {
        const el = document.elementFromPoint(Number(x), Number(y));
        const tile = el && (el as HTMLElement).closest('.ws-map-tile[data-ws-id]');
        return !!tile && tile.getAttribute('data-ws-id') === wanted;
      },
      [point.x, point.y, id]
    );
    if (onTile) return point;
  }
  throw new Error(`no grabbable point on ${id} — it is covered`);
}

async function districtSurfacePoint(page: Page, groupId: string) {
  const district = page.locator(`.ws-map-district[data-group-id="${groupId}"]`);
  const box = (await district.boundingBox())!;
  const candidates = [
    [0.5, 0.5],
    [0.85, 0.82],
    [0.15, 0.82],
    [0.85, 0.55],
    [0.15, 0.55],
    [0.5, 0.85]
  ];
  for (const [fx, fy] of candidates) {
    const point = { x: box.x + box.width * fx, y: box.y + box.height * fy };
    const onSurface = await page.evaluate(
      ([x, y, wanted]) => {
        const el = document.elementFromPoint(Number(x), Number(y));
        return (
          !!el &&
          el.classList.contains('ws-map-district') &&
          el.getAttribute('data-group-id') === wanted
        );
      },
      [point.x, point.y, groupId]
    );
    if (onSurface) return point;
  }
  throw new Error(`no uncovered district surface found for ${groupId}`);
}

async function openMap(page: Page) {
  await page.goto('/');
  await page.locator('.ws-map-world .ws-map-tile[data-ws-id]').first().waitFor();
  await page.waitForTimeout(300);
}

async function enableMapDrag(page: Page) {
  const toggle = page.locator('[data-map-drag]');
  await expect(toggle).toBeEnabled();
  if ((await toggle.getAttribute('aria-pressed')) !== 'true') await toggle.click();
  await expect(toggle).toHaveAttribute('aria-pressed', 'true');
  await expect(toggle).toHaveText('Move: on');
}

/**
 * Open the empty-ground context menu and choose one of its framing actions.
 *
 * Fit all, Center selected and Reset view have no buttons: since #317 they are
 * items on the canvas menu, reached by right-clicking bare ground or by
 * Shift+F10 on the focused canvas. The keyboard route is used here because it
 * needs no empty pixel to aim at.
 */
async function frameFromMenu(page: Page, action: 'fit' | 'center' | 'reset-view') {
  await page.locator('[data-ws-map-viewport]').focus();
  await page.keyboard.press('Shift+F10');
  const item = page.locator(`[data-menu-action="${action}"]`);
  await item.waitFor({ state: 'visible' });
  if ((await item.getAttribute('aria-disabled')) === 'true') {
    await page.keyboard.press('Escape');
    return false;
  }
  await item.click();
  await page.waitForTimeout(200);
  return true;
}

/**
 * Bring one workspace into view at 100%.
 *
 * A building parked far from the rest is genuinely off-screen — the map pans,
 * it does not scroll, so Playwright cannot reach it by itself. Center Selected
 * puts this one under the middle of the viewport; Fit All is the fallback when
 * nothing resolvable is selected.
 */
async function centerOnWorkspace(page: Page, id: string) {
  // Selected through the cockpit's own API rather than by clicking: zoom is
  // clamped at 50%, so a layout spread over more than two viewports genuinely
  // cannot all be on screen at once, and the building this test wants may not
  // be reachable by pointer until the camera is already on it.
  await page.evaluate(workspaceId => {
    (window as unknown as { OriHomeCockpit: { select(id: string): void } }).OriHomeCockpit.select(
      workspaceId
    );
  }, id);
  await page.waitForTimeout(300);
  // Home opens the selected workspace's context modal. Keep the selection, but
  // dismiss the modal before opening the canvas menu underneath it.
  const contextModal = page.locator('#cockpitContextModal');
  if (await contextModal.isVisible()) {
    await page.keyboard.press('Escape');
    await expect(contextModal).toBeHidden();
  }
  // Nothing selected (a group child can resolve to its district) leaves Center
  // disabled: fall back to framing everything.
  if (!(await frameFromMenu(page, 'center'))) {
    await frameFromMenu(page, 'fit');
  }
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

    await frameFromMenu(page, 'fit');
    await frameFromMenu(page, 'reset-view');
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

  test('Build places a workspace at the right-clicked point (FR-47 – FR-53, metric 3)', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await openMap(page);

    // Build is a context-menu item now (#317): right-click the spot you want,
    // and that point is the coordinate. There is no mode and no second click.
    const site = await emptyPointOn(page);
    await page.mouse.click(site.x, site.y, { button: 'right' });
    await expect(page.locator('[data-menu-action="build"]')).toBeVisible();
    await page.click('[data-menu-action="build"]');

    // The existing Create Workspace modal opens — the same four-step wizard,
    // not a second form (FR-51).
    await expect(page.locator('#addFolderModal')).toBeVisible();
    await expect(page.locator('[data-ws-map-menu]')).toHaveCount(0);

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

  test('cancelling a build creates nothing (FR-54)', async ({ page }) => {
    await ensureWorkspace(page);
    await openMap(page);
    // Counted through the API rather than the DOM: what must not change is the
    // set of workspaces, not how many tiles happen to be painted.
    const before = new Set((await listWorkspaces(page)).map(ws => ws.id));
    const positionsBefore = Object.keys(
      (await (await page.request.get('/api/workspace-map/layout')).json()).layout.positions
    ).length;

    // Escaping the menu without choosing Build creates nothing.
    const spot = await emptyPointOn(page);
    await page.mouse.click(spot.x, spot.y, { button: 'right' });
    await page.keyboard.press('Escape');
    await expect(page.locator('[data-ws-map-menu]')).toHaveCount(0);
    await expect(page.locator('#addFolderModal')).toBeHidden();

    // And cancelling from inside the modal leaves nothing behind either.
    const site = await emptyPointOn(page);
    await page.mouse.click(site.x, site.y, { button: 'right' });
    await page.click('[data-menu-action="build"]');
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

  test('Move starts off and pointer movement leaves a building inert', async ({ page }) => {
    const id = await ownWorkspaceAt(page);
    await openMap(page);
    await centerOnWorkspace(page, id);

    const toggle = page.locator('[data-map-drag]');
    await expect(toggle).toHaveAttribute('aria-pressed', 'false');
    await expect(toggle).toHaveText('Move: off');
    await expect(page.locator('[data-map-move]')).toHaveCount(0);
    const before = (await anchors(page)).find(anchor => anchor.id === id)!;
    const writes: string[] = [];
    page.on('request', request => {
      if (request.url().includes('/api/workspace-map/layout') && request.method() === 'PATCH') {
        writes.push(request.postData() || '');
      }
    });

    const grab = await grabPointOn(page, id);
    await page.mouse.move(grab.x, grab.y);
    await page.mouse.down();
    await page.mouse.move(grab.x + 140, grab.y + 90, { steps: 15 });
    await page.mouse.up();
    await page.waitForTimeout(300);

    expect((await anchors(page)).find(anchor => anchor.id === id)).toEqual(before);
    expect(writes.filter(body => body.includes('set_positions'))).toHaveLength(0);
    await expect(page.locator(`.ws-map-tile[data-ws-id="${id}"]`)).not.toHaveClass(/is-dragging/);
  });

  test('dragging a building saves once and survives a reload (FR-63 – FR-70)', async ({ page }) => {
    // A dedicated subject at a far, empty coordinate: nothing else is there, so
    // the drag is testing the drag rather than the sandbox.
    const id = await ownWorkspaceAt(page);
    await openMap(page);
    await centerOnWorkspace(page, id);
    await enableMapDrag(page);

    const writes: string[] = [];
    page.on('request', request => {
      if (request.url().includes('/api/workspace-map/layout') && request.method() === 'PATCH') {
        writes.push(request.postData() || '');
      }
    });

    const tile = page.locator(`.ws-map-tile[data-ws-id="${id}"]`);
    const grab = await grabPointOn(page, id);
    await page.mouse.move(grab.x, grab.y);
    await page.mouse.down();
    // Several move events, one drop.
    await page.mouse.move(grab.x + 140, grab.y + 90, { steps: 15 });
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
    const id = await ownWorkspaceAt(page);
    await openMap(page);
    await centerOnWorkspace(page, id);
    await enableMapDrag(page);
    const placedAt = (await anchors(page)).find(a => a.id === id)!;

    await page.route(LAYOUT_API, async route => {
      if (route.request().method() === 'PATCH') {
        await route.fulfill({ status: 500, contentType: 'application/json', body: '{}' });
        return;
      }
      await route.fallback();
    });

    const tile = page.locator(`.ws-map-tile[data-ws-id="${id}"]`);
    const grab = await grabPointOn(page, id);
    await page.mouse.move(grab.x, grab.y);
    await page.mouse.down();
    await page.mouse.move(grab.x + 120, grab.y + 60, { steps: 10 });
    await page.mouse.up();
    await page.waitForTimeout(800);

    const restored = (await anchors(page)).find(a => a.id === id)!;
    expect(restored, 'restored to the committed position').toEqual(placedAt);
    await expect(tile).toHaveClass(/is-unsaved/);
  });

  test('Move mode supports keyboard movement without a second control (FR-77 – FR-79)', async ({
    page
  }) => {
    const id = await ownWorkspaceAt(page);
    await openMap(page);
    const placedAt = (await anchors(page)).find(a => a.id === id)!;
    // Selecting without a pointer is the point of this test: the cockpit's own
    // select path is what a keyboard user reaches through the rail.
    await page.evaluate(workspaceId => {
      (window as unknown as { OriHomeCockpit: { select(id: string): void } }).OriHomeCockpit.select(
        workspaceId
      );
    }, id);
    await page.waitForTimeout(300);
    const contextModal = page.locator('#cockpitContextModal');
    if (await contextModal.isVisible()) {
      await page.keyboard.press('Escape');
      await expect(contextModal).toBeHidden();
    }
    await expect(page.locator('[data-map-drag]')).toHaveAttribute('aria-pressed', 'false');

    await page.click('[data-map-drag]');
    await expect(page.locator('.ws-map-canvas')).toBeFocused();
    await page.locator('.ws-map-canvas').press('ArrowRight');
    await page.locator('.ws-map-canvas').press('ArrowDown');
    await page.locator('.ws-map-canvas').press('Enter');
    await page.waitForTimeout(800);

    const layout = await (await page.request.get('/api/workspace-map/layout')).json();
    expect(layout.layout.positions[id]).toEqual({ x: placedAt.x + 38, y: placedAt.y + 38 });
  });

  test('dragging an automatic district handle neither snaps back nor pushes outsiders', async ({
    page
  }) => {
    test.setTimeout(60_000);
    const created = await page.request.post('/api/workspaces', {
      data: { name: `Automatic drag group ${Date.now()}`, kind: 'group' }
    });
    const group = (await created.json())?.folder?.id as string;
    const childA = await ensureWorkspace(page, `Automatic drag child A ${Date.now()}`);
    const childB = await ensureWorkspace(page, `Automatic drag child B ${Date.now()}`);
    const outsider = await ensureWorkspace(page, `Automatic stationary outsider ${Date.now()}`);
    for (const child of [childA, childB]) {
      await page.request.put(`/api/workspaces/${child}`, { data: { parent_id: group } });
    }

    const beforeLayout = (await (await page.request.get('/api/workspace-map/layout')).json()).layout
      .positions;
    for (const id of [group, childA, childB, outsider]) {
      expect(beforeLayout[id], `${id} begins on automatic fallback placement`).toBeUndefined();
    }

    await openMap(page);
    await centerOnWorkspace(page, group);
    await enableMapDrag(page);

    const district = page.locator(`.ws-map-district[data-group-id="${group}"]`);
    const districtOrigin = await district.evaluate(el => ({
      x: Number.parseFloat((el as HTMLElement).style.left),
      y: Number.parseFloat((el as HTMLElement).style.top)
    }));
    const renderedTiles = await anchors(page);
    const renderedBefore: Record<string, { x: number; y: number }> = {
      [group]: districtOrigin
    };
    for (const id of [childA, childB, outsider]) {
      const anchor = renderedTiles.find(candidate => candidate.id === id);
      expect(anchor, `${id} is rendered on the automatic map`).toBeTruthy();
      renderedBefore[id] = { x: anchor!.x, y: anchor!.y };
    }

    const cameraBefore = await cameraOf(page);
    const writes: string[] = [];
    page.on('request', request => {
      const body = request.postData() || '';
      if (
        request.url().includes('/api/workspace-map/layout') &&
        request.method() === 'PATCH' &&
        body.includes('translate_group')
      ) {
        writes.push(body);
      }
    });

    const handle = district.locator('[data-group-drag]');
    const grab = (await handle.boundingBox())!;
    const x = grab.x + grab.width / 2;
    const y = grab.y + grab.height / 2;
    await page.mouse.move(x, y);
    await page.mouse.down();
    await page.mouse.move(x, y + 300, { steps: 15 });
    await expect(district).toHaveClass(/is-dragging/);
    expect(writes, 'pointer movement performs no write').toHaveLength(0);
    await page.mouse.up();
    await page.waitForTimeout(800);

    expect(writes, 'the drop is one atomic request').toHaveLength(1);
    const operations = JSON.parse(writes[0]).operations;
    expect(operations.map((operation: { op: string }) => operation.op)).toEqual([
      'set_positions',
      'translate_group'
    ]);
    expect(operations[0].positions).toEqual(renderedBefore);
    expect(operations[1].group_id).toBe(group);
    expect(operations[1].delta.x !== 0 || operations[1].delta.y !== 0).toBe(true);

    const afterLayout = (await (await page.request.get('/api/workspace-map/layout')).json()).layout
      .positions;
    for (const id of [group, childA, childB]) {
      expect(afterLayout[id], `${id} persisted at its previewed destination`).toEqual({
        x: renderedBefore[id].x + operations[1].delta.x,
        y: renderedBefore[id].y + operations[1].delta.y
      });
    }
    expect(afterLayout[outsider], 'the unrelated automatic workspace is pinned in place').toEqual(
      renderedBefore[outsider]
    );
    const outsiderAfter = (await anchors(page)).find(anchor => anchor.id === outsider);
    expect(outsiderAfter, 'the unrelated workspace does not reflow after redraw').toEqual({
      id: outsider,
      ...renderedBefore[outsider]
    });
    expect(await cameraOf(page), 'moving the district never pans the camera').toEqual(cameraBefore);
    await expect(district).not.toHaveClass(/is-dragging/);
    const districtAfter = await district.evaluate(el => ({
      x: Number.parseFloat((el as HTMLElement).style.left),
      y: Number.parseFloat((el as HTMLElement).style.top)
    }));
    expect(districtAfter, 'the district remains where it was dropped after redraw').toEqual({
      x: districtOrigin.x + operations[1].delta.x,
      y: districtOrigin.y + operations[1].delta.y
    });

    const after = await listWorkspaces(page);
    for (const child of [childA, childB]) {
      const movedChild = after.find(ws => ws.id === child) as { parent_id?: string };
      expect(movedChild.parent_id, 'group movement never changes membership').toBe(group);
    }
  });

  test('dragging a district surface moves its cluster, not the camera or hierarchy', async ({
    page
  }) => {
    test.setTimeout(60_000);
    const currentLayout = await (await page.request.get('/api/workspace-map/layout')).json();
    const placed = Object.values(currentLayout.layout.positions || {}) as Array<{
      x: number;
      y: number;
    }>;
    const y = (placed.length ? Math.max(...placed.map(position => position.y)) : 0) + 760;
    const created = await page.request.post('/api/workspaces', {
      data: { name: `Surface drag group ${Date.now()}`, kind: 'group' }
    });
    const group = (await created.json())?.folder?.id as string;
    const childA = await ensureWorkspace(page, `Surface drag child A ${Date.now()}`);
    const childB = await ensureWorkspace(page, `Surface drag child B ${Date.now()}`);
    for (const child of [childA, childB]) {
      await page.request.put(`/api/workspaces/${child}`, { data: { parent_id: group } });
    }
    const seeded = await page.request.patch('/api/workspace-map/layout', {
      data: {
        operations: [
          {
            op: 'set_positions',
            positions: {
              [group]: { x: 380, y },
              [childA]: { x: 456, y: y + 76 },
              [childB]: { x: 456, y: y + 456 }
            }
          }
        ]
      }
    });
    expect(seeded.ok(), await seeded.text()).toBe(true);

    await openMap(page);
    await centerOnWorkspace(page, group);
    await enableMapDrag(page);

    const before = (await (await page.request.get('/api/workspace-map/layout')).json()).layout
      .positions;
    const cameraBefore = await cameraOf(page);
    const writes: string[] = [];
    page.on('request', request => {
      const body = request.postData() || '';
      if (
        request.url().includes('/api/workspace-map/layout') &&
        request.method() === 'PATCH' &&
        body.includes('translate_group')
      ) {
        writes.push(body);
      }
    });

    const district = page.locator(`.ws-map-district[data-group-id="${group}"]`);
    const grab = await districtSurfacePoint(page, group);
    await page.mouse.move(grab.x, grab.y);
    await page.mouse.down();
    await page.mouse.move(grab.x + 150, grab.y + 75, { steps: 15 });
    await expect(district).toHaveClass(/is-dragging/);
    expect(writes, 'pointer movement writes nothing').toHaveLength(0);
    expect(await cameraOf(page), 'the gesture did not pan the map').toEqual(cameraBefore);
    await page.mouse.up();
    await page.waitForTimeout(800);

    expect(writes).toHaveLength(1);
    const operation = JSON.parse(writes[0]).operations[0];
    expect(operation.op).toBe('translate_group');
    expect(operation.group_id).toBe(group);
    expect(operation.delta.x !== 0 || operation.delta.y !== 0).toBe(true);

    const afterLayout = await (await page.request.get('/api/workspace-map/layout')).json();
    const groupDelta = {
      x: afterLayout.layout.positions[group].x - before[group].x,
      y: afterLayout.layout.positions[group].y - before[group].y
    };
    for (const child of [childA, childB]) {
      const childDelta = {
        x: afterLayout.layout.positions[child].x - before[child].x,
        y: afterLayout.layout.positions[child].y - before[child].y
      };
      expect(childDelta, 'every member moved by the same delta').toEqual(groupDelta);
    }
    expect(groupDelta).toEqual(operation.delta);

    const after = await listWorkspaces(page);
    for (const child of [childA, childB]) {
      const movedChild = after.find(ws => ws.id === child) as { parent_id?: string };
      expect(movedChild.parent_id, 'group movement never changes membership').toBe(group);
    }
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
    const site = await emptyPointOn(page);
    await page.mouse.click(site.x, site.y, { button: 'right' });
    await expect(page.locator('[data-menu-action="build"]')).toHaveAttribute(
      'aria-disabled',
      'true'
    );
  });

  test('Reset Layout clears only positions and can be undone (FR-109 – FR-112, metric 9)', async ({
    page
  }) => {
    const id = await ownWorkspaceAt(page);
    await openMap(page);
    const before = await (await page.request.get('/api/workspace-map/layout')).json();
    const beforePositions = before.layout.positions;
    expect(Object.keys(beforePositions).length).toBeGreaterThan(0);
    const workspacesBefore = (await listWorkspaces(page)).map(ws => ws.id).sort();

    // Reset Layout is a separate control from Reset View, and it confirms.
    page.once('dialog', dialog => dialog.accept());
    await page.click('[data-map-reset-layout]');
    await page.waitForTimeout(800);

    const cleared = await (await page.request.get('/api/workspace-map/layout')).json();
    expect(Object.keys(cleared.layout.positions).length, 'every anchor cleared').toBe(0);
    expect(
      (await listWorkspaces(page)).map(ws => ws.id).sort(),
      'no workspace was deleted'
    ).toEqual(workspacesBefore);
    // The building is still on the map, at an automatic position.
    await expect(page.locator(`.ws-map-tile[data-ws-id="${id}"]`)).toBeAttached();

    await page.click('[data-map-undo-reset]');
    await page.waitForTimeout(800);
    const restored = await (await page.request.get('/api/workspace-map/layout')).json();
    expect(restored.layout.positions, 'the exact prior set came back').toEqual(beforePositions);
  });

  test('many buildings: pointer movement writes nothing and does not remount (FR-122, metric 11)', async ({
    page
  }) => {
    test.setTimeout(120_000);
    // A fixture big enough that a per-move re-render would be obvious.
    const existing = await listWorkspaces(page);
    for (let i = existing.length; i < 30; i += 1) {
      await page.request.post('/api/workspaces', { data: { name: `Perf ${i} ${Date.now()}` } });
    }
    const id = await ownWorkspaceAt(page);
    await openMap(page);
    await centerOnWorkspace(page, id);
    await enableMapDrag(page);

    // Position writes only. A camera save may also land here — it is debounced
    // and best-effort, and it is not what FR-69 bounds.
    const writes: string[] = [];
    page.on('request', request => {
      if (request.url().includes('/api/workspace-map/layout') && request.method() !== 'GET') {
        const body = request.postData() || '';
        if (body.includes('set_positions') || request.method() === 'DELETE') writes.push(body);
      }
    });
    // Tag the element so a remount (which rebuilds the DOM) is detectable.
    await page.evaluate(workspaceId => {
      const tile = document.querySelector(`.ws-map-tile[data-ws-id="${workspaceId}"]`);
      (tile as HTMLElement & { __probe?: string }).__probe = 'same-element';
    }, id);

    const grab = await grabPointOn(page, id);
    await page.mouse.move(grab.x, grab.y);
    await page.mouse.down();
    for (let step = 1; step <= 20; step += 1) {
      await page.mouse.move(grab.x + step * 6, grab.y + step * 3);
    }
    expect(writes.length, 'pointer movement performs zero writes').toBe(0);
    const sameElement = await page.evaluate(workspaceId => {
      const tile = document.querySelector(`.ws-map-tile[data-ws-id="${workspaceId}"]`);
      return (tile as HTMLElement & { __probe?: string }).__probe === 'same-element';
    }, id);
    expect(sameElement, 'the map was not remounted during the drag').toBe(true);

    await page.mouse.up();
    await page.waitForTimeout(900);
    expect(writes.length, 'one drop, one position update').toBe(1);
  });

  test.describe('a layout wider than two viewports (#307)', () => {
    // Fit All promised to show every workspace but stopped at the old 50% floor,
    // so a spread-out map was quietly framed as a subset. These drive the real
    // fix through a browser: the 10% floor, the buttons that now reach it, and
    // the save/restore round trip that makes a fitted view survive a reload.

    // The cockpit's map is a panel inside Home, so its height follows the
    // window's. At the project's default 1280x720 that panel is ~264px tall and
    // leaves ~92px of usable framing height, which puts EVERY layout on the 10%
    // floor and makes "fits below 50%" impossible to tell apart from "gave up".
    // These tests pin the ordinary desktop viewport the issue describes so the
    // fitted zoom is a property of the fixture rather than of the window. It is
    // scoped to this block: the tests above are written against the project's
    // default window and have no reason to move.
    test.use({ viewport: { width: 1440, height: 900 } });

    /** A span that needs well under 50% to fit, and well over the 10% floor. */
    const WIDE_SPAN = 3000;
    /** Wide enough that even the 10% floor cannot contain it. */
    const IMPOSSIBLE_SPAN = 400000;

    const readLayout = async (page: Page) =>
      (await (await page.request.get('/api/workspace-map/layout')).json()).layout || {};

    async function patchLayout(page: Page, operations: unknown[]) {
      const response = await page.request.patch('/api/workspace-map/layout', {
        data: { operations }
      });
      expect(response.ok(), `patch should succeed: ${await response.text()}`).toBe(true);
    }

    /**
     * Run `body` against a map spread `span` units wide, then put the layout back
     * exactly as it was.
     *
     * Two things make this necessary. The suite is serial and shares one layout
     * record, so a test that leaves 3000-unit coordinates behind hands every
     * later test a map it cannot fit; and the demo sandbox survives between runs,
     * so the damage would accumulate. Every other workspace is pulled into a
     * tight cluster for the duration, so the fitted zoom depends on the fixture
     * rather than on however many workspaces this sandbox happens to hold.
     *
     * The restore runs from `finally`, so a failed assertion cleans up too.
     */
    async function withWideLayout(
      page: Page,
      span: number,
      body: (ids: string[]) => Promise<void>
    ) {
      const before = await readLayout(page);
      const rows = (await listWorkspaces(page)).filter(
        ws => String((ws as { kind?: string }).kind || '').toLowerCase() !== 'group'
      );
      while (rows.length < 2) {
        const id = await ensureWorkspace(page, `Wide fixture ${rows.length} ${Date.now()}`);
        rows.push({ id });
      }
      const ids = [rows[0].id, rows[1].id];

      const positions: Record<string, { x: number; y: number }> = {
        [ids[0]]: { x: 0, y: 0 },
        [ids[1]]: { x: span, y: Math.round(span / 4) }
      };
      // Everything else goes into a tight cluster between the two fixtures, so
      // it neither widens the bounds nor stacks on top of them.
      rows.slice(2).forEach((ws, index) => {
        positions[ws.id] = {
          x: Math.round(span / 2) + (index % 6) * 40,
          y: Math.round(span / 8) + Math.floor(index / 6) * 40
        };
      });

      try {
        await patchLayout(page, [{ op: 'set_positions', positions }]);
        await body(ids);
      } finally {
        const restore: unknown[] = [
          { op: 'restore_positions', positions: before.positions || {} },
          // The camera has to go back too, not just the anchors. Leaving a
          // fitted 26% view behind hands the next test in this serial file a
          // map zoomed most of the way out — which is how the keyboard-move
          // test below started failing while its own behavior was fine. There
          // is no "clear viewport" operation, so a sandbox that had none
          // stored gets the documented default rather than these leftovers.
          {
            op: 'set_viewport',
            viewport: before.viewport || { center_x: 0, center_y: 0, zoom: 1 }
          }
        ];
        await page.request.patch('/api/workspace-map/layout', { data: { operations: restore } });
      }
    }

    const liveText = (page: Page) => page.locator('[data-map-live]').innerText();

    test('Fit All frames a map wider than two viewports (#307, FR-40)', async ({ page }) => {
      await withWideLayout(page, WIDE_SPAN, async () => {
        await openMap(page);
        expect(await frameFromMenu(page, 'fit')).toBe(true);

        const camera = await cameraOf(page);
        expect(camera.zoom, `fitted at ${camera.zoom}`).toBeLessThan(0.5);
        expect(
          camera.zoom,
          'this layout fits — landing exactly on the floor means the canvas was too short'
        ).toBeGreaterThan(0.1);
        expect(await liveText(page)).toContain('Showing every workspace');

        // Every building is inside the part of the canvas the control strip does
        // not cover — framed, not merely zoomed away from.
        const canvas = (await page.locator('.ws-map-canvas').boundingBox())!;
        const strip = await page.locator('.ws-map-actions').boundingBox();
        const clearBottom = strip ? strip.y : canvas.y + canvas.height;
        const tiles = page.locator('.ws-map-world .ws-map-tile[data-ws-id]');
        const count = await tiles.count();
        expect(count).toBeGreaterThan(1);
        for (let i = 0; i < count; i += 1) {
          const box = (await tiles.nth(i).boundingBox())!;
          expect(box.x, `tile ${i} off the left edge`).toBeGreaterThanOrEqual(canvas.x - 1);
          expect(box.x + box.width, `tile ${i} off the right edge`).toBeLessThanOrEqual(
            canvas.x + canvas.width + 1
          );
          expect(box.y, `tile ${i} off the top edge`).toBeGreaterThanOrEqual(canvas.y - 1);
          expect(box.y + box.height, `tile ${i} behind the control strip`).toBeLessThanOrEqual(
            clearBottom + 1
          );
        }
      });
    });

    test('the zoom controls stay truthful below 50% (#307, FR-38)', async ({ page }) => {
      await withWideLayout(page, WIDE_SPAN, async () => {
        await openMap(page);
        await frameFromMenu(page, 'fit');
        const fitted = await cameraOf(page);
        expect(fitted.zoom).toBeLessThan(0.5);

        await expect(page.locator('[data-map-zoom-readout]')).toHaveText(
          `${Math.round(fitted.zoom * 100)}%`
        );
        // A fitted view is not a dead end: the same floor applies to the
        // buttons, so the user can still zoom out by hand from here.
        const zoomOut = page.locator('[data-map-zoom-out]');
        await expect(zoomOut).toBeEnabled();
        await zoomOut.click();
        await page.waitForTimeout(200);
        const zoomedOut = await cameraOf(page);
        expect(zoomedOut.zoom, 'Zoom Out kept going past the fitted value').toBeLessThan(
          fitted.zoom
        );
        expect(zoomedOut.zoom).toBeGreaterThanOrEqual(0.1);

        await frameFromMenu(page, 'fit');
        await page.locator('[data-map-zoom-in]').click();
        await page.waitForTimeout(200);
        const zoomedIn = await cameraOf(page);
        expect(zoomedIn.zoom, 'Zoom In steps inward from the fitted value').toBeGreaterThan(
          fitted.zoom
        );
        // Exactly one step, rather than snapping back to the ordinary range.
        // This used to assert `< 0.5` outright, which quietly depended on the
        // fitted value sitting more than one step below the floor boundary —
        // true only while the map was stuck at a 320px height. #372 gave the
        // map the window, so the same fixture now fits at ~0.40 and one honest
        // 1.25x step lands just over 0.5. Asserting the step itself is both
        // viewport-independent and a tighter statement of FR-38.
        const limits = await page.evaluate(
          () =>
            (window as unknown as { OriWorkspaceMap: { camera: { limits: { step: number } } } })
              .OriWorkspaceMap.camera.limits
        );
        expect(zoomedIn.zoom, 'without jumping straight back to the ordinary range').toBeCloseTo(
          fitted.zoom * limits.step,
          5
        );
      });
    });

    test('zooming out by hand bottoms out at 10%, not before (#307, FR-38)', async ({ page }) => {
      await ensureWorkspace(page);
      await openMap(page);
      await frameFromMenu(page, 'reset-view');
      expect((await cameraOf(page)).zoom).toBe(1);

      // The old floor was 50%, which left a fitted wide view somewhere the user
      // could not zoom out of by hand. One floor now, and the button reaches it.
      const zoomOut = page.locator('[data-map-zoom-out]');
      for (let press = 0; press < 20; press += 1) {
        if (await zoomOut.isDisabled()) break;
        await zoomOut.click();
        await page.waitForTimeout(100);
      }
      expect((await cameraOf(page)).zoom, 'the floor a gesture may reach').toBe(0.1);
      await expect(zoomOut).toBeDisabled();
      await frameFromMenu(page, 'reset-view');
    });

    test('pan and Center Selected keep a fitted sub-50% view (#307, FR-41)', async ({ page }) => {
      await withWideLayout(page, WIDE_SPAN, async ids => {
        await openMap(page);
        await frameFromMenu(page, 'fit');
        const fitted = await cameraOf(page);
        expect(fitted.zoom).toBeLessThan(0.5);
        const placed = await anchors(page);

        const from = await emptyPointOn(page);
        await page.mouse.move(from.x, from.y);
        await page.mouse.down();
        await page.mouse.move(from.x - 160, from.y - 60, { steps: 12 });
        await page.mouse.up();
        await page.waitForTimeout(250);
        const panned = await cameraOf(page);
        expect(panned.zoom, 'panning is not a zoom').toBe(fitted.zoom);
        expect(panned.centerX, 'and it did pan').not.toBe(fitted.centerX);

        await centerOnWorkspace(page, ids[0]);
        expect((await cameraOf(page)).zoom, 'Center Selected is not a zoom either').toBe(
          fitted.zoom
        );
        // The record is untouched by any of it (FR-13).
        expect(await anchors(page)).toEqual(placed);
      });
    });

    test('a fitted sub-50% camera is saved and restored (#307, FR-43 – FR-45)', async ({
      page
    }) => {
      await withWideLayout(page, WIDE_SPAN, async () => {
        await openMap(page);
        await frameFromMenu(page, 'fit');
        const fitted = await cameraOf(page);
        expect(fitted.zoom).toBeLessThan(0.5);

        // Past the 600ms camera-save debounce.
        await page.waitForTimeout(1200);
        const stored = (await readLayout(page)).viewport;
        expect(stored, 'the fitted camera was never stored').toBeTruthy();
        expect(stored.zoom, `stored ${stored.zoom}`).toBeLessThan(0.5);
        expect(stored.zoom).toBeGreaterThanOrEqual(0.1);
        expect(Math.abs(stored.zoom - fitted.zoom)).toBeLessThan(0.001);

        await page.reload();
        await page.locator('.ws-map-world .ws-map-tile[data-ws-id]').first().waitFor();
        await page.waitForTimeout(400);
        const restored = await cameraOf(page);
        expect(restored.zoom, 'the saved view snapped back instead of reopening').toBe(stored.zoom);
      });
    });

    test('a layout too wide even for the floor says so (#307)', async ({ page }) => {
      await withWideLayout(page, IMPOSSIBLE_SPAN, async () => {
        await openMap(page);
        await frameFromMenu(page, 'fit');

        expect((await cameraOf(page)).zoom, 'the floor holds').toBe(0.1);
        expect(
          await liveText(page),
          'claiming success here would send someone hunting for a workspace that is not on screen'
        ).toContain('still off-screen');
      });
    });
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

  // -------------------------------------------------------------------------
  // Group districts (#346)
  //
  // These cover what a DOM harness cannot: real pointer geometry against a
  // screen-space overlay that must stay finger-sized while the world scales
  // underneath it, and the zoom range those targets have to survive.
  // -------------------------------------------------------------------------
  test.describe('group districts (#346)', () => {
    // Serial: every test seeds a fixture into the ONE shared demo server and
    // picks its row from how many already exist. Run in parallel, two workers
    // read the same count and stack their districts on each other.
    test.describe.configure({ mode: 'serial' });

    /**
     * A group with two members, on a row of its own.
     *
     * Each test in this block seeds its own fixture into the same sandbox, so a
     * fixed coordinate would stack every district on the last one — producing
     * genuine (and correctly reported) containment conflicts that have nothing
     * to do with what is being tested.
     */
    async function seedDistrict(page: Page) {
      const tag = String(Date.now()).slice(-6);
      const existing = await listWorkspaces(page);
      const row = existing.filter(ws => String(ws.name || '').startsWith('District A ')).length;
      const make = async (name: string, kind = 'workspace') => {
        const res = await page.request.post('/api/workspaces', { data: { name, kind } });
        return (await res.json()).folder.id;
      };
      const group = await make(`District ${tag}`, 'group');
      const a = await make(`District A ${tag}`);
      const b = await make(`District B ${tag}`);
      for (const id of [a, b]) {
        await page.request.put(`/api/workspaces/${id}`, { data: { parent_id: group } });
      }
      const y = 380 + row * 570;
      await page.request.patch('/api/workspace-map/layout', {
        data: {
          operations: [
            { op: 'set_positions', positions: { [a]: { x: 380, y }, [b]: { x: 608, y } } }
          ]
        }
      });
      return { group, a, b, tag, y };
    }

    const districtOf = (page: Page, group: string) =>
      page.locator(`.ws-map-district[data-group-id="${group}"]`);

    /**
     * Select the district, retrying across the cockpit's hydration remounts.
     *
     * Frames everything first: the sandbox is shared with every other spec in
     * this file, and a camera one of them left behind can put the fixture
     * outside the canvas entirely, where no click can reach it.
     */
    async function selectDistrict(page: Page, group: string) {
      const district = districtOf(page, group);
      await district.waitFor({ timeout: 15000 });
      await frameFromMenu(page, 'fit');
      await expect(async () => {
        await frameFromMenu(page, 'fit');
        await district.locator('.ws-map-district-tag').click({ force: true });
        await expect(page.locator('[data-ws-map-resize]')).toBeVisible({ timeout: 1500 });
      }).toPass({ timeout: 20000 });
    }

    async function zoomTo(page: Page, target: number) {
      // The camera's own buttons, so the spec exercises the real clamp rather
      // than poking module state.
      for (let i = 0; i < 40; i += 1) {
        const zoom = (await cameraOf(page)).zoom;
        if (Math.abs(zoom - target) < 0.02) return zoom;
        await page.locator(zoom < target ? '[data-map-zoom-in]' : '[data-map-zoom-out]').click();
        await page.waitForTimeout(40);
        const next = (await cameraOf(page)).zoom;
        if (next === zoom) return next; // clamped
      }
      return (await cameraOf(page)).zoom;
    }

    test('a populated district is compact and never spans to a stale anchor (FR-16, FR-25)', async ({
      page
    }) => {
      const { group, y } = await seedDistrict(page);
      // A group anchor left far below its members, as group creation used to
      // produce. It must not stretch the district by a single unit.
      await page.request.patch('/api/workspace-map/layout', {
        data: {
          operations: [{ op: 'set_positions', positions: { [group]: { x: 380, y: y + 6000 } } }]
        }
      });
      await openMap(page);

      const district = districtOf(page, group);
      await district.waitFor();
      const frame = await district.evaluate(el => ({
        y: parseFloat((el as HTMLElement).style.top),
        height: parseFloat((el as HTMLElement).style.height)
      }));
      expect(frame.y).toBeLessThan(y + 20);
      expect(frame.height).toBeLessThan(400);
    });

    test('resize handles keep a 44px target from 10% through 200% zoom (FR-56)', async ({
      page
    }) => {
      const { group } = await seedDistrict(page);
      await openMap(page);
      await districtOf(page, group).waitFor();
      await selectDistrict(page, group);

      for (const target of [0.1, 1, 2]) {
        const zoom = await zoomTo(page, target);
        for (const edge of ['n', 'ne', 'e', 'se', 's', 'sw', 'w', 'nw']) {
          const box = await page.locator(`[data-resize-handle="${edge}"]`).boundingBox();
          expect(box, `${edge} has a box at ${zoom}x`).not.toBeNull();
          expect(box!.width, `${edge} width at ${zoom}x`).toBeGreaterThanOrEqual(44);
          expect(box!.height, `${edge} height at ${zoom}x`).toBeGreaterThanOrEqual(44);
        }
      }
    });

    test('the resize overlay never intercepts map gestures aimed past it (FR-157)', async ({
      page
    }) => {
      const { group } = await seedDistrict(page);
      await openMap(page);
      await districtOf(page, group).waitFor();
      await selectDistrict(page, group);

      // The overlay spans the whole canvas. A point inside it but away from any
      // handle must hit the map, not the overlay.
      const canvas = (await page.locator('[data-ws-map-viewport]').boundingBox())!;
      const probe = await page.evaluate(
        point => {
          const el = document.elementFromPoint(point.x, point.y);
          return el ? el.className : 'none';
        },
        { x: canvas.x + canvas.width / 2, y: canvas.y + canvas.height / 2 }
      );
      expect(String(probe)).not.toContain('ws-map-resize-overlay');
      expect(String(probe)).not.toContain('ws-map-resize-box');
    });

    test('the district header stays clear of the resize handles (FR-157)', async ({ page }) => {
      const { group } = await seedDistrict(page);
      await openMap(page);
      await districtOf(page, group).waitFor();
      await selectDistrict(page, group);
      await zoomTo(page, 1);

      // Every header control must be the topmost thing at its own centre —
      // a resize target sitting over it would silently swallow the click.
      for (const attr of ['data-group-collapse', 'data-group-drag', 'data-group-menu']) {
        const control = districtOf(page, group).locator(`[${attr}]`);
        const box = await control.boundingBox();
        if (!box) continue; // off-screen at this camera; the rail covers it (FR-167)
        const owner = await page.evaluate(
          point => {
            const el = document.elementFromPoint(point.x, point.y);
            return el ? (el.closest('button')?.getAttribute('data-resize-handle') ?? 'ok') : 'none';
          },
          { x: box.x + box.width / 2, y: box.y + box.height / 2 }
        );
        expect(owner, `${attr} is covered by a resize handle`).not.toMatch(/^[ns]?[ew]?$/);
      }
    });

    test('collapse hides descendants, tightens Fit all, and survives a reload (FR-104, FR-112)', async ({
      page
    }) => {
      const { group, a } = await seedDistrict(page);
      await openMap(page);
      await districtOf(page, group).waitFor();

      await frameFromMenu(page, 'fit');
      const openZoom = (await cameraOf(page)).zoom;

      // Select, centre, and zoom to 100% first, so the header control is
      // genuinely on screen at a usable size — clicking it is then also proof
      // that it is reachable (FR-167). Fit all frames every fixture in the
      // sandbox, which can leave the camera at the 10% floor where a district's
      // controls are sub-pixel.
      await selectDistrict(page, group);
      await frameFromMenu(page, 'center');
      await zoomTo(page, 1);
      await districtOf(page, group).locator('[data-group-collapse]').click();
      await page.waitForTimeout(600);

      await expect(page.locator(`.ws-map-tile[data-ws-id="${a}"]`)).toHaveCount(0);
      await expect(districtOf(page, group)).toHaveClass(/is-collapsed/);

      await frameFromMenu(page, 'fit');
      expect(
        (await cameraOf(page)).zoom,
        'Fit all frames less content once the members are hidden'
      ).toBeGreaterThanOrEqual(openZoom);

      await page.reload();
      await districtOf(page, group).waitFor();
      await expect(districtOf(page, group)).toHaveClass(/is-collapsed/);

      // Leave the sandbox usable for the next spec.
      await selectDistrict(page, group).catch(() => {});
      await frameFromMenu(page, 'center');
      await zoomTo(page, 1);
      await districtOf(page, group).locator('[data-group-collapse]').click({ force: true });
      await page.waitForTimeout(400);
    });

    test('a district page never scrolls sideways at a narrow width (FR-169)', async ({ page }) => {
      const { group } = await seedDistrict(page);
      await page.setViewportSize({ width: 420, height: 820 });
      await openMap(page);
      await districtOf(page, group).waitFor();
      await selectDistrict(page, group).catch(() => {});

      const overflow = await page.evaluate(() => ({
        scroll: document.documentElement.scrollWidth,
        client: document.documentElement.clientWidth
      }));
      expect(overflow.scroll, 'a selected district must not widen the page').toBeLessThanOrEqual(
        overflow.client + 1
      );
    });
  });

  // #372 — the two Workspace Map polish changes. Both are claims about the
  // rendered page rather than about pure functions, which is why they are here
  // and not only in workspace-map.test.js.
  test.describe('viewport fit and create affordances (#372)', () => {
    test.describe.configure({ mode: 'serial' });

    const theatreHeight = (page: Page) =>
      page.evaluate(() => {
        const el = document.querySelector('.ws-map-theatre');
        return el ? Math.round(el.getBoundingClientRect().height) : 0;
      });

    test('the map grows with the window instead of sitting at its floor (#365)', async ({
      page
    }) => {
      await ensureWorkspace(page);

      await page.setViewportSize({ width: 1440, height: 800 });
      await openMap(page);
      const short = await theatreHeight(page);

      // No reload: this is the live resize path, and the assertion is relative
      // on purpose. A fixed pixel expectation would encode whatever chrome the
      // page happens to have above the map today and break on any header edit.
      await page.setViewportSize({ width: 1440, height: 1200 });
      await page.waitForTimeout(500);
      const tall = await theatreHeight(page);

      expect(tall, 'a 400px taller window must make the map taller').toBeGreaterThan(short + 300);
      expect(short, 'and even the short window must use more than the 320px floor').toBeGreaterThan(
        400
      );
      // It must use the window without overrunning it.
      expect(tall).toBeLessThanOrEqual(1200);
      expect(
        tall / 1200,
        'the map should own most of the window, not a band across the top'
      ).toBeGreaterThan(0.6);
    });

    test('resizing changes what is visible and never where anything is (#365, FR-13)', async ({
      page
    }) => {
      const id = await ensureWorkspace(page);
      await savePosition(page, id, 342, 266);
      await page.setViewportSize({ width: 1440, height: 800 });
      await openMap(page);
      const before = await anchors(page);
      const short = await theatreHeight(page);

      await page.setViewportSize({ width: 1440, height: 1200 });
      await page.waitForTimeout(500);

      expect(await theatreHeight(page), 'the map did resize').toBeGreaterThan(short);
      expect(await anchors(page), 'but no world anchor moved').toEqual(before);
    });

    test('a populated map draws no create pad and keeps its real create action (#367)', async ({
      page
    }) => {
      await ensureWorkspace(page);
      await page.setViewportSize({ width: 1440, height: 900 });
      await openMap(page);

      await expect(
        page.locator('.ws-map-world .ws-map-pad'),
        'the ordinary in-canvas create pad is gone'
      ).toHaveCount(0);
      await expect(page.locator('.ws-map-pad:not(.ws-map-pad--hero)')).toHaveCount(0);

      // Removing it must not leave the page with no way to make a workspace:
      // Home's workspace-area header owns that action in cockpit mode.
      await expect(
        page.locator('#cockpitCreateWorkspaceBtn'),
        'the Home header create action survives'
      ).toBeVisible();
    });

    test('the narrow layout still stacks without scrolling sideways (#365)', async ({ page }) => {
      await ensureWorkspace(page);
      await page.setViewportSize({ width: 720, height: 900 });
      await openMap(page);

      const overflow = await page.evaluate(() => ({
        scroll: document.documentElement.scrollWidth,
        client: document.documentElement.clientWidth
      }));
      expect(
        overflow.scroll,
        'the fit-to-viewport rule must not widen the narrow page'
      ).toBeLessThanOrEqual(overflow.client + 1);
      expect(await theatreHeight(page), 'and the map keeps a usable height').toBeGreaterThanOrEqual(
        300
      );
    });
  });
});

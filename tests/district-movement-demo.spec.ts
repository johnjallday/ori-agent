import { test, expect, Page, Locator } from '@playwright/test';

/**
 * Issue #346 group-4 demo: custom frames survive movement.
 *
 * Moves a custom-sized district, moves one member past its edge, attempts a
 * false-containment move, and then reparents through the API — the Tree/other
 * tab path — to prove the Map redraws the truth without writing a repair.
 *
 * Not part of CI, and it needs a FRESH demo sandbox — one run per server:
 *   ./scripts/demo-346.sh 8953 --fresh
 *   ./scripts/e2e.sh --port 8953 tests/district-movement-demo.spec.ts
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
  const group = await make(`Move Group ${tag}`, 'group');
  const a = await make(`Move A ${tag}`);
  const b = await make(`Move B ${tag}`);
  const outsider = await make(`Move Outsider ${tag}`);
  for (const id of [a, b]) {
    await page.request.put(`/api/workspaces/${id}`, { data: { parent_id: group } });
  }
  await page.request.patch('/api/workspace-map/layout', {
    data: {
      operations: [
        {
          op: 'set_positions',
          positions: {
            [a]: { x: 380, y: 380 },
            [b]: { x: 608, y: 380 },
            [outsider]: { x: 1444, y: 380 }
          }
        },
        // A deliberately roomy custom frame, so the district has reserved space
        // to carry around and to defend.
        {
          op: 'set_group_frame',
          group_id: group,
          frame: { x: 360, y: 360, width: 460, height: 220 }
        }
      ]
    }
  });
  return { group, a, b, outsider };
}

async function frameOf(el: Locator) {
  return el.evaluate(node => ({
    x: parseFloat((node as HTMLElement).style.left),
    y: parseFloat((node as HTMLElement).style.top),
    width: parseFloat((node as HTMLElement).style.width),
    height: parseFloat((node as HTMLElement).style.height)
  }));
}

async function layoutOf(page: Page) {
  return (await (await page.request.get('/api/workspace-map/layout')).json()).layout;
}

/** Drag an element by a WORLD-space delta, so the demo is zoom-independent. */
async function dragBy(page: Page, target: Locator, dx: number, dy: number, release = true) {
  const zoom = await page.evaluate(() => (window as any).OriWorkspaceMap.getCamera().zoom);
  const box = (await target.boundingBox())!;
  const cx = box.x + box.width / 2;
  const cy = box.y + box.height / 2;
  await page.mouse.move(cx, cy);
  await page.mouse.down();
  await page.mouse.move(cx + dx * zoom, cy + dy * zoom, { steps: 6 });
  if (release) await page.mouse.up();
}

test('#346 a custom frame moves with its district, grows for a member, and refuses false containment', async ({
  page
}) => {
  await skipOnboarding(page);
  const tag = String(Date.now()).slice(-5);
  const existing = await (await page.request.get('/api/workspaces')).json();
  const stale = (existing.folders || []).filter((f: { name?: string }) =>
    String(f.name || '').startsWith('Move A ')
  ).length;
  expect(stale, 'this demo needs a fresh sandbox: ./scripts/demo-346.sh <port> --fresh').toBe(0);
  const { group, a, outsider } = await seedFixture(page, tag);

  await page.goto('/');
  const district = page.locator(`.ws-map-district[data-group-id="${group}"]`);
  await district.waitFor({ timeout: 15000 });

  const canvas = page.locator('[data-ws-map-viewport]');
  await canvas.click({ button: 'right', position: { x: 40, y: 40 } });
  await page.getByRole('menuitem', { name: 'Fit all' }).click();
  await page.waitForTimeout(300);

  const startFrame = await frameOf(district);
  const startLayout = await layoutOf(page);
  expect(startLayout.groups[group].sizing_mode).toBe('custom');

  // FR-91: moving the district translates its saved frame without resizing it.
  await dragBy(page, district.locator('[data-group-drag]'), 0, 300);
  await page.waitForTimeout(500);
  const movedLayout = await layoutOf(page);
  const movedFrame = movedLayout.groups[group].frame;
  expect(movedFrame.width).toBe(startLayout.groups[group].frame.width);
  expect(movedFrame.height).toBe(startLayout.groups[group].frame.height);
  expect(movedFrame.y).toBeGreaterThan(startLayout.groups[group].frame.y);
  // Its members carried the same delta.
  const memberDelta = movedLayout.positions[a].y - startLayout.positions[a].y;
  expect(memberDelta).toBe(movedFrame.y - startLayout.groups[group].frame.y);
  expect(movedLayout.positions[outsider]).toEqual(startLayout.positions[outsider]);

  await page.screenshot({ path: 'test-results/346-move-district.png' });

  // FR-38: moving one member past an edge grows the frame in the SAME change.
  const beforeMember = await layoutOf(page);
  await dragBy(page, page.locator(`.ws-map-tile[data-ws-id="${a}"]`), 0, 260);
  await page.waitForTimeout(500);
  const afterMember = await layoutOf(page);
  expect(afterMember.positions[a].y).toBeGreaterThan(beforeMember.positions[a].y);
  expect(afterMember.groups[group].frame.height).toBeGreaterThan(
    beforeMember.groups[group].frame.height
  );
  expect(afterMember.groups[group].frame.y).toBe(
    beforeMember.groups[group].frame.y,
    'only the edge it crossed moved'
  );
  // One accepted change: the anchor and the frame share a revision.
  expect(afterMember.revision).toBe(beforeMember.revision + 1);

  await page.screenshot({ path: 'test-results/346-move-member-grew-frame.png' });

  // FR-84: dragging the district onto the outsider is refused before save.
  // Back up to the outsider's row as well as east — the district was moved down
  // earlier, so a purely eastward drag would pass harmlessly beneath it.
  const beforeBlocked = await layoutOf(page);
  await dragBy(page, district.locator('[data-group-drag]'), 900, -280, false);
  await expect(district).toHaveClass(/is-blocked/);
  await page.screenshot({ path: 'test-results/346-move-blocked.png' });
  await page.mouse.up();
  await page.waitForTimeout(400);
  expect((await layoutOf(page)).revision).toBe(beforeBlocked.revision);

  // FR-87 – FR-89: a reparent made outside the Map is authoritative and is
  // never rejected for how it looks. The Map redraws the real membership — the
  // frame grows to hold its new member, which stays exactly where it was — and
  // writes nothing at all. (The conflicted case, where a frame ends up around a
  // NON-member, is asserted in workspace-map.test.js; here the reparent
  // resolves the overlap rather than creating one.)
  await page.request.put(`/api/workspaces/${outsider}`, { data: { parent_id: group } });
  await page.reload();
  await district.waitFor({ timeout: 15000 });
  await page.waitForTimeout(500);
  const afterReparent = await layoutOf(page);
  expect(afterReparent.positions[outsider]).toEqual(
    beforeBlocked.positions[outsider],
    'reading the new hierarchy moved nothing'
  );
  expect(afterReparent.revision).toBe(
    beforeBlocked.revision,
    'and wrote nothing — a read never repairs'
  );
  await expect(district.locator('.ws-map-district-count')).toHaveText('3 workspaces');

  await page.screenshot({ path: 'test-results/346-move-reparented.png' });
});

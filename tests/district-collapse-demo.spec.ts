import { test, expect, Page, Locator } from '@playwright/test';

/**
 * Issue #346 group-5 demo: collapse and expand.
 *
 * Collapses a custom-sized district, checks that Fit all tightens and the
 * descendants really are unreachable, moves it while collapsed, reloads, and
 * expands it back to exactly the frame it had.
 *
 * Not part of CI, and it needs a FRESH demo sandbox — one run per server:
 *   ./scripts/demo-346.sh 8955 --fresh
 *   ./scripts/e2e.sh --port 8955 tests/district-collapse-demo.spec.ts
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
  const group = await make(`Fold Group ${tag}`, 'group');
  const a = await make(`Fold A ${tag}`);
  const b = await make(`Fold B ${tag}`);
  for (const id of [a, b]) {
    await page.request.put(`/api/workspaces/${id}`, { data: { parent_id: group } });
  }
  await page.request.patch('/api/workspace-map/layout', {
    data: {
      operations: [
        { op: 'set_positions', positions: { [a]: { x: 380, y: 380 }, [b]: { x: 950, y: 950 } } },
        {
          op: 'set_group_frame',
          group_id: group,
          frame: { x: 340, y: 340, width: 820, height: 800 }
        }
      ]
    }
  });
  return { group, a, b };
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

/**
 * Frame the district and select it, so its header controls are on screen.
 *
 * Without this the district can sit under the camera control strip or off the
 * canvas entirely, and a click aimed at its collapse control lands on whatever
 * is really at those coordinates. (A user in that position would reach for the
 * rail's Collapse group instead — FR-167.)
 */
async function frameAndSelect(page: Page, district: Locator) {
  const canvas = page.locator('[data-ws-map-viewport]');
  await canvas.click({ button: 'right', position: { x: 40, y: 40 } });
  await page.getByRole('menuitem', { name: 'Fit all' }).click();
  await page.waitForTimeout(300);
  await expect(async () => {
    await district.locator('.ws-map-district-tag').click({ force: true });
    await expect(page.locator('[data-rail-map-layout]')).toBeVisible({ timeout: 1500 });
  }).toPass({ timeout: 20000 });
  await canvas.click({ button: 'right', position: { x: 40, y: 40 } });
  await page.getByRole('menuitem', { name: 'Center selected' }).click();
  await page.waitForTimeout(300);
}

test('#346 a district collapses to a compact summary and expands back exactly', async ({
  page
}) => {
  await skipOnboarding(page);
  const tag = String(Date.now()).slice(-5);
  const existing = await (await page.request.get('/api/workspaces')).json();
  const stale = (existing.folders || []).filter((f: { name?: string }) =>
    String(f.name || '').startsWith('Fold A ')
  ).length;
  expect(stale, 'this demo needs a fresh sandbox: ./scripts/demo-346.sh <port> --fresh').toBe(0);
  const { group, a, b } = await seedFixture(page, tag);

  await page.goto('/');
  const district = page.locator(`.ws-map-district[data-group-id="${group}"]`);
  await district.waitFor({ timeout: 15000 });

  await frameAndSelect(page, district);

  const expandedFrame = await frameOf(district);
  expect(await page.locator('.ws-map-tile').count()).toBeGreaterThanOrEqual(2);
  await expect(district.locator('[data-group-collapse]')).toHaveAttribute('aria-expanded', 'true');

  // FR-102: collapse from the header's own control, with the district selected —
  // which is also the case where the resize handles must not swallow the click.
  await district.locator('[data-group-collapse]').click();
  await page.waitForTimeout(600);

  // FR-104/FR-105: the members are gone from the map entirely — not merely
  // invisible, but absent, so nothing can focus or right-click them.
  await expect(page.locator(`.ws-map-tile[data-ws-id="${a}"]`)).toHaveCount(0);
  await expect(page.locator(`.ws-map-tile[data-ws-id="${b}"]`)).toHaveCount(0);
  await expect(district.locator('[data-group-collapse]')).toHaveAttribute('aria-expanded', 'false');
  await expect(district).toHaveClass(/is-collapsed/);
  // FR-106: it still says what it holds.
  await expect(district.locator('.ws-map-district-count')).toHaveText('2 workspaces');

  const compact = await frameOf(district);
  expect(compact.width).toBeLessThan(expandedFrame.width);
  expect(compact.height).toBeLessThan(expandedFrame.height);

  // FR-104: hiding is presentation only — both coordinates are untouched.
  const collapsedLayout = await layoutOf(page);
  expect(collapsedLayout.groups[group].collapsed).toBe(true);
  expect(collapsedLayout.groups[group].frame.width).toBe(820, 'the expanded frame is preserved');
  expect(collapsedLayout.positions[a]).toEqual({ x: 380, y: 380 });
  expect(collapsedLayout.positions[b]).toEqual({ x: 950, y: 950 });

  await page.screenshot({ path: 'test-results/346-collapsed.png' });

  // FR-113: moving it while collapsed carries the hidden descendants too.
  const zoom = await page.evaluate(() => (window as any).OriWorkspaceMap.getCamera().zoom);
  const grip = district.locator('[data-group-drag]');
  const box = (await grip.boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2 + 190 * zoom, box.y + box.height / 2, { steps: 6 });
  await page.mouse.up();
  await page.waitForTimeout(600);

  const movedLayout = await layoutOf(page);
  const delta = movedLayout.positions[a].x - collapsedLayout.positions[a].x;
  expect(delta).toBeGreaterThan(0);
  expect(movedLayout.positions[b].x - collapsedLayout.positions[b].x).toBe(
    delta,
    'the hidden descendant moved by the same delta'
  );
  expect(movedLayout.groups[group].frame.x - collapsedLayout.groups[group].frame.x).toBe(
    delta,
    'and so did the preserved expanded frame'
  );

  // FR-103: the state survives a reload, then expands back to exactly the
  // arrangement that was there before (FR-116).
  await page.reload();
  await district.waitFor({ timeout: 15000 });
  await expect(district).toHaveClass(/is-collapsed/);

  // Expand from the Home rail this time, so both control surfaces are exercised
  // and both are proved to run the same action (FR-156).
  await frameAndSelect(page, district);
  const railCollapse = page.locator('[data-cockpit-group-collapse]');
  await expect(railCollapse).toHaveText('Expand group');
  await railCollapse.click();
  await page.waitForTimeout(600);

  const restored = await frameOf(district);
  expect(restored.width).toBe(expandedFrame.width);
  expect(restored.height).toBe(expandedFrame.height);
  expect(restored.x).toBe(expandedFrame.x + delta, 'at its new home, same size');
  await expect(page.locator(`.ws-map-tile[data-ws-id="${a}"]`)).toHaveCount(1);
  await expect(page.locator(`.ws-map-tile[data-ws-id="${b}"]`)).toHaveCount(1);

  await page.screenshot({ path: 'test-results/346-expanded-back.png' });
});

import { test, expect, Page } from '@playwright/test';

/**
 * Issue #346 group-2 demo: grouping already-arranged workspaces from Home
 * preserves every coordinate, produces a compact selected district, and tells
 * the same story in the header, the camera, and the shared context modal.
 *
 * Not part of CI. Run against the isolated demo server:
 *   ./scripts/e2e.sh --port 8947 tests/district-grouping-demo.spec.ts
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

/**
 * Create two workspaces and place them side by side, both saved by hand.
 *
 * Each run gets its own row of the world. Re-running against the same demo
 * sandbox would otherwise stack this run's buildings exactly on top of the last
 * run's, and the screenshot would show two districts drawn over each other.
 */
async function seedTwoPlacedWorkspaces(page: Page, tag: string, row: number) {
  const ids: string[] = [];
  for (const name of [`Demo A ${tag}`, `Demo B ${tag}`]) {
    const res = await page.request.post('/api/workspaces', { data: { name } });
    ids.push((await res.json()).folder.id);
  }
  const y = 1520 + row * 570;
  await page.request.patch('/api/workspace-map/layout', {
    data: {
      operations: [
        {
          op: 'set_positions',
          positions: {
            [ids[0]]: { x: 1520, y },
            [ids[1]]: { x: 1748, y }
          }
        }
      ]
    }
  });
  return { ids, y };
}

test('#346 grouping arranged workspaces keeps their coordinates and frames them tightly', async ({
  page
}) => {
  await skipOnboarding(page);
  const tag = String(Date.now()).slice(-5);
  // One fresh row per run, derived from how many demo workspaces already exist.
  const existing = await (await page.request.get('/api/workspaces')).json();
  const row = (existing.folders || []).filter((f: { name?: string }) =>
    String(f.name || '').startsWith('Demo A ')
  ).length;
  const {
    ids: [a, b],
    y
  } = await seedTwoPlacedWorkspaces(page, tag, row);

  // The name prompt is a window.prompt; answer it before it is opened.
  const groupName = `Demo Group ${tag}`;
  await page.addInitScript(name => {
    window.prompt = () => name;
  }, groupName);

  await page.goto('/');
  await page.locator('.ws-map-tile').first().waitFor({ timeout: 15000 });

  // Multi-select the two workspaces the way a user does, then group them from
  // the checked tile's own context menu (#317 moved bulk actions there).
  // force: the building's SVG art sits over its own button and intercepts the
  // synthetic hit test; a real pointer lands on it either way.
  for (const id of [a, b]) {
    await page
      .locator(`.ws-map-tile[data-ws-id="${id}"]`)
      .click({ modifiers: ['Meta'], force: true });
  }
  await expect(page.locator('[data-ws-selbar-count]')).toHaveText('2 selected');
  await page.locator(`.ws-map-tile[data-ws-id="${a}"]`).click({ button: 'right', force: true });
  await page.getByRole('menuitem', { name: /^Group 2 workspaces$/ }).click();

  const district = page.locator(`.ws-map-district`).filter({ hasText: groupName });
  await district.waitFor({ timeout: 15000 });

  // FR-13: neither workspace moved.
  const layout = await (await page.request.get('/api/workspace-map/layout')).json();
  expect(layout.layout.positions[a]).toEqual({ x: 1520, y });
  expect(layout.layout.positions[b]).toEqual({ x: 1748, y });

  // FR-139/FR-140: the header names the group and counts it truthfully, with no
  // leftover glyphs.
  await expect(district.locator('.ws-map-district-name')).toHaveText(groupName);
  await expect(district.locator('.ws-map-district-count')).toHaveText('2 workspaces');
  await expect(district.locator('[data-group-drag]')).toHaveAttribute(
    'aria-label',
    `Move group: ${groupName}`
  );
  await expect(district.locator('[data-group-menu]')).toBeVisible();

  // FR-15/FR-16: the district is tight around the two members, not stretched to
  // an unrelated anchor.
  const frame = await district.evaluate(el => ({
    width: parseFloat((el as HTMLElement).style.width),
    height: parseFloat((el as HTMLElement).style.height)
  }));
  expect(frame.width).toBeLessThan(500);
  expect(frame.height).toBeLessThan(300);

  // FR-21: the new group is the active selection in Map and context.
  await expect(district.locator('.ws-map-district-tag')).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('#cockpitContextModal')).toBeVisible();
  await expect(page.locator('#cockpitRailContext')).toContainText(groupName);

  await page.screenshot({ path: 'test-results/346-grouping-home.png' });
  await page.keyboard.press('Escape');
  await expect(page.locator('#cockpitContextModal')).toBeHidden();

  // A legible capture of the header itself: 100% zoom, centred on the district.
  const canvas = page.locator('[data-ws-map-viewport]');
  await canvas.click({ button: 'right', position: { x: 40, y: 40 } });
  await page.getByRole('menuitem', { name: 'Reset view' }).click();
  await canvas.click({ button: 'right', position: { x: 40, y: 40 } });
  await page.getByRole('menuitem', { name: 'Center selected' }).click();
  await page.waitForTimeout(400);
  await canvas.screenshot({ path: 'test-results/346-grouping-header.png' });
});

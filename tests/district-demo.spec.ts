import { test, expect, Page } from '@playwright/test';

/**
 * Issue #346 group-1 demo: a populated district stays compact even when the
 * group's own saved anchor was left far from its members.
 *
 * Not part of CI. Run against the isolated demo server seeded by
 * ./scripts/seed-346-demo.sh:
 *   ./scripts/e2e.sh --port 8947 tests/district-demo.spec.ts
 *
 * It captures the evidence for the group's Demo: checkpoint on both Map
 * surfaces, and asserts the two facts the milestone is about — the district
 * frames only its members, and Fit all is not dragged out to the stale anchor.
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

/** The district frame and the world bounds the map resolved, straight from the engine. */
async function districtFacts(page: Page) {
  return page.evaluate(async () => {
    const map = (window as any).OriWorkspaceMap;
    const body = await (await fetch('/api/workspace-map/layout')).json();
    const list = await (await fetch('/api/workspaces')).json();
    const layout = map.computeWorldLayout(list.folders || list.workspaces || [], {
      positions: body.layout.positions,
      groupPresentations: body.layout.groups
    });
    const district = layout.districts.find((d: any) => d.ws && d.ws.name === 'Campaign Ops');
    return {
      district: district && {
        x: district.x,
        y: district.y,
        width: district.width,
        height: district.height,
        sizingMode: district.sizingMode,
        memberCount: district.memberCount
      },
      bounds: layout.bounds,
      staleAnchor: body.layout.positions[district ? district.id : ''] || null
    };
  });
}

for (const surface of [
  { name: 'home', path: '/' },
  { name: 'workspaces', path: '/workspaces?view=map' }
]) {
  test(`#346 district stays compact on ${surface.name}`, async ({ page }) => {
    await skipOnboarding(page);
    await page.goto(surface.path);
    await page.locator('.ws-map-district').first().waitFor({ timeout: 15000 });

    const facts = await districtFacts(page);
    expect(facts.district, 'the Campaign Ops district is drawn').toBeTruthy();
    expect(facts.district!.memberCount).toBe(3);

    // The members occupy (152,152)–(380,380); the group's own anchor was left at
    // y=4180. The frame must follow the members and ignore the anchor.
    expect(facts.staleAnchor!.y).toBe(4180);
    expect(facts.district!.y).toBeLessThan(200);
    expect(facts.district!.y + facts.district!.height).toBeLessThan(700);
    expect(facts.district!.sizingMode).toBe('auto');

    // And Fit all's content bounds are not dragged down to the stale anchor.
    expect(facts.bounds.maxY).toBeLessThan(1500);

    // Drive the real Fit all from the canvas context menu, so the screenshot
    // shows the camera a user would actually get rather than the opening view.
    const canvas = page.locator('[data-ws-map-viewport]');
    await canvas.click({ button: 'right', position: { x: 60, y: 60 } });
    await page.getByRole('menuitem', { name: 'Fit all' }).click();
    await page.waitForTimeout(500);

    // Fit all's promise is kept: the district and every building is on screen.
    // Before this feature the district stretched to the stale anchor 4000 units
    // below, so Fit all bottomed out at the 10% floor and still showed empty
    // world instead of the workspaces.
    const zoom = await page.evaluate(() => (window as any).OriWorkspaceMap.getCamera().zoom);
    expect(zoom).toBeGreaterThan(0.25);

    const canvasBox = (await canvas.boundingBox())!;
    const districtBox = (await page.locator('.ws-map-district').first().boundingBox())!;
    expect(districtBox.y).toBeGreaterThanOrEqual(canvasBox.y - 1);
    expect(districtBox.y + districtBox.height).toBeLessThanOrEqual(
      canvasBox.y + canvasBox.height + 1
    );
    for (const name of ['Launch Brief', 'Ad Copy', 'Budget Model', 'Unrelated Research']) {
      const box = (await page.locator('.ws-map-tile', { hasText: name }).first().boundingBox())!;
      expect(box.y, `${name} is on screen after Fit all`).toBeGreaterThanOrEqual(canvasBox.y - 1);
      expect(box.y + box.height).toBeLessThanOrEqual(canvasBox.y + canvasBox.height + 1);
    }

    await page.screenshot({
      path: `test-results/346-district-${surface.name}.png`,
      fullPage: false
    });

    // A second, legible capture at 100% so the frame itself can be inspected.
    await canvas.click({ button: 'right', position: { x: 60, y: 60 } });
    await page.getByRole('menuitem', { name: 'Reset view' }).click();
    await page.waitForTimeout(400);
    await canvas.screenshot({ path: `test-results/346-district-${surface.name}-100.png` });
  });
}

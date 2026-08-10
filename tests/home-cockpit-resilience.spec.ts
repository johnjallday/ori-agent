import { test, expect, Page } from '@playwright/test';

/**
 * Failure isolation and state retention for the Home cockpit
 * (PRD FR85, FR110, FR113, FR116, FR119, FR120, FR122; Success Metric 7).
 *
 * Not part of CI — run against an isolated demo server:
 *   ./scripts/demo-server.sh 8941
 *   ./scripts/e2e.sh --port 8941 tests/home-cockpit-resilience.spec.ts
 *
 * The point of these is the thing unit tests structurally cannot show: that one
 * broken source degrades ONLY itself, and that repeated interaction does not
 * quietly lose the user's place.
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

async function ensureWorkspace(page: Page): Promise<string> {
  const list = await (await page.request.get('/api/workspaces')).json();
  const rows = list.workspaces || list.folders || [];
  const existing = rows.find(
    (ws: { id?: string; kind?: string }) =>
      ws?.id && String(ws.kind || '').toLowerCase() !== 'group'
  );
  if (existing?.id) return existing.id;
  const res = await page.request.post('/api/workspaces', {
    data: { name: `Resilience spec ${Date.now()}`, workspace_preset: 'general' }
  });
  return (await res.json())?.folder?.id;
}

/** Every optional Today source, and what breaking it must NOT take down. */
const OPTIONAL_SOURCES = [
  { name: 'upcoming scheduled work', pattern: '**/api/orchestration/scheduled-tasks/upcoming*' },
  { name: 'recent activity', pattern: '**/api/activity/recent*' },
  { name: 'calendar ops', pattern: '**/api/calendar-ops/home-portal-summary*' },
  { name: 'progression', pattern: '**/api/progression*' },
  { name: 'personal HQ status', pattern: '**/api/personal-hq/status*' },
  { name: 'daily brief', pattern: '**/api/personal-hq/brief/**' }
];

test.describe('Home cockpit resilience', () => {
  test.beforeEach(async ({ page }) => {
    await skipOnboarding(page);
  });

  for (const source of OPTIONAL_SOURCES) {
    test(`a failing ${source.name} source leaves the cockpit usable`, async ({ page }) => {
      await ensureWorkspace(page);
      await page.route(source.pattern, route =>
        route.fulfill({ status: 500, body: 'induced failure' })
      );

      await page.goto('/');

      // The workspace area still loads and is interactive.
      await expect(page.locator('.ws-map-tile[data-ws-id]').first()).toBeVisible();
      await page.locator('.ws-map-tile[data-ws-id]').first().click();
      await expect(page.locator('[data-cockpit-rail-open]')).toBeVisible();

      // Ask Ori, the view toggle, and Quick Capture all still work.
      await expect(page.locator('#homeAssistantInput')).toBeEnabled();
      await expect(page.locator('#cockpitCaptureBtn')).toBeVisible();
      await page.locator('[data-cockpit-view="tree"]').click();
      await expect(page.locator('#cockpitTree')).toBeVisible();
    });
  }

  test('a failing workspace list offers Retry while Ask Ori stays usable', async ({ page }) => {
    let fail = true;
    await page.route('**/api/workspaces?tree=true', route => {
      if (fail) return route.fulfill({ status: 500, body: 'induced failure' });
      return route.continue();
    });

    await page.goto('/');

    // FR113: a bounded, retryable error — not a blank cockpit.
    await expect(page.locator('[data-cockpit-retry]')).toBeVisible();
    await expect(page.locator('#homeAssistantInput')).toBeEnabled();
    // The rail opens on demand, so what FR113 needs here is that it is still
    // REACHABLE while the list is failing — a broken fetch must not take the
    // rest of the cockpit down with it. Only click when it is actually closed:
    // a blind click would close a rail the fixture had already opened.
    const cockpit = page.locator('#homeCockpit');
    if ((await cockpit.getAttribute('data-rail-open')) !== 'true') {
      await page.locator('#cockpitRailToggle').click();
    }
    await expect(page.locator('#cockpitRailToday')).toBeVisible();

    // Retry recovers rather than requiring a reload.
    fail = false;
    await page.locator('[data-cockpit-retry]').click();
    await expect(page.locator('.ws-map-tile[data-ws-id]').first()).toBeVisible();
    await expect(page.locator('[data-cockpit-retry]')).toHaveCount(0);
  });

  test('the cockpit shell renders before optional Today data resolves', async ({ page }) => {
    await ensureWorkspace(page);
    // Hang every optional source indefinitely.
    for (const source of OPTIONAL_SOURCES) {
      await page.route(source.pattern, () => {});
    }

    const start = Date.now();
    await page.goto('/', { waitUntil: 'commit' });
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor({ timeout: 15000 });
    const mapReadyMs = Date.now() - start;

    // FR110: the Map must not wait on optional data. Generous bound — the point
    // is that it resolves at all while every optional source is still hanging.
    expect(mapReadyMs).toBeLessThan(10000);
    await page.locator('.ws-map-tile[data-ws-id]').first().click();
    await expect(page.locator('[data-cockpit-rail-open]')).toBeVisible();
  });

  test('repeated view toggles and selections never lose view, filter, or selection', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();

    await page.locator('[data-cockpit-signal="running"]').click();
    const targetId = await page
      .locator('.ws-map-tile[data-ws-id]')
      .first()
      .getAttribute('data-ws-id');
    await page.locator('.ws-map-tile[data-ws-id]').first().click();

    // FR119/FR122: hammer the view toggle; nothing may drift.
    for (let i = 0; i < 8; i++) {
      await page.locator('[data-cockpit-view="tree"]').click();
      await page.locator('[data-cockpit-view="map"]').click();
    }
    await page.waitForTimeout(500);

    const state = await page.evaluate(() => {
      const cockpit = window.OriHomeCockpit?.getState?.();
      return {
        view: cockpit?.view,
        signal: cockpit?.signal,
        selectedId: cockpit?.selectedId,
        railPanel: document.querySelector('[data-rail-panel]')?.getAttribute('data-rail-panel'),
        // FR122: listeners must be idempotent, so the toggle must not have
        // accumulated duplicate map mounts.
        mapCanvases: document.querySelectorAll('#cockpitMap .ws-map-canvas').length,
        trees: document.querySelectorAll('#cockpitTree [role="tree"]').length
      };
    });

    expect(state.view).toBe('map');
    expect(state.signal).toBe('running');
    expect(state.selectedId).toBe(targetId);
    expect(state.railPanel).toBe('workspace');
    expect(state.mapCanvases).toBe(1);
    expect(state.trees).toBeLessThanOrEqual(1);
    await expect(page.locator('[data-cockpit-signal="running"]')).toHaveAttribute(
      'aria-pressed',
      'true'
    );
  });

  test('Tree expansion and bulk selection survive repeated view switches', async ({ page }) => {
    await ensureWorkspace(page);
    await page.goto('/?view=tree');
    await page.locator('[data-tree-row]').first().waitFor();

    // Take a bulk selection, then bounce through Map several times.
    await page.locator('[data-tree-check]').first().click();
    await expect(page.locator('[data-tree-bulkbar]')).toBeVisible();

    for (let i = 0; i < 5; i++) {
      await page.locator('[data-cockpit-view="map"]').click();
      await page.locator('[data-cockpit-view="tree"]').click();
    }
    await page.waitForTimeout(400);

    // FR55: bulk selection is current-session state and must survive.
    await expect(page.locator('[data-tree-check]:checked')).toHaveCount(1);
    await expect(page.locator('[data-tree-bulkbar]')).toBeVisible();
    // Still exactly one tree, one tabbable row.
    await expect(page.locator('#cockpitTree [role="tree"]')).toHaveCount(1);
    await expect(page.locator('[data-tree-row][tabindex="0"]')).toHaveCount(1);
  });

  test('a deleted selection returns the rail to Today with an announcement', async ({ page }) => {
    // Create a throwaway workspace, select it, then delete it out from under
    // the cockpit and force the refresh it would get from a mutation (FR73).
    const res = await page.request.post('/api/workspaces', {
      data: { name: `Doomed ${Date.now()}`, workspace_preset: 'general' }
    });
    const doomed = (await res.json())?.folder?.id;
    expect(doomed).toBeTruthy();

    await page.goto('/');
    const tile = page.locator(`.ws-map-tile[data-ws-id="${doomed}"]`);
    await tile.waitFor();
    await tile.click();
    await expect(page.locator('[data-cockpit-rail-open]')).toBeVisible();

    await page.request.delete(`/api/workspaces/${doomed}?confirm=true`);
    await page.evaluate(() => window.OriHomeCockpit?.refreshQuietly?.());
    await page.waitForTimeout(1200);

    await expect(page.locator('#cockpitRailToday')).toBeVisible();
    await expect(page.locator('#cockpitRailLive')).toContainText(/no longer available/i);
  });
});

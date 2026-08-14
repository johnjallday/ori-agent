import { test, expect, Page } from '@playwright/test';

/**
 * Behavior suite for the Map-first Home cockpit
 * (tasks/prd-home-workspace-cockpit.md).
 *
 * Not part of CI — run against an isolated demo server:
 *   ./scripts/demo-server.sh 8941
 *   ./scripts/e2e.sh --port 8941 tests/home-workspace-cockpit.spec.ts
 *
 * These cover the contracts that unit tests cannot: that the pieces are
 * actually wired to each other in a real page, that selection never navigates,
 * and that nothing covers the cockpit when it should not.
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

test.describe('Home onboarding workspace gate', () => {
  test('pending onboarding makes no workspace request and reveals no seeded workspace', async ({
    page
  }) => {
    const seededName = 'Foreign workspace must stay hidden';
    let workspaceTreeRequests = 0;
    const workspaceDerivedRequests: string[] = [];
    const workspaceDerivedPaths = new Set([
      '/api/activity/recent',
      '/api/calendar-ops/home-portal-summary',
      '/api/orchestration/scheduled-tasks/upcoming',
      '/api/personal-hq/status',
      '/api/progression'
    ]);

    page.on('request', request => {
      const url = new URL(request.url());
      if (url.pathname === '/api/workspaces' && url.searchParams.get('tree') === 'true') {
        workspaceTreeRequests += 1;
        workspaceDerivedRequests.push(`${url.pathname}${url.search}`);
      } else if (workspaceDerivedPaths.has(url.pathname)) {
        workspaceDerivedRequests.push(`${url.pathname}${url.search}`);
      }
    });
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          needs_onboarding: true,
          current_step: 0,
          completed: false,
          skipped: false,
          steps_completed: [],
          user_name: '',
          assistant_name: 'Ori'
        })
      })
    );
    await page.route('**/api/workspaces?tree=true', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          folders: [{ id: 'foreign-workspace', name: seededName, kind: 'workspace' }]
        })
      })
    );

    await page.goto('/');

    await expect(page.locator('#onboardingModal')).toBeVisible();
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-state', 'onboarding-required');
    await expect(page.getByText(seededName)).toHaveCount(0);
    await expect(page.locator('.ws-map-tile[data-ws-id="foreign-workspace"]')).toHaveCount(0);
    expect(workspaceTreeRequests).toBe(0);
    expect(workspaceDerivedRequests).toEqual([]);
  });

  test('authoritative empty renders the real Map and routes both canvas actions', async ({
    page
  }) => {
    let populated = false;
    const seeded = {
      id: 'arrived-after-empty',
      name: 'Arrived workspace',
      kind: 'workspace',
      children: []
    };
    await skipOnboarding(page);
    await page.route('**/api/workspaces?tree=true', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ folders: populated ? [seeded] : [] })
      })
    );
    await page.route('**/api/personal-hq/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ status: { valid: true, workspace_id: 'existing-hq' } })
      })
    );

    await page.goto('/');

    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-state', 'empty-map');
    await expect(page.locator('#cockpitMap')).toBeVisible();
    await expect(page.locator('.ws-map-canvas[data-ws-map-viewport]')).toHaveCount(1);
    await expect(page.locator('.ws-map-tile[data-ws-id]')).toHaveCount(0);
    await expect(page.getByText('No workspaces yet.', { exact: true })).toHaveCount(0);
    await expect(page.getByText(/No workspaces yet — build your first one/)).toHaveCount(0);

    const actions = page.locator('.cockpit-empty-map-actions');
    await expect(actions).toHaveCount(1);
    const create = actions.getByRole('button', { name: 'New Workspace' });
    const importFolder = actions.getByRole('button', { name: 'Import Folder' });
    await create.click();
    await expect(page.locator('#addFolderModal')).toBeVisible();
    await expect(page.locator('#addFolderModal')).toHaveAttribute('data-import-mode', 'false');
    expect(await page.evaluate(() => (window as any).sessionManager?.importEntryPoint)).toBe(
      'home_cockpit_create'
    );
    await page.locator('#addFolderModal [data-bs-dismiss="modal"]').first().click();
    await expect(page.locator('#addFolderModal')).toBeHidden();

    await importFolder.click();
    await expect(page.locator('#addFolderModal')).toBeVisible();
    await expect(page.locator('#addFolderModal')).toHaveAttribute('data-import-mode', 'true');
    expect(await page.evaluate(() => (window as any).sessionManager?.importEntryPoint)).toBe(
      'home_cockpit_import'
    );
    await page.locator('#addFolderModal [data-bs-dismiss="modal"]').first().click();
    await expect(page.locator('#addFolderModal')).toBeHidden();

    await page.getByRole('button', { name: 'Tree' }).click();
    await expect(actions).toBeHidden();
    await expect(page.locator('#cockpitTree')).toContainText('No workspaces yet');
    await page.getByRole('button', { name: 'Map' }).click();

    populated = true;
    await page.evaluate(() => window.dispatchEvent(new Event('ori:workspaces-changed')));
    await expect(page.locator(`.ws-map-tile[data-ws-id="${seeded.id}"]`)).toBeVisible();
    await expect(actions).toHaveCount(0);
    await expect(page.locator('.ws-map-canvas[data-ws-map-viewport]')).toHaveCount(1);
  });

  test('a late Personal HQ response preserves one empty action group and one canvas', async ({
    page
  }) => {
    await skipOnboarding(page);
    await page.route('**/api/workspaces?tree=true', route =>
      route.fulfill({ status: 200, contentType: 'application/json', body: '{"folders":[]}' })
    );
    await page.route('**/api/personal-hq/status', async route => {
      await new Promise(resolve => setTimeout(resolve, 150));
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ status: { valid: false, hq_onboarding_state: 'unseen' } })
      });
    });

    await page.goto('/');

    await expect(page.locator('[data-hq-site]')).toBeVisible();
    await expect(page.locator('.cockpit-empty-map-actions')).toHaveCount(1);
    await expect(page.locator('.ws-map-canvas[data-ws-map-viewport]')).toHaveCount(1);
    await page.locator('[data-hq-site]').click();
    await expect(page.getByRole('button', { name: /Build My HQ/i })).toBeVisible();
  });

  test('completed onboarding hydrates and renders the seeded workspace Map', async ({ page }) => {
    const seeded = {
      id: 'onboarding-gate-seeded',
      name: 'Ready workspace',
      kind: 'workspace',
      status: 'idle',
      agent_count: 0,
      open_task_count: 0,
      children: []
    };
    let workspaceTreeRequests = 0;

    page.on('request', request => {
      const url = new URL(request.url());
      if (url.pathname === '/api/workspaces' && url.searchParams.get('tree') === 'true') {
        workspaceTreeRequests += 1;
      }
    });
    await skipOnboarding(page);
    await page.route('**/api/workspaces?tree=true', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ folders: [seeded] })
      })
    );

    await page.goto('/');

    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-state', 'ready');
    await expect(
      page.locator(`.ws-map-tile[data-ws-id="${seeded.id}"] .ws-map-tile-name`)
    ).toHaveText(seeded.name);
    expect(workspaceTreeRequests).toBe(1);
  });
});

/**
 * Ensure at least one CONCRETE workspace exists so the Map has a tile to draw.
 *
 * Groups must be excluded: they render as district tags, not `.ws-map-tile`,
 * so returning one produces a confusing "tile never appeared" timeout that
 * looks like a product failure.
 */
async function ensureWorkspace(page: Page): Promise<string> {
  const list = await (await page.request.get('/api/workspaces')).json();
  const rows = list.workspaces || list.folders || [];
  const existing = rows.find(
    (ws: { id?: string; kind?: string }) =>
      ws?.id && String(ws.kind || '').toLowerCase() !== 'group'
  );
  if (existing?.id) return existing.id;
  const res = await page.request.post('/api/workspaces', {
    data: { name: `Cockpit spec ${Date.now()}`, workspace_preset: 'general' }
  });
  return (await res.json())?.folder?.id;
}

/**
 * Open the CONTEXT rail the only way it opens now: a real selection
 * (Issue #334 retired the old rail toggle — `#cockpitRailToggle` opens the
 * Updates flyout instead, and never the rail). No-ops if a fixture already
 * has a selection.
 */
async function openContextRailViaSelection(page: Page) {
  const cockpit = page.locator('#homeCockpit');
  if ((await cockpit.getAttribute('data-rail-open')) === 'true') return;
  await page.locator('.ws-map-tile[data-ws-id]').first().click();
  await expect(cockpit).toHaveAttribute('data-rail-open', 'true');
}

test.describe('Home workspace cockpit', () => {
  test.beforeEach(async ({ page }) => {
    await skipOnboarding(page);
  });

  test('renders the cockpit shell full-width, with no flyout open on a bare load', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await expect(page.locator('#homeCockpit')).toBeVisible();
    await expect(page.locator('#homeAssistantInput')).toBeVisible();
    await expect(page.locator('#cockpitMap')).toBeVisible();
    await expect(page.locator('#cockpitTree')).toBeHidden();
    await expect(page.locator('[data-cockpit-view="map"]')).toHaveAttribute('aria-pressed', 'true');

    // FR41-FR42: a bare Home load keeps the context rail closed.
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-rail-open', 'false');
    await expect(page.locator('#cockpitRail')).toBeHidden();
    // FR12: neither header flyout opens itself on load.
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeHidden();
    await expect(page.locator('#cockpitRailToggle')).toHaveAttribute('aria-expanded', 'false');

    // FR22/FR29: Updates is an overlay, never a layout column — opening and
    // closing it must not change the measured width of the workspace area.
    const baseWidth = (await page.locator('.cockpit-workspace-area').boundingBox())!.width;
    await page.locator('#cockpitRailToggle').click();
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeVisible();
    const openWidth = (await page.locator('.cockpit-workspace-area').boundingBox())!.width;
    expect(openWidth).toBe(baseWidth);

    await page.locator('#cockpitUpdatesFlyout [data-cockpit-flyout-close]').click();
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeHidden();
    const closedWidth = (await page.locator('.cockpit-workspace-area').boundingBox())!.width;
    expect(closedWidth).toBe(baseWidth);

    // FR22: the retired Operations Board must not render anywhere.
    await expect(page.locator('#homeDashboardSections')).toHaveCount(0);
  });

  test('selecting a workspace opens the context rail and narrows the workspace area; clearing restores full width', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();
    const baseWidth = (await page.locator('.cockpit-workspace-area').boundingBox())!.width;

    await page.locator('.ws-map-tile[data-ws-id]').first().click();
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-rail-open', 'true');
    await expect(page.locator('#cockpitRailContext')).toBeVisible();
    const openWidth = (await page.locator('.cockpit-workspace-area').boundingBox())!.width;
    expect(openWidth).toBeLessThan(baseWidth);

    await page.keyboard.press('Escape');
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-rail-open', 'false');
    const closedWidth = (await page.locator('.cockpit-workspace-area').boundingBox())!.width;
    expect(closedWidth).toBe(baseWidth);
  });

  test('Updates trigger exposes accurate ARIA/label and reveals real Update sections when opened', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();

    const trigger = page.locator('#cockpitRailToggle');
    await expect(trigger).toHaveText(/Updates/);
    await expect(trigger).toHaveAttribute('aria-controls', 'cockpitUpdatesFlyout');
    await expect(trigger).toHaveAttribute('aria-expanded', 'false');
    await expect(trigger).toHaveAccessibleName(/Updates/);

    await trigger.click();
    await expect(trigger).toHaveAttribute('aria-expanded', 'true');
    const flyout = page.locator('#cockpitUpdatesFlyout');
    await expect(flyout).toBeVisible();
    await expect(flyout.getByRole('heading', { name: 'Updates' })).toBeVisible();
    // Real Update sections render inside the flyout now, not the retired rail.
    await expect(flyout.locator('#homeRecentActivity')).toBeVisible();
    await expect(page.locator('#cockpitRailContext #homeRecentActivity')).toHaveCount(0);

    // Activating the SAME trigger again closes it (FR7) and returns focus.
    await trigger.click();
    await expect(flyout).toBeHidden();
    await expect(trigger).toHaveAttribute('aria-expanded', 'false');
    await expect(trigger).toBeFocused();
  });

  test('a positive attention count shows the Updates badge but never opens the flyout on load (FR15-FR17)', async ({
    page
  }) => {
    await page.route('**/api/workspaces?tree=true', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          folders: [
            {
              id: 'attention-seed',
              name: 'Needs attention workspace',
              kind: 'workspace',
              needs_attention_count: 2,
              children: []
            }
          ]
        })
      })
    );
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();

    // The count is how many workspaces need attention, not a sum of each
    // workspace's own count — one seeded workspace reads as "1".
    const badge = page.locator('[data-cockpit-rail-toggle-count]');
    await expect(badge).toHaveText('1');
    await expect(badge).toBeVisible();
    // The badge is informational only — a positive count must never open the
    // flyout on its own.
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeHidden();
    await expect(page.locator('#cockpitRailToggle')).toHaveAttribute('aria-expanded', 'false');
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-rail-open', 'false');
  });

  test('opening Updates over a selected workspace does not remount the Map or disturb the selection', async ({
    page
  }) => {
    await ensureWorkspace(page);
    let treeRequests = 0;
    await page.route('**/api/workspaces?tree=true', async route => {
      treeRequests += 1;
      await route.continue();
    });
    await page.goto('/');
    const tile = page.locator('.ws-map-tile[data-ws-id]').first();
    await tile.waitFor();
    await tile.click();
    await expect(page.locator('#cockpitRailContext')).toBeVisible();
    const selectedId = await tile.getAttribute('data-ws-id');
    const requestsBeforeOpen = treeRequests;

    await page.locator('#cockpitRailToggle').click();
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeVisible();
    // An overlay flyout must never clear or falsify a real selection (FR22,
    // FR46) — the same context stays visible underneath it.
    await expect(page.locator('#cockpitRailContext')).toBeVisible();
    await expect(page.locator(`.ws-map-tile[data-ws-id="${selectedId}"]`)).toHaveClass(
      /is-selected/
    );
    expect(treeRequests).toBe(requestsBeforeOpen);

    await page.locator('#cockpitUpdatesFlyout [data-cockpit-flyout-close]').click();
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeHidden();
    await expect(page.locator('#cockpitRailContext')).toBeVisible();
    await expect(page.locator(`.ws-map-tile[data-ws-id="${selectedId}"]`)).toHaveClass(
      /is-selected/
    );
  });

  test('a Map click selects without navigating, however often it is repeated', async ({ page }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    const site = page.locator('.ws-map-tile[data-ws-id]').first();
    await site.waitFor();

    await site.click();
    await expect(page.locator('[data-cockpit-rail-open]')).toBeVisible();
    expect(new URL(page.url()).pathname).toBe('/');

    // FR36: repeated clicks and a double-click must not become a hidden open.
    await site.click();
    await site.click();
    await site.dblclick();
    await page.waitForTimeout(400);
    expect(new URL(page.url()).pathname).toBe('/');
  });

  test('Open Workspace navigates and Enter opens the focused site', async ({ page }) => {
    const id = await ensureWorkspace(page);
    await page.goto('/');
    const site = page.locator(`.ws-map-tile[data-ws-id="${id}"]`);
    await site.waitFor();
    await site.click();

    const open = page.locator('[data-cockpit-rail-open]');
    await expect(open).toHaveAttribute('href', `/workspaces/${id}`);
    await open.click();
    await page.waitForURL(`**/workspaces/${id}`);

    // FR125: Enter on a focused site opens it explicitly.
    await page.goto('/');
    await page.locator(`.ws-map-tile[data-ws-id="${id}"]`).focus();
    await page.keyboard.press('Enter');
    await page.waitForURL(`**/workspaces/${id}`);
  });

  test('the Map/Tree toggle swaps peer views without adding history entries', async ({ page }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();
    const historyBefore = await page.evaluate(() => history.length);

    await page.locator('[data-cockpit-view="tree"]').click();
    await expect(page.locator('#cockpitTree')).toBeVisible();
    await expect(page.locator('#cockpitMap')).toBeHidden();
    expect(page.url()).toContain('view=tree');

    await page.locator('[data-cockpit-view="map"]').click();
    await expect(page.locator('#cockpitMap')).toBeVisible();
    expect(page.url()).not.toContain('view=');

    // FR13: a view toggle must not spam the back button.
    expect(await page.evaluate(() => history.length)).toBe(historyBefore);
  });

  test('Escape returns Home to Today (rail closed) without clearing Map filters', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().click();
    await expect(page.locator('#cockpitRailContext')).toBeVisible();

    await page.locator('[data-cockpit-signal="running"]').click();
    await page.keyboard.press('Escape');

    // Today has no rail content of its own now (Issue #334) — returning to it
    // closes the rail entirely rather than showing a Today panel inside it.
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-rail-open', 'false');
    await expect(page.locator('#cockpitRail')).toBeHidden();
    // FR61: returning to Today clears selection but NOT the signal filter.
    await expect(page.locator('[data-cockpit-signal="running"]')).toHaveAttribute(
      'aria-pressed',
      'true'
    );
  });

  test('the legacy launcher route redirects to Home preserving intent', async ({ page }) => {
    await page.goto('/workspaces?view=tree');
    await expect(page.locator('#homeCockpit')).toBeVisible();
    expect(page.url()).toContain('view=tree');
    await expect(page.locator('#cockpitTree')).toBeVisible();

    // FR6: the retired Cards view lands on Map, never resurrected.
    await page.goto('/workspaces?view=cards');
    await expect(page.locator('#cockpitMap')).toBeVisible();
    expect(page.url()).not.toContain('cards');
  });

  test('primary navigation offers Home and no separate Workspaces item', async ({ page }) => {
    await page.goto('/');
    const nav = page.locator('.ori-navbar-links .nav-link-item');
    await expect(nav.filter({ hasText: 'Home' })).toHaveCount(1);
    await expect(page.locator('.ori-navbar-links a[href="/workspaces"]')).toHaveCount(0);
    await expect(nav.filter({ hasText: 'Home' })).toHaveAttribute('aria-current', 'page');
  });

  test('?create=1 opens Create Workspace and scrubs the one-shot query', async ({ page }) => {
    await page.goto('/?create=1');
    await expect(page.locator('#addFolderModal')).toBeVisible();
    // FR106: the one-shot parameter is consumed so a refresh does not re-open.
    expect(page.url()).not.toContain('create=1');
  });

  test('Ask Ori activity renders in the rail, never as a blocking modal', async ({ page }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();

    await page.locator('#homeAssistantInput').fill('What needs attention today?');
    await page.locator('#homeAssistantSendBtn').click();
    await expect(page.locator('#homeAssistantThinkingModal')).toBeVisible();

    // FR96/FR92: no backdrop, and the Map is still the element on top.
    await expect(page.locator('.modal-backdrop')).toHaveCount(0);
    await expect(page.locator('#cockpitMap')).toBeVisible();
    const mapOnTop = await page.evaluate(() => {
      const rect = document.getElementById('cockpitMap')?.getBoundingClientRect();
      if (!rect) return false;
      const el = document.elementFromPoint(rect.x + rect.width / 2, rect.y + 60);
      return !!el?.closest('#cockpitMap');
    });
    expect(mapOnTop).toBe(true);
  });

  test('Tree carries the management toolset with real tree semantics', async ({ page }) => {
    await ensureWorkspace(page);
    await page.goto('/?view=tree');
    await page.locator('[data-tree-row]').first().waitFor();

    await expect(page.locator('[role="tree"]')).toHaveCount(1);
    await expect(page.locator('[role="treeitem"]').first()).toBeVisible();
    // FR127: exactly one row is tabbable (roving tabindex).
    await expect(page.locator('[data-tree-row][tabindex="0"]')).toHaveCount(1);
    // FR40/FR51: management actions and a non-drag Move path are present.
    for (const label of ['Create Workspace', 'New Group', 'Import Folder', 'Rescan']) {
      await expect(page.getByRole('button', { name: label })).toBeVisible();
    }
    await expect(page.locator('[data-tree-move]').first()).toBeVisible();
  });

  test('layout is side-by-side when wide and stacked when narrow, never h-scrolling', async ({
    page
  }) => {
    await ensureWorkspace(page);

    for (const [width, height] of [
      [1440, 950],
      [1100, 900],
      [900, 900],
      [600, 900]
    ] as const) {
      await page.setViewportSize({ width, height });
      await page.goto('/');
      await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();

      // The rail's geometry only exists once it is open, and it opens ONLY on
      // a real selection now (Issue #334). This test is about where the rail
      // sits relative to the workspace area, not about when it appears.
      await openContextRailViaSelection(page);
      await expect(page.locator('#cockpitRail')).toBeVisible();

      const layout = await page.evaluate(() => {
        const area = document.querySelector('.cockpit-workspace-area')?.getBoundingClientRect();
        const rail = document.getElementById('cockpitRail')?.getBoundingClientRect();
        return {
          areaX: area?.x ?? 0,
          areaW: area?.width ?? 0,
          areaY: area?.y ?? 0,
          railX: rail?.x ?? 0,
          railW: rail?.width ?? 0,
          railY: rail?.y ?? 0,
          pageWidth: document.documentElement.scrollWidth,
          viewportWidth: window.innerWidth
        };
      });

      // FR135: no full-page horizontal scrolling at any supported width.
      expect(layout.pageWidth, `page scrolls horizontally at ${width}px`).toBeLessThanOrEqual(
        layout.viewportWidth + 1
      );

      if (width >= 1200) {
        // FR16: side by side, with the workspace area taking the majority.
        expect(layout.railX).toBeGreaterThan(layout.areaX + layout.areaW - 5);
        expect(layout.areaW).toBeGreaterThan(layout.railW);
      } else if (width <= 900) {
        // FR134: stacked, workspace area above the context rail.
        expect(layout.railY).toBeGreaterThan(layout.areaY);
      }
    }
  });

  test('Add to backlog and Summary are reachable from every rail state', async ({ page }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();

    // Today
    await expect(page.locator('#cockpitCaptureBtn')).toBeVisible();
    await expect(page.locator('#cockpitSummaryBtn')).toBeVisible();

    // Workspace context
    await page.locator('.ws-map-tile[data-ws-id]').first().click();
    await expect(page.locator('#cockpitCaptureBtn')).toBeVisible();
    await expect(page.locator('#cockpitSummaryBtn')).toBeVisible();

    // Summary
    await page.locator('#cockpitSummaryBtn').click();
    await expect(page.locator('[data-rail-panel="summary"]')).toBeVisible();
    await expect(page.locator('#cockpitCaptureBtn')).toBeVisible();
  });
});

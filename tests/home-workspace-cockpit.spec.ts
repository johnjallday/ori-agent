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

/**
 * Group 3 (Issue #334): Updates, Quests, and Quick Capture share ONE
 * transient header-panel state, entirely separate from the context rail's
 * own (selection/Summary/Ask Ori) state. These tests exercise the seams
 * between the two — mutual exclusion among the three triggers, stability
 * across Map/Tree and selection changes, and coexistence with the rail.
 */
test.describe('Header disclosure coordination', () => {
  test.beforeEach(async ({ page }) => {
    await skipOnboarding(page);
    await page.route('**/api/progression', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          current_tier: 1,
          total_tiers: 3,
          total_count: 2,
          tiers: [
            {
              tier: 1,
              name: 'First contact',
              quests: [
                { id: 'coord-q1', title: 'Say hello to Ori', status: 'pending' },
                { id: 'coord-q2', title: 'Create a workspace', status: 'pending' }
              ]
            }
          ]
        })
      })
    );
  });

  test("Updates, Quests, and Quick Capture are mutually exclusive and never clear Quick Capture's draft (FR8-FR9)", async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('#cockpitQuestsToggle').waitFor({ state: 'visible' });

    const updatesBtn = page.locator('#cockpitRailToggle');
    const questsBtn = page.locator('#cockpitQuestsToggle');
    const captureBtn = page.locator('#cockpitCaptureBtn');
    const updatesFlyout = page.locator('#cockpitUpdatesFlyout');
    const questsFlyout = page.locator('#cockpitQuestsFlyout');
    const capturePanel = page.locator('#cockpitCapturePanel');

    await updatesBtn.click();
    await expect(updatesFlyout).toBeVisible();
    await expect(questsFlyout).toBeHidden();
    await expect(capturePanel).toBeHidden();

    // Opening Quests closes Updates (FR8).
    await questsBtn.click();
    await expect(questsFlyout).toBeVisible();
    await expect(updatesFlyout).toBeHidden();
    await expect(updatesBtn).toHaveAttribute('aria-expanded', 'false');

    // Opening Quick Capture closes Quests (FR9), and the reverse.
    await captureBtn.click();
    await expect(capturePanel).toBeVisible();
    await expect(questsFlyout).toBeHidden();
    await page.locator('#cockpitCaptureTitle').fill('Draft that must survive');

    await updatesBtn.click();
    await expect(updatesFlyout).toBeVisible();
    await expect(capturePanel).toBeHidden();

    // Reopening Quick Capture must not have lost the draft (FR9).
    await captureBtn.click();
    await expect(capturePanel).toBeVisible();
    await expect(page.locator('#cockpitCaptureTitle')).toHaveValue('Draft that must survive');
  });

  test('the same-trigger toggle and Escape close their panel and return focus to the trigger', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    const questsBtn = page.locator('#cockpitQuestsToggle');
    await questsBtn.waitFor({ state: 'visible' });

    await questsBtn.click();
    await expect(page.locator('#cockpitQuestsFlyout')).toBeVisible();
    // FR7: activating the SAME trigger again closes it.
    await questsBtn.click();
    await expect(page.locator('#cockpitQuestsFlyout')).toBeHidden();
    await expect(questsBtn).toBeFocused();

    await questsBtn.click();
    await expect(page.locator('#cockpitQuestsFlyout')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.locator('#cockpitQuestsFlyout')).toBeHidden();
    await expect(questsBtn).toBeFocused();
  });

  test('Escape closes an open flyout first, and needs a second press to clear a selection underneath it', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().click();
    await expect(page.locator('#cockpitRailContext')).toBeVisible();

    await page.locator('#cockpitRailToggle').click();
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeVisible();

    await page.keyboard.press('Escape');
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeHidden();
    // The selection/context rail is untouched by the first Escape.
    await expect(page.locator('#cockpitRailContext')).toBeVisible();
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-rail-open', 'true');

    await page.keyboard.press('Escape');
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-rail-open', 'false');
  });

  test('an open flyout survives a Map/Tree view switch without ever auto-opening on its own', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('#cockpitQuestsToggle').waitFor({ state: 'visible' });

    await page.locator('#cockpitQuestsToggle').click();
    await expect(page.locator('#cockpitQuestsFlyout')).toBeVisible();

    await page.locator('[data-cockpit-view="tree"]').click();
    await expect(page.locator('#cockpitTree')).toBeVisible();
    await expect(page.locator('#cockpitQuestsFlyout')).toBeVisible();
    await expect(page.locator('[data-role="progress-count"]')).toHaveText('0/2');

    await page.locator('[data-cockpit-view="map"]').click();
    await expect(page.locator('#cockpitMap')).toBeVisible();
    await expect(page.locator('#cockpitQuestsFlyout')).toBeVisible();

    // Closing, then switching views again, must never reopen it on its own.
    await page.locator('#cockpitQuestsToggle').click();
    await expect(page.locator('#cockpitQuestsFlyout')).toBeHidden();
    await page.locator('[data-cockpit-view="tree"]').click();
    await expect(page.locator('#cockpitQuestsFlyout')).toBeHidden();
  });

  test('changing the selected workspace while Updates is open never closes it or loses the new selection', async ({
    page
  }) => {
    const first = await ensureWorkspace(page);
    const res = await page.request.post('/api/workspaces', {
      data: { name: `Cockpit coordination ${Date.now()}`, workspace_preset: 'general' }
    });
    const second = (await res.json())?.folder?.id;

    await page.goto('/');
    await page.locator(`.ws-map-tile[data-ws-id="${first}"]`).click();
    await expect(page.locator('#cockpitRailContext')).toContainText(await titleOf(page, first));

    await page.locator('#cockpitRailToggle').click();
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeVisible();

    // Reselect via Tree rather than the Map: on a map with many accumulated
    // sites, an auto-placed tile can legitimately land underneath the
    // top-anchored flyout, which is expected overlay behavior (FR21) rather
    // than something this test should fight. Tree's row list is unaffected.
    await page.locator('[data-cockpit-view="tree"]').click();
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeVisible();
    await page.locator(`[data-tree-row="${second}"]`).click();
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeVisible();
    await expect(page.locator(`.ws-map-tile[data-ws-id="${second}"]`)).toHaveClass(/is-selected/);
    await expect(page.locator(`.ws-map-tile[data-ws-id="${first}"]`)).not.toHaveClass(
      /is-selected/
    );
  });

  test('Summary and an open flyout coexist; closing the flyout reveals Summary unchanged', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();

    await page.locator('#cockpitSummaryBtn').click();
    await expect(page.locator('[data-rail-panel="summary"]')).toBeVisible();

    await page.locator('#cockpitQuestsToggle').click();
    await expect(page.locator('#cockpitQuestsFlyout')).toBeVisible();
    // Opening a flyout must not disturb the rail's own state (FR29, FR41).
    await expect(page.locator('[data-rail-panel="summary"]')).toBeVisible();
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-rail-open', 'true');

    await page.locator('#cockpitQuestsToggle').click();
    await expect(page.locator('#cockpitQuestsFlyout')).toBeHidden();
    await expect(page.locator('[data-rail-panel="summary"]')).toBeVisible();
  });

  test('a background workspace refresh updates Updates in place without moving focus or closing it', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('#cockpitRailToggle').click();
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeVisible();
    await page.locator('#cockpitRailToggle').focus();

    await page.evaluate(() => window.dispatchEvent(new Event('ori:workspaces-changed')));
    await page.waitForTimeout(150);

    await expect(page.locator('#cockpitUpdatesFlyout')).toBeVisible();
    await expect(page.locator('#cockpitRailToggle')).toBeFocused();
  });
});

/**
 * Group 4 (Issue #334): responsive, accessible, and regression hardening —
 * narrow-width sheet presentation, keyboard reachability, and confirming
 * neither control leaked outside the Home cockpit header.
 */
test.describe('Responsive and regression hardening', () => {
  test.beforeEach(async ({ page }) => {
    await skipOnboarding(page);
  });

  test('at a narrow width Updates becomes a full-width sheet without horizontal overflow, and stays dismissible', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.setViewportSize({ width: 480, height: 800 });
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();

    await page.locator('#cockpitRailToggle').click();
    const flyout = page.locator('#cockpitUpdatesFlyout');
    await expect(flyout).toBeVisible();

    const layout = await page.evaluate(() => ({
      pageWidth: document.documentElement.scrollWidth,
      viewportWidth: window.innerWidth
    }));
    expect(layout.pageWidth).toBeLessThanOrEqual(layout.viewportWidth + 1);

    // The close control must stay reachable and dismiss the sheet.
    const close = page.locator('#cockpitUpdatesFlyout [data-cockpit-flyout-close]');
    await expect(close).toBeVisible();
    await close.click();
    await expect(flyout).toBeHidden();
  });

  test('neither Updates nor Quests appears in global navigation, a workspace tile, or the Personal HQ landmark', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();

    // Exactly one of each, both inside the Home cockpit header — never
    // duplicated into the navbar, a workspace tile, or a group/HQ landmark
    // (FR5).
    await expect(page.locator('#cockpitRailToggle')).toHaveCount(1);
    await expect(page.locator('.cockpit-area-header-zone #cockpitRailToggle')).toHaveCount(1);
    await expect(page.locator('.ori-navbar-links [id="cockpitRailToggle"]')).toHaveCount(0);
    await expect(page.locator('.ws-map-tile [id="cockpitRailToggle"]')).toHaveCount(0);
    await expect(page.locator('[data-hq-site] [id="cockpitRailToggle"]')).toHaveCount(0);
    await expect(page.locator('.ori-navbar-links [id="cockpitQuestsToggle"]')).toHaveCount(0);
    await expect(page.locator('.ws-map-tile [id="cockpitQuestsToggle"]')).toHaveCount(0);
  });

  test('the shared /workspaces Map never renders either header control', async ({ page }) => {
    // /workspaces redirects to Home, so this confirms the redirect target is
    // the only place these controls exist — there is no separate, older Map
    // surface that could carry a stale copy.
    await page.goto('/workspaces');
    await expect(page).toHaveURL(/\/(\?.*)?$/);
    await expect(page.locator('#cockpitRailToggle')).toHaveCount(1);
    await expect(page.locator('#cockpitQuestsToggle')).toHaveCount(1);
  });

  test('Updates and Quests are keyboard-reachable in document order after the guide and before the view toggle', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();

    const order = await page.evaluate(() => {
      const ids = [
        'oriGuideMapTrigger',
        'cockpitRailToggle',
        'cockpitQuestsToggle',
        'cockpitViewMap'
      ];
      return ids.map(id => {
        const el = document.getElementById(id);
        if (!el) return -1;
        // Position among all elements, to compare relative document order.
        return Array.from(document.querySelectorAll('*')).indexOf(el);
      });
    });
    const [guide, updates, quests, viewToggle] = order;
    expect(guide).toBeGreaterThan(-1);
    expect(updates).toBeGreaterThan(guide);
    expect(quests).toBeGreaterThan(updates);
    expect(viewToggle).toBeGreaterThan(quests);

    // And genuinely Tab-reachable: focusing the guide, then Tab, lands on
    // Updates next (FR53 — logical DOM order backs a logical focus order).
    await page.locator('#oriGuideMapTrigger').focus();
    await page.keyboard.press('Tab');
    await expect(page.locator('#cockpitRailToggle')).toBeFocused();
  });
});

async function titleOf(page: Page, id: string): Promise<string> {
  const name = await page
    .locator(`.ws-map-tile[data-ws-id="${id}"] .ws-map-tile-name`)
    .textContent();
  return (name || '').trim();
}

// ---------------------------------------------------------------------------
// The selected-group rail's Map layout section (#346 FR-150 – FR-156)
// ---------------------------------------------------------------------------

test.describe('group Map layout rail (#346)', () => {
  // Serial: every test seeds a fixture into the ONE shared demo server, and
  // picks its row from how many already exist. Run in parallel, two workers
  // read the same count and stack their districts on each other — producing
  // real (and correctly reported) containment conflicts that have nothing to do
  // with what is being tested.
  test.describe.configure({ mode: 'serial' });

  test.beforeEach(async ({ page }) => {
    await skipOnboarding(page);
  });

  /**
   * A group with one member, on a row of its own within the sandbox.
   *
   * The tag carries a random component as well as a timestamp: these specs run
   * in parallel workers, and two fixtures created in the same millisecond would
   * collide on the workspace name and one create would be refused.
   */
  async function seedGroup(page: Page) {
    const tag = `${String(Date.now()).slice(-6)}-${Math.random().toString(36).slice(2, 7)}`;
    const existing = await (await page.request.get('/api/workspaces')).json();
    const row = (existing.folders || []).filter((f: { name?: string }) =>
      String(f.name || '').startsWith('Rail member ')
    ).length;
    const make = async (name: string, kind = 'workspace') => {
      const res = await page.request.post('/api/workspaces', { data: { name, kind } });
      const body = await res.json();
      if (!body?.folder?.id) {
        throw new Error(
          `create ${name} failed: ${res.status()} ${JSON.stringify(body).slice(0, 200)}`
        );
      }
      return body.folder.id;
    };
    const group = await make(`Rail group ${tag}`, 'group');
    const member = await make(`Rail member ${tag}`);
    await page.request.put(`/api/workspaces/${member}`, { data: { parent_id: group } });
    await page.request.patch('/api/workspace-map/layout', {
      data: {
        operations: [
          { op: 'set_positions', positions: { [member]: { x: 380, y: 380 + row * 570 } } }
        ]
      }
    });
    return { group, member, tag };
  }

  async function selectGroup(page: Page, group: string) {
    const district = page.locator(`.ws-map-district[data-group-id="${group}"]`);
    await district.waitFor({ timeout: 15000 });
    await expect(async () => {
      await district.locator('.ws-map-district-tag').click({ force: true });
      await expect(page.locator('[data-rail-map-layout]')).toBeVisible({ timeout: 1500 });
    }).toPass({ timeout: 20000 });
  }

  test('a selected group keeps Open Group and gains Map layout (FR-150, FR-151, FR-155)', async ({
    page
  }) => {
    const { group } = await seedGroup(page);
    await page.goto('/');
    await selectGroup(page, group);

    const rail = page.locator('#cockpitRailContext');
    // The existing group rail is untouched: navigation and aggregates stay.
    await expect(rail.locator('[data-cockpit-rail-open]')).toHaveText(/Open Group/);
    await expect(rail).toContainText('Totals cover every workspace inside this group');

    const layout = page.locator('[data-rail-map-layout]');
    await expect(layout).toBeVisible();
    await expect(layout).toContainText('Automatic size');
    await expect(layout).toContainText('never change which workspaces are in it');
    await expect(page.locator('[data-cockpit-group-resize]')).toBeEnabled();
    await expect(page.locator('[data-cockpit-group-fit]')).toBeDisabled();
    await expect(page.locator('[data-cockpit-group-collapse]')).toHaveText('Collapse group');

    // Open Group leads; the layout controls follow it (FR-155).
    const openTop = (await rail.locator('[data-cockpit-rail-open]').boundingBox())!.y;
    const layoutTop = (await layout.boundingBox())!.y;
    expect(layoutTop).toBeGreaterThan(openTop);
  });

  test('Tree hides the Map-only layout controls (FR-154)', async ({ page }) => {
    const { group } = await seedGroup(page);
    await page.goto('/');
    await selectGroup(page, group);
    await expect(page.locator('[data-rail-map-layout]')).toBeVisible();

    await page.locator('#cockpitViewTree').click();
    await page.waitForTimeout(500);

    // The group is still selected and still openable — only the Map-only
    // controls are gone, rather than shown dead beside Tree rows.
    await expect(page.locator('#cockpitRailContext')).toContainText('Open Group');
    await expect(page.locator('[data-rail-map-layout]')).toHaveCount(0);

    await page.locator('#cockpitViewMap').click();
    await page.waitForTimeout(500);
  });

  test('the rail and the district menu run the same action (FR-156)', async ({ page }) => {
    const { group } = await seedGroup(page);
    await page.goto('/');
    await selectGroup(page, group);

    // Collapse from the rail...
    await page.locator('[data-cockpit-group-collapse]').click();
    await page.waitForTimeout(600);
    await expect(page.locator(`.ws-map-district[data-group-id="${group}"]`)).toHaveClass(
      /is-collapsed/
    );
    await expect(page.locator('[data-cockpit-group-collapse]')).toHaveText('Expand group');
    // ...and while collapsed, sizing is truthfully unavailable (FR-115).
    await expect(page.locator('[data-cockpit-group-resize]')).toBeDisabled();
    await expect(page.locator('[data-cockpit-group-fit]')).toBeDisabled();

    // ...then expand from the district's own context menu.
    await page.locator(`.ws-map-district[data-group-id="${group}"] [data-group-menu]`).click({
      force: true
    });
    await page.locator('[data-menu-action="expand-group"]').click();
    await page.waitForTimeout(600);
    await expect(page.locator(`.ws-map-district[data-group-id="${group}"]`)).not.toHaveClass(
      /is-collapsed/
    );
    await expect(page.locator('[data-cockpit-group-collapse]')).toHaveText('Collapse group');
  });

  test('appearance choices are named and reachable at a narrow width (FR-130, FR-168)', async ({
    page
  }) => {
    const { group } = await seedGroup(page);
    await page.setViewportSize({ width: 430, height: 900 });
    await page.goto('/');
    await selectGroup(page, group);

    const accent = page.locator('[data-rail-appearance="accent"]');
    await expect(accent).toContainText('Moss green');
    await expect(page.locator('[data-rail-appearance="theme"]')).toContainText('Blueprint');

    // Reachable without horizontal scrolling at a narrow rail width.
    const overflow = await page.evaluate(() => ({
      scroll: document.documentElement.scrollWidth,
      client: document.documentElement.clientWidth
    }));
    expect(overflow.scroll).toBeLessThanOrEqual(overflow.client + 1);

    await page.locator('[data-cockpit-group-accent][value="moss"]').check();
    await page.waitForTimeout(600);
    await expect(page.locator(`.ws-map-district[data-group-id="${group}"]`)).toHaveClass(
      /ws-map-accent-moss/
    );
    // Use default appearance now has something to undo (FR-137).
    await expect(page.locator('[data-cockpit-group-appearance-reset]')).toBeVisible();
  });
});

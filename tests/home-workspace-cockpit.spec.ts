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

async function dismissCreator(page: Page) {
  // Bootstrap marks the shell visible before its transition finishes; wait for
  // that short transition so the test does not ask Modal.hide() while it is
  // intentionally ignoring lifecycle changes.
  await page.waitForTimeout(200);
  await page.evaluate(() => {
    // @ts-expect-error Bootstrap is a page global.
    window.bootstrap.Modal.getInstance(document.getElementById('addFolderModal'))?.hide();
  });
  await expect(page.locator('#addFolderModal')).toBeHidden();
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
    // Until a hire, the folder action is absent; New Workspace is the header's
    // primary creation action either way.
    await expect(page.locator('#cockpitShowFolderBtn')).toBeHidden();
    await expect(page.locator('#cockpitCreateWorkspaceBtn')).toHaveClass(/modern-btn-primary/);
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

/** Open the blocking context modal through the real Map selection path. */
async function openContextModalViaSelection(page: Page) {
  const modal = page.locator('#cockpitContextModal');
  if (await modal.isVisible()) return;
  await page.locator('.ws-map-tile[data-ws-id]').first().click();
  await expect(modal).toBeVisible();
}

test.describe('Home workspace cockpit', () => {
  test.beforeEach(async ({ page }) => {
    await skipOnboarding(page);
  });

  test('routes Map and Tree Group actions through the shared creator', async ({ page }) => {
    await ensureWorkspace(page);
    await page.request.post('/api/workspaces', {
      data: { name: `Second groupable workspace ${Date.now()}`, workspace_preset: 'general' }
    });
    await page.goto('/');
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-state', 'ready');

    await page.locator('#cockpitMap [data-ws-check]').nth(0).click();
    await page.locator('#cockpitMap [data-ws-check]').nth(1).click();
    await page.getByRole('button', { name: 'Group selected', exact: true }).click();
    await expect(page.locator('#addFolderModal')).toBeVisible();
    await expect(page.locator('#workspaceCreatorKindFixedNotice')).toContainText(
      'keeps that choice fixed'
    );
    await dismissCreator(page);

    // The header now offers only the adaptive creator. Group remains a choice
    // inside it, while explicit empty/selected grouping stays in the Tree.
    await expect(page.getByRole('button', { name: 'Create Group', exact: true })).toHaveCount(0);
    await page.getByRole('button', { name: 'New Workspace', exact: true }).click();
    await expect(page.locator('#addFolderModal')).toBeVisible();
    await expect(page.locator('#workspaceCreatorKindWorkspace')).toBeChecked();
    await expect(page.locator('#workspaceCreatorKindGroup')).toBeEnabled();
    await dismissCreator(page);

    await page.getByRole('button', { name: 'Tree', exact: true }).click();
    await expect(page.locator('#cockpitTree')).toBeVisible();
    await page.locator('[data-tree-new-group]').click();
    await expect(page.locator('#addFolderModal')).toBeVisible();
    await expect(page.locator('#workspaceCreatorKindGroup')).toBeChecked();
    await dismissCreator(page);

    await page.locator('[data-tree-check]').first().check();
    await expect(page.getByRole('button', { name: 'Group selected', exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Group selected', exact: true }).click();
    await expect(page.locator('#addFolderModal')).toBeVisible();
    await expect(page.locator('#workspaceCreatorKindFixedNotice')).toContainText(
      'keeps that choice fixed'
    );
    const reviewedMember = await page
      .locator('[data-tree-check]')
      .first()
      .getAttribute('data-tree-check');
    await page.locator('#folderNameInput').fill('Reviewed Tree Group');
    await page.locator('#wizardNextBtn').click();
    await expect(page.locator('#workspaceReviewSummary')).toContainText(
      'top-level workspace will move'
    );
    const groupCreated = page.waitForResponse(
      response =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname === '/api/workspaces'
    );
    await page.locator('#createFolderBtn').click();
    const body = await (await groupCreated).json();
    const groupID = body?.folder?.id as string;
    await expect(page.locator('#addFolderModal')).toBeHidden();
    await expect
      .poll(async () => {
        const list = await (await page.request.get('/api/workspaces')).json();
        const rows = list.workspaces || list.folders || [];
        return rows.find((workspace: { id: string }) => workspace.id === reviewedMember)?.parent_id;
      })
      .toBe(groupID);
    // Keep the suite's shared demo state flat for later Map geometry cases.
    await page.request.delete(`/api/workspaces/${groupID}?confirm=true&delete_mode=group_only`);
  });

  test('renders the cockpit shell full-width, with no flyout open on a bare load', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await expect(page.locator('#homeCockpit')).toBeVisible();
    await expect(page.locator('#oriGuideMapTrigger')).toBeVisible();
    await expect(page.locator('#oriGuideInput')).toHaveCount(1);
    await expect(page.locator('#cockpitMap')).toBeVisible();
    await expect(page.locator('#cockpitTree')).toBeHidden();
    await expect(page.locator('[data-cockpit-view="map"]')).toHaveAttribute('aria-pressed', 'true');

    // Issue #366: bare Home has one dormant modal host and no docked rail.
    await expect(page.locator('#cockpitContextModal')).toBeHidden();
    await expect(page.locator('#cockpitContextModal')).toHaveCount(1);
    await expect(page.locator('#cockpitRail')).toHaveCount(0);
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

  test('workspace context opens in a modal, dismisses without clearing, reopens, and Back clears at invariant width', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    const site = page.locator('.ws-map-tile[data-ws-id]').first();
    await site.waitFor();
    const selectedId = await site.getAttribute('data-ws-id');
    const area = page.locator('.cockpit-workspace-area');
    const baseWidth = (await area.boundingBox())!.width;
    const baseCanvasCount = await page.locator('.ws-map-canvas[data-ws-map-viewport]').count();

    await site.click();
    const modal = page.locator('#cockpitContextModal');
    await expect(modal).toBeVisible();
    await expect(page.locator('#cockpitRailContext')).toBeVisible();
    expect((await area.boundingBox())!.width).toBe(baseWidth);

    await page.keyboard.press('Escape');
    await expect(modal).toBeHidden();
    await page.waitForFunction(() => window.OriHomeCockpit?.getState?.()?.modalVisible === false);
    await expect(page.locator(`.ws-map-tile[data-ws-id="${selectedId}"]`)).toBeFocused();
    await expect(page.locator(`.ws-map-tile[data-ws-id="${selectedId}"]`)).toHaveClass(
      /is-selected/
    );
    expect((await area.boundingBox())!.width).toBe(baseWidth);

    await page.locator(`.ws-map-tile[data-ws-id="${selectedId}"]`).click();
    await expect(modal).toBeVisible();
    await page.waitForFunction(() => window.OriHomeCockpit?.getState?.()?.modalVisible === true);
    await page.mouse.click(4, 4);
    await expect(modal).toBeHidden();
    await page.waitForFunction(() => window.OriHomeCockpit?.getState?.()?.modalVisible === false);
    await page.locator(`.ws-map-tile[data-ws-id="${selectedId}"]`).click();
    await expect(modal).toBeVisible();
    await page.locator('[data-cockpit-rail-back]').click();
    await expect(modal).toBeHidden();
    await expect(page.locator(`.ws-map-tile[data-ws-id="${selectedId}"]`)).not.toHaveClass(
      /is-selected/
    );
    expect((await area.boundingBox())!.width).toBe(baseWidth);
    await expect(page.locator('.ws-map-canvas[data-ws-map-viewport]')).toHaveCount(baseCanvasCount);
    await expect(page.locator('.modal-backdrop')).toHaveCount(0);
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
    await expect(page.locator('#cockpitContextModal')).toBeHidden();
  });

  test('opening workspace context closes Updates without remounting Map or clearing selection', async ({
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
    const selectedId = await tile.getAttribute('data-ws-id');
    await page.locator('#cockpitRailToggle').click();
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeVisible();
    const requestsBeforeOpen = treeRequests;
    const canvasCount = await page.locator('#cockpitMap .ws-map-canvas').count();

    await tile.click();
    await expect(page.locator('#cockpitContextModal')).toBeVisible();
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeHidden();
    await expect(page.locator(`.ws-map-tile[data-ws-id="${selectedId}"]`)).toHaveClass(
      /is-selected/
    );
    expect(treeRequests).toBe(requestsBeforeOpen);
    await expect(page.locator('#cockpitMap .ws-map-canvas')).toHaveCount(canvasCount);
  });

  test('a Map click selects without navigating and the selected item reopens context', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    const site = page.locator('.ws-map-tile[data-ws-id]').first();
    await site.waitFor();

    await site.click();
    await expect(page.locator('[data-cockpit-rail-open]')).toBeVisible();
    expect(new URL(page.url()).pathname).toBe('/');
    await page.locator('#cockpitContextModal [data-bs-dismiss="modal"]').click();
    await expect(page.locator('#cockpitContextModal')).toBeHidden();

    await site.click();
    await expect(page.locator('#cockpitContextModal')).toBeVisible();
    expect(new URL(page.url()).pathname).toBe('/');
  });

  test('Tree context traps focus and restores it to the invoking row on dismiss', async ({
    page
  }) => {
    const id = await ensureWorkspace(page);
    await page.goto('/?view=tree');
    const row = page.locator(`[data-tree-row="${id}"]`);
    await row.click();
    const modal = page.locator('#cockpitContextModal');
    await expect(modal).toBeVisible();
    await page.waitForFunction(() => window.OriHomeCockpit?.getState?.()?.modalVisible === true);
    await expect(page.locator('[data-cockpit-rail-back]')).toBeFocused();

    for (let i = 0; i < 8; i += 1) await page.keyboard.press('Tab');
    expect(
      await page.evaluate(() =>
        document.getElementById('cockpitContextModal')?.contains(document.activeElement)
      )
    ).toBe(true);

    await page.keyboard.press('Escape');
    await expect(modal).toBeHidden();
    await page.waitForFunction(() => window.OriHomeCockpit?.getState?.()?.modalVisible === false);
    await expect(row).toBeFocused();
  });

  test('Personal HQ uses the shared modal, preserves selection on dismiss, and dispatches actions once', async ({
    page
  }) => {
    await page.route('**/api/personal-hq/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ status: { valid: false, hq_onboarding_state: 'unseen' } })
      })
    );
    await page.goto('/');
    const site = page.locator('[data-hq-site]');
    await site.waitFor();
    await site.click();
    const modal = page.locator('#cockpitContextModal');
    await expect(modal).toBeVisible();
    await expect(page.locator('[data-rail-panel="personal-hq"]')).toBeVisible();

    await page.keyboard.press('Escape');
    await expect(modal).toBeHidden();
    await page.waitForFunction(() => window.OriHomeCockpit?.getState?.()?.modalVisible === false);
    expect(await page.evaluate(() => window.OriHomeCockpit?.getState?.()?.selectedId)).toBe(
      '__personal_hq_site__'
    );
    await site.click();
    await expect(modal).toBeVisible();

    await page.evaluate(() => {
      (window as any).__hqActionCount = 0;
      window.addEventListener('ori:personal-hq-action', () => {
        (window as any).__hqActionCount += 1;
      });
    });
    await page.locator('[data-hq-action="build"]').click();
    await expect(modal).toBeHidden();
    expect(await page.evaluate(() => (window as any).__hqActionCount)).toBe(1);
    const buildModal = page.locator('#hqBuildModal');
    await expect(buildModal).toBeVisible();
    await page.waitForFunction(() =>
      document.getElementById('hqBuildModal')?.contains(document.activeElement)
    );
    await buildModal.locator('[data-bs-dismiss="modal"]').first().click();
    await expect(buildModal).toBeHidden();

    await site.click();
    await expect(modal).toBeVisible();
    await page.locator('[data-cockpit-rail-back]').click();
    await expect(modal).toBeHidden();
    expect(await page.evaluate(() => window.OriHomeCockpit?.getState?.()?.selectedId)).toBe('');
  });

  test('Summary dismissal and Back restore the prior selected workspace truthfully', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    const tile = page.locator('.ws-map-tile[data-ws-id]').first();
    const id = await tile.getAttribute('data-ws-id');
    await tile.click();
    await expect(page.locator('#cockpitContextModal')).toBeVisible();
    await page.waitForFunction(() => window.OriHomeCockpit?.getState?.()?.modalVisible === true);
    await page.keyboard.press('Escape');
    await expect(page.locator('#cockpitContextModal')).toBeHidden();
    await page.waitForFunction(() => window.OriHomeCockpit?.getState?.()?.modalVisible === false);

    const summary = page.locator('#cockpitSummaryBtn');
    await summary.click();
    await expect(page.locator('[data-rail-panel="summary"]')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.locator('#cockpitContextModal')).toBeHidden();
    await page.waitForFunction(() => window.OriHomeCockpit?.getState?.()?.modalVisible === false);
    expect(await page.evaluate(() => window.OriHomeCockpit?.getState?.()?.selectedId)).toBe(id);
    await expect(summary).toHaveAttribute('aria-expanded', 'false');

    await summary.click();
    await page.locator('[data-cockpit-rail-back]').click();
    await expect(page.locator('#cockpitContextModal')).toBeVisible();
    await expect(page.locator('[data-rail-panel="workspace"]')).toBeVisible();
    expect(await page.evaluate(() => window.OriHomeCockpit?.getState?.()?.selectedId)).toBe(id);
  });

  test('another Bootstrap dialog settles context first and leaves one backdrop', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().click();
    await expect(page.locator('#cockpitContextModal')).toBeVisible();

    await page.evaluate(() => {
      const target = document.getElementById('addFolderModal');
      window.bootstrap.Modal.getOrCreateInstance(target).show();
    });
    await expect(page.locator('#cockpitContextModal')).toBeHidden();
    await expect(page.locator('#addFolderModal')).toBeVisible();
    await expect(page.locator('.modal.show')).toHaveCount(1);
    await expect(page.locator('.modal-backdrop.show')).toHaveCount(1);
    await page.waitForFunction(() =>
      document.getElementById('addFolderModal')?.contains(document.activeElement)
    );
    await page.locator('#addFolderModal [data-bs-dismiss="modal"]').first().click();
    await expect(page.locator('#addFolderModal')).toBeHidden();
    await expect(page.locator('.modal-backdrop')).toHaveCount(0);
    await expect(page.locator('body')).not.toHaveClass(/modal-open/);
  });

  test('Open Workspace navigates and Enter opens the focused site', async ({ page }) => {
    const id = await ensureWorkspace(page);
    const workspaceResponse = await page.request.get(`/api/workspaces/${id}`);
    const workspacePayload = await workspaceResponse.json();
    const slug =
      workspacePayload.folder_slug ||
      workspacePayload.folder?.folder_slug ||
      workspacePayload.workspace?.folder_slug;
    expect(slug).toBeTruthy();
    await page.goto('/');
    const site = page.locator(`.ws-map-tile[data-ws-id="${id}"]`);
    await site.waitFor();
    await site.click();

    const open = page.locator('[data-cockpit-rail-open]');
    await expect(open).toHaveAttribute('href', `/workspaces/${slug}`);
    await open.click();
    await page.waitForURL(`**/workspaces/${slug}`);

    // FR125: Enter on a focused site opens it explicitly.
    await page.goto('/');
    await page.locator(`.ws-map-tile[data-ws-id="${id}"]`).focus();
    await page.keyboard.press('Enter');
    await page.waitForURL(`**/workspaces/${slug}`);
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

  test('Escape dismisses context without clearing selection or Map filters', async ({ page }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    const tile = page.locator('.ws-map-tile[data-ws-id]').first();
    await page.locator('[data-cockpit-signal="running"]').click();
    await tile.click();
    const selectedId = await tile.getAttribute('data-ws-id');
    await expect(page.locator('#cockpitContextModal')).toBeVisible();

    await page.keyboard.press('Escape');

    await expect(page.locator('#cockpitContextModal')).toBeHidden();
    await expect(page.locator(`.ws-map-tile[data-ws-id="${selectedId}"]`)).toHaveClass(
      /is-selected/
    );
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

  test('Ask Ori stays in its universal non-modal panel and never opens context', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();

    await page.locator('#oriGuideMapTrigger').click();
    await page.locator('#oriGuideInput').fill('What needs attention today?');
    await page.locator('#oriGuideSend').click();
    await expect(page.locator('#oriGuidePanel')).toBeVisible();
    await expect(page.locator('#oriGuideReply')).not.toBeEmpty();
    await expect(page.locator('#homeAssistantThinkingModal')).toBeHidden();

    await expect(page.locator('#cockpitContextModal')).toBeHidden();
    await expect(page.locator('.modal-backdrop')).toHaveCount(0);
    await expect(page.locator('#cockpitMap')).toBeVisible();
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
    for (const label of ['Create Workspace', 'Create Group', 'Import Folder', 'Rescan']) {
      await expect(page.locator('#cockpitTree').getByRole('button', { name: label })).toBeVisible();
    }
    await expect(page.locator('[data-tree-move]').first()).toBeVisible();
  });

  test('theatre width stays invariant before, during, and after modal use at supported widths', async ({
    page
  }) => {
    await ensureWorkspace(page);

    for (const [width, height] of [
      [1440, 950],
      [1100, 900],
      [900, 900],
      [430, 900]
    ] as const) {
      await page.setViewportSize({ width, height });
      await page.goto('/');
      await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();
      const area = page.locator('.cockpit-workspace-area');
      const before = (await area.boundingBox())!.width;

      await openContextModalViaSelection(page);
      const during = (await area.boundingBox())!.width;
      await page.keyboard.press('Escape');
      await expect(page.locator('#cockpitContextModal')).toBeHidden();
      const after = (await area.boundingBox())!.width;

      expect(during).toBe(before);
      expect(after).toBe(before);
      const overflow = await page.evaluate(() => ({
        pageWidth: document.documentElement.scrollWidth,
        viewportWidth: window.innerWidth
      }));
      expect(overflow.pageWidth, `page scrolls horizontally at ${width}px`).toBeLessThanOrEqual(
        overflow.viewportWidth + 1
      );
    }
  });

  test('Summary header action opens the shared context modal truthfully', async ({ page }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();

    const summary = page.locator('#cockpitSummaryBtn');
    await expect(summary).toHaveAttribute('aria-controls', 'cockpitContextModal');
    await expect(summary).toHaveAttribute('aria-expanded', 'false');
    await summary.click();
    await expect(page.locator('#cockpitContextModal')).toBeVisible();
    await expect(page.locator('[data-rail-panel="summary"]')).toBeVisible();
    await expect(summary).toHaveAttribute('aria-expanded', 'true');
    await page.keyboard.press('Escape');
    await expect(summary).toHaveAttribute('aria-expanded', 'false');
    await expect(summary).toHaveText('Summary');
  });
});

/**
 * Group 3 (Issue #334): Updates, Quests, and Quick Capture share ONE
 * transient header-panel state. Issue #366 makes context blocking: an explicit
 * context open closes any header disclosure without clearing its draft/state.
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

  test("Updates and Quests are mutually exclusive; Quick Capture's dialog closes them, blocks them, and keeps its draft (FR8-FR9)", async ({
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
    const capture = page.locator('#cockpitCaptureModal');

    await updatesBtn.click();
    await expect(updatesFlyout).toBeVisible();
    await expect(questsFlyout).toBeHidden();

    // Opening Quests closes Updates (FR8).
    await questsBtn.click();
    await expect(questsFlyout).toBeVisible();
    await expect(updatesFlyout).toBeHidden();
    await expect(updatesBtn).toHaveAttribute('aria-expanded', 'false');

    // Opening Quick Capture closes Quests, and the dialog then owns the page:
    // the header triggers behind its backdrop cannot be used.
    await captureBtn.click();
    await expect(capture).toBeVisible();
    await expect(questsFlyout).toBeHidden();
    await expect(captureBtn).toHaveAttribute('aria-expanded', 'true');
    await page.locator('#cockpitCaptureTitle').fill('Draft that must survive');
    await expect(updatesBtn.click({ timeout: 800, trial: true })).rejects.toThrow();
    await expect(updatesFlyout).toBeHidden();

    // Dismissing keeps the draft (FR9), and focus returns to the trigger.
    await page.keyboard.press('Escape');
    await expect(capture).toBeHidden();
    await expect(captureBtn).toBeFocused();
    await updatesBtn.click();
    await expect(updatesFlyout).toBeVisible();
    await captureBtn.click();
    await expect(capture).toBeVisible();
    await expect(updatesFlyout).toBeHidden();
    await expect(page.locator('#cockpitCaptureTitle')).toHaveValue('Draft that must survive');
    await page.keyboard.press('Escape');
    await expect(capture).toBeHidden();

    // Blocking context never clears that draft either.
    await page.locator('.ws-map-tile[data-ws-id]').first().click();
    await expect(page.locator('#cockpitContextModal')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.locator('#cockpitContextModal')).toBeHidden();
    await captureBtn.click();
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

  test('Escape closes a flyout, while a later modal dismissal preserves selection', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    const tile = page.locator('.ws-map-tile[data-ws-id]').first();
    const id = await tile.getAttribute('data-ws-id');

    await page.locator('#cockpitRailToggle').click();
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeHidden();

    await tile.click();
    await expect(page.locator('#cockpitContextModal')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.locator('#cockpitContextModal')).toBeHidden();
    await expect(page.locator(`.ws-map-tile[data-ws-id="${id}"]`)).toHaveClass(/is-selected/);
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

  test('selecting workspace context closes Updates and keeps the new selection', async ({
    page
  }) => {
    const first = await ensureWorkspace(page);
    const res = await page.request.post('/api/workspaces', {
      data: { name: `Cockpit coordination ${Date.now()}`, workspace_preset: 'general' }
    });
    const second = (await res.json())?.folder?.id;

    await page.goto('/');
    await page.locator('#cockpitRailToggle').click();
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeVisible();
    await page.locator('[data-cockpit-view="tree"]').click();
    await page.locator(`[data-tree-row="${second}"]`).click();
    await expect(page.locator('#cockpitContextModal')).toBeVisible();
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeHidden();
    await expect(page.locator(`.ws-map-tile[data-ws-id="${second}"]`)).toHaveClass(/is-selected/);
    await expect(page.locator(`.ws-map-tile[data-ws-id="${first}"]`)).not.toHaveClass(
      /is-selected/
    );
  });

  test('opening Summary closes Quests and never loses its progression state', async ({ page }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('#cockpitQuestsToggle').waitFor({ state: 'visible' });
    await page.locator('#cockpitQuestsToggle').click();
    await expect(page.locator('#cockpitQuestsFlyout')).toBeVisible();
    await expect(page.locator('[data-role="progress-count"]')).toHaveText('0/2');

    await page.locator('#cockpitSummaryBtn').click();
    await expect(page.locator('#cockpitContextModal')).toBeVisible();
    await expect(page.locator('#cockpitQuestsFlyout')).toBeHidden();
    await page.keyboard.press('Escape');
    await page.locator('#cockpitQuestsToggle').click();
    await expect(page.locator('[data-role="progress-count"]')).toHaveText('0/2');
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

  test('header controls follow the visual grouping in document and focus order', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();

    // Identity row (view switch, then actions, creation last), then the
    // readout row (App Guide, filters, Updates, Quests, resources).
    const ids = [
      'cockpitViewMap',
      'cockpitViewTree',
      'cockpitCaptureBtn',
      'cockpitSummaryBtn',
      'cockpitShowFolderBtn',
      'cockpitCreateWorkspaceBtn',
      'oriGuideMapTrigger',
      'cockpitSignalFilters',
      'cockpitRailToggle',
      'cockpitQuestsToggle',
      'cockpitEconomy'
    ];
    const order = await page.evaluate(list => {
      const all = Array.from(document.querySelectorAll('*'));
      return list.map(id => all.indexOf(document.getElementById(id)));
    }, ids);
    order.forEach((position, index) => {
      expect(position, ids[index]).toBeGreaterThan(-1);
      if (index > 0) expect(position, ids[index]).toBeGreaterThan(order[index - 1]);
    });

    // Genuinely Tab-reachable in that order (FR53): Updates → Quests.
    await page.locator('#cockpitQuestsToggle').waitFor({ state: 'visible' });
    await page.locator('#cockpitRailToggle').focus();
    await page.keyboard.press('Tab');
    await expect(page.locator('#cockpitQuestsToggle')).toBeFocused();
  });
});

/**
 * Home header hierarchy (home-workspace-map-ui-refresh group 1): an identity
 * row with the view switch and actions, then a quieter readout row. Resource
 * and Quests states are route-mocked here; that is presentation evidence, not
 * proof of the economy or progression backends.
 */
test.describe('Header hierarchy', () => {
  function mockEconomy(page: Page, body: Record<string, unknown> | null) {
    return page.route('**/api/economy', route =>
      body === null
        ? route.fulfill({ status: 404, contentType: 'text/plain', body: 'not found' })
        : route.fulfill({
            status: 200,
            contentType: 'application/json',
            body: JSON.stringify(body)
          })
    );
  }

  async function headerGeometry(page: Page) {
    return page.evaluate(() => {
      const header = document.querySelector('.cockpit-area-header') as HTMLElement;
      const visible = Array.from(header.querySelectorAll('button')).filter(button => {
        const rect = button.getBoundingClientRect();
        return rect.width > 0 && rect.height > 0;
      });
      return {
        overflow: document.documentElement.scrollWidth - window.innerWidth,
        clipped: visible
          .filter(
            button =>
              button.scrollWidth > button.clientWidth + 1 ||
              button.getBoundingClientRect().right > window.innerWidth + 0.5
          )
          .map(button => button.id || button.textContent?.trim()),
        heights: [
          ...new Set(visible.map(button => Math.round(button.getBoundingClientRect().height)))
        ]
      };
    });
  }

  test.beforeEach(async ({ page }) => {
    await skipOnboarding(page);
  });

  test('groups identity, actions, and readouts, with New Workspace as the primary creation action', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();

    const main = page.locator('.cockpit-area-main');
    const utility = page.locator('.cockpit-area-utility');
    await expect(main.locator('[data-cockpit-area-title]')).toHaveText('Workspace Map');
    await expect(main.locator('#cockpitViewMap')).toBeVisible();
    await expect(main.locator('#cockpitCaptureBtn')).toBeVisible();
    await expect(main.locator('#cockpitSummaryBtn')).toBeVisible();
    await expect(main.locator('#cockpitCreateWorkspaceBtn')).toHaveClass(/modern-btn-primary/);
    await expect(utility.locator('#cockpitSignalFilters')).toBeVisible();
    await expect(utility.locator('#cockpitRailToggle')).toBeVisible();
    await expect(utility.locator('#oriGuideMapTrigger')).toContainText('App Guide');

    // Explore a folder appears once an assistant relationship makes it useful:
    // secondary, and before the primary New Workspace.
    await page.evaluate(() =>
      document.dispatchEvent(
        new CustomEvent('personal-assistant:status', {
          detail: { personalAssistant: { state: 'active' } }
        })
      )
    );
    const folder = main.locator('#cockpitShowFolderBtn');
    await expect(folder).toBeVisible();
    await expect(folder).toHaveClass(/modern-btn-secondary/);
    const [folderBox, createBox] = await Promise.all([
      folder.boundingBox(),
      main.locator('#cockpitCreateWorkspaceBtn').boundingBox()
    ]);
    expect(folderBox!.x).toBeLessThan(createBox!.x);
  });

  test('Map and Tree keep the same header; only the Map-only filters step out', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();
    const always = [
      '#cockpitCaptureBtn',
      '#cockpitSummaryBtn',
      '#cockpitCreateWorkspaceBtn',
      '#cockpitRailToggle',
      '#oriGuideMapTrigger'
    ];
    for (const selector of always) await expect(page.locator(selector)).toBeVisible();
    await expect(page.locator('#cockpitSignalFilters')).toBeVisible();

    await page.locator('#cockpitViewTree').click();
    await expect(page.locator('[data-cockpit-area-title]')).toHaveText('Workspace Tree');
    for (const selector of always) await expect(page.locator(selector)).toBeVisible();
    await expect(page.locator('#cockpitSignalFilters')).toBeHidden();
    // The readouts keep their right-hand home even with the filters gone.
    const controls = await page.locator('.cockpit-area-controls').boundingBox();
    const utility = await page.locator('.cockpit-area-utility').boundingBox();
    expect(controls!.x + controls!.width).toBeGreaterThan(utility!.x + utility!.width - 2);
  });

  test('resources: zero is quiet, positive is not, Energy is labelled, and help works by keyboard', async ({
    page
  }) => {
    await mockEconomy(page, {
      craft: 0,
      harvest: 3,
      energy: { used_today: 0, daily_figure: 100000 },
      farms: [],
      pending_by_workspace: {}
    });
    await ensureWorkspace(page);
    await page.goto('/');
    const economy = page.locator('#cockpitEconomy');
    await expect(economy).toBeVisible();
    await expect(page.locator('[data-economy-craft]')).toHaveText('0');
    await expect(page.locator('[data-economy-craft]')).toHaveAttribute('data-zero', 'true');
    await expect(page.locator('[data-economy-harvest]')).toHaveText('3');
    await expect(page.locator('[data-economy-harvest]')).toHaveAttribute('data-zero', 'false');
    const energy = page.locator('[data-economy-energy]');
    await expect(energy).toContainText('Energy');
    await expect(energy).toHaveAttribute('data-idle', 'true');

    // Keyboard: open the Energy explanation, then Escape returns focus to it.
    await energy.focus();
    await page.keyboard.press('Enter');
    const help = page.locator('#cockpitEconomyHelp');
    await expect(help).toBeVisible();
    await expect(help).toContainText('Energy');
    const [helpBox, utilityBox] = await Promise.all([
      help.boundingBox(),
      page.locator('.cockpit-area-utility').boundingBox()
    ]);
    expect(helpBox!.y).toBeGreaterThanOrEqual(utilityBox!.y + utilityBox!.height);
    await page.keyboard.press('Escape');
    await expect(help).toBeHidden();
    await expect(energy).toBeFocused();
  });

  test('a disabled economy hides the resource group instead of showing zeros', async ({ page }) => {
    await mockEconomy(page, null);
    await ensureWorkspace(page);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();
    await page.waitForTimeout(300);
    await expect(page.locator('#cockpitEconomy')).toBeHidden();
    await expect(page.locator('#cockpitRailToggle')).toBeVisible();
  });

  test('completed Quests keep their text but quiet down', async ({ page }) => {
    await page.route('**/api/progression', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ current_tier: 3, total_tiers: 3, all_complete: true, tiers: [] })
      })
    );
    await ensureWorkspace(page);
    await page.goto('/');
    const quests = page.locator('#cockpitQuestsToggle');
    await expect(quests).toBeVisible();
    await expect(quests).toContainText('All complete');
    await expect(quests).toHaveAttribute('data-complete', 'true');
  });

  for (const viewport of [
    { width: 390, height: 844 },
    { width: 768, height: 1024 },
    { width: 1440, height: 900 }
  ]) {
    test(`at ${viewport.width}px the header wraps without overflow and overlays never move the map`, async ({
      page
    }) => {
      await page.route('**/api/progression', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            current_tier: 10,
            total_tiers: 12,
            tiers: [
              {
                tier: 10,
                name: 'A very long tier name that should never clip a header label',
                quests: Array.from({ length: 12 }, (_, i) => ({
                  id: `long-${i}`,
                  title: `Quest ${i}`,
                  status: i < 3 ? 'completed' : 'pending'
                }))
              }
            ]
          })
        })
      );
      await page.setViewportSize(viewport);
      await ensureWorkspace(page);
      await page.goto('/');
      await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();
      await expect(page.locator('#cockpitQuestsToggle')).toContainText('Tier 10 · 3/12');

      const geometry = await headerGeometry(page);
      expect(geometry.overflow).toBeLessThanOrEqual(1);
      expect(geometry.clipped).toEqual([]);
      // One shared control height for every header control.
      expect(geometry.heights.length).toBe(1);

      const body = page.locator('.cockpit-area-body');
      const before = await body.boundingBox();
      for (const trigger of ['#cockpitRailToggle', '#cockpitQuestsToggle']) {
        await page.locator(trigger).click();
        const flyout = page.locator(
          trigger === '#cockpitRailToggle' ? '#cockpitUpdatesFlyout' : '#cockpitQuestsFlyout'
        );
        await expect(flyout).toBeVisible();
        expect(await body.boundingBox()).toEqual(before);
        // The overlay opens under its trigger row rather than over it.
        const [flyoutBox, triggerBox] = await Promise.all([
          flyout.boundingBox(),
          page.locator(trigger).boundingBox()
        ]);
        expect(flyoutBox!.y).toBeGreaterThanOrEqual(triggerBox!.y + triggerBox!.height);
        await page.keyboard.press('Escape');
        await expect(flyout).toBeHidden();
      }
    });
  }
});

/**
 * Sparse maps and the compact Home assistant (home-workspace-map-ui-refresh
 * group 3). Workspace lists, HQ status, and the assistant relationship are
 * route-mocked here so each state is exact; the real empty/HQ-only paths are
 * exercised in the demo.
 */
test.describe('Sparse map invitation and compact assistant', () => {
  const HQ = { id: 'hq-1', name: 'Personal HQ', kind: 'workspace', folder_slug: 'personal-hq' };
  const OTHER = { id: 'ws-2', name: 'Studio', kind: 'workspace', folder_slug: 'studio' };

  function routeTree(page: Page, folders: () => unknown[]) {
    return page.route('**/api/workspaces?tree=true', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ folders: folders() })
      })
    );
  }

  function routeHQ(page: Page, status: Record<string, unknown>, delay = 0) {
    return page.route('**/api/personal-hq/status', async route => {
      if (delay) await new Promise(resolve => setTimeout(resolve, delay));
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ status })
      });
    });
  }

  function routeAssistant(page: Page, state: string, name = 'Atlas') {
    return page.route(/\/api\/personal-assistant$/, route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          personal_assistant: {
            state,
            state_version: 1,
            assistant_id: 'spec-assistant',
            display_name: name,
            hq_workspace_id: 'hq-1',
            next_action: 'ask',
            availability: { model: { status: 'not_configured', available: false } }
          }
        })
      })
    );
  }

  test.beforeEach(async ({ page }) => {
    await skipOnboarding(page);
  });

  test('an authoritative empty map invites with a heading and the two existing entry points', async ({
    page
  }) => {
    await routeTree(page, () => []);
    await routeHQ(page, { valid: false, hq_onboarding_state: 'unseen' });
    await page.goto('/');
    const invite = page.locator('.cockpit-empty-map-actions');
    await expect(invite).toHaveAttribute('data-map-invitation', 'empty');
    // Named by its own visible heading.
    await expect(page.getByRole('group', { name: 'Add a workspace to your map' })).toBeVisible();
    await expect(invite.getByRole('button', { name: 'New Workspace' })).toHaveCount(1);
    await expect(invite.getByRole('button', { name: 'Import Folder' })).toHaveCount(1);
  });

  test('an HQ-only map invites once its late HQ status validates, and stops when a workspace arrives', async ({
    page
  }) => {
    let folders: unknown[] = [HQ];
    await routeTree(page, () => folders);
    await routeHQ(page, { valid: true, workspace_id: 'hq-1' }, 400);
    await page.goto('/');
    await expect(page.locator(`.ws-map-tile[data-ws-id="${HQ.id}"]`)).toBeVisible();

    // The list lands first; the invitation waits for the status that proves
    // this lone workspace is the designated HQ.
    const invite = page.locator('.cockpit-empty-map-actions');
    await expect(invite).toHaveAttribute('data-map-invitation', 'hq-only');
    await expect(invite).toContainText('Your Personal HQ is set up.');

    // A filter dims tiles; it never makes a map look empty or change the voice.
    await page.locator('[data-cockpit-signal="running"]').click();
    await expect(invite).toHaveAttribute('data-map-invitation', 'hq-only');
    await page.locator('[data-cockpit-signal="running"]').click();

    folders = [HQ, OTHER];
    await page.evaluate(() => window.dispatchEvent(new Event('ori:workspaces-changed')));
    await expect(page.locator(`.ws-map-tile[data-ws-id="${OTHER.id}"]`)).toBeVisible();
    await expect(invite).toHaveCount(0);
  });

  test('on a phone, the HQ-only invitation gets room and never covers the HQ', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await routeTree(page, () => [HQ]);
    await routeHQ(page, { valid: true, workspace_id: 'hq-1' });
    // A first visit: no saved camera, so the map frames the HQ itself (a
    // shared sandbox would otherwise restore wherever it last looked).
    await page.route('**/api/workspace-map/layout', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          layout: { schema_version: 1, revision: 1, snap_to_grid: true, positions: {} }
        })
      })
    );
    await page.goto('/');
    const invite = page.locator('.cockpit-empty-map-actions');
    await expect(invite).toHaveAttribute('data-map-invitation', 'hq-only');
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-invitation', 'hq-only');
    const map = (await page.locator('#cockpitMap').boundingBox())!;
    expect(
      map.height,
      'the first-run map gets the empty map’s extra height'
    ).toBeGreaterThanOrEqual(519);
    await page.waitForTimeout(300);
    const [card, hq] = await Promise.all([
      invite.boundingBox(),
      page.locator(`.ws-map-tile[data-ws-id="${HQ.id}"]`).boundingBox()
    ]);
    const overlap =
      card!.x < hq!.x + hq!.width &&
      hq!.x < card!.x + card!.width &&
      card!.y < hq!.y + hq!.height &&
      hq!.y < card!.y + card!.height;
    expect(overlap, 'the card leaves the HQ building visible').toBe(false);
  });

  test('a lone workspace that is not the HQ, or a failed load, never invites', async ({ page }) => {
    await routeTree(page, () => [OTHER]);
    await routeHQ(page, { valid: true, workspace_id: 'hq-1' });
    await page.goto('/');
    await expect(page.locator(`.ws-map-tile[data-ws-id="${OTHER.id}"]`)).toBeVisible();
    await page.waitForTimeout(400);
    await expect(page.locator('.cockpit-empty-map-actions')).toHaveCount(0);

    await page.unroute('**/api/workspaces?tree=true');
    await page.route('**/api/workspaces?tree=true', route =>
      route.fulfill({ status: 500, contentType: 'application/json', body: '{}' })
    );
    await page.reload();
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-state', 'error');
    await expect(page.locator('.cockpit-empty-map-actions')).toHaveCount(0);
  });

  test('the resting Home assistant is compact, keeps its identity, and shows cues that need the user', async ({
    page
  }) => {
    await routeAssistant(page, 'paused', 'Wilhelmina Featherstonehaugh-Montgomery');
    await ensureWorkspace(page);
    await page.goto('/');
    const launcher = page.locator('#personalAssistantLauncher');
    await expect(launcher).toBeVisible();
    await expect(launcher).toContainText('Wilhelmina Featherstonehaugh-Montgomery');
    await expect(launcher).toContainText('Personal Assistant');
    const status = page.locator('#personalAssistantLauncherStatus');
    await expect(status).toHaveText('Paused');
    await expect(status).toBeVisible();
    const box = (await launcher.boundingBox())!;
    expect(box.height, 'compact at rest').toBeLessThanOrEqual(56);
    expect(box.width).toBeLessThanOrEqual(262);
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - window.innerWidth
    );
    expect(overflow).toBeLessThanOrEqual(1);
    // Still the personal assistant, distinct from the App Guide.
    await expect(page.locator('#oriGuideMapTrigger')).toContainText('App Guide');
    await expect(launcher).not.toContainText('App Guide');
  });

  for (const [state, cue] of [
    ['needs_hq', 'Build HQ'],
    ['repair_needed', 'Repair needed']
  ] as const) {
    test(`the compact launcher keeps the "${cue}" cue visible`, async ({ page }) => {
      await routeAssistant(page, state);
      await ensureWorkspace(page);
      await page.goto('/');
      await expect(page.locator('#personalAssistantLauncherStatus')).toHaveText(cue);
      await expect(page.locator('#personalAssistantLauncherStatus')).toBeVisible();
    });
  }

  test('a routine cue rests on Home but not elsewhere, where the launcher is unchanged', async ({
    page
  }) => {
    await routeAssistant(page, 'active');
    await ensureWorkspace(page);
    await page.goto('/');
    const status = page.locator('#personalAssistantLauncherStatus');
    await expect(status).toHaveAttribute('data-tone', /info|action/);
    if ((await status.getAttribute('data-tone')) === 'info') await expect(status).toBeHidden();
    const home = (await page.locator('#personalAssistantLauncher').boundingBox())!;

    await page.goto('/agents');
    const launcher = page.locator('#personalAssistantLauncher');
    await expect(launcher).toBeVisible();
    const elsewhere = (await launcher.boundingBox())!;
    expect(elsewhere.height, 'the full launcher everywhere else').toBeGreaterThan(home.height);
    await expect(page.locator('.personal-assistant-launcher__role')).toHaveCSS(
      'text-transform',
      'uppercase'
    );
  });
});

/**
 * Quick Capture as a focused dialog (home-workspace-map-ui-refresh group 4).
 * Everything but the last test route-mocks the backlog POST and/or the HQ
 * status so each branch is exact; the last test saves for real and reads the
 * item back from the HQ's backlog.
 */
test.describe('Quick Capture dialog', () => {
  const HQ_STATUS = {
    valid: true,
    workspace_id: 'hq-mock',
    workspace: { id: 'hq-mock', folder_slug: 'personal-hq' }
  };

  function routeHQ(page: Page, status: Record<string, unknown>) {
    return page.route('**/api/personal-hq/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ status })
      })
    );
  }

  /** Record every backlog POST; `respond` decides each answer. */
  async function routeBacklogPosts(
    page: Page,
    respond: (index: number) => { status: number; delay?: number }
  ) {
    const bodies: Record<string, unknown>[] = [];
    await page.route('**/api/orchestration/backlog', async route => {
      if (route.request().method() !== 'POST') return route.fallback();
      bodies.push(JSON.parse(route.request().postData() || '{}'));
      const answer = respond(bodies.length - 1);
      if (answer.delay) await new Promise(resolve => setTimeout(resolve, answer.delay));
      await route.fulfill({
        status: answer.status,
        contentType: 'application/json',
        body: JSON.stringify(answer.status < 300 ? { success: true } : { error: 'nope' })
      });
    });
    return bodies;
  }

  const modal = (page: Page) => page.locator('#cockpitCaptureModal');
  const title = (page: Page) => page.locator('#cockpitCaptureTitle');

  /** Open the dialog; by default also wait until the HQ check lets it save. */
  async function openCapture(page: Page, { ready = true } = {}) {
    await page.locator('#cockpitCaptureBtn').click();
    await expect(modal(page)).toBeVisible();
    await expect(title(page)).toBeFocused();
    if (ready) await expect(page.locator('#cockpitCaptureSave')).toBeEnabled();
  }

  /** Everything capture must not disturb. */
  async function mapState(page: Page, host = '#cockpitMap') {
    return {
      box: await page.locator(host).boundingBox(),
      ...(await page.evaluate(() => ({
        camera: (window as any).OriWorkspaceMap?.getCamera?.() ?? null,
        selected:
          document.querySelector('.ws-map-tile.is-selected')?.getAttribute('data-ws-id') ?? '',
        signal:
          document
            .querySelector('[data-cockpit-signal][aria-pressed="true"]')
            ?.getAttribute('data-cockpit-signal') ?? '',
        anchors: [...document.querySelectorAll('.ws-map-tile[data-ws-id]')].map(el => [
          el.getAttribute('data-ws-id'),
          (el as HTMLElement).style.left,
          (el as HTMLElement).style.top
        ])
      })))
    };
  }

  function expectSameState(
    actual: Awaited<ReturnType<typeof mapState>>,
    expected: Awaited<ReturnType<typeof mapState>>,
    label: string
  ) {
    for (const key of ['x', 'y', 'width', 'height'] as const) {
      expect(
        Math.abs(actual.box![key] - expected.box![key]),
        `${label}: ${key}`
      ).toBeLessThanOrEqual(1);
    }
    expect(actual.camera, `${label}: camera`).toEqual(expected.camera);
    expect(actual.selected, `${label}: selection`).toBe(expected.selected);
    expect(actual.signal, `${label}: filter`).toBe(expected.signal);
    expect(actual.anchors, `${label}: layout`).toEqual(expected.anchors);
  }

  async function noDialogResidue(page: Page) {
    await expect(page.locator('.modal-backdrop')).toHaveCount(0);
    await expect(page.locator('body')).not.toHaveClass(/modal-open/);
    await expect(page.locator('#cockpitCaptureModal')).toHaveCount(1);
  }

  test.beforeEach(async ({ page }) => {
    await skipOnboarding(page);
  });

  test('capture never moves the Map or changes its camera, selection, filter, or layout — by keyboard or pointer', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await routeHQ(page, HQ_STATUS);
    await page.goto('/');
    const tile = page.locator('.ws-map-tile[data-ws-id]').first();
    await tile.click();
    await expect(page.locator('#cockpitContextModal')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.locator('#cockpitContextModal')).toBeHidden();
    await page.locator('[data-cockpit-signal="running"]').click();
    await page.waitForTimeout(700); // let the camera save debounce settle
    const before = await mapState(page);
    expect(before.selected).not.toBe('');

    // Keyboard: open from the trigger, type, and use keys the map would take.
    await page.locator('#cockpitCaptureBtn').focus();
    await page.keyboard.press('Enter');
    await expect(modal(page)).toBeVisible();
    await expect(title(page)).toBeFocused();
    await page.keyboard.type('Arrows + and - stay here');
    for (const key of ['ArrowLeft', 'ArrowUp', '+', '-', '0']) await page.keyboard.press(key);
    expectSameState(await mapState(page), before, 'while open');
    await page.keyboard.press('Escape');
    await expect(modal(page)).toBeHidden();
    await expect(page.locator('#cockpitCaptureBtn')).toBeFocused();
    expectSameState(await mapState(page), before, 'after Escape');

    // Pointer: open, then the close button.
    await openCapture(page);
    await modal(page).locator('.btn-close').click();
    await expect(modal(page)).toBeHidden();
    expectSameState(await mapState(page), before, 'after Close');
    await expect(title(page)).toHaveValue(/Arrows \+ and - stay here/);
    await noDialogResidue(page);
  });

  test('capture never moves the Tree either', async ({ page }) => {
    await ensureWorkspace(page);
    await routeHQ(page, HQ_STATUS);
    await page.goto('/?view=tree');
    await page.locator('[data-tree-row]').first().waitFor();
    const before = await page.locator('#cockpitTree').boundingBox();
    await openCapture(page);
    const during = await page.locator('#cockpitTree').boundingBox();
    await page.keyboard.press('Escape');
    const after = await page.locator('#cockpitTree').boundingBox();
    for (const box of [during, after]) {
      for (const key of ['x', 'y', 'width', 'height'] as const) {
        expect(Math.abs(box![key] - before![key])).toBeLessThanOrEqual(1);
      }
    }
  });

  test('a rapid Escape during opening and a handoff from context both settle to one clean state', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await routeHQ(page, HQ_STATUS);
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();
    const before = await mapState(page);

    // Escape pressed inside Bootstrap's show transition is not lost.
    await page.locator('#cockpitCaptureBtn').click();
    await page.keyboard.press('Escape');
    await expect(modal(page)).toBeHidden();
    await noDialogResidue(page);

    // Asked for while context is open: context closes first, then capture.
    await page.locator('.ws-map-tile[data-ws-id]').first().click();
    await expect(page.locator('#cockpitContextModal')).toBeVisible();
    await page.evaluate(() => (window as any).OriHomeCockpit.openCapture());
    await expect(page.locator('#cockpitContextModal')).toBeHidden();
    await expect(modal(page)).toBeVisible();
    await expect(page.locator('.modal-backdrop')).toHaveCount(1);
    await page.keyboard.press('Escape');
    await expect(modal(page)).toBeHidden();
    await noDialogResidue(page);
    const after = await mapState(page);
    expect(Math.abs(after.box!.height - before.box!.height)).toBeLessThanOrEqual(1);

    // Repeated use leaves no residue.
    for (let i = 0; i < 4; i += 1) {
      await openCapture(page);
      await page.keyboard.press('Escape');
      await expect(modal(page)).toBeHidden();
    }
    await noDialogResidue(page);
  });

  test('details are a disclosure that never erases text and reopens with a retained draft', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await routeHQ(page, HQ_STATUS);
    const bodies = await routeBacklogPosts(page, () => ({ status: 201 }));
    await page.goto('/');
    await openCapture(page);
    await expect(modal(page).getByRole('heading', { name: 'Add to backlog' })).toBeVisible();
    await expect(modal(page)).toContainText('Personal HQ · Backlog');
    const toggle = page.locator('#cockpitCaptureDetailsToggle');
    const details = page.locator('#cockpitCaptureDetails');
    await expect(details).toBeHidden();
    await toggle.click();
    await expect(toggle).toHaveAttribute('aria-expanded', 'true');
    await expect(details).toBeFocused();
    // A plain Enter in details is a new line, never a submit.
    await details.type('line one');
    await page.keyboard.press('Enter');
    await details.type('line two');
    expect(bodies).toHaveLength(0);
    await toggle.click();
    await expect(details).toBeHidden();
    await expect(details).toHaveValue('line one\nline two');

    await page.keyboard.press('Escape');
    await openCapture(page);
    await expect(details).toBeVisible();
    await expect(toggle).toHaveAttribute('aria-expanded', 'true');
    await expect(details).toHaveValue('line one\nline two');
  });

  test('an empty or blank title is refused inline and sends nothing', async ({ page }) => {
    await ensureWorkspace(page);
    await routeHQ(page, HQ_STATUS);
    const bodies = await routeBacklogPosts(page, () => ({ status: 201 }));
    await page.goto('/');
    await openCapture(page);
    for (const value of ['', '    ']) {
      await title(page).fill(value);
      await page.locator('#cockpitCaptureSave').click();
      await expect(page.locator('#cockpitCaptureStatus')).toContainText('Add a title');
      await expect(title(page)).toHaveAttribute('aria-invalid', 'true');
      await expect(title(page)).toBeFocused();
    }
    expect(bodies).toHaveLength(0);
    await expect(modal(page)).toBeVisible();
  });

  test('a confirmed save goes to Personal HQ even with another workspace selected, then closes with a receipt', async ({
    page
  }) => {
    const other = await ensureWorkspace(page);
    await routeHQ(page, HQ_STATUS);
    const bodies = await routeBacklogPosts(page, () => ({ status: 201 }));
    await page.goto('/');
    await page.locator(`.ws-map-tile[data-ws-id="${other}"]`).click();
    await expect(page.locator('#cockpitContextModal')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.locator('#cockpitContextModal')).toBeHidden();
    await expect(page.locator(`.ws-map-tile[data-ws-id="${other}"]`)).toHaveClass(/is-selected/);

    await openCapture(page);
    await title(page).fill('  Book the venue  ');
    await page.locator('#cockpitCaptureDetailsToggle').click();
    await page.locator('#cockpitCaptureDetails').fill('  for the June offsite  ');
    // Cmd/Ctrl+Enter submits from the details field.
    await page.keyboard.press(process.platform === 'darwin' ? 'Meta+Enter' : 'Control+Enter');

    await expect(modal(page)).toBeHidden();
    expect(bodies).toEqual([
      {
        workspace_id: 'hq-mock',
        description: 'Book the venue',
        details: 'for the June offsite',
        source_type: 'home_quick_capture'
      }
    ]);
    const receipt = page.locator('#cockpitCaptureReceipt');
    await expect(receipt).toBeVisible();
    await expect(receipt).toContainText('Added to Personal HQ backlog');
    await expect(receipt.getByRole('link', { name: 'View backlog' })).toHaveAttribute(
      'href',
      '/workspaces/personal-hq?panel=backlog'
    );
    // The receipt informs; it does not take focus.
    await expect(page.locator('#cockpitCaptureBtn')).toBeFocused();
    await expect(page.locator('#cockpitRailLive')).toContainText(
      'Added to your Personal HQ backlog'
    );

    // The submitted draft is gone; the next capture starts clean and collapsed.
    await openCapture(page);
    await expect(title(page)).toHaveValue('');
    await expect(page.locator('#cockpitCaptureDetails')).toBeHidden();
    await expect(receipt).toBeHidden();
  });

  test('an HQ whose slug cannot be resolved still saves, with a receipt that offers no broken link', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await routeHQ(page, { valid: true, workspace_id: 'hq-unlisted' });
    const bodies = await routeBacklogPosts(page, () => ({ status: 201 }));
    await page.goto('/');
    await openCapture(page);
    await title(page).fill('Somewhere safe');
    await page.keyboard.press('Enter');
    await expect(modal(page)).toBeHidden();
    expect(bodies).toHaveLength(1);
    const receipt = page.locator('#cockpitCaptureReceipt');
    await expect(receipt).toContainText('Added to Personal HQ backlog');
    await expect(receipt.getByRole('link')).toHaveCount(0);
  });

  test('a failed save keeps the text and says so; the retry sends exactly once more', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await routeHQ(page, HQ_STATUS);
    const bodies = await routeBacklogPosts(page, index => ({ status: index === 0 ? 500 : 201 }));
    await page.goto('/');
    await openCapture(page);
    await title(page).fill('Try twice');
    await page.locator('#cockpitCaptureSave').click();
    const status = page.locator('#cockpitCaptureStatus');
    await expect(status).toContainText('Your text is still here');
    await expect(status).toHaveAttribute('data-kind', 'error');
    await expect(modal(page)).toBeVisible();
    await expect(title(page)).toHaveValue('Try twice');
    await expect(page.locator('#cockpitCaptureSave')).toBeEnabled();

    await page.locator('#cockpitCaptureSave').click();
    await expect(modal(page)).toBeHidden();
    expect(bodies).toHaveLength(2);
  });

  test('a slow save survives dismissal and reopening, sends once, and never clears newer text', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await routeHQ(page, HQ_STATUS);
    const bodies = await routeBacklogPosts(page, () => ({ status: 201, delay: 1500 }));
    await page.goto('/');
    await openCapture(page);
    await title(page).fill('First thought');
    const save = page.locator('#cockpitCaptureSave');
    await save.click();
    await expect(save).toBeDisabled();
    await expect(save).toHaveText('Adding…');
    // More submit paths while in flight change nothing.
    await title(page).press('Enter');
    await page.keyboard.press(process.platform === 'darwin' ? 'Meta+Enter' : 'Control+Enter');

    // Dismiss and reopen while the request is still out.
    await page.keyboard.press('Escape');
    await expect(modal(page)).toBeHidden();
    await openCapture(page, { ready: false });
    await expect(save).toBeDisabled();
    await expect(page.locator('#cockpitCaptureStatus')).toContainText('Adding');
    await title(page).fill('First thought, and a second one');

    await expect(page.locator('#cockpitCaptureReceipt')).toBeVisible({ timeout: 5000 });
    expect(bodies).toHaveLength(1);
    expect(bodies[0].description).toBe('First thought');
    // The newer text is not what was saved, so it stays — and so does the dialog.
    await expect(modal(page)).toBeVisible();
    await expect(title(page)).toHaveValue('First thought, and a second one');
    await expect(page.locator('#cockpitCaptureStatus')).toContainText('still here');
    await expect(save).toBeEnabled();
  });

  test('user text is sent and shown as text, never as markup', async ({ page }) => {
    await ensureWorkspace(page);
    await routeHQ(page, HQ_STATUS);
    const bodies = await routeBacklogPosts(page, () => ({ status: 201 }));
    await page.goto('/');
    const hostile = '<img src=x onerror="window.__capturePwned=1">';
    await openCapture(page);
    await title(page).fill(hostile);
    await page.keyboard.press('Enter');
    await expect(modal(page)).toBeHidden();
    expect(bodies[0].description).toBe(hostile);
    expect(await page.evaluate(() => (window as any).__capturePwned)).toBeUndefined();
    await expect(page.locator('#cockpitCaptureReceipt img')).toHaveCount(0);
  });

  test('without a Personal HQ, capture explains, hands off to setup, and keeps the draft', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await routeHQ(page, { valid: false, hq_onboarding_state: 'unseen' });
    const bodies = await routeBacklogPosts(page, () => ({ status: 201 }));
    await page.goto('/');
    await page.evaluate(() => {
      (window as any).__hqActions = [];
      window.addEventListener('ori:personal-hq-action', event =>
        (window as any).__hqActions.push((event as CustomEvent).detail?.action)
      );
    });
    await openCapture(page, { ready: false });
    await title(page).fill('Idea before HQ');
    await expect(page.locator('#cockpitCaptureStatus')).toContainText('no Personal HQ is set up');
    await expect(page.locator('#cockpitCaptureSave')).toBeDisabled();
    await page.getByRole('button', { name: 'Set up Personal HQ' }).click();
    await expect(modal(page)).toBeHidden();
    expect(await page.evaluate(() => (window as any).__hqActions)).toEqual(['build']);
    expect(bodies).toHaveLength(0);
    // Whatever setup opened is its own dialog; close it if it did.
    await page.keyboard.press('Escape');
    await page.evaluate(() => (window as any).OriHomeCockpit.openCapture());
    await expect(title(page)).toHaveValue('Idea before HQ');
  });

  test('a dialog asked for while capture fades in or out waits for it instead of stacking', async ({
    page
  }) => {
    await ensureWorkspace(page);
    await routeHQ(page, HQ_STATUS);
    await page.goto('/');
    const creator = page.locator('#addFolderModal');
    const askForCreator = () =>
      page.evaluate(() =>
        (window as any).bootstrap.Modal.getOrCreateInstance(
          document.getElementById('addFolderModal')
        ).show()
      );

    // Fading out: Escape, then immediately another dialog.
    await openCapture(page);
    await page.keyboard.press('Escape');
    await askForCreator();
    await expect(creator).toBeVisible();
    await expect(modal(page)).toBeHidden();
    await page.waitForTimeout(400);
    await expect(page.locator('.modal-backdrop')).toHaveCount(1);
    await expect(page.locator('body')).toHaveClass(/modal-open/);
    await page.evaluate(() =>
      (window as any).bootstrap.Modal.getInstance(document.getElementById('addFolderModal')).hide()
    );
    await expect(creator).toBeHidden();
    await noDialogResidue(page);

    // Fading in: open capture and ask for the other dialog in the same beat.
    await page.locator('#cockpitCaptureBtn').click();
    await askForCreator();
    await expect(creator).toBeVisible();
    await expect(modal(page)).toBeHidden();
    await page.waitForTimeout(400);
    await expect(page.locator('.modal-backdrop')).toHaveCount(1);
    await page.evaluate(() =>
      (window as any).bootstrap.Modal.getInstance(document.getElementById('addFolderModal')).hide()
    );
    await noDialogResidue(page);
    // Escape still belongs to the header flyouts afterwards.
    await page.locator('#cockpitRailToggle').click();
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeHidden();
  });

  test('while setup gates workspaces, capture says so instead of checking forever', async ({
    page
  }) => {
    await page.unroute('**/api/onboarding/status');
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({ status: 503, contentType: 'application/json', body: '{}' })
    );
    await page.goto('/');
    await expect(page.locator('#homeCockpit')).toHaveAttribute(
      'data-state',
      'onboarding-unavailable'
    );
    await openCapture(page, { ready: false });
    await title(page).fill('Kept while gated');
    await expect(page.locator('#cockpitCaptureStatus')).toContainText("aren't available");
    await expect(page.locator('#cockpitCaptureStatus')).not.toContainText('Checking');
    await expect(page.locator('#cockpitCaptureSave')).toBeDisabled();
  });

  test('on an invited map, help opens over the card and a phone receipt stays clear of it', async ({
    page
  }) => {
    await page.route('**/api/workspaces?tree=true', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          folders: [
            { id: 'hq-mock', name: 'Personal HQ', kind: 'workspace', folder_slug: 'personal-hq' }
          ]
        })
      })
    );
    await routeHQ(page, HQ_STATUS);
    await routeBacklogPosts(page, () => ({ status: 201 }));
    await page.route('**/api/workspace-map/layout', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          layout: { schema_version: 1, revision: 1, snap_to_grid: true, positions: {} }
        })
      })
    );
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto('/');
    const card = page.locator('.cockpit-empty-map-actions');
    await expect(card).toHaveAttribute('data-map-invitation', 'hq-only');

    // Help's heading is the topmost thing where it is drawn, card or not.
    await page.locator('[data-map-help]').click();
    const heading = page.locator('#wsMapHelpPanel h4');
    await expect(heading).toBeVisible();
    const onTop = await heading.evaluate(el => {
      const r = el.getBoundingClientRect();
      const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
      return !!hit && el.closest('#wsMapHelpPanel')!.contains(hit);
    });
    expect(onTop, 'help is not hidden behind the invitation card').toBe(true);
    await page.locator('[data-map-help]').click();

    // A capture on this map shows its receipt clear of the card.
    await openCapture(page);
    await title(page).fill('First idea');
    await page.keyboard.press('Enter');
    const receipt = page.locator('#cockpitCaptureReceipt');
    await expect(receipt).toBeVisible();
    const [a, b] = await Promise.all([receipt.boundingBox(), card.boundingBox()]);
    const overlap =
      a!.x < b!.x + b!.width &&
      b!.x < a!.x + a!.width &&
      a!.y < b!.y + b!.height &&
      b!.y < a!.y + a!.height;
    expect(overlap, 'the receipt leaves the invitation readable').toBe(false);
  });

  test('an HQ status that cannot be read is not reported as a missing HQ', async ({ page }) => {
    await ensureWorkspace(page);
    let fail = true;
    await page.route('**/api/personal-hq/status', route =>
      fail
        ? route.fulfill({ status: 503, contentType: 'application/json', body: '{}' })
        : route.fulfill({
            status: 200,
            contentType: 'application/json',
            body: JSON.stringify({ status: HQ_STATUS })
          })
    );
    await page.goto('/');
    await openCapture(page, { ready: false });
    await title(page).fill('Kept while checking');
    const status = page.locator('#cockpitCaptureStatus');
    await expect(status).toContainText('could not be checked');
    await expect(status).not.toContainText('no Personal HQ is set up');
    await expect(page.locator('#cockpitCaptureSave')).toBeDisabled();

    fail = false;
    await page.getByRole('button', { name: 'Check again' }).click();
    await expect(page.locator('#cockpitCaptureSave')).toBeEnabled();
    await expect(status).toHaveText('');
    await expect(title(page)).toHaveValue('Kept while checking');
  });

  test('a real capture persists in the Personal HQ backlog and View backlog opens it', async ({
    page
  }) => {
    const status = (await (await page.request.get('/api/personal-hq/status')).json())?.status;
    test.skip(!status?.valid, 'this sandbox has no valid Personal HQ to capture into');
    await page.goto('/');
    const unique = `Quick capture ${Date.now()}`;
    await openCapture(page);
    await title(page).fill(unique);
    await page.keyboard.press('Enter');
    await expect(page.locator('#cockpitCaptureReceipt')).toBeVisible();

    const backlog = await (
      await page.request.get(
        `/api/orchestration/backlog?workspace_id=${encodeURIComponent(status.workspace_id)}`
      )
    ).json();
    const item = (backlog.items || []).find(
      (entry: { task?: { description?: string } }) => entry.task?.description === unique
    );
    expect(item, 'the captured item is in the HQ backlog').toBeTruthy();
    expect(item.task.source_type).toBe('home_quick_capture');

    const link = page.locator('#cockpitCaptureReceipt').getByRole('link', { name: 'View backlog' });
    await link.click();
    await page.waitForURL(/\/workspaces\/[^/?]+\?panel=backlog/);
  });
});

async function titleOf(page: Page, id: string): Promise<string> {
  const name = await page
    .locator(`.ws-map-tile[data-ws-id="${id}"] .ws-map-tile-name`)
    .textContent();
  return (name || '').trim();
}

// ---------------------------------------------------------------------------
// The selected-group modal's Map layout section (#346 FR-150 – FR-156)
// ---------------------------------------------------------------------------

test.describe('group Map layout context (#346)', () => {
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
    await page.waitForFunction(() => window.OriHomeCockpit?.getState?.()?.loading === false);
    const districtTag = district.locator('.ws-map-district-tag');
    await districtTag.evaluate((element: HTMLButtonElement) => element.click());
    await expect(page.locator('#cockpitContextModal')).toBeVisible({ timeout: 5000 });
    await expect(page.locator('[data-rail-map-layout]')).toBeVisible({ timeout: 5000 });
  }

  test('a selected group keeps Open Group and gains Map layout (FR-150, FR-151, FR-155)', async ({
    page
  }) => {
    const { group } = await seedGroup(page);
    await page.goto('/');
    await selectGroup(page, group);

    const rail = page.locator('#cockpitRailContext');
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

    await page.keyboard.press('Escape');
    await expect(page.locator('#cockpitContextModal')).toBeHidden();
    await page.locator('#cockpitViewTree').click();
    await page.locator(`[data-tree-row="${group}"]`).click();

    // Reopened in Tree, the same selected group stays openable while Map-only
    // controls are omitted.
    await expect(page.locator('#cockpitContextModal')).toBeVisible();
    await expect(page.locator('#cockpitRailContext')).toContainText('Open Group');
    await expect(page.locator('[data-rail-map-layout]')).toHaveCount(0);

    await page.keyboard.press('Escape');
    await page.locator('#cockpitViewMap').click();
    await page.waitForTimeout(500);
  });

  test('the context modal and district menu run the same action (FR-156)', async ({ page }) => {
    const { group } = await seedGroup(page);
    await page.goto('/');
    await selectGroup(page, group);

    // Collapse from the context modal...
    await page.locator('[data-cockpit-group-collapse]').click();
    await page.waitForTimeout(600);
    await expect(page.locator(`.ws-map-district[data-group-id="${group}"]`)).toHaveClass(
      /is-collapsed/
    );
    await expect(page.locator('[data-cockpit-group-collapse]')).toHaveText('Expand group');
    // ...and while collapsed, sizing is truthfully unavailable (FR-115).
    await expect(page.locator('[data-cockpit-group-resize]')).toBeDisabled();
    await expect(page.locator('[data-cockpit-group-fit]')).toBeDisabled();

    // ...then dismiss context and expand from the district's own menu.
    await page.keyboard.press('Escape');
    await expect(page.locator('#cockpitContextModal')).toBeHidden();
    await page.waitForFunction(() => window.OriHomeCockpit?.getState?.()?.modalVisible === false);
    await page.locator(`.ws-map-district[data-group-id="${group}"] .ws-map-district-tag`).focus();
    await page.keyboard.press('Shift+F10');
    await page.getByRole('menuitem', { name: 'Expand group' }).click();
    await page.waitForTimeout(600);
    await expect(page.locator(`.ws-map-district[data-group-id="${group}"]`)).not.toHaveClass(
      /is-collapsed/
    );
    await page.locator(`.ws-map-district[data-group-id="${group}"] .ws-map-district-tag`).click({
      force: true
    });
    await expect(page.locator('[data-cockpit-group-collapse]')).toHaveText('Collapse group');
  });

  test('appearance choices are named and reachable at a narrow width (FR-130, FR-168)', async ({
    page
  }) => {
    const { group } = await seedGroup(page);
    await page.setViewportSize({ width: 430, height: 600 });
    await page.goto('/');
    await selectGroup(page, group);

    const accent = page.locator('[data-rail-appearance="accent"]');
    await expect(accent).toContainText('Moss green');
    await expect(page.locator('[data-rail-appearance="theme"]')).toContainText('Blueprint');

    // Long controls scroll inside a bounded narrow dialog without page overflow.
    const overflow = await page.evaluate(() => {
      const body = document.getElementById('cockpitRailContext');
      const dialog = document.querySelector('#cockpitContextModal .modal-dialog');
      const close = document.querySelector('#cockpitContextModal .btn-close');
      return {
        scroll: document.documentElement.scrollWidth,
        client: document.documentElement.clientWidth,
        bodyScroll: body?.scrollHeight ?? 0,
        bodyClient: body?.clientHeight ?? 0,
        dialogRight: dialog?.getBoundingClientRect().right ?? 0,
        closeWidth: close?.getBoundingClientRect().width ?? 0,
        closeHeight: close?.getBoundingClientRect().height ?? 0
      };
    });
    expect(overflow.scroll).toBeLessThanOrEqual(overflow.client + 1);
    expect(overflow.bodyScroll).toBeGreaterThan(overflow.bodyClient);
    expect(overflow.dialogRight).toBeLessThanOrEqual(430);
    expect(overflow.closeWidth).toBeGreaterThanOrEqual(44);
    expect(overflow.closeHeight).toBeGreaterThanOrEqual(44);

    await page.locator('[data-cockpit-group-accent][value="moss"]').check();
    await page.waitForTimeout(600);
    await expect(page.locator(`.ws-map-district[data-group-id="${group}"]`)).toHaveClass(
      /ws-map-accent-moss/
    );
    // Use default appearance now has something to undo (FR-137).
    await expect(page.locator('[data-cockpit-group-appearance-reset]')).toBeVisible();
  });
});

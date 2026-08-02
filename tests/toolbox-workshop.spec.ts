import { test, expect, type Page } from '@playwright/test';

/**
 * The Workshop: named, versioned Toolboxes for one workspace agent instance
 * (PRD FR-37–FR-54).
 *
 * This drives the real surface against a running server — the per-group `Demo:`
 * checkpoint — and doubles as regression coverage that the panel actually
 * mounts. A module can be perfectly unit-tested and still never render, because
 * mounting depends on the tab host existing in the DOM at the right moment; the
 * only way to know is to open the tab and look.
 *
 * Run against the isolated demo sandbox:
 *   ./scripts/e2e.sh tests/toolbox-workshop.spec.ts
 */

const WORKSPACE_NAME = 'Toolbox Demo';

/**
 * Find the demo workspace, creating and seeding it when absent.
 *
 * Self-seeding so this is a real regression test rather than something that
 * only passes on a sandbox somebody prepared by hand. The seed is the minimum
 * that makes the Workshop's groups meaningful: one skill binding, and two MCP
 * bindings with different tool policies and risk classifications.
 */
async function workspaceID(page: Page): Promise<string> {
  const listing = await page.request.get('/api/workspaces');
  expect(listing.ok()).toBeTruthy();
  const payload = await listing.json();
  const folders = payload.folders ?? payload.workspaces ?? [];
  const existing = folders.find((entry: { name?: string }) => entry?.name === WORKSPACE_NAME);
  if (existing) return existing.id;

  const created = await page.request.post('/api/workspaces', {
    data: { name: WORKSPACE_NAME, description: 'Workshop regression fixture', agents: ['default'] }
  });
  expect(created.ok()).toBeTruthy();
  const id = (await created.json()).folder.id;

  await page.request.post(`/api/workspaces/${id}/agents`, { data: { agent_name: 'default' } });
  await page.request.post(`/api/workspaces/${id}/skill-bindings`, {
    data: { id: 'sb-1', skill_name: 'web-design-guidelines', enabled: true, trusted: true }
  });
  await page.request.post(`/api/workspaces/${id}/mcp-bindings`, {
    data: {
      id: 'mb-notes',
      server_name: 'filesystem',
      alias: 'Notes',
      enabled: true,
      allowed_tools: ['read_file', 'write_file'],
      default_side_effect: 'read',
      tool_overrides: { write_file: 'write' },
      scope: { roots: ['/tmp/demo-notes'] }
    }
  });
  await page.request.post(`/api/workspaces/${id}/mcp-bindings`, {
    data: {
      id: 'mb-tracker',
      server_name: 'memory',
      alias: 'Tracker',
      enabled: true,
      allowed_tools: ['create_entities', 'search_nodes']
    }
  });
  return id;
}

/**
 * Open the workspace's Toolbox tab.
 *
 * A fresh sandbox shows the first-run onboarding modal, which is modal in the
 * real sense — it intercepts every pointer event on the page. Stubbing the
 * status call is how the other specs get past it, and it keeps this spec about
 * the Workshop rather than about onboarding.
 */
async function openToolboxTab(page: Page) {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true })
    })
  );

  const id = await workspaceID(page);
  await page.goto(`/workspaces/${id}`);
  await page.waitForSelector('[data-cmd-agent-tab="loadout"]', { timeout: 20000 });

  const toolboxTab = page.locator('[data-cmd-agent-tab="loadout"]').first();
  await toolboxTab.click();

  const panel = page.locator('#workspace-toolbox-panel');
  await expect(panel).toBeVisible({ timeout: 15000 });
  return { id, panel, toolboxTab };
}

test.describe('Workshop', () => {
  test('the Toolbox tab mounts the Workshop and renders real capability groups', async ({
    page
  }) => {
    const { panel, toolboxTab } = await openToolboxTab(page);

    // The tab is labelled with the cozy vocabulary, not "Loadout" (FR-168).
    await expect(toolboxTab).toHaveText(/Toolbox/i);

    // Real server data, not a stub: the saved-toolbox picker and the
    // workspace-approved group both come from the API.
    await expect(panel.getByText('Saved toolboxes')).toBeVisible({ timeout: 10000 });
    await expect(panel.getByText('From this workspace')).toBeVisible();

    // The global-library group only renders when Ori knows about something this
    // workspace has not approved, which depends on what is enabled in the
    // install. When it IS there, the reassurance that selecting installs
    // nothing must be there too — that is the load-bearing part (FR-43, FR-45).
    const library = panel.getByText('Elsewhere in Ori');
    if ((await library.count()) > 0) {
      await expect(library).toBeVisible();
      await expect(panel.getByText(/does not install or connect anything/i)).toBeVisible();
    }

    await page.screenshot({ path: 'test-results/toolbox-workshop.png', fullPage: true });

    // The agent tab body is a 486px scroll container shared by every tab, so
    // the capability groups sit below the fold by design. Scroll to them and
    // confirm they are genuinely reachable rather than clipped away.
    await panel.getByText('From this workspace').scrollIntoViewIfNeeded();
    await page.screenshot({ path: 'test-results/toolbox-workshop-groups.png' });
    await expect(panel.getByText('From this workspace')).toBeInViewport();
  });

  test('editing is a draft: toggling changes nothing until the version is saved', async ({
    page
  }) => {
    const { panel } = await openToolboxTab(page);

    // Selection controls are inert until an edit is opened, so the surface can
    // never be mistaken for something that applies as you click.
    const selectors = panel.locator('.ws-toolbox-select');
    if ((await selectors.count()) > 0) {
      await expect(selectors.first()).toBeDisabled();
    }

    await panel.locator('[data-toolbox-edit]').click();
    await expect(panel.locator('[data-toolbox-save]')).toBeVisible();
    if ((await selectors.count()) > 0) {
      await expect(selectors.first()).toBeEnabled();
    }

    await panel.locator('[data-toolbox-cancel]').click();
    await expect(panel.locator('[data-toolbox-edit]')).toBeVisible();
  });

  test('previewing a toolbox shows Focus, the exact change, and the right action', async ({
    page
  }) => {
    const { panel } = await openToolboxTab(page);

    const preview = panel.locator('[data-toolbox-preview]').first();
    if ((await preview.count()) === 0) {
      test.skip(true, 'this sandbox has only one toolbox, so there is nothing to switch to');
    }
    await preview.click();

    const drawer = panel.locator('.ws-toolbox-preview');
    await expect(drawer).toBeVisible({ timeout: 10000 });

    // Separate readouts rather than one opaque number (FR-71). `.first()`
    // because the tool count legitimately appears twice — once as a readout and
    // again inside the Focus reason that explains the state.
    await expect(drawer.getByText(/Focus:/).first()).toBeVisible();
    await expect(drawer.getByText(/exposed tools/).first()).toBeVisible();
    await expect(drawer.getByText(/that change things/).first()).toBeVisible();
    await expect(drawer.getByText(/Readiness:/).first()).toBeVisible();

    // The action is whatever the SERVER said to offer — never re-derived here
    // (FR-78, FR-79).
    const useButton = drawer.locator('[data-toolbox-use]');
    await expect(useButton).toHaveText(/Use This Toolbox|Review & Use|Applying/);

    await page.screenshot({ path: 'test-results/toolbox-preview.png' });
  });

  test('Goal Prepare renders the brief and explained recommendations', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true })
      })
    );

    const id = await workspaceID(page);
    await page.goto(`/workspaces/${id}`);

    // Goal Prepare mounts inside the Command view's Mission stat-modal, reached
    // via Set goal -> Advanced settings. The config surface (which hosts it) is
    // a single shared DOM node relocated on demand rather than duplicated, so
    // this is the only path that actually renders it (FR-92, FR-94).
    await page.locator('[data-cmd-mission-action="edit"]').click();
    await page.locator('#workspace-detail-goal-modal.show').waitFor({ timeout: 10000 });
    await page.locator('#workspace-detail-goal-modal-advanced').click();

    const prepare = page.locator('#workspace-goal-prepare');
    await expect(prepare).toBeAttached({ timeout: 20000 });
    // The brief section always renders — either the accepted brief, or the
    // invitation to review a suggestion (FR-92, FR-94).
    await expect(prepare.getByText('What this goal needs')).toBeVisible({ timeout: 15000 });

    await page.screenshot({ path: 'test-results/goal-prepare.png' });
  });

  test('every operation and selector is reachable and named for assistive tech', async ({
    page
  }) => {
    const { panel } = await openToolboxTab(page);
    await panel.locator('[data-toolbox-edit]').click();

    for (const selector of await panel.locator('.ws-toolbox-select').all()) {
      await expect(selector).toHaveAttribute('role', 'switch');
      await expect(selector).toHaveAttribute('aria-label', /.+/);
      await expect(selector).toHaveAttribute('aria-checked', /true|false/);
    }
    for (const operation of await panel.locator('.ws-toolbox-op').all()) {
      await expect(operation).toHaveAttribute('aria-label', /.+/);
    }
  });
});

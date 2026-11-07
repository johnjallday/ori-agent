import { test, expect, Page } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// Current Ori routes and persisted workspaces in an isolated demo only. Optional
// program/surface declarations are labelled read fixtures; no creator is mocked.
test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      json: { needs_onboarding: false, completed: true, skipped: true }
    })
  );
});

async function workspace(page: Page, name = 'Quiet workspace') {
  const response = await page.request.post('/api/workspaces', {
    data: { name: `${name} ${Date.now()}`, blank: true, create_template_agents: true }
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  const body = await response.json();
  return body.folder ?? body.workspace ?? body;
}

async function open(page: Page, slug: string, query = '?mode=details') {
  await page.goto(`/workspaces/${slug}${query}`);
  await expect(page.locator('[data-cmd-give-work]')).toBeVisible({ timeout: 20000 });
  await page.waitForFunction(() => (window as any).workspaceDetail?.initialListsLoaded === true);
  await page.evaluate(() => document.fonts.ready);
}

async function capture(page: Page, name: string) {
  if (!process.env.WORKSPACE_UI_EVIDENCE) return;
  // Observe the actual entrance finishing; do not remove overlays or patch DOM.
  await expect
    .poll(() =>
      page.evaluate(() => {
        const header = document.querySelector('.ws-cmd-topbar');
        return header ? getComputedStyle(header).opacity : '';
      })
    )
    .toBe('1');
  await page.screenshot({ path: `tasks/evidence-workspace-ui-simplification/group-1/${name}.png` });
}

async function refresh(page: Page) {
  await page.evaluate(async () => {
    await (window as any).workspaceDetail.loadWorkspace();
  });
}

test('shell: shared capture preserves its draft through refresh and returns focus without writes', async ({
  page
}) => {
  const ws = await workspace(page);
  await open(page, ws.folder_slug);
  const writes: string[] = [];
  page.on('request', request => {
    // This pre-existing activation endpoint only registers directory watchers
    // (WorkspaceHandler.ActivateHandler), not runs or background-routine consent.
    const path = new URL(request.url()).pathname;
    if (
      request.method() !== 'GET' &&
      path !== '/api/orchestration/workspace/activate' &&
      /\/api\/(?:orchestration|workspaces\/.*\/(?:tickets|runs|continuity\/activate))/.test(path)
    )
      writes.push(path);
  });
  await expect(page.locator('[data-cmd-view-mode="details"]')).toHaveText('Overview');
  await page.locator('[data-cmd-give-work]').click();
  const dialog = page.locator('dialog.give-work');
  await expect(dialog).toBeVisible();
  await dialog.locator('[name="what"]').fill('Keep my unsaved capture');
  await capture(page, 'give-work');
  await refresh(page);
  await expect(dialog.locator('[name="what"]')).toHaveValue('Keep my unsaved capture');
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.locator('[data-cmd-give-work]')).toBeFocused();
  await page.locator('[data-cmd-settings]').click();
  await page.locator('[data-cmd-settings-owner="settings"]').click();
  await expect(page.getByRole('dialog', { name: 'Manager Settings', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Close manager' }).click();
  await expect(page.locator('[data-cmd-settings]')).toBeFocused();
  expect(writes).toEqual([]);
  for (const mode of ['map', 'tickets', 'details']) {
    await page.locator(`[data-cmd-view-mode="${mode}"]`).click();
    await expect(page.locator('[data-cmd-give-work]')).toHaveCount(1);
    await expect(page.locator('[data-cmd-map-give-work], #hubTicketsGiveWork')).toHaveCount(0);
  }
});

test('shell: secondary links remain native and disclosure Escape restores focus across refresh', async ({
  page,
  context
}) => {
  const ws = await workspace(page);
  await open(page, ws.folder_slug);
  const summary = page.locator('[data-cmd-disclosure="views"] > summary');
  await summary.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('link', { name: 'Plans', exact: true })).toBeVisible();
  await capture(page, 'secondary-navigation');
  await refresh(page);
  await expect(summary).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('link', { name: 'Plans', exact: true })).not.toBeVisible();
  await expect(summary).toBeFocused();
  await summary.click();
  const plans = page.getByRole('link', { name: 'Plans', exact: true });
  await expect(plans).toHaveAttribute('href', `/workspaces/${ws.folder_slug}/plans`);
  const [tab] = await Promise.all([
    context.waitForEvent('page'),
    plans.click({ modifiers: ['ControlOrMeta'] })
  ]);
  await tab.waitForURL(`**/workspaces/${ws.folder_slug}/plans`);
  await tab.close();
  expect(new URL(page.url()).pathname).toBe(`/workspaces/${ws.folder_slug}`);
});

test('shell: old details preference, Map history, Manager settings and Memory station arrivals', async ({
  page
}) => {
  const ws = await workspace(page);
  await open(page, ws.folder_slug, '?mode=details');
  await expect(page.locator('[data-cmd-view-mode="details"]')).toHaveAttribute(
    'aria-pressed',
    'true'
  );
  await page.locator('[data-cmd-view-mode="map"]').click();
  await expect(page).toHaveURL(/mode=map/);
  await page.goBack();
  await expect(page.locator('[data-cmd-view-mode="details"]')).toHaveAttribute(
    'aria-pressed',
    'true'
  );
  await page.reload();
  await expect(page.locator('[data-cmd-view-mode="details"]')).toHaveAttribute(
    'aria-pressed',
    'true'
  );
  await open(page, ws.folder_slug, '?panel=settings');
  await expect(page.getByRole('dialog', { name: 'Manager Settings', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Close manager' }).click();
  await open(page, ws.folder_slug, '?mode=map&station=memory');
  await expect(page.getByRole('dialog', { name: 'Memory', exact: true })).toBeVisible();
});

test('shell: declared non-music Home and catalog occupy one safe contribution host', async ({
  page
}) => {
  const ws = await workspace(page);
  const name = 'Research <and> planning '.repeat(4);
  await page.route(`**/api/workspaces/${ws.id}/assistant-program`, route =>
    route.fulfill({
      json: {
        available: true,
        is_station: true,
        hired: true,
        level: 2,
        stage_label: 'Collaborator',
        home_profile_title: 'Research profile',
        resource_workspace: true,
        declaration: { station_name: name }
      }
    })
  );
  await open(page, ws.folder_slug);
  for (const mode of ['details', 'map', 'tickets']) {
    await page.locator(`[data-cmd-view-mode="${mode}"]`).click();
    const link = page.locator('[data-assistant-program-entry]');
    await expect(link).toHaveCount(1);
    await expect(link).toHaveText(name.trim());
    await expect(link).toHaveAttribute('href', `/workspaces/${ws.folder_slug}/assistant`);
    await expect(page.locator('[data-home-profile-entry]')).toHaveCount(0);
    await expect(page.locator('[data-resource-catalog-entry]')).toHaveAttribute(
      'href',
      `/workspaces/${ws.folder_slug}/catalog`
    );
    if (mode === 'map') await capture(page, 'declared-destinations');
  }
});

test('shell: canonical ticket and filter arrivals keep their selection', async ({ page }) => {
  const ws = await workspace(page);
  const response = await page.request.post(`/api/workspaces/${ws.id}/tickets`, {
    data: { state: 'backlog', title: 'Preserved ticket arrival' }
  });
  expect(response.status(), await response.text()).toBe(201);
  const created = await response.json();
  const ticket = created.ticket ?? created;
  await open(page, ws.folder_slug, `?ticket=${ticket.id}`);
  await expect(page.locator('[data-cmd-view-mode="tickets"]')).toHaveAttribute(
    'aria-pressed',
    'true'
  );
  await expect(page.locator('#hubTicketDetailTitle')).toContainText('Preserved ticket arrival');
  await refresh(page);
  await expect(page.locator('#hubTicketDetailTitle')).toContainText('Preserved ticket arrival');
  await open(page, ws.folder_slug, '?tickets=backlog');
  await expect(page.locator('[data-ticket-state-filter="backlog"]')).toHaveAttribute(
    'aria-pressed',
    'true'
  );
  await expect(page.locator('#hubTicketsList')).toContainText('Preserved ticket arrival');
});

test('shell: HQ specialization and unavailable Dashboard stay reachable under shared chrome', async ({
  page
}) => {
  const statusResponse = await page.request.get('/api/personal-hq/status');
  expect(statusResponse.ok()).toBeTruthy();
  const status = (await statusResponse.json()).status;
  let ws = status?.workspace;
  if (!ws?.folder_slug) {
    ws = await workspace(page, 'Header HQ');
    const designated = await page.request.post('/api/personal-hq/designate', {
      data: { workspace_id: ws.id }
    });
    expect(designated.ok(), await designated.text()).toBeTruthy();
  }
  // Catalog-only read fixture: unavailable dashboard is not a successful iframe.
  await page.route(`**/api/workspaces/${ws.id}/surfaces`, route =>
    route.fulfill({
      json: {
        surfaces: [
          {
            key: 'user:ori.dashboard:dashboard:main',
            placement: 'workspace_view',
            available: false,
            description: 'Fixture dashboard is unreadable',
            plugin: { id: 'ori.dashboard' }
          }
        ]
      }
    })
  );
  await open(page, ws.folder_slug);
  await expect(page.locator('.ws-hq-main')).toBeVisible();
  await expect(page.locator('[data-cmd-view-mode="dashboard"]')).toBeVisible();
  await page.locator('[data-cmd-view-mode="dashboard"]').click();
  await expect(page.locator('[data-cmd-dashboard-status]')).toContainText(
    'Fixture dashboard is unreadable'
  );
  await expect(page.locator('[data-cmd-give-work]')).toBeVisible();
  await page.locator('[data-cmd-view-mode="details"]').click();
  await expect(page.locator('.ws-hq-main')).toBeVisible();
  await capture(page, 'hq');
});

test('shell: reviewed group shares the same compact Overview and Map header', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const name = `Studio projects ${Date.now()}`;
  const planResponse = await page.request.post('/api/workspaces/template-agent-plan', {
    data: { group_roster: true, group_name: name }
  });
  expect(planResponse.ok()).toBeTruthy();
  const plan = await planResponse.json();
  const manager = plan.agents[0];
  const response = await page.request.post('/api/workspaces', {
    data: {
      name,
      kind: 'group',
      group_roster: true,
      create_template_agents: true,
      template_agent_review: {
        version: 1,
        plan_revision: plan.revision,
        expectations: [{ index: 0, name: manager.name, action: manager.action }]
      }
    }
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  const body = await response.json();
  const ws = body.folder ?? body.workspace ?? body;
  await open(page, ws.folder_slug);
  await expect(page.locator('[data-cmd-settings]')).toBeVisible();
  await capture(page, 'group-overview');
  await page.locator('[data-cmd-view-mode="map"]').click();
  await expect(page.locator('.ws-cmd-map-shell.is-group')).toBeVisible();
  await expect(page.locator('[data-cmd-detachment-build]')).toBeVisible();
  await capture(page, 'group-map');
});

for (const width of [320, 390, 768, 1440]) {
  test(`shell: quiet header and long identity reflow at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    const ws = await workspace(page);
    await open(page, ws.folder_slug);
    // Resolve and measure in the same browser turn: initial async sources may
    // replace the header between a locator's resolution and its evaluate call.
    const height = await page.evaluate(
      () => document.querySelector('.ws-cmd-topbar')!.getBoundingClientRect().height
    );
    expect(height).toBeGreaterThan(0);
    if (width === 1440) expect(height).toBeLessThanOrEqual(220);
    test.info().annotations.push({ type: 'header-height', description: `${width}px: ${height}px` });
    if (process.env.WORKSPACE_UI_EVIDENCE) console.info(`Quiet header ${width}x900: ${height}px`);
    if (process.env.WORKSPACE_UI_EVIDENCE && [390, 1440].includes(width)) {
      await capture(page, `overview-${width}`);
      await page.locator('[data-cmd-view-mode="map"]').click();
      await expect(page.locator('.ws-cmd-map-shell')).toBeVisible();
      await capture(page, `map-${width}`);
    }
    await expect(page.locator('[data-cmd-settings]')).toBeVisible();
    for (const key of ['settings', 'views']) {
      const disclosure = page.locator(`[data-cmd-disclosure="${key}"]`);
      await disclosure.locator('summary').click();
      const menu = disclosure.locator('.ws-cmd-disclosure-body');
      await expect(menu).toBeVisible();
      await expect
        .poll(() =>
          menu.evaluate(node => {
            const rect = node.getBoundingClientRect();
            return rect.left >= 0 && rect.right <= innerWidth;
          })
        )
        .toBe(true);
      await disclosure.locator('summary').press('Escape');
    }
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)
    ).toBeLessThanOrEqual(0);
    const longName = `${width}${Date.now()}${'Long'.repeat(25)}`;
    const changed = await page.request.patch(`/api/workspaces/${ws.id}`, {
      data: { name: longName }
    });
    expect(changed.ok(), await changed.text()).toBeTruthy();
    await refresh(page);
    await expect(page.locator('.ws-cmd-title h2')).toHaveText(longName);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)
    ).toBeLessThanOrEqual(0);
  });
}

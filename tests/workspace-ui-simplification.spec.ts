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
  await page.screenshot({
    path: `tasks/evidence-workspace-ui-simplification/${name.startsWith('panels-') ? 'group-2' : 'group-1'}/${name}.png`
  });
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
  await page.locator('[data-panel-section="automation"]').click();
  await page
    .getByRole('button', { name: 'Manager behavior & planning policy', exact: true })
    .click();
  await expect(page.getByRole('dialog', { name: 'Manager Settings', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Close manager' }).click();
  await page.getByRole('button', { name: 'Close workspace panel' }).click();
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

test('panels: General edits keep drafts through refresh and save at the real endpoint', async ({
  page
}) => {
  const ws = await workspace(page);
  await open(page, ws.folder_slug);
  await page.locator('[data-cmd-settings]').click();
  const panel = page.getByRole('dialog', { name: 'Workspace settings', exact: true });
  await expect(panel).toBeVisible();
  await panel.locator('#ws-panel-description').fill('Unsaved <literal> description');
  await refresh(page);
  await expect(panel.locator('#ws-panel-description')).toHaveValue('Unsaved <literal> description');
  await panel.getByRole('button', { name: 'Save description', exact: true }).click();
  await expect(panel.locator('[data-panel-status]')).toHaveText('Saved.');
  await capture(page, 'panels-general');
  const stored = await (await page.request.get(`/api/workspaces/${ws.id}`)).json();
  expect((stored.folder ?? stored.workspace ?? stored).description).toBe(
    'Unsaved <literal> description'
  );
  await refresh(page);
  await expect(panel.locator('#ws-panel-description')).toHaveValue('Unsaved <literal> description');
  await panel.getByRole('button', { name: 'Close workspace panel' }).click();
  await expect(page.locator('[data-cmd-settings]')).toBeFocused();
});

test('panels: dirty Escape is guarded and Resources child owners suspend rather than stack', async ({
  page
}) => {
  const ws = await workspace(page);
  await open(page, ws.folder_slug);
  await page.locator('[data-cmd-settings]').click();
  const panel = page.getByRole('dialog', { name: 'Workspace settings', exact: true });
  await panel.locator('#ws-panel-name').fill('Unsaved name');
  page.once('dialog', dialog => dialog.dismiss());
  await panel.press('Escape');
  await expect(panel).toBeVisible();
  await expect(panel.locator('#ws-panel-name')).toHaveValue('Unsaved name');
  page.once('dialog', dialog => dialog.accept());
  await panel.getByRole('button', { name: 'Close workspace panel' }).click();
  await page.locator('[data-cmd-resources]').click();
  const resources = page.getByRole('dialog', { name: 'Resources', exact: true });
  await expect(resources).toBeVisible();
  for (const key of ['files', 'sessions', 'memory']) {
    await page.locator(`[data-panel-section="${key}"]`).click();
    await expect(resources).toBeVisible();
    await capture(page, `panels-resources-${key}`);
  }
  await page.getByRole('button', { name: 'Open memory manager', exact: true }).click();
  await expect(resources).not.toBeVisible();
  await expect(page.getByRole('dialog', { name: 'Memory', exact: true })).toBeVisible();
  await refresh(page);
  await page.getByRole('button', { name: 'Close manager' }).click();
  await expect(resources).toBeVisible();
  await resources.getByRole('button', { name: 'Close workspace panel' }).click();
  await expect(page.locator('[data-cmd-resources]')).toBeFocused();
});

test('panels: config-owner intent drafts survive background refresh without a repeated initialization', async ({
  page
}) => {
  const ws = await workspace(page);
  await open(page, ws.folder_slug);
  await page.locator('[data-cmd-settings]').click();
  await page.getByRole('button', { name: 'Intent & setup context', exact: true }).click();
  const intent = page.getByRole('dialog', { name: 'Workspace Intent', exact: true });
  await expect(intent).toBeVisible();
  await intent.locator('#workspace-detail-intent-context').fill('Keep this context draft');
  await refresh(page);
  await expect(intent.locator('#workspace-detail-intent-context')).toHaveValue(
    'Keep this context draft'
  );
  page.once('dialog', dialog => dialog.accept());
  await page.getByRole('button', { name: 'Close manager' }).click();
  await expect(page.getByRole('dialog', { name: 'Workspace settings', exact: true })).toBeVisible();
});

test('panels: failed save leaves a literal draft and opening tool/automation sections performs no operation', async ({
  page
}) => {
  const ws = await workspace(page);
  await open(page, ws.folder_slug);
  await page.route(`**/api/workspaces/${ws.id}`, route =>
    route.request().method() === 'PATCH'
      ? route.fulfill({ status: 500, json: { error: 'Fixture save failure' } })
      : route.continue()
  );
  await page.locator('[data-cmd-settings]').click();
  const panel = page.getByRole('dialog', { name: 'Workspace settings', exact: true });
  await panel.locator('#ws-panel-description').fill('<script>literal draft</script>');
  await panel.getByRole('button', { name: 'Save description', exact: true }).click();
  await expect(panel.locator('[data-panel-status]')).toContainText('Could not save');
  await expect(panel.locator('#ws-panel-description')).toHaveValue(
    '<script>literal draft</script>'
  );
  page.once('dialog', dialog => dialog.accept());
  await panel.locator('[data-panel-section="automation"]').click();
  const writes: string[] = [];
  page.on('request', request => {
    const path = new URL(request.url()).pathname;
    if (
      request.method() !== 'GET' &&
      /\/(?:execute|run|activate|install|approve|transition|enable)$/.test(path)
    )
      writes.push(path);
  });
  await panel.locator('[data-panel-section="tools"]').click();
  await panel.getByRole('button', { name: 'Skills', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'Tools', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Close manager' }).click();
  await expect(panel.getByRole('button', { name: 'Skills', exact: true })).toBeFocused();
  await panel.locator('[data-panel-section="advanced"]').click();
  await expect(panel.getByRole('link', { name: 'Diagnostics', exact: true })).toBeVisible();
  expect(writes).toEqual([]);
});

test('panels: real note editor suspends Resources and returns to its actual opener', async ({
  page
}) => {
  const ws = await workspace(page);
  await open(page, ws.folder_slug);
  await page.locator('[data-cmd-resources]').click();
  const resources = page.getByRole('dialog', { name: 'Resources', exact: true });
  await resources.getByRole('button', { name: 'New note', exact: true }).click();
  const editor = page.locator('#noteEditorModal');
  await expect(editor).toBeVisible();
  await expect(resources).not.toBeVisible();
  await refresh(page);
  await expect(editor).toBeVisible();
  await editor.getByRole('button', { name: 'Close note editor', exact: true }).click();
  await expect(resources).toBeVisible();
  await expect(resources.getByRole('button', { name: 'New note', exact: true })).toBeFocused();
});

test('panels: imported routines consent survives same-version refresh and rejects stale or unavailable data', async ({
  page
}) => {
  const ws = await workspace(page);
  let version = 3;
  let failed = false;
  const writes: string[] = [];
  await page.route(`**/api/workspaces/${ws.id}/continuity`, route =>
    route.fulfill({
      status: failed ? 500 : 200,
      json: failed
        ? {}
        : {
            success: true,
            continuity: {
              workspace_id: ws.id,
              state: 'ready',
              checkpoint_at: '2026-10-21T05:42:48Z',
              attachment: 'imported_inactive',
              imported: true,
              background_allowed: false,
              manual_allowed: true,
              attachment_version: version,
              tree_ready: true
            }
          }
    })
  );
  page.on('request', request => {
    if (request.method() === 'POST' && request.url().includes('/continuity/activate'))
      writes.push(request.url());
  });
  await open(page, ws.folder_slug);
  await expect(page.locator('.ws-cmd-topbar #workspaceContinuityChip')).toContainText(
    'routines off'
  );
  await page.locator('[data-cmd-settings]').click();
  await page.locator('[data-panel-section="advanced"]').click();
  await page.getByRole('button', { name: 'Moving this workspace', exact: true }).click();
  const dialog = page.locator('#workspaceContinuityDialog');
  await expect(dialog).toBeVisible();
  await dialog.locator('#workspaceContinuityActivateBtn').click();
  await expect(dialog.locator('#workspaceContinuityConfirm')).toBeVisible();
  await page.evaluate(() => (window as any).WorkspaceContinuityStatus.refresh());
  await refresh(page);
  await expect(dialog.locator('#workspaceContinuityConfirm')).toBeVisible();
  await capture(page, 'panels-imported-consent');
  version = 4;
  await page.evaluate(() => (window as any).WorkspaceContinuityStatus.refresh());
  await expect(dialog.locator('#workspaceContinuityConfirm')).not.toBeVisible();
  failed = true;
  await page.evaluate(() => (window as any).WorkspaceContinuityStatus.refresh());
  await expect(dialog.locator('#workspaceContinuityDialogLabel')).toContainText('unavailable');
  await dialog.locator('#workspaceContinuityCloseBtn').click();
  await expect(page.getByRole('dialog', { name: 'Workspace settings', exact: true })).toBeVisible();
  expect(writes).toEqual([]);
});

test('panels: quiet setup success lives in Advanced while owner details remain available', async ({
  page
}) => {
  const ws = await workspace(page);
  // Controlled read/owner-open acknowledgement fixture, not a performed setup.
  const status = {
    workspace_id: ws.id,
    applicable: true,
    state: 'ready',
    blueprint_id: 'host-fixture',
    blueprint_name: 'Host fixture',
    title: 'Host setup details',
    current_step_id: 'summary',
    dismissed: true,
    auto_open: false,
    steps: [
      {
        id: 'summary',
        kind: 'summary',
        title: 'Setup complete',
        required: true,
        status: 'completed'
      }
    ]
  };
  await page.route(`**/api/workspaces/${ws.id}/setup-wizard`, route =>
    route.fulfill({ json: { setup: status } })
  );
  await page.route(`**/api/workspaces/${ws.id}/setup-wizard/open`, route =>
    route.fulfill({ json: { setup: status } })
  );
  await open(page, ws.folder_slug);
  await expect(page.locator('#setupWizardBanner')).not.toBeVisible();
  await page.locator('[data-cmd-settings]').click();
  await page.locator('[data-panel-section="advanced"]').click();
  await capture(page, 'panels-advanced');
  await page.getByRole('button', { name: 'Setup & template details', exact: true }).click();
  await expect(page.locator('#setupWizardDialog')).toBeVisible();
  await expect(page.locator('#setupWizardTitle')).toHaveText('Host setup details');
  await page.locator('#setupWizardClose').click();
  await expect(page.getByRole('dialog', { name: 'Workspace settings', exact: true })).toBeVisible();
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
    for (const key of ['views']) {
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

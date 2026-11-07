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

async function reviewedGroup(page: Page) {
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
    path: `tasks/evidence-workspace-ui-simplification/${name.startsWith('panels-') ? 'group-2' : name.startsWith('work-') ? 'group-3' : name.startsWith('field-') ? 'group-4' : name.startsWith('journey-') ? 'group-5' : 'group-1'}/${name}.png`
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

for (const variant of ['leaf', 'group', 'hq']) {
  for (const width of [390, 1440]) {
    test(`menu stacking: ${variant} at ${width}px keeps More views above content, below dialogs`, async ({
      page,
      context
    }) => {
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript(
        theme => localStorage.setItem('ori-theme', theme),
        variant === 'hq' ? 'dark' : 'light'
      );
      await page.emulateMedia({ reducedMotion: 'no-preference' });
      const ws =
        variant === 'group' ? await reviewedGroup(page) : await workspace(page, `Menu ${variant}`);
      if (variant === 'hq') {
        // Controlled designation read only; no real HQ/model setup is claimed.
        await page.route(`**/api/orchestration/workspace?id=${ws.id}`, async route => {
          const response = await route.fetch();
          await route.fulfill({ json: { ...(await response.json()), designation: 'personal_hq' } });
        });
      }
      await open(page, ws.folder_slug);
      const disclosure = page.locator('[data-cmd-disclosure="views"]');
      const summary = disclosure.locator('summary');
      const assertHitTargets = async () => {
        for (const name of ['Plans', 'Canvas']) {
          await expect
            .poll(
              () =>
                page.evaluate(label => {
                  // Query and hit-test the live DOM in one turn: background
                  // status reads may replace chrome between locator actions.
                  const menu = document.querySelector<HTMLDetailsElement>(
                    '[data-cmd-disclosure="views"]'
                  );
                  const node = Array.from(menu?.querySelectorAll('a') || []).find(
                    link => link.textContent?.trim() === label
                  );
                  if (!menu?.open || !node) return false;
                  const box = node.getBoundingClientRect();
                  return [0.25, 0.75].every(x =>
                    [0.25, 0.75].every(y => {
                      const top = document.elementFromPoint(
                        box.left + box.width * x,
                        box.top + box.height * y
                      );
                      return top === node || node.contains(top);
                    })
                  );
                }, name),
              {
                message: `${name} must be the real pointer target, not merely visible behind page furniture`
              }
            )
            .toBe(true);
        }
      };
      for (const motion of ['no-preference', 'reduce'] as const) {
        await page.emulateMedia({ reducedMotion: motion });
        for (const mode of ['details', 'tickets', 'map']) {
          await page.locator(`[data-cmd-view-mode="${mode}"]`).click();
          await summary.click();
          await assertHitTargets();
          await refresh(page);
          await assertHitTargets();
          if (motion === 'no-preference' && mode === 'map')
            await capture(page, `journey-more-views-${variant}-${width}`);
          await summary.press('Escape');
        }
      }
      if (variant === 'hq') {
        await page.locator('[data-cmd-map-arrangement="free"]').click();
        await summary.click();
        await assertHitTargets();
        await summary.press('Escape');
      }
      await summary.click();
      const [canvas] = await Promise.all([
        context.waitForEvent('page'),
        disclosure
          .getByRole('link', { name: 'Canvas', exact: true })
          .click({ modifiers: ['ControlOrMeta'] })
      ]);
      await canvas.waitForURL(`**/workspaces/${ws.folder_slug}/canvas`);
      await canvas.close();
      // Raising a header menu must not let it paint over a real modal owner.
      await page.locator('[data-cmd-settings]').click();
      const panel = page.getByRole('dialog', { name: 'Workspace settings', exact: true });
      await expect(panel).toBeVisible();
      await panel.getByRole('button', { name: 'Close workspace panel', exact: true }).click();
      await expect(panel).toBeHidden();
    });
  }
}

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
  // Observe the real Bootstrap lifecycle; visible during fade is not yet
  // dismissible. Never force an early close or remove its backdrop.
  const shown = page.evaluate(
    () =>
      new Promise<boolean>(resolve => {
        document
          .getElementById('noteEditorModal')!
          .addEventListener('shown.bs.modal', () => resolve(true), { once: true });
      })
  );
  await resources.getByRole('button', { name: 'New note', exact: true }).click();
  await shown;
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

test('overview: leaf opens on current work, compact Team and Resources without a default goal or deck', async ({
  page
}) => {
  const ws = await workspace(page);
  await open(page, ws.folder_slug);
  await expect(page.locator('.ws-cmd-team')).toBeVisible();
  await expect(page.locator('#workspace-command-mission-card')).toHaveCount(0);
  await expect(page.locator('.ws-cmd-garrison')).toHaveCount(0);
  await expect(page.locator('.ws-cmd-linked-workspaces')).toHaveCount(0);
  await capture(page, 'work-quiet-overview');
  await page.locator('[data-cmd-team-agent]').first().click();
  await expect(page.locator('.ws-cmd-garrison .ws-cmd-deck')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Manager Settings', exact: true })).toBeVisible();
  await capture(page, 'work-contextual-team');
  await page.locator('[data-cmd-team-toggle]').click();
  const memory = page.locator('[data-cmd-resource-section="memory"]');
  await memory.click();
  await expect(
    page.getByRole('button', { name: 'Open memory manager', exact: true })
  ).toBeVisible();
  await refresh(page);
  await page.getByRole('button', { name: 'Close workspace panel' }).click();
  await expect(memory).toBeFocused();
});

test('overview: groups expose the real ordered member owner and one inert Add workspace menu', async ({
  page
}) => {
  const group = await reviewedGroup(page);
  const member = await workspace(page, 'Linked member');
  const linked = await page.request.patch(`/api/workspaces/${member.id}`, {
    data: { parent_id: group.id }
  });
  expect(linked.ok(), await linked.text()).toBeTruthy();
  await open(page, group.folder_slug);
  const section = page.getByRole('region', { name: 'Linked workspaces', exact: true });
  await expect(section.locator('.group-member-name')).toContainText('Linked member');
  await expect(section.locator('[data-member-remove]')).toBeVisible();
  await capture(page, 'work-group-overview');
  const treeReads: string[] = [];
  page.on('request', request => {
    if (request.url().endsWith('/api/workspaces?tree=true')) treeReads.push(request.url());
  });
  await page.evaluate(() => {
    for (let i = 0; i < 3; i++) (window as any).workspaceCommand.render();
  });
  expect(treeReads).toEqual([]);
  await section.getByRole('button', { name: 'Member inventory', exact: true }).click();
  await expect(section.locator('#workspace-detail-members-rollups')).toContainText(
    'Member tickets'
  );
  await section.locator('[data-cmd-add-workspace] > summary').click();
  await expect(section.getByRole('button', { name: 'Create new', exact: true })).toBeVisible();
  await section.getByRole('button', { name: 'Add existing', exact: true }).click();
  await expect(section.locator('#workspace-detail-member-picker')).toBeVisible();
  const picker = section.locator('[data-member-add-select]');
  await picker.focus();
  const selected = await picker.inputValue();
  await refresh(page);
  await expect(picker).toHaveValue(selected);
  await expect(picker).toBeFocused();
  await expect(section.locator('[data-cmd-add-workspace]')).not.toHaveAttribute('open', '');
});

test('overview: attention keeps ongoing work visible and Team offers every actual agent', async ({
  page
}) => {
  const ws = await workspace(page);
  const name = `Review specialist ${Date.now()}`;
  const agent = await page.request.post('/api/agents', { data: { name, type: 'orchestration' } });
  expect(agent.ok(), await agent.text()).toBeTruthy();
  const attached = await page.request.post(`/api/workspaces/${ws.id}/agents`, {
    data: { agent_name: name }
  });
  expect(attached.ok(), await attached.text()).toBeTruthy();
  // Controlled status response; there is no actual run or live model judgment.
  await page.route('**/api/orchestration/tasks?**', route =>
    route.fulfill({
      json: {
        tasks: [
          { id: 'fixture-blocked', status: 'blocked', description: 'Review the staged result' },
          {
            id: 'fixture-running',
            status: 'in_progress',
            description: 'Routine work still running'
          }
        ]
      }
    })
  );
  await open(page, ws.folder_slug);
  const work = page.getByRole('region', { name: 'Next action', exact: true });
  await expect(work).toContainText('Review the staged result');
  await expect(work.locator('.ws-cmd-work-context')).toContainText(/progress|running/i);
  await expect(page.locator('[data-cmd-team-agent]')).toHaveCount(2);
  await capture(page, 'work-review-and-running');
});

test('overview: an explicitly agentless workspace retains its real hire and team controls', async ({
  page
}) => {
  const response = await page.request.post('/api/workspaces', {
    data: { name: `Agentless overview ${Date.now()}`, blank: true, create_template_agents: false }
  });
  expect(response.ok()).toBeTruthy();
  const ws = (await response.json()).folder;
  await open(page, ws.folder_slug);
  await expect(page.locator('[data-cmd-team-agent]')).toHaveCount(0);
  await expect(page.locator('.ws-cmd-team-empty [data-cmd-add-agent]')).toBeVisible();
  const writes: string[] = [];
  page.on('request', request => {
    if (
      request.method() !== 'GET' &&
      /\/api\/(?:agents|workspaces\/.*\/(?:roles|tickets|runs))/.test(
        new URL(request.url()).pathname
      )
    )
      writes.push(request.url());
  });
  // Preserve and decline the existing setup confirmation normally; never
  // remove its backdrop or manufacture a staffed workspace to bypass it.
  const setup = page.getByRole('dialog', {
    name: 'Hire a Commander for this workspace?',
    exact: true
  });
  await expect(setup).toBeVisible();
  await setup.getByRole('button', { name: 'Not now', exact: true }).click();
  await expect(setup).toBeHidden();
  await page.locator('[data-cmd-team-toggle]').click();
  await expect(page.locator('.ws-cmd-garrison [data-cmd-add-agent]')).toBeVisible();
  expect(writes).toEqual([]);
  await capture(page, 'work-unstaffed');
});

test('overview: unavailable goal and resource sources remain explicitly unknown', async ({
  page
}) => {
  const ws = await workspace(page);
  await page.route(`**/api/workspaces/${ws.id}/mission`, route =>
    route.fulfill({ status: 503, json: { error: 'Fixture unreadable goal' } })
  );
  await page.route(`**/api/workspaces/${ws.id}/notes`, route =>
    route.fulfill({ status: 503, json: {} })
  );
  await open(page, ws.folder_slug);
  await expect(page.locator('#workspace-command-mission-card')).toContainText('Goal unavailable');
  await expect(page.locator('[data-cmd-resource-section="notes"]')).toContainText('Unknown');
  await page.locator('[data-cmd-resource-section="notes"]').click();
  await expect(page.locator('[data-panel-content]')).toContainText('Notes are not yet available');
});

test('map: group camera and selected member survive Overview and Tickets round trips', async ({
  page
}) => {
  const group = await reviewedGroup(page);
  const member = await workspace(page, 'Map member');
  const linked = await page.request.patch(`/api/workspaces/${member.id}`, {
    data: { parent_id: group.id }
  });
  expect(linked.ok()).toBeTruthy();
  await page.setViewportSize({ width: 1440, height: 900 });
  await open(page, group.folder_slug, '?mode=map');
  const map = page.locator('.ws-cmd-opmap.is-combined');
  await expect(map.locator(`[data-ws-id="${member.id}"]`)).toBeVisible();
  await map.locator(`[data-ws-id="${member.id}"]`).click();
  await map.locator('[data-map-zoom-out]').click();
  const before = await page.evaluate(() => ({
    camera: (window as any).OriWorkspaceMap.getCamera(),
    selected: (window as any).OriWorkspaceMap.getSelectedId()
  }));
  expect(before.selected).toBe(member.id);
  for (const mode of ['details', 'tickets']) {
    await page.locator(`[data-cmd-view-mode="${mode}"]`).click();
    await page.locator('[data-cmd-view-mode="map"]').click();
    await expect(map.locator(`[data-ws-id="${member.id}"]`)).toBeVisible();
    await expect
      .poll(() =>
        page.evaluate(() => ({
          camera: (window as any).OriWorkspaceMap.getCamera(),
          selected: (window as any).OriWorkspaceMap.getSelectedId()
        }))
      )
      .toEqual(before);
  }
  await page.locator('[data-cmd-map-resources]').click();
  await expect(page.getByRole('dialog', { name: 'Resources', exact: true })).toBeVisible();
  await refresh(page);
  await page.getByRole('button', { name: 'Close workspace panel' }).click();
  await expect(page.locator('[data-cmd-map-resources]')).toBeFocused();
  await map.locator('[data-cmd-hq-station="memory"]').click();
  await expect(page.getByRole('dialog', { name: 'Memory', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Close manager' }).click();
  await capture(page, 'field-group-map');
  const geometry = await page.evaluate(() => {
    const rect = document.querySelector('.ws-cmd-opmap.is-combined')!.getBoundingClientRect();
    return Math.min(rect.bottom, innerHeight) - Math.max(rect.top, 0);
  });
  expect(geometry).toBeGreaterThanOrEqual(450);
  console.log(
    `[workspace-ui] ${test.info().project.name} quiet group map: ${geometry}px visible at 1440×900`
  );
});

test('map: Add workspace delegates linking to the real picker without creating on open', async ({
  page
}) => {
  const group = await reviewedGroup(page);
  const candidate = await workspace(page, 'Existing map candidate');
  await page.setViewportSize({ width: 390, height: 900 });
  await open(page, group.folder_slug, '?mode=map');
  const writes: string[] = [];
  page.on('request', request => {
    if (request.method() !== 'GET' && request.url().includes('/api/workspaces'))
      writes.push(request.url());
  });
  await page.locator('[data-cmd-add-workspace] > summary').click();
  expect(writes).toEqual([]);
  await page.locator('[data-cmd-group-link]').click();
  const picker = page.locator('[data-cmd-detachment-picker]');
  await picker.locator('[data-member-add-select]').selectOption(candidate.id);
  await picker.locator('[data-member-add-select]').focus();
  await refresh(page);
  await expect(picker.locator('[data-member-add-select]')).toHaveValue(candidate.id);
  await expect(picker.locator('[data-member-add-select]')).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(picker).toBeHidden();
  await expect(page.locator('[data-cmd-add-workspace] > summary')).toBeFocused();
  expect(writes).toEqual([]);
  await page.keyboard.press('Enter');
  await page.locator('[data-cmd-group-link]').click();
  await picker.locator('[data-member-add-select]').selectOption(candidate.id);
  await picker.locator('[data-member-add-confirm]').click();
  await expect(
    page.locator(`.ws-cmd-detachment-host [data-ws-id="${candidate.id}"]`)
  ).toBeVisible();
  const stored = await (await page.request.get(`/api/workspaces/${candidate.id}`)).json();
  expect((stored.folder ?? stored.workspace ?? stored).parent_id).toBe(group.id);
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)
  ).toBeLessThanOrEqual(0);
  await page.locator('.ws-cmd-opmap.is-combined').scrollIntoViewIfNeeded();
  await capture(page, 'field-group-mobile');
});

test('map: failed ticket reads stay actionable rather than showing idle readiness', async ({
  page
}) => {
  const ws = await workspace(page, 'Unavailable map work');
  await page.route(`**/api/orchestration/tasks?workspace_id=${ws.id}`, route =>
    route.fulfill({ status: 503, json: { error: 'Controlled unreadable work source' } })
  );
  await open(page, ws.folder_slug, '?mode=map');
  const next = page.getByRole('region', { name: 'Next action', exact: true });
  // A saved welcome/update may legitimately outrank this source qualification.
  await expect(next).toContainText('Tickets are unavailable.');
  await expect(
    next.getByRole('button', { name: 'Retry workspace status', exact: true })
  ).toBeVisible();
  await expect(next).not.toHaveClass(/is-quiet/);
  await capture(page, 'field-unavailable-work');
});

test('map: failed membership is not an empty group and retries through the existing owner', async ({
  page
}) => {
  const group = await reviewedGroup(page);
  let failed = true;
  await page.route('**/api/workspaces?tree=true', route =>
    failed
      ? route.fulfill({ status: 503, json: { error: 'Controlled unreadable tree' } })
      : route.continue()
  );
  await open(page, group.folder_slug, '?mode=map');
  const toolbar = page.getByRole('toolbar', { name: 'Group map controls', exact: true });
  await expect(toolbar.getByLabel('Workspace count unavailable')).toHaveText('—');
  await expect(page.locator('.ws-cmd-detachment-host')).toContainText(
    'Workspace map is loading or unavailable'
  );
  await expect(page.locator('.ws-cmd-map-group-bar.is-empty')).toHaveCount(0);
  await expect(page.locator('.ws-cmd-detachment-host [data-ws-map-viewport]')).toHaveCount(0);
  await capture(page, 'field-unavailable-members');
  failed = false;
  await toolbar.getByRole('button', { name: 'Retry membership', exact: true }).click();
  await expect(page.locator('.ws-cmd-detachment-host [data-ws-map-viewport]')).toBeVisible();
  await expect(toolbar.getByLabel('0 members', { exact: true })).toHaveText('0');
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
  await capture(page, 'work-hq');
});

test('shell: reviewed group shares the same compact Overview and Map header', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const ws = await reviewedGroup(page);
  await open(page, ws.folder_slug);
  await expect(page.locator('[data-cmd-settings]')).toBeVisible();
  await capture(page, 'group-overview');
  await page.locator('[data-cmd-view-mode="map"]').click();
  await expect(page.locator('.ws-cmd-map-shell.is-group')).toBeVisible();
  await expect(page.locator('[data-cmd-add-workspace] > summary')).toBeVisible();
  await capture(page, 'group-map');
});

async function tabTo(page: Page, target: ReturnType<Page['locator']>) {
  // Safari's default macOS keyboard mode uses Option-Tab to include buttons
  // and links. Exercise that native gesture, not injected tabindex/style fixes.
  const key = page.context().browser()?.browserType().name() === 'webkit' ? 'Alt+Tab' : 'Tab';
  for (let step = 0; step < 100; step++) {
    if (await target.evaluate(node => node === document.activeElement)) return;
    await page.keyboard.press(key);
  }
  throw new Error('Keyboard focus could not reach ' + target.toString());
}

test('journeys: a real delayed ticket refresh retains keyboard row focus', async ({ page }) => {
  const ws = await workspace(page, 'Ticket row focus');
  const seeded = await page.request.post(`/api/workspaces/${ws.id}/tickets`, {
    data: { title: 'Focused ticket', state: 'backlog' }
  });
  expect(seeded.ok()).toBeTruthy();
  await open(page, ws.folder_slug);
  await page.locator('[data-cmd-view-mode="tickets"]').click();
  const button = page.locator('.ticket-card-open').filter({ hasText: 'Focused ticket' });
  await expect(button).toBeVisible();
  let release!: () => void;
  let started!: () => void;
  const held = new Promise<void>(resolve => {
    release = resolve;
  });
  const reading = new Promise<void>(resolve => {
    started = resolve;
  });
  // Delay an actual read response, without changing the server data or app state.
  await page.route(
    url => url.pathname === `/api/workspaces/${ws.id}/tickets`,
    async route => {
      const response = await route.fetch();
      started();
      await held;
      await route.fulfill({ response });
    }
  );
  try {
    await page.locator('#hubTicketsRefreshBtn').click();
    await reading;
    await tabTo(page, button);
    const old = (await button.elementHandle())!;
    release();
    await expect.poll(() => old.evaluate(node => node.isConnected)).toBe(false);
    await expect(button).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page.locator('#hubTicketDetailTitle')).toContainText('Focused ticket');
  } finally {
    release();
  }
});

for (const theme of ['light', 'dark']) {
  for (const width of [320, 390, 768, 1440]) {
    test(`journeys: keyboard capture and ticket draft/history stay canonical (${theme}, ${width}px)`, async ({
      page
    }) => {
      await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.setViewportSize({ width, height: 900 });
      const ws = await workspace(page, 'Keyboard journey');
      await open(page, ws.folder_slug);
      const runs: string[] = [];
      page.on('request', request => {
        if (
          request.method() !== 'GET' &&
          /\/(?:runs|execute|dispatch|mission\/first-open)(?:\/|$)/.test(
            new URL(request.url()).pathname
          )
        )
          runs.push(request.url());
      });
      const door = page.locator('[data-cmd-give-work]');
      await tabTo(page, door);
      await expect(door).toHaveCSS('outline-style', 'solid');
      await page.keyboard.press('Enter');
      const dialog = page.locator('dialog.give-work');
      await expect(dialog.locator('[name="who"]')).toHaveValue('');
      await expect(dialog.locator('[name="startNow"]')).not.toBeChecked();
      const title = `Keyboard ticket ${theme} ${width}`;
      await dialog.locator('[name="what"]').fill(title);
      const createdResponse = page.waitForResponse(
        response =>
          new URL(response.url()).pathname === `/api/workspaces/${ws.id}/tickets` &&
          response.request().method() === 'POST'
      );
      await tabTo(page, dialog.locator('[data-give-work-submit]'));
      await page.keyboard.press('Enter');
      const response = await createdResponse;
      expect(response.status(), await response.text()).toBe(201);
      const created = await response.json();
      const ticket = created.ticket ?? created;
      expect(ticket.state).toBe('backlog');
      await expect(dialog).toHaveCount(0);
      await expect(door).toBeFocused();
      await tabTo(page, page.locator('[data-cmd-view-mode="tickets"]'));
      await page.keyboard.press('Enter');
      const card = page.locator(`#hubTicketsList .ticket-card[data-ticket-id="${ticket.id}"]`);
      await expect(card).toBeVisible();
      await expect(card.locator('.ticket-card-number')).toHaveText(`#${ticket.number}`);
      await tabTo(page, card.locator('.ticket-card-open'));
      await page.keyboard.press('Enter');
      await expect(page.locator('#hubTicketDetailTitle')).toContainText(title);
      await tabTo(page, page.locator('.ticket-detail-edit > summary'));
      await page.keyboard.press('Enter');
      const details = page.locator('.ticket-detail-edit [name="description"]');
      await details.fill('Keep this real ticket draft across refresh.');
      const draftNode = (await details.elementHandle())!;
      await refresh(page);
      expect(await details.evaluate((node, original) => node === original, draftNode)).toBe(true);
      await expect(details).toHaveValue('Keep this real ticket draft across refresh.');
      await tabTo(
        page,
        page.locator('.ticket-detail-edit').getByRole('button', { name: 'Save', exact: true })
      );
      const savedResponse = page.waitForResponse(
        response =>
          new URL(response.url()).pathname === `/api/workspaces/${ws.id}/tickets/${ticket.id}` &&
          response.request().method() === 'PATCH'
      );
      await page.keyboard.press('Enter');
      expect((await savedResponse).ok()).toBe(true);
      await tabTo(page, page.locator('[data-cmd-view-mode="map"]'));
      await page.keyboard.press('Enter');
      await expect(page.locator('[data-cmd-view-mode="map"]')).toHaveAttribute(
        'aria-pressed',
        'true'
      );
      await page.goBack();
      await expect(page.locator('[data-cmd-view-mode="tickets"]')).toHaveAttribute(
        'aria-pressed',
        'true'
      );
      await expect(page.locator('#hubTicketDetailTitle')).toContainText(title);
      await page.goForward();
      await expect(page.locator('[data-cmd-view-mode="map"]')).toHaveAttribute(
        'aria-pressed',
        'true'
      );
      const stored = await (
        await page.request.get(`/api/workspaces/${ws.id}/tickets/${ticket.id}`)
      ).json();
      expect(stored.id).toBe(ticket.id);
      expect(stored.number).toBe(ticket.number);
      expect(stored.state).toBe('backlog');
      expect(stored.description).toBe('Keep this real ticket draft across refresh.');
      expect(runs).toEqual([]);
      await expect(door).toHaveCount(1);
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)
      ).toBeLessThanOrEqual(0);
      if (width === 390 || width === 1440) await capture(page, `journey-${theme}-${width}`);
    });
  }
}

for (const theme of ['light', 'dark']) {
  test(`journeys: zoom-equivalent reflow preserves Tickets filters, Settings and focus (${theme})`, async ({
    page
  }) => {
    await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    // A 1440×900 layout at 200% has a 720×450 CSS viewport; do not patch
    // application styles or hide fixed furniture to manufacture reflow.
    await page.setViewportSize({ width: 720, height: 450 });
    const ws = await workspace(page, 'Zoom-equivalent reflow');
    await open(page, ws.folder_slug);
    const ticketButton = page.locator('[data-cmd-view-mode="tickets"]');
    await tabTo(page, ticketButton);
    await page.keyboard.press('Enter');
    await tabTo(page, page.locator('[data-cmd-view-mode="map"]'));
    await page.keyboard.press('Enter');
    await expect(page.locator('[data-cmd-view-mode="map"]')).toHaveAttribute(
      'aria-pressed',
      'true'
    );
    await page.goBack();
    await expect(ticketButton).toHaveAttribute('aria-pressed', 'true');
    const backlog = page.locator('[data-ticket-state-filter="backlog"]');
    await tabTo(page, backlog);
    await page.keyboard.press('Enter');
    await expect(backlog).toHaveAttribute('aria-pressed', 'true');
    await tabTo(page, page.locator('[data-cmd-settings]'));
    await page.keyboard.press('Enter');
    const panel = page.getByRole('dialog', { name: 'Workspace settings', exact: true });
    await expect(panel).toBeVisible();
    const bounds = await panel.boundingBox();
    expect(bounds!.y).toBeGreaterThanOrEqual(0);
    expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(450);
    await page.keyboard.press('Escape');
    await expect(panel).toBeHidden();
    await expect(page.locator('[data-cmd-settings]')).toBeFocused();
    await page.goBack();
    await expect(backlog).toHaveAttribute('aria-pressed', 'false');
    await page.goForward();
    await expect(backlog).toHaveAttribute('aria-pressed', 'true');
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)
    ).toBeLessThanOrEqual(0);
    await capture(page, `journey-zoom-${theme}`);
  });
}

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

import { test, expect, Page } from '@playwright/test';

/**
 * E2E for the create-workspace "Agent behavior" consolidation.
 *
 * Covers, on the live /workspaces (sessions.js) host:
 *   - the Starting-point grid renders and maps to Agent behavior,
 *   - a manual override is preserved across card switches,
 *   - reopening the modal resets the override + Advanced disclosure,
 *   - createFolder() submits workspace_preset on both create and import.
 *
 * Run against a running server, e.g.:
 *   PLAYWRIGHT_BASE_URL=http://localhost:8765 \
 *     npx playwright test tests/create-workspace-behavior.spec.ts
 * (Not part of CI — CI runs `npm run test:modules`.)
 */

async function openCreateModal(page: Page) {
  await page.evaluate(() => {
    const el = document.getElementById('addFolderModal');
    // @ts-expect-error bootstrap is a page global
    window.bootstrap.Modal.getOrCreateInstance(el).show();
  });
  await expect(page.locator('#addFolderModal')).toBeVisible();
  // The show handler renders the unified Template picker.
  await expect(cardByLabel(page, 'Blank')).toBeVisible();
  await expect(cardByLabel(page, 'Research Project')).toBeVisible();
  await expect(cardByLabel(page, 'Travels')).toBeVisible();
  await expect(cardByLabel(page, 'Content Production')).toBeVisible();
}

async function advanceToWorkspaceDetails(page: Page) {
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
}

// Create mode is Blueprint → Details → Team → Review. Details requires a
// workspace name before Team, so callers must fill it first.
async function advanceToTeam(page: Page) {
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();
}

async function advanceToReview(page: Page) {
  await advanceToTeam(page);
  await advanceToReviewFromTeam(page);
}

// For tests that already interacted with Team and just need the last hop.
async function advanceToReviewFromTeam(page: Page) {
  const batch = page.locator('[data-team-accept-all]');
  if (await batch.isVisible()) await batch.click();
  const pendingRows = page
    .locator('#workspaceTeamRoster .workspace-team-row')
    .filter({ hasText: 'New · Needs setup' });
  while ((await pendingRows.count()) > 0) {
    await pendingRows.first().locator('[data-team-agent-setup]').click();
    await page.locator('#workspaceAgentSetupSave').click();
  }
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep4')).toBeVisible();
}

async function returnToBlueprints(page: Page) {
  await page.evaluate(() => {
    (
      window as unknown as { sessionManager: { goToWizardStep: (step: number) => void } }
    ).sessionManager.goToWizardStep(1);
  });
  await expect(page.locator('#wizardStep1')).toBeVisible();
}

function cardByLabel(page: Page, label: string) {
  return page.locator('#templatePicker .workspace-template-card').filter({
    has: page.locator('.workspace-template-card-label', { hasText: new RegExp(`^${label}$`) })
  });
}

async function routeProjectEntryTemplates(page: Page) {
  await page.route('**/api/project-templates', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        templates_root: '/tmp/templates',
        templates: [
          {
            id: 'research-project',
            name: 'Research Project',
            builtin: true,
            behavior_profile: 'research'
          },
          { id: 'travels', name: 'Travels', builtin: true, behavior_profile: 'general' },
          {
            id: 'content-production',
            name: 'Content Production',
            builtin: true,
            behavior_profile: 'general'
          },
          {
            id: 'auto-project',
            name: 'Auto Project',
            description: 'Template with automatic project opening.',
            builtin: true,
            behavior_profile: 'general',
            project_entry: { relative_path: '{{name}}.rpp', open_after_create_default: true }
          },
          {
            id: 'manual-project',
            name: 'Manual Project',
            description: 'Template with optional project opening.',
            builtin: true,
            behavior_profile: 'general',
            project_entry: { relative_path: '{{name}}.rpp', open_after_create_default: false }
          },
          { id: 'no-entry', name: 'No Entry', builtin: true, behavior_profile: 'general' }
        ]
      })
    });
  });
  await page.route('**/api/workspaces/template-agent-plan', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ has_agents: false, agents: [], warnings: [] })
    });
  });
}

async function stubWorkspaceReview(page: Page) {
  await page.evaluate(() => {
    const w = window as unknown as { WorkspaceBootstrapReview?: Record<string, unknown> };
    const r = (w.WorkspaceBootstrapReview = w.WorkspaceBootstrapReview || {});
    r.ensureReviewed = async () => ({ ready: true });
    r.applyPlan = async () => ({
      invitedAgents: 0,
      boundMCPs: 0,
      attachedSkills: 0,
      addedPlugins: 0,
      failures: []
    });
  });
}

test.beforeEach(async ({ page }) => {
  // Skip the first-run onboarding server-side so its modal (a static-backdrop
  // overlay that animates in) can't intercept create-modal clicks.
  await page.request.post('/api/onboarding/skip').catch(() => {});
  await page.goto('/workspaces');
});

test('Create mode shows four ordered steps and marks only the active one current', async ({
  page
}) => {
  await openCreateModal(page);

  const stepper = page.locator('#wizardStepper');
  await expect(stepper).toBeVisible();
  await expect(stepper.locator('.workspace-create-step')).toHaveText([
    /1\s*Blueprint/,
    /2\s*Details/,
    /3\s*Team/,
    /4\s*Review/
  ]);

  // Exactly one step is aria-current at a time, and it tracks navigation (FR3).
  const current = stepper.locator('.workspace-create-step[aria-current="step"]');
  await expect(current).toHaveCount(1);
  await expect(current).toHaveAttribute('data-step', '1');
  await expect(
    page.locator('#wizardStep1 .workspace-wizard-step-heading .workspace-wizard-eyebrow')
  ).toHaveText('Step 1 of 4');

  await advanceToWorkspaceDetails(page);
  await expect(current).toHaveCount(1);
  await expect(current).toHaveAttribute('data-step', '2');
  await page.locator('#folderNameInput').fill('Four Step WS');

  await advanceToTeam(page);
  await expect(current).toHaveAttribute('data-step', '3');
  await expect(page.locator('#wizardStep3Title')).toHaveText('Build your workspace team');
  // The final create action appears only on Review (FR11).
  await expect(page.locator('#createFolderBtn')).toBeHidden();

  await advanceToReviewFromTeam(page);
  await expect(current).toHaveAttribute('data-step', '4');
  await expect(page.locator('#wizardStep4Title')).toHaveText('Ready to create?');
  await expect(page.locator('#createFolderBtn')).toBeVisible();
  await expect(page.locator('#wizardNextBtn')).toBeHidden();

  // Back returns to Team and preserves the entered name (FR8).
  await page.locator('#wizardBackBtn').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();
  await page.locator('#wizardBackBtn').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await expect(page.locator('#folderNameInput')).toHaveValue('Four Step WS');
});

test('Details blocks continuing to Team until the workspace is named', async ({ page }) => {
  await openCreateModal(page);
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('');

  await page.locator('#wizardNextBtn').click();

  // Stays on Details, explains why, and puts focus on the field to fix (FR27, FR28).
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await expect(page.locator('#wizardStep3')).toBeHidden();
  await expect(page.locator('#workspaceNameHint')).toContainText('Workspace name is required');
  await expect(page.locator('#folderNameInput')).toBeFocused();

  await page.locator('#folderNameInput').fill('Now Named');
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();
});

test('Details blocks a folder slug an existing workspace already uses (FR28)', async ({ page }) => {
  // The workspace list the modal compares against is loaded from
  // /api/workspaces?tree=true; match the query string explicitly so this stub
  // cannot swallow the create POST to the bare /api/workspaces path.
  await page.route('**/api/workspaces?tree=true*', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        folders: [{ id: 'ws-1', name: 'Taken Name', kind: 'workspace', folder_slug: 'taken-name' }],
        workspaces: []
      })
    });
  });
  await page.reload();

  await openCreateModal(page);
  await advanceToWorkspaceDetails(page);
  // Different capitalisation and punctuation, same resulting folder.
  await page.locator('#folderNameInput').fill('taken NAME');
  await page.locator('#wizardNextBtn').click();

  await expect(page.locator('#wizardStep2')).toBeVisible();
  await expect(page.locator('#wizardStep3')).toBeHidden();
  await expect(page.locator('#workspaceNameHint')).toContainText('already uses the folder');
  await expect(page.locator('#folderNameInput')).toBeFocused();

  await page.locator('#folderNameInput').fill('Fresh Name');
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();
});

test('Import mode stays single-step and never exposes Team, Review, or the stepper', async ({
  page
}) => {
  await page.evaluate(() => {
    const el = document.getElementById('addFolderModal');
    if (el) el.dataset.pendingImportMode = 'true';
    // @ts-expect-error bootstrap is a page global
    window.bootstrap.Modal.getOrCreateInstance(el).show();
  });
  await expect(page.locator('#addFolderModal')).toBeVisible();

  // Import renders the details layout only (FR12).
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await expect(page.locator('#wizardStepper')).toBeHidden();
  await expect(page.locator('#wizardStep1')).toBeHidden();
  await expect(page.locator('#wizardStep3')).toBeHidden();
  await expect(page.locator('#wizardStep4')).toBeHidden();
  await expect(page.locator('#wizardNextBtn')).toBeHidden();
  await expect(page.locator('#wizardBackBtn')).toBeHidden();
  await expect(page.locator('#folderImportSection')).toBeVisible();
  // Import submits immediately from its single step.
  await expect(page.locator('#createFolderBtn')).toBeVisible();
  await expect(page.locator('#createFolderBtn')).toHaveText('Import Folder');
});

test('starting point sets Agent behavior; manual override is preserved; reopen resets', async ({
  page
}) => {
  await openCreateModal(page);
  await advanceToWorkspaceDetails(page);

  const sel = page.locator('#folderPresetSelect');
  const hint = page.locator('#folderBehaviorHint');
  const disclosure = page.locator('#folderAdvancedDisclosure');

  // Initial state: Blank -> general, collapsed.
  await expect(sel).toHaveValue('general');
  await expect(hint).toHaveText('Agent behavior: General');
  await expect(disclosure).toHaveJSProperty('open', false);

  // Pick Research Project -> research, hint updates (value/hint update even
  // while the Advanced section is collapsed).
  await returnToBlueprints(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await expect(sel).toHaveValue('research');
  await expect(hint).toHaveText('Agent behavior: Research');

  // Expand Advanced to manually override (a real user opens it first).
  await page.locator('#folderAdvancedDisclosure .workspace-advanced-summary').click();
  await expect(disclosure).toHaveJSProperty('open', true);
  await expect(sel).toBeVisible();

  // Manual override -> software_project.
  await sel.selectOption('software_project');
  await expect(hint).toHaveText('Agent behavior: Software Project');

  // Pick Travels (maps to general) -> override is preserved.
  await returnToBlueprints(page);
  await cardByLabel(page, 'Travels').click();
  await advanceToWorkspaceDetails(page);
  await expect(sel).toHaveValue('software_project');

  // Close + reopen -> reset to defaults, disclosure collapsed.
  await page.evaluate(() => {
    const el = document.getElementById('addFolderModal');
    // @ts-expect-error bootstrap is a page global
    window.bootstrap.Modal.getInstance(el)?.hide();
  });
  await expect(page.locator('#addFolderModal')).toBeHidden();

  await openCreateModal(page);
  await advanceToWorkspaceDetails(page);
  await expect(sel).toHaveValue('general');
  await expect(hint).toHaveText('Agent behavior: General');
  await expect(disclosure).toHaveJSProperty('open', false);
});

test('switching starting points updates auto-filled name, but never typed input', async ({
  page
}) => {
  await openCreateModal(page);
  const name = page.locator('#folderNameInput');

  // First pick fills the name.
  await cardByLabel(page, 'Travels').click();
  await expect(name).toHaveValue('Travels');

  // Regression: switching cards must update the auto-filled name (it used to
  // stick on the first pick because the field was no longer empty).
  await cardByLabel(page, 'Content Production').click();
  await expect(name).toHaveValue('Content Production');

  await advanceToWorkspaceDetails(page);

  // Typed input is never clobbered by a later card switch.
  await name.fill('My Custom Name');
  await returnToBlueprints(page);
  await cardByLabel(page, 'Research Project').click();
  await expect(name).toHaveValue('My Custom Name');

  // Blank clears an auto-filled value (clean slate) but leaves typed input.
  await advanceToWorkspaceDetails(page);
  await name.fill('');
  await returnToBlueprints(page);
  await cardByLabel(page, 'Travels').click();
  await expect(name).toHaveValue('Travels');
  await cardByLabel(page, 'Blank').click();
  await expect(name).toHaveValue('');
});

test('project-open option follows template defaults and resets for non-library flows', async ({
  page
}) => {
  await routeProjectEntryTemplates(page);
  await openCreateModal(page);

  const panel = page.locator('#projectTemplateOpenAfterCreate');
  const toggle = page.locator('#projectTemplateOpenAfterCreateToggle');
  await expect(panel).toBeHidden();
  await expect(toggle).not.toBeChecked();

  // "Open project after creation" is a mutable pre-create control, so it lives on
  // Details (FR29) — Review only summarizes the choice.
  await cardByLabel(page, 'Auto Project').click();
  await advanceToWorkspaceDetails(page);
  await expect(panel).toBeVisible();
  await expect(toggle).toBeChecked();

  await toggle.uncheck();
  await returnToBlueprints(page);
  await cardByLabel(page, 'Manual Project').click();
  await advanceToWorkspaceDetails(page);
  await expect(panel).toBeVisible();
  await expect(toggle).not.toBeChecked();
  await toggle.check();

  // Every template change reapplies that template's own default.
  await returnToBlueprints(page);
  await cardByLabel(page, 'Auto Project').click();
  await advanceToWorkspaceDetails(page);
  await expect(toggle).toBeChecked();
  await returnToBlueprints(page);
  await cardByLabel(page, 'Manual Project').click();
  await advanceToWorkspaceDetails(page);
  await expect(toggle).not.toBeChecked();

  await returnToBlueprints(page);
  await cardByLabel(page, 'No Entry').click();
  await advanceToWorkspaceDetails(page);
  await expect(panel).toBeHidden();
  await expect(toggle).not.toBeChecked();

  // An ad-hoc path overrides the selected library template and clears launch.
  await returnToBlueprints(page);
  await cardByLabel(page, 'Auto Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderAdvancedDisclosure .workspace-advanced-summary').click();
  await page.locator('#projectTemplatePathInput').fill('/tmp/ad-hoc-template');
  await expect(panel).toBeHidden();
  await expect(toggle).not.toBeChecked();

  // Import mode never carries a launch choice.
  await page.evaluate(() => {
    (
      window as unknown as { sessionManager: { setImportModeEnabled: (enabled: boolean) => void } }
    ).sessionManager.setImportModeEnabled(true);
  });
  await expect(panel).toBeHidden();
  await expect(toggle).not.toBeChecked();

  await page.evaluate(() => {
    const el = document.getElementById('addFolderModal');
    // @ts-expect-error bootstrap is a page global
    window.bootstrap.Modal.getInstance(el)?.hide();
  });
  await expect(page.locator('#addFolderModal')).toBeHidden();
  await openCreateModal(page);
  await advanceToWorkspaceDetails(page);
  await expect(panel).toBeHidden();
  await expect(toggle).not.toBeChecked();
});

test('a launch-default project supports keyboard opt-out and never opens on reload', async ({
  page
}) => {
  await routeProjectEntryTemplates(page);
  let openCalls = 0;
  await page.route('**/api/workspaces/**/project/open', async route => {
    openCalls += 1;
    await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Auto Project').click();
  await advanceToWorkspaceDetails(page);

  // Details owns the launch toggle (FR29); no need to reach Review to set it.
  const panel = page.locator('#projectTemplateOpenAfterCreate');
  const toggle = page.locator('#projectTemplateOpenAfterCreateToggle');
  await expect(panel).toBeVisible();
  await expect(toggle).toBeChecked();

  await toggle.focus();
  await page.keyboard.press('Space');
  await expect(toggle).not.toBeChecked();
  await expect(toggle).toBeFocused();

  await page.reload();
  await expect.poll(() => openCalls).toBe(0);
});

test('checked project-open option posts exactly once after create and before navigation', async ({
  page
}) => {
  await routeProjectEntryTemplates(page);
  await openCreateModal(page);
  await stubWorkspaceReview(page);

  const calls: string[] = [];
  await page.route('**/api/workspaces/created-open/project/open', async route => {
    calls.push('open');
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ message: 'ok' })
    });
  });
  await page.route('**/api/workspaces', async route => {
    calls.push('create');
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        folder: { id: 'created-open', folder_slug: 'created-open' },
        seeded_starter_tasks: 0
      })
    });
  });

  await cardByLabel(page, 'Auto Project').click();
  await advanceToWorkspaceDetails(page);
  // Assert the launch choice on the step that owns it, then continue to Review,
  // which is the only step with the final create action (FR11).
  await expect(page.locator('#projectTemplateOpenAfterCreateToggle')).toBeChecked();
  await advanceToReview(page);
  await page.locator('#createFolderBtn').click();
  await page.waitForURL('**/workspaces/created-open');

  await expect.poll(() => calls).toEqual(['create', 'open']);
});

test('unchecked project-open option creates and navigates without an open request', async ({
  page
}) => {
  await routeProjectEntryTemplates(page);
  await openCreateModal(page);
  await stubWorkspaceReview(page);

  let openCalls = 0;
  await page.route('**/api/workspaces/created-closed/project/open', async route => {
    openCalls += 1;
    await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' });
  });
  await page.route('**/api/workspaces', async route => {
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        folder: { id: 'created-closed', folder_slug: 'created-closed' },
        seeded_starter_tasks: 0
      })
    });
  });

  await cardByLabel(page, 'Manual Project').click();
  await advanceToWorkspaceDetails(page);
  await expect(page.locator('#projectTemplateOpenAfterCreateToggle')).not.toBeChecked();
  await advanceToReview(page);
  await page.locator('#createFolderBtn').click();
  await page.waitForURL('**/workspaces/created-closed');
  await expect.poll(() => openCalls).toBe(0);
});

test('project-open failure still navigates and shows a one-time retry notice', async ({ page }) => {
  await routeProjectEntryTemplates(page);
  await openCreateModal(page);
  await stubWorkspaceReview(page);

  let openCalls = 0;
  await page.route('**/api/workspaces/created-failure/project/open', async route => {
    openCalls += 1;
    await route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'No default app is available.' })
    });
  });
  await page.route('**/api/workspaces', async route => {
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        folder: { id: 'created-failure', folder_slug: 'created-failure' },
        seeded_starter_tasks: 0
      })
    });
  });

  await cardByLabel(page, 'Auto Project').click();
  await advanceToWorkspaceDetails(page);
  await advanceToReview(page);
  await page.locator('#createFolderBtn').click();
  await page.waitForURL('**/workspaces/created-failure');
  await expect.poll(() => openCalls).toBe(1);
  const storedNotice = await page.evaluate(() =>
    window.sessionStorage.getItem('oriProjectOpenNotice:created-failure')
  );
  expect(storedNotice).toContain('Use Open Project to try again');

  // The workspace-detail module consumes this one-time receipt. Its focused
  // module test covers that rendering; this route-only browser fixture has no
  // stored workspace detail to mount after navigation.
  await page.evaluate(() =>
    window.sessionStorage.removeItem('oriProjectOpenNotice:created-failure')
  );
  await page.reload();
  await expect.poll(() => openCalls).toBe(1);
});

test('createFolder submits workspace_preset for create and import', async ({ page }) => {
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ has_agents: false, agents: [], warnings: [] })
    });
  });
  await openCreateModal(page);

  // Stub the "Review Setup" gate so createFolder reaches the POST without an
  // LLM round-trip (the gate triggers whenever a description is present).
  await page.evaluate(() => {
    const w = window as unknown as { WorkspaceBootstrapReview?: Record<string, unknown> };
    const r = (w.WorkspaceBootstrapReview = w.WorkspaceBootstrapReview || {});
    r.ensureReviewed = async () => ({ ready: true });
    r.applyPlan = async () => ({
      invitedAgents: 0,
      boundMCPs: 0,
      attachedSkills: 0,
      addedPlugins: 0,
      failures: []
    });
  });

  const captured: Record<string, string | undefined> = {};
  // Register import first so the more specific path wins for that URL.
  await page.route('**/api/workspaces/import', async route => {
    captured.import = route.request().postDataJSON()?.workspace_preset;
    await route.fulfill({
      status: 400,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'stubbed' })
    });
  });
  await page.route('**/api/workspaces', async route => {
    captured.create = route.request().postDataJSON()?.workspace_preset;
    await route.fulfill({
      status: 400,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'stubbed' })
    });
  });

  // CREATE: Research Project -> expect workspace_preset 'research'.
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.fill('#folderNameInput', 'E2E Preset WS');
  await page.fill('#folderDescriptionInput', 'e2e preset submission test');
  await page.evaluate(() =>
    (
      window as unknown as { sessionManager: { createFolder: () => Promise<void> } }
    ).sessionManager.createFolder()
  );
  await expect.poll(() => captured.create).toBe('research');

  // IMPORT: enable import mode + a path, then submit again.
  await page.evaluate(() => {
    const sm = (window as unknown as { sessionManager: Record<string, unknown> }).sessionManager;
    sm.importModeEnabled = true;
    const toggle = document.getElementById('folderImportToggle') as HTMLInputElement | null;
    if (toggle) toggle.checked = true;
    const pathInput = document.getElementById('folderImportPathInput') as HTMLInputElement | null;
    if (pathInput) pathInput.value = '/tmp/e2e-folder';
  });
  await page.evaluate(() =>
    (
      window as unknown as { sessionManager: { createFolder: () => Promise<void> } }
    ).sessionManager.createFolder()
  );
  await expect.poll(() => captured.import).toBe('research');
});

test('Team attaches a saved agent and submits the complete team atomically', async ({ page }) => {
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ has_agents: false, agents: [], warnings: [] })
    });
  });
  await page.route('**/api/agents/dashboard/list**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        agents: [
          { name: 'Research Scout', model: 'gpt-5.5', workspace_count: 2 },
          { name: 'Data Miner', model: 'gpt-5.5-mini', workspace_count: 0 }
        ]
      })
    });
  });
  await openCreateModal(page);
  await cardByLabel(page, 'Blank').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Atomic Team');
  await advanceToTeam(page);

  // The Your Agents picker is inline on Team and loads on arrival — no button
  // press and no nested modal (FR56).
  await expect(page.locator('#existingAgentRosterPanel')).toBeVisible();
  const roster = page.locator('#workspaceTeamRoster');
  await page.locator('[data-existing-agent-add="Research Scout"]').click();
  await expect(roster.locator('.workspace-team-row').first()).toContainText('Research Scout');
  await expect(roster.locator('.workspace-team-badge.is-primary')).toHaveText('Primary');
  await expect(roster).toContainText('Saved agent · will be attached');

  // Buttons are the whole interaction — there is no drop zone to fall back on.
  await expect(page.locator('#workspaceTeamDropZone')).toHaveCount(0);
  await page.locator('[data-existing-agent-add="Data Miner"]').click();
  await expect(roster.locator('.workspace-team-row')).toHaveCount(2);
  await expect(roster).toContainText('Data Miner');

  let payload: Record<string, unknown> | undefined;
  await page.route('**/api/workspaces', async route => {
    payload = route.request().postDataJSON();
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        folder: { id: 'atomic-team', folder_slug: 'atomic-team' },
        seeded_starter_tasks: 0
      })
    });
  });
  // Create lives only on Review (FR11), and names the workspace (FR88).
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep4')).toBeVisible();
  await expect(page.locator('#createFolderBtn')).toHaveText('Create “Atomic Team”');
  await page.locator('#createFolderBtn').click();
  await page.waitForURL('**/workspaces/atomic-team');
  expect(payload?.existing_agent_names).toEqual(['Research Scout', 'Data Miner']);
  expect(payload?.entry_agent_name).toBe('Research Scout');
});

test('Team visualizes every included template agent and its lifecycle', async ({ page }) => {
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'lifecycle-plan-1',
        has_agents: true,
        agents: [
          {
            name: 'Research Lead',
            scope: 'reusable',
            action: 'reuse',
            entry_point: true,
            role: 'orchestrator',
            model: 'gpt-5.3-codex',
            provider: 'codex',
            model_source: 'existing'
          },
          {
            name: 'Source Scout',
            scope: 'reusable',
            action: 'reuse',
            entry_point: false,
            role: 'specialist',
            model_source: 'agent_default'
          },
          {
            name: 'Synthesis Writer',
            scope: 'reusable',
            action: 'create',
            entry_point: false,
            role: 'specialist',
            model_source: 'agent_default'
          }
        ],
        warnings: []
      })
    });
  });
  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  // The blueprint roster is edited on Team, not Review (FR32, FR83).
  await advanceToTeam(page);

  // ONE roster, primary first, everything else a Specialist (FR32-FR35).
  const rows = page.locator('#workspaceTeamRoster .workspace-team-row');
  await expect(rows).toHaveCount(3);
  await expect(page.locator('#templateAgentReview')).toHaveCount(0);
  await expect(rows.nth(0)).toContainText('Research Lead');
  await expect(rows.nth(0).locator('.workspace-team-badge')).toHaveText('Primary');
  await expect(rows.nth(1).locator('.workspace-team-badge')).toHaveText('Specialist');
  await expect(rows.nth(2).locator('.workspace-team-badge')).toHaveText('Specialist');

  // Source, readiness, and future action are all explicit in text.
  await expect(rows.nth(0)).toContainText('Saved · Ready to attach');
  await expect(rows.nth(0)).toContainText('Your Agents · Ready · Will attach saved definition');
  await expect(rows.nth(2)).toContainText('New · Needs setup');
  await expect(rows.nth(2)).toContainText('Blueprint · Needs setup · Will create with workspace');
  await expect(rows.nth(2).locator('.workspace-team-readiness-badge')).toHaveText('Missing');
  await expect(rows.nth(0).locator('.workspace-team-readiness-badge')).toHaveCount(0);
  await expect(page.locator('#workspaceTeamRoster')).not.toContainText(
    'already saved and attached'
  );
  await expect(page.locator('#workspaceTeamRoster')).not.toContainText(
    'Added to Your Agents and attached'
  );

  // Resolved model information is shown per row (FR36).
  await expect(rows.nth(0)).toContainText('codex / gpt-5.3-codex');

  // The action is named for what it does, not for copying (FR42).
  await expect(rows.nth(0).locator('[data-team-agent-setup]')).toHaveText('Customize as new agent');
  await expect(page.locator('#workspaceTeamRoster')).not.toContainText('Make a workspace copy');
});

test('proposed agent setup stays inside Create Workspace and submits one strict atomic request', async ({
  page
}) => {
  const unexpectedPosts: string[] = [];
  page.on('request', request => {
    if (
      request.method() === 'POST' &&
      (request.url().endsWith('/api/agents') ||
        request.url().includes('/api/workspaces/template-agent-create'))
    ) {
      unexpectedPosts.push(request.url());
    }
  });
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'reviewed-plan-1',
        has_agents: true,
        template_id: 'reaper-song',
        template_name: 'Reaper Song',
        entry_agent_name: 'Reaper Producer',
        agents: [
          {
            name: 'Reaper Producer',
            scope: 'reusable',
            action: 'create',
            entry_point: true,
            role: 'orchestrator',
            type: 'general',
            model: 'gpt-5.3-codex',
            provider: 'codex',
            reasoning_effort: 'high',
            system_prompt: 'Produce the session.',
            model_source: 'system',
            tools: { skills: ['reaper-session'], mcp_servers: ['reaper'] }
          }
        ],
        warnings: []
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Reviewed Session');
  await advanceToTeam(page);

  const row = page.locator('#workspaceTeamRoster .workspace-team-row');
  await expect(row).toContainText('New · Needs setup');
  await expect(row.locator('[data-team-agent-setup]')).toHaveText('Set up agent');
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();
  const requiredSetupAction = row.locator('[data-team-agent-setup]');
  await expect(requiredSetupAction).toBeFocused();
  await expect(requiredSetupAction).toHaveClass(/is-blocking-attention/);
  await expect(row).toHaveClass(/is-blocking-attention/);
  await expect(page.locator('#workspaceTeamLiveRegion')).toContainText(
    'Focus moved to the required control.'
  );

  await requiredSetupAction.click();
  await expect(page.locator('#workspaceTeamLayout')).toBeHidden();
  await expect(page.locator('#workspaceAgentSetupView')).toBeVisible();
  await expect(page.locator('#addFolderModal .modal.show')).toHaveCount(0);
  await expect(page.locator('#workspaceAgentSetupTitle')).toHaveText('Set up agent');
  await expect(page.locator('#workspaceAgentSetupName')).toHaveValue('Reaper Producer');
  await expect(page.locator('#workspaceAgentSetupReasoning')).toBeDisabled();
  await expect(page.locator('#workspaceAgentSetupSummary')).toContainText('reaper-session');
  await expect(page.locator('#workspaceAgentSetupSystemPrompt')).toHaveValue(
    'Produce the session.'
  );

  await page.locator('#workspaceAgentSetupName').fill('Session Producer');
  await page.locator('#workspaceAgentSetupSystemPrompt').fill('Produce this session carefully.');
  await page.locator('#workspaceAgentSetupSave').click();
  await expect(page.locator('#workspaceTeamLayout')).toBeVisible();
  await expect(row).toContainText('Session Producer');
  await expect(row).toContainText('Customized · Will be created with workspace');
  await expect(row.locator('.workspace-team-readiness-badge')).toHaveCount(0);

  let payload: Record<string, any> | undefined;
  await page.route('**/api/workspaces', async route => {
    payload = route.request().postDataJSON();
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        folder: { id: 'reviewed-session', folder_slug: 'reviewed-session' },
        seeded_starter_tasks: 0
      })
    });
  });
  await advanceToReviewFromTeam(page);
  await expect(page.locator('#workspaceReviewSummary')).toContainText('Session Producer');
  await page.locator('#createFolderBtn').click();
  await expect.poll(() => payload).toBeTruthy();

  expect(payload?.template_agent_overrides).toEqual([
    expect.objectContaining({
      index: 0,
      name: 'Session Producer',
      system_prompt: 'Produce this session carefully.'
    })
  ]);
  expect(payload?.template_agent_review).toEqual({
    version: 1,
    plan_revision: 'reviewed-plan-1',
    expectations: [{ index: 0, name: 'Session Producer', action: 'create' }]
  });
  expect(unexpectedPosts).toEqual([]);
});

test('agent setup remains keyboard-safe and readable at narrow widths in both themes', async ({
  page
}) => {
  await page.setViewportSize({ width: 380, height: 844 });
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'narrow-plan-1',
        has_agents: true,
        agents: [
          {
            name: 'Narrow Lead',
            action: 'create',
            entry_point: true,
            model: 'gpt-5.3-codex',
            provider: 'codex',
            reasoning_effort: 'high',
            system_prompt: 'Keep the layout readable.',
            model_source: 'template'
          }
        ],
        warnings: []
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Narrow Team');
  await advanceToTeam(page);
  const opener = page.locator('[data-team-agent-setup="0"]');
  await opener.focus();
  await page.keyboard.press('Enter');

  for (const theme of ['dark', 'light']) {
    await page.evaluate(
      value => document.documentElement.setAttribute('data-bs-theme', value),
      theme
    );
    await expect(page.locator('#workspaceAgentSetupView')).toBeVisible();
    await expect(page.locator('#workspaceAgentSetupName')).toBeVisible();
    const overflow = await page.locator('#addFolderModal .modal-content').evaluate(element => ({
      scroll: element.scrollWidth,
      client: element.clientWidth
    }));
    expect(overflow.scroll).toBeLessThanOrEqual(overflow.client + 1);
  }

  await page.keyboard.press('Escape');
  await expect(page.locator('#workspaceAgentSetupView')).toBeHidden();
  await expect(page.locator('#workspaceTeamLayout')).toBeVisible();
  await expect(opener).toBeFocused();
});

test('batch recommendation acceptance can be undone without losing individual setup', async ({
  page
}) => {
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'batch-plan-1',
        has_agents: true,
        entry_agent_name: 'Lead',
        agents: [
          { name: 'Lead', action: 'create', entry_point: true, model_source: 'system' },
          { name: 'Scout', action: 'create', entry_point: false, model_source: 'system' },
          { name: 'Writer', action: 'create', entry_point: false, model_source: 'system' }
        ],
        warnings: []
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Batch Team');
  await advanceToTeam(page);
  const rows = page.locator('#workspaceTeamRoster .workspace-team-row');
  await expect(page.locator('[data-team-accept-all]')).toHaveText(
    'Use recommended setup for all 3'
  );

  await rows.nth(0).locator('[data-team-agent-setup]').click();
  await page.locator('#workspaceAgentSetupSystemPrompt').fill('Individually reviewed.');
  await page.locator('#workspaceAgentSetupSave').click();
  await expect(page.locator('[data-team-accept-all]')).toHaveText(
    'Use recommended setup for all 2'
  );
  await page.locator('[data-team-accept-all]').click();
  await expect(rows).toContainText([
    /Customized · Will be created with workspace/,
    /Ready · Will be created with workspace/,
    /Ready · Will be created with workspace/
  ]);
  await expect(page.locator('[data-team-undo-batch]')).toBeFocused();

  await page.locator('[data-team-undo-batch]').click();
  await expect(rows.nth(0)).toContainText('Customized · Will be created with workspace');
  await expect(rows.nth(1)).toContainText('New · Needs setup');
  await expect(rows.nth(2)).toContainText('New · Needs setup');
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();
  await expect(rows.nth(1).locator('[data-team-agent-setup]')).toBeFocused();
});

test('a stale reviewed plan returns to Team with fresh setup and preserves the draft', async ({
  page
}) => {
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'stale-before',
        has_agents: true,
        agents: [
          {
            name: 'Lead',
            action: 'create',
            entry_point: true,
            model: 'before-model',
            system_prompt: 'Before prompt',
            model_source: 'template'
          }
        ],
        warnings: []
      })
    });
  });
  let createAttempts = 0;
  const submittedRevisions: string[] = [];
  await page.route('**/api/workspaces', async route => {
    createAttempts += 1;
    const payload = route.request().postDataJSON();
    submittedRevisions.push(payload.template_agent_review?.plan_revision);
    if (createAttempts === 1) {
      await route.fulfill({
        status: 409,
        contentType: 'application/json',
        body: JSON.stringify({
          error: 'The blueprint agent plan changed.',
          conflict: { type: 'template_agent_plan', index: 0, name: 'Lead' },
          template_agent_plan: {
            revision: 'stale-after',
            has_agents: true,
            agents: [
              {
                name: 'Lead',
                action: 'create',
                entry_point: true,
                model: 'after-model',
                system_prompt: 'After prompt',
                model_source: 'template'
              }
            ],
            warnings: []
          }
        })
      });
      return;
    }
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        folder: { id: 'stale-recovered', folder_slug: 'stale-recovered' },
        seeded_starter_tasks: 0
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Stale Draft');
  await advanceToTeam(page);
  await page.locator('[data-team-agent-setup="0"]').click();
  await page.locator('#workspaceAgentSetupSave').click();
  await advanceToReviewFromTeam(page);
  await page.locator('#createFolderBtn').click();

  await expect(page.locator('#wizardStep3')).toBeVisible();
  await expect(
    page.locator('#wizardStep3 [data-issue-id="template-agent-plan-changed"]')
  ).toContainText('Blueprint changed');
  await expect(page.locator('#folderNameInput')).toHaveValue('Stale Draft');
  await expect(page.locator('[data-team-agent-setup="0"]')).toHaveText('Review setup');
  await page.locator('[data-team-agent-setup="0"]').click();
  await expect(page.locator('#workspaceAgentSetupModel')).toHaveValue('after-model');
  await expect(page.locator('#workspaceAgentSetupSystemPrompt')).toHaveValue('After prompt');
  await page.locator('#workspaceAgentSetupSave').click();
  await page.locator('#wizardStep3 [data-team-recovery="confirm-fresh-plan"]').click();
  await advanceToReviewFromTeam(page);
  await page.locator('#createFolderBtn').click();
  await expect.poll(() => submittedRevisions).toHaveLength(2);
  expect(submittedRevisions).toEqual(['stale-before', 'stale-after']);
});

test('fatal strict agent creation returns to the owning row and retries the preserved setup', async ({
  page
}) => {
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'retry-plan-1',
        has_agents: true,
        agents: [{ name: 'Lead', action: 'create', entry_point: true, model_source: 'system' }],
        warnings: []
      })
    });
  });
  const payloads: Array<Record<string, any>> = [];
  await page.route('**/api/workspaces', async route => {
    payloads.push(route.request().postDataJSON());
    if (payloads.length === 1) {
      await route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({
          error: 'Agent “Reviewed Lead” could not be created. Nothing was created.',
          conflict: { type: 'template_agent_create', index: 0, name: 'Reviewed Lead' }
        })
      });
      return;
    }
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        folder: { id: 'retried-workspace', folder_slug: 'retried-workspace' },
        seeded_starter_tasks: 0
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Retry Workspace');
  await advanceToTeam(page);
  await page.locator('[data-team-agent-setup="0"]').click();
  await page.locator('#workspaceAgentSetupName').fill('Reviewed Lead');
  await page.locator('#workspaceAgentSetupSystemPrompt').fill('Preserve this setup.');
  await page.locator('#workspaceAgentSetupSave').click();
  await advanceToReviewFromTeam(page);
  await page.locator('#createFolderBtn').click();

  const row = page.locator('#workspaceTeamRoster .workspace-team-row');
  await expect(page.locator('#wizardStep3')).toBeVisible();
  await expect(row).toContainText('Missing · Creation failed');
  await expect(row.locator('[data-team-agent-retry]')).toBeFocused();
  await row.locator('[data-team-agent-retry]').click();
  await expect.poll(() => payloads).toHaveLength(2);
  expect(payloads[1].template_agent_overrides).toEqual(payloads[0].template_agent_overrides);
  expect(payloads[1].template_agent_review).toEqual(payloads[0].template_agent_review);
});

test('server prompt validation returns to the owning setup field before any create', async ({
  page
}) => {
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'prompt-validation-plan',
        has_agents: true,
        agents: [
          {
            name: 'Prompt Lead',
            action: 'create',
            entry_point: true,
            system_prompt: 'Initial prompt',
            model_source: 'system'
          }
        ],
        warnings: []
      })
    });
  });
  const payloads: Array<Record<string, any>> = [];
  await page.route('**/api/workspaces', async route => {
    payloads.push(route.request().postDataJSON());
    await route.fulfill({
      status: 400,
      contentType: 'application/json',
      body: JSON.stringify({
        error:
          'invalid prompt variable: agent "Prompt Lead" uses unknown prompt variable {{unknown}}',
        conflict: { type: 'template_agent_override', index: 0, field: 'system_prompt' }
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Prompt Validation Workspace');
  await advanceToTeam(page);
  await page.locator('[data-team-agent-setup="0"]').click();
  await page.locator('#workspaceAgentSetupSystemPrompt').fill('Use {{unknown}}.');
  await page.locator('#workspaceAgentSetupSave').click();
  await advanceToReviewFromTeam(page);
  await page.locator('#createFolderBtn').click();

  await expect.poll(() => payloads).toHaveLength(1);
  await expect(page.locator('#workspaceAgentSetupView')).toBeVisible();
  await expect(page.locator('#workspaceAgentSetupSystemPrompt')).toBeFocused();
  await expect(page.locator('#workspaceAgentSetupSystemPrompt')).toHaveAttribute(
    'aria-invalid',
    'true'
  );
  await expect(page.locator('#workspaceAgentSetupSystemPromptError')).toContainText(
    'unknown prompt variable'
  );
});

test('Team stages a customized copy without touching the reused agent (FR40-FR47)', async ({
  page
}) => {
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'copy-plan-1',
        has_agents: true,
        entry_agent_name: 'Shared Lead',
        agents: [
          {
            name: 'Shared Lead',
            action: 'reuse',
            entry_point: true,
            role: 'saved-role',
            model: 'saved-model',
            provider: 'saved-provider',
            system_prompt: 'Saved prompt',
            model_source: 'existing',
            recommended_setup: {
              role: 'orchestrator',
              type: 'general',
              model: 'blueprint-model',
              provider: 'blueprint-provider',
              system_prompt: 'Blueprint prompt',
              tools: { skills: ['blueprint-skill'] }
            }
          },
          {
            name: 'Brand New',
            action: 'create',
            entry_point: false,
            model_source: 'agent_default'
          }
        ],
        warnings: []
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Copy WS');
  await advanceToTeam(page);

  const rows = page.locator('#workspaceTeamRoster .workspace-team-row');
  await expect(rows.nth(0)).toContainText('Saved · Ready to attach');
  await expect(rows.nth(0)).toContainText('saved-provider / saved-model');
  await rows.nth(0).locator('[data-team-agent-setup]').click();
  await expect(page.locator('#workspaceAgentSetupName')).toHaveValue('Shared Lead copy');
  await expect(page.locator('#workspaceAgentSetupModel')).toHaveValue('blueprint-model');
  await expect(page.locator('#workspaceAgentSetupSummary')).toContainText('blueprint-skill');

  // The saved definition can only be reused unchanged. Behavioral edits need a
  // real copy name, and roster collisions are rejected beside the child form.
  await page.locator('#workspaceAgentSetupName').fill('Shared Lead');
  await page.locator('#workspaceAgentSetupSystemPrompt').fill('Behave differently.');
  await page.locator('#workspaceAgentSetupSave').click();
  await expect(page.locator('#workspaceAgentSetupError')).toContainText(
    'Give this copy a different name'
  );
  await page.locator('#workspaceAgentSetupName').fill('brand new');
  await page.locator('#workspaceAgentSetupSave').click();
  await expect(page.locator('#workspaceAgentSetupError')).toContainText('already called');

  await page.locator('#workspaceAgentSetupName').fill('Shared Lead Studio');
  await page.locator('#workspaceAgentSetupSave').click();
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText('Shared Lead Studio');
  await expect(rows.nth(0)).toContainText('Customized copy · Will be created with workspace');

  // Review the other absent definition so the strict two-member roster can be submitted.
  await rows.nth(1).locator('[data-team-agent-setup]').click();
  await page.locator('#workspaceAgentSetupSave').click();

  let payload: Record<string, any> | undefined;
  await page.route('**/api/workspaces', async route => {
    payload = route.request().postDataJSON();
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        folder: { id: 'copy-ws', folder_slug: 'copy-ws' },
        seeded_starter_tasks: 0
      })
    });
  });
  await advanceToReviewFromTeam(page);
  await expect(page.locator('#workspaceReviewSummary')).toContainText(
    '1 new · 0 saved · 1 customized copy'
  );
  await page.locator('#createFolderBtn').click();
  await expect.poll(() => payload).toBeTruthy();

  expect(payload?.create_template_agents).toBe(true);
  expect(payload?.template_agent_overrides).toEqual([
    expect.objectContaining({
      index: 0,
      name: 'Shared Lead Studio',
      model: 'blueprint-model',
      provider: 'blueprint-provider',
      system_prompt: 'Behave differently.'
    })
  ]);
  expect(payload?.template_agent_review.expectations).toEqual([
    { index: 0, name: 'Shared Lead Studio', action: 'create' },
    { index: 1, name: 'Brand New', action: 'create' }
  ]);
});

test('Advanced team options can exclude the blueprint team (FR48-FR50)', async ({ page }) => {
  await page.route('**/api/agents/dashboard/list**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ agents: [{ name: 'Research Scout', model: 'gpt-5.5' }] })
    });
  });
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'browser-reuse-plan',
        has_agents: true,
        entry_agent_name: 'Blueprint Lead',
        agents: [
          { name: 'Blueprint Lead', action: 'reuse', entry_point: true, model_source: 'existing' }
        ],
        warnings: []
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Excluded WS');
  await advanceToTeam(page);

  const rows = page.locator('#workspaceTeamRoster .workspace-team-row');
  await page.locator('[data-existing-agent-add="Research Scout"]').click();
  await expect(rows.nth(0)).toContainText('Blueprint Lead');

  await page.locator('#workspaceTeamAdvanced summary').click();
  await page.locator('#templateAgentReviewToggle').uncheck();

  // Blueprint entries leave the roster and the primary is recomputed.
  await expect(rows).toHaveCount(1);
  await expect(rows.nth(0)).toContainText('Research Scout');
  await expect(rows.nth(0).locator('.workspace-team-badge')).toHaveText('Primary');
  await expect(page.locator('#workspaceTeamLiveRegion')).toContainText(
    "Research Scout is now this workspace's primary agent"
  );

  let payload: Record<string, unknown> | undefined;
  await page.route('**/api/workspaces', async route => {
    payload = route.request().postDataJSON();
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        folder: { id: 'excluded-ws', folder_slug: 'excluded-ws' },
        seeded_starter_tasks: 0
      })
    });
  });
  await advanceToReviewFromTeam(page);
  await page.locator('#createFolderBtn').click({ force: true });
  await page.waitForURL('**/workspaces/excluded-ws');

  expect(payload?.create_template_agents).toBe(false);
  expect(payload?.existing_agent_names).toEqual(['Research Scout']);
  expect(payload?.entry_agent_name).toBe('Research Scout');
});

test('Your Agents searches, states why entries are unavailable, and needs no drag', async ({
  page
}) => {
  await page.route('**/api/agents/dashboard/list**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        agents: [
          { name: 'Research Scout', role: 'researcher', model: 'gpt-5.5', workspace_count: 2 },
          { name: 'Data Miner', role: 'analyst', model: 'claude-opus-5', workspace_count: 1 },
          { name: 'Blueprint Lead', role: 'orchestrator', model: 'gpt-5.5', workspace_count: 3 },
          { name: 'Claude Code', role: 'cli', model: 'claude-opus-5', source: 'cli' }
        ]
      })
    });
  });
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'browser-reuse-plan',
        has_agents: true,
        entry_agent_name: 'Blueprint Lead',
        agents: [
          { name: 'Blueprint Lead', action: 'reuse', entry_point: true, model_source: 'existing' }
        ],
        warnings: []
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Picker WS');
  await advanceToTeam(page);

  const cards = page.locator('#existingAgentRosterList .workspace-existing-agent-card');
  await expect(cards).toHaveCount(4);

  // Each result shows name, model, and workspace count (FR60).
  const scout = cards.filter({ hasText: 'Research Scout' });
  await expect(scout).toContainText('gpt-5.5');
  await expect(scout).toContainText('2 workspaces');

  // Every non-addable entry says why, in text (FR62).
  const included = cards.filter({ hasText: 'Blueprint Lead' });
  await expect(included).toContainText('Already included by this blueprint');
  await expect(included.locator('button')).toBeDisabled();
  const cli = cards.filter({ hasText: 'Claude Code' });
  await expect(cli).toContainText('Built-in CLI agents cannot be attached');
  await expect(cli.locator('button')).toBeDisabled();
  // The reason is in the accessible name too, since a disabled button's own
  // label ("Included") cannot explain itself.
  await expect(included.locator('button')).toHaveAccessibleName(
    /Blueprint Lead: Already included by this blueprint/
  );

  // Adding flips the entry to a stated reason rather than silently doing nothing.
  await page.locator('[data-existing-agent-add="Research Scout"]').click();
  await expect(scout).toContainText('Added to this workspace');
  await expect(scout.locator('button')).toBeDisabled();

  // Search covers name, role, and model (FR59).
  const search = page.locator('#existingAgentRosterSearch');
  await search.fill('analyst');
  await expect(cards).toHaveCount(1);
  await expect(cards.first()).toContainText('Data Miner');
  await search.fill('claude-opus-5');
  await expect(cards).toHaveCount(2);
  await search.fill('nothing matches this');
  await expect(page.locator('#existingAgentRosterStatus')).toHaveText('No matching saved agents.');
  await search.fill('');

  // Nothing in the picker advertises drag, because there is no drop target.
  await expect(page.locator('#existingAgentRosterList [draggable="true"]')).toHaveCount(0);
});

test('Your Agents can be added and promoted by keyboard alone (FR61, FR101)', async ({ page }) => {
  await page.route('**/api/agents/dashboard/list**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ agents: [{ name: 'Research Scout', model: 'gpt-5.5' }] })
    });
  });
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'browser-reuse-plan',
        has_agents: true,
        entry_agent_name: 'Blueprint Lead',
        agents: [
          { name: 'Blueprint Lead', action: 'reuse', entry_point: true, model_source: 'existing' }
        ],
        warnings: []
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Keyboard WS');
  await advanceToTeam(page);

  await page.locator('[data-existing-agent-add="Research Scout"]').focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('#workspaceTeamRoster .workspace-team-row')).toHaveCount(2);

  await page.locator('[data-existing-agent-primary="Research Scout"]').focus();
  await page.keyboard.press('Enter');

  const rows = page.locator('#workspaceTeamRoster .workspace-team-row');
  await expect(rows.nth(0)).toContainText('Research Scout');
  await expect(rows.nth(0).locator('.workspace-team-badge')).toHaveText('Primary');
  // The previous primary stays attached, demoted (FR52).
  await expect(rows.nth(1)).toContainText('Blueprint Lead');
  await expect(rows.nth(1).locator('.workspace-team-badge')).toHaveText('Specialist');
  await expect(page.locator('#workspaceTeamLiveRegion')).toContainText(
    'stays attached as a specialist'
  );

  // Removing by keyboard hands the primary slot back.
  await page.locator('[data-existing-agent-remove="Research Scout"]').focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('#workspaceTeamRoster .workspace-team-row')).toHaveCount(1);
  await expect(rows.nth(0)).toContainText('Blueprint Lead');
  await expect(rows.nth(0).locator('.workspace-team-badge')).toHaveText('Primary');
});

test('a Your Agents failure stays advisory and offers Retry (FR65, FR66)', async ({ page }) => {
  let attempts = 0;
  await page.route('**/api/agents/dashboard/list**', async route => {
    attempts += 1;
    if (attempts === 1) {
      await route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'roster backend down' })
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ agents: [{ name: 'Research Scout', model: 'gpt-5.5' }] })
    });
  });
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'browser-reuse-plan',
        has_agents: true,
        entry_agent_name: 'Blueprint Lead',
        agents: [
          { name: 'Blueprint Lead', action: 'reuse', entry_point: true, model_source: 'existing' }
        ],
        warnings: []
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Advisory WS');
  await advanceToTeam(page);

  // The preconfigured blueprint team is unaffected and still reviewable.
  await expect(page.locator('#workspaceTeamRoster .workspace-team-row')).toHaveCount(1);
  const issue = page.locator('#workspaceTeamIssues .workspace-team-issue');
  await expect(issue).toHaveClass(/is-advisory/);
  await expect(issue).toContainText('saved agents could not be loaded');
  await expect(page.locator('#workspaceTeamIssues .workspace-team-issue.is-blocking')).toHaveCount(
    0
  );

  // Retry recovers the picker in place.
  await issue.locator('[data-team-recovery="retry-saved-roster"]').click();
  await expect(page.locator('[data-existing-agent-add="Research Scout"]')).toBeVisible();
  await expect(page.locator('#workspaceTeamIssues .workspace-team-issue')).toHaveCount(0);
});

test('an agent-less team warns without blocking creation (FR55)', async ({ page }) => {
  await page.route('**/api/agents/dashboard/list**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ agents: [] })
    });
  });
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ has_agents: false, agents: [], warnings: [] })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Travels').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Empty Team WS');
  await advanceToTeam(page);

  const issue = page.locator('#workspaceTeamIssues .workspace-team-issue');
  await expect(issue).toContainText('Starter and setup tasks may remain unassigned');
  await expect(issue).toHaveClass(/is-advisory/);
  await expect(issue.locator('.workspace-team-issue-label')).toHaveText('Note');

  // Advisory, never blocking: Review is still reachable and Create still works.
  await advanceToReviewFromTeam(page);
  await expect(page.locator('#createFolderBtn')).toBeEnabled();
});

test('an unavailable blueprint plan blocks Team and offers recovery (FR94, FR95)', async ({
  page
}) => {
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'plan backend down' })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Blocked WS');
  await advanceToTeam(page);

  const blocker = page.locator('#workspaceTeamIssues .workspace-team-issue.is-blocking');
  await expect(blocker).toContainText('plan backend down');
  await expect(blocker.locator('.workspace-team-issue-label')).toHaveText('Needs attention');
  // All three documented recovery paths are offered (FR95).
  await expect(blocker.locator('[data-team-recovery="retry-plan"]')).toBeVisible();
  await expect(blocker.locator('[data-team-recovery="edit-blueprint"]')).toBeVisible();
  await expect(blocker.locator('[data-team-recovery="exclude-blueprint-team"]')).toBeVisible();

  // Taking the exclude path clears the blocker.
  await blocker.locator('[data-team-recovery="exclude-blueprint-team"]').click();
  await expect(page.locator('#workspaceTeamIssues .workspace-team-issue.is-blocking')).toHaveCount(
    0
  );
});

test('Blueprint summarizes included agents read-only, with no agent controls', async ({ page }) => {
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'blueprint-summary-plan',
        has_agents: true,
        entry_agent_name: 'Research Lead',
        agents: [
          { name: 'Research Lead', action: 'reuse', entry_point: true, model_source: 'template' },
          {
            name: 'Source Scout',
            action: 'reuse',
            entry_point: false,
            model_source: 'agent_default'
          },
          {
            name: 'Synthesis Writer',
            action: 'create',
            entry_point: false,
            model_source: 'agent_default'
          }
        ],
        warnings: []
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();

  // One plain sentence: count, names, and which agent is primary (FR15-FR17).
  const summary = page.locator('#blueprintAgentSummary');
  await expect(summary).toBeVisible();
  await expect(page.locator('#blueprintAgentSummaryText')).toHaveText(
    'Includes 3 agents: Research Lead (primary), Source Scout, Synthesis Writer.'
  );

  // FR20: Blueprint offers no agent action at all — the map, the create-all
  // shortcut, the reusable-agent setup form, and every per-agent control are gone.
  await expect(page.locator('#workspaceAgentMapPreview')).toHaveCount(0);
  await expect(page.locator('#workspaceTemplateAgentSetup')).toHaveCount(0);
  await expect(page.locator('#workspaceAgentMapCreateAll')).toHaveCount(0);
  await expect(page.locator('#wizardStep1 [data-template-agent-index]')).toHaveCount(0);
  await expect(page.locator('#wizardStep1 #templateAgentReview')).toHaveCount(0);
  await expect(page.locator('#wizardStep1 #templateAgentReviewToggle')).toHaveCount(0);
});

test('Blueprint tells checking, empty, and unavailable apart (FR18, FR19)', async ({ page }) => {
  // A blueprint that declares no agents: a confirmed empty team, not a blank panel.
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ has_agents: false, agents: [], warnings: [] })
    });
  });
  await openCreateModal(page);
  await cardByLabel(page, 'Travels').click();
  await expect(page.locator('#blueprintAgentSummaryText')).toHaveText(
    'This blueprint includes no agents. You can add saved agents to the team in step 3.'
  );
  await expect(page.locator('#blueprintAgentSummary')).toHaveClass(/is-empty/);

  // A plan that cannot be loaded says so, and is never shown as "no agents".
  await page.unroute('**/api/workspaces/template-agent-plan**');
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'plan backend down' })
    });
  });
  await cardByLabel(page, 'Research Project').click();
  await expect(page.locator('#blueprintAgentSummary')).toHaveClass(/is-error/);
  await expect(page.locator('#blueprintAgentSummaryText')).toContainText('could not be checked');
  await expect(page.locator('#blueprintAgentSummaryText')).not.toContainText('includes no agents');
});

test('changing blueprint keeps saved agents and attaches a duplicate only once', async ({
  page
}) => {
  // Blueprint A declares no agents; blueprint B declares the very agent the user
  // added by hand. The selection must survive the switch (FR22) and be attached
  // exactly once, owned by the blueprint (FR23).
  await page.route('**/api/agents/dashboard/list**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        agents: [
          { name: 'Research Lead', model: 'gpt-5.5', workspace_count: 1 },
          { name: 'Data Miner', model: 'gpt-5.5-mini', workspace_count: 0 }
        ]
      })
    });
  });
  // routeProjectEntryTemplates also stubs the agent plan, so register it first
  // and let this per-blueprint stub take precedence.
  await routeProjectEntryTemplates(page);
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    const body = route.request().postDataJSON() || {};
    const isResearch = String(body.template_id || '') === 'research-project';
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(
        isResearch
          ? {
              revision: 'dedup-research-plan',
              has_agents: true,
              entry_agent_name: 'Research Lead',
              agents: [
                {
                  name: 'Research Lead',
                  action: 'reuse',
                  entry_point: true,
                  model_source: 'existing'
                }
              ],
              warnings: []
            }
          : { has_agents: false, agents: [], warnings: [] }
      )
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Travels').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Dedup WS');
  await advanceToTeam(page);

  // Add both saved agents while the blueprint contributes none.
  await page.locator('[data-existing-agent-add="Research Lead"]').click();
  await page.locator('[data-existing-agent-add="Data Miner"]').click();
  const rows = page.locator('#workspaceTeamRoster .workspace-team-row');
  await expect(rows).toHaveCount(2);
  await expect(page.locator('#workspaceTeamRoster')).toContainText('Research Lead');
  await expect(page.locator('#workspaceTeamRoster')).toContainText('Data Miner');

  // Switch to a blueprint that already includes Research Lead.
  await returnToBlueprints(page);
  await cardByLabel(page, 'Research Project').click();
  await expect(page.locator('#blueprintAgentSummaryText')).toContainText('Research Lead (primary)');
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await advanceToTeam(page);

  // One roster entry for Research Lead, owned by the blueprint — not two.
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText('Research Lead');
  await expect(rows.nth(0).locator('[data-team-agent-setup]')).toBeVisible();
  await expect(rows.nth(1)).toContainText('Data Miner');
  // ...and the wizard explains which source owns it (FR23).
  await expect(page.locator('#workspaceTeamIssues')).toContainText(
    'Research Lead is already included by this blueprint'
  );

  let payload: Record<string, unknown> | undefined;
  await page.route('**/api/workspaces', async route => {
    payload = route.request().postDataJSON();
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        folder: { id: 'dedup-ws', folder_slug: 'dedup-ws' },
        seeded_starter_tasks: 0
      })
    });
  });
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep4')).toBeVisible();
  await page.locator('#createFolderBtn').click();
  await page.waitForURL('**/workspaces/dedup-ws');

  // The duplicate is not sent, and the blueprint keeps the primary slot.
  expect(payload?.existing_agent_names).toEqual(['Data Miner']);
  expect(payload?.entry_agent_name).toBeUndefined();
});

test('Review reads as a receipt: name once, blueprint as provenance, team summarized', async ({
  page
}) => {
  await page.route('**/api/agents/dashboard/list**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ agents: [{ name: 'Research Scout', model: 'gpt-5.5' }] })
    });
  });
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'review-receipt-plan',
        has_agents: true,
        entry_agent_name: 'Reaper Producer',
        agents: [
          { name: 'Reaper Producer', action: 'reuse', entry_point: true, model_source: 'existing' },
          {
            name: 'Session Scout',
            action: 'create',
            entry_point: false,
            model_source: 'agent_default'
          }
        ],
        warnings: []
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Midnight Sessions');
  await advanceToTeam(page);
  await page.locator('[data-existing-agent-add="Research Scout"]').click();
  await advanceToReviewFromTeam(page);

  const receipt = page.locator('#workspaceReviewSummary');

  // The workspace name appears once, as the primary value; the blueprint is
  // provenance beneath it, not a second equal-weight row (FR78, FR79).
  await expect(receipt.locator('.workspace-review-identity-name')).toHaveText('Midnight Sessions');
  await expect(receipt).toContainText('Based on Research Project');
  await expect(receipt).toContainText('Folder: midnight-sessions');
  await expect(receipt.locator('.workspace-review-identity-name')).toHaveCount(1);

  // A compact team summary: who is primary, how many specialists, what happens.
  await expect(receipt).toContainText('Reaper Producer · Primary');
  await expect(receipt).toContainText('2 specialists: Session Scout, Research Scout');
  await expect(receipt).toContainText('will be created and attached');

  // Review is a receipt, not a second configuration surface (FR83).
  await expect(page.locator('#wizardStep4 #workspaceTeamRoster')).toHaveCount(0);
  await expect(page.locator('#wizardStep4 #existingAgentRosterPanel')).toHaveCount(0);
  await expect(page.locator('#wizardStep4 [data-team-customize]')).toHaveCount(0);
  await expect(page.locator('#wizardStep4 [data-existing-agent-add]')).toHaveCount(0);
  await expect(page.locator('#wizardStep4 [data-existing-agent-primary]')).toHaveCount(0);
  await expect(page.locator('#wizardStep4 #templateAgentReviewToggle')).toHaveCount(0);
  await expect(page.locator('#wizardStep4 #projectTemplateOpenAfterCreateToggle')).toHaveCount(0);

  // A blueprint without post-create setup keeps that separate preview absent.
  await expect(page.locator('#workspaceSetupPreview')).toBeHidden();

  // Edit round trips return to the owning step and preserve everything else.
  await receipt.locator('[data-wizard-edit-step="3"]').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();
  await expect(page.locator('#workspaceTeamRoster .workspace-team-row')).toHaveCount(3);
  await advanceToReviewFromTeam(page);
  await expect(receipt).toContainText('Midnight Sessions');

  // The identity card's own Edit (the Details card carries a second one).
  const identityCard = receipt
    .locator('.workspace-review-card')
    .filter({ has: page.locator('.workspace-review-identity-name') });
  await identityCard.locator('[data-wizard-edit-step="2"]').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await expect(page.locator('#folderNameInput')).toHaveValue('Midnight Sessions');
  await advanceToReview(page);
  await expect(receipt).toContainText('2 specialists: Session Scout, Research Scout');
});

test('Review summarizes only the details that were actually chosen (FR81)', async ({ page }) => {
  await routeProjectEntryTemplates(page);

  await openCreateModal(page);
  // Blank with no extra choices: nothing material to restate.
  await cardByLabel(page, 'Blank').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Plain WS');
  await advanceToReview(page);
  await expect(page.locator('#workspaceReviewSummary')).not.toContainText('Agent behavior:');
  await expect(page.locator('#workspaceReviewSummary')).not.toContainText('Opens the project');

  // A non-default behavior profile and a launch choice are worth restating.
  await returnToBlueprints(page);
  await cardByLabel(page, 'Auto Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderAdvancedDisclosure .workspace-advanced-summary').click();
  await page.locator('#folderPresetSelect').selectOption('research');
  await advanceToReview(page);
  await expect(page.locator('#workspaceReviewSummary')).toContainText(
    'Opens the project after creation'
  );
  await expect(page.locator('#workspaceReviewSummary')).toContainText('Agent behavior: Research');
});

test('a failed create keeps the draft, shows the real error, and routes back (FR90, FR99)', async ({
  page
}) => {
  await page.route('**/api/agents/dashboard/list**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ agents: [{ name: 'Research Scout', model: 'gpt-5.5' }] })
    });
  });
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'browser-reuse-plan',
        has_agents: true,
        entry_agent_name: 'Blueprint Lead',
        agents: [
          { name: 'Blueprint Lead', action: 'reuse', entry_point: true, model_source: 'existing' }
        ],
        warnings: []
      })
    });
  });

  let attempts = 0;
  await page.route('**/api/workspaces', async route => {
    if (route.request().method() !== 'POST') return route.fallback();
    attempts += 1;
    if (attempts === 1) {
      await route.fulfill({
        status: 400,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'entry agent "Ghost" does not exist or cannot be attached' })
      });
      return;
    }
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        folder: { id: 'recovered-ws', folder_slug: 'recovered-ws' },
        seeded_starter_tasks: 0
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Recovering WS');
  await advanceToTeam(page);
  await page.locator('[data-existing-agent-add="Research Scout"]').click();
  await advanceToReviewFromTeam(page);

  await page.locator('#createFolderBtn').click();

  // The modal stays open with the server's own message, focused and actionable.
  await expect(page.locator('#addFolderModal')).toBeVisible();
  const failure = page.locator('#workspaceReviewError');
  await expect(failure).toBeVisible();
  await expect(failure).toContainText('does not exist or cannot be attached');
  await expect(failure).toBeFocused();

  // Editing from the failure returns to Team with the draft intact.
  await failure.getByRole('button', { name: 'Edit team' }).click({ force: true });
  await expect(page.locator('#wizardStep3')).toBeVisible();
  await expect(page.locator('#workspaceTeamRoster .workspace-team-row')).toHaveCount(2);
  await expect(page.locator('#workspaceTeamRoster')).toContainText('Research Scout');

  // Resubmitting succeeds and the failure notice is gone.
  await advanceToReviewFromTeam(page);
  await expect(page.locator('#workspaceReviewError')).toBeHidden();
  await page.locator('#createFolderBtn').click();
  await page.waitForURL('**/workspaces/recovered-ws');
});

test('Team refuses to reach Review while a blocker is unresolved (FR89, FR94)', async ({
  page
}) => {
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'plan backend down' })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Blocked WS');
  await advanceToTeam(page);

  // Continuing is refused and focus lands on the reason.
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();
  await expect(page.locator('#wizardStep4')).toBeHidden();
  await expect(
    page.locator('#workspaceTeamIssues .workspace-team-issue.is-blocking')
  ).toBeFocused();

  // Clearing the blocker lets the flow continue, and Create is enabled.
  await page.locator('#workspaceTeamIssues [data-team-recovery="exclude-blueprint-team"]').click();
  await advanceToReviewFromTeam(page);
  await expect(page.locator('#createFolderBtn')).toBeEnabled();
});

test('Team carries text semantics, list roles, and quiet live-region updates', async ({ page }) => {
  await page.route('**/api/agents/dashboard/list**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ agents: [{ name: 'Research Scout', model: 'gpt-5.5' }] })
    });
  });
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'browser-reuse-plan',
        has_agents: true,
        entry_agent_name: 'Blueprint Lead',
        agents: [
          { name: 'Blueprint Lead', action: 'reuse', entry_point: true, model_source: 'existing' }
        ],
        warnings: []
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Semantics WS');
  await advanceToTeam(page);

  // Both collections are lists with accessible names (FR105).
  await expect(page.locator('#workspaceTeamRoster')).toHaveRole('list');
  await expect(page.locator('#existingAgentRosterList')).toHaveRole('list');
  await expect(page.locator('#workspaceTeamRoster .workspace-team-row').first()).toHaveRole(
    'listitem'
  );
  await expect(
    page.locator('#existingAgentRosterList .workspace-existing-agent-card').first()
  ).toHaveRole('listitem');

  // Designation and lifecycle are words, not colour (FR102).
  await expect(page.locator('#workspaceTeamRoster .workspace-team-badge').first()).toHaveText(
    'Primary'
  );

  // Neither the roster nor the receipt is itself a live region — they re-render
  // wholesale, and announcing them would repeat the whole team on each edit.
  await expect(page.locator('#workspaceTeamSummary')).not.toHaveAttribute('aria-live', /.*/);
  await expect(page.locator('#workspaceReviewSummary')).not.toHaveAttribute('aria-live', /.*/);

  // A single deliberate message covers the change, and focus does not move (FR103).
  await page.locator('#folderNameInput').focus();
  await page.locator('[data-existing-agent-add="Research Scout"]').click();
  await expect(page.locator('#workspaceTeamLiveRegion')).toContainText('Research Scout added');
  await expect(page.locator('#workspaceTeamLiveRegion')).toContainText('will be attached');
});

test('the modal never scrolls horizontally at a narrow viewport (FR108)', async ({ page }) => {
  await page.setViewportSize({ width: 380, height: 800 });
  await page.route('**/api/agents/dashboard/list**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        agents: [{ name: 'An Extremely Long Saved Agent Name For Wrapping', model: 'gpt-5.5' }]
      })
    });
  });
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'narrow-overflow-plan',
        has_agents: true,
        entry_agent_name: 'A Very Long Blueprint Agent Name Indeed',
        agents: [
          {
            name: 'A Very Long Blueprint Agent Name Indeed',
            action: 'reuse',
            entry_point: true,
            model_source: 'existing'
          }
        ],
        warnings: []
      })
    });
  });

  const noHorizontalOverflow = async () =>
    page.evaluate(() => {
      const body = document.getElementById('addFolderModal')?.querySelector('.modal-body');
      if (!body) return true;
      // 1px of tolerance for sub-pixel layout rounding.
      return body.scrollWidth <= body.clientWidth + 1;
    });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  expect(await noHorizontalOverflow()).toBe(true);

  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('A Rather Long Workspace Name For Narrow Screens');
  expect(await noHorizontalOverflow()).toBe(true);

  await advanceToTeam(page);
  // Team stacks with the resulting team above the picker (FR58, FR110).
  const teamBox = await page.locator('#workspaceTeamReview').boundingBox();
  const pickerBox = await page.locator('#existingAgentRosterPanel').boundingBox();
  expect(teamBox!.y).toBeLessThan(pickerBox!.y);
  await page
    .locator('[data-existing-agent-add="An Extremely Long Saved Agent Name For Wrapping"]')
    .click();
  expect(await noHorizontalOverflow()).toBe(true);

  await page.locator('#workspaceTeamRoster [data-team-agent-setup]').first().click();
  expect(await noHorizontalOverflow()).toBe(true);
  await page.keyboard.press('Escape');
  await expect(page.locator('#workspaceTeamLayout')).toBeVisible();

  await advanceToReviewFromTeam(page);
  expect(await noHorizontalOverflow()).toBe(true);
});

test('a wide viewport puts the resulting team beside Your Agents (FR57)', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.route('**/api/agents/dashboard/list**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ agents: [{ name: 'Research Scout', model: 'gpt-5.5' }] })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Blank').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Wide WS');
  await advanceToTeam(page);

  const teamBox = await page.locator('#workspaceTeamReview').boundingBox();
  const pickerBox = await page.locator('#existingAgentRosterPanel').boundingBox();
  // Side by side: the picker starts to the right of the team, on the same row.
  expect(pickerBox!.x).toBeGreaterThan(teamBox!.x + teamBox!.width - 5);
  expect(Math.abs(pickerBox!.y - teamBox!.y)).toBeLessThan(40);
});

test('the wizard never persists an agent before the workspace is created (FR68)', async ({
  page
}) => {
  // The old flow saved reusable agents from the Blueprint step via
  // /api/workspaces/template-agent-create, so cancelling left them behind in
  // Your Agents. That path is gone: this asserts no such request is made while
  // browsing, customizing, or cancelling.
  const precreateCalls: string[] = [];
  page.on('request', request => {
    if (request.method() !== 'POST') return;
    const path = new URL(request.url()).pathname;
    if (
      path === '/api/agents' ||
      path === '/api/workspaces' ||
      path === '/api/workspaces/template-agent-create' ||
      path.includes('/agents/attach') ||
      path.includes('/capabilities')
    ) {
      precreateCalls.push(request.url());
    }
  });
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'cancel-plan-1',
        has_agents: true,
        entry_agent_name: 'Reaper Producer',
        agents: [
          {
            name: 'Reaper Producer',
            action: 'create',
            entry_point: true,
            model_source: 'agent_default'
          },
          {
            name: 'Session Scout',
            action: 'create',
            entry_point: false,
            model_source: 'agent_default'
          }
        ],
        warnings: []
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await expect(page.locator('#blueprintAgentSummaryText')).toContainText('Includes 2 agents');

  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('No Orphans WS');
  await advanceToTeam(page);

  // Edit and review staged blueprint agents on Team: still no persistence request.
  const row = page.locator('#workspaceTeamRoster .workspace-team-row').first();
  await row.locator('[data-team-agent-setup]').click();
  await page.locator('#workspaceAgentSetupName').fill('Renamed Producer');
  await page.locator('#workspaceAgentSetupSave').click();
  await page.locator('[data-team-agent-setup="1"]').click();
  await page.locator('#workspaceAgentSetupSave').click();
  await advanceToReviewFromTeam(page);

  // Cancel the whole wizard from Review.
  await page.locator('#addFolderModal .modal-footer [data-bs-dismiss="modal"]').click();
  await expect(page.locator('#addFolderModal')).toBeHidden();

  expect(precreateCalls).toEqual([]);
});

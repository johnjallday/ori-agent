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

  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep4')).toBeVisible();
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

test('live Reaper Song defaults to launch, supports keyboard opt-out, and never opens on reload', async ({
  page
}) => {
  let openCalls = 0;
  await page.route('**/api/workspaces/**/project/open', async route => {
    openCalls += 1;
    await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Reaper Song').click();
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
      body: JSON.stringify({ folder: { id: 'created-open' }, seeded_starter_tasks: 0 })
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
      body: JSON.stringify({ folder: { id: 'created-closed' }, seeded_starter_tasks: 0 })
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
      body: JSON.stringify({ folder: { id: 'created-failure' }, seeded_starter_tasks: 0 })
    });
  });

  await cardByLabel(page, 'Auto Project').click();
  await advanceToWorkspaceDetails(page);
  await advanceToReview(page);
  await page.locator('#createFolderBtn').click();
  await page.waitForURL('**/workspaces/created-failure');
  const retryNotice = page.locator('.toast-message', { hasText: 'Use Open Project to try again' });
  await expect(retryNotice).toHaveCount(1);
  await expect.poll(() => openCalls).toBe(1);

  await page.reload();
  await expect(retryNotice).toHaveCount(0);
  await expect.poll(() => openCalls).toBe(1);
});

test('createFolder submits workspace_preset for create and import', async ({ page }) => {
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
  await page.locator('[data-existing-agent-add="Research Scout"]').click();
  await expect(page.locator('#existingAgentTeamList')).toContainText('Research Scout');
  await expect(page.locator('#existingAgentTeamList')).toContainText('Primary workspace agent');
  await page.locator('[data-existing-agent-name="Data Miner"]').evaluate(card => {
    const data = new DataTransfer();
    data.setData('application/x-ori-agent-name', 'Data Miner');
    document
      .getElementById('workspaceTeamDropZone')
      ?.dispatchEvent(
        new DragEvent('drop', { bubbles: true, cancelable: true, dataTransfer: data })
      );
  });
  await expect(page.locator('#existingAgentTeamList')).toContainText('Data Miner');

  let payload: Record<string, unknown> | undefined;
  await page.route('**/api/workspaces', async route => {
    payload = route.request().postDataJSON();
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({ folder: { id: 'atomic-team' }, seeded_starter_tasks: 0 })
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
            model_source: 'template'
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
  await cardByLabel(page, 'Reaper Song').click();
  await advanceToWorkspaceDetails(page);
  // The blueprint roster is edited on Team, not Review (FR32, FR83).
  await advanceToTeam(page);

  const review = page.locator('#templateAgentReview');
  await expect(review).toContainText('Research Lead');
  await expect(review).toContainText('Primary workspace agent');
});

test('Blueprint summarizes included agents read-only, with no agent controls', async ({ page }) => {
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        has_agents: true,
        entry_agent_name: 'Research Lead',
        agents: [
          { name: 'Research Lead', action: 'reuse', entry_point: true, model_source: 'template' },
          { name: 'Source Scout', action: 'reuse', entry_point: false, model_source: 'agent_default' },
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
  await cardByLabel(page, 'Reaper Song').click();

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
  const team = page.locator('#existingAgentTeamList');
  await expect(team).toContainText('Research Lead');
  await expect(team).toContainText('Data Miner');

  // Switch to a blueprint that already includes Research Lead.
  await returnToBlueprints(page);
  await cardByLabel(page, 'Research Project').click();
  await expect(page.locator('#blueprintAgentSummaryText')).toContainText('Research Lead (primary)');
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await advanceToTeam(page);

  // Research Lead is now owned by the blueprint roster, so the saved-agent list
  // shows only Data Miner — one attachment, not two.
  await expect(page.locator('#templateAgentReview')).toContainText('Research Lead');
  await expect(team).toContainText('Data Miner');
  await expect(team).not.toContainText('Research Lead');

  let payload: Record<string, unknown> | undefined;
  await page.route('**/api/workspaces', async route => {
    payload = route.request().postDataJSON();
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({ folder: { id: 'dedup-ws' }, seeded_starter_tasks: 0 })
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

test('the wizard never persists an agent before the workspace is created (FR68)', async ({
  page
}) => {
  // The old flow saved reusable agents from the Blueprint step via
  // /api/workspaces/template-agent-create, so cancelling left them behind in
  // Your Agents. That path is gone: this asserts no such request is made while
  // browsing, customizing, or cancelling.
  const precreateCalls: string[] = [];
  await page.route('**/api/workspaces/template-agent-create', async route => {
    precreateCalls.push(route.request().url());
    await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' });
  });
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        has_agents: true,
        entry_agent_name: 'Reaper Producer',
        agents: [
          { name: 'Reaper Producer', action: 'create', entry_point: true, model_source: 'agent_default' },
          { name: 'Session Scout', action: 'create', entry_point: false, model_source: 'agent_default' }
        ],
        warnings: []
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Reaper Song').click();
  await expect(page.locator('#blueprintAgentSummaryText')).toContainText('Includes 2 agents');

  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('No Orphans WS');
  await advanceToTeam(page);

  // Edit a staged blueprint agent on Team: still no persistence request.
  const row = page.locator('.workspace-template-agent-row').first();
  await row.locator('.workspace-template-agent-customize-btn').click();
  await row.locator('.workspace-template-agent-name-input').fill('Renamed Producer');

  // Cancel the whole wizard.
  await page.evaluate(() => {
    const el = document.getElementById('addFolderModal');
    // @ts-expect-error bootstrap is a page global
    window.bootstrap.Modal.getInstance(el)?.hide();
  });
  await expect(page.locator('#addFolderModal')).toBeHidden();

  expect(precreateCalls).toEqual([]);
});

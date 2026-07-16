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

async function advanceToReview(page: Page) {
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();
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

  await cardByLabel(page, 'Auto Project').click();
  await advanceToWorkspaceDetails(page);
  await advanceToReview(page);
  await expect(panel).toBeVisible();
  await expect(toggle).toBeChecked();

  await toggle.uncheck();
  await returnToBlueprints(page);
  await cardByLabel(page, 'Manual Project').click();
  await advanceToWorkspaceDetails(page);
  await advanceToReview(page);
  await expect(panel).toBeVisible();
  await expect(toggle).not.toBeChecked();
  await toggle.check();

  // Every template change reapplies that template's own default.
  await returnToBlueprints(page);
  await cardByLabel(page, 'Auto Project').click();
  await advanceToWorkspaceDetails(page);
  await advanceToReview(page);
  await expect(toggle).toBeChecked();
  await returnToBlueprints(page);
  await cardByLabel(page, 'Manual Project').click();
  await advanceToWorkspaceDetails(page);
  await advanceToReview(page);
  await expect(toggle).not.toBeChecked();

  await returnToBlueprints(page);
  await cardByLabel(page, 'No Entry').click();
  await advanceToWorkspaceDetails(page);
  await advanceToReview(page);
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
  await advanceToReview(page);

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
  await advanceToReview(page);
  await expect(page.locator('#projectTemplateOpenAfterCreateToggle')).toBeChecked();
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
  await advanceToReview(page);
  await expect(page.locator('#projectTemplateOpenAfterCreateToggle')).not.toBeChecked();
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

test('review attaches a saved agent and submits the complete team atomically', async ({ page }) => {
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
  await advanceToReview(page);

  await page.locator('#addExistingAgentBtn').click();
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
  await page.locator('#createFolderBtn').click();
  await page.waitForURL('**/workspaces/atomic-team');
  expect(payload?.existing_agent_names).toEqual(['Research Scout', 'Data Miner']);
  expect(payload?.entry_agent_name).toBe('Research Scout');
});

test('review visualizes every included template agent and its lifecycle', async ({ page }) => {
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
            model_source: 'agent_default'
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
  await advanceToReview(page);

  const review = page.locator('#templateAgentReview');
  await expect(review).toContainText('Blueprint agents');
  await expect(review).toContainText('Research Lead');
  await expect(review).toContainText('New reusable agent · Added to Your Agents and attached');
  await expect(review).toContainText('Primary workspace agent');
  const preview = page.locator('#workspaceAgentMapPreview');
  await expect(preview).toContainText('Workspace team · 3 agents');
  await expect(preview).toContainText('Research Lead');
  await expect(preview).toContainText('Source Scout');
  await expect(preview).toContainText('Synthesis Writer');
  await expect(preview).toContainText('Synthesis Writer is not in Your Agents yet');
  await expect(page.locator('#workspaceAgentMapNode')).toHaveClass(/is-ready/);
  await expect(page.locator('#workspaceAgentMapSpecialists')).toHaveText(
    /Source Scout[\s\S]*Synthesis Writer/
  );
  await expect(page.locator('.workspace-agent-map-specialist-create-badge')).toHaveCSS(
    'display',
    'grid'
  );

  await review.locator('.workspace-template-agent-advanced summary').click();
  await review.locator('#templateAgentReviewToggle').uncheck();
  await expect(preview).toContainText('No agent selected');
  await expect(page.locator('#workspaceAgentMapNode')).toHaveClass(/is-missing/);
  await expect(page.locator('.workspace-agent-map-avatar-placeholder')).toHaveCSS(
    'display',
    'block'
  );
});

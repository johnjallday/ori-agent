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
    await expect(page.locator('#addAgentModal')).toBeVisible();
    await page.locator('#createAgentBtn').click();
    await expect(page.locator('#addAgentModal')).toBeHidden();
    await expect(page.locator('#addFolderModal')).toBeVisible();
  }
  // Strict role staffing never treats a blueprint proposal as membership. Tests
  // whose concern is after Team still cross the real UI boundary by explicitly
  // accepting each required Create action before advancing.
  const missingRequired = page.locator(
    '#workspaceRoleRoster .ws-role-row:has(.ws-role-tag--missing)'
  );
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (
            window as unknown as {
              sessionManager: { teamView: () => { planStatus?: string } | null };
            }
          ).sessionManager.teamView()?.planStatus
      )
    )
    .not.toBe('loading');
  while ((await missingRequired.count()) > 0) {
    await missingRequired
      .first()
      .getByRole('button', { name: /^Create an agent for / })
      .click();
    await expect(page.locator('#addAgentModal')).toBeVisible();
    await page.locator('#createAgentBtn').click();
    await expect(page.locator('#addAgentModal')).toBeHidden();
    await expect(page.locator('#addFolderModal')).toBeVisible();
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
      body: JSON.stringify({
        revision: 'empty-plan',
        has_agents: false,
        agents: [],
        warnings: []
      })
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
  // A {{name}} project file is never named after the blueprint, so the name
  // starts empty and the user names the workspace.
  await expect(page.locator('#folderNameInput')).toHaveValue('');
  await page.locator('#folderNameInput').fill('Created Open');
  await expect(page.locator('#workspaceNameHint')).toHaveText(
    'Folder: created-open · Project file: created-open.rpp'
  );
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
  await page.locator('#folderNameInput').fill('Created Closed');
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
  await page.locator('#folderNameInput').fill('Created Failure');
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
      body: JSON.stringify({
        revision: 'preset-empty-plan',
        has_agents: false,
        agents: [],
        warnings: []
      })
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

test('the shared creator makes an ordinary Group through Details, Roster, and Review', async ({
  page
}) => {
  let payload: Record<string, unknown> | undefined;
  await page.route('**/api/workspaces', async route => {
    if (route.request().method() !== 'POST') return route.continue();
    payload = route.request().postDataJSON();
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        folder: { id: 'browser-group', name: 'Browser group', folder_slug: 'browser-group' }
      })
    });
  });

  await page.evaluate(() => {
    (
      window as unknown as { sessionManager: { showAddWorkspaceModal: (options: unknown) => void } }
    ).sessionManager.showAddWorkspaceModal({ kind: 'group', entryPoint: 'browser-group-test' });
  });
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await expect(page.locator('#folderModalTitle')).toHaveText('Create Group');
  await expect(page.locator('#workspaceCreatorKindGroup')).toBeChecked();
  // An ordinary Group opens on its Blueprint step (General selected), not the
  // Workspace blueprint picker.
  await expect(page.locator('#wizardStep1')).toBeVisible();
  await expect(page.locator('#workspaceBlueprintStepBody')).toBeHidden();
  await expect(page.locator('#wizardStep1Title')).toHaveText('Choose a group blueprint');
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep3')).toBeHidden();
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await expect(page.locator('#folderAdvancedDisclosure')).toBeHidden();
  await expect(page.locator('#workspaceBootstrapFields')).toBeHidden();

  await page.fill('#folderNameInput', 'Browser group');
  await page.fill('#folderDescriptionInput', 'A managed group created by the shared dialog.');
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();
  await expect(page.locator('#workspaceTeamHeading')).toHaveText('Group roster');
  await advanceToReviewFromTeam(page);
  await expect(page.locator('#wizardStep4')).toBeVisible();
  await expect(page.locator('#workspaceReviewSummary')).toContainText(
    'One organizational group and its reviewed roster'
  );
  await expect(page.locator('#createFolderBtn')).toHaveText('Create group “Browser group”');
  await page.locator('#createFolderBtn').click();
  await expect.poll(() => payload).toBeDefined();
  expect(payload).toMatchObject({
    name: 'Browser group',
    description: 'A managed group created by the shared dialog.',
    kind: 'group',
    group_roster: true,
    create_template_agents: true
  });
  for (const field of [
    'template_id',
    'template_path',
    'blank',
    'workspace_bootstrap',
    'workspace_preset',
    'team_intent',
    'role_staffing',
    'existing_agent_names',
    'entry_agent_name'
  ]) {
    expect(payload).not.toHaveProperty(field);
  }
  expect(payload).toHaveProperty('template_agent_review');
  expect(payload?.template_agent_review).toMatchObject({
    version: 1,
    expectations: [{ index: 0, name: 'Browser group Manager', action: 'create' }]
  });
  await expect(page.locator('#toastContainer .toast-action')).toHaveText(
    'Open group / Manage team'
  );
});

test('creator lifecycle keeps ordinary drafts isolated across kind switches and cancellation', async ({
  page
}) => {
  const creator = page.locator('#addFolderModal');
  const open = async (options: Record<string, unknown> = {}) => {
    await page.evaluate(options => {
      (
        window as unknown as {
          sessionManager: { showAddWorkspaceModal: (options: Record<string, unknown>) => void };
        }
      ).sessionManager.showAddWorkspaceModal(options);
    }, options);
    await expect(creator).toBeVisible();
  };

  await open({ entryPoint: 'lifecycle-workspace' });
  await cardByLabel(page, 'Blank').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Draft stays local');
  await page.locator('#folderDescriptionInput').fill('Workspace description');

  await page.locator('#workspaceCreatorKindGroup').check();
  await expect(page.locator('#folderModalTitle')).toHaveText('Create Group');
  await expect(page.locator('#wizardStep1Title')).toHaveText('Choose a group blueprint');
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await expect(page.locator('#wizardStep1')).toBeHidden();
  await expect(page.locator('#wizardStep3')).toBeHidden();
  await expect(page.locator('#folderNameInput')).toHaveValue('Draft stays local');
  await page.locator('#folderDescriptionInput').fill('Group description');
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();
  await advanceToReviewFromTeam(page);
  await expect(page.locator('#workspaceReviewSummary')).toContainText(
    'One organizational group and its reviewed roster'
  );

  await page.locator('#workspaceCreatorKindWorkspace').check();
  await expect(page.locator('#wizardStep1')).toBeVisible();
  await cardByLabel(page, 'Blank').click();
  await advanceToWorkspaceDetails(page);
  await expect(page.locator('#folderNameInput')).toHaveValue('Draft stays local');
  await expect(page.locator('#folderDescriptionInput')).toHaveValue('Workspace description');

  await page.locator('#workspaceCreatorKindGroup').check();
  await expect(page.locator('#folderDescriptionInput')).toHaveValue('Group description');
  await page.keyboard.press('Escape');
  await expect(creator).toBeHidden();

  // A cancelled selected-member operation does not leave a fixed Group or its
  // reviewed members behind when the next caller starts an ordinary draft.
  await open({
    mode: 'selected-members',
    kind: 'group',
    selection: { ids: ['reviewed-workspace'], names: ['Reviewed workspace'] }
  });
  await expect(page.locator('#workspaceCreatorKindFixedNotice')).toContainText(
    'keeps that choice fixed'
  );
  await page.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(creator).toBeHidden();

  await open({ kind: 'group', entryPoint: 'ordinary-after-selected-cancel' });
  await expect(page.locator('#workspaceCreatorKindFixedNotice')).toBeHidden();
  await expect(page.locator('#workspaceCreatorKindGroup')).toBeEnabled();
  await expect(page.locator('#folderNameInput')).toHaveValue('');
  await expect(page.locator('#workspaceReviewSummary')).not.toContainText('Reviewed workspace');
  await page.keyboard.press('Escape');
  await expect(creator).toBeHidden();

  // Import remains a fixed Workspace operation, and cancelling it cannot make
  // the following ordinary Group operation inherit import-only state.
  await open({ importMode: true, entryPoint: 'lifecycle-import' });
  await expect(page.locator('#workspaceCreatorKindWorkspace')).toBeChecked();
  await expect(page.locator('#workspaceCreatorKindGroup')).toBeDisabled();
  await page.keyboard.press('Escape');
  await expect(creator).toBeHidden();

  await open({ kind: 'group', entryPoint: 'ordinary-after-import-cancel' });
  await expect(page.locator('#workspaceCreatorKindGroup')).toBeChecked();
  await expect(page.locator('#workspaceCreatorKindGroup')).toBeEnabled();
  await expect(page.locator('#workspaceCreatorKindFixedNotice')).toBeHidden();
  await page.keyboard.press('Escape');
  await expect(creator).toBeHidden();
});

test('Team attaches a saved agent and submits the complete team atomically', async ({ page }) => {
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'blank-team-plan',
        has_agents: false,
        agents: [],
        warnings: []
      })
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
  await expect(
    page.locator('#toastContainer .toast').filter({
      hasText: 'Research Scout added to the workspace draft.'
    })
  ).toBeVisible();
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

  // The server plan declares slots, not members. Project Lead and specialist
  // roles appear in declaration order with explicit Create/Assign actions.
  const rows = page.locator('#workspaceRoleRoster .ws-role-row');
  await expect(rows).toHaveCount(3);
  await expect(page.locator('#templateAgentReview')).toHaveCount(0);
  await expect(rows.nth(0)).toContainText('Research Lead');
  await expect(rows.nth(0).locator('.ws-role-row__designation')).toHaveText('PROJECT LEAD');
  await expect(rows.nth(0).locator('.ws-role-tag')).toHaveText('Missing');
  await expect(rows.nth(1).locator('.ws-role-row__designation')).toHaveText('SPECIALIST');
  await expect(rows.nth(1).locator('.ws-role-tag')).toHaveText('Optional');
  await expect(rows.nth(2).locator('.ws-role-row__designation')).toHaveText('SPECIALIST');
  await expect(rows.nth(2).locator('.ws-role-tag')).toHaveText('Optional');
  await expect(
    rows.nth(0).getByRole('button', { name: 'Create an agent for Research Lead' })
  ).toBeVisible();
  await expect(
    rows.nth(0).getByRole('button', { name: 'Assign an agent to Research Lead' })
  ).toBeVisible();
  await expect(page.locator('#workspaceTeamIssues')).toContainText(
    'Fill the required Research Lead role'
  );
});

test('proposed setup reuses Create New Agent in draft mode and submits one strict atomic request', async ({
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

  const row = page.locator('#workspaceRoleRoster .ws-role-row');
  await expect(row).toContainText('Missing');
  const requiredSetupAction = row.getByRole('button', {
    name: 'Create an agent for Reaper Producer'
  });
  await expect(requiredSetupAction).toHaveText('Create');
  await expect(page.locator('#wizardNextBtn')).toBeDisabled();

  await requiredSetupAction.click();
  await expect(page.locator('#addFolderModal')).toBeHidden();
  await expect(page.locator('#addAgentModal')).toBeVisible();
  await expect(page.locator('.modal.show')).toHaveCount(1);
  await expect(page.locator('#addAgentModalTitleText')).toHaveText(
    'Create an agent for Reaper Producer'
  );
  await expect(page.locator('#agentName')).toHaveValue('Reaper Producer');
  await expect(page.locator('#agentReasoning')).toBeDisabled();
  await expect(page.locator('#agentCreateDraftSummary')).toContainText('reaper-session');
  await expect(page.locator('#agentSystemPrompt')).toHaveValue('Produce the session.');
  await expect(page.locator('#agentCreateCapabilitiesSection')).toBeHidden();

  // Cancel discards only unsaved modal controls, restores the exact Team
  // opener, and resets the shared shell before it is reused.
  await page.locator('#agentName').fill('Unsaved Producer');
  await page.locator('#cancelAgentBtn').click();
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await expect(row).toContainText('Reaper Producer');
  await expect(row).toContainText('Missing');
  await expect(row).toBeFocused();
  await expect(page.locator('#addAgentModalTitleText')).toHaveText('Create New Agent');
  await expect(page.locator('#addAgentModal')).not.toHaveAttribute('data-agent-create-mode', /.+/);

  await requiredSetupAction.click();
  await expect(page.locator('#addAgentModal')).toBeVisible();
  await page.locator('#agentName').fill('Session Producer');
  await page.locator('#agentSystemPrompt').fill('Produce this session carefully.');
  await page.locator('#createAgentBtn').click();
  await expect(
    page.locator('#toastContainer .toast').filter({
      hasText: 'Session Producer added to the workspace draft.'
    })
  ).toBeVisible();
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await expect(row).toContainText('Session Producer');
  await expect(row).toContainText('New agent');
  await expect(row.locator('.ws-role-tag--filled')).toHaveCount(1);

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
  const successToasts: Array<{ message: string; type: string }> = [];
  await page.exposeFunction('captureWorkspaceSuccessToast', (message: string, type: string) => {
    successToasts.push({ message, type });
  });
  await page.evaluate(() => {
    const target = window as unknown as {
      notifyToast: (message: string, type: string) => void;
      captureWorkspaceSuccessToast: (message: string, type: string) => void;
    };
    target.notifyToast = (message, type) => target.captureWorkspaceSuccessToast(message, type);
  });
  await page.locator('#createFolderBtn').click();
  await expect.poll(() => payload).toBeTruthy();
  await expect
    .poll(() => successToasts)
    .toContainEqual({
      message: 'Workspace created with setup (Session Producer created and added).',
      type: 'success'
    });

  expect(payload?.team_intent).toEqual({
    version: 1,
    mode: 'staffed',
    plan_revision: 'reviewed-plan-1'
  });
  expect(payload?.role_staffing).toEqual([
    expect.objectContaining({
      role_id: 'reaper-producer',
      mode: 'create',
      name: 'Session Producer',
      system_prompt: 'Produce this session carefully.'
    })
  ]);
  expect(payload?.template_agent_overrides).toBeUndefined();
  expect(payload?.template_agent_review).toBeUndefined();
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
  const roleRow = page.locator('#workspaceRoleRoster .ws-role-row').first();
  const opener = roleRow.getByRole('button', { name: 'Create an agent for Narrow Lead' });
  await opener.focus();
  await page.keyboard.press('Enter');

  for (const theme of ['dark', 'light']) {
    await page.evaluate(
      value => document.documentElement.setAttribute('data-bs-theme', value),
      theme
    );
    await expect(page.locator('#addAgentModal')).toBeVisible();
    await expect(page.locator('#agentName')).toBeVisible();
    const overflow = await page.locator('#addAgentModal .modal-content').evaluate(element => ({
      scroll: element.scrollWidth,
      client: element.clientWidth
    }));
    expect(overflow.scroll).toBeLessThanOrEqual(overflow.client + 1);
  }

  await page.keyboard.press('Escape');
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await expect(roleRow).toBeFocused();

  await opener.click();
  await page.locator('#createAgentBtn').click();
  const toastBox = await page
    .locator('#toastContainer .toast')
    .filter({ hasText: 'Narrow Lead added to the workspace draft' })
    .boundingBox();
  expect(toastBox).not.toBeNull();
  expect(toastBox?.x || 0).toBeGreaterThanOrEqual(0);
  expect((toastBox?.x || 0) + (toastBox?.width || 0)).toBeLessThanOrEqual(380);
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep4')).toBeVisible();
});

test('required roles are explicit while optional roles may remain empty', async ({ page }) => {
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'role-plan-1',
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
  await page.locator('#folderNameInput').fill('Role Team');
  await advanceToTeam(page);
  const rows = page.locator('#workspaceRoleRoster .ws-role-row');
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0).locator('.ws-role-tag')).toHaveText('Missing');
  await expect(rows.nth(1).locator('.ws-role-tag')).toHaveText('Optional');
  await expect(rows.nth(2).locator('.ws-role-tag')).toHaveText('Optional');
  await rows.nth(0).getByRole('button', { name: 'Create an agent for Lead' }).click();
  await page.locator('#agentSystemPrompt').fill('Individually reviewed.');
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await expect(rows.nth(0)).toContainText('Lead');
  await expect(rows.nth(0)).toContainText('New agent');
  await expect(page.locator('#wizardNextBtn')).toBeEnabled();
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep4')).toBeVisible();
  await expect(page.locator('#workspaceReviewSummary')).toContainText('1 of 3 roles filled');
  await expect(page.locator('#workspaceReviewSummary')).toContainText('2 roles will stay empty');
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
    submittedRevisions.push(payload.team_intent?.plan_revision);
    if (createAttempts === 1) {
      await route.fulfill({
        status: 409,
        contentType: 'application/json',
        body: JSON.stringify({
          error: 'The blueprint team changed; review the current roles again.',
          conflict: {
            type: 'team_readiness',
            roles: [{ role_id: 'lead', label: 'Lead', required: true, state: 'empty' }],
            fresh_plan: {
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
  await page
    .locator('#workspaceRoleRoster [data-role-id="lead"]')
    .getByRole('button', { name: 'Create an agent for Lead' })
    .click();
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await advanceToReviewFromTeam(page);
  await page.locator('#createFolderBtn').click();

  await expect(page.locator('#wizardStep3')).toBeVisible();
  await expect(
    page.locator('#wizardStep3 [data-issue-id="template-agent-plan-changed"]')
  ).toContainText(/blueprint team changed/i);
  await expect(page.locator('#folderNameInput')).toHaveValue('Stale Draft');
  const refreshedRole = page.locator('#workspaceRoleRoster [data-role-id="lead"]');
  await expect(refreshedRole).toContainText('Lead');
  await refreshedRole.getByRole('button', { name: 'Clear Lead' }).click();
  await expect(refreshedRole).toContainText('Missing');
  await refreshedRole.getByRole('button', { name: 'Create an agent for Lead' }).click();
  await expect(page.locator('#agentModel')).toHaveValue('after-model');
  await expect(page.locator('#agentSystemPrompt')).toHaveValue('After prompt');
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await advanceToReviewFromTeam(page);
  await page.locator('#createFolderBtn').click();
  await expect.poll(() => submittedRevisions).toHaveLength(2);
  expect(submittedRevisions).toEqual(['stale-before', 'stale-after']);
});

test('a failed strict create retries the preserved role staffing request', async ({ page }) => {
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
          error: 'Agent “Reviewed Lead” could not be created. Nothing was created.'
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
  await page
    .locator('#workspaceRoleRoster [data-role-id="lead"]')
    .getByRole('button', { name: 'Create an agent for Lead' })
    .click();
  await page.locator('#agentName').fill('Reviewed Lead');
  await page.locator('#agentSystemPrompt').fill('Preserve this setup.');
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await advanceToReviewFromTeam(page);
  await page.locator('#createFolderBtn').click();

  await expect(page.locator('#wizardStep4')).toBeVisible();
  await expect(page.locator('#workspaceReviewError')).toContainText(
    'Reviewed Lead” could not be created'
  );
  await page.locator('#createFolderBtn').click();
  await expect.poll(() => payloads).toHaveLength(2);
  expect(payloads[1].team_intent).toEqual(payloads[0].team_intent);
  expect(payloads[1].role_staffing).toEqual(payloads[0].role_staffing);
});

test('server prompt validation keeps the reviewed role draft before any create', async ({
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
        code: 'team_intent_invalid',
        error:
          'invalid prompt variable: agent "Prompt Lead" uses unknown prompt variable {{unknown}}'
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Prompt Validation Workspace');
  await advanceToTeam(page);
  await page
    .locator('#workspaceRoleRoster [data-role-id="prompt-lead"]')
    .getByRole('button', { name: 'Create an agent for Prompt Lead' })
    .click();
  await page.locator('#agentSystemPrompt').fill('Use {{unknown}}.');
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await advanceToReviewFromTeam(page);
  await page.locator('#createFolderBtn').click();

  await expect.poll(() => payloads).toHaveLength(1);
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await expect(page.locator('#wizardStep4')).toBeVisible();
  await expect(page.locator('#workspaceReviewError')).toContainText('unknown prompt variable');
  expect(payloads[0].role_staffing).toEqual([
    expect.objectContaining({
      role_id: 'prompt-lead',
      mode: 'create',
      system_prompt: 'Use {{unknown}}.'
    })
  ]);
});

test('assigning a saved definition fills one role without mutating it', async ({ page }) => {
  await page.route('**/api/agents/dashboard/list**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ agents: [{ name: 'Shared Lead', model: 'saved-model' }] })
    });
  });
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        revision: 'assign-plan-1',
        has_agents: true,
        entry_agent_name: 'Shared Lead',
        agents: [
          { name: 'Shared Lead', action: 'reuse', entry_point: true, model_source: 'existing' },
          { name: 'Brand New', action: 'create', entry_point: false, model_source: 'agent_default' }
        ],
        warnings: []
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Assigned WS');
  await advanceToTeam(page);
  const role = page.locator('#workspaceRoleRoster [data-role-id="shared-lead"]');
  await expect(role).toContainText('Missing');
  await page
    .locator('[data-suggested-agent-use="Shared Lead"][data-suggested-role-id="shared-lead"]')
    .click();
  await expect(role).toContainText('Shared Lead');
  await expect(role).toContainText('Your saved agent');

  let payload: Record<string, unknown> | undefined;
  await page.route('**/api/workspaces', async route => {
    payload = route.request().postDataJSON();
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        folder: { id: 'assigned-ws', folder_slug: 'assigned-ws' },
        seeded_starter_tasks: 0
      })
    });
  });
  await advanceToReviewFromTeam(page);
  await page.locator('#createFolderBtn').click();
  await expect.poll(() => payload).toBeTruthy();

  expect(payload?.role_staffing).toEqual([
    { role_id: 'shared-lead', mode: 'assign', name: 'Shared Lead' }
  ]);
  expect(payload?.template_agent_overrides).toBeUndefined();
});

test('a required blueprint role cannot be excluded through the retired opt-out', async ({
  page
}) => {
  await page.route('**/api/agents/dashboard/list**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ agents: [{ name: 'Blueprint Lead', model: 'gpt-5.5' }] })
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
  await page.locator('#folderNameInput').fill('Staffed WS');
  await advanceToTeam(page);

  await expect(page.locator('#workspaceTeamAdvanced')).toHaveCount(0);
  await expect(page.locator('#templateAgentReviewToggle')).toHaveCount(0);
  await expect(page.locator('#workspaceRoleRoster [data-role-id="blueprint-lead"]')).toContainText(
    'Missing'
  );
  await expect(page.locator('#workspaceTeamIssues')).toContainText(
    'Fill the required Blueprint Lead role before reviewing this workspace.'
  );
  await expect(page.locator('#wizardNextBtn')).toBeDisabled();

  await page
    .locator('[data-suggested-agent-use="Blueprint Lead"][data-suggested-role-id="blueprint-lead"]')
    .click();
  await expect(page.locator('#workspaceRoleRoster [data-role-id="blueprint-lead"]')).toContainText(
    'Blueprint Lead'
  );
  await expect(page.locator('#wizardNextBtn')).toBeEnabled();

  let payload: Record<string, unknown> | undefined;
  await page.route('**/api/workspaces', async route => {
    payload = route.request().postDataJSON();
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        folder: { id: 'staffed-ws', folder_slug: 'staffed-ws' },
        seeded_starter_tasks: 0
      })
    });
  });
  await advanceToReviewFromTeam(page);
  await page.locator('#createFolderBtn').click({ force: true });
  await page.waitForURL('**/workspaces/staffed-ws');

  expect(payload?.team_intent).toEqual({
    version: 1,
    mode: 'staffed',
    plan_revision: 'browser-reuse-plan'
  });
  expect(payload?.role_staffing).toEqual([
    { role_id: 'blueprint-lead', mode: 'assign', name: 'Blueprint Lead' }
  ]);
  expect(payload?.create_template_agents).toBeUndefined();
});

test('Your Agents suggests a same-name saved agent without auto-filling its role', async ({
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

  // A same-name blueprint declaration is a suggestion, not membership. The
  // required role remains visibly vacant and the general list still says Add.
  const blueprintMatch = cards.filter({ hasText: 'Blueprint Lead' });
  await expect(blueprintMatch.locator('button')).toHaveText('Add');
  await expect(blueprintMatch.locator('button')).toBeEnabled();
  const role = page.locator('#workspaceRoleRoster [data-role-id="blueprint-lead"]');
  await expect(role).toContainText('Missing');
  const suggestions = page.locator('#workspaceSavedAgentSuggestions');
  await expect(suggestions).toContainText('Suggested for this workspace');
  await expect(suggestions).toContainText("Matches this role's name");
  const useSuggested = suggestions.locator(
    '[data-suggested-agent-use="Blueprint Lead"][data-suggested-role-id="blueprint-lead"]'
  );
  await expect(useSuggested).toHaveText('Use this agent');

  // The suggestion carries its role explicitly; no role-scoped Assign picker
  // needs to be opened first. Only this action fills the slot.
  await useSuggested.click();
  await expect(role).toContainText('Blueprint Lead');
  await expect(role).toContainText('Your saved agent');
  await expect(blueprintMatch).toContainText('Already filling Blueprint Lead');
  await expect(blueprintMatch.locator('button')).toHaveText('Assigned');
  await expect(blueprintMatch.locator('button')).toBeDisabled();

  // Every other non-addable entry says why in text.
  const cli = cards.filter({ hasText: 'Claude Code' });
  await expect(cli).toContainText('Built-in CLI agents cannot be attached');
  await expect(cli.locator('button')).toBeDisabled();

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

test('a suggested saved agent can fill and clear a role by keyboard alone', async ({ page }) => {
  await page.route('**/api/agents/dashboard/list**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ agents: [{ name: 'Blueprint Lead', model: 'gpt-5.5' }] })
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

  // Keyboard actions use the same explicit role assignment as pointer input.
  const assign = page.locator(
    '[data-suggested-agent-use="Blueprint Lead"][data-suggested-role-id="blueprint-lead"]'
  );
  await assign.focus();
  await page.keyboard.press('Enter');
  const role = page.locator('#workspaceRoleRoster [data-role-id="blueprint-lead"]');
  await expect(role).toContainText('Blueprint Lead');
  await expect(role).toContainText('Your saved agent');
  await expect(page.locator('#wizardNextBtn')).toBeEnabled();

  // Clearing by keyboard returns the same slot to Missing without removing the
  // saved definition.
  await role.getByRole('button', { name: 'Clear Blueprint Lead' }).focus();
  await page.keyboard.press('Enter');
  await expect(role).toContainText('Missing');
  await expect(page.locator('[data-existing-agent-add="Blueprint Lead"]')).toBeEnabled();
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

  // The declared blueprint role can still be filled even though saved-agent
  // suggestions are unavailable.
  const role = page.locator('#workspaceRoleRoster .ws-role-row');
  await expect(role).toHaveCount(1);
  await role.getByRole('button', { name: 'Create an agent for Blueprint Lead' }).click();
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addFolderModal')).toBeVisible();
  const issue = page.locator('#workspaceTeamIssues [data-issue-id="saved-roster-error"]');
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
      body: JSON.stringify({
        revision: 'travels-empty-plan',
        has_agents: false,
        agents: [],
        warnings: []
      })
    });
  });

  await openCreateModal(page);
  await cardByLabel(page, 'Travels').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Empty Team WS');
  await advanceToTeam(page);

  const issue = page.locator('#workspaceTeamIssues .workspace-team-issue');
  await expect(issue).toContainText(
    'This blueprint declares no agent roles. You can still add a saved teammate.'
  );
  await expect(issue).toHaveClass(/is-advisory/);
  await expect(issue.locator('.workspace-team-issue-label')).toHaveText('Note');

  // Advisory, never blocking: Review is still reachable and Create still works.
  await advanceToReviewFromTeam(page);
  await expect(page.locator('#createFolderBtn')).toBeEnabled();
});

test('an unavailable blueprint plan fails closed without the retired opt-out', async ({ page }) => {
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
  await expect(blocker.locator('[data-team-recovery="retry-plan"]')).toBeVisible();
  await expect(blocker.locator('[data-team-recovery="edit-blueprint"]')).toBeVisible();
  await expect(blocker.locator('[data-team-recovery="exclude-blueprint-team"]')).toHaveCount(0);
  await expect(page.locator('#wizardNextBtn')).toBeDisabled();
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
      body: JSON.stringify({
        revision: 'blueprint-empty-plan',
        has_agents: false,
        agents: [],
        warnings: []
      })
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

test('changing blueprint keeps saved agents without treating a proposal as membership', async ({
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
          : {
              revision: 'dedup-empty-plan',
              has_agents: false,
              agents: [],
              warnings: []
            }
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

  // The declaration is still a vacant slot. Saved selections remain truthful
  // unassigned members; the same-name proposal does not absorb either one.
  const role = page.locator('#workspaceRoleRoster [data-role-id="research-lead"]');
  await expect(role).toContainText('Missing');
  const unassigned = page.locator('#workspaceRoleRoster .ws-role-roster__also-row');
  await expect(unassigned).toHaveCount(2);
  await expect(unassigned.nth(0)).toContainText('Research Lead');
  await expect(unassigned.nth(1)).toContainText('Data Miner');

  // Filling the declared role is a separate explicit action. Rename the new
  // holder so it cannot collide with the existing saved definition.
  await role.getByRole('button', { name: 'Create an agent for Research Lead' }).click();
  await page.locator('#agentName').fill('Project Research Lead');
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addFolderModal')).toBeVisible();

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

  expect(payload?.existing_agent_names).toEqual(['Research Lead', 'Data Miner']);
  expect(payload?.role_staffing).toEqual([
    expect.objectContaining({
      role_id: 'research-lead',
      mode: 'create',
      name: 'Project Research Lead'
    })
  ]);
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

  // The receipt distinguishes filled roles, optional vacancies, and saved
  // teammates that were explicitly added outside a role.
  await expect(receipt).toContainText(
    'Reaper Producer · PROJECT LEAD · Reaper Producer · new agent will be created'
  );
  await expect(receipt).toContainText('Session Scout · SPECIALIST · Optional');
  await expect(receipt).toContainText('Research Scout · Also in this workspace · Saved agent');
  await expect(receipt).toContainText('1 new agent will be created');
  await expect(receipt).toContainText('1 saved agent will be attached');

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
  await expect(page.locator('#workspaceRoleRoster .ws-role-row')).toHaveCount(2);
  await expect(page.locator('#workspaceRoleRoster .ws-role-roster__also-row')).toHaveCount(1);
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
  await expect(receipt).toContainText('Research Scout · Also in this workspace · Saved agent');
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
  await expect(page.locator('#workspaceRoleRoster .ws-role-row')).toHaveCount(1);
  await expect(page.locator('#workspaceRoleRoster .ws-role-roster__also-row')).toContainText(
    'Research Scout'
  );

  // Resubmitting succeeds and the failure notice is gone.
  await advanceToReviewFromTeam(page);
  await expect(page.locator('#workspaceReviewError')).toBeHidden();
  await page.locator('#createFolderBtn').click();
  await page.waitForURL('**/workspaces/recovered-ws');
});

test('Team refuses to reach Review while a plan blocker is unresolved', async ({ page }) => {
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

  // Continuing is unavailable while the reason remains visible.
  await expect(page.locator('#wizardNextBtn')).toBeDisabled();
  await expect(page.locator('#wizardStep3')).toBeVisible();
  await expect(page.locator('#wizardStep4')).toBeHidden();
  await expect(
    page.locator('#workspaceTeamIssues .workspace-team-issue.is-blocking')
  ).toBeVisible();

  // A normal blueprint cannot bypass unknown staffing by excluding its team.
  await expect(
    page.locator('#workspaceTeamIssues [data-team-recovery="exclude-blueprint-team"]')
  ).toHaveCount(0);
  await expect(page.locator('#wizardNextBtn')).toBeDisabled();
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

  // Both collections are lists with accessible names.
  await expect(page.locator('#workspaceRoleRoster .ws-role-roster__list')).toHaveRole('list');
  await expect(page.locator('#existingAgentRosterList')).toHaveRole('list');
  await expect(page.locator('#workspaceRoleRoster .ws-role-row').first()).toHaveRole('listitem');
  await expect(
    page.locator('#existingAgentRosterList .workspace-existing-agent-card').first()
  ).toHaveRole('listitem');

  // Designation and vacancy are words, not colour.
  await expect(page.locator('#workspaceRoleRoster .ws-role-row__designation').first()).toHaveText(
    'PROJECT LEAD'
  );
  await expect(page.locator('#workspaceRoleRoster .ws-role-tag').first()).toHaveText('Missing');

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
      const body = document.querySelector('.modal.show .modal-body');
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

  await page
    .locator('#workspaceRoleRoster .ws-role-row')
    .first()
    .getByRole('button', { name: /^Create an agent for / })
    .click();
  await expect(page.locator('#addAgentModal')).toHaveCSS('opacity', '1');
  expect(await noHorizontalOverflow()).toBe(true);
  await page.keyboard.press('Escape');
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await expect(page.locator('#addFolderModal')).toBeVisible();

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

  // Fill and review declared roles on Team: still no persistence request.
  const rows = page.locator('#workspaceRoleRoster .ws-role-row');
  await rows
    .filter({ hasText: 'Reaper Producer' })
    .getByRole('button', { name: 'Create an agent for Reaper Producer' })
    .click();
  await page.locator('#agentName').fill('Renamed Producer');
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await rows
    .filter({ hasText: 'Session Scout' })
    .getByRole('button', { name: 'Create an agent for Session Scout' })
    .click();
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await advanceToReviewFromTeam(page);

  // Cancel the whole wizard from Review.
  await page.locator('#addFolderModal .modal-footer [data-bs-dismiss="modal"]').click();
  await expect(page.locator('#addFolderModal')).toBeHidden();

  // The shared shell returns to its ordinary standalone contract after draft
  // use; workspace-only context and labels cannot leak into Add Agent.
  await page.evaluate(() =>
    (window as unknown as { showAddAgentModal: () => void }).showAddAgentModal()
  );
  await expect(page.locator('#addAgentModal')).toBeVisible();
  await expect(page.locator('#addAgentModalTitleText')).toHaveText('Create New Agent');
  await expect(page.locator('#agentCreateDraftContext')).toBeHidden();
  await expect(page.locator('#agentCreateCapabilitiesSection')).toBeVisible();
  await page.locator('#cancelAgentBtn').click();
  await expect(page.locator('#addAgentModal')).toBeHidden();

  expect(precreateCalls).toEqual([]);
});

// ---------------------------------------------------------------------------
// Attach an existing project (Create Workspace Slice B, FR 20–26). The native
// folder picker, the folder review, and the create are all mocked, so nothing
// here depends on a real folder or plugin.
// ---------------------------------------------------------------------------

type ExistingProjectReview = {
  entry_name: string;
  entry_candidates: string[];
  duplicate?: { workspace_id: string; name: string; slug: string } | null;
  duplicate_message?: string;
};

async function routeExistingProjectBlueprint(
  page: Page,
  review: (entryName: string) => ExistingProjectReview,
  { openAfterCreateDefault = false } = {}
) {
  const requests = {
    reviews: [] as Record<string, unknown>[],
    creates: [] as Record<string, unknown>[],
    opens: [] as string[]
  };
  await page.route('**/api/workspaces/attached-song/project/open', async route => {
    requests.opens.push(route.request().method());
    await route.fulfill({ json: { message: 'Project open request accepted' } });
  });
  await page.route('**/api/project-templates', async route => {
    await route.fulfill({
      json: {
        templates_root: '/tmp/templates',
        templates: [
          { id: 'research-project', name: 'Research Project', builtin: true },
          { id: 'travels', name: 'Travels', builtin: true },
          { id: 'content-production', name: 'Content Production', builtin: true },
          {
            id: 'song-project',
            name: 'Song Project',
            description: 'Start or attach a song project.',
            builtin: true,
            behavior_profile: 'general',
            project_entry: {
              relative_path: '{{name}}.proj',
              open_after_create_default: openAfterCreateDefault
            },
            project_connection: {
              schema_version: 1,
              supported_modes: ['existing_project', 'new_project'],
              attach_existing: { entry_extensions: ['.proj'] }
            }
          }
        ]
      }
    });
  });
  await page.route('**/api/workspaces/template-agent-plan', route =>
    route.fulfill({ json: { revision: 'empty-plan', has_agents: false, agents: [], warnings: [] } })
  );
  await page.route('**/api/folder-picker/select-path', route =>
    route.fulfill({
      json: {
        success: true,
        selected: true,
        selection_token: 'picked-folder',
        path: '/music/Two Takes'
      }
    })
  );
  await page.route('**/api/workspaces/project-connection/review', async route => {
    const body = route.request().postDataJSON();
    requests.reviews.push(body);
    await route.fulfill({
      json: {
        selected_folder: '/music/Two Takes',
        entry_extensions: ['.proj'],
        duplicate: null,
        ...review(String(body.entry_name || ''))
      }
    });
  });
  await page.route('**/api/workspaces', async route => {
    if (route.request().method() !== 'POST') return route.fallback();
    requests.creates.push(route.request().postDataJSON());
    await route.fulfill({
      status: 201,
      json: { folder: { id: 'attached-song', folder_slug: 'two-takes' }, seeded_starter_tasks: 0 }
    });
  });
  return requests;
}

test('several project files: pick one, Review states it, and create names it exactly', async ({
  page
}) => {
  const requests = await routeExistingProjectBlueprint(page, entryName => ({
    entry_name: entryName,
    entry_candidates: ['Take One.proj', 'Take Two.proj']
  }));
  await openCreateModal(page);
  await stubWorkspaceReview(page);

  await cardByLabel(page, 'Song Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Two Takes');
  await expect(page.locator('#workspaceProjectChoice')).toBeVisible();
  await expect(page.locator('#workspaceProjectModeNew')).toBeChecked();
  await page.getByLabel('Use an existing project').check();
  await expect(page.locator('#workspaceNameHint')).toHaveText('Folder: two-takes');

  // Continue is refused before a folder is chosen.
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await expect(page.locator('#workspaceExistingProjectError')).toHaveText(
    'Choose the project folder to use an existing project.'
  );

  await page.getByRole('button', { name: 'Choose project folder' }).click();
  await expect(page.locator('#workspaceExistingProjectFolder')).toHaveText('/music/Two Takes');
  const projectFile = page.getByLabel('Project file');
  await expect(projectFile).toBeVisible();
  await projectFile.selectOption('Take Two.proj');
  await expect.poll(() => requests.reviews.at(-1)?.entry_name).toBe('Take Two.proj');
  expect(requests.reviews[0]).toEqual({
    template_id: 'song-project',
    selection_token: 'picked-folder'
  });

  await advanceToReview(page);
  const receipt = page.locator('[data-existing-project-receipt]');
  await expect(receipt).toContainText('Folder: /music/Two Takes');
  await expect(receipt).toContainText('Project file: Take Two.proj');
  await expect(receipt).toContainText('Destination: Top level');
  await expect(receipt).toContainText('Ori will not move or change these files.');

  await page.locator('#createFolderBtn').click();
  await page.waitForURL('**/workspaces/two-takes');
  expect(requests.creates).toHaveLength(1);
  expect(requests.creates[0].project_connection).toEqual({
    mode_id: 'existing_project',
    selection_token: 'picked-folder',
    entry_name: 'Take Two.proj'
  });
  expect(requests.creates[0]).not.toHaveProperty('project_name');
  expect(JSON.stringify(requests.creates[0])).not.toContain('/music/Two Takes');
});

test('a folder another workspace owns is blocked, and Open existing goes there', async ({
  page
}) => {
  const requests = await routeExistingProjectBlueprint(page, () => ({
    entry_name: 'Take One.proj',
    entry_candidates: ['Take One.proj'],
    duplicate: { workspace_id: 'owner-id', name: 'Night Drive', slug: 'night-drive' },
    duplicate_message:
      '“Night Drive” already uses this folder. Open that workspace, or choose a different project folder.'
  }));
  await openCreateModal(page);

  await cardByLabel(page, 'Song Project').click();
  await advanceToWorkspaceDetails(page);
  await page.locator('#folderNameInput').fill('Second Copy');
  await page.getByLabel('Use an existing project').check();
  await page.getByRole('button', { name: 'Choose project folder' }).click();

  const block = page.locator('#workspaceExistingProjectDuplicate');
  await expect(block).toBeVisible();
  await expect(block).toContainText('“Night Drive” already uses this folder.');

  // There is no attach anyway: Details cannot be left.
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Open existing' })).toBeFocused();

  await page.getByRole('button', { name: 'Open existing' }).click();
  await page.waitForURL('**/workspaces/night-drive');
  expect(requests.creates).toHaveLength(0);
});

test('an existing project names the workspace and starts with project opening off', async ({
  page
}) => {
  const requests = await routeExistingProjectBlueprint(
    page,
    () => ({ entry_name: 'Bridge Sketch.proj', entry_candidates: ['Bridge Sketch.proj'] }),
    { openAfterCreateDefault: true }
  );
  await openCreateModal(page);
  await stubWorkspaceReview(page);

  await cardByLabel(page, 'Song Project').click();
  await advanceToWorkspaceDetails(page);
  const openToggle = page.locator('#projectTemplateOpenAfterCreateToggle');
  await expect(openToggle).toBeChecked();
  await page.getByLabel('Use an existing project').check();
  await expect(openToggle).not.toBeChecked();

  await expect(page.locator('#folderNameInput')).toHaveValue('');
  await page.getByRole('button', { name: 'Choose project folder' }).click();
  await expect(page.locator('#folderNameInput')).toHaveValue('Bridge Sketch');
  await expect(page.locator('#workspaceNameHint')).toHaveText('Folder: bridge-sketch');
  await expect(page.locator('#workspaceExistingProjectStatus')).toHaveText(
    'Project file: Bridge Sketch.proj'
  );

  // Still the user's choice: turned on, the attached project opens after create.
  await openToggle.check();
  await advanceToReview(page);
  await page.locator('#createFolderBtn').click();
  await page.waitForURL('**/workspaces/two-takes');
  expect(requests.creates).toHaveLength(1);
  expect(requests.creates[0].name).toBe('Bridge Sketch');
  expect(requests.opens).toEqual(['POST']);
});

// ----- Blueprint-declared inputs -----

// A blueprint that asks for two typed values and writes them into the project
// it creates. Generic on purpose: everything the card shows comes from here.
async function routeBlueprintInputsTemplate(page: Page) {
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
            id: 'session-project',
            name: 'Session Project',
            description: 'A blueprint that asks for values before it creates the project.',
            builtin: true,
            behavior_profile: 'general',
            project_entry: { relative_path: '{{name}}.proj' },
            inputs: {
              schema_version: 1,
              title: 'Session settings',
              apply_to: ['{{name}}.proj'],
              fields: [
                {
                  id: 'tempo',
                  label: 'Tempo',
                  type: 'number',
                  min: 40,
                  max: 240,
                  step: 1,
                  default: 120,
                  unit: 'BPM'
                },
                {
                  id: 'time_signature',
                  label: 'Time signature',
                  type: 'select',
                  default: '4 4',
                  options: [
                    { value: '4 4', label: '4/4' },
                    { value: '3 4', label: '3/4' },
                    { value: '6 8', label: '6/8' }
                  ]
                }
              ]
            }
          }
        ]
      })
    });
  });
  await page.route('**/api/workspaces/template-agent-plan**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ revision: 'empty-plan', has_agents: false, agents: [], warnings: [] })
    });
  });
}

test('Details asks for a blueprint’s declared inputs, refuses a value it does not allow, and Review states them', async ({
  page
}) => {
  await routeBlueprintInputsTemplate(page);
  await openCreateModal(page);
  await stubWorkspaceReview(page);

  // A blueprint that asks nothing shows no card.
  await cardByLabel(page, 'Research Project').click();
  await advanceToWorkspaceDetails(page);
  await expect(page.locator('#workspaceBlueprintInputs')).toBeHidden();

  await page.locator('#wizardBackBtn').click();
  await expect(page.locator('#wizardStep1')).toBeVisible();
  await cardByLabel(page, 'Session Project').click();
  await advanceToWorkspaceDetails(page);

  const card = page.locator('#workspaceBlueprintInputs');
  await expect(card).toBeVisible();
  await expect(page.locator('#workspaceBlueprintInputsTitle')).toHaveText('Session settings');
  const tempo = page.locator('#workspaceBlueprintInput-tempo');
  const signature = page.locator('#workspaceBlueprintInput-time_signature');
  await expect(tempo).toHaveValue('120');
  await expect(signature).toHaveValue('4 4');
  await expect(page.locator('#workspaceBlueprintInput-tempo-hint')).toHaveText('40–240');

  await page.locator('#folderNameInput').fill('Night Drive');

  // Out of range blocks Continue, the same way an empty name does.
  await tempo.fill('900');
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await expect(page.locator('#workspaceBlueprintInput-tempo-error')).toHaveText(
    'Tempo must be between 40 and 240.'
  );

  await tempo.fill('96');
  await signature.selectOption('3 4');
  await expect(page.locator('#workspaceBlueprintInput-tempo-error')).toBeHidden();

  await advanceToReview(page);
  const receipt = page.locator('[data-blueprint-inputs-receipt]');
  await expect(receipt).toContainText('Session settings');
  await expect(receipt).toContainText('Tempo: 96 BPM');
  await expect(receipt).toContainText('Time signature: 3/4');
  await expect(receipt.getByRole('button', { name: 'Edit' })).toBeVisible();
});

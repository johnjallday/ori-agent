import { test, expect } from '@playwright/test';
import { Buffer } from 'node:buffer';

/**
 * Smoke tests to verify basic frontend functionality.
 * Run with: npx playwright test tests/smoke.spec.ts
 */

test.describe('Smoke Tests', () => {
  test('homepage loads successfully', async ({ page }) => {
    await page.goto('/');

    // Check page title or main content loads
    await expect(page.locator('body')).toBeVisible();

    // Verify navbar is present
    await expect(page.locator('nav, .navbar, [role="navigation"]').first()).toBeVisible();

    // Check no console errors (optional - remove if noisy)
    const errors: string[] = [];
    page.on('console', msg => {
      if (msg.type() === 'error') {
        errors.push(msg.text());
      }
    });

    // Filter out expected errors (add patterns as needed)
    const unexpectedErrors = errors.filter(e => !e.includes('favicon') && !e.includes('404'));

    expect(unexpectedErrors).toHaveLength(0);
  });

  test('theme toggle works', async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('html')).toBeAttached();

    // Get initial theme
    const html = page.locator('html');
    const initialTheme = await html.getAttribute('data-bs-theme');

    // Find and click theme toggle (adjust selector based on your UI)
    const themeToggle = page.locator('[data-theme-toggle], #themeToggle, .theme-toggle').first();

    if (await themeToggle.isVisible()) {
      await themeToggle.click();

      // Verify theme changed
      const newTheme = await html.getAttribute('data-bs-theme');
      expect(newTheme).not.toBe(initialTheme);
    }
  });

  test('home uses navbar navigation without a sidebar', async ({ page }) => {
    await page.goto('/');

    await expect(page.locator('.navbar').first()).toBeVisible();
    await expect(page.locator('#sidebar')).toHaveCount(0);
    await expect(page.locator('#sidebarToggle')).toHaveCount(0);
  });
});

test.describe('Onboarding', () => {
  async function installBaseOnboardingRoutes(page) {
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
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
      });
    });
    await page.route('**/api/onboarding/names', async route => {
      const body = route.request().postDataJSON();
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          user_name: body.user_name || '',
          assistant_name: body.assistant_name || 'Ori'
        })
      });
    });
    await page.route('**/api/onboarding/step', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true })
      });
    });
  }

  test('keeps the first step focused on naming and shows progress', async ({ page }) => {
    await installBaseOnboardingRoutes(page);
    await page.goto('/');

    await expect(page.locator('#onboardingModal')).toBeVisible();
    await expect(page.locator('#onboardingStepLabel')).toHaveText('Step 1 of 3');
    await expect(page.locator('#onboardingWorkspaceRoot')).toHaveCount(0);
    await expect(page.locator('#onboardingVaultRoot')).toHaveCount(0);
    await expect(
      page.getByText('Storage locations can be changed later in Settings when you need them.')
    ).toBeVisible();
    await expect(page.getByRole('button', { name: 'Set Up Later' })).toBeVisible();
  });

  test('auto-selects a recommended model before continuing', async ({ page }) => {
    await installBaseOnboardingRoutes(page);
    await page.route('**/api/providers', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          providers: [{ name: 'ollama', display_name: 'Ollama', available: true }]
        })
      });
    });
    await page.route('**/api/settings/available-models?provider=ollama', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          available: true,
          model_options: [
            { id: 'llama-small', label: 'Llama Small', description: 'Fast', recommended: false },
            {
              id: 'llama-balanced',
              label: 'Llama Balanced',
              description: 'Recommended',
              recommended: true
            }
          ]
        })
      });
    });

    await page.goto('/');
    await page.locator('#onboardingUserName').fill('Jamie');
    await page.locator('#welcomeNextBtn').click();

    await expect(page.locator('#onboardingStepLabel')).toHaveText('Step 2 of 3');
    await expect(page.locator('#onboardingSystemProvider')).toHaveValue('ollama');
    await expect(page.locator('#onboardingSystemModel')).toHaveValue('llama-balanced');
    await expect(page.locator('#modelNextBtn')).toBeEnabled();
    await expect(page.locator('#modelBackBtn')).toBeVisible();
  });

  test('blocks progress when no usable provider is available', async ({ page }) => {
    await installBaseOnboardingRoutes(page);
    await page.route('**/api/providers', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ providers: [] })
      });
    });

    await page.goto('/');
    await page.locator('#welcomeNextBtn').click();

    await expect(page.locator('#onboardingApiKeySection')).toBeVisible();
    await expect(page.locator('#modelNextBtn')).toBeDisabled();
    await expect(page.getByRole('button', { name: 'Set Up Later' })).toBeVisible();
  });
});

test.describe('Home First Run', () => {
  test('makes workspace creation the primary next step when no workspaces exist', async ({
    page,
    request
  }) => {
    const response = await request.get('/api/workspaces');
    const data = await response.json();
    test.skip((data.workspaces || []).length !== 0, 'requires an empty workspace store');

    await page.goto('/');
    await expect(page.locator('body.home-command-page')).toBeVisible();
    await expect(page.locator('#homeAssistantCard')).toHaveAttribute('data-first-run', 'true');
    await expect(page.locator('#homeDashboardSections')).toHaveAttribute('data-first-run', 'true');
    await expect(page.locator('#homeFirstRunStart')).toContainText('Establish first workspace');
    await expect(page.locator('#homeAssistantInput')).toHaveAttribute(
      'placeholder',
      'Plan a product launch…'
    );
    await expect(page.getByRole('button', { name: 'Create a workspace' })).toBeVisible();
    await expect(page.locator('#sidebar')).toHaveCount(0);
  });

  test('keeps the command-strip interaction contract on home', async ({ page }) => {
    await page.goto('/');

    await expect(page.locator('#homeAssistantCard')).toBeVisible();
    await expect(page.locator('#homeAssistantCard h1')).toHaveCount(0);
    await expect(page.locator('#homeAssistantInput')).toBeVisible();
    await expect(page.locator('#homeAssistantSendBtn')).toBeVisible();
    await expect(page.locator('.home-prompt-chip')).toHaveCount(3);
    await expect(page.locator('#homeDashboardSections')).toBeVisible();
  });

  test('preserves quick-order chips, the command shortcut, and submit flow', async ({ page }) => {
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });

    await page.goto('/');
    const input = page.locator('#homeAssistantInput');
    const chip = page.locator('.home-prompt-chip').first();
    const prompt = await chip.getAttribute('data-prompt');

    await chip.focus();
    await expect(chip).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(input).toHaveValue(prompt || '');

    await page.keyboard.press('Control+j');
    await expect(input).toBeFocused();

    await input.fill('Summarize current operations');
    await page.keyboard.press('Enter');
    await expect(page.locator('#homeAssistantThinkingModal')).toHaveClass(/show/);
  });

  test('renders the bridge without browser console errors', async ({ page }) => {
    const errors: string[] = [];
    page.on('console', message => {
      if (message.type() === 'error') errors.push(message.text());
    });
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });
    await page.route(/\/api\/agents\?name=Ori$/, async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ allow_web_search: false })
      });
    });

    await page.goto('/');
    await expect(page.locator('.home-command-bridge')).toBeVisible();
    expect(errors).toEqual([]);
  });

  test('keeps maximum bridge readouts inside a desktop viewport', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });

    const workspaces = Array.from({ length: 6 }, (_, index) => ({
      id: `bridge-${index + 1}`,
      name: `Bridge workspace ${index + 1}`,
      updated_at: `2026-07-${String(11 - index).padStart(2, '0')}T12:00:00Z`,
      agent_count: index + 1,
      open_task_count: index === 1 ? 2 : 0,
      needs_attention_count: index === 0 ? 1 : 0
    }));
    await page.route('**/api/workspaces', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ workspaces })
      });
    });
    await page.route('**/api/orchestration/scheduled-tasks/upcoming**', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          upcoming: Array.from({ length: 5 }, (_, index) => ({
            task_name: `Scheduled operation ${index + 1}`,
            workspace_id: `bridge-${index + 1}`,
            workspace_name: `Bridge workspace ${index + 1}`,
            agent_name: `Agent ${index + 1}`,
            next_run: `2026-07-12T${String(13 + index).padStart(2, '0')}:00:00Z`
          }))
        })
      });
    });
    await page.route('**/api/activity/recent**', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          events: Array.from({ length: 5 }, (_, index) => ({
            kind: index === 0 ? 'task_completed' : 'note_edited',
            description: `Operation event ${index + 1}`,
            workspace_id: `bridge-${index + 1}`,
            workspace_name: `Bridge workspace ${index + 1}`,
            timestamp: `2026-07-12T${String(11 + index).padStart(2, '0')}:00:00Z`
          }))
        })
      });
    });
    await page.route('**/api/progression', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          current_tier: 1,
          total_tiers: 3,
          total_count: 3,
          completed_count: 1,
          dismissed: false,
          all_complete: false,
          tiers: [
            {
              tier: 1,
              name: 'First contact',
              quests: [
                { id: 'bridge-q1', title: 'Establish a workspace', status: 'completed' },
                {
                  id: 'bridge-q2',
                  title: 'Plan an operation',
                  status: 'pending',
                  action_url: '/workspaces'
                },
                { id: 'bridge-q3', title: 'Run a review', status: 'pending', action_url: '/review' }
              ]
            }
          ]
        })
      });
    });

    await page.goto('/');
    await expect(page.locator('[data-role="workspace-card"]')).toHaveCount(6);
    await expect(page.locator('.home-row')).toHaveCount(10);
    await expect(page.locator('.home-status-led.is-attention')).toHaveCount(1);
    await expect(page.locator('.home-status-led.is-working')).toHaveCount(1);
    await expect(page.locator('#questLog')).toBeVisible();

    for (const [width, height] of [
      [1280, 800],
      [1512, 805]
    ]) {
      await page.setViewportSize({ width, height });
      const dimensions = await page.evaluate(() => ({
        viewport: window.innerHeight,
        documentHeight: document.documentElement.scrollHeight,
        bodyHeight: document.body.scrollHeight
      }));
      expect(dimensions.documentHeight).toBeLessThanOrEqual(dimensions.viewport + 1);
      expect(dimensions.bodyHeight).toBeLessThanOrEqual(dimensions.viewport + 1);
    }
  });

  test('collapses the quest log to a progress badge and restores it', async ({ page }) => {
    const status = {
      current_tier: 1,
      total_tiers: 3,
      total_count: 2,
      completed_count: 1,
      all_complete: false,
      tiers: [
        {
          tier: 1,
          name: 'First contact',
          quests: [
            { id: 'collapse-q1', title: 'Say hello to Ori', status: 'completed' },
            {
              id: 'collapse-q2',
              title: 'Personalize Ori',
              status: 'pending',
              action_url: '/profile'
            }
          ]
        }
      ]
    };
    const dismisses: boolean[] = [];
    let dismissed = false;
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });
    await page.route('**/api/progression/dismiss', async route => {
      const request = route.request().postDataJSON();
      dismissed = Boolean(request.dismissed);
      dismisses.push(dismissed);
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ ...status, dismissed })
      });
    });
    await page.route('**/api/progression', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ ...status, dismissed })
      });
    });

    await page.goto('/');
    await expect(page.locator('#questLog')).toBeVisible();
    await expect(page.locator('[data-role="progress-count"]')).toHaveText('1/2');

    const collapse = page.locator('[data-role="dismiss"]');
    await collapse.focus();
    await expect(collapse).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page.locator('#questLog')).toBeHidden();
    await expect(page.locator('#questLogRestore')).toBeVisible();
    await expect(page.locator('[data-role="restore-progress"]')).toHaveText('1/2');

    await page.reload();
    await expect(page.locator('#questLog')).toBeHidden();
    await expect(page.locator('#questLogRestore')).toBeVisible();

    const restore = page.locator('[data-role="restore"]');
    await restore.focus();
    await expect(restore).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page.locator('#questLog')).toBeVisible();
    await expect(page.locator('#questLogRestore')).toBeHidden();
    expect(dismisses).toEqual([true, false]);
  });

  test('stacks bridge zones below desktop width without horizontal overflow', async ({ page }) => {
    await page.setViewportSize({ width: 960, height: 720 });
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });
    await page.route('**/api/progression', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          current_tier: 1,
          total_tiers: 3,
          total_count: 2,
          completed_count: 0,
          dismissed: false,
          all_complete: false,
          tiers: [
            {
              tier: 1,
              name: 'First contact',
              quests: [
                { id: 'responsive-q1', title: 'Say hello to Ori', status: 'pending' },
                { id: 'responsive-q2', title: 'Create a workspace', status: 'pending' }
              ]
            }
          ]
        })
      });
    });

    await page.goto('/');
    await expect(page.locator('#homeDashboardSections')).toBeVisible();
    await expect(page.locator('#questLog')).toBeVisible();

    const layout = await page.evaluate(() => {
      const board = document.getElementById('homeDashboardSections')?.getBoundingClientRect();
      const progression = document.querySelector('.home-progression-zone')?.getBoundingClientRect();
      return {
        pageWidth: document.documentElement.scrollWidth,
        viewportWidth: window.innerWidth,
        boardBottom: board?.bottom || 0,
        progressionTop: progression?.top || 0
      };
    });
    expect(layout.pageWidth).toBeLessThanOrEqual(layout.viewportWidth + 1);
    expect(layout.progressionTop).toBeGreaterThanOrEqual(layout.boardBottom);
  });
});

test.describe('Agent Management', () => {
  test('can open create agent modal', async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('body')).toBeVisible();

    // Look for create agent button (adjust selector based on your UI)
    const createBtn = page
      .locator('button:has-text("Create"), [data-bs-target="#addAgentModal"]')
      .first();

    if (await createBtn.isVisible()) {
      await createBtn.click();

      // Verify modal opens
      const modal = page.locator('#addAgentModal');
      await expect(modal).toBeVisible();

      // Verify form fields exist
      await expect(page.locator('#agentName')).toBeVisible();
      await expect(page.locator('#agentType')).toBeVisible();
    }
  });

  test('agent form validation works', async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('body')).toBeVisible();

    // Open create modal
    const createBtn = page.locator('[data-bs-target="#addAgentModal"]').first();

    if (await createBtn.isVisible()) {
      await createBtn.click();
      await page.waitForSelector('#addAgentModal.show');

      // Try to submit without name
      const submitBtn = page.locator('#createAgentBtn');
      await submitBtn.click();

      // Check that form didn't submit (modal still visible) or validation message shown
      await expect(page.locator('#addAgentModal')).toBeVisible();
    }
  });
});

test.describe('Workspace Import Flow', () => {
  test('import modal supports picker selection and duplicate override', async ({ page }) => {
    await page.route('**/api/folder-picker/select-path', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          selected: true,
          path: '/tmp/demo-project'
        })
      });
    });

    await page.route('**/api/workspaces/import/check*', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          duplicate: {
            found: true,
            workspace_id: 'existing-ws',
            workspace_name: 'Existing Workspace'
          }
        })
      });
    });

    await page.route('**/api/workspaces/import/duplicate-action', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true })
      });
    });

    let importAttemptCount = 0;
    await page.route('**/api/workspaces/import', async route => {
      importAttemptCount += 1;
      const body = route.request().postDataJSON();

      if (!body.allow_duplicate) {
        await route.fulfill({
          status: 409,
          contentType: 'application/json',
          body: JSON.stringify({
            success: false,
            error: 'Folder is already imported in another workspace',
            duplicate: {
              found: true,
              workspace_id: 'existing-ws',
              workspace_name: 'Existing Workspace'
            }
          })
        });
        return;
      }

      await route.fulfill({
        status: 201,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          folder: {
            id: 'new-imported-ws',
            name: body.name || 'demo-project'
          },
          directory: {
            id: 'dir-ref-1',
            workspace_id: 'new-imported-ws',
            name: 'demo-project',
            path: '/tmp/demo-project'
          },
          duplicate: { found: false }
        })
      });
    });

    await page.goto('/workspaces');
    await expect(page.locator('#launcherCreateWorkspaceBtn')).toBeVisible();
    const modal = page.locator('#addFolderModal');
    await page.locator('#launcherImportFolderBtn').click();
    await expect(modal).toBeVisible();

    const importToggle = page.locator('#folderImportToggle');
    await expect(importToggle).toBeChecked();
    await expect(page.locator('#folderImportSection')).toBeVisible();

    await page.locator('#folderImportBrowseBtn').click();
    await expect(page.locator('#folderImportPathInput')).toHaveValue('/tmp/demo-project');
    await expect(page.locator('#folderImportDuplicateWarning')).toBeVisible();
    await page.locator('#folderDescriptionInput').fill('Imported demo project for smoke coverage.');

    await page.locator('#folderImportProceedDuplicateBtn').click();
    await page.locator('#createFolderBtn').click();

    await expect(page.locator('#addFolderModal.show')).toHaveCount(0);
    expect(importAttemptCount).toBeGreaterThan(0);
  });

  test('import controls are keyboard and mobile friendly', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto('/workspaces');
    await expect(page.locator('#launcherCreateWorkspaceBtn')).toBeVisible();

    const modal = page.locator('#addFolderModal');
    await page.locator('#launcherImportFolderBtn').focus();
    await page.keyboard.press('Enter');
    await expect(modal).toBeVisible();

    const importToggle = page.locator('#folderImportToggle');
    await expect(importToggle).toBeChecked();
    await expect(page.locator('#folderImportSection')).toBeVisible();

    const pathBox = await page.locator('#folderImportPathInput').boundingBox();
    const browseBox = await page.locator('#folderImportBrowseBtn').boundingBox();
    expect(pathBox).not.toBeNull();
    expect(browseBox).not.toBeNull();
    if (pathBox && browseBox) {
      expect(browseBox.y).toBeGreaterThanOrEqual(pathBox.y);
    }
  });
});

test.describe('API Health', () => {
  test('health endpoint returns OK', async ({ request }) => {
    const response = await request.get('/health');
    expect(response.ok()).toBeTruthy();
  });

  test('agents API is accessible', async ({ request }) => {
    const response = await request.get('/api/agents');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    expect(Array.isArray(data) || typeof data === 'object').toBeTruthy();
  });

  test('plugins API is accessible', async ({ request }) => {
    const response = await request.get('/api/plugins');
    expect(response.ok()).toBeTruthy();
  });
});

test.describe('Workspace Agent Character Roster', () => {
  async function installRosterRoutes(page, options = {}) {
    const workspace = {
      id: 'roster-ws',
      name: 'Roster Workspace',
      description: 'Workspace for roster UI smoke coverage',
      entry_agent_name: 'Roster Manager',
      agents: ['Roster Manager', 'Research Analyst'],
      agent_instances: [
        {
          id: 'manager-1',
          name: 'Roster Manager',
          instance_number: 1,
          node_id: 'Roster Manager-node-1',
          role: 'Coordinator',
          entry_point: true
        },
        {
          id: 'analyst-1',
          name: 'Research Analyst',
          instance_number: 1,
          node_id: 'Research Analyst-node-1',
          role: 'Research'
        },
        {
          id: 'analyst-2',
          name: 'Research Analyst',
          instance_number: 2,
          node_id: 'Research Analyst-node-2',
          role: 'Synthesis'
        }
      ],
      shared_data: {},
      skill_bindings: [
        { id: 'skill-planning', skill_name: 'workspace-planning', enabled: true, trusted: true },
        {
          id: 'skill-research',
          skill_name: 'browser:control-in-app-browser',
          enabled: true,
          trusted: true
        }
      ],
      agent_skill_access: [
        { agent_instance_id: 'manager-1', enabled_binding_ids: ['skill-planning'] },
        { agent_instance_id: 'analyst-1', enabled_binding_ids: ['skill-research'] },
        { agent_instance_id: 'analyst-2', enabled_binding_ids: ['skill-research'] }
      ],
      mcp_bindings: [],
      agent_mcp_access: [],
      directory_references: [],
      attachments: [],
      tasks: [],
      status: 'active'
    };
    const tasks = [
      {
        id: 'task-1',
        workspace_id: 'roster-ws',
        to: 'Roster Manager',
        status: 'pending',
        description: 'Plan the work'
      },
      {
        id: 'task-2',
        workspace_id: 'roster-ws',
        to: 'Research Analyst',
        status: 'waiting_for_choice',
        description: 'Choose source'
      },
      {
        id: 'task-3',
        workspace_id: 'roster-ws',
        to: 'Research Analyst',
        status: 'in_progress',
        description: 'Read source',
        parent_task_id: 'task-1'
      }
    ];
    const catalogAgents = [
      ...(options.omitRosterManagerFromCatalog
        ? []
        : [
            {
              name: 'Roster Manager',
              type: 'general',
              source: 'user',
              model: 'claude-opus-4',
              provider: 'anthropic',
              capabilities: ['files']
            }
          ]),
      {
        name: 'Research Analyst',
        type: 'research',
        source: 'user',
        model: 'claude-sonnet-4',
        provider: 'anthropic',
        allow_web_search: true
      }
    ];
    const snapshotAgents = Array.isArray(options.snapshotAgents) ? options.snapshotAgents : [];

    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          needs_onboarding: false,
          current_step: 3,
          completed: true,
          skipped: false,
          steps_completed: [0, 1, 2],
          user_name: 'Tester',
          assistant_name: 'Ori'
        })
      });
    });
    await page.route('**/api/orchestration/workspace?id=roster-ws', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(workspace)
      });
    });
    await page.route('**/api/workspaces/roster-ws/agent-snapshots', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ agents: snapshotAgents })
      });
    });
    await page.route('**/api/agents/dashboard/list', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ agents: catalogAgents })
      });
    });
    await page.route('**/api/skills?agent=default', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          skills: [
            { name: 'workspace-planning', enabled: true },
            { name: 'browser:control-in-app-browser', enabled: true }
          ]
        })
      });
    });
    await page.route('**/api/orchestration/tasks?workspace_id=roster-ws', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ tasks })
      });
    });
    await page.route('**/api/sessions?folder_id=roster-ws', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ sessions: [] })
      });
    });
    await page.route('**/api/workspaces/roster-ws/notes', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ notes: [] })
      });
    });
    await page.route('**/api/workspaces/roster-ws/mission', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          mission: '',
          mission_enabled: false,
          cadence: null,
          mission_execution_count: 0,
          mission_failure_count: 0,
          open_findings_count: 0
        })
      });
    });
    await page.route('**/api/workspaces/roster-ws/agents/*/effective-prompt', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          base_system_prompt: 'You are a focused workspace agent.',
          effective_prompt: 'You are a focused workspace agent.'
        })
      });
    });
    await page.route('**/api/workspaces/roster-ws', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(workspace)
      });
    });
    await page.route('**/api/workspaces?tree=true', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ workspaces: [workspace], folders: [workspace] })
      });
    });
    await page.route('**/api/orchestration/workspace/activate?id=roster-ws', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true })
      });
    });
    await page.route('**/api/project-templates', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ templates: [] })
      });
    });
  }

  test('command deck selects roster characters and updates the shared agent overview', async ({
    page
  }) => {
    await installRosterRoutes(page);
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto('/workspaces/roster-ws');

    const roster = page.locator('#workspaceCommandView .ws-cmd-roster-item');
    await expect(roster).toHaveCount(2);
    await expect(roster.first()).toHaveAttribute('aria-pressed', 'true');
    await expect(roster.first()).toContainText('Roster Manager');
    await expect(roster.first()).toContainText('Entry');
    await expect(roster.first().locator('.ws-cmd-character svg')).toBeVisible();
    await expect(roster.nth(1)).toContainText('Working');
    await expect(roster.nth(1)).toContainText('2×');

    const stage = page.locator('#workspaceCommandView .ws-cmd-agent-stage');
    await expect(stage.locator('h3')).toHaveText('Roster Manager');
    await expect(stage).toContainText('Entry Agent');
    await expect(stage).toContainText('Idle');

    await roster.nth(1).click();
    await expect(stage.locator('h3')).toHaveText('Research Analyst');
    await expect(stage).toContainText('Working');

    await page.getByRole('tab', { name: 'Tasks' }).click();
    await expect(page.locator('.ws-cmd-agent-tabpanel.is-active')).toContainText('Choose source');

    await page.getByRole('tab', { name: 'Loadout' }).click();
    await expect(page.locator('.ws-cmd-agent-tabpanel.is-active')).toContainText('Model');
    await expect(page.locator('.ws-cmd-agent-tabpanel.is-active')).toContainText('Skills');
    await expect(page.locator('.ws-cmd-loadout-prompt')).toContainText(
      'You are a focused workspace agent.'
    );
  });

  test('command deck uses the required desktop, tablet, and mobile layouts without page overflow', async ({
    page
  }) => {
    await installRosterRoutes(page);
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto('/workspaces/roster-ws');
    await expect(page.locator('.ws-cmd-roster-item')).toHaveCount(2);
    await expect(page.locator('.ws-cmd-deck')).toBeVisible();

    const mission = page.locator('#workspace-command-mission-card');
    await expect(mission).toBeVisible();
    expect((await mission.boundingBox())?.height || 0).toBeLessThanOrEqual(160);

    const desktopGeometry = await page.locator('.ws-cmd-deck').evaluate(element => {
      const roster = element.querySelector('.ws-cmd-roster')?.getBoundingClientRect();
      const stage = element.querySelector('.ws-cmd-agent-stage')?.getBoundingClientRect();
      const overview = element.querySelector('.ws-cmd-agent-overview')?.getBoundingClientRect();
      return {
        rosterTop: roster?.top,
        stageTop: stage?.top,
        overviewTop: overview?.top,
        rosterRight: roster?.right,
        stageLeft: stage?.left,
        stageRight: stage?.right,
        overviewLeft: overview?.left
      };
    });
    expect(
      Math.abs((desktopGeometry.rosterTop || 0) - (desktopGeometry.stageTop || 0))
    ).toBeLessThan(2);
    expect(
      Math.abs((desktopGeometry.stageTop || 0) - (desktopGeometry.overviewTop || 0))
    ).toBeLessThan(2);
    expect(desktopGeometry.rosterRight).toBeLessThanOrEqual(desktopGeometry.stageLeft || 0);
    expect(desktopGeometry.stageRight).toBeLessThanOrEqual(desktopGeometry.overviewLeft || 0);

    await page.setViewportSize({ width: 1024, height: 768 });
    const tabletGeometry = await page.locator('.ws-cmd-deck').evaluate(element => {
      const roster = element.querySelector('.ws-cmd-roster')?.getBoundingClientRect();
      const stage = element.querySelector('.ws-cmd-agent-stage')?.getBoundingClientRect();
      const overview = element.querySelector('.ws-cmd-agent-overview')?.getBoundingClientRect();
      return { rosterBottom: roster?.bottom, stageTop: stage?.top, overviewTop: overview?.top };
    });
    expect(tabletGeometry.rosterBottom).toBeLessThanOrEqual(tabletGeometry.stageTop || 0);
    expect(
      Math.abs((tabletGeometry.stageTop || 0) - (tabletGeometry.overviewTop || 0))
    ).toBeLessThan(2);

    await page.setViewportSize({ width: 390, height: 844 });
    const mobileGeometry = await page.locator('.ws-cmd-deck').evaluate(element => {
      const stage = element.querySelector('.ws-cmd-agent-stage')?.getBoundingClientRect();
      const overview = element.querySelector('.ws-cmd-agent-overview')?.getBoundingClientRect();
      return {
        stageBottom: stage?.bottom,
        overviewTop: overview?.top,
        pageWidth: document.documentElement.scrollWidth,
        viewportWidth: window.innerWidth
      };
    });
    expect(mobileGeometry.stageBottom).toBeLessThanOrEqual(mobileGeometry.overviewTop || 0);
    expect(mobileGeometry.pageWidth).toBeLessThanOrEqual(mobileGeometry.viewportWidth);
  });

  test('operations map switches from details, selects agents, and opens inventory', async ({
    page
  }) => {
    await installRosterRoutes(page);
    await page.addInitScript(() => {
      window.localStorage.removeItem('oriWorkspaceCommandViewMode');
    });
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto('/workspaces/roster-ws');

    await page.locator('#workspaceCommandView [data-cmd-view-mode="map"]').click();
    await expect(page.locator('#workspaceCommandView .ws-cmd-opmap')).toBeVisible();
    await expect(page.locator('#workspaceCommandView [data-map-zone="mission"]')).toHaveCount(0);
    await expect(page.locator('#workspaceCommandView [data-map-zone="tasks"]')).toHaveCount(0);
    await expect(page.locator('#workspaceCommandView [data-map-zone="tools"]')).toHaveCount(0);
    await expect(page.locator('#workspaceCommandView .ws-cmd-map-window')).toHaveCount(0);
    await expect(page.locator('#workspaceCommandView [data-map-zone="agents"]')).toContainText(
      'Research Analyst'
    );
    const beltGeometry = await page
      .locator('#workspaceCommandView .ws-cmd-map-belt')
      .evaluate(node => {
        const style = window.getComputedStyle(node);
        const rect = node.getBoundingClientRect();
        const mapRect = node.closest('.ws-cmd-opmap').getBoundingClientRect();
        return {
          flexDirection: style.flexDirection,
          rightGap: Math.round(mapRect.right - rect.right),
          topGap: Math.round(rect.top - mapRect.top)
        };
      });
    expect(beltGeometry.flexDirection).toBe('row');
    expect(beltGeometry.rightGap).toBeLessThanOrEqual(24);
    expect(beltGeometry.topGap).toBeLessThanOrEqual(24);
    const beltLabelGeometry = await page
      .locator('#workspaceCommandView .ws-cmd-map-belt-btn')
      .evaluateAll(buttons =>
        buttons.map(button => {
          const label = button.querySelector('.sr-only');
          const labelStyle = label ? window.getComputedStyle(label) : null;
          const labelRect = label ? label.getBoundingClientRect() : null;
          const buttonRect = button.getBoundingClientRect();
          return {
            ariaLabel: button.getAttribute('aria-label'),
            buttonHeight: Math.round(buttonRect.height),
            buttonWidth: Math.round(buttonRect.width),
            labelHeight: labelRect ? Math.round(labelRect.height) : 0,
            labelOverflow: labelStyle?.overflow || '',
            labelText: label?.textContent?.trim() || '',
            labelWidth: labelRect ? Math.round(labelRect.width) : 0
          };
        })
      );
    expect(beltLabelGeometry).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          ariaLabel: 'Workspace Objective',
          labelText: 'Workspace Objective'
        })
      ])
    );
    expect(
      beltLabelGeometry.every(
        item =>
          item.buttonHeight >= 44 &&
          item.buttonWidth >= 44 &&
          item.labelHeight <= 1 &&
          item.labelWidth <= 1 &&
          item.labelOverflow === 'hidden'
      )
    ).toBeTruthy();
    await expect(page.locator('#workspaceCommandView .ws-cmd-map-station-node')).toHaveCount(0);
    await expect(page.locator('#workspaceCommandView .ws-cmd-map-station-route')).toHaveCount(0);
    const entryUnit = page
      .locator('#workspaceCommandView .ws-cmd-map-agent')
      .filter({ hasText: 'Roster Manager' });
    await expect(entryUnit.locator('.ws-cmd-map-entry-badge')).toBeVisible();
    await expect(entryUnit.locator('.ws-cmd-map-agent-status')).toBeVisible();
    await expect(entryUnit).toHaveClass(/waiting/);
    await expect(entryUnit).toHaveAttribute('aria-label', /Entry Agent/);

    await page
      .locator('#workspaceCommandView .ws-cmd-map-belt-btn[data-cmd-map-window="inventory"]')
      .click();
    const inventoryWindow = page.locator('#workspaceCommandView .ws-cmd-map-window');
    const activeInventoryGroup = page.locator(
      '#workspaceCommandView .ws-cmd-map-inventory-group.is-active'
    );
    await expect(inventoryWindow).toBeVisible();
    await expect(inventoryWindow).toContainText('Inventory');
    await expect(activeInventoryGroup).toContainText('Notes');
    await expect(activeInventoryGroup.locator('.ws-cmd-map-inventory-grid')).toBeVisible();
    await expect(activeInventoryGroup.locator('.ws-cmd-map-inventory-slot').first()).toBeVisible();
    await expect(activeInventoryGroup.locator('.ws-cmd-map-slot-type').first()).toContainText(
      'Note'
    );
    const inventoryGeometry = await inventoryWindow.evaluate(node => {
      const rect = node.getBoundingClientRect();
      return {
        top: rect.top,
        bottom: rect.bottom,
        viewportHeight: window.innerHeight
      };
    });
    expect(inventoryGeometry.top).toBeGreaterThanOrEqual(0);
    expect(inventoryGeometry.bottom).toBeLessThanOrEqual(inventoryGeometry.viewportHeight);
    await expect(page.locator('#workspaceCommandView .ws-cmd-map-inventory-badge')).toContainText([
      'Notes',
      'Schedules',
      'Sessions',
      'Linked Folders',
      'Files',
      'Systems'
    ]);
    await page.locator('#workspaceCommandView [data-cmd-map-window-close]').click();
    await expect(page.locator('#workspaceCommandView .ws-cmd-map-window')).toHaveCount(0);

    await page
      .locator('#workspaceCommandView .ws-cmd-map-belt-btn[data-cmd-map-window="objectives"]')
      .click();
    await expect(page.locator('#workspaceCommandView .ws-cmd-map-window')).toContainText(
      'Plan the work'
    );
    await page.locator('#workspaceCommandView [data-cmd-map-window-close]').click();

    await page
      .locator('#workspaceCommandView .ws-cmd-map-agent')
      .filter({ hasText: 'Research Analyst' })
      .click();
    const inspector = page.locator('#workspaceCommandView .ws-cmd-map-window');
    await expect(inspector).toContainText('Research Analyst');
    await expect(inspector).toContainText('Needs input');
    await expect(inspector).toContainText('Class');
    await expect(inspector).toContainText('Loadout');
    await expect(inspector).toContainText('Current Quest');
    await expect(inspector).toContainText('Command Menu');
    await expect(inspector).toContainText('Resolve Quest');
    await expect(inspector).toContainText('Start Session');
    await expect(inspector).toContainText('Configure Loadout');
    await expect(inspector).toContainText('Quests');
    await expect(inspector).toContainText('Skills');
    const inspectorGeometry = await inspector.evaluate(node => {
      const rect = node.getBoundingClientRect();
      const body = node.querySelector('.ws-cmd-map-window-body')?.getBoundingClientRect();
      const menu = node.querySelector('.ws-cmd-rpg-command-panel')?.getBoundingClientRect();
      return {
        bottom: Math.round(rect.bottom),
        bodyBottom: body ? Math.round(body.bottom) : 0,
        bodyTop: body ? Math.round(body.top) : 0,
        menuBottom: menu ? Math.round(menu.bottom) : 0,
        menuTop: menu ? Math.round(menu.top) : 0,
        top: Math.round(rect.top),
        viewportHeight: window.innerHeight
      };
    });
    expect(inspectorGeometry.top).toBeGreaterThanOrEqual(0);
    expect(inspectorGeometry.bottom).toBeLessThanOrEqual(inspectorGeometry.viewportHeight);
    expect(inspectorGeometry.menuTop).toBeGreaterThanOrEqual(inspectorGeometry.bodyTop);
    expect(inspectorGeometry.menuBottom).toBeLessThanOrEqual(inspectorGeometry.bodyBottom);
    await page.locator('#workspaceCommandView [data-cmd-map-window-close]').click();
    await expect(page.locator('#workspaceCommandView .ws-cmd-map-window')).toHaveCount(0);

    await page.setViewportSize({ width: 390, height: 844 });
    const mobileGeometry = await page
      .locator('#workspaceCommandView .ws-cmd-opmap')
      .evaluate(() => {
        return {
          pageWidth: document.documentElement.scrollWidth,
          viewportWidth: window.innerWidth
        };
      });
    expect(mobileGeometry.pageWidth).toBeLessThanOrEqual(mobileGeometry.viewportWidth);

    await page.locator('#workspaceCommandView [data-cmd-view-mode="details"]').click();
    await expect(page.locator('#workspaceCommandView .ws-cmd-deck')).toBeVisible();
  });
});

test.describe('Task Output Contracts', () => {
  test('opens append-to-CSV contract editor from task details', async ({ page, request }) => {
    let workspaceId = '';
    const workspaceResp = await request.post('/api/orchestration/workspace', {
      data: {
        name: `Playwright Output Contract ${Date.now()}`,
        description: 'Temporary workspace for output contract smoke coverage'
      }
    });
    expect(workspaceResp.ok()).toBeTruthy();
    const workspaceData = await workspaceResp.json();
    workspaceId = workspaceData.workspace_id;

    try {
      const taskResp = await request.post('/api/orchestration/tasks', {
        data: {
          workspace_id: workspaceId,
          description: 'Track NYC pollen daily',
          to: 'Ori',
          result_storage: {
            enabled: true,
            file_path: `/tmp/ori-output-contract-${workspaceId}.csv`,
            format: 'csv',
            write_mode: 'append'
          },
          output_contract: {
            source: 'manual',
            columns: [
              { name: 'date', type: 'date', required: true },
              { name: 'location', type: 'string', required: true },
              { name: 'pollen_count', type: 'number', required: true }
            ]
          }
        }
      });
      expect(taskResp.ok()).toBeTruthy();
      const taskData = await taskResp.json();
      const taskId = taskData.task?.id;
      expect(taskId).toBeTruthy();

      await page.goto(`/workspaces/${workspaceId}/task/${taskId}`);
      await expect(page.locator('#workspace-task-automation-storage')).toContainText(
        'Storage destination'
      );
      await expect(page.locator('#workspace-task-automation-columns')).toContainText(
        'date, location, pollen_count'
      );

      await page.locator('.workspace-task-advanced-summary').click();
      await expect(
        page.locator(
          '#workspace-task-automation-storage [data-action="open-automation-storage-modal"]'
        )
      ).toBeVisible();
      await page
        .locator('#workspace-task-automation-storage [data-action="open-automation-storage-modal"]')
        .click();
      await expect(page.locator('#taskModalOutputContractSection')).toBeVisible();
      await expect(page.locator('#taskModalAutoSaveWriteMode')).toHaveValue('append');
      await expect(
        page.locator('#taskModalOutputContractRows [data-output-contract-name]').first()
      ).toHaveValue('date');
    } finally {
      if (workspaceId) {
        await request.delete(`/api/orchestration/workspace?id=${workspaceId}`);
      }
    }
  });

  test('shows storage destination immediately after enabling CSV storage', async ({
    page,
    request
  }) => {
    let workspaceId = '';
    const workspaceResp = await request.post('/api/orchestration/workspace', {
      data: {
        name: `Playwright CSV Storage ${Date.now()}`,
        description: 'Temporary workspace for CSV storage smoke coverage'
      }
    });
    expect(workspaceResp.ok()).toBeTruthy();
    const workspaceData = await workspaceResp.json();
    workspaceId = workspaceData.workspace_id;

    await page.route('**/api/orchestration/tasks/output-spec/suggest', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          output_contract: {
            source: 'ai_suggested',
            columns: [
              { name: 'date', type: 'date', required: true },
              { name: 'summary', type: 'string', required: true }
            ]
          },
          reasoning: 'Use one row per scheduled run.'
        })
      });
    });

    try {
      const taskResp = await request.post('/api/orchestration/tasks', {
        data: {
          workspace_id: workspaceId,
          description: 'Summarize the daily operations report',
          to: 'Ori',
          schedule_enabled: true,
          schedule_name: 'Daily report',
          schedule: {
            type: 'daily',
            time: '09:00'
          }
        }
      });
      expect(taskResp.ok()).toBeTruthy();
      const taskData = await taskResp.json();
      const taskId = taskData.task?.id;
      expect(taskId).toBeTruthy();

      await page.goto(`/workspaces/${workspaceId}/task/${taskId}`);
      await page.locator('.workspace-task-advanced-summary').click();
      await expect(page.locator('#workspace-task-automation-columns')).toContainText(
        'Save each run of this task to a dataset'
      );

      await page
        .locator('#workspace-task-automation-columns [data-action="toggle-csv-storage"]')
        .check();

      await expect(page.locator('#workspace-task-automation-storage')).toContainText(
        'Storage destination'
      );
      await expect(page.locator('#workspace-task-automation-storage')).toContainText('Custom path');
      await expect(page.locator('#workspace-task-automation-columns')).toContainText(
        'What each run returns'
      );
      await expect(page.locator('#workspace-task-automation-columns')).toContainText('date');
    } finally {
      if (workspaceId) {
        await request.delete(`/api/orchestration/workspace?id=${workspaceId}`);
      }
    }
  });
});

test.describe('Workspace File Folders', () => {
  test('creates a folder, uploads into it, browses it, and moves the file', async ({
    page,
    request
  }) => {
    test.setTimeout(60000);

    let workspaceId = '';
    const workspaceResp = await request.post('/api/orchestration/workspace', {
      data: {
        name: `Playwright File Folders ${Date.now()}`,
        description: 'Temporary workspace for workspace file folder smoke coverage'
      }
    });
    expect(workspaceResp.ok()).toBeTruthy();
    const workspaceData = await workspaceResp.json();
    workspaceId = workspaceData.workspace_id;

    try {
      await page.addInitScript(id => {
        window.sessionStorage.setItem(`workspace-detail-entry-agent-prompt-dismissed:${id}`, '1');
      }, workspaceId);
      await page.goto(`/workspaces/${workspaceId}`);
      await expect(page.locator('#workspaceCommandView .ws-cmd-files-panel')).toBeVisible();
      await page.waitForFunction(() =>
        Boolean((window as any).workspaceDetail?.fileModalManager?.fileModalElements?.modal)
      );

      await page
        .locator('#workspaceCommandView .ws-cmd-files-panel [data-cmd-primary-section="files"]')
        .click();
      await expect(page.locator('#hubAddFileModal')).toBeVisible();

      page.once('dialog', async dialog => {
        expect(dialog.type()).toBe('prompt');
        await dialog.accept('research');
      });
      await page.locator('#hubCreateUploadFolderBtn').click();
      await expect(page.locator('#hubFileFolderSelect')).toHaveValue('research');

      await page.locator('#hubFileInput').setInputFiles({
        name: 'folder-smoke-report.txt',
        mimeType: 'text/plain',
        buffer: Buffer.from('workspace folder smoke test')
      });
      await expect(page.locator('#hubSelectedFilesPreview')).toBeVisible();

      const uploadResponse = page.waitForResponse(
        response =>
          response.url().includes(`/api/workspaces/${workspaceId}/files`) &&
          response.request().method() === 'POST'
      );
      await page.locator('#hubAddFileSubmitBtn').click();
      expect((await uploadResponse).ok()).toBeTruthy();
      await expect(page.locator('#hubAddFileModal.show')).toHaveCount(0);
      await expect(page.locator('#workspaceCommandView .ws-cmd-files-panel')).toContainText(
        'folder-smoke-report.txt'
      );

      await page
        .locator('#workspaceCommandView .ws-cmd-files-panel [data-cmd-open-section="files"]')
        .first()
        .click();
      const explorer = page.locator('#workspace-directory-explorer-modal');
      await expect(explorer).toBeVisible();
      await expect(
        explorer.locator('.workspace-directory-tree-main', { hasText: 'research' })
      ).toBeVisible();

      await expect(explorer.locator('.workspace-directory-preview-code')).toContainText(
        'workspace folder smoke test'
      );

      page.once('dialog', async dialog => {
        expect(dialog.type()).toBe('prompt');
        await dialog.accept('archive');
      });
      await explorer.locator('[data-action="move-workspace-file"]').click();
      await expect(
        explorer.locator('.workspace-directory-tree-main', { hasText: 'archive' })
      ).toBeVisible();
      await expect(explorer.locator('.workspace-directory-preview-subtitle')).toContainText(
        'archive/'
      );

      const treeResp = await request.get(`/api/workspaces/${workspaceId}/files/tree`);
      expect(treeResp.ok()).toBeTruthy();
      const treeData = await treeResp.json();
      const movedFile = (treeData.files || []).find(
        (item: any) =>
          item.relative_path?.includes('archive/') &&
          item.relative_path?.endsWith('folder-smoke-report.txt')
      );
      expect(movedFile).toBeTruthy();

      let revealPayload: any = null;
      await page.route(`**/api/workspaces/${workspaceId}/files/reveal`, async route => {
        revealPayload = route.request().postDataJSON();
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ message: 'ok' })
        });
      });
      await explorer.locator('.workspace-directory-tree-main', { hasText: 'archive' }).click();
      await expect(explorer.getByRole('button', { name: 'Open in File Manager' })).toBeVisible();
      await explorer.getByRole('button', { name: 'Open in File Manager' }).click();
      await expect.poll(() => revealPayload?.relative_path).toBe('archive');
    } finally {
      if (workspaceId) {
        await request.delete(`/api/orchestration/workspace?id=${workspaceId}`);
      }
    }
  });
});

test.describe('Floating Workspace Assistant', () => {
  async function createTemporaryWorkspace(request, namePrefix: string) {
    const workspaceResp = await request.post('/api/orchestration/workspace', {
      data: {
        name: `${namePrefix} ${Date.now()}`,
        description: 'Temporary workspace for floating assistant smoke coverage'
      }
    });
    expect(workspaceResp.ok()).toBeTruthy();
    const workspaceData = await workspaceResp.json();
    return workspaceData.workspace_id as string;
  }

  async function suppressOnboarding(page) {
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          needs_onboarding: false,
          current_step: 3,
          completed: true,
          skipped: false,
          steps_completed: ['names', 'model', 'personalization'],
          user_name: 'Playwright',
          assistant_name: 'Ori'
        })
      });
    });
  }

  async function suppressEntryAgentPrompt(page, workspaceId: string) {
    await page.addInitScript(id => {
      window.sessionStorage.setItem(`workspace-detail-entry-agent-prompt-dismissed:${id}`, '1');
    }, workspaceId);
  }

  async function gotoWorkspaceCommand(page, workspaceId: string) {
    await suppressOnboarding(page);
    await suppressEntryAgentPrompt(page, workspaceId);
    await page.goto(`/workspaces/${workspaceId}`);
    await expect(page.locator('#workspaceCommandView')).toBeVisible();
  }

  test('replaces the workspace-detail inline bar with a full floating assistant panel', async ({
    page,
    request
  }) => {
    let workspaceId = '';
    workspaceId = await createTemporaryWorkspace(request, 'Playwright Floating Assistant');

    try {
      await gotoWorkspaceCommand(page, workspaceId);

      await expect(page.locator('#homeAssistantCard.modern-card')).toHaveCount(0);
      await expect(page.locator('#hubSupportChatLauncher')).toBeVisible();
      await expect(page.locator('#hubSupportChatPanel')).toBeHidden();

      await page.locator('#hubSupportChatLauncher').click();
      const panel = page.locator('#hubSupportChatPanel');
      await expect(panel).toBeVisible();
      await expect(panel.getByRole('button', { name: 'Task', exact: true })).toBeVisible();
      await expect(panel.getByRole('button', { name: 'Ask', exact: true })).toBeVisible();
      await expect(panel.getByRole('button', { name: 'Note', exact: true })).toBeVisible();
      await expect(panel.locator('#homeAssistantQuickPlan')).toBeVisible();
      await expect(panel.locator('#homeAssistantQuickTasks')).toBeVisible();
      await expect(panel.locator('#homeAssistantQuickNotes')).toBeVisible();
      await expect(panel.locator('#homeAssistantQuickReview')).toBeVisible();
    } finally {
      if (workspaceId) {
        await request.delete(`/api/orchestration/workspace?id=${workspaceId}`);
      }
    }
  });

  test('creates a workspace task from the floating assistant task mode', async ({
    page,
    request
  }) => {
    test.setTimeout(45000);

    let workspaceId = '';
    workspaceId = await createTemporaryWorkspace(request, 'Playwright Floating Task');
    const taskTitle = `Write floating assistant smoke task ${Date.now()}`;

    try {
      await gotoWorkspaceCommand(page, workspaceId);
      await page.waitForFunction(() => Boolean((window as any).workspaceDetail));

      await page.locator('#hubSupportChatLauncher').click();
      await page.locator('#homeAssistantInput').fill(taskTitle);
      await page.locator('#homeAssistantSendBtn').click();

      await expect(page.locator('#workspace-detail-task-confirm-modal')).toBeVisible();
      await page.locator('#workspace-detail-task-confirm-confirm').click();
      await expect(page.locator('#homeAssistantRoutingSummary')).toContainText('Task Created');
      await expect(page.locator('#homeAssistantConversation')).toContainText('Created a task');
    } finally {
      if (workspaceId) {
        await request.delete(`/api/orchestration/workspace?id=${workspaceId}`);
      }
    }
  });

  test('saves a workspace note from the floating assistant note mode', async ({
    page,
    request
  }) => {
    test.setTimeout(45000);

    let workspaceId = '';
    workspaceId = await createTemporaryWorkspace(request, 'Playwright Floating Note');
    const noteText = `Floating assistant note smoke ${Date.now()}`;

    try {
      await gotoWorkspaceCommand(page, workspaceId);
      await page.waitForFunction(() => Boolean((window as any).workspaceDetail));

      await page.locator('#hubSupportChatLauncher').click();
      await page.locator('#hubSupportChatPanel').getByRole('button', { name: 'Note' }).click();
      await page.locator('#homeAssistantInput').fill(noteText);
      await page.locator('#homeAssistantSendBtn').click();

      await expect(page.locator('#homeAssistantRoutingSummary')).toContainText('Note Created');
      await expect(page.locator('#homeAssistantConversation')).toContainText('Created a note');
    } finally {
      if (workspaceId) {
        await request.delete(`/api/orchestration/workspace?id=${workspaceId}`);
      }
    }
  });

  test('runs preserved quick actions from the floating assistant', async ({ page, request }) => {
    test.setTimeout(45000);

    let workspaceId = '';
    workspaceId = await createTemporaryWorkspace(request, 'Playwright Floating Quick Actions');

    try {
      await gotoWorkspaceCommand(page, workspaceId);

      await page.locator('#hubSupportChatLauncher').click();
      const panel = page.locator('#hubSupportChatPanel');
      await expect(panel).toBeVisible();

      await panel.locator('#homeAssistantQuickNotes').click();
      await expect(page.locator('#noteEditorModal')).toBeVisible();
      await expect(page.locator('#noteNameInput')).toHaveValue('Workspace Description');
      await expect(page.locator('#noteContentInput')).toHaveValue(/## Description/);

      await gotoWorkspaceCommand(page, workspaceId);
      await page.locator('#hubSupportChatLauncher').click();
      await page
        .locator('#hubSupportChatPanel')
        .getByRole('button', { name: 'Note', exact: true })
        .click();

      for (const selector of [
        '#homeAssistantQuickPlan',
        '#homeAssistantQuickTasks',
        '#homeAssistantQuickReview'
      ]) {
        const button = page.locator('#hubSupportChatPanel').locator(selector);
        const prompt = await button.getAttribute('data-home-prompt');
        expect(prompt).toBeTruthy();
        await button.click();
        await expect(page.locator('#homeAssistantInput')).toHaveValue(prompt || '');
        await expect(page.locator('#homeAssistantSendBtn')).toBeEnabled();
      }
    } finally {
      if (workspaceId) {
        await request.delete(`/api/orchestration/workspace?id=${workspaceId}`);
      }
    }
  });

  test('opens full chat for the active workspace assistant session', async ({ page, request }) => {
    test.setTimeout(45000);

    let workspaceId = '';
    workspaceId = await createTemporaryWorkspace(request, 'Playwright Floating Open Chat');
    let chatPayload: any = null;
    let chatSessionId = '';

    try {
      await suppressOnboarding(page);
      await page.route('**/api/chat', async route => {
        chatPayload = route.request().postDataJSON();
        chatSessionId = route.request().headers()['x-session-id'] || '';
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            response: 'Workspace manager inline response from smoke test.'
          })
        });
      });

      await gotoWorkspaceCommand(page, workspaceId);
      await page.waitForFunction(() => Boolean((window as any).workspaceDetail));

      await page.locator('#hubSupportChatLauncher').click();
      const panel = page.locator('#hubSupportChatPanel');
      await panel.getByRole('button', { name: 'Ask', exact: true }).click();
      await page.locator('#homeAssistantInput').fill('What should happen next in this workspace?');
      await page.locator('#homeAssistantSendBtn').click();

      await expect(page.locator('#homeAssistantConversation')).toContainText(
        'Workspace manager inline response'
      );
      expect(chatPayload?.route_context?.workspace_id).toBe(workspaceId);
      expect(chatPayload?.route_context?.surface).toBe('workspace_detail');
      expect(chatSessionId).toBeTruthy();

      await page
        .locator('#homeAssistantActions')
        .getByRole('button', { name: 'Open Chat' })
        .click();
      await expect(page.locator('#chatPanel')).toHaveAttribute('aria-hidden', 'false');
      const activeSessionId = await page.evaluate(() => {
        const manager = (window as any).sessionManager;
        return String(
          manager?.getActiveSessionId?.() ||
            manager?.activeSessionId ||
            manager?.currentSessionId ||
            ''
        );
      });
      expect(activeSessionId).toBe(chatSessionId);
    } finally {
      if (workspaceId) {
        await request.delete(`/api/orchestration/workspace?id=${workspaceId}`);
      }
    }
  });

  test('keeps home dashboard and workspace canvas inline assistant bars', async ({
    page,
    request
  }) => {
    let workspaceId = '';
    workspaceId = await createTemporaryWorkspace(request, 'Playwright Inline Assistant Regression');

    try {
      await suppressOnboarding(page);
      await page.goto('/');
      await expect(page.locator('#homeAssistantCard.modern-card')).toBeVisible();
      await expect(page.locator('#homeAssistantInput')).toBeVisible();
      await expect(page.locator('#hubSupportChat')).toHaveCount(0);

      await page.goto(`/workspaces/${workspaceId}/canvas`);
      await expect(page.locator('#homeAssistantCard.modern-card')).toBeVisible();
      await expect(page.locator('#homeAssistantInput')).toHaveAttribute(
        'placeholder',
        /from this canvas/
      );
      await expect(page.locator('#homeAssistantWorkspaceModeSwitch')).toBeVisible();
      await expect(page.locator('#hubSupportChat')).toHaveCount(0);
    } finally {
      if (workspaceId) {
        await request.delete(`/api/orchestration/workspace?id=${workspaceId}`);
      }
    }
  });
});

test.describe('Home Workspace Routing', () => {
  async function installWorkspaceAssistantMocks(
    page,
    options: {
      workspaceId: string;
      entryAgentName: string;
      onChat?: () => void;
    }
  ) {
    await page.route(`**/api/workspaces/${options.workspaceId}`, async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          id: options.workspaceId,
          entry_agent_name: options.entryAgentName
        })
      });
    });
    await page.route('**/api/chat', async route => {
      options.onChat?.();
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          response: 'Workspace manager is ready.'
        })
      });
    });
  }

  test('asks the user to choose when workspace routing is ambiguous', async ({ page }) => {
    await page.route('**/api/home-assistant/route', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          intent: 'general_task',
          intent_label: 'general task',
          routing_policy: 'assistant_only',
          context_mode: 'workspace',
          handoff_policy: 'assistant',
          score: 0,
          requires_creation: false,
          workspace_recommended: true,
          route_mode: 'workspace_task',
          target_surface: 'workspace',
          suggested_agent_name: 'Task Assistant',
          suggested_agent_type: 'general',
          workspace_resolution: {
            state: 'ambiguous',
            candidates: [
              {
                id: 'ws-alpha',
                name: 'Launch Alpha',
                score: 8,
                reasons: ['matched workspace goal']
              },
              { id: 'ws-beta', name: 'Launch Beta', score: 7, reasons: ['matched workspace goal'] }
            ]
          }
        })
      });
    });

    await page.goto('/');
    await page.locator('#homeAssistantInput').fill('ship the launch plan');
    await page.locator('#homeAssistantSendBtn').click();

    await expect(page.locator('#homeAssistantRoutingSummary')).toContainText('Choose Workspace');
    await expect(page.getByText('Launch Alpha', { exact: true })).toBeVisible();
    await expect(page.getByText('Launch Beta', { exact: true })).toBeVisible();
    await expect(page.getByText('Create New Workspace', { exact: true })).toBeVisible();
  });

  test('offers workspace creation when no existing workspace fits', async ({ page }) => {
    await page.route('**/api/home-assistant/route', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          intent: 'general_task',
          intent_label: 'general task',
          routing_policy: 'assistant_only',
          context_mode: 'workspace',
          handoff_policy: 'assistant',
          matched_agent: 'Ori',
          score: 4,
          requires_creation: false,
          workspace_recommended: true,
          route_mode: 'workspace_task',
          target_surface: 'workspace',
          suggested_agent_name: 'Task Assistant',
          suggested_agent_type: 'general',
          workspace_resolution: {
            state: 'no_fit',
            candidates: []
          }
        })
      });
    });

    await page.goto('/');
    await page.locator('#homeAssistantInput').fill('build a robotics dashboard from scratch');
    await page.locator('#homeAssistantSendBtn').click();

    await expect(page.locator('#homeAssistantRoutingSummary')).toContainText('Workspace Needed');
    const actions = page.locator('#homeAssistantActions');
    await expect(actions.getByText('Create Workspace', { exact: true })).toBeVisible();
    await expect(actions.getByText('Continue in Chat', { exact: true })).toBeVisible();
  });

  test('hands a confident workspace match to the workspace assistant', async ({ page }) => {
    let chatCalls = 0;
    await installWorkspaceAssistantMocks(page, {
      workspaceId: 'ws-cabinet',
      entryAgentName: 'Cabinet Manager',
      onChat: () => {
        chatCalls += 1;
      }
    });
    await page.route('**/api/home-assistant/route', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          intent: 'general_task',
          intent_label: 'general task',
          routing_policy: 'assistant_only',
          context_mode: 'workspace',
          handoff_policy: 'assistant',
          matched_agent: 'Cabinet Manager',
          score: 8,
          requires_creation: false,
          workspace_recommended: true,
          route_mode: 'workspace_task',
          target_surface: 'workspace',
          suggested_agent_name: 'Task Assistant',
          suggested_agent_type: 'general',
          workspace_resolution: {
            state: 'confident',
            selected_workspace_id: 'ws-cabinet',
            selected_workspace_name: 'Cabinet',
            confidence: 0.99,
            reasons: ['matched workspace name'],
            candidates: [
              { id: 'ws-cabinet', name: 'Cabinet', score: 12, reasons: ['matched workspace name'] }
            ]
          }
        })
      });
    });

    await page.goto('/');
    await page.evaluate(() => {
      (window as any).sessionManager = {
        sessions: [],
        async createSessionWithAgentInFolder(agentName: string, folderId: string) {
          return { id: 'sess-cabinet', agent_name: agentName, folder_id: folderId };
        }
      };
    });
    await page.locator('#homeAssistantInput').fill('build the cabinet roadmap');
    await page.locator('#homeAssistantSendBtn').click();

    await expect.poll(() => chatCalls).toBe(1);
    await expect(page.locator('#homeAssistantConversation')).toContainText(
      'Workspace manager is ready.'
    );
    await expect(page.locator('#homeAssistantActions')).toContainText('Choose Another Workspace');
  });

  test('lets the user override a confident workspace match', async ({ page }) => {
    await page.route('**/api/workspaces/ws-cabinet', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          id: 'ws-cabinet',
          entry_agent_name: 'Cabinet Manager'
        })
      });
    });
    await page.route('**/api/workspaces/ws-ops', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          id: 'ws-ops',
          entry_agent_name: 'Ops Manager'
        })
      });
    });
    await page.route('**/api/chat', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          response: 'Workspace manager is ready.'
        })
      });
    });
    await page.route('**/api/home-assistant/route', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          intent: 'general_task',
          intent_label: 'general task',
          routing_policy: 'assistant_only',
          context_mode: 'workspace',
          handoff_policy: 'assistant',
          matched_agent: 'Cabinet Manager',
          score: 8,
          requires_creation: false,
          workspace_recommended: true,
          route_mode: 'workspace_task',
          target_surface: 'workspace',
          suggested_agent_name: 'Task Assistant',
          suggested_agent_type: 'general',
          workspace_resolution: {
            state: 'confident',
            selected_workspace_id: 'ws-cabinet',
            selected_workspace_name: 'Cabinet',
            confidence: 0.99,
            reasons: ['matched workspace name'],
            candidates: [
              { id: 'ws-cabinet', name: 'Cabinet', score: 12, reasons: ['matched workspace name'] },
              { id: 'ws-ops', name: 'Ops Hub', score: 9, reasons: ['matched workspace goal'] }
            ]
          }
        })
      });
    });

    await page.goto('/');
    await page.evaluate(() => {
      (window as any).__handoffWorkspaceIds = [];
      (window as any).sessionManager = {
        sessions: [],
        async createSessionWithAgentInFolder(agentName: string, folderId: string) {
          (window as any).__handoffWorkspaceIds.push(folderId);
          return { id: `sess-${folderId}`, agent_name: agentName, folder_id: folderId };
        }
      };
    });
    await page.locator('#homeAssistantInput').fill('build the cabinet roadmap');
    await page.locator('#homeAssistantSendBtn').click();

    await expect
      .poll(() => page.evaluate(() => (window as any).__handoffWorkspaceIds))
      .toEqual(['ws-cabinet']);
    await page
      .locator('#homeAssistantActions')
      .getByText('Choose Another Workspace', { exact: true })
      .click();
    await expect(page.locator('#homeAssistantRoutingSummary')).toContainText('Choose Workspace');
    await page.locator('#homeAssistantActions').getByText('Ops Hub', { exact: true }).click();
    await expect
      .poll(() => page.evaluate(() => (window as any).__handoffWorkspaceIds))
      .toEqual(['ws-cabinet', 'ws-ops']);
  });

  test('shows a repair-required state when the matched workspace has no runnable entry agent', async ({
    page
  }) => {
    await page.route('**/api/home-assistant/route', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          intent: 'general_task',
          intent_label: 'general task',
          routing_policy: 'assistant_only',
          context_mode: 'workspace',
          handoff_policy: 'assistant',
          score: 0,
          requires_creation: false,
          workspace_recommended: true,
          route_mode: 'workspace_task',
          target_surface: 'workspace',
          suggested_agent_name: 'Task Assistant',
          suggested_agent_type: 'general',
          workspace_resolution: {
            state: 'needs_repair',
            selected_workspace_id: 'ws-broken',
            selected_workspace_name: 'Broken Ops',
            repair_reason: 'workspace has no entry agent',
            candidates: [
              {
                id: 'ws-broken',
                name: 'Broken Ops',
                score: 12,
                reasons: ['matched workspace name']
              }
            ]
          }
        })
      });
    });

    await page.goto('/');
    await page.locator('#homeAssistantInput').fill('build the broken ops roadmap');
    await page.locator('#homeAssistantSendBtn').click();

    await expect(page.locator('#homeAssistantRoutingSummary')).toContainText(
      'Entry Agent Required'
    );
    await expect(
      page.locator('#homeAssistantActions').getByText('Open Workspace Setup', { exact: true })
    ).toBeVisible();
  });

  test('resumes a created workspace prompt once the new workspace is ready', async ({ page }) => {
    let chatCalls = 0;
    await installWorkspaceAssistantMocks(page, {
      workspaceId: 'ws-new',
      entryAgentName: 'New Workspace Manager',
      onChat: () => {
        chatCalls += 1;
      }
    });
    await page.route('**/api/home-assistant/route', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          intent: 'general_task',
          intent_label: 'general task',
          routing_policy: 'assistant_only',
          context_mode: 'workspace',
          handoff_policy: 'assistant',
          matched_agent: 'Ori',
          score: 4,
          requires_creation: false,
          workspace_recommended: true,
          route_mode: 'workspace_task',
          target_surface: 'workspace',
          suggested_agent_name: 'Task Assistant',
          suggested_agent_type: 'general',
          workspace_resolution: {
            state: 'no_fit',
            candidates: []
          }
        })
      });
    });

    await page.goto('/');
    await page.evaluate(() => {
      (window as any).sessionManager = {
        sessions: [],
        async createSessionWithAgentInFolder(agentName: string, folderId: string) {
          return { id: 'sess-new', agent_name: agentName, folder_id: folderId };
        }
      };
    });
    await page.locator('#homeAssistantInput').fill('build a robotics dashboard from scratch');
    await page.locator('#homeAssistantSendBtn').click();
    await page
      .locator('#homeAssistantActions')
      .getByText('Create Workspace', { exact: true })
      .click();

    await page.evaluate(() => {
      window.dispatchEvent(
        new CustomEvent('ori:workspace-created', {
          detail: { workspaceId: 'ws-new', workspaceName: 'New Workspace' }
        })
      );
      return (window as any).OriAskRouting.refreshWorkspaceIdentity({
        workspace_id: 'ws-new',
        page_path: '/workspaces/ws-new',
        surface: 'workspace_detail',
        origin: 'ask_ori'
      });
    });

    await expect.poll(() => chatCalls).toBe(1);
  });

  test('waits for repair before resuming a preserved workspace prompt', async ({ page }) => {
    let workspaceReady = false;
    let chatCalls = 0;
    await page.route('**/api/workspaces/ws-broken', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(
          workspaceReady
            ? {
                id: 'ws-broken',
                entry_agent_name: 'Broken Ops Manager'
              }
            : {
                id: 'ws-broken'
              }
        )
      });
    });
    await page.route('**/api/chat', async route => {
      chatCalls += 1;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ response: 'Workspace repaired and ready.' })
      });
    });

    await page.goto('/');
    await page.evaluate(() => {
      window.sessionStorage.setItem(
        'ori.homeAssistant.pendingWorkspacePrompt',
        JSON.stringify({
          prompt: 'finish the broken ops roadmap',
          routeContext: {
            surface: 'dashboard',
            page_path: '/',
            workspace_id: '',
            session_id: '',
            origin: 'ask_ori'
          },
          expectedWorkspaceId: 'ws-broken',
          intentKey: 'general_task',
          source: 'repair',
          createdAt: Date.now()
        })
      );
      (window as any).sessionManager = {
        sessions: [],
        async createSessionWithAgentInFolder(agentName: string, folderId: string) {
          return { id: 'sess-repaired', agent_name: agentName, folder_id: folderId };
        }
      };
    });

    await page.evaluate(() =>
      (window as any).OriAskRouting.refreshWorkspaceIdentity({
        workspace_id: 'ws-broken',
        page_path: '/workspaces/ws-broken',
        surface: 'workspace_detail',
        origin: 'ask_ori'
      })
    );
    expect(chatCalls).toBe(0);

    workspaceReady = true;
    await page.evaluate(() =>
      (window as any).OriAskRouting.refreshWorkspaceIdentity({
        workspace_id: 'ws-broken',
        page_path: '/workspaces/ws-broken',
        surface: 'workspace_detail',
        origin: 'ask_ori'
      })
    );
    await expect.poll(() => chatCalls).toBe(1);
  });
});

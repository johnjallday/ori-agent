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

    // Wait for app initialization
    await page.waitForLoadState('networkidle');

    // Filter out expected errors (add patterns as needed)
    const unexpectedErrors = errors.filter(e =>
      !e.includes('favicon') &&
      !e.includes('404')
    );

    expect(unexpectedErrors).toHaveLength(0);
  });

  test('theme toggle works', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

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

  test('sidebar navigation is accessible', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // Check sidebar exists
    const sidebar = page.locator('.sidebar, [class*="sidebar"]').first();
    await expect(sidebar).toBeVisible();
  });
});

test.describe('Onboarding', () => {
  async function installBaseOnboardingRoutes(page) {
    await page.route('**/api/onboarding/status', async (route) => {
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
    await page.route('**/api/onboarding/names', async (route) => {
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
    await page.route('**/api/onboarding/step', async (route) => {
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
    await expect(page.getByText('Storage locations can be changed later in Settings when you need them.')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Set Up Later' })).toBeVisible();
  });

  test('auto-selects a recommended model before continuing', async ({ page }) => {
    await installBaseOnboardingRoutes(page);
    await page.route('**/api/providers', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          providers: [
            { name: 'ollama', display_name: 'Ollama', available: true }
          ]
        })
      });
    });
    await page.route('**/api/settings/available-models?provider=ollama', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          available: true,
          model_options: [
            { id: 'llama-small', label: 'Llama Small', description: 'Fast', recommended: false },
            { id: 'llama-balanced', label: 'Llama Balanced', description: 'Recommended', recommended: true }
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
    await page.route('**/api/providers', async (route) => {
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
  test('makes workspace creation the primary next step when no workspaces exist', async ({ page, request }) => {
    const response = await request.get('/api/workspaces');
    const data = await response.json();
    test.skip((data.workspaces || []).length !== 0, 'requires an empty workspace store');

    await page.goto('/');
    await expect(page.locator('#homeFirstRunHero')).toBeVisible();
    await expect(page.locator('#homeFirstRunStart')).toHaveText('Create Workspace');
    await expect(page.locator('#homeAssistantInput')).toHaveAttribute('placeholder', 'Plan a product launch…');
    await expect(page.getByText('Create a workspace for a software project', { exact: true })).toBeVisible();
  });
});

test.describe('Agent Management', () => {
  test('can open create agent modal', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // Look for create agent button (adjust selector based on your UI)
    const createBtn = page.locator('button:has-text("Create"), [data-bs-target="#addAgentModal"]').first();

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
    await page.waitForLoadState('networkidle');

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
    await page.route('**/api/folder-picker/select-path', async (route) => {
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

    await page.route('**/api/workspaces/import/check*', async (route) => {
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

    await page.route('**/api/workspaces/import/duplicate-action', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true })
      });
    });

    let importAttemptCount = 0;
    await page.route('**/api/workspaces/import', async (route) => {
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
    await page.waitForLoadState('networkidle');
    const modal = page.locator('#addFolderModal');
    if (!await modal.isVisible()) {
      await page.locator('#launcherCreateWorkspaceBtn').click();
    }
    await expect(modal).toBeVisible();

    const importToggle = page.locator('#folderImportToggle');
    if (!await importToggle.isChecked()) {
      await importToggle.click();
    }
    await expect(importToggle).toBeChecked();
    await expect(page.locator('#folderImportSection')).toBeVisible();

    await page.locator('#folderImportBrowseBtn').click();
    await expect(page.locator('#folderImportPathInput')).toHaveValue('/tmp/demo-project');
    await expect(page.locator('#folderImportDuplicateWarning')).toBeVisible();

    await page.locator('#folderImportProceedDuplicateBtn').click();
    await page.locator('#createFolderBtn').click();

    await expect(page.locator('#addFolderModal.show')).toHaveCount(0);
    expect(importAttemptCount).toBeGreaterThan(0);
  });

  test('import controls are keyboard and mobile friendly', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto('/workspaces');
    await page.waitForLoadState('networkidle');

    const modal = page.locator('#addFolderModal');
    if (!await modal.isVisible()) {
      await page.locator('#launcherCreateWorkspaceBtn').click();
    }

    const importToggle = page.locator('#folderImportToggle');
    if (await importToggle.isChecked()) {
      await importToggle.focus();
      await page.keyboard.press('Space');
    }
    await importToggle.focus();
    await page.keyboard.press('Space');
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
    const response = await request.get('/api/health');
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
      await expect(page.locator('#workspace-task-automation-storage')).toContainText('Storage destination');
      await expect(page.locator('#workspace-task-automation-columns')).toContainText('date, location, pollen_count');

      await page.getByText('Advanced settings', { exact: true }).click();
      await expect(page.locator('#taskModalOutputContractSection')).toBeVisible();
      await expect(page.locator('#taskModalAutoSaveWriteMode')).toHaveValue('append');
      await expect(page.locator('#taskModalOutputContractRows [data-output-contract-name]').first()).toHaveValue('date');
    } finally {
      if (workspaceId) {
        await request.delete(`/api/orchestration/workspace?id=${workspaceId}`);
      }
    }
  });

  test('shows storage destination immediately after enabling CSV storage', async ({ page, request }) => {
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

    await page.route('**/api/orchestration/tasks/output-spec/suggest', async (route) => {
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
      await expect(page.getByText('Store each run of this task to CSV')).toBeVisible();

      await page.locator('[data-action="toggle-csv-storage"]').check();

      await expect(page.locator('#workspace-task-automation-storage')).toContainText('Storage destination');
      await expect(page.locator('#workspace-task-automation-storage')).toContainText('Custom path');
      await expect(page.locator('#workspace-task-automation-columns')).toContainText('Result format');
      await expect(page.locator('#workspace-task-automation-columns')).toContainText('date');
    } finally {
      if (workspaceId) {
        await request.delete(`/api/orchestration/workspace?id=${workspaceId}`);
      }
    }
  });
});

test.describe('Workspace File Folders', () => {
  test('creates a folder, uploads into it, browses it, and moves the file', async ({ page, request }) => {
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
      await page.goto(`/workspaces/${workspaceId}`);
      await expect(page.locator('#workspace-detail-files-panel')).toBeVisible();
      await page.waitForFunction(() =>
        Boolean((window as any).workspaceDetail?.fileModalManager?.fileModalElements?.modal)
      );
      const entryAgentTitle = page.locator('#workspace-detail-task-confirm-title', {
        hasText: 'Create an entry agent for this workspace?'
      });
      if (await entryAgentTitle.waitFor({ state: 'visible', timeout: 1500 }).then(() => true).catch(() => false)) {
        await page.waitForTimeout(150);
        await page.locator('#workspace-detail-task-confirm-cancel').click();
        await page.waitForFunction(() => {
          const modal = document.getElementById('workspace-detail-task-confirm-modal');
          return !modal || !modal.classList.contains('show');
        });
        await page.waitForFunction(() => {
          const modal = document.getElementById('workspace-detail-task-confirm-modal');
          const settled = !modal || window.getComputedStyle(modal).display === 'none';
          return settled && !(window as any).workspaceDetail?.pendingTaskConfirm;
        });
        await page.evaluate(() => window.Toast?.dismissAll?.());
      }

      await page.locator('#workspace-detail-add-file').click();
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

      const uploadResponse = page.waitForResponse(response =>
        response.url().includes(`/api/workspaces/${workspaceId}/files`) &&
        response.request().method() === 'POST'
      );
      await page.locator('#hubAddFileSubmitBtn').click();
      expect((await uploadResponse).ok()).toBeTruthy();
      await expect(page.locator('#hubAddFileModal.show')).toHaveCount(0);
      await expect(page.locator('#workspace-detail-files-list')).toContainText('folder-smoke-report.txt');

      await page.locator('#workspace-detail-files-panel .workspace-detail-panel-title').click();
      const explorer = page.locator('#workspace-directory-explorer-modal');
      await expect(explorer).toBeVisible();
      await expect(page.locator('#workspace-detail-files-panel')).not.toHaveClass(/is-expanded/);
      await expect(explorer.locator('.workspace-directory-tree-main', { hasText: 'research' })).toBeVisible();

      await expect(explorer.locator('.workspace-directory-preview-code')).toContainText('workspace folder smoke test');

      page.once('dialog', async dialog => {
        expect(dialog.type()).toBe('prompt');
        await dialog.accept('archive');
      });
      await explorer.locator('[data-action="move-workspace-file"]').click();
      await expect(explorer.locator('.workspace-directory-tree-main', { hasText: 'archive' })).toBeVisible();
      await expect(explorer.locator('.workspace-directory-preview-subtitle')).toContainText('archive/');

      const treeResp = await request.get(`/api/workspaces/${workspaceId}/files/tree`);
      expect(treeResp.ok()).toBeTruthy();
      const treeData = await treeResp.json();
      const movedFile = (treeData.files || []).find((item: any) =>
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
    await page.route('**/api/onboarding/status', async (route) => {
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

  async function dismissEntryAgentDialogIfPresent(page) {
    const entryAgentTitle = page.locator('#workspace-detail-task-confirm-title', {
      hasText: 'Create an entry agent for this workspace?'
    });
    if (await entryAgentTitle.waitFor({ state: 'visible', timeout: 1500 }).then(() => true).catch(() => false)) {
      await page.waitForTimeout(150);
      await page.locator('#workspace-detail-task-confirm-cancel').click();
      await page.waitForFunction(() => {
        const modal = document.getElementById('workspace-detail-task-confirm-modal');
        return !modal || !modal.classList.contains('show');
      });
      await page.waitForFunction(() => {
        const modal = document.getElementById('workspace-detail-task-confirm-modal');
        const settled = !modal || window.getComputedStyle(modal).display === 'none';
        return settled && !(window as any).workspaceDetail?.pendingTaskConfirm;
      });
      await page.evaluate(() => window.Toast?.dismissAll?.());
    }
  }

  test('replaces the workspace-detail inline bar with a full floating assistant panel', async ({ page, request }) => {
    let workspaceId = '';
    workspaceId = await createTemporaryWorkspace(request, 'Playwright Floating Assistant');

    try {
      await suppressOnboarding(page);
      await page.goto(`/workspaces/${workspaceId}`);
      await expect(page.locator('#workspace-detail-files-panel')).toBeVisible();
      await dismissEntryAgentDialogIfPresent(page);

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

  test('creates a workspace task from the floating assistant task mode', async ({ page, request }) => {
    test.setTimeout(45000);

    let workspaceId = '';
    workspaceId = await createTemporaryWorkspace(request, 'Playwright Floating Task');
    const taskTitle = `Write floating assistant smoke task ${Date.now()}`;

    try {
      await suppressOnboarding(page);
      await page.goto(`/workspaces/${workspaceId}`);
      await expect(page.locator('#workspace-detail-files-panel')).toBeVisible();
      await dismissEntryAgentDialogIfPresent(page);
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

  test('saves a workspace note from the floating assistant note mode', async ({ page, request }) => {
    test.setTimeout(45000);

    let workspaceId = '';
    workspaceId = await createTemporaryWorkspace(request, 'Playwright Floating Note');
    const noteText = `Floating assistant note smoke ${Date.now()}`;

    try {
      await suppressOnboarding(page);
      await page.goto(`/workspaces/${workspaceId}`);
      await expect(page.locator('#workspace-detail-files-panel')).toBeVisible();
      await dismissEntryAgentDialogIfPresent(page);
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

    const getWorkspaceNoteCount = async () => {
      const notesResp = await request.get(`/api/workspaces/${workspaceId}/notes`);
      expect(notesResp.ok()).toBeTruthy();
      const notesData = await notesResp.json();
      return (notesData.notes || []).length;
    };

    try {
      await suppressOnboarding(page);
      await page.goto(`/workspaces/${workspaceId}`);
      await expect(page.locator('#workspace-detail-files-panel')).toBeVisible();
      await dismissEntryAgentDialogIfPresent(page);

      await page.locator('#hubSupportChatLauncher').click();
      const panel = page.locator('#hubSupportChatPanel');
      await expect(panel).toBeVisible();

      await panel.locator('#homeAssistantQuickNotes').click();
      await expect(page.locator('#noteEditorModal')).toBeVisible();
      await expect(page.locator('#noteNameInput')).toHaveValue('Workspace Description');
      await expect(page.locator('#noteContentInput')).toHaveValue(/## Description/);

      await page.goto(`/workspaces/${workspaceId}`);
      await expect(page.locator('#workspace-detail-files-panel')).toBeVisible();
      await dismissEntryAgentDialogIfPresent(page);
      await page.locator('#hubSupportChatLauncher').click();
      await page.locator('#hubSupportChatPanel').getByRole('button', { name: 'Note', exact: true }).click();

      let expectedNotes = await getWorkspaceNoteCount();
      for (const selector of ['#homeAssistantQuickPlan', '#homeAssistantQuickTasks', '#homeAssistantQuickReview']) {
        await page.locator('#hubSupportChatPanel').locator(selector).click();
        expectedNotes += 1;
        await expect(page.locator('#homeAssistantRoutingSummary')).toContainText('Note Created');
        await expect.poll(getWorkspaceNoteCount).toBe(expectedNotes);
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
      await page.route('**/api/chat', async (route) => {
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

      await page.goto(`/workspaces/${workspaceId}`);
      await expect(page.locator('#workspace-detail-files-panel')).toBeVisible();
      await dismissEntryAgentDialogIfPresent(page);
      await page.waitForFunction(() => Boolean((window as any).workspaceDetail));

      await page.locator('#hubSupportChatLauncher').click();
      const panel = page.locator('#hubSupportChatPanel');
      await panel.getByRole('button', { name: 'Ask', exact: true }).click();
      await page.locator('#homeAssistantInput').fill('What should happen next in this workspace?');
      await page.locator('#homeAssistantSendBtn').click();

      await expect(page.locator('#homeAssistantConversation')).toContainText('Workspace manager inline response');
      expect(chatPayload?.route_context?.workspace_id).toBe(workspaceId);
      expect(chatPayload?.route_context?.surface).toBe('workspace_detail');
      expect(chatSessionId).toBeTruthy();

      await page.locator('#homeAssistantActions').getByRole('button', { name: 'Open Chat' }).click();
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

  test('keeps home dashboard and workspace canvas inline assistant bars', async ({ page, request }) => {
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
      await expect(page.locator('#homeAssistantInput')).toHaveAttribute('placeholder', /from this canvas/);
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
    await page.route(`**/api/workspaces/${options.workspaceId}`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          id: options.workspaceId,
          entry_agent_name: options.entryAgentName
        })
      });
    });
    await page.route('**/api/chat', async (route) => {
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
    await page.route('**/api/home-assistant/route', async (route) => {
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
              { id: 'ws-alpha', name: 'Launch Alpha', score: 8, reasons: ['matched workspace goal'] },
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
    await page.route('**/api/home-assistant/route', async (route) => {
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
      onChat: () => { chatCalls += 1; }
    });
    await page.route('**/api/home-assistant/route', async (route) => {
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
    await expect(page.locator('#homeAssistantConversation')).toContainText('Workspace manager is ready.');
    await expect(page.locator('#homeAssistantActions')).toContainText('Choose Another Workspace');
  });

  test('lets the user override a confident workspace match', async ({ page }) => {
    await page.route('**/api/workspaces/ws-cabinet', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          id: 'ws-cabinet',
          entry_agent_name: 'Cabinet Manager'
        })
      });
    });
    await page.route('**/api/workspaces/ws-ops', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          id: 'ws-ops',
          entry_agent_name: 'Ops Manager'
        })
      });
    });
    await page.route('**/api/chat', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          response: 'Workspace manager is ready.'
        })
      });
    });
    await page.route('**/api/home-assistant/route', async (route) => {
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

    await expect.poll(() => page.evaluate(() => (window as any).__handoffWorkspaceIds)).toEqual(['ws-cabinet']);
    await page.locator('#homeAssistantActions').getByText('Choose Another Workspace', { exact: true }).click();
    await expect(page.locator('#homeAssistantRoutingSummary')).toContainText('Choose Workspace');
    await page.locator('#homeAssistantActions').getByText('Ops Hub', { exact: true }).click();
    await expect.poll(() => page.evaluate(() => (window as any).__handoffWorkspaceIds)).toEqual(['ws-cabinet', 'ws-ops']);
  });

  test('shows a repair-required state when the matched workspace has no runnable entry agent', async ({ page }) => {
    await page.route('**/api/home-assistant/route', async (route) => {
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
              { id: 'ws-broken', name: 'Broken Ops', score: 12, reasons: ['matched workspace name'] }
            ]
          }
        })
      });
    });

    await page.goto('/');
    await page.locator('#homeAssistantInput').fill('build the broken ops roadmap');
    await page.locator('#homeAssistantSendBtn').click();

    await expect(page.locator('#homeAssistantRoutingSummary')).toContainText('Entry Agent Required');
    await expect(page.locator('#homeAssistantActions').getByText('Open Workspace Setup', { exact: true })).toBeVisible();
  });

  test('resumes a created workspace prompt once the new workspace is ready', async ({ page }) => {
    let chatCalls = 0;
    await installWorkspaceAssistantMocks(page, {
      workspaceId: 'ws-new',
      entryAgentName: 'New Workspace Manager',
      onChat: () => { chatCalls += 1; }
    });
    await page.route('**/api/home-assistant/route', async (route) => {
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
    await page.locator('#homeAssistantActions').getByText('Create Workspace', { exact: true }).click();

    await page.evaluate(() => {
      window.dispatchEvent(new CustomEvent('ori:workspace-created', {
        detail: { workspaceId: 'ws-new', workspaceName: 'New Workspace' }
      }));
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
    await page.route('**/api/workspaces/ws-broken', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(workspaceReady ? {
          id: 'ws-broken',
          entry_agent_name: 'Broken Ops Manager'
        } : {
          id: 'ws-broken'
        })
      });
    });
    await page.route('**/api/chat', async (route) => {
      chatCalls += 1;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ response: 'Workspace repaired and ready.' })
      });
    });

    await page.goto('/');
    await page.evaluate(() => {
      window.sessionStorage.setItem('ori.homeAssistant.pendingWorkspacePrompt', JSON.stringify({
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
      }));
      (window as any).sessionManager = {
        sessions: [],
        async createSessionWithAgentInFolder(agentName: string, folderId: string) {
          return { id: 'sess-repaired', agent_name: agentName, folder_id: folderId };
        }
      };
    });

    await page.evaluate(() => (window as any).OriAskRouting.refreshWorkspaceIdentity({
      workspace_id: 'ws-broken',
      page_path: '/workspaces/ws-broken',
      surface: 'workspace_detail',
      origin: 'ask_ori'
    }));
    expect(chatCalls).toBe(0);

    workspaceReady = true;
    await page.evaluate(() => (window as any).OriAskRouting.refreshWorkspaceIdentity({
      workspace_id: 'ws-broken',
      page_path: '/workspaces/ws-broken',
      surface: 'workspace_detail',
      origin: 'ask_ori'
    }));
    await expect.poll(() => chatCalls).toBe(1);
  });
});

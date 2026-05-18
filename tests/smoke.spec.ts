import { test, expect } from '@playwright/test';

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

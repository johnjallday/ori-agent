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

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

import { test, expect } from '@playwright/test';

test('workspace detail accessibility', async ({ page }) => {
  const baseUrl = process.env.BASE_URL || 'http://localhost:8765';
  await page.goto(`${baseUrl}/workspaces`, { waitUntil: 'domcontentloaded' });

  const firstWorkspace = page
    .locator(
      'button[aria-label^="Open workspace"], [data-workspace-id], a[href^="/workspaces/"], [onclick*="viewWorkspace"]'
    )
    .first();

  await expect(firstWorkspace).toBeVisible();
  await firstWorkspace.click();
  await page.waitForURL(/\/workspaces\/[^/]+$/);
  await expect(page.locator('#workspaceCommandView .ws-cmd-title h2')).toBeVisible();

  await page.addScriptTag({
    url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js'
  });

  const results = await page.evaluate(async () => {
    const root = document.querySelector('#workspaceCommandView');
    if (!root) {
      throw new Error('workspaceCommandView root not found');
    }

    return await window.axe.run(root, {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
    });
  });

  expect(
    results.violations,
    JSON.stringify(results.violations, null, 2)
  ).toEqual([]);
});

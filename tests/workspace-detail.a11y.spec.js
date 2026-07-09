import { test, expect } from '@playwright/test';

for (const theme of ['light', 'dark']) {
  test(`workspace detail accessibility (${theme})`, async ({ page }) => {
    const baseUrl = process.env.BASE_URL || 'http://localhost:8765';
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.addInitScript(selectedTheme => {
      window.localStorage.setItem('ori-theme', selectedTheme);
    }, theme);
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
    const workspacesResponse = await page.request.get(`${baseUrl}/api/workspaces?tree=true`);
    expect(workspacesResponse.ok()).toBeTruthy();
    const workspaceData = await workspacesResponse.json();
    const workspaceId = workspaceData.workspaces?.find(
      workspace => workspace.kind === 'workspace' && workspace.agent_count > 0
    )?.id;
    expect(workspaceId).toBeTruthy();

    await page.goto(`${baseUrl}/workspaces/${encodeURIComponent(workspaceId)}`, {
      waitUntil: 'domcontentloaded'
    });
    await expect(page.locator('#workspaceCommandView .ws-cmd-title h2')).toBeVisible();
    await page.locator('#workspaceCommandView').evaluate(async root => {
      const finiteAnimations = root
        .getAnimations({ subtree: true })
        .filter(animation => animation.effect?.getTiming().iterations !== Infinity);
      await Promise.all(
        finiteAnimations.map(animation => animation.finished.catch(() => undefined))
      );
    });

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

    expect(results.violations, JSON.stringify(results.violations, null, 2)).toEqual([]);
  });
}

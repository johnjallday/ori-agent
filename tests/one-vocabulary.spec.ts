import { test, expect } from '@playwright/test';

/**
 * One vocabulary (PRD one-vocabulary FR15): on an ordinary (non-HQ) workspace
 * Map, the Backlog belt button and window are named Backlog, a backlog
 * ticket's button reads Start, and nothing calls the tickets quests.
 *
 * Run against an isolated demo server: ./scripts/e2e.sh tests/one-vocabulary.spec.ts
 */
test.describe('One vocabulary on the Map', () => {
  test('the Backlog window says Backlog and Start, never Quest', async ({ page }) => {
    test.setTimeout(45000);

    const stamp = Date.now();
    const wsRes = await page.request.post('/api/workspaces', {
      data: { name: `Vocabulary E2E ${stamp}` }
    });
    expect(wsRes.ok()).toBeTruthy();
    const created = (await wsRes.json()).folder as { id: string; folder_slug: string };

    const ticketRes = await page.request.post(`/api/workspaces/${created.id}/tickets`, {
      data: { state: 'backlog', title: 'Draft the release notes' }
    });
    expect(ticketRes.ok()).toBeTruthy();

    // Keep first-run onboarding from covering the Map (same bypass as smoke.spec.ts).
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });

    try {
      await page.goto(`/workspaces/${encodeURIComponent(created.folder_slug)}?mode=map`);
      await expect(page.locator('#workspaceCommandView .ws-cmd-opmap')).toBeVisible();

      const beltButton = page.locator('[data-cmd-map-window="backlog"]');
      await expect(beltButton).toHaveAttribute('aria-label', 'Backlog');
      await expect(beltButton).toHaveAttribute('title', 'Backlog');

      // dispatchEvent: at narrow widths the Give work dock can sit over the belt.
      await beltButton.dispatchEvent('click');
      const backlogWindow = page.locator('.ws-cmd-map-window-backlog');
      await expect(backlogWindow).toBeVisible();
      await expect(backlogWindow.locator('.ws-cmd-map-zone-head')).toContainText('Backlog');
      await expect(backlogWindow.locator('.ws-cmd-map-zone-head')).toContainText('Saved for later');

      const startButton = backlogWindow
        .locator('.ws-cmd-map-task-row-wrap', { hasText: 'Draft the release notes' })
        .locator('.ws-cmd-map-task-start');
      await expect(startButton).toHaveText('Start');
      await expect(startButton).toHaveAttribute(
        'aria-label',
        'Start — move Draft the release notes into work'
      );
      await expect(backlogWindow).not.toContainText('Quest');
    } finally {
      await page.request.delete(`/api/orchestration/workspace?id=${created.id}`);
    }
  });
});

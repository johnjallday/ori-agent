import { test, expect } from '@playwright/test';

/**
 * End-to-end coverage for the primary workspace-backlog user flow (PRD
 * workspace-backlog FR19-22, 30-57): capture an idea in the Details Backlog
 * panel, edit and reorder it in the shared drawer, open that SAME drawer from
 * the Map's Quest Board, Promote to Ready, and verify the item leaves every
 * Backlog projection and appears in Active Tasks — all without a page reload.
 *
 * Self-seeds a workspace + entry agent via the API (so the workspace loads
 * Map-ready with no Commander setup prompt), then drives the real UI.
 */
test.describe('Workspace Backlog', () => {
  test('capture → edit/reorder → open from Quest Board → promote → verify Active Tasks', async ({
    page
  }) => {
    test.setTimeout(45000);

    const stamp = Date.now();
    const wsRes = await page.request.post('/api/workspaces', {
      data: { name: `Backlog E2E ${stamp}` }
    });
    expect(wsRes.ok()).toBeTruthy();
    const workspaceId = (await wsRes.json()).folder.id as string;

    const agentName = `Backlog E2E Commander ${stamp}`;
    const agentRes = await page.request.post('/api/agents', {
      data: { name: agentName, type: 'orchestration' }
    });
    expect(agentRes.ok()).toBeTruthy();
    const attachRes = await page.request.post(`/api/workspaces/${workspaceId}/agents`, {
      data: { agent_name: agentName }
    });
    expect(attachRes.ok()).toBeTruthy();

    // Skip first-run onboarding so it never covers the Backlog panel/drawer
    // controls this test drives (mirrors tests/smoke.spec.ts's own bypass).
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });

    try {
      await page.goto(`/workspaces/${encodeURIComponent(workspaceId)}`);
      await page.waitForFunction(() => Boolean((window as any).workspaceDetail));

      // --- 1. Capture in the Details Backlog panel (FR19-22) ---
      const firstTitle = `Explore the new pricing model ${stamp}`;
      await page.locator('[data-cmd-backlog-add]').click();
      const quickInput = page.locator('[data-cmd-backlog-quick-input]');
      await expect(quickInput).toBeVisible();
      await quickInput.fill(firstTitle);
      await page.locator('.ws-cmd-backlog-quick-submit').click();

      const drawer = page.locator('.ws-cmd-drawer-backlog');
      await expect(drawer).toBeVisible();
      await expect(drawer).toContainText(firstTitle);
      // Backlog is not runnable: no Assign/Schedule/Start/Run/Review/Complete
      // controls appear anywhere in the drawer while the item is Backlog (FR39).
      await expect(
        drawer.getByRole('button', { name: /^(Assign|Schedule|Start|Run|Review|Complete)$/i })
      ).toHaveCount(0);

      // Capture a second item so reordering has something to do.
      await page.locator('[data-cmd-backlog-quick-open]').click();
      const secondTitle = `Draft onboarding copy ${stamp}`;
      await quickInput.fill(secondTitle);
      await page.locator('.ws-cmd-backlog-quick-submit').click();
      await expect(drawer).toContainText(secondTitle);

      // --- 2. Edit the first item (FR20) ---
      const firstRow = drawer.locator('.ws-cmd-drawer-row', { hasText: firstTitle });
      await firstRow.click();
      await drawer.locator('[data-cmd-backlog-edit]').click();
      const detailsField = drawer.locator('[data-cmd-backlog-edit-field="details"]');
      await expect(detailsField).toBeVisible();
      await detailsField.fill('Look at three competitors and summarize pricing tiers.');
      await drawer.locator('.ws-cmd-backlog-edit button[type="submit"]').click();
      await expect(drawer).toContainText('Look at three competitors');

      // --- 3. Reorder: move the first item down, then back up (FR18, 37, 57) ---
      await firstRow.click();
      const initialOrder = await drawer.locator('.ws-cmd-drawer-row-title').allTextContents();
      await drawer.locator('[data-cmd-backlog-move="down"]').click();
      await expect
        .poll(() => drawer.locator('.ws-cmd-drawer-row-title').allTextContents())
        .not.toEqual(initialOrder);
      await page.waitForTimeout(500);
      await drawer.locator('[data-cmd-backlog-move="up"]').click();
      await expect
        .poll(() => drawer.locator('.ws-cmd-drawer-row-title').allTextContents())
        .toEqual(initialOrder);

      await page.locator('[data-cmd-drawer-close]').click();
      await expect(drawer).toBeHidden();

      // --- 4. Open the SAME drawer from the Map's Quest Board (FR36, 52) ---
      await page.getByRole('button', { name: /^map$/i }).click();
      await expect(page.locator('#workspaceCommandView .ws-cmd-opmap')).toBeVisible();
      await page.locator('[data-cmd-map-window="backlog"]').click();
      const questBoard = page.locator('.ws-cmd-map-window-backlog');
      await expect(questBoard).toBeVisible();
      await expect(questBoard).toContainText(firstTitle);
      await expect(questBoard).toContainText(secondTitle);

      await page.locator('.ws-cmd-map-window-backlog [data-cmd-open-backlog-drawer]').click();
      await expect(drawer).toBeVisible();
      // The Map window closed in the same transition (FR52) so the two never stack.
      await expect(page.locator('.ws-cmd-map-window-backlog')).toBeHidden();
      await expect(drawer).toContainText(firstTitle);

      // --- 5. Promote the first item to Ready (FR9-12, 31) ---
      const promoteRow = drawer.locator('.ws-cmd-drawer-row', { hasText: firstTitle });
      await promoteRow.click();
      await drawer.locator('[data-cmd-backlog-promote]').click();
      await drawer.locator('[data-cmd-backlog-promote-confirm]').click();

      // Leaves every Backlog projection: drawer list, Details panel count.
      await expect(drawer.locator('.ws-cmd-drawer-row', { hasText: firstTitle })).toHaveCount(0);
      await page.locator('[data-cmd-drawer-close]').click();

      // --- 6. Verify it now appears in Active Tasks, without a page reload (FR47, 54) ---
      await page.locator('[data-cmd-map-window="objectives"]').click();
      const tasksWindow = page.locator('.ws-cmd-map-window-objectives');
      await expect(tasksWindow).toBeVisible();
      await expect(tasksWindow).toContainText(firstTitle);
      // Still not run/assigned by promotion alone: a Start control is offered,
      // not an in-progress/completed state.
      await expect(
        tasksWindow.locator('.ws-cmd-map-task-row-wrap', { hasText: firstTitle })
      ).toContainText(/pending/i);
    } finally {
      await page.request.delete(`/api/orchestration/workspace?id=${workspaceId}`);
    }
  });
});

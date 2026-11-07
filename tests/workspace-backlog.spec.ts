import { test, expect } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

/**
 * End-to-end coverage for the primary workspace-backlog user flow (PRD
 * workspace-backlog FR19-22, 30-57): preserve legacy Backlog URL arrivals,
 * capture/edit/reorder in the real shared drawer, arrive from an old Map URL,
 * promote explicitly, and verify the canonical Tickets destination sees Ready.
 *
 * Self-seeds a workspace + entry agent via the API (so the workspace loads
 * Map-ready with no Commander setup prompt), then drives the real UI.
 */
test.describe('Workspace Backlog', () => {
  test('legacy arrival → capture/edit/reorder → Map arrival → promote → canonical Tickets', async ({
    page
  }) => {
    test.setTimeout(45000);
    await installLocalCdn(page);

    const stamp = Date.now();
    const wsRes = await page.request.post('/api/workspaces', {
      data: { name: `Backlog E2E ${stamp}` }
    });
    expect(wsRes.ok()).toBeTruthy();
    const createdWorkspace = (await wsRes.json()).folder;
    const workspaceId = createdWorkspace.id as string;
    const workspaceSlug = createdWorkspace.folder_slug as string;

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
      await page.goto(`/workspaces/${encodeURIComponent(workspaceSlug)}?panel=backlog`);
      await page.waitForFunction(() => Boolean((window as any).workspaceDetail));

      // --- 1. Preserve the legacy Backlog arrival and capture owner ---
      const firstTitle = `Explore the new pricing model ${stamp}`;
      await page.locator('[data-cmd-backlog-quick-open]').click();
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

      // --- 4. An old Map panel URL still opens that same real drawer. ---
      await page.goto(`/workspaces/${encodeURIComponent(workspaceSlug)}?mode=map&panel=backlog`);
      await expect(page.locator('#workspaceCommandView .ws-cmd-opmap')).toBeVisible();
      await expect(drawer).toBeVisible();
      await expect(drawer).toContainText(firstTitle);
      await expect(drawer).toContainText(secondTitle);
      await expect(page.locator('.ws-cmd-map-window-backlog')).toHaveCount(0);

      // --- 5. Promote the first item to Ready (FR9-12, 31) ---
      // "Turn into Task" promotes atomically (still no auto-assignment) and
      // then opens the real Task modal on the now-Ready task so the user can
      // assign/schedule it right away if they choose to.
      const promoteRow = drawer.locator('.ws-cmd-drawer-row', { hasText: firstTitle });
      await promoteRow.click();
      await drawer.locator('[data-cmd-backlog-promote]').click();
      await drawer.locator('[data-cmd-backlog-promote-confirm]').click();

      const taskModal = page.locator('#taskModal');
      await expect(taskModal).toBeVisible();
      await expect(page.locator('#taskModalDescription')).toHaveValue(firstTitle);
      // The drawer closes in the same transition so the modal is never stacked on top of it.
      await expect(drawer).toBeHidden();
      await page.locator('#taskModalClose').click();
      await expect(taskModal).toBeHidden();

      // --- 6. The canonical destination sees Ready, not an invented raw status. ---
      await page.locator('[data-cmd-view-mode="tickets"]').click();
      const card = page.locator('#hubTicketsList .ticket-card', { hasText: firstTitle });
      await expect(card).toBeVisible();
      await expect(card.locator('.ticket-state-chip')).toHaveText('Ready');
    } finally {
      // Preserve a failed assertion rather than masking it with teardown timeout.
      await page.request.delete(`/api/orchestration/workspace?id=${workspaceId}`).catch(() => {});
    }
  });
});

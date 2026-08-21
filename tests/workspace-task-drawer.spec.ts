import { test, expect } from '@playwright/test';

/**
 * Reproduces the reported bug and proves the fix (PRD FR138): opening tasks from
 * the Operations Map's Objectives window must show a task surface that is
 * VISIBLE and interactive, not hidden beneath the window that launched it.
 *
 * The old flow opened a modal (z-index 1050) under the Map window (z-index
 * 10010). The new flow opens a non-modal drawer AND closes the Objectives
 * window in the same transition, so the two never stack.
 *
 * Self-seeds a workspace + tasks via the API, then drives the real UI (creating
 * the entry agent through the setup prompt if the workspace has none).
 */
test.describe('Workspace task drawer', () => {
  test('Open Tasks shows a visible drawer over the Map, not under the Objectives window', async ({
    page
  }) => {
    // --- seed via API: workspace + entry agent + a few tasks (so the page
    // loads Map-ready with no setup prompt) ---
    const stamp = Date.now();
    const wsRes = await page.request.post('/api/workspaces', {
      data: { name: `Drawer E2E ${stamp}`, description: 'drawer repro' }
    });
    expect(wsRes.ok()).toBeTruthy();
    const workspace = (await wsRes.json()).folder;
    const workspaceId = workspace.id as string;
    const workspaceSlug = workspace.folder_slug as string;
    expect(workspaceSlug).toBeTruthy();

    const agentName = `Drawer E2E Manager ${stamp}`;
    const agentRes = await page.request.post('/api/agents', {
      data: { name: agentName, type: 'orchestration' }
    });
    expect(agentRes.ok()).toBeTruthy();
    const attachRes = await page.request.post(`/api/workspaces/${workspaceId}/agents`, {
      data: { agent_name: agentName }
    });
    expect(attachRes.ok()).toBeTruthy();

    for (const description of ['Render the intro', 'Confirm the grade', 'Export the master']) {
      const t = await page.request.post(`/api/workspaces/${workspaceId}/tasks`, {
        data: { description }
      });
      expect(t.ok()).toBeTruthy();
    }

    await page.goto(`/workspaces/${encodeURIComponent(workspaceSlug)}`);

    // Switch to the Operations Map.
    await page.getByRole('button', { name: /^map$/i }).click();

    // Open the Objectives Map window, then Open Tasks (precise selectors).
    await page.locator('[data-cmd-map-window="objectives"]').click();
    const objectivesWindow = page.locator('.ws-cmd-map-window-backdrop');
    await expect(objectivesWindow).toBeVisible();
    await page.locator('[data-cmd-open-task-drawer]').click();

    // --- assertions: the fix ---
    const drawer = page.locator('.ws-cmd-drawer');
    await expect(drawer).toBeVisible();

    // The Objectives window is closed in the same transition (never stacked, FR11/FR117).
    await expect(objectivesWindow).toBeHidden();

    // The drawer is genuinely interactive: rows render and are clickable.
    const rows = drawer.locator('.ws-cmd-drawer-row');
    await expect(rows.first()).toBeVisible();
    expect(await rows.count()).toBeGreaterThan(0);

    // Rows use a stable short id (#XXXX), not the positional "Quest NN" marker.
    await expect(drawer).not.toContainText(/Quest\s*0\d/);

    // Selecting a row updates the preview in place (no navigation).
    const urlBefore = page.url();
    await rows.first().click();
    await expect(drawer.locator('.ws-cmd-drawer-preview')).toBeVisible();
    expect(page.url()).toBe(urlBefore);

    // Filters expose an actionable count and the drawer offers an Add Task control.
    await expect(drawer.getByRole('button', { name: /actionable/i })).toBeVisible();
    await expect(drawer.locator('[data-cmd-drawer-add]')).toBeVisible();
  });
});

import { test, expect } from '@playwright/test';

// Happy-path regression for the game-inspired Agents page (roster + stage).
// Assumes a running server; create/cleanup a throwaway agent via the API so the
// test is self-contained and order-independent.
const baseUrl = process.env.BASE_URL || 'http://localhost:8765';

test.describe('Agents roster', () => {
  test('browse, select, edit, assign workspace, and delete', async ({ page, request }) => {
    const name = `PW Roster ${Date.now()}`;

    const create = await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' },
    });
    expect(create.ok()).toBeTruthy();

    try {
      // Keep onboarding out of the way and pin a theme for determinism.
      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true }),
        })
      );

      // Roster is the default Agents view; deep-link straight to our agent.
      await page.goto(`${baseUrl}/agents?agent=${encodeURIComponent(name)}`, {
        waitUntil: 'domcontentloaded',
      });

      await expect(page.locator('#rosterList')).toBeVisible();
      await expect(page.locator('#stageName')).toHaveText(name);

      // Overview edit → save → persisted.
      const desc = page.locator('#ov-description');
      await expect(desc).toBeVisible();
      await desc.fill('Edited by Playwright.');
      const save = page.locator('#savebar-overview [data-role="save"]');
      await expect(save).toBeEnabled();
      await save.click();
      await expect
        .poll(async () => {
          const r = await request.get(`${baseUrl}/api/agents/${encodeURIComponent(name)}/detail`);
          return (await r.json()).metadata?.description;
        })
        .toBe('Edited by Playwright.');

      // Prompt tab lazy-loads an editable textarea.
      await page.locator('#tab-prompt').click();
      await expect(page.locator('#pr-prompt')).toBeVisible();

      // Workspaces tab renders the editable assignment list.
      await page.locator('#tab-workspaces').click();
      await expect(page.locator('#panel-workspaces')).toBeVisible();

      // Delete via the stage button (auto-accept the confirm dialog).
      page.once('dialog', dialog => dialog.accept());
      await page.locator('#stageDelete').click();
      await expect
        .poll(async () => {
          const r = await request.get(`${baseUrl}/api/agents/${encodeURIComponent(name)}/detail`);
          return r.status();
        })
        .toBe(404);
    } finally {
      // Best-effort cleanup if the test bailed before the delete step.
      await request.delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`).catch(() => undefined);
    }
  });
});

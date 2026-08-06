import { test, expect, Page, APIRequestContext } from '@playwright/test';

/**
 * Demo capture for the Create Workspace Team-step avatars.
 *
 * Drives the real wizard on the live /workspaces host and screenshots the Team
 * step, so the shared identity renderer is verified in the view the user
 * actually uses rather than only in unit tests.
 *
 * Seeds its own agents — one per resolver branch — so it is repeatable against
 * a fresh sandbox instead of depending on whatever the server happens to hold:
 *   ./scripts/e2e.sh tests/create-workspace-avatar-demo.spec.ts
 */

// Unique per run so a re-run never collides with its own leftovers.
const RUN = `PWAvatar${Date.now()}`;
const WITH_CHARACTER = `${RUN} Scout`;
const WITHOUT_CHARACTER = `${RUN} Plain`;

async function seedAgent(request: APIRequestContext, name: string, catalogId?: string) {
  const data: Record<string, unknown> = { name, model: 'gpt-5.5', role: 'specialist' };
  if (catalogId) data.character = { display_mode: 'character', catalog_id: catalogId };
  const res = await request.post('/api/agents', { data });
  expect(res.ok(), `seeding ${name} failed (${res.status()})`).toBe(true);
}

test.beforeAll(async ({ request }) => {
  await seedAgent(request, WITH_CHARACTER, 'insight-researcher');
  await seedAgent(request, WITHOUT_CHARACTER);
});

test.afterAll(async ({ request }) => {
  for (const name of [WITH_CHARACTER, WITHOUT_CHARACTER]) {
    await request.delete(`/api/agents/${encodeURIComponent(name)}`).catch(() => {});
  }
});

async function openCreateModal(page: Page) {
  await page.evaluate(() => {
    const el = document.getElementById('addFolderModal');
    // @ts-expect-error bootstrap is a page global
    window.bootstrap.Modal.getOrCreateInstance(el).show();
  });
  await expect(page.locator('#addFolderModal')).toBeVisible();
}

test.beforeEach(async ({ page }) => {
  await page.request.post('/api/onboarding/skip').catch(() => {});
  // Capture in dark theme: it is what the app is actually used in, and it is
  // where a portrait's contrast against the panel is hardest.
  await page.addInitScript(() => {
    try {
      localStorage.setItem('ori-theme', 'dark');
    } catch {
      /* private mode — the light capture is still valid */
    }
  });
  await page.goto('/workspaces');
});

test('Team step renders agent identities through the shared renderer', async ({ page }) => {
  await openCreateModal(page);

  // Blueprint → Details
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await page.locator('#folderNameInput').fill('Avatar Demo');

  // Details → Team
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();

  // The picker is what shows saved agents, so wait for it to finish loading.
  const cards = page.locator('#existingAgentRosterList .workspace-existing-agent-card');
  await expect(cards.first()).toBeVisible();

  // Every tile is the shared renderer, not the old private initials span.
  const tiles = page.locator('#existingAgentRosterList .workspace-agent-avatar');
  const count = await tiles.count();
  expect(count).toBeGreaterThan(0);
  for (let i = 0; i < count; i++) {
    await expect(tiles.nth(i)).toHaveClass(/agent-avatar/);
  }

  // The seeded character must resolve to a real portrait. Initials here would
  // mean the catalog never loaded — the exact silent failure this guards.
  const seeded = page.locator(
    `.workspace-existing-agent-card[data-existing-agent-name="${WITH_CHARACTER}"]`
  );
  await expect(seeded.locator('.agent-avatar--character .agent-avatar__portrait')).toBeVisible();

  // An agent with no character stays on the deterministic fallback, which is
  // what a not-yet-created blueprint agent also gets.
  const plain = page.locator(
    `.workspace-existing-agent-card[data-existing-agent-name="${WITHOUT_CHARACTER}"]`
  );
  await expect(plain.locator('.agent-avatar--fallback')).toBeVisible();

  // Add one so the left-hand Resulting Workspace Team roster shows it too.
  await page.locator(`[data-existing-agent-add="${WITH_CHARACTER}"]`).click();
  // The added agent keeps its portrait on the Team roster, and the blueprint's
  // own not-yet-created agent sits beside it on deterministic art.
  const teamRow = page.locator(
    `#workspaceTeamRoster [data-agent-key="${WITH_CHARACTER.toLowerCase()}"]`
  );
  await expect(teamRow.locator('.agent-avatar--character .agent-avatar__portrait')).toBeVisible();
  await expect(page.locator('#workspaceTeamRoster .agent-avatar--fallback').first()).toBeVisible();

  // Both rosters scroll independently; capture them from the top so the
  // Resulting Workspace Team roster is in frame, not scrolled past.
  await page.evaluate(() => {
    document.querySelectorAll('#wizardStep3 *').forEach(el => {
      if (el instanceof HTMLElement && el.scrollHeight > el.clientHeight) el.scrollTop = 0;
    });
    document.querySelector('#addFolderModal .modal-body')?.scrollTo(0, 0);
  });

  await page.locator('#addFolderModal .modal-dialog').screenshot({
    path: 'test-results/create-workspace-team-avatars.png'
  });
});

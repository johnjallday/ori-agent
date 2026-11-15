import { test, expect } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { installLocalCdn } from './helpers/offline-cdn';

// Deterministic browser coverage of Home's host-owned faces. The real hire and
// saved appearance were exercised separately in the isolated demo (findings).
const evidence = 'tasks/evidence-one-face-everywhere';
const folders = [
  {
    id: 'chosen',
    name: 'Albums',
    folder_slug: 'albums',
    kind: 'workspace',
    entry_agent_name: 'Albums Manager',
    agents: ['Albums Manager'],
    agent_count: 1,
    ticket_counts: { todo: 0, in_progress: 0, review: 0, done: 0 },
    task_summary_available: true
  },
  {
    id: 'generated',
    name: 'Notes',
    folder_slug: 'notes',
    kind: 'workspace',
    entry_agent_name: 'Notes Keeper',
    agents: ['Notes Keeper'],
    agent_count: 1,
    task_summary_available: true
  }
];

test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { needs_onboarding: false, completed: true } })
  );
  await page.route(/\/api\/personal-assistant$/, route =>
    route.fulfill({
      json: {
        personal_assistant: {
          state: 'active',
          state_version: 1,
          display_name: 'Atlas',
          assistant_id: 'fixture',
          hq_workspace_id: 'hq',
          availability: { model: { available: false } }
        }
      }
    })
  );
  await page.route('**/api/workspaces?tree=true', route => route.fulfill({ json: { folders } }));
  await page.route('**/api/personal-assistant/folder-agent', route =>
    route.fulfill({ json: { folder_agents: [] } })
  );
});

for (const theme of ['light', 'dark']) {
  test(`panel chosen/generated/pending faces, stable focus (${theme})`, async ({ page }) => {
    await page.addInitScript(theme => {
      localStorage.setItem('ori-theme', theme);
    }, theme);
    let release!: () => void;
    const held = new Promise<void>(resolve => {
      release = resolve;
    });
    let reads = 0;
    await page.route('**/api/agents/dashboard/list', async route => {
      reads++;
      await held;
      await route.fulfill({
        json: {
          agents: [
            {
              name: 'Albums Manager',
              role: 'orchestrator',
              appearance: { mode: 'character', character: { catalog_id: 'project-coordinator' } }
            },
            { name: 'Notes Keeper', appearance: { mode: 'generated' } }
          ]
        }
      });
    });
    await page.goto('/');
    await page.locator('[data-ws-id="chosen"]').click();
    const modal = page.locator('#cockpitContextModal');
    await expect(modal).toBeVisible();
    await expect(modal.locator('.home-agent-face-slot')).toHaveCount(2);
    await expect(modal.locator('.agent-avatar, .ws-map-av-figure')).toHaveCount(0);
    const open = modal.locator('[data-cockpit-rail-open]');
    await expect(modal.locator('[data-cockpit-rail-back]')).toBeFocused();
    await open.focus();
    const live = await page
      .locator('#cockpitRailLive')
      .textContent()
      .catch(() => '');
    release();
    await expect(modal.locator('.agent-avatar--character')).toHaveCount(2);
    await expect(open).toBeFocused();
    if (live) await expect(page.locator('#cockpitRailLive')).toHaveText(live);
    mkdirSync(evidence, { recursive: true });
    await page.screenshot({ path: `${evidence}/group2-character-${theme}.png` });
    await modal.getByRole('button', { name: 'Close context', exact: true }).click();
    await expect(modal).toBeHidden();
    await page.locator('[data-ws-id="generated"]').click();
    await expect(modal).toBeVisible();
    await expect(modal.locator('.ws-map-av-figure')).toHaveCount(2);
    await expect(modal.locator('.agent-avatar')).toHaveCount(0);
    expect(reads).toBe(1);
    await page.screenshot({ path: `${evidence}/group2-generated-${theme}.png` });
  });
}

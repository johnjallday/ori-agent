import { test, expect } from '@playwright/test';
import { capture, preparePage, contrast } from './helpers/ui-consistency';
import {
  assertCanvasAxe,
  boundedCanvasControl as bounded,
  tabToCanvasControl
} from './helpers/ui-consistency-canvas';
import {
  createWorkspaceSeed,
  seedCanvasSavedWorkflow,
  freezeWorkspaceSeed,
  retireWorkspaceSeed,
  type WorkspaceSeed
} from './helpers/ui-consistency-workspace';
import {
  workspaceMetadataReads,
  guardWorkspaceReadTour
} from './fixtures/ui-consistency-workspace';

let seed: WorkspaceSeed | undefined;
let check: (() => Promise<void>) | undefined;
test.afterEach(async ({ page, request }) => {
  test.setTimeout(120_000);
  await page.goto('about:blank');
  try {
    if (check) await check();
  } finally {
    if (seed) await retireWorkspaceSeed(request, seed);
    seed = undefined;
    check = undefined;
  }
});
for (const theme of ['light', 'dark'] as const)
  for (const width of [320, 1440])
    test(`real saved workflow sidebar filters refreshes and cancels deletion ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      seed = await createWorkspaceSeed(request);
      const workflow = await seedCanvasSavedWorkflow(request, seed);
      await freezeWorkspaceSeed(request, seed);
      await preparePage(page, theme, width);
      await workspaceMetadataReads(page);
      check = await guardWorkspaceReadTour(page, [seed]);
      let checks = 0;
      const responses: Promise<void>[] = [];
      const path = `/api/workflows/${workflow.id}/check-agents`;
      // Handler.handleCheckAgents only reads the workflow and workspace roster.
      // Narrowly allow this real POST read; never manufacture a successful write.
      await page.route(`**${path}`, async route => {
        if (route.request().method() !== 'POST') return route.fallback();
        expect(route.request().postDataJSON()).toEqual({ workspace_id: seed!.workspace.id });
        checks++;
        await route.continue();
      });
      page.on('response', response => {
        if (new URL(response.url()).pathname !== path) return;
        responses.push(
          (async () => {
            expect(response.ok()).toBe(true);
            const body = await response.json();
            expect(body.available).toBe(true);
            expect(body.missing_agents || []).toEqual([]);
            expect(body.required_agents).toEqual([]);
          })()
        );
      });
      await page.goto(`/workspaces/${seed.workspace.folder_slug}/canvas`);
      const panel = page.locator('#workflow-panel');
      const row = page.locator(`[data-workflow-row="${workflow.id}"]`);
      await expect(row).toBeVisible();
      const toggle = panel.getByRole('button', { name: 'Workflows', exact: true });
      await tabToCanvasControl(toggle);
      await expect(toggle).toBeFocused();
      await bounded(toggle);
      await toggle.press('Enter');
      await expect(toggle).toHaveAttribute('aria-expanded', 'false');
      await expect(page.locator('#workflow-panel-content')).toBeHidden();
      await toggle.press('Enter');
      await expect(toggle).toHaveAttribute('aria-expanded', 'true');
      const search = panel.getByRole('textbox', { name: 'Search workflows', exact: true });
      await search.fill('No matching fictional entry');
      await expect(panel).toContainText('No matching workflows');
      await expect(search).toBeFocused();
      await search.fill('saved local review');
      await panel
        .getByRole('combobox', { name: 'Workflow category', exact: true })
        .selectOption('Local review');
      await expect(row).toBeVisible();
      const add = row.getByRole('button', { name: 'Add', exact: true });
      await bounded(add); // Never instantiate the saved workflow.
      const remove = row.getByRole('button', { name: 'Delete workflow', exact: true });
      await bounded(remove);
      page.once('dialog', dialog => dialog.dismiss());
      await remove.press('Enter');
      await expect(remove).toBeFocused();
      const refresh = panel.getByRole('button', { name: 'Refresh workflows', exact: true });
      await bounded(refresh);
      await refresh.press('Enter');
      await expect(refresh).toHaveAttribute('aria-busy', 'false');
      await expect(row).toBeVisible();
      await expect(refresh).toBeFocused();
      await expect(search).toHaveValue('saved local review');
      await expect(
        panel.getByRole('combobox', { name: 'Workflow category', exact: true })
      ).toHaveValue('Local review');
      expect((await contrast(row.locator('.badge-success'))).ratio).toBeGreaterThanOrEqual(4.5);
      expect(checks).toBe(2);
      await assertCanvasAxe(page, '#workflow-panel');
      await Promise.all(responses);
      await capture(
        page,
        info,
        'workspace-canvas-saved-library',
        {
          workspaceId: seed.workspace.id,
          fixture:
            'Real saved definition and read-only POST agent checks; Add never invoked, Delete canceled, exact API/file cleanup journal'
        },
        { fullPage: false }
      );
      // Named GET-only failure projection, followed by a real refresh recovery.
      let refuse = true;
      await page.route('**/api/workflows', route =>
        route.request().method() === 'GET' && refuse
          ? route.fulfill({
              status: 503,
              json: { error: 'Presentation-only saved-library read refusal' }
            })
          : route.fallback()
      );
      await refresh.press('Enter');
      await expect(panel.getByRole('alert')).toContainText('Unable to load saved workflows');
      await expect(panel).not.toContainText('No workflows saved yet');
      await expect(refresh).toBeFocused();
      await capture(
        page,
        info,
        'workspace-canvas-library-unavailable',
        {
          workspaceId: seed.workspace.id,
          fixture: 'Presentation-only 503 GET, not a successful mutation or availability grant'
        },
        { fullPage: false }
      );
      refuse = false;
      await refresh.press('Enter');
      await expect(row).toBeVisible();
      await expect(panel.getByRole('alert')).toHaveCount(0);
      await expect(search).toHaveValue('saved local review');
      await expect(
        panel.getByRole('combobox', { name: 'Workflow category', exact: true })
      ).toHaveValue('Local review');
      await expect(refresh).toBeFocused();
      await Promise.all(responses);
      await check();
    });

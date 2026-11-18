import { expect, test } from '@playwright/test';
import { capture, contrast, preparePage, reachable } from './helpers/ui-consistency';
import {
  createWorkspaceSeed,
  populateWorkspaceSeed,
  freezeWorkspaceSeed,
  retireWorkspaceSeed,
  type WorkspaceSeed
} from './helpers/ui-consistency-workspace';
import {
  guardWorkspaceReadTour,
  simulatedRunReads,
  workspaceMetadataReads
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
  for (const width of [320, 390, 768, 1440]) {
    test(`workspace run states, bounded evidence and decision cancellation ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      test.setTimeout(90_000);
      seed = await createWorkspaceSeed(request);
      const data = await populateWorkspaceSeed(request, seed);
      await freezeWorkspaceSeed(request, seed);
      await preparePage(page, theme, width);
      await workspaceMetadataReads(page);
      check = await guardWorkspaceReadTour(page, [seed]);
      for (const status of ['executing', 'awaiting_approval', 'succeeded', 'failed'] as const) {
        const run = await simulatedRunReads(page, seed.workspace.id, data.execution.id, status);
        run.report.summary =
          'Fictional read-only run result; no provider, tool or workflow ran. '.repeat(70);
        await page.goto(`/workspaces/${seed.workspace.folder_slug}/runs/${run.id}`);
        await expect(page.locator('#workspace-run-content')).toBeVisible();
        await expect(page.locator('#workspace-run-status')).toHaveAttribute('data-state', status);
        await expect(page.locator('#workspace-run-overview')).toContainText(run.id);
        await expect(page.locator('#workspace-run-workspace-name')).toHaveText(seed.workspace.name);
        await expect(page.locator('#workspace-run-backlink')).toHaveAttribute(
          'href',
          `/workspaces/${seed.workspace.folder_slug}/task/${data.execution.id}`
        );
        expect(
          (await contrast(page.locator('#workspace-run-status'))).ratio
        ).toBeGreaterThanOrEqual(4.5);
        await reachable(page.locator('#workspace-run-refresh'), width <= 768 ? 44 : 40);
        const actions = page.locator('#workspace-run-actions');
        if (status === 'executing')
          await expect(actions.locator('[data-run-action]')).toHaveCount(1);
        else if (status === 'awaiting_approval') {
          await expect(actions.locator('[data-run-action]')).toHaveCount(2);
          const reject = actions.locator('[data-run-action="reject"]');
          await expect(actions.locator('[data-run-action="approve"]')).toBeEnabled();
          await reject.focus();
          page.once('dialog', dialog => dialog.dismiss());
          await reject.press('Enter');
          await expect(page.locator('#workspace-run-status')).toHaveAttribute(
            'data-state',
            'awaiting_approval'
          );
          await expect(page.locator('#workspace-run-alert')).toBeHidden();
        } else await expect(actions.locator('[data-run-action]')).toHaveCount(0);
        for (const action of await actions.locator('button').all()) {
          await reachable(action, width <= 768 ? 44 : 40);
          expect((await contrast(action)).ratio).toBeGreaterThanOrEqual(4.5);
        }
        await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }));
        await capture(
          page,
          info,
          `workspace-run-${status}-summary`,
          {
            workspaceId: seed.workspace.id,
            taskId: data.execution.id,
            runId: run.id,
            fixture:
              'GET-only runtime status/action/result presentation; no run or decision executed'
          },
          { fullPage: false }
        );
        const technical = page.locator('.workspace-run-advanced');
        await expect(technical).not.toHaveAttribute('open');
        await technical.locator('summary').click();
        await expect(technical).toHaveAttribute('open', '');
        await expect(technical).toContainText(run.id);
        for (const region of [
          page.locator('#workspace-run-report'),
          page.locator('#workspace-run-trace')
        ]) {
          await expect(region).toHaveAttribute('tabindex', '0');
          const dimensions = await region.evaluate(node => ({
            height: node.clientHeight,
            scroll: node.scrollHeight
          }));
          expect(dimensions.height).toBeLessThanOrEqual(384);
          expect(dimensions.scroll).toBeGreaterThan(dimensions.height);
          await region.focus();
          await region.press('PageDown');
          await expect.poll(() => region.evaluate(node => node.scrollTop)).toBeGreaterThan(0);
        }
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)
        ).toBe(true);
        await capture(
          page,
          info,
          `workspace-run-${status}-evidence`,
          {
            workspaceId: seed.workspace.id,
            taskId: data.execution.id,
            runId: run.id,
            fixture:
              'coherent GET-only runtime presentation; real saved definition remains pending; cancellation sent no decision'
          },
          { fullPage: false }
        );
        await check();
      }
      const unchanged = await (
        await request.get(`/api/orchestration/tasks?id=${data.execution.id}`)
      ).json();
      expect(unchanged).toEqual(data.execution);
    });
  }

import { expect, test, type Locator } from '@playwright/test';
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
  simulatedExecutionReads,
  workspaceMetadataReads
} from './fixtures/ui-consistency-workspace';

async function fullyUncovered(control: Locator) {
  await control.focus();
  await expect
    .poll(() =>
      control.evaluate(node => {
        const r = node.getBoundingClientRect();
        if (r.left < 0 || r.top < 0 || r.right > innerWidth + 1 || r.bottom > innerHeight + 1)
          return false;
        for (const selector of [
          '.navbar',
          '.workspace-task-page-hero',
          '.personal-assistant-launcher'
        ]) {
          const blocker = document.querySelector(selector);
          if (
            !blocker ||
            (selector.includes('hero') && getComputedStyle(blocker).position !== 'sticky')
          )
            continue;
          const b = blocker.getBoundingClientRect();
          if (
            b.width &&
            b.height &&
            r.left < b.right &&
            r.right > b.left &&
            r.top < b.bottom &&
            r.bottom > b.top
          )
            return false;
        }
        return [r.left + 8, r.right - 8].every(x =>
          [r.top + 8, r.bottom - 8].every(y => {
            const hit = document.elementFromPoint(x, y);
            return hit === node || node.contains(hit);
          })
        );
      })
    )
    .toBe(true);
}

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
    test(`saved execution projections retain state authority and cancel edits ${theme} ${width}`, async ({
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
      for (const state of ['running', 'blocked', 'completed', 'failed', 'review'] as const) {
        await simulatedExecutionReads(page, seed, data.execution, state);
        await page.goto(`/workspaces/${seed.workspace.folder_slug}/task/${data.execution.id}`);
        await expect(page.locator('#workspace-task-page-content')).toBeVisible();
        if (state === 'completed' || state === 'review') {
          const output = page.getByRole('region', { name: 'Ticket result', exact: true });
          await expect(output).toBeVisible();
          const bounds = await output.evaluate(node => ({
            height: node.clientHeight,
            content: node.scrollHeight
          }));
          expect(bounds.height).toBeLessThanOrEqual(384);
          expect(bounds.content).toBeGreaterThan(bounds.height);
          await output.focus();
          await output.press('PageDown');
          await expect.poll(() => output.evaluate(node => node.scrollTop)).toBeGreaterThan(0);
        }
        const title = page.locator('#workspace-task-title');
        await expect(title).toHaveText(data.execution.description);
        await expect(page.locator('#workspace-task-workspace-name')).toHaveText(
          seed.workspace.name
        );
        const status = page.locator('#workspace-task-status');
        await expect(status).toHaveText(
          state === 'review' ? /In Review/ : state === 'completed' ? /Done/ : /In Progress/
        );
        if (process.env.ORI_UI_EVIDENCE === 'before')
          await capture(
            page,
            info,
            `workspace-execution-${state}`,
            {
              workspaceId: seed.workspace.id,
              taskId: data.execution.id,
              fixture: 'GET-only execution projection; genuine saved definition untouched'
            },
            { fullPage: false }
          );
        expect((await contrast(status)).ratio).toBeGreaterThanOrEqual(4.5);
        await reachable(
          page.locator('#workspace-task-hero-overflow-toggle'),
          width <= 768 ? 44 : 40
        );
        await page.evaluate(() => scrollTo(0, 0));
        await capture(
          page,
          info,
          `workspace-execution-${state}-summary`,
          {
            workspaceId: seed.workspace.id,
            taskId: data.execution.id,
            fixture: 'GET-only execution projection over an unstarted saved definition'
          },
          { fullPage: false }
        );
        if (state === 'blocked') {
          await expect(page.locator('#workspace-task-assist-card')).toBeVisible();
          await expect(page.locator('#workspace-task-assist-needs')).toContainText('Choose 1 of 2');
          await expect(page.locator('#workspace-task-assist-question')).toHaveText(
            'Who should read the proposed workshop summary?'
          );
          const choice = page.locator('[data-assist-choice-id="writers"]');
          await choice.focus();
          await choice.press('Space');
          await expect(choice).toBeFocused();
          await expect(choice).toHaveAttribute('aria-pressed', 'true');
          await expect(page.locator('#workspace-task-assist-continue')).toHaveText(
            'Continue With Selected Path'
          );
          await expect(page.locator('#workspace-task-assist-continue')).toBeEnabled();
          await fullyUncovered(page.locator('#workspace-task-assist-continue'));
          await fullyUncovered(page.locator('#workspace-task-assist-fail'));
          await page.addScriptTag({
            url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js'
          });
          const access = await page.evaluate(() =>
            window.axe.run(document.querySelector('#workspace-task-assist-card'), {
              runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
            })
          );
          expect(access.violations).toEqual([]);
        }
        if (state === 'failed')
          await expect(page.locator('#workspace-task-run-signal')).toContainText(/failed/i);
        if (state === 'review') {
          const review = page.locator('.workspace-task-review-panel');
          await expect(review).toBeVisible();
          await expect(review).toHaveAttribute('data-review-task-id', data.execution.id);
          await expect(review.locator('.workspace-task-review-errors')).toContainText(
            'Fictional missing audience'
          );
          await expect(review.locator('[data-review-action="approve_append"]')).toBeEnabled();
          for (const action of [
            'approve_append',
            'copy',
            'retry_normalization',
            'rerun',
            'dismiss'
          ])
            await fullyUncovered(review.locator(`[data-review-action="${action}"]`));
        }
        await capture(
          page,
          info,
          `workspace-execution-${state}`,
          {
            workspaceId: seed.workspace.id,
            taskId: data.execution.id,
            fixture:
              'GET-only execution projection over actual pending definition; no start, retry, repair, approval or schedule'
          },
          { fullPage: false }
        );
        // Keyboard cancellation must discard this local draft without the blur
        // auto-save path turning a cancellation into a mutation.
        await title.focus();
        await title.press('Enter');
        const input = page.locator('.workspace-task-page-title-input');
        await expect(input).toBeFocused();
        await input.fill('Unsent execution title; cancellation is not a save.');
        await input.press('Escape');
        await expect(input).toHaveCount(0);
        await expect(title).toHaveText(data.execution.description);
        await expect(title).toBeFocused();
        await title.press('Enter');
        await input.fill('Keyboard Cancel must not reach blur-save.');
        expect((await input.boundingBox())!.width).toBeGreaterThanOrEqual(160);
        const cancelTitle = page.getByRole('button', { name: 'Cancel editing', exact: true });
        await cancelTitle.focus();
        await cancelTitle.press('Enter');
        await expect(input).toHaveCount(0);
        await expect(title).toBeFocused();
        const detailsDoor = page.locator('#workspace-task-details-edit');
        await detailsDoor.focus();
        await detailsDoor.press('Enter');
        const detailsInput = page.locator('.workspace-task-page-subtitle-input');
        await detailsInput.fill('Unsent extra details; cancel without a PATCH.');
        const cancelDetails = page.getByRole('button', {
          name: 'Cancel editing additional details',
          exact: true
        });
        await cancelDetails.focus();
        await cancelDetails.press('Enter');
        await expect(detailsInput).toHaveCount(0);
        await expect(detailsDoor).toBeFocused();
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)
        ).toBe(true);
        await check();
      }
      expect(
        await (await request.get(`/api/orchestration/tasks?id=${data.execution.id}`)).json()
      ).toEqual(data.execution);
    });
  }

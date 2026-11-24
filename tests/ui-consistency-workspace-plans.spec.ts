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
    test(`Plans index and genuine draft review history preserve authority ${theme} ${width}`, async ({
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
      const index = `/workspaces/${seed.workspace.folder_slug}/plans`;
      await page.goto(index);
      await expect(page.locator('#plan-active-list .plan-card')).toHaveCount(2);
      await expect(page.locator('#plan-history-list .plan-card')).toHaveCount(1);
      // Existing plans are listed whatever planning says; the page has no
      // creation panel and no "turn planning on" banner, and it does not claim
      // the workspace has no plans.
      await expect(page.locator('#plan-disabled-banner')).toHaveCount(0);
      await expect(page.locator('#plan-create-panel')).toHaveCount(0);
      await expect(page.locator('#plan-retired-note')).toBeHidden();
      if (width <= 720) {
        const description = (await page.locator('.plans-subtitle').boundingBox())!;
        const refresh = (await page.locator('#plan-refresh').boundingBox())!;
        expect(refresh.y - description.y - description.height).toBeLessThanOrEqual(24);
      }
      await capture(
        page,
        info,
        'workspace-plans-index',
        {
          workspaceId: seed.workspace.id,
          fixture:
            'Real local draft, immutable review version and archived history; planning remains off'
        },
        { fullPage: false }
      );
      for (const state of ['draft', 'review', 'history']) {
        const record = data.plans[state];
        const row = page.locator(`.plan-card[data-plan-id="${record.id}"]`);
        expect((await contrast(row.locator('.plan-status'))).ratio).toBeGreaterThanOrEqual(4.5);
        const link = row.locator('a.plan-card-link');
        await expect(link).toHaveAttribute('href', `${index}/${record.id}`);
        await link.click();
        await expect(page.locator('#plan-content')).toBeVisible();
        await expect(page.locator('#plan-title')).toHaveText(record.title);
        await expect(page.locator('#plan-status-badge')).not.toContainText('Loading');
        expect((await contrast(page.locator('#plan-status-badge'))).ratio).toBeGreaterThanOrEqual(
          4.5
        );
        await expect(page.locator('#plan-editor-groups')).toContainText('Inspect the brief');
        if (state === 'review') {
          await expect(page.locator('#plan-editor-readonly')).toBeVisible();
          for (const id of ['plan-add-group', 'plan-save', 'plan-request-review'])
            await expect(page.locator(`#${id}`)).toBeDisabled();
          await expect(page.locator('.plan-group-actions')).toBeHidden();
          await expect(page.locator('.plan-item-actions')).toBeHidden();
          await expect(page.locator('#plan-review-panel')).toBeVisible();
          await expect(page.locator('#plan-review-version')).toContainText('1');
          const contractResponse = await request.get(
            `/api/workspaces/${seed.workspace.id}/plans/${record.id}/versions/1`
          );
          expect(contractResponse.ok()).toBe(true);
          const contract = await contractResponse.json();
          expect(contract.starts_execution).toBe(false);
          expect(contract.approvable).toBe(true);
          await expect(page.locator('#plan-approve')).toBeEnabled();
          await expect(page.locator('#plan-approve')).toHaveAttribute(
            'data-starts-execution',
            'false'
          );
          await expect(page.locator('#plan-blocker')).toBeVisible();
        }
        await capture(
          page,
          info,
          `workspace-plan-${state}`,
          {
            workspaceId: seed.workspace.id,
            planId: record.id,
            fixture: 'Real owned metadata only; no generation, approval or materialization'
          },
          { fullPage: false }
        );
        await page.locator(`a[href="${index}"]`).click();
        await expect(page.locator('#plan-active-list .plan-card')).toHaveCount(2);
      }
      await reachable(page.locator('#plan-refresh'), width <= 768 ? 44 : 40);
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)
      ).toBe(true);
      await check();
    });
  }

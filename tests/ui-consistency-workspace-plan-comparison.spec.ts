import { expect, test } from '@playwright/test';
import { capture, preparePage } from './helpers/ui-consistency';
import {
  createWorkspaceSeed,
  populateWorkspaceSeed,
  reviseOwnedPlanFixture,
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

for (const theme of ['light', 'dark'] as const) {
  test(`two real immutable Plan versions expose bounded keyboard comparison ${theme}`, async ({
    page,
    request
  }, info) => {
    seed = await createWorkspaceSeed(request);
    const data = await populateWorkspaceSeed(request, seed);
    const plan = await reviseOwnedPlanFixture(request, seed, data.plans.review.id);
    await freezeWorkspaceSeed(request, seed);
    await preparePage(page, theme, 390);
    await workspaceMetadataReads(page);
    check = await guardWorkspaceReadTour(page, [seed]);
    const base = `/workspaces/${seed.workspace.folder_slug}/plans`;
    await page.goto(`${base}/${plan.id}`);
    await expect(page.locator('#plan-title')).toHaveText(plan.title);
    await expect(page.locator('#plan-review-version')).toHaveText('Version 2');
    await expect(page.locator('#plan-compare-panel')).toBeVisible();
    await expect(page.locator('#plan-compare-list li')).toHaveCount(24);
    await expect(page.locator('#plan-compare-list')).toContainText('Fictional review point 25');
    const comparison = page.getByRole('region', { name: 'Plan changes', exact: true });
    const geometry = await comparison.evaluate(node => ({
      height: node.clientHeight,
      content: node.scrollHeight
    }));
    expect(geometry.height).toBeLessThanOrEqual(384);
    expect(geometry.content).toBeGreaterThan(geometry.height);
    await comparison.focus();
    await comparison.press('PageDown');
    await expect.poll(() => comparison.evaluate(node => node.scrollTop)).toBeGreaterThan(0);
    await capture(
      page,
      info,
      'workspace-plan-real-comparison',
      {
        workspaceId: seed.workspace.id,
        planId: plan.id,
        fixture:
          'Real retained versions 1 and 2, metadata-only request changes/edit/review; original hash/content preserved; no approval/materialization'
      },
      { fullPage: false }
    );
    await page.locator(`a[href="${base}"]`).click();
    await expect(page.locator('#plan-active-list .plan-card')).toHaveCount(2);
    await page.goBack();
    await expect(page.locator('#plan-review-version')).toHaveText('Version 2');
    await expect(page.locator('#plan-compare-list li')).toHaveCount(24);
    await check();
  });
}

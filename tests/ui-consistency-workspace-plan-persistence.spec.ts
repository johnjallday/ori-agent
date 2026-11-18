import { expect, test } from '@playwright/test';
import { capture, preparePage } from './helpers/ui-consistency';
import {
  createWorkspaceSeed,
  populateWorkspaceSeed,
  freezeWorkspaceSeed,
  checkpointOwnedPlanDraft,
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
  test(`Plan structural draft autosave survives reload without changing the review version ${theme}`, async ({
    page,
    request
  }, info) => {
    seed = await createWorkspaceSeed(request);
    const data = await populateWorkspaceSeed(request, seed);
    await freezeWorkspaceSeed(request, seed);
    const original = data.plans.draft;
    const reviewPath = `/api/workspaces/${seed.workspace.id}/plans/${data.plans.review.id}/versions/1`;
    const retained = await (await request.get(reviewPath)).json();
    await preparePage(page, theme, 390);
    await workspaceMetadataReads(page);
    check = await guardWorkspaceReadTour(page, [seed]);
    const path = `/api/workspaces/${seed.workspace.id}/plans/${original.id}/draft`;
    let writes = 0;
    let expectedDraft: Record<string, any> | undefined;
    await page.route(`**${path}`, async route => {
      expect(route.request().method()).toBe('PATCH');
      expect(++writes).toBe(1);
      const body = route.request().postDataJSON();
      const added = body.content.groups[0].items[1];
      expect(added.id).toMatch(/^itm_local_[a-z0-9]+$/);
      expect(added.id).not.toBe(original.draft.groups[0].items[0].id);
      expectedDraft = structuredClone(original.draft);
      expectedDraft!.groups[0].items.push({
        ...original.draft.groups[0].items[0],
        id: added.id,
        description: `${original.draft.groups[0].items[0].description} (copy)`
      });
      expect(body).toEqual({
        title: original.title,
        objective: original.objective,
        content: expectedDraft,
        revision: original.draft_revision,
        autosave: true
      });
      // Exact owned metadata write only, using the real service and response.
      return route.continue();
    });
    const detail = `/workspaces/${seed.workspace.folder_slug}/plans/${original.id}`;
    await page.goto(detail);
    await expect(page.locator('#plan-title')).toHaveText(original.title);
    await expect(page.locator('#plan-editor-groups .plan-item')).toHaveCount(1);
    const saved = page.waitForResponse(
      response =>
        new URL(response.url()).pathname === path && response.request().method() === 'PATCH'
    );
    await page.locator('[data-action="item-duplicate"]').focus();
    await page.locator('[data-action="item-duplicate"]').press('Enter');
    const response = await saved;
    expect(response.ok()).toBe(true);
    await checkpointOwnedPlanDraft(request, seed, {
      id: original.id,
      draft: expectedDraft!,
      revision: original.draft_revision + 1
    });
    await expect(
      page.locator('.plan-item[data-item-id="i1"] [data-action="item-duplicate"]')
    ).toBeFocused();
    await expect(page.locator('#plan-save-state')).toContainText('All changes saved');
    await page.reload();
    await expect(page.locator('#plan-editor-groups .plan-item')).toHaveCount(2);
    await expect(page.locator('#plan-editor-groups')).toContainText('(copy)');
    expect(await (await request.get(reviewPath)).json()).toEqual(retained);
    expect(writes).toBe(1);
    await page.locator('#plan-editor-groups').scrollIntoViewIfNeeded();
    await capture(
      page,
      info,
      'workspace-plan-real-draft-persisted',
      {
        workspaceId: seed.workspace.id,
        planId: original.id,
        fixture:
          'Genuine owned revision-fenced autosave; separate immutable review contract unchanged; no approvals or materialization'
      },
      { fullPage: false }
    );
    await check();
  });
}

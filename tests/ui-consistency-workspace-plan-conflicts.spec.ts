import { test, expect } from '@playwright/test';
import { preparePage, capture } from './helpers/ui-consistency';
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
import { boundedCanvasControl as bounded, assertCanvasAxe } from './helpers/ui-consistency-canvas';

let seed: WorkspaceSeed | undefined, check: (() => Promise<void>) | undefined;
test.afterEach(async ({ page, request }) => {
  test.setTimeout(120_000);
  await page.goto('about:blank');
  try {
    if (check) await check();
  } finally {
    // This helper checks exact owned content before retirement. These fresh
    // scratch plans have NO approvals; retained approved fixtures are unrelated.
    if (seed) await retireWorkspaceSeed(request, seed);
    seed = undefined;
    check = undefined;
  }
});
for (const theme of ['light', 'dark'] as const)
  for (const choice of ['keep', 'discard'] as const)
    test(`real stale Plan draft ${choice} preserves the chosen content and focus ${theme}`, async ({
      page,
      request
    }, info) => {
      test.setTimeout(120_000);
      seed = await createWorkspaceSeed(request);
      const data = await populateWorkspaceSeed(request, seed);
      await freezeWorkspaceSeed(request, seed);
      const original = data.plans.draft,
        base = `/api/workspaces/${seed.workspace.id}/plans/${original.id}`,
        path = `${base}/draft`;
      const reviewPath = `/api/workspaces/${seed.workspace.id}/plans/${data.plans.review.id}/versions/1`;
      const retained = await (await request.get(reviewPath)).json();
      await preparePage(page, theme, 390);
      await workspaceMetadataReads(page);
      check = await guardWorkspaceReadTour(page, [seed]);
      const winner = structuredClone(original.draft);
      winner.rationale = `${winner.rationale} Explicit concurrent metadata edit.`;
      let mine: any,
        writes = 0;
      await page.route(`**${path}`, async route => {
        expect(route.request().method()).toBe('PATCH');
        const body = route.request().postDataJSON();
        writes++;
        if (writes === 1) {
          const added = body.content.groups[0].items[1];
          expect(added.id).toMatch(/^itm_local_[a-z0-9]+$/);
          mine = structuredClone(original.draft);
          mine.groups[0].items.push({
            ...mine.groups[0].items[0],
            id: added.id,
            description: `${mine.groups[0].items[0].description} (copy)`
          });
          expect(body).toEqual({
            title: original.title,
            objective: original.objective,
            content: mine,
            revision: original.draft_revision,
            autosave: true
          });
          // Real concurrent save first. The browser's now-stale request then
          // reaches the real backend and receives its genuine revision refusal.
          const concurrent = await request.patch(path, {
            data: {
              title: original.title,
              objective: original.objective,
              content: winner,
              revision: original.draft_revision
            }
          });
          expect(concurrent.ok()).toBe(true);
          await checkpointOwnedPlanDraft(request, seed!, {
            id: original.id,
            draft: winner,
            revision: original.draft_revision + 1
          });
          const response = await route.fetch();
          expect(response.status()).toBe(409);
          expect((await response.json()).code).toBe('stale_draft');
          return route.fulfill({ response });
        }
        expect(choice).toBe('keep');
        expect(writes).toBe(2);
        expect(body).toEqual({
          title: original.title,
          objective: original.objective,
          content: mine,
          revision: original.draft_revision + 1,
          autosave: false
        });
        const response = await route.fetch();
        expect(response.ok()).toBe(true);
        await checkpointOwnedPlanDraft(request, seed!, {
          id: original.id,
          draft: mine,
          revision: original.draft_revision + 2
        });
        return route.fulfill({ response });
      });
      await page.goto(`/workspaces/${seed.workspace.folder_slug}/plans/${original.id}`);
      const duplicate = page.locator('[data-action="item-duplicate"]');
      await expect(duplicate).toHaveCount(1);
      await duplicate.focus();
      await duplicate.press('Enter');
      await expect(page.locator('#plan-save-state')).toHaveAttribute('data-state', 'conflicted');
      await expect(page.locator('#plan-conflict-panel')).toBeVisible();
      await expect(page.locator('#plan-editor-groups .plan-item')).toHaveCount(2);
      const server = await (await request.get(base)).json();
      expect(server.draft).toEqual(winner);
      const keep = page.locator('#plan-conflict-keep-mine'),
        discard = page.locator('#plan-conflict-discard');
      await bounded(keep);
      await bounded(discard);
      await assertCanvasAxe(page, '#plan-content');
      await capture(
        page,
        info,
        `workspace-plan-real-conflict-${choice}`,
        {
          workspaceId: seed.workspace.id,
          planId: original.id,
          fixture:
            'Real revision-fenced concurrent metadata PATCH followed by genuine 409; both contents retained for explicit choice, no approvals or effects.'
        },
        { fullPage: false }
      );
      const decision = choice === 'keep' ? keep : discard;
      await decision.focus();
      await decision.press('Enter');
      await expect(page.locator('#plan-conflict-panel')).toBeHidden();
      await expect(page.locator('#plan-save')).toBeFocused();
      await expect(page.locator('#plan-save-state')).toContainText('All changes saved');
      await expect(page.locator('#plan-editor-groups .plan-item')).toHaveCount(
        choice === 'keep' ? 2 : 1
      );
      expect(writes).toBe(choice === 'keep' ? 2 : 1);
      const final = await (await request.get(base)).json();
      expect(final.draft).toEqual(choice === 'keep' ? mine : winner);
      expect(await (await request.get(reviewPath)).json()).toEqual(retained);
      await page.reload();
      await expect(page.locator('#plan-editor-groups .plan-item')).toHaveCount(
        choice === 'keep' ? 2 : 1
      );
      await check();
    });

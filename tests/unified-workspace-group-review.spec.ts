import { expect, test, type APIRequestContext } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { startJanitorBuildModel } from './helpers/janitor-build-model';

// Real owner/journal/roster/layout acceptance on an isolated branch server.
// The loopback fixture supplies a model catalog only; no inference is needed.
test.describe('ordinary Group shared review', () => {
  test.skip(process.env.ORI_TEST_REVIEW_GROUPS !== '1', 'requires isolated loopback catalog demo');
  let model: Awaited<ReturnType<typeof startJanitorBuildModel>>;
  test.beforeAll(async ({ request }) => {
    model = await startJanitorBuildModel();
    const result = await request.post('/api/settings/system-model', {
      data: { provider: 'lmstudio', model: 'ori-synthetic-model' }
    });
    expect(result.ok(), await result.text()).toBeTruthy();
  });
  test.afterAll(async () => {
    await model?.();
  });

  async function snapshot(request: APIRequestContext) {
    const workspaces = await (await request.get('/api/workspaces')).json();
    const agents = await (await request.get('/api/agents')).json();
    return {
      ids: workspaces.folders.map((item: { id: string }) => item.id).sort(),
      agents: agents.agents.map((item: { name: string }) => item.name).sort(),
      layout: await (await request.get('/api/workspace-map/layout')).json()
    };
  }

  for (const map of [false, true])
    test(`reviewed Group creates its exact Manager and no members through ${map ? 'a group frame' : 'automatic placement'}`, async ({
      page,
      request
    }) => {
      const skipped = await request.post('/api/onboarding/skip');
      expect(skipped.ok(), await skipped.text()).toBeTruthy();
      await page.goto('/');
      const before = await snapshot(request),
        calls = model.calls();
      await page.locator('#cockpitCreateWorkspaceBtn').click();
      await page.locator('#workspaceCreatorKindGroup').check();
      await page.locator('#wizardNextBtn').click();
      const name = `Reviewed Group ${randomUUID().slice(0, 8)}`;
      await page.locator('#folderNameInput').fill(name);
      await page.locator('#folderDescriptionInput').fill('Original Group purpose');
      await page.locator('#wizardNextBtn').click();
      await expect(page.locator('#wizardStep3')).toBeVisible();
      await expect(page.locator('#workspaceTeamHeading')).toHaveText('Group roster');
      await page.locator('#workspaceTeamRoster [data-team-agent-setup]').click();
      await expect(page.locator('#addAgentModal')).toBeVisible();
      await page.locator('#createAgentBtn').click();
      await expect(page.locator('#addAgentModal')).toBeHidden();
      await expect(page.locator('#wizardNextBtn')).toBeEnabled();
      await page.locator('#wizardNextBtn').click();
      const review = page.locator('#workspaceCreationReviewPanel');
      await expect(review).toBeVisible();
      await expect(review).toContainText('Group Manager');
      await expect(review).toContainText('does not grant access to member workspaces');
      const purpose = 'Reviewed Group purpose. 日本語';
      await review.locator('[data-placement-field="mission"]').fill(purpose);
      await review.locator('[data-placement-field="mission"]').blur();
      expect(await snapshot(request)).toEqual(before);
      expect(model.calls()).toBe(calls);
      let candidate: { x: number; y: number } | null = null;
      if (map) {
        await review.getByRole('button', { name: 'Place & create on map', exact: true }).click();
        await expect(page.locator('#addFolderModal')).toBeHidden();
        await expect(page.locator('.ws-map-placement-preview--group')).toBeVisible();
        expect(await snapshot(request)).toEqual(before);
        await page.keyboard.press('Escape');
        await expect(review).toBeVisible();
        await expect(review.locator('[data-placement-field="mission"]')).toHaveValue(purpose);
        await review.getByRole('button', { name: 'Place & create on map', exact: true }).click();
        await expect(page.locator('#addFolderModal')).toBeHidden();
        const canvas = page.locator('[data-ws-map-viewport]').filter({ visible: true }).first();
        const confirm = page.locator('[data-ws-map-placement-confirm]');
        await canvas.focus();
        for (let n = 0; n < 30 && !(await confirm.isEnabled()); n++)
          await canvas.press('ArrowDown');
        await expect(confirm).toHaveText('Create group here');
        await expect(confirm).toBeEnabled();
        // The map owns its initial suggestion; the creator's candidate is
        // updated only after a move. Assert the actual displayed frame, even
        // when its first suggestion fits without any keyboard nudges.
        const frame = page.locator('.ws-map-placement-preview--group');
        candidate = await frame.evaluate(element => ({
          x: Number.parseFloat((element as HTMLElement).style.left),
          y: Number.parseFloat((element as HTMLElement).style.top)
        }));
        expect(candidate).toEqual({ x: expect.any(Number), y: expect.any(Number) });
        await expect(frame).toHaveCSS('width', '176px');
        await expect(frame).toHaveCSS('height', '170px');
        await canvas.press('Enter');
      } else {
        await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
        await review.getByRole('button', { name: 'Create group', exact: true }).click();
      }
      await expect(
        review.getByRole('heading', { name: 'Group created', exact: true })
      ).toBeVisible();
      const result = await page.evaluate(
        () => (window as any).sessionManager.workspaceCreatorContext.ordinaryReview.result
      );
      const canonical = await (await request.get(`/api/workspaces/${result.id}`)).json();
      expect(canonical.kind).toBe('group');
      expect(canonical.name).toBe(name);
      expect(canonical.description).toBe(purpose);
      expect(canonical.agent_instances).toHaveLength(1);
      expect(canonical.agent_instances[0].entry_point).toBe(true);
      expect(canonical.shared_data.workspace_creation_operation_id).toBeTruthy();
      const layout = (await (await request.get('/api/workspace-map/layout')).json()).layout;
      if (map) expect(layout.positions[result.id]).toEqual(candidate);
      else expect(layout.positions[result.id]).toBeUndefined();
      const listed = (await (await request.get('/api/workspaces')).json()).folders;
      expect(
        listed.filter((item: { parent_id?: string }) => item.parent_id === result.id)
      ).toHaveLength(0);
      expect(
        listed
          .filter((item: { id: string }) => !before.ids.includes(item.id))
          .map((item: { id: string }) => item.id)
      ).toEqual([result.id]);
      expect(model.calls()).toBe(calls);
    });
});

import { expect, test } from '@playwright/test';
import { capture, contrast, preparePage, reachable } from './helpers/ui-consistency';
import {
  createWorkspaceSeed,
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
  for (const width of [320, 1440]) {
    test(`workspace native Give work and delete cancellation ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      seed = await createWorkspaceSeed(request);
      await freezeWorkspaceSeed(request, seed);
      await preparePage(page, theme, width);
      await workspaceMetadataReads(page);
      check = await guardWorkspaceReadTour(page, [seed]);
      await page.goto(`/workspaces/${seed.workspace.folder_slug}`);
      await page.waitForFunction(
        () => (window as any).workspaceDetail?.initialListsLoaded === true
      );
      const door = page.locator('[data-cmd-give-work]');
      await door.click();
      const give = page.locator('dialog.give-work');
      await expect(give).toBeVisible();
      await capture(
        page,
        info,
        'workspace-give-work',
        {
          workspaceId: seed.workspace.id,
          behavior: 'native dialog; no create/start request allowed'
        },
        { fullPage: false }
      );
      await expect(give).toHaveClass(/\bui-section\b/);
      expect((await contrast(give)).ratio).toBeGreaterThanOrEqual(4.5);
      await give.locator('[data-give-work-submit]').click();
      await expect(give.locator('[data-give-work-status]')).toHaveText('Say what needs doing.');
      const what = give.locator('[name="what"]');
      await expect(what).toBeFocused();
      await what.fill('Retain this draft while choosing who could do it.');
      await give
        .locator('[name="details"]')
        .fill('Fictional draft only; never submit this valid request.');
      const start = give.locator('[name="startNow"]');
      const who = give.locator('[name="who"]');
      await expect(start).toBeDisabled();
      await expect(start).not.toBeChecked();
      const names = await who
        .locator('option')
        .evaluateAll(options => options.map(option => option.value).filter(Boolean));
      expect(names.length).toBeGreaterThan(0);
      await who.selectOption(names[0]);
      await expect(start).toBeEnabled();
      await start.check();
      await who.selectOption('');
      await expect(start).toBeDisabled();
      await expect(start).not.toBeChecked();
      await expect(what).toHaveValue('Retain this draft while choosing who could do it.');
      await reachable(give.locator('[data-give-work-cancel]'), width <= 768 ? 44 : 40);
      await capture(
        page,
        info,
        'workspace-give-work-invalid-draft',
        {
          workspaceId: seed.workspace.id,
          behavior: 'actual empty-submit validation; retained unsent draft; no execution'
        },
        { fullPage: false }
      );
      await what.press('Escape');
      await expect(give).toHaveCount(0);
      await expect(door).toBeFocused();

      // Exercise the real Home Tree bulk controller, with only this exact owned
      // row selected. Cancel before the controller is authorized to DELETE.
      await page.goto('/?view=tree');
      const tree = page.locator('#cockpitTree');
      await expect(tree).toBeVisible();
      const row = tree.locator(`[data-tree-row="${seed.workspace.id}"]`);
      await expect(row).toBeVisible();
      await row.locator('.cockpit-tree-name').click({ modifiers: ['ControlOrMeta'] });
      await expect(row).toHaveAttribute('aria-checked', 'true');
      const remove = page.locator('[data-tree-delete-selected]');
      await remove.click();
      const deletion = page.locator('dialog.ws-delete-dialog');
      await expect(deletion).toBeVisible();
      await expect(deletion.locator('h2')).toContainText(seed.workspace.name);
      await capture(
        page,
        info,
        'workspace-delete-cancel',
        {
          workspaceId: seed.workspace.id,
          behavior: 'real native Tree delete confirmation; cancelled, not persisted deletion'
        },
        { fullPage: false }
      );
      await expect(deletion).toHaveClass(/\bui-section\b/);
      expect((await contrast(deletion)).ratio).toBeGreaterThanOrEqual(4.5);
      expect(
        (await contrast(deletion.locator('.ws-delete-dialog__copy').first())).ratio
      ).toBeGreaterThanOrEqual(4.5);
      const cancel = deletion.getByRole('button', { name: 'Cancel', exact: true });
      await expect(cancel).toBeFocused();
      await reachable(cancel, width <= 768 ? 44 : 40);
      await cancel.press('Escape');
      await expect(deletion).toHaveCount(0);
      await expect(remove).toBeFocused();
      expect((await request.get(`/api/workspaces/${seed.workspace.id}`)).ok()).toBe(true);
      await check();
    });
  }

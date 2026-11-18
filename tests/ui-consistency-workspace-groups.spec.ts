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
  workspaceMetadataReads,
  simulatedHQRead
} from './fixtures/ui-consistency-workspace';

async function verifyMoreViews(page: import('@playwright/test').Page, base: string, width: number) {
  const disclosure = page.locator('[data-cmd-disclosure="views"]');
  const summary = disclosure.locator('summary');
  await summary.click();
  await expect(disclosure).toHaveAttribute('open', '');
  for (const destination of ['plans', 'canvas']) {
    const link = disclosure.locator(`a[href="${base}/${destination}"]`);
    await reachable(link, width <= 768 ? 44 : 32);
    expect(
      await link.evaluate(node => {
        const box = node.getBoundingClientRect();
        return [0.25, 0.75].every(x =>
          [0.25, 0.75].every(y => {
            const hit = document.elementFromPoint(
              box.left + box.width * x,
              box.top + box.height * y
            );
            return hit === node || node.contains(hit);
          })
        );
      }),
      'destination must paint above map content'
    ).toBe(true);
  }
  await summary.press('Escape');
  await expect(disclosure).not.toHaveAttribute('open');
  await expect(summary).toBeFocused();
}

let seeds: WorkspaceSeed[] = [];
let check: (() => Promise<void>) | undefined;
test.afterEach(async ({ page, request }) => {
  test.setTimeout(120_000);
  await page.goto('about:blank');
  try {
    if (check) await check();
  } finally {
    for (const seed of [...seeds].reverse()) await retireWorkspaceSeed(request, seed);
    seeds = [];
    check = undefined;
  }
});
for (const theme of ['light', 'dark'] as const)
  for (const width of [320, 1440]) {
    test(`workspace group map, HQ projection and delete choices ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      const group = await createWorkspaceSeed(request, 'group');
      seeds.push(group);
      const member = await createWorkspaceSeed(request, 'workspace', group);
      seeds.push(member);
      expect(
        (await (await request.get(`/api/workspaces/${member.workspace.id}`)).json()).parent_id
      ).toBe(group.workspace.id);
      for (const seed of seeds) await freezeWorkspaceSeed(request, seed);
      await preparePage(page, theme, width);
      await workspaceMetadataReads(page);
      check = await guardWorkspaceReadTour(page, seeds);
      await page.goto(`/workspaces/${group.workspace.folder_slug}?mode=map`);
      const map = page.locator('.ws-cmd-opmap.is-combined');
      const node = map.locator(`[data-ws-id="${member.workspace.id}"]`);
      await expect(node).toBeVisible();
      await node.click();
      await map.locator('[data-map-zoom-out]').click();
      const placement = await node.evaluate(element => ({
        left: (element as HTMLElement).style.left,
        top: (element as HTMLElement).style.top
      }));
      expect(placement.left).not.toBe('');
      expect(placement.top).not.toBe('');
      const before = await page.evaluate(() => ({
        camera: (window as any).OriWorkspaceMap.getCamera(),
        selected: (window as any).OriWorkspaceMap.getSelectedId()
      }));
      expect(before.selected).toBe(member.workspace.id);
      for (const mode of ['details', 'tickets']) {
        await page.locator(`[data-cmd-view-mode="${mode}"]`).click();
        await expect(page.locator(`[data-cmd-view-mode="${mode}"]`)).toHaveAttribute(
          'aria-pressed',
          'true'
        );
        await page.locator('[data-cmd-view-mode="map"]').click();
        await expect(node).toBeVisible();
        await expect
          .poll(() =>
            page.evaluate(() => ({
              camera: (window as any).OriWorkspaceMap.getCamera(),
              selected: (window as any).OriWorkspaceMap.getSelectedId()
            }))
          )
          .toEqual(before);
        expect(
          await node.evaluate(element => ({
            left: (element as HTMLElement).style.left,
            top: (element as HTMLElement).style.top
          }))
        ).toEqual(placement);
      }
      await verifyMoreViews(page, `/workspaces/${group.workspace.folder_slug}`, width);
      await page.locator('[data-cmd-map-resources]').click();
      const resources = page.getByRole('dialog', { name: 'Resources', exact: true });
      await expect(resources).toBeVisible();
      await resources.locator('[data-panel-close]').press('Escape');
      await expect(resources).toBeHidden();
      await expect(page.locator('[data-cmd-map-resources]')).toBeFocused();
      await capture(
        page,
        info,
        'workspace-group-map-state',
        {
          workspaceId: group.workspace.id,
          memberId: member.workspace.id,
          persistence:
            'real membership; actual camera and selected member survived native view round trips'
        },
        { fullPage: false }
      );

      await simulatedHQRead(page, member);
      await page.goto(`/workspaces/${member.workspace.folder_slug}?mode=map`);
      await expect(page.locator('[data-cmd-view-mode="map"]')).toHaveAttribute(
        'aria-pressed',
        'true'
      );
      await expect(page.locator('.ws-cmd-map-shell.is-hq')).toBeVisible();
      await verifyMoreViews(page, `/workspaces/${member.workspace.folder_slug}`, width);
      await capture(
        page,
        info,
        'workspace-hq-map-chrome',
        {
          workspaceId: member.workspace.id,
          fixture:
            'designation-only GET projection, not hired/provisioned HQ or server-only HQ dialogs'
        },
        { fullPage: false }
      );

      await page.goto('/?view=tree');
      const row = page.locator(`#cockpitTree [data-tree-row="${group.workspace.id}"]`);
      await expect(row).toBeVisible();
      // Desktop reveals its row action on hover; touch layouts keep it visible.
      await row.hover();
      await row.locator(`[data-tree-menu-for="${group.workspace.id}"]`).click();
      await page.getByRole('menuitem', { name: 'Delete', exact: true }).click();
      const dialog = page.locator('dialog.ws-delete-dialog');
      await expect(dialog).toBeVisible();
      await expect(dialog.locator('.ws-delete-dialog__legend')).toContainText('1 workspace');
      const keepMembers = dialog.locator('input[value="group_only"]');
      const allContents = dialog.locator('input[value="contents"]');
      await expect(keepMembers).toBeChecked();
      await expect(allContents).not.toBeChecked();
      await allContents.check();
      await expect(dialog.locator('.ws-delete-dialog__mode.is-selected')).toContainText(
        'everything inside'
      );
      for (const mode of await dialog.locator('.ws-delete-dialog__mode').all()) {
        await reachable(mode, width <= 768 ? 44 : 0);
        const glyph = await mode.locator('input').boundingBox();
        expect(glyph!.width, 'native radio must not shrink into an ellipse').toBeGreaterThanOrEqual(
          glyph!.height - 1
        );
        expect((await contrast(mode.locator('strong'))).ratio).toBeGreaterThanOrEqual(4.5);
        expect((await contrast(mode.locator('small'))).ratio).toBeGreaterThanOrEqual(4.5);
      }
      await capture(
        page,
        info,
        'workspace-group-delete-choice',
        {
          workspaceId: group.workspace.id,
          memberId: member.workspace.id,
          behavior:
            'real group confirmation; safe default verified, destructive alternative selected but never confirmed'
        },
        { fullPage: false }
      );
      await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expect(dialog).toHaveCount(0);
      await expect(row).toBeFocused();
      expect(
        (await (await request.get(`/api/workspaces/${member.workspace.id}`)).json()).parent_id
      ).toBe(group.workspace.id);
      await check();
    });
  }

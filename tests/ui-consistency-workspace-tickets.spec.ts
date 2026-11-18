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
    test(`workspace Tickets list board and native detail ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      seed = await createWorkspaceSeed(request);
      const data = await populateWorkspaceSeed(request, seed);
      await freezeWorkspaceSeed(request, seed);
      await preparePage(page, theme, width);
      await workspaceMetadataReads(page);
      check = await guardWorkspaceReadTour(page, [seed]);
      const base = `/workspaces/${seed.workspace.folder_slug}`;
      await page.goto(`${base}?mode=details`);
      await page.waitForFunction(
        () => (window as any).workspaceDetail?.initialListsLoaded === true
      );
      await page.locator('[data-cmd-view-mode="tickets"]').click();
      await expect(page.locator('[data-cmd-view-mode="tickets"]')).toHaveAttribute(
        'aria-pressed',
        'true'
      );
      const card = page.locator(
        `#hubTicketsList [data-ticket-id="${data.ticket.id}"] .ticket-card-open`
      );
      await expect(card).toBeVisible();
      const chromeGap = await page.evaluate(
        () =>
          document.querySelector('.hub-tickets-header')!.getBoundingClientRect().top -
          document.querySelector('#workspaceCommandView .ws-cmd-compact')!.getBoundingClientRect()
            .bottom
      );
      expect(
        chromeGap,
        'header-only Tickets must not retain the other modes’ empty launcher footer'
      ).toBeLessThanOrEqual(48);
      const badge = card.locator('.ticket-state-chip');
      expect(await badge.evaluate(node => parseFloat(getComputedStyle(node).borderTopWidth))).toBe(
        1
      );
      expect((await contrast(badge)).ratio).toBeGreaterThanOrEqual(4.5);
      const minimum = width <= 768 ? 44 : 32;
      const list = page.locator('#hubTicketsViewList');
      const board = page.locator('#hubTicketsViewBoard');
      for (const control of [
        list,
        board,
        page.locator('#hubTicketsRefreshBtn'),
        page.locator('[data-ticket-state-filter="all"]'),
        page.locator('[data-ticket-state-filter="backlog"]')
      ]) {
        await control.evaluate(node =>
          node.scrollIntoView({ block: 'center', behavior: 'instant' })
        );
        await reachable(control, minimum);
      }
      await expect(list).toHaveClass(/\bnav-link\b/);
      await expect(board).toHaveClass(/\bnav-link\b/);
      await expect(page.locator('[data-ticket-state-filter="all"]')).toHaveClass(/\bui-filter\b/);
      await expect(list).toHaveAttribute('aria-selected', 'true');
      const query = page.locator('#hubTicketsSearch');
      await query.fill('review the long workshop brief');
      await expect(page.locator('#hubTicketsList > li')).toHaveCount(1);
      await expect(query).toBeFocused();
      await query.fill('');
      await expect(card).toBeVisible();
      await page.locator('[data-ticket-state-filter="backlog"]').click();
      await expect(page.locator('[data-ticket-state-filter="backlog"]')).toHaveAttribute(
        'aria-pressed',
        'true'
      );
      await expect(card).toBeVisible();
      await capture(page, info, 'workspace-ticket-list', {
        workspaceId: seed.workspace.id,
        ticketId: data.ticket.id,
        records: 'real owned manual Backlog and saved execution definitions; no writes'
      });
      await page.locator('[data-ticket-state-filter="all"]').click();
      await board.click();
      await expect(board).toHaveAttribute('aria-selected', 'true');
      await expect(page.locator('#hubTicketsBoard')).toBeVisible();
      await expect(page.locator('#hubTicketsList')).toBeHidden();
      await expect(page.locator('#hubTicketsBoard .ticket-board-column')).toHaveCount(5);
      const boardRegion = page.locator('#hubTicketsBoard');
      expect(await boardRegion.evaluate(node => node.scrollWidth >= node.clientWidth)).toBe(true);
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)
      ).toBe(true);
      await capture(page, info, 'workspace-ticket-board', {
        workspaceId: seed.workspace.id,
        ticketId: data.ticket.id,
        records: 'same loaded real tickets; fixed lifecycle columns; no drag/start'
      });
      await list.click();
      await card.click();
      const detail = page.locator('#hubTicketDetail');
      await expect(detail).toBeVisible();
      await expect(page).toHaveURL(new RegExp(`ticket=${data.ticket.id}`));
      await expect(page.locator('#hubTicketDetailTitle')).toContainText(data.ticket.title);
      await page
        .locator('#hubTicketDetailClose')
        .evaluate(node => node.scrollIntoView({ block: 'center', behavior: 'instant' }));
      await reachable(page.locator('#hubTicketDetailClose'), minimum);
      const edit = detail.locator('.ticket-detail-edit');
      await edit.locator('summary').click();
      const title = edit.locator('[name="title"]');
      await title.fill('Unsent local ticket edit; close means no save.');
      await title.focus();
      const refreshed = page.waitForResponse(
        response =>
          response.request().method() === 'GET' &&
          new URL(response.url()).pathname === `/api/workspaces/${seed!.workspace.id}/tickets`
      );
      await page.locator('#hubTicketsRefreshBtn').click();
      await refreshed;
      await expect(edit).toHaveAttribute('open', '');
      await expect(title).toHaveValue('Unsent local ticket edit; close means no save.');
      expect((await contrast(title)).ratio).toBeGreaterThanOrEqual(4.5);
      await capture(
        page,
        info,
        'workspace-ticket-draft',
        {
          workspaceId: seed.workspace.id,
          ticketId: data.ticket.id,
          behavior: 'native detail edit disclosure; unsaved local draft only'
        },
        { fullPage: false }
      );
      await page.locator('#hubTicketDetailClose').click();
      await expect(detail).toBeHidden();
      expect(
        (
          await (
            await request.get(`/api/workspaces/${seed.workspace.id}/tickets/${data.ticket.id}`)
          ).json()
        ).title
      ).toBe(data.ticket.title);
      await card.click();
      await expect(edit).not.toHaveAttribute('open');
      await edit.locator('summary').click();
      await expect(title).toHaveValue(data.ticket.title);
      await check();
    });
  }

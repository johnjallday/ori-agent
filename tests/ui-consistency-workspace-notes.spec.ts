import { expect, test, type Locator } from '@playwright/test';
import { capture, preparePage, reachable } from './helpers/ui-consistency';
import {
  createWorkspaceSeed,
  populateWorkspaceSeed,
  freezeWorkspaceSeed,
  checkpointOwnedNoteContent,
  retireWorkspaceSeed,
  type WorkspaceSeed
} from './helpers/ui-consistency-workspace';
import {
  guardWorkspaceReadTour,
  workspaceMetadataReads
} from './fixtures/ui-consistency-workspace';

async function noteControlBounds(control: Locator) {
  await control.focus();
  expect(
    await control.evaluate(node => {
      const r = node.getBoundingClientRect();
      if (r.left < 0 || r.top < 0 || r.right > innerWidth + 1 || r.bottom > innerHeight + 1)
        return false;
      for (const selector of ['.navbar', '.personal-assistant-launcher']) {
        const b = document.querySelector(selector)?.getBoundingClientRect();
        if (
          b &&
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
  ).toBe(true);
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
    test(`workspace and focused Notes retain live editor rail and linked ticket ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      seed = await createWorkspaceSeed(request);
      const data = await populateWorkspaceSeed(request, seed);
      await freezeWorkspaceSeed(request, seed);
      const original = seed.records.find(record => record.kind === 'note')!.value;
      const content = `${original.content}\n\n${Array.from({ length: 50 }, (_, i) => `## Review point ${i + 1}\n\nFictional long note paragraph ${i + 1}. Preserve the local draft and the chosen artwork.`).join('\n\n')}`;
      const updated = await request.patch(`/api/notes/${data.noteId}`, {
        data: { name: original.name, content }
      });
      expect(updated.ok()).toBe(true);
      const note = await checkpointOwnedNoteContent(request, seed, { id: data.noteId, content });
      await preparePage(page, theme, width);
      await workspaceMetadataReads(page);
      check = await guardWorkspaceReadTour(page, [seed]);
      for (const [mode, route] of [
        ['workspace', `/workspaces/${seed.workspace.folder_slug}/notes`],
        ['focused', `/notes/${data.noteId}`]
      ]) {
        await page.goto(route);
        await expect(page.locator('#noteNameInput')).toHaveValue(note.name);
        await expect(page.locator('#notePreviewContent')).toContainText('Studio observations');
        await expect(page.locator('#noteMainContent')).toHaveAttribute('data-note-id', data.noteId);
        await expect(page.locator('#notePageTickets')).toBeVisible();
        const ticketLink = page.locator('#notePageTicketsList a');
        await expect(ticketLink).toHaveCount(1);
        await expect(ticketLink).toContainText(data.ticket.title);
        await expect(ticketLink).toHaveAttribute('href', new RegExp(`ticket=${data.ticket.id}`));
        await capture(
          page,
          info,
          `workspace-note-${mode}`,
          {
            workspaceId: seed.workspace.id,
            noteId: data.noteId,
            fixture:
              'Genuine saved note and referencing manual ticket; no assistant, generation or edit writes'
          },
          { fullPage: false }
        );
        const lastLine = page.locator('#notePreviewContent .note-live-line-rendered').last();
        await expect(lastLine).toContainText('Fictional long note paragraph 50');
        await noteControlBounds(lastLine);
        await capture(
          page,
          info,
          `workspace-note-${mode}-last-line`,
          {
            workspaceId: seed.workspace.id,
            noteId: data.noteId,
            fixture: 'Real 50-section note; native keyboard scrolling above live navbar/assistant'
          },
          { fullPage: false }
        );
        await reachable(page.locator('#noteTocToggle'), width <= 768 ? 44 : 32);
        await noteControlBounds(page.locator('#noteTocToggle'));
        if (mode === 'workspace') {
          await noteControlBounds(page.locator('#notePageNewTabBtn'));
          await noteControlBounds(page.locator('#notePageSplitRightBtn'));
          await page.locator('#notePageSplitRightBtn').press('Enter');
          await expect(page.locator('#notePageSecondaryPane')).toBeVisible();
          await expect(page.locator('#notePageSecondaryBanner')).toContainText('Already open');
          await expect(page.locator('#notePageSecondaryPreview')).toHaveAttribute(
            'aria-readonly',
            'true'
          );
          await expect(page.locator('#notePageUnsplitBtn')).toBeFocused();
          await noteControlBounds(page.locator('#notePageUnsplitBtn'));
          await page.locator('#notePageUnsplitBtn').press('Enter');
          await expect(page.locator('#notePageSecondaryPane')).toBeHidden();
          await expect(page.locator('#notePageSplitRightBtn')).toBeFocused();
        }
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)
        ).toBe(true);
        const sidebar = page.locator('#noteTocToggle');
        if (!(await page.locator('#noteRailTabOutline').isVisible())) await sidebar.click();
        await page.locator('#noteRailTabOutline').focus();
        await page.locator('#noteRailTabOutline').press('Enter');
        await expect(page.locator('#noteRailTabOutline')).toHaveAttribute('aria-selected', 'true');
        await expect(page.locator('#noteRailPaneOutline')).toContainText('Studio observations');
        if (mode === 'workspace') {
          await page.locator('#noteRailTabNotes').click();
          await expect(page.locator('#noteRailTabNotes')).toHaveAttribute('aria-selected', 'true');
          await noteControlBounds(page.locator('#noteRailNewNoteBtn'));
        } else await expect(page.locator('#noteRailTabNotes')).toBeHidden();
        const ask = page.locator('#noteGenerateAIToggle');
        await ask.focus();
        await ask.press('Enter');
        await expect(ask).toHaveAttribute('aria-expanded', 'true');
        await expect(page.locator('#noteAIPromptInput')).toBeFocused();
        await page
          .locator('#noteAIPromptInput')
          .fill('Unsent fictional drafting request. Never submit.');
        await noteControlBounds(page.locator('#noteAIGenerateBtn'));
        await noteControlBounds(page.locator('#noteAICancelBtn'));
        await page.locator('#noteAICancelBtn').press('Enter');
        await expect(page.locator('#noteAIGeneratePanel')).toBeHidden();
        await expect(ask).toBeFocused();
        await expect(page.locator('#noteAIPromptInput')).toHaveValue('');
        await check();
      }
      expect(await (await request.get(`/api/notes/${data.noteId}`)).json()).toEqual(note);
    });
  }

import { expect, test, type Locator } from '@playwright/test';
import { capture, preparePage } from './helpers/ui-consistency';
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

async function modalControlBounds(control: Locator) {
  await control.focus();
  expect(
    await control.evaluate(node => {
      const r = node.getBoundingClientRect();
      if (r.left < 0 || r.top < 0 || r.right > innerWidth + 1 || r.bottom > innerHeight + 1)
        return false;
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
  for (const width of [320, 1440]) {
    test(`native Notes modal suspends Resources and restores actual invokers ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      seed = await createWorkspaceSeed(request);
      await freezeWorkspaceSeed(request, seed);
      await preparePage(page, theme, width);
      await workspaceMetadataReads(page);
      check = await guardWorkspaceReadTour(page, [seed]);
      await page.goto(`/workspaces/${seed.workspace.folder_slug}?mode=details`);
      await page.locator('[data-cmd-resources]').click();
      const resources = page.getByRole('dialog', { name: 'Resources', exact: true });
      const shown = page.evaluate(
        () =>
          new Promise<void>(resolve =>
            document
              .getElementById('noteEditorModal')!
              .addEventListener('shown.bs.modal', () => resolve(), { once: true })
          )
      );
      await resources.getByRole('button', { name: 'New note', exact: true }).click();
      await shown;
      const editor = page.locator('#noteEditorModal');
      await expect(editor).toBeVisible();
      await expect(resources).not.toBeVisible();
      await expect(editor.locator('#noteNameInput')).toHaveValue('');
      await expect(editor.locator('.note-status-unsaved')).toBeVisible();
      await expect(editor.locator('.note-status-saved')).toBeHidden();
      expect(
        await editor.locator('#notePreviewContent').evaluate(node => {
          const center = node.closest('.note-editor-grid')!.getBoundingClientRect();
          const r = node.getBoundingClientRect();
          return r.width >= center.width - 2 && node.scrollWidth <= node.clientWidth + 1;
        })
      ).toBe(true);
      // Do not type a note body/title here: the real modal auto-creates drafts.
      // Only open/cancel its shared AI form, never submit a model request.
      const ask = editor.locator('#noteGenerateAIToggle');
      if ((await ask.getAttribute('aria-expanded')) === 'true') {
        await ask.focus();
        await ask.press('Enter');
      }
      await ask.focus();
      await ask.press('Enter');
      await expect(ask).toHaveAttribute('aria-expanded', 'true');
      await expect(editor.locator('#noteAIPromptInput')).toBeFocused();
      await editor
        .locator('#noteAIPromptInput')
        .fill('Unsent layout-only request. Do not generate.');
      const closeAI = editor.locator('#noteAICancelBtn');
      await modalControlBounds(editor.locator('#noteAIGenerateBtn'));
      await modalControlBounds(closeAI);
      await closeAI.press('Enter');
      await expect(ask).toBeFocused();
      await expect(editor.locator('#noteAIGeneratePanel')).toBeHidden();
      for (const selector of [
        '#noteNameInput',
        '#noteOpenInPageBtn',
        '#noteCopyBtn',
        '#saveNoteBtn',
        '.modal-footer [data-bs-dismiss="modal"]',
        '[aria-label="Close note editor"]'
      ])
        await modalControlBounds(editor.locator(selector));
      await capture(
        page,
        info,
        'workspace-note-modal-cancel',
        {
          workspaceId: seed.workspace.id,
          fixture:
            'Real Bootstrap Notes lifecycle, empty unsaved note, AI form canceled without inference or successful writes'
        },
        { fullPage: false }
      );
      await editor.getByRole('button', { name: 'Close note editor', exact: true }).click();
      await expect(resources).toBeVisible();
      await expect(resources.getByRole('button', { name: 'New note', exact: true })).toBeFocused();
      await check();
    });
  }

import { expect, test } from '@playwright/test';
import { capture, contrast, preparePage } from './helpers/ui-consistency';
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
  test(`native Markdown edit autosaves and reopens in the focused route ${theme}`, async ({
    page,
    request
  }, info) => {
    seed = await createWorkspaceSeed(request);
    const data = await populateWorkspaceSeed(request, seed);
    await freezeWorkspaceSeed(request, seed);
    const note = seed.records.find(record => record.kind === 'note')!.value;
    const paragraph = 'Fictional local notes, edited and reopened without any agent call.';
    const content = note.content.replace('Fictional local notes.', paragraph);
    expect(content).not.toBe(note.content);
    await preparePage(page, theme, 390);
    await workspaceMetadataReads(page);
    check = await guardWorkspaceReadTour(page, [seed]);
    const path = `/api/notes/${data.noteId}`;
    let writes = 0;
    await page.route(`**${path}`, route => {
      if (route.request().method() !== 'PATCH') return route.fallback();
      expect(++writes).toBe(1);
      expect(route.request().postDataJSON()).toEqual({ name: note.name, content });
      return route.continue(); // Exact real owned content save, not a mock.
    });
    await page.goto(`/workspaces/${seed.workspace.folder_slug}/notes/${data.noteId}`);
    await expect(page.locator('#notePreviewContent')).toContainText('Fictional local notes.');
    await page.locator('#notePreviewContent .note-live-line-rendered[data-line-index="2"]').click();
    const field = page.locator('#notePreviewContent .note-live-line-input[data-line-index="2"]');
    await expect(field).toBeFocused();
    const saved = page.waitForResponse(
      response =>
        new URL(response.url()).pathname === path && response.request().method() === 'PATCH'
    );
    await field.fill(paragraph);
    const response = await saved;
    expect(response.ok()).toBe(true);
    const readback = await checkpointOwnedNoteContent(request, seed, { id: data.noteId, content });
    const status = page.locator('#noteSaveStatus .note-status-saved');
    await expect(status).toBeVisible();
    expect((await contrast(status)).ratio).toBeGreaterThanOrEqual(4.5);
    await expect(page.locator('#noteContentInput')).toHaveValue(content);
    await expect(field).toHaveValue(paragraph);
    // A live single-line editor may retain its textarea across blur. Its
    // current value (not textarea.textContent) and the saved source are truth.
    await page.locator('#noteNameInput').focus();
    await expect(page.locator('#noteContentInput')).toHaveValue(content);
    await page.goto(`/notes/${data.noteId}`);
    await expect(page.locator('#noteNameInput')).toHaveValue(note.name);
    await expect(page.locator('#notePreviewContent')).toContainText(paragraph);
    await expect(page.locator('#notePageTickets')).toBeVisible();
    expect(await (await request.get(path)).json()).toEqual(readback);
    expect(writes).toBe(1);
    await capture(
      page,
      info,
      'workspace-note-real-reopened',
      {
        workspaceId: seed.workspace.id,
        noteId: data.noteId,
        fixture:
          'Genuine one-field-content autosave, exact readback and focused-route reopen; no note assistant/generation'
      },
      { fullPage: false }
    );
    await check();
  });
}

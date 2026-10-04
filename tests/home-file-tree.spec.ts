import { test, expect, Page } from '@playwright/test';

/**
 * The Home file tree and its reading pane (tasks/prd-home-file-tree.md,
 * Release 1): expanding a workspace into its contents, opening them in tabs,
 * editing a note, creating from the row menu, selecting rows, filtering, the
 * one-column layout, and what is remembered across a reload.
 *
 * Not part of CI — run against an isolated server:
 *   ./scripts/e2e-fresh.sh tests/home-file-tree.spec.ts
 *
 * Every test makes its own workspace and removes it, and finds its rows by
 * the workspace's id, so the tests can run side by side on one server.
 */

async function skipOnboarding(page: Page) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
    })
  );
}

type Seeded = { id: string; name: string; noteId: string; noteName: string };

const created: string[] = [];

/** A workspace with one note in it. */
async function seedWorkspace(page: Page, label: string): Promise<Seeded> {
  const stamp = `${Date.now().toString(36)}${Math.floor(Math.random() * 1e6).toString(36)}`;
  const name = `${label} ${stamp}`;
  const workspace = await (await page.request.post('/api/workspaces', { data: { name } })).json();
  const id = workspace.folder.id as string;
  created.push(id);
  const noteName = `Blue hour ${stamp}`;
  const note = await (
    await page.request.post(`/api/workspaces/${id}/notes`, {
      data: { name: noteName, content: '# Blue hour\n\nThe light just after sunset.' }
    })
  ).json();
  return { id, name, noteId: note.note.id as string, noteName };
}

const row = (page: Page, key: string) => page.locator(`#cockpitTreeNav [data-tree-row="${key}"]`);
const name = (page: Page, key: string) => row(page, key).locator('.cockpit-tree-name');
const activeTab = (page: Page) => page.locator('#cockpitTreePane .cockpit-pane-tab.is-active');
const tabs = (page: Page) => page.locator('#cockpitTreePane .cockpit-pane-tab-label');
const editor = (page: Page) => page.locator('#cockpitPaneNoteEditor');
const menu = (page: Page) => page.getByRole('menu');

async function openTree(page: Page, workspaceId: string) {
  await page.goto('/?view=tree');
  await row(page, workspaceId).waitFor();
}

/** Expand a workspace and wait for its contents. */
async function expand(page: Page, workspaceId: string) {
  await row(page, workspaceId).locator('[data-tree-toggle]').click();
  await expect(row(page, workspaceId)).toHaveAttribute('aria-expanded', 'true');
  await row(page, `${workspaceId}/s/notes`).waitFor();
}

test.describe('Home file tree', () => {
  test.beforeEach(async ({ page }) => {
    await skipOnboarding(page);
  });

  test.afterEach(async ({ page }) => {
    while (created.length) {
      await page.request.delete(`/api/workspaces/${created.pop()}?confirm=true`);
    }
  });

  test('a workspace expands into its sections, and a note opens in the pane', async ({ page }) => {
    const ws = await seedWorkspace(page, 'Tree expand');
    await openTree(page, ws.id);

    // Split layout: a 300px tree and the pane beside it, with nothing open.
    await expect(page.locator('#cockpitTreePane .cockpit-pane-empty')).toContainText(
      'Nothing open'
    );
    expect((await page.locator('#cockpitTreeNav').boundingBox())!.width).toBe(300);

    // A workspace starts closed and fetches its contents when it is opened.
    await expect(row(page, ws.id)).toHaveAttribute('aria-expanded', 'false');
    await expand(page, ws.id);
    for (const section of ['notes', 'backlog', 'files', 'agents']) {
      await expect(row(page, `${ws.id}/s/${section}`)).toBeVisible();
    }
    await expect(row(page, `${ws.id}/m`)).toContainText('Memory');
    await expect(row(page, `${ws.id}/s/notes`).locator('.cockpit-tree-count')).toHaveText('1');

    // Two clicks from a closed workspace to a note: expand, then click.
    const noteKey = `${ws.id}/n/${ws.noteId}`;
    await name(page, noteKey).click();
    await expect(activeTab(page)).toContainText(ws.noteName);
    await expect(editor(page)).toContainText('The light just after sunset.');
    await expect(row(page, noteKey)).toHaveAttribute('aria-selected', 'true');
    // The context modal belongs to the Map; in Tree the pane is the detail.
    await expect(page.locator('#cockpitContextModal')).toBeHidden();

    // The workspace's own overview opens in a second tab.
    await name(page, ws.id).click();
    await expect(page.locator('.cockpit-pane-article[data-pane-kind="workspace"]')).toBeVisible();
    await expect(tabs(page)).toHaveCount(2);
  });

  test('a note edited in the pane is saved to the server', async ({ page }) => {
    const ws = await seedWorkspace(page, 'Tree edit');
    await openTree(page, ws.id);
    await expand(page, ws.id);
    await name(page, `${ws.id}/n/${ws.noteId}`).click();
    await editor(page).locator('.note-live-line-rendered').first().waitFor();

    await editor(page).locator('.note-live-line-rendered').last().click();
    await editor(page).locator('.note-live-line-input').first().waitFor();
    await page.keyboard.press('End');
    const saved = page.waitForResponse(
      response =>
        response.request().method() === 'PUT' &&
        new URL(response.url()).pathname === `/api/notes/${ws.noteId}`
    );
    await page.keyboard.type(' Edited from the tree.');
    const status = page.locator('[data-pane-save-status]');
    await expect(status).toHaveText('Unsaved');
    const savedResponse = await saved;
    expect(savedResponse.ok()).toBe(true);
    // Only the text is sent, and the server answers with the note as stored.
    expect(savedResponse.request().postDataJSON()).toEqual({
      content: '# Blue hour\n\nThe light just after sunset. Edited from the tree.'
    });
    expect((await savedResponse.json()).note.content).toContain('Edited from the tree.');
    await expect(status).toHaveText('Saved');
    // The note is not read back with a second request here. Listing a
    // workspace's notes re-imports its note files, and a listing made by
    // another page in the instant between a save reaching the database and
    // reaching the file puts the file's older text back until the next
    // listing (workspace_note_import.go). With specs running side by side
    // that makes a read-back fail now and then for reasons outside the tree.
  });

  test('a note and a ticket are created from the row menu', async ({ page }) => {
    const ws = await seedWorkspace(page, 'Tree create');
    await openTree(page, ws.id);

    // New note, from the workspace's right-click menu.
    await row(page, ws.id).click({ button: 'right' });
    await menu(page).getByRole('menuitem', { name: 'New note', exact: true }).click();
    const draft = page.locator('#cockpitTreeNav [data-tree-draft]');
    await expect(draft).toBeFocused();
    await draft.fill('Session log');
    await draft.press('Enter');
    const newNote = page.locator(
      `#cockpitTreeNav [data-tree-kind="note"][data-tree-row^="${ws.id}/"]`,
      { hasText: 'Session log' }
    );
    await expect(newNote).toBeVisible();
    await expect(activeTab(page)).toContainText('Session log');
    await expect(row(page, `${ws.id}/s/notes`).locator('.cockpit-tree-count')).toHaveText('2');

    // New ticket, from the Backlog section's menu, by keyboard.
    await row(page, `${ws.id}/s/backlog`).focus();
    await page.keyboard.press('Shift+F10');
    await expect(menu(page).getByRole('menuitem')).toHaveText(['New ticket']);
    await page.keyboard.press('Enter');
    await expect(draft).toBeFocused();
    const posted = page.waitForResponse(
      response =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname === `/api/workspaces/${ws.id}/tickets`
    );
    await page.keyboard.type('Tune the snare');
    await page.keyboard.press('Enter');
    expect((await posted).request().postDataJSON()).toMatchObject({
      title: 'Tune the snare',
      state: 'backlog',
      source: 'manual'
    });
    const ticket = page.locator(
      `#cockpitTreeNav [data-tree-kind="ticket"][data-tree-row^="${ws.id}/"]`,
      { hasText: 'Tune the snare' }
    );
    await expect(ticket).toContainText('Backlog');
    await expect(page.locator('.cockpit-pane-article[data-pane-kind="ticket"]')).toBeVisible();

    // Escape cancels a row being named, and nothing is sent.
    let extraPosts = 0;
    page.on('request', request => {
      if (request.method() === 'POST' && request.url().endsWith(`/api/workspaces/${ws.id}/notes`)) {
        extraPosts += 1;
      }
    });
    await row(page, `${ws.id}/s/notes`).click({ button: 'right' });
    await menu(page).getByRole('menuitem', { name: 'New note', exact: true }).click();
    await draft.fill('Never created');
    await draft.press('Escape');
    await expect(draft).toHaveCount(0);
    expect(extraPosts).toBe(0);
  });

  test('a file uploaded from the menu appears under Files and opens', async ({ page }) => {
    const ws = await seedWorkspace(page, 'Tree upload');
    await openTree(page, ws.id);
    await expand(page, ws.id);

    await row(page, `${ws.id}/s/files`).click({ button: 'right' });
    const chooser = page.waitForEvent('filechooser');
    await menu(page).getByRole('menuitem', { name: 'Upload file…', exact: true }).click();
    await (
      await chooser
    ).setFiles({
      name: 'setlist.md',
      mimeType: 'text/markdown',
      buffer: Buffer.from('# Setlist\n\n- Night Drive\n')
    });

    const file = page.locator(
      `#cockpitTreeNav [data-tree-kind="file"][data-tree-row^="${ws.id}/"]`,
      { hasText: 'setlist.md' }
    );
    await expect(file).toBeVisible();
    await expect(activeTab(page)).toContainText('setlist.md');
    await expect(page.locator('#cockpitTreePane .cockpit-pane-markdown h1')).toHaveText('Setlist');
  });

  test('the row menu holds the management actions and works from the keyboard', async ({
    page
  }) => {
    const ws = await seedWorkspace(page, 'Tree menu');
    await openTree(page, ws.id);

    await row(page, ws.id).focus();
    await page.keyboard.press('Shift+F10');
    await expect(menu(page).getByRole('menuitem')).toHaveText([
      'Open workspace',
      'New note',
      'New ticket',
      'Upload file…',
      'Refresh',
      'Move…',
      'Delete'
    ]);
    await expect(menu(page).getByRole('menuitem').first()).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(menu(page)).toHaveCount(0);
    await expect(row(page, ws.id)).toBeFocused();

    // The "⋯" button opens the same menu; a note's menu is its own.
    await expand(page, ws.id);
    const noteKey = `${ws.id}/n/${ws.noteId}`;
    await row(page, noteKey).hover();
    await row(page, noteKey).locator('[data-tree-menu-for]').click();
    await expect(menu(page).getByRole('menuitem')).toHaveText(['Open', 'Open in workspace']);
    await menu(page).getByRole('menuitem', { name: 'Open', exact: true }).click();
    await expect(activeTab(page)).toContainText(ws.noteName);

    // Delete asks first, and Undo in the header puts the workspace back.
    await row(page, ws.id).click({ button: 'right' });
    await menu(page).getByRole('menuitem', { name: 'Delete', exact: true }).click();
    const confirm = page.locator('dialog.ws-delete-dialog');
    await expect(confirm).toContainText(`Delete "${ws.name}"?`);
    await confirm.getByRole('button', { name: 'Delete', exact: true }).click();
    await expect(row(page, ws.id)).toHaveCount(0);
    // Its tab goes with it.
    await expect(tabs(page)).toHaveCount(0);
    const undo = page.locator('#cockpitTreeNav').getByRole('button', { name: 'Undo', exact: true });
    await expect(undo).toBeEnabled();
    await undo.click();
    await expect(row(page, ws.id)).toBeVisible();
  });

  test('Cmd/Ctrl-click and Shift-click select rows without opening them', async ({ page }) => {
    const first = await seedWorkspace(page, 'Tree select A');
    const second = await seedWorkspace(page, 'Tree select B');
    await openTree(page, first.id);
    const bar = page.locator('#cockpitTreeNav [data-tree-bulkbar]');
    await expect(bar).toBeHidden();

    await name(page, first.id).click({ modifiers: ['ControlOrMeta'] });
    await expect(row(page, first.id)).toHaveAttribute('aria-checked', 'true');
    await expect(row(page, first.id)).toHaveAttribute('aria-selected', 'false');
    await expect(bar).toContainText('1 selected');
    await expect(tabs(page)).toHaveCount(0);

    await name(page, second.id).click({ modifiers: ['ControlOrMeta'] });
    await expect(bar).toContainText('2 selected');
    for (const label of ['Select all', 'Group selected', 'Delete selected', 'Cancel']) {
      await expect(bar.getByRole('button', { name: label, exact: true })).toBeVisible();
    }

    await bar.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(bar).toBeHidden();
    await expect(row(page, first.id)).toHaveAttribute('aria-checked', 'false');

    // Shift-click selects from the last row picked to the one clicked.
    await name(page, first.id).click({ modifiers: ['ControlOrMeta'] });
    await name(page, second.id).click({ modifiers: ['Shift'] });
    await expect(row(page, first.id)).toHaveAttribute('aria-checked', 'true');
    await expect(row(page, second.id)).toHaveAttribute('aria-checked', 'true');
    await bar.getByRole('button', { name: 'Cancel', exact: true }).click();

    // A note cannot be selected: a modified click on it is a plain click.
    await expand(page, first.id);
    const noteKey = `${first.id}/n/${first.noteId}`;
    await name(page, noteKey).click({ modifiers: ['ControlOrMeta'] });
    await expect(activeTab(page)).toContainText(first.noteName);
    await expect(bar).toBeHidden();
  });

  test('the filter narrows the tree to matching names, loaded contents included', async ({
    page
  }) => {
    const ws = await seedWorkspace(page, 'Tree filter');
    await openTree(page, ws.id);
    const filter = page.locator('#cockpitTreeNav [data-tree-filter]');
    const rows = page.locator('#cockpitTreeNav [data-tree-row]');

    // By workspace name; the cursor stays in the box as the tree redraws.
    await filter.click();
    await page.keyboard.type(ws.name.toUpperCase());
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toHaveAttribute('data-tree-row', ws.id);
    await expect(filter).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(filter).toHaveValue('');
    await expect(row(page, ws.id)).toBeVisible();

    // A note is found once its workspace has been expanded, even when closed.
    await filter.fill(ws.noteName);
    await expect(page.locator('#cockpitTreeNav .cockpit-tree-empty')).toHaveText(
      'Nothing matches that filter.'
    );
    await expect(page.locator('#cockpitTreeNav .cockpit-tree-empty-note')).toContainText(
      'searched only after they are expanded'
    );
    await page.locator('#cockpitTreeNav [data-tree-filter-clear]').click();
    await expand(page, ws.id);
    await row(page, ws.id).locator('[data-tree-toggle]').click();
    await expect(row(page, `${ws.id}/s/notes`)).toHaveCount(0);
    await filter.fill(ws.noteName);
    await expect(rows).toHaveCount(3);
    await expect(row(page, `${ws.id}/n/${ws.noteId}`)).toBeVisible();
    await expect(row(page, ws.id)).toHaveAttribute('aria-expanded', 'true');
  });

  test('open rows and tabs come back after a reload, and a deleted note’s tab does not', async ({
    page
  }) => {
    const ws = await seedWorkspace(page, 'Tree restore');
    const other = await (
      await page.request.post(`/api/workspaces/${ws.id}/notes`, {
        data: { name: 'Second verse', content: 'Two.' }
      })
    ).json();
    const noteKey = `${ws.id}/n/${ws.noteId}`;
    const otherKey = `${ws.id}/n/${other.note.id}`;
    await openTree(page, ws.id);
    await expand(page, ws.id);
    await name(page, otherKey).click();
    await expect(activeTab(page)).toContainText('Second verse');
    await name(page, noteKey).click();
    await expect(editor(page)).toContainText('The light just after sunset.');

    // In Map view the remembered tree asks the server for nothing.
    const asked: string[] = [];
    page.on('request', request => {
      const path = new URL(request.url()).pathname;
      if (
        path === `/api/notes/${ws.noteId}` ||
        new RegExp(`^/api/workspaces/${ws.id}/(tickets|files/tree|memory|agents)$`).test(path)
      ) {
        asked.push(path);
      }
    });
    await page.goto('/');
    await page.locator('.ws-map-tile[data-ws-id]').first().waitFor();
    // Long enough for a request made on load to have been sent.
    await page.waitForTimeout(1500);
    expect(asked).toEqual([]);

    // Back in Tree: the same rows are open, the same tabs, the same one active.
    await page.locator('[data-cockpit-view="tree"]').click();
    await expect(row(page, ws.id)).toHaveAttribute('aria-expanded', 'true');
    await expect(tabs(page)).toHaveText(['Second verse', ws.noteName]);
    await expect(activeTab(page)).toContainText(ws.noteName);
    await expect(editor(page)).toContainText('The light just after sunset.');

    // The note is deleted elsewhere; after a reload its tab is simply gone.
    await page.request.delete(`/api/notes/${ws.noteId}`);
    await page.reload();
    await row(page, ws.id).waitFor();
    await expect(tabs(page)).toHaveText(['Second verse']);
    await expect(activeTab(page)).toContainText('Second verse');
    await expect(page.locator('#cockpitTreePane [data-pane-retry]')).toHaveCount(0);
    await expect(page.locator('#cockpitRailLive')).not.toContainText(/couldn.t load/i);
  });

  test('under 720px the pane takes the tree’s place and "Back to tree" returns', async ({
    page
  }) => {
    const ws = await seedWorkspace(page, 'Tree narrow');
    await page.setViewportSize({ width: 640, height: 900 });
    await openTree(page, ws.id);
    const tree = page.locator('#cockpitTree');
    await expect(tree).toHaveAttribute('data-columns', 'tree');
    await expect(page.locator('#cockpitTreePane')).toBeHidden();

    await expand(page, ws.id);
    const noteKey = `${ws.id}/n/${ws.noteId}`;
    await name(page, noteKey).click();
    await expect(tree).toHaveAttribute('data-columns', 'pane');
    await expect(page.locator('#cockpitTreeNav')).toBeHidden();
    await expect(page.locator('#cockpitTreePane [data-pane-title]')).toBeFocused();
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
    ).toBe(true);

    const back = page.getByRole('button', { name: 'Back to tree', exact: true });
    await back.click();
    await expect(tree).toHaveAttribute('data-columns', 'tree');
    await expect(row(page, noteKey)).toBeFocused();

    // With room for both again, both show and the button is gone.
    await page.setViewportSize({ width: 1440, height: 900 });
    await expect(tree).toHaveAttribute('data-columns', 'both');
    await expect(page.locator('#cockpitTreeNav')).toBeVisible();
    await expect(page.locator('#cockpitTreePane')).toBeVisible();
    await expect(back).toBeHidden();
  });
});

import { test, expect, Page } from '@playwright/test';
import { goToFiles, goToMap } from './helpers/home-destination';

/**
 * Files' one search box finds Ori's own items by name (tasks/prd-merge-tree-into-
 * files.md, requirements 12-28): workspaces, notes, tickets, chats and stored
 * files, whether or not their rows were ever expanded. What it finds is a
 * Results tab in the pane; the tree beside it is never narrowed; and the term
 * is never written to the address, to browser storage or to the remembered
 * tree state.
 *
 * Not part of CI — run against an isolated server:
 *   ./scripts/e2e-fresh.sh tests/files-name-search.spec.ts -- --workers=1
 *
 * Every name carries a stamp unique to its test, so the tests find only their
 * own items and can share a server.
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

const created: string[] = [];
const stampOf = () => `${Date.now().toString(36)}${Math.floor(Math.random() * 1e6).toString(36)}`;

type Seeded = {
  id: string;
  name: string;
  noteId: string;
  noteName: string;
  ticketName: string;
  chatName: string;
};

/** A workspace with a note, a ticket and a chat, every name holding `stamp`. */
async function seed(page: Page, stamp: string, label: string): Promise<Seeded> {
  const name = `${label} ${stamp}`;
  const workspace = await (await page.request.post('/api/workspaces', { data: { name } })).json();
  const id = workspace.folder.id as string;
  created.push(id);

  const noteName = `${label} note ${stamp}`;
  const note = await (
    await page.request.post(`/api/workspaces/${id}/notes`, {
      data: { name: noteName, content: '# Body\n\nSome text that is never searched.' }
    })
  ).json();

  const ticketName = `${label} ticket ${stamp}`;
  const ticket = await page.request.post(`/api/workspaces/${id}/tickets`, {
    data: { title: ticketName, state: 'backlog', source: 'manual' }
  });
  expect(ticket.ok(), 'the ticket was created').toBeTruthy();

  const chatName = `${label} chat ${stamp}`;
  const chat = await page.request.post('/api/sessions', {
    data: { title: chatName, folder_id: id }
  });
  expect(chat.ok(), 'the chat was created').toBeTruthy();

  return { id, name, noteId: note.note.id as string, noteName, ticketName, chatName };
}

const search = (page: Page) => page.locator('#cockpitFilesSearchInput');
const resultsTab = (page: Page) => page.locator('#cockpitTreePane .cockpit-pane-tab--results');
const strip = (page: Page) => page.locator('#cockpitTreePane .cockpit-pane-tab');
const group = (page: Page, kind: string) =>
  page.locator(`#cockpitTreePane [data-results-group="${kind}"]`);
const treeRow = (page: Page, key: string) =>
  page.locator(`#cockpitTreeNav [data-tree-row="${key}"]`);
const pane = (page: Page) => page.locator('#cockpitTreePane');
const searchRequests = (page: Page) => {
  const bodies: unknown[] = [];
  page.on('request', request => {
    if (new URL(request.url()).pathname === '/api/name-search') {
      bodies.push(JSON.parse(request.postData() || '{}'));
    }
  });
  return bodies;
};

async function openFiles(page: Page) {
  await page.goto('/?view=tree');
  await expect(page.locator('#cockpitTree')).toBeVisible();
  await expect(search(page)).toBeVisible();
}

test.describe('Files search: finding Ori’s own items by name', () => {
  test.beforeEach(async ({ page }) => {
    await skipOnboarding(page);
  });

  test.afterEach(async ({ page }) => {
    while (created.length) {
      await page.request.delete(`/api/workspaces/${created.pop()}?confirm=true`);
    }
  });

  test('finds every kind by name with nothing expanded, and leaves the tree whole', async ({
    page
  }) => {
    const stamp = stampOf();
    const a = await seed(page, stamp, 'Harbor');
    const b = await seed(page, stamp, 'Garden');
    await openFiles(page);
    await treeRow(page, a.id).waitFor();

    const rowsBefore = await page.locator('#cockpitTreeNav [data-tree-row]').count();
    await expect(treeRow(page, a.id)).toHaveAttribute('aria-expanded', 'false');

    await search(page).fill(stamp.toUpperCase());

    // The Results tab is first in the strip, selected, and reads the term.
    await expect(resultsTab(page)).toBeVisible();
    await expect(strip(page).first()).toHaveClass(/cockpit-pane-tab--results/);
    await expect(resultsTab(page).locator('[role="tab"]')).toHaveAttribute('aria-selected', 'true');
    await expect(resultsTab(page)).toContainText(`Results for “${stamp.toUpperCase()}”`);

    // Each kind, found without a row having been opened.
    await expect(group(page, 'workspaces')).toContainText(a.name);
    await expect(group(page, 'workspaces')).toContainText(b.name);
    await expect(group(page, 'workspaces').locator('.cockpit-results-count')).toHaveText('2');
    await expect(group(page, 'notes')).toContainText(a.noteName);
    await expect(group(page, 'notes')).toContainText(b.noteName);
    await expect(group(page, 'tickets')).toContainText(a.ticketName);
    await expect(group(page, 'chats')).toContainText(a.chatName);

    // Where each lives, and the order of the groups.
    await expect(group(page, 'notes').locator('.cockpit-result').first()).toContainText('› Notes');
    const order = await pane(page)
      .locator('[data-results-group]')
      .evaluateAll(els => els.map(el => el.getAttribute('data-results-group')));
    expect(order).toEqual(['workspaces', 'notes', 'tickets', 'chats']);

    // The tree is exactly as it was: no row hidden, none opened.
    await expect(page.locator('#cockpitTreeNav [data-tree-row]')).toHaveCount(rowsBefore);
    await expect(treeRow(page, a.id)).toHaveAttribute('aria-expanded', 'false');
  });

  test('matches names only, ignoring case, anywhere in the name', async ({ page }) => {
    const stamp = stampOf();
    const a = await seed(page, stamp, 'Lantern');
    await openFiles(page);
    await treeRow(page, a.id).waitFor();

    // Text inside the note is not searched.
    await search(page).fill('never searched');
    await expect(pane(page).locator('.cockpit-results-status')).toContainText(
      'Nothing named “never searched”'
    );
    await expect(pane(page).locator('[data-result-open]')).toHaveCount(0);

    // The middle of a name, in another case.
    await search(page).fill(`TERN TICKET ${stamp}`);
    await expect(group(page, 'tickets')).toContainText(a.ticketName);
  });

  test('says plainly when nothing matches, and when a kind could not be searched', async ({
    page
  }) => {
    await openFiles(page);
    await search(page).fill(`nothing-named-${stampOf()}`);
    await expect(pane(page).locator('.cockpit-results-status')).toContainText('Nothing named');

    // One kind failing is said, with Retry, while the others still answer.
    const requests: Array<{ kinds?: string[] }> = [];
    await page.route('**/api/name-search', async route => {
      const body = JSON.parse(route.request().postData() || '{}');
      requests.push(body);
      if (body.kinds && body.kinds[0] === 'tickets') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            kinds: [{ kind: 'tickets', status: 'ok', count: 0, items: [], next_cursor: '' }]
          })
        });
      }
      const response = await route.fetch();
      const json = await response.json();
      json.kinds = json.kinds.map((k: { kind: string }) =>
        k.kind === 'tickets'
          ? { kind: 'tickets', status: 'unavailable', count: null, items: [], next_cursor: '' }
          : k
      );
      return route.fulfill({
        response,
        contentType: 'application/json',
        body: JSON.stringify(json)
      });
    });
    await search(page).fill(`other-${stampOf()}`);
    const unavailable = pane(page).locator('[data-results-unavailable="tickets"]');
    await expect(unavailable).toContainText('couldn’t be searched right now');
    await unavailable.getByRole('button', { name: 'Retry' }).click();
    await expect(unavailable).toHaveCount(0);
    expect(requests.some(r => r.kinds && r.kinds[0] === 'tickets')).toBe(true);
  });

  test('one request after the pause, not one per keystroke; a slower earlier answer loses', async ({
    page
  }) => {
    const stamp = stampOf();
    const a = await seed(page, stamp, 'Pause');
    await openFiles(page);
    await treeRow(page, a.id).waitFor();
    const bodies = searchRequests(page);

    await search(page).pressSequentially(`pause ${stamp}`, { delay: 30 });
    await expect(group(page, 'workspaces')).toContainText(a.name);
    expect(bodies).toHaveLength(1);
    expect((bodies[0] as { term: string }).term).toBe(`pause ${stamp}`);

    // The first search is held back; a newer one overtakes it.
    await search(page).fill('');
    let held = 0;
    await page.route('**/api/name-search', async route => {
      const term = JSON.parse(route.request().postData() || '{}').term as string;
      if (term === `slow ${stamp}`) {
        held += 1;
        await new Promise(resolve => setTimeout(resolve, 1500));
      }
      await route.continue();
    });
    await search(page).fill(`slow ${stamp}`);
    await page.waitForTimeout(400); // past the pause: the slow request is in flight
    await search(page).fill(`pause ${stamp}`);
    await expect(group(page, 'workspaces')).toContainText(a.name);
    await page.waitForTimeout(1600); // the slow answer has now arrived and must be ignored
    await expect(resultsTab(page)).toContainText(`pause ${stamp}`);
    await expect(group(page, 'workspaces')).toContainText(a.name);
    expect(held).toBe(1);
  });

  test('the term is never in the address, in browser storage or in the remembered state', async ({
    page
  }) => {
    const stamp = stampOf();
    const a = await seed(page, stamp, 'Private');
    await openFiles(page);
    await treeRow(page, a.id).waitFor();

    const needle = `zebra-${stamp}`;
    const b = await (
      await page.request.post('/api/workspaces', { data: { name: `Private ${needle}` } })
    ).json();
    created.push(b.folder.id as string);
    await page.reload();
    await search(page).fill(needle);
    await expect(group(page, 'workspaces')).toContainText(needle);
    // The result is not opened here: an opened workspace's tab is stored under
    // its own NAME, which holds the term when the term is the name.

    const where = async () =>
      page.evaluate(() =>
        JSON.stringify({
          url: location.href,
          state: history.state,
          name: window.name,
          local: { ...localStorage },
          session: { ...sessionStorage }
        })
      );
    const before = await where();
    expect(before).not.toContain(needle);
    expect(before).not.toContain('search:results');
    expect(page.url()).not.toContain(needle);
    // Each request carries it only in its body: no address held it.
    const addresses: string[] = [];
    page.on('request', request => addresses.push(request.url()));
    await search(page).fill(`${needle}x`);
    await page.waitForTimeout(500);
    expect(addresses.filter(url => url.includes(needle))).toEqual([]);

    await page.reload();
    await expect(page.locator('#cockpitTree')).toBeVisible();
    const after = await where();
    expect(after).not.toContain(needle);
    await expect(search(page)).toHaveValue('');
    await expect(resultsTab(page)).toHaveCount(0);
  });

  test('opening a result opens the row’s own tab beside Results, which stays', async ({ page }) => {
    const stamp = stampOf();
    const a = await seed(page, stamp, 'Opener');
    await openFiles(page);
    await treeRow(page, a.id).waitFor();
    await search(page).fill(stamp);

    await group(page, 'notes').locator('[data-result-open]').first().click();
    const active = pane(page).locator('.cockpit-pane-tab.is-active');
    await expect(active).toContainText(a.noteName);
    await expect(resultsTab(page)).toBeVisible();
    await expect(resultsTab(page).locator('[role="tab"]')).toHaveAttribute(
      'aria-selected',
      'false'
    );
    // The same tab its tree row opens: keyed by the row's own id.
    await expect(pane(page).locator(`[data-pane-tab="${a.id}/n/${a.noteId}"]`)).toHaveCount(1);
    await expect(treeRow(page, `${a.id}/n/${a.noteId}`)).toHaveClass(/is-active/);

    // Back to the results: they are still there.
    await resultsTab(page).locator('[role="tab"]').click();
    await expect(group(page, 'notes')).toContainText(a.noteName);

    // A workspace result opens the workspace's own tab.
    await group(page, 'workspaces').locator('[data-result-open]').first().click();
    await expect(pane(page).locator('.cockpit-pane-tab.is-active')).toContainText(a.name);
  });

  test('clearing the box returns to the tab that was open, or to nothing open', async ({
    page
  }) => {
    const stamp = stampOf();
    const a = await seed(page, stamp, 'Return');
    await openFiles(page);
    await treeRow(page, a.id).waitFor();

    // Nothing open before: clearing returns to the empty pane.
    await search(page).fill(stamp);
    await expect(resultsTab(page)).toBeVisible();
    await search(page).press('Escape');
    await expect(search(page)).toHaveValue('');
    await expect(resultsTab(page)).toHaveCount(0);
    await expect(pane(page).locator('.cockpit-pane-empty')).toContainText('Nothing open');

    // A tab open before: it comes back.
    await treeRow(page, a.id).locator('[data-tree-toggle]').click();
    await treeRow(page, `${a.id}/s/notes`).waitFor();
    await treeRow(page, `${a.id}/n/${a.noteId}`).locator('.cockpit-tree-name').click();
    await expect(pane(page).locator('.cockpit-pane-tab.is-active')).toContainText(a.noteName);
    await search(page).fill(stamp);
    await expect(resultsTab(page).locator('[role="tab"]')).toHaveAttribute('aria-selected', 'true');
    await page.locator('#cockpitFilesSearchClear').click();
    await expect(resultsTab(page)).toHaveCount(0);
    await expect(pane(page).locator('.cockpit-pane-tab.is-active')).toContainText(a.noteName);
  });

  test('Show in tree opens the parents, loads their contents, and focuses the row', async ({
    page
  }) => {
    const stamp = stampOf();
    const a = await seed(page, stamp, 'Reveal');
    await openFiles(page);
    await treeRow(page, a.id).waitFor();
    await expect(treeRow(page, `${a.id}/n/${a.noteId}`)).toHaveCount(0);

    await search(page).fill(a.noteName);
    await group(page, 'notes')
      .getByRole('button', { name: /Show .* in tree/ })
      .click();

    await expect(treeRow(page, a.id)).toHaveAttribute('aria-expanded', 'true');
    const noteRow = treeRow(page, `${a.id}/n/${a.noteId}`);
    await expect(noteRow).toBeVisible();
    await expect(noteRow).toBeFocused();
    // Showing is not opening: the results are still what the pane shows.
    await expect(resultsTab(page).locator('[role="tab"]')).toHaveAttribute('aria-selected', 'true');
  });

  test('keyboard: "/" focuses the box, Down walks the results, Enter opens, Escape clears', async ({
    page
  }) => {
    const stamp = stampOf();
    const a = await seed(page, stamp, 'Keys');
    await openFiles(page);
    await treeRow(page, a.id).waitFor();

    await page.locator('body').click({ position: { x: 5, y: 5 } });
    await page.keyboard.press('/');
    await expect(search(page)).toBeFocused();
    await page.keyboard.type(`${stamp}`);
    await expect(group(page, 'workspaces')).toContainText(a.name);

    await page.keyboard.press('ArrowDown');
    const results = pane(page).locator('[data-result-open]');
    await expect(results.first()).toBeFocused();
    await page.keyboard.press('ArrowDown');
    await expect(results.nth(1)).toBeFocused();
    await page.keyboard.press('ArrowUp');
    await page.keyboard.press('ArrowUp');
    await expect(search(page)).toBeFocused();

    // Enter in the box opens the first result.
    await page.keyboard.press('Enter');
    await expect(pane(page).locator('.cockpit-pane-tab.is-active')).toContainText(a.name);

    // Escape from a result clears the box and puts the cursor back in it.
    await resultsTab(page).locator('[role="tab"]').click();
    await results.first().focus();
    await page.keyboard.press('Escape');
    await expect(search(page)).toHaveValue('');
    await expect(search(page)).toBeFocused();
    await expect(resultsTab(page)).toHaveCount(0);
  });

  test('"/" typed inside another field is just a character', async ({ page }) => {
    const ws = await seed(page, stampOf(), 'Slash');
    await openFiles(page);
    await treeRow(page, ws.id).waitFor();
    await treeRow(page, ws.id).locator('[data-tree-toggle]').click();
    await treeRow(page, `${ws.id}/n/${ws.noteId}`).locator('.cockpit-tree-name').click();
    const editor = page.locator('#cockpitPaneNoteEditor');
    await expect(editor).toBeVisible();
    await editor.click();
    await page.keyboard.type('a/b');
    await expect(search(page)).not.toBeFocused();
  });

  test('25 results per kind, then More results', async ({ page }) => {
    const stamp = stampOf();
    const name = `Many ${stamp}`;
    const workspace = await (await page.request.post('/api/workspaces', { data: { name } })).json();
    const id = workspace.folder.id as string;
    created.push(id);
    for (let i = 0; i < 27; i += 1) {
      const made = await page.request.post(`/api/workspaces/${id}/notes`, {
        data: { name: `Draft ${String(i).padStart(2, '0')} ${stamp}`, content: 'x' }
      });
      expect(made.ok()).toBeTruthy();
    }
    await openFiles(page);
    await treeRow(page, id).waitFor();
    // The stamp: every note is "Draft NN <stamp>", so "draft <stamp>" would not
    // be one contiguous piece of any name.
    await search(page).fill(stamp);

    const notes = group(page, 'notes');
    await expect(notes.locator('[data-result-open]')).toHaveCount(25);
    await expect(notes.locator('.cockpit-results-count')).toHaveText('27');
    await notes.getByRole('button', { name: 'More results' }).click();
    await expect(notes.locator('[data-result-open]')).toHaveCount(27);
    await expect(notes.getByRole('button', { name: 'More results' })).toHaveCount(0);
  });

  test('in one column the results take the tree’s place, and Back to tree returns', async ({
    page
  }) => {
    const stamp = stampOf();
    const a = await seed(page, stamp, 'Narrow');
    await page.setViewportSize({ width: 390, height: 844 });
    await openFiles(page);
    await treeRow(page, a.id).waitFor();
    await expect(page.locator('#cockpitTree')).toHaveAttribute('data-columns', 'tree');

    await search(page).fill(stamp);
    await expect(group(page, 'workspaces')).toContainText(a.name);
    await expect(page.locator('#cockpitTree')).toHaveAttribute('data-columns', 'pane');

    await page.getByRole('button', { name: 'Back to tree' }).click();
    await expect(page.locator('#cockpitTree')).toHaveAttribute('data-columns', 'tree');
    // The search is still there: Show in tree from a result brings the tree back.
    await expect(search(page)).toHaveValue(stamp);
  });

  test('Files and Map round trip keeps the search and its results in the page', async ({
    page
  }) => {
    const stamp = stampOf();
    const a = await seed(page, stamp, 'Round');
    await openFiles(page);
    await treeRow(page, a.id).waitFor();
    await search(page).fill(stamp);
    await expect(group(page, 'workspaces')).toContainText(a.name);

    await goToMap(page);
    await goToFiles(page);
    await expect(search(page)).toHaveValue(stamp);
    await expect(resultsTab(page)).toBeVisible();
    await expect(group(page, 'workspaces')).toContainText(a.name);
  });

  test('the heading, the box and its status are named for assistive technology', async ({
    page
  }) => {
    const stamp = stampOf();
    const a = await seed(page, stamp, 'Named');
    await openFiles(page);
    await treeRow(page, a.id).waitFor();
    await expect(page.getByRole('search')).toBeVisible();
    await expect(page.getByRole('textbox', { name: 'Search by name' })).toBeVisible();
    await search(page).fill(stamp);
    await expect(page.locator('#cockpitFilesSearchStatus')).toContainText(/\d+ results?/);
    await expect(page.getByRole('tablist', { name: 'Open items' })).toBeVisible();
    await expect(
      page.getByRole('tab', { name: new RegExp(`Results for .*${stamp}`) })
    ).toBeVisible();
  });
});

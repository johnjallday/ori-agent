/*
 * Drives the Home file tree (tasks/prd-home-file-tree.md) in a headless
 * browser against a running, seeded demo server, takes screenshots, and
 * checks the things only a real page can show.
 *
 *   node scripts/demo-home-file-tree.mjs <baseUrl> <outDir> <stage> [light|dark]
 *
 * Start and seed the server first:
 *   ./scripts/demo-server.sh 8931 <sandbox>
 *   ./scripts/smoke.sh filetree http://localhost:8931 seed
 *
 * Stages:
 *   tree   Group 1: the split layout, slim rows, lazy contents, a note in the
 *          pane, and proof that Map view asks for none of it.
 *   pane   Group 2: one of each kind in the pane, and how tabs behave.
 *   note   Group 3: editing a note in the pane. Give the sandbox directory as
 *          a fifth argument and it also reads the note's file on disk.
 *   create Group 4: a note, a ticket and an upload made from the row menu.
 *   manage Group 5: Move by menu and by drag, a move that is refused, Delete
 *          and Undo, selecting rows and grouping them, and tag filters.
 *
 * Exits non-zero when a check fails, a console error appears, or a request
 * fails, so a page that renders but is quietly broken does not pass.
 */
import { chromium } from 'playwright';
import { mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

const [baseUrl, outDirArg, stage = 'tree', theme = 'light', sandbox = ''] = process.argv.slice(2);
if (!baseUrl || !outDirArg) {
  console.error(
    'usage: node scripts/demo-home-file-tree.mjs <baseUrl> <outDir> <stage> [light|dark]'
  );
  process.exit(2);
}
const outDir = resolve(outDirArg);
mkdirSync(outDir, { recursive: true });

// The requests that fetch a workspace's contents. Home in Map view, and a Tree
// with nothing expanded, must make none of them (FR17).
const CONTENT_REQUEST = /\/api\/workspaces\/[^/]+\/(notes|tickets|files\/tree|memory|agents)(\?|$)/;

const problems = [];
const failures = [];
const check = (ok, message) => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${message}`);
  if (!ok) failures.push(message);
};

const browser = await chromium.launch();
const context = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  colorScheme: theme === 'dark' ? 'dark' : 'light'
});
await context.addInitScript(value => {
  try {
    window.localStorage.setItem('theme', value);
    window.localStorage.setItem('ori-theme', value);
  } catch (_) {
    /* storage may be unavailable */
  }
}, theme);
const page = await context.newPage();

const requests = [];
page.on('request', request => {
  const url = new URL(request.url());
  if (url.origin === new URL(baseUrl).origin) requests.push(`${request.method()} ${url.pathname}`);
});
// Failed requests are reported once, from the response itself; the browser's
// own "Failed to load resource" console line would only repeat them. One 404
// is expected in a fresh sandbox and has nothing to do with the tree: Ask Ori
// looks up its own agent, which a sandbox with no assistant does not have.
const EXPECTED_FAILURES = [/\/api\/agents\?name=Ask%20Ori$/];
page.on('console', message => {
  if (message.type() !== 'error') return;
  if (/Failed to load resource/.test(message.text())) return;
  // The update checker asks GitHub on every page; a sandbox may be offline, and
  // a navigation cuts the request short. Neither is the tree's doing. The same
  // goes for the model list every page loads for its agent dialogs: leaving
  // Home while it is still on its way logs "Failed to fetch".
  if (/Error checking for updates/.test(message.text())) return;
  if (/\[Agents\] Failed to load providers[\s\S]*Failed to fetch/.test(message.text())) return;
  problems.push(`console: ${message.text()}`);
});
page.on('pageerror', error => problems.push(`page error: ${error.message}`));
page.on('response', response => {
  if (response.status() < 400) return;
  if (EXPECTED_FAILURES.some(pattern => pattern.test(response.url()))) return;
  problems.push(`HTTP ${response.status()}: ${response.url()}`);
});

const contentRequests = () => requests.filter(entry => CONTENT_REQUEST.test(entry.split(' ')[1]));
const shot = async name => {
  const file = join(outDir, `${name}-${theme}.png`);
  await page.screenshot({ path: file });
  console.log(`shot ${file}`);
};
const settle = (ms = 500) => page.waitForTimeout(ms);
const row = name =>
  page.locator('#cockpitTreeNav [data-tree-row]').filter({ hasText: name }).first();
const rowByKind = (kind, name) =>
  page
    .locator(`#cockpitTreeNav [data-tree-row][data-tree-kind="${kind}"]`)
    .filter({ hasText: name })
    .first();

async function applyTheme() {
  await page.evaluate(value => {
    document.documentElement.setAttribute('data-bs-theme', value);
  }, theme);
}

async function stageTree() {
  // --- Map view: no contents are fetched ---------------------------------
  await page.goto(`${baseUrl}/`, { waitUntil: 'domcontentloaded' });
  await page.locator('#cockpitMap').waitFor({ state: 'visible' });
  await settle(1500);
  // Home already asks each workspace for its notes once: the session manager
  // (sessions.js) does that on every Home load, with or without the tree. That
  // is the baseline. What the tree must not add in Map view is anything else —
  // no tickets, files, memory or agents, and no second notes request.
  const baseline = contentRequests();
  const baselineKinds = [...new Set(baseline.map(entry => entry.split('/').slice(4).join('/')))];
  console.log(
    `Map view baseline: ${baseline.length} request(s) of kind [${baselineKinds.join(', ')}]`
  );
  check(
    baselineKinds.every(kind => kind === 'notes') && new Set(baseline).size === baseline.length,
    "Map view fetches no tickets, files, memory or agents, and each workspace's notes at most once"
  );
  const mapRequestCount = requests.length;
  const baselineCount = baseline.length;

  // --- Tree view: split layout ------------------------------------------
  await page.locator('#cockpitViewTree').click();
  await page.locator('#cockpitTreeNav [data-tree-row]').first().waitFor();
  await applyTheme();
  await settle(800);

  const layout = await page.evaluate(() => {
    const nav = document.getElementById('cockpitTreeNav').getBoundingClientRect();
    const pane = document.getElementById('cockpitTreePane').getBoundingClientRect();
    const rowEl = document.querySelector('#cockpitTreeNav [data-tree-row]');
    return {
      navWidth: Math.round(nav.width),
      paneLeft: Math.round(pane.left),
      navRight: Math.round(nav.right),
      paneWidth: Math.round(pane.width),
      rowHeight: Math.round(rowEl.getBoundingClientRect().height),
      pageScrollsSideways: document.documentElement.scrollWidth > window.innerWidth,
      navHeight: Math.round(nav.height),
      paneHeight: Math.round(pane.height)
    };
  });
  console.log('layout', JSON.stringify(layout));
  check(layout.navWidth === 300, `tree column is 300px wide (is ${layout.navWidth})`);
  check(
    layout.paneLeft === layout.navRight && layout.paneWidth > 600,
    'pane fills the rest of the width'
  );
  check(layout.rowHeight === 30, `rows are 30px tall (are ${layout.rowHeight})`);
  check(!layout.pageScrollsSideways, 'the page does not scroll sideways');
  check(
    (await page.locator('.cockpit-pane-empty').innerText()).includes('Nothing open'),
    'empty pane says "Nothing open"'
  );

  // Groups start expanded, so opening Tree fetches the group's sections — and
  // only the group's. Workspaces start collapsed.
  const afterTree = contentRequests().slice(baselineCount);
  const fetchedIds = [...new Set(afterTree.map(entry => entry.split('/')[3]))].sort();
  const groupRowIds = (
    await page
      .locator('#cockpitTreeNav [data-tree-row][data-tree-kind="group"]')
      .evaluateAll(els => els.map(el => el.getAttribute('data-tree-row')))
  ).sort();
  check(
    afterTree.length === 5 * groupRowIds.length && fetchedIds.join(',') === groupRowIds.join(','),
    `opening Tree fetches 5 sections for each expanded group and nothing for a collapsed workspace (${afterTree.length} requests, ${groupRowIds.length} group(s))`
  );
  check(
    (await rowByKind('section', 'Notes').count()) === 1,
    'the group shows its own Notes section'
  );
  check(
    (await rowByKind('group', 'Music').getAttribute('aria-expanded')) === 'true',
    'groups start expanded'
  );
  check(
    (await rowByKind('workspace', 'Studio Notes').getAttribute('aria-expanded')) === 'false',
    'workspaces start collapsed'
  );
  await shot('1-tree-initial');

  // --- Expand a workspace by its caret: sections load, nothing opens -----
  const before = contentRequests().length;
  const started = Date.now();
  await rowByKind('workspace', 'Studio Notes').locator('[data-tree-toggle]').click();
  await rowByKind('note', 'Weekly review').waitFor();
  const elapsed = Date.now() - started;
  await settle(300);
  check(contentRequests().length - before === 5, 'expanding a workspace fetches its 5 sections');
  check(elapsed < 1000, `contents appear quickly (${elapsed}ms)`);
  check((await page.locator('.cockpit-pane-tab').count()) === 0, 'the caret opens no tab');
  const sections = await page
    .locator(
      '#cockpitTreeNav [data-tree-row][data-parent-id]:is([data-tree-kind="section"],[data-tree-kind="memory"])'
    )
    .evaluateAll(
      (els, parent) =>
        els
          .filter(el => el.getAttribute('data-parent-id') === parent)
          .map(el => el.querySelector('.cockpit-tree-name').textContent),
      await rowByKind('workspace', 'Studio Notes').getAttribute('data-tree-row')
    );
  check(
    sections.join(',') === 'Notes,Backlog,Files,Memory,Agents',
    `sections in order (${sections.join(',')})`
  );
  await shot('2-workspace-expanded');

  // --- Open a folder to depth ---------------------------------------------
  await rowByKind('folder', 'stems').click();
  await rowByKind('folder', 'takes').click();
  check((await rowByKind('file', 'lead-vocal.wav').count()) === 1, 'folders open to any depth');
  check(
    (await page.locator('.cockpit-pane-tab').count()) === 0,
    'folders and sections open no tab'
  );

  // --- Open a note ---------------------------------------------------------
  await rowByKind('note', 'Weekly review').click();
  await page.locator('#cockpitPaneNoteEditor .note-live-line').first().waitFor();
  const pane = await page.locator('.cockpit-pane-article').innerText();
  check(
    /Studio Notes\s*\/\s*Notes\s*\/\s*Weekly review/.test(pane),
    'breadcrumb reads Studio Notes / Notes / Weekly review'
  );
  check(pane.includes('Note in Studio Notes'), 'the pane says "Note in Studio Notes"');
  check(
    (await page.locator('#cockpitPaneNoteEditor h2').count()) === 3,
    'the note is rendered Markdown'
  );
  check(
    (await rowByKind('note', 'Weekly review').getAttribute('aria-selected')) === 'true',
    "the open note's row is highlighted"
  );
  check(new URL(page.url()).pathname === '/', 'no page navigation');
  await shot('3-note-open');

  // --- A second item, then the same one again: no duplicate tab ----------
  await rowByKind('note', 'Studio ideas').click();
  await rowByKind('note', 'Weekly review').click();
  check(
    (await page.locator('.cockpit-pane-tab').count()) === 2,
    'reopening an item switches to its tab (2 tabs)'
  );

  // --- A workspace name opens its tab and expands it ---------------------
  await rowByKind('workspace', 'Night Drive').locator('.cockpit-tree-name').click();
  await rowByKind('note', 'Lyrics draft').waitFor();
  check(
    (await rowByKind('workspace', 'Night Drive').getAttribute('aria-expanded')) === 'true',
    'a workspace name expands it'
  );
  check(
    (await page.locator('.cockpit-pane-tab.is-active').innerText()).includes('Night Drive'),
    '…and opens its tab'
  );
  check(
    (await page.locator('#cockpitContextModal.show').count()) === 0,
    'selecting in Tree does not open the context modal'
  );
  await shot('4-workspace-tab');

  // --- The 100-row cap, an empty workspace, a group's note ---------------
  await rowByKind('workspace', 'Archive').locator('[data-tree-toggle]').click();
  await row('Open workspace to see all 105').waitFor();
  check(true, 'a 105-note section ends with "Open workspace to see all 105"');
  await rowByKind('workspace', 'Archive').locator('[data-tree-toggle]').click();
  await rowByKind('workspace', 'Empty Shelf').locator('[data-tree-toggle]').click();
  await row('No notes yet').waitFor();
  check(true, 'an empty workspace says "No notes yet"');
  await row('No notes yet').scrollIntoViewIfNeeded();
  await shot('5-empty-workspace');

  // --- Keyboard: arrows move, Enter opens and focuses the pane title -----
  await rowByKind('note', 'Release plan').focus();
  const tabsBefore = await page.locator('.cockpit-pane-tab').count();
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('ArrowUp');
  check(
    (await page.locator('.cockpit-pane-tab').count()) === tabsBefore,
    'arrow keys open nothing'
  );
  await page.keyboard.press('Enter');
  await page.locator('#cockpitPaneNoteEditor .note-live-line').first().waitFor();
  check(
    await page.evaluate(
      () => document.activeElement && document.activeElement.hasAttribute('data-pane-title')
    ),
    'Enter opens the item and moves focus to the pane title'
  );
  check(
    (await page.locator('.cockpit-pane-sub').innerText()).includes('Note in Music'),
    'a note that lives in a group opens'
  );
  check(
    (await page.locator("#cockpitTreeNav [data-tree-row][tabindex='0']").count()) === 1,
    'exactly one row is in the tab order'
  );
  await shot('6-group-note');

  // --- Header actions are present and named ------------------------------
  for (const label of ['Create Workspace', 'Create Group', 'Import Folder', 'Rescan', 'Undo']) {
    check(
      (await page.locator('#cockpitTree').getByRole('button', { name: label }).count()) === 1,
      `header action "${label}" is present`
    );
  }
  check(
    (await page.locator('#cockpitTree').getByRole('link', { name: 'Manage directory' }).count()) ===
      1,
    'header action "Manage directory" is present'
  );

  // --- Header actions still act --------------------------------------------
  await page.locator('#cockpitTree').getByRole('button', { name: 'Create Group' }).click();
  await page.locator('#addFolderModal').waitFor({ state: 'visible' });
  check(
    await page.locator('#workspaceCreatorKindGroup').isChecked(),
    'Create Group opens the shared creator with Group chosen'
  );
  await settle(300);
  await page.evaluate(() =>
    window.bootstrap.Modal.getInstance(document.getElementById('addFolderModal'))?.hide()
  );
  await page.locator('#addFolderModal').waitFor({ state: 'hidden' });
  const rescanned = page.waitForResponse(response =>
    response.url().endsWith('/api/workspaces/rescan')
  );
  await page.locator('#cockpitTree').getByRole('button', { name: 'Rescan' }).click();
  check((await rescanned).ok(), 'Rescan asks the server to rescan');
  await settle(600);

  // --- Tag filter chips ----------------------------------------------------
  const topLevelNames = () =>
    page
      .locator('#cockpitTreeNav [data-tree-row][aria-level="1"] > .cockpit-tree-name')
      .allInnerTexts();
  const allNames = await topLevelNames();
  await page.locator('#cockpitTreeNav [data-tree-tag-filter="archive"]').click();
  check(
    (await topLevelNames()).join(',') === 'Archive',
    `the "archive" tag chip narrows the tree to Archive (${(await topLevelNames()).join(',')})`
  );
  await page.locator('#cockpitTreeNav [data-tree-tag-clear]').click();
  check(
    (await topLevelNames()).join(',') === allNames.join(','),
    'Clear tag filters brings every row back'
  );

  // --- Drag to move (workspace and group rows only) ------------------------
  const dragRowTo = async (source, target, fraction) => {
    await source.scrollIntoViewIfNeeded();
    const from = await source.boundingBox();
    await page.mouse.move(from.x + 80, from.y + from.height / 2);
    await page.mouse.down();
    await page.mouse.move(from.x + 90, from.y + from.height / 2 + 5, { steps: 3 });
    const to = await target.boundingBox();
    await page.mouse.move(to.x + 80, to.y + to.height * fraction, { steps: 10 });
    await settle(150);
    await page.mouse.up();
    await settle(900);
  };
  const parentOf = name => rowByKind('workspace', name).getAttribute('data-parent-id');
  const musicId = await rowByKind('group', 'Music').getAttribute('data-tree-row');

  // Close the open rows so source and target are both on screen.
  for (const name of ['Studio Notes', 'Night Drive', 'Empty Shelf']) {
    const item = rowByKind('workspace', name);
    if ((await item.getAttribute('aria-expanded')) === 'true') {
      await item.locator('[data-tree-toggle]').click();
    }
  }
  await dragRowTo(rowByKind('workspace', 'Empty Shelf'), rowByKind('group', 'Music'), 0.5);
  check(
    (await parentOf('Empty Shelf')) === musicId,
    'dragging a workspace onto a group moves it in'
  );
  await shot('7-dragged-into-group');

  // A drop on the top edge of a top-level row puts it back at the top level.
  await dragRowTo(rowByKind('workspace', 'Empty Shelf'), rowByKind('workspace', 'Archive'), 0.2);
  check(
    (await parentOf('Empty Shelf')) === '',
    'dragging it above a top-level row moves it back out'
  );

  // An illegal move: a group cannot go into itself. Nothing is sent.
  const patchesBefore = requests.filter(entry => entry.startsWith('PATCH ')).length;
  await dragRowTo(rowByKind('group', 'Music'), rowByKind('workspace', 'Harbor Lights'), 0.5);
  check(
    requests.filter(entry => entry.startsWith('PATCH ')).length === patchesBefore &&
      (await rowByKind('group', 'Music').getAttribute('data-parent-id')) === '',
    'a group cannot be dropped inside itself; no request is sent'
  );

  // Content rows cannot be dragged at all (FR62).
  await rowByKind('workspace', 'Studio Notes').locator('[data-tree-toggle]').click();
  await rowByKind('note', 'hello').waitFor();
  check(
    (await rowByKind('note', 'hello').getAttribute('draggable')) === null,
    'a note row is not draggable'
  );

  // --- Back to Map: nothing new is fetched -------------------------------
  const contentBefore = contentRequests().length;
  await page.locator('#cockpitViewMap').click();
  await page.locator('#cockpitMap').waitFor({ state: 'visible' });
  await settle(1500);
  check(
    contentRequests().length === contentBefore,
    'switching to Map makes no new content requests'
  );
  check(
    mapRequestCount > 0,
    `Map view's own requests are unchanged in kind (${mapRequestCount} before Tree was opened)`
  );
  await shot('8-map-after');
}

// Records every API request Home makes in Map view, with ids replaced, so the
// list from this build can be diffed against the same list from origin/dev
// (start that build with `./scripts/demo-server.sh --rev origin/dev …`). The
// fourth argument names the output file rather than a theme:
//   … map-requests branch    → <outDir>/map-requests-branch.txt
async function stageMapRequests() {
  await page.goto(`${baseUrl}/`, { waitUntil: 'domcontentloaded' });
  await page.locator('#cockpitMap').waitFor({ state: 'visible' });
  await settle(5000);
  const normalized = requests
    .filter(entry => entry.includes(' /api/'))
    .map(entry => entry.replace(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f-]{22}/g, ':id'))
    .sort();
  const file = join(outDir, `map-requests-${theme}.txt`);
  writeFileSync(file, `${normalized.join('\n')}\n`);
  console.log(`${normalized.length} API request(s) in Map view written to ${file}`);
}

// Opens Home in Tree view with nothing remembered from an earlier stage.
async function openTree() {
  await page.goto(`${baseUrl}/?view=tree`, { waitUntil: 'domcontentloaded' });
  await page.locator('#cockpitTreeNav [data-tree-row]').first().waitFor();
  await applyTheme();
  await settle(600);
}

async function expand(kind, name) {
  const item = rowByKind(kind, name);
  if ((await item.getAttribute('aria-expanded')) !== 'true') {
    await item.locator('[data-tree-toggle]').click();
  }
}

const article = () => page.locator('#cockpitTreePane .cockpit-pane-article');
const activeTab = () => page.locator('#cockpitTreePane .cockpit-pane-tab.is-active');
const action = label =>
  page.locator('#cockpitTreePane .cockpit-pane-actions').getByText(label, { exact: true });

// Group 2: one of each kind in the pane, and how tabs behave.
async function stagePane() {
  await openTree();
  await expand('workspace', 'Studio Notes');
  await rowByKind('ticket', 'Reply to the mastering').waitFor();

  // --- Ticket (FR33) -------------------------------------------------------
  await rowByKind('ticket', 'Reply to the mastering').click();
  await page.locator('.cockpit-pane-lead').waitFor();
  let text = await article().innerText();
  check(text.includes('Ticket in Studio Notes'), 'ticket: says "Ticket in Studio Notes"');
  check(
    (await page.locator('.cockpit-pane-chip').innerText()) === 'Ready',
    'ticket: shows its state'
  );
  check(text.includes('They asked which mix to master'), 'ticket: shows its description');
  check(text.includes('#mix'), 'ticket: shows its tags');
  check(text.includes('Added by you'), 'ticket: shows its source in words');
  check(
    /\/workspaces\/studio-notes\?ticket=[0-9a-f-]{36}$/.test(
      await action('Open in Tickets').getAttribute('href')
    ),
    'ticket: "Open in Tickets" links to the workspace page with the ticket open'
  );
  await shot('p1-ticket');

  // --- Files (FR34) --------------------------------------------------------
  await expand('folder', 'briefs');
  await rowByKind('file', '2026-10-04.md').click();
  await page.locator('.cockpit-pane-markdown h1').waitFor();
  text = await article().innerText();
  check(text.includes('files/briefs/'), 'file: shows its path inside the workspace');
  check((await page.locator('.cockpit-pane-markdown h2').count()) === 2, 'Markdown file: rendered');
  const opened = page.waitForResponse(response => response.url().endsWith('/files/open'));
  await action('Open').click();
  const openResponse = await opened;
  check(
    openResponse.ok() &&
      /briefs\/.*2026-10-04\.md/.test(openResponse.request().postDataJSON().relative_path),
    `file: Open posts the file's path (${openResponse.status()})`
  );
  const revealed = page.waitForResponse(response => response.url().endsWith('/files/reveal'));
  await action('Reveal in Finder').click();
  check((await revealed).ok(), 'file: Reveal in Finder posts too');
  await shot('p2-markdown-file');

  await rowByKind('file', 'tempo.csv').click();
  await page.locator('.cockpit-pane-pre').waitFor();
  check(
    (await page.locator('.cockpit-pane-pre').innerText()).includes('Night Drive,92'),
    'text file: shown as plain text'
  );

  await rowByKind('file', 'cover.png').click();
  await page.locator('.cockpit-pane-image').waitFor();
  check(
    await page
      .locator('.cockpit-pane-image')
      .evaluate(image => image.complete && image.naturalWidth > 0),
    'image: shown inline'
  );
  await shot('p3-image');

  await expand('folder', 'stems');
  await expand('folder', 'takes');
  await rowByKind('file', 'lead-vocal.wav').click();
  await page.locator('.cockpit-pane-article[data-pane-kind="file"] .cockpit-pane-note').waitFor();
  check((await article().innerText()).includes('No preview'), 'other file: "No preview"');
  await shot('p4-no-preview');

  // BACKLOG.md lives in the group's files and carries the sync line.
  await rowByKind('file', 'BACKLOG.md').click();
  await page.locator('.cockpit-pane-lead').waitFor();
  check(
    (await page.locator('.cockpit-pane-lead').innerText()) ===
      'Ori keeps this file in step with the backlog.',
    'BACKLOG.md: says Ori keeps it in step with the backlog'
  );

  // --- Memory and agent (FR37, FR38) ---------------------------------------
  await rowByKind('memory', 'Memory').first().click();
  await page.locator('.cockpit-pane-list li').first().waitFor();
  check((await page.locator('.cockpit-pane-list li').count()) === 3, 'memory: lists its 3 entries');
  check(
    (await action('Open Memory').getAttribute('href')) === '/workspaces/studio-notes#memory',
    'memory: "Open Memory" goes to the Memory tab'
  );
  await shot('p5-memory');

  await rowByKind('agent', 'Scout').click();
  await page.locator('.cockpit-pane-article[data-pane-kind="agent"]').waitFor();
  text = await article().innerText();
  check(
    text.includes('Agent in Studio Notes') && /Role\s+\S+/.test(text) && /Model\s+\S+/.test(text),
    'agent: shows name, role and model'
  );
  check(
    (await action('Open agent').getAttribute('href')) === '/workspaces/studio-notes/agents/Scout',
    'agent: "Open agent" links to the agent'
  );
  await shot('p6-agent');

  // --- Workspace overview (FR39) -------------------------------------------
  await rowByKind('workspace', 'Studio Notes').locator('.cockpit-tree-name').click();
  await page.locator('.cockpit-pane-article[data-pane-kind="workspace"]').waitFor();
  await settle(400);
  text = await article().innerText();
  check(
    /Workspace/.test(await page.locator('.cockpit-pane-sub').innerText()),
    'overview: says it is a workspace'
  );
  check((await page.locator('.cockpit-pane-chip').count()) === 1, 'overview: shows the status');
  check(
    (await page.locator('.cockpit-pane-stat').count()) === 3 && /Next run/i.test(text),
    'overview: agents, open tasks, needs attention, and the next run'
  );
  check(text.includes('#music') && text.includes('#home'), 'overview: shows the tags');
  const links = await page.locator('.cockpit-pane-link').allInnerTexts();
  check(
    links.map(entry => entry.split('\n')[0]).join(',') === 'Notes,Backlog,Files,Memory,Agents',
    `overview: lists the sections with counts (${links.map(entry => entry.replace(/\s+/g, ' ')).join(' | ')})`
  );
  for (const label of ['Open workspace', 'Move…', 'Delete']) {
    check((await action(label).count()) === 1, `overview: has the "${label}" button`);
  }
  await shot('p7-workspace-overview');

  // A section link reveals that section in the tree and moves focus to it.
  await rowByKind('section', 'Backlog').first().locator('[data-tree-toggle]').click();
  await page.locator('.cockpit-pane-link', { hasText: 'Backlog' }).click();
  await settle(300);
  check(
    await page.evaluate(
      () =>
        document.activeElement?.getAttribute('data-tree-kind') === 'section' &&
        document.activeElement.getAttribute('aria-expanded') === 'true' &&
        document.activeElement.textContent.includes('Backlog')
    ),
    'overview: a section link opens that section in the tree and focuses it'
  );
  await page.locator('.cockpit-pane-link', { hasText: 'Memory' }).click();
  await page.locator('.cockpit-pane-article[data-pane-kind="memory"]').waitFor();
  check(true, 'overview: the Memory link opens Memory');

  // Move… opens the existing Move dialog.
  await page.locator('.cockpit-pane-tab-label', { hasText: 'Studio Notes' }).click();
  await action('Move…').click();
  await page.locator('[data-tree-move-dialog] [data-tree-move-to]').first().waitFor();
  check(
    (await page.locator('[data-tree-move-dialog]').innerText()).includes('Music'),
    'overview: Move… opens the Move dialog with its destinations'
  );
  await page.locator('[data-tree-move-cancel]').click();

  // --- Group overview (FR40) -----------------------------------------------
  await rowByKind('group', 'Music').locator('.cockpit-tree-name').click();
  await page.locator('.cockpit-pane-article[data-pane-kind="group"]').waitFor();
  await settle(400);
  text = await article().innerText();
  check(
    text.includes('Night Drive') && text.includes('Harbor Lights'),
    'group overview: lists its workspaces'
  );
  check(
    text.toUpperCase().includes("THE GROUP'S OWN CONTENTS"),
    'group overview: lists its own sections'
  );
  for (const label of ['Open group', 'Move…', 'Delete']) {
    check((await action(label).count()) === 1, `group overview: has the "${label}" button`);
  }
  await shot('p8-group-overview');
  await page.locator('.cockpit-pane-link', { hasText: 'Night Drive' }).click();
  await page.locator('.cockpit-pane-article[data-pane-kind="workspace"]').waitFor();
  check(
    (await activeTab().innerText()).includes('Night Drive') &&
      (await page.locator('.cockpit-pane-sub').innerText()).includes('Workspace in Music'),
    "group overview: a child's link opens that workspace's overview"
  );

  // --- Selection stays shared with the Map (FR30) -------------------------
  const nightId = await rowByKind('workspace', 'Night Drive').getAttribute('data-tree-row');
  check(
    (await page.evaluate(() => window.OriHomeCockpit.getState().selectedId)) === nightId &&
      (await page.locator('#cockpitContextModal.show').count()) === 0,
    "selection: the workspace is Home's selection, and no context modal opened"
  );

  // --- Tabs (FR25-FR29) -----------------------------------------------------
  const tabNames = () => page.locator('.cockpit-pane-tab-label').allInnerTexts();
  const names = await tabNames();
  check(new Set(names).size === names.length, `tabs: no item has two tabs (${names.length} open)`);
  const strip = await page.locator('.cockpit-pane-tabs').evaluate(el => ({
    scrolls: el.scrollWidth > el.clientWidth,
    pageScrolls: document.documentElement.scrollWidth > window.innerWidth
  }));
  check(
    strip.scrolls && !strip.pageScrolls,
    'tabs: more tabs than fit scroll inside the strip, not the page'
  );
  await shot('p9-many-tabs');

  // Closing the active (last) tab activates the one to its left; closing a
  // middle active tab activates the one to its right.
  const before = await tabNames();
  await activeTab().locator('[data-pane-close]').click();
  check(
    (await activeTab().innerText()).trim() === before[before.length - 2],
    'tabs: closing the last tab activates the one to its left'
  );
  await page.locator('.cockpit-pane-tab-label').nth(1).click();
  const middle = await tabNames();
  await activeTab().locator('[data-pane-close]').click();
  check(
    (await activeTab().innerText()).trim() === middle[2],
    'tabs: closing a middle tab activates the one to its right'
  );

  // The open item's row is highlighted and its ancestors are opened (FR28).
  await rowByKind('group', 'Music').locator('[data-tree-toggle]').click();
  await rowByKind('workspace', 'Studio Notes').locator('[data-tree-toggle]').click();
  const target = (await tabNames()).findIndex(name => name.includes('tempo.csv'));
  await page.locator('.cockpit-pane-tab-label').nth(target).click();
  await rowByKind('file', 'tempo.csv').waitFor();
  check(
    (await rowByKind('file', 'tempo.csv').getAttribute('aria-selected')) === 'true' &&
      (await rowByKind('workspace', 'Studio Notes').getAttribute('aria-expanded')) === 'true',
    'tabs: switching to a tab opens its ancestors and highlights its row'
  );

  // Closing every tab leaves the empty state.
  while ((await page.locator('.cockpit-pane-tab').count()) > 0) {
    await page.locator('.cockpit-pane-tab [data-pane-close]').first().click();
  }
  check(
    (await page.locator('.cockpit-pane-empty').innerText()).includes('Nothing open'),
    'tabs: with none open the pane says "Nothing open"'
  );

  // --- Map shows the same selection ---------------------------------------
  // Select a workspace in the Tree one more time, then switch: the Map must
  // show that workspace selected, and still no context modal.
  await rowByKind('workspace', 'Harbor Lights').locator('.cockpit-tree-name').click();
  const harborId = await rowByKind('workspace', 'Harbor Lights').getAttribute('data-tree-row');
  await page.locator('#cockpitViewMap').click();
  await page.locator('#cockpitMap').waitFor({ state: 'visible' });
  await settle(500);
  check(
    (await page.locator(`.ws-map-tile[data-ws-id="${harborId}"].is-selected`).count()) === 1 &&
      (await page.locator('.ws-map-tile.is-selected').count()) === 1 &&
      (await page.locator('#cockpitContextModal.show').count()) === 0,
    'selection: Map shows the workspace that was selected in the Tree'
  );
}

// Group 3: a note is edited in the pane with the existing editor.
async function stageNote() {
  const stamp = Date.now().toString(36);
  const editor = page.locator('#cockpitPaneNoteEditor');
  const saveLine = page.locator('[data-pane-save-status]');
  const notePut = response =>
    response.request().method() === 'PUT' && /\/api\/notes\/[0-9a-f-]+$/.test(response.url());
  const noteOnServer = async id =>
    (await (await page.request.get(`${baseUrl}/api/notes/${id}`)).json()).content;
  // Click a rendered line to edit it, go to its end, and type.
  const typeAtEndOfFirstLine = async words => {
    await editor.locator('.note-live-line-rendered').first().click();
    await editor.locator('.note-live-line-input').first().waitFor();
    await page.keyboard.press('End');
    await page.keyboard.type(words);
  };

  await openTree();
  await expand('workspace', 'Studio Notes');
  await rowByKind('note', 'hello').click();
  await editor.locator('.note-live-line').first().waitFor();
  const noteId = await editor.getAttribute('data-pane-editor');

  // --- The existing editor, one of it (FR41) -------------------------------
  check(
    (await editor.getAttribute('class')).includes('note-live-editor') &&
      (await page.locator('#cockpitTreePane .note-live-editor').count()) === 1,
    'the note is shown in the existing note editor, and there is one editor'
  );
  check(
    (await article().innerText()).includes('First note in this workspace.'),
    'the note text is shown'
  );
  check(
    /\/workspaces\/studio-notes\/notes\/[0-9a-f-]{36}$/.test(
      await action('Open full note').getAttribute('href')
    ),
    '"Open full note" links to the note page'
  );

  // --- Typing autosaves, with the note page's save states (FR42) -----------
  const firstSave = page.waitForResponse(notePut);
  await typeAtEndOfFirstLine(` Edited in the pane ${stamp}.`);
  await page.waitForFunction(
    () => document.querySelector('[data-pane-save-status]').textContent === 'Unsaved'
  );
  check(true, 'typing shows "Unsaved"');
  const saveResponse = await firstSave;
  check(saveResponse.ok(), `autosave PUTs /api/notes/{id} after the editor's own delay`);
  check(
    Object.keys(saveResponse.request().postDataJSON()).join(',') === 'content',
    'the save sends only the text'
  );
  await page.waitForFunction(
    () => document.querySelector('[data-pane-save-status]').textContent === 'Saved'
  );
  check(true, 'then "Saved"');
  await shot('n1-edited-and-saved');

  // --- Save before switching tab (FR43) ------------------------------------
  await page.keyboard.type(' Then switched tab.');
  check((await saveLine.innerText()) === 'Unsaved', 'more typing is unsaved again');
  const beforeSwitch = page.waitForResponse(notePut);
  await rowByKind('note', 'Studio ideas').click();
  check((await beforeSwitch).ok(), 'switching tab saves first, without waiting for the timer');
  await page.waitForFunction(() =>
    document.querySelector('.cockpit-pane-tab.is-active')?.textContent.includes('Studio ideas')
  );
  check((await noteOnServer(noteId)).includes('Then switched tab.'), 'the edit reached the server');
  check(
    (await page.locator('#cockpitTreePane .note-live-editor').count()) === 1 &&
      (await editor.getAttribute('data-pane-editor')) !== noteId,
    'the editor now holds the other note, and there is still only one'
  );

  // --- Save before closing the tab (FR43) ----------------------------------
  await page.locator('.cockpit-pane-tab-label', { hasText: 'hello' }).click();
  await page.waitForFunction(
    id => document.getElementById('cockpitPaneNoteEditor')?.getAttribute('data-pane-editor') === id,
    noteId
  );
  await editor.locator('.note-live-line').first().waitFor();
  await typeAtEndOfFirstLine(' Then closed the tab.');
  const beforeClose = page.waitForResponse(notePut);
  await activeTab().locator('[data-pane-close]').click();
  check((await beforeClose).ok(), 'closing the tab saves first');
  check((await noteOnServer(noteId)).includes('Then closed the tab.'), '…and the edit is kept');

  // --- Save before switching to Map (FR43) ---------------------------------
  await rowByKind('note', 'hello').click();
  await page.waitForFunction(
    id => document.getElementById('cockpitPaneNoteEditor')?.getAttribute('data-pane-editor') === id,
    noteId
  );
  await editor.locator('.note-live-line').first().waitFor();
  await typeAtEndOfFirstLine(' Then went to the Map.');
  const beforeMap = page.waitForResponse(notePut);
  await page.locator('#cockpitViewMap').click();
  check((await beforeMap).ok(), 'switching to Map saves first');
  await page.locator('#cockpitViewTree').click();
  await editor.locator('.note-live-line').first().waitFor();
  // The line being edited is a textarea, whose text is its value.
  const editorText = () =>
    editor.evaluate(el =>
      [el.innerText, ...Array.from(el.querySelectorAll('textarea'), input => input.value)].join(
        '\n'
      )
    );
  check(
    (await editorText()).includes('Then went to the Map.'),
    'back in Tree the note is as it was left'
  );

  // --- A failed save keeps the tab and the text, and offers Retry (FR44) ---
  // The 500 below is induced on purpose, so it is not a page problem.
  EXPECTED_FAILURES.push(/\/api\/notes\/[0-9a-f-]+$/);
  await page.route('**/api/notes/*', route =>
    route.request().method() === 'PUT'
      ? route.fulfill({
          status: 500,
          contentType: 'application/json',
          body: JSON.stringify({ code: 'internal_error', message: 'Failed to update note' })
        })
      : route.continue()
  );
  await typeAtEndOfFirstLine(' This save will fail.');
  await rowByKind('note', 'Studio ideas').click();
  await page.waitForFunction(() =>
    document.querySelector('[data-pane-save-status]').textContent.startsWith('Save failed')
  );
  check(
    (await activeTab().innerText()).includes('hello'),
    'a failed save keeps the note tab open instead of switching'
  );
  check(
    (await saveLine.innerText()) === 'Save failed: Failed to update note. Your text is kept.',
    `the save line says the save failed and why ("${await saveLine.innerText()}")`
  );
  check(
    (await editor.innerText()).includes('This save will fail.') ||
      (await editor.locator('.note-live-line-input').first().inputValue()).includes(
        'This save will fail.'
      ),
    "the user's text is still in the editor"
  );
  check(await page.locator('[data-pane-save-retry]').isVisible(), 'Retry is offered');
  await shot('n2-save-failed');
  await page.unroute('**/api/notes/*');
  const retried = page.waitForResponse(notePut);
  await page.locator('[data-pane-save-retry]').click();
  check((await retried).ok(), 'Retry saves');
  await page.waitForFunction(
    () => document.querySelector('[data-pane-save-status]').textContent === 'Saved'
  );
  check(
    (await noteOnServer(noteId)).includes('This save will fail.') &&
      (await page.locator('[data-pane-save-retry]').isHidden()),
    'after Retry the text is saved and Retry is gone'
  );

  // --- The same note open in another tab is reported (FR45) ----------------
  const other = await context.newPage();
  await other.goto(`${baseUrl}/workspaces/studio-notes/notes/${noteId}`, {
    waitUntil: 'domcontentloaded'
  });
  await other.locator('#notePreviewContent .note-live-line').first().waitFor();
  await rowByKind('note', 'Studio ideas').click();
  await page.waitForFunction(() =>
    document.querySelector('.cockpit-pane-tab.is-active')?.textContent.includes('Studio ideas')
  );
  await page.locator('.cockpit-pane-tab-label', { hasText: 'hello' }).click();
  await page.locator('[data-pane-note-elsewhere]:not([hidden])').waitFor();
  check(true, 'a note that is also open on the note page in another tab says so');
  await shot('n3-open-elsewhere');

  // --- Reload, the note page, and the file on disk all match ---------------
  const expected = await noteOnServer(noteId);
  check(
    (await other.locator('#noteContentInput').inputValue()) !== undefined,
    'the note page has the note open'
  );
  await other.reload({ waitUntil: 'domcontentloaded' });
  await other.locator('#notePreviewContent .note-live-line').first().waitFor();
  check(
    (await other.locator('#noteContentInput').inputValue()) === expected,
    'the note page shows exactly what the pane saved'
  );
  await other.close();

  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.locator('#cockpitTreeNav [data-tree-row]').first().waitFor();
  await applyTheme();
  await expand('workspace', 'Studio Notes');
  await rowByKind('note', 'hello').click();
  await editor.locator('.note-live-line').first().waitFor();
  check(
    (await page.evaluate(() => document.getElementById('cockpitPaneNoteEditor').innerText))
      .replace(/\s+/g, ' ')
      .includes(`Edited in the pane ${stamp}. Then switched tab. Then closed the tab.`),
    'after a reload the pane shows the edited note'
  );
  await shot('n4-after-reload');

  if (sandbox) {
    const dir = join(sandbox, 'Ori Workspaces', 'studio-notes', 'notes');
    const file = readdirSync(dir).find(name => name.startsWith('hello--'));
    const onDisk = file ? readFileSync(join(dir, file), 'utf8') : '';
    check(
      !!file && onDisk.includes(expected.trim()),
      `the note's file under notes/ holds the same text (${file || 'no file found'})`
    );
  } else {
    console.log('skip the on-disk check: no sandbox directory was given');
  }

  // --- Leaving Home with unsaved text (FR43) -------------------------------
  await typeAtEndOfFirstLine(' Last words before leaving.');
  await Promise.all([
    page.waitForURL(/\/workspaces\/studio-notes\/notes\//),
    action('Open full note').click()
  ]);
  await page.locator('#notePreviewContent .note-live-line').first().waitFor();
  await expectEventually(
    async () => (await noteOnServer(noteId)).includes('Last words before leaving.'),
    'leaving Home sends a last save, and the note page has the words'
  );
}

async function expectEventually(probe, message, timeoutMs = 4000) {
  const deadline = Date.now() + timeoutMs;
  let ok = await probe();
  while (!ok && Date.now() < deadline) {
    await settle(150);
    ok = await probe();
  }
  check(ok, message);
}

// A group created the way the Create Group dialog creates one, for a group
// that starts with no notes of its own.
async function createGroupByAPI(name) {
  const plan = await (
    await page.request.post(`${baseUrl}/api/workspaces/template-agent-plan`, {
      data: { group_roster: true, group_name: name }
    })
  ).json();
  const manager = plan.agents[0];
  const created = await (
    await page.request.post(`${baseUrl}/api/workspaces`, {
      data: {
        name,
        kind: 'group',
        group_roster: true,
        create_template_agents: true,
        template_agent_review: {
          version: 1,
          plan_revision: plan.revision,
          expectations: [{ index: 0, name: manager.name, action: manager.action }]
        }
      }
    })
  ).json();
  return created.folder.id;
}

// Group 4: creating a note, a ticket and an upload from the tree.
async function stageCreate() {
  const stamp = Date.now().toString(36);
  const menu = page.locator('[data-tree-menu]');
  const draft = page.locator('#cockpitTreeNav [data-tree-draft]');
  const live = () => page.locator('#cockpitRailLive').innerText();
  const posts = suffix =>
    requests.filter(entry => entry.startsWith('POST ') && entry.endsWith(suffix)).length;
  // This stage makes its own workspace and group and removes them at the end,
  // so it never changes what the other stages find in the seeded sandbox.
  const groupName = `Bare Group ${stamp}`;
  const groupId = await createGroupByAPI(groupName);
  const shelfName = `Scratch ${stamp}`;
  const shelfCreated = await (
    await page.request.post(`${baseUrl}/api/workspaces`, { data: { name: shelfName } })
  ).json();
  const scratchId = shelfCreated.folder.id;
  // One folder under files/, so a folder's own menu can be tried.
  await page.request.post(`${baseUrl}/api/workspaces/${scratchId}/files`, {
    multipart: {
      folder_path: 'inbox',
      file: { name: 'first.txt', mimeType: 'text/plain', buffer: Buffer.from('first\n') }
    }
  });
  const cleanUp = async () => {
    await page.request.delete(
      `${baseUrl}/api/workspaces/${groupId}?confirm=true&delete_mode=group_only`
    );
    await page.request.delete(`${baseUrl}/api/workspaces/${scratchId}?confirm=true`);
  };
  try {
    await runCreateStage({
      stamp,
      menu,
      draft,
      live,
      posts,
      groupName,
      groupId,
      shelfName,
      scratchId
    });
  } finally {
    await cleanUp();
  }
}

async function runCreateStage(options) {
  const { stamp, menu, draft, live, posts, groupName, groupId, shelfName, scratchId } = options;
  // The scratch workspace's own section rows, by key: other workspaces have
  // sections with the same names.
  const section = name => page.locator(`#cockpitTreeNav [data-tree-row="${scratchId}/s/${name}"]`);
  await openTree();

  // --- The header's New note needs a workspace in context (FR50) ----------
  const newNote = page.locator('#cockpitTreeNav [data-tree-new-note]');
  check(
    (await newNote.getAttribute('aria-disabled')) === 'true' &&
      (await newNote.getAttribute('title')) === 'Pick a workspace first',
    'with no workspace in context the New note button is disabled and says to pick one'
  );

  // --- New note from a workspace's right-click menu (FR47) ----------------
  const shelf = rowByKind('workspace', shelfName);
  await shelf.click({ button: 'right' });
  await menu.waitFor();
  check(
    (await menu.locator('[role="menuitem"]').allInnerTexts()).join(',') ===
      'Open workspace,New note,New ticket,Upload file…,Refresh,Move…,Delete',
    "right-click opens the workspace's menu, from Open workspace to Delete"
  );
  await shot('c1-row-menu');
  await menu.getByRole('menuitem', { name: 'New note' }).click();
  await draft.waitFor();
  // Asked of the document, not of an element handle: the tree redraws as the
  // workspace's sections load, and each redraw makes a new input.
  const draftFocused = () =>
    page.evaluate(() => !!document.activeElement?.hasAttribute('data-tree-draft'));
  // Wait until a note has been created, opened and given the cursor, so the
  // next step never starts while that is still happening.
  const editorHasCursor = () =>
    page.evaluate(() => {
      const active = document.activeElement;
      return (
        !!active && !!active.closest('#cockpitPaneNoteEditor') && active.tagName === 'TEXTAREA'
      );
    });
  check(
    await draftFocused(),
    'New note adds an editable row under Notes and puts the cursor in it'
  );
  check(
    (await shelf.getAttribute('aria-expanded')) === 'true',
    'the workspace opens to show the row'
  );
  const noteName = `Session log ${stamp}`;
  await draft.fill(noteName);
  await shot('c2-naming-a-note');
  await draft.press('Enter');
  await rowByKind('note', noteName).waitFor();
  await page.locator('#cockpitPaneNoteEditor .note-live-line').first().waitFor();
  check((await draft.count()) === 0, 'Enter creates the note and the naming row goes away');
  check((await activeTab().innerText()).includes(noteName), 'the new note opens in the pane');
  await expectEventually(editorHasCursor, 'the cursor is in the editor, ready to write');
  check((await live()).includes(`Created note "${noteName}"`), 'the result is announced');
  await page.keyboard.type('First line of a new note.');
  await page.waitForFunction(
    () => document.querySelector('[data-pane-save-status]')?.textContent === 'Unsaved'
  );
  check(true, 'typing straight away edits the new note');
  await shot('c3-note-created');

  // --- Escape cancels (FR47) ----------------------------------------------
  const before = posts('/notes');
  await section('notes').hover();
  await section('notes').locator('[data-tree-menu-for]').click();
  await menu.waitFor();
  check(
    (await menu.locator('[role="menuitem"]').allInnerTexts()).join(',') === 'New note',
    'the "⋯" button on Notes opens a menu with just New note'
  );
  await menu.getByRole('menuitem', { name: 'New note' }).click();
  await draft.fill('Never created');
  await draft.press('Escape');
  check(
    (await draft.count()) === 0 && posts('/notes') === before,
    'Escape cancels: no row is left and nothing was sent'
  );

  // --- An empty name becomes "Untitled", from the header button (FR47) ----
  await section('notes').focus();
  check(
    (await newNote.getAttribute('aria-disabled')) === 'false',
    'with a row focused the header New note button is enabled'
  );
  await newNote.click();
  await draft.waitFor();
  const untitled = page.waitForResponse(
    response => response.request().method() === 'POST' && response.url().endsWith('/notes')
  );
  await draft.press('Enter');
  const untitledBody = (await untitled).request().postDataJSON();
  check(untitledBody.name === 'Untitled', 'an empty name creates a note called "Untitled"');
  await page.waitForFunction(() =>
    document.querySelector('.cockpit-pane-tab.is-active')?.textContent.includes('Untitled')
  );
  await expectEventually(editorHasCursor, '…which opens with the cursor in the editor too');

  // --- New ticket, by keyboard (FR48, FR54) -------------------------------
  await section('backlog').focus();
  await page.keyboard.press('Shift+F10');
  await menu.waitFor();
  check(
    await page.evaluate(() => document.activeElement?.getAttribute('role') === 'menuitem'),
    'Shift+F10 opens the row menu with focus on its first item'
  );
  await page.keyboard.press('Enter');
  await draft.waitFor();
  const ticketTitle = `Tune the snare ${stamp}`;
  await page.keyboard.type(ticketTitle);
  const ticketPosted = page.waitForResponse(
    response => response.request().method() === 'POST' && response.url().endsWith('/tickets')
  );
  await page.keyboard.press('Enter');
  const ticketBody = (await ticketPosted).request().postDataJSON();
  check(
    ticketBody.state === 'backlog' &&
      ticketBody.source === 'manual' &&
      ticketBody.title === ticketTitle,
    'the ticket is created in state backlog with the manual source'
  );
  await rowByKind('ticket', ticketTitle).waitFor();
  await page.locator('.cockpit-pane-article[data-pane-kind="ticket"]').waitFor();
  check(
    (await rowByKind('ticket', ticketTitle).innerText()).includes('Backlog') &&
      (await activeTab().innerText()).includes(ticketTitle),
    'the ticket appears under Backlog and opens in the pane'
  );

  // --- A refused create shows the reason and changes nothing (FR51) -------
  EXPECTED_FAILURES.push(/\/api\/workspaces\/[0-9a-f-]+\/tickets$/);
  await section('backlog').focus();
  await page.keyboard.press('Shift+F10');
  await menu.waitFor();
  await page.keyboard.press('Enter');
  await draft.waitFor();
  const ticketRows = await page.locator('#cockpitTreeNav [data-tree-kind="ticket"]').count();
  await draft.press('Enter');
  await page.waitForFunction(() =>
    document.getElementById('cockpitRailLive').textContent.includes("Couldn't create the ticket")
  );
  check(
    (await live()) === "Couldn't create the ticket: title is required",
    `a refused create shows the server's reason ("${await live()}")`
  );
  check(
    (await page.locator('#cockpitTreeNav [data-tree-kind="ticket"]').count()) === ticketRows &&
      (await draft.count()) === 1 &&
      (await draftFocused()),
    'the tree is unchanged, and the naming row is still there to retry or cancel'
  );
  await shot('c4-create-refused');
  await draft.press('Escape');

  // --- Upload file… (FR49) -------------------------------------------------
  const pickAndUpload = async (rowLocator, name, content) => {
    await rowLocator.click({ button: 'right' });
    await menu.waitFor();
    const chooser = page.waitForEvent('filechooser');
    await menu.getByRole('menuitem', { name: 'Upload file…' }).click();
    await (
      await chooser
    ).setFiles({
      name,
      mimeType: 'text/markdown',
      buffer: Buffer.from(content)
    });
  };
  const fileName = `setlist-${stamp}.md`;
  await pickAndUpload(section('files'), fileName, '# Setlist\n\n- Night Drive\n');
  await rowByKind('file', fileName).waitFor();
  await page.locator('.cockpit-pane-markdown h1').waitFor();
  check(
    (await activeTab().innerText()).includes(fileName),
    'an uploaded file appears under Files and opens in the pane'
  );
  check(
    (await page.locator('.cockpit-pane-fields').innerText()).includes(fileName),
    'at the top of files/'
  );

  // …into a folder, from the folder's own menu.
  const folderFile = `notes-${stamp}.md`;
  await pickAndUpload(rowByKind('folder', 'inbox'), folderFile, '# In a folder\n');
  await rowByKind('file', folderFile).waitFor();
  check(
    /files\/inbox\/[0-9a-f]+_notes-/.test(await page.locator('.cockpit-pane-fields').innerText()) &&
      (await rowByKind('folder', 'inbox').getAttribute('aria-expanded')) === 'true',
    'a folder menu uploads into that folder, which opens to show the file'
  );
  await shot('c5-uploaded');

  // --- A note in a group whose Notes section was hidden (FR52) ------------
  const group = rowByKind('group', groupName);
  await group.scrollIntoViewIfNeeded();
  check(
    (await page.locator(`[data-tree-row="${groupId}/s/notes"]`).count()) === 0,
    'a group with no notes shows no Notes section'
  );
  await group.click({ button: 'right' });
  await menu.getByRole('menuitem', { name: 'New note' }).click();
  await draft.waitFor();
  check(
    (await page.locator(`[data-tree-row="${groupId}/s/notes"]`).count()) === 1,
    'naming a note in it makes the Notes section appear'
  );
  const groupNote = `Group plan ${stamp}`;
  await draft.fill(groupNote);
  await draft.press('Enter');
  await rowByKind('note', groupNote).waitFor();
  await page.locator('#cockpitPaneNoteEditor').waitFor();
  check(
    (await page.locator(`[data-tree-row="${groupId}/s/notes"] .cockpit-tree-count`).innerText()) ===
      '1' && (await page.locator('.cockpit-pane-sub').innerText()).includes(`Note in ${groupName}`),
    'the note is created in the group, and its Notes section stays, with a count of 1'
  );
  await shot('c6-note-in-a-group');

  // --- Refresh from the menu (FR21) ---------------------------------------
  const shelfId = await shelf.getAttribute('data-tree-row');
  const outside = `Added elsewhere ${stamp}`;
  await page.request.post(`${baseUrl}/api/workspaces/${shelfId}/notes`, {
    data: { name: outside, content: 'Made by another window.' }
  });
  check((await rowByKind('note', outside).count()) === 0, 'a note made elsewhere is not shown yet');
  await shelf.scrollIntoViewIfNeeded();
  await shelf.click({ button: 'right' });
  await menu.getByRole('menuitem', { name: 'Refresh' }).click();
  await rowByKind('note', outside).waitFor();
  check(true, 'Refresh in the menu reloads the row and the note appears');

  // --- A redraw of the tree leaves an open menu alone ---------------------
  // The tree redraws whenever a section finishes loading or the workspace
  // list reloads, and puts its own scroll position back each time. With the
  // tree scrolled, that used to close a menu that had only just been opened.
  const scroller = page.locator('#cockpitTreeNav .cockpit-tree-scroll');
  await scroller.evaluate(el => {
    el.scrollTop = 40;
  });
  await settle(200);
  await shelf.click({ button: 'right' });
  await menu.waitFor();
  await page.evaluate(() => window.OriHomeCockpit.refreshQuietly());
  await settle(500);
  check(
    (await menu.count()) === 1 &&
      (await page.evaluate(() => document.activeElement?.getAttribute('role') === 'menuitem')) &&
      (await scroller.evaluate(el => el.scrollTop)) > 0,
    'a redraw of the scrolled tree leaves the open menu, and its focus, alone'
  );
  await page.keyboard.press('Escape');
  check(
    await page.evaluate(
      () => document.activeElement?.getAttribute('data-tree-kind') === 'workspace'
    ),
    '…and Escape still returns focus to the row, though its element was replaced'
  );
  await scroller.evaluate(el => {
    el.scrollTop = 0;
  });

  // --- Dismissal ------------------------------------------------------------
  await shelf.click({ button: 'right' });
  await menu.waitFor();
  await page.keyboard.press('Escape');
  check(
    (await menu.count()) === 0 && (await shelf.evaluate(el => el === document.activeElement)),
    'Escape closes the menu and returns focus to its row'
  );
  await shelf.click({ button: 'right' });
  await menu.waitFor();
  await page.locator('#cockpitTreePane').click({ position: { x: 300, y: 300 } });
  check((await menu.count()) === 0, 'a click elsewhere closes the menu');
}

// Group 5: the management tools, now in the row menu and multi-select.
async function stageManage() {
  const stamp = Date.now().toString(36);
  const names = {
    group: `Crate ${stamp}`,
    alpha: `Alpha ${stamp}`,
    beta: `Beta ${stamp}`,
    gamma: `Gamma ${stamp}`,
    delta: `Delta ${stamp}`,
    made: `Picked ${stamp}`
  };
  const tags = { filter: `demo-${stamp}`, spare: `spare-${stamp}` };
  // Its own workspaces and group, removed at the end, so nothing it moves,
  // deletes or groups belongs to another stage.
  const create = async name =>
    (await (await page.request.post(`${baseUrl}/api/workspaces`, { data: { name } })).json()).folder
      .id;
  const ids = { group: await createGroupByAPI(names.group) };
  for (const key of ['alpha', 'beta', 'gamma', 'delta']) ids[key] = await create(names[key]);
  await page.request.patch(`${baseUrl}/api/workspaces/${ids.alpha}`, {
    data: { tags: [tags.filter, tags.spare] }
  });
  await page.request.post(`${baseUrl}/api/workspaces/${ids.gamma}/notes`, {
    data: { name: `Loose note ${stamp}`, content: 'A note, to show what cannot be dragged.' }
  });
  const madeGroups = [ids.group];
  try {
    await runManageStage({ stamp, names, ids, tags, madeGroups });
  } finally {
    // Groups first: removing only the group puts its workspaces back at the top.
    for (const id of madeGroups) {
      await page.request.delete(
        `${baseUrl}/api/workspaces/${id}?confirm=true&delete_mode=group_only`
      );
    }
    for (const key of ['alpha', 'beta', 'gamma', 'delta']) {
      await page.request.delete(`${baseUrl}/api/workspaces/${ids[key]}?confirm=true`);
    }
  }
}

async function runManageStage({ stamp, names, ids, tags, madeGroups }) {
  const menu = page.locator('[data-tree-menu]');
  const live = () => page.locator('#cockpitRailLive').innerText();
  const byId = id => page.locator(`#cockpitTreeNav [data-tree-row="${id}"]`);
  const parentOf = id => byId(id).getAttribute('data-parent-id');
  const patches = () => requests.filter(entry => entry.startsWith('PATCH /api/workspaces/')).length;
  const moveDialog = page.locator('[data-tree-move-dialog] [role="dialog"]');
  const bulkBar = page.locator('#cockpitTreeNav [data-tree-bulkbar]');
  const menuLabels = async () =>
    (await menu.locator('[role="menuitem"]').allInnerTexts()).join(',');
  await openTree();
  for (const key of ['group', 'alpha', 'beta', 'gamma', 'delta']) await byId(ids[key]).waitFor();
  await byId(ids.alpha).scrollIntoViewIfNeeded();

  // --- The slim row: no checkbox, no column of buttons (FR53) -------------
  check(
    (await page
      .locator(
        '#cockpitTreeNav [data-tree-check], #cockpitTreeNav .cockpit-tree-actions, #cockpitTreeNav .cockpit-tree-metrics'
      )
      .count()) === 0,
    'rows carry no checkbox, no metric cells and no buttons of their own'
  );
  check(
    (await byId(ids.alpha).evaluate(el => el.getBoundingClientRect().height)) <= 32,
    'a workspace row is one slim line'
  );

  // --- The header actions are still there, and named (FR53, FR64) ---------
  const toolbar = page.locator('#cockpitTreeNav .cockpit-tree-toolbar-actions');
  for (const label of ['New note', 'Create Workspace', 'Create Group', 'Import Folder', 'Rescan']) {
    check(
      (await toolbar.getByRole('button', { name: label, exact: true }).count()) === 1,
      `the header has a "${label}" button with that name`
    );
  }
  check(
    (await toolbar.getByRole('link', { name: 'Manage directory', exact: true }).count()) === 1 &&
      (await toolbar.getByRole('button', { name: 'Undo', exact: true }).isDisabled()),
    'and Manage directory, and an Undo that is disabled while there is nothing to undo'
  );

  // --- The menus (FR55) ----------------------------------------------------
  await byId(ids.gamma).locator('[data-tree-toggle]').click();
  const looseNote = rowByKind('note', `Loose note ${stamp}`);
  await looseNote.waitFor();
  await looseNote.click({ button: 'right' });
  await menu.waitFor();
  check(
    (await menuLabels()) === 'Open,Open in workspace',
    "a note's menu: Open, Open in workspace"
  );
  await page.keyboard.press('Escape');
  await byId(ids.group).click({ button: 'right' });
  await menu.waitFor();
  check(
    (await menuLabels()) === 'Open group,New note,New ticket,Upload file…,Refresh,Move…,Delete',
    "a group's menu: Open group, New note, New ticket, Upload file…, Refresh, Move…, Delete"
  );
  await page.keyboard.press('Escape');

  // --- Move… from the menu (FR56) ------------------------------------------
  await byId(ids.alpha).click({ button: 'right' });
  await menu.waitFor();
  await shot('m1-workspace-menu');
  await menu.getByRole('menuitem', { name: 'Move…', exact: true }).click();
  await moveDialog.waitFor();
  const destinations = await moveDialog.locator('[data-tree-move-to]').allInnerTexts();
  check(
    destinations.includes(names.group) && !destinations.includes('Top level'),
    'Move… opens the Move dialog: every group, and no "Top level" for a row already there'
  );
  check(
    await page.evaluate(() => !!document.activeElement?.closest('[data-tree-move-dialog]')),
    'focus moves into the dialog'
  );
  await shot('m2-move-dialog');
  // A redraw of the tree (a reload of the workspace list) must not close it.
  await page.evaluate(() => window.OriHomeCockpit.refreshQuietly());
  await settle(500);
  check((await moveDialog.count()) === 1, 'the dialog stays open while the tree redraws');
  await moveDialog.locator('[data-tree-move-to]', { hasText: names.group }).click();
  await expectEventually(
    async () => (await parentOf(ids.alpha)) === ids.group,
    'choosing the group moves the workspace into it'
  );
  check((await live()) === 'Workspace moved.', 'the move is announced');
  check((await moveDialog.count()) === 0, 'and the dialog closes');

  // Escape closes the dialog and goes back to the row.
  await byId(ids.alpha).focus();
  await page.keyboard.press('Shift+F10');
  await menu.getByRole('menuitem', { name: 'Move…', exact: true }).click();
  await moveDialog.waitFor();
  check(
    (await moveDialog.locator('[data-tree-move-to]').allInnerTexts()).includes('Top level'),
    'inside a group, the dialog offers "Top level"'
  );
  await page.keyboard.press('Escape');
  check(
    (await moveDialog.count()) === 0 &&
      (await byId(ids.alpha).evaluate(el => el === document.activeElement)),
    'Escape closes the Move dialog and returns focus to the row'
  );

  // --- Move by drag (FR57) --------------------------------------------------
  // Dragged by hand so the row being dragged over can be looked at mid-drag.
  const dragOver = async (fromId, toId) => {
    await byId(fromId).locator('.cockpit-tree-name').hover();
    await page.mouse.down();
    await byId(toId).locator('.cockpit-tree-name').hover();
    await byId(toId).locator('.cockpit-tree-name').hover();
  };
  await dragOver(ids.beta, ids.group);
  check(
    await byId(ids.group).evaluate(el => el.classList.contains('is-drop-into')),
    'dragging a workspace over a group marks the group as where it will land'
  );
  await shot('m3-dragging');
  await page.mouse.up();
  await expectEventually(
    async () => (await parentOf(ids.beta)) === ids.group,
    'dropping it there moves the workspace into the group'
  );

  // --- A move that is not allowed (FR57) -----------------------------------
  const beforeIllegal = patches();
  await dragOver(ids.group, ids.alpha);
  check(
    !(await byId(ids.alpha).evaluate(el => el.classList.contains('is-drop-target'))),
    'a group dragged over a workspace inside it is given nowhere to drop'
  );
  await page.mouse.up();
  await expectEventually(
    async () => (await live()) === `"${names.group}" cannot be moved into itself.`,
    'letting go there is refused, and the reason is announced'
  );
  await page.getByText('cannot be moved into itself').last().waitFor();
  check(true, 'and shown');
  await settle(400);
  await shot('m3b-move-refused');
  check(
    patches() === beforeIllegal && (await parentOf(ids.group)) === '',
    'nothing moves and nothing is sent'
  );
  await byId(ids.group).click({ button: 'right' });
  await menu.getByRole('menuitem', { name: 'Move…', exact: true }).click();
  await moveDialog.waitFor();
  const groupDestinations = await moveDialog.locator('[data-tree-move-to]').allInnerTexts();
  check(
    !groupDestinations.includes(names.alpha) && !groupDestinations.includes(names.group),
    "and the group's Move dialog does not offer the group itself or what is inside it"
  );
  await moveDialog.locator('[data-tree-move-cancel]').click();

  // Content rows cannot be dragged at all (FR57, FR62).
  check(
    (await looseNote.getAttribute('draggable')) === null &&
      (await byId(`${ids.gamma}/s/notes`).getAttribute('draggable')) === null &&
      (await byId(ids.gamma).getAttribute('draggable')) === 'true',
    'only workspace and group rows can be dragged: a note and a section cannot'
  );

  // --- Delete, then Undo (FR58, FR59) --------------------------------------
  const undo = toolbar.getByRole('button', { name: 'Undo', exact: true });
  await byId(ids.delta).locator('.cockpit-tree-name').click();
  await page.locator('.cockpit-pane-article[data-pane-kind="workspace"]').waitFor();
  check((await activeTab().innerText()).includes(names.delta), 'a workspace is open in a tab');
  await byId(ids.delta).click({ button: 'right' });
  await menu.getByRole('menuitem', { name: 'Delete', exact: true }).click();
  const confirmDelete = page.locator('dialog.ws-delete-dialog');
  await confirmDelete.waitFor();
  check(
    (await confirmDelete.innerText()).includes(`Delete "${names.delta}"?`) &&
      (await confirmDelete.innerText()).includes('restored with Undo'),
    'Delete asks first, in the same dialog the Map uses'
  );
  await shot('m4-delete-confirm');
  await confirmDelete.getByRole('button', { name: 'Cancel' }).click();
  await settle(300);
  check(
    (await byId(ids.delta).count()) === 1 && (await undo.isDisabled()),
    'Cancel deletes nothing'
  );
  await byId(ids.delta).click({ button: 'right' });
  await menu.getByRole('menuitem', { name: 'Delete', exact: true }).click();
  await confirmDelete.waitFor();
  await confirmDelete.getByRole('button', { name: 'Delete', exact: true }).click();
  await expectEventually(
    async () => (await byId(ids.delta).count()) === 0,
    'confirming removes the workspace from the tree'
  );
  await expectEventually(
    async () =>
      (await page.locator('.cockpit-pane-tab-label', { hasText: names.delta }).count()) === 0,
    'and closes its tab'
  );
  check(!(await undo.isDisabled()), 'Undo in the header becomes available');
  await shot('m5-deleted');
  await undo.click();
  await byId(ids.delta).waitFor();
  check(
    (await live()) === `Restored ${names.delta}.` && (await undo.isDisabled()),
    'Undo puts the workspace back and says so'
  );

  // --- Selecting rows (FR60, FR61, FR62) -----------------------------------
  check((await bulkBar.isHidden()) === true, 'with nothing selected there is no bar');
  await byId(ids.gamma).locator('.cockpit-tree-name').click();
  await page.locator('.cockpit-pane-article[data-pane-kind="workspace"]').waitFor();
  await byId(ids.delta)
    .locator('.cockpit-tree-name')
    .click({ modifiers: ['ControlOrMeta'] });
  await bulkBar.waitFor();
  const look = id =>
    byId(id).evaluate(el => ({
      checked: el.getAttribute('aria-checked'),
      selected: el.getAttribute('aria-selected'),
      picked: el.classList.contains('is-picked'),
      active: el.classList.contains('is-active'),
      outline: getComputedStyle(el).outlineStyle,
      background: getComputedStyle(el).backgroundColor
    }));
  const open = await look(ids.gamma);
  const picked = await look(ids.delta);
  check(
    picked.checked === 'true' && picked.selected === 'false' && picked.picked && !picked.active,
    'Cmd/Ctrl-click selects a row without opening it'
  );
  check(
    open.selected === 'true' &&
      open.checked === 'false' &&
      (open.outline !== picked.outline || open.background !== picked.background),
    'a selected row looks different from the row whose tab is open'
  );
  check(
    (await activeTab().innerText()).includes(names.gamma),
    'and the open tab stays where it was'
  );
  // The count is set in capitals by the stylesheet, which innerText reflects.
  const barText = async () => (await bulkBar.innerText()).replace(/\s+/g, ' ').trim().toLowerCase();
  check(
    (await barText()) === '1 selected select all group selected delete selected cancel',
    'the bar appears: "1 selected", Select all, Group selected, Delete selected, Cancel'
  );

  // Shift-click selects the range of workspace rows in between.
  await byId(ids.gamma)
    .locator('.cockpit-tree-name')
    .click({ modifiers: ['Shift'] });
  const between = await page.evaluate(
    ([from, to]) => {
      const rows = Array.from(
        document.querySelectorAll('#cockpitTreeNav [data-tree-row][aria-checked]')
      );
      const ids = rows.map(el => el.getAttribute('data-tree-row'));
      const [a, b] = [ids.indexOf(from), ids.indexOf(to)].sort((x, y) => x - y);
      return rows.slice(a, b + 1).map(el => el.getAttribute('aria-checked'));
    },
    [ids.delta, ids.gamma]
  );
  check(
    between.length >= 2 && between.every(value => value === 'true'),
    `Shift-click selects every workspace row from the last one picked (${between.length} rows)`
  );
  check(
    (await looseNote.getAttribute('aria-checked')) === null,
    'the notes in between are not part of the selection'
  );

  // On a content row a modified click is a plain click.
  await looseNote.click({ modifiers: ['ControlOrMeta'] });
  await page.locator('#cockpitPaneNoteEditor').waitFor();
  check(
    (await activeTab().innerText()).includes('Loose note') &&
      (await looseNote.getAttribute('aria-checked')) === null,
    'Cmd/Ctrl-click on a note just opens it: content rows cannot be selected'
  );

  // Cancel clears the selection; Space selects from the keyboard.
  await bulkBar.getByRole('button', { name: 'Cancel', exact: true }).click();
  check(
    (await bulkBar.isHidden()) === true &&
      (await page.locator('#cockpitTreeNav [aria-checked="true"]').count()) === 0,
    'Cancel clears the selection and the bar goes'
  );
  await byId(ids.gamma).focus();
  await page.keyboard.press('Space');
  check(
    (await byId(ids.gamma).getAttribute('aria-checked')) === 'true' &&
      (await byId(ids.gamma).evaluate(el => el === document.activeElement)),
    'Space selects the focused row and keeps focus on it'
  );
  await byId(ids.delta)
    .locator('.cockpit-tree-name')
    .click({ modifiers: ['ControlOrMeta'] });
  check((await barText()).startsWith('2 selected'), 'a second row makes it "2 selected"');
  await shot('m6-two-selected');

  // --- Group selected (FR61) -----------------------------------------------
  await bulkBar.getByRole('button', { name: 'Group selected', exact: true }).click();
  const creator = page.locator('#addFolderModal');
  await creator.waitFor();
  check(
    (await page.locator('#workspaceCreatorKindFixedNotice').innerText()).includes(
      'keeps that choice fixed'
    ),
    'Group selected opens the Create Group dialog, set to make a group'
  );
  await settle(300);
  await page.locator('#folderNameInput').fill(names.made);
  await page.locator('#wizardNextBtn').click();
  // The dialog's own roster step: the group's Manager is reviewed before the
  // group can be created. Nothing here is the tree's, it only has to be passed.
  await creator.locator('[data-team-agent-setup]').click();
  await page.locator('#addAgentModal').waitFor();
  await page.locator('#createAgentBtn').click();
  await page.locator('#addAgentModal').waitFor({ state: 'hidden' });
  await creator.getByRole('button', { name: 'Review →' }).click();
  await page.locator('#workspaceReviewSummary').waitFor();
  check(
    (await page.locator('#workspaceReviewSummary').innerText()).includes(
      'top-level workspaces will move'
    ),
    'its review step says the two selected workspaces will move into the group'
  );
  await shot('m7-group-review');
  const groupCreated = page.waitForResponse(
    response =>
      response.request().method() === 'POST' &&
      new URL(response.url()).pathname === '/api/workspaces'
  );
  await page.locator('#createFolderBtn').click();
  const madeId = (await (await groupCreated).json()).folder.id;
  madeGroups.push(madeId);
  await creator.waitFor({ state: 'hidden' });
  await expectEventually(
    async () =>
      (await byId(madeId).count()) === 1 &&
      (await parentOf(ids.gamma)) === madeId &&
      (await parentOf(ids.delta)) === madeId,
    'the new group appears with both workspaces inside it',
    8000
  );
  await expectEventually(
    async () => (await bulkBar.isHidden()) === true,
    'and the selection is cleared'
  );
  await byId(madeId).scrollIntoViewIfNeeded();
  await shot('m8-grouped');

  // --- Tags (FR63) -----------------------------------------------------------
  const tagBar = page.locator('#cockpitTreeNav .cockpit-tree-tagbar');
  const chip = tag => tagBar.locator(`[data-tree-tag-filter="${tag}"]`);
  check((await chip(tags.filter).count()) === 1, "the workspace's tag is a chip above the tree");
  check(
    (await tagBar.locator('[data-tree-tag-clear]').count()) === 0,
    '"Clear tag filters" is not shown while no filter is on'
  );
  await chip(tags.filter).click();
  await expectEventually(
    async () => (await byId(ids.delta).count()) === 0,
    'pressing the chip hides workspaces without the tag'
  );
  check(
    (await byId(ids.alpha).count()) === 1 &&
      (await byId(ids.group).count()) === 1 &&
      (await chip(tags.filter).getAttribute('aria-pressed')) === 'true',
    'the tagged workspace stays, inside its group, and the chip reads as pressed'
  );
  await shot('m9-tag-filter');
  await tagBar.getByRole('button', { name: 'Clear tag filters', exact: true }).click();
  await byId(ids.delta).waitFor();
  check(
    (await chip(tags.filter).getAttribute('aria-pressed')) === 'false',
    '"Clear tag filters" brings every row back'
  );

  // The same tags, in the workspace's overview.
  await byId(ids.alpha).scrollIntoViewIfNeeded();
  await byId(ids.alpha).locator('.cockpit-tree-name').click();
  await page.locator('.cockpit-pane-article[data-pane-kind="workspace"]').waitFor();
  const paneTag = tag =>
    page.locator(`#cockpitTreePane [data-pane-action="tag-filter"][data-pane-target="${tag}"]`);
  await paneTag(tags.filter).waitFor();
  check(
    (await paneTag(tags.filter).getAttribute('aria-label')) ===
      `Filter the tree by tag ${tags.filter}`,
    'the overview shows the tags as buttons, named for what they do'
  );
  await paneTag(tags.filter).click();
  await expectEventually(
    async () =>
      (await byId(ids.delta).count()) === 0 &&
      (await paneTag(tags.filter).getAttribute('aria-pressed')) === 'true' &&
      (await chip(tags.filter).getAttribute('aria-pressed')) === 'true',
    'pressing a tag in the overview filters the tree, and both places show it pressed'
  );
  check((await live()) === `Showing workspaces tagged ${tags.filter}.`, 'the filter is announced');
  check(
    await page.evaluate(
      () => document.activeElement?.getAttribute('data-pane-action') === 'tag-filter'
    ),
    'focus stays on the tag that was pressed'
  );
  await shot('m10-overview-tags');
  await paneTag(tags.filter).click();
  await byId(ids.delta).waitFor();

  // Removing a tag.
  const tagsSent = page.waitForResponse(
    response =>
      response.request().method() === 'PATCH' &&
      new URL(response.url()).pathname === `/api/workspaces/${ids.alpha}`
  );
  await page
    .locator(`#cockpitTreePane [data-pane-action="tag-remove"][data-pane-target="${tags.spare}"]`)
    .click();
  const sent = (await tagsSent).request().postDataJSON();
  check(
    JSON.stringify(sent.tags) === JSON.stringify([tags.filter]),
    'removing a tag saves the workspace with the remaining tags'
  );
  await expectEventually(
    async () => (await paneTag(tags.spare).count()) === 0 && (await chip(tags.spare).count()) === 0,
    'the tag goes from the overview and, with no workspace carrying it, from the chips'
  );
  check(
    (await live()) === `Removed tag ${tags.spare} from ${names.alpha}.`,
    'the removal is announced'
  );
}

const stages = {
  tree: stageTree,
  pane: stagePane,
  note: stageNote,
  create: stageCreate,
  manage: stageManage,
  'map-requests': stageMapRequests
};
try {
  if (!stages[stage])
    throw new Error(`unknown stage "${stage}" (have: ${Object.keys(stages).join(', ')})`);
  await stages[stage]();
} catch (error) {
  failures.push(`stage threw: ${error.message}`);
  console.error(error);
  try {
    await shot('failure');
  } catch (_) {
    /* the page may be gone */
  }
} finally {
  await browser.close();
}

const unique = [...new Set(problems)];
if (unique.length) {
  console.log(`\n${unique.length} page problem(s):`);
  unique.slice(0, 20).forEach(problem => console.log(`  ${problem}`));
}
console.log(failures.length ? `\n${failures.length} check(s) failed` : '\nall checks passed');
process.exit(failures.length || unique.length ? 1 : 0);

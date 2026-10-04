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
 *
 * Exits non-zero when a check fails, a console error appears, or a request
 * fails, so a page that renders but is quietly broken does not pass.
 */
import { chromium } from 'playwright';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

const [baseUrl, outDirArg, stage = 'tree', theme = 'light'] = process.argv.slice(2);
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
  const groupIds = new Set(afterTree.map(entry => entry.split('/')[3]));
  check(
    afterTree.length === 5 && groupIds.size === 1,
    `Tree with only the group expanded fetches 5 sections of 1 row (saw ${afterTree.length} for ${groupIds.size})`
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
  await page.locator('.cockpit-pane-markdown').waitFor();
  const pane = await page.locator('.cockpit-pane-article').innerText();
  check(
    /Studio Notes\s*\/\s*Notes\s*\/\s*Weekly review/.test(pane),
    'breadcrumb reads Studio Notes / Notes / Weekly review'
  );
  check(pane.includes('Note in Studio Notes'), 'the pane says "Note in Studio Notes"');
  check(
    (await page.locator('.cockpit-pane-markdown h2').count()) === 3,
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
  await page.locator('.cockpit-pane-markdown').waitFor();
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

const stages = { tree: stageTree, pane: stagePane, 'map-requests': stageMapRequests };
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

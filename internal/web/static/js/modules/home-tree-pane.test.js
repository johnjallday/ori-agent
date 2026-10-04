// Tests for home-tree-pane.js — the tabs and the reading pane beside the Home
// file tree.
//
// Pure helpers only: tab state, the view model of a tab, and the markup the
// pane draws from it. Mounting needs a DOM and is exercised in the browser.
//   node --test internal/web/static/js/modules/home-tree-pane.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ITEM_FAILED,
  ITEM_LOADING,
  ITEM_READY,
  activateTab,
  closeTab,
  openTab,
  paneCrumbs,
  paneSubline,
  paneView,
  renderEmptyPaneHTML,
  renderPaneHTML,
  renderTabStripHTML,
  tabFromRow
} from './home-tree-pane.js';

const tab = (key, extra = {}) => ({
  key,
  kind: 'note',
  workspaceId: 'ws1',
  label: key,
  meta: {},
  ...extra
});

// Music (group) > Night Drive; My HQ and Solo at the top level.
const FLAT = [
  { id: 'g1', name: 'Music', kind: 'group', parent_id: '' },
  { id: 'ws1', name: 'Night Drive', parent_id: 'g1' },
  { id: 'hq', name: 'My HQ', is_personal_hq: true, parent_id: '' },
  { id: 'solo', name: 'Solo', parent_id: '' },
  { id: 'g2', name: 'Stems', kind: 'group', parent_id: 'g1' }
];

// ---------------------------------------------------------------------------
// Tab state (FR25, FR26)
// ---------------------------------------------------------------------------

test('tabFromRow keys a tab by its row id and keeps what the pane needs', () => {
  const made = tabFromRow({
    id: 'ws1/n/n9',
    kind: 'note',
    name: 'Weekly review',
    workspaceId: 'ws1',
    meta: { noteId: 'n9' }
  });
  assert.deepEqual(made, {
    key: 'ws1/n/n9',
    kind: 'note',
    workspaceId: 'ws1',
    label: 'Weekly review',
    meta: { noteId: 'n9' }
  });
  // A workspace row is its own workspace.
  assert.equal(
    tabFromRow({ id: 'ws1', kind: 'workspace', name: 'Night Drive' }).workspaceId,
    'ws1'
  );
});

test('opening an item adds its tab at the end and makes it active', () => {
  const first = openTab([], '', tab('a'));
  assert.deepEqual(
    first.tabs.map(t => t.key),
    ['a']
  );
  assert.equal(first.activeKey, 'a');
  const second = openTab(first.tabs, first.activeKey, tab('b'));
  assert.deepEqual(
    second.tabs.map(t => t.key),
    ['a', 'b']
  );
  assert.equal(second.activeKey, 'b');
});

test('opening an item that is already open switches to it, never adds a second tab (FR25)', () => {
  const tabs = [tab('a'), tab('b'), tab('c')];
  const next = openTab(tabs, 'c', tab('a'));
  assert.deepEqual(
    next.tabs.map(t => t.key),
    ['a', 'b', 'c']
  );
  assert.equal(next.activeKey, 'a');
});

test('reopening takes the newer label, so a renamed note is not stuck with its old name', () => {
  const next = openTab([tab('a', { label: 'Old name', meta: { noteId: 'n1' } })], 'a', {
    ...tab('a'),
    label: 'New name',
    meta: { noteId: 'n1' }
  });
  assert.equal(next.tabs[0].label, 'New name');
  assert.equal(next.tabs[0].meta.noteId, 'n1');
});

test('openTab never changes the list it was given', () => {
  const tabs = [tab('a')];
  openTab(tabs, 'a', tab('b'));
  openTab(tabs, 'a', tab('a', { label: 'changed' }));
  assert.deepEqual(tabs, [tab('a')]);
});

test('openTab with nothing to open changes nothing', () => {
  const tabs = [tab('a')];
  assert.deepEqual(openTab(tabs, 'a', null), { tabs, activeKey: 'a' });
  assert.deepEqual(openTab(null, '', null), { tabs: [], activeKey: '' });
});

test('activateTab switches to an open tab and ignores an unknown key', () => {
  const tabs = [tab('a'), tab('b')];
  assert.equal(activateTab(tabs, 'a', 'b').activeKey, 'b');
  assert.equal(activateTab(tabs, 'a', 'zzz').activeKey, 'a');
});

test('closing the active tab activates the one to its right (FR26)', () => {
  const next = closeTab([tab('a'), tab('b'), tab('c')], 'b', 'b');
  assert.deepEqual(
    next.tabs.map(t => t.key),
    ['a', 'c']
  );
  assert.equal(next.activeKey, 'c');
});

test('closing the last active tab activates the one to its left (FR26)', () => {
  const next = closeTab([tab('a'), tab('b'), tab('c')], 'c', 'c');
  assert.deepEqual(
    next.tabs.map(t => t.key),
    ['a', 'b']
  );
  assert.equal(next.activeKey, 'b');
});

test('closing the only tab leaves nothing active', () => {
  assert.deepEqual(closeTab([tab('a')], 'a', 'a'), { tabs: [], activeKey: '' });
});

test('closing a tab that is not active leaves the active tab alone', () => {
  const next = closeTab([tab('a'), tab('b'), tab('c')], 'c', 'a');
  assert.deepEqual(
    next.tabs.map(t => t.key),
    ['b', 'c']
  );
  assert.equal(next.activeKey, 'c');
});

test('closing a tab that is not open changes nothing', () => {
  const tabs = [tab('a')];
  assert.deepEqual(closeTab(tabs, 'a', 'zzz'), { tabs, activeKey: 'a' });
});

// ---------------------------------------------------------------------------
// The "what and where" line and the breadcrumb (FR31)
// ---------------------------------------------------------------------------

test('the subline says what the item is and where', () => {
  const night = FLAT[1];
  assert.equal(paneSubline(tab('k', { kind: 'note' }), night, FLAT[0]), 'Note in Night Drive');
  assert.equal(paneSubline(tab('k', { kind: 'ticket' }), night, null), 'Ticket in Night Drive');
  assert.equal(paneSubline(tab('k', { kind: 'file' }), night, null), 'File in Night Drive');
  assert.equal(paneSubline(tab('k', { kind: 'agent' }), night, null), 'Agent in Night Drive');
  assert.equal(paneSubline(tab('k', { kind: 'memory' }), night, null), 'Memory for Night Drive');
});

test('a workspace says where it sits; Personal HQ says so; a group likewise', () => {
  assert.equal(
    paneSubline(tab('ws1', { kind: 'workspace' }), FLAT[1], FLAT[0]),
    'Workspace in Music'
  );
  assert.equal(paneSubline(tab('solo', { kind: 'workspace' }), FLAT[3], null), 'Workspace');
  assert.equal(paneSubline(tab('hq', { kind: 'workspace' }), FLAT[2], null), 'Personal HQ');
  assert.equal(paneSubline(tab('g1', { kind: 'group' }), FLAT[0], null), 'Group');
  assert.equal(paneSubline(tab('g2', { kind: 'group' }), FLAT[4], FLAT[0]), 'Group in Music');
});

test('the breadcrumb runs from the outermost group down to the item', () => {
  assert.deepEqual(paneCrumbs(tab('ws1/n/n1', { kind: 'note', label: 'Lyrics draft' }), FLAT), [
    'Music',
    'Night Drive',
    'Notes',
    'Lyrics draft'
  ]);
  assert.deepEqual(
    paneCrumbs(
      tab('hq/t/t1', { kind: 'ticket', workspaceId: 'hq', label: 'Renew the domain' }),
      FLAT
    ),
    ['My HQ', 'Backlog', 'Renew the domain']
  );
  assert.deepEqual(paneCrumbs(tab('ws1/a/Scout', { kind: 'agent', label: 'Scout' }), FLAT), [
    'Music',
    'Night Drive',
    'Agents',
    'Scout'
  ]);
});

test('a file breadcrumb includes the folders it sits in', () => {
  assert.deepEqual(
    paneCrumbs(
      tab('ws1/f/stems/takes/lead.wav', {
        kind: 'file',
        label: 'lead.wav',
        meta: { path: 'stems/takes/lead.wav' }
      }),
      FLAT
    ),
    ['Music', 'Night Drive', 'Files', 'stems', 'takes', 'lead.wav']
  );
});

test('a workspace, group or Memory breadcrumb stops at itself', () => {
  assert.deepEqual(paneCrumbs(tab('ws1', { kind: 'workspace' }), FLAT), ['Music', 'Night Drive']);
  assert.deepEqual(paneCrumbs(tab('g1', { kind: 'group', workspaceId: 'g1' }), FLAT), ['Music']);
  assert.deepEqual(paneCrumbs(tab('g2', { kind: 'group', workspaceId: 'g2' }), FLAT), [
    'Music',
    'Stems'
  ]);
  assert.deepEqual(paneCrumbs(tab('ws1/m', { kind: 'memory' }), FLAT), [
    'Music',
    'Night Drive',
    'Memory'
  ]);
});

// ---------------------------------------------------------------------------
// The view model
// ---------------------------------------------------------------------------

const NOTE_TAB = tab('ws1/n/n1', { kind: 'note', label: 'Lyrics draft', meta: { noteId: 'n1' } });

test('a note that has loaded shows its tags and its content as Markdown', () => {
  const view = paneView(NOTE_TAB, {
    flattened: FLAT,
    item: {
      status: ITEM_READY,
      value: { name: 'Lyrics draft', content: '## Verse 1\n\nWords.', tags: ['lyrics'] },
      error: ''
    }
  });
  assert.equal(view.title, 'Lyrics draft');
  assert.equal(view.sub, 'Note in Night Drive');
  assert.deepEqual(view.tags, ['lyrics']);
  assert.deepEqual(view.body, { type: 'markdown', markdown: '## Verse 1\n\nWords.' });
  assert.equal(view.status, ITEM_READY);
});

test('a note takes its title from the loaded note, in the breadcrumb too', () => {
  const view = paneView(NOTE_TAB, {
    flattened: FLAT,
    item: { status: ITEM_READY, value: { name: 'Lyrics (final)', content: '' }, error: '' }
  });
  assert.equal(view.title, 'Lyrics (final)');
  assert.deepEqual(view.crumbs, ['Music', 'Night Drive', 'Notes', 'Lyrics (final)']);
});

test('a note still loading, or one that failed, has a header but no body', () => {
  const loading = paneView(NOTE_TAB, {
    flattened: FLAT,
    item: { status: ITEM_LOADING, value: null, error: '' }
  });
  assert.equal(loading.status, ITEM_LOADING);
  assert.equal(loading.body, null);
  assert.equal(loading.title, 'Lyrics draft');

  const failed = paneView(NOTE_TAB, {
    flattened: FLAT,
    item: { status: ITEM_FAILED, value: null, error: 'Note not found' }
  });
  assert.equal(failed.status, ITEM_FAILED);
  assert.equal(failed.error, 'Note not found');
});

test('a workspace tab follows the current name of its workspace', () => {
  const stale = tab('ws1', { kind: 'workspace', label: 'Old Name' });
  assert.equal(paneView(stale, { flattened: FLAT }).title, 'Night Drive');
  assert.equal(paneView(stale, { flattened: FLAT }).sub, 'Workspace in Music');
  // With nothing to load, the tab is ready from the start.
  assert.equal(paneView(stale, { flattened: FLAT }).status, ITEM_READY);
});

test('a tab whose workspace is gone still has something honest to say', () => {
  const view = paneView(tab('gone/n/n1', { kind: 'note', workspaceId: 'gone', label: 'Orphan' }), {
    flattened: FLAT
  });
  assert.equal(view.sub, 'Note in a workspace that no longer exists');
});

// ---------------------------------------------------------------------------
// Markup
// ---------------------------------------------------------------------------

test('the tab strip uses tab semantics and marks the active tab in words (FR73)', () => {
  const html = renderTabStripHTML(
    [tab('ws1/n/n1', { label: 'Lyrics draft' }), tab('ws1/n/n2', { label: 'Mix notes' })],
    'ws1/n/n2',
    FLAT
  );
  assert.equal((html.match(/role="tab"/g) || []).length, 2);
  assert.match(html, /data-pane-tab="ws1\/n\/n2"[^>]*aria-selected="true"/);
  assert.match(html, /data-pane-tab="ws1\/n\/n1"[^>]*aria-selected="false"/);
  assert.match(html, /class="cockpit-pane-tab is-active"/);
  // Each tab has a named close button (FR26).
  assert.match(html, /data-pane-close="ws1\/n\/n1"[^>]*aria-label="Close Lyrics draft"/);
  assert.match(html, /aria-controls="cockpitPanePanel"/);
});

test('only the active tab is in the tab order', () => {
  const html = renderTabStripHTML([tab('a'), tab('b'), tab('c')], 'b', FLAT);
  assert.equal((html.match(/role="tab"[^>]*tabindex="0"/g) || []).length, 1);
  assert.match(html, /data-pane-tab="b"[^>]*tabindex="0"/);
});

test('no tabs renders no strip, and the pane says nothing is open (FR29)', () => {
  assert.equal(renderTabStripHTML([], '', FLAT), '');
  const empty = renderEmptyPaneHTML();
  assert.match(empty, /Nothing open/);
  assert.match(empty, /Pick a note, ticket or file from the tree and it opens here\./);
});

test('the pane shows the breadcrumb, the title and the "what and where" line (FR31)', () => {
  const html = renderPaneHTML(
    paneView(NOTE_TAB, {
      flattened: FLAT,
      item: { status: ITEM_READY, value: { name: 'Lyrics draft', content: 'Hi', tags: ['lyrics'] } }
    })
  );
  assert.match(html, /Music <span aria-hidden="true">\/<\/span> Night Drive/);
  assert.match(
    html,
    /<h3 class="cockpit-pane-title" data-pane-title tabindex="-1">Lyrics draft<\/h3>/
  );
  assert.match(html, /Note in Night Drive/);
  assert.match(html, /class="cockpit-pane-tag">#lyrics</);
  assert.match(html, /class="cockpit-pane-markdown"/);
});

test('a loading pane says so; a failed one gives the reason and a Retry', () => {
  const loading = renderPaneHTML(
    paneView(NOTE_TAB, { flattened: FLAT, item: { status: ITEM_LOADING } })
  );
  assert.match(loading, /role="status">Loading…</);
  const failed = renderPaneHTML(
    paneView(NOTE_TAB, { flattened: FLAT, item: { status: ITEM_FAILED, error: 'Note not found' } })
  );
  assert.match(failed, /role="alert"/);
  assert.match(failed, /Note not found/);
  assert.match(failed, /data-pane-retry="ws1\/n\/n1"/);
});

test('an empty note says it is empty rather than showing a blank pane', () => {
  const html = renderPaneHTML(
    paneView(NOTE_TAB, { flattened: FLAT, item: { status: ITEM_READY, value: { content: '  ' } } })
  );
  assert.match(html, /This note is empty\./);
});

test('names and tags are escaped everywhere they are printed', () => {
  const hostile = '<img src=x onerror=1>';
  const flat = [{ id: 'ws1', name: hostile, parent_id: '' }];
  const evil = tab('ws1/n/n1', { kind: 'note', label: hostile });
  const html =
    renderTabStripHTML([evil], evil.key, flat) +
    renderPaneHTML(
      paneView(evil, {
        flattened: flat,
        item: { status: ITEM_READY, value: { name: hostile, content: '', tags: [hostile] } }
      })
    );
  assert.doesNotMatch(html, /<img/i);
  assert.match(html, /&lt;img/);
});

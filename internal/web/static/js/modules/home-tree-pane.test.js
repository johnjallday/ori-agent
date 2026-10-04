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
  NOTE_EDITOR_ID,
  activateTab,
  closeTab,
  createNoteController,
  leaveNoteThen,
  nextRunLabel,
  openTab,
  paneCrumbs,
  paneSubline,
  paneView,
  saveStatusText,
  sectionLinks,
  renderEmptyPaneHTML,
  renderPaneHTML,
  renderTabStripHTML,
  tabFromRow
} from './home-tree-pane.js';
import { NoteAutoSaveTimer } from './note-editor.js';

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
  {
    id: 'g1',
    name: 'Music',
    kind: 'group',
    parent_id: '',
    folder_slug: 'music',
    description: 'Songs in progress.'
  },
  {
    id: 'ws1',
    name: 'Night Drive',
    parent_id: 'g1',
    folder_slug: 'night-drive',
    description: 'The first single.',
    agent_count: 2,
    open_task_count: 3,
    needs_attention_count: 1,
    active: false
  },
  { id: 'hq', name: 'My HQ', is_personal_hq: true, parent_id: '', folder_slug: 'my-hq' },
  // Solo reports nothing: no counts, no activity.
  { id: 'solo', name: 'Solo', parent_id: '', folder_slug: 'solo' },
  { id: 'g2', name: 'Stems', kind: 'group', parent_id: 'g1', folder_slug: 'stems' }
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

test('a note that has loaded shows its tags and a slot for the editor (FR32, FR41)', () => {
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
  // The note's text is not part of the view: the editor holds it, so a redraw
  // of the pane cannot disturb what is being typed.
  assert.deepEqual(view.body, { type: 'editor', noteId: 'n1' });
  // FR32: "Open full note" goes to the note page.
  assert.deepEqual(view.actions, [
    { label: 'Open full note', href: '/workspaces/night-drive/notes/n1', primary: true }
  ]);
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
});

test('a note is drawn as one element for the existing editor, plus its save line', () => {
  const html = renderPaneHTML(
    paneView(NOTE_TAB, {
      flattened: FLAT,
      item: { status: ITEM_READY, value: { name: 'Lyrics draft', content: 'Hi', tags: [] } }
    })
  );
  // The id the editor is told to draw into, and the two classes its shared
  // styles are keyed on.
  assert.match(html, new RegExp(`id="${NOTE_EDITOR_ID}"`));
  assert.match(html, /class="note-preview-content note-live-editor cockpit-pane-editor"/);
  assert.match(html, /data-pane-editor="n1"/);
  assert.match(html, /role="textbox" aria-multiline="true"/);
  assert.match(html, /data-pane-save-status role="status"/);
  assert.match(html, /data-pane-save-retry hidden/);
  // The text itself is never in the markup.
  assert.doesNotMatch(html, />Hi</);
});

test('two redraws of the same note produce identical markup, so the editor is left alone', () => {
  const draw = content =>
    renderPaneHTML(
      paneView(NOTE_TAB, {
        flattened: FLAT,
        item: { status: ITEM_READY, value: { name: 'Lyrics draft', content, tags: ['a'] } }
      })
    );
  assert.equal(draw('first version'), draw('typed a lot more since'));
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

test('an empty Markdown file says it is empty rather than showing a blank pane', () => {
  const html = renderPaneHTML(
    paneView(tab('ws1/f/a.md', { kind: 'file', label: 'a.md', meta: { path: 'a.md' } }), {
      flattened: FLAT,
      item: { status: ITEM_READY, value: { kind: 'markdown', text: '  ' } }
    })
  );
  assert.match(html, /This file is empty\./);
});

// ---------------------------------------------------------------------------
// Editing a note (FR41-FR45)
// ---------------------------------------------------------------------------

// A stand-in for window.NoteEditor.mount. It keeps the REAL autosave timer, so
// the dirty / saving / saved / error transitions under test are the editor's
// own; only the drawing is faked.
function fakeEditor() {
  const mounts = [];
  const mount = host => {
    const bundle = {
      host,
      renders: [],
      destroyed: false,
      history: { reset() {} },
      autosave: new NoteAutoSaveTimer({
        delayMs: 60_000,
        onFlush: host.onAutosaveFlush,
        onStatusChange: host.onAutosaveStatusChange
      }),
      render(options) {
        this.renders.push(options || {});
      },
      destroy() {
        this.destroyed = true;
        this.autosave.cancel();
      },
      // What typing does: change the text, then arm the autosave timer.
      type(text) {
        host.setContent(text);
        this.autosave.schedule();
      }
    };
    mounts.push(bundle);
    return bundle;
  };
  return { mount, mounts };
}

function fakePresence(openElsewhere = false) {
  const log = [];
  return {
    log,
    claimOpenNote: (id, surface) => log.push(`claim ${id} ${surface}`),
    releaseOpenNote: id => log.push(`release ${id}`),
    isOpenElsewhere: async () => ({ open: openElsewhere })
  };
}

const NOTE = { id: 'n1', content: 'first', updatedAt: '2026-10-04T10:00:00Z' };
const ELEMENT = { id: NOTE_EDITOR_ID };

function controllerWith(saveContent, extra = {}) {
  const editor = fakeEditor();
  const statuses = [];
  const saved = [];
  const controller = createNoteController({
    mountEditor: editor.mount,
    saveContent,
    onStatus: (status, error) => statuses.push(error ? `${status}: ${error}` : status),
    onSaved: (key, text) => saved.push(`${key} <- ${text}`),
    ...extra
  });
  return { controller, editor, statuses, saved };
}

test('the editor is mounted with the host options the note page uses for a second pane', () => {
  const { controller, editor } = controllerWith(async () => ({}));
  controller.attach(ELEMENT, 'ws1/n/n1', NOTE);
  const { host, renders } = editor.mounts[0];
  assert.equal(host.previewPaneId, NOTE_EDITOR_ID);
  assert.equal(host.enableToc, false);
  assert.equal(host.aiAssist, undefined);
  assert.equal(host.isPreviewMode(), true);
  assert.equal(host.getContent(), 'first');
  assert.deepEqual(host.getContentLines(), ['first']);
  host.setContentLines(['a', 'b']);
  assert.equal(controller.content(), 'a\nb');
  // An empty note is one empty line, which is what gives it somewhere to click.
  host.setContent('');
  assert.deepEqual(host.getContentLines(), ['']);
  assert.equal(renders.length, 1);
});

test('only one editor exists: mounting another note takes the first one down (FR41)', () => {
  const { controller, editor } = controllerWith(async () => ({}));
  controller.attach(ELEMENT, 'ws1/n/n1', NOTE);
  controller.attach(ELEMENT, 'ws1/n/n2', { id: 'n2', content: 'second' });
  assert.equal(editor.mounts.length, 2);
  assert.equal(editor.mounts[0].destroyed, true);
  assert.equal(editor.mounts[1].destroyed, false);
  assert.equal(controller.activeKey(), 'ws1/n/n2');
  assert.equal(controller.content(), 'second');
});

test('mounting the note that is already mounted changes nothing', () => {
  const { controller, editor } = controllerWith(async () => ({}));
  controller.attach(ELEMENT, 'ws1/n/n1', NOTE);
  editor.mounts[0].type('typed');
  controller.attach(ELEMENT, 'ws1/n/n1', NOTE);
  assert.equal(editor.mounts.length, 1);
  assert.equal(editor.mounts[0].renders.length, 1);
  assert.equal(controller.content(), 'typed');
});

test('a redrawn pane hands over a new element, and the same text is drawn into it', () => {
  const { controller, editor } = controllerWith(async () => ({}));
  controller.attach(ELEMENT, 'ws1/n/n1', NOTE);
  editor.mounts[0].type('typed');
  controller.attach({ id: NOTE_EDITOR_ID }, 'ws1/n/n1', NOTE);
  assert.equal(editor.mounts.length, 1);
  assert.equal(editor.mounts[0].renders.length, 2);
  assert.equal(controller.content(), 'typed');
});

test('autosave sends the current text to the save call and reports saving, then saved (FR42)', async () => {
  const calls = [];
  const { controller, editor, statuses, saved } = controllerWith(async (id, text) => {
    calls.push(`${id}: ${text}`);
    return { updatedAt: '2026-10-04T11:00:00Z' };
  });
  controller.attach(ELEMENT, 'ws1/n/n1', NOTE);
  editor.mounts[0].type('first, edited');
  assert.equal(controller.isDirty(), true);
  assert.equal(await controller.flush(), true);
  assert.deepEqual(calls, ['n1: first, edited']);
  assert.deepEqual(statuses, ['unsaved', 'saving', 'saved']);
  assert.deepEqual(saved, ['ws1/n/n1 <- first, edited']);
  assert.equal(controller.isDirty(), false);
});

test('with nothing unsaved, a flush sends nothing', async () => {
  const calls = [];
  const { controller } = controllerWith(async () => calls.push('save'));
  controller.attach(ELEMENT, 'ws1/n/n1', NOTE);
  assert.equal(await controller.flush(), true);
  assert.deepEqual(calls, []);
  // Nothing mounted at all is also "nothing to save".
  controller.detach();
  assert.equal(await controller.flush(), true);
});

test('leaving a note saves it BEFORE the switch happens (FR43)', async () => {
  const order = [];
  let finishSave;
  const { controller, editor } = controllerWith(
    (id, text) =>
      new Promise(resolve => {
        order.push(`save started: ${text}`);
        finishSave = () => {
          order.push('save finished');
          resolve({});
        };
      })
  );
  controller.attach(ELEMENT, 'ws1/n/n1', NOTE);
  editor.mounts[0].type('unsaved words');

  const leaving = leaveNoteThen(controller, () => order.push('switched tab'));
  await Promise.resolve();
  // The save is on its way and the switch has not happened.
  assert.deepEqual(order, ['save started: unsaved words']);
  assert.equal(controller.activeKey(), 'ws1/n/n1');

  finishSave();
  assert.equal(await leaving, true);
  assert.deepEqual(order, ['save started: unsaved words', 'save finished', 'switched tab']);
  // The editor is taken down only after the save, and before the switch.
  assert.equal(editor.mounts[0].destroyed, true);
  assert.equal(controller.activeKey(), '');
});

test('leaving a note with nothing unsaved switches at once, without a save', async () => {
  const calls = [];
  const { controller } = controllerWith(async () => calls.push('save'));
  controller.attach(ELEMENT, 'ws1/n/n1', NOTE);
  let switched = false;
  assert.equal(
    await leaveNoteThen(controller, () => {
      switched = true;
    }),
    true
  );
  assert.equal(switched, true);
  assert.deepEqual(calls, []);
});

test('a failed save keeps the tab, the text and says why; the switch does not happen (FR44)', async () => {
  let fail = true;
  const { controller, editor, statuses, saved } = controllerWith(async () => {
    if (fail) throw new Error('Failed to update note');
    return {};
  });
  controller.attach(ELEMENT, 'ws1/n/n1', NOTE);
  editor.mounts[0].type('words that must not be lost');

  let switched = false;
  const left = await leaveNoteThen(controller, () => {
    switched = true;
  });
  assert.equal(left, false);
  assert.equal(switched, false);
  // Still mounted, still holding the text, still unsaved.
  assert.equal(controller.activeKey(), 'ws1/n/n1');
  assert.equal(editor.mounts[0].destroyed, false);
  assert.equal(controller.content(), 'words that must not be lost');
  assert.equal(controller.isDirty(), true);
  assert.deepEqual(controller.status(), { status: 'error', error: 'Failed to update note' });
  assert.equal(statuses[statuses.length - 1], 'error: Failed to update note');
  assert.deepEqual(saved, []);

  // Retry is the same save again; once it works the note can be left.
  fail = false;
  assert.equal(await controller.flush(), true);
  assert.deepEqual(saved, ['ws1/n/n1 <- words that must not be lost']);
  assert.equal(statuses[statuses.length - 1], 'saved');
  assert.equal(await leaveNoteThen(controller, () => {}), true);
});

test('text typed while a save is in flight is saved again, not lost', async () => {
  const calls = [];
  let release;
  const { controller, editor } = controllerWith((id, text) => {
    calls.push(text);
    if (calls.length > 1) return Promise.resolve({});
    return new Promise(resolve => {
      release = () => resolve({});
    });
  });
  controller.attach(ELEMENT, 'ws1/n/n1', NOTE);
  editor.mounts[0].type('one');
  const first = controller.flush();
  await Promise.resolve();
  editor.mounts[0].type('one two');
  release();
  // The first save landed, but the editor is dirty again: not safe to leave.
  assert.equal(await first, false);
  assert.equal(controller.isDirty(), true);
  assert.equal(await controller.flush(), true);
  assert.deepEqual(calls, ['one', 'one two']);
});

test('a newer version from the server replaces the text only when nothing is unsaved (FR45)', () => {
  const { controller, editor } = controllerWith(async () => ({}));
  controller.attach(ELEMENT, 'ws1/n/n1', NOTE);
  const newer = { id: 'n1', content: 'edited elsewhere', updatedAt: '2026-10-04T12:00:00Z' };

  // Unsaved text here: the newer version is not allowed to wipe it.
  editor.mounts[0].type('my unsaved text');
  controller.attach(ELEMENT, 'ws1/n/n1', newer);
  assert.equal(controller.content(), 'my unsaved text');

  // Nothing unsaved: the newer version is shown.
  editor.mounts[0].autosave.markClean();
  controller.attach(ELEMENT, 'ws1/n/n1', newer);
  assert.equal(controller.content(), 'edited elsewhere');

  // An OLDER copy (a slow reload answering late) never replaces newer text.
  controller.attach(ELEMENT, 'ws1/n/n1', {
    id: 'n1',
    content: 'stale',
    updatedAt: '2026-10-04T09:00:00Z'
  });
  assert.equal(controller.content(), 'edited elsewhere');
});

test('the note is claimed while mounted and released after, as the note page does (FR45)', async () => {
  const presence = fakePresence(true);
  const elsewhere = [];
  const { controller } = controllerWith(async () => ({}), {
    presence,
    onElsewhere: (key, open) => elsewhere.push(`${key} ${open}`)
  });
  controller.attach(ELEMENT, 'ws1/n/n1', NOTE);
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.deepEqual(presence.log, ['claim n1 home']);
  assert.deepEqual(elsewhere, ['ws1/n/n1 true']);
  controller.detach();
  assert.deepEqual(presence.log, ['claim n1 home', 'release n1']);
});

test('a new note is opened with the cursor on its first line', () => {
  const { controller, editor } = controllerWith(async () => ({}));
  controller.attach(ELEMENT, 'ws1/n/n1', { id: 'n1', content: '' }, { focus: true });
  assert.deepEqual(editor.mounts[0].renders[0], { focusLineIndex: 0, cursorPosition: 0 });
});

test('leaving the page sends one keepalive save, and only when something is unsaved', () => {
  const calls = [];
  const { controller, editor } = controllerWith(async (id, text, options) => {
    calls.push({ id, text, keepalive: !!(options && options.keepalive) });
    return null;
  });
  controller.keepalive(); // nothing mounted
  controller.attach(ELEMENT, 'ws1/n/n1', NOTE);
  controller.keepalive(); // nothing unsaved
  assert.deepEqual(calls, []);
  editor.mounts[0].type('closing the tab mid-sentence');
  controller.keepalive(); // pagehide
  controller.keepalive(); // beforeunload, same text
  assert.deepEqual(calls, [{ id: 'n1', text: 'closing the tab mid-sentence', keepalive: true }]);
});

test('the save line uses the note page words and adds the reason to a failure', () => {
  assert.equal(saveStatusText('unsaved'), 'Unsaved');
  assert.equal(saveStatusText('saving'), 'Saving…');
  assert.equal(saveStatusText('saved'), 'Saved');
  assert.equal(saveStatusText('error'), 'Save failed. Your text is kept.');
  assert.equal(
    saveStatusText('error', 'Failed to update note'),
    'Save failed: Failed to update note. Your text is kept.'
  );
  assert.equal(saveStatusText(''), '');
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

// ---------------------------------------------------------------------------
// Ticket (FR33)
// ---------------------------------------------------------------------------

const TICKET_TAB = tab('ws1/t/t1', {
  kind: 'ticket',
  label: 'Record backing vocals',
  meta: { ticketId: 't1', state: 'ready', stateLabel: 'Ready' }
});

const ready = value => ({ status: ITEM_READY, value, error: '' });

test('a ticket shows its state, description, tags and source, and links to Tickets', () => {
  const view = paneView(TICKET_TAB, {
    flattened: FLAT,
    item: ready({
      title: 'Record backing vocals',
      number: '#4',
      state: 'in_progress',
      stateLabel: 'In progress',
      description: 'Double the chorus.',
      tags: ['vocals'],
      sourceLabel: 'Added by you'
    })
  });
  assert.equal(view.sub, 'Ticket in Night Drive');
  assert.deepEqual(view.chip, { label: 'In progress', tone: 'ok' });
  assert.equal(view.lead, 'Double the chorus.');
  assert.deepEqual(view.tags, ['vocals']);
  assert.deepEqual(view.fields, [
    { label: 'Number', value: '#4' },
    { label: 'Source', value: 'Added by you' },
    { label: 'Workspace', value: 'Night Drive' }
  ]);
  assert.deepEqual(view.actions, [
    { label: 'Open in Tickets', href: '/workspaces/night-drive?ticket=t1', primary: true }
  ]);
});

test('a ticket shows the state its row knew while the ticket itself is loading', () => {
  const view = paneView(TICKET_TAB, { flattened: FLAT, item: { status: ITEM_LOADING } });
  assert.deepEqual(view.chip, { label: 'Ready', tone: 'info' });
  assert.equal(view.lead, '');
  assert.deepEqual(view.fields, []);
});

test('a ticket with no description says so; finished states are muted', () => {
  const view = paneView(TICKET_TAB, {
    flattened: FLAT,
    item: ready({ title: 'Done one', state: 'done', stateLabel: 'Done', description: '', tags: [] })
  });
  assert.equal(view.notice, 'This ticket has no description.');
  assert.equal(view.chip.tone, 'mute');
  assert.equal(view.title, 'Done one');
});

// ---------------------------------------------------------------------------
// File (FR34)
// ---------------------------------------------------------------------------

const fileTab = path =>
  tab(`ws1/f/${path}`, { kind: 'file', label: path.split('/').pop(), meta: { path } });

test('a file shows its path inside the workspace and offers Open and Reveal', () => {
  const view = paneView(fileTab('briefs/today.md'), { flattened: FLAT });
  assert.equal(view.sub, 'File in Night Drive');
  assert.deepEqual(view.fields, [{ label: 'Path', value: 'files/briefs/today.md' }]);
  assert.deepEqual(
    view.actions.map(action => [action.label, action.action]),
    [
      ['Open', 'file-open'],
      ['Reveal in Finder', 'file-reveal']
    ]
  );
});

test('each kind of file gets its own preview', () => {
  const preview = (path, value) =>
    paneView(fileTab(path), { flattened: FLAT, item: ready(value) }).body;
  assert.deepEqual(preview('a.md', { kind: 'markdown', text: '# Hi' }), {
    type: 'markdown',
    markdown: '# Hi',
    empty: 'This file is empty.'
  });
  assert.deepEqual(preview('a.csv', { kind: 'text', text: 'a,b' }), {
    type: 'text',
    text: 'a,b',
    empty: 'This file is empty.'
  });
  assert.deepEqual(preview('cover.png', { kind: 'image', url: '/api/x/cover.png' }), {
    type: 'image',
    src: '/api/x/cover.png',
    alt: 'cover.png'
  });
  assert.deepEqual(preview('take.wav', { kind: 'none' }), { type: 'none', text: 'No preview' });
  assert.deepEqual(preview('huge.log', { kind: 'text', tooLarge: true }), {
    type: 'none',
    text: 'This file is too large to preview here.'
  });
});

test('BACKLOG.md says that Ori keeps it in step with the backlog; other files do not', () => {
  assert.equal(
    paneView(fileTab('BACKLOG.md'), { flattened: FLAT }).lead,
    'Ori keeps this file in step with the backlog.'
  );
  assert.equal(paneView(fileTab('docs/BACKLOG.md'), { flattened: FLAT }).lead, '');
  assert.equal(paneView(fileTab('plan.md'), { flattened: FLAT }).lead, '');
});

test('a text preview is escaped, a Markdown one rendered, an image shown inline', () => {
  const html = (path, value) =>
    renderPaneHTML(paneView(fileTab(path), { flattened: FLAT, item: ready(value) }));
  const text = html('a.json', { kind: 'text', text: '{"a": "<b>"}' });
  assert.match(
    text,
    /<pre class="cockpit-pane-pre">\{&quot;a&quot;: &quot;&lt;b&gt;&quot;\}<\/pre>/
  );
  assert.match(html('a.md', { kind: 'markdown', text: '# Hi' }), /class="cockpit-pane-markdown"/);
  assert.match(
    html('cover.png', { kind: 'image', url: '/api/x/cover.png' }),
    /<img class="cockpit-pane-image" src="\/api\/x\/cover\.png" alt="cover\.png">/
  );
  assert.match(html('take.wav', { kind: 'none' }), /No preview/);
});

// ---------------------------------------------------------------------------
// Memory and agent (FR37, FR38)
// ---------------------------------------------------------------------------

test('Memory lists its entries and links to the Memory tab', () => {
  const view = paneView(tab('ws1/m', { kind: 'memory', label: 'Memory' }), {
    flattened: FLAT,
    item: ready({
      entries: [
        { text: 'Ship on Fridays.', type: 'decision', date: '2026-10-01' },
        { text: 'Keep replies short.', type: '', date: '' }
      ]
    })
  });
  assert.equal(view.title, 'Memory');
  assert.equal(view.sub, 'Memory for Night Drive');
  assert.deepEqual(view.list.items, [
    { text: 'Ship on Fridays.', meta: 'decision · 2026-10-01' },
    { text: 'Keep replies short.', meta: '' }
  ]);
  assert.deepEqual(view.actions, [
    { label: 'Open Memory', href: '/workspaces/night-drive#memory', primary: true }
  ]);
});

test('Memory with no entries says so rather than showing an empty list', () => {
  const html = renderPaneHTML(
    paneView(tab('ws1/m', { kind: 'memory' }), { flattened: FLAT, item: ready({ entries: [] }) })
  );
  assert.match(html, /No memory entries yet\./);
  assert.doesNotMatch(html, /<ul class="cockpit-pane-list">/);
});

test('an agent shows its role and model and links to the agent', () => {
  const view = paneView(
    tab('ws1/a/REAPER Assistant', {
      kind: 'agent',
      label: 'REAPER Assistant',
      meta: { name: 'REAPER Assistant', role: 'specialist', model: 'claude-sonnet-5-5' }
    }),
    { flattened: FLAT }
  );
  assert.equal(view.sub, 'Agent in Night Drive');
  assert.deepEqual(view.fields, [
    { label: 'Role', value: 'specialist' },
    { label: 'Model', value: 'claude-sonnet-5-5' }
  ]);
  assert.deepEqual(view.actions, [
    {
      label: 'Open agent',
      href: '/workspaces/night-drive/agents/REAPER%20Assistant',
      primary: true
    }
  ]);
  // An agent has nothing to load, so it is ready at once.
  assert.equal(view.status, ITEM_READY);
});

test('an agent with no role or model on record shows a dash, not a blank', () => {
  const view = paneView(
    tab('ws1/a/Bare', { kind: 'agent', label: 'Bare', meta: { name: 'Bare' } }),
    {
      flattened: FLAT
    }
  );
  assert.deepEqual(
    view.fields.map(field => field.value),
    ['—', '—']
  );
});

// ---------------------------------------------------------------------------
// Workspace and group overviews (FR39, FR40)
// ---------------------------------------------------------------------------

const SECTIONS_LOADED = {
  notes: { status: 'ready', count: 2, rows: [] },
  backlog: { status: 'ready', count: 0, rows: [] },
  files: { status: 'loading', count: null, rows: [] },
  memory: { status: 'ready', count: 3, rows: [] },
  agents: { status: 'failed', count: null, rows: [] }
};

test('a workspace overview shows status, counts, next run, tags and its buttons', () => {
  const view = paneView(tab('ws1', { kind: 'workspace' }), {
    flattened: FLAT,
    tagsById: { ws1: ['music', 'reaper'] },
    scheduleIndex: {},
    sections: SECTIONS_LOADED
  });
  assert.deepEqual(view.chip, { label: 'Needs attention', tone: 'warn' });
  assert.deepEqual(view.tags, ['music', 'reaper']);
  assert.equal(view.lead, 'The first single.');
  assert.deepEqual(view.stats, [
    { value: '2', label: 'Agents' },
    { value: '3', label: 'Open tasks' },
    { value: '1', label: 'Need attention' }
  ]);
  assert.deepEqual(view.fields, [{ label: 'Next run', value: 'No schedule' }]);
  assert.deepEqual(view.actions, [
    { label: 'Open workspace', href: '/workspaces/night-drive', primary: true },
    { label: 'Move…', action: 'move' },
    { label: 'Delete', action: 'delete', danger: true }
  ]);
});

test('a count the server did not send reads "—", never "0" (FR39)', () => {
  const view = paneView(tab('solo', { kind: 'workspace', workspaceId: 'solo' }), {
    flattened: FLAT,
    scheduleIndex: null
  });
  assert.deepEqual(
    view.stats.map(stat => stat.value),
    ['—', '—', '—']
  );
  assert.equal(view.chip.label, 'Status unavailable');
  // The schedule has not loaded either: unknown, not "No schedule".
  assert.deepEqual(view.fields, [{ label: 'Next run', value: '—' }]);
});

test('a workspace overview lists every section with its count, loaded or not', () => {
  const view = paneView(tab('ws1', { kind: 'workspace' }), {
    flattened: FLAT,
    sections: SECTIONS_LOADED
  });
  const [group] = view.linkGroups;
  assert.equal(group.label, 'In this workspace');
  assert.deepEqual(
    group.links.map(link => [link.label, link.meta, link.action, link.target]),
    [
      ['Notes', '2', 'reveal-section', 'notes'],
      ['Backlog', '0', 'reveal-section', 'backlog'],
      ['Files', '—', 'reveal-section', 'files'],
      // Memory is one item: its link opens it instead of revealing a section.
      ['Memory', '3', 'open-item', 'ws1/m'],
      ['Agents', '—', 'reveal-section', 'agents']
    ]
  );
  // Nothing loaded at all: every count is unknown.
  assert.deepEqual(
    sectionLinks('ws1', null).map(link => link.meta),
    ['—', '—', '—', '—', '—']
  );
});

test('nextRunLabel: the earliest run, "No schedule" when none, "—" when unknown', () => {
  assert.equal(nextRunLabel('ws1', null), '—');
  assert.equal(nextRunLabel('ws1', {}), 'No schedule');
  assert.equal(nextRunLabel('ws1', { ws1: [{ next_run: 'not a date' }] }), 'No schedule');
  const label = nextRunLabel(
    'ws1',
    {
      ws1: [{ next_run: '2026-10-09T08:00:00Z' }, { next_run_at: '2026-10-05T08:00:00Z' }],
      other: [{ next_run: '2026-10-01T08:00:00Z' }]
    },
    'en-US'
  );
  assert.match(label, /Oct 5, 2026/);
});

test('a group overview lists its children, then only its own non-empty sections (FR40)', () => {
  const view = paneView(tab('g1', { kind: 'group', workspaceId: 'g1' }), {
    flattened: FLAT,
    sections: SECTIONS_LOADED
  });
  assert.equal(view.sub, 'Group');
  assert.equal(view.lead, 'Songs in progress.');
  assert.equal(view.chip, null);
  const [children, own] = view.linkGroups;
  assert.equal(children.label, 'Workspaces in this group');
  assert.deepEqual(
    children.links.map(link => [link.label, link.meta, link.action, link.target]),
    [
      ['Night Drive', 'Needs attention', 'open-workspace', 'ws1'],
      ['Stems', 'Group', 'open-workspace', 'g2']
    ]
  );
  assert.deepEqual(
    own.links.map(link => link.label),
    ['Notes', 'Memory']
  );
  assert.deepEqual(view.actions, [
    { label: 'Open group', href: '/workspaces/music', primary: true },
    { label: 'Move…', action: 'move' },
    { label: 'Delete', action: 'delete', danger: true }
  ]);
});

test('a group overview totals its workspaces from the same figures the rail uses', () => {
  const view = paneView(tab('g1', { kind: 'group', workspaceId: 'g1' }), { flattened: FLAT });
  assert.deepEqual(view.stats, [
    { value: '1', label: 'Workspaces' },
    { value: '3', label: 'Open tasks' },
    { value: '1', label: 'Need attention' }
  ]);
  // No sections loaded and none non-empty: only the children are listed.
  assert.equal(view.linkGroups.length, 1);
});

test('an empty group says so', () => {
  const html = renderPaneHTML(
    paneView(tab('g2', { kind: 'group', workspaceId: 'g2' }), { flattened: FLAT })
  );
  assert.match(html, /This group is empty\./);
  assert.match(html, /Group in Music/);
});

test('overview buttons are links where they navigate and buttons where they act', () => {
  const html = renderPaneHTML(
    paneView(tab('ws1', { kind: 'workspace' }), { flattened: FLAT, sections: SECTIONS_LOADED })
  );
  assert.match(
    html,
    /<a class="modern-btn modern-btn-primary modern-btn-sm" href="\/workspaces\/night-drive">Open workspace<\/a>/
  );
  assert.match(
    html,
    /<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-pane-action="move">Move…<\/button>/
  );
  assert.match(
    html,
    /class="modern-btn modern-btn-danger modern-btn-sm" data-pane-action="delete">Delete</
  );
  assert.match(html, /data-pane-action="reveal-section" data-pane-target="notes"/);
  assert.match(html, /class="cockpit-pane-chip is-warn">Needs attention</);
  assert.match(html, /class="cockpit-pane-stat-value">3</);
});

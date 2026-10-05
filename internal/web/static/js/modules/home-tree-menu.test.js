// Tests for home-tree-menu.js — what a row's menu holds and where it is put.
//
// Opening a menu needs a document and is exercised in the browser walkthrough;
// what is tested here is what decides its contents and its position.
//   node --test internal/web/static/js/modules/home-tree-menu.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  MENU_DELETE,
  MENU_FILE_OPEN,
  MENU_FILE_REVEAL,
  MENU_MOVE,
  MENU_NEW_NOTE,
  MENU_NEW_TICKET,
  MENU_OPEN,
  MENU_OPEN_IN_WORKSPACE,
  MENU_OPEN_PAGE,
  MENU_REFRESH,
  MENU_SHOW_OUTPUTS,
  MENU_UPLOAD,
  menuItemsFor,
  menuPosition,
  nextMenuIndex,
  renderMenuHTML,
  rowHasMenu
} from './home-tree-menu.js';

const labels = row =>
  menuItemsFor(row)
    .filter(entry => !entry.divider)
    .map(entry => entry.label);

test('a workspace and a group carry the whole management menu (FR55)', () => {
  assert.deepEqual(labels({ kind: 'workspace' }), [
    'Open workspace',
    'New note',
    'New ticket',
    'Upload file…',
    'Refresh',
    'Move…',
    'Delete'
  ]);
  assert.deepEqual(labels({ kind: 'group' }), [
    'Open group',
    'New note',
    'New ticket',
    'Upload file…',
    'Refresh',
    'Move…',
    'Delete'
  ]);
  assert.deepEqual(
    menuItemsFor({ kind: 'workspace' })
      .filter(entry => !entry.divider)
      .map(entry => entry.action),
    [
      MENU_OPEN_PAGE,
      MENU_NEW_NOTE,
      MENU_NEW_TICKET,
      MENU_UPLOAD,
      MENU_REFRESH,
      MENU_MOVE,
      MENU_DELETE
    ]
  );
});

test('Delete is the one item marked as dangerous', () => {
  const dangerous = menuItemsFor({ kind: 'group' }).filter(entry => entry.danger);
  assert.deepEqual(
    dangerous.map(entry => entry.label),
    ['Delete']
  );
  assert.match(
    renderMenuHTML(menuItemsFor({ kind: 'group' }), 'Actions'),
    /class="ori-context-item ori-context-danger"[^>]*data-tree-menu-action="delete"/
  );
});

test('a note, a ticket and an agent offer Open and Open in workspace (FR55)', () => {
  ['note', 'ticket', 'agent'].forEach(kind => {
    assert.deepEqual(labels({ kind }), ['Open', 'Open in workspace'], kind);
    assert.deepEqual(
      menuItemsFor({ kind }).map(entry => entry.action),
      [MENU_OPEN, MENU_OPEN_IN_WORKSPACE],
      kind
    );
  });
});

test('a file offers Open, Open in default app and Reveal in Finder (FR55)', () => {
  assert.deepEqual(labels({ kind: 'file' }), ['Open', 'Open in default app', 'Reveal in Finder']);
  assert.deepEqual(
    menuItemsFor({ kind: 'file' }).map(entry => entry.action),
    [MENU_OPEN, MENU_FILE_OPEN, MENU_FILE_REVEAL]
  );
});

test('the Outputs section offers "Show outputs folder", and nothing to create (FR55, D15)', () => {
  assert.deepEqual(labels({ kind: 'section', section: 'outputs' }), ['Show outputs folder']);
  assert.deepEqual(
    menuItemsFor({ kind: 'section', section: 'outputs' }).map(entry => entry.action),
    [MENU_SHOW_OUTPUTS]
  );
  assert.equal(rowHasMenu({ kind: 'section', section: 'outputs' }), true);
});

test('an output file offers Open only: no default app, no Reveal, no workspace page (FR55)', () => {
  assert.deepEqual(labels({ kind: 'output' }), ['Open']);
  const actions = menuItemsFor({ kind: 'output' }).map(entry => entry.action);
  assert.deepEqual(actions, [MENU_OPEN]);
  [MENU_FILE_OPEN, MENU_FILE_REVEAL, MENU_OPEN_IN_WORKSPACE, MENU_UPLOAD].forEach(action =>
    assert.equal(actions.includes(action), false, action)
  );
});

test('a folder in Outputs has no menu: nothing is uploaded into outputs/', () => {
  assert.deepEqual(menuItemsFor({ kind: 'outputFolder' }), []);
  assert.equal(rowHasMenu({ kind: 'outputFolder' }), false);
});

test('a file in a linked folder offers Open only (FR55)', () => {
  assert.deepEqual(labels({ kind: 'linkedFile' }), ['Open']);
  assert.deepEqual(
    menuItemsFor({ kind: 'linkedFile' }).map(entry => entry.action),
    [MENU_OPEN]
  );
});

test('a linked folder, the folders inside it and its section have no menu', () => {
  [{ kind: 'linked' }, { kind: 'linkedFolder' }, { kind: 'section', section: 'linked' }].forEach(
    row => {
      assert.deepEqual(menuItemsFor(row), [], row.kind);
      assert.equal(rowHasMenu(row), false);
    }
  );
});

test('content rows cannot be moved or deleted from the tree (non-goal)', () => {
  [
    'note',
    'ticket',
    'agent',
    'file',
    'folder',
    'output',
    'outputFolder',
    'linked',
    'linkedFolder',
    'linkedFile',
    'memory'
  ].forEach(kind => {
    const actions = menuItemsFor({ kind, section: 'files' }).map(entry => entry.action);
    assert.equal(actions.includes(MENU_MOVE), false, kind);
    assert.equal(actions.includes(MENU_DELETE), false, kind);
  });
});

test('each section offers the one thing that can be created in it (FR55)', () => {
  assert.deepEqual(labels({ kind: 'section', section: 'notes' }), ['New note']);
  assert.deepEqual(labels({ kind: 'section', section: 'backlog' }), ['New ticket']);
  assert.deepEqual(labels({ kind: 'section', section: 'files' }), ['Upload file…']);
  assert.deepEqual(labels({ kind: 'folder' }), ['Upload file…']);
});

test('rows with nothing to offer have no menu, and so no "⋯" button', () => {
  [
    { kind: 'section', section: 'agents' },
    { kind: 'memory' },
    { kind: 'loading' },
    { kind: 'empty' },
    { kind: 'failed' },
    { kind: 'more' },
    { kind: 'draft' },
    null
  ].forEach(row => {
    assert.deepEqual(menuItemsFor(row), []);
    assert.equal(rowHasMenu(row), false);
  });
  assert.equal(rowHasMenu({ kind: 'workspace' }), true);
});

test('the menu is drawn with menu semantics, one tabbable item at a time', () => {
  const html = renderMenuHTML(menuItemsFor({ kind: 'workspace' }), 'Actions for My HQ');
  assert.match(html, /class="ori-context-menu cockpit-tree-menu"/);
  assert.match(html, /role="menu" aria-label="Actions for My HQ"/);
  assert.equal((html.match(/role="menuitem"/g) || []).length, 7);
  assert.equal((html.match(/role="separator"/g) || []).length, 3);
  assert.doesNotMatch(html, /tabindex="0"/);
  assert.match(html, /data-tree-menu-action="new-note"[^>]*>New note</);
});

test('a menu label is escaped', () => {
  const html = renderMenuHTML(menuItemsFor({ kind: 'folder' }), 'Actions for <img src=x>');
  assert.doesNotMatch(html, /<img/);
  assert.match(html, /Actions for &lt;img/);
});

test('a menu opens at the point when it fits there', () => {
  assert.deepEqual(
    menuPosition({ x: 120, y: 200 }, { width: 180, height: 160 }, { width: 1440, height: 900 }),
    { left: 120, top: 200 }
  );
});

test('a menu that would run off the right or bottom edge is pulled back inside', () => {
  assert.deepEqual(
    menuPosition({ x: 1400, y: 880 }, { width: 180, height: 160 }, { width: 1440, height: 900 }),
    { left: 1440 - 180 - 8, top: 900 - 160 - 8 }
  );
});

test('a menu never starts off the top or left edge, even in a tiny window', () => {
  assert.deepEqual(
    menuPosition({ x: -20, y: -5 }, { width: 180, height: 160 }, { width: 100, height: 100 }),
    { left: 8, top: 8 }
  );
});

test('arrow keys wrap at both ends of the menu', () => {
  assert.equal(nextMenuIndex(0, 1, 4), 1);
  assert.equal(nextMenuIndex(3, 1, 4), 0);
  assert.equal(nextMenuIndex(0, -1, 4), 3);
  // Nothing focused yet: Down goes to the first item, Up to the last.
  assert.equal(nextMenuIndex(-1, 1, 4), 0);
  assert.equal(nextMenuIndex(-1, -1, 4), 3);
  assert.equal(nextMenuIndex(0, 1, 0), -1);
});

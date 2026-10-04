// Tests for home-tree-state.js — what the Home file tree remembers between
// visits (tasks/prd-home-file-tree.md FR68-FR70).
//
//   node --test internal/web/static/js/modules/home-tree-state.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  MAX_STORED_ROWS,
  MAX_STORED_TABS,
  TREE_STATE_KEY,
  TREE_STATE_VERSION,
  applyTreeState,
  dropMissingRows,
  dropMissingTabs,
  isGoneError,
  parseTreeState,
  readTreeState,
  snapshotTreeState,
  vanishedTabKeys,
  writeTreeState
} from './home-tree-state.js';

// A stand-in for localStorage that counts its writes.
function fakeStorage(initial = {}) {
  const data = { ...initial };
  const storage = {
    writes: 0,
    getItem: key => (key in data ? data[key] : null),
    setItem: (key, value) => {
      storage.writes += 1;
      data[key] = String(value);
    },
    data
  };
  return storage;
}

const noteTab = {
  key: 'ws1/n/n1',
  kind: 'note',
  workspaceId: 'ws1',
  label: 'Lyrics',
  meta: { noteId: 'n1' }
};
const fileTab = {
  key: 'ws1/f/stems/lead.wav',
  kind: 'file',
  workspaceId: 'ws1',
  label: 'lead.wav',
  meta: { path: 'stems/lead.wav', size: 2048 }
};
const overviewTab = {
  key: 'ws2',
  kind: 'workspace',
  workspaceId: 'ws2',
  label: 'Harbor',
  meta: {}
};

function homeState(overrides = {}) {
  return {
    collapsedGroups: new Set(['g1', 'ws1/s/backlog']),
    expandedRows: new Set(['ws1', 'ws1/d/stems']),
    treeTabs: [noteTab, fileTab, overviewTab],
    activeTabKey: fileTab.key,
    ...overrides
  };
}

// ---------------------------------------------------------------------------
// What is stored
// ---------------------------------------------------------------------------

test('the key carries the version, so a new shape starts clean', () => {
  assert.equal(TREE_STATE_KEY, `ori.home.fileTree.v${TREE_STATE_VERSION}`);
});

test('a snapshot holds the open rows, the tabs and the active tab, and nothing else', () => {
  const snapshot = snapshotTreeState({
    ...homeState(),
    // None of these may reach storage.
    treeTabItems: { [noteTab.key]: { status: 'ready', value: { content: 'secret text' } } },
    treeContents: { ws1: { sections: {} } },
    treeFilter: 'lyr',
    bulkSelection: new Set(['ws1']),
    activeTags: new Set(['music'])
  });
  assert.deepEqual(Object.keys(snapshot).sort(), [
    'activeKey',
    'collapsed',
    'expanded',
    'tabs',
    'version'
  ]);
  assert.equal(snapshot.version, TREE_STATE_VERSION);
  assert.deepEqual(snapshot.collapsed, ['g1', 'ws1/s/backlog']);
  assert.deepEqual(snapshot.expanded, ['ws1', 'ws1/d/stems']);
  assert.deepEqual(snapshot.tabs, [noteTab, fileTab, overviewTab]);
  assert.equal(snapshot.activeKey, fileTab.key);
  assert.doesNotMatch(JSON.stringify(snapshot), /secret text/);
});

test('a tab keeps only what its loader needs: the restored flag and nested values are left out', () => {
  const snapshot = snapshotTreeState(
    homeState({
      treeTabs: [
        {
          ...noteTab,
          restored: true,
          extra: 'x',
          meta: { noteId: 'n1', nested: { a: 1 }, list: [1], bad: NaN }
        }
      ],
      activeTabKey: noteTab.key
    })
  );
  assert.deepEqual(snapshot.tabs, [noteTab]);
});

test('an active tab that is not among the tabs is not stored as active', () => {
  assert.equal(snapshotTreeState(homeState({ activeTabKey: 'gone' })).activeKey, '');
  assert.equal(snapshotTreeState(homeState({ activeTabKey: '' })).activeKey, '');
});

test('an empty Home stores an empty snapshot rather than throwing', () => {
  [undefined, null, {}].forEach(state => {
    assert.deepEqual(snapshotTreeState(state), {
      version: TREE_STATE_VERSION,
      collapsed: [],
      expanded: [],
      tabs: [],
      activeKey: ''
    });
  });
});

test('what is stored is bounded: the newest tabs are the ones kept', () => {
  const tabs = Array.from({ length: MAX_STORED_TABS + 5 }, (_, i) => ({
    key: `ws1/n/n${i}`,
    kind: 'note',
    workspaceId: 'ws1',
    label: `Note ${i}`,
    meta: { noteId: `n${i}` }
  }));
  const snapshot = snapshotTreeState(homeState({ treeTabs: tabs, activeTabKey: tabs[0].key }));
  assert.equal(snapshot.tabs.length, MAX_STORED_TABS);
  assert.equal(snapshot.tabs[0].key, 'ws1/n/n5');
  // The active tab was among the ones left out.
  assert.equal(snapshot.activeKey, '');
  const rows = new Set(Array.from({ length: MAX_STORED_ROWS + 10 }, (_, i) => `ws${i}`));
  assert.equal(
    snapshotTreeState(homeState({ expandedRows: rows })).expanded.length,
    MAX_STORED_ROWS
  );
});

// ---------------------------------------------------------------------------
// Reading it back
// ---------------------------------------------------------------------------

test('a snapshot survives being stored and read back', () => {
  const snapshot = snapshotTreeState(homeState());
  assert.deepEqual(parseTreeState(JSON.stringify(snapshot)), snapshot);
});

test('nothing stored, text that is not JSON, and another version all read as nothing', () => {
  [
    null,
    undefined,
    '',
    'not json',
    '{"version":',
    '[]',
    'null',
    '42',
    JSON.stringify({ version: TREE_STATE_VERSION + 1, tabs: [], collapsed: [], expanded: [] }),
    JSON.stringify({ tabs: [noteTab] })
  ].forEach(text => assert.equal(parseTreeState(text), null, String(text)));
});

test('stored text of the right version but the wrong shape is cleaned, not trusted', () => {
  const parsed = parseTreeState(
    JSON.stringify({
      version: TREE_STATE_VERSION,
      collapsed: ['g1', 7, '', null, 'g1', { id: 'x' }],
      expanded: 'ws1',
      tabs: [
        noteTab,
        noteTab, // the same tab twice
        { key: 'ws1/x/1', kind: 'script', workspaceId: 'ws1', label: 'not a kind' },
        { key: '', kind: 'note', workspaceId: 'ws1' },
        { key: 'ws9/n/n9', kind: 'note' },
        'a string',
        null,
        { key: 'ws2', kind: 'workspace', workspaceId: 'ws2', label: 12, meta: 'nope' }
      ],
      activeKey: 'ws1/x/1'
    })
  );
  assert.deepEqual(parsed.collapsed, ['g1']);
  assert.deepEqual(parsed.expanded, []);
  assert.deepEqual(parsed.tabs, [
    noteTab,
    { key: 'ws2', kind: 'workspace', workspaceId: 'ws2', label: '', meta: {} }
  ]);
  // The stored active tab was one of the entries thrown away.
  assert.equal(parsed.activeKey, '');
});

test('readTreeState reads the versioned key, and a storage that throws has nothing', () => {
  const snapshot = snapshotTreeState(homeState());
  const storage = fakeStorage({ [TREE_STATE_KEY]: JSON.stringify(snapshot) });
  assert.deepEqual(readTreeState(storage), snapshot);
  assert.equal(readTreeState(fakeStorage()), null);
  assert.equal(
    readTreeState(fakeStorage({ 'ori.home.fileTree.v0': JSON.stringify(snapshot) })),
    null
  );
  const broken = {
    getItem: () => {
      throw new Error('storage is disabled');
    }
  };
  assert.equal(readTreeState(broken), null);
  assert.equal(readTreeState(null), null);
});

test('writeTreeState stores under the key and skips a write that would change nothing', () => {
  const storage = fakeStorage();
  const snapshot = snapshotTreeState(homeState());
  const first = writeTreeState(storage, snapshot, '');
  assert.equal(storage.writes, 1);
  assert.equal(storage.data[TREE_STATE_KEY], first);
  assert.deepEqual(parseTreeState(first), snapshot);
  // The same state again: nothing is written.
  assert.equal(writeTreeState(storage, snapshotTreeState(homeState()), first), first);
  assert.equal(storage.writes, 1);
  // A change is.
  const changed = writeTreeState(
    storage,
    snapshotTreeState(homeState({ activeTabKey: noteTab.key })),
    first
  );
  assert.notEqual(changed, first);
  assert.equal(storage.writes, 2);
});

test('a storage that refuses the write is not an error, and is tried again next time', () => {
  const full = {
    setItem: () => {
      throw new Error('QuotaExceededError');
    }
  };
  const snapshot = snapshotTreeState(homeState());
  assert.equal(writeTreeState(full, snapshot, 'what was stored before'), 'what was stored before');
});

// ---------------------------------------------------------------------------
// Putting it back (FR68)
// ---------------------------------------------------------------------------

test('applyTreeState fills the row sets, the tabs and the active tab', () => {
  const state = {
    collapsedGroups: new Set(),
    expandedRows: new Set(),
    treeTabs: [],
    activeTabKey: ''
  };
  const snapshot = snapshotTreeState(homeState());
  assert.equal(applyTreeState(state, snapshot), true);
  assert.ok(state.collapsedGroups instanceof Set);
  assert.deepEqual([...state.collapsedGroups], ['g1', 'ws1/s/backlog']);
  assert.deepEqual([...state.expandedRows], ['ws1', 'ws1/d/stems']);
  assert.equal(state.activeTabKey, fileTab.key);
  assert.deepEqual(
    state.treeTabs.map(tab => tab.key),
    [noteTab.key, fileTab.key, overviewTab.key]
  );
});

test('every restored tab is marked, so one whose item is gone can be dropped silently', () => {
  const state = {};
  applyTreeState(state, snapshotTreeState(homeState()));
  assert.ok(state.treeTabs.every(tab => tab.restored === true));
  // …and the mark never goes back into storage.
  assert.ok(snapshotTreeState(state).tabs.every(tab => !('restored' in tab)));
});

test('restored tabs are copies: changing one does not change the snapshot', () => {
  const snapshot = snapshotTreeState(homeState());
  const state = {};
  applyTreeState(state, snapshot);
  state.treeTabs[1].meta.path = 'changed';
  assert.equal(snapshot.tabs[1].meta.path, 'stems/lead.wav');
});

test('with nothing stored the state is left exactly as it was', () => {
  const state = homeState();
  assert.equal(applyTreeState(state, null), false);
  assert.deepEqual(state.treeTabs, [noteTab, fileTab, overviewTab]);
  assert.equal(applyTreeState(null, snapshotTreeState(homeState())), false);
});

// ---------------------------------------------------------------------------
// Dropping what is gone (FR69)
// ---------------------------------------------------------------------------

const tabs = [noteTab, fileTab, overviewTab];

test('tabs of a workspace that still exists are all kept, active tab included', () => {
  assert.deepEqual(dropMissingTabs(tabs, fileTab.key, new Set(['ws1', 'ws2'])), {
    tabs,
    activeKey: fileTab.key
  });
});

test('tabs of a workspace that is gone are dropped; the active tab stays if it survives', () => {
  assert.deepEqual(dropMissingTabs(tabs, noteTab.key, ['ws1']), {
    tabs: [noteTab, fileTab],
    activeKey: noteTab.key
  });
});

test('when the active tab goes, the nearest survivor to its right takes over, else to its left', () => {
  // ws1 gone: the note and the file go; the overview to their right survives.
  assert.deepEqual(dropMissingTabs(tabs, noteTab.key, ['ws2']), {
    tabs: [overviewTab],
    activeKey: overviewTab.key
  });
  // ws2 gone and its tab was active: nothing to the right, so the one before.
  assert.deepEqual(dropMissingTabs(tabs, overviewTab.key, ['ws1']), {
    tabs: [noteTab, fileTab],
    activeKey: fileTab.key
  });
});

test('with every workspace gone no tab is left and none is active', () => {
  assert.deepEqual(dropMissingTabs(tabs, fileTab.key, new Set()), { tabs: [], activeKey: '' });
  assert.deepEqual(dropMissingTabs(null, 'x', null), { tabs: [], activeKey: '' });
});

test('an active key that names no tab does not survive', () => {
  assert.equal(dropMissingTabs(tabs, 'nothing', ['ws1', 'ws2']).activeKey, '');
});

test('remembered rows of a workspace that is gone are dropped, at every depth', () => {
  const rows = new Set(['g1', 'ws1', 'ws1/s/notes', 'ws1/d/stems/sub', 'ws2', 'ws2/s/files']);
  assert.deepEqual(dropMissingRows(rows, new Set(['g1', 'ws2'])), ['g1', 'ws2', 'ws2/s/files']);
  assert.deepEqual(dropMissingRows(rows, []), []);
  assert.deepEqual(dropMissingRows(null, ['ws1']), []);
});

test('a row key that is not a key at all is dropped rather than kept forever', () => {
  assert.deepEqual(dropMissingRows(['ws1/zz/what', '', 'ws1'], ['ws1']), ['ws1']);
});

// --- A restored tab whose item is gone ------------------------------------

const restored = tab => ({ ...tab, restored: true });
const agentTab = {
  key: 'ws1/a/Scout',
  kind: 'agent',
  workspaceId: 'ws1',
  label: 'Scout',
  meta: {}
};
const ticketTab = {
  key: 'ws1/t/t1',
  kind: 'ticket',
  workspaceId: 'ws1',
  label: 'Mix',
  meta: { ticketId: 't1' }
};
const readySection = rows => ({ status: 'ready', rows, count: rows.length, error: '' });

test('a restored note that is not in the loaded Notes list is gone', () => {
  const open = [restored(noteTab), restored(fileTab)];
  const notes = readySection([{ id: 'ws1/n/other', kind: 'note' }]);
  assert.deepEqual(vanishedTabKeys(open, 'ws1', 'notes', notes), [noteTab.key]);
  // Present: kept.
  assert.deepEqual(
    vanishedTabKeys(open, 'ws1', 'notes', readySection([{ id: noteTab.key, kind: 'note' }])),
    []
  );
});

test('a restored file is looked for inside folders too', () => {
  const inFolder = readySection([
    { id: 'ws1/d/stems', kind: 'folder', children: [{ id: fileTab.key, kind: 'file' }] }
  ]);
  assert.deepEqual(vanishedTabKeys([restored(fileTab)], 'ws1', 'files', inFolder), []);
  const emptied = readySection([{ id: 'ws1/d/stems', kind: 'folder', children: [] }]);
  assert.deepEqual(vanishedTabKeys([restored(fileTab)], 'ws1', 'files', emptied), [fileTab.key]);
});

test('a restored agent that left the workspace is gone', () => {
  assert.deepEqual(vanishedTabKeys([restored(agentTab)], 'ws1', 'agents', readySection([])), [
    agentTab.key
  ]);
});

test('only a list that is whole can prove something is gone', () => {
  const open = [restored(noteTab)];
  // Cut at the row limit: the note may be one of the rows not shown.
  const capped = readySection([{ id: 'ws1/more/notes', kind: 'more' }]);
  assert.deepEqual(vanishedTabKeys(open, 'ws1', 'notes', capped), []);
  // Still loading, or failed: no evidence either way.
  assert.deepEqual(vanishedTabKeys(open, 'ws1', 'notes', { status: 'loading', rows: [] }), []);
  assert.deepEqual(vanishedTabKeys(open, 'ws1', 'notes', { status: 'failed', rows: [] }), []);
  assert.deepEqual(vanishedTabKeys(open, 'ws1', 'notes', null), []);
});

test('a "more" row inside a folder makes the Files list not whole either', () => {
  const cappedInside = readySection([
    { id: 'ws1/d/stems', kind: 'folder', children: [{ id: 'ws1/more/files', kind: 'more' }] }
  ]);
  assert.deepEqual(vanishedTabKeys([restored(fileTab)], 'ws1', 'files', cappedInside), []);
});

test('the Backlog list never proves a ticket gone: it leaves finished tickets out', () => {
  assert.deepEqual(vanishedTabKeys([restored(ticketTab)], 'ws1', 'backlog', readySection([])), []);
});

test('a tab opened in this visit is never dropped, and neither is another workspace’s', () => {
  const notes = readySection([]);
  assert.deepEqual(vanishedTabKeys([noteTab], 'ws1', 'notes', notes), []);
  assert.deepEqual(vanishedTabKeys([{ ...noteTab, restored: false }], 'ws1', 'notes', notes), []);
  assert.deepEqual(vanishedTabKeys([restored(noteTab)], 'ws2', 'notes', notes), []);
  // The Notes list says nothing about a file tab.
  assert.deepEqual(vanishedTabKeys([restored(fileTab)], 'ws1', 'notes', notes), []);
});

test('isGoneError: 404 and 410 mean gone; anything else is a failure to report', () => {
  assert.equal(isGoneError(Object.assign(new Error('Not found'), { status: 404 })), true);
  assert.equal(isGoneError(Object.assign(new Error('Gone'), { status: 410 })), true);
  assert.equal(isGoneError(Object.assign(new Error('Boom'), { status: 500 })), false);
  assert.equal(isGoneError(new Error('Failed to fetch')), false);
  assert.equal(isGoneError(null), false);
});

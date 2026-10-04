// Tests for home-workspace-tree.js — the Tree peer view's hierarchy, its
// contents (sections, folders, loading and failed rows), move validation, bulk
// selection, keyboard model, and rendering.
//
// Pure helpers only; the mount/interaction layer needs a DOM and is exercised
// in the browser walkthrough instead.
//   node --test internal/web/static/js/modules/home-workspace-tree.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { flattenWorkspaceTree } from './home-workspace-cockpit.js';
import {
  visibleTreeRows,
  descendantIds,
  ancestorIds,
  moveRejectionReason,
  isMoveAllowed,
  moveDestinations,
  moveOrderUpdates,
  bulkSelectionState,
  workspaceMarker,
  renderTreeHTML,
  renderMoveDialogHTML,
  resolveTreeKey,
  renderTagFilterBarHTML,
  filterTreeByTags,
  treeCanUndo,
  isRowExpanded,
  setRowExpanded,
  revealTargets,
  rowActivation,
  treeActiveRowId,
  isWorkspaceRowKind
} from './home-workspace-tree.js';
import {
  agentsToRows,
  filesToRows,
  memoryToRows,
  notesToRows,
  ticketsToRows
} from './home-tree-sources.js';

test('Undo is enabled exactly when the cockpit has a trashed item to restore', () => {
  // The cockpit only ever maintains undoStack; before this read it, the button
  // stayed disabled for every delete because nothing set canUndo.
  assert.equal(treeCanUndo({ undoStack: [] }), false);
  assert.equal(treeCanUndo({ undoStack: [{ id: 'home', name: 'Music Home' }] }), true);
  assert.equal(treeCanUndo({}), false);
  assert.equal(treeCanUndo(null), false);
  // An explicit flag still wins, in either direction.
  assert.equal(treeCanUndo({ canUndo: false, undoStack: [{ id: 'x', name: 'X' }] }), false);
  assert.equal(treeCanUndo({ canUndo: true, undoStack: [] }), true);
});

// A three-level hierarchy: Platform > [API, Web, Infra > DB], plus Standalone.
function tree() {
  return [
    {
      id: 'g1',
      name: 'Platform',
      kind: 'group',
      children: [
        { id: 'w1', name: 'API', open_task_count: 3, agent_count: 2, needs_attention_count: 1 },
        { id: 'w2', name: 'Web', open_task_count: 0, agent_count: 1, needs_attention_count: 0 },
        {
          id: 'g2',
          name: 'Infra',
          kind: 'group',
          children: [{ id: 'w3', name: 'DB' }]
        }
      ]
    },
    { id: 'w4', name: 'Standalone' }
  ];
}

const flat = () => flattenWorkspaceTree(tree());

// ---------------------------------------------------------------------------
// Hierarchy (FR41, FR42)
// ---------------------------------------------------------------------------

test('visibleTreeRows walks the hierarchy in document order with correct depth', () => {
  const rows = visibleTreeRows(tree(), new Set());
  assert.deepEqual(
    rows.map(r => r.id),
    ['g1', 'w1', 'w2', 'g2', 'w3', 'w4']
  );
  assert.deepEqual(
    rows.map(r => r.depth),
    [0, 1, 1, 1, 2, 0]
  );
});

test('visibleTreeRows OMITS rows inside a collapsed group, not merely hides them', () => {
  // Arrow-key navigation and aria-setsize must describe reachable rows only.
  const rows = visibleTreeRows(tree(), new Set(['g1']));
  assert.deepEqual(
    rows.map(r => r.id),
    ['g1', 'w4']
  );
  assert.equal(rows[0].expanded, false);
});

test('collapsing a nested group leaves its ancestors expanded', () => {
  const rows = visibleTreeRows(tree(), new Set(['g2']));
  assert.deepEqual(
    rows.map(r => r.id),
    ['g1', 'w1', 'w2', 'g2', 'w4']
  );
});

test('visibleTreeRows sets posinset/setsize per sibling group, not globally', () => {
  const rows = visibleTreeRows(tree(), new Set());
  const g1 = rows.find(r => r.id === 'g1');
  const w2 = rows.find(r => r.id === 'w2');
  assert.equal(g1.posInSet, 1);
  assert.equal(g1.setSize, 2); // g1, w4 at the top level
  assert.equal(w2.posInSet, 2);
  assert.equal(w2.setSize, 3); // w1, w2, g2 inside Platform
});

test('a childless node with kind=group is still a group; a node with children is too', () => {
  const rows = visibleTreeRows(
    [
      { id: 'empty', name: 'Empty', kind: 'group', children: [] },
      { id: 'implicit', name: 'Implicit', children: [{ id: 'kid', name: 'Kid' }] }
    ],
    new Set()
  );
  assert.equal(rows.find(r => r.id === 'empty').isGroup, true);
  assert.equal(rows.find(r => r.id === 'empty').hasChildren, false);
  assert.equal(rows.find(r => r.id === 'implicit').isGroup, true);
});

test('with no contents loaded the rows are still just groups and workspaces', () => {
  // Groups start open and workspaces start closed (FR6), and a group's own
  // sections only appear once their data has arrived.
  const rows = visibleTreeRows(tree(), new Set());
  assert.ok(rows.every(r => isWorkspaceRowKind(r.kind)));
  assert.equal(rows.find(r => r.id === 'g1').expanded, true);
  assert.equal(rows.find(r => r.id === 'w1').expanded, false);
  assert.equal(rows.find(r => r.id === 'w1').expandable, true);
});

// ---------------------------------------------------------------------------
// Contents: sections, items and their placeholder rows (FR9-FR19)
// ---------------------------------------------------------------------------

const ready = shaped => ({ status: 'ready', error: '', ...shaped });
const loading = () => ({ status: 'loading', rows: [], count: null, error: '' });
const failed = message => ({ status: 'failed', rows: [], count: null, error: message });

// What home-tree-sources.js hands the tree for one workspace.
function contentsFor(id, overrides = {}) {
  return {
    sections: {
      notes: ready(
        notesToRows(id, {
          notes: [
            { id: 'n2', name: 'Weekly review' },
            { id: 'n1', name: 'Arrangement' }
          ]
        })
      ),
      backlog: ready(
        ticketsToRows(id, {
          tickets: [
            { id: 't1', title: 'Record vocals', state: 'in_progress', state_label: 'In progress' },
            { id: 't2', title: 'Renew the domain', state: 'done', state_label: 'Done' }
          ]
        })
      ),
      files: ready(
        filesToRows(id, {
          files: [{ relative_path: 'BACKLOG.md' }, { relative_path: 'stems/lead.wav' }]
        })
      ),
      memory: ready(memoryToRows(id, { entries: [{ text: 'Ship on Fridays.' }] })),
      agents: ready(agentsToRows(id, { agents: [{ name: 'Scout', role: 'researcher' }] })),
      ...overrides
    }
  };
}

const emptyContents = () => ({
  sections: {
    notes: ready(notesToRows('x', {})),
    backlog: ready(ticketsToRows('x', {})),
    files: ready(filesToRows('x', {})),
    memory: ready(memoryToRows('x', {})),
    agents: ready(agentsToRows('x', {}))
  }
});

function expandedRows({ expanded = [], collapsed = [], contents = {} } = {}) {
  return visibleTreeRows(tree(), new Set(collapsed), 0, '', {
    expanded: new Set(expanded),
    contents
  });
}

const under = (rows, parentId) => rows.filter(r => r.parentId === parentId);

test('an expanded workspace yields its sections in FR9 order', () => {
  const rows = expandedRows({ expanded: ['w4'], contents: { w4: contentsFor('w4') } });
  assert.deepEqual(
    under(rows, 'w4').map(r => `${r.kind}:${r.name}`),
    ['section:Notes', 'section:Backlog', 'section:Files', 'memory:Memory', 'section:Agents']
  );
  assert.deepEqual(
    under(rows, 'w4').map(r => r.id),
    ['w4/s/notes', 'w4/s/backlog', 'w4/s/files', 'w4/m', 'w4/s/agents']
  );
  // Sections sit one level under their workspace and know which one it is.
  assert.ok(under(rows, 'w4').every(r => r.depth === 1 && r.workspaceId === 'w4'));
});

test('each section row carries the number of items it holds (FR10)', () => {
  const rows = expandedRows({ expanded: ['w4'], contents: { w4: contentsFor('w4') } });
  const count = id => rows.find(r => r.id === id).count;
  assert.equal(count('w4/s/notes'), 2);
  assert.equal(count('w4/s/backlog'), 2);
  assert.equal(count('w4/s/files'), 2); // counts inside the stems folder
  assert.equal(count('w4/m'), 1);
  assert.equal(count('w4/s/agents'), 1);
});

test('sections start open, so a note is two clicks away: expand, then click', () => {
  const rows = expandedRows({ expanded: ['w4'], contents: { w4: contentsFor('w4') } });
  assert.deepEqual(
    under(rows, 'w4/s/notes').map(r => `${r.kind}:${r.name}`),
    ['note:Arrangement', 'note:Weekly review']
  );
  assert.equal(rows.find(r => r.id === 'w4/s/notes').expanded, true);
});

test('a collapsed section keeps its row and count but shows no items', () => {
  const rows = expandedRows({
    expanded: ['w4'],
    collapsed: ['w4/s/notes'],
    contents: { w4: contentsFor('w4') }
  });
  const notes = rows.find(r => r.id === 'w4/s/notes');
  assert.equal(notes.expanded, false);
  assert.equal(notes.count, 2);
  assert.equal(under(rows, 'w4/s/notes').length, 0);
});

test('Memory is one row that cannot expand', () => {
  const rows = expandedRows({ expanded: ['w4'], contents: { w4: contentsFor('w4') } });
  const memory = rows.find(r => r.id === 'w4/m');
  assert.equal(memory.expandable, false);
  assert.equal(under(rows, 'w4/m').length, 0);
  assert.equal(rowActivation(memory), 'open');
});

test('ticket rows carry their state, and finished ones are marked (FR11)', () => {
  const rows = expandedRows({ expanded: ['w4'], contents: { w4: contentsFor('w4') } });
  const [open, done] = under(rows, 'w4/s/backlog');
  assert.equal(open.meta.stateLabel, 'In progress');
  assert.equal(open.meta.finished, false);
  assert.equal(done.meta.finished, true);
});

test('folders start closed and open to any depth (FR12)', () => {
  const closed = expandedRows({ expanded: ['w4'], contents: { w4: contentsFor('w4') } });
  assert.deepEqual(
    under(closed, 'w4/s/files').map(r => `${r.kind}:${r.name}`),
    ['folder:stems', 'file:BACKLOG.md']
  );
  assert.equal(closed.find(r => r.id === 'w4/d/stems').expanded, false);
  assert.equal(under(closed, 'w4/d/stems').length, 0);

  const open = expandedRows({
    expanded: ['w4', 'w4/d/stems'],
    contents: { w4: contentsFor('w4') }
  });
  assert.deepEqual(
    under(open, 'w4/d/stems').map(r => r.name),
    ['lead.wav']
  );
  assert.equal(open.find(r => r.id === 'w4/f/stems/lead.wav').depth, 3);
});

test('a workspace expanded before its contents arrive shows every section as loading (FR18)', () => {
  const rows = expandedRows({ expanded: ['w4'] });
  assert.deepEqual(
    under(rows, 'w4').map(r => r.name),
    ['Notes', 'Backlog', 'Files', 'Memory', 'Agents']
  );
  assert.deepEqual(
    under(rows, 'w4/s/notes').map(r => `${r.kind}:${r.name}`),
    ['loading:Loading…']
  );
  // An unknown count is not a zero.
  assert.equal(rows.find(r => r.id === 'w4/s/notes').count, null);
});

test('a failed section says so and offers a retry, and the others still show (FR19)', () => {
  const rows = expandedRows({
    expanded: ['w4'],
    contents: { w4: contentsFor('w4', { backlog: failed('Backlog store is locked') }) }
  });
  const [line] = under(rows, 'w4/s/backlog');
  assert.equal(line.kind, 'failed');
  assert.equal(line.name, "Couldn't load Backlog");
  assert.equal(line.section, 'backlog');
  assert.equal(rowActivation(line), 'retry');
  assert.equal(under(rows, 'w4/s/notes').length, 2);
});

test('Memory that failed to load becomes the retry row itself', () => {
  const rows = expandedRows({
    expanded: ['w4'],
    contents: { w4: contentsFor('w4', { memory: failed('nope') }) }
  });
  const line = under(rows, 'w4').find(r => r.section === 'memory');
  assert.equal(line.kind, 'failed');
  assert.equal(line.name, "Couldn't load Memory");
});

test('an empty section in a WORKSPACE shows one dimmed line saying so (FR13)', () => {
  const rows = expandedRows({ expanded: ['w4'], contents: { w4: emptyContents() } });
  assert.deepEqual(
    under(rows, 'w4').map(r => r.name),
    ['Notes', 'Backlog', 'Files', 'Memory', 'Agents']
  );
  assert.deepEqual(
    under(rows, 'w4/s/notes').map(r => `${r.kind}:${r.name}`),
    ['empty:No notes yet']
  );
  assert.equal(under(rows, 'w4/s/backlog')[0].name, 'No tickets yet');
  assert.equal(under(rows, 'w4/s/files')[0].name, 'No files yet');
  assert.equal(under(rows, 'w4/s/agents')[0].name, 'No agents yet');
  assert.equal(rowActivation(under(rows, 'w4/s/notes')[0]), '');
});

test('an expanded group yields its children first, then its own sections (FR16)', () => {
  const rows = expandedRows({ contents: { g1: contentsFor('g1') } });
  assert.deepEqual(
    under(rows, 'g1').map(r => r.id),
    ['w1', 'w2', 'g2', 'g1/s/notes', 'g1/s/backlog', 'g1/s/files', 'g1/m', 'g1/s/agents']
  );
  // posinset/setsize describe the whole level a screen reader will walk.
  const notes = rows.find(r => r.id === 'g1/s/notes');
  assert.equal(notes.posInSet, 4);
  assert.equal(notes.setSize, 8);
});

test("a group's empty sections are hidden, Memory included (FR16)", () => {
  const rows = expandedRows({
    contents: {
      g1: {
        sections: {
          ...emptyContents().sections,
          notes: ready(notesToRows('g1', { notes: [{ id: 'n1', name: 'Group plan' }] }))
        }
      }
    }
  });
  assert.deepEqual(
    under(rows, 'g1').map(r => r.id),
    ['w1', 'w2', 'g2', 'g1/s/notes']
  );
});

test("a group's sections appear only when their data arrives; its children show at once", () => {
  const rows = expandedRows({
    contents: { g1: { sections: { ...contentsFor('g1').sections, backlog: loading() } } }
  });
  const ids = under(rows, 'g1').map(r => r.id);
  assert.deepEqual(ids.slice(0, 3), ['w1', 'w2', 'g2']);
  assert.equal(ids.includes('g1/s/backlog'), false);
  assert.equal(ids.includes('g1/s/notes'), true);
});

test("a group's section that failed still shows, so it can be retried", () => {
  const rows = expandedRows({
    contents: { g1: { sections: { ...emptyContents().sections, files: failed('disk gone') } } }
  });
  assert.deepEqual(
    under(rows, 'g1').map(r => r.id),
    ['w1', 'w2', 'g2', 'g1/s/files']
  );
  assert.equal(under(rows, 'g1/s/files')[0].kind, 'failed');
});

test('a collapsed group shows neither its children nor its sections', () => {
  const rows = expandedRows({ collapsed: ['g1'], contents: { g1: contentsFor('g1') } });
  assert.deepEqual(
    rows.map(r => r.id),
    ['g1', 'w4']
  );
});

test("the next-sibling of a group's last child is never a section row", () => {
  // A drop "after" the last workspace must append, not insert before a section.
  const rows = expandedRows({ contents: { g1: contentsFor('g1') } });
  assert.equal(rows.find(r => r.id === 'g2').nextSiblingId, '');
  assert.equal(rows.find(r => r.id === 'w1').nextSiblingId, 'w2');
});

test('the 100-row cap arrives as an "Open workspace" row that visits the workspace (FR14)', () => {
  const notes = Array.from({ length: 101 }, (_, i) => ({ id: `n${i}`, name: `Note ${i}` }));
  const shaped = notesToRows('w4', { notes });
  const capped = {
    ...shaped,
    rows: [
      ...shaped.rows.slice(0, 100),
      {
        id: 'w4/more/notes',
        kind: 'more',
        label: 'Open workspace to see all 101',
        meta: { section: 'notes', total: 101 },
        children: [],
        workspaceId: 'w4'
      }
    ]
  };
  const rows = expandedRows({
    expanded: ['w4'],
    contents: { w4: contentsFor('w4', { notes: ready(capped) }) }
  });
  const items = under(rows, 'w4/s/notes');
  assert.equal(items.length, 101);
  assert.equal(items[100].name, 'Open workspace to see all 101');
  assert.equal(rowActivation(items[100]), 'visit');
  assert.equal(rows.find(r => r.id === 'w4/s/notes').count, 101);
});

test('isRowExpanded: groups and sections start open, workspaces and folders closed', () => {
  const none = new Set();
  assert.equal(isRowExpanded('group', 'g', none, none), true);
  assert.equal(isRowExpanded('section', 's', none, none), true);
  assert.equal(isRowExpanded('workspace', 'w', none, none), false);
  assert.equal(isRowExpanded('folder', 'd', none, none), false);
});

test('setRowExpanded records only where a row differs from its default', () => {
  const state = { collapsedGroups: new Set(), expandedRows: new Set() };
  setRowExpanded(state, 'workspace', 'w1', true);
  setRowExpanded(state, 'group', 'g1', false);
  setRowExpanded(state, 'section', 'w1/s/notes', false);
  setRowExpanded(state, 'folder', 'w1/d/docs', true);
  assert.deepEqual([...state.expandedRows].sort(), ['w1', 'w1/d/docs']);
  assert.deepEqual([...state.collapsedGroups].sort(), ['g1', 'w1/s/notes']);
  // Back to the defaults: both sets are empty again.
  setRowExpanded(state, 'workspace', 'w1', false);
  setRowExpanded(state, 'group', 'g1', true);
  setRowExpanded(state, 'section', 'w1/s/notes', true);
  setRowExpanded(state, 'folder', 'w1/d/docs', false);
  assert.equal(state.expandedRows.size + state.collapsedGroups.size, 0);
});

test('revealTargets lists what must be open for a row to be on screen (FR28)', () => {
  // A note in DB (g1 > g2 > w3): both groups, the workspace, then Notes.
  assert.deepEqual(revealTargets('w3/n/n1', flat()), [
    { kind: 'group', id: 'g1' },
    { kind: 'group', id: 'g2' },
    { kind: 'workspace', id: 'w3' },
    { kind: 'section', id: 'w3/s/notes' }
  ]);
  // A file two folders down also needs each folder above it.
  assert.deepEqual(revealTargets('w4/f/stems/takes/lead.wav', flat()), [
    { kind: 'workspace', id: 'w4' },
    { kind: 'section', id: 'w4/s/files' },
    { kind: 'folder', id: 'w4/d/stems' },
    { kind: 'folder', id: 'w4/d/stems/takes' }
  ]);
  // Memory is not inside a section; a ticket is inside Backlog.
  assert.deepEqual(revealTargets('w4/m', flat()), [{ kind: 'workspace', id: 'w4' }]);
  assert.deepEqual(revealTargets('w4/t/t1', flat()).pop(), { kind: 'section', id: 'w4/s/backlog' });
});

test('revealTargets never expands the row itself, and a group owner is a group', () => {
  // A workspace or group tab only needs its ancestors open.
  assert.deepEqual(revealTargets('w3', flat()), [
    { kind: 'group', id: 'g1' },
    { kind: 'group', id: 'g2' }
  ]);
  assert.deepEqual(revealTargets('w4', flat()), []);
  // A note that lives in a group: the group is the owner to open.
  assert.deepEqual(revealTargets('g2/n/n1', flat()), [
    { kind: 'group', id: 'g1' },
    { kind: 'group', id: 'g2' },
    { kind: 'section', id: 'g2/s/notes' }
  ]);
  // A section row needs its workspace open, not itself.
  assert.deepEqual(revealTargets('w4/s/notes', flat()), [{ kind: 'workspace', id: 'w4' }]);
  assert.deepEqual(revealTargets('', flat()), []);
  assert.deepEqual(revealTargets('w4/zz/unknown', flat()), []);
});

test('applying revealTargets makes the row appear among the visible rows', () => {
  const state = { collapsedGroups: new Set(['g1', 'g2', 'w3/s/notes']), expandedRows: new Set() };
  const contents = { w3: contentsFor('w3') };
  const rowsBefore = visibleTreeRows(tree(), state.collapsedGroups, 0, '', {
    expanded: state.expandedRows,
    contents
  });
  assert.equal(
    rowsBefore.some(r => r.id === 'w3/n/n1'),
    false
  );
  revealTargets('w3/n/n1', flat()).forEach(target =>
    setRowExpanded(state, target.kind, target.id, true)
  );
  const rowsAfter = visibleTreeRows(tree(), state.collapsedGroups, 0, '', {
    expanded: state.expandedRows,
    contents
  });
  assert.equal(
    rowsAfter.some(r => r.id === 'w3/n/n1'),
    true
  );
});

test('rowActivation: names open, sections and folders only toggle (FR22-FR24)', () => {
  assert.equal(rowActivation({ kind: 'workspace' }), 'open');
  assert.equal(rowActivation({ kind: 'group' }), 'open');
  assert.equal(rowActivation({ kind: 'section' }), 'toggle');
  assert.equal(rowActivation({ kind: 'folder' }), 'toggle');
  ['note', 'ticket', 'file', 'agent', 'memory'].forEach(kind =>
    assert.equal(rowActivation({ kind }), 'open', kind)
  );
  assert.equal(rowActivation({ kind: 'loading' }), '');
  assert.equal(rowActivation(null), '');
});

test('the highlighted row is the open tab, falling back to the shared selection', () => {
  assert.equal(treeActiveRowId({ activeTabKey: 'w1/n/n1', selectedId: 'w1' }), 'w1/n/n1');
  assert.equal(treeActiveRowId({ activeTabKey: '', selectedId: 'w1' }), 'w1');
  assert.equal(treeActiveRowId({}), '');
});

test('descendantIds and ancestorIds walk the whole chain', () => {
  assert.deepEqual(descendantIds(flat(), 'g1').sort(), ['g2', 'w1', 'w2', 'w3']);
  assert.deepEqual(descendantIds(flat(), 'g2'), ['w3']);
  assert.deepEqual(descendantIds(flat(), 'w4'), []);
  assert.deepEqual(ancestorIds(flat(), 'w3'), ['g1', 'g2']);
  assert.deepEqual(ancestorIds(flat(), 'w4'), []);
});

// ---------------------------------------------------------------------------
// Move validation (FR49, FR50, FR51)
// ---------------------------------------------------------------------------

test('a group cannot be moved into itself, with an actionable reason (FR50)', () => {
  const reason = moveRejectionReason(flat(), 'g1', 'g1');
  assert.match(reason, /cannot be moved into itself/);
  assert.match(reason, /Platform/);
  assert.equal(isMoveAllowed(flat(), 'g1', 'g1'), false);
});

test('a group cannot be moved into its own descendant (FR50)', () => {
  const reason = moveRejectionReason(flat(), 'g1', 'g2');
  assert.match(reason, /which is inside it/);
  assert.match(reason, /Infra/);
  assert.equal(isMoveAllowed(flat(), 'g1', 'g2'), false);
});

test('nothing can be moved INTO a plain workspace', () => {
  const reason = moveRejectionReason(flat(), 'w4', 'w1');
  assert.match(reason, /is a workspace, not a group/);
});

test('legal moves are allowed: into a group, and back to the top level', () => {
  assert.equal(isMoveAllowed(flat(), 'w4', 'g1'), true);
  assert.equal(isMoveAllowed(flat(), 'w4', 'g2'), true);
  assert.equal(isMoveAllowed(flat(), 'w1', ''), true);
  // A descendant CAN move up into an ancestor's ancestor.
  assert.equal(isMoveAllowed(flat(), 'w3', 'g1'), true);
});

test('moveDestinations offers a keyboard equivalent for every drag target (FR51)', () => {
  const dests = moveDestinations(flat(), 'w3').map(d => d.id);
  // w3 lives in g2, so Top level and g1 are offered but g2 (current) is not.
  assert.ok(dests.includes(''));
  assert.ok(dests.includes('g1'));
  assert.ok(!dests.includes('g2'));
});

test('moveDestinations never offers an illegal destination', () => {
  const dests = moveDestinations(flat(), 'g1').map(d => d.id);
  assert.ok(!dests.includes('g1'));
  assert.ok(!dests.includes('g2'));
});

test('a top-level item is not offered "Top level" as a destination', () => {
  assert.ok(!moveDestinations(flat(), 'w4').some(d => d.id === ''));
});

test('moveOrderUpdates renumbers the destination and reparents only the moved item', () => {
  const updates = moveOrderUpdates(flat(), 'w4', 'g1');
  // Existing children keep their order; the moved item lands last and is the
  // only one carrying parent_id.
  assert.deepEqual(Object.keys(updates), ['w1', 'w2', 'g2', 'w4']);
  assert.equal(updates.w4.parent_id, 'g1');
  assert.equal(updates.w4.order_index, 4);
  assert.equal(updates.w1.parent_id, undefined);
  assert.deepEqual(
    Object.values(updates).map(u => u.order_index),
    [1, 2, 3, 4]
  );
});

test('moveOrderUpdates can insert before a named sibling', () => {
  const updates = moveOrderUpdates(flat(), 'w4', 'g1', 'w2');
  assert.equal(updates.w4.order_index, 2);
  assert.equal(updates.w2.order_index, 3);
});

test('moveOrderUpdates to the top level reparents to empty string', () => {
  const updates = moveOrderUpdates(flat(), 'w1', '');
  assert.equal(updates.w1.parent_id, '');
  assert.ok(Object.keys(updates).includes('g1'));
});

// ---------------------------------------------------------------------------
// Bulk selection (FR46, FR47)
// ---------------------------------------------------------------------------

test('a group with every descendant checked is checked, not indeterminate', () => {
  const selected = new Set(['w1', 'w2', 'g2', 'w3']);
  const state = bulkSelectionState(flat(), selected);
  assert.deepEqual(state.g1, { checked: true, indeterminate: false });
});

test('a group with only some descendants checked is indeterminate', () => {
  const state = bulkSelectionState(flat(), new Set(['w1']));
  assert.deepEqual(state.g1, { checked: false, indeterminate: true });
  assert.deepEqual(state.w1, { checked: true, indeterminate: false });
  assert.deepEqual(state.w2, { checked: false, indeterminate: false });
});

test('a group with nothing checked is neither checked nor indeterminate', () => {
  const state = bulkSelectionState(flat(), new Set());
  assert.deepEqual(state.g1, { checked: false, indeterminate: false });
});

test('indeterminate rolls up through nesting levels', () => {
  const state = bulkSelectionState(flat(), new Set(['w3']));
  assert.deepEqual(state.g2, { checked: true, indeterminate: false });
  assert.deepEqual(state.g1, { checked: false, indeterminate: true });
});

// ---------------------------------------------------------------------------
// The workspace row's one trailing marker (FR8)
// ---------------------------------------------------------------------------

test('a workspace needing attention shows the count, and nothing else', () => {
  const marker = workspaceMarker({ needs_attention_count: 2, active: true, open_task_count: 5 });
  assert.equal(marker.attention, 2);
  assert.equal(marker.dot, false);
  assert.equal(marker.statusLabel, 'Needs attention');
});

test('a running or active workspace shows the dot; an idle one shows nothing', () => {
  const running = workspaceMarker({ needs_attention_count: 0, active: true });
  assert.deepEqual([running.attention, running.dot, running.statusLabel], [0, true, 'Running']);
  const busy = workspaceMarker({ needs_attention_count: 0, active: false, open_task_count: 3 });
  assert.deepEqual([busy.dot, busy.statusLabel], [true, 'Active']);
  const idle = workspaceMarker({ needs_attention_count: 0, active: false, open_task_count: 0 });
  assert.deepEqual([idle.attention, idle.dot, idle.statusLabel], [0, false, 'Idle']);
});

test('a workspace that reported nothing is not shown as running or as needing attention', () => {
  const blind = workspaceMarker({ id: 'x' });
  assert.deepEqual([blind.attention, blind.dot], [0, false]);
  assert.equal(blind.statusLabel, 'Status unavailable');
});

test('Personal HQ carries the HQ label; a group carries no status marker', () => {
  assert.equal(workspaceMarker({ is_personal_hq: true }).hq, true);
  assert.equal(workspaceMarker({ designation: 'personal_hq' }).hq, true);
  assert.equal(workspaceMarker({ id: 'plain' }).hq, false);
  assert.deepEqual(workspaceMarker({ kind: 'group', needs_attention_count: 9 }), {
    hq: false,
    attention: 0,
    dot: false,
    statusLabel: 'Group'
  });
});

// ---------------------------------------------------------------------------
// Rendering + ARIA (FR7, FR41, FR73)
// ---------------------------------------------------------------------------

function html(collapsed = new Set(), extra = {}) {
  return renderTreeHTML(visibleTreeRows(tree(), collapsed), {
    activeId: '',
    tabbableId: 'g1',
    bulkState: bulkSelectionState(flat(), new Set()),
    ...extra
  });
}

// The markup of one row, from its opening tag to the end of its own <div>.
function rowMarkup(out, id) {
  const at = out.indexOf(`data-tree-row="${id}"`);
  assert.ok(at >= 0, `row ${id} is rendered`);
  return out.slice(out.lastIndexOf('<div class="cockpit-tree-row', at), out.indexOf('</div>', at));
}

function contentHTML(options, extra = {}) {
  return renderTreeHTML(expandedRows(options), { activeId: '', tabbableId: 'g1', ...extra });
}

test('the tree uses real tree/treeitem/group roles and level semantics', () => {
  const out = html();
  assert.match(out, /role="tree"/);
  assert.match(out, /role="treeitem"/);
  assert.match(out, /role="group"/);
  assert.match(out, /aria-level="1"/);
  assert.match(out, /aria-level="3"/); // DB, nested two deep
  assert.match(out, /aria-multiselectable="true"/);
});

test('every row that can expand carries aria-expanded; a leaf row does not', () => {
  const out = contentHTML({ expanded: ['w4'], contents: { w4: contentsFor('w4') } });
  assert.match(out, /data-tree-row="g1"[^>]*aria-expanded="true"/);
  // A workspace is expandable now: closed until the user opens it.
  assert.match(out, /data-tree-row="w1"[^>]*aria-expanded="false"/);
  assert.match(out, /data-tree-row="w4"[^>]*aria-expanded="true"/);
  assert.match(out, /data-tree-row="w4\/s\/notes"[^>]*aria-expanded="true"/);
  assert.match(out, /data-tree-row="w4\/d\/stems"[^>]*aria-expanded="false"/);
  assert.doesNotMatch(rowMarkup(out, 'w4/n/n1'), /aria-expanded/);
  assert.doesNotMatch(rowMarkup(out, 'w4/m'), /aria-expanded/);
});

test('a collapsed group reports aria-expanded=false', () => {
  assert.match(html(new Set(['g1'])), /data-tree-row="g1"[^>]*aria-expanded="false"/);
});

test('exactly one row is tabbable across every kind of row (roving tabindex, FR127)', () => {
  const out = contentHTML({ expanded: ['w4'], contents: { w4: contentsFor('w4') } });
  assert.equal((out.match(/role="treeitem"[^>]*tabindex="0"/g) || []).length, 1);
  // Carets and Retry buttons inside a row never join the tab order.
  assert.doesNotMatch(out, /<button[^>]*tabindex="0"/);
});

test('the open item is aria-selected and carries the active class', () => {
  const out = html(new Set(), { activeId: 'w1' });
  assert.match(out, /data-tree-row="w1"[^>]*aria-selected="true"/);
  assert.match(out, /class="cockpit-tree-row is-kind-workspace is-active"/);
  assert.equal((out.match(/aria-selected="true"/g) || []).length, 1);
});

test('a row is slim: no checkbox, no per-row Move or Delete, no metrics (FR7)', () => {
  const out = html();
  assert.doesNotMatch(out, /type="checkbox"/);
  assert.doesNotMatch(out, /data-tree-move=|data-tree-delete=/);
  assert.doesNotMatch(out, /cockpit-tree-metric|No schedule/);
});

test('a row picked for a bulk action is marked apart from the open item (FR60)', () => {
  const out = html(new Set(), {
    activeId: 'w2',
    bulkState: bulkSelectionState(flat(), new Set(['w1']))
  });
  assert.match(rowMarkup(out, 'w1'), /is-kind-workspace is-picked"/);
  assert.match(rowMarkup(out, 'w2'), /is-kind-workspace is-active"/);
  assert.doesNotMatch(rowMarkup(out, 'w2'), /is-picked/);
});

test('a workspace row shows its attention count and names its status in words (FR8)', () => {
  const w1 = rowMarkup(html(), 'w1'); // needs_attention_count: 1
  assert.match(w1, /class="cockpit-tree-badge">1</);
  assert.match(w1, /class="visually-hidden">Needs attention</);
  assert.doesNotMatch(w1, /cockpit-tree-dot/);
  // w2 reports zero attention and no open tasks: no marker at all.
  const w2 = rowMarkup(html(), 'w2');
  assert.doesNotMatch(w2, /cockpit-tree-badge|cockpit-tree-dot/);
  assert.match(w2, /class="visually-hidden">Idle</);
});

test('Personal HQ shows the HQ label, and a running workspace shows the dot', () => {
  const out = renderTreeHTML(
    visibleTreeRows(
      [
        { id: 'hq', name: 'My HQ', is_personal_hq: true, needs_attention_count: 0, active: true },
        { id: 'plain', name: 'Plain', needs_attention_count: 0, active: false, open_task_count: 0 }
      ],
      new Set()
    ),
    { tabbableId: 'hq' }
  );
  assert.match(rowMarkup(out, 'hq'), /class="cockpit-tree-count">HQ</);
  assert.match(rowMarkup(out, 'hq'), /cockpit-tree-dot/);
  assert.match(rowMarkup(out, 'hq'), /class="visually-hidden">Running</);
  assert.doesNotMatch(rowMarkup(out, 'plain'), /cockpit-tree-dot|>HQ</);
});

test('section rows show their count; ticket rows their state; finished tickets are dimmed', () => {
  const out = contentHTML({ expanded: ['w4'], contents: { w4: contentsFor('w4') } });
  assert.match(rowMarkup(out, 'w4/s/notes'), /class="cockpit-tree-count">2</);
  assert.match(rowMarkup(out, 'w4/m'), /class="cockpit-tree-count">1</);
  assert.match(rowMarkup(out, 'w4/t/t1'), /class="cockpit-tree-count">In progress</);
  assert.doesNotMatch(rowMarkup(out, 'w4/t/t1'), /is-dim/);
  assert.match(rowMarkup(out, 'w4/t/t2'), /is-kind-ticket is-dim/);
  assert.match(rowMarkup(out, 'w4/t/t2'), /class="cockpit-tree-count">Done</);
});

test('a loading section shows no count rather than a zero', () => {
  const out = contentHTML({ expanded: ['w4'] });
  assert.doesNotMatch(rowMarkup(out, 'w4/s/notes'), /cockpit-tree-count/);
  assert.match(out, /is-kind-loading[^>]*>.*?Loading…/s);
});

test('a failed section renders a Retry button that names its workspace and section', () => {
  const out = contentHTML({
    expanded: ['w4'],
    contents: { w4: contentsFor('w4', { files: failed('disk gone') }) }
  });
  assert.match(out, /Couldn&#39;t load Files/);
  assert.match(out, /data-tree-retry="w4" data-tree-retry-section="files"[^>]*>Retry</);
});

test('only workspace and group rows are draggable (FR62)', () => {
  const out = contentHTML({ expanded: ['w4'], contents: { w4: contentsFor('w4') } });
  assert.match(out, /data-tree-row="w4"[^>]*draggable="true"/);
  assert.match(out, /data-tree-row="g1"[^>]*draggable="true"/);
  ['w4/s/notes', 'w4/n/n1', 'w4/t/t1', 'w4/d/stems', 'w4/f/BACKLOG.md', 'w4/m'].forEach(id =>
    assert.doesNotMatch(rowMarkup(out, id), /draggable/, id)
  );
});

test('an empty expanded group offers a drop target rather than looking broken', () => {
  const out = renderTreeHTML(
    visibleTreeRows([{ id: 'g', name: 'G', kind: 'group', children: [] }], new Set()),
    { activeId: '', tabbableId: 'g', bulkState: {} }
  );
  assert.match(out, /data-tree-drop-into="g"/);
});

test('an empty group with notes of its own shows the drop target and then its sections', () => {
  const out = renderTreeHTML(
    visibleTreeRows([{ id: 'g', name: 'G', kind: 'group', children: [] }], new Set(), 0, '', {
      contents: { g: contentsFor('g') }
    }),
    { tabbableId: 'g' }
  );
  assert.ok(out.indexOf('data-tree-drop-into="g"') < out.indexOf('data-tree-row="g/s/notes"'));
  // One nested list holds both, so the group still has a single child group.
  assert.equal((out.match(/class="cockpit-tree-children"/g) || []).length >= 1, true);
});

test('an empty tree says so instead of rendering an empty list', () => {
  assert.match(renderTreeHTML([], {}), /No workspaces yet/);
});

test('the tree escapes hostile workspace names', () => {
  const out = renderTreeHTML(
    visibleTreeRows([{ id: '<x>', name: '<img src=x onerror=y>' }], new Set()),
    { activeId: '', tabbableId: '<x>', bulkState: {}, tagsById: {} }
  );
  assert.doesNotMatch(out, /<img/i);
  assert.match(out, /&lt;img/);
});

test('renderMoveDialogHTML lists destinations and explains when there are none', () => {
  assert.match(renderMoveDialogHTML('API', moveDestinations(flat(), 'w1')), /data-tree-move-to="/);
  assert.match(renderMoveDialogHTML('Solo', []), /nowhere to move/i);
  assert.match(renderMoveDialogHTML('Solo', []), /Create a group first/);
});

// ---------------------------------------------------------------------------
// Keyboard model (FR127)
// ---------------------------------------------------------------------------

const rows = () => visibleTreeRows(tree(), new Set());

test('ArrowDown/ArrowUp walk visible rows across nesting levels', () => {
  assert.deepEqual(resolveTreeKey('ArrowDown', 'g1', rows()), { focusId: 'w1' });
  assert.deepEqual(resolveTreeKey('ArrowDown', 'g2', rows()), { focusId: 'w3' });
  assert.deepEqual(resolveTreeKey('ArrowDown', 'w3', rows()), { focusId: 'w4' });
  assert.deepEqual(resolveTreeKey('ArrowUp', 'w4', rows()), { focusId: 'w3' });
});

test('ArrowDown at the last row and ArrowUp at the first do nothing', () => {
  assert.equal(resolveTreeKey('ArrowDown', 'w4', rows()), null);
  assert.equal(resolveTreeKey('ArrowUp', 'g1', rows()), null);
});

test('Home and End jump to the first and last visible rows', () => {
  assert.deepEqual(resolveTreeKey('Home', 'w3', rows()), { focusId: 'g1' });
  assert.deepEqual(resolveTreeKey('End', 'g1', rows()), { focusId: 'w4' });
});

test('ArrowRight expands a collapsed group, then descends into it', () => {
  const collapsed = visibleTreeRows(tree(), new Set(['g1']));
  assert.deepEqual(resolveTreeKey('ArrowRight', 'g1', collapsed), { toggle: 'g1', expand: true });
  assert.deepEqual(resolveTreeKey('ArrowRight', 'g1', rows()), { focusId: 'w1' });
});

test('ArrowLeft collapses an expanded group, then climbs to the parent', () => {
  assert.deepEqual(resolveTreeKey('ArrowLeft', 'g1', rows()), { toggle: 'g1', expand: false });
  assert.deepEqual(resolveTreeKey('ArrowLeft', 'w1', rows()), { focusId: 'g1' });
  // A top-level workspace has nowhere to climb.
  assert.equal(resolveTreeKey('ArrowLeft', 'w4', rows()), null);
});

test('ArrowRight opens a workspace, then steps into its sections; ArrowLeft closes it', () => {
  assert.deepEqual(resolveTreeKey('ArrowRight', 'w4', rows()), { toggle: 'w4', expand: true });
  const open = expandedRows({ expanded: ['w4'], contents: { w4: contentsFor('w4') } });
  assert.deepEqual(resolveTreeKey('ArrowRight', 'w4', open), { focusId: 'w4/s/notes' });
  assert.deepEqual(resolveTreeKey('ArrowLeft', 'w4', open), { toggle: 'w4', expand: false });
});

test('arrow keys walk content rows, and never open anything (FR71, FR72)', () => {
  const open = expandedRows({ expanded: ['w4'], contents: { w4: contentsFor('w4') } });
  // Down from the Notes section lands on its first note; a leaf has no Right.
  assert.deepEqual(resolveTreeKey('ArrowDown', 'w4/s/notes', open), { focusId: 'w4/n/n1' });
  assert.equal(resolveTreeKey('ArrowRight', 'w4/n/n1', open), null);
  // Left on a note climbs to its section; Left on an open section closes it.
  assert.deepEqual(resolveTreeKey('ArrowLeft', 'w4/n/n1', open), { focusId: 'w4/s/notes' });
  assert.deepEqual(resolveTreeKey('ArrowLeft', 'w4/s/notes', open), {
    toggle: 'w4/s/notes',
    expand: false
  });
  // A closed folder opens with Right; Memory cannot expand, so Right is ignored.
  assert.deepEqual(resolveTreeKey('ArrowRight', 'w4/d/stems', open), {
    toggle: 'w4/d/stems',
    expand: true
  });
  assert.equal(resolveTreeKey('ArrowRight', 'w4/m', open), null);
  // End reaches the very last visible row, whatever kind it is.
  assert.deepEqual(resolveTreeKey('End', 'g1', open), { focusId: 'w4/a/Scout' });
  // Every result is a focus move or a toggle — there is no "open" outcome.
  ['ArrowDown', 'ArrowUp', 'ArrowLeft', 'ArrowRight', 'Home', 'End'].forEach(key =>
    open.forEach(row => {
      const result = resolveTreeKey(key, row.id, open);
      if (result) assert.ok('focusId' in result || 'toggle' in result);
    })
  );
});

test('resolveTreeKey still reads isGroup from callers that predate expandable rows', () => {
  const legacy = [
    { id: 'g', isGroup: true, expanded: false, depth: 0, parentId: '' },
    { id: 'w', isGroup: false, expanded: null, depth: 0, parentId: '' }
  ];
  assert.deepEqual(resolveTreeKey('ArrowRight', 'g', legacy), { toggle: 'g', expand: true });
  assert.equal(resolveTreeKey('ArrowRight', 'w', legacy), null);
});

test('unknown keys and unknown rows are ignored rather than throwing', () => {
  assert.equal(resolveTreeKey('x', 'g1', rows()), null);
  assert.equal(resolveTreeKey('ArrowDown', 'nope', rows()), null);
  assert.equal(resolveTreeKey('ArrowDown', 'g1', []), null);
});

// ---------------------------------------------------------------------------
// Positional drop + tag filtering (FR49, FR54)
// ---------------------------------------------------------------------------

test('rows expose their next sibling so an "after" drop knows where to insert', () => {
  const out = html();
  assert.match(out, /data-tree-row="w1"[^>]*data-next-sibling-id="w2"/);
  // The last child of a group has no next sibling.
  assert.match(out, /data-tree-row="g2"[^>]*data-next-sibling-id=""/);
});

test('rows carry no tag chips of their own: tags are filtered from the bar (FR63)', () => {
  // A workspace's tags are shown and removed in its overview tab; the slim row
  // has room for one marker only.
  const out = html(new Set(), { activeTags: new Set(['alpha']) });
  assert.doesNotMatch(out, /data-tree-tag-filter|data-tree-tag-remove/);
});

test('an active tag chip is marked with aria-pressed, not colour alone', () => {
  const bar = renderTagFilterBarHTML({
    metadata: { tagsById: { w1: ['alpha', 'beta'] } },
    activeTags: new Set(['alpha'])
  });
  assert.match(bar, /data-tree-tag-filter="alpha"[^>]*aria-pressed="true"/);
  assert.match(bar, /data-tree-tag-filter="beta"[^>]*aria-pressed="false"/);
});

test('renderTagFilterBarHTML lists every tag once and offers a clear action', () => {
  const bar = renderTagFilterBarHTML({
    metadata: { tagsById: { w1: ['beta', 'alpha'], w2: ['alpha'] } },
    activeTags: new Set(['alpha'])
  });
  assert.match(bar, /data-tree-tag-filter="alpha"/);
  assert.match(bar, /data-tree-tag-filter="beta"/);
  assert.equal((bar.match(/data-tree-tag-filter="alpha"/g) || []).length, 1);
  assert.match(bar, /data-tree-tag-clear/);
});

test('the tag filter bar hides itself when no workspace has tags', () => {
  const bar = renderTagFilterBarHTML({ metadata: { tagsById: {} }, activeTags: new Set() });
  assert.match(bar, /hidden/);
});

test('the tag bar offers no Clear action when nothing is filtered', () => {
  const bar = renderTagFilterBarHTML({
    metadata: { tagsById: { w1: ['alpha'] } },
    activeTags: new Set()
  });
  assert.doesNotMatch(bar, /data-tree-tag-clear/);
});

test('filterTreeByTags keeps a group whose descendant matches, so the path survives', () => {
  const filtered = filterTreeByTags(tree(), { w3: ['db'] }, new Set(['db']));
  assert.deepEqual(
    filtered.map(n => n.id),
    ['g1']
  );
  assert.deepEqual(
    filtered[0].children.map(n => n.id),
    ['g2']
  );
  assert.deepEqual(
    filtered[0].children[0].children.map(n => n.id),
    ['w3']
  );
});

test('filterTreeByTags with no active tags is a pass-through', () => {
  assert.equal(filterTreeByTags(tree(), {}, new Set()).length, 2);
});

test('a tag filter that matches nothing says so, rather than showing an empty tree', () => {
  const out = renderTreeHTML([], { activeTags: new Set(['nope']) });
  assert.match(out, /No workspaces match the selected tags/);
  // With no filter the copy is different, so the two states stay legible.
  assert.match(renderTreeHTML([], { activeTags: new Set() }), /No workspaces yet/);
});

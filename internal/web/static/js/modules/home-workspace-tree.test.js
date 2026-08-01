// Tests for home-workspace-tree.js — the Tree peer view's hierarchy, move
// validation, bulk selection, keyboard model, and rendering.
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
  rowMetaParts,
  renderTreeHTML,
  renderMoveDialogHTML,
  resolveTreeKey,
  renderTagFilterBarHTML,
  filterTreeByTags
} from './home-workspace-tree.js';

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
// Row scan data (FR43, FR44)
// ---------------------------------------------------------------------------

test('rowMetaParts shows real metrics and an em dash for missing ones (FR44)', () => {
  const parts = rowMetaParts({ agent_count: 2, open_task_count: 3, needs_attention_count: 0 });
  assert.deepEqual(
    parts.map(p => p.value),
    ['2', '3', '0']
  );
  const blind = rowMetaParts({ id: 'x' });
  assert.deepEqual(
    blind.map(p => p.value),
    ['—', '—', '—']
  );
});

test('a group row carries no per-workspace metrics', () => {
  assert.deepEqual(rowMetaParts({ kind: 'group' }), []);
});

// ---------------------------------------------------------------------------
// Rendering + ARIA (FR41, FR43, FR46)
// ---------------------------------------------------------------------------

function html(collapsed = new Set(), extra = {}) {
  return renderTreeHTML(visibleTreeRows(tree(), collapsed), {
    activeId: '',
    tabbableId: 'g1',
    bulkState: bulkSelectionState(flat(), new Set()),
    tagsById: {},
    ...extra
  });
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

test('group rows carry aria-expanded and workspace rows do not', () => {
  const out = html();
  assert.match(out, /data-tree-row="g1"[^>]*aria-expanded="true"/);
  const w4Row = out.slice(out.indexOf('data-tree-row="w4"'));
  assert.doesNotMatch(w4Row.slice(0, 260), /aria-expanded/);
});

test('a collapsed group reports aria-expanded=false', () => {
  assert.match(html(new Set(['g1'])), /data-tree-row="g1"[^>]*aria-expanded="false"/);
});

test('exactly one row is tabbable (roving tabindex, FR127)', () => {
  const out = html();
  assert.equal((out.match(/tabindex="0"/g) || []).length, 1);
});

test('active selection is aria-selected and visually distinct from the checkbox', () => {
  const out = html(new Set(), { activeId: 'w1' });
  assert.match(out, /data-tree-row="w1"[^>]*aria-selected="true"/);
  assert.match(out, /class="cockpit-tree-row is-active"/);
  // The bulk checkbox is a separate control with its own label.
  assert.match(out, /data-tree-check="w1"/);
  assert.match(out, /aria-label="Select API for bulk actions"/);
});

test('every row offers Move and Delete, so drag is never the only path (FR51)', () => {
  const out = html();
  assert.match(out, /data-tree-move="w1"/);
  assert.match(out, /data-tree-delete="w1"/);
});

test('rows show status, metrics, and an honest no-schedule state (FR43/FR44)', () => {
  const out = html();
  assert.match(out, /Needs attention/); // w1 has attention
  assert.match(out, /cockpit-tree-metric/);
  assert.match(out, /No schedule/);
});

test('an empty expanded group offers a drop target rather than looking broken', () => {
  const out = renderTreeHTML(
    visibleTreeRows([{ id: 'g', name: 'G', kind: 'group', children: [] }], new Set()),
    { activeId: '', tabbableId: 'g', bulkState: {}, tagsById: {} }
  );
  assert.match(out, /data-tree-drop-into="g"/);
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

test('ArrowRight on a plain workspace does nothing', () => {
  assert.equal(resolveTreeKey('ArrowRight', 'w1', rows()), null);
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

test('tag chips are both filterable and removable (FR54)', () => {
  const out = html(new Set(), { tagsById: { w1: ['alpha'] } });
  assert.match(out, /data-tree-tag-filter="alpha"/);
  assert.match(out, /data-tree-tag-remove="alpha"/);
  assert.match(out, /data-tree-tag-workspace="w1"/);
});

test('an active tag chip is marked with aria-pressed, not colour alone', () => {
  const out = html(new Set(), {
    tagsById: { w1: ['alpha'] },
    activeTags: new Set(['alpha'])
  });
  assert.match(out, /data-tree-tag-filter="alpha"[^>]*aria-pressed="true"/);
  assert.match(out, /class="cockpit-tree-tag is-active"/);
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

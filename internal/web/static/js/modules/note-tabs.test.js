// Tests for note-tabs.js — pure reducer for the page's multi-tab + split-pane
// workflow. Every action is deterministic and free of DOM, so we can drive
// state transitions through node --test with no mocking.

import { test } from 'node:test';
import assert from 'node:assert/strict';

const {
  MAX_PANES,
  initialState,
  openTab,
  setActiveTab,
  closeTab,
  splitRight,
  unsplit,
  swapPanes,
  focusPane,
  reorder,
  moveTab,
  hydrate,
  allOpenNoteIds,
  activeNoteIdFor,
} = await import('./note-tabs.js');

test('initialState: with no note', () => {
  const s = initialState();
  assert.equal(s.panes.length, 1);
  assert.deepEqual(s.panes[0].tabs, []);
  assert.equal(s.panes[0].activeId, null);
  assert.equal(s.splitMode, 'none');
  assert.equal(s.focusedPaneIndex, 0);
});

test('initialState: with starting note', () => {
  const s = initialState('note-1');
  assert.deepEqual(s.panes[0].tabs, ['note-1']);
  assert.equal(s.panes[0].activeId, 'note-1');
});

test('openTab: appends a new tab and makes it active', () => {
  const s = initialState('a');
  const s2 = openTab(s, 'b');
  assert.deepEqual(s2.panes[0].tabs, ['a', 'b']);
  assert.equal(s2.panes[0].activeId, 'b');
});

test('openTab: existing tab just switches active', () => {
  let s = initialState('a');
  s = openTab(s, 'b');
  s = openTab(s, 'c');
  s = openTab(s, 'a'); // already in the pane
  assert.deepEqual(s.panes[0].tabs, ['a', 'b', 'c']);
  assert.equal(s.panes[0].activeId, 'a');
});

test('openTab: pure — does not mutate input', () => {
  const s = initialState('a');
  const before = JSON.stringify(s);
  openTab(s, 'b');
  assert.equal(JSON.stringify(s), before);
});

test('openTab: empty noteId is a no-op', () => {
  const s = initialState('a');
  const s2 = openTab(s, '');
  assert.equal(s, s2); // reference equality
});

test('openTab: falls back to focused pane when paneIndex is out of range', () => {
  const s = initialState('a');
  const s2 = openTab(s, 'b', 5);
  assert.equal(s2.panes[0].activeId, 'b');
});

test('setActiveTab: switches when noteId is in the pane', () => {
  let s = initialState('a');
  s = openTab(s, 'b');
  s = setActiveTab(s, 0, 'a');
  assert.equal(s.panes[0].activeId, 'a');
});

test('setActiveTab: unknown noteId is a no-op', () => {
  const s = initialState('a');
  const s2 = setActiveTab(s, 0, 'zzz');
  assert.equal(s, s2);
});

test('closeTab: removes from pane and picks previous tab as active', () => {
  let s = initialState('a');
  s = openTab(s, 'b');
  s = openTab(s, 'c'); // active = c
  s = closeTab(s, 'c');
  assert.deepEqual(s.panes[0].tabs, ['a', 'b']);
  assert.equal(s.panes[0].activeId, 'b');
});

test('closeTab: removing first tab when it was active picks the next', () => {
  let s = initialState('a');
  s = openTab(s, 'b');
  s = setActiveTab(s, 0, 'a');
  s = closeTab(s, 'a');
  assert.deepEqual(s.panes[0].tabs, ['b']);
  assert.equal(s.panes[0].activeId, 'b');
});

test('closeTab: removing inactive tab keeps current active', () => {
  let s = initialState('a');
  s = openTab(s, 'b'); // active = b
  s = closeTab(s, 'a');
  assert.deepEqual(s.panes[0].tabs, ['b']);
  assert.equal(s.panes[0].activeId, 'b');
});

test('closeTab: last tab in single pane leaves the pane empty', () => {
  let s = initialState('a');
  s = closeTab(s, 'a');
  assert.equal(s.panes.length, 1);
  assert.deepEqual(s.panes[0].tabs, []);
  assert.equal(s.panes[0].activeId, null);
});

test('closeTab: unknown noteId is a no-op', () => {
  const s = initialState('a');
  const s2 = closeTab(s, 'zzz');
  assert.equal(s, s2);
});

test('splitRight: clones active tab into a new pane', () => {
  let s = initialState('a');
  s = openTab(s, 'b'); // active = b
  s = splitRight(s);
  assert.equal(s.panes.length, 2);
  assert.equal(s.splitMode, 'horizontal');
  assert.deepEqual(s.panes[1].tabs, ['b']);
  assert.equal(s.panes[1].activeId, 'b');
  assert.equal(s.focusedPaneIndex, 1);
});

test('splitRight: capped at MAX_PANES (2)', () => {
  let s = initialState('a');
  s = splitRight(s);
  const before = JSON.stringify(s);
  s = splitRight(s);
  assert.equal(JSON.stringify(s), before);
  assert.equal(MAX_PANES, 2);
});

test('splitRight: noop when active pane has no active tab', () => {
  const s = initialState();
  const s2 = splitRight(s);
  assert.equal(s, s2);
});

test('closeTab: collapses split when right pane becomes empty', () => {
  let s = initialState('a');
  s = splitRight(s); // 2 panes, both active = a
  s = closeTab(s, 'a', 1); // close from right pane
  assert.equal(s.panes.length, 1);
  assert.equal(s.splitMode, 'none');
  assert.equal(s.focusedPaneIndex, 0);
});

test('unsplit: keeps first pane, drops split mode', () => {
  let s = initialState('a');
  s = openTab(s, 'b');
  s = splitRight(s);
  s = openTab(s, 'c', 1); // tabs on the right
  s = unsplit(s);
  assert.equal(s.panes.length, 1);
  assert.equal(s.splitMode, 'none');
  assert.equal(s.panes[0].activeId, 'b'); // original left pane preserved
});

test('unsplit: noop when not split', () => {
  const s = initialState('a');
  const s2 = unsplit(s);
  assert.equal(s, s2);
});

test('moveTab: cross-pane move removes from source and inserts in target', () => {
  let s = initialState('a');
  s = openTab(s, 'b'); // pane 0: [a,b]/b
  s = splitRight(s);   // pane 1: [b]/b; focus = 1
  s = openTab(s, 'c', 1); // pane 1: [b,c]/c
  // Move 'b' from pane 0 → pane 1 at index 0.
  s = moveTab(s, 0, 1, 1, 0);
  assert.deepEqual(s.panes[0].tabs, ['a']);
  assert.equal(s.panes[0].activeId, 'a');
  assert.deepEqual(s.panes[1].tabs, ['b', 'c']);
  assert.equal(s.panes[1].activeId, 'b');
  assert.equal(s.focusedPaneIndex, 1);
});

test('moveTab: same pane delegates to reorder', () => {
  let s = initialState('a');
  s = openTab(s, 'b');
  s = openTab(s, 'c'); // ['a','b','c']
  const reordered = moveTab(s, 0, 0, 0, 2); // a → end
  assert.deepEqual(reordered.panes[0].tabs, ['b', 'c', 'a']);
});

test('moveTab: collapses split when source (right pane) empties', () => {
  let s = initialState('a');
  s = splitRight(s); // 2 panes; both have 'a'; right was the new one
  // Move pane 1's only tab to pane 0 — pane 1 now empty → collapse.
  s = moveTab(s, 1, 0, 0, 0);
  assert.equal(s.panes.length, 1);
  assert.equal(s.splitMode, 'none');
});

test('moveTab: deduplicates if target already has the tab', () => {
  let s = initialState('a');
  s = openTab(s, 'b'); // pane 0: [a,b]
  s = splitRight(s);   // pane 1: [b] (cloned active)
  // Move 'b' from pane 0 → pane 1 at index 0. Target already has 'b';
  // result should still have a single 'b' in pane 1.
  s = moveTab(s, 0, 1, 1, 0);
  assert.deepEqual(s.panes[0].tabs, ['a']);
  assert.deepEqual(s.panes[1].tabs, ['b']);
});

test('moveTab: out-of-range source index is a no-op', () => {
  let s = initialState('a');
  s = splitRight(s);
  const before = s;
  s = moveTab(s, 0, 1, 9, 0);
  assert.equal(s, before);
});

test('moveTab: invalid pane index is a no-op', () => {
  const s = initialState('a');
  const s2 = moveTab(s, 0, 5, 0, 0);
  assert.equal(s, s2);
});

test('swapPanes: flips left and right pane', () => {
  let s = initialState('a');
  s = openTab(s, 'b');
  s = splitRight(s); // pane 0: [a,b]/b, pane 1: [b]/b, focused = 1
  // Make pane 1 distinct:
  s = openTab(s, 'c', 1); // pane 1: [b,c]/c
  s = swapPanes(s);
  assert.equal(s.panes[0].activeId, 'c');
  assert.deepEqual(s.panes[0].tabs, ['b', 'c']);
  assert.equal(s.panes[1].activeId, 'b');
  assert.deepEqual(s.panes[1].tabs, ['a', 'b']);
  assert.equal(s.focusedPaneIndex, 0);
});

test('swapPanes: noop when not split', () => {
  const s = initialState('a');
  const s2 = swapPanes(s);
  assert.equal(s, s2);
});

test('focusPane: changes focusedPaneIndex', () => {
  let s = initialState('a');
  s = splitRight(s); // focused = 1
  s = focusPane(s, 0);
  assert.equal(s.focusedPaneIndex, 0);
});

test('focusPane: out-of-range is a no-op', () => {
  const s = initialState('a');
  const s2 = focusPane(s, 9);
  assert.equal(s, s2);
});

test('reorder: moves a tab to a new index', () => {
  let s = initialState('a');
  s = openTab(s, 'b');
  s = openTab(s, 'c'); // ['a','b','c']
  s = reorder(s, 0, 0, 2); // a → end
  assert.deepEqual(s.panes[0].tabs, ['b', 'c', 'a']);
});

test('reorder: same indices is a no-op', () => {
  let s = initialState('a');
  s = openTab(s, 'b');
  const s2 = reorder(s, 0, 1, 1);
  assert.equal(s, s2);
});

test('reorder: out-of-range indices are no-ops', () => {
  let s = initialState('a');
  s = openTab(s, 'b');
  const s2 = reorder(s, 0, 9, 0);
  assert.equal(s, s2);
});

test('hydrate: returns initialState on garbage input', () => {
  const s = hydrate(null, 'fallback');
  assert.equal(s.panes[0].activeId, 'fallback');
});

test('hydrate: returns initialState when panes are missing', () => {
  const s = hydrate({ splitMode: 'horizontal' }, 'x');
  assert.equal(s.panes[0].activeId, 'x');
});

test('hydrate: sanitizes pane shape (empty tabs → fallback)', () => {
  const s = hydrate({ panes: [{ activeId: 'a', tabs: [] }] }, 'fallback');
  assert.equal(s.panes[0].activeId, 'fallback');
});

test('hydrate: round-trips a valid two-pane state', () => {
  const saved = {
    panes: [
      { activeId: 'a', tabs: ['a', 'b'] },
      { activeId: 'c', tabs: ['c'] },
    ],
    splitMode: 'horizontal',
    focusedPaneIndex: 1,
  };
  const s = hydrate(saved, null);
  assert.equal(s.panes.length, 2);
  assert.equal(s.splitMode, 'horizontal');
  assert.equal(s.focusedPaneIndex, 1);
  assert.deepEqual(s.panes[0].tabs, ['a', 'b']);
  assert.equal(s.panes[1].activeId, 'c');
});

test('hydrate: clamps focusedPaneIndex when out of range', () => {
  const s = hydrate({
    panes: [{ activeId: 'a', tabs: ['a'] }],
    focusedPaneIndex: 5,
  }, 'fallback');
  assert.equal(s.focusedPaneIndex, 0);
});

test('hydrate: drops invalid activeId, falls back to first tab', () => {
  const s = hydrate({
    panes: [{ activeId: 'missing', tabs: ['a', 'b'] }],
  }, null);
  assert.equal(s.panes[0].activeId, 'a');
});

test('hydrate: rejects too many panes', () => {
  const s = hydrate({
    panes: [
      { activeId: 'a', tabs: ['a'] },
      { activeId: 'b', tabs: ['b'] },
      { activeId: 'c', tabs: ['c'] },
    ],
  }, 'fb');
  assert.equal(s.panes.length, 1);
  assert.equal(s.panes[0].activeId, 'fb');
});

test('allOpenNoteIds: collects unique ids across panes', () => {
  let s = initialState('a');
  s = openTab(s, 'b');
  s = splitRight(s);
  s = openTab(s, 'c', 1);
  assert.deepEqual(allOpenNoteIds(s).sort(), ['a', 'b', 'c']);
});

test('activeNoteIdFor: returns the active id for a pane', () => {
  let s = initialState('a');
  s = openTab(s, 'b');
  assert.equal(activeNoteIdFor(s, 0), 'b');
});

test('activeNoteIdFor: defaults to focused pane', () => {
  let s = initialState('a');
  s = splitRight(s);
  assert.equal(activeNoteIdFor(s), 'a');
});

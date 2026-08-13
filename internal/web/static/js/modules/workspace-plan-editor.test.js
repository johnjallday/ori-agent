import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  addGroup,
  addItem,
  dependentsOf,
  duplicateItem,
  EditorState,
  findItem,
  itemCount,
  moveGroup,
  moveItem,
  removeGroup,
  removeItem,
  resolveDependencies,
  saveStateMeta,
  unavailableAssignees
} from './workspace-plan-editor.js';

function content() {
  return {
    execution: { mode: 'step_through' },
    groups: [
      {
        id: 'grp-1',
        title: 'Prepare',
        depends_on: [],
        items: [
          { id: 'itm-1', description: 'Snapshot staging', depends_on: [] },
          { id: 'itm-2', description: 'Verify checksums', depends_on: ['itm-1'] }
        ]
      },
      {
        id: 'grp-2',
        title: 'Cut over',
        depends_on: ['grp-1'],
        items: [{ id: 'itm-3', description: 'Switch traffic', depends_on: ['itm-2'] }]
      }
    ]
  };
}

test('editing operations never mutate the content they are given', () => {
  const original = content();
  const snapshot = JSON.stringify(original);

  addGroup(original, 'New');
  addItem(original, 'grp-1');
  moveGroup(original, 'grp-1', 'down');
  duplicateItem(original, 'itm-1');
  removeItem(original, 'itm-3');

  assert.equal(JSON.stringify(original), snapshot, 'an operation mutated its input');
});

test('adding a group or item mints a new stable id', () => {
  const withGroup = addGroup(content(), 'Verify');
  assert.equal(withGroup.groups.length, 3);
  assert.ok(withGroup.groups[2].id.startsWith('grp_'));

  const withItem = addItem(withGroup, withGroup.groups[2].id, 'Run the checks');
  assert.equal(withItem.groups[2].items.length, 1);
  assert.ok(withItem.groups[2].items[0].id.startsWith('itm_'));
  assert.equal(withItem.groups[2].items[0].description, 'Run the checks');
});

test('adding an item to an unknown group changes nothing', () => {
  const next = addItem(content(), 'grp-missing');
  assert.equal(itemCount(next), 3);
});

// Reordering must not renumber anything: dependencies reference ids, so a
// changed id would silently rewire the plan (FR-52).
test('reordering preserves every id', () => {
  const before = content();
  const idsBefore = before.groups.flatMap(group => [group.id, ...group.items.map(i => i.id)]);

  const moved = moveGroup(before, 'grp-2', 'up');
  assert.deepEqual(
    moved.groups.map(group => group.id),
    ['grp-2', 'grp-1'],
    'the group did not move'
  );

  const idsAfter = moved.groups.flatMap(group => [group.id, ...group.items.map(i => i.id)]);
  assert.deepEqual(idsAfter.sort(), idsBefore.sort(), 'reordering changed an id');

  // The dependency still points at the same element.
  assert.deepEqual(findItem(moved, 'itm-3').item.depends_on, ['itm-2']);
});

test('moving an item within its group preserves ids and order elsewhere', () => {
  const moved = moveItem(content(), 'itm-2', 'up');
  assert.deepEqual(
    moved.groups[0].items.map(item => item.id),
    ['itm-2', 'itm-1']
  );
  assert.deepEqual(
    moved.groups[1].items.map(item => item.id),
    ['itm-3']
  );
});

test('moving past an edge is a no-op rather than an error', () => {
  const first = moveGroup(content(), 'grp-1', 'up');
  assert.deepEqual(
    first.groups.map(g => g.id),
    ['grp-1', 'grp-2']
  );

  const last = moveItem(content(), 'itm-3', 'down');
  assert.deepEqual(
    last.groups[1].items.map(i => i.id),
    ['itm-3']
  );
});

// A duplicate is new work. Copying dependents onto it would change the graph
// the user was looking at.
test('duplicating an item gives it a new id and does not acquire dependents', () => {
  const next = duplicateItem(content(), 'itm-1');
  const items = next.groups[0].items;

  assert.equal(items.length, 3);
  assert.equal(items[1].description, 'Snapshot staging (copy)');
  assert.notEqual(items[1].id, 'itm-1');

  // itm-2 still depends only on the original.
  assert.deepEqual(findItem(next, 'itm-2').item.depends_on, ['itm-1']);
  assert.deepEqual(dependentsOf(next, items[1].id), []);
});

// Removing something other work depends on is refused with the specifics, not
// silently repaired (FR-51).
test('removing a depended-on item is refused and names the dependents', () => {
  const result = removeItem(content(), 'itm-1');

  assert.equal(result.removed, false);
  assert.equal(result.blockedBy.length, 1);
  assert.equal(result.blockedBy[0].id, 'itm-2');
  assert.equal(result.blockedBy[0].description, 'Verify checksums');
  assert.equal(itemCount(result.content), 3, 'a refused removal changed the content');
});

test('removing an item nothing depends on succeeds', () => {
  const result = removeItem(content(), 'itm-3');
  assert.equal(result.removed, true);
  assert.equal(itemCount(result.content), 2);
  assert.equal(findItem(result.content, 'itm-3'), null);
});

test('resolving dependencies then removing succeeds', () => {
  const resolved = resolveDependencies(content(), 'itm-1');
  assert.deepEqual(findItem(resolved, 'itm-2').item.depends_on, []);

  const result = removeItem(resolved, 'itm-1');
  assert.equal(result.removed, true);
  assert.equal(findItem(result.content, 'itm-1'), null);
});

test('removing a group another group depends on is refused', () => {
  const result = removeGroup(content(), 'grp-1');
  assert.equal(result.removed, false);
  assert.ok(
    result.blockedBy.some(entry => entry.id === 'grp-2'),
    `blockedBy did not name the dependent group: ${JSON.stringify(result.blockedBy)}`
  );
});

// A group whose items are depended on from outside must not be removable
// either — the refusal has to look past the group boundary.
test('removing a group whose items are depended on from outside is refused', () => {
  const detached = resolveDependencies(content(), 'grp-1');
  const withoutGroupEdge = {
    ...detached,
    groups: detached.groups.map(group => ({ ...group, depends_on: [] }))
  };

  const result = removeGroup(withoutGroupEdge, 'grp-1');
  assert.equal(result.removed, false, 'a group with externally-depended items was removed');
  assert.ok(result.blockedBy.some(entry => entry.id === 'itm-3'));
});

test('removing a group nothing depends on succeeds', () => {
  const result = removeGroup(content(), 'grp-2');
  assert.equal(result.removed, true);
  assert.equal(result.content.groups.length, 1);
});

// An assignee that disappeared must be surfaced, not quietly dropped (FR-48).
test('unavailableAssignees reports items assigned to a missing agent', () => {
  const assigned = content();
  assigned.groups[0].items[0].assignee = 'ghost';
  assigned.groups[0].items[1].assignee = 'builder';

  const unavailable = unavailableAssignees(assigned, ['builder']);
  assert.equal(unavailable.length, 1);
  assert.equal(unavailable[0].id, 'itm-1');
  assert.equal(unavailable[0].assignee, 'ghost');

  // An unassigned item is not a problem.
  assert.deepEqual(unavailableAssignees(content(), ['builder']), []);
  // A nil roster means "not checked", never "nothing is available".
  assert.deepEqual(unavailableAssignees(assigned, null), []);
});

// --- Save state ------------------------------------------------------------

test('every save state has a label and an icon', () => {
  for (const state of ['saved', 'unsaved', 'saving', 'conflicted']) {
    const meta = saveStateMeta(state);
    assert.ok(meta.label, `${state} has no label`);
    assert.ok(meta.icon, `${state} has no icon`);
  }
  assert.equal(saveStateMeta('nonsense').label, saveStateMeta('saved').label);
});

test('the editor tracks unsaved, saving, and saved transitions', () => {
  const editor = new EditorState({
    id: 'plan_1',
    title: 'Plan',
    objective: 'Objective',
    draft: content(),
    draft_revision: 3
  });

  assert.equal(editor.state, 'saved');
  assert.equal(editor.revision, 3);

  editor.apply(current => addGroup(current, 'Verify'));
  assert.equal(editor.state, 'unsaved');
  assert.equal(editor.content.groups.length, 3);

  editor.markSaving();
  assert.equal(editor.state, 'saving');

  editor.markSaved({ draft_revision: 4 });
  assert.equal(editor.state, 'saved');
  assert.equal(editor.revision, 4);
  assert.equal(editor.payload().revision, 4);
});

// A conflict must keep BOTH versions: losing the user's unsaved work to show
// them the winner would be the same bug in a different costume (FR-30).
test('a conflict retains the user’s content and the winning version', () => {
  const editor = new EditorState({ id: 'plan_1', draft: content(), draft_revision: 1 });
  editor.apply(current => addGroup(current, 'Mine'));

  editor.markConflicted({
    current_revision: 5,
    current: { id: 'plan_1', objective: 'Theirs' },
    recoverable_snapshots: [{ id: 'snap-1' }]
  });

  assert.equal(editor.state, 'conflicted');
  assert.equal(editor.conflict.currentRevision, 5);
  assert.equal(editor.conflict.current.objective, 'Theirs');
  assert.equal(editor.conflict.snapshots.length, 1);
  assert.equal(editor.conflict.mine.groups.length, 3, 'the user’s unsaved work was lost');

  // The editor still holds the user's content, so they can re-apply it.
  assert.equal(editor.content.groups.length, 3);
});

test('the editor payload carries an autosave flag only when asked', () => {
  const editor = new EditorState({ id: 'plan_1', draft: content(), draft_revision: 0 });
  assert.equal(editor.payload().autosave, false);
  assert.equal(editor.payload({ autosave: true }).autosave, true);
});

test('applying a refused removal leaves the content unchanged', () => {
  const editor = new EditorState({ id: 'plan_1', draft: content(), draft_revision: 0 });
  const result = editor.apply(current => removeItem(current, 'itm-1'));

  assert.equal(result.removed, false);
  assert.equal(itemCount(editor.content), 3, 'a refused removal changed the editor content');
});

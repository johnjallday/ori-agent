import test from 'node:test';
import assert from 'node:assert/strict';
import {
  folderTreeView,
  folderFocusView,
  folderSelectionFocus,
  folderSelectAllFocus,
  MAX_FOLDER_FOCUS,
  MAX_FOLDER_FOCUS_BYTES
} from './personal-assistant-folder-tree.js';
import { createFolderContextController } from './personal-assistant-folder-context.js';

const snapshot = () => ({
  version: 1,
  id: 'snapshot-a',
  folder: 'Documents',
  tree: {
    omitted: 2,
    nodes: [
      { id: 'entry-0', name: 'Aurora 🎼', kind: 'folder' },
      { id: 'entry-1', parent_id: 'entry-0', name: '<img onerror=alert(1)>.rpp', kind: 'file' },
      { id: 'entry-2', name: 'Notes.md', kind: 'file' }
    ]
  }
});

function fixture() {
  let currentId = '',
    result = { observation: snapshot() },
    busy = false;
  const calls = [];
  const controller = createFolderContextController({
    uuid: () => 'draft',
    currentId: () => currentId,
    isBusy: () => busy,
    post: async (url, body) => {
      calls.push({ url, body });
      if (result instanceof Error) throw result;
      return result;
    }
  });
  const binding = () => ({
    observationId: controller.state.observation.id,
    generation: controller.state.generation,
    conversationId: controller.state.conversationId
  });
  return {
    controller,
    binding,
    calls,
    setId: id => {
      currentId = id;
    },
    setBusy: value => {
      busy = value;
    },
    result: value => {
      result = value;
    }
  };
}

test('real typed relationships and independent folder/file topics, not synthetic descendants', () => {
  const observation = snapshot(),
    view = folderTreeView(observation);
  assert.equal(view.roots.length, 2);
  assert.equal(view.roots[0].children[0].id, 'entry-1');
  assert.equal(view.omitted, 2);
  assert.deepEqual(folderFocusView(observation, ['entry-0']).topics, [
    { names: ['Aurora 🎼'], kind: 'folder' }
  ]);
  assert.equal(folderFocusView(observation, ['entry-0', 'entry-1']).topics.length, 2);
  assert.deepEqual(folderFocusView(observation, []).topics, []);
  assert.equal(folderFocusView({ folder: 'Legacy' }, ['entry-0']), null);
  assert.equal(folderTreeView({ projects: [{ name: 'Folder' }] }), null);
});

test('malformed graphs, duplicate/missing/over-limit topics and indistinguishable labels fail closed', () => {
  for (const mutate of [
    observation => {
      observation.tree.nodes[0].id = '/path';
    },
    observation => {
      observation.tree.nodes[1].parent_id = 'entry-2';
    },
    observation => {
      observation.tree.nodes[1].parent_id = 'entry-1';
    },
    observation => {
      observation.tree.nodes[1].kind = 'symlink';
    }
  ]) {
    const observation = snapshot();
    mutate(observation);
    assert.equal(folderTreeView(observation), null);
  }
  for (const ids of [
    ['entry-99'],
    ['entry-0', 'entry-0'],
    Array(MAX_FOLDER_FOCUS + 1).fill('entry-0')
  ])
    assert.equal(folderFocusView(snapshot(), ids), null);
  const duplicate = snapshot();
  duplicate.tree.nodes[2].name = 'Aurora 🎼';
  duplicate.tree.nodes[2].kind = 'folder';
  assert.equal(folderTreeView(duplicate).roots[0].ambiguous, true);
  assert.equal(folderFocusView(duplicate, ['entry-0']), null);
  // Go escapes Unicode line/paragraph separators as well as HTML characters.
  // Escaped labels that exceeded the old four-KiB ceiling remain selectable.
  for (const separator of ['\u2028', '\u2029', '<']) {
    const bounded = snapshot();
    bounded.tree.nodes = Array.from({ length: 8 }, (_, index) => ({
      id: `entry-${index}`,
      name: `a${index}${separator.repeat(92)}z`,
      kind: 'folder'
    }));
    assert.equal(
      folderFocusView(
        bounded,
        bounded.tree.nodes.map(node => node.id)
      ).topics.length,
      8
    );
  }
  assert.equal(folderFocusView({ folder: 'x'.repeat(MAX_FOLDER_FOCUS_BYTES + 1) }, []), null);
});

test('Select all includes collapsed descendants or explicitly uses whole-folder focus, never a subset', () => {
  assert.deepEqual(folderSelectAllFocus(snapshot()), {
    ids: ['entry-0', 'entry-1', 'entry-2'],
    wholeFolder: false,
    count: 3
  });
  for (const count of [8, 9, 64]) {
    const observation = snapshot();
    observation.tree.nodes = Array.from({ length: count }, (_, index) => ({
      id: `entry-${index}`,
      name: `Topic ${index}`,
      kind: 'file'
    }));
    const all = folderSelectAllFocus(observation);
    assert.equal(all.wholeFolder, false);
    assert.equal(all.count, count);
    assert.equal(all.ids.length, count);
    const focus = folderSelectionFocus(observation, all.ids);
    assert.equal(focus.topics.length, count);
    assert.equal(folderFocusView(observation, all.ids).topics.length, count);
  }
  const ambiguous = snapshot();
  ambiguous.tree.nodes[2].name = ambiguous.tree.nodes[0].name;
  assert.equal(folderSelectAllFocus(ambiguous).wholeFolder, true);
  const bounded = snapshot();
  bounded.tree.nodes = Array.from({ length: 8 }, (_, index) => ({
    id: `entry-${index}`,
    name: `a${index}${'<'.repeat(92)}z`,
    kind: 'file'
  }));
  assert.deepEqual(folderSelectAllFocus(bounded), {
    ids: bounded.tree.nodes.map(node => node.id),
    wholeFolder: false,
    count: 8
  });
  const ambiguousIDs = folderSelectAllFocus(ambiguous).ids;
  assert.deepEqual(folderSelectionFocus(ambiguous, ambiguousIDs).topics, []);
  assert.equal(folderSelectionFocus(ambiguous, ambiguousIDs.slice(0, -1)), null);
  assert.equal(folderSelectAllFocus({ tree: { nodes: [] } }), null);
  assert.equal(folderSelectAllFocus({ projects: [{ name: 'Legacy' }] }), null);
});

test('full and partial selections preserve every topic without broadening or accepting invalid IDs', () => {
  const observation = snapshot();
  observation.tree.nodes = Array.from({ length: 10 }, (_, index) => ({
    id: `entry-${index}`,
    name: `Topic ${index}`,
    kind: 'file'
  }));
  const ids = folderSelectAllFocus(observation).ids;
  assert.equal(folderSelectionFocus(observation, [...ids].reverse()).topics.length, 10);
  assert.equal(folderSelectionFocus(observation, ids.slice(0, 9)).topics.length, 9);
  for (const invalid of [
    [...ids.slice(0, 9), 'entry-0'],
    [...ids.slice(0, 9), 'entry-unknown'],
    [...ids, 'entry-unknown'],
    null,
    'all'
  ])
    assert.equal(folderSelectionFocus(observation, invalid), null);
  assert.equal(folderSelectionFocus(observation, ids.slice(0, 8)).topics.length, 8);
  assert.equal(folderSelectionFocus({ tree: { nodes: [] } }, ids), null);
});

test('bulk selection can be narrowed above eight topics and Send keeps the exact remaining IDs', async () => {
  const f = fixture();
  const observation = snapshot();
  observation.tree.nodes = Array.from({ length: MAX_FOLDER_FOCUS }, (_, index) => ({
    id: `entry-${index}`,
    name: `Topic ${index}`,
    kind: 'file'
  }));
  f.result({ observation });
  await f.controller.select('chip', 'documents');
  const ids = folderSelectAllFocus(observation).ids;
  assert.equal(f.controller.setFocus(ids, f.binding()), true);
  assert.deepEqual(f.controller.state.focusIDs, ids);
  const sent = f.controller.request();
  assert.deepEqual(sent.focus_ids, ids);
  assert.equal(f.calls.length, 1);
  const remaining = ids.filter(id => !['entry-0', 'entry-9', 'entry-63'].includes(id));
  assert.equal(f.controller.setFocus(remaining, f.binding()), true);
  assert.deepEqual(f.controller.state.focusIDs, remaining);
  assert.deepEqual(f.controller.request().focus_ids, remaining);
  assert.deepEqual(sent.focus_ids, ids);

  f.result({ cancelled: true });
  await f.controller.select('picker');
  assert.deepEqual(f.controller.state.focusIDs, remaining);
  f.setId('saved');
  f.controller.accepted('saved', { revision: 'r1', observation });
  assert.deepEqual(f.controller.state.focusIDs, remaining);
  assert.deepEqual(f.controller.request().focus_ids, remaining);

  // Clearing or replacing the selection still behaves independently of history.
  assert.equal(f.controller.setFocus([], f.binding()), true);
  assert.equal(f.controller.setFocus(['entry-0'], f.binding()), true);
  assert.deepEqual(f.controller.request().focus_ids, ['entry-0']);
  assert.deepEqual(sent.focus_ids, ids);
  f.controller.reset('saved', { revision: 'r1', observation });
  assert.deepEqual(f.controller.state.focusIDs, []);
});

test('long escaped ancestor labels do not prevent unchecking entries from a full snapshot', () => {
  const observation = snapshot();
  observation.tree.nodes = Array.from({ length: MAX_FOLDER_FOCUS }, (_, index) => ({
    id: `entry-${index}`,
    parent_id: index ? `entry-${Math.min(index - 1, 2)}` : '',
    name: index < 3 ? '<'.repeat(96) : `Topic ${index}`,
    kind: index < 3 ? 'folder' : 'file'
  }));
  const all = folderSelectAllFocus(observation);
  assert.equal(all.wholeFolder, false);
  const remaining = all.ids.filter(id => id !== 'entry-9');
  const focus = folderSelectionFocus(observation, remaining);
  assert.equal(focus.topics.length, 63);
  assert.equal(focus.topics.at(-1).names.length, 4);
  assert.equal(
    focus.topics.some(topic => topic.names.at(-1) === 'Topic 9'),
    false
  );
});

test('unchecking one of nine selected entries becomes eight explicit host topics', async () => {
  const f = fixture();
  const observation = snapshot();
  observation.tree.nodes = Array.from({ length: 9 }, (_, index) => ({
    id: `entry-${index}`,
    name: `Topic ${index}`,
    kind: 'file'
  }));
  f.result({ observation });
  await f.controller.select('chip', 'documents');
  const ids = folderSelectAllFocus(observation).ids;
  f.controller.setFocus(ids, f.binding());
  assert.deepEqual(f.controller.request().focus_ids, ids);
  assert.equal(f.controller.setFocus(ids.slice(1), f.binding()), true);
  assert.deepEqual(f.controller.request().focus_ids, ids.slice(1));
});

test('checkbox changes are local; Send receives a frozen opaque reference and no metadata or paths', async () => {
  const f = fixture();
  await f.controller.select('chip', 'documents');
  assert.equal(f.controller.setFocus(['entry-0'], f.binding()), true);
  const sent = f.controller.request();
  assert.deepEqual(sent.focus_ids, ['entry-0']);
  assert.equal(f.controller.setFocus(['entry-1'], f.binding()), true);
  assert.deepEqual(sent.focus_ids, ['entry-0']);
  assert.deepEqual(f.controller.request().focus_ids, ['entry-1']);
  assert.equal(f.calls.length, 1);
  assert.equal(JSON.stringify(sent).includes('Aurora'), false);
  const ids = ['entry-2'];
  f.controller.setFocus(ids, f.binding());
  ids[0] = 'changed';
  assert.deepEqual(f.controller.state.focusIDs, ['entry-2']);
});

test('next focus can change during a reply, is preserved on acceptance, but reload/New reset it', async () => {
  const f = fixture();
  await f.controller.select('chip', 'documents');
  f.controller.setFocus(['entry-0'], f.binding());
  const sent = f.controller.request();
  f.setBusy(true);
  assert.equal(f.controller.setFocus(['entry-2'], f.binding()), true);
  f.setId('saved');
  f.controller.accepted('saved', { revision: 'r1', observation: snapshot() });
  assert.deepEqual(f.controller.state.focusIDs, ['entry-2']);
  assert.deepEqual(sent.focus_ids, ['entry-0']);
  f.controller.reset('saved', { revision: 'r1', observation: snapshot() });
  assert.deepEqual(f.controller.state.focusIDs, []);
  f.setId('');
  f.controller.reset();
  assert.equal(f.controller.request(), null);
});

test('cancel/failure preserve focus, successful replacement and detach clear it; stale callbacks fail', async () => {
  const f = fixture();
  await f.controller.select('chip', 'documents');
  const old = f.binding();
  f.controller.setFocus(['entry-0'], old);
  f.result({ cancelled: true });
  await f.controller.select('picker');
  assert.deepEqual(f.controller.state.focusIDs, ['entry-0']);
  assert.equal(f.controller.setFocus(['entry-2'], old), false);
  f.result(new Error('Access unavailable'));
  await f.controller.select('picker');
  assert.deepEqual(f.controller.state.focusIDs, ['entry-0']);
  const replacement = snapshot();
  replacement.id = 'new-snapshot';
  f.result({ observation: replacement });
  await f.controller.select('chip', 'desktop');
  assert.deepEqual(f.controller.state.focusIDs, []);
  assert.equal(f.controller.setFocus(['entry-0'], old), false);
  f.controller.setFocus(['entry-0'], f.binding());
  await f.controller.remove();
  assert.deepEqual(f.controller.state.focusIDs, []);
});

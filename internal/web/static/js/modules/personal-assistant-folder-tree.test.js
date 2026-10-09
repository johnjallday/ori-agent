import test from 'node:test';
import assert from 'node:assert/strict';
import { folderTreeView, folderFocusView } from './personal-assistant-folder-tree.js';
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
  for (const ids of [['entry-99'], ['entry-0', 'entry-0'], Array(9).fill('entry-0')])
    assert.equal(folderFocusView(snapshot(), ids), null);
  const duplicate = snapshot();
  duplicate.tree.nodes[2].name = 'Aurora 🎼';
  duplicate.tree.nodes[2].kind = 'folder';
  assert.equal(folderTreeView(duplicate).roots[0].ambiguous, true);
  assert.equal(folderFocusView(duplicate, ['entry-0']), null);
  // Go escapes Unicode line/paragraph separators as well as HTML characters.
  // Eight individually valid labels must not bypass its resolved byte bound.
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
      ),
      null
    );
  }
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

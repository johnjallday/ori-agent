import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const window = {};
vm.runInNewContext(readFileSync(new URL('./agent-workspace-editor.js', import.meta.url), 'utf8'), {
  window
});
const api = window.AgentWorkspaceEditor;
const member = (id, entry = false) => ({ id, name: id, entry_point: entry });
const membership = refs => api.membership({ workspaces: refs, workspace_count: refs.length });

test('only editable reusable definitions can change membership', () => {
  const agent = { name: 'Researcher', version: 'v1', origin: { source: 'roster' } };
  assert.equal(api.editable(agent), true);
  for (const patch of [
    { version: '' },
    { source: 'cli' },
    { role: 'cli_agent' },
    { state: 'unreadable' },
    { name: 'Ask Ori' },
    { name: '__assistant__' },
    { origin: { source: 'system' } },
    { origin: { source: 'workspace' } }
  ])
    assert.equal(api.editable({ ...agent, ...patch }), false, JSON.stringify(patch));
});

test('missing and partial membership cannot be submitted as an empty set', () => {
  for (const agent of [
    {},
    { workspace_count: 1 },
    { workspaces: [{}] },
    { workspaces: [], workspace_count: 2 },
    { workspaces: [], workspace_count: -1 }
  ]) {
    assert.throws(() => api.membership(agent), /unavailable|incomplete/);
  }
  assert.equal(api.membership({ workspace_count: 0, workspaces: null }).size, 0);
  assert.equal(api.membership({ workspaces: [] }).size, 0);
});

test('workspace rows flatten the collection, deduplicate IDs, and keep absent members', () => {
  const rows = api.workspaceRows(
    {
      workspaces: [{ id: 'a', name: 'Group', kind: 'group', children: [member('b')] }, member('b')]
    },
    membership([member('b', true), member('missing')])
  );
  assert.equal(rows.length, 3);
  assert.equal(rows.find(row => row.id === 'b').entry_point, true);
  assert.equal(rows.find(row => row.id === 'missing').available, false);
  assert.throws(() => api.workspaceRows({}, membership([])), /unavailable/);
  assert.throws(() => api.workspaceRows([null], membership([])), /incomplete/);
});

test('reload rebases explicit choices without stripping unseen additions', () => {
  const before = membership([member('a'), member('b')]);
  const latest = membership([member('a'), member('b'), member('new-elsewhere')]);
  const rows = api.workspaceRows(['a', 'b', 'c', 'new-elsewhere'].map(member), latest);
  const result = api.rebaseSelection(before, new Set(['b', 'c']), latest, rows);
  assert.deepEqual([...result].sort(), ['b', 'c', 'new-elsewhere']);
});

test('reload never detaches a newly protected entry agent or an unavailable member', () => {
  const before = membership([member('a'), member('b')]);
  const latest = membership([member('a', true), member('b')]);
  const rows = api.workspaceRows([member('a')], latest);
  const result = api.rebaseSelection(before, new Set(), latest, rows);
  assert.deepEqual([...result].sort(), ['a', 'b']);
});

test('a deleted workspace cannot remain a pending addition on reload', () => {
  const result = api.rebaseSelection(membership([]), new Set(['gone']), membership([]), []);
  assert.equal(result.size, 0);
});

test('preflight compares entry protection and IDs, not mutable display names or order', () => {
  assert.equal(
    api.signature(membership([member('a'), member('b')])),
    api.signature(membership([member('b'), { ...member('a'), name: 'Renamed' }]))
  );
  assert.notEqual(
    api.signature(membership([member('a')])),
    api.signature(membership([member('a', true)]))
  );
});

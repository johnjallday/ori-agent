// Tests for agent-origin.js — where an Agents-page entry comes from.
//
// agent-origin.js is a classic deferred script, so it is evaluated in a
// node:vm sandbox with a bare window, mirroring agent-avatar.test.js.
//   node --test internal/web/static/js/modules/agent-origin.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./agent-origin.js', import.meta.url), 'utf8');

function load() {
  const sandbox = { window: {}, Array, String, Object };
  vm.createContext(sandbox);
  vm.runInContext(source, sandbox);
  return sandbox.window.AgentOrigin;
}

const workspaceEntry = {
  name: 'Stranger',
  origin: { source: 'workspace', workspace_id: 'ws-1', workspace_name: 'Studio' }
};
const rosterEntry = {
  name: 'Scout',
  origin: {
    source: 'roster',
    customised_in: [
      { id: 'ws-1', name: 'Studio' },
      { id: 'ws-2', name: 'Garage' }
    ]
  }
};

test('a workspace entry is marked with its workspace', () => {
  const origin = load();
  assert.equal(origin.isWorkspaceOwned(workspaceEntry), true);
  assert.equal(origin.workspaceMarker(workspaceEntry), 'From workspace: Studio');
  assert.equal(origin.customisedInLabel(workspaceEntry), '');
});

test('a roster agent lists the workspaces that customised it', () => {
  const origin = load();
  assert.equal(origin.isWorkspaceOwned(rosterEntry), false);
  assert.equal(origin.workspaceMarker(rosterEntry), '');
  assert.equal(origin.customisedInLabel(rosterEntry), 'Customised in: Studio, Garage');
});

test('an entry without an origin is one of the user agents with nothing to say', () => {
  const origin = load();
  for (const agent of [{ name: 'Old' }, null, undefined, { origin: 'nonsense' }]) {
    assert.equal(origin.isWorkspaceOwned(agent), false);
    assert.equal(origin.workspaceMarker(agent), '');
    assert.equal(origin.customisedInLabel(agent), '');
  }
});

test('a saved edit says which workspace copies it reached and which kept their own', () => {
  const origin = load();
  const ref = (id, name) => ({ id, name });
  assert.equal(
    origin.carriedEditLabel({
      updated: [ref('a', 'Song 1'), ref('c', 'Song 3')],
      customised: [ref('b', 'Song 2')]
    }),
    'Also updated in 2 workspaces. Song 2 keeps its own changes.'
  );
  assert.equal(
    origin.carriedEditLabel({ updated: [ref('a', 'Song 1')], customised: [] }),
    'Also updated in 1 workspace.'
  );
  assert.equal(
    origin.carriedEditLabel({ updated: [], customised: [ref('b', 'Song 2'), ref('d', '')] }),
    'Song 2, d keep their own changes.'
  );
  for (const nothing of [undefined, null, {}, { updated: [], customised: [] }, 'x']) {
    assert.equal(origin.carriedEditLabel(nothing), '');
  }
});

test('a delete of an agent that works in workspaces is refused up front with the count', () => {
  const origin = load();
  const songs = n =>
    Array.from({ length: n }, (_, i) => ({ id: `s${i + 1}`, name: `Song ${i + 1}` }));
  assert.equal(
    origin.attachedDeleteMessage({
      name: 'Studio Assistant',
      workspace_count: 12,
      workspaces: songs(12)
    }),
    '“Studio Assistant” works in 12 workspaces (Song 1, Song 2, Song 3 and 9 more). ' +
      'Remove it from those workspaces before deleting it.'
  );
  assert.equal(
    origin.attachedDeleteMessage({ name: 'Solo', workspace_count: 1, workspaces: songs(1) }),
    '“Solo” works in 1 workspace (Song 1). Remove it from that workspace before deleting it.'
  );
  // The count alone, when the list was not included.
  assert.equal(
    origin.attachedDeleteMessage({ name: 'Solo', workspace_count: 2 }),
    '“Solo” works in 2 workspaces. Remove it from those workspaces before deleting it.'
  );
  for (const free of [{ name: 'Idle', workspace_count: 0 }, { name: 'Idle' }, null]) {
    assert.equal(origin.attachedDeleteMessage(free), '');
  }
});

test('a workspace entry without a name falls back to its ID', () => {
  const origin = load();
  assert.equal(
    origin.workspaceMarker({ origin: { source: 'workspace', workspace_id: 'ws-9' } }),
    'From workspace: ws-9'
  );
});

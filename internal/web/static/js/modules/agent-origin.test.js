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

test('a workspace entry without a name falls back to its ID', () => {
  const origin = load();
  assert.equal(
    origin.workspaceMarker({ origin: { source: 'workspace', workspace_id: 'ws-9' } }),
    'From workspace: ws-9'
  );
});

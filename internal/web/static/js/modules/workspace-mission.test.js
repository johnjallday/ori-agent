import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./workspace-mission.js', import.meta.url), 'utf8');

// Loads the module with no goal card on the page, so init() bails and only
// the exposed API is exercised.
function loadWorkspaceMission() {
  const window = { currentWorkspaceId: 'ws-1' };
  const document = {
    readyState: 'complete',
    querySelector: () => null,
    querySelectorAll: () => [],
    addEventListener() {},
    body: { dataset: {} }
  };
  vm.runInNewContext(source, { window, document, console, setTimeout, clearTimeout });
  return window.workspaceMission;
}

test('a goal card leads with the goal text and drops the empty-state line (FR8)', () => {
  const mission = loadWorkspaceMission();
  const summary = mission.summarize({ mission: '  Keep the brand voice consistent  ' });
  assert.equal(summary.mission, 'Keep the brand voice consistent');
  assert.equal(summary.title, 'Keep the brand voice consistent');
  assert.equal(summary.text, '');
  assert.equal(summary.canRun, true);
});

test('a workspace with no goal asks for one so agents can suggest work (FR8)', () => {
  const mission = loadWorkspaceMission();
  const summary = mission.summarize({ mission: '' });
  assert.equal(summary.title, 'No goal yet');
  assert.equal(summary.text, 'Set a goal so your agents can suggest work');
  assert.equal(summary.canRun, false);
  assert.equal(summary.runTitle, 'Set a goal before running');
});

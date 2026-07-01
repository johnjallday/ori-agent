import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView } from './workspace-command.js';

// The constructor only reads the DOM (getElementById) and returns early from
// setup() when there's no container — a null-returning stub is enough to build
// an instance whose pure format helpers we can exercise.
globalThis.document = { getElementById: () => null };
const view = new WorkspaceCommandView(null);

test('statusTone maps an agent status to a visual tone', () => {
  assert.equal(view.statusTone('working', ''), 'working');
  assert.equal(view.statusTone('', 'running a task'), 'working');
  assert.equal(view.statusTone('busy', ''), 'working');
  assert.equal(view.statusTone('error', ''), 'alert');
  assert.equal(view.statusTone('failed', ''), 'alert');
  assert.equal(view.statusTone('idle', ''), 'idle');
  assert.equal(view.statusTone('', ''), 'idle');
});

test('taskTone maps a task status to a visual tone', () => {
  assert.equal(view.taskTone('pending'), 'pending');
  assert.equal(view.taskTone('in_progress'), 'working');
  assert.equal(view.taskTone('completed'), 'done');
  assert.equal(view.taskTone('done'), 'done');
  assert.equal(view.taskTone('failed'), 'alert');
  assert.equal(view.taskTone('cancelled'), 'alert');
  assert.equal(view.taskTone('anything-else'), 'pending');
});

test('opsModeLabel maps workflow modes to friendly labels', () => {
  const withMode = (mode) => {
    view.page = { workspace: { workspace_settings: { workflow: { mode } } } };
    return view.opsModeLabel();
  };
  assert.equal(withMode('guided'), 'Guided');
  assert.equal(withMode('direct'), 'Direct');
  assert.equal(withMode('plan_then_execute'), 'Autonomous');
  assert.equal(withMode(''), '');
});

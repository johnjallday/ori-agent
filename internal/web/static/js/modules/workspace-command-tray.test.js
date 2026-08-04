import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView } from './workspace-command.js';
import { RUN_PHASE } from './workspace-execution-controller.js';

globalThis.document = { getElementById: () => null };
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };

// A timer-free stand-in for the execution controller, so tray rendering is
// tested without spinning real polling intervals.
function fakeController(runs, selectedId) {
  let selected = selectedId;
  return {
    getSelected: () => runs.find(r => r.taskId === selected) || null,
    getRun: id => runs.find(r => r.taskId === id) || null,
    getRuns: () => runs,
    getActiveRuns: () => runs.filter(r => r.phase !== RUN_PHASE.SETTLED),
    getAttentionRuns: () =>
      runs.filter(r =>
        ['needs_input', 'failed', 'timed_out', 'blocked'].includes(r.presentation.state)
      ),
    select: id => {
      selected = id;
    }
  };
}

function runFixture(over = {}) {
  return {
    taskId: 't1',
    presentation: {
      state: 'running',
      label: 'Running',
      tone: 'active',
      assignee: 'editor',
      primaryAction: { id: 'track', label: 'Track' }
    },
    task: { description: 'Export the master' },
    activity: [{ label: 'Running' }],
    phase: RUN_PHASE.LIVE,
    startedAt: Date.now() - 5000,
    lastActivityAt: 0,
    ...over
  };
}

function makeView() {
  const view = new WorkspaceCommandView(null);
  view.page = { workspaceId: 'w1' };
  view.renderTrayBody = () => {}; // state methods shouldn't touch the DOM here
  view.trayOpen = true;
  return view;
}

test('expanded tray renders title, state, controls, activity log and primary action', () => {
  const view = makeView();
  view.execController = fakeController([runFixture()], 't1');
  const html = view.trayHTML();
  assert.ok(html.includes('Export the master'));
  assert.ok(html.includes('Running'));
  assert.ok(html.includes('data-cmd-tray-collapse'));
  assert.ok(html.includes('data-cmd-tray-close'));
  assert.ok(html.includes('ws-cmd-tray-log'));
  assert.ok(html.includes('data-cmd-tray-action="track"'));
});

test('collapsed tray drops the log/actions but keeps the header (FR48)', () => {
  const view = makeView();
  view.execController = fakeController([runFixture()], 't1');
  view.trayCollapsed = true;
  const html = view.trayHTML();
  assert.ok(html.includes('Export the master'), 'header still present');
  assert.ok(!html.includes('ws-cmd-tray-log'), 'no full log when collapsed');
  assert.ok(!html.includes('data-cmd-tray-action'), 'no action buttons when collapsed');
});

test('toggleTrayCollapsed flips the collapsed flag (monitoring untouched)', () => {
  const view = makeView();
  view.execController = fakeController([runFixture()], 't1');
  assert.equal(view.trayCollapsed, false);
  view.toggleTrayCollapsed();
  assert.equal(view.trayCollapsed, true);
});

test('closeTray hides the launcher but never clears the controller (FR52)', () => {
  const view = makeView();
  const controller = fakeController([runFixture()], 't1');
  view.execController = controller;
  view.trayEl = { hidden: false };
  view.closeTray();
  assert.equal(view.trayOpen, false);
  assert.equal(view.trayEl.hidden, true);
  assert.equal(
    view.execController,
    controller,
    'controller (and its runs) survive closing the tray'
  );
  assert.ok(controller.getSelected(), 'the run is still tracked');
});

test('a running task offers Cancel that requires an explicit armed confirmation (FR63)', () => {
  const cancels = [];
  const view = makeView();
  view.page = { workspaceId: 'w1', cancelTask: id => cancels.push(id) };
  view.execController = fakeController([runFixture()], 't1');

  // First activation arms; it does NOT cancel.
  view.trayCancel('t1');
  assert.equal(view._trayCancelArmed, 't1');
  assert.deepEqual(cancels, []);
  assert.match(view.trayHTML(), /Confirm cancel/);

  // Second activation cancels.
  view.trayCancel('t1');
  assert.deepEqual(cancels, ['t1']);
  assert.equal(view._trayCancelArmed, '');
});

test('run switcher lists every run and marks the selected one (FR53)', () => {
  const view = makeView();
  const runs = [
    runFixture(),
    runFixture({
      taskId: 't2',
      task: { description: 'Render intro' },
      presentation: {
        state: 'needs_input',
        label: 'Needs Input',
        tone: 'attention',
        assignee: 'ed'
      }
    })
  ];
  view.execController = fakeController(runs, 't1');
  const html = view.trayHTML();
  assert.ok(html.includes('data-cmd-tray-run="t1"'));
  assert.ok(html.includes('data-cmd-tray-run="t2"'));
  assert.match(html, /data-cmd-tray-run="t1"[^>]*aria-current="true"/);
});

test('selectTrayRun switches the controller selection', () => {
  const view = makeView();
  const controller = fakeController([runFixture(), runFixture({ taskId: 't2' })], 't1');
  view.execController = controller;
  view.selectTrayRun('t2');
  assert.equal(controller.getSelected().taskId, 't2');
});

test('terminal run keeps its primary next action available (FR62)', () => {
  const view = makeView();
  view.execController = fakeController(
    [
      runFixture({
        phase: RUN_PHASE.SETTLED,
        presentation: {
          state: 'completed',
          label: 'Completed',
          tone: 'success',
          primaryAction: { id: 'view_result', label: 'View Result' }
        }
      })
    ],
    't1'
  );
  const html = view.trayHTML();
  assert.ok(html.includes('data-cmd-tray-action="view_result"'));
  assert.ok(!html.includes('data-cmd-tray-cancel'), 'no Cancel on a settled run');
});

test('trayElapsedLabel formats seconds and minutes', () => {
  const view = makeView();
  const now = Date.now();
  assert.equal(view.trayElapsedLabel({ startedAt: now - 5000 }).endsWith('s'), true);
  const mins = view.trayElapsedLabel({ startedAt: now - 125000 });
  assert.match(mins, /2m \d+s/);
});

test('empty controller shows a no-run placeholder', () => {
  const view = makeView();
  view.execController = fakeController([], '');
  assert.match(view.trayHTML(), /No active run/);
});

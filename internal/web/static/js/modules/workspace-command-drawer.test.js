import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView } from './workspace-command.js';
import { FILTER } from './task-presentation.js';

// The constructor only touches the DOM via getElementById (stubbed to null) and
// returns early without a container, so a bare instance exposes the drawer's
// pure builders. DOM-touching methods (render/ensureTaskDrawer/renderTaskDrawerBody)
// are stubbed per-test so we can assert state and generated HTML.
globalThis.document = { getElementById: () => null };
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };

function makeView(tasks, extraPage = {}) {
  const view = new WorkspaceCommandView(null);
  view.page = { tasks, workspaceId: 'w1', ...extraPage };
  view.render = () => {};
  view.ensureTaskDrawer = () => null;
  view.renderTaskDrawerBody = () => {};
  // Stub the tray hand-off so drawer tests never spin a real polling controller.
  view.trackAndShowTray = () => {};
  return view;
}

const tasks = [
  { id: 'a-run', status: 'in_progress', to: 'agent', updated_at: '2026-06-01T00:00:00Z' },
  { id: 'b-blocked', status: 'blocked', to: 'agent', updated_at: '2026-06-01T00:00:00Z' },
  { id: 'c-done', status: 'completed', to: 'agent', result: 'ok', updated_at: '2026-05-01T00:00:00Z' },
  { id: 'd-ready', status: 'pending', to: 'agent', updated_at: '2026-06-02T00:00:00Z' },
  { id: 'e-unassigned', status: 'pending', updated_at: '2026-06-02T00:00:00Z' },
  { id: 'sub', status: 'pending', to: 'agent', parent_task_id: 'a-run' }
];

test('openTaskDrawer closes the Objectives map window and opens the drawer (FR11)', () => {
  const view = makeView(tasks);
  view.activeMapWindow = 'objectives';
  view.openTaskDrawer({ focus() {} });
  assert.equal(view.activeMapWindow, '', 'the map window is closed in the same transition');
  assert.equal(view.taskDrawerOpen, true);
  assert.ok(view.taskDrawerSelectedId, 'a task is auto-selected');
});

test('drawerTasks excludes subtasks and sorts by resolver priority (FR24)', () => {
  const view = makeView(tasks);
  const ids = view.drawerTasks().map(t => t.id);
  assert.ok(!ids.includes('sub'), 'subtasks excluded');
  // blocked (intervention) sorts before running, completed last-ish.
  assert.ok(ids.indexOf('b-blocked') < ids.indexOf('a-run'));
  assert.ok(ids.indexOf('a-run') < ids.indexOf('c-done'));
});

test('Actionable is the default filter and excludes completed (FR17)', () => {
  const view = makeView(tasks);
  assert.equal(view.taskDrawerFilter, FILTER.ACTIONABLE);
  const ids = view.drawerFilteredTasks().map(t => t.id);
  assert.ok(!ids.includes('c-done'), 'completed not actionable');
  assert.ok(ids.includes('a-run') && ids.includes('b-blocked'));
});

test('drawer list uses a stable short id, not a Quest array index (FR7, FR15)', () => {
  const view = makeView(tasks);
  const html = view.drawerListHTML();
  assert.ok(!/Quest\s*\d/.test(html), 'no positional Quest NN marker');
  assert.ok(html.includes('#'), 'shows a stable short id derived from the task id');
  assert.ok(html.includes('data-cmd-drawer-select="a-run"'));
});

test('filter counts come from the shared presentation model (FR16, FR45)', () => {
  const view = makeView(tasks);
  const html = view.taskDrawerHTML();
  // Completed filter count should be exactly 1 (c-done).
  assert.match(html, /data-cmd-drawer-filter="completed"[^>]*>[^<]*Completed<[^>]*>1</);
});

test('preview shows the selected task with a real Open Full Task href (FR22)', () => {
  const view = makeView(tasks);
  view.taskDrawerSelectedId = 'a-run';
  const html = view.drawerPreviewHTML();
  assert.ok(html.includes('/workspaces/w1/task/a-run'), 'real, workspace-scoped task href');
  assert.ok(html.includes('data-cmd-drawer-action="track"'), 'running task offers Track');
});

test('selecting a task that left the filter keeps it visible with an off-filter notice (FR26)', () => {
  const view = makeView(tasks);
  view.taskDrawerFilter = FILTER.ACTIVE; // excludes completed
  view.taskDrawerSelectedId = 'c-done';
  const html = view.drawerPreviewHTML();
  assert.ok(html.includes('moved out of the'), 'off-filter notice shown');
  assert.ok(html.includes('data-cmd-drawer-filter="all"'), 'offers to reveal in All');
});

test('setDrawerFilter keeps a still-present selection and reselects only when it is gone', () => {
  const view = makeView(tasks);
  view.taskDrawerSelectedId = 'a-run';
  view.setDrawerFilter(FILTER.COMPLETED);
  assert.equal(view.taskDrawerSelectedId, 'a-run', 'selection preserved even if off-filter');

  view.taskDrawerSelectedId = 'gone';
  view.setDrawerFilter(FILTER.ACTIVE);
  assert.ok(
    view.drawerFilteredTasks().some(t => t.id === view.taskDrawerSelectedId),
    'a missing selection is replaced by a visible task'
  );
});

test('empty filter renders an explanatory empty state, not a blank list', () => {
  const view = makeView([{ id: 'only-done', status: 'completed', to: 'a' }]);
  view.taskDrawerFilter = FILTER.ACTIVE;
  assert.match(view.drawerListHTML(), /No tasks here/);
});

test('closeTaskDrawer marks the drawer closed', () => {
  const view = makeView(tasks);
  view.taskDrawerOpen = true;
  view.container = null;
  view.closeTaskDrawer();
  assert.equal(view.taskDrawerOpen, false);
});

test('runDrawerAction routes start/retry to the page executeTask handler', () => {
  const calls = [];
  const view = makeView(tasks, { executeTask: (id, opts) => calls.push([id, opts]) });
  view.runDrawerAction('start', 'd-ready');
  assert.deepEqual(calls, [['d-ready', { skipConfirm: true, skipModal: true }]]);
});

test('runDrawerAction routes view_result to showTaskResult', () => {
  const calls = [];
  const view = makeView(tasks, { showTaskResult: id => calls.push(id) });
  view.runDrawerAction('view_result', 'c-done');
  assert.deepEqual(calls, ['c-done']);
});

test('drawer header exposes an Add Task entry point and a polite live region (FR23, FR27)', () => {
  const view = makeView(tasks);
  const html = view.taskDrawerHTML();
  assert.ok(html.includes('data-cmd-drawer-add'), 'in-drawer Add Task control present');
  assert.match(html, /aria-live="polite"/, 'polite live region for announcements');
});

test('reconcileDrawerSelection announces and reselects when the selection vanishes (FR27)', () => {
  const view = makeView(tasks);
  view.taskDrawerSelectedId = 'ghost-task';
  view.reconcileDrawerSelection();
  assert.match(view._drawerAnnounce, /no longer available/);
  assert.ok(
    view.drawerTasks().some(t => t.id === view.taskDrawerSelectedId),
    'a present task is selected in place of the vanished one'
  );
});

test('reconcileDrawerSelection is silent when the selection still exists', () => {
  const view = makeView(tasks);
  view.taskDrawerSelectedId = 'a-run';
  view.reconcileDrawerSelection();
  assert.equal(view._drawerAnnounce, '');
  assert.equal(view.taskDrawerSelectedId, 'a-run');
});

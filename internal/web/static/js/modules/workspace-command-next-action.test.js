import test from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView } from './workspace-command.js';

globalThis.document = { getElementById: () => null };
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
function view(tasks, extra = {}) {
  const result = new WorkspaceCommandView(null);
  result.page = { workspaceId: 'w', workspaceSlug: 'desk', tasks, ...extra };
  return result;
}
const running = { id: 'run', status: 'in_progress', description: 'Routine work' };
const completed = {
  id: 'done',
  status: 'completed',
  description: 'Available result',
  result: 'Done',
  updated_at: '2026-10-01T00:00:00Z'
};

test('attention precedes routine running while preserving canonical priority within attention', () => {
  const failed = { id: 'failure', status: 'failed' };
  const input = { id: 'input', status: 'in_progress', context: { execution_step_waiting: true } };
  const command = view([running, failed, completed]);
  assert.equal(command.nextWork().next.id, failed.id);
  assert.equal(command.nextWork().current.id, running.id);
  assert.equal(command.nextWork().result.id, completed.id);
  command.page.tasks.push(input);
  assert.equal(command.nextWork().next.id, input.id);
  assert.equal(command.nextWork().attention, 2);
});

for (const [task, action] of [
  [{ status: 'pending' }, 'assign_start'],
  [{ status: 'pending', to: 'Writer' }, 'start'],
  [{ status: 'in_progress' }, 'track'],
  [{ status: 'waiting_for_choice' }, 'respond'],
  [{ status: 'blocked' }, 'inspect'],
  [
    {
      status: 'blocked',
      context: { human_loop: { repair: { label: 'Connect', url: '/agents' } } }
    },
    'repair'
  ],
  [{ status: 'failed' }, 'retry'],
  [{ status: 'timeout' }, 'retry'],
  [completed, 'view_result'],
  [{ status: 'future_status' }, 'inspect']
]) {
  test(`next action reuses the ${action} owner for ${task.status}`, () => {
    const command = view([{ ...task, id: 'task' }]);
    assert.match(command.renderWorkFirst(), new RegExp(`data-cmd-work-action="${action}"`));
    const calls = [];
    command.openTaskDrawer = () => calls.push(['drawer', command.taskDrawerSelectedId]);
    command.runDrawerAction = (...args) => calls.push(args);
    command.runWorkAction(action, 'task', null);
    assert.deepEqual(calls, [
      ['drawer', 'task'],
      [action, 'task']
    ]);
  });
}

test('loading and failure never masquerade as an idle workspace; Retry has one owner', () => {
  assert.match(view(undefined).renderWorkFirst(), /Loading workspace tasks/);
  assert.match(view([], { tasksLoading: true }).renderWorkFirst(), /Loading workspace tasks/);
  const failed = view([], { tasksLoadFailed: true });
  assert.match(failed.renderWorkFirst(), /Retry tasks/);
  assert.doesNotMatch(failed.renderWorkFirst(), /Give Task|No open work/);
  assert.match(failed.drawerListHTML(), /Tasks unavailable/);
  assert.match(failed.taskDrawerHTML(), /—/);
  assert.match(view([]).renderWorkFirst(), /Give Task/);
});

test('closed and Backlog records do not invent work or a missing result', () => {
  const command = view([
    { id: 'b', status: 'backlog' },
    { id: 'c', status: 'completed' }
  ]);
  assert.equal(command.nextWork().state, 'idle');
  assert.doesNotMatch(command.renderWorkFirst(), /View Result|data-cmd-work-action="start"/);
  assert.equal(view([], { workspace: { kind: 'group' } }).renderWorkFirst(), '');
});

test('refresh replaces recommendations without changing explicit task and agent selection', () => {
  const command = view([running]);
  command.taskDrawerSelectedId = running.id;
  command.activeAgentName = 'writer';
  command.page.tasks = [completed];
  assert.equal(command.nextWork().next.id, completed.id);
  assert.equal(command.taskDrawerSelectedId, running.id);
  assert.equal(command.activeAgentName, 'writer');
});

test('stale or newly repair-gated primary actions cannot start execution', () => {
  const command = view([{ id: 't', status: 'blocked' }]);
  command.openTaskDrawer = () => {};
  command.runDrawerAction = () => assert.fail('must review the changed task first');
  command.runWorkAction('start', 't');
  assert.match(command.workNotice, /changed/);
  command.runWorkAction('retry', 'deleted');
  assert.equal(command.taskDrawerSelectedId, 'deleted');
});

test('explicit deep-link selection survives loading and a failed read', () => {
  const command = view([], { tasksLoading: true });
  command.taskDrawerOpen = true;
  command.taskDrawerSelectedId = 'requested';
  command.reconcileDrawerSelection();
  assert.equal(command.taskDrawerSelectedId, 'requested');
  command.page.tasksLoading = false;
  command.page.tasksLoadFailed = true;
  command.reconcileDrawerSelection();
  assert.equal(command.taskDrawerSelectedId, 'requested');
  command.page.tasksLoadFailed = false;
  command.reconcileDrawerSelection();
  assert.match(command._drawerAnnounce, /no longer available/);
});

test('summary-only drawer links acquire data without overriding an explicit task', () => {
  const command = view([]);
  command.taskDrawerOpen = true;
  command.reconcileDrawerSelection();
  assert.equal(command.taskDrawerSelectedId, '');
  command.page.tasks = [running, completed];
  command.reconcileDrawerSelection();
  assert.equal(command.taskDrawerSelectedId, 'run');
  command.taskDrawerSelectedId = 'done';
  command.reconcileDrawerSelection();
  assert.equal(command.taskDrawerSelectedId, 'done');
});

test('task titles stay text and configuration is a named secondary disclosure', () => {
  const command = view([{ ...running, description: '<img onerror=alert(1)>' }]);
  assert.match(command.renderWorkFirst(), /&lt;img/);
  assert.doesNotMatch(command.renderWorkFirst(), /<img/);
  assert.match(command.renderWorkspaceAdmin(), /<summary>Workspace details &amp; settings/);
});

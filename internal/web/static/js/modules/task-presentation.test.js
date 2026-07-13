import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  PRESENTATION_STATE,
  FILTER,
  resolveTaskState,
  resolveTaskPresentation,
  taskMatchesFilter,
  resolveTaskCounts,
  sortTasksForDrawer,
  taskShortId,
  taskLatestActivityAt
} from './task-presentation.js';

// ---- Fixtures for every presentation state (FR143) ----
const unassigned = { id: 't-unassigned', status: 'pending' };
const ready = { id: 't-ready', status: 'pending', to: 'agent-a' };
const readyAssigned = { id: 't-assigned', status: 'assigned', agent_name: 'agent-b' };
const running = { id: 't-running', status: 'in_progress', to: 'agent-a' };
const needsInputChoice = { id: 't-choice', status: 'waiting_for_choice', to: 'agent-a' };
const needsInputHumanLoop = {
  id: 't-hl',
  status: 'in_progress',
  to: 'agent-a',
  context: { human_loop: { state: 'waiting_for_choice' } }
};
const needsInputStep = {
  id: 't-step',
  status: 'in_progress',
  to: 'agent-a',
  context: { execution_step_waiting: true }
};
const blocked = { id: 't-blocked', status: 'blocked', to: 'agent-a' };
const blockedWithInput = {
  id: 't-blocked-input',
  status: 'blocked',
  to: 'agent-a',
  context: { human_loop: { state: 'waiting_for_choice' } }
};
const failed = { id: 't-failed', status: 'failed', to: 'agent-a' };
const errored = { id: 't-error', status: 'error', to: 'agent-a' };
const timedOut = { id: 't-timeout', status: 'timeout', to: 'agent-a' };
const completed = { id: 't-completed', status: 'completed', to: 'agent-a', result: 'ok' };
const succeeded = { id: 't-success', status: 'success', to: 'agent-a' };
const cancelled = { id: 't-cancelled', status: 'cancelled' };
const skipped = { id: 't-skipped', status: 'skipped' };
const unknown = { id: 't-unknown', status: 'frobnicating' };

test('resolveTaskState maps every raw status to its canonical state', () => {
  assert.equal(resolveTaskState(unassigned), PRESENTATION_STATE.NEEDS_ASSIGNMENT);
  assert.equal(resolveTaskState(ready), PRESENTATION_STATE.READY);
  assert.equal(resolveTaskState(readyAssigned), PRESENTATION_STATE.READY);
  assert.equal(resolveTaskState(running), PRESENTATION_STATE.RUNNING);
  assert.equal(resolveTaskState(needsInputChoice), PRESENTATION_STATE.NEEDS_INPUT);
  assert.equal(resolveTaskState(needsInputHumanLoop), PRESENTATION_STATE.NEEDS_INPUT);
  assert.equal(resolveTaskState(needsInputStep), PRESENTATION_STATE.NEEDS_INPUT);
  assert.equal(resolveTaskState(blocked), PRESENTATION_STATE.BLOCKED);
  assert.equal(resolveTaskState(failed), PRESENTATION_STATE.FAILED);
  assert.equal(resolveTaskState(errored), PRESENTATION_STATE.FAILED);
  assert.equal(resolveTaskState(timedOut), PRESENTATION_STATE.TIMED_OUT);
  assert.equal(resolveTaskState(completed), PRESENTATION_STATE.COMPLETED);
  assert.equal(resolveTaskState(succeeded), PRESENTATION_STATE.COMPLETED);
  assert.equal(resolveTaskState(cancelled), PRESENTATION_STATE.CANCELLED);
  assert.equal(resolveTaskState(skipped), PRESENTATION_STATE.SKIPPED);
});

test('timed-out is NOT flattened into failed (FR35)', () => {
  const pres = resolveTaskPresentation(timedOut);
  assert.equal(pres.state, PRESENTATION_STATE.TIMED_OUT);
  assert.equal(pres.label, 'Timed Out');
  assert.notEqual(pres.state, PRESENTATION_STATE.FAILED);
});

test('blocked WITH an answerable input resolves to Needs Input, not Blocked (FR34)', () => {
  assert.equal(resolveTaskState(blockedWithInput), PRESENTATION_STATE.NEEDS_INPUT);
});

test('unknown raw status is a safe neutral state, never Pending (FR38)', () => {
  const pres = resolveTaskPresentation(unknown);
  assert.equal(pres.state, PRESENTATION_STATE.UNKNOWN);
  assert.equal(pres.isUnknown, true);
  assert.equal(pres.rawState, 'frobnicating', 'raw value stays exposed for developer detail');
  assert.notEqual(pres.label, 'Pending');
});

test('primary actions match the state matrix (FR30-36)', () => {
  assert.equal(resolveTaskPresentation(unassigned).primaryAction.id, 'assign_start');
  assert.equal(resolveTaskPresentation(ready).primaryAction.id, 'start');
  assert.equal(resolveTaskPresentation(running).primaryAction.id, 'track');
  assert.equal(resolveTaskPresentation(needsInputChoice).primaryAction.id, 'respond');
  assert.equal(resolveTaskPresentation(blocked).primaryAction.id, 'inspect');
  assert.equal(resolveTaskPresentation(completed).primaryAction.id, 'view_result');
});

test('failed/timed-out expose Inspect & Retry only when retry is supported (FR35)', () => {
  assert.equal(resolveTaskPresentation(failed).primaryAction.id, 'retry');
  assert.equal(resolveTaskPresentation(timedOut).primaryAction.id, 'retry');

  const exhausted = {
    id: 't-exhausted',
    status: 'failed',
    context: { execution_retry: { attempts_used: 3, max_attempts: 3 } }
  };
  assert.equal(resolveTaskPresentation(exhausted).primaryAction.id, 'inspect');

  const hasBudget = {
    id: 't-budget',
    status: 'failed',
    context: { execution_retry: { attempts_used: 1, max_attempts: 3 } }
  };
  assert.equal(resolveTaskPresentation(hasBudget).primaryAction.id, 'retry');
});

test('secondary actions: Running→Cancel(confirm), Completed→Re-run (FR32, FR36)', () => {
  const runPres = resolveTaskPresentation(running);
  assert.deepEqual(runPres.secondaryActions, [{ id: 'cancel', label: 'Cancel', confirm: true }]);
  assert.equal(resolveTaskPresentation(running, { cancelSupported: false }).secondaryActions.length, 0);

  const donePres = resolveTaskPresentation(completed);
  assert.deepEqual(donePres.secondaryActions, [{ id: 'rerun', label: 'Re-run' }]);
  assert.equal(resolveTaskPresentation(completed, { rerunSupported: false }).secondaryActions.length, 0);
});

test('tones carry role-independent runtime meaning (FR40 boundary)', () => {
  assert.equal(resolveTaskPresentation(running).tone, 'active');
  assert.equal(resolveTaskPresentation(needsInputChoice).tone, 'attention');
  assert.equal(resolveTaskPresentation(failed).tone, 'danger');
  assert.equal(resolveTaskPresentation(timedOut).tone, 'danger');
  assert.equal(resolveTaskPresentation(completed).tone, 'success');
});

test('count categories: Actionable/Active/Needs Attention membership (FR16-19)', () => {
  // Active excludes needs_assignment, failed, timed-out, and terminal states.
  assert.equal(taskMatchesFilter(unassigned, FILTER.ACTIVE), false);
  assert.equal(taskMatchesFilter(ready, FILTER.ACTIVE), true);
  assert.equal(taskMatchesFilter(running, FILTER.ACTIVE), true);
  assert.equal(taskMatchesFilter(needsInputChoice, FILTER.ACTIVE), true);
  assert.equal(taskMatchesFilter(blocked, FILTER.ACTIVE), true);
  assert.equal(taskMatchesFilter(failed, FILTER.ACTIVE), false);
  assert.equal(taskMatchesFilter(completed, FILTER.ACTIVE), false);

  // Needs Attention: needs assignment, needs input, blocked, failed, timed out.
  assert.equal(taskMatchesFilter(unassigned, FILTER.NEEDS_ATTENTION), true);
  assert.equal(taskMatchesFilter(needsInputChoice, FILTER.NEEDS_ATTENTION), true);
  assert.equal(taskMatchesFilter(blocked, FILTER.NEEDS_ATTENTION), true);
  assert.equal(taskMatchesFilter(failed, FILTER.NEEDS_ATTENTION), true);
  assert.equal(taskMatchesFilter(timedOut, FILTER.NEEDS_ATTENTION), true);
  assert.equal(taskMatchesFilter(running, FILTER.NEEDS_ATTENTION), false);

  // Actionable includes everything that can be acted on; terminal-quiet states excluded.
  assert.equal(taskMatchesFilter(running, FILTER.ACTIONABLE), true);
  assert.equal(taskMatchesFilter(failed, FILTER.ACTIONABLE), true);
  assert.equal(taskMatchesFilter(cancelled, FILTER.ACTIONABLE), false);
  assert.equal(taskMatchesFilter(completed, FILTER.ACTIONABLE), false);
});

test('resolveTaskCounts sums the same categories as the rows (FR45)', () => {
  const tasks = [unassigned, ready, running, needsInputChoice, blocked, failed, timedOut, completed, cancelled];
  const counts = resolveTaskCounts(tasks);
  assert.equal(counts[FILTER.ALL], tasks.length);
  assert.equal(counts[FILTER.COMPLETED], 1);
  // Needs Attention: unassigned, needsInput, blocked, failed, timedOut = 5
  assert.equal(counts[FILTER.NEEDS_ATTENTION], 5);
  // Active: ready, running, needsInput, blocked = 4
  assert.equal(counts[FILTER.ACTIVE], 4);
});

test('sortTasksForDrawer: intervention → running → failed/timed → ready → terminal (FR24)', () => {
  const shuffled = [completed, ready, running, failed, blocked, needsInputChoice, cancelled];
  const order = sortTasksForDrawer(shuffled).map(t => resolveTaskState(t));
  assert.deepEqual(order, [
    PRESENTATION_STATE.NEEDS_INPUT,
    PRESENTATION_STATE.BLOCKED,
    PRESENTATION_STATE.RUNNING,
    PRESENTATION_STATE.FAILED,
    PRESENTATION_STATE.READY,
    PRESENTATION_STATE.COMPLETED,
    PRESENTATION_STATE.CANCELLED
  ]);
});

test('sort ties break by most-recent activity then ascending id (FR24)', () => {
  const older = { id: 'b-older', status: 'in_progress', to: 'x', updated_at: '2026-01-01T00:00:00Z' };
  const newer = { id: 'a-newer', status: 'in_progress', to: 'x', updated_at: '2026-06-01T00:00:00Z' };
  const sorted = sortTasksForDrawer([older, newer]);
  assert.equal(sorted[0].id, 'a-newer', 'more recent activity sorts first');

  const sameTime = [
    { id: 'z', status: 'in_progress', to: 'x', updated_at: '2026-06-01T00:00:00Z' },
    { id: 'a', status: 'in_progress', to: 'x', updated_at: '2026-06-01T00:00:00Z' }
  ];
  assert.equal(sortTasksForDrawer(sameTime)[0].id, 'a', 'id breaks the tie ascending');
});

test('sortTasksForDrawer does not mutate its input', () => {
  const input = [completed, needsInputChoice];
  const copy = input.slice();
  sortTasksForDrawer(input);
  assert.deepEqual(input, copy);
});

test('taskShortId is stable from the task id, not the array position (FR15)', () => {
  assert.equal(taskShortId({ id: 'task-abcdef' }), '#CDEF');
  assert.equal(taskShortId({ id: 'task-abcdef' }), taskShortId({ id: 'task-abcdef' }));
  assert.equal(taskShortId({ id: '' }), '');
  assert.equal(taskShortId(null), '');
});

test('taskLatestActivityAt falls back to created_at then 0', () => {
  assert.equal(taskLatestActivityAt({ created_at: '2026-06-01T00:00:00Z' }) > 0, true);
  assert.equal(taskLatestActivityAt({}), 0);
  assert.equal(taskLatestActivityAt(null), 0);
});

test('countCategories always include ALL', () => {
  [unassigned, ready, running, completed, cancelled, unknown].forEach(t => {
    assert.ok(resolveTaskPresentation(t).countCategories.includes(FILTER.ALL));
  });
});

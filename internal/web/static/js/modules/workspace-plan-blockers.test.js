import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  blockersFor,
  POLL_INTERVAL_MS,
  primaryBlocker,
  shouldPoll
} from './workspace-plan-blockers.js';

function planFixture(overrides = {}) {
  return {
    id: 'plan_1',
    studio_id: 'ws-1',
    status: 'approved',
    progress: { total: 2, completed: 0, running: 0, failed: 0, waiting_for_slot: 0 },
    available_agents: ['builder'],
    draft: {
      groups: [{ id: 'grp-1', items: [{ id: 'itm-1', assignee: 'builder' }] }]
    },
    ...overrides
  };
}

// --- Each blocker names its own cause (FR-156) -----------------------------

test('a plan awaiting approval says so and points at the review', () => {
  const blocker = primaryBlocker(planFixture({ status: 'in_review' }));
  assert.equal(blocker.kind, 'approval');
  assert.match(blocker.reason, /waiting for your approval/);
  // And it says nothing was created, because that is the question a user
  // actually has at this moment.
  assert.match(blocker.reason, /Nothing has been created/);
  assert.equal(blocker.action.href, '/workspaces/ws-1/plans/plan_1');
});

test('a plan awaiting answers is distinct from one awaiting approval', () => {
  const input = primaryBlocker(planFixture({ status: 'needs_input' }));
  const approval = primaryBlocker(planFixture({ status: 'in_review' }));
  assert.equal(input.kind, 'input');
  assert.notEqual(input.reason, approval.reason);
  assert.notEqual(input.action.label, approval.action.label);
});

test('a missing agent is named, with adding it as the action', () => {
  const blocker = primaryBlocker(
    planFixture({
      available_agents: ['someone-else'],
      draft: { groups: [{ id: 'grp-1', items: [{ id: 'itm-1', assignee: 'builder' }] }] }
    })
  );
  assert.equal(blocker.kind, 'capability');
  assert.match(blocker.reason, /builder/);
  assert.match(blocker.action.label, /Add the missing agent/);
});

test('failed work reports how much failed', () => {
  const blocker = primaryBlocker(
    planFixture({ status: 'paused', progress: { failed: 2, waiting_for_slot: 0 } })
  );
  assert.equal(blocker.kind, 'failure');
  assert.match(blocker.reason, /2 task\(s\) failed/);
});

// Waiting for the slot resolves itself, so it explains rather than demands.
test('waiting for the workspace slot explains rather than demanding action', () => {
  const blocker = primaryBlocker(
    planFixture({ status: 'approved', progress: { failed: 0, waiting_for_slot: 1 } })
  );
  assert.equal(blocker.kind, 'slot');
  assert.match(blocker.reason, /starts when that finishes/);
});

// --- Precedence (the point of ordering) ------------------------------------

// A plan missing an agent AND awaiting approval must lead with the agent:
// approving would not start anything, so leading with approval would send the
// user to do something that changes nothing.
test('a missing agent outranks a pending approval', () => {
  const blocker = primaryBlocker(
    planFixture({
      status: 'in_review',
      available_agents: [],
      draft: { groups: [{ id: 'grp-1', items: [{ id: 'itm-1', assignee: 'builder' }] }] }
    })
  );
  assert.equal(blocker.kind, 'capability');
});

test('a failure outranks a pending approval', () => {
  const blocker = primaryBlocker(
    planFixture({ status: 'in_review', progress: { failed: 1, waiting_for_slot: 0 } })
  );
  assert.equal(blocker.kind, 'failure');
});

test('every blocker carries exactly one primary action', () => {
  const stuck = planFixture({
    status: 'in_review',
    available_agents: [],
    progress: { failed: 1, waiting_for_slot: 1 },
    draft: { groups: [{ id: 'grp-1', items: [{ id: 'itm-1', assignee: 'builder' }] }] }
  });
  const blockers = blockersFor(stuck);
  assert.ok(blockers.length > 1, 'fixture should produce several blockers');
  for (const blocker of blockers) {
    assert.ok(blocker.action?.label, `${blocker.kind} has no action label`);
    assert.ok(blocker.reason, `${blocker.kind} has no reason`);
  }
});

// --- Nothing wrong means nothing shown -------------------------------------

test('a healthy plan reports no blocker', () => {
  assert.equal(primaryBlocker(planFixture({ status: 'executing' })), null);
  assert.deepEqual(blockersFor(planFixture({ status: 'executing' })), []);
});

test('a completed plan reports no blocker', () => {
  assert.equal(primaryBlocker(planFixture({ status: 'completed' })), null);
});

// An unknown roster flags nothing. Reporting every assignee as missing because
// nobody could read the list would be worse than saying nothing.
test('an unknown agent roster raises no capability blocker', () => {
  const plan = planFixture({ available_agents: undefined });
  assert.equal(blockersFor(plan).filter(b => b.kind === 'capability').length, 0);
});

test('an unassigned item is not reported as a missing agent', () => {
  const plan = planFixture({
    available_agents: ['builder'],
    draft: { groups: [{ id: 'grp-1', items: [{ id: 'itm-1', assignee: '' }] }] }
  });
  assert.equal(blockersFor(plan).filter(b => b.kind === 'capability').length, 0);
});

// --- Bounded polling (FR-155) ----------------------------------------------

test('only plans whose progress can still change are polled', () => {
  for (const status of ['executing', 'approved', 'paused']) {
    assert.equal(shouldPoll({ status }), true, `${status} should poll`);
  }
  for (const status of ['draft', 'needs_input', 'in_review', 'completed', 'failed', 'cancelled']) {
    assert.equal(shouldPoll({ status }), false, `${status} should not poll`);
  }
});

test('the poll interval is bounded rather than a tight loop', () => {
  assert.ok(POLL_INTERVAL_MS >= 2000, 'polling faster than 2s costs a request per tab per second');
  assert.ok(POLL_INTERVAL_MS <= 15000, 'polling slower than 15s makes finished work look stuck');
});

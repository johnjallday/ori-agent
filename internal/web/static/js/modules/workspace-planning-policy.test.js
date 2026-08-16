import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  enforcedLine,
  enforcedState,
  guidanceLines,
  policySummary,
  unavailableControls
} from './workspace-planning-policy.js';

function policyFixture(overrides = {}) {
  return {
    version: 1,
    profile: 'software_project',
    preset: 'planner',
    planning_enabled: true,
    guidance: {
      style: 'feature',
      clarification_depth: 'standard',
      preferred_artifacts: ['prd', 'task_list'],
      detail_level: 'standard',
      tone: 'practical'
    },
    enforced: [
      {
        key: 'plan_approval',
        label: 'Explicit plan approval',
        description: 'No tasks are created until you approve one exact plan version.',
        enabled: true,
        available: true
      },
      {
        key: 'safe_branch',
        label: 'Branch precondition',
        description: 'Code execution is blocked on a disallowed branch.',
        enabled: true,
        available: false,
        reason: 'not_a_repository',
        detail: "This workspace's folder is not a version-controlled repository."
      },
      {
        key: 'note_creation',
        label: 'Save outputs as notes',
        description: 'Useful results are written to workspace notes.',
        enabled: false,
        available: true
      }
    ],
    ...overrides
  };
}

// --- Guidance never promises (FR-129) --------------------------------------

test('every guidance line marks itself as a request, not a guarantee', () => {
  const lines = guidanceLines(policyFixture().guidance);
  assert.ok(lines.length > 0, 'no guidance rendered');
  for (const line of lines) {
    assert.match(line, /asked, not enforced/, `guidance line reads as a promise: ${line}`);
  }
});

test('guidance uses none of the words reserved for enforcement', () => {
  const lines = guidanceLines(policyFixture().guidance).join(' ').toLowerCase();
  for (const forbidden of ['required', 'guaranteed', 'will be', 'must ']) {
    assert.ok(!lines.includes(forbidden), `guidance used enforcement language: ${forbidden}`);
  }
});

test('empty guidance renders nothing rather than empty labels', () => {
  assert.deepEqual(guidanceLines({}), []);
  assert.deepEqual(guidanceLines(null), []);
});

// --- Enforcement says what it does (FR-128) --------------------------------

test('an active control says it is on and what it does', () => {
  const line = enforcedLine(policyFixture().enforced[0]);
  assert.match(line, /^Explicit plan approval — on\./);
  assert.match(line, /approve one exact plan version/);
});

// An unavailable control explains the missing dependency rather than silently
// looking like an ordinary "off".
test('an unavailable control explains what is missing', () => {
  const line = enforcedLine(policyFixture().enforced[1]);
  assert.match(line, /unavailable/);
  assert.match(line, /not a version-controlled repository/);
  assert.ok(!/— on\./.test(line), 'an unenforceable control read as active');
});

test('an available but disabled control reads as off, not unavailable', () => {
  const line = enforcedLine(policyFixture().enforced[2]);
  assert.match(line, /— off\./);
  assert.ok(!/unavailable/.test(line), 'a disabled control was reported as unavailable');
});

// The three states must be distinguishable without color.
test('state is reported as text, not left to styling', () => {
  const [approval, branch, notes] = policyFixture().enforced;
  assert.equal(enforcedState(approval), 'on');
  assert.equal(enforcedState(branch), 'unavailable');
  assert.equal(enforcedState(notes), 'off');
});

test('unavailable controls are listed for disabling rather than hiding', () => {
  const blocked = unavailableControls(policyFixture());
  assert.equal(blocked.length, 1);
  assert.equal(blocked[0].key, 'safe_branch');
});

// --- The headline is honest about how much is real -------------------------

test('the summary counts active checks and names what is unavailable', () => {
  const summary = policySummary(policyFixture());
  assert.match(summary, /1 of 3 checks active/);
  assert.match(summary, /1 unavailable/);
});

test('a policy with no controls says so rather than claiming zero of zero', () => {
  assert.equal(policySummary({ enforced: [] }), 'No planning policy is configured.');
  assert.equal(policySummary(null), 'No planning policy is configured.');
});

// A workspace that CAN enforce everything reports no unavailable clause, so the
// line does not imply a problem where there is none.
test('a fully capable workspace reports no unavailable clause', () => {
  const policy = policyFixture();
  policy.enforced = policy.enforced.map(control => ({ ...control, available: true }));
  const summary = policySummary(policy);
  assert.match(summary, /2 of 3 checks active/);
  assert.ok(!/unavailable/.test(summary), `summary implied a problem: ${summary}`);
});

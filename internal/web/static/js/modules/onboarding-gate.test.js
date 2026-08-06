import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ONBOARDING_GATE_READY,
  ONBOARDING_GATE_REQUIRED,
  ONBOARDING_GATE_UNAVAILABLE,
  loadInitialWorkspaceTree,
  loadOnboardingStatus,
  onboardingGateDecision,
  resetOnboardingGateForTests
} from './onboarding-gate.js';

test('onboardingGateDecision permits hydration only after onboarding', () => {
  assert.deepEqual(onboardingGateDecision({ needs_onboarding: true }), {
    state: ONBOARDING_GATE_REQUIRED,
    allowWorkspaceHydration: false,
    message: 'Finish setup to load your workspaces.'
  });
  assert.deepEqual(onboardingGateDecision({ needs_onboarding: false }), {
    state: ONBOARDING_GATE_READY,
    allowWorkspaceHydration: true,
    message: ''
  });
});

test('onboardingGateDecision fails closed for missing or ambiguous status', () => {
  for (const status of [null, undefined, {}, { needs_onboarding: 'false' }]) {
    const decision = onboardingGateDecision(status);
    assert.equal(decision.state, ONBOARDING_GATE_UNAVAILABLE);
    assert.equal(decision.allowWorkspaceHydration, false);
  }
});

test('loadOnboardingStatus deduplicates concurrent callers', async () => {
  let calls = 0;
  resetOnboardingGateForTests(async url => {
    calls += 1;
    assert.equal(url, '/api/onboarding/status');
    return { ok: true, json: async () => ({ needs_onboarding: true }) };
  });

  const [first, second] = await Promise.all([loadOnboardingStatus(), loadOnboardingStatus()]);
  assert.equal(calls, 1);
  assert.equal(first.needs_onboarding, true);
  assert.equal(second, first);
});

test('loadOnboardingStatus converts request and payload failures into unavailable status', async () => {
  resetOnboardingGateForTests(async () => ({ ok: false, status: 503 }));
  const httpFailure = await loadOnboardingStatus();
  assert.equal(httpFailure.needs_onboarding, null);
  assert.equal(httpFailure.onboarding_status_unavailable, true);
  assert.match(httpFailure.onboarding_status_error, /503/);

  resetOnboardingGateForTests(async () => ({ ok: true, json: async () => ({}) }));
  const invalidPayload = await loadOnboardingStatus();
  assert.equal(invalidPayload.needs_onboarding, null);
  assert.match(invalidPayload.onboarding_status_error, /invalid/i);
});

test('loadOnboardingStatus force retries an unavailable cached result', async () => {
  let calls = 0;
  resetOnboardingGateForTests(async () => {
    calls += 1;
    if (calls === 1) throw new Error('temporary outage');
    return { ok: true, json: async () => ({ needs_onboarding: false, completed: true }) };
  });

  const unavailable = await loadOnboardingStatus();
  assert.equal(onboardingGateDecision(unavailable).state, ONBOARDING_GATE_UNAVAILABLE);
  const ready = await loadOnboardingStatus({ force: true });
  assert.equal(onboardingGateDecision(ready).state, ONBOARDING_GATE_READY);
  assert.equal(calls, 2);
});

test('loadInitialWorkspaceTree shares one validated bootstrap payload', async () => {
  resetOnboardingGateForTests();
  let calls = 0;
  const fetchImpl = async url => {
    calls += 1;
    assert.equal(url, '/api/workspaces?tree=true');
    return { ok: true, json: async () => ({ folders: [{ id: 'shared' }] }) };
  };

  const [first, second] = await Promise.all([
    loadInitialWorkspaceTree({ fetchImpl }),
    loadInitialWorkspaceTree({ fetchImpl })
  ]);

  assert.equal(calls, 1);
  assert.equal(second, first);
  assert.deepEqual(first.folders, [{ id: 'shared' }]);
});

test('loadInitialWorkspaceTree clears a failed bootstrap so retry can recover', async () => {
  resetOnboardingGateForTests();
  await assert.rejects(
    loadInitialWorkspaceTree({
      fetchImpl: async () => ({ ok: true, json: async () => ({ folders: null }) })
    }),
    /invalid/i
  );

  const recovered = await loadInitialWorkspaceTree({
    fetchImpl: async () => ({ ok: true, json: async () => ({ folders: [] }) })
  });
  assert.deepEqual(recovered.folders, []);
});

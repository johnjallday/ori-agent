import { test } from 'node:test';
import assert from 'node:assert/strict';
import { beginHQAttempt, claimWorkspaceArrival, handoffURL } from './hq-guided-attempt.js';
import {
  hasWorkspaceVisit,
  openingExistingWorkspace,
  openingReviewedWorkspace,
  rememberWorkspaceReview,
  verifyWorkspaceVisit
} from './hq-workspace-outcome.js';

function storage() {
  const map = new Map();
  return {
    getItem: k => map.get(k),
    setItem: (k, v) => map.set(k, v),
    removeItem: k => map.delete(k)
  };
}

test('only the chosen, available anchor on the actual detail page completes this HQ', async t => {
  const original = Object.fromEntries(
    ['fetch', 'sessionStorage', 'localStorage'].map(k => [
      k,
      Object.getOwnPropertyDescriptor(globalThis, k)
    ])
  );
  t.after(() => {
    for (const [k, d] of Object.entries(original)) {
      if (d) Object.defineProperty(globalThis, k, d);
      else delete globalThis[k];
    }
  });
  for (const scenario of [
    'duplicate',
    'review',
    'wrong-operation',
    'wrong-result',
    'wrong-anchor',
    'managed',
    'cancel',
    'stale'
  ]) {
    for (const key of ['sessionStorage', 'localStorage'])
      Object.defineProperty(globalThis, key, { value: storage(), configurable: true });
    const hint = beginHQAttempt('hq', 'workspace');
    claimWorkspaceArrival(handoffURL('/', hint).slice(1));
    const reference = { id: 'op', digest: 'exact' };
    const ws = {
      id: 'owner',
      shape: 'anchored',
      shared_data: { workspace_creation_operation_id: 'op' },
      anchor: { path: '/chosen', directory_reference_id: 'anchor', missing: false }
    };
    if (scenario === 'review' || scenario === 'wrong-operation') {
      rememberWorkspaceReview(hint, reference, '/chosen');
      assert.equal(await verifyWorkspaceVisit('owner', ws), false, 'create without open');
      openingReviewedWorkspace(
        { ...reference, digest: scenario === 'review' ? 'exact' : 'other' },
        'owner'
      );
    } else if (scenario !== 'cancel') openingExistingWorkspace(hint, 'owner', '/chosen');
    if (scenario === 'stale') beginHQAttempt('hq', 'workspace');
    if (scenario === 'managed') ws.shape = 'managed';
    if (scenario === 'wrong-result') ws.id = 'other';
    if (scenario === 'wrong-anchor') ws.anchor.path = '/other';
    globalThis.fetch = async url => {
      assert.equal(url, '/api/workspaces/owner');
      return { ok: true, json: async () => ws };
    };
    const expected = ['duplicate', 'review'].includes(scenario);
    assert.equal(await verifyWorkspaceVisit(ws.id, ws), expected, scenario);
    assert.equal(hasWorkspaceVisit('hq'), expected);
    assert.equal(hasWorkspaceVisit('other-hq'), false);
    assert.equal(await verifyWorkspaceVisit(ws.id, ws), false, 'already consumed');
  }
});

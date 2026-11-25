import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  beginHQAttempt,
  claimWorkspaceArrival,
  currentHQAttempt,
  guidedReturnURL,
  handoffURL
} from './hq-guided-attempt.js';

test('return navigation is scoped to the current tab attempt, path, designated HQ and bounded age', () => {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'sessionStorage');
  let value;
  Object.defineProperty(globalThis, 'sessionStorage', {
    configurable: true,
    value: { setItem: (_, v) => (value = v), getItem: () => value }
  });
  try {
    const hint = beginHQAttempt('ws-1', 'folder');
    const status = { valid: true, workspace_id: 'ws-1', workspace: { folder_slug: 'hq' } };
    assert.ok(
      guidedReturnURL(hint, status).startsWith('/workspaces/hq?mode=details&guided=folder&attempt=')
    );
    assert.equal(currentHQAttempt(hint.hq, 'email', hint.attempt), null);
    assert.equal(guidedReturnURL(hint, { ...status, workspace_id: 'another' }), '');
    assert.equal(
      guidedReturnURL(hint, { ...status, workspace: { folder_slug: '//other.site' } }),
      ''
    );
    beginHQAttempt('ws-1', 'folder');
    assert.equal(guidedReturnURL(hint, status), '');
    value = JSON.stringify({ ...hint, at: Date.now() - 86400000 });
    assert.equal(guidedReturnURL(hint, status), '');
    const workspace = beginHQAttempt('ws-1', 'workspace');
    const search = handoffURL('/', workspace).slice(1);
    assert.equal(claimWorkspaceArrival(search.replace('ws-1', 'ws-2')), null);
    assert.deepEqual(claimWorkspaceArrival(search), workspace);
    assert.equal(claimWorkspaceArrival(search), null, 'an old link cannot arm another open');
    value = JSON.stringify({ ...workspace, at: Date.now() - 16 * 60000 });
    assert.equal(claimWorkspaceArrival(search), null);
  } finally {
    if (original) Object.defineProperty(globalThis, 'sessionStorage', original);
    else delete globalThis.sessionStorage;
  }
});

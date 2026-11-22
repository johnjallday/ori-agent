import { test } from 'node:test';
import assert from 'node:assert/strict';
import { beginHQAttempt, currentHQAttempt, guidedReturnURL } from './hq-guided-attempt.js';

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
  } finally {
    if (original) Object.defineProperty(globalThis, 'sessionStorage', original);
    else delete globalThis.sessionStorage;
  }
});

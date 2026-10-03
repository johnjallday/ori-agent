import test from 'node:test';
import assert from 'node:assert/strict';

import { ageLabel, errorView, kindLabel, listView } from './email-needs-you.js';

test('a workspace without a mailbox hides the panel', () => {
  assert.deepEqual(errorView(409, { error: 'not_linked' }), { hidden: true });
  assert.deepEqual(errorView(404, { error: 'workspace_unavailable' }), { hidden: true });
  assert.deepEqual(errorView(503, { error: 'unavailable' }), { hidden: true });
  assert.notDeepEqual(errorView(503, { error: 'busy' }), { hidden: true });
});

test('each failure offers the one action that fixes it', () => {
  assert.equal(errorView(409, { error: 'vault_locked', message: 'Unlock' }).action.href, '/vaults');
  assert.equal(
    errorView(409, { error: 'reconnect', message: 'Sign in' }).action.href,
    '/?setup=email'
  );
  assert.equal(errorView(409, { error: 'account_unavailable' }).action.href, '/?setup=email');
  assert.equal(errorView(503, { error: 'busy', message: 'Busy' }).action.retry, true);
  assert.equal(errorView(0, {}).action.retry, true);
});

test('the server’s message is shown, with a plain fallback', () => {
  assert.equal(
    errorView(502, { error: 'unreachable', message: 'Gmail is not answering.' }).message,
    'Gmail is not answering.'
  );
  assert.match(errorView(500, {}).message, /couldn’t read your mail/);
});

test('the list view counts only what needs the user', () => {
  const view = listView({
    needs_you: [{ thread_id: 'a' }, { thread_id: 'b' }],
    fyi: [{ thread_id: 'c' }],
    ignorable: [],
    explained: true
  });
  assert.equal(view.count, 2);
  assert.equal(view.empty, false);
  assert.equal(view.fyi.length, 1);
  assert.equal(view.note, '');
});

test('rules-only reasons say so, but only when there is something to explain', () => {
  assert.match(listView({ needs_you: [{ thread_id: 'a' }], explained: false }).note, /rules/);
  assert.equal(listView({ needs_you: [], explained: false }).note, '');
});

test('a malformed list draws as empty', () => {
  const view = listView({ needs_you: 'nope' });
  assert.equal(view.empty, true);
  assert.deepEqual(view.fyi, []);
  assert.equal(listView(null).count, 0);
});

test('ages are compact', () => {
  const now = Date.parse('2026-10-04T12:00:00Z');
  assert.equal(ageLabel(now, '2026-10-04T11:59:50Z'), '1m');
  assert.equal(ageLabel(now, '2026-10-04T11:15:00Z'), '45m');
  assert.equal(ageLabel(now, '2026-10-04T09:00:00Z'), '3h');
  assert.equal(ageLabel(now, '2026-10-02T12:00:00Z'), '2d');
  assert.equal(ageLabel(now, 'not a date'), '');
  assert.equal(ageLabel(now, ''), '');
});

test('kind chips name only known kinds', () => {
  assert.equal(kindLabel('deadline'), 'Deadline');
  assert.equal(kindLabel('decision'), 'Decision');
  assert.equal(kindLabel('<script>'), '');
  assert.equal(kindLabel('constructor'), '');
});

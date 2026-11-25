import test from 'node:test';
import assert from 'node:assert/strict';
import { ticketForSuggestion } from './inbox-review.js';

test('ticket link is the actual accepted target, never a guessed fallback', () => {
  const workspaces = [
    { id: 'a', slug: 'garden' },
    { id: 'b', slug: 'house' }
  ];
  const item = { state: 'accepted', workspace_id: 'b', ticket_id: 't1' };
  assert.equal(ticketForSuggestion(item, workspaces), '/workspaces/house?ticket=t1');
  for (const state of ['claiming', 'pending', 'dismissed'])
    assert.equal(ticketForSuggestion({ ...item, state }, workspaces), '');
  assert.equal(ticketForSuggestion(item, [workspaces[0]]), '');
});

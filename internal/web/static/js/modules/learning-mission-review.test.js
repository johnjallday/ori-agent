import { test } from 'node:test';
import assert from 'node:assert/strict';
import { learningForTicket } from './learning-mission-review.js';

const row = {
  id: 'mission',
  state: 'active',
  definition: { studio_id: 'workspace' },
  progress: { ticket_id: 'ticket', run_id: 'exact-run' }
};

test('review handoff requires both exact workspace and Ticket identity', () => {
  const status = { instances: [row] };
  assert.equal(learningForTicket(status, 'workspace', 'ticket'), row);
  assert.equal(learningForTicket(status, 'foreign', 'ticket'), null);
  assert.equal(learningForTicket(status, 'workspace', 'other'), null);
});

test('stopped or cleared learning does not manufacture a new result review', () => {
  assert.equal(
    learningForTicket({ instances: [{ ...row, state: 'stopped' }] }, 'workspace', 'ticket'),
    null
  );
  assert.equal(learningForTicket({ instances: [] }, 'workspace', 'ticket'), null);
  const complete = { ...row, state: 'completed' };
  assert.equal(learningForTicket({ instances: [complete] }, 'workspace', 'ticket'), complete);
});

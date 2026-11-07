import { test } from 'node:test';
import assert from 'node:assert/strict';
import { learningForTicket, observationForTicket } from './learning-mission-review.js';

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

test('ordinary review handoff requires its own exact scoped host observation', () => {
  const observed = { id: 'claim', studio_id: 'workspace', ticket_id: 'ticket', ready: true };
  assert.equal(observationForTicket({ observations: [observed] }, 'workspace', 'ticket'), observed);
  assert.equal(observationForTicket({ observations: [observed] }, 'foreign', 'ticket'), null);
  assert.equal(observationForTicket({ instances: [row] }, 'workspace', 'ticket'), null);
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

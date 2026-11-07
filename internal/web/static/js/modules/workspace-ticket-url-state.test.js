import test from 'node:test';
import assert from 'node:assert/strict';
import {
  readTicketURLState,
  writeTicketURLState,
  ticketURLStatesEqual
} from './workspace-ticket-url-state.js';

test('existing ticket and list/filter arrivals retain their owning vocabulary', () => {
  assert.deepEqual(readTicketURLState('?ticket=stable-id&tickets=review'), {
    ticket: 'stable-id',
    tickets: 'review'
  });
  assert.deepEqual(readTicketURLState('mode=details&panel=tasks&task=legacy'), {
    ticket: '',
    tickets: ''
  });
  assert.equal(writeTicketURLState('', { tickets: 'all' }), 'tickets=all');
  assert.equal(
    writeTicketURLState('', { ticket: 'stable-id', tickets: 'all' }),
    'ticket=stable-id'
  );
});

test('mode changes clear only canonical Ticket keys, not stations, legacy adapters or other owners', () => {
  const source =
    'mode=map&ticket=t&tickets=backlog&station=memory&panel=tasks&task=legacy&run=r&other=keep';
  assert.equal(
    writeTicketURLState(source, {}),
    'mode=map&station=memory&panel=tasks&task=legacy&run=r&other=keep'
  );
  assert.equal(ticketURLStatesEqual({ ticket: 't', tickets: 'all' }, { ticket: 't' }), true);
  assert.equal(ticketURLStatesEqual({ tickets: 'all' }, {}), false);
});

test('opaque IDs and multi-state filters are encoded, never interpreted as markup or routes', () => {
  const state = { ticket: '../other?<img>', tickets: 'ready,review' };
  assert.deepEqual(readTicketURLState(writeTicketURLState('', state)), state);
  assert.ok(!writeTicketURLState('', state).includes('<img>'));
});

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { chooserWorkspaces, makeTicket, relatedTicketView } from './follow-up-ticket.js';

function replying(status, body) {
  const calls = [];
  const fetchImpl = async (url, options) => {
    calls.push({ url, body: JSON.parse(options.body) });
    return { ok: status < 400, status, json: async () => body };
  };
  return { calls, fetchImpl };
}

test('a linked follow-up shows the ticket number, its state and a link', () => {
  assert.deepEqual(
    relatedTicketView({
      id: 't1',
      display_number: 'T-3',
      state_label: 'Review',
      href: '/workspaces/album?ticket=t1'
    }),
    { id: 't1', line: 'Ticket T-3 · Review', href: '/workspaces/album?ticket=t1' }
  );
  assert.equal(relatedTicketView(null), null);
  assert.equal(relatedTicketView({}), null);
});

test('the chooser leaves out groups, HQ and workspaces that are not active', () => {
  const chosen = chooserWorkspaces({
    folders: [
      { id: 'a', name: 'Album', status: 'active' },
      { id: 'g', name: 'Group', kind: 'group' },
      { id: 'h', name: 'HQ', designation: 'personal_hq' },
      { id: 't', name: 'Trashed', status: 'trashed' }
    ]
  });
  assert.deepEqual(chosen, [{ id: 'a', name: 'Album' }]);
});

test('makeTicket reports the ticket and whether it was new', async () => {
  const { calls, fetchImpl } = replying(201, { ticket: { id: 't1' }, created: true });
  const result = await makeTicket(fetchImpl, 'f1');
  assert.deepEqual(result, { ticket: { id: 't1' }, created: true });
  assert.deepEqual(calls[0].body, { id: 'f1', workspace_id: '' });
});

test('an HQ follow-up asks for a workspace instead of failing', async () => {
  const { fetchImpl } = replying(400, { details: { field: 'workspace_id' }, message: 'Choose' });
  assert.deepEqual(await makeTicket(fetchImpl, 'f1'), { choose: true });
});

test('any other refusal carries the message to show', async () => {
  const { fetchImpl } = replying(404, { message: 'Workspace not found' });
  await assert.rejects(makeTicket(fetchImpl, 'f1', 'w'), /Workspace not found/);
});

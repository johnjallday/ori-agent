import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// The ticket client's two additions for the one work count (PRD 760 FR-6,
// FR-11): a list response carries the workspace-wide counts, and a create,
// move or delete announces itself so the header tile can refresh.

const source = readFileSync(new URL('./workspace-hub-tickets.js', import.meta.url), 'utf8');

class FakeCustomEvent {
  constructor(type, init = {}) {
    this.type = type;
    this.detail = init.detail;
  }
}

function loadTickets(responder) {
  const announced = [];
  const window = {
    fetch: async (url, options = {}) => {
      const result = responder ? await responder(url, options) : { ok: true, body: {} };
      return {
        ok: result.ok !== false,
        status: result.status || (result.ok === false ? 400 : 200),
        json: async () => result.body
      };
    },
    dispatchEvent: event => announced.push({ type: event.type, detail: event.detail })
  };
  vm.runInNewContext(
    source,
    { console, window, URLSearchParams, JSON, CustomEvent: FakeCustomEvent },
    { filename: 'workspace-hub-tickets.js' }
  );
  return { api: window.WorkspaceHubTickets, announced };
}

const ticket = { id: 'tkt-1', title: 'A thing', state: 'ready' };

test('list returns the workspace-wide counts beside the tickets', async () => {
  const { api } = loadTickets(() => ({
    body: {
      tickets: [ticket],
      count: 1,
      total: 9,
      counts: { backlog: 4, ready: 3, in_progress: 1, review: 1, done: 0, cancelled: 0 }
    }
  }));
  const result = await api.api.list('ws-1', { limit: 1 });
  assert.equal(result.count, 1);
  assert.equal(result.counts.ready, 3);
  assert.equal(result.counts.backlog, 4);
});

test('list reports null counts when the server sent none', async () => {
  const { api } = loadTickets(() => ({ body: { tickets: [], count: 0 } }));
  assert.equal((await api.api.list('ws-1')).counts, null);

  const { api: garbled } = loadTickets(() => ({ body: { tickets: [], counts: 'nope' } }));
  assert.equal((await garbled.api.list('ws-1')).counts, null, 'a non-object is not counts');
});

test('creating a ticket announces the change for its workspace', async () => {
  const { api, announced } = loadTickets(() => ({ body: ticket }));
  await api.api.create('ws-1', { state: 'ready', title: 'A thing' });
  assert.deepEqual(JSON.parse(JSON.stringify(announced)), [
    { type: 'ori:tickets-changed', detail: { studioId: 'ws-1' } }
  ]);
  assert.equal(api.CHANGED_EVENT, 'ori:tickets-changed');
});

test('a transition, a delete and a create from a note each announce the change', async () => {
  const { api, announced } = loadTickets(() => ({ body: ticket }));
  await api.api.transition('ws-1', 'tkt-1', 'in_progress');
  await api.api.remove('ws-1', 'tkt-1');
  await api.api.createFromNote('ws-1', 'note-1', { state: 'backlog', title: 'From a note' });
  assert.equal(announced.length, 3);
  assert.ok(announced.every(event => event.detail.studioId === 'ws-1'));
});

test('reads and content edits do not announce a change', async () => {
  const { api, announced } = loadTickets(() => ({ body: { tickets: [ticket], ...ticket } }));
  await api.api.list('ws-1');
  await api.api.get('ws-1', 'tkt-1');
  await api.api.update('ws-1', 'tkt-1', { title: 'Renamed' });
  assert.equal(announced.length, 0, 'none of these can change a count');
});

test('create sends the assignee, the start intent and the key, and keeps the start outcome', async () => {
  let sent = null;
  const { api } = loadTickets((_url, options) => {
    sent = JSON.parse(options.body);
    return { body: { ...ticket, start: { started: false, error: 'workspace is paused' } } };
  });
  const created = await api.api.create('ws-1', {
    state: 'ready',
    title: 'Write',
    assignee: 'Writer',
    start: true,
    idempotencyKey: 'k1'
  });
  assert.equal(sent.assignee, 'Writer');
  assert.equal(sent.start, true);
  assert.equal(sent.idempotency_key, 'k1');
  assert.equal(created.start.started, false);
  assert.equal(created.start.error, 'workspace is paused');
});

test('a create with no start intent carries no start outcome', async () => {
  const { api } = loadTickets(() => ({ body: ticket }));
  const created = await api.api.create('ws-1', { state: 'backlog', title: 'X' });
  assert.equal(created.start, undefined);
});

test('ticketEditChanges turns the edit form into an update carrying the version', () => {
  const { api } = loadTickets();
  const changes = api.ticketEditChanges(
    {
      title: ' New ',
      description: 'Body',
      priority: '2',
      tags: 'a, b,, ',
      due: '2026-12-01',
      reference: ' https://x.test '
    },
    { version: 4 }
  );
  assert.deepEqual(JSON.parse(JSON.stringify(changes)), {
    title: 'New',
    description: 'Body',
    priority: 2,
    tags: ['a', 'b'],
    dueDate: '2026-12-01T00:00:00Z',
    referenceUrl: 'https://x.test',
    version: 4
  });
  assert.equal(api.ticketEditChanges({ due: '' }, { version: 1 }).dueDate, null, 'clearing due');
});

test('a failed create announces nothing', async () => {
  const { api, announced } = loadTickets(() => ({
    ok: false,
    status: 400,
    body: { message: 'title is required' }
  }));
  await assert.rejects(api.api.create('ws-1', { state: 'ready', title: '' }));
  assert.equal(announced.length, 0);
});

test('a listener that throws never turns a saved ticket into an error', async () => {
  const window = {
    fetch: async () => ({ ok: true, status: 200, json: async () => ticket }),
    dispatchEvent: () => {
      throw new Error('listener blew up');
    }
  };
  vm.runInNewContext(
    source,
    { console, window, URLSearchParams, JSON, CustomEvent: FakeCustomEvent },
    { filename: 'workspace-hub-tickets.js' }
  );
  const created = await window.WorkspaceHubTickets.api.create('ws-1', {
    state: 'ready',
    title: 'A thing'
  });
  assert.equal(created.id, 'tkt-1');
});

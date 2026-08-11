import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./workspace-hub-tickets.js', import.meta.url), 'utf8');

/**
 * Loads the module with a recording fetch stub. Every test drives the real
 * exported surface — there is no mock ticket data anywhere in the module, so
 * anything rendered here came from a (stubbed) server response.
 */
function loadTickets(responder) {
  const calls = [];
  const fetchImpl = async (url, options = {}) => {
    calls.push({
      url,
      method: options.method,
      body: options.body ? JSON.parse(options.body) : null
    });
    const result = responder ? await responder(url, options) : { ok: true, body: {} };
    return {
      ok: result.ok !== false,
      status: result.status || (result.ok === false ? 400 : 200),
      json: async () => result.body
    };
  };
  const window = { fetch: fetchImpl };
  vm.runInNewContext(
    source,
    { console, window, URLSearchParams, JSON },
    {
      filename: 'workspace-hub-tickets.js'
    }
  );
  return { api: window.WorkspaceHubTickets, calls };
}

/**
 * Objects the module builds live in the vm's realm, so their prototypes are
 * not reference-equal to this file's. deepStrictEqual compares prototypes and
 * would fail on structurally identical values; a JSON round-trip re-creates
 * them in this realm so structural comparison means what it looks like.
 */
function plain(value) {
  return JSON.parse(JSON.stringify(value));
}

function serverTicket(overrides = {}) {
  return {
    id: 'tkt-1',
    number: 7,
    display_number: '#7',
    qualified_number: 'Alpha #7',
    owning_workspace_id: 'ws-1',
    owning_workspace_name: 'Alpha',
    title: 'Ship it',
    description: '## body',
    state: 'backlog',
    state_label: 'Backlog',
    state_rank: 2,
    tags: ['infra'],
    priority: 3,
    version: 1,
    created_at: '2026-08-01T00:00:00Z',
    updated_at: '2026-08-01T00:00:00Z',
    legal_transitions: ['ready', 'cancelled'],
    ...overrides
  };
}

test('normalizeTicket maps the canonical envelope and defaults missing optionals', () => {
  const { api } = loadTickets();
  const ticket = api.normalizeTicket(serverTicket());

  assert.equal(ticket.id, 'tkt-1');
  assert.equal(ticket.number, 7);
  assert.equal(ticket.displayNumber, '#7');
  assert.equal(ticket.qualifiedNumber, 'Alpha #7');
  assert.equal(ticket.owningWorkspaceId, 'ws-1');
  assert.equal(ticket.state, 'backlog');
  assert.equal(ticket.stateLabel, 'Backlog');
  assert.deepEqual(plain(ticket.tags), ['infra']);
  // Optionals the server omitted must not be undefined at render time.
  assert.deepEqual(plain(ticket.linkedNoteIds), []);
  assert.equal(ticket.dueDate, null);
  assert.equal(ticket.needsAttention, false);
});

test('normalizeTicket does not invent a state for an unrecognized value', () => {
  const { api } = loadTickets();
  const ticket = api.normalizeTicket(serverTicket({ state: 'weird', state_label: '' }));
  // Passing it through surfaces the problem instead of mislabeling the
  // ticket as Backlog (FR-7).
  assert.equal(ticket.state, 'weird');
  assert.equal(ticket.stateLabel, 'weird');
});

test('formatTicketNumber renders nothing for an unnumbered ticket', () => {
  const { api } = loadTickets();
  assert.equal(api.formatTicketNumber(7), '#7');
  assert.equal(api.formatTicketNumber(0), '');
  assert.equal(api.formatTicketNumber(null), '');
  assert.equal(api.formatQualifiedNumber({ number: 7, owningWorkspaceName: 'Alpha' }), 'Alpha #7');
  assert.equal(api.formatQualifiedNumber({ number: 7 }), '#7');
});

test('create requires an explicit backlog/ready choice and never guesses', async () => {
  const { api, calls } = loadTickets();

  await assert.rejects(
    () => api.api.create('ws-1', { title: 'no state' }),
    err => err.name === 'TicketApiError' && err.field === 'state'
  );
  await assert.rejects(() => api.api.create('ws-1', { state: 'done', title: 'shortcut' }));
  // Neither attempt reached the network.
  assert.equal(calls.length, 0);
});

test('create posts the chosen state to the owner-scoped route', async () => {
  const { api, calls } = loadTickets(() => ({ body: serverTicket({ state: 'ready' }) }));

  const ticket = await api.api.create('ws-1', { state: 'ready', title: 'committed' });

  assert.equal(calls[0].method, 'POST');
  assert.equal(calls[0].url, '/api/workspaces/ws-1/tickets');
  assert.equal(calls[0].body.state, 'ready');
  assert.equal(ticket.state, 'ready');
});

test('list builds state filters and the descendant roll-up flag', async () => {
  const { api, calls } = loadTickets(() => ({
    body: { tickets: [serverTicket()], count: 1, include_descendants: true }
  }));

  const result = await api.api.list('ws-1', {
    states: ['backlog', 'ready'],
    includeDescendants: true
  });

  assert.match(calls[0].url, /state=backlog/);
  assert.match(calls[0].url, /state=ready/);
  assert.match(calls[0].url, /include_descendants=true/);
  assert.equal(result.count, 1);
  assert.equal(result.tickets[0].id, 'tkt-1');
});

test('update cannot send a state change', async () => {
  const { api, calls } = loadTickets(() => ({ body: serverTicket() }));

  await api.api.update('ws-1', 'tkt-1', { title: 'renamed', state: 'done', version: 1 });

  assert.equal(calls[0].method, 'PATCH');
  assert.equal(calls[0].body.title, 'renamed');
  assert.equal('state' in calls[0].body, false, 'update must not be able to move lifecycle state');
  assert.equal(calls[0].body.version, 1);
});

test('transition targets the dedicated route and carries the version', async () => {
  const { api, calls } = loadTickets(() => ({ body: serverTicket({ state: 'ready' }) }));

  await api.api.transition('ws-1', 'tkt-1', 'ready', { reason: 'committing', version: 3 });

  assert.equal(calls[0].method, 'POST');
  assert.equal(calls[0].url, '/api/workspaces/ws-1/tickets/tkt-1/transition');
  assert.equal(calls[0].body.to, 'ready');
  assert.equal(calls[0].body.reason, 'committing');
  assert.equal(calls[0].body.version, 3);
});

test('mutations address the owning workspace, not the listing workspace', async () => {
  // A rolled-up ticket owned by ws-child, seen in ws-parent's list.
  const { api, calls } = loadTickets(() => ({
    body: serverTicket({ owning_workspace_id: 'ws-child' })
  }));
  const rolledUp = api.normalizeTicket(
    serverTicket({ owning_workspace_id: 'ws-child', owning_workspace_name: 'Child' })
  );

  await api.api.transition(rolledUp.owningWorkspaceId, rolledUp.id, 'ready');

  assert.equal(calls[0].url, '/api/workspaces/ws-child/tickets/tkt-1/transition');
});

test('a version conflict surfaces the server current record for recovery', async () => {
  const { api } = loadTickets(() => ({
    ok: false,
    status: 409,
    body: {
      code: 'conflict',
      message: 'ticket was modified by someone else',
      details: { current: serverTicket({ title: 'winner', version: 2 }) }
    }
  }));

  await assert.rejects(
    () => api.api.update('ws-1', 'tkt-1', { title: 'loser', version: 1 }),
    err => {
      assert.equal(err.isVersionConflict, true);
      assert.equal(err.currentTicket.title, 'winner');
      assert.equal(err.currentTicket.version, 2);
      return true;
    }
  );
});

test('an illegal transition surfaces the legal destinations', async () => {
  const { api } = loadTickets(() => ({
    ok: false,
    status: 409,
    body: {
      code: 'conflict',
      message: 'cannot move from Backlog to Done',
      details: { current_state: 'backlog', legal_transitions: ['ready', 'cancelled'] }
    }
  }));

  await assert.rejects(
    () => api.api.transition('ws-1', 'tkt-1', 'done'),
    err => {
      assert.equal(err.isIllegalTransition, true);
      assert.deepEqual(plain(err.details.legal_transitions), ['ready', 'cancelled']);
      return true;
    }
  );
});

test('a validation failure names the offending field', async () => {
  const { api } = loadTickets(() => ({
    ok: false,
    status: 400,
    body: { code: 'validation_error', message: 'title is required', details: { field: 'title' } }
  }));

  await assert.rejects(
    () => api.api.create('ws-1', { state: 'backlog', title: '' }),
    err => err.status === 400 && err.field === 'title'
  );
});

test('transitionOptions renders only legal moves and labels promotion explicitly', () => {
  const { api } = loadTickets();

  const backlog = api.normalizeTicket(
    serverTicket({ state: 'backlog', legal_transitions: ['ready', 'cancelled'] })
  );
  assert.deepEqual(plain(api.transitionOptions(backlog)), [
    { id: 'ready', label: 'Promote to Ready' },
    { id: 'cancelled', label: 'Move to Cancelled' }
  ]);

  const review = api.normalizeTicket(
    serverTicket({ state: 'review', legal_transitions: ['in_progress', 'done', 'cancelled'] })
  );
  const labels = api.transitionOptions(review).map(option => option.label);
  assert.deepEqual(plain(labels), ['Move to In Progress', 'Move to Done', 'Move to Cancelled']);

  assert.equal(api.canTransitionTo(backlog, 'ready'), true);
  assert.equal(api.canTransitionTo(backlog, 'done'), false);
});

test('state metadata never relies on color alone', () => {
  const { api } = loadTickets();
  // Every state carries a distinct human-readable label; `tone` is a
  // semantic name, not a color value.
  const labels = api.TICKET_STATES.map(state => state.label);
  assert.equal(new Set(labels).size, labels.length);
  api.TICKET_STATES.forEach(state => {
    assert.ok(state.label && state.label.trim().length > 0);
    assert.doesNotMatch(state.tone, /^#|rgb|red|green|blue$/i);
  });
  // Cancelled is not a default board column (FR-45).
  assert.equal(api.stateMeta('cancelled').activeColumn, false);
});

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
    { id: 'cancelled', label: 'Cancel' }
  ]);

  const review = api.normalizeTicket(
    serverTicket({ state: 'review', legal_transitions: ['in_progress', 'done', 'cancelled'] })
  );
  const labels = api.transitionOptions(review).map(option => option.label);
  // Labels name the INTENT, not the destination state: from Review,
  // in_progress means "request changes" and done means "mark done".
  assert.deepEqual(plain(labels), ['Request changes', 'Mark done', 'Cancel']);

  assert.equal(api.canTransitionTo(backlog, 'ready'), true);
  assert.equal(api.canTransitionTo(backlog, 'done'), false);
});

// FR-51/FR-70: the same destination state means different things from
// different origins, and the label has to say which.
test('the same transition is labelled by intent, not by destination', () => {
  const { api } = loadTickets();

  const labelFor = (state, legal, target) => {
    const ticket = api.normalizeTicket(serverTicket({ state, legal_transitions: legal }));
    return api.transitionOptions(ticket).find(option => option.id === target).label;
  };

  // `ready` from three different origins is three different decisions.
  assert.equal(labelFor('backlog', ['ready'], 'ready'), 'Promote to Ready');
  assert.equal(labelFor('done', ['ready'], 'ready'), 'Reopen');
  assert.equal(labelFor('cancelled', ['ready'], 'ready'), 'Reopen');
  assert.equal(labelFor('in_progress', ['ready'], 'ready'), 'Stop work');

  // `in_progress` likewise.
  assert.equal(labelFor('ready', ['in_progress'], 'in_progress'), 'Start work');
  assert.equal(labelFor('review', ['in_progress'], 'in_progress'), 'Request changes');
});

// FR-29/FR-30: Done is offered from Review and nowhere else.
test('Mark done is offered only from Review', () => {
  const { api } = loadTickets();

  const offersDone = (state, legal) => {
    const ticket = api.normalizeTicket(serverTicket({ state, legal_transitions: legal }));
    return api.transitionOptions(ticket).some(option => option.id === 'done');
  };

  assert.equal(offersDone('review', ['in_progress', 'done', 'cancelled']), true);
  // The server never lists `done` as legal from these, so the UI never shows
  // it — a successful run cannot put the button there either.
  assert.equal(offersDone('backlog', ['ready', 'cancelled']), false);
  assert.equal(offersDone('ready', ['backlog', 'in_progress', 'cancelled']), false);
  assert.equal(offersDone('in_progress', ['ready', 'review', 'cancelled']), false);
});

// --- Group 2: filters, search, sort, archive, hierarchy -------------------

test('list sends every filter to the server rather than filtering locally', async () => {
  const { api, calls } = loadTickets(() => ({ body: { tickets: [], count: 0, total: 0 } }));

  await api.api.list('ws-1', {
    states: ['backlog', 'ready'],
    tags: ['infra', 'ci'],
    priorities: [1, 2],
    assignees: ['unassigned'],
    sources: ['note'],
    owners: ['ws-child'],
    due: 'overdue',
    archive: 'archived',
    search: 'flaky',
    sort: 'due_date',
    descending: true,
    limit: 50,
    includeDescendants: true
  });

  const url = calls[0].url;
  ['state=backlog', 'state=ready', 'tag=infra', 'tag=ci', 'priority=1', 'priority=2'].forEach(
    part => assert.ok(url.includes(part), `expected ${part} in ${url}`)
  );
  assert.match(url, /assignee=unassigned/);
  assert.match(url, /source=note/);
  assert.match(url, /owner=ws-child/);
  assert.match(url, /due=overdue/);
  assert.match(url, /archive=archived/);
  assert.match(url, /search=flaky/);
  assert.match(url, /sort=due_date/);
  assert.match(url, /desc=true/);
  assert.match(url, /limit=50/);
  assert.match(url, /include_descendants=true/);
});

test('an omitted filter is not sent at all', async () => {
  const { api, calls } = loadTickets(() => ({ body: { tickets: [], count: 0 } }));
  await api.api.list('ws-1');
  // A bare list must not smuggle in defaults the caller never asked for.
  assert.equal(calls[0].url, '/api/workspaces/ws-1/tickets');
});

test('list surfaces truncation and unreadable roll-up owners', async () => {
  const { api } = loadTickets(() => ({
    body: {
      tickets: [serverTicket()],
      count: 1,
      total: 40,
      truncated: true,
      partial_owners: ['ws-broken']
    }
  }));

  const result = await api.api.list('ws-1', { includeDescendants: true });

  // The caller has to be able to say "this list is partial" rather than
  // present it as complete.
  assert.equal(result.total, 40);
  assert.equal(result.truncated, true);
  assert.deepEqual(plain(result.partialOwners), ['ws-broken']);
});

test('normalizeTicket carries hierarchy and the archived flag on detail reads', () => {
  const { api } = loadTickets();
  const ticket = api.normalizeTicket(
    serverTicket({
      archived: true,
      parent: {
        id: 'tkt-parent',
        number: 2,
        display_number: '#2',
        title: 'Parent work',
        state: 'ready',
        state_label: 'Ready',
        owning_workspace_id: 'ws-1'
      },
      subtickets: [
        {
          id: 'tkt-child',
          number: 3,
          display_number: '#3',
          title: 'Child work',
          state: 'backlog',
          state_label: 'Backlog',
          owning_workspace_id: 'ws-1'
        }
      ]
    })
  );

  assert.equal(ticket.archived, true);
  assert.equal(ticket.parent.id, 'tkt-parent');
  assert.equal(ticket.parent.displayNumber, '#2');
  assert.equal(ticket.subtickets.length, 1);
  assert.equal(ticket.subtickets[0].title, 'Child work');
});

test('a list response leaves hierarchy empty', () => {
  const { api } = loadTickets();
  // Hierarchy is a detail-only concern; the Board renders independent cards.
  const ticket = api.normalizeTicket(serverTicket());
  assert.equal(ticket.parent, null);
  assert.deepEqual(plain(ticket.subtickets), []);
  assert.equal(ticket.archived, false);
});

// --- Group 3: the fixed, server-authoritative Board -----------------------

test('board columns are fixed, ordered, and exclude Cancelled', () => {
  const { api } = loadTickets();
  const ids = api.BOARD_COLUMNS.map(column => column.id);

  // Fixed and derived from canonical state (FR-43, FR-44).
  assert.deepEqual(plain(ids), ['backlog', 'ready', 'in_progress', 'review', 'done']);
  // Cancelled is retained as a state but is not a default column (FR-45).
  assert.equal(ids.includes('cancelled'), false);
  assert.equal(api.stateMeta('cancelled').activeColumn, false);
});

test('every board column corresponds to a real canonical state', () => {
  const { api } = loadTickets();
  const known = new Set(api.TICKET_STATES.map(state => state.id));
  api.BOARD_COLUMNS.forEach(column => {
    assert.ok(known.has(column.id), `board column ${column.id} is not a canonical state`);
  });
});

test('a board move goes through the transition route, never a context patch', async () => {
  const { api, calls } = loadTickets(() => ({ body: serverTicket({ state: 'ready' }) }));
  const ticket = api.normalizeTicket(serverTicket({ state: 'backlog' }));

  await api.api.transition(ticket.owningWorkspaceId, ticket.id, 'ready', {
    version: ticket.version
  });

  // The one thing that must never happen: a lifecycle change smuggled through
  // a generic task/context update (FR-64, FR-88).
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, '/api/workspaces/ws-1/tickets/tkt-1/transition');
  assert.equal(calls[0].method, 'POST');
  assert.equal('kanban_column_id' in calls[0].body, false);
  assert.equal('context' in calls[0].body, false);
});

test('drop legality is derived from the server legal transitions', () => {
  const { api } = loadTickets();
  const backlog = api.normalizeTicket(
    serverTicket({ state: 'backlog', legal_transitions: ['ready', 'cancelled'] })
  );

  // Board columns a Backlog ticket may be dropped into.
  const droppable = api.BOARD_COLUMNS.filter(column => api.canTransitionTo(backlog, column.id)).map(
    column => column.id
  );
  assert.deepEqual(plain(droppable), ['ready']);

  // FR-36: no shortcut past Ready, so these columns must reject the drop.
  ['in_progress', 'review', 'done'].forEach(state => {
    assert.equal(api.canTransitionTo(backlog, state), false);
  });
});

test('the board move menu offers exactly the legal board destinations', () => {
  const { api } = loadTickets();
  const review = api.normalizeTicket(
    serverTicket({ state: 'review', legal_transitions: ['in_progress', 'done', 'cancelled'] })
  );

  const boardIds = new Set(api.BOARD_COLUMNS.map(column => column.id));
  const menu = api.transitionOptions(review).filter(option => boardIds.has(option.id));

  // Cancelled is legal but is not a board column, so it is not a board move.
  assert.deepEqual(
    plain(menu.map(option => option.id)),
    ['in_progress', 'done'],
    'the keyboard menu must match the moves a drag could make'
  );
});

// FR-27/FR-33: a ticket's full attempt history is discoverable by its ID, and
// a retry adds an attempt rather than replacing one.
test('run history is fetched per ticket and ordered newest first', async () => {
  const { api, calls } = loadTickets(() => ({
    body: {
      runs: [
        { id: 'run-1', status: 'failed', started_at: '2026-08-01T10:00:00Z', error: 'boom' },
        { id: 'run-2', status: 'succeeded', started_at: '2026-08-02T10:00:00Z' }
      ]
    }
  }));

  const runs = await api.api.runs('ws-1', 'tkt-1');

  assert.match(calls[0].url, /\/api\/workspaces\/ws-1\/runs\?ticket_id=tkt-1/);
  assert.equal(runs.length, 2, 'both attempts must survive; a retry never overwrites');
  assert.equal(runs[0].id, 'run-2', 'newest attempt first');
  assert.equal(runs[1].error, 'boom', 'the earlier failure keeps its error');
});

// --- Group 5: Note links --------------------------------------------------

test('linked notes come back as identity only, never note bodies', async () => {
  const { api } = loadTickets(() => ({
    body: {
      notes: [
        { id: 'note-1', title: 'Research spike', workspace_id: 'ws-1', content: 'SECRET BODY' }
      ]
    }
  }));

  const notes = await api.api.linkedNotes('ws-1', 'tkt-1');

  assert.equal(notes.length, 1);
  assert.equal(notes[0].title, 'Research spike');
  // A ticket references notes; it does not contain them. Body must not be
  // carried into ticket surfaces (FR-79).
  assert.equal('content' in notes[0], false);
  assert.equal('body' in notes[0], false);
});

test('unlink sends a DELETE that names the note, not the ticket content', async () => {
  const { api, calls } = loadTickets(() => ({ body: serverTicket({ linked_note_ids: [] }) }));

  await api.api.unlinkNote('ws-1', 'tkt-1', 'note-1', 3);

  assert.equal(calls[0].method, 'DELETE');
  assert.equal(calls[0].url, '/api/workspaces/ws-1/tickets/tkt-1/notes');
  assert.equal(calls[0].body.note_id, 'note-1');
  assert.equal(calls[0].body.version, 3);
  // Nothing in the request could modify the note itself.
  assert.equal('title' in calls[0].body, false);
  assert.equal('content' in calls[0].body, false);
});

test('createFromNote requires an explicit capture choice and posts reviewed values', async () => {
  const { api, calls } = loadTickets(() => ({ body: serverTicket({ source: 'note' }) }));

  await assert.rejects(
    () => api.api.createFromNote('ws-1', 'note-1', { title: 'no state' }),
    err => err.field === 'state'
  );
  assert.equal(calls.length, 0, 'an unreviewed create must not reach the network');

  await api.api.createFromNote('ws-1', 'note-1', {
    state: 'backlog',
    title: 'Reviewed title',
    description: 'Reviewed body'
  });

  assert.equal(calls[0].method, 'POST');
  assert.equal(calls[0].url, '/api/workspaces/ws-1/notes/note-1/tickets');
  // The reviewed values are what get sent — the user edited the prefill.
  assert.equal(calls[0].body.title, 'Reviewed title');
  assert.equal(calls[0].body.state, 'backlog');
});

test('the reverse lookup returns navigable summaries, not mutable tickets', async () => {
  const { api, calls } = loadTickets(() => ({
    body: {
      tickets: [
        {
          id: 'tkt-1',
          number: 4,
          display_number: '#4',
          title: 'Linked work',
          state: 'ready',
          state_label: 'Ready',
          owning_workspace_id: 'ws-1'
        }
      ]
    }
  }));

  const summaries = await api.api.ticketsForNote('ws-1', 'note-1');

  assert.equal(calls[0].url, '/api/workspaces/ws-1/notes/note-1/tickets');
  assert.equal(summaries.length, 1);
  assert.equal(summaries[0].displayNumber, '#4');
  assert.equal(summaries[0].stateLabel, 'Ready');
  // A summary carries enough to display and navigate, and deliberately not a
  // version token — the note surface is not a second mutation authority.
  assert.equal('version' in summaries[0], false);
  assert.equal('legalTransitions' in summaries[0], false);
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

import test from 'node:test';
import assert from 'node:assert/strict';

import {
  DEBOUNCE_MS,
  KIND_ORDER,
  RESULTS_TAB_KEY,
  SEARCH_ENDPOINT,
  applyAnswer,
  createFilesSearch,
  failedSnapshot,
  findResult,
  formatResultDate,
  normalizeTerm,
  partialWords,
  renderResultsHTML,
  renderResultsTabHTML,
  requestBody,
  resultCount,
  resultsTabLabel,
  totalFound,
  searchingSnapshot,
  statusLine,
  stepResult,
  tabFromResult
} from './home-files-search.js';

// ---- fixtures ---------------------------------------------------------------

const item = (kind, id, label, extra = {}) => ({
  kind,
  id,
  label,
  row_key: extra.row_key || `w1/${kind[0]}/${id}`,
  workspace: { id: 'w1', name: 'Album Mix' },
  location: ['Studio', 'Album Mix', 'Notes'],
  modified: '2026-11-02T00:00:00Z',
  ...extra
});

const kindAnswer = (kind, items, extra = {}) => ({
  kind,
  status: 'ok',
  count: items.length,
  items,
  next_cursor: '',
  ...extra
});

const answerOf = (...kinds) => ({ kinds });

// ---- the request --------------------------------------------------------------

test('normalizeTerm trims, and nothing typed is no search', () => {
  assert.equal(normalizeTerm('  Album  '), 'Album');
  assert.equal(normalizeTerm('   '), '');
  assert.equal(normalizeTerm(null), '');
  assert.equal(normalizeTerm(undefined), '');
});

test('the request body carries the term and only what was asked for', () => {
  assert.deepEqual(requestBody({ term: ' album ' }), { term: 'album' });
  assert.deepEqual(requestBody({ term: 'a', scope: { workspace_id: 'w1' } }), {
    term: 'a',
    scope: { workspace_id: 'w1' }
  });
  assert.deepEqual(requestBody({ term: 'a', scope: {} }), { term: 'a' });
  assert.deepEqual(requestBody({ term: 'a', kinds: ['notes'], cursors: { notes: '25' } }), {
    term: 'a',
    kinds: ['notes'],
    cursors: { notes: '25' }
  });
  assert.deepEqual(requestBody({ term: 'a', kinds: [], cursors: {} }), { term: 'a' });
});

// ---- the model ----------------------------------------------------------------

test('a new search starts with every kind waiting', () => {
  const snapshot = searchingSnapshot('  album ');
  assert.equal(snapshot.term, 'album');
  assert.equal(snapshot.status, 'searching');
  assert.deepEqual(Object.keys(snapshot.kinds), KIND_ORDER);
  assert.equal(snapshot.kinds.notes.status, 'loading');
});

test('an answer is read into each kind, in the server’s words', () => {
  const snapshot = applyAnswer(
    searchingSnapshot('album'),
    answerOf(
      kindAnswer('workspaces', [item('workspace', 'w1', 'Album Mix', { row_key: 'w1' })]),
      kindAnswer('notes', [item('note', 'n1', 'Sketch')], { next_cursor: '25', count: 30 }),
      { kind: 'tickets', status: 'unavailable', count: null, items: [], next_cursor: '' },
      kindAnswer('files', [], { status: 'partial', partial_reason: 'too_many_files', count: 0 })
    )
  );
  assert.equal(snapshot.status, 'done');
  assert.equal(snapshot.kinds.workspaces.items[0].label, 'Album Mix');
  assert.equal(snapshot.kinds.notes.count, 30);
  assert.equal(snapshot.kinds.notes.nextCursor, '25');
  assert.equal(snapshot.kinds.tickets.status, 'unavailable');
  assert.equal(snapshot.kinds.files.status, 'partial');
  assert.equal(snapshot.kinds.files.partialReason, 'too_many_files');
  // A kind the answer did not mention is left as it was.
  assert.equal(snapshot.kinds.chats.status, 'loading');
});

test('items that cannot be opened or shown are dropped, unknown kinds are ignored', () => {
  const snapshot = applyAnswer(
    searchingSnapshot('a'),
    answerOf(
      kindAnswer('notes', [
        item('note', 'n1', 'Good'),
        { kind: 'note', id: '', label: 'no id', row_key: 'w1/n/' },
        { kind: 'note', id: 'n2', label: 'no row key' },
        null,
        'junk'
      ]),
      { kind: 'bananas', status: 'ok', count: 1, items: [item('note', 'x', 'X')] }
    )
  );
  assert.deepEqual(
    snapshot.kinds.notes.items.map(i => i.label),
    ['Good']
  );
  assert.equal(snapshot.kinds.bananas, undefined);
});

test('an unknown status reads as unavailable rather than as an empty list', () => {
  const snapshot = applyAnswer(
    searchingSnapshot('a'),
    answerOf({ kind: 'notes', status: 'what', count: 0, items: [] })
  );
  assert.equal(snapshot.kinds.notes.status, 'unavailable');
});

test('More results appends to the kind; a plain answer replaces it', () => {
  const first = applyAnswer(
    searchingSnapshot('a'),
    answerOf(kindAnswer('notes', [item('note', 'n1', 'One')], { next_cursor: '1', count: 2 }))
  );
  const appended = applyAnswer(
    first,
    answerOf(kindAnswer('notes', [item('note', 'n2', 'Two')], { count: 2 })),
    { append: true }
  );
  assert.deepEqual(
    appended.kinds.notes.items.map(i => i.label),
    ['One', 'Two']
  );
  assert.equal(appended.kinds.notes.nextCursor, '');
  const replaced = applyAnswer(first, answerOf(kindAnswer('notes', [item('note', 'n3', 'Three')])));
  assert.deepEqual(
    replaced.kinds.notes.items.map(i => i.label),
    ['Three']
  );
});

test('resultCount and statusLine say what is true, never an empty list for a state', () => {
  assert.equal(statusLine(null), '');
  assert.equal(statusLine(searchingSnapshot('a')), 'Searching…');
  assert.equal(
    statusLine(failedSnapshot(searchingSnapshot('a'), '')),
    'Search is unavailable right now.'
  );

  const nothing = applyAnswer(
    searchingSnapshot('zzz'),
    answerOf(...KIND_ORDER.map(kind => kindAnswer(kind, [])))
  );
  assert.equal(resultCount(nothing), 0);
  assert.equal(statusLine(nothing), 'Nothing named “zzz”');

  // Part of it could not be searched: say so rather than claiming nothing exists.
  const partlyNothing = applyAnswer(
    searchingSnapshot('zzz'),
    answerOf(
      ...KIND_ORDER.map(kind =>
        kind === 'tickets'
          ? { kind, status: 'unavailable', count: null, items: [] }
          : kindAnswer(kind, [])
      )
    )
  );
  assert.match(statusLine(partlyNothing), /in what could be searched/);

  const one = applyAnswer(
    searchingSnapshot('a'),
    answerOf(kindAnswer('notes', [item('note', 'n1', 'A')]))
  );
  assert.equal(statusLine(one), '1 result');
  const two = applyAnswer(
    searchingSnapshot('a'),
    answerOf(kindAnswer('notes', [item('note', 'n1', 'A'), item('note', 'n2', 'B')]))
  );
  assert.equal(statusLine(two), '2 results');
});

test('totalFound counts what was found, not what is on screen, and is a floor when partial', () => {
  assert.deepEqual(totalFound(null), { total: 0, atLeast: false });
  const snapshot = applyAnswer(
    searchingSnapshot('a'),
    answerOf(
      kindAnswer('notes', [item('note', 'n1', 'One')], { count: 30, next_cursor: '25' }),
      kindAnswer('files', [item('file', 'f1', 'a.txt', { row_key: 'w1/f/a.txt' })], {
        status: 'partial',
        partial_reason: 'too_many_files',
        count: 4
      }),
      { kind: 'tickets', status: 'unavailable', count: null, items: [] }
    )
  );
  // Loaded rows: 2. Found: 30 + 4. The unavailable kind adds nothing.
  assert.equal(resultCount(snapshot), 2);
  assert.deepEqual(totalFound(snapshot), { total: 34, atLeast: true });
  assert.equal(statusLine(snapshot), '34+ results');
  // A single exact result keeps the singular.
  const one = applyAnswer(
    searchingSnapshot('a'),
    answerOf(kindAnswer('notes', [item('note', 'n1', 'One')]))
  );
  assert.equal(statusLine(one), '1 result');
});

test('a top-level workspace or group says what it is where the place would be', () => {
  const html = renderResultsHTML(
    applyAnswer(
      searchingSnapshot('a'),
      answerOf(
        kindAnswer('workspaces', [
          item('workspace', 'w1', 'Album Mix', { row_key: 'w1', location: [] }),
          item('group', 'g1', 'Studio', { row_key: 'g1', location: [] }),
          item('workspace', 'w2', 'Nested', { row_key: 'w2', location: ['Studio'] })
        ])
      )
    )
  );
  assert.match(html, /cockpit-result-where">Workspace</);
  assert.match(html, /cockpit-result-where">Group</);
  assert.match(html, /cockpit-result-where">Studio</);
});

// ---- the Results tab ----------------------------------------------------------

test('the tab reads Results for the term, and is selected by its own key', () => {
  const snapshot = searchingSnapshot('album');
  assert.equal(resultsTabLabel(snapshot), 'Results for “album”');
  const active = renderResultsTabHTML(snapshot, true);
  assert.match(active, /role="tab"/);
  assert.match(active, new RegExp(`data-pane-tab="${RESULTS_TAB_KEY}"`));
  assert.match(active, /aria-selected="true"/);
  assert.match(active, /tabindex="0"/);
  assert.match(renderResultsTabHTML(snapshot, false), /aria-selected="false"/);
  // It goes away by clearing the box, not by a close button of its own.
  assert.doesNotMatch(active, /data-pane-close/);
});

test('the tab and the panel escape what the user typed', () => {
  const snapshot = searchingSnapshot('<img src=x onerror=alert(1)>');
  assert.doesNotMatch(renderResultsTabHTML(snapshot, true), /<img/);
  assert.doesNotMatch(renderResultsHTML(snapshot), /<img/);
});

test('groups come in the fixed order, each with its count, 25 rows then More results', () => {
  const notes = Array.from({ length: 25 }, (_, i) => item('note', `n${i}`, `Note ${i}`));
  const snapshot = applyAnswer(
    searchingSnapshot('a'),
    answerOf(
      kindAnswer('files', [
        item('file', 'f1', 'report.pdf', { row_key: 'w1/f/report.pdf', path: 'report.pdf' })
      ]),
      kindAnswer('notes', notes, { count: 30, next_cursor: '25' }),
      kindAnswer('workspaces', [item('workspace', 'w1', 'Album Mix', { row_key: 'w1' })])
    )
  );
  const html = renderResultsHTML(snapshot);
  const order = [...html.matchAll(/data-results-group="(\w+)"/g)].map(m => m[1]);
  assert.deepEqual(order, ['workspaces', 'notes', 'files']);
  assert.match(html, /Notes <span class="cockpit-results-count">30<\/span>/);
  assert.equal([...html.matchAll(/data-result-open /g)].length, 27);
  assert.match(html, /data-result-more="notes"/);
  assert.doesNotMatch(html, /data-result-more="files"/);
  // The status counts what was found (1 workspace + 30 notes + 1 file), not the
  // 27 rows on screen.
  assert.match(html, /cockpit-results-status[^>]*>32 results</);
});

test('a row shows its name, where it lives and when, and offers Show in tree', () => {
  const snapshot = applyAnswer(
    searchingSnapshot('a'),
    answerOf(kindAnswer('notes', [item('note', 'n1', 'Album Sketch')]))
  );
  const html = renderResultsHTML(snapshot);
  assert.match(html, /Album Sketch/);
  assert.match(html, /Studio › Album Mix › Notes/);
  assert.match(html, /<time datetime="2026-11-02T00:00:00Z">/);
  assert.match(html, /data-result-reveal[^>]*aria-label="Show Album Sketch in tree"/);
  assert.match(html, /<svg class="cockpit-tree-icon"/);
});

test('a row without a date shows none, and a bad date shows none', () => {
  assert.equal(formatResultDate(''), '');
  assert.equal(formatResultDate('not a date'), '');
  assert.match(formatResultDate('2026-11-02T12:00:00Z'), /2026/);
  const snapshot = applyAnswer(
    searchingSnapshot('a'),
    answerOf(kindAnswer('agents', [item('agent', 'Scout', 'Album Scout', { modified: undefined })]))
  );
  assert.doesNotMatch(renderResultsHTML(snapshot), /<time/);
});

test('exactly one result is a tab stop, so the arrow keys walk the rest', () => {
  const snapshot = applyAnswer(
    searchingSnapshot('a'),
    answerOf(
      kindAnswer('workspaces', [item('workspace', 'w1', 'A', { row_key: 'w1' })]),
      kindAnswer('notes', [item('note', 'n1', 'B'), item('note', 'n2', 'C')])
    )
  );
  const html = renderResultsHTML(snapshot);
  const stops = [...html.matchAll(/data-result-open [^>]*tabindex="(-?\d)"/g)].map(m => m[1]);
  assert.deepEqual(stops, ['0', '-1', '-1']);
});

test('a kind that could not be searched says so and offers Retry for that kind', () => {
  const snapshot = applyAnswer(
    searchingSnapshot('a'),
    answerOf(kindAnswer('notes', [item('note', 'n1', 'B')]), {
      kind: 'tickets',
      status: 'unavailable',
      count: null,
      items: []
    })
  );
  const html = renderResultsHTML(snapshot);
  assert.match(html, /data-results-unavailable="tickets"/);
  assert.match(html, /Tickets couldn’t be searched right now/);
  assert.match(html, /data-result-retry="tickets"/);
  // The ones that did answer are still shown.
  assert.match(html, /data-results-group="notes"/);
});

test('a partly searched kind says why, and its count is a lower bound', () => {
  const snapshot = applyAnswer(
    searchingSnapshot('a'),
    answerOf(
      kindAnswer('files', [item('file', 'f1', 'a.txt', { row_key: 'w1/f/a.txt' })], {
        status: 'partial',
        partial_reason: 'too_many_files',
        count: 1
      })
    )
  );
  const html = renderResultsHTML(snapshot);
  assert.match(html, /Files <span class="cockpit-results-count">1\+<\/span>/);
  assert.match(html, /more files than one search can read/);
  assert.match(partialWords('timed_out'), /ran out of time/);
  assert.match(partialWords('unreadable'), /could not be read/);
  assert.match(partialWords('something-new'), /partly searched/);
});

test('searching and failed states are words, with Retry when the whole request failed', () => {
  const searching = renderResultsHTML(searchingSnapshot('a'));
  assert.match(searching, /Searching…/);
  assert.doesNotMatch(searching, /data-results-group/);

  const failed = renderResultsHTML(
    failedSnapshot(searchingSnapshot('a'), 'Search is unavailable right now.')
  );
  assert.match(failed, /role="alert"/);
  assert.match(failed, /data-result-retry="all"/);
});

test('a line about what was left out is shown once, under the status', () => {
  const snapshot = applyAnswer(searchingSnapshot('a'), answerOf(kindAnswer('notes', [])));
  const html = renderResultsHTML(snapshot, {
    note: 'Linked-folder filenames are searched only on this Mac.'
  });
  assert.equal([...html.matchAll(/searched only on this Mac/g)].length, 1);
});

test('the panel takes a title that can be focused, and a loading-more line replaces the button', () => {
  const snapshot = applyAnswer(
    searchingSnapshot('a'),
    answerOf(kindAnswer('notes', [item('note', 'n1', 'B')], { next_cursor: '1', count: 2 }))
  );
  assert.match(renderResultsHTML(snapshot), /<h3 [^>]*data-pane-title tabindex="-1">/);
  const loading = {
    ...snapshot,
    kinds: { ...snapshot.kinds, notes: { ...snapshot.kinds.notes, loadingMore: true } }
  };
  const html = renderResultsHTML(loading);
  assert.doesNotMatch(html, /data-result-more/);
  assert.match(html, /Loading more/);
  const bad = {
    ...snapshot,
    kinds: { ...snapshot.kinds, notes: { ...snapshot.kinds.notes, moreFailed: true } }
  };
  assert.match(renderResultsHTML(bad), /Couldn’t load more notes/);
});

test('findResult looks a result up by kind and row key, and says null when it moved on', () => {
  const snapshot = applyAnswer(
    searchingSnapshot('a'),
    answerOf(kindAnswer('notes', [item('note', 'n1', 'B')]))
  );
  assert.equal(findResult(snapshot, 'note', 'w1/n/n1').label, 'B');
  assert.equal(findResult(snapshot, 'note', 'w1/n/gone'), null);
  assert.equal(findResult(snapshot, 'ticket', 'w1/n/n1'), null);
  assert.equal(findResult(null, 'note', 'w1/n/n1'), null);
});

test('stepResult moves through the results; up from the first returns to the box', () => {
  assert.equal(stepResult(-1, 1, 3), 0);
  assert.equal(stepResult(0, 1, 3), 1);
  assert.equal(stepResult(2, 1, 3), 2, 'stays on the last');
  assert.equal(stepResult(1, -1, 3), 0);
  assert.equal(stepResult(0, -1, 3), -1, 'the box');
  assert.equal(stepResult(-1, -1, 3), -1);
  assert.equal(stepResult(0, 1, 0), -1);
});

// ---- opening a result opens the tab its row would -----------------------------

test('a result opens the same tab as its tree row, under the row’s own key', () => {
  const note = tabFromResult({
    kind: 'note',
    id: 'n1',
    label: 'Sketch',
    rowKey: 'w1/n/n1',
    workspaceId: 'w1',
    modified: 'x',
    path: '',
    size: null
  });
  assert.deepEqual(note, {
    key: 'w1/n/n1',
    kind: 'note',
    workspaceId: 'w1',
    label: 'Sketch',
    meta: { noteId: 'n1', updatedAt: 'x' }
  });

  const ticket = tabFromResult({
    kind: 'ticket',
    id: 't1',
    label: 'T',
    rowKey: 'w1/t/t1',
    workspaceId: 'w1',
    modified: '',
    path: '',
    size: null
  });
  assert.equal(ticket.meta.ticketId, 't1');
  assert.equal(ticket.key, 'w1/t/t1');

  const chat = tabFromResult({
    kind: 'chat',
    id: 'c1',
    label: 'C',
    rowKey: 'w1/c/c1',
    workspaceId: 'w1',
    modified: '',
    path: '',
    size: null
  });
  assert.equal(chat.meta.chatId, 'c1');

  const agent = tabFromResult({
    kind: 'agent',
    id: 'Scout',
    label: 'Scout',
    rowKey: 'w1/a/Scout',
    workspaceId: 'w1',
    modified: '',
    path: '',
    size: null
  });
  assert.equal(agent.meta.name, 'Scout');

  const file = tabFromResult({
    kind: 'file',
    id: 'sub/a.txt',
    label: 'a.txt',
    rowKey: 'w1/f/sub/a.txt',
    workspaceId: 'w1',
    modified: '',
    path: 'sub/a.txt',
    size: 12
  });
  assert.deepEqual(file.meta, { path: 'sub/a.txt', size: 12 });
  const output = tabFromResult({
    kind: 'output',
    id: 'o.md',
    label: 'o.md',
    rowKey: 'w1/o/o.md',
    workspaceId: 'w1',
    modified: '',
    path: 'o.md',
    size: null
  });
  assert.deepEqual(output.meta, { path: 'o.md' });
  assert.equal(output.kind, 'output');
});

test('a workspace or group result opens the workspace’s own tab, keyed by its id', () => {
  for (const kind of ['workspace', 'group']) {
    const tab = tabFromResult({
      kind,
      id: 'w9',
      label: 'Name',
      rowKey: 'w9',
      workspaceId: 'w9',
      modified: '',
      path: '',
      size: null
    });
    assert.equal(tab.key, 'w9');
    assert.equal(tab.kind, kind);
    assert.equal(tab.workspaceId, 'w9');
  }
});

// ---- the controller -----------------------------------------------------------

function makeTimers() {
  let next = 1;
  const pending = new Map();
  return {
    setTimeoutImpl(fn, ms) {
      const id = next++;
      pending.set(id, { fn, ms });
      return id;
    },
    clearTimeoutImpl(id) {
      pending.delete(id);
    },
    get size() {
      return pending.size;
    },
    delays: () => [...pending.values()].map(t => t.ms),
    flush() {
      const due = [...pending.entries()];
      pending.clear();
      due.forEach(([, t]) => t.fn());
    }
  };
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const okResponse = body => ({ ok: true, json: async () => body });

// A fetch whose answers the test hands out one at a time. It honours abort.
function makeFetch() {
  const calls = [];
  const fetchImpl = (url, init) => {
    const d = deferred();
    const call = { url, init, body: JSON.parse(init.body), ...d, aborted: false };
    init.signal.addEventListener('abort', () => {
      call.aborted = true;
      const error = new Error('aborted');
      error.name = 'AbortError';
      d.reject(error);
    });
    calls.push(call);
    return d.promise;
  };
  return { fetchImpl, calls };
}

const tick = () => new Promise(resolve => setImmediate(resolve));

function harness(options = {}) {
  const timers = makeTimers();
  const net = makeFetch();
  const events = [];
  const search = createFilesSearch({
    onChange: (snapshot, reason) => events.push({ snapshot, reason }),
    fetchImpl: net.fetchImpl,
    setTimeoutImpl: timers.setTimeoutImpl,
    clearTimeoutImpl: timers.clearTimeoutImpl,
    ...options
  });
  return { search, timers, net, events };
}

test('typing shows the Results tab at once and asks the server only after a pause', () => {
  const { search, timers, net, events } = harness();
  search.input('alb');
  assert.deepEqual(
    events.map(e => e.reason),
    ['typed']
  );
  assert.equal(events[0].snapshot.status, 'searching');
  assert.equal(net.calls.length, 0, 'no request on a keystroke');
  assert.deepEqual(timers.delays(), [DEBOUNCE_MS]);
});

test('typing again before the pause ends sends one request, for the last text', async () => {
  const { search, timers, net } = harness();
  search.input('a');
  search.input('al');
  search.input('alb');
  assert.equal(timers.size, 1, 'each keystroke replaces the pending request');
  timers.flush();
  await tick();
  assert.equal(net.calls.length, 1);
  assert.equal(net.calls[0].body.term, 'alb');
});

test('the request is a POST of JSON that asks not to be kept, with the term only in its body', async () => {
  const { search, timers, net } = harness();
  search.input('  secret-term  ');
  timers.flush();
  await tick();
  const call = net.calls[0];
  assert.equal(call.url, SEARCH_ENDPOINT);
  assert.equal(call.url.includes('secret-term'), false);
  assert.equal(call.init.method, 'POST');
  assert.equal(call.init.headers['Content-Type'], 'application/json');
  assert.equal(call.init.cache, 'no-store');
  assert.equal(call.init.referrerPolicy, 'no-referrer');
  assert.equal(call.init.credentials, 'same-origin');
  assert.deepEqual(call.body, { term: 'secret-term' });
});

test('the answer arrives as results; the scope is read at the moment of the request', async () => {
  let scope = null;
  const { search, timers, net, events } = harness({ getScope: () => scope });
  search.input('album');
  scope = { workspace_id: 'w1' };
  timers.flush();
  await tick();
  assert.deepEqual(net.calls[0].body.scope, { workspace_id: 'w1' });
  net.calls[0].resolve(
    okResponse(answerOf(kindAnswer('notes', [item('note', 'n1', 'Album Sketch')])))
  );
  await tick();
  const last = events[events.length - 1];
  assert.equal(last.reason, 'results');
  assert.equal(last.snapshot.status, 'done');
  assert.equal(last.snapshot.kinds.notes.items[0].label, 'Album Sketch');
});

test('a slower earlier answer never overwrites a newer one', async () => {
  const { search, timers, net, events } = harness();
  search.input('old');
  timers.flush();
  await tick();
  search.input('new');
  timers.flush();
  await tick();
  assert.equal(net.calls.length, 2);
  assert.equal(net.calls[0].aborted, true, 'the old request is cancelled');

  // The new answer arrives first...
  net.calls[1].resolve(okResponse(answerOf(kindAnswer('notes', [item('note', 'n2', 'New')]))));
  await tick();
  // ...then the old one comes in late, as if the abort had not reached the server.
  net.calls[0].resolve(okResponse(answerOf(kindAnswer('notes', [item('note', 'n1', 'Old')]))));
  await tick();
  const snapshot = search.snapshot();
  assert.equal(snapshot.term, 'new');
  assert.deepEqual(
    snapshot.kinds.notes.items.map(i => i.label),
    ['New']
  );
  assert.equal(events.filter(e => e.reason === 'results').length, 1);
});

test('an answer that lands after the box was cleared is thrown away', async () => {
  const { search, timers, net, events } = harness();
  search.input('album');
  timers.flush();
  await tick();
  search.clear();
  net.calls[0].resolve(okResponse(answerOf(kindAnswer('notes', [item('note', 'n1', 'Album')]))));
  await tick();
  assert.equal(search.snapshot(), null);
  assert.deepEqual(
    events.map(e => e.reason),
    ['typed', 'cleared']
  );
});

test('clearing cancels the pause and the request, and says so once', async () => {
  const { search, timers, net, events } = harness();
  search.input('album');
  search.clear();
  assert.equal(timers.size, 0);
  search.input('again');
  timers.flush();
  await tick();
  search.clear();
  assert.equal(net.calls[0].aborted, true);
  search.clear(); // already clear: no second event
  assert.deepEqual(
    events.map(e => e.reason),
    ['typed', 'cleared', 'typed', 'cleared']
  );
});

test('blank text is a clear; extra spaces around the same term change nothing', async () => {
  const { search, timers, net, events } = harness();
  search.input('album');
  search.input('album  ');
  assert.equal(events.length, 1, 'only spaces changed');
  timers.flush();
  await tick();
  assert.equal(net.calls.length, 1);
  search.input('   ');
  assert.equal(events[events.length - 1].reason, 'cleared');
});

test('a failed request reports it, and Retry asks again', async () => {
  const { search, timers, net, events } = harness();
  search.input('album');
  timers.flush();
  await tick();
  net.calls[0].reject(new Error('network down'));
  await tick();
  assert.equal(search.snapshot().status, 'failed');
  assert.equal(events[events.length - 1].reason, 'results');

  void search.retry('all');
  await tick();
  assert.equal(net.calls.length, 2);
  assert.equal(search.snapshot().status, 'searching');
  net.calls[1].resolve(okResponse(answerOf(kindAnswer('notes', [item('note', 'n1', 'Album')]))));
  await tick();
  assert.equal(search.snapshot().status, 'done');
});

test('a server error is a failure, not an empty answer', async () => {
  const { search, timers, net } = harness();
  search.input('album');
  timers.flush();
  await tick();
  net.calls[0].resolve({ ok: false, status: 500, json: async () => ({}) });
  await tick();
  assert.equal(search.snapshot().status, 'failed');
});

test('More results asks for the next page of one kind and adds it', async () => {
  const { search, timers, net, events } = harness();
  search.input('draft');
  timers.flush();
  await tick();
  net.calls[0].resolve(
    okResponse(
      answerOf(
        kindAnswer('notes', [item('note', 'n1', 'Draft 1')], { next_cursor: '25', count: 26 })
      )
    )
  );
  await tick();

  const more = search.loadMore('notes');
  assert.equal(search.snapshot().kinds.notes.loadingMore, true);
  await tick();
  assert.deepEqual(net.calls[1].body, {
    term: 'draft',
    kinds: ['notes'],
    cursors: { notes: '25' }
  });
  // A second press while it loads does not send a second request.
  void search.loadMore('notes');
  await tick();
  assert.equal(net.calls.length, 2);

  net.calls[1].resolve(
    okResponse(answerOf(kindAnswer('notes', [item('note', 'n26', 'Draft 26')], { count: 26 })))
  );
  await more;
  const notes = search.snapshot().kinds.notes;
  assert.deepEqual(
    notes.items.map(i => i.label),
    ['Draft 1', 'Draft 26']
  );
  assert.equal(notes.nextCursor, '');
  assert.equal(notes.loadingMore, false);
  assert.equal(events[events.length - 1].reason, 'more');
});

test('More results that fails keeps what is shown and says so', async () => {
  const { search, timers, net } = harness();
  search.input('draft');
  timers.flush();
  await tick();
  net.calls[0].resolve(
    okResponse(
      answerOf(
        kindAnswer('notes', [item('note', 'n1', 'Draft 1')], { next_cursor: '25', count: 26 })
      )
    )
  );
  await tick();
  const more = search.loadMore('notes');
  await tick();
  net.calls[1].reject(new Error('down'));
  await more;
  const notes = search.snapshot().kinds.notes;
  assert.equal(notes.moreFailed, true);
  assert.equal(notes.loadingMore, false);
  assert.equal(notes.items.length, 1);
  assert.equal(notes.nextCursor, '25', 'it can be tried again');
});

test('More results is dropped if a newer search started meanwhile', async () => {
  const { search, timers, net } = harness();
  search.input('draft');
  timers.flush();
  await tick();
  net.calls[0].resolve(
    okResponse(
      answerOf(
        kindAnswer('notes', [item('note', 'n1', 'Draft 1')], { next_cursor: '25', count: 26 })
      )
    )
  );
  await tick();
  void search.loadMore('notes');
  await tick();
  search.input('other');
  net.calls[1].resolve(okResponse(answerOf(kindAnswer('notes', [item('note', 'late', 'Late')]))));
  await tick();
  assert.equal(search.snapshot().term, 'other');
  assert.equal(search.snapshot().kinds.notes.items.length, 0);
});

test('Retry for one kind asks for that kind alone and replaces only it', async () => {
  const { search, timers, net } = harness();
  search.input('album');
  timers.flush();
  await tick();
  net.calls[0].resolve(
    okResponse(
      answerOf(
        kindAnswer('workspaces', [item('workspace', 'w1', 'Album Mix', { row_key: 'w1' })]),
        { kind: 'tickets', status: 'unavailable', count: null, items: [] }
      )
    )
  );
  await tick();
  assert.equal(search.snapshot().kinds.tickets.status, 'unavailable');

  const retry = search.retry('tickets');
  await tick();
  assert.deepEqual(net.calls[1].body, { term: 'album', kinds: ['tickets'] });
  net.calls[1].resolve(
    okResponse(answerOf(kindAnswer('tickets', [item('ticket', 't1', 'Album ticket')])))
  );
  await retry;
  const snapshot = search.snapshot();
  assert.equal(snapshot.kinds.tickets.status, 'ok');
  assert.equal(snapshot.kinds.tickets.items[0].label, 'Album ticket');
  assert.equal(snapshot.kinds.workspaces.items[0].label, 'Album Mix', 'the others are untouched');
});

test('Retry for one kind that fails again stays unavailable', async () => {
  const { search, timers, net } = harness();
  search.input('album');
  timers.flush();
  await tick();
  net.calls[0].resolve(
    okResponse(answerOf({ kind: 'tickets', status: 'unavailable', count: null, items: [] }))
  );
  await tick();
  const retry = search.retry('tickets');
  await tick();
  net.calls[1].reject(new Error('still down'));
  await retry;
  assert.equal(search.snapshot().kinds.tickets.status, 'unavailable');
});

test('dispose cancels everything and forgets the term', async () => {
  const { search, timers, net } = harness();
  search.input('album');
  timers.flush();
  await tick();
  search.dispose();
  assert.equal(net.calls[0].aborted, true);
  assert.equal(search.snapshot(), null);
  assert.equal(search.term(), '');
});

// ---- the term is private --------------------------------------------------------

test('a whole search touches no browser storage, address, history or window name', async () => {
  const touched = [];
  const trap = name =>
    new Proxy(
      {},
      {
        get: (_, prop) => {
          touched.push(`${name}.${String(prop)}`);
          return undefined;
        },
        set: (_, prop) => {
          touched.push(`${name}.${String(prop)}=`);
          return true;
        }
      }
    );
  const saved = {};
  const traps = ['localStorage', 'sessionStorage', 'history', 'location', 'document'];
  traps.forEach(name => {
    saved[name] = Object.getOwnPropertyDescriptor(globalThis, name);
    Object.defineProperty(globalThis, name, {
      value: trap(name),
      configurable: true,
      writable: true
    });
  });
  try {
    const { search, timers, net } = harness();
    search.input('zebra-secret-term');
    timers.flush();
    await tick();
    net.calls[0].resolve(
      okResponse(
        answerOf(kindAnswer('notes', [item('note', 'n1', 'Zebra')], { next_cursor: '1', count: 2 }))
      )
    );
    await tick();
    void search.loadMore('notes');
    await tick();
    net.calls[1].resolve(okResponse(answerOf(kindAnswer('notes', []))));
    await tick();
    search.clear();
  } finally {
    traps.forEach(name => {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]);
      else delete globalThis[name];
    });
  }
  assert.deepEqual(touched, []);
});

test('the results panel and tab never carry the term anywhere but their visible text', () => {
  const snapshot = applyAnswer(
    searchingSnapshot('zebra'),
    answerOf(kindAnswer('notes', [item('note', 'n1', 'Zebra note')]))
  );
  const html = renderResultsHTML(snapshot) + renderResultsTabHTML(snapshot, true);
  // No attribute other than a label or title holds it: nothing keyed by the term.
  assert.doesNotMatch(html, /data-[a-z-]+="[^"]*zebra/i);
});

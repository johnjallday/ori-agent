// Tests for the filename-index half of home-files-search.js: files in linked and
// searchable folders, merged into the Files group beside the files stored in
// workspaces (merge-tree-into-files FR13, FR19, FR23, FR25).
//   node --test internal/web/static/js/modules/home-files-search-index.test.js

import test from 'node:test';
import assert from 'node:assert/strict';

import {
  INDEX_OFF_WORDS,
  applyAnswer,
  applyIndexAnswer,
  createFilesSearch,
  filesFound,
  findResult,
  mergedFiles,
  partlyIndexed,
  renderResultsHTML,
  searchingSnapshot,
  sourceNames,
  statusLine,
  totalFound
} from './home-files-search.js';

// ---- fixtures ---------------------------------------------------------------

const day = n => `2026-11-${String(n).padStart(2, '0')}T12:00:00Z`;

const source = (id, label, extra = {}) => ({
  id,
  label,
  state: 'ready',
  generation: 7,
  workspace: { id: 'w1', name: 'Album Mix', link: 'dir1', connected: true, slug: 'album-mix' },
  ...extra
});

const hit = (id, name, modified, extra = {}) => ({
  id,
  generation: 3,
  name,
  parent: '',
  modified,
  source: 's1',
  reader: 'Preview',
  ...extra
});

const serverFile = (id, name, modified) => ({
  kind: 'file',
  id,
  label: name,
  row_key: `w1/f/${id}`,
  workspace: { id: 'w1', name: 'Album Mix' },
  location: ['Album Mix', 'Files'],
  modified,
  path: id,
  size: 3
});

const filesKind = (items, extra = {}) => ({
  kind: 'files',
  status: 'ok',
  count: items.length,
  items,
  next_cursor: '',
  ...extra
});

const withServer = (snapshot, items, extra) =>
  applyAnswer(snapshot, { kinds: [filesKind(items, extra)] });

const withIndex = (snapshot, hits, { next = '', sources = [source('s1', 'Projects')] } = {}) =>
  applyIndexAnswer(snapshot, { hits, next, sources });

const local = term => searchingSnapshot(term, { index: true });

// ---- the model ----------------------------------------------------------------

test('a hit becomes a result row with where it lives, its reader, and its tree row', () => {
  const snapshot = withIndex(local('plan'), [
    hit('h1', 'plan.pdf', day(9), { parent: 'docs/2026' })
  ]);
  const [row] = snapshot.index.items;
  assert.equal(row.kind, 'indexed');
  assert.equal(row.label, 'plan.pdf');
  assert.equal(row.reader, 'Preview');
  assert.deepEqual(row.location, ['Projects', 'docs', '2026']);
  assert.equal(row.path, 'docs/2026/plan.pdf');
  // The tree row of a file inside a linked folder: workspace, linked-file kind,
  // the folder's id, then the path inside it.
  assert.equal(row.treeKey, 'w1/lf/dir1/docs/2026/plan.pdf');
  assert.equal(row.workspaceName, 'Album Mix');
  assert.equal(snapshot.index.status, 'ok');
});

test('a file in a folder with no workspace has no tree row, and is not a tab', () => {
  const snapshot = withIndex(local('a'), [hit('h1', 'a.txt', day(1))], {
    sources: [source('s1', 'Downloads', { workspace: null })]
  });
  const [row] = snapshot.index.items;
  assert.equal(row.treeKey, '');
  assert.equal(row.workspaceId, '');
  // Its key only tells results apart; it never names a row.
  assert.match(row.rowKey, /^idx:/);
});

test('folders that share a name are told apart, and a folder at the top has no parent crumbs', () => {
  const names = sourceNames([
    { id: 'abcdef123', label: 'Docs' },
    { id: 'fedcba987', label: 'Docs' },
    { id: 'zzz', label: 'Other' }
  ]);
  assert.equal(names.get('abcdef123'), 'Docs · abcdef');
  assert.equal(names.get('fedcba987'), 'Docs · fedcba');
  assert.equal(names.get('zzz'), 'Other');
  const row = withIndex(local('a'), [hit('h1', 'a.txt', day(1))]).index.items[0];
  assert.deepEqual(row.location, ['Projects']);
});

test('hits without an id or a name are dropped; a missing reader is no reader', () => {
  const snapshot = withIndex(local('a'), [
    hit('h1', 'ok.txt', day(1), { reader: undefined }),
    { id: '', name: 'no id', source: 's1' },
    { id: 'h3', name: '', source: 's1' },
    null
  ]);
  assert.equal(snapshot.index.items.length, 1);
  assert.equal(snapshot.index.items[0].reader, '');
});

test('More results appends a page of hits and keeps the folders it learnt', () => {
  const first = withIndex(local('a'), [hit('h1', 'a.txt', day(9))], { next: 'c1' });
  assert.equal(first.index.next, 'c1');
  const second = applyIndexAnswer(
    first,
    { hits: [hit('h2', 'b.txt', day(8))], next: '', sources: [source('s1', 'Projects')] },
    { append: true }
  );
  assert.deepEqual(
    second.index.items.map(i => i.label),
    ['a.txt', 'b.txt']
  );
  assert.equal(second.index.next, '');
});

test('folders that are not ready are reported, ready ones are not', () => {
  const snapshot = withIndex(local('a'), [], {
    sources: [
      source('s1', 'Done'),
      source('s2', 'Half', { state: 'partial' }),
      source('s3', 'Working', { state: 'mapping' })
    ]
  });
  assert.deepEqual(
    partlyIndexed(snapshot).map(s => s.label),
    ['Half', 'Working']
  );
});

// ---- the merge ----------------------------------------------------------------

test('the Files group is both streams, newest first', () => {
  let snapshot = local('plan');
  snapshot = withServer(snapshot, [
    serverFile('a.txt', 'a.txt', day(10)),
    serverFile('c.txt', 'c.txt', day(6))
  ]);
  snapshot = withIndex(snapshot, [hit('h1', 'b.pdf', day(8)), hit('h2', 'd.pdf', day(5))]);
  const merged = mergedFiles(snapshot);
  assert.deepEqual(
    merged.items.map(i => i.label),
    ['a.txt', 'b.pdf', 'c.txt', 'd.pdf']
  );
  assert.equal(merged.more, false);
});

test('a row is held back while a stream with more could still hold something newer', () => {
  let snapshot = local('x');
  // The server has more, and its last fetched file is from day 8: anything it
  // has not sent is no newer than that.
  snapshot = withServer(
    snapshot,
    [serverFile('s1', 's1', day(10)), serverFile('s2', 's2', day(8))],
    { next_cursor: '2', count: 5 }
  );
  snapshot = withIndex(snapshot, [hit('h1', 'i1', day(9)), hit('h2', 'i2', day(3))]);
  const merged = mergedFiles(snapshot);
  // day 3 could be beaten by an unfetched server file from day 7: not final.
  assert.deepEqual(
    merged.items.map(i => i.label),
    ['s1', 'i1', 's2']
  );
  assert.equal(merged.more, true);
  assert.equal(merged.serverMore, true);
  assert.equal(merged.indexMore, false);
});

test('with both streams having more, only rows as new as the newer boundary are final', () => {
  let snapshot = local('x');
  snapshot = withServer(
    snapshot,
    [serverFile('s1', 's1', day(10)), serverFile('s2', 's2', day(6))],
    {
      next_cursor: '2',
      count: 9
    }
  );
  snapshot = withIndex(snapshot, [hit('h1', 'i1', day(9)), hit('h2', 'i2', day(7))], { next: 'c' });
  const merged = mergedFiles(snapshot);
  // The server's boundary is day 6, the index's day 7: nothing older than day 7
  // is final, because the index might still send a day-7 file ahead of it.
  assert.deepEqual(
    merged.items.map(i => i.label),
    ['s1', 'i1', 'i2']
  );
});

test('a stream that is unavailable adds nothing and holds nothing back', () => {
  let snapshot = local('x');
  snapshot = applyAnswer(snapshot, {
    kinds: [{ kind: 'files', status: 'unavailable', count: null, items: [] }]
  });
  snapshot = withIndex(snapshot, [hit('h1', 'i1', day(2))]);
  assert.deepEqual(
    mergedFiles(snapshot).items.map(i => i.label),
    ['i1']
  );
});

test('the count is what was found in both, a floor when either is partial or has more', () => {
  let snapshot = withServer(local('x'), [serverFile('a', 'a', day(2))], { count: 4 });
  snapshot = withIndex(snapshot, [hit('h1', 'i1', day(1)), hit('h2', 'i2', day(1))]);
  assert.deepEqual(filesFound(snapshot), { count: 6, atLeast: false });
  assert.deepEqual(filesFound(withIndex(snapshot, [hit('h1', 'i1', day(1))], { next: 'c' })), {
    count: 5,
    atLeast: true
  });
  const partial = withServer(local('x'), [], {
    status: 'partial',
    partial_reason: 'timed_out',
    count: 1
  });
  assert.equal(filesFound(partial).atLeast, true);
  // The status line counts them with every other kind.
  const all = applyAnswer(snapshot, {
    kinds: [{ kind: 'notes', status: 'ok', count: 3, items: [], next_cursor: '' }]
  });
  assert.equal(totalFound(all).total, 9);
  assert.equal(statusLine(all), '9 results');
});

test('findResult finds a file in a searchable folder by its key', () => {
  const snapshot = withIndex(local('a'), [hit('h1', 'a.txt', day(1))]);
  assert.equal(findResult(snapshot, 'indexed', 'idx:h1').label, 'a.txt');
  assert.equal(findResult(snapshot, 'indexed', 'idx:nope'), null);
});

// ---- the markup ---------------------------------------------------------------

test('a file in a searchable folder is a selectable row, not a tab', () => {
  const snapshot = withIndex(local('plan'), [hit('h1', 'plan.pdf', day(9))]);
  const html = renderResultsHTML({ ...snapshot, status: 'done' });
  assert.match(html, /data-result-open data-result-indexed aria-pressed="false"/);
  assert.match(html, /data-result-kind="indexed"/);
  assert.match(html, /data-result-reveal/, 'it can be shown in the tree');
  assert.doesNotMatch(html, /data-result-act/, 'no actions until it is selected');
});

test('selecting it shows Open in the reader, Reveal in Finder and Show in tree under its row', () => {
  const snapshot = {
    ...withIndex(local('plan'), [hit('h1', 'plan.pdf', day(9))]),
    status: 'done',
    selected: 'idx:h1'
  };
  const html = renderResultsHTML(snapshot);
  assert.match(html, /aria-pressed="true"/);
  assert.match(html, /data-result-act="open"[^>]*>Open in Preview</);
  assert.match(html, /data-result-act="reveal"[^>]*>Reveal in Finder</);
  assert.match(html, /cockpit-result-actions[\s\S]*data-result-reveal[^>]*>Show in tree</);
  // The row's own Show in tree gives way to the one among the actions.
  assert.equal([...html.matchAll(/data-result-reveal/g)].length, 1);
});

test('a file with no reader offers only Reveal; one with no workspace offers no Show in tree', () => {
  const snapshot = {
    ...withIndex(local('a'), [hit('h1', 'a.bin', day(9), { reader: '' })], {
      sources: [source('s1', 'Downloads', { workspace: null })]
    }),
    status: 'done',
    selected: 'idx:h1'
  };
  const html = renderResultsHTML(snapshot);
  assert.doesNotMatch(html, /data-result-act="open"/);
  assert.match(html, /data-result-act="reveal"/);
  assert.doesNotMatch(html, /data-result-reveal/);
});

test('what happened when a file was opened or refused is said under the status', () => {
  const html = renderResultsHTML({
    ...withIndex(local('a'), [hit('h1', 'a.txt', day(1))]),
    status: 'done',
    notice: 'That file changed since it was indexed.'
  });
  assert.match(html, /data-results-notice[^>]*>That file changed since it was indexed\.</);
});

test('the notice about a file is said under that file, not above a long list', () => {
  const base = withIndex(local('plan'), [
    hit('h1', 'plan.pdf', day(9)),
    hit('h2', 'other.pdf', day(8))
  ]);
  const html = renderResultsHTML({
    ...base,
    status: 'done',
    selected: 'idx:h1',
    notice: 'Sent to the document reader.',
    noticeAt: 'idx:h1'
  });
  assert.equal([...html.matchAll(/data-results-notice/g)].length, 1);
  assert.match(
    html,
    /cockpit-result-actions[\s\S]*cockpit-result-notice[^>]*data-results-notice[^>]*>Sent to the document reader\./
  );
  // A notice about the index as a whole (refreshing) is still said at the top.
  const top = renderResultsHTML({
    ...base,
    status: 'done',
    selected: 'idx:h1',
    notice: 'Refreshing the index…',
    noticeAt: ''
  });
  assert.doesNotMatch(top, /cockpit-result-notice/);
  assert.match(top, /cockpit-results-note[^>]*data-results-notice[^>]*>Refreshing the index/);
  // A notice about a file that is no longer in the results is not lost.
  const gone = renderResultsHTML({
    ...base,
    status: 'done',
    selected: 'idx:h1',
    notice: 'That file changed.',
    noticeAt: 'idx:gone'
  });
  assert.match(gone, /data-results-notice[^>]*>That file changed\./);
});

test('rows with and without Show in tree keep their columns in line', () => {
  const html = renderResultsHTML({
    ...withIndex(local('a'), [hit('h1', 'a.txt', day(9)), hit('h2', 'b.txt', day(8))], {
      sources: [source('s1', 'Downloads', { workspace: null })]
    }),
    status: 'done'
  });
  // Neither has a workspace to show in the tree; each keeps the room for it.
  assert.equal([...html.matchAll(/cockpit-result-reveal-slot/g)].length, 2);
  assert.doesNotMatch(html, /data-result-reveal/);
});

test('away from the Mac that runs Ori, one line says linked-folder filenames are not searched', () => {
  const html = renderResultsHTML({
    ...applyAnswer(searchingSnapshot('a'), {
      kinds: [filesKind([serverFile('a', 'a.txt', day(1))])]
    }),
    status: 'done'
  });
  assert.equal([...html.matchAll(/data-results-index-off/g)].length, 1);
  assert.match(html, new RegExp(INDEX_OFF_WORDS));
  // And the workspace files are still there.
  assert.match(html, /a\.txt/);
  // On the Mac that runs Ori the line is not said.
  assert.doesNotMatch(
    renderResultsHTML({ ...local('a'), status: 'done' }),
    /data-results-index-off/
  );
});

test('while the folders are still being asked the group says so', () => {
  const html = renderResultsHTML({
    ...withServer(local('a'), [serverFile('a', 'a.txt', day(1))]),
    status: 'done'
  });
  assert.match(html, /data-results-index-loading[^>]*>Searching your folders/);
});

test('a failed index is said with Retry, never an empty Files group', () => {
  const failed = {
    ...withServer(local('a'), []),
    status: 'done',
    index: { ...local('a').index, status: 'unavailable' }
  };
  const html = renderResultsHTML(failed);
  assert.match(html, /Files in your folders couldn’t be searched right now/);
  assert.match(html, /data-result-retry="index"/);
  assert.doesNotMatch(html, /data-results-group="files"/);

  // The server half failing while the folders answered says which part.
  const serverDown = {
    ...applyAnswer(local('a'), {
      kinds: [{ kind: 'files', status: 'unavailable', count: null, items: [] }]
    }),
    status: 'done'
  };
  const both = renderResultsHTML(withIndex(serverDown, [hit('h1', 'i.txt', day(1))]));
  assert.match(both, /Files stored in workspaces couldn’t be searched right now/);
  assert.match(both, /i\.txt/, 'the folders’ files are still shown');
  assert.match(both, /data-result-retry="files"/);
});

test('one notice under the results says which folders are only partly indexed, with Refresh index', () => {
  const html = renderResultsHTML({
    ...withIndex(withServer(local('a'), []), [hit('h1', 'a.txt', day(1))], {
      sources: [
        source('s1', 'Projects'),
        source('s2', 'Half done', { state: 'partial' }),
        source('s3', 'Working', { state: 'mapping' })
      ]
    }),
    status: 'done'
  });
  assert.equal([...html.matchAll(/data-results-partly-indexed/g)].length, 1);
  assert.match(html, /Half done, Working/);
  assert.match(html, /data-result-index-refresh[^>]*>Refresh index</);
  // Nothing about a folder that is ready.
  assert.doesNotMatch(html.match(/data-results-partly-indexed[\s\S]*?<\/p>/)[0], /Projects/);
});

test('the merged group shows a page, More results, and is final-rows only', () => {
  const server = Array.from({ length: 30 }, (_, i) => serverFile(`f${i}`, `f${i}`, day(30 - i)));
  const html = renderResultsHTML({
    ...withServer(local('f'), server.slice(0, 25), { count: 30, next_cursor: '25' }),
    status: 'done'
  });
  assert.equal([...html.matchAll(/data-result-open /g)].length, 25);
  assert.match(html, /data-result-more="files"/);
  assert.match(html, /Files <span class="cockpit-results-count">30\+?</);
});

// ---- the controller -------------------------------------------------------------

function makeTimers() {
  let next = 1;
  const pending = new Map();
  return {
    setTimeoutImpl(fn, ms) {
      const id = next++;
      pending.set(id, { fn, ms });
      return id;
    },
    clearTimeoutImpl: id => pending.delete(id),
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

const tick = () => new Promise(resolve => setImmediate(resolve));

// A fetch for the server half that answers every request with the next queued
// answer, or hands the test a deferred to resolve.
function makeServer() {
  const calls = [];
  const fetchImpl = (url, init) =>
    new Promise((resolve, reject) => {
      const call = { url, body: JSON.parse(init.body), resolve, reject };
      init.signal.addEventListener('abort', () => {
        const error = new Error('aborted');
        error.name = 'AbortError';
        reject(error);
      });
      calls.push(call);
    });
  const answer = (call, body) => call.resolve({ ok: true, json: async () => body });
  return { calls, fetchImpl, answer };
}

function makeIndex() {
  const queries = [];
  const acts = [];
  const refreshes = [];
  return {
    queries,
    acts,
    refreshes,
    indexSearch: params =>
      new Promise((resolve, reject) => queries.push({ params, resolve, reject })),
    indexAct: (op, body) =>
      new Promise((resolve, reject) => acts.push({ op, body, resolve, reject })),
    indexRefresh: body =>
      new Promise((resolve, reject) => refreshes.push({ body, resolve, reject }))
  };
}

function harness(extra = {}) {
  const timers = makeTimers();
  const server = makeServer();
  const index = makeIndex();
  const events = [];
  const search = createFilesSearch({
    onChange: (snapshot, reason) => events.push({ snapshot, reason }),
    fetchImpl: server.fetchImpl,
    setTimeoutImpl: timers.setTimeoutImpl,
    clearTimeoutImpl: timers.clearTimeoutImpl,
    indexSearch: index.indexSearch,
    indexAct: index.indexAct,
    indexRefresh: index.indexRefresh,
    ...extra
  });
  return { search, timers, server, index, events };
}

async function started(h, term = 'plan') {
  h.search.input(term);
  h.timers.flush();
  await tick();
}

test('the server and the filename index are asked together, the term in neither address', async () => {
  const h = harness();
  await started(h, 'plan');
  assert.equal(h.server.calls.length, 1);
  assert.equal(h.index.queries.length, 1);
  assert.deepEqual(h.index.queries[0].params, { term: 'plan', cursor: '', source: '' });
  assert.equal(h.search.snapshot().index.status, 'loading');
});

test('either answer shows as soon as it arrives', async () => {
  const h = harness();
  await started(h);
  h.index.queries[0].resolve({
    hits: [hit('h1', 'plan.pdf', day(9))],
    next: '',
    sources: [source('s1', 'Projects')]
  });
  await tick();
  assert.equal(h.search.snapshot().index.status, 'ok');
  assert.equal(h.search.snapshot().status, 'searching', 'the server has not answered yet');
  h.server.answer(h.server.calls[0], { kinds: [filesKind([serverFile('a', 'a.txt', day(10))])] });
  await tick();
  const snapshot = h.search.snapshot();
  assert.equal(snapshot.status, 'done');
  assert.deepEqual(
    mergedFiles(snapshot).items.map(i => i.label),
    ['a.txt', 'plan.pdf']
  );
});

test('a failed index is unavailable on its own while the server answers; Retry asks it alone', async () => {
  const h = harness();
  await started(h);
  h.index.queries[0].reject(new Error('Search is unavailable. Please try again.'));
  h.server.answer(h.server.calls[0], { kinds: [filesKind([serverFile('a', 'a.txt', day(10))])] });
  await tick();
  assert.equal(h.search.snapshot().index.status, 'unavailable');
  assert.equal(h.search.snapshot().kinds.files.status, 'ok');

  const retry = h.search.retry('index');
  await tick();
  assert.equal(h.index.queries.length, 2);
  assert.equal(h.server.calls.length, 1, 'the server is not asked again');
  h.index.queries[1].resolve({
    hits: [hit('h1', 'plan.pdf', day(9))],
    next: '',
    sources: [source('s1', 'Projects')]
  });
  await retry;
  assert.equal(h.search.snapshot().index.status, 'ok');
});

test('an answer from the index that lands after the term changed is thrown away', async () => {
  const h = harness();
  await started(h, 'old');
  h.search.input('new');
  h.index.queries[0].resolve({
    hits: [hit('h1', 'old.pdf', day(9))],
    next: '',
    sources: [source('s1', 'Projects')]
  });
  await tick();
  assert.equal(h.search.snapshot().term, 'new');
  assert.equal(h.search.snapshot().index.items.length, 0);
});

test('More results fetches the next page of whichever stream has more, until a page is final', async () => {
  const h = harness();
  await started(h, 'x');
  // 25 server files (day 30..6), and 25 index hits (day 29..5), both with more.
  const server = Array.from({ length: 25 }, (_, i) => serverFile(`s${i}`, `s${i}`, day(30 - i)));
  const hits = Array.from({ length: 25 }, (_, i) => hit(`h${i}`, `h${i}`, day(29 - i)));
  h.server.answer(h.server.calls[0], {
    kinds: [filesKind(server, { next_cursor: '25', count: 40 })]
  });
  h.index.queries[0].resolve({ hits, next: 'c1', sources: [source('s1', 'Projects')] });
  await tick();
  await tick();
  // Final rows are those as new as the newer boundary: day 6 vs day 5 -> day 6.
  assert.equal(mergedFiles(h.search.snapshot()).items.length, 2 * 25 - 1);
  // The first page is already 25 final rows, so nothing more was asked for.
  assert.equal(h.server.calls.length, 1);
  assert.equal(h.index.queries.length, 1);

  const more = h.search.loadMore('files');
  await tick();
  assert.equal(h.search.snapshot().filesShown, 50);
  // 49 final rows < 50 shown and both have more: both are asked for a page.
  assert.deepEqual(h.server.calls[1].body, {
    term: 'x',
    kinds: ['files'],
    cursors: { files: '25' }
  });
  assert.equal(h.index.queries[1].params.cursor, 'c1');
  h.server.answer(h.server.calls[1], {
    kinds: [filesKind([serverFile('s25', 's25', day(5) + '')], { count: 40 })]
  });
  h.index.queries[1].resolve({
    hits: [hit('h25', 'h25', day(4))],
    next: '',
    sources: [source('s1', 'Projects')]
  });
  await more;
  const labels = mergedFiles(h.search.snapshot()).items.map(i => i.label);
  assert.equal(labels.length, 52);
  assert.equal(labels[labels.length - 1], 'h25');
});

test('a page that fails stops the filling, so a broken stream cannot loop', async () => {
  const h = harness();
  await started(h, 'x');
  const server = Array.from({ length: 25 }, (_, i) => serverFile(`s${i}`, `s${i}`, day(30 - i)));
  h.server.answer(h.server.calls[0], {
    kinds: [filesKind(server, { next_cursor: '25', count: 80 })]
  });
  h.index.queries[0].resolve({ hits: [], next: '', sources: [source('s1', 'Projects')] });
  await tick();
  const more = h.search.loadMore('files');
  await tick();
  h.server.calls[1].reject(new Error('down'));
  await more;
  assert.equal(h.server.calls.length, 2, 'it did not ask again');
  assert.equal(h.search.snapshot().kinds.files.moreFailed, true);
});

test('a folder still being indexed is asked again a few times, then left alone', async () => {
  const h = harness();
  await started(h, 'x');
  h.server.answer(h.server.calls[0], { kinds: [filesKind([])] });
  const working = { hits: [], next: '', sources: [source('s1', 'Projects', { state: 'mapping' })] };
  h.index.queries[0].resolve(working);
  await tick();
  assert.equal(h.timers.size, 1, 'a poll is waiting');
  for (let i = 1; i <= 8; i += 1) {
    h.timers.flush();
    await tick();
    assert.equal(h.index.queries.length, i + 1);
    h.index.queries[i].resolve(working);
    await tick();
  }
  assert.equal(h.timers.size, 0, 'it gave up after a bounded number of tries');
});

test('the polling stops when the folder turns ready', async () => {
  const h = harness();
  await started(h, 'x');
  h.server.answer(h.server.calls[0], { kinds: [filesKind([])] });
  h.index.queries[0].resolve({
    hits: [],
    next: '',
    sources: [source('s1', 'P', { state: 'mapping' })]
  });
  await tick();
  h.timers.flush();
  await tick();
  h.index.queries[1].resolve({
    hits: [hit('h1', 'a.txt', day(1))],
    next: '',
    sources: [source('s1', 'P')]
  });
  await tick();
  assert.equal(h.timers.size, 0);
  assert.equal(h.search.snapshot().index.items.length, 1);
});

test('clearing the box cancels a poll that was waiting', async () => {
  const h = harness();
  await started(h, 'x');
  h.server.answer(h.server.calls[0], { kinds: [filesKind([])] });
  h.index.queries[0].resolve({
    hits: [],
    next: '',
    sources: [source('s1', 'P', { state: 'queued' })]
  });
  await tick();
  assert.equal(h.timers.size, 1);
  h.search.clear();
  assert.equal(h.timers.size, 0);
});

test('selecting a file, then opening it, tells the reader and says so', async () => {
  const h = harness();
  await started(h);
  h.server.answer(h.server.calls[0], { kinds: [filesKind([])] });
  h.index.queries[0].resolve({
    hits: [hit('h1', 'plan.pdf', day(9))],
    next: '',
    sources: [source('s1', 'Projects')]
  });
  await tick();

  h.search.select('idx:h1');
  assert.equal(h.search.snapshot().selected, 'idx:h1');
  const opening = h.search.dispatch('idx:h1', 'open');
  await tick();
  assert.deepEqual(h.index.acts[0].op, 'open');
  // The same select and dispatch calls the standalone page makes: an id and its
  // generation, nothing about the term.
  assert.deepEqual(h.index.acts[0].body, { id: 'h1', generation: 3 });
  h.index.acts[0].resolve(null);
  await opening;
  assert.equal(h.search.snapshot().notice, 'Sent to the document reader.');

  const revealing = h.search.dispatch('idx:h1', 'reveal');
  await tick();
  h.index.acts[1].resolve(null);
  await revealing;
  assert.equal(h.search.snapshot().notice, 'Requested in Finder.');
});

test('a moved or replaced file is refused in the server’s own words', async () => {
  const h = harness();
  await started(h);
  h.server.answer(h.server.calls[0], { kinds: [filesKind([])] });
  h.index.queries[0].resolve({
    hits: [hit('h1', 'plan.pdf', day(9))],
    next: '',
    sources: [source('s1', 'Projects')]
  });
  await tick();
  const opening = h.search.dispatch('idx:h1', 'open');
  await tick();
  h.index.acts[0].reject(new Error('This file is no longer in the index. Refresh the folder.'));
  await opening;
  assert.equal(
    h.search.snapshot().notice,
    'This file is no longer in the index. Refresh the folder.'
  );
  assert.equal(h.events[h.events.length - 1].reason, 'notice');
});

test('Refresh index re-indexes only the folders that were not ready, then asks again', async () => {
  const h = harness();
  await started(h);
  h.server.answer(h.server.calls[0], { kinds: [filesKind([])] });
  h.index.queries[0].resolve({
    hits: [hit('h1', 'a.txt', day(1))],
    next: '',
    sources: [source('s1', 'Done'), source('s2', 'Half', { state: 'partial', generation: 11 })]
  });
  await tick();
  const refreshing = h.search.refreshIndex();
  await tick();
  assert.equal(h.index.refreshes.length, 1);
  assert.deepEqual(h.index.refreshes[0].body, { id: 's2', generation: 11 });
  h.index.refreshes[0].resolve(null);
  await tick();
  h.index.queries[1].resolve({
    hits: [hit('h1', 'a.txt', day(1)), hit('h2', 'b.txt', day(1))],
    next: '',
    sources: [source('s1', 'Done'), source('s2', 'Half')]
  });
  await refreshing;
  assert.equal(h.search.snapshot().index.items.length, 2);
  assert.equal(partlyIndexed(h.search.snapshot()).length, 0);
  assert.equal(h.search.snapshot().notice, '');
});

test('without a filename index nothing is asked of one, and the snapshot says it is off', async () => {
  const timers = makeTimers();
  const server = makeServer();
  const events = [];
  const search = createFilesSearch({
    onChange: (snapshot, reason) => events.push(reason),
    fetchImpl: server.fetchImpl,
    setTimeoutImpl: timers.setTimeoutImpl,
    clearTimeoutImpl: timers.clearTimeoutImpl
  });
  search.input('plan');
  assert.equal(search.snapshot().index.status, 'off');
  timers.flush();
  await tick();
  server.answer(server.calls[0], { kinds: [filesKind([serverFile('a', 'a.txt', day(1))])] });
  await tick();
  assert.equal(search.snapshot().index.status, 'off');
  assert.deepEqual(events, ['typed', 'results']);
  // Select and dispatch are inert without an index.
  await search.dispatch('idx:h1', 'open');
  assert.equal(search.snapshot().notice, '');
});

test('a whole search with the index touches no browser storage, address or window name', async () => {
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
  const names = ['localStorage', 'sessionStorage', 'history', 'location', 'document'];
  const saved = Object.fromEntries(
    names.map(n => [n, Object.getOwnPropertyDescriptor(globalThis, n)])
  );
  names.forEach(n =>
    Object.defineProperty(globalThis, n, { value: trap(n), configurable: true, writable: true })
  );
  try {
    const h = harness();
    await started(h, 'zebra-secret-term');
    h.server.answer(h.server.calls[0], { kinds: [filesKind([])] });
    h.index.queries[0].resolve({
      hits: [hit('h1', 'zebra.txt', day(1))],
      next: 'c',
      sources: [source('s1', 'P')]
    });
    await tick();
    h.search.select('idx:h1');
    void h.search.dispatch('idx:h1', 'reveal');
    await tick();
    h.index.acts[0].resolve(null);
    await tick();
    h.search.clear();
  } finally {
    names.forEach(n => {
      if (saved[n]) Object.defineProperty(globalThis, n, saved[n]);
      else delete globalThis[n];
    });
  }
  assert.deepEqual(touched, []);
});

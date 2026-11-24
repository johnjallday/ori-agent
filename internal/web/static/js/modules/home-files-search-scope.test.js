// Tests for searching in one workspace, group or folder instead of everywhere
// (merge-tree-into-files FR22): the scope helpers, and what each kind of scope
// asks the server and the filename index.
//   node --test internal/web/static/js/modules/home-files-search-scope.test.js

import test from 'node:test';
import assert from 'node:assert/strict';

import {
  applyIndexAnswer,
  createFilesSearch,
  decodePages,
  decodeScope,
  encodePages,
  encodeScope,
  renderResultsHTML,
  requestScope,
  scopeAsksIndex,
  scopeAsksServer,
  scopeKinds,
  scopeLabel,
  searchingSnapshot,
  serverSkipped
} from './home-files-search.js';

const workspace = {
  kind: 'workspace',
  name: 'Harbor Album',
  workspaceId: 'w1',
  workspaceIds: ['w1']
};
const group = {
  kind: 'group',
  name: 'Studio',
  workspaceId: 'g1',
  workspaceIds: ['g1', 'w1', 'w2']
};
const folder = {
  kind: 'folder',
  name: 'Mixes',
  workspaceId: 'w1',
  section: 'files',
  path: 'audio/mixes'
};
const outputs = {
  kind: 'folder',
  name: 'renders',
  workspaceId: 'w1',
  section: 'outputs',
  path: ''
};
const linked = {
  kind: 'linked',
  name: 'Downloads',
  workspaceId: 'w1',
  sourceId: 'src-9',
  workspaceIds: ['w1']
};

// ---- the helpers --------------------------------------------------------------

test('the chip reads "in <name>", and says nothing with no scope', () => {
  assert.equal(scopeLabel(workspace), 'in Harbor Album');
  assert.equal(scopeLabel(folder), 'in Mixes');
  assert.equal(scopeLabel(null), '');
  assert.equal(scopeLabel({}), '');
});

test('what the by-name search is told depends on the scope', () => {
  assert.equal(requestScope(null), null);
  assert.deepEqual(requestScope(workspace), { workspace_id: 'w1' });
  // A group is its id: the server covers its whole subtree.
  assert.deepEqual(requestScope(group), { workspace_id: 'g1' });
  assert.deepEqual(requestScope(folder), {
    workspace_id: 'w1',
    folder: { section: 'files', path: 'audio/mixes' }
  });
  assert.deepEqual(requestScope(outputs).folder, { section: 'outputs', path: '' });
  // A linked folder has nothing in the server to ask.
  assert.equal(requestScope(linked), null);
});

test('a folder is files only; a linked folder is the filename index alone; a workspace is everything', () => {
  assert.deepEqual(scopeKinds(folder), ['files']);
  assert.equal(scopeKinds(workspace), null);
  assert.equal(scopeKinds(group), null);
  assert.equal(scopeKinds(null), null);

  assert.equal(scopeAsksServer(linked), false);
  assert.equal(scopeAsksServer(workspace), true);
  assert.equal(scopeAsksServer(null), true);

  assert.equal(scopeAsksIndex(folder), false);
  assert.equal(scopeAsksIndex(linked), true);
  assert.equal(scopeAsksIndex(workspace), true);
  assert.equal(scopeAsksIndex(null), true);
});

test('a workspace scope keeps only that scope’s files from the filename index', () => {
  const answer = {
    hits: [
      {
        id: 'a',
        generation: 1,
        name: 'in-scope.txt',
        parent: '',
        modified: '2026-01-01T00:00:00Z',
        source: 's1'
      },
      {
        id: 'b',
        generation: 1,
        name: 'elsewhere.txt',
        parent: '',
        modified: '2026-01-01T00:00:00Z',
        source: 's2'
      },
      {
        id: 'c',
        generation: 1,
        name: 'no-workspace.txt',
        parent: '',
        modified: '2026-01-01T00:00:00Z',
        source: 's3'
      }
    ],
    next: '',
    sources: [
      { id: 's1', label: 'One', state: 'ready', workspace: { id: 'w1', link: 'd1' } },
      { id: 's2', label: 'Two', state: 'ready', workspace: { id: 'w9', link: 'd2' } },
      { id: 's3', label: 'Three', state: 'ready' }
    ]
  };
  const start = searchingSnapshot('txt', { index: true });
  const all = applyIndexAnswer(start, answer);
  assert.equal(all.index.items.length, 3);
  const scoped = applyIndexAnswer(start, answer, { only: ['w1', 'w2'] });
  assert.deepEqual(
    scoped.index.items.map(i => i.label),
    ['in-scope.txt']
  );
});

test('a skipped server leaves every kind empty and done, not waiting', () => {
  const snapshot = serverSkipped(searchingSnapshot('a', { index: true }));
  assert.equal(snapshot.status, 'done');
  for (const state of Object.values(snapshot.kinds)) {
    assert.equal(state.status, 'ok');
    assert.equal(state.count, 0);
    assert.deepEqual(state.items, []);
  }
});

test('a folder scope says nothing about the filename index: it was never asked', () => {
  const none = searchingSnapshot('a', { index: 'none' });
  assert.equal(none.index.status, 'none');
  const html = renderResultsHTML({ ...none, status: 'done' });
  assert.doesNotMatch(html, /data-results-index-off/);
  assert.doesNotMatch(html, /data-results-index-loading/);
  // A search with no index at all still says so, once.
  assert.match(
    renderResultsHTML({ ...searchingSnapshot('a'), status: 'done' }),
    /data-results-index-off/
  );
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
    delays: () => [...pending.values()].map(t => t.ms),
    flush() {
      const due = [...pending.entries()];
      pending.clear();
      due.forEach(([, t]) => t.fn());
    }
  };
}

const tick = () => new Promise(resolve => setImmediate(resolve));

function harness(scope) {
  const timers = makeTimers();
  const calls = [];
  const queries = [];
  const events = [];
  let current = scope;
  const search = createFilesSearch({
    onChange: (snapshot, reason) => events.push({ snapshot, reason }),
    getScope: () => current,
    fetchImpl: (url, init) =>
      new Promise((resolve, reject) => {
        const call = { body: JSON.parse(init.body), resolve, reject };
        init.signal.addEventListener('abort', () => {
          const error = new Error('aborted');
          error.name = 'AbortError';
          reject(error);
        });
        calls.push(call);
      }),
    setTimeoutImpl: timers.setTimeoutImpl,
    clearTimeoutImpl: timers.clearTimeoutImpl,
    indexSearch: params =>
      new Promise((resolve, reject) => queries.push({ params, resolve, reject }))
  });
  return {
    search,
    timers,
    calls,
    queries,
    events,
    setScope: next => {
      current = next;
    }
  };
}

const ok = body => ({ ok: true, json: async () => body });
const emptyAnswer = { kinds: [] };

test('a workspace scope narrows the by-name search and asks the index unscoped, then filters', async () => {
  const h = harness(workspace);
  h.search.input('plan');
  h.timers.flush();
  await tick();
  assert.deepEqual(h.calls[0].body, { term: 'plan', scope: { workspace_id: 'w1' } });
  // The filename index is asked for everything; the answer is cut to the scope.
  assert.deepEqual(h.queries[0].params, { term: 'plan', cursor: '', source: '' });
  h.queries[0].resolve({
    hits: [
      {
        id: 'a',
        generation: 1,
        name: 'mine.txt',
        parent: '',
        modified: '2026-01-01T00:00:00Z',
        source: 's1'
      },
      {
        id: 'b',
        generation: 1,
        name: 'theirs.txt',
        parent: '',
        modified: '2026-01-02T00:00:00Z',
        source: 's2'
      }
    ],
    next: '',
    sources: [
      { id: 's1', label: 'One', state: 'ready', workspace: { id: 'w1', link: 'd1' } },
      { id: 's2', label: 'Two', state: 'ready', workspace: { id: 'w9', link: 'd2' } }
    ]
  });
  h.calls[0].resolve(ok(emptyAnswer));
  await tick();
  assert.deepEqual(
    h.search.snapshot().index.items.map(i => i.label),
    ['mine.txt']
  );
});

test('a folder scope asks for files only, inside that folder, and never asks the filename index', async () => {
  const h = harness(folder);
  h.search.input('mix');
  assert.equal(h.search.snapshot().index.status, 'none');
  h.timers.flush();
  await tick();
  assert.deepEqual(h.calls[0].body, {
    term: 'mix',
    scope: { workspace_id: 'w1', folder: { section: 'files', path: 'audio/mixes' } },
    kinds: ['files']
  });
  assert.equal(h.queries.length, 0);
});

test('a linked-folder scope asks only the filename index, for that folder', async () => {
  const h = harness(linked);
  h.search.input('plan');
  h.timers.flush();
  await tick();
  assert.equal(h.calls.length, 0, 'the server is not asked');
  assert.deepEqual(h.queries[0].params, { term: 'plan', cursor: '', source: 'src-9' });
  const snapshot = h.search.snapshot();
  assert.equal(snapshot.status, 'done', 'nothing is waiting on the server');
  assert.equal(snapshot.kinds.notes.status, 'ok');
});

test('changing the scope asks again, at once, for the same term', async () => {
  const h = harness(null);
  h.search.input('plan');
  h.timers.flush();
  await tick();
  assert.equal(h.calls.length, 1);
  assert.deepEqual(h.calls[0].body, { term: 'plan' });

  h.setScope(workspace);
  h.search.rescope();
  // No pause: the new request follows on the next turn, and the old one is cancelled.
  assert.deepEqual(h.timers.delays(), [0]);
  assert.equal(h.events[h.events.length - 1].reason, 'typed');
  h.timers.flush();
  await tick();
  assert.deepEqual(h.calls[1].body, { term: 'plan', scope: { workspace_id: 'w1' } });
  // A late answer to the unscoped search is thrown away.
  h.calls[0].resolve(
    ok({ kinds: [{ kind: 'notes', status: 'ok', count: 1, items: [], next_cursor: '' }] })
  );
  await tick();
  assert.equal(h.search.snapshot().status, 'searching');
});

test('rescoping with nothing typed does nothing', () => {
  const h = harness(null);
  h.search.rescope();
  assert.equal(h.events.length, 0);
  assert.equal(h.search.snapshot(), null);
});

test('More results and Retry keep the scope', async () => {
  const h = harness(workspace);
  h.search.input('plan');
  h.timers.flush();
  await tick();
  h.queries[0].resolve({ hits: [], next: '', sources: [] });
  h.calls[0].resolve(
    ok({
      kinds: [
        {
          kind: 'notes',
          status: 'ok',
          count: 30,
          items: [
            {
              kind: 'note',
              id: 'n1',
              label: 'A',
              row_key: 'w1/n/n1',
              workspace: { id: 'w1', name: 'Harbor Album' },
              location: []
            }
          ],
          next_cursor: '25'
        }
      ]
    })
  );
  await tick();
  void h.search.loadMore('notes');
  await tick();
  assert.deepEqual(h.calls[1].body, {
    term: 'plan',
    scope: { workspace_id: 'w1' },
    kinds: ['notes'],
    cursors: { notes: '25' }
  });
});

// ---- what a tab remembers across a Back ------------------------------------------

test('a scope is saved small, without what can be worked out again, and read back whole', () => {
  assert.equal(encodeScope(null), '');
  const text = encodeScope(group);
  assert.ok(text.length < 600);
  assert.doesNotMatch(text, /workspaceIds/);
  const back = decodeScope(text, {
    exists: id => id === 'g1',
    members: () => ['w1', 'w2']
  });
  assert.equal(back.ok, true);
  assert.deepEqual(back.scope, group);
  assert.deepEqual(decodeScope(encodeScope(folder)).scope, folder);
  assert.deepEqual(decodeScope(encodeScope(outputs)).scope, outputs);
  assert.deepEqual(decodeScope(encodeScope(linked)).scope, linked);
  assert.deepEqual(decodeScope(''), { ok: true, scope: null });
});

test('a scope too big to keep is not saved at all, rather than saved short', () => {
  assert.equal(encodeScope({ ...workspace, name: 'x'.repeat(700) }), null);
});

test('a saved scope that no longer holds is refused, never half-restored', () => {
  const text = encodeScope(workspace);
  assert.equal(decodeScope(text, { exists: () => false }).ok, false);
  assert.equal(decodeScope('not json').ok, false);
  assert.equal(decodeScope('{"kind":"galaxy","workspaceId":"w1"}').ok, false);
  assert.equal(decodeScope('{"kind":"workspace"}').ok, false);
  assert.equal(decodeScope('{"kind":"folder","workspaceId":"w1","section":"notes"}').ok, false);
  assert.equal(decodeScope('{"kind":"linked","workspaceId":"w1"}').ok, false);
  assert.equal(decodeScope('[1,2]').ok, false);
  assert.equal(decodeScope('null').ok, false);
});

test('only the pages past the first are remembered, and read back bounded', () => {
  const snapshot = searchingSnapshot('a');
  assert.equal(encodePages(snapshot), '');
  assert.equal(encodePages(null), '');
  assert.deepEqual(decodePages(''), {});
  assert.deepEqual(decodePages('notes:2,files:3'), { notes: 2, files: 3 });
  assert.deepEqual(decodePages('notes:1,galaxy:4,files:999,tickets:x'), { files: 40 });
  assert.deepEqual(decodePages(null), {});
});

test('a restored search asks at once, then brings back the pages and the selected file', async () => {
  const h = harness(null);
  const item = (id, label) => ({
    kind: 'note',
    id,
    label,
    row_key: `w1/n/${id}`,
    workspace: { id: 'w1', name: 'Harbor' },
    location: []
  });
  const restoring = h.search.restore({ term: 'plan', selected: '', pages: { notes: 2 } });
  await tick();
  // Asked right away: no waiting on the typing pause.
  assert.equal(h.timers.delays().length, 0);
  assert.equal(h.calls.length, 1);
  assert.deepEqual(h.calls[0].body, { term: 'plan' });
  h.queries[0].resolve({ hits: [], next: '', sources: [] });
  h.calls[0].resolve(
    ok({
      kinds: [
        { kind: 'notes', status: 'ok', count: 30, items: [item('n1', 'A')], next_cursor: '25' }
      ]
    })
  );
  await tick();
  // The second page of notes, as it was on screen.
  assert.equal(h.calls.length, 2);
  assert.deepEqual(h.calls[1].body, {
    term: 'plan',
    kinds: ['notes'],
    cursors: { notes: '25' }
  });
  h.calls[1].resolve(
    ok({
      kinds: [{ kind: 'notes', status: 'ok', count: 30, items: [item('n2', 'B')], next_cursor: '' }]
    })
  );
  await restoring;
  assert.equal(h.search.snapshot().kinds.notes.items.length, 2);
});

test('a restored search selects the file again only when it is still there', async () => {
  const run = async selected => {
    const h = harness(null);
    const restoring = h.search.restore({ term: 'plan', selected, pages: {} });
    await tick();
    h.queries[0].resolve({
      hits: [
        {
          id: 'a',
          generation: 1,
          name: 'plan.txt',
          parent: '',
          modified: '2026-01-01T00:00:00Z',
          source: 's1'
        }
      ],
      next: '',
      sources: [{ id: 's1', label: 'One', state: 'ready', workspace: { id: 'w1', link: 'd1' } }]
    });
    h.calls[0].resolve(ok(emptyAnswer));
    await restoring;
    return h.search.snapshot();
  };
  const first = await run('');
  const key = first.index.items[0].rowKey;
  assert.equal(first.selected, '');
  assert.equal((await run(key)).selected, key);
  assert.equal((await run('no/such/file')).selected, '');
});

test('restoring nothing, or a blank term, asks nothing', async () => {
  const h = harness(null);
  await h.search.restore({});
  await h.search.restore({ term: '   ' });
  assert.equal(h.calls.length, 0);
  assert.equal(h.search.snapshot(), null);
});

test('a search-only folder is a scope of its own: the filename index alone, no workspace', () => {
  const source = { kind: 'source', name: 'Old projects', sourceId: 'src-9' };
  assert.equal(scopeLabel(source), 'in Old projects');
  assert.equal(requestScope(source), null);
  assert.equal(scopeAsksServer(source), false);
  assert.equal(scopeAsksIndex(source), true);
  assert.equal(scopeKinds(source), null);
  // Saved and read back without a workspace; a scope with no source is not one.
  const text = encodeScope(source);
  assert.deepEqual(decodeScope(text, { exists: () => false }), { ok: true, scope: source });
  assert.deepEqual(decodeScope(JSON.stringify({ kind: 'source', name: 'x' })), { ok: false });
});

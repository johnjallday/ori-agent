import test from 'node:test';
import assert from 'node:assert/strict';
import { createWorkspaceUpdates } from './workspace-updates.js';

const summary = (id, unread = 1) => ({
  workspace_id: id,
  folder_slug: `route-${id}`,
  name: 'Desk',
  welcome: { state: 'ready', unread_count: unread },
  review: { state: 'ready', pending_count: 10, batch_id: `batch-${id}` }
});
const page = (items, hasMore = false, nextAfter = '') => ({
  items,
  has_more: hasMore,
  ...(nextAfter ? { next_after: nextAfter } : {})
});
const response = (data, status = 200) => ({ ok: status === 200, status, json: async () => data });
const deferred = () => {
  let resolve;
  const promise = new Promise(done => {
    resolve = done;
  });
  return { promise, resolve };
};
const settle = () => new Promise(done => setImmediate(done));

test('one coalesced bounded page feeds the same per-workspace cache; continuation is explicit', async () => {
  const wait = deferred();
  const calls = [];
  const updates = createWorkspaceUpdates({
    fetchImpl: (url, init) => {
      calls.push({ url, init });
      return wait.promise;
    }
  });
  const first = updates.refreshPage();
  assert.equal(updates.refreshPage(), first);
  wait.resolve(response(page([summary('a'), summary('b')], true, 'b')));
  await first;
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, '/api/workspace-updates?limit=100');
  assert.equal(calls[0].init.method, undefined);
  assert.equal(calls[0].init.cache, 'no-store');
  assert.deepEqual(updates.readPage().ids, ['a', 'b']);
  assert.equal(updates.readPage().hasMore, true);
  assert.equal(updates.readPage().nextAfter, 'b');
  assert.equal(updates.read('a').data.review.pending_count, 10);
  assert.equal(updates.read('not-checked').state, 'loading');
});

test('a partial/empty page never erases another page; complete first-page omission clears old authority', async () => {
  let next = page([summary('a')], true, 'a');
  const updates = createWorkspaceUpdates({ fetchImpl: async () => response(next) });
  await updates.refreshPage();
  next = page([], true, 'b');
  await updates.refreshPage({ after: 'a' });
  assert.equal(updates.readPage().hasMore, true);
  assert.equal(updates.read('a').data.welcome.unread_count, 1);
  next = page([], false);
  await updates.refreshPage({ after: 'b' });
  assert.equal(updates.read('a').state, 'ready');
  await updates.refreshPage();
  assert.equal(updates.read('a').state, 'not_found');
  assert.equal(updates.read('a').data, null);
});

test('an aggregate requested before an exact acknowledgement cannot restore its old unread count', async () => {
  const old = deferred();
  let reads = 0;
  const updates = createWorkspaceUpdates({
    fetchImpl: async (_url, init) => {
      if (init.method === 'POST') return response(summary('a', 0));
      if (++reads === 1) return old.promise;
      return response(page([summary('a', 0)]));
    }
  });
  const request = updates.refreshPage();
  assert.equal(
    await updates.acknowledge('a', { session_id: 's', message_id: 'm', revision: 1 }),
    true
  );
  old.resolve(response(page([summary('a', 1)])));
  await request;
  await settle();
  assert.equal(updates.read('a').data.welcome.unread_count, 0);
  assert.equal(updates.read('a').data.review.pending_count, 10);
  assert.equal(reads, 2, 'one trailing canonical read, not an unbounded traversal');
});

test('newer individual reads win over delayed pages and older individual reads cannot overwrite pages', async () => {
  for (const pageFirst of [true, false]) {
    const old = deferred();
    let calls = 0;
    const updates = createWorkspaceUpdates({
      fetchImpl: async url => {
        if (++calls === 1) return old.promise;
        return response(
          url.startsWith('/api/workspace-updates?') ? page([summary('a', 0)]) : summary('a', 0)
        );
      }
    });
    const pending = pageFirst ? updates.refreshPage() : updates.refresh('a');
    await (pageFirst ? updates.refresh('a') : updates.refreshPage());
    old.resolve(response(pageFirst ? page([summary('a')]) : summary('a')));
    await pending;
    assert.equal(updates.read('a').data.welcome.unread_count, 0);
  }
});

test('page failures retain qualified observations and malformed pagination is not an all-clear', async () => {
  let next = response(page([summary('a')], true, 'a'));
  const updates = createWorkspaceUpdates({ fetchImpl: async () => next });
  await updates.refreshPage();
  for (const invalid of [
    response(null, 503),
    response({ items: [] }),
    response(page([], true)),
    response(page([], true, 'a')),
    response(page([summary('b'), summary('b')]))
  ]) {
    next = invalid;
    await updates.refreshPage({ after: 'a' });
    assert.equal(updates.readPage().state, 'unavailable');
    assert.equal(updates.readPage().stale, true);
    assert.deepEqual(updates.readPage().ids, ['a']);
    assert.equal(updates.read('a').data.review.pending_count, 10);
  }
  next = response(null, 403);
  await updates.refreshPage();
  assert.equal(updates.read('a').data, null);
  assert.deepEqual(updates.readPage().ids, []);
});

test('reset rejects delayed pages and does not run another queued page under the new context', async () => {
  const wait = deferred();
  let calls = 0;
  const updates = createWorkspaceUpdates({
    fetchImpl: () => {
      calls++;
      return wait.promise;
    }
  });
  const pending = updates.refreshPage();
  const queued = updates.refreshPage({ after: 'a' });
  updates.reset();
  wait.resolve(response(page([summary('a')], true, 'a')));
  await Promise.all([pending, queued]);
  assert.equal(calls, 1);
  assert.equal(updates.read('a').data, null);
  assert.deepEqual(updates.readPage().ids, []);
});

test('commit hints during a page coalesce into one trailing read; subscriptions dispose', async () => {
  const wait = deferred();
  let calls = 0;
  let notices = 0;
  const updates = createWorkspaceUpdates({
    fetchImpl: () => {
      calls++;
      return calls === 1 ? wait.promise : Promise.resolve(response(page([summary('b')])));
    }
  });
  const stop = updates.subscribePage(() => notices++);
  const pending = updates.refreshPage();
  updates.invalidatePage();
  updates.invalidatePage();
  wait.resolve(response(page([summary('a')])));
  await pending;
  await settle();
  assert.equal(calls, 2);
  assert.deepEqual(updates.readPage().ids, ['b']);
  const before = notices;
  stop();
  await updates.refreshPage();
  assert.equal(notices, before);
});

test('settled cache is bounded; eviction means unknown, not zero, and the visible page remains', async () => {
  let chunk = 0;
  const updates = createWorkspaceUpdates({
    maxEntries: 200,
    fetchImpl: async () => {
      const items = Array.from({ length: 100 }, (_, n) => summary(`w-${chunk * 100 + n}`));
      chunk++;
      return response(page(items, true, `cursor-${chunk}`));
    }
  });
  await updates.refreshPage();
  await updates.refreshPage({ after: 'cursor-1' });
  await updates.refreshPage({ after: 'cursor-2' });
  assert.equal(updates.read('w-0').state, 'loading');
  assert.equal(updates.read('w-0').data, null);
  assert.equal(updates.read('w-200').data.review.pending_count, 10);
  assert.equal(updates.read('w-299').state, 'ready');
  assert.equal(updates.readPage().ids.length, 100);
  assert.equal(chunk, 3);
});

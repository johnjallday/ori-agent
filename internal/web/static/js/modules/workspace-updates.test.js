import test from 'node:test';
import assert from 'node:assert/strict';
import {
  createWorkspaceUpdates,
  exposedWelcomeMessages,
  watchWorkspaceUpdates
} from './workspace-updates.js';

const summary = (id = 'ws', seen = false) => ({
  workspace_id: id,
  welcome: {
    state: 'ready',
    session_id: 'chat',
    unread_count: seen ? 0 : 1,
    messages: [{ message_id: 'hello', revision: 1, ...(seen ? { seen_at: 'saved-time' } : {}) }]
  }
});
const response = (data, status = 200) => ({ ok: status === 200, status, json: async () => data });
const deferred = () => {
  let resolve;
  const promise = new Promise(done => {
    resolve = done;
  });
  return { promise, resolve };
};

test('summary reads coalesce and never deliver, acknowledge or start work', async () => {
  const wait = deferred();
  const calls = [];
  const updates = createWorkspaceUpdates({
    fetchImpl: (url, init) => {
      calls.push({ url, init });
      return wait.promise;
    }
  });
  const first = updates.refresh('ws');
  const second = updates.refresh('ws');
  assert.equal(first, second);
  wait.resolve(response(summary()));
  await first;
  assert.equal(updates.read('ws').data.welcome.unread_count, 1);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, '/api/workspaces/ws/updates');
  assert.equal(calls[0].init.method, undefined);
});

test('an old GET cannot resurrect unread after an exact acknowledgement', async () => {
  const wait = deferred();
  const calls = [];
  const updates = createWorkspaceUpdates({
    fetchImpl: async (url, init) => {
      calls.push({ url, init });
      return init.method === 'POST' ? response(summary('ws', true)) : wait.promise;
    }
  });
  const oldRead = updates.refresh('ws');
  assert.equal(
    await updates.acknowledge('ws', {
      session_id: 'chat',
      message_id: 'hello',
      revision: 1,
      owner: 'ignored'
    }),
    true
  );
  wait.resolve(response(summary()));
  await oldRead;
  assert.equal(updates.read('ws').data.welcome.unread_count, 0);
  assert.deepEqual(JSON.parse(calls[1].init.body), {
    session_id: 'chat',
    message_id: 'hello',
    revision: 1
  });
});

test('failed reads preserve qualified last-known state; missing/foreign clears it', async () => {
  let next = response(summary());
  const updates = createWorkspaceUpdates({ fetchImpl: async () => next });
  await updates.refresh('ws');
  next = response(null, 503);
  await updates.refresh('ws');
  assert.equal(updates.read('ws').state, 'unavailable');
  assert.equal(updates.read('ws').stale, true);
  assert.equal(updates.read('ws').data.welcome.unread_count, 1);
  next = response(null, 404);
  await updates.refresh('ws');
  assert.equal(updates.read('ws').state, 'not_found');
  assert.equal(updates.read('ws').data, null);
});

test('a source failure never turns its last observation into a ready zero', async () => {
  let next = summary();
  const updates = createWorkspaceUpdates({ fetchImpl: async () => response(next) });
  await updates.refresh('ws');
  next = { workspace_id: 'ws', welcome: { state: 'unavailable' } };
  await updates.refresh('ws');
  assert.deepEqual(updates.read('ws').data.welcome, {
    ...summary().welcome,
    state: 'unavailable',
    stale: true
  });
  next = summary('ws', true);
  await updates.refresh('ws');
  assert.equal(updates.read('ws').data.welcome.unread_count, 0);
  assert.equal(updates.read('ws').data.welcome.stale, undefined);
});

test('owner/context reset rejects late responses and clears cached observations', async () => {
  const wait = deferred();
  const updates = createWorkspaceUpdates({ fetchImpl: () => wait.promise });
  const read = updates.refresh('ws');
  updates.reset();
  wait.resolve(response(summary()));
  await read;
  assert.equal(updates.read('ws').data, null);
});

test('lost acknowledgement response is not treated as a local seen write', async () => {
  const updates = createWorkspaceUpdates({
    fetchImpl: async (_url, init) => {
      if (init.method === 'POST') throw new Error('lost reply');
      return response(summary());
    }
  });
  await updates.refresh('ws');
  assert.equal(
    await updates.acknowledge('ws', { session_id: 'chat', message_id: 'hello', revision: 1 }),
    false
  );
  assert.equal(updates.read('ws').data.welcome.unread_count, 1);
  assert.equal(updates.read('ws').stale, true);
});

function exposure() {
  return {
    source: summary().welcome,
    sessionId: 'chat',
    open: true,
    visible: true,
    focused: true,
    settled: true,
    blocked: false,
    viewport: { top: 100, bottom: 400, left: 0, right: 300, height: 300 },
    rows: [
      {
        messageId: 'hello',
        sessionId: 'chat',
        bounds: { top: 120, bottom: 220, height: 100, left: 0, right: 300 }
      }
    ]
  };
}

test('only an exact visible eligible message is exposed', () => {
  const input = exposure();
  assert.deepEqual(exposedWelcomeMessages(input), input.source.messages);
  for (const field of ['open', 'visible', 'focused', 'settled']) {
    assert.deepEqual(exposedWelcomeMessages({ ...input, [field]: false }), [], field);
  }
  assert.deepEqual(exposedWelcomeMessages({ ...input, blocked: true }), []);
  assert.deepEqual(exposedWelcomeMessages({ ...input, sessionId: 'other' }), []);
  assert.deepEqual(
    exposedWelcomeMessages({ ...input, source: { ...input.source, state: 'unavailable' } }),
    []
  );
  assert.deepEqual(exposedWelcomeMessages({ ...input, rows: [] }), []); // compact preview/hidden hydration
  assert.deepEqual(
    exposedWelcomeMessages({ ...input, rows: [{ ...input.rows[0], messageId: 'ordinary-reply' }] }),
    []
  );
  assert.deepEqual(
    exposedWelcomeMessages({ ...input, rows: [{ ...input.rows[0], sessionId: 'other' }] }),
    []
  );
});

test('long messages require their beginning and 80px; scroll-away never counts', () => {
  const input = exposure();
  const shown = top =>
    exposedWelcomeMessages({
      ...input,
      rows: [{ ...input.rows[0], bounds: { ...input.rows[0].bounds, top, height: 900 } }]
    });
  assert.equal(shown(120).length, 1);
  assert.equal(shown(320).length, 1);
  assert.equal(shown(321).length, 0);
  assert.equal(shown(50).length, 0);
  input.rows[0].bounds.height = 20;
  input.rows[0].bounds.top = 380;
  assert.equal(exposedWelcomeMessages(input).length, 1);
  input.source.messages[0].seen_at = 'saved';
  assert.equal(exposedWelcomeMessages(input).length, 0);
});

test('independent source failure retains only qualified observations', async () => {
  let data = { ...summary(), review: { state: 'ready', pending_count: 10, batch_id: 'batch' } };
  const updates = createWorkspaceUpdates({ fetchImpl: async () => response(data) });
  await updates.refresh('ws');
  data = {
    ...summary(),
    welcome: { state: 'unavailable' },
    review: { state: 'ready', pending_count: 2, batch_id: 'batch' }
  };
  await updates.refresh('ws');
  assert.equal(updates.read('ws').data.welcome.stale, true);
  assert.equal(updates.read('ws').data.review.pending_count, 2);
  data = { ...summary('ws', true), review: { state: 'unavailable' } };
  await updates.refresh('ws');
  assert.equal(updates.read('ws').data.welcome.unread_count, 0);
  assert.deepEqual(updates.read('ws').data.review, {
    state: 'unavailable',
    stale: true,
    pending_count: 2,
    batch_id: 'batch'
  });
});

test('a committed change during a read gets one trailing canonical read', async () => {
  const wait = deferred();
  let reads = 0;
  const updates = createWorkspaceUpdates({
    fetchImpl: () => {
      reads++;
      return reads === 1 ? wait.promise : Promise.resolve(response(summary('ws', true)));
    }
  });
  const pending = updates.refresh('ws');
  updates.invalidate('ws');
  updates.invalidate('ws');
  wait.resolve(response(summary()));
  await pending;
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(reads, 2);
  assert.equal(updates.read('ws').data.welcome.unread_count, 0);
});

test('local filing hints and active return only invalidate; cleanup removes listeners and polling', () => {
  const doc = new EventTarget();
  const win = new EventTarget();
  doc.visibilityState = 'visible';
  const hints = [];
  let poll;
  let cleared = false;
  const stop = watchWorkspaceUpdates('ws', {
    document: doc,
    window: win,
    updates: { invalidate: id => hints.push(id) },
    setTimer: (fn, delay) => {
      assert.equal(delay, 30000);
      poll = fn;
      return 7;
    },
    clearTimer: id => {
      assert.equal(id, 7);
      cleared = true;
    }
  });
  const send = (id, count) =>
    doc.dispatchEvent(
      new CustomEvent('ori:file-janitor-changed', {
        detail: {
          state: {
            workspaceId: id,
            actionState: {
              status: { state: 'ready' },
              batch: { state: 'ready' },
              pendingCount: count
            }
          }
        }
      })
    );
  send('other', 10);
  assert.equal(hints.length, 1);
  send('ws', 10); // preparation/manual scan
  send('ws', 10); // unchanged refresh never announces or invalidates again
  send('ws', 9); // partial apply/decision
  send('ws', 10); // undo
  assert.equal(hints.length, 4);
  doc.visibilityState = 'hidden';
  poll();
  win.dispatchEvent(new Event('focus'));
  assert.equal(hints.length, 4);
  doc.visibilityState = 'visible';
  doc.dispatchEvent(new Event('visibilitychange'));
  poll(); // missing/automatic scan hint recovers on a canonical active read
  assert.equal(hints.length, 6);
  stop();
  send('ws', 0);
  poll();
  win.dispatchEvent(new Event('pageshow'));
  assert.equal(hints.length, 6);
  assert.equal(cleared, true);
});

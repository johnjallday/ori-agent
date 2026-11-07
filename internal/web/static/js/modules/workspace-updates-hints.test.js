import test from 'node:test';
import assert from 'node:assert/strict';
import {
  createWorkspaceUpdates,
  watchWorkspaceUpdates,
  watchWorkspaceUpdatePage
} from './workspace-updates.js';

const settled = () => new Promise(resolve => setImmediate(resolve));
const response = data => ({ ok: true, status: 200, json: async () => data });
const summary = seen => ({
  workspace_id: 'ws',
  welcome: {
    state: 'ready',
    session_id: 'chat',
    unread_count: seen ? 0 : 1,
    messages: [{ message_id: 'hello', revision: 1, ...(seen ? { seen_at: 'saved' } : {}) }]
  },
  review: { state: 'ready', pending_count: 3 }
});

function broadcastBus() {
  const channels = new Set();
  const sent = [];
  class Channel extends EventTarget {
    constructor(name) {
      super();
      this.name = name;
      channels.add(this);
    }
    postMessage(data) {
      sent.push(structuredClone(data));
      for (const peer of channels)
        if (peer !== this && peer.name === this.name) {
          queueMicrotask(() => {
            if (channels.has(peer))
              peer.dispatchEvent(new MessageEvent('message', { data: structuredClone(data) }));
          });
        }
    }
    close() {
      channels.delete(this);
    }
  }
  const tab = () => {
    const window = new EventTarget();
    window.BroadcastChannel = Channel;
    const document = new EventTarget();
    document.visibilityState = 'visible';
    return { window, document, setTimer: () => 1, clearTimer: () => {} };
  };
  return { tab, sent, channels };
}

function filingHint(document, count = 3) {
  document.dispatchEvent(
    new CustomEvent('ori:file-janitor-changed', {
      detail: {
        state: {
          workspaceId: 'ws',
          actionState: {
            status: { state: 'ready', root_path: '/PRIVATE/folder', approval_token: 'SECRET' },
            batch: { state: 'ready' },
            pendingCount: count
          }
        }
      }
    })
  );
}

test('two tabs converge through canonical reads after exact seen commits; hints contain no counts or private content', async () => {
  const bus = broadcastBus();
  const left = bus.tab();
  const right = bus.tab();
  let seen = false;
  const calls = [];
  const client = tab =>
    createWorkspaceUpdates({
      fetchImpl: async (url, init) => {
        calls.push({ tab, url, method: init.method || 'GET' });
        if (init.method === 'POST') seen = true;
        return response(summary(seen));
      }
    });
  const a = client('left');
  const b = client('right');
  const stopA = watchWorkspaceUpdates('ws', { ...left, updates: a });
  const stopB = watchWorkspaceUpdates('ws', { ...right, updates: b });
  try {
    await settled();
    assert.equal(b.read('ws').data.welcome.unread_count, 1);
    assert.equal(
      await a.acknowledge('ws', { session_id: 'chat', message_id: 'hello', revision: 1 }),
      true
    );
    await settled();
    assert.equal(b.read('ws').data.welcome.unread_count, 0);
    assert.equal(b.read('ws').data.review.pending_count, 3);
    assert.equal(
      calls.filter(call => call.method === 'POST').length,
      1,
      'receiving never acknowledges'
    );
    assert.equal(bus.sent.length, 1, 'receiving does not echo');
    filingHint(left.document);
    await settled();
    assert.equal(bus.sent.length, 2);
    for (const hint of bus.sent)
      assert.deepEqual(Object.keys(hint).sort(), ['kind', 'source', 'workspace_id']);
    assert.doesNotMatch(
      JSON.stringify(bus.sent),
      /PRIVATE|SECRET|pending_count|unread_count|hello|chat/
    );
    const before = calls.filter(call => call.tab === 'right').length;
    right.document.visibilityState = 'hidden';
    filingHint(left.document, 2);
    await settled();
    assert.equal(calls.filter(call => call.tab === 'right').length, before);
    right.document.visibilityState = 'visible';
    right.document.dispatchEvent(new Event('visibilitychange'));
    await settled();
    assert.equal(calls.filter(call => call.tab === 'right').length, before + 1);
    assert.equal(
      calls.every(call => /^\/api\/workspaces\/ws\/updates(?:\/seen)?$/.test(call.url)),
      true
    );
  } finally {
    stopA();
    stopB();
  }
  assert.equal(bus.channels.size, 0);
});

test('page recovery is one selected window, not tree traversal; hidden/inactive views and disposed listeners do no reads', () => {
  const document = new EventTarget();
  const window = new EventTarget();
  document.visibilityState = 'visible';
  let active = true;
  let reads = 0;
  let poll;
  let cleared = false;
  const stop = watchWorkspaceUpdatePage({
    document,
    window,
    isActive: () => active,
    updates: {
      invalidatePage: () => {
        reads++;
      }
    },
    setTimer: (fn, delay) => {
      assert.equal(delay, 60000);
      poll = fn;
      return 1;
    },
    clearTimer: () => {
      cleared = true;
    }
  });
  assert.equal(reads, 1);
  filingHint(document);
  filingHint(document);
  assert.equal(reads, 2, 'unchanged local observations do not invalidate repeatedly');
  document.visibilityState = 'hidden';
  poll();
  window.dispatchEvent(new Event('focus'));
  assert.equal(reads, 2);
  document.visibilityState = 'visible';
  active = false;
  poll();
  assert.equal(reads, 2);
  active = true;
  window.dispatchEvent(new Event('popstate'));
  window.dispatchEvent(new Event('pageshow'));
  poll();
  assert.equal(reads, 5);
  stop();
  filingHint(document, 2);
  window.dispatchEvent(new Event('focus'));
  poll();
  assert.equal(reads, 5);
  assert.equal(cleared, true);
});

test('reset generations clear observations and reject old results; channels suspend and resume with page lifecycle', async () => {
  const bus = broadcastBus();
  const tab = bus.tab();
  let resolve;
  let delayed = false;
  const updates = createWorkspaceUpdates({
    fetchImpl: () =>
      delayed
        ? new Promise(done => {
            resolve = done;
          })
        : Promise.resolve(response(summary(false)))
  });
  const stop = watchWorkspaceUpdates('ws', { ...tab, updates });
  await settled();
  delayed = true;
  const old = updates.refresh('ws');
  tab.window.dispatchEvent(
    Object.assign(new Event('storage'), {
      key: 'ori.reset.generation',
      oldValue: 'before',
      newValue: 'after'
    })
  );
  assert.equal(updates.read('ws').data, null);
  resolve(response(summary(false)));
  assert.equal(
    (await old).data,
    null,
    'a discarded read cannot hand its old private view back to a caller'
  );
  assert.equal(updates.read('ws').data, null);
  tab.window.dispatchEvent(new Event('pagehide'));
  assert.equal(bus.channels.size, 0);
  delayed = false;
  tab.window.dispatchEvent(new Event('pageshow'));
  await settled();
  assert.equal(bus.channels.size, 1);
  assert.equal(
    updates.read('ws').data.welcome.unread_count,
    1,
    'return re-reads the authoritative source'
  );
  stop();
  assert.equal(bus.channels.size, 0);
});

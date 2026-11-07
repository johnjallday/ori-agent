import test from 'node:test';
import assert from 'node:assert/strict';
import { createLeadWelcome } from './personal-assistant-lead-welcome.js';

function fixture() {
  const f = {
    current: { workspaceId: 'ws', name: 'Mira', sessionId: '' },
    ready: true,
    busy: false,
    reads: [],
    changes: [],
    timers: [],
    summaries: []
  };
  f.answer = { session_id: 'welcome', changed: true, awaiting_assessment: false };
  f.view = { state: 'ready', data: { workspace_id: 'ws', welcome: { unread_count: 1 } } };
  f.updates = {
    read: () => f.view,
    refresh: async id => {
      f.summaries.push(id);
      return f.view;
    },
    subscribe: listener => {
      f.listener = listener;
      return () => {
        f.listener = null;
      };
    }
  };
  f.controller = createLeadWelcome({
    target: () => f.current,
    ready: () => f.ready,
    busy: () => f.busy,
    updates: f.updates,
    changed: view => f.changes.push(view),
    fetchImpl: async (url, init) => {
      f.reads.push({ url, init });
      return { ok: true, json: async () => f.answer };
    },
    setTimer: fn => {
      f.timers.push(fn);
      return f.timers.length;
    },
    clearTimer: () => {}
  });
  return f;
}

test('arrival saves through the legacy fallback but only publishes a quiet summary', async () => {
  const f = fixture();
  await f.controller.refresh();
  await f.controller.refresh();
  assert.deepEqual(f.reads, [
    { url: '/api/workspaces/ws/lead-conversation/open', init: { method: 'POST' } }
  ]);
  assert.equal(f.current.sessionId, '');
  assert.ok(f.changes.every(change => change === f.view));
  assert.equal(f.view.data.welcome.unread_count, 1); // no implicit seen
});

test('setup, drafts and New suppress only legacy delivery, not already saved discovery', async () => {
  for (const mode of ['setup', 'busy', 'new']) {
    const f = fixture();
    if (mode === 'setup') f.ready = false;
    if (mode === 'busy') f.busy = true;
    if (mode === 'new') f.current.fresh = true;
    await f.controller.refresh();
    assert.equal(f.reads.length, 0);
    assert.deepEqual(f.summaries, ['ws']);
    assert.equal(f.changes.at(-1), f.view);
    assert.equal(f.current.sessionId, '');
  }
});

test('another recipient neither delivers nor retains a workspace indicator', async () => {
  const f = fixture();
  await f.controller.refresh();
  f.current = null;
  await f.controller.refresh();
  assert.equal(f.changes.at(-1), null);
  assert.equal(f.reads.length, 1);
});

test('a delayed assessment remains a summary update, never a drawer/session instruction', async () => {
  const f = fixture();
  f.answer.awaiting_assessment = true;
  await f.controller.refresh();
  f.answer.awaiting_assessment = false;
  f.view = { ...f.view, data: { workspace_id: 'ws', welcome: { unread_count: 2 } } };
  await f.controller.refresh();
  assert.equal(f.changes.at(-1).data.welcome.unread_count, 2);
  assert.equal(f.current.sessionId, '');
  assert.equal(f.reads.length, 2);
});

test('late responses cannot publish into a different workspace', async () => {
  const f = fixture();
  let finish;
  f.updates.refresh = () =>
    new Promise(resolve => {
      finish = resolve;
    });
  const work = f.controller.refresh();
  f.current = { workspaceId: 'elsewhere', name: 'Mira', sessionId: 'user-session' };
  finish(f.view);
  await work;
  assert.equal(f.reads.length, 0);
  assert.equal(f.changes.length, 1); // initial snapshot only
  assert.equal(f.current.sessionId, 'user-session');
  f.controller.destroy();
});

test('New chosen during a summary read still prevents legacy delivery', async () => {
  const f = fixture();
  let finish;
  f.updates.refresh = () =>
    new Promise(resolve => {
      finish = resolve;
    });
  const work = f.controller.refresh();
  f.current.fresh = true;
  finish(f.view);
  await work;
  assert.equal(f.reads.length, 0);
  assert.equal(f.current.sessionId, '');
});

test('shared refreshes apply only to the current workspace; destruction removes listeners', async () => {
  const f = fixture();
  const foreign = { state: 'ready', data: { workspace_id: 'other' } };
  f.listener('other', foreign);
  assert.equal(f.changes.length, 0);
  f.listener('ws', f.view);
  assert.equal(f.changes.length, 1);
  f.controller.destroy();
  assert.equal(f.listener, null);
  await f.controller.refresh();
  assert.equal(f.reads.length, 0);
});

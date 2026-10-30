import test from 'node:test';
import assert from 'node:assert/strict';
import { createLeadWelcome } from './personal-assistant-lead-welcome.js';

function fixture() {
  const f = {
    current: { workspaceId: 'ws', name: 'Mira', sessionId: '' },
    ready: true,
    busy: false,
    reads: [],
    deliveries: [],
    timers: []
  };
  f.answer = { session_id: 'welcome', changed: true, awaiting_assessment: false };
  f.controller = createLeadWelcome({
    target: () => f.current,
    ready: () => f.ready,
    busy: () => f.busy,
    fetchImpl: async (url, init) => {
      f.reads.push({ url, init });
      return { ok: true, json: async () => f.answer };
    },
    deliver: async (id, options) => {
      f.deliveries.push({ id, ...options });
    },
    setTimer: fn => {
      f.timers.push(fn);
      return f.timers.length;
    },
    clearTimer: () => {}
  });
  return f;
}

test('arrival delivers once, through the existing conversation, without a chat/model call', async () => {
  const f = fixture();
  await f.controller.refresh();
  await f.controller.refresh();
  assert.deepEqual(f.deliveries, [{ id: 'welcome', reveal: true }]);
  assert.deepEqual(f.reads, [
    { url: '/api/workspaces/ws/lead-conversation/open', init: { method: 'POST' } }
  ]);
});

test('setup, drafts, other recipients and New never trigger the welcome', async () => {
  for (const mode of ['setup', 'busy', 'other', 'new']) {
    const f = fixture();
    if (mode === 'setup') f.ready = false;
    if (mode === 'busy') f.busy = true;
    if (mode === 'other') f.current = null;
    if (mode === 'new') f.current.fresh = true;
    await f.controller.refresh();
    assert.equal(f.reads.length, 0);
    assert.equal(f.deliveries.length, 0);
  }
});

test('another tab or reload with an already delivered receipt never reopens the drawer', async () => {
  const f = fixture();
  f.answer.changed = false;
  await f.controller.refresh();
  assert.equal(f.deliveries.length, 0);
});

test('closing the drawer does not reopen it for the eventual first assessment', async () => {
  const f = fixture();
  f.answer.awaiting_assessment = true;
  await f.controller.refresh();
  f.controller.quiet();
  f.answer = { session_id: 'welcome', changed: true, awaiting_assessment: false };
  await f.controller.refresh();
  assert.deepEqual(f.deliveries, [
    { id: 'welcome', reveal: true },
    { id: 'welcome', reveal: false }
  ]);
});

test('late responses cannot replace a different workspace or a user-started conversation', async () => {
  for (const mode of ['workspace', 'session', 'new']) {
    const f = fixture();
    let resolve;
    const wait = new Promise(r => {
      resolve = r;
    });
    const ctl = createLeadWelcome({
      target: () => f.current,
      ready: () => true,
      busy: () => false,
      fetchImpl: () => wait,
      deliver: () => {
        throw new Error('stale delivery');
      },
      setTimer: () => 1,
      clearTimer: () => {}
    });
    const work = ctl.refresh();
    if (mode === 'workspace') f.current = { ...f.current, workspaceId: 'elsewhere' };
    if (mode === 'session') f.current = { ...f.current, sessionId: 'user-session' };
    if (mode === 'new') f.current = { ...f.current, fresh: true };
    resolve({ ok: true, json: async () => f.answer });
    await work;
    ctl.destroy();
  }
});

test('finishing an old delivery does not mark another workspace as delivered', async () => {
  let current = { workspaceId: 'first', name: 'Mira', sessionId: '' };
  let ready = true,
    finish,
    started,
    reads = 0;
  const waiting = new Promise(resolve => {
    finish = resolve;
  });
  const delivering = new Promise(resolve => {
    started = resolve;
  });
  const controller = createLeadWelcome({
    target: () => current,
    ready: () => ready,
    busy: () => false,
    fetchImpl: async () => {
      reads++;
      return { ok: true, json: async () => ({ session_id: 'welcome', changed: true }) };
    },
    deliver: async () => {
      started();
      if (current.workspaceId === 'first') await waiting;
    },
    setTimer: () => 1,
    clearTimer: () => {}
  });
  const old = controller.refresh();
  await delivering;
  current = { ...current, workspaceId: 'second' };
  ready = false;
  await controller.refresh();
  finish();
  await old;
  ready = true;
  await controller.refresh();
  assert.equal(reads, 2);
  controller.destroy();
});

test('a response deferred by a draft never replaces a subsequently started conversation', async () => {
  for (const newSession of [false, true]) {
    let current = { workspaceId: 'ws', name: 'Mira', sessionId: '' };
    let busy = false,
      resolve;
    const delivered = [];
    const promise = new Promise(r => {
      resolve = r;
    });
    const ctl = createLeadWelcome({
      target: () => current,
      ready: () => true,
      busy: () => busy,
      fetchImpl: () => promise,
      deliver: async id => delivered.push(id),
      setTimer: () => 1,
      clearTimer: () => {}
    });
    const work = ctl.refresh();
    busy = true;
    resolve({
      ok: true,
      json: async () => ({ session_id: 'welcome', changed: true, awaiting_assessment: false })
    });
    await work;
    assert.equal(delivered.length, 0);
    busy = false;
    if (newSession) current = { ...current, sessionId: 'user-created' };
    await ctl.refresh();
    assert.deepEqual(delivered, newSession ? [] : ['welcome']);
    ctl.destroy();
  }
});

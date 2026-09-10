// Tests for economy-harvest.js — the shared client half of collecting Harvest.
//
//   node --test internal/web/static/js/modules/economy-harvest.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { bankHarvest, taskResultDeepLink, ECONOMY_CHANGED_EVENT } from './economy-harvest.js';

function withWindow(run) {
  const events = [];
  const toasts = [];
  const previousWindow = globalThis.window;
  const previousFetch = globalThis.fetch;
  globalThis.window = {
    Toast: { success: message => toasts.push(message) },
    dispatchEvent: event => events.push(event)
  };
  globalThis.CustomEvent = class CustomEvent {
    constructor(type, init) {
      this.type = type;
      this.detail = (init && init.detail) || null;
    }
  };
  return Promise.resolve(run({ events, toasts })).finally(() => {
    globalThis.window = previousWindow;
    globalThis.fetch = previousFetch;
  });
}

function respondWith(status, body) {
  const calls = [];
  globalThis.fetch = async (url, init) => {
    calls.push({ url, init });
    return {
      ok: status >= 200 && status < 300,
      status,
      json: async () => body
    };
  };
  return calls;
}

test('banking a pile toasts what arrived and what the user now has (FR45)', async () => {
  await withWindow(async ({ events, toasts }) => {
    const calls = respondWith(200, { banked: 3, craft: 120, harvest: 48 });

    const result = await bankHarvest({ workspaceId: 'ws1', taskId: 't1' });

    assert.equal(result.banked, 3);
    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, '/api/economy/harvest');
    assert.deepEqual(JSON.parse(calls[0].init.body), { workspace_id: 'ws1', task_id: 't1' });
    assert.deepEqual(toasts, ['Harvested 3 · Harvest 48']);
    assert.equal(events.length, 1);
    assert.equal(events[0].type, ECONOMY_CHANGED_EVENT);
  });
});

// Opening a result you have already read is the common case, not an error: it
// must be silent, with no toast and nothing for the HUD to react to.
test('banking nothing says nothing', async () => {
  await withWindow(async ({ events, toasts }) => {
    respondWith(200, { banked: 0, craft: 120, harvest: 45 });

    const result = await bankHarvest({ workspaceId: 'ws1', taskId: 't1' });

    assert.equal(result.banked, 0);
    assert.deepEqual(toasts, []);
    assert.deepEqual(events, []);
  });
});

// 404 is the feature flag being off. There is no economy on this install, so
// there is nothing to report and nothing went wrong.
test('a 404 is silent', async () => {
  await withWindow(async ({ events, toasts }) => {
    respondWith(404, {});

    assert.equal(await bankHarvest({ workspaceId: 'ws1', taskId: 't1' }), null);
    assert.deepEqual(toasts, []);
    assert.deepEqual(events, []);
  });
});

test('a failed bank never interrupts the reader', async () => {
  await withWindow(async ({ events, toasts }) => {
    globalThis.fetch = async () => {
      throw new Error('offline');
    };

    assert.equal(await bankHarvest({ workspaceId: 'ws1', taskId: 't1' }), null);
    assert.deepEqual(toasts, []);
    assert.deepEqual(events, []);
  });
});

test('banking without a task id makes no request at all', async () => {
  await withWindow(async () => {
    const calls = respondWith(200, { banked: 1 });

    assert.equal(await bankHarvest({ workspaceId: 'ws1', taskId: '  ' }), null);
    assert.equal(await bankHarvest({}), null);
    assert.equal(calls.length, 0);
  });
});

test('the result deep link is read only when it asks for a result', () => {
  assert.equal(taskResultDeepLink('?task=t1&result=1'), 't1');
  assert.equal(taskResultDeepLink('task=t1&result=true'), 't1');
  // A bare ?task= is an ordinary task link and must not open a result modal.
  assert.equal(taskResultDeepLink('?task=t1'), '');
  assert.equal(taskResultDeepLink('?result=1'), '');
  assert.equal(taskResultDeepLink('?panel=backlog'), '');
  assert.equal(taskResultDeepLink(''), '');
  assert.equal(taskResultDeepLink(null), '');
});

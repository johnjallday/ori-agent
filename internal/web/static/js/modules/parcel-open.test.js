// Tests for parcel-open.js — seeing a result elsewhere opens its parcel.
//
//   node --test internal/web/static/js/modules/parcel-open.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { openParcelByRef } from './parcel-open.js';

function respondWith(status, body, { throws = false } = {}) {
  const calls = [];
  const warnings = [];
  const previousFetch = globalThis.fetch;
  const previousWarn = console.warn;
  globalThis.fetch = async (url, init) => {
    calls.push({ url, init });
    if (throws) throw new Error('offline');
    return { ok: status >= 200 && status < 300, status, json: async () => body };
  };
  console.warn = (...args) => warnings.push(args);
  return {
    calls,
    warnings,
    restore() {
      globalThis.fetch = previousFetch;
      console.warn = previousWarn;
    }
  };
}

test('it asks the server to open the parcels for one reference', async () => {
  const net = respondWith(200, { opened: 2 });
  try {
    const opened = await openParcelByRef({ kind: 'task', workspaceId: 'ws-1', refId: 't1' });
    assert.equal(opened, 2);
    assert.equal(net.calls.length, 1);
    assert.equal(net.calls[0].url, '/api/workspace-map/parcels/open-by-ref');
    assert.equal(net.calls[0].init.method, 'POST');
    assert.deepEqual(JSON.parse(net.calls[0].init.body), {
      kind: 'task',
      workspace_id: 'ws-1',
      ref_id: 't1'
    });
  } finally {
    net.restore();
  }
});

test('the brief and the janitor console open everything of their kind', async () => {
  const net = respondWith(200, { opened: 3 });
  try {
    assert.equal(await openParcelByRef({ kind: 'file_janitor', workspaceId: 'ws-1' }), 3);
    assert.deepEqual(JSON.parse(net.calls[0].init.body), {
      kind: 'file_janitor',
      workspace_id: 'ws-1'
    });
  } finally {
    net.restore();
  }
});

test('an incomplete reference makes no request', async () => {
  const net = respondWith(200, { opened: 1 });
  try {
    assert.equal(await openParcelByRef({ kind: 'task', workspaceId: 'ws-1' }), null);
    assert.equal(await openParcelByRef(), null);
    assert.equal(net.calls.length, 0);
  } finally {
    net.restore();
  }
});

test('404 means the feature is off: no warning, no result', async () => {
  const net = respondWith(404, {});
  try {
    assert.equal(await openParcelByRef({ kind: 'task', workspaceId: 'ws-1', refId: 't1' }), null);
    assert.equal(net.warnings.length, 0);
  } finally {
    net.restore();
  }
});

test('any other failure is only logged', async () => {
  for (const setup of [() => respondWith(500, {}), () => respondWith(200, {}, { throws: true })]) {
    const net = setup();
    try {
      assert.equal(await openParcelByRef({ kind: 'task', workspaceId: 'ws-1', refId: 't1' }), null);
      assert.equal(net.warnings.length, 1);
    } finally {
      net.restore();
    }
  }
});

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ParentSurfaceBridge } from './workspace-surface-bridge.js';
import { OriWorkspaceSurfaceSDK } from '../plugin/workspace-surface-sdk.js';

class EventTargetFake {
  constructor() {
    this.listeners = new Map();
  }

  addEventListener(type, listener) {
    const listeners = this.listeners.get(type) || new Set();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  removeEventListener(type, listener) {
    this.listeners.get(type)?.delete(listener);
  }

  emit(type, event) {
    for (const listener of this.listeners.get(type) || []) listener(event);
  }
}

function deterministicCrypto() {
  let next = 1;
  return {
    getRandomValues(bytes) {
      for (let index = 0; index < bytes.length; index += 1) {
        bytes[index] = next % 255;
        next += 1;
      }
      return bytes;
    }
  };
}

function envelope(bridgeId, type, requestId, payload) {
  return {
    protocol_version: 1,
    bridge_id: bridgeId,
    type,
    request_id: requestId,
    payload
  };
}

test('parent handshake binds the exact frame window and forwards a declared request', async () => {
  const events = new EventTargetFake();
  const posted = [];
  const frame = { postMessage: (message, target) => posted.push({ message, target }) };
  const calls = [];
  const bridge = new ParentSurfaceBridge({
    frameWindow: frame,
    eventTarget: events,
    crypto: deterministicCrypto(),
    surface: { key: 'plugin:demo:tools:main', label: 'Demo' },
    features: { operation: true, close: true },
    onRequest: async request => {
      calls.push(request);
      return { ok: true, result: { message: 'Hello, Ori.' } };
    }
  });

  bridge.start();
  assert.equal(posted.length, 1);
  assert.equal(posted[0].message.type, 'ori.surface.challenge');
  assert.equal(posted[0].target, '*', 'opaque frame origins require targetOrigin="*"');
  const challenge = posted[0].message.payload.challenge;

  await bridge.receive({
    source: { postMessage() {} },
    data: envelope(bridge.bridgeId, 'ori.surface.ready', 'handshake', {
      challenge,
      sdk_version: '1.0.0',
      supported_protocols: [1]
    })
  });
  assert.equal(bridge.ready, false, 'a lookalike frame cannot finish the handshake');

  await bridge.receive({
    source: frame,
    data: envelope(bridge.bridgeId, 'ori.surface.ready', 'handshake', {
      challenge,
      sdk_version: '1.0.0',
      supported_protocols: [1]
    })
  });
  assert.equal(bridge.ready, true);
  assert.equal(posted.at(-1).message.type, 'ori.surface.init');
  assert.deepEqual(posted.at(-1).message.payload.features, { operation: true, close: true });

  await bridge.receive({
    source: frame,
    data: envelope(bridge.bridgeId, 'ori.surface.operation.invoke', 'request-1', {
      operation_id: 'greeting.create',
      input: { name: 'Ori' }
    })
  });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].payload.operation_id, 'greeting.create');
  assert.equal(posted.at(-1).message.type, 'ori.surface.response');
  assert.deepEqual(posted.at(-1).message.payload.result, { message: 'Hello, Ori.' });
});

test('parent bridge ignores source mismatches, unknown messages, and oversized payloads', async () => {
  const events = new EventTargetFake();
  const posted = [];
  const frame = { postMessage: message => posted.push(message) };
  let calls = 0;
  const bridge = new ParentSurfaceBridge({
    frameWindow: frame,
    eventTarget: events,
    bridgeId: 'bridge-1',
    challenge: 'challenge-1',
    surface: { key: 'plugin:demo:tools:main' },
    onRequest: async () => {
      calls += 1;
      return { ok: true, result: {} };
    }
  });
  bridge.start();
  await bridge.receive({
    source: frame,
    data: envelope('bridge-1', 'ori.surface.ready', 'handshake', {
      challenge: 'challenge-1',
      sdk_version: '1.0.0',
      supported_protocols: [1]
    })
  });
  const afterHandshake = posted.length;

  await bridge.receive({
    source: {},
    data: envelope('bridge-1', 'ori.surface.operation.invoke', 'wrong-source', {
      operation_id: 'greeting.create',
      input: {}
    })
  });
  await bridge.receive({
    source: frame,
    data: envelope('bridge-1', 'ori.surface.parent_dom.read', 'unknown', {})
  });
  await bridge.receive({
    source: frame,
    data: envelope('bridge-1', 'ori.surface.operation.invoke', 'oversized', {
      operation_id: 'greeting.create',
      input: { value: 'x'.repeat(70 * 1024) }
    })
  });
  assert.equal(calls, 0);
  assert.equal(posted.length, afterHandshake, 'rejected messages receive no reflective response');
});

test('plain JavaScript SDK completes handshake and correlates operation responses', async () => {
  const frameEvents = new EventTargetFake();
  const toParent = [];
  const parent = { postMessage: (message, target) => toParent.push({ message, target }) };
  const frameWindow = Object.assign(frameEvents, { parent });
  const sdk = new OriWorkspaceSurfaceSDK({
    eventTarget: frameWindow,
    parentWindow: parent,
    crypto: deterministicCrypto()
  }).start();

  frameEvents.emit('message', {
    source: parent,
    data: envelope('bridge-sdk', 'ori.surface.challenge', 'handshake', {
      challenge: 'challenge-sdk',
      surface: { plugin_id: 'demo', capability_id: 'tools', surface_id: 'main' }
    })
  });
  assert.equal(toParent.at(-1).message.type, 'ori.surface.ready');
  assert.equal(toParent.at(-1).message.payload.challenge, 'challenge-sdk');

  frameEvents.emit('message', {
    source: parent,
    data: envelope('bridge-sdk', 'ori.surface.init', 'handshake', {
      surface: { key: 'plugin:demo:tools:main', label: 'Demo' },
      features: { operation: true, close: true }
    })
  });
  assert.equal(sdk.ready, true);
  assert.equal(sdk.surface.label, 'Demo');

  const pending = sdk.invoke('greeting.create', { name: 'Ori' });
  const request = toParent.at(-1).message;
  assert.equal(request.type, 'ori.surface.operation.invoke');
  assert.deepEqual(request.payload, {
    operation_id: 'greeting.create',
    input: { name: 'Ori' }
  });
  frameEvents.emit('message', {
    source: parent,
    data: envelope('bridge-sdk', 'ori.surface.response', request.request_id, {
      ok: true,
      result: { message: 'Hello, Ori.' }
    })
  });
  assert.deepEqual(await pending, { message: 'Hello, Ori.' });
  sdk.destroy();
});

test('SDK never accepts a lookalike parent and rejects calls before init', async () => {
  const events = new EventTargetFake();
  const parent = { postMessage() {} };
  const frame = Object.assign(events, { parent });
  const sdk = new OriWorkspaceSurfaceSDK({
    eventTarget: frame,
    parentWindow: parent,
    crypto: deterministicCrypto()
  }).start();

  await assert.rejects(
    sdk.invoke('greeting.create', {}),
    error => error.code === 'bridge_not_ready'
  );
  events.emit('message', {
    source: { postMessage() {} },
    data: envelope('attacker', 'ori.surface.challenge', 'handshake', {
      challenge: 'stolen',
      surface: {}
    })
  });
  assert.equal(sdk.bridgeId, '');
  sdk.destroy();
});

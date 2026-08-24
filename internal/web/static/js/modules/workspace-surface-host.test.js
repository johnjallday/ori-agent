import { test } from 'node:test';
import assert from 'node:assert/strict';
import { OverlayCoordinator } from './workspace-overlay-coordinator.js';
import { WorkspaceSurfaceHost } from './workspace-surface-host.js';

class NodeFake {
  constructor(tag) {
    this.tagName = tag.toUpperCase();
    this.children = [];
    this.attributes = new Map();
    this.listeners = new Map();
    this.parentNode = null;
    this.className = '';
    this.textContent = '';
    this.styleValues = new Map();
    this.style = { setProperty: (name, value) => this.styleValues.set(name, value) };
    this.contentWindow = tag === 'iframe' ? { postMessage() {} } : null;
    this.focused = false;
  }

  appendChild(child) {
    child.parentNode = this;
    this.children.push(child);
    return child;
  }

  setAttribute(name, value) {
    this.attributes.set(name, String(value));
  }

  getAttribute(name) {
    return this.attributes.get(name) ?? null;
  }

  addEventListener(type, listener) {
    const listeners = this.listeners.get(type) || [];
    listeners.push(listener);
    this.listeners.set(type, listeners);
  }

  emit(type, event = {}) {
    for (const listener of this.listeners.get(type) || []) listener(event);
  }

  focus() {
    this.focused = true;
  }

  remove() {
    if (!this.parentNode) return;
    this.parentNode.children = this.parentNode.children.filter(child => child !== this);
    this.parentNode = null;
  }
}

class DocumentFake {
  constructor() {
    this.body = new NodeFake('body');
  }

  createElement(tag) {
    return new NodeFake(tag);
  }

  dispatchEvent() {}
}

class BridgeFake {
  constructor(options) {
    this.options = options;
    this.started = false;
    this.destroyed = false;
    this.visibilityValues = [];
  }

  start() {
    this.started = true;
  }

  destroy() {
    this.destroyed = true;
  }

  visibility(value) {
    this.visibilityValues.push(value);
  }
}

function response(status, payload = null) {
  return {
    ok: status >= 200 && status < 300,
    status,
    async json() {
      return payload;
    }
  };
}

function makeHost() {
  const document = new DocumentFake();
  const calls = [];
  const fetch = async (path, options = {}) => {
    calls.push({ path, options });
    if (path.endsWith('/surfaces')) {
      return response(200, {
        surfaces: [
          {
            key: 'plugin:workspace-surface-demo:demo-tools:main',
            plugin: { id: 'workspace-surface-demo', version: '0.1.0', generation: '7' },
            capability_id: 'demo-tools',
            surface_id: 'main',
            label: 'Surface Demo',
            description: 'Open the harmless demo surface.',
            icon: { kind: 'host', value: 'puzzle' },
            placement: 'map_modal',
            modal: { width: 720, height: 560 },
            status: {
              state: 'ready',
              value: 'Available',
              description: 'The demo service is ready.'
            },
            available: true,
            polling: { map_seconds: 5, open_seconds: 1 }
          }
        ]
      });
    }
    if (path.includes('/sessions') && options.method === 'POST') {
      return response(201, {
        session: 'parent-only-session',
        frame_url: '/api/workspace-surfaces/frames/frame-token/ui/index.html'
      });
    }
    if (path === '/api/workspace-surfaces/operations') {
      return response(200, { output: { message: 'Hello, Ori.' } });
    }
    if (path === '/api/workspace-surfaces/sessions' && options.method === 'DELETE') {
      return response(204);
    }
    return response(404, { code: 'not_found', message: 'Not found.' });
  };
  const restored = [];
  const coordinator = new OverlayCoordinator({
    setInert() {},
    releaseInert() {},
    trapFocus() {},
    restoreFocus(trigger) {
      restored.push(trigger);
      trigger?.focus?.();
    }
  });
  const timers = [];
  const window = {
    setTimeout(callback) {
      timers.push(callback);
    }
  };
  const host = new WorkspaceSurfaceHost({
    workspaceId: 'workspace-1',
    fetch,
    document,
    window,
    coordinator,
    Bridge: BridgeFake
  });
  return { host, document, calls, coordinator, restored, timers };
}

function find(node, tag) {
  if (node.tagName === tag.toUpperCase()) return node;
  for (const child of node.children) {
    const found = find(child, tag);
    if (found) return found;
  }
  return null;
}

test('generic catalog becomes one Map station without plugin-name branching', async () => {
  const { host } = makeHost();
  await host.loadCatalog();
  const stations = host.stations();
  assert.equal(stations.length, 1);
  assert.equal(stations[0].key, 'plugin:workspace-surface-demo:demo-tools:main');
  assert.equal(stations[0].label, 'Surface Demo');
  assert.deepEqual(stations[0].state(), {
    applies: true,
    value: 'Available',
    description: 'The demo service is ready.',
    tone: 'clear'
  });
});

test('host opens an opaque sandbox through the overlay coordinator and invokes operation', async () => {
  const { host, document, calls, coordinator } = makeHost();
  await host.loadCatalog();
  const trigger = new NodeFake('button');

  assert.equal(await host.open('plugin:workspace-surface-demo:demo-tools:main', trigger), true);
  assert.equal(
    coordinator.activeModal()?.id,
    'workspace-surface:plugin:workspace-surface-demo:demo-tools:main'
  );
  assert.equal(document.body.children.length, 1);
  const frame = find(document.body, 'iframe');
  assert.ok(frame);
  assert.equal(frame.getAttribute('sandbox'), 'allow-scripts');
  assert.equal(frame.getAttribute('sandbox').includes('allow-same-origin'), false);
  assert.equal(frame.getAttribute('credentialless'), '');
  assert.equal(frame.src, '/api/workspace-surfaces/frames/frame-token/ui/index.html');
  frame.emit('load');
  assert.equal(host.active.bridge.started, true);

  const result = await host.active.bridge.options.onRequest({
    type: 'ori.surface.operation.invoke',
    payload: { operation_id: 'greeting.create', input: { name: 'Ori' } }
  });
  assert.deepEqual(result, { ok: true, result: { message: 'Hello, Ori.' } });
  const operation = calls.find(call => call.path === '/api/workspace-surfaces/operations');
  assert.deepEqual(JSON.parse(operation.options.body), {
    session: 'parent-only-session',
    operation_id: 'greeting.create',
    input: { name: 'Ori' }
  });
});

test('host-owned close tears down frame, invalidates session, and restores station focus', async () => {
  const { host, document, calls, coordinator, restored } = makeHost();
  await host.loadCatalog();
  const trigger = new NodeFake('button');
  await host.open('plugin:workspace-surface-demo:demo-tools:main', trigger);
  const bridge = host.active.bridge;

  assert.equal(await host.close(), true);
  assert.equal(host.active, null);
  assert.equal(document.body.children.length, 0);
  assert.equal(bridge.destroyed, true);
  assert.equal(coordinator.activeModal(), null);
  assert.deepEqual(restored, [trigger]);
  assert.equal(trigger.focused, true);
  const close = calls.find(
    call => call.path === '/api/workspace-surfaces/sessions' && call.options.method === 'DELETE'
  );
  assert.deepEqual(JSON.parse(close.options.body), { session: 'parent-only-session' });
});

test('frame close intent receives a response before host teardown is scheduled', async () => {
  const { host, timers } = makeHost();
  await host.loadCatalog();
  await host.open('plugin:workspace-surface-demo:demo-tools:main', new NodeFake('button'));
  const result = await host.active.bridge.options.onRequest({
    type: 'ori.surface.host.close',
    payload: {}
  });
  assert.deepEqual(result, { ok: true, result: { closed: true } });
  assert.notEqual(host.active, null);
  assert.equal(timers.length, 1);
  timers[0]();
  await Promise.resolve();
  assert.equal(host.active, null);
});

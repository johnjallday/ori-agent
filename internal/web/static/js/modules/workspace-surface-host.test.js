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
    this.dispatchedEvents = [];
  }

  createElement(tag) {
    return new NodeFake(tag);
  }

  dispatchEvent(event) {
    this.dispatchedEvents.push(event);
  }
}

class CustomEventFake {
  constructor(type, options = {}) {
    this.type = type;
    this.detail = options.detail;
  }
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

  invalidate() {
    this.destroy();
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
  const asks = [];
  const setupCalls = [];
  const createdTasks = [];
  const executedTasks = [];
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
            polling: { map_seconds: 5, open_seconds: 1 },
            features: {
              confirmation: true,
              state: true,
              ask_ori: true,
              create_task: true,
              open_setup: true,
              close: true
            }
          },
          {
            key: 'plugin:workspace-surface-demo:demo-tools:project',
            plugin: { id: 'workspace-surface-demo', version: '0.1.0', generation: '7' },
            capability_id: 'demo-tools',
            surface_id: 'project',
            label: 'Project cleanup',
            description: 'Review cosmetic cleanup.',
            icon: { kind: 'host', value: 'folder' },
            placement: 'project_entry',
            modal: { width: 720, height: 560 },
            status: {
              state: 'ready',
              value: '1',
              description: 'One proposal is ready.'
            },
            available: true,
            polling: { map_seconds: 5, open_seconds: 1 },
            features: {
              confirmation: false,
              state: false,
              ask_ori: false,
              create_task: true,
              open_setup: true,
              close: true
            }
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
      const request = JSON.parse(options.body);
      if (request.operation_id === 'setting.validate' && !request.confirmation_token) {
        return response(409, {
          code: 'confirmation_required',
          message: 'Review this action.',
          confirmation_id: 'confirmation-1'
        });
      }
      if (request.operation_id === 'setting.validate') {
        return response(200, { output: { accepted: true } });
      }
      return response(200, { output: { message: 'Hello, Ori.' } });
    }
    if (path === '/api/workspace-surfaces/confirmations' && options.method === 'POST') {
      return response(200, { confirmation_token: 'host-only-token' });
    }
    if (path === '/api/workspace-surfaces/confirmations' && options.method === 'DELETE') {
      return response(204);
    }
    if (path === '/api/workspace-surfaces/state') {
      return response(200, { found: false, revision: '0' });
    }
    if (path === '/api/workspace-surfaces/intents') {
      const request = JSON.parse(options.body);
      if (request.type === 'ask_ori') {
        return response(200, {
          intent: 'ask_ori',
          workspace_id: 'workspace-1',
          plugin_context: request.context,
          required_capabilities: ['demo_runtime']
        });
      }
      if (request.type === 'create_task') {
        return response(200, {
          intent: 'create_task',
          workspace_id: 'workspace-1',
          task: {
            template_id: 'survey',
            title: 'Run fixed survey',
            details: 'Run the canonical survey workflow.',
            required_capabilities: ['demo_runtime'],
            auto_start: true,
            assignee_strategy: 'workspace_entry_agent'
          }
        });
      }
      return response(200, {
        intent: 'open_setup',
        workspace_id: 'workspace-1',
        provider_id: 'demo-runtime'
      });
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
    CustomEvent: CustomEventFake,
    confirm: () => true,
    OriAskRouting: {
      async submit(prompt, options) {
        asks.push({ prompt, options });
      }
    },
    SetupWizard: {
      open() {
        setupCalls.push(true);
      }
    },
    workspaceDetail: {
      workspace: { entry_agent_name: 'Commander' },
      async createTask(title, details, column, options) {
        createdTasks.push({ title, details, column, options });
        return { id: 'task-created' };
      },
      async executeTask(id, options) {
        executedTasks.push({ id, options });
      }
    },
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
    Bridge: BridgeFake,
    schedule: () => null,
    cancelSchedule: () => {}
  });
  return {
    host,
    document,
    calls,
    coordinator,
    restored,
    timers,
    asks,
    setupCalls,
    createdTasks,
    executedTasks
  };
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

test('project-entry actions stay out of Map stations and run through a headless host session', async () => {
  const { host, calls, createdTasks, executedTasks, setupCalls } = makeHost();
  await host.loadCatalog();
  assert.equal(host.stations().length, 1);
  const actions = host.projectEntryActions();
  assert.equal(actions.length, 1);
  assert.equal(actions[0].label, 'Project cleanup');
  assert.equal(actions[0].enabled, true);
  assert.equal(actions[0].badge, '1');

  assert.equal(
    await host.runProjectEntryTask('plugin:workspace-surface-demo:demo-tools:project'),
    true
  );
  assert.equal(createdTasks.length, 1);
  assert.deepEqual(createdTasks[0].options.requiredCapabilities, ['demo_runtime']);
  assert.equal(executedTasks.length, 1);
  assert.equal(
    calls.filter(
      call => call.path === '/api/workspace-surfaces/sessions' && call.options.method === 'DELETE'
    ).length,
    1
  );

  const project = host.surfaces.find(surface => surface.placement === 'project_entry');
  project.status = {
    state: 'disabled',
    value: 'Live control required',
    description: 'Set up live control.'
  };
  assert.equal(host.projectEntryActions()[0].enabled, false);
  assert.equal(await host.runProjectEntryTask(project.key), false);
  assert.equal(setupCalls.length, 1);
});

test('catalog polling announces only render-relevant station changes', async () => {
  const { host, document } = makeHost();
  await host.loadCatalog();
  assert.deepEqual(
    document.dispatchedEvents.map(event => event.type),
    ['ori:workspace-surfaces-changed']
  );

  const firstCatalog = host.surfaces.map(surface => ({ ...surface }));
  host.fetch = async () =>
    response(200, {
      surfaces: firstCatalog.map((surface, index) =>
        index === 0
          ? {
              ...surface,
              status: { ...surface.status, checked_at: '2026-08-27T14:44:48Z' }
            }
          : surface
      )
    });
  await host.loadCatalog();
  assert.equal(host.surfaces[0].status.checked_at, '2026-08-27T14:44:48Z');
  assert.equal(
    document.dispatchedEvents.length,
    1,
    'checked_at-only polling must not rebuild the Map'
  );

  host.fetch = async () =>
    response(200, {
      surfaces: firstCatalog.map((surface, index) =>
        index === 0
          ? {
              ...surface,
              status: { ...surface.status, value: 'Updated status' }
            }
          : surface
      )
    });
  await host.loadCatalog();
  assert.equal(document.dispatchedEvents.length, 2);
  assert.equal(document.dispatchedEvents[1].type, 'ori:workspace-surfaces-changed');
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

test('host keeps confirmation token out of frame while retrying the exact operation', async () => {
  const { host, calls } = makeHost();
  await host.loadCatalog();
  await host.open('plugin:workspace-surface-demo:demo-tools:main', new NodeFake('button'));
  const result = await host.active.bridge.options.onRequest({
    type: 'ori.surface.operation.invoke',
    payload: { operation_id: 'setting.validate', input: { enabled: true } }
  });
  assert.deepEqual(result, { ok: true, result: { accepted: true } });
  const confirmation = calls.find(
    call => call.path === '/api/workspace-surfaces/confirmations' && call.options.method === 'POST'
  );
  assert.ok(confirmation);
  const operationCalls = calls.filter(call => call.path === '/api/workspace-surfaces/operations');
  assert.equal(operationCalls.length, 2);
  assert.equal(JSON.parse(operationCalls[0].options.body).confirmation_token, undefined);
  assert.equal(JSON.parse(operationCalls[1].options.body).confirmation_token, 'host-only-token');
  assert.equal(
    JSON.stringify(result).includes('host-only-token'),
    false,
    'frame response never receives the confirmation token'
  );
});

test('state, Ask Ori, and Setup requests use host-owned generic intents', async () => {
  const { host, calls, asks, setupCalls, timers } = makeHost();
  await host.loadCatalog();
  await host.open('plugin:workspace-surface-demo:demo-tools:main', new NodeFake('button'));

  const state = await host.active.bridge.options.onRequest({
    type: 'ori.surface.state.get',
    payload: { key: 'display' }
  });
  assert.deepEqual(state, { ok: true, result: { found: false, revision: '0' } });
  const stateCall = calls.find(call => call.path === '/api/workspace-surfaces/state');
  assert.deepEqual(JSON.parse(stateCall.options.body), {
    session: 'parent-only-session',
    action: 'get',
    key: 'display'
  });

  const asked = await host.active.bridge.options.onRequest({
    type: 'ori.surface.host.ask_ori',
    payload: { context: 'Explain this status.' }
  });
  assert.deepEqual(asked, { ok: true, result: { submitted: true } });
  assert.equal(asks.length, 1);
  assert.deepEqual(asks[0].options.routeContext.required_capabilities, ['demo_runtime']);
  assert.equal(asks[0].options.routeContext.plugin_context, 'Explain this status.');
  assert.equal(asks[0].options.routeContext.plugin_context_untrusted, true);

  const setup = await host.active.bridge.options.onRequest({
    type: 'ori.surface.host.open_setup',
    payload: {}
  });
  assert.deepEqual(setup, { ok: true, result: { opened: true } });
  assert.equal(timers.length, 1);
  timers[0]();
  await Promise.resolve();
  assert.equal(setupCalls.length, 1);
});

test('direct task request uses host-resolved fixed task without Ask Ori routing', async () => {
  const { host, calls, asks, createdTasks, executedTasks } = makeHost();
  await host.loadCatalog();
  await host.open('plugin:workspace-surface-demo:demo-tools:main', new NodeFake('button'));

  const result = await host.active.bridge.options.onRequest({
    type: 'ori.surface.host.create_task',
    payload: { template_id: 'survey', variables: { proposal_id: 'proposal-1' } }
  });
  assert.deepEqual(result, {
    ok: true,
    result: { task_id: 'task-created', started: true }
  });
  assert.equal(asks.length, 0);
  assert.deepEqual(createdTasks, [
    {
      title: 'Run fixed survey',
      details: 'Run the canonical survey workflow.',
      column: '',
      options: {
        assignee: 'Commander',
        requiredCapabilities: ['demo_runtime'],
        successToast: false
      }
    }
  ]);
  assert.deepEqual(executedTasks, [
    { id: 'task-created', options: { skipConfirm: true, skipModal: true } }
  ]);
  const intent = calls.find(
    call =>
      call.path === '/api/workspace-surfaces/intents' &&
      JSON.parse(call.options.body).type === 'create_task'
  );
  assert.deepEqual(JSON.parse(intent.options.body), {
    session: 'parent-only-session',
    type: 'create_task',
    template_id: 'survey',
    variables: { proposal_id: 'proposal-1' }
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

test('polling is clamped, visibility-aware, and generation changes invalidate the frame', async () => {
  const { host } = makeHost();
  await host.loadCatalog();
  let scheduled = null;
  const canceled = [];
  host.schedule = (callback, delay) => {
    scheduled = { callback, delay, id: 9 };
    return 9;
  };
  host.cancelSchedule = id => canceled.push(id);
  host.setMapVisible(true);
  assert.equal(scheduled.delay, 5000);
  host.setDocumentVisible(false);
  assert.deepEqual(canceled, [9]);
  assert.equal(host.pollTimer, null);

  host.setDocumentVisible(true);
  await host.open('plugin:workspace-surface-demo:demo-tools:main', new NodeFake('button'));
  const bridge = host.active.bridge;
  assert.equal(scheduled.delay, 1000);
  host.surfaces = host.surfaces.map(surface => ({
    ...surface,
    plugin: { ...surface.plugin, generation: '8' }
  }));
  host._reconcileActiveSurface();
  await Promise.resolve();
  assert.equal(bridge.destroyed, true);
  assert.equal(host.active, null);
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

const DASHBOARD_KEY = 'user:ori.dashboard:dashboard:main';

function dashboardSurface(overrides = {}) {
  return {
    key: DASHBOARD_KEY,
    plugin: { id: 'ori.dashboard', version: '1', generation: '1' },
    capability_id: 'dashboard',
    surface_id: 'main',
    label: 'Dashboard',
    description: 'Your own dashboard for this workspace.',
    icon: { kind: 'host', value: 'grid' },
    placement: 'workspace_view',
    modal: { width: 1200, height: 800 },
    status: { state: 'ready', value: 'Ready', description: 'Ready.' },
    available: true,
    polling: { map_seconds: 60, open_seconds: 60 },
    features: {
      confirmation: false,
      state: false,
      ask_ori: false,
      create_task: false,
      open_setup: false,
      close: false
    },
    ...overrides
  };
}

async function mountedDashboard(overrides = {}) {
  const context = makeHost();
  await context.host.loadCatalog();
  context.host.surfaces = [...context.host.surfaces, dashboardSurface(overrides)];
  const container = new NodeFake('div');
  const ok = await context.host.mountInline(DASHBOARD_KEY, container);
  return { ...context, container, ok };
}

// The inline path must be exactly as sandboxed as the modal path. A divergence
// here would silently make a user-authored dashboard less isolated than a
// plugin surface.
test('inline mount carries the same isolation attributes as the modal path', async () => {
  const { host, container, ok } = await mountedDashboard();
  assert.equal(ok, true);

  const inlineFrame = find(container, 'iframe');
  assert.ok(inlineFrame, 'no iframe was mounted into the container');
  assert.equal(inlineFrame.getAttribute('sandbox'), 'allow-scripts');
  assert.equal(inlineFrame.getAttribute('credentialless'), '');
  assert.equal(inlineFrame.getAttribute('referrerpolicy'), 'no-referrer');

  await host.open('plugin:workspace-surface-demo:demo-tools:main', new NodeFake('button'));
  const modalFrame = find(host.active.modal, 'iframe');
  for (const attribute of ['sandbox', 'credentialless', 'referrerpolicy']) {
    assert.equal(
      inlineFrame.getAttribute(attribute),
      modalFrame.getAttribute(attribute),
      `inline and modal frames disagree on ${attribute}`
    );
  }
});

// FR24/FR25: the command view re-renders constantly. Re-mounting must not
// reload the frame or drop its bridge, or the dashboard would reset under the
// user on every background refresh.
test('re-mounting the same inline surface does not reload the frame', async () => {
  const { host, container, calls } = await mountedDashboard();
  const firstFrame = find(container, 'iframe');
  const firstBridge = host.inline.bridge;
  const sessionCalls = () =>
    calls.filter(call => call.path.includes('/sessions') && call.options.method === 'POST').length;
  const openedOnce = sessionCalls();

  assert.equal(await host.mountInline(DASHBOARD_KEY, container), true);
  assert.equal(sessionCalls(), openedOnce, 'a second session was opened for the same mount');
  assert.equal(find(container, 'iframe'), firstFrame, 'the frame was replaced');
  assert.equal(host.inline.bridge, firstBridge, 'the bridge was replaced');
  assert.equal(firstBridge.destroyed, false);
});

// Regression: the command view calls syncDashboardView on every render, and a
// second render can arrive while the first mount is still awaiting its session.
// Both calls used to see an empty slot and append a frame, stacking two live
// dashboards in the container with only one of them tracked.
test('concurrent inline mounts produce exactly one frame', async () => {
  const context = makeHost();
  await context.host.loadCatalog();
  context.host.surfaces = [...context.host.surfaces, dashboardSurface()];
  const container = new NodeFake('div');

  const results = await Promise.all([
    context.host.mountInline(DASHBOARD_KEY, container),
    context.host.mountInline(DASHBOARD_KEY, container),
    context.host.mountInline(DASHBOARD_KEY, container)
  ]);

  assert.deepEqual(results, [true, true, true]);
  const frames = container.children.filter(child => child.tagName === 'IFRAME');
  assert.equal(frames.length, 1, `expected one frame, found ${frames.length}`);
  assert.equal(context.host.inline.frame, frames[0]);
  const sessionOpens = context.calls.filter(
    call => call.path.includes('/sessions') && call.options.method === 'POST'
  ).length;
  assert.equal(sessionOpens, 1, 'more than one session was opened');
});

test('unmounting an inline surface tears down the frame, bridge, and session', async () => {
  const { host, container, calls } = await mountedDashboard();
  const bridge = host.inline.bridge;

  assert.equal(await host.unmountInline(), true);
  assert.equal(host.inline, null);
  assert.equal(bridge.destroyed, true);
  assert.equal(find(container, 'iframe'), null);
  assert.equal(await host.unmountInline(), false);

  const closed = calls.filter(
    call => call.path === '/api/workspace-surfaces/sessions' && call.options.method === 'DELETE'
  );
  assert.equal(closed.length, 1);
});

// Operations must resolve against the inline record, not this.active. With no
// modal open, an inline operation that read this.active would fail outright.
test('inline operations use the inline session with no modal open', async () => {
  const { host, calls } = await mountedDashboard();
  assert.equal(host.active, null);

  const result = await host.inline.bridge.options.onRequest({
    type: 'ori.surface.operation.invoke',
    payload: { operation_id: 'greeting.create', input: { name: 'Ori' } }
  });
  assert.equal(result.ok, true);

  const invoked = calls.filter(call => call.path === '/api/workspace-surfaces/operations');
  assert.equal(invoked.length, 1);
  assert.equal(JSON.parse(invoked[0].options.body).session, host.inline.session);
});

// Host intents act on the modal the user is looking at. An inline surface must
// not be able to reach them, even while a modal happens to be open.
test('inline surfaces cannot reach modal host intents', async () => {
  const { host } = await mountedDashboard();
  await host.open('plugin:workspace-surface-demo:demo-tools:main', new NodeFake('button'));
  const modalBefore = host.active;

  for (const type of [
    'ori.surface.host.ask_ori',
    'ori.surface.host.create_task',
    'ori.surface.host.open_setup',
    'ori.surface.host.close'
  ]) {
    const result = await host.inline.bridge.options.onRequest({ type, payload: {} });
    assert.equal(result.ok, false, `${type} was allowed from an inline surface`);
    assert.equal(result.error.code, 'host_intent_unavailable');
  }
  assert.equal(host.active, modalBefore, 'an inline request disturbed the open modal');
});

// A dashboard deleted from the workspace folder leaves the catalog. The live
// frame must not be left pointed at a surface the server no longer resolves.
test('an inline surface that leaves the catalog is unmounted', async () => {
  const { host, container } = await mountedDashboard();
  const bridge = host.inline.bridge;

  host.surfaces = host.surfaces.filter(surface => surface.key !== DASHBOARD_KEY);
  host._reconcileActiveSurface();
  await Promise.resolve();

  assert.equal(host.inline, null);
  assert.equal(bridge.destroyed, true);
  assert.equal(find(container, 'iframe'), null);
});

test('an unavailable or unknown surface never mounts inline', async () => {
  const { host } = makeHost();
  await host.loadCatalog();
  const container = new NodeFake('div');
  assert.equal(await host.mountInline(DASHBOARD_KEY, container), false);

  host.surfaces = [...host.surfaces, dashboardSurface({ available: false })];
  assert.equal(await host.mountInline(DASHBOARD_KEY, container), false);
  assert.equal(host.inline, null);
  assert.equal(find(container, 'iframe'), null);
});

// A modal opening over a dashboard must not tear the dashboard down; the two
// slots are independent.
test('opening and closing a modal leaves an inline mount intact', async () => {
  const { host, container } = await mountedDashboard();
  const frame = find(container, 'iframe');
  const bridge = host.inline.bridge;

  await host.open('plugin:workspace-surface-demo:demo-tools:main', new NodeFake('button'));
  assert.equal(host.inline?.bridge, bridge);
  await host.close();
  await Promise.resolve();

  assert.equal(host.inline?.bridge, bridge, 'closing a modal tore down the inline mount');
  assert.equal(bridge.destroyed, false);
  assert.equal(find(container, 'iframe'), frame);
});

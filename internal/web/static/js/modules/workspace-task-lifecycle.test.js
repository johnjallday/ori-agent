import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createTaskPageDataLoader } from './workspace-task-data-loader.js';

globalThis.window = {};
globalThis.document = { getElementById: () => null };
globalThis.escapeHtml = value => String(value);
const { WorkspaceTaskPage } = await import('./workspace-task.js');

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

async function flush() {
  // Drain the transport/JSON/loader microtasks without counting implementation
  // layers; deliberately unresolved responses remain pending.
  await new Promise(resolve => setImmediate(resolve));
}

function snapshot(label) {
  const task = { id: 'task', workspace_id: 'ws', description: label, current_run_id: label };
  return {
    label,
    task,
    workspace: { id: 'ws', name: label, tasks: [task] },
    agents: [{ name: label }],
    events: [{ type: label }],
    outputDir: label,
    runs: [{ id: label }]
  };
}

function makePage(t, snapshots = []) {
  let current;
  let index = 0;
  const view = { renders: 0, states: [], alerts: [], errors: [] };
  const response = body => ({ ok: true, status: 200, json: async () => body });
  const loader = createTaskPageDataLoader(async (url, options) => {
    if (url === '/api/workspaces/ws') {
      current = snapshots[index++];
      assert.ok(current, 'unexpected data request');
      current.signal = options?.signal;
      return response(current.workspace);
    }
    if (url === '/api/orchestration/tasks?id=task') return response(current.task);
    if (url === '/api/agents') return response({ agents: current.agents });
    if (url.startsWith('/api/orchestration/events?')) return response({ events: current.events });
    if (url === '/api/workspaces/ws/output-dir') return response({ output_dir: current.outputDir });
    if (url.startsWith('/api/workspaces/ws/runs/')) {
      const runId = decodeURIComponent(url.split('/').at(-1));
      const source = snapshots.find(
        item => item.label === runId || item.task?.current_run_id === runId
      );
      return response(((await source?.runs) || []).find(run => run.id === runId) || null);
    }
    // Plan tests exercise the real lookup through their mocked transport.
    assert.ok(url.startsWith('/api/workspaces/ws/plan-for-task/'), `unexpected request: ${url}`);
    return fetch(url, options);
  });
  const page = new WorkspaceTaskPage('ws', 'task', 'workspace-slug', loader);
  page.loadRelatedPlan = async () => {};
  page.render = () => view.renders++;
  page.setState = state => view.states.push(state);
  page.setAlert = message => view.alerts.push(message);
  page.seedLiveActivityFromHistory = () => {};
  page.stopActivityTick = () => {};
  page.closeResultSectionMenu = () => {};
  page.captureLiveActivity = () => {};
  page.cacheElements = () => {};
  page.bindEvents = () => {};
  t.mock.method(console, 'error', (...args) => view.errors.push(args));
  return { page, view };
}

function loadedSnapshot(label) {
  const source = snapshot(label);
  return {
    workspace: source.workspace,
    task: source.task,
    tasks: source.workspace.tasks,
    workspaceOutputDir: source.outputDir,
    availableAgents: source.agents,
    taskEvents: source.events,
    workspaceRuns: source.runs,
    currentRun: source.runs[0]
  };
}

for (const [olderMethod, newerMethod] of [
  ['loadData', 'loadData'],
  ['loadData', 'refreshAfterStepChange'],
  ['refreshAfterStepChange', 'loadData'],
  ['refreshAfterStepChange', 'refreshAfterStepChange']
]) {
  test(`controller retains ownership when ${newerMethod} supersedes a cancellation-ignoring ${olderMethod} loader`, async t => {
    const pending = deferred();
    const { page, view } = makePage(t);
    const inputs = [];
    const read = input => {
      inputs.push(input);
      return inputs.length === 1 ? pending.promise : Promise.resolve(loadedSnapshot('new'));
    };
    page.dataLoader = { loadSnapshot: read, refreshSnapshot: read };
    const oldRead = page[olderMethod]();
    await page[newerMethod]();
    assert.equal(inputs[0].signal.aborted, true);
    assert.equal(inputs[1].signal.aborted, false);
    assert.equal(inputs[0].workspaceId, 'ws');
    assert.equal(inputs[0].taskId, 'task');
    assert.ok(!('controller' in inputs[0]), 'the loader receives a signal, not request ownership');
    pending.resolve(loadedSnapshot('old'));
    await oldRead;
    assert.equal(page.task.description, 'new');
    assert.equal(page.currentRun.id, 'new');
    assert.equal(view.renders, 1);
    assert.equal(view.states.at(-1), 'content');
  });
}

for (const method of ['loadData', 'refreshAfterStepChange']) {
  test(`controller discards a cancellation-ignoring ${method} loader snapshot after teardown`, async t => {
    const pending = deferred();
    const { page, view } = makePage(t);
    page.dataLoader = {
      loadSnapshot: () => pending.promise,
      refreshSnapshot: () => pending.promise
    };
    const read = page[method]();
    page.destroy();
    const states = view.states.length;
    pending.resolve(loadedSnapshot('late'));
    await read;
    assert.equal(page.task, null);
    assert.equal(view.renders, 0);
    assert.equal(view.states.length, states);
  });
}

test('controller refresh passes only cached data and preserves auxiliary page metadata', async t => {
  const { page, view } = makePage(t);
  const cached = loadedSnapshot('cached');
  Object.assign(page, cached);
  page.dataLoader = {
    refreshSnapshot: async input => {
      assert.deepEqual(input.previousSnapshot, {
        workspace: cached.workspace,
        tasks: cached.tasks,
        task: cached.task
      });
      assert.ok(!('elements' in input));
      return loadedSnapshot('new');
    }
  };
  await page.refreshAfterStepChange();
  assert.equal(page.task.description, 'new');
  assert.equal(page.workspaceOutputDir, 'cached');
  assert.deepEqual(page.availableAgents, cached.availableAgents);
  assert.deepEqual(page.taskEvents, cached.taskEvents);
  assert.equal(view.renders, 1);
});

function fakeTimers(t) {
  const timers = new Map();
  let next = 1;
  const schedule = callback => {
    const id = next++;
    timers.set(id, callback);
    return id;
  };
  const cancel = id => timers.delete(id);
  t.mock.method(globalThis, 'setTimeout', schedule);
  t.mock.method(globalThis, 'clearTimeout', cancel);
  window.setTimeout = schedule;
  window.clearTimeout = cancel;
  return {
    timers,
    async run(id = timers.keys().next().value) {
      const callback = timers.get(id);
      timers.delete(id);
      await callback();
      await flush();
    }
  };
}

for (const [olderMethod, newerMethod] of [
  ['loadData', 'loadData'],
  ['loadData', 'refreshAfterStepChange'],
  ['refreshAfterStepChange', 'loadData'],
  ['refreshAfterStepChange', 'refreshAfterStepChange']
]) {
  test(`${newerMethod} supersedes an older ${olderMethod} even when abort is ignored`, async t => {
    const pending = deferred();
    const older = snapshot('old');
    older.workspace = pending.promise;
    const newer = snapshot('new');
    const { page, view } = makePage(t, [older, newer]);
    const oldRead = page[olderMethod]();
    await page[newerMethod]();
    pending.resolve({ id: 'ws', name: 'old', tasks: [older.task] });
    await oldRead;
    assert.equal(page.task.description, 'new');
    assert.equal(page.workspace.name, 'new');
    assert.equal(page.currentRun.id, 'new');
    assert.equal(view.renders, 1);
    assert.equal(
      view.states.at(-1),
      'content',
      'a superseding refresh must not leave initial loading visible'
    );
    assert.equal(older.signal?.aborted, true, 'superseded reads should release network resources');
    if (newerMethod === 'loadData') {
      assert.equal(page.availableAgents[0].name, 'new');
      assert.equal(page.taskEvents[0].type, 'new');
      assert.equal(page.workspaceOutputDir, 'new');
    }
  });
}

for (const stage of ['workspace', 'runs']) {
  test(`load commits atomically and ignores a superseded ${stage} response`, async t => {
    const pending = deferred();
    const older = snapshot('old');
    if (stage === 'workspace') older.workspace = pending.promise;
    else older.runs = pending.promise;
    const { page, view } = makePage(t, [older, snapshot('new')]);
    page.task = { id: 'task', description: 'cached' };
    const oldRead = page.loadData();
    await flush();
    assert.equal(page.task.description, 'cached', 'partial responses must not mutate page state');
    await page.loadData();
    pending.resolve(
      stage === 'workspace' ? { id: 'ws', name: 'old', tasks: [older.task] } : [{ id: 'old' }]
    );
    await oldRead;
    assert.equal(page.task.description, 'new');
    assert.equal(page.currentRun.id, 'new');
    assert.equal(view.renders, 1);
  });
}

test('an obsolete load failure cannot clear a newer successful view', async t => {
  const pending = deferred();
  const older = snapshot('old');
  older.workspace = pending.promise;
  const { page, view } = makePage(t, [older, snapshot('new')]);
  const oldRead = page.loadData();
  await page.loadData();
  pending.reject(new Error('obsolete failure'));
  await oldRead;
  assert.equal(page.task.description, 'new');
  assert.equal(view.states.at(-1), 'content');
  assert.ok(!view.alerts.includes('obsolete failure'));
  assert.equal(view.errors.length, 0);
});

test('a current load failure still reports an error', async t => {
  const pending = deferred();
  const source = snapshot('failed');
  source.workspace = pending.promise;
  const { page, view } = makePage(t, [source]);
  const read = page.loadData();
  pending.reject(new Error('current failure'));
  await read;
  assert.equal(view.states.at(-1), 'empty');
  assert.equal(view.alerts.at(-1), 'current failure');
  assert.equal(view.errors.length, 1);
});

for (const method of ['loadData', 'refreshAfterStepChange']) {
  test(`destroy invalidates an in-flight ${method}`, async t => {
    fakeTimers(t);
    const pending = deferred();
    const source = snapshot('late');
    source.workspace = pending.promise;
    const { page, view } = makePage(t, [source]);
    const read = page[method]();
    page.destroy();
    const statesAtDestroy = view.states.length;
    pending.resolve({ id: 'ws', name: 'late', tasks: [source.task] });
    await read;
    assert.equal(page.task, null);
    assert.equal(view.renders, 0);
    assert.equal(view.states.length, statesAtDestroy);
    assert.equal(source.signal?.aborted, true);
  });
}

test('destroy during run lookup cannot commit task data or render', async t => {
  fakeTimers(t);
  const pending = deferred();
  const source = snapshot('late');
  source.runs = pending.promise;
  const { page, view } = makePage(t, [source]);
  const read = page.loadData();
  await flush();
  page.destroy();
  pending.resolve([{ id: 'late' }]);
  await read;
  assert.equal(page.task, null);
  assert.equal(view.renders, 0);
});

test('init cannot subscribe after being destroyed while loading', async t => {
  fakeTimers(t);
  const pending = deferred();
  const source = snapshot('late');
  source.workspace = pending.promise;
  const { page } = makePage(t, [source]);
  let subscriptions = 0;
  window.workspaceRealtime = {
    subscribeToWorkspace: () => {
      subscriptions++;
      return () => {};
    }
  };
  const init = page.init();
  page.destroy();
  pending.resolve({ id: 'ws', tasks: [source.task] });
  await init;
  assert.equal(subscriptions, 0);
  delete window.workspaceRealtime;
});

test('a destroyed page cannot restart reads, realtime, or polling', async t => {
  const { timers } = fakeTimers(t);
  const { page, view } = makePage(t);
  let subscriptions = 0;
  window.workspaceRealtime = {
    subscribeToWorkspace: () => {
      subscriptions++;
      return () => {};
    }
  };
  page.destroy();
  await page.init();
  await page.loadData();
  await page.refreshAfterStepChange();
  page.setupRealtime();
  page.handleRealtimeEvent({ type: 'task.started', data: { task_id: 'task' } });
  page.pollStepCompletion('task');
  assert.equal(subscriptions, 0);
  assert.equal(timers.size, 0);
  assert.equal(view.renders, 0);
  assert.equal(view.states.length, 0);
  delete window.workspaceRealtime;
});

test('realtime invalidates pending reads immediately, before the debounced reload', async t => {
  const clock = fakeTimers(t);
  const pending = deferred();
  const older = snapshot('old');
  older.workspace = pending.promise;
  const { page, view } = makePage(t, [older, snapshot('new')]);
  const read = page.loadData();
  page.handleRealtimeEvent({ type: 'task.started', data: { task_id: 'task' } });
  pending.resolve({ id: 'ws', name: 'old', tasks: [older.task] });
  await read;
  assert.equal(view.renders, 0, 'a response started before the event must not render');
  assert.equal(older.signal?.aborted, true);
  await clock.run();
  assert.equal(page.task.description, 'new');
  assert.equal(view.renders, 1);
  assert.equal(page.pendingRefreshTimer, null);
});

test('unrelated realtime events do not invalidate a pending read', async t => {
  const clock = fakeTimers(t);
  const pending = deferred();
  const source = snapshot('current');
  source.workspace = pending.promise;
  const { page, view } = makePage(t, [source]);
  const read = page.loadData();
  page.handleRealtimeEvent({ type: 'task.started', data: { task_id: 'unrelated' } });
  pending.resolve({ id: 'ws', tasks: [source.task] });
  await read;
  assert.equal(view.renders, 1);
  assert.equal(clock.timers.size, 0);
  assert.equal(source.signal?.aborted, false);
});

test('destroy clears polling and debounce timers and unsubscribes only once', t => {
  const clock = fakeTimers(t);
  const { page } = makePage(t);
  let unsubscribed = 0;
  page.workspaceRealtimeUnsubscribe = () => unsubscribed++;
  page.pollStepCompletion('task');
  page.handleRealtimeEvent({ type: 'task.started', data: { task_id: 'task' } });
  assert.equal(clock.timers.size, 2);
  page.destroy();
  page.destroy();
  assert.equal(unsubscribed, 1);
  assert.equal(clock.timers.size, 0);
  assert.equal(page.workflowPollTimer, null);
});

test('an in-flight poll cannot resurrect a timer after destroy', async t => {
  const clock = fakeTimers(t);
  const { page } = makePage(t);
  const pending = deferred();
  page.refreshAfterStepChange = () => pending.promise;
  page.pollStepCompletion('task');
  const tick = clock.run();
  await flush();
  page.destroy();
  pending.resolve();
  await tick;
  assert.equal(clock.timers.size, 0);
});

test('replacing an in-flight poll leaves only the replacement timer', async t => {
  const clock = fakeTimers(t);
  const { page } = makePage(t);
  const pending = deferred();
  page.refreshAfterStepChange = () => pending.promise;
  page.pollStepCompletion('first');
  const tick = clock.run();
  await flush();
  page.pollStepCompletion('second');
  pending.resolve();
  await tick;
  assert.equal(clock.timers.size, 1);
});

test('related-plan responses cannot touch a destroyed page', async t => {
  fakeTimers(t);
  const pending = deferred();
  const container = { innerHTML: 'untouched', hidden: false };
  const { page } = makePage(t, [snapshot('current')]);
  t.mock.method(document, 'getElementById', () => container);
  t.mock.method(globalThis, 'fetch', () => pending.promise);
  await page.loadData();
  const plan = WorkspaceTaskPage.prototype.loadRelatedPlan.call(page);
  page.destroy();
  pending.resolve({
    ok: true,
    json: async () => ({ plan_id: 'late', title: 'Late', url: '/late' })
  });
  await plan;
  assert.equal(container.innerHTML, 'untouched');
  assert.equal(container.hidden, false);
});

test('a superseding refresh can hydrate from the workspace when the task endpoint is unavailable', async t => {
  const pending = deferred();
  const older = snapshot('old');
  older.workspace = pending.promise;
  const newer = snapshot('new');
  newer.task = null;
  const { page, view } = makePage(t, [older, newer]);
  const load = page.loadData();
  await page.refreshAfterStepChange();
  pending.resolve({ id: 'ws', tasks: [older.task] });
  await load;
  assert.equal(page.task?.description, 'new');
  assert.equal(view.states.at(-1), 'content');
});

for (const cached of [false, true]) {
  test(`a refresh failure restores ${cached ? 'cached content' : 'an empty view'} after superseding a load`, async t => {
    const oldPending = deferred();
    const newPending = deferred();
    const older = snapshot('old');
    older.workspace = oldPending.promise;
    const newer = snapshot('failed');
    newer.workspace = newPending.promise;
    const { page, view } = makePage(t, [older, newer]);
    if (cached) page.task = { id: 'task', description: 'cached' };
    const load = page.loadData();
    const refresh = page.refreshAfterStepChange();
    newPending.reject(new Error('refresh failed'));
    await refresh;
    oldPending.resolve({ id: 'ws', tasks: [older.task] });
    await load;
    assert.equal(view.states.at(-1), cached ? 'content' : 'empty');
    assert.equal(page.task?.description, cached ? 'cached' : undefined);
  });
}

test('changing task identity invalidates an outstanding read', async t => {
  const pending = deferred();
  const source = snapshot('old identity');
  source.workspace = pending.promise;
  const { page, view } = makePage(t, [source]);
  const load = page.loadData();
  page.taskId = 'another-task';
  pending.resolve({ id: 'ws', tasks: [source.task] });
  await load;
  assert.equal(page.task, null);
  assert.equal(view.renders, 0);
});

test('a superseded related-plan response cannot overwrite the newer plan', async t => {
  const pending = deferred();
  const container = { innerHTML: 'untouched', hidden: false };
  const { page } = makePage(t, [snapshot('first'), snapshot('second')]);
  t.mock.method(document, 'getElementById', () => container);
  let calls = 0;
  t.mock.method(globalThis, 'fetch', () => {
    calls++;
    return calls === 1
      ? pending.promise
      : Promise.resolve({ ok: true, json: async () => ({ plan_id: 'new', title: 'New plan' }) });
  });
  page.loadRelatedPlan = WorkspaceTaskPage.prototype.loadRelatedPlan;
  await page.loadData();
  await page.loadData();
  await flush();
  pending.resolve({ ok: true, json: async () => ({ plan_id: 'old', title: 'Old plan' }) });
  await flush();
  assert.ok(container.innerHTML.includes('New plan'));
  assert.ok(!container.innerHTML.includes('Old plan'));
});

test('a superseding refresh replaces its aborted plan lookup', async t => {
  const pending = deferred();
  const container = { innerHTML: 'untouched', hidden: false };
  const { page } = makePage(t, [snapshot('first'), snapshot('second')]);
  t.mock.method(document, 'getElementById', () => container);
  let calls = 0;
  t.mock.method(globalThis, 'fetch', () => {
    calls++;
    return calls === 1
      ? pending.promise
      : Promise.resolve({ ok: true, json: async () => ({ plan_id: 'new', title: 'New plan' }) });
  });
  page.loadRelatedPlan = WorkspaceTaskPage.prototype.loadRelatedPlan;
  await page.loadData();
  await page.refreshAfterStepChange();
  await flush();
  pending.resolve({ ok: true, json: async () => ({ plan_id: 'old', title: 'Old plan' }) });
  await flush();
  assert.equal(calls, 2);
  assert.ok(container.innerHTML.includes('New plan'));
});

test('data loader propagates cancellation to every request and run lookup', async t => {
  const calls = [];
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    calls.push({ url: String(url), signal: options?.signal });
    return {
      ok: true,
      json: async () => ({
        id: 'run',
        current_run_id: 'run',
        execution_history: [{ run_id: 'previous' }],
        agents: [],
        events: []
      })
    };
  });
  const controller = new AbortController();
  await createTaskPageDataLoader().loadSnapshot({
    workspaceId: 'ws',
    taskId: 'task',
    signal: controller.signal
  });
  assert.equal(calls.length, 7);
  assert.ok(calls.every(call => call.signal === controller.signal));
});

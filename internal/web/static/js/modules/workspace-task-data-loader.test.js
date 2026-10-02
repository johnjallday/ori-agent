import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createTaskPageDataLoader } from './workspace-task-data-loader.js';

function deferred() {
  let resolve;
  const promise = new Promise(yes => (resolve = yes));
  return { promise, resolve };
}

function response(body, status = 200) {
  return { ok: status >= 200 && status < 300, status, json: async () => body };
}

function fixture(overrides = {}) {
  const task = {
    id: 'task/1',
    workspace_id: 'workspace/uuid',
    current_run_id: 'current',
    execution_history: [{ run_id: 'previous' }]
  };
  const workspace = { id: 'workspace/uuid', name: 'Workspace', tasks: [task] };
  const routes = {
    '/api/workspaces/workspace%2Fuuid': response(workspace),
    '/api/orchestration/tasks?id=task%2F1': response(task),
    '/api/agents': response({ agents: [{ name: 'Agent' }] }),
    '/api/orchestration/events?workspace_id=workspace%2Fuuid&task_id=task%2F1&limit=200': response({
      events: [{ type: 'task.started' }]
    }),
    '/api/workspaces/workspace%2Fuuid/output-dir': response({ output_dir: '  /outputs  ' }),
    '/api/workspaces/workspace%2Fuuid/runs/current': response({
      id: 'current',
      started_at: '2026-01-02T00:00:00Z'
    }),
    '/api/workspaces/workspace%2Fuuid/runs/previous': response({
      id: 'previous',
      finished_at: '2026-01-03T00:00:00Z'
    }),
    '/api/workspaces/workspace%2Fuuid/plan-for-task/task%2F1': response({
      plan_id: 'plan',
      title: 'Plan',
      url: '/plans/plan'
    }),
    ...overrides
  };
  const calls = [];
  const loader = createTaskPageDataLoader(async (url, options) => {
    calls.push({ url, options });
    assert.ok(Object.hasOwn(routes, url), `unexpected request: ${url}`);
    const result = routes[url];
    if (result instanceof Error) throw result;
    return result;
  });
  const request = { workspaceId: 'workspace/uuid', taskId: 'task/1' };
  return { loader, request, calls, task, workspace };
}

const workspaceURL = '/api/workspaces/workspace%2Fuuid';
const taskURL = '/api/orchestration/tasks?id=task%2F1';
const agentsURL = '/api/agents';
const eventsURL =
  '/api/orchestration/events?workspace_id=workspace%2Fuuid&task_id=task%2F1&limit=200';
const outputURL = '/api/workspaces/workspace%2Fuuid/output-dir';
const currentRunURL = '/api/workspaces/workspace%2Fuuid/runs/current';
const previousRunURL = '/api/workspaces/workspace%2Fuuid/runs/previous';
const planURL = '/api/workspaces/workspace%2Fuuid/plan-for-task/task%2F1';

test('loader works without browser globals and returns a complete initial snapshot', async () => {
  assert.equal(typeof window, 'undefined');
  assert.equal(typeof document, 'undefined');
  const { loader, request, task, workspace, calls } = fixture();
  const snapshot = await loader.loadSnapshot(request);
  assert.deepEqual(snapshot, {
    workspace,
    task,
    tasks: [task],
    workspaceOutputDir: '/outputs',
    availableAgents: [{ name: 'Agent' }],
    taskEvents: [{ type: 'task.started' }],
    workspaceRuns: [
      { id: 'previous', finished_at: '2026-01-03T00:00:00Z' },
      { id: 'current', started_at: '2026-01-02T00:00:00Z' }
    ],
    currentRun: { id: 'current', started_at: '2026-01-02T00:00:00Z' }
  });
  assert.equal(calls.length, 7);
  assert.ok(!calls.some(call => call.url === planURL), 'plan lookup must stay non-blocking');
});

test('a task 404 falls back to the embedded workspace task', async () => {
  const { loader, request, task } = fixture({ [taskURL]: response(null, 404) });
  assert.deepEqual((await loader.loadSnapshot(request)).task, task);
});

test('a direct task not present in the workspace replaces the task list', async () => {
  const { loader, request, task } = fixture({
    [workspaceURL]: response({ id: 'workspace/uuid', tasks: [{ id: 'unrelated' }] })
  });
  const snapshot = await loader.loadSnapshot(request);
  assert.deepEqual(snapshot.tasks, [task]);
  assert.deepEqual(snapshot.task, task);
});

for (const invalid of [
  null,
  { id: 'task/1', workspace_id: 'foreign', current_run_id: 'current' }
]) {
  test(`an initial ${invalid ? 'foreign' : 'missing'} task yields an empty snapshot without run reads`, async () => {
    const { loader, request, calls } = fixture({
      [workspaceURL]: response({ id: 'workspace/uuid' }),
      [taskURL]: response(invalid, invalid ? 200 : 404)
    });
    const snapshot = await loader.loadSnapshot(request);
    assert.equal(snapshot.task, null);
    assert.deepEqual(snapshot.workspaceRuns, []);
    assert.equal(snapshot.currentRun, null);
    assert.ok(!calls.some(call => call.url.includes('/runs/')));
  });
}

for (const [url, message] of [
  [workspaceURL, 'Failed to load workspace details.'],
  [taskURL, 'Failed to load task details.']
]) {
  test(`initial required endpoint failure propagates: ${url}`, async () => {
    const { loader, request } = fixture({ [url]: response(null, 500) });
    await assert.rejects(loader.loadSnapshot(request), { message });
  });
}

for (const url of [workspaceURL, taskURL]) {
  test(`initial required JSON failure propagates: ${url}`, async () => {
    const failure = new Error('invalid JSON');
    const { loader, request } = fixture({
      [url]: {
        ok: true,
        json: async () => {
          throw failure;
        }
      }
    });
    await assert.rejects(loader.loadSnapshot(request), failure);
  });
}

for (const url of [agentsURL, eventsURL, outputURL]) {
  for (const failure of [new Error('offline'), response(null, 500)]) {
    test(`optional endpoint ${url} tolerates ${failure instanceof Error ? 'network' : 'HTTP'} failure`, async () => {
      const { loader, request } = fixture({ [url]: failure });
      const snapshot = await loader.loadSnapshot(request);
      assert.ok(snapshot.task);
      if (url === agentsURL) assert.deepEqual(snapshot.availableAgents, []);
      if (url === eventsURL) assert.deepEqual(snapshot.taskEvents, []);
      if (url === outputURL) assert.equal(snapshot.workspaceOutputDir, '');
    });
  }
}

test('malformed optional payloads use empty metadata defaults', async () => {
  const { loader, request } = fixture({
    [agentsURL]: response({ agents: {} }),
    [eventsURL]: {
      ok: true,
      json: async () => {
        throw new Error('bad events JSON');
      }
    },
    [outputURL]: response({})
  });
  const snapshot = await loader.loadSnapshot(request);
  assert.deepEqual(snapshot.availableAgents, []);
  assert.deepEqual(snapshot.taskEvents, []);
  assert.equal(snapshot.workspaceOutputDir, '');
});

test('run IDs are trimmed, deduplicated, and sorted with timestamp fallbacks', async () => {
  const task = {
    id: 'task/1',
    current_run_id: ' current ',
    execution_history: [
      { run_id: 'current' },
      { run_id: ' previous ' },
      { run_id: '' },
      null,
      { run_id: 'bad' },
      { run_id: 'created' }
    ]
  };
  const { loader, request, calls } = fixture({
    [taskURL]: response(task),
    '/api/workspaces/workspace%2Fuuid/runs/bad': response({ id: 'bad', created_at: 'invalid' }),
    '/api/workspaces/workspace%2Fuuid/runs/created': response({
      id: 'created',
      created_at: '2026-01-01T00:00:00Z'
    })
  });
  const snapshot = await loader.loadSnapshot(request);
  assert.deepEqual(
    snapshot.workspaceRuns.map(run => run.id),
    ['previous', 'current', 'created', 'bad']
  );
  assert.equal(snapshot.currentRun.id, 'current');
  assert.equal(calls.filter(call => call.url.includes('/runs/')).length, 4);
});

test('run 404s and failures are ignored individually', async () => {
  const { loader, request } = fixture({
    [currentRunURL]: response(null, 404),
    [previousRunURL]: new Error('offline')
  });
  const snapshot = await loader.loadSnapshot(request);
  assert.deepEqual(snapshot.workspaceRuns, []);
  assert.equal(snapshot.currentRun, null);
});

test('snapshot resolution waits for run metadata without mutating input', async () => {
  const pending = deferred();
  const task = Object.freeze({ id: 'task/1', current_run_id: 'current' });
  const workspace = Object.freeze({ id: 'workspace/uuid', tasks: Object.freeze([task]) });
  const { loader, request } = fixture({
    [workspaceURL]: response(workspace),
    [taskURL]: response(task),
    [currentRunURL]: pending.promise
  });
  let resolved = false;
  const read = loader.loadSnapshot(request).then(value => {
    resolved = true;
    return value;
  });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(resolved, false);
  pending.resolve(response({ id: 'current' }));
  assert.equal((await read).currentRun.id, 'current');
  assert.deepEqual(workspace, { id: 'workspace/uuid', tasks: [task] });
});

test('refresh reads only workspace, task, and runs and leaves auxiliary metadata to the page', async () => {
  const { loader, request, calls } = fixture();
  const snapshot = await loader.refreshSnapshot(request);
  assert.ok(snapshot.task);
  assert.deepEqual(Object.keys(snapshot).sort(), [
    'currentRun',
    'task',
    'tasks',
    'workspace',
    'workspaceRuns'
  ]);
  assert.equal(calls.length, 4);
  assert.ok(!calls.some(call => [agentsURL, eventsURL, outputURL, planURL].includes(call.url)));
});

for (const failure of [response(null, 404), response(null, 500), new Error('offline')]) {
  test(`refresh task ${failure instanceof Error ? 'network failure' : failure.status} hydrates from the workspace`, async () => {
    const { loader, request, task } = fixture({ [taskURL]: failure });
    assert.deepEqual((await loader.refreshSnapshot(request)).task, task);
  });
}

test('refresh preserves cached workspace and task when responses omit them without mutating the cache', async () => {
  const task = Object.freeze({ id: 'task/1', description: 'cached' });
  const tasks = Object.freeze([task]);
  const workspace = Object.freeze({ id: 'workspace/uuid', tasks });
  const previousSnapshot = Object.freeze({ workspace, tasks, task });
  const { loader, request } = fixture({
    [workspaceURL]: response(null),
    [taskURL]: response(null, 404)
  });
  const snapshot = await loader.refreshSnapshot({ ...request, previousSnapshot });
  assert.equal(snapshot.workspace, workspace);
  assert.equal(snapshot.task, task);
  assert.deepEqual(snapshot.tasks, tasks);
  assert.deepEqual(snapshot.workspaceRuns, []);
  assert.equal(snapshot.currentRun, null);
});

test('refresh without cached state can return an empty snapshot', async () => {
  const { loader, request } = fixture({
    [workspaceURL]: response({ id: 'workspace/uuid' }),
    [taskURL]: response(null, 404)
  });
  const snapshot = await loader.refreshSnapshot(request);
  assert.equal(snapshot.task, null);
  assert.deepEqual(snapshot.tasks, []);
  assert.deepEqual(snapshot.workspaceRuns, []);
});

test('refresh required workspace failure still propagates', async () => {
  const { loader, request } = fixture({ [workspaceURL]: response(null, 500) });
  await assert.rejects(loader.refreshSnapshot(request), {
    message: 'Failed to load workspace details.'
  });
});

for (const method of ['loadSnapshot', 'refreshSnapshot']) {
  test(`${method} forwards the supplied cancellation signal through every read`, async () => {
    const { loader, request, calls } = fixture();
    const controller = new AbortController();
    await loader[method]({ ...request, signal: controller.signal });
    assert.ok(calls.length > 0);
    assert.ok(calls.every(call => call.options?.signal === controller.signal));
    assert.equal(controller.signal.aborted, false, 'the caller owns cancellation');
  });

  test(`${method} does not start reads for an already-aborted signal`, async () => {
    const { loader, request, calls } = fixture();
    const controller = new AbortController();
    controller.abort();
    await assert.rejects(loader[method]({ ...request, signal: controller.signal }), {
      name: 'AbortError'
    });
    assert.equal(calls.length, 0);
  });

  test(`${method} stops before run reads if cancellation happened while an ignoring transport was pending`, async () => {
    const pending = deferred();
    const { loader, request, calls, workspace } = fixture({ [workspaceURL]: pending.promise });
    const controller = new AbortController();
    const read = loader[method]({ ...request, signal: controller.signal });
    controller.abort();
    pending.resolve(response(workspace));
    await assert.rejects(read, { name: 'AbortError' });
    assert.ok(!calls.some(call => call.url.includes('/runs/')));
  });
}

test('one loader handles overlapping requests with explicit identities, not remembered page state', async () => {
  const pending = deferred();
  const calls = [];
  const loader = createTaskPageDataLoader(async url => {
    calls.push(url);
    if (url === '/api/workspaces/first') return pending.promise;
    const first = url.includes('first');
    if (url.includes('/runs/'))
      return response({ id: 'run', workspace_id: first ? 'first' : 'second' });
    return response(
      url.includes('/tasks?')
        ? { id: first ? 'first-task' : 'second-task', current_run_id: 'run' }
        : {}
    );
  });
  const oldRead = loader.loadSnapshot({ workspaceId: 'first', taskId: 'first-task' });
  const newRead = await loader.loadSnapshot({ workspaceId: 'second', taskId: 'second-task' });
  assert.equal(newRead.task.id, 'second-task');
  assert.equal(newRead.currentRun.workspace_id, 'second');
  pending.resolve(response({ id: 'first' }));
  const oldSnapshot = await oldRead;
  assert.equal(oldSnapshot.workspace.id, 'first');
  assert.equal(oldSnapshot.currentRun.workspace_id, 'first');
  assert.ok(calls.includes('/api/orchestration/tasks?id=first-task'));
  assert.ok(calls.includes('/api/orchestration/tasks?id=second-task'));
  assert.ok(calls.includes('/api/workspaces/first/runs/run'));
  assert.ok(calls.includes('/api/workspaces/second/runs/run'));
});

test('related-plan lookup uses explicit IDs and the caller signal independently of snapshots', async () => {
  const { loader, request, calls } = fixture();
  const controller = new AbortController();
  const related = await loader.loadRelatedPlan({ ...request, signal: controller.signal });
  assert.equal(related.plan_id, 'plan');
  assert.deepEqual(calls, [{ url: planURL, options: { signal: controller.signal } }]);
});

for (const failure of [response(null, 404), new Error('offline'), response({})]) {
  test(`missing or unavailable plan is optional: ${failure instanceof Error ? 'network' : failure.status}`, async () => {
    const { loader, request } = fixture({ [planURL]: failure });
    assert.equal(await loader.loadRelatedPlan(request), null);
  });
}

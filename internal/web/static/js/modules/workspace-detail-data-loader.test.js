import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createWorkspaceDetailDataLoader } from './workspace-detail-data-loader.js';

const response = (body, status = 200) => ({
  ok: status >= 200 && status < 300,
  status,
  json: async () => body
});
const request = { workspaceId: 'workspace/uuid with + spaces' };
const tasksURL = '/api/orchestration/tasks?workspace_id=workspace%2Fuuid%20with%20%2B%20spaces';
const backlogURL = '/api/orchestration/backlog?workspace_id=workspace%2Fuuid+with+%2B+spaces';
const resources = [
  { method: 'loadTasks', url: tasksURL, message: 'Failed to load tasks' },
  { method: 'loadBacklog', url: backlogURL, message: 'Failed to load backlog' }
];

function fixture(body, status = 200) {
  const calls = [];
  const loader = createWorkspaceDetailDataLoader(async (url, options) => {
    calls.push({ url, options });
    return response(body, status);
  });
  return { loader, calls };
}

for (const resource of resources) {
  test(`${resource.method} works without browser globals and forwards the UUID and signal`, async () => {
    assert.equal(typeof window, 'undefined');
    assert.equal(typeof document, 'undefined');
    const signal = new AbortController().signal;
    const tasks = Object.freeze([{ id: 'task' }]);
    const items = Object.freeze([{ id: 'backlog' }]);
    const sync = Object.freeze({ state: 'synced' });
    const body = Object.freeze({ tasks, items, sync, unrelated: 'must not return' });
    const { loader, calls } = fixture(body);
    const snapshot = await loader[resource.method](Object.freeze({ ...request, signal }));
    assert.deepEqual(snapshot, resource.method === 'loadTasks' ? { tasks } : { items, sync });
    assert.notEqual(snapshot, body);
    assert.deepEqual(calls, [{ url: resource.url, options: { signal } }]);
    assert.equal(signal.aborted, false);
  });

  for (const status of [404, 500]) {
    test(`${resource.method} propagates HTTP ${status} with the existing error message`, async () => {
      let parsed = false;
      const loader = createWorkspaceDetailDataLoader(async () => ({
        ok: false,
        status,
        json: async () => {
          parsed = true;
          return {};
        }
      }));
      await assert.rejects(loader[resource.method](request), { message: resource.message });
      assert.equal(parsed, false);
    });
  }

  for (const stage of ['fetch', 'json']) {
    test(`${resource.method} preserves the original ${stage} failure`, async () => {
      const failure = new Error(`${stage} failure`);
      const loader = createWorkspaceDetailDataLoader(async () => {
        if (stage === 'fetch') throw failure;
        return {
          ok: true,
          json: async () => {
            throw failure;
          }
        };
      });
      await assert.rejects(loader[resource.method](request), error => error === failure);
    });
  }

  test(`${resource.method} retains null-payload failures instead of inventing an empty success`, async () => {
    const { loader } = fixture(null);
    await assert.rejects(loader[resource.method](request), TypeError);
  });

  test(`${resource.method} forwards transport cancellation without owning the signal`, async () => {
    const controller = new AbortController();
    const reason = new Error('caller cancelled');
    controller.abort(reason);
    const loader = createWorkspaceDetailDataLoader(async (_url, { signal }) => {
      assert.equal(signal, controller.signal);
      throw signal.reason;
    });
    await assert.rejects(
      loader[resource.method]({ ...request, signal: controller.signal }),
      error => error === reason
    );
  });
}

for (const tasks of [undefined, null, false, 0, '', [], [{ id: 'task' }], {}, 'legacy truthy']) {
  test(`task projection preserves the existing fallback for ${JSON.stringify(tasks)}`, async () => {
    const { loader } = fixture({ tasks });
    assert.deepEqual(await loader.loadTasks(request), { tasks: tasks || [] });
  });
}

for (const items of [undefined, null, false, 0, '', {}, 'invalid', [], [{ id: 'backlog' }]]) {
  test(`backlog projection preserves array-only items for ${JSON.stringify(items)}`, async () => {
    const { loader } = fixture({ items });
    assert.deepEqual(await loader.loadBacklog(request), {
      items: Array.isArray(items) ? items : [],
      sync: null
    });
  });
}

for (const sync of [undefined, null, false, 0, '', {}, { state: 'synced' }]) {
  test(`backlog projection preserves sync metadata for ${JSON.stringify(sync)}`, async () => {
    const { loader } = fixture({ items: [], sync });
    assert.deepEqual(await loader.loadBacklog(request), { items: [], sync: sync || null });
  });
}

for (const includeDescendants of [undefined, false, true]) {
  test(`backlog descendant filter remains opt-in: ${includeDescendants}`, async () => {
    const { loader, calls } = fixture({});
    await loader.loadBacklog({ ...request, includeDescendants });
    assert.equal(
      calls[0].url,
      backlogURL + (includeDescendants ? '&include_descendants=true' : '')
    );
  });
}

for (const resource of resources) {
  test(`${resource.method} waits for JSON parsing before returning a snapshot`, async () => {
    let finish;
    const json = new Promise(resolve => (finish = resolve));
    let resolved = false;
    const loader = createWorkspaceDetailDataLoader(async () => ({ ok: true, json: () => json }));
    const read = loader[resource.method](request).then(snapshot => {
      resolved = true;
      return snapshot;
    });
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(resolved, false);
    finish({ tasks: [], items: [], sync: { state: 'done' } });
    assert.deepEqual(
      await read,
      resource.method === 'loadTasks' ? { tasks: [] } : { items: [], sync: { state: 'done' } }
    );
  });
}

test('one loader can serve concurrent workspaces and resources without sharing request state', async () => {
  const pending = [];
  const loader = createWorkspaceDetailDataLoader((url, options) => {
    let resolve;
    const promise = new Promise(yes => (resolve = yes));
    pending.push({ url, signal: options.signal, resolve });
    return promise;
  });
  const controllers = Array.from({ length: 3 }, () => new AbortController());
  const first = loader.loadTasks({ workspaceId: 'first', signal: controllers[0].signal });
  const second = loader.loadTasks({ workspaceId: 'second', signal: controllers[1].signal });
  const backlog = loader.loadBacklog({ workspaceId: 'third', signal: controllers[2].signal });
  controllers[0].abort();
  assert.deepEqual(
    pending.map(call => call.signal.aborted),
    [true, false, false]
  );
  pending[2].resolve(response({ items: [{ id: 'third' }] }));
  pending[1].resolve(response({ tasks: [{ id: 'second' }] }));
  pending[0].resolve(response({ tasks: [{ id: 'first' }] })); // Transport ignores abort.
  assert.deepEqual(await backlog, { items: [{ id: 'third' }], sync: null });
  assert.deepEqual(await second, { tasks: [{ id: 'second' }] });
  assert.deepEqual(await first, { tasks: [{ id: 'first' }] });
  assert.deepEqual(
    pending.map(call => call.url),
    [
      '/api/orchestration/tasks?workspace_id=first',
      '/api/orchestration/tasks?workspace_id=second',
      '/api/orchestration/backlog?workspace_id=third'
    ]
  );
});

test('the default fetch adapter resolves the current transport at call time', async t => {
  const loader = createWorkspaceDetailDataLoader();
  t.mock.method(globalThis, 'fetch', async () => response({ tasks: [{ id: 'current' }] }));
  assert.deepEqual(await loader.loadTasks(request), { tasks: [{ id: 'current' }] });
});

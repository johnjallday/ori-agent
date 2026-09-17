// Tests for quick-task.js — the page-independent create and start calls.
//
//   node --test internal/web/static/js/modules/quick-task.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { createTask, startTask, taskCreateBody } from './quick-task.js';

function withFetch(responses, run) {
  const calls = [];
  const previous = globalThis.fetch;
  globalThis.fetch = async (url, init) => {
    calls.push({ url, init });
    const next = responses.shift();
    return {
      ok: next.status >= 200 && next.status < 300,
      status: next.status,
      json: async () => next.body,
      text: async () => (typeof next.body === 'string' ? next.body : JSON.stringify(next.body))
    };
  };
  return Promise.resolve(run(calls)).finally(() => {
    globalThis.fetch = previous;
  });
}

test('a new task with no assignee leaves the choice to the server', () => {
  const body = taskCreateBody('ws-1', '  Compare the launch notes  ');
  assert.equal(body.workspace_id, 'ws-1');
  assert.equal(body.description, 'Compare the launch notes');
  assert.equal(body.status, 'pending');
  assert.equal(body.to, undefined, 'no assignee: the entry-agent default applies');
  assert.equal(body.required_capabilities, undefined);
});

test('createTask posts the task and returns what the server created', async () => {
  await withFetch([{ status: 201, body: { task: { id: 't1', to: 'Theo' } } }], async calls => {
    const task = await createTask('ws-1', 'Compare the launch notes');
    assert.equal(task.id, 't1');
    assert.equal(calls[0].url, '/api/orchestration/tasks');
    assert.equal(calls[0].init.method, 'POST');
    assert.equal(JSON.parse(calls[0].init.body).description, 'Compare the launch notes');
  });
});

test('createTask refuses an empty description without calling the server', async () => {
  await withFetch([], async calls => {
    await assert.rejects(() => createTask('ws-1', '   '), /Describe the task first/);
    assert.equal(calls.length, 0);
  });
});

test('createTask surfaces the server message', async () => {
  await withFetch(
    [{ status: 409, body: { code: 'price', message: 'Not enough Craft.' } }],
    async () => {
      await assert.rejects(() => createTask('ws-1', 'Build a Farm'), /Not enough Craft\./);
    }
  );
});

test('startTask posts to the execute endpoint and reports its refusal', async () => {
  await withFetch(
    [
      { status: 202, body: { success: true } },
      { status: 400, body: 'Task is already in progress' }
    ],
    async calls => {
      assert.equal(await startTask('t1'), true);
      assert.equal(calls[0].url, '/api/orchestration/tasks/execute');
      assert.deepEqual(JSON.parse(calls[0].init.body), { task_id: 't1' });
      await assert.rejects(() => startTask('t1'), /Task is already in progress/);
    }
  );
});

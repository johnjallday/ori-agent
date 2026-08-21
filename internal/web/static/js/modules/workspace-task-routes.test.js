import { test } from 'node:test';
import assert from 'node:assert/strict';

globalThis.window = globalThis.window || {};
const { WorkspaceTaskPage } = await import('./workspace-task.js');

test('workspace task keeps UUID APIs separate from slug page links', async () => {
  const page = new WorkspaceTaskPage('workspace-uuid', 'task-1', 'marketing-site');
  const previousFetch = globalThis.fetch;
  const requests = [];
  globalThis.fetch = async url => {
    requests.push(String(url));
    return { ok: true, json: async () => ({ id: 'workspace-uuid' }) };
  };

  try {
    await page.fetchWorkspace();
  } finally {
    globalThis.fetch = previousFetch;
  }

  assert.deepEqual(requests, ['/api/workspaces/workspace-uuid']);
  assert.equal(page.getTaskHref('task/2'), '/workspaces/marketing-site/task/task%2F2');
  assert.equal(page.getRunHref('run/2'), '/workspaces/marketing-site/runs/run%2F2');
});

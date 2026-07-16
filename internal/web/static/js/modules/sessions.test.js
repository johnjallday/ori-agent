import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./sessions.js', import.meta.url), 'utf8');

function loadSessionManager(fetchImpl = async () => ({ ok: true, json: async () => ({}) })) {
  const window = {};
  const document = { addEventListener() {} };
  vm.runInNewContext(source, { window, document, fetch: fetchImpl }, { filename: 'sessions.js' });
  return window.sessionManager;
}

test('workspace post-create action keeps the standard workspace destination by default', async () => {
  const manager = loadSessionManager();
  const result = await manager.applyWorkspacePostCreateAction('workspace 1');

  assert.equal(result.applied, false);
  assert.equal(result.destination, '/workspaces/workspace%201');
});

test('Personal HQ import designates the imported workspace and completes onboarding', async () => {
  const calls = [];
  const manager = loadSessionManager(async (url, options = {}) => {
    calls.push({ url, options });
    return { ok: true, json: async () => ({ success: true }) };
  });
  manager.workspacePostCreateAction = 'designate_personal_hq';

  const result = await manager.applyWorkspacePostCreateAction('hq-workspace');

  assert.equal(result.applied, true);
  assert.equal(result.destination, '/workspaces?view=map&focus=personal-hq');
  assert.equal(calls.length, 2);
  assert.equal(calls[0].url, '/api/personal-hq/replace');
  assert.deepEqual(JSON.parse(calls[0].options.body), { workspace_id: 'hq-workspace' });
  assert.equal(calls[1].url, '/api/personal-hq/onboarding-state');
  assert.deepEqual(JSON.parse(calls[1].options.body), { state: 'completed' });
});

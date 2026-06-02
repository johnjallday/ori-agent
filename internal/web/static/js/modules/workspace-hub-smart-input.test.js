import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

function loadSmartInput(overrides = {}) {
  const source = readFileSync(new URL('./workspace-hub-smart-input.js', import.meta.url), 'utf8');
  const window = {
    WorkspaceHubState: {
      getState: () => ({ selectedId: 'workspace-1', smartInputCancelled: false }),
      getElements: () => ({})
    },
    WorkspaceHubTasks: {
      loadTasks: overrides.loadTasks || (async () => {})
    },
    Toast: {
      error: overrides.toastError || (() => {}),
      info: () => {},
      warning: () => {}
    }
  };
  const context = {
    console,
    window,
    fetch: overrides.fetch || (async () => ({ ok: false, text: async () => 'not configured' })),
    confirm: overrides.confirm || (() => true),
    setTimeout,
    clearTimeout
  };
  vm.runInNewContext(source, context, { filename: 'workspace-hub-smart-input.js' });
  return window.WorkspaceHubSmartInput;
}

test('smart input auto-parse failure does not create fallback task', async () => {
  const calls = [];
  let loadCount = 0;
  const smartInput = loadSmartInput({
    fetch: async (url, options) => {
      calls.push({ url, body: options?.body ? JSON.parse(options.body) : null });
      return {
        ok: false,
        text: async () => 'Failed to parse task description: request timed out'
      };
    },
    loadTasks: async () => { loadCount += 1; }
  });

  await assert.rejects(
    () => smartInput.createTaskFromSmartInput('3 things to do in Vienna Austria'),
    (error) => error?.code === 'auto_parse_failed' && /No task was created/.test(error.message)
  );

  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, '/api/orchestration/tasks/auto-parse');
  assert.equal(loadCount, 0);
});

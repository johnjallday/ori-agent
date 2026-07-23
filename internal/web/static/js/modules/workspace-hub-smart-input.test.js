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
    WorkspaceHubModals: {
      showExecutionConfirm: overrides.showExecutionConfirm || (async () => true)
    },
    Toast: {
      error: overrides.toastError || (() => {}),
      success: overrides.toastSuccess || (() => {}),
      info: overrides.toastInfo || (() => {}),
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
    loadTasks: async () => {
      loadCount += 1;
    }
  });

  await assert.rejects(
    () => smartInput.createTaskFromSmartInput('3 things to do in Vienna Austria'),
    error => error?.code === 'auto_parse_failed' && /No task was created/.test(error.message)
  );

  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, '/api/orchestration/tasks/auto-parse');
  assert.equal(loadCount, 0);
});

test('createBacklogItemFromSmartInput posts to the backlog endpoint, not tasks (FR20-22)', async () => {
  const calls = [];
  const smartInput = loadSmartInput({
    fetch: async (url, options) => {
      calls.push({ url, body: options?.body ? JSON.parse(options.body) : null });
      return {
        ok: true,
        json: async () => ({
          item: { task: { id: 'b1', description: 'Explore competitor pricing' } }
        })
      };
    }
  });

  const result = await smartInput.createBacklogItemFromSmartInput('Explore competitor pricing');

  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, '/api/orchestration/backlog');
  assert.deepEqual(calls[0].body, {
    workspace_id: 'workspace-1',
    description: 'Explore competitor pricing',
    source_type: 'manual'
  });
  assert.equal(result.task.id, 'b1');
});

test('createBacklogItemFromSmartInput shows the confirmation before mutating and never calls fetch on cancel (FR23-25)', async () => {
  const calls = [];
  let confirmArgs = null;
  const smartInput = loadSmartInput({
    fetch: async url => {
      calls.push(url);
      return { ok: true, json: async () => ({ item: null }) };
    },
    showExecutionConfirm: async options => {
      confirmArgs = options;
      return false;
    }
  });

  const result = await smartInput.createBacklogItemFromSmartInput('Draft onboarding copy');

  assert.equal(result.cancelled, true);
  assert.equal(calls.length, 0, 'no mutation happens before the user confirms');
  assert.ok(confirmArgs, 'the confirmation modal was shown');
  assert.match(confirmArgs.confirmLabel, /Add to Backlog/);
  assert.ok(
    confirmArgs.details.some(d => d.includes('Draft onboarding copy')),
    'the proposed title is shown in the confirmation'
  );
});

test('createBacklogItemFromSmartInput strips the recognized backlog prefix from the title (regression: live testing found "backlog: X" saved literally)', async () => {
  const calls = [];
  const smartInput = loadSmartInput({
    fetch: async (url, options) => {
      calls.push({ url, body: options?.body ? JSON.parse(options.body) : null });
      return { ok: true, json: async () => ({ item: { task: { id: 'b1' } } }) };
    }
  });

  await smartInput.createBacklogItemFromSmartInput('backlog: explore competitor pricing');
  await smartInput.createBacklogItemFromSmartInput('idea: dark mode toggle');
  await smartInput.createBacklogItemFromSmartInput('someday redesign onboarding');
  await smartInput.createBacklogItemFromSmartInput('no prefix at all');

  assert.equal(calls[0].body.description, 'explore competitor pricing');
  assert.equal(calls[1].body.description, 'dark mode toggle');
  assert.equal(calls[2].body.description, 'redesign onboarding');
  assert.equal(calls[3].body.description, 'no prefix at all');
});

test('handleDecision("backlog") routes to the backlog endpoint and never calls task auto-parse', async () => {
  const calls = [];
  const smartInput = loadSmartInput({
    fetch: async url => {
      calls.push(url);
      if (url === '/api/orchestration/backlog') {
        return { ok: true, json: async () => ({ item: { task: { id: 'b1' } } }) };
      }
      return { ok: false, text: async () => 'unexpected call' };
    }
  });

  await smartInput.handleDecision('backlog', {
    input: 'Explore new markets',
    decision: 'backlog',
    confidence: 0.9,
    method: 'heuristic'
  });

  assert.deepEqual(calls, ['/api/orchestration/backlog']);
});

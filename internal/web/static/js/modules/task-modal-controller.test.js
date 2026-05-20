import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

function loadController(overrides = {}) {
  const source = readFileSync(new URL('./task-modal-controller.js', import.meta.url), 'utf8');
  const elements = new Map();
  const document = {
    getElementById(id) {
      return elements.get(id) || null;
    },
    querySelector() {
      return null;
    },
    querySelectorAll() {
      return [];
    },
    addEventListener() {},
  };
  const window = {};
  const context = {
    console,
    document,
    window,
    setTimeout,
    clearTimeout,
    fetch: overrides.fetch || (async () => ({ ok: false, text: async () => 'not configured' })),
    confirm: overrides.confirm || (() => true),
  };
  vm.runInNewContext(source, context, { filename: 'task-modal-controller.js' });
  return { Controller: window.TaskModalController, elements };
}

test('output contract save gating rejects empty and duplicate columns', () => {
  const { Controller } = loadController();
  const controller = new Controller();
  controller.getOutputContractRows = () => [{ name: '', type: 'string', required: true }];

  assert.equal(controller.getOutputContractData().error, 'Each output contract column needs a name.');

  controller.getOutputContractRows = () => [
    { name: 'date', type: 'date', required: true },
    { name: 'DATE', type: 'string', required: false },
  ];
  assert.equal(controller.getOutputContractData().error, 'Duplicate output contract column: DATE');
});

test('output contract normalization deduplicates and preserves usable columns', () => {
  const { Controller } = loadController();
  const controller = new Controller();

  const normalized = controller.normalizeOutputContractPayload({
    source: 'ai_suggested',
    columns: [
      { name: ' date ', type: 'date', required: true, description: 'Run date' },
      { name: 'DATE', type: 'number', required: true },
      { name: 'pollen_count', type: 'number', required: true },
      { name: 'ignored' },
    ],
  });

  assert.equal(normalized.source, 'ai_suggested');
  assert.equal(JSON.stringify(normalized.columns.map((column) => column.name)), JSON.stringify(['date', 'pollen_count', 'ignored']));
  assert.equal(normalized.columns[0].description, 'Run date');
  assert.equal(normalized.columns[2].type, 'string');
});

test('output contract suggestion cache key is stable for equivalent drafts', () => {
  const { Controller } = loadController();
  const controller = new Controller();

  const draft = {
    title: 'Daily pollen',
    details: 'NYC',
    workspace_id: 'workspace-1',
    schedule_enabled: true,
    schedule_name: 'Daily',
    schedule: { type: 'daily', time: '09:00' },
    result_storage: { enabled: true, format: 'csv', write_mode: 'append' },
  };

  const first = controller.getOutputContractSuggestionCacheKey(draft);
  const second = controller.getOutputContractSuggestionCacheKey({ ...draft, workspace_id: 'workspace-2' });
  assert.equal(first, second);
});

test('output contract heuristic fallback covers common recurring tasks', () => {
  const { Controller, elements } = loadController();
  const controller = new Controller();
  elements.set('taskModalDescription', { value: 'Check pollen count in NYC daily' });
  elements.set('taskModalDetails', { value: '' });

  const columns = controller.suggestOutputContractColumns();
  assert.equal(JSON.stringify(columns.map((column) => column.name)), JSON.stringify(['date', 'location', 'pollen_count', 'category', 'source']));
});

test('output contract manual edits emit telemetry only once per draft', () => {
  const calls = [];
  const { Controller } = loadController({
    fetch: async (url, options) => {
      calls.push({ url, body: JSON.parse(options.body) });
      return { ok: true, json: async () => ({ success: true }) };
    },
  });
  const controller = new Controller();
  controller.workspaceId = 'workspace-1';
  controller.editingTaskId = 'task-1';
  controller.getOutputContractRows = () => [{ name: 'date' }, { name: 'pollen_count' }];

  controller.markOutputContractEdited();
  controller.markOutputContractEdited();

  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, '/api/orchestration/tasks/output-contract/telemetry');
  assert.equal(calls[0].body.action, 'suggestion_edited');
  assert.equal(calls[0].body.column_count, 2);
});

test('output contract regenerate emits telemetry and requests forced suggestion', async () => {
  const calls = [];
  const { Controller } = loadController({
    fetch: async (url, options) => {
      calls.push({ url, body: JSON.parse(options.body) });
      return { ok: true, json: async () => ({ success: true }) };
    },
  });
  const controller = new Controller();
  controller.workspaceId = 'workspace-1';
  controller.outputContractSource = 'manual';
  controller.getOutputContractRows = () => [{ name: 'date', description: '' }];
  let forceRequested = false;
  controller.ensureOutputContractSuggestion = async ({ force } = {}) => {
    forceRequested = Boolean(force);
  };

  await controller.regenerateOutputContractSuggestion();

  assert.equal(forceRequested, true);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].body.action, 'suggestion_regenerated');
  assert.equal(calls[0].body.column_count, 1);
});

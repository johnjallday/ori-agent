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
    console: overrides.console || console,
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

test('output contract data includes structured output spec payload', () => {
  const { Controller } = loadController();
  const controller = new Controller();
  controller.getOutputContractRows = () => [
    { name: 'date', type: 'date', required: true, description: 'Run date' },
    { name: 'pollen_count', type: 'number', required: true, description: 'Reported pollen level' },
  ];

  const data = controller.getOutputContractData();

  assert.equal(data.output_contract.columns.length, 2);
  assert.equal(data.output_spec.schema.fields[0].name, 'date');
  assert.equal(data.output_spec.contract.columns[1].name, 'pollen_count');
  assert.equal(data.output_spec.mappings[1].schema_field, 'pollen_count');
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

test('modal state snapshot includes reference URL field', () => {
  const { Controller, elements } = loadController();
  const controller = new Controller();
  elements.set('taskModalReferenceURL', { value: 'https://example.com/spec' });
  elements.set('taskAutoReferenceURL', { value: 'https://example.com/auto-spec' });

  const snapshot = JSON.parse(controller.getModalStateSnapshot());

  assert.equal(snapshot.manual.referenceURL, 'https://example.com/spec');
  assert.equal(snapshot.auto.referenceURL, 'https://example.com/auto-spec');
});

test('auto mode task creation includes reference URL payload', async () => {
  const calls = [];
  const { Controller, elements } = loadController({
    fetch: async (url, options) => {
      const body = options?.body ? JSON.parse(options.body) : null;
      calls.push({ url, body });
      if (url === '/api/orchestration/tasks/auto-parse') {
        return {
          ok: true,
          json: async () => ({
            title: 'Parsed task',
            details: 'Parsed details',
            priority: 2
          }),
          text: async () => ''
        };
      }
      return {
        ok: true,
        json: async () => ({ task: { id: 'task-1', description: body.description } }),
        text: async () => ''
      };
    }
  });
  const controller = new Controller();
  controller.workspaceId = 'workspace-1';
  controller.autoMode = true;
  controller.showProgress = () => {};
  controller.updateProgress = () => {};
  controller.hideProgress = () => {};
  controller.showToast = () => {};
  controller.finalizeSuccessfulSave = async () => {};
  controller._safeAttachUploads = async () => {};
  controller._reportUploadFailures = () => {};
  elements.set('taskAutoDescription', { value: 'Create a task from this page', focus() {} });
  elements.set('taskAutoReferenceURL', { value: 'https://example.com/spec' });
  elements.set('taskModalReferenceURL', { value: '' });

  await controller.saveAutoMode();

  assert.equal(calls.length, 2);
  assert.equal(calls[1].url, '/api/orchestration/tasks');
  assert.equal(calls[1].body.reference_url, 'https://example.com/spec');
  assert.equal(calls[1].body.description, 'Parsed task');
});

test('auto mode parse failure shows inline error and does not create task', async () => {
  const calls = [];
  const toasts = [];
  const attributes = new Map();
  let focused = false;
  const { Controller, elements } = loadController({
    console: { ...console, error: () => {} },
    fetch: async (url, options) => {
      const body = options?.body ? JSON.parse(options.body) : null;
      calls.push({ url, body });
      return {
        ok: false,
        text: async () => 'Failed to parse task description: request timed out - the AI took too long to respond.'
      };
    }
  });
  const controller = new Controller();
  controller.workspaceId = 'workspace-1';
  controller.autoMode = true;
  controller.showProgress = () => {};
  controller.updateProgress = () => {};
  controller.hideProgress = () => {};
  controller.showToast = (message, type) => toasts.push({ message, type });
  elements.set('taskAutoDescription', {
    value: '3 things to do in Vienna Austria',
    focus() { focused = true; },
    setAttribute(name, value) { attributes.set(name, value); },
    removeAttribute(name) { attributes.delete(name); }
  });
  elements.set('taskAutoReferenceURL', { value: '' });
  elements.set('taskModalReferenceURL', { value: '' });
  const errorBox = { hidden: true };
  const errorMessage = { textContent: '' };
  elements.set('taskAutoParseError', errorBox);
  elements.set('taskAutoParseErrorMessage', errorMessage);

  await controller.saveAutoMode();

  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, '/api/orchestration/tasks/auto-parse');
  assert.equal(errorBox.hidden, false);
  assert.match(errorMessage.textContent, /No task was created/);
  assert.equal(attributes.get('aria-invalid'), 'true');
  assert.equal(focused, true);
  assert.deepEqual(toasts.at(-1), {
    message: 'Auto parsing did not work. No task was created.',
    type: 'error'
  });
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

test('workflow auto-save payload routes output contract to final step only', () => {
  const { Controller } = loadController();
  const controller = new Controller();
  const autoSaveData = {
    result_storage: { enabled: true, format: 'csv', write_mode: 'append' },
    output_contract: {
      columns: [
        { name: 'date', type: 'date', required: true },
        { name: 'pollen_count', type: 'number', required: true },
      ],
    },
  };

  assert.equal(JSON.stringify(controller.getWorkflowAutoSavePayload(autoSaveData, 0, 2, true)), JSON.stringify({
    result_storage: null,
    output_contract: { columns: [] },
  }));
  assert.equal(JSON.stringify(controller.getWorkflowAutoSavePayload(autoSaveData, 1, 2, true)), JSON.stringify(autoSaveData));
});

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
    createElement(tagName) {
      return { tagName, value: '', textContent: '' };
    },
    addEventListener() {}
  };
  const window = {};
  const context = {
    console: overrides.console || console,
    document,
    window,
    setTimeout,
    clearTimeout,
    fetch: overrides.fetch || (async () => ({ ok: false, text: async () => 'not configured' })),
    confirm: overrides.confirm || (() => true)
  };
  vm.runInNewContext(source, context, { filename: 'task-modal-controller.js' });
  return { Controller: window.TaskModalController, elements };
}

function makeElement(overrides = {}) {
  return {
    hidden: false,
    textContent: '',
    dataset: {},
    focus() {},
    addEventListener() {},
    append() {},
    appendChild() {},
    setAttribute() {},
    removeAttribute() {},
    ...overrides
  };
}

test('output contract save gating rejects empty and duplicate columns', () => {
  const { Controller } = loadController();
  const controller = new Controller();
  controller.getOutputContractRows = () => [{ name: '', type: 'string', required: true }];

  assert.equal(
    controller.getOutputContractData().error,
    'Each output contract column needs a name.'
  );

  controller.getOutputContractRows = () => [
    { name: 'date', type: 'date', required: true },
    { name: 'DATE', type: 'string', required: false }
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
      { name: 'ignored' }
    ]
  });

  assert.equal(normalized.source, 'ai_suggested');
  assert.equal(
    JSON.stringify(normalized.columns.map(column => column.name)),
    JSON.stringify(['date', 'pollen_count', 'ignored'])
  );
  assert.equal(normalized.columns[0].description, 'Run date');
  assert.equal(normalized.columns[2].type, 'string');
});

test('output contract data includes structured output spec payload', () => {
  const { Controller } = loadController();
  const controller = new Controller();
  controller.getOutputContractRows = () => [
    { name: 'date', type: 'date', required: true, description: 'Run date' },
    { name: 'pollen_count', type: 'number', required: true, description: 'Reported pollen level' }
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
    result_storage: { enabled: true, format: 'csv', write_mode: 'append' }
  };

  const first = controller.getOutputContractSuggestionCacheKey(draft);
  const second = controller.getOutputContractSuggestionCacheKey({
    ...draft,
    workspace_id: 'workspace-2'
  });
  assert.equal(first, second);
});

test('output contract heuristic fallback covers common recurring tasks', () => {
  const { Controller, elements } = loadController();
  const controller = new Controller();
  elements.set('taskModalDescription', { value: 'Check pollen count in NYC daily' });
  elements.set('taskModalDetails', { value: '' });

  const columns = controller.suggestOutputContractColumns();
  assert.equal(
    JSON.stringify(columns.map(column => column.name)),
    JSON.stringify(['date', 'location', 'pollen_count', 'category', 'source'])
  );
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

test('auto mode forwards coordinator assignment provenance to created task', async () => {
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
            details: '',
            priority: 3,
            agent_name: 'Manager',
            assignment_mode: 'static_plan',
            assigned_by: 'Manager',
            assignment_reason: 'coordinator plan'
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
  controller.confirmAutoParsedTaskCreation = async () => true;
  controller.finalizeSuccessfulSave = async () => {};
  controller._safeAttachUploads = async () => {};
  controller._reportUploadFailures = () => {};
  elements.set('taskAutoDescription', { value: 'do something', focus() {} });
  elements.set('taskAutoReferenceURL', { value: '' });
  elements.set('taskModalReferenceURL', { value: '' });

  await controller.saveAutoMode();

  assert.equal(calls[1].url, '/api/orchestration/tasks');
  assert.equal(calls[1].body.to, 'Manager');
  assert.equal(calls[1].body.assignment_mode, 'static_plan');
  assert.equal(calls[1].body.assigned_by, 'Manager');
  assert.equal(calls[1].body.assignment_reason, 'coordinator plan');
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
        text: async () =>
          'Failed to parse task description: request timed out - the AI took too long to respond.'
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
    focus() {
      focused = true;
    },
    setAttribute(name, value) {
      attributes.set(name, value);
    },
    removeAttribute(name) {
      attributes.delete(name);
    }
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

test('auto mode asks for confirmation before creating an unassigned parsed task', async () => {
  const calls = [];
  const toasts = [];
  let confirmText = '';
  const { Controller, elements } = loadController({
    confirm: message => {
      confirmText = message;
      return false;
    },
    fetch: async (url, options) => {
      const body = options?.body ? JSON.parse(options.body) : null;
      calls.push({ url, body });
      return {
        ok: true,
        json: async () => ({
          title: 'Three Vienna foods',
          details: 'Find or suggest three things to eat in Vienna.',
          agent_name: '',
          priority: 3
        }),
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
  controller.showToast = (message, type) => toasts.push({ message, type });
  elements.set('taskAutoDescription', { value: '3 things to eat in Vienna Austria', focus() {} });
  elements.set('taskAutoReferenceURL', { value: '' });
  elements.set('taskModalReferenceURL', { value: '' });

  await controller.saveAutoMode();

  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, '/api/orchestration/tasks/auto-parse');
  assert.match(confirmText, /Assignment: Unassigned/);
  assert.deepEqual(toasts.at(-1), {
    message: 'Task creation cancelled',
    type: 'info'
  });
});

test('auto mode uses in-modal confirmation and shows assigned agent', async () => {
  const calls = [];
  let nativeConfirmCalled = false;
  const { Controller, elements } = loadController({
    confirm: () => {
      nativeConfirmCalled = true;
      return true;
    },
    fetch: async (url, options) => {
      const body = options?.body ? JSON.parse(options.body) : null;
      calls.push({ url, body });
      return {
        ok: true,
        json: async () => ({
          title: 'Parsed task',
          details: 'Parsed details',
          agent_name: 'Ori',
          priority: 2
        }),
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
  elements.set('taskAutoReferenceURL', { value: '' });
  elements.set('taskModalReferenceURL', { value: '' });
  elements.set('taskAutoConfirm', makeElement({ hidden: true }));
  elements.set('taskAutoConfirmEyebrow', makeElement());
  elements.set('taskAutoConfirmTitle', makeElement());
  elements.set('taskAutoConfirmMessage', makeElement());
  elements.set('taskAutoConfirmParsedTitle', makeElement());
  elements.set('taskAutoConfirmAssignment', makeElement());
  elements.set('taskAutoConfirmDetailsRow', makeElement());
  elements.set('taskAutoConfirmDetails', makeElement());
  elements.set('taskAutoConfirmScheduleRow', makeElement());
  elements.set('taskAutoConfirmSchedule', makeElement());
  elements.set('taskAutoConfirmStorageRow', makeElement());
  elements.set('taskAutoConfirmStorage', makeElement());
  elements.set('taskAutoConfirmSteps', makeElement());
  elements.set('taskAutoConfirmCreate', makeElement());
  elements.set('taskAutoConfirmCancel', makeElement());

  const savePromise = controller.saveAutoMode();
  await new Promise(resolve => setTimeout(resolve, 0));

  assert.equal(elements.get('taskAutoConfirm').hidden, false);
  assert.equal(elements.get('taskAutoConfirmAssignment').textContent, 'Ori');
  assert.equal(elements.get('taskAutoConfirmAssignment').dataset.state, 'assigned');
  assert.equal(nativeConfirmCalled, false);

  controller.resolveAutoParseConfirm(true);
  await savePromise;

  assert.equal(calls.length, 2);
  assert.equal(calls[1].url, '/api/orchestration/tasks');
  assert.equal(calls[1].body.to, 'Ori');
});

test('output contract manual edits emit telemetry only once per draft', () => {
  const calls = [];
  const { Controller } = loadController({
    fetch: async (url, options) => {
      calls.push({ url, body: JSON.parse(options.body) });
      return { ok: true, json: async () => ({ success: true }) };
    }
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
    }
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
        { name: 'pollen_count', type: 'number', required: true }
      ]
    }
  };

  assert.equal(
    JSON.stringify(controller.getWorkflowAutoSavePayload(autoSaveData, 0, 2, true)),
    JSON.stringify({
      result_storage: null,
      output_contract: { columns: [] }
    })
  );
  assert.equal(
    JSON.stringify(controller.getWorkflowAutoSavePayload(autoSaveData, 1, 2, true)),
    JSON.stringify(autoSaveData)
  );
});

test('a named agent instance is added to the assignment list instead of becoming no agent', () => {
  const { Controller, elements } = loadController();
  const controller = new Controller();
  const added = [];
  const select = {
    options: [
      { value: '', textContent: '-- No agent (manual task) --' },
      { value: 'node:Mixer-node-1', textContent: 'Mixer' }
    ],
    appendChild(option) {
      added.push(option);
      this.options.push(option);
    }
  };
  elements.set('taskModalAssignment', select);

  // An instance the app-wide list already offers is left alone.
  assert.equal(controller.ensureAssignmentOption('node:Mixer-node-1', 'Mixer'), true);
  assert.equal(added.length, 0);
  // An instance with another node is added with its own label.
  assert.equal(controller.ensureAssignmentOption('node:Mixer-node-2', 'Mixer'), true);
  assert.equal(added.length, 1);
  assert.equal(added[0].value, 'node:Mixer-node-2');
  assert.equal(added[0].textContent, 'Mixer');
  // Asking again adds nothing; an empty value is never an option.
  assert.equal(controller.ensureAssignmentOption('node:Mixer-node-2', 'Mixer'), true);
  assert.equal(added.length, 1);
  assert.equal(controller.ensureAssignmentOption('', 'Nobody'), false);
  // The form still reads the agent's name back from the node on save.
  assert.equal(controller.parseAssignmentValue('node:Mixer-node-2').to, 'Mixer');
  assert.equal(controller.parseAssignmentValue('node:Mixer-node-2').assignedNodeId, 'Mixer-node-2');
});

test('the save callback receives what was saved, and callers that ignore it still work', async () => {
  const { Controller } = loadController();
  const controller = new Controller();
  // Closing the form touches the page; this test is about the callback.
  controller.close = () => true;
  const received = [];
  controller.onSaveCallback = async payload => {
    received.push(payload);
  };
  controller.workspaceId = 'ws-1';
  await controller.finalizeSuccessfulSave('task:created', { task: { id: 'task-9', to: 'Mixer' } });
  assert.equal(received.length, 1);
  assert.equal(received[0].task.id, 'task-9');

  let called = 0;
  controller.onSaveCallback = () => {
    called += 1;
  };
  await controller.finalizeSuccessfulSave('task:created');
  assert.equal(called, 1);
});

// ---- saving a draft the personal assistant prepared ----

/** A manual-mode form, ready to save one plain task. */
function loadSavingForm({ respond, schedule = { schedule_enabled: false }, inputs = [] } = {}) {
  const calls = [];
  const toasts = [];
  const { Controller, elements } = loadController({
    console: { ...console, error() {}, warn() {} },
    fetch: async (url, options) => {
      const body = options?.body ? JSON.parse(options.body) : null;
      calls.push({ url, method: options?.method, body });
      const reply = respond(body, calls.length);
      return {
        ok: reply.status < 400,
        status: reply.status,
        json: async () => reply.body,
        text: async () => JSON.stringify(reply.body)
      };
    }
  });
  const controller = new Controller();
  controller.workspaceId = 'ws-album';
  controller.showToast = (message, kind) => toasts.push({ message, kind });
  controller.getReferenceURLValue = () => '';
  controller.getSelectedInputRefs = () => inputs;
  controller.resolveInputRefs = refs => refs;
  controller.collectSubtasks = () => ({ subtasks: [], invalidInput: null });
  controller.getScheduleData = () => schedule;
  controller.getAutoSaveData = () => ({});
  controller._economyRefusal = async () => null;
  controller.announceEconomyCharge = () => {};
  controller._reportUploadFailures = () => {};
  controller._safeAttachUploads = async () => {};
  controller._clearSubtaskGraphErrors = () => {};
  const saved = [];
  controller.finalizeSuccessfulSave = async (_event, payload) => saved.push(payload);
  elements.set('taskModalDescription', { value: 'Bounce the stems at 48k', focus() {} });
  elements.set('taskModalDetails', { value: 'Export stems for mastering.' });
  elements.set('taskModalPriority', { value: '3' });
  elements.set('taskModalAssignment', { value: 'node:Engineer-node-1' });
  return { controller, calls, toasts, saved };
}

test('a prepared draft is saved once by the form, naming its operation', async () => {
  const created = {
    id: 'task-9',
    to: 'Engineer',
    description: 'Bounce the stems at 48k',
    assistant_operation_id: 'op-task'
  };
  const { controller, calls, saved } = loadSavingForm({
    respond: () => ({ status: 201, body: { success: true, task: created } })
  });
  controller.assistantOperation = controller.normalizeAssistantOperation({
    id: 'op-task',
    revision: 2
  });
  await controller.save();

  assert.equal(calls.length, 1, 'the form is the only create owner');
  assert.equal(calls[0].url, '/api/orchestration/tasks');
  assert.equal(calls[0].method, 'POST');
  // The human's edits are what is sent, with the operation and its revision.
  assert.equal(calls[0].body.description, 'Bounce the stems at 48k');
  assert.equal(calls[0].body.to, 'Engineer');
  assert.equal(calls[0].body.assigned_node_id, 'Engineer-node-1');
  assert.equal(calls[0].body.assistant_operation_id, 'op-task');
  assert.equal(calls[0].body.assistant_draft_revision, 2);
  // The save receipt is the server's own record of the task.
  assert.equal(saved.length, 1);
  assert.equal(saved[0].task.id, 'task-9');
  assert.equal(saved[0].task.assistant_operation_id, 'op-task');
});

test('an ordinary save, a scheduled one and one with inputs name no operation', async () => {
  const respond = body => ({ status: 201, body: { task: { id: 'task-1', ...body } } });
  const ordinary = loadSavingForm({ respond });
  await ordinary.controller.save();
  assert.equal('assistant_operation_id' in ordinary.calls[0].body, false);
  assert.equal('assistant_draft_revision' in ordinary.calls[0].body, false);

  for (const options of [
    { schedule: { schedule_enabled: true, schedule: { type: 'daily', time: '09:00' } } },
    { schedule: { schedule_enabled: false, schedule: { type: 'daily', time: '09:00' } } },
    { inputs: ['other-task'] }
  ]) {
    const form = loadSavingForm({ respond, ...options });
    form.controller.assistantOperation = { id: 'op-task', revision: 1 };
    await form.controller.save();
    assert.equal(form.calls.length, 1);
    assert.equal(
      'assistant_operation_id' in form.calls[0].body,
      false,
      "more than one plain task is the user's own task"
    );
  }
});

test('a malformed operation reference is not a reference', () => {
  const { Controller } = loadController();
  const controller = new Controller();
  for (const value of [
    null,
    {},
    { id: 'op-task' },
    { id: '', revision: 1 },
    { id: 'op-task', revision: 0 },
    { id: 'op-task', revision: 1.5 },
    { id: 'op-task', revision: 'x' }
  ]) {
    assert.equal(controller.normalizeAssistantOperation(value), null);
  }
  const normalized = controller.normalizeAssistantOperation({ id: ' op-task ', revision: 3 });
  assert.equal(normalized.id, 'op-task');
  assert.equal(normalized.revision, 3);
});

test('a refused assignee keeps the form bound so the corrected save is still the reviewed one', async () => {
  const { controller, calls, toasts, saved } = loadSavingForm({
    respond: (_body, n) =>
      n === 1
        ? {
            status: 409,
            body: {
              code: 'operation_assignee',
              message:
                'That agent is not in this workspace. Choose an agent from this workspace, then save again.'
            }
          }
        : { status: 201, body: { task: { id: 'task-9', assistant_operation_id: 'op-task' } } }
  });
  controller.assistantOperation = { id: 'op-task', revision: 1 };
  await controller.save();
  assert.equal(saved.length, 0, 'nothing was created');
  assert.match(toasts[0].message, /not in this workspace/);
  assert.doesNotMatch(toasts[0].message, /your own task/);
  assert.equal(controller.assistantOperation.id, 'op-task');

  await controller.save();
  assert.equal(calls[1].body.assistant_operation_id, 'op-task');
  assert.equal(saved.length, 1);
});

test('an ended request is said plainly, and only the next Save makes an ordinary task', async () => {
  for (const code of [
    'operation_expired',
    'operation_invalidated',
    'operation_not_found',
    'operation_ended',
    'operation_conflict',
    'operation_invalid',
    'operation_not_plain'
  ]) {
    const { controller, calls, toasts, saved } = loadSavingForm({
      respond: (body, n) =>
        n === 1
          ? {
              status: 410,
              body: { code, message: "The assistant's request ran out of time. Nothing was saved." }
            }
          : { status: 201, body: { task: { id: 'task-1', description: body.description } } }
    });
    controller.assistantOperation = { id: 'op-task', revision: 1 };
    await controller.save();
    // The refused save created nothing and was not retried by itself.
    assert.equal(calls.length, 1, code);
    assert.equal(saved.length, 0, code);
    assert.equal(controller.assistantOperation, null, code);
    assert.equal(toasts.length, 1, code);
    assert.equal(toasts[0].kind, 'error');
    assert.match(
      toasts[0].message,
      /Nothing was saved\. Save again to create this as your own task\./
    );

    // The user's own second click is an ordinary create.
    await controller.save();
    assert.equal(calls.length, 2, code);
    assert.equal('assistant_operation_id' in calls[1].body, false, code);
    assert.equal(saved.length, 1, code);
  }
});

test('a transient operation error keeps the binding, and other errors are untouched', async () => {
  const { Controller } = loadController();
  const controller = new Controller();
  controller.assistantOperation = { id: 'op-task', revision: 1 };
  for (const code of ['operation_unavailable', 'operation_create_in_flight']) {
    const message = controller.explainAssistantOperationRefusal({ code, message: 'Try again.' });
    assert.equal(message, 'Try again.');
    assert.equal(controller.assistantOperation.id, 'op-task');
  }
  assert.equal(
    controller.explainAssistantOperationRefusal(new Error('description is required')),
    ''
  );
  assert.equal(controller.explainAssistantOperationRefusal({ code: 'bad_request' }), '');
  assert.equal(controller.assistantOperation.id, 'op-task');

  // The server's code survives parsing, so the form can tell these apart.
  const parsed = await controller._parseTaskApiError(
    {
      text: async () => JSON.stringify({ code: 'operation_expired', message: 'Ran out of time.' })
    },
    'Failed to create task'
  );
  assert.equal(parsed.code, 'operation_expired');
  assert.equal(parsed.message, 'Ran out of time.');
});

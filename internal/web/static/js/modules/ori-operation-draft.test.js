import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createOperationTargets, OUTCOME } from './ori-operation-targets.js';
import { OperationClient, PHASE, TAB_KEY, OPERATION_KEY } from './ori-operation-client.js';
import { operationView } from './personal-assistant-operations.js';

// Preparing a task: the real task form is opened with the draft, the form is
// checked to really hold it, and the user's own edits always win.

const draft = {
  revision: 1,
  title: 'Bounce the stems',
  brief: 'Export stems for mastering.',
  agent_name: 'Engineer',
  agent_node_id: 'Engineer-node-1'
};
const snapshotOf = (overrides = {}) => ({
  id: 'op-task',
  kind: 'task',
  status: 'prepared',
  revision: 1,
  watching: true,
  pending: 2,
  context: {
    workspace_id: 'ws-album',
    workspace_slug: 'album-1',
    workspace_name: 'Album 1',
    agent_name: 'Engineer',
    agent_node_id: 'Engineer-node-1'
  },
  draft: { ...draft },
  commands: [
    {
      id: 'c1',
      sequence: 1,
      action: 'open_workspace',
      status: 'pending',
      workspace_id: 'ws-album',
      workspace_slug: 'album-1'
    },
    {
      id: 'c2',
      sequence: 2,
      action: 'open_task_draft',
      status: 'pending',
      workspace_id: 'ws-album',
      draft_revision: 1
    }
  ],
  ...overrides
});
const formCommand = { action: 'open_task_draft', workspace_id: 'ws-album', draft_revision: 1 };

/** A workspace page with a task form that behaves like the real controller. */
function makePage(overrides = {}) {
  const state = {
    workspaceId: 'ws-album',
    open: false,
    dirty: false,
    form: { title: '', brief: '', assignment: '' },
    options: ['', 'node:Engineer-node-1'],
    opens: [],
    closes: 0,
    ...overrides
  };
  const composer = {
    isModalOpen: () => state.open,
    hasUnsavedChanges: () => state.open && state.dirty,
    async openForCreate(workspaceId, title, onSave, options) {
      state.opens.push({ workspaceId, title, options });
      state.onSave = onSave;
      if (state.failsToOpen) return;
      if (options.draftAssignmentLabel && !state.rejectsAgent)
        state.options.push(options.draftAssignmentValue);
      state.open = true;
      state.dirty = false;
      state.form = {
        title: state.dropsText ? '' : options.prefillTitle,
        brief: state.dropsText ? '' : options.prefillDetails,
        // A select that lacks the option silently becomes "no agent".
        assignment: state.options.includes(options.draftAssignmentValue)
          ? options.draftAssignmentValue
          : ''
      };
    },
    close() {
      state.closes += 1;
      state.open = false;
    }
  };
  const env = {
    pathname: () => '/workspaces/album-1',
    workspacePage: () => ({
      workspaceId: state.workspaceId,
      command: null,
      composer: state.noForm ? null : composer
    }),
    agentControl: () => null,
    taskForm: () => ({ open: state.open, ...state.form }),
    hasUnsavedWork: () => state.open && state.dirty,
    wait: async () => {}
  };
  return { env, state, targets: createOperationTargets(env) };
}

test('the draft is handed to the real form in manual mode and verified there', async () => {
  const { targets, state } = makePage();
  const onSaved = () => {};
  const result = await targets.execute(formCommand, { snapshot: snapshotOf(), onSaved });
  assert.deepEqual(result, { outcome: OUTCOME.ACKNOWLEDGED, observed: { draft_revision: 1 } });
  assert.equal(state.opens.length, 1);
  const { workspaceId, title, options } = state.opens[0];
  assert.equal(workspaceId, 'ws-album');
  assert.equal(title, 'Bounce the stems');
  assert.deepEqual(options, {
    forceManualMode: true, // never auto-parsed into something else
    prefillTitle: 'Bounce the stems',
    prefillDetails: 'Export stems for mastering.',
    draftAssignmentValue: 'node:Engineer-node-1',
    draftAssignmentLabel: 'Engineer',
    modalTitle: 'Review task for Engineer'
  });
  assert.equal(state.onSave, onSaved, 'the form keeps its one save owner');
  assert.deepEqual(state.form, {
    title: 'Bounce the stems',
    brief: 'Export stems for mastering.',
    assignment: 'node:Engineer-node-1'
  });
});

test('an agent the form cannot assign is reported, never left as no agent', async () => {
  const { targets, state } = makePage({ options: [''], rejectsAgent: true });
  const result = await targets.execute(formCommand, { snapshot: snapshotOf() });
  assert.equal(result.outcome, OUTCOME.UNAVAILABLE);
  assert.match(result.reason, /Engineer cannot be assigned/);
  // The form is closed: a save from it would go to someone the user was not shown.
  assert.equal(state.closes, 1);
  assert.equal(state.open, false);
});

test('a form that did not open, or did not take the text, is not reported as prepared', async () => {
  const closed = makePage({ failsToOpen: true });
  assert.equal(
    (await closed.targets.execute(formCommand, { snapshot: snapshotOf() })).outcome,
    OUTCOME.UNAVAILABLE
  );
  const empty = makePage({ dropsText: true });
  const result = await empty.targets.execute(formCommand, { snapshot: snapshotOf() });
  assert.equal(result.outcome, OUTCOME.UNAVAILABLE);
  assert.match(result.reason, /did not take the draft/);
  assert.equal(empty.state.open, false);
  const noForm = makePage({ noForm: true });
  assert.equal(
    (await noForm.targets.execute(formCommand, { snapshot: snapshotOf() })).outcome,
    OUTCOME.UNAVAILABLE
  );
});

test('a late draft never overwrites what the user typed unless they choose it', async () => {
  const { targets, state } = makePage({
    open: true,
    dirty: true,
    form: { title: 'My own task', brief: '', assignment: '' }
  });
  assert.deepEqual(await targets.execute(formCommand, { snapshot: snapshotOf() }), {
    outcome: OUTCOME.YIELD,
    reason: 'unsaved_work'
  });
  assert.equal(state.opens.length, 0);
  assert.equal(state.form.title, 'My own task');
  // Replacing is the user's explicit choice.
  const replaced = await targets.execute(formCommand, { snapshot: snapshotOf(), replace: true });
  assert.equal(replaced.outcome, OUTCOME.ACKNOWLEDGED);
  assert.equal(state.form.title, 'Bounce the stems');
});

test('an older revision, another workspace or a draft without an agent opens nothing', async () => {
  const stale = makePage();
  const old = await stale.targets.execute(
    { ...formCommand, draft_revision: 1 },
    { snapshot: snapshotOf({ draft: { ...draft, revision: 2 } }) }
  );
  assert.equal(old.outcome, OUTCOME.UNAVAILABLE);
  assert.equal(stale.state.opens.length, 0);

  const elsewhere = makePage({ workspaceId: 'ws-other' });
  assert.deepEqual(await elsewhere.targets.execute(formCommand, { snapshot: snapshotOf() }), {
    outcome: OUTCOME.YIELD,
    reason: 'moved_elsewhere'
  });
  assert.equal(elsewhere.state.opens.length, 0);

  const unassigned = makePage();
  const none = await unassigned.targets.execute(formCommand, {
    snapshot: snapshotOf({ draft: { ...draft, agent_node_id: '' } })
  });
  assert.equal(none.outcome, OUTCOME.UNAVAILABLE);
  assert.equal(unassigned.state.opens.length, 0);
  assert.equal(
    (await makePage().targets.execute(formCommand, { snapshot: null })).outcome,
    OUTCOME.UNAVAILABLE
  );
});

// ---- the client ----

function memoryStorage(initial = {}) {
  const data = new Map(Object.entries(initial));
  return {
    getItem: key => (data.has(key) ? data.get(key) : null),
    setItem: (key, value) => data.set(key, String(value)),
    removeItem: key => data.delete(key)
  };
}

function makeClient(script) {
  const calls = [];
  const executed = [];
  let current = snapshotOf();
  const client = new OperationClient({
    request: async (method, path, body) => {
      calls.push({ method, path, body });
      if (path.endsWith('/ack')) {
        const command = current.commands.find(item => item.id === body.command_id);
        command.status = body.outcome;
        current = {
          ...current,
          revision: current.revision + 1,
          pending: current.commands.filter(item => item.status === 'pending').length
        };
        return { operation: current };
      }
      if (path.endsWith('/cancel')) {
        current = { ...current, status: 'cancelled', pending: 0 };
        return { operation: current };
      }
      return { operation: current };
    },
    storage: memoryStorage({ [TAB_KEY]: 'tab-a' }),
    targets: {
      execute: async (command, options) => {
        executed.push({
          action: command.action,
          replace: Boolean(options.replace),
          draft: options.snapshot && options.snapshot.draft,
          onSaved: options.onSaved
        });
        return script(command, options, executed.length);
      }
    },
    navigate: () => {},
    now: () => 1000,
    newId: () => 'fresh',
    wait: async () => {}
  });
  return { client, calls, executed, snapshot: () => current };
}

test('the client hands the adapter the current draft and reports the form by its revision', async () => {
  const { client, calls, executed } = makeClient(command =>
    command.action === 'open_workspace'
      ? { outcome: OUTCOME.ACKNOWLEDGED, observed: { path: '/workspaces/album-1' } }
      : { outcome: OUTCOME.ACKNOWLEDGED, observed: { draft_revision: 1 } }
  );
  await client.adopt(snapshotOf());
  assert.equal(client.phase, PHASE.SETTLED);
  assert.deepEqual(
    executed.map(step => step.action),
    ['open_workspace', 'open_task_draft']
  );
  assert.equal(executed[1].draft.title, 'Bounce the stems');
  assert.equal(typeof executed[1].onSaved, 'function');
  const acks = calls.filter(call => call.path.endsWith('/ack')).map(call => call.body);
  assert.deepEqual(acks[1].observed, { draft_revision: 1 });
});

test('unsaved work pauses the draft; Resume replaces only because the user chose it', async () => {
  const { client, executed } = makeClient((command, options) => {
    if (command.action === 'open_workspace')
      return { outcome: OUTCOME.ACKNOWLEDGED, observed: { path: '/workspaces/album-1' } };
    return options.replace
      ? { outcome: OUTCOME.ACKNOWLEDGED, observed: { draft_revision: 1 } }
      : { outcome: OUTCOME.YIELD, reason: 'unsaved_work' };
  });
  await client.adopt(snapshotOf());
  assert.equal(client.phase, PHASE.PAUSED);
  assert.equal(client.pauseReason, 'unsaved_work');
  assert.equal(executed.at(-1).replace, false, 'nothing is replaced by itself');
  await client.resume();
  assert.equal(executed.at(-1).replace, true);
  assert.equal(client.phase, PHASE.SETTLED);
});

test('reopening a draft is local, reports nothing and keeps the user edits', async () => {
  const outcomes = [
    { outcome: OUTCOME.ACKNOWLEDGED, observed: { draft_revision: 1 } },
    { outcome: OUTCOME.YIELD, reason: 'unsaved_work' },
    { outcome: OUTCOME.YIELD, reason: 'moved_elsewhere' },
    { outcome: OUTCOME.UNAVAILABLE, reason: 'no form' }
  ];
  const { client, calls, executed } = makeClient((_command, _options, n) => outcomes[n - 1]);
  client.snapshot = snapshotOf({ pending: 0 });
  assert.equal(await client.openDraft(), 'opened');
  assert.equal(await client.openDraft(), 'kept');
  assert.equal(await client.openDraft(), 'elsewhere');
  assert.equal(await client.openDraft(), 'unavailable');
  assert.deepEqual(calls, [], 'reopening sends nothing to the server');
  assert.ok(executed.every(step => step.replace === false));
  // An ended operation, or one without a draft, has nothing to reopen.
  client.snapshot = snapshotOf({ status: 'cancelled' });
  assert.equal(await client.openDraft(), 'unavailable');
  client.snapshot = { ...snapshotOf(), draft: null };
  assert.equal(await client.openDraft(), 'unavailable');
});

test('saving the form stops this tab offering the same draft again', async () => {
  const { client, calls, executed } = makeClient(command =>
    command.action === 'open_workspace'
      ? { outcome: OUTCOME.ACKNOWLEDGED, observed: { path: '/workspaces/album-1' } }
      : { outcome: OUTCOME.ACKNOWLEDGED, observed: { draft_revision: 1 } }
  );
  await client.adopt(snapshotOf());
  // The form's own save owner created the task and calls back with it.
  await executed[1].onSaved({ task: { id: 'task-9', to: 'Engineer' } });
  assert.equal(client.snapshot, null);
  assert.equal(client.phase, PHASE.IDLE);
  assert.deepEqual(client.saved, { task: { id: 'task-9', to: 'Engineer' } });
  // The operation is dismissed; the client itself created nothing.
  assert.deepEqual(
    calls.filter(call => call.method === 'POST').map(call => call.path.split('/').pop()),
    ['ack', 'ack', 'cancel']
  );
  assert.equal(await client.openDraft(), 'unavailable');
});

// ---- what the drawer says ----

const withForm = (open, form, overrides = {}) =>
  snapshotOf({
    pending: [open, form].filter(status => status === 'pending').length,
    commands: [
      { id: 'c1', sequence: 1, action: 'open_workspace', status: open },
      { id: 'c2', sequence: 2, action: 'open_task_draft', status: form, reason: overrides.reason }
    ],
    ...overrides
  });

test('a draft is never called a task, and each step is said only once observed', () => {
  assert.equal(
    operationView({ snapshot: withForm('pending', 'pending'), phase: PHASE.RUNNING }).text,
    'Opening Album 1…'
  );
  assert.equal(
    operationView({ snapshot: withForm('acknowledged', 'pending'), phase: PHASE.RUNNING }).text,
    'Opened Album 1. Opening the task form…'
  );
  const shown = operationView({
    snapshot: withForm('acknowledged', 'acknowledged'),
    phase: PHASE.SETTLED,
    onWorkspace: true
  });
  assert.equal(
    shown.text,
    'Draft for Engineer: “Bounce the stems”. Review it in the task form. Nothing is created until you save it.'
  );
  assert.deepEqual(shown.actions, [
    { id: 'open_draft', label: 'Open draft again' },
    { id: 'dismiss', label: 'Dismiss' }
  ]);
  // Away from the workspace the form cannot be reopened; the page is linked.
  const away = operationView({
    snapshot: withForm('acknowledged', 'acknowledged'),
    phase: PHASE.SETTLED,
    onWorkspace: false
  });
  assert.deepEqual(away.actions, [{ id: 'dismiss', label: 'Dismiss' }]);
  assert.deepEqual(away.link, { href: '/workspaces/album-1', label: 'Open Album 1 yourself' });
  for (const view of [shown, away])
    assert.doesNotMatch(view.text, /task (was|is|has been) (created|assigned|started)/i);
});

test('Watch off keeps an explicit Open draft; unsaved work asks before replacing', () => {
  const waiting = operationView({
    snapshot: withForm('pending', 'pending', { watching: false, commands: [] }),
    phase: PHASE.WAITING
  });
  assert.equal(
    waiting.text,
    'Draft ready for Engineer: “Bounce the stems”. Nothing is created until you save it.'
  );
  assert.deepEqual(waiting.actions[0], { id: 'resume', label: 'Open draft', primary: true });

  const dirty = operationView({
    snapshot: withForm('acknowledged', 'pending'),
    phase: PHASE.PAUSED,
    pauseReason: 'unsaved_work'
  });
  assert.equal(dirty.text, "This page has unsaved changes. Ori's draft for Engineer is waiting.");
  assert.deepEqual(dirty.actions[0], {
    id: 'resume',
    label: "Replace with Ori's draft",
    primary: true
  });

  const moved = operationView({
    snapshot: withForm('pending', 'pending'),
    phase: PHASE.PAUSED,
    pauseReason: 'moved_elsewhere'
  });
  assert.match(moved.text, /another page/);
  assert.deepEqual(moved.actions[0], { id: 'resume', label: 'Open draft', primary: true });
});

test('a form that could not take the draft, an ended draft and local notes are said plainly', () => {
  const failed = operationView({
    snapshot: withForm('acknowledged', 'unavailable', {
      reason: 'Engineer cannot be assigned in the task form.'
    }),
    phase: PHASE.SETTLED
  });
  assert.equal(
    failed.text,
    'Ori could not open the task form. Engineer cannot be assigned in the task form.'
  );
  assert.equal(failed.tone, 'attention');

  assert.match(
    operationView({
      snapshot: withForm('pending', 'pending', { status: 'expired' }),
      phase: PHASE.ENDED
    }).text,
    /draft ran out of time/
  );
  assert.match(
    operationView({
      snapshot: withForm('pending', 'pending', { status: 'invalidated' }),
      phase: PHASE.ENDED
    }).text,
    /workspace or its team changed/
  );

  // A note replaces the sentence and keeps the choices.
  const kept = operationView({
    snapshot: withForm('acknowledged', 'acknowledged'),
    phase: PHASE.SETTLED,
    onWorkspace: true,
    note: 'The task form is open with your changes. Nothing was replaced.'
  });
  assert.equal(kept.text, 'The task form is open with your changes. Nothing was replaced.');
  assert.equal(kept.actions.length, 2);
  // After a save there is no operation left, only the remark.
  assert.equal(
    operationView({ note: 'Task saved from the draft. It is in the workspace now.' }).text,
    'Task saved from the draft. It is in the workspace now.'
  );
  // A long title is shortened in the status line, never in the form.
  const long = operationView({
    snapshot: withForm('acknowledged', 'acknowledged', {
      draft: { ...draft, title: 'x'.repeat(200) }
    }),
    phase: PHASE.SETTLED
  });
  assert.ok(long.text.includes('x'.repeat(60) + '…'));
  assert.ok(!long.text.includes('x'.repeat(61)));
});

test('the operation key in storage is only a pointer, never the draft', async () => {
  const storage = memoryStorage({ [TAB_KEY]: 'tab-a' });
  const client = new OperationClient({
    request: async () => ({ operation: snapshotOf() }),
    storage,
    targets: { execute: async () => ({ outcome: OUTCOME.YIELD, reason: 'unsaved_work' }) },
    navigate: () => {},
    now: () => 1000,
    newId: () => 'fresh',
    wait: async () => {}
  });
  await client.adopt(snapshotOf());
  const stored = storage.getItem(OPERATION_KEY);
  assert.deepEqual(Object.keys(JSON.parse(stored)).sort(), ['auto_until', 'id']);
  assert.ok(!stored.includes('Bounce the stems'));
});

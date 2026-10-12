import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createOperationTargets, operationResultURL, OUTCOME } from './ori-operation-targets.js';
import { OperationClient, PHASE, TAB_KEY, OPERATION_KEY } from './ori-operation-client.js';
import { operationView, operationRecap } from './personal-assistant-operations.js';

// Results, the recap, taking over from Ori, and coming back after a reload.

const context = {
  workspace_id: 'ws-album',
  workspace_slug: 'album-1',
  workspace_name: 'Album 1',
  agent_name: 'Engineer',
  agent_node_id: 'Engineer-node-1'
};
const draft = {
  revision: 1,
  title: 'Bounce the stems',
  brief: 'Export stems for mastering.',
  agent_name: 'Engineer',
  agent_node_id: 'Engineer-node-1'
};
const receipt = (kind, extra = {}) => ({ kind, at: '2026-10-11T09:00:00Z', ...extra });
const started = (outcome, overrides = {}) => ({
  id: 'op-task',
  kind: 'task',
  status: 'started',
  revision: 9,
  watching: true,
  pending: 0,
  context: { ...context },
  draft: { ...draft },
  commands: [],
  task: { task_id: 'task-9', run_id: 'run-1' },
  receipts: [
    receipt('draft_prepared'),
    receipt('view_opened'),
    receipt('draft_opened'),
    receipt('task_created'),
    receipt('start_accepted'),
    receipt('run_established', { ref: 'run-1' })
  ],
  outcome,
  ...overrides
});
const prepared = (overrides = {}) => ({
  id: 'op-task',
  kind: 'task',
  status: 'prepared',
  revision: 1,
  watching: true,
  pending: 2,
  context: { ...context },
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
  receipts: [receipt('draft_prepared')],
  ...overrides
});

function memoryStorage(initial = {}) {
  const data = new Map(Object.entries(initial));
  return {
    getItem: key => (data.has(key) ? data.get(key) : null),
    setItem: (key, value) => data.set(key, String(value)),
    removeItem: key => data.delete(key)
  };
}

function makeClient({ current, execute, showResult, storage, now = () => 1000 } = {}) {
  const calls = [];
  const navigations = [];
  const server = { current };
  const client = new OperationClient({
    request: async (method, path, body) => {
      const last = path.split('?')[0].split('/').pop();
      calls.push({ method, path: last, body });
      if (method === 'GET') return { operation: server.current };
      if (last === 'ack') {
        const command = server.current.commands.find(item => item.id === body.command_id);
        command.status = body.outcome;
        server.current = {
          ...server.current,
          revision: server.current.revision + 1,
          pending: server.current.commands.filter(item => item.status === 'pending').length
        };
        return { operation: server.current };
      }
      if (last === 'watch') {
        server.current = { ...server.current, watching: Boolean(body.watch) };
        return { operation: server.current };
      }
      if (last === 'cancel') return { operation: { ...server.current, status: 'cancelled' } };
      throw new Error('unexpected ' + path);
    },
    storage: storage || memoryStorage({ [TAB_KEY]: 'tab-a' }),
    targets: { execute: execute || (async () => ({ outcome: 'unavailable' })), showResult },
    navigate: url => navigations.push(url),
    now,
    newId: () => 'fresh',
    wait: async () => {},
    later: () => () => {}
  });
  const posts = () => calls.filter(call => call.method === 'POST').map(call => call.path);
  return { client, calls, posts, navigations, server };
}

const viewOf = (snapshot, extra = {}) =>
  operationView({ snapshot, phase: PHASE.SETTLED, taskTitle: 'Bounce the stems', ...extra });

// ---- the result of this request's own run ----

test('Open result is offered only when the task shows this run result', () => {
  const current = viewOf(started({ run: 'succeeded', result: 'current' }), {
    taskPresentation: { state: 'completed' }
  });
  assert.equal(current.text, 'Engineer finished “Bounce the stems”.');
  assert.deepEqual(current.actions, [
    { id: 'open_result', label: 'Open result', primary: true },
    { id: 'dismiss', label: 'Dismiss' }
  ]);

  // The task says completed, but the server has not yet said whose result it is.
  const unknownYet = viewOf(started(null), { taskPresentation: { state: 'completed' } });
  assert.ok(!unknownYet.actions.some(action => action.id === 'open_result'));

  const gone = viewOf(started({ run: 'succeeded', result: 'gone' }), {
    taskPresentation: { state: 'completed' }
  });
  assert.equal(
    gone.text,
    'Engineer finished “Bounce the stems”. Its result is no longer kept on the task.'
  );
  assert.ok(!gone.actions.some(action => action.id === 'open_result'));
});

test('a later run is never shown as this request result', () => {
  const rerun = viewOf(started({ run: 'succeeded', result: 'history', later_runs: 1 }), {
    // The task's own state is the LATER run's: completed, with a result.
    taskPresentation: { state: 'completed' }
  });
  assert.equal(
    rerun.text,
    "Engineer finished “Bounce the stems”. The task has run again since, so what it shows now is a later run's result. This request's result is in the task's run history."
  );
  assert.ok(!rerun.actions.some(action => action.id === 'open_result'));
  assert.deepEqual(rerun.link, {
    href: '/workspaces/album-1?panel=tasks&task=task-9',
    label: 'Open the task'
  });

  // While a rerun is in progress the drawer does not say this request is "working".
  const rerunning = viewOf(started({ run: 'succeeded', result: 'history', later_runs: 1 }), {
    taskPresentation: { state: 'running' }
  });
  assert.doesNotMatch(rerunning.text, /is working on/);

  const lost = viewOf(started({ run: 'succeeded', result: 'gone', later_runs: 2 }), {
    taskPresentation: { state: 'completed' }
  });
  assert.match(lost.text, /this request's own result is no longer kept/);
  const continued = viewOf(started({ run: 'stopped', result: 'none', later_runs: 1 }), {
    taskPresentation: { state: 'completed' }
  });
  assert.match(continued.text, /stopped and asked for input\. It was continued from the task/);
  const trimmed = viewOf(started({ run: 'unknown', result: 'gone', later_runs: 4 }), {
    taskPresentation: { state: 'completed' }
  });
  assert.match(trimmed.text, /no longer keeps this request's own run/);
  for (const view of [rerun, rerunning, lost, continued, trimmed])
    assert.ok(!view.actions.some(action => action.id === 'open_result'));
});

test('an interrupted run and a silent connection are said as they are', () => {
  const interrupted = viewOf(started({ run: 'interrupted', result: 'none' }), {
    taskPresentation: { state: 'ready' }
  });
  assert.equal(
    interrupted.text,
    'The run of “Bounce the stems” was interrupted before it finished, and nothing was started again. Run it from the task when you are ready.'
  );
  assert.equal(interrupted.tone, 'attention');
  assert.ok(!interrupted.actions.some(action => action.id === 'start'), 'no restart from here');

  const rerunAfter = viewOf(started({ run: 'interrupted', result: 'none', later_runs: 1 }), {
    taskPresentation: { state: 'completed' }
  });
  assert.match(rerunAfter.text, /was interrupted before it finished\. The task has run again/);
  assert.ok(!rerunAfter.actions.some(action => action.id === 'open_result'));

  // Not hearing from the task is not the task finishing.
  const quiet = viewOf(started({ run: 'running', result: 'none' }), {
    taskPresentation: { state: 'running', label: 'Running' },
    taskPhase: 'reconnecting'
  });
  assert.equal(quiet.text, 'Reconnecting to “Bounce the stems”… It last reported: Running.');
  assert.doesNotMatch(quiet.text, /finished|completed/i);
});

test('opening a result uses the page own view, or its own one-shot link', () => {
  const shown = [];
  const here = makeClient({
    current: started({ run: 'succeeded', result: 'current' }),
    showResult: (workspaceId, taskId) => {
      shown.push([workspaceId, taskId]);
      return true;
    }
  });
  here.client._accept(here.server.current);
  assert.equal(here.client.openResult(), 'opened');
  assert.deepEqual(shown, [['ws-album', 'task-9']]);
  assert.deepEqual(here.navigations, []);

  const away = makeClient({
    current: started({ run: 'succeeded', result: 'current' }),
    showResult: () => false
  });
  away.client._accept(away.server.current);
  assert.equal(away.client.openResult(), 'navigating');
  assert.deepEqual(away.navigations, ['/workspaces/album-1?task=task-9&result=1']);

  // Not this run's result: nothing opens, whatever was clicked.
  for (const outcome of [
    { run: 'succeeded', result: 'history', later_runs: 1 },
    { run: 'succeeded', result: 'gone' },
    { run: 'running', result: 'none' },
    null
  ]) {
    const other = makeClient({ current: started(outcome), showResult: () => true });
    other.client._accept(other.server.current);
    assert.equal(other.client.openResult(), 'unavailable');
    assert.deepEqual(other.navigations, []);
  }
  // Opening a result reports nothing and changes nothing.
  assert.deepEqual(here.posts(), []);
  assert.deepEqual(away.posts(), []);
});

test('a result link is built only from a plain slug and a task identity', () => {
  assert.equal(operationResultURL('album-1', 'task-9'), '/workspaces/album-1?task=task-9&result=1');
  for (const [slug, id] of [
    ['', 'task-9'],
    ['album-1', ''],
    ['../etc', 'task-9'],
    ['album-1', 'task 9&result=0'],
    ['album-1', 'https://example.com'],
    ['a#b', 'task-9']
  ]) {
    assert.equal(operationResultURL(slug, id), '', `${slug} ${id}`);
  }
});

// ---- the recap ----

test('the recap lists recorded facts by where each came from', () => {
  const recap = operationRecap({
    snapshot: started({ run: 'succeeded', result: 'current' })
  });
  assert.equal(recap.label, 'Recap');
  assert.deepEqual(
    recap.items.map(item => [item.sourceLabel, item.text]),
    [
      ['Recorded by Ori', 'A draft was prepared for Engineer.'],
      ['Seen in this tab', 'Album 1 was opened.'],
      ['Seen in this tab', 'The draft was shown in the task form.'],
      ['Recorded by Ori', 'You saved the form, and the task was created.'],
      ['Recorded by Ori', 'You pressed Start, and it was accepted.'],
      ['From the task', 'The task reported that it had started.'],
      ['From the task', 'It finished.']
    ]
  );
  // A view step is never a write, and an accepted Start is never a finish.
  const tab = recap.items.filter(item => item.source === 'tab').map(item => item.text);
  assert.ok(tab.every(text => !/created|started|finished|accepted/i.test(text)));
  const accepted = recap.items.find(item => /accepted/.test(item.text));
  assert.equal(accepted.source, 'ori');
});

test('the recap says only what was recorded, including refusals and reruns', () => {
  const refused = operationRecap({
    snapshot: started(null, {
      status: 'reviewed',
      receipts: [
        receipt('draft_prepared'),
        receipt('task_created'),
        receipt('start_refused', { reason: 'Ori is being reset, so nothing can start right now' })
      ]
    })
  });
  assert.deepEqual(
    refused.items.map(item => item.text),
    [
      'A draft was prepared for Engineer.',
      'You saved the form, and the task was created.',
      'A Start was refused. Ori is being reset, so nothing can start right now.'
    ]
  );
  // Nothing about a run that never happened.
  assert.ok(!refused.items.some(item => item.source === 'task'));

  const rerun = operationRecap({
    snapshot: started({ run: 'succeeded', result: 'history', later_runs: 2 })
  });
  assert.equal(rerun.items.at(-1).text, 'It finished. The task has run 2 times more since.');
  const interrupted = operationRecap({ snapshot: started({ run: 'interrupted', result: 'none' }) });
  assert.equal(interrupted.items.at(-1).text, 'It was interrupted before it finished.');

  // A navigation request with one fact has nothing to recap; no operation, none.
  assert.equal(operationRecap({ snapshot: prepared() }), null);
  assert.equal(operationRecap({}), null);
});

// ---- taking over ----

test('a step that was waiting on the page does nothing once its request is dismissed', async () => {
  const opened = [];
  const selected = [];
  let ticks = 0;
  let wanted = true;
  const composer = {
    isModalOpen: () => false,
    hasUnsavedChanges: () => false,
    openForCreate: async (...args) => opened.push(args),
    close() {}
  };
  const roster = {
    selectedAgentKey: '',
    normalizeAgentKey: name => String(name).toLowerCase(),
    agentGroups: () => ({ agents: [{ key: 'engineer', name: 'Engineer' }] }),
    selectAgent: name => selected.push(name)
  };
  const env = {
    pathname: () => '/workspaces/album-1',
    // The page's controllers mount late: the adapters wait three beats.
    workspacePage: () => ({
      workspaceId: 'ws-album',
      command: ticks >= 3 ? roster : null,
      composer: ticks >= 3 ? composer : null
    }),
    agentControl: () => null,
    taskForm: () => ({ open: opened.length > 0, title: '', brief: '', assignment: '' }),
    hasUnsavedWork: () => false,
    wait: async () => {
      ticks += 1;
      // The user dismisses while the adapter is still waiting for the page.
      if (ticks === 2) wanted = false;
    }
  };
  const targets = createOperationTargets(env);
  const stoodDown = { outcome: OUTCOME.YIELD, reason: 'stood_down' };

  const form = await targets.execute(
    { action: 'open_task_draft', workspace_id: 'ws-album', draft_revision: 1 },
    { snapshot: prepared(), wanted: () => wanted }
  );
  assert.deepEqual(form, stoodDown);
  assert.deepEqual(opened, [], 'no form opens for a request nobody wants any more');

  ticks = 0;
  wanted = true;
  const selection = await targets.execute(
    { action: 'select_agent', workspace_id: 'ws-album', agent_name: 'Engineer' },
    { wanted: () => wanted }
  );
  assert.deepEqual(selection, stoodDown);
  assert.deepEqual(selected, [], 'no agent is selected for it either');

  // The same steps do act while the request is still wanted.
  ticks = 3;
  wanted = true;
  await targets.execute(
    { action: 'open_task_draft', workspace_id: 'ws-album', draft_revision: 1 },
    { snapshot: prepared(), wanted: () => wanted }
  );
  assert.equal(opened.length, 1);
});

test('dismissing or turning Watch off mid-step reports nothing and moves nothing', async () => {
  for (const takeover of ['dismiss', 'watch_off']) {
    let finish;
    const step = new Promise(resolve => {
      finish = resolve;
    });
    const seen = [];
    const { client, posts, navigations, server } = makeClient({
      current: prepared(),
      execute: async (command, options) => {
        seen.push(options.wanted);
        return step;
      }
    });
    const running = client.adopt(server.current);
    await Promise.resolve();
    assert.equal(seen[0](), true);
    if (takeover === 'dismiss') await client.dismiss();
    else await client.setWatching(false);
    assert.equal(seen[0](), false, 'the waiting step is told at once');
    // The page-side wait ends late, claiming it should navigate.
    finish({ outcome: OUTCOME.NAVIGATING, url: '/workspaces/album-1' });
    await running;
    assert.deepEqual(navigations, [], `${takeover}: no late navigation`);
    assert.ok(!posts().includes('ack'), `${takeover}: nothing is acknowledged for it`);
  }
});

test('changing conversation stops the steps, keeps the request, and Resume carries on', async () => {
  const outcomes = [
    { outcome: OUTCOME.YIELD, reason: 'moved_elsewhere' },
    { outcome: OUTCOME.ACKNOWLEDGED, observed: { path: '/workspaces/album-1' } },
    { outcome: OUTCOME.ACKNOWLEDGED, observed: { draft_revision: 1 } }
  ];
  const { client, posts, server } = makeClient({
    current: prepared(),
    execute: async () => outcomes.shift()
  });
  await client.adopt(server.current);
  client.highlight = { element: {}, commandId: 'c0' };
  client.standDown('conversation_changed');
  assert.equal(client.phase, PHASE.PAUSED);
  assert.equal(client.pauseReason, 'conversation_changed');
  assert.equal(client.highlight, null, 'a stale mark is cleared');
  assert.equal(client.snapshot.id, 'op-task', 'the request is still there');
  assert.ok(!posts().includes('cancel'), 'nothing is cancelled');
  const paused = operationView({
    snapshot: client.snapshot,
    phase: client.phase,
    pauseReason: client.pauseReason
  });
  assert.equal(paused.text, 'Paused: you moved to another conversation.');
  assert.deepEqual(paused.actions[0], { id: 'resume', label: 'Open draft', primary: true });

  // A stray run() while stood down does nothing.
  await client.run({ allowNavigate: true });
  assert.ok(!posts().includes('ack'));
  // Resume is the user's choice and picks up the same request.
  await client.resume();
  assert.deepEqual(
    posts().filter(path => path === 'ack'),
    ['ack', 'ack']
  );
  assert.equal(client.phase, PHASE.SETTLED);
});

test('standing down from a started task keeps following it and cancels nothing', () => {
  const { client, posts, server } = makeClient({
    current: started({ run: 'running', result: 'none' })
  });
  client._accept(server.current);
  client.standDown('conversation_changed');
  assert.equal(client.phase, PHASE.SETTLED);
  assert.equal(client.snapshot.status, 'started');
  assert.deepEqual(posts(), []);
});

test('a newer request replaces a step still waiting for the older one', async () => {
  let finishOld;
  const oldStep = new Promise(resolve => {
    finishOld = resolve;
  });
  const executed = [];
  const newer = prepared({ id: 'op-new' });
  const { client, calls, server } = makeClient({
    current: prepared(),
    execute: async (command, options) => {
      executed.push(options.snapshot.id);
      if (options.snapshot.id === 'op-task') return oldStep;
      return command.action === 'open_workspace'
        ? { outcome: OUTCOME.ACKNOWLEDGED, observed: { path: '/workspaces/album-1' } }
        : { outcome: OUTCOME.ACKNOWLEDGED, observed: { draft_revision: 1 } };
    }
  });
  const first = client.adopt(server.current);
  await Promise.resolve();
  server.current = newer;
  const second = client.adopt(newer);
  finishOld({ outcome: OUTCOME.ACKNOWLEDGED, observed: { path: '/workspaces/album-1' } });
  await Promise.all([first, second]);
  // The old step's outcome was never reported; the new request's steps ran.
  const acked = calls.filter(call => call.path === 'ack').map(call => call.body.command_id);
  assert.deepEqual(acked, ['c1', 'c2']);
  assert.deepEqual(executed, ['op-task', 'op-new', 'op-new']);
  assert.equal(client.snapshot.id, 'op-new');
});

// ---- coming back after a reload ----

test('after a reload the drawer says typed edits are gone instead of silently re-opening', async () => {
  const shown = prepared({
    pending: 0,
    commands: [
      { id: 'c1', sequence: 1, action: 'open_workspace', status: 'acknowledged' },
      { id: 'c2', sequence: 2, action: 'open_task_draft', status: 'acknowledged' }
    ]
  });
  const storage = memoryStorage({
    [TAB_KEY]: 'tab-a',
    [OPERATION_KEY]: JSON.stringify({ id: 'op-task', auto_until: 5000 })
  });
  const executed = [];
  const { client, posts } = makeClient({
    current: shown,
    storage,
    execute: async command => {
      executed.push(command.action);
      return { outcome: OUTCOME.ACKNOWLEDGED, observed: { draft_revision: 1 } };
    }
  });
  await client.restore();
  assert.equal(client.restored, true);
  assert.deepEqual(executed, [], 'a settled draft is not re-opened by a page load');
  assert.deepEqual(posts(), []);

  const state = {
    snapshot: client.snapshot,
    phase: client.phase,
    restored: client.restored,
    onWorkspace: true,
    formOpen: false
  };
  const view = operationView(state);
  assert.equal(
    view.text,
    "Draft for Engineer: “Bounce the stems”. This page was reloaded, so anything typed into the form is gone. Open the draft again to start from Ori's version. Nothing is created until you save it."
  );
  assert.equal(view.tone, 'attention');
  assert.deepEqual(view.actions[0], { id: 'open_draft', label: 'Open draft again', primary: true });
  // With the form showing, or away from the workspace, the ordinary line stands.
  assert.match(operationView({ ...state, formOpen: true }).text, /Review it in the task form/);
  assert.match(operationView({ ...state, onWorkspace: false }).text, /Review it in the task form/);

  // Opening the draft again is the user's choice, and ends the notice.
  await client.openDraft();
  assert.equal(client.restored, false);
  assert.deepEqual(executed, ['open_task_draft']);
});

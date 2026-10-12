import { test } from 'node:test';
import assert from 'node:assert/strict';
import { operationTaskURL } from './ori-operation-targets.js';
import {
  OperationClient,
  PHASE,
  TAB_KEY,
  OPERATION_KEY,
  FOLLOW_FAST_MS,
  FOLLOW_SLOW_MS
} from './ori-operation-client.js';
import { operationView } from './personal-assistant-operations.js';

// Creating and starting the reviewed task: the form's save binds one task, the
// user's own Start is claimed once, and what the drawer says about execution
// comes from the task, never from an acceptance.

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
const receipt = (kind, reason) => ({ kind, at: '2026-10-11T09:00:00Z', ref: 'task-9', reason });
const operation = (status, overrides = {}) => ({
  id: 'op-task',
  kind: 'task',
  status,
  revision: 5,
  watching: true,
  pending: 0,
  context: { ...context },
  draft: { ...draft },
  commands: [
    { id: 'c1', sequence: 1, action: 'open_workspace', status: 'acknowledged' },
    { id: 'c2', sequence: 2, action: 'open_task_draft', status: 'acknowledged' }
  ],
  task: status === 'prepared' ? undefined : { task_id: 'task-9' },
  receipts: status === 'prepared' ? [] : [receipt('task_created')],
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

/** An execution controller as the client uses it: track, read, subscribe. */
function makeMonitor({ owned = false, canShow = true } = {}) {
  const state = { tracked: [], shown: [], disposed: 0, unsubscribed: 0, listeners: new Set() };
  let run = null;
  const controller = {
    track(taskId, options) {
      state.tracked.push({ taskId, select: options && options.select });
      run = run || { taskId, phase: 'starting', task: null, presentation: null, lastActivityAt: 0 };
      return run;
    },
    getRun: taskId => (run && run.taskId === taskId ? run : null),
    subscribe(listener) {
      state.listeners.add(listener);
      return () => {
        state.unsubscribed += 1;
        state.listeners.delete(listener);
      };
    },
    dispose() {
      state.disposed += 1;
    }
  };
  const monitor = {
    controller,
    owned,
    show: canShow
      ? taskId => {
          state.shown.push(taskId);
          controller.track(taskId, { select: true });
        }
      : null
  };
  /** The task moved: what the controller's own poll would have read. */
  const report = (presentationState, task = {}, phase = 'live') => {
    run.phase = phase;
    run.task = { id: run.taskId, description: 'Bounce the stems at 48k', ...task };
    run.presentation = { state: presentationState };
    run.lastActivityAt += 1;
    state.listeners.forEach(listener => listener());
  };
  return { monitor, state, report };
}

/**
 * A client over a scripted server. `server.current` is what a read returns;
 * `server.start` answers the Start request.
 */
function makeClient({ current, start, monitor = makeMonitor(), now = () => 1000, storage } = {}) {
  const calls = [];
  const timers = [];
  const server = { current, start, readFails: false };
  const client = new OperationClient({
    request: async (method, path, body) => {
      calls.push({ method, path: path.split('?')[0].split('/').pop(), body });
      if (method === 'GET') {
        if (server.readFails) throw new Error('offline');
        return { operation: server.current };
      }
      if (path.endsWith('/start')) return server.start(server);
      if (path.endsWith('/cancel'))
        return { operation: { ...server.current, status: 'cancelled' } };
      throw new Error('unexpected ' + path);
    },
    storage: storage || memoryStorage({ [TAB_KEY]: 'tab-a' }),
    targets: { execute: async () => ({ outcome: 'unavailable' }) },
    navigate: () => {},
    now,
    newId: () => 'fresh',
    wait: async () => {},
    taskMonitor: () => monitor.monitor,
    later: (fn, ms) => {
      const timer = { fn, ms, cancelled: false };
      timers.push(timer);
      return () => {
        timer.cancelled = true;
      };
    }
  });
  const pending = () => timers.filter(timer => !timer.cancelled && !timer.fired);
  /** Let the next scheduled read happen. */
  const tick = async () => {
    const timer = pending()[0];
    timer.fired = true;
    await timer.fn();
  };
  const posts = () => calls.filter(call => call.method === 'POST').map(call => call.path);
  return { client, calls, server, monitor, timers, pending, tick, posts };
}

const settle = () => new Promise(resolve => setImmediate(resolve));

const view = (client, extra = {}) =>
  operationView({
    snapshot: client.snapshot,
    phase: client.phase,
    task: client.task,
    taskPresentation: client.taskPresentation,
    taskTitle: (client.task && client.task.description) || '',
    starting: client.starting,
    startUnconfirmed: client.startUnconfirmed,
    canShowTask: client.canShowTask(),
    ...extra
  });

test('a save the server bound to the operation is followed, not dismissed', async () => {
  const { client, server, posts, pending } = makeClient({ current: operation('prepared') });
  client._accept(operation('prepared'));
  // The form's own create succeeded; the server now shows the task.
  server.current = operation('reviewed');
  await client._draftSaved('op-task', {
    task: {
      id: 'task-9',
      description: 'Bounce the stems at 48k',
      assistant_operation_id: 'op-task'
    }
  });
  assert.equal(client.snapshot.status, 'reviewed');
  assert.deepEqual(posts(), [], 'a bound save cancels nothing and creates nothing here');
  // It keeps following on a slow beat, in case the task is started elsewhere.
  assert.deepEqual(
    pending().map(timer => timer.ms),
    [FOLLOW_SLOW_MS]
  );

  const shown = view(client, { taskTitle: 'Bounce the stems at 48k' });
  assert.equal(
    shown.text,
    'Task created for Engineer: “Bounce the stems at 48k”. It has not started.'
  );
  assert.deepEqual(shown.actions, [
    { id: 'start', label: 'Start task', primary: true },
    { id: 'dismiss', label: 'Dismiss' }
  ]);
  assert.deepEqual(shown.link, {
    href: '/workspaces/album-1?panel=tasks&task=task-9',
    label: 'Open the task'
  });
  // Created is not started: nothing here suggests work has begun.
  assert.doesNotMatch(shown.text, /is working|is running|accepted|finished/i);
});

test('a bound save the server has not shown yet is read again, never assumed', async () => {
  const { client, server, pending, tick, posts } = makeClient({ current: operation('prepared') });
  client._accept(operation('prepared'));
  server.readFails = true;
  await client._draftSaved('op-task', {
    task: { id: 'task-9', assistant_operation_id: 'op-task' }
  });
  // Still a draft as far as this tab can prove.
  assert.equal(client.snapshot.status, 'prepared');
  assert.deepEqual(
    pending().map(timer => timer.ms),
    [FOLLOW_FAST_MS]
  );
  server.readFails = false;
  server.current = operation('reviewed');
  await tick();
  assert.equal(client.snapshot.status, 'reviewed');
  assert.deepEqual(posts(), []);
});

test('a save that was not bound is the user own task: the draft stops being offered', async () => {
  const { client, posts } = makeClient({ current: operation('prepared') });
  client._accept(operation('prepared'));
  await client._draftSaved('op-task', { task: { id: 'task-3', to: 'Engineer' } });
  assert.equal(client.snapshot, null);
  assert.deepEqual(posts(), ['cancel']);
  // A save for some other operation changes nothing here.
  client._accept(operation('prepared'));
  await client._draftSaved('op-other', {
    task: { id: 'task-4', assistant_operation_id: 'op-other' }
  });
  assert.equal(client.snapshot.id, 'op-task');
});

test('Start is sent once, and an acceptance is shown as waiting, not as running', async () => {
  let release;
  const gate = new Promise(resolve => {
    release = resolve;
  });
  const { client, posts, monitor, pending } = makeClient({
    current: operation('reviewed'),
    start: async () => {
      await gate;
      return {
        operation: operation('start_requested', {
          task: { task_id: 'task-9', claimed_at: '2026-10-11T09:00:05Z' },
          receipts: [receipt('task_created'), receipt('start_accepted')]
        })
      };
    }
  });
  client._accept(operation('reviewed'));

  // A double click, then a third while the first is still on its way.
  const first = client.start();
  const second = client.start();
  assert.equal(client.starting, true);
  assert.equal(view(client).text, 'Asking Ori to start the task for Engineer…');
  assert.deepEqual(view(client).actions, [], 'no second Start while one is on its way');
  assert.equal(await second, false);
  assert.equal(await client.start(), false);
  release();
  assert.equal(await first, true);
  assert.deepEqual(posts(), ['start']);

  assert.equal(client.snapshot.status, 'start_requested');
  // The page's own execution tray is asked to follow the task this tab started.
  assert.deepEqual(monitor.state.shown, ['task-9']);
  const accepted = view(client);
  assert.equal(accepted.text, 'Start accepted. Waiting for Engineer to begin…');
  assert.equal(accepted.tone, 'working');
  assert.deepEqual(
    accepted.actions.map(action => [action.id, action.label]),
    [
      ['show_task', 'Show progress'],
      ['dismiss', 'Stop following']
    ]
  );
  // Stopping here is not cancelling the task, and the control says so.
  assert.match(accepted.actions[1].hint, /The task keeps going; cancel it from the task itself/);
  assert.doesNotMatch(accepted.text, /is working|finished|completed/i);
  assert.deepEqual(
    pending().map(timer => timer.ms),
    [FOLLOW_FAST_MS]
  );

  // Once claimed, Start is not offered and cannot be sent again from here.
  assert.equal(await client.start(), false);
  assert.deepEqual(posts(), ['start']);
});

test('execution is shown from the task: running, then the run the server observed', async () => {
  const { client, server, monitor, pending, calls } = makeClient({
    current: operation('reviewed'),
    start: async () => ({
      operation: operation('start_requested', {
        receipts: [receipt('task_created'), receipt('start_accepted')]
      })
    })
  });
  client._accept(operation('reviewed'));
  await client.start();
  const readsBefore = calls.filter(call => call.method === 'GET').length;

  // The task itself reports that it is running; the server has seen the run.
  server.current = operation('started', {
    task: { task_id: 'task-9', run_id: 'run-1' },
    receipts: [receipt('task_created'), receipt('start_accepted'), receipt('run_established')]
  });
  monitor.report('running');
  await settle();
  assert.equal(calls.filter(call => call.method === 'GET').length, readsBefore + 1);
  assert.equal(client.snapshot.status, 'started');
  assert.equal(client.snapshot.task.run_id, 'run-1');
  const running = view(client);
  assert.equal(running.text, 'Engineer is working on “Bounce the stems at 48k”.');
  assert.equal(running.tone, 'working');
  assert.deepEqual(pending(), [], 'a run that is established needs no timed reads');

  // Later states come from the task too. Each move reads the operation once
  // more (for what became of this request's own run); none sends anything.
  monitor.report('needs_input');
  assert.equal(
    view(client).text,
    'Engineer needs your input to continue “Bounce the stems at 48k”.'
  );
  assert.equal(view(client).tone, 'attention');
  monitor.report('failed', { error: 'The export folder is missing' }, 'settled');
  assert.equal(
    view(client).text,
    '“Bounce the stems at 48k” failed. The export folder is missing.'
  );
  monitor.report('completed', {}, 'settled');
  const done = view(client);
  assert.equal(done.text, 'Engineer finished “Bounce the stems at 48k”.');
  assert.equal(done.tone, 'done');
  assert.deepEqual(done.link, {
    href: '/workspaces/album-1?panel=tasks&task=task-9',
    label: 'Open the task'
  });
  await settle();
  assert.equal(calls.filter(call => call.method === 'GET').length, readsBefore + 4);
  assert.deepEqual(
    calls.filter(call => call.method === 'POST').map(call => call.path),
    ['start'],
    'following a task sends nothing but the one Start'
  );
  assert.deepEqual(pending(), []);
});

test('a refused Start keeps the task and says why; trying again is the same claim', async () => {
  let attempts = 0;
  const { client, monitor, posts } = makeClient({
    current: operation('reviewed'),
    start: async () => {
      attempts += 1;
      if (attempts === 1) {
        const error = new Error('refused');
        error.status = 409;
        error.data = {
          code: 'operation_start_refused',
          message: 'Ori is being reset, so nothing can start right now.',
          details: {
            operation: operation('reviewed', {
              receipts: [
                receipt('task_created'),
                receipt('start_refused', 'Ori is being reset, so nothing can start right now.')
              ]
            })
          }
        };
        throw error;
      }
      return {
        operation: operation('start_requested', {
          receipts: [
            receipt('task_created'),
            receipt('start_refused', 'Ori is being reset, so nothing can start right now.'),
            receipt('start_accepted')
          ]
        })
      };
    }
  });
  client._accept(operation('reviewed'));
  assert.equal(await client.start(), false);
  assert.equal(client.snapshot.status, 'reviewed');
  assert.equal(client.snapshot.task.task_id, 'task-9');
  const refused = view(client);
  assert.equal(
    refused.text,
    'The task for Engineer was not started. Ori is being reset, so nothing can start right now.'
  );
  assert.equal(refused.tone, 'attention');
  assert.deepEqual(refused.actions[0], { id: 'start', label: 'Try starting again', primary: true });
  assert.ok(refused.link, 'the task stays reachable');
  assert.deepEqual(monitor.state.shown, [], 'nothing is shown as running');
  assert.doesNotMatch(refused.text, /working|accepted|finished/i);

  assert.equal(await client.start(), true);
  assert.deepEqual(posts(), ['start', 'start']);
  assert.equal(view(client).text, 'Start accepted. Waiting for Engineer to begin…');
});

test('a Start that got no answer claims nothing and is never repeated by itself', async () => {
  const { client, server, posts, tick, pending } = makeClient({
    current: operation('reviewed'),
    start: async () => {
      throw new Error('network down');
    }
  });
  client._accept(operation('reviewed'));
  server.readFails = true;
  assert.equal(await client.start(), false);
  assert.equal(client.startUnconfirmed, true);
  const unknown = view(client);
  assert.match(unknown.text, /not known whether the task started/);
  assert.deepEqual(unknown.actions[0], { id: 'check', label: 'Check again', primary: true });
  assert.ok(!unknown.actions.some(action => action.id === 'start'), 'no blind retry is offered');

  // Background reads go on, fail, and are scheduled again; none starts anything.
  for (let beat = 0; beat < 3; beat += 1) {
    assert.equal(pending().length, 1);
    await tick();
  }
  assert.equal(client.startUnconfirmed, true);
  assert.deepEqual(posts(), ['start']);

  // The request had arrived after all: checking shows the claim, starts nothing.
  server.readFails = false;
  server.current = operation('start_requested', {
    receipts: [receipt('task_created'), receipt('start_accepted')]
  });
  assert.equal(await client.check(), true);
  assert.equal(client.startUnconfirmed, false);
  assert.equal(view(client).text, 'Start accepted. Waiting for Engineer to begin…');
  assert.deepEqual(posts(), ['start']);
});

test('an unresolved start offers a check first and an explicit retry, and starts nothing itself', async () => {
  const unresolved = operation('unresolved', {
    receipts: [
      receipt('task_created'),
      receipt('start_unresolved', 'Ori could not confirm whether the start request arrived.')
    ]
  });
  const { client, posts, pending } = makeClient({
    current: unresolved,
    start: async () => ({
      operation: operation('start_requested', {
        receipts: [...unresolved.receipts, receipt('start_accepted')]
      })
    })
  });
  client._accept(unresolved);
  const shown = view(client);
  assert.equal(
    shown.text,
    'Ori could not confirm that the task for Engineer started. Ori could not confirm whether the start request arrived. Nothing was started again.'
  );
  assert.deepEqual(
    shown.actions.map(action => action.id),
    ['check', 'start', 'dismiss']
  );
  assert.deepEqual(posts(), []);
  assert.deepEqual(
    pending().map(timer => timer.ms),
    [FOLLOW_SLOW_MS]
  );
  // The retry is the user's click, through the same claim.
  assert.equal(await client.start(), true);
  assert.deepEqual(posts(), ['start']);
});

test('a task started elsewhere is followed without opening anything in this tab', async () => {
  const started = operation('started', {
    task: { task_id: 'task-9', run_id: 'run-1' },
    receipts: [receipt('task_created'), receipt('run_established')]
  });
  // The pointer's auto window has long passed: this is a later visit.
  const storage = memoryStorage({
    [TAB_KEY]: 'tab-a',
    [OPERATION_KEY]: JSON.stringify({ id: 'op-task', auto_until: 500 })
  });
  const { client, monitor, pending } = makeClient({ current: started, storage, now: () => 9000 });
  await client.restore();
  assert.equal(client.snapshot.status, 'started');
  assert.deepEqual(monitor.state.shown, [], 'a page load opens no tray by itself');
  assert.deepEqual(monitor.state.tracked, [{ taskId: 'task-9', select: false }]);
  assert.equal(view(client).text, 'The task for Engineer started.');
  assert.deepEqual(pending(), []);
  monitor.report('running');
  assert.equal(view(client).text, 'Engineer is working on “Bounce the stems at 48k”.');
  // Showing progress is the user's choice.
  assert.equal(client.showTask(), true);
  assert.deepEqual(monitor.state.shown, ['task-9']);
});

test('arriving right after asking shows the page own tray for the running task', async () => {
  const started = operation('started', { task: { task_id: 'task-9', run_id: 'run-1' } });
  const storage = memoryStorage({
    [TAB_KEY]: 'tab-a',
    [OPERATION_KEY]: JSON.stringify({ id: 'op-task', auto_until: 5000 })
  });
  const { client, monitor } = makeClient({ current: started, storage, now: () => 1000 });
  await client.restore();
  assert.deepEqual(monitor.state.shown, ['task-9']);
});

test('the page controller is waited for briefly, then the tab follows with its own monitor', async () => {
  const own = makeMonitor({ owned: true, canShow: false });
  const asked = [];
  const timers = [];
  const client = new OperationClient({
    request: async () => ({
      operation: operation('started', { task: { task_id: 'task-9', run_id: 'r' } })
    }),
    storage: memoryStorage({ [TAB_KEY]: 'tab-a' }),
    targets: { execute: async () => ({ outcome: 'unavailable' }) },
    taskMonitor: (workspaceId, options) => {
      asked.push({ workspaceId, final: options.final });
      return options.final ? own.monitor : { pending: true };
    },
    later: (fn, ms) => {
      const timer = { fn, ms, cancelled: false };
      timers.push(timer);
      return () => {
        timer.cancelled = true;
      };
    }
  });
  client._accept(operation('started', { task: { task_id: 'task-9', run_id: 'r' } }));
  assert.equal(client.canShowTask(), false);
  for (let guard = 0; guard < 40 && !own.state.tracked.length; guard += 1) {
    const next = timers.find(timer => !timer.cancelled && !timer.fired);
    assert.ok(next, 'a retry is scheduled while the page controller is awaited');
    next.fired = true;
    await next.fn();
  }
  assert.equal(asked[0].workspaceId, 'ws-album');
  assert.equal(asked.at(-1).final, true);
  assert.ok(asked.length > 2 && asked.length < 30, 'the wait is bounded');
  assert.deepEqual(own.state.tracked, [{ taskId: 'task-9', select: false }]);
  assert.equal(client.canShowTask(), false, 'away from the page there is no tray to open');
  own.report('running');
  assert.equal(client.taskPresentation.state, 'running');
});

test('dismissing stops following and leaves the task alone', async () => {
  const own = makeMonitor({ owned: true, canShow: false });
  const { client, posts, pending } = makeClient({
    current: operation('started', { task: { task_id: 'task-9' } }),
    monitor: own
  });
  client._accept(operation('started', { task: { task_id: 'task-9' } }));
  assert.equal(pending().length, 1, 'a run without an identity is still being looked for');
  await client.dismiss();
  assert.deepEqual(posts(), ['cancel'], 'only the operation is dismissed');
  assert.equal(own.state.unsubscribed, 1);
  assert.equal(own.state.disposed, 1);
  assert.deepEqual(pending(), []);
  assert.equal(client.task, null);
  assert.equal(client.phase, PHASE.IDLE);
});

test('an ended request says the task is still there, unless the task itself is gone', () => {
  const expired = operationView({ snapshot: operation('expired'), phase: PHASE.ENDED });
  assert.equal(
    expired.text,
    'That request ran out of time. The task for Engineer is still in Album 1.'
  );
  assert.deepEqual(expired.link, {
    href: '/workspaces/album-1?panel=tasks&task=task-9',
    label: 'Open the task'
  });
  const invalid = operationView({ snapshot: operation('invalidated'), phase: PHASE.ENDED });
  assert.match(invalid.text, /no longer valid\. The task for Engineer is still in Album 1\./);
  const deleted = operationView({
    snapshot: operation('invalidated', {
      receipts: [
        receipt('task_created'),
        { kind: 'continuation_ended', ref: 'invalidated', reason: 'the task no longer exists' }
      ]
    }),
    phase: PHASE.ENDED
  });
  assert.equal(deleted.text, 'The task was deleted, so there is nothing left to follow.');
  assert.equal(deleted.link, null);
  // A dismissed operation says nothing.
  const dismissed = operationView({ snapshot: operation('cancelled'), phase: PHASE.ENDED });
  assert.equal(dismissed.text, '');
  assert.equal(dismissed.link, null);
});

test('other task states are said in the task own words', () => {
  const started = operation('started', { task: { task_id: 'task-9', run_id: 'run-1' } });
  const say = (state, task = {}) =>
    operationView({
      snapshot: started,
      phase: PHASE.SETTLED,
      taskPresentation: state ? { state } : null,
      task,
      taskTitle: 'Bounce the stems'
    }).text;
  assert.equal(say(''), 'The task for Engineer started.');
  assert.equal(say('timed_out'), '“Bounce the stems” timed out.');
  assert.equal(say('cancelled'), '“Bounce the stems” was cancelled.');
  assert.equal(
    say('blocked'),
    '“Bounce the stems” is blocked. Open the task to see what it needs.'
  );
  // Started, then waiting again with nothing to show: said as it is.
  assert.equal(
    say('ready'),
    '“Bounce the stems” started and is waiting again. Open the task to see why.'
  );
  assert.equal(
    say('unknown'),
    'Ori cannot tell what state “Bounce the stems” is in. Open the task to check.'
  );
  // A long error is cut, not dumped into the drawer.
  const long = say('failed', { error: 'x'.repeat(400) });
  assert.ok(long.length < 220, String(long.length));
});

test('a task link is built only from a plain slug and a task identity', () => {
  assert.equal(
    operationTaskURL('album-1', 'task-9'),
    '/workspaces/album-1?panel=tasks&task=task-9'
  );
  for (const [slug, id] of [
    ['', 'task-9'],
    ['album-1', ''],
    ['../etc', 'task-9'],
    ['album-1', 'task 9'],
    ['album-1', 'task/9'],
    ['album-1', 'https://example.com'],
    ['album-1', 'x'.repeat(65)],
    ['a?b', 'task-9']
  ]) {
    assert.equal(operationTaskURL(slug, id), '', `${slug} ${id}`);
  }
});

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceExecutionController, RUN_PHASE } from './workspace-execution-controller.js';
import { PRESENTATION_STATE } from './task-presentation.js';

const flush = () => new Promise(resolve => setTimeout(resolve, 0));

/**
 * Build a controller with controllable fakes. `scripts` maps taskId -> a
 * function(callIndex) returning the task payload (or throwing) for each poll.
 */
function makeHarness(scripts = {}) {
  const timers = [];
  const fetchCalls = [];
  const callCounts = {};
  let realtimeHandler = null;
  let realtimeUnsubbed = false;

  const controller = new WorkspaceExecutionController({
    workspaceId: 'w1',
    fetchTask: async id => {
      fetchCalls.push(id);
      const n = callCounts[id] || 0;
      callCounts[id] = n + 1;
      const script = scripts[id];
      const value = typeof script === 'function' ? script(n) : script;
      if (value instanceof Error) throw value;
      return value;
    },
    subscribeRealtime: (_wid, handler) => {
      realtimeHandler = handler;
      return () => {
        realtimeHandler = null;
        realtimeUnsubbed = true;
      };
    },
    setIntervalFn: fn => {
      const t = { fn, stopped: false };
      timers.push(t);
      return t;
    },
    clearIntervalFn: t => {
      if (t) t.stopped = true;
    },
    now: () => 1000
  });

  return {
    controller,
    fetchCalls,
    liveTimers: () => timers.filter(t => !t.stopped),
    tick: async () => {
      timers.filter(t => !t.stopped).forEach(t => t.fn());
      await flush();
    },
    fireRealtime: event => {
      if (realtimeHandler) realtimeHandler(event);
    },
    wasRealtimeUnsubbed: () => realtimeUnsubbed
  };
}

const running = {
  id: 't1',
  status: 'in_progress',
  to: 'agent-a',
  updated_at: '2026-06-01T00:00:00Z'
};

test('track() starts a run and resolves presentation from the first poll', async () => {
  const h = makeHarness({ t1: running });
  const events = [];
  h.controller.subscribe(s => events.push(s));
  h.controller.track('t1');
  await flush();

  const run = h.controller.getRun('t1');
  assert.ok(run);
  assert.equal(run.presentation.state, PRESENTATION_STATE.RUNNING);
  assert.equal(run.phase, RUN_PHASE.LIVE);
  assert.equal(h.controller.getSelectedTaskId(), 't1');
  assert.ok(events.length >= 1, 'renderers are notified');
});

test('track() is idempotent — no duplicate monitor for the same task (FR65)', async () => {
  const h = makeHarness({ t1: running });
  h.controller.track('t1');
  h.controller.track('t1');
  await flush();
  assert.equal(h.liveTimers().length, 1, 'only one interval for the task');
});

test('multiple concurrent runs; selecting one does not stop the other (FR53, FR55)', async () => {
  const h = makeHarness({
    t1: running,
    t2: { id: 't2', status: 'in_progress', to: 'b', updated_at: '2026-06-01T00:00:00Z' }
  });
  h.controller.track('t1');
  h.controller.track('t2');
  await flush();

  assert.equal(h.controller.getActiveRuns().length, 2);
  assert.equal(h.controller.getSelectedTaskId(), 't2', 'latest tracked is selected');

  h.controller.select('t1');
  assert.equal(h.controller.getSelectedTaskId(), 't1');
  assert.equal(h.liveTimers().length, 2, 'both runs still monitored after switching selection');
});

test('terminal state settles: polling stops but the run stays available (FR61)', async () => {
  const h = makeHarness({ t1: { id: 't1', status: 'completed', to: 'a', result: 'ok' } });
  h.controller.track('t1');
  await flush();

  const run = h.controller.getRun('t1');
  assert.equal(run.phase, RUN_PHASE.SETTLED);
  assert.equal(h.liveTimers().length, 0, 'no active timer after terminal');
  assert.equal(h.controller.getActiveRuns().length, 0);
  assert.ok(h.controller.getRun('t1'), 'run is still retrievable for the user to act on');
});

test('needs-input settles polling but keeps the run and flags attention', async () => {
  const h = makeHarness({ t1: { id: 't1', status: 'waiting_for_choice', to: 'a' } });
  h.controller.track('t1');
  await flush();
  assert.equal(h.controller.getRun('t1').phase, RUN_PHASE.SETTLED);
  assert.equal(h.controller.getAttentionRuns().length, 1);
});

test('a failed poll enters reconnecting and retains activity, then resumes (FR58)', async () => {
  const h = makeHarness({
    t1: n => (n === 1 ? new Error('network') : running) // 1st poll ok, 2nd throws, 3rd ok
  });
  h.controller.track('t1');
  await flush(); // poll 0 -> running (LIVE), 1 activity entry
  const before = h.controller.getRun('t1').activity.length;
  assert.equal(h.controller.getRun('t1').phase, RUN_PHASE.LIVE);

  await h.tick(); // poll 1 -> throws -> reconnecting
  assert.equal(h.controller.getRun('t1').phase, RUN_PHASE.RECONNECTING);
  assert.equal(
    h.controller.getRun('t1').activity.length,
    before,
    'activity retained across reconnect'
  );

  await h.tick(); // poll 2 -> running -> live again
  assert.equal(h.controller.getRun('t1').phase, RUN_PHASE.LIVE);
});

test('activity is de-duped by stable key across repeated polls (FR58, FR127)', async () => {
  const h = makeHarness({ t1: running }); // same payload every poll
  h.controller.track('t1');
  await flush();
  await h.tick();
  await h.tick();
  const statusEntries = h.controller.getRun('t1').activity.filter(a => a.kind === 'status');
  assert.equal(statusEntries.length, 1, 'unchanged status logged once, not per poll');
});

test('a status change appends a new de-duped activity entry', async () => {
  const h = makeHarness({
    t1: n =>
      n === 0
        ? { id: 't1', status: 'in_progress', to: 'a', updated_at: '2026-06-01T00:00:00Z' }
        : {
            id: 't1',
            status: 'completed',
            to: 'a',
            updated_at: '2026-06-02T00:00:00Z',
            result: 'ok'
          }
  });
  h.controller.track('t1');
  await flush(); // running
  await h.tick(); // completed
  const states = h.controller
    .getRun('t1')
    .activity.filter(a => a.kind === 'status')
    .map(a => a.state);
  assert.deepEqual(states, [PRESENTATION_STATE.RUNNING, PRESENTATION_STATE.COMPLETED]);
});

test('realtime event for a tracked task triggers an authoritative poll; untracked is ignored', async () => {
  const h = makeHarness({ t1: running });
  h.controller.track('t1');
  await flush();
  const before = h.fetchCalls.length;

  h.fireRealtime({ data: { data: { task_id: 't1' } } });
  await flush();
  assert.equal(h.fetchCalls.length, before + 1, 'tracked task re-polled on realtime');

  h.fireRealtime({ data: { data: { task_id: 'other' } } });
  await flush();
  assert.equal(h.fetchCalls.length, before + 1, 'untracked task ignored');
});

test('untrack() removes the run and reselects an active one', async () => {
  const h = makeHarness({
    t1: running,
    t2: { id: 't2', status: 'in_progress', to: 'b', updated_at: '2026-06-01T00:00:00Z' }
  });
  h.controller.track('t1');
  h.controller.track('t2'); // selected = t2
  await flush();

  h.controller.untrack('t2');
  assert.equal(h.controller.getRun('t2'), null);
  assert.equal(h.controller.getSelectedTaskId(), 't1', 'reselects a remaining run');
});

test('dispose() stops all timers and unsubscribes realtime', async () => {
  const h = makeHarness({ t1: running, t2: running });
  h.controller.track('t1');
  h.controller.track('t2');
  await flush();
  h.controller.dispose();
  assert.equal(h.liveTimers().length, 0);
  assert.equal(h.wasRealtimeUnsubbed(), true);
  assert.equal(h.controller.getRuns().length, 0);
});

test('monitoring is independent of any modal/view — no view coupling in the API surface', () => {
  // The controller exposes no modal/collapse concept; a view collapsing cannot
  // reach in to stop a monitor. This guards the core decoupling (FR51).
  const h = makeHarness({ t1: running });
  const api = Object.getOwnPropertyNames(Object.getPrototypeOf(h.controller));
  ['track', 'untrack', 'select', 'subscribe', 'dispose'].forEach(m =>
    assert.ok(api.includes(m), `controller exposes ${m}`)
  );
  assert.ok(
    !api.some(m => /modal|collapse|hide|show/i.test(m)),
    'no modal/collapse lifecycle in the controller'
  );
});

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./map-activity-feed.js', import.meta.url), 'utf8');

// A controllable EventSource: tests push named events into the newest instance.
function makeHarness(options = {}) {
  const instances = [];
  class FakeEventSource {
    constructor(url) {
      this.url = url;
      this.closed = false;
      this.listeners = {};
      instances.push(this);
    }
    addEventListener(name, fn) {
      (this.listeners[name] = this.listeners[name] || []).push(fn);
    }
    close() {
      this.closed = true;
    }
    emit(name, data) {
      (this.listeners[name] || []).forEach(fn =>
        fn({ data: data === undefined ? undefined : JSON.stringify(data) })
      );
    }
  }

  const timers = [];
  let now = 0;
  const window = {
    EventSource: FakeEventSource,
    setTimeout(fn, delay) {
      const timer = { fn, at: now + delay, delay, cleared: false };
      timers.push(timer);
      return timer;
    },
    clearTimeout(timer) {
      if (timer) timer.cleared = true;
    },
    fetch: options.fetch || (() => Promise.resolve({ status: 200 }))
  };
  vm.runInNewContext(
    source,
    { window, console: { warn() {} }, Promise, JSON, Object, Array, Math, String },
    {
      filename: 'map-activity-feed.js'
    }
  );

  return {
    feed: window.OriMapActivityFeed,
    instances,
    timers,
    latest: () => instances[instances.length - 1],
    advance(ms) {
      now += ms;
      timers
        .filter(t => !t.cleared && !t.fired && t.at <= now)
        .forEach(t => {
          t.fired = true;
          t.fn();
        });
    },
    pendingDelays: () => timers.filter(t => !t.cleared && !t.fired).map(t => t.delay)
  };
}

const running = (overrides = {}) => ({
  activity_id: 'task:ws-1:task-1',
  kind: 'task',
  workspace_id: 'ws-1',
  agent_name: 'Theo',
  task_id: 'task-1',
  blocked: false,
  started_at: '2026-09-16T10:00:00Z',
  at: '2026-09-16T10:00:00Z',
  ...overrides
});

const flush = () => new Promise(resolve => setImmediate(resolve));

test('one EventSource is shared by every subscriber and closed by the last release', () => {
  const h = makeHarness();
  const releaseA = h.feed.subscribe(() => {});
  const releaseB = h.feed.subscribe(() => {});
  const releaseC = h.feed.subscribe(() => {});
  assert.equal(h.instances.length, 1);
  assert.equal(h.latest().url, '/api/workspace-map/activity/stream');

  releaseA();
  releaseB();
  assert.equal(h.latest().closed, false, 'still one subscriber');
  releaseC();
  releaseC();
  assert.equal(h.latest().closed, true);
  assert.equal(h.feed.subscriberCount(), 0);
});

test('a snapshot replaces the state and marks the feed connected', () => {
  const h = makeHarness();
  const changes = [];
  const connections = [];
  h.feed.subscribe({
    onChange: (id, view) => changes.push([id, view]),
    onConnection: connected => connections.push(connected)
  });
  assert.equal(h.feed.connected, false);

  h.latest().emit('snapshot', { running: [running()], parcels: [] });
  assert.equal(h.feed.connected, true);
  assert.deepEqual(connections, [true]);
  assert.equal(changes.length, 1);
  const [id, view] = changes[0];
  assert.equal(id, 'ws-1');
  assert.equal(view.count, 1);
  assert.equal(view.latest.agent_name, 'Theo');
  assert.equal(view.lastEvent, null);

  // A second snapshot without that run drops it — and tells the listener.
  changes.length = 0;
  h.latest().emit('snapshot', { running: [], parcels: [] });
  assert.equal(changes.length, 1);
  assert.equal(changes[0][1].count, 0);
});

test('activity events patch the state and carry the event', () => {
  const h = makeHarness();
  const views = [];
  h.feed.subscribe((id, view) => views.push(view));
  h.latest().emit('snapshot', { running: [], parcels: [] });

  h.latest().emit('activity', { ...running(), phase: 'started' });
  assert.equal(views.at(-1).count, 1);
  assert.equal(views.at(-1).lastEvent.phase, 'started');

  h.latest().emit('activity', {
    ...running({ at: '2026-09-16T10:00:02Z' }),
    phase: 'step',
    step: { type: 'tool_call', tool_name: 'web_search' }
  });
  assert.equal(views.at(-1).latest.step.tool_name, 'web_search');

  h.latest().emit('activity', { ...running(), phase: 'blocked' });
  assert.equal(views.at(-1).blocked, true);

  h.latest().emit('activity', { ...running(), phase: 'finished', outcome: 'succeeded' });
  assert.equal(views.at(-1).count, 0);
  assert.equal(views.at(-1).lastEvent.outcome, 'succeeded');
});

test('several runs in one workspace report the newest first', () => {
  const h = makeHarness();
  h.feed.subscribe(() => {});
  h.latest().emit('snapshot', {
    running: [
      running({ activity_id: 'task:ws-1:a', agent_name: 'Ada', at: '2026-09-16T10:00:01Z' }),
      running({ activity_id: 'task:ws-1:b', agent_name: 'Theo', at: '2026-09-16T10:00:05Z' })
    ],
    parcels: []
  });
  const view = h.feed.viewFor('ws-1');
  assert.equal(view.count, 2);
  assert.equal(view.latest.agent_name, 'Theo');
});

test('newest-first compares instants, not timestamp text', () => {
  const h = makeHarness();
  h.feed.subscribe(() => {});
  h.latest().emit('snapshot', {
    running: [
      running({ activity_id: 'task:ws-1:a', agent_name: 'Whole', at: '2026-09-16T10:00:00Z' }),
      running({ activity_id: 'task:ws-1:b', agent_name: 'Later', at: '2026-09-16T10:00:00.5Z' })
    ],
    parcels: []
  });
  assert.equal(h.feed.viewFor('ws-1').latest.agent_name, 'Later');
});

test('a drop clears running work, keeps parcels, and reconnects with backoff', async () => {
  const h = makeHarness();
  const connections = [];
  h.feed.subscribe({ onChange() {}, onConnection: c => connections.push(c) });
  h.latest().emit('snapshot', {
    running: [running()],
    parcels: [
      {
        id: 'p1',
        workspace_id: 'ws-1',
        kind: 'task',
        title: 'T',
        produced_at: '2026-09-16T10:00:00Z'
      }
    ]
  });

  h.latest().emit('error');
  assert.equal(h.feed.connected, false);
  assert.deepEqual(connections, [true, false]);
  const view = h.feed.viewFor('ws-1');
  assert.equal(view.count, 0, 'nothing stays lit without a live feed');
  assert.equal(view.parcels.length, 1, 'parcels keep their last known count');
  assert.equal(h.latest().closed, true);

  // Each failed attempt waits longer: 1s, 2s, 5s, then 15s.
  const delays = [];
  for (let i = 0; i < 5; i++) {
    delays.push(h.pendingDelays()[0]);
    h.advance(h.pendingDelays()[0]);
    h.latest().emit('error'); // fails before any snapshot → probe → reconnect
    await flush();
  }
  assert.deepEqual(delays, [1000, 2000, 5000, 15000, 15000]);
  assert.equal(h.instances.length, 6);

  // A snapshot resets the backoff.
  h.advance(h.pendingDelays()[0]);
  h.latest().emit('snapshot', { running: [], parcels: [] });
  h.latest().emit('error');
  assert.deepEqual(h.pendingDelays(), [1000]);
});

test('a 404 from the snapshot probe means the feature is off: no more reconnects', async () => {
  const h = makeHarness({ fetch: () => Promise.resolve({ status: 404 }) });
  h.feed.subscribe(() => {});
  h.latest().emit('error');
  await flush();
  assert.deepEqual(h.pendingDelays(), []);
  assert.equal(h.instances.length, 1);
});

test('a late subscriber receives the current state immediately', () => {
  const h = makeHarness();
  h.feed.subscribe(() => {});
  h.latest().emit('snapshot', { running: [running()], parcels: [] });

  const seen = [];
  let told = null;
  h.feed.subscribe({
    onChange: (id, view) => seen.push([id, view.count]),
    onConnection: c => (told = c)
  });
  assert.equal(told, true);
  assert.deepEqual(seen, [['ws-1', 1]]);
  assert.equal(h.instances.length, 1);
});

test('events before the snapshot and from a closed source are ignored', () => {
  const h = makeHarness();
  const views = [];
  const release = h.feed.subscribe((id, view) => views.push(view));
  const first = h.latest();
  first.emit('activity', { ...running(), phase: 'started' });
  assert.equal(views.length, 0);

  release();
  first.emit('snapshot', { running: [running()], parcels: [] });
  assert.equal(views.length, 0);
  assert.equal(h.feed.connected, false);
});

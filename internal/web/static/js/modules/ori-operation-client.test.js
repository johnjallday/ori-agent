import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  OperationClient,
  PHASE,
  AUTO_WINDOW_MS,
  TAB_KEY,
  WATCH_KEY,
  OPERATION_KEY,
  pendingCommands
} from './ori-operation-client.js';
import { OUTCOME } from './ori-operation-targets.js';

function memoryStorage(initial = {}) {
  const data = new Map(Object.entries(initial));
  return {
    getItem: key => (data.has(key) ? data.get(key) : null),
    setItem: (key, value) => data.set(key, String(value)),
    removeItem: key => data.delete(key),
    dump: () => Object.fromEntries(data)
  };
}

function refusal(status, code) {
  const error = new Error(code);
  error.status = status;
  error.data = { code };
  return error;
}

/**
 * A tiny in-memory operation server with the real protocol's rules: one lease
 * holder sees pending commands, acknowledgements need the current revision,
 * and a settled command repeats harmlessly.
 */
function makeServer({ agent = true, watch = true, tab = 'tab-a' } = {}) {
  const op = {
    id: 'op-1',
    status: 'requested',
    revision: 1,
    tab,
    watch,
    context: { workspace_id: 'ws-album', workspace_slug: 'album-1', workspace_name: 'Album 1' },
    commands: [
      {
        id: 'c1',
        sequence: 1,
        action: 'open_workspace',
        status: 'pending',
        workspace_id: 'ws-album',
        workspace_slug: 'album-1'
      }
    ]
  };
  if (agent) {
    op.context.agent_name = 'Mixer';
    op.commands.push({
      id: 'c2',
      sequence: 2,
      action: 'select_agent',
      status: 'pending',
      workspace_id: 'ws-album',
      agent_name: 'Mixer'
    });
  }
  const calls = [];
  const snapshotFor = tabId => {
    const watching = op.watch && op.tab === tabId;
    const pending = op.commands.filter(c => c.status === 'pending');
    return {
      id: op.id,
      status: op.status,
      revision: op.revision,
      watching,
      pending: pending.length,
      context: { ...op.context },
      commands: op.commands.filter(c => c.status !== 'pending' || watching).map(c => ({ ...c }))
    };
  };
  const server = {
    op,
    calls,
    fail: null,
    gone: false,
    snapshotFor,
    request: async (method, path, body) => {
      calls.push({ method, path, body });
      if (server.fail) throw server.fail;
      if (server.gone) throw refusal(404, 'operation_not_found');
      if (method === 'GET') {
        const tabId = decodeURIComponent(path.split('tab=')[1] || '');
        return { operation: snapshotFor(tabId) };
      }
      if (['cancelled', 'expired', 'invalidated'].includes(op.status)) {
        throw refusal(
          op.status === 'cancelled' ? 409 : 410,
          op.status === 'cancelled' ? 'operation_ended' : 'operation_' + op.status
        );
      }
      if (path.endsWith('/watch')) {
        const holder = op.tab === body.tab_id;
        if (!holder && op.tab && !(body.watch && body.adopt))
          throw refusal(409, 'operation_tab_mismatch');
        if (body.revision !== op.revision) throw refusal(409, 'operation_conflict');
        op.tab = body.tab_id;
        op.watch = body.watch;
        op.revision += 1;
        return { operation: snapshotFor(body.tab_id) };
      }
      if (path.endsWith('/cancel')) {
        op.status = 'cancelled';
        op.commands.forEach(c => {
          if (c.status === 'pending') c.status = 'superseded';
        });
        op.revision += 1;
        return { operation: snapshotFor(body.tab_id) };
      }
      if (path.endsWith('/ack')) {
        if (!op.watch || op.tab !== body.tab_id) throw refusal(409, 'operation_tab_mismatch');
        const command = op.commands.find(c => c.id === body.command_id);
        if (!command) throw refusal(400, 'operation_invalid');
        if (command.status !== 'pending') {
          if (command.status === body.outcome) return { operation: snapshotFor(body.tab_id) };
          throw refusal(409, 'operation_conflict');
        }
        if (body.revision !== op.revision) throw refusal(409, 'operation_conflict');
        command.status = body.outcome;
        if (body.outcome === 'unavailable') {
          command.reason = body.reason;
          op.commands.forEach(c => {
            if (c.sequence > command.sequence && c.status === 'pending') c.status = 'superseded';
          });
        }
        op.revision += 1;
        return { operation: snapshotFor(body.tab_id) };
      }
      throw refusal(400, 'operation_invalid');
    }
  };
  return server;
}

/** Adapters scripted per action; records what it was asked to do. */
function makeTargets(script) {
  const executed = [];
  return {
    executed,
    execute: async (command, options) => {
      executed.push({ action: command.action, allowNavigate: Boolean(options.allowNavigate) });
      const step = script[command.action];
      return typeof step === 'function' ? step(command, options) : step;
    }
  };
}

function makeClient({
  server,
  targets,
  storage = memoryStorage({ [TAB_KEY]: 'tab-a' }),
  clock = { now: 1000 }
} = {}) {
  const navigations = [];
  const client = new OperationClient({
    request: server.request,
    storage,
    targets,
    navigate: url => navigations.push(url),
    now: () => clock.now,
    newId: () => 'fresh-id',
    wait: async () => {}
  });
  return { client, navigations, storage, clock };
}

const acked = observed => ({ outcome: OUTCOME.ACKNOWLEDGED, observed });

test('the tab identity is stable, well formed and renewable', () => {
  const storage = memoryStorage();
  const { client } = makeClient({ server: makeServer(), targets: makeTargets({}), storage });
  assert.equal(client.tabId(), 'fresh-id');
  assert.equal(client.tabId(), 'fresh-id');
  assert.equal(storage.getItem(TAB_KEY), 'fresh-id');
  storage.setItem(TAB_KEY, 'not a tab id<script>');
  assert.equal(client.tabId(), 'fresh-id', 'a malformed stored identity is replaced');
  assert.deepEqual(client.askRef(), { tab_id: 'fresh-id', watch: false });
  client.setWatchPreference(true);
  assert.equal(storage.getItem(WATCH_KEY), '1');
  assert.deepEqual(client.askRef(), { tab_id: 'fresh-id', watch: true });
});

test('a fresh watched operation navigates once and reports nothing before arrival', async () => {
  const server = makeServer();
  const targets = makeTargets({
    open_workspace: { outcome: OUTCOME.NAVIGATING, url: '/workspaces/album-1' }
  });
  const { client, navigations, storage } = makeClient({ server, targets });
  await client.adopt(server.snapshotFor('tab-a'));
  assert.deepEqual(navigations, ['/workspaces/album-1']);
  assert.equal(client.phase, PHASE.NAVIGATING);
  assert.deepEqual(targets.executed, [{ action: 'open_workspace', allowNavigate: true }]);
  // Queued navigation is not acknowledged navigation.
  assert.equal(server.calls.filter(call => call.path.endsWith('/ack')).length, 0);
  assert.equal(server.op.commands[0].status, 'pending');
  const pointer = JSON.parse(storage.getItem(OPERATION_KEY));
  assert.equal(pointer.id, 'op-1');
  assert.equal(pointer.auto_until, 1000 + AUTO_WINDOW_MS);
});

test('the arriving page reads first, reports arrival, then selects the agent', async () => {
  const server = makeServer();
  const element = { id: 'mixer-control' };
  const targets = makeTargets({
    open_workspace: acked({ path: '/workspaces/album-1' }),
    select_agent: { ...acked({ agent_key: 'Mixer' }), element }
  });
  const storage = memoryStorage({
    [TAB_KEY]: 'tab-a',
    [OPERATION_KEY]: JSON.stringify({ id: 'op-1', auto_until: 1000 + AUTO_WINDOW_MS })
  });
  const { client, navigations } = makeClient({ server, targets, storage, clock: { now: 5000 } });
  await client.restore();
  assert.equal(server.calls[0].method, 'GET', 'the snapshot is read before any command runs');
  // A page load never navigates by itself.
  assert.deepEqual(targets.executed, [
    { action: 'open_workspace', allowNavigate: false },
    { action: 'select_agent', allowNavigate: false }
  ]);
  assert.deepEqual(navigations, []);
  assert.equal(client.phase, PHASE.SETTLED);
  assert.deepEqual(
    server.op.commands.map(c => c.status),
    ['acknowledged', 'acknowledged']
  );
  assert.equal(client.highlight.element, element);
  assert.equal(client.highlight.commandId, 'c2');
  const acks = server.calls.filter(call => call.path.endsWith('/ack')).map(call => call.body);
  assert.deepEqual(
    acks.map(body => [body.command_id, body.revision, body.tab_id, body.outcome]),
    [
      ['c1', 1, 'tab-a', 'acknowledged'],
      ['c2', 2, 'tab-a', 'acknowledged']
    ]
  );
});

test('a reload after the window, or somewhere else, waits for Resume', async () => {
  const late = makeServer();
  const lateTargets = makeTargets({ open_workspace: acked({ path: '/workspaces/album-1' }) });
  const stale = memoryStorage({
    [TAB_KEY]: 'tab-a',
    [OPERATION_KEY]: JSON.stringify({ id: 'op-1', auto_until: 2000 })
  });
  const first = makeClient({
    server: late,
    targets: lateTargets,
    storage: stale,
    clock: { now: 9000 }
  });
  await first.client.restore();
  assert.equal(first.client.phase, PHASE.PAUSED);
  assert.equal(first.client.pauseReason, 'reloaded');
  assert.deepEqual(lateTargets.executed, [], 'an old request is not carried out by a page load');

  const moved = makeServer();
  const movedTargets = makeTargets({
    open_workspace: (_command, options) =>
      options.allowNavigate
        ? { outcome: OUTCOME.NAVIGATING, url: '/workspaces/album-1' }
        : { outcome: OUTCOME.YIELD, reason: 'moved_elsewhere' }
  });
  const fresh = memoryStorage({
    [TAB_KEY]: 'tab-a',
    [OPERATION_KEY]: JSON.stringify({ id: 'op-1', auto_until: 99999 })
  });
  const second = makeClient({
    server: moved,
    targets: movedTargets,
    storage: fresh,
    clock: { now: 5000 }
  });
  await second.client.restore();
  assert.equal(second.client.phase, PHASE.PAUSED);
  assert.equal(second.client.pauseReason, 'moved_elsewhere');
  assert.deepEqual(second.navigations, []);
  // Resume is the user's explicit choice, so it may navigate.
  await second.client.resume();
  assert.deepEqual(second.navigations, ['/workspaces/album-1']);
});

test('unsaved work is not overwritten: the step waits', async () => {
  const server = makeServer();
  const targets = makeTargets({
    open_workspace: { outcome: OUTCOME.YIELD, reason: 'unsaved_work' }
  });
  const { client, navigations } = makeClient({ server, targets });
  await client.adopt(server.snapshotFor('tab-a'));
  assert.equal(client.phase, PHASE.PAUSED);
  assert.equal(client.pauseReason, 'unsaved_work');
  assert.deepEqual(navigations, []);
  assert.equal(server.op.commands[0].status, 'pending', 'nothing was reported or lost');
});

test('with Watch off nothing is delivered until the user opens it here', async () => {
  const server = makeServer({ watch: false });
  const targets = makeTargets({
    open_workspace: { outcome: OUTCOME.NAVIGATING, url: '/workspaces/album-1' }
  });
  const { client, navigations } = makeClient({ server, targets });
  await client.adopt(server.snapshotFor('tab-a'));
  assert.equal(client.phase, PHASE.WAITING);
  assert.deepEqual(targets.executed, []);
  assert.deepEqual(navigations, []);
  await client.resume();
  assert.equal(server.op.watch, true);
  assert.deepEqual(navigations, ['/workspaces/album-1']);
});

test('a second tab gets no commands and moves only by explicit adoption', async () => {
  const server = makeServer();
  const targets = makeTargets({
    open_workspace: { outcome: OUTCOME.NAVIGATING, url: '/workspaces/album-1' }
  });
  const storage = memoryStorage({
    [TAB_KEY]: 'tab-b',
    [OPERATION_KEY]: JSON.stringify({ id: 'op-1', auto_until: 99999 })
  });
  const { client, navigations } = makeClient({ server, targets, storage, clock: { now: 5000 } });
  await client.restore();
  assert.equal(client.phase, PHASE.WAITING);
  assert.deepEqual(pendingCommands(client.snapshot), []);
  assert.deepEqual(targets.executed, []);
  assert.deepEqual(navigations, []);
  await client.resume();
  assert.equal(server.op.tab, 'tab-b', 'adoption was explicit');
  assert.deepEqual(navigations, ['/workspaces/album-1']);
});

test('a copied tab gives up its identity and so its claim', async () => {
  const server = makeServer();
  const storage = memoryStorage({ [TAB_KEY]: 'tab-a' });
  const { client } = makeClient({ server, targets: makeTargets({}), storage });
  const posted = [];
  const channel = { postMessage: message => posted.push(message) };
  client.guardTab(channel);
  assert.deepEqual(posted[0].type, 'hello');
  // Another tab with the same identity announces itself: this one answers.
  channel.onmessage({ data: { type: 'hello', tab: 'tab-a', nonce: 'other' } });
  assert.deepEqual(posted[1], { type: 'taken', tab: 'tab-a', nonce: posted[0].nonce });
  assert.equal(client.tabId(), 'tab-a', 'the original keeps its identity');
  // This tab is told its identity is taken: it takes a new one.
  channel.onmessage({ data: { type: 'taken', tab: 'tab-a', nonce: 'other' } });
  assert.equal(client.tabId(), 'fresh-id');
  // Its own echo and other tabs' identities are ignored.
  channel.onmessage({ data: { type: 'taken', tab: 'fresh-id', nonce: posted[0].nonce } });
  channel.onmessage({ data: { type: 'taken', tab: 'someone-else', nonce: 'x' } });
  assert.equal(client.tabId(), 'fresh-id');
});

test('a missing target is reported and the later steps stop', async () => {
  const server = makeServer();
  const targets = makeTargets({
    open_workspace: acked({ path: '/workspaces/album-1' }),
    select_agent: {
      outcome: OUTCOME.UNAVAILABLE,
      reason: "Mixer's control is not shown in this view."
    }
  });
  const { client } = makeClient({ server, targets });
  await client.adopt(server.snapshotFor('tab-a'));
  assert.equal(client.phase, PHASE.SETTLED);
  assert.equal(server.op.commands[1].status, 'unavailable');
  assert.equal(client.highlight, null);
});

test('an unsupported action is refused without reaching an adapter', async () => {
  const server = makeServer({ agent: false });
  server.op.commands[0].action = 'run_script';
  const targets = makeTargets({});
  const { client } = makeClient({ server, targets });
  await client.adopt(server.snapshotFor('tab-a'));
  assert.deepEqual(targets.executed, []);
  assert.equal(server.op.commands[0].status, 'unavailable');
});

test('a stale revision is re-read a bounded number of times, never forced', async () => {
  const server = makeServer({ agent: false });
  const targets = makeTargets({ open_workspace: acked({ path: '/workspaces/album-1' }) });
  const { client } = makeClient({ server, targets });
  const stale = server.snapshotFor('tab-a');
  server.op.revision = 7; // something else changed the operation meanwhile
  await client.adopt(stale);
  assert.equal(server.op.commands[0].status, 'acknowledged', 'it re-read and then reported');
  assert.equal(client.snapshot.revision, 8);

  const stuck = makeServer({ agent: false });
  const original = stuck.request;
  stuck.request = async (method, path, body) => {
    if (path.endsWith('/ack')) {
      stuck.calls.push({ method, path, body });
      throw refusal(409, 'operation_conflict');
    }
    return original(method, path, body);
  };
  const looping = makeClient({
    server: stuck,
    targets: makeTargets({ open_workspace: acked({ path: '/workspaces/album-1' }) })
  });
  await looping.client.adopt(stuck.snapshotFor('tab-a'));
  // It gives up after a few tries and waits for the user, with nothing lost.
  assert.equal(looping.client.phase, PHASE.PAUSED);
  assert.equal(looping.client.pauseReason, 'conflict');
  assert.equal(stuck.calls.filter(call => call.path.endsWith('/ack')).length, 3);
  assert.equal(stuck.op.commands[0].status, 'pending');
});

test('ended and vanished operations stop delivery and say so', async () => {
  const expired = makeServer();
  const targets = makeTargets({ open_workspace: acked({ path: '/workspaces/album-1' }) });
  const one = makeClient({ server: expired, targets });
  const snapshot = expired.snapshotFor('tab-a');
  expired.op.status = 'expired';
  expired.op.commands.forEach(c => (c.status = 'superseded'));
  await one.client.adopt(snapshot);
  assert.equal(one.client.phase, PHASE.ENDED);
  assert.equal(one.client.snapshot.status, 'expired');

  const gone = makeServer();
  const storage = memoryStorage({
    [TAB_KEY]: 'tab-a',
    [OPERATION_KEY]: JSON.stringify({ id: 'op-1', auto_until: 99999 })
  });
  gone.gone = true;
  const two = makeClient({ server: gone, targets, storage });
  await two.client.restore();
  assert.equal(two.client.phase, PHASE.IDLE);
  assert.equal(storage.getItem(OPERATION_KEY), null, 'a deleted operation is forgotten');

  const offline = makeServer();
  const kept = memoryStorage({
    [TAB_KEY]: 'tab-a',
    [OPERATION_KEY]: JSON.stringify({ id: 'op-1', auto_until: 99999 })
  });
  offline.fail = Object.assign(new Error('network'), { status: 0, data: null });
  const three = makeClient({ server: offline, targets, storage: kept });
  await three.client.restore();
  assert.equal(three.client.phase, PHASE.PAUSED);
  assert.equal(three.client.pauseReason, 'unreachable');
  assert.notEqual(
    kept.getItem(OPERATION_KEY),
    null,
    'an unreachable server is not a deleted operation'
  );
});

test('Watch off and Dismiss cancel nothing but this tab following', async () => {
  const server = makeServer();
  const targets = makeTargets({
    open_workspace: { outcome: OUTCOME.YIELD, reason: 'unsaved_work' }
  });
  const { client, storage } = makeClient({ server, targets });
  await client.adopt(server.snapshotFor('tab-a'));
  assert.equal(await client.setWatching(false), true);
  assert.equal(client.phase, PHASE.WAITING);
  assert.equal(server.op.status, 'requested');
  await client.dismiss();
  assert.equal(client.phase, PHASE.IDLE);
  assert.equal(server.op.status, 'cancelled');
  assert.equal(storage.getItem(OPERATION_KEY), null);
});

test('a tab with no operation makes no request at all', async () => {
  const server = makeServer();
  const { client } = makeClient({ server, targets: makeTargets({}) });
  await client.restore();
  await client.resume();
  assert.deepEqual(server.calls, []);
  assert.equal(client.phase, PHASE.IDLE);
});

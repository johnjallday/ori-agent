import test from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView } from './workspace-command.js';
import { workspaceUpdates } from './workspace-updates.js';
import { assistantPanelOpenIntent } from './personal-assistant-panel.js';

const welcomeQuery =
  '?mode=map&update=welcome&update_workspace=ws&update_session=session&update_lead=lead&update_message=message';
const reviewQuery = '?mode=map&update=review&update_workspace=ws';
const source = () => ({
  state: 'ready',
  data: {
    workspace_id: 'ws',
    welcome: {
      state: 'ready',
      session_id: 'session',
      agent_instance_id: 'lead',
      messages: [{ message_id: 'message', revision: 1 }]
    },
    review: { state: 'ready', root_id: 'root', batch_id: 'current' }
  }
});
function setup(t, search) {
  const saved = {
    window: globalThis.window,
    document: globalThis.document,
    localStorage: globalThis.localStorage,
    refresh: workspaceUpdates.refresh
  };
  const routes = [];
  const opened = [];
  let reads = 0;
  globalThis.document = { readyState: 'complete', getElementById: () => null };
  globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
  globalThis.window = {
    location: { search, pathname: '/workspaces/desk', hash: '#unchanged' },
    history: {
      state: { keep: 'state' },
      replaceState(state, _title, route) {
        routes.push({ state, route });
      }
    }
  };
  workspaceUpdates.refresh = async id => {
    reads++;
    assert.equal(id, 'ws');
    return source();
  };
  const command = new WorkspaceCommandView(null);
  command.page = { workspaceId: 'ws', workspaceSlug: 'desk', tasks: [] };
  command.active = true;
  command.render = () => {};
  command.openUpdateReview = async () => {
    opened.push('review');
    return true;
  };
  command.openUpdateMessage = async (_trigger, expected) => {
    opened.push(expected);
    return true;
  };
  t.after(() => {
    globalThis.window = saved.window;
    globalThis.document = saved.document;
    globalThis.localStorage = saved.localStorage;
    workspaceUpdates.refresh = saved.refresh;
  });
  return { command, routes, opened, reads: () => reads };
}

test('review/message arrivals suppress ordinary drawer restoration, including malformed intent', () => {
  for (const query of [
    reviewQuery,
    welcomeQuery,
    '?update=apply',
    '?update_message=unknown',
    reviewQuery + '&panel=assistant'
  ]) {
    const intent = assistantPanelOpenIntent(query, false, '/workspaces/desk');
    assert.equal(intent.requestedOpen, false);
    assert.equal(intent.restoreAllowed, false);
    assert.equal(intent.consumes, '');
  }
  assert.equal(
    assistantPanelOpenIntent('?mode=map', false, '/workspaces/desk').restoreAllowed,
    true
  );
});

test('arrival consumes once, preserves mode/hash/history and passes exact identity to ordinary guarded navigation', async t => {
  const f = setup(t, welcomeQuery);
  await f.command.consumeUpdateArrival();
  assert.equal(f.reads(), 1);
  assert.deepEqual(f.opened, [
    {
      kind: 'welcome',
      workspaceId: 'ws',
      sessionId: 'session',
      leadId: 'lead',
      messageId: 'message'
    }
  ]);
  assert.deepEqual(f.routes, [
    { state: { keep: 'state' }, route: '/workspaces/desk?mode=map#unchanged' }
  ]);
  // Even a restored stale address cannot replay the consumed arrival in this
  // live page (the history entry itself was replaced above).
  await f.command.consumeUpdateArrival();
  assert.equal(f.opened.length, 1);
});

test('Review arrival opens only current Review, never the message drawer', async t => {
  const f = setup(t, reviewQuery);
  await f.command.consumeUpdateArrival();
  assert.deepEqual(f.opened, ['review']);
  assert.equal(f.reads(), 1);
});

test('foreign or malformed arrival never queries its supplied workspace', async t => {
  const f = setup(t, '?update=review&update_workspace=other');
  await f.command.consumeUpdateArrival();
  assert.equal(f.reads(), 0);
  assert.deepEqual(f.opened, []);
  assert.match(f.command.workNotice, /unavailable/);
});

test('changed saved destination is honest unavailability, not another conversation', async t => {
  const f = setup(t, welcomeQuery);
  workspaceUpdates.refresh = async () => {
    const view = source();
    view.data.welcome.session_id = 'replacement';
    return view;
  };
  await f.command.consumeUpdateArrival();
  assert.deepEqual(f.opened, []);
  assert.match(f.command.workNotice, /destination changed/);
});

test('leaving during a canonical read cannot open the old arrival in a different workspace', async t => {
  const f = setup(t, reviewQuery);
  let resolve;
  workspaceUpdates.refresh = () =>
    new Promise(done => {
      resolve = done;
    });
  const pending = f.command.consumeUpdateArrival();
  f.command.page.workspaceId = 'other';
  resolve(source());
  await pending;
  assert.deepEqual(f.opened, []);
});

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { MyNotes, latestNotesRequest, myNotesConnection, myNotesLanding } from './my-notes.js';
import { LOOK_POLL_LIMIT, LOOK_POLL_MS } from './personal-assistant-second-brain.js';

test('arrival lands in the notebook only when a source is connected and reachable', () => {
  const created = {
    status: 'connected',
    browse_ref: 'r'.repeat(64),
    source: { id: 's', name: 'My Notes', origin: 'created_by_ori' }
  };
  const external = { ...created, source: { ...created.source, origin: 'connected' } };
  assert.deepEqual(myNotesLanding({ status: 'not_connected', setup_required: 'assistant' }), {
    kind: 'setup'
  });
  assert.deepEqual(myNotesLanding({ status: 'not_connected', setup_required: 'hq' }), {
    kind: 'setup'
  });
  assert.deepEqual(myNotesLanding({ status: 'not_connected' }, 'inbox'), { kind: 'setup' });
  assert.deepEqual(myNotesLanding({ status: 'missing', source: created.source }), {
    kind: 'repair'
  });
  assert.deepEqual(myNotesLanding(undefined, 'loops'), { kind: 'retry' });
  assert.deepEqual(myNotesLanding({ ...created, browse_ref: '' }), { kind: 'connected' });
  assert.deepEqual(myNotesLanding(created), { kind: 'browse', folder: ['Inbox'], tools: null });
  assert.deepEqual(myNotesLanding(external), { kind: 'browse', folder: [], tools: null });
  // Explicit destinations still open their views beside the browser.
  assert.equal(myNotesLanding(created, 'inbox').tools, 'inbox');
  assert.equal(myNotesLanding(created, 'loops').tools, 'loops');
  assert.equal(myNotesLanding(external, 'loops').tools, 'loops');
  assert.equal(myNotesLanding(external, 'inbox').tools, null, 'a connected folder has no Inbox');
  assert.equal(myNotesLanding(created, 'settings').tools, null);
});

test('notes setup prerequisites are not presented as connection errors or empty content', () => {
  assert.equal(
    myNotesConnection({ status: 'not_connected', setup_required: 'assistant' }).href,
    '/?quest=meet-assistant'
  );
  assert.equal(
    myNotesConnection({ status: 'not_connected', setup_required: 'hq' }).href,
    '/?quest=build-hq'
  );
  assert.equal(myNotesConnection({ status: 'not_connected' }).manage, 'Set up notes…');
  assert.equal(myNotesConnection().retry, true);
  assert.equal(myNotesConnection({ status: 'connected', source: {} }).retry, true);
});

test('missing and connected sources have distinct actions and modes', () => {
  const source = { id: 'notebook', name: 'Field notes', origin: 'connected' };
  assert.match(myNotesConnection({ status: 'connected', source }).detail, /read-only/);
  assert.match(
    myNotesConnection({ status: 'connected', source: { ...source, origin: 'created_by_ori' } })
      .detail,
    /capture to Inbox/
  );
  const missing = myNotesConnection({ status: 'missing', source });
  assert.equal(missing.connected, undefined);
  assert.equal(missing.manage, 'Choose again…');
});

test('a slow earlier request and a response after departure cannot repaint', () => {
  const requests = latestNotesRequest();
  const initial = requests.begin();
  const retry = requests.begin();
  assert.equal(requests.isCurrent(initial), false);
  assert.equal(requests.isCurrent(retry), true);
  requests.cancel();
  assert.equal(requests.isCurrent(retry), false);
  assert.equal(requests.isCurrent(requests.begin()), true);
});

function notesController(panel) {
  const controller = Object.create(MyNotes.prototype);
  controller.panel = panel;
  controller.paintTools = () => {};
  return controller;
}

test('triage stays explicit and shows the resulting Look', async t => {
  const panel = { look: { look_id: 'look-1' } };
  const controller = notesController(panel);
  const calls = [];
  const updated = { state: 'finished', look_id: 'look-1', result: { id: 'after' } };
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    calls.push([url, JSON.parse(options.body)]);
    return { ok: true, json: async () => ({ look: updated }) };
  });
  await controller.act('notes-item-done', 'item-1');
  assert.equal(calls[0][0], '/api/personal-assistant/second-brain/look/look-1/items/item-1');
  assert.equal(calls[0][1].action, 'done');
  assert.match(calls[0][1].request_id, /^notes-/);
  assert.equal(panel.look, updated);
  await controller.act('notes-show-all');
  assert.equal(panel.showAll, true);
});

test('Look again shows the Retry-After guidance inside the limit', async t => {
  const panel = {
    look: { result: { finished_at: new Date(Date.now() - 4 * 60000).toISOString() } }
  };
  const controller = notesController(panel);
  t.mock.method(globalThis, 'fetch', async () => ({
    ok: false,
    status: 429,
    headers: { get: () => '361' },
    json: async () => ({})
  }));
  await controller.act('notes-look-again');
  assert.equal(panel.lookMessage, 'Looked 4 minutes ago. Try again in 7 minutes.');
  assert.equal(panel.lookBusy, false);
});

test('the running Look stops polling at the bounded limit', async t => {
  const look = { state: 'running', look_id: 'look-2' };
  const panel = { look };
  const controller = notesController(panel);
  let pending;
  const fetcher = t.mock.method(globalThis, 'fetch', async () => ({
    ok: true,
    json: async () => ({ look })
  }));
  t.mock.method(globalThis, 'setTimeout', (fn, delay) => {
    assert.equal(delay, LOOK_POLL_MS);
    pending = fn;
    return undefined;
  });
  controller.pollLook(panel);
  for (let i = 0; i < LOOK_POLL_LIMIT; i++) {
    const fn = pending;
    pending = null;
    await fn();
  }
  assert.equal(fetcher.mock.callCount(), LOOK_POLL_LIMIT);
  assert.equal(pending, null);
  assert.equal(panel.lookMessage, 'Still reading. Check back in a moment.');
});

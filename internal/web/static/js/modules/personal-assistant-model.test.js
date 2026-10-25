import test from 'node:test';
import assert from 'node:assert/strict';
import {
  modelDescription,
  modelEndpoint,
  modelSettingsHref,
  createModelController,
  bindModelLifecycle
} from './personal-assistant-model.js';

const configured = (model = 'sonnet') => ({
  state: 'configured',
  source: 'default',
  provider: 'claude_code',
  model,
  settings_href: '/settings#system-model'
});
const tick = () => new Promise(resolve => setImmediate(resolve));

function fixture(load) {
  const frames = [];
  const controller = createModelController({ load, render: frame => frames.push(frame) });
  controller.setContext({ kind: 'personal', sessionId: '' });
  return { controller, frames, last: () => frames.at(-1) };
}

test('model display preserves aliases and never advertises an unavailable saved value', () => {
  assert.equal(modelDescription(configured()), 'claude_code / sonnet');
  for (const state of ['unconfigured', 'unavailable', 'unknown']) {
    assert.equal(
      modelDescription({ ...configured('old'), state }),
      state === 'unconfigured' ? 'not configured' : state
    );
  }
  assert.equal(modelDescription(null), 'unknown');
  assert.equal(modelDescription({ state: 'configured' }), 'unknown');
});

test('context uses server-side selection and settings links have a narrow allowlist', () => {
  assert.equal(
    modelEndpoint({ kind: 'personal', sessionId: 'a b' }),
    '/api/home-assistant/model?conversation_id=a+b'
  );
  assert.equal(
    modelEndpoint({
      kind: 'workspace_entry',
      workspaceId: 'ws',
      name: 'A&B',
      sessionId: 'session'
    }),
    '/api/chat/model?studio_id=ws&agent_name=A%26B&session_id=session'
  );
  assert.equal(modelSettingsHref(configured()), '/settings#system-model');
  assert.equal(
    modelSettingsHref({ source: 'agent', settings_href: '/workspaces/ws?editAgentModel=A%26B' }),
    '/workspaces/ws?editAgentModel=A%26B'
  );
  for (const href of [
    'javascript:alert(1)',
    '//example.com',
    '/settings',
    '/workspaces/ws?editAgentModel=A&bad=1'
  ]) {
    assert.equal(modelSettingsHref({ source: 'agent', settings_href: href }), '');
  }
});

test('rapid recipient switches clear immediately and discard late reads', async () => {
  const pending = [];
  const f = fixture(
    (context, { signal }) => new Promise(resolve => pending.push({ context, signal, resolve }))
  );
  const first = f.controller.open();
  f.controller.setContext({ kind: 'workspace_entry', workspaceId: 'new', name: 'Commander' });
  assert.equal(f.last().selection, null);
  const second = f.controller.refresh();
  assert.equal(pending[0].signal.aborted, true);
  pending[1].resolve({ ...configured('entry-model'), source: 'agent' });
  await second;
  pending[0].resolve(configured('old-personal-model'));
  await first;
  assert.equal(f.last().selection.model, 'entry-model');
});

test('failed reads and clearing settings remove old values and destinations', async () => {
  let response = configured();
  const f = fixture(async () => {
    if (response instanceof Error) throw response;
    return response;
  });
  await f.controller.open();
  response = new Error('offline');
  await f.controller.refresh();
  assert.equal(f.last().selection, null);
  assert.equal(modelSettingsHref(f.last().selection), '');
  response = { state: 'unconfigured', source: 'default', settings_href: '/settings#system-model' };
  await f.controller.refresh();
  assert.equal(modelDescription(f.last().selection), 'not configured');
});

test('in-flight selection stays frozen through settings change and first session assignment', async () => {
  let selected = 'before';
  let reads = 0;
  const f = fixture(async () => {
    reads++;
    return configured(selected);
  });
  await f.controller.open();
  selected = 'at-send';
  await f.controller.beforeSend();
  assert.equal(f.last().selection.model, 'at-send');
  selected = 'after';
  f.controller.setContext({ kind: 'personal', sessionId: 'new-session' });
  await f.controller.refresh();
  assert.equal(reads, 2);
  assert.equal(f.last().selection.model, 'at-send');
  await f.controller.setBusy(false);
  assert.equal(f.last().selection.model, 'after');
  await f.controller.beforeSend();
  assert.equal(reads, 4, 'every later send gets a fresh read');
});

test('a pending read cannot replace the snapshot after a turn starts', async () => {
  let resolve;
  const f = fixture(
    () =>
      new Promise(done => {
        resolve = done;
      })
  );
  const read = f.controller.open();
  f.controller.setBusy(true);
  resolve(configured('late'));
  await read;
  assert.equal(f.last().selection, null);
  assert.equal(f.last().frozen, true);
});

test('close ignores late responses; reopen, session resume and focus refresh the next choice', async () => {
  const doc = new EventTarget();
  const win = new EventTarget();
  let busRefresh;
  win.EventBus = {
    on: (event, handler) => {
      assert.equal(event, 'systemModel:changed');
      busRefresh = handler;
    }
  };
  let context = { kind: 'personal', sessionId: '' };
  const seen = [];
  const f = fixture(async ctx => {
    seen.push(ctx);
    return configured(`read-${seen.length}`);
  });
  bindModelLifecycle(f.controller, { doc, win, context: () => context });
  doc.dispatchEvent(new Event('personal-assistant:opened'));
  await tick();
  assert.equal(seen.length, 1);
  context = { kind: 'personal', sessionId: 'resumed' };
  doc.dispatchEvent(new Event('personal-assistant:conversation'));
  await tick();
  assert.equal(seen.at(-1).sessionId, 'resumed');
  for (const fire of [
    () => win.dispatchEvent(new Event('focus')),
    () => doc.dispatchEvent(new Event('visibilitychange')),
    () => busRefresh()
  ]) {
    const before = seen.length;
    fire();
    await tick();
    assert.equal(seen.length, before + 1);
  }
  doc.hidden = true;
  const before = seen.length;
  doc.dispatchEvent(new Event('visibilitychange'));
  await tick();
  assert.equal(seen.length, before);
  doc.dispatchEvent(new Event('personal-assistant:closed'));
  win.dispatchEvent(new Event('focus'));
  assert.equal(seen.length, before);
  doc.dispatchEvent(new Event('personal-assistant:opened'));
  await tick();
  assert.equal(seen.length, before + 1);
});

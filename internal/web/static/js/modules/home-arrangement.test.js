import test from 'node:test';
import assert from 'node:assert/strict';
import {
  homeArrangementContext,
  homeArrangementFailure,
  mountHomeArrangementPlanner
} from './home-arrangement.js';
import { ArrangementConsent } from './workspace-arrangement-consent.js';

const context = () => ({
  home_id: 'home-a',
  revision: 1,
  schema_version: 1,
  agent_name: 'Bound Manager',
  provider: 'ollama',
  model: 'fixture',
  context_pin: 'a'.repeat(64)
});
function dom() {
  const document = {
    createElement: tag => ({
      tag,
      ownerDocument: document,
      children: [],
      events: {},
      textContent: '',
      value: '',
      attributes: {},
      isConnected: true,
      append(...nodes) {
        this.children.push(...nodes);
      },
      replaceChildren(...nodes) {
        this.children = nodes;
        this.textContent = '';
      },
      setAttribute(key, value) {
        this.attributes[key] = value;
      },
      addEventListener(key, fn) {
        this.events[key] = fn;
      }
    })
  };
  return document.createElement('div');
}
const nodes = root => [root, ...root.children.flatMap(nodes)];
function mount(request, active = () => true) {
  const root = dom();
  const api = mountHomeArrangementPlanner(root, { homeID: 'home-a', request, isCurrent: active });
  const all = nodes(root);
  return {
    root,
    api,
    all,
    refresh: all.find(node => node.textContent === 'Refresh planning context'),
    history: all.find(node => node.textContent === 'Reopen saved history before retrying'),
    goal: all.find(node => node.tag === 'input' && node.type === 'text'),
    send: all.find(node => node.textContent === 'Send organizing goal'),
    checks: all.filter(node => node.tag === 'input' && node.type === 'checkbox')
  };
}
const settle = () => new Promise(resolve => setImmediate(resolve));
const choose = input => {
  input.checked = true;
  input.events.change();
};

test('Home disclosure binds exact scope/runtime and rejects unknown or changed context', () => {
  for (const [home, view, active] of [
    ['other', context(), true],
    ['home-a', context(), false],
    ['home-a', { ...context(), context_pin: 'bad' }, true]
  ]) {
    assert.equal(
      new ArrangementConsent().update(homeArrangementContext(home, view, 'Organize', active))
        .available,
      false
    );
  }
  const consent = new ArrangementConsent();
  const before = homeArrangementContext('home-a', context(), 'Organize');
  const { epoch } = consent.update(before);
  consent.chooseSharing(true, epoch);
  consent.chooseUpgrade(true, epoch);
  assert.equal(
    consent.take(
      homeArrangementContext('home-a', { ...context(), context_pin: 'b'.repeat(64) }, 'Organize')
    ),
    null
  );
});

test('structured recovery never renders a raw server/provider error', () => {
  for (const code of [
    'unavailable',
    'stale_context',
    'model_unavailable',
    'upgrade_required',
    '__proto__',
    undefined
  ]) {
    const message = homeArrangementFailure({
      message: '/private/secret',
      payload: { arrangement_error: { code, message: '/private/secret' } }
    });
    assert.doesNotMatch(message, /private|secret/);
  }
  assert.match(
    homeArrangementFailure({ payload: { arrangement_error: { code: 'unavailable' } } }),
    /trusted skill\/Toolbox/
  );
});

test('mount and checkbox changes never send; a POST carries only consent plus the current pin', async () => {
  const calls = [];
  let validHistory = false;
  const ui = mount(async (path, options) => {
    calls.push({ path, options });
    if (path === '/arrangements/context') return context();
    if (path === '/arrangements') return validHistory ? { arrangements: [] } : {};
    throw new Error('uncertain save /private/secret');
  });
  assert.equal(calls.length, 0);
  ui.goal.value = 'Organize';
  ui.goal.events.input();
  ui.refresh.events.click();
  await settle();
  choose(ui.checks[0]);
  assert.equal(ui.send.attributes['aria-disabled'], 'true');
  choose(ui.checks[1]);
  assert.equal(calls.length, 1);
  await ui.send.events.click();
  assert.equal(calls.length, 2);
  assert.equal(calls[1].path, '/arrangements/plan');
  assert.deepEqual(JSON.parse(calls[1].options.body), {
    goal: 'Organize',
    expected_revision: 1,
    share_catalog_metadata: true,
    allow_upgrade: true,
    expected_context: 'a'.repeat(64)
  });
  choose(ui.checks[0]);
  choose(ui.checks[1]);
  await ui.send.events.click();
  assert.equal(calls.length, 2, 'uncertain save requires history, not a checkbox retry');
  assert.equal(ui.goal.value, 'Organize');
  assert.ok(nodes(ui.root).every(node => !node.textContent.includes('/private/secret')));
  await ui.history.events.click();
  choose(ui.checks[0]);
  choose(ui.checks[1]);
  await ui.send.events.click();
  assert.equal(calls.length, 3, 'malformed history must not unlock a retry');
  validHistory = true;
  await ui.history.events.click();
  assert.equal(calls.length, 5, 'history and fresh context only, no automatic retry');
  assert.equal(ui.send.attributes['aria-disabled'], 'true');
  choose(ui.checks[0]);
  choose(ui.checks[1]);
  await ui.send.events.click();
  assert.equal(calls.length, 6, 'fresh choices allow exactly one new request');
});

test('late context reads cannot enable a different Home or an invalidated view', async () => {
  let finish;
  let active = true;
  const ui = mount(
    () =>
      new Promise(resolve => {
        finish = resolve;
      }),
    () => active
  );
  ui.goal.value = 'Organize';
  ui.refresh.events.click();
  active = false;
  ui.api.invalidate();
  finish(context());
  await settle();
  assert.equal(ui.send.disabled, true);
  assert.equal(ui.checks[0].disabled, true);
  assert.equal(ui.refresh.disabled, false, 'a superseded read must not strand Refresh');
});

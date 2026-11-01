import test from 'node:test';
import assert from 'node:assert/strict';
import { createFolderPrerequisites } from './folder-prerequisites.js';
import {
  createPluginSetupWizard,
  pluginFinalState,
  pluginSetupPlan
} from './plugin-setup-wizard.js';

let harness = { focused: null };

class FakeNode {
  constructor(tag) {
    this.tag = tag;
    this.children = [];
    this.attributes = {};
    this.listeners = {};
    this.className = '';
    this.isConnected = true;
    this.text = '';
  }
  get textContent() {
    return this.text + this.children.map(child => child.textContent).join('');
  }
  set textContent(value) {
    this.text = String(value);
    this.children = [];
  }
  append(...nodes) {
    this.children.push(...nodes);
  }
  replaceChildren(...nodes) {
    this.children = nodes;
  }
  setAttribute(name, value) {
    this.attributes[name] = value;
    if (name === 'open') this.open = true;
  }
  removeAttribute(name) {
    delete this.attributes[name];
    if (name === 'open') this.open = false;
  }
  addEventListener(type, handler) {
    this.listeners[type] = handler;
  }
  dispatch(type, event = {}) {
    this.listeners[type]?.({ preventDefault() {}, stopPropagation() {}, ...event });
  }
  contains(node) {
    return node === this || this.walk().includes(node);
  }
  focus() {
    harness.focused = this;
  }
  click() {
    this.dispatch('click');
  }
  showModal() {
    this.open = true;
    this.modal = true;
  }
  close() {
    this.open = false;
  }
  walk() {
    return this.children.flatMap(child => [child, ...child.walk()]);
  }
  button(label) {
    return this.walk().find(node => node.tag === 'button' && node.textContent === label);
  }
  labels() {
    return this.walk()
      .filter(node => node.tag === 'button')
      .map(node => node.textContent);
  }
}

const row = (kind, operation, name, status = operation) => ({ kind, operation, name, status });
const reaper = operation => row('integration', operation, 'REAPER');
const music = operation => row('provider', operation, 'Music Project Management');

const integrationReview = (action, extra = {}) => ({
  ok: true,
  status: 200,
  data: {
    prerequisite: {
      kind: 'integration',
      run_id: 'run',
      revision: 7,
      review_token: 'token',
      action,
      operation: action,
      version: '1.2.3',
      source: 'github.com/johnjallday/reaper-plugin',
      enabled_after: action === 'enable',
      ...(action === 'enable' ? {} : { disclosure: { Skills: ['one'] } }),
      details: { secret_free: true },
      ...extra
    }
  }
});
const providerReview = () => ({
  ok: true,
  status: 200,
  data: {
    prerequisite: {
      kind: 'provider',
      operation: 'install',
      digest: 'a'.repeat(64),
      version: '0.4.0',
      source: 'github.com/johnjallday/music-project-management',
      enabled_after: true,
      disclosure: { Skills: ['two'] }
    }
  }
});
const done = () => ({ ok: true, status: 200, data: { prerequisite: { completed: true } } });

function setup({ rows = [reaper('install')], dialogSupport = true, trust } = {}) {
  const body = new FakeNode('body');
  const doc = {
    body,
    activeElement: null,
    createElement: tag => {
      const node = new FakeNode(tag);
      if (!dialogSupport) node.showModal = undefined;
      return node;
    },
    querySelector: () => null
  };
  const fixture = {
    rows,
    calls: [],
    responses: [],
    afterConfirm: [],
    closes: [],
    owner: 'offer',
    trigger: new FakeNode('button')
  };
  let sequence = 0;
  let wizard;
  const controller = createFolderPrerequisites({
    key: () => fixture.owner,
    id: () => `request-${++sequence}`,
    request: async request => {
      fixture.calls.push(structuredClone(request));
      const response = fixture.responses.shift();
      return typeof response === 'function' ? response() : response;
    },
    changed: () => wizard?.sync(),
    // Stands in for the card's re-read: the new rows are in place by the time
    // the refresh settles, as recheckReview's returned promise guarantees.
    refreshed: async () => {
      const next = fixture.afterConfirm.shift();
      if (next) fixture.rows = next;
    }
  });
  controller.sync(true);
  wizard = createPluginSetupWizard({
    doc,
    controller,
    readRows: () => fixture.rows,
    trust: trust ?? {
      render: (report, options) => {
        const node = new FakeNode('div');
        node.className = 'trust-report';
        node.options = options;
        node.textContent = JSON.stringify(report);
        return node;
      },
      summary: () => '1 skill'
    },
    onClose: info => fixture.closes.push(info)
  });
  harness = { focused: null };
  return {
    ...fixture,
    fixture,
    wizard,
    controller,
    doc,
    dialog: () => body.children[0],
    open: (kind = 'integration') => wizard.open(fixture.rows, { kind, trigger: fixture.trigger }),
    text: () => body.children[0].textContent
  };
}

const settle = () => new Promise(resolve => setImmediate(resolve));

test('the plan keeps only pending plugin changes and leads with the pressed plugin', () => {
  const rows = [reaper('install'), music('install'), row('other', 'ready', 'Ready')];
  assert.deepEqual(
    pluginSetupPlan(rows).map(step => step.kind),
    ['integration', 'provider']
  );
  assert.deepEqual(
    pluginSetupPlan(rows, 'provider').map(step => step.kind),
    ['provider', 'integration']
  );
  assert.deepEqual(pluginSetupPlan(undefined), []);
});

test('nothing opens with no pending plugin, and a single plugin skips the intro', async () => {
  const none = setup({ rows: [reaper('ready')] });
  none.open();
  assert.equal(none.wizard.view().open, false);
  assert.equal(none.dialog(), undefined);

  const f = setup();
  f.fixture.responses.push(integrationReview('install'));
  f.open();
  assert.equal(f.dialog().modal, true);
  assert.equal(f.wizard.view().phase, 'review');
  assert.equal(f.wizard.view().title, 'Plugin 1 of 1 · REAPER');
  assert.equal(f.dialog().attributes['aria-labelledby'], 'pluginSetupWizardTitle');
  await settle();
});

test('a browser without showModal still gets a usable panel', async () => {
  const f = setup({ dialogSupport: false });
  f.fixture.responses.push(integrationReview('install'));
  f.open();
  assert.equal(f.dialog().attributes.open, 'open');
  await settle();
});

test('several plugins open on the intro, which asks the server for nothing', async () => {
  const f = setup({ rows: [reaper('install'), music('install')] });
  f.open();
  assert.equal(f.wizard.view().phase, 'intro');
  assert.equal(f.wizard.view().title, 'What this setup needs');
  assert.match(f.text(), /Ori plugins are app-wide/);
  assert.match(f.text(), /Music Project Management/);
  assert.equal(f.fixture.calls.length, 0);

  f.fixture.responses.push(integrationReview('install'));
  f.dialog().button('Continue').click();
  await settle();
  assert.equal(f.wizard.view().title, 'Plugin 1 of 2 · REAPER');
  assert.equal(f.fixture.calls.length, 1);
});

test('the review shows the decision before the report, in the planned order', async () => {
  const f = setup();
  f.fixture.responses.push(integrationReview('install'));
  f.open();
  assert.match(f.text(), /Reading the reviewed package…/);
  await settle();
  const nodes = f.dialog().walk();
  const at = predicate => nodes.findIndex(predicate);
  const heading = at(n => n.tag === 'h3' && n.textContent === 'Install REAPER');
  const release = at(n => n.textContent === 'Release 1.2.3 · github.com/johnjallday/reaper-plugin');
  const risk = at(n => n.textContent === '1 skill');
  const enabled = at(
    n => n.textContent === 'Enabled after this action: No — the next step enables it'
  );
  const confirm = at(n => n.tag === 'button' && n.textContent === 'Confirm installation');
  const cancel = at(n => n.tag === 'button' && n.textContent === 'Cancel');
  const report = at(n => n.className === 'trust-report');
  const note = at(n => /^This changes only this Ori plugin/.test(n.textContent));
  const expander = at(n => n.tag === 'summary' && n.textContent === 'Everything Ori reviewed');
  const order = [heading, release, risk, enabled, confirm, cancel, report, note, expander];
  assert.ok(
    order.every(index => index >= 0),
    JSON.stringify(order)
  );
  assert.deepEqual(
    order,
    [...order].sort((a, b) => a - b)
  );
  assert.equal(nodes.find(n => n.className === 'trust-report').options.summary, false);
});

test('confirm is disabled without a disclosure or a way to show it, except for enable', async () => {
  const noDisclosure = setup();
  noDisclosure.fixture.responses.push(integrationReview('install', { disclosure: undefined }));
  noDisclosure.open();
  await settle();
  assert.equal(noDisclosure.dialog().button('Confirm installation').disabled, true);
  assert.match(noDisclosure.text(), /Do not confirm an undisclosed installation/);

  const noRenderer = setup({ trust: {} });
  noRenderer.fixture.responses.push(integrationReview('install'));
  noRenderer.open();
  await settle();
  assert.equal(noRenderer.dialog().button('Confirm installation').disabled, true);

  const enable = setup({ rows: [reaper('enable')] });
  enable.fixture.responses.push(integrationReview('enable'));
  enable.open();
  await settle();
  assert.equal(enable.dialog().button('Confirm enablement').disabled, false);
  assert.match(enable.text(), /No download/);
});

test('confirm sends the same envelope the card always sent, and shows it is applying', async () => {
  const f = setup();
  let release;
  f.fixture.responses.push(
    integrationReview('install'),
    () =>
      new Promise(resolve => {
        release = resolve;
      })
  );
  f.fixture.afterConfirm.push([reaper('ready')]);
  f.open();
  await settle();
  f.dialog().button('Confirm installation').click();
  await settle();
  assert.equal(f.dialog().button('Applying reviewed change…').disabled, true);
  assert.equal(f.dialog().button('Cancel').disabled, true);
  assert.deepEqual(f.wizard.view().progress, ['Installing']);
  // Neither Escape nor a forced close can abandon a confirmation in flight.
  f.dialog().dispatch('keydown', { key: 'Escape' });
  f.dialog().close();
  f.dialog().dispatch('close');
  assert.equal(f.wizard.view().open, true);
  assert.equal(f.dialog().open, true);
  assert.deepEqual(f.fixture.calls[1], {
    kind: 'integration',
    confirm: true,
    request_id: 'request-2',
    run_id: 'run',
    revision: 7,
    review_token: 'token',
    action: 'install'
  });
  release(done());
  await settle();
  assert.equal(f.wizard.view().phase, 'summary');
});

test('install chains to the enable review on the same step, and each needs its own consent', async () => {
  const f = setup({ rows: [reaper('install')] });
  f.fixture.responses.push(
    integrationReview('install'),
    done(),
    integrationReview('enable'),
    done()
  );
  f.fixture.afterConfirm.push([reaper('enable')], [reaper('ready')]);
  f.open();
  await settle();
  f.dialog().button('Confirm installation').click();
  await settle();
  assert.equal(f.wizard.view().title, 'Plugin 1 of 1 · REAPER');
  assert.equal(f.wizard.view().phase, 'review');
  assert.match(f.text(), /Enable REAPER/);
  // The enable review was fetched by itself, and nothing confirmed it.
  assert.deepEqual(
    f.fixture.calls.map(call => [Boolean(call.confirm), call.action || '']),
    [
      [false, ''],
      [true, 'install'],
      [false, '']
    ]
  );
  f.dialog().button('Confirm enablement').click();
  await settle();
  assert.equal(f.fixture.calls[3].action, 'enable');
  assert.equal(f.wizard.view().phase, 'summary');
  assert.match(f.text(), /Installed and enabled/);
  assert.match(f.text(), /No workspace, Home, agent or model task was created\./);
});

test('a provider that enables on install moves straight to the next plugin', async () => {
  const f = setup({ rows: [music('install'), reaper('install')] });
  f.fixture.responses.push(providerReview(), done(), integrationReview('install'));
  f.fixture.afterConfirm.push([music('ready'), reaper('install')]);
  f.open('provider');
  f.dialog().button('Continue').click();
  await settle();
  assert.match(f.text(), /Enabled after this action: Yes/);
  f.dialog().button('Confirm installation').click();
  await settle();
  assert.equal(f.wizard.view().title, 'Plugin 2 of 2 · REAPER');
  assert.deepEqual(f.wizard.view().progress, ['Done', 'Reviewing']);
});

for (const [name, after, expected] of [
  ['update', [reaper('update')], /update/],
  ['unavailable', [reaper('unavailable')], /unavailable/],
  ['missing', [], /no longer part of this setup/]
]) {
  test(`after an install a row reporting ${name} stops the plugin and offers no second install`, async () => {
    const f = setup();
    f.fixture.responses.push(integrationReview('install'), done());
    f.fixture.afterConfirm.push(after);
    f.open();
    await settle();
    f.dialog().button('Confirm installation').click();
    await settle();
    assert.equal(f.wizard.view().phase, 'stopped');
    assert.deepEqual(f.wizard.view().progress, ['Stopped here']);
    assert.match(f.text(), expected);
    assert.deepEqual(f.dialog().labels(), ['Finish']);
    assert.equal(f.fixture.calls.length, 2);
    f.dialog().button('Finish').click();
    assert.equal(f.wizard.view().phase, 'summary');
    assert.doesNotMatch(f.text(), /Installed and enabled/);
  });
}

test('an enable that does not take effect stops instead of looping', async () => {
  const f = setup({ rows: [reaper('enable')] });
  f.fixture.responses.push(integrationReview('enable'), done());
  f.fixture.afterConfirm.push([reaper('enable')]);
  f.open();
  await settle();
  f.dialog().button('Confirm enablement').click();
  await settle();
  assert.equal(f.wizard.view().phase, 'stopped');
  assert.equal(f.fixture.calls.length, 2);
  f.dialog().button('Finish').click();
  assert.match(f.text(), /Installed, not enabled/);
});

test('a refused confirm reports the error and asks for an explicit new review', async () => {
  const f = setup();
  f.fixture.responses.push(integrationReview('install'), {
    ok: false,
    status: 409,
    data: { error: 'Review the changed package.' }
  });
  f.open();
  await settle();
  f.dialog().button('Confirm installation').click();
  await settle();
  const alert = f
    .dialog()
    .walk()
    .find(node => node.attributes?.role === 'alert');
  assert.equal(alert.textContent, 'Review the changed package.');
  assert.equal(f.fixture.calls.length, 2);
  assert.equal(f.wizard.view().changed, false);
  f.fixture.responses.push(integrationReview('install'));
  f.dialog().button('Review again').click();
  await settle();
  assert.equal(f.fixture.calls.length, 3);
  assert.equal(f.fixture.calls[2].confirm, undefined);
});

test('a lost reply keeps its envelope through Cancel and reopening, and replays it exactly', async () => {
  const f = setup();
  f.fixture.responses.push(integrationReview('install'), () => {
    throw new Error('lost response');
  });
  f.open();
  await settle();
  f.dialog().button('Confirm installation').click();
  await settle();
  assert.ok(f.dialog().button('Check the same installation'));
  assert.equal(f.dialog().button('Cancel').disabled, false);
  f.dialog().button('Cancel').click();
  assert.equal(f.wizard.view().open, false);
  assert.ok(f.controller.view().pending);

  // Pressing another plugin's row still lands on the unsettled installation.
  f.fixture.rows = [music('install'), reaper('install')];
  f.wizard.open(f.fixture.rows, { kind: 'provider' });
  await settle();
  assert.equal(f.wizard.view().kind, 'integration');
  assert.equal(f.wizard.view().phase, 'review');
  assert.equal(f.fixture.calls.length, 2);
  f.fixture.responses.push(done());
  f.fixture.afterConfirm.push([music('install'), reaper('ready')]);
  f.dialog().button('Check the same installation').click();
  await settle();
  assert.deepEqual(f.fixture.calls[2], f.fixture.calls[1]);
});

test('a review dropped because the offer moved on is reported, not silently re-requested', async () => {
  const f = setup();
  f.fixture.responses.push(integrationReview('install'));
  f.open();
  await settle();
  f.fixture.owner = 'newer-offer';
  f.controller.sync(true);
  f.wizard.sync();
  assert.equal(f.wizard.view().phase, 'stopped');
  assert.match(f.text(), /This setup changed while the wizard was open/);
  assert.equal(f.fixture.calls.length, 1);
});

test('a reply arriving after Cancel cannot reopen or change anything', async () => {
  const f = setup();
  let release;
  f.fixture.responses.push(
    () =>
      new Promise(resolve => {
        release = resolve;
      })
  );
  f.open();
  await settle();
  f.dialog().button('Cancel').click();
  assert.equal(f.wizard.view().open, false);
  release(integrationReview('install'));
  await settle();
  assert.equal(f.wizard.view().open, false);
  assert.equal(f.controller.view().review, null);
  assert.equal(f.fixture.closes.length, 1);
});

test('Cancel with no change closes once and returns focus to the launching button', async () => {
  const f = setup();
  f.fixture.responses.push(integrationReview('install'));
  f.open();
  await settle();
  f.dialog().button('Cancel').click();
  assert.equal(f.wizard.view().open, false);
  assert.equal(f.dialog().open, false);
  assert.deepEqual(f.fixture.closes, [{ changed: false }]);
  assert.equal(harness.focused, f.fixture.trigger);
  assert.equal(f.controller.active(), false);
  f.wizard.close();
  assert.equal(f.fixture.closes.length, 1);
});

test('Escape mirrors Cancel through the native event and the key, and other keys do nothing', async () => {
  for (const press of [d => d.dispatch('cancel'), d => d.dispatch('keydown', { key: 'Escape' })]) {
    const f = setup();
    f.fixture.responses.push(integrationReview('install'));
    f.open();
    await settle();
    f.dialog().dispatch('keydown', { key: 'Tab' });
    assert.equal(f.wizard.view().open, true);
    press(f.dialog());
    assert.equal(f.wizard.view().open, false);
    assert.equal(f.fixture.closes.length, 1);
  }
});

test('Cancel on the enable step after an install lands on an honest summary', async () => {
  const f = setup();
  f.fixture.responses.push(integrationReview('install'), done(), integrationReview('enable'));
  f.fixture.afterConfirm.push([reaper('enable')]);
  f.open();
  await settle();
  f.dialog().button('Confirm installation').click();
  await settle();
  f.dialog().button('Cancel').click();
  assert.equal(f.wizard.view().open, true);
  assert.equal(f.wizard.view().phase, 'summary');
  assert.match(f.text(), /Installed, not enabled/);
  assert.doesNotMatch(f.text(), /Installed and enabled/);
  f.dialog().button('Back to setup').click();
  assert.deepEqual(f.fixture.closes, [{ changed: true }]);
  assert.equal(f.wizard.view().open, false);
});

test('Back from a later plugin returns to the intro once the earlier one is finished', async () => {
  const f = setup({ rows: [music('install'), reaper('install')] });
  f.fixture.responses.push(providerReview(), done(), integrationReview('install'));
  f.fixture.afterConfirm.push([music('ready'), reaper('install')]);
  f.open('provider');
  f.dialog().button('Continue').click();
  await settle();
  f.dialog().button('Confirm installation').click();
  await settle();
  f.dialog().button('Back').click();
  assert.equal(f.wizard.view().phase, 'intro');
  assert.equal(f.controller.active(), false);
});

test('the final state comes from the refreshed row, never from what the wizard attempted', () => {
  const install = { original: 'install', applied: true };
  const untouched = { original: 'install', applied: false };
  const update = { original: 'update', applied: true };
  assert.equal(pluginFinalState(install, reaper('ready')), 'Installed and enabled');
  assert.equal(pluginFinalState(install, reaper('enable')), 'Installed, not enabled');
  assert.equal(pluginFinalState(update, reaper('ready')), 'Updated');
  assert.equal(pluginFinalState(update, reaper('enable')), 'Updated, not enabled');
  assert.equal(pluginFinalState(untouched, reaper('install')), 'Not changed');
  assert.equal(
    pluginFinalState({ original: 'update', applied: false }, reaper('update')),
    'Not changed'
  );
  assert.equal(pluginFinalState(install, reaper('install')), 'Could not be confirmed');
  assert.equal(pluginFinalState(install, null), 'Could not be confirmed');
  assert.equal(
    pluginFinalState(untouched, row('integration', 'unavailable', 'REAPER', 'Check unavailable')),
    'Check unavailable'
  );
});

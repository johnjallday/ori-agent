import test from 'node:test';
import assert from 'node:assert/strict';
import {
  ArrangementConsent,
  arrangementDisclosure,
  mountArrangementConsent,
  validArrangementGoal
} from './workspace-arrangement-consent.js';

const home = () => ({
  kind: 'home_library',
  agentName: 'Portfolio Manager',
  providerName: 'ollama',
  modelName: 'fixture-model',
  homeID: 'home-a',
  contextKey: 'owner/instance/manager/provider',
  available: true,
  expectedRevision: 12,
  libraryVersion: 1,
  goal: 'Keep experiments catalog-only'
});
const attachment = () => ({
  kind: 'attachment',
  agentName: 'Personal assistant',
  providerName: 'ollama',
  modelName: 'fixture-model',
  contextKey: 'owner/conversation/selection/revision/runtime',
  available: true,
  historical: false,
  focusIDs: [],
  goal: 'Group only the selected client projects'
});
const allow = (consent, input) => {
  const { epoch } = consent.update(input);
  consent.chooseSharing(true, epoch);
  consent.chooseUpgrade(true, epoch);
};

test('disclosure distinguishes expanded held evidence from catalog sharing and storage upgrade', () => {
  assert.match(arrangementDisclosure('attachment').source, /eight names and 8 KiB/);
  assert.match(arrangementDisclosure('attachment').budget, /64 KiB/);
  assert.match(arrangementDisclosure('home_library').source, /currently bound Manager/);
  assert.match(arrangementDisclosure('home_library').upgrade, /proposal or clarification/);
  assert.match(arrangementDisclosure('home_library').upgrade, /Older Ori versions cannot open/);
  assert.equal(arrangementDisclosure('unknown'), null);
});

test('goal validation matches host byte, whitespace, Unicode and single-line boundaries', () => {
  for (const value of ['Goal', '音'.repeat(666), 'a'.repeat(2000)])
    assert.equal(validArrangementGoal(value), true);
  for (const value of [
    '',
    ' goal',
    'goal ',
    'a\nb',
    'a\tb',
    '\u200bhidden',
    'a'.repeat(2001),
    '音'.repeat(667),
    '\ud800',
    null,
    4
  ]) {
    assert.equal(validArrangementGoal(value), false, String(value));
  }
});

test('Home sharing and irreversible storage consent are independent, exact and one-turn only', () => {
  const input = home();
  const consent = new ArrangementConsent();
  const view = consent.update(input);
  assert.equal(consent.take(input), null);
  consent.chooseSharing('true', view.epoch);
  assert.equal(consent.view().share, false);
  consent.chooseSharing(true, view.epoch);
  assert.equal(consent.take(input), null);
  consent.chooseUpgrade(true, view.epoch);
  assert.deepEqual(consent.take(input), {
    goal: input.goal,
    expected_revision: 12,
    share_catalog_metadata: true,
    allow_upgrade: true
  });
  assert.equal(consent.take(input), null);
  assert.equal(
    consent.chooseSharing(true, view.epoch),
    false,
    'the consumed checkbox event is stale'
  );
  assert.equal(new ArrangementConsent().update(input).canSend, false, 'no persisted consent');
});

test('version-2 library and PA produce minimal, distinct request fragments', () => {
  for (const input of [{ ...home(), libraryVersion: 2 }, attachment()]) {
    const consent = new ArrangementConsent();
    const { epoch } = consent.update(input);
    assert.equal(consent.chooseUpgrade(true, epoch), false);
    consent.chooseSharing(true, epoch);
    assert.deepEqual(
      consent.take(input),
      input.kind === 'attachment'
        ? { prompt: input.goal, arrangement: { share_held_metadata: true } }
        : {
            goal: input.goal,
            expected_revision: 12,
            share_catalog_metadata: true,
            allow_upgrade: false
          }
    );
  }
});

test('any goal, source, owner/runtime epoch, version or catalog change revokes choices', () => {
  for (const patch of [
    { goal: 'A different goal' },
    { agentName: 'Another Manager' },
    { providerName: 'openai' },
    { modelName: 'another-model' },
    { homeID: 'home-b' },
    { expectedRevision: 13 },
    { libraryVersion: 2 },
    { contextKey: 'new-owner' },
    { available: false }
  ]) {
    const input = home();
    const consent = new ArrangementConsent();
    allow(consent, input);
    assert.equal(consent.take({ ...input, ...patch }), null);
    assert.equal(consent.view().share, false);
    assert.equal(consent.view().upgrade, false);
  }
  const input = home();
  const consent = new ArrangementConsent();
  allow(consent, input);
  input.goal = 'Changed after disclosure';
  assert.equal(consent.take(input), null, 'caller object alias cannot mutate a granted goal');
});

test('missing prerequisites, unknown schemas, historical and focused attachments fail closed', () => {
  const invalid = [
    null,
    {},
    { ...home(), libraryVersion: 3 },
    { ...home(), modelName: '' },
    { ...home(), providerName: null },
    { ...home(), agentName: undefined },
    { ...home(), expectedRevision: 0 },
    { ...home(), expectedRevision: 1.5 },
    { ...home(), homeID: '..' },
    { ...home(), contextKey: '' },
    { ...home(), available: 'true' },
    { ...attachment(), historical: true },
    { ...attachment(), historical: undefined },
    { ...attachment(), focusIDs: ['topic'] },
    { ...attachment(), focusIDs: undefined }
  ];
  for (const input of invalid) {
    const consent = new ArrangementConsent();
    allow(consent, input);
    assert.equal(consent.view().available, false);
    assert.equal(consent.take(input), null);
  }
});

function dom() {
  const document = {
    createElement: tag => ({
      tag,
      ownerDocument: document,
      children: [],
      events: {},
      textContent: '',
      attributes: {},
      append(...nodes) {
        this.children.push(...nodes);
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
function component(input = home(), enabled = true, onSend = async () => {}) {
  const root = dom();
  const api = mountArrangementConsent(root, {
    kind: input.kind,
    getContext: () => input,
    enabled,
    onSend
  });
  const all = nodes(root);
  const [share, upgrade] = all.filter(node => node.tag === 'input');
  return {
    root,
    api,
    share,
    upgrade,
    send: all.find(node => node.tag === 'button'),
    status: all.find(node => node.attributes.role === 'status')
  };
}
const choose = (field, checked = true) => {
  field.checked = checked;
  field.events.change();
};

test('production default stays disabled and never calls Send even for synthetic events', async () => {
  let calls = 0;
  const root = dom();
  mountArrangementConsent(root, {
    kind: 'home_library',
    getContext: home,
    onSend: async () => {
      calls++;
    }
  });
  for (const input of nodes(root).filter(node => node.tag === 'input')) {
    assert.equal(input.disabled, true);
    choose(input);
  }
  const send = nodes(root).find(node => node.tag === 'button');
  assert.equal(send.disabled, true);
  await send.events.click();
  assert.equal(calls, 0);
});

test('changing context before a checkbox event or Send requires a fresh disclosure choice', async () => {
  let calls = 0;
  const input = home();
  const ui = component(input, true, async () => {
    calls++;
  });
  input.expectedRevision++;
  choose(ui.share);
  assert.equal(ui.share.checked, false);
  assert.match(ui.status.textContent, /changed/);
  choose(ui.share);
  choose(ui.upgrade);
  assert.equal(ui.send.disabled, false);
  input.contextKey = 'another-manager';
  await ui.send.events.click();
  assert.equal(calls, 0);
  assert.equal(ui.send.attributes['aria-disabled'], 'true');
});

test('one request, no automatic retry, no raw error and no input loss after an uncertain save', async () => {
  let calls = 0;
  let fail;
  const input = home();
  const ui = component(input, true, () => {
    calls++;
    return new Promise((_, reject) => {
      fail = reject;
    });
  });
  choose(ui.share);
  choose(ui.upgrade);
  const pending = ui.send.events.click();
  await ui.send.events.click();
  ui.api.refresh();
  assert.match(ui.status.textContent, /pending/);
  assert.equal(ui.api.historyReopened(), false);
  assert.equal(calls, 1);
  fail(new Error('/private/provider-secret'));
  await pending;
  assert.equal(calls, 1);
  assert.equal(ui.send.attributes['aria-disabled'], 'true');
  assert.equal(ui.share.checked, false);
  assert.equal(ui.upgrade.checked, false);
  assert.equal(input.goal, home().goal);
  assert.match(ui.status.textContent, /Reopen saved history/);
  assert.doesNotMatch(ui.status.textContent, /provider-secret/);
  choose(ui.share);
  choose(ui.upgrade);
  await ui.send.events.click();
  assert.equal(
    calls,
    1,
    'even an explicit retry waits for the adapter to reopen canonical history'
  );
  assert.equal(ui.api.historyReopened(), true);
  choose(ui.share);
  choose(ui.upgrade);
  const retry = ui.send.events.click();
  assert.equal(calls, 2);
  fail(new Error('still unavailable'));
  await retry;
});

test('late completion is not presented as the new context outcome', async () => {
  let finish;
  const input = home();
  const ui = component(
    input,
    true,
    () =>
      new Promise(resolve => {
        finish = resolve;
      })
  );
  choose(ui.share);
  choose(ui.upgrade);
  const pending = ui.send.events.click();
  input.homeID = 'another-home';
  ui.api.refresh();
  finish();
  await pending;
  assert.match(ui.status.textContent, /original saved history/);
  assert.equal(ui.send.attributes['aria-disabled'], 'true');
});

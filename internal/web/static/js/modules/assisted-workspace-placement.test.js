import test from 'node:test';
import assert from 'node:assert/strict';
import {
  placementPanelHTML,
  mountPlacementPanel,
  createPlacementCards
} from './assisted-workspace-placement.js';

const draft = { name: 'Music', mission: 'Prepare a review for my incoming files.' };
const team = [{ name: 'File Curator', action: 'create' }];
const folder = { name: 'Music', path: '/synthetic/Music' };
const review = { workspace: draft, team, folder, monitoring: false, terms: {} };

test('the primary surface describes all five comprehension facts without a map', () => {
  const html = placementPanelHTML({ draft, team, folder, review, canConfirm: true });
  for (const text of [
    'Music',
    'File Curator · new agent',
    '/synthetic/Music',
    'Prepare a filing review',
    'Off · manual scans only.',
    'linked, not moved',
    'Filed/',
    'No file contents.',
    'Create workspace & scan',
    'Use suggested setup',
    'Position does not join a district'
  ])
    assert.ok(html.includes(text), text);
  assert.doesNotMatch(html, /data-placement-action="confirm" disabled/);
});

test('a visual draft is never sufficient for confirmation', () => {
  for (const view of [
    {},
    { canConfirm: true },
    { review },
    { review, canConfirm: true, busy: true },
    { review, canConfirm: true, conflict: true },
    { review, canConfirm: true, closed: true }
  ]) {
    assert.match(
      placementPanelHTML({ draft, ...view }),
      /data-placement-action="confirm" disabled/
    );
  }
});

test('another-tab conflict locks material controls and offers an explicit saved-draft read', () => {
  const html = placementPanelHTML({ draft, conflict: true });
  assert.match(html, /data-placement-action="reload-draft"/);
  assert.match(html, /Discard local edits &amp; load saved draft/);
  assert.match(html, /data-placement-action="agent" disabled/);
  assert.match(html, /data-placement-action="cancel" disabled/);
  assert.doesNotMatch(html, /data-placement-action="back" disabled/);
});

test('accepted or uncertain setup offers observational recovery, never cancellation or an uncreated promise', () => {
  const html = placementPanelHTML({ accepted: true, review, reconciliationRequired: true });
  assert.match(html, /Read saved status/);
  assert.match(html, /Closing is not undo/);
  assert.match(html, /data-placement-action="confirm" disabled/);
  assert.doesNotMatch(html, /Nothing is created|Cancel draft|data-placement-field=/);
});

test('incomplete accepted setup shows observed choices and separates its existing route from continuation', () => {
  const html = placementPanelHTML({
    accepted: true,
    recoveryRoute: '/workspaces/saved?panel=file-janitor',
    review: {
      monitoring: true,
      outcome: {
        name: 'Saved <inbox>',
        goal: 'Keep this goal',
        members: ['Actual Curator'],
        folder_path: '/synthetic/exact-inbox',
        terms: { daily_scan_local_time: '09:00' }
      }
    }
  });
  for (const text of [
    'Saved &lt;inbox&gt;',
    'Keep this goal',
    'Actual Curator',
    '/synthetic/exact-inbox',
    '09:00',
    'Open existing filing review'
  ])
    assert.ok(html.includes(text), text);
  assert.doesNotMatch(html, /data-placement-field=/);
  const pending = placementPanelHTML({
    result: { outcome: 'no_eligible' },
    sourceReceiptPending: true
  });
  assert.match(pending, /assistant receipt is not yet saved/);
  assert.match(pending, /Read saved status/);
});

test('names, paths and draft fields are inert text, including review and messages', () => {
  const hostile = '<img src=x onerror="alert(1)">';
  const html = placementPanelHTML({
    draft: { name: hostile, mission: hostile },
    team: [{ name: hostile }],
    folder: { path: hostile },
    message: hostile
  });
  assert.doesNotMatch(html, /<img/);
  assert.match(html, /&lt;img/);
  assert.match(html, /&quot;/);
});

test('monitoring selection and reviewed schedule are distinct disclosures', () => {
  const html = placementPanelHTML({
    draft,
    monitoring: true,
    review: { ...review, monitoring: true, terms: { daily_scan_local_time: '09:00' } }
  });
  assert.match(html, /data-placement-field="monitoring" checked/);
  assert.match(html, /daily catch-up at 09:00/);
  assert.match(html, /Review the exact schedule/);
});

test('card references are ephemeral, versioned, typed and never accept arbitrary payloads', () => {
  let sequence = 0;
  const cards = createPlacementCards(() => `opaque-${++sequence}`);
  const choice = { kind: 'agent', id: 'Journal', label: 'Journal', detail: 'Saved agent' };
  const [first] = cards.update(1, [choice]);
  assert.equal(first.id, undefined);
  assert.deepEqual(cards.resolve(first.key, 'agent', 1), { kind: 'agent', id: 'Journal' });
  assert.equal(cards.update(1, [choice])[0].key, first.key, 'redraw preserves the handle');
  for (const key of [
    'Journal',
    '/synthetic/Inbox',
    '{"agent":"Journal"}',
    { key: first.key },
    'foreign'
  ])
    assert.equal(cards.resolve(key, 'agent', 1), null);
  assert.equal(cards.resolve(first.key, 'folder', 1), null);
  assert.equal(cards.resolve(first.key, 'agent', 2), null);
  const other = createPlacementCards(() => 'another-editor');
  other.update(1, [choice]);
  assert.equal(other.resolve(first.key, 'agent', 1), null);
  const [fresh] = cards.update(2, [choice]);
  assert.notEqual(fresh.key, first.key);
  assert.equal(cards.resolve(first.key, 'agent', 2), null);
  cards.update(2, []);
  assert.equal(cards.resolve(fresh.key, 'agent', 2), null, 'removed cards cannot be applied');
});

test('card drop and slot buttons emit the same intention; OS files are refused', () => {
  const events = new Map();
  const actions = [];
  const slot = { dataset: { placementSlot: 'agent' }, classList: { add() {}, remove() {} } };
  const host = {
    ownerDocument: {},
    contains: () => true,
    addEventListener: (name, handler) => events.set(name, handler),
    removeEventListener: name => events.delete(name),
    querySelectorAll: () => [slot],
    querySelector: () => null,
    replaceChildren() {}
  };
  const panel = mountPlacementPanel(host, action => actions.push(action));
  panel.update({ selectedCard: { key: 'opaque', kind: 'agent' } });
  const data = new Map();
  const transfer = {
    files: [],
    setData: (type, value) => data.set(type, value),
    getData: type => data.get(type)
  };
  events.get('dragstart')({
    target: { closest: () => ({ dataset: { placementCard: 'opaque' } }) },
    dataTransfer: transfer
  });
  assert.deepEqual([...data], [['application/x-ori-placement-card', 'opaque']]);
  events.get('drop')({
    preventDefault() {},
    target: { closest: () => slot },
    dataTransfer: transfer
  });
  events.get('click')({
    target: {
      closest: () => ({ dataset: { placementAction: 'card-use', placementSlotUse: 'agent' } })
    }
  });
  assert.deepEqual(actions[0], { action: 'card-use', key: 'opaque', slot: 'agent' });
  assert.deepEqual(actions[0], actions[1]);
  transfer.files = [{ name: 'not-authority.txt' }];
  events.get('drop')({
    preventDefault() {},
    target: { closest: () => slot },
    dataTransfer: transfer
  });
  assert.deepEqual(actions[2], { action: 'card-reject' });
  panel.destroy();
  assert.equal(events.size, 0);
});

test('acceptance and result keep disabled-control focus on readable status without stealing outside focus', () => {
  const control = { dataset: { placementAction: 'confirm' } };
  let focused = 0;
  const document = { activeElement: control, addEventListener() {}, removeEventListener() {} };
  const status = {
    dataset: { placementFocus: 'status' },
    focus() {
      document.activeElement = status;
      focused++;
    }
  };
  const host = {
    ownerDocument: document,
    addEventListener() {},
    removeEventListener() {},
    replaceChildren() {},
    contains: element => element === control || element === status,
    querySelector: selector =>
      selector.includes('placement-focus')
        ? status
        : { disabled: true, focus: () => assert.fail('disabled control') }
  };
  const panel = mountPlacementPanel(host, () => {});
  panel.update({ accepted: true, busy: true, message: 'Checking the confirmation.' });
  panel.update({ result: { outcome: 'no_eligible' }, message: 'Setup complete.' });
  assert.equal(focused, 2);
  document.activeElement = {};
  panel.update({ result: { outcome: 'no_eligible' }, message: 'Setup complete.' });
  assert.equal(focused, 2);
  panel.destroy();
});

test('panel emits intentions only, cleans listeners and preserves the focused control', () => {
  const events = new Map();
  const actions = [];
  let focused = '';
  const host = {
    ownerDocument: { activeElement: { dataset: { placementField: 'name' } } },
    contains: () => true,
    addEventListener: (name, handler) => events.set(name, handler),
    removeEventListener: name => events.delete(name),
    querySelector: selector => ({
      focus: () => {
        focused = selector;
      }
    }),
    replaceChildren: () => {
      host.innerHTML = '';
    }
  };
  const panel = mountPlacementPanel(host, action => actions.push(action));
  panel.update({ draft, review, canConfirm: true });
  assert.equal(focused, '[data-placement-field="name"]');
  events.get('change')({ target: { dataset: { placementField: 'monitoring' }, checked: true } });
  events.get('click')({
    target: { closest: () => ({ dataset: { placementAction: 'suggested' } }) }
  });
  events.get('click')({
    target: { closest: () => ({ dataset: { placementAction: 'confirm' }, disabled: true }) }
  });
  assert.deepEqual(actions, [
    { action: 'edit', field: 'monitoring', value: true },
    { action: 'suggested' }
  ]);
  panel.destroy();
  assert.equal(events.size, 0);
  assert.equal(host.innerHTML, '');
});

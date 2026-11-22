import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView } from './workspace-command.js';
import { FIRST_VISIT_MISSIONS } from './hq-first-visit.js';
import { readNoteDraft, writeNoteDraft } from './hq-first-note.js';
import { offerOnEntry } from './hq-first-visit-card.js';
import { HQQuickCapture } from './hq-quick-capture.js';

const missionRows = (note = 'open') =>
  Object.entries(FIRST_VISIT_MISSIONS).map(([key, id]) => ({
    id,
    status: key === 'note' ? note : 'open'
  }));
const notes = { status: 'connected', source: { id: 'notes', origin: 'created_by_ori' } };
function fixture() {
  globalThis.document = {
    getElementById: () => null,
    body: { dataset: { hqFirstVisit: 'pending' } }
  };
  const owner = new WorkspaceCommandView({ workspaceId: 'hq', workspace: { id: 'hq' }, tasks: [] });
  owner.isPersonalHQ = () => true;
  owner.notesState = () => ({ secondBrain: notes, status: 'ready' });
  owner.requestNotesData = () => {};
  owner.render = () => {};
  owner.container = { querySelector: () => null };
  owner.active = true;
  owner.viewMode = 'details';
  return { owner, card: owner.firstVisit };
}

test('the header offers one closed door; no suggestions occupy the main column', () => {
  const { owner, card } = fixture();
  assert.match(card.headerHTML(), /Things to try/);
  assert.match(card.headerHTML(), /aria-expanded="false"/);
  assert.doesNotMatch(card.headerHTML(), /badge|\d of|role="dialog"/);
  owner.hqNeedsYouHTML = () => '';
  owner.renderHQGitHubHost = () => '';
  owner.renderFollowUpsHost = () => '';
  owner.hqCards = () => [];
  owner.renderHQRail = () => '';
  assert.doesNotMatch(owner.renderHQDetails(), /Things to try|data-hq-firstvisit/);
  assert.match(owner.renderHQDetails(), /data-cmd-hq-host="notes"/);
  card.state = 'done';
  assert.match(card.headerHTML(), /Things to try/);
  owner.isPersonalHQ = () => false;
  assert.equal(card.headerHTML(), '');
});

test('compact choices use real handoffs, with no duplicate editor, quota, avatar or ticket footer', async () => {
  const { owner, card } = fixture();
  owner.stationJSON = async path => {
    assert.equal(path, '/api/progression');
    return { missions: missionRows() };
  };
  assert.doesNotMatch(card.html(), /disabled/);
  await card.load();
  writeNoteDraft({ text: 'Home draft survives', requestId: 'existing', sourceId: 'notes' });
  const before = readNoteDraft();
  const html = card.html();
  assert.match(html, /role="dialog"/);
  assert.match(html, /Explore Documents/);
  assert.match(html, /Connect email/);
  assert.match(html, /Scan recent apps/);
  assert.doesNotMatch(html, /href=/);
  await card.act('folder');
  assert.match(card.html(), /suggest=documents/);
  assert.doesNotMatch(html, /textarea|<input|avatar|Tickets|of 3|aria-modal/);
  assert.match(html, /aria-describedby="hq-suggestions-hint"/);
  card.sheet = true;
  assert.match(card.html(), /aria-modal="true"/);
  assert.deepEqual(readNoteDraft(), before, 'presentation never consumes a Home draft');
});

test('an existing dialog retains focus; closing it later does not spring a delayed invitation', () => {
  const { card } = fixture();
  const oldWindow = globalThis.window;
  globalThis.window = { location: { search: '', hash: '' } };
  try {
    card.door = () => ({});
    card.open = () => assert.fail('stole the active surface');
    document.querySelector = () => ({});
    card.bind();
    document.querySelector = () => null;
    card.bind();
    assert.equal(card.entryChecked, true);
  } finally {
    globalThis.window = oldWindow;
  }
});

test('a brief returns focus to the current suggestion door after an HQ repaint', () => {
  const { owner, card } = fixture();
  const fresh = { focus() {} };
  card.door = () => fresh;
  const stale = {
    isConnected: false,
    hasAttribute: name => name === 'data-hq-firstvisit-action',
    getAttribute: () => 'open'
  };
  assert.equal(owner.resolveStatModalTrigger(stale), fresh);
});

test('ordinary brief completion refreshes missions without reopening suggestions or stealing focus', async () => {
  const { owner, card } = fixture();
  owner.stationJSON = async () => ({
    missions: missionRows().map(m =>
      m.id === FIRST_VISIT_MISSIONS.brief ? { ...m, status: 'completed' } : m
    )
  });
  await card.briefClosed();
  assert.equal(card.view().steps[1].resolved, true);
  assert.equal(card.node, null);
});

test('all resolved on arrival finishes silently; failure leaves a closed usable door', async () => {
  const previous = globalThis.fetch;
  let ok = false;
  globalThis.fetch = async () => ({ ok });
  try {
    const { owner, card } = fixture();
    owner.stationJSON = async () => ({
      missions: missionRows().map(m => ({ ...m, status: 'skipped' }))
    });
    await card.load();
    assert.equal(card.state, 'pending');
    assert.match(card.headerHTML(), /aria-expanded="false"/);
    assert.match(card.html(), /Maybe later/);
    ok = true;
    await card.finish();
    assert.equal(card.state, 'done');
    assert.match(card.headerHTML(), /Things to try/);
  } finally {
    globalThis.fetch = previous;
  }
});

test('finishing the existing visit never writes mission progress', async () => {
  const { card } = fixture();
  card.missions = missionRows();
  const before = structuredClone(card.missions);
  const calls = [];
  const previous = globalThis.fetch;
  globalThis.fetch = async (path, options) => {
    calls.push([path, JSON.parse(options.body)]);
    return { ok: true };
  };
  try {
    await card.finish();
    assert.equal(card.state, 'done');
    assert.deepEqual(card.missions, before);
    assert.deepEqual(calls, [['/api/personal-hq/first-visit-state', { state: 'done' }]]);
  } finally {
    globalThis.fetch = previous;
  }
});

test('failed progression reads do not invent progress or block voluntary choices', async () => {
  const { owner, card } = fixture();
  owner.stationJSON = async () => {
    throw Error('offline');
  };
  await card.load();
  assert.equal(card.missions, null);
  assert.equal(card.node, null);
  assert.equal(card.view().count, 0);
  assert.match(card.html(), /Explore Documents/);
});

test('page departure and BFCache return keep suggestions closed without losing canonical refresh', () => {
  const { card } = fixture();
  const previous = globalThis.window;
  const events = new Map();
  globalThis.window = { addEventListener: (name, callback) => events.set(name, callback) };
  try {
    const calls = [];
    card.close = restore => calls.push(['close', restore]);
    card.load = async () => calls.push(['load']);
    card.ensure();
    events.get('pagehide')();
    events.get('pageshow')({ persisted: true });
    assert.deepEqual(calls, [['load'], ['close', false], ['close', false], ['load']]);
  } finally {
    globalThis.window = previous;
  }
});

test('ticket, station and fragment deep links never auto-offer', () => {
  for (const suffix of ['?mode=tickets', '?mode=details&station=notes', '?setup=email', '#notes'])
    assert.equal(offerOnEntry(new URL(suffix, 'https://ori.test')), false);
});

test('switching away from HQ closes any open suggestions without stealing focus', () => {
  const { owner, card } = fixture();
  card.started = true;
  card.node = {};
  card.id = 'hq';
  owner.viewMode = 'tickets';
  let closed;
  card.close = restore => {
    closed = restore;
  };
  card.ensure();
  assert.equal(closed, false);
});

test('note handoff focuses HQ capture without replacing its draft', () => {
  const { owner } = fixture();
  const calls = [];
  const capture = new HQQuickCapture();
  capture.draft.edit('Existing draft', 'same-connection');
  capture.host = {
    querySelector: selector => {
      assert.equal(selector, 'input:not([readonly])');
      return { focus: () => calls.push('focus') };
    }
  };
  owner.hqQuickCapture = capture;
  owner.focusHQNoteCapture();
  assert.equal(capture.draft.text, 'Existing draft');
  assert.deepEqual(calls, ['focus']);
  capture.mount(null);
  owner.openNotesStation = trigger => calls.push(trigger);
  owner.focusHQNoteCapture('connection');
  assert.equal(calls.at(-1), 'connection');
});

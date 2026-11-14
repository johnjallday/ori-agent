import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView } from './workspace-command.js';
import { FIRST_VISIT_MISSIONS } from './hq-first-visit.js';
import { writeNoteDraft } from './hq-first-note.js';

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
  owner.dailyBriefStationData = () => ({});
  owner.hqTeam = () => [{ name: 'Atlas', isLead: true }];
  owner.page.getAgentAvatarPresentation = () => ({ markup: () => '<img src="face.png" alt="">' });
  owner.requestNotesData = () => {};
  owner.requestDailyBriefStationData = () => {};
  owner.render = () => {};
  owner.notesChanged = () => {};
  owner.container = { querySelector: () => null };
  owner.active = true;
  return { owner, card: owner.firstVisit };
}

test('brief uses the existing panel and refreshes canonical missions on close', async () => {
  const { owner, card } = fixture();
  card.missions = missionRows('completed');
  card.resolvedOnLoad = false;
  const trigger = {};
  owner.openDailyBriefPanel = value => assert.equal(value, trigger);
  let completed = false;
  owner.stationJSON = async () => ({
    missions: missionRows('completed').map(m =>
      m.id === FIRST_VISIT_MISSIONS.brief && completed ? { ...m, status: 'completed' } : m
    )
  });
  let focused = false;
  card.focus = () => {
    focused = true;
  };
  await card.act('brief', trigger);
  await card.briefClosed();
  assert.equal(card.view().steps[1].resolved, false, 'failed brief cannot tick the step');
  completed = true;
  await card.act('brief', trigger);
  await card.briefClosed();
  assert.equal(card.view().steps[1].resolved, true);
  assert.equal(card.view().layout.stationKeys, null, 'all normal stations stay available');
  assert.equal(focused, true);
});

test('last mission resolves to the finished card; already-resolved arrival records done silently', async () => {
  const { owner, card } = fixture();
  const all = missionRows('completed').map(m => ({
    ...m,
    status: m.id === FIRST_VISIT_MISSIONS.folder ? 'skipped' : 'completed'
  }));
  owner.stationJSON = async () => ({ missions: all });
  card.resolvedOnLoad = false;
  await card.load();
  assert.equal(card.view().card, 'finished');
  assert.match(card.html(), /Ready when you are/);
  const previous = globalThis.fetch;
  let posts = 0;
  globalThis.fetch = async () => {
    posts++;
    return { ok: true };
  };
  try {
    await card.act('done');
    assert.equal(card.view().card, 'none');
    const arriving = fixture();
    arriving.owner.stationJSON = async () => ({ missions: all });
    await arriving.card.load();
    assert.equal(arriving.card.state, 'done');
    assert.equal(arriving.card.html(), '');
    assert.equal(posts, 2);
  } finally {
    globalThis.fetch = previous;
  }
});

test('HQ card loads missions, escapes content and uses the real avatar presentation', async () => {
  const { owner, card } = fixture();
  assert.match(card.html(), /disabled/);
  assert.doesNotMatch(card.html(), /is-resolved/);
  const requests = [];
  owner.stationJSON = async path => {
    requests.push(path);
    return { missions: missionRows() };
  };
  await card.load();
  assert.deepEqual(requests, ['/api/progression']);
  writeNoteDraft({ text: '</textarea><script>', requestId: 'draft', sourceId: 'notes' });
  const html = card.html();
  assert.match(html, /face.png/);
  assert.match(html, /&lt;\/textarea&gt;&lt;script&gt;/);
  assert.equal((html.match(/aria-live="polite"/g) || []).length, 1);
  assert.match(html, /href="\/\?panel=today&folder=show"/);
});

test('pending suggestions keep Notes available before capture completion', () => {
  const { owner, card } = fixture();
  card.missions = missionRows();
  owner.hqNeedsYouHTML = () => '';
  owner.renderHQGitHubHost = () => '';
  owner.renderFollowUpsHost = () => '';
  owner.hqCards = () => [];
  owner.renderHQRail = () => '';
  assert.match(owner.renderHQDetails(), /data-cmd-hq-host="notes"/);
  card.missions = missionRows('completed');
  assert.match(owner.renderHQDetails(), /data-cmd-hq-host="notes"/);
  assert.match(owner.renderHQDetails(), /Your first note is in your Inbox/);
});

test('successful save refreshes missions and Notes without pushing another suggestion', async () => {
  const { owner, card } = fixture();
  card.missions = missionRows();
  card.resolvedOnLoad = false;
  writeNoteDraft({ text: 'A reminder', requestId: 'capture', sourceId: 'notes' });
  const actions = [];
  owner.stationJSON = async path =>
    path === '/api/progression' ? { missions: missionRows('completed') } : { second_brain: notes };
  owner.requestNotesData = force => actions.push(['notes', force]);
  card.focus = action => actions.push(['focus', action]);
  const previous = globalThis.fetch;
  globalThis.fetch = async () => ({
    ok: true,
    json: async () => ({ capture: { note: 'Inbox/reminder.md' } })
  });
  try {
    await card.save();
  } finally {
    globalThis.fetch = previous;
  }
  assert.equal(card.view().count, 1);
  assert.match(card.html(), /Saved to your Inbox: “A reminder”/);
  assert.deepEqual(actions, [
    ['notes', true],
    ['focus', undefined]
  ]);
});

test('a completed suggestion can retain card focus across asynchronous HQ repaints', () => {
  const { owner } = fixture();
  const previous = globalThis.document;
  const focus = [];
  const card = {
    hasAttribute: attr => attr === 'data-hq-firstvisit',
    getAttribute: () => '',
    focus: options => focus.push(options)
  };
  try {
    globalThis.document = { activeElement: card };
    owner.container = {
      contains: node => node === card,
      querySelectorAll: selector => {
        assert.equal(selector, '[data-hq-firstvisit]');
        return [card];
      }
    };
    owner.rememberHQFocus();
    owner.restoreHQFocus();
    assert.deepEqual(focus, [{ preventScroll: true }]);
  } finally {
    globalThis.document = previous;
  }
});

test('refreshing the mounted Inbox preserves its tab and draft, and ignores stale connections', async () => {
  const { owner } = fixture();
  const panel = {
    status: 'ready',
    tab: 'more',
    captureDraft: 'Do not lose this',
    inbox: { total: 0 }
  };
  owner._notesPanel = panel;
  owner.notesPanelOpen = () => true;
  const painted = [];
  owner.paintNotesPanel = value => painted.push(value);
  owner.stationJSON = async path => {
    assert.equal(path, '/api/personal-assistant/second-brain/inbox');
    return { inbox: { notes: [{ name: 'saved.md' }], total: 1 } };
  };
  await owner.refreshOpenNotesInbox();
  assert.equal(panel.inbox.total, 1);
  assert.equal(panel.tab, 'more');
  assert.equal(panel.captureDraft, 'Do not lose this');
  assert.deepEqual(painted, [panel]);
  owner.stationJSON = async () => {
    owner._notesPanel = { status: 'loading' };
    return { inbox: { total: 99 } };
  };
  await owner.refreshOpenNotesInbox();
  assert.equal(panel.inbox.total, 1);
  assert.equal(painted.length, 1);
  owner._notesPanel = panel;
  owner.stationJSON = async () => {
    throw Error('offline');
  };
  await assert.doesNotReject(() => owner.refreshOpenNotesInbox());
  assert.equal(panel.inbox.total, 1);
});

test('a refused save keeps the draft and server message, ready to retry', async () => {
  const { owner, card } = fixture();
  card.missions = missionRows();
  writeNoteDraft({ text: 'A reminder', requestId: 'capture', sourceId: 'notes' });
  owner.stationJSON = async () => ({ second_brain: notes });
  const previous = globalThis.fetch;
  globalThis.fetch = async () => ({
    ok: false,
    status: 400,
    json: async () => ({ error: 'Please shorten the note' })
  });
  try {
    await card.save();
  } finally {
    globalThis.fetch = previous;
  }
  assert.equal(card.busy, false);
  assert.equal(card.view().count, 0);
  assert.match(card.html(), /Please shorten the note/);
  assert.match(card.html(), />A reminder<\/textarea>/);
});

test('show everything and Continue stay usable without storage; Hide waits for server success', async () => {
  const { card } = fixture();
  card.missions = missionRows();
  await card.act('show-all');
  assert.equal(card.view().card, 'bar');
  await card.act('continue');
  assert.equal(card.view().card, 'full');
  const previous = globalThis.fetch;
  let ok = false;
  globalThis.fetch = async (path, options) => {
    assert.equal(path, '/api/personal-hq/first-visit-state');
    assert.equal(options.body, '{"state":"done"}');
    return { ok };
  };
  try {
    await card.act('hide');
    assert.equal(card.state, 'pending');
    assert.match(card.html(), /could not be dismissed/);
    ok = true;
    await card.act('hide');
    assert.equal(card.view().card, 'none');
  } finally {
    globalThis.fetch = previous;
  }
});

test('failed progression reads cannot mark progress complete and offer an explicit retry', async () => {
  const { owner, card } = fixture();
  owner.stationJSON = async () => {
    throw Error('offline');
  };
  await card.load();
  assert.equal(card.missions, null);
  assert.match(card.html(), /data-hq-firstvisit-action="retry"/);
  assert.equal(card.view().count, 0);
});

test('suggestions are unnumbered, dismissible and keep the note field behind explicit intent', async () => {
  const { card } = fixture();
  card.missions = missionRows();
  writeNoteDraft({ text: '', requestId: 'optional', sourceId: 'notes' });
  const html = card.html();
  assert.match(html, /Things to try/);
  assert.match(html, /Not now/);
  assert.doesNotMatch(html, /<textarea|aria-current|of 3|firstvisit-journey/);
  assert.ok(html.indexOf('Explore a folder with Ori') < html.indexOf('Read your first brief'));
  assert.match(html, /href="\/\?panel=today&folder=show&suggest=documents"/);
  const focused = [];
  card.focus = target => focused.push(target);
  await card.act('note');
  assert.match(card.html(), /<textarea/);
  assert.deepEqual(focused, ['note-input']);
});

test('Not now dismisses untouched suggestions without skipping or completing missions', async () => {
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
    await card.act('hide');
    assert.equal(card.state, 'done');
    assert.deepEqual(card.missions, before);
    assert.deepEqual(calls, [['/api/personal-hq/first-visit-state', { state: 'done' }]]);
  } finally {
    globalThis.fetch = previous;
  }
});

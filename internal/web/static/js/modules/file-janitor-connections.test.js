// Tests for file-janitor-connections.js: "Sends to", "Receives from" and the
// card that turns a connection on. Inline DOM stub, no jsdom.
//
// Run with: node --test internal/web/static/js/modules/file-janitor-connections.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ARRIVAL_PAGE,
  BANNED_WORDS,
  KINDS,
  arrivedTitle,
  arrivedWhen,
  carriedBy,
  kindsPhrase,
  preTicked,
  renderArrivals,
  renderConnectCard,
  renderReceivesFrom,
  renderSendsTo,
  workspaceName
} from './file-janitor-connections.js';

class El {
  constructor(tag) {
    this.tagName = String(tag).toUpperCase();
    this.children = [];
    this.attrs = {};
    this.listeners = {};
    this.className = '';
    this._text = '';
    this.disabled = false;
    this.checked = false;
    this.value = '';
  }
  get textContent() {
    return [this._text, ...this.children.map(child => child.textContent)].filter(Boolean).join(' ');
  }
  set textContent(value) {
    this._text = value;
  }
  appendChild(child) {
    this.children.push(child);
    return child;
  }
  setAttribute(key, value) {
    this.attrs[key] = value;
  }
  getAttribute(key) {
    return this.attrs[key];
  }
  addEventListener(event, fn) {
    (this.listeners[event] ||= []).push(fn);
  }
  fire(event) {
    (this.listeners[event] || []).forEach(fn => fn());
  }
  all(predicate, out = []) {
    if (predicate(this)) out.push(this);
    this.children.forEach(child => child.all(predicate, out));
    return out;
  }
}
const doc = { createElement: tag => new El(tag) };
const buttons = node => node.all(item => item.tagName === 'BUTTON');
const buttonNamed = (node, label) => buttons(node).find(item => item.textContent === label);
const byId = (node, id) => node.all(item => item.id === id)[0];
const checkbox = (node, kind) => byId(node, 'fileJanitorConnectKind-' + kind);

const onView = (over = {}) => ({
  id: 'connection-1',
  sender_workspace_id: 'ws-1',
  sender_name: 'Downloads',
  receiver_workspace_id: 'ws-2',
  receiver_name: 'Documents',
  kinds: ['documents'],
  on: true,
  ...over
});
const connections = (over = {}) => ({
  workspace_name: 'Downloads',
  sends_to: [],
  receives_from: [],
  receivers: [{ workspace_id: 'ws-2', name: 'Documents' }],
  ...over
});

function assertNoBannedWords(node) {
  const words = node.textContent.toLowerCase();
  BANNED_WORDS.forEach(word => {
    assert.ok(
      !new RegExp('\\b' + word + '(e?s)?\\b').test(words),
      `uses the banned word "${word}": ${node.textContent}`
    );
  });
}

test('with no connection, Sends to says what it does and offers Connect a workspace (FR 4)', () => {
  let started = 0;
  const section = renderSendsTo(doc, connections(), { onConnectStart: () => started++ });
  assert.match(section.textContent, /Sends to/);
  assert.match(section.textContent, /One way only, and nothing moves until you press File/);
  const start = buttonNamed(section, 'Connect a workspace');
  assert.ok(start, 'Connect a workspace is a real button');
  assert.equal(start.getAttribute('type'), 'button');
  start.fire('click');
  assert.equal(started, 1);
  assertNoBannedWords(section);
});

test('a connection that is on says where its kinds go and has Turn off (FR 5, FR 11)', () => {
  const turnedOff = [];
  const section = renderSendsTo(
    doc,
    connections({ sends_to: [onView({ kinds: ['documents', 'data'] })] }),
    { onTurnOff: view => turnedOff.push(view.id) }
  );
  assert.match(section.textContent, /Documents/);
  assert.match(
    section.textContent,
    /On · Documents and Data files you approve on Review go to the top level of Documents\./
  );
  const off = buttonNamed(section, 'Turn off');
  assert.equal(off.getAttribute('aria-label'), 'Turn off sending to Documents');
  off.fire('click');
  assert.deepEqual(turnedOff, ['connection-1']);
  assertNoBannedWords(section);
});

test('an ended connection says why and offers Remove (FR 10)', () => {
  const section = renderSendsTo(
    doc,
    connections({
      sends_to: [
        onView({ on: false, ended: 'Ended because Documents now points at a different folder.' })
      ]
    }),
    {}
  );
  assert.match(section.textContent, /Ended because Documents now points at a different folder\./);
  assert.ok(buttonNamed(section, 'Remove'));
  assert.ok(!buttonNamed(section, 'Turn off'));
});

test('while a turn-off runs, every connection control waits', () => {
  const section = renderSendsTo(doc, connections({ sends_to: [onView()] }), {
    busyId: 'connection-1'
  });
  assert.ok(buttonNamed(section, 'Turning off…').disabled);
  // With one connection on, the mockup's wording: "Connect another workspace".
  assert.ok(buttonNamed(section, 'Connect another workspace').disabled);
});

test('Receives from lists who sends here with Turn off, and is absent when nobody does (FR 41)', () => {
  assert.equal(renderReceivesFrom(doc, connections(), {}), null);
  const turnedOff = [];
  const section = renderReceivesFrom(
    doc,
    connections({ workspace_name: 'Documents', receives_from: [onView()] }),
    { onTurnOff: view => turnedOff.push(view.id) }
  );
  assert.match(section.textContent, /Receives from/);
  assert.match(
    section.textContent,
    /On · Downloads can send Documents files here\. Each send waits for approval there\./
  );
  const off = buttonNamed(section, 'Turn off');
  assert.equal(off.getAttribute('aria-label'), 'Turn off receiving from Downloads');
  off.fire('click');
  assert.deepEqual(turnedOff, ['connection-1']);
  assertNoBannedWords(section);
});

test('the card shows both workspaces one way, the two lists and the two facts, in the mockup words (FR 5)', () => {
  const card = renderConnectCard(doc, connections(), { receiverId: 'ws-2', kinds: [] }, {});
  const text = card.textContent;
  assert.match(text, /Let Downloads send files to Documents\?/);
  assert.match(
    text,
    /One way, between these two folders only\. Putting workspaces in a group never turns this on\./
  );
  const diagram = card.all(item => item.className === 'fj-connect-diagram')[0];
  assert.equal(
    diagram.getAttribute('aria-label'),
    'Downloads sends to Documents, one way. Nothing comes back.'
  );
  assert.match(text, /What this allows/);
  assert.match(text, /Moving files you approve from Downloads into Documents\./);
  assert.match(text, /What it never allows/);
  assert.match(text, /Reading, renaming, replacing or deleting anything already in Documents\./);
  assert.match(text, /Sending anything from Documents back to Downloads\./);
  assert.match(text, /Moving a file you did not approve\./);
  assert.match(
    text,
    /Your approval Every send waits for you on Review\. Undo all puts sent files back in Downloads\./
  );
  assert.match(
    text,
    /Turning it off From either workspace, at any time\. Files already sent stay where they are\./
  );
  assert.doesNotMatch(text, /suggest/i, 'with no model, nothing about the agent is shown (FR 42)');
  assertNoBannedWords(card);
});

test('the card names the agent only when it can suggest (FR 42)', () => {
  const card = renderConnectCard(
    doc,
    connections(),
    { receiverId: 'ws-2', kinds: [] },
    { agentName: 'File Curator' }
  );
  assert.match(
    card.textContent,
    /File Curator suggesting which files to send\. A suggestion moves nothing\./
  );
});

test('the card offers every kind as a labelled checkbox and needs at least one ticked (FR 6, FR 52)', () => {
  const allowed = [];
  const empty = renderConnectCard(
    doc,
    connections(),
    { receiverId: 'ws-2', kinds: [] },
    { onConnectAllow: (id, kinds) => allowed.push([id, kinds]) }
  );
  assert.equal(empty.all(item => item.tagName === 'INPUT').length, KINDS.length);
  KINDS.forEach(kind => {
    const box = checkbox(empty, kind.id);
    assert.equal(box.type, 'checkbox');
    assert.equal(box.checked, false, `${kind.id} is not pre-ticked for an ordinary workspace`);
  });
  const allowEmpty = byId(empty, 'fileJanitorConnectAllow');
  assert.equal(allowEmpty.textContent, 'Allow sending to Documents');
  assert.ok(allowEmpty.disabled, 'nothing ticked: Allow waits');
  assert.match(empty.textContent, /Tick at least one kind of file to send\./);
  allowEmpty.fire('click');
  assert.equal(allowed.length, 0);

  const ticked = renderConnectCard(
    doc,
    connections(),
    { receiverId: 'ws-2', kinds: ['documents'] },
    { onConnectAllow: (id, kinds) => allowed.push([id, kinds]) }
  );
  const allow = byId(ticked, 'fileJanitorConnectAllow');
  assert.ok(!allow.disabled);
  allow.fire('click');
  assert.deepEqual(allowed, [['ws-2', ['documents']]]);
});

test('Documents is pre-ticked only for a workspace made from the guided Documents suggestion (FR 6)', () => {
  assert.deepEqual(preTicked({ workspace_id: 'ws-2', name: 'Documents', guided_documents: true }), [
    'documents'
  ]);
  assert.deepEqual(preTicked({ workspace_id: 'ws-2', name: 'Documents' }), []);
  assert.deepEqual(preTicked(null), []);
});

test('a kind another connection already carries is shown taken, with where it goes (FR 7)', () => {
  const state = connections({
    sends_to: [onView({ receiver_workspace_id: 'ws-3', receiver_name: 'Papers', kinds: ['data'] })],
    receivers: [{ workspace_id: 'ws-2', name: 'Documents' }]
  });
  assert.deepEqual(carriedBy(state, 'ws-2'), { data: 'Papers' });
  const card = renderConnectCard(doc, state, { receiverId: 'ws-2', kinds: ['data'] }, {});
  const data = checkbox(card, 'data');
  assert.ok(data.disabled);
  assert.equal(data.checked, false);
  assert.match(card.textContent, /Already goes to Papers/);
  assert.ok(
    byId(card, 'fileJanitorConnectAllow').disabled,
    'only a taken kind ticked: Allow waits'
  );
});

test('a receiver Ori already knows cannot receive says why, and Allow waits (FR 9)', () => {
  const state = connections({
    receivers: [
      {
        workspace_id: 'ws-2',
        name: 'Documents',
        refusal: "Documents's Filing is paused. Turn it back on there first."
      }
    ]
  });
  const card = renderConnectCard(doc, state, { receiverId: 'ws-2', kinds: ['documents'] }, {});
  const note = byId(card, 'fileJanitorConnectRefusal');
  assert.equal(note.getAttribute('role'), 'alert');
  assert.match(note.textContent, /is paused/);
  assert.ok(byId(card, 'fileJanitorConnectAllow').disabled);
});

test("the server's refusal sentence is shown on the card", () => {
  const card = renderConnectCard(
    doc,
    connections(),
    {
      receiverId: 'ws-2',
      kinds: ['documents'],
      error:
        'Downloads and Documents are on different disks. Ori only sends a file by moving it on one disk, never by copying it.'
    },
    {}
  );
  assert.match(byId(card, 'fileJanitorConnectRefusal').textContent, /different disks/);
});

test('with several receivers, a labelled select chooses one', () => {
  const changed = [];
  const state = connections({
    receivers: [
      { workspace_id: 'ws-2', name: 'Documents' },
      { workspace_id: 'ws-3', name: 'Papers' }
    ]
  });
  const card = renderConnectCard(
    doc,
    state,
    { receiverId: 'ws-3', kinds: [] },
    { onReceiverChange: id => changed.push(id) }
  );
  const select = byId(card, 'fileJanitorConnectReceiver');
  const label = card.all(
    item => item.tagName === 'LABEL' && item.getAttribute('for') === 'fileJanitorConnectReceiver'
  )[0];
  assert.equal(label.textContent, 'Send to');
  assert.match(card.textContent, /Let Downloads send files to Papers\?/);
  select.value = 'ws-2';
  select.fire('change');
  assert.deepEqual(changed, ['ws-2']);
});

test('with no workspace that can receive, the card says how to get one', () => {
  const card = renderConnectCard(
    doc,
    connections({ receivers: [] }),
    { receiverId: '', kinds: [] },
    {}
  );
  assert.match(card.textContent, /No other workspace has Filing set up on its folder yet\./);
  assert.ok(buttonNamed(card, 'Close'));
});

test('workspace names are text: a direction override cannot disguise them', () => {
  assert.equal(workspaceName('Docs‮fdp.exe'), 'Docsfdp.exe');
  assert.equal(workspaceName(''), 'That workspace');
  assert.equal(kindsPhrase(['documents', 'images', 'other']), 'Documents, Images and Other');
  assert.equal(kindsPhrase([]), '');
});

// ---------------------------------------------------------------- arrivals

const arrival = (over = {}) => ({
  id: 'arrival-1',
  sender_workspace_id: 'ws-1',
  sender_name: 'Downloads',
  arrived_at: '2026-11-26T10:42:00Z',
  count: 14,
  names: Array.from({ length: 14 }, (_, i) => 'Invoice-' + i + '.pdf'),
  ...over
});

test('the strip says how many arrived from whom, when, and that they are loose here (FR 36)', () => {
  const section = renderArrivals(doc, [arrival()], { now: new Date('2026-11-26T12:00:00Z') });
  assert.match(section.textContent, /14 files arrived from Downloads/);
  assert.match(
    section.textContent,
    /You sent them from the Downloads review\. They are loose at the top level until you file them here\./
  );
  assert.equal(arrivedTitle(arrival({ count: 1 })), '1 file arrived from Downloads');
  assert.match(arrivedWhen('2026-11-26T10:42:00Z', new Date('2026-11-26T12:00:00Z')), /^today, /);
  assert.doesNotMatch(section.textContent, /linked|batch|candidate|proposal|draft/i);
});

test('the list shows five names at a time, each from the sender, with and N more (FR 37)', () => {
  const more = [];
  const section = renderArrivals(doc, [arrival()], { onShowMore: a => more.push(a.id) });
  const items = section.all(item => item.className === 'fj-arrival-file');
  assert.equal(items.length, ARRIVAL_PAGE);
  assert.match(items[0].textContent, /Invoice-0\.pdf from Downloads/);
  const button = buttonNamed(section, 'and 9 more');
  button.fire('click');
  assert.deepEqual(more, ['arrival-1']);
  const wider = renderArrivals(doc, [arrival()], { shown: { 'arrival-1': 10 } });
  assert.equal(wider.all(item => item.className === 'fj-arrival-file').length, 10);
});

test('Undo this send and Dismiss are real buttons that wait while one runs (FR 38, 39)', () => {
  const calls = [];
  const section = renderArrivals(doc, [arrival()], {
    onUndo: a => calls.push('undo ' + a.id),
    onDismiss: a => calls.push('dismiss ' + a.id)
  });
  const undo = byId(section, 'fileJanitorUndoSend-arrival-1');
  assert.equal(undo.textContent, 'Undo this send');
  assert.equal(undo.getAttribute('type'), 'button');
  undo.fire('click');
  byId(section, 'fileJanitorDismissArrival-arrival-1').fire('click');
  assert.deepEqual(calls, ['undo arrival-1', 'dismiss arrival-1']);
  const busy = renderArrivals(doc, [arrival()], { busyId: 'arrival-1' });
  assert.equal(byId(busy, 'fileJanitorUndoSend-arrival-1').textContent, 'Putting files back…');
  assert.ok(byId(busy, 'fileJanitorDismissArrival-arrival-1').disabled);
});

test('local review is a separate user action and requires no receiving agent', () => {
  const calls = [];
  const section = renderArrivals(doc, [arrival()], { onReview: a => calls.push(a.id) });
  assert.match(section.textContent, /confirm separately/);
  assert.deepEqual(calls, []);
  buttonNamed(section, 'Review local filing').fire('click');
  assert.deepEqual(calls, ['arrival-1']);
});

test('nothing arrived draws nothing; a result message is still said', () => {
  assert.equal(renderArrivals(doc, [], {}), null);
  const said = renderArrivals(doc, [], { message: '14 files are back in Downloads.' });
  assert.match(said.textContent, /14 files are back in Downloads\./);
});

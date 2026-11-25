import assert from 'node:assert/strict';
import { test } from 'node:test';
import { HQCaptureDraft } from './hq-quick-capture.js';
import {
  inboxHasUserNotes,
  QUICK_NOTE_DESTINATION,
  QUICK_NOTE_RECEIPT,
  quickNoteEntry
} from './my-notes-quick-note.js';

const REF = 'a'.repeat(64);
const NEW_REF = 'b'.repeat(64);
const created = (ref = REF) => ({
  status: 'connected',
  browse_ref: ref,
  source: { id: 's', name: 'My Notes', origin: 'created_by_ori' }
});
const external = { ...created(), source: { id: 'v', name: 'Vault', origin: 'connected' } };

test('the destination and receipt name My Notes → Inbox, as HQ capture does', () => {
  assert.equal(QUICK_NOTE_DESTINATION, 'My Notes → Inbox');
  assert.equal(QUICK_NOTE_RECEIPT, new HQCaptureDraft().receipt || 'Saved to My Notes → Inbox.');
});

test('Quick note is offered only where Ori may write', () => {
  const draft = new HQCaptureDraft('my-notes');
  assert.deepEqual(quickNoteEntry({ view: created(), draft }), {
    label: 'Quick note',
    stranded: false
  });
  assert.equal(
    quickNoteEntry({ view: created(), draft, firstNote: true }).label,
    'Write your first note'
  );
  for (const view of [
    external,
    { ...created(), status: 'missing', browse_ref: '' },
    { status: 'not_connected' },
    { status: 'not_connected', setup_required: 'hq' },
    undefined
  ]) {
    assert.equal(quickNoteEntry({ view, draft }), null, JSON.stringify(view));
  }
});

test('a draft written for an earlier connection waits for a decision', () => {
  const draft = new HQCaptureDraft('my-notes');
  draft.edit('Ask Maya about disposal.', REF);
  assert.equal(quickNoteEntry({ view: created(REF), draft }).stranded, false);
  for (const view of [created(NEW_REF), external, { status: 'not_connected' }]) {
    assert.deepEqual(quickNoteEntry({ view, draft }), { label: 'Unsent note', stranded: true });
  }
});

test('first-note state comes from Inbox kinds, not from walking the notebook', () => {
  assert.equal(inboxHasUserNotes({ notes: [], total: 0 }), false);
  assert.equal(
    inboxHasUserNotes({
      notes: [
        { name: 'Start here.md', kind: 'seed' },
        { name: 'Call the plumber.md', kind: 'follow_up' }
      ],
      total: 2
    }),
    false,
    'setup and follow-up notes are Ori’s, not the person’s'
  );
  assert.equal(inboxHasUserNotes({ notes: [{ kind: 'capture' }], total: 1 }), true);
  assert.equal(inboxHasUserNotes({ notes: [{ kind: 'user' }], total: 1 }), true);
  assert.equal(inboxHasUserNotes({ notes: [{ kind: 'seed' }], total: 1, partial: true }), true);
  assert.equal(inboxHasUserNotes({ notes: [{ kind: 'seed' }], total: 3 }), true);
  assert.equal(inboxHasUserNotes(null), false);
});

function sender(results) {
  const calls = [];
  const send = async (path, body, id) => {
    calls.push({ path, body, id });
    const next = results.shift();
    if (next instanceof Error) throw next;
    return next;
  };
  return { calls, send };
}

test('a save sends one request with the connection it was written for', async () => {
  const draft = new HQCaptureDraft('my-notes');
  draft.edit('Ask Maya about disposal.', REF);
  const { calls, send } = sender([{ capture: { note: 'Inbox/20261125-0900-ask-maya.md' } }]);
  assert.equal(await draft.save(REF, send), true);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].path, '/capture');
  assert.deepEqual(calls[0].body, { text: 'Ask Maya about disposal.', source_ref: REF });
  assert.match(calls[0].id, /^my-notes-/);
  assert.equal(draft.text, '');
  assert.equal(draft.receipt, QUICK_NOTE_RECEIPT);
});

test('a second submit while saving sends nothing more', async () => {
  const draft = new HQCaptureDraft('my-notes');
  draft.edit('One thought', REF);
  let release;
  const calls = [];
  const send = (path, body, id) => {
    calls.push(id);
    return new Promise(resolve => {
      release = () => resolve({ capture: { note: 'Inbox/one-thought.md' } });
    });
  };
  const first = draft.save(REF, send);
  assert.equal(await draft.save(REF, send), false);
  draft.edit('typed while saving', REF);
  assert.equal(draft.text, 'One thought', 'the field is read-only while saving');
  release();
  assert.equal(await first, true);
  assert.equal(calls.length, 1);
});

test('a failed save keeps the draft and retries as the same request', async () => {
  const draft = new HQCaptureDraft('my-notes');
  draft.edit('Ask Maya about disposal.', REF);
  const { calls, send } = sender([
    new Error('Notes are temporarily unavailable'),
    { capture: { note: 'Inbox/ask-maya.md' } }
  ]);
  assert.equal(await draft.save(REF, send), false);
  assert.equal(draft.text, 'Ask Maya about disposal.');
  assert.equal(draft.problem, 'Notes are temporarily unavailable');
  assert.equal(await draft.save(REF, send), true);
  assert.equal(calls.length, 2);
  assert.equal(calls[0].id, calls[1].id, 'the server can recognise the retry');
});

test('a draft never goes to a new folder unless the person keeps it there', async () => {
  const draft = new HQCaptureDraft('my-notes');
  draft.edit('Ask Maya about disposal.', REF);
  const { calls, send } = sender([
    new Error('first attempt'),
    { capture: { note: 'Inbox/ask-maya.md' } }
  ]);
  await draft.save(REF, send);
  const oldID = calls[0].id;
  assert.equal(await draft.save(NEW_REF, send), false);
  assert.equal(calls.length, 1, 'nothing is sent to the new connection');
  assert.match(draft.problem, /connection changed/);

  assert.equal(draft.retarget(''), false);
  assert.equal(draft.retarget(NEW_REF), true);
  assert.equal(await draft.save(NEW_REF, send), true);
  assert.equal(calls[1].body.source_ref, NEW_REF);
  assert.notEqual(calls[1].id, oldID, 'a new destination is a new request');

  assert.equal(new HQCaptureDraft().retarget(NEW_REF), false, 'nothing to keep');
});

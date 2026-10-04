import test from 'node:test';
import assert from 'node:assert/strict';

import {
  canSaveDraft,
  characterCount,
  draftCounts,
  draftSavePayload,
  draftUpdatePayload,
  pickWorkingDraft,
  receiptSummary,
  resumePlan,
  saveFailure,
  savedDraftLabel,
  updateFailure,
  updateReceiptSummary,
  workingDraftNote
} from './personal-assistant-drafts.js';

const limits = { title_chars: 300, body_chars: 100000 };
const review = {
  operation_id: 'op-1',
  title: '생일 축하',
  body: '미나야, 생일 정말 축하해! 🎂',
  target: { workspace_id: 'hq-1', name: 'My HQ' },
  source: { conversation_id: 'conv-1', message_id: 'msg-4' },
  limits
};

test('characters are counted by code point, like the server', () => {
  assert.equal(characterCount('abc'), 3);
  assert.equal(characterCount('미나야'), 3);
  assert.equal(characterCount('🎂'), 1);
  assert.equal(characterCount(''), 0);
  assert.equal(characterCount(null), 0);
});

test('the form reports empty, oversized, and multi-line values', () => {
  const ok = draftCounts(' 생일 축하 ', '\n미나야 🎂\n', limits);
  assert.equal(ok.titleChars, 5);
  assert.equal(ok.bodyChars, 5);
  assert.equal(ok.titleOver || ok.bodyOver || ok.titleEmpty || ok.bodyEmpty, false);

  assert.equal(draftCounts('   ', 'body', limits).titleEmpty, true);
  assert.equal(draftCounts('title', ' \n ', limits).bodyEmpty, true);
  assert.equal(draftCounts('가'.repeat(301), 'body', limits).titleOver, true);
  assert.equal(draftCounts('가'.repeat(300), 'body', limits).titleOver, false);
  assert.equal(draftCounts('title', '가'.repeat(100001), limits).bodyOver, true);
  assert.equal(draftCounts('one\ntwo', 'body', limits).titleMultiline, true);
  // Missing limits fall back to the canonical Ticket limits, never to "no limit".
  assert.equal(draftCounts('가'.repeat(301), 'body', null).titleOver, true);
});

test('Save is available only for a valid form that is not already saving', () => {
  assert.equal(canSaveDraft('title', 'body', limits, false), true);
  assert.equal(canSaveDraft('title', 'body', limits, true), false);
  assert.equal(canSaveDraft('', 'body', limits, false), false);
  assert.equal(canSaveDraft('title', '', limits, false), false);
  assert.equal(canSaveDraft('a\nb', 'body', limits, false), false);
  assert.equal(canSaveDraft('title', '가'.repeat(100001), limits, false), false);
});

test('the save sends the form under the review’s own operation ID and target', () => {
  const payload = draftSavePayload(review, 'Edited title', 'Edited body');
  assert.deepEqual(payload, {
    operation_id: 'op-1',
    title: 'Edited title',
    body: 'Edited body',
    target_workspace_id: 'hq-1',
    source: { conversation_id: 'conv-1', message_id: 'msg-4' }
  });
  // A retry of the same review is the same operation: nothing is regenerated.
  assert.equal(draftSavePayload(review, 'Edited title', 'Edited body').operation_id, 'op-1');
  // The body is sent as typed; the server applies the canonical normalization.
  assert.equal(draftSavePayload(review, 't', '  spaced  ').body, '  spaced  ');
});

test('a receipt claims only what the Ticket shows', () => {
  const created = receiptSummary({
    created: true,
    workspace_name: 'My HQ',
    display_number: '#4',
    state_label: 'Backlog',
    assigned: false,
    scheduled: false
  });
  assert.equal(created, 'Saved to My HQ backlog as #4. It is unassigned and not scheduled.');

  const replay = receiptSummary({ created: false, workspace_name: 'My HQ', display_number: '#4' });
  assert.equal(replay, 'Already saved to My HQ as #4. Nothing was saved twice.');

  const edited = receiptSummary({
    created: false,
    changed_since: true,
    workspace_name: 'My HQ',
    display_number: '#4'
  });
  assert.match(edited, /edited since it was saved/);

  // If the Ticket is somehow assigned or scheduled, the receipt does not say otherwise.
  const assigned = receiptSummary({
    created: true,
    workspace_name: 'My HQ',
    display_number: '#4',
    assigned: true
  });
  assert.doesNotMatch(assigned, /unassigned/);
  // No receipt promises a reminder or a schedule.
  for (const text of [created, replay, edited, assigned]) {
    assert.doesNotMatch(text, /remind|will be sent|scheduled for/i);
  }
});

test('a field error keeps the form editable and names the field', () => {
  const failure = saveFailure(422, {
    error: 'invalid_draft',
    field: 'title',
    message: 'title is required. Nothing was saved.'
  });
  assert.deepEqual(failure, {
    kind: 'field',
    field: 'title',
    message: 'title is required. Nothing was saved.'
  });
});

test('an unknown outcome is reported as unknown and retried as the same save', () => {
  const lost = saveFailure(0, null);
  assert.equal(lost.kind, 'retry');
  assert.match(lost.message, /Could not confirm whether this was saved/);
  assert.match(lost.message, /never applied twice/);
  assert.doesNotMatch(lost.message, /was not saved|Nothing was saved/);

  const unavailable = saveFailure(503, {
    error: 'draft_save_unavailable',
    message:
      'The backlog could not be written right now. Nothing was saved; your draft is still here.'
  });
  assert.equal(unavailable.kind, 'retry');
  assert.match(unavailable.message, /Nothing was saved/);

  assert.equal(saveFailure(500, null).kind, 'retry');
});

test('a refused review is blocked, and a conflict points at what is already saved', () => {
  const conflict = saveFailure(409, {
    error: 'operation_conflict',
    saved: { ticket_id: 't-1', display_number: '#4', href: '/workspaces/my-hq?ticket=t-1' }
  });
  assert.equal(conflict.kind, 'blocked');
  assert.equal(conflict.saved.ticket_id, 't-1');
  assert.match(conflict.message, /already saved as #4/);
  assert.match(conflict.message, /Your changes here were not saved/);

  for (const code of [
    'target_changed',
    'conversation_not_found',
    'source_message_not_found',
    'assistant_not_ready'
  ]) {
    const failure = saveFailure(409, { error: code, message: `${code} message` });
    assert.equal(failure.kind, 'blocked');
    assert.equal(failure.message, `${code} message`);
  }
});

// --- resume and update ---

const savedDraft = {
  ticket_id: 't-5',
  display_number: '#5',
  title: 'Birthday greeting',
  state: 'backlog',
  state_label: 'Backlog',
  version: 3,
  editable: true,
  matches_source: true,
  newer_replies: 0,
  message_id: 'msg-2',
  href: '/workspaces/my-hq?ticket=t-5'
};

test('an update is sent against the version the user reviewed, never without one', () => {
  const update = { current: { ...savedDraft, version: 3 }, target: { workspace_id: 'hq-1' } };
  assert.deepEqual(draftUpdatePayload(update, 'New title', 'New text'), {
    if_version: 3,
    title: 'New title',
    body: 'New text',
    target_workspace_id: 'hq-1'
  });
  // No reviewed version is sent as 0, which the server refuses.
  assert.equal(draftUpdatePayload({ target: { workspace_id: 'hq-1' } }, 't', 'b').if_version, 0);
  // The payload cannot carry state, owner, assignee, or schedule.
  assert.deepEqual(Object.keys(draftUpdatePayload(update, 't', 'b')).sort(), [
    'body',
    'if_version',
    'target_workspace_id',
    'title'
  ]);
});

test('an update receipt says only the title and text changed', () => {
  assert.equal(
    updateReceiptSummary({ applied: true, display_number: '#5', workspace_name: 'My HQ' }),
    'Updated #5 in My HQ. Only its title and text changed.'
  );
  assert.equal(
    updateReceiptSummary({ applied: false, display_number: '#5', workspace_name: 'My HQ' }),
    '#5 already has this text. Nothing was changed twice.'
  );
});

test('a stale update keeps the proposal and returns the current saved version', () => {
  const stale = updateFailure(409, {
    error: 'saved_draft_changed',
    current: { ...savedDraft, version: 4, body: 'Edited in HQ' }
  });
  assert.equal(stale.kind, 'stale');
  assert.equal(stale.current.version, 4);
  assert.equal(stale.current.body, 'Edited in HQ');
  assert.match(stale.message, /Nothing was overwritten/);
  // Without the current version there is nothing safe to re-review against.
  assert.equal(updateFailure(409, { error: 'saved_draft_changed' }).kind, 'blocked');
});

test('other update failures are a field fix, a safe retry, or a stop', () => {
  assert.deepEqual(
    updateFailure(422, { error: 'invalid_draft', field: 'body', message: 'the draft is empty.' }),
    { kind: 'field', field: 'body', message: 'the draft is empty.' }
  );
  const lost = updateFailure(0, null);
  assert.equal(lost.kind, 'retry');
  assert.match(lost.message, /Could not confirm whether this was updated/);
  assert.match(lost.message, /never applied twice/);
  assert.equal(updateFailure(503, { error: 'draft_save_unavailable' }).kind, 'retry');
  for (const code of ['saved_draft_not_found', 'saved_draft_not_editable', 'target_changed']) {
    const failure = updateFailure(409, { error: code, message: `${code} message` });
    assert.equal(failure.kind, 'blocked');
    assert.equal(failure.message, `${code} message`);
  }
});

test('a saved reply is labelled with how its saved copy has moved on', () => {
  assert.equal(savedDraftLabel(savedDraft), 'Saved as #5');
  assert.equal(
    savedDraftLabel({ ...savedDraft, matches_source: false }),
    'Saved as #5 · changed in Personal HQ'
  );
  assert.equal(
    savedDraftLabel({ ...savedDraft, state: 'ready', state_label: 'Ready' }),
    'Saved as #5 · Ready'
  );
  assert.equal(
    savedDraftLabel({ ...savedDraft, state: 'done', state_label: 'Done', matches_source: false }),
    'Saved as #5 · Done · changed in Personal HQ'
  );
});

test('the tab keeps its working draft, else takes the latest saved one', () => {
  const older = { ...savedDraft, ticket_id: 't-4', display_number: '#4' };
  const list = [older, savedDraft];
  assert.equal(pickWorkingDraft(list, 't-4').ticket_id, 't-4');
  assert.equal(pickWorkingDraft(list, '').ticket_id, 't-5');
  // A working draft from another conversation is not carried into this one.
  assert.equal(pickWorkingDraft(list, 't-99').ticket_id, 't-5');
  assert.equal(pickWorkingDraft([], 't-5'), null);
  assert.equal(pickWorkingDraft(null, 't-5'), null);
});

test('the working-draft line says what is and is not saved', () => {
  assert.match(workingDraftNote(savedDraft), /Working on #5 “Birthday greeting”/);
  assert.match(workingDraftNote(savedDraft), /not saved until you choose Update saved draft/);
  assert.match(
    workingDraftNote({ ...savedDraft, newer_replies: 2 }),
    /2 newer replies here are not saved/
  );
  assert.match(
    workingDraftNote({ ...savedDraft, newer_replies: 1 }),
    /1 newer reply here is not saved/
  );
  assert.match(
    workingDraftNote({ ...savedDraft, matches_source: false }),
    /changed in Personal HQ and differs from the reply/
  );
  assert.match(
    workingDraftNote({ ...savedDraft, editable: false, state_label: 'In Progress' }),
    /It is In Progress now, so it is edited in Personal HQ/
  );
  assert.equal(workingDraftNote(null), '');
});

test('a saved draft opens its conversation, or offers a new one when it is gone', () => {
  const live = resumePlan({ draft: savedDraft, conversation: { id: 'conv-1', available: true } });
  assert.equal(live.kind, 'conversation');
  assert.equal(live.conversationId, 'conv-1');

  const gone = resumePlan({
    draft: savedDraft,
    conversation: { id: 'conv-1', available: false, reason: 'conversation_not_found' }
  });
  assert.equal(gone.kind, 'new');
  assert.equal(gone.draft.ticket_id, 't-5');
  assert.match(gone.message, /is gone/);
  assert.match(gone.message, /still in Personal HQ/);
  // Nothing in the plan recreates the conversation.
  assert.equal('conversationId' in gone, false);

  const unreadable = resumePlan({
    draft: savedDraft,
    conversation: { id: 'conv-1', available: false, reason: 'conversation_unavailable' }
  });
  assert.match(unreadable.message, /could not be read right now/);

  assert.equal(resumePlan({ conversation: { id: 'conv-1', available: true } }).kind, 'unavailable');
  assert.equal(resumePlan(null).kind, 'unavailable');
});

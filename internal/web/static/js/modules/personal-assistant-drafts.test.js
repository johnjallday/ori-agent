import test from 'node:test';
import assert from 'node:assert/strict';

import {
  canSaveDraft,
  characterCount,
  draftCounts,
  draftSavePayload,
  receiptSummary,
  saveFailure
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

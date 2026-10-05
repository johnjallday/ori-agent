import test from 'node:test';
import assert from 'node:assert/strict';

import {
  CONVERSATION_ERRORS,
  conversationLabel,
  conversationNotice,
  conversationRequest,
  isPanelRequest,
  nextConversationId,
  shouldRestoreInput,
  tagMessageRow
} from './personal-assistant-conversation.js';

test('a request carries only an opaque conversation ID, or none for a new conversation', () => {
  assert.deepEqual(conversationRequest(''), { id: '' });
  assert.deepEqual(conversationRequest(null), { id: '' });
  assert.deepEqual(conversationRequest('  conv-1 '), { id: 'conv-1' });
  assert.deepEqual(Object.keys(conversationRequest('conv-1')), ['id']);
});

test('only the Personal Assistant panel sends a conversation', () => {
  assert.equal(isPanelRequest({ origin: 'personal_assistant_panel' }), true);
  assert.equal(isPanelRequest({ origin: 'ask_ori' }), false);
  assert.equal(isPanelRequest({ origin: 'ask_ori_panel', session_id: 'tab-session' }), false);
  assert.equal(isPanelRequest(null), false);
});

test('the tab keeps its thread across replies and drops a refused one', () => {
  assert.equal(nextConversationId('', { id: 'conv-1', started: true, stored: true }), 'conv-1');
  assert.equal(nextConversationId('conv-1', { id: 'conv-1', stored: true }), 'conv-1');
  // A turn that was not stored (model failure) keeps the thread it was in.
  assert.equal(nextConversationId('conv-1', { id: 'conv-1', stored: false }), 'conv-1');
  assert.equal(nextConversationId('conv-1', { stored: false }), 'conv-1');
  // A refused thread is never retried: the next message starts a new one.
  assert.equal(nextConversationId('conv-1', { error: 'conversation_not_found' }), '');
  assert.equal(nextConversationId('conv-1', { error: 'conversation_out_of_scope' }), '');
  // A history read that failed is not a dead thread: the retry stays in it,
  // instead of quietly starting a new conversation with no history.
  assert.equal(nextConversationId('conv-1', { error: 'conversation_unavailable' }), 'conv-1');
  // A reply with no conversation at all leaves the tab alone.
  assert.equal(nextConversationId('conv-1', null), 'conv-1');
});

test('unsent text comes back only when the turn was refused or unanswered', () => {
  assert.equal(
    shouldRestoreInput({ model_unavailable: true, conversation: { stored: false } }),
    true
  );
  assert.equal(shouldRestoreInput({ conversation: { error: 'conversation_not_found' } }), true);
  assert.equal(shouldRestoreInput({ conversation: { id: 'c', stored: true } }), false);
  // A shown answer that could not be saved is still an answer: nothing to resend.
  assert.equal(shouldRestoreInput({ response: 'draft', conversation: { stored: false } }), false);
  assert.equal(shouldRestoreInput({}), false);
  assert.equal(shouldRestoreInput(null), false);
});

test('saved turns stay quiet while history limitations remain explicit', () => {
  const started = conversationNotice(
    { conversation: { id: 'c', started: true, stored: true } },
    'Nova'
  );
  assert.equal(started, '');

  assert.equal(conversationNotice({ conversation: { id: 'c', stored: true } }, 'Nova'), '');
  assert.match(
    conversationNotice(
      { conversation: { id: 'c', stored: true, history_truncated: true } },
      'Nova'
    ),
    /still stored but were left out/
  );
  assert.match(
    conversationNotice({ response: 'draft', conversation: { id: 'c', stored: false } }, 'Nova'),
    /could not be saved/
  );
  assert.match(
    conversationNotice(
      { model_unavailable: true, conversation: { id: 'c', stored: false } },
      'Nova'
    ),
    /was not saved/
  );
  assert.equal(
    conversationNotice({ conversation: { error: 'conversation_not_found' } }, 'Nova'),
    CONVERSATION_ERRORS.conversation_not_found
  );
  assert.equal(
    conversationNotice({ conversation: { error: 'something_new' } }, 'Nova'),
    CONVERSATION_ERRORS.conversation_unavailable
  );
  // No conversation, no claim about history.
  assert.equal(conversationNotice({ response: 'summary' }, 'Nova'), '');
  // No reply ever claims the turn became memory.
  for (const notice of [started, 'Saved in this conversation.']) {
    assert.doesNotMatch(notice, /remembered/i);
  }
});

test('folder failures preserve the intended conversation and recover the draft', () => {
  for (const error of [
    'folder_context_conflict',
    'folder_context_unavailable',
    'folder_context_save_failed'
  ]) {
    assert.equal(nextConversationId('owned', { error }), 'owned');
    assert.equal(
      shouldRestoreInput({
        response: 'Possibly unsaved reply',
        conversation: { id: 'owned', error }
      }),
      true
    );
    assert.equal(conversationNotice({ conversation: { error } }), CONVERSATION_ERRORS[error]);
  }
  assert.match(CONVERSATION_ERRORS.folder_context_save_failed, /not saved/);
});

test('conversation labels are stable for same-day, older, and malformed entries', () => {
  const now = new Date('2026-10-04T15:00:00');
  const today = conversationLabel(
    { title: 'Birthday greeting 🎂', message_count: 6, updated_at: '2026-10-04T09:30:00' },
    now
  );
  assert.equal(today.title, 'Birthday greeting 🎂');
  assert.match(today.meta, /^6 messages · /);

  const older = conversationLabel(
    { title: '생일 축하', message_count: 1, updated_at: '2026-09-28T09:30:00' },
    now
  );
  assert.equal(older.title, '생일 축하');
  assert.match(older.meta, /^1 message · /);
  assert.notEqual(older.meta, today.meta);

  assert.deepEqual(conversationLabel({ title: '  ', updated_at: 'not a date' }, now), {
    title: 'Conversation',
    meta: '0 messages'
  });
  assert.deepEqual(conversationLabel(null, now), { title: 'Conversation', meta: '0 messages' });
});

test('a rendered row is marked with canonical IDs only when there is a message ID', () => {
  const row = { dataset: {} };
  assert.equal(tagMessageRow(row, 'conv-1', 'msg-9'), true);
  assert.deepEqual(row.dataset, { messageId: 'msg-9', conversationId: 'conv-1' });

  const unsaved = { dataset: {} };
  assert.equal(tagMessageRow(unsaved, 'conv-1', ''), false);
  assert.deepEqual(unsaved.dataset, {});
  assert.equal(tagMessageRow(null, 'conv-1', 'msg-9'), false);
});

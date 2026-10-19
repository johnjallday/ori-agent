import test from 'node:test';
import assert from 'node:assert/strict';
import { conversationNotice, unansweredTurnLine } from './personal-assistant-conversation.js';

test('continuity notices report recap/retrieval and honest omissions', () => {
  const notice = continuity => conversationNotice({ conversation: { stored: true, continuity } });
  assert.match(notice({ recap_used: true, older_omitted: true }), /historical references.*omitted/);
  assert.match(notice({ older_used: true }), /historical references/);
  assert.match(notice({ recap_unavailable: true }), /recap was unavailable/);
  assert.match(notice({ stale_discarded: true }), /outdated recap was discarded/);
  assert.match(notice({ older_omitted: true }), /left out/);
});

test('continuity never hides unsaved/model/scope failures or claims a new memory', () => {
  const continuity = { recap_used: true };
  assert.match(
    conversationNotice({ conversation: { stored: false, continuity } }),
    /could not be saved/
  );
  // An unanswered turn is said by its own line, never by a continuity claim.
  const unanswered = { model_unavailable: true, conversation: { continuity } };
  assert.equal(conversationNotice(unanswered), '');
  assert.match(unansweredTurnLine(unanswered), /could not answer.*Your draft is kept/);
  assert.match(
    conversationNotice({ conversation: { error: 'context_save_failed', continuity } }),
    /reply was not saved/
  );
  assert.doesNotMatch(
    conversationNotice({ conversation: { stored: true, continuity } }),
    /remembered|current evidence|installed/
  );
});

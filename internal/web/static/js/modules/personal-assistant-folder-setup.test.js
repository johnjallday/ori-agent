import test from 'node:test';
import assert from 'node:assert/strict';
import { setupCandidates, currentReview } from './personal-assistant-folder-setup.js';
import {
  conversationFolderOfferView,
  folderReviewResponseCurrent
} from './personal-assistant-folder.js';

const offer = {
  id: 'offer-a',
  conversation_id: 'chat-a',
  status: 'pending',
  verdict: 'project',
  subject: { name: 'Observed project', kind: 'project' },
  create_available: true,
  remember: true
};

test('candidate choices use only disclosed IDs and distinguish the whole root', () => {
  assert.deepEqual(
    setupCandidates({
      projects: [
        { id: 'root', name: 'Work', root: true },
        { id: 'child', name: 'Work', marker: '.git' }
      ]
    }),
    [
      { id: 'root', label: 'Work (whole folder)' },
      { id: 'child', label: 'Work · .git' }
    ]
  );
  assert.deepEqual(setupCandidates(null), []);
});

test('only the current conversation and exact canonical offer get live controls', () => {
  assert.equal(currentReview({ conversationId: 'chat-a', offerId: 'offer-a' }, offer), true);
  assert.equal(currentReview({ conversationId: 'chat-b', offerId: 'offer-a' }, offer), false);
  assert.equal(currentReview({ conversationId: 'chat-a', offerId: 'old' }, offer), false);
  assert.equal(currentReview({}, null), false);
});

test('bound review preserves canonical Create and memory disclosure; Keep chatting is not No', () => {
  const view = conversationFolderOfferView(offer);
  assert.equal(view.actions.find(action => action.id === 'setup').create, true);
  assert.equal(view.actions.find(action => action.id === 'rename').rename, true);
  assert.equal(view.actions.find(action => action.id === 'keep-chatting').closeReview, true);
  assert.equal(
    view.actions.some(action => ['no', 'later'].includes(action.decision)),
    false
  );
  assert.match(view.question, /remember/);
  assert.match(view.question, /links this selected folder/);
  assert.match(view.question, /staffing/);
});

test('late setup responses and polls cannot replace another conversation review', () => {
  assert.equal(folderReviewResponseCurrent(offer, 1, 1, offer), true);
  assert.equal(folderReviewResponseCurrent(offer, 1, 2, offer), false);
  assert.equal(folderReviewResponseCurrent(offer, 1, 1, { ...offer, id: 'other' }), false);
  assert.equal(folderReviewResponseCurrent(offer, 1, 1, null), false);
  assert.equal(folderReviewResponseCurrent({ ...offer, conversation_id: '' }, 1, 2, null), true);
});

test('lost authority keeps local closure, never creation; unavailable setup stays discussion-only', () => {
  const lost = conversationFolderOfferView({ ...offer, needs_pick: true });
  assert.equal(
    lost.actions.some(action => action.create),
    false
  );
  assert.equal(
    lost.actions.some(action => action.closeReview),
    true
  );
  const unavailable = conversationFolderOfferView({ ...offer, create_available: false });
  assert.equal(
    unavailable.actions.some(action => action.modal || action.create),
    false
  );
  assert.match(unavailable.question, /unavailable/);
});

test('a closed conversation review never falls back to legacy execution controls', () => {
  const closed = conversationFolderOfferView({ ...offer, status: 'closed' });
  assert.deepEqual(closed.actions, []);
  assert.match(closed.question, /review is closed/);
});

test('legacy cards retain their existing Adjust, No and Later controls', () => {
  const view = conversationFolderOfferView({ ...offer, conversation_id: '' });
  assert.equal(view.actions.find(action => action.id === 'adjust').modal, true);
  assert.equal(view.actions.find(action => action.id === 'no').decision, 'no');
  assert.equal(view.actions.find(action => action.id === 'later').decision, 'later');
});

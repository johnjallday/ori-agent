import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  setupCandidates,
  currentReview,
  currentSuggestion,
  subjectPlacement,
  existingProjectView,
  reviewStatusView,
  reviewStateKey
} from './personal-assistant-folder-setup.js';
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

test('focus handoff requires the exact live canonical card and never reconstructs a control', () => {
  const state = {
    conversationId: 'chat-a',
    offerId: 'offer-a',
    revision: 'r',
    observation: { id: 'o' }
  };
  const value = {
    conversation_id: 'chat-a',
    offer_id: 'offer-a',
    revision: 'r',
    observation_id: 'o',
    message_id: 'answer'
  };
  assert.equal(currentSuggestion(state, value, offer), true);
  assert.equal(currentSuggestion(state, value), false);
  assert.equal(currentSuggestion(state, value, { ...offer, status: 'resolved' }), false);
  assert.equal(currentSuggestion({ ...state, authority: 'unavailable' }, value, offer), false);
  assert.equal(currentSuggestion(state, { ...value, offer_id: 'foreign' }, offer), false);
  assert.equal(currentSuggestion(state, { ...value, conversation_id: 'other' }, offer), false);
});

test('suggested review binds exact saved reply, revision, observation and disclosed choices', () => {
  const state = {
    conversationId: 'chat-a',
    revision: 'revision-a',
    observation: { id: 'observation-a', projects: [{ id: 'root' }, { id: 'child' }] }
  };
  const suggestion = {
    conversation_id: 'chat-a',
    revision: 'revision-a',
    observation_id: 'observation-a',
    message_id: 'reply-a',
    options: [{ candidate_id: 'root', workspace_type: 'Blank workspace' }]
  };
  assert.equal(currentSuggestion(state, suggestion), true);
  for (const change of [
    { conversationId: 'chat-b' },
    { revision: 'new-revision' },
    { observation: null },
    { preview: true },
    { authority: 'lost' },
    { offerId: 'active-review' }
  ])
    assert.equal(currentSuggestion({ ...state, ...change }, suggestion), false);
  for (const change of [
    { message_id: '' },
    { options: [] },
    { options: [{ candidate_id: 'invented' }] },
    { observation_id: 'old' }
  ])
    assert.equal(currentSuggestion(state, { ...suggestion, ...change }), false);
  assert.equal(currentSuggestion(state, null), false);
  assert.equal(currentSuggestion(null, suggestion), false);
});

test('a named workspace stays with the review through a placement choice, and is never invented', () => {
  const option = { operation: 'create_project_workspace', destination_id: 'ws-home' };
  assert.deepEqual(subjectPlacement('ws-home'), { subject_workspace_id: 'ws-home' });
  assert.deepEqual(subjectPlacement('ws-home', option), {
    ...option,
    subject_workspace_id: 'ws-home'
  });
  // No named workspace: Review follows the page, exactly as before.
  assert.deepEqual(subjectPlacement(''), {});
  assert.equal(subjectPlacement(undefined, option), option);
});

test('the drawer has one distinct sentence for every review status Ori gives the model', () => {
  const { statuses } = JSON.parse(
    readFileSync(
      new URL('../../../../agenthttp/testdata/review_status_vocabulary.json', import.meta.url),
      'utf8'
    )
  );
  const context = status => ({
    version: 1,
    status,
    subject: 'Album-5',
    destination_status: 'existing',
    destination_name: 'Music Home'
  });
  assert.equal(reviewStatusView(context('no_proposal')).visible, false);
  assert.equal(reviewStatusView(null).visible, false);
  const sentences = new Map();
  for (const status of statuses.filter(status => status !== 'no_proposal')) {
    const view = reviewStatusView(context(status));
    assert.equal(view.visible, true, status);
    assert.equal(view.status, status);
    assert.match(view.text, /\S/);
    assert.equal(sentences.has(view.text), false, `${status} repeats ${sentences.get(view.text)}`);
    sentences.set(view.text, status);
  }
  // A status this drawer does not know is "could not be read", never "no review".
  const unknown = reviewStatusView(context('a_status_added_later'));
  assert.equal(unknown.text, reviewStatusView(context('state_unavailable')).text);
  assert.match(unknown.text, /does not mean there is no review/);
});

test('the status sentence names the reviewed destination and why a control is blocked', () => {
  const base = { version: 1, subject: 'Album-5', destination_name: 'Music Home' };
  assert.match(
    reviewStatusView({
      ...base,
      status: 'awaiting_confirmation',
      destination_status: 'existing'
    }).text,
    /Album-5 in Music Home is waiting for your confirmation/
  );
  assert.match(
    reviewStatusView({ ...base, status: 'completed', destination_status: 'new' }).text,
    /Album-5 is set up in a new Music Home\./
  );
  assert.match(
    reviewStatusView({
      ...base,
      status: 'awaiting_confirmation',
      destination_status: 'existing',
      operation: 'link_supporting_folder'
    }).text,
    /as a supporting folder in Music Home/
  );
  // An undisclosed destination is never filled in from a name alone.
  assert.doesNotMatch(
    reviewStatusView({
      ...base,
      status: 'awaiting_confirmation',
      destination_status: 'not_yet_disclosed'
    }).text,
    /Music Home/
  );
  assert.match(
    reviewStatusView({ ...base, status: 'setup_stopped', blocker: 'needs_choice' }).text,
    /stopped because it needs you to choose the project file/
  );
  assert.match(
    reviewStatusView({ ...base, status: 'setup_unavailable', blocker: 'destination_changed' }).text,
    /destination for Album-5 has changed\. Use Review setup to refresh/
  );
  assert.match(
    reviewStatusView({ ...base, status: 'awaiting_confirmation', historical: true }).text,
    /earlier review; it cannot be confirmed here/
  );
  // Only a review pending elsewhere opens another conversation, by its reference.
  const elsewhere = { conversation_id: 'chat-b' };
  assert.equal(
    reviewStatusView({ ...base, status: 'pending_elsewhere' }, elsewhere).openConversation,
    'chat-b'
  );
  assert.equal(
    reviewStatusView({ ...base, status: 'awaiting_confirmation' }, elsewhere).openConversation,
    ''
  );
});

test('only a real change in the card retires the last status sentence', () => {
  const pending = { id: 'offer-a', status: 'pending', review_digest: 'a', plan: { digest: 'x' } };
  // The same review read through another endpoint, with other incidental fields.
  assert.equal(
    reviewStateKey(pending),
    reviewStateKey({ id: 'offer-a', status: 'pending', review_digest: 'b', remember: true })
  );
  for (const changed of [
    { status: 'awaiting_outcome' },
    { setup: { status: 'running' } },
    { setup: { status: 'stopped', stop_reason: 'needs_choice' } },
    { destination_status: 'changed' },
    { needs_pick: true }
  ])
    assert.notEqual(reviewStateKey(pending), reviewStateKey({ ...pending, ...changed }));
});

test('a folder that is already a project links its page and offers no setup control', () => {
  const view = existingProjectView({
    subject: 'Album-5',
    workspace: 'Album-5',
    route: '/workspaces/album-5'
  });
  assert.match(view.message, /Album-5 is already set up as the project “Album-5”/);
  assert.match(view.message, /Nothing new has been prepared/);
  assert.equal(view.route, '/workspaces/album-5');
  assert.equal(view.openLabel, 'Open Album-5');
  // Only an in-app workspace page is linked.
  for (const route of [
    'https://example.test/x',
    '//example.test',
    'javascript:void(0)',
    '/settings',
    ''
  ])
    assert.equal(existingProjectView({ subject: 'A', workspace: 'B', route }).route, '');
  assert.match(existingProjectView(null).message, /This folder is already set up/);
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

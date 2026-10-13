import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  setupCandidates,
  choiceIsCurrent,
  currentReview,
  currentSuggestion,
  subjectPlacement,
  existingProjectView,
  reviewStatusView,
  reviewStateKey,
  suggestionNote,
  proposedStructure,
  suggestionChoices
} from './personal-assistant-folder-setup.js';
import {
  conversationFolderOfferView,
  folderReceiptView,
  folderReviewResponseCurrent,
  offerStructure,
  structureCounts
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

test('local choice binding uses exact conversation, observation and revision, never discussion topics', () => {
  const binding = { conversationId: 'one', observationId: 'picked', revision: 'turn' };
  const state = {
    conversationId: 'one',
    observation: { id: 'picked' },
    revision: 'turn',
    focus: ['child'],
    pending: true
  };
  assert.equal(choiceIsCurrent(binding, state), true);
  for (const changed of [
    { conversationId: 'other' },
    { observation: { id: 'replaced' } },
    { revision: 'later' },
    { authority: 'expired' }
  ])
    assert.equal(choiceIsCurrent(binding, { ...state, ...changed }), false);
  assert.equal(choiceIsCurrent(null, state), false);
  assert.equal(choiceIsCurrent(binding, null), false);
});

test('a proposal about one folder does not ask for a scope to be chosen', () => {
  const one = suggestionNote({ options: [{ candidate_id: 'candidate-0' }] });
  assert.match(one, /Review its effects before confirming\. Nothing is set up here\./);
  assert.doesNotMatch(one, /Choose scope/);
  assert.match(
    suggestionNote({ options: [{ candidate_id: 'candidate-0' }, { candidate_id: 'candidate-1' }] }),
    /Choose scope and review effects/
  );
  // A named workspace and an existing review keep their own lines.
  assert.match(
    suggestionNote({ options: [{}], subject: { name: 'Album' } }),
    /Workspace you named: Album/
  );
  assert.match(suggestionNote({ offer_id: 'offer-a' }), /existing canonical review only/);
});

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

test('bounded or indistinguishable candidate names retain separate opaque choices', () => {
  const names = setupCandidates({
    projects: [
      { id: 'first', name: 'Same' },
      { id: 'second', name: 'Same' },
      { id: 'long', name: '<svg onload=confirm()>' + '界'.repeat(300) }
    ]
  });
  assert.deepEqual(names.slice(0, 2), [
    { id: 'first', label: 'Same · Choice 1' },
    { id: 'second', label: 'Same · Choice 2' }
  ]);
  assert.equal(names[2].id, 'long');
  assert.ok(Array.from(names[2].label).length < 200);
  assert.match(names[2].label, /^<svg/);
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
  // A folder made into a group is not "in a new" anything, even under one name.
  assert.equal(
    reviewStatusView({
      version: 1,
      subject: 'Downloads',
      destination_name: 'Downloads',
      destination_status: 'new',
      status: 'completed',
      operation: 'create_group_with_workspaces'
    }).text,
    'Downloads is set up as the group Downloads.'
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
  // A folder that already has its workspace is not "unavailable here".
  assert.match(
    reviewStatusView({ ...base, status: 'setup_unavailable', blocker: 'project_exists' }).text,
    /Album-5 already has a workspace\. Open it from its card/
  );
  assert.match(
    reviewStatusView({ ...base, status: 'setup_unavailable', blocker: 'setup_path_unavailable' })
      .text,
    /Setup is not available for Album-5 here/
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

const albumsStructure = (group = {}) => ({
  version: 1,
  folder: 'Albums',
  group: { name: 'Albums', type: 'group', destination_state: 'new', ...group },
  members: [
    ...[1, 2, 3, 4, 5].map(number => ({
      candidate_id: `candidate-${number}`,
      name: `Album-${number}`,
      type_label: 'Session',
      project: true,
      included: true,
      availability: 'integration_not_installed'
    })),
    {
      candidate_id: 'candidate-6',
      name: 'Logic Sketch',
      type_label: 'Plain workspace',
      included: true
    }
  ],
  notes: ['The Home is not installed; a plain group is proposed instead.']
});
const structureChoice = structure => ({
  candidate_id: 'candidate-0',
  workspace_type: 'Group with 6 workspaces',
  structure,
  presentation: { version: 1, kind: 'structure', state: 'proposed', effect: 'group_workspaces' }
});

test('a proposed group is the reply proposal, and single folders move behind Adjust', () => {
  const group = structureChoice(albumsStructure());
  const sketch = {
    candidate_id: 'candidate-6',
    presentation: { version: 1, kind: 'workspace', state: 'proposed', effect: 'workspace' }
  };
  const album = {
    candidate_id: 'candidate-1',
    presentation: { version: 1, kind: 'unknown', state: 'unavailable' }
  };
  assert.equal(proposedStructure([album, group, sketch]), group);
  const choices = suggestionChoices([group, album, sketch]);
  assert.deepEqual(choices.shown, [group]);
  // Only a folder that can be set up on its own is offered as one.
  assert.deepEqual(choices.single, [sketch]);

  // Without a group the options that can be set up are shown, and when none
  // can the unavailable ones are shown as such rather than dropped.
  assert.deepEqual(suggestionChoices([album, sketch]).shown, [sketch]);
  assert.deepEqual(suggestionChoices([album]).shown, [album]);
  assert.equal(suggestionChoices([album]).structure, null);
  // The closed host fields decide: a label, an unavailable group or a group
  // with no record is not a proposal.
  for (const not of [
    { workspace_type: 'Group with 6 workspaces' },
    { ...group, presentation: { ...group.presentation, state: 'unavailable' } },
    { ...group, structure: undefined },
    { ...group, structure: { ...group.structure, version: 2 } }
  ])
    assert.equal(proposedStructure([not]), null);
});

test('a review of a proposed group shows the group, its members and Adjust, never a single create', () => {
  const pending = {
    ...offer,
    subject: { name: 'Albums', kind: 'project', is_root: true },
    create_available: true,
    // The collection evidence the same offer carries is not this card's
    // business: the group is the proposal.
    portfolio: { shape: 'audio', projects: 5 },
    capability: { question: 'Set up one Home for these projects?', accept_label: 'Yes' },
    structure: albumsStructure()
  };
  const view = conversationFolderOfferView(pending);
  assert.deepEqual(
    view.headline.map(part => part.text),
    ['Albums', ' has 5 Session folders and 1 other folder.']
  );
  assert.equal(view.question, 'Set up a group with 6 workspaces?');
  assert.equal(view.structure.label, 'Proposed workspace group');
  assert.equal(view.structure.members.length, 6);
  // What the proposal goes without is said once, by the proposal card.
  assert.equal(view.capabilityDetail, '');
  assert.deepEqual(view.structure.notes, [
    'The Home is not installed; a plain group is proposed instead.'
  ]);
  assert.deepEqual(
    view.actions.map(action => action.label),
    ['Adjust…', 'Keep chatting']
  );
  assert.equal(
    view.actions.some(action => action.create || action.modal || action.decision || action.journey),
    false
  );
  // A proposal the server could not plan says the one thing that stops it,
  // and still offers Adjust.
  const taken = conversationFolderOfferView({ ...pending, setup_unavailable_reason: 'name_taken' });
  assert.equal(
    taken.capabilityDetail,
    'The name Albums is not one a group can have. Choose another in Adjust.'
  );
  assert.deepEqual(
    taken.actions.map(action => action.label),
    ['Adjust…', 'Keep chatting']
  );
  assert.match(
    conversationFolderOfferView({ ...pending, setup_unavailable_reason: 'setup_unavailable' })
      .capabilityDetail,
    /^Set up is not available for this proposal right now/
  );
  // A run that stopped at one workspace is carried on from the card.
  const stopped = conversationFolderOfferView({
    ...pending,
    status: 'awaiting_outcome',
    setup: {
      plan_digest: 'd1',
      status: 'stopped',
      stop_reason: 'member_failed',
      lines: [
        { kind: 'group', name: 'Creates group Albums', state: 'done' },
        { kind: 'member', name: 'Album-1', state: 'failed' }
      ]
    }
  });
  assert.match(stopped.question, /^1 of 2 steps finished\. One workspace could not be created/);
  assert.deepEqual(
    stopped.actions.map(action => [action.label, action.oneCard, action.retry]),
    [['Continue', true, true]]
  );
  // A finished setup shows its receipt: the group, then every workspace, each
  // with its own page. A row whose route is not a workspace page is not linked.
  const done = {
    ...pending,
    status: 'resolved',
    outcome: {
      kind: 'group',
      workspace_id: 'group-1',
      route: '/workspaces/albums',
      receipt: [
        { kind: 'workspace', name: 'Albums', detail: 'group', route: '/workspaces/albums' },
        {
          kind: 'workspace',
          name: 'Album-1',
          detail: 'linked to Album-1',
          route: '/workspaces/album-1'
        },
        { kind: 'workspace', name: 'Album-2', route: 'https://example.com/elsewhere' }
      ]
    }
  };
  const receipt = folderReceiptView(done);
  assert.equal(receipt.visible, true);
  assert.equal(receipt.route, '/workspaces/albums');
  assert.equal(receipt.openLabel, 'Open Albums');
  assert.deepEqual(
    receipt.rows.map(row => [row.name, row.route]),
    [
      ['Albums', '/workspaces/albums'],
      ['Album-1', '/workspaces/album-1'],
      ['Album-2', '']
    ]
  );
  const finished = conversationFolderOfferView(done);
  assert.equal(finished.question, "Here's what I set up:");
  assert.equal(finished.structure, null);
  // A group outcome with no workspace to show is not a receipt.
  assert.equal(
    folderReceiptView({ ...done, outcome: { ...done.outcome, receipt: [] } }).visible,
    false
  );
  // With a plan from the server, Set up comes first and the plan says the rest.
  const planned = conversationFolderOfferView({
    ...pending,
    plan: { digest: 'd1', lines: [{ kind: 'workspace', name: 'Creates group Albums' }] }
  });
  assert.deepEqual(
    planned.actions.map(action => action.label),
    ['Set up', 'Adjust…', 'Keep chatting']
  );
  assert.equal(planned.actions[0].oneCard, true);
  // The plan names the group, every workspace and what is unavailable, so the
  // proposal card steps aside: nothing is said twice.
  assert.equal(planned.structure, null);
  assert.equal(planned.structured, true);
  assert.match(planned.question, /Here is everything Set up will do:$/);
  // The shape's Home, new and existing; a single remaining member.
  assert.equal(
    conversationFolderOfferView({
      ...pending,
      structure: albumsStructure({ name: 'Music Home', type: 'home' })
    }).question,
    'Set up Music Home with 6 workspaces?'
  );
  const one = albumsStructure({ name: 'Music Home', type: 'home', destination_state: 'existing' });
  one.members.forEach((member, index) => (member.included = index === 0));
  assert.equal(
    conversationFolderOfferView({ ...pending, structure: one }).question,
    'Add 1 workspace to your Music Home?'
  );
  // Choosing the Home-plus-library listing in Adjust is the earlier card.
  const library = conversationFolderOfferView({
    ...pending,
    structure: albumsStructure({ name: 'Music Home', type: 'library' })
  });
  assert.equal(library.structure, undefined);
  assert.equal(offerStructure({ structure: albumsStructure({ type: 'library' }) }), null);
  // Folders of different kinds are counted without a made-up label.
  const mixed = albumsStructure();
  mixed.members[0].type_label = 'Repository';
  assert.equal(structureCounts(mixed), '5 project folders and 1 other folder');
  mixed.members = mixed.members.slice(0, 1);
  assert.equal(structureCounts(mixed), '1 Repository folder');
});

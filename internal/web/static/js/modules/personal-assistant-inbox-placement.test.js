import test from 'node:test';
import assert from 'node:assert/strict';
import {
  buildConflictView,
  folderOfferView,
  conversationFolderOfferView,
  inboxPlacementConflict,
  inboxPlacementRequest
} from './personal-assistant-folder.js';

const offer = () => ({
  id: 'source',
  conversation_id: 'chat',
  status: 'pending',
  verdict: 'project',
  subject: { shape: 'inbox', is_root: true, name: 'Music' },
  plan: {
    digest: 'digest',
    brief_default: 'Prepare a filing review.',
    lines: [{ kind: 'workspace', name: 'Music' }],
    hire: { mode: 'create', agent_name: 'File Curator', role_label: 'File Curator' }
  }
});
const context = () => ({
  conversationId: 'chat',
  offerId: 'source',
  revision: 'event',
  observation: { id: 'held' }
});

test('active inbox review offers two draft-only entries, not the original one-click confirmation', () => {
  for (const project of [folderOfferView, conversationFolderOfferView]) {
    const view = project(offer());
    const actions = view.actions.filter(action => action.placement);
    assert.deepEqual(
      actions.map(action => action.label),
      ['Review setup', 'Use automatic placement']
    );
    assert.deepEqual(
      actions.map(action => action.placement),
      ['map', 'suggested']
    );
    assert.ok(!view.actions.some(action => action.oneCard));
  }
});

test('accepted inbox and other recipes keep their existing recovery, never become new drafts', () => {
  const source = offer();
  for (const changed of [
    { ...source, setup: { status: 'running' }, status: 'awaiting_outcome' },
    {
      ...source,
      setup: { status: 'stopped', stop_reason: 'scan_failed' },
      status: 'awaiting_outcome'
    },
    { ...source, subject: { shape: 'code', is_root: true } },
    { ...source, conversation_id: '' }
  ])
    assert.ok(!folderOfferView(changed).actions.some(action => action.placement));
});

test('a transferred source opens the same draft even though its original review is closed', () => {
  const transferred = {
    ...offer(),
    status: 'closed',
    plan: null,
    placement_handoff: {
      build_id: 'build',
      revision: 'event',
      plan_digest: 'digest',
      draft_saved: true
    }
  };
  for (const project of [folderOfferView, conversationFolderOfferView]) {
    const view = project(transferred);
    assert.equal(view.resume, true);
    assert.deepEqual(
      view.actions.map(action => action.id),
      ['placement-resume']
    );
    assert.ok(!view.actions.some(action => action.oneCard));
  }
  assert.equal(inboxPlacementRequest(transferred, context()).plan_digest, 'digest');
});

test('a canonically resolved transferred source has no remaining draft or setup invitation', () => {
  const completed = {
    ...offer(),
    status: 'resolved',
    placement_handoff: { build_id: 'bound-build' },
    outcome: {
      kind: 'project',
      workspace_id: 'real-workspace',
      route: '/workspaces/music',
      receipt: [{ kind: 'workspace', name: 'Music' }]
    }
  };
  for (const project of [folderOfferView, conversationFolderOfferView])
    assert.ok(!project(completed).actions.some(action => action.placement || action.oneCard));
});

test('handoff carries exact explicit edits and canonical references, never paths or serialized plans', () => {
  const input = inboxPlacementRequest(offer(), context(), {
    brief: '  음악 — exact\nbrief  ',
    name: 'My inbox',
    monitoring: true,
    path: '/untrusted',
    plan: { create: true }
  });
  assert.deepEqual(input, {
    offer_id: 'source',
    conversation_id: 'chat',
    observation_id: 'held',
    source_revision: 'event',
    plan_digest: 'digest',
    brief: '  음악 — exact\nbrief  ',
    name: 'My inbox',
    monitoring: true
  });
  assert.equal(inboxPlacementRequest(offer(), context()).monitoring, false);
});

test('historical, loading, replaced and foreign conversation callbacks cannot request handoff', () => {
  for (const changed of [
    { ...context(), pending: true },
    { ...context(), conversationId: 'newer' },
    { ...context(), offerId: 'different' },
    { ...context(), observation: null },
    { ...context(), revision: '' }
  ])
    assert.equal(inboxPlacementRequest(offer(), changed), null);
  assert.equal(inboxPlacementRequest({ ...offer(), status: 'closed' }, context()), null);
  assert.equal(
    inboxPlacementRequest({ ...offer(), setup: { status: 'running' } }, context()),
    null
  );
  assert.equal(
    inboxPlacementRequest(
      { ...offer(), placement_handoff: { revision: 'old', plan_digest: 'digest' } },
      context()
    ),
    null
  );
});

const refusal = build =>
  Object.assign(new Error('Another workspace draft is still unfinished.'), {
    code: 'build_conflict',
    details: { build_id: build.id, build }
  });

test('only a build_conflict naming its draft becomes the card’s choice', () => {
  const conflict = inboxPlacementConflict(
    refusal({
      id: 'other',
      name: ' Garage Sync ',
      version: 4,
      from_folder: false,
      cancellable: true
    }),
    offer(),
    'map'
  );
  assert.deepEqual(conflict, {
    offerId: 'source',
    conversationId: 'chat',
    mode: 'map',
    build: { id: 'other', name: 'Garage Sync', version: 4, fromFolder: false, cancellable: true }
  });
  // Any other refusal, or one without the draft, stays an error line.
  assert.equal(inboxPlacementConflict(new Error('stale'), offer(), 'map'), null);
  assert.equal(
    inboxPlacementConflict(
      Object.assign(new Error('x'), { code: 'build_conflict', details: { build_id: 'other' } }),
      offer(),
      'map'
    ),
    null
  );
  assert.equal(
    inboxPlacementConflict(
      Object.assign(new Error('x'), { code: 'source_changed', details: { build: { id: 'b' } } }),
      offer(),
      'map'
    ),
    null
  );
});

const conflictFor = build =>
  inboxPlacementConflict(
    refusal({ id: 'other', version: 1, from_folder: false, cancellable: true, ...build }),
    offer(),
    'suggested'
  );

test('a cancellable draft offers Open first and Cancel second, replacing the refused placement', () => {
  const view = buildConflictView(
    conversationFolderOfferView(offer()),
    conflictFor({ name: 'Garage Sync' }),
    offer()
  );
  assert.deepEqual(
    view.actions.map(action => [action.label, action.style]),
    [
      ['Open that draft', 'primary'],
      ['Cancel it and set up Music', 'outline'],
      ['Keep chatting', 'link']
    ]
  );
  assert.ok(!view.actions.some(action => action.placement));
  assert.match(view.conflictSummary, /“Garage Sync” is still an unfinished workspace draft\./);
});

test('a draft being set up is never offered for cancellation', () => {
  const view = buildConflictView(
    conversationFolderOfferView(offer()),
    conflictFor({ name: 'Garage Sync', cancellable: false }),
    offer()
  );
  assert.deepEqual(
    view.actions.map(action => action.id),
    ['conflict-open', 'keep-chatting']
  );
  assert.match(view.conflictSummary, /is being set up right now.*once it finishes/);
});

test('another folder’s handed-off draft is not reopened from the dialog', () => {
  const view = buildConflictView(
    conversationFolderOfferView(offer()),
    conflictFor({ name: '', from_folder: true }),
    offer()
  );
  assert.deepEqual(
    view.actions.map(action => [action.id, action.style]),
    [
      ['conflict-cancel', 'primary'],
      ['keep-chatting', 'link']
    ]
  );
  assert.match(view.conflictSummary, /^An unnamed workspace draft .*from another setup review/);
});

test('a conflict belongs to its own offer and to a card still offering placement', () => {
  const view = conversationFolderOfferView(offer());
  const conflict = conflictFor({ name: 'Garage Sync' });
  assert.equal(buildConflictView(view, conflict, { ...offer(), id: 'different' }), view);
  assert.equal(buildConflictView(view, null, offer()), view);
  const decided = { ...view, actions: view.actions.filter(action => !action.placement) };
  assert.equal(buildConflictView(decided, conflict, offer()), decided);
});

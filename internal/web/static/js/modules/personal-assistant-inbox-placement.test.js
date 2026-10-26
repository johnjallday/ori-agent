import test from 'node:test';
import assert from 'node:assert/strict';
import {
  folderOfferView,
  conversationFolderOfferView,
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
      ['Place & review', 'Use suggested setup']
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

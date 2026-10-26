import test from 'node:test';
import assert from 'node:assert/strict';
import { folderReceiptView, folderSetupPolling } from './personal-assistant-folder.js';
import {
  setupCompletionView,
  setupTaskVisible,
  setupAttachmentConsumed,
  ownsSetupCompletion,
  completedTaskView,
  completionDisclosureOpen
} from './personal-assistant-setup-completion.js';

const offer = {
  id: 'review',
  conversation_id: 'chat',
  subject: { name: 'Album' },
  status: 'resolved',
  outcome: {
    kind: 'project',
    route: '/workspaces/album',
    receipt: [
      { kind: 'workspace', name: 'Album' },
      { kind: 'folder', name: 'Album' },
      { kind: 'agent', name: 'Ari', assigned: true }
    ]
  }
};
const view = value => setupCompletionView(value, folderReceiptView(value));

test('runner done keeps observational polling until its separate verified receipt arrives', () => {
  assert.equal(folderSetupPolling({ status: 'awaiting_outcome', setup: { status: 'done' } }), true);
  assert.equal(
    folderSetupPolling({ status: 'awaiting_outcome', setup: { status: 'running' } }),
    true
  );
  assert.equal(
    folderSetupPolling({ status: 'awaiting_outcome', setup: { status: 'stopped' } }),
    false
  );
  assert.equal(folderSetupPolling({ ...offer, setup: { status: 'done' } }), false);
});

test('verified receipt compacts independently of each optional task state', () => {
  for (const state of [undefined, 'seeded', 'running', 'waiting', 'finished', 'failed']) {
    const result = view({ ...offer, first_task: state ? { state } : null });
    assert.equal(result.ready, true);
    assert.equal(result.headline, '✓ Album is ready');
    assert.equal(result.facts, 'Folder linked · Ari assigned');
    assert.equal(result.href, '/workspaces/album');
  }
});

test('only observed membership is a compact fact; legacy metadata is not guessed', () => {
  const legacy = {
    ...offer,
    plan: { hire: { agent_name: 'Planned', model: 'Configured' } },
    outcome: {
      ...offer.outcome,
      receipt: [
        { kind: 'workspace', name: 'Legacy' },
        { kind: 'agent', name: 'Unverified', detail: 'Assigned here' }
      ]
    }
  };
  assert.equal(view(legacy).facts, '');
  assert.equal(view(legacy).name, 'Legacy');
});

test('partial, stopped, missing, deleted and unsafe destinations never qualify', () => {
  for (const change of [
    { status: 'pending' },
    { status: 'awaiting_outcome', setup: { status: 'done' } },
    { outcome: null },
    { outcome: { ...offer.outcome, receipt: [] } },
    { setup: { status: 'stopped' } },
    { setup: { status: 'running' } },
    { workspace_gone: 'trashed' },
    { workspace_gone: 'removed' },
    ...['', '//evil.test', '/\\evil.test', '/not-a-workspace'].map(route => ({
      outcome: { ...offer.outcome, route }
    }))
  ])
    assert.equal(view({ ...offer, ...change }).ready, false, JSON.stringify(change));
});

test('Home, group and batch retain their canonical nouns and distinct destinations', () => {
  const home = view({
    ...offer,
    outcome: {
      kind: 'home',
      receipt: [
        {
          kind: 'home',
          name: 'Music Home',
          route: '/workspaces/music/assistant#projectLibraryPanel'
        }
      ]
    }
  });
  assert.equal(home.headline, '✓ Music Home is ready');
  assert.equal(home.href, '/workspaces/music/assistant#projectLibraryPanel');
  const deletedHome = folderReceiptView({
    ...offer,
    workspace_gone: 'removed',
    outcome: { kind: 'home', receipt: [{ kind: 'home', name: 'Music Home', route: home.href }] }
  });
  assert.ok(deletedHome.gone);
  assert.equal(deletedHome.route, '');
  const group = view({
    ...offer,
    outcome: {
      kind: 'group',
      route: '/workspaces/album',
      receipt: [
        { kind: 'workspace', name: 'One' },
        { kind: 'workspace', name: 'Two' }
      ]
    }
  });
  assert.equal(group.name, 'Album');
  assert.equal(group.facts, '2 workspaces');
  assert.equal(group.openLabel, 'Open One');
  const batch = view({
    ...offer,
    outcome: {
      ...offer.outcome,
      first_look: {
        batch: { outcome: 'batch', route: '/workspaces/album?panel=file-janitor&batch_id=a' }
      }
    }
  });
  assert.equal(batch.href, '/workspaces/album');
  assert.equal(batch.openLabel, 'Open workspace');
});

test('composer completion requires the exact accepted canonical review binding', () => {
  const state = {
    conversationId: 'chat',
    offerId: 'review',
    observation: { id: 'observed' },
    accepted: { id: 'observed' }
  };
  assert.equal(ownsSetupCompletion(state, offer, view(offer)), true);
  // Historical snapshots can still carry verified outcomes, not live setup authority.
  assert.equal(ownsSetupCompletion({ ...state, authority: 'lost' }, offer, view(offer)), true);
  for (const change of [
    { conversationId: 'other' },
    { offerId: 'newer' },
    { observation: null },
    { accepted: null },
    { observation: { id: 'replacement' } },
    { preview: true }
  ])
    assert.equal(ownsSetupCompletion({ ...state, ...change }, offer, view(offer)), false);
  assert.equal(ownsSetupCompletion(state, offer, { ready: false }), false);
});

test('a reserved placement build and planned role are not a completed setup receipt', () => {
  const pending = {
    ...offer,
    status: 'closed',
    placement_handoff: { build_id: 'reserved-build' },
    outcome: null,
    plan: { hire: { agent_name: 'File Curator' } }
  };
  const completion = view(pending);
  assert.equal(completion.ready, false);
  assert.equal(
    ownsSetupCompletion(
      {
        conversationId: 'chat',
        offerId: 'review',
        observation: { id: 'root' },
        accepted: { id: 'root' }
      },
      pending,
      completion
    ),
    false
  );
});

test('finished output is saved text with inert canonical report actions only', () => {
  const saved = {
    ...offer,
    first_task: { recipe: 'folder_summary', next_request: 'Saved suggestion' }
  };
  const firstLook = {
    visible: true,
    state: 'finished',
    excerpt: '<script>literal</script>',
    actions: [
      { id: 'report', href: '/workspaces/album/task/one', label: 'Open summary' },
      { id: 'open', href: '/workspaces/album' },
      { id: 'start', start: true }
    ]
  };
  const result = completedTaskView(saved, firstLook, view(offer));
  assert.equal(result.title, 'Summary');
  assert.equal(result.excerpt, '<script>literal</script>');
  assert.equal(result.next, 'Saved suggestion');
  assert.deepEqual(result.actions, [firstLook.actions[0]]);
  for (const state of ['seeded', 'running', 'waiting', 'failed'])
    assert.equal(completedTaskView(saved, { ...firstLook, state }, view(offer)).visible, false);
  assert.equal(completedTaskView(saved, firstLook, { ready: false }).visible, false);
});

test('ready setup does not prompt an unstarted task, but keeps live states and finished output', () => {
  assert.equal(setupTaskVisible({ visible: true, state: 'seeded' }, { ready: true }), false);
  for (const state of ['running', 'waiting', 'failed', 'finished'])
    assert.equal(setupTaskVisible({ visible: true, state }, { ready: true }), true);
  assert.equal(setupTaskVisible({ visible: false }, { ready: true }), false);
});

test('consumption is owned saved setup, including later deletion, never a plan or replacement', () => {
  const state = {
    conversationId: 'chat',
    offerId: 'review',
    observation: { id: 'o' },
    accepted: { id: 'o' }
  };
  assert.equal(setupAttachmentConsumed(state, offer, folderReceiptView(offer)), true);
  const gone = { ...offer, workspace_gone: 'removed' };
  assert.equal(setupAttachmentConsumed(state, gone, folderReceiptView(gone)), true);
  assert.equal(view(gone).ready, false);
  for (const change of [
    { conversationId: 'other' },
    { offerId: 'new' },
    { preview: true },
    { observation: { id: 'replacement' } },
    { accepted: null }
  ])
    assert.equal(
      setupAttachmentConsumed({ ...state, ...change }, offer, folderReceiptView(offer)),
      false
    );
  for (const change of [
    { status: 'pending' },
    { status: 'awaiting_outcome' },
    { setup: { status: 'stopped' } },
    { outcome: null }
  ]) {
    const value = { ...offer, ...change };
    assert.equal(setupAttachmentConsumed(state, value, folderReceiptView(value)), false);
  }
});

test('first collapse keeps focused evidence open and redraw preserves reader intent', () => {
  const initial = { sameState: false, open: true, focused: false, compact: true };
  assert.equal(completionDisclosureOpen(initial), false);
  assert.equal(completionDisclosureOpen({ ...initial, focused: true }), true);
  assert.equal(completionDisclosureOpen({ ...initial, sameState: true }), true);
  assert.equal(completionDisclosureOpen({ ...initial, sameState: true, open: false }), false);
});

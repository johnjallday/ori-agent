import test from 'node:test';
import assert from 'node:assert/strict';
import { folderReceiptView } from './personal-assistant-folder.js';
import {
  setupCompletionView,
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

test('first collapse keeps focused evidence open and redraw preserves reader intent', () => {
  const initial = { sameState: false, open: true, focused: false, compact: true };
  assert.equal(completionDisclosureOpen(initial), false);
  assert.equal(completionDisclosureOpen({ ...initial, focused: true }), true);
  assert.equal(completionDisclosureOpen({ ...initial, sameState: true }), true);
  assert.equal(completionDisclosureOpen({ ...initial, sameState: true, open: false }), false);
});

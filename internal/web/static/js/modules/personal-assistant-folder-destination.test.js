import test from 'node:test';
import assert from 'node:assert/strict';
import {
  folderSetupDestinationLabel,
  folderOfferView,
  conversationFolderOfferView
} from './personal-assistant-folder.js';

const plan = {
  digest: 'canonical-digest',
  lines: [{ name: 'Creates Album-5 as a separate project' }],
  destination: { status: 'existing', name: 'Music Home', kind: 'home', workspace_id: 'home-a' }
};
const offer = {
  id: 'review',
  status: 'pending',
  verdict: 'project',
  subject: { name: 'Album-5' },
  folder: 'Documents',
  capability: { recognized: 'REAPER', workspace: 'REAPER Song' },
  plan
};

test('canonical Home and relationship are visible on the setup card', () => {
  const view = folderOfferView(offer);
  assert.match(view.question, /Destination: Home “Music Home” · separate project\./);
  assert.ok(!view.question.includes('home-a'));
  assert.match(
    folderSetupDestinationLabel({ ...offer, portfolio: {} }),
    /Home “Music Home” · collection in its library/
  );
});

test('a stopped run retains its reviewed destination instead of a fresh plan or page', () => {
  const changed = {
    ...offer,
    current_workspace: { name: 'Other Home' },
    plan: { ...plan, destination: { status: 'existing', name: 'Other Home' } },
    setup: { status: 'stopped', destination: plan.destination }
  };
  assert.equal(
    folderSetupDestinationLabel(changed),
    'Destination: Home “Music Home” · separate project.'
  );
  assert.equal(folderSetupDestinationLabel({ ...changed, setup: { status: 'stopped' } }), '');
});

test('changed and unavailable pinned destinations have a visible recovery and no controls', () => {
  for (const status of ['changed', 'unavailable']) {
    const view = folderOfferView({
      ...offer,
      plan: undefined,
      destination: plan.destination,
      destination_status: status
    });
    assert.match(view.question, /Home “Music Home”/);
    assert.match(view.question, /Use Review workspace setup to refresh the review/);
    assert.match(view.question, /Your draft is unchanged/);
    assert.equal(view.actions.length, 0);
    assert.equal(view.plan, null);
  }
  assert.match(
    folderOfferView({ ...offer, plan: undefined, destination: plan.destination }).question,
    /Home “Music Home”/
  );
});

test('a blocked generic review in a conversation does not describe a confirmation', () => {
  const generic = {
    id: 'review',
    conversation_id: 'chat',
    status: 'pending',
    verdict: 'project',
    subject: { name: 'Album-5' },
    folder: 'Documents',
    create_available: true,
    destination: { status: 'existing', name: 'Portfolio', kind: 'group', workspace_id: 'group-a' }
  };
  const ready = conversationFolderOfferView(generic);
  assert.match(ready.question, /Destination: Group “Portfolio” · separate project\./);
  assert.match(ready.question, /Confirming creates the workspace/);
  assert.deepEqual(
    ready.actions.map(action => action.label),
    ['Set up', 'Adjust name', 'Keep chatting']
  );
  for (const status of ['changed', 'unavailable']) {
    const blocked = conversationFolderOfferView({ ...generic, destination_status: status });
    assert.match(blocked.question, /Use Review workspace setup to refresh the review/);
    assert.doesNotMatch(blocked.question, /Confirming creates/);
    assert.deepEqual(
      blocked.actions.map(action => action.label),
      ['Keep chatting']
    );
  }
});

test('reviewed supporting links disclose their grant without promising project replacement', () => {
  const view = folderOfferView({
    ...offer,
    capability: undefined,
    plan: undefined,
    create_available: true,
    operation: 'link_supporting_folder',
    destination: { status: 'existing', kind: 'project', name: 'Album-1' }
  });
  assert.match(view.question, /Project “Album-1” · supporting folder/);
  assert.match(view.question, /grants read access to this folder only/i);
  assert.match(
    view.question,
    /primary project entry, blueprint, mode, agents and tasks stay unchanged/i
  );
  assert.deepEqual(
    view.actions.map(action => action.label),
    ['Link supporting folder']
  );
});

test('completed creation labels use the verified resulting parent not a new-Home promise', () => {
  assert.equal(
    folderSetupDestinationLabel({
      ...offer,
      status: 'resolved',
      destination: { status: 'new', name: 'Old planned Home', kind: 'home' },
      outcome: { parent: { status: 'existing', name: 'Verified Home', kind: 'home' } }
    }),
    'Destination: Home “Verified Home” · separate project.'
  );
});

test('legacy and incomplete destinations are not filled from navigation', () => {
  assert.equal(folderSetupDestinationLabel({ current_workspace: { name: 'My HQ' } }), '');
  assert.equal(folderSetupDestinationLabel({ plan: { destination: { status: 'existing' } } }), '');
  assert.equal(
    folderSetupDestinationLabel({
      plan: { destination: { status: 'unsupported', name: 'Fake Home' } }
    }),
    ''
  );
  assert.equal(
    folderSetupDestinationLabel({ plan: { destination: { status: 'standalone' } } }),
    'Separate project without a parent Home.'
  );
});

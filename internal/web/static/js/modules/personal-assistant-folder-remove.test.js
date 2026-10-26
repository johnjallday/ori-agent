import test from 'node:test';
import assert from 'node:assert/strict';

import {
  folderOfferView,
  offerActionById,
  removeConfirmView,
  removeFailureSentence,
  setupRunView
} from './personal-assistant-folder.js';

const lines = states => [
  { kind: 'workspace', name: 'Creates a Song workspace named Album-4', state: states[0] },
  { kind: 'folder', name: 'Links the folder', state: states[1] },
  { kind: 'agents', name: 'Hires an assistant', state: states[2] },
  { kind: 'mode', name: 'Uses File-only mode', state: states[3] },
  { kind: 'task', name: 'Queues a first task', state: states[4] }
];

const stoppedSetup = (reason = 'failed', states = ['done', 'done', 'done', 'done', 'failed']) => ({
  status: 'stopped',
  stop_reason: reason,
  lines: lines(states)
});

const stopped = (reason, extra = {}) => ({
  id: 'offer-1',
  status: 'awaiting_outcome',
  verdict: 'project',
  folder: 'Album-4',
  subject: { name: 'Album-4', kind: 'project', is_root: true },
  capability: { recognized: 'REAPER', workspace: 'Song workspace' },
  choice: 'project',
  decision: 'yes',
  setup: stoppedSetup(reason),
  ...extra
});

const VARIANTS = {
  default: {},
  portfolio: { portfolio: true },
  software: { software: true },
  structure: { structure: true },
  inbox: { inbox: true }
};

test('Remove is the last action of every stopped variant and every stop reason', () => {
  for (const [name, options] of Object.entries(VARIANTS)) {
    for (const reason of [
      'failed',
      'interrupted',
      'needs_pick',
      'needs_model',
      'plan_changed',
      'member_failed',
      'consent_stale',
      'needs_choice',
      'something_unknown'
    ]) {
      const view = setupRunView(stoppedSetup(reason), 'Album-4', options);
      const last = view.actions[view.actions.length - 1];
      assert.equal(last.id, 'remove', `${name}/${reason}`);
      assert.equal(last.remove, true, `${name}/${reason}`);
      assert.equal(last.label, 'Remove');
      assert.equal(last.style, 'link');
      assert.ok(view.actions.length > 1, `${name}/${reason} keeps its way forward`);
    }
  }
});

test('Remove is absent while a run is live, on pending and on resolved cards', () => {
  const ids = view => (view.actions || []).map(action => action.id);
  for (const options of Object.values(VARIANTS)) {
    const running = setupRunView(
      { status: 'running', lines: lines(['working']) },
      'Album-4',
      options
    );
    assert.ok(!ids(running).includes('remove'));
    const done = setupRunView({ status: 'done', lines: lines(['done']) }, 'Album-4', options);
    assert.ok(!ids(done).includes('remove'));
  }
  assert.ok(
    !ids(
      folderOfferView(
        stopped('failed', { setup: { status: 'running', lines: lines(['working']) } })
      )
    ).includes('remove')
  );
  const pending = folderOfferView(
    stopped('failed', { status: 'pending', setup: undefined, decision: '' })
  );
  assert.ok(!ids(pending).includes('remove'));
  const resolved = folderOfferView(
    stopped('failed', {
      status: 'resolved',
      setup: { status: 'done', lines: lines(['done']) },
      outcome: {
        kind: 'project',
        route: '/workspaces/album-4',
        receipt: [{ kind: 'workspace', name: 'Album-4' }]
      }
    })
  );
  assert.ok(!ids(resolved).includes('remove'));
});

test('a stopped offer card carries Remove last, after the way forward', () => {
  const view = folderOfferView(stopped('needs_model'));
  const ids = view.actions.map(action => action.id);
  assert.equal(ids[ids.length - 1], 'remove');
  assert.ok(ids.includes('retry') && ids.includes('resume'));
  assert.equal(view.actions.find(action => action.id === 'remove').remove, true);
  assert.equal(view.resume, true);
});

test('a stopped setup whose folder was lost keeps Remove beside Pick it again', () => {
  // A restart drops a held folder: the card asks for it again, but Remove needs
  // no folder, so the user never has to pick one only to drop the setup.
  for (const extra of [
    {},
    { destination_status: 'changed' },
    { destination_status: 'unavailable' }
  ]) {
    const offer = stopped('install_failed', { needs_pick: true, ...extra });
    const view = folderOfferView(offer);
    const label = JSON.stringify(extra);
    assert.deepEqual(
      view.actions.map(action => action.id),
      ['repick', 'remove'],
      label
    );
    assert.equal(view.resume, true, label);
    assert.match(view.question, /^Ori no longer has Album-4 open/, label);
    // The headline asks for what the primary does, not for the plugin.
    assert.equal(view.setup.ask, 'pick the folder again', label);
    assert.equal(offerActionById(offer, '', 'remove')?.remove, true, label);
    assert.deepEqual(
      folderOfferView(offer, { confirm: 'remove' }).actions.map(action => action.id),
      ['remove-confirm', 'remove-keep'],
      label
    );
  }
});

test('a lost folder whose setup has not stopped offers no Remove', () => {
  const ids = offer => folderOfferView(offer).actions.map(action => action.id);
  assert.deepEqual(ids(stopped('failed', { needs_pick: true, setup: undefined })), ['repick']);
  assert.deepEqual(
    ids(
      stopped('failed', {
        needs_pick: true,
        setup: { status: 'running', lines: lines(['working']) }
      })
    ),
    ['repick']
  );
});

test('the confirm names what stays, from the lines that finished', () => {
  const confirm = removeConfirmView(stopped('failed'));
  assert.match(confirm.question, /^Remove this setup from Needs you\? /);
  assert.match(confirm.question, /Kept: the workspace, the folder link, the agent\./);
  assert.match(confirm.question, /Show the folder again to start over\.$/);
  assert.doesNotMatch(confirm.question, /first task|File-only/);
  assert.deepEqual(
    confirm.actions.map(action => [action.id, action.label]),
    [
      ['remove-confirm', 'Remove'],
      ['remove-keep', 'Keep']
    ]
  );
  assert.equal(confirm.removing, true);

  const nothing = removeConfirmView(
    stopped('failed', {
      setup: stoppedSetup('failed', ['failed', 'waiting', 'waiting', 'waiting', 'waiting'])
    })
  );
  assert.match(nothing.question, /Kept: nothing was created yet\./);
  assert.match(
    removeConfirmView(stopped('failed', { setup: undefined })).question,
    /nothing was created yet/
  );
});

test('the confirm keeps the view it replaces and only swaps the question and actions', () => {
  const offer = stopped('needs_pick');
  const plain = folderOfferView(offer);
  const confirm = folderOfferView(offer, { confirm: 'remove' });
  assert.equal(confirm.removing, true);
  assert.equal(confirm.status, plain.status);
  assert.equal(confirm.resume, true);
  assert.deepEqual(
    confirm.actions.map(action => action.id),
    ['remove-confirm', 'remove-keep']
  );
  // A card with nothing to remove ignores the request.
  const running = stopped('failed', { setup: { status: 'running', lines: lines(['working']) } });
  assert.equal(folderOfferView(running, { confirm: 'remove' }).removing, undefined);
});

test('act finds Remove, its confirm and Keep, and nothing on a card that cannot remove', () => {
  const offer = stopped('failed');
  assert.equal(offerActionById(offer, '', 'remove')?.remove, true);
  assert.equal(offerActionById(offer, '', 'remove-confirm'), null);
  assert.equal(offerActionById(offer, 'remove', 'remove-confirm')?.removeConfirm, true);
  assert.equal(offerActionById(offer, 'remove', 'remove-keep')?.removeKeep, true);
  assert.equal(offerActionById(offer, 'remove', 'remove'), null);
  assert.equal(offerActionById(null, '', 'remove'), null);
  const running = stopped('failed', { setup: { status: 'running', lines: lines(['working']) } });
  assert.equal(offerActionById(running, '', 'remove'), null);
  const resolved = stopped('failed', {
    status: 'resolved',
    setup: { status: 'done', lines: lines(['done']) },
    outcome: {
      kind: 'project',
      route: '/workspaces/album-4',
      receipt: [{ kind: 'workspace', name: 'Album-4' }]
    }
  });
  assert.equal(offerActionById(resolved, '', 'remove'), null);
});

test('a refused Remove says why in the server sentence or the generic retry', () => {
  assert.equal(
    removeFailureSentence({ reason: 'setup_running', error: 'x' }),
    'Setup is still running. Wait for it to stop.'
  );
  assert.equal(
    removeFailureSentence({ reason: 'offer_decided' }),
    'This setup already moved on. Reload to see it.'
  );
  assert.equal(removeFailureSentence(null), 'That could not be saved. Try again.');
  assert.equal(removeFailureSentence({ error: 'Sign in again.' }), 'Sign in again.');
});

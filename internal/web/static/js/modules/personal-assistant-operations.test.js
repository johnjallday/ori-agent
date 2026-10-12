import { test } from 'node:test';
import assert from 'node:assert/strict';
import { operationView } from './personal-assistant-operations.js';
import { PHASE } from './ori-operation-client.js';

function snapshot(statuses = {}, overrides = {}) {
  const commands = [
    { id: 'c1', sequence: 1, action: 'open_workspace', status: statuses.open || 'pending' }
  ];
  if (statuses.select !== null) {
    commands.push({
      id: 'c2',
      sequence: 2,
      action: 'select_agent',
      status: statuses.select || 'pending',
      reason: statuses.reason
    });
  }
  return {
    id: 'op-1',
    status: 'requested',
    watching: true,
    pending: commands.filter(c => c.status === 'pending').length,
    context: {
      workspace_slug: 'album-1',
      workspace_name: 'Album 1',
      agent_name: statuses.select === null ? '' : 'Mixer'
    },
    commands,
    ...overrides
  };
}

test('the Watch control appears only where the assistant can operate or an operation exists', () => {
  assert.equal(operationView({}).watchVisible, false);
  assert.equal(operationView({ capable: false }).watchVisible, false);
  assert.equal(operationView({ capable: true }).watchVisible, true);
  assert.equal(operationView({ snapshot: snapshot(), phase: PHASE.WAITING }).watchVisible, true);
  const off = operationView({ capable: true, watch: false });
  const on = operationView({ capable: true, watch: true });
  assert.equal(off.watchChecked, false);
  assert.equal(on.watchChecked, true);
  assert.match(off.watchHint, /ask before/);
  assert.match(on.watchHint, /may move this tab/);
  assert.equal(off.text, '', 'with no operation there is nothing to report');
});

test('status follows observed acknowledgements, never the request', () => {
  const requested = operationView({ snapshot: snapshot(), phase: PHASE.RUNNING });
  assert.equal(requested.text, 'Opening Album 1…');
  assert.equal(
    operationView({ snapshot: snapshot(), phase: PHASE.NAVIGATING }).text,
    'Opening Album 1…'
  );

  const opened = operationView({
    snapshot: snapshot({ open: 'acknowledged' }),
    phase: PHASE.RUNNING
  });
  assert.equal(opened.text, 'Opened Album 1. Selecting Mixer…');

  const done = operationView({
    snapshot: snapshot({ open: 'acknowledged', select: 'acknowledged' }),
    phase: PHASE.SETTLED
  });
  assert.equal(done.text, 'Opened Album 1 and selected Mixer.');
  assert.equal(done.tone, 'done');

  const openOnly = operationView({
    snapshot: snapshot({ open: 'acknowledged', select: null }),
    phase: PHASE.SETTLED
  });
  assert.equal(openOnly.text, 'Opened Album 1.');
  // No text ever claims a step that was only requested.
  for (const view of [requested, opened]) {
    assert.doesNotMatch(view.text, /selected Mixer|Opened Album 1\.$/);
  }
});

test('Watch off offers explicit navigation instead of moving', () => {
  const view = operationView({
    snapshot: snapshot({}, { watching: false, commands: [] }),
    phase: PHASE.WAITING
  });
  assert.equal(
    view.text,
    'Ready to open Album 1 and select Mixer. Nothing moves until you choose.'
  );
  assert.deepEqual(view.actions, [
    { id: 'resume', label: 'Open Album 1', primary: true },
    { id: 'dismiss', label: 'Dismiss' }
  ]);
});

test('a pause says why and keeps both Resume and the manual route', () => {
  for (const [reason, pattern] of [
    ['moved_elsewhere', /another page/],
    ['unsaved_work', /unsaved changes/],
    ['reloaded', /reloaded/],
    ['unreachable', /could not be reached/],
    ['conflict', /changed while/],
    ['refused', /could not confirm/]
  ]) {
    const view = operationView({ snapshot: snapshot(), phase: PHASE.PAUSED, pauseReason: reason });
    assert.match(view.text, pattern);
    assert.equal(view.actions[0].id, 'resume');
    assert.deepEqual(view.link, { href: '/workspaces/album-1', label: 'Open Album 1 yourself' });
  }
  assert.equal(
    operationView({ snapshot: snapshot(), phase: PHASE.PAUSED, pauseReason: 'new' }).text,
    'Paused.'
  );
});

test('a missing target is reported without claiming the step', () => {
  const view = operationView({
    snapshot: snapshot({
      open: 'acknowledged',
      select: 'unavailable',
      reason: "Mixer's control is not shown in this view."
    }),
    phase: PHASE.SETTLED
  });
  assert.equal(
    view.text,
    "Ori could not select Mixer after opening Album 1. Mixer's control is not shown in this view."
  );
  assert.equal(view.tone, 'attention');
  assert.equal(view.link, null, 'already on the workspace: no link back to it');

  const unopened = operationView({
    snapshot: snapshot({ open: 'unavailable', select: 'superseded', reason: '' }),
    phase: PHASE.SETTLED
  });
  assert.equal(unopened.text, 'Ori could not open Album 1.');
  assert.deepEqual(unopened.link, { href: '/workspaces/album-1', label: 'Open Album 1 yourself' });
});

test('ended operations explain themselves; cancelled and replaced ones say nothing', () => {
  const expired = operationView({
    snapshot: snapshot({}, { status: 'expired' }),
    phase: PHASE.ENDED
  });
  assert.match(expired.text, /ran out of time/);
  const invalid = operationView({
    snapshot: snapshot({}, { status: 'invalidated' }),
    phase: PHASE.ENDED
  });
  assert.match(invalid.text, /no longer valid/);
  assert.equal(
    operationView({ snapshot: snapshot({}, { status: 'cancelled' }), phase: PHASE.ENDED }).text,
    ''
  );
  const replaced = operationView({
    snapshot: snapshot({ open: 'superseded', select: 'superseded' }),
    phase: PHASE.SETTLED
  });
  assert.equal(replaced.text, '');
});

test('a request that was not recorded says nothing will open', () => {
  assert.equal(
    operationView({ error: 'turn_not_saved' }).text,
    'That request was not recorded, so nothing will open.'
  );
  assert.match(
    operationView({ error: 'operation_unavailable' }).text,
    /could not record that request/
  );
});

test('a hostile slug never becomes a link', () => {
  const view = operationView({
    snapshot: snapshot(
      {},
      { context: { workspace_slug: 'https://evil.test/x', workspace_name: 'Album 1' } }
    ),
    phase: PHASE.PAUSED,
    pauseReason: 'reloaded'
  });
  assert.equal(view.link, null);
});

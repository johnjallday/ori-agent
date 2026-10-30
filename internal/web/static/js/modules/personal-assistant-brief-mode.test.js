import test from 'node:test';
import assert from 'node:assert/strict';

import {
  BRIEF_LIMIT,
  BRIEF_SEND_LABEL,
  BRIEF_TOO_LONG,
  briefModeView,
  briefSend
} from './personal-assistant-brief-mode.js';

const manager = {
  kind: 'workspace_entry',
  name: 'Music Portfolio Manager',
  workspaceId: 'home-1',
  available: true
};
const personal = { kind: 'personal', name: 'Ada', available: true };

function song(offerID, name, extra = {}) {
  return {
    offerID,
    song: name,
    manager: 'Portfolio Manager',
    asksBrief: true,
    hire: { name: 'REAPER Assistant' },
    ...extra
  };
}

const oneSong = {
  kind: 'offers',
  manager: { name: 'Music Portfolio Manager' },
  pending: [song('offer-5', 'Album-5')]
};

test('brief mode is on with exactly one offered song under the Home’s manager', () => {
  const mode = briefModeView({ recipient: manager, offers: oneSong });
  assert.deepEqual(mode, {
    on: true,
    offerID: 'offer-5',
    song: 'Album-5',
    manager: 'Music Portfolio Manager',
    placeholder: 'Tell Music Portfolio Manager what the REAPER Assistant should do with Album-5',
    sendLabel: BRIEF_SEND_LABEL
  });
  assert.equal(BRIEF_SEND_LABEL, 'Use as brief');
});

test('brief mode is off for two songs, after Not now or the press, and under the Personal Assistant', () => {
  const off = { on: false };
  // Two or more pending songs: each card's own field is the only input (FR11).
  assert.deepEqual(
    briefModeView({
      recipient: manager,
      offers: { ...oneSong, pending: [song('a', 'A'), song('b', 'B')] }
    }),
    off
  );
  // Not now and the press leave no song pending (the offer controller's state).
  assert.deepEqual(briefModeView({ recipient: manager, offers: { ...oneSong, pending: [] } }), off);
  // The Personal Assistant, or an unreachable manager.
  assert.deepEqual(briefModeView({ recipient: personal, offers: oneSong }), off);
  assert.deepEqual(
    briefModeView({ recipient: { ...manager, available: false }, offers: oneSong }),
    off
  );
  // No offer shown, or a plan that asks for no brief.
  assert.deepEqual(briefModeView({ recipient: manager, offers: { kind: 'none' } }), off);
  assert.deepEqual(briefModeView({ recipient: manager }), off);
  assert.deepEqual(
    briefModeView({
      recipient: manager,
      offers: { ...oneSong, pending: [song('offer-5', 'Album-5', { asksBrief: false })] }
    }),
    off
  );
});

test('switching back to the manager restores brief mode', () => {
  assert.equal(briefModeView({ recipient: personal, offers: oneSong }).on, false);
  assert.equal(briefModeView({ recipient: manager, offers: oneSong }).on, true);
});

test('Send in brief mode stores the trimmed text; empty does nothing; too long says so', () => {
  const mode = briefModeView({ recipient: manager, offers: oneSong });
  assert.deepEqual(briefSend(mode, '  clean up the drums and bounce stems \n'), {
    action: 'brief',
    offerID: 'offer-5',
    brief: 'clean up the drums and bounce stems'
  });
  assert.deepEqual(briefSend(mode, '   '), { action: 'none' });
  assert.deepEqual(briefSend(mode, 'x'.repeat(BRIEF_LIMIT + 1)), {
    action: 'tooLong',
    message: BRIEF_TOO_LONG
  });
  assert.equal(briefSend(mode, 'x'.repeat(BRIEF_LIMIT)).action, 'brief');
  // Outside brief mode Send is an ordinary turn (FR20).
  assert.deepEqual(briefSend({ on: false }, 'hello'), { action: 'turn' });
  assert.deepEqual(briefSend(undefined, 'hello'), { action: 'turn' });
});

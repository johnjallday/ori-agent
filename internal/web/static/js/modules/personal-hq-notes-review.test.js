// Tests for personal-hq-notes-review.js — the notes part of Build My HQ.
//   node --test internal/web/static/js/modules/personal-hq-notes-review.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  FIRST_ACTION_KEY,
  NOTES_SCOPE_SENTENCE,
  forgetFirstAction,
  hqFirstActionView,
  readFirstAction,
  rememberFirstAction,
  hqNotesOutcome,
  hqLiveNotesOutcome,
  hqNotesPayload,
  hqNotesReviewView,
  notesSeedSentence
} from './personal-hq-notes-review.js';

const DEFAULT = {
  status: 'not_connected',
  setup_default: { parent_name: 'Ori Workspaces', path: '/Users/me/Ori Workspaces/Second Brain' },
  setup_preview: { follow_ups: 2, people: 1, facts: false }
};

test('a new user without notes reviews notes included at the default place', () => {
  const view = hqNotesReviewView(DEFAULT);
  assert.equal(view.mode, 'create');
  assert.equal(view.include, true, 'included by default');
  assert.equal(view.path, '/Users/me/Ori Workspaces/Second Brain');
  assert.equal(view.scope, NOTES_SCOPE_SENTENCE);
  assert.equal(view.seed, 'It starts with 2 open follow-ups and 1 person you mentioned.');
  assert.deepEqual(hqNotesPayload(view), {
    include: true,
    witness: '/Users/me/Ori Workspaces/Second Brain'
  });
});

test('unchecking defers notes and the payload writes nothing', () => {
  const view = hqNotesReviewView(DEFAULT, { include: false });
  assert.equal(view.include, false);
  assert.match(view.message, /later from Notes/);
  assert.deepEqual(hqNotesPayload(view), { include: false });
});

test('a chosen location is sent only as its selection token and name', () => {
  const view = hqNotesReviewView(DEFAULT, {
    chosen: { selectionToken: 'tok-1', path: '/Users/me/Documents', name: 'Second Brain' }
  });
  assert.equal(view.chosen, true);
  assert.equal(view.path, '/Users/me/Documents/Second Brain');
  const payload = hqNotesPayload(view);
  assert.deepEqual(payload, { include: true, selection_token: 'tok-1', name: 'Second Brain' });
  assert.equal(JSON.stringify(payload).includes('/Users'), false, 'no raw path is sent');
});

test('an existing writable or read-only connection stays and the build makes no notes', () => {
  const ori = hqNotesReviewView({
    status: 'connected',
    source: { name: 'Second Brain', path: '/n/Second Brain', origin: 'created_by_ori' }
  });
  assert.deepEqual([ori.mode, ori.include, ori.writable], ['existing', false, true]);
  assert.match(ori.message, /already set up/);
  const vault = hqNotesReviewView({
    status: 'connected',
    source: { name: 'Vault', path: '/n/Vault', origin: 'connected' }
  });
  assert.deepEqual([vault.mode, vault.include, vault.writable], ['existing', false, false]);
  assert.match(vault.message, /never writes to them/);
  for (const view of [ori, vault]) assert.deepEqual(hqNotesPayload(view), { include: false });
});

test('a missing connection is kept, explained, and never replaced by a new folder', () => {
  const view = hqNotesReviewView({
    status: 'missing',
    source: { name: 'Vault', path: '/gone/Vault', origin: 'connected' },
    setup_default: DEFAULT.setup_default
  });
  assert.equal(view.mode, 'missing');
  assert.match(view.message, /reconnect or choose another folder from Notes/);
  assert.deepEqual(hqNotesPayload(view), { include: false });
});

test('an unreadable status or no default place offers notes later, never a guess', () => {
  for (const status of [null, undefined, { status: 'not_connected' }]) {
    const view = hqNotesReviewView(status);
    assert.equal(view.mode, 'unavailable');
    assert.deepEqual(hqNotesPayload(view), { include: false });
  }
});

test('the seed sentence is honest when nothing is copied in', () => {
  assert.equal(notesSeedSentence({ follow_ups: 0, people: 0, facts: false }), 'It starts empty.');
  assert.equal(
    notesSeedSentence({ follow_ups: 1, people: 2, facts: true }),
    'It starts with 1 open follow-up, 2 people you mentioned and what you have told Ori about yourself.'
  );
  assert.equal(notesSeedSentence(null), '');
});

test('build outcomes separate ready, pending, failed and stopped notes', () => {
  const ready = hqNotesOutcome({ status: 'ready', path: '/n/Second Brain', writable: true });
  assert.deepEqual([ready.canRetry, ready.writable], [false, true]);
  assert.match(ready.line, /Notes ready/);
  const vault = hqNotesOutcome({ status: 'ready', path: '/n/Vault', writable: false });
  assert.match(vault.line, /Notes connected/);
  const failed = hqNotesOutcome({ status: 'failed', message: 'Occupied.' });
  assert.deepEqual([failed.canRetry, failed.line], [true, 'Occupied.']);
  const stopped = hqNotesOutcome({ status: 'stopped', message: 'Another folder.' });
  assert.equal(stopped.canRetry, false, 'a source race is not retried blindly');
  assert.equal(hqNotesOutcome({ status: 'pending' }).canRetry, true);
  assert.equal(hqNotesOutcome(null).status, 'none');
});

test('the first step follows canonical notes states, never treating HQ success as notes', () => {
  const route = '/workspaces/my-hq';
  const writable = hqFirstActionView({ status: 'ready', writable: true }, { hqRoute: route });
  assert.deepEqual(
    [writable.note, writable.openNotes, writable.setupNotes, writable.retry],
    [true, '', false, false]
  );
  assert.match(writable.lead, /Notes are ready/);
  const readOnly = hqFirstActionView({ status: 'ready', writable: false }, { hqRoute: route });
  assert.deepEqual(
    [readOnly.note, readOnly.openNotes],
    [false, '/workspaces/my-hq?station=second-brain']
  );
  const deferred = hqFirstActionView(null, { hqRoute: route });
  assert.deepEqual([deferred.note, deferred.setupNotes], [false, true]);
  assert.doesNotMatch(deferred.lead, /notes have a home/);
  for (const status of ['failed', 'pending']) {
    const pending = hqFirstActionView({ status }, { hqRoute: route });
    assert.deepEqual([pending.note, pending.retry], [false, true], status);
  }
  const stopped = hqFirstActionView({ status: 'stopped' }, { hqRoute: route });
  assert.deepEqual([stopped.note, stopped.retry, stopped.openNotes !== ''], [false, false, true]);
  for (const view of [writable, readOnly, deferred, stopped]) {
    assert.equal(view.folder, false, 'folder setup stays in its existing entry points');
    assert.equal(view.explore, true, 'exploring is always possible');
  }
});

test('live Notes capability, not a historical ready receipt, gates capture', () => {
  const source = { id: 'notes', path: '/notes', origin: 'created_by_ori' };
  assert.equal(hqLiveNotesOutcome({ status: 'connected', source }).writable, true);
  assert.equal(
    hqLiveNotesOutcome({ status: 'connected', source: { ...source, origin: 'connected' } })
      .writable,
    false
  );
  for (const notes of [
    null,
    { status: 'missing', source },
    { status: 'stopped', source },
    { status: 'not_connected', setup: { status: 'ready', writable: true } }
  ]) {
    const result = hqLiveNotesOutcome(notes);
    assert.notEqual(result.writable, true);
    assert.equal(hqFirstActionView(result, { hqRoute: '/workspaces/hq' }).note, false);
  }
  for (const status of ['failed', 'pending'])
    assert.equal(hqLiveNotesOutcome({ setup: { status } }).canRetry, true);
});

test('the first-action record is per HQ, survives a reload, and is forgotten on leaving', () => {
  const data = new Map();
  const storage = {
    getItem: key => (data.has(key) ? data.get(key) : null),
    setItem: (key, value) => data.set(key, value),
    removeItem: key => data.delete(key)
  };
  assert.equal(readFirstAction(storage), null);
  assert.equal(rememberFirstAction(storage, ''), false);
  assert.equal(rememberFirstAction(storage, 'hq-1'), true);
  assert.deepEqual(readFirstAction(storage), { workspaceId: 'hq-1' });
  forgetFirstAction(storage);
  assert.equal(readFirstAction(storage), null);
  data.set(FIRST_ACTION_KEY, '{junk');
  assert.equal(readFirstAction(storage), null);
  const throwing = {
    getItem() {
      throw new Error('denied');
    }
  };
  assert.equal(readFirstAction(throwing), null);
  assert.doesNotThrow(() => forgetFirstAction(throwing));
});

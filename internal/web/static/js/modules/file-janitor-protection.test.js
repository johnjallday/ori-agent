import test from 'node:test';
import assert from 'node:assert/strict';
import {
  createProtectionTrips,
  validateTrip,
  protectionSentence,
  TRIP_KEY,
  TRIP_TTL
} from './file-janitor-protection.js';

function fixture() {
  const values = new Map();
  const prefs = new Map();
  let nonce = 0;
  const storage = map => ({
    getItem: key => map.get(key) ?? null,
    setItem: (key, value) => map.set(key, value),
    removeItem: key => map.delete(key)
  });
  const win = {
    currentWorkspaceId: 'filing',
    location: { href: 'https://ori.test/workspaces/filing', search: '' },
    crypto: { randomUUID: () => 'nonce-' + ++nonce },
    sessionStorage: storage(values),
    localStorage: storage(prefs),
    history: {
      replaceState: (_, __, route) => {
        win.location.href = new URL(route, win.location.href).href;
      }
    }
  };
  const input = {
    workspaceId: 'filing',
    rootGeneration: 'root',
    bindingId: 'a'.repeat(64),
    batchId: 'batch',
    moves: ['move'],
    trash: ['trash'],
    filter: 'pending',
    page: 50
  };
  return { win, values, prefs, input, trips: createProtectionTrips(win) };
}

test('same-page choices remain in memory and contain no authority, names or categories', () => {
  const { trips, input, values } = fixture();
  const trip = trips.begin(input);
  assert.equal(values.size, 0);
  assert.deepEqual(trip.moves, input.moves);
  assert.deepEqual(trip.trash, input.trash);
  assert.equal(trip.focus, 'protection');
  for (const key of [
    'approval_token',
    'acknowledgement',
    'path',
    'name',
    'category',
    'return_url'
  ]) {
    assert.equal(validateTrip({ ...trip, [key]: 'private' }), null);
  }
  assert.equal(trips.consume(trip.nonce), true);
  assert.equal(trips.read(), null);
  assert.equal(trips.consume(trip.nonce), false);
});

test('cross-page state is correlated, same-tab, consume-once and Back has a typed review link', () => {
  const { trips, input, values, win } = fixture();
  const trip = trips.begin(input);
  trips.persist();
  assert.match(win.location.href, /filing_return=nonce-1/);
  assert.match(win.location.href, /panel=file-janitor/);
  const refreshed = createProtectionTrips(win);
  assert.deepEqual(refreshed.read(), trip);
  win.location.href = 'https://ori.test/workspaces/backup?filing_return=wrong';
  assert.equal(createProtectionTrips(win).read(), null);
  win.location.href = 'https://ori.test/workspaces/backup?filing_return=' + trip.nonce;
  const away = createProtectionTrips(win);
  assert.equal(away.read().nonce, trip.nonce);
  assert.equal(away.consume(trip.nonce), true);
  assert.equal(values.has(TRIP_KEY), false);
  assert.doesNotMatch(win.location.href, /filing_return/);
  assert.equal(createProtectionTrips(win).read(), null);
  const independent = fixture();
  independent.win.location.href = 'https://ori.test/?filing_return=' + trip.nonce;
  assert.equal(independent.trips.read(), null);
});

test('newer trips supersede earlier trips and TTL or tampering never restores authority', () => {
  const { trips, input, win, values } = fixture();
  const first = trips.begin(input);
  trips.persist();
  const second = trips.begin({ ...input, moves: ['new'] });
  assert.equal(trips.consume(first.nonce), false);
  trips.persist();
  assert.equal(JSON.parse(values.get(TRIP_KEY)).nonce, second.nonce);
  for (const update of [
    { createdAt: Date.now() - TRIP_TTL - 1 },
    { createdAt: Date.now() + 10000 },
    { version: 9 },
    { kind: 'url' },
    { workspaceId: '//evil.test' },
    { rootGeneration: '/Users/private' },
    { filter: 'arbitrary' },
    { page: -1 },
    { moves: ['same', 'same'] },
    { trash: ['new'] },
    { moves: ['a'.repeat(129)] },
    { focus: 'arbitrary-dom-id' }
  ]) {
    values.set(TRIP_KEY, JSON.stringify({ ...second, ...update }));
    assert.equal(createProtectionTrips(win).read(), null, JSON.stringify(update));
  }
});

test('blocked and oversized storage never navigates or truncates choices', () => {
  const { trips, input, win } = fixture();
  const trip = trips.begin(input);
  win.sessionStorage = {
    getItem() {
      throw new Error('denied');
    },
    setItem() {
      throw new Error('denied');
    }
  };
  const original = win.location.href;
  assert.throws(
    () => trips.openBackup({ kind: 'workspace', workspace_id: 'backup', slug: 'backup' }),
    /cannot save/
  );
  assert.equal(win.location.href, original);
  assert.deepEqual(trips.read(), trip);
  assert.equal(trips.consume(trip.nonce), true, 'same-page return needs no storage');
  assert.throws(
    () =>
      trips.begin({ ...input, moves: Array.from({ length: 600 }, (_, i) => 'x'.repeat(120) + i) }),
    /cannot fit/
  );
  assert.equal(win.location.href, original);
});

test('malformed or oversized serialized state fails closed', () => {
  const { win, values, input, trips } = fixture();
  trips.begin(input);
  trips.persist();
  for (const raw of ['{', 'null', '[]', '"text"', 'x'.repeat(65537)]) {
    values.set(TRIP_KEY, raw);
    assert.equal(createProtectionTrips(win).read(), null);
  }
});

test('typed backup and return routes reject arbitrary destinations and revalidate binding', async () => {
  const { win, trips, input } = fixture();
  trips.begin(input);
  for (const target of [
    { kind: 'url', route: '//evil.test' },
    { kind: 'workspace', workspace_id: 'backup', slug: '../other' },
    { kind: 'workspace', workspace_id: 'backup', slug: 'https://evil.test' }
  ]) {
    assert.throws(() => trips.openBackup(target), /destination is unavailable/);
  }
  trips.openBackup({ kind: 'workspace', workspace_id: 'backup', slug: 'my-backups' });
  assert.equal(
    win.location.href,
    '/workspaces/my-backups?panel=backup-recovery&filing_return=nonce-1'
  );
  win.location.href = 'https://ori.test' + win.location.href;
  win.currentWorkspaceId = 'backup';
  let calls = 0;
  win.fetch = async url => {
    calls++;
    assert.equal(url, '/api/workspaces/filing/file-janitor/protection');
    return {
      ok: true,
      json: async () => ({
        workspace_id: 'filing',
        root_generation: 'different',
        binding_id: input.bindingId,
        workspace_slug: 'filing'
      })
    };
  };
  await assert.rejects(trips.returnToReview(), /folder or its access changed/);
  assert.equal(calls, 1);
  assert.equal(trips.read(), null);
});

test('dismissal survives reload per binding and storage failure honestly falls back to memory', () => {
  const { trips, win, input } = fixture();
  assert.equal(trips.dismiss(input.bindingId), true);
  assert.equal(createProtectionTrips(win).isDismissed(input.bindingId), true);
  assert.equal(trips.isDismissed('b'.repeat(64)), false);
  win.localStorage = {
    setItem() {
      throw new Error('denied');
    }
  };
  assert.equal(trips.dismiss('b'.repeat(64)), false);
  assert.equal(trips.isDismissed('b'.repeat(64)), true);
  assert.equal(createProtectionTrips(win).isDismissed('b'.repeat(64)), false);
});

test('configuration, uncertainty and unavailable roots never claim completed backups', () => {
  assert.match(protectionSentence({ state: 'configured' }), /not observed a completed backup/);
  assert.match(protectionSentence({ state: 'unknown' }), /does not mean there is no backup/);
  assert.match(protectionSentence({ state: 'unavailable' }), /Reconnect/);
});

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  clearTipsLooked,
  readTipsLooked,
  tipLookedStorageKey,
  writeTipLooked
} from './hq-tip-looked.js';

function memoryStorage(seed = {}) {
  const data = new Map(Object.entries(seed));
  return {
    data,
    getItem: key => (data.has(key) ? data.get(key) : null),
    setItem: (key, value) => data.set(key, String(value)),
    removeItem: key => data.delete(key)
  };
}

const refusing = () => ({
  getItem() {
    throw new Error('blocked');
  },
  setItem() {
    throw new Error('blocked');
  },
  removeItem() {
    throw new Error('blocked');
  }
});

test('nothing is looked at until a part is written, and each HQ keeps its own', () => {
  const storage = memoryStorage();
  assert.deepEqual(readTipsLooked(storage, 'hq-1'), {});
  assert.deepEqual(writeTipLooked(storage, 'hq-1', 'daily-brief'), { 'daily-brief': true });
  writeTipLooked(storage, 'hq-1', 'memory');
  assert.deepEqual(readTipsLooked(storage, 'hq-1'), { 'daily-brief': true, memory: true });
  assert.deepEqual(readTipsLooked(storage, 'hq-2'), {}, 'another HQ is untouched');
  assert.ok(storage.data.has(tipLookedStorageKey('hq-1')));
});

test('writing the same part twice stores it once', () => {
  const storage = memoryStorage();
  writeTipLooked(storage, 'hq-1', 'notes');
  const first = storage.data.get(tipLookedStorageKey('hq-1'));
  writeTipLooked(storage, 'hq-1', 'notes');
  assert.equal(storage.data.get(tipLookedStorageKey('hq-1')), first);
  assert.equal(JSON.parse(first).length, 1);
});

test('clearing forgets the HQ, and only that HQ', () => {
  const storage = memoryStorage();
  writeTipLooked(storage, 'hq-1', 'notes');
  writeTipLooked(storage, 'hq-2', 'notes');
  clearTipsLooked(storage, 'hq-1');
  assert.deepEqual(readTipsLooked(storage, 'hq-1'), {});
  assert.deepEqual(readTipsLooked(storage, 'hq-2'), { notes: true });
});

test('a browser that blocks storage reads as nothing looked at and never throws', () => {
  assert.deepEqual(readTipsLooked(refusing(), 'hq-1'), {});
  assert.deepEqual(readTipsLooked(null, 'hq-1'), {});
  assert.deepEqual(readTipsLooked(undefined, 'hq-1'), {});
  // The write is refused, but the caller still gets the in-memory answer.
  assert.deepEqual(writeTipLooked(refusing(), 'hq-1', 'notes'), { notes: true });
  assert.deepEqual(writeTipLooked(null, 'hq-1', 'notes'), { notes: true });
  clearTipsLooked(refusing(), 'hq-1');
  clearTipsLooked(null, 'hq-1');
});

test('damaged or foreign stored values are ignored, not trusted', () => {
  const key = tipLookedStorageKey('hq-1');
  for (const raw of ['', 'not json', '{"a":1}', '3', 'null', '"notes"']) {
    assert.deepEqual(readTipsLooked(memoryStorage({ [key]: raw }), 'hq-1'), {}, raw);
  }
  assert.deepEqual(
    readTipsLooked(memoryStorage({ [key]: '["notes", 7, "Bad Key!", "<x>", "memory"]' }), 'hq-1'),
    { notes: true, memory: true },
    'only well-formed part keys survive'
  );
});

test('a missing HQ id or a bad part key writes nothing', () => {
  const storage = memoryStorage();
  writeTipLooked(storage, '', 'notes');
  writeTipLooked(storage, 'hq-1', 'Not A Key');
  writeTipLooked(storage, 'hq-1', '');
  assert.equal(storage.data.size, 0);
  assert.deepEqual(readTipsLooked(storage, ''), {});
});

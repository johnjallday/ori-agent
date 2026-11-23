import test from 'node:test';
import assert from 'node:assert/strict';
import { firstQuestionPresentation as first } from './commander-question-memory.js';

test('presentation survives a new renderer; only a distinct question is new', () => {
  const map = new Map();
  const storage = () => ({ getItem: k => map.get(k), setItem: (k, v) => map.set(k, v) });
  assert.equal(first('a:p', storage), true);
  assert.equal(first('b:p', storage), true);
  assert.equal(first('a:p', storage), false);
  assert.equal(first('a:s', storage), true);
});

test('ambiguous identity or unavailable, corrupt or full memory leaves the cue static', () => {
  for (const storage of [
    () => null,
    () => {
      throw Error();
    },
    () => ({ getItem: () => '{' }),
    () => ({
      getItem: () => null,
      setItem: () => {
        throw Error();
      }
    }),
    () => ({ getItem: () => JSON.stringify(Array(128).fill('old')) })
  ])
    assert.equal(first('new', storage), false);
  assert.equal(
    first('', () => null),
    false
  );
});

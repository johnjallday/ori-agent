import { test } from 'node:test';
import assert from 'node:assert/strict';

globalThis.window = globalThis;
globalThis.document = { addEventListener() {} };
await import('./blueprint-intake-pending.js');

test('pending re-intake banner counts only new and changed items', () => {
  const value = window.BlueprintIntakePending.counts([
    { classification: 'new' },
    { classification: 'new' },
    { classification: 'changed' },
    { classification: 'unchanged' },
    { classification: 'no_longer_found' }
  ]);
  assert.deepEqual(value, { newItems: 2, changed: 1 });
  assert.equal(window.BlueprintIntakePending.summary(value), '2 new items, 1 changed');
});

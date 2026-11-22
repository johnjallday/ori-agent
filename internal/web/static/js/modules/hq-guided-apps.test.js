import { test } from 'node:test';
import assert from 'node:assert/strict';
import { beginHQAttempt } from './hq-guided-attempt.js';
import { reviewNames, saveReviewedTools, toolsReceipt } from './hq-guided-apps.js';

test('empty is a successful result only with an explicit list', () => {
  assert.deepEqual(reviewNames([]), []);
  assert.throws(() => reviewNames(undefined));
  assert.deepEqual(reviewNames([' Test ', 'test', 'Other']), ['Test', 'Other']);
});
test('verified selected preferences, not inventory, survive return; cancel does not write', async () => {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'sessionStorage');
  const data = new Map();
  Object.defineProperty(globalThis, 'sessionStorage', {
    configurable: true,
    value: { getItem: k => data.get(k), setItem: (k, v) => data.set(k, v) }
  });
  try {
    const h = beginHQAttempt('hq', 'apps');
    const read = async () => ({ success: true, profile: { preferred_tools: ['Fixture'] } });
    const noPost = () => assert.fail('unexpected write');
    await saveReviewedTools(h, [], noPost);
    assert.match((await toolsReceipt(h, read)).text, /No tools saved/);
    await assert.rejects(saveReviewedTools(h, ['Fixture'], async () => ({ preferred_tools: [] })));
    await saveReviewedTools(h, ['Fixture'], async () => ({ preferred_tools: ['Fixture'] }));
    assert.ok([...data.values()].every(v => !v.includes('Fixture')));
    assert.match((await toolsReceipt(h, read)).text, /in Profile: Fixture/);
    assert.match(
      (await toolsReceipt(h, async () => ({ success: true, profile: { preferred_tools: [] } })))
        .text,
      /no longer/
    );
    const next = beginHQAttempt('hq', 'apps');
    await assert.rejects(saveReviewedTools(h, ['Old'], noPost));
    assert.match((await toolsReceipt(next, read)).text, /No tool save was confirmed/);
  } finally {
    if (original) Object.defineProperty(globalThis, 'sessionStorage', original);
    else delete globalThis.sessionStorage;
  }
});

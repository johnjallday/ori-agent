import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./reset-generation.js', import.meta.url), 'utf8');

test('a verified Start Fresh generation reloads stale tabs once', () => {
  let listener;
  let reloads = 0;
  const context = {
    window: {
      localStorage: { getItem: () => 'old-operation' },
      addEventListener: (name, fn) => {
        if (name === 'storage') listener = fn;
      },
      location: { reload: () => reloads++ }
    }
  };
  vm.runInNewContext(source, context, { filename: 'reset-generation.js' });
  listener({ key: 'unrelated', newValue: 'new-operation' });
  listener({ key: 'ori.reset.generation', newValue: 'old-operation' });
  assert.equal(reloads, 0);
  listener({ key: 'ori.reset.generation', newValue: 'new-operation' });
  listener({ key: 'ori.reset.generation', newValue: 'new-operation' });
  assert.equal(reloads, 1);
});

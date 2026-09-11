import test from 'node:test';
import assert from 'node:assert/strict';

// The shared creator registers a browser bridge at import time; no DOM is read
// until the modal opens.
globalThis.window ||= {};
globalThis.document ||= { getElementById: () => null };

const { setupWorkspacePlacement } = await import('./setup-workspace-creator.js');

test('Required setup placement stays grouped and cannot bypass preparation', () => {
  const missing = setupWorkspacePlacement({
    group_policy: 'required',
    available_compositions: ['grouped'],
    exists: false,
    acknowledged: false
  });
  assert.equal(missing.groupComposition, 'grouped');
  assert.equal(missing.canOpen, false);

  const ready = setupWorkspacePlacement({
    group_policy: 'required',
    available_compositions: ['grouped'],
    exists: true,
    acknowledged: true
  });
  assert.equal(ready.canOpen, true);
  assert.deepEqual(ready.availableCompositions, ['grouped']);
});

test('Recommended setup placement requires an explicit grouped or standalone choice', () => {
  const undecided = setupWorkspacePlacement({
    group_policy: 'recommended',
    available_compositions: ['grouped', 'standalone'],
    exists: false,
    acknowledged: false
  });
  assert.equal(undecided.canOpen, true);
  assert.equal(undecided.groupComposition, '');

  const standalone = setupWorkspacePlacement(
    {
      group_policy: 'recommended',
      available_compositions: ['grouped', 'standalone'],
      exists: false,
      acknowledged: false
    },
    'standalone'
  );
  assert.equal(standalone.groupComposition, 'standalone');
});

test('None setup placement is explicitly standalone without a Home', () => {
  const placement = setupWorkspacePlacement({
    group_policy: 'none',
    available_compositions: ['standalone'],
    exists: false,
    acknowledged: false
  });
  assert.equal(placement.canOpen, true);
  assert.equal(placement.groupComposition, 'standalone');
  assert.deepEqual(placement.availableCompositions, ['standalone']);
});

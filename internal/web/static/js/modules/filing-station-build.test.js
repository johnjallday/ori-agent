import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

const sandbox = { window: {}, setTimeout: cb => queueMicrotask(cb) };
vm.runInNewContext(
  readFileSync(new URL('./filing-station-build.js', import.meta.url), 'utf8'),
  sandbox
);
const { label, observer } = sandbox.window.FilingStationBuild;
const watch = async (parts, { started = true, reduced = false } = {}) => {
  const seen = [];
  const o = observer({
    read: async () => parts.shift(),
    paint: b => seen.push(b),
    reduced: () => reduced
  });
  await o.watch('ws', started, () => parts.length > 0);
  return seen.map(b => b.part);
};
test('a fast accepted build reveals only the parts proven by its receipts', async () => {
  assert.deepEqual(
    await watch([
      { operation_id: 'op', part: 'foundation', running: true },
      { operation_id: 'op', part: 'walls', repair: true }
    ]),
    ['plot', 'foundation', 'walls']
  );
  assert.deepEqual(await watch([{ operation_id: 'op', part: 'open' }]), [
    'plot',
    'foundation',
    'walls',
    'roof',
    'open'
  ]);
});
test('a completed station never replays on reload; reduced motion skips the reveal', async () => {
  assert.deepEqual(await watch([{ operation_id: 'op', part: 'open' }], { started: false }), [
    'open'
  ]);
  assert.deepEqual(await watch([{ operation_id: 'op', part: 'open' }], { reduced: true }), [
    'plot',
    'open'
  ]);
});
test('manager never guesses a count and distinguishes an empty result', () => {
  assert.match(label({ looking: true }), /No count yet/);
  assert.match(label({ looking: true, counted: true, checked: 8, total: 13 }), /8 of 13/);
  assert.equal(label({ part: 'open', eligible: 0 }), 'Nothing to file yet');
});

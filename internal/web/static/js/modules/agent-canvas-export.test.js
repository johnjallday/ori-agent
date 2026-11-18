import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// Exercise the public browser adapter without constructing the rendering engine.
const source = readFileSync(new URL('./agent-canvas.js', import.meta.url), 'utf8');
const marker = source.lastIndexOf('// Export canvas function - accessible globally');
assert.ok(marker >= 0);
function adapter(window) {
  let warnings = 0;
  vm.runInNewContext(source.slice(marker), {
    window,
    console: {
      warn() {
        warnings++;
      }
    }
  });
  window.exportCanvas();
  return warnings;
}
test('Export uses the actual workspace Canvas instance', () => {
  let calls = 0;
  const canvas = {
    exportCanvas() {
      assert.equal(this, canvas);
      calls++;
    }
  };
  assert.equal(adapter({ agentCanvas: canvas }), 0);
  assert.equal(calls, 1);
});
test('older embedded currentCanvas hosts retain their export contract', () => {
  let calls = 0;
  assert.equal(
    adapter({
      currentCanvas: {
        exportCanvas() {
          calls++;
        }
      }
    }),
    0
  );
  assert.equal(calls, 1);
});
test('missing instances warn without inventing a successful export', () => {
  assert.equal(adapter({}), 1);
});

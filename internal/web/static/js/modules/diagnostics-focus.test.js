import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
const source = readFileSync(new URL('./workspace-detail.js', import.meta.url), 'utf8');
const start = source.indexOf('  restoreDiagnosticsFocus(invoker) {');
assert.ok(start >= 0);
const method = source.slice(start, source.indexOf('\n  }', start) + 4);
function fixture() {
  let focused = 0;
  const body = {},
    button = {
      isConnected: true,
      focus() {
        focused++;
      }
    };
  const context = { document: { body, activeElement: body, querySelector: () => null } };
  const page = vm.runInNewContext(`({ diagnosticsFocusOwner: null, ${method} })`, context);
  page.diagnosticsFocusOwner = button;
  return { page, button, context, focused: () => focused };
}
test('completed Diagnostics action restores its actual invoker after disabling settles', () => {
  const f = fixture();
  f.page.restoreDiagnosticsFocus(f.button);
  assert.equal(f.focused(), 1);
});
test('an earlier action cannot take focus from the latest Diagnostics action', () => {
  const f = fixture();
  f.page.diagnosticsFocusOwner = {};
  f.page.restoreDiagnosticsFocus(f.button);
  assert.equal(f.focused(), 0);
});
test('another editor or modal keeps its focus', () => {
  const f = fixture();
  f.context.document.activeElement = {};
  f.page.restoreDiagnosticsFocus(f.button);
  assert.equal(f.focused(), 0);
  f.context.document.activeElement = f.context.document.body;
  f.context.document.querySelector = () => ({});
  f.page.restoreDiagnosticsFocus(f.button);
  assert.equal(f.focused(), 0);
});
test('detached and programmatic invokers do not acquire focus', () => {
  const f = fixture();
  f.button.isConnected = false;
  f.page.restoreDiagnosticsFocus(f.button);
  f.page.restoreDiagnosticsFocus(null);
  assert.equal(f.focused(), 0);
});

import test from 'node:test';
import assert from 'node:assert/strict';
import { bindCanvasAdd } from './workspace-canvas-controls.js';

function fixture() {
  const events = {},
    local = {};
  let focused = 0;
  const trigger = {
    focus() {
      focused++;
    }
  };
  const choice = { dataset: { canvasAddDialog: '#ownedModal' } };
  const disclosure = {
    open: true,
    isConnected: true,
    dataset: {},
    querySelector: () => trigger,
    contains: node => node === choice || node === trigger,
    addEventListener(type, fn) {
      (local[type] ||= []).push(fn);
    }
  };
  const doc = {
    getElementById: () => disclosure,
    querySelector: () => null,
    addEventListener(type, fn) {
      (events[type] ||= []).push(fn);
    }
  };
  bindCanvasAdd(doc);
  return {
    doc,
    disclosure,
    choice,
    trigger,
    events,
    local,
    focused: () => focused,
    choose() {
      local.click[0]({ target: { closest: () => choice } });
    }
  };
}
test('Escape dismisses an open disclosure and restores its persistent trigger', () => {
  const f = fixture();
  let prevented = 0;
  f.local.keydown[0]({
    key: 'Escape',
    preventDefault() {
      prevented++;
    },
    stopPropagation() {}
  });
  assert.equal(f.disclosure.open, false);
  assert.equal(f.focused(), 1);
  assert.equal(prevented, 1);
  f.local.keydown[0]({ key: 'Escape' });
  assert.equal(f.focused(), 1);
});
test('outside pointer dismisses without taking focus; inside pointer keeps it open', () => {
  const f = fixture();
  f.events.pointerdown[0]({ target: f.trigger });
  assert.equal(f.disclosure.open, true);
  f.events.pointerdown[0]({ target: {} });
  assert.equal(f.disclosure.open, false);
  assert.equal(f.focused(), 0);
});
test('choice keeps original action dispatch and only its dialog restores focus', () => {
  const f = fixture();
  f.choose();
  assert.equal(f.disclosure.open, false);
  assert.equal(f.focused(), 0);
  f.events['hidden.bs.modal'][0]({ target: { matches: () => false } });
  assert.equal(f.focused(), 0);
  f.events['hidden.bs.modal'][0]({ target: { matches: selector => selector === '#ownedModal' } });
  assert.equal(f.focused(), 1);
  f.events.close[0]({ target: { matches: () => true } });
  assert.equal(f.focused(), 1);
});
test('dialog completion cannot steal focus from another active overlay', () => {
  const f = fixture();
  f.choose();
  f.doc.querySelector = () => ({});
  f.events.close[0]({ target: { matches: () => true } });
  assert.equal(f.focused(), 0);
  f.doc.querySelector = () => null;
  f.events.close[0]({ target: { matches: () => true } });
  assert.equal(f.focused(), 0);
});
test('binding is idempotent and absent hosts are ignored', () => {
  const f = fixture();
  bindCanvasAdd(f.doc);
  assert.equal(f.local.click.length, 1);
  assert.doesNotThrow(() => bindCanvasAdd({ getElementById: () => null }));
});

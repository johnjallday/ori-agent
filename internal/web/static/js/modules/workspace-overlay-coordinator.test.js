import { test } from 'node:test';
import assert from 'node:assert/strict';
import { OverlayCoordinator, LAYER } from './workspace-overlay-coordinator.js';

function makeCoordinator() {
  const calls = [];
  const c = new OverlayCoordinator({
    setInert: el => calls.push(['setInert', el]),
    releaseInert: el => calls.push(['releaseInert', el]),
    trapFocus: el => calls.push(['trapFocus', el]),
    restoreFocus: el => calls.push(['restoreFocus', el])
  });
  return { c, calls };
}

test('layer scale is ordered so modals sit above map windows (retires 10010 vs 1050)', () => {
  assert.ok(LAYER.MODAL > LAYER.MAP, 'a bounded dialog is above the Map and its windows');
  assert.ok(LAYER.MENU > LAYER.MODAL, 'menus sit above a dialog');
  assert.ok(LAYER.TOAST > LAYER.MENU, 'toasts are always on top');
  assert.ok(LAYER.TRAY > LAYER.DRAWER && LAYER.DRAWER > LAYER.MAP);
});

test('layerFor maps kinds to the documented tokens', () => {
  const { c } = makeCoordinator();
  assert.equal(c.layerFor('map'), LAYER.MAP);
  assert.equal(c.layerFor('modal'), LAYER.MODAL);
  assert.equal(c.layerFor('menu'), LAYER.MENU);
  assert.equal(c.layerFor('drawer'), LAYER.DRAWER);
  assert.equal(c.layerFor('tray'), LAYER.TRAY);
});

test('only one modal is active at a time; a child modal suspends the owner (FR119)', () => {
  const { c } = makeCoordinator();
  const closes = [];
  c.open({ id: 'parent', kind: 'modal', onClose: info => closes.push(['parent', info.reason]) });
  c.open({ id: 'child', kind: 'modal', onClose: info => closes.push(['child', info.reason]) });

  assert.equal(c.activeModal().id, 'child', 'child is the active modal');
  assert.equal(c.modalCount(), 2, 'parent is retained but suspended, not stacked as active');
  assert.deepEqual(closes[0], ['parent', 'suspended'], 'owner was suspended via the coordinator');
});

test('closing the child modal resumes the suspended parent (FR119)', () => {
  const { c } = makeCoordinator();
  c.open({ id: 'parent', kind: 'modal' });
  c.open({ id: 'child', kind: 'modal' });
  c.close('child');
  assert.equal(c.activeModal().id, 'parent', 'parent resumes as the active modal');
  assert.equal(c.modalCount(), 1);
});

test('a user action never opens a modal under an existing one (FR117) — no two active modals', () => {
  const { c } = makeCoordinator();
  c.open({ id: 'a', kind: 'modal' });
  c.open({ id: 'b', kind: 'modal' });
  c.open({ id: 'cc', kind: 'modal' });
  const active = c.openIds().filter(id => id === c.activeModal().id);
  assert.equal(active.length, 1);
  assert.equal(c.activeModal().id, 'cc');
});

test('close returns focus to the overlay trigger (FR121)', () => {
  const { c, calls } = makeCoordinator();
  const trigger = { name: 'open-btn' };
  c.open({ id: 'm', kind: 'modal', trigger });
  c.close('m');
  assert.ok(calls.some(([op, arg]) => op === 'restoreFocus' && arg === trigger));
});

test('Escape closes menu/popover before a dialog (FR123)', () => {
  const { c } = makeCoordinator();
  c.open({ id: 'dialog', kind: 'modal' });
  c.open({ id: 'menu', kind: 'menu' });
  assert.equal(c.escapeTopmost(), 'menu', 'menu dismissed first');
  assert.equal(c.escapeTopmost(), 'dialog', 'then the dialog');
  assert.equal(c.escapeTopmost(), null, 'nothing left dismissable; Map untouched');
});

test('Escape collapses tray/drawer only after menus and dialogs', () => {
  const { c } = makeCoordinator();
  c.open({ id: 'drawer', kind: 'drawer' });
  c.open({ id: 'tray', kind: 'tray' });
  c.open({ id: 'dialog', kind: 'modal' });
  assert.equal(c.escapeTopmost(), 'dialog', 'dialog (priority 1) before tray/drawer (priority 2)');
  const next = c.escapeTopmost();
  assert.ok(next === 'tray' || next === 'drawer', 'then a tray/drawer collapses');
});

test('focus is trapped on open and inert applied for modals', () => {
  const { c, calls } = makeCoordinator();
  const container = { id: 'modal-el' };
  c.open({ id: 'm', kind: 'modal', container });
  assert.ok(calls.some(([op, arg]) => op === 'setInert' && arg === container));
  assert.ok(calls.some(([op, arg]) => op === 'trapFocus' && arg === container));
});

test('re-opening an already-open id is a no-op, not a duplicate', () => {
  const { c } = makeCoordinator();
  c.open({ id: 'm', kind: 'modal' });
  c.open({ id: 'm', kind: 'modal' });
  assert.equal(c.modalCount(), 1);
  assert.deepEqual(c.openIds(), ['m']);
});

test('closing an unknown id is a safe no-op', () => {
  const { c } = makeCoordinator();
  assert.equal(c.close('nope'), false);
});

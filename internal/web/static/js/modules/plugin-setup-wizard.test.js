import test from 'node:test';
import assert from 'node:assert/strict';
import { createPluginSetupWizard, pluginSetupPlan } from './plugin-setup-wizard.js';

class FakeNode {
  constructor(tag) {
    this.tag = tag;
    this.children = [];
    this.attributes = {};
    this.listeners = {};
    this.className = '';
    this.isConnected = true;
    this.text = '';
  }
  get textContent() {
    return this.text + this.children.map(child => child.textContent).join('');
  }
  set textContent(value) {
    this.text = String(value);
    this.children = [];
  }
  append(...nodes) {
    this.children.push(...nodes);
  }
  replaceChildren(...nodes) {
    this.children = nodes;
  }
  setAttribute(name, value) {
    this.attributes[name] = value;
    if (name === 'open') this.open = true;
  }
  removeAttribute(name) {
    delete this.attributes[name];
    if (name === 'open') this.open = false;
  }
  addEventListener(type, handler) {
    this.listeners[type] = handler;
  }
  dispatch(type, event = {}) {
    this.listeners[type]?.({ preventDefault() {}, stopPropagation() {}, ...event });
  }
  focus() {
    harness.focused = this;
  }
  click() {
    this.dispatch('click');
  }
  showModal() {
    this.open = true;
    this.modal = true;
  }
  close() {
    this.open = false;
  }
  walk() {
    return this.children.flatMap(child => [child, ...child.walk()]);
  }
  button(label) {
    return this.walk().find(node => node.tag === 'button' && node.textContent === label);
  }
}

let harness = { focused: null };

const rows = [
  { kind: 'integration', operation: 'install', name: 'REAPER', version: 'Target version 1.0' },
  { kind: 'provider', operation: 'install', name: 'Music Project Management' },
  { kind: 'other', operation: 'ready', name: 'Already ready' }
];

function setup({ dialogSupport = true } = {}) {
  const body = new FakeNode('body');
  const doc = {
    body,
    createElement: tag => {
      const node = new FakeNode(tag);
      if (!dialogSupport) node.showModal = undefined;
      return node;
    },
    querySelector: () => null
  };
  let cancels = 0,
    closes = 0;
  const controller = { cancel: () => cancels++ };
  const wizard = createPluginSetupWizard({ doc, controller, onClose: () => closes++ });
  harness = { focused: null };
  return {
    wizard,
    dialog: () => body.children[0],
    cancels: () => cancels,
    closes: () => closes
  };
}

test('the plan keeps only pending plugin changes and leads with the pressed plugin', () => {
  assert.deepEqual(
    pluginSetupPlan(rows).map(step => step.kind),
    ['integration', 'provider']
  );
  assert.deepEqual(
    pluginSetupPlan(rows, 'provider').map(step => step.kind),
    ['provider', 'integration']
  );
  assert.deepEqual(pluginSetupPlan(undefined), []);
});

test('opening shows the step count in a modal, and nothing opens with no pending plugin', () => {
  const f = setup();
  f.wizard.open([rows[2]]);
  assert.equal(f.wizard.view().open, false);
  assert.equal(f.dialog(), undefined);
  f.wizard.open(rows, { kind: 'integration' });
  assert.equal(f.dialog().modal, true);
  assert.equal(f.wizard.view().total, 2);
  assert.equal(f.wizard.view().title, 'Plugin 1 of 2 · REAPER');
  assert.equal(f.dialog().attributes['aria-labelledby'], 'pluginSetupWizardTitle');
});

test('a browser without showModal still gets a usable panel', () => {
  const f = setup({ dialogSupport: false });
  f.wizard.open(rows);
  assert.equal(f.dialog().open, true);
  assert.equal(f.dialog().attributes.open, 'open');
});

test('Cancel closes once, tells the controller and returns focus to the launching button', () => {
  const f = setup();
  const trigger = new FakeNode('button');
  f.wizard.open(rows, { trigger });
  f.dialog().button('Cancel').click();
  assert.equal(f.wizard.view().open, false);
  assert.equal(f.dialog().open, false);
  assert.equal(f.cancels(), 1);
  assert.equal(f.closes(), 1);
  assert.equal(harness.focused, trigger);
  f.wizard.close();
  assert.equal(f.closes(), 1);
});

test('Escape mirrors Cancel through the native cancel event and the key itself', () => {
  for (const press of [d => d.dispatch('cancel'), d => d.dispatch('keydown', { key: 'Escape' })]) {
    const f = setup();
    f.wizard.open(rows);
    press(f.dialog());
    assert.equal(f.wizard.view().open, false);
    assert.equal(f.closes(), 1);
  }
});

test('other keys leave the wizard open', () => {
  const f = setup();
  f.wizard.open(rows);
  f.dialog().dispatch('keydown', { key: 'Tab' });
  assert.equal(f.wizard.view().open, true);
});

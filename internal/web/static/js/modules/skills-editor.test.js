import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('../skills.js', import.meta.url), 'utf8');

function element() {
  const classes = new Set();
  return {
    value: '',
    isConnected: true,
    focusCount: 0,
    resetCount: 0,
    reset() {
      this.resetCount += 1;
    },
    events: new Map(),
    classList: {
      add: (...names) => names.forEach(name => classes.add(name)),
      remove: (...names) => names.forEach(name => classes.delete(name)),
      contains: name => classes.has(name)
    },
    addEventListener(name, handler) {
      assert.equal(this.events.has(name), false, 'bind each modal event only once');
      this.events.set(name, handler);
    },
    getClientRects: () => [{}],
    focus() {
      this.focusCount += 1;
    }
  };
}

function load(nodes = {}) {
  const shown = [];
  const context = vm.createContext({
    console,
    window: {},
    document: {
      readyState: 'loading',
      addEventListener() {},
      getElementById: id => nodes[id] || null,
      querySelector: () => null,
      activeElement: null
    },
    bootstrap: { Modal: { getOrCreateInstance: () => ({ show: trigger => shown.push(trigger) }) } },
    fetch: () => {
      throw new Error('unexpected network request');
    }
  });
  vm.runInContext(source, context, { filename: 'skills.js' });
  return { context, shown };
}

test('invalid editor submission exposes validation without sending a write', async () => {
  const form = element();
  let reports = 0;
  form.reportValidity = () => {
    reports += 1;
    return false;
  };
  const { context } = load({ skillsEditorForm: form });
  await context.saveSkillEditor();
  assert.equal(reports, 1);
  assert.equal(form.classList.contains('was-validated'), true);
});

test('opening a fresh editor clears prior validation and carries its launcher', () => {
  const form = element();
  form.classList.add('was-validated');
  const name = element();
  name.value = 'previous-draft';
  const launcher = element();
  const { context, shown } = load({
    skillsEditorForm: form,
    skillNameInput: name,
    skillsEditorModal: element()
  });
  context.openSkillEditor(null, launcher);
  assert.equal(form.classList.contains('was-validated'), false);
  assert.equal(form.resetCount, 1);
  assert.equal(name.value, '');
  assert.equal(name.disabled, false);
  assert.equal(shown[0], launcher);
});

test('programmatic dialog closes restore the visible launcher, without duplicate listeners', () => {
  const modal = element();
  const launcher = element();
  const { context } = load({ skillsEditorModal: modal });
  context.getEditorModal();
  context.getEditorModal();
  modal.events.get('show.bs.modal')({ relatedTarget: launcher });
  modal.events.get('hidden.bs.modal')();
  assert.equal(launcher.focusCount, 1);
});

test('dialog handoffs do not steal focus; removed launchers fall back to the page action', () => {
  const modal = element();
  const launcher = element();
  const fallback = element();
  const { context } = load({ skillsEditorModal: modal, skillsCreateBtn: fallback });
  context.getEditorModal();
  modal.events.get('show.bs.modal')({ relatedTarget: launcher });
  context.document.querySelector = () => element();
  modal.events.get('hidden.bs.modal')();
  assert.equal(launcher.focusCount, 0);
  context.document.querySelector = () => null;
  launcher.isConnected = false;
  modal.events.get('hidden.bs.modal')();
  assert.equal(fallback.focusCount, 1);
});

test('card and detail badges share readable states and escape source labels', () => {
  const { context } = load();
  const invalid = context.renderSkillBadges('<img src=x>', {
    hasErrors: true,
    hasScripts: true,
    isTrusted: false
  });
  assert.match(invalid, /&lt;img src=x&gt;/);
  assert.match(invalid, /bg-danger">invalid/);
  assert.match(invalid, /bg-danger">untrusted/);
  assert.doesNotMatch(invalid, /style=|<img/);
  assert.match(
    context.renderSkillBadges('agent', {
      hasErrors: false,
      hasScripts: true,
      isTrusted: true
    }),
    /bg-success">trusted/
  );
  assert.doesNotMatch(
    context.renderSkillBadges('agent', {
      hasErrors: false,
      hasScripts: false,
      isTrusted: false
    }),
    /trusted|invalid/
  );
});

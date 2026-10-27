import { test } from 'node:test';
import assert from 'node:assert/strict';

import { openChooseModelDialog, saveDefaultModel } from './choose-model-dialog.js';

class FakeElement {
  constructor(tag = 'div') {
    this.tagName = String(tag).toUpperCase();
    this.children = [];
    this.attributes = {};
    this.className = '';
    this.hidden = false;
    this.disabled = false;
    this.id = '';
    this.textContent = '';
    this.listeners = {};
    this.parent = null;
    this.focused = false;
  }
  appendChild(child) {
    child.parent = this;
    this.children.push(child);
    return child;
  }
  remove() {
    if (this.parent) this.parent.children = this.parent.children.filter(c => c !== this);
  }
  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }
  addEventListener(type, handler) {
    (this.listeners[type] = this.listeners[type] || []).push(handler);
  }
  querySelector() {
    return null;
  }
  focus() {
    this.focused = true;
  }
  click() {
    return Promise.all((this.listeners.click || []).map(handler => handler({})));
  }
}

function find(node, id) {
  if (node.id === id) return node;
  for (const child of node.children) {
    const found = find(child, id);
    if (found) return found;
  }
  return null;
}

function fakeDoc() {
  const keys = new Set();
  const doc = {
    body: new FakeElement('body'),
    createElement: tag => new FakeElement(tag),
    addEventListener: (type, fn) => type === 'keydown' && keys.add(fn),
    removeEventListener: (type, fn) => type === 'keydown' && keys.delete(fn),
    press: key => [...keys].forEach(fn => fn({ key })),
    listening: () => keys.size
  };
  return doc;
}

// A stand-in for the shared picker: the test sets what it selects.
function fakePicker() {
  const picker = { current: null, onChange: null };
  picker.create = ({ onChange }) => {
    picker.onChange = onChange;
    return { ready: Promise.resolve(), selection: () => picker.current };
  };
  picker.select = selection => {
    picker.current = selection;
    picker.onChange(selection);
  };
  return picker;
}

const CODEX = { provider: 'codex', model: 'gpt-x', reasoning_effort: 'high' };

test('saveDefaultModel sends the Settings request, with reasoning for Codex only', async () => {
  const bodies = [];
  const fetchImpl = async (url, options) => {
    assert.equal(url, '/api/settings/system-model');
    assert.equal(options.method, 'POST');
    bodies.push(JSON.parse(options.body));
    return { ok: true };
  };
  assert.equal((await saveDefaultModel(CODEX, { fetchImpl })).ok, true);
  await saveDefaultModel(
    { provider: 'openai', model: 'gpt-s', reasoning_effort: 'high' },
    { fetchImpl }
  );
  await saveDefaultModel(
    { provider: 'codex', model: 'gpt-x', reasoning_effort: '' },
    { fetchImpl }
  );
  assert.deepEqual(bodies, [
    { provider: 'codex', model: 'gpt-x', reasoning_effort: 'high' },
    { provider: 'openai', model: 'gpt-s', reasoning_effort: '' },
    { provider: 'codex', model: 'gpt-x', reasoning_effort: 'medium' }
  ]);
});

test('saveDefaultModel refuses an empty choice and reports a failed save', async () => {
  const never = async () => assert.fail('no request for an empty choice');
  assert.equal((await saveDefaultModel(null, { fetchImpl: never })).ok, false);
  const rejected = await saveDefaultModel(CODEX, { fetchImpl: async () => ({ ok: false }) });
  assert.equal(rejected.ok, false);
  assert.match(rejected.error, /could not save that model/);
  const offline = await saveDefaultModel(CODEX, {
    fetchImpl: async () => {
      throw new Error('offline');
    }
  });
  assert.equal(offline.ok, false);
});

test('Save is enabled only with a selection, and saving emits, refreshes, and closes', async () => {
  const doc = fakeDoc();
  const picker = fakePicker();
  const emitted = [];
  const saved = [];
  const requests = [];
  const closed = openChooseModelDialog({
    documentImpl: doc,
    createPicker: picker.create,
    fetchImpl: async (url, options) => {
      requests.push([url, JSON.parse(options.body)]);
      return { ok: true };
    },
    emit: model => emitted.push(model),
    onSaved: model => saved.push(model)
  });
  const save = find(doc.body, 'chooseModelSave');
  assert.equal(doc.body.children.length, 1, 'the dialog is on the page');
  assert.equal(save.disabled, true, 'nothing chosen yet');

  picker.select(CODEX);
  assert.equal(save.disabled, false);
  await save.click();

  const result = await closed;
  assert.equal(result.saved, true);
  assert.deepEqual(requests, [
    ['/api/settings/system-model', { provider: 'codex', model: 'gpt-x', reasoning_effort: 'high' }]
  ]);
  assert.deepEqual(emitted, [{ provider: 'codex', model: 'gpt-x', reasoning_effort: 'high' }]);
  assert.deepEqual(saved, emitted);
  assert.equal(doc.body.children.length, 0, 'the dialog closed');
  assert.equal(doc.listening(), 0, 'the key listener is gone');
});

test('a failed save keeps the dialog open with the message and emits nothing', async () => {
  const doc = fakeDoc();
  const picker = fakePicker();
  const emitted = [];
  const closed = openChooseModelDialog({
    documentImpl: doc,
    createPicker: picker.create,
    fetchImpl: async () => ({ ok: false }),
    emit: model => emitted.push(model)
  });
  picker.select(CODEX);
  await find(doc.body, 'chooseModelSave').click();
  assert.equal(doc.body.children.length, 1, 'still open');
  assert.equal(emitted.length, 0);
  const error = find(doc.body, 'chooseModelSave').parent.parent.children.find(
    c => c.attributes.role === 'alert'
  );
  assert.equal(error.hidden, false);
  assert.match(error.textContent, /could not save that model/);
  assert.equal(find(doc.body, 'chooseModelSave').disabled, false, 'it can be tried again');

  await find(doc.body, 'chooseModelCancel').click();
  assert.deepEqual(await closed, { saved: false, model: null });
});

test('Cancel and Escape close without saving', async () => {
  for (const how of ['cancel', 'escape']) {
    const doc = fakeDoc();
    const requests = [];
    const closed = openChooseModelDialog({
      documentImpl: doc,
      createPicker: fakePicker().create,
      fetchImpl: async url => requests.push(url)
    });
    if (how === 'cancel') await find(doc.body, 'chooseModelCancel').click();
    else doc.press('Escape');
    assert.deepEqual(await closed, { saved: false, model: null });
    assert.deepEqual(requests, []);
    assert.equal(doc.body.children.length, 0);
  }
});

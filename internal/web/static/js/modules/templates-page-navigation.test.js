import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./templates-page.js', import.meta.url), 'utf8');

function harness(narrow = true) {
  const nodes = new Map();
  const calls = [];
  const document = {
    readyState: 'loading',
    activeElement: null,
    addEventListener() {},
    getElementById: id => nodes.get(id),
    createElement: tag => node(tag)
  };
  function node(id) {
    const events = new Map();
    const element = {
      id,
      children: [],
      attrs: {},
      isConnected: true,
      appendChild(child) {
        this.children.push(child);
      },
      setAttribute(name, value) {
        this.attrs[name] = value;
      },
      addEventListener(name, callback, options) {
        const listeners = events.get(name) || [];
        listeners.push({ callback, options });
        events.set(name, listeners);
      },
      dispatch(name) {
        for (const listener of [...(events.get(name) || [])]) {
          if (listener.options?.once)
            events.set(
              name,
              events.get(name).filter(item => item !== listener)
            );
          listener.callback();
        }
      },
      focus() {
        document.activeElement = this;
        calls.push(`focus:${id}`);
      },
      scrollIntoView() {
        calls.push(`scroll:${id}`);
      },
      querySelector: () => nodes.get('selected-row') || null
    };
    nodes.set(id, element);
    return element;
  }
  for (const id of [
    'tplDetailName',
    'tplList',
    'tplSearch',
    'selected-row',
    'modal',
    'input',
    'invoker'
  ])
    node(id);
  const sandbox = {
    document,
    window: { matchMedia: () => ({ matches: narrow }) },
    bootstrap: {
      Modal: { getOrCreateInstance: modal => ({ show: () => modal.dispatch('shown.bs.modal') }) }
    }
  };
  vm.createContext(sandbox);
  vm.runInContext(
    source +
      '\nglobalThis.subject = { tplState, tplBuildRow, tplFocusSelection, tplBackToLibrary, tplShowModal };',
    sandbox
  );
  return { subject: sandbox.subject, nodes, calls, document };
}

test('template selection focuses the detail heading on phones and the rebuilt row on desktop', () => {
  for (const narrow of [true, false]) {
    const { subject, calls } = harness(narrow);
    subject.tplFocusSelection();
    assert.deepEqual(
      calls,
      narrow ? ['scroll:tplDetailName', 'focus:tplDetailName'] : ['focus:selected-row']
    );
  }
});

test('back to library focuses its selected row, or search if filters removed that row', () => {
  const { subject, nodes, calls } = harness();
  subject.tplBackToLibrary();
  assert.deepEqual(calls.splice(0), ['scroll:selected-row', 'focus:selected-row']);
  nodes.delete('selected-row');
  subject.tplBackToLibrary();
  assert.deepEqual(calls, ['scroll:tplSearch', 'focus:tplSearch']);
});

test('template rows retain native buttons inside list items and expose their selected state', () => {
  const { subject } = harness();
  subject.tplState.selectedId = 'example';
  const item = subject.tplBuildRow({ id: 'example', name: '<literal template label>' });
  assert.equal(item.attrs.role, 'listitem');
  const button = item.children[0];
  assert.equal(button.id, 'button');
  assert.equal(button.type, 'button');
  assert.equal(button.attrs.role, undefined);
  assert.equal(button.attrs['aria-pressed'], 'true');
  assert.equal(button.children[0].textContent, '<literal template label>');
});

test('programmatic template dialogs focus after showing and restore only the connected invoker once', () => {
  const { subject, nodes, document, calls } = harness();
  const modal = nodes.get('modal');
  const invoker = nodes.get('invoker');
  // Pointer activation in WebKit leaves a different element active.
  document.activeElement = nodes.get('selected-row');
  subject.tplShowModal(modal, nodes.get('input'), invoker);
  modal.dispatch('hidden.bs.modal');
  modal.dispatch('hidden.bs.modal');
  assert.deepEqual(calls.splice(0), ['focus:input', 'focus:invoker']);
  subject.tplShowModal(modal, nodes.get('input'));
  invoker.isConnected = false;
  modal.dispatch('hidden.bs.modal');
  assert.deepEqual(calls, ['focus:input']);
});

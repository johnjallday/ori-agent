import test from 'node:test';
import assert from 'node:assert/strict';
import { AgentCanvasWorkflowSelector } from './agent-canvas-workflow-selector.js';
import { EVENT_TYPES } from './agent-canvas-state.js';

function fixture() {
  let focused = 0;
  const attributes = {};
  const trigger = {
    focus() {
      focused++;
    },
    setAttribute(k, v) {
      attributes[k] = v;
    }
  };
  const menu = { hidden: true, classList: { toggle() {}, remove() {} } };
  const removed = [],
    unsubscribed = [];
  const container = {
    innerHTML: '',
    contains: node => node === trigger,
    querySelector: () => trigger,
    querySelectorAll: () => [],
    removeEventListener: (...args) => removed.push(args)
  };
  const doc = {
    activeElement: null,
    getElementById: id => (id === 'workflow-selector-menu' ? menu : null),
    removeEventListener: (...args) => removed.push(args)
  };
  const state = {
    selectedWorkflowId: null,
    getAvailableWorkflows: () => [{ id: 'parent', name: 'Real outline', taskCount: 1 }],
    getSelectedWorkflow: () => null,
    getTotalTaskCount: () => 2,
    off: (...args) => unsubscribed.push(args)
  };
  const selector = new AgentCanvasWorkflowSelector(state, {}, 'host');
  selector.container = container;
  // DOM text serialization does not escape quotes: attribute sites must do so.
  selector.escapeHtml = value =>
    String(value).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
  return {
    selector,
    state,
    container,
    doc,
    trigger,
    menu,
    attributes,
    removed,
    unsubscribed,
    focused: () => focused
  };
}
function withDocument(f, action) {
  const original = globalThis.document;
  globalThis.document = f.doc;
  try {
    action();
  } finally {
    if (original === undefined) delete globalThis.document;
    else globalThis.document = original;
  }
}
test('workflow choices are native shared buttons with explicit state and no inline dispatch', () => {
  const f = fixture();
  withDocument(f, () => f.selector.render());
  assert.match(f.container.innerHTML, /role="group" aria-label="Canvas workflow filter" hidden/);
  assert.match(f.container.innerHTML, /data-workflow-choice="parent" aria-pressed="false"/);
  assert.match(f.container.innerHTML, /aria-expanded="false"/);
  assert.doesNotMatch(f.container.innerHTML, /onclick=/);
});
test('unavailable workflow data cannot leave an open disabled filter', () => {
  const f = fixture();
  f.selector.dropdownOpen = true;
  f.state.getAvailableWorkflows = () => [];
  withDocument(f, () => f.selector.render());
  assert.equal(f.selector.dropdownOpen, false);
  assert.match(f.container.innerHTML, /aria-expanded="false"/);
  assert.match(f.container.innerHTML, /disabled title="No workflows available"/);
  assert.match(f.container.innerHTML, /aria-label="Canvas workflow filter" hidden/);
});

test('untrusted IDs stay in inert quoted data attributes', () => {
  const f = fixture();
  f.state.getAvailableWorkflows = () => [
    { id: '" autofocus onfocus="bad', name: '<script>', taskCount: 1 }
  ];
  withDocument(f, () => f.selector.render());
  assert.match(f.container.innerHTML, /data-workflow-choice="&quot; autofocus onfocus=&quot;bad"/);
  assert.match(f.container.innerHTML, /&lt;script&gt;/);
});
test('Escape synchronizes hidden/expanded state and returns focus', () => {
  const f = fixture();
  withDocument(f, () => {
    f.selector.toggleDropdown();
    assert.equal(f.menu.hidden, false);
    assert.equal(f.attributes['aria-expanded'], 'true');
    let prevented = 0;
    f.selector.handleKeydown({
      key: 'Escape',
      preventDefault() {
        prevented++;
      },
      stopPropagation() {}
    });
    assert.equal(prevented, 1);
    assert.equal(f.menu.hidden, true);
    assert.equal(f.focused(), 1);
    assert.equal(f.attributes['aria-expanded'], 'false');
  });
});
test('selection preserves the state/URL contract and focuses its replacement trigger', () => {
  const f = fixture();
  const values = [];
  f.state.setSelectedWorkflow = id => values.push(['state', id]);
  f.selector.updateUrl = id => values.push(['url', id]);
  withDocument(f, () => f.selector.selectWorkflow('parent'));
  assert.deepEqual(values, [
    ['state', 'parent'],
    ['url', 'parent']
  ]);
  assert.equal(f.focused(), 1);
});
test('background redraw never steals focus from another host', () => {
  const f = fixture();
  withDocument(f, () => f.selector.render());
  assert.equal(f.focused(), 0);
  f.doc.activeElement = f.trigger;
  withDocument(f, () => f.selector.render());
  assert.equal(f.focused(), 1);
});
test('destroy removes the exact event callbacks, including task-data subscriptions', () => {
  const f = fixture();
  withDocument(f, () => f.selector.destroy());
  assert.deepEqual(
    f.unsubscribed.map(([event]) => event),
    [
      EVENT_TYPES.WORKFLOW_SELECTED,
      EVENT_TYPES.TASK_CREATED,
      EVENT_TYPES.TASK_UPDATED,
      EVENT_TYPES.DATA_LOADED
    ]
  );
  assert.equal(f.removed.length, 3);
});

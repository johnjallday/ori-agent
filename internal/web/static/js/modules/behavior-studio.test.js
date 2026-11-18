import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./behavior-studio.js', import.meta.url), 'utf8');
function harness() {
  const nodes = new Map();
  const events = new Map();
  function node(id) {
    if (!nodes.has(id))
      nodes.set(id, {
        id,
        dataset: {},
        innerHTML: '',
        value: '',
        open: true,
        isConnected: true,
        listeners: new Map(),
        addEventListener(type, listener) {
          this.listeners.set(type, listener);
        },
        dispatch(type, target) {
          this.listeners.get(type)?.({ target });
        },
        querySelector: () => null,
        querySelectorAll: () => [],
        contains: () => false,
        setAttribute() {},
        removeAttribute() {},
        classList: { add() {}, remove() {} },
        focus() {
          document.activeElement = this;
        },
        scrollIntoView() {}
      });
    return nodes.get(id);
  }
  const document = {
    activeElement: null,
    getElementById: node,
    addEventListener: (type, listener) => events.set(type, listener)
  };
  const sandbox = {
    document,
    console,
    URL,
    URLSearchParams,
    escapeHtml: text =>
      String(text).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;'),
    escapeAttr: text => String(text).replaceAll('"', '&quot;'),
    window: {
      location: { href: 'http://localhost/workflows', search: '' },
      history: { replaceState() {} },
      matchMedia: () => ({ matches: false }),
      addEventListener() {}
    }
  };
  vm.createContext(sandbox);
  vm.runInContext(
    source.replace(
      /\n\}\)\(\);\s*$/,
      '\n globalThis.subject = { BehaviorStudio, buildBlankTemplate };\n})();'
    ),
    sandbox
  );
  const studio = new sandbox.subject.BehaviorStudio(node('behaviorStudioPage'));
  studio.state.draft = sandbox.subject.buildBlankTemplate();
  studio.state.draft.name = 'Fictional local notes';
  studio.state.draft.steps[0].description = 'A fictional step; never executed.';
  return { studio, node, sandbox, document, events };
}

test('edited parameter defaults and step JSON survive payload normalization', () => {
  const { studio } = harness();
  studio.state.draft.parameters[0]._defaultText = 'edited default';
  studio.state.draft.steps[0]._contextText = '{"fictional":"edited"}';
  const payload = studio.buildTemplatePayload();
  assert.equal(payload.parameters[0].default_value, 'edited default');
  assert.equal(payload.steps[0].context.fictional, 'edited');
});

test('invalid edited default JSON is rejected before save', () => {
  const { studio } = harness();
  studio.state.draft.parameters[0].type = 'object';
  studio.state.draft.parameters[0]._defaultText = '{invalid';
  assert.throws(() => studio.buildTemplatePayload(), /JSON|Expected|property/i);
});

test('invalid edited step context is rejected before save', () => {
  const { studio } = harness();
  studio.state.draft.steps[0]._contextText = '{invalid';
  assert.throws(() => studio.buildTemplatePayload(), /Step 1 context/);
});

test('editor redraw keeps trailing spaces in a live step name', () => {
  const { studio, node } = harness();
  studio.state.draft.steps[0].name = 'Next word ';
  studio.renderEditor();
  assert.match(node('behaviorStudioEditor').innerHTML, /value="Next word "/);
  assert.match(node('behaviorStudioEditor').innerHTML, /class="form-control behavior-input"/);
  assert.match(node('behaviorStudioEditor').innerHTML, /data-editor-heading tabindex="-1"/);
});

test('library selection is a native button with a pressed state and named secondary actions', () => {
  const { studio, node } = harness();
  studio.state.draft.id = 'fictional';
  studio.state.templates = [studio.state.draft];
  studio.state.filteredTemplates = studio.state.templates;
  studio.state.selectedTemplateId = 'fictional';
  studio.renderSidebar();
  const markup = node('behaviorStudioList').innerHTML;
  assert.match(markup, /<button type="button" class="behavior-card__select"/);
  assert.match(markup, /aria-pressed="true"/);
  assert.match(markup, /aria-label="Duplicate Fictional local notes"/);
  assert.doesNotMatch(markup, /onclick=/);
});

test('blur-time text change does not replace Save before its click; selects update once', () => {
  const { studio, node } = harness();
  let edits = 0;
  let launches = 0;
  studio.handleEditorInput = () => edits++;
  studio.handleLaunchInput = () => launches++;
  studio.bindEvents();
  for (const [id, count] of [
    ['behaviorStudioEditor', () => edits],
    ['behaviorLaunchModalBody', () => launches]
  ]) {
    node(id).dispatch('input', { tagName: 'INPUT' });
    node(id).dispatch('change', { tagName: 'INPUT' });
    assert.equal(count(), 1);
    node(id).dispatch('input', { tagName: 'SELECT' });
    node(id).dispatch('change', { tagName: 'SELECT' });
    assert.equal(count(), 2);
  }
});

test('focus restoration uses field identity and restores the text caret', () => {
  const { studio, node, document } = harness();
  const container = node('container');
  const input = node('old');
  input.id = '';
  input.tagName = 'INPUT';
  input.dataset = { stepIndex: '1', stepField: 'name' };
  input.selectionStart = 4;
  input.selectionEnd = 8;
  container.contains = candidate => candidate === input;
  document.activeElement = input;
  const saved = studio.rememberFocus(container);
  const replacement = node('replacement');
  replacement.id = '';
  replacement.tagName = 'INPUT';
  replacement.dataset = { ...input.dataset };
  let range;
  replacement.setSelectionRange = (...args) => {
    range = args;
  };
  container.querySelectorAll = () => [replacement];
  studio.restoreFocus(container, saved);
  assert.equal(document.activeElement, replacement);
  assert.deepEqual(range, [4, 8]);
});

test('load failure has an explicit retry and never claims an empty successful count', () => {
  const { studio, node } = harness();
  studio.showLoadError(new Error('Fictional unavailable'));
  assert.match(node('behaviorStudioEditor').innerHTML, /Could not load orchestration skills/);
  assert.match(node('behaviorStudioEditor').innerHTML, /data-action="retry-library"/);
  assert.equal(node('behaviorStudioCount').textContent, '—');
  assert.equal(node('behaviorStudioPreview').hidden, true);
});

test('name validation is associated with visible feedback and focused without a write', () => {
  const { studio, node, document } = harness();
  const input = node('name');
  const attributes = new Map();
  input.setAttribute = (name, value) => attributes.set(name, value);
  node('behaviorStudioEditor').querySelector = selector =>
    selector === '#behaviorEditorFeedback' ? node('feedback') : input;
  studio.showEditorError(new Error('Skill name is required.'));
  assert.equal(node('feedback').hidden, false);
  assert.equal(node('feedback').textContent, 'Skill name is required.');
  assert.equal(attributes.get('aria-invalid'), 'true');
  assert.equal(attributes.get('aria-describedby'), 'behaviorEditorFeedback');
  assert.equal(document.activeElement, input);
});

test('launch cancellation restores its actual invoker after Bootstrap hides', () => {
  const { studio, node, document } = harness();
  studio.bindEvents();
  studio.launchTrigger = node('actual-launch-invoker');
  node('behaviorLaunchModal').dispatch('hidden.bs.modal', node('behaviorLaunchModal'));
  assert.equal(document.activeElement, studio.launchTrigger);
});

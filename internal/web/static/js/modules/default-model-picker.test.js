import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  chooseModel,
  chooseProvider,
  createDefaultModelPicker,
  formatModelOptionText,
  normalizeModelOptions
} from './default-model-picker.js';

class FakeElement {
  constructor(tag = 'div') {
    this.tagName = String(tag).toUpperCase();
    this.children = [];
    this.attributes = {};
    this.className = '';
    this.hidden = false;
    this.disabled = false;
    this.value = '';
    this.id = '';
    this.textContent = '';
    this.listeners = {};
  }
  appendChild(child) {
    this.children.push(child);
    return child;
  }
  replaceChildren(...children) {
    this.children = children;
  }
  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }
  addEventListener(type, handler) {
    (this.listeners[type] = this.listeners[type] || []).push(handler);
  }
  dispatch(type) {
    (this.listeners[type] || []).forEach(handler => handler({ target: this }));
  }
}

const fakeDocument = { createElement: tag => new FakeElement(tag) };

function find(node, id) {
  if (node.id === id) return node;
  for (const child of node.children) {
    const found = find(child, id);
    if (found) return found;
  }
  return null;
}

function optionTexts(select) {
  return select.children.map(option => option.textContent);
}

// fakeFetch answers by URL; a function value is called so a test can hold an
// answer back. Unknown URLs fail like a 404.
function fakeFetch(routes) {
  const calls = [];
  const fetchImpl = async url => {
    calls.push(url);
    let body = routes[url];
    if (typeof body === 'function') body = await body();
    if (body === undefined) return { ok: false, status: 404, json: async () => ({}) };
    return { ok: true, status: 200, json: async () => body };
  };
  return { fetchImpl, calls };
}

const PROVIDERS = {
  providers: [
    { name: 'openai', display_name: 'OpenAI', available: true },
    { name: 'codex', display_name: 'Codex', available: true },
    { name: 'claude', display_name: 'Claude', available: false }
  ]
};
const MODELS = provider => `/api/settings/available-models?provider=${provider}`;

function mount({ routes, initial = null } = {}) {
  const host = new FakeElement('div');
  const changes = [];
  const { fetchImpl, calls } = fakeFetch(routes);
  const picker = createDefaultModelPicker({
    host,
    idPrefix: 'cr-model',
    fetchImpl,
    initial,
    documentImpl: fakeDocument,
    onChange: selection => changes.push(selection)
  });
  return { host, picker, changes, calls };
}

test('normalizeModelOptions prefers model_options and falls back to plain names', () => {
  assert.deepEqual(normalizeModelOptions([{ id: 'a', label: 'A', recommended: 1 }, { id: '' }]), [
    { id: 'a', label: 'A', description: '', recommended: true }
  ]);
  assert.deepEqual(
    normalizeModelOptions([], ['gpt-4o-mini', 'gpt-4o']).map(o => [o.id, o.recommended]),
    [
      ['gpt-4o-mini', true],
      ['gpt-4o', false]
    ]
  );
  assert.equal(
    formatModelOptionText({ label: 'Fast', recommended: true, description: 'cheap' }),
    'Fast (Recommended) — cheap'
  );
});

test('chooseProvider keeps the provider in place, else a local runtime, else the first', () => {
  const available = [{ name: 'openai' }, { name: 'ollama' }, { name: 'codex' }];
  assert.equal(chooseProvider(available, 'codex'), 'codex');
  assert.equal(chooseProvider(available, 'claude'), 'ollama');
  assert.equal(chooseProvider([{ name: 'openai' }, { name: 'codex' }]), 'openai');
  assert.equal(chooseProvider([]), '');
});

test('chooseModel keeps the model in place, else the recommended one, else the first', () => {
  const options = [{ id: 'a' }, { id: 'b', recommended: true }];
  assert.equal(chooseModel(options, 'a'), 'a');
  assert.equal(chooseModel(options, 'missing'), 'b');
  assert.equal(chooseModel([{ id: 'only' }]), 'only');
  assert.equal(chooseModel([]), '');
});

test('the picker preselects the recommended model and reports it', async () => {
  const { host, picker, changes } = mount({
    routes: {
      '/api/providers': PROVIDERS,
      [MODELS('openai')]: {
        available: true,
        model_options: [
          { id: 'gpt-big', label: 'Big' },
          { id: 'gpt-small', label: 'Small', recommended: true }
        ]
      }
    }
  });
  assert.equal(picker.selection(), null, 'nothing is selected while loading');
  await picker.ready;
  assert.deepEqual(picker.selection(), {
    provider: 'openai',
    model: 'gpt-small',
    reasoning_effort: ''
  });
  const providerSelect = find(host, 'cr-model-provider');
  assert.deepEqual(
    optionTexts(providerSelect),
    ['OpenAI', 'Codex'],
    'unavailable providers hidden'
  );
  const modelSelect = find(host, 'cr-model');
  assert.equal(modelSelect.value, 'gpt-small');
  assert.deepEqual(optionTexts(modelSelect), ['Big', 'Small (Recommended)']);
  assert.equal(find(host, 'cr-model-reasoning').disabled, true);
  assert.deepEqual(changes.at(-1), picker.selection());
  assert.ok(
    changes.some(selection => selection === null),
    'loading is reported as null'
  );
});

test('a local runtime wins the first pick', async () => {
  const { picker } = mount({
    routes: {
      '/api/providers': {
        providers: [
          { name: 'openai', display_name: 'OpenAI', available: true },
          { name: 'lmstudio', display_name: 'LM Studio', available: true }
        ]
      },
      [MODELS('lmstudio')]: { available: true, models: ['qwen'] }
    }
  });
  await picker.ready;
  assert.equal(picker.selection().provider, 'lmstudio');
  assert.equal(picker.selection().model, 'qwen');
});

test('the reasoning level shows and counts for Codex only', async () => {
  let release;
  const held = new Promise(resolve => (release = resolve));
  const { host, picker } = mount({
    routes: {
      '/api/providers': PROVIDERS,
      [MODELS('openai')]: { available: true, models: ['gpt-small'] },
      [MODELS('codex')]: () =>
        held.then(() => ({ available: true, model_options: [{ id: 'gpt-codex', label: 'Codex' }] }))
    }
  });
  await picker.ready;
  const reasoning = find(host, 'cr-model-reasoning');
  const reasoningField = host.children[0].children[2];
  assert.equal(reasoningField.hidden, true);

  const providerSelect = find(host, 'cr-model-provider');
  providerSelect.value = 'codex';
  providerSelect.dispatch('change');
  assert.equal(picker.selection(), null, 'switching providers reloads models');
  assert.equal(optionTexts(find(host, 'cr-model'))[0], 'Loading models…');
  release();
  await held;
  await new Promise(resolve => setImmediate(resolve));

  assert.equal(reasoningField.hidden, false);
  assert.deepEqual(picker.selection(), {
    provider: 'codex',
    model: 'gpt-codex',
    reasoning_effort: 'medium'
  });
  reasoning.value = 'high';
  reasoning.dispatch('change');
  assert.equal(picker.selection().reasoning_effort, 'high');
});

test('the Default model in place wins over the recommendation', async () => {
  const { picker } = mount({
    initial: { provider: 'codex', model: 'gpt-old', reasoning_effort: 'xhigh' },
    routes: {
      '/api/providers': PROVIDERS,
      [MODELS('codex')]: {
        available: true,
        model_options: [{ id: 'gpt-new', label: 'New', recommended: true }]
      }
    }
  });
  await picker.ready;
  // gpt-old is no longer listed, but it stays the choice rather than silently
  // switching to the recommendation.
  assert.deepEqual(picker.selection(), {
    provider: 'codex',
    model: 'gpt-old',
    reasoning_effort: 'xhigh'
  });
});

test('no available provider leaves nothing selected', async () => {
  const { host, picker } = mount({
    routes: { '/api/providers': { providers: [{ name: 'openai', available: false }] } }
  });
  await picker.ready;
  assert.equal(picker.selection(), null);
  assert.equal(find(host, 'cr-model-provider').disabled, true);
});

test('a failed provider load says so and selects nothing', async () => {
  const { host, picker } = mount({ routes: {} });
  await picker.ready;
  assert.equal(picker.selection(), null);
  assert.match(find(host, 'cr-model-status').textContent, /could not load the AI providers/);
});

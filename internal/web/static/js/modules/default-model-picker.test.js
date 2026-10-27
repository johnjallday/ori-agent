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
  const reasoningField = host.children[0].children[3];
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

// A fetch whose /api/providers answer can change, and which records bodies.
function connectFetch() {
  const state = { connected: false, posts: [], fail: false };
  const fetchImpl = async (url, options = {}) => {
    const json = body => ({ ok: true, status: 200, json: async () => body });
    if (url === '/api/api-key') {
      state.posts.push(JSON.parse(options.body));
      if (state.fail) return { ok: false, status: 400, json: async () => ({}) };
      state.connected = true;
      return json({});
    }
    if (url === '/api/providers') {
      return json({
        providers: state.connected
          ? [{ name: 'claude', display_name: 'Claude', available: true }]
          : [{ name: 'openai', display_name: 'OpenAI', available: false }]
      });
    }
    if (url === MODELS('claude')) {
      return json({
        available: true,
        model_options: [{ id: 'claude-x', label: 'X', recommended: true }]
      });
    }
    return { ok: false, status: 404, json: async () => ({}) };
  };
  return { state, fetchImpl };
}

function mountConnect(fetchImpl, onChange = () => {}) {
  const host = new FakeElement('div');
  const picker = createDefaultModelPicker({
    host,
    idPrefix: 'cr-model',
    fetchImpl,
    documentImpl: fakeDocument,
    onChange
  });
  return { host, picker };
}

const settle = () => new Promise(resolve => setTimeout(resolve, 10));

test('no connected provider shows the API-key entry and selects nothing', async () => {
  const { host, picker } = mountConnect(connectFetch().fetchImpl);
  await picker.ready;
  assert.equal(picker.selection(), null);
  const connect = find(host, 'cr-model-connect');
  assert.equal(connect.hidden, false);
  assert.deepEqual(
    ['openai', 'anthropic', 'gemini'].map(name => find(host, `cr-model-tab-${name}`).textContent),
    ['OpenAI', 'Anthropic', 'Gemini']
  );
  assert.match(connect.children[0].textContent, /^I need an AI connection to work\./);
  assert.match(
    connect.children[connect.children.length - 2].textContent,
    /Nothing else happens until you press Hire assistant\.$/
  );
});

test('saving a key posts only the typed key, then reloads providers in place', async () => {
  const { state, fetchImpl } = connectFetch();
  const changes = [];
  const { host, picker } = mountConnect(fetchImpl, selection => changes.push(selection));
  await picker.ready;

  // Nothing typed: no request, an error.
  find(host, 'cr-model-save-key').listeners.click[0]();
  await settle();
  assert.equal(state.posts.length, 0);
  assert.equal(find(host, 'cr-model-key-error').hidden, false);

  find(host, 'cr-model-key-anthropic').value = ' sk-ant-123 ';
  find(host, 'cr-model-save-key').listeners.click[0]();
  await settle();
  assert.deepEqual(state.posts, [{ anthropic_api_key: 'sk-ant-123' }]);
  assert.equal(find(host, 'cr-model-key-anthropic').value, '', 'the key is not kept in the field');
  assert.equal(find(host, 'cr-model-connect').hidden, true);
  assert.deepEqual(picker.selection(), {
    provider: 'claude',
    model: 'claude-x',
    reasoning_effort: ''
  });
  assert.deepEqual(changes[changes.length - 1], picker.selection());
});

test('a rejected key says so and leaves nothing selected', async () => {
  const { state, fetchImpl } = connectFetch();
  state.fail = true;
  const { host, picker } = mountConnect(fetchImpl);
  await picker.ready;
  find(host, 'cr-model-key-openai').value = 'bad';
  find(host, 'cr-model-save-key').listeners.click[0]();
  await settle();
  assert.match(find(host, 'cr-model-key-error').textContent, /could not save that key/);
  assert.equal(picker.selection(), null);
});

test('a configured Default model wins over the recommendation without an initial', async () => {
  const { picker } = mount({
    routes: {
      '/api/settings/system-model': {
        provider: 'codex',
        model: 'gpt-chosen',
        reasoning_effort: 'high',
        configured: true
      },
      '/api/providers': PROVIDERS,
      [MODELS('codex')]: {
        available: true,
        model_options: [
          { id: 'gpt-chosen', label: 'Chosen' },
          { id: 'gpt-rec', label: 'Rec', recommended: true }
        ]
      }
    }
  });
  await picker.ready;
  assert.deepEqual(picker.selection(), {
    provider: 'codex',
    model: 'gpt-chosen',
    reasoning_effort: 'high'
  });
});

test('every non-Codex provider sends an empty reasoning level', async () => {
  const { picker } = mount({
    initial: { provider: 'openai', model: 'gpt-small', reasoning_effort: 'xhigh' },
    routes: {
      '/api/providers': PROVIDERS,
      [MODELS('openai')]: { available: true, models: ['gpt-small'] }
    }
  });
  await picker.ready;
  assert.equal(picker.selection().reasoning_effort, '');
});

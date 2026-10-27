/*
 * default-model-picker.js — the provider / model / reasoning picker for Ori's
 * Default model.
 *
 * The Mission 01 hire preset uses it to ask which AI the assistant thinks
 * with, and the Today banner uses it to choose a model for an assistant hired
 * without one. Either way the answer becomes Settings → Default model; this
 * module only collects it. The caller saves it (the hire request, or
 * POST /api/settings/system-model) and emits systemModel:changed.
 *
 * Providers come from GET /api/providers (only those available on this
 * server), models from GET /api/settings/available-models. A local runtime is
 * preferred, then the first available provider; the model marked recommended
 * is preselected, unless the caller passes the Default model already in place.
 *
 * The roster controller is a classic script, so createDefaultModelPicker is
 * also published on window.OriDefaultModelPicker at the end of this file.
 */

// Local runtimes need no key and keep data on the machine, so they win the
// first pick, as the onboarding wizard's Model step did.
export const LOCAL_PROVIDERS = Object.freeze(['ollama', 'lmstudio', 'mlx_lm']);

// Codex's reasoning levels, in the order Settings lists them.
export const REASONING_LEVELS = Object.freeze([
  { value: 'medium', label: 'Medium (Recommended)' },
  { value: 'high', label: 'High' },
  { value: 'low', label: 'Low' },
  { value: 'xhigh', label: 'XHigh (Deepest)' }
]);
export const DEFAULT_REASONING_EFFORT = 'medium';
const REASONING_VALUES = new Set(REASONING_LEVELS.map(level => level.value));

export const LOADING_PROVIDERS_TEXT = 'Loading providers…';
export const LOADING_MODELS_TEXT = 'Loading models…';
export const NO_PROVIDER_TEXT = 'No AI provider is connected to Ori yet.';
const PROVIDERS_ERROR_TEXT = 'Ori could not load the AI providers. Reload the page to try again.';
const MODELS_ERROR_TEXT = 'Ori could not load this provider’s models. Choose it again to retry.';

function reasoningFor(provider, effort) {
  if (provider !== 'codex') return '';
  const value = String(effort || '')
    .trim()
    .toLowerCase();
  return REASONING_VALUES.has(value) ? value : DEFAULT_REASONING_EFFORT;
}

// normalizeModelOptions accepts the server's model_options (preferred) or its
// plain models list, and returns { id, label, description, recommended }.
export function normalizeModelOptions(rawModelOptions, fallbackModels) {
  if (Array.isArray(rawModelOptions) && rawModelOptions.length > 0) {
    return rawModelOptions
      .filter(option => option && typeof option.id === 'string' && option.id.length > 0)
      .map(option => ({
        id: option.id,
        label:
          typeof option.label === 'string' && option.label.length > 0 ? option.label : option.id,
        description: typeof option.description === 'string' ? option.description : '',
        recommended: Boolean(option.recommended)
      }));
  }
  const models = Array.isArray(fallbackModels) ? fallbackModels : [];
  return models
    .filter(model => typeof model === 'string' && model.length > 0)
    .map(model => ({
      id: model,
      label: model,
      description: '',
      recommended: model.includes('haiku') || model.includes('4o-mini')
    }));
}

export function formatModelOptionText(modelOption) {
  const recommended = modelOption.recommended ? ' (Recommended)' : '';
  if (modelOption.description) {
    return `${modelOption.label}${recommended} — ${modelOption.description}`;
  }
  return `${modelOption.label}${recommended}`;
}

// chooseProvider picks the provider to start from: the one already in place
// when it is still available, else a local runtime, else the first.
export function chooseProvider(available = [], initialProvider = '') {
  const names = available.map(provider => provider.name);
  if (initialProvider && names.includes(initialProvider)) return initialProvider;
  const local = names.find(name => LOCAL_PROVIDERS.includes(name));
  return local || names[0] || '';
}

// chooseModel picks the model to start from: the one already in place, else
// the recommended one, else the first.
export function chooseModel(options = [], initialModel = '') {
  if (initialModel && options.some(option => option.id === initialModel)) return initialModel;
  const recommended = options.find(option => option.recommended);
  return (recommended || options[0] || {}).id || '';
}

async function getJSON(fetchImpl, url) {
  const response = await fetchImpl(url, { headers: { Accept: 'application/json' } });
  if (!response.ok) throw new Error(`${url} answered ${response.status}`);
  return response.json();
}

// createDefaultModelPicker renders the picker into host and starts loading.
//
//   host        the element the picker fills (its children are replaced)
//   idPrefix    ids: <prefix>-provider, <prefix>, <prefix>-reasoning
//   initial     { provider, model, reasoning_effort } already in place, or null
//   onChange    called with selection() after every change, loads included
//
// It returns { ready, selection, reload }. ready settles after the first load;
// selection() is { provider, model, reasoning_effort } or null while loading
// or when nothing usable is chosen.
export function createDefaultModelPicker({
  host,
  idPrefix = 'default-model',
  fetchImpl = globalThis.fetch,
  initial = null,
  onChange = () => {},
  documentImpl = globalThis.document
} = {}) {
  const doc = documentImpl;
  const state = {
    loading: true,
    providers: [],
    provider: '',
    models: [],
    model: '',
    reasoning: reasoningFor(initial?.provider, initial?.reasoning_effort),
    message: ''
  };
  let initialProvider = String(initial?.provider || '').trim();
  let initialModel = String(initial?.model || '').trim();
  let generation = 0;

  const el = (tag, className = '') => {
    const node = doc.createElement(tag);
    if (className) node.className = className;
    return node;
  };
  const field = (id, labelText) => {
    const wrap = el('div', 'mb-3 default-model-picker__field');
    const label = el('label', 'form-label');
    label.setAttribute('for', id);
    label.textContent = labelText;
    const select = el('select', 'form-select modern-input');
    select.id = id;
    wrap.appendChild(label);
    wrap.appendChild(select);
    return { wrap, select };
  };
  const fillSelect = (select, options) => {
    select.replaceChildren();
    options.forEach(({ value, text }) => {
      const option = el('option');
      option.value = value;
      option.textContent = text;
      select.appendChild(option);
    });
  };

  const root = el('div', 'default-model-picker');
  const providerField = field(`${idPrefix}-provider`, 'Provider');
  const modelField = field(idPrefix, 'Model');
  const reasoningField = field(`${idPrefix}-reasoning`, 'Reasoning level');
  const status = el('p', 'form-helper default-model-picker__status');
  status.id = `${idPrefix}-status`;
  status.setAttribute('role', 'status');
  root.appendChild(providerField.wrap);
  root.appendChild(modelField.wrap);
  root.appendChild(reasoningField.wrap);
  root.appendChild(status);
  if (host) host.replaceChildren(root);

  function selection() {
    if (state.loading || !state.provider || !state.model) return null;
    return {
      provider: state.provider,
      model: state.model,
      reasoning_effort: reasoningFor(state.provider, state.reasoning)
    };
  }

  function render() {
    const providerSelect = providerField.select;
    if (state.providers.length) {
      fillSelect(
        providerSelect,
        state.providers.map(provider => ({
          value: provider.name,
          text: provider.display_name || provider.name
        }))
      );
      providerSelect.value = state.provider;
      providerSelect.disabled = false;
    } else {
      fillSelect(providerSelect, [
        { value: '', text: state.loading ? LOADING_PROVIDERS_TEXT : NO_PROVIDER_TEXT }
      ]);
      providerSelect.disabled = true;
    }

    const modelSelect = modelField.select;
    if (state.models.length) {
      fillSelect(
        modelSelect,
        state.models.map(option => ({ value: option.id, text: formatModelOptionText(option) }))
      );
      modelSelect.value = state.model;
      modelSelect.disabled = state.loading;
    } else {
      const text =
        state.loading && state.provider ? LOADING_MODELS_TEXT : 'Choose a provider first';
      fillSelect(modelSelect, [{ value: '', text }]);
      modelSelect.disabled = true;
    }

    const codex = state.provider === 'codex';
    fillSelect(
      reasoningField.select,
      REASONING_LEVELS.map(level => ({ value: level.value, text: level.label }))
    );
    reasoningField.select.value = reasoningFor('codex', state.reasoning);
    reasoningField.select.disabled = !codex;
    reasoningField.wrap.hidden = !codex;

    status.textContent = state.message;
    status.hidden = !state.message;
    root.setAttribute('data-state', state.loading ? 'loading' : selection() ? 'ready' : 'empty');
  }

  function changed() {
    render();
    try {
      onChange(selection());
    } catch (_) {
      // A caller's handler must not break the picker.
    }
  }

  // Every load takes a new generation; an answer that arrives after a newer
  // load started is dropped, so a slow provider cannot overwrite a later pick.
  async function loadModels(provider) {
    const mine = ++generation;
    state.loading = true;
    state.models = [];
    state.model = '';
    state.message = '';
    changed();
    let options = [];
    try {
      const data = await getJSON(
        fetchImpl,
        `/api/settings/available-models?provider=${encodeURIComponent(provider)}`
      );
      if (mine !== generation) return;
      options =
        data?.available === false ? [] : normalizeModelOptions(data?.model_options, data?.models);
      if (!options.length) state.message = 'This provider has no models available right now.';
    } catch (_) {
      if (mine !== generation) return;
      state.message = MODELS_ERROR_TEXT;
    }
    // The model already in place stays selectable even when the provider no
    // longer lists it, so preselecting it never silently changes the choice.
    if (provider === initialProvider && initialModel && !options.some(o => o.id === initialModel)) {
      options = [
        { id: initialModel, label: initialModel, description: '', recommended: false },
        ...options
      ];
    }
    state.models = options;
    state.model = chooseModel(options, provider === initialProvider ? initialModel : '');
    state.loading = false;
    changed();
  }

  async function loadProviders() {
    const mine = ++generation;
    state.loading = true;
    state.providers = [];
    state.provider = '';
    state.models = [];
    state.model = '';
    state.message = '';
    changed();
    let available = [];
    try {
      const data = await getJSON(fetchImpl, '/api/providers');
      if (mine !== generation) return;
      available = (Array.isArray(data?.providers) ? data.providers : []).filter(
        provider => provider && provider.available && provider.name
      );
    } catch (_) {
      if (mine !== generation) return;
      state.loading = false;
      state.message = PROVIDERS_ERROR_TEXT;
      changed();
      return;
    }
    state.providers = available;
    state.provider = chooseProvider(available, initialProvider);
    if (!state.provider) {
      state.loading = false;
      changed();
      return;
    }
    await loadModels(state.provider);
  }

  providerField.select.addEventListener('change', () => {
    const provider = String(providerField.select.value || '');
    if (!provider || provider === state.provider) return;
    state.provider = provider;
    loadModels(provider);
  });
  modelField.select.addEventListener('change', () => {
    state.model = String(modelField.select.value || '');
    changed();
  });
  reasoningField.select.addEventListener('change', () => {
    state.reasoning = String(reasoningField.select.value || DEFAULT_REASONING_EFFORT);
    changed();
  });

  const ready = loadProviders();

  return {
    ready,
    selection,
    // reload starts over from GET /api/providers, keeping what was chosen
    // when it is still available.
    reload() {
      if (state.provider) initialProvider = state.provider;
      if (state.model) initialModel = state.model;
      return loadProviders();
    },
    element: root
  };
}

if (typeof window !== 'undefined') {
  window.OriDefaultModelPicker = Object.freeze({
    LOCAL_PROVIDERS,
    REASONING_LEVELS,
    DEFAULT_REASONING_EFFORT,
    normalizeModelOptions,
    formatModelOptionText,
    chooseProvider,
    chooseModel,
    createDefaultModelPicker
  });
}

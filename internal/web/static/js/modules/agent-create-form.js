// Shared Create Agent form controller.
//
// The server-rendered <template> is inert and ID-free. Each mount clones it,
// assigns IDs from a caller-owned prefix, and scopes all subsequent queries to
// that mount. Standalone and Create Workspace draft modes can therefore reuse
// the same canonical form without reading or retaining one another's state.
(function () {
  'use strict';

  const PROFILE_STANDALONE = 'standalone';
  const PROFILE_TEMPLATE = 'template';
  const NAME_HELP = 'Use 1–100 characters: letters, numbers, spaces, underscores, and hyphens.';
  const NAME_PATTERN = /^[A-Za-z0-9 _-]+$/;
  const controllers = new WeakMap();

  const PROFILE_FIELDS = {
    [PROFILE_STANDALONE]: ['name', 'type', 'model', 'provider', 'reasoningEffort', 'systemPrompt'],
    [PROFILE_TEMPLATE]: ['name', 'type', 'model', 'provider', 'systemPrompt']
  };

  function text(value) {
    return String(value == null ? '' : value);
  }

  function normalizedText(value) {
    return text(value).trim();
  }

  function normalizeProfile(value) {
    return value === PROFILE_TEMPLATE ? PROFILE_TEMPLATE : PROFILE_STANDALONE;
  }

  function safePrefix(value) {
    const prefix = normalizedText(value).replace(/[^A-Za-z0-9_-]/g, '');
    if (!prefix) throw new Error('AgentCreateForm requires an ID prefix.');
    return prefix;
  }

  function scopedId(prefix, suffix) {
    return `${safePrefix(prefix)}${text(suffix)}`;
  }

  function supportsCodexReasoning(providerName, modelName) {
    const provider = normalizedText(providerName).toLowerCase();
    const model = normalizedText(modelName).toLowerCase();
    return provider === 'codex' || model.includes('codex');
  }

  function validateName(value) {
    const name = normalizedText(value);
    if (!name) return 'Agent name is required.';
    if (name.length > 100) return 'Agent name must be 100 characters or fewer.';
    if (!NAME_PATTERN.test(name)) {
      return 'Agent name may use only letters, numbers, spaces, underscores, and hyphens.';
    }
    return '';
  }

  function profileFields(profile) {
    return [...PROFILE_FIELDS[normalizeProfile(profile)]];
  }

  function normalizeProviders(providers) {
    return (Array.isArray(providers) ? providers : [])
      .map(provider => ({
        name: normalizedText(provider && provider.name),
        displayName:
          normalizedText(provider && provider.display_name) ||
          normalizedText(provider && provider.name),
        models: (Array.isArray(provider && provider.models) ? provider.models : [])
          .map(model => ({
            value: normalizedText(model && model.value),
            label: normalizedText(model && model.label) || normalizedText(model && model.value),
            type: normalizedText(model && model.type),
            provider:
              normalizedText(model && model.provider) || normalizedText(provider && provider.name)
          }))
          .filter(model => model.value)
      }))
      .filter(provider => provider.name || provider.models.length > 0);
  }

  function modelChoices(providers, currentModel, currentProvider) {
    const normalized = normalizeProviders(providers);
    const model = normalizedText(currentModel);
    const provider = normalizedText(currentProvider);
    const choices = normalized.flatMap(group =>
      group.models.map(item => ({ ...item, group: group.displayName || group.name }))
    );
    const known = choices.some(
      choice =>
        choice.value === model &&
        (!provider || choice.provider.toLowerCase() === provider.toLowerCase())
    );
    if (model && !known) {
      choices.unshift({
        value: model,
        label: provider ? `${provider} / ${model} (current)` : `${model} (current)`,
        type: '',
        provider,
        group: 'Current selection',
        current: true
      });
    }
    return choices;
  }

  function field(host, name) {
    return host.querySelector(`[data-agent-create-field="${name}"]`);
  }

  function selectedProvider(modelSelect) {
    const option = modelSelect && modelSelect.selectedOptions && modelSelect.selectedOptions[0];
    return normalizedText(option && option.getAttribute('data-provider'));
  }

  function assignScopedIDs(root, prefix) {
    root.querySelectorAll('[data-agent-create-id]').forEach(element => {
      element.id = scopedId(prefix, element.getAttribute('data-agent-create-id'));
    });
    root.querySelectorAll('[data-agent-create-for]').forEach(element => {
      element.setAttribute('for', scopedId(prefix, element.getAttribute('data-agent-create-for')));
    });
    root.querySelectorAll('[data-agent-create-describedby]').forEach(element => {
      const ids = normalizedText(element.getAttribute('data-agent-create-describedby'))
        .split(/\s+/)
        .filter(Boolean)
        .map(suffix => scopedId(prefix, suffix));
      if (ids.length > 0) element.setAttribute('aria-describedby', ids.join(' '));
    });
  }

  function setFieldError(host, name, message) {
    const input = field(host, name);
    const error = host.querySelector(`[data-agent-create-error="${name}"]`);
    if (input) {
      input.classList.toggle('is-invalid', Boolean(message));
      if (message) input.setAttribute('aria-invalid', 'true');
      else input.removeAttribute('aria-invalid');
    }
    if (error) {
      error.textContent = message || '';
      error.classList.toggle('d-block', Boolean(message));
    }
  }

  function setProfile(root, profile) {
    const normalized = normalizeProfile(profile);
    root.setAttribute('data-agent-create-profile', normalized);
    const prompt = field(root, 'systemPrompt');
    const promptHelp = root.querySelector('[data-agent-create-id="SystemPromptHelp"]');
    const reasoning = field(root, 'reasoningEffort');
    if (normalized === PROFILE_TEMPLATE) {
      if (prompt) prompt.removeAttribute('maxlength');
      if (promptHelp) {
        promptHelp.textContent =
          'Blueprint instructions are staged with this workspace. Existing prompts are not truncated.';
      }
      if (reasoning) {
        reasoning.disabled = true;
        reasoning.setAttribute('aria-readonly', 'true');
      }
    } else {
      if (prompt) prompt.setAttribute('maxlength', '4000');
      if (reasoning) {
        reasoning.disabled = false;
        reasoning.removeAttribute('aria-readonly');
      }
    }
  }

  function populateModels(controller, providers, values) {
    const select = field(controller.host, 'model');
    if (!select) return;
    const requestedModel = text(values && values.model);
    const requestedProvider = text(values && values.provider);
    const selectedBefore = requestedModel || select.value;
    const providerBefore = requestedProvider || selectedProvider(select);
    const choices = modelChoices(providers, selectedBefore, providerBefore);
    select.innerHTML = '';

    const defaultOption = document.createElement('option');
    defaultOption.value = '';
    defaultOption.textContent = 'Use app default';
    defaultOption.setAttribute('data-provider', '');
    select.appendChild(defaultOption);

    let activeGroup = '';
    let groupElement = null;
    choices.forEach(choice => {
      if (choice.group !== activeGroup) {
        activeGroup = choice.group;
        groupElement = document.createElement('optgroup');
        groupElement.label = activeGroup;
        select.appendChild(groupElement);
      }
      const option = document.createElement('option');
      option.value = choice.value;
      option.textContent = choice.label;
      option.setAttribute('data-type', choice.type);
      option.setAttribute('data-provider', choice.provider);
      if (choice.current) option.setAttribute('data-current-model', 'true');
      groupElement.appendChild(option);
    });

    const wanted = Array.from(select.options).find(option => {
      if (option.value !== selectedBefore) return false;
      const optionProvider = normalizedText(option.getAttribute('data-provider'));
      return !providerBefore || optionProvider.toLowerCase() === providerBefore.toLowerCase();
    });
    if (wanted) wanted.selected = true;
    else select.value = '';
    controller.providers = normalizeProviders(providers);
    filterModels(controller, field(controller.host, 'type')?.value || 'tool-calling');
  }

  function filterModels(controller, selectedType) {
    const select = field(controller.host, 'model');
    if (!select) return;
    const current = select.selectedOptions && select.selectedOptions[0];
    Array.from(select.options).forEach(option => {
      const type = normalizedText(option.getAttribute('data-type'));
      const matches = !option.value || !type || type === selectedType || option === current;
      option.disabled = !matches;
      option.hidden = !matches;
    });
    if (controller.profile === PROFILE_STANDALONE && !select.value) {
      const firstAvailable = Array.from(select.options).find(
        option => option.value && !option.disabled
      );
      if (firstAvailable) firstAvailable.selected = true;
    }
    updateReasoning(controller);
  }

  function ensureReasoningOption(select, value) {
    if (!select || !value || Array.from(select.options).some(option => option.value === value))
      return;
    const option = document.createElement('option');
    option.value = value;
    option.textContent = value;
    select.appendChild(option);
  }

  function updateReasoning(controller) {
    const model = field(controller.host, 'model');
    const reasoning = field(controller.host, 'reasoningEffort');
    const section = controller.host.querySelector('[data-agent-create-section="reasoningEffort"]');
    if (!model || !reasoning || !section) return;
    const show =
      supportsCodexReasoning(selectedProvider(model), model.value) ||
      (controller.profile === PROFILE_TEMPLATE && Boolean(normalizedText(reasoning.value)));
    section.classList.toggle('d-none', !show);
    if (controller.profile === PROFILE_STANDALONE) reasoning.disabled = !show;
  }

  function setValues(controller, values) {
    const input = values || {};
    for (const name of ['name', 'type', 'systemPrompt']) {
      if (!Object.prototype.hasOwnProperty.call(input, name)) continue;
      const element = field(controller.host, name);
      if (element) element.value = text(input[name]);
    }
    populateModels(controller, controller.providers, input);
    const reasoning = field(controller.host, 'reasoningEffort');
    if (reasoning && Object.prototype.hasOwnProperty.call(input, 'reasoningEffort')) {
      ensureReasoningOption(reasoning, text(input.reasoningEffort));
      reasoning.value = text(input.reasoningEffort);
    } else if (reasoning && controller.profile === PROFILE_STANDALONE && !reasoning.value) {
      reasoning.value = 'medium';
    }
    updateReasoning(controller);
  }

  function readValues(host) {
    const model = field(host, 'model');
    return {
      name: text(field(host, 'name')?.value),
      type: text(field(host, 'type')?.value),
      model: text(model?.value),
      provider: selectedProvider(model),
      reasoningEffort: text(field(host, 'reasoningEffort')?.value),
      systemPrompt: text(field(host, 'systemPrompt')?.value)
    };
  }

  function extract(hostOrController, requestedProfile) {
    const controller = hostOrController && hostOrController.host ? hostOrController : null;
    const host = controller ? controller.host : hostOrController;
    const profile = normalizeProfile(requestedProfile || controller?.profile);
    const raw = readValues(host);
    const values = {};
    profileFields(profile).forEach(name => {
      if (name === 'name') values[name] = normalizedText(raw[name]);
      else if (name === 'systemPrompt') values[name] = normalizedText(raw[name]);
      else values[name] = raw[name];
    });
    const errors = { name: validateName(raw.name) };
    if (profile === PROFILE_STANDALONE && raw.systemPrompt.length > 4000) {
      errors.systemPrompt = 'System prompt must be 4000 characters or fewer.';
    }
    Object.keys(errors).forEach(name => {
      if (!errors[name]) delete errors[name];
    });
    if (host && typeof host.querySelector === 'function') {
      setFieldError(host, 'name', errors.name || '');
      setFieldError(host, 'systemPrompt', errors.systemPrompt || '');
    }
    return { values, errors, valid: Object.keys(errors).length === 0 };
  }

  function mount(host, options) {
    if (!host || typeof host.querySelector !== 'function') {
      throw new Error('AgentCreateForm requires a mount host.');
    }
    const config = options || {};
    const template = document.getElementById('agentCreateFormTemplate');
    if (!template || !template.content) {
      throw new Error('The shared agent-create-form template is unavailable.');
    }
    const prefix = safePrefix(config.idPrefix);
    const profile = normalizeProfile(config.profile);
    const fragment = template.content.cloneNode(true);
    assignScopedIDs(fragment, prefix);
    const formRoot = fragment.querySelector('[data-agent-create-root]');
    if (!formRoot) throw new Error('The shared agent-create-form template is malformed.');
    setProfile(formRoot, profile);
    host.replaceChildren(fragment);

    const controller = {
      host,
      idPrefix: prefix,
      profile,
      providers: normalizeProviders(config.providers),
      get(name) {
        return field(host, name);
      },
      setValues(values) {
        setValues(controller, values);
        return controller;
      },
      setProviders(providers, values) {
        populateModels(controller, providers, values || readValues(host));
        return controller;
      },
      filterModels(type) {
        filterModels(controller, type);
        return controller;
      },
      extract() {
        return extract(controller);
      },
      setError(name, message) {
        setFieldError(host, name, message);
        return controller;
      },
      focus(name) {
        field(host, name)?.focus();
      }
    };
    controllers.set(host, controller);

    field(host, 'type')?.addEventListener('change', event => {
      filterModels(controller, event.target.value);
    });
    field(host, 'model')?.addEventListener('change', () => updateReasoning(controller));
    field(host, 'name')?.addEventListener('input', () => setFieldError(host, 'name', ''));

    setValues(controller, config.values || {});
    return controller;
  }

  window.AgentCreateForm = {
    PROFILE_STANDALONE,
    PROFILE_TEMPLATE,
    NAME_HELP,
    mount,
    extract,
    validateName,
    normalizeProviders,
    modelChoices,
    profileFields,
    scopedId,
    supportsCodexReasoning,
    getController(host) {
      return controllers.get(host) || null;
    }
  };
})();

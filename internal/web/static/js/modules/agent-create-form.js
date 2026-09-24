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

  // The role is standalone-only: a blueprint's draft agent has its role fixed
  // by the blueprint, so the template profile never collects one.
  const PROFILE_FIELDS = {
    [PROFILE_STANDALONE]: ['name', 'role', 'model', 'provider', 'reasoningEffort', 'systemPrompt'],
    [PROFILE_TEMPLATE]: ['name', 'model', 'provider', 'reasoningEffort', 'systemPrompt']
  };

  // Every field the template renders as its own section. Provider is derived
  // from the chosen model and has no section of its own.
  const SECTION_FIELDS = ['name', 'role', 'model', 'reasoningEffort', 'systemPrompt'];

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

  // Which models take a reasoning level is decided once, in
  // reasoning-effort.js. Without it the field stays hidden rather than guessing.
  function reasoningApi() {
    return (typeof window !== 'undefined' && window.OriReasoningEffort) || null;
  }

  function supportsReasoning(providerName, modelName) {
    const api = reasoningApi();
    return Boolean(api && api.supports(providerName, modelName));
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

  // A caller may omit a field its contract never accepts — a program Home
  // role's instructions are applied server-side, so that caller mounts the
  // form without a prompt box. Only the prompt is omissible: name, model, and
  // provider are what every create needs.
  const OMISSIBLE_FIELDS = ['systemPrompt'];

  function normalizeOmitted(fields) {
    return (Array.isArray(fields) ? fields : []).filter(name => OMISSIBLE_FIELDS.includes(name));
  }

  function mountedFields(profile, omitFields) {
    const omitted = normalizeOmitted(omitFields);
    return profileFields(profile).filter(name => !omitted.includes(name));
  }

  // Removes each omitted field's whole section from a mount and, when given,
  // puts one read-only note in the prompt's place. A removed field cannot be
  // typed into, so nothing a caller cannot send is ever collected.
  function omitSections(root, omitFields, note) {
    const omitted = normalizeOmitted(omitFields);
    for (const name of omitted) {
      const section = root.querySelector(`[data-agent-create-section="${name}"]`);
      if (!section) continue;
      if (name === 'systemPrompt' && normalizedText(note)) {
        const replacement = document.createElement('p');
        replacement.className = 'form-helper agent-create-form-note';
        replacement.setAttribute('data-agent-create-note', name);
        replacement.textContent = normalizedText(note);
        section.replaceWith(replacement);
      } else {
        section.remove();
      }
    }
    return omitted;
  }

  // Removes the sections a profile's contract has no field for, so a mount
  // never shows a control whose value it could not send. Returns their names.
  function dropOffProfileSections(root, profile) {
    const fields = profileFields(profile);
    const dropped = SECTION_FIELDS.filter(name => !fields.includes(name));
    for (const name of dropped) {
      root.querySelector(`[data-agent-create-section="${name}"]`)?.remove();
    }
    return dropped;
  }

  function normalizeRole(value) {
    return normalizedText(value).toLowerCase();
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
    if (normalized === PROFILE_TEMPLATE) {
      if (prompt) prompt.removeAttribute('maxlength');
      if (promptHelp) {
        promptHelp.textContent =
          'Blueprint instructions are staged with this workspace. Existing prompts are not truncated.';
      }
    } else if (prompt) {
      prompt.setAttribute('maxlength', '4000');
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
    if (controller.profile === PROFILE_STANDALONE && !select.value) {
      const firstAvailable = Array.from(select.options).find(option => option.value);
      if (firstAvailable) firstAvailable.selected = true;
    }
    updateReasoning(controller);
  }

  // Shows the reasoning level only for a model that takes one, offering exactly
  // that provider's levels. The last level chosen (by the caller or the user)
  // is kept as a preference, so browsing through a model that has no "max"
  // does not lose it when a model that has one is picked again.
  function updateReasoning(controller) {
    const model = field(controller.host, 'model');
    const reasoning = field(controller.host, 'reasoningEffort');
    const section = controller.host.querySelector('[data-agent-create-section="reasoningEffort"]');
    if (!model || !reasoning || !section) return;
    const provider = selectedProvider(model);
    const show = supportsReasoning(provider, model.value);
    section.classList.toggle('d-none', !show);
    reasoning.disabled = !show;
    if (!show) return;
    const api = reasoningApi();
    api.syncSelect(reasoning, provider, model.value, controller.reasoningPreference);
    const help = controller.host.querySelector('[data-agent-create-id="ReasoningHelp"]');
    if (help) help.textContent = api.helpText(provider, model.value);
  }

  function setValues(controller, values) {
    const input = values || {};
    for (const name of ['name', 'systemPrompt']) {
      if (!Object.prototype.hasOwnProperty.call(input, name)) continue;
      const element = field(controller.host, name);
      if (element) element.value = text(input[name]);
    }
    if (Object.prototype.hasOwnProperty.call(input, 'role')) {
      const select = field(controller.host, 'role');
      if (select) {
        // A role the select does not offer falls back to Unspecialized rather
        // than leaving the select with nothing chosen.
        const wanted = normalizeRole(input.role);
        const known = Array.from(select.options || []).some(option => option.value === wanted);
        select.value = known ? wanted : 'general';
      }
    }
    if (Object.prototype.hasOwnProperty.call(input, 'reasoningEffort')) {
      controller.reasoningPreference = normalizedText(input.reasoningEffort);
    }
    populateModels(controller, controller.providers, input);
  }

  function readValues(host) {
    const model = field(host, 'model');
    return {
      name: text(field(host, 'name')?.value),
      role: text(field(host, 'role')?.value),
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
    mountedFields(profile, controller?.omitFields).forEach(name => {
      if (name === 'name') values[name] = normalizedText(raw[name]);
      else if (name === 'role') values[name] = normalizeRole(raw[name]);
      else if (name === 'systemPrompt') values[name] = normalizedText(raw[name]);
      else if (name === 'reasoningEffort') {
        // Only a level the selected model accepts; '' for every other model.
        const api = reasoningApi();
        values[name] = api ? api.normalize(raw.provider, raw.model, raw.reasoningEffort) : '';
      } else values[name] = raw[name];
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
    dropOffProfileSections(formRoot, profile);
    const omitFields = omitSections(formRoot, config.omitFields, config.note);
    host.replaceChildren(fragment);

    const controller = {
      host,
      idPrefix: prefix,
      profile,
      omitFields,
      providers: normalizeProviders(config.providers),
      reasoningPreference: '',
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

    field(host, 'model')?.addEventListener('change', () => updateReasoning(controller));
    field(host, 'reasoningEffort')?.addEventListener('change', event => {
      controller.reasoningPreference = normalizedText(event.target?.value);
    });
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
    mountedFields,
    omitSections,
    dropOffProfileSections,
    scopedId,
    supportsReasoning,
    getController(host) {
      return controllers.get(host) || null;
    }
  };
})();

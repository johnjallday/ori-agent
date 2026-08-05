// Agent Management Module
// Handles all agent-related functionality including CRUD operations and UI management

// Create a contextual logger for this module
const agentsLog = Logger.withContext('Agents');

// Agent state management
let allAgents = [];
let currentAgentName = '';
let visibleAgentCount = 3;
let availableProviders = []; // Cache for available providers and models
let systemModelPreferencePromise = null;
let cachedSystemModelPreference = null;
// The working system assistant. Renamed from "Ori" when that name was reserved
// for the app guide, which is a character identity rather than an agent record.
const SYSTEM_ASSISTANT_AGENT_NAME = 'Workspace Manager';
const AGENT_CREATION_SKILL_CATALOG_AGENT = '__ori_agent_create_catalog__';
const agentCreationCapabilityState = {
  mcpServers: [],
  skills: [],
  selectedMCPServers: new Set(),
  selectedSkills: new Set(),
  errors: {
    mcp: '',
    skills: ''
  },
  loadingToken: 0
};

function createDefaultAgentCreationFlowState() {
  return {
    seedName: '',
    seedType: '',
    autoDescription: '',
    preferAutoConfig: false,
    workspaceId: '',
    taskId: '',
    assignTask: false,
    suggestedMCPServers: [],
    suggestedSkills: []
  };
}

let pendingAgentCreationFlow = createDefaultAgentCreationFlowState();

function isWorkspaceGeneratedMCPServer(server) {
  const name = String(server?.name || '').trim();
  return /^ws:[^:]+:mcp:/i.test(name);
}

function supportsCodexReasoning(providerName, modelName) {
  const provider = String(providerName || '')
    .trim()
    .toLowerCase();
  const model = String(modelName || '')
    .trim()
    .toLowerCase();
  return provider === 'codex' || model.includes('codex');
}

function normalizeProviderName(value) {
  return String(value || '')
    .trim()
    .toLowerCase();
}

function getProviderDisplayName(providerName) {
  const normalized = normalizeProviderName(providerName);
  const provider = availableProviders.find(
    item => normalizeProviderName(item?.name) === normalized
  );
  return String(provider?.display_name || providerName || '').trim();
}

function formatProviderModelLabel(providerName, modelName) {
  const providerLabel = getProviderDisplayName(providerName) || providerName;
  const modelLabel = String(modelName || '').trim();
  if (!providerLabel) return modelLabel;
  if (!modelLabel) return providerLabel;
  return `${providerLabel} / ${modelLabel}`;
}

function setAgentModelRecommendationMessage(message) {
  const helper = document.getElementById('agentModelRecommendation');
  if (!helper) return;

  const text = String(message || '').trim();
  helper.textContent = text;
  helper.classList.toggle('d-none', text === '');
}

async function loadSystemModelPreference(forceRefresh = false) {
  if (forceRefresh) {
    systemModelPreferencePromise = null;
    cachedSystemModelPreference = null;
  }
  if (cachedSystemModelPreference) {
    return cachedSystemModelPreference;
  }
  if (!systemModelPreferencePromise) {
    systemModelPreferencePromise = API.get('/api/settings/system-model')
      .then(data => {
        cachedSystemModelPreference = data || {};
        return cachedSystemModelPreference;
      })
      .catch(error => {
        agentsLog.debug('Failed to load system model preference', {
          error: error && error.message ? error.message : error
        });
        cachedSystemModelPreference = {};
        return cachedSystemModelPreference;
      });
  }
  return systemModelPreferencePromise;
}

function selectModelOption(modelSelect, providerName, modelName) {
  if (!modelSelect) return false;

  const normalizedProvider = normalizeProviderName(providerName);
  const normalizedModel = String(modelName || '').trim();
  if (!normalizedModel) return false;

  for (let index = 0; index < modelSelect.options.length; index += 1) {
    const option = modelSelect.options[index];
    const optionProvider = normalizeProviderName(option.getAttribute('data-provider'));
    if (option.disabled) continue;
    if (option.value === normalizedModel && optionProvider === normalizedProvider) {
      modelSelect.selectedIndex = index;
      return true;
    }
  }

  for (let index = 0; index < modelSelect.options.length; index += 1) {
    const option = modelSelect.options[index];
    if (option.disabled) continue;
    if (option.value === normalizedModel) {
      modelSelect.selectedIndex = index;
      return true;
    }
  }

  return false;
}

function updateAgentReasoningVisibility() {
  const field = document.getElementById('agentReasoningField');
  const select = document.getElementById('agentReasoning');
  const modelSelect = document.getElementById('agentModel');
  if (!field || !select || !modelSelect) return;

  const selectedOption = modelSelect.selectedOptions?.[0];
  const provider = selectedOption?.getAttribute('data-provider') || '';
  const show = supportsCodexReasoning(provider, modelSelect.value);

  field.classList.toggle('d-none', !show);
  select.disabled = !show;
  if (show && !select.value) {
    select.value = 'medium';
  }
}

// Fetch available providers and models from API
async function loadAvailableProviders() {
  try {
    const data = await API.get('/api/providers');
    availableProviders = data.providers || [];
    return availableProviders;
  } catch (error) {
    agentsLog.error('Failed to load providers', error);
    return [];
  }
}

// Populate model select with options from available providers
function populateModelSelect(modelSelect, selectedType = 'tool-calling') {
  if (!modelSelect || availableProviders.length === 0) return;

  // For orchestration agents, the user's configured system model is the right
  // default even when it wasn't categorized as "orchestration" (e.g. a local
  // Ollama model that defaults to tool-calling). Surface it in the dropdown so
  // it remains selectable.
  const systemPref = cachedSystemModelPreference || {};
  const systemProviderName = normalizeProviderName(systemPref.provider);
  const systemModelName = String(systemPref.model || '').trim();

  // Clear existing options
  modelSelect.innerHTML = '';

  // Group models by provider
  availableProviders.forEach(provider => {
    const providerGroup = document.createElement('optgroup');
    providerGroup.label = provider.display_name;

    provider.models.forEach(model => {
      const option = document.createElement('option');
      option.value = model.value;
      option.textContent = model.label;
      option.setAttribute('data-type', model.type);
      option.setAttribute('data-provider', model.provider);

      const isSystemModel =
        selectedType === 'orchestration' &&
        systemModelName &&
        model.value === systemModelName &&
        normalizeProviderName(model.provider) === systemProviderName;

      // Only show models matching the selected type (plus the system model
      // when building the orchestration view).
      if (model.type !== selectedType && !isSystemModel) {
        option.style.display = 'none';
        option.disabled = true;
      }

      providerGroup.appendChild(option);
    });

    modelSelect.appendChild(providerGroup);
  });

  // Select first available option
  for (let i = 0; i < modelSelect.options.length; i++) {
    if (!modelSelect.options[i].disabled) {
      modelSelect.selectedIndex = i;
      break;
    }
  }
}

// Initialize models on page load
async function initializeModels() {
  await loadAvailableProviders();

  // Populate the model select in the create agent modal
  const agentModelSelect = document.getElementById('agentModel');
  if (agentModelSelect) {
    populateModelSelect(agentModelSelect, 'tool-calling');
    updateAgentReasoningVisibility();
  }
}

// Initialize form validation for agent creation modal
function initializeAgentFormValidation() {
  if (typeof FormValidation === 'undefined') return;

  // Initialize character counter for system prompt
  const systemPromptInput = document.getElementById('agentSystemPrompt');
  if (systemPromptInput) {
    FormValidation.initCharCounter(systemPromptInput, { maxLength: 4000 });
  }

  // Initialize character counter for auto-config description
  const autoConfigDescription = document.getElementById('baseAutoConfigDescription');
  if (autoConfigDescription) {
    FormValidation.initCharCounter(autoConfigDescription, { maxLength: 1000 });
  }

  // Add real-time validation to agent name
  const agentNameInput = document.getElementById('agentName');
  if (agentNameInput) {
    FormValidation.initInputValidation(agentNameInput, {
      required: true,
      requiredMessage: 'Agent name is required',
      minLength: 2,
      minLengthMessage: 'Name must be at least 2 characters',
      maxLength: 50,
      maxLengthMessage: 'Name cannot exceed 50 characters',
      pattern: '^[a-zA-Z0-9][a-zA-Z0-9-_]*$',
      patternMessage:
        'Use letters, numbers, hyphens, and underscores. Must start with a letter or number.'
    });
  }
}

// Call initialization when DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', () => {
    initializeModels();
    initializeAgentFormValidation();
  });
} else {
  initializeModels();
  initializeAgentFormValidation();
}

// Agent Management Functions
function selectAgent(agentName) {
  agentsLog.debug('Selecting agent', { agent: agentName });
  currentAgent = agentName;
  // Update UI to reflect selected agent
  document.querySelectorAll('.agent-item').forEach(item => {
    item.style.background = 'var(--bg-secondary)';
  });
  event.target.closest('.agent-item').style.background = 'var(--primary-color-light)';
}

// Show add agent modal
function showAddAgentModal(options = {}) {
  pendingAgentCreationFlow = normalizeAgentCreationFlowOptions(options);
  systemModelPreferencePromise = null;
  cachedSystemModelPreference = null;
  const modalElement = document.getElementById('addAgentModal');
  if (!modalElement) {
    agentsLog.debug('addAgentModal not available on this page');
    return;
  }
  const modal = new bootstrap.Modal(modalElement);
  const agentNameInput = document.getElementById('agentName');
  const agentTypeInput = document.getElementById('agentType');
  const agentSystemPromptInput = document.getElementById('agentSystemPrompt');
  const agentModelInput = document.getElementById('agentModel');
  const agentTemperatureInput = document.getElementById('agentTemperature');
  const temperatureValueSpan = document.getElementById('temperatureValue');
  const agentAllowWebSearchInput = document.getElementById('agentAllowWebSearch');

  // Clear previous inputs
  if (agentNameInput) {
    agentNameInput.value = '';
  }
  if (agentTypeInput) {
    agentTypeInput.value = 'tool-calling'; // Default to cheapest tier
  }
  if (agentSystemPromptInput) {
    agentSystemPromptInput.value = '';
  }
  if (agentModelInput) {
    // Re-filter models based on default type (models already loaded on page init)
    populateModelSelect(agentModelInput, 'tool-calling');
  }
  setAgentModelRecommendationMessage('');
  const agentReasoningInput = document.getElementById('agentReasoning');
  if (agentReasoningInput) {
    agentReasoningInput.value = 'medium';
  }
  if (agentTemperatureInput) {
    agentTemperatureInput.value = '1.0';
    if (temperatureValueSpan) {
      temperatureValueSpan.textContent = '1.0';
    }
  }
  if (agentAllowWebSearchInput) {
    agentAllowWebSearchInput.checked = true;
  }
  resetBaseAutoConfigState();
  resetAgentCreationCapabilitySelections();
  setAgentCreationCapabilityLoadingState();
  updateAgentCreationCapabilityCopy();
  updateAgentReasoningVisibility();

  modal.show();
  void loadAgentCreationCapabilityCatalog();
  void applyPendingAgentCreationFlowToModal();

  // Focus on input after modal is shown
  setTimeout(() => {
    if (agentNameInput) {
      agentNameInput.focus();
    }
  }, 500);
}

// Filter models based on agent type
function filterModelsByType(agentType, modelSelect) {
  if (!modelSelect) return;

  // Repopulate the select with filtered models
  populateModelSelect(modelSelect, agentType);
}

function normalizeAgentCreationFlowSelections(values) {
  const source = Array.isArray(values) ? values : [values];
  const seen = new Set();
  const normalized = [];

  source.forEach(value => {
    const name = String(value || '').trim();
    const key = normalizeAgentCapabilityName(name);
    if (!name || !key || seen.has(key)) return;
    seen.add(key);
    normalized.push(name);
  });

  return normalized;
}

function normalizeAgentCreationFlowOptions(options = {}) {
  return {
    seedName: String(options?.seedName || '').trim(),
    seedType: String(options?.seedType || '').trim(),
    seedModel: String(options?.seedModel || '').trim(),
    seedProvider: String(options?.seedProvider || '').trim(),
    seedReasoningEffort: String(options?.seedReasoningEffort || '').trim(),
    seedSystemPrompt: String(options?.seedSystemPrompt || '').trim(),
    autoDescription: String(options?.autoDescription || '').trim(),
    preferAutoConfig: Boolean(options?.preferAutoConfig),
    workspaceId: String(options?.workspaceId || '').trim(),
    taskId: String(options?.taskId || '').trim(),
    assignTask: Boolean(options?.assignTask),
    suggestedMCPServers: normalizeAgentCreationFlowSelections(options?.suggestedMCPServers),
    suggestedSkills: normalizeAgentCreationFlowSelections(options?.suggestedSkills)
  };
}

function clearPendingAgentCreationFlow() {
  pendingAgentCreationFlow = createDefaultAgentCreationFlowState();
}

async function applyPendingAgentCreationFlowToModal() {
  const options = pendingAgentCreationFlow;
  if (!options) return;

  const agentNameInput = document.getElementById('agentName');
  const agentTypeInput = document.getElementById('agentType');
  const agentModelInput = document.getElementById('agentModel');
  const agentReasoningInput = document.getElementById('agentReasoning');
  const descriptionTextarea = document.getElementById('baseAutoConfigDescription');
  const autoModeInput = document.getElementById('baseConfigModeAuto');
  const manualModeInput = document.getElementById('baseConfigModeManual');

  if (options.seedName && agentNameInput) {
    agentNameInput.value = options.seedName;
  }

  // For orchestration agents, ensure the system model preference is cached
  // before populateModelSelect runs — otherwise the dropdown would drop the
  // configured model if its category doesn't match 'orchestration'.
  let systemPref = null;
  if (options.seedType === 'orchestration') {
    systemPref = await loadSystemModelPreference();
  }

  if (options.seedType && agentTypeInput) {
    agentTypeInput.value = options.seedType;
    agentTypeInput.dispatchEvent(new Event('change'));
  }

  if (options.seedModel && agentModelInput) {
    selectModelOption(agentModelInput, options.seedProvider, options.seedModel);
    if (agentReasoningInput && supportsCodexReasoning(options.seedProvider, options.seedModel)) {
      agentReasoningInput.value = options.seedReasoningEffort || 'medium';
    }
  } else if (
    options.seedType === 'orchestration' &&
    agentModelInput &&
    systemPref &&
    systemPref.configured &&
    systemPref.model
  ) {
    // No explicit seed model — the user's configured system model is the
    // right default for orchestration, matching the backend auto-config
    // override in validateAndSanitizeConfig.
    selectModelOption(agentModelInput, systemPref.provider, systemPref.model);
    if (agentReasoningInput && supportsCodexReasoning(systemPref.provider, systemPref.model)) {
      agentReasoningInput.value = systemPref.reasoning_effort || 'medium';
    }
  }

  if (options.seedSystemPrompt) {
    const systemPromptInput = document.getElementById('agentSystemPrompt');
    if (systemPromptInput) {
      systemPromptInput.value = options.seedSystemPrompt;
    }
  }

  if (options.autoDescription && descriptionTextarea) {
    descriptionTextarea.value = options.autoDescription;
  }

  if (options.preferAutoConfig && options.autoDescription) {
    if (autoModeInput) autoModeInput.checked = true;
    if (manualModeInput) manualModeInput.checked = false;
    await checkBaseLLMAvailability();
    handleBaseConfigModeChange('auto');
    if (baseLLMAvailable) {
      await generateBaseAutoConfig();
    }
  }

  updateAgentReasoningVisibility();
}

function normalizeAgentCapabilityName(value) {
  return String(value || '')
    .trim()
    .toLowerCase();
}

function deriveAgentRoleForType(agentType) {
  const normalized = String(agentType || '')
    .trim()
    .toLowerCase();
  if (normalized === 'orchestration') {
    return 'orchestrator';
  }
  return '';
}

function getAgentCreationCapabilityElements() {
  return {
    title: document.getElementById('agentCreateCapabilitiesTitle'),
    grid: document.getElementById('agentCreateCapabilitiesGrid'),
    mcpPanel: document.getElementById('agentCreateMCPPanel'),
    mcpList: document.getElementById('agentCreateMCPList'),
    skillsList: document.getElementById('agentCreateSkillsList'),
    mcpCount: document.getElementById('agentCreateMCPCount'),
    skillCount: document.getElementById('agentCreateSkillCount'),
    capabilitiesIntro: document.getElementById('agentCreateCapabilitiesIntro'),
    mcpSubtitle: document.getElementById('agentCreateMCPSubtitle'),
    capabilitiesNote: document.getElementById('agentCreateCapabilitiesNote')
  };
}

function isAgentCreationMCPSelectionEnabled() {
  return Boolean(String(pendingAgentCreationFlow?.workspaceId || '').trim());
}

function updateAgentCreationCapabilityCopy() {
  const { title, grid, mcpPanel, capabilitiesIntro, mcpSubtitle, capabilitiesNote } =
    getAgentCreationCapabilityElements();
  const hasWorkspace = isAgentCreationMCPSelectionEnabled();

  // When creating from a workspace context, hide the entire MCP/Skills section.
  // MCP connectors and skills are managed from the workspace detail panels instead.
  const section = document.getElementById('agentCreateCapabilitiesSection');
  if (section) {
    section.hidden = hasWorkspace;
  }
  if (hasWorkspace) return;

  // Standalone agent creation (no workspace) — show skills only
  if (title) {
    title.textContent = 'Review Skills';
  }

  if (mcpPanel) {
    mcpPanel.hidden = true;
  }

  if (grid) {
    grid.classList.add('is-single-column');
  }

  if (capabilitiesIntro) {
    capabilitiesIntro.textContent =
      'Optional. Skills attach to the agent here. If a skill needs MCP connectors, you will bind them after adding the agent to a workspace.';
  }

  if (mcpSubtitle) {
    mcpSubtitle.textContent = 'Global connectors shown for reference until a workspace is selected';
  }

  if (capabilitiesNote) {
    capabilitiesNote.textContent =
      'Skills with scripts are trusted for this agent when selected. MCP connector access is configured later from the target workspace.';
  }
}

function buildAgentCreationCommandSummary(server) {
  const pieces = [server?.command, ...(Array.isArray(server?.args) ? server.args.slice(0, 3) : [])]
    .map(piece => String(piece || '').trim())
    .filter(Boolean);

  return pieces.length > 0 ? pieces.join(' ') : 'Configured MCP server';
}

function resetAgentCreationCapabilitySelections() {
  agentCreationCapabilityState.selectedMCPServers = new Set();
  agentCreationCapabilityState.selectedSkills = new Set();
  agentCreationCapabilityState.errors = { mcp: '', skills: '' };
}

function applyPendingAgentCreationCapabilitySuggestions() {
  const flow = pendingAgentCreationFlow || createDefaultAgentCreationFlowState();
  const suggestedMCPServers = normalizeAgentCreationFlowSelections(flow.suggestedMCPServers);
  const suggestedSkills = normalizeAgentCreationFlowSelections(flow.suggestedSkills);
  if (suggestedMCPServers.length === 0 && suggestedSkills.length === 0) {
    return;
  }

  const availableMCPKeys = new Set(
    agentCreationCapabilityState.mcpServers.map(server =>
      normalizeAgentCapabilityName(server?.name)
    )
  );
  suggestedMCPServers.forEach(serverName => {
    const key = normalizeAgentCapabilityName(serverName);
    if (key && availableMCPKeys.has(key)) {
      agentCreationCapabilityState.selectedMCPServers.add(key);
    }
  });

  const skillByKey = new Map(
    agentCreationCapabilityState.skills.map(skill => [
      normalizeAgentCapabilityName(skill?.name),
      skill
    ])
  );
  suggestedSkills.forEach(skillName => {
    const key = normalizeAgentCapabilityName(skillName);
    const skill = skillByKey.get(key);
    if (!key || !skill) return;

    const validationErrors = Array.isArray(skill.validationErrors) ? skill.validationErrors : [];
    if (validationErrors.length > 0) return;

    const requiredMCPServers = Array.isArray(skill.requiredMCPServers)
      ? skill.requiredMCPServers
      : [];
    const missingMCPServers = requiredMCPServers.filter(
      serverName => !availableMCPKeys.has(normalizeAgentCapabilityName(serverName))
    );
    if (missingMCPServers.length > 0) return;

    agentCreationCapabilityState.selectedSkills.add(key);
  });
}

function getRequiredAgentCreationMCPServerKeys() {
  const required = new Set();
  const skillIndex = new Map(
    agentCreationCapabilityState.skills.map(skill => [
      normalizeAgentCapabilityName(skill?.name),
      skill
    ])
  );

  agentCreationCapabilityState.selectedSkills.forEach(skillKey => {
    const skill = skillIndex.get(skillKey);
    if (!skill || !Array.isArray(skill.requiredMCPServers)) return;
    skill.requiredMCPServers.forEach(serverName => {
      const normalized = normalizeAgentCapabilityName(serverName);
      if (normalized) required.add(normalized);
    });
  });

  return required;
}

function getSelectedAgentCreationMCPServers() {
  const serverNameByKey = new Map(
    agentCreationCapabilityState.mcpServers.map(server => [
      normalizeAgentCapabilityName(server?.name),
      server?.name
    ])
  );
  const selected = new Set([
    ...agentCreationCapabilityState.selectedMCPServers,
    ...getRequiredAgentCreationMCPServerKeys()
  ]);

  return Array.from(selected)
    .map(key => serverNameByKey.get(key))
    .filter(Boolean);
}

function getSelectedAgentCreationSkills() {
  return agentCreationCapabilityState.skills.filter(skill =>
    agentCreationCapabilityState.selectedSkills.has(normalizeAgentCapabilityName(skill?.name))
  );
}

function renderAgentCreationCapabilityCounts() {
  const { mcpCount, skillCount } = getAgentCreationCapabilityElements();
  const availableMCPCount = Array.isArray(agentCreationCapabilityState.mcpServers)
    ? agentCreationCapabilityState.mcpServers.length
    : 0;
  const requiredMCPCount = getRequiredAgentCreationMCPServerKeys().size;
  const suggestedMCPCount = agentCreationCapabilityState.selectedMCPServers.size;
  const selectedSkillCount = getSelectedAgentCreationSkills().length;

  if (mcpCount) {
    if (requiredMCPCount > 0) {
      mcpCount.textContent = `${requiredMCPCount} required`;
    } else if (suggestedMCPCount > 0) {
      mcpCount.textContent = `${suggestedMCPCount} suggested`;
    } else {
      mcpCount.textContent = `${availableMCPCount} available`;
    }
  }
  if (skillCount) {
    skillCount.textContent = `${selectedSkillCount} selected`;
  }
}

function setAgentCreationCapabilityLoadingState() {
  const { mcpList, skillsList } = getAgentCreationCapabilityElements();
  if (mcpList) {
    mcpList.innerHTML = '<div class="agent-create-capability-empty">Loading MCP servers...</div>';
  }
  if (skillsList) {
    skillsList.innerHTML = '<div class="agent-create-capability-empty">Loading skills...</div>';
  }
  renderAgentCreationCapabilityCounts();
}

function renderAgentCreationMCPServers() {
  const { mcpList } = getAgentCreationCapabilityElements();
  if (!mcpList) return;

  const errorMessage = agentCreationCapabilityState.errors.mcp;
  if (errorMessage) {
    mcpList.innerHTML = `<div class="agent-create-capability-empty is-error">${escapeHtml(errorMessage)}</div>`;
    renderAgentCreationCapabilityCounts();
    return;
  }

  const servers = Array.isArray(agentCreationCapabilityState.mcpServers)
    ? agentCreationCapabilityState.mcpServers
    : [];
  const requiredKeys = getRequiredAgentCreationMCPServerKeys();
  const selectionEnabled = isAgentCreationMCPSelectionEnabled();

  if (servers.length === 0) {
    mcpList.innerHTML = `
      <div class="agent-create-capability-empty">
        No global MCP servers are configured yet.
        <a href="/mcp">Open MCP Servers</a>
      </div>
    `;
    renderAgentCreationCapabilityCounts();
    return;
  }

  mcpList.innerHTML = servers
    .map(server => {
      const name = String(server?.name || '').trim();
      const normalizedName = normalizeAgentCapabilityName(name);
      const isRequired = requiredKeys.has(normalizedName);
      const isSuggested = agentCreationCapabilityState.selectedMCPServers.has(normalizedName);
      const isHighlighted = isRequired || isSuggested;
      const commandSummary = buildAgentCreationCommandSummary(server);
      const toolCount = Number.isFinite(server?.toolCount) ? server.toolCount : 0;
      const status = String(server?.status || 'configured').trim() || 'configured';
      const statusClass =
        status === 'running'
          ? 'is-success'
          : status === 'starting' || status === 'restarting'
            ? 'is-warning'
            : 'is-muted';
      const wrapperTag = selectionEnabled ? 'label' : 'div';
      const selectionInput = selectionEnabled
        ? `
        <input type="checkbox"
               value="${escapeHtml(name)}"
               data-agent-create-mcp="true"
               ${isHighlighted ? 'checked' : ''}
               ${isRequired ? 'disabled' : ''}>
      `
        : '';
      const availabilityTag = selectionEnabled
        ? '<span class="agent-create-capability-tag is-required">bind on create</span>'
        : '<span class="agent-create-capability-tag is-muted">bind from a workspace</span>';

      return `
      <${wrapperTag} class="agent-create-capability-card${isHighlighted ? ' is-selected' : ''}${isRequired ? ' is-locked' : ''}${selectionEnabled ? '' : ' is-readonly'}">
        ${selectionInput}
        <span class="agent-create-capability-copy">
          <span class="agent-create-capability-title-row">
            <span class="agent-create-capability-title">${escapeHtml(name)}</span>
            <span class="agent-create-capability-pills">
              <span class="agent-create-capability-pill ${statusClass}">${escapeHtml(status)}</span>
              ${toolCount > 0 ? `<span class="agent-create-capability-pill is-muted">${toolCount} tool${toolCount === 1 ? '' : 's'}</span>` : ''}
            </span>
          </span>
          <span class="agent-create-capability-description">${escapeHtml(commandSummary)}</span>
          <span class="agent-create-capability-tags">
            <span class="agent-create-capability-tag is-muted">${escapeHtml(server?.transport || 'stdio')}</span>
            <span class="agent-create-capability-tag is-muted">global</span>
            ${availabilityTag}
            ${isRequired ? '<span class="agent-create-capability-tag is-required">required by selected skill</span>' : ''}
            ${!isRequired && isSuggested ? '<span class="agent-create-capability-tag is-required">suggested</span>' : ''}
          </span>
        </span>
      </${wrapperTag}>
    `;
    })
    .join('');

  if (selectionEnabled) {
    mcpList.querySelectorAll('input[data-agent-create-mcp]').forEach(checkbox => {
      checkbox.addEventListener('change', () => {
        const normalizedName = normalizeAgentCapabilityName(checkbox.value);
        if (!normalizedName) return;

        if (checkbox.checked) {
          agentCreationCapabilityState.selectedMCPServers.add(normalizedName);
        } else {
          agentCreationCapabilityState.selectedMCPServers.delete(normalizedName);
        }

        renderAgentCreationMCPServers();
      });
    });
  }

  renderAgentCreationCapabilityCounts();
}

function renderAgentCreationSkills() {
  const { skillsList } = getAgentCreationCapabilityElements();
  if (!skillsList) return;

  const errorMessage = agentCreationCapabilityState.errors.skills;
  if (errorMessage) {
    skillsList.innerHTML = `<div class="agent-create-capability-empty is-error">${escapeHtml(errorMessage)}</div>`;
    renderAgentCreationCapabilityCounts();
    return;
  }

  const skills = Array.isArray(agentCreationCapabilityState.skills)
    ? agentCreationCapabilityState.skills
    : [];
  const availableMCPServerKeys = new Set(
    agentCreationCapabilityState.mcpServers.map(server =>
      normalizeAgentCapabilityName(server?.name)
    )
  );
  const selectionEnabled = isAgentCreationMCPSelectionEnabled();

  if (skills.length === 0) {
    skillsList.innerHTML = `
      <div class="agent-create-capability-empty">
        No reusable skills are available yet.
        <a href="/skills">Open Skills</a>
      </div>
    `;
    renderAgentCreationCapabilityCounts();
    return;
  }

  skillsList.innerHTML = skills
    .map(skill => {
      const name = String(skill?.name || '').trim();
      const normalizedName = normalizeAgentCapabilityName(name);
      const validationErrors = Array.isArray(skill?.validationErrors) ? skill.validationErrors : [];
      const requiredMCPServers = Array.isArray(skill?.requiredMCPServers)
        ? skill.requiredMCPServers.filter(Boolean)
        : [];
      const missingMCPServers = requiredMCPServers.filter(
        serverName => !availableMCPServerKeys.has(normalizeAgentCapabilityName(serverName))
      );
      const hasBlockingIssue = validationErrors.length > 0 || missingMCPServers.length > 0;
      const isSelected = agentCreationCapabilityState.selectedSkills.has(normalizedName);
      const description = String(skill?.description || '').trim() || 'No description provided.';

      return `
      <label class="agent-create-capability-card${isSelected ? ' is-selected' : ''}">
        <input type="checkbox"
               value="${escapeHtml(name)}"
               data-agent-create-skill="true"
               ${isSelected ? 'checked' : ''}
               ${hasBlockingIssue ? 'disabled' : ''}>
        <span class="agent-create-capability-copy">
          <span class="agent-create-capability-title-row">
            <span class="agent-create-capability-title">${escapeHtml(name)}</span>
            <span class="agent-create-capability-pills">
              <span class="agent-create-capability-pill is-source">${escapeHtml(skill?.source || 'repo')}</span>
              ${skill?.hasScripts ? '<span class="agent-create-capability-pill is-script">scripted</span>' : ''}
              ${hasBlockingIssue ? '<span class="agent-create-capability-pill is-danger">unavailable</span>' : ''}
            </span>
          </span>
          <span class="agent-create-capability-description">${escapeHtml(description)}</span>
          ${
            requiredMCPServers.length > 0
              ? `
            <span class="agent-create-capability-tags">
              ${requiredMCPServers
                .map(serverName => {
                  const missing = missingMCPServers.includes(serverName);
                  return `<span class="agent-create-capability-tag ${missing ? 'is-danger' : 'is-required'}">${escapeHtml(serverName)}</span>`;
                })
                .join('')}
            </span>
          `
              : ''
          }
          ${requiredMCPServers.length > 0 && !selectionEnabled ? '<span class="agent-create-capability-meta">Bind these connectors after adding the agent to a workspace.</span>' : ''}
          ${validationErrors.length > 0 ? `<span class="agent-create-capability-meta">${escapeHtml(validationErrors.join('; '))}</span>` : ''}
          ${missingMCPServers.length > 0 ? `<span class="agent-create-capability-meta">Missing MCP servers: ${escapeHtml(missingMCPServers.join(', '))}</span>` : ''}
        </span>
      </label>
    `;
    })
    .join('');

  skillsList.querySelectorAll('input[data-agent-create-skill]').forEach(checkbox => {
    checkbox.addEventListener('change', () => {
      const normalizedName = normalizeAgentCapabilityName(checkbox.value);
      if (!normalizedName) return;

      if (checkbox.checked) {
        agentCreationCapabilityState.selectedSkills.add(normalizedName);
      } else {
        agentCreationCapabilityState.selectedSkills.delete(normalizedName);
      }

      renderAgentCreationSkills();
      renderAgentCreationMCPServers();
    });
  });

  renderAgentCreationCapabilityCounts();
}

async function loadAgentCreationCapabilityCatalog() {
  const currentLoadToken = ++agentCreationCapabilityState.loadingToken;
  agentCreationCapabilityState.errors = { mcp: '', skills: '' };

  const [mcpResult, skillsResult] = await Promise.allSettled([
    fetch('/api/mcp/servers'),
    fetch(`/api/skills?agent=${encodeURIComponent(AGENT_CREATION_SKILL_CATALOG_AGENT)}`)
  ]);

  if (currentLoadToken !== agentCreationCapabilityState.loadingToken) {
    return;
  }

  if (mcpResult.status === 'fulfilled') {
    const response = mcpResult.value;
    if (response.ok) {
      const data = await response.json().catch(() => ({}));
      const stats = data?.stats && typeof data.stats === 'object' ? data.stats : {};
      const servers = Array.isArray(data?.servers) ? data.servers : [];
      agentCreationCapabilityState.mcpServers = servers
        .filter(server => !isWorkspaceGeneratedMCPServer(server))
        .map(server => {
          const name = String(server?.name || '').trim();
          if (!name) return null;
          const stat = stats[name] || {};
          const toolCount = Number(stat?.tool_count ?? stat?.toolCount ?? 0);
          return {
            name,
            command: server?.command || '',
            args: Array.isArray(server?.args) ? server.args : [],
            transport: server?.transport || 'stdio',
            status: stat?.status || (server?.enabled === false ? 'disabled' : 'configured'),
            toolCount
          };
        })
        .filter(Boolean)
        .sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }));
    } else {
      agentCreationCapabilityState.mcpServers = [];
      agentCreationCapabilityState.errors.mcp = 'Failed to load MCP servers.';
    }
  } else {
    agentCreationCapabilityState.mcpServers = [];
    agentCreationCapabilityState.errors.mcp = 'Failed to load MCP servers.';
  }

  if (skillsResult.status === 'fulfilled') {
    const response = skillsResult.value;
    if (response.ok) {
      const data = await response.json().catch(() => ({}));
      const skills = Array.isArray(data?.skills) ? data.skills : [];
      agentCreationCapabilityState.skills = skills
        .map(skill => {
          const name = String(skill?.name || '').trim();
          if (!name) return null;
          return {
            name,
            description: skill?.description || '',
            source: skill?.source || 'repo',
            hasScripts: Boolean(skill?.has_scripts),
            requiredMCPServers: Array.isArray(skill?.required_mcp_servers)
              ? skill.required_mcp_servers
              : [],
            validationErrors: Array.isArray(skill?.validation_errors) ? skill.validation_errors : []
          };
        })
        .filter(Boolean)
        .sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }));
    } else {
      const data = await response.json().catch(() => ({}));
      agentCreationCapabilityState.skills = [];
      agentCreationCapabilityState.errors.skills = data?.error || 'Failed to load skills.';
    }
  } else {
    agentCreationCapabilityState.skills = [];
    agentCreationCapabilityState.errors.skills = 'Failed to load skills.';
  }

  applyPendingAgentCreationCapabilitySuggestions();
  renderAgentCreationSkills();
  renderAgentCreationMCPServers();
}

async function applyAgentCreationCapabilities(agentName) {
  const selectedSkills = getSelectedAgentCreationSkills();
  const summary = {
    skillsEnabled: 0,
    scriptedSkillsTrusted: 0,
    failures: []
  };

  await Promise.all(
    selectedSkills.map(async skill => {
      try {
        await API.post(`/api/skills/${encodeURIComponent(skill.name)}/enable`, {
          agent: agentName,
          enabled: true
        });
        summary.skillsEnabled += 1;
      } catch (error) {
        summary.failures.push(`Skill ${skill.name}: ${error.message || 'failed to enable'}`);
        return;
      }

      if (skill.hasScripts) {
        try {
          await API.post(`/api/skills/${encodeURIComponent(skill.name)}/trust`, {
            agent: agentName,
            trusted: true
          });
          summary.scriptedSkillsTrusted += 1;
        } catch (error) {
          summary.failures.push(`Skill ${skill.name}: ${error.message || 'failed to trust'}`);
        }
      }
    })
  );

  return summary;
}

function describeAgentCreationCapabilities(summary) {
  const parts = [];
  if (summary.skillsEnabled > 0) {
    parts.push(`${summary.skillsEnabled} skill${summary.skillsEnabled === 1 ? '' : 's'}`);
  }
  if (summary.scriptedSkillsTrusted > 0) {
    parts.push(
      `${summary.scriptedSkillsTrusted} scripted trust${summary.scriptedSkillsTrusted === 1 ? '' : 's'}`
    );
  }

  if (parts.length === 0) return '';
  if (parts.length === 1) return parts[0];
  return `${parts.slice(0, -1).join(', ')} and ${parts[parts.length - 1]}`;
}

function findAgentCreationWorkspaceBindingByServerName(workspaceData, serverName) {
  if (!workspaceData || !serverName) return null;

  const target = normalizeAgentCapabilityName(serverName);
  const bindings = Array.isArray(workspaceData.mcp_bindings) ? workspaceData.mcp_bindings : [];
  return (
    bindings.find(binding => normalizeAgentCapabilityName(binding?.server_name) === target) || null
  );
}

function getAgentCreationWorkspaceAccessEntry(workspaceData, agentInstanceId) {
  if (!workspaceData || !agentInstanceId) return null;

  const target = String(agentInstanceId || '').trim();
  const entries = Array.isArray(workspaceData.agent_mcp_access)
    ? workspaceData.agent_mcp_access
    : [];
  return entries.find(entry => String(entry?.agent_instance_id || '').trim() === target) || null;
}

function upsertAgentCreationWorkspaceBinding(workspaceData, nextBinding) {
  if (!workspaceData || !nextBinding) return;

  const bindings = Array.isArray(workspaceData.mcp_bindings)
    ? workspaceData.mcp_bindings.slice()
    : [];
  const bindingId = String(nextBinding.id || '').trim();
  const existingIndex = bindings.findIndex(
    binding => String(binding?.id || '').trim() === bindingId
  );
  if (existingIndex >= 0) {
    bindings[existingIndex] = nextBinding;
  } else {
    bindings.push(nextBinding);
  }
  workspaceData.mcp_bindings = bindings;
}

function upsertAgentCreationWorkspaceAccessEntry(workspaceData, nextEntry) {
  if (!workspaceData || !nextEntry) return;

  const entries = Array.isArray(workspaceData.agent_mcp_access)
    ? workspaceData.agent_mcp_access.slice()
    : [];
  const agentInstanceId = String(nextEntry.agent_instance_id || '').trim();
  const existingIndex = entries.findIndex(
    entry => String(entry?.agent_instance_id || '').trim() === agentInstanceId
  );
  if (existingIndex >= 0) {
    entries[existingIndex] = nextEntry;
  } else {
    entries.push(nextEntry);
  }
  workspaceData.agent_mcp_access = entries;
}

function mergeAgentCreationBindingIDs(values = []) {
  return Array.from(new Set(values.map(value => String(value || '').trim()).filter(Boolean)));
}

async function bindAgentCreationMCPServersForWorkspace(
  workspaceId,
  agentInstanceId,
  serverNames,
  workspaceData
) {
  const normalizedWorkspaceId = String(workspaceId || '').trim();
  const normalizedAgentInstanceId = String(agentInstanceId || '').trim();
  const dedupedServerNames = Array.from(
    new Set(
      (Array.isArray(serverNames) ? serverNames : [])
        .map(value => String(value || '').trim())
        .filter(Boolean)
    )
  );
  const summary = {
    connectorsReady: 0,
    failures: []
  };

  if (!normalizedWorkspaceId || !normalizedAgentInstanceId || dedupedServerNames.length === 0) {
    return summary;
  }

  const localWorkspaceData =
    workspaceData && typeof workspaceData === 'object'
      ? workspaceData
      : { mcp_bindings: [], agent_mcp_access: [] };

  if (!Array.isArray(localWorkspaceData.mcp_bindings)) {
    localWorkspaceData.mcp_bindings = [];
  }
  if (!Array.isArray(localWorkspaceData.agent_mcp_access)) {
    localWorkspaceData.agent_mcp_access = [];
  }

  for (const serverName of dedupedServerNames) {
    let binding = findAgentCreationWorkspaceBindingByServerName(localWorkspaceData, serverName);

    try {
      if (!binding) {
        const created = await API.post(
          `/api/workspaces/${encodeURIComponent(normalizedWorkspaceId)}/mcp-bindings`,
          {
            server_name: serverName,
            enabled: true
          }
        );
        binding = created?.binding || {
          id: '',
          server_name: serverName,
          enabled: true
        };
      } else if (binding.enabled === false) {
        const updated = await API.put(
          `/api/workspaces/${encodeURIComponent(normalizedWorkspaceId)}/mcp-bindings/${encodeURIComponent(binding.id)}`,
          { enabled: true }
        );
        binding = updated?.binding || { ...binding, enabled: true };
      }
    } catch (error) {
      summary.failures.push(`MCP ${serverName}: ${error.message || 'failed to bind in workspace'}`);
      continue;
    }

    upsertAgentCreationWorkspaceBinding(localWorkspaceData, binding);

    const bindingId = String(binding?.id || '').trim();
    if (!bindingId) {
      summary.failures.push(`MCP ${serverName}: missing workspace binding identifier`);
      continue;
    }

    const accessEntry = getAgentCreationWorkspaceAccessEntry(
      localWorkspaceData,
      normalizedAgentInstanceId
    );
    const nextBindingIDs = mergeAgentCreationBindingIDs([
      ...(Array.isArray(accessEntry?.enabled_binding_ids) ? accessEntry.enabled_binding_ids : []),
      bindingId
    ]);

    try {
      await API.put(
        `/api/workspaces/${encodeURIComponent(normalizedWorkspaceId)}/agent-mcp-access/${encodeURIComponent(normalizedAgentInstanceId)}`,
        { enabled_binding_ids: nextBindingIDs }
      );
      upsertAgentCreationWorkspaceAccessEntry(localWorkspaceData, {
        ...(accessEntry || {}),
        agent_instance_id: normalizedAgentInstanceId,
        enabled_binding_ids: nextBindingIDs
      });
      summary.connectorsReady += 1;
    } catch (error) {
      summary.failures.push(
        `MCP ${serverName}: ${error.message || 'failed to update agent access'}`
      );
    }
  }

  return summary;
}

async function applyPendingAgentCreationFollowUp(agentName) {
  const flow = pendingAgentCreationFlow;
  const summary = {
    workspaceAdded: false,
    workspaceMCPReady: 0,
    taskAssigned: false,
    failures: []
  };

  if (!flow || (!flow.workspaceId && !flow.taskId)) {
    return summary;
  }

  let workspaceAgentInstanceId = '';
  let workspaceData = null;

  if (flow.workspaceId) {
    try {
      const result = await API.post(
        `/api/workspaces/${encodeURIComponent(flow.workspaceId)}/agents`,
        {
          agent_name: agentName
        }
      );
      summary.workspaceAdded = true;
      workspaceAgentInstanceId = String(result?.agent_instance?.id || '').trim();
      workspaceData =
        result?.workspace && typeof result.workspace === 'object'
          ? {
              ...result.workspace,
              mcp_bindings: Array.isArray(result.workspace.mcp_bindings)
                ? result.workspace.mcp_bindings
                : [],
              agent_mcp_access: Array.isArray(result.workspace.agent_mcp_access)
                ? result.workspace.agent_mcp_access
                : []
            }
          : { mcp_bindings: [], agent_mcp_access: [] };
      if (window.EventBus) {
        EventBus.emit('workspace:agents:updated', { workspaceId: flow.workspaceId, agentName });
      }
    } catch (error) {
      summary.failures.push(`workspace add: ${error.message || 'failed'}`);
    }
  }

  if (flow.workspaceId && summary.workspaceAdded) {
    if (!workspaceAgentInstanceId) {
      summary.failures.push('MCP binding: missing workspace agent instance');
    } else {
      const mcpBindingSummary = await bindAgentCreationMCPServersForWorkspace(
        flow.workspaceId,
        workspaceAgentInstanceId,
        getSelectedAgentCreationMCPServers(),
        workspaceData
      );
      summary.workspaceMCPReady = mcpBindingSummary.connectorsReady;
      if (mcpBindingSummary.failures.length > 0) {
        summary.failures.push(...mcpBindingSummary.failures);
      }
    }
  }

  if (flow.taskId && flow.assignTask && (!flow.workspaceId || summary.workspaceAdded)) {
    try {
      await API.put(`/api/orchestration/tasks/${encodeURIComponent(flow.taskId)}`, {
        to: agentName
      });
      summary.taskAssigned = true;
      if (window.EventBus) {
        EventBus.emit('task:updated', { workspaceId: flow.workspaceId, taskId: flow.taskId });
      }
    } catch (error) {
      summary.failures.push(`task assignment: ${error.message || 'failed'}`);
    }
  }

  return summary;
}

function describeAgentCreationFollowUp(summary) {
  const parts = [];
  if (summary.workspaceAdded) {
    parts.push('added to the workspace');
  }
  if (summary.workspaceMCPReady > 0) {
    parts.push(
      `${summary.workspaceMCPReady} MCP connector${summary.workspaceMCPReady === 1 ? '' : 's'} ready in the workspace`
    );
  }
  if (summary.taskAssigned) {
    parts.push('assigned to the task');
  }
  if (parts.length === 0) return '';
  if (parts.length === 1) return parts[0];
  return `${parts.slice(0, -1).join(', ')} and ${parts[parts.length - 1]}`;
}

// Create new agent
async function createNewAgent() {
  const agentNameInput = document.getElementById('agentName');
  const agentTypeInput = document.getElementById('agentType');
  const agentSystemPromptInput = document.getElementById('agentSystemPrompt');
  const agentModelInput = document.getElementById('agentModel');
  const agentReasoningInput = document.getElementById('agentReasoning');
  const agentTemperatureInput = document.getElementById('agentTemperature');
  const agentAllowWebSearchInput = document.getElementById('agentAllowWebSearch');
  const createBtn = document.getElementById('createAgentBtn');

  if (!agentNameInput) return;

  const agentName = agentNameInput.value.trim();
  if (!agentName) {
    if (window.Toast) {
      Toast.warning('Please enter an agent name', { title: 'Missing Name' });
    }
    agentNameInput.focus();
    return;
  }

  // Set loading state
  const originalText = createBtn.textContent;
  createBtn.disabled = true;
  createBtn.innerHTML =
    '<span class="spinner-border spinner-border-sm me-2" role="status"></span>Creating...';

  try {
    const requestBody = { name: agentName };
    requestBody.allow_web_search = agentAllowWebSearchInput
      ? Boolean(agentAllowWebSearchInput.checked)
      : true;

    // Add agent type if provided
    if (agentTypeInput && agentTypeInput.value) {
      requestBody.type = agentTypeInput.value;
      const inferredRole = deriveAgentRoleForType(agentTypeInput.value);
      if (inferredRole) {
        requestBody.role = inferredRole;
      }
    }

    // Add model if provided
    if (agentModelInput && agentModelInput.value) {
      requestBody.model = agentModelInput.value;
      const selectedModelOption = agentModelInput.selectedOptions?.[0];
      const selectedProvider = selectedModelOption?.getAttribute('data-provider');
      if (selectedProvider) {
        requestBody.llm_provider = selectedProvider;
      }
      if (
        supportsCodexReasoning(selectedProvider, agentModelInput.value) &&
        agentReasoningInput?.value
      ) {
        requestBody.reasoning_effort = agentReasoningInput.value;
      }
    }

    // Add temperature if provided
    if (agentTemperatureInput && agentTemperatureInput.value) {
      requestBody.temperature = parseFloat(agentTemperatureInput.value);
    }

    // Add system prompt if provided
    if (agentSystemPromptInput && agentSystemPromptInput.value.trim()) {
      requestBody.system_prompt = agentSystemPromptInput.value.trim();
    }

    await API.post('/api/agents', requestBody);

    const capabilitySelections = {
      mcpServers: getSelectedAgentCreationMCPServers(),
      skills: getSelectedAgentCreationSkills()
    };
    let capabilitySummary = {
      skillsEnabled: 0,
      scriptedSkillsTrusted: 0,
      failures: []
    };

    if (capabilitySelections.skills.length > 0) {
      createBtn.innerHTML =
        '<span class="spinner-border spinner-border-sm me-2" role="status"></span>Configuring...';
      capabilitySummary = await applyAgentCreationCapabilities(agentName);
    }

    let followUpSummary = {
      workspaceAdded: false,
      taskAssigned: false,
      failures: []
    };
    if (pendingAgentCreationFlow.workspaceId || pendingAgentCreationFlow.taskId) {
      createBtn.innerHTML =
        '<span class="spinner-border spinner-border-sm me-2" role="status"></span>Linking...';
      followUpSummary = await applyPendingAgentCreationFollowUp(agentName);
    }

    // Success - close modal and refresh agent list
    const modal = bootstrap.Modal.getInstance(document.getElementById('addAgentModal'));
    if (modal) {
      modal.hide();
    }

    // Clear form
    agentNameInput.value = '';
    if (agentSystemPromptInput) {
      agentSystemPromptInput.value = '';
    }
    if (agentModelInput) {
      // Select first available tool-calling model
      const firstToolCallingOption = agentModelInput.querySelector(
        'option[data-type="tool-calling"]:not([disabled])'
      );
      if (firstToolCallingOption) {
        agentModelInput.value = firstToolCallingOption.value;
      }
    }
    if (agentTemperatureInput) {
      agentTemperatureInput.value = '1.0';
    }
    if (agentReasoningInput) {
      agentReasoningInput.value = 'medium';
    }
    if (agentAllowWebSearchInput) {
      agentAllowWebSearchInput.checked = true;
    }
    resetBaseAutoConfigState();
    resetAgentCreationCapabilitySelections();
    clearPendingAgentCreationFlow();
    updateAgentReasoningVisibility();

    // Show success message
    agentsLog.info('Agent created successfully', {
      agent: agentName,
      capabilities: capabilitySummary,
      followUp: followUpSummary
    });
    if (window.Toast) {
      const capabilityDetails = describeAgentCreationCapabilities(capabilitySummary);
      const followUpDetails = describeAgentCreationFollowUp(followUpSummary);
      const failureCount = capabilitySummary.failures.length + followUpSummary.failures.length;
      const detailParts = [capabilityDetails, followUpDetails].filter(Boolean);
      const detailText = detailParts.length > 0 ? ` with ${detailParts.join(' and ')}` : '';
      if (failureCount > 0) {
        Toast.warning(
          `Agent "${agentName}" created${detailText}, but ${failureCount} follow-up update${failureCount === 1 ? '' : 's'} failed.`
        );
      } else {
        Toast.success(`Agent "${agentName}" created successfully${detailText}.`);
      }
    }
    if (capabilitySummary.failures.length > 0 || followUpSummary.failures.length > 0) {
      agentsLog.warn('Agent created with capability setup warnings', {
        agent: agentName,
        failures: [...capabilitySummary.failures, ...followUpSummary.failures]
      });
    }

    // Emit event for other modules
    EventBus.emit('agent:created', { name: agentName, type: requestBody.type });

    // Refresh the agent list
    agentsLog.debug('Refreshing agent list...');
    await refreshAgentList();

    // Force page reload to ensure UI updates
    agentsLog.debug('Reloading page to show new agent...');
    window.location.reload();
  } catch (error) {
    agentsLog.error('Error creating agent', error);
    if (window.Toast) {
      Toast.error(`Failed to create agent: ${error.message}`);
    }
  } finally {
    // Reset button state
    createBtn.disabled = false;
    createBtn.innerHTML = originalText;
  }
}

// Load and display agents for sidebar
async function loadAgentsForSidebar() {
  agentsLog.debug('Loading agents from /api/agents...');
  try {
    const data = await API.get('/api/agents');
    const all = Array.isArray(data.agents) ? data.agents : [];
    // Every definition is listed — entry agents are no longer hidden from the
    // global sidebar; their workspace membership is surfaced elsewhere
    // (PRD FR6). Keep only well-formed entries.
    const visible = all.filter(agent => !!agent);
    agentsLog.debug('Received agents', { count: all.length, visible: visible.length });
    displayAgents(visible, resolveSidebarCurrentAgent());
  } catch (error) {
    agentsLog.error('Error loading agents', error);
    const agentsList = document.getElementById('agentsList');
    if (agentsList) {
      agentsList.innerHTML = '<div class="text-muted small p-2">Failed to load agents</div>';
    }
  }
}

// Display agents in the sidebar with pagination
function displayAgents(agents, currentAgent) {
  agentsLog.debug('Displaying agents', { count: agents?.length || 0 });
  const agentsList = document.getElementById('agentsList');
  if (!agentsList) {
    agentsLog.warn('agentsList element not found');
    return;
  }

  // Sort agents: current/active agent first, then alphabetically
  const sortedAgents = [...agents].sort((a, b) => {
    const nameA = typeof a === 'string' ? a : a.name;
    const nameB = typeof b === 'string' ? b : b.name;

    // Current agent comes first
    if (nameA === currentAgent) return -1;
    if (nameB === currentAgent) return 1;

    // Keep the system assistant visible because it owns the merged progress UI.
    if (isSystemAssistantAgentName(nameA)) return -1;
    if (isSystemAssistantAgentName(nameB)) return 1;

    // Then sort alphabetically
    return nameA.localeCompare(nameB);
  });

  // Store the data for pagination
  allAgents = sortedAgents;
  currentAgentName = currentAgent;

  renderAgents();
}

function resolveSidebarCurrentAgent() {
  const sessionAgent = window.sessionManager?.getActiveSession?.()?.agent_name;
  return sessionAgent || '';
}

function renderAgents() {
  agentsLog.debug('Rendering agents', {
    total: allAgents?.length || 0,
    visible: visibleAgentCount
  });
  const agentsList = document.getElementById('agentsList');
  if (!agentsList) {
    agentsLog.warn('agentsList element not found in renderAgents');
    return;
  }

  // Clear existing agents
  agentsList.innerHTML = '';

  // Show only the first 'visibleAgentCount' agents
  const agentsToShow = allAgents.slice(0, visibleAgentCount);

  // Add each visible agent
  agentsToShow.forEach(agent => {
    const agentItem = createAgentElement(agent, currentAgentName);
    agentsList.appendChild(agentItem);
  });

  // Add pagination buttons
  if (allAgents.length > 3) {
    const paginationBtn = document.createElement('div');
    paginationBtn.className = 'agent-pagination';

    if (visibleAgentCount < allAgents.length) {
      // Show "Load More" button
      paginationBtn.innerHTML = `
        <button class="modern-btn modern-btn-secondary w-100 mt-2" style="font-size: 0.875rem;" onclick="loadMoreAgents()">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
            <path d="M7.41,8.58L12,13.17L16.59,8.58L18,10L12,16L6,10L7.41,8.58Z"/>
          </svg>
          Load More (${allAgents.length - visibleAgentCount} more)
        </button>
      `;
    } else {
      // Show "Hide" button
      paginationBtn.innerHTML = `
        <button class="modern-btn modern-btn-secondary w-100 mt-2" style="font-size: 0.875rem;" onclick="hideAgents()">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
            <path d="M7.41,15.41L12,10.83L16.59,15.41L18,14L12,8L6,14L7.41,15.41Z"/>
          </svg>
          Show Less
        </button>
      `;
    }

    agentsList.appendChild(paginationBtn);
  }

  // Setup accordion listeners after rendering
  setupAccordionListeners();

  if (typeof EventBus !== 'undefined') {
    EventBus.emit('agents:rendered', { count: agentsToShow.length });
  }

  // Load settings for the current agent accordion when it's expanded
  agentsToShow.forEach(agent => {
    const agentName = typeof agent === 'string' ? agent : agent.name;
    const accordionId = `agent-${agentName.replace(/\s+/g, '-')}`;
    const collapseElement = document.getElementById(`collapse-${accordionId}`);

    if (collapseElement) {
      collapseElement.addEventListener('shown.bs.collapse', async function () {
        await loadAgentSettings(agentName, getAgentType(agent), accordionId);
      });
    }
  });
}

function loadMoreAgents() {
  visibleAgentCount = allAgents.length; // Show all agents
  renderAgents();
}

function hideAgents() {
  visibleAgentCount = 3; // Show only first 3 agents
  renderAgents();
}

function getAgentName(agent) {
  return typeof agent === 'string' ? agent : agent?.name;
}

function getAgentType(agent) {
  return typeof agent === 'string' ? 'tool-calling' : agent?.type || 'tool-calling';
}

function getAgentStatus(agent) {
  return typeof agent === 'string' ? '' : String(agent?.status || '').trim();
}

function isSystemAssistantAgentName(agentName) {
  return (
    String(agentName || '')
      .trim()
      .toLowerCase() === SYSTEM_ASSISTANT_AGENT_NAME.toLowerCase()
  );
}

// Built-in CLI agents (Claude Code, Codex, Gemini CLI) are projected from the
// CLI registry, not stored records; the backend rejects deleting them.
function isCLIAgentEntry(agent) {
  if (typeof agent === 'string') return false;
  return (
    String(agent?.source || '')
      .trim()
      .toLowerCase() === 'cli' ||
    String(agent?.role || '')
      .trim()
      .toLowerCase() === 'cli_agent'
  );
}

function normalizeAgentEvolution(agent) {
  const evolution = typeof agent === 'string' ? null : agent?.evolution;
  if (!evolution || typeof evolution !== 'object') {
    return null;
  }

  const level = Number.isFinite(Number(evolution.level)) ? Math.max(0, Number(evolution.level)) : 0;
  const experience = Number.isFinite(Number(evolution.experience))
    ? Math.max(0, Number(evolution.experience))
    : 0;
  const stage =
    typeof evolution.stage === 'string' && evolution.stage.trim()
      ? evolution.stage.trim()
      : 'spark';
  const path = typeof evolution.path === 'string' ? evolution.path.trim() : '';

  return { level, experience, stage, path };
}

function toTitleCaseLabel(value) {
  return String(value || '')
    .split(/[\s_-]+/)
    .filter(Boolean)
    .map(word => word.charAt(0).toUpperCase() + word.slice(1).toLowerCase())
    .join(' ');
}

function renderAgentEvolutionSummary(evolution) {
  if (!evolution || !window.oriFeatures?.evolutionEnabled) {
    return '';
  }

  const stageLabel = escapeHtml(toTitleCaseLabel(evolution.stage));
  const pathLabel = evolution.path ? escapeHtml(toTitleCaseLabel(evolution.path)) : '';
  const level = Math.max(0, Math.floor(evolution.level));
  const experience = Math.max(0, Math.floor(evolution.experience));
  const progress = experience % 100;
  const progressPercent = Math.min(100, Math.max(0, Math.round(progress)));

  return `
    <div class="mt-1">
      <div class="d-flex align-items-center gap-1 flex-wrap">
        <span class="badge" style="background: var(--primary-color-light); color: var(--primary-color); font-size: 0.62rem;">${stageLabel}</span>
        ${pathLabel ? `<span class="badge" style="background: var(--bg-tertiary); color: var(--text-secondary); font-size: 0.62rem;">${pathLabel}</span>` : ''}
        <span style="color: var(--text-secondary); font-size: 0.66rem;">Lv ${level}</span>
      </div>
      <div class="progress mt-1" style="height: 4px; background: var(--bg-tertiary);">
        <div class="progress-bar" role="progressbar" style="width: ${progressPercent}%; background: var(--primary-color);" aria-valuenow="${progressPercent}" aria-valuemin="0" aria-valuemax="100"></div>
      </div>
    </div>
  `;
}

function renderAssistantProgressSlot() {
  if (!window.oriFeatures?.evolutionEnabled) {
    return '';
  }

  return `
    <div class="sidebar-assistant-progress d-none" data-assistant-progress-widget aria-live="polite">
      <div class="sidebar-assistant-progress-meta">
        <span class="sidebar-assistant-rank-badge badge" data-assistant-rank-badge>Novice</span>
        <span class="sidebar-assistant-level" data-assistant-level-value>Level 0</span>
        <span class="sidebar-assistant-xp" data-assistant-xp-value>0 XP</span>
      </div>
      <div class="progress">
        <div class="progress-bar" data-assistant-xp-progress-bar role="progressbar" style="width: 0%;" aria-valuemin="0" aria-valuemax="100" aria-valuenow="0"></div>
      </div>
    </div>
  `;
}

// Create agent element with accordion
function createAgentElement(agent, currentAgent) {
  const agentName = getAgentName(agent);
  const agentType = getAgentType(agent);
  const evolution = normalizeAgentEvolution(agent);
  const isCurrentAgent = agentName === currentAgent;
  const isSystemAssistant = isSystemAssistantAgentName(agentName);
  const isDisabled = getAgentStatus(agent) === 'disabled';
  const accordionId = `agent-${agentName.replace(/\s+/g, '-')}`;

  // Format type label
  const typeLabels = {
    'tool-calling': 'Tool Calling',
    general: 'General',
    research: 'Research'
  };
  const typeLabel = typeLabels[agentType] || agentType;

  const agentDiv = document.createElement('div');
  agentDiv.className = 'accordion-item mb-2';
  agentDiv.style.background = 'var(--bg-secondary)';
  agentDiv.style.border = `1px solid var(--border-color)`;
  agentDiv.style.borderRadius = '8px';

  // Escape agent name for safe HTML rendering (XSS prevention)
  const safeAgentName = escapeHtml(agentName);
  const safeAgentNameJs = escapeJs(agentName);
  const safeAgentNameAttr = escapeAttr(agentName);
  const safeTypeLabel = escapeHtml(typeLabel);
  const evolutionSummary = isSystemAssistant
    ? renderAssistantProgressSlot()
    : renderAgentEvolutionSummary(evolution);
  const disabledBadge = isDisabled
    ? '<span class="badge" style="background: rgba(234, 179, 8, 0.16); color: #ca8a04; font-size: 0.62rem;">Disabled</span>'
    : '';
  const newChatAction = isDisabled
    ? `<span class="modern-btn modern-btn-secondary px-2 py-1 disabled" title="Enable this agent before starting a chat" aria-disabled="true" style="font-size: 0.75rem; cursor: not-allowed; opacity: 0.55;">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" class="me-1" style="vertical-align: -1px;" aria-hidden="true">
          <path d="M20,2H4A2,2 0 0,0 2,4V22L6,18H20A2,2 0 0,0 22,16V4A2,2 0 0,0 20,2Z"/>
        </svg>
        Disabled
      </span>`
    : `<span class="modern-btn modern-btn-secondary px-2 py-1" onclick="event.stopPropagation(); newChatWithAgent('${safeAgentNameJs}')" title="Start new chat with this agent" style="font-size: 0.75rem; cursor: pointer;">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" class="me-1" style="vertical-align: -1px;" aria-hidden="true">
          <path d="M20,2H4A2,2 0 0,0 2,4V22L6,18H20A2,2 0 0,0 22,16V4A2,2 0 0,0 20,2Z"/>
        </svg>
        New Chat
      </span>`;

  // Built-in agents (system assistant + CLI agents) cannot be deleted; the
  // backend rejects it, so render the control as a disabled, non-clickable icon.
  const deleteBlockedReason = isSystemAssistant
    ? 'System assistant cannot be deleted.'
    : isCLIAgentEntry(agent)
      ? 'Built-in CLI agent cannot be deleted.'
      : '';
  const deleteIconSvg = `<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
              <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
            </svg>`;
  const deleteAction = deleteBlockedReason
    ? `<span class="sidebar-agent-delete disabled" title="${escapeAttr(deleteBlockedReason)}" role="button" aria-disabled="true" tabindex="-1">
            ${deleteIconSvg}
          </span>`
    : `<span class="sidebar-agent-delete" onclick="event.stopPropagation(); deleteAgent('${safeAgentNameJs}')" title="Delete agent" role="button" tabindex="0">
            ${deleteIconSvg}
          </span>`;

  agentDiv.innerHTML = `
    <div class="accordion-header" id="heading-${accordionId}">
      <button class="d-flex align-items-center justify-content-between p-2 w-100 border-0 accordion-button collapsed"
              type="button"
              data-bs-toggle="collapse"
              data-bs-target="#collapse-${accordionId}"
              aria-expanded="false"
              aria-controls="collapse-${accordionId}"
              style="background: ${isCurrentAgent ? 'var(--primary-color-light)' : 'var(--bg-secondary)'}; color: var(--text-primary); text-align: left;">
        <div class="d-flex align-items-center gap-2 flex-grow-1">
          <div class="d-flex flex-column">
            <span style="color: var(--text-primary); font-weight: 500;">${safeAgentName}</span>
            <span style="color: var(--text-secondary); font-size: 0.7rem;">${safeTypeLabel}</span>
            ${disabledBadge}
            ${evolutionSummary}
          </div>
        </div>
        <div class="agent-actions d-flex align-items-center gap-2">
          ${newChatAction}
          ${deleteAction}
        </div>
      </button>
    </div>
    <div id="collapse-${accordionId}" class="accordion-collapse collapse" aria-labelledby="heading-${accordionId}">
      <div class="accordion-body p-3" style="background: var(--bg-tertiary);">
        <h6 class="fw-semibold mb-3" style="color: var(--text-primary);">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
            <path d="M12,15.5A3.5,3.5 0 0,1 8.5,12A3.5,3.5 0 0,1 12,8.5A3.5,3.5 0 0,1 15.5,12A3.5,3.5 0 0,1 12,15.5M19.43,12.97C19.47,12.65 19.5,12.33 19.5,12C19.5,11.67 19.47,11.34 19.43,11L21.54,9.37C21.73,9.22 21.78,8.95 21.66,8.73L19.66,5.27C19.54,5.05 19.27,4.96 19.05,5.05L16.56,6.05C16.04,5.66 15.5,5.32 14.87,5.07L14.5,2.42C14.46,2.18 14.25,2 14,2H10C9.75,2 9.54,2.18 9.5,2.42L9.13,5.07C8.5,5.32 7.96,5.66 7.44,6.05L4.95,5.05C4.73,4.96 4.46,5.05 4.34,5.27L2.34,8.73C2.22,8.95 2.27,9.22 2.46,9.37L4.57,11C4.53,11.34 4.5,11.67 4.5,12C4.5,12.33 4.53,12.65 4.57,12.97L2.46,14.63C2.27,14.78 2.22,15.05 2.34,15.27L4.34,18.73C4.46,18.95 4.73,19.03 4.95,18.95L7.44,17.94C7.96,18.34 8.5,18.68 9.13,18.93L9.5,21.58C9.54,21.82 9.75,22 10,22H14C14.25,22 14.46,21.82 14.5,21.58L14.87,18.93C15.5,18.68 16.04,18.34 16.56,17.94L19.05,18.95C19.27,19.03 19.54,18.95 19.66,18.73L21.66,15.27C21.78,15.05 21.73,14.78 21.54,14.63L19.43,12.97Z"/>
          </svg>
          Settings
        </h6>

        <div class="setting-item mb-3">
          <div class="d-flex flex-column">
            <label style="color: var(--text-primary); font-size: 0.85rem; margin-bottom: 0.5rem;">Agent Name</label>
            <input type="text" id="agentNameInput-${accordionId}" class="form-control form-control-sm"
                   value="${safeAgentNameAttr}"
                   style="background: var(--bg-primary); border: 1px solid var(--border-color); color: var(--text-primary); font-size: 0.85rem;"
                   placeholder="Enter agent name">
          </div>
        </div>

        <div class="setting-item mb-3">
          <div class="d-flex flex-column">
            <label style="color: var(--text-primary); font-size: 0.85rem; margin-bottom: 0.5rem;">Agent Type</label>
            <select id="agentTypeSelect-${accordionId}" class="form-select form-select-sm"
                    style="background: var(--bg-primary); border: 1px solid var(--border-color); color: var(--text-primary); font-size: 0.85rem;">
              <option value="tool-calling" ${agentType === 'tool-calling' ? 'selected' : ''}>Tool Calling</option>
              <option value="general" ${agentType === 'general' ? 'selected' : ''}>General</option>
              <option value="research" ${agentType === 'research' ? 'selected' : ''}>Research</option>
            </select>
          </div>
        </div>

        <div class="setting-item mb-3">
          <div class="d-flex align-items-center justify-content-between">
            <span style="color: var(--text-primary); font-size: 0.85rem;">Model</span>
            <select id="gptModelSelect-${accordionId}" class="form-select form-select-sm" style="width: auto; min-width: 180px; background: var(--bg-primary); border: 1px solid var(--border-color); color: var(--text-primary); font-size: 0.85rem;">
              <!-- Models will be loaded dynamically from /api/providers -->
              <option value="">Loading models...</option>
            </select>
          </div>
        </div>

        <div class="setting-item mb-3">
          <div class="d-flex flex-column">
            <div class="d-flex align-items-center justify-content-between mb-2">
              <span style="color: var(--text-primary); font-size: 0.85rem;">Temperature</span>
              <span id="temperatureValue-${accordionId}" style="color: var(--text-secondary); font-size: 0.85em;">0.0</span>
            </div>
            <input type="range" id="temperatureSlider-${accordionId}" class="form-range" min="0" max="2" step="0.1" value="0" style="accent-color: var(--accent-color);">
          </div>
        </div>

        <div class="setting-item mb-3">
          <div class="d-flex flex-column">
            <label style="color: var(--text-primary); font-size: 0.85rem; margin-bottom: 0.5rem;">System Prompt</label>
            <textarea id="systemPromptInput-${accordionId}" class="form-control" rows="3"
                      style="background: var(--bg-primary); border: 1px solid var(--border-color); color: var(--text-primary); font-size: 0.8em; resize: vertical;"
                      placeholder="You are a helpful assistant..."></textarea>
          </div>
        </div>

        <div class="setting-item mb-3">
          <div class="d-flex flex-column">
            <h6 class="fw-semibold mb-2" style="color: var(--text-primary); font-size: 0.85rem;">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                <path d="M20.5,11H19V7C19,5.89 18.1,5 17,5H13V3.5A2.5,2.5 0 0,0 10.5,1A2.5,2.5 0 0,0 8,3.5V5H4C2.89,5 2,5.89 2,7V10.8H3.5C5,10.8 6.2,12 6.2,13.5C6.2,15 5,16.2 3.5,16.2H2V20C2,21.11 2.89,22 4,22H7.8V20.5C7.8,19 9,17.8 10.5,17.8C12,17.8 13.2,19 13.2,20.5V22H17C18.11,22 19,21.11 19,20V16H20.5A2.5,2.5 0 0,0 23,13.5A2.5,2.5 0 0,0 20.5,11Z"/>
              </svg>
              Plugins
            </h6>
            <div id="pluginsList-${accordionId}" style="font-size: 0.85rem;">
              <div class="text-muted small">Loading plugins...</div>
            </div>
          </div>
        </div>

        <div class="setting-item mb-3">
          <div class="d-flex flex-column">
            <h6 class="fw-semibold mb-2" style="color: var(--text-primary); font-size: 0.85rem;">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                <path d="M20,2H4A2,2 0 0,0 2,4V22L6,18H20A2,2 0 0,0 22,16V4A2,2 0 0,0 20,2M6,9H18V11H6M14,14H6V12H14M18,8H6V6H18"/>
              </svg>
              MCP Connectors
            </h6>
            <div id="mcpServersList-${accordionId}" style="font-size: 0.85rem;">
              <div class="text-muted small">Loading MCP connector summary...</div>
            </div>
          </div>
        </div>

        <button id="updateSettingsBtn-${accordionId}" class="modern-btn modern-btn-primary w-100" onclick="updateAgentSettings('${agentName}', '${accordionId}')">
          Update Settings
        </button>
      </div>
    </div>
  `;

  return agentDiv;
}

// Start new chat with agent - shows workspace picker
async function newChatWithAgent(agentName) {
  try {
    // Fetch available workspaces
    const response = await API.get('/api/folders');
    const folders = response.folders || [];

    // Show workspace picker modal
    showWorkspacePickerForAgent(agentName, folders);
  } catch (error) {
    agentsLog.error('Error fetching workspaces', error);
    // Fallback: create session without workspace
    if (window.sessionManager?.createSessionWithAgent) {
      await window.sessionManager.createSessionWithAgent(agentName);
    }
  }
}

// Show workspace picker modal for new chat
function showWorkspacePickerForAgent(agentName, folders) {
  // Remove existing modal if any
  const existingModal = document.getElementById('workspacePickerModal');
  if (existingModal) existingModal.remove();

  const modal = document.createElement('div');
  modal.id = 'workspacePickerModal';
  modal.className = 'modal fade show';
  modal.style.display = 'block';
  modal.style.backgroundColor = 'rgba(0,0,0,0.5)';
  modal.style.zIndex = '10600';

  const escapeHtml = str => {
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
  };

  modal.innerHTML = `
    <div class="modal-dialog modal-dialog-centered modal-sm">
      <div class="modal-content" style="background: var(--bg-primary); border: 1px solid var(--border-color);">
        <div class="modal-header" style="border-bottom: 1px solid var(--border-color);">
          <h6 class="modal-title" style="color: var(--text-primary);">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-2" style="vertical-align: -2px;">
              <path d="M20,2H4A2,2 0 0,0 2,4V22L6,18H20A2,2 0 0,0 22,16V4A2,2 0 0,0 20,2Z"/>
            </svg>
            New Chat with ${escapeHtml(agentName)}
          </h6>
          <button type="button" class="btn-close" data-dismiss="modal" style="filter: var(--btn-close-filter);"></button>
        </div>
        <div class="modal-body">
          <p class="text-muted small mb-3">Choose where to save this conversation:</p>
          <div class="workspace-picker-list">
            <button class="workspace-picker-item modern-btn modern-btn-secondary w-100 mb-2 text-start" data-folder="">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-2" style="opacity: 0.6;">
                <path d="M20,18H4V8H20M20,6H12L10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6Z"/>
              </svg>
              <span>No workspace (root)</span>
            </button>
            ${folders
              .map(
                folder => `
              <button class="workspace-picker-item modern-btn modern-btn-secondary w-100 mb-2 text-start" data-folder="${folder.id}">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-2" style="opacity: 0.6;">
                  <path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>
                </svg>
                <span>${escapeHtml(folder.name)}</span>
              </button>
            `
              )
              .join('')}
          </div>
        </div>
      </div>
    </div>
  `;

  document.body.appendChild(modal);

  // Handle workspace selection
  modal.querySelectorAll('.workspace-picker-item').forEach(btn => {
    btn.addEventListener('click', async () => {
      const folderId = btn.dataset.folder;
      modal.remove();

      try {
        if (folderId && window.sessionManager?.createSessionWithAgentInFolder) {
          await window.sessionManager.createSessionWithAgentInFolder(agentName, folderId);
        } else if (window.sessionManager?.createSessionWithAgent) {
          await window.sessionManager.createSessionWithAgent(agentName);
        }

        if (typeof showNotification === 'function') {
          showNotification(`Started new chat with ${agentName}`, 'success');
        }

        // Emit event for other modules
        EventBus.emit('agent:newChat', { name: agentName, folderId });

        // Refresh agent list to show new current agent
        await loadAgentsForSidebar();
      } catch (error) {
        agentsLog.error('Error creating session', error);
        if (typeof showNotification === 'function') {
          showNotification(`Failed to create chat: ${error.message}`, 'error');
        }
      }
    });
  });

  // Handle close
  modal.querySelector('.btn-close').addEventListener('click', () => modal.remove());
  modal.addEventListener('click', e => {
    if (e.target === modal) modal.remove();
  });

  // Handle escape key
  const handleEscape = e => {
    if (e.key === 'Escape') {
      modal.remove();
      document.removeEventListener('keydown', handleEscape);
    }
  };
  document.addEventListener('keydown', handleEscape);
}

// Delete agent
async function deleteAgent(agentName) {
  if (!confirm(`Are you sure you want to delete agent "${agentName}"?`)) {
    return;
  }

  try {
    await API.delete(`/api/agents?name=${encodeURIComponent(agentName)}`);

    // Emit event for other modules
    EventBus.emit('agent:deleted', { name: agentName });

    // Refresh the agent list
    await loadAgentsForSidebar();

    agentsLog.info('Deleted agent', { agent: agentName });
    if (window.Toast) {
      Toast.success(`Agent "${agentName}" deleted`);
    }
  } catch (error) {
    agentsLog.error('Error deleting agent', error);
    if (window.Toast) {
      Toast.error(`Failed to delete agent: ${error.message}`);
    }
  }
}

// Refresh agent list
async function refreshAgentList() {
  await loadAgentsForSidebar();
}

// Setup agent management event listeners
function setupAgentManagement() {
  // Agent management buttons
  const addAgentBtn = document.getElementById('addAgentBtn');
  if (addAgentBtn) {
    addAgentBtn.addEventListener('click', () => {
      agentsLog.debug('Add agent clicked');
      showAddAgentModal();
    });
  }

  // Create agent button in modal
  const createAgentBtn = document.getElementById('createAgentBtn');
  if (createAgentBtn) {
    createAgentBtn.addEventListener('click', () => {
      createNewAgent();
    });
  }

  // Handle form submission with Enter key
  const addAgentForm = document.getElementById('addAgentForm');
  if (addAgentForm) {
    addAgentForm.addEventListener('submit', e => {
      e.preventDefault();
      createNewAgent();
    });
  }

  const addAgentModal = document.getElementById('addAgentModal');
  if (addAgentModal) {
    addAgentModal.addEventListener('hidden.bs.modal', () => {
      clearPendingAgentCreationFlow();
    });
  }

  const loadMoreAgentsBtn = document.getElementById('loadMoreAgentsBtn');
  if (loadMoreAgentsBtn) {
    loadMoreAgentsBtn.addEventListener('click', () => {
      agentsLog.debug('Load more agents clicked');
      loadAgentsForSidebar(); // Reload all agents (for now, until pagination is implemented)
    });
  }

  // Temperature slider update
  const agentTemperatureInput = document.getElementById('agentTemperature');
  const temperatureValueSpan = document.getElementById('temperatureValue');
  if (agentTemperatureInput && temperatureValueSpan) {
    agentTemperatureInput.addEventListener('input', e => {
      temperatureValueSpan.textContent = e.target.value;
    });
  }

  // Agent type selector update - filter models when type changes
  const agentTypeInput = document.getElementById('agentType');
  const agentModelInput = document.getElementById('agentModel');
  if (agentTypeInput && agentModelInput) {
    agentTypeInput.addEventListener('change', async e => {
      filterModelsByType(e.target.value, agentModelInput);
      setAgentModelRecommendationMessage('');
      updateAgentReasoningVisibility();
    });
  }

  if (agentModelInput) {
    agentModelInput.addEventListener('change', () => {
      updateAgentReasoningVisibility();
    });
  }

  // Auto-config mode toggle listeners
  setupAutoConfigListeners();

  agentsLog.debug('Agent management setup complete');
}

// State for auto-config
let baseAutoConfigApplied = false;
let baseLLMAvailable = false;
let baseSystemModelConfigured = false;

function isBaseAutoConfigFallback(config) {
  return Boolean(
    config &&
    typeof config.reasoning === 'string' &&
    config.reasoning.startsWith('Auto-config failed')
  );
}

// Setup auto-config event listeners
function setupAutoConfigListeners() {
  const configModeManual = document.getElementById('baseConfigModeManual');
  const configModeAuto = document.getElementById('baseConfigModeAuto');

  if (configModeManual) {
    configModeManual.addEventListener('change', function () {
      if (this.checked) handleBaseConfigModeChange('manual');
    });
  }

  if (configModeAuto) {
    configModeAuto.addEventListener('change', async function () {
      if (this.checked) {
        await checkBaseLLMAvailability();
        handleBaseConfigModeChange('auto');
      }
    });
  }

  // Generate auto-config button
  const generateBtn = document.getElementById('baseGenerateAutoConfigBtn');
  if (generateBtn) {
    generateBtn.addEventListener('click', generateBaseAutoConfig);
  }
}

// Check if any LLM provider is available
async function checkBaseLLMAvailability() {
  try {
    const data = await API.get('/api/agents/auto-config/availability');
    baseLLMAvailable = data.available;
    baseSystemModelConfigured = data.system_model_configured || false;
    return data;
  } catch (error) {
    agentsLog.error('Failed to check LLM availability', error);
    baseLLMAvailable = false;
    baseSystemModelConfigured = false;
    return {
      available: false,
      system_model_configured: false,
      message: 'Failed to check LLM availability'
    };
  }
}

// Handle config mode toggle
function handleBaseConfigModeChange(mode) {
  const autoConfigSection = document.getElementById('baseAutoConfigSection');
  const llmWarning = document.getElementById('baseLlmNotAvailableWarning');
  const llmWarningMessage = document.getElementById('baseLlmWarningMessage');
  const configModeHelp = document.getElementById('baseConfigModeHelp');
  const autoSelectedIndicator = document.getElementById('baseAutoSelectedIndicator');

  if (mode === 'auto') {
    if (configModeHelp) configModeHelp.classList.remove('d-none');

    if (baseLLMAvailable) {
      if (autoConfigSection) autoConfigSection.classList.remove('d-none');
      if (llmWarning) llmWarning.classList.add('d-none');
    } else {
      if (autoConfigSection) autoConfigSection.classList.add('d-none');
      if (llmWarning) llmWarning.classList.remove('d-none');
      // Update warning message based on what's missing
      if (llmWarningMessage) {
        if (!baseSystemModelConfigured) {
          llmWarningMessage.textContent = 'Auto-config requires a System Model to be configured.';
        } else {
          llmWarningMessage.textContent =
            'Auto-config requires an LLM provider. Please set up an API key or install Ollama.';
        }
      }
    }
  } else {
    // Manual mode
    if (autoConfigSection) autoConfigSection.classList.add('d-none');
    if (llmWarning) llmWarning.classList.add('d-none');
    if (configModeHelp) configModeHelp.classList.add('d-none');
    if (autoSelectedIndicator) autoSelectedIndicator.classList.add('d-none');
    baseAutoConfigApplied = false;
  }
}

// Generate auto-config from description
async function generateBaseAutoConfig() {
  const description = document.getElementById('baseAutoConfigDescription').value.trim();
  const generateBtn = document.getElementById('baseGenerateAutoConfigBtn');
  const autoConfigStatus = document.getElementById('baseAutoConfigStatus');

  if (!description) {
    if (window.Toast) {
      Toast.warning('Please describe what you want your agent to do', {
        title: 'Missing Description'
      });
    }
    return;
  }

  // Show loading state
  generateBtn.disabled = true;
  generateBtn.innerHTML = `
    <span class="spinner-border spinner-border-sm me-1" role="status"></span>
    Generating...
  `;
  if (autoConfigStatus) {
    autoConfigStatus.textContent = 'Analyzing...';
    autoConfigStatus.classList.remove('d-none', 'bg-success', 'bg-danger');
    autoConfigStatus.classList.add('bg-secondary');
  }

  try {
    const config = await API.post('/api/agents/auto-config', { description });

    // Apply the configuration to form fields
    applyBaseAutoConfig(config);

    // Show success status
    const fallback = isBaseAutoConfigFallback(config);
    if (autoConfigStatus) {
      autoConfigStatus.textContent = fallback ? 'Applied (defaults)' : 'Applied!';
      autoConfigStatus.classList.remove('bg-secondary', 'bg-success', 'bg-danger', 'bg-warning');
      autoConfigStatus.classList.add(fallback ? 'bg-warning' : 'bg-success');
    }

    // Show the auto-selected indicator
    const indicator = document.getElementById('baseAutoSelectedIndicator');
    if (indicator) indicator.classList.remove('d-none');
    baseAutoConfigApplied = true;

    if (config.reasoning) {
      agentsLog.debug('Auto-config reasoning', { reasoning: config.reasoning });
    }
    if (fallback && window.Toast) {
      Toast.warning('Auto-config failed, using defaults. Review the settings before saving.');
    }
  } catch (error) {
    agentsLog.error('Auto-config error', error);
    if (autoConfigStatus) {
      autoConfigStatus.textContent = 'Failed';
      autoConfigStatus.classList.remove('bg-secondary');
      autoConfigStatus.classList.add('bg-danger');
    }
    if (window.Toast) {
      Toast.error('Failed to generate configuration: ' + error.message);
    }
  } finally {
    // Restore button
    generateBtn.disabled = false;
    generateBtn.innerHTML = `
      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
        <path d="M12,3L2,12H5V20H19V12H22L12,3M12,8.75A2.25,2.25 0 0,1 14.25,11A2.25,2.25 0 0,1 12,13.25A2.25,2.25 0 0,1 9.75,11A2.25,2.25 0 0,1 12,8.75Z"/>
      </svg>
      Generate Config
    `;
  }
}

// Apply auto-generated config to form fields
function applyBaseAutoConfig(config) {
  // Apply agent type
  const typeSelect = document.getElementById('agentType');
  if (typeSelect && config.agent_type) {
    typeSelect.value = config.agent_type;
    // Trigger change to update model list
    typeSelect.dispatchEvent(new Event('change'));
  }

  // Apply model (need to wait a moment for model list to repopulate)
  setTimeout(() => {
    const modelSelect = document.getElementById('agentModel');
    if (modelSelect && config.model) {
      for (let i = 0; i < modelSelect.options.length; i++) {
        if (modelSelect.options[i].value === config.model) {
          modelSelect.selectedIndex = i;
          break;
        }
      }
      updateAgentReasoningVisibility();
    }
  }, 100);

  // Apply temperature
  const tempSlider = document.getElementById('agentTemperature');
  const tempValue = document.getElementById('temperatureValue');
  if (tempSlider && config.temperature !== undefined) {
    tempSlider.value = config.temperature;
    if (tempValue) {
      tempValue.textContent = config.temperature.toFixed(1);
    }
  }

  // Apply system prompt
  const promptTextarea = document.getElementById('agentSystemPrompt');
  if (promptTextarea && config.system_prompt) {
    promptTextarea.value = config.system_prompt;
  }

  // Add visual feedback - briefly highlight the changed fields
  highlightBaseAutoConfiguredFields();
}

// Briefly highlight fields that were auto-configured
function highlightBaseAutoConfiguredFields() {
  const fields = ['agentType', 'agentModel', 'agentTemperature', 'agentSystemPrompt'];

  fields.forEach(id => {
    const element = document.getElementById(id);
    if (element) {
      element.style.transition = 'box-shadow 0.3s ease';
      element.style.boxShadow = '0 0 0 2px var(--primary-color)';
      setTimeout(() => {
        element.style.boxShadow = '';
      }, 1500);
    }
  });
}

// Reset auto-config state (call after form submission)
function resetBaseAutoConfigState() {
  baseAutoConfigApplied = false;

  const configModeManual = document.getElementById('baseConfigModeManual');
  if (configModeManual) configModeManual.checked = true;

  const autoConfigSection = document.getElementById('baseAutoConfigSection');
  const llmWarning = document.getElementById('baseLlmNotAvailableWarning');
  const configModeHelp = document.getElementById('baseConfigModeHelp');
  const autoSelectedIndicator = document.getElementById('baseAutoSelectedIndicator');
  const autoConfigStatus = document.getElementById('baseAutoConfigStatus');
  const descriptionTextarea = document.getElementById('baseAutoConfigDescription');

  if (autoConfigSection) autoConfigSection.classList.add('d-none');
  if (llmWarning) llmWarning.classList.add('d-none');
  if (configModeHelp) configModeHelp.classList.add('d-none');
  if (autoSelectedIndicator) autoSelectedIndicator.classList.add('d-none');
  if (autoConfigStatus) autoConfigStatus.classList.add('d-none');
  if (descriptionTextarea) descriptionTextarea.value = '';
}

// Update agent settings from accordion
async function updateAgentSettings(agentName, accordionId) {
  try {
    const agentNameInput = document.getElementById(`agentNameInput-${accordionId}`);
    const agentTypeSelect = document.getElementById(`agentTypeSelect-${accordionId}`);
    const modelSelect = document.getElementById(`gptModelSelect-${accordionId}`);
    const temperatureSlider = document.getElementById(`temperatureSlider-${accordionId}`);
    const systemPromptInput = document.getElementById(`systemPromptInput-${accordionId}`);

    if (!modelSelect || !temperatureSlider) {
      agentsLog.error('Settings elements not found for agent', { agent: agentName });
      return;
    }

    const newAgentName = agentNameInput ? agentNameInput.value.trim() : agentName;
    const newAgentType = agentTypeSelect ? agentTypeSelect.value : 'tool-calling';

    // If agent name changed, we need to rename the agent first
    if (newAgentName !== agentName) {
      await API.put(`/api/agents/${encodeURIComponent(agentName)}`, {
        new_name: newAgentName,
        type: newAgentType
      });
    }

    const settingsData = {
      model: modelSelect.value,
      temperature: parseFloat(temperatureSlider.value),
      type: newAgentType
    };

    // Add system prompt if it exists
    if (systemPromptInput) {
      settingsData.system_prompt = systemPromptInput.value;
    }

    await API.post(`/api/settings?agent=${encodeURIComponent(newAgentName)}`, settingsData);

    agentsLog.info('Settings updated for agent', { agent: newAgentName, settings: settingsData });

    // Show success notification
    if (typeof showNotification === 'function') {
      showNotification(`Settings updated for ${newAgentName}!`, 'success');
    }

    // Emit event for other modules
    EventBus.emit('agent:settings:updated', { name: newAgentName, settings: settingsData });

    // If name changed, reload agents list to reflect the change
    if (newAgentName !== agentName) {
      await loadAgentsForSidebar();
    }
  } catch (error) {
    agentsLog.error('Error saving settings', error);
    if (typeof showNotification === 'function') {
      showNotification('Error saving settings', 'error');
    }
  }
}

// Setup accordion event listeners after agents are rendered
function setupAccordionListeners() {
  // Add temperature slider listeners for each agent
  document.querySelectorAll('[id^="temperatureSlider-"]').forEach(slider => {
    const accordionId = slider.id.replace('temperatureSlider-', '');
    const temperatureValue = document.getElementById(`temperatureValue-${accordionId}`);
    const modelSelect = document.getElementById(`gptModelSelect-${accordionId}`);

    if (temperatureValue) {
      slider.addEventListener('input', function (e) {
        // Enforce GPT-5 temperature restriction
        if (modelSelect && modelSelect.value.includes('gpt-5')) {
          e.target.value = 1.0;
          temperatureValue.textContent = '1.0';
          return;
        }
        temperatureValue.textContent = parseFloat(e.target.value).toFixed(1);
      });
    }
  });

  // Add model change listener for GPT-5 temperature restriction
  document.querySelectorAll('[id^="gptModelSelect-"]').forEach(modelSelect => {
    const accordionId = modelSelect.id.replace('gptModelSelect-', '');
    const temperatureSlider = document.getElementById(`temperatureSlider-${accordionId}`);
    const temperatureValue = document.getElementById(`temperatureValue-${accordionId}`);

    modelSelect.addEventListener('change', function () {
      if (this.value.includes('gpt-5')) {
        if (temperatureSlider) {
          temperatureSlider.value = 1.0;
          temperatureSlider.disabled = true;
        }
        if (temperatureValue) {
          temperatureValue.textContent = '1.0';
        }
      } else {
        if (temperatureSlider) {
          temperatureSlider.disabled = false;
        }
      }
    });
  });
}

// Load settings for a specific agent accordion
async function loadAgentSettings(agentName, agentType, accordionId) {
  try {
    // Ensure providers are loaded
    if (availableProviders.length === 0) {
      await loadAvailableProviders();
    }

    // Populate model dropdown from API, filtering by agent type
    const modelSelect = document.getElementById(`gptModelSelect-${accordionId}`);
    if (modelSelect) {
      // Clear existing options
      modelSelect.innerHTML = '';

      // Add models matching the agent's type from all providers
      availableProviders.forEach(provider => {
        const providerGroup = document.createElement('optgroup');
        providerGroup.label = provider.display_name;
        let hasMatchingModels = false;

        provider.models.forEach(model => {
          // Only add models matching the agent type
          if (model.type === agentType) {
            const option = document.createElement('option');
            option.value = model.value;
            option.textContent = model.label;
            option.setAttribute('data-type', model.type);
            option.setAttribute('data-provider', model.provider);
            providerGroup.appendChild(option);
            hasMatchingModels = true;
          }
        });

        // Only add the provider group if it has matching models
        if (hasMatchingModels) {
          modelSelect.appendChild(providerGroup);
        }
      });
    }

    const settings = await API.get(`/api/settings?agent=${encodeURIComponent(agentName)}`);

    // Update model dropdown with current value
    const modelValue = (settings.Settings && settings.Settings.model) || settings.model;
    if (modelSelect && modelValue) {
      modelSelect.value = modelValue;
    }

    // Update temperature slider
    const temperatureSlider = document.getElementById(`temperatureSlider-${accordionId}`);
    const temperatureValue = document.getElementById(`temperatureValue-${accordionId}`);
    let temperatureValueData =
      settings.Settings && typeof settings.Settings.temperature !== 'undefined'
        ? settings.Settings.temperature
        : settings.temperature;

    // Force temperature to 1.0 for GPT-5 models
    if (modelValue && modelValue.includes('gpt-5')) {
      temperatureValueData = 1.0;
      if (temperatureSlider) temperatureSlider.disabled = true;
    } else {
      if (temperatureSlider) temperatureSlider.disabled = false;
    }

    if (temperatureSlider && typeof temperatureValueData !== 'undefined') {
      temperatureSlider.value = temperatureValueData;
      if (temperatureValue) {
        temperatureValue.textContent = temperatureValueData.toFixed(1);
      }
    }

    // Update system prompt
    const systemPromptInput = document.getElementById(`systemPromptInput-${accordionId}`);
    const systemPromptValue =
      (settings.Settings && settings.Settings.system_prompt) || settings.system_prompt || '';
    if (systemPromptInput) {
      systemPromptInput.value = systemPromptValue;
    }

    // Load plugins for this agent
    await loadAgentPlugins(agentName, accordionId);

    // Load MCP servers for this agent
    await loadAgentMCPServers(agentName, accordionId);
  } catch (error) {
    agentsLog.error('Error loading settings for agent', { agent: agentName, error });
  }
}

// Load plugins for a specific agent
async function loadAgentPlugins(agentName, accordionId) {
  const pluginsList = document.getElementById(`pluginsList-${accordionId}`);
  if (!pluginsList) return;

  try {
    // Fetch all available plugins from registry
    const registry = await API.get('/api/plugin-registry');

    // Fetch currently loaded plugins for this agent
    const activePlugins = await API.get(`/api/plugins?agent=${encodeURIComponent(agentName)}`);

    // Create a set of active plugin names for quick lookup (only enabled ones)
    const activePluginNames = new Set(
      activePlugins.plugins
        .filter(p => p.enabled === true)
        .map(p =>
          p.name
            .toLowerCase()
            .replace(/_/g, '-')
            .replace(/-\d+\.\d+\.\d+(?:[-+][\w.]+)?$/, '')
            .trim()
        )
    );

    // Filter to only show installed plugins (those with a local path in uploaded_plugins)
    const localPlugins = registry.plugins
      .filter(plugin => plugin.path && plugin.path.includes('uploaded_plugins'))
      .map(plugin => ({
        ...plugin,
        displayName: plugin.metadata?.name || stripVersionSuffix(plugin.name || '')
      }));

    if (localPlugins.length === 0) {
      pluginsList.innerHTML = '<div class="text-muted small">No plugins installed</div>';
      return;
    }

    // Render plugins list
    let html = '';
    localPlugins.forEach(plugin => {
      const normalizedName = stripVersionSuffix(
        plugin.name.toLowerCase().replace(/_/g, '-')
      ).trim();
      const isActive = activePluginNames.has(normalizedName);

      html += `
        <div class="d-flex align-items-center justify-content-between mb-2 p-2"
             style="background: var(--bg-primary); border: 1px solid var(--border-color); border-radius: 6px;">
          <div class="d-flex flex-column flex-grow-1">
            <span style="color: var(--text-primary); font-weight: 500;">${escapeHtml(plugin.displayName)}</span>
            ${plugin.description ? `<span class="text-muted small">${escapeHtml(plugin.description)}</span>` : ''}
          </div>
          <div class="form-check form-switch mb-0">
            <input class="form-check-input" type="checkbox" role="switch"
                   id="plugin-${normalizedName}-${accordionId}"
                   ${isActive ? 'checked' : ''}
                   onchange="toggleAgentPlugin('${escapeJs(agentName)}', '${escapeJs(plugin.name)}', '${escapeJs(plugin.path)}', this.checked, '${accordionId}')"
                   style="cursor: pointer;">
          </div>
        </div>
      `;
    });

    pluginsList.innerHTML = html;
  } catch (error) {
    agentsLog.error('Error loading plugins for agent', { agent: agentName, error });
    pluginsList.innerHTML = '<div class="text-danger small">Failed to load plugins</div>';
  }
}

// Toggle plugin for an agent
async function toggleAgentPlugin(agentName, pluginName, pluginPath, enabled, accordionId) {
  const normalizedName = stripVersionSuffix(pluginName.toLowerCase().replace(/_/g, '-')).trim();
  const checkbox = document.getElementById(`plugin-${normalizedName}-${accordionId}`);
  const originalState = !enabled;

  try {
    if (enabled) {
      // Enable the plugin for this agent
      const formData = new FormData();
      formData.append('name', pluginName);
      formData.append('path', pluginPath);
      formData.append('agent', agentName);

      const response = await fetch(`/api/plugins?agent=${encodeURIComponent(agentName)}`, {
        method: 'POST',
        body: formData
      });

      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(errorText || 'Failed to enable plugin');
      }
    } else {
      // Disable the plugin for this agent
      await API.delete(
        `/api/plugins?name=${encodeURIComponent(pluginName)}&agent=${encodeURIComponent(agentName)}`
      );
    }

    // Show success notification
    if (typeof showNotification === 'function') {
      showNotification(`Plugin ${enabled ? 'enabled' : 'disabled'} for ${agentName}`, 'success');
    }

    // Reload plugins list to update state
    await loadAgentPlugins(agentName, accordionId);
  } catch (error) {
    agentsLog.error('Error toggling plugin', error);

    // Rollback checkbox state on error
    if (checkbox) {
      checkbox.checked = originalState;
    }

    // Show error notification
    if (typeof showNotification === 'function') {
      showNotification(
        `Failed to ${enabled ? 'enable' : 'disable'} plugin: ${error.message}`,
        'error'
      );
    }
  }
}

// Load MCP servers for a specific agent
async function loadAgentMCPServers(agentName, accordionId) {
  const mcpServersList = document.getElementById(`mcpServersList-${accordionId}`);
  if (!mcpServersList) return;

  mcpServersList.innerHTML = `
    <div class="small" style="color: var(--text-secondary); line-height: 1.5;">
      MCP is now configured per workspace instead of per agent.
      <div style="margin-top: 8px;">
        <a href="/" style="color: var(--primary-color); text-decoration: none;">Open Workspace Map</a>
        <span style="margin: 0 6px; color: var(--text-muted);">|</span>
        <a href="/mcp" style="color: var(--primary-color); text-decoration: none;">Manage Global MCP</a>
      </div>
    </div>
  `;
}

// Initialize agent management when DOM is ready
window.showAddAgentModal = showAddAgentModal;

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', setupAgentManagement);
} else {
  setupAgentManagement();
}

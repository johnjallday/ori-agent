// Agent Creation Form JavaScript

let availablePlugins = [];
let availableMCPServers = [];
let selectedTags = [];
let availableProviders = []; // Cache for available providers and models from API
const createValidatedFields = [
  'createAutoConfigDescription',
  'agentName',
  'agentType',
  'agentRole',
  'llmModel'
];

// Initialize page
document.addEventListener('DOMContentLoaded', () => {
  loadPlugins();
  loadMCPServers();
  setupTagsInput();
  loadAvailableProviders();
  setupAutoConfigListeners();
  setupValidationListeners();
  setupCreateFormSubmission();
  refreshSystemModelDisplay();
});

// Fetch and display system model in navbar
async function refreshSystemModelDisplay() {
  const modelNameEl = document.getElementById('systemModelName');
  const providerEl = document.getElementById('navSystemModelProvider');
  const indicatorEl = document.getElementById('systemModelIndicator');

  if (!modelNameEl || !providerEl) return;

  try {
    const response = await fetch('/api/settings/system-model');
    if (!response.ok) {
      throw new Error('Failed to fetch system model');
    }
    const data = await response.json();

    if (data.configured && data.model) {
      const modelName = data.model.length > 20 ? data.model.substring(0, 18) + '...' : data.model;
      modelNameEl.textContent = modelName;
      modelNameEl.title = data.model;

      if (data.provider) {
        providerEl.textContent = data.provider;
        providerEl.style.display = 'inline';

        switch (data.provider.toLowerCase()) {
          case 'openai':
            providerEl.style.background = 'rgba(16, 163, 127, 0.2)';
            providerEl.style.color = '#10a37f';
            break;
          case 'claude':
          case 'anthropic':
            providerEl.style.background = 'rgba(204, 147, 102, 0.2)';
            providerEl.style.color = '#cc9366';
            break;
          case 'gemini':
            providerEl.style.background = 'rgba(66, 133, 244, 0.2)';
            providerEl.style.color = '#4285f4';
            break;
          case 'ollama':
            providerEl.style.background = 'rgba(59, 130, 246, 0.2)';
            providerEl.style.color = '#3b82f6';
            break;
          default:
            providerEl.style.background = 'var(--bg-tertiary)';
            providerEl.style.color = 'var(--text-muted)';
        }
      } else {
        providerEl.style.display = 'none';
      }

      if (indicatorEl) {
        indicatorEl.title = `System Model: ${data.model} (${data.provider}) - Click to configure`;
      }
    } else {
      modelNameEl.textContent = 'Not configured';
      providerEl.style.display = 'none';
      if (indicatorEl) {
        indicatorEl.title = 'System Model not configured - Click to set up';
      }
    }
  } catch (error) {
    console.error('Failed to load system model:', error);
    modelNameEl.textContent = 'Error';
    providerEl.style.display = 'none';
  }
}

// Fetch available providers and models from API
async function loadAvailableProviders() {
  try {
    const response = await fetch('/api/providers');
    if (!response.ok) {
      throw new Error('Failed to load providers');
    }
    const data = await response.json();
    availableProviders = data.providers || [];

    // Populate the model select with data from API
    updateModelOptions();

    return availableProviders;
  } catch (error) {
    console.error('Failed to load providers:', error);
    // Fall back to showing an error in the model select
    const modelSelect = document.getElementById('llmModel');
    if (modelSelect) {
      modelSelect.innerHTML = '<option value="">Failed to load models</option>';
    }
    return [];
  }
}

// Load available plugins
async function loadPlugins() {
  try {
    const response = await fetch('/api/plugins');

    if (!response.ok) {
      throw new Error('Failed to load plugins');
    }

    const data = await response.json();
    availablePlugins = data.plugins || [];
    renderPlugins();

  } catch (error) {
    console.error('Error loading plugins:', error);
    showError('Failed to load plugins. Some features may be unavailable.');
  }
}

// Load available MCP servers
async function loadMCPServers() {
  try {
    const response = await fetch('/api/mcp/servers');

    if (!response.ok) {
      throw new Error('Failed to load MCP servers');
    }

    const data = await response.json();
    availableMCPServers = Array.isArray(data.servers) ? data.servers : [];
    renderMCPServers();
  } catch (error) {
    console.error('Error loading MCP servers:', error);
    const container = document.getElementById('mcpServersList');
    if (container) {
      container.innerHTML = '<div style="text-align: center; padding: 20px; color: var(--text-muted, #666);">Failed to load MCP servers</div>';
    }
  }
}

// Render plugins list
function renderPlugins() {
  const container = document.getElementById('pluginsList');

  if (availablePlugins.length === 0) {
    container.innerHTML = '<div style="text-align: center; padding: 20px; color: var(--text-muted, #666);">No plugins available</div>';
    return;
  }

  container.innerHTML = '';
  availablePlugins.forEach((plugin, index) => {
    const item = document.createElement('div');
    item.className = 'plugin-item';
    const displayName = plugin.metadata?.name || stripVersionSuffix(plugin.name || '');
    item.innerHTML = `
            <input type="checkbox" id="plugin-${index}" class="plugin-checkbox" value="${escapeHtml(plugin.name)}">
            <label for="plugin-${index}" class="plugin-info" style="cursor: pointer;">
                <div class="plugin-name">${escapeHtml(displayName)}</div>
                ${plugin.description ? `<div class="plugin-description">${escapeHtml(plugin.description)}</div>` : ''}
            </label>
        `;
    container.appendChild(item);
  });
}

// Render MCP servers list
function renderMCPServers() {
  const container = document.getElementById('mcpServersList');
  if (!container) return;

  if (availableMCPServers.length === 0) {
    container.innerHTML = `
      <div style="text-align: center; padding: 20px; color: var(--text-muted, #666);">
        No MCP servers configured yet.
        <div style="margin-top: 8px;">
          <a href="/mcp" style="color: var(--primary-color); text-decoration: none;">Open MCP settings</a>
        </div>
      </div>
    `;
    return;
  }

  container.innerHTML = '';
  availableMCPServers
    .slice()
    .sort((a, b) => String(a?.name || '').localeCompare(String(b?.name || ''), undefined, { sensitivity: 'base' }))
    .forEach((server, index) => {
      const serverName = String(server?.name || '').trim();
      if (!serverName) return;

      const status = String(server?.status || 'unknown').toLowerCase();
      const statusClass = status === 'running'
        ? 'ready'
        : (status === 'starting' || status === 'restarting' ? 'warming' : 'stopped');
      const args = Array.isArray(server?.args) && server.args.length > 0
        ? server.args.join(' ')
        : '';
      const commandSummary = [server?.command, args].filter(Boolean).join(' ');

      const item = document.createElement('div');
      item.className = 'plugin-item';
      item.innerHTML = `
            <input type="checkbox" id="mcp-${index}" class="plugin-checkbox mcp-checkbox" value="${escapeHtml(serverName)}">
            <label for="mcp-${index}" class="plugin-info" style="cursor: pointer;">
                <div class="plugin-name">${escapeHtml(serverName)}</div>
                <div class="plugin-description">Enable this MCP server for the new agent.</div>
                <div class="mcp-meta">
                  <span class="mcp-status-badge ${statusClass}">${escapeHtml(status)}</span>
                  ${commandSummary ? `<span class="mcp-command">${escapeHtml(commandSummary)}</span>` : ''}
                </div>
            </label>
        `;
      container.appendChild(item);
    });
}

// Setup tags input
function setupTagsInput() {
  const input = document.getElementById('tagsInput');
  if (!input) return;

  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && input.value.trim()) {
      e.preventDefault();
      addTag(input.value.trim());
      input.value = '';
    } else if (e.key === 'Backspace' && !input.value && selectedTags.length > 0) {
      removeTag(selectedTags[selectedTags.length - 1]);
    }
  });
}

// Add tag
function addTag(tag) {
  if (!selectedTags.includes(tag)) {
    selectedTags.push(tag);
    renderTags();
  }
}

// Remove tag
function removeTag(tag) {
  selectedTags = selectedTags.filter(t => t !== tag);
  renderTags();
}

// Render tags
function renderTags() {
  const container = document.getElementById('tagsContainer');
  const input = document.getElementById('tagsInput');
  if (!container || !input) return;

  // Clear existing tags
  const existingTags = container.querySelectorAll('.tag-item');
  existingTags.forEach(tag => tag.remove());

  // Add tags before input
  selectedTags.forEach(tag => {
    const tagEl = document.createElement('div');
    tagEl.className = 'tag-item';
    const textEl = document.createElement('span');
    textEl.textContent = tag;

    const removeBtn = document.createElement('button');
    removeBtn.type = 'button';
    removeBtn.className = 'tag-remove';
    removeBtn.setAttribute('aria-label', `Remove tag ${tag}`);
    removeBtn.textContent = '×';
    removeBtn.addEventListener('click', () => removeTag(tag));

    tagEl.appendChild(textEl);
    tagEl.appendChild(removeBtn);
    container.insertBefore(tagEl, input);
  });
}

function setupValidationListeners() {
  const inputFieldIds = ['createAutoConfigDescription', 'agentName'];
  const selectFieldIds = ['agentType', 'agentRole', 'llmModel'];

  inputFieldIds.forEach((fieldId) => {
    const field = document.getElementById(fieldId);
    if (!field) return;
    field.addEventListener('input', () => clearFieldError(fieldId));
  });

  selectFieldIds.forEach((fieldId) => {
    const field = document.getElementById(fieldId);
    if (!field) return;
    field.addEventListener('change', () => clearFieldError(fieldId));
  });
}

function setupCreateFormSubmission() {
  const form = document.getElementById('createAgentForm');
  if (!form) return;

  form.addEventListener('submit', (event) => {
    event.preventDefault();
    createAgent();
  });
}

// Update model options based on provider and agent type
function updateModelOptions() {
  const providerSelect = document.getElementById('llmProvider');
  const modelSelect = document.getElementById('llmModel');
  const agentTypeSelect = document.getElementById('agentType');

  if (!modelSelect || availableProviders.length === 0) {
    return;
  }

  const selectedProvider = providerSelect ? providerSelect.value : null;
  const selectedAgentType = agentTypeSelect ? agentTypeSelect.value : 'tool-calling';

  // Clear existing options
  modelSelect.innerHTML = '';

  // Find models from the API data
  availableProviders.forEach(provider => {
    // Map provider display names to our provider select values
    const providerNameMap = {
      'OpenAI': 'openai',
      'OpenAI Codex (CLI)': 'codex',
      'Anthropic': 'claude',
      'Anthropic Claude': 'claude',
      'Claude Code (CLI)': 'claude_code',
      'Ollama': 'ollama',
      'Google Gemini': 'gemini'
    };
    const providerKey = providerNameMap[provider.display_name] || provider.name;

    // If a specific provider is selected, only show models from that provider
    if (selectedProvider && providerKey !== selectedProvider) {
      return;
    }

    // Create optgroup for this provider
    const providerGroup = document.createElement('optgroup');
    providerGroup.label = provider.display_name;
    let hasMatchingModels = false;

    provider.models.forEach(model => {
      // Filter by agent type if the model has a type specified
      if (model.type && model.type !== selectedAgentType) {
        return;
      }

      const option = document.createElement('option');
      option.value = model.value;
      option.textContent = model.label;
      option.setAttribute('data-provider', providerKey);
      if (model.type) {
        option.setAttribute('data-type', model.type);
      }
      providerGroup.appendChild(option);
      hasMatchingModels = true;
    });

    // Only add the provider group if it has matching models
    if (hasMatchingModels) {
      modelSelect.appendChild(providerGroup);
    }
  });

  // If no models found, show a message
  if (modelSelect.options.length === 0) {
    const option = document.createElement('option');
    option.value = '';
    option.textContent = 'No models available for this configuration';
    modelSelect.appendChild(option);
  }
}

function clearGlobalError() {
  const errorEl = document.getElementById('errorMessage');
  if (!errorEl) return;

  errorEl.textContent = '';
  errorEl.hidden = true;
}

function showError(message) {
  const errorEl = document.getElementById('errorMessage');
  if (!errorEl) return;

  errorEl.textContent = message;
  errorEl.hidden = false;
  errorEl.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  errorEl.focus();
}

function setFieldError(fieldId, message) {
  const field = document.getElementById(fieldId);
  const errorEl = document.getElementById(`${fieldId}Error`);
  const hasError = Boolean(message);

  if (field) {
    field.classList.toggle('is-invalid', hasError);
    field.setAttribute('aria-invalid', hasError ? 'true' : 'false');
  }

  if (errorEl) {
    errorEl.textContent = message || '';
    errorEl.classList.toggle('is-visible', hasError);
  }
}

function clearFieldError(fieldId) {
  setFieldError(fieldId, '');
}

function clearValidationErrors() {
  createValidatedFields.forEach(clearFieldError);
}

function focusField(fieldId) {
  const field = document.getElementById(fieldId);
  if (!field) return;

  field.focus();
  field.scrollIntoView({ behavior: 'smooth', block: 'center' });
}

function validateCreateAgentForm() {
  clearGlobalError();
  clearValidationErrors();

  const validations = [
    {
      id: 'agentName',
      invalid: !document.getElementById('agentName')?.value.trim(),
      message: 'Enter an agent name.'
    },
    {
      id: 'agentType',
      invalid: !document.getElementById('agentType')?.value,
      message: 'Choose an agent type.'
    },
    {
      id: 'agentRole',
      invalid: !document.getElementById('agentRole')?.value,
      message: 'Choose a role.'
    },
    {
      id: 'llmModel',
      invalid: !document.getElementById('llmModel')?.value,
      message: 'Choose a model before creating the agent.'
    }
  ];

  let firstInvalidField = null;

  validations.forEach(({ id, invalid, message }) => {
    if (!invalid) return;
    setFieldError(id, message);
    if (!firstInvalidField) {
      firstInvalidField = id;
    }
  });

  if (firstInvalidField) {
    focusField(firstInvalidField);
    return false;
  }

  return true;
}

// Create agent
async function createAgent() {
  if (!validateCreateAgentForm()) {
    return;
  }

  // Validate required fields
  const name = document.getElementById('agentName').value.trim();
  const type = document.getElementById('agentType').value;
  const role = document.getElementById('agentRole').value;
  const modelSelect = document.getElementById('llmModel');
  const model = modelSelect.value;
  clearGlobalError();

  // Get provider from the selected model's data attribute
  const selectedOption = modelSelect.options[modelSelect.selectedIndex];
  const provider = selectedOption ? selectedOption.getAttribute('data-provider') : null;

  // Gather optional fields
  const description = document.getElementById('agentDescription').value.trim();
  const temperature = parseFloat(document.getElementById('temperature').value);
  const systemPrompt = document.getElementById('systemPrompt').value.trim();
  const avatarColor = document.getElementById('avatarColor').value;
  const allowWebSearchInput = document.getElementById('allowWebSearch');
  const allowWebSearch = allowWebSearchInput ? Boolean(allowWebSearchInput.checked) : true;

  // Get selected plugins
  const pluginCheckboxes = document.querySelectorAll('#pluginsList .plugin-checkbox:checked');
  const enabledPlugins = Array.from(pluginCheckboxes).map(cb => cb.value);
  const mcpCheckboxes = document.querySelectorAll('#mcpServersList .mcp-checkbox:checked');
  const enabledMCPServers = Array.from(mcpCheckboxes).map(cb => cb.value);

  // Build request
  const requestData = {
    name: name,
    type: type,
    role: role,
    model: model,
    temperature: temperature,
    allow_web_search: allowWebSearch
  };

  // Add provider if we could determine it
  if (provider) {
    requestData.llm_provider = provider;
  }

  // Add optional fields
  if (description) requestData.description = description;
  if (systemPrompt) requestData.system_prompt = systemPrompt;
  if (avatarColor) requestData.avatar_color = avatarColor;
  if (selectedTags.length > 0) requestData.tags = selectedTags;
  if (enabledPlugins.length > 0) requestData.enabled_plugins = enabledPlugins;
  if (enabledMCPServers.length > 0) requestData.mcp_servers = enabledMCPServers;

  // Show loading
  showLoading(true);
  document.getElementById('createBtn').disabled = true;

  try {
    const response = await fetch('/api/agents', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(requestData)
    });

    if (!response.ok) {
      const error = await response.json();
      throw new Error(error.error || 'Failed to create agent');
    }

    const mcpResult = await enableMCPServersForAgent(name, enabledMCPServers);
    if (mcpResult.failures.length > 0) {
      console.warn('Some MCP servers failed to enable for new agent:', mcpResult.failures);
      if (window.Toast && typeof Toast.warning === 'function') {
        Toast.warning(`Agent created, but ${mcpResult.failures.length} MCP server(s) could not be enabled.`);
      }
      // Redirect to detail page so user can immediately resolve MCP setup issues.
      window.location.href = `/agents/${encodeURIComponent(name)}`;
      return;
    }

    // Success - redirect to agents page
    window.location.href = '/agents';

  } catch (error) {
    console.error('Error creating agent:', error);
    showError(error.message || 'Failed to create agent');
    showLoading(false);
    document.getElementById('createBtn').disabled = false;
  }
}

async function enableMCPServersForAgent(agentName, serverNames) {
  const targets = Array.from(new Set((serverNames || [])
    .map((name) => String(name || '').trim())
    .filter(Boolean)));
  if (targets.length === 0) {
    return { successCount: 0, failures: [] };
  }

  let successCount = 0;
  const failures = [];

  await Promise.all(targets.map(async (serverName) => {
    try {
      const endpoint = `/api/agents/${encodeURIComponent(agentName)}/mcp-servers/${encodeURIComponent(serverName)}/enable`;
      const response = await fetch(endpoint, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        }
      });

      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(errorText || 'Enable failed');
      }

      successCount += 1;
    } catch (error) {
      failures.push(`${serverName}: ${error.message || 'unknown error'}`);
    }
  }));

  return { successCount, failures };
}

function showLoading(show) {
  document.getElementById('loadingOverlay').style.display = show ? 'flex' : 'none';
}

// escapeHtml is provided by dom-utils.js

// ============================================
// Auto-Config Functions
// ============================================

let createLLMAvailable = false;
let createSystemModelConfigured = false;
let createAutoConfigApplied = false;

function isCreateAutoConfigFallback(config) {
  return Boolean(config && typeof config.reasoning === 'string' && config.reasoning.startsWith('Auto-config failed'));
}

// Setup auto-config event listeners
function setupAutoConfigListeners() {
  const configModeManual = document.getElementById('createConfigModeManual');
  const configModeAuto = document.getElementById('createConfigModeAuto');

  if (configModeManual) {
    configModeManual.addEventListener('change', function() {
      if (this.checked) handleCreateConfigModeChange('manual');
    });
  }

  if (configModeAuto) {
    configModeAuto.addEventListener('change', async function() {
      if (this.checked) {
        await checkCreateLLMAvailability();
        handleCreateConfigModeChange('auto');
      }
    });
  }

  const generateBtn = document.getElementById('createGenerateAutoConfigBtn');
  if (generateBtn) {
    generateBtn.addEventListener('click', generateCreateAutoConfig);
  }
}

// Check if any LLM provider is available
async function checkCreateLLMAvailability() {
  try {
    const response = await fetch('/api/agents/auto-config/availability');
    const data = await response.json();
    createLLMAvailable = data.available;
    createSystemModelConfigured = data.system_model_configured || false;
    return data;
  } catch (error) {
    console.error('Failed to check LLM availability:', error);
    createLLMAvailable = false;
    createSystemModelConfigured = false;
    return { available: false, system_model_configured: false };
  }
}

// Handle config mode toggle
function handleCreateConfigModeChange(mode) {
  const autoConfigSection = document.getElementById('createAutoConfigSection');
  const llmWarning = document.getElementById('createLlmNotAvailableWarning');
  const llmWarningMessage = document.getElementById('createLlmWarningMessage');
  const autoSelectedIndicator = document.getElementById('createAutoSelectedIndicator');
  const autoConfigStatus = document.getElementById('createAutoConfigStatus');
  const autoConfigDescription = document.getElementById('createAutoConfigDescription');

  if (mode === 'auto') {
    if (createLLMAvailable) {
      if (autoConfigSection) autoConfigSection.classList.remove('d-none');
      if (llmWarning) llmWarning.classList.add('d-none');
      if (autoConfigDescription) autoConfigDescription.focus();
    } else {
      if (autoConfigSection) autoConfigSection.classList.add('d-none');
      if (llmWarning) llmWarning.classList.remove('d-none');
      // Update warning message based on what's missing
      if (llmWarningMessage) {
        if (!createSystemModelConfigured) {
          llmWarningMessage.textContent = 'Auto-config requires a System Model to be configured.';
        } else {
          llmWarningMessage.textContent = 'Auto-config requires an LLM provider. Please set up an API key or install Ollama.';
        }
      }
    }
  } else {
    // Manual mode
    if (autoConfigSection) autoConfigSection.classList.add('d-none');
    if (llmWarning) llmWarning.classList.add('d-none');
    if (autoSelectedIndicator) autoSelectedIndicator.classList.add('d-none');
    createAutoConfigApplied = false;
  }

  if (autoConfigStatus) {
    autoConfigStatus.textContent = '';
    autoConfigStatus.classList.add('d-none');
    autoConfigStatus.classList.remove('bg-success', 'bg-danger', 'bg-warning');
    autoConfigStatus.classList.add('bg-secondary');
  }

  clearFieldError('createAutoConfigDescription');
}

// Generate auto-config
async function generateCreateAutoConfig() {
  const autoConfigDescription = document.getElementById('createAutoConfigDescription');
  const description = autoConfigDescription ? autoConfigDescription.value.trim() : '';
  const generateBtn = document.getElementById('createGenerateAutoConfigBtn');
  const autoConfigStatus = document.getElementById('createAutoConfigStatus');
  const indicator = document.getElementById('createAutoSelectedIndicator');
  const indicatorTitle = document.getElementById('createAutoSelectedTitle');
  const indicatorMessage = document.getElementById('createAutoSelectedMessage');

  clearGlobalError();
  clearFieldError('createAutoConfigDescription');

  if (!description) {
    setFieldError('createAutoConfigDescription', 'Describe the agent before generating settings.');
    focusField('createAutoConfigDescription');
    return;
  }

  generateBtn.disabled = true;
  generateBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1" aria-hidden="true"></span>Generating…';
  if (autoConfigStatus) {
    autoConfigStatus.textContent = 'Analyzing…';
    autoConfigStatus.classList.remove('d-none', 'bg-success', 'bg-danger', 'bg-warning');
    autoConfigStatus.classList.add('bg-secondary');
  }

  try {
    const response = await fetch('/api/agents/auto-config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ description })
    });

    if (!response.ok) {
      const error = await response.text();
      throw new Error(error || 'Failed to generate configuration');
    }

    const config = await response.json();

    // Apply config
    applyCreateAutoConfig(config);

    const fallback = isCreateAutoConfigFallback(config);
    if (autoConfigStatus) {
      autoConfigStatus.textContent = fallback ? 'Review Defaults' : '';
      autoConfigStatus.classList.remove('bg-secondary', 'bg-success', 'bg-danger', 'bg-warning');
      if (fallback) {
        autoConfigStatus.classList.add('bg-warning');
        autoConfigStatus.classList.remove('d-none');
      } else {
        autoConfigStatus.classList.add('d-none');
      }
    }

    if (indicator) {
      indicator.classList.remove('d-none', 'alert-info', 'alert-warning');
      indicator.classList.add(fallback ? 'alert-warning' : 'alert-info');
    }
    if (indicatorTitle) {
      indicatorTitle.textContent = fallback ? 'Defaults Applied:' : 'Auto-configured!';
    }
    if (indicatorMessage) {
      indicatorMessage.textContent = fallback
        ? 'Default settings were applied because auto-config could not complete. Review them before creating the agent.'
        : 'Settings below were generated from your description. You can still adjust them manually.';
    }
    createAutoConfigApplied = true;

    if (fallback && window.Toast) {
      Toast.warning('Auto-config failed, using defaults. Review the settings before saving.');
    }

  } catch (error) {
    console.error('Auto-config error:', error);
    if (autoConfigStatus) {
      autoConfigStatus.textContent = 'Failed';
      autoConfigStatus.classList.remove('bg-secondary', 'bg-warning');
      autoConfigStatus.classList.add('bg-danger');
    }
    showError('Failed to generate configuration: ' + error.message);
  } finally {
    generateBtn.disabled = false;
    generateBtn.innerHTML = '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1" aria-hidden="true" focusable="false"><path d="M12,3L2,12H5V20H19V12H22L12,3M12,8.75A2.25,2.25 0 0,1 14.25,11A2.25,2.25 0 0,1 12,13.25A2.25,2.25 0 0,1 9.75,11A2.25,2.25 0 0,1 12,8.75Z"/></svg>Generate Configuration';
  }
}

// Apply auto-generated config to form fields
function applyCreateAutoConfig(config) {
  clearFieldError('createAutoConfigDescription');

  // Apply agent name
  const nameField = document.getElementById('agentName');
  if (nameField && config.agent_name) {
    nameField.value = config.agent_name;
    clearFieldError('agentName');
  }

  // Apply agent type
  const typeSelect = document.getElementById('agentType');
  if (typeSelect && config.agent_type) {
    typeSelect.value = config.agent_type;
    clearFieldError('agentType');
    // Trigger change to update model list
    updateModelOptions();
  }

  // Apply description to the agent description field
  const descriptionField = document.getElementById('agentDescription');
  const autoDescription = document.getElementById('createAutoConfigDescription');
  if (descriptionField && autoDescription && autoDescription.value.trim()) {
    descriptionField.value = autoDescription.value.trim();
  }

  // Apply model (need to wait a moment for model list to repopulate)
  setTimeout(() => {
    const modelSelect = document.getElementById('llmModel');
    if (modelSelect && config.model) {
      for (let i = 0; i < modelSelect.options.length; i++) {
        if (modelSelect.options[i].value === config.model) {
          modelSelect.selectedIndex = i;
          clearFieldError('llmModel');
          break;
        }
      }
    }
  }, 100);

  // Apply temperature
  const tempSlider = document.getElementById('temperature');
  const tempValue = document.getElementById('tempValue');
  if (tempSlider && config.temperature !== undefined) {
    tempSlider.value = config.temperature;
    if (tempValue) tempValue.textContent = config.temperature.toFixed(1);
  }

  // Apply system prompt
  const promptTextarea = document.getElementById('systemPrompt');
  if (promptTextarea && config.system_prompt) {
    promptTextarea.value = config.system_prompt;
  }

  // Apply recommended plugins
  if (config.recommended_plugins && config.recommended_plugins.length > 0) {
    applyRecommendedPlugins(config.recommended_plugins);
  }

  // Highlight fields that were auto-configured
  highlightCreateAutoConfiguredFields();
}

// Apply recommended plugins by checking matching plugin checkboxes
function applyRecommendedPlugins(recommendedPlugins) {
  const checkboxes = document.querySelectorAll('.plugin-checkbox');

  checkboxes.forEach(checkbox => {
    const pluginName = checkbox.value.toLowerCase();

    // Check if any recommended plugin keyword matches this plugin name
    const shouldCheck = recommendedPlugins.some(keyword => {
      const lowerKeyword = keyword.toLowerCase();
      // Match if plugin name contains the keyword or keyword contains plugin name
      return pluginName.includes(lowerKeyword) || lowerKeyword.includes(pluginName);
    });

    if (shouldCheck) {
      checkbox.checked = true;
    }
  });
}

// Briefly highlight fields that were auto-configured
function highlightCreateAutoConfiguredFields() {
  const fields = ['agentName', 'agentType', 'llmModel', 'temperature', 'systemPrompt', 'pluginsList'];

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

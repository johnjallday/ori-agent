// Agent Creation Form JavaScript

let availablePlugins = [];
let selectedTags = [];
let availableProviders = []; // Cache for available providers and models from API

// Initialize page
document.addEventListener('DOMContentLoaded', () => {
  loadPlugins();
  setupTagsInput();
  loadAvailableProviders();
  setupAutoConfigListeners();
});

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
    item.innerHTML = `
            <input type="checkbox" id="plugin-${index}" class="plugin-checkbox" value="${escapeHtml(plugin.name)}">
            <label for="plugin-${index}" class="plugin-info" style="cursor: pointer;">
                <div class="plugin-name">${escapeHtml(plugin.name)}</div>
                ${plugin.description ? `<div class="plugin-description">${escapeHtml(plugin.description)}</div>` : ''}
            </label>
        `;
    container.appendChild(item);
  });
}

// Setup tags input
function setupTagsInput() {
  const input = document.getElementById('tagsInput');
  const container = document.getElementById('tagsContainer');

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

  // Clear existing tags
  const existingTags = container.querySelectorAll('.tag-item');
  existingTags.forEach(tag => tag.remove());

  // Add tags before input
  selectedTags.forEach(tag => {
    const tagEl = document.createElement('div');
    tagEl.className = 'tag-item';
    tagEl.innerHTML = `
            ${escapeHtml(tag)}
            <span class="tag-remove" onclick="removeTag('${escapeHtml(tag)}')">×</span>
        `;
    container.insertBefore(tagEl, input);
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
      'Anthropic': 'claude',
      'Ollama': 'ollama'
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

// Create agent
async function createAgent() {
  // Validate required fields
  const name = document.getElementById('agentName').value.trim();
  if (!name) {
    showError('Agent name is required');
    return;
  }

  const type = document.getElementById('agentType').value;
  const role = document.getElementById('agentRole').value;
  const modelSelect = document.getElementById('llmModel');
  const model = modelSelect.value;

  if (!type || !role || !model) {
    showError('Please fill in all required fields');
    return;
  }

  // Get provider from the selected model's data attribute
  const selectedOption = modelSelect.options[modelSelect.selectedIndex];
  const provider = selectedOption ? selectedOption.getAttribute('data-provider') : null;

  // Gather optional fields
  const description = document.getElementById('agentDescription').value.trim();
  const temperature = parseFloat(document.getElementById('temperature').value);
  const systemPrompt = document.getElementById('systemPrompt').value.trim();
  const avatarColor = document.getElementById('avatarColor').value;

  // Get selected plugins
  const pluginCheckboxes = document.querySelectorAll('.plugin-checkbox:checked');
  const enabledPlugins = Array.from(pluginCheckboxes).map(cb => cb.value);

  // Build request
  const requestData = {
    name: name,
    type: type,
    role: role,
    model: model,
    temperature: temperature
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

    // Success - redirect to agents page
    window.location.href = '/agents';

  } catch (error) {
    console.error('Error creating agent:', error);
    showError(error.message || 'Failed to create agent');
    showLoading(false);
    document.getElementById('createBtn').disabled = false;
  }
}

// Helper functions
function showError(message) {
  const errorEl = document.getElementById('errorMessage');
  errorEl.textContent = message;
  errorEl.style.display = 'block';

  // Scroll to error
  errorEl.scrollIntoView({ behavior: 'smooth', block: 'nearest' });

  // Auto-hide after 5 seconds
  setTimeout(() => {
    errorEl.style.display = 'none';
  }, 5000);
}

function showLoading(show) {
  document.getElementById('loadingOverlay').style.display = show ? 'flex' : 'none';
}

function escapeHtml(text) {
  const div = document.createElement('div');
  div.textContent = text;
  return div.innerHTML;
}

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

  if (mode === 'auto') {
    if (createLLMAvailable) {
      if (autoConfigSection) autoConfigSection.classList.remove('d-none');
      if (llmWarning) llmWarning.classList.add('d-none');
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
}

// Generate auto-config
async function generateCreateAutoConfig() {
  const description = document.getElementById('createAutoConfigDescription').value.trim();
  const generateBtn = document.getElementById('createGenerateAutoConfigBtn');
  const autoConfigStatus = document.getElementById('createAutoConfigStatus');

  if (!description) {
    showError('Please enter a description of what you want your agent to do.');
    return;
  }

  generateBtn.disabled = true;
  generateBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1" role="status"></span>Generating...';
  if (autoConfigStatus) {
    autoConfigStatus.textContent = 'Analyzing...';
    autoConfigStatus.classList.remove('d-none', 'bg-success', 'bg-danger');
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
      autoConfigStatus.textContent = fallback ? 'Applied (defaults)' : 'Applied!';
      autoConfigStatus.classList.remove('bg-secondary', 'bg-success', 'bg-danger', 'bg-warning');
      autoConfigStatus.classList.add(fallback ? 'bg-warning' : 'bg-success');
    }

    const indicator = document.getElementById('createAutoSelectedIndicator');
    if (indicator) indicator.classList.remove('d-none');
    createAutoConfigApplied = true;

    if (config.reasoning) console.log('Auto-config reasoning:', config.reasoning);
    if (fallback && window.Toast) {
      Toast.warning('Auto-config failed, using defaults. Review the settings before saving.');
    }

  } catch (error) {
    console.error('Auto-config error:', error);
    if (autoConfigStatus) {
      autoConfigStatus.textContent = 'Failed';
      autoConfigStatus.classList.remove('bg-secondary');
      autoConfigStatus.classList.add('bg-danger');
    }
    showError('Failed to generate configuration: ' + error.message);
  } finally {
    generateBtn.disabled = false;
    generateBtn.innerHTML = '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1"><path d="M12,3L2,12H5V20H19V12H22L12,3M12,8.75A2.25,2.25 0 0,1 14.25,11A2.25,2.25 0 0,1 12,13.25A2.25,2.25 0 0,1 9.75,11A2.25,2.25 0 0,1 12,8.75Z"/></svg>Generate Configuration';
  }
}

// Apply auto-generated config to form fields
function applyCreateAutoConfig(config) {
  // Apply agent type
  const typeSelect = document.getElementById('agentType');
  if (typeSelect && config.agent_type) {
    typeSelect.value = config.agent_type;
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

  // Highlight fields that were auto-configured
  highlightCreateAutoConfiguredFields();
}

// Briefly highlight fields that were auto-configured
function highlightCreateAutoConfiguredFields() {
  const fields = ['agentType', 'llmModel', 'temperature', 'systemPrompt'];

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

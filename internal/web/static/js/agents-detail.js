// Agent Detail Page JavaScript

let currentAgent = null;
let agentName = '';
let isEditingConfig = false;
let isEditingPrompt = false;
let isEditingDescription = false;
let availableProviders = []; // Cache for available providers and models from API

// Get agent name from URL - supports both /agents/{name} and ?name={name}
function getAgentNameFromURL() {
  // First try path-based URL: /agents/{agent-name}
  const pathMatch = window.location.pathname.match(/^\/agents\/([^\/]+)$/);
  if (pathMatch) {
    return decodeURIComponent(pathMatch[1]);
  }
  // Fall back to query parameter for backward compatibility
  const params = new URLSearchParams(window.location.search);
  return params.get('name');
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
    return availableProviders;
  } catch (error) {
    console.error('Failed to load providers:', error);
    return [];
  }
}

// Populate model select with options from available providers
function populateEditModelOptions() {
  const modelSelect = document.getElementById('editModel');
  const providerFilter = document.getElementById('editProviderFilter');
  const typeSelect = document.getElementById('editType');

  if (!modelSelect || availableProviders.length === 0) {
    return;
  }

  const selectedProvider = providerFilter ? providerFilter.value : '';
  const selectedAgentType = typeSelect ? typeSelect.value : (currentAgent?.type || 'tool-calling');

  // Store current value to re-select it after populating
  const currentModelValue = currentAgent?.model || '';

  // Clear existing options
  modelSelect.innerHTML = '';

  // Map provider display names to our filter values
  const providerNameMap = {
    'OpenAI': 'openai',
    'Anthropic': 'claude',
    'Ollama': 'ollama'
  };

  // Find models from the API data
  availableProviders.forEach(provider => {
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

  // Try to select the current model
  if (currentModelValue) {
    for (let i = 0; i < modelSelect.options.length; i++) {
      if (modelSelect.options[i].value === currentModelValue) {
        modelSelect.selectedIndex = i;
        break;
      }
    }
  }
}

// Filter models by provider (called when provider filter changes)
function filterEditModelOptions() {
  populateEditModelOptions();
}

// Set up sidebar toggle functionality
function setupSidebarToggle() {
  const sidebarToggle = document.getElementById('sidebarToggle');
  const sidebar = document.getElementById('sidebar');

  if (!sidebarToggle || !sidebar) return;

  // Show sidebar on large screens by default
  function handleSidebarResponsive() {
    if (window.innerWidth >= 992) {
      // Show sidebar on large screens
      sidebar.classList.remove('d-none');
      sidebar.classList.add('d-lg-block');
      sidebarToggle.setAttribute('aria-expanded', 'true');
      // Restore sidebar width
      const savedWidth = localStorage.getItem('sidebarWidth') || '300';
      document.documentElement.style.setProperty('--sidebar-width', `${savedWidth}px`);
    } else {
      // Hide sidebar on small screens by default
      sidebar.classList.add('d-none');
      sidebar.classList.remove('d-lg-block');
      sidebar.classList.remove('sidebar-mobile-show');
      sidebarToggle.setAttribute('aria-expanded', 'false');
      document.documentElement.style.setProperty('--sidebar-width', '0px');
    }
  }

  // Toggle button click handler
  sidebarToggle.addEventListener('click', function(event) {
    event.preventDefault();

    const isHidden = sidebar.classList.toggle('d-none');

    if (isHidden) {
      sidebar.classList.remove('d-lg-block');
      sidebar.classList.remove('sidebar-mobile-show');
      sidebarToggle.setAttribute('aria-expanded', 'false');
      document.documentElement.style.setProperty('--sidebar-width', '0px');
    } else {
      if (window.innerWidth >= 992) {
        sidebar.classList.add('d-lg-block');
        sidebar.classList.remove('sidebar-mobile-show');
      } else {
        sidebar.classList.remove('d-lg-block');
        sidebar.classList.add('sidebar-mobile-show');
      }
      sidebarToggle.setAttribute('aria-expanded', 'true');
      const savedWidth = localStorage.getItem('sidebarWidth') || '300';
      document.documentElement.style.setProperty('--sidebar-width', `${savedWidth}px`);
    }

    if (window.EventBus) {
      EventBus.emit('sidebar:toggled', { hidden: isHidden });
    }
  });

  // Close sidebar when clicking outside on mobile
  document.addEventListener('click', function(event) {
    const isClickInSidebar = sidebar.contains(event.target);
    const isClickOnToggle = sidebarToggle.contains(event.target);
    const isClickInModal = event.target.closest('.modal') || event.target.classList.contains('modal-backdrop');

    if (!isClickInSidebar && !isClickOnToggle && !isClickInModal &&
        !sidebar.classList.contains('d-none') &&
        window.innerWidth < 992) {
      sidebar.classList.add('d-none');
      sidebar.classList.remove('sidebar-mobile-show');
      sidebar.classList.remove('d-lg-block');
      document.documentElement.style.setProperty('--sidebar-width', '0px');
    }
  });

  // Handle window resize
  window.addEventListener('resize', handleSidebarResponsive);

  // Run initial check on page load
  handleSidebarResponsive();
}

// Initialize page
document.addEventListener('DOMContentLoaded', async () => {
  // Set up sidebar toggle functionality
  setupSidebarToggle();

  // Get agent name from URL
  agentName = getAgentNameFromURL();

  if (!agentName) {
    showError('No agent specified');
    setTimeout(() => {
      window.location.href = '/agents';
    }, 2000);
    return;
  }

  // Load providers in parallel with agent details
  await Promise.all([
    loadAvailableProviders(),
    loadAgentDetails()
  ]);

  const pluginPanel = document.getElementById('pluginManagerPanel');
  if (pluginPanel) {
    await loadAvailablePlugins();
    updateAvailablePluginsToggle();
  }

  if (window.EventBus && typeof EventBus.on === 'function') {
    EventBus.on('plugin:uploaded', async () => {
      if (pluginManagerVisible) {
        await loadAvailablePlugins();
      }
    }, 'agents-detail');
  }
});

async function fetchAgentDetail() {
  const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}/detail`);

  if (!response.ok) {
    if (response.status === 404) {
      throw new Error('Agent not found');
    }
    throw new Error('Failed to load agent details');
  }

  return response.json();
}

// Load agent details from API
async function loadAgentDetails() {
  try {
    showLoading(true);
    currentAgent = await fetchAgentDetail();
    renderAgentDetails();
  } catch (error) {
    console.error('Error loading agent details:', error);
    showError(error.message || 'Failed to load agent details');
    setTimeout(() => {
      window.location.href = '/agents';
    }, 3000);
  } finally {
    showLoading(false);
  }
}

async function refreshAgentDetails() {
  try {
    currentAgent = await fetchAgentDetail();
    renderAgentDetails();
  } catch (error) {
    console.error('Error refreshing agent details:', error);
    setConfigStatus(error.message || 'Failed to refresh agent details', 'error');
  }
}

// Render agent details on page
function renderAgentDetails() {
  if (!currentAgent) return;

  // Header
  const avatar = document.getElementById('agentAvatar');
  if (avatar) {
    // Check for custom avatar image
    if (currentAgent.metadata?.avatar_image) {
      avatar.innerHTML = `<img src="/avatars/${escapeHtml(currentAgent.metadata.avatar_image)}" alt="${escapeHtml(currentAgent.name)}" style="width: 100%; height: 100%; object-fit: cover; border-radius: inherit;">`;
      avatar.style.background = 'transparent';
      avatar.style.overflow = 'hidden';
    } else {
      avatar.style.background = getAgentColor(currentAgent);
      avatar.textContent = getAgentInitials(currentAgent.name);
    }
  }

  const nameEl = document.getElementById('agentName');
  if (nameEl) nameEl.textContent = currentAgent.name;

  const description = currentAgent.metadata?.description || 'No description provided';
  const descEl = document.getElementById('agentDescription');
  if (descEl) descEl.textContent = description;

  const typeEl = document.getElementById('agentType');
  if (typeEl) typeEl.textContent = capitalize(currentAgent.type || 'tool-calling');

  const modelEl = document.getElementById('agentModel');
  if (modelEl) modelEl.textContent = currentAgent.model || 'Not set';

  const providerEl = document.getElementById('agentProvider');
  const provider = currentAgent.provider || currentAgent.llm_provider || '';
  if (providerEl) providerEl.textContent = provider || 'Not set';

  const tempEl = document.getElementById('agentTemperature');
  if (tempEl) tempEl.textContent = currentAgent.temperature ?? 'Not set';

  const maxTokensEl = document.getElementById('agentMaxTokens');
  if (maxTokensEl) maxTokensEl.textContent = formatMaxTokens(currentAgent.max_output_tokens);

  const pluginCountEl = document.getElementById('pluginCount');
  if (pluginCountEl) pluginCountEl.textContent = currentAgent.enabled_plugins?.length || 0;

  // Statistics
  const stats = currentAgent.statistics || {};
  const statMessages = document.getElementById('statMessages');
  if (statMessages) statMessages.textContent = formatNumber(stats.message_count || 0);
  const statTokens = document.getElementById('statTokens');
  if (statTokens) statTokens.textContent = formatNumber(stats.token_usage || 0);
  const statCost = document.getElementById('statCost');
  if (statCost) statCost.textContent = '$' + (stats.total_cost || 0).toFixed(4);

  const avgTokens = stats.message_count > 0
    ? Math.round(stats.token_usage / stats.message_count)
    : 0;
  const statAvgTokens = document.getElementById('statAvgTokens');
  if (statAvgTokens) statAvgTokens.textContent = formatNumber(avgTokens);

  const createdAt = document.getElementById('createdAt');
  if (createdAt) createdAt.textContent = formatFullDate(stats.created_at);
  const updatedAt = document.getElementById('updatedAt');
  if (updatedAt) updatedAt.textContent = formatFullDate(stats.updated_at);

  // Configuration
  const configModel = document.getElementById('configModel');
  if (configModel) configModel.textContent = currentAgent.model || 'Not set';
  const configTemp = document.getElementById('configTemp');
  if (configTemp) configTemp.textContent = currentAgent.temperature ?? 1.0;
  const configType = document.getElementById('configType');
  if (configType) configType.textContent = capitalize(currentAgent.type || 'tool-calling');
  const configRole = document.getElementById('configRole');
  if (configRole) configRole.textContent = capitalize(currentAgent.role || 'general');

  const systemPrompt = currentAgent.system_prompt || 'Default system prompt';
  const promptEl = document.getElementById('configPrompt');
  if (promptEl) {
    promptEl.textContent = systemPrompt || 'No system prompt set';
  }
  const promptDisplay = document.getElementById('promptDisplay');
  if (promptDisplay) {
    promptDisplay.textContent = systemPrompt || 'No system prompt set';
  }

  // Plugins
  renderPlugins();

  // Tags
  renderTags();

  // MCP Servers
  renderMCPServers();

  // Skills
  renderSkills();

  // Show content
  const header = document.getElementById('agentHeader');
  if (header) header.style.display = 'flex';
  const grid = document.getElementById('contentGrid');
  if (grid) grid.style.display = 'grid';

  // Keep edit form in sync with latest data
  populateConfigForm();
  setConfigEditMode(isEditingConfig);
  populatePromptForm();
  setPromptEditMode(isEditingPrompt);
  populateDescriptionForm();
  setDescriptionEditMode(isEditingDescription);
}

function setConfigEditMode(enabled) {
  const display = document.getElementById('configDisplay');
  const form = document.getElementById('configEditForm');
  const editBtn = document.getElementById('editConfigBtn');
  const actions = document.getElementById('configEditActions');
  const saveBtn = document.getElementById('saveConfigBtn');

  isEditingConfig = enabled;

  if (display) display.style.display = enabled ? 'none' : 'block';
  if (form) form.style.display = enabled ? 'block' : 'none';
  if (actions) actions.style.display = enabled ? 'flex' : 'none';
  if (saveBtn) saveBtn.style.display = enabled ? 'inline-flex' : 'none';
  if (editBtn) {
    editBtn.textContent = enabled ? 'Cancel' : 'Edit';
  }

  if (enabled) {
    populateConfigForm();
    setConfigStatus('');
  }
}

function toggleConfigEditMode() {
  if (isEditingConfig) {
    setConfigEditMode(false);
    setConfigStatus('');
    populateConfigForm(); // reset any unsaved edits
  } else {
    setConfigEditMode(true);
  }
}

function populateConfigForm() {
  if (!currentAgent) return;
  const modelSelect = document.getElementById('editModel');
  const providerFilter = document.getElementById('editProviderFilter');
  const tempInput = document.getElementById('editTemperature');
  const maxTokensInput = document.getElementById('editMaxTokens');
  const typeSelect = document.getElementById('editType');
  const roleSelect = document.getElementById('editRole');

  // Set type and role first as they affect model filtering
  if (typeSelect) typeSelect.value = currentAgent.type || 'tool-calling';
  if (roleSelect) roleSelect.value = currentAgent.role || 'general';
  if (tempInput) tempInput.value = currentAgent.temperature ?? '';
  if (maxTokensInput) maxTokensInput.value = currentAgent.max_output_tokens || '';

  // Reset provider filter and populate models
  if (providerFilter) providerFilter.value = '';

  // Populate the model dropdown and select current model
  populateEditModelOptions();

  // Select the current model if available
  if (modelSelect && currentAgent.model) {
    for (let i = 0; i < modelSelect.options.length; i++) {
      if (modelSelect.options[i].value === currentAgent.model) {
        modelSelect.selectedIndex = i;
        break;
      }
    }
  }
}

async function saveConfigChanges() {
  if (!currentAgent) return;

  const modelSelect = document.getElementById('editModel');
  const model = modelSelect?.value || '';
  const tempRaw = document.getElementById('editTemperature')?.value;
  const maxTokensRaw = document.getElementById('editMaxTokens')?.value;
  const type = document.getElementById('editType')?.value || 'tool-calling';
  const role = document.getElementById('editRole')?.value || 'general';

  if (!model) {
    setConfigStatus('Model is required to save configuration.', 'error');
    return;
  }

  // Get provider from the selected model's data attribute
  const selectedOption = modelSelect?.options[modelSelect.selectedIndex];
  const provider = selectedOption?.getAttribute('data-provider') || '';

  const payload = {
    model,
    type,
    role
  };

  const temp = parseFloat(tempRaw);
  if (!Number.isNaN(temp)) {
    payload.temperature = temp;
  }

  const maxTokens = parseInt(maxTokensRaw, 10);
  if (!Number.isNaN(maxTokens)) {
    payload.max_output_tokens = maxTokens;
  }

  if (provider) {
    payload.llm_provider = provider;
  }

  try {
    setConfigSavingState(true);
    setConfigStatus('Saving changes...');

    const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}`, {
      method: 'PATCH',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(payload)
    });

    if (!response.ok) {
      const errorText = await response.text();
      throw new Error(errorText || 'Failed to save changes');
    }

    await refreshAgentDetails();
    setConfigStatus('Configuration updated successfully.', 'success');
    setConfigEditMode(false);
  } catch (error) {
    console.error('Failed to save configuration:', error);
    setConfigStatus(error.message || 'Failed to save configuration', 'error');
  } finally {
    setConfigSavingState(false);
  }
}

function setConfigSavingState(isSaving) {
  const saveBtn = document.getElementById('saveConfigBtn');
  const editBtn = document.getElementById('editConfigBtn');
  if (saveBtn) {
    saveBtn.disabled = isSaving;
    if (isSaving) {
      saveBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1" role="status"></span>Saving...';
    } else {
      saveBtn.innerHTML = 'Save';
    }
  }
  if (editBtn) {
    editBtn.disabled = isSaving;
  }
}

function setConfigStatus(message, type = 'info') {
  const statusEl = document.getElementById('configEditStatus');
  if (!statusEl) return;
  statusEl.textContent = message || '';
  if (!message) return;

  if (type === 'error') {
    statusEl.style.color = 'var(--danger-color)';
  } else if (type === 'success') {
    statusEl.style.color = 'var(--success-color, #22c55e)';
  } else {
    statusEl.style.color = 'var(--text-secondary)';
  }
}

function setPromptEditMode(enabled) {
  const display = document.getElementById('promptDisplay');
  const form = document.getElementById('promptEditForm');
  const editBtn = document.getElementById('editPromptBtn');
  const actions = document.getElementById('promptEditActions');
  const saveBtn = document.getElementById('savePromptBtn');

  isEditingPrompt = enabled;

  if (display) display.style.display = enabled ? 'none' : 'block';
  if (form) form.style.display = enabled ? 'block' : 'none';
  if (actions) actions.style.display = enabled ? 'flex' : 'none';
  if (saveBtn) saveBtn.style.display = enabled ? 'inline-flex' : 'none';
  if (editBtn) {
    editBtn.textContent = enabled ? 'Cancel' : 'Edit';
  }

  if (enabled) {
    populatePromptForm();
    setPromptStatus('');
  }
}

function togglePromptEditMode() {
  if (isEditingPrompt) {
    setPromptEditMode(false);
    setPromptStatus('');
    populatePromptForm(); // reset unsaved edits
  } else {
    setPromptEditMode(true);
  }
}

function populatePromptForm() {
  if (!currentAgent) return;
  const promptInput = document.getElementById('editSystemPrompt');
  if (promptInput) promptInput.value = currentAgent.system_prompt || '';
}

async function savePromptChanges() {
  if (!currentAgent) return;
  const promptInput = document.getElementById('editSystemPrompt');
  const systemPrompt = promptInput ? promptInput.value.trim() : '';

  try {
    setPromptSavingState(true);
    setPromptStatus('Saving system prompt...');

    const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}`, {
      method: 'PATCH',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ system_prompt: systemPrompt })
    });

    if (!response.ok) {
      const errorText = await response.text();
      throw new Error(errorText || 'Failed to save system prompt');
    }

    await refreshAgentDetails();
    setPromptStatus('System prompt updated successfully.', 'success');
    setPromptEditMode(false);
  } catch (error) {
    console.error('Failed to save system prompt:', error);
    setPromptStatus(error.message || 'Failed to save system prompt', 'error');
  } finally {
    setPromptSavingState(false);
  }
}

function setPromptSavingState(isSaving) {
  const saveBtn = document.getElementById('savePromptBtn');
  const editBtn = document.getElementById('editPromptBtn');
  if (saveBtn) {
    saveBtn.disabled = isSaving;
    if (isSaving) {
      saveBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1" role="status"></span>Saving...';
    } else {
      saveBtn.innerHTML = 'Save';
    }
  }
  if (editBtn) {
    editBtn.disabled = isSaving;
  }
}

function setPromptStatus(message, type = 'info') {
  const statusEl = document.getElementById('promptEditStatus');
  if (!statusEl) return;
  statusEl.textContent = message || '';
  if (!message) return;

  if (type === 'error') {
    statusEl.style.color = 'var(--danger-color)';
  } else if (type === 'success') {
    statusEl.style.color = 'var(--success-color, #22c55e)';
  } else {
    statusEl.style.color = 'var(--text-secondary)';
  }
}

// Description editing functions
function setDescriptionEditMode(enabled) {
  const display = document.getElementById('agentDescription');
  const form = document.getElementById('descriptionEditForm');
  const editBtn = document.getElementById('editDescriptionBtn');

  isEditingDescription = enabled;

  if (display) display.style.display = enabled ? 'none' : 'block';
  if (form) form.style.display = enabled ? 'block' : 'none';
  if (editBtn) editBtn.style.display = enabled ? 'none' : 'inline-flex';

  if (enabled) {
    populateDescriptionForm();
    setDescriptionStatus('');
  }
}

function toggleDescriptionEditMode() {
  if (isEditingDescription) {
    setDescriptionEditMode(false);
    setDescriptionStatus('');
    populateDescriptionForm();
  } else {
    setDescriptionEditMode(true);
  }
}

function populateDescriptionForm() {
  if (!currentAgent) return;
  const descInput = document.getElementById('editDescription');
  if (descInput) descInput.value = currentAgent.metadata?.description || '';
}

async function saveDescriptionChanges() {
  if (!currentAgent) return;
  const descInput = document.getElementById('editDescription');
  const description = descInput ? descInput.value.trim() : '';

  try {
    setDescriptionSavingState(true);
    setDescriptionStatus('Saving description...');

    const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}`, {
      method: 'PATCH',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ description: description })
    });

    if (!response.ok) {
      const errorText = await response.text();
      throw new Error(errorText || 'Failed to save description');
    }

    await refreshAgentDetails();
    setDescriptionStatus('Description updated successfully.', 'success');
    setDescriptionEditMode(false);
  } catch (error) {
    console.error('Failed to save description:', error);
    setDescriptionStatus(error.message || 'Failed to save description', 'error');
  } finally {
    setDescriptionSavingState(false);
  }
}

function setDescriptionSavingState(isSaving) {
  const saveBtn = document.getElementById('saveDescriptionBtn');
  if (saveBtn) {
    saveBtn.disabled = isSaving;
    if (isSaving) {
      saveBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1" role="status"></span>Saving...';
    } else {
      saveBtn.innerHTML = 'Save';
    }
  }
}

function setDescriptionStatus(message, type = 'info') {
  const statusEl = document.getElementById('descriptionEditStatus');
  if (!statusEl) return;
  statusEl.textContent = message || '';
  if (!message) return;

  if (type === 'error') {
    statusEl.style.color = 'var(--danger-color)';
  } else if (type === 'success') {
    statusEl.style.color = 'var(--success-color, #22c55e)';
  } else {
    statusEl.style.color = 'var(--text-secondary)';
  }
}

function formatMaxTokens(value) {
  if (!value || value <= 0) return 'Not set';
  return value.toLocaleString();
}

// Check if a plugin has configuration by checking the default-settings endpoint
async function checkPluginHasConfig(pluginName) {
  try {
    const response = await fetch(`/api/plugins/${encodeURIComponent(pluginName)}/default-settings`);
    return response.ok;
  } catch (error) {
    return false;
  }
}

// Render plugins list
async function renderPlugins() {
  const container = document.getElementById('enabledPluginsList');
  if (!container) return;

  const pluginsRaw = currentAgent?.enabled_plugins;
  const plugins = Array.isArray(pluginsRaw) ? pluginsRaw : (pluginsRaw ? [pluginsRaw] : []);

  if (plugins.length === 0) {
    container.innerHTML = '<div class="empty-message">No plugins enabled</div>';
    return;
  }

  // Show loading while checking config status
  container.innerHTML = '<div class="text-center py-4" style="color: var(--text-secondary);">Loading plugins...</div>';

  // Check configuration status for all plugins in parallel
  const pluginNames = plugins.map(plugin =>
    typeof plugin === 'string' ? plugin : (plugin?.name || plugin?.id || plugin?.plugin || '')
  );

  const configChecks = await Promise.all(
    pluginNames.map(name => checkPluginHasConfig(name))
  );

  const configStatus = new Map();
  pluginNames.forEach((name, index) => {
    configStatus.set(name, configChecks[index]);
  });

  container.innerHTML = '';
  plugins.forEach(plugin => {
    const name = typeof plugin === 'string' ? plugin : (plugin?.name || plugin?.id || plugin?.plugin || '');
    const version = typeof plugin === 'object' && plugin ? (plugin.version || plugin?.meta?.version || '') : '';
    const hasConfig = configStatus.get(name) || false;

    const item = document.createElement('div');
    item.className = 'plugin-item';
    item.style.cssText = 'display: flex; justify-content: space-between; align-items: center;';
    item.innerHTML = `
            <div>
                <div class="plugin-name">${escapeHtml(name || '(unknown plugin)')}</div>
                ${version ? `<div class="plugin-version">v${escapeHtml(version)}</div>` : ''}
            </div>
            ${hasConfig ? `
                <button class="modern-btn modern-btn-secondary plugin-config-btn"
                        data-plugin-name="${escapeHtml(name)}"
                        title="Configure plugin"
                        style="padding: 0.4rem 0.8rem; font-size: 12px;">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                        <path d="M12,15.5A3.5,3.5 0 0,1 8.5,12A3.5,3.5 0 0,1 12,8.5A3.5,3.5 0 0,1 15.5,12A3.5,3.5 0 0,1 12,15.5M19.43,12.97C19.47,12.65 19.5,12.33 19.5,12C19.5,11.67 19.47,11.34 19.43,11L21.54,9.37C21.73,9.22 21.78,8.95 21.66,8.73L19.66,5.27C19.54,5.05 19.27,4.96 19.05,5.05L16.56,6.05C16.04,5.66 15.5,5.32 14.87,5.07L14.5,2.42C14.46,2.18 14.25,2 14,2H10C9.75,2 9.54,2.18 9.5,2.42L9.13,5.07C8.5,5.32 7.96,5.66 7.44,6.05L4.95,5.05C4.73,4.96 4.46,5.05 4.34,5.27L2.34,8.73C2.22,8.95 2.27,9.22 2.46,9.37L4.57,11C4.53,11.34 4.5,11.67 4.5,12C4.5,12.33 4.53,12.65 4.57,12.97L2.46,14.63C2.27,14.78 2.22,15.05 2.34,15.27L4.34,18.73C4.46,18.95 4.73,19.03 4.95,18.95L7.44,17.94C7.96,18.34 8.5,18.68 9.13,18.93L9.5,21.58C9.54,21.82 9.75,22 10,22H14C14.25,22 14.46,21.82 14.5,21.58L14.87,18.93C15.5,18.68 16.04,18.34 16.56,17.94L19.05,18.95C19.27,19.03 19.54,18.95 19.66,18.73L21.66,15.27C21.78,15.05 21.73,14.78 21.54,14.63L19.43,12.97Z"/>
                    </svg>
                    Configure
                </button>
            ` : ''}
        `;
    container.appendChild(item);
  });

  // Setup click handlers for configure buttons
  setupPluginConfigButtons();
}

// Setup event listeners for plugin configure buttons
function setupPluginConfigButtons() {
  const configButtons = document.querySelectorAll('#enabledPluginsList .plugin-config-btn');
  configButtons.forEach(button => {
    button.addEventListener('click', async (e) => {
      e.stopPropagation();
      const pluginName = button.dataset.pluginName;
      if (pluginName && typeof showPluginConfigModal === 'function') {
        await showPluginConfigModal(pluginName);
      } else {
        console.error('showPluginConfigModal is not available or plugin name is missing');
      }
    });
  });
}

// Render tags
function renderTags() {
  const container = document.getElementById('tagsList');
  const tags = currentAgent.metadata?.tags || [];

  if (tags.length === 0) {
    document.getElementById('tagsSection').style.display = 'none';
    return;
  }

  container.innerHTML = '';
  tags.forEach(tag => {
    const tagEl = document.createElement('span');
    tagEl.className = 'tag';
    tagEl.textContent = tag;
    container.appendChild(tagEl);
  });
}

// Render skills list
async function renderSkills() {
  const section = document.getElementById('skillsSection');
  const container = document.getElementById('skillsList');
  if (!section || !container) return;

  const name = currentAgent?.name || agentName;
  if (!name) {
    section.style.display = 'none';
    return;
  }

  section.style.display = 'block';
  container.innerHTML = '<div class="text-center py-3" style="color: var(--text-secondary);">Loading skills...</div>';

  try {
    const response = await fetch(`/api/skills?agent=${encodeURIComponent(name)}`);
    if (!response.ok) {
      throw new Error('Failed to load skills');
    }
    const data = await response.json();
    const skills = Array.isArray(data.skills) ? data.skills : [];

    if (skills.length === 0) {
      container.innerHTML = '<div class="text-center py-3" style="color: var(--text-secondary);">No skills available for this agent.</div>';
      return;
    }

    skills.sort((a, b) => (a.name || '').localeCompare(b.name || '', undefined, { sensitivity: 'base' }));

    container.innerHTML = '';
    skills.forEach(skill => {
      const skillName = skill?.name || '(unnamed skill)';
      const source = skill?.source || 'local';
      const description = skill?.description || 'No description';

      const item = document.createElement('div');
      item.style.cssText = 'padding: 12px; border: 1px solid var(--border-color); border-radius: 8px; background: var(--bg-secondary);';
      item.innerHTML = `
        <div style="display: flex; align-items: center; gap: 8px; flex-wrap: wrap;">
          <div style="font-weight: 600; color: var(--text-primary);">${escapeHtml(skillName)}</div>
          <span style="font-size: 10px; padding: 2px 6px; border-radius: 4px; background: var(--bg-tertiary); color: var(--text-secondary); text-transform: uppercase; letter-spacing: 0.3px;">${escapeHtml(source)}</span>
        </div>
        <div style="font-size: 12px; color: var(--text-secondary); margin-top: 6px;">${escapeHtml(description)}</div>
      `;
      container.appendChild(item);
    });
  } catch (error) {
    console.error('Failed to load skills:', error);
    container.innerHTML = '<div class="text-center py-3" style="color: var(--danger-color);">Failed to load skills.</div>';
  }
}

function openUploadPluginModal() {
  if (typeof showPluginUploadModal === 'function') {
    showPluginUploadModal();
    return;
  }
  showToast('Plugin upload is not available right now', 'error');
}

// ============================================
// Plugin Management Functions
// ============================================

let pluginManagerVisible = true;
let allAvailablePlugins = [];

function updateAvailablePluginsToggle() {
  const toggleBtn = document.getElementById('toggleAvailablePluginsBtn');
  if (!toggleBtn) return;

  if (pluginManagerVisible) {
    toggleBtn.innerHTML = `
      <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1">
        <path d="M7.41,15.41L12,10.83L16.59,15.41L18,14L12,8L6,14L7.41,15.41Z"/>
      </svg>
      Hide Available
    `;
    toggleBtn.setAttribute('aria-expanded', 'true');
  } else {
    toggleBtn.innerHTML = `
      <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1">
        <path d="M7.41,8.58L12,13.17L16.59,8.58L18,10L12,16L6,10L7.41,8.58Z"/>
      </svg>
      Show Available
    `;
    toggleBtn.setAttribute('aria-expanded', 'false');
  }
}

// Toggle available plugins panel
async function toggleAvailablePluginsPanel() {
  const panel = document.getElementById('pluginManagerPanel');
  if (!panel) return;

  pluginManagerVisible = !pluginManagerVisible;
  panel.style.display = pluginManagerVisible ? 'block' : 'none';
  updateAvailablePluginsToggle();

  if (pluginManagerVisible) {
    await loadAvailablePlugins();
  }
}

// Load all available plugins from registry
async function loadAvailablePlugins() {
  const container = document.getElementById('availablePluginsList');
  container.innerHTML = '<div class="text-center py-3" style="color: var(--text-secondary);">Loading plugins...</div>';

  try {
    const response = await fetch('/api/plugins');
    if (!response.ok) throw new Error('Failed to load plugins');

    const data = await response.json();
    allAvailablePlugins = data.plugins || [];

    renderAvailablePlugins();
  } catch (error) {
    console.error('Error loading plugins:', error);
    container.innerHTML = '<div class="text-center py-3" style="color: var(--danger-color);">Failed to load plugins</div>';
  }
}

// Render available plugins with toggle switches
function renderAvailablePlugins() {
  const container = document.getElementById('availablePluginsList');
  const enabledPlugins = currentAgent?.enabled_plugins || [];
  // enabled_plugins can be an array of strings or objects with 'name' property
  const enabledNames = enabledPlugins.map(p => typeof p === 'string' ? p : (p?.name || p));
  const enabledSet = new Set(enabledNames);

  if (allAvailablePlugins.length === 0) {
    container.innerHTML = `
      <div class="text-center py-3" style="color: var(--text-secondary);">
        <div>No plugins available yet.</div>
        <button class="modern-btn modern-btn-secondary mt-2" type="button" onclick="openUploadPluginModal()">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
            <path d="M9,16V10H5L12,3L19,10H15V16H9M5,20V18H19V20H5Z"/>
          </svg>
          Upload Plugin
        </button>
      </div>
    `;
    return;
  }

  container.innerHTML = '';
  allAvailablePlugins.forEach(plugin => {
    const pluginName = plugin.name || plugin;
    const isEnabled = enabledSet.has(pluginName);

    const item = document.createElement('div');
    item.className = 'plugin-item';
    item.style.cssText = 'display: flex; justify-content: space-between; align-items: center; padding: 12px; background: var(--bg-secondary); border: 1px solid var(--border-color); border-radius: 8px;';
    item.innerHTML = `
            <div style="flex: 1;">
                <div style="font-weight: 500; color: var(--text-primary);">${escapeHtml(pluginName)}</div>
                ${plugin.description ? `<div style="font-size: 12px; color: var(--text-secondary); margin-top: 4px;">${escapeHtml(plugin.description)}</div>` : ''}
            </div>
            <label class="toggle-switch" style="margin-left: 12px;">
                <input type="checkbox" id="plugin_${pluginName}" ${isEnabled ? 'checked' : ''} onchange="togglePlugin('${escapeHtml(pluginName)}', this.checked)">
                <span class="toggle-slider"></span>
            </label>
        `;
    container.appendChild(item);
  });
}

// Toggle plugin for this agent
async function togglePlugin(pluginName, enabled) {
  const checkbox = document.getElementById(`plugin_${pluginName}`);

  try {
    // First, switch to this agent
    const switchResponse = await fetch(`/api/agents?name=${encodeURIComponent(agentName)}`, {
      method: 'PUT'
    });

    if (!switchResponse.ok) {
      throw new Error('Failed to switch to agent');
    }

    // Then enable/disable the plugin
    const endpoint = `/api/plugins/${encodeURIComponent(pluginName)}/${enabled ? 'enable' : 'disable'}`;
    const response = await fetch(endpoint, {
      method: 'POST'
    });

    if (response.ok) {
      showToast(`${pluginName} ${enabled ? 'enabled' : 'disabled'}`, 'success');
      // Reload agent details to refresh the enabled plugins list
      await loadAgentDetails();
      if (pluginManagerVisible) {
        renderAvailablePlugins();
      }
      // Refresh plugin pages dropdown
      if (window.refreshPluginPages) {
        window.refreshPluginPages();
      }
    } else {
      const error = await response.text();
      showToast(`Failed: ${error}`, 'error');
      if (checkbox) checkbox.checked = !enabled;
    }
  } catch (error) {
    console.error('Toggle plugin error:', error);
    showToast(`Failed to ${enabled ? 'enable' : 'disable'} plugin`, 'error');
    if (checkbox) checkbox.checked = !enabled;
  }
}

// Render MCP servers
function renderMCPServers() {
  const container = document.getElementById('mcpList');
  const servers = currentAgent.mcp_servers || [];

  document.getElementById('mcpSection').style.display = 'block';

  if (servers.length === 0) {
    container.innerHTML = '<p style="color: var(--text-secondary); font-size: 14px;">No MCP servers enabled for this agent. Click "Configure" to enable MCP servers.</p>';
    return;
  }

  container.innerHTML = '';
  servers.forEach(server => {
    const item = document.createElement('div');
    item.className = 'plugin-item';
    item.innerHTML = `
            <div class="plugin-name">${escapeHtml(server)}</div>
        `;
    container.appendChild(item);
  });
}

// Toggle MCP configuration panel
async function toggleMCPConfig() {
  const panel = document.getElementById('mcpConfigPanel');
  if (panel.style.display === 'none') {
    panel.style.display = 'block';
    await loadMCPConfigPanel();
  } else {
    panel.style.display = 'none';
  }
}

// Load MCP configuration panel
async function loadMCPConfigPanel() {
  const panel = document.getElementById('mcpConfigPanel');

  try {
    // Fetch all available MCP servers
    const response = await fetch('/api/mcp/servers');
    const data = await response.json();
    const allServers = data.servers || [];
    const enabledServers = currentAgent.mcp_servers || [];

    panel.innerHTML = `
            <h3 style="font-size: 16px; margin-bottom: 16px; color: var(--text-primary);">Available MCP Servers</h3>
            <div id="mcpServersList">
                ${allServers.map(server => `
                    <div class="mcp-server-config" style="margin-bottom: 16px; padding: 16px; background: var(--bg-tertiary); border-radius: 8px;">
                        <div class="d-flex justify-content-between align-items-start mb-2">
                            <div class="d-flex align-items-center gap-2">
                                <input type="checkbox"
                                    id="mcp_${server.name}"
                                    ${enabledServers.includes(server.name) ? 'checked' : ''}
                                    onchange="toggleMCPServer('${server.name}', this.checked)"
                                    style="cursor: pointer;">
                                <label for="mcp_${server.name}" style="cursor: pointer; font-weight: 600; color: var(--text-primary); margin: 0;">
                                    ${server.name}
                                </label>
                            </div>
                        </div>
                        <div id="mcpConfig_${server.name}" style="display: ${enabledServers.includes(server.name) ? 'block' : 'none'}; margin-top: 12px; padding-left: 24px;">
                            ${getMCPServerConfigUI(server)}
                        </div>
                    </div>
                `).join('')}
            </div>
        `;
  } catch (error) {
    console.error('Failed to load MCP config:', error);
    panel.innerHTML = '<p style="color: var(--text-secondary);">Failed to load MCP configuration</p>';
  }
}

// Get configuration UI for specific MCP server
function getMCPServerConfigUI(server) {
  // Special handling for filesystem server
  if (server.name === 'filesystem') {
    const currentPath = server.args && server.args.length > 2 ? server.args[2] : '/path/to/directory';
    return `
            <div class="mb-2">
                <label style="font-size: 13px; color: var(--text-secondary); display: block; margin-bottom: 4px;">
                    Allowed Directory Path:
                </label>
                <input type="text"
                    id="filesystem_path"
                    value="${currentPath}"
                    placeholder="/path/to/directory"
                    style="width: 100%; padding: 8px; background: var(--bg-secondary); border: 1px solid var(--border-color); border-radius: 4px; color: var(--text-primary); font-size: 14px;"
                    onchange="updateMCPServerConfig('filesystem', 'path', this.value)">
                <small style="color: var(--text-secondary); font-size: 12px;">The directory this agent can access via the filesystem MCP server</small>
            </div>
        `;
  }

  // Default: show command and args
  return `
        <div style="font-size: 13px; color: var(--text-secondary);">
            <div><strong>Command:</strong> ${server.command}</div>
            <div><strong>Args:</strong> ${server.args ? server.args.join(' ') : 'none'}</div>
        </div>
    `;
}

// Toggle MCP server for this agent
async function toggleMCPServer(serverName, enabled) {
  const configDiv = document.getElementById(`mcpConfig_${serverName}`);
  if (configDiv) {
    configDiv.style.display = enabled ? 'block' : 'none';
  }

  try {
    const endpoint = enabled ? `/api/mcp/servers/${serverName}/enable` : `/api/mcp/servers/${serverName}/disable`;
    const response = await fetch(endpoint, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ agent_name: agentName })
    });

    if (response.ok) {
      showToast(`${serverName} ${enabled ? 'enabled' : 'disabled'}`, 'success');
      await loadAgent();
    } else {
      const error = await response.text();
      showToast(`Failed: ${error}`, 'error');
      document.getElementById(`mcp_${serverName}`).checked = !enabled;
    }
  } catch (error) {
    console.error('Toggle MCP server error:', error);
    showToast('Failed to update MCP server', 'error');
    document.getElementById(`mcp_${serverName}`).checked = !enabled;
  }
}

// Update MCP server configuration
async function updateMCPServerConfig(serverName, configKey, value) {
  // For now, just store the value - you can expand this to save to backend
  showToast(`${serverName} path updated`, 'info');

  // TODO: Add API call to save per-agent MCP server config
}

  // Show toast notification
  function showToast(message, type = 'info') {
    // Simple toast implementation - you can enhance this
  }

// Actions
function chatWithAgent() {
  // Switch to this agent and go to chat
  fetch(`/api/agents?name=${encodeURIComponent(agentName)}`, {
    method: 'PUT'
  })
    .then(response => {
      if (response.ok) {
        window.location.href = '/';
      }
    })
    .catch(error => {
      console.error('Error switching agent:', error);
      showError('Failed to switch to agent');
    });
}

async function confirmDelete() {
  if (!confirm(`Are you sure you want to delete agent "${agentName}"? This action cannot be undone.`)) {
    return;
  }

  try {
    const response = await fetch(`/api/agents?name=${encodeURIComponent(agentName)}`, {
      method: 'DELETE'
    });

    if (!response.ok) {
      throw new Error('Failed to delete agent');
    }

    alert(`Agent "${agentName}" deleted successfully`);
    window.location.href = '/agents';

  } catch (error) {
    console.error('Error deleting agent:', error);
    showError('Failed to delete agent');
  }
}

// Helper functions
function getAgentColor(agent) {
  if (agent.metadata?.avatar_color) {
    return agent.metadata.avatar_color;
  }
  // Generate color from name
  const hash = agent.name.split('').reduce((acc, char) => {
    return char.charCodeAt(0) + ((acc << 5) - acc);
  }, 0);
  const hue = hash % 360;
  return `hsl(${hue}, 60%, 50%)`;
}

function getAgentInitials(name) {
  const words = name.split(/[\s_-]+/);
  if (words.length >= 2) {
    return (words[0][0] + words[1][0]).toUpperCase();
  }
  return name.substring(0, 2).toUpperCase();
}

function capitalize(str) {
  if (!str) return '';
  return str.charAt(0).toUpperCase() + str.slice(1);
}

function formatNumber(num) {
  if (num >= 1000000) {
    return (num / 1000000).toFixed(1) + 'M';
  } else if (num >= 1000) {
    return (num / 1000).toFixed(1) + 'K';
  }
  return num.toString();
}

function formatFullDate(dateString) {
  if (!dateString) return 'Never';

  const date = new Date(dateString);
  const now = new Date();
  const diff = now - date;

  const minutes = Math.floor(diff / 60000);
  const hours = Math.floor(diff / 3600000);
  const days = Math.floor(diff / 86400000);

  if (minutes < 1) return 'Just now';
  if (minutes < 60) return `${minutes} minutes ago`;
  if (hours < 24) return `${hours} hours ago`;
  if (days < 7) return `${days} days ago`;

  return date.toLocaleString();
}

// escapeHtml is provided by dom-utils.js

function showLoading(show) {
  const loading = document.getElementById('loadingState');
  const content = document.getElementById('content');
  if (loading) loading.style.display = show ? 'flex' : 'none';
  if (content) content.style.display = show ? 'none' : 'block';
}

function showError(message) {
  alert('Error: ' + message);
}

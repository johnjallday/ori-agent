/**
 * Studios Agent Modal Management Module
 * Handles agent creation, deletion, and workspace assignment via modals
 */

// State for agent management (using shared variable from studios-workspace.js)
// studiosSystemAgents is declared in studios-workspace.js
let studiosAvailableProviders = [];
let studiosLLMAvailable = false;
let studiosSystemModelConfigured = false;
let studiosAutoConfigApplied = false;

function isStudiosAutoConfigFallback(config) {
  return Boolean(config && typeof config.reasoning === 'string' && config.reasoning.startsWith('Auto-config failed'));
}

/**
 * Load providers from API
 */
async function loadStudiosProviders() {
  try {
    const response = await fetch('/api/providers');
    const data = await response.json();
    studiosAvailableProviders = data.providers || [];
    return studiosAvailableProviders;
  } catch (error) {
    console.error('Failed to load providers:', error);
    return [];
  }
}

/**
 * Populate model select dropdown based on agent type
 */
function populateStudiosModelSelect(modelSelect, selectedType = 'tool-calling') {
  if (!modelSelect || studiosAvailableProviders.length === 0) {
    console.warn('Cannot populate models: modelSelect or providers missing');
    return;
  }

  modelSelect.innerHTML = '';

  studiosAvailableProviders.forEach(provider => {
    const providerGroup = document.createElement('optgroup');
    providerGroup.label = provider.display_name;

    provider.models.forEach(model => {
      const option = document.createElement('option');
      option.value = model.value;
      option.textContent = model.label;
      option.setAttribute('data-type', model.type);
      option.setAttribute('data-provider', model.provider);

      if (model.type !== selectedType) {
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

/**
 * Open the manage agents modal
 */
async function openManageAgentsModal() {
  const modal = new bootstrap.Modal(document.getElementById('manageAgentsModal'));
  modal.show();

  // Load agents when modal opens
  await loadStudiosSystemAgents();

  // Load workspaces for the dropdown
  await loadWorkspacesForAgentManagement();

  // Load and populate available models
  try {
    await loadStudiosProviders();
    const modelSelect = document.getElementById('studios-new-agent-model');
    const typeSelect = document.getElementById('studios-new-agent-type');
    if (modelSelect && typeSelect) {
      populateStudiosModelSelect(modelSelect, typeSelect.value);
    }
  } catch (error) {
    console.error('Error loading providers:', error);
  }
}

/**
 * Load system agents from API
 */
async function loadStudiosSystemAgents() {
  try {
    const response = await fetch('/api/agents');
    const data = await response.json();

    studiosSystemAgents = data.agents || [];
    renderStudiosAgentsListModal();
  } catch (error) {
    console.error('Error loading agents:', error);
    document.getElementById('studios-agents-list-modal').innerHTML = `
            <div class="alert alert-danger">Failed to load agents: ${escapeHtml(error.message)}</div>
        `;
  }
}

/**
 * Render agents list in modal
 */
function renderStudiosAgentsListModal() {
  const container = document.getElementById('studios-agents-list-modal');

  if (studiosSystemAgents.length === 0) {
    container.innerHTML = `
            <div class="text-center py-3">
                <p style="color: var(--text-muted);">No agents found</p>
            </div>
        `;
    return;
  }

  container.innerHTML = studiosSystemAgents.map(agent => `
        <div class="d-flex align-items-center justify-content-between p-2 mb-2" style="border-left: 3px solid var(--primary-color); background: var(--bg-secondary); border-radius: var(--radius-sm);">
            <div class="d-flex align-items-center gap-3">
                <div class="status-indicator status-online" style="width: 8px; height: 8px; border-radius: 50%; background: var(--success-color);"></div>
                <div>
                    <div style="color: var(--text-primary); font-weight: 500;">${escapeHtml(agent.name)}</div>
                    <div class="text-muted small">${escapeHtml(agent.type || 'tool-calling')}</div>
                </div>
            </div>
            <div class="d-flex gap-2">
                <button class="btn btn-sm btn-outline-primary" onclick="addAgentToSelectedWorkspace('${escapeHtml(agent.name)}')" title="Add to workspace">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                        <path d="M19,13H13V19H11V13H5V11H11V5H13V11H19V13Z"/>
                    </svg>
                    Add to Workspace
                </button>
                <button class="btn btn-sm btn-outline-danger" onclick="deleteStudiosSystemAgent('${escapeHtml(agent.name)}')" title="Delete agent">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                        <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
                    </svg>
                </button>
            </div>
        </div>
    `).join('');
}

/**
 * Delete a system agent
 */
async function deleteStudiosSystemAgent(agentName) {
  if (!confirm(`Are you sure you want to delete agent "${agentName}"? This action cannot be undone.`)) {
    return;
  }

  try {
    const response = await fetch(`/api/agents?name=${encodeURIComponent(agentName)}`, {
      method: 'DELETE'
    });

    if (!response.ok) {
      const error = await response.text();
      throw new Error(error || 'Failed to delete agent');
    }

    await loadStudiosSystemAgents();

    // Also refresh workspace agents list if the function exists
    if (typeof loadWorkspaceAgents === 'function') {
      await loadWorkspaceAgents();
    }

    alert('Agent deleted successfully: ' + agentName);
  } catch (error) {
    console.error('Error deleting agent:', error);
    alert('Failed to delete agent: ' + error.message);
  }
}

/**
 * Load workspaces for the agent management dropdown
 */
async function loadWorkspacesForAgentManagement() {
  try {
    const response = await fetch('/api/orchestration/workspace');
    const data = await response.json();

    const workspaces = data.workspaces || [];
    const select = document.getElementById('studios-workspace-select');

    if (workspaces.length === 0) {
      select.innerHTML = '<option value="">No workspaces available</option>';
      return;
    }

    select.innerHTML = '<option value="">-- Select a workspace --</option>' +
            workspaces.map(ws =>
              `<option value="${escapeHtml(ws.id)}">${escapeHtml(ws.name || ws.id)}</option>`
            ).join('');
  } catch (error) {
    console.error('Error loading workspaces:', error);
    const select = document.getElementById('studios-workspace-select');
    select.innerHTML = '<option value="">Error loading workspaces</option>';
  }
}

/**
 * Add an agent to the selected workspace
 */
async function addAgentToSelectedWorkspace(agentName) {
  const workspaceSelect = document.getElementById('studios-workspace-select');
  const workspaceId = workspaceSelect.value;

  if (!workspaceId) {
    alert('Please select a workspace first');
    workspaceSelect.focus();
    return;
  }

  try {
    const response = await fetch(`/api/studios/${encodeURIComponent(workspaceId)}/agents`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ agent_name: agentName })
    });

    if (!response.ok) {
      const error = await response.text();
      throw new Error(error || 'Failed to add agent to workspace');
    }

    await response.json();
    alert(`Agent "${agentName}" added to workspace successfully!`);

    // Refresh workspaces if the function exists
    if (typeof loadWorkspaces === 'function') {
      await loadWorkspaces();
    }
  } catch (error) {
    console.error('Error adding agent to workspace:', error);
    // Check if it's a conflict (agent already in workspace)
    if (error.message.includes('already in workspace') || error.message.includes('409')) {
      alert(`Agent "${agentName}" is already in this workspace`);
    } else {
      alert('Failed to add agent to workspace: ' + error.message);
    }
  }
}

/**
 * Check if any LLM provider is available for auto-config
 */
async function checkLLMAvailability() {
  try {
    const response = await fetch('/api/agents/auto-config/availability');
    const data = await response.json();
    studiosLLMAvailable = data.available;
    studiosSystemModelConfigured = data.system_model_configured || false;
    return data;
  } catch (error) {
    console.error('Failed to check LLM availability:', error);
    studiosLLMAvailable = false;
    studiosSystemModelConfigured = false;
    return { available: false, system_model_configured: false, message: 'Failed to check LLM availability' };
  }
}

/**
 * Handle config mode toggle
 */
function handleConfigModeChange(mode) {
  const autoConfigSection = document.getElementById('autoConfigSection');
  const llmWarning = document.getElementById('llmNotAvailableWarning');
  const llmWarningMessage = document.getElementById('llmWarningMessage');
  const configModeHelp = document.getElementById('configModeHelp');
  const autoSelectedIndicator = document.getElementById('autoSelectedIndicator');

  if (mode === 'auto') {
    configModeHelp.classList.remove('d-none');

    if (studiosLLMAvailable) {
      autoConfigSection.classList.remove('d-none');
      llmWarning.classList.add('d-none');
    } else {
      autoConfigSection.classList.add('d-none');
      llmWarning.classList.remove('d-none');
      // Update warning message based on what's missing
      if (llmWarningMessage) {
        if (!studiosSystemModelConfigured) {
          llmWarningMessage.textContent = 'Auto-config requires a System Model to be configured.';
        } else {
          llmWarningMessage.textContent = 'Auto-config requires an LLM provider. Please set up an API key or install Ollama.';
        }
      }
    }
  } else {
    // Manual mode
    autoConfigSection.classList.add('d-none');
    llmWarning.classList.add('d-none');
    configModeHelp.classList.add('d-none');
    autoSelectedIndicator.classList.add('d-none');
    studiosAutoConfigApplied = false;
  }
}

/**
 * Generate auto-config from description
 */
async function generateAutoConfig() {
  const description = document.getElementById('studios-auto-config-description').value.trim();
  const generateBtn = document.getElementById('generateAutoConfigBtn');
  const autoConfigStatus = document.getElementById('autoConfigStatus');

  if (!description) {
    alert('Please enter a description of what you want your agent to do.');
    return;
  }

  // Show loading state
  generateBtn.disabled = true;
  generateBtn.innerHTML = `
        <span class="spinner-border spinner-border-sm me-1" role="status"></span>
        Generating...
    `;
  autoConfigStatus.textContent = 'Analyzing...';
  autoConfigStatus.classList.remove('d-none', 'bg-success', 'bg-danger');
  autoConfigStatus.classList.add('bg-secondary');

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

    // Apply the configuration to form fields
    applyAutoConfig(config);

    const fallback = isStudiosAutoConfigFallback(config);
    // Show success status
    autoConfigStatus.textContent = fallback ? 'Applied (defaults)' : 'Applied!';
    autoConfigStatus.classList.remove('bg-secondary', 'bg-success', 'bg-danger', 'bg-warning');
    autoConfigStatus.classList.add(fallback ? 'bg-warning' : 'bg-success');

    // Show the auto-selected indicator
    document.getElementById('autoSelectedIndicator').classList.remove('d-none');
    studiosAutoConfigApplied = true;

    // Log reasoning if available
    if (fallback) {
      console.warn('Auto-config failed, using defaults.');
    }

  } catch (error) {
    console.error('Auto-config error:', error);
    autoConfigStatus.textContent = 'Failed';
    autoConfigStatus.classList.remove('bg-secondary');
    autoConfigStatus.classList.add('bg-danger');
    alert('Failed to generate configuration: ' + error.message);
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

/**
 * Apply auto-generated config to form fields
 */
function applyAutoConfig(config) {
  // Apply agent type
  const typeSelect = document.getElementById('studios-new-agent-type');
  if (typeSelect && config.agent_type) {
    typeSelect.value = config.agent_type;
    // Trigger change to update model list
    typeSelect.dispatchEvent(new Event('change'));
  }

  // Apply model (need to wait a moment for model list to repopulate)
  setTimeout(() => {
    const modelSelect = document.getElementById('studios-new-agent-model');
    if (modelSelect && config.model) {
      // Try to find and select the model
      for (let i = 0; i < modelSelect.options.length; i++) {
        if (modelSelect.options[i].value === config.model) {
          modelSelect.selectedIndex = i;
          break;
        }
      }
    }
  }, 100);

  // Apply temperature
  const tempSlider = document.getElementById('studios-new-agent-temperature');
  const tempValue = document.getElementById('studios-new-agent-temperature-value');
  if (tempSlider && config.temperature !== undefined) {
    tempSlider.value = config.temperature;
    if (tempValue) {
      tempValue.textContent = config.temperature.toFixed(1);
    }
  }

  // Apply system prompt
  const promptTextarea = document.getElementById('studios-new-agent-prompt');
  if (promptTextarea && config.system_prompt) {
    promptTextarea.value = config.system_prompt;
  }

  // Add visual feedback - briefly highlight the changed fields
  highlightAutoConfiguredFields();
}

/**
 * Briefly highlight fields that were auto-configured
 */
function highlightAutoConfiguredFields() {
  const fields = [
    'studios-new-agent-type',
    'studios-new-agent-model',
    'studios-new-agent-temperature',
    'studios-new-agent-prompt'
  ];

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

/**
 * Initialize agent modal event listeners
 */
function initializeAgentModalListeners() {
  // Config mode toggle listeners
  const configModeManual = document.getElementById('configModeManual');
  const configModeAuto = document.getElementById('configModeAuto');

  if (configModeManual) {
    configModeManual.addEventListener('change', function() {
      if (this.checked) handleConfigModeChange('manual');
    });
  }

  if (configModeAuto) {
    configModeAuto.addEventListener('change', async function() {
      if (this.checked) {
        // Check LLM availability when switching to auto mode
        await checkLLMAvailability();
        handleConfigModeChange('auto');
      }
    });
  }

  // Generate auto-config button
  const generateBtn = document.getElementById('generateAutoConfigBtn');
  if (generateBtn) {
    generateBtn.addEventListener('click', generateAutoConfig);
  }

  // Update model dropdown when agent type changes
  const typeSelect = document.getElementById('studios-new-agent-type');
  if (typeSelect) {
    typeSelect.addEventListener('change', function(e) {
      const modelSelect = document.getElementById('studios-new-agent-model');
      if (modelSelect && studiosAvailableProviders.length > 0) {
        populateStudiosModelSelect(modelSelect, e.target.value);
      }
    });
  }

  // Update temperature value display when slider changes
  const tempSlider = document.getElementById('studios-new-agent-temperature');
  if (tempSlider) {
    tempSlider.addEventListener('input', function(e) {
      const valueDisplay = document.getElementById('studios-new-agent-temperature-value');
      if (valueDisplay) {
        valueDisplay.textContent = e.target.value;
      }
    });
  }

  // Create agent form submission
  const createAgentForm = document.getElementById('studiosCreateAgentForm');
  if (createAgentForm) {
    createAgentForm.addEventListener('submit', async function(e) {
      e.preventDefault();

      const name = document.getElementById('studios-new-agent-name').value.trim();
      const type = document.getElementById('studios-new-agent-type').value;
      const modelSelectEl = document.getElementById('studios-new-agent-model');
      const model = modelSelectEl?.value?.trim() || '';
      const temperature = document.getElementById('studios-new-agent-temperature').value;
      const systemPrompt = document.getElementById('studios-new-agent-prompt').value.trim();
      const allowWebSearchInput = document.getElementById('studios-new-agent-allow-web-search');

      if (!name) {
        alert('Please enter an agent name');
        return;
      }

      // Check if agent already exists
      if (studiosSystemAgents.some(a => a.name === name)) {
        alert('An agent with this name already exists');
        return;
      }

      try {
        const requestBody = { name, type };
        requestBody.allow_web_search = allowWebSearchInput ? Boolean(allowWebSearchInput.checked) : true;

        if (model) {
          requestBody.model = model;
          const selectedModelOption = modelSelectEl?.selectedOptions?.[0];
          const selectedProvider = selectedModelOption?.getAttribute('data-provider');
          if (selectedProvider) {
            requestBody.llm_provider = selectedProvider;
          }
        }
        if (temperature) requestBody.temperature = parseFloat(temperature);
        if (systemPrompt) requestBody.system_prompt = systemPrompt;

        const response = await fetch('/api/agents', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(requestBody)
        });

        if (!response.ok) {
          const error = await response.text();
          throw new Error(error || 'Failed to create agent');
        }

        // Clear form and reload
        createAgentForm.reset();
        resetAutoConfigState();
        await loadStudiosSystemAgents();

        // Refresh workspace agents if function exists
        if (typeof loadWorkspaceAgents === 'function') {
          await loadWorkspaceAgents();
        }

        alert('Agent created successfully: ' + name);
      } catch (error) {
        console.error('Error creating agent:', error);
        alert('Failed to create agent: ' + error.message);
      }
    });
  }
}

// escapeHtml is provided by dom-utils.js

/**
 * Reset auto-config state (called when form is reset or modal closed)
 */
function resetAutoConfigState() {
  studiosAutoConfigApplied = false;

  const configModeManual = document.getElementById('configModeManual');
  if (configModeManual) {
    configModeManual.checked = true;
  }

  const autoConfigSection = document.getElementById('autoConfigSection');
  const llmWarning = document.getElementById('llmNotAvailableWarning');
  const configModeHelp = document.getElementById('configModeHelp');
  const autoSelectedIndicator = document.getElementById('autoSelectedIndicator');
  const autoConfigStatus = document.getElementById('autoConfigStatus');
  const descriptionTextarea = document.getElementById('studios-auto-config-description');
  const allowWebSearchInput = document.getElementById('studios-new-agent-allow-web-search');

  if (autoConfigSection) autoConfigSection.classList.add('d-none');
  if (llmWarning) llmWarning.classList.add('d-none');
  if (configModeHelp) configModeHelp.classList.add('d-none');
  if (autoSelectedIndicator) autoSelectedIndicator.classList.add('d-none');
  if (autoConfigStatus) autoConfigStatus.classList.add('d-none');
  if (descriptionTextarea) descriptionTextarea.value = '';
  if (allowWebSearchInput) allowWebSearchInput.checked = true;
}

// Export functions for global access
window.openManageAgentsModal = openManageAgentsModal;
window.loadStudiosSystemAgents = loadStudiosSystemAgents;
window.deleteStudiosSystemAgent = deleteStudiosSystemAgent;
window.addAgentToSelectedWorkspace = addAgentToSelectedWorkspace;
window.loadStudiosProviders = loadStudiosProviders;
window.populateStudiosModelSelect = populateStudiosModelSelect;
window.checkLLMAvailability = checkLLMAvailability;
window.generateAutoConfig = generateAutoConfig;
window.resetAutoConfigState = resetAutoConfigState;

// Initialize on DOM ready
document.addEventListener('DOMContentLoaded', initializeAgentModalListeners);

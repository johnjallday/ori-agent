// Agent Management Module
// Handles all agent-related functionality including CRUD operations and UI management

// Agent state management
let allAgents = [];
let currentAgentName = '';
let visibleAgentCount = 3;
let availableProviders = []; // Cache for available providers and models

// Fetch available providers and models from API
async function loadAvailableProviders() {
  try {
    const response = await fetch('/api/providers');
    const data = await response.json();
    availableProviders = data.providers || [];
    return availableProviders;
  } catch (error) {
    console.error('Failed to load providers:', error);
    return [];
  }
}

// Populate model select with options from available providers
function populateModelSelect(modelSelect, selectedType = 'tool-calling') {
  if (!modelSelect) return;

  // Only populate if we have providers loaded, otherwise keep existing options
  if (availableProviders.length === 0) {
    // Fallback: just filter existing hardcoded options by type
    const allOptions = modelSelect.querySelectorAll('option');
    allOptions.forEach(option => {
      const optionType = option.getAttribute('data-type');
      if (optionType === selectedType || !optionType) {
        option.style.display = '';
        option.disabled = false;
      } else {
        option.style.display = 'none';
        option.disabled = true;
      }
    });
    return;
  }

  // Clear existing options only if we have providers to replace them with
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

      // Only show models matching the selected type
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

// Agent Management Functions
function selectAgent(agentName) {
  console.log('Selecting agent:', agentName);
  currentAgent = agentName;
  // Update UI to reflect selected agent
  document.querySelectorAll('.agent-item').forEach(item => {
    item.style.background = 'var(--bg-secondary)';
  });
  event.target.closest('.agent-item').style.background = 'var(--primary-color-light)';
}

// Show add agent modal
async function showAddAgentModal() {
  const modal = new bootstrap.Modal(document.getElementById('addAgentModal'));
  const agentNameInput = document.getElementById('agentName');
  const agentTypeInput = document.getElementById('agentType');
  const agentSystemPromptInput = document.getElementById('agentSystemPrompt');
  const agentModelInput = document.getElementById('agentModel');
  const agentTemperatureInput = document.getElementById('agentTemperature');
  const temperatureValueSpan = document.getElementById('temperatureValue');

  // Load providers if not already loaded
  if (availableProviders.length === 0) {
    await loadAvailableProviders();
  }

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
    // Populate model select with dynamic providers
    populateModelSelect(agentModelInput, 'tool-calling');
  }
  if (agentTemperatureInput) {
    agentTemperatureInput.value = '1.0';
    if (temperatureValueSpan) {
      temperatureValueSpan.textContent = '1.0';
    }
  }

  modal.show();

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

// Create new agent
async function createNewAgent() {
  const agentNameInput = document.getElementById('agentName');
  const agentTypeInput = document.getElementById('agentType');
  const agentSystemPromptInput = document.getElementById('agentSystemPrompt');
  const agentModelInput = document.getElementById('agentModel');
  const agentTemperatureInput = document.getElementById('agentTemperature');
  const createBtn = document.getElementById('createAgentBtn');

  if (!agentNameInput) return;

  const agentName = agentNameInput.value.trim();
  if (!agentName) {
    alert('Please enter an agent name');
    agentNameInput.focus();
    return;
  }

  // Set loading state
  const originalText = createBtn.textContent;
  createBtn.disabled = true;
  createBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-2" role="status"></span>Creating...';

  try {
    const requestBody = { name: agentName };

    // Add agent type if provided
    if (agentTypeInput && agentTypeInput.value) {
      requestBody.type = agentTypeInput.value;
    }

    // Add model if provided
    if (agentModelInput && agentModelInput.value) {
      requestBody.model = agentModelInput.value;
    }

    // Add temperature if provided
    if (agentTemperatureInput && agentTemperatureInput.value) {
      requestBody.temperature = parseFloat(agentTemperatureInput.value);
    }

    // Add system prompt if provided
    if (agentSystemPromptInput && agentSystemPromptInput.value.trim()) {
      requestBody.system_prompt = agentSystemPromptInput.value.trim();
    }

    const response = await fetch('/api/agents', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(requestBody)
    });

    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
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
      agentModelInput.value = 'gpt-5-nano';
    }
    if (agentTemperatureInput) {
      agentTemperatureInput.value = '1.0';
    }

    // Show success message
    console.log('✅ Agent created successfully:', agentName);

    // Refresh the agent list
    console.log('🔄 Refreshing agent list...');
    await refreshAgentList();
    console.log('✅ Agent list refreshed');

    // Force page reload to ensure UI updates
    console.log('🔄 Reloading page to show new agent...');
    window.location.reload();

  } catch (error) {
    console.error('Error creating agent:', error);
    alert(`Failed to create agent: ${error.message}`);
  } finally {
    // Reset button state
    createBtn.disabled = false;
    createBtn.innerHTML = originalText;
  }
}

// Load and display agents
async function loadAgents() {
  console.log('📡 Loading agents from /api/agents...');
  try {
    const response = await fetch('/api/agents');
    console.log(`📊 Response: status=${response.status}, ok=${response.ok}`);
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }

    const data = await response.json();
    console.log(`📦 Received agents:`, data);
    console.log(`👥 Agent count: ${data.agents?.length || 0}, Current: ${data.current}`);
    displayAgents(data.agents, data.current);
    console.log('✅ Agents displayed');

  } catch (error) {
    console.error('❌ Error loading agents:', error);
    const agentsList = document.getElementById('agentsList');
    if (agentsList) {
      agentsList.innerHTML = '<div class="text-muted small p-2">Failed to load agents</div>';
    }
  }
}

// Display agents in the sidebar with pagination
function displayAgents(agents, currentAgent) {
  console.log(`🎨 displayAgents called with ${agents?.length || 0} agents`);
  const agentsList = document.getElementById('agentsList');
  if (!agentsList) {
    console.warn('⚠️ agentsList element not found!');
    return;
  }

  // Store the data for pagination
  allAgents = agents;
  currentAgentName = currentAgent;
  console.log(`📋 Stored agents: ${allAgents?.length || 0}, current: ${currentAgentName}`);

  renderAgents();
}

function renderAgents() {
  console.log(`🖼️ renderAgents called, total agents: ${allAgents?.length || 0}, visible count: ${visibleAgentCount}`);
  const agentsList = document.getElementById('agentsList');
  if (!agentsList) {
    console.warn('⚠️ agentsList element not found in renderAgents!');
    return;
  }

  // Clear existing agents
  console.log('🗑️ Clearing existing agents...');
  agentsList.innerHTML = '';

  // Show only the first 'visibleAgentCount' agents
  const agentsToShow = allAgents.slice(0, visibleAgentCount);
  console.log(`📋 Rendering ${agentsToShow.length} agents:`, agentsToShow);

  // Add each visible agent
  agentsToShow.forEach(agentName => {
    console.log(`➕ Adding agent: ${agentName}`);
    const agentItem = createAgentElement(agentName, currentAgentName);
    agentsList.appendChild(agentItem);
  });

  // Add pagination buttons
  if (allAgents.length > 3) {
    const paginationBtn = document.createElement('div');
    paginationBtn.className = 'agent-pagination';

    if (visibleAgentCount < allAgents.length) {
      // Show "Load More" button
      paginationBtn.innerHTML = `
        <button class="btn btn-sm text-muted w-100 mt-2" style="border: 1px dashed var(--border-color); background: transparent; color: var(--text-secondary);" onclick="loadMoreAgents()">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
            <path d="M19,13H13V19H11V13H5V11H11V5H13V11H19V13Z"/>
          </svg>
          Load More (${allAgents.length - visibleAgentCount} more)
        </button>
      `;
    } else {
      // Show "Hide" button
      paginationBtn.innerHTML = `
        <button class="btn btn-sm text-muted w-100 mt-2" style="border: 1px dashed var(--border-color); background: transparent; color: var(--text-secondary);" onclick="hideAgents()">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
            <path d="M19,13H5V11H19V13Z"/>
          </svg>
          Hide (show only 3)
        </button>
      `;
    }

    agentsList.appendChild(paginationBtn);
  }

  // Setup accordion listeners after rendering
  setupAccordionListeners();

  // Load settings for the current agent accordion when it's expanded
  agentsToShow.forEach(agentName => {
    const accordionId = `agent-${agentName.replace(/\s+/g, '-')}`;
    const collapseElement = document.getElementById(`collapse-${accordionId}`);

    if (collapseElement) {
      collapseElement.addEventListener('shown.bs.collapse', async function () {
        await loadAgentSettings(agentName, accordionId);
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

// Create agent element with accordion
function createAgentElement(agentName, currentAgent) {
  const isCurrentAgent = agentName === currentAgent;
  const accordionId = `agent-${agentName.replace(/\s+/g, '-')}`;

  const agentDiv = document.createElement('div');
  agentDiv.className = 'accordion-item mb-2';
  agentDiv.style.background = 'var(--bg-secondary)';
  agentDiv.style.border = `1px solid var(--border-color)`;
  agentDiv.style.borderRadius = '8px';
  agentDiv.style.overflow = 'hidden';

  agentDiv.innerHTML = `
    <div class="accordion-header" id="heading-${accordionId}">
      <div class="d-flex align-items-center justify-content-between p-2" style="background: ${isCurrentAgent ? 'var(--primary-color-light)' : 'var(--bg-secondary)'};">
        <div class="d-flex align-items-center gap-2 flex-grow-1">
          <button class="accordion-button collapsed p-0 bg-transparent border-0 shadow-none"
                  type="button"
                  data-bs-toggle="collapse"
                  data-bs-target="#collapse-${accordionId}"
                  aria-expanded="false"
                  aria-controls="collapse-${accordionId}"
                  style="color: var(--text-primary); width: 20px; height: 20px;">
          </button>
          ${isCurrentAgent ? '<div class="status-indicator status-online"></div>' : ''}
          <span style="color: var(--text-primary); font-weight: 500;">${agentName}</span>
        </div>
        <div class="agent-actions d-flex align-items-center gap-2">
          ${!isCurrentAgent ? `<button class="modern-btn modern-btn-secondary px-2 py-1" onclick="event.stopPropagation(); switchToAgent('${agentName}')" title="Switch to this agent" style="font-size: 0.75rem;">
            Load
          </button>` : ''}
          <button class="btn btn-sm btn-link p-1" onclick="event.stopPropagation(); deleteAgent('${agentName}')" title="Delete agent">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M9,3V4H4V6H5V19A2,2 0 0,0 7,21H17A2,2 0 0,0 19,19V6H20V4H15V3H9M7,6H17V19H7V6M9,8V17H11V8H9M13,8V17H15V8H13Z"/>
            </svg>
          </button>
        </div>
      </div>
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
          <div class="d-flex align-items-center justify-content-between">
            <span style="color: var(--text-primary); font-size: 0.85rem;">Model</span>
            <select id="gptModelSelect-${accordionId}" class="form-select form-select-sm" style="width: auto; min-width: 180px; background: var(--bg-primary); border: 1px solid var(--border-color); color: var(--text-primary); font-size: 0.85rem;">
              <optgroup label="Cheap Models (Recommended for Agents)">
                <option value="gpt-5-nano">GPT-5 Nano</option>
                <option value="gpt-4.1-nano">GPT-4.1 Nano</option>
                <option value="claude-3-haiku-20240307">Claude 3 Haiku</option>
              </optgroup>
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

        <button id="updateSettingsBtn-${accordionId}" class="modern-btn modern-btn-primary w-100" onclick="updateAgentSettings('${agentName}', '${accordionId}')">
          Update Settings
        </button>
      </div>
    </div>
  `;

  return agentDiv;
}

// Switch to agent
async function switchToAgent(agentName) {
  try {
    const response = await fetch(`/api/agents?name=${encodeURIComponent(agentName)}`, {
      method: 'PUT'
    });
    
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }
    
    // Show success notification
    if (typeof showNotification === 'function') {
      showNotification(`Switched to agent: ${agentName}`, 'success');
    }
    
    // Refresh the agent list to update current agent
    await loadAgents();
    
    // Reload plugins for the new agent
    if (typeof loadPlugins === 'function') {
      await loadPlugins();
    }
    
    // Reload settings for the new agent
    if (typeof loadSettings === 'function') {
      await loadSettings();
    }
    
    console.log('Switched to agent:', agentName);
    
  } catch (error) {
    console.error('Error switching agent:', error);
    if (typeof showNotification === 'function') {
      showNotification(`Failed to switch to agent: ${agentName}`, 'error');
    }
  }
}

// Delete agent
async function deleteAgent(agentName) {
  if (!confirm(`Are you sure you want to delete agent "${agentName}"?`)) {
    return;
  }
  
  try {
    const response = await fetch(`/api/agents?name=${encodeURIComponent(agentName)}`, {
      method: 'DELETE'
    });
    
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }
    
    // Refresh the agent list
    await loadAgents();
    
    console.log('Deleted agent:', agentName);
    
  } catch (error) {
    console.error('Error deleting agent:', error);
    alert(`Failed to delete agent: ${error.message}`);
  }
}

// Refresh agent list
async function refreshAgentList() {
  await loadAgents();
}

// Setup agent management event listeners
function setupAgentManagement() {
  // Agent management buttons
  const addAgentBtn = document.getElementById('addAgentBtn');
  if (addAgentBtn) {
    addAgentBtn.addEventListener('click', () => {
      console.log('Add agent clicked');
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
    addAgentForm.addEventListener('submit', (e) => {
      e.preventDefault();
      createNewAgent();
    });
  }

  const loadMoreAgentsBtn = document.getElementById('loadMoreAgentsBtn');
  if (loadMoreAgentsBtn) {
    loadMoreAgentsBtn.addEventListener('click', () => {
      console.log('Load more agents clicked');
      loadAgents(); // Reload all agents (for now, until pagination is implemented)
    });
  }

  // Temperature slider update
  const agentTemperatureInput = document.getElementById('agentTemperature');
  const temperatureValueSpan = document.getElementById('temperatureValue');
  if (agentTemperatureInput && temperatureValueSpan) {
    agentTemperatureInput.addEventListener('input', (e) => {
      temperatureValueSpan.textContent = e.target.value;
    });
  }

  // Agent type selector update - filter models when type changes
  const agentTypeInput = document.getElementById('agentType');
  const agentModelInput = document.getElementById('agentModel');
  if (agentTypeInput && agentModelInput) {
    agentTypeInput.addEventListener('change', (e) => {
      filterModelsByType(e.target.value, agentModelInput);
    });
  }

  console.log('Agent management setup complete');
}

// Update agent settings from accordion
async function updateAgentSettings(agentName, accordionId) {
  try {
    const modelSelect = document.getElementById(`gptModelSelect-${accordionId}`);
    const temperatureSlider = document.getElementById(`temperatureSlider-${accordionId}`);
    const systemPromptInput = document.getElementById(`systemPromptInput-${accordionId}`);

    if (!modelSelect || !temperatureSlider) {
      console.error('Settings elements not found for agent:', agentName);
      return;
    }

    const settingsData = {
      model: modelSelect.value,
      temperature: parseFloat(temperatureSlider.value)
    };

    // Add system prompt if it exists
    if (systemPromptInput) {
      settingsData.system_prompt = systemPromptInput.value;
    }

    const response = await fetch(`/api/settings?agent=${encodeURIComponent(agentName)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(settingsData)
    });

    if (response.ok) {
      console.log('Settings updated for agent:', agentName, settingsData);

      // Show success notification
      if (typeof showNotification === 'function') {
        showNotification(`Settings updated for ${agentName}!`, 'success');
      }
    } else {
      console.error('Failed to save settings:', response.status);
      if (typeof showNotification === 'function') {
        showNotification('Failed to save settings', 'error');
      }
    }
  } catch (error) {
    console.error('Error saving settings:', error);
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

    if (temperatureValue) {
      slider.addEventListener('input', function(e) {
        temperatureValue.textContent = parseFloat(e.target.value).toFixed(1);
      });
    }
  });

  // Add model change listener for GPT-5 temperature restriction
  document.querySelectorAll('[id^="gptModelSelect-"]').forEach(modelSelect => {
    const accordionId = modelSelect.id.replace('gptModelSelect-', '');
    const temperatureSlider = document.getElementById(`temperatureSlider-${accordionId}`);
    const temperatureValue = document.getElementById(`temperatureValue-${accordionId}`);

    modelSelect.addEventListener('change', function() {
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
async function loadAgentSettings(agentName, accordionId) {
  try {
    const response = await fetch(`/api/settings?agent=${encodeURIComponent(agentName)}`);
    if (response.ok) {
      const settings = await response.json();

      // Update model dropdown
      const modelSelect = document.getElementById(`gptModelSelect-${accordionId}`);
      const modelValue = (settings.Settings && settings.Settings.model) || settings.model;
      if (modelSelect && modelValue) {
        modelSelect.value = modelValue;
      }

      // Update temperature slider
      const temperatureSlider = document.getElementById(`temperatureSlider-${accordionId}`);
      const temperatureValue = document.getElementById(`temperatureValue-${accordionId}`);
      let temperatureValueData = (settings.Settings && typeof settings.Settings.temperature !== 'undefined')
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
      const systemPromptValue = (settings.Settings && settings.Settings.system_prompt) || settings.system_prompt || '';
      if (systemPromptInput) {
        systemPromptInput.value = systemPromptValue;
      }
    }
  } catch (error) {
    console.error('Error loading settings for agent:', agentName, error);
  }
}

// Initialize agent management when DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', setupAgentManagement);
} else {
  setupAgentManagement();
}
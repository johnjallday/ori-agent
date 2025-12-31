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
  if (!modelSelect || availableProviders.length === 0) return;

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

// Initialize models on page load
async function initializeModels() {
  await loadAvailableProviders();

  // Populate the model select in the create agent modal
  const agentModelSelect = document.getElementById('agentModel');
  if (agentModelSelect) {
    populateModelSelect(agentModelSelect, 'tool-calling');
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
      patternMessage: 'Use letters, numbers, hyphens, and underscores. Must start with a letter or number.'
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
  console.log('Selecting agent:', agentName);
  currentAgent = agentName;
  // Update UI to reflect selected agent
  document.querySelectorAll('.agent-item').forEach(item => {
    item.style.background = 'var(--bg-secondary)';
  });
  event.target.closest('.agent-item').style.background = 'var(--primary-color-light)';
}

// Show add agent modal
function showAddAgentModal() {
  const modal = new bootstrap.Modal(document.getElementById('addAgentModal'));
  const agentNameInput = document.getElementById('agentName');
  const agentTypeInput = document.getElementById('agentType');
  const agentSystemPromptInput = document.getElementById('agentSystemPrompt');
  const agentModelInput = document.getElementById('agentModel');
  const agentTemperatureInput = document.getElementById('agentTemperature');
  const temperatureValueSpan = document.getElementById('temperatureValue');

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
    if (window.Toast) {
      Toast.warning('Please enter an agent name', { title: 'Missing Name' });
    }
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
      // Select first available tool-calling model
      const firstToolCallingOption = agentModelInput.querySelector('option[data-type="tool-calling"]:not([disabled])');
      if (firstToolCallingOption) {
        agentModelInput.value = firstToolCallingOption.value;
      }
    }
    if (agentTemperatureInput) {
      agentTemperatureInput.value = '1.0';
    }

    // Show success message
    console.log('✅ Agent created successfully:', agentName);
    if (window.Toast) {
      Toast.success(`Agent "${agentName}" created successfully`);
    }

    // Refresh the agent list
    console.log('🔄 Refreshing agent list...');
    await refreshAgentList();
    console.log('✅ Agent list refreshed');

    // Force page reload to ensure UI updates
    console.log('🔄 Reloading page to show new agent...');
    window.location.reload();

  } catch (error) {
    console.error('Error creating agent:', error);
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
    displayAgents(data.agents, resolveSidebarCurrentAgent(data.current));
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

  // Sort agents: current/active agent first, then alphabetically
  const sortedAgents = [...agents].sort((a, b) => {
    const nameA = typeof a === 'string' ? a : a.name;
    const nameB = typeof b === 'string' ? b : b.name;

    // Current agent comes first
    if (nameA === currentAgent) return -1;
    if (nameB === currentAgent) return 1;

    // Then sort alphabetically
    return nameA.localeCompare(nameB);
  });

  // Store the data for pagination
  allAgents = sortedAgents;
  currentAgentName = currentAgent;
  console.log(`📋 Stored agents: ${allAgents?.length || 0}, current: ${currentAgentName}`);

  renderAgents();
}

function resolveSidebarCurrentAgent(fallbackAgent) {
  const sessionAgent = window.sessionManager?.getActiveSession?.()?.agent_name;
  return sessionAgent || fallbackAgent;
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
  agentsToShow.forEach(agent => {
    // Handle both old format (string) and new format (object with name and type)
    const agentName = typeof agent === 'string' ? agent : agent.name;
    const agentType = typeof agent === 'string' ? 'tool-calling' : (agent.type || 'tool-calling');
    console.log(`➕ Adding agent: ${agentName} (type: ${agentType})`);
    const agentItem = createAgentElement(agentName, agentType, currentAgentName);
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

  // Load settings for the current agent accordion when it's expanded
  agentsToShow.forEach(agent => {
    const agentName = typeof agent === 'string' ? agent : agent.name;
    const agentType = typeof agent === 'string' ? 'tool-calling' : (agent.type || 'tool-calling');
    const accordionId = `agent-${agentName.replace(/\s+/g, '-')}`;
    const collapseElement = document.getElementById(`collapse-${accordionId}`);

    if (collapseElement) {
      collapseElement.addEventListener('shown.bs.collapse', async function () {
        await loadAgentSettings(agentName, agentType, accordionId);
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
function createAgentElement(agentName, agentType, currentAgent) {
  const isCurrentAgent = agentName === currentAgent;
  const accordionId = `agent-${agentName.replace(/\s+/g, '-')}`;

  // Format type label
  const typeLabels = {
    'tool-calling': 'Tool Calling',
    'general': 'General',
    'research': 'Research'
  };
  const typeLabel = typeLabels[agentType] || agentType;

  const agentDiv = document.createElement('div');
  agentDiv.className = 'accordion-item mb-2';
  agentDiv.style.background = 'var(--bg-secondary)';
  agentDiv.style.border = `1px solid var(--border-color)`;
  agentDiv.style.borderRadius = '8px';
  agentDiv.style.overflow = 'hidden';

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
          ${isCurrentAgent ? '<div class="status-indicator status-online"></div>' : ''}
          <div class="d-flex flex-column">
            <span style="color: var(--text-primary); font-weight: 500;">${agentName}</span>
            <span style="color: var(--text-secondary); font-size: 0.7rem;">${typeLabel}</span>
          </div>
        </div>
        <div class="agent-actions d-flex align-items-center gap-2">
          ${!isCurrentAgent ? `<span class="modern-btn modern-btn-secondary px-2 py-1" onclick="event.stopPropagation(); switchToAgent('${agentName}')" title="Switch to this agent" style="font-size: 0.75rem; cursor: pointer;">
            Load
          </span>` : ''}
          <span class="btn btn-sm btn-link p-1" onclick="event.stopPropagation(); deleteAgent('${agentName}')" title="Delete agent" style="cursor: pointer;">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M9,3V4H4V6H5V19A2,2 0 0,0 7,21H17A2,2 0 0,0 19,19V6H20V4H15V3H9M7,6H17V19H7V6M9,8V17H11V8H9M13,8V17H15V8H13Z"/>
            </svg>
          </span>
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
                   value="${agentName}"
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
                <path d="M20,2H4A2,2 0 0,0 2,4V22L6,18H20A2,2 0 0,0 22,16V4A2,2 0 0,0 20,2M6,9H18V11H6M14,14H6V12H14M18,8H6V6H18"/>
              </svg>
              MCP Servers
            </h6>
            <div id="mcpServersList-${accordionId}" style="font-size: 0.85rem;">
              <div class="text-muted small">Loading MCP servers...</div>
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

// Switch to agent
async function switchToAgent(agentName) {
  try {
    const activeSession = window.sessionManager?.getActiveSession?.();
    if (activeSession && window.sessionManager?.changeSessionAgent) {
      await window.sessionManager.changeSessionAgent(activeSession.id, agentName);
    } else {
      const response = await fetch(`/api/agents?name=${encodeURIComponent(agentName)}`, {
        method: 'PUT'
      });

      if (!response.ok) {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`);
      }
    }
    
    // Show success notification
    if (typeof showNotification === 'function') {
      showNotification(`Switched to agent: ${agentName}`, 'success');
    }
    
    // Refresh the agent list to update current agent
    await loadAgentsForSidebar();
    
    // Reload plugins for the new agent
    if (typeof loadPlugins === 'function') {
      await loadPlugins();
    } else if (typeof loadPluginsForSidebar === 'function') {
      await loadPluginsForSidebar();
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
    await loadAgentsForSidebar();

    console.log('Deleted agent:', agentName);
    if (window.Toast) {
      Toast.success(`Agent "${agentName}" deleted`);
    }

  } catch (error) {
    console.error('Error deleting agent:', error);
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
      loadAgentsForSidebar(); // Reload all agents (for now, until pagination is implemented)
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

  // Auto-config mode toggle listeners
  setupAutoConfigListeners();

  console.log('Agent management setup complete');
}

// State for auto-config
let baseAutoConfigApplied = false;
let baseLLMAvailable = false;
let baseSystemModelConfigured = false;

function isBaseAutoConfigFallback(config) {
  return Boolean(config && typeof config.reasoning === 'string' && config.reasoning.startsWith('Auto-config failed'));
}

// Setup auto-config event listeners
function setupAutoConfigListeners() {
  const configModeManual = document.getElementById('baseConfigModeManual');
  const configModeAuto = document.getElementById('baseConfigModeAuto');

  if (configModeManual) {
    configModeManual.addEventListener('change', function() {
      if (this.checked) handleBaseConfigModeChange('manual');
    });
  }

  if (configModeAuto) {
    configModeAuto.addEventListener('change', async function() {
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
    const response = await fetch('/api/agents/auto-config/availability');
    const data = await response.json();
    baseLLMAvailable = data.available;
    baseSystemModelConfigured = data.system_model_configured || false;
    return data;
  } catch (error) {
    console.error('Failed to check LLM availability:', error);
    baseLLMAvailable = false;
    baseSystemModelConfigured = false;
    return { available: false, system_model_configured: false, message: 'Failed to check LLM availability' };
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
          llmWarningMessage.textContent = 'Auto-config requires an LLM provider. Please set up an API key or install Ollama.';
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
      Toast.warning('Please describe what you want your agent to do', { title: 'Missing Description' });
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
      console.log('Auto-config reasoning:', config.reasoning);
    }
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
      console.error('Settings elements not found for agent:', agentName);
      return;
    }

    const newAgentName = agentNameInput ? agentNameInput.value.trim() : agentName;
    const newAgentType = agentTypeSelect ? agentTypeSelect.value : 'tool-calling';

    // If agent name changed, we need to rename the agent first
    if (newAgentName !== agentName) {
      const renameResponse = await fetch(`/api/agents/${encodeURIComponent(agentName)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          new_name: newAgentName,
          type: newAgentType
        })
      });

      if (!renameResponse.ok) {
        console.error('Failed to rename agent:', renameResponse.status);
        if (typeof showNotification === 'function') {
          showNotification('Failed to rename agent', 'error');
        }
        return;
      }
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

    const response = await fetch(`/api/settings?agent=${encodeURIComponent(newAgentName)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(settingsData)
    });

    if (response.ok) {
      console.log('Settings updated for agent:', newAgentName, settingsData);

      // Show success notification
      if (typeof showNotification === 'function') {
        showNotification(`Settings updated for ${newAgentName}!`, 'success');
      }

      // If name changed, reload agents list to reflect the change
      if (newAgentName !== agentName) {
        await loadAgentsForSidebar();
        // If this was the current agent, switch to the new name
        if (currentAgentName === agentName) {
          switchToAgent(newAgentName);
        }
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
    const modelSelect = document.getElementById(`gptModelSelect-${accordionId}`);

    if (temperatureValue) {
      slider.addEventListener('input', function(e) {
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

    const response = await fetch(`/api/settings?agent=${encodeURIComponent(agentName)}`);
    if (response.ok) {
      const settings = await response.json();

      // Update model dropdown with current value
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

    // Load MCP servers for this agent
    await loadAgentMCPServers(agentName, accordionId);

  } catch (error) {
    console.error('Error loading settings for agent:', agentName, error);
  }
}

// Load MCP servers for a specific agent
async function loadAgentMCPServers(agentName, accordionId) {
  const mcpServersList = document.getElementById(`mcpServersList-${accordionId}`);
  if (!mcpServersList) return;

  try {
    const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}/mcp-servers`);
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }

    const data = await response.json();
    const servers = data.servers || [];

    if (servers.length === 0) {
      mcpServersList.innerHTML = '<div class="text-muted small">No MCP servers configured</div>';
      return;
    }

    // Render MCP servers list
    let html = '';
    servers.forEach(server => {
      const statusColor = server.status === 'running' ? 'success' :
                         server.status === 'error' ? 'danger' : 'secondary';
      const statusIcon = server.status === 'running' ? '●' : '○';

      html += `
        <div class="d-flex align-items-center justify-content-between mb-2 p-2"
             style="background: var(--bg-primary); border: 1px solid var(--border-color); border-radius: 6px;">
          <div class="d-flex flex-column flex-grow-1">
            <div class="d-flex align-items-center gap-2">
              <span class="text-${statusColor}" style="font-size: 0.6rem;">${statusIcon}</span>
              <span style="color: var(--text-primary); font-weight: 500;">${server.name}</span>
              ${server.tool_count > 0 ? `<span class="badge bg-secondary" style="font-size: 0.65rem;">${server.tool_count} tools</span>` : ''}
            </div>
            ${server.description ? `<span class="text-muted small mt-1">${server.description}</span>` : ''}
          </div>
          <div class="form-check form-switch mb-0">
            <input class="form-check-input" type="checkbox" role="switch"
                   id="mcp-${server.name}-${accordionId}"
                   ${server.enabled ? 'checked' : ''}
                   onchange="toggleMCPServer('${agentName}', '${server.name}', this.checked, '${accordionId}')"
                   style="cursor: pointer;">
          </div>
        </div>
      `;
    });

    mcpServersList.innerHTML = html;

  } catch (error) {
    console.error('Error loading MCP servers for agent:', agentName, error);
    mcpServersList.innerHTML = '<div class="text-danger small">Failed to load MCP servers</div>';
  }
}

// Toggle MCP server for an agent
async function toggleMCPServer(agentName, serverName, enabled, accordionId) {
  const checkbox = document.getElementById(`mcp-${serverName}-${accordionId}`);
  const originalState = !enabled; // Store original state for rollback

  try {
    const endpoint = enabled ? 'enable' : 'disable';
    const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}/mcp-servers/${serverName}/${endpoint}`, {
      method: 'POST'
    });

    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }

    const data = await response.json();

    // Show success notification
    if (typeof showNotification === 'function') {
      showNotification(data.message || `MCP server ${enabled ? 'enabled' : 'disabled'}`, 'success');
    }

    // Reload MCP servers list to update status and tool count
    await loadAgentMCPServers(agentName, accordionId);

  } catch (error) {
    console.error('Error toggling MCP server:', error);

    // Rollback checkbox state on error
    if (checkbox) {
      checkbox.checked = originalState;
    }

    // Show error notification
    if (typeof showNotification === 'function') {
      showNotification(`Failed to ${enabled ? 'enable' : 'disable'} MCP server: ${error.message}`, 'error');
    }
  }
}

// Initialize agent management when DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', setupAgentManagement);
} else {
  setupAgentManagement();
}

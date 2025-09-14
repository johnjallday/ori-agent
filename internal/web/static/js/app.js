// Dolphin Agent Application JavaScript

let currentAgent = '';
let isComposing = false; // IME safety
let isWaitingForResponse = false; // Chat state

// Prompt history for up/down arrow navigation
let promptHistory = [];
let historyIndex = -1;

// Chat messages storage
let chatMessages = [];

// ---- Dark Mode (Bootstrap + custom) ----
function applyTheme(isDark) {
  // Bootstrap theming
  document.documentElement.setAttribute('data-bs-theme', isDark ? 'dark' : 'light');
  // Your extra overrides
  document.documentElement.classList.toggle('dark-mode', isDark);
  // Persist
  localStorage.setItem('darkMode', String(isDark));
}

// Setup dark mode functionality
function setupDarkMode() {
  // Init theme from storage (default light)
  const storedDark = localStorage.getItem('darkMode') === 'true';
  applyTheme(storedDark);

  // Toggle button
  const darkModeToggle = document.getElementById('darkModeToggle');
  if (darkModeToggle) {
    darkModeToggle.addEventListener('click', () => {
      const next = !(localStorage.getItem('darkMode') === 'true');
      applyTheme(next);
    });
  }
}

// ---- Chat Functionality ----

// Add message to chat area
function addMessageToChat(message, isUser = false, isError = false) {
  const chatArea = document.getElementById('chatArea');
  if (!chatArea) return;

  const messageDiv = document.createElement('div');
  messageDiv.className = `message-container mb-3 ${isUser ? 'user-message' : 'assistant-message'}`;
  
  const messageContent = document.createElement('div');
  messageContent.className = `modern-card p-3 ${isUser ? 'ms-auto' : 'me-auto'}`;
  messageContent.style.maxWidth = '85%';
  
  if (isError) {
    messageContent.style.background = 'var(--danger-color)';
    messageContent.style.color = 'white';
  } else if (isUser) {
    messageContent.style.background = 'var(--primary-color)';
    messageContent.style.color = 'white';
  } else {
    messageContent.style.background = 'var(--bg-secondary)';
    messageContent.style.color = 'var(--text-primary)';
  }

  // Process message content (support markdown)
  if (typeof marked !== 'undefined' && !isUser) {
    messageContent.innerHTML = marked.parse(message);
  } else {
    messageContent.textContent = message;
  }

  messageDiv.appendChild(messageContent);
  chatArea.appendChild(messageDiv);
  
  // Scroll to bottom
  chatArea.scrollTop = chatArea.scrollHeight;
  
  // Store message
  chatMessages.push({
    content: message,
    isUser: isUser,
    timestamp: new Date().toISOString()
  });
}

// Show typing indicator
function showTypingIndicator() {
  const chatArea = document.getElementById('chatArea');
  if (!chatArea) return;

  const typingDiv = document.createElement('div');
  typingDiv.id = 'typingIndicator';
  typingDiv.className = 'message-container mb-3 assistant-message';
  
  const typingContent = document.createElement('div');
  typingContent.className = 'modern-card p-3 me-auto';
  typingContent.style.maxWidth = '85%';
  typingContent.style.background = 'var(--bg-secondary)';
  typingContent.innerHTML = `
    <div class="d-flex align-items-center">
      <span style="margin-right: 8px;">Assistant is typing</span>
      <div class="typing-dots">
        <span></span><span></span><span></span>
      </div>
    </div>
  `;
  
  typingDiv.appendChild(typingContent);
  chatArea.appendChild(typingDiv);
  chatArea.scrollTop = chatArea.scrollHeight;
}

// Hide typing indicator
function hideTypingIndicator() {
  const typingIndicator = document.getElementById('typingIndicator');
  if (typingIndicator) {
    typingIndicator.remove();
  }
}

// Send message to chat API
async function sendMessage(message) {
  if (isWaitingForResponse) return;
  
  const trimmedMessage = message.trim();
  if (!trimmedMessage) return;

  // Add to history
  promptHistory.unshift(trimmedMessage);
  historyIndex = -1;

  // Add user message to chat
  addMessageToChat(trimmedMessage, true);
  
  // Clear input
  const input = document.getElementById('input');
  if (input) {
    input.value = '';
    input.style.height = 'auto';
  }

  // Set loading state
  isWaitingForResponse = true;
  updateSendButton();
  showTypingIndicator();

  try {
    const response = await fetch('/api/chat', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        question: trimmedMessage
      })
    });

    hideTypingIndicator();

    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }

    const data = await response.json();
    
    console.log('Received data:', data);
    console.log('data.response:', data.response);
    console.log('typeof data.response:', typeof data.response);
    
    if (data.response) {
      addMessageToChat(data.response, false);
    } else {
      console.error('No response field found. Available fields:', Object.keys(data));
      addMessageToChat('Sorry, I received an unexpected response format.', false, true);
    }

  } catch (error) {
    console.error('Chat error:', error);
    hideTypingIndicator();
    addMessageToChat(`Error: ${error.message}`, false, true);
  } finally {
    isWaitingForResponse = false;
    updateSendButton();
  }
}

// Update send button state
function updateSendButton() {
  const sendBtn = document.getElementById('sendBtn');
  if (!sendBtn) return;

  if (isWaitingForResponse) {
    sendBtn.textContent = 'Sending...';
    sendBtn.disabled = true;
    sendBtn.style.opacity = '0.6';
  } else {
    sendBtn.textContent = 'Send';
    sendBtn.disabled = false;
    sendBtn.style.opacity = '1';
  }
}

// Setup chat event listeners
function setupChat() {
  const input = document.getElementById('input');
  const sendBtn = document.getElementById('sendBtn');
  const enterToSend = document.getElementById('enterToSend');

  if (!input || !sendBtn) {
    console.warn('Chat elements not found');
    return;
  }

  // Send button click
  sendBtn.addEventListener('click', () => {
    const message = input.value.trim();
    if (message && !isWaitingForResponse) {
      sendMessage(message);
    }
  });

  // Input handling
  input.addEventListener('keydown', (e) => {
    if (isComposing) return;

    // Handle Enter key
    if (e.key === 'Enter') {
      const shouldSend = enterToSend ? enterToSend.checked : true;
      
      if (shouldSend && !e.shiftKey) {
        e.preventDefault();
        const message = input.value.trim();
        if (message && !isWaitingForResponse) {
          sendMessage(message);
        }
      }
    }
    
    // Handle history navigation
    if (e.key === 'ArrowUp' && !e.shiftKey && promptHistory.length > 0) {
      e.preventDefault();
      if (historyIndex < promptHistory.length - 1) {
        historyIndex++;
        input.value = promptHistory[historyIndex];
      }
    }
    
    if (e.key === 'ArrowDown' && !e.shiftKey) {
      e.preventDefault();
      if (historyIndex > 0) {
        historyIndex--;
        input.value = promptHistory[historyIndex];
      } else if (historyIndex === 0) {
        historyIndex = -1;
        input.value = '';
      }
    }
  });

  // IME composition handling
  input.addEventListener('compositionstart', () => {
    isComposing = true;
  });

  input.addEventListener('compositionend', () => {
    isComposing = false;
  });

  // Auto-resize textarea
  input.addEventListener('input', () => {
    input.style.height = 'auto';
    input.style.height = input.scrollHeight + 'px';
  });

  // Enter to send toggle
  if (enterToSend) {
    enterToSend.addEventListener('change', () => {
      localStorage.setItem('enterToSend', enterToSend.checked);
    });
    
    // Load saved preference
    const savedEnterToSend = localStorage.getItem('enterToSend');
    if (savedEnterToSend !== null) {
      enterToSend.checked = savedEnterToSend === 'true';
    }
  }

  console.log('Chat functionality initialized');
}

// ---- Sidebar Functionality ----

// Agent Management
function selectAgent(agentName) {
  console.log('Selecting agent:', agentName);
  currentAgent = agentName;
  // Update UI to reflect selected agent
  document.querySelectorAll('.agent-item').forEach(item => {
    item.style.background = 'var(--bg-secondary)';
  });
  event.target.closest('.agent-item').style.background = 'var(--primary-color-light)';
}

// Plugin Management
function togglePlugin(pluginName, enabled) {
  console.log('Toggling plugin:', pluginName, 'enabled:', enabled);
  // Send plugin toggle request to server
}

// Settings Management
function toggleSetting(settingName, enabled) {
  console.log('Toggling setting:', settingName, 'enabled:', enabled);
  // Save setting to localStorage or send to server
  localStorage.setItem(settingName, String(enabled));
}

// ---- Agent Management Functions ----

// Show add agent modal
function showAddAgentModal() {
  const modal = new bootstrap.Modal(document.getElementById('addAgentModal'));
  const agentNameInput = document.getElementById('agentName');
  
  // Clear previous input
  if (agentNameInput) {
    agentNameInput.value = '';
  }
  
  modal.show();
  
  // Focus on input after modal is shown
  setTimeout(() => {
    if (agentNameInput) {
      agentNameInput.focus();
    }
  }, 500);
}

// Create new agent
async function createNewAgent() {
  const agentNameInput = document.getElementById('agentName');
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
    const response = await fetch('/api/agents', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ name: agentName })
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
    
    // Show success message
    console.log('Agent created successfully:', agentName);
    
    // Refresh the agent list (we'll implement this later)
    await refreshAgentList();
    
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
  try {
    const response = await fetch('/api/agents');
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }
    
    const data = await response.json();
    displayAgents(data.agents, data.current);
    
  } catch (error) {
    console.error('Error loading agents:', error);
    const agentsList = document.getElementById('agentsList');
    if (agentsList) {
      agentsList.innerHTML = '<div class="text-muted small p-2">Failed to load agents</div>';
    }
  }
}

// Display agents in the sidebar with pagination
let allAgents = [];
let currentAgentName = '';
let visibleAgentCount = 3;

function displayAgents(agents, currentAgent) {
  const agentsList = document.getElementById('agentsList');
  if (!agentsList) return;
  
  // Store the data for pagination
  allAgents = agents;
  currentAgentName = currentAgent;
  
  renderAgents();
}

function renderAgents() {
  const agentsList = document.getElementById('agentsList');
  if (!agentsList) return;
  
  // Clear existing agents
  agentsList.innerHTML = '';
  
  // Show only the first 'visibleAgentCount' agents
  const agentsToShow = allAgents.slice(0, visibleAgentCount);
  
  // Add each visible agent
  agentsToShow.forEach(agentName => {
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
}

function loadMoreAgents() {
  visibleAgentCount = allAgents.length; // Show all agents
  renderAgents();
}

function hideAgents() {
  visibleAgentCount = 3; // Show only first 3 agents
  renderAgents();
}

// Create agent element
function createAgentElement(agentName, currentAgent) {
  const isCurrentAgent = agentName === currentAgent;
  
  const agentDiv = document.createElement('div');
  agentDiv.className = 'agent-item';
  agentDiv.style.background = isCurrentAgent ? 'var(--primary-color-light)' : 'var(--bg-secondary)';
  agentDiv.style.cursor = 'pointer';
  
  agentDiv.innerHTML = `
    <div class="d-flex align-items-center justify-content-between">
      <div class="flex-grow-1">
        <div class="fw-medium" style="color: var(--text-primary);">${agentName}</div>
        <div class="text-muted small">Agent</div>
      </div>
      <div class="d-flex align-items-center gap-2">
        ${isCurrentAgent ? '<div class="status-indicator status-online"></div>' : ''}
        <div class="agent-actions">
          ${!isCurrentAgent ? `<button class="modern-btn modern-btn-secondary px-2 py-1" onclick="switchToAgent('${agentName}')" title="Switch to this agent" style="font-size: 0.75rem;">
            Load
          </button>` : ''}
          <button class="btn btn-sm btn-link p-1" onclick="deleteAgent('${agentName}')" title="Delete agent">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M9,3V4H4V6H5V19A2,2 0 0,0 7,21H17A2,2 0 0,0 19,19V6H20V4H15V3H9M7,6H17V19H7V6M9,8V17H11V8H9M13,8V17H15V8H13Z"/>
            </svg>
          </button>
        </div>
      </div>
    </div>
  `;
  
  // Add click event to select agent
  agentDiv.addEventListener('click', (e) => {
    // Don't trigger if clicking on action buttons
    if (!e.target.closest('.agent-actions')) {
      selectAgent(agentName);
    }
  });
  
  // Add hover effects
  agentDiv.addEventListener('mouseenter', () => {
    if (!isCurrentAgent) {
      agentDiv.style.background = 'var(--bg-tertiary)';
    }
  });
  
  agentDiv.addEventListener('mouseleave', () => {
    if (!isCurrentAgent) {
      agentDiv.style.background = 'var(--bg-secondary)';
    }
  });
  
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
    showNotification(`Switched to agent: ${agentName}`, 'success');
    
    // Refresh the agent list to update current agent
    await loadAgents();
    
    // Reload plugins for the new agent
    await loadPlugins();
    
    // Reload settings for the new agent
    await loadSettings();
    
    console.log('Switched to agent:', agentName);
    
  } catch (error) {
    console.error('Error switching agent:', error);
    showNotification(`Failed to switch to agent: ${agentName}`, 'error');
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

// Refresh agent list (now implemented)
async function refreshAgentList() {
  await loadAgents();
}

// ---- Plugin Functionality ----

// Load available plugins
async function loadPlugins() {
  try {
    // Fetch all available plugins from registry
    const registryResponse = await fetch('/api/plugin-registry');
    if (!registryResponse.ok) {
      throw new Error('Failed to fetch plugin registry');
    }
    const registry = await registryResponse.json();
    
    // Fetch currently loaded plugins for this agent
    const activeResponse = await fetch('/api/plugins');
    if (!activeResponse.ok) {
      throw new Error('Failed to fetch active plugins');
    }
    const activePlugins = await activeResponse.json();
    
    // Create a set of active plugin names for quick lookup
    const activePluginNames = new Set(activePlugins.plugins.map(p => p.name));
    
    // Filter to only show local plugins in sidebar (those without github_repo)
    const localPlugins = registry.plugins.filter(plugin => !plugin.github_repo);
    
    displayPlugins(localPlugins, activePluginNames);
  } catch (error) {
    console.error('Error loading plugins:', error);
    const pluginsList = document.getElementById('pluginsList');
    if (pluginsList) {
      pluginsList.innerHTML = '<div class="text-danger small">Failed to load plugins</div>';
    }
  }
}

// Display plugins in the sidebar
function displayPlugins(plugins, activePluginNames) {
  const pluginsList = document.getElementById('pluginsList');
  if (!pluginsList) return;
  
  if (plugins.length === 0) {
    pluginsList.innerHTML = '<div class="text-muted small">No plugins available</div>';
    return;
  }
  
  pluginsList.innerHTML = plugins.map(plugin => {
    const isActive = activePluginNames.has(plugin.name);
    const pluginPath = plugin.path || '';
    const isUploaded = pluginPath.includes('uploaded_plugins') && !plugin.github_repo;
    
    return `
      <div class="plugin-item">
        <div class="d-flex align-items-center justify-content-between">
          <div>
            <div class="fw-medium d-flex align-items-center" style="color: var(--text-primary);">
              ${plugin.name}
              ${isUploaded ? '<span class="badge badge-success ms-2" style="font-size: 0.7em;">Local</span>' : ''}
            </div>
            <div class="text-muted small">${plugin.description || 'No description available'}</div>
            ${plugin.version ? `<div class="text-muted" style="font-size: 0.7em;">v${plugin.version}</div>` : ''}
          </div>
          <div class="d-flex align-items-center">
            ${isUploaded ? `
              <button class="btn btn-sm btn-outline-danger me-2 plugin-remove-btn" 
                      data-plugin-name="${plugin.name}" 
                      data-plugin-path="${plugin.path}"
                      title="Remove plugin">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
                </svg>
              </button>
            ` : ''}
            <div class="form-check form-switch">
              <input class="form-check-input plugin-toggle" type="checkbox" 
                     data-plugin-name="${plugin.name}" 
                     data-plugin-path="${plugin.path}"
                     ${isActive ? 'checked' : ''}>
            </div>
          </div>
        </div>
      </div>
    `;
  }).join('');
  
  // Add event listeners to plugin toggles
  setupPluginToggles();
}

// Setup plugin toggle event listeners
function setupPluginToggles() {
  // Setup plugin toggle switches
  const toggles = document.querySelectorAll('.plugin-toggle');
  toggles.forEach(toggle => {
    toggle.addEventListener('change', async (e) => {
      const pluginName = e.target.dataset.pluginName;
      const pluginPath = e.target.dataset.pluginPath;
      const isEnabled = e.target.checked;
      
      try {
        await togglePlugin(pluginName, pluginPath, isEnabled);
      } catch (error) {
        console.error('Failed to toggle plugin:', error);
        // Revert the toggle state
        e.target.checked = !isEnabled;
        alert(`Failed to ${isEnabled ? 'enable' : 'disable'} plugin: ${error.message}`);
      }
    });
  });
  
  // Setup plugin remove buttons
  const removeButtons = document.querySelectorAll('.plugin-remove-btn');
  removeButtons.forEach(button => {
    button.addEventListener('click', async (e) => {
      const pluginName = e.target.closest('button').dataset.pluginName;
      const pluginPath = e.target.closest('button').dataset.pluginPath;
      
      // Confirm removal
      if (!confirm(`Are you sure you want to remove the plugin "${pluginName}"? This action cannot be undone.`)) {
        return;
      }
      
      try {
        await removePlugin(pluginName, pluginPath);
        // Refresh plugins list
        await loadPlugins();
      } catch (error) {
        console.error('Failed to remove plugin:', error);
        alert(`Failed to remove plugin: ${error.message}`);
      }
    });
  });
}

// Toggle plugin on/off
async function togglePlugin(pluginName, pluginPath, enable) {
  const method = enable ? 'POST' : 'DELETE';
  const url = enable ? '/api/plugins' : `/api/plugins?name=${encodeURIComponent(pluginName)}`;
  
  let body;
  let headers = {};
  
  if (enable) {
    // Use FormData instead of JSON for multipart/form-data
    body = new FormData();
    body.append('name', pluginName);
    body.append('path', pluginPath);
    // Don't set Content-Type header - let browser set it with boundary
  }
  
  const response = await fetch(url, {
    method: method,
    headers: headers,
    body: body
  });
  
  if (!response.ok) {
    const errorText = await response.text();
    throw new Error(errorText || `Failed to ${enable ? 'enable' : 'disable'} plugin`);
  }
  
  console.log(`Plugin ${pluginName} ${enable ? 'enabled' : 'disabled'} successfully`);
}

// Remove uploaded plugin
async function removePlugin(pluginName, pluginPath) {
  const response = await fetch(`/api/plugin-registry?name=${encodeURIComponent(pluginName)}`, {
    method: 'DELETE'
  });
  
  if (!response.ok) {
    const errorText = await response.text();
    throw new Error(errorText || 'Failed to remove plugin');
  }
  
  console.log(`Plugin ${pluginName} removed successfully from registry and filesystem`);
}

// Plugin store modal functions
async function showPluginStoreModal() {
  const modal = new bootstrap.Modal(document.getElementById('pluginStoreModal'));
  modal.show();
  
  // Load online plugins when modal opens
  await loadOnlinePlugins();
}

async function loadOnlinePlugins() {
  try {
    const response = await fetch('/api/plugin-registry');
    if (!response.ok) {
      throw new Error('Failed to fetch plugin registry');
    }
    
    const data = await response.json();
    
    // Filter for online plugins (ones with github_repo)
    const onlinePlugins = data.plugins.filter(plugin => plugin.github_repo);
    
    // Get list of actually installed local plugins (ones without github_repo)
    const localPlugins = data.plugins.filter(plugin => !plugin.github_repo);
    const installedPluginNames = new Set(localPlugins.map(p => p.name));
    
    displayOnlinePlugins(onlinePlugins, installedPluginNames);
  } catch (error) {
    console.error('Error loading online plugins:', error);
    
    const onlinePluginsList = document.getElementById('onlinePluginsList');
    if (onlinePluginsList) {
      onlinePluginsList.innerHTML = `
        <div class="alert alert-danger" role="alert">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
            <path d="M12,2L13.09,8.26L22,9L13.09,9.74L12,16L10.91,9.74L2,9L10.91,8.26L12,2Z"/>
          </svg>
          Failed to load online plugins: ${error.message}
        </div>
      `;
    }
  }
}

function displayOnlinePlugins(onlinePlugins, installedPluginNames = new Set()) {
  const onlinePluginsList = document.getElementById('onlinePluginsList');
  if (!onlinePluginsList) return;
  
  if (onlinePlugins.length === 0) {
    onlinePluginsList.innerHTML = `
      <div class="text-center py-4" style="color: var(--text-secondary);">
        <svg width="48" height="48" viewBox="0 0 24 24" fill="currentColor" class="mb-3">
          <path d="M20.5,11H19V7C19,5.89 18.1,5 17,5H13V3.5A2.5,2.5 0 0,0 10.5,1A2.5,2.5 0 0,0 8,3.5V5H4C2.89,5 2,5.89 2,7V10.8H3.5C5,10.8 6.2,12 6.2,13.5C6.2,15 5,16.2 3.5,16.2H2V20C2,21.11 2.89,22 4,22H7.8V20.5C7.8,19 9,17.8 10.5,17.8C12,17.8 13.2,19 13.2,20.5V22H17C18.11,22 19,21.11 19,20V16H20.5A2.5,2.5 0 0,0 23,13.5A2.5,2.5 0 0,0 20.5,11Z"/>
        </svg>
        <p>No online plugins available</p>
      </div>
    `;
    return;
  }
  
  onlinePluginsList.innerHTML = onlinePlugins.map(plugin => {
    const isInstalled = installedPluginNames.has(plugin.name);
    const githubUrl = plugin.github_repo;
    const version = plugin.version || 'Unknown';
    
    return `
      <div class="online-plugin-item mb-3 p-3" style="border: 1px solid var(--border-color); border-radius: 8px; background: var(--bg-secondary);">
        <div class="d-flex justify-content-between align-items-start">
          <div class="flex-grow-1">
            <div class="d-flex align-items-center mb-2">
              <h6 class="mb-0 me-2" style="color: var(--text-primary);">${plugin.name}</h6>
              <span class="badge" style="background: var(--accent-color); color: white;">v${version}</span>
              ${isInstalled ? '<span class="badge bg-success ms-2">Installed</span>' : ''}
            </div>
            <p class="text-muted small mb-2">${plugin.description}</p>
            <div class="d-flex align-items-center">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1" style="color: var(--text-secondary);">
                <path d="M12,2A10,10 0 0,0 2,12C2,16.42 4.87,20.17 8.84,21.5C9.34,21.58 9.5,21.27 9.5,21C9.5,20.77 9.5,20.14 9.5,19.31C6.73,19.91 6.14,17.97 6.14,17.97C5.68,16.81 5.03,16.5 5.03,16.5C4.12,15.88 5.1,15.9 5.1,15.9C6.1,15.97 6.63,16.93 6.63,16.93C7.5,18.45 8.97,18 9.54,17.76C9.63,17.11 9.89,16.67 10.17,16.42C7.95,16.17 5.62,15.31 5.62,11.5C5.62,10.39 6,9.5 6.65,8.79C6.55,8.54 6.2,7.5 6.75,6.15C6.75,6.15 7.59,5.88 9.5,7.17C10.29,6.95 11.15,6.84 12,6.84C12.85,6.84 13.71,6.95 14.5,7.17C16.41,5.88 17.25,6.15 17.25,6.15C17.8,7.5 17.45,8.54 17.35,8.79C18,9.5 18.38,10.39 18.38,11.5C18.38,15.32 16.04,16.16 13.81,16.41C14.17,16.72 14.5,17.33 14.5,18.26C14.5,19.6 14.5,20.68 14.5,21C14.5,21.27 14.66,21.59 15.17,21.5C19.14,20.16 22,16.42 22,12A10,10 0 0,0 12,2Z"/>
              </svg>
              <a href="${githubUrl}" target="_blank" class="text-decoration-none small" style="color: var(--text-secondary);">
                ${githubUrl.replace('https://github.com/', '')}
              </a>
            </div>
          </div>
          <div class="ms-3">
            ${isInstalled ? 
              `<button class="modern-btn modern-btn-secondary" disabled>
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/>
                </svg>
                Installed
              </button>` :
              `<button class="modern-btn modern-btn-primary" onclick="installOnlinePlugin('${plugin.name}', '${plugin.download_url}')">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M5,20H19V18H5M19,9H15V3H9V9H5L12,16L19,9Z"/>
                </svg>
                Install
              </button>`
            }
          </div>
        </div>
      </div>
    `;
  }).join('');
}

async function installOnlinePlugin(pluginName, downloadUrl) {
  try {
    const button = event.target;
    const originalText = button.innerHTML;
    
    // Show loading state
    button.disabled = true;
    button.innerHTML = `
      <div class="spinner-border spinner-border-sm me-1" role="status">
        <span class="visually-hidden">Loading...</span>
      </div>
      Installing...
    `;
    
    const response = await fetch('/api/plugins/install', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        name: pluginName,
        download_url: downloadUrl
      })
    });
    
    if (!response.ok) {
      const errorText = await response.text();
      throw new Error(errorText || 'Failed to install plugin');
    }
    
    // Success - update button state
    button.innerHTML = `
      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
        <path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/>
      </svg>
      Installed
    `;
    button.className = 'modern-btn modern-btn-secondary';
    
    // Refresh plugins in sidebar
    await loadPlugins();
    
    console.log(`Plugin ${pluginName} installed successfully`);
    
  } catch (error) {
    console.error('Error installing plugin:', error);
    
    // Reset button state
    button.disabled = false;
    button.innerHTML = originalText;
    
    // Show error message
    alert(`Failed to install plugin: ${error.message}`);
  }
}

// Plugin upload modal functions
let uploadListenersSetup = false;

function showPluginUploadModal() {
  const modal = new bootstrap.Modal(document.getElementById('pluginUploadModal'));
  modal.show();
  
  // Setup drag and drop and file input listeners only once
  if (!uploadListenersSetup) {
    setupUploadListeners();
    uploadListenersSetup = true;
  }
}

function setupUploadListeners() {
  const dropZone = document.getElementById('uploadDropZone');
  const fileInput = document.getElementById('pluginFileInput');
  
  if (!dropZone || !fileInput) return;
  
  // Drag and drop events
  dropZone.addEventListener('dragover', handleDragOver);
  dropZone.addEventListener('dragleave', handleDragLeave);
  dropZone.addEventListener('drop', handleDrop);
  dropZone.addEventListener('click', handleDropZoneClick);
  fileInput.addEventListener('change', handleFileInputChange);
}

function handleDragOver(e) {
  e.preventDefault();
  const dropZone = document.getElementById('uploadDropZone');
  dropZone.style.borderColor = 'var(--accent-color)';
  dropZone.style.backgroundColor = 'var(--bg-hover)';
}

function handleDragLeave(e) {
  e.preventDefault();
  const dropZone = document.getElementById('uploadDropZone');
  dropZone.style.borderColor = 'var(--border-color)';
  dropZone.style.backgroundColor = 'var(--bg-secondary)';
}

function handleDrop(e) {
  e.preventDefault();
  const dropZone = document.getElementById('uploadDropZone');
  dropZone.style.borderColor = 'var(--border-color)';
  dropZone.style.backgroundColor = 'var(--bg-secondary)';
  
  const files = e.dataTransfer.files;
  if (files.length > 0) {
    handlePluginFile(files[0]);
  }
}

function handleDropZoneClick(e) {
  // Only trigger file input if clicking directly on the drop zone, not on the button
  if (e.target.tagName === 'BUTTON' || e.target.closest('button')) {
    return;
  }
  
  const fileInput = document.getElementById('pluginFileInput');
  fileInput.click();
}

function handleFileInputChange(e) {
  if (e.target.files.length > 0) {
    handlePluginFile(e.target.files[0]);
  }
}

function handlePluginFile(file) {
  console.log('File selected:', file.name);
  
  // Validate file type
  if (!file.name.endsWith('.so')) {
    showUploadResult('error', 'Please select a valid plugin file (.so)');
    return;
  }
  
  // Validate file size (e.g., max 50MB)
  const maxSize = 50 * 1024 * 1024; // 50MB
  if (file.size > maxSize) {
    showUploadResult('error', 'File size too large. Maximum size is 50MB.');
    return;
  }
  
  uploadPluginFile(file);
}

async function uploadPluginFile(file) {
  const formData = new FormData();
  formData.append('plugin', file);
  
  const progressDiv = document.getElementById('uploadProgress');
  const progressBar = document.getElementById('uploadProgressBar');
  const progressPercent = document.getElementById('uploadPercent');
  const resultDiv = document.getElementById('uploadResult');
  
  // Show progress, hide result
  progressDiv.classList.remove('d-none');
  resultDiv.classList.add('d-none');
  
  try {
    const xhr = new XMLHttpRequest();
    
    // Track upload progress
    xhr.upload.addEventListener('progress', (e) => {
      if (e.lengthComputable) {
        const percentComplete = (e.loaded / e.total) * 100;
        progressBar.style.width = percentComplete + '%';
        progressBar.setAttribute('aria-valuenow', percentComplete);
        progressPercent.textContent = Math.round(percentComplete) + '%';
      }
    });
    
    // Handle completion
    xhr.addEventListener('load', () => {
      progressDiv.classList.add('d-none');
      
      if (xhr.status === 200) {
        showUploadResult('success', 'Plugin uploaded successfully!');
        
        // Refresh plugins in sidebar
        setTimeout(() => {
          loadPlugins();
        }, 1000);
        
        // Reset file input
        document.getElementById('pluginFileInput').value = '';
        
      } else {
        let errorMessage = 'Upload failed';
        try {
          const response = JSON.parse(xhr.responseText);
          errorMessage = response.error || errorMessage;
        } catch (e) {
          errorMessage = xhr.responseText || errorMessage;
        }
        showUploadResult('error', errorMessage);
      }
    });
    
    xhr.addEventListener('error', () => {
      progressDiv.classList.add('d-none');
      showUploadResult('error', 'Network error occurred during upload');
    });
    
    // Start upload
    xhr.open('POST', '/api/plugins/upload');
    xhr.send(formData);
    
  } catch (error) {
    progressDiv.classList.add('d-none');
    showUploadResult('error', 'Upload failed: ' + error.message);
  }
}

function showUploadResult(type, message) {
  const resultDiv = document.getElementById('uploadResult');
  if (!resultDiv) return;
  
  const isSuccess = type === 'success';
  const iconPath = isSuccess 
    ? 'M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z'
    : 'M13,13H11V7H13M13,17H11V15H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z';
  
  resultDiv.innerHTML = `
    <div class="alert alert-${isSuccess ? 'success' : 'danger'}" role="alert">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
        <path d="${iconPath}"/>
      </svg>
      ${message}
    </div>
  `;
  
  resultDiv.classList.remove('d-none');
}

// Setup sidebar event listeners
function setupSidebar() {
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

  // Plugin management buttons
  const browsePluginsBtn = document.getElementById('browsePluginsBtn');
  if (browsePluginsBtn) {
    browsePluginsBtn.addEventListener('click', () => {
      console.log('Browse plugins clicked');
      showPluginUploadModal();
    });
  }

  const pluginStoreBtn = document.getElementById('pluginStoreBtn');
  if (pluginStoreBtn) {
    pluginStoreBtn.addEventListener('click', () => {
      console.log('Plugin store clicked');
      showPluginStoreModal();
    });
  }

  // Settings buttons
  const advancedSettingsBtn = document.getElementById('advancedSettingsBtn');
  if (advancedSettingsBtn) {
    advancedSettingsBtn.addEventListener('click', () => {
      console.log('Advanced settings clicked');
      // Show advanced settings modal
    });
  }

  // System buttons
  const systemDiagnosticsBtn = document.getElementById('systemDiagnosticsBtn');
  if (systemDiagnosticsBtn) {
    systemDiagnosticsBtn.addEventListener('click', () => {
      console.log('System diagnostics clicked');
      // Show system diagnostics panel
    });
  }

  // Plugin toggle switches
  document.querySelectorAll('.plugin-item .form-check-input').forEach(toggle => {
    toggle.addEventListener('change', (e) => {
      const pluginName = e.target.closest('.plugin-item').querySelector('.fw-medium').textContent;
      togglePlugin(pluginName, e.target.checked);
    });
  });

  // Settings toggle switches
  document.querySelectorAll('.setting-item .form-check-input').forEach(toggle => {
    toggle.addEventListener('change', (e) => {
      const settingName = e.target.closest('.setting-item').querySelector('span').textContent;
      toggleSetting(settingName, e.target.checked);
    });
  });

  // Add hover effects to interactive items
  document.querySelectorAll('.agent-item, .plugin-item').forEach(item => {
    item.addEventListener('mouseenter', () => {
      if (!item.style.background.includes('var(--primary-color-light)')) {
        item.style.background = 'var(--bg-tertiary)';
      }
    });
    
    item.addEventListener('mouseleave', () => {
      if (!item.style.background.includes('var(--primary-color-light)')) {
        item.style.background = 'var(--bg-secondary)';
      }
    });
  });
}

// ---- Settings Functionality ----

// Load current settings
async function loadSettings() {
  try {
    const response = await fetch('/api/settings');
    if (response.ok) {
      const settings = await response.json();
      
      // Update model dropdown
      const modelSelect = document.getElementById('gptModelSelect');
      if (modelSelect && settings.Settings && settings.Settings.model) {
        modelSelect.value = settings.Settings.model;
      }
      
      // Update temperature slider
      const temperatureSlider = document.getElementById('temperatureSlider');
      const temperatureValue = document.getElementById('temperatureValue');
      if (temperatureSlider && settings.Settings && typeof settings.Settings.temperature !== 'undefined') {
        temperatureSlider.value = settings.Settings.temperature;
        if (temperatureValue) {
          temperatureValue.textContent = settings.Settings.temperature.toFixed(1);
        }
      }
    } else {
      console.error('Failed to load settings:', response.status);
    }
  } catch (error) {
    console.error('Error loading settings:', error);
    // Fallback to defaults
    const modelSelect = document.getElementById('gptModelSelect');
    const temperatureSlider = document.getElementById('temperatureSlider');
    const temperatureValue = document.getElementById('temperatureValue');
    
    if (modelSelect) modelSelect.value = 'gpt-4o';
    if (temperatureSlider) temperatureSlider.value = 0;
    if (temperatureValue) temperatureValue.textContent = '0.0';
  }
}

// Save settings
async function saveSettings() {
  try {
    const modelSelect = document.getElementById('gptModelSelect');
    const temperatureSlider = document.getElementById('temperatureSlider');
    
    if (!modelSelect || !temperatureSlider) {
      console.error('Settings elements not found');
      return;
    }
    
    const response = await fetch('/api/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ 
        model: modelSelect.value,
        temperature: parseFloat(temperatureSlider.value)
      })
    });
    
    if (response.ok) {
      console.log('Settings updated:', {
        model: modelSelect.value,
        temperature: parseFloat(temperatureSlider.value)
      });
      
      // Show success notification
      showNotification('Settings updated successfully!', 'success');
    } else {
      console.error('Failed to save settings:', response.status);
      showNotification('Failed to save settings', 'error');
    }
  } catch (error) {
    console.error('Error saving settings:', error);
    showNotification('Error saving settings', 'error');
  }
}

// Show notification function
function showNotification(message, type = 'info') {
  // Create notification element
  const notification = document.createElement('div');
  notification.className = `alert alert-${type === 'success' ? 'success' : type === 'error' ? 'danger' : 'info'} position-fixed`;
  notification.style.cssText = `
    top: 20px;
    right: 20px;
    z-index: 1050;
    max-width: 300px;
    box-shadow: 0 4px 12px rgba(0,0,0,0.15);
    border: none;
  `;
  notification.innerHTML = `
    <div class="d-flex align-items-center">
      <span>${message}</span>
      <button type="button" class="btn-close ms-2" aria-label="Close"></button>
    </div>
  `;
  
  // Add to body
  document.body.appendChild(notification);
  
  // Add close functionality
  const closeBtn = notification.querySelector('.btn-close');
  closeBtn.addEventListener('click', () => {
    notification.remove();
  });
  
  // Auto-remove after 3 seconds
  setTimeout(() => {
    if (document.body.contains(notification)) {
      notification.remove();
    }
  }, 3000);
}

// Setup settings event listeners
function setupSettings() {
  const modelSelect = document.getElementById('gptModelSelect');
  const temperatureSlider = document.getElementById('temperatureSlider');
  const temperatureValue = document.getElementById('temperatureValue');
  const updateBtn = document.getElementById('updateSettingsBtn');
  
  if (temperatureSlider && temperatureValue) {
    temperatureSlider.addEventListener('input', function(e) {
      temperatureValue.textContent = parseFloat(e.target.value).toFixed(1);
    });
  }
  
  if (updateBtn) {
    updateBtn.addEventListener('click', function() {
      saveSettings();
      console.log('Settings saved to config.json');
    });
  }
  
  // Load current settings
  loadSettings();
}

// Initialize application
function initializeApp() {
  // Set up dark mode functionality
  setupDarkMode();
  
  // Set up chat functionality
  setupChat();
  
  // Set up sidebar functionality
  setupSidebar();
  
  // Set up settings functionality
  setupSettings();
  
  // Load agents
  loadAgents();
  
  // Load plugins
  loadPlugins();
  
  console.log('App initialized successfully');
}

// Initialize when DOM is ready
document.addEventListener('DOMContentLoaded', function() {
  initializeApp();
});
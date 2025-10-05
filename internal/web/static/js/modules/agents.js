// Agent Management Module
// Handles all agent-related functionality including CRUD operations and UI management

// Agent state management
let allAgents = [];
let currentAgentName = '';
let visibleAgentCount = 3;

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
  const agentSystemPromptInput = document.getElementById('agentSystemPrompt');

  // Clear previous inputs
  if (agentNameInput) {
    agentNameInput.value = '';
  }
  if (agentSystemPromptInput) {
    agentSystemPromptInput.value = '';
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
  const agentSystemPromptInput = document.getElementById('agentSystemPrompt');
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

    // Show success message
    console.log('Agent created successfully:', agentName);

    // Refresh the agent list
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

  console.log('Agent management setup complete');
}

// Initialize agent management when DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', setupAgentManagement);
} else {
  setupAgentManagement();
}
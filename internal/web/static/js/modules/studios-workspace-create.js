/**
 * Studios Workspace Creation Module
 * Handles workspace creation modal and agent selection
 */

// State for workspace creation (using shared variables from window object)
// window.selectedAgents and window.availableAgents are declared in studios.tmpl

/**
 * Opens the workspace creation modal and populates available agents
 * @description Displays the modal for creating a new workspace with agent selection
 */
function openCreateWorkspaceModal() {
  // Populate parent/group selection
  void populateWorkspaceParentSelect();

  // Populate agent selection
  const container = document.getElementById('agents-selection');

  // Ensure availableAgents is initialized
  if (!window.availableAgents) {
    window.availableAgents = [];
  }

  if (container) {
    container.innerHTML = window.availableAgents.map(agent => `
            <div class="col-md-6">
                <div class="modern-card p-3">
                    <div class="form-check">
                        <input class="form-check-input" type="checkbox" id="agent-${escapeHtml(agent.name)}"
                               value="${escapeHtml(agent.name)}" onchange="toggleAgent('${escapeHtml(agent.name)}')">
                        <label class="form-check-label" for="agent-${escapeHtml(agent.name)}" style="color: var(--text-primary);">
                            ${escapeHtml(agent.name)}
                        </label>
                    </div>
                </div>
            </div>
        `).join('');
  }

  // Reset selected agents
  window.selectedAgents.clear();

  const modalElement = document.getElementById('addFolderModal');
  if (!modalElement) {
    return;
  }

  const modal = new bootstrap.Modal(modalElement);
  modal.show();
}

async function populateWorkspaceParentSelect() {
  const select = document.getElementById('folderParentSelect');
  if (!select) return;

  try {
    const response = await fetch('/api/workspaces?tree=true');
    if (!response.ok) throw new Error('Failed to load workspaces');
    const data = await response.json();
    const tree = data.folders || [];

    const flattened = [];
    (function walk(nodes, depth) {
      (nodes || []).forEach((node) => {
        if (!node || !node.id) return;
        flattened.push({ id: node.id, name: node.name || node.id, depth });
        if (node.children && node.children.length > 0) {
          walk(node.children, depth + 1);
        }
      });
    })(tree, 0);

    const options = ['<option value="">No group</option>'];
    flattened.forEach((ws) => {
      const indent = ws.depth > 0 ? `${'--'.repeat(ws.depth)} ` : '';
      options.push(`<option value="${escapeHtml(ws.id)}">${escapeHtml(indent + ws.name)}</option>`);
    });

    select.innerHTML = options.join('');
    select.value = '';
  } catch (err) {
    console.error('Failed to populate parent select:', err);
    select.innerHTML = '<option value="">No group</option>';
    select.value = '';
  }
}

/**
 * Toggles agent selection for workspace creation
 * @param {string} agentName - Name of the agent to toggle
 */
function toggleAgent(agentName) {
  if (window.selectedAgents.has(agentName)) {
    window.selectedAgents.delete(agentName);
  } else {
    window.selectedAgents.add(agentName);
  }
}

/**
 * Creates a new workspace with selected agents
 * @async
 * @throws {Error} When workspace creation fails or validation fails
 * @returns {Promise<void>}
 */
async function createWorkspace() {
  const nameInput = document.getElementById('folderNameInput');
  const descriptionInput = document.getElementById('folderDescriptionInput');
  const parentSelect = document.getElementById('folderParentSelect');
  const colorBtn = document.querySelector('#addFolderModal .folder-color-btn.active');

  const name = nameInput?.value.trim();
  const description = descriptionInput?.value.trim() || '';
  const parentId = parentSelect?.value?.trim() || '';
  const color = colorBtn?.dataset.color || '';

  if (!name) {
    showError('Please fill in all required fields');
    return;
  }

  // Allow creating workspace without agents - they can be added later

  try {
    // Use unified workspace API to create workspace
    const response = await fetch('/api/workspaces', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: name,
        description: description,
        parent_id: parentId,
        color: color
      })
    });

    const result = await response.json();

    // Add selected agents to the workspace if any were selected
    if (result.folder && window.selectedAgents.size > 0) {
      const workspaceId = result.folder.id;
      for (const agentName of window.selectedAgents) {
        await fetch(`/api/workspaces/${workspaceId}/agents`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ agent_name: agentName })
        });
      }
    }

    if (result.error) {
      showError('Failed to create workspace: ' + result.error);
      return;
    }

    // Close modal
    const modalElement = document.getElementById('addFolderModal');
    const modal = bootstrap.Modal.getInstance(modalElement);
    if (modal) {
      modal.hide();
    }

    // Clear form
    if (nameInput) nameInput.value = '';
    if (descriptionInput) descriptionInput.value = '';
    if (parentSelect) parentSelect.value = '';
    window.selectedAgents.clear();

    // Refresh workspaces list if function exists
    if (typeof loadWorkspaces === 'function') {
      await loadWorkspaces();
    }
  } catch (error) {
    console.error('Error creating workspace:', error);
    showError('Failed to create workspace');
  }
}

/**
 * Displays an error message to the user
 * @param {string} message - Error message to display
 */
function showError(message) {
  // Check if there's a global toast/notification system
  if (typeof window.showToast === 'function') {
    window.showToast(message, 'error');
  } else {
    // Fallback to alert
    console.error('Workspace Creation Error:', message);
    alert(message);
  }
}

// escapeHtml is provided by dom-utils.js

/**
 * Sets the list of available agents for workspace creation
 * @param {Array<Object>} agents - Array of agent objects with name property
 */
function setAvailableAgents(agents) {
  window.availableAgents = agents;
}

/**
 * Initializes event listeners for workspace creation controls
 * @description Sets up click handler for create workspace button
 */
function initializeWorkspaceCreationListeners() {
  const createBtn = document.getElementById('createFolderBtn');
  if (createBtn) {
    createBtn.addEventListener('click', createWorkspace);
  }

  document.querySelectorAll('#addFolderModal .folder-color-btn').forEach(btn => {
    btn.addEventListener('click', (event) => {
      document.querySelectorAll('#addFolderModal .folder-color-btn').forEach(colorBtn => {
        colorBtn.classList.remove('active');
      });
      event.currentTarget.classList.add('active');
    });
  });
}

// Export functions for global access
window.openCreateWorkspaceModal = openCreateWorkspaceModal;
window.toggleAgent = toggleAgent;
window.setAvailableAgents = setAvailableAgents;

// Initialize on DOM ready
document.addEventListener('DOMContentLoaded', initializeWorkspaceCreationListeners);

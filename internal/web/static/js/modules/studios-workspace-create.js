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
    // Populate agent selection
    const container = document.getElementById('agents-selection');

    // Ensure availableAgents is initialized
    if (!window.availableAgents) {
        window.availableAgents = [];
    }

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

    // Reset selected agents
    selectedAgents = new Set();

    const modal = new bootstrap.Modal(document.getElementById('createWorkspaceModal'));
    modal.show();
}

/**
 * Toggles agent selection for workspace creation
 * @param {string} agentName - Name of the agent to toggle
 */
function toggleAgent(agentName) {
    if (selectedAgents.has(agentName)) {
        selectedAgents.delete(agentName);
    } else {
        selectedAgents.add(agentName);
    }
}

/**
 * Creates a new workspace with selected agents
 * @async
 * @throws {Error} When workspace creation fails or validation fails
 * @returns {Promise<void>}
 */
async function createWorkspace() {
    const name = document.getElementById('workspace-name').value;
    const description = document.getElementById('workspace-description').value;

    if (!name) {
        showError('Please fill in all required fields');
        return;
    }

    if (selectedAgents.size === 0) {
        showError('Please select at least one agent');
        return;
    }

    try {
        const response = await fetch('/api/orchestration/workspace', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                name: name,
                description: description,
                participating_agents: Array.from(selectedAgents)
            })
        });

        const result = await response.json();

        if (result.error) {
            showError('Failed to create workspace: ' + result.error);
            return;
        }

        // Close modal
        const modalElement = document.getElementById('createWorkspaceModal');
        const modal = bootstrap.Modal.getInstance(modalElement);
        if (modal) {
            modal.hide();
        }

        // Clear form
        document.getElementById('workspace-name').value = '';
        document.getElementById('workspace-description').value = '';
        selectedAgents.clear();

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
    if (typeof window.showError === 'function') {
        window.showError(message);
    } else {
        alert(message);
    }
}

/**
 * Escapes HTML special characters to prevent XSS attacks
 * @param {string|null} text - Text to escape
 * @returns {string} HTML-escaped text
 */
// escapeHtml is defined in studios-workspace.js and exported to window
function escapeHtml(text) {
    if (text == null) return '';
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

/**
 * Sets the list of available agents for workspace creation
 * @param {Array<Object>} agents - Array of agent objects with name property
 */
function setAvailableAgents(agents) {
    availableAgents = agents;
}

/**
 * Initializes event listeners for workspace creation controls
 * @description Sets up click handler for create workspace button
 */
function initializeWorkspaceCreationListeners() {
    const createBtn = document.getElementById('createWorkspaceBtn');
    if (createBtn) {
        createBtn.addEventListener('click', createWorkspace);
    }
}

// Export functions for global access
window.openCreateWorkspaceModal = openCreateWorkspaceModal;
window.toggleAgent = toggleAgent;
window.setAvailableAgents = setAvailableAgents;

// Initialize on DOM ready
document.addEventListener('DOMContentLoaded', initializeWorkspaceCreationListeners);

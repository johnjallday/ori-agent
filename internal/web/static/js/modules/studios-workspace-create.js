/**
 * Studios Workspace Creation Module
 * Handles workspace creation modal and agent selection
 */

// State for workspace creation (using shared variables from window object)
// window.selectedAgents and window.availableAgents are declared in studios.tmpl
const workspaceCreateState = {
  importMode: false,
  allowDuplicateImport: false,
  duplicateWorkspaceId: '',
  duplicateWorkspaceName: '',
  entryPoint: 'create_modal'
};

function resetImportState() {
  workspaceCreateState.importMode = false;
  workspaceCreateState.allowDuplicateImport = false;
  workspaceCreateState.duplicateWorkspaceId = '';
  workspaceCreateState.duplicateWorkspaceName = '';
  workspaceCreateState.entryPoint = 'create_modal';
}

function extractFolderNameFromPath(pathValue) {
  const trimmed = (pathValue || '').trim().replace(/[\\/]+$/, '');
  if (!trimmed) return '';
  const parts = trimmed.split(/[\\/]/);
  return parts[parts.length - 1] || '';
}

function clearDuplicateWarning() {
  const warning = document.getElementById('folderImportDuplicateWarning');
  const text = document.getElementById('folderImportDuplicateText');
  if (warning) warning.style.display = 'none';
  if (text) text.textContent = '';
  workspaceCreateState.duplicateWorkspaceId = '';
  workspaceCreateState.duplicateWorkspaceName = '';
}

function showDuplicateWarning(duplicate) {
  const warning = document.getElementById('folderImportDuplicateWarning');
  const text = document.getElementById('folderImportDuplicateText');
  if (!warning || !text || !duplicate) return;

  const workspaceName = duplicate.workspace_name || duplicate.workspace_id || 'this workspace';
  text.textContent = `This folder is already linked to "${workspaceName}".`;
  warning.style.display = 'block';

  workspaceCreateState.duplicateWorkspaceId = duplicate.workspace_id || '';
  workspaceCreateState.duplicateWorkspaceName = duplicate.workspace_name || '';
}

function prefillWorkspaceNameFromPath(pathValue) {
  const nameInput = document.getElementById('folderNameInput');
  if (!nameInput) return;

  const folderName = extractFolderNameFromPath(pathValue);
  if (!folderName) return;

  const currentName = nameInput.value.trim();
  const previousAutoName = nameInput.dataset.autofillName || '';

  // Only auto-fill if the field is empty or still holding the previous auto-filled name.
  if (!currentName || currentName === previousAutoName) {
    nameInput.value = folderName;
    nameInput.dataset.autofillName = folderName;
  }
}

function setImportMode(enabled) {
  workspaceCreateState.importMode = Boolean(enabled);
  if (!workspaceCreateState.importMode) {
    workspaceCreateState.allowDuplicateImport = false;
    clearDuplicateWarning();
  }

  const section = document.getElementById('folderImportSection');
  if (section) {
    section.hidden = !workspaceCreateState.importMode;
  }
}

async function checkFolderDuplicate(pathValue) {
  const path = (pathValue || '').trim();
  if (!path || !workspaceCreateState.importMode) {
    clearDuplicateWarning();
    return;
  }

  try {
    const response = await fetch(`/api/workspaces/import/check?path=${encodeURIComponent(path)}`);
    const result = await response.json().catch(() => ({}));

    if (!response.ok || !result.success) {
      clearDuplicateWarning();
      return;
    }

    if (result.duplicate && result.duplicate.found) {
      showDuplicateWarning(result.duplicate);
    } else {
      clearDuplicateWarning();
    }
  } catch (error) {
    console.error('Failed to check folder duplicate:', error);
    clearDuplicateWarning();
  }
}

function emitDuplicateOutcomeTelemetry(action, pathValue) {
  const normalizedAction = (action || '').trim();
  if (!normalizedAction) {
    return;
  }

  const payload = {
    action: normalizedAction,
    workspace_id: workspaceCreateState.duplicateWorkspaceId || '',
    entry_point: workspaceCreateState.entryPoint || 'create_modal',
    path: (pathValue || '').trim()
  };

  const endpoint = '/api/workspaces/import/duplicate-action';
  const body = JSON.stringify(payload);

  try {
    if (navigator.sendBeacon) {
      const data = new Blob([body], { type: 'application/json' });
      navigator.sendBeacon(endpoint, data);
      return;
    }
  } catch (error) {
    console.debug('sendBeacon failed for duplicate telemetry:', error);
  }

  fetch(endpoint, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body,
    keepalive: true
  }).catch((error) => {
    console.debug('Failed to send duplicate telemetry:', error);
  });
}

function setImportBrowseLoading(isLoading) {
  const browseButton = document.getElementById('folderImportBrowseBtn');
  if (!browseButton) {
    return;
  }

  if (isLoading) {
    browseButton.disabled = true;
    browseButton.dataset.originalText = browseButton.textContent || 'Browse';
    browseButton.textContent = 'Selecting...';
    return;
  }

  browseButton.disabled = false;
  browseButton.textContent = browseButton.dataset.originalText || 'Browse';
}

function populateWorkspaceEntryAgentSelect() {
  // Entry agent is always auto-created as workspace manager — no UI needed.
}

async function browseImportFolderPath() {
  const importPathInput = document.getElementById('folderImportPathInput');
  if (!importPathInput) {
    return;
  }

  setImportBrowseLoading(true);
  try {
    const response = await fetch('/api/folder-picker/select-path', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        title: 'Select Folder to Import as Workspace'
      })
    });
    const result = await response.json().catch(() => ({}));

    if (!response.ok || !result.success) {
      showError(result.error || 'Failed to open folder picker');
      return;
    }

    if (!result.selected || !result.path) {
      return;
    }

    importPathInput.value = result.path;
    workspaceCreateState.allowDuplicateImport = false;
    clearDuplicateWarning();
    prefillWorkspaceNameFromPath(result.path);
    await checkFolderDuplicate(result.path);
  } catch (error) {
    console.error('Failed to browse import folder path:', error);
    showError('Failed to open folder picker');
  } finally {
    setImportBrowseLoading(false);
    importPathInput.focus();
  }
}

/**
 * Opens the workspace creation modal and populates available agents
 * @description Displays the modal for creating a new workspace with agent selection
 */
function openCreateWorkspaceModal(options = {}) {
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
  populateWorkspaceEntryAgentSelect();

  // Reset selected agents
  window.selectedAgents.clear();
  resetImportState();

  const nameInput = document.getElementById('folderNameInput');
  const descriptionInput = document.getElementById('folderDescriptionInput');
  const parentSelect = document.getElementById('folderParentSelect');
  const importToggle = document.getElementById('folderImportToggle');
  const importPathInput = document.getElementById('folderImportPathInput');

  if (nameInput) {
    nameInput.value = '';
    nameInput.dataset.autofillName = '';
  }
  if (descriptionInput) descriptionInput.value = '';
  if (parentSelect) parentSelect.value = '';
  if (importToggle) importToggle.checked = false;
  if (importPathInput) importPathInput.value = '';
  clearDuplicateWarning();

  const wantsImport = Boolean(options && options.importMode);
  if (options && typeof options.entryPoint === 'string' && options.entryPoint.trim()) {
    workspaceCreateState.entryPoint = options.entryPoint.trim();
  }
  if (wantsImport) {
    if (importToggle) importToggle.checked = true;
    setImportMode(true);
    workspaceCreateState.entryPoint = workspaceCreateState.entryPoint || 'dashboard_button';
  } else {
    setImportMode(false);
    workspaceCreateState.entryPoint = 'create_modal';
  }

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
  const importToggle = document.getElementById('folderImportToggle');
  const importPathInput = document.getElementById('folderImportPathInput');

  const name = nameInput?.value.trim() || '';
  const description = descriptionInput?.value.trim() || '';
  const parentId = parentSelect?.value?.trim() || '';
  const color = colorBtn?.dataset.color || '';
  const importEnabled = Boolean(importToggle?.checked);
  const importPath = importPathInput?.value?.trim() || '';

  if (!name && !importEnabled) {
    showError('Please fill in all required fields');
    return;
  }
  if (importEnabled && !importPath) {
    showError('Please enter a folder path to import');
    return;
  }

  try {
    let endpoint = '/api/workspaces';
    const payload = {
      name: name,
      description: description,
      parent_id: parentId,
      color: color
    };
    if (importEnabled) {
      endpoint = '/api/workspaces/import';
      payload.path = importPath;
      payload.allow_duplicate = workspaceCreateState.allowDuplicateImport;
      payload.entry_point = workspaceCreateState.entryPoint || 'create_modal';
    }

    const response = await fetch(endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });

    const result = await response.json().catch(() => ({}));

    if (response.status === 409 && importEnabled && result.duplicate) {
      showDuplicateWarning(result.duplicate);
      showError('This folder is already imported. Open the existing workspace or click "Import Anyway".');
      return;
    }

    if (!response.ok || result.error) {
      const fallbackMessage = importEnabled ? 'Failed to import folder as workspace' : 'Failed to create workspace';
      showError(result.error || fallbackMessage);
      return;
    }

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

    // Close modal
    const modalElement = document.getElementById('addFolderModal');
    const modal = bootstrap.Modal.getInstance(modalElement);
    if (modal) {
      modal.hide();
    }

    // Clear form
    if (nameInput) nameInput.value = '';
    if (nameInput) nameInput.dataset.autofillName = '';
    if (descriptionInput) descriptionInput.value = '';
    if (parentSelect) parentSelect.value = '';
    if (importToggle) importToggle.checked = false;
    if (importPathInput) importPathInput.value = '';
    setImportMode(false);
    clearDuplicateWarning();
    window.selectedAgents.clear();
    resetImportState();

    // Navigate to the new workspace and open the Create Agent modal
    const workspaceId = result.folder && result.folder.id;
    if (workspaceId) {
      window.location.href = `/workspaces/${encodeURIComponent(workspaceId)}`;
      return;
    }

    // Fallback: refresh workspaces list if we couldn't navigate
    const refreshFn = (window.WorkspaceHub && window.WorkspaceHub.loadWorkspaces)
      || window.loadWorkspaces;
    if (typeof refreshFn === 'function') {
      await refreshFn();
    }

    if (typeof window.showToast === 'function') {
      window.showToast(importEnabled ? 'Workspace imported successfully' : 'Workspace created successfully', 'success');
    }
  } catch (error) {
    console.error('Error creating/importing workspace:', error);
    showError(importEnabled ? 'Failed to import folder as workspace' : 'Failed to create workspace');
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
  populateWorkspaceEntryAgentSelect();
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

  const importToggle = document.getElementById('folderImportToggle');
  if (importToggle) {
    importToggle.addEventListener('change', (event) => {
      const checked = Boolean(event?.currentTarget?.checked);
      setImportMode(checked);
      if (!checked) {
        workspaceCreateState.allowDuplicateImport = false;
      }
    });
  }

  const importPathInput = document.getElementById('folderImportPathInput');
  if (importPathInput) {
    importPathInput.addEventListener('input', (event) => {
      workspaceCreateState.allowDuplicateImport = false;
      clearDuplicateWarning();
      prefillWorkspaceNameFromPath(event?.target?.value || '');
    });
    importPathInput.addEventListener('blur', (event) => {
      void checkFolderDuplicate(event?.target?.value || '');
    });
  }

  const openExistingBtn = document.getElementById('folderImportOpenExistingBtn');
  if (openExistingBtn) {
    openExistingBtn.addEventListener('click', () => {
      if (!workspaceCreateState.duplicateWorkspaceId) {
        return;
      }
      emitDuplicateOutcomeTelemetry('suggestion_accepted', importPathInput?.value || '');
      window.location.href = `/workspaces/${encodeURIComponent(workspaceCreateState.duplicateWorkspaceId)}`;
    });
  }

  const proceedDuplicateBtn = document.getElementById('folderImportProceedDuplicateBtn');
  if (proceedDuplicateBtn) {
    proceedDuplicateBtn.addEventListener('click', () => {
      workspaceCreateState.allowDuplicateImport = true;
      emitDuplicateOutcomeTelemetry('override_confirmed', importPathInput?.value || '');
      if (typeof window.showToast === 'function') {
        window.showToast('Duplicate override enabled. Click Create to continue.', 'warning');
      }
    });
  }

  const browseBtn = document.getElementById('folderImportBrowseBtn');
  if (browseBtn) {
    browseBtn.addEventListener('click', () => {
      void browseImportFolderPath();
    });
  }
}

// Export functions for global access
window.openCreateWorkspaceModal = openCreateWorkspaceModal;
window.toggleAgent = toggleAgent;
window.setAvailableAgents = setAvailableAgents;

// Initialize on DOM ready
document.addEventListener('DOMContentLoaded', initializeWorkspaceCreationListeners);

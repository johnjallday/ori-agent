/**
 * Workspace Creation Module
 * Handles workspace creation modal and agent selection
 *
 * Workspace template cards (Blank / Travels / Daily Briefings) are defined
 * in workspace-templates.js and exposed via window.WorkspaceTemplates.
 */

// State for workspace creation (using shared variables from window object)
// window.selectedAgents and window.availableAgents are declared in workspaces-hub.tmpl
const workspaceCreateState = {
  importMode: false,
  allowDuplicateImport: false,
  duplicateWorkspaceId: '',
  duplicateWorkspaceName: '',
  entryPoint: 'create_modal',
  // Currently picked template; populated when the modal opens. Falls back
  // to the first template (Blank) when the user hasn't interacted.
  template: null
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

function getWorkspaceBootstrapFromModal() {
  const description = String(document.getElementById('folderDescriptionInput')?.value || '').trim();
  const systems = String(document.getElementById('folderSystemsInput')?.value || '').trim();
  const context = String(document.getElementById('folderContextInput')?.value || '').trim();
  const systemsList = systems
    ? systems
      .split(/[\n,;]+/)
      .map((value) => value.trim())
      .filter(Boolean)
    : [];

  return {
    hasAny: Boolean(description || systems || context),
    description,
    goal: description,
    capabilities: '',
    systems,
    systemsList,
    context
  };
}

function resetWorkspaceBootstrapFields() {
  const goalInput = document.getElementById('folderPrimaryGoalInput');
  const systemsInput = document.getElementById('folderSystemsInput');
  const contextInput = document.getElementById('folderContextInput');

  if (goalInput) goalInput.value = '';
  if (systemsInput) systemsInput.value = '';
  if (contextInput) contextInput.value = '';
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

function buildWorkspaceSlugConflictMessage(conflict) {
  const requestedSlug = typeof conflict?.requested_slug === 'string' ? conflict.requested_slug.trim() : '';
  const suggestedSlug = typeof conflict?.suggested_slug === 'string' ? conflict.suggested_slug.trim() : '';
  const location = typeof conflict?.location === 'string' ? conflict.location.trim().replace(/[\\/]+$/, '') : '';
  const suggestedPath = location && suggestedSlug ? `${location}/${suggestedSlug}` : '';

  const parts = [
    `A workspace folder named "${requestedSlug || 'this workspace'}" already exists on disk.`
  ];
  if (suggestedSlug) {
    parts.push(`Create this workspace with the folder name "${suggestedSlug}" instead?`);
  }
  if (suggestedPath) {
    parts.push(`Folder: ${suggestedPath}`);
  }
  return parts.join('\n\n');
}

function setImportMode(enabled) {
  workspaceCreateState.importMode = Boolean(enabled);
  if (!workspaceCreateState.importMode) {
    workspaceCreateState.allowDuplicateImport = false;
    clearDuplicateWarning();
  }

  const modal = document.getElementById('addFolderModal');
  if (modal) {
    modal.dataset.importMode = workspaceCreateState.importMode ? 'true' : 'false';
  }

  const importToggle = document.getElementById('folderImportToggle');
  if (importToggle) {
    importToggle.checked = workspaceCreateState.importMode;
  }

  const title = document.getElementById('folderModalTitle');
  if (title) {
    title.textContent = workspaceCreateState.importMode ? 'Import Folder' : 'Create Workspace';
  }

  const card = document.getElementById('folderImportCard');
  if (card) {
    card.hidden = !workspaceCreateState.importMode;
  }

  const section = document.getElementById('folderImportSection');
  if (section) {
    section.hidden = !workspaceCreateState.importMode;
  }

  // Project templates scaffold a *new* project; they don't apply when
  // importing an existing folder as the workspace. (ProjectTemplateCard also
  // syncs this on toggle changes and modal open.)
  const projectCard = document.getElementById('projectTemplateCard');
  if (projectCard) {
    projectCard.hidden = workspaceCreateState.importMode;
  }

  if (window.WorkspaceBootstrapReview && typeof window.WorkspaceBootstrapReview.refreshPrimaryActionLabel === 'function') {
    window.WorkspaceBootstrapReview.refreshPrimaryActionLabel();
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

// Project-template card behavior (population, pickers, manage link) lives in
// project-templates-manage.js (ProjectTemplateCard) so every page that ships
// the create-workspace markup gets it; this module only merges the card's
// payload fields into the create request.

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
  const presetSelect = document.getElementById('folderPresetSelect');
  const parentSelect = document.getElementById('folderParentSelect');
  const importToggle = document.getElementById('folderImportToggle');
  const importPathInput = document.getElementById('folderImportPathInput');

  if (nameInput) {
    nameInput.value = '';
    nameInput.dataset.autofillName = '';
  }
  if (descriptionInput) descriptionInput.value = '';
  if (presetSelect) presetSelect.value = 'general';
  resetWorkspaceBootstrapFields();
  if (parentSelect) parentSelect.value = '';
  if (importToggle) importToggle.checked = false;
  if (importPathInput) importPathInput.value = '';
  clearDuplicateWarning();
  if (window.WorkspaceBootstrapReview && typeof window.WorkspaceBootstrapReview.reset === 'function') {
    window.WorkspaceBootstrapReview.reset();
  }

  // Render the template card grid and pre-fill name/description from the
  // chosen template when the user hasn't typed anything custom yet. The
  // first template (Blank) is the default selection; for it both
  // defaultName and defaultDescription are empty so nothing is auto-filled.
  const templateGrid = document.getElementById('folderTemplateGrid');
  if (templateGrid && window.WorkspaceTemplates) {
    const initial = window.WorkspaceTemplates.render(templateGrid, {
      onSelect: (template) => {
        workspaceCreateState.template = template;
        // Only autofill if the user hasn't already typed something — never
        // overwrite their input.
        if (nameInput && !nameInput.value && template.defaultName) {
          nameInput.value = template.defaultName;
        }
        if (descriptionInput && !descriptionInput.value && template.defaultDescription) {
          descriptionInput.value = template.defaultDescription;
        }
      }
    });
    workspaceCreateState.template = initial;
  } else {
    workspaceCreateState.template = null;
  }

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

  const groupOptions = window.WorkspaceGroupOptions;

  try {
    const response = await fetch('/api/workspaces?tree=true');
    if (!response.ok) throw new Error('Failed to load workspaces');
    const data = await response.json();
    const tree = data.folders || [];
    const groups = groupOptions.collectWorkspaceGroupOptions(tree);

    select.innerHTML = groupOptions.renderWorkspaceParentOptions(groups);
    select.value = '';
    groupOptions.setWorkspaceParentSelectState(select, groups.length);
  } catch (err) {
    console.error('Failed to populate parent select:', err);
    select.innerHTML = '<option value="">No group</option>';
    select.value = '';
    groupOptions.setWorkspaceParentSelectState(select, 0);
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
  const presetSelect = document.getElementById('folderPresetSelect');
  const parentSelect = document.getElementById('folderParentSelect');
  const colorBtn = document.querySelector('#addFolderModal .folder-color-btn.active');
  const importToggle = document.getElementById('folderImportToggle');
  const importPathInput = document.getElementById('folderImportPathInput');
  const workspaceBootstrap = getWorkspaceBootstrapFromModal();

  const name = nameInput?.value.trim() || '';
  const description = descriptionInput?.value.trim() || '';
  const workspacePreset = presetSelect?.value?.trim() || 'general';
  const parentId = parentSelect?.value?.trim() || '';
  const color = colorBtn?.dataset.color || '';
  const importEnabled = workspaceCreateState.importMode || Boolean(importToggle?.checked);
  const importPath = importPathInput?.value?.trim() || '';

  if (!name && !importEnabled) {
    showError('Please fill in all required fields');
    return;
  }
  if (importEnabled && !importPath) {
    showError('Please enter a folder path to import');
    return;
  }
  if (!description) {
    showError('Workspace description is required');
    descriptionInput?.focus();
    return;
  }

  if (window.WorkspaceBootstrapReview && typeof window.WorkspaceBootstrapReview.ensureReviewed === 'function') {
    const reviewOutcome = await window.WorkspaceBootstrapReview.ensureReviewed();
    if (!reviewOutcome.ready) {
      return;
    }
  }

  try {
    let endpoint = '/api/workspaces';
    const payload = {
      name: name,
      workspace_preset: workspacePreset,
      description: description,
      parent_id: parentId,
      color: color
    };
    if (workspaceBootstrap.hasAny) {
      payload.workspace_bootstrap = {
        goal: workspaceBootstrap.description || workspaceBootstrap.goal,
        systems: workspaceBootstrap.systems,
        context: workspaceBootstrap.context
      };
    }
    if (importEnabled) {
      endpoint = '/api/workspaces/import';
      payload.path = importPath;
      payload.allow_duplicate = workspaceCreateState.allowDuplicateImport;
      payload.entry_point = workspaceCreateState.entryPoint || 'create_modal';
    } else {
      if (window.ProjectTemplateCard) {
        Object.assign(payload, window.ProjectTemplateCard.getPayloadFields());
      }
      if (window.WorkspaceTagsCard) {
        Object.assign(payload, window.WorkspaceTagsCard.getPayloadFields());
      }
    }

    const requestPayload = { ...payload };
    let response;
    let result = {};

    while (true) {
      response = await fetch(endpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(requestPayload)
      });

      result = await response.json().catch(() => ({}));

      if (response.status === 409 && importEnabled && result.duplicate) {
        showDuplicateWarning(result.duplicate);
        showError('This folder is already imported. Open the existing workspace or click "Import Anyway".');
        return;
      }

      if (response.status === 409 && !importEnabled && result.conflict?.type === 'folder_slug') {
        const suggestedSlug = typeof result.conflict.suggested_slug === 'string'
          ? result.conflict.suggested_slug.trim()
          : '';
        if (!suggestedSlug) {
          showError(result.error || 'Failed to create workspace');
          return;
        }

        const confirmed = window.confirm(buildWorkspaceSlugConflictMessage(result.conflict));
        if (!confirmed) {
          return;
        }

        requestPayload.folder_slug = suggestedSlug;
        continue;
      }

      break;
    }

    if (!response.ok || result.error) {
      const fallbackMessage = importEnabled ? 'Failed to import folder as workspace' : 'Failed to create workspace';
      showError(result.error || fallbackMessage);
      return;
    }

    const workspaceId = result.folder && result.folder.id;
    let bootstrapApplyResult = {
      invitedAgents: 0,
      boundMCPs: 0,
      attachedSkills: 0,
      addedPlugins: 0,
      failures: []
    };
    if (
      workspaceId &&
      window.WorkspaceBootstrapReview &&
      typeof window.WorkspaceBootstrapReview.applyPlan === 'function'
    ) {
      bootstrapApplyResult = await window.WorkspaceBootstrapReview.applyPlan(workspaceId);
    }

    // Seed starter tasks from the picked template. Errors are non-fatal —
    // the workspace is already created and partial seeding is fine.
    let seededTaskCount = 0;
    if (workspaceId && workspaceCreateState.template && window.WorkspaceTemplates) {
      try {
        seededTaskCount = await window.WorkspaceTemplates.seedStarterTasks(
          workspaceId,
          workspaceCreateState.template
        );
      } catch (error) {
        console.warn('Failed to seed workspace starter tasks:', error);
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
    resetWorkspaceBootstrapFields();
    if (parentSelect) parentSelect.value = '';
    if (importToggle) importToggle.checked = false;
    if (importPathInput) importPathInput.value = '';
    setImportMode(false);
    clearDuplicateWarning();
    if (window.ProjectTemplateCard) window.ProjectTemplateCard.reset();
    if (window.WorkspaceTagsCard) window.WorkspaceTagsCard.reset();
    window.OriTagInput?.clearTagPoolCache?.();
    window.selectedAgents.clear();
    resetImportState();

    // The workspace exists even when project-template instantiation failed;
    // surface the warning and give the toast time to be read before
    // navigating away.
    const projectWarning = typeof result.project_warning === 'string' ? result.project_warning : '';
    if (projectWarning && typeof window.showToast === 'function') {
      window.showToast(projectWarning, 'warning');
    }

    if (workspaceId) {
      window.dispatchEvent(new CustomEvent('ori:workspace-created', {
        detail: {
          workspaceId,
          workspaceName: result.folder?.name || name || extractFolderNameFromPath(importPath) || ''
        }
      }));

      const successMessageParts = [];
      if (bootstrapApplyResult.invitedAgents > 0) successMessageParts.push(`${bootstrapApplyResult.invitedAgents} agent${bootstrapApplyResult.invitedAgents === 1 ? '' : 's'} invited`);
      if (bootstrapApplyResult.boundMCPs > 0) successMessageParts.push(`${bootstrapApplyResult.boundMCPs} MCP${bootstrapApplyResult.boundMCPs === 1 ? '' : 's'} bound`);
      if (bootstrapApplyResult.attachedSkills > 0) successMessageParts.push(`${bootstrapApplyResult.attachedSkills} skill${bootstrapApplyResult.attachedSkills === 1 ? '' : 's'} attached`);
      if (bootstrapApplyResult.addedPlugins > 0) successMessageParts.push(`${bootstrapApplyResult.addedPlugins} plugin${bootstrapApplyResult.addedPlugins === 1 ? '' : 's'} added`);
      if (seededTaskCount > 0) successMessageParts.push(`${seededTaskCount} starter task${seededTaskCount === 1 ? '' : 's'} added`);
      if (typeof window.showToast === 'function') {
        if (bootstrapApplyResult.failures.length > 0) {
          window.showToast(
            `${importEnabled ? 'Workspace imported' : 'Workspace created'} with partial setup${successMessageParts.length > 0 ? ` (${successMessageParts.join(', ')})` : ''}.${bootstrapApplyResult.failures[0] ? ` ${bootstrapApplyResult.failures[0]}` : ''}`,
            'warning'
          );
        } else if (successMessageParts.length > 0) {
          window.showToast(
            `${importEnabled ? 'Workspace imported' : 'Workspace created'} with ${successMessageParts.join(', ')}.`,
            'success'
          );
        }
      }
      const navigate = () => {
        window.location.href = `/workspaces/${encodeURIComponent(workspaceId)}`;
      };
      if (projectWarning) {
        setTimeout(navigate, 2500);
      } else {
        navigate();
      }
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
        window.showToast('Duplicate override enabled. Click Import Folder to continue.', 'warning');
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
window.WorkspaceCreate = window.WorkspaceCreate || {};
window.WorkspaceCreate.__test = {
  collectWorkspaceGroupOptions: window.WorkspaceGroupOptions.collectWorkspaceGroupOptions,
  renderWorkspaceParentOptions: window.WorkspaceGroupOptions.renderWorkspaceParentOptions
};

// Initialize on DOM ready
document.addEventListener('DOMContentLoaded', initializeWorkspaceCreationListeners);

// Auto-open the create-workspace modal when the page is loaded with the
// `?create=1` query param (used by the home-page first-run CTA). Strips the
// param from the URL afterward so a refresh doesn't re-trigger the modal.
document.addEventListener('DOMContentLoaded', () => {
  try {
    const url = new URL(window.location.href);
    if (url.searchParams.get('create') !== '1') return;
    url.searchParams.delete('create');
    window.history.replaceState({}, '', url.pathname + (url.search || '') + url.hash);
    // Defer one frame so any other DOMContentLoaded listeners (agent
    // population, etc.) have a chance to run before the modal renders.
    requestAnimationFrame(() => {
      if (typeof window.openCreateWorkspaceModal === 'function') {
        window.openCreateWorkspaceModal({ entryPoint: 'home_first_run' });
      }
    });
  } catch (_) { /* malformed URL or missing API — silently skip */ }
});

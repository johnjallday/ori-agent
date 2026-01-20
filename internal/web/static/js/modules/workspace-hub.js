/**
 * Workspace Hub - Main Coordinator
 * Orchestrates workspace hub sub-modules for task, session, note, and file management.
 *
 * Dependencies (load in this order):
 * - workspace-hub-utils.js
 * - workspace-hub-state.js
 * - workspace-hub-modals.js
 * - workspace-hub-selection.js
 * - workspace-hub-tasks.js
 * - workspace-hub-sessions.js
 * - workspace-hub-notes.js
 * - workspace-hub-files.js
 * - workspace-hub-smart-input.js
 * - workspace-hub.js (this file)
 *
 * @module workspace-hub
 */
(function() {
  'use strict';

  const hubEl = document.getElementById('workspaceHub');
  if (!hubEl) return;

  // Initialize DOM element references
  const elements = {
    workspaceSelect: document.getElementById('hubWorkspaceSelect'),
    workspaceBrowseBtn: document.getElementById('hubWorkspaceBrowseBtn'),
    workspaceMeta: document.getElementById('hubWorkspaceMeta'),
    workspaceStatus: document.getElementById('hubWorkspaceStatus'),
    workspaceUpdated: document.getElementById('hubWorkspaceUpdated'),
    workspaceAgents: document.getElementById('hubWorkspaceAgents'),
    workspaceDescription: document.getElementById('hubWorkspaceDescription'),
    workspaceCanvasBtn: document.getElementById('hubOpenCanvasBtn'),
    addTaskBtn: document.getElementById('hubAddTaskBtn'),
    importWorkflowBtn: document.getElementById('hubImportWorkflowBtn'),
    refreshTasksBtn: document.getElementById('hubRefreshTasksBtn'),
    tasksList: document.getElementById('hubTasksList'),
    tasksSubtitle: document.getElementById('hubTasksSubtitle'),
    statCompleted: document.getElementById('hubStatCompleted'),
    statInProgress: document.getElementById('hubStatInProgress'),
    statScheduled: document.getElementById('hubStatScheduled'),
    statFailed: document.getElementById('hubStatFailed'),
    schedulesList: document.getElementById('hubSchedulesList'),
    viewSchedulesBtn: document.getElementById('hubViewSchedulesBtn'),
    launcher: document.getElementById('workspaceLauncher'),
    launcherGrid: document.getElementById('launcherGrid'),
    launcherEmpty: document.getElementById('launcherEmptyState'),
    launcherRefreshBtn: document.getElementById('launcherRefreshBtn'),
    loadingOverlay: document.getElementById('workspaceHubLoading'),
    sessionsList: document.getElementById('hubSessionsList'),
    newSessionBtn: document.getElementById('hubNewSessionBtn'),
    notesList: document.getElementById('hubNotesList'),
    newNoteBtn: document.getElementById('hubNewNoteBtn'),
    filesList: document.getElementById('hubFilesList'),
    addFileBtn: document.getElementById('hubAddFileBtn'),
    directoriesList: document.getElementById('hubDirectoriesList'),
    addDirectoryPanelBtn: document.getElementById('hubAddDirectoryPanelBtn'),
    smartInputCard: document.getElementById('hubSmartInput'),
    smartInputField: document.getElementById('hubSmartInputField'),
    smartInputSubmit: document.getElementById('hubSmartInputSubmit'),
    smartInputStatus: document.getElementById('hubSmartInputStatus'),
    smartInputPrompt: document.getElementById('hubSmartInputPrompt'),
    smartInputPromptHint: document.getElementById('hubSmartInputPromptHint'),
    smartInputPromptTask: document.getElementById('hubSmartInputPromptTask'),
    smartInputPromptChat: document.getElementById('hubSmartInputPromptChat'),
    smartInputPromptCancel: document.getElementById('hubSmartInputPromptCancel'),
    smartInputAttachBtn: document.getElementById('hubSmartInputAttachBtn'),
    smartInputProgressModal: document.getElementById('hubSmartInputProgressModal'),
    smartInputProgressHeadline: document.getElementById('hubSmartInputProgressHeadline'),
    smartInputProgressMessage: document.getElementById('hubSmartInputProgressMessage'),
    smartInputProgressSteps: document.getElementById('hubSmartInputProgressSteps'),
    smartInputCancelBtn: document.getElementById('hubSmartInputCancelBtn'),
    // Selection mode elements
    tasksPanel: document.getElementById('hubTasksPanel'),
    sessionsPanel: document.getElementById('hubSessionsPanel'),
    notesPanel: document.getElementById('hubNotesPanel'),
    filesPanel: document.getElementById('hubFilesPanel'),
    directoriesPanel: document.getElementById('hubDirectoriesPanel'),
    selectTasksBtn: document.getElementById('hubSelectTasksBtn'),
    bulkDeleteTasksBtn: document.getElementById('hubBulkDeleteTasksBtn'),
    selectSessionsBtn: document.getElementById('hubSelectSessionsBtn'),
    bulkDeleteSessionsBtn: document.getElementById('hubBulkDeleteSessionsBtn'),
    selectNotesBtn: document.getElementById('hubSelectNotesBtn'),
    bulkDeleteNotesBtn: document.getElementById('hubBulkDeleteNotesBtn'),
    selectFilesBtn: document.getElementById('hubSelectFilesBtn'),
    bulkTrashFilesBtn: document.getElementById('hubBulkTrashFilesBtn'),
    // Delete confirmation modal
    deleteConfirmModal: document.getElementById('hubDeleteConfirmModal'),
    deleteConfirmTitle: document.getElementById('hubDeleteConfirmTitle'),
    deleteConfirmBody: document.getElementById('hubDeleteConfirmBody'),
    deleteConfirmBtn: document.getElementById('hubDeleteConfirmBtn'),
    parentDeleteModal: document.getElementById('hubParentDeleteModal'),
    parentDeleteTitle: document.getElementById('hubParentDeleteTitle'),
    parentDeleteBody: document.getElementById('hubParentDeleteBody'),
    parentDeleteUngroupBtn: document.getElementById('hubParentDeleteUngroupBtn'),
    parentDeleteAllBtn: document.getElementById('hubParentDeleteAllBtn'),
    // Add file modal
    addFileModal: document.getElementById('hubAddFileModal'),
    fileDropZone: document.getElementById('hubFileDropZone'),
    fileInput: document.getElementById('hubFileInput'),
    selectedFilesPreview: document.getElementById('hubSelectedFilesPreview'),
    selectedFilesList: document.getElementById('hubSelectedFilesList'),
    fileTitle: document.getElementById('hubFileTitle'),
    fileNotes: document.getElementById('hubFileNotes'),
    fileUploadProgress: document.getElementById('hubFileUploadProgress'),
    fileUploadPercent: document.getElementById('hubFileUploadPercent'),
    fileUploadProgressBar: document.getElementById('hubFileUploadProgressBar'),
    addFileSubmitBtn: document.getElementById('hubAddFileSubmitBtn')
  };

  // Initialize state module with elements
  window.WorkspaceHubState.initElements(elements);

  const { formatDate, flattenWorkspaces } = window.WorkspaceHubUtils;

  /**
   * Schedule a workspace tasks refresh (debounced)
   * @param {number} delayMs - Delay in milliseconds
   */
  function scheduleWorkspaceTasksRefresh(delayMs = 500) {
    const state = window.WorkspaceHubState.getState();
    if (!state.selectedId) return;

    let timer = window.WorkspaceHubState.getRealtimeTimer();
    if (timer) {
      clearTimeout(timer);
    }
    timer = setTimeout(() => {
      window.WorkspaceHubState.setRealtimeTimer(null);
      if (state.selectedId) {
        window.WorkspaceHubTasks.loadTasks(state.selectedId);
      }
    }, delayMs);
    window.WorkspaceHubState.setRealtimeTimer(timer);
  }

  /**
   * Populate workspace select dropdown
   * @param {Array} flattened - Flattened workspace list
   */
  function populateWorkspaceSelect(flattened) {
    if (!elements.workspaceSelect) return;

    const options = ['<option value="">Select a workspace...</option>'];
    flattened.forEach((workspace) => {
      const indent = workspace.depth > 0 ? `${'--'.repeat(workspace.depth)} ` : '';
      const label = `${indent}${escapeHtml(workspace.name || 'Untitled Workspace')}`;
      options.push(`<option value="${escapeHtml(workspace.id)}">${label}</option>`);
    });

    elements.workspaceSelect.innerHTML = options.join('');
  }

  /**
   * Render launcher grid
   * @param {Array} flattened - Flattened workspace list
   */
  function renderLauncher(flattened) {
    if (!elements.launcherGrid || !elements.launcherEmpty) return;

    if (flattened.length === 0) {
      elements.launcherGrid.innerHTML = '';
      elements.launcherEmpty.style.display = 'flex';
      return;
    }

    elements.launcherEmpty.style.display = 'none';

    const cards = flattened.map((workspace) => {
      const description = workspace.description || 'No description yet.';
      const status = workspace.status || 'active';
      const statusLabel = escapeHtml(status.replace('_', ' '));
      const accentStyle = workspace.color ? `style="border-color: ${escapeHtml(workspace.color)}"` : '';

      return `
        <button class="launcher-card-item" data-workspace-id="${escapeHtml(workspace.id)}" ${accentStyle}>
          <div class="launcher-card-title">${escapeHtml(workspace.name || 'Untitled Workspace')}</div>
          <div class="launcher-card-path">${escapeHtml(workspace.path)}</div>
          <div class="launcher-card-description">${escapeHtml(description)}</div>
          <div class="launcher-card-meta">
            <span class="launcher-card-status status-${escapeHtml(status)}">${statusLabel}</span>
            <span>${workspace.session_count || 0} sessions</span>
          </div>
        </button>
      `;
    });

    elements.launcherGrid.innerHTML = cards.join('');

    elements.launcherGrid.querySelectorAll('[data-workspace-id]').forEach((card) => {
      card.addEventListener('click', () => {
        const workspaceId = card.dataset.workspaceId;
        selectWorkspace(workspaceId, { focus: true });
      });
    });
  }

  /**
   * Render workspace summary header
   * @param {Object} workspace - Workspace object
   */
  function renderWorkspaceSummary(workspace) {
    if (!workspace) return;

    if (elements.workspaceMeta) {
      const description = workspace.description ? ` ${workspace.description}` : '';
      elements.workspaceMeta.textContent = `${workspace.name || 'Workspace'}${description ? ` - ${description}` : ''}`;
    }

    if (elements.workspaceStatus) {
      elements.workspaceStatus.textContent = workspace.status || 'active';
    }

    if (elements.workspaceUpdated) {
      elements.workspaceUpdated.textContent = formatDate(workspace.updated_at || workspace.created_at);
    }

    if (elements.workspaceAgents) {
      const agentCount = (workspace.agent_instances && workspace.agent_instances.length) || (workspace.agents && workspace.agents.length) || 0;
      elements.workspaceAgents.textContent = agentCount ? `${agentCount} agents` : 'No agents yet';
    }

    if (elements.workspaceDescription) {
      elements.workspaceDescription.textContent = workspace.description || 'No description';
    }

    if (elements.workspaceCanvasBtn) {
      elements.workspaceCanvasBtn.href = `/workspaces/${encodeURIComponent(workspace.id)}/canvas`;
    }
  }

  /**
   * Clear workspace summary header
   */
  function clearWorkspaceSummary() {
    if (elements.workspaceMeta) {
      elements.workspaceMeta.textContent = 'Select a workspace to see tasks and schedules.';
    }

    if (elements.workspaceStatus) elements.workspaceStatus.textContent = '--';
    if (elements.workspaceUpdated) elements.workspaceUpdated.textContent = '--';
    if (elements.workspaceAgents) elements.workspaceAgents.textContent = '--';
    if (elements.workspaceDescription) elements.workspaceDescription.textContent = '--';

    if (elements.workspaceCanvasBtn) elements.workspaceCanvasBtn.removeAttribute('href');
  }

  /**
   * Select a workspace
   * @param {string} workspaceId - Workspace ID to select
   * @param {Object} options - Options
   * @param {boolean} options.focus - Whether to blur select after selection
   */
  function selectWorkspace(workspaceId, { focus = false } = {}) {
    const state = window.WorkspaceHubState.getState();
    const workspace = state.workspaceMap.get(workspaceId);
    if (!workspace) return;

    window.WorkspaceHubState.stopRealtime();
    state.selectedId = workspaceId;
    sessionStorage.setItem(window.WorkspaceHubState.getStorageKey(), workspaceId);

    if (elements.workspaceSelect) {
      elements.workspaceSelect.value = workspaceId;
    }

    renderWorkspaceSummary(workspace);
    window.WorkspaceHubState.setUIState('selected');
    window.WorkspaceHubSmartInput.setEnabled(true);
    window.WorkspaceHubSmartInput.resetPrompt();
    window.WorkspaceHubSmartInput.setStatus('', { busy: false });
    window.WorkspaceHubTasks.loadTasks(workspaceId);
    window.WorkspaceHubSessions.loadSessions(workspaceId);
    window.WorkspaceHubNotes.loadNotes(workspaceId);
    window.WorkspaceHubFiles.loadFiles(workspaceId);

    if (window.workspaceRealtime && typeof window.workspaceRealtime.subscribeToWorkspace === 'function') {
      const unsub = window.workspaceRealtime.subscribeToWorkspace(workspaceId, (event) => {
        if (!event || event.workspaceId !== state.selectedId) return;
        if (window.WorkspaceHubState.shouldRefreshForEvent(event.type)) {
          scheduleWorkspaceTasksRefresh();
        }
        if (event.type === 'workspace.updated' && event.data && typeof event.data === 'object') {
          const action = event.data.action || '';
          if (action.startsWith('directory_')) {
            window.WorkspaceHubFiles.loadFiles(state.selectedId);
          }
        }
      });
      window.WorkspaceHubState.setRealtimeUnsub(unsub);
    }

    if (focus && elements.workspaceSelect) {
      elements.workspaceSelect.blur();
    }
  }

  /**
   * Show the launcher view
   */
  function showLauncher() {
    const state = window.WorkspaceHubState.getState();

    window.WorkspaceHubState.stopRealtime();
    const controller = window.WorkspaceHubState.getTasksAbortController();
    if (controller) {
      controller.abort();
      window.WorkspaceHubState.setTasksAbortController(null);
    }
    window.WorkspaceHubState.setUIState('launcher');
    if (elements.workspaceSelect) {
      elements.workspaceSelect.value = '';
    }
    state.selectedId = null;
    window.WorkspaceHubSmartInput.setEnabled(false);
    window.WorkspaceHubSmartInput.resetPrompt();
    window.WorkspaceHubSmartInput.clearField();
    window.WorkspaceHubSmartInput.setStatus('Select a workspace to use quick add.', { busy: false });
    sessionStorage.removeItem(window.WorkspaceHubState.getStorageKey());
    clearWorkspaceSummary();
    window.WorkspaceHubTasks.renderStats({ completed: 0, in_progress: 0, failed: 0, scheduled: 0 });
    if (elements.tasksList) {
      elements.tasksList.innerHTML = '<div class="hub-empty">Select a workspace to view tasks.</div>';
    }
    if (elements.tasksSubtitle) {
      elements.tasksSubtitle.textContent = 'Select a workspace to see task activity.';
    }
    if (elements.schedulesList) {
      elements.schedulesList.innerHTML = '<div class="hub-empty">Select a workspace to view schedules.</div>';
    }
    if (elements.sessionsList) {
      elements.sessionsList.innerHTML = '<div class="hub-empty">Select a workspace to view sessions.</div>';
    }
    if (elements.notesList) {
      elements.notesList.innerHTML = '<div class="hub-empty">Select a workspace to view notes.</div>';
    }
    if (elements.filesList) {
      elements.filesList.innerHTML = '<div class="hub-empty">Select a workspace to view files.</div>';
    }
    if (elements.directoriesList) {
      elements.directoriesList.innerHTML = '<div class="hub-empty">Select a workspace to view directories.</div>';
    }
  }

  /**
   * Load all workspaces
   */
  async function loadWorkspaces() {
    const state = window.WorkspaceHubState.getState();

    window.WorkspaceHubState.setUIState('loading');

    try {
      const response = await fetch('/api/workspaces?tree=true');
      if (!response.ok) throw new Error('Failed to load workspaces');

      const data = await response.json();
      state.workspaces = data.folders || [];

      const flattened = flattenWorkspaces(state.workspaces);
      state.workspaceMap = new Map(flattened.map((workspace) => [workspace.id, workspace]));

      populateWorkspaceSelect(flattened);
      renderLauncher(flattened);

      const saved = sessionStorage.getItem(window.WorkspaceHubState.getStorageKey());
      if (saved && state.workspaceMap.has(saved)) {
        selectWorkspace(saved);
        return;
      }

      showLauncher();
    } catch (error) {
      console.error('Workspace hub failed to load workspaces:', error);
      showLauncher();
    }
  }

  /**
   * Open schedule panel
   */
  function openSchedulePanel() {
    const state = window.WorkspaceHubState.getState();
    if (!state.selectedId) return;
    if (window.sessionManager && typeof window.sessionManager.openScheduledTasksPanel === 'function') {
      window.sessionManager.openScheduledTasksPanel(state.selectedId);
    }
  }

  /**
   * Open task creation modal
   */
  function openTaskModal() {
    const state = window.WorkspaceHubState.getState();
    if (!state.selectedId) return;
    if (window.taskModalController) {
      window.taskModalController.openForCreate(state.selectedId, '', () => window.WorkspaceHubTasks.loadTasks(state.selectedId));
    }
  }

  /**
   * Open import workflow modal
   */
  function openImportWorkflowModal() {
    const state = window.WorkspaceHubState.getState();

    if (!state.selectedId) {
      if (window.Toast) window.Toast.error('Please select a workspace first');
      return;
    }

    let modal = document.getElementById('hubImportWorkflowModal');
    if (!modal) {
      modal = document.createElement('div');
      modal.id = 'hubImportWorkflowModal';
      modal.className = 'modal fade';
      modal.tabIndex = -1;
      modal.innerHTML = `
        <div class="modal-dialog">
          <div class="modal-content">
            <div class="modal-header">
              <h5 class="modal-title" style="color: var(--text-primary);">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                  <path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13.5,16V19H10.5V16H8L12,12L16,16H13.5M13,9V3.5L18.5,9H13Z"/>
                </svg>
                Import Workflow
              </h5>
              <button type="button" class="btn-close" data-bs-dismiss="modal"></button>
            </div>
            <div class="modal-body">
              <p style="color: var(--text-secondary);">Select a workflow JSON file to import as tasks:</p>
              <div class="mb-3">
                <input type="file" id="hubImportWorkflowFile" class="form-control" accept=".json,application/json">
              </div>
              <div id="hubImportWorkflowInfo" style="display: none;" class="modern-card p-3 mb-3">
                <strong id="hubImportWorkflowName" style="color: var(--text-primary);"></strong>
                <p id="hubImportWorkflowSteps" class="mb-0 mt-1 small" style="color: var(--text-muted);"></p>
              </div>
              <div id="hubImportWorkflowError" class="alert alert-danger" style="display: none;"></div>
            </div>
            <div class="modal-footer">
              <button type="button" class="modern-btn modern-btn-secondary" data-bs-dismiss="modal">Cancel</button>
              <button type="button" id="hubConfirmImportWorkflowBtn" class="modern-btn modern-btn-primary" disabled>
                <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13.5,16V19H10.5V16H8L12,12L16,16H13.5M13,9V3.5L18.5,9H13Z"/>
                </svg>
                Import
              </button>
            </div>
          </div>
        </div>
      `;
      document.body.appendChild(modal);

      const fileInput = modal.querySelector('#hubImportWorkflowFile');
      const confirmBtn = modal.querySelector('#hubConfirmImportWorkflowBtn');
      const infoEl = modal.querySelector('#hubImportWorkflowInfo');
      const nameEl = modal.querySelector('#hubImportWorkflowName');
      const stepsEl = modal.querySelector('#hubImportWorkflowSteps');
      const errorEl = modal.querySelector('#hubImportWorkflowError');

      let selectedWorkflowData = null;

      fileInput.addEventListener('change', (e) => {
        const file = e.target.files[0];
        if (!file) {
          selectedWorkflowData = null;
          infoEl.style.display = 'none';
          confirmBtn.disabled = true;
          return;
        }

        const reader = new FileReader();
        reader.onload = (event) => {
          try {
            const data = JSON.parse(event.target.result);
            selectedWorkflowData = data;
            nameEl.textContent = data.name || 'Unnamed Workflow';
            const stepCount = data.steps ? data.steps.length : 0;
            stepsEl.textContent = `${stepCount} step${stepCount !== 1 ? 's' : ''}`;
            infoEl.style.display = 'block';
            errorEl.style.display = 'none';
            confirmBtn.disabled = false;
          } catch (err) {
            selectedWorkflowData = null;
            errorEl.textContent = 'Invalid JSON file';
            errorEl.style.display = 'block';
            infoEl.style.display = 'none';
            confirmBtn.disabled = true;
          }
        };
        reader.readAsText(file);
      });

      confirmBtn.addEventListener('click', async () => {
        if (!selectedWorkflowData || !state.selectedId) return;

        confirmBtn.disabled = true;
        confirmBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1"></span> Importing...';

        try {
          await importWorkflowAsTask(selectedWorkflowData);
          bootstrap.Modal.getInstance(modal).hide();
          if (window.Toast) window.Toast.success('Workflow imported as tasks');
          window.WorkspaceHubTasks.loadTasks(state.selectedId);
        } catch (err) {
          errorEl.textContent = 'Failed to import: ' + err.message;
          errorEl.style.display = 'block';
        } finally {
          confirmBtn.disabled = false;
          confirmBtn.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1"><path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13.5,16V19H10.5V16H8L12,12L16,16H13.5M13,9V3.5L18.5,9H13Z"/></svg> Import';
        }
      });
    }

    // Reset modal state
    const fileInput = modal.querySelector('#hubImportWorkflowFile');
    const infoEl = modal.querySelector('#hubImportWorkflowInfo');
    const errorEl = modal.querySelector('#hubImportWorkflowError');
    const confirmBtn = modal.querySelector('#hubConfirmImportWorkflowBtn');

    fileInput.value = '';
    infoEl.style.display = 'none';
    errorEl.style.display = 'none';
    confirmBtn.disabled = true;

    const bsModal = new bootstrap.Modal(modal);
    bsModal.show();
  }

  /**
   * Import a workflow as tasks
   * @param {Object} workflowData - Workflow data
   * @returns {Promise<string>} Parent task ID
   */
  async function importWorkflowAsTask(workflowData) {
    const state = window.WorkspaceHubState.getState();

    const parentTask = {
      workspace_id: state.selectedId,
      name: workflowData.name || 'Imported Workflow',
      description: workflowData.description || '',
      status: 'pending'
    };

    const parentResponse = await fetch('/api/orchestration/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(parentTask)
    });

    if (!parentResponse.ok) {
      throw new Error('Failed to create workflow task');
    }

    const parentResult = await parentResponse.json();
    const parentId = parentResult.id;

    if (workflowData.steps && workflowData.steps.length > 0) {
      for (let i = 0; i < workflowData.steps.length; i++) {
        const step = workflowData.steps[i];
        const subtask = {
          workspace_id: state.selectedId,
          parent_id: parentId,
          name: step.name || `Step ${i + 1}`,
          description: step.description || '',
          to: step.assigned_to || 'unassigned',
          subtask_index: step.step_number || i + 1,
          status: 'pending'
        };

        const subtaskResponse = await fetch('/api/orchestration/tasks', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(subtask)
        });

        if (!subtaskResponse.ok) {
          console.error('Failed to create subtask', step);
        }
      }
    }

    return parentId;
  }

  /**
   * Bind all event handlers
   */
  function bindEvents() {
    const state = window.WorkspaceHubState.getState();

    if (elements.workspaceSelect) {
      elements.workspaceSelect.addEventListener('change', (event) => {
        const workspaceId = event.target.value;
        if (workspaceId) {
          selectWorkspace(workspaceId, { focus: true });
        } else {
          showLauncher();
        }
      });
    }

    if (elements.workspaceBrowseBtn) {
      elements.workspaceBrowseBtn.addEventListener('click', () => showLauncher());
    }

    if (elements.addTaskBtn) {
      elements.addTaskBtn.addEventListener('click', openTaskModal);
    }

    if (elements.refreshTasksBtn) {
      elements.refreshTasksBtn.addEventListener('click', () => {
        if (state.selectedId) {
          window.WorkspaceHubTasks.loadTasks(state.selectedId);
        }
      });
    }

    if (elements.importWorkflowBtn) {
      elements.importWorkflowBtn.addEventListener('click', openImportWorkflowModal);
    }

    if (elements.viewSchedulesBtn) {
      elements.viewSchedulesBtn.addEventListener('click', openSchedulePanel);
    }

    if (elements.launcherRefreshBtn) {
      elements.launcherRefreshBtn.addEventListener('click', () => loadWorkspaces());
    }

    if (elements.newSessionBtn) {
      elements.newSessionBtn.addEventListener('click', () => {
        if (!state.selectedId) return;
        if (window.sessionManager && typeof window.sessionManager.showCreateChatModalForWorkspace === 'function') {
          window.sessionManager.showCreateChatModalForWorkspace(state.selectedId);
        }
      });
    }

    if (elements.newNoteBtn) {
      elements.newNoteBtn.addEventListener('click', window.WorkspaceHubNotes.createNewNote);
    }

    if (elements.addFileBtn) {
      elements.addFileBtn.addEventListener('click', () => {
        if (state.selectedId) {
          window.WorkspaceHubFiles.openAddFileModal();
        }
      });
    }

    // Selection mode buttons
    if (elements.selectTasksBtn) {
      elements.selectTasksBtn.addEventListener('click', () =>
        window.WorkspaceHubSelection.toggleSelectionMode('tasks', () =>
          window.WorkspaceHubTasks.renderTasksList(state.tasks)));
    }
    if (elements.bulkDeleteTasksBtn) {
      elements.bulkDeleteTasksBtn.addEventListener('click', window.WorkspaceHubTasks.bulkDeleteTasks);
    }

    if (elements.selectSessionsBtn) {
      elements.selectSessionsBtn.addEventListener('click', () =>
        window.WorkspaceHubSelection.toggleSelectionMode('sessions', () =>
          window.WorkspaceHubSessions.renderSessions(state.sessions)));
    }
    if (elements.bulkDeleteSessionsBtn) {
      elements.bulkDeleteSessionsBtn.addEventListener('click', window.WorkspaceHubSessions.bulkDeleteSessions);
    }

    if (elements.selectNotesBtn) {
      elements.selectNotesBtn.addEventListener('click', () =>
        window.WorkspaceHubSelection.toggleSelectionMode('notes', () =>
          window.WorkspaceHubNotes.renderNotes(state.notes)));
    }
    if (elements.bulkDeleteNotesBtn) {
      elements.bulkDeleteNotesBtn.addEventListener('click', window.WorkspaceHubNotes.bulkDeleteNotes);
    }

    if (elements.selectFilesBtn) {
      elements.selectFilesBtn.addEventListener('click', () =>
        window.WorkspaceHubSelection.toggleSelectionMode('files', () =>
          window.WorkspaceHubFiles.renderFiles(state.files)));
    }
    if (elements.bulkTrashFilesBtn) {
      elements.bulkTrashFilesBtn.addEventListener('click', window.WorkspaceHubFiles.bulkMoveFilesToTrash);
    }

    // Initialize sub-module event bindings
    window.WorkspaceHubModals.bindModalEvents();
    window.WorkspaceHubSmartInput.bindEvents();
    window.WorkspaceHubFiles.bindFileUploadEvents();
  }

  // Subscribe to global events
  if (window.EventBus) {
    EventBus.on('workspace:files:updated', (data) => {
      const state = window.WorkspaceHubState.getState();
      if (!data?.workspaceId || data.workspaceId !== state.selectedId) return;
      window.WorkspaceHubFiles.loadFiles(state.selectedId);
    }, 'workspaceHub');
  }

  // Initialize
  bindEvents();
  loadWorkspaces();
})();

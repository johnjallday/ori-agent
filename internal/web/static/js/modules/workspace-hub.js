(function() {
  'use strict';

  const STORAGE_KEY = 'oriWorkspaceHubSelectedId';
  const SMART_INPUT_CLASSIFY_ENDPOINT = '/api/smart-input/classify';
  const SMART_INPUT_OVERRIDE_ENDPOINT = '/api/smart-input/override';

  const hubEl = document.getElementById('workspaceHub');
  if (!hubEl) return;

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

  const state = {
    workspaces: [],
    workspaceMap: new Map(),
    selectedId: null,
    tasks: [],
    stats: null,
    taskHierarchy: null,
    sessions: [],
    notes: [],
    files: [],
    smartInput: null,
    smartInputCancelled: false,
    // Selection mode state
    selectionMode: { tasks: false, sessions: false, notes: false, files: false },
    selectedItems: { tasks: new Set(), sessions: new Set(), notes: new Set(), files: new Set() },
    // File upload state
    pendingFiles: []
  };

  let workspaceRealtimeUnsub = null;
  let workspaceRealtimeTimer = null;

  function escapeHtml(text) {
    return String(text || '').replace(/[&<>"]/g, (ch) => ({
      '&': '&amp;',
      '<': '&lt;',
      '>': '&gt;',
      '"': '&quot;'
    }[ch]));
  }

  function formatDate(value) {
    if (!value) return '--';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '--';
    return date.toLocaleString();
  }

  function setState(nextState) {
    hubEl.dataset.state = nextState;
    if (elements.loadingOverlay) {
      elements.loadingOverlay.style.display = nextState === 'loading' ? 'flex' : 'none';
    }
  }

  function setSmartInputStatus(message, { busy = false } = {}) {
    if (!elements.smartInputStatus) return;
    elements.smartInputStatus.textContent = message || '';
    elements.smartInputStatus.classList.toggle('is-busy', busy);
  }

  function setSmartInputEnabled(enabled) {
    if (elements.smartInputField) elements.smartInputField.disabled = !enabled;
    if (elements.smartInputSubmit) elements.smartInputSubmit.disabled = !enabled;
    if (elements.smartInputCard) {
      elements.smartInputCard.classList.toggle('is-disabled', !enabled);
    }
  }

  function setSmartInputBusy(isBusy, message) {
    if (elements.smartInputField) elements.smartInputField.disabled = isBusy;
    if (elements.smartInputSubmit) elements.smartInputSubmit.disabled = isBusy;
    if (elements.smartInputCard) {
      elements.smartInputCard.dataset.state = isBusy ? 'deciding' : 'idle';
    }
    if (message !== undefined) {
      setSmartInputStatus(message, { busy: isBusy });
    } else if (isBusy) {
      setSmartInputStatus('Deciding...', { busy: true });
    } else {
      setSmartInputStatus('', { busy: false });
    }
  }

  function shouldRefreshForRealtimeEvent(type) {
    if (!type) return false;
    return type.startsWith('task.') ||
      type.startsWith('workspace.') ||
      type.startsWith('workflow.') ||
      type.startsWith('step.') ||
      type === 'connection.opened';
  }

  function scheduleWorkspaceTasksRefresh(delayMs = 500) {
    if (!state.selectedId) return;
    if (workspaceRealtimeTimer) {
      clearTimeout(workspaceRealtimeTimer);
    }
    workspaceRealtimeTimer = setTimeout(() => {
      workspaceRealtimeTimer = null;
      if (state.selectedId) {
        loadWorkspaceTasks(state.selectedId);
      }
    }, delayMs);
  }

  function stopWorkspaceRealtime() {
    if (workspaceRealtimeUnsub) {
      workspaceRealtimeUnsub();
      workspaceRealtimeUnsub = null;
    }
    if (workspaceRealtimeTimer) {
      clearTimeout(workspaceRealtimeTimer);
      workspaceRealtimeTimer = null;
    }
  }

  const SMART_INPUT_PROGRESS_STEPS = ['analyze', 'decide', 'execute'];
  let smartInputProgressModal = null;

  function getSmartInputProgressModal() {
    if (!elements.smartInputProgressModal || !window.bootstrap) return null;
    if (!smartInputProgressModal) {
      smartInputProgressModal = new bootstrap.Modal(elements.smartInputProgressModal);
    }
    return smartInputProgressModal;
  }

  function updateSmartInputProgress(step, { headline, message } = {}) {
    if (elements.smartInputProgressHeadline && headline) {
      elements.smartInputProgressHeadline.textContent = headline;
    }
    if (elements.smartInputProgressMessage && message) {
      elements.smartInputProgressMessage.textContent = message;
    }
    if (!elements.smartInputProgressSteps) return;

    const stepIndex = SMART_INPUT_PROGRESS_STEPS.indexOf(step);
    const items = Array.from(elements.smartInputProgressSteps.querySelectorAll('li'));
    items.forEach((item) => {
      const itemStep = item.dataset.step;
      const itemIndex = SMART_INPUT_PROGRESS_STEPS.indexOf(itemStep);
      item.classList.remove('is-active', 'is-complete');
      if (itemIndex === -1 || stepIndex === -1) return;
      if (itemIndex < stepIndex) item.classList.add('is-complete');
      if (itemIndex === stepIndex) item.classList.add('is-active');
    });
  }

  function showSmartInputProgress(step, { headline, message } = {}) {
    const modal = getSmartInputProgressModal();
    if (!modal) return;
    updateSmartInputProgress(step, { headline, message });
    modal.show();
  }

  function hideSmartInputProgress() {
    const modal = getSmartInputProgressModal();
    if (!modal) return;
    modal.hide();
  }

  function cancelSmartInput() {
    state.smartInputCancelled = true;
    hideSmartInputProgress();
    setSmartInputBusy(false);
    resetSmartInputPrompt();
    setSmartInputStatus('Cancelled', { busy: false });

    // Clear status after a moment
    setTimeout(() => {
      if (elements.smartInputStatus && elements.smartInputStatus.textContent === 'Cancelled') {
        setSmartInputStatus('');
      }
    }, 2000);

    if (window.Toast) {
      window.Toast.info('Operation cancelled');
    }
  }

  function setSmartInputDefaultDecision(decision) {
    const isTask = decision === 'task';
    const isChat = decision === 'chat';

    if (elements.smartInputPromptTask) {
      elements.smartInputPromptTask.classList.toggle('is-default', isTask);
    }
    if (elements.smartInputPromptChat) {
      elements.smartInputPromptChat.classList.toggle('is-default', isChat);
    }
    if (elements.smartInputPromptHint) {
      if (isTask) {
        elements.smartInputPromptHint.textContent = 'Suggested: Create Task';
      } else if (isChat) {
        elements.smartInputPromptHint.textContent = 'Suggested: Start Chat';
      } else {
        elements.smartInputPromptHint.textContent = '';
      }
    }
  }

  function hideSmartInputPrompt() {
    if (elements.smartInputPrompt) {
      elements.smartInputPrompt.hidden = true;
    }
  }

  function resetSmartInputPrompt() {
    state.smartInput = null;
    hideSmartInputPrompt();
    setSmartInputDefaultDecision(null);
  }

  function showSmartInputPrompt(payload) {
    if (!elements.smartInputPrompt) return;
    state.smartInput = {
      input: payload.input,
      predictedDecision: payload.decision,
      confidence: payload.confidence || 0,
      method: payload.method || 'fallback'
    };
    setSmartInputDefaultDecision(payload.decision);
    elements.smartInputPrompt.hidden = false;
    setSmartInputStatus(payload.message || 'Choose where to route this.', { busy: false });
  }

  function clearSmartInputField() {
    if (elements.smartInputField) {
      elements.smartInputField.value = '';
    }
  }

  // =============================================================================
  // Delete Confirmation Modal
  // =============================================================================

  let deleteConfirmResolve = null;

  function showDeleteConfirm(options) {
    return new Promise((resolve) => {
      deleteConfirmResolve = resolve;

      if (elements.deleteConfirmTitle) {
        elements.deleteConfirmTitle.textContent = options.title || 'Confirm Delete';
      }
      if (elements.deleteConfirmBody) {
        elements.deleteConfirmBody.textContent = options.message || 'Are you sure you want to delete the selected items?';
      }
      if (elements.deleteConfirmBtn) {
        elements.deleteConfirmBtn.textContent = options.variant === 'trash' ? 'Move to Trash' : 'Delete';
      }

      if (elements.deleteConfirmModal && window.bootstrap) {
        const modal = new bootstrap.Modal(elements.deleteConfirmModal);
        modal.show();
      }
    });
  }

  function handleDeleteConfirm() {
    if (deleteConfirmResolve) {
      deleteConfirmResolve(true);
      deleteConfirmResolve = null;
    }
    if (elements.deleteConfirmModal && window.bootstrap) {
      const modal = bootstrap.Modal.getInstance(elements.deleteConfirmModal);
      if (modal) modal.hide();
    }
  }

  function handleDeleteCancel() {
    if (deleteConfirmResolve) {
      deleteConfirmResolve(false);
      deleteConfirmResolve = null;
    }
  }

  // =============================================================================
  // Parent Task Delete Modal
  // =============================================================================

  let parentDeleteResolve = null;

  function showParentDeletePrompt(options) {
    return new Promise((resolve) => {
      parentDeleteResolve = resolve;

      if (elements.parentDeleteTitle) {
        elements.parentDeleteTitle.textContent = options.title || 'Delete Workflow';
      }
      if (elements.parentDeleteBody) {
        elements.parentDeleteBody.textContent = options.message || 'This workflow has subtasks. What would you like to do?';
      }

      if (elements.parentDeleteModal && window.bootstrap) {
        const modal = new bootstrap.Modal(elements.parentDeleteModal);
        modal.show();
      }
    });
  }

  function handleParentDeleteChoice(choice) {
    if (parentDeleteResolve) {
      parentDeleteResolve(choice);
      parentDeleteResolve = null;
    }
    if (elements.parentDeleteModal && window.bootstrap) {
      const modal = bootstrap.Modal.getInstance(elements.parentDeleteModal);
      if (modal) modal.hide();
    }
  }

  function handleParentDeleteCancel() {
    if (parentDeleteResolve) {
      parentDeleteResolve(null);
      parentDeleteResolve = null;
    }
  }

  // =============================================================================
  // Selection Mode Functions
  // =============================================================================

  function toggleSelectionMode(panelType) {
    const isEnabled = !state.selectionMode[panelType];
    state.selectionMode[panelType] = isEnabled;
    state.selectedItems[panelType].clear();

    const panelMap = {
      tasks: elements.tasksPanel,
      sessions: elements.sessionsPanel,
      notes: elements.notesPanel,
      files: elements.filesPanel
    };

    const panel = panelMap[panelType];
    if (panel) {
      panel.classList.toggle('selection-mode', isEnabled);
    }

    updateBulkActionsVisibility(panelType);

    // Re-render to show/hide checkboxes
    if (panelType === 'tasks') renderTasksList(state.tasks);
    if (panelType === 'sessions') renderSessions(state.sessions);
    if (panelType === 'notes') renderNotes(state.notes);
    if (panelType === 'files') renderFiles(state.files);
  }

  function toggleItemSelection(panelType, itemId) {
    const selectedSet = state.selectedItems[panelType];
    if (selectedSet.has(itemId)) {
      selectedSet.delete(itemId);
    } else {
      selectedSet.add(itemId);
    }

    // Update UI for selected item
    const selector = {
      tasks: '.hub-task-card',
      sessions: '.hub-session-item',
      notes: '.hub-note-item',
      files: '.hub-file-item'
    }[panelType];

    const idAttr = {
      tasks: 'data-task-id',
      sessions: 'data-session-id',
      notes: 'data-note-id',
      files: 'data-file-id'
    }[panelType];

    const item = document.querySelector(`${selector}[${idAttr}="${itemId}"]`);
    if (item) {
      item.classList.toggle('selected', selectedSet.has(itemId));
      const checkbox = item.querySelector('input[type="checkbox"]');
      if (checkbox) checkbox.checked = selectedSet.has(itemId);
    }

    updateBulkActionsVisibility(panelType);
  }

  function updateBulkActionsVisibility(panelType) {
    const count = state.selectedItems[panelType].size;
    const btnMap = {
      tasks: elements.bulkDeleteTasksBtn,
      sessions: elements.bulkDeleteSessionsBtn,
      notes: elements.bulkDeleteNotesBtn,
      files: elements.bulkTrashFilesBtn
    };

    const btn = btnMap[panelType];
    if (btn) {
      btn.style.display = count > 0 ? 'inline-flex' : 'none';
    }
  }

  // =============================================================================
  // Individual Delete Functions
  // =============================================================================

  async function deleteTask(taskId) {
    try {
      const subtasks = getSubtasksForParent(taskId);
      const isParent = subtasks.length > 0;

      if (isParent) {
        const choice = await showParentDeletePrompt({
          title: 'Delete Workflow',
          message: `This workflow has ${subtasks.length} subtask${subtasks.length === 1 ? '' : 's'}. What would you like to do?`
        });

        if (!choice) return;

        if (choice === 'delete_all') {
          for (const subtask of subtasks) {
            const response = await fetch(`/api/orchestration/tasks?id=${encodeURIComponent(subtask.id)}`, {
              method: 'DELETE'
            });
            if (!response.ok) {
              throw new Error('Failed to delete a subtask');
            }
          }
        } else if (choice === 'ungroup') {
          for (const subtask of subtasks) {
            const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(subtask.id)}`, {
              method: 'PUT',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({
                parent_task_id: '',
                subtask_index: 0
              })
            });
            if (!response.ok) {
              throw new Error('Failed to ungroup a subtask');
            }
          }
        }
      } else {
        const confirmed = await showDeleteConfirm({
          title: 'Delete Task',
          message: 'Are you sure you want to delete this task? This action cannot be undone.'
        });
        if (!confirmed) return;
      }

      const response = await fetch(`/api/orchestration/tasks?id=${encodeURIComponent(taskId)}`, {
        method: 'DELETE'
      });
      if (!response.ok) throw new Error('Failed to delete task');

      if (window.Toast) window.Toast.success('Task deleted');
      await loadWorkspaceTasks(state.selectedId);
    } catch (error) {
      console.error('Failed to delete task:', error);
      if (window.Toast) window.Toast.error('Failed to delete task');
    }
  }

  async function executeTask(taskId) {
    const task = state.tasks.find((item) => item.id === taskId);
    if (!task) return;

    const subtasks = getSubtasksForParent(taskId);
    const isParent = subtasks.length > 0;

    if (isParent) {
      const hasUnassigned = subtasks.some((subtask) => !subtask.to || subtask.to === 'unassigned');
      if (hasUnassigned) {
        if (window.Toast) {
          window.Toast.error('Assign agents to all subtasks before executing this workflow.');
        } else {
          alert('Assign agents to all subtasks before executing this workflow.');
        }
        return;
      }

      const hasRunning = subtasks.some((subtask) => subtask.status === 'in_progress');
      if (hasRunning) {
        if (window.Toast) {
          window.Toast.error('A subtask is already running.');
        } else {
          alert('A subtask is already running.');
        }
        return;
      }
    } else {
      const assignedAgent = task.to && task.to !== 'unassigned' ? task.to : '';
      if (!assignedAgent) {
        if (window.Toast) {
          window.Toast.error('Assign an agent before executing this task.');
        } else {
          alert('Assign an agent before executing this task.');
        }
        return;
      }
    }

    const confirmMessage = isParent
      ? `Execute this workflow (${subtasks.length} step${subtasks.length === 1 ? '' : 's'}) now?`
      : 'Execute this task now?';
    const confirmed = confirm(confirmMessage);
    if (!confirmed) return;

    try {
      const response = await fetch('/api/orchestration/tasks/execute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task_id: taskId })
      });

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to execute task');
      }

      if (window.Toast) window.Toast.success('Task started');
      await loadWorkspaceTasks(state.selectedId);
      pollTaskCompletion(taskId);
    } catch (error) {
      console.error('Failed to execute task:', error);
      if (window.Toast) {
        window.Toast.error('Failed to execute task');
      } else {
        alert('Failed to execute task');
      }
    }
  }

  async function exportWorkflowTask(task, taskId) {
    const subtasks = getSubtasksForParent(taskId);

    // Build workflow export structure
    const workflowExport = {
      name: task.name || task.description || 'Exported Workflow',
      description: task.description || '',
      category: 'imported',
      source: 'workspace',
      exported_at: new Date().toISOString(),
      steps: subtasks.map((subtask, index) => ({
        step_number: subtask.subtask_index || index + 1,
        name: subtask.name || subtask.description || `Step ${index + 1}`,
        description: subtask.description || '',
        assigned_to: subtask.to || 'unassigned',
        dependencies: subtask.depends_on || []
      }))
    };

    // Create and trigger download
    const blob = new Blob([JSON.stringify(workflowExport, null, 2)], { type: 'application/json' });
    const url = window.URL.createObjectURL(blob);
    const a = document.createElement('a');
    const safeName = (task.name || 'workflow').replace(/[^a-zA-Z0-9]/g, '-').toLowerCase();
    a.href = url;
    a.download = `workflow-${safeName}.json`;
    document.body.appendChild(a);
    a.click();
    window.URL.revokeObjectURL(url);
    document.body.removeChild(a);

    if (window.Toast) {
      window.Toast.success('Workflow exported');
    }
  }

  function saveTaskAsWorkflow(task, taskId) {
    const subtasks = getSubtasksForParent(taskId);

    if (subtasks.length === 0) {
      if (window.Toast) {
        window.Toast.error('Workflow must have at least one step');
      }
      return;
    }

    openSaveWorkflowModal(task, taskId, subtasks);
  }

  function openSaveWorkflowModal(task, taskId, subtasks) {
    // Create modal if it doesn't exist
    let modal = document.getElementById('hubSaveWorkflowModal');
    if (!modal) {
      modal = document.createElement('div');
      modal.id = 'hubSaveWorkflowModal';
      modal.className = 'modal fade';
      modal.tabIndex = -1;
      modal.innerHTML = `
        <div class="modal-dialog">
          <div class="modal-content">
            <div class="modal-header">
              <h5 class="modal-title" style="color: var(--text-primary);">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                  <path d="M17,3H5C3.89,3 3,3.9 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V7L17,3M19,19H5V5H16.17L19,7.83V19M12,12C10.34,12 9,13.34 9,15C9,16.66 10.34,18 12,18C13.66,18 15,16.66 15,15C15,13.34 13.66,12 12,12M6,6H15V10H6V6Z"/>
                </svg>
                Save to Workflow Library
              </h5>
              <button type="button" class="btn-close" data-bs-dismiss="modal"></button>
            </div>
            <div class="modal-body">
              <div class="mb-3">
                <label class="form-label" style="color: var(--text-primary);">Workflow Name</label>
                <input type="text" id="hubSaveWorkflowName" class="form-control" placeholder="Enter workflow name">
              </div>
              <div class="mb-3">
                <label class="form-label" style="color: var(--text-primary);">Description</label>
                <textarea id="hubSaveWorkflowDesc" class="form-control" rows="3" placeholder="Describe what this workflow does..."></textarea>
              </div>
              <div class="mb-3">
                <label class="form-label" style="color: var(--text-primary);">Category</label>
                <input type="text" id="hubSaveWorkflowCategory" class="form-control" placeholder="e.g., automation, data, reports">
              </div>
              <div class="modern-card p-3" style="background: var(--bg-secondary);">
                <div class="d-flex align-items-center gap-2 mb-2">
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" style="color: var(--text-secondary);">
                    <path d="M3,4H7V8H3V4M9,5V7H21V5H9M3,10H7V14H3V10M9,11V13H21V11H9M3,16H7V20H3V16M9,17V19H21V17H9"/>
                  </svg>
                  <span style="color: var(--text-secondary); font-size: 0.85rem;"><strong id="hubSaveWorkflowStepCount">0</strong> steps will be saved</span>
                </div>
                <div id="hubSaveWorkflowSteps" style="font-size: 0.8rem; color: var(--text-muted); max-height: 100px; overflow-y: auto;"></div>
              </div>
              <div id="hubSaveWorkflowError" class="alert alert-danger mt-3" style="display: none;"></div>
            </div>
            <div class="modal-footer">
              <button type="button" class="modern-btn modern-btn-secondary" data-bs-dismiss="modal">Cancel</button>
              <button type="button" id="hubConfirmSaveWorkflowBtn" class="modern-btn modern-btn-primary">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M17,3H5C3.89,3 3,3.9 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V7L17,3M19,19H5V5H16.17L19,7.83V19M12,12C10.34,12 9,13.34 9,15C9,16.66 10.34,18 12,18C13.66,18 15,16.66 15,15C15,13.34 13.66,12 12,12M6,6H15V10H6V6Z"/>
                </svg>
                Save to Library
              </button>
            </div>
          </div>
        </div>
      `;
      document.body.appendChild(modal);
    }

    // Populate modal
    const nameInput = modal.querySelector('#hubSaveWorkflowName');
    const descInput = modal.querySelector('#hubSaveWorkflowDesc');
    const categoryInput = modal.querySelector('#hubSaveWorkflowCategory');
    const stepCountEl = modal.querySelector('#hubSaveWorkflowStepCount');
    const stepsEl = modal.querySelector('#hubSaveWorkflowSteps');
    const errorEl = modal.querySelector('#hubSaveWorkflowError');
    const confirmBtn = modal.querySelector('#hubConfirmSaveWorkflowBtn');

    // Set default values
    nameInput.value = task.name || task.description || '';
    descInput.value = task.description || '';
    categoryInput.value = 'workspace';
    stepCountEl.textContent = subtasks.length;
    stepsEl.innerHTML = subtasks.map((s, i) =>
      `<div>${i + 1}. ${escapeHtml(s.name || s.description || 'Unnamed step')}</div>`
    ).join('');
    errorEl.style.display = 'none';

    // Remove old listener and add new one
    const newConfirmBtn = confirmBtn.cloneNode(true);
    confirmBtn.parentNode.replaceChild(newConfirmBtn, confirmBtn);

    newConfirmBtn.addEventListener('click', async () => {
      const name = nameInput.value.trim();
      if (!name) {
        errorEl.textContent = 'Please enter a workflow name';
        errorEl.style.display = 'block';
        return;
      }

      newConfirmBtn.disabled = true;
      newConfirmBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1"></span> Saving...';
      errorEl.style.display = 'none';

      try {
        const workflowData = {
          name: name,
          description: descInput.value.trim(),
          category: categoryInput.value.trim() || 'workspace',
          nodes: subtasks.map((subtask, index) => ({
            id: subtask.id || `node-${index}`,
            type: 'task',
            config: {
              name: subtask.name || subtask.description || `Step ${index + 1}`,
              description: subtask.description || '',
              to: subtask.to || 'unassigned'
            },
            relative_x: 0,
            relative_y: index * 100
          })),
          internal_connections: [],
          layout: {
            width: 300,
            height: subtasks.length * 100 + 100,
            node_positions: {}
          }
        };

        const response = await fetch('/api/workflows', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(workflowData)
        });

        if (!response.ok) {
          const text = await response.text();
          throw new Error(text || 'Failed to save workflow');
        }

        bootstrap.Modal.getInstance(modal).hide();
        if (window.Toast) {
          window.Toast.success(`Workflow "${name}" saved to library`);
        }
      } catch (error) {
        console.error('Failed to save workflow:', error);
        errorEl.textContent = 'Failed to save: ' + error.message;
        errorEl.style.display = 'block';
      } finally {
        newConfirmBtn.disabled = false;
        newConfirmBtn.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1"><path d="M17,3H5C3.89,3 3,3.9 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V7L17,3M19,19H5V5H16.17L19,7.83V19M12,12C10.34,12 9,13.34 9,15C9,16.66 10.34,18 12,18C13.66,18 15,16.66 15,15C15,13.34 13.66,12 12,12M6,6H15V10H6V6Z"/></svg> Save to Library';
      }
    });

    const bsModal = new bootstrap.Modal(modal);
    bsModal.show();

    // Focus on name input when modal opens
    modal.addEventListener('shown.bs.modal', () => nameInput.focus(), { once: true });
  }

  function openImportWorkflowModal() {
    if (!state.selectedId) {
      if (window.Toast) window.Toast.error('Please select a workspace first');
      return;
    }

    // Create modal if it doesn't exist
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

      // Set up file input handler
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
          loadWorkspaceTasks(state.selectedId);
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

  async function importWorkflowAsTask(workflowData) {
    // Create parent task for the workflow
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

    // Create subtasks for each step
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

  async function pollTaskCompletion(taskId, maxAttempts = 36, intervalMs = 5000) {
    let attempts = 0;

    const poll = async () => {
      attempts++;
      if (attempts > maxAttempts) return;

      try {
        const response = await fetch(`/api/orchestration/tasks?id=${encodeURIComponent(taskId)}`);
        if (!response.ok) {
          setTimeout(poll, intervalMs);
          return;
        }

        const task = await response.json();
        const status = task.status;
        if (status === 'completed' || status === 'failed' || status === 'cancelled' || status === 'timeout') {
          await loadWorkspaceTasks(state.selectedId);
          return;
        }
      } catch (error) {
        console.error('Failed to poll task status:', error);
      }

      setTimeout(poll, intervalMs);
    };

    setTimeout(poll, intervalMs);
  }

  async function deleteSession(sessionId) {
    const confirmed = await showDeleteConfirm({
      title: 'Delete Chat Session',
      message: 'Are you sure you want to delete this chat session and all its messages? This action cannot be undone.'
    });
    if (!confirmed) return;

    try {
      const response = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}`, {
        method: 'DELETE'
      });
      if (!response.ok) throw new Error('Failed to delete session');

      if (window.Toast) window.Toast.success('Session deleted');
      await loadWorkspaceSessions(state.selectedId);
    } catch (error) {
      console.error('Failed to delete session:', error);
      if (window.Toast) window.Toast.error('Failed to delete session');
    }
  }

  async function deleteNote(noteId) {
    const confirmed = await showDeleteConfirm({
      title: 'Delete Note',
      message: 'Are you sure you want to delete this note? This action cannot be undone.'
    });
    if (!confirmed) return;

    try {
      const response = await fetch(`/api/notes/${encodeURIComponent(noteId)}`, {
        method: 'DELETE'
      });
      if (!response.ok) throw new Error('Failed to delete note');

      if (window.Toast) window.Toast.success('Note deleted');
      await loadWorkspaceNotes(state.selectedId);
    } catch (error) {
      console.error('Failed to delete note:', error);
      if (window.Toast) window.Toast.error('Failed to delete note');
    }
  }

  async function moveFileToTrash(fileId) {
    const confirmed = await showDeleteConfirm({
      title: 'Move to Trash',
      message: 'Move this file to trash? You can restore it later from the canvas.',
      variant: 'trash'
    });
    if (!confirmed) return;

    try {
      const response = await fetch(`/api/studios/${encodeURIComponent(state.selectedId)}/attachments/${encodeURIComponent(fileId)}/trash`, {
        method: 'PATCH'
      });
      if (!response.ok) throw new Error('Failed to move file to trash');

      if (window.Toast) window.Toast.success('File moved to trash');
      await loadWorkspaceFiles(state.selectedId);
    } catch (error) {
      console.error('Failed to move file to trash:', error);
      if (window.Toast) window.Toast.error('Failed to move file to trash');
    }
  }

  // =============================================================================
  // Bulk Delete Functions
  // =============================================================================

  async function bulkDeleteTasks() {
    const ids = Array.from(state.selectedItems.tasks);
    if (ids.length === 0) return;

    const confirmed = await showDeleteConfirm({
      title: `Delete ${ids.length} Task${ids.length > 1 ? 's' : ''}`,
      message: `Are you sure you want to delete ${ids.length} task${ids.length > 1 ? 's' : ''}? This action cannot be undone.`
    });
    if (!confirmed) return;

    try {
      const response = await fetch('/api/orchestration/tasks/bulk', {
        method: 'DELETE',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task_ids: ids, workspace_id: state.selectedId })
      });
      if (!response.ok) throw new Error('Failed to delete tasks');

      const result = await response.json();
      if (window.Toast) {
        window.Toast.success(`Deleted ${result.success_count} task${result.success_count !== 1 ? 's' : ''}`);
      }

      toggleSelectionMode('tasks');
      await loadWorkspaceTasks(state.selectedId);
    } catch (error) {
      console.error('Failed to bulk delete tasks:', error);
      if (window.Toast) window.Toast.error('Failed to delete tasks');
    }
  }

  async function bulkDeleteSessions() {
    const ids = Array.from(state.selectedItems.sessions);
    if (ids.length === 0) return;

    const confirmed = await showDeleteConfirm({
      title: `Delete ${ids.length} Session${ids.length > 1 ? 's' : ''}`,
      message: `Are you sure you want to delete ${ids.length} chat session${ids.length > 1 ? 's' : ''} and all their messages? This action cannot be undone.`
    });
    if (!confirmed) return;

    try {
      const response = await fetch('/api/sessions/bulk', {
        method: 'DELETE',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_ids: ids })
      });
      if (!response.ok) throw new Error('Failed to delete sessions');

      const result = await response.json();
      if (window.Toast) {
        window.Toast.success(`Deleted ${result.success_count} session${result.success_count !== 1 ? 's' : ''}`);
      }

      toggleSelectionMode('sessions');
      await loadWorkspaceSessions(state.selectedId);
    } catch (error) {
      console.error('Failed to bulk delete sessions:', error);
      if (window.Toast) window.Toast.error('Failed to delete sessions');
    }
  }

  async function bulkDeleteNotes() {
    const ids = Array.from(state.selectedItems.notes);
    if (ids.length === 0) return;

    const confirmed = await showDeleteConfirm({
      title: `Delete ${ids.length} Note${ids.length > 1 ? 's' : ''}`,
      message: `Are you sure you want to delete ${ids.length} note${ids.length > 1 ? 's' : ''}? This action cannot be undone.`
    });
    if (!confirmed) return;

    try {
      const response = await fetch('/api/notes/bulk', {
        method: 'DELETE',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ note_ids: ids })
      });
      if (!response.ok) throw new Error('Failed to delete notes');

      const result = await response.json();
      if (window.Toast) {
        window.Toast.success(`Deleted ${result.success_count} note${result.success_count !== 1 ? 's' : ''}`);
      }

      toggleSelectionMode('notes');
      await loadWorkspaceNotes(state.selectedId);
    } catch (error) {
      console.error('Failed to bulk delete notes:', error);
      if (window.Toast) window.Toast.error('Failed to delete notes');
    }
  }

  async function bulkMoveFilesToTrash() {
    const ids = Array.from(state.selectedItems.files);
    if (ids.length === 0) return;

    const confirmed = await showDeleteConfirm({
      title: `Move ${ids.length} File${ids.length > 1 ? 's' : ''} to Trash`,
      message: `Move ${ids.length} file${ids.length > 1 ? 's' : ''} to trash? You can restore them later from the canvas.`,
      variant: 'trash'
    });
    if (!confirmed) return;

    try {
      const response = await fetch(`/api/studios/${encodeURIComponent(state.selectedId)}/attachments/bulk-trash`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ attachment_ids: ids })
      });
      if (!response.ok) throw new Error('Failed to move files to trash');

      const result = await response.json();
      if (window.Toast) {
        window.Toast.success(`Moved ${result.success_count} file${result.success_count !== 1 ? 's' : ''} to trash`);
      }

      toggleSelectionMode('files');
      await loadWorkspaceFiles(state.selectedId);
    } catch (error) {
      console.error('Failed to bulk move files to trash:', error);
      if (window.Toast) window.Toast.error('Failed to move files to trash');
    }
  }

  function flattenWorkspaces(workspaces, depth = 0, path = []) {
    const rows = [];
    workspaces.forEach((workspace) => {
      const currentPath = [...path, workspace.name || 'Untitled'];
      rows.push({
        ...workspace,
        depth,
        path: currentPath.join(' / ')
      });

      if (workspace.children && workspace.children.length > 0) {
        rows.push(...flattenWorkspaces(workspace.children, depth + 1, currentPath));
      }
    });
    return rows;
  }

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

  function computeStats(tasks) {
    const stats = {
      completed: 0,
      in_progress: 0,
      failed: 0,
      scheduled: 0
    };

    tasks.forEach((task) => {
      const status = task.status || 'pending';
      if (status === 'completed') stats.completed += 1;
      if (status === 'in_progress') stats.in_progress += 1;
      if (status === 'failed') stats.failed += 1;
      if (task.schedule_enabled) stats.scheduled += 1;
    });

    return stats;
  }

  function renderStats(stats) {
    if (!stats) return;
    if (elements.statCompleted) elements.statCompleted.textContent = stats.completed || 0;
    if (elements.statInProgress) elements.statInProgress.textContent = stats.in_progress || 0;
    if (elements.statFailed) elements.statFailed.textContent = stats.failed || 0;
    if (elements.statScheduled) elements.statScheduled.textContent = stats.scheduled || 0;
  }

  function buildTaskHierarchy(tasks) {
    const taskById = new Map();
    const subtasksByParent = new Map();
    const rootTasks = [];

    (tasks || []).forEach((task) => {
      if (task && task.id) {
        taskById.set(task.id, task);
      }
    });

    (tasks || []).forEach((task) => {
      if (!task || !task.id) return;
      const parentId = task.parent_task_id;
      if (parentId && taskById.has(parentId)) {
        if (!subtasksByParent.has(parentId)) {
          subtasksByParent.set(parentId, []);
        }
        subtasksByParent.get(parentId).push(task);
      } else {
        rootTasks.push(task);
      }
    });

    subtasksByParent.forEach((list) => {
      list.sort((a, b) => {
        const aIndex = Number.isFinite(a.subtask_index) && a.subtask_index > 0 ? a.subtask_index : Number.MAX_SAFE_INTEGER;
        const bIndex = Number.isFinite(b.subtask_index) && b.subtask_index > 0 ? b.subtask_index : Number.MAX_SAFE_INTEGER;
        if (aIndex !== bIndex) return aIndex - bIndex;
        const aTime = a.created_at ? new Date(a.created_at).getTime() : 0;
        const bTime = b.created_at ? new Date(b.created_at).getTime() : 0;
        if (aTime !== bTime) return aTime - bTime;
        return String(a.id || '').localeCompare(String(b.id || ''));
      });
    });

    return { taskById, subtasksByParent, rootTasks };
  }

  function getSubtasksForParent(taskId) {
    if (!taskId) return [];
    const hierarchy = state.taskHierarchy || buildTaskHierarchy(state.tasks || []);
    state.taskHierarchy = hierarchy;
    return hierarchy.subtasksByParent.get(taskId) || [];
  }

  function getDisplayStatus(task, subtasks) {
    if (!subtasks || subtasks.length === 0) return task.status || 'pending';

    const statuses = subtasks.map((subtask) => subtask.status || 'pending');
    if (statuses.some((status) => status === 'in_progress')) return 'in_progress';
    if (statuses.some((status) => status === 'failed')) return 'failed';
    if (statuses.some((status) => status === 'timeout')) return 'timeout';
    if (statuses.some((status) => status === 'cancelled')) return 'cancelled';
    if (statuses.every((status) => status === 'completed')) return 'completed';
    if (statuses.some((status) => status === 'assigned')) return 'assigned';
    return task.status || 'pending';
  }

  function getDisplayResult(task, subtasks) {
    if (task.error) return { label: 'Error', text: task.error };
    if (task.result) return { label: 'Result', text: task.result };
    if (subtasks && subtasks.length > 0) {
      const lastSubtask = subtasks[subtasks.length - 1];
      if (lastSubtask.error) return { label: 'Error', text: lastSubtask.error };
      if (lastSubtask.result) return { label: 'Result', text: lastSubtask.result };
    }
    return null;
  }

  function renderTasksList(tasks) {
    if (!elements.tasksList) return;

    if (!tasks || tasks.length === 0) {
      elements.tasksList.innerHTML = '<div class="hub-empty">No tasks yet. Create the first one to get started.</div>';
      if (elements.tasksSubtitle) {
        elements.tasksSubtitle.textContent = 'No tasks created yet.';
      }
      return;
    }

    state.taskHierarchy = buildTaskHierarchy(tasks);
    const { rootTasks, subtasksByParent, taskById } = state.taskHierarchy;
    const parentCount = rootTasks.filter((task) => subtasksByParent.has(task.id)).length;
    const subtaskCount = Array.from(subtasksByParent.values()).reduce((total, list) => total + list.length, 0);
    const standaloneCount = rootTasks.length - parentCount;

    if (elements.tasksSubtitle) {
      const parts = [];
      if (parentCount) {
        parts.push(`${parentCount} workflow${parentCount === 1 ? '' : 's'}`);
      }
      if (standaloneCount) {
        parts.push(`${standaloneCount} task${standaloneCount === 1 ? '' : 's'}`);
      }
      if (subtaskCount) {
        parts.push(`${subtaskCount} subtask${subtaskCount === 1 ? '' : 's'}`);
      }
      elements.tasksSubtitle.textContent = parts.length > 0
        ? `${parts.join(' | ')} in this workspace.`
        : `${tasks.length} task${tasks.length === 1 ? '' : 's'} queued for this workspace.`;
    }

    const inSelectionMode = state.selectionMode.tasks;
    const selectedSet = state.selectedItems.tasks;

    const renderTaskCard = (task, {
      isParent = false,
      isSubtask = false,
      subtasks = [],
      stepNumber = null,
      parentId = ''
    } = {}) => {
      const status = isParent ? getDisplayStatus(task, subtasks) : (task.status || 'pending');
      const statusLabel = status.replace('_', ' ');
      const scheduleLabel = isParent
        ? 'Workflow container'
        : task.schedule_enabled ? `Next run: ${formatDate(task.next_run)}` : 'Not scheduled';
      const assignedAgent = task.to && task.to !== 'unassigned' ? task.to : '';
      const assignment = isParent ? `${subtasks.length} step${subtasks.length === 1 ? '' : 's'}` : (assignedAgent || 'unassigned');
      const isSelected = selectedSet.has(task.id);
      const hasUnassignedSubtasks = isParent && subtasks.some((subtask) => !subtask.to || subtask.to === 'unassigned');
      const hasRunningSubtasks = isParent && subtasks.some((subtask) => subtask.status === 'in_progress');
      const canExecute = isParent
        ? subtasks.length > 0 && !hasUnassignedSubtasks && !hasRunningSubtasks
        : Boolean(assignedAgent) && status !== 'in_progress';
      const executeLabel = status === 'completed' || status === 'failed' ? 'Re-run' : (isParent ? 'Run All' : 'Execute');
      const executeTitle = isParent
        ? hasUnassignedSubtasks
          ? 'Assign agents to all subtasks before executing'
          : hasRunningSubtasks
            ? 'A subtask is already running'
            : executeLabel === 'Re-run'
              ? 'Re-run workflow'
              : 'Execute workflow now'
        : !assignedAgent
          ? 'Assign an agent before executing'
          : status === 'in_progress'
            ? 'Task is already running'
            : executeLabel === 'Re-run'
              ? 'Re-execute task'
              : 'Execute task now';
      const resultData = getDisplayResult(task, isParent ? subtasks : null);
      const stepBadge = isSubtask
        ? `<span class="hub-task-step">Step ${escapeHtml(stepNumber || '')}</span>`
        : isParent ? '<span class="hub-task-badge">Workflow</span>' : '';
      const toggleButton = isParent
        ? `
          <button class="hub-task-toggle" data-action="toggle-subtasks" aria-label="Toggle subtasks">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
              <path d="M7,10L12,15L17,10H7Z"/>
            </svg>
          </button>
        `
        : '';

      return `
        <div class="hub-task-card${isSelected ? ' selected' : ''}${isParent ? ' hub-task-parent' : ''}${isSubtask ? ' hub-task-subtask' : ''}" data-task-id="${escapeHtml(task.id)}"${parentId ? ` data-parent-id="${escapeHtml(parentId)}"` : ''}>
          <div class="hub-item-checkbox">
            <input type="checkbox" ${isSelected ? 'checked' : ''} aria-label="Select task">
          </div>
          <div class="hub-task-content">
            <div class="hub-task-header">
              <div class="hub-task-title">
                ${toggleButton}
                ${stepBadge}
                <span>${escapeHtml(task.name || task.description || task.id)}</span>
              </div>
              <span class="hub-task-status status-${escapeHtml(status)}">${escapeHtml(statusLabel)}</span>
            </div>
            <div class="hub-task-meta">
              <span>${escapeHtml(assignment)}</span>
              <span>${escapeHtml(scheduleLabel)}</span>
            </div>
            <div class="hub-task-actions">
              <button class="modern-btn modern-btn-secondary" data-action="edit">Edit</button>
              ${isParent ? `<button class="modern-btn modern-btn-secondary" data-action="view-canvas" title="View workflow in canvas">View</button>` : ''}
              ${isParent ? `<button class="modern-btn modern-btn-secondary" data-action="save-workflow" title="Save to Workflows library">Save</button>` : ''}
              ${isParent ? `<button class="modern-btn modern-btn-secondary" data-action="export" title="Export workflow to file">Export</button>` : ''}
              <button class="modern-btn modern-btn-primary" data-action="execute" ${canExecute ? '' : 'disabled'} title="${escapeHtml(executeTitle)}">${escapeHtml(executeLabel)}</button>
            </div>
            ${resultData ? `
            <details class="hub-task-result">
              <summary>${escapeHtml(resultData.label)}</summary>
              <pre>${escapeHtml(resultData.text)}</pre>
            </details>
            ` : ''}
          </div>
          <button class="hub-item-delete-btn" data-action="delete" title="Delete task">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
            </svg>
          </button>
        </div>
      `;
    };

    const items = rootTasks.map((task) => {
      const subtasks = subtasksByParent.get(task.id) || [];
      const isParent = subtasks.length > 0;
      const parentCard = renderTaskCard(task, { isParent, subtasks });

      if (!isParent) {
        return parentCard;
      }

      const subtaskCards = subtasks.map((subtask, index) => {
        const stepNumber = subtask.subtask_index || index + 1;
        return renderTaskCard(subtask, {
          isSubtask: true,
          stepNumber,
          parentId: task.id
        });
      });

      return `
        <div class="hub-task-group" data-parent-id="${escapeHtml(task.id)}">
          ${parentCard}
          <div class="hub-subtask-list">
            ${subtaskCards.join('')}
          </div>
        </div>
      `;
    });

    elements.tasksList.innerHTML = items.join('');

    elements.tasksList.querySelectorAll('.hub-task-card').forEach((card) => {
      const taskId = card.dataset.taskId;
      const task = taskById.get(taskId);

      // Handle checkbox click in selection mode
      const checkbox = card.querySelector('input[type="checkbox"]');
      if (checkbox) {
        checkbox.addEventListener('change', (event) => {
          event.stopPropagation();
          toggleItemSelection('tasks', taskId);
        });
      }

      // Handle card click in selection mode
      card.addEventListener('click', (event) => {
        if (inSelectionMode && !event.target.closest('button') && !event.target.closest('input')) {
          toggleItemSelection('tasks', taskId);
        }
      });

      // Delete button
      card.querySelectorAll('[data-action="delete"]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.stopPropagation();
          deleteTask(taskId);
        });
      });

      card.querySelectorAll('[data-action="toggle-subtasks"]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.stopPropagation();
          const group = card.closest('.hub-task-group');
          if (group) {
            group.classList.toggle('is-collapsed');
          }
        });
      });

      card.querySelectorAll('[data-action="edit"]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.stopPropagation();
          if (window.taskModalController && task) {
            window.taskModalController.openForEdit(task, () => loadWorkspaceTasks(state.selectedId));
          }
        });
      });

      card.querySelectorAll('[data-action="execute"]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.stopPropagation();
          if (task && !btn.disabled) {
            executeTask(taskId);
          }
        });
      });

      card.querySelectorAll('[data-action="export"]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.stopPropagation();
          if (task) {
            exportWorkflowTask(task, taskId);
          }
        });
      });

      card.querySelectorAll('[data-action="save-workflow"]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.stopPropagation();
          if (task) {
            saveTaskAsWorkflow(task, taskId);
          }
        });
      });

      card.querySelectorAll('[data-action="view-canvas"]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.stopPropagation();
          if (task && state.selectedId) {
            window.location.href = `/workspaces/${state.selectedId}/canvas?workflow=${taskId}`;
          }
        });
      });
    });
  }

  function renderSchedules(tasks) {
    if (!elements.schedulesList) return;

    const scheduled = (tasks || []).filter((task) => task.schedule_enabled);

    if (scheduled.length === 0) {
      elements.schedulesList.innerHTML = '<div class="hub-empty">No scheduled tasks yet.</div>';
      return;
    }

    const sorted = scheduled.sort((a, b) => {
      const aTime = a.next_run ? new Date(a.next_run).getTime() : Number.MAX_SAFE_INTEGER;
      const bTime = b.next_run ? new Date(b.next_run).getTime() : Number.MAX_SAFE_INTEGER;
      return aTime - bTime;
    });

    const items = sorted.slice(0, 5).map((task) => {
      const nextRun = task.next_run ? formatDate(task.next_run) : 'Not scheduled';
      return `
        <div class="hub-schedule-item">
          <div>
            <div class="hub-schedule-title">${escapeHtml(task.name || task.description || task.id)}</div>
            <div class="hub-schedule-subtitle">${escapeHtml(nextRun)}</div>
          </div>
          <span class="hub-schedule-status">${escapeHtml(task.status || 'pending')}</span>
        </div>
      `;
    });

    elements.schedulesList.innerHTML = items.join('');
  }

  function renderSessions(sessions) {
    if (!elements.sessionsList) return;

    if (!sessions || sessions.length === 0) {
      elements.sessionsList.innerHTML = '<div class="hub-empty">No chat sessions yet.</div>';
      return;
    }

    const inSelectionMode = state.selectionMode.sessions;
    const selectedSet = state.selectedItems.sessions;

    const items = sessions.map((session) => {
      const title = session.title || session.name || 'Untitled Chat';
      const agent = session.agent_name || 'default';
      const updated = formatDate(session.updated_at || session.created_at);
      const messageCount = session.message_count || 0;
      const isSelected = selectedSet.has(session.id);

      return `
        <div class="hub-session-item${isSelected ? ' selected' : ''}" data-session-id="${escapeHtml(session.id)}">
          <div class="hub-item-checkbox">
            <input type="checkbox" ${isSelected ? 'checked' : ''} aria-label="Select session">
          </div>
          <div class="hub-session-info">
            <div class="hub-session-title">${escapeHtml(title)}</div>
            <div class="hub-session-meta">
              <span class="hub-session-agent">${escapeHtml(agent)}</span>
              <span>${messageCount} message${messageCount === 1 ? '' : 's'}</span>
              <span>${escapeHtml(updated)}</span>
            </div>
          </div>
          <button class="modern-btn modern-btn-secondary hub-session-open" data-action="open">Open</button>
          <button class="hub-item-delete-btn" data-action="delete" title="Delete session">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
            </svg>
          </button>
        </div>
      `;
    });

    elements.sessionsList.innerHTML = items.join('');

    elements.sessionsList.querySelectorAll('.hub-session-item').forEach((item) => {
      const sessionId = item.dataset.sessionId;

      // Handle checkbox click in selection mode
      const checkbox = item.querySelector('input[type="checkbox"]');
      if (checkbox) {
        checkbox.addEventListener('change', (event) => {
          event.stopPropagation();
          toggleItemSelection('sessions', sessionId);
        });
      }

      // Delete button
      item.querySelector('[data-action="delete"]')?.addEventListener('click', (event) => {
        event.stopPropagation();
        deleteSession(sessionId);
      });

      item.querySelector('[data-action="open"]')?.addEventListener('click', (event) => {
        event.stopPropagation();
        openSession(sessionId);
      });

      item.addEventListener('click', (event) => {
        if (inSelectionMode && !event.target.closest('button') && !event.target.closest('input')) {
          toggleItemSelection('sessions', sessionId);
        } else if (!inSelectionMode && !event.target.closest('button')) {
          openSession(sessionId);
        }
      });
    });
  }

  async function loadWorkspaceSessions(workspaceId) {
    if (!workspaceId) return;

    if (elements.sessionsList) {
      elements.sessionsList.innerHTML = '<div class="hub-loading">Loading sessions...</div>';
    }

    try {
      const response = await fetch(`/api/sessions?folder_id=${encodeURIComponent(workspaceId)}`);
      if (!response.ok) throw new Error('Failed to load sessions');

      const data = await response.json();
      state.sessions = data.sessions || [];
      renderSessions(state.sessions);
    } catch (error) {
      console.error('Workspace hub failed to load sessions:', error);
      if (elements.sessionsList) {
        elements.sessionsList.innerHTML = '<div class="hub-empty">Unable to load sessions.</div>';
      }
    }
  }

  function openSession(sessionId) {
    if (window.chatPanel && typeof window.chatPanel.open === 'function') {
      window.chatPanel.open();
    }

    if (window.sessionManager && typeof window.sessionManager.switchToSession === 'function') {
      window.sessionManager.switchToSession(sessionId);
    }
  }

  function renderNotes(notes) {
    if (!elements.notesList) return;

    if (!notes || notes.length === 0) {
      elements.notesList.innerHTML = '<div class="hub-empty">No notes yet.</div>';
      return;
    }

    const inSelectionMode = state.selectionMode.notes;
    const selectedSet = state.selectedItems.notes;

    const items = notes.slice(0, 5).map((note) => {
      const title = note.name || 'Untitled Note';
      const updated = formatDate(note.updated_at || note.created_at);
      const preview = note.content ? note.content.substring(0, 80).replace(/\n/g, ' ') : '';
      const isSelected = selectedSet.has(note.id);

      return `
        <div class="hub-note-item${isSelected ? ' selected' : ''}" data-note-id="${escapeHtml(note.id)}">
          <div class="hub-item-checkbox">
            <input type="checkbox" ${isSelected ? 'checked' : ''} aria-label="Select note">
          </div>
          <div class="hub-note-info">
            <div class="hub-note-title">${escapeHtml(title)}</div>
            <div class="hub-note-preview">${escapeHtml(preview)}${note.content && note.content.length > 80 ? '...' : ''}</div>
            <div class="hub-note-meta">${escapeHtml(updated)}</div>
          </div>
          <button class="modern-btn modern-btn-secondary hub-note-open" data-action="open">Open</button>
          <button class="hub-item-delete-btn" data-action="delete" title="Delete note">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
            </svg>
          </button>
        </div>
      `;
    });

    elements.notesList.innerHTML = items.join('');

    elements.notesList.querySelectorAll('.hub-note-item').forEach((item) => {
      const noteId = item.dataset.noteId;

      // Handle checkbox click in selection mode
      const checkbox = item.querySelector('input[type="checkbox"]');
      if (checkbox) {
        checkbox.addEventListener('change', (event) => {
          event.stopPropagation();
          toggleItemSelection('notes', noteId);
        });
      }

      // Delete button
      item.querySelector('[data-action="delete"]')?.addEventListener('click', (event) => {
        event.stopPropagation();
        deleteNote(noteId);
      });

      item.querySelector('[data-action="open"]')?.addEventListener('click', (event) => {
        event.stopPropagation();
        openNote(noteId);
      });

      item.addEventListener('click', (event) => {
        if (inSelectionMode && !event.target.closest('button') && !event.target.closest('input')) {
          toggleItemSelection('notes', noteId);
        } else if (!inSelectionMode && !event.target.closest('button')) {
          openNote(noteId);
        }
      });
    });
  }

  async function loadWorkspaceNotes(workspaceId) {
    if (!workspaceId) return;

    if (elements.notesList) {
      elements.notesList.innerHTML = '<div class="hub-loading">Loading notes...</div>';
    }

    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/notes`);
      if (!response.ok) throw new Error('Failed to load notes');

      const data = await response.json();
      state.notes = data.notes || [];
      renderNotes(state.notes);
    } catch (error) {
      console.error('Workspace hub failed to load notes:', error);
      if (elements.notesList) {
        elements.notesList.innerHTML = '<div class="hub-empty">Unable to load notes.</div>';
      }
    }
  }

  function openNote(noteId) {
    if (window.sessionManager && typeof window.sessionManager.openNoteEditorModal === 'function') {
      const note = state.notes.find(n => n.id === noteId);
      if (note) {
        window.sessionManager.openNoteEditorModal(note);
      }
    }
  }

  function createNewNote() {
    if (!state.selectedId) return;
    if (window.sessionManager && typeof window.sessionManager.openNoteCreateModal === 'function') {
      window.sessionManager.openNoteCreateModal(state.selectedId);
    }
  }

  async function createQuickNote(content) {
    if (!state.selectedId) return;

    setSmartInputBusy(true, 'Creating note...');

    try {
      // Generate a title from the first line or first few words
      const firstLine = content.split('\n')[0];
      const title = firstLine.length > 50 ? firstLine.slice(0, 47) + '...' : firstLine;

      const response = await fetch(`/api/workspaces/${state.selectedId}/notes`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: title || 'Quick Note',
          content: content
        })
      });

      if (!response.ok) {
        throw new Error('Failed to create note');
      }

      // Clear input and show success
      if (elements.smartInputField) {
        elements.smartInputField.value = '';
      }
      setSmartInputBusy(false);
      setSmartInputStatus('');

      if (window.Toast) {
        window.Toast.success('Note created');
      }

      // Refresh notes list
      await loadWorkspaceNotes(state.selectedId);

    } catch (error) {
      console.error('Failed to create quick note:', error);
      setSmartInputBusy(false);
      setSmartInputStatus('Failed to create note', { busy: false });
      if (window.Toast) {
        window.Toast.error('Failed to create note');
      }
    }
  }

  async function createQuickTask(description) {
    if (!state.selectedId) return;

    setSmartInputBusy(true, 'Creating task...');

    try {
      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: state.selectedId,
          description: description,
          name: description.length > 100 ? description.slice(0, 97) + '...' : description,
          status: 'pending'
        })
      });

      if (!response.ok) {
        throw new Error('Failed to create task');
      }

      // Clear input and show success
      if (elements.smartInputField) {
        elements.smartInputField.value = '';
      }
      setSmartInputBusy(false);
      setSmartInputStatus('');

      if (window.Toast) {
        window.Toast.success('Task created');
      }

      // Refresh tasks list
      await loadWorkspaceTasks(state.selectedId);

    } catch (error) {
      console.error('Failed to create quick task:', error);
      setSmartInputBusy(false);
      setSmartInputStatus('Failed to create task', { busy: false });
      if (window.Toast) {
        window.Toast.error('Failed to create task');
      }
    }
  }

  async function createQuickChat(initialMessage) {
    if (!state.selectedId) return;

    setSmartInputBusy(true, 'Starting chat...');

    try {
      // Use sessionManager to create chat if available
      if (window.sessionManager && typeof window.sessionManager.createChatSession === 'function') {
        const session = await window.sessionManager.createChatSession(state.selectedId, {
          title: initialMessage ? initialMessage.slice(0, 50) : 'New Chat',
          initialMessage: initialMessage || null
        });

        // Clear input
        if (elements.smartInputField) {
          elements.smartInputField.value = '';
        }
        setSmartInputBusy(false);
        setSmartInputStatus('');

        if (session && session.id) {
          // Navigate to chat
          window.location.href = `/chat/${session.id}`;
        }
        return;
      }

      // Fallback: create session via API directly
      const response = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: state.selectedId,
          title: initialMessage ? initialMessage.slice(0, 50) : 'New Chat'
        })
      });

      if (!response.ok) {
        throw new Error('Failed to create chat');
      }

      const data = await response.json();

      // Clear input
      if (elements.smartInputField) {
        elements.smartInputField.value = '';
      }
      setSmartInputBusy(false);
      setSmartInputStatus('');

      // Navigate to chat
      if (data.session && data.session.id) {
        window.location.href = `/chat/${data.session.id}`;
      } else if (data.id) {
        window.location.href = `/chat/${data.id}`;
      }

    } catch (error) {
      console.error('Failed to create quick chat:', error);
      setSmartInputBusy(false);
      setSmartInputStatus('Failed to start chat', { busy: false });
      if (window.Toast) {
        window.Toast.error('Failed to start chat');
      }
    }
  }

  function openFileAttachmentModal() {
    // Clear the input field
    if (elements.smartInputField) {
      elements.smartInputField.value = '';
    }
    setSmartInputStatus('');

    // Open the add file modal
    if (elements.addFileModal && window.bootstrap) {
      const modal = new bootstrap.Modal(elements.addFileModal);
      modal.show();
    } else if (window.Toast) {
      window.Toast.info('Use the + button in the Files panel to add files');
    }
  }

  function renderFiles(files) {
    if (!elements.filesList) return;

    if (!files || files.length === 0) {
      elements.filesList.innerHTML = '<div class="hub-empty">No files yet.</div>';
      return;
    }

    const inSelectionMode = state.selectionMode.files;
    const selectedSet = state.selectedItems.files;

    const getFileIcon = (type, mime) => {
      if (type === 'image' || (mime && mime.startsWith('image/'))) {
        return '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M8.5,13.5L11,16.5L14.5,12L19,18H5M21,19V5C21,3.89 20.1,3 19,3H5A2,2 0 0,0 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19Z"/></svg>';
      }
      if (type === 'doc' || (mime && (mime.includes('text') || mime.includes('document')))) {
        return '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M14,17H7V15H14M17,13H7V11H17M17,9H7V7H17M19,3H5C3.89,3 3,3.89 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V5C21,3.89 20.1,3 19,3Z"/></svg>';
      }
      return '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M13,9V3.5L18.5,9M6,2C4.89,2 4,2.89 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2H6Z"/></svg>';
    };

    const formatFileSize = (bytes) => {
      if (!bytes) return '';
      if (bytes < 1024) return bytes + ' B';
      if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
      return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
    };

    const items = files.slice(0, 5).map((file) => {
      const title = file.title || (file.file_meta && file.file_meta.name) || 'Untitled File';
      const size = file.file_meta ? formatFileSize(file.file_meta.size) : '';
      const icon = getFileIcon(file.type, file.file_meta?.mime);
      const isSelected = selectedSet.has(file.id);

      return `
        <div class="hub-file-item${isSelected ? ' selected' : ''}" data-file-id="${escapeHtml(file.id)}">
          <div class="hub-item-checkbox">
            <input type="checkbox" ${isSelected ? 'checked' : ''} aria-label="Select file">
          </div>
          <div class="hub-file-icon">${icon}</div>
          <div class="hub-file-info">
            <div class="hub-file-title">${escapeHtml(title)}</div>
            ${size ? `<div class="hub-file-meta">${escapeHtml(size)}</div>` : ''}
          </div>
          <button class="hub-item-delete-btn" data-action="trash" title="Move to trash">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
            </svg>
          </button>
        </div>
      `;
    });

    elements.filesList.innerHTML = items.join('');

    elements.filesList.querySelectorAll('.hub-file-item').forEach((item) => {
      const fileId = item.dataset.fileId;

      // Handle checkbox click in selection mode
      const checkbox = item.querySelector('input[type="checkbox"]');
      if (checkbox) {
        checkbox.addEventListener('change', (event) => {
          event.stopPropagation();
          toggleItemSelection('files', fileId);
        });
      }

      // Trash button
      item.querySelector('[data-action="trash"]')?.addEventListener('click', (event) => {
        event.stopPropagation();
        moveFileToTrash(fileId);
      });

      item.addEventListener('click', (event) => {
        if (inSelectionMode && !event.target.closest('button') && !event.target.closest('input')) {
          toggleItemSelection('files', fileId);
        } else if (!inSelectionMode && !event.target.closest('button')) {
          openFile(fileId);
        }
      });
    });
  }

  function openFile(fileId) {
    // Open the canvas to view/edit the file attachment
    if (state.selectedId) {
      window.location.href = `/workspaces/${encodeURIComponent(state.selectedId)}/canvas`;
    }
  }

  // =============================================================================
  // Add File Modal Functions
  // =============================================================================

  function openAddFileModal() {
    if (!state.selectedId) return;

    // Reset modal state
    state.pendingFiles = [];
    if (elements.fileTitle) elements.fileTitle.value = '';
    if (elements.fileNotes) elements.fileNotes.value = '';
    if (elements.selectedFilesPreview) elements.selectedFilesPreview.style.display = 'none';
    if (elements.selectedFilesList) elements.selectedFilesList.innerHTML = '';
    if (elements.fileUploadProgress) elements.fileUploadProgress.style.display = 'none';
    if (elements.addFileSubmitBtn) elements.addFileSubmitBtn.disabled = true;

    if (elements.addFileModal && window.bootstrap) {
      const modal = new bootstrap.Modal(elements.addFileModal);
      modal.show();
    }
  }

  function updateSelectedFilesPreview() {
    if (!elements.selectedFilesPreview || !elements.selectedFilesList) return;

    if (state.pendingFiles.length === 0) {
      elements.selectedFilesPreview.style.display = 'none';
      if (elements.addFileSubmitBtn) elements.addFileSubmitBtn.disabled = true;
      return;
    }

    elements.selectedFilesPreview.style.display = 'block';
    if (elements.addFileSubmitBtn) elements.addFileSubmitBtn.disabled = false;

    const formatFileSize = (bytes) => {
      if (!bytes) return '';
      if (bytes < 1024) return bytes + ' B';
      if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
      return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
    };

    const items = state.pendingFiles.map((file, index) => `
      <div class="hub-selected-file-item" data-index="${index}">
        <div class="hub-selected-file-info">
          <span class="hub-selected-file-name">${escapeHtml(file.name)}</span>
          <span class="hub-selected-file-size">${formatFileSize(file.size)}</span>
        </div>
        <button type="button" class="hub-selected-file-remove" data-index="${index}" title="Remove">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19,6.41L17.59,5L12,10.59L6.41,5L5,6.41L10.59,12L5,17.59L6.41,19L12,13.41L17.59,19L19,17.59L13.41,12L19,6.41Z"/>
          </svg>
        </button>
      </div>
    `);

    elements.selectedFilesList.innerHTML = items.join('');

    // Bind remove button handlers
    elements.selectedFilesList.querySelectorAll('.hub-selected-file-remove').forEach((btn) => {
      btn.addEventListener('click', (event) => {
        event.stopPropagation();
        const index = parseInt(btn.dataset.index, 10);
        state.pendingFiles.splice(index, 1);
        updateSelectedFilesPreview();
      });
    });
  }

  function handleFileDropZoneDragOver(event) {
    event.preventDefault();
    event.stopPropagation();
    if (elements.fileDropZone) {
      elements.fileDropZone.classList.add('drag-active');
    }
  }

  function handleFileDropZoneDragLeave(event) {
    event.preventDefault();
    event.stopPropagation();
    if (elements.fileDropZone) {
      elements.fileDropZone.classList.remove('drag-active');
    }
  }

  function handleFileDropZoneDrop(event) {
    event.preventDefault();
    event.stopPropagation();
    if (elements.fileDropZone) {
      elements.fileDropZone.classList.remove('drag-active');
    }

    const files = event.dataTransfer?.files;
    if (files && files.length > 0) {
      addFilesToPending(Array.from(files));
    }
  }

  function handleFileInputChange(event) {
    const files = event.target?.files;
    if (files && files.length > 0) {
      addFilesToPending(Array.from(files));
      event.target.value = ''; // Reset input
    }
  }

  function addFilesToPending(files) {
    // Max file size: 10MB
    const maxSize = 10 * 1024 * 1024;

    files.forEach((file) => {
      if (file.size > maxSize) {
        if (window.Toast) {
          window.Toast.warning(`${file.name} exceeds 10MB limit`);
        }
        return;
      }
      // Avoid duplicates
      if (!state.pendingFiles.some((f) => f.name === file.name && f.size === file.size)) {
        state.pendingFiles.push(file);
      }
    });

    updateSelectedFilesPreview();
  }

  async function submitAddFile() {
    if (!state.selectedId || state.pendingFiles.length === 0) return;

    const title = elements.fileTitle?.value?.trim() || '';
    const notes = elements.fileNotes?.value?.trim() || '';

    if (elements.addFileSubmitBtn) {
      elements.addFileSubmitBtn.disabled = true;
      elements.addFileSubmitBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1"></span>Uploading...';
    }

    if (elements.fileUploadProgress) {
      elements.fileUploadProgress.style.display = 'block';
    }

    let successCount = 0;
    const total = state.pendingFiles.length;

    for (let i = 0; i < state.pendingFiles.length; i++) {
      const file = state.pendingFiles[i];
      const percent = Math.round(((i + 0.5) / total) * 100);

      if (elements.fileUploadPercent) {
        elements.fileUploadPercent.textContent = `${percent}%`;
      }
      if (elements.fileUploadProgressBar) {
        elements.fileUploadProgressBar.style.width = `${percent}%`;
      }

      try {
        await uploadFileAttachment(file, title, notes);
        successCount++;
      } catch (error) {
        console.error('Failed to upload file:', file.name, error);
        if (window.Toast) {
          window.Toast.error(`Failed to upload ${file.name}`);
        }
      }
    }

    // Complete progress
    if (elements.fileUploadPercent) {
      elements.fileUploadPercent.textContent = '100%';
    }
    if (elements.fileUploadProgressBar) {
      elements.fileUploadProgressBar.style.width = '100%';
    }

    // Close modal
    if (elements.addFileModal && window.bootstrap) {
      const modal = bootstrap.Modal.getInstance(elements.addFileModal);
      if (modal) modal.hide();
    }

    // Reset state
    state.pendingFiles = [];

    // Show success toast
    if (successCount > 0 && window.Toast) {
      window.Toast.success(`Uploaded ${successCount} file${successCount !== 1 ? 's' : ''}`);
    }

    // Reload files list
    await loadWorkspaceFiles(state.selectedId);

    // Notify other components
    if (window.EventBus) {
      EventBus.emit('workspace:files:updated', { workspaceId: state.selectedId });
    }

    // Reset button
    if (elements.addFileSubmitBtn) {
      elements.addFileSubmitBtn.disabled = false;
      elements.addFileSubmitBtn.innerHTML = `
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
          <path d="M9,16V10H5L12,3L19,10H15V16H9M5,20V18H19V20H5Z"/>
        </svg>
        Upload
      `;
    }
  }

  async function uploadFileAttachment(file, title, notes) {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();

      reader.onload = async (e) => {
        try {
          // Get base64 content
          let content = e.target.result;
          if (content.includes(',')) {
            content = content.split(',')[1];
          }

          // Determine file type
          let type = 'other';
          const mime = file.type || '';
          const ext = file.name.split('.').pop().toLowerCase();

          if (mime.startsWith('image/') || ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg'].includes(ext)) {
            type = 'image';
          } else if (mime.includes('pdf') || ext === 'pdf') {
            type = 'pdf';
          } else if (mime.includes('text') || ['txt', 'md', 'json', 'xml', 'csv', 'html'].includes(ext)) {
            type = 'doc';
          }

          const attachment = {
            title: title || file.name,
            body: notes || '',
            type: type,
            file_meta: {
              name: file.name,
              size: file.size,
              mime: mime || 'application/octet-stream',
              content: content
            }
          };

          const response = await fetch(`/api/studios/${encodeURIComponent(state.selectedId)}/attachments`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(attachment)
          });

          if (!response.ok) {
            const errorText = await response.text();
            throw new Error(errorText || 'Upload failed');
          }

          resolve(await response.json());
        } catch (error) {
          reject(error);
        }
      };

      reader.onerror = () => {
        reject(new Error('Failed to read file'));
      };

      reader.readAsDataURL(file);
    });
  }

  async function loadWorkspaceTasks(workspaceId) {
    if (!workspaceId) return;

    if (elements.tasksList) {
      elements.tasksList.innerHTML = '<div class="hub-loading">Loading tasks...</div>';
    }

    try {
      const response = await fetch(`/api/orchestration/tasks?studio_id=${encodeURIComponent(workspaceId)}`);
      if (!response.ok) throw new Error('Failed to load tasks');

      const data = await response.json();
      state.tasks = data.tasks || [];
      const computed = computeStats(state.tasks);
      state.stats = { ...computed, ...(data.stats || {}) };
      if (state.stats.scheduled === undefined) state.stats.scheduled = computed.scheduled;

      renderStats(state.stats);
      renderTasksList(state.tasks);
      renderSchedules(state.tasks);
    } catch (error) {
      console.error('Workspace hub failed to load tasks:', error);
      if (elements.tasksList) {
        elements.tasksList.innerHTML = '<div class="hub-empty">Unable to load tasks right now.</div>';
      }
      if (elements.schedulesList) {
        elements.schedulesList.innerHTML = '<div class="hub-empty">Unable to load schedules right now.</div>';
      }
    }
  }

  function selectWorkspace(workspaceId, { focus = false } = {}) {
    const workspace = state.workspaceMap.get(workspaceId);
    if (!workspace) return;

    stopWorkspaceRealtime();
    state.selectedId = workspaceId;
    sessionStorage.setItem(STORAGE_KEY, workspaceId);

    if (elements.workspaceSelect) {
      elements.workspaceSelect.value = workspaceId;
    }

    renderWorkspaceSummary(workspace);
    setState('selected');
    setSmartInputEnabled(true);
    resetSmartInputPrompt();
    setSmartInputStatus('', { busy: false });
    loadWorkspaceTasks(workspaceId);
    loadWorkspaceSessions(workspaceId);
    loadWorkspaceNotes(workspaceId);
    loadWorkspaceFiles(workspaceId);

    if (window.workspaceRealtime && typeof window.workspaceRealtime.subscribeToWorkspace === 'function') {
      workspaceRealtimeUnsub = window.workspaceRealtime.subscribeToWorkspace(workspaceId, (event) => {
        if (!event || event.workspaceId !== state.selectedId) return;
        if (shouldRefreshForRealtimeEvent(event.type)) {
          scheduleWorkspaceTasksRefresh();
        }
      });
    }

    if (focus && elements.workspaceSelect) {
      elements.workspaceSelect.blur();
    }
  }

  async function loadWorkspaceFiles(workspaceId) {
    if (!workspaceId) return;

    if (elements.filesList) {
      elements.filesList.innerHTML = '<div class="hub-loading">Loading files...</div>';
    }

    try {
      // Fetch full workspace data to get attachments
      const response = await fetch(`/api/studios/${encodeURIComponent(workspaceId)}`);
      if (!response.ok) throw new Error('Failed to load workspace');

      const workspace = await response.json();
      // Filter attachments that have file metadata (actual files, not just notes)
      state.files = (workspace.attachments || []).filter(a => a.file_meta || a.type === 'image' || a.type === 'other');
      renderFiles(state.files);
    } catch (error) {
      console.error('Workspace hub failed to load files:', error);
      if (elements.filesList) {
        elements.filesList.innerHTML = '<div class="hub-empty">Unable to load files.</div>';
      }
    }
  }

  function showLauncher() {
    stopWorkspaceRealtime();
    setState('launcher');
    if (elements.workspaceSelect) {
      elements.workspaceSelect.value = '';
    }
    state.selectedId = null;
    setSmartInputEnabled(false);
    resetSmartInputPrompt();
    clearSmartInputField();
    setSmartInputStatus('Select a workspace to use quick add.', { busy: false });
    sessionStorage.removeItem(STORAGE_KEY);
    clearWorkspaceSummary();
    renderStats({ completed: 0, in_progress: 0, failed: 0, scheduled: 0 });
    if (elements.tasksList) {
      elements.tasksList.innerHTML = '<div class=\"hub-empty\">Select a workspace to view tasks.</div>';
    }
    if (elements.tasksSubtitle) {
      elements.tasksSubtitle.textContent = 'Select a workspace to see task activity.';
    }
    if (elements.schedulesList) {
      elements.schedulesList.innerHTML = '<div class=\"hub-empty\">Select a workspace to view schedules.</div>';
    }
    if (elements.sessionsList) {
      elements.sessionsList.innerHTML = '<div class=\"hub-empty\">Select a workspace to view sessions.</div>';
    }
    if (elements.notesList) {
      elements.notesList.innerHTML = '<div class="hub-empty">Select a workspace to view notes.</div>';
    }
    if (elements.filesList) {
      elements.filesList.innerHTML = '<div class="hub-empty">Select a workspace to view files.</div>';
    }
  }

  async function loadWorkspaces() {
    setState('loading');

    try {
      const response = await fetch('/api/workspaces?tree=true');
      if (!response.ok) throw new Error('Failed to load workspaces');

      const data = await response.json();
      state.workspaces = data.folders || [];

      const flattened = flattenWorkspaces(state.workspaces);
      state.workspaceMap = new Map(flattened.map((workspace) => [workspace.id, workspace]));

      populateWorkspaceSelect(flattened);
      renderLauncher(flattened);

      const saved = sessionStorage.getItem(STORAGE_KEY);
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

  function openSchedulePanel() {
    if (!state.selectedId) return;
    if (window.sessionManager && typeof window.sessionManager.openScheduledTasksPanel === 'function') {
      window.sessionManager.openScheduledTasksPanel(state.selectedId);
    }
  }

  function openTaskModal() {
    if (!state.selectedId) return;
    if (window.taskModalController) {
      window.taskModalController.openForCreate(state.selectedId, '', () => loadWorkspaceTasks(state.selectedId));
    }
  }

  async function classifySmartInput(input) {
    const response = await fetch(SMART_INPUT_CLASSIFY_ENDPOINT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        workspace_id: state.selectedId,
        input
      })
    });

    if (!response.ok) {
      const errText = await response.text();
      throw new Error(errText || 'Failed to classify input');
    }

    return response.json();
  }

  async function logSmartInputOverride(payload) {
    try {
      await fetch(SMART_INPUT_OVERRIDE_ENDPOINT, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: payload.workspaceId,
          input: payload.input,
          predicted_decision: payload.predictedDecision,
          selected_decision: payload.selectedDecision,
          confidence: payload.confidence,
          method: payload.method
        })
      });
    } catch (error) {
      console.warn('Failed to log smart input override:', error);
    }
  }

  function extractTaskFromResponse(payload) {
    if (!payload) return null;
    if (payload.task) return payload.task;
    if (payload.id) return payload;
    return null;
  }

  async function createTaskFromSmartInput(input) {
    const fallbackCreate = async (fallbackError) => {
      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          studio_id: state.selectedId,
          description: input,
          details: '',
          priority: 3
        })
      });

      if (!response.ok) {
        const errText = await response.text();
        throw new Error(errText || 'Failed to create task');
      }

      await loadWorkspaceTasks(state.selectedId);
      return { kind: 'task', fallback: true, error: fallbackError };
    };

    const parseResponse = await fetch('/api/orchestration/tasks/auto-parse', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        description: input,
        workspace_id: state.selectedId
      })
    });

    if (!parseResponse.ok) {
      const errText = await parseResponse.text();
      return fallbackCreate(errText || 'Auto-parse unavailable');
    }

    const parsed = await parseResponse.json();

    let scheduleData = { schedule_enabled: false };
    if (parsed.schedule_enabled && parsed.schedule) {
      const schedule = { ...parsed.schedule };
      if (schedule.once_at && !schedule.run_at) {
        schedule.run_at = schedule.once_at;
        delete schedule.once_at;
      }
      scheduleData = {
        schedule: schedule,
        schedule_enabled: true,
        schedule_name: parsed.schedule_name || ''
      };
    }

    let resultStorageData = {};
    if (parsed.result_storage && parsed.result_storage.enabled) {
      resultStorageData = {
        result_storage: {
          enabled: true,
          format: parsed.result_storage.format || 'text',
          store_node_id: parsed.result_storage.store_node_id || undefined,
          file_path: parsed.result_storage.file_path || undefined
        }
      };
    }

    const workflowSteps = Array.isArray(parsed.tasks) ? parsed.tasks.filter(Boolean) : [];
    if (workflowSteps.length > 0) {
      const parentTitle = parsed.title || workflowSteps[0]?.title || 'New Workflow';
      const parentDetails = parsed.details || '';
      const parentPriority = parsed.priority || 3;

      const parentResponse = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          studio_id: state.selectedId,
          description: parentTitle,
          details: parentDetails,
          priority: parentPriority
        })
      });

      if (!parentResponse.ok) {
        const errText = await parentResponse.text();
        return fallbackCreate(errText || 'Failed to create parent task');
      }

      const parentPayload = await parentResponse.json();
      const parentTask = extractTaskFromResponse(parentPayload);
      const parentTaskId = parentTask?.id;

      if (!parentTaskId) {
        throw new Error('Parent task created but ID is missing');
      }

      const stepIdToTaskId = new Map();

      for (let i = 0; i < workflowSteps.length; i++) {
        const step = workflowSteps[i] || {};
        const stepId = step.id || `step-${i + 1}`;
        const stepTitle = step.title || step.description || parsed.title || `Task ${i + 1}`;
        const stepDetails = step.details || '';
        const stepPriority = Number.isInteger(step.priority) ? step.priority : (parsed.priority || 3);

        let to = '';
        let assignedNodeId = '';
        if (step.agent_name) {
          assignedNodeId = `${step.agent_name}-node-1`;
          to = step.agent_name;
        }

        let dependsOn = Array.isArray(step.depends_on) ? step.depends_on : [];
        if (dependsOn.length === 0 && i > 0) {
          const fallbackId = workflowSteps[i - 1]?.id || `step-${i}`;
          dependsOn = [fallbackId];
        }
        const inputTaskIds = dependsOn.map((id) => stepIdToTaskId.get(id)).filter(Boolean);

        const stepScheduleData = i === 0 ? scheduleData : { schedule_enabled: false };
        const stepResultStorageData = i === workflowSteps.length - 1 ? resultStorageData : {};

        const createResponse = await fetch('/api/orchestration/tasks', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            studio_id: state.selectedId,
            description: stepTitle,
            details: stepDetails,
            priority: stepPriority,
            to: to || undefined,
            assigned_node_id: assignedNodeId || undefined,
            input_task_ids: inputTaskIds,
            parent_task_id: parentTaskId,
            subtask_index: i + 1,
            ...stepScheduleData,
            ...stepResultStorageData
          })
        });

        if (!createResponse.ok) {
          const errText = await createResponse.text();
          throw new Error(errText || 'Failed to create task');
        }

        const createdPayload = await createResponse.json();
        const createdTask = extractTaskFromResponse(createdPayload);
        if (createdTask?.id) {
          stepIdToTaskId.set(stepId, createdTask.id);
        }
      }

      await loadWorkspaceTasks(state.selectedId);
      return { kind: 'workflow', fallback: false };
    }

    let to = '';
    let assignedNodeId = '';
    if (parsed.agent_name) {
      assignedNodeId = `${parsed.agent_name}-node-1`;
      to = parsed.agent_name;
    }

    const response = await fetch('/api/orchestration/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        studio_id: state.selectedId,
        description: parsed.title || input,
        details: parsed.details || '',
        priority: parsed.priority || 3,
        to: to || undefined,
        assigned_node_id: assignedNodeId || undefined,
        ...scheduleData,
        ...resultStorageData
      })
    });

    if (!response.ok) {
      const errText = await response.text();
      throw new Error(errText || 'Failed to create task');
    }

    await loadWorkspaceTasks(state.selectedId);
    return { kind: 'task', fallback: false };
  }

  async function createChatFromSmartInput(input) {
    if (!window.sessionManager || typeof window.sessionManager.fetchAgents !== 'function') {
      throw new Error('Chat manager not available');
    }

    const agents = await window.sessionManager.fetchAgents();
    if (!agents || agents.length === 0) {
      throw new Error('No agents available');
    }

    const agentName = agents[0].name;
    if (typeof window.sessionManager.createSessionWithAgentInFolder === 'function') {
      await window.sessionManager.createSessionWithAgentInFolder(agentName, state.selectedId);
    } else if (typeof window.sessionManager.createSessionWithAgent === 'function') {
      await window.sessionManager.createSessionWithAgent(agentName);
    }

    if (window.sendMessageToChat) {
      setTimeout(() => window.sendMessageToChat(input), 100);
    }

    await loadWorkspaceSessions(state.selectedId);
  }

  async function handleSmartInputDecision(decision, classification = null) {
    // Check if cancelled
    if (state.smartInputCancelled) {
      return;
    }

    const meta = classification || state.smartInput || {};
    const input = meta.input || (elements.smartInputField ? elements.smartInputField.value.trim() : '');
    if (!input) return;

    const predictedDecision = meta.decision || meta.predictedDecision || decision;
    const confidence = meta.confidence || 0;
    const method = meta.method || 'fallback';

    hideSmartInputPrompt();
    setSmartInputBusy(true, decision === 'task' ? 'Creating task...' : 'Starting chat...');
    showSmartInputProgress('execute', {
      headline: decision === 'task' ? 'Creating task' : 'Starting chat',
      message: decision === 'task' ? 'Building tasks in your workspace.' : 'Opening a new session.'
    });

    try {
      if (decision === 'task') {
        const createResult = await createTaskFromSmartInput(input);
        if (createResult?.fallback && window.Toast) {
          window.Toast.warning('Auto-parse unavailable. Created a basic task instead.');
        }
        const createdLabel = createResult?.kind === 'workflow' ? 'Workflow created.' : 'Task created.';
        setSmartInputBusy(false, createdLabel);
      } else {
        await createChatFromSmartInput(input);
        setSmartInputBusy(false, 'Chat started.');
      }

      clearSmartInputField();
    } catch (error) {
      console.error('Smart input routing failed:', error);
      setSmartInputBusy(false, 'Something went wrong. Try again.');
      if (window.Toast) {
        window.Toast.error(decision === 'task' ? 'Failed to create task' : 'Failed to start chat');
      }
    } finally {
      hideSmartInputProgress();
    }

    if (predictedDecision && predictedDecision !== decision) {
      void logSmartInputOverride({
        workspaceId: state.selectedId,
        input,
        predictedDecision,
        selectedDecision: decision,
        confidence,
        method
      });
    }

    state.smartInput = null;
  }

  async function submitSmartInput() {
    if (!elements.smartInputField) return;

    const input = elements.smartInputField.value.trim();
    if (!input) {
      setSmartInputStatus('Type something to get started.', { busy: false });
      return;
    }

    if (!state.selectedId) {
      setSmartInputStatus('Select a workspace first.', { busy: false });
      return;
    }

    // Handle slash commands
    const lowerInput = input.toLowerCase();

    // /note command - create a quick note
    if (lowerInput.startsWith('/note ') || lowerInput === '/note') {
      const noteContent = input.slice(6).trim();
      if (!noteContent) {
        setSmartInputStatus('Usage: /note <your note content>', { busy: false });
        return;
      }
      await createQuickNote(noteContent);
      return;
    }

    // /task command - create a task directly
    if (lowerInput.startsWith('/task ') || lowerInput === '/task') {
      const taskContent = input.slice(6).trim();
      if (!taskContent) {
        setSmartInputStatus('Usage: /task <task description>', { busy: false });
        return;
      }
      await createQuickTask(taskContent);
      return;
    }

    // /chat command - start a new chat
    if (lowerInput.startsWith('/chat ') || lowerInput === '/chat') {
      const chatMessage = input.slice(6).trim();
      await createQuickChat(chatMessage);
      return;
    }

    // @file command - open file attachment modal
    if (lowerInput === '@file' || lowerInput.startsWith('@file ')) {
      openFileAttachmentModal();
      return;
    }

    // Reset cancelled flag for new operation
    state.smartInputCancelled = false;

    resetSmartInputPrompt();
    setSmartInputBusy(true, 'Deciding...');
    showSmartInputProgress('analyze', {
      headline: 'Analyzing input',
      message: 'Reviewing your request.'
    });

    let classification;
    try {
      classification = await classifySmartInput(input);
    } catch (error) {
      console.error('Smart input classification failed:', error);
      setSmartInputBusy(false);
      hideSmartInputProgress();
      showSmartInputPrompt({
        input,
        decision: 'task',
        confidence: 0,
        method: 'fallback',
        message: 'Could not auto-classify. Choose where to route this.'
      });
      return;
    }

    updateSmartInputProgress('decide', {
      headline: 'Routing',
      message: 'Choosing the best path.'
    });
    setSmartInputBusy(false);

    const decision = classification.decision || 'task';
    const payload = {
      input,
      decision,
      confidence: classification.confidence || 0,
      method: classification.method || 'fallback',
      message: classification.message
    };

    if (classification.needs_confirmation) {
      hideSmartInputProgress();
      showSmartInputPrompt(payload);
      return;
    }

    await handleSmartInputDecision(decision, payload);
  }

  function bindEvents() {
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
          loadWorkspaceTasks(state.selectedId);
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
      elements.newNoteBtn.addEventListener('click', createNewNote);
    }

    if (elements.addFileBtn) {
      elements.addFileBtn.addEventListener('click', () => {
        if (state.selectedId) {
          openAddFileModal();
        }
      });
    }

    if (elements.smartInputSubmit) {
      elements.smartInputSubmit.addEventListener('click', submitSmartInput);
    }

    if (elements.smartInputAttachBtn) {
      elements.smartInputAttachBtn.addEventListener('click', () => {
        if (state.selectedId) {
          openAddFileModal();
        }
      });
    }

    if (elements.smartInputField) {
      elements.smartInputField.addEventListener('keydown', (event) => {
        if (event.key === 'Enter') {
          event.preventDefault();
          submitSmartInput();
        }
      });

      elements.smartInputField.addEventListener('input', () => {
        if (elements.smartInputPrompt && !elements.smartInputPrompt.hidden) {
          resetSmartInputPrompt();
        }
        setSmartInputStatus('', { busy: false });
      });
    }

    if (elements.smartInputPromptTask) {
      elements.smartInputPromptTask.addEventListener('click', () => handleSmartInputDecision('task'));
    }

    if (elements.smartInputPromptChat) {
      elements.smartInputPromptChat.addEventListener('click', () => handleSmartInputDecision('chat'));
    }

    if (elements.smartInputPromptCancel) {
      elements.smartInputPromptCancel.addEventListener('click', () => {
        resetSmartInputPrompt();
        setSmartInputStatus('', { busy: false });
      });
    }

    // Cancel button in progress modal
    if (elements.smartInputCancelBtn) {
      elements.smartInputCancelBtn.addEventListener('click', () => {
        cancelSmartInput();
      });
    }

    // ==========================================================================
    // Selection Mode and Delete Buttons
    // ==========================================================================

    // Tasks selection
    if (elements.selectTasksBtn) {
      elements.selectTasksBtn.addEventListener('click', () => toggleSelectionMode('tasks'));
    }
    if (elements.bulkDeleteTasksBtn) {
      elements.bulkDeleteTasksBtn.addEventListener('click', bulkDeleteTasks);
    }

    // Sessions selection
    if (elements.selectSessionsBtn) {
      elements.selectSessionsBtn.addEventListener('click', () => toggleSelectionMode('sessions'));
    }
    if (elements.bulkDeleteSessionsBtn) {
      elements.bulkDeleteSessionsBtn.addEventListener('click', bulkDeleteSessions);
    }

    // Notes selection
    if (elements.selectNotesBtn) {
      elements.selectNotesBtn.addEventListener('click', () => toggleSelectionMode('notes'));
    }
    if (elements.bulkDeleteNotesBtn) {
      elements.bulkDeleteNotesBtn.addEventListener('click', bulkDeleteNotes);
    }

    // Files selection
    if (elements.selectFilesBtn) {
      elements.selectFilesBtn.addEventListener('click', () => toggleSelectionMode('files'));
    }
    if (elements.bulkTrashFilesBtn) {
      elements.bulkTrashFilesBtn.addEventListener('click', bulkMoveFilesToTrash);
    }

    // Delete confirmation modal
    if (elements.deleteConfirmBtn) {
      elements.deleteConfirmBtn.addEventListener('click', handleDeleteConfirm);
    }
    if (elements.deleteConfirmModal) {
      elements.deleteConfirmModal.addEventListener('hidden.bs.modal', handleDeleteCancel);
    }

    if (elements.parentDeleteUngroupBtn) {
      elements.parentDeleteUngroupBtn.addEventListener('click', () => handleParentDeleteChoice('ungroup'));
    }
    if (elements.parentDeleteAllBtn) {
      elements.parentDeleteAllBtn.addEventListener('click', () => handleParentDeleteChoice('delete_all'));
    }
    if (elements.parentDeleteModal) {
      elements.parentDeleteModal.addEventListener('hidden.bs.modal', handleParentDeleteCancel);
    }

    // File upload modal
    if (elements.fileDropZone) {
      elements.fileDropZone.addEventListener('click', () => elements.fileInput?.click());
      elements.fileDropZone.addEventListener('dragover', handleFileDropZoneDragOver);
      elements.fileDropZone.addEventListener('dragleave', handleFileDropZoneDragLeave);
      elements.fileDropZone.addEventListener('drop', handleFileDropZoneDrop);
    }
    if (elements.fileInput) {
      elements.fileInput.addEventListener('change', handleFileInputChange);
    }
    if (elements.addFileSubmitBtn) {
      elements.addFileSubmitBtn.addEventListener('click', submitAddFile);
    }
  }

  if (window.EventBus) {
    EventBus.on('workspace:files:updated', (data) => {
      if (!data?.workspaceId || data.workspaceId !== state.selectedId) return;
      loadWorkspaceFiles(state.selectedId);
    }, 'workspaceHub');
  }

  bindEvents();
  loadWorkspaces();
})();

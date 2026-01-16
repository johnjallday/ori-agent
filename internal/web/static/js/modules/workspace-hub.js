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
    deleteConfirmBtn: document.getElementById('hubDeleteConfirmBtn')
  };

  const state = {
    workspaces: [],
    workspaceMap: new Map(),
    selectedId: null,
    tasks: [],
    stats: null,
    sessions: [],
    notes: [],
    files: [],
    smartInput: null,
    // Selection mode state
    selectionMode: { tasks: false, sessions: false, notes: false, files: false },
    selectedItems: { tasks: new Set(), sessions: new Set(), notes: new Set(), files: new Set() }
  };

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
    const confirmed = await showDeleteConfirm({
      title: 'Delete Task',
      message: 'Are you sure you want to delete this task? This action cannot be undone.'
    });
    if (!confirmed) return;

    try {
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

  function renderTasksList(tasks) {
    if (!elements.tasksList) return;

    if (!tasks || tasks.length === 0) {
      elements.tasksList.innerHTML = '<div class="hub-empty">No tasks yet. Create the first one to get started.</div>';
      if (elements.tasksSubtitle) {
        elements.tasksSubtitle.textContent = 'No tasks created yet.';
      }
      return;
    }

    if (elements.tasksSubtitle) {
      elements.tasksSubtitle.textContent = `${tasks.length} task${tasks.length === 1 ? '' : 's'} queued for this workspace.`;
    }

    const inSelectionMode = state.selectionMode.tasks;
    const selectedSet = state.selectedItems.tasks;

    const items = tasks.map((task) => {
      const status = task.status || 'pending';
      const scheduleLabel = task.schedule_enabled ? `Next run: ${formatDate(task.next_run)}` : 'Not scheduled';
      const assignment = task.to || 'unassigned';
      const isSelected = selectedSet.has(task.id);

      return `
        <div class="hub-task-card${isSelected ? ' selected' : ''}" data-task-id="${escapeHtml(task.id)}">
          <div class="hub-item-checkbox">
            <input type="checkbox" ${isSelected ? 'checked' : ''} aria-label="Select task">
          </div>
          <div class="hub-task-content">
            <div class="hub-task-header">
              <div class="hub-task-title">${escapeHtml(task.name || task.description || task.id)}</div>
              <span class="hub-task-status status-${escapeHtml(status)}">${escapeHtml(status.replace('_', ' '))}</span>
            </div>
            <div class="hub-task-meta">
              <span>${escapeHtml(assignment)}</span>
              <span>${escapeHtml(scheduleLabel)}</span>
            </div>
            <div class="hub-task-actions">
              <button class="modern-btn modern-btn-secondary" data-action="edit">Edit</button>
              <button class="modern-btn modern-btn-secondary" data-action="chat">Open Chat</button>
            </div>
          </div>
          <button class="hub-item-delete-btn" data-action="delete" title="Delete task">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
            </svg>
          </button>
        </div>
      `;
    });

    elements.tasksList.innerHTML = items.join('');

    elements.tasksList.querySelectorAll('.hub-task-card').forEach((card) => {
      const taskId = card.dataset.taskId;
      const task = tasks.find((item) => item.id === taskId);

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

      card.querySelectorAll('[data-action="edit"]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.stopPropagation();
          if (window.taskModalController && task) {
            window.taskModalController.openForEdit(task, () => loadWorkspaceTasks(state.selectedId));
          }
        });
      });

      card.querySelectorAll('[data-action="chat"]').forEach((btn) => {
        btn.addEventListener('click', (event) => {
          event.stopPropagation();
          openChatForWorkspace(state.selectedId);
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

  function openChatForWorkspace(workspaceId) {
    if (window.chatPanel && typeof window.chatPanel.open === 'function') {
      window.chatPanel.open();
    }

    if (workspaceId && window.sessionManager && typeof window.sessionManager.getActiveSessionId === 'function') {
      const activeSession = window.sessionManager.getActiveSessionId();
      if (!activeSession && typeof window.sessionManager.showCreateChatModalForWorkspace === 'function') {
        window.sessionManager.showCreateChatModalForWorkspace(workspaceId);
      }
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

  async function createTaskFromSmartInput(input) {
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
    const meta = classification || state.smartInput || {};
    const input = meta.input || (elements.smartInputField ? elements.smartInputField.value.trim() : '');
    if (!input) return;

    const predictedDecision = meta.decision || meta.predictedDecision || decision;
    const confidence = meta.confidence || 0;
    const method = meta.method || 'fallback';

    hideSmartInputPrompt();
    setSmartInputBusy(true, decision === 'task' ? 'Creating task...' : 'Starting chat...');

    try {
      if (decision === 'task') {
        await createTaskFromSmartInput(input);
        setSmartInputBusy(false, 'Task created.');
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

    resetSmartInputPrompt();
    setSmartInputBusy(true, 'Deciding...');

    let classification;
    try {
      classification = await classifySmartInput(input);
    } catch (error) {
      console.error('Smart input classification failed:', error);
      setSmartInputBusy(false);
      showSmartInputPrompt({
        input,
        decision: 'task',
        confidence: 0,
        method: 'fallback',
        message: 'Could not auto-classify. Choose where to route this.'
      });
      return;
    }

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
        // Open canvas to add files
        if (state.selectedId) {
          window.location.href = `/workspaces/${encodeURIComponent(state.selectedId)}/canvas`;
        }
      });
    }

    if (elements.smartInputSubmit) {
      elements.smartInputSubmit.addEventListener('click', submitSmartInput);
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

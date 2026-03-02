/**
 * Workspace Detail Page Module
 * Handles the dedicated workspace detail page with panels for tasks, sessions, files, and notes.
 *
 * @module workspace-detail
 */

/**
 * Format a date for display
 * @param {string} dateString - ISO date string
 * @returns {string} Formatted date
 */
function formatDate(dateString) {
  if (!dateString) return '';
  const date = new Date(dateString);
  const now = new Date();
  const diffMs = now - date;
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMs / 3600000);
  const diffDays = Math.floor(diffMs / 86400000);

  if (diffMins < 1) return 'Just now';
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;
  if (diffDays < 7) return `${diffDays}d ago`;
  return date.toLocaleDateString();
}

/**
 * Get status badge class
 * @param {string} status - Task status
 * @returns {string} CSS class
 */
function getStatusClass(status) {
  const statusMap = {
    pending: 'pending',
    in_progress: 'in_progress',
    completed: 'completed',
    failed: 'failed'
  };
  return statusMap[status] || 'pending';
}

/**
 * Get display status text
 * @param {string} status - Task status
 * @returns {string} Display text
 */
function getDisplayStatus(status) {
  const statusMap = {
    pending: 'Pending',
    in_progress: 'In Progress',
    completed: 'Completed',
    failed: 'Failed'
  };
  return statusMap[status] || status;
}

export class WorkspaceDetailPage {
  constructor(workspaceId) {
    this.workspaceId = workspaceId;
    this.workspace = null;
    this.tasks = [];
    this.sessions = [];
    this.files = [];
    this.notes = [];
    this.directories = [];
    this.schedules = [];
    this.children = [];

    // Board state
    this.currentView = 'list'; // 'list' or 'board'
    this.boardConfig = null;
    this.agentOptions = null;
    this.agentCatalog = [];
    this.agentIndex = new Map();
    this.boardDidDrag = false;
    this.currentTaskResultText = '';
    this.currentBlockedTask = null;
    this.currentAssistRecommendation = null;

    // DOM elements
    this.elements = {};
  }

  /**
   * Initialize the workspace detail page
   */
  async init() {
    this.cacheElements();
    this.bindEvents();
    this.setupFileModal();
    await this.loadWorkspace();
    await this.loadAgentCatalog();
    await Promise.all([
      this.loadTasks(),
      this.loadSessions(),
      this.loadFiles(),
      this.loadNotes(),
      this.loadDirectories(),
      this.loadSchedules()
    ]);
    this.setupRealtime();
  }

  /**
   * Setup file modal handlers
   */
  setupFileModal() {
    const dropZone = document.getElementById('hubFileDropZone');
    const fileInput = document.getElementById('hubFileInput');
    const submitBtn = document.getElementById('hubAddFileSubmitBtn');
    const selectedFilesPreview = document.getElementById('hubSelectedFilesPreview');
    const selectedFilesList = document.getElementById('hubSelectedFilesList');

    if (!dropZone || !fileInput) return;

    let selectedFiles = [];

    // Click to select files
    dropZone.addEventListener('click', () => fileInput.click());

    // Drag and drop handlers
    dropZone.addEventListener('dragover', (e) => {
      e.preventDefault();
      dropZone.classList.add('drag-active');
    });

    dropZone.addEventListener('dragleave', () => {
      dropZone.classList.remove('drag-active');
    });

    dropZone.addEventListener('drop', (e) => {
      e.preventDefault();
      dropZone.classList.remove('drag-active');
      if (e.dataTransfer.files.length > 0) {
        selectedFiles = Array.from(e.dataTransfer.files);
        updateFilesPreview();
      }
    });

    // File input change
    fileInput.addEventListener('change', () => {
      if (fileInput.files.length > 0) {
        selectedFiles = Array.from(fileInput.files);
        updateFilesPreview();
      }
    });

    // Update files preview
    const updateFilesPreview = () => {
      if (selectedFiles.length === 0) {
        selectedFilesPreview.style.display = 'none';
        submitBtn.disabled = true;
        return;
      }

      selectedFilesPreview.style.display = 'block';
      submitBtn.disabled = false;

      selectedFilesList.innerHTML = selectedFiles.map((file, i) => `
        <div class="d-flex justify-content-between align-items-center p-2" style="background: var(--bg-secondary); border-radius: 4px;">
          <span style="color: var(--text-primary);">${this.escapeHtml(file.name)}</span>
          <button type="button" class="btn-close btn-sm" onclick="window.workspaceDetail?.removeFile(${i})"></button>
        </div>
      `).join('');
    };

    // Remove file from selection
    this.removeFile = (index) => {
      selectedFiles.splice(index, 1);
      updateFilesPreview();
    };

    // Submit upload
    submitBtn?.addEventListener('click', async () => {
      if (selectedFiles.length === 0) return;

      const title = document.getElementById('hubFileTitle')?.value || '';
      const notes = document.getElementById('hubFileNotes')?.value || '';

      submitBtn.disabled = true;
      submitBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1"></span> Uploading...';

      try {
        for (const file of selectedFiles) {
          const formData = new FormData();
          formData.append('file', file);
          formData.append('workspace_id', this.workspaceId);
          if (title) formData.append('title', title);
          if (notes) formData.append('notes', notes);

          const response = await fetch(`/api/studios/${encodeURIComponent(this.workspaceId)}/files`, {
            method: 'POST',
            body: formData
          });

          if (!response.ok) throw new Error(`Failed to upload ${file.name}`);
        }

        if (window.Toast) window.Toast.success('File(s) uploaded successfully');
        await this.loadFiles();

        // Close modal and reset
        const modal = bootstrap.Modal.getInstance(document.getElementById('hubAddFileModal'));
        modal?.hide();
        selectedFiles = [];
        updateFilesPreview();
        document.getElementById('hubFileTitle').value = '';
        document.getElementById('hubFileNotes').value = '';
        fileInput.value = '';
      } catch (error) {
        console.error('Upload failed:', error);
        if (window.Toast) window.Toast.error(error.message || 'Upload failed');
      } finally {
        submitBtn.disabled = false;
        submitBtn.innerHTML = '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1"><path d="M9,16V10H5L12,3L19,10H15V16H9M5,20V18H19V20H5Z"/></svg> Upload';
      }
    });

    // Reset on modal close
    document.getElementById('hubAddFileModal')?.addEventListener('hidden.bs.modal', () => {
      selectedFiles = [];
      updateFilesPreview();
      document.getElementById('hubFileTitle').value = '';
      document.getElementById('hubFileNotes').value = '';
      fileInput.value = '';
    });
  }

  /**
   * Cache DOM elements
   */
  cacheElements() {
    this.elements = {
      // Header elements
      workspaceName: document.getElementById('workspace-name'),
      workspaceDescription: document.getElementById('workspace-description'),
      workspaceBreadcrumb: document.getElementById('workspace-breadcrumb-name'),
      openCanvasBtn: document.getElementById('open-canvas-btn'),

      // Stats
      agentCount: document.getElementById('workspace-agent-count'),
      taskCount: document.getElementById('workspace-task-count'),
      sessionCount: document.getElementById('workspace-session-count'),

      // Quick input
      smartInput: document.getElementById('workspace-detail-input'),
      smartSubmit: document.getElementById('workspace-detail-submit'),

      // Lists
      tasksList: document.getElementById('workspace-detail-tasks-list'),
      sessionsList: document.getElementById('workspace-detail-sessions-list'),
      filesList: document.getElementById('workspace-detail-files-list'),
      notesList: document.getElementById('workspace-detail-notes-list'),
      directoriesList: document.getElementById('workspace-detail-directories-list'),
      schedulesList: document.getElementById('workspace-detail-schedules-list'),
      childrenList: document.getElementById('workspace-detail-children-list'),

      // Panels
      childrenPanel: document.getElementById('workspace-detail-children-panel'),
      childrenCount: document.getElementById('workspace-detail-children-count'),

      // Buttons
      addTaskBtn: document.getElementById('workspace-detail-add-task'),
      refreshTasksBtn: document.getElementById('workspace-detail-refresh-tasks'),
      newSessionBtn: document.getElementById('workspace-detail-new-session'),
      addFileBtn: document.getElementById('workspace-detail-add-file'),
      addNoteBtn: document.getElementById('workspace-detail-add-note'),
      copyNotesBtn: document.getElementById('workspace-detail-copy-notes'),
      addDirectoryBtn: document.getElementById('workspace-detail-add-directory'),
      viewSchedulesBtn: document.getElementById('workspace-detail-view-schedules'),

      // View toggle
      viewListBtn: document.getElementById('workspace-detail-view-list'),
      viewBoardBtn: document.getElementById('workspace-detail-view-board'),

      // Board elements
      tasksPanel: document.getElementById('workspace-detail-tasks-panel'),
      tasksBoard: document.getElementById('workspace-detail-tasks-board'),
      boardColumns: document.getElementById('workspace-detail-board-columns'),
      boardEmpty: document.getElementById('workspace-detail-board-empty'),
      boardSetupBtn: document.getElementById('workspace-detail-board-setup'),

      // Result modal
      taskResultModal: document.getElementById('workspace-detail-task-result-modal'),
      taskResultTitle: document.getElementById('workspace-detail-task-result-title'),
      taskResultMeta: document.getElementById('workspace-detail-task-result-meta'),
      taskResultBody: document.getElementById('workspace-detail-task-result-body'),
      taskResultCopyBtn: document.getElementById('workspace-detail-task-result-copy'),

      // Assist modal
      taskAssistModal: document.getElementById('workspace-detail-task-assist-modal'),
      taskAssistMeta: document.getElementById('workspace-detail-task-assist-meta'),
      taskAssistReason: document.getElementById('workspace-detail-task-assist-reason'),
      taskAssistQuestion: document.getElementById('workspace-detail-task-assist-question'),
      taskAssistRecommendationWrap: document.getElementById('workspace-detail-task-assist-recommendation-wrap'),
      taskAssistRecommendation: document.getElementById('workspace-detail-task-assist-recommendation'),
      taskAssistAgent: document.getElementById('workspace-detail-task-assist-agent'),
      taskAssistMessage: document.getElementById('workspace-detail-task-assist-message'),
      taskAssistResponseWrap: document.getElementById('workspace-detail-task-assist-response-wrap'),
      taskAssistResponse: document.getElementById('workspace-detail-task-assist-response'),
      taskAssistRetryBtn: document.getElementById('workspace-detail-task-assist-retry'),
      taskAssistContinueBtn: document.getElementById('workspace-detail-task-assist-continue'),
      taskAssistSwitchBtn: document.getElementById('workspace-detail-task-assist-switch'),
      taskAssistFailBtn: document.getElementById('workspace-detail-task-assist-fail')
    };
  }

  /**
   * Bind event handlers
   */
  bindEvents() {
    // Quick input
    this.elements.smartSubmit?.addEventListener('click', () => this.handleQuickInput());
    this.elements.smartInput?.addEventListener('keypress', (e) => {
      if (e.key === 'Enter') this.handleQuickInput();
    });

    // Task buttons
    this.elements.addTaskBtn?.addEventListener('click', () => this.showAddTaskModal());
    this.elements.refreshTasksBtn?.addEventListener('click', () => this.loadTasks());

    // View toggle
    this.elements.viewListBtn?.addEventListener('click', () => this.setView('list'));
    this.elements.viewBoardBtn?.addEventListener('click', () => this.setView('board'));

    // Board setup
    this.elements.boardSetupBtn?.addEventListener('click', () => this.setupBoard());

    // Session buttons
    this.elements.newSessionBtn?.addEventListener('click', () => this.createNewSession());

    // File buttons
    this.elements.addFileBtn?.addEventListener('click', () => this.showFileModal());

    // Note buttons
    this.elements.addNoteBtn?.addEventListener('click', () => this.showNoteModal());
    this.elements.copyNotesBtn?.addEventListener('click', () => this.copyAllNotesToClipboard());

    // Directory buttons
    this.elements.addDirectoryBtn?.addEventListener('click', () => this.showAddDirectoryModal());

    // Schedule buttons
    this.elements.viewSchedulesBtn?.addEventListener('click', () => this.showSchedulesModal());
    this.elements.taskResultCopyBtn?.addEventListener('click', () => this.copyCurrentTaskResult());
    this.elements.taskAssistRetryBtn?.addEventListener('click', () => this.submitTaskAssist('retry'));
    this.elements.taskAssistContinueBtn?.addEventListener('click', () => this.submitTaskAssist('continue_with_instruction'));
    this.elements.taskAssistSwitchBtn?.addEventListener('click', () => this.submitTaskAssist('switch_agent_retry'));
    this.elements.taskAssistFailBtn?.addEventListener('click', () => this.submitTaskAssist('mark_failed'));
    this.elements.taskAssistAgent?.addEventListener('change', () => this.updateAssistSwitchButtonState());

    // Make workspace name and description editable
    this.makeEditable(this.elements.workspaceName, 'name', false);
    this.makeEditable(this.elements.workspaceDescription, 'description', true);

    // Subscribe to EventBus events for auto-refresh
    console.log('[workspace-detail] EventBus available:', !!window.EventBus);
    if (window.EventBus) {
      console.log('[workspace-detail] Registering EventBus listeners');
      EventBus.on('task:created', (data) => {
        if (!data?.workspaceId || data.workspaceId === this.workspaceId) {
          this.loadTasks();
        }
      }, 'workspaceDetail');

      EventBus.on('task:updated', (data) => {
        if (!data?.workspaceId || data.workspaceId === this.workspaceId) {
          this.loadTasks();
        }
      }, 'workspaceDetail');

      EventBus.on('session:created', (data) => {
        if (!data?.folderId || data.folderId === this.workspaceId) {
          this.loadSessions();
        }
      }, 'workspaceDetail');

      EventBus.on('note:created', (data) => {
        if (!data?.workspaceId || data.workspaceId === this.workspaceId) {
          this.loadNotes();
        }
      }, 'workspaceDetail');

      EventBus.on('note:updated', (data) => {
        console.log('[workspace-detail] note:updated received', data?.workspaceId, 'this.workspaceId:', this.workspaceId);
        if (!data?.workspaceId || data.workspaceId === this.workspaceId) {
          console.log('[workspace-detail] calling loadNotes');
          this.loadNotes();
        }
      }, 'workspaceDetail');

      EventBus.on('workspace:files:updated', (data) => {
        if (!data?.workspaceId || data.workspaceId === this.workspaceId) {
          this.loadFiles();
        }
      }, 'workspaceDetail');
    }
  }

  /**
   * Update workspace via API
   * @param {Object} updates - Fields to update (name, description, etc.)
   */
  async updateWorkspace(updates) {
    const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(updates)
    });
    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to update workspace');
    }
    return response.json();
  }

  /**
   * Create inline editable element
   * @param {HTMLElement} element - The element to make editable
   * @param {string} field - Field name ('name' or 'description')
   * @param {boolean} isMultiline - Whether to use textarea
   */
  makeEditable(element, field, isMultiline = false) {
    if (!element) return;

    element.classList.add('is-editable');

    // Find the edit button in the container
    const container = element.closest('.workspace-editable-field');
    const editBtn = container?.querySelector('.workspace-edit-btn');

    const startEdit = () => {
      const currentValue = element.textContent || '';

      // Create input/textarea
      const input = document.createElement(isMultiline ? 'textarea' : 'input');
      input.className = 'workspace-detail-inline-edit';
      input.value = currentValue === 'No description' ? '' : currentValue;
      if (!isMultiline) {
        input.type = 'text';
      } else {
        input.rows = 2;
      }

      // Store original display
      const originalDisplay = element.style.display;

      // Hide text and edit button, show input
      element.style.display = 'none';
      if (editBtn) editBtn.style.display = 'none';
      element.parentNode.insertBefore(input, element.nextSibling);
      input.focus();
      input.select();

      const finishEdit = async (save) => {
        const newValue = input.value.trim();
        input.remove();
        element.style.display = originalDisplay || '';
        if (editBtn) editBtn.style.display = '';

        if (!save || newValue === currentValue) return;

        // For name, don't allow empty
        if (field === 'name' && !newValue) {
          if (window.Toast) window.Toast.error('Name cannot be empty');
          return;
        }

        try {
          await this.updateWorkspace({ [field]: newValue });
          element.textContent = newValue || (field === 'description' ? 'No description' : 'Workspace');

          // Update local workspace object
          if (this.workspace) {
            this.workspace[field] = newValue;
          }

          // Update breadcrumb if name changed
          if (field === 'name' && this.elements.workspaceBreadcrumb) {
            this.elements.workspaceBreadcrumb.textContent = newValue || 'Workspace';
          }

          if (window.Toast) window.Toast.success(`${field === 'name' ? 'Name' : 'Description'} updated`);
        } catch (err) {
          console.error(`Failed to update ${field}:`, err);
          if (window.Toast) window.Toast.error(`Failed to update ${field}`);
        }
      };

      input.addEventListener('blur', () => finishEdit(true));
      input.addEventListener('keydown', (evt) => {
        if (evt.key === 'Enter' && !isMultiline) {
          evt.preventDefault();
          input.blur();
        } else if (evt.key === 'Enter' && isMultiline && evt.ctrlKey) {
          evt.preventDefault();
          input.blur();
        } else if (evt.key === 'Escape') {
          evt.preventDefault();
          finishEdit(false);
        }
      });
    };

    // Add click handler to edit button
    if (editBtn) {
      editBtn.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();
        startEdit();
      });
    }
  }

  /**
   * Load workspace data
   */
  async loadWorkspace() {
    try {
      const response = await fetch(`/api/orchestration/workspace?id=${encodeURIComponent(this.workspaceId)}`);
      if (!response.ok) throw new Error('Failed to load workspace');

      this.workspace = await response.json();
      await this.renderWorkspaceInfo();
    } catch (error) {
      console.error('Failed to load workspace:', error);
      if (window.Toast) window.Toast.error('Failed to load workspace');
    }
  }

  /**
   * Render workspace information in header
   */
  async renderWorkspaceInfo() {
    if (!this.workspace) return;

    if (this.elements.workspaceName) {
      this.elements.workspaceName.textContent = this.workspace.name || 'Unnamed Workspace';
    }
    if (this.elements.workspaceDescription) {
      if (this.workspace.description) {
        this.elements.workspaceDescription.textContent = this.workspace.description;
        this.elements.workspaceDescription.style.opacity = '1';
      } else {
        this.elements.workspaceDescription.textContent = 'No description';
        this.elements.workspaceDescription.style.opacity = '0.6';
      }
    }
    if (this.elements.workspaceBreadcrumb) {
      this.elements.workspaceBreadcrumb.textContent = this.workspace.name || 'Workspace';
    }
    if (this.elements.openCanvasBtn) {
      this.elements.openCanvasBtn.href = `/workspaces/${this.workspaceId}/canvas`;
    }

    // Update stats
    const agents = this.workspace.agent_instances || [];
    if (this.elements.agentCount) {
      this.elements.agentCount.textContent = agents.length;
    }

    // Load children workspaces from tree API
    await this.loadChildren();
  }

  /**
   * Load children workspaces from the tree API
   */
  async loadChildren() {
    try {
      // Fetch the full workspace tree to find children
      const response = await fetch('/api/workspaces?tree=true');
      if (!response.ok) {
        this.children = [];
        this.renderChildren();
        return;
      }

      const data = await response.json();
      const folders = data.folders || [];

      // Find this workspace in the tree and get its children
      const findWorkspace = (workspaces, targetId) => {
        for (const ws of workspaces) {
          if (ws.id === targetId) {
            return ws;
          }
          if (ws.children && ws.children.length > 0) {
            const found = findWorkspace(ws.children, targetId);
            if (found) return found;
          }
        }
        return null;
      };

      const currentWorkspace = findWorkspace(folders, this.workspaceId);
      this.children = currentWorkspace?.children || [];
      this.renderChildren();
    } catch (error) {
      console.error('Failed to load children workspaces:', error);
      this.children = [];
      this.renderChildren();
    }
  }

  /**
   * Render children workspaces
   */
  renderChildren() {
    // Show/hide the children panel based on whether there are children
    if (this.elements.childrenPanel) {
      if (this.children.length > 0) {
        this.elements.childrenPanel.style.display = '';
      } else {
        this.elements.childrenPanel.style.display = 'none';
        return;
      }
    }

    // Update count badge
    if (this.elements.childrenCount) {
      this.elements.childrenCount.textContent = this.children.length;
    }

    if (!this.elements.childrenList) return;

    this.elements.childrenList.innerHTML = this.children.map(child => {
      const name = child.name || 'Unnamed Workspace';
      const description = child.description || '';
      const colorAccent = child.color ? `border-left: 3px solid ${child.color};` : '';
      const childCount = (child.children || []).length;
      const hasChildren = childCount > 0;

      return `
        <div class="workspace-detail-child-card"
             data-workspace-id="${child.id}"
             role="button"
             tabindex="0"
             aria-label="Open workspace ${this.escapeHtml(name)}"
             onclick="window.location.href='/workspaces/${child.id}'"
             onkeydown="if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); window.location.href='/workspaces/${child.id}'; }"
             style="${colorAccent}">
          <div class="child-name">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" style="color: ${child.color || 'var(--text-secondary)'}; flex-shrink: 0;">
              ${hasChildren
                ? '<path d="M12,3L2,12H5V20H19V12H22L12,3M12,8.75A2.25,2.25 0 0,1 14.25,11A2.25,2.25 0 0,1 12,13.25A2.25,2.25 0 0,1 9.75,11A2.25,2.25 0 0,1 12,8.75M12,15C13.5,15 16.5,15.75 16.5,17.25V18H7.5V17.25C7.5,15.75 10.5,15 12,15Z"/>'
                : '<path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>'}
            </svg>
            <span>${this.escapeHtml(name)}</span>
          </div>
          ${description ? `<div class="child-desc" title="${this.escapeHtml(description)}">${this.escapeHtml(description)}</div>` : ''}
          <div class="child-meta">
            ${hasChildren ? `<span class="badge bg-secondary" style="font-size: 0.65rem;">${childCount} sub</span>` : ''}
            <span>Click to open</span>
          </div>
        </div>
      `;
    }).join('');
  }

  /**
   * Load tasks for the workspace
   */
  async loadTasks() {
    if (this.elements.tasksList) {
      this.elements.tasksList.innerHTML = '<div class="workspace-detail-loading">Loading tasks...</div>';
    }

    try {
      const response = await fetch(`/api/orchestration/tasks?studio_id=${encodeURIComponent(this.workspaceId)}`);
      if (!response.ok) throw new Error('Failed to load tasks');

      const data = await response.json();
      this.tasks = data.tasks || [];
      this.renderTasks();

      // Also refresh board if in board view
      if (this.currentView === 'board' && this.boardConfig) {
        this.renderBoard();
      }

      // Update task count
      if (this.elements.taskCount) {
        this.elements.taskCount.textContent = this.tasks.length;
      }
    } catch (error) {
      console.error('Failed to load tasks:', error);
      if (this.elements.tasksList) {
        this.elements.tasksList.innerHTML = '<div class="workspace-detail-empty">Failed to load tasks</div>';
      }
    }
  }

  /**
   * Render tasks list
   */
  renderTasks() {
    if (!this.elements.tasksList) return;

    if (this.tasks.length === 0) {
      this.elements.tasksList.innerHTML = '<div class="workspace-detail-empty">No tasks yet. Create one to get started.</div>';
      return;
    }

    // Filter to only show top-level tasks (no parent)
    const topLevelTasks = this.tasks.filter(t => !t.parent_task_id);

    this.elements.tasksList.innerHTML = topLevelTasks.map(task => {
      const taskLabel = this.escapeHtml(task.description || task.name || 'Untitled Task');
      const assignedAgent = task.to && task.to !== 'unassigned' ? task.to : '';
      const subtasks = this.tasks.filter((subtask) => subtask.parent_task_id === task.id);
      const isParent = subtasks.length > 0;
      const statusInfo = this.getTaskStatusPresentation(task);
      const hasUnassignedSubtasks = isParent && subtasks.some((subtask) => !subtask.to || subtask.to === 'unassigned');
      const hasRunningSubtasks = isParent && subtasks.some((subtask) => subtask.status === 'in_progress');
      const canExecute = isParent
        ? subtasks.length > 0 && !hasUnassignedSubtasks && !hasRunningSubtasks
        : task.status !== 'in_progress';
      const resultData = this.getDisplayResult(task, subtasks);
      const hasResultData = !!resultData;
      const hasAssistData = !!statusInfo.isBlocked;
      const executeTitle = isParent
        ? hasUnassignedSubtasks ? 'Assign agents to all subtasks before executing'
          : hasRunningSubtasks ? 'A subtask is already running'
            : 'Execute workflow now'
        : !assignedAgent ? 'Will auto-assign a workspace agent before execution'
          : task.status === 'in_progress' ? 'Task is already running'
            : 'Execute task now';
      const resultTitle = hasResultData
        ? `View ${resultData.label} from ${resultData.answeredBy || 'Unknown agent'}`
        : '';
      const assistTitle = statusInfo.reason || 'Agent needs your guidance before this task can continue.';

      return `
      <div class="workspace-detail-item" data-task-id="${task.id}">
        ${hasAssistData ? `
        <button type="button"
                class="workspace-detail-item-result"
                onclick="event.stopPropagation(); window.workspaceDetail?.openTaskAssistModal('${task.id}')"
                title="${this.escapeHtml(assistTitle)}"
                aria-label="Help blocked task ${taskLabel}">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor">
            <path d="M13,13H11V7H13M13,17H11V15H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z"/>
          </svg>
        </button>
        ` : hasResultData ? `
        <button type="button"
                class="workspace-detail-item-result"
                onclick="event.stopPropagation(); window.workspaceDetail?.showTaskResult('${task.id}')"
                title="${this.escapeHtml(resultTitle)}"
                aria-label="View result for task ${taskLabel}">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor">
            <path d="M14,17H7V15H14M17,13H7V11H17M17,9H7V7H17M19,3H5C3.89,3 3,3.89 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V5C21,3.89 20.1,3 19,3Z"/>
          </svg>
        </button>
        ` : ''}
        <button type="button"
                class="workspace-detail-item-run"
                onclick="event.stopPropagation(); window.workspaceDetail?.executeTask('${task.id}')"
                title="${this.escapeHtml(executeTitle)}"
                aria-label="Execute task ${taskLabel}"
                ${canExecute ? '' : 'disabled'}>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M8,5.14V19.14L19,12.14L8,5.14Z"/>
          </svg>
        </button>
        <button type="button" class="workspace-detail-item-delete" onclick="event.stopPropagation(); window.workspaceDetail?.deleteTask('${task.id}')" title="Delete task" aria-label="Delete task ${this.escapeHtml(task.description || task.name || 'Untitled Task')}">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
          </svg>
        </button>
        <div class="workspace-detail-item-content"
             role="button"
             tabindex="0"
             aria-label="Open task ${this.escapeHtml(task.description || task.name || 'Untitled Task')}"
             onclick="window.workspaceDetail?.openTask('${task.id}')"
             onkeydown="if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); window.workspaceDetail?.openTask('${task.id}'); }">
          <div class="d-flex justify-content-between align-items-start">
            <div class="workspace-detail-item-title">${taskLabel}</div>
            <span class="workspace-detail-task-status ${statusInfo.className}">${statusInfo.label}</span>
          </div>
          <div class="workspace-detail-item-meta">
            ${assignedAgent ? `<span class="workspace-detail-assigned-agent">Assigned to: ${this.escapeHtml(assignedAgent)}${this.renderAgentCapabilityBadges(assignedAgent)}</span> · ` : ''}
            ${formatDate(task.created_at)}
          </div>
        </div>
      </div>
      `;
    }).join('');
  }

  getTaskHumanLoop(task) {
    if (!task || typeof task !== 'object' || !task.context || typeof task.context !== 'object') {
      return null;
    }
    const humanLoop = task.context.human_loop;
    return humanLoop && typeof humanLoop === 'object' ? humanLoop : null;
  }

  getTaskStatusPresentation(task) {
    const humanLoop = this.getTaskHumanLoop(task);
    if (humanLoop && String(humanLoop.state || '').toLowerCase() === 'blocked') {
      const reason = String(humanLoop.reason || '').trim();
      return {
        className: 'blocked',
        label: 'Needs Input',
        isBlocked: true,
        reason
      };
    }

    return {
      className: getStatusClass(task?.status),
      label: getDisplayStatus(task?.status),
      isBlocked: false,
      reason: ''
    };
  }

  getDisplayResult(task, subtasks = []) {
    if (!task) return null;
    if (task.error) {
      return {
        label: 'Error',
        text: this.normalizeResultText(task.error),
        sourceTask: task,
        answeredBy: this.getAnsweringAgentLabel(task)
      };
    }
    if (task.result) {
      return {
        label: 'Result',
        text: this.normalizeResultText(task.result),
        sourceTask: task,
        answeredBy: this.getAnsweringAgentLabel(task)
      };
    }

    if (Array.isArray(subtasks) && subtasks.length > 0) {
      const orderedSubtasks = [...subtasks].sort((a, b) => {
        const aIndex = Number.isFinite(a?.subtask_index) && a.subtask_index > 0 ? a.subtask_index : Number.MAX_SAFE_INTEGER;
        const bIndex = Number.isFinite(b?.subtask_index) && b.subtask_index > 0 ? b.subtask_index : Number.MAX_SAFE_INTEGER;
        if (aIndex !== bIndex) return aIndex - bIndex;
        const aTime = a?.created_at ? new Date(a.created_at).getTime() : 0;
        const bTime = b?.created_at ? new Date(b.created_at).getTime() : 0;
        return aTime - bTime;
      });

      const lastSubtask = orderedSubtasks[orderedSubtasks.length - 1];
      if (lastSubtask?.error) {
        return {
          label: 'Error (last step)',
          text: this.normalizeResultText(lastSubtask.error),
          sourceTask: lastSubtask,
          answeredBy: this.getAnsweringAgentLabel(lastSubtask)
        };
      }
      if (lastSubtask?.result) {
        return {
          label: 'Result (last step)',
          text: this.normalizeResultText(lastSubtask.result),
          sourceTask: lastSubtask,
          answeredBy: this.getAnsweringAgentLabel(lastSubtask)
        };
      }
    }

    return null;
  }

  getAnsweringAgentLabel(task) {
    if (!task || typeof task !== 'object') return 'Unknown agent';

    const to = String(task.to || '').trim();
    const assignedNodeId = String(task.assigned_node_id || '').trim();
    const from = String(task.from || '').trim();

    if (assignedNodeId) {
      const nodeMatch = assignedNodeId.match(/^(.+)-node-(\d+)$/);
      if (nodeMatch) {
        const nodeAgentName = nodeMatch[1];
        const nodeNumber = nodeMatch[2];
        if (to && to !== 'unassigned' && to !== nodeAgentName) {
          return `${to} (${assignedNodeId})`;
        }
        return `${nodeAgentName} (node ${nodeNumber})`;
      }

      if (to && to !== 'unassigned') {
        return `${to} (${assignedNodeId})`;
      }
      return assignedNodeId;
    }

    if (to && to !== 'unassigned') return to;
    if (from && from !== 'system') return from;
    return 'Unknown agent';
  }

  showTaskResult(taskId) {
    const task = this.tasks.find((item) => item.id === taskId);
    if (!task) return;

    const subtasks = this.getSubtasksForParent(taskId);
    const isParent = subtasks.length > 0;
    const resultData = this.getDisplayResult(task, subtasks);
    if (!resultData || !resultData.text) {
      if (window.Toast) window.Toast.info('No result captured for this task yet.');
      return;
    }

    const taskName = task.description || task.name || task.id || 'Task Result';
    const statusText = getDisplayStatus(task.status);
    const timestamp = formatDate(task.completed_at || task.updated_at || task.created_at);
    const answeredBy = resultData.answeredBy || 'Unknown agent';
    const metaParts = [`Answered by ${answeredBy}`, `${statusText}`, timestamp];
    if (isParent) {
      metaParts.unshift(`${subtasks.length} step${subtasks.length === 1 ? '' : 's'}`);
    }

    if (this.elements.taskResultTitle) {
      this.elements.taskResultTitle.textContent = taskName;
    }
    if (this.elements.taskResultMeta) {
      this.elements.taskResultMeta.textContent = `${resultData.label} • ${metaParts.join(' • ')}`;
    }
    if (this.elements.taskResultBody) {
      this.elements.taskResultBody.textContent = String(resultData.text || '');
    }
    this.currentTaskResultText = String(resultData.text || '');

    if (this.elements.taskResultModal && window.bootstrap) {
      const modal = typeof bootstrap.Modal.getOrCreateInstance === 'function'
        ? bootstrap.Modal.getOrCreateInstance(this.elements.taskResultModal)
        : (bootstrap.Modal.getInstance(this.elements.taskResultModal) || new bootstrap.Modal(this.elements.taskResultModal));
      modal.show();
    }
  }

  populateAssistAgents(currentAgent = '') {
    if (!this.elements.taskAssistAgent) return;

    const select = this.elements.taskAssistAgent;
    const seen = new Set();
    const options = ['<option value="">Keep current assignment</option>'];

    const addOption = (agentName) => {
      const normalized = String(agentName || '').trim();
      const key = this.normalizeAgentName(normalized);
      if (!normalized || normalized === 'unassigned' || seen.has(key)) return;
      seen.add(key);
      const isCurrent = this.normalizeAgentName(normalized) === this.normalizeAgentName(currentAgent);
      const label = isCurrent ? `${normalized} (current)` : normalized;
      options.push(`<option value="${this.escapeHtml(normalized)}">${this.escapeHtml(label)}</option>`);
    };

    this.getWorkspaceAgentNames().forEach((name) => addOption(name));

    select.innerHTML = options.join('');
  }

  parseAssistActions(value) {
    if (Array.isArray(value)) {
      return value.map((item) => String(item || '').trim().toLowerCase()).filter(Boolean);
    }
    if (typeof value === 'string') {
      return value.split(',').map((item) => item.trim().toLowerCase()).filter(Boolean);
    }
    return [];
  }

  getAssistActionButton(action) {
    if (action === 'retry') return this.elements.taskAssistRetryBtn;
    if (action === 'continue_with_instruction') return this.elements.taskAssistContinueBtn;
    if (action === 'switch_agent_retry') return this.elements.taskAssistSwitchBtn;
    if (action === 'mark_failed') return this.elements.taskAssistFailBtn;
    return null;
  }

  updateAssistSwitchButtonState() {
    if (!this.elements.taskAssistSwitchBtn) return;
    const selectedAgent = String(this.elements.taskAssistAgent?.value || '').trim();
    this.elements.taskAssistSwitchBtn.disabled = selectedAgent === '';
  }

  setAssistRecommendationUI(recommendation) {
    this.currentAssistRecommendation = recommendation || null;

    [
      this.elements.taskAssistRetryBtn,
      this.elements.taskAssistContinueBtn,
      this.elements.taskAssistSwitchBtn,
      this.elements.taskAssistFailBtn
    ].forEach((button) => {
      button?.classList.remove('is-recommended');
    });

    if (this.elements.taskAssistRecommendationWrap && this.elements.taskAssistRecommendation) {
      if (recommendation && recommendation.text) {
        this.elements.taskAssistRecommendation.textContent = recommendation.text;
        this.elements.taskAssistRecommendationWrap.classList.remove('d-none');
      } else {
        this.elements.taskAssistRecommendation.textContent = '';
        this.elements.taskAssistRecommendationWrap.classList.add('d-none');
      }
    }

    if (recommendation?.suggestedAgent && this.elements.taskAssistAgent) {
      const hasOption = Array.from(this.elements.taskAssistAgent.options || [])
        .some((option) => String(option?.value || '') === recommendation.suggestedAgent);
      if (hasOption) {
        this.elements.taskAssistAgent.value = recommendation.suggestedAgent;
      }
    }

    const recommendedButton = this.getAssistActionButton(recommendation?.action || '');
    if (recommendedButton) {
      recommendedButton.classList.add('is-recommended');
    }

    this.updateAssistSwitchButtonState();
  }

  determineAssistRecommendation(reasonCode, suggestedActions, currentAgent) {
    const normalizedCode = String(reasonCode || '').trim().toLowerCase();
    const actions = this.parseAssistActions(suggestedActions);
    const allows = (action) => actions.length === 0 || actions.includes(action);
    const browserRelated = normalizedCode === 'capability_mismatch' ||
      normalizedCode === 'capability_refusal' ||
      normalizedCode.includes('capability');
    const browserAgent = browserRelated ? this.findBestBrowserCapableAgent(currentAgent) : '';

    if (browserRelated && browserAgent && allows('switch_agent_retry')) {
      return {
        action: 'switch_agent_retry',
        suggestedAgent: browserAgent,
        text: `Recommended: Switch to "${browserAgent}" and retry.`
      };
    }

    if (allows('continue_with_instruction')) {
      return {
        action: 'continue_with_instruction',
        text: 'Recommended: Add guidance and continue.'
      };
    }

    if (allows('retry')) {
      return {
        action: 'retry',
        text: 'Recommended: Retry with the current setup.'
      };
    }

    if (allows('switch_agent_retry') && browserAgent) {
      return {
        action: 'switch_agent_retry',
        suggestedAgent: browserAgent,
        text: `Recommended: Switch to "${browserAgent}" and retry.`
      };
    }

    return null;
  }

  openTaskAssistModal(taskId, eventData = null) {
    const task = this.tasks.find((item) => item.id === taskId);
    if (!task) return;

    const humanLoop = this.getTaskHumanLoop(task) || {};
    const payload = (eventData && typeof eventData === 'object') ? eventData : {};
    const blockId = String(payload.block_id || humanLoop.block_id || '').trim();
    const reasonCode = String(payload.reason_code || humanLoop.reason_code || '').trim();
    const suggestedActions = payload.suggested_actions || humanLoop.suggested_actions;
    const reason = String(payload.reason || humanLoop.reason || 'The assigned agent needs guidance before it can continue.').trim();
    const question = String(payload.question || humanLoop.question || 'How should I proceed?').trim();
    const response = String(payload.agent_response || humanLoop.agent_response || '').trim();
    const statusText = getDisplayStatus(task.status);
    const timestamp = formatDate(task.updated_at || task.created_at);

    const currentAgent = String(task.to || '').trim();
    this.currentBlockedTask = {
      taskId,
      blockId,
      currentAgent,
      reasonCode,
      suggestedActions: this.parseAssistActions(suggestedActions)
    };

    if (this.elements.taskAssistMeta) {
      this.elements.taskAssistMeta.textContent = `${task.description || task.name || task.id} • ${statusText} • ${timestamp}`;
    }
    if (this.elements.taskAssistReason) {
      this.elements.taskAssistReason.textContent = reason;
    }
    if (this.elements.taskAssistQuestion) {
      this.elements.taskAssistQuestion.textContent = question;
    }
    if (this.elements.taskAssistMessage) {
      this.elements.taskAssistMessage.value = '';
    }
    if (this.elements.taskAssistResponse && this.elements.taskAssistResponseWrap) {
      if (response) {
        this.elements.taskAssistResponse.textContent = response;
        this.elements.taskAssistResponseWrap.classList.remove('d-none');
      } else {
        this.elements.taskAssistResponse.textContent = '';
        this.elements.taskAssistResponseWrap.classList.add('d-none');
      }
    }

    this.populateAssistAgents(currentAgent);
    const recommendation = this.determineAssistRecommendation(reasonCode, suggestedActions, currentAgent);
    this.setAssistRecommendationUI(recommendation);

    if (this.elements.taskAssistModal && window.bootstrap) {
      const modal = typeof bootstrap.Modal.getOrCreateInstance === 'function'
        ? bootstrap.Modal.getOrCreateInstance(this.elements.taskAssistModal)
        : (bootstrap.Modal.getInstance(this.elements.taskAssistModal) || new bootstrap.Modal(this.elements.taskAssistModal));
      modal.show();
    }
  }

  async submitTaskAssist(action) {
    if (!this.currentBlockedTask?.taskId) return;

    const selectedAgent = String(this.elements.taskAssistAgent?.value || '').trim();
    const message = String(this.elements.taskAssistMessage?.value || '').trim();
    if (action === 'switch_agent_retry' && !selectedAgent) {
      if (window.Toast) window.Toast.error('Select an agent to switch before retrying.');
      return;
    }
    if (action === 'switch_agent_retry' &&
        this.normalizeAgentName(selectedAgent) === this.normalizeAgentName(this.currentBlockedTask.currentAgent)) {
      if (window.Toast) window.Toast.error('Select a different agent before switching.');
      return;
    }

    const payload = {
      action,
      block_id: this.currentBlockedTask.blockId || undefined,
      message: message || undefined,
      agent: selectedAgent || undefined
    };

    try {
      const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(this.currentBlockedTask.taskId)}/assist`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to submit assistance');
      }

      if (window.Toast) window.Toast.success('Task updated');
      await this.loadTasks();

      if (action !== 'mark_failed' && this.elements.taskAssistModal && window.bootstrap) {
        const modal = bootstrap.Modal.getInstance(this.elements.taskAssistModal);
        modal?.hide();
      }
    } catch (error) {
      console.error('Failed to assist blocked task:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to assist task');
    }
  }

  normalizeResultText(value) {
    if (value === undefined || value === null) return '';
    if (typeof value === 'string') return value;
    if (typeof value === 'number' || typeof value === 'boolean') return String(value);
    try {
      return JSON.stringify(value, null, 2);
    } catch (_error) {
      return String(value);
    }
  }

  getSubtasksForParent(taskId) {
    if (!taskId) return [];
    return this.tasks.filter((task) => task.parent_task_id === taskId);
  }

  normalizeAgentName(name) {
    return String(name || '').trim().toLowerCase();
  }

  getWorkspaceAgentNames() {
    if (!this.workspace) return [];

    const seen = new Set();
    const names = [];
    const add = (name) => {
      const normalized = String(name || '').trim();
      if (!normalized || normalized === 'unassigned') return;
      const key = this.normalizeAgentName(normalized);
      if (seen.has(key)) return;
      seen.add(key);
      names.push(normalized);
    };

    if (Array.isArray(this.workspace.agent_instances)) {
      this.workspace.agent_instances.forEach((instance) => add(instance?.name));
    }
    if (Array.isArray(this.workspace.agents)) {
      this.workspace.agents.forEach((name) => add(name));
    }

    return names;
  }

  getAgentProfile(agentName) {
    const key = this.normalizeAgentName(agentName);
    if (!key || !this.agentIndex) return null;
    return this.agentIndex.get(key) || null;
  }

  async loadAgentCatalog(force = false) {
    if (!force && this.agentIndex instanceof Map && this.agentIndex.size > 0) {
      return this.agentCatalog;
    }

    const nextCatalog = [];
    const nextIndex = new Map();
    try {
      const response = await fetch('/api/agents/dashboard/list');
      if (response.ok) {
        const data = await response.json();
        const agents = Array.isArray(data?.agents) ? data.agents : [];
        agents.forEach((agent) => {
          const name = String(agent?.name || '').trim();
          if (!name) return;

          const profile = {
            name,
            capabilities: Array.isArray(agent?.capabilities) ? agent.capabilities.map((value) => String(value || '').trim()).filter(Boolean) : [],
            allowWebSearch: Boolean(agent?.allow_web_search),
            enabledPlugins: Array.isArray(agent?.enabled_plugins) ? agent.enabled_plugins.map((value) => String(value || '').trim()).filter(Boolean) : [],
            mcpServers: Array.isArray(agent?.mcp_servers) ? agent.mcp_servers.map((value) => String(value || '').trim()).filter(Boolean) : []
          };

          nextCatalog.push(profile);
          nextIndex.set(this.normalizeAgentName(name), profile);
        });
      }
    } catch (error) {
      console.error('Failed to load agent catalog:', error);
    }

    this.agentCatalog = nextCatalog;
    this.agentIndex = nextIndex;

    if (!Array.isArray(this.agentOptions) || this.agentOptions.length === 0) {
      this.agentOptions = this.buildAgentOptionsFromCatalog();
    }

    return this.agentCatalog;
  }

  buildAgentOptionsFromCatalog() {
    const options = [{ label: 'Unassigned', value: '' }];
    const seen = new Set();
    this.agentCatalog.forEach((profile) => {
      const name = String(profile?.name || '').trim();
      if (!name) return;
      const key = this.normalizeAgentName(name);
      if (seen.has(key)) return;
      seen.add(key);
      options.push({ label: name, value: `node:${name}-node-1` });
    });
    return options;
  }

  agentSupportsBrowserAutomation(profile) {
    if (!profile || !profile.allowWebSearch) {
      return false;
    }

    const lowerCapabilities = new Set((profile.capabilities || []).map((value) => String(value || '').trim().toLowerCase()).filter(Boolean));
    if (lowerCapabilities.has('browser') || lowerCapabilities.has('browser_automation') || lowerCapabilities.has('web_search') || lowerCapabilities.has('web_fetch')) {
      return true;
    }

    const pluginNames = (profile.enabledPlugins || []).map((value) => String(value || '').trim().toLowerCase()).filter(Boolean);
    for (const name of pluginNames) {
      if (name.startsWith('browser') ||
          name.startsWith('web_fetch') ||
          name.startsWith('web_search') ||
          name === 'navigate' ||
          name === 'open_url' ||
          name.includes('playwright') ||
          name.includes('browserbase') ||
          name.includes('puppeteer')) {
        return true;
      }
    }

    const serverNames = (profile.mcpServers || []).map((value) => String(value || '').trim().toLowerCase()).filter(Boolean);
    for (const name of serverNames) {
      if (name.includes('playwright') || name.includes('browserbase') || name.includes('puppeteer') || name.includes('browser')) {
        return true;
      }
    }

    return false;
  }

  findBestBrowserCapableAgent(excludeAgent = '') {
    const exclude = this.normalizeAgentName(excludeAgent);
    const candidates = this.getWorkspaceAgentNames();
    for (const candidate of candidates) {
      if (this.normalizeAgentName(candidate) === exclude) continue;
      if (this.agentSupportsBrowserAutomation(this.getAgentProfile(candidate))) {
        return candidate;
      }
    }
    return '';
  }

  isLikelyBrowserAutomationIntent(description) {
    const lower = String(description || '').trim().toLowerCase();
    if (!lower) return false;

    const verbs = ['open', 'visit', 'navigate', 'go to', 'browse', 'click', 'fill', 'type', 'extract'];
    const hasVerb = verbs.some((verb) => lower.includes(verb));
    if (!hasVerb) return false;

    if (lower.includes('http://') || lower.includes('https://') || lower.includes('www.')) {
      return true;
    }

    const tokens = lower.split(/\s+/);
    for (const token of tokens) {
      const cleaned = token.replace(/^[\s,.;:!?"'`()\[\]{}<>]+|[\s,.;:!?"'`()\[\]{}<>]+$/g, '');
      if (!cleaned || cleaned.includes('/') || (cleaned.split('.').length - 1) < 1) continue;
      const parts = cleaned.split('.');
      const tld = parts[parts.length - 1];
      if (tld.length >= 2 && tld.length <= 12) {
        return true;
      }
    }

    return false;
  }

  getTaskExecutionPreflight(task, assignedAgent) {
    const description = String(task?.description || task?.name || '').trim();
    if (!this.isLikelyBrowserAutomationIntent(description)) {
      return { kind: 'none' };
    }

    const currentAgent = String(assignedAgent || '').trim();
    if (!currentAgent) {
      return { kind: 'none' };
    }

    const currentProfile = this.getAgentProfile(currentAgent);
    if (!currentProfile || this.agentSupportsBrowserAutomation(currentProfile)) {
      return { kind: 'none' };
    }

    const recommendedAgent = this.findBestBrowserCapableAgent(currentAgent);
    if (recommendedAgent) {
      return {
        kind: 'switch_recommended',
        recommendedAgent,
        message: `"${currentAgent}" likely cannot execute browser actions for this task.`
      };
    }

    return {
      kind: 'warning',
      message: `This looks like a browser task, but "${currentAgent}" likely lacks browser capability in this workspace.`
    };
  }

  getAgentCapabilityBadges(agentName) {
    const profile = this.getAgentProfile(agentName);
    if (!profile) return [];

    const badges = [];
    if (this.agentSupportsBrowserAutomation(profile)) {
      badges.push({ key: 'browser', label: 'Browser' });
    }
    if (profile.allowWebSearch) {
      badges.push({ key: 'web', label: 'Web' });
    }
    if ((profile.mcpServers || []).length > 0) {
      badges.push({ key: 'mcp', label: 'MCP' });
    }
    if ((profile.enabledPlugins || []).length > 0) {
      badges.push({ key: 'tools', label: 'Tools' });
    }

    return badges.slice(0, 4);
  }

  renderAgentCapabilityBadges(agentName) {
    const badges = this.getAgentCapabilityBadges(agentName);
    if (!badges.length) return '';

    const chips = badges
      .map((badge) => `<span class="workspace-detail-capability-chip ${this.escapeHtml(badge.key)}">${this.escapeHtml(badge.label)}</span>`)
      .join('');

    return `<span class="workspace-detail-agent-capabilities">${chips}</span>`;
  }

  getWorkspaceFallbackAgent(options = {}) {
    const candidates = this.getWorkspaceAgentNames();
    if (candidates.length === 0) return '';

    if (options.preferBrowser === true) {
      const browserCandidate = candidates.find((name) => this.agentSupportsBrowserAutomation(this.getAgentProfile(name)));
      if (browserCandidate) {
        return browserCandidate;
      }
    }

    return candidates[0];
  }

  async assignTaskToAgent(taskId, agentName) {
    const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(taskId)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ to: agentName })
    });
    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to assign task');
    }
  }

  async executeTask(taskId) {
    await this.loadAgentCatalog();

    const task = this.tasks.find((item) => item.id === taskId);
    if (!task) return;

    const subtasks = this.getSubtasksForParent(taskId);
    const isParent = subtasks.length > 0;
    const isBrowserIntent = !isParent && this.isLikelyBrowserAutomationIntent(task.description || task.name || '');

    if (isParent) {
      const hasUnassigned = subtasks.some((subtask) => !subtask.to || subtask.to === 'unassigned');
      if (hasUnassigned) {
        if (window.Toast) window.Toast.error('Assign agents to all subtasks before executing this workflow.');
        return;
      }
      const hasRunning = subtasks.some((subtask) => subtask.status === 'in_progress');
      if (hasRunning) {
        if (window.Toast) window.Toast.error('A subtask is already running.');
        return;
      }
    } else if (task.status === 'in_progress') {
      if (window.Toast) window.Toast.error('This task is already running.');
      return;
    }

    let assignedAgent = task.to && task.to !== 'unassigned' ? task.to : '';
    if (!isParent && !assignedAgent) {
      const fallbackAgent = this.getWorkspaceFallbackAgent({ preferBrowser: isBrowserIntent });
      if (!fallbackAgent) {
        if (window.Toast) window.Toast.error('No agent is available in this workspace. Add an agent or assign this task first.');
        return;
      }

      const assignAndRun = confirm(`This task is unassigned. Assign it to "${fallbackAgent}" and execute now?`);
      if (!assignAndRun) return;

      try {
        await this.assignTaskToAgent(taskId, fallbackAgent);
        assignedAgent = fallbackAgent;
        task.to = fallbackAgent;
      } catch (error) {
        console.error('Failed to auto-assign task before execution:', error);
        if (window.Toast) window.Toast.error('Failed to assign task before execution');
        return;
      }
    }

    let preflightWarning = '';
    if (!isParent) {
      const preflight = this.getTaskExecutionPreflight(task, assignedAgent);
      if (preflight.kind === 'switch_recommended' && preflight.recommendedAgent) {
        const switchNow = confirm(`${preflight.message}\n\nRecommended: switch to "${preflight.recommendedAgent}" before execution. Switch now?`);
        if (switchNow) {
          try {
            await this.assignTaskToAgent(taskId, preflight.recommendedAgent);
            assignedAgent = preflight.recommendedAgent;
            task.to = preflight.recommendedAgent;
          } catch (error) {
            console.error('Failed to switch task to recommended agent:', error);
            if (window.Toast) window.Toast.error('Failed to switch to recommended agent');
            return;
          }
        } else {
          preflightWarning = `${preflight.message} Continuing may trigger a pause for user input.`;
        }
      } else if (preflight.kind === 'warning') {
        preflightWarning = preflight.message;
      }
    }

    const baseConfirmMessage = isParent
      ? `Execute this workflow (${subtasks.length} step${subtasks.length === 1 ? '' : 's'}) now?`
      : `Execute this task${assignedAgent ? ` with "${assignedAgent}"` : ''} now?`;
    const confirmMessage = preflightWarning ? `${preflightWarning}\n\n${baseConfirmMessage}` : baseConfirmMessage;
    if (!confirm(confirmMessage)) return;

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
      await this.loadTasks();
      this.pollTaskCompletion(taskId);
    } catch (error) {
      console.error('Failed to execute task:', error);
      if (window.Toast) window.Toast.error('Failed to execute task');
    }
  }

  async pollTaskCompletion(taskId, maxAttempts = 36, intervalMs = 5000) {
    let attempts = 0;

    const poll = async () => {
      attempts += 1;
      if (attempts > maxAttempts) return;

      try {
        const response = await fetch(`/api/orchestration/tasks?id=${encodeURIComponent(taskId)}`);
        if (!response.ok) {
          setTimeout(poll, intervalMs);
          return;
        }

        const task = await response.json();
        const status = task && task.status;
        if (status === 'completed' || status === 'failed' || status === 'cancelled' || status === 'timeout') {
          await this.loadTasks();
          return;
        }
      } catch (error) {
        console.error('Failed to poll task status:', error);
      }

      setTimeout(poll, intervalMs);
    };

    setTimeout(poll, intervalMs);
  }

  /**
   * Set the current view (list or board)
   */
  setView(view) {
    this.currentView = view;

    // Update toggle buttons
    if (this.elements.viewListBtn) {
      this.elements.viewListBtn.classList.toggle('is-active', view === 'list');
      this.elements.viewListBtn.setAttribute('aria-selected', (view === 'list').toString());
    }
    if (this.elements.viewBoardBtn) {
      this.elements.viewBoardBtn.classList.toggle('is-active', view === 'board');
      this.elements.viewBoardBtn.setAttribute('aria-selected', (view === 'board').toString());
    }

    // Toggle panel class for expanded board view
    if (this.elements.tasksPanel) {
      this.elements.tasksPanel.classList.toggle('board-view', view === 'board');
    }

    // Show/hide views
    if (this.elements.tasksList) {
      this.elements.tasksList.style.display = view === 'list' ? '' : 'none';
    }
    if (this.elements.tasksBoard) {
      this.elements.tasksBoard.style.display = view === 'board' ? '' : 'none';
    }

    // Load board if switching to board view
    if (view === 'board') {
      this.loadBoard();
    }
  }

  /**
   * Load board configuration and render
   */
  async loadBoard() {
    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/board`);
      if (!response.ok) {
        this.boardConfig = null;
        this.renderBoard();
        return;
      }

      const data = await response.json();
      this.boardConfig = data.board || null;
      await this.ensureAgentOptions();
      this.renderBoard();
    } catch (error) {
      console.error('Failed to load board:', error);
      this.boardConfig = null;
      this.renderBoard();
    }
  }

  async saveBoardConfig(columns) {
    const payload = { columns };
    if (this.boardConfig?.version) {
      payload.version = this.boardConfig.version;
    }

    const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/board`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });

    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to save board');
    }

    const data = await response.json();
    return data.board || { columns };
  }

  async ensureAgentOptions() {
    if (Array.isArray(this.agentOptions) && this.agentOptions.length > 0) {
      return this.agentOptions;
    }

    await this.loadAgentCatalog();
    if (Array.isArray(this.agentOptions) && this.agentOptions.length > 0) {
      return this.agentOptions;
    }

    const options = [{ label: 'Unassigned', value: '' }];
    this.getWorkspaceAgentNames().forEach((name) => {
      const nodeId = `${name}-node-1`;
      options.push({ label: name, value: `node:${nodeId}` });
    });
    this.agentOptions = options;
    return this.agentOptions;
  }

  getKanbanLabels(task) {
    const ctx = task?.context;
    if (!ctx || typeof ctx !== 'object') return [];
    const raw = ctx.kanban_labels;
    if (Array.isArray(raw)) {
      return raw.map((label) => String(label || '').trim()).filter(Boolean);
    }
    if (typeof raw === 'string') {
      return raw.split(',').map((label) => label.trim()).filter(Boolean);
    }
    return [];
  }

  formatLabelsInput(labels) {
    return (labels || []).join(', ');
  }

  parseLabelsInput(value) {
    if (!value) return [];
    return value.split(',').map((label) => label.trim()).filter(Boolean);
  }

  getKanbanDueDate(task) {
    const ctx = task?.context;
    if (!ctx || typeof ctx !== 'object') return '';
    const raw = ctx.kanban_due_date;
    return typeof raw === 'string' ? raw.trim() : '';
  }

  normalizeDueInput(value) {
    if (!value) return '';
    if (/^\d{4}-\d{2}-\d{2}$/.test(value)) return value;
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '';
    return date.toISOString().slice(0, 10);
  }

  formatDueDate(value) {
    if (!value) return '';
    const date = /^\d{4}-\d{2}-\d{2}$/.test(value) ? new Date(`${value}T00:00:00`) : new Date(value);
    if (Number.isNaN(date.getTime())) return value;
    return date.toLocaleDateString();
  }

  getAssignmentValue(task) {
    if (task?.assigned_node_id) return `node:${task.assigned_node_id}`;
    if (task?.to && task.to !== 'unassigned') return `node:${task.to}-node-1`;
    return '';
  }

  getAssignmentLabel(task) {
    if (task?.assigned_node_id) {
      const match = String(task.assigned_node_id).match(/^(.+)-node-\d+$/);
      return match ? match[1] : task.assigned_node_id;
    }
    if (task?.to && task.to !== 'unassigned') return task.to;
    return 'Unassigned';
  }

  buildAssignmentOptions(selectedValue, selectedLabel) {
    const options = Array.isArray(this.agentOptions) ? this.agentOptions.slice() : [{ label: 'Unassigned', value: '' }];
    if (selectedValue && !options.some((opt) => opt.value === selectedValue)) {
      options.push({ label: selectedLabel || selectedValue, value: selectedValue });
    }
    return options;
  }

  getNextColumnOrder(columns) {
    const maxOrder = (columns || []).reduce((max, col) => {
      const value = Number.isFinite(col?.order) ? col.order : 0;
      return value > max ? value : max;
    }, 0);
    return maxOrder + 1;
  }

  makeColumnId(name, columns) {
    const existing = new Set((columns || []).map((col) => String(col?.id || '').trim()).filter(Boolean));
    const base = String(name || '')
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/(^-|-$)/g, '') || 'column';
    let id = base;
    let idx = 1;
    while (existing.has(id)) {
      idx += 1;
      id = `${base}-${idx}`;
    }
    return id;
  }

  /**
   * Render the kanban board
   */
  renderBoard() {
    if (!this.elements.boardColumns || !this.elements.boardEmpty) return;

    const columns = this.boardConfig?.columns || [];

    if (columns.length === 0) {
      this.elements.boardColumns.querySelectorAll('.workspace-detail-board-card').forEach((card) => {
        if (card._editOutsideHandler) {
          document.removeEventListener('click', card._editOutsideHandler);
          card._editOutsideHandler = null;
        }
      });
      this.elements.boardColumns.querySelectorAll('.workspace-detail-board-add').forEach((addEl) => {
        if (addEl._addOutsideHandler) {
          document.removeEventListener('click', addEl._addOutsideHandler);
          addEl._addOutsideHandler = null;
        }
      });
      this.elements.boardColumns.querySelectorAll('.workspace-detail-board-add-column').forEach((addEl) => {
        if (addEl._addOutsideHandler) {
          document.removeEventListener('click', addEl._addOutsideHandler);
          addEl._addOutsideHandler = null;
        }
      });
      this.elements.boardColumns.innerHTML = '';
      this.elements.boardEmpty.style.display = '';
      return;
    }

    this.elements.boardEmpty.style.display = 'none';

    // Group tasks by column
    const tasksByColumn = this.groupTasksByColumn(columns);

    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-card').forEach((card) => {
      if (card._editOutsideHandler) {
        document.removeEventListener('click', card._editOutsideHandler);
        card._editOutsideHandler = null;
      }
    });
    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-add').forEach((addEl) => {
      if (addEl._addOutsideHandler) {
        document.removeEventListener('click', addEl._addOutsideHandler);
        addEl._addOutsideHandler = null;
      }
    });
    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-add-column').forEach((addEl) => {
      if (addEl._addOutsideHandler) {
        document.removeEventListener('click', addEl._addOutsideHandler);
        addEl._addOutsideHandler = null;
      }
    });

    const columnsHtml = columns.map(col => {
      const columnTasks = tasksByColumn[col.id] || [];
      return `
        <div class="workspace-detail-board-column" data-column-id="${col.id}">
          <div class="workspace-detail-board-column-header">
            <div class="workspace-detail-board-column-title-wrap">
              <span class="workspace-detail-board-column-title" data-column-id="${this.escapeHtml(col.id)}">${this.escapeHtml(col.name)}</span>
              <button class="workspace-detail-board-column-edit-btn" type="button" title="Edit column name" aria-label="Edit column name">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M5,18.08V19H5.92L14.81,10.11L13.89,9.19L5,18.08M17.71,7.04C18.1,6.65 18.1,6 17.71,5.63L16.37,4.29C16,3.9 15.35,3.9 14.96,4.29L13.13,6.12L14.88,7.87L17.71,7.04Z"/>
                </svg>
              </button>
            </div>
            <span class="workspace-detail-board-column-count">${columnTasks.length}</span>
          </div>
          <div class="workspace-detail-board-column-body" data-column-id="${this.escapeHtml(col.id)}">
            ${columnTasks.map(task => this.renderBoardCard(task)).join('')}
            <div class="workspace-detail-board-add" data-column-id="${this.escapeHtml(col.id)}">
              <button class="workspace-detail-board-add-btn" type="button">+ Add card</button>
              <div class="workspace-detail-board-add-form" hidden>
                <input class="workspace-detail-board-add-input" type="text" placeholder="Task title" />
                <div class="workspace-detail-board-add-actions">
                  <button class="modern-btn modern-btn-secondary workspace-detail-board-add-cancel" type="button">Cancel</button>
                  <button class="modern-btn modern-btn-primary workspace-detail-board-add-submit" type="button">Add</button>
                </div>
              </div>
            </div>
          </div>
        </div>
      `;
    }).join('');

    const addColumnHtml = `
      <div class="workspace-detail-board-add-column">
        <button class="workspace-detail-board-add-column-btn" type="button">+ Add column</button>
        <div class="workspace-detail-board-add-column-form" hidden>
          <input class="workspace-detail-board-add-column-input" type="text" placeholder="Column name" />
          <div class="workspace-detail-board-add-column-actions">
            <button class="modern-btn modern-btn-secondary workspace-detail-board-add-column-cancel" type="button">Cancel</button>
            <button class="modern-btn modern-btn-primary workspace-detail-board-add-column-submit" type="button">Add</button>
          </div>
        </div>
      </div>
    `;

    this.elements.boardColumns.innerHTML = columnsHtml + addColumnHtml;

    this.wireBoardDragAndDrop();
    this.wireBoardCardEditing();
    this.wireBoardCardAdd();
    this.wireBoardColumnAdd();
    this.wireBoardColumnRename();
  }

  /**
   * Render a single board card
   */
  renderBoardCard(task) {
    const labels = this.getKanbanLabels(task);
    const dueDate = this.getKanbanDueDate(task);
    const assignmentValue = this.getAssignmentValue(task);
    const assignmentLabel = this.getAssignmentLabel(task);
    const assignmentOptions = this.buildAssignmentOptions(assignmentValue, assignmentLabel);
    const labelMarkup = labels.length > 0
      ? `<div class="workspace-detail-board-card-labels">${labels.map((label) => `<span class="workspace-detail-board-card-label">${this.escapeHtml(label)}</span>`).join('')}</div>`
      : '';
    const dueMarkup = dueDate ? `<span class="workspace-detail-board-card-due">Due ${this.escapeHtml(this.formatDueDate(dueDate))}</span>` : '';
    const assignedMarkup = assignmentLabel && assignmentLabel !== 'Unassigned'
      ? `<span class="workspace-detail-board-card-assignee">${this.escapeHtml(assignmentLabel)}</span>`
      : '<span class="workspace-detail-board-card-assignee is-muted">Unassigned</span>';
    const editTitleValue = this.escapeHtml(task.description || task.name || task.id || '');
    const editDetailsValue = this.escapeHtml(task.details || '');
    const editLabelsValue = this.escapeHtml(this.formatLabelsInput(labels));
    const editDueValue = this.escapeHtml(this.normalizeDueInput(dueDate));
    const assignmentOptionsHtml = assignmentOptions
      .map((opt) => {
        const selected = opt.value === assignmentValue ? ' selected' : '';
        return `<option value="${this.escapeHtml(opt.value)}"${selected}>${this.escapeHtml(opt.label)}</option>`;
      })
      .join('');

    return `
      <div class="workspace-detail-board-card" draggable="true" data-task-id="${this.escapeHtml(task.id)}">
        <div class="workspace-detail-board-card-view">
          <div class="workspace-detail-board-card-header">
            <div class="workspace-detail-board-card-title">${this.escapeHtml(task.description || task.name || task.id || 'Untitled')}</div>
            <button class="workspace-detail-board-card-edit-btn" type="button" title="Edit card" aria-label="Edit card">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                <path d="M5,18.08V19H5.92L14.81,10.11L13.89,9.19L5,18.08M17.71,7.04C18.1,6.65 18.1,6 17.71,5.63L16.37,4.29C16,3.9 15.35,3.9 14.96,4.29L13.13,6.12L14.88,7.87L17.71,7.04Z"/>
              </svg>
            </button>
          </div>
          ${labelMarkup}
          <div class="workspace-detail-board-card-meta">
            ${assignedMarkup}
            ${dueMarkup}
          </div>
          <div class="workspace-detail-board-card-meta workspace-detail-board-card-meta-secondary">
            <span>${getDisplayStatus(task.status)}</span>
          </div>
        </div>
        <div class="workspace-detail-board-card-edit" hidden>
          <label class="workspace-detail-board-card-edit-label">Title</label>
          <input class="workspace-detail-board-card-edit-input workspace-detail-board-card-edit-title" type="text" value="${editTitleValue}" />
          <label class="workspace-detail-board-card-edit-label">Description</label>
          <textarea class="workspace-detail-board-card-edit-input workspace-detail-board-card-edit-details" rows="3">${editDetailsValue}</textarea>
          <label class="workspace-detail-board-card-edit-label">Assignee</label>
          <select class="workspace-detail-board-card-edit-input workspace-detail-board-card-edit-assignee">
            ${assignmentOptionsHtml}
          </select>
          <label class="workspace-detail-board-card-edit-label">Labels</label>
          <input class="workspace-detail-board-card-edit-input workspace-detail-board-card-edit-labels" type="text" value="${editLabelsValue}" placeholder="design, frontend" />
          <label class="workspace-detail-board-card-edit-label">Due date</label>
          <input class="workspace-detail-board-card-edit-input workspace-detail-board-card-edit-due" type="date" value="${editDueValue}" />
          <div class="workspace-detail-board-card-edit-actions">
            <button class="modern-btn modern-btn-secondary workspace-detail-board-card-edit-cancel" type="button">Cancel</button>
            <button class="modern-btn modern-btn-primary workspace-detail-board-card-edit-save" type="button">Done</button>
          </div>
        </div>
      </div>
    `;
  }

  /**
   * Group tasks by their kanban column
   */
  groupTasksByColumn(columns) {
    const groups = {};
    columns.forEach(col => { groups[col.id] = []; });

    // Only include top-level tasks
    const topLevelTasks = this.tasks.filter(t => !t.parent_task_id);

    topLevelTasks.forEach(task => {
      const colId = task.context?.kanban_column_id || columns[0]?.id;
      if (groups[colId]) {
        groups[colId].push(task);
      } else if (columns.length > 0) {
        // Fallback to first column
        groups[columns[0].id].push(task);
      }
    });

    return groups;
  }

  parseAssignmentValue(value) {
    let to = '';
    let assignedNodeId = '';
    if (value && value.startsWith('node:')) {
      assignedNodeId = value.slice('node:'.length);
      const match = assignedNodeId.match(/^(.+)-node-\d+$/);
      to = match ? match[1] : assignedNodeId;
    }
    return { to, assignedNodeId };
  }

  getBoardCardEditValues(card) {
    const titleEl = card.querySelector('.workspace-detail-board-card-edit-title');
    const detailsEl = card.querySelector('.workspace-detail-board-card-edit-details');
    const assigneeEl = card.querySelector('.workspace-detail-board-card-edit-assignee');
    const labelsEl = card.querySelector('.workspace-detail-board-card-edit-labels');
    const dueEl = card.querySelector('.workspace-detail-board-card-edit-due');

    const title = titleEl ? titleEl.value.trim() : '';
    const details = detailsEl ? detailsEl.value.trim() : '';
    const assigneeValue = assigneeEl ? assigneeEl.value : '';
    const labels = this.parseLabelsInput(labelsEl ? labelsEl.value : '');
    const dueDate = dueEl ? dueEl.value : '';
    const assignment = this.parseAssignmentValue(assigneeValue);

    return {
      title,
      details,
      assigneeValue,
      labels,
      dueDate,
      to: assignment.to,
      assignedNodeId: assignment.assignedNodeId
    };
  }

  hasBoardCardChanges(current, original) {
    if (!original) return true;
    if (current.title !== original.title) return true;
    if (current.details !== original.details) return true;
    if (current.assigneeValue !== original.assigneeValue) return true;
    if (current.dueDate !== original.dueDate) return true;
    const currentLabels = (current.labels || []).join('|');
    const originalLabels = (original.labels || []).join('|');
    return currentLabels !== originalLabels;
  }

  enterBoardCardEdit(card) {
    if (!card || card.classList.contains('is-editing')) return;
    const editEl = card.querySelector('.workspace-detail-board-card-edit');
    const viewEl = card.querySelector('.workspace-detail-board-card-view');
    if (!editEl) return;

    card.classList.add('is-editing');
    card.setAttribute('draggable', 'false');
    if (viewEl) viewEl.setAttribute('hidden', '');
    editEl.removeAttribute('hidden');

    card._editOriginal = this.getBoardCardEditValues(card);

    const focusEl = editEl.querySelector('input, textarea, select');
    if (focusEl) {
      focusEl.focus();
      if (focusEl.select) focusEl.select();
    }

    setTimeout(() => {
      const handler = (evt) => {
        if (card.contains(evt.target)) return;
        this.saveBoardCardEdits(card);
      };
      card._editOutsideHandler = handler;
      document.addEventListener('click', handler);
    }, 0);
  }

  exitBoardCardEdit(card, { reset = false } = {}) {
    if (!card || !card.classList.contains('is-editing')) return;
    const editEl = card.querySelector('.workspace-detail-board-card-edit');
    const viewEl = card.querySelector('.workspace-detail-board-card-view');

    if (reset && card._editOriginal) {
      const titleEl = card.querySelector('.workspace-detail-board-card-edit-title');
      const detailsEl = card.querySelector('.workspace-detail-board-card-edit-details');
      const assigneeEl = card.querySelector('.workspace-detail-board-card-edit-assignee');
      const labelsEl = card.querySelector('.workspace-detail-board-card-edit-labels');
      const dueEl = card.querySelector('.workspace-detail-board-card-edit-due');
      if (titleEl) titleEl.value = card._editOriginal.title || '';
      if (detailsEl) detailsEl.value = card._editOriginal.details || '';
      if (assigneeEl) assigneeEl.value = card._editOriginal.assigneeValue || '';
      if (labelsEl) labelsEl.value = this.formatLabelsInput(card._editOriginal.labels || []);
      if (dueEl) dueEl.value = card._editOriginal.dueDate || '';
    }

    card.classList.remove('is-editing');
    card.setAttribute('draggable', 'true');
    if (editEl) editEl.setAttribute('hidden', '');
    if (viewEl) viewEl.removeAttribute('hidden');

    if (card._editOutsideHandler) {
      document.removeEventListener('click', card._editOutsideHandler);
      card._editOutsideHandler = null;
    }
    card._editOriginal = null;
  }

  async updateTaskDetails(taskId, updates) {
    const payload = {
      description: updates.title,
      details: updates.details,
      to: updates.to,
      assigned_node_id: updates.assignedNodeId,
      kanban_labels: updates.labels,
      kanban_due_date: updates.dueDate
    };

    const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(taskId)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to update task');
    }

    const updated = await response.json();
    const idx = this.tasks.findIndex((t) => t && t.id === taskId);
    if (idx >= 0) {
      this.tasks.splice(idx, 1, { ...this.tasks[idx], ...updated });
    }

    this.renderTasks();
    if (this.boardConfig) {
      this.renderBoard();
    }
  }

  async updateTaskKanbanColumn(taskId, columnId) {
    const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(taskId)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ kanban_column_id: columnId })
    });
    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to move task');
    }

    const updated = await response.json();
    const idx = this.tasks.findIndex((t) => t && t.id === taskId);
    if (idx >= 0) {
      const existing = this.tasks[idx];
      const nextContext = { ...(existing.context || {}), ...(updated.context || {}) };
      this.tasks.splice(idx, 1, { ...existing, ...updated, context: nextContext });
    }

    this.renderTasks();
    if (this.boardConfig) {
      this.renderBoard();
    }
  }

  async saveBoardCardEdits(card) {
    if (!card || !card.classList.contains('is-editing')) return;
    if (card.dataset.saving === '1') return;

    const taskId = card.dataset.taskId;
    if (!taskId) return;

    const current = this.getBoardCardEditValues(card);
    const original = card._editOriginal;
    if (!this.hasBoardCardChanges(current, original)) {
      this.exitBoardCardEdit(card);
      return;
    }

    if (!current.title) {
      if (window.Toast) window.Toast.error('Title is required');
      const titleEl = card.querySelector('.workspace-detail-board-card-edit-title');
      if (titleEl) titleEl.focus();
      return;
    }

    card.dataset.saving = '1';
    card.classList.add('is-saving');

    try {
      await this.updateTaskDetails(taskId, current);
      this.exitBoardCardEdit(card);
    } catch (error) {
      console.error('Failed to update task:', error);
      if (window.Toast) window.Toast.error('Failed to update task');
    } finally {
      card.dataset.saving = '';
      card.classList.remove('is-saving');
    }
  }

  wireBoardCardEditing() {
    if (!this.elements.boardColumns) return;

    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-card').forEach((card) => {
      card.addEventListener('click', (e) => {
        if (this.boardDidDrag) return;
        if (card.classList.contains('is-editing')) return;
        if (e.target.closest('.workspace-detail-board-card-edit') || e.target.closest('.workspace-detail-board-card-edit-btn')) return;
        if (e.target.closest('input') || e.target.closest('textarea') || e.target.closest('select') || e.target.closest('button')) return;
        const taskId = card.dataset.taskId;
        if (taskId) this.openTask(taskId);
      });

      const editBtn = card.querySelector('.workspace-detail-board-card-edit-btn');
      if (editBtn) {
        editBtn.addEventListener('click', (e) => {
          e.preventDefault();
          e.stopPropagation();
          this.enterBoardCardEdit(card);
        });
      }

      const editContainer = card.querySelector('.workspace-detail-board-card-edit');
      if (editContainer) {
        editContainer.addEventListener('click', (e) => e.stopPropagation());
      }

      const cancelBtn = card.querySelector('.workspace-detail-board-card-edit-cancel');
      if (cancelBtn) {
        cancelBtn.addEventListener('click', (e) => {
          e.preventDefault();
          e.stopPropagation();
          this.exitBoardCardEdit(card, { reset: true });
        });
      }

      const saveBtn = card.querySelector('.workspace-detail-board-card-edit-save');
      if (saveBtn) {
        saveBtn.addEventListener('click', (e) => {
          e.preventDefault();
          e.stopPropagation();
          this.saveBoardCardEdits(card);
        });
      }

      const editFields = card.querySelectorAll('.workspace-detail-board-card-edit input, .workspace-detail-board-card-edit textarea, .workspace-detail-board-card-edit select');
      editFields.forEach((field) => {
        field.addEventListener('click', (e) => e.stopPropagation());
        field.addEventListener('keydown', (e) => {
          if (e.key === 'Escape') {
            e.preventDefault();
            this.exitBoardCardEdit(card, { reset: true });
            return;
          }
          if (e.key === 'Enter') {
            if (field.tagName === 'TEXTAREA' && !(e.metaKey || e.ctrlKey)) return;
            e.preventDefault();
            this.saveBoardCardEdits(card);
          }
        });
      });
    });
  }

  wireBoardCardAdd() {
    if (!this.elements.boardColumns) return;

    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-add').forEach((container) => {
      const columnId = container.dataset.columnId || '';
      const button = container.querySelector('.workspace-detail-board-add-btn');
      const form = container.querySelector('.workspace-detail-board-add-form');
      const input = container.querySelector('.workspace-detail-board-add-input');
      const cancelBtn = container.querySelector('.workspace-detail-board-add-cancel');
      const submitBtn = container.querySelector('.workspace-detail-board-add-submit');

      if (!button || !form || !input || !cancelBtn || !submitBtn) return;

      const closeForm = () => {
        form.setAttribute('hidden', '');
        button.removeAttribute('hidden');
        input.value = '';
        if (container._addOutsideHandler) {
          document.removeEventListener('click', container._addOutsideHandler);
          container._addOutsideHandler = null;
        }
      };

      const openForm = () => {
        button.setAttribute('hidden', '');
        form.removeAttribute('hidden');
        input.focus();
        input.select();
        setTimeout(() => {
          const handler = (evt) => {
            if (container.contains(evt.target)) return;
            closeForm();
          };
          container._addOutsideHandler = handler;
          document.addEventListener('click', handler);
        }, 0);
      };

      const submitForm = async () => {
        const title = input.value.trim();
        if (!title) {
          input.focus();
          return;
        }
        submitBtn.disabled = true;
        try {
          await this.createTask(title, '', columnId);
          closeForm();
        } catch (error) {
          console.error('Failed to create task:', error);
          if (window.Toast) window.Toast.error('Failed to create task');
        } finally {
          submitBtn.disabled = false;
        }
      };

      button.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();
        openForm();
      });

      cancelBtn.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();
        closeForm();
      });

      submitBtn.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();
        submitForm();
      });

      input.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') {
          e.preventDefault();
          closeForm();
          return;
        }
        if (e.key === 'Enter') {
          e.preventDefault();
          submitForm();
        }
      });
    });
  }

  wireBoardColumnAdd() {
    if (!this.elements.boardColumns) return;

    const container = this.elements.boardColumns.querySelector('.workspace-detail-board-add-column');
    if (!container) return;

    const button = container.querySelector('.workspace-detail-board-add-column-btn');
    const form = container.querySelector('.workspace-detail-board-add-column-form');
    const input = container.querySelector('.workspace-detail-board-add-column-input');
    const cancelBtn = container.querySelector('.workspace-detail-board-add-column-cancel');
    const submitBtn = container.querySelector('.workspace-detail-board-add-column-submit');

    if (!button || !form || !input || !cancelBtn || !submitBtn) return;

    const closeForm = () => {
      form.setAttribute('hidden', '');
      button.removeAttribute('hidden');
      input.value = '';
      if (container._addOutsideHandler) {
        document.removeEventListener('click', container._addOutsideHandler);
        container._addOutsideHandler = null;
      }
    };

    const openForm = () => {
      button.setAttribute('hidden', '');
      form.removeAttribute('hidden');
      input.focus();
      input.select();
      setTimeout(() => {
        const handler = (evt) => {
          if (container.contains(evt.target)) return;
          closeForm();
        };
        container._addOutsideHandler = handler;
        document.addEventListener('click', handler);
      }, 0);
    };

    const submitForm = async () => {
      const name = input.value.trim();
      if (!name) {
        input.focus();
        return;
      }
      submitBtn.disabled = true;
      try {
        const columns = Array.isArray(this.boardConfig?.columns) ? this.boardConfig.columns.slice() : [];
        const id = this.makeColumnId(name, columns);
        const order = this.getNextColumnOrder(columns);
        const next = columns.concat({ id, name, order });
        this.boardConfig = await this.saveBoardConfig(next);
        this.renderBoard();
        if (window.Toast) window.Toast.success('Column added');
      } catch (error) {
        console.error('Failed to add column:', error);
        if (window.Toast) window.Toast.error('Failed to add column');
      } finally {
        submitBtn.disabled = false;
      }
    };

    button.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      openForm();
    });

    cancelBtn.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      closeForm();
    });

    submitBtn.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      submitForm();
    });

    input.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        closeForm();
        return;
      }
      if (e.key === 'Enter') {
        e.preventDefault();
        submitForm();
      }
    });
  }

  wireBoardColumnRename() {
    if (!this.elements.boardColumns) return;

    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-column-header').forEach((headerEl) => {
      const titleEl = headerEl.querySelector('.workspace-detail-board-column-title');
      const editBtn = headerEl.querySelector('.workspace-detail-board-column-edit-btn');
      const titleWrap = headerEl.querySelector('.workspace-detail-board-column-title-wrap');
      if (!titleEl || !editBtn) return;

      editBtn.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();

        const columnId = titleEl.dataset.columnId || titleEl.closest('.workspace-detail-board-column')?.dataset.columnId;
        if (!columnId || !this.boardConfig?.columns) return;

        const currentName = titleEl.textContent || '';

        const input = document.createElement('input');
        input.type = 'text';
        input.className = 'workspace-detail-board-column-rename-input';
        input.value = currentName;

        titleEl.style.display = 'none';
        editBtn.style.display = 'none';
        if (titleWrap && titleWrap.contains(editBtn)) {
          titleWrap.insertBefore(input, editBtn);
        } else {
          const countEl = headerEl.querySelector('.workspace-detail-board-column-count');
          if (countEl) {
            headerEl.insertBefore(input, countEl);
          } else {
            headerEl.appendChild(input);
          }
        }
        input.focus();
        input.select();

        const finishRename = async () => {
          const newName = input.value.trim();
          input.remove();
          titleEl.style.display = '';
          editBtn.style.display = '';

          if (!newName || newName === currentName) return;

          const columns = (this.boardConfig?.columns || []).map((col) => {
            if (col.id === columnId) {
              return { ...col, name: newName };
            }
            return col;
          });

          try {
            this.boardConfig = await this.saveBoardConfig(columns);
            titleEl.textContent = newName;
            if (window.Toast) window.Toast.success('Column renamed');
          } catch (error) {
            console.error('Failed to rename column:', error);
            titleEl.textContent = currentName;
            if (window.Toast) window.Toast.error('Failed to rename column');
          }
        };

        input.addEventListener('blur', finishRename);
        input.addEventListener('keydown', (evt) => {
          if (evt.key === 'Enter') {
            evt.preventDefault();
            input.blur();
          } else if (evt.key === 'Escape') {
            evt.preventDefault();
            input.value = currentName;
            input.blur();
          }
        });
      });
    });
  }

  wireBoardDragAndDrop() {
    if (!this.elements.boardColumns) return;

    let dragged = null;
    this.boardDidDrag = false;

    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-card').forEach((card) => {
      card.addEventListener('dragstart', (e) => {
        if (card.classList.contains('is-editing') || e.target.closest('.workspace-detail-board-card-edit')) {
          e.preventDefault();
          return;
        }
        dragged = card;
        this.boardDidDrag = true;
        card.classList.add('is-dragging');
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', card.dataset.taskId);
      });

      card.addEventListener('dragend', () => {
        card.classList.remove('is-dragging');
        dragged = null;
        setTimeout(() => {
          this.boardDidDrag = false;
        }, 0);
      });
    });

    this.elements.boardColumns.querySelectorAll('.workspace-detail-board-column-body').forEach((colEl) => {
      colEl.addEventListener('dragover', (e) => {
        e.preventDefault();
        colEl.closest('.workspace-detail-board-column')?.classList.add('is-drag-over');
        e.dataTransfer.dropEffect = 'move';
      });
      colEl.addEventListener('dragleave', () => {
        colEl.closest('.workspace-detail-board-column')?.classList.remove('is-drag-over');
      });
      colEl.addEventListener('drop', async (e) => {
        e.preventDefault();
        const columnId = colEl.closest('.workspace-detail-board-column')?.dataset.columnId || colEl.dataset.columnId;
        colEl.closest('.workspace-detail-board-column')?.classList.remove('is-drag-over');

        const taskId = e.dataTransfer.getData('text/plain') || dragged?.dataset.taskId;
        if (!taskId || !columnId) return;

        try {
          await this.updateTaskKanbanColumn(taskId, columnId);
        } catch (error) {
          console.error('Failed to update kanban column:', error);
          if (window.Toast) window.Toast.error('Failed to move task');
        }
      });
    });
  }

  /**
   * Setup a new board with default columns
   */
  async setupBoard() {
    const defaultColumns = [
      { id: 'backlog', name: 'Backlog' },
      { id: 'in-progress', name: 'In Progress' },
      { id: 'done', name: 'Done' }
    ];

    try {
      this.boardConfig = await this.saveBoardConfig(defaultColumns);
      await this.ensureAgentOptions();
      this.renderBoard();

      if (window.Toast) window.Toast.success('Board created');
    } catch (error) {
      console.error('Failed to setup board:', error);
      if (window.Toast) window.Toast.error('Failed to create board');
    }
  }

  /**
   * Load sessions for the workspace
   */
  async loadSessions() {
    if (this.elements.sessionsList) {
      this.elements.sessionsList.innerHTML = '<div class="workspace-detail-loading">Loading sessions...</div>';
    }

    try {
      const response = await fetch(`/api/sessions?folder_id=${encodeURIComponent(this.workspaceId)}`);
      if (!response.ok) throw new Error('Failed to load sessions');

      const data = await response.json();
      this.sessions = data.sessions || data || [];
      this.renderSessions();

      // Update session count
      if (this.elements.sessionCount) {
        this.elements.sessionCount.textContent = this.sessions.length;
      }
    } catch (error) {
      console.error('Failed to load sessions:', error);
      if (this.elements.sessionsList) {
        this.elements.sessionsList.innerHTML = '<div class="workspace-detail-empty">Failed to load sessions</div>';
      }
    }
  }

  /**
   * Render sessions list
   */
  renderSessions() {
    if (!this.elements.sessionsList) return;

    if (this.sessions.length === 0) {
      this.elements.sessionsList.innerHTML = '<div class="workspace-detail-empty">No chat sessions yet.</div>';
      return;
    }

    this.elements.sessionsList.innerHTML = this.sessions.map(session => `
      <div class="workspace-detail-item" data-session-id="${session.id}">
        <button type="button" class="workspace-detail-item-delete" onclick="event.stopPropagation(); window.workspaceDetail?.deleteSession('${session.id}')" title="Delete session" aria-label="Delete session ${this.escapeHtml(session.title || session.name || 'Untitled Session')}">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
          </svg>
        </button>
        <div class="workspace-detail-item-content"
             role="button"
             tabindex="0"
             aria-label="Open session ${this.escapeHtml(session.title || session.name || 'Untitled Session')}"
             onclick="window.workspaceDetail?.openSession('${session.id}')"
             onkeydown="if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); window.workspaceDetail?.openSession('${session.id}'); }">
          <div class="workspace-detail-item-title">${this.escapeHtml(session.title || session.name || 'Untitled Session')}</div>
          <div class="workspace-detail-item-meta">
            ${session.agent_name ? `${this.escapeHtml(session.agent_name)} · ` : ''}
            ${formatDate(session.updated_at || session.created_at)}
          </div>
        </div>
      </div>
    `).join('');
  }

  /**
   * Load files for the workspace
   */
  async loadFiles() {
    if (this.elements.filesList) {
      this.elements.filesList.innerHTML = '<div class="workspace-detail-loading">Loading files...</div>';
    }

    try {
      const response = await fetch(`/api/studios/${encodeURIComponent(this.workspaceId)}`);
      if (!response.ok) {
        this.files = [];
        this.renderFiles();
        return;
      }

      const workspace = await response.json();
      // Filter attachments to only include files (not text content)
      this.files = (workspace.attachments || []).filter(a => a.file_meta || a.type === 'image' || a.type === 'other');
      this.renderFiles();
    } catch (error) {
      console.error('Failed to load files:', error);
      this.files = [];
      this.renderFiles();
    }
  }

  /**
   * Render files list
   */
  renderFiles() {
    if (!this.elements.filesList) return;

    if (!this.files || this.files.length === 0) {
      this.elements.filesList.innerHTML = '<div class="workspace-detail-empty">No files yet.</div>';
      return;
    }

    this.elements.filesList.innerHTML = this.files.map(file => {
      const title = file.title || (file.file_meta && file.file_meta.name) || 'Untitled File';
      const size = file.file_meta ? file.file_meta.size : null;
      return `
        <div class="workspace-detail-item" data-file-id="${file.id}">
          <div class="workspace-detail-item-title">${this.escapeHtml(title)}</div>
          <div class="workspace-detail-item-meta">
            ${size ? this.formatFileSize(size) + ' · ' : ''}
            ${formatDate(file.created_at)}
          </div>
        </div>
      `;
    }).join('');
  }

  /**
   * Load notes for the workspace
   */
  async loadNotes() {
    this.updateCopyNotesButtonState(true);
    if (this.elements.notesList) {
      this.elements.notesList.innerHTML = '<div class="workspace-detail-loading">Loading notes...</div>';
    }

    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/notes`);
      if (!response.ok) {
        // Notes endpoint might return workspace notes differently
        this.notes = [];
        this.renderNotes();
        return;
      }

      const data = await response.json();
      this.notes = data.notes || (Array.isArray(data) ? data : [data]).filter(Boolean);
      this.renderNotes();
    } catch (error) {
      console.error('Failed to load notes:', error);
      this.notes = [];
      this.renderNotes();
    }
  }

  /**
   * Render notes list
   */
  renderNotes() {
    if (!this.elements.notesList) return;

    if (this.notes.length === 0) {
      this.elements.notesList.innerHTML = '<div class="workspace-detail-empty">No notes yet.</div>';
      this.updateCopyNotesButtonState(false);
      return;
    }

    this.elements.notesList.innerHTML = this.notes.map(note => {
      const title = note.name || note.title || 'Untitled Note';
      const preview = note.preview || note.content || '';
      return `
      <div class="workspace-detail-item" data-note-id="${note.id}">
        <button type="button" class="workspace-detail-item-delete" onclick="event.stopPropagation(); window.workspaceDetail?.deleteNote('${note.id}')" title="Delete note" aria-label="Delete note ${this.escapeHtml(title)}">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
          </svg>
        </button>
        <div class="workspace-detail-item-content"
             role="button"
             tabindex="0"
             aria-label="Open note ${this.escapeHtml(title)}"
             onclick="window.workspaceDetail?.editNote('${note.id}')"
             onkeydown="if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); window.workspaceDetail?.editNote('${note.id}'); }">
          <div class="workspace-detail-item-title">${this.escapeHtml(title)}</div>
          <div class="workspace-detail-item-meta">
            ${preview ? this.escapeHtml(preview.substring(0, 50)) + (preview.length > 50 ? '...' : '') : 'Empty note'}
          </div>
        </div>
      </div>
    `;
    }).join('');
    this.updateCopyNotesButtonState(false);
  }

  /**
   * Load directories for the workspace
   */
  async loadDirectories() {
    if (this.elements.directoriesList) {
      this.elements.directoriesList.innerHTML = '<div class="workspace-detail-loading">Loading directories...</div>';
    }

    try {
      // Directories are stored as attachments with type 'directory'
      const response = await fetch(`/api/studios/${encodeURIComponent(this.workspaceId)}`);
      if (!response.ok) {
        this.directories = [];
        this.renderDirectories();
        return;
      }

      const workspace = await response.json();
      this.directories = (workspace.attachments || []).filter(a => a.type === 'directory');
      this.renderDirectories();
    } catch (error) {
      console.error('Failed to load directories:', error);
      this.directories = [];
      this.renderDirectories();
    }
  }

  /**
   * Render directories list
   */
  renderDirectories() {
    if (!this.elements.directoriesList) return;

    if (!this.directories || this.directories.length === 0) {
      this.elements.directoriesList.innerHTML = '<div class="workspace-detail-empty">No directories yet.</div>';
      return;
    }

    this.elements.directoriesList.innerHTML = this.directories.map(dir => {
      const name = dir.title || dir.name || dir.path || 'Unnamed Directory';
      const path = dir.path || '';
      return `
        <div class="workspace-detail-item" data-directory-id="${dir.id}">
          <div class="workspace-detail-item-title">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-2" style="color: var(--text-secondary);">
              <path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>
            </svg>
            ${this.escapeHtml(name)}
          </div>
          <div class="workspace-detail-item-meta">${this.escapeHtml(path)}</div>
        </div>
      `;
    }).join('');
  }

  /**
   * Show add directory modal - launches the folder picker
   */
  async showAddDirectoryModal() {
    const button = this.elements.addDirectoryBtn;
    if (!button) return;

    // Show loading state on button
    const originalHTML = button.innerHTML;
    button.disabled = true;
    button.innerHTML = '<span class="spinner-border spinner-border-sm"></span>';

    try {
      const response = await fetch('/api/launch-folder-picker', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: this.workspaceId
        })
      });

      const result = await response.json();

      if (result.success) {
        if (window.Toast) window.Toast.info('Folder picker opened. Select a folder to add it.');
        // The folder picker will add the directory and trigger a reload via realtime events
        // Schedule a reload in case realtime doesn't trigger
        setTimeout(() => this.loadDirectories(), 2000);
      } else {
        throw new Error(result.error || 'Failed to open folder picker');
      }
    } catch (error) {
      console.error('Failed to launch folder picker:', error);
      if (window.Toast) window.Toast.error('Failed to open folder picker');
    } finally {
      button.disabled = false;
      button.innerHTML = originalHTML;
    }
  }

  /**
   * Add a directory reference
   */
  async addDirectory(path) {
    try {
      const response = await fetch(`/api/studios/${encodeURIComponent(this.workspaceId)}/attachments`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          type: 'directory',
          path: path,
          title: path.split('/').pop() || path
        })
      });

      if (!response.ok) throw new Error('Failed to add directory');

      if (window.Toast) window.Toast.success('Directory added');
      await this.loadDirectories();
    } catch (error) {
      console.error('Failed to add directory:', error);
      if (window.Toast) window.Toast.error('Failed to add directory');
    }
  }

  /**
   * Load scheduled tasks for the workspace
   * Schedules are tasks that have a schedule field set
   */
  async loadSchedules() {
    if (this.elements.schedulesList) {
      this.elements.schedulesList.innerHTML = '<div class="workspace-detail-loading">Loading schedules...</div>';
    }

    try {
      // Schedules are stored as tasks with schedule field
      const response = await fetch(`/api/orchestration/tasks?studio_id=${encodeURIComponent(this.workspaceId)}`);
      if (!response.ok) {
        this.schedules = [];
        this.renderSchedules();
        return;
      }

      const data = await response.json();
      const allTasks = data.tasks || [];

      // Filter tasks that have schedules
      this.schedules = allTasks
        .filter(task => task.schedule != null)
        .map(task => ({
          id: task.id,
          name: task.schedule_name || task.description,
          description: task.description,
          enabled: task.schedule_enabled,
          schedule: task.schedule,
          next_run: task.next_run,
          last_run: task.last_run,
          schedule_type: task.schedule ? 'cron' : 'interval'
        }));

      this.renderSchedules();
    } catch (error) {
      console.error('Failed to load schedules:', error);
      this.schedules = [];
      this.renderSchedules();
    }
  }

  /**
   * Render schedules list
   */
  renderSchedules() {
    if (!this.elements.schedulesList) return;

    if (!this.schedules || this.schedules.length === 0) {
      this.elements.schedulesList.innerHTML = '<div class="workspace-detail-empty">No scheduled tasks yet.</div>';
      return;
    }

    this.elements.schedulesList.innerHTML = this.schedules.map(schedule => {
      const name = schedule.name || schedule.task_description || 'Unnamed Schedule';
      const status = schedule.enabled ? 'Active' : 'Paused';
      const statusClass = schedule.enabled ? 'completed' : 'pending';
      return `
        <div class="workspace-detail-item"
             data-schedule-id="${schedule.id}"
             role="button"
             tabindex="0"
             aria-label="Open schedule ${this.escapeHtml(name)}"
             onclick="window.workspaceDetail?.openSchedule('${schedule.id}')"
             onkeydown="if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); window.workspaceDetail?.openSchedule('${schedule.id}'); }">
          <div class="d-flex justify-content-between align-items-start">
            <div class="workspace-detail-item-title">${this.escapeHtml(name)}</div>
            <span class="workspace-detail-task-status ${statusClass}">${status}</span>
          </div>
          <div class="workspace-detail-item-meta">
            ${schedule.schedule_type || 'interval'} · Next: ${schedule.next_run ? formatDate(schedule.next_run) : 'N/A'}
          </div>
        </div>
      `;
    }).join('');
  }

  /**
   * Show schedules modal
   */
  showSchedulesModal() {
    // Use the session manager's scheduled tasks panel if available
    if (window.sessionManager && typeof window.sessionManager.openScheduledTasksPanel === 'function') {
      window.sessionManager.openScheduledTasksPanel(this.workspaceId);
    } else {
      // Just reload the schedules list
      this.loadSchedules();
    }
  }

  /**
   * Open a schedule for viewing/editing
   */
  openSchedule(scheduleId) {
    if (window.sessionManager && typeof window.sessionManager.showScheduledTaskDetails === 'function') {
      const schedule = this.schedules.find(s => s.id === scheduleId);
      if (schedule) {
        window.sessionManager.showScheduledTaskDetails(schedule);
      }
    }
  }

  /**
   * Setup real-time updates
   */
  setupRealtime() {
    // Use existing realtime system if available
    if (window.workspaceRealtime && typeof window.workspaceRealtime.subscribeToWorkspace === 'function') {
      window.workspaceRealtime.subscribeToWorkspace(this.workspaceId, (event) => {
        this.handleRealtimeEvent(event);
      });
    }
  }

  /**
   * Handle real-time events
   */
  handleRealtimeEvent(event) {
    switch (event.type) {
      case 'task_created':
      case 'task_updated':
      case 'task_deleted':
      case 'task.created':
      case 'task.assigned':
      case 'task.started':
      case 'task.completed':
      case 'task.failed':
      case 'task.deleted':
        this.loadTasks();
        break;
      case 'task.blocked': {
        this.loadTasks();
        const payload = event?.data?.data || event?.data || {};
        const taskId = payload.task_id || event?.data?.task_id;
        if (taskId) {
          this.openTaskAssistModal(taskId, payload);
        }
        break;
      }
      case 'task.resumed':
        this.loadTasks();
        if (this.elements.taskAssistModal && window.bootstrap) {
          const modal = bootstrap.Modal.getInstance(this.elements.taskAssistModal);
          modal?.hide();
        }
        break;
      case 'session_created':
      case 'session_updated':
      case 'session_deleted':
        this.loadSessions();
        break;
      case 'file_created':
      case 'file_deleted':
        this.loadFiles();
        break;
      case 'note_updated':
      case 'note_created':
      case 'note_deleted':
        this.loadNotes();
        break;
      case 'directory_added':
      case 'directory_removed':
      case 'attachment_created':
      case 'attachment_deleted':
        this.loadDirectories();
        this.loadFiles();
        break;
      case 'schedule_created':
      case 'schedule_updated':
      case 'schedule_deleted':
        this.loadSchedules();
        break;
    }
  }

  /**
   * Handle quick input submission
   */
  async handleQuickInput() {
    const input = this.elements.smartInput?.value?.trim();
    if (!input) return;

    // Check for commands
    if (input.startsWith('/task ')) {
      const taskName = input.substring(6).trim();
      await this.createTask(taskName);
    } else if (input.startsWith('/chat ') || input.startsWith('/c ')) {
      const message = input.replace(/^\/(chat|c)\s+/, '').trim();
      await this.createSessionWithMessage(message);
    } else if (input.startsWith('/note ')) {
      const noteContent = input.substring(6).trim();
      await this.createNote('Quick Note', noteContent);
    } else {
      // Default: create a task
      await this.createTask(input);
    }

    if (this.elements.smartInput) {
      this.elements.smartInput.value = '';
    }
  }

  /**
   * Create a new task
   */
  async createTask(name, description = '', columnId = '') {
    try {
      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          studio_id: this.workspaceId,
          description: name, // Task API uses description as the main field
          details: description,
          status: 'pending'
        })
      });

      if (!response.ok) throw new Error('Failed to create task');

      const data = await response.json();
      const createdTask = data.task || data;

      if (columnId && createdTask?.id) {
        await this.updateTaskKanbanColumn(createdTask.id, columnId);
        await this.loadTasks();
      } else {
        await this.loadTasks();
      }

      if (window.Toast) window.Toast.success('Task created');
    } catch (error) {
      console.error('Failed to create task:', error);
      if (window.Toast) window.Toast.error('Failed to create task');
    }
  }

  /**
   * Show add task modal
   */
  showAddTaskModal() {
    // Use existing task modal controller
    if (window.taskModalController && typeof window.taskModalController.openForCreate === 'function') {
      window.taskModalController.openForCreate(this.workspaceId, '', () => this.loadTasks());
    } else {
      // Fallback to prompt
      const name = prompt('Enter task name:');
      if (name) this.createTask(name);
    }
  }

  /**
   * Open a task for editing
   */
  openTask(taskId) {
    // Use existing task modal controller
    if (window.taskModalController && typeof window.taskModalController.openForEdit === 'function') {
      const task = this.tasks.find(t => t.id === taskId);
      if (task) {
        window.taskModalController.openForEdit(task, () => this.loadTasks());
      }
    }
  }

  /**
   * Delete a task
   */
  async deleteTask(taskId) {
    if (!confirm('Are you sure you want to delete this task?')) return;

    try {
      const response = await fetch(`/api/orchestration/tasks/${encodeURIComponent(taskId)}`, {
        method: 'DELETE'
      });

      if (!response.ok) throw new Error('Failed to delete task');

      if (window.Toast) window.Toast.success('Task deleted');
      await this.loadTasks();
    } catch (error) {
      console.error('Failed to delete task:', error);
      if (window.Toast) window.Toast.error('Failed to delete task');
    }
  }

  /**
   * Delete a session
   */
  async deleteSession(sessionId) {
    if (!confirm('Are you sure you want to delete this session?')) return;

    try {
      const response = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}`, {
        method: 'DELETE'
      });

      if (!response.ok) throw new Error('Failed to delete session');

      if (window.Toast) window.Toast.success('Session deleted');
      await this.loadSessions();
    } catch (error) {
      console.error('Failed to delete session:', error);
      if (window.Toast) window.Toast.error('Failed to delete session');
    }
  }

  /**
   * Delete a note
   */
  async deleteNote(noteId) {
    if (!confirm('Are you sure you want to delete this note?')) return;

    try {
      const response = await fetch(`/api/notes/${encodeURIComponent(noteId)}`, {
        method: 'DELETE'
      });

      if (!response.ok) throw new Error('Failed to delete note');

      if (window.Toast) window.Toast.success('Note deleted');
      await this.loadNotes();
    } catch (error) {
      console.error('Failed to delete note:', error);
      if (window.Toast) window.Toast.error('Failed to delete note');
    }
  }

  /**
   * Create a new session using the existing chat modal
   */
  createNewSession() {
    // Use the existing create chat modal from sessionManager
    if (window.sessionManager && typeof window.sessionManager.showCreateChatModalForWorkspace === 'function') {
      window.sessionManager.showCreateChatModalForWorkspace(this.workspaceId);
    } else if (window.sessionManager && typeof window.sessionManager.showCreateChatModal === 'function') {
      window.sessionManager.showCreateChatModal();
    } else {
      // Fallback to simple API call
      this.createSimpleSession();
    }
  }

  /**
   * Fallback simple session creation
   */
  async createSimpleSession() {
    try {
      const response = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          folder_id: this.workspaceId,
          title: 'New Chat'
        })
      });

      if (!response.ok) throw new Error('Failed to create session');

      const session = await response.json();
      if (window.Toast) window.Toast.success('Chat session created');
      await this.loadSessions();

      // Open the session
      this.openSession(session.id);
    } catch (error) {
      console.error('Failed to create session:', error);
      if (window.Toast) window.Toast.error('Failed to create session');
    }
  }

  /**
   * Create session with initial message
   */
  async createSessionWithMessage(message) {
    try {
      const response = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          folder_id: this.workspaceId,
          title: message.substring(0, 50)
        })
      });

      if (!response.ok) throw new Error('Failed to create session');

      const session = await response.json();
      await this.loadSessions();
      this.openSession(session.id);
      if (message && typeof window.sendMessageToChat === 'function') {
        setTimeout(() => window.sendMessageToChat(message), 150);
      }
    } catch (error) {
      console.error('Failed to create session:', error);
      if (window.Toast) window.Toast.error('Failed to create chat');
    }
  }

  /**
   * Open a session
   */
  openSession(sessionId) {
    // Open chat panel if available
    if (window.chatPanel && typeof window.chatPanel.open === 'function') {
      window.chatPanel.open();
    }
    if (window.sessionManager && typeof window.sessionManager.switchToSession === 'function') {
      window.sessionManager.switchToSession(sessionId);
    }
  }

  /**
   * Show file upload modal using hub's file modal
   */
  showFileModal() {
    // Use the hub file modal if available
    const hubFileModal = document.getElementById('hubAddFileModal');
    if (hubFileModal && typeof bootstrap !== 'undefined') {
      // Set up the workspace ID for the file upload
      window.currentWorkspaceId = this.workspaceId;
      const modal = new bootstrap.Modal(hubFileModal);
      modal.show();
    } else {
      // Fallback to file input click
      const fileInput = document.createElement('input');
      fileInput.type = 'file';
      fileInput.multiple = true;
      fileInput.onchange = () => this.uploadFiles(fileInput.files);
      fileInput.click();
    }
  }

  /**
   * Upload files
   */
  async uploadFiles(files) {
    if (!files || files.length === 0) return;

    for (const file of files) {
      const formData = new FormData();
      formData.append('file', file);
      formData.append('workspace_id', this.workspaceId);

      try {
        const response = await fetch(`/api/studios/${encodeURIComponent(this.workspaceId)}/files`, {
          method: 'POST',
          body: formData
        });

        if (!response.ok) throw new Error('Failed to upload file');
      } catch (error) {
        console.error('Failed to upload file:', error);
        if (window.Toast) window.Toast.error(`Failed to upload ${file.name}`);
        return;
      }
    }

    if (window.Toast) window.Toast.success('File(s) uploaded');
    await this.loadFiles();
  }

  /**
   * Show note modal using sessionManager
   */
  showNoteModal(note = null) {
    if (note) {
      // Edit existing note
      if (window.sessionManager && typeof window.sessionManager.openNoteEditorModal === 'function') {
        window.sessionManager.openNoteEditorModal(note);
      }
    } else {
      // Create new note
      if (window.sessionManager && typeof window.sessionManager.openNoteCreateModal === 'function') {
        window.sessionManager.openNoteCreateModal(this.workspaceId);
      }
    }
  }

  /**
   * Edit a note
   */
  editNote(noteId) {
    const note = this.notes.find(n => n.id === noteId);
    if (note) {
      this.showNoteModal(note);
    }
  }

  /**
   * Create a quick note from content (for quick input)
   */
  async createNote(title, content, noteId = null) {
    try {
      const url = noteId
        ? `/api/workspaces/${encodeURIComponent(this.workspaceId)}/notes/${encodeURIComponent(noteId)}`
        : `/api/workspaces/${encodeURIComponent(this.workspaceId)}/notes`;

      const response = await fetch(url, {
        method: noteId ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: title, content })
      });

      if (!response.ok) throw new Error('Failed to save note');

      if (window.Toast) window.Toast.success(noteId ? 'Note updated' : 'Note created');
      await this.loadNotes();
    } catch (error) {
      console.error('Failed to save note:', error);
      if (window.Toast) window.Toast.error('Failed to save note');
    }
  }

  updateCopyNotesButtonState(isBusy = false) {
    const button = this.elements.copyNotesBtn;
    if (!button) return;

    const hasNotes = Array.isArray(this.notes) && this.notes.length > 0;
    button.disabled = Boolean(isBusy) || !hasNotes;
    if (isBusy) {
      button.title = 'Copying notes...';
      return;
    }
    button.title = hasNotes ? 'Copy all note contents' : 'No notes to copy';
  }

  async writeClipboardText(text) {
    if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
      await navigator.clipboard.writeText(text);
      return;
    }

    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.setAttribute('readonly', '');
    textarea.style.position = 'fixed';
    textarea.style.top = '-9999px';
    textarea.style.left = '-9999px';
    document.body.appendChild(textarea);
    textarea.focus();
    textarea.select();

    const copied = document.execCommand('copy');
    document.body.removeChild(textarea);
    if (!copied) throw new Error('Copy command failed');
  }

  async copyAllNotesToClipboard() {
    if (!this.notes || this.notes.length === 0) {
      this.updateCopyNotesButtonState(false);
      if (typeof window.notifyToast === 'function') {
        window.notifyToast('No notes to copy', 'error');
      } else if (window.Toast) {
        window.Toast.error('No notes to copy');
      }
      return;
    }

    this.updateCopyNotesButtonState(true);
    try {
      const sections = await Promise.all(this.notes.map(async (note, index) => {
        const noteId = String(note?.id || '').trim();
        const title = String(note?.name || note?.title || `Note ${index + 1}`).trim() || `Note ${index + 1}`;
        let content = '';

        if (noteId) {
          try {
            const response = await fetch(`/api/notes/${encodeURIComponent(noteId)}`);
            if (response.ok) {
              const detail = await response.json();
              content = String(detail?.content || detail?.preview || '').trim();
            }
          } catch (error) {
            console.error('Failed to load note content for copy:', error);
          }
        }

        if (!content) {
          content = String(note?.content || note?.preview || '').trim();
        }

        return `# ${title}\n${content || '(empty note)'}`;
      }));

      await this.writeClipboardText(sections.join('\n\n---\n\n'));
      if (typeof window.notifyToast === 'function') {
        window.notifyToast(`Copied ${this.notes.length} note${this.notes.length === 1 ? '' : 's'} to clipboard`, 'success');
      } else if (window.Toast) {
        window.Toast.success(`Copied ${this.notes.length} note${this.notes.length === 1 ? '' : 's'} to clipboard`);
      }
    } catch (error) {
      console.error('Failed to copy notes:', error);
      if (typeof window.notifyToast === 'function') {
        window.notifyToast('Failed to copy notes', 'error');
      } else if (window.Toast) {
        window.Toast.error('Failed to copy notes');
      }
    } finally {
      this.updateCopyNotesButtonState(false);
    }
  }

  async copyCurrentTaskResult() {
    if (!this.currentTaskResultText) {
      if (window.Toast) window.Toast.info('No result to copy.');
      return;
    }

    try {
      await this.writeClipboardText(this.currentTaskResultText);
      if (window.Toast) window.Toast.success('Result copied to clipboard');
    } catch (error) {
      console.error('Failed to copy task result:', error);
      if (window.Toast) window.Toast.error('Failed to copy result');
    }
  }

  /**
   * Escape HTML to prevent XSS
   */
  escapeHtml(text) {
    if (!text) return '';
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }

  /**
   * Format file size
   */
  formatFileSize(bytes) {
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
  }
}

// Make available globally for onclick handlers
window.workspaceDetail = null;

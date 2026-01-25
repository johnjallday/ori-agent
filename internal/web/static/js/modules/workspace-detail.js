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

          const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/files`, {
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
      boardSetupBtn: document.getElementById('workspace-detail-board-setup')
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

    // Directory buttons
    this.elements.addDirectoryBtn?.addEventListener('click', () => this.showAddDirectoryModal());

    // Schedule buttons
    this.elements.viewSchedulesBtn?.addEventListener('click', () => this.showSchedulesModal());
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
      this.elements.workspaceDescription.textContent = this.workspace.description || 'No description';
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
             onclick="window.location.href='/workspaces/${child.id}'"
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

    this.elements.tasksList.innerHTML = topLevelTasks.map(task => `
      <div class="workspace-detail-item" data-task-id="${task.id}" onclick="window.workspaceDetail?.openTask('${task.id}')">
        <div class="d-flex justify-content-between align-items-start">
          <div class="workspace-detail-item-title">${this.escapeHtml(task.description || task.name || 'Untitled Task')}</div>
          <span class="workspace-detail-task-status ${getStatusClass(task.status)}">${getDisplayStatus(task.status)}</span>
        </div>
        <div class="workspace-detail-item-meta">
          ${task.to && task.to !== 'unassigned' ? `<span>Assigned to: ${this.escapeHtml(task.to)}</span> · ` : ''}
          ${formatDate(task.created_at)}
        </div>
      </div>
    `).join('');
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
      this.renderBoard();
    } catch (error) {
      console.error('Failed to load board:', error);
      this.boardConfig = null;
      this.renderBoard();
    }
  }

  /**
   * Render the kanban board
   */
  renderBoard() {
    if (!this.elements.boardColumns || !this.elements.boardEmpty) return;

    const columns = this.boardConfig?.columns || [];

    if (columns.length === 0) {
      this.elements.boardColumns.innerHTML = '';
      this.elements.boardEmpty.style.display = '';
      return;
    }

    this.elements.boardEmpty.style.display = 'none';

    // Group tasks by column
    const tasksByColumn = this.groupTasksByColumn(columns);

    this.elements.boardColumns.innerHTML = columns.map(col => {
      const columnTasks = tasksByColumn[col.id] || [];
      return `
        <div class="workspace-detail-board-column" data-column-id="${col.id}">
          <div class="workspace-detail-board-column-header">
            <span class="workspace-detail-board-column-title">${this.escapeHtml(col.name)}</span>
            <span class="workspace-detail-board-column-count">${columnTasks.length}</span>
          </div>
          <div class="workspace-detail-board-column-body">
            ${columnTasks.map(task => this.renderBoardCard(task)).join('')}
          </div>
        </div>
      `;
    }).join('');
  }

  /**
   * Render a single board card
   */
  renderBoardCard(task) {
    return `
      <div class="workspace-detail-board-card" data-task-id="${task.id}" onclick="window.workspaceDetail?.openTask('${task.id}')">
        <div class="workspace-detail-board-card-title">${this.escapeHtml(task.description || task.name || 'Untitled')}</div>
        <div class="workspace-detail-board-card-meta">
          ${task.to && task.to !== 'unassigned' ? this.escapeHtml(task.to) : ''}
          ${task.to && task.to !== 'unassigned' ? ' · ' : ''}
          ${getDisplayStatus(task.status)}
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

  /**
   * Setup a new board with default columns
   */
  async setupBoard() {
    const defaultColumns = [
      { id: 'todo', name: 'To Do' },
      { id: 'in-progress', name: 'In Progress' },
      { id: 'done', name: 'Done' }
    ];

    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/board`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ columns: defaultColumns })
      });

      if (!response.ok) throw new Error('Failed to setup board');

      const data = await response.json();
      this.boardConfig = data.board || { columns: defaultColumns };
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
      <div class="workspace-detail-item" data-session-id="${session.id}" onclick="window.workspaceDetail?.openSession('${session.id}')">
        <div class="workspace-detail-item-title">${this.escapeHtml(session.title || session.name || 'Untitled Session')}</div>
        <div class="workspace-detail-item-meta">
          ${session.agent_name ? `${this.escapeHtml(session.agent_name)} · ` : ''}
          ${formatDate(session.updated_at || session.created_at)}
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
      return;
    }

    this.elements.notesList.innerHTML = this.notes.map(note => `
      <div class="workspace-detail-item" data-note-id="${note.id}" onclick="window.workspaceDetail?.editNote('${note.id}')">
        <div class="workspace-detail-item-title">${this.escapeHtml(note.title || 'Untitled Note')}</div>
        <div class="workspace-detail-item-meta">
          ${note.content ? this.escapeHtml(note.content.substring(0, 50)) + (note.content.length > 50 ? '...' : '') : 'Empty note'}
        </div>
      </div>
    `).join('');
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
        <div class="workspace-detail-item" data-schedule-id="${schedule.id}" onclick="window.workspaceDetail?.openSchedule('${schedule.id}')">
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
        this.loadTasks();
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
  async createTask(name, description = '') {
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

      if (window.Toast) window.Toast.success('Task created');
      await this.loadTasks();
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
        const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/files`, {
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

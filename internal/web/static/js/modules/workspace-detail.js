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

    // DOM elements
    this.elements = {};
  }

  /**
   * Initialize the workspace detail page
   */
  async init() {
    this.cacheElements();
    this.bindEvents();
    await this.loadWorkspace();
    await Promise.all([
      this.loadTasks(),
      this.loadSessions(),
      this.loadFiles(),
      this.loadNotes()
    ]);
    this.setupRealtime();
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

      // Buttons
      addTaskBtn: document.getElementById('workspace-detail-add-task'),
      refreshTasksBtn: document.getElementById('workspace-detail-refresh-tasks'),
      newSessionBtn: document.getElementById('workspace-detail-new-session'),
      addFileBtn: document.getElementById('workspace-detail-add-file'),
      addNoteBtn: document.getElementById('workspace-detail-add-note'),

      // Modals
      fileModal: document.getElementById('workspace-detail-file-modal'),
      noteModal: document.getElementById('workspace-detail-note-modal'),
      fileInput: document.getElementById('workspace-detail-file-input'),
      fileUploadBtn: document.getElementById('workspace-detail-file-upload-btn'),
      noteTitle: document.getElementById('workspace-detail-note-title'),
      noteContent: document.getElementById('workspace-detail-note-content'),
      noteSaveBtn: document.getElementById('workspace-detail-note-save-btn')
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

    // Session buttons
    this.elements.newSessionBtn?.addEventListener('click', () => this.createNewSession());

    // File buttons
    this.elements.addFileBtn?.addEventListener('click', () => this.showFileModal());
    this.elements.fileUploadBtn?.addEventListener('click', () => this.uploadFile());

    // Note buttons
    this.elements.addNoteBtn?.addEventListener('click', () => this.showNoteModal());
    this.elements.noteSaveBtn?.addEventListener('click', () => this.saveNote());
  }

  /**
   * Load workspace data
   */
  async loadWorkspace() {
    try {
      const response = await fetch(`/api/orchestration/workspace?id=${encodeURIComponent(this.workspaceId)}`);
      if (!response.ok) throw new Error('Failed to load workspace');

      this.workspace = await response.json();
      this.renderWorkspaceInfo();
    } catch (error) {
      console.error('Failed to load workspace:', error);
      if (window.Toast) window.Toast.error('Failed to load workspace');
    }
  }

  /**
   * Render workspace information in header
   */
  renderWorkspaceInfo() {
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
  }

  /**
   * Load tasks for the workspace
   */
  async loadTasks() {
    if (this.elements.tasksList) {
      this.elements.tasksList.innerHTML = '<div class="workspace-detail-loading">Loading tasks...</div>';
    }

    try {
      const response = await fetch(`/api/orchestration/tasks?workspace_id=${encodeURIComponent(this.workspaceId)}`);
      if (!response.ok) throw new Error('Failed to load tasks');

      const data = await response.json();
      this.tasks = data.tasks || [];
      this.renderTasks();

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
          <div class="workspace-detail-item-title">${this.escapeHtml(task.name || 'Untitled Task')}</div>
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
   * Load sessions for the workspace
   */
  async loadSessions() {
    if (this.elements.sessionsList) {
      this.elements.sessionsList.innerHTML = '<div class="workspace-detail-loading">Loading sessions...</div>';
    }

    try {
      const response = await fetch(`/api/sessions?workspace_id=${encodeURIComponent(this.workspaceId)}`);
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
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/files`);
      if (!response.ok) {
        // Files endpoint might not exist yet
        this.files = [];
        this.renderFiles();
        return;
      }

      const data = await response.json();
      this.files = data.files || data || [];
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

    if (this.files.length === 0) {
      this.elements.filesList.innerHTML = '<div class="workspace-detail-empty">No files yet.</div>';
      return;
    }

    this.elements.filesList.innerHTML = this.files.map(file => `
      <div class="workspace-detail-item" data-file-id="${file.id}">
        <div class="workspace-detail-item-title">${this.escapeHtml(file.name || file.filename || 'Untitled File')}</div>
        <div class="workspace-detail-item-meta">
          ${file.size ? this.formatFileSize(file.size) + ' · ' : ''}
          ${formatDate(file.created_at)}
        </div>
      </div>
    `).join('');
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
        this.loadNotes();
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
          workspace_id: this.workspaceId,
          name: name,
          description: description,
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
    // Use existing task modal if available
    if (window.taskModal && typeof window.taskModal.show === 'function') {
      window.taskModal.show({
        workspaceId: this.workspaceId,
        onSave: () => this.loadTasks()
      });
    } else {
      // Fallback to prompt
      const name = prompt('Enter task name:');
      if (name) this.createTask(name);
    }
  }

  /**
   * Open a task
   */
  openTask(taskId) {
    // Use existing task modal if available
    if (window.taskModal && typeof window.taskModal.showEdit === 'function') {
      const task = this.tasks.find(t => t.id === taskId);
      if (task) {
        window.taskModal.showEdit(task, () => this.loadTasks());
      }
    }
  }

  /**
   * Create a new session
   */
  async createNewSession() {
    try {
      const response = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: this.workspaceId,
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
          workspace_id: this.workspaceId,
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
   * Show file upload modal
   */
  showFileModal() {
    if (this.elements.fileModal) {
      const modal = new bootstrap.Modal(this.elements.fileModal);
      modal.show();
    }
  }

  /**
   * Upload a file
   */
  async uploadFile() {
    const fileInput = this.elements.fileInput;
    if (!fileInput?.files?.length) {
      if (window.Toast) window.Toast.error('Please select a file');
      return;
    }

    const file = fileInput.files[0];
    const formData = new FormData();
    formData.append('file', file);
    formData.append('workspace_id', this.workspaceId);

    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/files`, {
        method: 'POST',
        body: formData
      });

      if (!response.ok) throw new Error('Failed to upload file');

      if (window.Toast) window.Toast.success('File uploaded');
      await this.loadFiles();

      // Close modal
      const modal = bootstrap.Modal.getInstance(this.elements.fileModal);
      modal?.hide();
      fileInput.value = '';
    } catch (error) {
      console.error('Failed to upload file:', error);
      if (window.Toast) window.Toast.error('Failed to upload file');
    }
  }

  /**
   * Show note modal
   */
  showNoteModal(note = null) {
    if (this.elements.noteTitle) {
      this.elements.noteTitle.value = note?.title || '';
    }
    if (this.elements.noteContent) {
      this.elements.noteContent.value = note?.content || '';
    }

    this.editingNoteId = note?.id || null;

    const modalTitle = document.getElementById('workspace-detail-note-modal-title');
    if (modalTitle) {
      modalTitle.textContent = note ? 'Edit Note' : 'Add Note';
    }

    if (this.elements.noteModal) {
      const modal = new bootstrap.Modal(this.elements.noteModal);
      modal.show();
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
   * Save a note
   */
  async saveNote() {
    const title = this.elements.noteTitle?.value?.trim() || 'Untitled Note';
    const content = this.elements.noteContent?.value?.trim() || '';

    await this.createNote(title, content, this.editingNoteId);

    // Close modal
    const modal = bootstrap.Modal.getInstance(this.elements.noteModal);
    modal?.hide();
  }

  /**
   * Create or update a note
   */
  async createNote(title, content, noteId = null) {
    try {
      const url = noteId
        ? `/api/workspaces/${encodeURIComponent(this.workspaceId)}/notes/${encodeURIComponent(noteId)}`
        : `/api/workspaces/${encodeURIComponent(this.workspaceId)}/notes`;

      const response = await fetch(url, {
        method: noteId ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title, content })
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

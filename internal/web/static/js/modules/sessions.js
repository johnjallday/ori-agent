// Session Sidebar Module
// Handles session management, folders, tags, search, and UI interactions

const sessionManager = {
  // State
  sessions: [],
  folders: [],
  tags: [],
  activeSessionId: null,
  activeFolder: null,
  searchQuery: '',
  sortBy: 'updated_at',
  filterAgent: null,
  filterTags: [],
  isLoading: false,
  selectedSessionIds: [],
  lastSelectedIndex: null,
  folderTreeWasCollapsedOnDrag: false,
  dragHintTimer: null,
  sessionsByFolder: new Map(),
  collapsedFolderIds: new Set(),
  agentModelCache: new Map(),
  sessionTaskCounts: new Map(), // Cache of task counts per session: sessionId -> {total, pending, completed}
  editAgentOriginalName: '',
  editAgentSelectedTags: [],
  editAgentModalInitialized: false,
  editAgentModelOptionsLoaded: false,

  // Auto mode state
  chatAutoMode: false,
  chatLlmAvailable: false,
  chatSystemModelConfigured: false,
  autoModeSessionIds: new Set(), // Sessions that need auto-classification

  // Tab ID for multi-tab support
  tabId: null,

  // Generate or retrieve unique tab ID from sessionStorage
  generateTabId() {
    let tabId = sessionStorage.getItem('oriTabId');
    if (!tabId) {
      tabId = 'tab_' + Date.now() + '_' + Math.random().toString(36).substr(2, 9);
      sessionStorage.setItem('oriTabId', tabId);
    }
    this.tabId = tabId;
    return tabId;
  },

  // Get the active session ID for this tab
  getActiveSessionId() {
    return this.activeSessionId;
  },

  // Get headers for API requests that need session context
  getSessionHeaders() {
    const headers = {};
    if (this.activeSessionId) {
      headers['X-Session-ID'] = this.activeSessionId;
    }
    return headers;
  },

  // Debounce timer for search
  searchDebounceTimer: null,

  // Polling interval for session updates (in ms)
  pollInterval: 30000,
  pollTimer: null,

  // Virtual scroll state
  virtualScrollEnabled: false,
  itemHeight: 70,
  visibleCount: 20,
  scrollOffset: 0,

  // Context menu state
  contextMenuTarget: null,
  contextMenuType: null,

  // Initialize the session sidebar
  async init() {
    // Generate unique tab ID for multi-tab support
    this.generateTabId();

    this.loadCollapsedFolders();
    this.bindEvents();
    this.bindNoteEvents();
    this.setupKeyboardShortcuts();
    this.initChatAgentBar();
    this.initMainTaskPanel();
    this.initScheduledTasksModal();
    await this.loadSessions();
    await this.loadFolders();
    await this.loadTags();

    // Try to restore active session, or prompt to create workspace
    const restored = await this.restoreActiveSession();
    if (!restored && this.sessions.length === 0 && this.folders.length === 0) {
      // Show create workspace modal when no workspaces exist
      this.showAddWorkspaceModal();
    } else if (!restored && this.sessions.length > 0) {
      // No saved session but sessions exist - use the first one
      await this.switchToSession(this.sessions[0].id);
    }

    // Start polling for session updates
    this.startPolling();

    // Stop polling when tab is hidden, resume when visible
    document.addEventListener('visibilitychange', () => {
      if (document.hidden) {
        this.stopPolling();
      } else {
        this.startPolling();
      }
    });
  },

  // Bind all event listeners
  bindEvents() {
    // New chat button - show modal
    document.getElementById('newChatBtn')?.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      this.showCreateChatModal();
    });
    document.getElementById('createFirstSessionBtn')?.addEventListener('click', () => this.handleEmptyStateAction());

    // Create chat modal - create button
    document.getElementById('createChatBtn')?.addEventListener('click', () => this.handleCreateChatFromModal());

    // Toggle sidebar (from inside session sidebar)
    document.getElementById('toggleSessionSidebarBtn')?.addEventListener('click', () => this.toggleSidebar());

    // Toggle sidebar (from navbar)
    document.getElementById('sessionsToggle')?.addEventListener('click', () => this.toggleSidebar());

    // Search input
    const searchInput = document.getElementById('sessionSearchInput');
    searchInput?.addEventListener('input', (e) => this.handleSearchInput(e.target.value));

    const clearSearch = document.getElementById('clearSessionSearch');
    clearSearch?.addEventListener('click', () => this.clearSearch());

    // Sort dropdown
    document.getElementById('sortDropdownBtn')?.addEventListener('click', (e) => {
      e.stopPropagation();
      this.toggleDropdown('sortDropdown');
    });

    // Filter dropdown
    document.getElementById('filterDropdownBtn')?.addEventListener('click', (e) => {
      e.stopPropagation();
      this.toggleDropdown('filterDropdown');
    });

    // Sort options
    document.querySelectorAll('#sortDropdown .session-dropdown-item').forEach(item => {
      item.addEventListener('click', (e) => {
        const sort = e.target.dataset.sort;
        this.setSortOrder(sort);
        this.closeDropdowns();
      });
    });

    // Folder tree toggle
    document.getElementById('folderTreeToggle')?.addEventListener('click', () => this.toggleFolderTree());

    // Add folder button
    document.getElementById('addFolderBtn')?.addEventListener('click', (e) => {
      e.stopPropagation();
      this.showAddWorkspaceModal();
    });

    // Create folder button
    document.getElementById('createFolderBtn')?.addEventListener('click', () => this.createFolder());

    // Folder color options
    document.querySelectorAll('.folder-color-btn').forEach(btn => {
      btn.addEventListener('click', (e) => {
        document.querySelectorAll('.folder-color-btn').forEach(b => b.classList.remove('active'));
        e.target.classList.add('active');
      });
    });

    // Save tags button
    document.getElementById('saveTagsBtn')?.addEventListener('click', () => this.saveTags());

    // Close dropdowns when clicking outside
    document.addEventListener('click', () => this.closeDropdowns());

    // Context menu handlers
    document.addEventListener('click', () => this.hideContextMenus());

    // Session context menu actions
    document.querySelectorAll('#sessionContextMenu .session-context-item').forEach(item => {
      item.addEventListener('click', (e) => {
        const action = e.currentTarget.dataset.action;
        this.handleSessionContextAction(action);
      });
    });

    // Folder context menu actions
    document.querySelectorAll('#folderContextMenu .session-context-item').forEach(item => {
      item.addEventListener('click', (e) => {
        e.stopPropagation();
        const action = e.currentTarget.dataset.action;
        this.handleFolderContextAction(action);
      });
    });

    // Resize handle
    this.setupResizeHandle();

    // Session list scroll (for virtual scroll)
    document.getElementById('sessionListContainer')?.addEventListener('scroll',
      () => this.handleScroll());

    // Session files panel toggle
    document.getElementById('sessionFilesToggle')?.addEventListener('click', () => {
      const content = document.getElementById('sessionFilesContent');
      const icon = document.querySelector('#sessionFilesToggle .folder-expand-icon');
      if (content && icon) {
        content.classList.toggle('collapsed');
        icon.classList.toggle('expanded');
      }
    });

    // Open folder button (uses FileManager if available)
    document.getElementById('openFolderBtn')?.addEventListener('click', () => {
      if (window.sessionFileManager) {
        window.sessionFileManager.openFolder();
      }
    });

    // Chat mode toggle - Manual
    document.getElementById('chatConfigModeManual')?.addEventListener('change', function() {
      if (this.checked) {
        sessionManager.handleChatModeChange('manual');
      }
    });

    // Chat mode toggle - Auto
    document.getElementById('chatConfigModeAuto')?.addEventListener('change', async function() {
      if (this.checked) {
        await sessionManager.checkChatLlmAvailability();
        sessionManager.handleChatModeChange('auto');
      }
    });
  },

  // Setup keyboard shortcuts
  setupKeyboardShortcuts() {
    document.addEventListener('keydown', (e) => {
      // Ctrl/Cmd + N - New session
      if ((e.ctrlKey || e.metaKey) && e.key === 'n') {
        e.preventDefault();
        this.createNewSession();
      }

      // Ctrl/Cmd + K - Focus search
      if ((e.ctrlKey || e.metaKey) && e.key === 'k') {
        e.preventDefault();
        this.focusSearch();
      }

      // Escape - Close context menus and dropdowns
      if (e.key === 'Escape') {
        this.hideContextMenus();
        this.closeDropdowns();
      }
    });
  },

  // Load sessions from API
  // Store for note search results
  noteSearchResults: [],

  async loadSessions() {
    this.isLoading = true;
    this.renderLoadingState();

    try {
      const params = new URLSearchParams();
      if (this.searchQuery) params.set('query', this.searchQuery);
      if (this.sortBy) params.set('sort', this.sortBy);
      if (this.filterAgent) params.set('agent', this.filterAgent);
      if (this.activeFolder) params.set('folder', this.activeFolder);
      if (this.filterTags.length > 0) params.set('tags', this.filterTags.join(','));

      const response = await fetch(`/api/sessions?${params.toString()}`);
      if (!response.ok) throw new Error('Failed to load sessions');

      const data = await response.json();
      this.sessions = data.sessions || [];
      this.reconcileSelection();

      // Also search notes if there's a search query
      if (this.searchQuery && this.searchQuery.trim()) {
        await this.searchNotes(this.searchQuery);
      } else {
        this.noteSearchResults = [];
      }

      this.renderSessions();

      // Load task counts for all sessions (async, updates badges when ready)
      this.loadSessionTaskCounts();
    } catch (error) {
      console.error('Failed to load sessions:', error);
      this.sessions = [];
      this.noteSearchResults = [];
      this.clearSelection();
      this.renderEmptyState();
    } finally {
      this.isLoading = false;
    }
  },

  // Load task counts for all visible sessions (from workspace orchestration API)
  async loadSessionTaskCounts() {
    if (this.sessions.length === 0) return;

    // Group sessions by workspace to avoid duplicate API calls
    const workspaceStats = new Map();
    const workspaceIds = [...new Set(this.sessions.map(s => s.folder_id).filter(Boolean))];

    // Fetch stats for each unique workspace
    const fetchPromises = workspaceIds.map(async (workspaceId) => {
      try {
        const response = await fetch(`/api/orchestration/tasks?studio_id=${workspaceId}`);
        if (response.ok) {
          const data = await response.json();
          workspaceStats.set(workspaceId, data.stats || { total: 0, pending: 0, completed: 0 });
        }
      } catch (error) {
        console.error(`Failed to load task counts for workspace ${workspaceId}:`, error);
      }
    });

    await Promise.all(fetchPromises);

    // Apply workspace stats to sessions
    this.sessions.forEach(session => {
      if (session.folder_id) {
        const stats = workspaceStats.get(session.folder_id);
        if (stats) {
          this.sessionTaskCounts.set(session.id, stats);
        }
      }
    });

    this.updateSessionTaskBadges();
  },

  // Update task count badges on session items
  updateSessionTaskBadges() {
    document.querySelectorAll('.session-item').forEach(item => {
      const sessionId = item.dataset.sessionId;
      const counts = this.sessionTaskCounts.get(sessionId);

      let badge = item.querySelector('.session-task-count-badge');

      if (!counts || counts.total === 0) {
        // Remove badge if no tasks
        if (badge) badge.remove();
        return;
      }

      // Create badge if it doesn't exist
      if (!badge) {
        badge = document.createElement('span');
        badge.className = 'session-task-count-badge';
        const header = item.querySelector('.session-item-header');
        if (header) {
          // Insert before time
          const timeSpan = header.querySelector('.session-time');
          header.insertBefore(badge, timeSpan);
        }
      }

      // Update badge content and style
      if (counts.pending > 0) {
        badge.textContent = counts.pending;
        badge.classList.remove('complete');
        badge.title = `${counts.pending} pending task${counts.pending > 1 ? 's' : ''}`;
      } else {
        badge.textContent = '✓';
        badge.classList.add('complete');
        badge.title = `All ${counts.completed} tasks complete`;
      }
    });
  },

  // Search notes
  async searchNotes(query) {
    try {
      const response = await fetch(`/api/notes/search?q=${encodeURIComponent(query)}`);
      if (!response.ok) throw new Error('Failed to search notes');
      const data = await response.json();
      this.noteSearchResults = data.notes || [];
    } catch (error) {
      console.error('Failed to search notes:', error);
      this.noteSearchResults = [];
    }
  },

  // Load workspaces (folders) from API
  async loadFolders() {
    try {
      const response = await fetch('/api/workspaces?tree=true');
      if (!response.ok) throw new Error('Failed to load workspaces');

      const data = await response.json();
      this.folders = data.folders || [];
      this.updateSessionsEmptyState();

      // Load notes for all folders
      await this.loadAllFolderNotes(this.folders);

      // Load tasks for all workspaces
      await this.loadAllWorkspaceTasks(this.folders);

      // Load scheduled tasks for all workspaces
      await this.loadAllWorkspaceScheduledTasks(this.folders);

      this.renderFolderTree();
    } catch (error) {
      console.error('Failed to load folders:', error);
      this.folders = [];
      this.updateSessionsEmptyState();
    }
  },

  // Load notes for all folders recursively
  async loadAllFolderNotes(folders) {
    for (const folder of folders) {
      await this.loadFolderNotes(folder.id);
      if (folder.children && folder.children.length > 0) {
        await this.loadAllFolderNotes(folder.children);
      }
    }
  },

  // Load all tags from API
  async loadTags() {
    try {
      const response = await fetch('/api/tags');
      if (!response.ok) throw new Error('Failed to load tags');

      const data = await response.json();
      this.tags = data.tags || [];
      this.renderTagFilters();
    } catch (error) {
      console.error('Failed to load tags:', error);
      this.tags = [];
    }
  },

  // Render sessions list
  renderSessions() {
    const container = document.getElementById('sessionsList');
    const loadingState = document.getElementById('sessionsLoading');

    if (!container) return;

    if (loadingState) loadingState.style.display = 'none';

    const hasNotes = this.noteSearchResults.length > 0;
    const hasSessions = this.sessions.length > 0;
    const hasWorkspaces = this.folders.length > 0;
    const shouldShowEmpty = !hasSessions && !hasNotes;

    if (shouldShowEmpty) {
      container.innerHTML = '';
    }

    this.updateSessionsEmptyState();

    if (shouldShowEmpty && !hasWorkspaces) {
      return;
    }

    const folderGroups = new Map();
    this.sessions.forEach(session => {
      const folderId = session.folder_id || '';
      if (!folderGroups.has(folderId)) {
        folderGroups.set(folderId, []);
      }
      folderGroups.get(folderId).push(session);
    });
    this.sessionsByFolder = folderGroups;

    const rootSessions = folderGroups.get('') || [];

    // Render note search results first (if searching)
    const noteResultsHtml = this.searchQuery && hasNotes ? this.renderNoteSearchResults() : '';

    // Render root sessions with "No Folder" header
    container.innerHTML = `
      ${noteResultsHtml}
      ${rootSessions.length > 0 ? this.renderRootHeader() : ''}
      ${rootSessions.map(session => this.renderSessionItem(session)).join('')}
    `;

    container.onclick = (e) => {
      if (e.target === container) {
        this.clearSelection();
      }
    };

    this.bindSessionItemEvents(container);
    this.renderFolderTree();
  },

  // Render note search results
  renderNoteSearchResults() {
    if (this.noteSearchResults.length === 0) return '';

    return `
      <div class="search-notes-section">
        <div class="search-notes-header">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13,9V3.5L18.5,9H13"/>
          </svg>
          <span>Notes (${this.noteSearchResults.length})</span>
        </div>
        <div class="search-notes-list">
          ${this.noteSearchResults.map(note => `
            <div class="search-note-result" data-note-id="${note.id}">
              <svg class="search-note-result-icon" width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                <path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13,9V3.5L18.5,9H13"/>
              </svg>
              <div class="search-note-result-content">
                <div class="search-note-result-title">${this.escapeHtml(note.name)}</div>
                ${note.folder_name ? `<div class="search-note-result-folder">${this.escapeHtml(note.folder_name)}</div>` : ''}
                ${note.snippets && note.snippets.length > 0 ? `<div class="search-note-result-snippet">${note.snippets[0]}</div>` : ''}
              </div>
            </div>
          `).join('')}
        </div>
      </div>
    `;
  },

  // Render a single session item
  renderSessionItem(session) {
    const isActive = session.id === this.activeSessionId;
    const isSelected = this.selectedSessionIds.includes(session.id);
    const timeAgo = this.formatTimeAgo(session.updated_at);
    const preview = session.preview || 'No messages yet';
    const tags = session.tags || [];
    const agentName = session.agent_name || 'Unknown';

    return `
      <div class="session-item ${isActive ? 'active' : ''} ${isSelected ? 'selected' : ''}" data-session-id="${session.id}">
        <div class="session-item-header">
          <svg class="session-icon" width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
            <path d="M20,2H4A2,2 0 0,0 2,4V22L6,18H20A2,2 0 0,0 22,16V4A2,2 0 0,0 20,2Z"/>
          </svg>
          <span class="session-title">${this.escapeHtml(session.title || 'New Session')}</span>
          <span class="session-agent-badge" title="Agent: ${this.escapeHtml(agentName)}">${this.escapeHtml(agentName)}</span>
          <span class="session-time">${timeAgo}</span>
        </div>
        <div class="session-item-footer">
          <span class="session-preview">${this.escapeHtml(preview)}</span>
          ${tags.length > 0 ? `
            <div class="session-tags">
              ${tags.slice(0, 3).map(tag => `
                <span class="session-tag" data-color="${this.getTagColor(tag)}">${this.escapeHtml(tag)}</span>
              `).join('')}
              ${tags.length > 3 ? `<span class="session-tag" data-color="0">+${tags.length - 3}</span>` : ''}
            </div>
          ` : ''}
        </div>
      </div>
    `;
  },

  // Render folder tree
  renderFolderTree() {
    const container = document.getElementById('folderTree');
    if (!container) return;

    if (this.folders.length === 0) {
      container.innerHTML = '<div class="text-center text-muted small py-2">No workspaces</div>';
      return;
    }

    container.innerHTML = this.renderFolderItems(this.folders);

    // Bind folder events
    container.querySelectorAll('.folder-item').forEach(item => {
      const folderId = item.dataset.folderId;

      // Click to collapse/expand folder sessions
      item.addEventListener('click', (e) => {
        e.stopPropagation();
        this.toggleFolderSessions(folderId);
      });

      // Right-click context menu
      item.addEventListener('contextmenu', (e) => {
        e.preventDefault();
        this.showFolderContextMenu(e, folderId);
      });

      // Drop target
      item.addEventListener('dragover', (e) => {
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';
        item.classList.add('drag-over');
      });

      item.addEventListener('dragenter', () => {
        item.classList.add('drag-over');
      });

      item.addEventListener('dragleave', () => {
        item.classList.remove('drag-over');
      });

      item.addEventListener('drop', async (e) => {
        e.preventDefault();
        e.stopPropagation();
        item.classList.remove('drag-over');

        // Check if files are being dropped from OS
        const files = e.dataTransfer.files;
        if (files && files.length > 0) {
          const folderName = item.querySelector('.folder-name')?.textContent?.trim();
          for (const file of files) {
            await this.createFileReferenceNote(folderId, file);
          }
          item.classList.add('drop-success');
          setTimeout(() => item.classList.remove('drop-success'), 700);
          const fileText = files.length === 1 ? 'file reference' : `${files.length} file references`;
          this.showToast(`Created ${fileText} in ${folderName}`, 'success');
          return;
        }

        // Check if it's a note being dropped
        const noteId = e.dataTransfer.getData('application/x-ori-note-id');
        const sourceFolderId = e.dataTransfer.getData('application/x-ori-note-folder');
        if (noteId && sourceFolderId !== folderId) {
          const moved = await this.moveNoteToFolder(noteId, folderId);
          if (moved) {
            item.classList.add('drop-success');
            setTimeout(() => item.classList.remove('drop-success'), 700);
            const folderName = item.querySelector('.folder-name')?.textContent?.trim();
            this.showToast(`Note moved to ${folderName}`, 'success');
          }
          return;
        }

        // Otherwise check for sessions
        const sessionIds = this.getDraggedSessionIds(e);
        if (sessionIds.length > 0) {
          const moved = await this.moveSessionsToFolder(sessionIds, folderId);
          if (moved) {
            item.classList.add('drop-success');
            setTimeout(() => item.classList.remove('drop-success'), 700);
            const folderName = item.querySelector('.folder-name')?.textContent?.trim();
            this.flashDragHint(this.formatMoveHint(sessionIds.length, folderName));
          }
        }
      });
    });

    // Toggle nested sessions visibility
    container.querySelectorAll('.folder-collapse-btn').forEach(button => {
      const folderId = button.dataset.folderId;
      button.addEventListener('click', (e) => {
        e.stopPropagation();
        this.toggleFolderSessions(folderId);
      });
    });

    this.bindSessionItemEvents(container);

    // Bind task section events
    container.querySelectorAll('.folder-tasks-header').forEach(header => {
      header.addEventListener('click', (e) => {
        if (e.target.closest('.folder-tasks-add-btn')) return; // Don't open on add click
        e.stopPropagation();
        const workspaceId = header.dataset.workspaceId;
        this.openWorkspaceTaskPanel(workspaceId);
      });
    });

    container.querySelectorAll('.folder-tasks-add-btn').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        const workspaceId = btn.dataset.workspaceId;
        this.openTaskModalForWorkspace(workspaceId);
      });
    });

    // Bind scheduled tasks section events
    container.querySelectorAll('.folder-schedules-header').forEach(header => {
      header.addEventListener('click', (e) => {
        e.stopPropagation();
        const workspaceId = header.dataset.workspaceId;
        this.openScheduledTasksPanel(workspaceId);
      });
    });
  },

  // Render folder items recursively
  renderFolderItems(folders, depth = 0) {
    return folders.map(folder => {
      const isActive = folder.id === this.activeFolder;
      const colorStyle = folder.color ? `color: ${folder.color};` : '';
      const children = folder.children || [];
      const folderSessions = this.sessionsByFolder?.get(folder.id) || [];
      const isCollapsed = this.collapsedFolderIds.has(folder.id);
      const hasNestedSessions = folderSessions.length > 0;
      const hasAccent = Boolean(folder.color);
      const accentStyles = hasAccent
        ? `data-has-accent="true" style="--folder-accent-bg: ${this.hexToRgba(folder.color, 0.12)}; --folder-accent-bg-hover: ${this.hexToRgba(folder.color, 0.18)}; --folder-accent-border: ${this.hexToRgba(folder.color, 0.35)};"`
        : ``;
      // Get notes for this folder
      const folderNotes = this.notesByFolder?.get(folder.id) || [];
      // Get tasks for this workspace
      const workspaceTasks = this.tasksByWorkspace?.get(folder.id) || [];
      const taskCount = workspaceTasks.length;
      const hasContent = folderSessions.length > 0 || folderNotes.length > 0;

      // Tasks section - clickable header that opens the task modal
      const tasksHtml = taskCount > 0
        ? `
          <div class="folder-tasks-wrapper ${isCollapsed ? 'collapsed' : ''}">
            <div class="folder-tasks-header" data-workspace-id="${folder.id}">
              <svg class="folder-tasks-header-icon" width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                <path d="M19,3H5A2,2 0 0,0 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V5A2,2 0 0,0 19,3M19,19H5V5H19V19M17,17H7V7H17V17M9,9V15H15V9H9Z"/>
              </svg>
              <span class="folder-tasks-header-text">Tasks</span>
              <span class="folder-tasks-count">${taskCount}</span>
              <button class="folder-tasks-add-btn" data-workspace-id="${folder.id}" title="Add task">
                <svg viewBox="0 0 24 24" fill="currentColor" width="14" height="14">
                  <path d="M19,13H13V19H11V13H5V11H11V5H13V11H19V13Z"/>
                </svg>
              </button>
            </div>
          </div>
        `
        : '';

      // Get scheduled tasks for this workspace
      const workspaceScheduledTasks = this.scheduledTasksByWorkspace?.get(folder.id) || [];
      const scheduledTaskCount = workspaceScheduledTasks.length;
      const enabledScheduleCount = workspaceScheduledTasks.filter(st => st.enabled).length;

      // Schedules section - clickable header that opens the scheduled tasks panel
      const schedulesHtml = scheduledTaskCount > 0
        ? `
          <div class="folder-schedules-wrapper ${isCollapsed ? 'collapsed' : ''}">
            <div class="folder-schedules-header" data-workspace-id="${folder.id}">
              <svg class="folder-schedules-header-icon" width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                <path d="M12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22C6.47,22 2,17.5 2,12A10,10 0 0,1 12,2M12.5,7V12.25L17,14.92L16.25,16.15L11,13V7H12.5Z"/>
              </svg>
              <span class="folder-schedules-header-text">Schedules</span>
              <span class="folder-schedules-count">${enabledScheduleCount}/${scheduledTaskCount}</span>
            </div>
          </div>
        `
        : '';

      const sessionsHtml = folderSessions.length > 0
        ? `
          <div class="folder-sessions ${isCollapsed ? 'collapsed' : ''}" ${accentStyles}>
            ${folderSessions.map(session => this.renderSessionItem(session)).join('')}
          </div>
        `
        : '';

      const notesHtml = folderNotes.length > 0
        ? `
          <div class="folder-notes-section ${isCollapsed ? 'collapsed' : ''}">
            ${folderNotes.map(note => `
              <div class="folder-note-item" data-note-id="${note.id}" data-folder-id="${folder.id}" draggable="true">
                <svg class="folder-note-icon" width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13,9V3.5L18.5,9H13"/>
                </svg>
                <span class="folder-note-name">${this.escapeHtml(note.name)}</span>
              </div>
            `).join('')}
          </div>
        `
        : '';

      const folderTitle = folder.description ? this.escapeHtml(folder.description) : folder.name;
      return `
        <div class="folder-item ${isActive ? 'active' : ''} ${isCollapsed ? 'collapsed' : ''}" data-folder-id="${folder.id}" style="padding-left: ${8 + depth * 12}px;" title="${folderTitle}">
          ${hasContent ? `
            <button class="folder-collapse-btn" data-folder-id="${folder.id}" title="${isCollapsed ? 'Show content' : 'Hide content'}">
              <svg viewBox="0 0 24 24" fill="currentColor">
                <path d="M8.59,16.58L13.17,12L8.59,7.41L10,6L16,12L10,18L8.59,16.58Z"/>
              </svg>
            </button>
          ` : '<span class="folder-collapse-spacer"></span>'}
          <svg class="folder-icon" viewBox="0 0 24 24" fill="currentColor" style="${colorStyle}">
            <path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>
          </svg>
          <span class="folder-name">${this.escapeHtml(folder.name)}</span>
          ${folder.session_count > 0 ? `<span class="folder-count">${folder.session_count}</span>` : ''}
        </div>
        ${notesHtml}
        ${sessionsHtml}
        ${tasksHtml}
        ${schedulesHtml}
        ${children.length > 0 ? `<div class="folder-children">${this.renderFolderItems(children, depth + 1)}</div>` : ''}
      `;
    }).join('');
  },

  // Bind events for session items in a container
  bindSessionItemEvents(container) {
    container.querySelectorAll('.session-item').forEach(item => {
      const sessionId = item.dataset.sessionId;

      // Click to switch session or multi-select
      item.addEventListener('click', (e) => this.handleSessionClick(e, sessionId));

      // Right-click context menu
      item.addEventListener('contextmenu', (e) => {
        e.preventDefault();
        if (!this.selectedSessionIds.includes(sessionId)) {
          this.setSelection([sessionId], this.getSessionIndex(sessionId));
        }
        this.showSessionContextMenu(e, sessionId);
      });

      // Drag and drop
      item.setAttribute('draggable', 'true');
      item.addEventListener('dragstart', (e) => this.handleDragStart(e, sessionId));
      item.addEventListener('dragend', () => this.handleDragEnd());
    });
  },

  // Render "No Folder" header for root sessions
  renderRootHeader() {
    return `
      <div class="session-folder-header">
        <svg class="session-folder-icon" viewBox="0 0 24 24" fill="currentColor">
          <path d="M19,20H5A2,2 0 0,1 3,18V6A2,2 0 0,1 5,4H9L11,6H19A2,2 0 0,1 21,8V18A2,2 0 0,1 19,20M5,18H19V10H5V18M5,8H9L7,6H5V8Z"/>
        </svg>
        <span class="session-folder-name">No Workspace</span>
      </div>
    `;
  },

  // Toggle visibility of sessions under a folder
  toggleFolderSessions(folderId) {
    if (!folderId) return;
    if (this.collapsedFolderIds.has(folderId)) {
      this.collapsedFolderIds.delete(folderId);
    } else {
      this.collapsedFolderIds.add(folderId);
    }
    this.saveCollapsedFolders();
    this.renderFolderTree();
  },

  // Load collapsed folder state from localStorage
  loadCollapsedFolders() {
    const raw = localStorage.getItem('sessionFolderCollapsed');
    if (!raw) return;
    try {
      const parsed = JSON.parse(raw);
      if (Array.isArray(parsed)) {
        this.collapsedFolderIds = new Set(parsed);
      }
    } catch (error) {
      console.warn('Failed to load collapsed folders', error);
    }
  },

  // Persist collapsed folder state
  saveCollapsedFolders() {
    localStorage.setItem('sessionFolderCollapsed', JSON.stringify(Array.from(this.collapsedFolderIds)));
  },

  // Update empty state copy and visibility
  updateSessionsEmptyState() {
    const emptyState = document.getElementById('sessionsEmpty');
    if (!emptyState) return;

    const emptyText = document.getElementById('sessionsEmptyText');
    const emptyActionLabel = document.getElementById('sessionsEmptyActionLabel');
    const hasSessions = this.sessions.length > 0;
    const hasNotes = this.noteSearchResults.length > 0;
    const hasWorkspaces = this.folders.length > 0;
    const shouldShow = !hasSessions && !hasNotes;

    if (!shouldShow) {
      emptyState.style.display = 'none';
      return;
    }

    if (hasWorkspaces) {
      if (emptyText) emptyText.textContent = 'No sessions yet';
      if (emptyActionLabel) emptyActionLabel.textContent = 'New Chat';
    } else {
      if (emptyText) emptyText.textContent = 'No workspaces yet';
      if (emptyActionLabel) emptyActionLabel.textContent = 'Create Workspace';
    }

    emptyState.style.display = 'flex';
  },

  // Handle empty state action button
  handleEmptyStateAction() {
    if (this.folders.length === 0) {
      this.showAddWorkspaceModal();
      return;
    }
    this.showCreateChatModal();
  },


  // Render loading state
  renderLoadingState() {
    const loadingState = document.getElementById('sessionsLoading');
    const emptyState = document.getElementById('sessionsEmpty');
    const container = document.getElementById('sessionsList');

    if (loadingState) loadingState.style.display = 'flex';
    if (emptyState) emptyState.style.display = 'none';
    if (container) container.innerHTML = '';
  },

  // Render empty state
  renderEmptyState() {
    const loadingState = document.getElementById('sessionsLoading');

    if (loadingState) loadingState.style.display = 'none';
    this.updateSessionsEmptyState();
  },

  // Render tag filters
  renderTagFilters() {
    const container = document.getElementById('tagFilters');
    if (!container) return;

    if (this.tags.length === 0) {
      container.innerHTML = '<div class="text-muted small px-3 py-1">No tags yet</div>';
      return;
    }

    container.innerHTML = this.tags.map(tag => `
      <button class="session-dropdown-item ${this.filterTags.includes(tag) ? 'active' : ''}" data-tag="${this.escapeHtml(tag)}">
        <span class="session-tag" data-color="${this.getTagColor(tag)}" style="margin-right: 6px;">${this.escapeHtml(tag)}</span>
      </button>
    `).join('');

    container.querySelectorAll('.session-dropdown-item').forEach(item => {
      item.addEventListener('click', (e) => {
        const tag = e.currentTarget.dataset.tag;
        this.toggleTagFilter(tag);
      });
    });
  },

  // Check LLM availability for auto mode
  async checkChatLlmAvailability() {
    try {
      const response = await fetch('/api/agents/auto-config/availability');
      const data = await response.json();
      this.chatLlmAvailable = data.available;
      this.chatSystemModelConfigured = data.system_model_configured || false;
      return data;
    } catch (error) {
      console.error('Failed to check LLM availability:', error);
      this.chatLlmAvailable = false;
      this.chatSystemModelConfigured = false;
      return { available: false, system_model_configured: false };
    }
  },

  // Handle chat mode toggle change
  handleChatModeChange(mode) {
    const manualSection = document.getElementById('chatManualSection');
    const autoSection = document.getElementById('chatAutoSection');
    const llmWarning = document.getElementById('chatLlmNotAvailableWarning');
    const llmWarningMessage = document.getElementById('chatLlmWarningMessage');
    const createBtnText = document.getElementById('createChatBtnText');

    this.chatAutoMode = (mode === 'auto');

    if (mode === 'auto') {
      if (this.chatLlmAvailable) {
        if (manualSection) manualSection.classList.add('d-none');
        if (autoSection) autoSection.classList.remove('d-none');
        if (llmWarning) llmWarning.classList.add('d-none');
        if (createBtnText) createBtnText.textContent = 'Start Chat';
      } else {
        // LLM not available - show warning with action button
        if (manualSection) manualSection.classList.add('d-none');
        if (autoSection) autoSection.classList.add('d-none');
        if (llmWarning) llmWarning.classList.remove('d-none');
        if (createBtnText) createBtnText.textContent = 'Go to Settings';
        if (llmWarningMessage) {
          if (!this.chatSystemModelConfigured) {
            llmWarningMessage.textContent = 'Auto mode requires a System Model to be configured.';
          } else {
            llmWarningMessage.textContent = 'Auto mode requires an LLM provider. Please set up an API key or install Ollama.';
          }
        }
      }
    } else {
      // Manual mode
      if (manualSection) manualSection.classList.remove('d-none');
      if (autoSection) autoSection.classList.add('d-none');
      if (llmWarning) llmWarning.classList.add('d-none');
      if (createBtnText) createBtnText.textContent = 'Create';
    }
  },

  // Show create chat modal with workspace and agent selection
  async showCreateChatModal() {
    try {
      // Fetch agents
      const agents = await this.fetchAgents();

      if (!agents || agents.length === 0) {
        console.error('No agents available');
        return;
      }

      // Reset mode to manual
      this.chatAutoMode = false;
      const manualRadio = document.getElementById('chatConfigModeManual');
      if (manualRadio) manualRadio.checked = true;
      this.handleChatModeChange('manual');

      // Clear auto message textarea
      const autoMessageInput = document.getElementById('chatAutoMessage');
      if (autoMessageInput) autoMessageInput.value = '';

      // Populate workspace dropdown
      const workspaceSelect = document.getElementById('chatWorkspaceSelect');
      if (workspaceSelect) {
        workspaceSelect.innerHTML = '<option value="">No workspace (root)</option>';
        this.folders.forEach(folder => {
          workspaceSelect.innerHTML += `<option value="${folder.id}">${this.escapeHtml(folder.name)}</option>`;
        });
      }

      // Populate agent dropdown
      const agentSelect = document.getElementById('chatAgentSelect');
      if (agentSelect) {
        agentSelect.innerHTML = '';
        agents.forEach(agent => {
          agentSelect.innerHTML += `<option value="${agent.name}">${this.escapeHtml(agent.name)}</option>`;
        });
      }

      // Show the modal
      const modal = document.getElementById('createChatModal');
      if (modal) {
        if (typeof bootstrap !== 'undefined' && typeof bootstrap.Modal !== 'undefined') {
          const bsModal = new bootstrap.Modal(modal);
          bsModal.show();
        } else {
          // Fallback: show modal manually
          modal.classList.add('show');
          modal.style.display = 'block';
          document.body.classList.add('modal-open');
        }
      }
    } catch (error) {
      console.error('Failed to show create chat modal:', error);
    }
  },

  // Show create chat modal with workspace pre-selected
  async showCreateChatModalForWorkspace(workspaceId) {
    try {
      // Fetch agents
      const agents = await this.fetchAgents();

      if (!agents || agents.length === 0) {
        console.error('No agents available');
        return;
      }

      // Populate workspace dropdown
      const workspaceSelect = document.getElementById('chatWorkspaceSelect');
      if (workspaceSelect) {
        workspaceSelect.innerHTML = '<option value="">No workspace (root)</option>';
        this.folders.forEach(folder => {
          const selected = folder.id === workspaceId ? ' selected' : '';
          workspaceSelect.innerHTML += `<option value="${folder.id}"${selected}>${this.escapeHtml(folder.name)}</option>`;
        });
      }

      // Populate agent dropdown
      const agentSelect = document.getElementById('chatAgentSelect');
      if (agentSelect) {
        agentSelect.innerHTML = '';
        agents.forEach(agent => {
          agentSelect.innerHTML += `<option value="${agent.name}">${this.escapeHtml(agent.name)}</option>`;
        });
      }

      // Show the modal
      const modal = document.getElementById('createChatModal');
      if (modal) {
        if (typeof bootstrap !== 'undefined' && typeof bootstrap.Modal !== 'undefined') {
          const bsModal = new bootstrap.Modal(modal);
          bsModal.show();
        } else {
          // Fallback: show modal manually
          modal.classList.add('show');
          modal.style.display = 'block';
          document.body.classList.add('modal-open');
        }
      }
    } catch (error) {
      console.error('Failed to show create chat modal:', error);
    }
  },

  // Handle create chat from modal
  async handleCreateChatFromModal() {
    // If auto mode selected but LLM not available, redirect to settings
    if (this.chatAutoMode && !this.chatLlmAvailable) {
      // Close the modal first
      const modal = document.getElementById('createChatModal');
      if (modal) {
        const bsModal = bootstrap.Modal.getInstance(modal);
        bsModal?.hide();
      }
      // Redirect to settings
      window.location.href = '/settings';
      return;
    }

    // Get the initial message for auto mode before closing modal
    const autoMessageInput = document.getElementById('chatAutoMessage');
    const initialMessage = this.chatAutoMode ? (autoMessageInput?.value?.trim() || '') : '';

    // Validate message in auto mode
    if (this.chatAutoMode && this.chatLlmAvailable && !initialMessage) {
      if (window.Toast) {
        Toast.warning('Please enter a message to start the chat');
      }
      autoMessageInput?.focus();
      return;
    }

    // Close the modal
    const modal = document.getElementById('createChatModal');
    if (modal) {
      const bsModal = bootstrap.Modal.getInstance(modal);
      bsModal?.hide();
    }

    if (this.chatAutoMode && this.chatLlmAvailable) {
      // Auto mode - create session with default agent, no workspace
      // The workspace and agent will be determined after a few messages
      const agents = await this.fetchAgents();
      if (!agents || agents.length === 0) {
        console.error('No agents available');
        return;
      }

      // Use first agent as default
      const defaultAgent = agents[0].name;
      const session = await this.createSessionWithAgent(defaultAgent);

      if (session && session.id) {
        // Mark this session for auto-classification
        this.autoModeSessionIds.add(session.id);
        console.log('Auto-mode session created:', session.id);

        // Send the initial message after a brief delay to ensure UI is ready
        if (initialMessage && window.sendMessageToChat) {
          setTimeout(() => {
            window.sendMessageToChat(initialMessage);
          }, 100);
        }
      }
    } else {
      // Manual mode - use selected workspace and agent
      const workspaceSelect = document.getElementById('chatWorkspaceSelect');
      const agentSelect = document.getElementById('chatAgentSelect');

      const workspaceId = workspaceSelect?.value || '';
      const agentName = agentSelect?.value;

      if (!agentName) {
        console.error('No agent selected');
        return;
      }

      // Create the session
      if (workspaceId) {
        await this.createSessionWithAgentInFolder(agentName, workspaceId);
      } else {
        await this.createSessionWithAgent(agentName);
      }
    }
  },

  // Create new session - shows agent picker dialog first (legacy)
  async createNewSession() {
    // Now redirects to the modal
    this.showCreateChatModal();
  },

  // Fetch available agents
  async fetchAgents() {
    try {
      const response = await fetch('/api/agents');
      if (!response.ok) return [];
      const data = await response.json();
      return data.agents || [];
    } catch (error) {
      console.error('Failed to fetch agents:', error);
      return [];
    }
  },

  // Show agent picker dialog
  showAgentPickerDialog(agents) {
    // Remove existing modal if any
    const existingModal = document.getElementById('agentPickerModal');
    if (existingModal) existingModal.remove();

    const currentAgentName = typeof currentAgent !== 'undefined' ? currentAgent : '';

    const modalHtml = `
      <div class="modal fade" id="agentPickerModal" tabindex="-1">
        <div class="modal-dialog modal-dialog-centered">
          <div class="modal-content bg-dark text-light">
            <div class="modal-header border-secondary">
              <h5 class="modal-title">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                  <path d="M12,2A2,2 0 0,1 14,4C14,4.74 13.6,5.39 13,5.73V7H14A7,7 0 0,1 21,14H22A1,1 0 0,1 23,15V18A1,1 0 0,1 22,19H21V20A2,2 0 0,1 19,22H5A2,2 0 0,1 3,20V19H2A1,1 0 0,1 1,18V15A1,1 0 0,1 2,14H3A7,7 0 0,1 10,7H11V5.73C10.4,5.39 10,4.74 10,4A2,2 0 0,1 12,2M7.5,13A2.5,2.5 0 0,0 5,15.5A2.5,2.5 0 0,0 7.5,18A2.5,2.5 0 0,0 10,15.5A2.5,2.5 0 0,0 7.5,13M16.5,13A2.5,2.5 0 0,0 14,15.5A2.5,2.5 0 0,0 16.5,18A2.5,2.5 0 0,0 19,15.5A2.5,2.5 0 0,0 16.5,13Z"/>
                </svg>
                Select Agent for New Session
              </h5>
              <button type="button" class="btn-close btn-close-white" data-bs-dismiss="modal"></button>
            </div>
            <div class="modal-body">
              <div class="list-group list-group-flush">
                ${agents.map(agent => `
                  <button type="button" class="list-group-item list-group-item-action bg-dark text-light border-secondary agent-picker-item ${agent.name === currentAgentName ? 'active' : ''}"
                          data-agent-name="${this.escapeHtml(agent.name)}">
                    <div class="d-flex justify-content-between align-items-center">
                      <div>
                        <strong>${this.escapeHtml(agent.name)}</strong>
                        ${agent.model ? `<small class="text-muted ms-2">${this.escapeHtml(agent.model)}</small>` : ''}
                      </div>
                      ${agent.name === currentAgentName ? '<span class="badge bg-primary">Current</span>' : ''}
                    </div>
                    ${agent.description ? `<small class="text-muted d-block mt-1">${this.escapeHtml(agent.description)}</small>` : ''}
                  </button>
                `).join('')}
              </div>
            </div>
            <div class="modal-footer border-secondary">
              <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">Cancel</button>
            </div>
          </div>
        </div>
      </div>
    `;

    document.body.insertAdjacentHTML('beforeend', modalHtml);

    const modal = new bootstrap.Modal(document.getElementById('agentPickerModal'));

    // Handle agent selection
    document.querySelectorAll('.agent-picker-item').forEach(item => {
      item.addEventListener('click', async () => {
        const agentName = item.dataset.agentName;
        modal.hide();
        await this.createSessionWithAgent(agentName);
      });
    });

    // Cleanup on modal hidden
    document.getElementById('agentPickerModal').addEventListener('hidden.bs.modal', () => {
      document.getElementById('agentPickerModal')?.remove();
    });

    modal.show();
  },

  // Create session with specific agent
  async createSessionWithAgent(agentName) {
    try {
      const response = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: 'New Session',
          agent_name: agentName
        })
      });

      if (!response.ok) throw new Error('Failed to create session');

      const data = await response.json();
      if (data.session) {
        this.sessions.unshift(data.session);
        this.activeSessionId = data.session.id;
        this.saveActiveSession();
        this.renderSessions();

        // Update the combined chat info bar with session title and agent
        this.updateChatInfoBar(data.session.title || 'New Session', agentName);

        // Update the current agent globally
        this.updateCurrentAgent(agentName);

        // Clear chat area for new session
        if (typeof clearChatHistory === 'function') {
          clearChatHistory();
        }

        return data.session;
      }
      return null;
    } catch (error) {
      console.error('Failed to create session:', error);
      return null;
    }
  },

  // Create a new session in a specific folder/workspace
  async createNewSessionInFolder(folderId) {
    try {
      // Fetch available agents
      const agents = await this.fetchAgents();
      if (!agents || agents.length === 0) {
        console.error('No agents available');
        return;
      }

      // If only one agent, create directly
      if (agents.length === 1) {
        await this.createSessionWithAgentInFolder(agents[0].name, folderId);
        return;
      }

      // Show agent picker dialog with folder context
      this.showAgentPickerDialogForFolder(agents, folderId);
    } catch (error) {
      console.error('Failed to create session in folder:', error);
    }
  },

  // Show agent picker dialog for folder context
  showAgentPickerDialogForFolder(agents, folderId) {
    // Remove existing modal if any
    const existingModal = document.getElementById('agentPickerModal');
    if (existingModal) existingModal.remove();

    const modal = document.createElement('div');
    modal.id = 'agentPickerModal';
    modal.className = 'modal fade show';
    modal.style.display = 'block';
    modal.style.backgroundColor = 'rgba(0,0,0,0.5)';
    modal.innerHTML = `
      <div class="modal-dialog modal-dialog-centered modal-sm">
        <div class="modal-content" style="background: var(--bg-primary); border: 1px solid var(--border-color);">
          <div class="modal-header" style="border-bottom: 1px solid var(--border-color);">
            <h6 class="modal-title" style="color: var(--text-primary);">Select Agent</h6>
            <button type="button" class="btn-close" data-dismiss="modal"></button>
          </div>
          <div class="modal-body">
            <div class="agent-picker-list">
              ${agents.map(agent => `
                <button class="agent-picker-item modern-btn modern-btn-secondary w-100 mb-2 text-start" data-agent="${agent.name}">
                  <span class="agent-name">${this.escapeHtml(agent.name)}</span>
                  ${agent.description ? `<small class="text-muted d-block">${this.escapeHtml(agent.description)}</small>` : ''}
                </button>
              `).join('')}
            </div>
          </div>
        </div>
      </div>
    `;

    document.body.appendChild(modal);

    // Handle agent selection
    modal.querySelectorAll('.agent-picker-item').forEach(btn => {
      btn.addEventListener('click', () => {
        const agentName = btn.dataset.agent;
        modal.remove();
        this.createSessionWithAgentInFolder(agentName, folderId);
      });
    });

    // Handle close
    modal.querySelector('.btn-close').addEventListener('click', () => modal.remove());
    modal.addEventListener('click', (e) => {
      if (e.target === modal) modal.remove();
    });
  },

  // Create session with agent in a specific folder
  async createSessionWithAgentInFolder(agentName, folderId) {
    try {
      const response = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: 'New Session',
          agent_name: agentName,
          folder_id: folderId
        })
      });

      if (!response.ok) throw new Error('Failed to create session');

      const data = await response.json();
      if (data.session) {
        this.sessions.unshift(data.session);
        this.activeSessionId = data.session.id;
        this.saveActiveSession();
        this.renderSessions();

        // Update the combined chat info bar with session title and agent
        this.updateChatInfoBar(data.session.title || 'New Session', agentName);

        // Update the current agent globally
        this.updateCurrentAgent(agentName);

        // Clear chat area for new session
        if (typeof clearChatHistory === 'function') {
          clearChatHistory();
        }
      }
    } catch (error) {
      console.error('Failed to create session in folder:', error);
    }
  },

  // Switch to a session
  async switchToSession(sessionId) {
    if (sessionId === this.activeSessionId) return;

    try {
      // Load full session with messages
      const response = await fetch(`/api/sessions/${sessionId}`);
      if (!response.ok) throw new Error('Failed to load session');

      const session = await response.json();

      this.activeSessionId = sessionId;
      this.saveActiveSession();
      this.renderSessions();

      // Update the combined chat info bar with session title and agent
      this.updateChatInfoBar(session.title || 'New Session', session.agent_name);

      // Update the current agent globally
      if (session.agent_name) {
        this.updateCurrentAgent(session.agent_name);
      }

      // Restore messages to chat area
      this.restoreSessionMessages(session);

      // Initialize/update session file manager
      this.initializeSessionFiles(sessionId);

      // Load tasks for this session
      await this.loadSessionTasks();
    } catch (error) {
      console.error('Failed to switch session:', error);
    }
  },

  // Initialize session files panel
  initializeSessionFiles(sessionId) {
    const panel = document.getElementById('sessionFilesPanel');
    const dropzone = document.getElementById('fileDropzoneCompact');
    const fileList = document.getElementById('fileListContainer');

    if (panel && sessionId) {
      // Show the panel
      panel.style.display = 'block';

      // Update session ID in data attributes
      if (dropzone) dropzone.dataset.sessionId = sessionId;
      if (fileList) fileList.dataset.sessionId = sessionId;

      // Initialize or update FileManager
      if (window.FileManager) {
        if (window.sessionFileManager) {
          window.sessionFileManager.disconnect();
        }
        window.sessionFileManager = new FileManager(sessionId);
      }
    } else if (panel) {
      // Hide panel when no session
      panel.style.display = 'none';
    }
  },

  // Update the current agent display (chat area bar and navbar)
  updateCurrentAgent(agentName) {
    if (!agentName) return;

    // Update the global currentAgent variable
    if (typeof currentAgent !== 'undefined') {
      window.currentAgent = agentName;
    }

    const session = this.getActiveSession();
    this.updateChatInfoBar(session?.title, agentName);

    // Update the model display for the active agent
    this.updateChatModelForAgent(agentName);

    // Update the navbar display (secondary/legacy)
    const agentElement = document.querySelector('#currentAgentDisplay span.fw-medium');
    if (agentElement) {
      agentElement.textContent = agentName;
    }

    // Also update any agent display in the header
    const agentHeader = document.querySelector('.agent-name');
    if (agentHeader) {
      agentHeader.textContent = agentName;
    }

    if (typeof loadAgentsForSidebar === 'function') {
      loadAgentsForSidebar();
    }
  },

  // Update the combined chat info bar (session + agent)
  updateChatInfoBar(sessionTitle, agentName) {
    const infoBar = document.getElementById('chatInfoBar');
    const sessionTitleEl = document.getElementById('chatSessionTitle');
    const agentNameEl = document.getElementById('chatAgentName');
    const editAgentBtn = document.getElementById('chatEditAgentBtn');
    const navbarAgentDisplay = document.getElementById('currentAgentDisplay');

    if (!infoBar) return;

    // Update session title
    if (sessionTitleEl && sessionTitle) {
      sessionTitleEl.textContent = sessionTitle;
    }

    // Update agent name
    if (agentNameEl && agentName) {
      agentNameEl.textContent = agentName;
    }

    if (editAgentBtn) {
      if (agentName) {
        editAgentBtn.dataset.agentName = agentName;
        editAgentBtn.classList.remove('disabled');
        editAgentBtn.removeAttribute('aria-disabled');
        editAgentBtn.removeAttribute('tabindex');
        editAgentBtn.disabled = false;
      } else {
        editAgentBtn.dataset.agentName = '';
        editAgentBtn.classList.add('disabled');
        editAgentBtn.setAttribute('aria-disabled', 'true');
        editAgentBtn.setAttribute('tabindex', '-1');
        editAgentBtn.disabled = true;
      }
    }

    // Show bar if we have either session or agent
    if (sessionTitle || agentName) {
      infoBar.style.display = 'block';
      // Hide navbar agent display when chat info bar is visible
      if (navbarAgentDisplay) {
        navbarAgentDisplay.style.display = 'none';
      }
    } else {
      infoBar.style.display = 'none';
      // Show navbar agent display as fallback
      if (navbarAgentDisplay) {
        navbarAgentDisplay.style.display = '';
      }
    }
  },

  // Update model display in chat info bar
  updateChatModelName(modelName) {
    const modelInfo = document.getElementById('chatModelInfo');
    const modelNameEl = document.getElementById('chatModelName');
    if (!modelInfo || !modelNameEl) return;

    if (modelName) {
      modelNameEl.textContent = modelName;
      modelNameEl.title = modelName;
      modelInfo.style.display = 'flex';
    } else {
      modelNameEl.textContent = '';
      modelNameEl.title = '';
      modelInfo.style.display = 'none';
    }
  },

  // Fetch and update the model for the current agent
  async updateChatModelForAgent(agentName) {
    if (!agentName) {
      this.updateChatModelName('');
      return;
    }

    const cachedModel = this.agentModelCache.get(agentName);
    if (cachedModel) {
      this.updateChatModelName(cachedModel);
      return;
    }

    this.updateChatModelName('');

    try {
      const response = await fetch(`/api/agents?name=${encodeURIComponent(agentName)}`);
      if (!response.ok) {
        this.updateChatModelName('');
        return;
      }

      const data = await response.json();
      const modelName = data.model || '';
      if (modelName) {
        this.agentModelCache.set(agentName, modelName);
      }

      const activeAgentName = this.getActiveSession()?.agent_name || agentName;
      if (activeAgentName !== agentName) return;

      this.updateChatModelName(modelName);
    } catch (error) {
      console.error('Failed to load agent model:', error);
      this.updateChatModelName('');
    }
  },

  // Initialize chat agent bar button handler
  initChatAgentBar() {
    // Hide navbar agent display - it's replaced by the chat agent bar
    const navbarAgentDisplay = document.getElementById('currentAgentDisplay');
    if (navbarAgentDisplay) {
      navbarAgentDisplay.style.display = 'none';
    }

    const changeBtn = document.getElementById('chatChangeAgentBtn');
    if (changeBtn && !changeBtn.dataset.bound) {
      changeBtn.dataset.bound = 'true';
      changeBtn.addEventListener('click', async () => {
        if (this.activeSessionId) {
          await this.showChangeAgentDialog(this.activeSessionId);
        }
      });
    }

    const editBtn = document.getElementById('chatEditAgentBtn');
    if (editBtn && !editBtn.dataset.bound) {
      editBtn.dataset.bound = 'true';
      editBtn.addEventListener('click', (event) => {
        event.preventDefault();
        const agentName = editBtn.dataset.agentName || this.getActiveSession()?.agent_name;
        if (!agentName) {
          if (window.Toast) {
            Toast.warning('Select an agent before editing.', { title: 'No Agent Selected' });
          }
          return;
        }
        this.showEditAgentModal(agentName);
      });
    }

    this.initEditAgentModal();
  },

  initEditAgentModal() {
    if (this.editAgentModalInitialized) return;
    const modalEl = document.getElementById('editAgentModal');
    if (!modalEl) return;

    this.editAgentModalInitialized = true;

    modalEl.addEventListener('hidden.bs.modal', () => {
      this.resetEditAgentModal();
    });

    const form = document.getElementById('editAgentForm');
    if (form) {
      form.addEventListener('submit', (event) => event.preventDefault());
    }

    const saveBtn = document.getElementById('editAgentSaveBtn');
    if (saveBtn) {
      saveBtn.addEventListener('click', () => this.saveEditAgentChanges());
    }

    const tagsInput = document.getElementById('editAgentTagsInput');
    if (tagsInput) {
      tagsInput.addEventListener('keydown', (event) => {
        if (event.key === 'Enter' && tagsInput.value.trim()) {
          event.preventDefault();
          this.addEditAgentTag(tagsInput.value.trim());
          tagsInput.value = '';
        } else if (event.key === 'Backspace' && !tagsInput.value && this.editAgentSelectedTags.length > 0) {
          this.removeEditAgentTag(this.editAgentSelectedTags[this.editAgentSelectedTags.length - 1]);
        }
      });
    }

    const tagsContainer = document.getElementById('editAgentTagsContainer');
    if (tagsContainer && tagsInput) {
      tagsContainer.addEventListener('click', (event) => {
        if (event.target === tagsContainer) {
          tagsInput.focus();
        }
      });
    }

    const colorInput = document.getElementById('editAgentAvatarColor');
    if (colorInput) {
      colorInput.addEventListener('input', (event) => {
        this.updateEditAgentColorPreview(event.target.value);
      });
    }

    // Listen for Type changes to update Model options
    const typeSelect = document.getElementById('editAgentTypeSelect');
    if (typeSelect) {
      typeSelect.addEventListener('change', (event) => {
        const modelSelect = document.getElementById('editAgentModelSelect');
        const currentModel = modelSelect?.value;
        this.updateEditAgentModelOptions(event.target.value, currentModel);
      });
    }
  },

  async showEditAgentModal(agentName) {
    const modalEl = document.getElementById('editAgentModal');
    if (!modalEl) return;

    this.initEditAgentModal();
    this.editAgentOriginalName = agentName;
    this.editAgentSelectedTags = [];
    this.clearEditAgentMessages();
    this.setEditAgentLoading(true, 'Loading agent...');
    this.setEditAgentFormEnabled(false);

    const modal = bootstrap.Modal.getInstance(modalEl) || new bootstrap.Modal(modalEl);
    modal.show();

    try {
      await this.ensureEditAgentModelOptions();
      await this.loadEditAgentDetails(agentName);
    } catch (error) {
      console.error('Failed to load agent details:', error);
      this.showEditAgentError(error.message || 'Failed to load agent details');
    } finally {
      this.setEditAgentLoading(false);
      this.setEditAgentFormEnabled(true);
    }
  },

  async loadEditAgentDetails(agentName) {
    const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}/detail`);
    if (!response.ok) {
      if (response.status === 404) {
        throw new Error('Agent not found');
      }
      throw new Error('Failed to load agent details');
    }

    const agent = await response.json();
    this.populateEditAgentForm(agent);
  },

  populateEditAgentForm(agent) {
    const nameInput = document.getElementById('editAgentNameInput');
    if (nameInput) nameInput.value = agent.name || '';

    const typeSelect = document.getElementById('editAgentTypeSelect');
    if (typeSelect) {
      this.ensureEditAgentSelectOption(typeSelect, agent.type);
      typeSelect.value = agent.type || typeSelect.value;
    }

    const roleSelect = document.getElementById('editAgentRoleSelect');
    if (roleSelect) {
      this.ensureEditAgentSelectOption(roleSelect, agent.role);
      roleSelect.value = agent.role || roleSelect.value;
    }

    // Update model options based on agent type, then set the current model
    const agentType = agent.type || 'tool-calling';
    this.updateEditAgentModelOptions(agentType, agent.model);

    const descriptionInput = document.getElementById('editAgentDescription');
    if (descriptionInput) {
      descriptionInput.value = agent.metadata?.description || '';
    }

    const colorInput = document.getElementById('editAgentAvatarColor');
    const colorValue = agent.metadata?.avatar_color || '#4f46e5';
    if (colorInput) {
      colorInput.value = colorValue;
      this.updateEditAgentColorPreview(colorValue);
    }

    const favoriteToggle = document.getElementById('editAgentFavoriteToggle');
    if (favoriteToggle) {
      favoriteToggle.checked = Boolean(agent.metadata?.favorite);
    }

    this.editAgentSelectedTags = agent.metadata?.tags || [];
    this.renderEditAgentTags();
  },

  async ensureEditAgentModelOptions() {
    if (this.editAgentModelOptionsLoaded) return;

    // Load and cache providers data
    if (typeof loadAvailableProviders === 'function') {
      this.editAgentProvidersData = await loadAvailableProviders();
    }

    // Set default fallback models if no providers loaded
    if (!this.editAgentProvidersData || this.editAgentProvidersData.length === 0) {
      this.editAgentProvidersData = [{
        name: 'default',
        display_name: 'Default',
        models: [
          { value: 'gpt-5', label: 'gpt-5', type: 'research' },
          { value: 'gpt-5-mini', label: 'gpt-5-mini', type: 'general' },
          { value: 'gpt-5-nano', label: 'gpt-5-nano', type: 'tool-calling' },
          { value: 'gpt-4o', label: 'gpt-4o', type: 'general' },
          { value: 'gpt-4o-mini', label: 'gpt-4o-mini', type: 'tool-calling' },
          { value: 'claude-3-5-sonnet-20241022', label: 'claude-3-5-sonnet', type: 'general' },
          { value: 'claude-3-haiku-20240307', label: 'claude-3-haiku', type: 'tool-calling' },
          { value: 'llama3.2', label: 'llama3.2', type: 'tool-calling' },
          { value: 'mistral', label: 'mistral', type: 'tool-calling' },
          { value: 'codellama', label: 'codellama', type: 'general' }
        ]
      }];
    }

    this.editAgentModelOptionsLoaded = true;
  },

  updateEditAgentModelOptions(agentType, currentModel = null) {
    const select = document.getElementById('editAgentModelSelect');
    if (!select) return;

    // Clear existing options
    select.innerHTML = '<option value="">Select a model...</option>';

    if (!this.editAgentProvidersData) return;

    // Group models by provider
    this.editAgentProvidersData.forEach((provider) => {
      if (!provider.models || provider.models.length === 0) return;

      // Filter models by agent type
      const filteredModels = provider.models.filter((model) => {
        // If model has no type, include it for all agent types
        if (!model.type) return true;
        return model.type === agentType;
      });

      if (filteredModels.length === 0) return;

      // Create optgroup for this provider
      const optgroup = document.createElement('optgroup');
      optgroup.label = provider.display_name || provider.name;

      filteredModels.forEach((model) => {
        const option = document.createElement('option');
        option.value = model.value;
        option.textContent = model.label || model.value;
        if (model.value === currentModel) {
          option.selected = true;
        }
        optgroup.appendChild(option);
      });

      select.appendChild(optgroup);
    });

    // If current model wasn't found in filtered list, add it as a custom option
    if (currentModel && select.value !== currentModel) {
      const customOption = document.createElement('option');
      customOption.value = currentModel;
      customOption.textContent = `${currentModel} (current)`;
      customOption.selected = true;
      select.insertBefore(customOption, select.firstChild.nextSibling);
    }
  },

  ensureEditAgentSelectOption(selectEl, value) {
    if (!selectEl || !value) return;
    const exists = Array.from(selectEl.options).some((option) => option.value === value);
    if (exists) return;
    const option = document.createElement('option');
    option.value = value;
    option.textContent = value;
    selectEl.appendChild(option);
  },

  updateEditAgentColorPreview(color) {
    const preview = document.getElementById('editAgentColorPreview');
    if (preview) {
      preview.style.background = color;
    }
  },

  renderEditAgentTags() {
    const container = document.getElementById('editAgentTagsContainer');
    const input = document.getElementById('editAgentTagsInput');
    if (!container || !input) return;

    container.querySelectorAll('.agent-edit-tag').forEach((tag) => tag.remove());

    this.editAgentSelectedTags.forEach((tag) => {
      const tagEl = document.createElement('span');
      tagEl.className = 'agent-edit-tag';

      const label = document.createElement('span');
      label.textContent = tag;

      const removeBtn = document.createElement('button');
      removeBtn.type = 'button';
      removeBtn.className = 'agent-edit-tag-remove';
      removeBtn.setAttribute('aria-label', `Remove tag ${tag}`);
      removeBtn.textContent = 'x';
      removeBtn.addEventListener('click', (event) => {
        event.stopPropagation();
        this.removeEditAgentTag(tag);
      });

      tagEl.appendChild(label);
      tagEl.appendChild(removeBtn);
      container.insertBefore(tagEl, input);
    });
  },

  addEditAgentTag(tag) {
    const cleanTag = tag.trim();
    if (!cleanTag) return;
    if (!this.editAgentSelectedTags.includes(cleanTag)) {
      this.editAgentSelectedTags.push(cleanTag);
      this.renderEditAgentTags();
    }
  },

  removeEditAgentTag(tag) {
    this.editAgentSelectedTags = this.editAgentSelectedTags.filter((item) => item !== tag);
    this.renderEditAgentTags();
  },

  async saveEditAgentChanges() {
    const nameInput = document.getElementById('editAgentNameInput');
    const typeSelect = document.getElementById('editAgentTypeSelect');
    const roleSelect = document.getElementById('editAgentRoleSelect');
    const modelSelect = document.getElementById('editAgentModelSelect');
    const descriptionInput = document.getElementById('editAgentDescription');
    const colorInput = document.getElementById('editAgentAvatarColor');
    const favoriteToggle = document.getElementById('editAgentFavoriteToggle');

    const newName = nameInput?.value.trim();
    const type = typeSelect?.value;
    const role = roleSelect?.value;
    const model = modelSelect?.value;

    if (!newName) {
      this.showEditAgentError('Name is required.');
      nameInput?.focus();
      return;
    }
    if (!type || !role) {
      this.showEditAgentError('Type and role are required.');
      return;
    }
    if (!model) {
      this.showEditAgentError('Model is required.');
      modelSelect?.focus();
      return;
    }

    const payload = {
      name: newName,
      type,
      role,
      model,
      description: descriptionInput?.value.trim() || '',
      avatar_color: colorInput?.value || '#4f46e5',
      tags: this.editAgentSelectedTags,
      favorite: Boolean(favoriteToggle?.checked)
    };

    const saveBtn = document.getElementById('editAgentSaveBtn');
    const originalText = saveBtn?.innerHTML;
    if (saveBtn) {
      saveBtn.disabled = true;
      saveBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-2" role="status"></span>Saving...';
    }

    try {
      const response = await fetch(`/api/agents/${encodeURIComponent(this.editAgentOriginalName)}`, {
        method: 'PATCH',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify(payload)
      });

      if (!response.ok) {
        let errorMessage = 'Failed to update agent.';
        try {
          const errData = await response.json();
          errorMessage = errData.error || errData.message || errorMessage;
        } catch {
          // ignore parse errors
        }
        throw new Error(errorMessage);
      }

      const data = await response.json();
      const updatedName = data.name || newName || this.editAgentOriginalName;
      const previousName = this.editAgentOriginalName;
      this.editAgentOriginalName = updatedName;

      this.sessions.forEach((session) => {
        if (session.agent_name === previousName) {
          session.agent_name = updatedName;
        }
      });

      this.agentModelCache.delete(previousName);
      if (model) {
        this.agentModelCache.set(updatedName, model);
      }

      this.updateCurrentAgent(updatedName);
      this.renderSessions();
      this.showEditAgentSuccess('Agent updated successfully.');

      const modalEl = document.getElementById('editAgentModal');
      const modal = modalEl ? bootstrap.Modal.getInstance(modalEl) : null;
      setTimeout(() => {
        modal?.hide();
      }, 500);
    } catch (error) {
      console.error('Failed to update agent:', error);
      this.showEditAgentError(error.message || 'Failed to update agent.');
    } finally {
      if (saveBtn) {
        saveBtn.disabled = false;
        saveBtn.innerHTML = originalText;
      }
    }
  },

  setEditAgentLoading(show, message) {
    const loadingEl = document.getElementById('editAgentModalLoading');
    const contentEl = document.getElementById('editAgentModalContent');
    if (loadingEl) {
      loadingEl.classList.toggle('d-none', !show);
      const textEl = loadingEl.querySelector('p');
      if (textEl && message) {
        textEl.textContent = message;
      }
    }
    if (contentEl) {
      contentEl.classList.toggle('d-none', show);
    }
  },

  setEditAgentFormEnabled(enabled) {
    const form = document.getElementById('editAgentForm');
    if (form) {
      form.querySelectorAll('input, select, textarea, button').forEach((element) => {
        element.disabled = !enabled;
      });
    }
    const saveBtn = document.getElementById('editAgentSaveBtn');
    if (saveBtn) {
      saveBtn.disabled = !enabled;
    }
  },

  showEditAgentError(message) {
    const errorEl = document.getElementById('editAgentError');
    const successEl = document.getElementById('editAgentSuccess');
    if (errorEl) {
      errorEl.textContent = message;
      errorEl.classList.remove('d-none');
    }
    if (successEl) {
      successEl.classList.add('d-none');
    }
  },

  showEditAgentSuccess(message) {
    const errorEl = document.getElementById('editAgentError');
    const successEl = document.getElementById('editAgentSuccess');
    if (successEl) {
      successEl.textContent = message;
      successEl.classList.remove('d-none');
    }
    if (errorEl) {
      errorEl.classList.add('d-none');
    }
  },

  clearEditAgentMessages() {
    const errorEl = document.getElementById('editAgentError');
    const successEl = document.getElementById('editAgentSuccess');
    if (errorEl) {
      errorEl.textContent = '';
      errorEl.classList.add('d-none');
    }
    if (successEl) {
      successEl.classList.add('d-none');
    }
  },

  resetEditAgentModal() {
    this.editAgentOriginalName = '';
    this.editAgentSelectedTags = [];
    const form = document.getElementById('editAgentForm');
    if (form) {
      form.reset();
    }
    this.updateEditAgentColorPreview('#4f46e5');
    this.renderEditAgentTags();
    this.clearEditAgentMessages();
    this.setEditAgentLoading(false);
    this.setEditAgentFormEnabled(true);
  },

  // Restore session messages to chat area
  restoreSessionMessages(session) {
    const chatArea = document.getElementById('chatArea');
    if (!chatArea) return;

    chatArea.innerHTML = '';

    const messages = session.messages || [];
    messages.forEach(msg => {
      const isUser = msg.role === 'user';
      if (typeof appendMessageToUI === 'function') {
        appendMessageToUI(msg.content, isUser);
      }
    });
  },

  // Delete session
  async deleteSession(sessionId) {
    if (!confirm('Are you sure you want to delete this session?')) return;

    try {
      const response = await fetch(`/api/sessions/${sessionId}`, {
        method: 'DELETE'
      });

      if (!response.ok) throw new Error('Failed to delete session');

      // Remove from local state
      this.sessions = this.sessions.filter(s => s.id !== sessionId);

      // If deleted session was active, switch to first session or create new
      if (sessionId === this.activeSessionId) {
        if (this.sessions.length > 0) {
          this.switchToSession(this.sessions[0].id);
        } else {
          this.activeSessionId = null;
          this.saveActiveSession();
          if (typeof clearChatHistory === 'function') {
            clearChatHistory();
          }
          this.updateChatInfoBar('', '');
          this.updateChatModelName('');
        }
      }

      this.renderSessions();
    } catch (error) {
      console.error('Failed to delete session:', error);
    }
  },

  // Rename session
  async renameSession(sessionId, newTitle) {
    try {
      const response = await fetch(`/api/sessions/${sessionId}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: newTitle })
      });

      if (!response.ok) throw new Error('Failed to rename session');

      // Update local state
      const session = this.sessions.find(s => s.id === sessionId);
      if (session) {
        session.title = newTitle;
        this.renderSessions();
        if (sessionId === this.activeSessionId) {
          this.updateChatInfoBar(newTitle, session.agent_name);
        }
      }
    } catch (error) {
      console.error('Failed to rename session:', error);
    }
  },

  // Move session to folder
  async moveSessionToFolder(sessionId, folderId) {
    return this.moveSessionsToFolder([sessionId], folderId);
  },

  // Move multiple sessions to folder
  async moveSessionsToFolder(sessionIds, folderId) {
    const ids = Array.from(new Set(sessionIds)).filter(Boolean);
    if (ids.length === 0) return;

    try {
      await Promise.all(ids.map(async (id) => {
        const response = await fetch(`/api/sessions/${id}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ folder_id: folderId })
        });

        if (!response.ok) throw new Error('Failed to move session');
      }));

      // Refresh sessions and folders
      await this.loadSessions();
      await this.loadFolders();
      return true;
    } catch (error) {
      console.error('Failed to move sessions:', error);
      this.flashDragHint('Failed to move sessions');
      return false;
    }
  },

  // Update session tags
  async updateSessionTags(sessionId, tags) {
    try {
      const response = await fetch(`/api/sessions/${sessionId}/tags`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ tags })
      });

      if (!response.ok) throw new Error('Failed to update tags');

      // Update local state
      const session = this.sessions.find(s => s.id === sessionId);
      if (session) {
        session.tags = tags;
        this.renderSessions();
      }

      // Refresh tag list
      await this.loadTags();
    } catch (error) {
      console.error('Failed to update tags:', error);
    }
  },

  // Create folder
  async createFolder() {
    const nameInput = document.getElementById('folderNameInput');
    const descriptionInput = document.getElementById('folderDescriptionInput');
    const colorBtn = document.querySelector('.folder-color-btn.active');

    const name = nameInput?.value.trim();
    if (!name) return;

    const description = descriptionInput?.value.trim() || '';
    const color = colorBtn?.dataset.color || '';

    try {
      const response = await fetch('/api/workspaces', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, description, color })
      });

      if (!response.ok) throw new Error('Failed to create workspace');

      // Close modal
      const modal = bootstrap.Modal.getInstance(document.getElementById('addFolderModal'));
      modal?.hide();

      // Clear inputs
      if (nameInput) nameInput.value = '';
      if (descriptionInput) descriptionInput.value = '';

      // Refresh folders
      await this.loadFolders();
    } catch (error) {
      console.error('Failed to create folder:', error);
    }
  },

  // Delete folder
  async deleteFolder(folderId) {
    if (!confirm('Are you sure you want to delete this workspace? Sessions will be moved to root.')) return;

    try {
      const response = await fetch(`/api/workspaces/${folderId}`, {
        method: 'DELETE'
      });

      if (!response.ok) throw new Error('Failed to delete workspace');

      // Clear active folder if deleted
      if (this.activeFolder === folderId) {
        this.activeFolder = null;
      }

      // Refresh
      await this.loadFolders();
      await this.loadSessions();
    } catch (error) {
      console.error('Failed to delete folder:', error);
    }
  },

  // Handle search input
  handleSearchInput(query) {
    this.searchQuery = query;

    // Show/hide clear button
    const clearBtn = document.getElementById('clearSessionSearch');
    if (clearBtn) {
      clearBtn.style.display = query ? 'flex' : 'none';
    }

    // Debounce search
    if (this.searchDebounceTimer) {
      clearTimeout(this.searchDebounceTimer);
    }

    this.searchDebounceTimer = setTimeout(() => {
      this.loadSessions();
    }, 300);
  },

  // Clear search
  clearSearch() {
    const input = document.getElementById('sessionSearchInput');
    if (input) input.value = '';
    this.handleSearchInput('');
  },

  // Focus search input
  focusSearch() {
    const input = document.getElementById('sessionSearchInput');
    if (input) {
      // On mobile, show sidebar if hidden
      const sidebar = document.getElementById('sessionSidebar');
      if (sidebar && window.innerWidth < 992 && !sidebar.classList.contains('mobile-open')) {
        sidebar.classList.add('mobile-open');
      }
      input.focus();
    }
  },

  // Set sort order
  setSortOrder(sort) {
    this.sortBy = sort;

    // Update UI
    const label = document.getElementById('sortLabel');
    if (label) {
      const sortLabels = { updated_at: 'Recent', created_at: 'Created', title: 'Title' };
      label.textContent = sortLabels[sort] || 'Recent';
    }

    // Update active state
    document.querySelectorAll('#sortDropdown .session-dropdown-item').forEach(item => {
      item.classList.toggle('active', item.dataset.sort === sort);
    });

    this.loadSessions();
  },

  // Filter by folder
  filterByFolder(folderId) {
    if (this.activeFolder === folderId) {
      this.activeFolder = null; // Toggle off
    } else {
      this.activeFolder = folderId;
    }

    // Update folder UI
    document.querySelectorAll('.folder-item').forEach(item => {
      item.classList.toggle('active', item.dataset.folderId === this.activeFolder);
    });

    this.loadSessions();
  },

  // Toggle tag filter
  toggleTagFilter(tag) {
    const index = this.filterTags.indexOf(tag);
    if (index >= 0) {
      this.filterTags.splice(index, 1);
    } else {
      this.filterTags.push(tag);
    }

    // Update filter label
    const label = document.getElementById('filterLabel');
    if (label) {
      label.textContent = this.filterTags.length > 0 ? `${this.filterTags.length} tags` : 'All';
    }

    this.renderTagFilters();
    this.loadSessions();
  },

  // Toggle folder tree visibility
  toggleFolderTree(forceOpen = null) {
    const header = document.getElementById('folderTreeToggle');
    const tree = document.getElementById('folderTree');

    if (header && tree) {
      if (forceOpen === true) {
        header.classList.add('expanded');
        tree.style.display = 'block';
        return;
      }
      if (forceOpen === false) {
        header.classList.remove('expanded');
        tree.style.display = 'none';
        return;
      }

      header.classList.toggle('expanded');
      tree.style.display = tree.style.display === 'none' ? 'block' : 'none';
    }
  },

  // Toggle dropdown
  toggleDropdown(dropdownId) {
    const dropdown = document.getElementById(dropdownId);
    if (dropdown) {
      const isVisible = dropdown.style.display !== 'none';
      this.closeDropdowns();
      if (!isVisible) {
        dropdown.style.display = 'block';
      }
    }
  },

  // Close all dropdowns
  closeDropdowns() {
    document.querySelectorAll('.session-dropdown-menu').forEach(menu => {
      menu.style.display = 'none';
    });
  },

  // Toggle sidebar (for mobile only - desktop sidebar is always visible)
  toggleSidebar() {
    const sidebar = document.getElementById('sessionSidebar');
    if (sidebar) {
      // On mobile (< 992px), toggle the mobile-open class
      if (window.innerWidth < 992) {
        sidebar.classList.toggle('mobile-open');
      }
      // On desktop, sidebar is always visible - no toggle needed
    }
  },

  // Close sidebar on mobile
  closeSidebarMobile() {
    const sidebar = document.getElementById('sessionSidebar');
    if (sidebar && window.innerWidth < 992) {
      sidebar.classList.remove('mobile-open');
    }
  },

  // Show session context menu
  showSessionContextMenu(e, sessionId) {
    this.contextMenuTarget = sessionId;
    this.contextMenuType = 'session';

    const menu = document.getElementById('sessionContextMenu');
    if (menu) {
      menu.style.display = 'block';
      menu.style.left = `${e.pageX}px`;
      menu.style.top = `${e.pageY}px`;

      // Adjust if menu goes off screen
      const rect = menu.getBoundingClientRect();
      if (rect.right > window.innerWidth) {
        menu.style.left = `${e.pageX - rect.width}px`;
      }
      if (rect.bottom > window.innerHeight) {
        menu.style.top = `${e.pageY - rect.height}px`;
      }
    }
  },

  // Show folder context menu
  showFolderContextMenu(e, folderId) {
    this.contextMenuTarget = folderId;
    this.contextMenuType = 'folder';

    const menu = document.getElementById('folderContextMenu');
    if (menu) {
      menu.style.display = 'block';
      menu.style.left = `${e.pageX}px`;
      menu.style.top = `${e.pageY}px`;
    }
  },

  // Hide all context menus
  hideContextMenus() {
    const sessionMenu = document.getElementById('sessionContextMenu');
    const folderMenu = document.getElementById('folderContextMenu');
    const noteMenu = document.getElementById('noteContextMenu');
    if (sessionMenu) sessionMenu.style.display = 'none';
    if (folderMenu) folderMenu.style.display = 'none';
    if (noteMenu) noteMenu.style.display = 'none';
    this.contextMenuTarget = null;
    this.contextMenuType = null;
  },

  // Handle session context menu action
  handleSessionContextAction(action) {
    const sessionId = this.contextMenuTarget;
    if (!sessionId) return;

    this.hideContextMenus();

    switch (action) {
      case 'info':
        this.showSessionInfoModal(sessionId);
        break;
      case 'rename':
        this.startInlineRename(sessionId);
        break;
      case 'move':
        this.showMoveToFolderModal(sessionId);
        break;
      case 'tags':
        this.showEditTagsModal(sessionId);
        break;
      case 'delete':
        this.deleteSession(sessionId);
        break;
    }
  },

  // Handle folder context menu action
  handleFolderContextAction(action) {
    const folderId = this.contextMenuTarget;
    if (!folderId) return;

    this.hideContextMenus();

    switch (action) {
      case 'new-session':
        this.showCreateChatModalForWorkspace(folderId);
        break;
      case 'new-note':
        this.createNewNoteForFolder(folderId);
        break;
      case 'new-task':
        this.openTaskModalForWorkspace(folderId);
        break;
      case 'paste-note':
        this.pasteNoteToFolder(folderId);
        break;
      case 'rename':
        this.startFolderRename(folderId);
        break;
      case 'color':
        this.showFolderColorPicker(folderId);
        break;
      case 'delete':
        this.deleteFolder(folderId);
        break;
    }
  },

  // Start inline rename for session
  startInlineRename(sessionId) {
    const sessionItem = document.querySelector(`.session-item[data-session-id="${sessionId}"]`);
    if (!sessionItem) return;

    const titleSpan = sessionItem.querySelector('.session-title');
    if (!titleSpan) return;

    const currentTitle = titleSpan.textContent;

    // Create input
    const input = document.createElement('input');
    input.type = 'text';
    input.className = 'session-title-input';
    input.value = currentTitle;

    // Replace span with input
    titleSpan.replaceWith(input);
    input.focus();
    input.select();

    // Handle save
    const saveRename = () => {
      const newTitle = input.value.trim() || currentTitle;
      input.replaceWith(titleSpan);
      if (newTitle !== currentTitle) {
        this.renameSession(sessionId, newTitle);
      }
    };

    input.addEventListener('blur', saveRename);
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        input.blur();
      } else if (e.key === 'Escape') {
        input.value = currentTitle;
        input.blur();
      }
    });
  },

  // Start inline rename for folder
  startFolderRename(folderId) {
    const folderItem = document.querySelector(`.folder-item[data-folder-id="${folderId}"]`);
    if (!folderItem) return;

    const nameSpan = folderItem.querySelector('.folder-name');
    if (!nameSpan) return;

    const currentName = nameSpan.textContent;

    // Create input
    const input = document.createElement('input');
    input.type = 'text';
    input.className = 'folder-name-input';
    input.value = currentName;

    // Replace span with input
    nameSpan.replaceWith(input);
    input.focus();
    input.select();

    // Handle save
    const saveRename = async () => {
      const newName = input.value.trim() || currentName;
      input.replaceWith(nameSpan);
      if (newName !== currentName) {
        await this.renameFolder(folderId, newName);
      }
    };

    input.addEventListener('blur', saveRename);
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        input.blur();
      } else if (e.key === 'Escape') {
        input.value = currentName;
        input.blur();
      }
    });
  },

  // Rename folder via API
  async renameFolder(folderId, newName) {
    try {
      const response = await fetch(`/api/workspaces/${folderId}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: newName })
      });

      if (!response.ok) throw new Error('Failed to rename workspace');

      // Update local data
      const folder = this.findFolderById(folderId, this.folders);
      if (folder) {
        folder.name = newName;
      }

      // Re-render
      this.renderFolderTree();
      this.showToast('Workspace renamed', 'success');
    } catch (error) {
      console.error('Failed to rename folder:', error);
      this.showToast('Failed to rename workspace', 'error');
      // Reload to restore correct state
      await this.loadFolders();
    }
  },

  // Show folder color picker
  showFolderColorPicker(folderId) {
    const folder = this.findFolderById(folderId, this.folders);
    if (!folder) return;

    const folderItem = document.querySelector(`.folder-item[data-folder-id="${folderId}"]`);
    if (!folderItem) return;

    // Create color picker popup
    const popup = document.createElement('div');
    popup.className = 'folder-color-picker-popup';
    popup.innerHTML = `
      <div class="folder-color-options">
        <button class="folder-color-option" data-color="" title="Default">
          <svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor">
            <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
          </svg>
        </button>
        <button class="folder-color-option" data-color="#ef4444" style="background: #ef4444;" title="Red"></button>
        <button class="folder-color-option" data-color="#f97316" style="background: #f97316;" title="Orange"></button>
        <button class="folder-color-option" data-color="#eab308" style="background: #eab308;" title="Yellow"></button>
        <button class="folder-color-option" data-color="#22c55e" style="background: #22c55e;" title="Green"></button>
        <button class="folder-color-option" data-color="#3b82f6" style="background: #3b82f6;" title="Blue"></button>
        <button class="folder-color-option" data-color="#8b5cf6" style="background: #8b5cf6;" title="Purple"></button>
        <button class="folder-color-option" data-color="#ec4899" style="background: #ec4899;" title="Pink"></button>
      </div>
    `;

    // Position popup near the folder item
    const rect = folderItem.getBoundingClientRect();
    popup.style.top = `${rect.bottom + 4}px`;
    popup.style.left = `${rect.left}px`;

    document.body.appendChild(popup);

    // Handle color selection
    popup.querySelectorAll('.folder-color-option').forEach(btn => {
      btn.addEventListener('click', async () => {
        const color = btn.dataset.color;
        await this.setFolderColor(folderId, color);
        popup.remove();
      });
    });

    // Close on click outside
    const closePopup = (e) => {
      if (!popup.contains(e.target)) {
        popup.remove();
        document.removeEventListener('click', closePopup);
      }
    };
    setTimeout(() => document.addEventListener('click', closePopup), 0);
  },

  // Set folder color via API
  async setFolderColor(folderId, color) {
    try {
      const response = await fetch(`/api/workspaces/${folderId}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ color: color })
      });

      if (!response.ok) throw new Error('Failed to update workspace color');

      // Update local data
      const folder = this.findFolderById(folderId, this.folders);
      if (folder) {
        folder.color = color;
      }

      // Re-render
      this.renderFolderTree();
    } catch (error) {
      console.error('Failed to set folder color:', error);
      this.showToast('Failed to update color', 'error');
    }
  },

  // Show move to folder modal
  showMoveToFolderModal(sessionId) {
    const container = document.getElementById('moveToFolderList');
    if (!container) return;

    // Find current folder
    const session = this.sessions.find(s => s.id === sessionId);
    const currentFolderId = session?.folder_id;

    // Render folder options
    container.innerHTML = `
      <div class="move-folder-item ${!currentFolderId ? 'selected' : ''}" data-folder-id="">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
          <path d="M19,20H5A2,2 0 0,1 3,18V6A2,2 0 0,1 5,4H9L11,6H19A2,2 0 0,1 21,8V18A2,2 0 0,1 19,20M5,18H19V10H5V18M5,8H9L7,6H5V8Z"/>
        </svg>
        No Workspace
      </div>
      ${this.folders.map(folder => `
        <div class="move-folder-item ${folder.id === currentFolderId ? 'selected' : ''}" data-folder-id="${folder.id}">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="${folder.color || 'currentColor'}" class="me-2">
            <path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>
          </svg>
          ${this.escapeHtml(folder.name)}
        </div>
      `).join('')}
    `;

    // Bind click handlers
    container.querySelectorAll('.move-folder-item').forEach(item => {
      item.addEventListener('click', async () => {
        const folderId = item.dataset.folderId;
        await this.moveSessionToFolder(sessionId, folderId);

        const modal = bootstrap.Modal.getInstance(document.getElementById('moveToFolderModal'));
        modal?.hide();
      });
    });

    // Show modal
    const modal = new bootstrap.Modal(document.getElementById('moveToFolderModal'));
    modal.show();
  },

  // Show edit tags modal
  showEditTagsModal(sessionId) {
    const session = this.sessions.find(s => s.id === sessionId);
    if (!session) return;

    const currentTags = session.tags || [];
    const tagInput = document.getElementById('tagInput');
    const currentTagsContainer = document.getElementById('currentTags');
    const suggestedTagsContainer = document.getElementById('suggestedTags');

    // Clear input
    if (tagInput) tagInput.value = '';

    // Render current tags
    if (currentTagsContainer) {
      currentTagsContainer.innerHTML = currentTags.map(tag => `
        <span class="session-tag-edit" data-color="${this.getTagColor(tag)}">
          ${this.escapeHtml(tag)}
          <button class="tag-remove" data-tag="${this.escapeHtml(tag)}">
            <svg width="8" height="8" viewBox="0 0 24 24" fill="white">
              <path d="M19,6.41L17.59,5L12,10.59L6.41,5L5,6.41L10.59,12L5,17.59L6.41,19L12,13.41L17.59,19L19,17.59L13.41,12L19,6.41Z"/>
            </svg>
          </button>
        </span>
      `).join('');

      // Bind remove handlers
      currentTagsContainer.querySelectorAll('.tag-remove').forEach(btn => {
        btn.addEventListener('click', (e) => {
          e.stopPropagation();
          const tagToRemove = e.currentTarget.dataset.tag;
          const newTags = currentTags.filter(t => t !== tagToRemove);
          this.updateSessionTags(sessionId, newTags);

          // Update UI
          e.currentTarget.parentElement.remove();
        });
      });
    }

    // Render suggested tags (tags not already on this session)
    if (suggestedTagsContainer) {
      const suggestedTags = this.tags.filter(t => !currentTags.includes(t));
      suggestedTagsContainer.innerHTML = suggestedTags.length > 0
        ? suggestedTags.map(tag => `
            <button class="session-tag-suggest" data-tag="${this.escapeHtml(tag)}">+ ${this.escapeHtml(tag)}</button>
          `).join('')
        : '<span class="text-muted small">No suggestions</span>';

      // Bind add handlers
      suggestedTagsContainer.querySelectorAll('.session-tag-suggest').forEach(btn => {
        btn.addEventListener('click', (e) => {
          const tagToAdd = e.currentTarget.dataset.tag;
          const newTags = [...currentTags, tagToAdd];
          this.updateSessionTags(sessionId, newTags);

          // Remove from suggestions, add to current
          e.currentTarget.remove();
        });
      });
    }

    // Setup save button
    const saveBtn = document.getElementById('saveTagsBtn');
    if (saveBtn) {
      saveBtn.onclick = () => {
        const input = document.getElementById('tagInput');
        if (input?.value.trim()) {
          const newTags = input.value.split(',').map(t => t.trim()).filter(t => t);
          const allTags = [...new Set([...currentTags, ...newTags])];
          this.updateSessionTags(sessionId, allTags);
        }

        const modal = bootstrap.Modal.getInstance(document.getElementById('editTagsModal'));
        modal?.hide();
      };
    }

    // Show modal
    const modal = new bootstrap.Modal(document.getElementById('editTagsModal'));
    modal.show();
  },

  // Show add folder modal
  showAddWorkspaceModal() {
    const modal = new bootstrap.Modal(document.getElementById('addFolderModal'));
    modal.show();
  },

  // Show session info modal
  showSessionInfoModal(sessionId) {
    const session = this.sessions.find(s => s.id === sessionId);
    if (!session) return;

    const infoBody = document.getElementById('sessionInfoBody');
    if (!infoBody) return;

    const folderName = this.getFolderName(session.folder_id);
    const tags = session.tags || [];
    const createdAt = this.formatDateTime(session.created_at);
    const updatedAt = this.formatDateTime(session.updated_at);

    infoBody.innerHTML = `
      <div class="session-info-row">
        <span class="session-info-label">Title</span>
        <span class="session-info-value">${this.escapeHtml(session.title || 'New Session')}</span>
      </div>
      <div class="session-info-row">
        <span class="session-info-label">Agent</span>
        <div class="session-info-value d-flex align-items-center gap-2">
          <span id="sessionInfoAgentName">${this.escapeHtml(session.agent_name || 'Unknown')}</span>
          <button type="button" class="btn btn-sm btn-outline-primary" id="changeSessionAgentBtn" data-session-id="${session.id}">
            Change
          </button>
        </div>
      </div>
      <div class="session-info-row">
        <span class="session-info-label">Workspace</span>
        <span class="session-info-value">${this.escapeHtml(folderName)}</span>
      </div>
      <div class="session-info-row">
        <span class="session-info-label">Messages</span>
        <span class="session-info-value">${session.message_count ?? 0}</span>
      </div>
      <div class="session-info-row">
        <span class="session-info-label">Created</span>
        <span class="session-info-value">${this.escapeHtml(createdAt)}</span>
      </div>
      <div class="session-info-row">
        <span class="session-info-label">Updated</span>
        <span class="session-info-value">${this.escapeHtml(updatedAt)}</span>
      </div>
      <div class="session-info-row">
        <span class="session-info-label">Tags</span>
        <span class="session-info-value">${tags.length > 0 ? tags.map(tag => this.escapeHtml(tag)).join(', ') : 'None'}</span>
      </div>
      <div class="session-info-row">
        <span class="session-info-label">ID</span>
        <span class="session-info-value session-info-mono">${this.escapeHtml(session.id)}</span>
      </div>
    `;

    // Bind change agent button
    document.getElementById('changeSessionAgentBtn')?.addEventListener('click', async () => {
      const sessionId = document.getElementById('changeSessionAgentBtn').dataset.sessionId;
      // Close the info modal first
      bootstrap.Modal.getInstance(document.getElementById('sessionInfoModal'))?.hide();
      // Show agent picker for changing
      await this.showChangeAgentDialog(sessionId);
    });

    const modal = new bootstrap.Modal(document.getElementById('sessionInfoModal'));
    modal.show();
  },

  // Show dialog to change session's agent
  async showChangeAgentDialog(sessionId) {
    const session = this.sessions.find(s => s.id === sessionId);
    if (!session) return;

    const agents = await this.fetchAgents();
    if (!agents || agents.length === 0) return;

    // Remove existing modal if any
    const existingModal = document.getElementById('changeAgentModal');
    if (existingModal) existingModal.remove();

    const modalHtml = `
      <div class="modal fade" id="changeAgentModal" tabindex="-1">
        <div class="modal-dialog modal-dialog-centered">
          <div class="modal-content bg-dark text-light">
            <div class="modal-header border-secondary">
              <h5 class="modal-title">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                  <path d="M12,2A2,2 0 0,1 14,4C14,4.74 13.6,5.39 13,5.73V7H14A7,7 0 0,1 21,14H22A1,1 0 0,1 23,15V18A1,1 0 0,1 22,19H21V20A2,2 0 0,1 19,22H5A2,2 0 0,1 3,20V19H2A1,1 0 0,1 1,18V15A1,1 0 0,1 2,14H3A7,7 0 0,1 10,7H11V5.73C10.4,5.39 10,4.74 10,4A2,2 0 0,1 12,2M7.5,13A2.5,2.5 0 0,0 5,15.5A2.5,2.5 0 0,0 7.5,18A2.5,2.5 0 0,0 10,15.5A2.5,2.5 0 0,0 7.5,13M16.5,13A2.5,2.5 0 0,0 14,15.5A2.5,2.5 0 0,0 16.5,18A2.5,2.5 0 0,0 19,15.5A2.5,2.5 0 0,0 16.5,13Z"/>
                </svg>
                Change Session Agent
              </h5>
              <button type="button" class="btn-close btn-close-white" data-bs-dismiss="modal"></button>
            </div>
            <div class="modal-body">
              <p class="text-muted small mb-3">Select a new agent for this session. Future messages will use the selected agent.</p>
              <div class="list-group list-group-flush">
                ${agents.map(agent => `
                  <button type="button" class="list-group-item list-group-item-action bg-dark text-light border-secondary change-agent-item ${agent.name === session.agent_name ? 'active' : ''}"
                          data-agent-name="${this.escapeHtml(agent.name)}">
                    <div class="d-flex justify-content-between align-items-center">
                      <div>
                        <strong>${this.escapeHtml(agent.name)}</strong>
                        ${agent.model ? `<small class="text-muted ms-2">${this.escapeHtml(agent.model)}</small>` : ''}
                      </div>
                      ${agent.name === session.agent_name ? '<span class="badge bg-success">Current</span>' : ''}
                    </div>
                  </button>
                `).join('')}
              </div>
            </div>
            <div class="modal-footer border-secondary">
              <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">Cancel</button>
            </div>
          </div>
        </div>
      </div>
    `;

    document.body.insertAdjacentHTML('beforeend', modalHtml);

    const modal = new bootstrap.Modal(document.getElementById('changeAgentModal'));

    // Handle agent selection
    document.querySelectorAll('.change-agent-item').forEach(item => {
      item.addEventListener('click', async () => {
        const newAgentName = item.dataset.agentName;
        if (newAgentName === session.agent_name) {
          modal.hide();
          return;
        }

        modal.hide();
        await this.changeSessionAgent(sessionId, newAgentName);
      });
    });

    // Cleanup on modal hidden
    document.getElementById('changeAgentModal').addEventListener('hidden.bs.modal', () => {
      document.getElementById('changeAgentModal')?.remove();
    });

    modal.show();
  },

  // Change a session's agent
  async changeSessionAgent(sessionId, newAgentName) {
    try {
      const response = await fetch(`/api/sessions/${sessionId}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ agent_name: newAgentName })
      });

      if (!response.ok) throw new Error('Failed to update session');

      const data = await response.json();
      if (data.session) {
        // Update local session data
        const index = this.sessions.findIndex(s => s.id === sessionId);
        if (index >= 0) {
          this.sessions[index] = data.session;
        }

        // Re-render sessions
        this.renderSessions();

        // If this is the active session, update the agent display
        if (sessionId === this.activeSessionId) {
          this.updateCurrentAgent(newAgentName);
        }

        console.log(`Session ${sessionId} agent changed to ${newAgentName}`);
      }
    } catch (error) {
      console.error('Failed to change session agent:', error);
      alert('Failed to change session agent');
    }
  },

  // Update folder drag hint text
  setDragHint(text) {
    const hint = document.getElementById('folderDragHint');
    if (!hint) return;
    if (!text) {
      hint.style.display = 'none';
      hint.textContent = '';
      return;
    }
    hint.textContent = text;
    hint.style.display = 'inline-flex';
  },

  // Flash drag hint for a short duration
  flashDragHint(text) {
    this.setDragHint(text);
    clearTimeout(this.dragHintTimer);
    this.dragHintTimer = setTimeout(() => this.setDragHint(''), 1600);
  },

  // Build hint text for a move action
  formatMoveHint(count, folderName = null) {
    const label = count === 1 ? 'session' : 'sessions';
    if (!folderName) return `Drop to move ${count} ${label}`;
    return `Moved ${count} ${label} to ${folderName}`;
  },

  // Setup resize handle
  setupResizeHandle() {
    const handle = document.getElementById('sessionSidebarResizeHandle');
    const sidebar = document.getElementById('sessionSidebar');
    if (!handle || !sidebar) return;

    let isResizing = false;
    let startX = 0;
    let startWidth = 0;

    handle.addEventListener('mousedown', (e) => {
      isResizing = true;
      startX = e.clientX;
      startWidth = sidebar.offsetWidth;
      handle.classList.add('resizing');
      document.body.style.cursor = 'col-resize';
      document.body.style.userSelect = 'none';
    });

    document.addEventListener('mousemove', (e) => {
      if (!isResizing) return;

      const width = startWidth + (e.clientX - startX);
      const clampedWidth = Math.max(200, Math.min(400, width));

      document.documentElement.style.setProperty('--session-sidebar-width', `${clampedWidth}px`);
      localStorage.setItem('sessionSidebarWidth', clampedWidth);
    });

    document.addEventListener('mouseup', () => {
      if (isResizing) {
        isResizing = false;
        handle.classList.remove('resizing');
        document.body.style.cursor = '';
        document.body.style.userSelect = '';
      }
    });

    // Restore saved width
    const savedWidth = localStorage.getItem('sessionSidebarWidth');
    if (savedWidth) {
      document.documentElement.style.setProperty('--session-sidebar-width', `${savedWidth}px`);
    }
  },

  // Handle drag start
  handleDragStart(e, sessionId) {
    if (!this.selectedSessionIds.includes(sessionId)) {
      this.setSelection([sessionId], this.getSessionIndex(sessionId));
    }
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('application/x-ori-session-ids', JSON.stringify(this.selectedSessionIds));
    e.dataTransfer.setData('text/plain', String(sessionId));
    e.currentTarget.classList.add('dragging');
    document.getElementById('folderTreeSection')?.classList.add('dragging');
    this.setDragHint(this.formatMoveHint(this.selectedSessionIds.length));
    const tree = document.getElementById('folderTree');
    const isCollapsed = tree && tree.style.display === 'none';
    this.folderTreeWasCollapsedOnDrag = isCollapsed;
    if (isCollapsed) {
      this.toggleFolderTree(true);
    }
  },

  // Handle drag end
  handleDragEnd() {
    document.querySelectorAll('.session-item').forEach(item => {
      item.classList.remove('dragging');
    });
    document.querySelectorAll('.folder-item').forEach(item => {
      item.classList.remove('drag-over');
    });
    document.getElementById('folderTreeSection')?.classList.remove('dragging');
    this.setDragHint('');
    if (this.folderTreeWasCollapsedOnDrag) {
      this.toggleFolderTree(false);
      this.folderTreeWasCollapsedOnDrag = false;
    }
  },

  // Handle session click with multi-select support
  handleSessionClick(e, sessionId) {
    const clickedIndex = this.getSessionIndex(sessionId);

    if (e.shiftKey && this.lastSelectedIndex !== null && clickedIndex !== -1) {
      this.selectRange(this.lastSelectedIndex, clickedIndex, e.ctrlKey || e.metaKey);
      return;
    }

    if (e.ctrlKey || e.metaKey) {
      this.toggleSelection(sessionId, clickedIndex);
      return;
    }

    this.setSelection([sessionId], clickedIndex);
    this.switchToSession(sessionId);
  },

  // Keep selection aligned to current session list
  reconcileSelection() {
    const sessionIds = new Set(this.sessions.map(session => session.id));
    this.selectedSessionIds = this.selectedSessionIds.filter(id => sessionIds.has(id));
    if (this.selectedSessionIds.length === 0) {
      this.lastSelectedIndex = null;
      return;
    }
    const lastId = this.selectedSessionIds[this.selectedSessionIds.length - 1];
    this.lastSelectedIndex = this.getSessionIndex(lastId);
  },

  // Update selection UI
  updateSelectionUI() {
    const selected = new Set(this.selectedSessionIds);
    document.querySelectorAll('.session-item').forEach(item => {
      item.classList.toggle('selected', selected.has(item.dataset.sessionId));
    });
  },

  // Clear selection
  clearSelection() {
    this.selectedSessionIds = [];
    this.lastSelectedIndex = null;
    this.updateSelectionUI();
  },

  // Set selection explicitly
  setSelection(sessionIds, lastIndex) {
    this.selectedSessionIds = Array.from(new Set(sessionIds));
    this.lastSelectedIndex = Number.isInteger(lastIndex) ? lastIndex : null;
    this.updateSelectionUI();
  },

  // Toggle a single session in selection
  toggleSelection(sessionId, index) {
    const existingIndex = this.selectedSessionIds.indexOf(sessionId);
    if (existingIndex >= 0) {
      this.selectedSessionIds.splice(existingIndex, 1);
      if (this.selectedSessionIds.length === 0) {
        this.lastSelectedIndex = null;
      }
    } else {
      this.selectedSessionIds.push(sessionId);
      this.lastSelectedIndex = Number.isInteger(index) ? index : this.lastSelectedIndex;
    }
    this.updateSelectionUI();
  },

  // Select a range between two indices
  selectRange(fromIndex, toIndex, additive) {
    if (fromIndex === null || toIndex === null) return;
    const start = Math.min(fromIndex, toIndex);
    const end = Math.max(fromIndex, toIndex);
    const rangeIds = this.sessions.slice(start, end + 1).map(session => session.id);
    if (additive) {
      const combined = new Set([...this.selectedSessionIds, ...rangeIds]);
      this.setSelection(Array.from(combined), toIndex);
      return;
    }
    this.setSelection(rangeIds, toIndex);
  },

  // Get session index by id
  getSessionIndex(sessionId) {
    return this.sessions.findIndex(session => session.id === sessionId);
  },

  // Get dragged session IDs from data transfer
  getDraggedSessionIds(e) {
    const payload = e.dataTransfer.getData('application/x-ori-session-ids');
    if (payload) {
      try {
        const parsed = JSON.parse(payload);
        if (Array.isArray(parsed)) {
          return parsed;
        }
      } catch (error) {
        console.warn('Failed to parse dragged session ids', error);
      }
    }
    const single = e.dataTransfer.getData('text/plain');
    return single ? [single] : [];
  },

  // Handle scroll (for virtual scrolling)
  handleScroll() {
    if (!this.virtualScrollEnabled) return;
    // Virtual scroll implementation would go here
  },

  // Save active session to localStorage (tab-specific for multi-tab support)
  saveActiveSession() {
    const tabKey = `activeSessionId_${this.tabId}`;
    if (this.activeSessionId) {
      // Use sessionStorage for tab-specific isolation
      sessionStorage.setItem(tabKey, this.activeSessionId);
      // Also save to localStorage for backward compatibility
      localStorage.setItem('activeSessionId', this.activeSessionId);
    } else {
      sessionStorage.removeItem(tabKey);
      localStorage.removeItem('activeSessionId');
    }
  },

  // Get the active session object
  getActiveSession() {
    if (!this.activeSessionId) return null;
    return this.sessions.find(s => s.id === this.activeSessionId) || null;
  },

  // Restore active session from localStorage
  // Returns true if session was restored, false otherwise
  async restoreActiveSession() {
    // Use tab-specific key for multi-tab support
    const tabKey = `activeSessionId_${this.tabId}`;
    let savedId = sessionStorage.getItem(tabKey);

    // Fallback to legacy localStorage for backward compatibility
    if (!savedId) {
      savedId = localStorage.getItem('activeSessionId');
    }

    const session = savedId ? this.sessions.find(s => s.id === savedId) : null;
    if (session) {
      this.activeSessionId = savedId;
      this.renderSessions();

      // Update the combined chat info bar with session title and agent
      this.updateChatInfoBar(session.title || 'New Session', session.agent_name);

      // Update the current agent globally
      if (session.agent_name) {
        this.updateCurrentAgent(session.agent_name);
      }

      // Initialize session files panel
      this.initializeSessionFiles(savedId);

      // Load tasks for the restored session
      await this.loadSessionTasks();

      return true;
    }
    return false;
  },

  // Refresh the active session's data (called after chat messages)
  async refreshActiveSession() {
    if (!this.activeSessionId) return;

    try {
      // Just update the session's updated_at and preview in the list
      const response = await fetch(`/api/sessions/${this.activeSessionId}`);
      if (!response.ok) return;

      const session = await response.json();

      // Update the session in our local list
      const index = this.sessions.findIndex(s => s.id === this.activeSessionId);
      if (index >= 0) {
        this.sessions[index] = session;
        // Move to top if sorted by updated_at
        if (this.sortBy === 'updated_at' && index > 0) {
          this.sessions.splice(index, 1);
          this.sessions.unshift(session);
        }
        this.renderSessions();
      }
    } catch (error) {
      console.error('Failed to refresh active session:', error);
    }
  },

  // Start polling for session list updates
  startPolling() {
    if (this.pollTimer) return;

    this.pollTimer = setInterval(() => {
      this.loadSessions();
    }, this.pollInterval);
  },

  // Stop polling
  stopPolling() {
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
  },

  // Called when a message is sent to refresh the session list
  onMessageSent() {
    // Debounce to avoid too frequent updates
    if (this.messageDebounceTimer) {
      clearTimeout(this.messageDebounceTimer);
    }
    this.messageDebounceTimer = setTimeout(() => {
      this.refreshActiveSession();
      // Check if active session needs auto-classification
      this.checkAutoClassification();
    }, 500);
  },

  // Track message count for auto-mode sessions
  autoModeMessageCounts: new Map(),

  // Check if the active session needs auto-classification
  async checkAutoClassification() {
    const sessionId = this.activeSessionId;
    if (!sessionId) return;

    // Only check sessions that are in auto mode
    if (!this.autoModeSessionIds.has(sessionId)) return;

    // Increment message count for this session
    const currentCount = (this.autoModeMessageCounts.get(sessionId) || 0) + 1;
    this.autoModeMessageCounts.set(sessionId, currentCount);

    // Trigger classification after 3 message exchanges (6 messages: 3 user + 3 assistant)
    // This gives the AI enough context to understand the conversation
    if (currentCount >= 3) {
      console.log('Triggering auto-classification for session:', sessionId);
      await this.triggerAutoClassification(sessionId);
    }
  },

  // Trigger auto-classification for a session
  async triggerAutoClassification(sessionId) {
    try {
      const response = await fetch('/api/sessions/auto-classify', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_id: sessionId })
      });

      if (!response.ok) {
        console.error('Auto-classify failed:', response.statusText);
        return;
      }

      const result = await response.json();
      console.log('Auto-classification result:', result);

      if (result.applied) {
        // Remove from auto-mode tracking
        this.autoModeSessionIds.delete(sessionId);
        this.autoModeMessageCounts.delete(sessionId);

        // Refresh session list to show updated workspace/agent
        await this.loadSessions();

        // Show a notification
        if (window.Toast) {
          let message = 'Chat auto-assigned';
          if (result.workspace_name) {
            message += ` to workspace "${result.workspace_name}"`;
          }
          if (result.agent_name) {
            message += result.workspace_name ? ` with agent "${result.agent_name}"` : ` to agent "${result.agent_name}"`;
          }
          Toast.success(message);
        }

        // Update the chat info bar if the agent changed
        if (result.agent_name) {
          const session = this.sessions.find(s => s.id === sessionId);
          if (session) {
            this.updateChatInfoBar(session.title || 'Chat', result.agent_name);
            this.updateCurrentAgent(result.agent_name);
          }
        }
      } else if (result.reasoning) {
        console.log('Auto-classification not applied:', result.reasoning);
        // After 3 attempts without a match, stop trying
        const count = this.autoModeMessageCounts.get(sessionId) || 0;
        if (count >= 6) {
          this.autoModeSessionIds.delete(sessionId);
          this.autoModeMessageCounts.delete(sessionId);
          console.log('Auto-classification attempts exhausted for session:', sessionId);
        }
      }
    } catch (error) {
      console.error('Auto-classify error:', error);
    }
  },

  // Format time ago
  formatTimeAgo(dateString) {
    if (!dateString) return '';

    const date = new Date(dateString);
    const now = new Date();
    const seconds = Math.floor((now - date) / 1000);

    if (seconds < 60) return 'now';
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
    if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
    if (seconds < 604800) return `${Math.floor(seconds / 86400)}d`;
    if (seconds < 2592000) return `${Math.floor(seconds / 604800)}w`;

    return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
  },

  // Get tag color based on hash
  getTagColor(tag) {
    let hash = 0;
    for (let i = 0; i < tag.length; i++) {
      hash = tag.charCodeAt(i) + ((hash << 5) - hash);
    }
    return Math.abs(hash) % 8;
  },

  // Escape HTML
  escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  },

  // Convert hex color to rgba string
  hexToRgba(hex, alpha) {
    if (!hex || typeof hex !== 'string') return '';
    const cleaned = hex.replace('#', '').trim();
    if (cleaned.length !== 6) return '';
    const r = parseInt(cleaned.slice(0, 2), 16);
    const g = parseInt(cleaned.slice(2, 4), 16);
    const b = parseInt(cleaned.slice(4, 6), 16);
    return `rgba(${r}, ${g}, ${b}, ${alpha})`;
  },

  // Format a readable date/time
  formatDateTime(dateString) {
    if (!dateString) return '';
    const date = new Date(dateString);
    return date.toLocaleString('en-US', {
      month: 'short',
      day: 'numeric',
      year: 'numeric',
      hour: 'numeric',
      minute: '2-digit'
    });
  },

  // Find folder name by id
  getFolderName(folderId) {
    if (!folderId) return 'No Workspace';
    const folder = this.findFolderById(folderId, this.folders);
    return folder?.name || 'Unknown';
  },

  // Recursive folder lookup
  findFolderById(folderId, folders) {
    for (const folder of folders || []) {
      if (folder.id === folderId) return folder;
      const match = this.findFolderById(folderId, folder.children || []);
      if (match) return match;
    }
    return null;
  },

  // ============================================================================
  // Utilities
  // ============================================================================

  // Show toast notification
  showToast(message, type = 'info') {
    // Use global showToast if available, otherwise console log
    if (typeof window.showToast === 'function') {
      window.showToast(message, type);
    } else {
      console.log(`[${type.toUpperCase()}] ${message}`);
    }
  },

  // ============================================================================
  // Folder Notes
  // ============================================================================

  // Notes state (keyed by folder ID)
  notesByFolder: new Map(),
  currentNote: null,
  isNotePreviewMode: false,

  // Load notes for a folder
  async loadFolderNotes(folderId) {
    try {
      const response = await fetch(`/api/workspaces/${folderId}/notes`);
      if (!response.ok) throw new Error('Failed to load notes');
      const data = await response.json();
      this.notesByFolder.set(folderId, data.notes || []);
      return data.notes || [];
    } catch (error) {
      console.error('Failed to load folder notes:', error);
      return [];
    }
  },

  // Create a file reference note from a dropped file
  async createFileReferenceNote(folderId, file) {
    const name = `📎 ${file.name}`;
    const sizeKB = (file.size / 1024).toFixed(1);
    const sizeStr = file.size > 1024 * 1024
      ? `${(file.size / (1024 * 1024)).toFixed(1)} MB`
      : `${sizeKB} KB`;

    // Upload file to server
    let serverPath = '';
    try {
      const base64 = await this.fileToBase64(file);
      const response = await fetch('/api/files/upload', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          filename: file.name,
          content: base64,
          mime_type: file.type || 'application/octet-stream'
        })
      });
      const result = await response.json();
      if (result.error) {
        throw new Error(result.error);
      }
      serverPath = result.path;
    } catch (e) {
      console.error('Failed to upload file:', e);
      this.showToast('Failed to upload file', 'error');
      return null;
    }

    let content = `**File:** ${file.name}\n**Size:** ${sizeStr}\n**Type:** ${file.type || 'unknown'}\n**Path:** ${serverPath}`;

    const textExtensions = ['txt', 'md', 'json', 'xml', 'html', 'css', 'js', 'ts', 'csv', 'yaml', 'yml'];
    const ext = file.name.split('.').pop()?.toLowerCase();

    // For text files, include readable preview
    if (textExtensions.includes(ext) && file.size < 50 * 1024) { // Under 50KB
      try {
        const text = await file.text();
        const preview = text.length > 2000 ? text.substring(0, 2000) + '\n...' : text;
        content += `\n\n---\n\n\`\`\`${ext}\n${preview}\n\`\`\``;
      } catch (e) {
        console.warn('Could not read file content:', e);
      }
    }

    // Store server path for later retrieval
    content += `\n\n<!-- FILE_PATH:${serverPath} -->`;

    return this.createNote(folderId, name, content);
  },

  // Convert file to base64
  fileToBase64(file) {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => {
        const base64 = reader.result.split(',')[1];
        resolve(base64);
      };
      reader.onerror = reject;
      reader.readAsDataURL(file);
    });
  },

  // Create a new note in a folder
  async createNote(folderId, name = 'Untitled Note', content = '') {
    try {
      const response = await fetch(`/api/workspaces/${folderId}/notes`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, content })
      });
      if (!response.ok) throw new Error('Failed to create note');
      const data = await response.json();

      // Add to local cache
      const notes = this.notesByFolder.get(folderId) || [];
      notes.unshift(data.note);
      this.notesByFolder.set(folderId, notes);

      // Refresh folder tree to show new note
      this.renderFolderTree();

      return data.note;
    } catch (error) {
      console.error('Failed to create note:', error);
      this.showToast('Failed to create note', 'error');
      return null;
    }
  },

  // Update a note
  async updateNote(noteId, updates) {
    try {
      const response = await fetch(`/api/notes/${noteId}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(updates)
      });
      if (!response.ok) throw new Error('Failed to update note');
      const data = await response.json();

      // Update local cache
      for (const [folderId, notes] of this.notesByFolder) {
        const index = notes.findIndex(n => n.id === noteId);
        if (index !== -1) {
          notes[index] = { ...notes[index], ...data.note };
          break;
        }
      }

      return data.note;
    } catch (error) {
      console.error('Failed to update note:', error);
      this.showToast('Failed to save note', 'error');
      return null;
    }
  },

  // Delete a note
  async deleteNote(noteId) {
    try {
      const response = await fetch(`/api/notes/${noteId}`, {
        method: 'DELETE'
      });
      if (!response.ok) throw new Error('Failed to delete note');

      // Remove from local cache
      for (const [folderId, notes] of this.notesByFolder) {
        const index = notes.findIndex(n => n.id === noteId);
        if (index !== -1) {
          notes.splice(index, 1);
          break;
        }
      }

      // Refresh folder tree
      this.renderFolderTree();

      this.showToast('Note deleted', 'success');
    } catch (error) {
      console.error('Failed to delete note:', error);
      this.showToast('Failed to delete note', 'error');
    }
  },

  // Move a note to a different folder
  async moveNoteToFolder(noteId, targetFolderId) {
    try {
      const response = await fetch(`/api/notes/${noteId}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ folder_id: targetFolderId })
      });
      if (!response.ok) throw new Error('Failed to move note');

      // Refresh folder data
      await this.loadFolders();
      return true;
    } catch (error) {
      console.error('Failed to move note:', error);
      this.showToast('Failed to move note', 'error');
      return false;
    }
  },

  // Copy note to clipboard
  copyNoteToClipboard(noteId, folderId) {
    this.clipboardNote = { noteId, folderId };
    this.clipboardAction = 'copy';
    this.showToast('Note copied', 'info');
  },

  // Cut note to clipboard
  cutNoteToClipboard(noteId, folderId) {
    this.clipboardNote = { noteId, folderId };
    this.clipboardAction = 'cut';
    this.showToast('Note cut', 'info');
  },

  // Paste note from clipboard to folder
  async pasteNoteToFolder(targetFolderId) {
    if (!this.clipboardNote) {
      this.showToast('Nothing to paste', 'error');
      return;
    }

    const { noteId, folderId: sourceFolderId } = this.clipboardNote;

    if (this.clipboardAction === 'cut') {
      // Move the note
      if (sourceFolderId !== targetFolderId) {
        await this.moveNoteToFolder(noteId, targetFolderId);
      }
      this.clipboardNote = null;
      this.clipboardAction = null;
    } else {
      // Copy - create a duplicate
      try {
        // Get the original note
        const note = await this.getNote(noteId);
        if (!note) throw new Error('Note not found');

        // Create a copy in the target folder
        await this.createNote(targetFolderId, `${note.name} (copy)`, note.content);
        this.showToast('Note pasted', 'success');
      } catch (error) {
        console.error('Failed to paste note:', error);
        this.showToast('Failed to paste note', 'error');
      }
    }
  },

  // Get a note by ID
  async getNote(noteId) {
    try {
      const response = await fetch(`/api/notes/${noteId}`);
      if (!response.ok) throw new Error('Failed to get note');
      return await response.json();
    } catch (error) {
      console.error('Failed to get note:', error);
      return null;
    }
  },

  // Render notes in a folder's tree item
  renderFolderNotes(folderId) {
    const notes = this.notesByFolder.get(folderId) || [];
    if (notes.length === 0) return '';

    return `
      <div class="folder-notes-section">
        ${notes.map(note => `
          <div class="folder-note-item" data-note-id="${note.id}" data-folder-id="${folderId}" draggable="true">
            <svg class="folder-note-icon" width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
              <path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13,9V3.5L18.5,9H13"/>
            </svg>
            <span class="folder-note-name">${this.escapeHtml(note.name)}</span>
          </div>
        `).join('')}
      </div>
    `;
  },

  // Clipboard for copy/paste
  clipboardNote: null,
  clipboardAction: null, // 'copy' or 'cut'

  // Bind note events
  bindNoteEvents() {
    // Note click to open editor (folder tree items)
    document.addEventListener('click', (e) => {
      const noteItem = e.target.closest('.folder-note-item');
      if (noteItem) {
        e.preventDefault();
        e.stopPropagation();
        const noteId = noteItem.dataset.noteId;
        this.openNoteEditor(noteId);
      }

      // Search result note click
      const searchNoteItem = e.target.closest('.search-note-result');
      if (searchNoteItem) {
        e.preventDefault();
        e.stopPropagation();
        const noteId = searchNoteItem.dataset.noteId;
        this.openNoteEditor(noteId);
      }

    });

    // Note context menu
    document.addEventListener('contextmenu', (e) => {
      const noteItem = e.target.closest('.folder-note-item');
      if (noteItem) {
        e.preventDefault();
        e.stopPropagation();
        const noteId = noteItem.dataset.noteId;
        const folderId = noteItem.dataset.folderId;
        this.showNoteContextMenu(e, noteId, folderId);
      }
    });

    // Note context menu actions
    document.querySelectorAll('#noteContextMenu .session-context-item').forEach(item => {
      item.addEventListener('click', (e) => {
        e.stopPropagation();
        const action = e.currentTarget.dataset.action;
        this.handleNoteContextAction(action);
      });
    });

    // Note editor save button
    document.getElementById('saveNoteBtn')?.addEventListener('click', () => this.saveCurrentNote());

    // Note preview toggle
    document.getElementById('notePreviewToggle')?.addEventListener('click', () => this.toggleNotePreview());

    // Note drag start
    document.addEventListener('dragstart', (e) => {
      const noteItem = e.target.closest('.folder-note-item');
      if (noteItem) {
        const noteId = noteItem.dataset.noteId;
        const folderId = noteItem.dataset.folderId;
        e.dataTransfer.effectAllowed = 'copyMove';
        e.dataTransfer.setData('application/x-ori-note-id', noteId);
        e.dataTransfer.setData('application/x-ori-note-folder', folderId);
        e.dataTransfer.setData('text/plain', noteId);
        noteItem.classList.add('dragging');
      }
    });

    // Note drag end
    document.addEventListener('dragend', (e) => {
      const noteItem = e.target.closest('.folder-note-item');
      if (noteItem) {
        noteItem.classList.remove('dragging');
        document.querySelectorAll('.folder-item').forEach(f => f.classList.remove('drag-over'));
      }
    });
  },

  // Show note context menu
  showNoteContextMenu(event, noteId, folderId) {
    this.hideContextMenus();
    this.contextMenuTarget = noteId;
    this.contextMenuFolderId = folderId;
    this.contextMenuType = 'note';

    const menu = document.getElementById('noteContextMenu');
    if (!menu) return;

    menu.style.display = 'block';
    menu.style.left = `${event.clientX}px`;
    menu.style.top = `${event.clientY}px`;

    // Ensure menu stays in viewport
    const rect = menu.getBoundingClientRect();
    if (rect.right > window.innerWidth) {
      menu.style.left = `${window.innerWidth - rect.width - 10}px`;
    }
    if (rect.bottom > window.innerHeight) {
      menu.style.top = `${window.innerHeight - rect.height - 10}px`;
    }
  },

  // Handle note context menu action
  handleNoteContextAction(action) {
    const noteId = this.contextMenuTarget;
    const folderId = this.contextMenuFolderId;
    this.hideContextMenus();

    switch (action) {
      case 'edit':
        this.openNoteEditor(noteId);
        break;
      case 'attach':
        this.attachNoteFileToChat(noteId);
        break;
      case 'rename':
        this.promptRenameNote(noteId);
        break;
      case 'copy':
        this.copyNoteToClipboard(noteId, folderId);
        break;
      case 'cut':
        this.cutNoteToClipboard(noteId, folderId);
        break;
      case 'delete':
        this.confirmDeleteNote(noteId);
        break;
    }
  },

  // Attach file from note to chat
  async attachNoteFileToChat(noteId) {
    const note = await this.getNote(noteId);
    if (!note || !note.content) {
      this.showToast('No file data found in this note', 'error');
      return;
    }

    console.log('Note content for attach:', note.content.substring(note.content.length - 200));

    // Try new format first: FILE_PATH (server storage)
    const filePathMatch = note.content.match(/<!-- FILE_PATH:(.+?) -->/);
    if (filePathMatch) {
      const [, serverPath] = filePathMatch;
      console.log('Found FILE_PATH:', serverPath);
      await this.attachFileFromServer(serverPath);
      return;
    }

    // Fall back to old format: FILE_DATA (base64 in note)
    const fileDataMatch = note.content.match(/<!-- FILE_DATA:(.+?):([^:]*):([A-Za-z0-9+/=]+) -->/);
    if (fileDataMatch) {
      const [, fileName, mimeType] = fileDataMatch;
      console.log('Found FILE_DATA:', fileName, mimeType);
      this.attachFileDirectly(fileName, mimeType, fileDataMatch[3]);
      return;
    }

    this.showToast('No attached file found in this note. Re-drop the file to enable this feature.', 'error');
  },

  // Attach file from server storage
  async attachFileFromServer(serverPath) {
    try {
      const response = await fetch(`/api/files/content?path=${encodeURIComponent(serverPath)}`);
      const result = await response.json();

      if (result.error) {
        this.showToast('Failed to load file: ' + result.error, 'error');
        return;
      }

      if (typeof window.addFileToUpload === 'function') {
        const success = window.addFileToUpload({
          name: result.filename,
          type: result.mime_type || 'application/octet-stream',
          size: Math.round(result.content.length * 0.75),
          content: result.content
        });
        if (success) {
          this.showToast(`Attached "${result.filename}" to chat`, 'success');
        } else {
          this.showToast('Chat input not available on this page', 'error');
        }
      } else {
        this.showToast('Chat not available', 'error');
      }
    } catch (e) {
      console.error('Failed to fetch file:', e);
      this.showToast('Failed to load file', 'error');
    }
  },

  // Attach file directly from base64 content (legacy format)
  attachFileDirectly(fileName, mimeType, base64Content) {
    if (typeof window.addFileToUpload === 'function') {
      const success = window.addFileToUpload({
        name: fileName,
        type: mimeType || 'application/octet-stream',
        size: Math.round(base64Content.length * 0.75),
        content: base64Content
      });
      if (success) {
        this.showToast(`Attached "${fileName}" to chat`, 'success');
      } else {
        this.showToast('Chat input not available on this page', 'error');
      }
    } else {
      this.showToast('Chat not available', 'error');
    }
  },

  // Open note editor modal
  async openNoteEditor(noteId) {
    const note = await this.getNote(noteId);
    if (!note) return;

    this.currentNote = note;
    this.isNotePreviewMode = false;

    const modal = document.getElementById('noteEditorModal');
    const nameInput = document.getElementById('noteNameInput');
    const contentInput = document.getElementById('noteContentInput');
    const previewContent = document.getElementById('notePreviewContent');
    const previewToggle = document.getElementById('notePreviewToggle');
    const lastSaved = document.getElementById('noteLastSaved');

    if (nameInput) nameInput.value = note.name;
    if (contentInput) {
      contentInput.value = note.content;
      contentInput.style.display = 'block';
    }
    if (previewContent) previewContent.style.display = 'none';
    if (previewToggle) previewToggle.classList.remove('active');
    if (lastSaved) {
      lastSaved.textContent = `Last saved: ${this.formatDateTime(note.updated_at)}`;
    }

    const bsModal = new bootstrap.Modal(modal);
    bsModal.show();
  },

  // Create new note for folder (called from folder context menu)
  async createNewNoteForFolder(folderId) {
    const note = await this.createNote(folderId, 'Untitled Note', '');
    if (note) {
      this.openNoteEditor(note.id);
    }
  },

  // Save current note
  async saveCurrentNote() {
    if (!this.currentNote) return;

    const nameInput = document.getElementById('noteNameInput');
    const contentInput = document.getElementById('noteContentInput');

    const updates = {
      name: nameInput?.value || 'Untitled Note',
      content: contentInput?.value || ''
    };

    const updated = await this.updateNote(this.currentNote.id, updates);
    if (updated) {
      this.currentNote = { ...this.currentNote, ...updated };
      this.showToast('Note saved', 'success');

      // Refresh folder tree to show updated note name
      this.renderFolderTree();

      // Close the modal
      const modal = bootstrap.Modal.getInstance(document.getElementById('noteEditorModal'));
      modal?.hide();
    }
  },

  // Expand @notename references in a message with note content
  // Returns the message with note references expanded
  async expandNoteReferences(message) {
    // Get current session's folder
    const session = this.getActiveSession();
    if (!session?.folder_id) {
      return message; // No folder, return as-is
    }

    // Get notes for this folder
    const notes = this.notesByFolder.get(session.folder_id) || [];
    if (notes.length === 0) {
      return message; // No notes, return as-is
    }

    // Find @notename patterns (matches @word or @"phrase with spaces")
    const noteRefPattern = /@(\w+)|@"([^"]+)"/g;
    let expandedMessage = message;
    let match;

    while ((match = noteRefPattern.exec(message)) !== null) {
      const noteName = match[1] || match[2]; // Either simple word or quoted phrase
      const fullMatch = match[0];

      // Find note by name (case-insensitive)
      const note = notes.find(n => n.name.toLowerCase() === noteName.toLowerCase());

      if (note) {
        // Fetch full note content if we only have preview
        let noteContent = note.content;
        if (!noteContent) {
          try {
            const response = await fetch(`/api/notes/${note.id}`);
            if (response.ok) {
              const fullNote = await response.json();
              noteContent = fullNote.content || '';
            }
          } catch (e) {
            console.error('Failed to fetch note content:', e);
          }
        }

        if (noteContent) {
          // Replace the reference with the note content (formatted for clarity)
          expandedMessage = expandedMessage.replace(
            fullMatch,
            `\n\n---\n📝 **Note: ${note.name}**\n\n${noteContent}\n\n---\n\n`
          );
        }
      }
    }

    return expandedMessage;
  },

  // Toggle preview mode
  toggleNotePreview() {
    this.isNotePreviewMode = !this.isNotePreviewMode;

    const contentInput = document.getElementById('noteContentInput');
    const previewContent = document.getElementById('notePreviewContent');
    const previewToggle = document.getElementById('notePreviewToggle');

    if (this.isNotePreviewMode) {
      contentInput.style.display = 'none';
      previewContent.style.display = 'block';
      previewToggle.classList.add('active');

      // Simple markdown rendering (basic)
      const markdown = contentInput.value;
      previewContent.innerHTML = this.renderMarkdown(markdown);
    } else {
      contentInput.style.display = 'block';
      previewContent.style.display = 'none';
      previewToggle.classList.remove('active');
    }
  },

  // Simple markdown renderer (basic implementation)
  renderMarkdown(text) {
    if (!text) return '<p style="color: var(--text-tertiary);">No content</p>';

    let html = this.escapeHtml(text);

    // Headers
    html = html.replace(/^### (.+)$/gm, '<h3>$1</h3>');
    html = html.replace(/^## (.+)$/gm, '<h2>$1</h2>');
    html = html.replace(/^# (.+)$/gm, '<h1>$1</h1>');

    // Bold and italic
    html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
    html = html.replace(/\*(.+?)\*/g, '<em>$1</em>');

    // Code blocks
    html = html.replace(/```(\w*)\n([\s\S]*?)```/g, '<pre><code>$2</code></pre>');
    html = html.replace(/`([^`]+)`/g, '<code>$1</code>');

    // Lists
    html = html.replace(/^\s*[-*]\s+(.+)$/gm, '<li>$1</li>');
    html = html.replace(/(<li>.*<\/li>\n?)+/g, '<ul>$&</ul>');

    // Blockquotes
    html = html.replace(/^>\s+(.+)$/gm, '<blockquote>$1</blockquote>');

    // Paragraphs
    html = html.replace(/\n\n/g, '</p><p>');
    html = '<p>' + html + '</p>';

    // Clean up empty paragraphs
    html = html.replace(/<p><\/p>/g, '');
    html = html.replace(/<p>(<h[1-6]>)/g, '$1');
    html = html.replace(/(<\/h[1-6]>)<\/p>/g, '$1');
    html = html.replace(/<p>(<ul>)/g, '$1');
    html = html.replace(/(<\/ul>)<\/p>/g, '$1');
    html = html.replace(/<p>(<pre>)/g, '$1');
    html = html.replace(/(<\/pre>)<\/p>/g, '$1');
    html = html.replace(/<p>(<blockquote>)/g, '$1');
    html = html.replace(/(<\/blockquote>)<\/p>/g, '$1');

    return html;
  },

  // Prompt to rename note
  async promptRenameNote(noteId) {
    // Find the note
    let note = null;
    for (const [folderId, notes] of this.notesByFolder) {
      note = notes.find(n => n.id === noteId);
      if (note) break;
    }
    if (!note) return;

    const newName = prompt('Enter new note name:', note.name);
    if (newName && newName.trim() && newName !== note.name) {
      await this.updateNote(noteId, { name: newName.trim() });
      this.renderFolderTree();
    }
  },

  // Confirm delete note
  async confirmDeleteNote(noteId) {
    if (confirm('Are you sure you want to delete this note?')) {
      await this.deleteNote(noteId);
    }
  },

  // =============================================================================
  // Workspace Tasks
  // =============================================================================

  // Task state
  workspaceTasks: [],
  taskCounts: { total: 0, pending: 0, completed: 0 },
  tasksByWorkspace: new Map(), // Tasks keyed by workspace (folder) ID
  scheduledTasksByWorkspace: new Map(), // Scheduled tasks keyed by workspace (folder) ID
  currentScheduledTaskWorkspaceId: null, // Workspace context for scheduled task operations

  // Load tasks for current session (from workspace/orchestration)
  async loadSessionTasks() {
    if (!this.activeSessionId) {
      this.workspaceTasks = [];
      this.taskCounts = { total: 0, pending: 0, completed: 0 };
      return;
    }

    try {
      // Get the workspace ID from the active session
      const activeSession = this.sessions.find(s => s.id === this.activeSessionId);
      if (!activeSession || !activeSession.folder_id) {
        this.workspaceTasks = [];
        this.taskCounts = { total: 0, pending: 0, completed: 0 };
        return;
      }

      const workspaceId = activeSession.folder_id;
      const response = await fetch(`/api/orchestration/tasks?studio_id=${workspaceId}`);
      if (!response.ok) throw new Error('Failed to load tasks');

      const data = await response.json();
      this.workspaceTasks = data.tasks || [];
      this.taskCounts = data.stats || { total: 0, pending: 0, completed: 0 };

      // Update the counts cache and badges
      this.sessionTaskCounts.set(this.activeSessionId, this.taskCounts);
      this.updateSessionTaskBadges();
      this.updateChatTaskBadge();

      // Update main panel if open
      if (this.mainTaskPanelOpen) {
        this.renderMainTaskPanel();
      }

      // Update workspace tasks in sidebar
      this.tasksByWorkspace.set(workspaceId, this.workspaceTasks);
      this.renderFolderTree();
    } catch (error) {
      console.error('Failed to load session tasks:', error);
      this.workspaceTasks = [];
      this.taskCounts = { total: 0, pending: 0, completed: 0 };
      this.updateChatTaskBadge();
    }
  },

  // Load tasks for a workspace (folder) using orchestration API
  async loadWorkspaceTasks(workspaceId) {
    try {
      const response = await fetch(`/api/orchestration/tasks?studio_id=${workspaceId}`);
      if (!response.ok) throw new Error('Failed to load workspace tasks');
      const data = await response.json();
      this.tasksByWorkspace.set(workspaceId, data.tasks || []);
      return data.tasks || [];
    } catch (error) {
      console.error('Failed to load workspace tasks:', error);
      return [];
    }
  },

  // Load tasks for all workspaces recursively
  async loadAllWorkspaceTasks(folders) {
    for (const folder of folders) {
      await this.loadWorkspaceTasks(folder.id);
      if (folder.children && folder.children.length > 0) {
        await this.loadAllWorkspaceTasks(folder.children);
      }
    }
  },

  // Load scheduled tasks for a workspace (folder) using orchestration API
  // Now returns tasks that have schedules (not legacy ScheduledTask entities)
  async loadWorkspaceScheduledTasks(workspaceId) {
    try {
      const response = await fetch(`/api/orchestration/tasks?studio_id=${workspaceId}`);
      if (!response.ok) throw new Error('Failed to load workspace tasks');
      const data = await response.json();
      const allTasks = data.tasks || [];
      // Filter tasks that have schedules - normalize to consistent format
      const scheduledTasks = allTasks
        .filter(task => task.schedule != null)
        .map(task => ({
          id: task.id,
          name: task.schedule_name || task.description,
          description: task.description,
          enabled: task.schedule_enabled,
          schedule: task.schedule,
          next_run: task.next_run,
          last_run: task.last_run,
          execution_count: task.execution_count,
          failure_count: task.failure_count,
          execution_history: task.execution_history,
          // Keep reference to original task
          task_id: task.id,
          from: task.from,
          to: task.to
        }));
      this.scheduledTasksByWorkspace.set(workspaceId, scheduledTasks);
      return scheduledTasks;
    } catch (error) {
      console.error('Failed to load workspace scheduled tasks:', error);
      return [];
    }
  },

  // Load scheduled tasks for all workspaces recursively
  async loadAllWorkspaceScheduledTasks(folders) {
    for (const folder of folders) {
      await this.loadWorkspaceScheduledTasks(folder.id);
      if (folder.children && folder.children.length > 0) {
        await this.loadAllWorkspaceScheduledTasks(folder.children);
      }
    }
  },

  // Add task to workspace from sidebar using orchestration API
  async addWorkspaceTask(workspaceId) {
    // Prompt for task description
    const description = prompt('Enter task description:');
    if (!description || !description.trim()) return;

    try {
      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          studio_id: workspaceId,
          description: description.trim(),
          priority: 3
        })
      });

      if (!response.ok) throw new Error('Failed to create task');

      // Reload workspace tasks
      await this.loadWorkspaceTasks(workspaceId);
      this.renderFolderTree();
      this.showToast('Task created', 'success');

      // Also reload session tasks if active session is in this workspace
      const activeSession = this.sessions.find(s => s.id === this.activeSessionId);
      if (activeSession && activeSession.folder_id === workspaceId) {
        await this.loadSessionTasks();
      }
    } catch (error) {
      console.error('Failed to create task:', error);
      this.showToast('Failed to create task', 'error');
    }
  },

  // Toggle task completion using orchestration API
  async toggleTaskComplete(taskId) {
    const task = this.workspaceTasks.find(t => t.id === taskId);
    if (!task) return;

    try {
      if (task.status === 'completed') {
        // Uncomplete - update status back to pending
        const response = await fetch('/api/orchestration/tasks', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ task_id: taskId, status: 'pending' })
        });
        if (!response.ok) throw new Error('Failed to update task');
      } else {
        // Complete using the complete endpoint
        const response = await fetch(`/api/orchestration/tasks/${taskId}/complete`, {
          method: 'POST'
        });
        if (!response.ok) throw new Error('Failed to complete task');
      }

      // Reload tasks - use workspace ID if no active session
      if (this.activeSessionId) {
        await this.loadSessionTasks();
      } else if (this.currentTaskWorkspaceId) {
        const tasksResponse = await fetch(`/api/orchestration/tasks?studio_id=${this.currentTaskWorkspaceId}`);
        if (tasksResponse.ok) {
          const data = await tasksResponse.json();
          this.workspaceTasks = data.tasks || [];
          this.taskCounts = data.stats || { total: 0, pending: 0, completed: 0 };
        }
        this.renderMainTaskPanel();
      }
    } catch (error) {
      console.error('Failed to toggle task:', error);
      this.showToast('Failed to update task', 'error');
    }
  },

  // Delete task using orchestration API
  async deleteTask(taskId) {
    if (!confirm('Delete this task?')) return;

    try {
      const response = await fetch(`/api/orchestration/tasks?id=${taskId}`, {
        method: 'DELETE'
      });

      if (!response.ok) throw new Error('Failed to delete task');

      await this.loadSessionTasks();
      this.showToast('Task deleted', 'success');
    } catch (error) {
      console.error('Failed to delete task:', error);
      this.showToast('Failed to delete task', 'error');
    }
  },

  // ===== Main Task Panel (Chat Area) =====

  mainTaskPanelOpen: false,

  // Initialize main task panel events
  initMainTaskPanel() {
    // Tasks button in chat header
    document.getElementById('chatTasksBtn')?.addEventListener('click', () => this.toggleMainTaskPanel());

    // Close button
    document.getElementById('mainTaskPanelClose')?.addEventListener('click', () => this.closeMainTaskPanel());

    // Backdrop click to close
    document.getElementById('mainTaskModalBackdrop')?.addEventListener('click', () => this.closeMainTaskPanel());

    // Add task button - opens modal
    document.getElementById('mainTaskAddBtn')?.addEventListener('click', () => this.openTaskModal());

    // Input enter key - opens modal with prefilled title
    document.getElementById('mainTaskInput')?.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        const input = document.getElementById('mainTaskInput');
        const title = input?.value?.trim() || '';
        this.openTaskModal(null, title);
        if (input) input.value = '';
      }
    });

    // Task modal event handlers
    document.getElementById('taskModalClose')?.addEventListener('click', () => this.closeTaskModal());
    document.getElementById('taskModalCancel')?.addEventListener('click', () => this.closeTaskModal());
    document.getElementById('taskModalSave')?.addEventListener('click', () => this.saveTaskFromModal());
    document.querySelector('.task-modal-backdrop')?.addEventListener('click', () => this.closeTaskModal());

    // Schedule fields toggle
    document.getElementById('taskModalScheduleEnabled')?.addEventListener('change', (e) => {
      const scheduleFields = document.getElementById('taskModalScheduleFields');
      if (scheduleFields) {
        scheduleFields.style.display = e.target.checked ? 'block' : 'none';
      }
    });

    // Schedule type change handler
    document.getElementById('taskModalScheduleType')?.addEventListener('change', (e) => {
      this.updateTaskModalScheduleTypeFields();
    });

    // Modal escape key for task modal
    document.getElementById('taskModal')?.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        this.closeTaskModal();
      }
    });

    // Escape key for main task modal
    document.getElementById('mainTaskModal')?.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        this.closeMainTaskPanel();
      }
    });
  },

  // Toggle main task panel
  toggleMainTaskPanel() {
    if (this.mainTaskPanelOpen) {
      this.closeMainTaskPanel();
    } else {
      this.openMainTaskPanel();
    }
  },

  // Open main task modal
  openMainTaskPanel() {
    const modal = document.getElementById('mainTaskModal');
    // Allow opening if we have either an active session or a workspace ID (from sidebar click)
    if (!modal || (!this.activeSessionId && !this.currentTaskWorkspaceId)) return;

    modal.style.display = 'flex';
    this.mainTaskPanelOpen = true;

    // Update button state
    document.getElementById('chatTasksBtn')?.classList.add('active');

    // Render tasks
    this.renderMainTaskPanel();

    // Focus input
    setTimeout(() => {
      document.getElementById('mainTaskInput')?.focus();
    }, 100);

    // Prevent body scrolling when modal is open
    document.body.style.overflow = 'hidden';
  },

  // Close main task modal
  closeMainTaskPanel() {
    const modal = document.getElementById('mainTaskModal');
    if (!modal) return;

    modal.style.display = 'none';
    this.mainTaskPanelOpen = false;

    // Update button state
    document.getElementById('chatTasksBtn')?.classList.remove('active');

    // Restore body scrolling
    document.body.style.overflow = '';
  },

  // Open workspace task panel from sidebar
  // With unified task system, tasks are stored in workspaces (not sessions)
  // so we can view tasks without needing a session
  async openWorkspaceTaskPanel(workspaceId) {
    try {
      // Load tasks directly from workspace using orchestration API
      const response = await fetch(`/api/orchestration/tasks?studio_id=${workspaceId}`);
      if (!response.ok) throw new Error('Failed to load workspace tasks');

      const data = await response.json();
      this.workspaceTasks = data.tasks || [];
      this.taskCounts = data.stats || { total: 0, pending: 0, completed: 0 };

      // Store workspace ID for task operations
      this.currentTaskWorkspaceId = workspaceId;

      // Open main task panel (works without active session)
      this.openMainTaskPanel();
    } catch (error) {
      console.error('Failed to open workspace tasks:', error);
      this.showToast('Failed to open workspace tasks', 'error');
    }
  },

  // =============================================================================
  // Scheduled Tasks Panel Functions
  // =============================================================================

  // Open scheduled tasks panel for a workspace
  async openScheduledTasksPanel(workspaceId) {
    try {
      // Load tasks with schedules (not legacy ScheduledTask entities)
      const scheduledTasks = await this.loadWorkspaceScheduledTasks(workspaceId);

      this.currentScheduledTaskWorkspaceId = workspaceId;
      this.showScheduledTasksModal(scheduledTasks, workspaceId);
    } catch (error) {
      console.error('Failed to open scheduled tasks:', error);
      this.showToast('Failed to load scheduled tasks', 'error');
    }
  },

  // Show scheduled tasks modal
  showScheduledTasksModal(scheduledTasks, workspaceId) {
    const modal = document.getElementById('scheduledTasksModal');
    if (!modal) return;

    const folder = this.findFolderById(workspaceId, this.folders);
    const workspaceName = folder ? folder.name : 'Workspace';

    // Set title
    const titleEl = document.getElementById('scheduledTasksModalTitle');
    if (titleEl) {
      titleEl.innerHTML = `
        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
          <path d="M12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22C6.47,22 2,17.5 2,12A10,10 0 0,1 12,2M12.5,7V12.25L17,14.92L16.25,16.15L11,13V7H12.5Z"/>
        </svg>
        Schedules - ${this.escapeHtml(workspaceName)}
      `;
    }

    // Render list
    this.renderScheduledTasksList(scheduledTasks);

    // Show modal
    modal.style.display = 'flex';
    document.body.style.overflow = 'hidden';
  },

  // Render scheduled tasks list
  renderScheduledTasksList(scheduledTasks) {
    const listContainer = document.getElementById('scheduledTasksList');
    const emptyState = document.getElementById('scheduledTasksEmpty');

    if (!listContainer) return;

    listContainer.innerHTML = '';

    if (scheduledTasks.length === 0) {
      if (emptyState) emptyState.style.display = 'flex';
      return;
    }

    if (emptyState) emptyState.style.display = 'none';

    scheduledTasks.forEach(st => {
      const itemEl = document.createElement('div');
      itemEl.className = 'scheduled-task-item';
      itemEl.dataset.scheduleId = st.id;

      const scheduleDesc = this.getScheduleDescription(st.schedule);
      const nextRun = st.next_run ? new Date(st.next_run).toLocaleString() : 'Not scheduled';
      const statusClass = st.enabled ? 'enabled' : 'disabled';

      itemEl.innerHTML = `
        <div class="scheduled-task-status-indicator ${statusClass}"></div>
        <div class="scheduled-task-content">
          <div class="scheduled-task-name">${this.escapeHtml(st.name)}</div>
          <div class="scheduled-task-desc">${this.escapeHtml(st.description || '')}</div>
          <div class="scheduled-task-meta">
            <span class="scheduled-task-schedule">${scheduleDesc}</span>
            ${st.enabled ? `<span class="scheduled-task-next">Next: ${nextRun}</span>` : ''}
          </div>
        </div>
        <div class="scheduled-task-actions">
          <button class="scheduled-task-toggle" title="${st.enabled ? 'Disable' : 'Enable'}">
            ${st.enabled ? '⏸' : '▶'}
          </button>
          <button class="scheduled-task-trigger" title="Trigger Now" ${!st.enabled ? 'disabled' : ''}>
            ⚡
          </button>
        </div>
      `;

      // Bind click for details
      itemEl.addEventListener('click', (e) => {
        if (!e.target.closest('button')) {
          this.showScheduledTaskDetails(st);
        }
      });

      // Toggle button
      itemEl.querySelector('.scheduled-task-toggle')?.addEventListener('click', (e) => {
        e.stopPropagation();
        this.toggleScheduledTaskEnabled(st.id, !st.enabled);
      });

      // Trigger button
      itemEl.querySelector('.scheduled-task-trigger')?.addEventListener('click', (e) => {
        e.stopPropagation();
        this.triggerScheduledTask(st.id);
      });

      listContainer.appendChild(itemEl);
    });
  },

  // Get human-readable schedule description
  getScheduleDescription(schedule) {
    if (!schedule) return 'No schedule';

    switch (schedule.type) {
      case 'daily':
        return `Daily at ${schedule.time_of_day || '00:00'}`;
      case 'weekly':
        const days = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
        return `Weekly on ${days[schedule.day_of_week || 0]} at ${schedule.time_of_day || '00:00'}`;
      case 'interval':
        // Interval is in nanoseconds
        const totalSeconds = Math.floor((schedule.interval || 0) / 1000000000);
        const hours = Math.floor(totalSeconds / 3600);
        const minutes = Math.floor((totalSeconds % 3600) / 60);
        if (hours > 0) return `Every ${hours} hour${hours > 1 ? 's' : ''}`;
        return `Every ${minutes} minute${minutes > 1 ? 's' : ''}`;
      case 'once':
        return schedule.execute_at ? `Once at ${new Date(schedule.execute_at).toLocaleString()}` : 'Once';
      case 'cron':
        return `Cron: ${schedule.cron_expr || 'custom'}`;
      default:
        return 'Custom schedule';
    }
  },

  // Show scheduled task details modal
  showScheduledTaskDetails(st) {
    const modal = document.getElementById('scheduledTaskDetailModal');
    if (!modal) return;

    // Populate details
    document.getElementById('scheduleDetailName').textContent = st.name;
    document.getElementById('scheduleDetailDesc').textContent = st.description || 'No description';

    const statusEl = document.getElementById('scheduleDetailStatus');
    statusEl.textContent = st.enabled ? 'Enabled' : 'Disabled';
    statusEl.className = 'schedule-detail-badge ' + (st.enabled ? 'badge-enabled' : 'badge-disabled');

    document.getElementById('scheduleDetailSchedule').textContent = this.getScheduleDescription(st.schedule);
    document.getElementById('scheduleDetailNextRun').textContent = st.next_run ? new Date(st.next_run).toLocaleString() : 'Not scheduled';
    document.getElementById('scheduleDetailLastRun').textContent = st.last_run ? new Date(st.last_run).toLocaleString() : 'Never';
    document.getElementById('scheduleDetailExecutions').textContent = `${st.execution_count || 0} total, ${st.failure_count || 0} failures`;

    // Render execution history
    this.renderExecutionHistory(st.execution_history || []);

    // Store current schedule ID for actions
    modal.dataset.scheduleId = st.id;
    modal.dataset.enabled = st.enabled;

    // Update toggle button text
    const toggleBtn = document.getElementById('scheduleDetailToggleBtn');
    if (toggleBtn) toggleBtn.textContent = st.enabled ? 'Disable' : 'Enable';

    modal.style.display = 'flex';
  },

  // Render execution history
  renderExecutionHistory(history) {
    const container = document.getElementById('scheduleDetailHistory');
    if (!container) return;

    if (!history || history.length === 0) {
      container.innerHTML = '<div class="history-empty">No execution history</div>';
      return;
    }

    // Show last 10 executions (reversed to show most recent first)
    const recentHistory = history.slice().reverse().slice(0, 10);

    container.innerHTML = recentHistory.map(exec => `
      <div class="history-item ${exec.status}">
        <span class="history-time">${new Date(exec.executed_at).toLocaleString()}</span>
        <span class="history-status">${exec.status === 'success' ? '✓' : '✗'}</span>
        ${exec.error ? `<span class="history-error" title="${this.escapeHtml(exec.error)}">${this.escapeHtml(exec.error.substring(0, 30))}...</span>` : ''}
      </div>
    `).join('');
  },

  // Toggle scheduled task enabled/disabled
  // Now updates the task's schedule_enabled field
  async toggleScheduledTaskEnabled(taskId, enable) {
    const action = enable ? 'enable' : 'disable';
    try {
      const response = await fetch(`/api/orchestration/tasks/${taskId}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          task_id: taskId,
          schedule_enabled: enable
        })
      });

      if (!response.ok) throw new Error(`Failed to ${action} task schedule`);

      // Reload and refresh
      if (this.currentScheduledTaskWorkspaceId) {
        await this.loadWorkspaceScheduledTasks(this.currentScheduledTaskWorkspaceId);
        const tasks = this.scheduledTasksByWorkspace.get(this.currentScheduledTaskWorkspaceId) || [];
        this.renderScheduledTasksList(tasks);
        this.renderFolderTree();
      }

      this.showToast(`Schedule ${enable ? 'enabled' : 'disabled'}`, 'success');
    } catch (error) {
      console.error(`Failed to ${action} task schedule:`, error);
      this.showToast(`Failed to ${action} schedule`, 'error');
    }
  },

  // Trigger scheduled task manually (execute the task now)
  async triggerScheduledTask(taskId) {
    if (!confirm('Execute this scheduled task now?')) return;

    try {
      const response = await fetch('/api/orchestration/tasks/execute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task_id: taskId })
      });

      if (!response.ok) throw new Error('Failed to trigger scheduled task');

      const data = await response.json();
      this.showToast(`Task triggered! ID: ${data.task_id?.substring(0, 8)}...`, 'success');
    } catch (error) {
      console.error('Failed to trigger scheduled task:', error);
      this.showToast('Failed to trigger schedule', 'error');
    }
  },

  // Close scheduled tasks modal
  closeScheduledTasksModal() {
    const modal = document.getElementById('scheduledTasksModal');
    if (modal) {
      modal.style.display = 'none';
      document.body.style.overflow = '';
    }
  },

  // Close scheduled task detail modal
  closeScheduledTaskDetailModal() {
    const modal = document.getElementById('scheduledTaskDetailModal');
    if (modal) modal.style.display = 'none';
  },

  // Initialize scheduled tasks modal events
  initScheduledTasksModal() {
    // Close buttons
    document.getElementById('scheduledTasksModalClose')?.addEventListener('click', () => this.closeScheduledTasksModal());
    document.getElementById('scheduledTasksModalBackdrop')?.addEventListener('click', () => this.closeScheduledTasksModal());

    // Detail modal close
    document.getElementById('scheduleDetailClose')?.addEventListener('click', () => this.closeScheduledTaskDetailModal());
    document.getElementById('scheduleDetailBackdrop')?.addEventListener('click', () => this.closeScheduledTaskDetailModal());

    // Detail modal actions
    document.getElementById('scheduleDetailToggleBtn')?.addEventListener('click', () => {
      const modal = document.getElementById('scheduledTaskDetailModal');
      if (modal) {
        const scheduleId = modal.dataset.scheduleId;
        const currentlyEnabled = modal.dataset.enabled === 'true';
        this.toggleScheduledTaskEnabled(scheduleId, !currentlyEnabled);
        this.closeScheduledTaskDetailModal();
      }
    });

    document.getElementById('scheduleDetailTriggerBtn')?.addEventListener('click', () => {
      const modal = document.getElementById('scheduledTaskDetailModal');
      if (modal) {
        const scheduleId = modal.dataset.scheduleId;
        this.triggerScheduledTask(scheduleId);
      }
    });

    // Escape key handlers
    document.getElementById('scheduledTasksModal')?.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') this.closeScheduledTasksModal();
    });

    document.getElementById('scheduledTaskDetailModal')?.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') this.closeScheduledTaskDetailModal();
    });
  },

  // Helper to find folder by ID recursively
  findFolderById(folderId, folders) {
    for (const folder of folders) {
      if (folder.id === folderId) return folder;
      if (folder.children && folder.children.length > 0) {
        const found = this.findFolderById(folderId, folder.children);
        if (found) return found;
      }
    }
    return null;
  },

  // Submit task from main panel using orchestration API
  async submitMainTask() {
    const input = document.getElementById('mainTaskInput');
    const description = input?.value?.trim();
    if (!description) return;

    try {
      // Get workspace ID from active session or stored workspace context
      let workspaceId = this.currentTaskWorkspaceId;
      if (!workspaceId && this.activeSessionId) {
        const activeSession = this.sessions.find(s => s.id === this.activeSessionId);
        workspaceId = activeSession?.folder_id;
      }

      if (!workspaceId) {
        this.showToast('No workspace context available', 'error');
        return;
      }

      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          studio_id: workspaceId,
          description,
          priority: 3
        })
      });

      if (!response.ok) throw new Error('Failed to create task');

      input.value = '';

      // Reload tasks for the workspace
      const tasksResponse = await fetch(`/api/orchestration/tasks?studio_id=${workspaceId}`);
      if (tasksResponse.ok) {
        const data = await tasksResponse.json();
        this.workspaceTasks = data.tasks || [];
        this.taskCounts = data.stats || { total: 0, pending: 0, completed: 0 };
      }

      this.renderMainTaskPanel();
      this.showToast('Task created', 'success');
    } catch (error) {
      console.error('Failed to create task:', error);
      this.showToast('Failed to create task', 'error');
    }
  },

  // Render main task panel
  renderMainTaskPanel() {
    const listContainer = document.getElementById('mainTaskList');
    const emptyState = document.getElementById('mainTaskEmpty');
    const chatBadge = document.getElementById('chatTasksBadge');

    if (!listContainer) return;

    // Update chat button badge
    if (chatBadge) {
      if (this.taskCounts.total === 0) {
        chatBadge.classList.add('d-none');
      } else {
        chatBadge.classList.remove('d-none');
        if (this.taskCounts.pending > 0) {
          chatBadge.textContent = this.taskCounts.pending;
          chatBadge.classList.remove('complete');
        } else {
          chatBadge.textContent = '✓';
          chatBadge.classList.add('complete');
        }
      }
    }

    // Clear existing items
    listContainer.innerHTML = '';

    if (this.workspaceTasks.length === 0) {
      if (emptyState) emptyState.style.display = 'flex';
      return;
    }

    if (emptyState) emptyState.style.display = 'none';

    // Sort: pending first, then by creation date
    const sortedTasks = [...this.workspaceTasks].sort((a, b) => {
      const aCompleted = a.status === 'completed' ? 1 : 0;
      const bCompleted = b.status === 'completed' ? 1 : 0;
      if (aCompleted !== bCompleted) return aCompleted - bCompleted;
      return new Date(b.created_at) - new Date(a.created_at);
    });

    sortedTasks.forEach(task => {
      const isCompleted = task.status === 'completed';
      const isWorkspaceTask = !task.session_id && task.workspace_id;
      const createdAt = this.formatTimeAgo(task.created_at);

      const taskEl = document.createElement('div');
      taskEl.className = `main-task-item ${isCompleted ? 'completed' : ''}`;
      taskEl.dataset.taskId = task.id;
      const hasDetails = task.details && task.details.trim().length > 0;
      taskEl.innerHTML = `
        <input type="checkbox" class="main-task-checkbox" ${isCompleted ? 'checked' : ''}>
        <div class="main-task-content">
          <div class="main-task-description">${this.escapeHtml(task.description)}</div>
          ${hasDetails ? `<div class="main-task-details">${this.escapeHtml(task.details)}</div>` : ''}
          <div class="main-task-meta">
            ${isWorkspaceTask ? '<span class="main-task-workspace-badge">Workspace</span>' : ''}
            <span>${createdAt}</span>
          </div>
        </div>
        <button class="main-task-edit" title="Edit task">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18,2.9 17.35,2.9 16.96,3.29L15.12,5.12L18.87,8.87M3,17.25V21H6.75L17.81,9.93L14.06,6.18L3,17.25Z"/>
          </svg>
        </button>
        <button class="main-task-execute" title="Execute task">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M8,5.14V19.14L19,12.14L8,5.14Z"/>
          </svg>
        </button>
        <button class="main-task-delete" title="Delete task">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
          </svg>
        </button>
      `;

      // Checkbox change
      taskEl.querySelector('.main-task-checkbox')?.addEventListener('change', () => {
        this.toggleTaskComplete(task.id);
      });

      // Edit click
      taskEl.querySelector('.main-task-edit')?.addEventListener('click', (e) => {
        e.stopPropagation();
        this.openTaskModal(task.id);
      });

      // Execute click
      taskEl.querySelector('.main-task-execute')?.addEventListener('click', (e) => {
        e.stopPropagation();
        this.executeTask(task.id);
      });

      // Delete click
      taskEl.querySelector('.main-task-delete')?.addEventListener('click', (e) => {
        e.stopPropagation();
        this.deleteMainTask(task.id);
      });

      listContainer.appendChild(taskEl);
    });
  },

  // Delete task from main panel (with confirmation) using orchestration API
  async deleteMainTask(taskId) {
    if (!confirm('Delete this task?')) return;

    try {
      const response = await fetch(`/api/orchestration/tasks?id=${taskId}`, {
        method: 'DELETE'
      });

      if (!response.ok) throw new Error('Failed to delete task');

      // Reload tasks - use workspace ID if no active session
      if (this.activeSessionId) {
        await this.loadSessionTasks();
      } else if (this.currentTaskWorkspaceId) {
        const tasksResponse = await fetch(`/api/orchestration/tasks?studio_id=${this.currentTaskWorkspaceId}`);
        if (tasksResponse.ok) {
          const data = await tasksResponse.json();
          this.workspaceTasks = data.tasks || [];
          this.taskCounts = data.stats || { total: 0, pending: 0, completed: 0 };
        }
      }
      this.renderMainTaskPanel();
      this.showToast('Task deleted', 'success');
    } catch (error) {
      console.error('Failed to delete task:', error);
      this.showToast('Failed to delete task', 'error');
    }
  },

  // Execute task - send to chat as a prompt using orchestration API
  async executeTask(taskId) {
    const task = this.workspaceTasks.find(t => t.id === taskId);
    if (!task) return;

    try {
      // Mark task as in_progress using orchestration API
      await fetch('/api/orchestration/tasks', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task_id: taskId, status: 'in_progress' })
      });

      // Close task panel
      this.closeMainTaskPanel();

      // Build prompt from description and details
      let prompt = task.description;
      if (task.details && task.details.trim()) {
        prompt += '\n\n' + task.details.trim();
      }

      // Send task to chat
      if (window.sendMessageToChat) {
        window.sendMessageToChat(prompt);
      }

      // Reload tasks to reflect status change
      await this.loadSessionTasks();
    } catch (error) {
      console.error('Failed to execute task:', error);
      this.showToast('Failed to execute task', 'error');
    }
  },

  // Track which task is being edited (null for new task)
  editingTaskId: null,
  taskModalWorkspaceId: null, // For workspace-level tasks

  // Open task modal for creating or editing
  openTaskModal(taskId = null, prefillTitle = '') {
    // Clear workspace context when opening for session task
    this.taskModalWorkspaceId = null;
    this.editingTaskId = taskId;

    // Get workspace ID from active session or stored workspace context (from sidebar click)
    let workspaceId = this.currentTaskWorkspaceId;
    if (!workspaceId && this.activeSessionId) {
      const session = this.sessions.find(s => s.id === this.activeSessionId);
      workspaceId = session?.folder_id;
    }

    // Helper to reload tasks after modal save
    const reloadTasks = async () => {
      if (this.activeSessionId) {
        await this.loadSessionTasks();
      } else if (workspaceId) {
        const tasksResponse = await fetch(`/api/orchestration/tasks?studio_id=${workspaceId}`);
        if (tasksResponse.ok) {
          const data = await tasksResponse.json();
          this.workspaceTasks = data.tasks || [];
          this.taskCounts = data.stats || { total: 0, pending: 0, completed: 0 };
        }
      }
      this.renderMainTaskPanel();
    };

    // Use shared task modal controller if available
    if (window.taskModalController && workspaceId) {
      if (taskId) {
        // Editing existing task
        const task = this.workspaceTasks.find(t => t.id === taskId);
        if (task) {
          window.taskModalController.openForEdit(task, reloadTasks);
        }
      } else {
        // Creating new task
        window.taskModalController.openForCreate(workspaceId, prefillTitle, reloadTasks);
      }
      return;
    }

    // Fallback to legacy implementation
    const modal = document.getElementById('taskModal');
    if (!modal) return;

    // Set modal title
    const modalTitle = document.getElementById('taskModalTitle');
    if (modalTitle) {
      modalTitle.textContent = taskId ? 'Edit Task' : 'Add Task';
    }

    // Get form elements
    const descriptionInput = document.getElementById('taskModalDescription');
    const detailsInput = document.getElementById('taskModalDetails');

    if (taskId) {
      // Editing existing task - populate fields
      const task = this.workspaceTasks.find(t => t.id === taskId);
      if (task) {
        if (descriptionInput) descriptionInput.value = task.description || '';
        if (detailsInput) detailsInput.value = task.details || '';
        // Populate schedule fields if task has schedule
        this.populateTaskModalScheduleFields(task);
      } else {
        this.resetTaskModalScheduleFields();
      }
    } else {
      // New task - clear or prefill
      if (descriptionInput) descriptionInput.value = prefillTitle;
      if (detailsInput) detailsInput.value = '';
      // Reset schedule fields for new task
      this.resetTaskModalScheduleFields();
    }

    // Show modal
    modal.style.display = 'flex';

    // Focus title input
    setTimeout(() => {
      descriptionInput?.focus();
    }, 100);
  },

  // Close task modal
  closeTaskModal() {
    const modal = document.getElementById('taskModal');
    if (modal) {
      modal.style.display = 'none';
    }
    this.editingTaskId = null;
    this.taskModalWorkspaceId = null;
  },

  // Update schedule type fields visibility based on selected schedule type
  updateTaskModalScheduleTypeFields() {
    const scheduleType = document.getElementById('taskModalScheduleType')?.value || 'interval';

    const timeField = document.getElementById('taskModalScheduleTimeField');
    const dayField = document.getElementById('taskModalScheduleDayField');
    const intervalField = document.getElementById('taskModalScheduleIntervalField');
    const onceField = document.getElementById('taskModalScheduleOnceField');

    // Hide all
    if (timeField) timeField.style.display = 'none';
    if (dayField) dayField.style.display = 'none';
    if (intervalField) intervalField.style.display = 'none';
    if (onceField) onceField.style.display = 'none';

    // Show relevant fields
    switch (scheduleType) {
      case 'daily':
        if (timeField) timeField.style.display = 'block';
        break;
      case 'weekly':
        if (timeField) timeField.style.display = 'block';
        if (dayField) dayField.style.display = 'block';
        break;
      case 'interval':
        if (intervalField) intervalField.style.display = 'block';
        break;
      case 'once':
        if (onceField) onceField.style.display = 'block';
        break;
    }
  },

  // Reset schedule fields in task modal
  resetTaskModalScheduleFields() {
    const enabledCheckbox = document.getElementById('taskModalScheduleEnabled');
    const scheduleFields = document.getElementById('taskModalScheduleFields');
    const scheduleName = document.getElementById('taskModalScheduleName');
    const scheduleType = document.getElementById('taskModalScheduleType');
    const scheduleTime = document.getElementById('taskModalScheduleTime');
    const scheduleDay = document.getElementById('taskModalScheduleDay');
    const scheduleIntervalValue = document.getElementById('taskModalScheduleIntervalValue');
    const scheduleIntervalUnit = document.getElementById('taskModalScheduleIntervalUnit');
    const scheduleDatetime = document.getElementById('taskModalScheduleDatetime');

    if (enabledCheckbox) enabledCheckbox.checked = false;
    if (scheduleFields) scheduleFields.style.display = 'none';
    if (scheduleName) scheduleName.value = '';
    if (scheduleType) scheduleType.value = 'interval';
    if (scheduleTime) scheduleTime.value = '09:00';
    if (scheduleDay) scheduleDay.value = 'monday';
    if (scheduleIntervalValue) scheduleIntervalValue.value = '1';
    if (scheduleIntervalUnit) scheduleIntervalUnit.value = 'hours';
    if (scheduleDatetime) scheduleDatetime.value = '';

    this.updateTaskModalScheduleTypeFields();
  },

  // Populate schedule fields from existing task
  populateTaskModalScheduleFields(task) {
    const enabledCheckbox = document.getElementById('taskModalScheduleEnabled');
    const scheduleFields = document.getElementById('taskModalScheduleFields');
    const scheduleName = document.getElementById('taskModalScheduleName');
    const scheduleType = document.getElementById('taskModalScheduleType');
    const scheduleTime = document.getElementById('taskModalScheduleTime');
    const scheduleDay = document.getElementById('taskModalScheduleDay');
    const scheduleIntervalValue = document.getElementById('taskModalScheduleIntervalValue');
    const scheduleIntervalUnit = document.getElementById('taskModalScheduleIntervalUnit');
    const scheduleDatetime = document.getElementById('taskModalScheduleDatetime');

    if (task.schedule_enabled && task.schedule) {
      if (enabledCheckbox) enabledCheckbox.checked = true;
      if (scheduleFields) scheduleFields.style.display = 'block';
      if (scheduleName) scheduleName.value = task.schedule_name || '';
      if (scheduleType) scheduleType.value = task.schedule.type || 'interval';

      // Populate type-specific fields
      const schedule = task.schedule;
      if (schedule.time && scheduleTime) scheduleTime.value = schedule.time;
      if (schedule.day_of_week && scheduleDay) scheduleDay.value = schedule.day_of_week;
      if (schedule.interval_minutes) {
        // Convert minutes to value + unit
        const minutes = schedule.interval_minutes;
        if (minutes >= 1440 && minutes % 1440 === 0) {
          if (scheduleIntervalValue) scheduleIntervalValue.value = minutes / 1440;
          if (scheduleIntervalUnit) scheduleIntervalUnit.value = 'days';
        } else if (minutes >= 60 && minutes % 60 === 0) {
          if (scheduleIntervalValue) scheduleIntervalValue.value = minutes / 60;
          if (scheduleIntervalUnit) scheduleIntervalUnit.value = 'hours';
        } else {
          if (scheduleIntervalValue) scheduleIntervalValue.value = minutes;
          if (scheduleIntervalUnit) scheduleIntervalUnit.value = 'minutes';
        }
      }
      if (schedule.run_at && scheduleDatetime) {
        // Convert ISO datetime to local datetime-local format
        const date = new Date(schedule.run_at);
        scheduleDatetime.value = date.toISOString().slice(0, 16);
      }

      this.updateTaskModalScheduleTypeFields();
    } else {
      this.resetTaskModalScheduleFields();
    }
  },

  // Get schedule data from modal fields
  getScheduleDataFromModal() {
    const enabledCheckbox = document.getElementById('taskModalScheduleEnabled');
    if (!enabledCheckbox?.checked) {
      return { schedule_enabled: false };
    }

    const scheduleType = document.getElementById('taskModalScheduleType')?.value || 'interval';
    const scheduleName = document.getElementById('taskModalScheduleName')?.value || '';

    const schedule = { type: scheduleType };

    switch (scheduleType) {
      case 'daily':
        schedule.time = document.getElementById('taskModalScheduleTime')?.value || '09:00';
        break;
      case 'weekly':
        schedule.time = document.getElementById('taskModalScheduleTime')?.value || '09:00';
        schedule.day_of_week = document.getElementById('taskModalScheduleDay')?.value || 'monday';
        break;
      case 'interval':
        // Combine value and unit into interval_minutes
        const intervalValue = parseInt(document.getElementById('taskModalScheduleIntervalValue')?.value || '1', 10);
        const intervalUnit = document.getElementById('taskModalScheduleIntervalUnit')?.value || 'hours';
        let intervalMinutes = intervalValue;
        if (intervalUnit === 'hours') {
          intervalMinutes = intervalValue * 60;
        } else if (intervalUnit === 'days') {
          intervalMinutes = intervalValue * 1440;
        }
        schedule.interval_minutes = intervalMinutes;
        break;
      case 'once':
        const datetime = document.getElementById('taskModalScheduleDatetime')?.value;
        if (datetime) {
          schedule.run_at = new Date(datetime).toISOString();
        }
        break;
    }

    return {
      schedule,
      schedule_enabled: true,
      schedule_name: scheduleName
    };
  },

  // Open task modal for workspace-level task
  openTaskModalForWorkspace(workspaceId) {
    this.editingTaskId = null;
    this.taskModalWorkspaceId = workspaceId;

    // Use shared task modal controller if available
    if (window.taskModalController) {
      window.taskModalController.openForCreate(workspaceId, '', async () => {
        await this.loadFolders();
        this.renderFolders();
      });
      return;
    }

    // Fallback to legacy implementation
    const modal = document.getElementById('taskModal');
    if (!modal) return;

    // Set modal title
    const modalTitle = document.getElementById('taskModalTitle');
    if (modalTitle) {
      const folder = this.folders.find(f => f.id === workspaceId);
      const workspaceName = folder ? folder.name : 'Workspace';
      modalTitle.textContent = `Add Task to ${workspaceName}`;
    }

    // Clear form
    const descriptionInput = document.getElementById('taskModalDescription');
    const detailsInput = document.getElementById('taskModalDetails');
    if (descriptionInput) descriptionInput.value = '';
    if (detailsInput) detailsInput.value = '';

    // Reset schedule fields for new task
    this.resetTaskModalScheduleFields();

    // Show modal
    modal.style.display = 'flex';

    // Focus title input
    setTimeout(() => {
      descriptionInput?.focus();
    }, 100);
  },

  // Save task from modal (create or update)
  async saveTaskFromModal() {
    const descriptionInput = document.getElementById('taskModalDescription');
    const detailsInput = document.getElementById('taskModalDetails');

    const description = descriptionInput?.value?.trim();
    const details = detailsInput?.value?.trim() || '';

    if (!description) {
      this.showToast('Task title is required', 'error');
      descriptionInput?.focus();
      return;
    }

    try {
      // Determine workspace ID - either from modal context or from active session
      let workspaceId = this.taskModalWorkspaceId;
      if (!workspaceId && this.activeSessionId) {
        // Get workspace ID from active session
        const session = this.sessions.find(s => s.id === this.activeSessionId);
        workspaceId = session?.folder_id;
      }

      if (!workspaceId) {
        this.showToast('No workspace selected', 'error');
        return;
      }

      // Get schedule data from modal
      const scheduleData = this.getScheduleDataFromModal();

      if (this.editingTaskId) {
        // Update existing task via orchestration API
        const response = await fetch(`/api/orchestration/tasks/${this.editingTaskId}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            task_id: this.editingTaskId,
            description,
            details,
            ...scheduleData
          })
        });

        if (!response.ok) throw new Error('Failed to update task');
        this.showToast('Task updated', 'success');
      } else {
        // Create new task via orchestration API (simple task - no agent)
        const response = await fetch('/api/orchestration/tasks', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            studio_id: workspaceId,
            description,
            details,
            priority: 3,
            ...scheduleData
          })
        });

        if (!response.ok) throw new Error('Failed to create task');
        this.showToast('Task created', 'success');
      }

      this.closeTaskModal();

      // Refresh data
      if (this.taskModalWorkspaceId) {
        await this.loadFolders();
        this.renderFolders();
      } else {
        await this.loadSessionTasks();
        this.renderMainTaskPanel();
      }
    } catch (error) {
      console.error('Failed to save task:', error);
      this.showToast('Failed to save task', 'error');
    }
  },

  // Update chat badge when tasks change (called from loadSessionTasks)
  updateChatTaskBadge() {
    const chatBadge = document.getElementById('chatTasksBadge');
    if (!chatBadge) return;

    if (this.taskCounts.total === 0) {
      chatBadge.classList.add('d-none');
    } else {
      chatBadge.classList.remove('d-none');
      if (this.taskCounts.pending > 0) {
        chatBadge.textContent = this.taskCounts.pending;
        chatBadge.classList.remove('complete');
      } else {
        chatBadge.textContent = '✓';
        chatBadge.classList.add('complete');
      }
    }
  }
};

// Export for use in other modules
window.sessionManager = sessionManager;

// Initialize when DOM is ready
document.addEventListener('DOMContentLoaded', () => {
  // Only initialize if session sidebar exists
  if (document.getElementById('sessionSidebar')) {
    sessionManager.init();
  }
});

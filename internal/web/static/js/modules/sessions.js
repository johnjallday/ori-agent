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
  editAgentCurrentProvider: '',
  editAgentSelectedTags: [],
  editAgentMCPServers: [],
  editAgentModalInitialized: false,
  editAgentModelOptionsLoaded: false,
  isCreatingFolder: false,
  importModeEnabled: false,
  importAllowDuplicate: false,
  importDuplicateWorkspaceId: '',
  importDuplicateWorkspaceName: '',
  importEntryPoint: 'workspace_hub_create',

  // Auto mode state
  chatAutoMode: false,
  chatLlmAvailable: false,
  chatSystemModelConfigured: false,
  autoModeSessionIds: new Set(), // Sessions that need auto-classification
  chatPendingFiles: [], // Files to attach when creating chat

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

  // Ensure chat panel is visible when switching/creating sessions
  openChatPanelIfAvailable() {
    if (window.chatPanel && typeof window.chatPanel.open === 'function') {
      window.chatPanel.open();
    }
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
    this.restoreSidebarState();
    this.initChatAgentBar();
    this.initMainTaskPanel();
    this.initScheduledTasksModal();
    await this.loadSessions();
    await this.loadFolders();
    await this.loadTags();

    // Try to restore active session, or prompt to create workspace
    const restored = await this.restoreActiveSession();
    const isWorkspacePage = document.body.classList.contains('home-hub');
    if (!restored && this.sessions.length === 0 && this.folders.length === 0 && isWorkspacePage) {
      // Show create workspace modal when no workspaces exist (only on workspaces page)
      this.showAddWorkspaceModal();
    } else if (!restored && this.sessions.length > 0) {
      // No saved session but sessions exist - use the first one
      // Pass false to not auto-open the chat panel on page load
      await this.switchToSession(this.sessions[0].id, false);
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

    // New note button - open note modal (workspace selectable)
    document.getElementById('newNoteBtn')?.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      this.openNoteCreateModal(this.activeFolder || null);
    });

    // New task button - open task modal (workspace can be selected in modal if not active)
    document.getElementById('newTaskBtn')?.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      // Open task modal - if no active folder, user can select workspace in the modal
      this.openTaskModalForWorkspace(this.activeFolder || null);
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

    const importToggle = document.getElementById('folderImportToggle');
    importToggle?.addEventListener('change', (event) => {
      const checked = Boolean(event?.currentTarget?.checked);
      this.setImportModeEnabled(checked);
      if (!checked) {
        this.importAllowDuplicate = false;
      }
    });

    const importPathInput = document.getElementById('folderImportPathInput');
    importPathInput?.addEventListener('input', (event) => {
      this.importAllowDuplicate = false;
      this.clearImportDuplicateWarning();
      this.prefillWorkspaceNameFromImportPath(event?.target?.value || '');
    });
    importPathInput?.addEventListener('blur', (event) => {
      void this.checkImportDuplicate(event?.target?.value || '');
    });

    const importBrowseBtn = document.getElementById('folderImportBrowseBtn');
    importBrowseBtn?.addEventListener('click', () => {
      void this.browseImportFolderPath();
    });

    const openExistingBtn = document.getElementById('folderImportOpenExistingBtn');
    openExistingBtn?.addEventListener('click', () => {
      if (!this.importDuplicateWorkspaceId) {
        return;
      }
      this.emitImportDuplicateActionTelemetry('suggestion_accepted', importPathInput?.value || '');
      window.location.href = `/workspaces/${encodeURIComponent(this.importDuplicateWorkspaceId)}`;
    });

    const proceedDuplicateBtn = document.getElementById('folderImportProceedDuplicateBtn');
    proceedDuplicateBtn?.addEventListener('click', () => {
      this.importAllowDuplicate = true;
      this.emitImportDuplicateActionTelemetry('override_confirmed', importPathInput?.value || '');
      this.showToast('Duplicate override enabled. Click Import Folder to continue.', 'warning');
    });

    // Folder color options
    document.querySelectorAll('.folder-color-btn').forEach(btn => {
      btn.addEventListener('click', (e) => {
        document.querySelectorAll('.folder-color-btn').forEach(b => b.classList.remove('active'));
        e.target.classList.add('active');
      });
    });

    const addFolderModal = document.getElementById('addFolderModal');
    addFolderModal?.addEventListener('show.bs.modal', (event) => {
      const trigger = event?.relatedTarget || null;
      const pendingImportMode = String(addFolderModal.dataset.pendingImportMode || '') === 'true';
      const triggerImportMode = String(trigger?.dataset?.workspaceImportMode || '') === 'true';
      const importMode = trigger ? triggerImportMode : pendingImportMode;
      const entryPoint = String(
        trigger?.dataset?.workspaceEntryPoint ||
        addFolderModal.dataset.pendingEntryPoint ||
        ''
      ).trim();

      this.resetAddWorkspaceModalForm({ preserveAskOri: true });
      this.importEntryPoint = entryPoint || (importMode ? 'workspace_hub_import' : 'workspace_hub_create');
      if (importMode) {
        this.setImportModeEnabled(true);
      }

      delete addFolderModal.dataset.pendingImportMode;
      delete addFolderModal.dataset.pendingEntryPoint;
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
        const response = await fetch(`/api/orchestration/tasks?workspace_id=${workspaceId}`);
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
    const agentName = session.agent_name || '';
    const modeLabel = agentName || 'Assistant';
    const badgeTitle = agentName ? `Direct agent chat: ${agentName}` : 'Assistant session';

    return `
      <div class="session-item ${isActive ? 'active' : ''} ${isSelected ? 'selected' : ''}" data-session-id="${session.id}">
        <div class="session-item-header">
          <svg class="session-icon" width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
            <path d="M20,2H4A2,2 0 0,0 2,4V22L6,18H20A2,2 0 0,0 22,16V4A2,2 0 0,0 20,2Z"/>
          </svg>
          <span class="session-title">${this.escapeHtml(session.title || 'New Session')}</span>
          <span class="session-agent-badge" title="${this.escapeHtml(badgeTitle)}">${this.escapeHtml(modeLabel)}</span>
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
        if (createBtnText) createBtnText.textContent = 'Start Assistant';
      } else {
        // LLM not available - show warning with action button
        if (manualSection) manualSection.classList.add('d-none');
        if (autoSection) autoSection.classList.add('d-none');
        if (llmWarning) llmWarning.classList.remove('d-none');
        if (createBtnText) createBtnText.textContent = 'Go to Settings';
        if (llmWarningMessage) {
          if (!this.chatSystemModelConfigured) {
            llmWarningMessage.textContent = 'Assistant mode requires a System Model to be configured.';
          } else {
            llmWarningMessage.textContent = 'Assistant mode requires an LLM provider. Please set up an API key or install Ollama.';
          }
        }
      }
    } else {
      // Direct agent chat mode
      if (manualSection) manualSection.classList.remove('d-none');
      if (autoSection) autoSection.classList.add('d-none');
      if (llmWarning) llmWarning.classList.add('d-none');
      if (createBtnText) createBtnText.textContent = 'Start Direct Chat';
    }
  },

  populateChatAgentSelect(agentSelect, agents, emptyLabel = 'No direct-chat agents available') {
    if (!agentSelect) return;

    const normalizedAgents = Array.isArray(agents) ? agents : [];
    if (normalizedAgents.length === 0) {
      agentSelect.innerHTML = `<option value="">${this.escapeHtml(emptyLabel)}</option>`;
      agentSelect.disabled = true;
      return;
    }

    agentSelect.innerHTML = normalizedAgents
      .map((agent) => `<option value="${this.escapeHtml(agent.name)}">${this.escapeHtml(agent.name)}</option>`)
      .join('');
    agentSelect.disabled = false;
  },

  updateAssistantModeText(workspaceId) {
    const autoModeText = document.getElementById('chatAutoModeText');
    if (!autoModeText) return;

    if (workspaceId) {
      autoModeText.textContent = 'Assistant stays in this workspace and uses workspace context by default. Switch to Direct agent chat only when you want a specific agent profile.';
      return;
    }

    autoModeText.textContent = 'Assistant starts as the system assistant. Pick a workspace to keep context scoped, or continue here before deciding where the work belongs.';
  },

  buildAssistantSessionTitle(initialMessage) {
    const message = String(initialMessage || '').trim();
    if (!message) return 'Assistant';
    return message.length > 50 ? `${message.slice(0, 47)}...` : message;
  },

  // Show create chat modal with workspace and agent selection
  async showCreateChatModal() {
    try {
      // Fetch agents
      const agents = await this.fetchAgents();

      // Check LLM availability to determine default mode
      await this.checkChatLlmAvailability();

      // Default to Auto mode if System Model is configured, otherwise Manual
      if (this.chatSystemModelConfigured && this.chatLlmAvailable) {
        this.chatAutoMode = true;
        const autoRadio = document.getElementById('chatConfigModeAuto');
        if (autoRadio) autoRadio.checked = true;
        this.handleChatModeChange('auto');
      } else {
        this.chatAutoMode = false;
        const manualRadio = document.getElementById('chatConfigModeManual');
        if (manualRadio) manualRadio.checked = true;
        this.handleChatModeChange('manual');
      }

      // Clear initial message textareas
      const autoMessageInput = document.getElementById('chatAutoMessage');
      if (autoMessageInput) autoMessageInput.value = '';
      const manualMessageInput = document.getElementById('chatManualMessage');
      if (manualMessageInput) manualMessageInput.value = '';

      // Reset file attachments
      this.chatPendingFiles = [];
      this.updateChatFilesPreview();
      this.bindChatFileEvents();

      // Populate workspace dropdown
      const workspaceSelect = document.getElementById('chatWorkspaceSelect');
      if (workspaceSelect) {
        workspaceSelect.innerHTML = '<option value="">No workspace (root)</option>';
        const appendWorkspaceOptions = (folders, depth = 0) => {
          (folders || []).forEach(folder => {
            if (!folder || !folder.id) return;
            const indent = depth > 0 ? `${'--'.repeat(depth)} ` : '';
            if (String(folder.kind || '').trim() !== 'group') {
              workspaceSelect.innerHTML += `<option value="${folder.id}">${this.escapeHtml(indent + (folder.name || 'Unnamed Workspace'))}</option>`;
            }
            appendWorkspaceOptions(folder.children || [], depth + 1);
          });
        };
        appendWorkspaceOptions(this.folders);
      }

      this.updateAssistantModeText('');

      const agentSelect = document.getElementById('chatAgentSelect');
      this.populateChatAgentSelect(agentSelect, agents, 'No direct-chat agents available');

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
      // Prefer agents configured in this workspace; fall back to global agents
      const agents = await this.fetchWorkspaceAgents(workspaceId);

      // Check LLM availability to determine default mode
      await this.checkChatLlmAvailability();

      // Default to Auto mode if System Model is configured, otherwise Manual
      if (this.chatSystemModelConfigured && this.chatLlmAvailable) {
        this.chatAutoMode = true;
        const autoRadio = document.getElementById('chatConfigModeAuto');
        if (autoRadio) autoRadio.checked = true;
        this.handleChatModeChange('auto');
      } else {
        this.chatAutoMode = false;
        const manualRadio = document.getElementById('chatConfigModeManual');
        if (manualRadio) manualRadio.checked = true;
        this.handleChatModeChange('manual');
      }

      // Clear initial message textareas
      const autoMessageInput = document.getElementById('chatAutoMessage');
      if (autoMessageInput) autoMessageInput.value = '';
      const manualMessageInput = document.getElementById('chatManualMessage');
      if (manualMessageInput) manualMessageInput.value = '';

      // Reset file attachments
      this.chatPendingFiles = [];
      this.updateChatFilesPreview();
      this.bindChatFileEvents();

      // Populate workspace dropdown
      const workspaceSelect = document.getElementById('chatWorkspaceSelect');
      if (workspaceSelect) {
        workspaceSelect.innerHTML = '<option value="">No workspace (root)</option>';
        const appendWorkspaceOptions = (folders, depth = 0) => {
          (folders || []).forEach(folder => {
            if (!folder || !folder.id) return;
            const indent = depth > 0 ? `${'--'.repeat(depth)} ` : '';
            if (String(folder.kind || '').trim() !== 'group') {
              const selected = folder.id === workspaceId ? ' selected' : '';
              workspaceSelect.innerHTML += `<option value="${folder.id}"${selected}>${this.escapeHtml(indent + (folder.name || 'Unnamed Workspace'))}</option>`;
            }
            appendWorkspaceOptions(folder.children || [], depth + 1);
          });
        };
        appendWorkspaceOptions(this.folders);
      }

      this.updateAssistantModeText(workspaceId);

      const agentSelect = document.getElementById('chatAgentSelect');
      this.populateChatAgentSelect(
        agentSelect,
        agents,
        workspaceId
          ? 'No direct-chat agents in this workspace'
          : 'No direct-chat agents available'
      );

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

    // Get initial messages before closing modal
    const autoMessageInput = document.getElementById('chatAutoMessage');
    const autoInitialMessage = this.chatAutoMode ? (autoMessageInput?.value?.trim() || '') : '';
    const manualMessageInput = document.getElementById('chatManualMessage');
    const manualInitialMessage = !this.chatAutoMode ? (manualMessageInput?.value?.trim() || '') : '';

    // Validate message in auto mode
    if (this.chatAutoMode && this.chatLlmAvailable && !autoInitialMessage) {
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
      // Check if workspace was pre-selected
      const workspaceSelect = document.getElementById('chatWorkspaceSelect');
      const preSelectedWorkspace = workspaceSelect?.value || '';
      const session = await this.createAssistantSession(
        preSelectedWorkspace,
        this.buildAssistantSessionTitle(autoInitialMessage)
      );

      if (session && session.id) {
        if (!preSelectedWorkspace) {
          this.autoModeSessionIds.add(session.id);
        }

        // Upload pending files first
        if (this.chatPendingFiles.length > 0) {
          await this.uploadChatPendingFiles(session.id);
        }

        // Send the initial message after a brief delay to ensure UI is ready
        if (autoInitialMessage && window.sendMessageToChat) {
          setTimeout(() => {
            window.sendMessageToChat(autoInitialMessage);
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
        if (window.Toast) {
          Toast.warning(workspaceId
            ? 'No direct-chat agent is available in this workspace. Add an agent or use Assistant.'
            : 'No direct-chat agent is available. Add an agent or use Assistant.');
        }
        return;
      }

      // Create the session
      let session;
      if (workspaceId) {
        session = await this.createSessionWithAgentInFolder(agentName, workspaceId);
      } else {
        session = await this.createSessionWithAgent(agentName);
      }

      // Upload pending files
      if (session && session.id && this.chatPendingFiles.length > 0) {
        await this.uploadChatPendingFiles(session.id);
      }

      // Send optional initial message for manual mode
      if (session && session.id && manualInitialMessage && window.sendMessageToChat) {
        setTimeout(() => {
          window.sendMessageToChat(manualInitialMessage);
        }, 100);
      }
    }

    // Clear pending files
    this.chatPendingFiles = [];
  },

  // Create new session - shows agent picker dialog first (legacy)
  async createNewSession() {
    // Now redirects to the modal
    this.showCreateChatModal();
  },

  // =============================================================================
  // Chat Modal File Attachment Functions
  // =============================================================================

  bindChatFileEvents() {
    const dropZone = document.getElementById('chatFileDropZone');
    const fileInput = document.getElementById('chatFileInput');

    if (dropZone) {
      // Remove old event listeners by cloning the element
      const newDropZone = dropZone.cloneNode(true);
      dropZone.parentNode.replaceChild(newDropZone, dropZone);

      newDropZone.addEventListener('click', () => {
        document.getElementById('chatFileInput')?.click();
      });
      newDropZone.addEventListener('dragover', (e) => {
        e.preventDefault();
        e.stopPropagation();
        newDropZone.classList.add('drag-active');
      });
      newDropZone.addEventListener('dragleave', (e) => {
        e.preventDefault();
        e.stopPropagation();
        newDropZone.classList.remove('drag-active');
      });
      newDropZone.addEventListener('drop', (e) => {
        e.preventDefault();
        e.stopPropagation();
        newDropZone.classList.remove('drag-active');
        const files = e.dataTransfer?.files;
        if (files && files.length > 0) {
          this.addChatFiles(Array.from(files));
        }
      });
    }

    if (fileInput) {
      const newFileInput = fileInput.cloneNode(true);
      fileInput.parentNode.replaceChild(newFileInput, fileInput);

      newFileInput.addEventListener('change', (e) => {
        const files = e.target?.files;
        if (files && files.length > 0) {
          this.addChatFiles(Array.from(files));
          e.target.value = '';
        }
      });
    }
  },

  addChatFiles(files) {
    const maxSize = 10 * 1024 * 1024; // 10MB

    files.forEach((file) => {
      if (file.size > maxSize) {
        if (window.Toast) {
          Toast.warning(`${file.name} exceeds 10MB limit`);
        }
        return;
      }
      // Avoid duplicates
      if (!this.chatPendingFiles.some((f) => f.name === file.name && f.size === file.size)) {
        this.chatPendingFiles.push(file);
      }
    });

    this.updateChatFilesPreview();
  },

  updateChatFilesPreview() {
    const container = document.getElementById('chatSelectedFiles');
    if (!container) return;

    if (this.chatPendingFiles.length === 0) {
      container.style.display = 'none';
      container.innerHTML = '';
      return;
    }

    container.style.display = 'block';

    const formatSize = (bytes) => {
      if (!bytes) return '';
      if (bytes < 1024) return bytes + ' B';
      if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
      return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
    };

    const items = this.chatPendingFiles.map((file, index) => `
      <div class="chat-selected-file-item" data-index="${index}">
        <span class="chat-file-name">${this.escapeHtml(file.name)}</span>
        <span class="chat-file-size">${formatSize(file.size)}</span>
        <button type="button" class="chat-file-remove" data-index="${index}" title="Remove">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19,6.41L17.59,5L12,10.59L6.41,5L5,6.41L10.59,12L5,17.59L6.41,19L12,13.41L17.59,19L19,17.59L13.41,12L19,6.41Z"/>
          </svg>
        </button>
      </div>
    `);

    container.innerHTML = items.join('');

    // Bind remove buttons
    container.querySelectorAll('.chat-file-remove').forEach((btn) => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        const index = parseInt(btn.dataset.index, 10);
        this.chatPendingFiles.splice(index, 1);
        this.updateChatFilesPreview();
      });
    });
  },

  async uploadChatPendingFiles(sessionId) {
    if (!sessionId || this.chatPendingFiles.length === 0) return;

    for (const file of this.chatPendingFiles) {
      try {
        await this.uploadFileToSession(sessionId, file);
      } catch (error) {
        console.error('Failed to upload file:', file.name, error);
        if (window.Toast) {
          Toast.error(`Failed to upload ${file.name}`);
        }
      }
    }
  },

  async uploadFileToSession(sessionId, file) {
    const formData = new FormData();
    formData.append('file', file);

    const response = await fetch(`/api/sessions/${sessionId}/files`, {
      method: 'POST',
      body: formData
    });

    if (!response.ok) {
      throw new Error(`Upload failed: ${response.status}`);
    }

    return response.json();
  },

  normalizeAgentList(rawAgents) {
    const source = Array.isArray(rawAgents) ? rawAgents : [];
    const result = [];
    const seen = new Set();

    source.forEach((agent) => {
      const isString = typeof agent === 'string';
      const name = String(isString ? agent : agent?.name || '').trim();
      if (!name) return;

      const key = name.toLowerCase();
      if (seen.has(key)) return;
      seen.add(key);

      result.push({
        name,
        model: isString ? '' : String(agent?.model || '').trim(),
        description: isString ? '' : String(agent?.description || '').trim()
      });
    });

    return result;
  },

  // Fetch available global agents
  async fetchAgents() {
    try {
      const response = await fetch('/api/agents');
      if (response.ok) {
        const data = await response.json();
        const agents = this.normalizeAgentList(data.agents);
        if (agents.length > 0) return agents;
      }

      // Fallback endpoint used by dashboard/workspace pages
      const fallback = await fetch('/api/agents/dashboard/list');
      if (!fallback.ok) return [];
      const fallbackData = await fallback.json();
      return this.normalizeAgentList(fallbackData.agents);
    } catch (error) {
      console.error('Failed to fetch agents:', error);
      return [];
    }
  },

  populateWorkspaceEntryAgentSelect() {
    // Entry agent is always auto-created as workspace manager — no UI needed.
  },

  async populateWorkspaceEntryAgentSelectFromAgents() {
    // Entry agent is always auto-created as workspace manager — no UI needed.
  },

  // Fetch agents scoped to a workspace; falls back to global agents
  async fetchWorkspaceAgents(workspaceId) {
    const globalAgents = await this.fetchAgents();
    if (!workspaceId) return globalAgents;

    try {
      const response = await fetch(`/api/orchestration/workspace?id=${encodeURIComponent(workspaceId)}`);
      if (!response.ok) return globalAgents;

      const workspace = await response.json();
      const names = [];
      const seen = new Set();

      const addName = (value) => {
        const name = String(value || '').trim();
        if (!name) return;
        const key = name.toLowerCase();
        if (seen.has(key)) return;
        seen.add(key);
        names.push(name);
      };

      if (Array.isArray(workspace?.agent_instances)) {
        workspace.agent_instances.forEach((instance) => addName(instance?.name));
      }
      if (Array.isArray(workspace?.agents)) {
        workspace.agents.forEach((name) => addName(name));
      }

      if (names.length === 0) return globalAgents;

      const globalByName = new Map(globalAgents.map((agent) => [String(agent.name || '').toLowerCase(), agent]));
      return names.map((name) => globalByName.get(name.toLowerCase()) || { name, model: '', description: '' });
    } catch (error) {
      console.error('Failed to fetch workspace agents:', error);
      return globalAgents;
    }
  },

  // Show agent picker dialog
  showAgentPickerDialog(agents) {
    // Remove existing modal if any
    const existingModal = document.getElementById('agentPickerModal');
    if (existingModal) existingModal.remove();

    const currentAgentName = this.getActiveSession()?.agent_name || '';

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
                      ${agent.name === currentAgentName ? '<span class="badge bg-primary">Pinned</span>' : ''}
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
  async createSessionWithAgent(agentName, openChat = true) {
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

        this.updateCurrentAgent(agentName);

        // Clear chat area for new session
        if (typeof clearChatHistory === 'function') {
          clearChatHistory();
        }

        if (openChat) this.openChatPanelIfAvailable();

        // Emit event for workspace hub to refresh
        if (window.EventBus) {
          EventBus.emit('session:created', { session: data.session, folderId: null });
        }

        return data.session;
      }
      return null;
    } catch (error) {
      console.error('Failed to create session:', error);
      return null;
    }
  },

  async createAssistantSession(folderId = '', title = 'Assistant', openChat = true) {
    try {
      const payload = {
        title: title || 'Assistant'
      };
      if (folderId) {
        payload.folder_id = folderId;
      }

      const response = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });

      if (!response.ok) throw new Error('Failed to create assistant session');

      const data = await response.json();
      if (data.session) {
        this.sessions.unshift(data.session);
        this.activeSessionId = data.session.id;
        this.saveActiveSession();
        this.renderSessions();

        this.updateCurrentAgent(data.session.agent_name || '');

        if (typeof clearChatHistory === 'function') {
          clearChatHistory();
        }

        if (openChat) this.openChatPanelIfAvailable();

        if (window.EventBus) {
          EventBus.emit('session:created', { session: data.session, folderId: folderId || null });
        }

        return data.session;
      }
      return null;
    } catch (error) {
      console.error('Failed to create assistant session:', error);
      return null;
    }
  },

  // Create a new session in a specific folder/workspace
  async createNewSessionInFolder(folderId) {
    return this.createAssistantSession(folderId, 'Assistant');
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
  async createSessionWithAgentInFolder(agentName, folderId, openChat = true) {
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

        this.updateCurrentAgent(agentName);

        // Clear chat area for new session
        if (typeof clearChatHistory === 'function') {
          clearChatHistory();
        }

        if (openChat) this.openChatPanelIfAvailable();

        // Emit event for workspace hub to refresh
        if (window.EventBus) {
          EventBus.emit('session:created', { session: data.session, folderId });
        }

        return data.session;
      }
      return null;
    } catch (error) {
      console.error('Failed to create session in folder:', error);
      return null;
    }
  },

  // Switch to a session
  // openChat: whether to open the chat panel (default true for user-initiated, false for init)
  async switchToSession(sessionId, openChat = true) {
    const chatArea = document.getElementById('chatArea');
    const hasRenderedMessages = chatArea && chatArea.children.length > 0;

    // If clicking on already-active session, just open the chat panel
    if (sessionId === this.activeSessionId && hasRenderedMessages) {
      if (openChat) this.openChatPanelIfAvailable();
      return;
    }

    try {
      // Load full session with messages
      const response = await fetch(`/api/sessions/${sessionId}`);
      if (!response.ok) throw new Error('Failed to load session');

      const session = await response.json();

      this.activeSessionId = sessionId;
      this.saveActiveSession();
      this.renderSessions();

      this.updateCurrentAgent(session.agent_name || '');

      // Restore messages to chat area
      this.restoreSessionMessages(session);

      if (openChat) this.openChatPanelIfAvailable();

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

  // Update the execution-agent display for the active session.
  updateCurrentAgent(agentName) {
    const normalizedAgentName = String(agentName || '').trim();

    const session = this.getActiveSession();
    this.updateChatInfoBar(session?.title, normalizedAgentName);

    this.updateChatModelForAgent(normalizedAgentName);

    // Update the navbar display (secondary/legacy)
    const agentElement = document.querySelector('#currentAgentDisplay span.fw-medium');
    if (agentElement) {
      agentElement.textContent = normalizedAgentName || 'Assistant';
    }

    // Also update any agent display in the header
    const agentHeader = document.querySelector('.agent-name');
    if (agentHeader) {
      agentHeader.textContent = normalizedAgentName || 'Assistant';
    }

    if (typeof window.refreshAgentDisplay === 'function') {
      window.refreshAgentDisplay();
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

    // Update session ID in header
    const sessionIdEl = document.getElementById('chatPanelSessionId');
    if (sessionIdEl) {
      if (this.activeSessionId) {
        sessionIdEl.textContent = this.activeSessionId;
        sessionIdEl.style.display = '';
      } else {
        sessionIdEl.style.display = 'none';
      }
    }

    // Update session title
    if (sessionTitleEl && sessionTitle) {
      sessionTitleEl.textContent = sessionTitle;
    }

    const hasSessionContext = Boolean(this.activeSessionId || sessionTitle);
    const displayAgentName = agentName || (hasSessionContext ? 'Assistant' : '');

    // Update agent name
    if (agentNameEl) {
      agentNameEl.textContent = displayAgentName;
    }
    if (typeof window.refreshChatWebSearchToggle === 'function') {
      window.refreshChatWebSearchToggle(agentName || 'Ori');
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
    if (sessionTitle || displayAgentName) {
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

  // Fetch and update the model for the active execution agent
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
    if (changeBtn) {
      changeBtn.style.display = 'none';
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
    this.editAgentMCPServers = [];
    this.clearEditAgentMessages();
    this.renderEditAgentMCPServers([], { workspaceScoped: true });
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
    this.editAgentCurrentProvider = String(agent.provider || '').trim();

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
    this.renderEditAgentMCPServers([], { workspaceScoped: true });
  },

  normalizeEditAgentMCPServers(servers, enabledOnly = false) {
    if (!Array.isArray(servers)) return [];

    const seen = new Set();
    const normalized = [];

    servers.forEach((server) => {
      let record = null;

      if (typeof server === 'string') {
        const name = server.trim();
        if (name) {
          record = {
            name,
            status: 'configured',
            tool_count: 0,
            enabled: true
          };
        }
      } else if (server && typeof server === 'object') {
        const name = typeof server.name === 'string' ? server.name.trim() : '';
        if (name) {
          const enabled = server.enabled !== false;
          const statusRaw = typeof server.status === 'string' ? server.status.trim().toLowerCase() : '';
          const toolCount = Number.isFinite(server.tool_count)
            ? server.tool_count
            : (Number.isFinite(server.toolCount) ? server.toolCount : 0);

          record = {
            name,
            status: statusRaw || (enabled ? 'configured' : 'stopped'),
            tool_count: toolCount,
            enabled
          };
        }
      }

      if (!record) return;
      if (enabledOnly && !record.enabled) return;

      const key = record.name.toLowerCase();
      if (seen.has(key)) return;
      seen.add(key);
      normalized.push(record);
    });

    return normalized;
  },

  getEditAgentMCPStatusClass(status) {
    const normalized = String(status || '').toLowerCase();
    if (normalized === 'running') return 'running';
    if (normalized === 'starting' || normalized === 'warming') return 'starting';
    if (normalized === 'error' || normalized === 'failed') return 'error';
    if (normalized === 'stopped' || normalized === 'disabled') return 'stopped';
    return 'configured';
  },

  renderEditAgentMCPServers(servers, options = {}) {
    const container = document.getElementById('editAgentMCPList');
    if (!container) return;

    if (options.workspaceScoped) {
      container.innerHTML = '<span class="agent-edit-mcp-empty">Workspace-scoped. Configure MCP bindings from the target workspace.</span>';
      return;
    }

    if (options.loading) {
      container.innerHTML = '<span class="agent-edit-mcp-empty">Loading MCP servers...</span>';
      return;
    }

    if (options.error) {
      container.innerHTML = '<span class="agent-edit-mcp-error">Could not load MCP server status.</span>';
      return;
    }

    const enabledServers = this.normalizeEditAgentMCPServers(servers, true);
    if (enabledServers.length === 0) {
      container.innerHTML = '<span class="agent-edit-mcp-empty">No MCP preferences saved for this agent.</span>';
      return;
    }

    container.innerHTML = enabledServers.map((server) => {
      const statusClass = this.getEditAgentMCPStatusClass(server.status);
      const statusLabel = statusClass === 'configured' ? 'configured' : (server.status || 'configured');
      const toolCount = Number.isFinite(server.tool_count) ? server.tool_count : 0;
      const toolText = toolCount > 0
        ? `${toolCount} tool${toolCount === 1 ? '' : 's'}`
        : 'tool count unknown';

      return `
        <span class="agent-edit-mcp-pill">
          <span class="agent-edit-mcp-name">${this.escapeHtml(server.name)}</span>
          <span class="agent-edit-mcp-meta">
            <span class="agent-edit-mcp-status ${statusClass}">${this.escapeHtml(statusLabel)}</span>
            <span>${this.escapeHtml(toolText)}</span>
          </span>
        </span>
      `;
    }).join('');
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
          { value: 'gemini-2.5-flash', label: 'gemini-2.5-flash', type: 'tool-calling' },
          { value: 'gemini-2.5-pro', label: 'gemini-2.5-pro', type: 'research' },
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
        option.setAttribute('data-provider', model.provider || provider.name || '');
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
      if (this.editAgentCurrentProvider) {
        customOption.setAttribute('data-provider', this.editAgentCurrentProvider);
      }
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
    const selectedModelOption = modelSelect?.selectedOptions?.[0] || null;
    const selectedProvider = String(selectedModelOption?.getAttribute('data-provider') || this.editAgentCurrentProvider || '').trim();
    const validProviders = new Set(['openai', 'codex', 'claude_code', 'claude', 'gemini', 'ollama', 'lmstudio', 'mlx_lm']);
    const resolvedProvider = validProviders.has(selectedProvider) ? selectedProvider : String(this.editAgentCurrentProvider || '').trim();

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
    if (resolvedProvider && validProviders.has(resolvedProvider)) {
      payload.llm_provider = resolvedProvider;
    }

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
      this.editAgentCurrentProvider = resolvedProvider || this.editAgentCurrentProvider;

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
    this.editAgentCurrentProvider = '';
    this.editAgentSelectedTags = [];
    this.editAgentMCPServers = [];
    const form = document.getElementById('editAgentForm');
    if (form) {
      form.reset();
    }
    this.updateEditAgentColorPreview('#4f46e5');
    this.renderEditAgentTags();
    this.renderEditAgentMCPServers([]);
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
    const persistedMessages = [];
    messages.forEach(msg => {
      const isUser = msg.role === 'user';
      persistedMessages.push({
        content: msg.content,
        isUser,
        timestamp: msg.created_at || new Date().toISOString()
      });
      if (typeof appendMessageToUI === 'function') {
        appendMessageToUI(msg.content, isUser);
      }
    });

    if (typeof window.replaceChatHistoryMessages === 'function') {
      window.replaceChatHistoryMessages(persistedMessages);
    }
  },

  // Delete session
  async deleteSession(sessionId) {
    if (!confirm('Are you sure you want to delete this session?')) return false;

    try {
      const response = await fetch(`/api/sessions/${sessionId}`, {
        method: 'DELETE'
      });

      if (!response.ok) throw new Error('Failed to delete session');

      const deletedSession = this.sessions.find(s => s.id === sessionId) || null;

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
      if (window.EventBus) {
        EventBus.emit('session:deleted', { sessionId, session: deletedSession });
      }
      return true;
    } catch (error) {
      console.error('Failed to delete session:', error);
      return false;
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

  getWorkspaceBootstrapFromModal() {
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
  },

  buildWorkspaceBootstrapSeedNote(workspaceBootstrap, _workspaceName) {
    if (!workspaceBootstrap || !workspaceBootstrap.hasAny) {
      return null;
    }

    const systemsSection = workspaceBootstrap.systemsList.length > 0
      ? workspaceBootstrap.systemsList.map((item) => `- ${item}`).join('\n')
      : '_Not specified._';
    const descriptionSection = workspaceBootstrap.description || workspaceBootstrap.goal || '_Not specified._';
    const capabilitiesSection = workspaceBootstrap.capabilities || '_Not specified._';
    const contextSection = workspaceBootstrap.context || '_Not specified._';

    return {
      name: 'Workspace Description',
      content: `# Workspace Description\n\n## Description\n${descriptionSection}\n\n## Apps and Systems\n${systemsSection}\n\n## Key Files or Context\n${contextSection}\n\n## Special Capabilities or Workflows\n${capabilitiesSection}\n`
    };
  },

  extractFolderNameFromPath(pathValue) {
    const trimmed = String(pathValue || '').trim().replace(/[\\/]+$/, '');
    if (!trimmed) return '';
    const parts = trimmed.split(/[\\/]/);
    return parts[parts.length - 1] || '';
  },

  prefillWorkspaceNameFromImportPath(pathValue) {
    const nameInput = document.getElementById('folderNameInput');
    if (!nameInput) return;

    const folderName = this.extractFolderNameFromPath(pathValue);
    if (!folderName) return;

    const currentName = nameInput.value.trim();
    const previousAutoName = nameInput.dataset.autofillName || '';
    if (!currentName || currentName === previousAutoName) {
      nameInput.value = folderName;
      nameInput.dataset.autofillName = folderName;
    }
  },

  setImportModeEnabled(enabled) {
    this.importModeEnabled = Boolean(enabled);
    const modal = document.getElementById('addFolderModal');
    if (modal) {
      modal.dataset.importMode = this.importModeEnabled ? 'true' : 'false';
    }

    const importToggle = document.getElementById('folderImportToggle');
    if (importToggle) {
      importToggle.checked = this.importModeEnabled;
    }

    const title = document.getElementById('folderModalTitle');
    if (title) {
      title.textContent = this.importModeEnabled ? 'Import Folder' : 'Create Workspace';
    }

    const card = document.getElementById('folderImportCard');
    if (card) {
      card.hidden = !this.importModeEnabled;
    }

    const section = document.getElementById('folderImportSection');
    if (section) {
      section.hidden = !this.importModeEnabled;
    }

    const createBtn = document.getElementById('createFolderBtn');
    if (createBtn && !this.isCreatingFolder) {
      if (window.WorkspaceBootstrapReview && typeof window.WorkspaceBootstrapReview.refreshPrimaryActionLabel === 'function') {
        window.WorkspaceBootstrapReview.refreshPrimaryActionLabel();
      } else {
        createBtn.textContent = this.importModeEnabled ? 'Import Folder' : 'Create Workspace';
      }
    }

    if (!this.importModeEnabled) {
      this.importAllowDuplicate = false;
      this.clearImportDuplicateWarning();
    }
  },

  clearImportDuplicateWarning() {
    const warning = document.getElementById('folderImportDuplicateWarning');
    const text = document.getElementById('folderImportDuplicateText');
    if (warning) warning.style.display = 'none';
    if (text) text.textContent = '';
    this.importDuplicateWorkspaceId = '';
    this.importDuplicateWorkspaceName = '';
  },

  showImportDuplicateWarning(duplicate) {
    const warning = document.getElementById('folderImportDuplicateWarning');
    const text = document.getElementById('folderImportDuplicateText');
    if (!warning || !text || !duplicate) return;

    const workspaceName = duplicate.workspace_name || duplicate.workspace_id || 'this workspace';
    text.textContent = `This folder is already linked to "${workspaceName}".`;
    warning.style.display = 'block';

    this.importDuplicateWorkspaceId = duplicate.workspace_id || '';
    this.importDuplicateWorkspaceName = duplicate.workspace_name || '';
  },

  async checkImportDuplicate(pathValue) {
    const path = String(pathValue || '').trim();
    if (!path || !this.importModeEnabled) {
      this.clearImportDuplicateWarning();
      return;
    }

    try {
      const response = await fetch(`/api/workspaces/import/check?path=${encodeURIComponent(path)}`);
      const result = await response.json().catch(() => ({}));
      if (!response.ok || !result.success) {
        this.clearImportDuplicateWarning();
        return;
      }

      if (result.duplicate && result.duplicate.found) {
        this.showImportDuplicateWarning(result.duplicate);
      } else {
        this.clearImportDuplicateWarning();
      }
    } catch (error) {
      console.error('Failed to check import duplicate:', error);
      this.clearImportDuplicateWarning();
    }
  },

  setImportBrowseLoading(isLoading) {
    const browseBtn = document.getElementById('folderImportBrowseBtn');
    if (!browseBtn) return;

    if (isLoading) {
      browseBtn.disabled = true;
      browseBtn.dataset.originalText = browseBtn.textContent || 'Browse';
      browseBtn.textContent = 'Selecting...';
      return;
    }

    browseBtn.disabled = false;
    browseBtn.textContent = browseBtn.dataset.originalText || 'Browse';
  },

  async browseImportFolderPath() {
    const importPathInput = document.getElementById('folderImportPathInput');
    if (!importPathInput) return;

    this.setImportBrowseLoading(true);
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
        this.showToast(result.error || 'Failed to open folder picker', 'error');
        return;
      }

      if (!result.selected || !result.path) {
        return;
      }

      importPathInput.value = result.path;
      this.importAllowDuplicate = false;
      this.clearImportDuplicateWarning();
      this.prefillWorkspaceNameFromImportPath(result.path);
      await this.checkImportDuplicate(result.path);
    } catch (error) {
      console.error('Failed to browse import path:', error);
      this.showToast('Failed to open folder picker', 'error');
    } finally {
      this.setImportBrowseLoading(false);
      importPathInput.focus();
    }
  },

  emitImportDuplicateActionTelemetry(action, pathValue) {
    const normalizedAction = String(action || '').trim();
    if (!normalizedAction) return;

    const payload = {
      action: normalizedAction,
      workspace_id: this.importDuplicateWorkspaceId || '',
      entry_point: this.importEntryPoint || 'workspace_hub_create',
      path: String(pathValue || '').trim()
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
      console.debug('sendBeacon failed for import duplicate telemetry:', error);
    }

    fetch(endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body,
      keepalive: true
    }).catch((error) => {
      console.debug('Failed to send import duplicate telemetry:', error);
    });
  },

  resetAddWorkspaceModalForm(options = {}) {
    const { preserveAskOri = false } = options;
    const modalElement = document.getElementById('addFolderModal');
    const nameInput = document.getElementById('folderNameInput');
    const descriptionInput = document.getElementById('folderDescriptionInput');
    const primaryGoalInput = document.getElementById('folderPrimaryGoalInput');
    const systemsInput = document.getElementById('folderSystemsInput');
    const contextInput = document.getElementById('folderContextInput');
    const parentSelect = document.getElementById('folderParentSelect');
    const keepSeedValues = Boolean(
      preserveAskOri &&
      modalElement &&
      String(modalElement.dataset.askOriPostCreate || '') === 'open_workspace_dashboard'
    );

    if (!keepSeedValues) {
      if (nameInput) nameInput.value = '';
      if (nameInput) nameInput.dataset.autofillName = '';
      if (descriptionInput) descriptionInput.value = '';
      if (primaryGoalInput) primaryGoalInput.value = '';
      if (systemsInput) systemsInput.value = '';
      if (contextInput) contextInput.value = '';
    }
    if (parentSelect) {
      const optionsHtml = ['<option value="">No group</option>'];
      const flattened = [];
      const walk = (nodes, depth) => {
        (nodes || []).forEach((node) => {
          if (!node || !node.id) return;
          if (String(node.kind || '').trim() === 'group') {
            flattened.push({ id: node.id, name: node.name || node.id, depth });
          }
          walk(node.children || [], depth + 1);
        });
      };
      walk(this.folders || [], 0);
      flattened.forEach((folder) => {
        const indent = folder.depth > 0 ? `${'--'.repeat(folder.depth)} ` : '';
        optionsHtml.push(`<option value="${this.escapeHtml(folder.id)}">${this.escapeHtml(indent + folder.name)}</option>`);
      });
      parentSelect.innerHTML = optionsHtml.join('');
      parentSelect.value = '';
    }
    document.querySelectorAll('#addFolderModal .folder-color-btn').forEach((btn) => btn.classList.remove('active'));
    const defaultColorBtn = document.querySelector('#addFolderModal .folder-color-btn[data-color=""]')
      || document.querySelector('#addFolderModal .folder-color-btn');
    if (defaultColorBtn) {
      defaultColorBtn.classList.add('active');
    }

    const importToggle = document.getElementById('folderImportToggle');
    const importPathInput = document.getElementById('folderImportPathInput');
    if (importToggle) importToggle.checked = false;
    if (importPathInput) importPathInput.value = '';
    this.importEntryPoint = 'workspace_hub_create';
    this.importAllowDuplicate = false;
    this.setImportBrowseLoading(false);
    this.setImportModeEnabled(false);
    this.clearImportDuplicateWarning();
    if (window.WorkspaceBootstrapReview && typeof window.WorkspaceBootstrapReview.reset === 'function') {
      window.WorkspaceBootstrapReview.reset();
    }
  },

  async createWorkspaceSeedTask(workspaceId, taskConfig) {
    const payload = {
      workspace_id: workspaceId,
      description: taskConfig.description,
      details: taskConfig.details || '',
      priority: Number(taskConfig.priority) || 1
    };

    if (taskConfig.to) {
      payload.to = taskConfig.to;
    }

    if (taskConfig.schedule) {
      payload.schedule = taskConfig.schedule;
      payload.schedule_enabled = Boolean(taskConfig.schedule_enabled);
      payload.schedule_name = taskConfig.schedule_name || '';
      if (!payload.to) {
        payload.to = 'ori';
      }
    }

    const response = await fetch('/api/orchestration/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });

    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to create setup task');
    }
  },

  async createWorkspaceSeedNote(workspaceId, noteConfig) {
    const response = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/notes`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: noteConfig.name || 'Starter Note',
        content: noteConfig.content || ''
      })
    });

    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || 'Failed to create setup note');
    }
  },

  // Create folder
  async createFolder() {
    if (this.isCreatingFolder) return;

    const nameInput = document.getElementById('folderNameInput');
    const descriptionInput = document.getElementById('folderDescriptionInput');
    const parentSelect = document.getElementById('folderParentSelect');
    const modalElement = document.getElementById('addFolderModal');
    const colorBtn = document.querySelector('#addFolderModal .folder-color-btn.active');
    const createBtn = document.getElementById('createFolderBtn');
    const importToggle = document.getElementById('folderImportToggle');
    const importPathInput = document.getElementById('folderImportPathInput');
    const workspaceBootstrap = this.getWorkspaceBootstrapFromModal();

    const importEnabled = this.importModeEnabled || Boolean(importToggle?.checked);
    const importPath = importPathInput?.value?.trim() || '';
    const name = nameInput?.value.trim() || '';
    const description = descriptionInput?.value.trim() || '';
    if (!name && !importEnabled) {
      this.showToast('Workspace name is required', 'warning');
      return;
    }
    if (importEnabled && !importPath) {
      this.showToast('Please enter or browse for a folder path to import', 'warning');
      return;
    }
    if (!description) {
      this.showToast('Workspace description is required', 'warning');
      descriptionInput?.focus();
      return;
    }

    if (window.WorkspaceBootstrapReview && typeof window.WorkspaceBootstrapReview.ensureReviewed === 'function') {
      const reviewOutcome = await window.WorkspaceBootstrapReview.ensureReviewed();
      if (!reviewOutcome.ready) {
        return;
      }
    }
    const parentId = parentSelect?.value?.trim() || '';
    const color = colorBtn?.dataset.color || '';
    const originalCreateLabel = createBtn ? createBtn.textContent : '';

    this.isCreatingFolder = true;
    if (createBtn) {
      createBtn.disabled = true;
      createBtn.textContent = importEnabled ? 'Importing...' : 'Creating...';
    }

    try {
      const payload = {
        name,
        description,
        parent_id: parentId,
        color
      };
      if (workspaceBootstrap.hasAny) {
        payload.workspace_bootstrap = {
          goal: workspaceBootstrap.description || workspaceBootstrap.goal,
          systems: workspaceBootstrap.systems,
          context: workspaceBootstrap.context
        };
      }
      const buildSlugConflictMessage = (conflict) => {
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
      };
      let endpoint = '/api/workspaces';
      if (importEnabled) {
        endpoint = '/api/workspaces/import';
        payload.path = importPath;
        payload.allow_duplicate = Boolean(this.importAllowDuplicate);
        payload.entry_point = this.importEntryPoint || 'workspace_hub_create';
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

        try {
          result = await response.json();
        } catch (parseErr) {
          result = {};
        }

        if (response.status === 409 && importEnabled && result.duplicate) {
          this.showImportDuplicateWarning(result.duplicate);
          this.showToast('This folder is already imported. Open the existing workspace or click "Import Anyway".', 'warning');
          return;
        }

        if (response.status === 409 && !importEnabled && result.conflict?.type === 'folder_slug') {
          const suggestedSlug = typeof result.conflict.suggested_slug === 'string'
            ? result.conflict.suggested_slug.trim()
            : '';
          if (!suggestedSlug) {
            throw new Error(result.error || 'Failed to create workspace');
          }

          const confirmed = window.confirm(buildSlugConflictMessage(result.conflict));
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
        throw new Error(result.error || fallbackMessage);
      }

      const createdWorkspaceId = result && result.folder && result.folder.id
        ? String(result.folder.id)
        : '';
      const askOriSeedNoteRaw = modalElement ? String(modalElement.dataset.askOriSeedNote || '') : '';
      const askOriSeedTaskRaw = modalElement ? String(modalElement.dataset.askOriSeedTask || '') : '';
      if (modalElement) {
        delete modalElement.dataset.askOriPostCreate;
        delete modalElement.dataset.askOriSeedNote;
        delete modalElement.dataset.askOriSeedTask;
      }

      let askOriSeedNote = null;
      let askOriSeedTask = null;
      if (askOriSeedNoteRaw) {
        try {
          askOriSeedNote = JSON.parse(askOriSeedNoteRaw);
        } catch (error) {
          console.warn('Failed to parse Assistant seed note:', error);
        }
      }
      if (askOriSeedTaskRaw) {
        try {
          askOriSeedTask = JSON.parse(askOriSeedTaskRaw);
        } catch (error) {
          console.warn('Failed to parse Assistant seed task:', error);
        }
      }
      const bootstrapWorkspaceName = name || this.extractFolderNameFromPath(importPath) || 'New Workspace';
      const workspaceBriefNote = this.buildWorkspaceBootstrapSeedNote(workspaceBootstrap, bootstrapWorkspaceName);
      let bootstrapApplyResult = {
        invitedAgents: 0,
        boundMCPs: 0,
        attachedSkills: 0,
        failures: []
      };

      const askOriSeedResult = {
        notesCreated: 0,
        tasksCreated: 0,
        errors: []
      };
      const workspaceDescriptionResult = {
        notesCreated: 0,
        errors: []
      };

      if (createdWorkspaceId && askOriSeedNote) {
        try {
          await this.createWorkspaceSeedNote(createdWorkspaceId, askOriSeedNote);
          askOriSeedResult.notesCreated += 1;
        } catch (error) {
          askOriSeedResult.errors.push(error);
        }
      }
      if (createdWorkspaceId && workspaceBriefNote && !askOriSeedNote) {
        try {
          await this.createWorkspaceSeedNote(createdWorkspaceId, workspaceBriefNote);
          workspaceDescriptionResult.notesCreated += 1;
        } catch (error) {
          workspaceDescriptionResult.errors.push(error);
        }
      }
      if (createdWorkspaceId && askOriSeedTask) {
        try {
          await this.createWorkspaceSeedTask(createdWorkspaceId, askOriSeedTask);
          askOriSeedResult.tasksCreated += 1;
        } catch (error) {
          askOriSeedResult.errors.push(error);
        }
      }

      if (
        createdWorkspaceId &&
        window.WorkspaceBootstrapReview &&
        typeof window.WorkspaceBootstrapReview.applyPlan === 'function'
      ) {
        bootstrapApplyResult = await window.WorkspaceBootstrapReview.applyPlan(createdWorkspaceId);
      }

      if (
        bootstrapApplyResult.invitedAgents > 0 ||
        bootstrapApplyResult.boundMCPs > 0 ||
        bootstrapApplyResult.attachedSkills > 0 ||
        askOriSeedResult.tasksCreated > 0 ||
        askOriSeedResult.notesCreated > 0 ||
        workspaceDescriptionResult.notesCreated > 0
      ) {
        const summaryParts = [];
        if (bootstrapApplyResult.invitedAgents > 0) summaryParts.push(`${bootstrapApplyResult.invitedAgents} agent${bootstrapApplyResult.invitedAgents === 1 ? '' : 's'} invited`);
        if (bootstrapApplyResult.boundMCPs > 0) summaryParts.push(`${bootstrapApplyResult.boundMCPs} MCP${bootstrapApplyResult.boundMCPs === 1 ? '' : 's'} bound`);
        if (bootstrapApplyResult.attachedSkills > 0) summaryParts.push(`${bootstrapApplyResult.attachedSkills} skill${bootstrapApplyResult.attachedSkills === 1 ? '' : 's'} attached`);
        if (askOriSeedResult.tasksCreated > 0) summaryParts.push(`${askOriSeedResult.tasksCreated} Assistant task`);
        if (askOriSeedResult.notesCreated > 0) summaryParts.push(`${askOriSeedResult.notesCreated} Assistant note`);
        if (workspaceDescriptionResult.notesCreated > 0) summaryParts.push('workspace description');
        const summaryText = summaryParts.join(', ');
        if (
          askOriSeedResult.errors.length > 0 ||
          workspaceDescriptionResult.errors.length > 0 ||
          bootstrapApplyResult.failures.length > 0
        ) {
          const firstFailure = bootstrapApplyResult.failures[0];
          this.showToast(
            `${importEnabled ? 'Workspace imported' : 'Workspace created'} with partial setup (${summaryText}).${firstFailure ? ` ${firstFailure}` : ''}`,
            'warning'
          );
        } else {
          this.showToast(`${importEnabled ? 'Workspace imported' : 'Workspace created'} with setup (${summaryText}).`, 'success');
        }
      } else {
        this.showToast(importEnabled ? 'Workspace imported successfully' : 'Workspace created successfully', 'success');
      }

      // Close modal
      const modal = bootstrap.Modal.getInstance(modalElement);
      modal?.hide();
      this.resetAddWorkspaceModalForm();

      // Navigate to the new workspace once the reviewed setup has been applied
      if (createdWorkspaceId) {
        window.location.href = `/workspaces/${encodeURIComponent(createdWorkspaceId)}`;
        return;
      }

      // Refresh folders
      await this.loadFolders();
      if (window.WorkspaceHub && typeof window.WorkspaceHub.loadWorkspaces === 'function') {
        await window.WorkspaceHub.loadWorkspaces();
      }
    } catch (error) {
      console.error('Failed to create folder:', error);
      this.showToast(error && error.message ? error.message : 'Failed to create workspace', 'error');
    } finally {
      this.isCreatingFolder = false;
      if (createBtn) {
        createBtn.disabled = false;
        createBtn.textContent = originalCreateLabel || 'Create';
      }
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

  // Restore sidebar state from localStorage
  restoreSidebarState() {
    const sidebar = document.getElementById('sessionSidebar');
    if (!sidebar) return;

    // Default to collapsed (true) if no preference saved
    const isCollapsed = localStorage.getItem('sessionSidebarCollapsed') !== 'false';
    if (isCollapsed) {
      sidebar.classList.add('collapsed');
    } else {
      sidebar.classList.remove('collapsed');
    }
  },

  // Toggle sidebar visibility
  toggleSidebar() {
    const sidebar = document.getElementById('sessionSidebar');
    if (sidebar) {
      sidebar.classList.toggle('collapsed');
      // Save preference
      localStorage.setItem('sessionSidebarCollapsed', sidebar.classList.contains('collapsed'));
    }
  },

  // Close sidebar
  closeSidebar() {
    const sidebar = document.getElementById('sessionSidebar');
    if (sidebar) {
      sidebar.classList.add('collapsed');
      localStorage.setItem('sessionSidebarCollapsed', 'true');
    }
  },

  // Open sidebar
  openSidebar() {
    const sidebar = document.getElementById('sessionSidebar');
    if (sidebar) {
      sidebar.classList.remove('collapsed');
      localStorage.setItem('sessionSidebarCollapsed', 'false');
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

  slugifyWorkspaceName(value) {
    return String(value || '')
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .replace(/-{2,}/g, '-');
  },

  buildWorkspaceSlugConflictMessage(conflict) {
    const requestedSlug = typeof conflict?.requested_slug === 'string' ? conflict.requested_slug.trim() : '';
    const suggestedSlug = typeof conflict?.suggested_slug === 'string' ? conflict.suggested_slug.trim() : '';
    const location = typeof conflict?.location === 'string' ? conflict.location.trim().replace(/[\\/]+$/, '') : '';
    const suggestedPath = location && suggestedSlug ? `${location}/${suggestedSlug}` : '';

    const parts = [
      `A workspace folder named "${requestedSlug || 'this workspace'}" already exists on disk.`
    ];
    if (suggestedSlug) {
      parts.push(`Rename this workspace with the folder name "${suggestedSlug}" instead?`);
    }
    if (suggestedPath) {
      parts.push(`Folder: ${suggestedPath}`);
    }
    return parts.join('\n\n');
  },

  // Rename folder via API
  async renameFolder(folderId, newName, folderSlug = '') {
    try {
      const payload = { name: newName };
      if (folderSlug) {
        payload.folder_slug = folderSlug;
      }

      const response = await fetch(`/api/workspaces/${folderId}/rename`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      const result = await response.json().catch(() => ({}));
      if (response.status === 409 && result?.conflict?.type === 'folder_slug') {
        const suggestedSlug = typeof result.conflict.suggested_slug === 'string'
          ? result.conflict.suggested_slug.trim()
          : '';
        if (suggestedSlug && window.confirm(this.buildWorkspaceSlugConflictMessage(result.conflict))) {
          return this.renameFolder(folderId, newName, suggestedSlug);
        }
        const cancelled = new Error(result?.error || 'Workspace rename cancelled');
        cancelled.cancelled = true;
        throw cancelled;
      }

      if (!response.ok) {
        throw new Error(result?.error || result?.message || 'Failed to rename workspace');
      }

      // Update local data
      const folder = this.findFolderById(folderId, this.folders);
      const updatedFolder = result?.folder || result?.workspace || null;
      if (folder) {
        if (updatedFolder && typeof updatedFolder === 'object') {
          Object.assign(folder, updatedFolder);
        } else {
          folder.name = newName;
        }
      }

      // Re-render
      this.renderFolderTree();
      const appliedSlug = String(updatedFolder?.folder_slug || '').trim();
      const expectedSlug = this.slugifyWorkspaceName(newName);
      this.showToast(
        appliedSlug && expectedSlug && appliedSlug !== expectedSlug
          ? `Workspace renamed. Folder saved as "${appliedSlug}".`
          : 'Workspace renamed',
        'success'
      );
    } catch (error) {
      console.error('Failed to rename folder:', error);
      if (!error?.cancelled) {
        this.showToast(error.message || 'Failed to rename workspace', 'error');
      }
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
    const assignableFolders = [];
    const collectAssignableFolders = (folders, depth = 0) => {
      (folders || []).forEach(folder => {
        if (!folder || !folder.id) return;
        if (String(folder.kind || '').trim() !== 'group') {
          assignableFolders.push({
            id: folder.id,
            name: folder.name,
            color: folder.color,
            depth
          });
        }
        collectAssignableFolders(folder.children || [], depth + 1);
      });
    };
    collectAssignableFolders(this.folders);

    // Render folder options
    container.innerHTML = `
      <div class="move-folder-item ${!currentFolderId ? 'selected' : ''}" data-folder-id="">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
          <path d="M19,20H5A2,2 0 0,1 3,18V6A2,2 0 0,1 5,4H9L11,6H19A2,2 0 0,1 21,8V18A2,2 0 0,1 19,20M5,18H19V10H5V18M5,8H9L7,6H5V8Z"/>
        </svg>
        No Workspace
      </div>
      ${assignableFolders.map(folder => `
        <div class="move-folder-item ${folder.id === currentFolderId ? 'selected' : ''}" data-folder-id="${folder.id}">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="${folder.color || 'currentColor'}" class="me-2">
            <path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>
          </svg>
          ${this.escapeHtml(`${folder.depth > 0 ? `${'--'.repeat(folder.depth)} ` : ''}${folder.name}`)}
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
  showAddWorkspaceModal(options = {}) {
    const modalElement = document.getElementById('addFolderModal');
    if (!modalElement) return;

    modalElement.dataset.pendingImportMode = options.importMode ? 'true' : 'false';
    modalElement.dataset.pendingEntryPoint = String(
      options.entryPoint || (options.importMode ? 'workspace_hub_import' : 'workspace_hub_create')
    );

    const modal = new bootstrap.Modal(modalElement);
    modal.show();
  },

  // Show import workspace modal
  showImportWorkspaceModal() {
    this.showAddWorkspaceModal({ importMode: true, entryPoint: 'workspace_hub_import' });
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
      <div class="modal fade" id="changeAgentModal" tabindex="-1" style="z-index: 10700;">
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

      this.updateCurrentAgent(session.agent_name || '');

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
        // After 3 attempts without a match, stop trying
        const count = this.autoModeMessageCounts.get(sessionId) || 0;
        if (count >= 6) {
          this.autoModeSessionIds.delete(sessionId);
          this.autoModeMessageCounts.delete(sessionId);
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
    const normalizedType = (type || 'info').toLowerCase();
    const toastType = normalizedType === 'danger' ? 'error' : normalizedType === 'warn' ? 'warning' : normalizedType;

    if (typeof window.notifyToast === 'function') {
      window.notifyToast(message, toastType);
      return;
    }

    if (window.Toast) {
      const toastFn = window.Toast[toastType];
      if (typeof toastFn === 'function') {
        toastFn(message);
        return;
      }
      if (typeof window.Toast.show === 'function') {
        window.Toast.show(message, toastType);
        return;
      }
    }

    if (typeof window.showToast === 'function') {
      window.showToast(message, type);
      return;
    }
  },

  // ============================================================================
  // Folder Notes
  // ============================================================================

  // Notes state (keyed by folder ID)
  notesByFolder: new Map(),
  currentNote: null,
  noteModalWorkspaceId: null,
  isNotePreviewMode: false,
  noteLiveActiveLineIndex: null,
  noteLiveActiveRange: null,
  noteLiveSelectionAnchorIndex: null,
  noteLiveSelectionFocusIndex: null,
  noteLivePointerDown: null,
  noteLiveCollapsedHeadings: new Set(),
  // Undo/redo lives in a NoteHistory instance (see note-editor.js). Lazily
  // created in resetNoteHistory so it's always defined before first use.
  noteHistory: null,

  // Auto-save state
  noteAutoSaveTimeout: null,
  noteIsDirty: false,
  noteAutoSaveDelay: 3000, // 3 seconds

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

      // Emit event for workspace hub to refresh
      if (window.EventBus) {
        EventBus.emit('note:created', { note: data.note, workspaceId: folderId });
      }

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
      for (const [, notes] of this.notesByFolder) {
        const index = notes.findIndex(n => n.id === noteId);
        if (index !== -1) {
          notes[index] = { ...notes[index], ...data.note };
          break;
        }
      }

      // Emit event for workspace hub to refresh
      if (window.EventBus) {
        const workspaceId = data.note?.workspace_id || data.note?.folder_id;
        EventBus.emit('note:updated', { note: data.note, workspaceId });
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
      for (const [, notes] of this.notesByFolder) {
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
    document.getElementById('noteCopyBtn')?.addEventListener('click', () => this.copyCurrentNoteContent());

    // Auto-save: listen for input changes on note title and content
    document.getElementById('noteNameInput')?.addEventListener('input', () => this.scheduleNoteAutoSave());
    document.getElementById('noteContentInput')?.addEventListener('input', () => {
      this.scheduleNoteAutoSave();
      if (this.isNotePreviewMode) {
        this.refreshNotePreview();
        this._scheduleNoteTocRebuild();
      }
    });

    // Auto-save on modal close (if there are unsaved changes)
    document.getElementById('noteEditorModal')?.addEventListener('hidden.bs.modal', () => {
      this.handleNoteModalClose();
    });

    // Note workspace selector
    document.getElementById('noteWorkspaceChange')?.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      this.showNoteWorkspaceSelector();
    });

    document.getElementById('noteWorkspaceSelect')?.addEventListener('change', (e) => {
      const workspaceId = e.target.value;
      if (!workspaceId) {
        this.noteModalWorkspaceId = null;
        return;
      }
      this.noteModalWorkspaceId = workspaceId;
      this.showNoteWorkspaceBadge(workspaceId, true);
    });

    // Note preview toggle
    document.getElementById('notePreviewToggle')?.addEventListener('click', () => this.toggleNotePreview());

    // Note AI generation toggle
    document.getElementById('noteGenerateAIToggle')?.addEventListener('click', () => this.toggleNoteAIPanel());

    // Rail collapse toggles (TOC + AI Assist). The buttons stay hidden until
    // the corresponding feature (tasks 3.0 / 4.0) reveals them.
    document.getElementById('noteTocToggle')?.addEventListener('click', () => this.toggleNoteTocRail());
    document.getElementById('noteAssistToggle')?.addEventListener('click', () => this.toggleNoteAssistRail());

    // Note AI generation buttons
    document.getElementById('noteAIGenerateBtn')?.addEventListener('click', () => this.generateNoteWithAI());
    document.getElementById('noteAICancelBtn')?.addEventListener('click', () => this.hideNoteAIPanel());

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

    // Try new format first: FILE_PATH (server storage)
    const filePathMatch = note.content.match(/<!-- FILE_PATH:(.+?) -->/);
    if (filePathMatch) {
      const [, serverPath] = filePathMatch;
      await this.attachFileFromServer(serverPath);
      return;
    }

    // Fall back to old format: FILE_DATA (base64 in note)
    const fileDataMatch = note.content.match(/<!-- FILE_DATA:(.+?):([^:]*):([A-Za-z0-9+/=]+) -->/);
    if (fileDataMatch) {
      const [, fileName, mimeType] = fileDataMatch;
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

  // Open note editor modal for a new note
  openNoteCreateModal(workspaceId = null) {
    // Reset auto-save state when opening a new note
    this.resetNoteAutoSaveState();

    this.currentNote = null;
    this.noteModalWorkspaceId = workspaceId;
    this.isNotePreviewMode = false;

    const modal = document.getElementById('noteEditorModal');
    if (!modal) return;

    const nameInput = document.getElementById('noteNameInput');
    const contentInput = document.getElementById('noteContentInput');
    const lastSaved = document.getElementById('noteLastSaved');
    const saveBtn = document.getElementById('saveNoteBtn');

    if (nameInput) nameInput.value = '';
    if (contentInput) {
      contentInput.value = '';
    }
    this.resetNoteHistory();
    this.setNotePreviewMode(true);
    if (lastSaved) lastSaved.textContent = '';
    if (saveBtn) saveBtn.textContent = 'Create Note';
    this.hideNoteVaultReferenceBadge();
    this._applyNoteRailState();
    this._initNoteAIAssist();
    this._setNoteAIAgentDefault(workspaceId).then(() => {
      window.NoteAIAssist?.onNoteOpened({
        noteId: null,
        workspaceId: workspaceId || null,
        agentId: this._resolveWorkspaceAgentId(),
      });
      this._openGeneratePanelByDefault();
    });

    if (workspaceId) {
      this.showNoteWorkspaceBadge(workspaceId, true);
    } else {
      this.showNoteWorkspaceSelector();
    }

    // Reset AI panel state
    this.hideNoteAIPanel();
    this.loadNoteAIAgents();

    const bsModal = new bootstrap.Modal(modal);
    bsModal.show();

    setTimeout(() => {
      nameInput?.focus();
    }, 100);
  },

  showNoteWorkspaceBadge(workspaceId, allowChange = false) {
    const badge = document.getElementById('noteWorkspaceBadge');
    const nameSpan = document.getElementById('noteWorkspaceName');
    const selector = document.getElementById('noteWorkspaceSelector');
    const changeBtn = document.getElementById('noteWorkspaceChange');

    if (selector) selector.style.display = 'none';
    if (!badge || !nameSpan) return;

    if (!workspaceId) {
      badge.style.display = 'none';
      return;
    }

    const folder = this.findFolderById(workspaceId, this.folders);
    nameSpan.textContent = folder?.name || 'Unknown Workspace';
    badge.style.display = 'block';
    if (changeBtn) changeBtn.style.display = allowChange ? 'inline-flex' : 'none';
  },

  normalizeNoteVaultReference(ref) { return window.NoteEditor?.normalizeVaultReference(ref) ?? null; },
  showNoteVaultReferenceBadge(ref) { window.NoteEditor?.showVaultReferenceBadge(ref); },
  hideNoteVaultReferenceBadge() { window.NoteEditor?.hideVaultReferenceBadge(); },

  showNoteWorkspaceSelector() {
    const badge = document.getElementById('noteWorkspaceBadge');
    const selector = document.getElementById('noteWorkspaceSelector');
    if (badge) badge.style.display = 'none';
    if (selector) selector.style.display = 'block';
    this.populateNoteWorkspaceDropdown();
  },

  populateNoteWorkspaceDropdown() {
    const select = document.getElementById('noteWorkspaceSelect');
    if (!select) return;

    let options = '<option value="">-- Select a workspace --</option>';
    const appendOptions = (folders, depth = 0) => {
      (folders || []).forEach(folder => {
        if (!folder || !folder.id) return;
        if (String(folder.kind || '').trim() !== 'group') {
          const indent = depth > 0 ? `${'-- '.repeat(depth)}` : '';
          const label = `${indent}${folder.name || 'Unnamed Workspace'}`;
          const selected = folder.id === this.noteModalWorkspaceId ? ' selected' : '';
          options += `<option value="${this.escapeHtml(folder.id)}"${selected}>${this.escapeHtml(label)}</option>`;
        }
        if (folder.children && folder.children.length > 0) {
          appendOptions(folder.children, depth + 1);
        }
      });
    };

    appendOptions(this.folders);
    select.innerHTML = options;
  },

  // Open note editor modal with a note object
  // If note doesn't have full content, fetches it first
  async openNoteEditorModal(note) {
    if (!note || !note.id) return;

    // If note only has preview, fetch full content
    let fullNote = note;
    if (note.preview !== undefined && note.content === undefined) {
      fullNote = await this.getNote(note.id);
      if (!fullNote) return;
    }

    return this._openNoteEditorWithNote(fullNote);
  },

  // Open note editor modal by ID
  // Opens a note in live-preview mode and scrolls to a specific heading by
  // text. Used by the global search palette (⌘K) when the user picks a
  // heading result. Falls back to plain openNoteEditor if the heading can't
  // be located in the loaded source.
  async openNoteEditorWithHeading(noteId, headingText) {
    await this.openNoteEditor(noteId);
    if (!this.isNotePreviewMode) this.setNotePreviewMode(true);
    const source = this.getNoteContentValue();
    const lines = source.split('\n');
    let cursor = 0;
    for (const line of lines) {
      if (/^#{1,6}\s+/.test(line) && line.includes(headingText)) {
        // Wait one frame for live-preview render to settle, then scroll.
        requestAnimationFrame(() => this._scrollNoteToHeading(cursor));
        return;
      }
      cursor += line.length + 1;
    }
  },

  async openNoteEditor(noteId) {
    const note = await this.getNote(noteId);
    if (!note) return;

    return this._openNoteEditorWithNote(note);
  },

  // Internal: Open note editor with a full note object
  _openNoteEditorWithNote(note) {
    // Reset auto-save state when opening a note
    this.resetNoteAutoSaveState();

    this.currentNote = note;
    this.noteModalWorkspaceId = note.workspace_id || note.folder_id || null;
    this.isNotePreviewMode = false;

    const modal = document.getElementById('noteEditorModal');
    const nameInput = document.getElementById('noteNameInput');
    const contentInput = document.getElementById('noteContentInput');
    const lastSaved = document.getElementById('noteLastSaved');
    const saveBtn = document.getElementById('saveNoteBtn');

    if (nameInput) nameInput.value = note.name;
    if (contentInput) {
      contentInput.value = note.content || '';
    }
    this.resetNoteHistory();
    this._applyNoteRailState();
    this._initNoteAIAssist();
    this._setNoteAIAgentDefault(this.noteModalWorkspaceId).then(() => {
      window.NoteAIAssist?.onNoteOpened({
        noteId: note.id,
        workspaceId: this.noteModalWorkspaceId,
        agentId: this._resolveWorkspaceAgentId(),
      });
      this._openGeneratePanelByDefault();
    });
    this.setNotePreviewMode(true);
    if (lastSaved) {
      lastSaved.textContent = `Last saved: ${this.formatDateTime(note.updated_at)}`;
    }
    if (saveBtn) saveBtn.textContent = 'Save';

    if (this.noteModalWorkspaceId) {
      this.showNoteWorkspaceBadge(this.noteModalWorkspaceId, false);
    } else {
      this.showNoteWorkspaceSelector();
    }
    this.showNoteVaultReferenceBadge(note.vault_reference);

    // Reset AI panel state
    this.hideNoteAIPanel();
    this.loadNoteAIAgents();

    const bsModal = new bootstrap.Modal(modal);
    bsModal.show();
  },

  // Create new note for folder (called from folder context menu)
  createNewNoteForFolder(folderId) {
    this.openNoteCreateModal(folderId);
  },

  async writeTextToClipboard(text) {
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
  },

  async copyCurrentNoteContent() {
    const contentInput = document.getElementById('noteContentInput');
    const noteContent = String(contentInput?.value || '');
    if (!noteContent.trim()) {
      this.showToast('Note is empty', 'info');
      return;
    }

    try {
      await this.writeTextToClipboard(noteContent);
      this.showToast('Note content copied', 'success');
    } catch (error) {
      console.error('Failed to copy note content:', error);
      this.showToast('Failed to copy note', 'error');
    }
  },

  // Save current note
  async saveCurrentNote() {
    // Clear any pending auto-save
    if (this.noteAutoSaveTimeout) {
      clearTimeout(this.noteAutoSaveTimeout);
      this.noteAutoSaveTimeout = null;
    }

    const nameInput = document.getElementById('noteNameInput');
    const contentInput = document.getElementById('noteContentInput');
    const noteName = nameInput?.value?.trim() || 'Untitled Note';
    const noteContent = contentInput?.value || '';

    // Show saving status
    this.updateNoteSaveStatus('saving');

    if (!this.currentNote) {
      const workspaceId = this.noteModalWorkspaceId;
      if (!workspaceId) {
        this.showToast('Please select a workspace first', 'warning');
        this.showNoteWorkspaceSelector();
        this.updateNoteSaveStatus('unsaved');
        return;
      }

      const created = await this.createNote(workspaceId, noteName, noteContent);
      if (created) {
        this.currentNote = created;
        this.noteIsDirty = false;
        this.updateNoteSaveStatus('saved');
        this.showToast('Note created', 'success');

        const modal = bootstrap.Modal.getInstance(document.getElementById('noteEditorModal'));
        modal?.hide();
      } else {
        this.updateNoteSaveStatus('error');
      }
      return;
    }

    const updates = {
      name: noteName,
      content: noteContent
    };

    const updated = await this.updateNote(this.currentNote.id, updates);
    if (updated) {
      this.currentNote = { ...this.currentNote, ...updated };
      this.noteIsDirty = false;
      this.updateNoteSaveStatus('saved');
      this.showToast('Note saved', 'success');

      // Refresh folder tree to show updated note name
      this.renderFolderTree();

      // Close the modal
      const modal = bootstrap.Modal.getInstance(document.getElementById('noteEditorModal'));
      modal?.hide();
    } else {
      this.updateNoteSaveStatus('error');
    }
  },

  // =============================================================================
  // Note Auto-Save
  // =============================================================================

  // Schedule auto-save with debounce
  scheduleNoteAutoSave() {
    // Clear any existing timeout
    if (this.noteAutoSaveTimeout) {
      clearTimeout(this.noteAutoSaveTimeout);
    }

    // Mark as dirty and show unsaved status
    this.noteIsDirty = true;
    this.updateNoteSaveStatus('unsaved');

    // Schedule auto-save after delay
    this.noteAutoSaveTimeout = setTimeout(() => {
      this.autoSaveNote();
    }, this.noteAutoSaveDelay);
  },

  // Perform auto-save
  async autoSaveNote() {
    if (!this.noteIsDirty) return;

    const nameInput = document.getElementById('noteNameInput');
    const contentInput = document.getElementById('noteContentInput');
    const noteName = nameInput?.value?.trim() || 'Untitled Note';
    const noteContent = contentInput?.value || '';

    // Show saving status
    this.updateNoteSaveStatus('saving');

    try {
      if (this.currentNote?.id) {
        // Update existing note
        const updates = { name: noteName, content: noteContent };
        const updated = await this.updateNote(this.currentNote.id, updates);
        if (updated) {
          this.currentNote = { ...this.currentNote, ...updated };
          this.noteIsDirty = false;
          this.updateNoteSaveStatus('saved');
          // Refresh folder tree to show updated note name
          this.renderFolderTree();
        } else {
          this.updateNoteSaveStatus('error');
        }
      } else {
        // Create new note (auto-create on first auto-save)
        const workspaceId = this.noteModalWorkspaceId;
        if (!workspaceId) {
          // Can't auto-create without workspace - show unsaved status
          this.updateNoteSaveStatus('unsaved');
          return;
        }
        const created = await this.createNote(workspaceId, noteName, noteContent);
        if (created) {
          this.currentNote = created;
          this.noteIsDirty = false;
          this.updateNoteSaveStatus('saved');
          // Update save button text since we now have a note
          const saveBtn = document.getElementById('saveNoteBtn');
          if (saveBtn) saveBtn.textContent = 'Save';
        } else {
          this.updateNoteSaveStatus('error');
        }
      }
    } catch (error) {
      console.error('Auto-save failed:', error);
      this.updateNoteSaveStatus('error');
    }
  },

  // Update save status indicator
  updateNoteSaveStatus(status) { window.NoteEditor?.updateSaveStatus(status); },

  // Handle modal close - save any unsaved changes
  handleNoteModalClose() {
    // Clear any pending auto-save timeout
    if (this.noteAutoSaveTimeout) {
      clearTimeout(this.noteAutoSaveTimeout);
      this.noteAutoSaveTimeout = null;
    }

    // If there are unsaved changes, save immediately (don't wait)
    if (this.noteIsDirty) {
      this.autoSaveNote();
    }

    // Reset dirty state
    this.noteIsDirty = false;
    this.updateNoteSaveStatus('saved');
  },

  // Reset auto-save state (called when opening a note)
  resetNoteAutoSaveState() {
    if (this.noteAutoSaveTimeout) {
      clearTimeout(this.noteAutoSaveTimeout);
      this.noteAutoSaveTimeout = null;
    }
    this.noteIsDirty = false;
    this.updateNoteSaveStatus('saved');
  },

  // =============================================================================
  // Note Editor Rails (TOC + AI Assist)
  // =============================================================================
  // Both rails are hidden by default. Tasks 3.0 (TOC) and 4.0 (AI Assist) call
  // showNoteTocRail() / showNoteAssistRail() once they have content to display.
  // The collapse toggle is per-rail and persisted in localStorage.

  _NOTE_RAIL_STORAGE_KEYS: {
    toc: 'note.toc.collapsed',
    assist: 'note.aiAssist.collapsed',
  },

  _readNoteRailCollapsed(rail) {
    try {
      return localStorage.getItem(this._NOTE_RAIL_STORAGE_KEYS[rail]) === '1';
    } catch (_) {
      return false;
    }
  },

  _writeNoteRailCollapsed(rail, collapsed) {
    try {
      localStorage.setItem(this._NOTE_RAIL_STORAGE_KEYS[rail], collapsed ? '1' : '0');
    } catch (_) {
      // ignore quota or privacy-mode failures
    }
  },

  _applyNoteRailCollapsed(rail) {
    const el = document.getElementById(rail === 'toc' ? 'noteTocRail' : 'noteAssistRail');
    const btn = document.getElementById(rail === 'toc' ? 'noteTocToggle' : 'noteAssistToggle');
    if (!el) return;
    const collapsed = this._readNoteRailCollapsed(rail);
    el.dataset.collapsed = collapsed ? 'true' : 'false';
    if (btn) btn.setAttribute('aria-pressed', collapsed ? 'true' : 'false');
  },

  // Apply persisted rail state on modal open. Called from openNoteEditor* and
  // openNoteCreateModal so the layout doesn't flash.
  _applyNoteRailState() {
    this._applyNoteRailCollapsed('toc');
    this._applyNoteRailCollapsed('assist');
  },

  // Toggle handlers used by the toolbar buttons.
  toggleNoteTocRail() {
    const collapsed = !this._readNoteRailCollapsed('toc');
    this._writeNoteRailCollapsed('toc', collapsed);
    this._applyNoteRailCollapsed('toc');
  },

  toggleNoteAssistRail() {
    const collapsed = !this._readNoteRailCollapsed('assist');
    this._writeNoteRailCollapsed('assist', collapsed);
    this._applyNoteRailCollapsed('assist');
  },

  // Show / hide methods are called by tasks 3.0 / 4.0 once they have content.
  showNoteTocRail() {
    const rail = document.getElementById('noteTocRail');
    const btn = document.getElementById('noteTocToggle');
    if (rail) rail.hidden = false;
    if (btn) btn.hidden = false;
    this._applyNoteRailCollapsed('toc');
  },

  hideNoteTocRail() {
    const rail = document.getElementById('noteTocRail');
    const btn = document.getElementById('noteTocToggle');
    if (rail) rail.hidden = true;
    if (btn) btn.hidden = true;
  },

  showNoteAssistRail() {
    const rail = document.getElementById('noteAssistRail');
    const btn = document.getElementById('noteAssistToggle');
    if (rail) rail.hidden = false;
    if (btn) btn.hidden = false;
    this._applyNoteRailCollapsed('assist');
  },

  hideNoteAssistRail() {
    const rail = document.getElementById('noteAssistRail');
    const btn = document.getElementById('noteAssistToggle');
    if (rail) rail.hidden = true;
    if (btn) btn.hidden = true;
  },

  // =============================================================================
  // Note AI Assist (selection action bar + sidebar wiring)
  // =============================================================================

  _initNoteAIAssist() {
    if (this._aiAssistInitialized) return;
    if (typeof window === 'undefined' || !window.NoteAIAssist) return;
    const bar = document.getElementById('noteAIActionBar');
    const rail = document.getElementById('noteAssistRail');
    if (!bar || !rail) return;
    window.NoteAIAssist.init({
      bar,
      rail,
      sessionsApi: {
        getNoteContent: () => this.getNoteContentValue(),
        setNoteContent: (value) => {
          this.setNoteContentValue(value);
          if (this.isNotePreviewMode) this.renderNoteLiveEditor();
          this._scheduleNoteTocRebuild?.();
        },
        pushUndo: () => this.pushNoteUndoState(),
        scheduleAutoSave: () => this.scheduleNoteAutoSave(),
        showToast: (msg, kind) => this.showToast?.(msg, kind),
        showAssistRail: () => this.showNoteAssistRail(),
        hideAssistRail: () => this.hideNoteAssistRail(),
      },
    });
    this._wireNoteSelectionTracking();
    this._wireNoteAIAgentChange();
    // Populate the agent dropdown eagerly so AI Assist has something to use
    // without requiring the user to open the whole-note Generate panel first.
    // Per-workspace preselect happens via _setNoteAIAgentDefault (called from
    // each modal-open path so the workspace's entry agent wins).
    this.loadNoteAIAgents();
    this._aiAssistInitialized = true;
  },

  // _setNoteAIAgentDefault picks the right agent for the dropdown given the
  // current workspace. Prefers the workspace's entry agent (if set and present
  // in the loaded options); otherwise falls back to the first available agent.
  // Called from openNoteCreateModal and _openNoteEditorWithNote so the picker
  // tracks the workspace, not the modal lifetime.
  async _setNoteAIAgentDefault(workspaceId) {
    // Make sure the dropdown is populated before we try to select.
    await this.loadNoteAIAgents();
    const select = document.getElementById('noteAIAgentSelect');
    if (!select) return;

    let entryAgent = null;
    if (workspaceId) {
      try {
        const r = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}`);
        if (r.ok) {
          const data = await r.json();
          entryAgent = data?.entry_agent_name || data?.workspace?.entry_agent_name || null;
        }
      } catch (_) {
        // network error — fall back to first-available below
      }
    }

    if (entryAgent) {
      const match = Array.from(select.options).find(o => o.value === entryAgent);
      if (match) {
        select.value = entryAgent;
        window.NoteAIAssist?.onAgentChanged(entryAgent);
        return;
      }
    }

    // Fall back: pick the first non-placeholder option if nothing is selected.
    if (!select.value) {
      const first = Array.from(select.options).find(o => o.value);
      if (first) select.value = first.value;
    }
    window.NoteAIAssist?.onAgentChanged(this._resolveWorkspaceAgentId());
  },

  _wireNoteAIAgentChange() {
    if (this._aiAgentChangeWired) return;
    document.getElementById('noteAIAgentSelect')?.addEventListener('change', () => {
      window.NoteAIAssist?.onAgentChanged(this._resolveWorkspaceAgentId());
    });
    this._aiAgentChangeWired = true;
  },

  _resolveWorkspaceAgentId() {
    // Prefer the agent dropdown the user already configured for whole-note
    // generation; later we can replace this with a true workspace-default.
    const select = document.getElementById('noteAIAgentSelect');
    return select?.value || null;
  },

  _wireNoteSelectionTracking() {
    if (this._aiAssistSelectionWired) return;
    const update = () => {
      if (typeof window === 'undefined' || !window.NoteAIAssist) return;
      window.NoteAIAssist.onSelectionChanged(this._readNoteSelection());
    };
    document.addEventListener('selectionchange', () => update());
    document.getElementById('noteContentInput')?.addEventListener('select', () => update());
    document.getElementById('noteContentInput')?.addEventListener('keyup', () => update());
    document.getElementById('noteContentInput')?.addEventListener('mouseup', () => update());
    // Hide the bar on Esc.
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') window.NoteAIAssist?.hideBar();
    });
    this._aiAssistSelectionWired = true;
  },

  // _readNoteSelection returns { text, source, range, anchorRect } or null.
  // `source` is 'textarea' or 'preview'. `range` is { start, end } in source
  // markdown coordinates if computable (preview path), else null.
  _readNoteSelection() {
    const modal = document.getElementById('noteEditorModal');
    if (!modal || !modal.classList.contains('show')) return null;

    // Plain-edit textarea path
    if (!this.isNotePreviewMode) {
      const ta = document.getElementById('noteContentInput');
      if (!ta || document.activeElement !== ta) return null;
      const start = ta.selectionStart;
      const end = ta.selectionEnd;
      if (start === end) return null;
      const text = ta.value.slice(start, end);
      const rect = ta.getBoundingClientRect();
      return {
        text,
        source: 'textarea',
        range: { start, end },
        // Anchor at the top-right of the textarea — caret position inside a
        // textarea isn't trivially available without canvas measurement.
        anchorRect: { top: rect.top, bottom: rect.top + 24, left: rect.right - 320, right: rect.right },
      };
    }

    // Live-preview path — use document selection
    const sel = window.getSelection();
    if (!sel || sel.rangeCount === 0 || sel.isCollapsed) return null;
    const previewPane = document.getElementById('notePreviewContent');
    if (!previewPane) return null;
    const range = sel.getRangeAt(0);
    if (!previewPane.contains(range.commonAncestorContainer)) return null;
    const text = sel.toString();
    if (!text || !text.trim()) return null;
    const anchorRect = range.getBoundingClientRect();

    // Try to map selection text back to source markdown coordinates by string
    // search. Cheap heuristic — good enough until v2 range stability lands.
    const source = this.getNoteContentValue();
    const idx = source.indexOf(text);
    const sourceRange = idx >= 0 ? { start: idx, end: idx + text.length } : null;

    return { text, source: 'preview', range: sourceRange, anchorRect };
  },

  // Called by AI Assist when the rail is collapsed by the user — keep state in sync.

  // =============================================================================
  // Note TOC (live-preview only)
  // =============================================================================
  // Builds an outline from the rendered Markdown headings via NoteTOC.buildOutline,
  // renders it into the left rail, syncs the active-section indicator on scroll,
  // and supports drag-reorder of sections in the underlying Markdown source.

  // Debounced wrapper called from the input listener — TOC rebuild is cheap
  // but we still avoid running on every keystroke.
  _scheduleNoteTocRebuild() {
    if (this._noteTocRebuildTimer) clearTimeout(this._noteTocRebuildTimer);
    this._noteTocRebuildTimer = setTimeout(() => {
      this._noteTocRebuildTimer = null;
      this._renderNoteTocOutline();
    }, 250);
  },

  _renderNoteTocOutline() {
    if (!this.isNotePreviewMode) {
      this.hideNoteTocRail();
      this._teardownNoteTocActiveObserver();
      return;
    }
    if (typeof window === 'undefined' || !window.NoteTOC) return;
    const rail = document.getElementById('noteTocRail');
    if (!rail) return;

    const empty = rail.querySelector('[data-role="empty"]');
    const content = rail.querySelector('[data-role="content"]');
    if (!empty || !content) return;

    const outline = window.NoteTOC.buildOutline(this.getNoteContentValue());
    const flat = [];
    const flatten = (nodes) => {
      for (const n of nodes) {
        flat.push(n);
        if (n.children && n.children.length) flatten(n.children);
      }
    };
    flatten(outline);

    this.showNoteTocRail();
    if (flat.length === 0) {
      empty.style.display = '';
      content.style.display = 'none';
      content.innerHTML = '';
      this._teardownNoteTocActiveObserver();
      return;
    }
    empty.style.display = 'none';
    content.style.display = '';

    const list = document.createElement('ul');
    list.className = 'note-toc-list';
    for (const h of flat) {
      const li = document.createElement('li');
      li.className = 'note-toc-item';
      li.dataset.level = String(h.level);
      li.dataset.position = String(h.position);

      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'note-toc-entry';
      btn.style.paddingLeft = `${8 + (h.level - 1) * 12}px`;
      btn.textContent = h.text;
      btn.title = h.text;
      btn.draggable = true;
      btn.addEventListener('click', () => this._scrollNoteToHeading(h.position));
      btn.addEventListener('dragstart', (e) => this._onNoteTocDragStart(e, h.position));
      btn.addEventListener('dragend', () => this._onNoteTocDragEnd());
      btn.addEventListener('dragover', (e) => this._onNoteTocDragOver(e, h.position));
      btn.addEventListener('drop', (e) => this._onNoteTocDrop(e, h.position));

      li.appendChild(btn);
      list.appendChild(li);
    }
    content.replaceChildren(list);

    this._attachNoteTocActiveObserver();
  },

  // Find the rendered heading element in the live-preview pane that matches
  // the source Markdown position, and smooth-scroll it to the top of the view.
  _scrollNoteToHeading(position) {
    const previewPane = document.getElementById('notePreviewContent');
    if (!previewPane) return;
    const target = this._findRenderedHeadingByPosition(position);
    if (!target) return;
    target.scrollIntoView({ behavior: 'smooth', block: 'start' });
  },

  // Live-preview rendering attaches one element per source line. Heading lines
  // carry `is-heading-N` plus `data-line-index` equal to the source line index;
  // we look up by source position by scanning lines until we hit the right offset.
  _findRenderedHeadingByPosition(position) {
    const previewPane = document.getElementById('notePreviewContent');
    if (!previewPane) return null;
    const source = this.getNoteContentValue();
    let lineIndex = 0;
    let cursor = 0;
    while (cursor < source.length && cursor < position) {
      const nl = source.indexOf('\n', cursor);
      if (nl < 0 || nl >= position) break;
      cursor = nl + 1;
      lineIndex++;
    }
    return previewPane.querySelector(`.note-live-line-rendered[data-line-index="${lineIndex}"]`);
  },

  _attachNoteTocActiveObserver() {
    this._teardownNoteTocActiveObserver();
    if (typeof IntersectionObserver === 'undefined') return;
    const previewPane = document.getElementById('notePreviewContent');
    if (!previewPane) return;
    const headingEls = previewPane.querySelectorAll('.note-live-line-rendered[class*="is-heading-"]');
    if (!headingEls.length) return;

    const visible = new Map();
    const observer = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        if (entry.isIntersecting) visible.set(entry.target, entry.intersectionRatio);
        else visible.delete(entry.target);
      }
      // Choose the topmost intersecting heading.
      let top = null;
      let topY = Infinity;
      for (const el of visible.keys()) {
        const rect = el.getBoundingClientRect();
        if (rect.top < topY) {
          topY = rect.top;
          top = el;
        }
      }
      if (top) this._setActiveTocEntry(top.dataset.lineIndex);
    }, { root: previewPane, rootMargin: '-10% 0px -85% 0px', threshold: [0, 0.1, 0.5] });

    headingEls.forEach(el => observer.observe(el));
    this._noteTocActiveObserver = observer;
  },

  _teardownNoteTocActiveObserver() {
    if (this._noteTocActiveObserver) {
      this._noteTocActiveObserver.disconnect();
      this._noteTocActiveObserver = null;
    }
  },

  _setActiveTocEntry(lineIndex) {
    if (lineIndex == null) return;
    const rail = document.getElementById('noteTocRail');
    if (!rail) return;
    const source = this.getNoteContentValue();
    let cursor = 0;
    let line = 0;
    while (line < Number(lineIndex) && cursor < source.length) {
      const nl = source.indexOf('\n', cursor);
      if (nl < 0) break;
      cursor = nl + 1;
      line++;
    }
    const target = rail.querySelector(`[data-position="${cursor}"]`);
    rail.querySelectorAll('.note-toc-item').forEach(el => {
      el.removeAttribute('aria-current');
      el.classList.remove('is-active');
    });
    if (target) {
      target.setAttribute('aria-current', 'location');
      target.classList.add('is-active');
    }
  },

  _onNoteTocDragStart(e, position) {
    if (!e.dataTransfer) return;
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', String(position));
    this._noteTocDragSource = position;
    e.currentTarget.closest('.note-toc-item')?.classList.add('is-dragging');
  },

  _onNoteTocDragOver(e, position) {
    if (this._noteTocDragSource == null || this._noteTocDragSource === position) return;
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
    const item = e.currentTarget.closest('.note-toc-item');
    if (!item) return;
    document.querySelectorAll('.note-toc-item.is-drop-target')
      .forEach(el => el.classList.remove('is-drop-target'));
    item.classList.add('is-drop-target');
  },

  _onNoteTocDragEnd() {
    document.querySelectorAll('.note-toc-item.is-dragging, .note-toc-item.is-drop-target')
      .forEach(el => el.classList.remove('is-dragging', 'is-drop-target'));
    this._noteTocDragSource = null;
  },

  _onNoteTocDrop(e, targetPosition) {
    e.preventDefault();
    const source = this._noteTocDragSource;
    this._onNoteTocDragEnd();
    if (source == null || source === targetPosition) return;
    if (typeof window === 'undefined' || !window.NoteTOC) return;
    const next = window.NoteTOC.moveHeadingRange(this.getNoteContentValue(), source, targetPosition);
    if (next == null || next === this.getNoteContentValue()) return;
    this.pushNoteUndoState();
    this.setNoteContentValue(next);
    this.scheduleNoteAutoSave();
    this.renderNoteLiveEditor();
    this._renderNoteTocOutline();
  },

  // =============================================================================
  // Note AI Generation
  // =============================================================================

  // _openGeneratePanelByDefault opens the Generate-with-AI panel on each note
  // modal open. The panel lives in the right rail and is the user's primary
  // entry point for AI-driven note creation/editing. If the user closes it,
  // it stays closed for the rest of that modal's lifetime.
  _openGeneratePanelByDefault() {
    const panel = document.getElementById('noteAIGeneratePanel');
    if (!panel) return;
    if (panel.style.display === 'none' || panel.style.display === '') {
      this.toggleNoteAIPanel();
    }
  },

  // Toggle AI generation panel visibility. The panel now lives inside the
  // right-side AI Assist rail; opening it temporarily hides the suggestion
  // stack so the form is the only visible content.
  toggleNoteAIPanel() {
    const panel = document.getElementById('noteAIGeneratePanel');
    const toggle = document.getElementById('noteGenerateAIToggle');
    if (!panel) return;

    const isVisible = panel.style.display !== 'none';
    if (isVisible) {
      this.hideNoteAIPanel();
    } else {
      panel.style.display = 'block';
      toggle?.classList.add('ai-active');
      this._showAssistRailForGenerate();
    }
  },

  // Hide AI generation panel and restore the rail to its normal state.
  hideNoteAIPanel() {
    const panel = document.getElementById('noteAIGeneratePanel');
    const toggle = document.getElementById('noteGenerateAIToggle');
    const promptInput = document.getElementById('noteAIPromptInput');
    const errorDiv = document.getElementById('noteAIError');
    const generatingDiv = document.getElementById('noteAIGenerating');
    const generateBtn = document.getElementById('noteAIGenerateBtn');

    if (panel) panel.style.display = 'none';
    if (toggle) toggle.classList.remove('ai-active');
    if (promptInput) promptInput.value = '';
    if (errorDiv) errorDiv.style.display = 'none';
    if (generatingDiv) generatingDiv.style.display = 'none';
    if (generateBtn) generateBtn.disabled = false;
    this._restoreAssistRailFromGenerate();
  },

  // Show the rail in "generate" mode: hides cards/empty/status so only the
  // generate panel is visible. Tracks whether we hid the rail entirely so
  // we can restore it correctly.
  _showAssistRailForGenerate() {
    this.showNoteAssistRail();
    const rail = document.getElementById('noteAssistRail');
    if (!rail) return;
    rail.classList.add('is-generating');
    this._assistRailGenerateActive = true;
  },

  _restoreAssistRailFromGenerate() {
    const rail = document.getElementById('noteAssistRail');
    if (rail) rail.classList.remove('is-generating');
    if (!this._assistRailGenerateActive) return;
    this._assistRailGenerateActive = false;
    // Let the AI Assist module decide whether to show the rail (cards present)
    // or hide it (no cards). render() handles both.
    window.NoteAIAssist?.render?.();
  },

  // Load agents for AI generation dropdown
  async loadNoteAIAgents() {
    const select = document.getElementById('noteAIAgentSelect');
    if (!select) return;

    try {
      const response = await fetch('/api/agents');
      if (!response.ok) throw new Error('Failed to load agents');
      const data = await response.json();

      // Clear existing options (keep first placeholder)
      select.innerHTML = '<option value="">Select an agent...</option>';

      // Add agents
      const agents = data.agents || [];
      agents.forEach(agent => {
        const option = document.createElement('option');
        option.value = agent.name;
        option.textContent = agent.name;
        select.appendChild(option);
      });
    } catch (error) {
      console.error('Failed to load agents for note AI:', error);
    }
  },

  // Generate note content with AI
  async generateNoteWithAI() {
    const agentSelect = document.getElementById('noteAIAgentSelect');
    const promptInput = document.getElementById('noteAIPromptInput');
    const errorDiv = document.getElementById('noteAIError');
    const generatingDiv = document.getElementById('noteAIGenerating');
    const generateBtn = document.getElementById('noteAIGenerateBtn');
    const nameInput = document.getElementById('noteNameInput');
    const contentInput = document.getElementById('noteContentInput');

    const agentId = agentSelect?.value || '';
    const prompt = promptInput?.value?.trim() || '';
    const workspaceId = this.noteModalWorkspaceId || this.currentNote?.workspace_id || this.currentNote?.folder_id || '';

    // Validate prompt
    if (!prompt) {
      if (errorDiv) {
        errorDiv.textContent = 'Please enter a prompt describing what you want the note to contain.';
        errorDiv.style.display = 'block';
      }
      return;
    }

    // Hide error, show loading
    if (errorDiv) errorDiv.style.display = 'none';
    if (generatingDiv) generatingDiv.style.display = 'block';
    if (generateBtn) generateBtn.disabled = true;

    try {
      const response = await fetch('/api/notes/generate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          prompt: prompt,
          workspace_id: workspaceId,
          agent_id: agentId
        })
      });

      if (!response.ok) {
        const error = await response.json();
        throw new Error(error.error || 'Failed to generate note');
      }

      const result = await response.json();

      // Update the note fields with generated content
      if (nameInput && result.title) {
        nameInput.value = result.title;
      }
      if (contentInput && result.content) {
        contentInput.value = result.content;
      }
      if (this.isNotePreviewMode) {
        this.setNotePreviewMode(true);
      }

      // Hide AI panel and show success
      this.hideNoteAIPanel();
      this.showToast('Note content generated', 'success');

    } catch (error) {
      console.error('Note AI generation failed:', error);
      if (errorDiv) {
        errorDiv.textContent = error.message || 'Failed to generate note content. Please try again.';
        errorDiv.style.display = 'block';
      }
    } finally {
      if (generatingDiv) generatingDiv.style.display = 'none';
      if (generateBtn) generateBtn.disabled = false;
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

  // Set preview mode for the note editor
  setNotePreviewMode(enabled) {
    this.isNotePreviewMode = Boolean(enabled);
    const editorContainer = document.querySelector('.note-editor-container');
    const contentInput = document.getElementById('noteContentInput');
    const previewContent = document.getElementById('notePreviewContent');
    const previewToggle = document.getElementById('notePreviewToggle');

    if (this.isNotePreviewMode) {
      editorContainer?.classList.add('is-previewing');
      if (contentInput) contentInput.style.display = 'none';
      if (previewContent) {
        previewContent.style.display = 'block';
        this.renderNoteLiveEditor();
      }
      if (previewToggle) {
        previewToggle.classList.add('active');
        previewToggle.setAttribute('aria-pressed', 'true');
        previewToggle.title = 'Use plain Markdown editor';
      }
      this._renderNoteTocOutline();
    } else {
      this.noteLiveActiveLineIndex = null;
      this.noteLiveActiveRange = null;
      this.noteLiveSelectionAnchorIndex = null;
      this.noteLiveSelectionFocusIndex = null;
      this.noteLivePointerDown = null;
      editorContainer?.classList.remove('is-previewing');
      if (contentInput) contentInput.style.display = 'block';
      if (previewContent) {
        previewContent.style.display = 'none';
        previewContent.innerHTML = '';
        previewContent.onmousedown = null;
        previewContent.onmouseup = null;
        previewContent.onclick = null;
        previewContent.onkeydown = null;
        previewContent.oninput = null;
        previewContent.onchange = null;
        previewContent.onpaste = null;
        previewContent.oncut = null;
        previewContent.onfocusout = null;
      }
      if (previewToggle) {
        previewToggle.classList.remove('active');
        previewToggle.setAttribute('aria-pressed', 'false');
        previewToggle.title = 'Show live preview';
      }
      this.hideNoteTocRail();
      this._teardownNoteTocActiveObserver();
    }
  },

  resetNoteHistory() {
    if (!this.noteHistory && window.NoteEditor) {
      this.noteHistory = new window.NoteEditor.NoteHistory({ limit: 100 });
    } else if (this.noteHistory) {
      this.noteHistory.reset();
    }
    this.noteLiveCollapsedHeadings = new Set();
  },

  getNoteContentValue() { return window.NoteEditor?.getContentValue() ?? ''; },
  setNoteContentValue(value) { window.NoteEditor?.setContentValue(value); },

  pushNoteUndoState() {
    if (!this.noteHistory) this.resetNoteHistory();
    if (!this.noteHistory) return;
    this.noteHistory.push(this.getNoteContentValue());
  },

  applyNoteHistoryState(value, options = {}) {
    if (this.noteHistory) this.noteHistory.applying = true;
    this.setNoteContentValue(value);
    this.noteLiveActiveRange = null;
    this.noteLiveActiveLineIndex = null;
    this.noteLiveSelectionAnchorIndex = null;
    this.noteLiveSelectionFocusIndex = null;
    this.noteLivePointerDown = null;
    this.noteLiveCollapsedHeadings = new Set();
    this.clearNoteLiveSelection();
    this.scheduleNoteAutoSave();
    if (this.noteHistory) this.noteHistory.applying = false;

    if (this.isNotePreviewMode) {
      this.renderNoteLiveEditor();
      const lines = this.getNoteContentLines();
      const focusIndex = Math.max(0, Math.min(options.focusLineIndex ?? 0, lines.length - 1));
      const focusLine = document.querySelector(`.note-live-line-rendered[data-line-index="${focusIndex}"]`);
      focusLine?.focus({ preventScroll: true });
    }
  },

  undoNoteEdit() {
    if (!this.noteHistory) return false;
    const previous = this.noteHistory.undo(this.getNoteContentValue());
    if (previous == null) return false;
    this.applyNoteHistoryState(previous);
    return true;
  },

  redoNoteEdit() {
    if (!this.noteHistory) return false;
    const next = this.noteHistory.redo(this.getNoteContentValue());
    if (next == null) return false;
    this.applyNoteHistoryState(next);
    return true;
  },

  isNoteUndoShortcut(event) { return window.NoteEditor?.isUndoShortcut(event) ?? false; },
  isNoteRedoShortcut(event) { return window.NoteEditor?.isRedoShortcut(event) ?? false; },

  handleNoteHistoryShortcut(event) {
    if (this.isNoteUndoShortcut(event)) {
      if (this.undoNoteEdit()) {
        event.preventDefault();
        return true;
      }
      return false;
    }

    if (this.isNoteRedoShortcut(event)) {
      if (this.redoNoteEdit()) {
        event.preventDefault();
        return true;
      }
      return false;
    }

    return false;
  },

  refreshNotePreview() {
    this.renderNoteLiveEditor();
  },

  getNoteContentLines() { return window.NoteEditor?.getContentLines() ?? ['']; },
  setNoteContentLines(lines) { window.NoteEditor?.setContentLines(lines); },

  // The following four helpers were moved to note-editor.js as the first slice
  // of the v2 task 1.0 extraction. The thin delegators stay here so existing
  // callers (this.noteHeadingLevel(...), etc.) keep working untouched.
  noteHeadingLevel(line) { return window.NoteEditor?.parseHeadingLevel(line) ?? 0; },
  noteLineKindClass(line) { return window.NoteEditor?.lineKindClass(line) ?? ''; },
  parseNoteTaskLine(line) { return window.NoteEditor?.parseTaskLine(line) ?? null; },
  normalizeCompactTaskListMarkdown(text) {
    return window.NoteEditor?.normalizeCompactTaskListMarkdown(text) ?? String(text || '');
  },

  // pruneNoteCollapsedHeadings still touches `this.noteLiveCollapsedHeadings`
  // (state that hasn't been migrated yet), so it stays here for now.
  pruneNoteCollapsedHeadings(lines) {
    for (const index of Array.from(this.noteLiveCollapsedHeadings || [])) {
      if (!Number.isInteger(index) || index < 0 || index >= lines.length || this.noteHeadingLevel(lines[index]) === 0) {
        this.noteLiveCollapsedHeadings.delete(index);
      }
    }
  },

  renderNoteLiveEditor(options = {}) {
    const previewContent = document.getElementById('notePreviewContent');
    if (!previewContent || !this.isNotePreviewMode) return;

    const lines = this.getNoteContentLines();
    this.pruneNoteCollapsedHeadings(lines);
    const activeRange = this.noteLiveActiveRange
      ? {
          start: Math.max(0, Math.min(this.noteLiveActiveRange.start, lines.length - 1)),
          end: Math.max(0, Math.min(this.noteLiveActiveRange.end, lines.length - 1))
        }
      : null;
    const focusLineIndex = Number.isInteger(options.focusLineIndex)
      ? options.focusLineIndex
      : this.noteLiveActiveLineIndex;
    const activeLineIndex = !activeRange && Number.isInteger(focusLineIndex)
      ? Math.max(0, Math.min(focusLineIndex, lines.length - 1))
      : null;
    this.noteLiveActiveLineIndex = activeLineIndex;

    const html = [];
    let hiddenByHeadingLevel = 0;
    for (let index = 0; index < lines.length; index += 1) {
      const headingLevel = this.noteHeadingLevel(lines[index]);
      if (hiddenByHeadingLevel > 0) {
        if (headingLevel > 0 && headingLevel <= hiddenByHeadingLevel) {
          hiddenByHeadingLevel = 0;
        } else {
          continue;
        }
      }

      if (activeRange && index === activeRange.start) {
        html.push(this.renderNoteLiveRangeInput(lines.slice(activeRange.start, activeRange.end + 1).join('\n'), activeRange.start, activeRange.end));
        index = activeRange.end;
        continue;
      }
      if (index === activeLineIndex) {
        html.push(this.renderNoteLiveInputLine(lines[index], index));
        continue;
      }
      html.push(this.renderNoteLiveRenderedLine(lines[index], index));

      if (headingLevel > 0 && this.noteLiveCollapsedHeadings.has(index)) {
        hiddenByHeadingLevel = headingLevel;
      }
    }
    previewContent.innerHTML = html.join('');

    this.bindNoteLiveEditorEvents(previewContent);

    if (activeRange) {
      const input = previewContent.querySelector('.note-live-block-input');
      if (input) {
        const cursorPosition = Number.isInteger(options.cursorPosition)
          ? Math.max(0, Math.min(options.cursorPosition, input.value.length))
          : input.value.length;
        input.focus();
        input.setSelectionRange(cursorPosition, cursorPosition);
        this.resizeNoteLiveInput(input);
      }
    } else if (activeLineIndex !== null) {
      const input = previewContent.querySelector(`.note-live-line-input[data-line-index="${activeLineIndex}"]`);
      if (input) {
        const cursorPosition = Number.isInteger(options.cursorPosition)
          ? Math.max(0, Math.min(options.cursorPosition, input.value.length))
          : input.value.length;
        input.focus();
        input.setSelectionRange(cursorPosition, cursorPosition);
        this.resizeNoteLiveInput(input);
      }
    }
  },

  renderNoteLiveInputLine(line, index) {
    const kindClass = this.noteLineKindClass(line);
    const className = ['note-live-line-input', kindClass].filter(Boolean).join(' ');
    return `
      <div class="note-live-line is-editing" data-line-index="${index}">
        <textarea class="${className}" data-line-index="${index}" rows="1" spellcheck="true">${this.escapeHtml(line)}</textarea>
      </div>
    `;
  },

  renderNoteLiveRangeInput(markdown, startIndex, endIndex) {
    return `
      <div class="note-live-line is-editing is-block-editing" data-line-index="${startIndex}" data-line-end="${endIndex}">
        <textarea class="note-live-line-input note-live-block-input" data-line-start="${startIndex}" data-line-end="${endIndex}" spellcheck="true">${this.escapeHtml(markdown)}</textarea>
      </div>
    `;
  },

  renderNoteLiveRenderedLine(line, index) {
    const kindClass = this.noteLineKindClass(line);
    const emptyClass = line ? '' : ' is-empty';
    const headingLine = this.renderNoteHeadingLine(line, index);
    const taskLine = this.renderNoteTaskLine(line, index);
    return `
      <div class="note-live-line note-live-line-rendered ${kindClass}${emptyClass}" data-line-index="${index}" tabindex="0">
        ${line ? (headingLine || taskLine || this.renderMarkdownLine(line)) : '<br>'}
      </div>
    `;
  },

  renderNoteHeadingLine(line, index) {
    const headingLevel = this.noteHeadingLevel(line);
    if (headingLevel === 0) return '';
    const collapsed = this.noteLiveCollapsedHeadings.has(index);
    const expandedValue = collapsed ? 'false' : 'true';
    const summary = collapsed ? '<span class="note-heading-fold-summary">...</span>' : '';
    return `
      <div class="note-heading-line">
        <button type="button" class="note-heading-fold" data-line-index="${index}" aria-expanded="${expandedValue}" title="${collapsed ? 'Expand section' : 'Collapse section'}">
          <span aria-hidden="true">${collapsed ? '›' : '⌄'}</span>
        </button>
        <div class="note-heading-content">${this.renderMarkdownLine(line)}</div>
        ${summary}
      </div>
    `;
  },

  renderNoteTaskLine(line, index) {
    const task = this.parseNoteTaskLine(line);
    if (!task) return '';
    const checked = task.checked ? ' checked' : '';
    const content = task.text ? this.renderInlineMarkdown(task.text) : '';
    return `
      <span class="note-task-line">
        <input type="checkbox" class="note-task-checkbox" data-line-index="${index}"${checked} aria-label="Toggle checkbox">
        <span class="note-task-content">${content}</span>
      </span>
    `;
  },

  bindNoteLiveEditorEvents(previewContent) {
    previewContent.onmousedown = (event) => {
      const target = event.target;
      if (!target || typeof target.closest !== 'function') {
        this.noteLivePointerDown = null;
        return;
      }
      const renderedLine = target.closest('.note-live-line-rendered');
      this.noteLivePointerDown = renderedLine && previewContent.contains(renderedLine)
        ? {
            lineIndex: Number(renderedLine.dataset.lineIndex),
            x: event.clientX,
            y: event.clientY
          }
        : null;
    };

    previewContent.onmouseup = () => {
      window.setTimeout(() => {
        if (this.hasNoteLiveTextSelection(previewContent)) {
          previewContent.focus({ preventScroll: true });
        }
      }, 0);
    };

    previewContent.onclick = (event) => {
      const target = event.target;
      if (!target || typeof target.closest !== 'function') return;
      const headingFold = target.closest('.note-heading-fold');
      if (headingFold && previewContent.contains(headingFold)) {
        event.preventDefault();
        event.stopPropagation();
        this.toggleNoteHeadingFold(Number(headingFold.dataset.lineIndex));
        return;
      }
      if (target.closest('.note-task-checkbox')) {
        event.stopPropagation();
        return;
      }
      if (target === previewContent) {
        if (this.hasNoteLiveTextSelection(previewContent)) return;
        const lines = this.getNoteContentLines();
        this.activateNoteLiveLine(lines.length - 1, (lines[lines.length - 1] || '').length);
        return;
      }
      const renderedLine = target.closest('.note-live-line-rendered');
      if (!renderedLine || !previewContent.contains(renderedLine)) return;
      const lineIndex = Number(renderedLine.dataset.lineIndex);
      if (!Number.isInteger(lineIndex)) return;

      if (event.shiftKey) {
        event.preventDefault();
        this.selectNoteLiveLineRange(this.noteLiveSelectionAnchorIndex ?? lineIndex, lineIndex);
        return;
      }

      if (this.hasNoteLiveTextSelection(previewContent) || this.didNoteLivePointerDrag(event)) {
        this.noteLiveSelectionAnchorIndex = lineIndex;
        this.noteLivePointerDown = null;
        return;
      }

      this.clearNoteLiveSelection();
      this.activateNoteLiveLine(lineIndex);
    };

    previewContent.onkeydown = (event) => {
      if (this.handleNoteHistoryShortcut(event)) return;

      const target = event.target;
      if (!target || typeof target.closest !== 'function') return;
      if (target.closest('.note-heading-fold')) return;
      const checkbox = target.closest('.note-task-checkbox');
      if (checkbox && previewContent.contains(checkbox)) {
        if (event.key === 'Enter') {
          event.preventDefault();
          this.toggleNoteTaskLine(Number(checkbox.dataset.lineIndex), !checkbox.checked);
        }
        return;
      }
      const blockInput = target.closest('.note-live-block-input');
      if (blockInput && previewContent.contains(blockInput)) {
        this.handleNoteLiveRangeInputKeydown(event, blockInput);
        return;
      }
      const input = target.closest('.note-live-line-input');
      if (input && previewContent.contains(input)) {
        this.handleNoteLiveInputKeydown(event, input);
        return;
      }

      const renderedLine = target.closest('.note-live-line-rendered');
      const selectedRange = this.getNoteLiveSelectedLineRange(previewContent);

      if (selectedRange) {
        if (event.key === 'Backspace' || event.key === 'Delete') {
          event.preventDefault();
          this.deleteNoteLiveLineRange(selectedRange);
          return;
        }

        if (event.key === 'Enter') {
          event.preventDefault();
          this.editNoteLiveLineRange(selectedRange);
          return;
        }

        if (this.isNoteLivePrintableKey(event)) {
          event.preventDefault();
          this.replaceNoteLiveLineRange(selectedRange, event.key);
          return;
        }
      }

      if (!renderedLine || !previewContent.contains(renderedLine)) return;
      const lineIndex = Number(renderedLine.dataset.lineIndex);
      if (!Number.isInteger(lineIndex)) return;

      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'a') {
        event.preventDefault();
        const lines = this.getNoteContentLines();
        this.selectNoteLiveLineRange(0, Math.max(0, lines.length - 1));
        return;
      }

      if (event.key === 'Escape') {
        this.clearNoteLiveSelection();
        return;
      }

      if (event.shiftKey && (event.key === 'ArrowUp' || event.key === 'ArrowDown')) {
        event.preventDefault();
        const direction = event.key === 'ArrowUp' ? -1 : 1;
        const lines = this.getNoteContentLines();
        const nextIndex = Math.max(0, Math.min(lineIndex + direction, lines.length - 1));
        this.selectNoteLiveLineRange(this.noteLiveSelectionAnchorIndex ?? lineIndex, nextIndex);
        return;
      }

      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        this.clearNoteLiveSelection();
        this.activateNoteLiveLine(lineIndex);
      }
    };

    previewContent.oninput = (event) => {
      const target = event.target;
      if (!target || typeof target.closest !== 'function') return;
      const blockInput = target.closest('.note-live-block-input');
      if (blockInput && previewContent.contains(blockInput)) {
        this.handleNoteLiveRangeInputChange(blockInput);
        return;
      }
      const input = target.closest('.note-live-line-input');
      if (input && previewContent.contains(input)) {
        this.handleNoteLiveInputChange(input);
      }
    };

    previewContent.onchange = (event) => {
      const target = event.target;
      if (!target || typeof target.closest !== 'function') return;
      const checkbox = target.closest('.note-task-checkbox');
      if (!checkbox || !previewContent.contains(checkbox)) return;
      this.toggleNoteTaskLine(Number(checkbox.dataset.lineIndex), checkbox.checked);
    };

    previewContent.onpaste = (event) => {
      const target = event.target;
      if (target && typeof target.closest === 'function' && target.closest('.note-live-line-input')) return;
      const selectedRange = this.getNoteLiveSelectedLineRange(previewContent);
      if (!selectedRange) return;
      const pastedText = event.clipboardData?.getData('text/plain') || '';
      event.preventDefault();
      this.replaceNoteLiveLineRange(selectedRange, pastedText);
    };

    previewContent.oncut = (event) => {
      const target = event.target;
      if (target && typeof target.closest === 'function' && target.closest('.note-live-line-input')) return;
      const selectedRange = this.getNoteLiveSelectedLineRange(previewContent);
      if (!selectedRange) return;
      const lines = this.getNoteContentLines();
      const markdown = lines.slice(selectedRange.start, selectedRange.end + 1).join('\n');
      event.clipboardData?.setData('text/plain', markdown);
      event.preventDefault();
      this.deleteNoteLiveLineRange(selectedRange);
    };

    previewContent.onfocusout = (event) => {
      if (event.relatedTarget && previewContent.contains(event.relatedTarget)) return;
      window.setTimeout(() => {
        const activeElement = document.activeElement;
        if (activeElement && previewContent.contains(activeElement)) return;
        if (this.isNotePreviewMode && (this.noteLiveActiveLineIndex !== null || this.noteLiveActiveRange !== null)) {
          this.noteLiveActiveLineIndex = null;
          this.noteLiveActiveRange = null;
          this.renderNoteLiveEditor();
        }
      }, 0);
    };
  },

  noteLiveSelectionContains(container, node) {
    if (!container || !node) return false;
    const element = node.nodeType === Node.ELEMENT_NODE ? node : node.parentElement;
    return Boolean(element && container.contains(element));
  },

  hasNoteLiveTextSelection(container) {
    const selection = window.getSelection?.();
    if (!selection || selection.isCollapsed) return false;
    return this.noteLiveSelectionContains(container, selection.anchorNode)
      && this.noteLiveSelectionContains(container, selection.focusNode);
  },

  didNoteLivePointerDrag(event) {
    const start = this.noteLivePointerDown;
    if (!start) return false;
    const distanceX = Math.abs(event.clientX - start.x);
    const distanceY = Math.abs(event.clientY - start.y);
    return distanceX > 4 || distanceY > 4;
  },

  clearNoteLiveSelection() {
    const selection = window.getSelection?.();
    if (selection && !selection.isCollapsed) {
      selection.removeAllRanges();
    }
    this.noteLiveSelectionFocusIndex = null;
  },

  selectNoteLiveLineRange(anchorIndex, focusIndex) {
    const previewContent = document.getElementById('notePreviewContent');
    if (!previewContent) return;

    if (this.noteLiveActiveLineIndex !== null) {
      this.noteLiveActiveLineIndex = null;
      this.renderNoteLiveEditor();
    }

    const lines = this.getNoteContentLines();
    const maxIndex = Math.max(0, lines.length - 1);
    const normalizedAnchor = Math.max(0, Math.min(anchorIndex, maxIndex));
    const normalizedFocus = Math.max(0, Math.min(focusIndex, maxIndex));
    const startIndex = Math.min(normalizedAnchor, normalizedFocus);
    const endIndex = Math.max(normalizedAnchor, normalizedFocus);
    const startLine = previewContent.querySelector(`.note-live-line-rendered[data-line-index="${startIndex}"]`);
    const endLine = previewContent.querySelector(`.note-live-line-rendered[data-line-index="${endIndex}"]`);

    if (!startLine || !endLine) return;

    const range = document.createRange();
    range.setStartBefore(startLine);
    range.setEndAfter(endLine);

    const selection = window.getSelection?.();
    if (selection) {
      selection.removeAllRanges();
      selection.addRange(range);
    }

    const focusLine = previewContent.querySelector(`.note-live-line-rendered[data-line-index="${normalizedFocus}"]`);
    focusLine?.focus({ preventScroll: true });
    this.noteLiveSelectionAnchorIndex = normalizedAnchor;
    this.noteLiveSelectionFocusIndex = normalizedFocus;
  },

  getNoteLiveSelectedLineRange(container) {
    const selection = window.getSelection?.();
    if (!container || !selection || selection.isCollapsed || selection.rangeCount === 0) return null;
    if (!this.noteLiveSelectionContains(container, selection.anchorNode)
      || !this.noteLiveSelectionContains(container, selection.focusNode)) {
      return null;
    }

    const range = selection.getRangeAt(0);
    const selectedIndexes = Array.from(container.querySelectorAll('.note-live-line-rendered'))
      .filter(line => {
        try {
          return range.intersectsNode(line);
        } catch {
          return false;
        }
      })
      .map(line => Number(line.dataset.lineIndex))
      .filter(Number.isInteger);

    if (selectedIndexes.length === 0) return null;
    return {
      start: Math.min(...selectedIndexes),
      end: Math.max(...selectedIndexes)
    };
  },

  isNoteLivePrintableKey(event) { return window.NoteEditor?.isPrintableKey(event) ?? false; },

  toggleNoteHeadingFold(lineIndex) {
    if (!Number.isInteger(lineIndex)) return;
    const lines = this.getNoteContentLines();
    if (lineIndex < 0 || lineIndex >= lines.length || this.noteHeadingLevel(lines[lineIndex]) === 0) return;

    if (this.noteLiveCollapsedHeadings.has(lineIndex)) {
      this.noteLiveCollapsedHeadings.delete(lineIndex);
    } else {
      this.noteLiveCollapsedHeadings.add(lineIndex);
    }

    this.noteLiveActiveLineIndex = null;
    this.noteLiveActiveRange = null;
    this.clearNoteLiveSelection();
    this.renderNoteLiveEditor();

    const foldButton = document.querySelector(`.note-heading-fold[data-line-index="${lineIndex}"]`);
    foldButton?.focus({ preventScroll: true });
  },

  toggleNoteTaskLine(lineIndex, checked) {
    if (!Number.isInteger(lineIndex)) return;
    const lines = this.getNoteContentLines();
    if (lineIndex < 0 || lineIndex >= lines.length) return;

    const task = this.parseNoteTaskLine(lines[lineIndex]);
    if (!task) return;

    this.pushNoteUndoState();
    const marker = checked ? '[x]' : '[]';
    lines[lineIndex] = `${task.indent}${task.bullet}${task.gap}${marker}${task.afterGap}${task.text}`;
    this.setNoteContentLines(lines);
    this.noteLiveActiveLineIndex = null;
    this.noteLiveActiveRange = null;
    this.clearNoteLiveSelection();
    this.scheduleNoteAutoSave();
    this.renderNoteLiveEditor();

    const checkbox = document.querySelector(`.note-task-checkbox[data-line-index="${lineIndex}"]`);
    checkbox?.focus({ preventScroll: true });
  },

  deleteNoteLiveLineRange(range) {
    const lines = this.getNoteContentLines();
    const start = Math.max(0, Math.min(range.start, lines.length - 1));
    const end = Math.max(start, Math.min(range.end, lines.length - 1));
    this.pushNoteUndoState();
    lines.splice(start, end - start + 1);
    if (lines.length === 0) lines.push('');
    this.setNoteContentLines(lines);
    this.noteLiveActiveRange = null;
    this.clearNoteLiveSelection();
    this.scheduleNoteAutoSave();
    this.activateNoteLiveLine(Math.min(start, lines.length - 1), 0);
  },

  replaceNoteLiveLineRange(range, replacement) {
    const lines = this.getNoteContentLines();
    const start = Math.max(0, Math.min(range.start, lines.length - 1));
    const end = Math.max(start, Math.min(range.end, lines.length - 1));
    this.pushNoteUndoState();
    lines.splice(start, end - start + 1, replacement);
    this.setNoteContentLines(lines);
    this.noteLiveActiveRange = null;
    this.clearNoteLiveSelection();
    this.scheduleNoteAutoSave();
    this.activateNoteLiveLine(start, replacement.length);
  },

  editNoteLiveLineRange(range) {
    const lines = this.getNoteContentLines();
    const start = Math.max(0, Math.min(range.start, lines.length - 1));
    const end = Math.max(start, Math.min(range.end, lines.length - 1));
    this.clearNoteLiveSelection();
    this.noteLiveActiveLineIndex = null;
    this.noteLiveActiveRange = { start, end };
    this.renderNoteLiveEditor({ cursorPosition: lines.slice(start, end + 1).join('\n').length });
  },

  activateNoteLiveLine(lineIndex, cursorPosition = null) {
    this.noteLiveSelectionAnchorIndex = lineIndex;
    this.noteLiveSelectionFocusIndex = null;
    this.noteLivePointerDown = null;
    this.noteLiveActiveRange = null;
    this.noteLiveActiveLineIndex = lineIndex;
    this.renderNoteLiveEditor({ focusLineIndex: lineIndex, cursorPosition });
  },

  handleNoteLiveInputChange(input) {
    const lineIndex = Number(input.dataset.lineIndex);
    if (!Number.isInteger(lineIndex)) return;

    const normalizedValue = String(input.value || '').replace(/\r\n?/g, '\n');
    const lines = this.getNoteContentLines();
    const parts = normalizedValue.split('\n');
    this.pushNoteUndoState();

    if (parts.length > 1) {
      lines.splice(lineIndex, 1, ...parts);
      this.setNoteContentLines(lines);
      this.noteLiveActiveLineIndex = lineIndex + parts.length - 1;
      this.scheduleNoteAutoSave();
      this.renderNoteLiveEditor({
        focusLineIndex: this.noteLiveActiveLineIndex,
        cursorPosition: parts[parts.length - 1].length
      });
      return;
    }

    lines[lineIndex] = normalizedValue;
    this.setNoteContentLines(lines);
    input.className = ['note-live-line-input', this.noteLineKindClass(normalizedValue)]
      .filter(Boolean)
      .join(' ');
    this.resizeNoteLiveInput(input);
    this.scheduleNoteAutoSave();
  },

  handleNoteLiveRangeInputChange(input) {
    const start = Number(input.dataset.lineStart);
    const end = Number(input.dataset.lineEnd);
    if (!Number.isInteger(start) || !Number.isInteger(end)) return;

    const normalizedValue = String(input.value || '').replace(/\r\n?/g, '\n');
    const parts = normalizedValue.split('\n');
    const lines = this.getNoteContentLines();
    this.pushNoteUndoState();
    lines.splice(start, end - start + 1, ...parts);
    this.setNoteContentLines(lines);

    const newEnd = start + parts.length - 1;
    input.dataset.lineEnd = String(newEnd);
    input.closest('.note-live-line')?.setAttribute('data-line-end', String(newEnd));
    this.noteLiveActiveRange = { start, end: newEnd };
    this.resizeNoteLiveInput(input);
    this.scheduleNoteAutoSave();
  },

  handleNoteLiveRangeInputKeydown(event, input) {
    if (event.key === 'Escape' || ((event.metaKey || event.ctrlKey) && event.key === 'Enter')) {
      event.preventDefault();
      this.handleNoteLiveRangeInputChange(input);
      this.noteLiveActiveRange = null;
      this.noteLiveActiveLineIndex = null;
      this.renderNoteLiveEditor();
    }
  },

  handleNoteLiveInputKeydown(event, input) {
    const lineIndex = Number(input.dataset.lineIndex);
    if (!Number.isInteger(lineIndex)) return;

    const lines = this.getNoteContentLines();
    const value = input.value || '';
    const selectionStart = input.selectionStart ?? value.length;
    const selectionEnd = input.selectionEnd ?? selectionStart;

    if (event.key === 'Enter') {
      event.preventDefault();
      const before = value.slice(0, selectionStart);
      const after = value.slice(selectionEnd);
      this.pushNoteUndoState();
      lines.splice(lineIndex, 1, before, after);
      this.setNoteContentLines(lines);
      this.scheduleNoteAutoSave();
      this.activateNoteLiveLine(lineIndex + 1, 0);
      return;
    }

    if (event.key === 'Backspace' && selectionStart === 0 && selectionEnd === 0 && lineIndex > 0) {
      event.preventDefault();
      const previousLine = lines[lineIndex - 1] || '';
      this.pushNoteUndoState();
      lines.splice(lineIndex - 1, 2, previousLine + value);
      this.setNoteContentLines(lines);
      this.scheduleNoteAutoSave();
      this.activateNoteLiveLine(lineIndex - 1, previousLine.length);
      return;
    }

    if (event.key === 'Delete' && selectionStart === value.length && selectionEnd === value.length && lineIndex < lines.length - 1) {
      event.preventDefault();
      this.pushNoteUndoState();
      lines.splice(lineIndex, 2, value + (lines[lineIndex + 1] || ''));
      this.setNoteContentLines(lines);
      this.scheduleNoteAutoSave();
      this.activateNoteLiveLine(lineIndex, value.length);
      return;
    }

    if (event.key === 'ArrowUp' && selectionStart === 0 && lineIndex > 0) {
      event.preventDefault();
      const previousLine = lines[lineIndex - 1] || '';
      this.activateNoteLiveLine(lineIndex - 1, previousLine.length);
      return;
    }

    if (event.key === 'ArrowDown' && selectionStart === value.length && lineIndex < lines.length - 1) {
      event.preventDefault();
      this.activateNoteLiveLine(lineIndex + 1, Math.min((lines[lineIndex + 1] || '').length, selectionStart));
    }
  },

  resizeNoteLiveInput(input) {
    input.style.height = 'auto';
    input.style.height = `${Math.max(input.scrollHeight, 24)}px`;
  },

  renderMarkdownLine(line) {
    if (!line) return '<br>';

    if (window.marked && typeof window.marked.parse === 'function') {
      const canSanitize = window.DOMPurify && typeof window.DOMPurify.sanitize === 'function';
      const normalizedLine = this.normalizeCompactTaskListMarkdown(line);
      const rendered = window.marked.parse(canSanitize ? normalizedLine : this.escapeHtml(normalizedLine), {
        breaks: true,
        gfm: true
      });
      return canSanitize
        ? window.DOMPurify.sanitize(rendered)
        : rendered;
    }

    return this.renderMarkdown(line);
  },

  renderInlineMarkdown(text) {
    if (!text) return '';

    if (window.marked && typeof window.marked.parseInline === 'function') {
      const canSanitize = window.DOMPurify && typeof window.DOMPurify.sanitize === 'function';
      const rendered = window.marked.parseInline(canSanitize ? text : this.escapeHtml(text), {
        breaks: true,
        gfm: true
      });
      return canSanitize
        ? window.DOMPurify.sanitize(rendered)
        : rendered;
    }

    return this.escapeHtml(text);
  },

  // Toggle preview mode
  toggleNotePreview() {
    this.setNotePreviewMode(!this.isNotePreviewMode);
  },

  // Render markdown for note previews
  renderMarkdown(text) {
    if (!text) return '<p style="color: var(--text-tertiary);">No content</p>';

    if (window.marked && typeof window.marked.parse === 'function') {
      const canSanitize = window.DOMPurify && typeof window.DOMPurify.sanitize === 'function';
      const normalizedText = this.normalizeCompactTaskListMarkdown(text);
      const rendered = window.marked.parse(canSanitize ? normalizedText : this.escapeHtml(normalizedText), {
        breaks: true,
        gfm: true
      });
      return canSanitize
        ? window.DOMPurify.sanitize(rendered)
        : rendered;
    }

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
    for (const [, notes] of this.notesByFolder) {
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
      const response = await fetch(`/api/orchestration/tasks?workspace_id=${workspaceId}`);
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
      const response = await fetch(`/api/orchestration/tasks?workspace_id=${workspaceId}`);
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
      const response = await fetch(`/api/orchestration/tasks?workspace_id=${workspaceId}`);
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
          workspace_id: workspaceId,
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
        const tasksResponse = await fetch(`/api/orchestration/tasks?workspace_id=${this.currentTaskWorkspaceId}`);
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

    // Shared task modal controller owns these bindings when present.
    const hasSharedTaskController = Boolean(
      window.taskModalController && typeof window.taskModalController.openForCreate === 'function'
    );
    if (!hasSharedTaskController) {
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
      document.getElementById('taskModalScheduleType')?.addEventListener('change', () => {
        this.updateTaskModalScheduleTypeFields();
      });

      // Modal escape key for task modal
      document.getElementById('taskModal')?.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') {
          this.closeTaskModal();
        }
      });
    }

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
      const response = await fetch(`/api/orchestration/tasks?workspace_id=${workspaceId}`);
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
          <button class="scheduled-task-delete" title="Delete Schedule">
            🗑
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

      // Delete button
      itemEl.querySelector('.scheduled-task-delete')?.addEventListener('click', (e) => {
        e.stopPropagation();
        this.deleteScheduledTask(st.id);
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
      case 'weekly': {
        const days = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
        return `Weekly on ${days[schedule.day_of_week || 0]} at ${schedule.time_of_day || '00:00'}`;
      }
      case 'interval': {
        // Interval is in nanoseconds
        const totalSeconds = Math.floor((schedule.interval || 0) / 1000000000);
        const hours = Math.floor(totalSeconds / 3600);
        const minutes = Math.floor((totalSeconds % 3600) / 60);
        if (hours > 0) return `Every ${hours} hour${hours > 1 ? 's' : ''}`;
        return `Every ${minutes} minute${minutes > 1 ? 's' : ''}`;
      }
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

  // Delete/clear schedule from a task
  async deleteScheduledTask(taskId) {
    if (!confirm('Are you sure you want to delete this schedule? The task itself will remain but will no longer run on a schedule.')) return;

    try {
      const response = await fetch(`/api/orchestration/tasks/${taskId}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          task_id: taskId,
          schedule_enabled: false,
          schedule: null
        })
      });

      if (!response.ok) throw new Error('Failed to delete schedule');

      // Close the detail modal
      this.closeScheduledTaskDetailModal();

      // Reload and refresh the scheduled tasks list
      if (this.currentScheduledTaskWorkspaceId) {
        await this.loadWorkspaceScheduledTasks(this.currentScheduledTaskWorkspaceId);
        const tasks = this.scheduledTasksByWorkspace.get(this.currentScheduledTaskWorkspaceId) || [];
        this.renderScheduledTasksList(tasks);
        this.renderFolderTree();
      }

      this.showToast('Schedule deleted', 'success');
    } catch (error) {
      console.error('Failed to delete schedule:', error);
      this.showToast('Failed to delete schedule', 'error');
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

    document.getElementById('scheduleDetailDeleteBtn')?.addEventListener('click', () => {
      const modal = document.getElementById('scheduledTaskDetailModal');
      if (modal) {
        const scheduleId = modal.dataset.scheduleId;
        this.deleteScheduledTask(scheduleId);
      }
    });

    // Escape key handlers (document-level for reliable closing)
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        const detailModal = document.getElementById('scheduledTaskDetailModal');
        const listModal = document.getElementById('scheduledTasksModal');
        // Close detail modal first if visible, then list modal
        if (detailModal && detailModal.style.display !== 'none' && detailModal.style.display !== '') {
          this.closeScheduledTaskDetailModal();
        } else if (listModal && listModal.style.display !== 'none' && listModal.style.display !== '') {
          this.closeScheduledTasksModal();
        }
      }
    });
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
          workspace_id: workspaceId,
          description,
          priority: 3
        })
      });

      if (!response.ok) throw new Error('Failed to create task');

      input.value = '';

      // Reload tasks for the workspace
      const tasksResponse = await fetch(`/api/orchestration/tasks?workspace_id=${workspaceId}`);
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
        const tasksResponse = await fetch(`/api/orchestration/tasks?workspace_id=${this.currentTaskWorkspaceId}`);
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
        const tasksResponse = await fetch(`/api/orchestration/tasks?workspace_id=${workspaceId}`);
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
      case 'interval': {
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
      }
      case 'once': {
        const datetime = document.getElementById('taskModalScheduleDatetime')?.value;
        if (datetime) {
          schedule.run_at = new Date(datetime).toISOString();
        }
        break;
      }
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
            workspace_id: workspaceId,
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
  // Initialize if session sidebar OR the extracted modals exist
  if (document.getElementById('sessionSidebar') || document.getElementById('createChatModal')) {
    sessionManager.init();
  }
});

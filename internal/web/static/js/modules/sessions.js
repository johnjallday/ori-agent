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
  editAgentOriginalName: '',
  editAgentSelectedTags: [],
  editAgentModalInitialized: false,
  editAgentModelOptionsLoaded: false,

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
    await this.loadSessions();
    await this.loadFolders();
    await this.loadTags();

    // Try to restore active session, or auto-create one
    const restored = this.restoreActiveSession();
    if (!restored && this.sessions.length === 0) {
      // Auto-create initial session for this tab
      await this.createNewSession();
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
    // New session button
    document.getElementById('newSessionBtn')?.addEventListener('click', () => this.createNewSession());
    document.getElementById('createFirstSessionBtn')?.addEventListener('click', () => this.createNewSession());

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
      this.showAddFolderModal();
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

  // Load folders from API
  async loadFolders() {
    try {
      const response = await fetch('/api/folders?tree=true');
      if (!response.ok) throw new Error('Failed to load folders');

      const data = await response.json();
      this.folders = data.folders || [];

      // Load notes for all folders
      await this.loadAllFolderNotes(this.folders);

      this.renderFolderTree();
    } catch (error) {
      console.error('Failed to load folders:', error);
      this.folders = [];
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
    const emptyState = document.getElementById('sessionsEmpty');
    const loadingState = document.getElementById('sessionsLoading');

    if (!container) return;

    if (loadingState) loadingState.style.display = 'none';

    const hasNotes = this.noteSearchResults.length > 0;
    const hasSessions = this.sessions.length > 0;

    if (!hasSessions && !hasNotes) {
      container.innerHTML = '';
      if (emptyState) emptyState.style.display = 'flex';
      return;
    }

    if (emptyState) emptyState.style.display = 'none';

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
      container.innerHTML = '<div class="text-center text-muted small py-2">No folders</div>';
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
        ? `data-has-accent="true" style="padding-left: ${16 + depth * 16}px; --folder-accent-bg: ${this.hexToRgba(folder.color, 0.12)}; --folder-accent-bg-hover: ${this.hexToRgba(folder.color, 0.18)}; --folder-accent-border: ${this.hexToRgba(folder.color, 0.35)};"`
        : `style="padding-left: ${16 + depth * 16}px;"`;
      // Get notes for this folder
      const folderNotes = this.notesByFolder?.get(folder.id) || [];
      const hasContent = folderSessions.length > 0 || folderNotes.length > 0;

      const sessionsHtml = folderSessions.length > 0
        ? `
          <div class="folder-sessions ${isCollapsed ? 'collapsed' : ''}" ${accentStyles}>
            ${folderSessions.map(session => this.renderSessionItem(session)).join('')}
          </div>
        `
        : '';

      const notesHtml = folderNotes.length > 0
        ? `
          <div class="folder-notes-section ${isCollapsed ? 'collapsed' : ''}" style="padding-left: ${16 + depth * 16}px;">
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
        <div class="folder-item ${isActive ? 'active' : ''} ${isCollapsed ? 'collapsed' : ''}" data-folder-id="${folder.id}" style="padding-left: ${8 + depth * 16}px;" title="${folderTitle}">
          ${hasContent ? `
            <button class="folder-collapse-btn" data-folder-id="${folder.id}" title="${isCollapsed ? 'Show content' : 'Hide content'}">
              <svg viewBox="0 0 24 24" fill="currentColor">
                <path d="M8.59,16.58L13.17,12L8.59,7.41L10,6L16,12L10,18L8.59,16.58Z"/>
              </svg>
            </button>
          ` : ''}
          <svg class="folder-icon" viewBox="0 0 24 24" fill="currentColor" style="${colorStyle}">
            <path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>
          </svg>
          <span class="folder-name">${this.escapeHtml(folder.name)}</span>
          ${folder.session_count > 0 ? `<span class="folder-count">${folder.session_count}</span>` : ''}
        </div>
        ${notesHtml}
        ${sessionsHtml}
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
        <span class="session-folder-name">No Folder</span>
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
    const emptyState = document.getElementById('sessionsEmpty');
    const loadingState = document.getElementById('sessionsLoading');

    if (loadingState) loadingState.style.display = 'none';
    if (emptyState) emptyState.style.display = 'flex';
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

  // Create new session - shows agent picker dialog first
  async createNewSession() {
    try {
      // Fetch available agents
      const agents = await this.fetchAgents();
      if (!agents || agents.length === 0) {
        console.error('No agents available');
        return;
      }

      // If only one agent, skip the picker
      if (agents.length === 1) {
        await this.createSessionWithAgent(agents[0].name);
        return;
      }

      // Show agent picker dialog
      this.showAgentPickerDialog(agents);
    } catch (error) {
      console.error('Failed to create session:', error);
    }
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
      }
    } catch (error) {
      console.error('Failed to create session:', error);
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

    const modelInput = document.getElementById('editAgentModelInput');
    if (modelInput) {
      modelInput.value = agent.model || '';
      this.ensureEditAgentModelOption(agent.model);
    }

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
    const list = document.getElementById('editAgentModelOptions');
    if (!list) return;

    let options = [];
    if (typeof loadAvailableProviders === 'function') {
      const providers = await loadAvailableProviders();
      providers.forEach((provider) => {
        (provider.models || []).forEach((model) => {
          if (model?.value) {
            options.push(model.value);
          }
        });
      });
    }

    if (options.length === 0) {
      options = [
        'gpt-5',
        'gpt-5-mini',
        'gpt-5-nano',
        'gpt-4o',
        'gpt-4o-mini',
        'claude-3-5-sonnet-20241022',
        'claude-3-haiku-20240307',
        'llama3.2',
        'mistral',
        'codellama'
      ];
    }

    const uniqueOptions = Array.from(new Set(options));
    list.innerHTML = '';
    uniqueOptions.forEach((value) => {
      const option = document.createElement('option');
      option.value = value;
      list.appendChild(option);
    });

    this.editAgentModelOptionsLoaded = true;
  },

  ensureEditAgentModelOption(model) {
    if (!model) return;
    const list = document.getElementById('editAgentModelOptions');
    if (!list) return;
    const exists = Array.from(list.options).some((option) => option.value === model);
    if (exists) return;
    const option = document.createElement('option');
    option.value = model;
    list.appendChild(option);
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
    const modelInput = document.getElementById('editAgentModelInput');
    const descriptionInput = document.getElementById('editAgentDescription');
    const colorInput = document.getElementById('editAgentAvatarColor');
    const favoriteToggle = document.getElementById('editAgentFavoriteToggle');

    const newName = nameInput?.value.trim();
    const type = typeSelect?.value;
    const role = roleSelect?.value;
    const model = modelInput?.value.trim();

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
      modelInput?.focus();
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
      const response = await fetch('/api/folders', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, description, color })
      });

      if (!response.ok) throw new Error('Failed to create folder');

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
    if (!confirm('Are you sure you want to delete this folder? Sessions will be moved to root.')) return;

    try {
      const response = await fetch(`/api/folders/${folderId}`, {
        method: 'DELETE'
      });

      if (!response.ok) throw new Error('Failed to delete folder');

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
      case 'new-note':
        this.createNewNoteForFolder(folderId);
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
        No Folder
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
  showAddFolderModal() {
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
        <span class="session-info-label">Folder</span>
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
  restoreActiveSession() {
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
    }, 500);
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
    if (!folderId) return 'No Folder';
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
      const response = await fetch(`/api/folders/${folderId}/notes`);
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
      const response = await fetch(`/api/folders/${folderId}/notes`, {
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

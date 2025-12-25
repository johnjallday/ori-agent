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
    this.setupKeyboardShortcuts();
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
        const action = e.currentTarget.dataset.action;
        this.handleFolderContextAction(action);
      });
    });

    // Resize handle
    this.setupResizeHandle();

    // Session list scroll (for virtual scroll)
    document.getElementById('sessionListContainer')?.addEventListener('scroll',
      () => this.handleScroll());
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
      this.renderSessions();
    } catch (error) {
      console.error('Failed to load sessions:', error);
      this.sessions = [];
      this.clearSelection();
      this.renderEmptyState();
    } finally {
      this.isLoading = false;
    }
  },

  // Load folders from API
  async loadFolders() {
    try {
      const response = await fetch('/api/folders?tree=true');
      if (!response.ok) throw new Error('Failed to load folders');

      const data = await response.json();
      this.folders = data.folders || [];
      this.renderFolderTree();
    } catch (error) {
      console.error('Failed to load folders:', error);
      this.folders = [];
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

    if (this.sessions.length === 0) {
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

    // Render root sessions with "No Folder" header
    container.innerHTML = `
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

      // Click to filter by folder
      item.addEventListener('click', (e) => {
        e.stopPropagation();
        this.filterByFolder(folderId);
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
      const sessionsHtml = folderSessions.length > 0
        ? `
          <div class="folder-sessions ${isCollapsed ? 'collapsed' : ''}" ${accentStyles}>
            ${folderSessions.map(session => this.renderSessionItem(session)).join('')}
          </div>
        `
        : '';

      return `
        <div class="folder-item ${isActive ? 'active' : ''} ${isCollapsed ? 'collapsed' : ''}" data-folder-id="${folder.id}" style="padding-left: ${8 + depth * 16}px;">
          <svg class="folder-icon" viewBox="0 0 24 24" fill="currentColor" style="${colorStyle}">
            <path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>
          </svg>
          <span class="folder-name">${this.escapeHtml(folder.name)}</span>
          ${folder.session_count > 0 ? `<span class="folder-count">${folder.session_count}</span>` : ''}
          ${hasNestedSessions ? `
            <button class="folder-collapse-btn" data-folder-id="${folder.id}" title="${isCollapsed ? 'Show sessions' : 'Hide sessions'}">
              <svg viewBox="0 0 24 24" fill="currentColor">
                <path d="M7,10L12,15L17,10H7Z"/>
              </svg>
            </button>
          ` : ''}
        </div>
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

        // Update the current agent display
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

      // Update the current agent to match the session's agent
      if (session.agent_name) {
        this.updateCurrentAgent(session.agent_name);
      }

      // Restore messages to chat area
      this.restoreSessionMessages(session);
    } catch (error) {
      console.error('Failed to switch session:', error);
    }
  },

  // Update the current agent display in navbar
  updateCurrentAgent(agentName) {
    if (!agentName) return;

    // Update the global currentAgent variable
    if (typeof currentAgent !== 'undefined') {
      window.currentAgent = agentName;
    }

    // Update the navbar display
    const agentElement = document.querySelector('#currentAgentDisplay span.fw-medium');
    if (agentElement) {
      agentElement.textContent = agentName;
    }

    // Also update any agent display in the header
    const agentHeader = document.querySelector('.agent-name');
    if (agentHeader) {
      agentHeader.textContent = agentName;
    }
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
    const colorBtn = document.querySelector('.folder-color-btn.active');

    const name = nameInput?.value.trim();
    if (!name) return;

    const color = colorBtn?.dataset.color || '';

    try {
      const response = await fetch('/api/folders', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, color })
      });

      if (!response.ok) throw new Error('Failed to create folder');

      // Close modal
      const modal = bootstrap.Modal.getInstance(document.getElementById('addFolderModal'));
      modal?.hide();

      // Clear input
      if (nameInput) nameInput.value = '';

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
      // Show sidebar if hidden
      const sidebar = document.getElementById('sessionSidebar');
      if (sidebar?.classList.contains('d-none')) {
        sidebar.classList.remove('d-none');
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

  // Toggle sidebar
  toggleSidebar() {
    const sidebar = document.getElementById('sessionSidebar');
    if (sidebar) {
      sidebar.classList.toggle('d-none');
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
    if (sessionMenu) sessionMenu.style.display = 'none';
    if (folderMenu) folderMenu.style.display = 'none';
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

      // Update the current agent to match the session's agent
      if (session.agent_name) {
        this.updateCurrentAgent(session.agent_name);
      }

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

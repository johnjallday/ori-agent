/**
 * Main application coordinator
 * Manages all modules and provides central initialization
 */

'use strict';

class DolphinApp {
  constructor() {
    // Global state
    this.currentAgent = '';
    this.isComposing = false;
    this.promptHistory = [];
    this.historyIndex = -1;
    
    // Module instances
    this.themeManager = null;
    this.agentsManager = null;
    
    this.init();
  }

  /**
   * Initialize the application and all modules
   */
  async init() {
    try {
      Logger?.info && Logger.info('Initializing Dolphin App...');
      
      // Initialize theme manager (already auto-initialized)
      this.themeManager = window.themeManager;
      
      // Initialize agents manager
      this.agentsManager = new AgentsManager();
      
      // Set up core event listeners
      this.setupEventListeners();
      
      // Load initial data
      await this.loadInitialData();
      
      Logger?.info && Logger.info('Dolphin App initialized successfully');
    } catch (error) {
      Logger?.error && Logger.error('Failed to initialize app:', error);
    }
  }

  /**
   * Load initial application data
   */
  async loadInitialData() {
    try {
      // Load agents first to set currentAgent
      await this.agentsManager.refreshAgents();
      this.currentAgent = this.agentsManager.getCurrentAgent();
      
      // Load other data
      await this.refreshPlugins();
      await this.refreshRegistry();
      
    } catch (error) {
      Logger?.error && Logger.error('Failed to load initial data:', error);
    }
  }

  /**
   * Set up core application event listeners
   */
  setupEventListeners() {
    // Chat send button
    const sendBtn = document.getElementById('sendBtn');
    if (sendBtn) {
      sendBtn.onclick = async () => {
        await this.sendMessage();
      };
    }

    // Chat input with enter key and history navigation
    const input = document.getElementById('input');
    if (input) {
      this.setupChatInput(input);
    }

    // Refresh app button
    const refreshBtn = document.getElementById('refreshAppBtn');
    if (refreshBtn) {
      refreshBtn.onclick = async () => {
        await this.refreshApp(true);
      };
    }
  }

  /**
   * Set up chat input with keyboard handling
   * @param {HTMLElement} input - Chat input element
   */
  setupChatInput(input) {
    input.addEventListener('compositionstart', () => {
      this.isComposing = true;
    });

    input.addEventListener('compositionend', () => {
      this.isComposing = false;
    });

    input.addEventListener('keydown', async (e) => {
      if (e.key === 'Enter' && !e.shiftKey && !this.isComposing) {
        e.preventDefault();
        await this.sendMessage();
      } else if (e.key === 'ArrowUp' && this.promptHistory.length > 0) {
        e.preventDefault();
        this.historyIndex = Math.min(this.historyIndex + 1, this.promptHistory.length - 1);
        input.value = this.promptHistory[this.promptHistory.length - 1 - this.historyIndex];
      } else if (e.key === 'ArrowDown') {
        e.preventDefault();
        if (this.historyIndex > 0) {
          this.historyIndex--;
          input.value = this.promptHistory[this.promptHistory.length - 1 - this.historyIndex];
        } else if (this.historyIndex === 0) {
          this.historyIndex = -1;
          input.value = '';
        }
      }
    });
  }

  /**
   * Send a chat message
   */
  async sendMessage() {
    const input = document.getElementById('input');
    const q = input?.value?.trim();
    if (!q) return;

    // Add to prompt history (avoid duplicates)
    if (this.promptHistory.length === 0 || this.promptHistory[this.promptHistory.length - 1] !== q) {
      this.promptHistory.push(q);
      if (this.promptHistory.length > 10) {
        this.promptHistory.shift();
      }
    }
    this.historyIndex = -1;

    // Handle slash commands
    if (await this.handleSlashCommand(q, input)) {
      return;
    }

    // Send regular chat message
    this.addChat('User', q);
    input.value = '';

    try {
      const res = await fetch('/api/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ question: q }),
      });

      if (!res.ok) {
        const text = await res.text();
        this.addChat('Assistant', `Error: ${text || res.statusText}`);
        return;
      }

      const data = await res.json();
      if (data.toolCall) {
        this.addChat(data.toolCall.function, data.toolCall.result);
      }

      // Check if we need to refresh after tool calls
      if (data.toolCall || data.toolCalls) {
        const shouldRefresh = this.shouldRefreshAfterToolCalls(data.toolCalls || [data.toolCall]);
        if (shouldRefresh) {
          setTimeout(() => this.refreshApp(false), 1000);
        }
      }

      this.addChat('Assistant', data.response || '(no response)');
    } catch (e) {
      this.addChat('Assistant', `Network error: ${e}`);
    }
  }

  /**
   * Handle slash commands
   * @param {string} command - The command string
   * @param {HTMLElement} input - Input element
   * @returns {boolean} True if command was handled
   */
  async handleSlashCommand(command, input) {
    const slashCommands = {
      '/agent': () => this.showAgentDashboard(),
      '/plugins': () => this.showPluginRegistry(),
      '/tools': () => this.showLoadedTools(),
      '/agents': () => this.showAgentsList(),
      '/reaper-functions': () => this.showReaperFunctions()
    };

    if (slashCommands[command]) {
      input.value = '';
      await slashCommands[command]();
      return true;
    }

    return false;
  }

  /**
   * Check if app should refresh after tool calls
   * @param {Array} toolCalls - Array of tool call results
   * @returns {boolean} True if refresh is needed
   */
  shouldRefreshAfterToolCalls(toolCalls) {
    return toolCalls.some(toolCall => {
      return toolCall && (
        toolCall.function === 'music_project_manager' ||
        toolCall.function === 'reaper_manager' ||
        (toolCall.result && (
          toolCall.result.includes('loaded') ||
          toolCall.result.includes('initialized') ||
          toolCall.result.includes('setup')
        ))
      );
    });
  }

  /**
   * Add a message to the chat
   * @param {string} who - Who sent the message
   * @param {string} text - Message text
   */
  addChat(who, text) {
    const chatArea = document.getElementById('chatArea');
    if (!chatArea) return;

    const messageDiv = document.createElement('div');
    messageDiv.className = `mb-3 fade-in ${who === 'User' ? 'user-message' : 'assistant-message'}`;

    const headerDiv = document.createElement('div');
    headerDiv.className = 'd-flex align-items-center gap-2 mb-1';

    const avatar = document.createElement('div');
    avatar.className = `chat-avatar ${who === 'User' ? 'user-avatar' : 'assistant-avatar'}`;
    avatar.textContent = who === 'User' ? 'U' : 'A';

    const nameSpan = document.createElement('span');
    nameSpan.className = 'fw-medium';
    nameSpan.textContent = who;

    const timeSpan = document.createElement('small');
    timeSpan.className = 'text-muted';
    timeSpan.textContent = new Date().toLocaleTimeString();

    headerDiv.appendChild(avatar);
    headerDiv.appendChild(nameSpan);
    headerDiv.appendChild(timeSpan);

    const contentDiv = document.createElement('div');
    contentDiv.className = 'chat-content';

    if (this.isStructuredContent(text)) {
      this.renderStructuredContent(contentDiv, text);
    } else {
      contentDiv.innerHTML = marked.parse(text);
    }

    messageDiv.appendChild(headerDiv);
    messageDiv.appendChild(contentDiv);
    chatArea.appendChild(messageDiv);
    
    // Scroll to bottom
    chatArea.scrollTop = chatArea.scrollHeight;
  }

  /**
   * Check if content is structured (contains tables, lists, etc.)
   * @param {string} text - Text to check
   * @returns {boolean} True if structured content
   */
  isStructuredContent(text) {
    const indicators = [
      /\|.*\|.*\|/,  // table format
      /^\d+\.\s/m,   // numbered list
      /^-\s/m,       // bullet list
      /^#{1,6}\s/m,  // headers
    ];
    return indicators.some(pattern => pattern.test(text));
  }

  /**
   * Render structured content with enhanced formatting
   * @param {HTMLElement} container - Container element
   * @param {string} text - Text to render
   */
  renderStructuredContent(container, text) {
    // Use marked.js for now, could be enhanced later
    container.innerHTML = marked.parse(text);
  }

  /**
   * Refresh the entire application
   * @param {boolean} showNotification - Whether to show notification
   */
  async refreshApp(showNotification = false) {
    try {
      if (showNotification) {
        this.showNotification('Refreshing application...', 'info');
      }

      await this.loadInitialData();

      if (showNotification) {
        this.showNotification('Application refreshed successfully', 'success');
      }
    } catch (error) {
      Logger?.error && Logger.error('Failed to refresh app:', error);
      if (showNotification) {
        this.showNotification('Failed to refresh application', 'error');
      }
    }
  }

  /**
   * Show a notification to the user
   * @param {string} message - Notification message
   * @param {string} type - Notification type (success, error, info)
   */
  showNotification(message, type = 'info') {
    // Create notification element
    const notification = document.createElement('div');
    notification.className = `alert alert-${type === 'error' ? 'danger' : type} alert-dismissible fade show position-fixed`;
    notification.style.cssText = `
      top: 20px;
      right: 20px;
      z-index: 9999;
      max-width: 300px;
    `;
    notification.innerHTML = `
      ${message}
      <button type="button" class="btn-close" data-bs-dismiss="alert"></button>
    `;

    document.body.appendChild(notification);

    // Auto-remove after 3 seconds
    setTimeout(() => {
      if (notification.parentNode) {
        notification.remove();
      }
    }, 3000);
  }

  /**
   * Refresh plugins list and update UI
   */
  async refreshPlugins() {
    try {
      const res = await fetch('/api/plugins');
      const data = await res.json();
      this.updatePluginsUI(data);
    } catch (error) {
      Logger?.error && Logger.error('Failed to refresh plugins:', error);
    }
  }

  /**
   * Update plugins UI with loaded plugins
   * @param {Object} data - Plugin data from API
   */
  updatePluginsUI(data) {
    const ul = document.getElementById('plugins');
    const noPluginsMessage = document.getElementById('noPluginsMessage');
    const currentAgentNameSpan = document.getElementById('currentAgentName');
    const loadedPluginsCount = document.getElementById('loadedPluginsCount');

    // Update current agent name in plugins section
    if (currentAgentNameSpan) {
      currentAgentNameSpan.textContent = this.currentAgent;
    }

    // Update loaded plugins count
    const pluginCount = data.plugins ? data.plugins.length : 0;
    if (loadedPluginsCount) {
      loadedPluginsCount.textContent = pluginCount;
    }

    if (!ul) return;
    ul.innerHTML = '';

    // Show/hide no plugins message
    if (!data.plugins || data.plugins.length === 0) {
      if (noPluginsMessage) noPluginsMessage.style.display = 'block';
      ul.style.display = 'none';
    } else {
      if (noPluginsMessage) noPluginsMessage.style.display = 'none';
      ul.style.display = 'block';

      data.plugins.forEach(p => {
        const li = document.createElement('li');
        li.className = 'list-group-item d-flex justify-content-between align-items-center';
        
        const info = document.createElement('div');
        info.innerHTML = `<strong>${p.name}</strong>: ${p.description}`;
        
        const btn = document.createElement('button');
        btn.className = 'btn btn-sm btn-outline-danger';
        btn.textContent = 'Unload';
        btn.onclick = async () => {
          await fetch(`/api/plugins?name=${encodeURIComponent(p.name)}`, { method: 'DELETE' });
          await this.refreshApp(true);
        };
        
        li.appendChild(info);
        li.appendChild(btn);
        ul.appendChild(li);
      });
    }
  }

  /**
   * Refresh plugin registry and update UI
   */
  async refreshRegistry() {
    try {
      const res = await fetch('/api/plugin-registry');
      const data = await res.json();

      // Fetch currently loaded plugins
      const loadedRes = await fetch('/api/plugins');
      const loadedData = await loadedRes.json();
      const loadedPluginsMap = {};
      (loadedData.plugins || []).forEach(plugin => {
        loadedPluginsMap[plugin.name] = true;
      });

      // Separate plugins into local and downloadable
      const localPlugins = [];
      const downloadablePlugins = [];

      (data.plugins || []).forEach(p => {
        if (p.github_repo || p.url || p.download_url) {
          downloadablePlugins.push(p);
        } else {
          localPlugins.push(p);
        }
      });

      this.updateLocalRegistry(localPlugins, loadedPluginsMap);
      this.updateDownloadablePlugins(downloadablePlugins, loadedPluginsMap);
    } catch (error) {
      Logger?.error && Logger.error('Failed to refresh registry:', error);
    }
  }

  /**
   * Update local registry UI
   * @param {Array} localPlugins - Local plugins
   * @param {Object} loadedPluginsMap - Map of loaded plugins
   */
  updateLocalRegistry(localPlugins, loadedPluginsMap) {
    const container = document.getElementById('localRegistryList');
    const noMessage = document.getElementById('noLocalRegistryMessage');
    const count = document.getElementById('localRegistryCount');

    if (!container) return;
    container.innerHTML = '';

    // Update count badge
    if (count) {
      count.textContent = localPlugins.length;
    }

    // Show/hide empty message
    if (localPlugins.length === 0) {
      if (noMessage) noMessage.style.display = 'block';
      container.style.display = 'none';
    } else {
      if (noMessage) noMessage.style.display = 'none';
      container.style.display = 'block';

      localPlugins.forEach(p => {
        const item = document.createElement('div');
        item.className = 'list-group-item d-flex justify-content-between align-items-center';
        
        const info = document.createElement('div');
        let pluginInfo = `<span class="badge bg-success me-2">Local</span><strong>${p.name}</strong>: ${p.description}`;
        
        // Add loaded status indicator
        if (loadedPluginsMap[p.name]) {
          pluginInfo += ' <span class="badge bg-success ms-1">✓ Loaded</span>';
        }
        
        // Add version if available
        if (p.version) {
          pluginInfo += ` <span class="badge bg-secondary ms-1">v${p.version}</span>`;
        }
        
        info.innerHTML = pluginInfo;
        
        const btnContainer = document.createElement('div');
        btnContainer.className = 'd-flex gap-2 flex-wrap align-items-center justify-content-end';
        
        // Load button
        const loadBtn = document.createElement('button');
        loadBtn.className = loadedPluginsMap[p.name] ? 'btn btn-sm btn-outline-secondary' : 'btn btn-sm btn-primary';
        loadBtn.innerHTML = loadedPluginsMap[p.name] ? '✓ Loaded' : 'Load';
        loadBtn.disabled = loadedPluginsMap[p.name];
        loadBtn.onclick = async () => {
          loadBtn.disabled = true;
          loadBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1"></span> Loading...';
          try {
            await fetch('/api/plugin-registry', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ name: p.name })
            });
            await this.refreshApp(true);
          } catch (error) {
            Logger?.error && Logger.error('Failed to load plugin:', error);
            loadBtn.disabled = false;
            loadBtn.innerHTML = 'Load';
          }
        };
        btnContainer.appendChild(loadBtn);
        
        item.appendChild(info);
        item.appendChild(btnContainer);
        container.appendChild(item);
      });
    }
  }

  /**
   * Update downloadable plugins UI
   * @param {Array} downloadablePlugins - Downloadable plugins
   * @param {Object} loadedPluginsMap - Map of loaded plugins
   */
  updateDownloadablePlugins(downloadablePlugins, loadedPluginsMap) {
    const container = document.getElementById('downloadablePluginsList');
    const noMessage = document.getElementById('noDownloadableMessage');
    const count = document.getElementById('downloadableCount');

    if (!container) return;
    container.innerHTML = '';

    // Update count badge
    if (count) {
      count.textContent = downloadablePlugins.length;
    }

    // Show/hide empty message
    if (downloadablePlugins.length === 0) {
      if (noMessage) noMessage.style.display = 'block';
      container.style.display = 'none';
    } else {
      if (noMessage) noMessage.style.display = 'none';
      container.style.display = 'block';

      downloadablePlugins.forEach(p => {
        const item = document.createElement('div');
        item.className = 'list-group-item d-flex justify-content-between align-items-center';
        
        const info = document.createElement('div');
        let typeIndicator = '<span class="badge bg-primary me-2">GitHub</span>';
        let pluginInfo = `${typeIndicator}<strong>${p.name}</strong>: ${p.description}`;
        
        // Add loaded status
        if (loadedPluginsMap[p.name]) {
          pluginInfo += ' <span class="badge bg-success ms-1">✓ Loaded</span>';
        }
        
        // Add version
        if (p.version) {
          pluginInfo += ` <span class="badge bg-secondary ms-1">v${p.version}</span>`;
        }
        
        info.innerHTML = pluginInfo;
        
        const btnContainer = document.createElement('div');
        btnContainer.className = 'd-flex gap-2 flex-wrap align-items-center justify-content-end';
        
        // Download/Install button
        if (!loadedPluginsMap[p.name]) {
          const downloadBtn = document.createElement('button');
          downloadBtn.className = 'btn btn-sm btn-success';
          downloadBtn.innerHTML = 'Download & Install';
          downloadBtn.onclick = async () => {
            downloadBtn.disabled = true;
            downloadBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1"></span> Installing...';
            try {
              await fetch('/api/plugins/download', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ url: p.download_url, name: p.name })
              });
              await this.refreshApp(true);
            } catch (error) {
              Logger?.error && Logger.error('Failed to download plugin:', error);
              downloadBtn.disabled = false;
              downloadBtn.innerHTML = 'Download & Install';
            }
          };
          btnContainer.appendChild(downloadBtn);
        } else {
          const loadedLabel = document.createElement('span');
          loadedLabel.className = 'badge bg-success';
          loadedLabel.textContent = '✓ Installed & Loaded';
          btnContainer.appendChild(loadedLabel);
        }
        
        item.appendChild(info);
        item.appendChild(btnContainer);
        container.appendChild(item);
      });
    }
  }

  // Placeholder methods for other dashboard functions
  async showAgentDashboard() { Logger?.info && Logger.info('Agent dashboard placeholder'); }
  async showPluginRegistry() { Logger?.info && Logger.info('Plugin registry placeholder'); }
  async showLoadedTools() { Logger?.info && Logger.info('Loaded tools placeholder'); }
  async showAgentsList() { Logger?.info && Logger.info('Agents list placeholder'); }
  async showReaperFunctions() { Logger?.info && Logger.info('Reaper functions placeholder'); }
}

// Global app instance
window.app = null;

// Initialize app when DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', () => {
    window.app = new DolphinApp();
  });
} else {
  window.app = new DolphinApp();
}
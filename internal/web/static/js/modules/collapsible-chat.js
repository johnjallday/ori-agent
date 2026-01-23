/**
 * Collapsible Chat Module
 * Handles the expand/collapse behavior of the chat panel
 */

class CollapsibleChat {
  constructor() {
    this.collapsedPanel = null;
    this.expandedPanel = null;
    this.toggleBtn = null;
    this.minimizeBtn = null;
    this.chatInput = null;
    this.chatSendBtn = null;
    this.messagesContainer = null;
    this.agentNameElement = null;
    this.badgeElement = null;
    this.isExpanded = false;
    this.unreadCount = 0;
    this.currentAgent = null;
  }

  /**
   * Initialize the collapsible chat
   */
  init() {
    this.collapsedPanel = document.getElementById('chatPanelCollapsed');
    this.expandedPanel = document.getElementById('chatPanelExpanded');
    this.toggleBtn = document.getElementById('chatToggleBtn');
    this.minimizeBtn = document.getElementById('chatMinimizeBtn');
    this.chatInput = document.getElementById('chatPanelInput');
    this.chatSendBtn = document.getElementById('chatPanelSendBtn');
    this.messagesContainer = document.getElementById('chatPanelMessages');
    this.agentNameElement = document.getElementById('chatAgentName');
    this.badgeElement = document.getElementById('chatBadge');

    if (!this.collapsedPanel || !this.expandedPanel) {
      console.warn('CollapsibleChat: Required elements not found');
      return;
    }

    this.setupEventListeners();
    this.loadCurrentAgent();

    // Keyboard shortcut: Ctrl+J to toggle
    document.addEventListener('keydown', (e) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'j') {
        e.preventDefault();
        this.toggle();
      }
      // Escape to collapse
      if (e.key === 'Escape' && this.isExpanded) {
        this.minimize();
      }
    });

    // Listen for new messages
    window.addEventListener('chatMessage', (e) => {
      this.handleNewMessage(e.detail);
    });

  }

  /**
   * Set up event listeners
   */
  setupEventListeners() {
    // Toggle button click
    if (this.toggleBtn) {
      this.toggleBtn.addEventListener('click', () => this.maximize());
    }

    // Minimize button click
    if (this.minimizeBtn) {
      this.minimizeBtn.addEventListener('click', () => this.minimize());
    }

    // Send message on button click
    if (this.chatSendBtn) {
      this.chatSendBtn.addEventListener('click', () => this.sendMessage());
    }

    // Send message on Enter
    if (this.chatInput) {
      this.chatInput.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' && !e.shiftKey) {
          e.preventDefault();
          this.sendMessage();
        }
      });
    }
  }

  /**
   * Load current agent context
   */
  async loadCurrentAgent() {
    try {
      // Get current agent from session or API
      const response = await fetch('/api/agents');
      if (response.ok) {
        const data = await response.json();
        const agents = data.agents || [];
        const currentName = data.current;

        // Find current agent or use first one
        if (currentName) {
          this.currentAgent = agents.find(a => a.name === currentName) || agents[0];
        } else if (agents.length > 0) {
          this.currentAgent = agents[0];
        }

        this.updateAgentDisplay();
      }
    } catch (error) {
      console.error('CollapsibleChat: Failed to load agent', error);
    }
  }

  /**
   * Update agent name display
   */
  updateAgentDisplay() {
    if (this.agentNameElement && this.currentAgent) {
      this.agentNameElement.textContent = this.currentAgent.name;
    }
  }

  /**
   * Toggle chat panel state
   */
  toggle() {
    if (this.isExpanded) {
      this.minimize();
    } else {
      this.maximize();
    }
  }

  /**
   * Expand the chat panel
   */
  maximize() {
    this.isExpanded = true;
    this.collapsedPanel.style.display = 'none';
    this.expandedPanel.style.display = 'flex';

    // Clear unread count
    this.unreadCount = 0;
    this.updateBadge();

    // Focus input
    if (this.chatInput) {
      setTimeout(() => this.chatInput.focus(), 100);
    }

    // Scroll to bottom
    this.scrollToBottom();
  }

  /**
   * Collapse the chat panel
   */
  minimize() {
    this.isExpanded = false;
    this.expandedPanel.style.display = 'none';
    this.collapsedPanel.style.display = 'block';
  }

  /**
   * Check if chat is expanded
   * @returns {boolean}
   */
  isOpen() {
    return this.isExpanded;
  }

  /**
   * Send a message
   */
  async sendMessage() {
    if (!this.chatInput) return;

    const message = this.chatInput.value.trim();
    if (!message) return;

    // Add user message to display
    this.addMessage({
      role: 'user',
      content: message,
      timestamp: new Date().toISOString()
    });

    // Clear input
    this.chatInput.value = '';

    try {
      // Send to API
      const response = await fetch('/api/chat/send', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          message: message,
          agent_name: this.currentAgent?.name
        })
      });

      if (!response.ok) {
        throw new Error('Failed to send message');
      }

      // Response will come through SSE or polling
      // For now, we'll add a placeholder response
      const data = await response.json();
      if (data.response) {
        this.addMessage({
          role: 'assistant',
          content: data.response,
          timestamp: new Date().toISOString()
        });
      }

    } catch (error) {
      console.error('CollapsibleChat: Failed to send message', error);
      this.addMessage({
        role: 'system',
        content: 'Failed to send message. Please try again.',
        timestamp: new Date().toISOString(),
        isError: true
      });
    }
  }

  /**
   * Add a message to the display
   * @param {Object} message - Message object
   */
  addMessage(message) {
    if (!this.messagesContainer) return;

    const messageHtml = this.renderMessage(message);
    const tempDiv = document.createElement('div');
    tempDiv.innerHTML = messageHtml;
    const messageElement = tempDiv.firstElementChild;

    this.messagesContainer.appendChild(messageElement);
    this.scrollToBottom();
  }

  /**
   * Render a message
   * @param {Object} message - Message object
   * @returns {string} HTML string
   */
  renderMessage(message) {
    const isUser = message.role === 'user';
    const isError = message.isError;

    return `
      <div class="chat-message ${isUser ? 'chat-message-user' : 'chat-message-assistant'} ${isError ? 'chat-message-error' : ''}">
        <div class="chat-message-content">
          ${this.escapeHtml(message.content)}
        </div>
        <div class="chat-message-time">
          ${this.formatTime(message.timestamp)}
        </div>
      </div>
    `;
  }

  /**
   * Handle incoming message from other sources
   * @param {Object} message - Message data
   */
  handleNewMessage(message) {
    if (!this.isExpanded) {
      this.unreadCount++;
      this.updateBadge();
    }

    this.addMessage(message);
  }

  /**
   * Update unread badge
   */
  updateBadge() {
    if (!this.badgeElement) return;

    if (this.unreadCount > 0) {
      this.badgeElement.textContent = this.unreadCount > 99 ? '99+' : this.unreadCount;
      this.badgeElement.style.display = 'flex';
    } else {
      this.badgeElement.style.display = 'none';
    }
  }

  /**
   * Scroll messages to bottom
   */
  scrollToBottom() {
    if (this.messagesContainer) {
      this.messagesContainer.scrollTop = this.messagesContainer.scrollHeight;
    }
  }

  /**
   * Format timestamp
   * @param {string} timestamp - ISO timestamp
   * @returns {string} Formatted time
   */
  formatTime(timestamp) {
    if (!timestamp) return '';
    const date = new Date(timestamp);
    return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }

  /**
   * Escape HTML
   * @param {string} str - String to escape
   * @returns {string} Escaped string
   */
  escapeHtml(str) {
    if (!str) return '';
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
  }

  /**
   * Set the current agent for chat
   * @param {Object} agent - Agent object
   */
  setAgent(agent) {
    this.currentAgent = agent;
    this.updateAgentDisplay();
  }

  /**
   * Clear chat messages
   */
  clearMessages() {
    if (this.messagesContainer) {
      this.messagesContainer.innerHTML = '';
    }
  }
}

// Create global instance
window.collapsibleChat = new CollapsibleChat();

// Initialize when DOM is ready
document.addEventListener('DOMContentLoaded', () => {
  window.collapsibleChat.init();
});

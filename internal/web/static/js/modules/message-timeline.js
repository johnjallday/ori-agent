/**
 * message-timeline.js
 *
 * Message timeline component for workspace communication
 * Displays agent messages with filtering, search, and export capabilities
 */

class MessageTimeline {
  constructor(workspaceId, containerId) {
    this.workspaceId = workspaceId;
    this.container = document.getElementById(containerId);
    this.messages = [];
    this.filteredMessages = [];
    this.filters = {
      agent: 'all',
      search: ''
    };
    this.unsubscribe = null;
  }

  /**
   * Initialize the timeline
   */
  async init() {
    await this.loadMessages();
    this.render();

    // Subscribe to real-time message events
    if (window.workspaceRealtime) {
      this.unsubscribe = window.workspaceRealtime.subscribeToWorkspace(
        this.workspaceId,
        (event) => this.handleRealtimeEvent(event)
      );
    }
  }

  /**
   * Load messages from API
   */
  async loadMessages() {
    try {
      const response = await fetch(`/api/orchestration/messages?workspace_id=${this.workspaceId}`);
      const data = await response.json();

      if (!data.error) {
        this.messages = data.messages || [];
        this.applyFilters();
      }
    } catch (error) {
      console.error('Error loading messages:', error);
    }
  }

  /**
   * Handle real-time events
   */
  handleRealtimeEvent(event) {
    if (event.type === 'message.sent') {
      this.loadMessages().then(() => this.render());
    }
  }

  /**
   * Apply filters to messages
   */
  applyFilters() {
    this.filteredMessages = this.messages.filter(msg => {
      if (this.filters.agent !== 'all' && msg.from !== this.filters.agent && msg.to !== this.filters.agent) {
        return false;
      }

      if (this.filters.search) {
        const search = this.filters.search.toLowerCase();
        if (!msg.content.toLowerCase().includes(search)) {
          return false;
        }
      }

      return true;
    });
  }

  /**
   * Render the timeline
   */
  render() {
    if (!this.container) return;

    this.container.innerHTML = `
      <div class="message-timeline">
        <div class="timeline-header mb-3">
          <div class="row g-2">
            <div class="col-md-4">
              <select id="agent-filter" class="form-control form-control-sm">
                <option value="all">All Agents</option>
                ${this.getUniqueAgents().map(agent =>
                  `<option value="${this.escapeHtml(agent)}" ${this.filters.agent === agent ? 'selected' : ''}>${this.escapeHtml(agent)}</option>`
                ).join('')}
              </select>
            </div>
            <div class="col-md-4">
              <input type="text" id="message-search" class="form-control form-control-sm" placeholder="Search messages..." value="${this.escapeHtml(this.filters.search)}">
            </div>
            <div class="col-md-4">
              <div class="d-flex gap-2">
                <button class="modern-btn modern-btn-secondary flex-grow-1" onclick="messageTimeline.refresh()">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M17.65,6.35C16.2,4.9 14.21,4 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20C15.73,20 18.84,17.45 19.73,14H17.65C16.83,16.33 14.61,18 12,18A6,6 0 0,1 6,12A6,6 0 0,1 12,6C13.66,6 15.14,6.69 16.22,7.78L13,11H20V4L17.65,6.35Z"/></svg>
                  Refresh
                </button>
                <button class="modern-btn modern-btn-primary" onclick="messageTimeline.exportMessages()">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M18,20H6V4H13V9H18V20Z"/></svg>
                  Export
                </button>
              </div>
            </div>
          </div>
        </div>

        <div class="mb-3">
          <small class="text-muted">Showing ${this.filteredMessages.length} of ${this.messages.length} messages</small>
        </div>

        <div class="timeline-container" style="max-height: 600px; overflow-y: auto;">
          ${this.renderMessages()}
        </div>
      </div>
    `;

    this.attachEventListeners();
  }

  /**
   * Render messages
   */
  renderMessages() {
    if (this.filteredMessages.length === 0) {
      return `
        <div class="text-center py-5">
          <svg width="48" height="48" viewBox="0 0 24 24" fill="currentColor" style="color: var(--text-muted); opacity: 0.5;"><path d="M20,2H4A2,2 0 0,0 2,4V22L6,18H20A2,2 0 0,0 22,16V4C22,2.89 21.1,2 20,2Z"/></svg>
          <p class="text-muted mt-2">No messages found</p>
        </div>
      `;
    }

    const messagesByDate = this.groupMessagesByDate(this.filteredMessages);

    return Object.keys(messagesByDate).map(date => `
      <div class="timeline-date-group mb-4">
        <div class="timeline-date-header mb-3">
          <span class="modern-badge badge-secondary">${this.formatDate(date)}</span>
        </div>
        ${messagesByDate[date].map(msg => this.renderMessage(msg)).join('')}
      </div>
    `).join('');
  }

  /**
   * Render a single message
   */
  renderMessage(msg) {
    const timestamp = new Date(msg.timestamp);
    const timeStr = timestamp.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

    return `
      <div class="timeline-message modern-card p-3 mb-2" data-message-id="${msg.id}">
        <div class="d-flex justify-content-between align-items-start mb-2">
          <div class="d-flex align-items-center gap-2">
            <div class="message-avatar" style="width: 32px; height: 32px; border-radius: 50%; background: var(--primary-color); display: flex; align-items: center; justify-content: center; color: white; font-weight: 600; font-size: 0.85rem;">
              ${this.getInitials(msg.from)}
            </div>
            <div>
              <div style="color: var(--text-primary); font-weight: 600; font-size: 0.9rem;">${this.escapeHtml(msg.from)}</div>
              <div class="text-muted small">to ${this.escapeHtml(msg.to || 'workspace')}</div>
            </div>
          </div>
          <span class="text-muted small">${timeStr}</span>
        </div>
        <div class="message-content" style="color: var(--text-secondary); padding-left: 40px; white-space: pre-wrap;">${this.escapeHtml(msg.content)}</div>
      </div>
    `;
  }

  groupMessagesByDate(messages) {
    const groups = {};
    messages.forEach(msg => {
      const date = new Date(msg.timestamp).toDateString();
      if (!groups[date]) groups[date] = [];
      groups[date].push(msg);
    });
    return groups;
  }

  getUniqueAgents() {
    const agents = new Set();
    this.messages.forEach(msg => {
      agents.add(msg.from);
      if (msg.to) agents.add(msg.to);
    });
    return Array.from(agents).sort();
  }

  attachEventListeners() {
    const agentFilter = document.getElementById('agent-filter');
    const searchInput = document.getElementById('message-search');

    if (agentFilter) {
      agentFilter.addEventListener('change', (e) => {
        this.filters.agent = e.target.value;
        this.applyFilters();
        this.render();
      });
    }

    if (searchInput) {
      searchInput.addEventListener('input', (e) => {
        this.filters.search = e.target.value;
        this.applyFilters();
        this.render();
      });
    }
  }

  async exportMessages() {
    try {
      const csv = this.messagesToCSV(this.filteredMessages);
      const blob = new Blob([csv], { type: 'text/csv' });
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `workspace-${this.workspaceId}-messages-${new Date().toISOString().split('T')[0]}.csv`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      window.URL.revokeObjectURL(url);
      this.showToast('Export Successful', 'Messages exported to CSV', 'success');
    } catch (error) {
      console.error('Error exporting messages:', error);
      this.showToast('Export Failed', 'Failed to export messages', 'error');
    }
  }

  messagesToCSV(messages) {
    const headers = ['Timestamp', 'From', 'To', 'Content'];
    const rows = messages.map(msg => [
      new Date(msg.timestamp).toISOString(),
      msg.from,
      msg.to || '',
      msg.content.replace(/"/g, '""')
    ]);
    const csvRows = [headers.join(','), ...rows.map(row => row.map(cell => `"${cell}"`).join(','))];
    return csvRows.join('\n');
  }

  async refresh() {
    await this.loadMessages();
    this.render();
  }

  formatDate(dateStr) {
    const date = new Date(dateStr);
    const today = new Date();
    const yesterday = new Date(today);
    yesterday.setDate(yesterday.getDate() - 1);

    if (date.toDateString() === today.toDateString()) return 'Today';
    if (date.toDateString() === yesterday.toDateString()) return 'Yesterday';
    return date.toLocaleDateString([], { weekday: 'long', month: 'short', day: 'numeric' });
  }

  getInitials(name) {
    return name.split(/[\s-_]/).map(part => part[0]).join('').toUpperCase().substring(0, 2);
  }

  showToast(title, message, type = 'info') {
    const toast = document.createElement('div');
    toast.className = `toast-notification toast-${type}`;
    toast.style.cssText = `position: fixed; top: 20px; right: 20px; background: var(--surface-color); border-left: 4px solid var(--${type === 'error' ? 'danger' : type === 'success' ? 'success' : 'info'}-color); padding: 1rem; border-radius: var(--radius-md); box-shadow: var(--shadow-lg); z-index: 9999; min-width: 300px; animation: slideIn 0.3s ease-out;`;
    toast.innerHTML = `<div style="color: var(--text-primary); font-weight: 600; margin-bottom: 0.25rem;">${this.escapeHtml(title)}</div><div style="color: var(--text-secondary); font-size: 0.9rem;">${this.escapeHtml(message)}</div>`;
    document.body.appendChild(toast);
    setTimeout(() => { toast.style.animation = 'slideOut 0.3s ease-in'; setTimeout(() => toast.remove(), 300); }, 4000);
  }

  destroy() {
    if (this.unsubscribe) this.unsubscribe();
  }

  escapeHtml(text) {
    if (text === null || text === undefined) return '';
    const div = document.createElement('div');
    div.textContent = String(text);
    return div.innerHTML;
  }
}

const messageTimelineStyle = document.createElement('style');
messageTimelineStyle.textContent = `
  .timeline-message { transition: all 0.2s ease; border-left: 3px solid transparent; }
  .timeline-message:hover { border-left-color: var(--primary-color); transform: translateX(4px); box-shadow: var(--shadow-md); }
  .timeline-date-header { text-align: center; position: relative; }
  .timeline-date-header::before { content: ''; position: absolute; top: 50%; left: 0; right: 0; height: 1px; background: var(--border-color); z-index: 0; }
  .timeline-date-header .modern-badge { position: relative; z-index: 1; background: var(--bg-color); padding: 0.25rem 1rem; }
  .message-avatar { flex-shrink: 0; }
  .timeline-container::-webkit-scrollbar { width: 8px; }
  .timeline-container::-webkit-scrollbar-track { background: var(--surface-color); border-radius: 4px; }
  .timeline-container::-webkit-scrollbar-thumb { background: var(--border-color); border-radius: 4px; }
  .timeline-container::-webkit-scrollbar-thumb:hover { background: var(--text-muted); }
`;
document.head.appendChild(messageTimelineStyle);

window.MessageTimeline = MessageTimeline;

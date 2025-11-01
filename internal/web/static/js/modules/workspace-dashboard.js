/**
 * workspace-dashboard.js
 *
 * Real-time workspace dashboard with live status updates
 * Displays workspace metrics, task progress, and agent activity
 */

console.log('[workspace-dashboard.js] File is loading - START OF FILE');

class WorkspaceDashboard {
  constructor(workspaceId, containerId) {
    this.workspaceId = workspaceId;
    this.container = document.getElementById(containerId);
    this.data = {
      workspace: null,
      tasks: [],
      agents: [],
      messages: [],
      events: []
    };
    this.unsubscribe = null;
    this.refreshInterval = null;
    this.messageTimeline = null;
    this.activeTab = 'overview';
  }

  /**
   * Initialize the dashboard
   */
  async init() {
    // Load initial data
    await this.loadWorkspaceData();
    await this.loadTasks();

    // Render dashboard
    this.render();

    // Subscribe to real-time updates
    if (window.workspaceRealtime) {
      this.unsubscribe = window.workspaceRealtime.subscribeToWorkspace(
        this.workspaceId,
        (event) => this.handleRealtimeEvent(event)
      );
    } else {
      // Fallback to polling if realtime not available
      this.startPolling();
    }
  }

  /**
   * Load workspace data from API
   */
  async loadWorkspaceData() {
    try {
      const response = await fetch(`/api/orchestration/workspace?id=${this.workspaceId}`);
      if (!response.ok) {
        throw new Error(`HTTP error! status: ${response.status}`);
      }
      const data = await response.json();

      if (data.error) {
        throw new Error(data.error);
      }

      this.data.workspace = data;
      this.data.agents = data.agents || [];
    } catch (error) {
      console.error('Error loading workspace data:', error);
      throw error; // Re-throw to be caught by init()
    }
  }

  /**
   * Load tasks from API
   */
  async loadTasks() {
    try {
      const response = await fetch(`/api/orchestration/tasks?workspace_id=${this.workspaceId}`);
      if (!response.ok) {
        throw new Error(`HTTP error! status: ${response.status}`);
      }
      const data = await response.json();

      if (data.error) {
        throw new Error(data.error);
      }

      this.data.tasks = data.tasks || [];
    } catch (error) {
      console.error('Error loading tasks:', error);
      // Don't re-throw here, tasks are optional
      this.data.tasks = [];
    }
  }

  /**
   * Handle real-time events
   */
  handleRealtimeEvent(event) {
    console.log('📡 Real-time event:', event.type, event);

    switch (event.type) {
      case 'workspace.status':
        this.updateWorkspaceStatus(event.data);
        break;

      case 'task.started':
      case 'task.completed':
      case 'task.failed':
        this.handleTaskUpdate(event);
        break;

      case 'workspace.updated':
        this.loadWorkspaceData().then(() => this.render());
        break;

      case 'workflow.started':
      case 'workflow.completed':
      case 'step.started':
      case 'step.completed':
        this.handleWorkflowUpdate(event);
        break;

      case 'connection.opened':
        this.showConnectionStatus('connected');
        break;

      case 'connection.error':
        this.showConnectionStatus('error', event.message);
        break;
    }
  }

  /**
   * Update workspace status from real-time data
   */
  updateWorkspaceStatus(statusData) {
    if (statusData.workspace_id === this.workspaceId) {
      this.data.workspace = { ...this.data.workspace, ...statusData };
      this.updateStatusBadge();
      this.updateWorkflowProgress();
    }
  }

  /**
   * Handle task updates
   */
  async handleTaskUpdate(event) {
    // Reload tasks to get latest data
    await this.loadTasks();

    // Update task list UI
    this.renderTaskList();

    // Show toast notification
    this.showTaskNotification(event);
  }

  /**
   * Handle workflow updates
   */
  handleWorkflowUpdate(event) {
    this.loadWorkspaceData().then(() => {
      this.updateWorkflowProgress();
    });
  }

  /**
   * Render the complete dashboard
   */
  render() {
    if (!this.container) return;

    const ws = this.data.workspace;

    // If workspace data is not loaded yet, show loading state
    if (!ws) {
      this.container.innerHTML = `
        <div class="text-center py-5">
          <div class="spinner-border text-primary" role="status">
            <span class="visually-hidden">Loading...</span>
          </div>
          <p class="mt-3" style="color: var(--text-muted);">Loading workspace data...</p>
        </div>
      `;
      return;
    }

    this.container.innerHTML = `
      <div class="workspace-dashboard">
        <!-- Header -->
        <div class="dashboard-header mb-4">
          <div class="d-flex justify-content-between align-items-start">
            <div>
              <h3 style="color: var(--text-primary);">${this.escapeHtml(ws?.name || ws?.id || 'Workspace')}</h3>
              ${ws?.description ? `<p class="text-muted mb-2">${this.escapeHtml(ws.description)}</p>` : ''}
              <div class="d-flex gap-2 align-items-center">
                <span id="workspace-status-badge" class="modern-badge ${this.getStatusBadgeClass(ws?.status)}">
                  ${this.escapeHtml(ws?.status || 'unknown')}
                </span>
                <span id="connection-status" class="modern-badge badge-secondary" style="font-size: 0.75rem;">
                  <span class="status-indicator status-online"></span> Live
                </span>
              </div>
            </div>
            <div class="d-flex gap-2">
              <button class="modern-btn modern-btn-secondary" onclick="workspaceDashboard.refresh()">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M17.65,6.35C16.2,4.9 14.21,4 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20C15.73,20 18.84,17.45 19.73,14H17.65C16.83,16.33 14.61,18 12,18A6,6 0 0,1 6,12A6,6 0 0,1 12,6C13.66,6 15.14,6.69 16.22,7.78L13,11H20V4L17.65,6.35Z"/>
                </svg>
                Refresh
              </button>
            </div>
          </div>
        </div>

        <!-- Tabs -->
        <ul class="nav nav-tabs mb-4" role="tablist">
          <li class="nav-item">
            <button class="nav-link ${this.activeTab === 'overview' ? 'active' : ''}" onclick="workspaceDashboard.switchTab('overview')">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1"><path d="M13,9H11V7H13M13,17H11V11H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z"/></svg>
              Overview
            </button>
          </li>
          <li class="nav-item">
            <button class="nav-link ${this.activeTab === 'messages' ? 'active' : ''}" onclick="workspaceDashboard.switchTab('messages')">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1"><path d="M20,2H4A2,2 0 0,0 2,4V22L6,18H20A2,2 0 0,0 22,16V4C22,2.89 21.1,2 20,2Z"/></svg>
              Messages
            </button>
          </li>
        </ul>

        <!-- Tab Content -->
        <div class="tab-content">
          <div id="overview-tab" style="display: ${this.activeTab === 'overview' ? 'block' : 'none'}">
            <!-- Metrics Cards -->
            <div class="row g-3 mb-4">
              ${this.renderMetricsCards()}
            </div>

            <!-- Workflow Progress -->
            ${this.renderWorkflowProgress()}

            <!-- Task List -->
            <div class="modern-card p-4 mb-4">
              <h5 style="color: var(--text-primary);" class="mb-3">
                <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                  <path d="M9,20.42L2.79,14.21L5.62,11.38L9,14.77L18.88,4.88L21.71,7.71L9,20.42Z"/>
                </svg>
                Tasks
              </h5>
              <div id="task-list-container">
                ${this.renderTaskList()}
              </div>
            </div>

            <!-- Agent Activity -->
            <div class="modern-card p-4">
              <h5 style="color: var(--text-primary);" class="mb-3">
                <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                  <path d="M12,5.5A3.5,3.5 0 0,1 15.5,9A3.5,3.5 0 0,1 12,12.5A3.5,3.5 0 0,1 8.5,9A3.5,3.5 0 0,1 12,5.5M5,8C5.56,8 6.08,8.15 6.53,8.42C6.38,9.85 6.8,11.27 7.66,12.38C7.16,13.34 6.16,14 5,14A3,3 0 0,1 2,11A3,3 0 0,1 5,8M19,8A3,3 0 0,1 22,11A3,3 0 0,1 19,14C17.84,14 16.84,13.34 16.34,12.38C17.2,11.27 17.62,9.85 17.47,8.42C17.92,8.15 18.44,8 19,8Z"/>
                </svg>
                Agents
              </h5>
              <div id="agent-list-container">
                ${this.renderAgentList()}
              </div>
            </div>
          </div>

          <div id="messages-tab" style="display: ${this.activeTab === 'messages' ? 'block' : 'none'}">
            <div class="modern-card p-4">
              <div id="message-timeline-container"></div>
            </div>
          </div>
        </div>
      </div>
    `;

    // Initialize message timeline if on messages tab
    if (this.activeTab === 'messages' && window.MessageTimeline) {
      setTimeout(() => {
        if (this.messageTimeline) {
          this.messageTimeline.destroy();
        }
        this.messageTimeline = new MessageTimeline(this.workspaceId, 'message-timeline-container');
        this.messageTimeline.init();
        window.messageTimeline = this.messageTimeline;
      }, 100);
    }
  }

  /**
   * Render metrics cards
   */
  renderMetricsCards() {
    const tasks = this.data.tasks || [];
    const completedTasks = tasks.filter(t => t.status === 'completed').length;
    const failedTasks = tasks.filter(t => t.status === 'failed').length;
    const pendingTasks = tasks.filter(t => t.status === 'pending').length;
    const inProgressTasks = tasks.filter(t => t.status === 'in_progress').length;

    return `
      <div class="col-md-3">
        <div class="metric-card modern-card p-3">
          <div class="d-flex justify-content-between align-items-center">
            <div>
              <p class="text-muted mb-1" style="font-size: 0.85rem;">Total Tasks</p>
              <h4 style="color: var(--text-primary); margin: 0;">${tasks.length}</h4>
            </div>
            <div class="metric-icon" style="background: var(--primary-color); opacity: 0.2; border-radius: 12px; padding: 12px;">
              <svg width="24" height="24" viewBox="0 0 24 24" fill="var(--primary-color)">
                <path d="M9,20.42L2.79,14.21L5.62,11.38L9,14.77L18.88,4.88L21.71,7.71L9,20.42Z"/>
              </svg>
            </div>
          </div>
        </div>
      </div>

      <div class="col-md-3">
        <div class="metric-card modern-card p-3">
          <div class="d-flex justify-content-between align-items-center">
            <div>
              <p class="text-muted mb-1" style="font-size: 0.85rem;">Completed</p>
              <h4 style="color: var(--success-color); margin: 0;">${completedTasks}</h4>
            </div>
            <div class="metric-icon" style="background: var(--success-color); opacity: 0.2; border-radius: 12px; padding: 12px;">
              <svg width="24" height="24" viewBox="0 0 24 24" fill="var(--success-color)">
                <path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/>
              </svg>
            </div>
          </div>
        </div>
      </div>

      <div class="col-md-3">
        <div class="metric-card modern-card p-3">
          <div class="d-flex justify-content-between align-items-center">
            <div>
              <p class="text-muted mb-1" style="font-size: 0.85rem;">In Progress</p>
              <h4 style="color: var(--info-color); margin: 0;">${inProgressTasks}</h4>
            </div>
            <div class="metric-icon" style="background: var(--info-color); opacity: 0.2; border-radius: 12px; padding: 12px;">
              <svg width="24" height="24" viewBox="0 0 24 24" fill="var(--info-color)">
                <path d="M12,4V2A10,10 0 0,0 2,12H4A8,8 0 0,1 12,4Z"/>
              </svg>
            </div>
          </div>
        </div>
      </div>

      <div class="col-md-3">
        <div class="metric-card modern-card p-3">
          <div class="d-flex justify-content-between align-items-center">
            <div>
              <p class="text-muted mb-1" style="font-size: 0.85rem;">Failed</p>
              <h4 style="color: var(--danger-color); margin: 0;">${failedTasks}</h4>
            </div>
            <div class="metric-icon" style="background: var(--danger-color); opacity: 0.2; border-radius: 12px; padding: 12px;">
              <svg width="24" height="24" viewBox="0 0 24 24" fill="var(--danger-color)">
                <path d="M13,13H11V7H13M13,17H11V15H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z"/>
              </svg>
            </div>
          </div>
        </div>
      </div>
    `;
  }

  /**
   * Render workflow progress
   */
  renderWorkflowProgress() {
    const ws = this.data.workspace;
    if (!ws?.workflow) return '';

    const progress = ws.workflow.progress || 0;

    return `
      <div id="workflow-progress-container" class="modern-card p-4 mb-4">
        <h5 style="color: var(--text-primary);" class="mb-3">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" class="me-2">
            <path d="M2,21H8V3H2V21M9,21H15V8H9V21M16,21H22V13H16V21Z"/>
          </svg>
          Workflow Progress
        </h5>
        <div class="progress mb-2" style="height: 24px; border-radius: var(--radius-md);">
          <div class="progress-bar progress-bar-striped progress-bar-animated"
               role="progressbar"
               style="width: ${progress}%;"
               aria-valuenow="${progress}"
               aria-valuemin="0"
               aria-valuemax="100">
            ${progress}%
          </div>
        </div>
        <p class="text-muted small mb-0">${ws.workflow.current_step || 'Initializing...'}</p>
      </div>
    `;
  }

  /**
   * Render task list
   */
  renderTaskList() {
    const tasks = this.data.tasks || [];

    if (tasks.length === 0) {
      return `
        <div class="text-center py-4">
          <svg width="48" height="48" viewBox="0 0 24 24" fill="currentColor" style="color: var(--text-muted); opacity: 0.5;">
            <path d="M19,3H14.82C14.4,1.84 13.3,1 12,1C10.7,1 9.6,1.84 9.18,3H5A2,2 0 0,0 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V5A2,2 0 0,0 19,3M12,3A1,1 0 0,1 13,4A1,1 0 0,1 12,5A1,1 0 0,1 11,4A1,1 0 0,1 12,3Z"/>
          </svg>
          <p class="text-muted mt-2">No tasks yet</p>
        </div>
      `;
    }

    return `
      <div class="task-list">
        ${tasks.map(task => this.renderTask(task)).join('')}
      </div>
    `;
  }

  /**
   * Render a single task
   */
  renderTask(task) {
    const statusBadge = this.getStatusBadgeClass(task.status);
    const statusIcon = this.getStatusIcon(task.status);

    return `
      <div class="task-item modern-card p-3 mb-2" data-task-id="${task.id}">
        <div class="d-flex justify-content-between align-items-start">
          <div class="flex-grow-1">
            <div class="d-flex align-items-center gap-2 mb-2">
              ${statusIcon}
              <h6 class="mb-0" style="color: var(--text-primary);">${this.escapeHtml(task.description)}</h6>
            </div>
            <div class="d-flex gap-3 text-muted small">
              <span>From: ${this.escapeHtml(task.from)}</span>
              <span>To: ${this.escapeHtml(task.to)}</span>
              ${task.priority ? `<span>Priority: ${task.priority}</span>` : ''}
            </div>
            ${task.error ? `
              <div class="alert alert-danger mt-2 mb-0 py-2" style="font-size: 0.85rem;">
                ${this.escapeHtml(task.error)}
              </div>
            ` : ''}
          </div>
          <span class="modern-badge ${statusBadge}">
            ${this.escapeHtml(task.status)}
          </span>
        </div>
      </div>
    `;
  }

  /**
   * Render agent list
   */
  renderAgentList() {
    const agents = this.data.agents || [];

    if (agents.length === 0) {
      return '<p class="text-muted">No agents configured</p>';
    }

    return `
      <div class="agent-list">
        ${agents.map(agent => `
          <div class="agent-item d-flex align-items-center gap-3 p-2 mb-2" style="border-left: 3px solid var(--primary-color); background: var(--surface-color); border-radius: var(--radius-sm);">
            <div class="status-indicator status-online"></div>
            <div>
              <div style="color: var(--text-primary); font-weight: 500;">${this.escapeHtml(agent)}</div>
              <div class="text-muted small">Active</div>
            </div>
          </div>
        `).join('')}
      </div>
    `;
  }

  /**
   * Update status badge
   */
  updateStatusBadge() {
    const badge = document.getElementById('workspace-status-badge');
    if (badge && this.data.workspace) {
      badge.className = `modern-badge ${this.getStatusBadgeClass(this.data.workspace.status)}`;
      badge.textContent = this.data.workspace.status || 'unknown';
    }
  }

  /**
   * Update workflow progress
   */
  updateWorkflowProgress() {
    const container = document.getElementById('workflow-progress-container');
    if (container) {
      const html = this.renderWorkflowProgress();
      if (html) {
        container.outerHTML = html;
      }
    }
  }

  /**
   * Show connection status
   */
  showConnectionStatus(status, message) {
    const statusBadge = document.getElementById('connection-status');
    if (statusBadge) {
      if (status === 'connected') {
        statusBadge.innerHTML = '<span class="status-indicator status-online"></span> Live';
        statusBadge.className = 'modern-badge badge-success';
      } else if (status === 'error') {
        statusBadge.innerHTML = '<span class="status-indicator status-offline"></span> Disconnected';
        statusBadge.className = 'modern-badge badge-danger';
        if (message) {
          this.showToast('Connection Error', message, 'error');
        }
      }
    }
  }

  /**
   * Show task notification
   */
  showTaskNotification(event) {
    let message = '';
    let type = 'info';

    switch (event.type) {
      case 'task.started':
        message = `Task started: ${event.data?.data?.description || 'Unknown task'}`;
        type = 'info';
        break;
      case 'task.completed':
        message = `Task completed: ${event.data?.data?.description || 'Unknown task'}`;
        type = 'success';
        break;
      case 'task.failed':
        message = `Task failed: ${event.data?.data?.description || 'Unknown task'}`;
        type = 'error';
        break;
    }

    if (message) {
      this.showToast('Task Update', message, type);
    }
  }

  /**
   * Show toast notification
   */
  showToast(title, message, type = 'info') {
    // Simple toast implementation
    const toast = document.createElement('div');
    toast.className = `toast-notification toast-${type}`;
    toast.style.cssText = `
      position: fixed;
      top: 20px;
      right: 20px;
      background: var(--surface-color);
      border-left: 4px solid var(--${type === 'error' ? 'danger' : type === 'success' ? 'success' : 'info'}-color);
      padding: 1rem;
      border-radius: var(--radius-md);
      box-shadow: var(--shadow-lg);
      z-index: 9999;
      min-width: 300px;
      animation: slideIn 0.3s ease-out;
    `;

    toast.innerHTML = `
      <div style="color: var(--text-primary); font-weight: 600; margin-bottom: 0.25rem;">${this.escapeHtml(title)}</div>
      <div style="color: var(--text-secondary); font-size: 0.9rem;">${this.escapeHtml(message)}</div>
    `;

    document.body.appendChild(toast);

    setTimeout(() => {
      toast.style.animation = 'slideOut 0.3s ease-in';
      setTimeout(() => toast.remove(), 300);
    }, 4000);
  }

  /**
   * Get status badge class
   */
  getStatusBadgeClass(status) {
    switch (status) {
      case 'active':
      case 'in_progress':
        return 'badge-info';
      case 'completed':
        return 'badge-success';
      case 'failed':
        return 'badge-danger';
      case 'pending':
        return 'badge-secondary';
      default:
        return 'badge-secondary';
    }
  }

  /**
   * Get status icon
   */
  getStatusIcon(status) {
    const iconColor = status === 'completed' ? 'var(--success-color)' :
                     status === 'failed' ? 'var(--danger-color)' :
                     status === 'in_progress' ? 'var(--info-color)' :
                     'var(--text-muted)';

    let path = '';
    switch (status) {
      case 'completed':
        path = 'M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z';
        break;
      case 'failed':
        path = 'M13,13H11V7H13M13,17H11V15H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z';
        break;
      case 'in_progress':
        path = 'M12,4V2A10,10 0 0,0 2,12H4A8,8 0 0,1 12,4Z';
        break;
      default:
        path = 'M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2M12,20C7.59,20 4,16.41 4,12C4,7.59 7.59,4 12,4C16.41,4 20,7.59 20,12C20,16.41 16.41,20 12,20Z';
    }

    return `
      <svg width="16" height="16" viewBox="0 0 24 24" fill="${iconColor}">
        <path d="${path}"/>
      </svg>
    `;
  }

  /**
   * Switch tabs
   */
  switchTab(tab) {
    this.activeTab = tab;
    this.render();
  }

  /**
   * Refresh dashboard data
   */
  async refresh() {
    await this.loadWorkspaceData();
    await this.loadTasks();
    this.render();
  }

  /**
   * Start polling (fallback)
   */
  startPolling() {
    this.refreshInterval = setInterval(() => {
      this.refresh();
    }, 5000); // Poll every 5 seconds
  }

  /**
   * Stop polling
   */
  stopPolling() {
    if (this.refreshInterval) {
      clearInterval(this.refreshInterval);
      this.refreshInterval = null;
    }
  }

  /**
   * Destroy dashboard
   */
  destroy() {
    if (this.unsubscribe) {
      this.unsubscribe();
    }
    if (this.messageTimeline) {
      this.messageTimeline.destroy();
    }
    this.stopPolling();
  }

  /**
   * Escape HTML
   */
  escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }
}

// Add CSS animations
const workspaceDashboardStyle = document.createElement('style');
workspaceDashboardStyle.textContent = `
  @keyframes slideIn {
    from {
      transform: translateX(400px);
      opacity: 0;
    }
    to {
      transform: translateX(0);
      opacity: 1;
    }
  }

  @keyframes slideOut {
    from {
      transform: translateX(0);
      opacity: 1;
    }
    to {
      transform: translateX(400px);
      opacity: 0;
    }
  }

  .status-indicator {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    display: inline-block;
  }

  .status-online {
    background-color: var(--success-color);
    box-shadow: 0 0 0 2px rgba(var(--success-color-rgb, 40, 167, 69), 0.2);
  }

  .status-offline {
    background-color: var(--danger-color);
    box-shadow: 0 0 0 2px rgba(var(--danger-color-rgb, 220, 53, 69), 0.2);
  }

  .metric-card {
    transition: transform 0.2s ease, box-shadow 0.2s ease;
  }

  .metric-card:hover {
    transform: translateY(-2px);
    box-shadow: var(--shadow-lg);
  }

  .task-item {
    transition: all 0.2s ease;
  }

  .task-item:hover {
    transform: translateX(4px);
    box-shadow: var(--shadow-md);
  }

  .progress-bar-animated {
    animation: progress-bar-stripes 1s linear infinite;
  }

  @keyframes progress-bar-stripes {
    0% {
      background-position: 1rem 0;
    }
    100% {
      background-position: 0 0;
    }
  }
`;
document.head.appendChild(workspaceDashboardStyle);

// Export for use in templates
console.log('[workspace-dashboard.js] Loading... WorkspaceDashboard class:', typeof WorkspaceDashboard);
window.WorkspaceDashboard = WorkspaceDashboard;
console.log('[workspace-dashboard.js] Exported to window.WorkspaceDashboard:', typeof window.WorkspaceDashboard);

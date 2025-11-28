/**
 * Dashboard Renderer Module
 * Handles main dashboard rendering, metrics cards, and progress visualization
 */

export class DashboardRenderer {
  constructor(parent) {
    this.parent = parent;
  }

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
          <li class="nav-item">
            <button class="nav-link ${this.activeTab === 'scheduled' ? 'active' : ''}" onclick="workspaceDashboard.switchTab('scheduled')">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1"><path d="M12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22C6.47,22 2,17.5 2,12A10,10 0 0,1 12,2M12.5,7V12.25L17,14.92L16.25,16.15L11,13V7H12.5Z"/></svg>
              Scheduled Tasks
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
              <div class="d-flex justify-content-between align-items-center mb-3">
                <h5 style="color: var(--text-primary);" class="mb-0">
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                    <path d="M9,20.42L2.79,14.21L5.62,11.38L9,14.77L18.88,4.88L21.71,7.71L9,20.42Z"/>
                  </svg>
                  Tasks
                </h5>
                <button class="modern-btn modern-btn-primary modern-btn-sm" onclick="workspaceDashboard.showCreateTaskForm()">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                    <path d="M19,13H13V19H11V13H5V11H11V5H13V11H19V13Z"/>
                  </svg>
                  Create Task
                </button>
              </div>
              <div id="create-task-form" style="display: none; background: var(--surface-color); border-radius: var(--radius-md);" class="mb-3 p-3">
                <form id="task-form" onsubmit="event.preventDefault(); workspaceDashboard.createTask();">
                  <div class="mb-2">
                    <label class="form-label small" style="color: var(--text-primary);">From (Sender Agent)</label>
                    <select id="task-from" class="form-control form-control-sm" required>
                      ${this.data.agents.map(agent => `<option value="${this.escapeHtml(agent)}">${this.escapeHtml(agent)}</option>`).join('')}
                    </select>
                  </div>
                  <div class="mb-2">
                    <label class="form-label small" style="color: var(--text-primary);">To (Recipient Agent)</label>
                    <select id="task-to" class="form-control form-control-sm" required>
                      ${this.data.agents.map(agent => `<option value="${this.escapeHtml(agent)}">${this.escapeHtml(agent)}</option>`).join('')}
                    </select>
                  </div>
                  <div class="mb-2">
                    <label class="form-label small" style="color: var(--text-primary);">Task Description</label>
                    <textarea id="task-description" class="form-control form-control-sm" rows="2" placeholder="What should this agent do?" required></textarea>
                  </div>
                  <div class="mb-2">
                    <label class="form-label small" style="color: var(--text-primary);">Priority</label>
                    <select id="task-priority" class="form-control form-control-sm">
                      <option value="0">Normal</option>
                      <option value="1">High</option>
                      <option value="2">Urgent</option>
                    </select>
                  </div>
                  <div class="mb-3">
                    <label class="form-label small" style="color: var(--text-primary);">
                      Use Results From Previous Tasks (Optional)
                      <span class="text-muted ms-1" title="Select completed tasks whose results will be provided as input to this task">ℹ️</span>
                    </label>
                    <select id="task-input-tasks" class="form-control form-control-sm" multiple size="3">
                      ${this.renderCompletedTaskOptions()}
                    </select>
                    <small class="text-muted">Hold Ctrl/Cmd to select multiple tasks</small>
                  </div>
                  <div class="mb-3" id="combination-mode-container" style="display: none;">
                    <label class="form-label small" style="color: var(--text-primary);">
                      Result Combination Mode
                      <span class="text-muted ms-1" title="How should results from previous tasks be combined with this task?">ℹ️</span>
                    </label>
                    <select id="task-combination-mode" class="form-control form-control-sm">
                      <option value="default">Default (Include as context)</option>
                      <option value="append">Append (Build upon results)</option>
                      <option value="merge">Merge (Synthesize into coherent whole)</option>
                      <option value="summarize">Summarize (Extract key points)</option>
                      <option value="compare">Compare (Find similarities/differences)</option>
                      <option value="custom">Custom (Specify instructions)</option>
                    </select>
                  </div>
                  <div class="mb-3" id="combination-instruction-container" style="display: none;">
                    <label class="form-label small" style="color: var(--text-primary);">Custom Combination Instructions</label>
                    <textarea id="task-combination-instruction" class="form-control form-control-sm" rows="2" placeholder="Describe how to combine the results..."></textarea>
                  </div>
                  <div class="d-flex gap-2">
                    <button type="submit" class="modern-btn modern-btn-primary modern-btn-sm">
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                        <path d="M9,20.42L2.79,14.21L5.62,11.38L9,14.77L18.88,4.88L21.71,7.71L9,20.42Z"/>
                      </svg>
                      Create
                    </button>
                    <button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" onclick="workspaceDashboard.hideCreateTaskForm()">Cancel</button>
                  </div>
                </form>
              </div>
              <div id="task-list-container">
                ${this.renderTaskList()}
              </div>
            </div>

            <!-- Agent Activity -->
            <div class="modern-card p-4">
              <div class="d-flex justify-content-between align-items-center mb-3">
                <h5 style="color: var(--text-primary);" class="mb-0">
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                    <path d="M12,5.5A3.5,3.5 0 0,1 15.5,9A3.5,3.5 0 0,1 12,12.5A3.5,3.5 0 0,1 8.5,9A3.5,3.5 0 0,1 12,5.5M5,8C5.56,8 6.08,8.15 6.53,8.42C6.38,9.85 6.8,11.27 7.66,12.38C7.16,13.34 6.16,14 5,14A3,3 0 0,1 2,11A3,3 0 0,1 5,8M19,8A3,3 0 0,1 22,11A3,3 0 0,1 19,14C17.84,14 16.84,13.34 16.34,12.38C17.2,11.27 17.62,9.85 17.47,8.42C17.92,8.15 18.44,8 19,8Z"/>
                  </svg>
                  Agents
                </h5>
                <button class="modern-btn modern-btn-primary modern-btn-sm" onclick="workspaceDashboard.showAddAgentForm()">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                    <path d="M19,13H13V19H11V13H5V11H11V5H13V11H19V13Z"/>
                  </svg>
                  Add Agent
                </button>
              </div>
              <div id="add-agent-form" style="display: none; background: var(--surface-color); border-radius: var(--radius-md);" class="mb-3 p-3">
                <form id="agent-form" onsubmit="event.preventDefault(); workspaceDashboard.addAgent();">
                  <div class="mb-2">
                    <label class="form-label small" style="color: var(--text-primary);">Select Agent</label>
                    <select id="agent-to-add" class="form-control form-control-sm" required>
                      <option value="">-- Select an agent --</option>
                    </select>
                  </div>
                  <div class="d-flex gap-2">
                    <button type="submit" class="modern-btn modern-btn-primary modern-btn-sm">
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                        <path d="M9,20.42L2.79,14.21L5.62,11.38L9,14.77L18.88,4.88L21.71,7.71L9,20.42Z"/>
                      </svg>
                      Add
                    </button>
                    <button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" onclick="workspaceDashboard.hideAddAgentForm()">Cancel</button>
                  </div>
                </form>
              </div>
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

          <div id="scheduled-tab" style="display: ${this.activeTab === 'scheduled' ? 'block' : 'none'}">
            ${this.renderScheduledTasksTab()}
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

    // Initialize combination mode controls
    this.initializeCombinationModeControls();
  }

  updateStatusBadge() {
    const badge = document.getElementById('workspace-status-badge');
    if (badge && this.data.workspace) {
      badge.className = `modern-badge ${this.getStatusBadgeClass(this.data.workspace.status)}`;
      badge.textContent = this.data.workspace.status || 'unknown';
    }
  }

  updateWorkflowProgress() {
    const container = document.getElementById('workflow-progress-container');
    if (container) {
      const html = this.renderWorkflowProgress();
      if (html) {
        container.outerHTML = html;
      }
    }
  }

}

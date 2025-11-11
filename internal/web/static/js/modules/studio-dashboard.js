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
      events: [],
      scheduledTasks: []
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
    await this.loadScheduledTasks();

    // Render dashboard
    this.render();

    // Subscribe to real-time updates (SSE)
    if (window.workspaceRealtime) {
      this.unsubscribe = window.workspaceRealtime.subscribeToWorkspace(
        this.workspaceId,
        (event) => this.handleRealtimeEvent(event)
      );
    }
    // Note: No automatic polling - use manual Refresh button if needed
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
   * Load scheduled tasks from API
   */
  async loadScheduledTasks() {
    try {
      const response = await fetch(`/api/orchestration/scheduled-tasks?workspace_id=${this.workspaceId}`);
      if (!response.ok) {
        throw new Error(`HTTP error! status: ${response.status}`);
      }
      const data = await response.json();

      if (data.error) {
        throw new Error(data.error);
      }

      this.data.scheduledTasks = data.scheduled_tasks || [];
    } catch (error) {
      console.error('Error loading scheduled tasks:', error);
      this.data.scheduledTasks = [];
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

  /**
   * Initialize the combination mode UI controls
   */
  initializeCombinationModeControls() {
    const inputTasksSelect = document.getElementById('task-input-tasks');
    const combinationModeContainer = document.getElementById('combination-mode-container');
    const combinationModeSelect = document.getElementById('task-combination-mode');
    const combinationInstructionContainer = document.getElementById('combination-instruction-container');

    if (!inputTasksSelect || !combinationModeContainer || !combinationModeSelect || !combinationInstructionContainer) {
      return;
    }

    // Show/hide combination mode when input tasks selection changes
    inputTasksSelect.addEventListener('change', () => {
      const hasInputTasks = inputTasksSelect.selectedOptions.length > 0;
      combinationModeContainer.style.display = hasInputTasks ? 'block' : 'none';

      // Also hide instruction container if no input tasks
      if (!hasInputTasks) {
        combinationInstructionContainer.style.display = 'none';
      }
    });

    // Show/hide custom instruction field based on combination mode
    combinationModeSelect.addEventListener('change', () => {
      const isCustomMode = combinationModeSelect.value === 'custom';
      const hasInputTasks = inputTasksSelect.selectedOptions.length > 0;
      combinationInstructionContainer.style.display = (isCustomMode && hasInputTasks) ? 'block' : 'none';
    });
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
    const canExecute = task.status === 'pending' || task.status === 'assigned';
    const hasResult = task.result && task.status === 'completed';
    const hasInputTasks = task.input_task_ids && task.input_task_ids.length > 0;
    const hasCombinationMode = task.result_combination_mode && task.result_combination_mode !== 'default';

    return `
      <div class="task-item modern-card p-3 mb-2" data-task-id="${task.id}" style="position: relative; cursor: pointer;" onclick="workspaceDashboard.showTaskDetails('${task.id}')">
        ${hasResult ? `
          <span class="position-absolute top-0 end-0 m-2" title="This task has a result that can be used in other tasks" style="cursor: help;">
            📊
          </span>
        ` : ''}
        <div class="d-flex justify-content-between align-items-start" onclick="event.stopPropagation();">
          <div class="flex-grow-1">
            <div class="d-flex align-items-center gap-2 mb-2">
              ${statusIcon}
              <h6 class="mb-0" style="color: var(--text-primary);">${this.escapeHtml(task.description)}</h6>
            </div>
            <div class="d-flex gap-3 text-muted small">
              <span>From: ${this.escapeHtml(task.from)}</span>
              <span>To: ${this.escapeHtml(task.to)}</span>
              ${task.priority ? `<span>Priority: ${task.priority}</span>` : ''}
              ${hasInputTasks ? `<span style="color: #9b59b6;" title="Uses results from ${task.input_task_ids.length} task(s)">🔗 ${task.input_task_ids.length} input(s)</span>` : ''}
              ${hasCombinationMode ? `<span style="color: #e67e22;" title="Combination mode: ${task.result_combination_mode}">⚙️ ${this.escapeHtml(task.result_combination_mode)}</span>` : ''}
            </div>
            ${task.result ? `
              <div class="alert alert-success mt-2 mb-0 py-2" style="font-size: 0.85rem;">
                <strong>Result:</strong>
                ${task.result.length > 300 ? `
                  <br>
                  <pre style="white-space: pre-wrap; margin-bottom: 0; font-size: 0.85rem; max-height: 150px; overflow: hidden;">${this.escapeHtml(task.result.substring(0, 300))}...</pre>
                  <button class="btn btn-sm btn-outline-success mt-2" onclick="workspaceDashboard.showTaskDetails('${task.id}')">
                    View Full Result
                  </button>
                ` : `
                  <br>
                  <pre style="white-space: pre-wrap; margin-bottom: 0; font-size: 0.85rem;">${this.escapeHtml(task.result)}</pre>
                `}
              </div>
            ` : ''}
            ${task.error ? `
              <div class="alert alert-danger mt-2 mb-0 py-2" style="font-size: 0.85rem;">
                <strong>Error:</strong> ${this.escapeHtml(task.error)}
              </div>
            ` : ''}
          </div>
          <div class="d-flex align-items-start gap-2">
            ${canExecute ? `
              <button class="modern-btn modern-btn-primary modern-btn-sm" onclick="workspaceDashboard.executeTask('${task.id}')">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M8,5.14V19.14L19,12.14L8,5.14Z"/>
                </svg>
                Execute Now
              </button>
            ` : ''}
            <button class="modern-btn modern-btn-danger modern-btn-sm" onclick="workspaceDashboard.deleteTask('${task.id}')" title="Delete task">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
              </svg>
            </button>
            <span class="modern-badge ${statusBadge}">
              ${this.escapeHtml(task.status)}
            </span>
          </div>
        </div>
      </div>
    `;
  }

  /**
   * Render options for completed tasks that can be used as inputs
   */
  renderCompletedTaskOptions() {
    const tasks = this.data.tasks || [];
    const completedTasks = tasks.filter(task => task.status === 'completed' && task.result);

    if (completedTasks.length === 0) {
      return '<option disabled>No completed tasks with results available</option>';
    }

    return completedTasks.map(task => {
      const truncatedDesc = task.description.length > 50
        ? task.description.substring(0, 47) + '...'
        : task.description;
      return `<option value="${task.id}">${this.escapeHtml(truncatedDesc)} (${task.from} → ${task.to})</option>`;
    }).join('');
  }

  /**
   * Render agent list
   */
  renderAgentList() {
    const agents = this.data.agents || [];

    if (agents.length === 0) {
      return '<p class="text-muted">No participating agents configured</p>';
    }

    return `
      <div class="agent-list">
        ${agents.map(agent => `
          <div class="agent-item d-flex align-items-center justify-content-between p-2 mb-2" style="border-left: 3px solid var(--primary-color); background: var(--surface-color); border-radius: var(--radius-sm);">
            <div class="d-flex align-items-center gap-3">
              <div class="status-indicator status-online"></div>
              <div>
                <div style="color: var(--text-primary); font-weight: 500;">
                  ${this.escapeHtml(agent)}
                </div>
                <div class="text-muted small">Active</div>
              </div>
            </div>
            <button class="btn btn-sm btn-outline-danger" onclick="workspaceDashboard.removeAgent('${this.escapeHtml(agent)}')" title="Remove agent from workspace">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                <path d="M19,13H5V11H19V13Z"/>
              </svg>
            </button>
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
   * Show create task form
   */
  showCreateTaskForm() {
    const form = document.getElementById('create-task-form');
    if (form) {
      form.style.display = 'block';
    }
  }

  /**
   * Hide create task form
   */
  hideCreateTaskForm() {
    const form = document.getElementById('create-task-form');
    if (form) {
      form.style.display = 'none';
      // Reset form
      document.getElementById('task-form').reset();
    }
  }

  /**
   * Create a new task
   */
  async createTask() {
    const from = document.getElementById('task-from').value;
    const to = document.getElementById('task-to').value;
    const description = document.getElementById('task-description').value;
    const priority = parseInt(document.getElementById('task-priority').value) || 0;

    // Get selected input task IDs
    const inputTasksSelect = document.getElementById('task-input-tasks');
    const inputTaskIds = Array.from(inputTasksSelect.selectedOptions).map(opt => opt.value);

    // Get combination mode and instruction
    const combinationMode = document.getElementById('task-combination-mode')?.value || 'default';
    const combinationInstruction = document.getElementById('task-combination-instruction')?.value || '';

    if (!from || !to || !description) {
      alert('Please fill in all required fields');
      return;
    }

    // Build request body
    const requestBody = {
      workspace_id: this.workspaceId,
      from: from,
      to: to,
      description: description,
      priority: priority,
    };

    // Add input_task_ids if any are selected
    if (inputTaskIds.length > 0) {
      requestBody.input_task_ids = inputTaskIds;

      // Add combination mode if input tasks are selected
      if (combinationMode && combinationMode !== 'default') {
        requestBody.result_combination_mode = combinationMode;
      }

      // Add custom instruction if mode is custom
      if (combinationMode === 'custom' && combinationInstruction) {
        requestBody.combination_instruction = combinationInstruction;
      }
    }

    try {
      const response = await fetch('/api/orchestration/tasks', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(requestBody),
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(error || 'Failed to create task');
      }

      const result = await response.json();

      // Hide form and reload tasks
      this.hideCreateTaskForm();
      await this.loadTasks();
      this.renderTaskList();

      // Show success notification
      this.showToast('✅ Task created successfully!', 'success');
    } catch (error) {
      console.error('Error creating task:', error);
      this.showToast('❌ Failed to create task: ' + error.message, 'error');
    }
  }

  /**
   * Execute a task immediately
   */
  async executeTask(taskId) {
    if (!confirm('Execute this task now?')) {
      return;
    }

    try {
      const response = await fetch('/api/orchestration/tasks/execute', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          task_id: taskId,
        }),
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(error || 'Failed to execute task');
      }

      const result = await response.json();

      // Reload tasks to show updated status
      await this.loadTasks();
      this.renderTaskList();

      // Show success notification
      this.showToast('✅ Task execution started!', 'success');
    } catch (error) {
      console.error('Error executing task:', error);
      this.showToast('❌ Failed to execute task: ' + error.message, 'error');
    }
  }

  /**
   * Delete a task
   */
  async deleteTask(taskId) {
    if (!confirm('Are you sure you want to delete this task? This action cannot be undone.')) {
      return;
    }

    try {
      const response = await fetch(`/api/orchestration/tasks?id=${taskId}`, {
        method: 'DELETE',
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(error || 'Failed to delete task');
      }

      // Reload tasks to show updated list
      await this.loadTasks();
      this.renderTaskList();

      // Show success notification
      this.showToast('Task Deleted', '✅ Task deleted successfully', 'success');
    } catch (error) {
      console.error('Error deleting task:', error);
      this.showToast('Delete Failed', '❌ Failed to delete task: ' + error.message, 'error');
    }
  }

  /**
   * Show task details in a modal
   */
  showTaskDetails(taskId) {
    const task = this.data.tasks.find(t => t.id === taskId);
    if (!task) {
      console.error('Task not found:', taskId);
      return;
    }

    // Get input tasks if any
    let inputTasksHTML = '';
    if (task.input_task_ids && task.input_task_ids.length > 0) {
      const inputTasks = task.input_task_ids.map(id => this.data.tasks.find(t => t.id === id)).filter(Boolean);
      if (inputTasks.length > 0) {
        inputTasksHTML = `
          <h6>Input Tasks:</h6>
          <div class="mb-3">
            ${inputTasks.map(it => `
              <div class="card mb-2" style="background-color: var(--bg-secondary);">
                <div class="card-body py-2">
                  <div class="d-flex justify-content-between align-items-start">
                    <div>
                      <strong>${this.escapeHtml(it.description.substring(0, 60))}${it.description.length > 60 ? '...' : ''}</strong>
                      <br>
                      <small class="text-muted">${it.from} → ${it.to}</small>
                    </div>
                    <button class="btn btn-sm btn-outline-primary" onclick="workspaceDashboard.showTaskDetails('${it.id}')">
                      View
                    </button>
                  </div>
                  ${it.result ? `
                    <div class="mt-2">
                      <small class="text-muted">Result:</small>
                      <pre style="font-size: 0.75rem; max-height: 100px; overflow-y: auto; background: var(--surface-color); padding: 8px; border-radius: 4px;">${this.escapeHtml(it.result.substring(0, 200))}${it.result.length > 200 ? '...' : ''}</pre>
                    </div>
                  ` : ''}
                </div>
              </div>
            `).join('')}
          </div>
        `;
      }
    }

    // Create modal HTML
    const modalHTML = `
      <div class="modal fade" id="taskDetailsModal" tabindex="-1" aria-labelledby="taskDetailsModalLabel" aria-hidden="true">
        <div class="modal-dialog modal-lg">
          <div class="modal-content" style="background-color: var(--surface-color); color: var(--text-primary);">
            <div class="modal-header" style="border-bottom: 1px solid var(--border-color);">
              <h5 class="modal-title" id="taskDetailsModalLabel">Task Details</h5>
              <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
            </div>
            <div class="modal-body">
              <h6>Description:</h6>
              <p>${this.escapeHtml(task.description)}</p>

              <h6>Status:</h6>
              <p><span class="modern-badge ${this.getStatusBadgeClass(task.status)}">${this.escapeHtml(task.status)}</span></p>

              <h6>Details:</h6>
              <ul>
                <li><strong>From:</strong> ${this.escapeHtml(task.from)}</li>
                <li><strong>To:</strong> ${this.escapeHtml(task.to)}</li>
                <li><strong>Priority:</strong> ${task.priority || 'N/A'}</li>
                <li><strong>Created:</strong> ${new Date(task.created_at).toLocaleString()}</li>
                ${task.completed_at ? `<li><strong>Completed:</strong> ${new Date(task.completed_at).toLocaleString()}</li>` : ''}
              </ul>

              ${inputTasksHTML}

              ${task.result ? `
                <h6>Result:</h6>
                <div class="alert alert-success">
                  <div class="d-flex justify-content-between align-items-center mb-2">
                    <strong>Task Output:</strong>
                    <button class="btn btn-sm btn-outline-success" onclick="navigator.clipboard.writeText(\`${this.escapeHtml(task.result).replace(/`/g, '\\`')}\`).then(() => alert('Result copied to clipboard!'))">
                      📋 Copy
                    </button>
                  </div>
                  <pre style="white-space: pre-wrap; margin-bottom: 0; max-height: 400px; overflow-y: auto;">${this.escapeHtml(task.result)}</pre>
                </div>
              ` : ''}

              ${task.error ? `
                <h6>Error:</h6>
                <div class="alert alert-danger">
                  <pre style="white-space: pre-wrap; margin-bottom: 0;">${this.escapeHtml(task.error)}</pre>
                </div>
              ` : ''}
            </div>
            <div class="modal-footer" style="border-top: 1px solid var(--border-color);">
              ${task.result && task.status === 'completed' ? `
                <button type="button" class="btn btn-primary" onclick="workspaceDashboard.useTaskResultInNewTask('${task.id}')" data-bs-dismiss="modal">
                  ✨ Use Result in New Task
                </button>
              ` : ''}
              <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">Close</button>
            </div>
          </div>
        </div>
      </div>
    `;

    // Remove any existing modal
    const existingModal = document.getElementById('taskDetailsModal');
    if (existingModal) {
      existingModal.remove();
    }

    // Append modal to body
    document.body.insertAdjacentHTML('beforeend', modalHTML);

    // Show modal
    const modal = new bootstrap.Modal(document.getElementById('taskDetailsModal'));
    modal.show();

    // Clean up modal after it's hidden
    document.getElementById('taskDetailsModal').addEventListener('hidden.bs.modal', function () {
      this.remove();
    });
  }

  /**
   * Pre-fill task creation form with selected task result as input
   */
  useTaskResultInNewTask(taskId) {
    // Show the create task form
    this.showCreateTaskForm();

    // Pre-select the task in the input tasks dropdown
    setTimeout(() => {
      const inputTasksSelect = document.getElementById('task-input-tasks');
      if (inputTasksSelect) {
        // Find and select the option with matching task ID
        for (let option of inputTasksSelect.options) {
          if (option.value === taskId) {
            option.selected = true;
            break;
          }
        }

        // Add a helpful placeholder text
        const descriptionField = document.getElementById('task-description');
        if (descriptionField && !descriptionField.value) {
          descriptionField.placeholder = 'Describe how to process the result from the selected task...';
          descriptionField.focus();
        }
      }
    }, 100);
  }

  /**
   * Show add agent form
   */
  showAddAgentForm() {
    const form = document.getElementById('add-agent-form');
    if (form) {
      form.style.display = 'block';
      this.populateAvailableAgents();
    }
  }

  /**
   * Hide add agent form
   */
  hideAddAgentForm() {
    const form = document.getElementById('add-agent-form');
    if (form) {
      form.style.display = 'none';
      document.getElementById('agent-form').reset();
    }
  }

  /**
   * Populate available agents dropdown
   */
  async populateAvailableAgents() {
    try {
      // Get all agents from the system
      const response = await fetch('/api/agents');
      if (!response.ok) {
        throw new Error('Failed to fetch agents');
      }

      const data = await response.json();
      const agents = data.agents || [];
      const select = document.getElementById('agent-to-add');
      if (!select) return;

      // Clear existing options except the first one
      select.innerHTML = '<option value="">-- Select an agent --</option>';

      // Get current workspace agents
      const currentAgents = this.data.agents || [];

      // Add agents that are not already in the workspace
      agents.forEach(agent => {
        if (!currentAgents.includes(agent.name)) {
          const option = document.createElement('option');
          option.value = agent.name;
          option.textContent = agent.name;
          select.appendChild(option);
        }
      });
    } catch (error) {
      console.error('Error fetching agents:', error);
      this.showToast('Error', '❌ Failed to fetch available agents', 'error');
    }
  }

  /**
   * Add an agent to the workspace
   */
  async addAgent() {
    const agentName = document.getElementById('agent-to-add').value;

    if (!agentName) {
      alert('Please select an agent');
      return;
    }

    try {
      const response = await fetch('/api/orchestration/workspace/agents', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          workspace_id: this.workspaceId,
          agent_name: agentName,
        }),
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(error || 'Failed to add agent');
      }

      const result = await response.json();

      // Hide form and reload workspace data
      this.hideAddAgentForm();
      await this.loadWorkspaceData();

      // Update agent list
      const agentListContainer = document.getElementById('agent-list-container');
      if (agentListContainer) {
        agentListContainer.innerHTML = this.renderAgentList();
      }

      // Show success notification
      this.showToast('Agent Added', `✅ ${agentName} added to workspace`, 'success');
    } catch (error) {
      console.error('Error adding agent:', error);
      this.showToast('Add Failed', '❌ Failed to add agent: ' + error.message, 'error');
    }
  }

  /**
   * Remove an agent from the workspace
   */
  async removeAgent(agentName) {
    if (!confirm(`Remove ${agentName} from this workspace?`)) {
      return;
    }

    try {
      const response = await fetch(`/api/orchestration/workspace/agents?workspace_id=${this.workspaceId}&agent_name=${encodeURIComponent(agentName)}`, {
        method: 'DELETE',
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(error || 'Failed to remove agent');
      }

      // Reload workspace data
      await this.loadWorkspaceData();

      // Update agent list
      const agentListContainer = document.getElementById('agent-list-container');
      if (agentListContainer) {
        agentListContainer.innerHTML = this.renderAgentList();
      }

      // Show success notification
      this.showToast('Agent Removed', `✅ ${agentName} removed from workspace`, 'success');
    } catch (error) {
      console.error('Error removing agent:', error);
      this.showToast('Remove Failed', '❌ Failed to remove agent: ' + error.message, 'error');
    }
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

  /**
   * ==========================================
   * SCHEDULED TASKS METHODS
   * ==========================================
   */

  /**
   * Render scheduled tasks tab
   */
  renderScheduledTasksTab() {
    const ws = this.data.workspace;
    if (!ws) return '<p>Loading...</p>';

    return `
      <div class="modern-card p-4">
        <div class="d-flex justify-content-between align-items-center mb-3">
          <h5 style="color: var(--text-primary);" class="mb-0">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" class="me-2">
              <path d="M12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22C6.47,22 2,17.5 2,12A10,10 0 0,1 12,2M12.5,7V12.25L17,14.92L16.25,16.15L11,13V7H12.5Z"/>
            </svg>
            Scheduled Tasks
          </h5>
          <button class="modern-btn modern-btn-primary modern-btn-sm" onclick="workspaceDashboard.showCreateScheduledTaskForm()">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
              <path d="M19,13H13V19H11V13H5V11H11V5H13V11H19V13Z"/>
            </svg>
            Create Scheduled Task
          </button>
        </div>

        <!-- Create Scheduled Task Form -->
        <div id="create-scheduled-task-form" style="display: none; background: var(--surface-color); border-radius: var(--radius-md);" class="mb-3 p-3">
          <form id="scheduled-task-form" onsubmit="event.preventDefault(); workspaceDashboard.createScheduledTask();">
            <div class="row">
              <div class="col-md-6 mb-2">
                <label class="form-label small" style="color: var(--text-primary);">Name</label>
                <input type="text" id="st-name" class="form-control form-control-sm" placeholder="e.g., Daily Status Report" required>
              </div>
              <div class="col-md-6 mb-2">
                <label class="form-label small" style="color: var(--text-primary);">Schedule Type</label>
                <select id="st-schedule-type" class="form-control form-control-sm" onchange="workspaceDashboard.updateScheduleFields()" required>
                  <option value="daily">Daily</option>
                  <option value="weekly">Weekly</option>
                  <option value="interval">Interval</option>
                  <option value="once">Once</option>
                </select>
              </div>
            </div>

            <div class="mb-2">
              <label class="form-label small" style="color: var(--text-primary);">Description</label>
              <input type="text" id="st-description" class="form-control form-control-sm" placeholder="What does this scheduled task do?">
            </div>

            <div class="row">
              <div class="col-md-6 mb-2">
                <label class="form-label small" style="color: var(--text-primary);">From (Sender Agent)</label>
                <select id="st-from" class="form-control form-control-sm" required>
                  ${this.data.agents.map(agent => `<option value="${this.escapeHtml(agent)}">${this.escapeHtml(agent)}</option>`).join('')}
                </select>
              </div>
              <div class="col-md-6 mb-2">
                <label class="form-label small" style="color: var(--text-primary);">To (Recipient Agent)</label>
                <select id="st-to" class="form-control form-control-sm" required>
                  ${this.data.agents.map(agent => `<option value="${this.escapeHtml(agent)}">${this.escapeHtml(agent)}</option>`).join('')}
                </select>
              </div>
            </div>

            <div class="mb-2">
              <label class="form-label small" style="color: var(--text-primary);">Task Prompt</label>
              <textarea id="st-prompt" class="form-control form-control-sm" rows="2" placeholder="What should the agent do when this task runs?" required></textarea>
            </div>

            <!-- Schedule-specific fields -->
            <div id="schedule-fields">
              <div id="daily-fields" style="display: block;">
                <div class="mb-2">
                  <label class="form-label small" style="color: var(--text-primary);">Time of Day (24-hour format)</label>
                  <input type="time" id="st-time-daily" class="form-control form-control-sm" value="09:00">
                </div>
              </div>

              <div id="weekly-fields" style="display: none;">
                <div class="row">
                  <div class="col-md-6 mb-2">
                    <label class="form-label small" style="color: var(--text-primary);">Day of Week</label>
                    <select id="st-day-of-week" class="form-control form-control-sm">
                      <option value="0">Sunday</option>
                      <option value="1">Monday</option>
                      <option value="2">Tuesday</option>
                      <option value="3">Wednesday</option>
                      <option value="4">Thursday</option>
                      <option value="5">Friday</option>
                      <option value="6">Saturday</option>
                    </select>
                  </div>
                  <div class="col-md-6 mb-2">
                    <label class="form-label small" style="color: var(--text-primary);">Time of Day</label>
                    <input type="time" id="st-time-weekly" class="form-control form-control-sm" value="09:00">
                  </div>
                </div>
              </div>

              <div id="interval-fields" style="display: none;">
                <div class="row">
                  <div class="col-md-6 mb-2">
                    <label class="form-label small" style="color: var(--text-primary);">Interval Value</label>
                    <input type="number" id="st-interval-value" class="form-control form-control-sm" value="1" min="1">
                  </div>
                  <div class="col-md-6 mb-2">
                    <label class="form-label small" style="color: var(--text-primary);">Interval Unit</label>
                    <select id="st-interval-unit" class="form-control form-control-sm">
                      <option value="hours">Hours</option>
                      <option value="minutes">Minutes</option>
                      <option value="days">Days</option>
                    </select>
                  </div>
                </div>
              </div>

              <div id="once-fields" style="display: none;">
                <div class="mb-2">
                  <label class="form-label small" style="color: var(--text-primary);">Execute At</label>
                  <input type="datetime-local" id="st-execute-at" class="form-control form-control-sm">
                </div>
              </div>
            </div>

            <div class="row">
              <div class="col-md-6 mb-3">
                <label class="form-label small" style="color: var(--text-primary);">Priority</label>
                <select id="st-priority" class="form-control form-control-sm">
                  <option value="0">Low</option>
                  <option value="1">Normal</option>
                  <option value="2">High</option>
                  <option value="3">Urgent</option>
                </select>
              </div>
              <div class="col-md-6 mb-3">
                <div class="form-check mt-4">
                  <input class="form-check-input" type="checkbox" id="st-enabled" checked>
                  <label class="form-check-label small" for="st-enabled" style="color: var(--text-primary);">
                    Enabled (start immediately)
                  </label>
                </div>
              </div>
            </div>

            <div class="d-flex gap-2">
              <button type="submit" class="modern-btn modern-btn-primary modern-btn-sm">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M9,20.42L2.79,14.21L5.62,11.38L9,14.77L18.88,4.88L21.71,7.71L9,20.42Z"/>
                </svg>
                Create
              </button>
              <button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" onclick="workspaceDashboard.hideCreateScheduledTaskForm()">Cancel</button>
            </div>
          </form>
        </div>

        <!-- Scheduled Tasks List -->
        <div id="scheduled-tasks-list">
          ${this.renderScheduledTasksList()}
        </div>
      </div>
    `;
  }

  /**
   * Render scheduled tasks list
   */
  renderScheduledTasksList() {
    if (!this.data.scheduledTasks || this.data.scheduledTasks.length === 0) {
      return `
        <div class="text-center py-4" style="color: var(--text-secondary);">
          <svg width="48" height="48" viewBox="0 0 24 24" fill="currentColor" style="opacity: 0.3;">
            <path d="M12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22C6.47,22 2,17.5 2,12A10,10 0 0,1 12,2M12.5,7V12.25L17,14.92L16.25,16.15L11,13V7H12.5Z"/>
          </svg>
          <p class="mt-2">No scheduled tasks yet</p>
          <small>Create a scheduled task to automate recurring prompts</small>
        </div>
      `;
    }

    return this.data.scheduledTasks.map(st => this.renderScheduledTask(st)).join('');
  }

  /**
   * Render a single scheduled task
   */
  renderScheduledTask(st) {
    const scheduleDesc = this.getScheduleDescription(st.schedule);
    const nextRun = st.next_run ? new Date(st.next_run).toLocaleString() : 'Not scheduled';
    const statusBadge = st.enabled
      ? '<span class="badge bg-success">Enabled</span>'
      : '<span class="badge bg-secondary">Disabled</span>';

    const hasHistory = st.execution_history && st.execution_history.length > 0;
    const successCount = hasHistory ? st.execution_history.filter(e => e.status === 'success').length : 0;
    const failedCount = hasHistory ? st.execution_history.filter(e => e.status === 'failed').length : 0;

    return `
      <div class="modern-card p-3 mb-2" style="border-left: 3px solid ${st.enabled ? 'var(--primary-color)' : '#6c757d'};">
        <div class="d-flex justify-content-between align-items-start">
          <div class="flex-grow-1">
            <h6 style="color: var(--text-primary);" class="mb-1">
              ${this.escapeHtml(st.name)}
              ${statusBadge}
            </h6>
            <p class="small mb-1" style="color: var(--text-secondary);">${this.escapeHtml(st.description || st.prompt)}</p>
            <div class="small" style="color: var(--text-tertiary);">
              <span class="me-3">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22C6.47,22 2,17.5 2,12A10,10 0 0,1 12,2M12.5,7V12.25L17,14.92L16.25,16.15L11,13V7H12.5Z"/>
                </svg>
                ${scheduleDesc}
              </span>
              <span class="me-3">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M12,1L8,5H11V14H13V5H16M18,23H6C4.89,23 4,22.1 4,21V9A2,2 0 0,1 6,7H9V9H6V21H18V9H15V7H18A2,2 0 0,1 20,9V21A2,2 0 0,1 18,23Z"/>
                </svg>
                ${this.escapeHtml(st.from)} → ${this.escapeHtml(st.to)}
              </span>
              ${st.next_run ? `
              <span>
                <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M19,4H18V2H16V4H8V2H6V4H5A2,2 0 0,0 3,6V20A2,2 0 0,0 5,22H19A2,2 0 0,0 21,20V6A2,2 0 0,0 19,4M19,20H5V10H19V20Z"/>
                </svg>
                Next: ${nextRun}
              </span>
              ` : ''}
            </div>
            <div class="small mt-1" style="color: var(--text-tertiary);">
              ${st.execution_count > 0 ? `Executed ${st.execution_count} times` : 'Not yet executed'}
              ${hasHistory ? ` (<span class="text-success">${successCount} ✓</span> / <span class="text-danger">${failedCount} ✗</span>)` : ''}
              ${hasHistory ? `
                <button class="btn btn-link btn-sm p-0 ms-2" style="font-size: 0.75rem;" onclick="workspaceDashboard.toggleHistory('${st.id}')">
                  <span id="history-toggle-${st.id}">Show history ▼</span>
                </button>
              ` : ''}
            </div>
            ${hasHistory ? `
              <div id="history-${st.id}" style="display: none; margin-top: 0.5rem; padding: 0.5rem; background: var(--surface-color); border-radius: 4px;">
                <div class="small fw-bold mb-1" style="color: var(--text-primary);">Execution History (last ${st.execution_history.length})</div>
                ${st.execution_history.slice().reverse().slice(0, 10).map(exec => `
                  <div class="d-flex justify-content-between align-items-center py-1 border-bottom" style="font-size: 0.7rem;">
                    <span style="color: var(--text-secondary);">${new Date(exec.executed_at).toLocaleString()}</span>
                    <span>
                      ${exec.status === 'success'
                        ? `<span class="badge bg-success">✓ Success</span> <a href="#" onclick="workspaceDashboard.switchTab('overview'); return false;" class="text-muted" title="Task ID: ${exec.task_id}">#${exec.task_id.substring(0,8)}</a>`
                        : `<span class="badge bg-danger">✗ Failed</span> <span class="text-danger" style="font-size: 0.65rem;" title="${this.escapeHtml(exec.error || '')}">${this.escapeHtml((exec.error || '').substring(0, 30))}...</span>`
                      }
                    </span>
                  </div>
                `).join('')}
              </div>
            ` : ''}
          </div>
          <div class="d-flex gap-1">
            ${st.enabled ? `
              <button class="btn btn-sm btn-outline-success" onclick="workspaceDashboard.triggerScheduledTask('${st.id}')" title="Trigger Now">
                ▶
              </button>
              <button class="btn btn-sm btn-outline-warning" onclick="workspaceDashboard.toggleScheduledTask('${st.id}', false)" title="Disable">
                ⏸
              </button>
            ` : `
              <button class="btn btn-sm btn-outline-success" onclick="workspaceDashboard.toggleScheduledTask('${st.id}', true)" title="Enable">
                ▶
              </button>
            `}
            <button class="btn btn-sm btn-outline-danger" onclick="workspaceDashboard.deleteScheduledTask('${st.id}')" title="Delete">
              🗑
            </button>
          </div>
        </div>
      </div>
    `;
  }

  /**
   * Get human-readable schedule description
   */
  getScheduleDescription(schedule) {
    switch (schedule.type) {
      case 'daily':
        return `Daily at ${schedule.time_of_day}`;
      case 'weekly':
        const days = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
        return `Weekly on ${days[schedule.day_of_week]} at ${schedule.time_of_day}`;
      case 'interval':
        const hours = Math.floor(schedule.interval / 3600000000000);
        const minutes = Math.floor((schedule.interval % 3600000000000) / 60000000000);
        if (hours > 0) return `Every ${hours} hour${hours > 1 ? 's' : ''}`;
        return `Every ${minutes} minute${minutes > 1 ? 's' : ''}`;
      case 'once':
        return `Once at ${new Date(schedule.execute_at).toLocaleString()}`;
      default:
        return 'Custom schedule';
    }
  }

  /**
   * Show create scheduled task form
   */
  showCreateScheduledTaskForm() {
    document.getElementById('create-scheduled-task-form').style.display = 'block';
  }

  /**
   * Hide create scheduled task form
   */
  hideCreateScheduledTaskForm() {
    document.getElementById('create-scheduled-task-form').style.display = 'none';
    document.getElementById('scheduled-task-form').reset();
  }

  /**
   * Update schedule fields based on selected type
   */
  updateScheduleFields() {
    const type = document.getElementById('st-schedule-type').value;
    document.getElementById('daily-fields').style.display = type === 'daily' ? 'block' : 'none';
    document.getElementById('weekly-fields').style.display = type === 'weekly' ? 'block' : 'none';
    document.getElementById('interval-fields').style.display = type === 'interval' ? 'block' : 'none';
    document.getElementById('once-fields').style.display = type === 'once' ? 'block' : 'none';
  }

  /**
   * Create scheduled task
   */
  async createScheduledTask() {
    const type = document.getElementById('st-schedule-type').value;
    const name = document.getElementById('st-name').value;
    const description = document.getElementById('st-description').value;
    const from = document.getElementById('st-from').value;
    const to = document.getElementById('st-to').value;
    const prompt = document.getElementById('st-prompt').value;
    const priority = parseInt(document.getElementById('st-priority').value);
    const enabled = document.getElementById('st-enabled').checked;

    // Build schedule config based on type
    let schedule = { type };

    switch (type) {
      case 'daily':
        schedule.time_of_day = document.getElementById('st-time-daily').value;
        break;
      case 'weekly':
        schedule.time_of_day = document.getElementById('st-time-weekly').value;
        schedule.day_of_week = parseInt(document.getElementById('st-day-of-week').value);
        break;
      case 'interval':
        const value = parseInt(document.getElementById('st-interval-value').value);
        const unit = document.getElementById('st-interval-unit').value;
        let nanoseconds;
        switch (unit) {
          case 'minutes': nanoseconds = value * 60 * 1000000000; break;
          case 'hours': nanoseconds = value * 3600 * 1000000000; break;
          case 'days': nanoseconds = value * 86400 * 1000000000; break;
        }
        schedule.interval = nanoseconds;
        break;
      case 'once':
        schedule.execute_at = document.getElementById('st-execute-at').value;
        break;
    }

    try {
      const response = await fetch('/api/orchestration/scheduled-tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: this.workspaceId,
          name,
          description,
          from,
          to,
          prompt,
          priority,
          schedule,
          enabled
        })
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(error);
      }

      await this.loadScheduledTasks();
      this.render();
      this.hideCreateScheduledTaskForm();
      this.showToast('✅ Scheduled task created successfully!', 'success');
    } catch (error) {
      console.error('Error creating scheduled task:', error);
      this.showToast('❌ Failed to create scheduled task: ' + error.message, 'error');
    }
  }

  /**
   * Toggle scheduled task enable/disable
   */
  async toggleScheduledTask(id, enable) {
    const action = enable ? 'enable' : 'disable';
    try {
      const response = await fetch(`/api/orchestration/scheduled-tasks/${id}/${action}`, {
        method: 'POST'
      });

      if (!response.ok) {
        throw new Error(`HTTP error! status: ${response.status}`);
      }

      await this.loadScheduledTasks();
      this.render();
      this.showToast(`✅ Scheduled task ${enable ? 'enabled' : 'disabled'}!`, 'success');
    } catch (error) {
      console.error('Error toggling scheduled task:', error);
      this.showToast('❌ Failed to toggle scheduled task', 'error');
    }
  }

  /**
   * Trigger scheduled task manually
   */
  async triggerScheduledTask(id) {
    if (!confirm('Trigger this scheduled task now?')) {
      return;
    }

    try {
      const response = await fetch(`/api/orchestration/scheduled-tasks/${id}/trigger`, {
        method: 'POST'
      });

      if (!response.ok) {
        throw new Error(`HTTP error! status: ${response.status}`);
      }

      const data = await response.json();
      await this.loadTasks();
      this.showToast(`✅ Task triggered! Task ID: ${data.task_id}`, 'success');

      // Switch to overview tab to show the new task
      if (confirm('Task created! Switch to Overview tab to see it?')) {
        this.switchTab('overview');
      }
    } catch (error) {
      console.error('Error triggering scheduled task:', error);
      this.showToast('❌ Failed to trigger scheduled task', 'error');
    }
  }

  /**
   * Delete scheduled task
   */
  async deleteScheduledTask(id) {
    if (!confirm('Are you sure you want to delete this scheduled task? This action cannot be undone.')) {
      return;
    }

    try {
      const response = await fetch(`/api/orchestration/scheduled-tasks/${id}`, {
        method: 'DELETE'
      });

      if (!response.ok) {
        throw new Error(`HTTP error! status: ${response.status}`);
      }

      await this.loadScheduledTasks();
      this.render();
      this.showToast('✅ Scheduled task deleted!', 'success');
    } catch (error) {
      console.error('Error deleting scheduled task:', error);
      this.showToast('❌ Failed to delete scheduled task', 'error');
    }
  }

  /**
   * Toggle visibility of execution history for a scheduled task
   */
  toggleHistory(id) {
    const historyDiv = document.getElementById(`history-${id}`);
    const toggleText = document.getElementById(`history-toggle-${id}`);

    if (historyDiv && toggleText) {
      if (historyDiv.style.display === 'none') {
        historyDiv.style.display = 'block';
        toggleText.textContent = 'Hide history ▲';
      } else {
        historyDiv.style.display = 'none';
        toggleText.textContent = 'Show history ▼';
      }
    }
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

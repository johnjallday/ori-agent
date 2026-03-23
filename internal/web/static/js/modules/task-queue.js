/**
 * Task Queue Module
 * Manages task list display, filtering, and real-time updates
 */

class TaskQueue {
  constructor() {
    this.container = null;
    this.listElement = null;
    this.emptyElement = null;
    this.filterButtons = null;
    this.currentFilter = 'all';
    this.tasks = [];
    this.currentWorkspaceId = null;
  }

  /**
   * Initialize the task queue
   */
  init() {
    this.container = document.getElementById('taskQueuePanel');
    this.listElement = document.getElementById('taskList');
    this.emptyElement = document.getElementById('taskListEmpty');
    this.filterButtons = document.querySelectorAll('#taskFilters .filter-btn');

    if (!this.container || !this.listElement) {
      console.warn('TaskQueue: Required elements not found');
      return;
    }

    this.setupEventListeners();
    this.loadWorkspaceContext();
    this.loadTasks();

    // Listen for task updates
    window.addEventListener('taskCreated', () => this.loadTasks());
    window.addEventListener('taskUpdated', () => this.loadTasks());
    window.addEventListener('sseEvent', (e) => this.handleSSEEvent(e.detail));

  }

  /**
   * Set up event listeners
   */
  setupEventListeners() {
    // Filter buttons
    this.filterButtons.forEach(btn => {
      btn.addEventListener('click', () => {
        this.setFilter(btn.dataset.filter);
      });
    });

    // Refresh button
    const refreshBtn = document.getElementById('refreshTasksBtn');
    if (refreshBtn) {
      refreshBtn.addEventListener('click', () => this.loadTasks());
    }
  }

  /**
   * Load current workspace context
   */
  async loadWorkspaceContext() {
    try {
      const urlParams = new URLSearchParams(window.location.search);
      this.currentWorkspaceId = urlParams.get('workspace') || sessionStorage.getItem('currentWorkspaceId');

      if (!this.currentWorkspaceId) {
        const response = await fetch('/api/workspaces');
        if (response.ok) {
          const data = await response.json();
          const workspaces = data.workspaces || data.folders || data.studios || (Array.isArray(data) ? data : []);
          if (workspaces.length > 0) {
            this.currentWorkspaceId = workspaces[0].id;
            sessionStorage.setItem('currentWorkspaceId', this.currentWorkspaceId);
          }
        }
      }
    } catch (error) {
      console.error('TaskQueue: Failed to load workspace context', error);
    }
  }

  /**
   * Load tasks from API
   * @param {string} filter - Status filter (all, pending, in_progress, completed, failed)
   */
  async loadTasks(filter = null) {
    if (filter) {
      this.currentFilter = filter;
    }

    // Wait for workspace context if not yet loaded
    if (!this.currentWorkspaceId) {
      await this.loadWorkspaceContext();
    }

    // If still no workspace, show empty state
    if (!this.currentWorkspaceId) {
      this.tasks = [];
      this.renderTasks();
      return;
    }

    this.showLoading();

    try {
      const params = new URLSearchParams();
      params.set('studio_id', this.currentWorkspaceId);

      if (this.currentFilter !== 'all') {
        params.set('status', this.currentFilter);
      }

      const url = '/api/orchestration/tasks?' + params.toString();
      const response = await fetch(url);

      if (!response.ok) {
        const errorData = await response.json().catch(() => ({}));
        throw new Error(errorData.message || 'Failed to fetch tasks');
      }

      const data = await response.json();
      this.tasks = Array.isArray(data) ? data : (data.tasks || []);
      this.renderTasks();

    } catch (error) {
      console.error('TaskQueue: Failed to load tasks', error);
      this.showError('Failed to load tasks');
    }
  }

  /**
   * Set the current filter
   * @param {string} filter - Filter value
   */
  setFilter(filter) {
    this.currentFilter = filter;

    // Update button states
    this.filterButtons.forEach(btn => {
      btn.classList.toggle('active', btn.dataset.filter === filter);
    });

    this.loadTasks();
  }

  /**
   * Render the task list
   */
  renderTasks() {
    if (!this.listElement) return;

    if (this.tasks.length === 0) {
      this.listElement.innerHTML = '';
      if (this.emptyElement) {
        this.emptyElement.style.display = 'flex';
      }
      return;
    }

    if (this.emptyElement) {
      this.emptyElement.style.display = 'none';
    }

    this.listElement.innerHTML = this.tasks.map(task => this.renderTask(task)).join('');

    // Add click handlers
    this.listElement.querySelectorAll('.task-card').forEach(card => {
      card.addEventListener('click', () => {
        this.expandTask(card.dataset.taskId);
      });
    });
  }

  /**
   * Render a single task card
   * @param {Object} task - Task data
   * @returns {string} HTML string
   */
  renderTask(task) {
    const statusLabel = this.getStatusLabel(task.status);
    const timeAgo = this.formatTimeAgo(task.created_at);

    return `
      <div class="task-card" data-task-id="${task.id}">
        <div class="task-card-header">
          <h4 class="task-card-title">${this.escapeHtml(task.description)}</h4>
          <span class="task-card-status ${task.status}">${statusLabel}</span>
        </div>
        <div class="task-card-meta">
          ${task.agent_name ? `
            <span class="task-card-agent">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                <path d="M12,2A2,2 0 0,1 14,4C14,4.74 13.6,5.39 13,5.73V7H14A7,7 0 0,1 21,14H22A1,1 0 0,1 23,15V18A1,1 0 0,1 22,19H21V20A2,2 0 0,1 19,22H5A2,2 0 0,1 3,20V19H2A1,1 0 0,1 1,18V15A1,1 0 0,1 2,14H3A7,7 0 0,1 10,7H11V5.73C10.4,5.39 10,4.74 10,4A2,2 0 0,1 12,2Z"/>
              </svg>
              ${this.escapeHtml(task.agent_name)}
            </span>
          ` : ''}
          <span class="task-card-time">${timeAgo}</span>
        </div>
      </div>
    `;
  }

  /**
   * Expand a task to show details
   * @param {string} taskId - Task ID
   */
  expandTask(taskId) {
    const task = this.tasks.find(t => t.id === taskId);
    if (!task) return;

    // For now, show details in a modal or sidebar
    // Could integrate with existing task modal
    if (window.taskModal && typeof window.taskModal.show === 'function') {
      window.taskModal.show(task);
    } else {
      // Fallback: could emit event for other handlers
      window.dispatchEvent(new CustomEvent('taskSelected', { detail: task }));
    }
  }

  /**
   * Update a task's status
   * @param {string} taskId - Task ID
   * @param {string} status - New status
   */
  updateStatus(taskId, status) {
    const task = this.tasks.find(t => t.id === taskId);
    if (task) {
      task.status = status;
      this.renderTasks();
    }
  }

  /**
   * Handle SSE events for real-time updates
   * @param {Object} event - SSE event data
   */
  handleSSEEvent(event) {
    if (!event || !event.type) return;

    switch (event.type) {
      case 'task_started':
      case 'task_completed':
      case 'task_failed':
        if (event.task_id) {
          this.loadTasks(); // Reload to get latest status
        }
        break;
    }
  }

  /**
   * Execute a task
   * @param {string} taskId - Task ID
   */
  async executeTask(taskId) {
    try {
      const response = await fetch('/api/orchestration/tasks/execute', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task_id: taskId })
      });

      if (!response.ok) throw new Error('Failed to execute task');

      this.loadTasks();

    } catch (error) {
      console.error('TaskQueue: Failed to execute task', error);
    }
  }

  /**
   * Cancel a task
   * @param {string} taskId - Task ID
   */
  async cancelTask(taskId) {
    try {
      const response = await fetch(`/api/orchestration/tasks/${taskId}`, {
        method: 'DELETE'
      });

      if (!response.ok) throw new Error('Failed to cancel task');

      this.loadTasks();

    } catch (error) {
      console.error('TaskQueue: Failed to cancel task', error);
    }
  }

  /**
   * Show loading state
   */
  showLoading() {
    if (this.listElement) {
      this.listElement.innerHTML = `
        <div class="task-list-loading">
          <div class="spinner-border spinner-border-sm" role="status">
            <span class="visually-hidden">Loading...</span>
          </div>
          <span>Loading tasks...</span>
        </div>
      `;
    }
    if (this.emptyElement) {
      this.emptyElement.style.display = 'none';
    }
  }

  /**
   * Show error state
   * @param {string} message - Error message
   */
  showError(message) {
    if (this.listElement) {
      this.listElement.innerHTML = `
        <div class="task-list-error" style="padding: 2rem; text-align: center; color: var(--text-muted);">
          <p>${this.escapeHtml(message)}</p>
          <button class="modern-btn modern-btn-secondary" onclick="window.taskQueue.loadTasks()">Retry</button>
        </div>
      `;
    }
  }

  /**
   * Get human-readable status label
   * @param {string} status - Task status
   * @returns {string} Label
   */
  getStatusLabel(status) {
    const labels = {
      pending: 'Pending',
      in_progress: 'Active',
      completed: 'Done',
      failed: 'Failed'
    };
    return labels[status] || status;
  }

  /**
   * Format timestamp as relative time
   * @param {string} timestamp - ISO timestamp
   * @returns {string} Relative time string
   */
  formatTimeAgo(timestamp) {
    if (!timestamp) return '';

    const date = new Date(timestamp);
    const now = new Date();
    const seconds = Math.floor((now - date) / 1000);

    if (seconds < 60) return 'Just now';
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
    if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
    return `${Math.floor(seconds / 86400)}d ago`;
  }

  /**
   * Escape HTML to prevent XSS
   * @param {string} str - String to escape
   * @returns {string} Escaped string
   */
  escapeHtml(str) {
    if (!str) return '';
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
  }
}

// Create global instance
window.taskQueue = new TaskQueue();

// Initialize when DOM is ready
document.addEventListener('DOMContentLoaded', () => {
  window.taskQueue.init();
});

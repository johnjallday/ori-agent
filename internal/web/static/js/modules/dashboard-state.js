/**
 * Dashboard State Module
 * Handles data loading, state management, and real-time updates
 */

export class DashboardState {
  constructor(workspaceId, parent) {
    this.workspaceId = workspaceId;
    this.parent = parent;
  }

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

  updateWorkspaceStatus(statusData) {
    if (statusData.workspace_id === this.workspaceId) {
      this.data.workspace = { ...this.data.workspace, ...statusData };
      this.updateStatusBadge();
      this.updateWorkflowProgress();
    }
  }

  async handleTaskUpdate(event) {
    // Reload tasks to get latest data
    await this.loadTasks();

    // Update task list UI
    this.renderTaskList();

    // Show toast notification
    this.showTaskNotification(event);
  }

  handleWorkflowUpdate(event) {
    this.loadWorkspaceData().then(() => {
      this.updateWorkflowProgress();
    });
  }

  async refresh() {
    await this.loadWorkspaceData();
    await this.loadTasks();
    this.render();
  }

}

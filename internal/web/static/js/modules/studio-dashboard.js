/**
 * workspace-dashboard.js (Orchestrator)
 *
 * Real-time workspace dashboard with live status updates
 * Main orchestrator that coordinates all dashboard modules
 */

import { DashboardState } from './dashboard-state.js';
import { DashboardUI } from './dashboard-ui.js';
import { DashboardAgents } from './dashboard-agents.js';
import { DashboardRenderer } from './dashboard-renderer.js';
import { DashboardTasks } from './dashboard-tasks.js';

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

    // Initialize specialized modules
    this.state = new DashboardState(workspaceId, this);
    this.ui = new DashboardUI(this);
    this.agents = new DashboardAgents(this);
    this.renderer = new DashboardRenderer(this);
    this.tasks = new DashboardTasks(this);
  }

  /**
   * Initialize the dashboard
   */
  async init() {
    // Load initial data
    await this.state.loadWorkspaceData();
    await this.state.loadTasks();

    // Render dashboard
    this.renderer.render();

    // Subscribe to real-time updates (SSE)
    if (window.workspaceRealtime) {
      this.unsubscribe = window.workspaceRealtime.subscribeToWorkspace(this.workspaceId, event =>
        this.state.handleRealtimeEvent(event)
      );
    }
  }

  /**
   * Cleanup and destroy
   */
  destroy() {
    if (this.unsubscribe) {
      this.unsubscribe();
    }
    this.ui.stopPolling();
  }

  // ==================== DELEGATION METHODS ====================

  // State methods
  loadWorkspaceData() {
    return this.state.loadWorkspaceData();
  }
  loadTasks() {
    return this.state.loadTasks();
  }
  handleRealtimeEvent(event) {
    return this.state.handleRealtimeEvent(event);
  }
  updateWorkspaceStatus(statusData) {
    return this.state.updateWorkspaceStatus(statusData);
  }
  handleTaskUpdate(event) {
    return this.state.handleTaskUpdate(event);
  }
  handleWorkflowUpdate(event) {
    return this.state.handleWorkflowUpdate(event);
  }
  refresh() {
    return this.state.refresh();
  }

  // UI methods
  showConnectionStatus(status, message) {
    return this.ui.showConnectionStatus(status, message);
  }
  showTaskNotification(event) {
    return this.ui.showTaskNotification(event);
  }
  showToast(title, message, type = 'info') {
    return this.ui.showToast(title, message, type);
  }
  getStatusBadgeClass(status) {
    return this.ui.getStatusBadgeClass(status);
  }
  getStatusIcon(status) {
    return this.ui.getStatusIcon(status);
  }
  switchTab(tab) {
    return this.ui.switchTab(tab);
  }
  startPolling() {
    return this.ui.startPolling();
  }
  stopPolling() {
    return this.ui.stopPolling();
  }
  escapeHtml(text) {
    return this.ui.escapeHtml(text);
  }

  // Agents methods
  renderAgentList() {
    return this.agents.renderAgentList();
  }
  showAddAgentForm() {
    return this.agents.showAddAgentForm();
  }
  hideAddAgentForm() {
    return this.agents.hideAddAgentForm();
  }
  populateAvailableAgents() {
    return this.agents.populateAvailableAgents();
  }
  addAgent() {
    return this.agents.addAgent();
  }
  removeAgent(agentName) {
    return this.agents.removeAgent(agentName);
  }

  // Renderer methods
  render() {
    return this.renderer.render();
  }
  renderMetricsCards() {
    return this.renderer.renderMetricsCards();
  }
  renderWorkflowProgress() {
    return this.renderer.renderWorkflowProgress();
  }
  updateStatusBadge() {
    return this.renderer.updateStatusBadge();
  }
  updateWorkflowProgress() {
    return this.renderer.updateWorkflowProgress();
  }

  // Tasks methods
  renderTaskList() {
    return this.tasks.renderTaskList();
  }
  renderTask(task) {
    return this.tasks.renderTask(task);
  }
  renderCompletedTaskOptions() {
    return this.tasks.renderCompletedTaskOptions();
  }
  showCreateTaskForm() {
    return this.tasks.showCreateTaskForm();
  }
  hideCreateTaskForm() {
    return this.tasks.hideCreateTaskForm();
  }
  createTask() {
    return this.tasks.createTask();
  }
  executeTask(taskId) {
    return this.tasks.executeTask(taskId);
  }
  deleteTask(taskId) {
    return this.tasks.deleteTask(taskId);
  }
  showTaskDetails(taskId) {
    return this.tasks.showTaskDetails(taskId);
  }
  useTaskResultInNewTask(taskId) {
    return this.tasks.useTaskResultInNewTask(taskId);
  }
  initializeCombinationModeControls() {
    return this.tasks.initializeCombinationModeControls();
  }
  toggleScheduleFields() {
    return this.tasks.toggleScheduleFields();
  }
  updateScheduleTypeFields() {
    return this.tasks.updateScheduleTypeFields();
  }
  toggleTaskComplete(taskId, completed) {
    return this.tasks.toggleTaskComplete(taskId, completed);
  }
  toggleEditScheduleFields() {
    return this.tasks.toggleEditScheduleFields();
  }
  updateEditScheduleTypeFields() {
    return this.tasks.updateEditScheduleTypeFields();
  }
  saveTaskSchedule(taskId) {
    return this.tasks.saveTaskSchedule(taskId);
  }
}

window.WorkspaceDashboard = WorkspaceDashboard;

let activeDashboard = null;

window.initWorkspaceDashboard = function (workspaceId, containerId) {
  if (activeDashboard) {
    activeDashboard.destroy();
  }
  activeDashboard = new WorkspaceDashboard(workspaceId, containerId);
  activeDashboard.init();
  return activeDashboard;
};

window.addEventListener('pagehide', () => {
  if (activeDashboard) {
    activeDashboard.destroy();
    activeDashboard = null;
  }
});

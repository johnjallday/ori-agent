/**
 * Agent Canvas Workflow Selector
 *
 * Dropdown component for filtering canvas tasks by workflow.
 * A workflow is any task that has subtasks (tasks with parent_id pointing to it).
 *
 * @module agent-canvas-workflow-selector
 */

import { EVENT_TYPES } from './agent-canvas-state.js';

/**
 * AgentCanvasWorkflowSelector - Dropdown for selecting workflows
 */
export class AgentCanvasWorkflowSelector {
  /**
   * Create a new workflow selector
   * @param {AgentCanvasState} state - Canvas state instance
   * @param {AgentCanvas} parent - Parent canvas instance
   * @param {string} containerId - DOM element ID for the dropdown container
   */
  constructor(state, parent, containerId) {
    this.state = state;
    this.parent = parent;
    this.containerId = containerId;
    this.container = null;
    this.dropdownOpen = false;

    // Bind methods
    this.handleDocumentClick = this.handleDocumentClick.bind(this);
    this.handleWorkflowSelected = this.handleWorkflowSelected.bind(this);
  }

  /**
   * Initialize the selector
   */
  init() {
    this.container = document.getElementById(this.containerId);
    if (!this.container) {
      console.warn('Workflow selector container not found:', this.containerId);
      return;
    }

    // Initial render
    this.render();

    // Listen for task changes to update workflow list
    this.state.on(EVENT_TYPES.TASK_CREATED, () => this.updateWorkflowList());
    this.state.on(EVENT_TYPES.TASK_UPDATED, () => this.updateWorkflowList());
    this.state.on(EVENT_TYPES.DATA_LOADED, () => this.updateWorkflowList());

    // Listen for workflow selection changes
    this.state.on(EVENT_TYPES.WORKFLOW_SELECTED, this.handleWorkflowSelected);

    // Close dropdown when clicking outside
    document.addEventListener('click', this.handleDocumentClick);

    // Check URL for workflow parameter
    this.initFromUrl();

    // Update canvas title on init
    this.updateCanvasTitle();
  }

  /**
   * Check URL for workflow parameter and set initial selection
   */
  initFromUrl() {
    const urlParams = new URLSearchParams(window.location.search);
    const workflowId = urlParams.get('workflow');
    if (workflowId) {
      this.state.setSelectedWorkflow(workflowId);
    }
  }

  /**
   * Render the dropdown component
   */
  render() {
    if (!this.container) return;

    const workflows = this.state.getAvailableWorkflows();
    const selectedWorkflow = this.state.getSelectedWorkflow();
    const totalTasks = this.state.getTotalTaskCount();
    const hasWorkflows = workflows.length > 0;

    // Determine display text
    let displayText = 'All Tasks';
    let displayCount = totalTasks;
    if (selectedWorkflow) {
      displayText = selectedWorkflow.name || selectedWorkflow.description || 'Workflow';
      const wf = workflows.find(w => w.id === selectedWorkflow.id);
      displayCount = wf ? wf.taskCount : 1;
    }

    this.container.innerHTML = `
      <div class="workflow-selector ${!hasWorkflows ? 'workflow-selector-disabled' : ''}" id="workflow-selector-dropdown">
        <button class="workflow-selector-trigger modern-btn modern-btn-secondary"
                onclick="event.stopPropagation(); window.workflowSelector?.toggleDropdown()"
                ${!hasWorkflows ? 'disabled title="No workflows available"' : ''}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1" style="vertical-align: text-bottom;">
            <path d="M17,3H7A2,2 0 0,0 5,5V21L12,18L19,21V5A2,2 0 0,0 17,3M15,14H13V17H11V14H9V12H11V9H13V12H15V14Z"/>
          </svg>
          <span class="workflow-selector-text">${this.escapeHtml(displayText)}</span>
          <span class="workflow-selector-count">(${displayCount})</span>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" class="ms-1" style="vertical-align: text-bottom;">
            <path d="M7.41,8.58L12,13.17L16.59,8.58L18,10L12,16L6,10L7.41,8.58Z"/>
          </svg>
        </button>

        <div class="workflow-selector-menu ${this.dropdownOpen ? 'show' : ''}" id="workflow-selector-menu">
          <div class="workflow-selector-item ${!selectedWorkflow ? 'selected' : ''}"
               onclick="event.stopPropagation(); window.workflowSelector?.selectWorkflow(null)">
            <span class="workflow-selector-item-icon">
              ${!selectedWorkflow ? '✓' : ''}
            </span>
            <span class="workflow-selector-item-text">All Tasks</span>
            <span class="workflow-selector-item-count">${totalTasks}</span>
          </div>

          ${
            hasWorkflows
              ? `
            <div class="workflow-selector-divider"></div>

            ${workflows
              .map(
                wf => `
              <div class="workflow-selector-item ${this.state.selectedWorkflowId === wf.id ? 'selected' : ''}"
                   onclick="event.stopPropagation(); window.workflowSelector?.selectWorkflow('${wf.id}')">
                <span class="workflow-selector-item-icon">
                  ${this.state.selectedWorkflowId === wf.id ? '✓' : ''}
                </span>
                <span class="workflow-selector-item-text">${this.escapeHtml(wf.name)}</span>
                <span class="workflow-selector-item-count">${wf.taskCount}</span>
              </div>
            `
              )
              .join('')}
          `
              : ''
          }

          <div class="workflow-selector-divider"></div>

          <div class="workflow-selector-item workflow-selector-action"
               onclick="event.stopPropagation(); window.workflowSelector?.handleCreateWorkflow()">
            <span class="workflow-selector-item-icon">+</span>
            <span class="workflow-selector-item-text">Create Workflow</span>
          </div>
        </div>
      </div>
    `;
  }

  /**
   * Toggle dropdown visibility
   */
  toggleDropdown() {
    this.dropdownOpen = !this.dropdownOpen;
    const menu = document.getElementById('workflow-selector-menu');
    if (menu) {
      menu.classList.toggle('show', this.dropdownOpen);
    }
  }

  /**
   * Close dropdown
   */
  closeDropdown() {
    this.dropdownOpen = false;
    const menu = document.getElementById('workflow-selector-menu');
    if (menu) {
      menu.classList.remove('show');
    }
  }

  /**
   * Handle document click to close dropdown
   */
  handleDocumentClick(event) {
    const dropdown = document.getElementById('workflow-selector-dropdown');
    if (dropdown && !dropdown.contains(event.target)) {
      this.closeDropdown();
    }
  }

  /**
   * Select a workflow
   * @param {string|null} workflowId - Workflow ID or null for all tasks
   */
  selectWorkflow(workflowId) {
    this.closeDropdown();
    this.state.setSelectedWorkflow(workflowId);
    this.updateUrl(workflowId);
  }

  /**
   * Handle workflow selection event
   */
  handleWorkflowSelected() {
    this.render();
    this.updateCanvasTitle();
  }

  /**
   * Update the canvas title to show workflow name when filtered
   */
  updateCanvasTitle() {
    const titleContainer = document.getElementById('canvas-workflow-title');
    const nameElement = document.getElementById('canvas-workflow-name');

    if (!titleContainer || !nameElement) return;

    const selectedWorkflow = this.state.getSelectedWorkflow();

    if (selectedWorkflow) {
      const workflowName = selectedWorkflow.name || selectedWorkflow.description || 'Workflow';
      nameElement.textContent = workflowName;
      titleContainer.style.display = 'inline';
    } else {
      titleContainer.style.display = 'none';
      nameElement.textContent = '';
    }
  }

  /**
   * Update URL with workflow parameter
   * @param {string|null} workflowId
   */
  updateUrl(workflowId) {
    const url = new URL(window.location.href);
    if (workflowId) {
      url.searchParams.set('workflow', workflowId);
    } else {
      url.searchParams.delete('workflow');
    }
    window.history.replaceState({}, document.title, url.toString());
  }

  /**
   * Update workflow list (called when tasks change)
   */
  updateWorkflowList() {
    // Check if currently selected workflow still exists
    if (this.state.selectedWorkflowId) {
      const workflows = this.state.getAvailableWorkflows();
      const exists = workflows.some(w => w.id === this.state.selectedWorkflowId);
      if (!exists) {
        // Workflow was deleted, switch to all tasks
        this.selectWorkflow(null);
        return;
      }
    }

    this.render();
  }

  /**
   * Handle create workflow action
   */
  handleCreateWorkflow() {
    this.closeDropdown();

    // Open the task creation modal with workflow flag
    // Check for different modal systems
    if (typeof showAddTaskModal === 'function') {
      showAddTaskModal();
    } else if (
      this.parent &&
      this.parent.forms &&
      typeof this.parent.forms.showCreateTaskForm === 'function'
    ) {
      this.parent.forms.showCreateTaskForm();
    } else {
      console.warn('Task creation modal not available');
    }
  }

  /**
   * Escape HTML to prevent XSS
   * @param {string} text
   * @returns {string}
   */
  escapeHtml(text) {
    if (!text) return '';
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }

  /**
   * Cleanup event listeners
   */
  destroy() {
    document.removeEventListener('click', this.handleDocumentClick);
    this.state.off(EVENT_TYPES.WORKFLOW_SELECTED, this.handleWorkflowSelected);
  }
}

// Export for module usage
export default AgentCanvasWorkflowSelector;

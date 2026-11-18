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
    this.updateWorkflowList = this.updateWorkflowList.bind(this);
    this.handleChoice = this.handleChoice.bind(this);
    this.handleKeydown = this.handleKeydown.bind(this);
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
    this.state.on(EVENT_TYPES.TASK_CREATED, this.updateWorkflowList);
    this.state.on(EVENT_TYPES.TASK_UPDATED, this.updateWorkflowList);
    this.state.on(EVENT_TYPES.DATA_LOADED, this.updateWorkflowList);
    this.container.addEventListener('click', this.handleChoice);
    this.container.addEventListener('keydown', this.handleKeydown);

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
    if (!hasWorkflows) this.dropdownOpen = false;

    // Determine display text
    let displayText = 'All Tickets';
    let displayCount = totalTasks;
    if (selectedWorkflow) {
      displayText = selectedWorkflow.name || selectedWorkflow.description || 'Workflow';
      const wf = workflows.find(w => w.id === selectedWorkflow.id);
      displayCount = wf ? wf.taskCount : 1;
    }

    const active = document.activeElement;
    const ownedFocus = this.container.contains(active);
    const focusedChoice = ownedFocus ? active?.dataset?.workflowChoice : undefined;
    this.container.innerHTML = `
      <div class="workflow-selector ${!hasWorkflows ? 'workflow-selector-disabled' : ''}" id="workflow-selector-dropdown">
        <button type="button" class="workflow-selector-trigger modern-btn modern-btn-secondary"
                aria-controls="workflow-selector-menu" aria-expanded="${this.dropdownOpen}"
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

        <div class="workflow-selector-menu ui-section ${this.dropdownOpen ? 'show' : ''}" id="workflow-selector-menu" role="group" aria-label="Canvas workflow filter" ${this.dropdownOpen ? '' : 'hidden'}>
          <button type="button" class="workflow-selector-item modern-btn modern-btn-secondary" data-workflow-choice="" aria-pressed="${!selectedWorkflow}">
            <span class="workflow-selector-item-icon" aria-hidden="true">
              ${!selectedWorkflow ? '✓' : ''}
            </span>
            <span class="workflow-selector-item-text">All Tickets</span>
            <span class="workflow-selector-item-count">${totalTasks}</span>
          </button>

          ${
            hasWorkflows
              ? `
            <div class="workflow-selector-divider"></div>

            ${workflows
              .map(
                wf => `
              <button type="button" class="workflow-selector-item modern-btn modern-btn-secondary" data-workflow-choice="${this.escapeHtml(wf.id).replace(/"/g, '&quot;')}" aria-pressed="${this.state.selectedWorkflowId === wf.id}">
                <span class="workflow-selector-item-icon" aria-hidden="true">
                  ${this.state.selectedWorkflowId === wf.id ? '✓' : ''}
                </span>
                <span class="workflow-selector-item-text">${this.escapeHtml(wf.name)}</span>
                <span class="workflow-selector-item-count">${wf.taskCount}</span>
              </button>
            `
              )
              .join('')}
          `
              : ''
          }

          <div class="workflow-selector-divider"></div>

          <button type="button" class="workflow-selector-item modern-btn modern-btn-secondary" data-workflow-create>
            <span class="workflow-selector-item-icon" aria-hidden="true">+</span>
            <span class="workflow-selector-item-text">Create Workflow</span>
          </button>
        </div>
      </div>
    `;
    if (ownedFocus) {
      const replacement = [...this.container.querySelectorAll('[data-workflow-choice]')].find(
        node => node.dataset.workflowChoice === focusedChoice
      );
      const trigger = this.container.querySelector('.workflow-selector-trigger');
      if (trigger?.disabled) this.parent?.canvas?.focus?.();
      else (replacement || trigger)?.focus();
    }
  }

  handleChoice(event) {
    const button = event.target.closest('button');
    if (!button || !this.container.contains(button) || button.disabled) return;
    if (button.classList.contains('workflow-selector-trigger')) this.toggleDropdown();
    else if (button.hasAttribute('data-workflow-choice'))
      this.selectWorkflow(button.dataset.workflowChoice || null);
    else if (button.hasAttribute('data-workflow-create')) this.handleCreateWorkflow();
  }

  handleKeydown(event) {
    if (event.key !== 'Escape' || !this.dropdownOpen) return;
    event.preventDefault();
    event.stopPropagation();
    this.closeDropdown(true);
  }

  /**
   * Toggle dropdown visibility
   */
  toggleDropdown() {
    this.dropdownOpen = !this.dropdownOpen;
    const menu = document.getElementById('workflow-selector-menu');
    if (menu) {
      menu.classList.toggle('show', this.dropdownOpen);
      menu.hidden = !this.dropdownOpen;
    }
    this.container
      ?.querySelector('.workflow-selector-trigger')
      ?.setAttribute('aria-expanded', String(this.dropdownOpen));
  }

  /**
   * Close dropdown
   */
  closeDropdown(restoreFocus = false) {
    this.dropdownOpen = false;
    const menu = document.getElementById('workflow-selector-menu');
    if (menu) {
      menu.classList.remove('show');
      menu.hidden = true;
    }
    const trigger = this.container?.querySelector('.workflow-selector-trigger');
    trigger?.setAttribute('aria-expanded', 'false');
    if (restoreFocus) trigger?.focus();
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
    this.container?.querySelector('.workflow-selector-trigger')?.focus();
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
      showAddTaskModal(this.container?.querySelector('.workflow-selector-trigger'));
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
    for (const event of [
      EVENT_TYPES.TASK_CREATED,
      EVENT_TYPES.TASK_UPDATED,
      EVENT_TYPES.DATA_LOADED
    ])
      this.state.off(event, this.updateWorkflowList);
    this.container?.removeEventListener('click', this.handleChoice);
    this.container?.removeEventListener('keydown', this.handleKeydown);
  }
}

// Export for module usage
export default AgentCanvasWorkflowSelector;

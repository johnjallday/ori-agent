/**
 * WorkflowPanel - Collapsible sidebar panel for workflow templates
 *
 * Provides a UI for browsing, searching, and instantiating saved workflows
 * in a workspace canvas. Workflows can be custom (user-created) or built-in.
 *
 * @class WorkflowPanel
 * @example
 * const panel = new WorkflowPanel('workflow-panel-container', 'workspace-123');
 * await panel.init();
 */

class WorkflowPanel {
  /**
   * Create a new WorkflowPanel instance
   * @param {string} containerId - DOM element ID where the panel will be rendered
   * @param {string} workspaceId - ID of the workspace this panel belongs to
   */
  constructor(containerId, workspaceId) {
    /** @type {string} */
    this.containerId = containerId;
    /** @type {string} */
    this.workspaceId = workspaceId;
    /** @type {Array<Object>} */
    this.workflows = [];
    /** @type {Array<Object>} */
    this.filteredWorkflows = [];
    /** @type {boolean} */
    this.isLoading = false;
    /** @type {string} */
    this.searchQuery = '';
    /** @type {string} */
    this.categoryFilter = '';
    /** @type {boolean} */
    this.isCollapsed = false;
    this.loadError = false;
    this.handleAction = this.handleAction.bind(this);
  }

  /**
   * Initialize the panel - renders UI and loads workflows
   * @returns {Promise<void>}
   */
  async init() {
    this.render();
    await this.loadWorkflows();
  }

  /**
   * Load workflows from the API and check agent availability
   * @returns {Promise<void>}
   */
  async loadWorkflows() {
    if (this.isLoading) return;
    this.isLoading = true;
    this.loadError = false;
    this.renderWorkflowList();

    try {
      const response = await fetch('/api/workflows');
      if (!response.ok) {
        throw new Error('Failed to load workflows');
      }

      const data = await response.json();
      if (!Array.isArray(data.workflows)) throw new Error('Invalid workflow list');
      this.workflows = data.workflows;

      // Check agent availability for each workflow
      await this.checkAllAgentAvailability();

      this.applyFilters();
    } catch (error) {
      console.error('Error loading workflows:', error);
      this.loadError = true;
      this.workflows = [];
      this.filteredWorkflows = [];
    } finally {
      this.isLoading = false;
      this.renderWorkflowList();
    }
  }

  /**
   * Check agent availability for all workflows against the current workspace
   * Updates each workflow's missingAgents and allAgentsAvailable properties
   * @returns {Promise<void>}
   * @private
   */
  async checkAllAgentAvailability() {
    for (const workflow of this.workflows) {
      try {
        const response = await fetch(`/api/workflows/${workflow.id}/check-agents`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ workspace_id: this.workspaceId })
        });

        if (response.ok) {
          const result = await response.json();
          workflow.missingAgents = result.missing_agents || [];
          workflow.agentCheckUnavailable = false;
          workflow.allAgentsAvailable = workflow.missingAgents.length === 0;
        } else {
          workflow.missingAgents = [];
          workflow.agentCheckUnavailable = true;
          workflow.allAgentsAvailable = false;
        }
      } catch (error) {
        console.error(`Error checking agents for workflow ${workflow.id}:`, error);
        workflow.missingAgents = [];
        workflow.agentCheckUnavailable = true;
        workflow.allAgentsAvailable = false;
      }
    }
  }

  /**
   * Apply search and category filters
   */
  applyFilters() {
    this.filteredWorkflows = this.workflows.filter(workflow => {
      const matchesSearch =
        !this.searchQuery ||
        workflow.name.toLowerCase().includes(this.searchQuery.toLowerCase()) ||
        (workflow.description &&
          workflow.description.toLowerCase().includes(this.searchQuery.toLowerCase()));

      const matchesCategory = !this.categoryFilter || workflow.category === this.categoryFilter;

      return matchesSearch && matchesCategory;
    });

    this.renderWorkflowList();
  }

  /**
   * Get unique categories from all loaded workflows
   * @returns {string[]} Sorted array of category names
   */
  getCategories() {
    const categories = new Set();
    this.workflows.forEach(w => {
      if (w.category) categories.add(w.category);
    });
    return Array.from(categories).sort();
  }

  /**
   * Render the panel container
   */
  render() {
    const container = document.getElementById(this.containerId);
    if (!container) return;

    container.innerHTML = `
      <div class="modern-card p-3 mb-3" id="workflow-panel">
        <div class="d-flex justify-content-between align-items-center mb-2">
          <h6 class="mb-0"><button type="button" id="workflow-panel-toggle" class="modern-btn modern-btn-secondary modern-btn-sm" data-workflow-action="collapse" aria-controls="workflow-panel-content" aria-expanded="true">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1" style="vertical-align: text-bottom;">
              <path d="M4,2H20A2,2 0 0,1 22,4V20A2,2 0 0,1 20,22H4A2,2 0 0,1 2,20V4A2,2 0 0,1 4,2M4,4V8H9V4H4M11,4V8H20V4H11M4,10V14H9V10H4M11,10V14H20V10H11M4,16V20H9V16H4M11,16V20H20V16H11Z"/>
            </svg>
            Workflows
            <svg id="workflow-panel-chevron" width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="ms-1" style="vertical-align: text-bottom; transition: transform 0.2s;">
              <path d="M7.41,8.58L12,13.17L16.59,8.58L18,10L12,16L6,10L7.41,8.58Z"/>
            </svg>
          </button></h6>
          <button type="button" id="workflow-refresh" class="modern-btn modern-btn-secondary modern-btn-icon" data-workflow-action="refresh" title="Refresh workflows" aria-label="Refresh workflows">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
              <path d="M17.65,6.35C16.2,4.9 14.21,4 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20C15.73,20 18.84,17.45 19.73,14H17.65C16.83,16.33 14.61,18 12,18A6,6 0 0,1 6,12A6,6 0 0,1 12,6C13.66,6 15.14,6.69 16.22,7.78L13,11H20V4L17.65,6.35Z"/>
            </svg>
          </button>
        </div>

        <div id="workflow-panel-content">
          <!-- Search and filter -->
          <div class="mb-2">
            <input type="text" class="form-control form-control-sm"
                   placeholder="Search workflows..." aria-label="Search workflows"
                   id="workflow-search"
                   oninput="workflowPanel.onSearch(this.value)">
          </div>

          <div class="mb-2">
            <select class="form-select form-select-sm" id="workflow-category-filter" aria-label="Workflow category" onchange="workflowPanel.onCategoryFilter(this.value)">
              <option value="">All Categories</option>
            </select>
          </div>

          <!-- Workflow list -->
          <div id="workflow-list" tabindex="0" role="region" aria-label="Saved workflows" style="max-height: 24rem; overflow-y: auto;">
            <div class="text-center py-3">
              <div class="spinner-border spinner-border-sm text-primary" role="status">
                <span class="visually-hidden">Loading...</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    `;
    container.removeEventListener('click', this.handleAction);
    container.addEventListener('click', this.handleAction);
  }

  handleAction(event) {
    const button = event.target.closest('[data-workflow-action]');
    if (!button || !document.getElementById(this.containerId)?.contains(button) || button.disabled)
      return;
    switch (button.dataset.workflowAction) {
      case 'collapse':
        this.toggleCollapse();
        break;
      case 'refresh':
        void this.loadWorkflows();
        break;
      case 'delete':
        void this.deleteWorkflow(button.dataset.workflowId);
        break;
      case 'instantiate':
        void this.instantiateWorkflow(button.dataset.workflowId);
        break;
    }
  }

  /**
   * Toggle panel collapse
   */
  toggleCollapse() {
    this.isCollapsed = !this.isCollapsed;
    const content = document.getElementById('workflow-panel-content');
    const chevron = document.getElementById('workflow-panel-chevron');
    document
      .getElementById('workflow-panel-toggle')
      ?.setAttribute('aria-expanded', String(!this.isCollapsed));

    if (content) {
      content.style.display = this.isCollapsed ? 'none' : 'block';
    }
    if (chevron) {
      chevron.style.transform = this.isCollapsed ? 'rotate(-90deg)' : 'rotate(0deg)';
    }
  }

  /**
   * Handle search input change
   * @param {string} query - Search query string
   */
  onSearch(query) {
    this.searchQuery = query;
    this.applyFilters();
  }

  /**
   * Handle category filter change
   * @param {string} category - Category to filter by, or empty string for all
   */
  onCategoryFilter(category) {
    this.categoryFilter = category;
    this.applyFilters();
  }

  /**
   * Render the workflow list
   */
  renderWorkflowList() {
    const listContainer = document.getElementById('workflow-list');
    const categorySelect = document.getElementById('workflow-category-filter');

    if (!listContainer) return;
    const active = document.activeElement;
    const ownedFocus = listContainer.contains(active);
    const focusId = active?.dataset?.workflowId,
      focusAction = active?.dataset?.workflowAction;
    const restoreFocus = () => {
      if (!ownedFocus) return;
      const replacement = [...listContainer.querySelectorAll('[data-workflow-action]')].find(
        node => node.dataset.workflowId === focusId && node.dataset.workflowAction === focusAction
      );
      const refresh = document.getElementById('workflow-refresh');
      const fallback = refresh?.disabled
        ? document.getElementById('workflow-panel-toggle')
        : refresh;
      (replacement || fallback)?.focus();
    };
    listContainer.setAttribute('aria-busy', String(this.isLoading));
    const refresh = document.getElementById('workflow-refresh');
    refresh?.setAttribute('aria-busy', String(this.isLoading));

    // Update category dropdown
    if (categorySelect) {
      const categories = this.getCategories();
      const currentValue = this.categoryFilter;
      // A failed/empty refresh must not silently change a person's filter.
      if (currentValue && !categories.includes(currentValue)) categories.push(currentValue);
      categorySelect.innerHTML =
        `<option value="">All Categories</option>` +
        categories
          .map(
            cat =>
              `<option value="${this.escapeAttribute(cat)}" ${cat === currentValue ? 'selected' : ''}>${this.escapeHtml(cat)}</option>`
          )
          .join('');
    }

    if (this.isLoading) {
      listContainer.innerHTML = `
        <div class="text-center py-3">
          <div class="spinner-border spinner-border-sm text-primary" role="status">
            <span class="visually-hidden">Loading...</span>
          </div>
        </div>
      `;
      restoreFocus();
      return;
    }

    if (this.loadError) {
      listContainer.innerHTML =
        '<p class="ui-status-danger" role="alert">Unable to load saved workflows. Refresh to retry.</p>';
      restoreFocus();
      return;
    }

    if (this.filteredWorkflows.length === 0) {
      listContainer.innerHTML = `
        <div class="text-center py-3" style="color: var(--text-muted); font-size: 0.875rem;">
          ${this.workflows.length === 0 ? 'No workflows saved yet' : 'No matching workflows'}
        </div>
      `;
      restoreFocus();
      return;
    }

    listContainer.innerHTML = this.filteredWorkflows
      .map(workflow => this.renderWorkflowItem(workflow))
      .join('');
    restoreFocus();
  }

  /**
   * Render a single workflow item as HTML
   * @param {Object} workflow - Workflow object to render
   * @returns {string} HTML string for the workflow item
   * @private
   */
  renderWorkflowItem(workflow) {
    const isCustom = workflow.source === 'custom';
    const nodeCount = workflow.nodes ? workflow.nodes.length : 0;
    const hasWarning = workflow.missingAgents && workflow.missingAgents.length > 0;

    const warningTooltip = workflow.agentCheckUnavailable
      ? 'Agent availability could not be checked'
      : hasWarning
        ? `Missing agents: ${workflow.missingAgents.join(', ')}`
        : '';

    return `
      <div class="workflow-item" data-workflow-row="${this.escapeAttribute(workflow.id)}">
        <div class="d-flex justify-content-between align-items-start mb-1">
          <div class="d-flex align-items-center gap-1" style="flex: 1; min-width: 0;">
            <strong class="workflow-item-name">
              ${this.escapeHtml(workflow.name)}
            </strong>
          </div>
          ${
            isCustom
              ? `
            <button type="button" class="modern-btn modern-btn-danger modern-btn-icon ms-1"
                    data-workflow-action="delete" data-workflow-id="${this.escapeAttribute(workflow.id)}"
                    title="Delete workflow" aria-label="Delete workflow">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
              </svg>
            </button>
          `
              : ''
          }
        </div>

        ${
          workflow.description
            ? `
          <p class="ui-field-help mb-1 workflow-item-description">
            ${this.escapeHtml(workflow.description)}
          </p>
        `
            : ''
        }

        ${warningTooltip ? `<p class="ui-status-warning ui-field-help">${this.escapeHtml(warningTooltip)}</p>` : ''}
        <div class="d-flex flex-wrap justify-content-between align-items-center gap-2 mt-2">
          <div class="d-flex gap-1 flex-wrap">
            ${workflow.category ? `<span class="badge badge-info">${this.escapeHtml(workflow.category)}</span>` : ''}
            <span class="badge badge-secondary">${nodeCount} node${nodeCount !== 1 ? 's' : ''}</span>
            ${isCustom ? `<span class="badge badge-success">Custom</span>` : ''}
          </div>

          <button type="button" class="modern-btn modern-btn-primary modern-btn-sm"
                  data-workflow-action="instantiate" data-workflow-id="${this.escapeAttribute(workflow.id)}"
                  ${warningTooltip ? `title="${this.escapeAttribute(warningTooltip)}"` : ''}>
            <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" class="me-1">
              <path d="M19,13H13V19H11V13H5V11H11V5H13V11H19V13Z"/>
            </svg>
            Add
          </button>
        </div>
      </div>
    `;
  }

  /**
   * Instantiate a workflow on the canvas by creating all its nodes
   * @param {string} workflowId - ID of the workflow to instantiate
   * @returns {Promise<void>}
   */
  async instantiateWorkflow(workflowId) {
    try {
      // Fetch workflow details
      const response = await fetch(`/api/workflows/${workflowId}`);
      if (!response.ok) {
        throw new Error('Failed to fetch workflow');
      }

      const workflow = await response.json();

      // Check for missing agents
      if (workflow.missingAgents && workflow.missingAgents.length > 0) {
        const proceed = confirm(
          `This workflow requires agents that are not in this workspace:\n${workflow.missingAgents.join(', ')}\n\nDo you want to proceed anyway? (Nodes will be created but may not work correctly)`
        );
        if (!proceed) return;
      }

      // Call the canvas instantiation method
      if (window.agentCanvas && typeof window.agentCanvas.instantiateWorkflow === 'function') {
        await window.agentCanvas.instantiateWorkflow(workflow);
      } else if (typeof window.instantiateWorkflow === 'function') {
        await window.instantiateWorkflow(workflow);
      } else {
        // Fallback: create nodes manually
        await this.createWorkflowNodes(workflow);
      }

      this.showNotification(`Workflow "${workflow.name}" added to canvas`, 'success');
    } catch (error) {
      console.error('Error instantiating workflow:', error);
      this.showNotification('Failed to add workflow: ' + error.message, 'error');
    }
  }

  /**
   * Create workflow nodes on the canvas (fallback method)
   * @param {Object} workflow - Workflow object containing nodes to create
   * @returns {Promise<Array>} Array of created nodes
   * @private
   */
  async createWorkflowNodes(workflow) {
    // Get viewport center for positioning
    const canvas = document.getElementById('agent-canvas');
    const centerX = canvas ? canvas.width / 2 : 400;
    const centerY = canvas ? canvas.height / 2 : 300;

    const createdNodes = [];

    for (const node of workflow.nodes) {
      try {
        const x = centerX + (node.relative_x || 0);
        const y = centerY + (node.relative_y || 0);

        switch (node.type) {
          case 'task':
            await this.createTaskNode(node, x, y);
            break;
          case 'agent':
            await this.createAgentNode(node, x, y);
            break;
          case 'store':
            await this.createStoreNode(node, x, y);
            break;
          case 'attachment':
            await this.createAttachmentNode(node, x, y);
            break;
        }

        createdNodes.push(node);
      } catch (error) {
        console.error(`Error creating ${node.type} node:`, error);
      }
    }

    // Reload canvas to show new nodes
    if (window.agentCanvas && typeof window.agentCanvas.loadWorkspace === 'function') {
      await window.agentCanvas.loadWorkspace();
    } else {
      window.location.reload();
    }

    return createdNodes;
  }

  /**
   * Create a task node via API
   * @param {Object} node - Node definition from workflow
   * @param {number} x - X position on canvas
   * @param {number} y - Y position on canvas
   * @returns {Promise<Object>} Created task object
   * @private
   */
  async createTaskNode(node, x, y) {
    const config = node.config || {};
    return window.WorkspaceHubTickets.api.createStep(this.workspaceId, {
      description: config.description || 'New step',
      to: config.to || 'unassigned',
      from: config.from || 'user',
      priority: config.priority || 0,
      x: x,
      y: y
    });
  }

  /**
   * Create an agent node via API
   * @param {Object} node - Node definition from workflow
   * @param {number} x - X position on canvas
   * @param {number} y - Y position on canvas
   * @returns {Promise<Object>} Created agent object
   * @private
   */
  async createAgentNode(node, x, y) {
    const config = node.config || {};
    const response = await fetch('/api/orchestration/workspace/agents', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        workspace_id: this.workspaceId,
        agent_name: config.name || node.id
      })
    });

    if (!response.ok) {
      throw new Error('Failed to add agent');
    }

    return response.json();
  }

  /**
   * Create a store node via API
   * @param {Object} node - Node definition from workflow
   * @param {number} x - X position on canvas
   * @param {number} y - Y position on canvas
   * @returns {Promise<Object>} Created store object
   * @private
   */
  async createStoreNode(node, x, y) {
    const config = node.config || {};
    const response = await fetch(`/api/workspaces/${this.workspaceId}/stores`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: config.name || 'New Store',
        store_type: config.store_type || 'file',
        file_path: config.file_path || '',
        x: x,
        y: y
      })
    });

    if (!response.ok) {
      throw new Error('Failed to create store');
    }

    return response.json();
  }

  /**
   * Create an attachment node via API
   * @param {Object} node - Node definition from workflow
   * @param {number} x - X position on canvas
   * @param {number} y - Y position on canvas
   * @returns {Promise<Object>} Created attachment object
   * @private
   */
  async createAttachmentNode(node, x, y) {
    const config = node.config || {};
    const response = await fetch(`/api/workspaces/${this.workspaceId}/attachments`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        title: config.title || 'New Attachment',
        type: config.type || 'note',
        body: config.body || '',
        link_url: config.link_url || '',
        x: x,
        y: y
      })
    });

    if (!response.ok) {
      throw new Error('Failed to create attachment');
    }

    return response.json();
  }

  /**
   * Delete a custom workflow after confirmation
   * @param {string} workflowId - ID of the workflow to delete
   * @returns {Promise<void>}
   */
  async deleteWorkflow(workflowId) {
    const workflow = this.workflows.find(w => w.id === workflowId);
    if (!workflow) return;

    if (workflow.source !== 'custom') {
      this.showNotification('Cannot delete built-in workflows', 'warning');
      return;
    }

    const confirmed = confirm(
      `Delete workflow "${workflow.name}"?\n\nThis action cannot be undone.`
    );
    if (!confirmed) return;

    try {
      const response = await fetch(`/api/workflows/${workflowId}`, {
        method: 'DELETE'
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(error);
      }

      this.showNotification('Workflow deleted', 'success');
      await this.loadWorkflows();
    } catch (error) {
      console.error('Error deleting workflow:', error);
      this.showNotification('Failed to delete workflow: ' + error.message, 'error');
    }
  }

  /**
   * Show a notification message to the user
   * @param {string} message - Message to display
   * @param {string} [type='info'] - Notification type: 'success', 'error', 'warning', 'info'
   */
  showNotification(message, type = 'info') {
    if (window.agentCanvas && window.agentCanvas.notifications) {
      window.agentCanvas.notifications.showNotification(message, type);
    } else {
      // Fallback to alert for errors
      if (type === 'error') {
        alert(message);
      }
    }
  }

  /**
   * Escape HTML special characters to prevent XSS attacks
   * @param {string} text - Text to escape
   * @returns {string} Escaped HTML string
   * @private
   */
  escapeAttribute(text) {
    return this.escapeHtml(text).replace(/"/g, '&quot;');
  }

  escapeHtml(text) {
    if (!text) return '';
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }
}

// Export for module usage
if (typeof module !== 'undefined' && module.exports) {
  module.exports = WorkflowPanel;
}

// Make available globally
window.WorkflowPanel = WorkflowPanel;

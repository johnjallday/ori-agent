/**
 * Dashboard Agents Module
 * Handles agent list rendering and agent management
 */

export class DashboardAgents {
  constructor(parent) {
    this.parent = parent;
  }

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

  showAddAgentForm() {
    const form = document.getElementById('add-agent-form');
    if (form) {
      form.style.display = 'block';
      this.populateAvailableAgents();
    }
  }

  hideAddAgentForm() {
    const form = document.getElementById('add-agent-form');
    if (form) {
      form.style.display = 'none';
      document.getElementById('agent-form').reset();
    }
  }

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

}

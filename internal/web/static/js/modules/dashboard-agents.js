/**
 * Dashboard Agents Module
 * Handles agent list rendering and agent management
 */

export class DashboardAgents {
  constructor(parent) {
    this.parent = parent;
    this.evolutionByAgent = {};
    this.evolutionLoading = new Set();
  }

  renderAgentList() {
    const agents = this.parent?.data?.agents || [];

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
                ${this.renderEvolution(agent)}
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
      const currentAgents = this.parent?.data?.agents || [];

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
      this.parent.showToast('Error', '❌ Failed to fetch available agents', 'error');
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
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          workspace_id: this.parent.workspaceId,
          agent_name: agentName
        })
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(error || 'Failed to add agent');
      }

      await response.json();

      // Hide form and reload workspace data
      this.hideAddAgentForm();
      await this.parent.loadWorkspaceData();

      // Update agent list
      const agentListContainer = document.getElementById('agent-list-container');
      if (agentListContainer) {
        agentListContainer.innerHTML = this.renderAgentList();
      }

      // Show success notification
      this.parent.showToast('Agent Added', `✅ ${agentName} added to workspace`, 'success');
    } catch (error) {
      console.error('Error adding agent:', error);
      this.parent.showToast('Add Failed', '❌ Failed to add agent: ' + error.message, 'error');
    }
  }

  async removeAgent(agentName) {
    if (!confirm(`Remove ${agentName} from this workspace?`)) {
      return;
    }

    try {
      const response = await fetch(`/api/orchestration/workspace/agents?workspace_id=${this.parent.workspaceId}&agent_name=${encodeURIComponent(agentName)}`, {
        method: 'DELETE'
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(error || 'Failed to remove agent');
      }

      // Reload workspace data
      await this.parent.loadWorkspaceData();

      // Update agent list
      const agentListContainer = document.getElementById('agent-list-container');
      if (agentListContainer) {
        agentListContainer.innerHTML = this.renderAgentList();
      }

      // Show success notification
      this.parent.showToast('Agent Removed', `✅ ${agentName} removed from workspace`, 'success');
    } catch (error) {
      console.error('Error removing agent:', error);
      this.parent.showToast('Remove Failed', '❌ Failed to remove agent: ' + error.message, 'error');
    }
  }

  renderEvolution(agentName) {
    if (!window.oriFeatures?.evolutionEnabled) {
      return '';
    }

    const evolution = this.evolutionByAgent[agentName];
    if (!evolution && !this.evolutionLoading.has(agentName)) {
      this.fetchEvolution(agentName);
      return '<div class="text-muted small mt-1">Loading progression...</div>';
    }
    if (!evolution) {
      return '';
    }

    const stage = this.escapeHtml(this.toTitleCase(evolution.stage || 'spark'));
    const path = evolution.path ? this.escapeHtml(this.toTitleCase(evolution.path)) : '';
    const level = Number.isFinite(Number(evolution.level)) ? Math.max(0, Math.floor(Number(evolution.level))) : 0;
    const experience = Number.isFinite(Number(evolution.experience)) ? Math.max(0, Math.floor(Number(evolution.experience))) : 0;
    const progressPercent = Math.min(100, Math.max(0, Math.round(experience % 100)));

    return `
      <div class="mt-1">
        <div class="d-flex align-items-center gap-1 flex-wrap">
          <span class="badge" style="background: var(--primary-color-light); color: var(--primary-color); font-size: 0.62rem;">${stage}</span>
          ${path ? `<span class="badge" style="background: var(--bg-tertiary); color: var(--text-secondary); font-size: 0.62rem;">${path}</span>` : ''}
          <span style="color: var(--text-secondary); font-size: 0.68rem;">Lv ${level}</span>
        </div>
        <div class="progress mt-1" style="height: 4px; background: var(--bg-tertiary);">
          <div class="progress-bar" role="progressbar" style="width: ${progressPercent}%; background: var(--primary-color);" aria-valuenow="${progressPercent}" aria-valuemin="0" aria-valuemax="100"></div>
        </div>
      </div>
    `;
  }

  async fetchEvolution(agentName) {
    if (!window.oriFeatures?.evolutionEnabled || this.evolutionLoading.has(agentName)) {
      return;
    }

    this.evolutionLoading.add(agentName);
    try {
      const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}/evolution`);
      if (!response.ok) {
        return;
      }
      const data = await response.json();
      if (data?.evolution) {
        this.evolutionByAgent[agentName] = data.evolution;
        const container = document.getElementById('agent-list-container');
        if (container) {
          container.innerHTML = this.renderAgentList();
        }
      }
    } catch (error) {
      console.debug('Failed to load agent evolution', { agent: agentName, error });
    } finally {
      this.evolutionLoading.delete(agentName);
    }
  }

  toTitleCase(value) {
    return String(value || '')
      .split(/[\s_-]+/)
      .filter(Boolean)
      .map(word => word.charAt(0).toUpperCase() + word.slice(1).toLowerCase())
      .join(' ');
  }

  escapeHtml(text) {
    return this.parent.escapeHtml(text);
  }
}

/**
 * Agents management system
 */

'use strict';

class AgentsManager {
  constructor() {
    this.currentAgent = '';
    this.init();
  }

  /**
   * Initialize agents manager and set up event listeners
   */
  init() {
    this.setupEventListeners();
  }

  /**
   * Set up event listeners for agent management
   */
  setupEventListeners() {
    const createAgentBtn = document.getElementById('createAgent');
    if (createAgentBtn) {
      createAgentBtn.onclick = async () => {
        await this.createAgent();
      };
    }
  }

  /**
   * Refresh agents list and update UI
   */
  async refreshAgents() {
    try {
      const res = await fetch('/api/agents');
      const data = await res.json();
      this.currentAgent = data.current;
      
      // Fetch current model for navbar display
      const settingsRes = await fetch('/api/settings');
      const settings = await settingsRes.json();
      
      // Display current agent and model in navbar
      this.updateNavbarDisplay(this.currentAgent, settings.model);
      
      // Update agents list in sidebar
      this.updateAgentsList(data.agents);
      
    } catch (error) {
      Logger?.error && Logger.error('Failed to refresh agents:', error);
    }
  }

  /**
   * Update navbar display with current agent and model
   * @param {string} agentName - Current agent name
   * @param {string} model - Current model name
   */
  updateNavbarDisplay(agentName, model) {
    const disp = document.getElementById('currentAgentDisplay');
    if (disp) {
      const textSpan = disp.querySelector('.fw-medium');
      if (textSpan) {
        textSpan.textContent = `${agentName} • ${model}`;
      }
    }
  }

  /**
   * Update the agents list in the sidebar
   * @param {Array} agents - List of agent names
   */
  updateAgentsList(agents) {
    const ul = document.getElementById('agentsList');
    if (!ul) return;
    
    ul.innerHTML = '';
    agents.forEach(agentName => {
      const listItem = this.createAgentListItem(agentName);
      ul.appendChild(listItem);
    });
  }

  /**
   * Create a list item for an agent
   * @param {string} agentName - Name of the agent
   * @returns {HTMLElement} Agent list item element
   */
  createAgentListItem(agentName) {
    const li = document.createElement('div');
    li.className = 'modern-list-item d-flex justify-content-between align-items-center';
    
    const nameContainer = document.createElement('div');
    nameContainer.className = 'd-flex align-items-center gap-2';
    
    const statusIndicator = document.createElement('span');
    statusIndicator.className = 'status-indicator ' + (agentName === this.currentAgent ? 'status-online' : 'status-offline');
    
    const nameSpan = document.createElement('span');
    nameSpan.textContent = agentName;
    nameSpan.style.fontWeight = agentName === this.currentAgent ? '600' : '500';
    nameSpan.style.color = agentName === this.currentAgent ? 'var(--primary-color)' : 'var(--text-primary)';
    
    nameContainer.appendChild(statusIndicator);
    nameContainer.appendChild(nameSpan);
    
    if (agentName === this.currentAgent) {
      const currentBadge = document.createElement('span');
      currentBadge.className = 'modern-badge badge-primary';
      currentBadge.textContent = 'Current';
      nameContainer.appendChild(currentBadge);
    }
    
    const btnContainer = this.createAgentButtons(agentName);
    
    li.appendChild(nameContainer);
    li.appendChild(btnContainer);
    
    return li;
  }

  /**
   * Create buttons for agent actions
   * @param {string} agentName - Name of the agent
   * @returns {HTMLElement} Button container element
   */
  createAgentButtons(agentName) {
    const btnContainer = document.createElement('div');
    btnContainer.className = 'd-flex gap-2';
    
    // Switch button (only for non-current agents)
    if (agentName !== this.currentAgent) {
      const switchBtn = document.createElement('button');
      switchBtn.className = 'modern-btn modern-btn-secondary';
      switchBtn.innerHTML = `
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
          <path d="M9 12L15 6L15 18L9 12Z" fill="currentColor"/>
        </svg>
        Switch
      `;
      switchBtn.onclick = async () => {
        await this.switchAgent(agentName);
      };
      btnContainer.appendChild(switchBtn);
    }
    
    // Delete button
    const deleteBtn = document.createElement('button');
    deleteBtn.className = 'modern-btn modern-btn-danger';
    deleteBtn.innerHTML = `
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
        <path d="M3 6H5H21M8 6V4C8 3.46957 8.21071 2.96086 8.58579 2.58579C8.96086 2.21071 9.46957 2 10 2H14C14.5304 2 15.0391 2.21071 15.4142 2.58579C15.7893 2.96086 16 3.46957 16 4V6M19 6V20C19 20.5304 18.7893 21.0391 18.4142 21.4142C18.0391 21.7893 17.5304 22 17 22H7C6.46957 22 5.96086 21.7893 5.58579 21.4142C5.21071 21.0391 5 20.5304 5 20V6H19Z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
      Delete
    `;
    deleteBtn.disabled = agentName === this.currentAgent;
    deleteBtn.style.opacity = agentName === this.currentAgent ? '0.5' : '1';
    deleteBtn.onclick = async () => {
      await this.deleteAgent(agentName);
    };
    btnContainer.appendChild(deleteBtn);
    
    return btnContainer;
  }

  /**
   * Switch to a different agent
   * @param {string} agentName - Name of agent to switch to
   */
  async switchAgent(agentName) {
    try {
      await fetch(`/api/agents?name=${encodeURIComponent(agentName)}`, { method: 'PUT' });
      if (window.app && typeof window.app.init === 'function') {
        await window.app.init();
      } else {
        // Fallback if app init is not available
        await this.refreshAgents();
      }
    } catch (error) {
      Logger?.error && Logger.error('Failed to switch agent:', error);
    }
  }

  /**
   * Delete an agent
   * @param {string} agentName - Name of agent to delete
   */
  async deleteAgent(agentName) {
    if (!confirm(`Are you sure you want to delete agent "${agentName}"? This action cannot be undone.`)) {
      return;
    }
    
    try {
      await fetch(`/api/agents?name=${encodeURIComponent(agentName)}`, { method: 'DELETE' });
      if (window.app && typeof window.app.init === 'function') {
        await window.app.init();
      } else {
        // Fallback if app init is not available
        await this.refreshAgents();
      }
    } catch (error) {
      Logger?.error && Logger.error('Failed to delete agent:', error);
    }
  }

  /**
   * Create a new agent
   */
  async createAgent() {
    const nameInput = document.getElementById('agentName');
    const name = nameInput?.value?.trim();
    
    if (!name) {
      alert('Enter agent name');
      return;
    }
    
    try {
      await fetch('/api/agents', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name })
      });
      
      // Clear input
      if (nameInput) {
        nameInput.value = '';
      }
      
      if (window.app && typeof window.app.init === 'function') {
        await window.app.init();
      } else {
        // Fallback if app init is not available
        await this.refreshAgents();
      }
    } catch (error) {
      Logger?.error && Logger.error('Failed to create agent:', error);
      alert('Failed to create agent');
    }
  }

  /**
   * Get current agent name
   * @returns {string} Current agent name
   */
  getCurrentAgent() {
    return this.currentAgent;
  }
}

// Export for module systems
if (typeof module !== 'undefined' && module.exports) {
  module.exports = AgentsManager;
}

// Global instance will be created by main app
window.AgentsManager = AgentsManager;
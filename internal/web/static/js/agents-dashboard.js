// Agents Dashboard JavaScript

let dashboardAllAgents = [];
let dashboardFilteredAgents = [];
let dashboardCurrentView = 'table';
const dashboardCurrentSort = 'name';
const dashboardSortOrder = 'asc';
let dashboardRefreshInterval = null;
let dashboardActiveAgent = ''; // Currently active/loaded agent

// Initialize dashboard
function initializeDashboard() {
  console.log('🚀 Initializing agents dashboard...');
  loadAgents();
  setupAutoRefresh();
}

// Run initialization when DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initializeDashboard);
} else {
  // DOM already loaded, run immediately
  initializeDashboard();
}

// Setup auto-refresh for statistics
function setupAutoRefresh() {
  // Refresh stats every 60 seconds
  dashboardRefreshInterval = setInterval(() => {
    // Only refresh if page is visible
    if (!document.hidden) {
      updateStatistics();
    }
  }, 60000); // 60 seconds

  // Stop refreshing when page is hidden
  document.addEventListener('visibilitychange', () => {
    if (document.hidden && dashboardRefreshInterval) {
      clearInterval(dashboardRefreshInterval);
      dashboardRefreshInterval = null;
    } else if (!document.hidden && !dashboardRefreshInterval) {
      // Restart when page becomes visible
      updateStatistics(); // Immediate refresh
      dashboardRefreshInterval = setInterval(() => {
        if (!document.hidden) {
          updateStatistics();
        }
      }, 60000);
    }
  });

  // Cleanup on page unload
  window.addEventListener('beforeunload', () => {
    if (dashboardRefreshInterval) {
      clearInterval(dashboardRefreshInterval);
    }
  });
}

// Load agents from API
async function loadAgents() {
  try {
    console.log('🔄 Loading agents from API...');
    showLoading(true);
    const response = await fetch('/api/agents');
    console.log('📡 Response received:', response.status);

    if (!response.ok) {
      throw new Error('Failed to load agents');
    }

    const data = await response.json();
    console.log('📊 Data received:', data);
    dashboardAllAgents = data.agents || [];
    dashboardFilteredAgents = [...dashboardAllAgents];
    dashboardActiveAgent = data.current || ''; // Track the currently active agent
    console.log('✅ Loaded', dashboardAllAgents.length, 'agents, active:', dashboardActiveAgent);

    updateStatistics();
    renderDashboardAgents();
    showLoading(false);
    console.log('✓ Rendering complete');

  } catch (error) {
    console.error('❌ Error loading agents:', error);
    showLoading(false);
    showError('Failed to load agents');
  }
}

// Update dashboard statistics from API
async function updateStatistics() {
  try {
    const response = await fetch('/api/agents/dashboard/stats');

    if (!response.ok) {
      // Fallback to client-side calculation
      const stats = calculateStatistics(dashboardAllAgents);
      displayStatistics(stats.total, stats.cost);
      return;
    }

    const stats = await response.json();
    displayStatistics(stats.total_agents, stats.total_cost);
  } catch (error) {
    console.error('Error loading statistics:', error);
    // Fallback to client-side calculation
    const stats = calculateStatistics(dashboardAllAgents);
    displayStatistics(stats.total, stats.cost);
  }
}

// Display statistics in the UI
function displayStatistics(total, cost) {
  document.getElementById('totalAgents').textContent = total;
  document.getElementById('totalCost').textContent = '$' + cost.toFixed(2);
}

// Calculate statistics from agents (fallback)
function calculateStatistics(agents) {
  const total = agents.length;
  let cost = 0;

  agents.forEach(agent => {
    if (agent.statistics) {
      cost += agent.statistics.total_cost || 0;
    }
  });

  return { total, cost };
}

// Render agents in current view
function renderDashboardAgents() {
  if (dashboardFilteredAgents.length === 0) {
    showEmptyState();
    return;
  }

  hideEmptyState();

  if (dashboardCurrentView === 'table') {
    renderTableView();
  } else {
    renderCardView();
  }
}

// Render table view
function renderTableView() {
  const tbody = document.getElementById('agentsTableBody');
  tbody.innerHTML = '';

  dashboardFilteredAgents.forEach(agent => {
    const row = document.createElement('tr');
    row.onclick = () => viewAgent(agent.name);
    const isActive = agent.name === dashboardActiveAgent;

    // Highlight active agent row
    if (isActive) {
      row.style.background = 'rgba(40, 167, 69, 0.15)';
      row.style.borderLeft = '3px solid var(--success-color)';
    }

    row.innerHTML = `
            <td>
                <div class="agent-name-cell">
                    <div class="agent-avatar" style="background: ${getAgentColor(agent)}">
                        ${getAgentInitials(agent.name)}
                    </div>
                    <div class="agent-info">
                        <div class="agent-name">${escapeHtml(agent.name)}</div>
                        ${agent.metadata?.description ?
    `<div class="agent-description">${escapeHtml(agent.metadata.description)}</div>` : ''}
                    </div>
                </div>
            </td>
            <td>${capitalize(agent.type || 'tool-calling')}</td>
            <td>$${(agent.statistics?.total_cost || 0).toFixed(4)}</td>
            <td>
                <div class="actions-cell" onclick="event.stopPropagation()">
                    ${isActive ?
    '<button class="action-btn" disabled style="opacity: 0.5; cursor: default;">Active</button>' :
    `<button class="action-btn" onclick="loadAgentForChat('${escapeHtml(agent.name)}')">Load</button>`}
                    <button class="action-btn" onclick="viewAgent('${escapeHtml(agent.name)}')">View</button>
                    <button class="action-btn" onclick="confirmDelete('${escapeHtml(agent.name)}')">Delete</button>
                </div>
            </td>
        `;

    tbody.appendChild(row);
  });
}

// Render card view
function renderCardView() {
  const grid = document.getElementById('cardView');
  grid.innerHTML = '';

  dashboardFilteredAgents.forEach(agent => {
    const card = document.createElement('div');
    card.className = 'agent-card';
    card.onclick = () => viewAgent(agent.name);
    const isActive = agent.name === dashboardActiveAgent;

    // Add active styling to card
    if (isActive) {
      card.style.borderColor = 'var(--success-color)';
      card.style.boxShadow = '0 0 0 2px rgba(40, 167, 69, 0.2)';
    }

    card.innerHTML = `
            <div class="agent-card-header">
                <div class="agent-card-avatar" style="background: ${getAgentColor(agent)}">
                    ${getAgentInitials(agent.name)}
                </div>
                <div class="agent-card-info">
                    <div class="agent-card-name">${escapeHtml(agent.name)}</div>
                </div>
            </div>
            ${agent.metadata?.description ?
    `<div class="agent-description">${escapeHtml(agent.metadata.description)}</div>` :
    '<div class="agent-description" style="opacity: 0.5">No description</div>'}
            <div class="agent-card-meta">
                <span>📦 ${capitalize(agent.type || 'tool-calling')}</span>
                <span>🔧 ${agent.enabled_plugins?.length || 0} plugins</span>
            </div>
            <div class="agent-card-stats">
                <div class="card-stat">
                    <div class="card-stat-value">${formatNumber(agent.statistics?.token_usage || 0)}</div>
                    <div class="card-stat-label">Tokens</div>
                </div>
                <div class="card-stat">
                    <div class="card-stat-value">$${(agent.statistics?.total_cost || 0).toFixed(2)}</div>
                    <div class="card-stat-label">Cost</div>
                </div>
            </div>
            <div class="agent-card-actions" onclick="event.stopPropagation()">
                ${isActive ?
    '<button class="action-btn" disabled style="opacity: 0.5; cursor: default;">Active</button>' :
    `<button class="action-btn" onclick="loadAgentForChat('${escapeHtml(agent.name)}')">Load</button>`}
                <button class="action-btn" onclick="viewAgent('${escapeHtml(agent.name)}')">View</button>
                <button class="action-btn" onclick="confirmDelete('${escapeHtml(agent.name)}')">Delete</button>
            </div>
        `;

    grid.appendChild(card);
  });
}

// Filter agents based on search and filters
function filterAgents() {
  const searchTerm = document.getElementById('searchInput').value.toLowerCase();

  dashboardFilteredAgents = dashboardAllAgents.filter(agent => {
    // Search filter
    const matchesSearch = !searchTerm ||
            agent.name.toLowerCase().includes(searchTerm) ||
            (agent.metadata?.description || '').toLowerCase().includes(searchTerm);
    return matchesSearch;
  });

  sortAgents();
  renderDashboardAgents();
}

// Sort agents
function sortAgents() {
  const sortBy = document.getElementById('sortSelect').value;

  dashboardFilteredAgents.sort((a, b) => {
    let aVal, bVal;

    switch (sortBy) {
      case 'name':
        aVal = a.name.toLowerCase();
        bVal = b.name.toLowerCase();
        return aVal.localeCompare(bVal);

      case 'created_at':
        aVal = new Date(a.statistics?.created_at || 0);
        bVal = new Date(b.statistics?.created_at || 0);
        return bVal - aVal; // Newest first

      case 'cost':
        aVal = a.statistics?.total_cost || 0;
        bVal = b.statistics?.total_cost || 0;
        return bVal - aVal; // Highest first

      default:
        return 0;
    }
  });

  renderDashboardAgents();
}

// Switch between table and card view
function switchView(view) {
  dashboardCurrentView = view;

  // Update button states
  document.querySelectorAll('.view-btn').forEach(btn => {
    btn.classList.toggle('active', btn.dataset.view === view);
  });

  // Show/hide views
  const tableView = document.getElementById('tableView');
  const cardView = document.getElementById('cardView');

  if (view === 'table') {
    tableView.classList.remove('hidden');
    cardView.classList.add('hidden');
  } else {
    tableView.classList.add('hidden');
    cardView.classList.remove('hidden');
  }

  renderDashboardAgents();
}

// Create new agent
function createAgent() {
  window.location.href = '/agents-create.html';
}

// View agent details
function viewAgent(name) {
  window.location.href = `/agents/${encodeURIComponent(name)}`;
}

// Load agent for chat (sets current agent then redirects to chat view)
async function loadAgentForChat(name) {
  try {
    await switchToAgent(name);
    window.location.href = '/';
  } catch (error) {
    console.error('Failed to load agent for chat:', error);
    showError('Failed to load agent for chat');
  }
}

// Delete agent with confirmation
async function confirmDelete(name) {
  if (!confirm(`Are you sure you want to delete agent "${name}"? This action cannot be undone.`)) {
    return;
  }

  try {
    const response = await fetch(`/api/agents?name=${encodeURIComponent(name)}`, {
      method: 'DELETE'
    });

    if (!response.ok) {
      throw new Error('Failed to delete agent');
    }

    // Reload agents
    await loadAgents();
    showSuccess(`Agent "${name}" deleted successfully`);

  } catch (error) {
    console.error('Error deleting agent:', error);
    showError('Failed to delete agent');
  }
}

// Helper functions
function getAgentColor(agent) {
  if (agent.metadata?.avatar_color) {
    return agent.metadata.avatar_color;
  }
  // Generate color from name
  const hash = agent.name.split('').reduce((acc, char) => {
    return char.charCodeAt(0) + ((acc << 5) - acc);
  }, 0);
  const hue = hash % 360;
  return `hsl(${hue}, 60%, 50%)`;
}

function getAgentInitials(name) {
  const words = name.split(/[\s_-]+/);
  if (words.length >= 2) {
    return (words[0][0] + words[1][0]).toUpperCase();
  }
  return name.substring(0, 2).toUpperCase();
}

function capitalize(str) {
  return str.charAt(0).toUpperCase() + str.slice(1);
}

function formatNumber(num) {
  if (num >= 1000000) {
    return (num / 1000000).toFixed(1) + 'M';
  } else if (num >= 1000) {
    return (num / 1000).toFixed(1) + 'K';
  }
  return num.toString();
}

function formatDate(dateString) {
  if (!dateString) return 'Never';
  const date = new Date(dateString);
  const now = new Date();
  const diff = now - date;

  const minutes = Math.floor(diff / 60000);
  const hours = Math.floor(diff / 3600000);
  const days = Math.floor(diff / 86400000);

  if (minutes < 1) return 'Just now';
  if (minutes < 60) return `${minutes}m ago`;
  if (hours < 24) return `${hours}h ago`;
  if (days < 7) return `${days}d ago`;

  return date.toLocaleDateString();
}

function escapeHtml(text) {
  const div = document.createElement('div');
  div.textContent = text;
  return div.innerHTML;
}

function showLoading(show) {
  document.getElementById('loadingState').style.display = show ? 'block' : 'none';
  document.getElementById('tableView').style.display = show ? 'none' : '';
  document.getElementById('cardView').style.display = show ? 'none' : '';
}

function showEmptyState() {
  document.getElementById('emptyState').classList.remove('hidden');
  document.getElementById('tableView').classList.add('hidden');
  document.getElementById('cardView').classList.add('hidden');
}

function hideEmptyState() {
  document.getElementById('emptyState').classList.add('hidden');
  if (dashboardCurrentView === 'table') {
    document.getElementById('tableView').classList.remove('hidden');
  } else {
    document.getElementById('cardView').classList.remove('hidden');
  }
}

function showError(message) {
  // Simple alert for now - could be replaced with toast notification
  alert('Error: ' + message);
}

function showSuccess(message) {
  // Simple alert for now - could be replaced with toast notification
  alert(message);
}

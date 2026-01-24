// Agents Dashboard JavaScript

let dashboardAllAgents = [];
let dashboardFilteredAgents = [];
let dashboardCurrentView = 'table';
const dashboardCurrentSort = 'name';
const dashboardSortOrder = 'asc';
let dashboardRefreshInterval = null;
let dashboardCurrentSourceFilter = 'all'; // 'all', 'ori', 'claude', 'codex'
let externalAgentsData = null;

function initializeDashboard() {
  loadAgents();
  loadExternalAgents();
  setupAutoRefresh();
  setupFilterTabs();
}

// Load external agents (Claude, Codex)
async function loadExternalAgents() {
  try {
    if (typeof ExternalAgents !== 'undefined') {
      externalAgentsData = await ExternalAgents.fetchExternalAgents();
      updateExternalAgentsCounts();
      updateExternalAgentsUI();
    }
  } catch (error) {
    console.error('Error loading external agents:', error);
  }
}

// Update UI based on whether external agents are enabled
function updateExternalAgentsUI() {
  const isEnabled = typeof ExternalAgents !== 'undefined' && ExternalAgents.isExternalAgentsEnabled();
  const claudeTab = document.querySelector('[data-filter="claude"]');
  const codexTab = document.querySelector('[data-filter="codex"]');
  const refreshBtn = document.getElementById('refreshExternalBtn');
  const disabledBanner = document.getElementById('externalAgentsDisabledBanner');

  if (claudeTab) {
    claudeTab.style.opacity = isEnabled ? '1' : '0.5';
    claudeTab.title = isEnabled ? '' : 'Enable in Settings to view external agents';
  }
  if (codexTab) {
    codexTab.style.opacity = isEnabled ? '1' : '0.5';
    codexTab.title = isEnabled ? '' : 'Enable in Settings to view external agents';
  }
  if (refreshBtn) {
    refreshBtn.style.display = isEnabled ? '' : 'none';
  }

  // Show/hide disabled banner
  if (disabledBanner) {
    disabledBanner.style.display = isEnabled ? 'none' : '';
  }
}

// Update badge counts on filter tabs
function updateExternalAgentsCounts() {
  const claudeCount = ExternalAgents.getClaudeAgents().length;
  const codexAgents = ExternalAgents.getCodexAgents();
  const codexCount = codexAgents.length;

  const claudeCountEl = document.getElementById('claudeAgentCount');
  const codexCountEl = document.getElementById('codexAgentCount');

  if (claudeCountEl) {
    claudeCountEl.textContent = claudeCount;
    claudeCountEl.style.display = claudeCount > 0 ? 'inline' : 'none';
  }
  if (codexCountEl) {
    codexCountEl.textContent = codexCount;
    codexCountEl.style.display = codexCount > 0 ? 'inline' : 'none';
  }
}

// Setup filter tab click handlers
function setupFilterTabs() {
  document.querySelectorAll('.source-filter-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      const filter = btn.dataset.filter;
      setSourceFilter(filter);
    });
  });
}

// Set source filter and re-render
function setSourceFilter(filter) {
  dashboardCurrentSourceFilter = filter;

  // Update button states
  document.querySelectorAll('.source-filter-btn').forEach(btn => {
    btn.classList.toggle('active', btn.dataset.filter === filter);
  });

  // Re-render based on filter
  renderDashboardAgents();
}

// Refresh external agents
async function refreshExternalAgents() {
  const btn = document.getElementById('refreshExternalBtn');
  if (btn) {
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner-border spinner-border-sm" role="status"></span> Refreshing...';
  }

  try {
    if (typeof ExternalAgents !== 'undefined') {
      externalAgentsData = await ExternalAgents.refreshExternalAgents();
      updateExternalAgentsCounts();
      renderDashboardAgents();
    }
  } catch (error) {
    console.error('Error refreshing external agents:', error);
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.innerHTML = 'Refresh External';
    }
  }
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

async function loadAgents() {
  try {
    showLoading(true);
    const response = await fetch('/api/agents');

    if (!response.ok) {
      throw new Error('Failed to load agents');
    }

    const data = await response.json();
    dashboardAllAgents = data.agents || [];
    dashboardFilteredAgents = [...dashboardAllAgents];

    updateStatistics();
    renderDashboardAgents();
    showLoading(false);

  } catch (error) {
    console.error('Error loading agents:', error);
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
  const showOri = dashboardCurrentSourceFilter === 'all' || dashboardCurrentSourceFilter === 'ori';
  const showClaude = dashboardCurrentSourceFilter === 'all' || dashboardCurrentSourceFilter === 'claude';
  const showCodex = dashboardCurrentSourceFilter === 'all' || dashboardCurrentSourceFilter === 'codex';

  const hasOriAgents = showOri && dashboardFilteredAgents.length > 0;
  const claudeAgents = showClaude && typeof ExternalAgents !== 'undefined' ? ExternalAgents.getClaudeAgents() : [];
  const codexAgents = showCodex && typeof ExternalAgents !== 'undefined' ? ExternalAgents.getCodexAgents() : [];
  const codexData = showCodex && typeof ExternalAgents !== 'undefined' ? ExternalAgents.getCodexData() : null;

  const hasAnyContent = hasOriAgents || claudeAgents.length > 0 || codexAgents.length > 0;

  if (!hasAnyContent) {
    showEmptyState();
    return;
  }

  hideEmptyState();

  if (dashboardCurrentView === 'table') {
    renderTableView(showOri, claudeAgents, codexAgents, codexData);
  } else {
    renderCardView(showOri, claudeAgents, codexAgents, codexData);
  }
}

// Render table view
function renderTableView(showOri, claudeAgents, codexAgents, codexData) {
  const tbody = document.getElementById('agentsTableBody');
  tbody.innerHTML = '';

  // Render Ori agents
  if (showOri) {
    dashboardFilteredAgents.forEach(agent => {
      const row = document.createElement('tr');
      row.onclick = () => viewAgent(agent.name);

      const avatarHtml = getAvatarHtml(agent, 'agent-avatar');

      row.innerHTML = `
              <td>
                  <div class="agent-name-cell">
                      ${avatarHtml}
                      <div class="agent-info">
                          <div class="agent-name">
                              ${escapeHtml(agent.name)}
                              <span class="badge badge-ori">Ori</span>
                          </div>
                      </div>
                  </div>
              </td>
              <td class="description-cell">${agent.metadata?.description ? escapeHtml(agent.metadata.description) : '<span class="text-muted">-</span>'}</td>
              <td>${capitalize(agent.type || 'tool-calling')}</td>
              <td>$${(agent.statistics?.total_cost || 0).toFixed(4)}</td>
              <td>
                  <div class="actions-cell" onclick="event.stopPropagation()">
                      <button class="action-btn" onclick="viewAgent('${escapeHtml(agent.name)}')">View</button>
                      <button class="action-btn" onclick="confirmDelete('${escapeHtml(agent.name)}')">Delete</button>
                  </div>
              </td>
          `;

      tbody.appendChild(row);
    });
  }

  // Render Claude agents
  if (claudeAgents.length > 0 && typeof ExternalAgents !== 'undefined') {
    ExternalAgents.renderClaudeAgentsTable(claudeAgents, tbody);
  }

  // Render Codex agents (skills)
  if (codexAgents.length > 0 && typeof ExternalAgents !== 'undefined') {
    ExternalAgents.renderCodexAgentsTable(codexAgents, tbody);
  }
}

// Render card view
function renderCardView(showOri, claudeAgents, codexAgents, codexData) {
  const grid = document.getElementById('cardView');
  grid.innerHTML = '';

  // Render Ori agents
  if (showOri) {
    dashboardFilteredAgents.forEach(agent => {
      const card = document.createElement('div');
      card.className = 'agent-card';
      card.onclick = () => viewAgent(agent.name);

      const avatarHtml = getAvatarHtml(agent, 'agent-card-avatar');

      card.innerHTML = `
              <div class="agent-card-header">
                  ${avatarHtml}
                  <div class="agent-card-info">
                      <div class="agent-card-name">
                          ${escapeHtml(agent.name)}
                          <span class="badge badge-ori">Ori</span>
                      </div>
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
                  <button class="action-btn" onclick="viewAgent('${escapeHtml(agent.name)}')">View</button>
                  <button class="action-btn" onclick="confirmDelete('${escapeHtml(agent.name)}')">Delete</button>
              </div>
          `;

      grid.appendChild(card);
    });
  }

  // Render Claude agents
  if (claudeAgents.length > 0 && typeof ExternalAgents !== 'undefined') {
    ExternalAgents.renderClaudeAgentsCards(claudeAgents, grid);
  }

  // Render Claude settings/plugins card if showing Claude
  if ((dashboardCurrentSourceFilter === 'all' || dashboardCurrentSourceFilter === 'claude') && typeof ExternalAgents !== 'undefined') {
    const claudeData = ExternalAgents.getClaudeData();
    if (claudeData && (claudeData.settings || (claudeData.plugins && claudeData.plugins.length > 0))) {
      ExternalAgents.renderClaudeSettingsCard(claudeData, grid);
    }
  }

  // Render Codex agents (skills)
  if (codexAgents.length > 0 && typeof ExternalAgents !== 'undefined') {
    ExternalAgents.renderCodexAgentsCards(codexAgents, grid);
  }
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

// Returns HTML for agent avatar (image if available, fallback to color/initials)
function getAvatarHtml(agent, className = 'agent-avatar') {
  if (agent.metadata?.avatar_image) {
    return `<div class="${className}" style="padding: 0; overflow: hidden;">
              <img src="/avatars/${escapeHtml(agent.metadata.avatar_image)}" alt="${escapeHtml(agent.name)}" style="width: 100%; height: 100%; object-fit: cover;">
            </div>`;
  }
  return `<div class="${className}" style="background: ${getAgentColor(agent)}">
            ${getAgentInitials(agent.name)}
          </div>`;
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

// escapeHtml is provided by dom-utils.js

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

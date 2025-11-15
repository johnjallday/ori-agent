// Agents Dashboard JavaScript

let allAgents = [];
let filteredAgents = [];
let currentView = 'table';
let currentSort = 'name';
let sortOrder = 'asc';
let refreshInterval = null;

// Initialize dashboard
document.addEventListener('DOMContentLoaded', () => {
    loadAgents();
    setupAutoRefresh();
});

// Setup auto-refresh for statistics
function setupAutoRefresh() {
    // Refresh stats every 60 seconds
    refreshInterval = setInterval(() => {
        // Only refresh if page is visible
        if (!document.hidden) {
            updateStatistics();
        }
    }, 60000); // 60 seconds

    // Stop refreshing when page is hidden
    document.addEventListener('visibilitychange', () => {
        if (document.hidden && refreshInterval) {
            clearInterval(refreshInterval);
            refreshInterval = null;
        } else if (!document.hidden && !refreshInterval) {
            // Restart when page becomes visible
            updateStatistics(); // Immediate refresh
            refreshInterval = setInterval(() => {
                if (!document.hidden) {
                    updateStatistics();
                }
            }, 60000);
        }
    });

    // Cleanup on page unload
    window.addEventListener('beforeunload', () => {
        if (refreshInterval) {
            clearInterval(refreshInterval);
        }
    });
}

// Load agents from API
async function loadAgents() {
    try {
        showLoading(true);
        const response = await fetch('/api/agents/dashboard/list');

        if (!response.ok) {
            throw new Error('Failed to load agents');
        }

        const data = await response.json();
        allAgents = data.agents || [];
        filteredAgents = [...allAgents];

        updateStatistics();
        renderAgents();
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
            const stats = calculateStatistics(allAgents);
            displayStatistics(stats.total, stats.active, stats.messages, stats.cost);
            return;
        }

        const stats = await response.json();
        displayStatistics(
            stats.total_agents,
            stats.active_agents,
            stats.total_messages,
            stats.total_cost
        );
    } catch (error) {
        console.error('Error loading statistics:', error);
        // Fallback to client-side calculation
        const stats = calculateStatistics(allAgents);
        displayStatistics(stats.total, stats.active, stats.messages, stats.cost);
    }
}

// Display statistics in the UI
function displayStatistics(total, active, messages, cost) {
    document.getElementById('totalAgents').textContent = total;
    document.getElementById('activeAgents').textContent = active;
    document.getElementById('totalMessages').textContent = formatNumber(messages);
    document.getElementById('totalCost').textContent = '$' + cost.toFixed(2);
}

// Calculate statistics from agents (fallback)
function calculateStatistics(agents) {
    let total = agents.length;
    let active = 0;
    let messages = 0;
    let cost = 0;

    agents.forEach(agent => {
        if (agent.status === 'active') active++;
        if (agent.statistics) {
            messages += agent.statistics.message_count || 0;
            cost += agent.statistics.total_cost || 0;
        }
    });

    return { total, active, messages, cost };
}

// Render agents in current view
function renderAgents() {
    if (filteredAgents.length === 0) {
        showEmptyState();
        return;
    }

    hideEmptyState();

    if (currentView === 'table') {
        renderTableView();
    } else {
        renderCardView();
    }
}

// Render table view
function renderTableView() {
    const tbody = document.getElementById('agentsTableBody');
    tbody.innerHTML = '';

    filteredAgents.forEach(agent => {
        const row = document.createElement('tr');
        row.onclick = () => viewAgent(agent.name);

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
            <td>
                <span class="status-badge status-${agent.status || 'idle'}">
                    ${capitalize(agent.status || 'idle')}
                </span>
            </td>
            <td>${capitalize(agent.type || 'tool-calling')}</td>
            <td>${formatNumber(agent.statistics?.message_count || 0)}</td>
            <td>$${(agent.statistics?.total_cost || 0).toFixed(4)}</td>
            <td>${formatDate(agent.statistics?.last_active)}</td>
            <td>
                <div class="actions-cell" onclick="event.stopPropagation()">
                    <button class="action-btn" onclick="viewAgent('${escapeHtml(agent.name)}')">View</button>
                    <button class="action-btn" onclick="editAgent('${escapeHtml(agent.name)}')">Edit</button>
                    <select class="action-btn status-select" onchange="changeAgentStatus('${escapeHtml(agent.name)}', this.value, this)" onclick="event.stopPropagation()">
                        <option value="">Change Status...</option>
                        <option value="active" ${agent.status === 'active' ? 'disabled' : ''}>Active</option>
                        <option value="idle" ${agent.status === 'idle' ? 'disabled' : ''}>Idle</option>
                        <option value="disabled" ${agent.status === 'disabled' ? 'disabled' : ''}>Disabled</option>
                    </select>
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

    filteredAgents.forEach(agent => {
        const card = document.createElement('div');
        card.className = 'agent-card';
        card.onclick = () => viewAgent(agent.name);

        card.innerHTML = `
            <div class="agent-card-header">
                <div class="agent-card-avatar" style="background: ${getAgentColor(agent)}">
                    ${getAgentInitials(agent.name)}
                </div>
                <div class="agent-card-info">
                    <div class="agent-card-name">${escapeHtml(agent.name)}</div>
                    <span class="status-badge status-${agent.status || 'idle'}">
                        ${capitalize(agent.status || 'idle')}
                    </span>
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
                    <div class="card-stat-value">${formatNumber(agent.statistics?.message_count || 0)}</div>
                    <div class="card-stat-label">Messages</div>
                </div>
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
                <select class="action-btn status-select" onchange="changeAgentStatus('${escapeHtml(agent.name)}', this.value, this)">
                    <option value="">Change Status...</option>
                    <option value="active" ${agent.status === 'active' ? 'disabled' : ''}>Active</option>
                    <option value="idle" ${agent.status === 'idle' ? 'disabled' : ''}>Idle</option>
                    <option value="disabled" ${agent.status === 'disabled' ? 'disabled' : ''}>Disabled</option>
                </select>
            </div>
        `;

        grid.appendChild(card);
    });
}

// Filter agents based on search and filters
function filterAgents() {
    const searchTerm = document.getElementById('searchInput').value.toLowerCase();
    const statusFilter = document.getElementById('statusFilter').value;

    filteredAgents = allAgents.filter(agent => {
        // Search filter
        const matchesSearch = !searchTerm ||
            agent.name.toLowerCase().includes(searchTerm) ||
            (agent.metadata?.description || '').toLowerCase().includes(searchTerm);

        // Status filter
        const matchesStatus = !statusFilter || agent.status === statusFilter;

        return matchesSearch && matchesStatus;
    });

    sortAgents();
    renderAgents();
}

// Sort agents
function sortAgents() {
    const sortBy = document.getElementById('sortSelect').value;

    filteredAgents.sort((a, b) => {
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

            case 'last_active':
                aVal = new Date(a.statistics?.last_active || 0);
                bVal = new Date(b.statistics?.last_active || 0);
                return bVal - aVal; // Most recent first

            case 'cost':
                aVal = a.statistics?.total_cost || 0;
                bVal = b.statistics?.total_cost || 0;
                return bVal - aVal; // Highest first

            default:
                return 0;
        }
    });

    renderAgents();
}

// Switch between table and card view
function switchView(view) {
    currentView = view;

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

    renderAgents();
}

// Create new agent
function createAgent() {
    window.location.href = '/agents-create.html';
}

// View agent details
function viewAgent(name) {
    window.location.href = `/agents-detail.html?name=${encodeURIComponent(name)}`;
}

// Edit agent
function editAgent(name) {
    window.location.href = `/agents-edit.html?name=${encodeURIComponent(name)}`;
}

// Change agent status
async function changeAgentStatus(name, newStatus, selectElement) {
    if (!newStatus) return; // User selected "Change Status..." placeholder

    const originalStatus = allAgents.find(a => a.name === name)?.status;

    // Disable the select dropdown during update
    selectElement.disabled = true;

    // Show loading state on status badge
    const statusBadges = document.querySelectorAll('.status-badge');
    statusBadges.forEach(badge => {
        if (badge.closest('tr')?.onclick?.toString().includes(name) ||
            badge.closest('.agent-card')?.onclick?.toString().includes(name)) {
            badge.innerHTML = '⏳ Updating...';
            badge.className = 'status-badge status-updating';
        }
    });

    try {
        const response = await fetch(`/api/agents/${encodeURIComponent(name)}/status`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json'
            },
            body: JSON.stringify({ status: newStatus })
        });

        if (!response.ok) {
            const errorData = await response.json().catch(() => ({}));
            throw new Error(errorData.error || 'Failed to update agent status');
        }

        // Update local agent data (optimistic update)
        const agent = allAgents.find(a => a.name === name);
        if (agent) {
            agent.status = newStatus;
        }

        // Refresh the view
        filterAgents();
        updateStatistics();

        // Show success message
        showSuccess(`Agent "${name}" status changed to ${capitalize(newStatus)}`);

    } catch (error) {
        console.error('Error changing agent status:', error);
        showError('Failed to change agent status: ' + error.message);

        // Revert status on error
        const agent = allAgents.find(a => a.name === name);
        if (agent) {
            agent.status = originalStatus;
        }
        filterAgents();

    } finally {
        // Re-enable the select dropdown
        selectElement.disabled = false;
        selectElement.value = ''; // Reset to placeholder
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
    if (currentView === 'table') {
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

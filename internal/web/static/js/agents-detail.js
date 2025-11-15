// Agent Detail Page JavaScript

let currentAgent = null;
let agentName = '';

// Initialize page
document.addEventListener('DOMContentLoaded', () => {
    // Get agent name from URL
    const params = new URLSearchParams(window.location.search);
    agentName = params.get('name');

    if (!agentName) {
        showError('No agent specified');
        setTimeout(() => {
            window.location.href = '/agents-dashboard';
        }, 2000);
        return;
    }

    loadAgentDetails();
});

// Load agent details from API
async function loadAgentDetails() {
    try {
        showLoading(true);

        const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}/detail`);

        if (!response.ok) {
            if (response.status === 404) {
                throw new Error('Agent not found');
            }
            throw new Error('Failed to load agent details');
        }

        currentAgent = await response.json();
        renderAgentDetails();
        showLoading(false);

    } catch (error) {
        console.error('Error loading agent details:', error);
        showLoading(false);
        showError(error.message || 'Failed to load agent details');
        setTimeout(() => {
            window.location.href = '/agents-dashboard';
        }, 3000);
    }
}

// Render agent details on page
function renderAgentDetails() {
    if (!currentAgent) return;

    // Header
    const avatar = document.getElementById('agentAvatar');
    avatar.style.background = getAgentColor(currentAgent);
    avatar.textContent = getAgentInitials(currentAgent.name);

    document.getElementById('agentName').textContent = currentAgent.name;

    const description = currentAgent.metadata?.description || 'No description provided';
    document.getElementById('agentDescription').textContent = description;

    const statusBadge = document.getElementById('statusBadge');
    const status = currentAgent.status || 'idle';
    statusBadge.className = `status-badge status-${status}`;
    statusBadge.textContent = capitalize(status);

    document.getElementById('agentType').textContent = capitalize(currentAgent.type || 'tool-calling');
    document.getElementById('agentModel').textContent = currentAgent.model || 'Not set';
    document.getElementById('pluginCount').textContent = currentAgent.enabled_plugins?.length || 0;

    // Statistics
    const stats = currentAgent.statistics || {};
    document.getElementById('statMessages').textContent = formatNumber(stats.message_count || 0);
    document.getElementById('statTokens').textContent = formatNumber(stats.token_usage || 0);
    document.getElementById('statCost').textContent = '$' + (stats.total_cost || 0).toFixed(4);

    const avgTokens = stats.message_count > 0
        ? Math.round(stats.token_usage / stats.message_count)
        : 0;
    document.getElementById('statAvgTokens').textContent = formatNumber(avgTokens);

    document.getElementById('createdAt').textContent = formatFullDate(stats.created_at);
    document.getElementById('lastActive').textContent = formatFullDate(stats.last_active);
    document.getElementById('updatedAt').textContent = formatFullDate(stats.updated_at);

    // Configuration
    document.getElementById('configModel').textContent = currentAgent.model || 'Not set';
    document.getElementById('configTemp').textContent = currentAgent.temperature || 1.0;
    document.getElementById('configType').textContent = capitalize(currentAgent.type || 'tool-calling');
    document.getElementById('configRole').textContent = capitalize(currentAgent.role || 'general');

    const systemPrompt = currentAgent.system_prompt || 'Default system prompt';
    document.getElementById('configPrompt').textContent = systemPrompt.length > 100
        ? systemPrompt.substring(0, 100) + '...'
        : systemPrompt;

    // Plugins
    renderPlugins();

    // Tags
    renderTags();

    // MCP Servers
    renderMCPServers();

    // Show content
    document.getElementById('agentHeader').style.display = 'flex';
    document.getElementById('contentGrid').style.display = 'grid';
}

// Render plugins list
function renderPlugins() {
    const container = document.getElementById('pluginsList');
    const plugins = currentAgent.enabled_plugins || [];

    if (plugins.length === 0) {
        container.innerHTML = '<div class="empty-message">No plugins enabled</div>';
        return;
    }

    container.innerHTML = '';
    plugins.forEach(plugin => {
        const item = document.createElement('div');
        item.className = 'plugin-item';
        item.innerHTML = `
            <div>
                <div class="plugin-name">${escapeHtml(plugin.name)}</div>
                ${plugin.version ? `<div class="plugin-version">v${escapeHtml(plugin.version)}</div>` : ''}
            </div>
        `;
        container.appendChild(item);
    });
}

// Render tags
function renderTags() {
    const container = document.getElementById('tagsList');
    const tags = currentAgent.metadata?.tags || [];

    if (tags.length === 0) {
        document.getElementById('tagsSection').style.display = 'none';
        return;
    }

    container.innerHTML = '';
    tags.forEach(tag => {
        const tagEl = document.createElement('span');
        tagEl.className = 'tag';
        tagEl.textContent = tag;
        container.appendChild(tagEl);
    });
}

// Render MCP servers
function renderMCPServers() {
    const container = document.getElementById('mcpList');
    const servers = currentAgent.mcp_servers || [];

    if (servers.length === 0) {
        document.getElementById('mcpSection').style.display = 'none';
        return;
    }

    container.innerHTML = '';
    servers.forEach(server => {
        const item = document.createElement('div');
        item.className = 'plugin-item';
        item.innerHTML = `
            <div class="plugin-name">${escapeHtml(server)}</div>
        `;
        container.appendChild(item);
    });
}

// Actions
function chatWithAgent() {
    // Switch to this agent and go to chat
    fetch(`/api/agents?name=${encodeURIComponent(agentName)}`, {
        method: 'PUT'
    })
    .then(response => {
        if (response.ok) {
            window.location.href = '/';
        }
    })
    .catch(error => {
        console.error('Error switching agent:', error);
        showError('Failed to switch to agent');
    });
}

function editAgent() {
    // TODO: Implement edit page in Task 5.0
    alert('Edit functionality will be available after implementing the agent creation/edit form');
}

async function confirmDelete() {
    if (!confirm(`Are you sure you want to delete agent "${agentName}"? This action cannot be undone.`)) {
        return;
    }

    try {
        const response = await fetch(`/api/agents?name=${encodeURIComponent(agentName)}`, {
            method: 'DELETE'
        });

        if (!response.ok) {
            throw new Error('Failed to delete agent');
        }

        alert(`Agent "${agentName}" deleted successfully`);
        window.location.href = '/agents-dashboard';

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
    if (!str) return '';
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

function formatFullDate(dateString) {
    if (!dateString) return 'Never';

    const date = new Date(dateString);
    const now = new Date();
    const diff = now - date;

    const minutes = Math.floor(diff / 60000);
    const hours = Math.floor(diff / 3600000);
    const days = Math.floor(diff / 86400000);

    if (minutes < 1) return 'Just now';
    if (minutes < 60) return `${minutes} minutes ago`;
    if (hours < 24) return `${hours} hours ago`;
    if (days < 7) return `${days} days ago`;

    return date.toLocaleString();
}

function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

function showLoading(show) {
    document.getElementById('loadingState').style.display = show ? 'block' : 'none';
}

function showError(message) {
    alert('Error: ' + message);
}

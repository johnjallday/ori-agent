// Agent Detail Page JavaScript

let currentAgent = null;
let agentName = '';
let isEditingConfig = false;
let isEditingPrompt = false;

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

async function fetchAgentDetail() {
    const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}/detail`);

    if (!response.ok) {
        if (response.status === 404) {
            throw new Error('Agent not found');
        }
        throw new Error('Failed to load agent details');
    }

    return response.json();
}

// Load agent details from API
async function loadAgentDetails() {
    try {
        showLoading(true);
        currentAgent = await fetchAgentDetail();
        renderAgentDetails();
    } catch (error) {
        console.error('Error loading agent details:', error);
        showError(error.message || 'Failed to load agent details');
        setTimeout(() => {
            window.location.href = '/agents-dashboard';
        }, 3000);
    } finally {
        showLoading(false);
    }
}

async function refreshAgentDetails() {
    try {
        currentAgent = await fetchAgentDetail();
        renderAgentDetails();
    } catch (error) {
        console.error('Error refreshing agent details:', error);
        setConfigStatus(error.message || 'Failed to refresh agent details', 'error');
    }
}

// Render agent details on page
function renderAgentDetails() {
    if (!currentAgent) return;

    // Header
    const avatar = document.getElementById('agentAvatar');
    if (avatar) {
        avatar.style.background = getAgentColor(currentAgent);
        avatar.textContent = getAgentInitials(currentAgent.name);
    }

    const nameEl = document.getElementById('agentName');
    if (nameEl) nameEl.textContent = currentAgent.name;

    const description = currentAgent.metadata?.description || 'No description provided';
    const descEl = document.getElementById('agentDescription');
    if (descEl) descEl.textContent = description;

    const typeEl = document.getElementById('agentType');
    if (typeEl) typeEl.textContent = capitalize(currentAgent.type || 'tool-calling');

    const modelEl = document.getElementById('agentModel');
    if (modelEl) modelEl.textContent = currentAgent.model || 'Not set';

    const providerEl = document.getElementById('agentProvider');
    const provider = currentAgent.provider || currentAgent.llm_provider || '';
    if (providerEl) providerEl.textContent = provider || 'Not set';

    const tempEl = document.getElementById('agentTemperature');
    if (tempEl) tempEl.textContent = currentAgent.temperature ?? 'Not set';

    const maxTokensEl = document.getElementById('agentMaxTokens');
    if (maxTokensEl) maxTokensEl.textContent = formatMaxTokens(currentAgent.max_output_tokens);

    const pluginCountEl = document.getElementById('pluginCount');
    if (pluginCountEl) pluginCountEl.textContent = currentAgent.enabled_plugins?.length || 0;

    // Statistics
    const stats = currentAgent.statistics || {};
    const statMessages = document.getElementById('statMessages');
    if (statMessages) statMessages.textContent = formatNumber(stats.message_count || 0);
    const statTokens = document.getElementById('statTokens');
    if (statTokens) statTokens.textContent = formatNumber(stats.token_usage || 0);
    const statCost = document.getElementById('statCost');
    if (statCost) statCost.textContent = '$' + (stats.total_cost || 0).toFixed(4);

    const avgTokens = stats.message_count > 0
        ? Math.round(stats.token_usage / stats.message_count)
        : 0;
    const statAvgTokens = document.getElementById('statAvgTokens');
    if (statAvgTokens) statAvgTokens.textContent = formatNumber(avgTokens);

    const createdAt = document.getElementById('createdAt');
    if (createdAt) createdAt.textContent = formatFullDate(stats.created_at);
    const updatedAt = document.getElementById('updatedAt');
    if (updatedAt) updatedAt.textContent = formatFullDate(stats.updated_at);

    // Configuration
    const configModel = document.getElementById('configModel');
    if (configModel) configModel.textContent = currentAgent.model || 'Not set';
    const configTemp = document.getElementById('configTemp');
    if (configTemp) configTemp.textContent = currentAgent.temperature ?? 1.0;
    const configType = document.getElementById('configType');
    if (configType) configType.textContent = capitalize(currentAgent.type || 'tool-calling');
    const configRole = document.getElementById('configRole');
    if (configRole) configRole.textContent = capitalize(currentAgent.role || 'general');

    const systemPrompt = currentAgent.system_prompt || 'Default system prompt';
    const promptEl = document.getElementById('configPrompt');
    if (promptEl) {
        promptEl.textContent = systemPrompt || 'No system prompt set';
    }
    const promptDisplay = document.getElementById('promptDisplay');
    if (promptDisplay) {
        promptDisplay.textContent = systemPrompt || 'No system prompt set';
    }

    // Plugins
    renderPlugins();

    // Tags
    renderTags();

    // MCP Servers
    renderMCPServers();

    // Show content
    const header = document.getElementById('agentHeader');
    if (header) header.style.display = 'flex';
    const grid = document.getElementById('contentGrid');
    if (grid) grid.style.display = 'grid';

    // Keep edit form in sync with latest data
    populateConfigForm();
    setConfigEditMode(isEditingConfig);
    populatePromptForm();
    setPromptEditMode(isEditingPrompt);
}

function setConfigEditMode(enabled) {
    const display = document.getElementById('configDisplay');
    const form = document.getElementById('configEditForm');
    const editBtn = document.getElementById('editConfigBtn');
    const actions = document.getElementById('configEditActions');
    const saveBtn = document.getElementById('saveConfigBtn');

    isEditingConfig = enabled;

    if (display) display.style.display = enabled ? 'none' : 'block';
    if (form) form.style.display = enabled ? 'block' : 'none';
    if (actions) actions.style.display = enabled ? 'flex' : 'none';
    if (saveBtn) saveBtn.style.display = enabled ? 'inline-flex' : 'none';
    if (editBtn) {
        editBtn.textContent = enabled ? 'Cancel' : 'Edit';
    }

    if (enabled) {
        populateConfigForm();
        setConfigStatus('');
    }
}

function toggleConfigEditMode() {
    if (isEditingConfig) {
        setConfigEditMode(false);
        setConfigStatus('');
        populateConfigForm(); // reset any unsaved edits
    } else {
        setConfigEditMode(true);
    }
}

function populateConfigForm() {
    if (!currentAgent) return;
    const modelInput = document.getElementById('editModel');
    const providerInput = document.getElementById('editProvider');
    const tempInput = document.getElementById('editTemperature');
    const maxTokensInput = document.getElementById('editMaxTokens');
    const typeSelect = document.getElementById('editType');
    const roleSelect = document.getElementById('editRole');

    if (modelInput) modelInput.value = currentAgent.model || '';
    if (providerInput) providerInput.value = currentAgent.provider || currentAgent.llm_provider || '';
    if (tempInput) tempInput.value = currentAgent.temperature ?? '';
    if (maxTokensInput) maxTokensInput.value = currentAgent.max_output_tokens || '';
    if (typeSelect) typeSelect.value = currentAgent.type || 'tool-calling';
    if (roleSelect) roleSelect.value = currentAgent.role || 'general';
}

async function saveConfigChanges() {
    if (!currentAgent) return;

    const model = document.getElementById('editModel')?.value.trim() || '';
    const provider = document.getElementById('editProvider')?.value.trim() || '';
    const tempRaw = document.getElementById('editTemperature')?.value;
    const maxTokensRaw = document.getElementById('editMaxTokens')?.value;
    const type = document.getElementById('editType')?.value || 'tool-calling';
    const role = document.getElementById('editRole')?.value || 'general';

    if (!model) {
        setConfigStatus('Model is required to save configuration.', 'error');
        return;
    }

    const payload = {
        model,
        type,
        role,
    };

    const temp = parseFloat(tempRaw);
    if (!Number.isNaN(temp)) {
        payload.temperature = temp;
    }

    const maxTokens = parseInt(maxTokensRaw, 10);
    if (!Number.isNaN(maxTokens)) {
        payload.max_output_tokens = maxTokens;
    }

    if (provider) {
        payload.llm_provider = provider;
    }

    try {
        setConfigSavingState(true);
        setConfigStatus('Saving changes...');

        const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}`, {
            method: 'PATCH',
            headers: {
                'Content-Type': 'application/json'
            },
            body: JSON.stringify(payload)
        });

        if (!response.ok) {
            const errorText = await response.text();
            throw new Error(errorText || 'Failed to save changes');
        }

        await refreshAgentDetails();
        setConfigStatus('Configuration updated successfully.', 'success');
        setConfigEditMode(false);
    } catch (error) {
        console.error('Failed to save configuration:', error);
        setConfigStatus(error.message || 'Failed to save configuration', 'error');
    } finally {
        setConfigSavingState(false);
    }
}

function setConfigSavingState(isSaving) {
    const saveBtn = document.getElementById('saveConfigBtn');
    const editBtn = document.getElementById('editConfigBtn');
    if (saveBtn) {
        saveBtn.disabled = isSaving;
        if (isSaving) {
            saveBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1" role="status"></span>Saving...';
        } else {
            saveBtn.innerHTML = 'Save';
        }
    }
    if (editBtn) {
        editBtn.disabled = isSaving;
    }
}

function setConfigStatus(message, type = 'info') {
    const statusEl = document.getElementById('configEditStatus');
    if (!statusEl) return;
    statusEl.textContent = message || '';
    if (!message) return;

    if (type === 'error') {
        statusEl.style.color = 'var(--danger-color)';
    } else if (type === 'success') {
        statusEl.style.color = 'var(--success-color, #22c55e)';
    } else {
        statusEl.style.color = 'var(--text-secondary)';
    }
}

function setPromptEditMode(enabled) {
    const display = document.getElementById('promptDisplay');
    const form = document.getElementById('promptEditForm');
    const editBtn = document.getElementById('editPromptBtn');
    const actions = document.getElementById('promptEditActions');
    const saveBtn = document.getElementById('savePromptBtn');

    isEditingPrompt = enabled;

    if (display) display.style.display = enabled ? 'none' : 'block';
    if (form) form.style.display = enabled ? 'block' : 'none';
    if (actions) actions.style.display = enabled ? 'flex' : 'none';
    if (saveBtn) saveBtn.style.display = enabled ? 'inline-flex' : 'none';
    if (editBtn) {
        editBtn.textContent = enabled ? 'Cancel' : 'Edit';
    }

    if (enabled) {
        populatePromptForm();
        setPromptStatus('');
    }
}

function togglePromptEditMode() {
    if (isEditingPrompt) {
        setPromptEditMode(false);
        setPromptStatus('');
        populatePromptForm(); // reset unsaved edits
    } else {
        setPromptEditMode(true);
    }
}

function populatePromptForm() {
    if (!currentAgent) return;
    const promptInput = document.getElementById('editSystemPrompt');
    if (promptInput) promptInput.value = currentAgent.system_prompt || '';
}

async function savePromptChanges() {
    if (!currentAgent) return;
    const promptInput = document.getElementById('editSystemPrompt');
    const systemPrompt = promptInput ? promptInput.value.trim() : '';

    try {
        setPromptSavingState(true);
        setPromptStatus('Saving system prompt...');

        const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}`, {
            method: 'PATCH',
            headers: {
                'Content-Type': 'application/json'
            },
            body: JSON.stringify({ system_prompt: systemPrompt })
        });

        if (!response.ok) {
            const errorText = await response.text();
            throw new Error(errorText || 'Failed to save system prompt');
        }

        await refreshAgentDetails();
        setPromptStatus('System prompt updated successfully.', 'success');
        setPromptEditMode(false);
    } catch (error) {
        console.error('Failed to save system prompt:', error);
        setPromptStatus(error.message || 'Failed to save system prompt', 'error');
    } finally {
        setPromptSavingState(false);
    }
}

function setPromptSavingState(isSaving) {
    const saveBtn = document.getElementById('savePromptBtn');
    const editBtn = document.getElementById('editPromptBtn');
    if (saveBtn) {
        saveBtn.disabled = isSaving;
        if (isSaving) {
            saveBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1" role="status"></span>Saving...';
        } else {
            saveBtn.innerHTML = 'Save';
        }
    }
    if (editBtn) {
        editBtn.disabled = isSaving;
    }
}

function setPromptStatus(message, type = 'info') {
    const statusEl = document.getElementById('promptEditStatus');
    if (!statusEl) return;
    statusEl.textContent = message || '';
    if (!message) return;

    if (type === 'error') {
        statusEl.style.color = 'var(--danger-color)';
    } else if (type === 'success') {
        statusEl.style.color = 'var(--success-color, #22c55e)';
    } else {
        statusEl.style.color = 'var(--text-secondary)';
    }
}

function formatMaxTokens(value) {
    if (!value || value <= 0) return 'Not set';
    return value.toLocaleString();
}

// Render plugins list
function renderPlugins() {
    const container = document.getElementById('enabledPluginsList');
    if (!container) return;

    const pluginsRaw = currentAgent?.enabled_plugins;
    const plugins = Array.isArray(pluginsRaw) ? pluginsRaw : (pluginsRaw ? [pluginsRaw] : []);

    if (plugins.length === 0) {
        container.innerHTML = '<div class="empty-message">No plugins enabled</div>';
        return;
    }

    container.innerHTML = '';
    plugins.forEach(plugin => {
        const name = typeof plugin === 'string' ? plugin : (plugin?.name || plugin?.id || plugin?.plugin || '');
        const version = typeof plugin === 'object' && plugin ? (plugin.version || plugin?.meta?.version || '') : '';

        const item = document.createElement('div');
        item.className = 'plugin-item';
        item.innerHTML = `
            <div>
                <div class="plugin-name">${escapeHtml(name || '(unknown plugin)')}</div>
                ${version ? `<div class="plugin-version">v${escapeHtml(version)}</div>` : ''}
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

// ============================================
// Plugin Management Functions
// ============================================

let pluginManagerVisible = false;
let allAvailablePlugins = [];

// Toggle plugin manager panel
async function togglePluginManager() {
    const panel = document.getElementById('pluginManagerPanel');
    pluginManagerVisible = !pluginManagerVisible;

    if (pluginManagerVisible) {
        panel.style.display = 'block';
        await loadAvailablePlugins();
    } else {
        panel.style.display = 'none';
    }
}

// Load all available plugins from registry
async function loadAvailablePlugins() {
    const container = document.getElementById('availablePluginsList');
    container.innerHTML = '<div class="text-center py-3" style="color: var(--text-secondary);">Loading plugins...</div>';

    try {
        const response = await fetch('/api/plugins');
        if (!response.ok) throw new Error('Failed to load plugins');

        const data = await response.json();
        allAvailablePlugins = data.plugins || [];

        renderAvailablePlugins();
    } catch (error) {
        console.error('Error loading plugins:', error);
        container.innerHTML = '<div class="text-center py-3" style="color: var(--danger-color);">Failed to load plugins</div>';
    }
}

// Render available plugins with toggle switches
function renderAvailablePlugins() {
    const container = document.getElementById('availablePluginsList');
    const enabledPlugins = currentAgent?.enabled_plugins || [];
    // enabled_plugins can be an array of strings or objects with 'name' property
    const enabledNames = enabledPlugins.map(p => typeof p === 'string' ? p : (p?.name || p));
    const enabledSet = new Set(enabledNames);

    if (allAvailablePlugins.length === 0) {
        container.innerHTML = '<div class="text-center py-3" style="color: var(--text-secondary);">No plugins available. Upload plugins from the Plugins page.</div>';
        return;
    }

    container.innerHTML = '';
    allAvailablePlugins.forEach(plugin => {
        const pluginName = plugin.name || plugin;
        const isEnabled = enabledSet.has(pluginName);

        const item = document.createElement('div');
        item.className = 'plugin-item';
        item.style.cssText = 'display: flex; justify-content: space-between; align-items: center; padding: 12px; background: var(--bg-secondary); border: 1px solid var(--border-color); border-radius: 8px;';
        item.innerHTML = `
            <div style="flex: 1;">
                <div style="font-weight: 500; color: var(--text-primary);">${escapeHtml(pluginName)}</div>
                ${plugin.description ? `<div style="font-size: 12px; color: var(--text-secondary); margin-top: 4px;">${escapeHtml(plugin.description)}</div>` : ''}
            </div>
            <label class="toggle-switch" style="margin-left: 12px;">
                <input type="checkbox" id="plugin_${pluginName}" ${isEnabled ? 'checked' : ''} onchange="togglePlugin('${escapeHtml(pluginName)}', this.checked)">
                <span class="toggle-slider"></span>
            </label>
        `;
        container.appendChild(item);
    });
}

// Toggle plugin for this agent
async function togglePlugin(pluginName, enabled) {
    const checkbox = document.getElementById(`plugin_${pluginName}`);

    try {
        // First, switch to this agent
        const switchResponse = await fetch(`/api/agents?name=${encodeURIComponent(agentName)}`, {
            method: 'PUT'
        });

        if (!switchResponse.ok) {
            throw new Error('Failed to switch to agent');
        }

        // Then enable/disable the plugin
        const endpoint = `/api/plugins/${encodeURIComponent(pluginName)}/${enabled ? 'enable' : 'disable'}`;
        const response = await fetch(endpoint, {
            method: 'POST'
        });

        if (response.ok) {
            showToast(`${pluginName} ${enabled ? 'enabled' : 'disabled'}`, 'success');
            // Reload agent details to refresh the enabled plugins list
            await loadAgentDetails();
            if (pluginManagerVisible) {
                renderAvailablePlugins();
            }
        } else {
            const error = await response.text();
            showToast(`Failed: ${error}`, 'error');
            if (checkbox) checkbox.checked = !enabled;
        }
    } catch (error) {
        console.error('Toggle plugin error:', error);
        showToast(`Failed to ${enabled ? 'enable' : 'disable'} plugin`, 'error');
        if (checkbox) checkbox.checked = !enabled;
    }
}

// Render MCP servers
function renderMCPServers() {
    const container = document.getElementById('mcpList');
    const servers = currentAgent.mcp_servers || [];

    document.getElementById('mcpSection').style.display = 'block';

    if (servers.length === 0) {
        container.innerHTML = '<p style="color: var(--text-secondary); font-size: 14px;">No MCP servers enabled for this agent. Click "Configure" to enable MCP servers.</p>';
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

// Toggle MCP configuration panel
async function toggleMCPConfig() {
    const panel = document.getElementById('mcpConfigPanel');
    if (panel.style.display === 'none') {
        panel.style.display = 'block';
        await loadMCPConfigPanel();
    } else {
        panel.style.display = 'none';
    }
}

// Load MCP configuration panel
async function loadMCPConfigPanel() {
    const panel = document.getElementById('mcpConfigPanel');

    try {
        // Fetch all available MCP servers
        const response = await fetch('/api/mcp/servers');
        const data = await response.json();
        const allServers = data.servers || [];
        const enabledServers = currentAgent.mcp_servers || [];

        panel.innerHTML = `
            <h3 style="font-size: 16px; margin-bottom: 16px; color: var(--text-primary);">Available MCP Servers</h3>
            <div id="mcpServersList">
                ${allServers.map(server => `
                    <div class="mcp-server-config" style="margin-bottom: 16px; padding: 16px; background: var(--bg-tertiary); border-radius: 8px;">
                        <div class="d-flex justify-content-between align-items-start mb-2">
                            <div class="d-flex align-items-center gap-2">
                                <input type="checkbox"
                                    id="mcp_${server.name}"
                                    ${enabledServers.includes(server.name) ? 'checked' : ''}
                                    onchange="toggleMCPServer('${server.name}', this.checked)"
                                    style="cursor: pointer;">
                                <label for="mcp_${server.name}" style="cursor: pointer; font-weight: 600; color: var(--text-primary); margin: 0;">
                                    ${server.name}
                                </label>
                            </div>
                        </div>
                        <div id="mcpConfig_${server.name}" style="display: ${enabledServers.includes(server.name) ? 'block' : 'none'}; margin-top: 12px; padding-left: 24px;">
                            ${getMCPServerConfigUI(server)}
                        </div>
                    </div>
                `).join('')}
            </div>
        `;
    } catch (error) {
        console.error('Failed to load MCP config:', error);
        panel.innerHTML = '<p style="color: var(--text-secondary);">Failed to load MCP configuration</p>';
    }
}

// Get configuration UI for specific MCP server
function getMCPServerConfigUI(server) {
    // Special handling for filesystem server
    if (server.name === 'filesystem') {
        const currentPath = server.args && server.args.length > 2 ? server.args[2] : '/path/to/directory';
        return `
            <div class="mb-2">
                <label style="font-size: 13px; color: var(--text-secondary); display: block; margin-bottom: 4px;">
                    Allowed Directory Path:
                </label>
                <input type="text"
                    id="filesystem_path"
                    value="${currentPath}"
                    placeholder="/path/to/directory"
                    style="width: 100%; padding: 8px; background: var(--bg-secondary); border: 1px solid var(--border-color); border-radius: 4px; color: var(--text-primary); font-size: 14px;"
                    onchange="updateMCPServerConfig('filesystem', 'path', this.value)">
                <small style="color: var(--text-secondary); font-size: 12px;">The directory this agent can access via the filesystem MCP server</small>
            </div>
        `;
    }

    // Default: show command and args
    return `
        <div style="font-size: 13px; color: var(--text-secondary);">
            <div><strong>Command:</strong> ${server.command}</div>
            <div><strong>Args:</strong> ${server.args ? server.args.join(' ') : 'none'}</div>
        </div>
    `;
}

// Toggle MCP server for this agent
async function toggleMCPServer(serverName, enabled) {
    const configDiv = document.getElementById(`mcpConfig_${serverName}`);
    if (configDiv) {
        configDiv.style.display = enabled ? 'block' : 'none';
    }

    try {
        const endpoint = enabled ? `/api/mcp/servers/${serverName}/enable` : `/api/mcp/servers/${serverName}/disable`;
        const response = await fetch(endpoint, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json'
            },
            body: JSON.stringify({ agent_name: agentName })
        });

        if (response.ok) {
            showToast(`${serverName} ${enabled ? 'enabled' : 'disabled'}`, 'success');
            await loadAgent();
        } else {
            const error = await response.text();
            showToast(`Failed: ${error}`, 'error');
            document.getElementById(`mcp_${serverName}`).checked = !enabled;
        }
    } catch (error) {
        console.error('Toggle MCP server error:', error);
        showToast('Failed to update MCP server', 'error');
        document.getElementById(`mcp_${serverName}`).checked = !enabled;
    }
}

// Update MCP server configuration
async function updateMCPServerConfig(serverName, configKey, value) {
    // For now, just store the value - you can expand this to save to backend
    console.log(`Update ${serverName} config: ${configKey} = ${value}`);
    showToast(`${serverName} path updated`, 'info');

    // TODO: Add API call to save per-agent MCP server config
}

// Show toast notification
function showToast(message, type = 'info') {
    // Simple toast implementation - you can enhance this
    console.log(`[${type.toUpperCase()}] ${message}`);
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
    div.textContent = text == null ? '' : String(text);
    return div.innerHTML;
}

function showLoading(show) {
    const loading = document.getElementById('loadingState');
    const content = document.getElementById('content');
    if (loading) loading.style.display = show ? 'flex' : 'none';
    if (content) content.style.display = show ? 'none' : 'block';
}

function showError(message) {
    alert('Error: ' + message);
}

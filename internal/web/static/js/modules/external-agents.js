// External Agents Module
// Handles fetching and rendering of external agent data from Claude Code and Codex

const ExternalAgents = (function() {
    let externalData = null;
    let claudeEnabled = false;
    let codexEnabled = false;
    let currentFilter = 'all'; // 'all', 'ori', 'claude', 'codex'

    // Fetch all external agents data
    async function fetchExternalAgents() {
        try {
            const response = await fetch('/api/external-agents');
            if (!response.ok) {
                throw new Error('Failed to fetch external agents');
            }
            const data = await response.json();

            // Check individual source enabled states
            claudeEnabled = data.claude_enabled === true;
            codexEnabled = data.codex_enabled === true;

            externalData = {
                claude: claudeEnabled ? data.claude : null,
                codex: codexEnabled ? data.codex : null
            };
            return externalData;
        } catch (error) {
            console.error('Error fetching external agents:', error);
            claudeEnabled = false;
            codexEnabled = false;
            externalData = { claude: null, codex: null };
            return externalData;
        }
    }

    // Check if any external agents feature is enabled
    function isExternalAgentsEnabled() {
        return claudeEnabled || codexEnabled;
    }

    // Check if Claude is enabled
    function isClaudeEnabled() {
        return claudeEnabled;
    }

    // Check if Codex is enabled
    function isCodexEnabled() {
        return codexEnabled;
    }

    // Get disabled message (for backwards compatibility)
    function getDisabledMessage() {
        if (!claudeEnabled && !codexEnabled) {
            return 'External agents are disabled. Enable in Settings.';
        }
        return '';
    }

    // Fetch Claude-specific data
    async function fetchClaudeData() {
        try {
            const response = await fetch('/api/external-agents/claude');
            if (!response.ok) {
                throw new Error('Failed to fetch Claude data');
            }
            return await response.json();
        } catch (error) {
            console.error('Error fetching Claude data:', error);
            return null;
        }
    }

    // Fetch Codex-specific data
    async function fetchCodexData() {
        try {
            const response = await fetch('/api/external-agents/codex');
            if (!response.ok) {
                throw new Error('Failed to fetch Codex data');
            }
            return await response.json();
        } catch (error) {
            console.error('Error fetching Codex data:', error);
            return null;
        }
    }

    // Refresh external agents cache
    async function refreshExternalAgents() {
        try {
            const response = await fetch('/api/external-agents/refresh', {
                method: 'POST'
            });
            if (!response.ok) {
                throw new Error('Failed to refresh external agents');
            }
            // Re-fetch data after refresh
            return await fetchExternalAgents();
        } catch (error) {
            console.error('Error refreshing external agents:', error);
            return null;
        }
    }

    // Get color for agent source badge
    function getSourceBadgeClass(source) {
        switch (source) {
            case 'claude':
                return 'badge-claude';
            case 'codex':
                return 'badge-codex';
            default:
                return 'badge-ori';
        }
    }

    // Get icon for agent source
    function getSourceIcon(source) {
        switch (source) {
            case 'claude':
                return '<svg class="source-icon" viewBox="0 0 24 24" width="14" height="14"><path fill="currentColor" d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm-2 15l-5-5 1.41-1.41L10 14.17l7.59-7.59L19 8l-9 9z"/></svg>';
            case 'codex':
                return '<svg class="source-icon" viewBox="0 0 24 24" width="14" height="14"><path fill="currentColor" d="M22.2819 9.8211a5.9847 5.9847 0 0 0-.5157-4.9108 6.0462 6.0462 0 0 0-6.5098-2.9A6.0651 6.0651 0 0 0 4.9807 4.1818a5.9847 5.9847 0 0 0-3.9977 2.9 6.0462 6.0462 0 0 0 .7427 7.0966 5.98 5.98 0 0 0 .511 4.9107 6.051 6.051 0 0 0 6.5146 2.9001A5.9847 5.9847 0 0 0 13.2599 24a6.0557 6.0557 0 0 0 5.7718-4.2058 5.9894 5.9894 0 0 0 3.9977-2.9001 6.0557 6.0557 0 0 0-.7475-7.0729z"/></svg>';
            default:
                return '';
        }
    }

    // Render Claude agents in table view
    function renderClaudeAgentsTable(agents, tbody) {
        agents.forEach(agent => {
            const row = document.createElement('tr');
            row.className = 'external-agent-row';
            row.onclick = () => showClaudeAgentDetail(agent);

            const colorStyle = agent.color ? `background-color: ${agent.color}` : 'background-color: #6366f1';

            row.innerHTML = `
                <td>
                    <div class="agent-name-cell">
                        <div class="agent-avatar" style="${colorStyle}">
                            ${agent.name.substring(0, 2).toUpperCase()}
                        </div>
                        <div class="agent-info">
                            <div class="agent-name">
                                ${escapeHtml(agent.name)}
                                <span class="badge ${getSourceBadgeClass('claude')}">${getSourceIcon('claude')} Claude</span>
                            </div>
                        </div>
                    </div>
                </td>
                <td class="description-cell">${agent.description ? escapeHtml(truncateText(agent.description, 80)) : '<span class="text-muted">-</span>'}</td>
                <td>${agent.model || 'default'}</td>
                <td><span class="read-only-label">Read Only</span></td>
                <td>
                    <div class="actions-cell" onclick="event.stopPropagation()">
                        <button class="action-btn" onclick="ExternalAgents.showClaudeAgentDetail(${JSON.stringify(agent).replace(/"/g, '&quot;')})">View</button>
                    </div>
                </td>
            `;

            tbody.appendChild(row);
        });
    }

    // Render Claude agents in card view
    function renderClaudeAgentsCards(agents, grid) {
        agents.forEach(agent => {
            const card = document.createElement('div');
            card.className = 'agent-card external-agent-card';
            card.onclick = () => showClaudeAgentDetail(agent);

            const colorStyle = agent.color ? `background-color: ${agent.color}` : 'background-color: #6366f1';

            card.innerHTML = `
                <div class="agent-card-header">
                    <div class="agent-card-avatar" style="${colorStyle}">
                        ${agent.name.substring(0, 2).toUpperCase()}
                    </div>
                    <div class="agent-card-info">
                        <div class="agent-card-name">
                            ${escapeHtml(agent.name)}
                            <span class="badge ${getSourceBadgeClass('claude')}">${getSourceIcon('claude')} Claude</span>
                        </div>
                    </div>
                </div>
                ${agent.description ?
                    `<div class="agent-description">${escapeHtml(truncateText(agent.description, 100))}</div>` :
                    '<div class="agent-description" style="opacity: 0.5">No description</div>'}
                <div class="agent-card-meta">
                    <span>Model: ${agent.model || 'default'}</span>
                </div>
                <div class="agent-card-stats">
                    <div class="card-stat">
                        <span class="read-only-label">Read Only</span>
                    </div>
                </div>
            `;

            grid.appendChild(card);
        });
    }

    // Render Claude settings/plugins card
    function renderClaudeSettingsCard(claudeData, container) {
        if (!claudeData) return;

        const settings = claudeData.settings;
        const plugins = claudeData.plugins || [];

        if (!settings && plugins.length === 0) return;

        const card = document.createElement('div');
        card.className = 'claude-settings-card';

        const enabledPluginsHtml = settings && settings.enabledPlugins
            ? Object.entries(settings.enabledPlugins)
                .filter(([_, enabled]) => enabled)
                .map(([name]) => `<span class="plugin-tag enabled">${escapeHtml(name.split('@')[0])}</span>`)
                .join('')
            : '<span class="text-muted">None</span>';

        const installedPluginsHtml = plugins.length > 0
            ? plugins.map(p => `
                <div class="installed-plugin-item">
                    <div class="plugin-name">${escapeHtml(p.name.split('@')[0])}</div>
                    <div class="plugin-meta">
                        <span class="plugin-version">v${escapeHtml(p.version)}</span>
                        <span class="plugin-scope badge-${p.scope}">${p.scope}</span>
                    </div>
                </div>
            `).join('')
            : '<div class="text-muted">No plugins installed</div>';

        card.innerHTML = `
            <div class="claude-settings-header">
                <div class="claude-settings-icon" style="background: linear-gradient(135deg, #6366f1 0%, #8b5cf6 100%)">
                    <svg viewBox="0 0 24 24" width="24" height="24">
                        <path fill="white" d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm-2 15l-5-5 1.41-1.41L10 14.17l7.59-7.59L19 8l-9 9z"/>
                    </svg>
                </div>
                <div class="claude-settings-title">
                    <h4>Claude Code Configuration</h4>
                    <span class="badge badge-claude">Claude</span>
                </div>
            </div>
            <div class="claude-settings-body">
                ${settings ? `
                <div class="settings-section">
                    <h6>Enabled Plugins</h6>
                    <div class="enabled-plugins-list">
                        ${enabledPluginsHtml}
                    </div>
                </div>
                ${settings.permissions ? `
                <div class="settings-section">
                    <h6>Permissions</h6>
                    <div class="permissions-summary">
                        <div class="permission-stat">
                            <span class="permission-count">${settings.permissions.allow?.length || 0}</span>
                            <span class="permission-label">Allow Rules</span>
                        </div>
                        <div class="permission-stat">
                            <span class="permission-count">${settings.permissions.deny?.length || 0}</span>
                            <span class="permission-label">Deny Rules</span>
                        </div>
                        <div class="permission-stat">
                            <span class="permission-mode">${settings.permissions.defaultMode || 'ask'}</span>
                            <span class="permission-label">Default Mode</span>
                        </div>
                    </div>
                </div>
                ` : ''}
                ` : ''}

                <div class="settings-section">
                    <h6>Installed Plugins (${plugins.length})</h6>
                    <div class="installed-plugins-list">
                        ${installedPluginsHtml}
                    </div>
                </div>
            </div>
            <div class="claude-settings-footer">
                <span class="read-only-label">Read Only - Managed by Claude Code</span>
            </div>
        `;

        container.appendChild(card);
    }

    // Render Codex agents in table view
    function renderCodexAgentsTable(agents, tbody) {
        agents.forEach(agent => {
            const row = document.createElement('tr');
            row.className = 'external-agent-row';
            row.onclick = () => showCodexAgentDetail(agent);

            row.innerHTML = `
                <td>
                    <div class="agent-name-cell">
                        <div class="agent-avatar" style="background-color: #10a37f">
                            ${agent.name.substring(0, 2).toUpperCase()}
                        </div>
                        <div class="agent-info">
                            <div class="agent-name">
                                ${escapeHtml(agent.name)}
                                <span class="badge ${getSourceBadgeClass('codex')}">${getSourceIcon('codex')} Codex</span>
                            </div>
                        </div>
                    </div>
                </td>
                <td class="description-cell">${agent.description ? escapeHtml(truncateText(agent.description, 80)) : '<span class="text-muted">-</span>'}</td>
                <td>-</td>
                <td><span class="read-only-label">Read Only</span></td>
                <td>
                    <div class="actions-cell" onclick="event.stopPropagation()">
                        <button class="action-btn" onclick="ExternalAgents.showCodexAgentDetail(${JSON.stringify(agent).replace(/"/g, '&quot;')})">View</button>
                    </div>
                </td>
            `;

            tbody.appendChild(row);
        });
    }

    // Render Codex agents in card view
    function renderCodexAgentsCards(agents, grid) {
        agents.forEach(agent => {
            const card = document.createElement('div');
            card.className = 'agent-card external-agent-card';
            card.onclick = () => showCodexAgentDetail(agent);

            card.innerHTML = `
                <div class="agent-card-header">
                    <div class="agent-card-avatar" style="background-color: #10a37f">
                        ${agent.name.substring(0, 2).toUpperCase()}
                    </div>
                    <div class="agent-card-info">
                        <div class="agent-card-name">
                            ${escapeHtml(agent.name)}
                            <span class="badge ${getSourceBadgeClass('codex')}">${getSourceIcon('codex')} Codex</span>
                        </div>
                    </div>
                </div>
                ${agent.description ?
                    `<div class="agent-description">${escapeHtml(truncateText(agent.description, 100))}</div>` :
                    '<div class="agent-description" style="opacity: 0.5">No description</div>'}
                <div class="agent-card-meta">
                    <span>Skill</span>
                </div>
                <div class="agent-card-stats">
                    <div class="card-stat">
                        <span class="read-only-label">Read Only</span>
                    </div>
                </div>
            `;

            grid.appendChild(card);
        });
    }

    // Show Codex agent detail modal
    function showCodexAgentDetail(agent) {
        // Create modal if it doesn't exist
        let modal = document.getElementById('codexAgentDetailModal');
        if (!modal) {
            modal = document.createElement('div');
            modal.id = 'codexAgentDetailModal';
            modal.className = 'modal fade';
            modal.innerHTML = `
                <div class="modal-dialog modal-lg">
                    <div class="modal-content">
                        <div class="modal-header">
                            <h5 class="modal-title">Codex Skill Details</h5>
                            <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
                        </div>
                        <div class="modal-body" id="codexAgentDetailBody">
                        </div>
                        <div class="modal-footer">
                            <span class="read-only-label me-auto">Read Only - Managed by Codex CLI</span>
                            <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">Close</button>
                        </div>
                    </div>
                </div>
            `;
            document.body.appendChild(modal);
        }

        const body = document.getElementById('codexAgentDetailBody');

        body.innerHTML = `
            <div class="codex-agent-detail">
                <div class="agent-detail-header">
                    <div class="agent-avatar-large" style="background-color: #10a37f">
                        ${agent.name.substring(0, 2).toUpperCase()}
                    </div>
                    <div class="agent-detail-info">
                        <h4>${escapeHtml(agent.name)}</h4>
                        <span class="badge ${getSourceBadgeClass('codex')}">${getSourceIcon('codex')} Codex Skill</span>
                    </div>
                </div>

                <div class="agent-detail-section">
                    <h6>Description</h6>
                    <p>${agent.description ? escapeHtml(agent.description) : '<span class="text-muted">No description</span>'}</p>
                </div>

                ${agent.systemPrompt ? `
                <div class="agent-detail-section">
                    <h6>Skill Content</h6>
                    <pre class="system-prompt-display">${escapeHtml(agent.systemPrompt)}</pre>
                </div>
                ` : ''}
            </div>
        `;

        const bsModal = new bootstrap.Modal(modal);
        bsModal.show();
    }

    // Render Codex config card
    function renderCodexConfigCard(config, container) {
        if (!config || !config.config) return;

        const card = document.createElement('div');
        card.className = 'codex-config-card';

        card.innerHTML = `
            <div class="codex-config-header">
                <div class="codex-config-icon" style="background-color: #10a37f">
                    <svg viewBox="0 0 24 24" width="24" height="24">
                        <path fill="white" d="M22.2819 9.8211a5.9847 5.9847 0 0 0-.5157-4.9108 6.0462 6.0462 0 0 0-6.5098-2.9A6.0651 6.0651 0 0 0 4.9807 4.1818a5.9847 5.9847 0 0 0-3.9977 2.9 6.0462 6.0462 0 0 0 .7427 7.0966 5.98 5.98 0 0 0 .511 4.9107 6.051 6.051 0 0 0 6.5146 2.9001A5.9847 5.9847 0 0 0 13.2599 24a6.0557 6.0557 0 0 0 5.7718-4.2058 5.9894 5.9894 0 0 0 3.9977-2.9001 6.0557 6.0557 0 0 0-.7475-7.0729z"/>
                    </svg>
                </div>
                <div class="codex-config-title">
                    <h4>Codex Configuration</h4>
                    <span class="badge badge-codex">Codex</span>
                </div>
            </div>
            <div class="codex-config-body">
                <div class="config-item">
                    <span class="config-label">Model:</span>
                    <span class="config-value">${escapeHtml(config.config.model || 'Not set')}</span>
                </div>
                ${config.config.modelReasoningEffort ? `
                <div class="config-item">
                    <span class="config-label">Reasoning Effort:</span>
                    <span class="config-value">${escapeHtml(config.config.modelReasoningEffort)}</span>
                </div>
                ` : ''}
                ${config.skills && config.skills.length > 0 ? `
                <div class="config-item">
                    <span class="config-label">Skills:</span>
                    <span class="config-value">${config.skills.length} skill(s)</span>
                </div>
                ` : ''}
                ${config.rules && config.rules.length > 0 ? `
                <div class="config-item">
                    <span class="config-label">Rules:</span>
                    <span class="config-value">${config.rules.length} rule file(s)</span>
                </div>
                ` : ''}
            </div>
            <div class="codex-config-footer">
                <span class="read-only-label">Read Only</span>
            </div>
        `;

        container.appendChild(card);
    }

    // Show Claude agent detail modal
    function showClaudeAgentDetail(agent) {
        // Create modal if it doesn't exist
        let modal = document.getElementById('claudeAgentDetailModal');
        if (!modal) {
            modal = document.createElement('div');
            modal.id = 'claudeAgentDetailModal';
            modal.className = 'modal fade';
            modal.innerHTML = `
                <div class="modal-dialog modal-lg">
                    <div class="modal-content">
                        <div class="modal-header">
                            <h5 class="modal-title">Claude Agent Details</h5>
                            <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
                        </div>
                        <div class="modal-body" id="claudeAgentDetailBody">
                        </div>
                        <div class="modal-footer">
                            <span class="read-only-label me-auto">Read Only - Managed by Claude Code</span>
                            <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">Close</button>
                        </div>
                    </div>
                </div>
            `;
            document.body.appendChild(modal);
        }

        const body = document.getElementById('claudeAgentDetailBody');
        const colorStyle = agent.color ? `background-color: ${agent.color}` : 'background-color: #6366f1';

        body.innerHTML = `
            <div class="claude-agent-detail">
                <div class="agent-detail-header">
                    <div class="agent-avatar-large" style="${colorStyle}">
                        ${agent.name.substring(0, 2).toUpperCase()}
                    </div>
                    <div class="agent-detail-info">
                        <h4>${escapeHtml(agent.name)}</h4>
                        <span class="badge ${getSourceBadgeClass('claude')}">${getSourceIcon('claude')} Claude Code Agent</span>
                    </div>
                </div>

                <div class="agent-detail-section">
                    <h6>Description</h6>
                    <p>${agent.description ? escapeHtml(agent.description) : '<span class="text-muted">No description</span>'}</p>
                </div>

                <div class="agent-detail-section">
                    <h6>Configuration</h6>
                    <div class="config-grid">
                        <div class="config-item">
                            <span class="config-label">Model:</span>
                            <span class="config-value">${agent.model || 'default'}</span>
                        </div>
                        ${agent.color ? `
                        <div class="config-item">
                            <span class="config-label">Color:</span>
                            <span class="config-value">
                                <span class="color-swatch" style="background-color: ${agent.color}"></span>
                                ${agent.color}
                            </span>
                        </div>
                        ` : ''}
                    </div>
                </div>

                ${agent.systemPrompt ? `
                <div class="agent-detail-section">
                    <h6>System Prompt</h6>
                    <pre class="system-prompt-display">${escapeHtml(agent.systemPrompt)}</pre>
                </div>
                ` : ''}
            </div>
        `;

        const bsModal = new bootstrap.Modal(modal);
        bsModal.show();
    }

    // Truncate text helper
    function truncateText(text, maxLength) {
        if (!text) return '';
        if (text.length <= maxLength) return text;
        return text.substring(0, maxLength) + '...';
    }

    // Set current filter
    function setFilter(filter) {
        currentFilter = filter;
    }

    // Get current filter
    function getFilter() {
        return currentFilter;
    }

    // Get external data
    function getData() {
        return externalData;
    }

    // Get Claude agents
    function getClaudeAgents() {
        return externalData?.claude?.agents || [];
    }

    // Get Codex data
    function getCodexData() {
        return externalData?.codex || null;
    }

    // Get Codex agents
    function getCodexAgents() {
        return externalData?.codex?.agents || [];
    }

    // Get Claude data
    function getClaudeData() {
        return externalData?.claude || null;
    }

    // Public API
    return {
        fetchExternalAgents,
        fetchClaudeData,
        fetchCodexData,
        refreshExternalAgents,
        renderClaudeAgentsTable,
        renderClaudeAgentsCards,
        renderClaudeSettingsCard,
        renderCodexAgentsTable,
        renderCodexAgentsCards,
        renderCodexConfigCard,
        showClaudeAgentDetail,
        showCodexAgentDetail,
        getSourceBadgeClass,
        getSourceIcon,
        setFilter,
        getFilter,
        getData,
        getClaudeAgents,
        getCodexAgents,
        getClaudeData,
        getCodexData,
        isExternalAgentsEnabled,
        isClaudeEnabled,
        isCodexEnabled,
        getDisabledMessage
    };
})();

// Make available globally
window.ExternalAgents = ExternalAgents;

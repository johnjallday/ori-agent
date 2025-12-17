// Plugin Management Page JavaScript

// State management
let allPlugins = [];
let filteredPlugins = [];
let notifications = [];
let selectedPlugins = new Set();

// Initialize page
document.addEventListener('DOMContentLoaded', () => {
    initializeEventListeners();
    loadPlugins();
    loadNotifications();
    startNotificationPolling();
});

// Event Listeners
function initializeEventListeners() {
    // Header actions
    document.getElementById('uploadBtn').addEventListener('click', () => {
        if (typeof showPluginUploadModal === 'function') {
            showPluginUploadModal();
        }
    });
    document.getElementById('notificationBadge').addEventListener('click', toggleNotificationDrawer);
    document.getElementById('closeDrawer').addEventListener('click', toggleNotificationDrawer);

    // Filters
    document.getElementById('searchInput').addEventListener('input', debounce(applyFilters, 300));
    document.getElementById('statusFilter').addEventListener('change', applyFilters);
    document.getElementById('categoryFilter').addEventListener('change', applyFilters);

    // Select all checkbox
    document.getElementById('selectAll').addEventListener('change', handleSelectAll);
}

// API calls
async function loadPlugins() {
    showLoading(true);
    try {
        const response = await fetch('/api/plugins?management=true');
        const data = await response.json();

        if (data.plugins) {
            allPlugins = data.plugins;
            filteredPlugins = [...allPlugins];
            renderPluginsTable();
        }
    } catch (error) {
        console.error('Failed to load plugins:', error);
        showToast('Failed to load plugins', 'error');
    } finally {
        showLoading(false);
    }
}

async function loadNotifications() {
    try {
        const response = await fetch('/api/plugins/notifications');
        const data = await response.json();

        if (data.notifications) {
            notifications = data.notifications;
            updateNotificationBadge();
            renderNotifications();
        }
    } catch (error) {
        console.error('Failed to load notifications:', error);
    }
}

async function deletePlugin(pluginName) {
    if (!confirm(`Are you sure you want to delete ${pluginName}? This action cannot be undone.`)) {
        return;
    }

    try {
        const response = await fetch(`/api/plugins/${pluginName}`, {
            method: 'DELETE'
        });
        const data = await response.json();

        if (data.success) {
            showToast(`Plugin ${pluginName} deleted`, 'success');
            await loadPlugins();
        } else {
            showToast(data.message || 'Failed to delete plugin', 'error');
        }
    } catch (error) {
        console.error('Failed to delete plugin:', error);
        showToast('Failed to delete plugin', 'error');
    }
}

async function testPlugin(pluginName) {
    const modal = createTestModal(pluginName);
    document.body.appendChild(modal);
    showModal(modal);
}

async function showPluginDetails(pluginName) {
    try {
        const response = await fetch(`/api/plugins/${pluginName}`);
        const plugin = await response.json();

        const modal = createDetailsModal(plugin);
        document.body.appendChild(modal);
        showModal(modal);
    } catch (error) {
        console.error('Failed to load plugin details:', error);
        showToast('Failed to load plugin details', 'error');
    }
}

async function rollbackPlugin(pluginName) {
    try {
        const response = await fetch(`/api/plugins/${pluginName}`);
        const plugin = await response.json();

        if (!plugin.version_history || plugin.version_history.length === 0) {
            showToast('No previous versions available', 'warning');
            return;
        }

        const modal = createRollbackModal(pluginName, plugin.version_history);
        document.body.appendChild(modal);
        showModal(modal);
    } catch (error) {
        console.error('Failed to load version history:', error);
        showToast('Failed to load version history', 'error');
    }
}

async function dismissNotification(notificationId) {
    try {
        await fetch(`/api/plugins/notifications/${notificationId}/dismiss`, {
            method: 'POST'
        });
        await loadNotifications();
    } catch (error) {
        console.error('Failed to dismiss notification:', error);
    }
}

// Rendering functions
function renderPluginsTable() {
    const tbody = document.getElementById('pluginsTableBody');
    const emptyState = document.getElementById('emptyState');

    if (filteredPlugins.length === 0) {
        tbody.innerHTML = '';
        emptyState.style.display = 'block';
        return;
    }

    emptyState.style.display = 'none';

    tbody.innerHTML = filteredPlugins.map(plugin => `
        <tr data-plugin="${plugin.name}">
            <td>
                <input type="checkbox" class="plugin-checkbox" data-plugin="${plugin.name}">
            </td>
            <td>
                <div class="plugin-name-cell">
                    <div>
                        <div class="plugin-name">${plugin.name}</div>
                        <div class="plugin-description">${plugin.description || 'No description'}</div>
                        ${renderTagBadges(plugin.tags)}
                    </div>
                </div>
            </td>
            <td>${plugin.version || 'N/A'}</td>
            <td>${renderStatusBadge(plugin)}</td>
            <td><span class="category-badge">${plugin.category || 'Uncategorized'}</span></td>
            <td>
                <div class="action-buttons">
                    <button class="btn-action" onclick="showPluginDetails('${plugin.name}')">Details</button>
                    <button class="btn-action" onclick="testPlugin('${plugin.name}')">Test</button>
                    <button class="btn-action" onclick="rollbackPlugin('${plugin.name}')">Rollback</button>
                    <button class="btn-action btn-danger" onclick="deletePlugin('${plugin.name}')">Remove</button>
                </div>
            </td>
        </tr>
    `).join('');

    // Add checkbox event listeners
    document.querySelectorAll('.plugin-checkbox').forEach(checkbox => {
        checkbox.addEventListener('change', handlePluginCheckbox);
    });
}

function renderTagBadges(tags) {
    if (!Array.isArray(tags) || tags.length === 0) {
        return '';
    }

    return `
        <div class="plugin-tags">
            ${tags.map(t => `<span class="tag-badge">${t}</span>`).join('')}
        </div>
    `;
}

function renderStatusBadge(plugin) {
    // Show status badge based on plugin health
    if (plugin.health_status === 'error') {
        return `
            <span class="status-badge status-error">
                <span class="status-dot"></span>
                Error
            </span>
        `;
    } else if (plugin.needs_update) {
        return `
            <span class="status-badge status-update">
                <span class="status-dot"></span>
                Needs Update
            </span>
        `;
    }

    // Show "Healthy" for plugins with no issues
    return `
        <span class="status-badge status-active">
            <span class="status-dot"></span>
            Healthy
        </span>
    `;
}

function renderNotifications() {
    const list = document.getElementById('notificationsList');

    if (notifications.length === 0) {
        list.innerHTML = '<div style="padding: 2rem; text-align: center; color: var(--text-muted);">No notifications</div>';
        return;
    }

    list.innerHTML = notifications.map(notif => `
        <div class="notification-item ${notif.read ? '' : 'unread'}">
            <div class="notification-header">
                <span class="notification-type">${notif.type}</span>
                <span class="notification-time">${formatTime(notif.timestamp)}</span>
            </div>
            <div class="notification-message">${notif.message}</div>
            <div class="notification-actions">
                <button class="btn-action" onclick="dismissNotification('${notif.id}')">Dismiss</button>
            </div>
        </div>
    `).join('');
}

// Modal creation functions
function createDetailsModal(plugin) {
    const modal = document.createElement('div');
    modal.className = 'modal-overlay';
    modal.innerHTML = `
        <div class="modal-content">
            <div class="modal-header">
                <h2 class="modal-title">${plugin.name}</h2>
                <button class="modal-close" onclick="closeModal(this)">&times;</button>
            </div>
            <div class="modal-body">
                <div style="margin-bottom: 1.5rem;">
                    <h3 style="font-size: 1rem; margin-bottom: 0.5rem; color: var(--text-secondary);">Description</h3>
                    <p style="color: var(--text-primary);">${plugin.description || 'No description available'}</p>
                    ${renderTagBadges(plugin.tags)}
                </div>
                <div style="margin-bottom: 1.5rem;">
                    <h3 style="font-size: 1rem; margin-bottom: 0.5rem; color: var(--text-secondary);">Details</h3>
                    <div style="display: grid; grid-template-columns: 150px 1fr; gap: 0.5rem;">
                        <span style="color: var(--text-muted);">Version:</span>
                        <span style="color: var(--text-primary);">${plugin.version || 'N/A'}</span>
                        <span style="color: var(--text-muted);">Category:</span>
                        <span style="color: var(--text-primary);">${plugin.category || 'Uncategorized'}</span>
                        <span style="color: var(--text-muted);">Status:</span>
                        <span>${renderStatusBadge(plugin)}</span>
                        <span style="color: var(--text-muted);">Tags:</span>
                        <div>${renderTagBadges(plugin.tags) || '<span style="color: var(--text-muted);">None</span>'}</div>
                        <span style="color: var(--text-muted);">Path:</span>
                        <span style="color: var(--text-primary); font-family: monospace; font-size: 0.85rem;">${plugin.path || 'N/A'}</span>
                    </div>
                </div>
                ${plugin.permissions ? `
                <div style="margin-bottom: 1.5rem;">
                    <h3 style="font-size: 1rem; margin-bottom: 0.5rem; color: var(--text-secondary);">Permissions</h3>
                    <div style="display: flex; flex-direction: column; gap: 0.5rem;">
                        ${renderPermissions(plugin.permissions)}
                    </div>
                </div>
                ` : ''}
            </div>
            <div class="modal-footer">
                <button class="btn-modern btn-secondary" onclick="closeModal(this)">Close</button>
            </div>
        </div>
    `;
    return modal;
}

function createTestModal(pluginName) {
    const modal = document.createElement('div');
    modal.className = 'modal-overlay';
    modal.innerHTML = `
        <div class="modal-content">
            <div class="modal-header">
                <h2 class="modal-title">Test Plugin: ${pluginName}</h2>
                <button class="modal-close" onclick="closeModal(this)">&times;</button>
            </div>
            <div class="modal-body">
                <div style="margin-bottom: 1rem;">
                    <label style="display: block; margin-bottom: 0.5rem; color: var(--text-secondary);">Test Arguments (JSON)</label>
                    <textarea id="testArgs" style="width: 100%; padding: 0.75rem; border: 1px solid var(--border-color); border-radius: var(--radius-md); background: var(--bg-secondary); color: var(--text-primary); font-family: monospace; min-height: 100px;" placeholder='{"key": "value"}'></textarea>
                </div>
                <div id="testResult" style="display: none; margin-top: 1rem; padding: 1rem; background: var(--bg-secondary); border-radius: var(--radius-md); border: 1px solid var(--border-color);">
                    <h4 style="margin-bottom: 0.5rem; color: var(--text-secondary);">Result:</h4>
                    <pre id="testResultContent" style="margin: 0; white-space: pre-wrap; color: var(--text-primary); font-size: 0.85rem;"></pre>
                </div>
            </div>
            <div class="modal-footer">
                <button class="btn-modern btn-secondary" onclick="closeModal(this)">Close</button>
                <button class="btn-modern btn-primary" onclick="executeTest('${pluginName}')">Run Test</button>
            </div>
        </div>
    `;
    return modal;
}

function createRollbackModal(pluginName, versionHistory) {
    const modal = document.createElement('div');
    modal.className = 'modal-overlay';
    modal.innerHTML = `
        <div class="modal-content">
            <div class="modal-header">
                <h2 class="modal-title">Rollback Plugin: ${pluginName}</h2>
                <button class="modal-close" onclick="closeModal(this)">&times;</button>
            </div>
            <div class="modal-body">
                <p style="color: var(--text-secondary); margin-bottom: 1rem;">Select a version to rollback to:</p>
                <div style="display: flex; flex-direction: column; gap: 0.75rem;">
                    ${versionHistory.map((v, idx) => `
                        <label style="padding: 1rem; border: 1px solid var(--border-color); border-radius: var(--radius-md); cursor: pointer; transition: all 0.2s;" class="version-option">
                            <input type="radio" name="rollbackVersion" value="${v.version}" ${idx === 0 ? 'checked' : ''}>
                            <span style="margin-left: 0.5rem; font-weight: 500;">${v.version}</span>
                            <span style="margin-left: 1rem; color: var(--text-muted); font-size: 0.85rem;">${formatTime(v.timestamp)}</span>
                        </label>
                    `).join('')}
                </div>
            </div>
            <div class="modal-footer">
                <button class="btn-modern btn-secondary" onclick="closeModal(this)">Cancel</button>
                <button class="btn-modern btn-primary" onclick="executeRollback('${pluginName}')">Rollback</button>
            </div>
        </div>
    `;
    return modal;
}

// Modal actions
async function executeTest(pluginName) {
    const argsInput = document.getElementById('testArgs');
    const resultDiv = document.getElementById('testResult');
    const resultContent = document.getElementById('testResultContent');

    try {
        const response = await fetch(`/api/plugins/${pluginName}/test`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ args: argsInput.value || '{}' })
        });
        const data = await response.json();

        resultDiv.style.display = 'block';
        resultContent.textContent = JSON.stringify(data, null, 2);

        if (data.success) {
            showToast('Test completed successfully', 'success');
        } else {
            showToast('Test failed', 'error');
        }
    } catch (error) {
        console.error('Test failed:', error);
        resultDiv.style.display = 'block';
        resultContent.textContent = `Error: ${error.message}`;
        showToast('Test execution failed', 'error');
    }
}

async function executeRollback(pluginName) {
    const selected = document.querySelector('input[name="rollbackVersion"]:checked');
    if (!selected) {
        showToast('Please select a version', 'warning');
        return;
    }

    try {
        const response = await fetch(`/api/plugins/${pluginName}/rollback`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ version: selected.value })
        });
        const data = await response.json();

        if (data.success) {
            showToast(`Rolled back to version ${selected.value}`, 'success');
            closeModal(document.querySelector('.modal-overlay.active .modal-close'));
            await loadPlugins();
        } else {
            showToast(data.message || 'Rollback failed', 'error');
        }
    } catch (error) {
        console.error('Rollback failed:', error);
        showToast('Rollback execution failed', 'error');
    }
}

// Utility functions
function applyFilters() {
    const searchTerm = document.getElementById('searchInput').value.toLowerCase();
    const statusFilter = document.getElementById('statusFilter').value;
    const categoryFilter = document.getElementById('categoryFilter').value;

    filteredPlugins = allPlugins.filter(plugin => {
        // Search filter
        const matchesSearch = !searchTerm ||
            plugin.name.toLowerCase().includes(searchTerm) ||
            (plugin.description && plugin.description.toLowerCase().includes(searchTerm));

        // Status filter
        const pluginStatus = getPluginStatus(plugin);
        const matchesStatus = !statusFilter || pluginStatus === statusFilter;

        // Category filter
        const matchesCategory = !categoryFilter || plugin.category === categoryFilter;

        return matchesSearch && matchesStatus && matchesCategory;
    });

    renderPluginsTable();
}

function getPluginStatus(plugin) {
    if (plugin.health_status === 'error') return 'error';
    if (plugin.needs_update) return 'update';
    return ''; // No status for healthy plugins
}

function handleSelectAll(e) {
    const checkboxes = document.querySelectorAll('.plugin-checkbox');
    checkboxes.forEach(cb => {
        cb.checked = e.target.checked;
        handlePluginCheckbox({ target: cb });
    });
}

function handlePluginCheckbox(e) {
    const pluginName = e.target.dataset.plugin;
    if (e.target.checked) {
        selectedPlugins.add(pluginName);
    } else {
        selectedPlugins.delete(pluginName);
        document.getElementById('selectAll').checked = false;
    }
}

function toggleNotificationDrawer() {
    const drawer = document.getElementById('notificationDrawer');
    drawer.classList.toggle('active');
}

function updateNotificationBadge() {
    const badge = document.getElementById('notificationCount');
    const unreadCount = notifications.filter(n => !n.read).length;

    if (unreadCount > 0) {
        badge.textContent = unreadCount;
        badge.style.display = 'block';
    } else {
        badge.style.display = 'none';
    }
}

function startNotificationPolling() {
    setInterval(loadNotifications, 30000); // Poll every 30 seconds
}

function showModal(modal) {
    setTimeout(() => modal.classList.add('active'), 10);
}

function closeModal(closeBtn) {
    const modal = closeBtn.closest('.modal-overlay');
    modal.classList.remove('active');
    setTimeout(() => modal.remove(), 300);
}

function showToast(message, type = 'info') {
    const container = document.getElementById('toastContainer');
    const toast = document.createElement('div');
    toast.className = `toast ${type}`;
    toast.innerHTML = `
        <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor">
            ${type === 'success' ? '<path d="M12 2C6.5 2 2 6.5 2 12S6.5 22 12 22 22 17.5 22 12 17.5 2 12 2M10 17L5 12L6.41 10.59L10 14.17L17.59 6.58L19 8L10 17Z"/>' :
              type === 'error' ? '<path d="M13,13H11V7H13M13,17H11V15H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z"/>' :
              type === 'warning' ? '<path d="M13,14H11V10H13M13,18H11V16H13M1,21H23L12,2L1,21Z"/>' :
              '<path d="M13,9H11V7H13M13,17H11V11H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z"/>'}
        </svg>
        <span>${message}</span>
    `;

    container.appendChild(toast);

    setTimeout(() => {
        toast.style.opacity = '0';
        setTimeout(() => toast.remove(), 300);
    }, 3000);
}

function showLoading(show) {
    const loading = document.getElementById('loadingState');
    const table = document.querySelector('.plugins-table-container');

    if (show) {
        loading.style.display = 'flex';
        table.style.display = 'none';
    } else {
        loading.style.display = 'none';
        table.style.display = 'block';
    }
}

function formatTime(timestamp) {
    if (!timestamp) return 'N/A';
    const date = new Date(timestamp);
    return date.toLocaleString();
}

function renderPermissions(permissions) {
    return Object.entries(permissions).map(([key, value]) => `
        <div style="display: flex; align-items: center; gap: 0.5rem;">
            <span style="color: ${value ? 'var(--success-color)' : 'var(--danger-color)'};">
                ${value ? '✓' : '✗'}
            </span>
            <span style="color: var(--text-primary); text-transform: capitalize;">
                ${key.replace(/_/g, ' ')}
            </span>
        </div>
    `).join('');
}

function debounce(func, wait) {
    let timeout;
    return function executedFunction(...args) {
        const later = () => {
            clearTimeout(timeout);
            func(...args);
        };
        clearTimeout(timeout);
        timeout = setTimeout(later, wait);
    };
}

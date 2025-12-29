// Plugin Management Page JavaScript

// State management
let allPlugins = [];
let filteredPlugins = [];
let paginatedPlugins = [];
let notifications = [];
let selectedPlugins = new Set();

// Pagination state
let currentPage = 1;
let pageSize = 25;
let totalPages = 1;

// Sorting state
let sortColumn = 'name';
let sortDirection = 'asc';

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

    // Sortable headers
    document.querySelectorAll('.sortable').forEach(header => {
        header.addEventListener('click', () => handleSort(header.dataset.sort));
    });
}

// API calls
async function loadPlugins() {
    showLoading(true);
    try {
        const response = await fetch('/api/plugins?management=true');
        const data = await response.json();

        if (data.plugins) {
            allPlugins = data.plugins;
            filteredPlugins = sortPlugins([...allPlugins]);
            updatePagination();
            renderPluginsTable();
            renderMobileCards();
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

// Sorting function
function handleSort(column) {
    // Toggle direction if same column, otherwise default to asc
    if (sortColumn === column) {
        sortDirection = sortDirection === 'asc' ? 'desc' : 'asc';
    } else {
        sortColumn = column;
        sortDirection = 'asc';
    }

    // Update header UI
    document.querySelectorAll('.sortable').forEach(h => {
        h.classList.remove('active', 'desc');
    });
    const activeHeader = document.querySelector(`.sortable[data-sort="${column}"]`);
    if (activeHeader) {
        activeHeader.classList.add('active');
        if (sortDirection === 'desc') {
            activeHeader.classList.add('desc');
        }
    }

    applyFilters();
}

// Sort plugins
function sortPlugins(plugins) {
    return [...plugins].sort((a, b) => {
        let aVal, bVal;

        switch (sortColumn) {
            case 'name':
                aVal = (a.name || '').toLowerCase();
                bVal = (b.name || '').toLowerCase();
                break;
            case 'version':
                aVal = a.version || '';
                bVal = b.version || '';
                break;
            case 'status':
                aVal = getPluginStatusPriority(a);
                bVal = getPluginStatusPriority(b);
                break;
            case 'category':
                aVal = (a.category || '').toLowerCase();
                bVal = (b.category || '').toLowerCase();
                break;
            default:
                return 0;
        }

        if (aVal < bVal) return sortDirection === 'asc' ? -1 : 1;
        if (aVal > bVal) return sortDirection === 'asc' ? 1 : -1;
        return 0;
    });
}

// Get status priority for sorting (errors first)
function getPluginStatusPriority(plugin) {
    if (plugin.health_status === 'error') return 0;
    if (plugin.needs_update) return 1;
    if (!plugin.is_configured) return 2;
    return 3;
}

// Pagination functions
function updatePagination() {
    totalPages = Math.ceil(filteredPlugins.length / pageSize);
    if (currentPage > totalPages) currentPage = totalPages || 1;

    const start = (currentPage - 1) * pageSize;
    const end = Math.min(start + pageSize, filteredPlugins.length);
    paginatedPlugins = filteredPlugins.slice(start, end);

    // Update pagination info
    document.getElementById('paginationStart').textContent = filteredPlugins.length > 0 ? start + 1 : 0;
    document.getElementById('paginationEnd').textContent = end;
    document.getElementById('paginationTotal').textContent = filteredPlugins.length;

    // Update buttons
    document.getElementById('prevPage').disabled = currentPage <= 1;
    document.getElementById('nextPage').disabled = currentPage >= totalPages;

    // Render page numbers
    renderPageNumbers();
}

function renderPageNumbers() {
    const container = document.getElementById('pageNumbers');
    if (!container) return;

    let html = '';
    const maxVisible = 5;
    let startPage = Math.max(1, currentPage - Math.floor(maxVisible / 2));
    let endPage = Math.min(totalPages, startPage + maxVisible - 1);

    if (endPage - startPage < maxVisible - 1) {
        startPage = Math.max(1, endPage - maxVisible + 1);
    }

    if (startPage > 1) {
        html += `<button class="pagination-btn" onclick="goToPage(1)">1</button>`;
        if (startPage > 2) html += `<span style="padding: 0 0.5rem; color: var(--text-muted);">...</span>`;
    }

    for (let i = startPage; i <= endPage; i++) {
        html += `<button class="pagination-btn ${i === currentPage ? 'active' : ''}" onclick="goToPage(${i})">${i}</button>`;
    }

    if (endPage < totalPages) {
        if (endPage < totalPages - 1) html += `<span style="padding: 0 0.5rem; color: var(--text-muted);">...</span>`;
        html += `<button class="pagination-btn" onclick="goToPage(${totalPages})">${totalPages}</button>`;
    }

    container.innerHTML = html;
}

function changePage(delta) {
    goToPage(currentPage + delta);
}

function goToPage(page) {
    currentPage = Math.max(1, Math.min(page, totalPages));
    updatePagination();
    renderPluginsTable();
    renderMobileCards();
}

function changePageSize(size) {
    pageSize = parseInt(size, 10);
    currentPage = 1;
    updatePagination();
    renderPluginsTable();
    renderMobileCards();
}

// Get plugin icon based on category
function getPluginIcon(plugin) {
    const category = (plugin.category || '').toLowerCase();
    let iconClass = '';
    let iconSvg = '';

    if (category.includes('system')) {
        iconClass = 'category-system';
        iconSvg = '<svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor"><path d="M12,15.5A3.5,3.5 0 0,1 8.5,12A3.5,3.5 0 0,1 12,8.5A3.5,3.5 0 0,1 15.5,12A3.5,3.5 0 0,1 12,15.5M19.43,12.97C19.47,12.65 19.5,12.33 19.5,12C19.5,11.67 19.47,11.34 19.43,11L21.54,9.37C21.73,9.22 21.78,8.95 21.66,8.73L19.66,5.27C19.54,5.05 19.27,4.96 19.05,5.05L16.56,6.05C16.04,5.66 15.5,5.32 14.87,5.07L14.5,2.42C14.46,2.18 14.25,2 14,2H10C9.75,2 9.54,2.18 9.5,2.42L9.13,5.07C8.5,5.32 7.96,5.66 7.44,6.05L4.95,5.05C4.73,4.96 4.46,5.05 4.34,5.27L2.34,8.73C2.21,8.95 2.27,9.22 2.46,9.37L4.57,11C4.53,11.34 4.5,11.67 4.5,12C4.5,12.33 4.53,12.65 4.57,12.97L2.46,14.63C2.27,14.78 2.21,15.05 2.34,15.27L4.34,18.73C4.46,18.95 4.73,19.03 4.95,18.95L7.44,17.94C7.96,18.34 8.5,18.68 9.13,18.93L9.5,21.58C9.54,21.82 9.75,22 10,22H14C14.25,22 14.46,21.82 14.5,21.58L14.87,18.93C15.5,18.67 16.04,18.34 16.56,17.94L19.05,18.95C19.27,19.03 19.54,18.95 19.66,18.73L21.66,15.27C21.78,15.05 21.73,14.78 21.54,14.63L19.43,12.97Z"/></svg>';
    } else if (category.includes('ai') || category.includes('ml')) {
        iconClass = 'category-ai';
        iconSvg = '<svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor"><path d="M21.33,12.91C21.42,14.46 20.71,15.95 19.44,16.86L20.21,18.35C20.44,18.8 20.47,19.33 20.27,19.8C20.08,20.27 19.69,20.64 19.21,20.8L18.42,21.05C18.25,21.11 18.06,21.14 17.88,21.14C17.37,21.14 16.89,20.91 16.56,20.5L14.44,18C13.55,17.85 12.71,17.47 12,16.9C11.5,17.05 11,17.13 10.5,17.13C9.62,17.13 8.74,16.86 8,16.36L6.73,17.63C6.19,18.17 5.39,18.38 4.66,18.18L4.24,18.07C3.44,17.86 2.79,17.31 2.5,16.57L2.41,16.35C2.05,15.47 2.21,14.47 2.82,13.75L4.11,12.17C3.86,11.08 3.86,9.94 4.12,8.85L2.82,7.27C2.2,6.55 2.04,5.55 2.41,4.67L2.5,4.46C2.79,3.72 3.44,3.16 4.24,2.96L4.66,2.85C5.39,2.64 6.19,2.86 6.73,3.39L8,4.65C8.74,4.15 9.62,3.88 10.5,3.88C11,3.88 11.5,3.96 12,4.11C12.71,3.55 13.55,3.17 14.44,3L16.56,0.5C16.9,0.11 17.37,-0.11 17.89,0.14C18.06,0.14 18.25,0.17 18.42,0.22L19.21,0.48C19.69,0.64 20.08,1.01 20.27,1.48C20.47,1.95 20.44,2.48 20.21,2.93L19.44,4.42C20.7,5.33 21.42,6.82 21.33,8.37L21.33,12.91Z"/></svg>';
    } else if (category.includes('data')) {
        iconClass = 'category-data';
        iconSvg = '<svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor"><path d="M12,3C7.58,3 4,4.79 4,7C4,9.21 7.58,11 12,11C16.42,11 20,9.21 20,7C20,4.79 16.42,3 12,3M4,9V12C4,14.21 7.58,16 12,16C16.42,16 20,14.21 20,12V9C20,11.21 16.42,13 12,13C7.58,13 4,11.21 4,9M4,14V17C4,19.21 7.58,21 12,21C16.42,21 20,19.21 20,17V14C20,16.21 16.42,18 12,18C7.58,18 4,16.21 4,14Z"/></svg>';
    } else if (category.includes('automation')) {
        iconClass = 'category-automation';
        iconSvg = '<svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor"><path d="M12,2A2,2 0 0,1 14,4C14,4.74 13.6,5.39 13,5.73V7H14A7,7 0 0,1 21,14H22A1,1 0 0,1 23,15V18A1,1 0 0,1 22,19H21V20A2,2 0 0,1 19,22H5A2,2 0 0,1 3,20V19H2A1,1 0 0,1 1,18V15A1,1 0 0,1 2,14H3A7,7 0 0,1 10,7H11V5.73C10.4,5.39 10,4.74 10,4A2,2 0 0,1 12,2M7.5,13A2.5,2.5 0 0,0 5,15.5A2.5,2.5 0 0,0 7.5,18A2.5,2.5 0 0,0 10,15.5A2.5,2.5 0 0,0 7.5,13M16.5,13A2.5,2.5 0 0,0 14,15.5A2.5,2.5 0 0,0 16.5,18A2.5,2.5 0 0,0 19,15.5A2.5,2.5 0 0,0 16.5,13Z"/></svg>';
    } else if (category.includes('security')) {
        iconClass = 'category-security';
        iconSvg = '<svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor"><path d="M12,12H19C18.47,16.11 15.72,19.78 12,20.92V12H5V6.3L12,3.19M12,1L3,5V11C3,16.55 6.84,21.73 12,23C17.16,21.73 21,16.55 21,11V5L12,1Z"/></svg>';
    } else {
        iconSvg = '<svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor"><path d="M20.5,11H19V7C19,5.89 18.1,5 17,5H13V3.5A2.5,2.5 0 0,0 10.5,1A2.5,2.5 0 0,0 8,3.5V5H4C2.89,5 2,5.89 2,7V10.8H3.5C5,10.8 6.2,12 6.2,13.5C6.2,15 5,16.2 3.5,16.2H2V20C2,21.11 2.89,22 4,22H7.8V20.5C7.8,19 9,17.8 10.5,17.8C12,17.8 13.2,19 13.2,20.5V22H17C18.11,22 19,21.11 19,20V16H20.5A2.5,2.5 0 0,0 23,13.5A2.5,2.5 0 0,0 20.5,11Z"/></svg>';
    }

    return `<div class="plugin-icon ${iconClass}">${iconSvg}</div>`;
}

// Rendering functions
function renderPluginsTable() {
    const tbody = document.getElementById('pluginsTableBody');
    const emptyState = document.getElementById('emptyState');
    const tableContainer = document.querySelector('.plugins-table-container');
    const paginationContainer = document.getElementById('paginationContainer');

    if (filteredPlugins.length === 0) {
        tbody.innerHTML = '';
        emptyState.style.display = 'block';
        if (tableContainer) tableContainer.style.display = 'none';
        return;
    }

    emptyState.style.display = 'none';
    if (tableContainer) tableContainer.style.display = 'block';

    // Show/hide pagination based on total items
    if (paginationContainer) {
        paginationContainer.style.display = filteredPlugins.length > 10 ? 'flex' : 'none';
    }

    tbody.innerHTML = paginatedPlugins.map(plugin => `
        <tr data-plugin="${plugin.name}">
            <td>
                <input type="checkbox" class="plugin-checkbox" data-plugin="${plugin.name}" ${selectedPlugins.has(plugin.name) ? 'checked' : ''} aria-label="Select ${plugin.name}">
            </td>
            <td>
                <div class="plugin-name-cell">
                    ${getPluginIcon(plugin)}
                    <div>
                        <div class="plugin-name">${escapeHtml(plugin.name)}</div>
                        <div class="plugin-description">${escapeHtml(plugin.description || 'No description')}</div>
                        ${renderTagBadges(plugin.tags)}
                    </div>
                </div>
            </td>
            <td>${escapeHtml(plugin.version || 'N/A')}</td>
            <td>${renderStatusBadge(plugin)}</td>
            <td><span class="category-badge">${escapeHtml(plugin.category || 'Uncategorized')}</span></td>
            <td>
                <div class="action-buttons">
                    <button class="btn-action" onclick="showPluginDetails('${escapeHtml(plugin.name)}')">Details</button>
                    <button class="btn-action" onclick="testPlugin('${escapeHtml(plugin.name)}')">Test</button>
                    <button class="btn-action" onclick="rollbackPlugin('${escapeHtml(plugin.name)}')">Rollback</button>
                    <button class="btn-action btn-danger" onclick="deletePlugin('${escapeHtml(plugin.name)}')">Remove</button>
                </div>
            </td>
        </tr>
    `).join('');

    // Add checkbox event listeners
    document.querySelectorAll('.plugin-checkbox').forEach(checkbox => {
        checkbox.addEventListener('change', handlePluginCheckbox);
    });

    // Update select all state
    updateSelectAllState();
}

// Render mobile card view
function renderMobileCards() {
    const container = document.getElementById('pluginsCardsContainer');
    if (!container) return;

    if (paginatedPlugins.length === 0) {
        container.innerHTML = '';
        return;
    }

    container.innerHTML = paginatedPlugins.map(plugin => `
        <div class="plugin-card" data-plugin="${plugin.name}">
            <div class="plugin-card-header">
                <input type="checkbox" class="plugin-checkbox plugin-card-checkbox" data-plugin="${plugin.name}" ${selectedPlugins.has(plugin.name) ? 'checked' : ''} aria-label="Select ${plugin.name}">
                ${getPluginIcon(plugin)}
                <div class="plugin-card-info">
                    <div class="plugin-card-name">${escapeHtml(plugin.name)}</div>
                    <div class="plugin-card-description">${escapeHtml(plugin.description || 'No description')}</div>
                </div>
            </div>
            <div class="plugin-card-meta">
                ${renderStatusBadge(plugin)}
                <span class="category-badge">${escapeHtml(plugin.category || 'Uncategorized')}</span>
                <span style="font-size: 0.8rem; color: var(--text-muted);">v${escapeHtml(plugin.version || 'N/A')}</span>
            </div>
            ${renderTagBadges(plugin.tags)}
            <div class="plugin-card-actions">
                <button class="btn-action" onclick="showPluginDetails('${escapeHtml(plugin.name)}')">Details</button>
                <button class="btn-action" onclick="testPlugin('${escapeHtml(plugin.name)}')">Test</button>
                <button class="btn-action btn-danger" onclick="deletePlugin('${escapeHtml(plugin.name)}')">Remove</button>
            </div>
        </div>
    `).join('');

    // Add checkbox event listeners for cards
    container.querySelectorAll('.plugin-checkbox').forEach(checkbox => {
        checkbox.addEventListener('change', handlePluginCheckbox);
    });
}

// HTML escape helper
function escapeHtml(str) {
    if (!str) return '';
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
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
    } else if (plugin.is_configured === false) {
        return `
            <span class="status-badge status-not-configured">
                <span class="status-dot"></span>
                Not Configured
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

    // Apply sorting
    filteredPlugins = sortPlugins(filteredPlugins);

    // Reset to first page when filtering
    currentPage = 1;

    // Update pagination and render
    updatePagination();
    renderPluginsTable();
    renderMobileCards();
}

function getPluginStatus(plugin) {
    if (plugin.health_status === 'error') return 'error';
    if (plugin.needs_update) return 'update';
    if (plugin.is_configured === false) return 'not-configured';
    return 'healthy';
}

function handleSelectAll(e) {
    const checkboxes = document.querySelectorAll('.plugin-checkbox');
    checkboxes.forEach(cb => {
        cb.checked = e.target.checked;
        const pluginName = cb.dataset.plugin;
        if (e.target.checked) {
            selectedPlugins.add(pluginName);
        } else {
            selectedPlugins.delete(pluginName);
        }
    });
    updateBulkActionsBar();
}

function handlePluginCheckbox(e) {
    const pluginName = e.target.dataset.plugin;
    if (e.target.checked) {
        selectedPlugins.add(pluginName);
    } else {
        selectedPlugins.delete(pluginName);
    }
    updateSelectAllState();
    updateBulkActionsBar();
}

function updateSelectAllState() {
    const selectAll = document.getElementById('selectAll');
    const checkboxes = document.querySelectorAll('.plugin-checkbox');
    const checkedCount = document.querySelectorAll('.plugin-checkbox:checked').length;

    if (checkboxes.length === 0) {
        selectAll.checked = false;
        selectAll.indeterminate = false;
    } else if (checkedCount === 0) {
        selectAll.checked = false;
        selectAll.indeterminate = false;
    } else if (checkedCount === checkboxes.length) {
        selectAll.checked = true;
        selectAll.indeterminate = false;
    } else {
        selectAll.checked = false;
        selectAll.indeterminate = true;
    }
}

function updateBulkActionsBar() {
    const bar = document.getElementById('bulkActionsBar');
    const countSpan = document.getElementById('selectedCount');

    if (selectedPlugins.size > 0) {
        bar.classList.add('active');
        countSpan.textContent = selectedPlugins.size;
    } else {
        bar.classList.remove('active');
    }
}

// Bulk action functions
async function bulkDeletePlugins() {
    if (selectedPlugins.size === 0) {
        showToast('No plugins selected', 'warning');
        return;
    }

    const count = selectedPlugins.size;
    if (!confirm(`Are you sure you want to delete ${count} plugin${count > 1 ? 's' : ''}? This action cannot be undone.`)) {
        return;
    }

    let successCount = 0;
    let failCount = 0;

    for (const pluginName of selectedPlugins) {
        try {
            const response = await fetch(`/api/plugins/${pluginName}`, {
                method: 'DELETE'
            });
            const data = await response.json();

            if (data.success) {
                successCount++;
            } else {
                failCount++;
            }
        } catch (error) {
            console.error(`Failed to delete plugin ${pluginName}:`, error);
            failCount++;
        }
    }

    selectedPlugins.clear();
    updateBulkActionsBar();

    if (successCount > 0) {
        showToast(`Deleted ${successCount} plugin${successCount > 1 ? 's' : ''}`, 'success');
    }
    if (failCount > 0) {
        showToast(`Failed to delete ${failCount} plugin${failCount > 1 ? 's' : ''}`, 'error');
    }

    await loadPlugins();
}

async function bulkUpdatePlugins() {
    if (selectedPlugins.size === 0) {
        showToast('No plugins selected', 'warning');
        return;
    }

    // Filter to only plugins that need updates
    const pluginsToUpdate = [...selectedPlugins].filter(name => {
        const plugin = allPlugins.find(p => p.name === name);
        return plugin && plugin.needs_update;
    });

    if (pluginsToUpdate.length === 0) {
        showToast('No selected plugins need updates', 'info');
        return;
    }

    let successCount = 0;
    let failCount = 0;

    for (const pluginName of pluginsToUpdate) {
        try {
            const response = await fetch(`/api/plugins/${pluginName}/update`, {
                method: 'POST'
            });
            const data = await response.json();

            if (data.success) {
                successCount++;
            } else {
                failCount++;
            }
        } catch (error) {
            console.error(`Failed to update plugin ${pluginName}:`, error);
            failCount++;
        }
    }

    selectedPlugins.clear();
    updateBulkActionsBar();

    if (successCount > 0) {
        showToast(`Updated ${successCount} plugin${successCount > 1 ? 's' : ''}`, 'success');
    }
    if (failCount > 0) {
        showToast(`Failed to update ${failCount} plugin${failCount > 1 ? 's' : ''}`, 'error');
    }

    await loadPlugins();
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

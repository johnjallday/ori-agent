// Plugin Management Page JavaScript

// State management
let allPlugins = [];
let filteredPlugins = [];
let paginatedPlugins = [];
let notifications = [];
const selectedPlugins = new Set();

// Pagination state
let currentPage = 1;
let pageSize = 25;
let totalPages = 1;

// Sorting state
let sortColumn = 'name';
let sortDirection = 'asc';

function getDisplayName(plugin) {
  return plugin.metadata?.name || stripVersionSuffix(plugin.name || '');
}

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
      const currentAgent = data.agent || 'Assistant';
      const agentDisplay = document.getElementById('currentAgentNameDisplay');
      if (agentDisplay) {
        agentDisplay.textContent = currentAgent;
      }
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

async function showPluginTools(pluginName) {
  console.log('showPluginTools called for', pluginName);
  try {
    const response = await fetch(`/api/plugins/${pluginName}`);
    const plugin = await response.json();
    console.log('Fetched plugin for tools modal:', plugin);

    if (!plugin.installed) {
         console.log('Returning early: plugin not installed');
         showToast('Plugin must be installed to view tools', 'warning');
         return;
    }
    
    if (!plugin.definition) {
        console.log('Returning early: no definition');
        showToast('No tool definition available for this plugin', 'warning');
        return;
    }

    const modal = createToolsModal(plugin);
    console.log('Modal created, appending to body...');
    document.body.appendChild(modal);
    console.log('Modal appended, body length:', document.body.children.length);
    showModal(modal);
  } catch (error) {
    console.error('Failed to load plugin tools:', error);
    showToast('Failed to load plugin tools', 'error');
  }
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
  return 2;
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
  const endPage = Math.min(totalPages, startPage + maxVisible - 1);

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

// Rendering functions
function renderPluginsTable() {
  console.log('Rendering plugins table with', paginatedPlugins.length, 'plugins');
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

  tbody.innerHTML = paginatedPlugins.map(plugin => {
    console.log(`Plugin ${plugin.name}: installed=${plugin.installed}, enabled=${plugin.enabled}`);
    return `
        <tr data-plugin="${plugin.name}">
            <td>
                <input type="checkbox" class="plugin-checkbox" data-plugin="${plugin.name}" ${selectedPlugins.has(plugin.name) ? 'checked' : ''} aria-label="Select ${plugin.name}">
            </td>
            <td>
                <div class="plugin-name-cell">
                    <div>
                        <div class="plugin-name">
                            ${escapeHtml(getDisplayName(plugin))}
                            ${plugin.supports_initialization ? 
                                '<span class="badge bg-info-subtle text-info ms-2" style="font-size: 0.7em;">Configurable</span>' : ''}
                        </div>
                        <div class="plugin-description">${escapeHtml(plugin.description || 'No description')}</div>
                        ${renderTagBadges(plugin.tags)}
                    </div>
                </div>
            </td>
            <td>${escapeHtml(plugin.version || 'N/A')}</td>
            <td>
                <div class="agent-list">
                    ${plugin.agents && plugin.agents.length > 0 ? 
                        plugin.agents.map(a => `<span class="agent-tag">${escapeHtml(a)}</span>`).join('') : 
                        '<span style="color: var(--text-muted); font-size: 0.8rem;">None</span>'}
                </div>
            </td>
            <td>${renderStatusBadge(plugin)}</td>
            <td><span class="category-badge">${escapeHtml(plugin.category || 'Uncategorized')}</span></td>
            <td>
                <div class="action-buttons">
                    <button class="btn-action" onclick="showPluginDetails('${escapeHtml(plugin.name)}')">Details</button>
                    <button class="btn-action" onclick="showPluginTools('${escapeHtml(plugin.name)}')" ${plugin.installed ? '' : 'disabled style="opacity: 0.5; cursor: not-allowed;" title="Install plugin to view tools"'}>Tools</button>
                    <button class="btn-action btn-danger" onclick="deletePlugin('${escapeHtml(plugin.name)}')">Remove</button>
                </div>
            </td>
        </tr>
    `;
  }).join('');

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
                <div class="plugin-card-info">
                    <div class="plugin-card-name">
                        ${escapeHtml(getDisplayName(plugin))}
                        ${plugin.supports_initialization ? 
                            '<span class="badge bg-info-subtle text-info ms-2" style="font-size: 0.7em;">Configurable</span>' : ''}
                    </div>
                    <div class="plugin-card-description">${escapeHtml(plugin.description || 'No description')}</div>
                </div>
            </div>
            <div class="plugin-card-meta">
                ${renderStatusBadge(plugin)}
                <span class="category-badge">${escapeHtml(plugin.category || 'Uncategorized')}</span>
                <span style="font-size: 0.8rem; color: var(--text-muted);">v${escapeHtml(plugin.version || 'N/A')}</span>
            </div>
            <div class="mb-2">
                <div class="agent-list">
                    ${plugin.agents && plugin.agents.length > 0 ? 
                        plugin.agents.map(a => `<span class="agent-tag">${escapeHtml(a)}</span>`).join('') : 
                        '<span style="color: var(--text-muted); font-size: 0.8rem;">No agents using this plugin</span>'}
                </div>
            </div>
            ${renderTagBadges(plugin.tags)}
            <div class="plugin-card-actions">
                <button class="btn-action" onclick="showPluginDetails('${escapeHtml(plugin.name)}')">Details</button>
                <button class="btn-action" onclick="showPluginTools('${escapeHtml(plugin.name)}')" ${plugin.installed ? '' : 'disabled style="opacity: 0.5; cursor: not-allowed;" title="Install plugin to view tools"'}>Tools</button>
                <button class="btn-action btn-danger" onclick="deletePlugin('${escapeHtml(plugin.name)}')">Remove</button>
            </div>
        </div>
    `).join('');

  // Add checkbox event listeners for cards
  container.querySelectorAll('.plugin-checkbox').forEach(checkbox => {
    checkbox.addEventListener('change', handlePluginCheckbox);
  });
}

// escapeHtml is provided by dom-utils.js

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
  // Show status badge based on plugin health and installation
  if (plugin.health_status === 'error') {
    return `
            <span class="status-badge status-error">
                <span class="status-dot"></span>
                Error
            </span>
        `;
  }

  if (plugin.installed) {
    return `
        <span class="status-badge status-active">
            <span class="status-dot"></span>
            Installed
        </span>
    `;
  }

  return `
        <span class="status-badge status-inactive">
            <span class="status-dot"></span>
            Available
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
function createToolsModal(plugin) {
  console.log('createToolsModal starting for', plugin.name);
  const definition = plugin.definition;
  const operations = plugin.operations;
  const modal = document.createElement('div');
  modal.className = 'modal-overlay';
  
  let toolsHtml = '';
  
  // 1. Check for modern explicit operations list (OperationInfo format)
  if (Array.isArray(operations) && operations.length > 0) {
      toolsHtml += `
        <div style="margin-bottom: 2rem;">
            <h3 style="font-size: 1.1rem; margin-bottom: 1rem; color: var(--primary-color); display: flex; align-items: center; gap: 8px;">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor"><path d="M12,15.5A3.5,3.5 0 0,1 8.5,12A3.5,3.5 0 0,1 12,8.5A3.5,3.5 0 0,1 15.5,12A3.5,3.5 0 0,1 12,15.5M19.43,12.97C19.47,12.65 19.5,12.33 19.5,12C19.5,11.67 19.47,11.34 19.43,11L21.54,9.37C21.73,9.22 21.78,8.95 21.66,8.73L19.66,5.27C19.54,5.05 19.27,4.96 19.05,5.05L16.56,6.05C16.04,5.66 15.5,5.32 14.87,5.07L14.5,2.42C14.46,2.18 14.25,2 14,2H10C9.75,2 9.54,2.18 9.5,2.42L9.13,5.07C8.5,5.32 7.96,5.66 7.44,6.05L4.95,5.05C4.73,4.96 4.46,5.05 4.34,5.27L2.34,8.73C2.22,8.95 2.27,9.22 2.46,9.37L4.57,11C4.53,11.34 4.5,11.67 4.5,12C4.5,12.33 4.53,12.65 4.57,12.97L2.46,14.63C2.27,14.78 2.22,15.05 2.34,15.27L4.34,18.73C4.46,18.95 4.73,19.03 4.95,18.95L7.44,17.94C7.96,18.34 8.5,18.68 9.13,18.93L9.5,21.58C9.54,21.82 9.75,22 10,22H14C14.25,22 14.46,21.82 14.5,21.58L14.87,18.93C15.5,18.68 16.04,18.34 16.56,17.94L19.05,18.95C19.27,19.03 19.54,18.95 19.66,18.73L21.66,15.27C21.78,15.05 21.73,14.78 21.54,14.63L19.43,12.97Z"/></svg>
                Available Operations
            </h3>
            <div style="display: flex; flex-direction: column; gap: 12px;">
                ${operations.map(op => {
                    const opName = op.name || op.Name || 'Unknown';
                    const opDesc = op.description || op.Description || 'Operation: ' + opName;
                    const params = op.parameters || op.Parameters || [];
                    const requiredParams = op.required_parameters || op.RequiredParameters || [];
                    return `
                    <div style="background: var(--bg-secondary); border: 1px solid var(--border-color); border-radius: var(--radius-md); padding: 1rem;">
                        <div style="display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 0.5rem;">
                            <code style="font-weight: 600; color: var(--primary-color); font-size: 1rem;">${escapeHtml(opName)}</code>
                        </div>
                        <p style="color: var(--text-primary); margin-bottom: 0.75rem; font-size: 0.9rem;">${escapeHtml(opDesc)}</p>
                        
                        ${params.length > 0 ? `
                            <div style="margin-top: 1rem;">
                                <span style="font-size: 0.75rem; font-weight: 600; text-transform: uppercase; color: var(--text-muted); letter-spacing: 0.05em;">Parameters</span>
                                <div style="display: grid; grid-template-columns: 1fr; gap: 8px; margin-top: 0.5rem;">
                                    ${params.map(paramName => {
                                        const isRequired = requiredParams.includes(paramName);
                                        // Look up parameter details in the main properties map
                                        const props = definition.parameters?.properties || {};
                                        const detail = props[paramName] || {};
                                        return `
                                        <div style="display: flex; align-items: baseline; gap: 12px; font-size: 0.85rem; padding: 4px 8px; background: var(--bg-primary); border-radius: 4px; border-left: 3px solid ${isRequired ? 'var(--danger-color)' : 'var(--border-color)'};">
                                            <code style="color: var(--text-primary); font-weight: 600;">${escapeHtml(paramName)}</code>
                                            <span style="color: var(--text-muted); font-style: italic;">(${escapeHtml(detail.type || 'string')})</span>
                                            <span style="color: var(--text-secondary); flex: 1;">${escapeHtml(detail.description || '')}</span>
                                            ${isRequired ? '<span class="badge bg-danger-subtle text-danger" style="font-size: 0.65rem;">Required</span>' : ''}
                                        </div>
                                        `;
                                    }).join('')}
                                </div>
                            </div>
                        ` : ''}
                    </div>
                    `;
                }).join('')}
            </div>
        </div>
      `;
  }
  // 2. Check for operations map in definition (internal format)
  else if (definition.operations && Object.keys(definition.operations).length > 0) {
      toolsHtml += `
        <div style="margin-bottom: 2rem;">
            <h3 style="font-size: 1.1rem; margin-bottom: 1rem; color: var(--primary-color); display: flex; align-items: center; gap: 8px;">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor"><path d="M12,15.5A3.5,3.5 0 0,1 8.5,12A3.5,3.5 0 0,1 12,8.5A3.5,3.5 0 0,1 15.5,12A3.5,3.5 0 0,1 12,15.5M19.43,12.97C19.47,12.65 19.5,12.33 19.5,12C19.5,11.67 19.47,11.34 19.43,11L21.54,9.37C21.73,9.22 21.78,8.95 21.66,8.73L19.66,5.27C19.54,5.05 19.27,4.96 19.05,5.05L16.56,6.05C16.04,5.66 15.5,5.32 14.87,5.07L14.5,2.42C14.46,2.18 14.25,2 14,2H10C9.75,2 9.54,2.18 9.5,2.42L9.13,5.07C8.5,5.32 7.96,5.66 7.44,6.05L4.95,5.05C4.73,4.96 4.46,5.05 4.34,5.27L2.34,8.73C2.22,8.95 2.27,9.22 2.46,9.37L4.57,11C4.53,11.34 4.5,11.67 4.5,12C4.5,12.33 4.53,12.65 4.57,12.97L2.46,14.63C2.27,14.78 2.22,15.05 2.34,15.27L4.34,18.73C4.46,18.95 4.73,19.03 4.95,18.95L7.44,17.94C7.96,18.34 8.5,18.68 9.13,18.93L9.5,21.58C9.54,21.82 9.75,22 10,22H14C14.25,22 14.46,21.82 14.5,21.58L14.87,18.93C15.5,18.68 16.04,18.34 16.56,17.94L19.05,18.95C19.27,19.03 19.54,18.95 19.66,18.73L21.66,15.27C21.78,15.05 21.73,14.78 21.54,14.63L19.43,12.97Z"/></svg>
                Available Operations
            </h3>
            <div style="display: flex; flex-direction: column; gap: 12px;">
                ${Object.entries(definition.operations).map(([name, op]) => `
                    <div style="background: var(--bg-secondary); border: 1px solid var(--border-color); border-radius: var(--radius-md); padding: 1rem;">
                        <div style="display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: 0.5rem;">
                            <code style="font-weight: 600; color: var(--primary-color); font-size: 1rem;">${escapeHtml(name)}</code>
                        </div>
                        <p style="color: var(--text-primary); margin-bottom: 0.75rem; font-size: 0.9rem;">${escapeHtml(op.description || 'No description available')}</p>
                        
                        ${op.parameters && op.parameters.length > 0 ? `
                            <div style="margin-top: 1rem;">
                                <span style="font-size: 0.75rem; font-weight: 600; text-transform: uppercase; color: var(--text-muted); letter-spacing: 0.05em;">Parameters</span>
                                <div style="display: grid; grid-template-columns: 1fr; gap: 8px; margin-top: 0.5rem;">
                                    ${op.parameters.map(param => `
                                        <div style="display: flex; align-items: baseline; gap: 12px; font-size: 0.85rem; padding: 4px 8px; background: var(--bg-primary); border-radius: 4px; border-left: 3px solid ${param.required ? 'var(--danger-color)' : 'var(--border-color)'};">
                                            <code style="color: var(--text-primary); font-weight: 600;">${escapeHtml(param.name)}</code>
                                            <span style="color: var(--text-muted); font-style: italic;">(${escapeHtml(param.type)})</span>
                                            <span style="color: var(--text-secondary); flex: 1;">${escapeHtml(param.description || '')}</span>
                                            ${param.required ? '<span class="badge bg-danger-subtle text-danger" style="font-size: 0.65rem;">Required</span>' : ''}
                                        </div>
                                    `).join('')}
                                </div>
                            </div>
                        ` : ''}
                    </div>
                `).join('')}
            </div>
        </div>
      `;
  }
  // 3. Check for legacy format (definition.parameters list)
  else if (definition.parameters && definition.parameters.length > 0) {
      toolsHtml += `
        <div style="margin-bottom: 2rem;">
            <h3 style="font-size: 1.1rem; margin-bottom: 1rem; color: var(--primary-color); display: flex; align-items: center; gap: 8px;">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor"><path d="M12,15.5A3.5,3.5 0 0,1 8.5,12A3.5,3.5 0 0,1 12,8.5A3.5,3.5 0 0,1 15.5,12A3.5,3.5 0 0,1 12,15.5M19.43,12.97C19.47,12.65 19.5,12.33 19.5,12C19.5,11.67 19.47,11.34 19.43,11L21.54,9.37C21.73,9.22 21.78,8.95 21.66,8.73L19.66,5.27C19.54,5.05 19.27,4.96 19.05,5.05L16.56,6.05C16.04,5.66 15.5,5.32 14.87,5.07L14.5,2.42C14.46,2.18 14.25,2 14,2H10C9.75,2 9.54,2.18 9.5,2.42L9.13,5.07C8.5,5.32 7.96,5.66 7.44,6.05L4.95,5.05C4.73,4.96 4.46,5.05 4.34,5.27L2.34,8.73C2.22,8.95 2.27,9.22 2.46,9.37L4.57,11C4.53,11.34 4.5,11.67 4.5,12C4.5,12.33 4.53,12.65 4.57,12.97L2.46,14.63C2.27,14.78 2.22,15.05 2.34,15.27L4.34,18.73C4.46,18.95 4.73,19.03 4.95,18.95L7.44,17.94C7.96,18.34 8.5,18.68 9.13,18.93L9.5,21.58C9.54,21.82 9.75,22 10,22H14C14.25,22 14.46,21.82 14.5,21.58L14.87,18.93C15.5,18.68 16.04,18.34 16.56,17.94L19.05,18.95C19.27,19.03 19.54,18.95 19.66,18.73L21.66,15.27C21.78,15.05 21.73,14.78 21.54,14.63L19.43,12.97Z"/></svg>
                Unified Tool Parameters
            </h3>
            <div style="background: var(--bg-secondary); border: 1px solid var(--border-color); border-radius: var(--radius-md); padding: 1rem;">
                <p style="color: var(--text-primary); margin-bottom: 1rem; font-size: 0.9rem;">This tool uses a unified parameter set for all operations.</p>
                <div style="display: grid; grid-template-columns: 1fr; gap: 8px;">
                    ${definition.parameters.map(param => `
                        <div style="display: flex; align-items: baseline; gap: 12px; font-size: 0.85rem; padding: 4px 8px; background: var(--bg-primary); border-radius: 4px; border-left: 3px solid ${param.required ? 'var(--danger-color)' : 'var(--border-color)'};">
                            <code style="color: var(--text-primary); font-weight: 600;">${escapeHtml(param.name)}</code>
                            <span style="color: var(--text-muted); font-style: italic;">(${escapeHtml(param.type)})</span>
                            <span style="color: var(--text-secondary); flex: 1;">${escapeHtml(param.description || '')}</span>
                            ${param.required ? '<span class="badge bg-danger-subtle text-danger" style="font-size: 0.65rem;">Required</span>' : ''}
                        </div>
                    `).join('')}
                </div>
            </div>
        </div>
      `;
  }
  else {
      // Fallback for simple tools without explicit operations
      toolsHtml += `
        <div style="padding: 2rem; text-align: center; background: var(--bg-secondary); border-radius: var(--radius-lg); border: 1px dashed var(--border-color);">
            <p style="color: var(--text-muted); margin: 0;">This plugin provides a single unified tool capability.</p>
        </div>
      `;
  }

  modal.innerHTML = `
      <div class="modal-content" style="max-width: 850px; width: 95%;">
          <div class="modal-header" style="background: var(--bg-secondary);">
              <div style="display: flex; flex-direction: column;">
                <h2 class="modal-title">Tools: ${escapeHtml(getDisplayName(plugin))}</h2>
                <span style="font-size: 0.85rem; color: var(--text-muted);">Inspect available operations and their parameters</span>
              </div>
              <button class="modal-close" onclick="closeModal(this)">&times;</button>
          </div>
          <div class="modal-body" style="padding: 2rem;">
            <div style="margin-bottom: 2rem; padding-bottom: 1.5rem; border-bottom: 1px solid var(--border-color);">
                <h3 style="font-size: 0.85rem; text-transform: uppercase; letter-spacing: 0.05em; color: var(--text-muted); margin-bottom: 0.5rem;">Plugin Description</h3>
                <p style="color: var(--text-primary); font-size: 1rem; line-height: 1.5;">${escapeHtml(definition.description || plugin.description || 'No description available')}</p>
            </div>
            ${toolsHtml}
          </div>
          <div class="modal-footer">
              <button class="modern-btn modern-btn-secondary" onclick="closeModal(this)">Close</button>
          </div>
      </div>
  `;
  return modal;
}

function createDetailsModal(plugin) {
  const modal = document.createElement('div');
  modal.className = 'modal-overlay';
  
  // Extract repository URL from metadata or github_repo field
  let repoUrl = plugin.metadata?.repository || plugin.github_repo || '';
  if (repoUrl && !repoUrl.startsWith('http') && repoUrl.includes('/')) {
      // Convert user/repo to full github URL
      repoUrl = `https://github.com/${repoUrl}`;
  }

      modal.innerHTML = `
          <div class="modal-content">
              <div class="modal-header">
                  <h2 class="modal-title">${escapeHtml(getDisplayName(plugin))}</h2>
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
                        ${repoUrl ? `
                            <span style="color: var(--text-muted);">Repository:</span>
                            <a href="${escapeAttr(repoUrl)}" target="_blank" style="color: var(--primary-color); text-decoration: none; word-break: break-all;">
                                ${escapeHtml(repoUrl)}
                                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="margin-left: 4px; vertical-align: middle;">
                                    <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"></path>
                                    <polyline points="15 3 21 3 21 8"></polyline>
                                    <line x1="10" y1="14" x2="21" y2="3"></line>
                                </svg>
                            </a>
                        ` : ''}
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
                <button class="modern-btn modern-btn-secondary" onclick="closeModal(this)">Close</button>
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
                <h2 class="modal-title">Test Plugin: ${escapeHtml(stripVersionSuffix(pluginName))}</h2>
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
                <button class="modern-btn modern-btn-secondary" onclick="closeModal(this)">Close</button>
                <button class="modern-btn modern-btn-primary" onclick="executeTest('${pluginName}')">Run Test</button>
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
  console.log('showModal called for', modal);
  setTimeout(() => {
    modal.classList.add('active');
    console.log('modal "active" class added, visibility should change now');
  }, 10);

  const handleEsc = (e) => {
    if (e.key === 'Escape') {
      cleanupAndClose(modal);
    }
  };

  modal._escHandler = handleEsc;

  modal.addEventListener('click', (e) => {
    if (e.target === modal) {
      cleanupAndClose(modal);
    }
  });

  document.addEventListener('keydown', handleEsc);
}

function cleanupAndClose(modal) {
  if (modal._escHandler) {
    document.removeEventListener('keydown', modal._escHandler);
  }
  modal.classList.remove('active');
  setTimeout(() => modal.remove(), 300);
}

function closeModal(closeBtn) {
  const modal = closeBtn.closest('.modal-overlay');
  cleanupAndClose(modal);
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

/**
 * Workspace Management Module
 * Handles workspace CRUD operations, agent management, and UI interactions for the Workspaces page
 */
/* global loadCanvasWorkspace */

// Global state - use window object for shared state with other modules
// These are initialized in workspaces-hub.tmpl inline script
let workspaceRefreshInterval = null;
let hasLoadedWorkspaces = false;

// Server connection state management
let isServerConnected = true;
let consecutiveFailures = 0;
let retryDelay = 5000; // Start with 5 seconds
const MAX_RETRY_DELAY = 60000; // Max 60 seconds between retries
const MAX_CONSECUTIVE_FAILURES = 3; // After 3 failures, show offline notification
let serverOfflineNotification = null;

// Workspace-specific state for agent management
// Note: Using let instead of const so workspace-agent-modals.js can reassign this array
let workspaceSystemAgents = [];

// Last successfully loaded workspace list + the shared tag filter bar over it.
// The filter is plain UI state and intentionally resets on reload.
let loadedWorkspaces = [];
let workspaceTagFilterBar = null;
// workspaceAvailableProviders is declared in workspace-agent-modals.js

/**
 * Initialize the workspaces page
 */
function initializeWorkspacesPage() {
  loadWorkspaces({ showLoading: true });
  loadWorkspaceAgents();

  // Check URL parameters for view and workspace
  const urlParams = new URLSearchParams(window.location.search);
  const view = urlParams.get('view');
  const workspaceId = urlParams.get('workspace');

  if (view === 'canvas') {
    // Switch to canvas view
    switchView('canvas');

    // If workspace ID is provided, select it after workspaces are loaded
    if (workspaceId) {
      // Hide the "Select Workspace:" label since workspace is already selected
      const label = document.getElementById('canvas-workspace-label');
      if (label) {
        label.style.display = 'none';
      }

      // Wait a bit for the select to be populated
      setTimeout(() => {
        const select = document.getElementById('canvas-workspace-select');
        if (select) {
          select.value = workspaceId;
          loadCanvasWorkspace(workspaceId);
        }
      }, 500);
    }
  }

  // Enable auto-refresh
  startWorkspacePolling();
}

/**
 * Cleanup on page unload
 */
function cleanupWorkspacesPage() {
  stopWorkspacePolling();
}

/**
 * Start automatic workspace polling
 */
function startWorkspacePolling() {
  if (workspaceRefreshInterval) {
    clearInterval(workspaceRefreshInterval);
  }
  workspaceRefreshInterval = setInterval(() => {
    loadWorkspaces();
  }, 10000); // Refresh every 10 seconds
}

/**
 * Stop automatic workspace polling
 */
function stopWorkspacePolling() {
  if (workspaceRefreshInterval) {
    clearInterval(workspaceRefreshInterval);
    workspaceRefreshInterval = null;
  }
}

/**
 * Show server offline notification
 */
function showServerOfflineNotification() {
  // Remove existing notification if any
  hideServerOfflineNotification();

  // Create notification banner
  const notification = document.createElement('div');
  notification.id = 'server-offline-notification';
  notification.style.cssText = `
        position: fixed;
        top: 70px;
        left: 50%;
        transform: translateX(-50%);
        background: #dc3545;
        color: white;
        padding: 16px 24px;
        border-radius: 8px;
        box-shadow: 0 4px 12px rgba(0,0,0,0.2);
        z-index: 10000;
        display: flex;
        align-items: center;
        gap: 12px;
        animation: slideDown 0.3s ease;
    `;

  notification.innerHTML = `
        <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor">
            <path d="M12,2C17.53,2 22,6.47 22,12C22,17.53 17.53,22 12,22C6.47,22 2,17.53 2,12C2,6.47 6.47,2 12,2M15.59,7L12,10.59L8.41,7L7,8.41L10.59,12L7,15.59L8.41,17L12,13.41L15.59,17L17,15.59L13.41,12L17,8.41L15.59,7Z"/>
        </svg>
        <span><strong>Server Offline</strong> - Unable to connect to the server. Retrying automatically...</span>
        <button onclick="manualRetryConnection()" style="
            background: rgba(255,255,255,0.2);
            border: 1px solid rgba(255,255,255,0.3);
            color: white;
            padding: 6px 12px;
            border-radius: 4px;
            cursor: pointer;
            font-size: 14px;
            margin-left: 12px;
        ">Retry Now</button>
    `;

  // Add animation
  const style = document.createElement('style');
  style.textContent = `
        @keyframes slideDown {
            from {
                opacity: 0;
                transform: translateX(-50%) translateY(-20px);
            }
            to {
                opacity: 1;
                transform: translateX(-50%) translateY(0);
            }
        }
    `;
  document.head.appendChild(style);

  document.body.appendChild(notification);
  serverOfflineNotification = notification;
}

/**
 * Hide server offline notification
 */
function hideServerOfflineNotification() {
  if (serverOfflineNotification) {
    serverOfflineNotification.remove();
    serverOfflineNotification = null;
  }
}

/**
 * Handle connection failure
 */
function handleConnectionFailure() {
  consecutiveFailures++;

  if (consecutiveFailures >= MAX_CONSECUTIVE_FAILURES) {
    // Stop regular polling
    stopWorkspacePolling();

    // Show offline notification
    showServerOfflineNotification();
    isServerConnected = false;

    // Implement exponential backoff for retries
    retryDelay = Math.min(retryDelay * 1.5, MAX_RETRY_DELAY);

    // Schedule retry with exponential backoff
    setTimeout(() => {
      loadWorkspaces();
    }, retryDelay);
  }
}

/**
 * Handle connection success
 */
function handleConnectionSuccess() {
  if (!isServerConnected) {
    // Server is back online
    hideServerOfflineNotification();

    // Resume normal polling
    startWorkspacePolling();
  }

  // Reset failure tracking
  consecutiveFailures = 0;
  retryDelay = 5000;
  isServerConnected = true;
}

/**
 * Manual retry connection
 */
window.manualRetryConnection = async function() {
  hideServerOfflineNotification();

  await loadWorkspaces({ showLoading: true });
};

/**
 * Load workspaces from server (unified workspace API)
 */
async function loadWorkspaces(options = {}) {
  const { showLoading = false } = options;
  const isInitialLoad = showLoading || !hasLoadedWorkspaces;
  const controller = new AbortController();
  const timeoutId = setTimeout(() => controller.abort(), 10000); // 10 second timeout

  try {
    if (isInitialLoad) {
      renderWorkspacesLoadingState();
    }

    // Use unified workspace API (same as sessions sidebar)
    const response = await fetch('/api/workspaces', {
      signal: controller.signal
    });

    clearTimeout(timeoutId);

    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`);
    }

    const data = await response.json();

    if (data.error) {
      console.error('Server error:', data.error);
      if (isInitialLoad) renderWorkspacesErrorState();
      handleConnectionFailure();
      return;
    }

    // Connection successful
    handleConnectionSuccess();
    // Flat workspace list includes groups (kind === 'group').
    loadedWorkspaces = data.folders || [];
    renderFilteredWorkspaces();
    hasLoadedWorkspaces = true;

  } catch (error) {
    clearTimeout(timeoutId);

    // Silently ignore abort errors if server is connected
    if (error.name === 'AbortError' && isServerConnected) {
      console.warn('Request timed out, but server appears online');
      return;
    }

    console.error('Error loading workspaces:', error);

    if (isInitialLoad) renderWorkspacesErrorState();

    // Check if it's a network error (server offline)
    if (error.name === 'AbortError' || error.message.includes('Failed to fetch') || error.message.includes('NetworkError')) {
      handleConnectionFailure();
    } else {
      if (consecutiveFailures < MAX_CONSECUTIVE_FAILURES) {
        consecutiveFailures++;
      }
    }
  }
}

function renderWorkspacesLoadingState() {
  const grid = document.getElementById('workspaces-grid');
  if (!grid) return;

  const skeletons = Array.from({ length: 6 }).map(() => `
        <div class="col-12 col-sm-6 col-lg-4">
            <div class="modern-card p-4 h-100 workspace-card workspace-card-skeleton">
                <div class="d-flex justify-content-between align-items-start mb-3">
                    <div class="skeleton skeleton-heading" style="width: 60%;"></div>
                    <div class="skeleton" style="width: 72px; height: 20px;"></div>
                </div>
                <div class="skeleton skeleton-text"></div>
                <div class="skeleton skeleton-text" style="width: 80%;"></div>
                <div class="workspace-card-metrics">
                    <div class="skeleton" style="height: 18px;"></div>
                    <div class="skeleton" style="height: 18px;"></div>
                    <div class="skeleton" style="height: 18px;"></div>
                </div>
                <div class="workspace-card-actions mt-auto">
                    <div class="skeleton" style="height: 34px; flex: 1;"></div>
                    <div class="skeleton" style="height: 34px; flex: 1;"></div>
                    <div class="skeleton" style="height: 34px; width: 38px;"></div>
                </div>
            </div>
        </div>
    `).join('');

  grid.innerHTML = skeletons;
}

function renderWorkspacesErrorState() {
  const grid = document.getElementById('workspaces-grid');
  if (!grid) return;
  grid.innerHTML = `
        <div class="col-12">
            <div class="workspace-empty-card modern-card" role="alert">
                <div class="workspace-empty-content">
                    <span class="modern-badge badge-warning workspace-empty-badge">Connection issue</span>
                    <h3 class="workspace-empty-title">Couldn't load workspaces</h3>
                    <p class="workspace-empty-description">
                        Something went wrong reaching the server. Check that the agent is running and try again.
                    </p>
                    <div class="d-flex gap-2 justify-content-center mt-3">
                        <button type="button" class="btn btn-primary" data-action="workspaces-retry">
                            <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                                <path d="M17.65,6.35C16.2,4.9 14.21,4 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20C15.73,20 18.84,17.45 19.73,14H17.65C16.83,16.33 14.61,18 12,18A6,6 0 0,1 6,12A6,6 0 0,1 12,6C13.66,6 15.14,6.69 16.22,7.78L13,11H20V4L17.65,6.35Z"/>
                            </svg>
                            Retry
                        </button>
                    </div>
                </div>
            </div>
        </div>
    `;
  const retryBtn = grid.querySelector('[data-action="workspaces-retry"]');
  if (retryBtn) {
    retryBtn.addEventListener('click', () => {
      if (typeof window.manualRetryConnection === 'function') {
        window.manualRetryConnection();
      } else {
        loadWorkspaces({ showLoading: true });
      }
    });
  }
}

function renderWorkspacesEmptyState() {
  return `
        <div class="col-12">
            <div class="workspace-empty-card modern-card">
                <div class="workspace-empty-content">
                    <span class="modern-badge badge-secondary workspace-empty-badge">Get started</span>
                    <h3 class="workspace-empty-title">No workspaces yet</h3>
                    <p class="workspace-empty-description">
                        Create your first workspace to organize agents, sessions, and notes in one place.
                    </p>
                </div>
                <div class="workspace-empty-features">
                    <div class="workspace-empty-feature">
                        <span class="workspace-empty-dot"></span>
                        <div>
                            <div class="workspace-empty-feature-title">Shared canvas</div>
                            <div class="workspace-empty-feature-text">See agents collaborate in real time.</div>
                        </div>
                    </div>
                    <div class="workspace-empty-feature">
                        <span class="workspace-empty-dot"></span>
                        <div>
                            <div class="workspace-empty-feature-title">Structured context</div>
                            <div class="workspace-empty-feature-text">Keep tasks, notes, and sessions together.</div>
                        </div>
                    </div>
                    <div class="workspace-empty-feature">
                        <span class="workspace-empty-dot"></span>
                        <div>
                            <div class="workspace-empty-feature-title">Quick actions</div>
                            <div class="workspace-empty-feature-text">Jump into canvas or dashboard instantly.</div>
                        </div>
                    </div>
                </div>
            </div>
        </div>
    `;
}

function getWorkspaceStatusMeta(status) {
  const normalized = (status || '').toString().toLowerCase();
  if (normalized === 'active') {
    return { label: 'Active', badgeClass: 'badge-success', indicatorClass: 'status-online', isActive: true };
  }
  if (normalized === 'completed') {
    return { label: 'Completed', badgeClass: 'badge-info', indicatorClass: '', isActive: false };
  }
  if (normalized === 'failed') {
    return { label: 'Failed', badgeClass: 'badge-danger', indicatorClass: '', isActive: false };
  }
  if (normalized === 'paused') {
    return { label: 'Paused', badgeClass: 'badge-warning', indicatorClass: '', isActive: false };
  }
  const label = normalized ? normalized.charAt(0).toUpperCase() + normalized.slice(1) : 'Idle';
  return { label, badgeClass: 'badge-secondary', indicatorClass: '', isActive: false };
}

function sanitizeWorkspaceColor(color) {
  if (!color || typeof color !== 'string') return '';
  const trimmed = color.trim();
  if (/^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.test(trimmed)) {
    return trimmed;
  }
  return '';
}

function formatRelativeTime(dateString) {
  if (!dateString) return '';
  const date = new Date(dateString);
  if (Number.isNaN(date.getTime())) return '';

  const diffSeconds = Math.floor((Date.now() - date.getTime()) / 1000);
  if (diffSeconds < 60) return 'just now';
  if (diffSeconds < 3600) return `${Math.floor(diffSeconds / 60)}m ago`;
  if (diffSeconds < 86400) return `${Math.floor(diffSeconds / 3600)}h ago`;
  if (diffSeconds < 604800) return `${Math.floor(diffSeconds / 86400)}d ago`;
  if (diffSeconds < 2592000) return `${Math.floor(diffSeconds / 604800)}w ago`;

  return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
}

function formatDateTime(dateString) {
  if (!dateString) return '';
  const date = new Date(dateString);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleString('en-US', {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    hour: 'numeric',
    minute: '2-digit'
  });
}

/**
 * Render workspaces grid
 */
function renderWorkspaces(workspaces) {
  const grid = document.getElementById('workspaces-grid');
  if (!grid) return;

  if (!Array.isArray(workspaces) || workspaces.length === 0) {
    grid.innerHTML = renderWorkspacesEmptyState();
    return;
  }

  grid.innerHTML = workspaces.map(renderWorkspaceCard).join('');
}

/**
 * Render the loaded workspaces through the shared tag filter bar. The bar
 * only offers tags that appear on the listed workspaces and AND-matches
 * the selected ones (same behavior as the hub launcher filter).
 */
function renderFilteredWorkspaces() {
  const filterBar = ensureWorkspaceTagFilterBar();
  if (!filterBar) {
    renderWorkspaces(loadedWorkspaces);
    return;
  }
  filterBar.setAvailableTags(window.OriTagFilterBar.collectTags(loadedWorkspaces));
  renderWorkspaces(window.OriTagFilterBar.filterItems(loadedWorkspaces, filterBar.getActiveTags()));
}

function ensureWorkspaceTagFilterBar() {
  if (workspaceTagFilterBar) return workspaceTagFilterBar;
  const mount = document.getElementById('workspaces-tag-filter');
  if (!mount || !window.OriTagFilterBar?.createTagFilterBar) return null;
  workspaceTagFilterBar = window.OriTagFilterBar.createTagFilterBar({
    container: mount,
    onChange: () => renderFilteredWorkspaces()
  });
  return workspaceTagFilterBar;
}

function renderWorkspaceCard(workspace) {
  const statusMeta = getWorkspaceStatusMeta(workspace.status);
  const safeColor = sanitizeWorkspaceColor(workspace.color);
  const activityDate = workspace.updated_at || workspace.created_at;
  const relativeActivity = activityDate ? formatRelativeTime(activityDate) : '';
  const activityLabel = relativeActivity ? `Updated ${relativeActivity}` : 'Activity unknown';
  const activityTitle = activityDate ? formatDateTime(activityDate) : '';
  const description = workspace.description ? escapeHtml(workspace.description) : 'No description provided yet.';
  const tasksCount = workspace.task_count || 0;
  const sessionsCount = workspace.session_count || 0;
  const notesCount = workspace.note_count || 0;
  const tags = Array.isArray(workspace.tags) ? workspace.tags.filter((tag) => String(tag || '').trim()) : [];
  const tagsMarkup = tags.length > 0
    ? `<div class="workspace-card-tags">${tags.map((tag) => `<span class="workspace-card-tag" title="${escapeHtml(tag)}">${escapeHtml(tag)}</span>`).join('')}</div>`
    : '';

  return `
        <div class="col-12 col-sm-6 col-lg-4">
            <div class="modern-card p-4 h-100 d-flex flex-column workspace-card ${statusMeta.isActive ? 'active-workspace' : ''}"
                 role="button"
                 tabindex="0"
                 aria-label="Open workspace ${escapeHtml(workspace.name || workspace.id)}"
                 onclick="viewWorkspace('${workspace.id}')"
                 onkeydown="if ((event.key === 'Enter' || event.key === ' ') && !event.target.closest('button, a, input, select, textarea')) { event.preventDefault(); viewWorkspace('${workspace.id}'); }"
                 style="cursor: pointer; transition: background-color 0.2s ease, border-color 0.2s ease, box-shadow 0.2s ease, transform 0.2s ease;">
                <div class="workspace-card-header">
                    <div class="workspace-card-title">
                        <span class="workspace-card-dot" ${safeColor ? `style="background: ${safeColor}; border-color: ${safeColor};"` : ''}></span>
                        <div>
                            <h5 class="workspace-card-name">${escapeHtml(workspace.name || workspace.id)}</h5>
                            <div class="workspace-card-activity" ${activityTitle ? `title="${activityTitle}"` : ''}>${activityLabel}</div>
                        </div>
                    </div>
                    <span class="modern-badge ${statusMeta.badgeClass} workspace-card-status">
                        ${statusMeta.indicatorClass ? `<span class="status-indicator ${statusMeta.indicatorClass}"></span>` : ''}
                        ${escapeHtml(statusMeta.label)}
                    </span>
                </div>

                <p class="workspace-card-description">${description}</p>
                ${tagsMarkup}

                <div class="workspace-card-metrics">
                    <div class="workspace-card-metric">
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                            <path d="M5,3H19A2,2 0 0,1 21,5V19A2,2 0 0,1 19,21H5A2,2 0 0,1 3,19V5A2,2 0 0,1 5,3M7,7V9H17V7H7M7,11V13H17V11H7M7,15V17H13V15H7Z"/>
                        </svg>
                        <span>Tasks</span>
                        <strong>${tasksCount}</strong>
                    </div>
                    <div class="workspace-card-metric">
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                            <path d="M20,2H4A2,2 0 0,0 2,4V22L6,18H20A2,2 0 0,0 22,16V4A2,2 0 0,0 20,2Z"/>
                        </svg>
                        <span>Sessions</span>
                        <strong>${sessionsCount}</strong>
                    </div>
                    <div class="workspace-card-metric">
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                            <path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M13,9V3.5L18.5,9H13M7,13H17V11H7V13M7,17H17V15H7V17Z"/>
                        </svg>
                        <span>Notes</span>
                        <strong>${notesCount}</strong>
                    </div>
                </div>

                <div class="workspace-card-actions mt-auto">
                    <button type="button" class="modern-btn modern-btn-secondary flex-grow-1" onclick="event.stopPropagation(); viewWorkspace('${workspace.id}')">
                        Dashboard
                    </button>
                    <button type="button" class="modern-btn modern-btn-primary flex-grow-1" onclick="event.stopPropagation(); openWorkspaceCanvas('${workspace.id}')">
                        Canvas
                    </button>
                    <button type="button" class="modern-btn modern-btn-danger" onclick="event.stopPropagation(); deleteWorkspace('${workspace.id}')" title="Delete Workspace" aria-label="Delete workspace ${escapeHtml(workspace.name || workspace.id)}">
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                            <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
                        </svg>
                    </button>
                </div>
            </div>
        </div>
    `;
}

// escapeHtml is provided by dom-utils.js

/**
 * Load available agents for workspace creation
 */
async function loadWorkspaceAgents() {
  try {
    const response = await fetch('/api/agents');
    const data = await response.json();

    window.availableAgents = data.agents || [];
    populateAgentDropdown();
  } catch (error) {
    console.error('Error loading workspace agents:', error);
  }
}

/**
 * Populate agent dropdown
 */
function populateAgentDropdown() {
  const select = document.getElementById('workspaceEntryAgentSelect');
  if (!select) return;
  const options = ['<option value="">Auto-create workspace manager</option>'];
  (window.availableAgents || []).forEach((agent) => {
    const name = String(agent && agent.name || '').trim();
    if (!name) return;
    options.push(`<option value="${escapeHtml(name)}">${escapeHtml(name)}</option>`);
  });
  select.innerHTML = options.join('');
}

/**
 * Delete a workspace
 */
async function deleteWorkspace(workspaceId) {
  if (!confirm('Are you sure you want to delete this workspace? This action cannot be undone.')) {
    return;
  }

  try {
    // Use unified workspace API
    const response = await fetch(`/api/workspaces/${workspaceId}`, {
      method: 'DELETE'
    });

    if (response.ok) {
      await loadWorkspaces();
    } else {
      showError('Failed to delete workspace');
    }
  } catch (error) {
    console.error('Error deleting workspace:', error);
    showError('Error deleting workspace');
  }
}

/**
 * Show error message
 */
function showError(message) {
  alert(message);
}

/**
 * Open workspace canvas view
 */
function openWorkspaceCanvas(workspaceId) {
  window.location.href = `/workspaces/${workspaceId}/canvas`;
}

/**
 * Switch between grid and canvas view
 */
function switchView(view) {
  const gridView = document.getElementById('grid-view');
  const canvasView = document.getElementById('canvas-view');

  if (view === 'canvas') {
    gridView.style.display = 'none';
    canvasView.style.display = 'block';
    populateCanvasWorkspaceSelect();
  } else {
    gridView.style.display = 'block';
    canvasView.style.display = 'none';
  }
}

/**
 * Populate canvas workspace select dropdown
 */
function populateCanvasWorkspaceSelect() {
  const select = document.getElementById('canvas-workspace-select');
  if (!select) return;

  // Use unified workspace API
  fetch('/api/workspaces')
    .then(res => res.json())
    .then(data => {
      // API returns { folders: [...] } - map to workspaces
      const workspaces = data.folders || [];
      select.innerHTML = '<option value="">Choose a workspace...</option>' +
                workspaces.map(ws => {
                  const groupLabel = String(ws.kind || '').toLowerCase() === 'group' ? ' (group)' : '';
                  return `<option value="${ws.id}">${escapeHtml(ws.name || ws.id)}${groupLabel}</option>`;
                }).join('');
    })
    .catch(err => console.error('Error loading workspaces:', err));
}

/**
 * View workspace details
 */
async function viewWorkspace(workspaceId) {
  window.location.href = `/workspaces/${workspaceId}`;
}

// Export functions for global access
// openManageAgentsModal is exported from workspace-agent-modals.js
// openCreateWorkspaceModal is exported from workspace-create.js
window.viewWorkspace = viewWorkspace;
window.deleteWorkspace = deleteWorkspace;
window.openWorkspaceCanvas = openWorkspaceCanvas;
window.switchView = switchView;
window.WorkspaceListing = window.WorkspaceListing || {};
window.WorkspaceListing.__test = {
  renderWorkspaceCard
};
// Note: escapeHtml is provided by dom-utils.js which should be loaded before this script

// Initialize on DOM ready
document.addEventListener('DOMContentLoaded', initializeWorkspacesPage);
window.addEventListener('beforeunload', cleanupWorkspacesPage);

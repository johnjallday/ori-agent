/**
 * Studios Workspace Management Module
 * Handles workspace CRUD operations, agent management, and UI interactions for the Studios page
 */

// Global state
let availableAgents = [];
let selectedAgents = new Set();
let workspaceRefreshInterval = null;

// Server connection state management
let isServerConnected = true;
let consecutiveFailures = 0;
let retryDelay = 5000; // Start with 5 seconds
const MAX_RETRY_DELAY = 60000; // Max 60 seconds between retries
const MAX_CONSECUTIVE_FAILURES = 3; // After 3 failures, show offline notification
let serverOfflineNotification = null;

// Studios-specific state for agent management
let studiosSystemAgents = [];
// studiosAvailableProviders is declared in studios-agent-modals.js

/**
 * Initialize the studios page
 */
function initializeStudiosPage() {
    loadWorkspaces();
    loadWorkspaceAgents();

    // Check URL parameters for view and workspace
    const urlParams = new URLSearchParams(window.location.search);
    const view = urlParams.get('view');
    const workspaceId = urlParams.get('workspace');

    if (view === 'canvas') {
        // Switch to canvas view
        switchView('canvas');

        // If workspace ID is provided, select it after studios are loaded
        if (workspaceId) {
            // Hide the "Select Studio:" label since studio is already selected
            const label = document.getElementById('canvas-studio-label');
            if (label) {
                label.style.display = 'none';
            }

            // Wait a bit for the select to be populated
            setTimeout(() => {
                const select = document.getElementById('canvas-studio-select');
                if (select) {
                    select.value = workspaceId;
                    loadCanvasStudio(workspaceId);
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
function cleanupStudiosPage() {
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

        console.log(`Server appears offline. Will retry in ${retryDelay/1000} seconds...`);

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
        console.log('Server connection restored');
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
    console.log('Manual retry triggered');
    hideServerOfflineNotification();

    // Show loading state
    const grid = document.getElementById('workspaces-grid');
    grid.innerHTML = `
        <div class="col-12 text-center py-5">
            <div class="spinner-border text-primary" role="status">
                <span class="visually-hidden">Reconnecting...</span>
            </div>
            <p class="mt-3" style="color: var(--text-muted);">Attempting to reconnect...</p>
        </div>
    `;

    await loadWorkspaces();
};

/**
 * Load workspaces from server
 */
async function loadWorkspaces() {
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), 10000); // 10 second timeout

    try {
        const response = await fetch('/api/orchestration/workspace', {
            signal: controller.signal
        });

        clearTimeout(timeoutId);

        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`);
        }

        const data = await response.json();

        if (data.error) {
            console.error('Server error:', data.error);
            handleConnectionFailure();
            return;
        }

        // Connection successful
        handleConnectionSuccess();
        renderWorkspaces(data.workspaces || []);

    } catch (error) {
        clearTimeout(timeoutId);

        // Silently ignore abort errors if server is connected
        if (error.name === 'AbortError' && isServerConnected) {
            console.warn('Request timed out, but server appears online');
            return;
        }

        console.error('Error loading workspaces:', error);

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

/**
 * Render workspaces grid
 */
function renderWorkspaces(workspaces) {
    const grid = document.getElementById('workspaces-grid');

    if (workspaces.length === 0) {
        grid.innerHTML = `
            <div class="col-12 text-center py-5">
                <div class="empty-state-container" style="max-width: 500px; margin: 0 auto; padding: 3rem 2rem;">
                    <div class="empty-state-icon-wrapper" style="display: inline-block; position: relative; margin-bottom: 2rem;">
                        <div style="position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%); width: 120px; height: 120px; background: radial-gradient(circle, rgba(99, 102, 241, 0.1) 0%, transparent 70%); border-radius: 50%; animation: pulse-bg 3s ease-in-out infinite;"></div>
                        <svg width="80" height="80" viewBox="0 0 24 24" fill="currentColor" style="color: var(--primary-color); position: relative; z-index: 1; opacity: 0.9;">
                            <path d="M12,5.5A3.5,3.5 0 0,1 15.5,9A3.5,3.5 0 0,1 12,12.5A3.5,3.5 0 0,1 8.5,9A3.5,3.5 0 0,1 12,5.5M5,8C5.56,8 6.08,8.15 6.53,8.42C6.38,9.85 6.8,11.27 7.66,12.38C7.16,13.34 6.16,14 5,14A3,3 0 0,1 2,11A3,3 0 0,1 5,8M19,8A3,3 0 0,1 22,11A3,3 0 0,1 19,14C17.84,14 16.84,13.34 16.34,12.38C17.2,11.27 17.62,9.85 17.47,8.42C17.92,8.15 18.44,8 19,8M5.5,18.25C5.5,16.18 8.41,14.5 12,14.5C15.59,14.5 18.5,16.18 18.5,18.25V20H5.5V18.25M0,20V18.5C0,17.11 1.89,15.94 4.45,15.6C3.86,16.28 3.5,17.22 3.5,18.25V20H0M24,20H20.5V18.25C20.5,17.22 20.14,16.28 19.55,15.6C22.11,15.94 24,17.11 24,18.5V20Z"/>
                        </svg>
                    </div>
                    <h3 style="color: var(--text-primary); font-weight: 700; margin-bottom: 1rem; font-size: 1.75rem;">Welcome to Agent Studios</h3>
                    <p style="color: var(--text-secondary); margin-bottom: 0.5rem; font-size: 1.1rem; line-height: 1.6;">Create your first workspace to unlock collaborative multi-agent capabilities</p>
                    <p style="color: var(--text-muted); font-size: 0.9rem; margin-bottom: 2rem;">Workspaces let multiple AI agents work together on complex tasks with real-time visualization</p>
                    <button class="modern-btn modern-btn-primary" onclick="openCreateWorkspaceModal()" style="padding: 0.75rem 2rem; font-size: 1rem; box-shadow: 0 4px 14px rgba(29, 78, 216, 0.3);">
                        <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                            <path d="M19,13H13V19H11V13H5V11H11V5H13V11H19V13Z"/>
                        </svg>
                        Create Your First Workspace
                    </button>
                    <div style="margin-top: 2rem; padding-top: 2rem; border-top: 1px solid var(--border-color);">
                        <p style="color: var(--text-muted); font-size: 0.85rem; margin-bottom: 0.75rem;">Quick features:</p>
                        <div style="display: flex; gap: 1.5rem; justify-content: center; flex-wrap: wrap; font-size: 0.85rem;">
                            <span style="color: var(--text-secondary);"><svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" style="vertical-align: text-bottom; color: var(--success-color);" class="me-1"><path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/></svg>Multi-agent collaboration</span>
                            <span style="color: var(--text-secondary);"><svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" style="vertical-align: text-bottom; color: var(--success-color);" class="me-1"><path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/></svg>Visual canvas</span>
                            <span style="color: var(--text-secondary);"><svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" style="vertical-align: text-bottom; color: var(--success-color);" class="me-1"><path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/></svg>Real-time updates</span>
                        </div>
                    </div>
                </div>
                <style>
                    @keyframes pulse-bg {
                        0%, 100% { transform: translate(-50%, -50%) scale(1); opacity: 0.3; }
                        50% { transform: translate(-50%, -50%) scale(1.2); opacity: 0.5; }
                    }
                    .empty-state-container {
                        animation: fadeIn 0.5s ease-in-out;
                    }
                    @keyframes fadeIn {
                        from { opacity: 0; transform: translateY(20px); }
                        to { opacity: 1; transform: translateY(0); }
                    }
                </style>
            </div>
        `;
        return;
    }

    grid.innerHTML = workspaces.map(workspace => {
        const statusBadge = workspace.status === 'active' ? 'badge-success' :
                           workspace.status === 'completed' ? 'badge-info' : 'badge-secondary';

        const isActive = workspace.status === 'active';
        const activityIndicator = isActive ? `
            <span class="d-inline-flex align-items-center gap-1 text-success" style="font-size: 0.75rem;">
                <span class="status-indicator status-online"></span>
                <span>Active</span>
            </span>
        ` : '';

        return `
            <div class="col-12 col-sm-6 col-lg-4">
                <div class="modern-card p-4 h-100 d-flex flex-column workspace-card ${isActive ? 'active-workspace' : ''}" onclick="viewWorkspace('${workspace.id}')" style="cursor: pointer; transition: all 0.2s ease;">
                    <div class="d-flex justify-content-between align-items-start mb-3">
                        <div class="flex-grow-1">
                            <h5 style="color: var(--text-primary);" class="mb-1">${escapeHtml(workspace.name || workspace.id)}</h5>
                            ${activityIndicator}
                        </div>
                        <span class="modern-badge ${statusBadge}">${escapeHtml(workspace.status || 'unknown')}</span>
                    </div>

                    ${workspace.description ? `
                        <p class="text-muted small mb-3">${escapeHtml(workspace.description)}</p>
                    ` : ''}

                    <div class="mb-3">
                        <small style="color: var(--text-secondary); font-weight: 500;">Agents: ${workspace.agents ? workspace.agents.length : 0}</small>
                    </div>

                    <div class="d-flex gap-2 mt-3">
                        <button class="modern-btn modern-btn-secondary flex-grow-1" onclick="event.stopPropagation(); viewWorkspace('${workspace.id}')">
                            View
                        </button>
                        <button class="modern-btn modern-btn-primary flex-grow-1" onclick="event.stopPropagation(); openWorkspaceCanvas('${workspace.id}')">
                            Canvas
                        </button>
                        <button class="modern-btn modern-btn-danger" onclick="event.stopPropagation(); deleteWorkspace('${workspace.id}')" title="Delete Workspace">
                            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                                <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
                            </svg>
                        </button>
                    </div>
                </div>
            </div>
        `;
    }).join('');
}

/**
 * Utility function to escape HTML
 */
function escapeHtml(text) {
    if (text == null) return '';
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

/**
 * Load available agents for workspace creation
 */
async function loadWorkspaceAgents() {
    try {
        const response = await fetch('/api/agents');
        const data = await response.json();

        availableAgents = data.agents || [];
        populateAgentDropdown();
    } catch (error) {
        console.error('Error loading workspace agents:', error);
    }
}

/**
 * Populate agent dropdown
 */
function populateAgentDropdown() {
    const select = document.getElementById('parent-agent');
    if (!select) return;
    select.innerHTML = availableAgents.map(agent =>
        `<option value="${escapeHtml(agent.name)}">${escapeHtml(agent.name)}</option>`
    ).join('');
}

/**
 * Delete a workspace
 */
async function deleteWorkspace(workspaceId) {
    if (!confirm('Are you sure you want to delete this workspace? This action cannot be undone.')) {
        return;
    }

    try {
        const response = await fetch(`/api/orchestration/workspace/${workspaceId}`, {
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
    window.location.href = `/studios?view=canvas&workspace=${workspaceId}`;
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
        populateCanvasStudioSelect();
    } else {
        gridView.style.display = 'block';
        canvasView.style.display = 'none';
    }
}

/**
 * Populate canvas studio select dropdown
 */
function populateCanvasStudioSelect() {
    const select = document.getElementById('canvas-studio-select');
    if (!select) return;

    fetch('/api/orchestration/workspace')
        .then(res => res.json())
        .then(data => {
            const workspaces = data.workspaces || [];
            select.innerHTML = '<option value="">Choose a studio...</option>' +
                workspaces.map(ws => `<option value="${ws.id}">${escapeHtml(ws.name || ws.id)}</option>`).join('');
        })
        .catch(err => console.error('Error loading studios:', err));
}

/**
 * View workspace details
 */
async function viewWorkspace(workspaceId) {
    window.location.href = `/studios/${workspaceId}`;
}

// Export functions for global access
// openManageAgentsModal is exported from studios-agent-modals.js
// openCreateWorkspaceModal is exported from studios-workspace-create.js
window.viewWorkspace = viewWorkspace;
window.deleteWorkspace = deleteWorkspace;
window.openWorkspaceCanvas = openWorkspaceCanvas;
window.switchView = switchView;
window.escapeHtml = escapeHtml;

// Initialize on DOM ready
document.addEventListener('DOMContentLoaded', initializeStudiosPage);
window.addEventListener('beforeunload', cleanupStudiosPage);

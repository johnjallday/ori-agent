// Plugin Update Checker Module
// Checks for plugin updates and shows notifications

let pluginUpdateInterval = null;
let cachedPluginUpdates = null;

// Initialize plugin update checker
function initPluginUpdateChecker() {
  // Check immediately on load
  checkForPluginUpdates();

  // Check every 30 minutes
  pluginUpdateInterval = setInterval(checkForPluginUpdates, 30 * 60 * 1000);
}

// Check for available plugin updates
async function checkForPluginUpdates() {
  try {
    const response = await fetch('/api/plugins/updates/check');
    if (!response.ok) {
      console.error('Failed to check for plugin updates:', response.status);
      return null;
    }

    const data = await response.json();
    cachedPluginUpdates = data;

    if (data.success && data.updatesCount > 0) {
      console.log(`Found ${data.updatesCount} plugin update(s) available`);
      // Update plugin list UI if visible
      if (typeof refreshPluginList === 'function') {
        refreshPluginList();
      }
    }

    return data;
  } catch (error) {
    console.error('Error checking for plugin updates:', error);
    return null;
  }
}

// Show plugin update modal with list of updates
async function showPluginUpdatesModal() {
  const modal = new bootstrap.Modal(document.getElementById('pluginUpdatesModal'));
  modal.show();

  // Use cached data or fetch fresh
  let updateData = cachedPluginUpdates;
  if (!updateData || !updateData.success) {
    updateData = await checkForPluginUpdates();
  }

  displayPluginUpdates(updateData);
}

// Display plugin update information
function displayPluginUpdates(updateData) {
  const modalBody = document.getElementById('pluginUpdatesModalBody');

  if (!updateData || !updateData.success) {
    modalBody.innerHTML = `
      <div class="alert alert-danger" role="alert">
        Failed to check for plugin updates. Please try again later.
      </div>
    `;
    return;
  }

  if (updateData.updatesCount === 0) {
    modalBody.innerHTML = `
      <div class="alert alert-success" role="alert">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="me-2">
          <path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/>
        </svg>
        All plugins are up to date!
      </div>
    `;
    return;
  }

  // Display list of available updates
  const updatesHTML = updateData.updates.map(update => `
    <div class="plugin-update-item mb-3 p-3" style="border: 1px solid var(--border-color); border-radius: 8px; background: var(--bg-secondary);">
      <div class="d-flex justify-content-between align-items-start">
        <div class="flex-grow-1">
          <h6 class="mb-2" style="color: var(--text-primary);">${update.name}</h6>
          <p class="text-muted small mb-2">${update.description || ''}</p>
          <div class="d-flex align-items-center gap-3">
            <div>
              <small class="text-muted">Current:</small>
              <span class="badge bg-secondary ms-1">${update.currentVersion || 'unknown'}</span>
            </div>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" style="color: var(--primary-color);">
              <path d="M8.59,16.58L13.17,12L8.59,7.41L10,6L16,12L10,18L8.59,16.58Z"/>
            </svg>
            <div>
              <small class="text-muted">Latest:</small>
              <span class="badge bg-success ms-1">${update.latestVersion}</span>
            </div>
          </div>
          ${update.githubRepo ? `
            <div class="mt-2">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1" style="color: var(--text-secondary);">
                <path d="M12,2A10,10 0 0,0 2,12C2,16.42 4.87,20.17 8.84,21.5C9.34,21.58 9.5,21.27 9.5,21C9.5,20.77 9.5,20.14 9.5,19.31C6.73,19.91 6.14,17.97 6.14,17.97C5.68,16.81 5.03,16.5 5.03,16.5C4.12,15.88 5.1,15.9 5.1,15.9C6.1,15.97 6.63,16.93 6.63,16.93C7.5,18.45 8.97,18 9.54,17.76C9.63,17.11 9.89,16.67 10.17,16.42C7.95,16.17 5.62,15.31 5.62,11.5C5.62,10.39 6,9.5 6.65,8.79C6.55,8.54 6.2,7.5 6.75,6.15C6.75,6.15 7.59,5.88 9.5,7.17C10.29,6.95 11.15,6.84 12,6.84C12.85,6.84 13.71,6.95 14.5,7.17C16.41,5.88 17.25,6.15 17.25,6.15C17.8,7.5 17.45,8.54 17.35,8.79C18,9.5 18.38,10.39 18.38,11.5C18.38,15.32 16.04,16.16 13.81,16.41C14.17,16.72 14.5,17.33 14.5,18.26C14.5,19.6 14.5,20.68 14.5,21C14.5,21.27 14.66,21.59 15.17,21.5C19.14,20.16 22,16.42 22,12A10,10 0 0,0 12,2Z"/>
              </svg>
              <a href="${update.githubRepo}" target="_blank" class="text-decoration-none small" style="color: var(--text-secondary);">
                ${update.githubRepo.replace('https://github.com/', '')}
              </a>
            </div>
          ` : ''}
        </div>
        <div class="ms-3">
          <button class="modern-btn modern-btn-primary" onclick="updatePlugin('${update.name}', '${update.downloadURL}')">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
              <path d="M5,20H19V18H5M19,9H15V3H9V9H5L12,16L19,9Z"/>
            </svg>
            Update
          </button>
        </div>
      </div>
      <div id="update-status-${update.name.replace(/[^a-zA-Z0-9]/g, '_')}" class="mt-2"></div>
    </div>
  `).join('');

  modalBody.innerHTML = `
    <div class="mb-3">
      <div class="alert alert-info" role="alert">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="me-2">
          <path d="M13,9H11V7H13M13,17H11V11H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z"/>
        </svg>
        <strong>${updateData.updatesCount} update(s) available</strong>
      </div>
    </div>
    ${updatesHTML}
  `;
}

// Update a specific plugin
async function updatePlugin(pluginName, downloadURL) {
  const statusDiv = document.getElementById(`update-status-${pluginName.replace(/[^a-zA-Z0-9]/g, '_')}`);

  try {
    statusDiv.innerHTML = `
      <div class="alert alert-info mb-0" role="alert">
        <div class="d-flex align-items-center">
          <div class="spinner-border spinner-border-sm me-2" role="status"></div>
          <div>Downloading update...</div>
        </div>
      </div>
    `;

    const response = await fetch('/api/plugins/download', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        name: pluginName
      })
    });

    const result = await response.json();

    if (!response.ok || !result.success) {
      throw new Error(result.message || 'Failed to update plugin');
    }

    // Success
    statusDiv.innerHTML = `
      <div class="alert alert-success mb-0" role="alert">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
          <path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/>
        </svg>
        <small>Updated successfully! Reload the plugin to use the new version.</small>
      </div>
    `;

    // Refresh plugin list after update
    if (typeof loadPlugins === 'function') {
      setTimeout(loadPlugins, 1000);
    }

    // Refresh updates list
    setTimeout(checkForPluginUpdates, 2000);

  } catch (error) {
    console.error('Update error:', error);
    statusDiv.innerHTML = `
      <div class="alert alert-danger mb-0" role="alert">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
          <path d="M13,14H11V10H13M13,18H11V16H13M1,21H23L12,2L1,21Z"/>
        </svg>
        <small>Update failed: ${error.message}</small>
      </div>
    `;
  }
}

// Make functions globally available
window.initPluginUpdateChecker = initPluginUpdateChecker;
window.checkForPluginUpdates = checkForPluginUpdates;
window.showPluginUpdatesModal = showPluginUpdatesModal;
window.updatePlugin = updatePlugin;

// Auto-initialize on page load
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initPluginUpdateChecker);
} else {
  initPluginUpdateChecker();
}

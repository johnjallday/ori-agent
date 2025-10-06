// Plugin Store Module
// Handles plugin store functionality including loading and installing online plugins

// Plugin store modal functions
async function showPluginStoreModal() {
  const modal = new bootstrap.Modal(document.getElementById('pluginStoreModal'));
  modal.show();

  // Load online plugins when modal opens
  await loadOnlinePlugins();
}

async function loadOnlinePlugins() {
  try {
    const response = await fetch('/api/plugin-registry');
    if (!response.ok) {
      throw new Error('Failed to fetch plugin registry');
    }

    const data = await response.json();

    // Filter for online plugins (ones with github_repo)
    const onlinePlugins = data.plugins.filter(plugin => plugin.github_repo);

    // Get list of actually installed local plugins (ones without github_repo)
    const localPlugins = data.plugins.filter(plugin => !plugin.github_repo);
    const installedPluginNames = new Set(localPlugins.map(p => p.name));

    displayOnlinePlugins(onlinePlugins, installedPluginNames);
  } catch (error) {
    console.error('Error loading online plugins:', error);

    const onlinePluginsList = document.getElementById('onlinePluginsList');
    if (onlinePluginsList) {
      onlinePluginsList.innerHTML = `
        <div class="alert alert-danger" role="alert">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
            <path d="M12,2L13.09,8.26L22,9L13.09,9.74L12,16L10.91,9.74L2,9L10.91,8.26L12,2Z"/>
          </svg>
          Failed to load online plugins: ${error.message}
        </div>
      `;
    }
  }
}

function displayOnlinePlugins(onlinePlugins, installedPluginNames = new Set()) {
  const onlinePluginsList = document.getElementById('onlinePluginsList');
  if (!onlinePluginsList) return;

  if (onlinePlugins.length === 0) {
    onlinePluginsList.innerHTML = `
      <div class="text-center py-4" style="color: var(--text-secondary);">
        <svg width="48" height="48" viewBox="0 0 24 24" fill="currentColor" class="mb-3">
          <path d="M20.5,11H19V7C19,5.89 18.1,5 17,5H13V3.5A2.5,2.5 0 0,0 10.5,1A2.5,2.5 0 0,0 8,3.5V5H4C2.89,5 2,5.89 2,7V10.8H3.5C5,10.8 6.2,12 6.2,13.5C6.2,15 5,16.2 3.5,16.2H2V20C2,21.11 2.89,22 4,22H7.8V20.5C7.8,19 9,17.8 10.5,17.8C12,17.8 13.2,19 13.2,20.5V22H17C18.11,22 19,21.11 19,20V16H20.5A2.5,2.5 0 0,0 23,13.5A2.5,2.5 0 0,0 20.5,11Z"/>
        </svg>
        <p>No online plugins available</p>
      </div>
    `;
    return;
  }

  onlinePluginsList.innerHTML = onlinePlugins.map(plugin => {
    const isInstalled = installedPluginNames.has(plugin.name);
    const githubUrl = plugin.github_repo;
    const version = plugin.version || 'Unknown';

    return `
      <div class="online-plugin-item mb-3 p-3" style="border: 1px solid var(--border-color); border-radius: 8px; background: var(--bg-secondary);">
        <div class="d-flex justify-content-between align-items-start">
          <div class="flex-grow-1">
            <div class="d-flex align-items-center mb-2">
              <h6 class="mb-0 me-2" style="color: var(--text-primary);">${plugin.name}</h6>
              <span class="badge" style="background: var(--accent-color); color: white;">v${version}</span>
              ${isInstalled ? '<span class="badge bg-success ms-2">Installed</span>' : ''}
            </div>
            <p class="text-muted small mb-2">${plugin.description}</p>
            <div class="d-flex align-items-center">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1" style="color: var(--text-secondary);">
                <path d="M12,2A10,10 0 0,0 2,12C2,16.42 4.87,20.17 8.84,21.5C9.34,21.58 9.5,21.27 9.5,21C9.5,20.77 9.5,20.14 9.5,19.31C6.73,19.91 6.14,17.97 6.14,17.97C5.68,16.81 5.03,16.5 5.03,16.5C4.12,15.88 5.1,15.9 5.1,15.9C6.1,15.97 6.63,16.93 6.63,16.93C7.5,18.45 8.97,18 9.54,17.76C9.63,17.11 9.89,16.67 10.17,16.42C7.95,16.17 5.62,15.31 5.62,11.5C5.62,10.39 6,9.5 6.65,8.79C6.55,8.54 6.2,7.5 6.75,6.15C6.75,6.15 7.59,5.88 9.5,7.17C10.29,6.95 11.15,6.84 12,6.84C12.85,6.84 13.71,6.95 14.5,7.17C16.41,5.88 17.25,6.15 17.25,6.15C17.8,7.5 17.45,8.54 17.35,8.79C18,9.5 18.38,10.39 18.38,11.5C18.38,15.32 16.04,16.16 13.81,16.41C14.17,16.72 14.5,17.33 14.5,18.26C14.5,19.6 14.5,20.68 14.5,21C14.5,21.27 14.66,21.59 15.17,21.5C19.14,20.16 22,16.42 22,12A10,10 0 0,0 12,2Z"/>
              </svg>
              <a href="${githubUrl}" target="_blank" class="text-decoration-none small" style="color: var(--text-secondary);">
                ${githubUrl.replace('https://github.com/', '')}
              </a>
            </div>
          </div>
          <div class="ms-3">
            ${isInstalled ?
              `<button class="modern-btn modern-btn-secondary" disabled>
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/>
                </svg>
                Installed
              </button>` :
              `<button class="modern-btn modern-btn-primary" onclick="installOnlinePlugin('${plugin.name}', '${plugin.download_url}')">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                  <path d="M5,20H19V18H5M19,9H15V3H9V9H5L12,16L19,9Z"/>
                </svg>
                Install
              </button>`
            }
          </div>
        </div>
      </div>
    `;
  }).join('');
}

async function installOnlinePlugin(pluginName, downloadUrl) {
  const button = event.target;
  const originalText = button.innerHTML;

  try {
    console.log('Installing plugin:', pluginName);

    // Show loading state
    button.disabled = true;
    button.innerHTML = `
      <div class="spinner-border spinner-border-sm me-1" role="status">
        <span class="visually-hidden">Loading...</span>
      </div>
      Installing...
    `;

    console.log('Sending request to /api/plugins/download with name:', pluginName);

    const response = await fetch('/api/plugins/download', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        name: pluginName
      })
    });

    console.log('Response status:', response.status);

    const result = await response.json();
    console.log('Response result:', result);

    if (!response.ok || !result.success) {
      throw new Error(result.message || 'Failed to install plugin');
    }

    // Success - update button state
    button.innerHTML = `
      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
        <path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/>
      </svg>
      Installed
    `;
    button.className = 'modern-btn modern-btn-secondary';

    // Refresh plugins in sidebar - call loadPlugins from main plugins module
    if (typeof loadPlugins === 'function') {
      await loadPlugins();
    }

    console.log(`Plugin ${pluginName} installed successfully`);

  } catch (error) {
    console.error('Error installing plugin:', error);

    // Reset button state
    button.disabled = false;
    button.innerHTML = originalText;

    // Show error message
    alert(`Failed to install plugin: ${error.message}`);
  }
}

// Make functions globally available
window.showPluginStoreModal = showPluginStoreModal;
window.loadOnlinePlugins = loadOnlinePlugins;
window.displayOnlinePlugins = displayOnlinePlugins;
window.installOnlinePlugin = installOnlinePlugin;
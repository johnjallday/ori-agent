// Plugin Management Module
// Handles all plugin-related functionality including loading, toggling, upload, and online installation

// Plugin upload state management
let uploadListenersSetup = false;

// Plugin Management Functions

// Load available plugins
async function loadPlugins() {
  try {
    // Fetch all available plugins from registry
    const registryResponse = await fetch('/api/plugin-registry');
    if (!registryResponse.ok) {
      throw new Error('Failed to fetch plugin registry');
    }
    const registry = await registryResponse.json();
    
    // Fetch currently loaded plugins for this agent
    const activeResponse = await fetch('/api/plugins');
    if (!activeResponse.ok) {
      throw new Error('Failed to fetch active plugins');
    }
    const activePlugins = await activeResponse.json();
    
    // Create a set of active plugin names for quick lookup
    const activePluginNames = new Set(activePlugins.plugins.map(p => p.name));
    
    // Filter to only show local plugins in sidebar (those without github_repo)
    const localPlugins = registry.plugins.filter(plugin => !plugin.github_repo);
    
    displayPlugins(localPlugins, activePluginNames);
  } catch (error) {
    console.error('Error loading plugins:', error);
    const pluginsList = document.getElementById('pluginsList');
    if (pluginsList) {
      pluginsList.innerHTML = '<div class="text-danger small">Failed to load plugins</div>';
    }
  }
}

// Display plugins in the sidebar
function displayPlugins(plugins, activePluginNames) {
  const pluginsList = document.getElementById('pluginsList');
  if (!pluginsList) return;
  
  if (plugins.length === 0) {
    pluginsList.innerHTML = '<div class="text-muted small">No plugins available</div>';
    return;
  }
  
  pluginsList.innerHTML = plugins.map(plugin => {
    const isActive = activePluginNames.has(plugin.name);
    const pluginPath = plugin.path || '';
    const isUploaded = pluginPath.includes('uploaded_plugins') && !plugin.github_repo;
    
    return `
      <div class="plugin-item">
        <div class="d-flex align-items-center justify-content-between">
          <div>
            <div class="fw-medium d-flex align-items-center" style="color: var(--text-primary);">
              ${plugin.name}
              ${isUploaded ? '<span class="badge badge-success ms-2" style="font-size: 0.7em;">Local</span>' : ''}
            </div>
            <div class="text-muted small">${plugin.description || 'No description available'}</div>
            ${plugin.version ? `<div class="text-muted" style="font-size: 0.7em;">v${plugin.version}</div>` : ''}
          </div>
          <div class="d-flex align-items-center">
            ${isUploaded ? `
              <button class="btn btn-sm btn-outline-danger me-2 plugin-remove-btn" 
                      data-plugin-name="${plugin.name}" 
                      data-plugin-path="${plugin.path}"
                      title="Remove plugin">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
                </svg>
              </button>
            ` : ''}
            <div class="form-check form-switch">
              <input class="form-check-input plugin-toggle" type="checkbox" 
                     data-plugin-name="${plugin.name}" 
                     data-plugin-path="${plugin.path}"
                     ${isActive ? 'checked' : ''}>
            </div>
          </div>
        </div>
      </div>
    `;
  }).join('');
  
  // Add event listeners to plugin toggles
  setupPluginToggles();
}

// Setup plugin toggle event listeners
function setupPluginToggles() {
  // Setup plugin toggle switches
  const toggles = document.querySelectorAll('.plugin-toggle');
  toggles.forEach(toggle => {
    toggle.addEventListener('change', async (e) => {
      const pluginName = e.target.dataset.pluginName;
      const pluginPath = e.target.dataset.pluginPath;
      const isEnabled = e.target.checked;
      
      console.log(`Toggle event: ${pluginName}, isEnabled: ${isEnabled}`);
      
      try {
        await togglePlugin(pluginName, pluginPath, isEnabled);
      } catch (error) {
        console.error('Failed to toggle plugin:', error);
        // Revert the toggle state
        e.target.checked = !isEnabled;
        
        // If we were trying to disable and it failed, still refresh to ensure clean state
        if (!isEnabled) {
          console.log('Plugin disable failed but refreshing anyway to ensure clean state');
          window.location.reload();
          return;
        }
        
        alert(`Failed to ${isEnabled ? 'enable' : 'disable'} plugin: ${error.message}`);
      }
    });
  });
  
  // Setup plugin remove buttons
  const removeButtons = document.querySelectorAll('.plugin-remove-btn');
  removeButtons.forEach(button => {
    button.addEventListener('click', async (e) => {
      const pluginName = e.target.closest('button').dataset.pluginName;
      const pluginPath = e.target.closest('button').dataset.pluginPath;
      
      // Confirm removal
      if (!confirm(`Are you sure you want to remove the plugin "${pluginName}"? This action cannot be undone.`)) {
        return;
      }
      
      try {
        await removePlugin(pluginName, pluginPath);
        // Refresh plugins list
        await loadPlugins();
      } catch (error) {
        console.error('Failed to remove plugin:', error);
        alert(`Failed to remove plugin: ${error.message}`);
      }
    });
  });
}

// Toggle plugin on/off
async function togglePlugin(pluginName, pluginPath, enable) {
  console.log(`togglePlugin called: ${pluginName}, enable: ${enable}`);
  
  if (enable) {
    // For enabling, check if the file needs to be renamed back from .disabled
    const disabledPath = pluginPath + '.disabled';
    
    // Try to enable the plugin first
    const enableResponse = await fetch('/api/plugins', {
      method: 'POST',
      body: (() => {
        const formData = new FormData();
        formData.append('name', pluginName);
        formData.append('path', pluginPath);
        return formData;
      })()
    });
    
    if (!enableResponse.ok) {
      const errorText = await enableResponse.text();
      // If it failed because the file doesn't exist, try to restore from .disabled
      if (errorText.includes('realpath failed') || errorText.includes('no such file')) {
        console.log('Plugin file not found, attempting to restore from .disabled version');
        // Call a new API endpoint to restore the plugin file
        const restoreResponse = await fetch('/api/plugins/restore', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: pluginName, path: pluginPath })
        });
        
        if (!restoreResponse.ok) {
          const restoreError = await restoreResponse.text();
          throw new Error(`Failed to restore plugin file: ${restoreError}`);
        }
        
        // Now try to enable again
        const retryResponse = await fetch('/api/plugins', {
          method: 'POST',
          body: (() => {
            const formData = new FormData();
            formData.append('name', pluginName);
            formData.append('path', pluginPath);
            return formData;
          })()
        });
        
        if (!retryResponse.ok) {
          const retryError = await retryResponse.text();
          throw new Error(`Failed to enable plugin after restore: ${retryError}`);
        }
      } else {
        throw new Error(errorText || 'Failed to enable plugin');
      }
    }
    
    console.log(`Plugin ${pluginName} enabled successfully`);
    
  } else {
    // For disabling, first unload from cache
    const unloadResponse = await fetch(`/api/plugins?name=${encodeURIComponent(pluginName)}`, {
      method: 'DELETE'
    });
    
    if (!unloadResponse.ok) {
      console.warn('Failed to unload plugin from cache, but continuing with disable');
    }
    
    // Show immediate notification that requires server restart
    const notification = document.createElement('div');
    notification.style.cssText = `
      position: fixed; top: 50%; left: 50%; transform: translate(-50%, -50%); z-index: 10000;
      background: var(--warning-color, #ff6b35); color: white; padding: 20px 30px;
      border-radius: 12px; box-shadow: 0 8px 24px rgba(0,0,0,0.4);
      font-weight: 500; max-width: 400px; text-align: center;
      border: 3px solid #ff8c5a;
    `;
    notification.innerHTML = `
      <div style="font-size: 18px; margin-bottom: 10px;">⚠️ Plugin Disable Required</div>
      <div style="margin-bottom: 15px;">
        To fully disable "${pluginName}", the server must be restarted.<br>
        The plugin will remain active until then.
      </div>
      <button id="restartServerBtn" style="
        background: white; color: #ff6b35; border: none; padding: 8px 16px;
        border-radius: 6px; font-weight: bold; cursor: pointer; margin-right: 10px;
      ">Restart Server</button>
      <button id="cancelDisableBtn" style="
        background: transparent; color: white; border: 1px solid white;
        padding: 8px 16px; border-radius: 6px; cursor: pointer;
      ">Cancel</button>
    `;
    document.body.appendChild(notification);
    
    // Handle restart button
    document.getElementById('restartServerBtn').onclick = () => {
      // Show restarting message
      notification.innerHTML = `
        <div style="font-size: 16px;">🔄 Restarting server...</div>
        <div style="margin-top: 10px; font-size: 14px;">Page will reload automatically</div>
      `;
      
      // Simulate server restart by reloading after a delay
      setTimeout(() => {
        window.location.reload();
      }, 2000);
    };
    
    // Handle cancel button
    document.getElementById('cancelDisableBtn').onclick = () => {
      notification.remove();
      // Revert the toggle state since we're canceling
      const toggle = document.querySelector(`[data-plugin-name="${pluginName}"]`);
      if (toggle) {
        toggle.checked = true; // Keep it enabled
      }
    };
    
    console.log(`Plugin ${pluginName} disable initiated - user must restart server`);
  }
}

// Remove uploaded plugin
async function removePlugin(pluginName, pluginPath) {
  const response = await fetch(`/api/plugin-registry?name=${encodeURIComponent(pluginName)}`, {
    method: 'DELETE'
  });
  
  if (!response.ok) {
    const errorText = await response.text();
    throw new Error(errorText || 'Failed to remove plugin');
  }
  
  console.log(`Plugin ${pluginName} removed successfully from registry and filesystem`);
}

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
  try {
    const button = event.target;
    const originalText = button.innerHTML;
    
    // Show loading state
    button.disabled = true;
    button.innerHTML = `
      <div class="spinner-border spinner-border-sm me-1" role="status">
        <span class="visually-hidden">Loading...</span>
      </div>
      Installing...
    `;
    
    const response = await fetch('/api/plugins/install', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        name: pluginName,
        download_url: downloadUrl
      })
    });
    
    if (!response.ok) {
      const errorText = await response.text();
      throw new Error(errorText || 'Failed to install plugin');
    }
    
    // Success - update button state
    button.innerHTML = `
      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
        <path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/>
      </svg>
      Installed
    `;
    button.className = 'modern-btn modern-btn-secondary';
    
    // Refresh plugins in sidebar
    await loadPlugins();
    
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

// Plugin upload modal functions
function showPluginUploadModal() {
  const modal = new bootstrap.Modal(document.getElementById('pluginUploadModal'));
  modal.show();
  
  // Setup drag and drop and file input listeners only once
  if (!uploadListenersSetup) {
    setupUploadListeners();
    uploadListenersSetup = true;
  }
}

function setupUploadListeners() {
  const dropZone = document.getElementById('uploadDropZone');
  const fileInput = document.getElementById('pluginFileInput');
  
  if (!dropZone || !fileInput) return;
  
  // Drag and drop events
  dropZone.addEventListener('dragover', handleDragOver);
  dropZone.addEventListener('dragleave', handleDragLeave);
  dropZone.addEventListener('drop', handleDrop);
  dropZone.addEventListener('click', handleDropZoneClick);
  fileInput.addEventListener('change', handleFileInputChange);
}

function handleDragOver(e) {
  e.preventDefault();
  const dropZone = document.getElementById('uploadDropZone');
  dropZone.style.borderColor = 'var(--accent-color)';
  dropZone.style.backgroundColor = 'var(--bg-hover)';
}

function handleDragLeave(e) {
  e.preventDefault();
  const dropZone = document.getElementById('uploadDropZone');
  dropZone.style.borderColor = 'var(--border-color)';
  dropZone.style.backgroundColor = 'var(--bg-secondary)';
}

function handleDrop(e) {
  e.preventDefault();
  const dropZone = document.getElementById('uploadDropZone');
  dropZone.style.borderColor = 'var(--border-color)';
  dropZone.style.backgroundColor = 'var(--bg-secondary)';
  
  const files = e.dataTransfer.files;
  if (files.length > 0) {
    handlePluginFile(files[0]);
  }
}

function handleDropZoneClick(e) {
  // Only trigger file input if clicking directly on the drop zone, not on the button
  if (e.target.tagName === 'BUTTON' || e.target.closest('button')) {
    return;
  }
  
  const fileInput = document.getElementById('pluginFileInput');
  fileInput.click();
}

function handleFileInputChange(e) {
  if (e.target.files.length > 0) {
    handlePluginFile(e.target.files[0]);
  }
}

function handlePluginFile(file) {
  console.log('File selected:', file.name);
  
  // Validate file type
  if (!file.name.endsWith('.so')) {
    showUploadResult('error', 'Please select a valid plugin file (.so)');
    return;
  }
  
  // Validate file size (e.g., max 50MB)
  const maxSize = 50 * 1024 * 1024; // 50MB
  if (file.size > maxSize) {
    showUploadResult('error', 'File size too large. Maximum size is 50MB.');
    return;
  }
  
  uploadPluginFile(file);
}

async function uploadPluginFile(file) {
  const formData = new FormData();
  formData.append('plugin', file);
  
  const progressDiv = document.getElementById('uploadProgress');
  const progressBar = document.getElementById('uploadProgressBar');
  const progressPercent = document.getElementById('uploadPercent');
  const resultDiv = document.getElementById('uploadResult');
  
  // Show progress, hide result
  progressDiv.classList.remove('d-none');
  resultDiv.classList.add('d-none');
  
  try {
    const xhr = new XMLHttpRequest();
    
    // Track upload progress
    xhr.upload.addEventListener('progress', (e) => {
      if (e.lengthComputable) {
        const percentComplete = (e.loaded / e.total) * 100;
        progressBar.style.width = percentComplete + '%';
        progressBar.setAttribute('aria-valuenow', percentComplete);
        progressPercent.textContent = Math.round(percentComplete) + '%';
      }
    });
    
    // Handle completion
    xhr.addEventListener('load', () => {
      progressDiv.classList.add('d-none');
      
      if (xhr.status === 200) {
        showUploadResult('success', 'Plugin uploaded successfully!');
        
        // Refresh plugins in sidebar
        setTimeout(() => {
          loadPlugins();
        }, 1000);
        
        // Reset file input
        document.getElementById('pluginFileInput').value = '';
        
      } else {
        let errorMessage = 'Upload failed';
        try {
          const response = JSON.parse(xhr.responseText);
          errorMessage = response.error || errorMessage;
        } catch (e) {
          errorMessage = xhr.responseText || errorMessage;
        }
        showUploadResult('error', errorMessage);
      }
    });
    
    xhr.addEventListener('error', () => {
      progressDiv.classList.add('d-none');
      showUploadResult('error', 'Network error occurred during upload');
    });
    
    // Start upload
    xhr.open('POST', '/api/plugins/upload');
    xhr.send(formData);
    
  } catch (error) {
    progressDiv.classList.add('d-none');
    showUploadResult('error', 'Upload failed: ' + error.message);
  }
}

function showUploadResult(type, message) {
  const resultDiv = document.getElementById('uploadResult');
  if (!resultDiv) return;
  
  const isSuccess = type === 'success';
  const iconPath = isSuccess 
    ? 'M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z'
    : 'M13,13H11V7H13M13,17H11V15H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z';
  
  resultDiv.innerHTML = `
    <div class="alert alert-${isSuccess ? 'success' : 'danger'}" role="alert">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
        <path d="${iconPath}"/>
      </svg>
      ${message}
    </div>
  `;
  
  resultDiv.classList.remove('d-none');
}

// Setup plugin management event listeners
function setupPluginManagement() {
  // Plugin management buttons
  const browsePluginsBtn = document.getElementById('browsePluginsBtn');
  if (browsePluginsBtn) {
    browsePluginsBtn.addEventListener('click', () => {
      console.log('Browse plugins clicked');
      showPluginUploadModal();
    });
  }

  const pluginStoreBtn = document.getElementById('pluginStoreBtn');
  if (pluginStoreBtn) {
    pluginStoreBtn.addEventListener('click', () => {
      console.log('Plugin store clicked');
      showPluginStoreModal();
    });
  }

  console.log('Plugin management setup complete');
}

// Initialize plugin management when DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', setupPluginManagement);
} else {
  setupPluginManagement();
}